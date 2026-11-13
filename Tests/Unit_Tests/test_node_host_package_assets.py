import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "Tests/tools"))
spec = importlib.util.spec_from_file_location("node_host_packages", ROOT / "Tests/tools/build_node_host_package_assets.py")
packages = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packages)
sys.path.pop(0)


def fixture_plan(root):
    cache = root / "archives"
    cache.mkdir()
    plan = dict(version=1, os="ubuntu", os_version="24.04", suite="noble", platform="linux-amd64", packages=[])
    for name, phase in sorted(packages.ROOTS.items()):
        file = f"{name}_1.0_all.deb"
        # Opaque inert bytes exercise archive/pin contracts. Native package
        # metadata for real pins is separately measured during lock review.
        data = ("fixture " + name).encode()
        (cache / file).write_bytes(data)
        plan["packages"].append(dict(name=name, version="1.0", architecture="all", file=file, phase=phase,
                                     size=len(data), sha256=hashlib.sha256(data).hexdigest(), file_bytes=13, entries=2,
                                     url=f"https://archive.ubuntu.com/ubuntu/pool/main/f/fixture/{file}"))
    return plan, cache


def wire(plan):
    return json.dumps(plan, sort_keys=True, indent=2).encode() + b"\n"


class NodeHostPackageAssetsTests(unittest.TestCase):
    def test_locked_download_bounds_origins_and_error_redaction(self):
        data = b"pinned package bytes"
        pin = dict(name="python3", version="1.0", architecture="all", file="python3_1.0_all.deb", size=len(data),
                   sha256=hashlib.sha256(data).hexdigest(), url="https://archive.ubuntu.com/ubuntu/pool/main/p/python3/python3_1.0_all.deb")
        for mode in ("valid", "short", "long", "hash", "encoding", "length", "remote error", "deadline"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                value = dict(pin)
                if mode == "hash": value["sha256"] = "a" * 64
                response = io.BytesIO(data[:-1] if mode == "short" else data + b"x" if mode == "long" else data)
                response.status, response.headers = 200, {}
                if mode == "encoding": response.headers["Content-Encoding"] = "gzip"
                if mode == "length": response.headers["Content-Length"] = str(len(data) + 1)
                opener = mock.Mock()
                opener.open.return_value = response
                if mode == "remote error": opener.open.side_effect = RuntimeError("private upstream diagnostics")
                with mock.patch.object(packages.urllib.request, "build_opener", return_value=opener) as factory, \
                        mock.patch.object(packages.time, "monotonic", side_effect=[0, 301] if mode == "deadline" else None, return_value=0):
                    destination = Path(temporary) / "archive"
                    if mode == "valid":
                        packages.download(value, destination)
                        self.assertEqual(destination.read_bytes(), data)
                        self.assertEqual(factory.call_args.args[0].proxies, {})
                        self.assertIsInstance(factory.call_args.args[1], packages.NoRedirect)
                    else:
                        with self.assertRaisesRegex(ValueError, "^pinned host package download failed$"):
                            packages.download(value, destination)
        for url in (pin["url"].replace("https:", "http:"), pin["url"].replace(".com/", ".com.evil/"),
                    pin["url"].replace("https://", "https://user:password@"), pin["url"] + "?token=value", pin["url"] + "#fragment",
                    pin["url"].replace("/pool/", "/%70ool/"), pin["url"].replace("/python3/", "/../")):
            self.assertFalse(packages.valid_url(dict(pin, url=url)))
        with self.assertRaises(ValueError):
            packages.NoRedirect().redirect_request(urllib.request.Request(pin["url"]), None, 302, "", {}, pin["url"])

    def test_reviewed_lock_and_strict_boundaries(self):
        original = (ROOT / packages.LOCK).read_bytes()
        plan, digest = packages.parse_plan(original)
        self.assertEqual(digest, hashlib.sha256(original).hexdigest())
        self.assertEqual(len(plan["packages"]), 82)
        for mode in ("no newline", "extra newline", "suite", "architecture", "phase", "missing root", "order", "path",
                     "duplicate file", "zero digest", "size", "boolean", "extra field", "alias", "duplicate", "null"):
            with self.subTest(mode=mode):
                value = copy.deepcopy(plan)
                first = value["packages"][0]
                if mode == "suite": value["suite"] = "other"
                elif mode == "architecture": first["architecture"] = "arm64"
                elif mode == "phase": next(p for p in value["packages"] if p["name"] == "docker-ce")["phase"] = "base"
                elif mode == "missing root": value["packages"] = [p for p in value["packages"] if p["name"] != "python3"]
                elif mode == "order": value["packages"].reverse()
                elif mode == "path": first["file"] = "../escape.deb"
                elif mode == "duplicate file": value["packages"][1]["file"] = first["file"]
                elif mode == "zero digest": first["sha256"] = "0" * 64
                elif mode == "size": first["size"] = packages.MAX_ARCHIVE + 1
                elif mode == "boolean": first["entries"] = True
                elif mode == "extra field": first["extra"] = 1
                elif mode == "null": first["file_bytes"] = None
                raw = wire(value)
                if mode == "no newline": raw = raw[:-1]
                elif mode == "extra newline": raw += b"\n"
                elif mode == "alias": raw = raw.replace(b'"size":', b'"Size":', 1)
                elif mode == "duplicate": raw = raw.replace(b'"size":', b'"size": 1, "size":', 1)
                with self.assertRaises(ValueError):
                    packages.parse_plan(raw)

    def test_exact_tag_offline_packaging_determinism_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            plan, cache = fixture_plan(root)
            source = root / "source"
            source.mkdir()
            git = packages.bootstrap.git
            git(source, "init", "--quiet", "--template=")
            git(source, "config", "user.name", "Package fixture")
            git(source, "config", "user.email", "fixture@example.invalid")
            lock = source / packages.LOCK
            lock.parent.mkdir(parents=True)
            lock.write_bytes(wire(plan))
            git(source, "add", ".")
            git(source, "commit", "--quiet", "-m", "fixture")
            sha, release = git(source, "rev-parse", "HEAD"), "2026.11.999-rc.1"
            git(source, "tag", release)
            (source / "operator-private").write_text("not a release input")
            (cache / "unrelated.deb").write_bytes(b"not a release input")
            args = dict(source=source, source_sha=sha, release=release, repository="bunny-lab-io/Borealis", archives=cache, output_dir=root / "assets")
            result = packages.build_assets(**args)
            self.assertEqual(result["source_sha"], sha)
            self.assertEqual(result["plan_sha256"], hashlib.sha256(lock.read_bytes()).hexdigest())
            self.assertEqual(result["packages"], plan["packages"])
            first = (root / "assets" / packages.ARCHIVE).read_bytes()
            self.assertEqual(result["archive"], dict(name=packages.ARCHIVE, size=len(first), sha256=hashlib.sha256(first).hexdigest()))
            with tarfile.open(root / "assets" / packages.ARCHIVE) as archive:
                self.assertEqual(archive.getnames(), [p["file"] for p in plan["packages"]])
                for item in archive:
                    self.assertEqual((item.mode, item.uid, item.gid, item.mtime), (0o644, 0, 0, 0))
                    self.assertEqual(archive.extractfile(item).read(), (cache / item.name).read_bytes())
            args["output_dir"] = root / "second"
            self.assertEqual(packages.build_assets(**args), result)
            self.assertEqual((root / "second" / packages.ARCHIVE).read_bytes(), first)
            args["output_dir"] = root / "downloaded"
            args["archives"] = None
            def download(pin, destination):
                destination.write_bytes((cache / pin["file"]).read_bytes())
            with mock.patch.object(packages, "download", side_effect=download) as fetch:
                self.assertEqual(packages.build_assets(**args), result)
                self.assertEqual(fetch.call_count, len(plan["packages"]))
            args["archives"], args["output_dir"] = cache, root / "second"
            with self.assertRaises(FileExistsError):
                packages.build_assets(**args)
            self.assertEqual((root / "second" / packages.ARCHIVE).read_bytes(), first)
            args["output_dir"], args["source_sha"] = root / "failed", "b" * 40
            with self.assertRaisesRegex(ValueError, "tag does not identify"):
                packages.build_assets(**args)
            self.assertFalse(args["output_dir"].exists())
            args["source_sha"] = sha
            lock.write_bytes(lock.read_bytes() + b"\n")
            with self.assertRaisesRegex(ValueError, "tracked local changes"):
                packages.build_assets(**args)
            self.assertFalse(args["output_dir"].exists())

    def test_cached_bytes_and_concurrent_mutation_reject(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            plan, cache = fixture_plan(root)
            pin = plan["packages"][0]
            path, original = cache / pin["file"], (cache / pin["file"]).read_bytes()
            for mode in ("tampered", "short", "symlink", "changed while copying"):
                with self.subTest(mode=mode):
                    selected = copy.deepcopy(plan)
                    if mode == "tampered": path.write_bytes(original[:-1] + bytes([original[-1] ^ 1]))
                    elif mode == "short": path.write_bytes(original[:-1])
                    elif mode == "symlink":
                        target = root / "same-bytes"
                        target.write_bytes(original)
                        path.unlink()
                        path.symlink_to(target)
                    real = packages.DigestReader

                    def reader(source):
                        if mode == "changed while copying":
                            path.write_bytes(original[:-1] + bytes([original[-1] ^ 1]))
                        return real(source)

                    with mock.patch.object(packages, "DigestReader", side_effect=reader):
                        with self.assertRaises((ValueError, OSError)):
                            packages.assemble_archive(selected, cache, root / (mode.replace(" ", "-") + ".tar"))
                    path.unlink()
                    path.write_bytes(original)


if __name__ == "__main__":
    unittest.main()
