from __future__ import annotations

import threading
from pathlib import Path

import Data.Agent.Roles.role_WireGuardTunnel as wireguard_role
from Data.Agent.Roles.role_WireGuardTunnel import (
    LinuxWireGuardClient,
    Role,
    SessionConfig,
    WireGuardClient,
    _session_config_equivalent,
)


def _build_session(*, endpoint: str = "borealis.bunny-lab.io:30000") -> SessionConfig:
    return SessionConfig(
        token={"agent_id": "agent-1", "tunnel_id": "tunnel-1", "expires_at": 9999999999, "port": 30000},
        tunnel_id="tunnel-1",
        virtual_ip="10.255.0.15/32",
        allowed_ips="10.255.0.1/32",
        endpoint=endpoint,
        server_public_key="server-public-key",
        allowed_ports="47002,5900",
    )


def test_session_config_equivalent_detects_endpoint_drift() -> None:
    current = _build_session(endpoint="borealis.bunny-lab.io:30000")
    desired = _build_session(endpoint="192.168.3.252:30000")
    assert _session_config_equivalent(current, current) is True
    assert _session_config_equivalent(current, desired) is False


def test_role_resolve_endpoint_prefers_internal_server_url_host() -> None:
    role = Role.__new__(Role)
    role._get_server_url = lambda: "https://192.168.3.252:5000"
    log_messages: list[str] = []
    role._log = lambda message, *, error=False: log_messages.append(message)

    resolved = role._resolve_endpoint(
        "borealis.bunny-lab.io:30000",
        {"port": 30000},
    )

    assert resolved == "192.168.3.252:30000"
    assert any("WireGuard endpoint override" in message for message in log_messages)


def test_role_session_config_matches_live_snapshot() -> None:
    role = Role.__new__(Role)
    role._read_live_config_snapshot = lambda: {
        "virtual_ip": "10.255.0.15/32",
        "endpoint": "192.168.3.252:30000",
        "active_config": True,
    }

    assert role._session_config_matches_live(_build_session(endpoint="192.168.3.252:30000")) is True
    assert role._session_config_matches_live(_build_session(endpoint="borealis.bunny-lab.io:30000")) is False


def test_role_build_session_honors_force_restart_flag() -> None:
    role = Role.__new__(Role)
    role.ctx = type("Ctx", (), {"agent_id": "agent-1"})()
    role._remember_tunnel_snapshot = lambda payload: None
    role._resolve_endpoint = lambda endpoint, token: endpoint
    role._log = lambda message, *, error=False: None

    session = role._build_session(
        {
            "agent_id": "agent-1",
            "token": {"agent_id": "agent-1", "tunnel_id": "tunnel-1", "expires_at": 9999999999, "port": 30000},
            "tunnel_id": "tunnel-1",
            "virtual_ip": "10.255.0.15/32",
            "allowed_ips": "10.255.0.1/32",
            "endpoint": "borealis.bunny-lab.io:30000",
            "server_public_key": "server-public-key",
            "allowed_ports": [47002, 5900],
            "force_restart": True,
            "restart_reason": "shell_connect_retry",
        }
    )

    assert session is not None
    assert session.force_restart is True
    assert session.restart_reason == "shell_connect_retry"


def test_linux_client_force_restart_skips_same_session_reuse() -> None:
    client = LinuxWireGuardClient.__new__(LinuxWireGuardClient)
    client._session_lock = threading.Lock()
    client.session = _build_session()
    client.conf_path = None
    client._client_keys = {"private": "client-private"}
    calls: list[str] = []

    client._service_state = lambda: "RUNNING"
    client._validate_token = lambda token, signing_client=None: None
    client._write_config = lambda text: calls.append("write_config") or True
    client._bring_down = lambda: calls.append("bring_down")
    client._bring_up = lambda: calls.append("bring_up") or True

    forced = _build_session()
    forced.force_restart = True
    forced.restart_reason = "shell_connect_retry"

    client.start_session(forced, signing_client=None)

    assert calls == ["write_config", "bring_down", "bring_up"]


def _build_windows_client() -> WireGuardClient:
    client = WireGuardClient.__new__(WireGuardClient)
    client._session_lock = threading.Lock()
    client._stop_event = threading.Event()
    client._client_keys = {"private": "client-private"}
    client._wg_exe = "wireguard.exe"
    client._last_install_already_present = False
    client.service_name = "Borealis"
    client.display_name = "Borealis"
    client.service_display_name = "Borealis - WireGuard - Agent"
    client.conf_path = Path("/tmp/Borealis.conf")
    client.session = None
    client.idle_deadline = None
    return client


def test_windows_client_repairs_stale_service_binding() -> None:
    client = _build_windows_client()
    calls: list[str] = []
    states = iter(["RUNNING"])

    client._validate_token = lambda token, signing_client=None: None
    client._write_config = lambda text: calls.append("write_config") or True
    client._write_config_to = lambda path, text: True
    client._service_exists = lambda: True
    client._service_config_path = lambda: Path("D:/Github/Borealis/Agent/Borealis/Settings/WireGuard/Borealis.conf")
    client._reinstall_service = lambda: calls.append("reinstall") or True
    client._restart_service = lambda: calls.append("restart") or True
    client._service_state = lambda: next(states)
    client._ensure_adapter_name = lambda: calls.append("adapter")
    client._ensure_service_display_name = lambda: calls.append("display")
    client._ensure_shell_firewall = lambda allowed_ips, allowed_ports: calls.append("firewall")
    client._log_recovery_event = lambda *args, **kwargs: None

    session = _build_session()
    client.start_session(session, signing_client=None)

    assert calls == ["write_config", "reinstall", "restart", "adapter", "display", "firewall"]
    assert client.session is session


def test_windows_client_requires_healthy_service_before_marking_session_started() -> None:
    client = _build_windows_client()
    calls: list[str] = []
    states = iter(["STOPPED", "STOPPED"])

    client._validate_token = lambda token, signing_client=None: None
    client._write_config = lambda text: calls.append("write_config") or True
    client._service_exists = lambda: True
    client._service_config_path = lambda: client.conf_path
    client._restart_service = lambda: calls.append("restart") or True
    client._reinstall_service = lambda: calls.append("reinstall") or True
    client._service_state = lambda: next(states)
    client._ensure_adapter_name = lambda: calls.append("adapter")
    client._ensure_service_display_name = lambda: calls.append("display")
    client._ensure_shell_firewall = lambda allowed_ips, allowed_ports: calls.append("firewall")
    client._log_recovery_event = lambda *args, **kwargs: None

    client.start_session(_build_session(), signing_client=None)

    assert calls == ["write_config", "restart", "reinstall", "restart"]
    assert client.session is None


def test_role_run_ensure_cycle_uses_requested_reason() -> None:
    role = Role.__new__(Role)
    role._ensure_cycle_lock = threading.Lock()
    captured: list[tuple[str, object]] = []
    session = _build_session()

    class _Client:
        session = None

        @staticmethod
        def _service_state() -> None:
            return None

        @staticmethod
        def start_session(start_session_obj, signing_client=None) -> None:
            captured.append(("start_session", start_session_obj))
            captured.append(("signing_client", signing_client))

    role.client = _Client()
    role._request_persistent_session = lambda *, reason="agent_boot": captured.append(("reason", reason)) or {"ok": True}
    role._build_session = lambda payload: session
    role._session_config_matches_live = lambda current: False
    role._http_client = lambda: "signing-client"
    role._log = lambda message, *, error=False: None

    role._run_ensure_cycle(reason="socket_connect")

    assert ("reason", "socket_connect") in captured
    assert ("start_session", session) in captured
    assert ("signing_client", "signing-client") in captured


def test_role_run_ensure_cycle_safe_logs_exceptions() -> None:
    role = Role.__new__(Role)
    captured: list[tuple[str, bool]] = []
    role._run_ensure_cycle = lambda *, reason="agent_boot": (_ for _ in ()).throw(RuntimeError("boom"))
    role._log = lambda message, *, error=False: captured.append((message, error))

    role._run_ensure_cycle_safe(reason="socket_connect", source="immediate")

    assert any("source=immediate" in message and "reason=socket_connect" in message for message, _ in captured)
    assert any(error for _, error in captured)


def test_role_request_immediate_ensure_starts_supervisor_and_runs_cycle() -> None:
    role = Role.__new__(Role)
    role._ensure_stop = threading.Event()
    events: list[tuple[str, str]] = []
    original_thread = wireguard_role.threading.Thread

    class _ImmediateThread:
        def __init__(self, target=None, name=None, kwargs=None, daemon=None):
            self._target = target
            self._kwargs = kwargs or {}

        def start(self) -> None:
            if self._target is not None:
                self._target(**self._kwargs)

    role._start_ensure_thread = lambda *, reason: events.append(("start_supervisor", reason))
    role._run_ensure_cycle_safe = lambda *, reason, source: events.append((source, reason))
    role._log = lambda message, *, error=False: events.append(("log", message))

    try:
        wireguard_role.threading.Thread = _ImmediateThread
        role.request_immediate_ensure(reason="socket_connect")
    finally:
        wireguard_role.threading.Thread = original_thread

    assert ("start_supervisor", "socket_connect") in events
    assert ("immediate", "socket_connect") in events
    assert any(kind == "log" and "WireGuard immediate ensure requested" in message for kind, message in events)
