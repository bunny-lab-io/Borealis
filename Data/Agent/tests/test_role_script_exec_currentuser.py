from __future__ import annotations

import asyncio
import types
from pathlib import Path

import Data.Agent.tray_state as tray_state
import Data.Agent.Roles.role_ScriptExec_CURRENTUSER as role_module


class _FakeClipboard:
    def __init__(self) -> None:
        self.text = ""

    def setText(self, value: str) -> None:
        self.text = value


class _FakeApp:
    def __init__(self, clipboard: _FakeClipboard) -> None:
        self._clipboard = clipboard

    def clipboard(self) -> _FakeClipboard:
        return self._clipboard


def _setup_start_path(tmp_path: Path) -> Path:
    (tmp_path / "Borealis.ps1").write_text("", encoding="utf-8")
    start = tmp_path / "Data" / "Agent"
    start.mkdir(parents=True, exist_ok=True)
    return start


def test_restart_agent_writes_both_restart_requests(monkeypatch, tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    prompts: list[str] = []

    class _FakeMessageBox:
        Yes = 1
        No = 0

        @staticmethod
        def question(*args, **kwargs) -> int:
            if len(args) >= 3:
                prompts.append(str(args[2]))
            return _FakeMessageBox.Yes

    monkeypatch.setattr(
        role_module,
        "QtWidgets",
        types.SimpleNamespace(QMessageBox=_FakeMessageBox),
    )
    monkeypatch.setattr(
        role_module.tray_state,
        "request_restart",
        lambda service_modes, requested_by, requested_by_pid: tray_state.request_restart(
            service_modes,
            start=start,
            requested_by=requested_by,
            requested_by_pid=requested_by_pid,
        ),
    )

    role = role_module.Role.__new__(role_module.Role)
    role.ctx = type("Ctx", (), {"hooks": {"process_restart_request_now": lambda: True}})()
    role._restart_pending = False
    role._refresh_tray_view = lambda: None
    role._spawn_currentuser_agent = lambda: True
    role._exit_app = lambda: None

    role._restart_agent()

    assert role._restart_pending is True
    assert tray_state.load_restart_request("currentuser", start=start)["service_mode"] == "currentuser"
    assert tray_state.load_restart_request("system", start=start)["service_mode"] == "system"
    assert prompts == [
        "Restart the Borealis Agent now?\n\nRemote support activity may pause briefly while the agent reconnects.\n\nPlease wait up to 1 minute for the agent restart request to trigger."
    ]


def test_copy_support_details_uses_clipboard(monkeypatch) -> None:
    clipboard = _FakeClipboard()
    app = _FakeApp(clipboard)
    fake_qapplication = type(
        "QApplication",
        (),
        {"instance": staticmethod(lambda: app)},
    )
    monkeypatch.setattr(
        role_module,
        "QtWidgets",
        types.SimpleNamespace(QApplication=fake_qapplication),
    )

    role = role_module.Role.__new__(role_module.Role)
    role._copy_support_details({"support_text": "Device: workstation-01"})

    assert clipboard.text == "Device: workstation-01"


def test_build_status_details_text_includes_wireguard_and_logs() -> None:
    role = role_module.Role.__new__(role_module.Role)

    text = role._build_status_details_text(
        {
            "support_text": "Device: workstation-01\nStatus: Connected",
            "wireguard_detail": "Persistent tunnel active.",
            "logs_dir": "/tmp/Agent/Logs",
        }
    )

    assert "Device: workstation-01" in text
    assert "WireGuard Detail: Persistent tunnel active." in text
    assert "Logs Folder: /tmp/Agent/Logs" in text


def test_open_logs_folder_uses_subprocess_on_non_windows(monkeypatch) -> None:
    popen_calls: list[list[str]] = []
    monkeypatch.setattr(role_module.subprocess, "Popen", lambda args: popen_calls.append(list(args)))

    role = role_module.Role.__new__(role_module.Role)
    role._open_logs_folder({"logs_dir": "/tmp/Agent/Logs"})

    assert popen_calls == [["xdg-open", "/tmp/Agent/Logs"]]


def test_spawn_currentuser_agent_uses_currentuser_config(monkeypatch) -> None:
    popen_calls: list[tuple[list[str], dict]] = []
    monkeypatch.setattr(
        role_module,
        "__file__",
        "/runtime/Borealis/Roles/role_ScriptExec_CURRENTUSER.py",
        raising=False,
    )
    monkeypatch.setattr(
        role_module.os.path,
        "isfile",
        lambda path: path in {
            "/runtime/Scripts/pythonw.exe",
            "/runtime/Borealis/agent.py",
        },
    )
    monkeypatch.setattr(
        role_module.subprocess,
        "Popen",
        lambda args, **kwargs: popen_calls.append((list(args), dict(kwargs))),
    )

    role = role_module.Role.__new__(role_module.Role)

    assert role._spawn_currentuser_agent() is True
    assert popen_calls == [
        (
            [
                "/runtime/Scripts/pythonw.exe",
                "-W",
                "ignore::SyntaxWarning",
                "/runtime/Borealis/agent.py",
                "--config",
                "CURRENTUSER",
            ],
            {
                "cwd": "/runtime/Borealis",
                "stdin": role_module.subprocess.DEVNULL,
                "stdout": role_module.subprocess.DEVNULL,
                "stderr": role_module.subprocess.DEVNULL,
                "start_new_session": True,
            },
        )
    ]


def test_restart_agent_on_windows_exits_after_requesting_restart(monkeypatch, tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)

    class _FakeMessageBox:
        Yes = 1
        No = 0

        @staticmethod
        def question(*args, **kwargs) -> int:
            return _FakeMessageBox.Yes

    monkeypatch.setattr(role_module, "IS_WINDOWS", True)
    monkeypatch.setattr(
        role_module,
        "QtWidgets",
        types.SimpleNamespace(QMessageBox=_FakeMessageBox),
    )
    monkeypatch.setattr(
        role_module.tray_state,
        "request_restart",
        lambda service_modes, requested_by, requested_by_pid: tray_state.request_restart(
            service_modes,
            start=start,
            requested_by=requested_by,
            requested_by_pid=requested_by_pid,
        ),
    )

    calls: list[str] = []
    role = role_module.Role.__new__(role_module.Role)
    role.ctx = type("Ctx", (), {"hooks": {"process_restart_request_now": lambda: calls.append("processor") or True}})()
    role._restart_pending = False
    role._refresh_tray_view = lambda: calls.append("refresh")
    role._spawn_currentuser_agent = lambda: calls.append("spawn") or True
    role._exit_app = lambda: calls.append("exit")

    role._restart_agent()

    assert tray_state.load_restart_request("currentuser", start=start)["service_mode"] == "currentuser"
    assert tray_state.load_restart_request("system", start=start)["service_mode"] == "system"
    assert calls == ["refresh", "exit"]


def test_register_events_uses_helper_handler_when_present() -> None:
    registered = {}

    role = role_module.Role.__new__(role_module.Role)
    role.ctx = type(
        "Ctx",
        (),
        {
            "sio": object(),
            "hooks": {
                "register_local_helper_handler": lambda handler: registered.setdefault("handler", handler),
            },
        },
    )()
    role._listener_registered = False

    role.register_events()

    assert role._listener_registered is True
    assert callable(registered.get("handler"))


def test_handle_quick_job_run_trusts_broker_verified_payload(monkeypatch) -> None:
    async def _fake_ps(content, env_map, timeout_seconds):
        return 0, "hello from helper", ""

    monkeypatch.setattr(role_module, "_run_powershell_script_content", _fake_ps)

    role = role_module.Role.__new__(role_module.Role)
    role.ctx = type("Ctx", (), {"hooks": {}})()
    role._listener_registered = True

    payload = {
        "job_id": 14,
        "target_hostname": "",
        "script_type": "powershell",
        "run_mode": "currentuser",
        "script_content": "V3JpdGUtT3V0cHV0ICdoZWxsbycK",
        "script_encoding": "base64",
        "broker_verified": True,
        "context": {"source": "test"},
    }

    result = asyncio.run(role._handle_quick_job_run(payload))

    assert result == {
        "job_id": 14,
        "status": "Success",
        "stdout": "hello from helper",
        "stderr": "",
        "context": {"source": "test"},
    }
