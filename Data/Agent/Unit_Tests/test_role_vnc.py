from __future__ import annotations

import threading
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


def test_health_report_uses_recent_ready_grace_for_flapping_listener(monkeypatch) -> None:
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
    role._last_ready_at = 95
    role._last_health_recover_at = 0
    role._trace = lambda *_args, **_kwargs: None
    role._ensure_always_on = lambda *, reason: recovery_calls.append(reason)
    role.vnc = SimpleNamespace(
        _resolve_service_name=lambda: "BorealisAgentUltraVNC",
        _service_state_by_name=lambda _service_name: "RUNNING",
        is_listener_ready=lambda _port: False,
        _last_service_error="",
    )
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)
    monkeypatch.setattr(vnc_role, "_collect_windows_display_topology", lambda: [])

    report = role.health_report()

    assert recovery_calls == []
    assert report["status"] == "healthy"
    assert report["details"]["listener_state"] == "recently_listening"
    assert report["details"]["ready"] == "true"


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


def test_health_report_throttles_recovery_when_listener_stays_down(monkeypatch) -> None:
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
    role._last_health_recover_at = 95
    role._trace = lambda *_args, **_kwargs: None
    role._ensure_always_on = lambda *, reason: recovery_calls.append(reason)
    role.vnc = SimpleNamespace(
        _resolve_service_name=lambda: "BorealisAgentUltraVNC",
        _service_state_by_name=lambda _service_name: "RUNNING",
        is_listener_ready=lambda _port: False,
        _last_service_error="",
    )
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)
    monkeypatch.setattr(vnc_role, "_collect_windows_display_topology", lambda: [])

    report = role.health_report()

    assert recovery_calls == []
    assert report["status"] == "recovering"
    assert report["details"]["listener_state"] == "not_listening"


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


def test_compute_ultravnc_password_hash_uses_stored_vnc_des_format() -> None:
    assert vnc_role._compute_ultravnc_password_hash("password") == "33483FD570CF869B"
    assert vnc_role._compute_ultravnc_password_hash("bootpass") == "2FEAEE5C3DA6B27C"


def test_normalize_ultravnc_password_hash_uses_eight_byte_blob() -> None:
    assert (
        vnc_role._normalize_ultravnc_password_hash("ff 97 50 2e 94 22 f0 89")
        == "FF97502E9422F089"
    )
    assert vnc_role._normalize_ultravnc_password_hash("FF97502E9422F089AA") == "FF97502E9422F089"


def test_apply_passwords_uses_internal_hash_when_tool_missing(monkeypatch, tmp_path) -> None:
    config_dir = tmp_path / "settings"
    config_dir.mkdir()
    config_path = config_dir / "ultravnc.ini"
    config_path.write_text("[UltraVNC]\nAuthRequired=1\n", encoding="ascii")
    logs: list[str] = []

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._password_tool = None
    manager._password_tool_logged = False
    manager._last_service_error = ""

    monkeypatch.setattr(vnc_role, "_resolve_vnc_password_tool", lambda _config_dir: None)
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))

    assert manager._apply_passwords(config_dir, config_path, "bootpass", None) == ("bootpass", None)

    raw = config_path.read_text(encoding="ascii")
    assert "passwd=2FEAEE5C3DA6B27C" in raw
    assert "passwd2=" in raw
    assert manager._last_service_error == ""
    assert any("internal hash generator" in entry for entry in logs)


def test_mirror_ultravnc_config_to_service_dir_copies_full_config(tmp_path) -> None:
    config_dir = tmp_path / "settings"
    config_dir.mkdir()
    source = config_dir / "ultravnc.ini"
    source.write_text("[UltraVNC]\nSocketConnect=1\nPortNumber=5900\npasswd=HASH\n", encoding="ascii")
    exe_dir = tmp_path / "server"
    exe_dir.mkdir()
    exe_path = exe_dir / "winvnc.exe"
    exe_path.write_text("stub", encoding="ascii")

    mirrored = vnc_role._mirror_ultravnc_config_to_service_dir(source, str(exe_path))

    assert mirrored == exe_dir / "ultravnc.ini"
    assert mirrored.read_text(encoding="ascii") == source.read_text(encoding="ascii")


def test_vnc_start_recovers_running_service_when_listener_missing(monkeypatch, tmp_path) -> None:
    restarts: list[str] = []
    waits: list[float] = []
    logs: list[str] = []
    service_config_paths: list[Path] = []
    config_dir = tmp_path / "settings"
    exe_dir = tmp_path / "server"
    config_dir.mkdir()
    exe_dir.mkdir()
    exe_path = exe_dir / "winvnc.exe"
    exe_path.write_text("stub", encoding="ascii")

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._lock = threading.RLock()
    manager._last_port = 5900
    manager._last_controller_password = "bootpass"
    manager._last_view_only_password = None
    manager._vnc_exe = str(exe_path)
    manager._vnc_root = exe_dir
    manager._password_tool = None
    manager._password_tool_logged = False
    manager._service_name = "BorealisAgentUltraVNC"
    manager._last_service_error = ""
    manager._last_listener_recovery_at = 0.0
    manager._ensure_firewall = lambda _allowed_ips, _port: None
    manager._resolve_service_name = lambda refresh=False: "BorealisAgentUltraVNC"
    manager._service_state_by_name = lambda _service_name: "RUNNING"
    manager._ensure_service_running = lambda config_path=None: service_config_paths.append(config_path) or True
    manager._restart_service = lambda: restarts.append("restart")
    manager._apply_passwords = lambda _config_dir, _config_path, _controller, _view_only: (
        "bootpass",
        None,
    )

    def _fake_wait_for_listener(_port, timeout=8.0):
        waits.append(float(timeout))
        return len(waits) >= 2

    manager._wait_for_listener = _fake_wait_for_listener
    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(vnc_role, "_resolve_vnc_config_dir", lambda: config_dir)
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))
    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)
    monkeypatch.setattr(vnc_role.time, "sleep", lambda _seconds: None)

    manager.start(
        port=5900,
        allowed_ips="10.255.0.1/32",
        controller_password="bootpass",
        view_only_password=None,
        reason="health_report_recover",
    )

    assert restarts == ["restart"]
    assert waits == [8.0, 10.0]
    assert service_config_paths == [exe_dir / "ultravnc.ini"]
    assert manager._last_service_error == ""
    assert "SocketConnect=1" in (exe_dir / "ultravnc.ini").read_text(encoding="ascii")
    assert any("listener not ready" in entry for entry in logs)


def test_vnc_start_uses_programdata_config_for_system_install(monkeypatch, tmp_path) -> None:
    service_config_paths: list[Path | None] = []
    logs: list[str] = []
    program_files = tmp_path / "Program Files"
    exe_dir = program_files / "uvnc bvba" / "UltraVNC"
    config_dir = tmp_path / "ProgramData" / "UltraVNC"
    exe_dir.mkdir(parents=True)
    config_dir.mkdir(parents=True)
    exe_path = exe_dir / "winvnc.exe"
    exe_path.write_text("stub", encoding="ascii")

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._lock = threading.RLock()
    manager._last_port = 5900
    manager._last_controller_password = "bootpass"
    manager._last_view_only_password = None
    manager._vnc_exe = str(exe_path)
    manager._vnc_root = exe_dir
    manager._password_tool = None
    manager._password_tool_logged = False
    manager._service_name = "BorealisAgentUltraVNC"
    manager._last_service_error = ""
    manager._last_listener_recovery_at = 0.0
    manager._ensure_firewall = lambda _allowed_ips, _port: None
    manager._resolve_service_name = lambda refresh=False: "BorealisAgentUltraVNC"
    manager._service_state_by_name = lambda _service_name: "RUNNING"
    manager._ensure_service_running = lambda config_path=None: service_config_paths.append(config_path) or True
    manager._restart_service = lambda: None
    manager._wait_for_listener = lambda _port, timeout=8.0: True
    manager._apply_passwords = lambda _config_dir, _config_path, _controller, _view_only: (
        "bootpass",
        None,
    )

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setenv("ProgramFiles", str(program_files))
    monkeypatch.setattr(vnc_role, "_resolve_vnc_config_dir", lambda: config_dir)
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))

    manager.start(
        port=5900,
        allowed_ips="10.255.0.1/32",
        controller_password="bootpass",
        view_only_password=None,
        reason="always_on_check",
    )

    assert service_config_paths == [None]
    assert not (exe_dir / "ultravnc.ini").exists()
    service_config = config_dir / "BorealisAgentUltraVNC.ini"
    assert service_config.exists()
    assert "SocketConnect=1" in service_config.read_text(encoding="ascii")
    assert any("ProgramData config paths" in entry for entry in logs)


def test_vnc_start_syncs_programdata_named_and_legacy_passwords(monkeypatch, tmp_path) -> None:
    program_files = tmp_path / "Program Files"
    exe_dir = program_files / "uvnc bvba" / "UltraVNC"
    config_dir = tmp_path / "ProgramData" / "UltraVNC"
    exe_dir.mkdir(parents=True)
    config_dir.mkdir(parents=True)
    exe_path = exe_dir / "winvnc.exe"
    exe_path.write_text("stub", encoding="ascii")

    legacy_config = config_dir / "ultravnc.ini"
    legacy_config.write_text(
        "[UltraVNC]\nUseRegistry=0\nAuthRequired=1\npasswd=OLDHASH\npasswd2=\n",
        encoding="ascii",
    )

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._lock = threading.RLock()
    manager._last_port = 5900
    manager._last_controller_password = "bootpass"
    manager._last_view_only_password = None
    manager._vnc_exe = str(exe_path)
    manager._vnc_root = exe_dir
    manager._password_tool = None
    manager._password_tool_logged = False
    manager._service_name = "BorealisAgentUltraVNC"
    manager._last_service_error = ""
    manager._last_listener_recovery_at = 0.0
    manager._ensure_firewall = lambda _allowed_ips, _port: None
    manager._resolve_service_name = lambda refresh=False: "BorealisAgentUltraVNC"
    manager._service_state_by_name = lambda _service_name: "RUNNING"
    manager._ensure_service_running = lambda config_path=None: True
    manager._restart_service = lambda: None
    manager._wait_for_listener = lambda _port, timeout=8.0: True

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setenv("ProgramFiles", str(program_files))
    monkeypatch.setattr(vnc_role, "_resolve_vnc_config_dir", lambda: config_dir)
    monkeypatch.setattr(vnc_role, "_resolve_vnc_password_tool", lambda _config_dir: None)
    monkeypatch.setattr(vnc_role, "_write_log", lambda _message: None)

    manager.start(
        port=5900,
        allowed_ips="10.255.0.1/32",
        controller_password="bootpass",
        view_only_password=None,
        reason="always_on_check",
    )

    service_config = config_dir / "BorealisAgentUltraVNC.ini"
    service_raw = service_config.read_text(encoding="ascii")
    legacy_raw = legacy_config.read_text(encoding="ascii")

    assert "passwd=2FEAEE5C3DA6B27C" in service_raw
    assert service_raw == legacy_raw
    assert not (exe_dir / "ultravnc.ini").exists()


def test_vnc_auth_retry_reloads_running_service_when_password_changes(monkeypatch, tmp_path) -> None:
    restarts: list[str] = []
    logs: list[str] = []
    program_files = tmp_path / "Program Files"
    exe_dir = program_files / "uvnc bvba" / "UltraVNC"
    config_dir = tmp_path / "ProgramData" / "UltraVNC"
    exe_dir.mkdir(parents=True)
    config_dir.mkdir(parents=True)
    exe_path = exe_dir / "winvnc.exe"
    exe_path.write_text("stub", encoding="ascii")

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._lock = threading.RLock()
    manager._last_port = 5900
    manager._last_controller_password = "oldpass"
    manager._last_view_only_password = None
    manager._vnc_exe = str(exe_path)
    manager._vnc_root = exe_dir
    manager._password_tool = None
    manager._password_tool_logged = False
    manager._service_name = "BorealisAgentUltraVNC"
    manager._last_service_error = ""
    manager._last_listener_recovery_at = 0.0
    manager._ensure_firewall = lambda _allowed_ips, _port: None
    manager._resolve_service_name = lambda refresh=False: "BorealisAgentUltraVNC"
    manager._service_state_by_name = lambda _service_name: "RUNNING"
    manager._ensure_service_running = lambda config_path=None: True
    manager._restart_service = lambda: restarts.append("restart")
    manager._wait_for_listener = lambda _port, timeout=8.0: True
    manager._apply_passwords = lambda _config_dir, _config_path, _controller, _view_only: (
        "bootpass",
        None,
    )

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setenv("ProgramFiles", str(program_files))
    monkeypatch.setattr(vnc_role, "_resolve_vnc_config_dir", lambda: config_dir)
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))

    manager.start(
        port=5900,
        allowed_ips="10.255.0.1/32",
        controller_password="bootpass",
        view_only_password=None,
        reason="vnc_auth_retry",
    )

    assert restarts == ["restart"]
    assert any("reload requested reason=vnc_auth_retry" in entry for entry in logs)
    assert any("force_reload=true" in entry for entry in logs)


def test_vnc_auth_retry_reloads_running_service_for_same_password(monkeypatch, tmp_path) -> None:
    restarts: list[str] = []
    program_files = tmp_path / "Program Files"
    exe_dir = program_files / "uvnc bvba" / "UltraVNC"
    config_dir = tmp_path / "ProgramData" / "UltraVNC"
    exe_dir.mkdir(parents=True)
    config_dir.mkdir(parents=True)
    exe_path = exe_dir / "winvnc.exe"
    exe_path.write_text("stub", encoding="ascii")

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._lock = threading.RLock()
    manager._last_port = 5900
    manager._last_controller_password = "bootpass"
    manager._last_view_only_password = None
    manager._vnc_exe = str(exe_path)
    manager._vnc_root = exe_dir
    manager._password_tool = None
    manager._password_tool_logged = False
    manager._service_name = "BorealisAgentUltraVNC"
    manager._last_service_error = ""
    manager._last_listener_recovery_at = 0.0
    manager._ensure_firewall = lambda _allowed_ips, _port: None
    manager._resolve_service_name = lambda refresh=False: "BorealisAgentUltraVNC"
    manager._service_state_by_name = lambda _service_name: "RUNNING"
    manager._ensure_service_running = lambda config_path=None: True
    manager._restart_service = lambda: restarts.append("restart")
    manager._wait_for_listener = lambda _port, timeout=8.0: True
    manager._apply_passwords = lambda _config_dir, _config_path, _controller, _view_only: (
        "bootpass",
        None,
    )

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setenv("ProgramFiles", str(program_files))
    monkeypatch.setattr(vnc_role, "_resolve_vnc_config_dir", lambda: config_dir)
    monkeypatch.setattr(vnc_role, "_write_log", lambda _message: None)

    manager.start(
        port=5900,
        allowed_ips="10.255.0.1/32",
        controller_password="bootpass",
        view_only_password=None,
        reason="vnc_auth_retry",
    )

    assert restarts == ["restart"]


def test_recover_listener_starts_stopped_service(monkeypatch, tmp_path) -> None:
    starts: list[Path] = []
    waits: list[float] = []
    logs: list[str] = []
    config_path = tmp_path / "server" / "ultravnc.ini"

    manager = vnc_role.VncManager.__new__(vnc_role.VncManager)
    manager._last_listener_recovery_at = 0.0
    manager._service_state_by_name = lambda _service_name: "STOPPED"
    manager._ensure_service_running = lambda config_path=None: starts.append(config_path) or True
    manager._wait_for_listener = lambda _port, timeout=8.0: waits.append(float(timeout)) or True
    manager._service_status_summary = lambda _service_name: "state=1 STOPPED win32_exit=1077"

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(vnc_role.time, "time", lambda: 100.0)
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))

    recovered = manager._recover_listener(
        5900,
        "BorealisAgentUltraVNC",
        "health_report_recover",
        config_path=config_path,
    )

    assert recovered is True
    assert starts == [config_path]
    assert waits == [10.0]
    assert any("service stopped" in entry for entry in logs)


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
