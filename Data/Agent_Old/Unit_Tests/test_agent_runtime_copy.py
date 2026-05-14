from __future__ import annotations

from pathlib import Path


def test_agent_exe_bootstrap_source_layout_exists() -> None:
    bootstrap_root = Path(__file__).resolve().parents[3] / "Data" / "Agent" / "Bootstrap"

    assert (bootstrap_root / "Agent.exe").is_file()
    assert (bootstrap_root / "main.go").is_file()
    assert (bootstrap_root / "bootstrap-config.go").is_file()
    assert (bootstrap_root / "bootstrap-update.go").is_file()
    assert (bootstrap_root / "bootstrap-uninstall.go").is_file()
    assert (bootstrap_root / "bootstrap-python-environment.go").is_file()


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
    assert "Engine/Deploy/deploy-manifest.json" in content
    assert "Engine/Services/api-backend" in content
    assert "Refusing to install the Linux Agent in an Engine install root" in content


def test_agent_sh_does_not_treat_synced_engine_source_as_engine_install() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.sh"
    content = script_path.read_text(encoding="utf-8")

    assert "Data/Engine/Containers/compose.yaml" not in content


def test_agent_sh_supports_root_shell_bootstrap_without_sudo_pipe() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.sh"
    content = script_path.read_text(encoding="utf-8")

    assert "exec_agent_script" in content
    assert "ensure_root_execution" in content
    assert "sudo -E bash" in content
    assert "curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Agent.sh | bash -s -- deploy" in content


def test_agent_sh_supports_noninteractive_ssh_onboarding_without_tty() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.sh"
    content = script_path.read_text(encoding="utf-8")

    assert "BOREALIS_AGENT_NONINTERACTIVE" in content
    assert "BOREALIS_AGENT_NO_TTY" in content
    assert "{ exec {tty_fd}< /dev/tty; } 2>/dev/null" in content


def test_agent_exe_owns_windows_runtime_tasks() -> None:
    bootstrap_root = Path(__file__).resolve().parents[3] / "Data" / "Agent" / "Bootstrap"
    task_content = (bootstrap_root / "bootstrap-tasks.go").read_text(encoding="utf-8")
    python_content = (bootstrap_root / "bootstrap-python-environment.go").read_text(encoding="utf-8")

    assert "Borealis Agent (AutoUpdater)" in (bootstrap_root / "bootstrap-config.go").read_text(encoding="utf-8")
    assert "launch_service.ps1" in task_content
    assert "Agent.exe" in task_content
    assert "https://www.nuget.org/api/v2/package/python/3.13.3" in (bootstrap_root / "bootstrap-config.go").read_text(encoding="utf-8")
    assert "python.exe not found after NuGet extraction" in python_content
    assert "agent-requirements.txt" in python_content
