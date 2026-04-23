from __future__ import annotations

import Data.Agent.Roles.system_script_execution as exec_module


def test_build_wrapped_script_can_disable_background_job() -> None:
    wrapped = exec_module._build_wrapped_script(
        "Write-Output 'hello'",
        {},
        30,
        use_background_job=False,
    )

    assert "Start-Job" not in wrapped
    assert "& $__BorealisScript" in wrapped


def test_build_wrapped_script_uses_supported_job_timeout_cleanup() -> None:
    wrapped = exec_module._build_wrapped_script(
        "Write-Output 'hello'",
        {},
        30,
    )

    assert "Stop-Job $job -Force" not in wrapped
    assert "Stop-Job -Job $job -ErrorAction SilentlyContinue | Out-Null" in wrapped
    assert "Remove-Job -Job $job -Force -ErrorAction SilentlyContinue | Out-Null" in wrapped


def test_run_system_script_disables_background_job_when_progress_callback_present(monkeypatch) -> None:
    captured: dict[str, object] = {}

    def _fake_system_task(content, env_map, timeout_seconds, **kwargs):
        captured.update(kwargs)
        return 0, "ok", ""

    monkeypatch.setattr(exec_module.os, "name", "nt")
    monkeypatch.setattr(exec_module, "_run_powershell_via_system_task", _fake_system_task)

    rc, out, err = exec_module.run_system_script(
        script_type="powershell",
        content="Write-Output 'hello'",
        env_map={},
        timeout_seconds=30,
        progress_callback=lambda _chunk: None,
    )

    assert (rc, out, err) == (0, "ok", "")
    assert captured["use_background_job"] is False


def test_run_system_script_logs_scheduled_task_fallback_before_direct_launch(monkeypatch) -> None:
    logs: list[str] = []

    def _fake_system_task(content, env_map, timeout_seconds, **kwargs):
        return -999, "", "task registration failed"

    def _fake_direct(content, env_map, timeout_seconds, **kwargs):
        return 0, "done", ""

    monkeypatch.setattr(exec_module.os, "name", "nt")
    monkeypatch.setattr(exec_module, "_run_powershell_via_system_task", _fake_system_task)
    monkeypatch.setattr(exec_module, "_run_powershell_script_content", _fake_direct)

    rc, out, err = exec_module.run_system_script(
        script_type="powershell",
        content="Write-Output 'hello'",
        env_map={},
        timeout_seconds=30,
        log_callback=logs.append,
    )

    assert (rc, out, err) == (0, "done", "")
    assert any("scheduled-task fallback triggered" in message for message in logs)
    assert any("task registration failed" in message for message in logs)
