# ======================================================
# Data\Engine\Unit_Tests\test_vnc_api.py
# Description: Validates VNC session bootstrap behavior for same-origin routed access.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

from Data.Engine.services.API.devices import vnc as vnc_api

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


class _FakeTunnelService:
    def __init__(self) -> None:
        self.connect_calls: list[tuple[str, Any, Any]] = []
        self.start_calls: list[tuple[str, bool, str]] = []
        self.transport_marks: list[tuple[str, str]] = []
        self.transport_confirms: list[tuple[str, str]] = []
        self.transport_recovers: list[tuple[str, str, str]] = []

    def session_payload(self, agent_id: str, include_token: bool = False) -> Any:
        _ = agent_id
        _ = include_token
        return None

    def connect(self, *, agent_id: str, operator_id: Any, endpoint_host: Any) -> dict[str, Any]:
        self.connect_calls.append((agent_id, operator_id, endpoint_host))
        return {
            "tunnel_id": "tun-vnc-1",
            "agent_id": agent_id,
            "virtual_ip": "10.255.0.2/32",
            "engine_virtual_ip": "10.255.0.1/32",
        }

    def request_agent_start(
        self,
        agent_id: str,
        *,
        force_restart: bool = False,
        reason: str | None = None,
    ) -> dict[str, Any]:
        self.start_calls.append((agent_id, bool(force_restart), str(reason or "")))
        return {"status": "ok"}

    def mark_transport_required(self, agent_id: str, *, reason: str | None = None) -> bool:
        self.transport_marks.append((agent_id, str(reason or "")))
        return True

    def recover_transport(
        self,
        agent_id: str,
        *,
        trigger: str,
        reason: str | None = None,
    ) -> dict[str, Any]:
        self.transport_recovers.append((agent_id, str(trigger or ""), str(reason or "")))
        return {"status": "ok"}

    def confirm_transport_success(self, agent_id: str, *, reason: str | None = None) -> bool:
        self.transport_confirms.append((agent_id, str(reason or "")))
        return True


class _FakeRegistry:
    def __init__(self) -> None:
        self.created: list[tuple[str, str, int, Any]] = []
        self.restart_callbacks: list[Any] = []
        self.confirm_callbacks: list[Any] = []

    def create(
        self,
        *,
        agent_id: str,
        host: str,
        port: int,
        operator_id: Any,
        restart_tunnel: Any = None,
        confirm_transport: Any = None,
    ):
        self.created.append((agent_id, host, port, operator_id))
        self.restart_callbacks.append(restart_tunnel)
        self.confirm_callbacks.append(confirm_transport)
        return SimpleNamespace(token="session-token")


def test_vnc_establish_returns_same_origin_websocket(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeRegistry()
    engine_harness.context.public_base_url = "https://borealis.example.com"
    engine_harness.context.public_hostname = "borealis.example.com"
    engine_harness.context.public_wireguard_host = "borealis.example.com"

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "ensure_vnc_proxy", lambda *args, **kwargs: fake_registry)

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["token"] == "session-token"
    assert payload["ws_path"] == "/remote-desktop/vnc"
    assert payload["ws_url"] == "wss://borealis.example.com/remote-desktop/vnc?token=session-token"
    assert fake_registry.created == [("test-device-agent", "10.255.0.2", 5900, "admin")]
    assert fake_tunnel.connect_calls == [("test-device-agent", "admin", "borealis.example.com")]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap")]
    assert fake_tunnel.start_calls == [("test-device-agent", False, "vnc_bootstrap")]
    assert fake_tunnel.transport_recovers == []
    assert callable(fake_registry.restart_callbacks[0])
    assert callable(fake_registry.confirm_callbacks[0])

    fake_registry.confirm_callbacks[0]("vnc_backend_connect")

    assert fake_tunnel.transport_confirms == [("test-device-agent", "vnc_backend_connect")]
