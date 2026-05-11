from __future__ import annotations

from types import SimpleNamespace
from pathlib import Path

import Data.Agent.Roles.role_system_vnc as vnc_role


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


def test_ensure_ultravnc_ini_enables_loopback_health_probe(tmp_path) -> None:
    config_path = tmp_path / "ultravnc.ini"

    assert vnc_role._ensure_ultravnc_ini(config_path, 5900)

    raw = config_path.read_text(encoding="utf-8")
    assert "SocketConnect=1" in raw
    assert "AllowLoopback=1" in raw
    assert "LoopbackOnly=0" in raw


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
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 88,
    }
    role._runtime_session = {
        "session_id": "",
        "controller_password": "bootpass",
        "view_only_password": None,
        "credential_revision": 88,
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
    assert role._runtime_session["controller_password"] == "bootpass"
    assert role._runtime_session["view_only_password"] is None
    assert role._runtime_session["credential_revision"] == 88
    assert acquired == ["bootstrap_restore"]
    assert all("password" not in snapshot for snapshot in saved_states)


def test_health_report_requires_listener_readiness_for_healthy_status(monkeypatch) -> None:
    role = vnc_role.Role.__new__(vnc_role.Role)
    role.role_health_label = "UltraVNC Service"
    role._always_on_thread = SimpleNamespace(is_alive=lambda: True)
    role._engine_ready_for_vnc = True
    role._state = {"port": 5900, "remove_wallpaper": True}
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 88,
    }
    role._runtime_session = {
        "session_id": "session-3",
        "controller_password": "bootpass",
        "view_only_password": None,
        "credential_revision": 88,
        "remove_wallpaper": True,
    }
    role._last_ready_at = 0
    role.vnc = SimpleNamespace(
        _resolve_service_name=lambda: "BorealisAgentUltraVNC",
        _service_state_by_name=lambda _service_name: "RUNNING",
        is_listener_ready=lambda _port: False,
    )
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)

    report = role.health_report()

    assert report["status"] == "recovering"
    assert report["details"]["service_state"] == "RUNNING"
    assert report["details"]["listener_state"] == "not_listening"
    assert report["details"]["ready"] == "false"


def test_health_report_is_healthy_without_active_session_when_listener_is_ready(monkeypatch) -> None:
    role = vnc_role.Role.__new__(vnc_role.Role)
    role.role_health_label = "UltraVNC Service"
    role._always_on_thread = SimpleNamespace(is_alive=lambda: True)
    role._engine_ready_for_vnc = True
    role._state = {"port": 5900, "remove_wallpaper": True}
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 88,
    }
    role._runtime_session = {
        "session_id": "",
        "controller_password": "bootpass",
        "view_only_password": None,
        "credential_revision": 88,
        "remove_wallpaper": True,
    }
    role._last_ready_at = 0
    role._disconnect_grace = {
        "deadline": 0.0,
        "controller_password": None,
        "view_only_password": None,
        "allowed_ips": None,
        "port": 5900,
        "remove_wallpaper": True,
        "reason": "",
    }
    role.vnc = SimpleNamespace(
        _resolve_service_name=lambda: "BorealisAgentUltraVNC",
        _service_state_by_name=lambda _service_name: "RUNNING",
        is_listener_ready=lambda _port: True,
    )
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(
        vnc_role,
        "_collect_windows_display_topology",
        lambda: [
            {
                "id": "1",
                "display_index": 1,
                "label": "1",
                "device_name": "\\\\.\\DISPLAY1",
                "left": 0,
                "top": 0,
                "right": 1920,
                "bottom": 1080,
                "width": 1920,
                "height": 1080,
                "work_left": 0,
                "work_top": 0,
                "work_right": 1920,
                "work_bottom": 1040,
                "work_width": 1920,
                "work_height": 1040,
                "primary": True,
            }
        ],
    )

    report = role.health_report()

    assert report["status"] == "healthy"
    assert report["details"]["listener_state"] == "listening"
    assert report["details"]["ready"] == "true"
    assert report["detail"] == "BorealisAgentUltraVNC listener is ready for always-on access."
    assert "\"display_index\": 1" in report["details"]["display_topology_json"]
    assert "\"width\": 1920" in report["details"]["display_virtual_bounds_json"]


def test_health_report_triggers_recovery_when_service_stopped(monkeypatch) -> None:
    recovery_calls: list[str] = []
    role = vnc_role.Role.__new__(vnc_role.Role)
    role.role_health_label = "UltraVNC Service"
    role._always_on_thread = SimpleNamespace(is_alive=lambda: True)
    role._engine_ready_for_vnc = True
    role._state = {"allowed_ips": "10.255.0.1/32", "port": 5900, "remove_wallpaper": True}
    role._last_allowed_ips = "10.255.0.1/32"
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 88,
    }
    role._runtime_session = {
        "session_id": "",
        "controller_password": "bootpass",
        "view_only_password": None,
        "credential_revision": 88,
        "remove_wallpaper": True,
    }
    role._disconnect_grace = {
        "deadline": 0.0,
        "controller_password": None,
        "view_only_password": None,
        "allowed_ips": None,
        "port": 5900,
        "remove_wallpaper": True,
        "reason": "",
    }
    role._last_ready_at = 0
    role._trace = lambda *_args, **_kwargs: None
    role._ensure_always_on = lambda *, reason: recovery_calls.append(reason)
    role.vnc = SimpleNamespace(
        _resolve_service_name=lambda: "BorealisAgentUltraVNC",
        _service_state_by_name=lambda _service_name: "STOPPED",
        is_listener_ready=lambda _port: False,
        _last_service_error="",
    )
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(vnc_role, "_collect_windows_display_topology", lambda: [])

    report = role.health_report()

    assert recovery_calls == ["health_report_recover"]
    assert report["status"] == "recovering"
    assert report["details"]["service_state"] == "STOPPED"


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


def _role_for_disconnect_grace() -> vnc_role.Role:
    role = vnc_role.Role.__new__(vnc_role.Role)
    role.role_health_label = "UltraVNC Service"
    role._log = lambda *_args, **_kwargs: None
    role._state = {"allowed_ips": "10.255.0.1/32", "port": 5900, "remove_wallpaper": False}
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 88,
    }
    role._last_allowed_ips = "10.255.0.1/32"
    role._engine_ready_for_vnc = True
    role._engine_wait_logged = False
    role._missing_password_logged = False
    role._last_ready_at = 0
    role._always_on_thread = SimpleNamespace(is_alive=lambda: True)
    role._session_busy_lease = None
    role._runtime_session = {
        "session_id": "session-1",
        "controller_password": "bootpass",
        "view_only_password": None,
        "credential_revision": 88,
        "remove_wallpaper": False,
    }
    role._disconnect_grace = {
        "deadline": 0.0,
        "controller_password": None,
        "view_only_password": None,
        "allowed_ips": None,
        "port": 5900,
        "remove_wallpaper": False,
        "reason": "",
    }
    return role


def test_ensure_always_on_uses_disconnect_grace_credentials_after_soft_disconnect(monkeypatch) -> None:
    start_calls: list[dict] = []
    standby_calls: list[str] = []
    role = _role_for_disconnect_grace()
    role.vnc = SimpleNamespace(
        start=lambda **kwargs: start_calls.append(dict(kwargs)),
        ensure_standby=lambda *, reason="standby": standby_calls.append(reason),
        is_listener_ready=lambda _port: True,
    )

    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)

    assert role._schedule_disconnect_grace("operator_disconnect") is True

    role._clear_runtime_session()
    role._ensure_always_on(reason="disconnect_grace")

    assert standby_calls == []
    assert len(start_calls) == 1
    assert start_calls[0]["controller_password"] == "bootpass"
    assert start_calls[0]["view_only_password"] is None
    assert start_calls[0]["allowed_ips"] == "10.255.0.1/32"
    assert start_calls[0]["remove_wallpaper"] is False
    assert start_calls[0]["reason"] == "disconnect_grace"


def test_disconnect_grace_expiry_keeps_listener_running(monkeypatch) -> None:
    start_calls: list[dict] = []
    standby_calls: list[str] = []
    role = _role_for_disconnect_grace()
    role.vnc = SimpleNamespace(
        start=lambda **kwargs: start_calls.append(dict(kwargs)),
        ensure_standby=lambda *, reason="standby": standby_calls.append(reason),
        is_listener_ready=lambda _port: False,
    )

    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)
    assert role._schedule_disconnect_grace("operator_disconnect") is True

    role._clear_runtime_session()

    monkeypatch.setattr(vnc_role.time, "time", lambda: 200.0)
    role._ensure_always_on(reason="always_on_check")

    assert len(start_calls) == 1
    assert start_calls[0]["controller_password"] == "bootpass"
    assert start_calls[0]["allowed_ips"] == "10.255.0.1/32"
    assert start_calls[0]["reason"] == "always_on_check"
    assert standby_calls == []
    assert role._disconnect_grace_active(now=200.0) is False


def test_ensure_always_on_keeps_listener_running_without_active_session(monkeypatch) -> None:
    start_calls: list[dict] = []
    standby_calls: list[str] = []
    role = _role_for_disconnect_grace()
    role._runtime_session = {
        "session_id": "",
        "controller_password": "bootpass",
        "view_only_password": None,
        "credential_revision": 88,
        "remove_wallpaper": False,
    }
    role.vnc = SimpleNamespace(
        start=lambda **kwargs: start_calls.append(dict(kwargs)),
        ensure_standby=lambda *, reason="standby": standby_calls.append(reason),
        is_listener_ready=lambda _port: True,
    )

    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)

    role._ensure_always_on(reason="always_on_check")

    assert len(start_calls) == 1
    assert start_calls[0]["controller_password"] == "bootpass"
    assert start_calls[0]["allowed_ips"] == "10.255.0.1/32"
    assert start_calls[0]["reason"] == "always_on_check"
    assert standby_calls == []


def test_request_vnc_bootstrap_advertises_runtime_credential(monkeypatch) -> None:
    requests: list[tuple[str, dict, bool]] = []

    class _Client:
        def post_json(self, path, payload, require_auth):
            requests.append((path, dict(payload), bool(require_auth)))
            return {"status": "ok"}

    role = vnc_role.Role.__new__(vnc_role.Role)
    role.ctx = SimpleNamespace(agent_id="agent-1")
    role._log = lambda *_args, **_kwargs: None
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 12345,
    }
    role._http_client = lambda: _Client()
    monkeypatch.setattr(
        vnc_role,
        "_collect_windows_display_topology",
        lambda: [
            {
                "id": "1",
                "display_index": 1,
                "label": "1",
                "device_name": "\\\\.\\DISPLAY1",
                "left": 0,
                "top": 0,
                "right": 1920,
                "bottom": 1080,
                "width": 1920,
                "height": 1080,
                "work_left": 0,
                "work_top": 0,
                "work_right": 1920,
                "work_bottom": 1040,
                "work_width": 1920,
                "work_height": 1040,
                "primary": True,
            }
        ],
    )

    payload = role._request_vnc_bootstrap("agent_boot")

    assert payload == {"status": "ok"}
    assert requests == [
        (
            "/api/agent/vnc/ensure",
            {
                "agent_id": "agent-1",
                "reason": "agent_boot",
                "controller_password": "bootpass",
                "credential_revision": 12345,
                "display_topology": [
                    {
                        "id": "1",
                        "display_index": 1,
                        "label": "1",
                        "device_name": "\\\\.\\DISPLAY1",
                        "left": 0,
                        "top": 0,
                        "right": 1920,
                        "bottom": 1080,
                        "width": 1920,
                        "height": 1080,
                        "work_left": 0,
                        "work_top": 0,
                        "work_right": 1920,
                        "work_bottom": 1040,
                        "work_width": 1920,
                        "work_height": 1040,
                        "primary": True,
                    }
                ],
                "display_virtual_bounds": {
                    "left": 0,
                    "top": 0,
                    "right": 1920,
                    "bottom": 1080,
                    "width": 1920,
                    "height": 1080,
                },
            },
            True,
        )
    ]


def test_runtime_credential_due_after_rotation_window(monkeypatch) -> None:
    role = vnc_role.Role.__new__(vnc_role.Role)
    role._agent_runtime_credentials = {
        "controller_password": "bootpass",
        "credential_revision": 12345,
        "issued_at": 100.0,
    }

    monkeypatch.setattr(vnc_role, "VNC_CREDENTIAL_ROTATION_SECONDS", 60)

    assert role._runtime_credential_due(now=159.0) is False
    assert role._runtime_credential_due(now=160.0) is True


def test_rotate_runtime_credential_refreshes_password_revision_and_runtime_session(monkeypatch) -> None:
    logs: list[str] = []
    role = _role_for_disconnect_grace()
    role._log = lambda message, **_kwargs: logs.append(message)
    role._disconnect_grace = {
        "deadline": 999.0,
        "controller_password": "bootpass",
        "view_only_password": None,
        "allowed_ips": "10.255.0.1/32",
        "port": 5900,
        "remove_wallpaper": False,
        "reason": "operator_disconnect",
    }

    monkeypatch.setattr(
        vnc_role,
        "_new_runtime_vnc_credential",
        lambda now=None: {
            "controller_password": "nextpass",
            "credential_revision": 99999,
            "issued_at": 500.0 if now is None else float(now),
        },
    )

    role._rotate_runtime_credential(reason="scheduled_rotation", now=500.0)

    assert role._agent_runtime_credentials["controller_password"] == "nextpass"
    assert role._agent_runtime_credentials["credential_revision"] == 99999
    assert role._runtime_session["controller_password"] == "nextpass"
    assert role._runtime_session["credential_revision"] == 99999
    assert role._disconnect_grace["deadline"] == 0.0
    assert logs == ["VNC runtime credential rotated (reason=scheduled_rotation)."]
