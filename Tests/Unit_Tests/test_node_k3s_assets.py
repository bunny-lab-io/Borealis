import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import tarfile
import subprocess
import unittest
from unittest import mock
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "Tests/tools"))
spec = importlib.util.spec_from_file_location("node_k3s", ROOT / "Tests/tools/build_node_k3s_assets.py")
k3s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(k3s)
sys.path.pop(0)


class NodeK3sAssetsTests(unittest.TestCase):
    def test_static_payload_measurement_and_failure_bounds(self):
        for mode in ("valid", "binary changed", "frame changed", "tar changed", "too small", "wrong entries", "escaping link"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                raw = io.BytesIO()
                with tarfile.open(fileobj=raw, mode="w") as archive:
                    member = tarfile.TarInfo("./bin/busybox")
                    member.size = 4
                    archive.addfile(member, io.BytesIO(b"data"))
                    member = tarfile.TarInfo("./bin/aux/mount")
                    member.type, member.linkname = tarfile.SYMTYPE, "../../../escape" if mode == "escaping link" else "../busybox"
                    archive.addfile(member)
                frame = subprocess.run(["zstd", "--compress", "--stdout"], input=raw.getvalue(), stdout=subprocess.PIPE,
                                       stderr=subprocess.PIPE, check=True, timeout=10).stdout
                binary = root / "binary"
                binary.write_bytes(b"fixture prefix" + frame + b"suffix")
                p = dict(offset=len(b"fixture prefix"), compressed_bytes=len(frame), sha256=hashlib.sha256(frame).hexdigest(),
                         tar_sha256=hashlib.sha256(raw.getvalue()).hexdigest(), tar_bytes=len(raw.getvalue()), file_bytes=4, entries=2, cni_links=0)
                pins = dict(binary=dict(size=binary.stat().st_size, sha256=hashlib.sha256(binary.read_bytes()).hexdigest()), payload=p)
                if mode == "binary changed":
                    binary.write_bytes(b"changed")
                elif mode == "frame changed":
                    p["sha256"] = "a" * 64
                elif mode == "tar changed":
                    p["tar_sha256"] = "b" * 64
                elif mode == "too small":
                    p["tar_bytes"] = 1024
                elif mode == "wrong entries":
                    p["entries"] += 1
                if mode == "valid":
                    self.assertEqual(k3s.measure_payload(binary, pins, root), p)
                else:
                    with self.assertRaises(ValueError):
                        k3s.measure_payload(binary, pins, root)

    def test_download_pins_length_digest_and_hides_remote_errors(self):
        for mode in ("valid", "short", "long", "digest", "encoding", "remote error"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                data = b"pinned binary"
                pin = dict(name="k3s", size=len(data), sha256=hashlib.sha256(data).hexdigest())
                if mode == "digest":
                    pin["sha256"] = "a" * 64
                response = io.BytesIO(data[:-1] if mode == "short" else data + b"x" if mode == "long" else data)
                response.status = 200
                response.headers = {"Content-Encoding": "gzip"} if mode == "encoding" else {}
                opener = mock.Mock()
                opener.open.return_value = response
                if mode == "remote error":
                    opener.open.side_effect = RuntimeError("signed URL must stay private")
                with mock.patch.object(k3s.urllib.request, "build_opener", return_value=opener):
                    target = Path(temporary) / "asset"
                    if mode == "valid":
                        k3s.download("v1.36.3+k3s1", pin, target)
                        self.assertEqual(target.read_bytes(), data)
                    else:
                        with self.assertRaisesRegex(ValueError, "^pinned K3s asset download failed$"):
                            k3s.download("v1.36.3+k3s1", pin, target)

    def test_redirect_is_single_public_asset_request(self):
        handler = k3s.ReleaseRedirect()
        req = urllib.request.Request("https://github.com/k3s-io/k3s/releases/download/version/k3s",
                                     headers={"Authorization": "fixture", "Cookie": "fixture"})
        good = "https://release-assets.githubusercontent.com/github-production-release-asset/123/asset?sig=fixture"
        next_req = handler.redirect_request(req, None, 302, "", {}, good)
        self.assertFalse(next_req.has_header("Authorization"))
        self.assertFalse(next_req.has_header("Cookie"))
        for original, url in ((next_req, good), (req, "http://release-assets.githubusercontent.com/github-production-release-asset/a"),
                              (req, good.replace(".com/", ".com.evil/")), (req, good.replace("/github-production-release-asset/", "/other/"))):
            with self.assertRaises(ValueError):
                handler.redirect_request(original, None, 302, "", {}, url)

    def test_exact_source_packaging_baseline_failure_cleanup_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            source.mkdir()
            git = k3s.bootstrap.git
            git(source, "init", "--quiet", "--template=")
            git(source, "config", "user.name", "K3s fixture")
            git(source, "config", "user.email", "fixture@example.invalid")
            lock = source / k3s.LOCK
            lock.parent.mkdir(parents=True)
            lock.write_bytes((ROOT / k3s.LOCK).read_bytes())
            pins = json.loads(lock.read_text())
            manifest = source / "Data/Engine/release-manifest.json"
            manifest.write_text(json.dumps({"required_k3s_baseline": pins["version"]}))
            git(source, "add", ".")
            git(source, "commit", "--quiet", "-m", "fixture")
            sha = git(source, "rev-parse", "HEAD")
            release = "2026.09.999-rc.1"
            git(source, "tag", release)
            (source / "operator-private").write_text("not in release")
            proof = dict(archive_sha256=pins["archive"]["sha256"], archive_bytes=pins["archive"]["size"],
                         content_bytes=1024, content_entries=4, expanded_bytes=4096, expanded_entries=2, images=pins["images"])

            def build_manager(snapshot, destination, go_bin):
                self.assertNotEqual(source, snapshot)
                self.assertFalse((snapshot / "operator-private").exists())
                # Stand-in only for verifier process; native Go archive tests
                # independently cover the producer's content measurement.
                destination.write_text("#!/usr/bin/env python3\nimport json,sys\nassert sys.argv[1:3] == ['k3s-archive-proof','--archive']\nprint(" + repr(json.dumps(proof)) + ")\n")
                destination.chmod(0o700)

            def download(version, pin, destination):
                self.assertEqual(version, pins["version"])
                destination.write_bytes(b"fixture input")

            args = dict(source=source, release=release, source_sha=sha, repository="bunny-lab-io/Borealis",
                        output_dir=root / "assets", go_bin="fixture-go")
            with mock.patch.object(k3s.bootstrap, "build_manager", side_effect=build_manager), mock.patch.object(k3s, "download", side_effect=download), mock.patch.object(k3s, "measure_payload", return_value=pins["payload"]):
                result = k3s.build_assets(**args)
                self.assertEqual(result["archive"], proof)
                self.assertEqual(result["source_sha"], sha)
                self.assertEqual(result, json.loads((root / "assets" / k3s.INVENTORY).read_text()))
                with self.assertRaises(FileExistsError):
                    k3s.build_assets(**args)
                args["output_dir"] = root / "failed"
                with mock.patch.object(k3s, "download", side_effect=ValueError("failed")):
                    with self.assertRaises(ValueError):
                        k3s.build_assets(**args)
                self.assertFalse(args["output_dir"].exists())
                manifest.write_text(json.dumps({"required_k3s_baseline": "v1.36.4+k3s1"}))
                git(source, "add", ".")
                git(source, "commit", "--quiet", "-m", "changed baseline")
                args["source_sha"] = git(source, "rev-parse", "HEAD")
                args["release"] = "2026.09.999-rc.2"
                git(source, "tag", args["release"])
                with self.assertRaisesRegex(ValueError, "differs from release baseline"):
                    k3s.build_assets(**args)
                self.assertFalse(args["output_dir"].exists())


if __name__ == "__main__":
    unittest.main()
