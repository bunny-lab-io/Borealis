from __future__ import annotations

import os

os.environ.setdefault("BOREALIS_AGENT_MODE", "system")

import Data.Agent.agent as agent_module


class _FakeTrayState:
    RESTART_REQUEST_TTL_SECONDS = 120

    def __init__(self) -> None:
        self.payload = {
            "request_id": "restart-1",
            "requested_at": 1000,
            "service_mode": "system",
        }
        self.cleared: list[str] = []

    def load_restart_request(self, service_mode: str):
        if service_mode != "system":
            return {}
        return dict(self.payload)

    def clear_restart_request(self, service_mode: str) -> None:
        self.cleared.append(service_mode)


def test_process_tray_restart_request_consumes_own_scope(monkeypatch) -> None:
    fake_tray_state = _FakeTrayState()
    calls: list[str] = []

    monkeypatch.setattr(agent_module, "_tray_state", fake_tray_state)
    monkeypatch.setattr(agent_module, "SERVICE_MODE", "system")
    monkeypatch.setattr(agent_module, "_launch_replacement_agent_process", lambda mode: calls.append(mode) or True)
    monkeypatch.setattr(agent_module, "_shutdown_for_tray_restart", lambda: calls.append("shutdown"))
    monkeypatch.setattr(agent_module.time, "time", lambda: 1005)

    processed = agent_module._process_tray_restart_request_now()

    assert processed is True
    assert fake_tray_state.cleared == ["system"]
    assert calls == ["system", "shutdown"]


def test_replacement_launch_spec_uses_currentuser_config(monkeypatch) -> None:
    monkeypatch.setattr(agent_module, "__file__", "/runtime/Borealis/agent.py", raising=False)
    monkeypatch.setattr(
        agent_module.os.path,
        "isfile",
        lambda path: path in {
            "/runtime/Scripts/pythonw.exe",
            "/runtime/Borealis/agent.py",
        },
    )

    args, popen_kwargs = agent_module._replacement_launch_spec("currentuser", windows=True)

    assert args == [
        "/runtime/Scripts/pythonw.exe",
        "-W",
        "ignore::SyntaxWarning",
        "/runtime/Borealis/agent.py",
        "--config",
        "CURRENTUSER",
    ]
    assert popen_kwargs == {
        "cwd": "/runtime/Borealis",
        "stdin": agent_module.subprocess.DEVNULL,
        "stdout": agent_module.subprocess.DEVNULL,
        "stderr": agent_module.subprocess.DEVNULL,
        "creationflags": 0x08000208,
    }


def test_replacement_launch_spec_uses_service_wrapper_for_system_on_windows(monkeypatch) -> None:
    monkeypatch.setattr(agent_module, "__file__", "/runtime/Borealis/agent.py", raising=False)
    monkeypatch.setattr(
        agent_module.os.path,
        "expandvars",
        lambda value: "/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
    )
    monkeypatch.setattr(
        agent_module.os.path,
        "isfile",
        lambda path: path in {
            "/runtime/Borealis/launch_service.ps1",
            "/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
        },
    )

    args, popen_kwargs = agent_module._replacement_launch_spec("system", windows=True)

    assert args == [
        "/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-WindowStyle",
        "Hidden",
        "-File",
        "/runtime/Borealis/launch_service.ps1",
    ]
    assert popen_kwargs == {
        "cwd": "/runtime/Borealis",
        "stdin": agent_module.subprocess.DEVNULL,
        "stdout": agent_module.subprocess.DEVNULL,
        "stderr": agent_module.subprocess.DEVNULL,
        "creationflags": 0x08000208,
    }
