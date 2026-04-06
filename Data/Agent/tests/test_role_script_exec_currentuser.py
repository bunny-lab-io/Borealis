from __future__ import annotations

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

    class _FakeMessageBox:
        Yes = 1
        No = 0

        @staticmethod
        def question(*args, **kwargs) -> int:
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
