from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools"))
import build_engine_development as dev
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
