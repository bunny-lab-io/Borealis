from __future__ import annotations

from pathlib import Path


def test_agent_ps1_stages_runtime_paths_into_agent_runtime() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "launch_service.ps1" in content
    assert "restart_agent_tasks.ps1" in content
    assert "desktop_environment.py" in content
    assert "runtime_paths.py" in content
    assert "qt_compat.py" in content
    assert "session_runtime.py" in content
    assert "tray_state.py" in content
    assert "PreserveDirectories @('Agent', 'Temp')" in content


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


def test_agent_ps1_hardens_tray_folder_permissions() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "Ensure-AgentTrayFolderPermissions" in content
    assert "Join-Path $settingsDir 'Tray'" in content
    assert "S-1-5-11" in content
    assert "S-1-5-32-545" in content


def test_agent_ps1_repairs_nested_python_msi_layout() -> None:
    script_path = Path(__file__).resolve().parents[3] / "Agent.ps1"
    content = script_path.read_text(encoding="utf-8")

    assert "function Find-BorealisPythonExecutable" in content
    assert "function Repair-BorealisPythonBootstrapLayout" in content
    assert "function Invoke-BorealisInstallerProcess" in content
    assert "function Install-BorealisPythonFromInstaller" in content
    assert "function Install-BorealisPythonFromNuGetPackage" in content
    assert "function Get-BorealisPythonBootstrapLayoutSummary" in content
    assert "Get-ChildItem -LiteralPath $InstallDir -Filter 'python.exe' -File -Recurse" in content
    assert "Normalizing MSI administrative install layout" in content
    assert "https://www.nuget.org/api/v2/package/python/3.13.3" in content
    assert "Python NuGet package did not contain tools\\python.exe." in content
    assert "Installing Python from NuGet package" in content
    assert "$lastExitCode -ne 1618" in content
    assert "Windows Installer busy while running" in content
    assert "Start-Sleep -Seconds $DelaySeconds" in content
    assert "$extractExitCode -notin @(0, 3010)" in content
    assert "Python MSI extraction failed for '$file' with exit code $extractExitCode." in content
    assert "Falling back to full Python installer after MSI failure" in content
    assert "python-3.13.3-amd64.exe" in content
    assert "MSI administrative extraction did not produce python.exe" in content
    assert "Include_pip=1" in content
    assert "Include_test=0" in content
    assert "Python executable not found after MSI extraction or installer fallback." in content
