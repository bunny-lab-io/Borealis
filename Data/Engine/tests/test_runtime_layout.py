"""Unit tests for runtime layout normalisation helpers."""

from __future__ import annotations

import shutil
import tempfile
from pathlib import Path
import unittest

from Data.Engine.runtime_layout import RuntimeLayoutResult, normalise_runtime_layout


class NormaliseRuntimeLayoutTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmpdir = Path(tempfile.mkdtemp(prefix="runtime-layout-test"))
        self.runtime_root = self.tmpdir / "Engine"
        (self.runtime_root / "Data" / "Engine").mkdir(parents=True)

    def tearDown(self) -> None:
        shutil.rmtree(self.tmpdir)

    def test_moves_children_into_runtime_root(self) -> None:
        legacy_root = self.runtime_root / "Data" / "Engine"
        (legacy_root / "builders").mkdir()
        (legacy_root / "config").mkdir()
        (legacy_root / "web-interface").mkdir()
        (legacy_root / "config" / "settings.py").write_text("value = 1", encoding="utf-8")

        result = normalise_runtime_layout(self.runtime_root)

        self.assertIsInstance(result, RuntimeLayoutResult)
        self.assertTrue(result.changed)
        self.assertFalse((legacy_root).exists())
        self.assertTrue((self.runtime_root / "builders").is_dir())
        self.assertTrue((self.runtime_root / "config" / "settings.py").exists())
        self.assertTrue((self.runtime_root / "web-interface").is_dir())

    def test_returns_reason_when_legacy_missing(self) -> None:
        shutil.rmtree(self.runtime_root / "Data")

        result = normalise_runtime_layout(self.runtime_root)

        self.assertFalse(result.changed)
        self.assertEqual(result.reason, "legacy-missing")


if __name__ == "__main__":  # pragma: no cover - convenience
    unittest.main()
