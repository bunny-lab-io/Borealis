from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools"))
import build_engine_development as dev
import development_image_update as image_update
sys.path.pop(0)


class DevelopmentSnapshotTests(unittest.TestCase):
    def test_unpublished_revision_and_content_bound_identity(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / "src"
            source.mkdir()
            git = dev.bootstrap.git
            git(source, "init", "--quiet", "--template=")
            (source / "tracked").write_text("authorization input")
            git(source, "add", "tracked")
            git(source, "-c", "user.name=Fixture", "-c", "user.email=test@example.invalid",
                "commit", "-qm", "local revision")
            original = git(source, "rev-parse", "HEAD")
            (source / "tracked").write_text("later edit")
            (source / "private").write_text("local only")
            out = root / "out"
            out.mkdir()
            def package(*, source, release, source_sha, repository, output_dir, archives):
                self.assertEqual((source / "tracked").read_text(), "authorization input")
                self.assertFalse((source / "private").exists())
                self.assertEqual(source_sha, original)
                self.assertEqual(release, "dev-src-" + original)
                doc = dict(source_sha=source_sha, release=release, repository=repository, platform="linux-amd64")
                output_dir.mkdir()
                (output_dir / dev.packages.ARCHIVE).write_bytes(b"fixture")
                (output_dir / dev.packages.INVENTORY).write_bytes(dev.canonical(doc))
                return doc
            with patch.object(dev.packages, "build_assets", side_effect=package):
                path = dev.build(source, "HEAD", out, ["host_package"], "test/Borealis", "go")
                self.assertEqual(dev.verify(path)["kind"], "component-set")
                self.assertEqual(path, dev.build(source, "HEAD", out, ["host_package"], "test/Borealis", "go"))
            self.assertEqual(git(source, "tag", "--list"), "")
            (path / "host_package" / dev.packages.ARCHIVE).write_bytes(b"corrupt")
            with self.assertRaisesRegex(ValueError, "content"):
                dev.verify(path)
            (path / "host_package" / dev.packages.ARCHIVE).unlink()
            with self.assertRaisesRegex(ValueError, "incomplete"):
                dev.verify(path)

    def test_release_packagers_keep_distinct_source_identity(self):
        sha = "a"*40
        self.assertTrue(dev.bootstrap.packaging_identity("dev-src-" + sha, sha))
        self.assertFalse(dev.bootstrap.packaging_identity("dev-src-" + "b"*40, sha))
        self.assertFalse(dev.bootstrap.RELEASE.fullmatch("dev-src-" + sha))

    def test_reuse_requires_unchanged_inputs_and_supported_dockerfile(self):
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp)
            dockerfile = source / "Dockerfile"
            item = {"context": ".", "dockerfile": "Dockerfile", "inputs": ["config/**"]}
            retained = dev.RetainedRelease.__new__(dev.RetainedRelease)
            dockerfile.write_text("FROM scratch\nCOPY config /app\n")
            paths = retained.role_inputs(source, item)
            self.assertIn("config", paths)
            retained.original = {"config/app": "100644 blob a", "outside": "100644 blob b"}
            retained.current = {**retained.original, "outside": "100644 blob c"}
            self.assertTrue(retained.unchanged(paths))
            retained.current["config/app"] = "100755 blob a"
            self.assertFalse(retained.unchanged(paths))
            for contents in ("# syntax=example/frontend\nCOPY config /app\n",
                             "RUN --mount=type=bind,target=/data true\n",
                             "COPY *.sh /bin/\n", "ADD https://example.invalid/a /a\n"):
                dockerfile.write_text(contents)
                self.assertIsNone(retained.role_inputs(source, item))

    def test_changed_development_image_requires_dedicated_worker(self):
        import socket
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            path = root / "buildkitd.sock"
            with patch.object(dev.images, "DEV_BUILDKIT", path):
                with self.assertRaisesRegex(ValueError, "dedicated BuildKit"):
                    dev.images.development_builder()
                path.write_text("fixture")
                with self.assertRaises(ValueError):
                    dev.images.development_builder()
                path.unlink()
                with socket.socket(socket.AF_UNIX) as sock:
                    sock.bind(str(path))
                    command = dev.images.development_image_command(dev.images.development_builder(),
                        root, root / "Dockerfile", {"targets": {"prod": "prod"}}, "image:dev",
                        root / "export.oci", ["io.borealis.service=test"])
                    self.assertEqual(command[:4], ["buildctl", "--addr", "unix://" + str(path), "build"])
                    self.assertIn("target=prod", command)
                    self.assertIn("label:io.borealis.service=test", command)
                    self.assertFalse(any("docker.sock" in arg for arg in command))

    def test_offline_frontend_uses_pinned_node_and_current_lockfile_cache(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            lock = root / "data/web-interface/package-lock.json"
            lock.parent.mkdir(parents=True)
            lock.write_text("{}")
            node = root / dev.images.WEBUI_NODE_SHA256
            node.mkdir()
            (node / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
            cache = root / dev.bootstrap.digest(lock)
            (cache / "_cacache").mkdir(parents=True)
            item = {"dockerfile": "Data/Engine/Containers/webui-frontend/Dockerfile", "targets": {"prod": "prod"}}
            def command():
                return dev.images.development_image_command(["buildctl"], root, root / "Dockerfile",
                                                            item, "image:dev", root / "image.oci", [])
            with patch.object(dev.images, "WEBUI_BUILD_INPUTS", root), patch.dict("os.environ", BOREALIS_BUILD_INPUTS_OFFLINE="1"):
                args = command()
                self.assertIn(f"context:node:22-alpine=oci-layout://node@sha256:{node.name}", args)
                self.assertIn(f"npm-seed={cache}", args)
                self.assertIn("build-arg:NPM_CONFIG_OFFLINE=true", args)
                lock.write_text('{"changed":true}')
                with self.assertRaisesRegex(ValueError, "cache for this npm lockfile"):
                    command()

    def test_upgrade_history_fast_forwards_existing_member_without_origin(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            src = root / "src"
            src.mkdir()
            git = dev.bootstrap.git
            git(src, "init", "--quiet", "--template=")
            git(src, "config", "user.email", "fixture@example.invalid")
            git(src, "config", "user.name", "fixture")
            manifest = src / "Data/Engine/release-manifest.json"
            manifest.parent.mkdir(parents=True)
            manifest.write_text('{"cluster_compatible":true}')
            git(src, "add", ".")
            git(src, "commit", "--quiet", "-m", "baseline")
            base = git(src, "rev-parse", "HEAD")
            recipient = root / "recipient"
            git(root, "clone", "--quiet", "--no-local", src.as_uri(), str(recipient))
            git(recipient, "remote", "remove", "origin")
            (src / "fix").write_text("focused change")
            git(src, "add", ".")
            git(src, "commit", "--quiet", "-m", "correction")
            sha = git(src, "rev-parse", "HEAD")
            package = root / "upgrade"
            info = dev.upgrade.package(src, src, base, sha, package)
            self.assertEqual(info, {"base_sha": base, "compatibility": {"cluster_compatible": True}})
            bundle = str(package / dev.upgrade.BUNDLE)
            git(recipient, "bundle", "verify", bundle)
            git(recipient, "fetch", "--no-tags", "--no-write-fetch-head", bundle, dev.upgrade.REF)
            self.assertEqual(git(recipient, "rev-parse", "HEAD"), base)
            git(recipient, "merge", "--ff-only", sha)
            self.assertEqual((recipient / "fix").read_text(), "focused change")
            with self.assertRaises(ValueError):
                dev.upgrade.package(src, src, sha, base, root / "backward")

    def test_binary_layer_requires_only_compiled_changes_and_equivalent_copy_contract(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            update = image_update
            changed = update.MODULE + "cmd/api-backend/route.go"
            file = root / "Dockerfile"
            item = {"context": ".", "dockerfile": "Dockerfile"}
            retained = dev.RetainedRelease.__new__(dev.RetainedRelease)
            retained.original = {changed: "old", "Dockerfile": "original"}
            retained.current = {changed: "new", "Dockerfile": "original"}
            instructions = ("FROM base\nCOPY " + update.DIST + "api-backend /usr/local/bin/server\n"
                            "RUN chmod 0755 /usr/local/bin/server\nUSER borealis-engine\n")
            file.write_text(instructions)
            paths = {update.MODULE.rstrip("/"), "Dockerfile"}
            def choose():
                return update.binary_layer("api-backend", root, item, retained.changed_inputs(paths))
            self.assertEqual(choose(), [("api-backend", "/usr/local/bin/server")])
            metadata = "Data/Engine/release-manifest.json"
            retained.current[metadata] = "updated"
            paths.add(metadata)
            self.assertEqual(choose(), [("api-backend", "/usr/local/bin/server")])
            file.write_text(instructions + "COPY Data/Engine /usr/share/source\n")
            self.assertIsNone(choose())
            file.write_text(instructions)
            schema = "Data/Engine/K3s/cluster/crds.yaml"
            retained.current[schema] = "new schema"
            paths.add(schema)
            self.assertEqual(choose(), [("api-backend", "/usr/local/bin/server")])
            file.write_text(instructions + "COPY Data/Engine/K3s/cluster /etc/cluster\n")
            self.assertIsNone(choose())
            file.write_text(instructions)
            retained.current["Dockerfile"] = "different"
            self.assertIsNone(choose())
            retained.current["Dockerfile"] = "original"
            file.write_text(instructions + "RUN generate-app-configuration\n")
            self.assertIsNone(choose())
