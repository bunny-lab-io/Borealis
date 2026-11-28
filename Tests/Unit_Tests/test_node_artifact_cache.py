import concurrent.futures
import hashlib
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools"))
from node_artifact_cache import ArtifactCache
sys.path.pop(0)


class NodeArtifactCacheTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.store = ArtifactCache(self.root / "objects", 32, 2, 0, 0)

    def get(self, data, name, fetch=None, *, offline=False):
        return self.store.materialize(hashlib.sha256(data).hexdigest(), len(data),
                                      self.root / name, fetch or (lambda p: p.write_bytes(data)), offline=offline)

    def test_single_population_independent_outputs_and_offline(self):
        acquired = []
        def fetch(p):
            acquired.append(p)
            time.sleep(.03)
            p.write_bytes(b"first")
        with concurrent.futures.ThreadPoolExecutor(2) as pool:
            hits = list(pool.map(lambda n: self.get(b"first", n, fetch), ["one", "two"]))
        self.assertEqual(sorted(hits), [False, True])
        self.assertEqual(len(acquired), 1)
        (self.root / "one").write_bytes(b"other")
        self.assertTrue(self.get(b"first", "three", offline=True))
        self.assertEqual((self.root / "three").read_bytes(), b"first")
        with self.assertRaisesRegex(ValueError, "offline"):
            self.get(b"new", "four", offline=True)
        with self.assertRaises(FileExistsError):
            self.get(b"first", "one", offline=True)
        self.assertEqual((self.root / "one").read_bytes(), b"other")

    def test_partial_wrong_and_corrupt_never_publish(self):
        def interrupted(path):
            path.write_bytes(b"s")
            raise InterruptedError()
        with self.assertRaises(InterruptedError):
            self.get(b"safe", "result", interrupted)
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            self.get(b"safe", "result", lambda p: p.write_bytes(b"oops"))
        self.assertEqual(list(self.store.root.iterdir()), [])
        (self.store.root / ".incoming").write_bytes(b"orphan")
        self.get(b"safe", "original")
        digest = hashlib.sha256(b"safe").hexdigest()
        blob = self.store.root / digest
        blob.chmod(0o600)
        blob.write_bytes(b"oops")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            self.get(b"safe", "result", offline=True)
        self.assertFalse((self.root / "result").exists())

    def test_retained_objects_not_evicted_and_size_bound(self):
        self.get(b"a" * 16, "a")
        retained = self.store.root / hashlib.sha256(b"a" * 16).hexdigest()
        for _ in range(2):
            self.assertTrue(self.store.materialize(retained.name, 16, self.root / "recovery",
                                                  None, offline=True, retain=True))
        self.assertTrue(retained.samefile(self.root / "recovery"))
        with self.assertRaisesRegex(ValueError, "conflicts"):
            self.store.materialize(retained.name, 16, self.root / "a", None, offline=True, retain=True)
        self.get(b"b" * 16, "b")
        self.get(b"c" * 16, "c")
        self.assertEqual(retained.read_bytes(), b"a" * 16)
        self.assertEqual(len(list(self.store.root.iterdir())), 2)
        with self.assertRaisesRegex(ValueError, "capacity"):
            self.get(b"d" * 17, "d")
        self.assertEqual((self.root / "b").read_bytes(), b"b" * 16)
