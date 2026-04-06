from __future__ import annotations

from pathlib import Path


def test_borealis_ps1_stages_runtime_paths_into_agent_runtime() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Borealis.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "launch_service.ps1" in content
    assert "runtime_paths.py" in content
    assert "tray_state.py" in content


def test_borealis_ps1_hardens_tray_folder_permissions() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Borealis.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "Ensure-AgentTrayFolderPermissions" in content
    assert "Join-Path $settingsDir 'Tray'" in content
    assert "S-1-5-11" in content
    assert "S-1-5-32-545" in content
