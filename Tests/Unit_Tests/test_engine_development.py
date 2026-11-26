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
