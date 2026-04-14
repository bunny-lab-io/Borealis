from __future__ import annotations

from types import SimpleNamespace
from pathlib import Path

import Data.Agent.Roles.role_VNC as vnc_role


class _CompletedProcess:
    def __init__(self, *, returncode: int, stderr: str = "") -> None:
        self.returncode = returncode
        self.stderr = stderr


def test_ensure_firewall_success_is_silent(monkeypatch) -> None:
    logs: list[str] = []
    calls: list[list[str]] = []
    manager = vnc_role.VncManager()

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(manager, "_normalize_firewall_remote", lambda _allowed_ips: "10.255.0.1/32")
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))

    def _fake_run(command, capture_output, text, check):
        calls.append(command)
        return _CompletedProcess(returncode=0)

    monkeypatch.setattr(vnc_role.subprocess, "run", _fake_run)

    manager._ensure_firewall("10.255.0.1/32", 5900)

    assert calls
    assert logs == []


def test_ensure_firewall_failure_still_logs(monkeypatch) -> None:
    logs: list[str] = []
    manager = vnc_role.VncManager()

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(manager, "_normalize_firewall_remote", lambda _allowed_ips: "10.255.0.1/32")
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))
    monkeypatch.setattr(
        vnc_role.subprocess,
        "run",
        lambda _command, capture_output, text, check: _CompletedProcess(
            returncode=1,
            stderr="boom",
        ),
    )

    manager._ensure_firewall("10.255.0.1/32", 5900)

    assert logs == ["Failed to ensure VNC firewall rule: boom"]


def test_sanitize_state_removes_legacy_password_fields(monkeypatch) -> None:
    saved_states: list[dict] = []
    role = vnc_role.Role.__new__(vnc_role.Role)
    role._state = {
        "password": "secret123",
        "controller_password": "secret456",
        "view_only_password": "secret789",
        "session_id": "session-1",
        "credential_revision": 3,
        "allowed_ips": "10.255.0.1/32",
    }

    monkeypatch.setattr(vnc_role, "_save_vnc_state", lambda state: saved_states.append(dict(state)))

    role._sanitize_state()

    assert "password" not in role._state
    assert "controller_password" not in role._state
    assert "view_only_password" not in role._state
    assert "session_id" not in role._state
    assert "credential_revision" not in role._state
    assert role._state["allowed_ips"] == "10.255.0.1/32"
    assert saved_states == [{"allowed_ips": "10.255.0.1/32"}]


def test_apply_bootstrap_payload_keeps_credentials_in_memory_only(monkeypatch) -> None:
    saved_states: list[dict] = []
    acquired: list[str] = []
    role = vnc_role.Role.__new__(vnc_role.Role)
    role._state = {}
    role._last_allowed_ips = None
    role._runtime_session = {
        "session_id": "",
        "controller_password": None,
        "view_only_password": None,
        "credential_revision": 0,
        "remove_wallpaper": True,
    }
    role._session_busy_lease = None

    monkeypatch.setattr(vnc_role, "_save_vnc_state", lambda state: saved_states.append(dict(state)))
    monkeypatch.setattr(role, "_acquire_session_busy", lambda reason: acquired.append(reason))
    monkeypatch.setattr(role, "_release_session_busy", lambda: None)

    role._apply_bootstrap_payload(
        {
            "session_id": "session-2",
            "controller_password": "abc12345",
            "view_only_password": "def67890",
            "credential_revision": 4,
            "allowed_ips": "10.255.0.1/32",
            "vnc_port": 5900,
            "remove_wallpaper": False,
            "reason": "bootstrap_restore",
        }
    )

    assert role._state == {
        "allowed_ips": "10.255.0.1/32",
        "port": 5900,
        "remove_wallpaper": False,
    }
    assert role._runtime_session["session_id"] == "session-2"
    assert role._runtime_session["controller_password"] == "abc12345"
    assert role._runtime_session["view_only_password"] == "def67890"
    assert acquired == ["bootstrap_restore"]
    assert all("password" not in snapshot for snapshot in saved_states)


def test_health_report_requires_listener_readiness_for_healthy_status(monkeypatch) -> None:
    role = vnc_role.Role.__new__(vnc_role.Role)
    role.role_health_label = "UltraVNC Service"
    role._always_on_thread = SimpleNamespace(is_alive=lambda: True)
    role._engine_ready_for_vnc = True
    role._state = {"port": 5900, "remove_wallpaper": True}
    role._runtime_session = {
        "session_id": "session-3",
        "controller_password": "abc12345",
        "view_only_password": "def67890",
        "credential_revision": 5,
        "remove_wallpaper": True,
    }
    role._last_ready_at = 0
    role.vnc = SimpleNamespace(
        _resolve_service_name=lambda: "uvnc_service",
        _service_state_by_name=lambda _service_name: "RUNNING",
        is_listener_ready=lambda _port: False,
    )
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)

    report = role.health_report()

    assert report["status"] == "recovering"
    assert report["details"]["service_state"] == "RUNNING"
    assert report["details"]["listener_state"] == "not_listening"
    assert report["details"]["ready"] == "false"


def test_write_ultravnc_config_preserves_both_password_keys_in_ultravnc_section(tmp_path) -> None:
    config_path = tmp_path / "ultravnc.ini"

    assert vnc_role._write_ultravnc_config(
        config_path,
        {
            "UseRegistry": "0",
            "AuthRequired": "1",
            "passwd": "HASHONE",
        },
    )
    assert vnc_role._write_ultravnc_config(
        config_path,
        {
            "passwd2": "HASHTWO",
        },
    )

    raw = config_path.read_text(encoding="ascii")

    assert "[UltraVNC]" in raw
    assert "UseRegistry=0" in raw
    assert "AuthRequired=1" in raw
    assert "passwd=HASHONE" in raw
    assert "passwd2=HASHTWO" in raw
    assert raw.count("[UltraVNC]") == 1


def test_apply_ultravnc_password_hash_keeps_primary_password_when_setting_passwd2(tmp_path) -> None:
    config_path = tmp_path / "ultravnc.ini"
    config_path.write_text(
        "[UltraVNC]\nUseRegistry=0\nAuthRequired=1\npasswd=HASHONE\n",
        encoding="ascii",
    )

    assert vnc_role._apply_ultravnc_password_hash(config_path, "HASHTWO", key="passwd2")

    raw = config_path.read_text(encoding="ascii")

    assert "[UltraVNC]" in raw
    assert "passwd=HASHONE" in raw
    assert "passwd2=HASHTWO" in raw


def test_read_ultravnc_password_hash_uses_temp_scratch_not_live_config(monkeypatch, tmp_path) -> None:
    config_dir = tmp_path / "server"
    config_dir.mkdir()
    live_config = config_dir / "ultravnc.ini"
    live_config.write_text("[UltraVNC]\npasswd=LIVEHASH\n", encoding="ascii")
    tool_path = config_dir / "createpassword.exe"
    tool_path.write_text("stub", encoding="ascii")

    class _CompletedProcess:
        returncode = 0
        stdout = ""
        stderr = ""

    def _fake_run(command, cwd, capture_output, text, check):
        scratch_dir = Path(cwd)
        (scratch_dir / "UltraVNC.ini").write_text("[UltraVNC]\npasswd=NEWHASH\n", encoding="ascii")
        return _CompletedProcess()

    monkeypatch.setattr(vnc_role.subprocess, "run", _fake_run)

    hash_value, secure_value = vnc_role._read_ultravnc_password_hash(
        str(tool_path),
        "abc12345",
        config_dir,
    )

    assert hash_value == "NEWHASH"
    assert secure_value is None
    assert live_config.read_text(encoding="ascii") == "[UltraVNC]\npasswd=LIVEHASH\n"
