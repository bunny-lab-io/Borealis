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
    return _client_with_session(harness, username="admin", role="Admin")


def _client_with_session(harness: EngineTestHarness, *, username: str, role: str = "Admin"):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = username
        sess["role"] = role
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
        self.created: list[dict[str, Any]] = []
        self.restart_callbacks: list[Any] = []
        self.confirm_callbacks: list[Any] = []

    def create(
        self,
        *,
        agent_id: str,
        host: str,
        port: int,
        operator_id: Any,
        session_id: str = "",
        participant_id: str = "",
        role: str = "",
        restart_tunnel: Any = None,
        confirm_transport: Any = None,
        on_open: Any = None,
        on_close: Any = None,
    ):
        self.created.append(
            {
                "agent_id": agent_id,
                "host": host,
                "port": port,
                "operator_id": operator_id,
                "session_id": session_id,
                "participant_id": participant_id,
                "role": role,
                "on_open": on_open,
                "on_close": on_close,
            }
        )
        self.restart_callbacks.append(restart_tunnel)
        self.confirm_callbacks.append(confirm_transport)
        return SimpleNamespace(token="session-token")


class _FakeProxy:
    def __init__(self) -> None:
        self.disconnect_session_calls: list[tuple[str, str]] = []
        self.disconnect_participant_calls: list[tuple[str, str, str]] = []

    def disconnect_session(self, session_id: str, *, reason: str = "") -> None:
        self.disconnect_session_calls.append((session_id, str(reason or "")))

    def disconnect_participant(self, session_id: str, participant_id: str, *, reason: str = "") -> None:
        self.disconnect_participant_calls.append((session_id, participant_id, str(reason or "")))


def test_vnc_establish_returns_same_origin_websocket(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeRegistry()
    engine_harness.context.public_base_url = "https://borealis.example.com"
    engine_harness.context.public_hostname = "borealis.example.com"
    engine_harness.context.public_wireguard_host = "borealis.example.com"

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "ensure_vnc_proxy", lambda *args, **kwargs: fake_registry)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["token"] == "session-token"
    assert payload["ws_path"] == "/remote-desktop/vnc"
    assert payload["ws_url"] == "wss://borealis.example.com/remote-desktop/vnc?token=session-token"
    assert fake_registry.created[0]["agent_id"] == "test-device-agent"
    assert fake_registry.created[0]["host"] == "10.255.0.2"
    assert fake_registry.created[0]["port"] == 5900
    assert fake_registry.created[0]["operator_id"] == "admin"
    assert fake_registry.created[0]["role"] == "controller"
    assert payload["participant_role"] == "controller"
    assert payload["view_only"] is False
    assert payload["session"]["controller_operator_id"] == "admin"
    assert fake_tunnel.connect_calls == [("test-device-agent", "admin", "borealis.example.com")]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap")]
    assert fake_tunnel.start_calls == [("test-device-agent", False, "vnc_bootstrap")]
    assert fake_tunnel.transport_recovers == []
    assert callable(fake_registry.restart_callbacks[0])
    assert callable(fake_registry.confirm_callbacks[0])

    fake_registry.confirm_callbacks[0]("vnc_backend_connect")

    assert fake_tunnel.transport_confirms == [
        ("test-device-agent", "vnc_backend_ready"),
        ("test-device-agent", "vnc_backend_connect"),
    ]


def test_vnc_handoff_updates_controller_and_forces_reconnect(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    admin_client = _client_with_session(engine_harness, username="admin")
    spectator_client = _client_with_session(engine_harness, username="alice")
    fake_tunnel = _FakeTunnelService()
    fake_proxy = _FakeProxy()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []

    engine_harness.context.vnc_proxy = fake_proxy
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "ensure_vnc_proxy", lambda *args, **kwargs: _FakeRegistry())
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    controller_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    controller_payload = controller_response.get_json()
    spectator_response = spectator_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    spectator_payload = spectator_response.get_json()

    assert controller_response.status_code == 200
    assert spectator_response.status_code == 200
    assert spectator_payload["participant_role"] == "spectator"

    handoff_response = admin_client.post(
        "/api/vnc/handoff",
        json={
            "session_id": controller_payload["session_id"],
            "target_operator_id": "alice",
        },
    )

    assert handoff_response.status_code == 200
    payload = handoff_response.get_json()
    assert payload["status"] == "ok"
    assert payload["reconnect_required"] is True
    assert payload["participant_role"] == "spectator"
    assert payload["session"]["controller_operator_id"] == "alice"
    assert payload["session"]["credential_revision"] == 2
    assert fake_proxy.disconnect_session_calls == [
        (controller_payload["session_id"], "handoff_reconnect_required")
    ]
    assert emitted_events[-1][0] == "test-device-agent"
    assert emitted_events[-1][1] == "vnc_start"
    assert emitted_events[-1][2]["reason"] == "controller_handoff"
    assert emitted_events[-1][2]["credential_revision"] == 2


def test_vnc_disconnect_vacates_controller_and_claim_control_restores_session(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    admin_client = _client_with_session(engine_harness, username="admin")
    spectator_client = _client_with_session(engine_harness, username="alice")
    fake_tunnel = _FakeTunnelService()
    fake_proxy = _FakeProxy()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []

    engine_harness.context.vnc_proxy = fake_proxy
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "ensure_vnc_proxy", lambda *args, **kwargs: _FakeRegistry())
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    controller_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    controller_payload = controller_response.get_json()
    spectator_response = spectator_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    spectator_payload = spectator_response.get_json()

    disconnect_response = admin_client.post(
        "/api/vnc/disconnect",
        json={"session_id": controller_payload["session_id"]},
    )

    assert disconnect_response.status_code == 200
    disconnect_payload = disconnect_response.get_json()
    assert disconnect_payload["status"] == "left"
    assert disconnect_payload["controller_vacant"] is True
    assert disconnect_payload["session"]["state"] == "controller_vacant"
    assert disconnect_payload["session"]["controller_operator_id"] == ""
    assert fake_proxy.disconnect_participant_calls == [
        (controller_payload["session_id"], controller_payload["participant_id"], "operator_disconnect")
    ]
    assert fake_proxy.disconnect_session_calls == [
        (controller_payload["session_id"], "controller_reconnect_required")
    ]
    assert emitted_events[-1][2]["reason"] == "controller_vacated"

    sessions_response = spectator_client.get(
        f"/api/vnc/sessions?session_id={controller_payload['session_id']}"
    )
    assert sessions_response.status_code == 200
    sessions_payload = sessions_response.get_json()
    assert sessions_payload["count"] == 1
    assert sessions_payload["sessions"][0]["controller_vacant"] is True
    assert sessions_payload["sessions"][0]["current_operator_role"] == "spectator"

    claim_response = spectator_client.post(
        "/api/vnc/handoff",
        json={"session_id": spectator_payload["session_id"]},
    )
    assert claim_response.status_code == 200
    claim_payload = claim_response.get_json()
    assert claim_payload["participant_role"] == "controller"
    assert claim_payload["session"]["controller_operator_id"] == "alice"
    assert claim_payload["session"]["state"] == "active"
    assert claim_payload["session"]["credential_revision"] == 3
