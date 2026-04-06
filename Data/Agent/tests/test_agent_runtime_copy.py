from __future__ import annotations

from pathlib import Path


def test_borealis_ps1_stages_runtime_paths_into_agent_runtime() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Borealis.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "runtime_paths.py" in content
    assert "tray_state.py" in content
