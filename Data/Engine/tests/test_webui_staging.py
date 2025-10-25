"""Unit tests for Engine web UI staging helpers."""
from __future__ import annotations
import shutil
import tempfile
from pathlib import Path
import unittest

from Data.Engine.webui.staging import WebUIStagingResult, stage_web_interface


class StageWebInterfaceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmpdir = Path(tempfile.mkdtemp(prefix="webui-stage-test"))

    def tearDown(self) -> None:
        shutil.rmtree(self.tmpdir)

    def test_copies_webui_from_legacy_source(self) -> None:
        source = self.tmpdir / "Data" / "Server" / "WebUI"
        destination = self.tmpdir / "Engine" / "web-interface"
        (source / "nested").mkdir(parents=True)
        (source / "index.html").write_text("<html></html>", encoding="utf-8")
        (source / "nested" / "file.txt").write_text("contents", encoding="utf-8")

        destination.mkdir(parents=True)
        (destination / "stale.txt").write_text("stale", encoding="utf-8")

        result = stage_web_interface(self.tmpdir)

        self.assertIsInstance(result, WebUIStagingResult)
        self.assertTrue(result.copied)
        self.assertTrue((destination / "index.html").exists())
        self.assertFalse((destination / "stale.txt").exists())
        self.assertEqual((destination / "nested" / "file.txt").read_text(encoding="utf-8"), "contents")

    def test_handles_missing_source_directory(self) -> None:
        destination = self.tmpdir / "Engine" / "web-interface"

        result = stage_web_interface(self.tmpdir)

        self.assertFalse(result.copied)
        self.assertEqual(result.reason, "source-missing")
        self.assertTrue(destination.exists())
        self.assertEqual(result.destination, destination.resolve())


if __name__ == "__main__":  # pragma: no cover - convenience
    unittest.main()
