from __future__ import annotations

import threading

from Data.Agent.Roles.role_WireGuardTunnel import (
    LinuxWireGuardClient,
    Role,
    SessionConfig,
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
