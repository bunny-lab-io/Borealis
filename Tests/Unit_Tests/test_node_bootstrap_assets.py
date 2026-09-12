import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("node_bootstrap", ROOT / "Tests/tools/build_node_bootstrap_assets.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
REPOSITORY = "bunny-lab-io/Borealis"
RELEASE = "2026.09.999-rc.1"


class NodeBootstrapAssetsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "operator-checkout"
        self.source.mkdir()
        builder.git(self.source, "init", "--quiet", "--template=")
        builder.git(self.source, "config", "user.name", "Borealis Tests")
        builder.git(self.source, "config", "user.email", "tests@example.invalid")
        builder.git(self.source, "config", "credential.helper", "local-helper-must-not-ship")
        (self.source / "Engine.sh").write_text("#!/bin/sh\nexit 0\n")
        (self.source / "Engine.sh").chmod(0o755)
        module = self.source / "Data/Engine/Containers/api-backend"
        command = module / "cmd/borealis-node-manager"
        command.mkdir(parents=True)
        (module / "go.mod").write_text("module borealis/api-backend\n\ngo 1.25.12\n")
        (command / "main.go").write_text('package main\nimport "fmt"\nfunc main() { fmt.Print("tagged-node-manager") }\n')
        self.commit()
        # Ensure source snapshot cuts history instead of shipping old revisions.
        (self.source / "release-marker").write_text("tagged source\n")
        self.sha = self.commit()
        builder.git(self.source, "tag", "-a", RELEASE, "-m", "qualification fixture")

    def commit(self):
        builder.git(self.source, "add", ".")
        builder.git(self.source, "commit", "--quiet", "-m", "source fixture")
        return builder.git(self.source, "rev-parse", "HEAD")

    def build(self, output="assets", **changes):
        values = dict(source=self.source, source_sha=self.sha, release=RELEASE,
                      repository=REPOSITORY, output_dir=self.root / output,
                      go_bin=os.environ.get("BOREALIS_GO_BIN", "go"))
        values.update(changes)
        return builder.build_assets(**values)

    def test_exact_tag_build_is_deterministic_and_excludes_operator_state(self):
        (self.source / "Engine.sh").write_text("uncommitted operator change\n")
        (self.source / "operator-private-file").write_text("untracked fixture, never package\n")
        first = self.build()
        second = self.build("second")
        self.assertEqual(first, second)
        bundle = self.root / "assets" / builder.BUNDLE_NAME
        self.assertEqual(first["asset"]["sha256"], builder.digest(bundle))
        self.assertEqual(first["asset"]["size"], bundle.stat().st_size)
        unpacked = self.root / "unpacked"
        with tarfile.open(bundle) as archive:
            self.assertFalse(any(item.issym() or item.islnk() for item in archive.getmembers()))
            self.assertTrue(all(item.uid == item.gid == item.mtime == 0 for item in archive.getmembers()))
            archive.extractall(unpacked, filter="data")
        source = unpacked / "source"
        self.assertEqual(builder.git(source, "rev-parse", "HEAD"), self.sha)
        self.assertEqual(builder.git(source, "rev-parse", f"refs/tags/{RELEASE}^{{commit}}"), self.sha)
        self.assertEqual(builder.git(source, "rev-list", "--count", "HEAD"), "1")
        self.assertEqual(builder.git(source, "status", "--porcelain"), "")
        self.assertFalse((source / "operator-private-file").exists())
        self.assertEqual((source / "Engine.sh").read_text(), "#!/bin/sh\nexit 0\n")
        self.assertEqual((source / "Engine.sh").stat().st_mode & 0o777, 0o755)
        config = (source / ".git/config").read_text()
        self.assertNotIn(str(self.source), config)
        self.assertNotIn("local-helper-must-not-ship", config)
        self.assertEqual(builder.git(source, "remote", "get-url", "origin"), f"https://github.com/{REPOSITORY}.git")
        identity = json.loads((unpacked / "identity.json").read_text())
        self.assertEqual(identity, {key: value for key, value in first.items() if key != "asset"})
        binary = unpacked / "bin/borealis-node-manager"
        self.assertEqual(first["node_manager"]["sha256"], builder.digest(binary))
        self.assertEqual(subprocess.check_output([str(binary)], text=True), "tagged-node-manager")
        with self.assertRaisesRegex(ValueError, "replace"):
            self.build()

    def test_missing_moved_or_unrelated_tag_fails_before_build(self):
        for changes in ({"release": "2026.09.999-rc.2"}, {"source_sha": "a" * 40}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.build(**changes)
        (self.source / "different-release").write_text("new source\n")
        self.commit()
        builder.git(self.source, "tag", "-f", RELEASE)
        with self.assertRaisesRegex(ValueError, "source SHA"):
            self.build()

    def test_symlink_or_submodule_fails_before_checkout(self):
        (self.source / "escape").symlink_to("../../outside")
        self.sha = self.commit()
        builder.git(self.source, "tag", "-f", RELEASE)
        with self.assertRaisesRegex(ValueError, "symlinks"):
            self.build()
        builder.git(self.source, "rm", "escape")
        builder.git(self.source, "update-index", "--add", "--cacheinfo", f"160000,{self.sha},submodule")
        builder.git(self.source, "commit", "--quiet", "-m", "submodule fixture")
        self.sha = builder.git(self.source, "rev-parse", "HEAD")
        builder.git(self.source, "tag", "-f", RELEASE)
        with self.assertRaisesRegex(ValueError, "submodules"):
            self.build()

    def test_invalid_release_identity_and_toolchain_fail(self):
        for changes in ({"release": "main"}, {"release": "2026.09.1-rc.0"},
                        {"source_sha": "HEAD"}, {"repository": "../outside"}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.build(**changes)
        fake = self.root / "wrong-go"
        fake.write_text("#!/bin/sh\nprintf 'go version go1.22.12 linux/amd64\\n'\n")
        fake.chmod(0o755)
        with self.assertRaisesRegex(ValueError, "go1.25.12"):
            self.build(go_bin=str(fake))
        self.assertFalse((self.root / "assets" / builder.BUNDLE_NAME).exists())


if __name__ == "__main__":
    unittest.main()
