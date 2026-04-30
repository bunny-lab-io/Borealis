"""Software inventory config isolation helpers for Engine unit tests."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from Data.Engine.services.API.devices import software_icons as software_icons_module
from Data.Engine.services.API.devices import software_uninstall as software_uninstall_module


def _write_json(path: Path, payload: dict[str, Any]) -> Path:
    path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
    return path


def isolate_software_icon_overrides(
    monkeypatch: Any,
    tmp_path: Path,
    *,
    rules: list[dict[str, Any]] | None = None,
) -> Path:
    """Point icon override loading at test-owned JSON."""
    path = _write_json(
        tmp_path / "software_icons_overrides.json",
        {"windows_icon_overrides": list(rules or [])},
    )
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": list(rules or [])},
    )
    return path


def isolate_uninstall_blocklist(
    monkeypatch: Any,
    tmp_path: Path,
    *,
    rules: list[dict[str, Any]] | None = None,
) -> Path:
    """Point uninstall blocklist loading at test-owned JSON."""
    path = _write_json(
        tmp_path / "software_uninstall_blocklist.json",
        {"windows_quiet_uninstall_blocklist": list(rules or [])},
    )
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_BLOCKLIST_PATH", path)
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_BLOCKLIST_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_uninstall_module,
        "_UNINSTALL_BLOCKLIST_CACHE",
        {"windows_quiet_uninstall_blocklist": list(rules or [])},
    )
    return path


def isolate_uninstall_overrides(
    monkeypatch: Any,
    tmp_path: Path,
    *,
    rules: list[dict[str, Any]] | None = None,
) -> Path:
    """Point uninstall override loading at test-owned JSON."""
    path = _write_json(
        tmp_path / "software_uninstall_overrides.json",
        {"windows_uninstall_overrides": list(rules or [])},
    )
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_OVERRIDES_PATH", path)
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_uninstall_module,
        "_UNINSTALL_OVERRIDES_CACHE",
        {"windows_uninstall_overrides": list(rules or [])},
    )
    return path
