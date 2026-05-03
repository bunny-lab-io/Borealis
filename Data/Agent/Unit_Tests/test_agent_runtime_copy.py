from __future__ import annotations

from pathlib import Path


def test_borealis_ps1_stages_runtime_paths_into_agent_runtime() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Borealis.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "launch_service.ps1" in content
    assert "restart_agent_tasks.ps1" in content
    assert "desktop_environment.py" in content
    assert "runtime_paths.py" in content
    assert "qt_compat.py" in content
    assert "session_runtime.py" in content
    assert "tray_state.py" in content


def test_agent_sh_stages_runtime_paths_into_agent_runtime() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.sh"
    content = script_path.read_text(encoding="utf-8")

    assert "desktop_environment.py" in content
    assert "runtime_paths.py" in content
    assert "qt_compat.py" in content
    assert "session_runtime.py" in content
    assert "tray_state.py" in content


def test_agent_sh_does_not_stop_running_updater_service() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.sh"
    content = script_path.read_text(encoding="utf-8")

    assert "running_in_agent_updater_service" in content
    assert "BOREALIS_AGENT_UPDATER_SERVICE=1" in content
    assert "borealis-agent-updater\\.service" in content


def test_agent_sh_blocks_engine_host_install() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.sh"
    content = script_path.read_text(encoding="utf-8")

    assert "ensure_not_engine_host" in content
    assert "Data/Engine/Containers/compose.yaml" in content
    assert "Refusing to install the Linux Agent in an Engine install root" in content


def test_borealis_ps1_hardens_tray_folder_permissions() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Borealis.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "Ensure-AgentTrayFolderPermissions" in content
    assert "Join-Path $settingsDir 'Tray'" in content
    assert "S-1-5-11" in content
    assert "S-1-5-32-545" in content
