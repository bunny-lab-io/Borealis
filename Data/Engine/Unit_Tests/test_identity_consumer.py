"""Linux worker startup guard; real flock, private temporary directories only."""

import ast
import fcntl
import os
from pathlib import Path
import tempfile
import unittest

from Data.Engine.identity_consumer import RECOVERY_MARKER, acquire_identity_consumer


class IdentityConsumerTests(unittest.TestCase):
    def test_shared_lifetime_and_exclusive_recovery(self):
        with tempfile.TemporaryDirectory() as root:
            first = acquire_identity_consumer(root)
            second = acquire_identity_consumer(root)
            recovery = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
            try:
                self.assertFalse(os.get_inheritable(first))
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(recovery, fcntl.LOCK_EX | fcntl.LOCK_NB)
                os.close(first)
                first = None
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(recovery, fcntl.LOCK_EX | fcntl.LOCK_NB)
                os.close(second)
                second = None
                fcntl.flock(recovery, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with self.assertRaises(BlockingIOError):
                    acquire_identity_consumer(root)
            finally:
                for fd in (first, second, recovery):
                    if fd is not None:
                        os.close(fd)

    def test_any_marker_blocks_even_on_read_only_directory(self):
        for kind in ("empty", "malformed", "symlink", "directory"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as root:
                marker = Path(root, RECOVERY_MARKER)
                if kind == "symlink":
                    marker.symlink_to("missing")
                elif kind == "directory":
                    marker.mkdir()
                else:
                    marker.write_bytes(b"" if kind == "empty" else b"invalid")
                os.chmod(root, 0o500)
                try:
                    with self.assertRaisesRegex(RuntimeError, "identity recovery incomplete"):
                        acquire_identity_consumer(root)
                finally:
                    os.chmod(root, 0o700)

    def test_symlink_and_writable_root_rejected(self):
        with tempfile.TemporaryDirectory() as parent:
            root = Path(parent, "identity")
            root.mkdir(mode=0o700)
            alias = Path(parent, "alias")
            alias.symlink_to(root, target_is_directory=True)
            with self.assertRaisesRegex(RuntimeError, "without symlinks"):
                acquire_identity_consumer(str(alias))
            root.chmod(0o777)
            with self.assertRaisesRegex(RuntimeError, "unsafe permissions"):
                acquire_identity_consumer(str(root))

    def test_worker_holds_guard_before_configuration_can_load_keys(self):
        # Read module AST without importing its network/DB runtime dependencies.
        source = Path(__file__).parents[1] / "Containers/site-worker/data/services/job_scheduler/worker.py"
        tree = ast.parse(source.read_text(encoding="utf-8"))
        main = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "main")
        assignments = [node for node in main.body if isinstance(node, ast.Assign)]
        guard = next(node for node in assignments if isinstance(node.value, ast.Call)
                     and isinstance(node.value.func, ast.Name) and node.value.func.id == "acquire_identity_consumer")
        settings = next(node for node in assignments if isinstance(node.value, ast.Call)
                        and isinstance(node.value.func, ast.Name) and node.value.func.id == "load_runtime_config")
        self.assertLess(guard.lineno, settings.lineno)
        self.assertEqual([target.id for target in guard.targets], ["_identity_consumer_fd"])
        self.assertTrue(any(isinstance(node, ast.Global) and "_identity_consumer_fd" in node.names for node in main.body))


if __name__ == "__main__":
    unittest.main()
