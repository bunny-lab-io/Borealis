import hashlib
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools"))
import retain_engine_release as release
from node_artifact_cache import ArtifactCache
sys.path.pop(0)


class RetainedRelease(unittest.TestCase):
    def test_reuse_pins_and_failed_partial_acquisition(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            ref = root / "refs"
            ref.mkdir()
            store = ArtifactCache(root / "objects", 128, 3, 0, 0)
            payloads = {"borealis-node-a": b"one", "borealis-node-b": b"two"}
            assets = {k: {"name": k, "digest": "sha256:" + hashlib.sha256(v).hexdigest(), "size": len(v)}
                      for k, v in payloads.items()}
            def retain():
                return release.retain("owner/project", "2026.11.1", "a" * 40, ref)
            with patch.object(release, "release_assets", return_value=assets), patch.object(release, "configured_store", return_value=store):
                with patch.object(release, "download", side_effect=lambda asset, path, _: path.write_bytes(b"bad")):
                    with self.assertRaises(ValueError):
                        retain()
                self.assertEqual(list(ref.iterdir()), [])
                with patch.object(release, "download", side_effect=lambda asset, path, _: path.write_bytes(payloads[asset["name"]])) as get:
                    candidate = retain()
                    self.assertEqual(retain(), candidate)
                    self.assertEqual(get.call_count, 2)
                self.assertEqual((candidate / "borealis-node-b").stat().st_nlink, 2)
