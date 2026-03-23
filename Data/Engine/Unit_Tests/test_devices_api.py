# ======================================================
# Data\Engine\Unit_Tests\test_devices_api.py
# Description: Exercises device management endpoints covering lists, views, site workflows, and approvals.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import logging
from Data.Engine.db import dbapi as sqlite3
from typing import Any
from types import SimpleNamespace

import pytest
from Data.Engine.auth import jwt_service as jwt_service_module
from Data.Engine.integrations import github as github_integration
from Data.Engine.services.API.devices import management as device_management
from Data.Engine.services.API.devices import tunnel as tunnel_api
from Data.Engine.services.VPN.vpn_tunnel_service import VpnTunnelService

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _device_headers() -> dict:
    jwt_service = jwt_service_module.load_service()
    token = jwt_service.issue_access_token(
        "GUID-TEST-0001",
        "ff:ff:ff",
        1,
        expires_in=900,
    )
    return {"Authorization": f"Bearer {token}"}


def _patch_repo_call(monkeypatch: pytest.MonkeyPatch, calls: dict) -> None:
    class DummyResponse:
        def __init__(self, status_code: int, payload: Any):
            self.status_code = status_code
            self._payload = payload

        def json(self) -> Any:
            return self._payload

    request_exception = getattr(github_integration.requests, "RequestException", RuntimeError)

    def fake_get(url: str, headers: Any, timeout: int) -> DummyResponse:
        calls["count"] += 1
        if calls["count"] == 1:
            return DummyResponse(200, {"commit": {"sha": "abc123"}})
        raise request_exception("network error")

    monkeypatch.setattr(github_integration.requests, "get", fake_get)


def _set_test_device_guid(engine_harness: EngineTestHarness, guid: str) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("UPDATE devices SET guid = ? WHERE hostname = ?", (guid, "test-device"))
        conn.commit()
    finally:
        conn.close()


class _DummySocketIO:
    def __init__(self) -> None:
        self.tasks = []
        self.emits = []

    def start_background_task(self, target, *args, **kwargs):
        self.tasks.append((target, args, kwargs))
        return None

    def emit(self, event: str, payload: Any, namespace: str = "/") -> None:
        self.emits.append((event, payload, namespace))


class _FakeWireGuardManager:
    def __init__(self) -> None:
        self.server_public_key = "server-public-key"
        self.logger = logging.getLogger("borealis.test.wireguard")
        self.health = {
            "healthy": True,
            "reason": "listener_running",
            "service_state": "RUNNING",
        }
        self.keep_unhealthy = False
        self.fail_start = False
        self.start_calls = 0
        self.stop_calls = 0
        self.apply_calls = 0
        self.removed_rules = []

    def require_orchestration_token(self, token: Any) -> Any:
        return token

    def build_peer_profile(self, agent_id: str, virtual_ip: str, allowed_ports: Any = None) -> dict:
        return {
            "agent_id": agent_id,
            "virtual_ip": virtual_ip,
            "allowed_ips": [virtual_ip],
            "allowed_ports": tuple(allowed_ports or ()),
        }

    def apply_firewall_rules(self, peer: Any) -> list[str]:
        self.apply_calls += 1
        return [f"rule-{peer.get('agent_id', 'agent')}"]

    def remove_firewall_rules(self, rule_names: Any) -> None:
        self.removed_rules.append(list(rule_names))

    def start_listener(self, peers: Any) -> None:
        self.start_calls += 1
        if self.fail_start:
            raise RuntimeError("listener start failed")
        if not self.keep_unhealthy:
            self.health = {
                "healthy": True,
                "reason": "listener_running",
                "service_state": "RUNNING",
            }

    def stop_listener(self) -> None:
        self.stop_calls += 1

    def check_listener_health(self) -> dict:
        return dict(self.health)


def _build_vpn_service() -> tuple[VpnTunnelService, _FakeWireGuardManager, _DummySocketIO, list[tuple[str, str, str]]]:
    socketio = _DummySocketIO()
    wg = _FakeWireGuardManager()
    service_events: list[tuple[str, str, str]] = []
    context = SimpleNamespace(
        logger=logging.getLogger("borealis.test.vpn"),
        wireguard_port=30000,
        wireguard_engine_virtual_ip="10.255.0.1/24",
        wireguard_peer_network="10.255.0.0/24",
        wireguard_port_allowlist=(47002, 5900),
    )
    service = VpnTunnelService(
        context=context,
        wireguard_manager=wg,
        db_conn_factory=None,
        socketio=socketio,
        service_log=lambda name, message, level="INFO": service_events.append((name, message, level)),
        signer=None,
    )
    return service, wg, socketio, service_events


class _FakeTunnelApiService:
    def __init__(self, *, status_payload: Any, active_payloads: list[dict[str, Any]]) -> None:
        self._status_payload = status_payload
        self._active_payloads = active_payloads
        self.bumped_agents: list[str] = []
        self.connect_calls: list[tuple[str, Any, Any]] = []

    def status(self, agent_id: str) -> Any:
        if callable(self._status_payload):
            return self._status_payload(agent_id)
        return self._status_payload

    def connect(self, *, agent_id: str, operator_id: Any, endpoint_host: Any) -> dict[str, Any]:
        self.connect_calls.append((agent_id, operator_id, endpoint_host))
        return {
            "tunnel_id": "tun-1",
            "agent_id": agent_id,
            "virtual_ip": "10.255.0.2/32",
            "engine_virtual_ip": "10.255.0.1/32",
            "endpoint": "engine.local:30000",
        }

    def list_sessions(self) -> list[dict[str, Any]]:
        return list(self._active_payloads)

    def bump_activity(self, agent_id: str) -> None:
        self.bumped_agents.append(agent_id)


def test_list_devices(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.get("/api/devices")
    assert response.status_code == 200
    payload = response.get_json()
    assert isinstance(payload, dict)
    devices = payload.get("devices")
    assert isinstance(devices, list) and devices
    device = devices[0]
    assert device["hostname"] == "test-device"
    assert "summary" in device and isinstance(device["summary"], dict)


def test_device_hostname_search_requires_three_characters(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.get("/api/devices/search?hostname=te")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload == {"devices": [], "query": "te", "count": 0}


def test_device_hostname_search_returns_matches(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.get("/api/devices/search?hostname=tes")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["count"] == 1
    assert payload["devices"][0]["hostname"] == "test-device"
    assert payload["devices"][0]["site_name"] == "Main Lab"


def test_list_agents(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.get("/api/agents")
    assert response.status_code == 200
    payload = response.get_json()
    assert isinstance(payload, dict)
    assert payload, "expected at least one agent in the response"
    first_agent = next(iter(payload.values()))
    assert first_agent["hostname"] == "test-device"
    assert first_agent["agent_id"] == "test-device-agent"


def test_device_details(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.get("/api/device/details/test-device")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["summary"]["hostname"] == "test-device"


def test_device_description_requires_login(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    response = client.post(
        "/api/device/description/test-device",
        json={"description": "Updated"},
    )
    assert response.status_code == 401


def test_device_description_update(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.post(
        "/api/device/description/test-device",
        json={"description": "Updated"},
    )
    assert response.status_code == 200
    detail = client.get("/api/device/details/test-device").get_json()
    assert detail["description"] == "Updated"


def test_agent_details_syncs_normalized_software_inventory(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/details",
        json={
            "hostname": "test-device",
            "agent_hash": "hash-123",
            "details": {
                "summary": {
                    "hostname": "test-device",
                    "last_seen": 1_700_000_800,
                    "operating_system": "Windows 11 Pro",
                },
                "software": [
                    {
                        "name": "Google Chrome",
                        "version": "124.0.6367.92",
                        "source": "local_installed",
                    },
                    {
                        "name": "Contoso.App",
                        "version": "1.2.0",
                        "source": "windows_store",
                    },
                ],
            },
        },
        headers=_device_headers(),
    )
    assert response.status_code == 200

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT name, version, source
              FROM device_software_inventory
             WHERE device_guid = ?
          ORDER BY name ASC
            """,
            ("GUID-TEST-0001",),
        )
        rows = cur.fetchall()
    finally:
        conn.close()

    assert rows == [
        ("Contoso.App", "1.2.0", "windows_store"),
        ("Google Chrome", "124.0.6367.92", "local_installed"),
    ]

    admin_client = _client_with_admin_session(engine_harness)
    detail_response = admin_client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {"name": "Contoso.App", "version": "1.2.0", "source": "windows_store", "metadata": {}},
        {"name": "Google Chrome", "version": "124.0.6367.92", "source": "local_installed", "metadata": {}},
    ]


def test_device_list_views_lifecycle(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    create_resp = client.post(
        "/api/device_list_views",
        json={"name": "Custom", "columns": ["hostname"], "filters": {"site": "Main"}},
    )
    assert create_resp.status_code == 201
    view_id = create_resp.get_json()["id"]

    fetch_resp = client.get("/api/device_list_views")
    assert any(view["id"] == view_id for view in fetch_resp.get_json()["views"])

    update_resp = client.put(
        f"/api/device_list_views/{view_id}",
        json={"name": "Custom-2"},
    )
    assert update_resp.status_code == 200
    assert update_resp.get_json()["name"] == "Custom-2"

    delete_resp = client.delete(f"/api/device_list_views/{view_id}")
    assert delete_resp.status_code == 200


def test_repo_current_hash_uses_cache(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    calls = {"count": 0}

    _patch_repo_call(monkeypatch, calls)

    client = _client_with_admin_session(engine_harness)
    first = client.get("/api/repo/current_hash?repo=test/test&branch=main")
    assert first.status_code == 200
    assert first.get_json()["sha"] == "abc123"
    second = client.get("/api/repo/current_hash?repo=test/test&branch=main")
    assert second.status_code == 200
    second_payload = second.get_json()
    assert second_payload["sha"] == "abc123"
    assert second_payload["cached"] is True or second_payload["source"].startswith("cache")
    assert calls["count"] == 1


def test_repo_current_hash_allows_device_token(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    calls = {"count": 0}
    _patch_repo_call(monkeypatch, calls)

    client = engine_harness.app.test_client()
    response = client.get(
        "/api/repo/current_hash?repo=test/test&branch=main",
        headers=_device_headers(),
    )
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["sha"] == "abc123"
    assert calls["count"] == 1


def test_agent_hash_list_permissions(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    forbidden = client.get("/api/agent/hash_list", environ_base={"REMOTE_ADDR": "192.0.2.10"})
    assert forbidden.status_code == 403
    allowed = client.get("/api/agent/hash_list", environ_base={"REMOTE_ADDR": "127.0.0.1"})
    assert allowed.status_code == 200
    agents = allowed.get_json()["agents"]
    assert agents and agents[0]["hostname"] == "test-device"


def test_sites_lifecycle(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    create_resp = client.post(
        "/api/sites",
        json={"name": "Edge", "description": "Edge location"},
    )
    assert create_resp.status_code == 201
    site_id = create_resp.get_json()["id"]

    list_resp = client.get("/api/sites")
    sites = list_resp.get_json()["sites"]
    assert any(site["id"] == site_id for site in sites)

    assign_resp = client.post(
        "/api/sites/assign",
        json={"site_id": site_id, "hostnames": ["test-device"]},
    )
    assert assign_resp.status_code == 200

    mapping_resp = client.get("/api/sites/device_map")
    mapping = mapping_resp.get_json()["mapping"]
    assert mapping["test-device"]["site_id"] == site_id

    rename_resp = client.post(
        "/api/sites/rename",
        json={"id": site_id, "new_name": "Edge-Renamed"},
    )
    assert rename_resp.status_code == 200
    assert rename_resp.get_json()["name"] == "Edge-Renamed"

    delete_resp = client.post("/api/sites/delete", json={"ids": [site_id]})
    assert delete_resp.status_code == 200


def test_admin_device_approvals(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    list_resp = client.get("/api/admin/device-approvals")
    approvals = list_resp.get_json()["approvals"]
    assert approvals and approvals[0]["status"] == "pending"

    approve_resp = client.post(
        "/api/admin/device-approvals/approval-1/approve",
        json={"conflict_resolution": "overwrite"},
    )
    assert approve_resp.status_code == 200


def test_vpn_service_reuses_existing_session_with_listener_recovery() -> None:
    service, wg, _socketio, service_events = _build_vpn_service()

    initial = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    assert initial["tunnel_id"]
    assert wg.start_calls == 1

    wg.health = {
        "healthy": False,
        "reason": "service_unhealthy",
        "service_state": "STOPPED",
    }

    reused = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    assert reused["tunnel_id"] == initial["tunnel_id"]
    assert wg.start_calls == 2
    assert any("vpn_listener_recovery_attempt" in message for _name, message, _level in service_events)
    assert any("vpn_listener_recovery_success" in message for _name, message, _level in service_events)


def test_vpn_service_throttles_listener_recovery_retries() -> None:
    service, wg, _socketio, service_events = _build_vpn_service()
    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    wg.keep_unhealthy = True
    wg.health = {
        "healthy": False,
        "reason": "service_unhealthy",
        "service_state": "STOPPED",
    }

    first = service._recover_listener(trigger="test", reason="manual")
    first_start_calls = wg.start_calls
    second = service._recover_listener(trigger="test", reason="manual")

    assert first["recovery_in_progress"] is True
    assert second["recovery_in_progress"] is True
    assert wg.start_calls == first_start_calls
    assert any("vpn_listener_recovery_throttled" in message for _name, message, _level in service_events)


def test_vpn_service_watchdog_noops_when_listener_healthy() -> None:
    service, wg, _socketio, _service_events = _build_vpn_service()
    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    start_calls = wg.start_calls
    service._watchdog_tick()

    status = service.status("agent-1")
    assert wg.start_calls == start_calls
    assert status is not None
    assert status["listener_healthy"] is True
    assert status["recovery_in_progress"] is False


def test_tunnel_status_endpoint_exposes_listener_health(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_service = _FakeTunnelApiService(
        status_payload={
            "tunnel_id": "tun-1",
            "agent_id": "test-device-agent",
            "virtual_ip": "10.255.0.2/32",
            "listener_healthy": False,
            "recovery_in_progress": True,
            "last_recovery_attempt_at": 1_700_123_456,
            "last_recovery_attempt_at_iso": "2025-11-14T00:00:00+00:00",
        },
        active_payloads=[],
    )
    monkeypatch.setattr(tunnel_api, "_get_tunnel_service", lambda _adapters: fake_service)

    response = client.get("/api/tunnel/status?agent_id=test-device-agent&bump=1")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "up"
    assert payload["listener_healthy"] is False
    assert payload["recovery_in_progress"] is True
    assert payload["last_recovery_attempt_at"] == 1_700_123_456
    assert payload["last_recovery_attempt_at_iso"] == "2025-11-14T00:00:00+00:00"
    assert fake_service.bumped_agents == ["test-device-agent"]


def test_tunnel_status_endpoint_resolves_guid_to_agent_id(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    valid_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    _set_test_device_guid(engine_harness, valid_guid)
    client = _client_with_admin_session(engine_harness)
    fake_service = _FakeTunnelApiService(
        status_payload=lambda agent_id: {
            "tunnel_id": "tun-1",
            "agent_id": agent_id,
            "virtual_ip": "10.255.0.2/32",
            "listener_healthy": True,
            "recovery_in_progress": False,
            "last_recovery_attempt_at": None,
            "last_recovery_attempt_at_iso": "",
        }
        if agent_id == "test-device-agent"
        else None,
        active_payloads=[],
    )
    monkeypatch.setattr(tunnel_api, "_get_tunnel_service", lambda _adapters: fake_service)

    response = client.get(f"/api/tunnel/status?agent_id={valid_guid}")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "up"
    assert payload["agent_id"] == "test-device-agent"


def test_tunnel_connect_endpoint_resolves_guid_to_agent_id(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    valid_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    _set_test_device_guid(engine_harness, valid_guid)
    client = _client_with_admin_session(engine_harness)
    fake_service = _FakeTunnelApiService(status_payload=None, active_payloads=[])
    monkeypatch.setattr(tunnel_api, "_get_tunnel_service", lambda _adapters: fake_service)

    response = client.post("/api/tunnel/connect", json={"agent_id": valid_guid})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["agent_id"] == "test-device-agent"
    assert fake_service.connect_calls == [("test-device-agent", "admin", "localhost")]


def test_tunnel_status_endpoint_returns_down_health_defaults(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_service = _FakeTunnelApiService(status_payload=None, active_payloads=[])
    monkeypatch.setattr(tunnel_api, "_get_tunnel_service", lambda _adapters: fake_service)

    response = client.get("/api/tunnel/status?agent_id=test-device-agent")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "down"
    assert payload["listener_healthy"] is False
    assert payload["recovery_in_progress"] is False
    assert payload["last_recovery_attempt_at"] is None
    assert payload["last_recovery_attempt_at_iso"] == ""


def test_tunnel_active_endpoint_exposes_listener_health(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_service = _FakeTunnelApiService(
        status_payload=None,
        active_payloads=[
            {
                "tunnel_id": "tun-1",
                "agent_id": "test-device-agent",
                "virtual_ip": "10.255.0.2/32",
                "status": "up",
                "listener_healthy": True,
                "recovery_in_progress": False,
                "last_recovery_attempt_at": 1_700_123_000,
                "last_recovery_attempt_at_iso": "2025-11-14T00:00:00+00:00",
            }
        ],
    )
    monkeypatch.setattr(tunnel_api, "_get_tunnel_service", lambda _adapters: fake_service)

    response = client.get("/api/tunnel/active")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["count"] == 1
    tunnel = payload["tunnels"][0]
    assert tunnel["status"] == "up"
    assert tunnel["listener_healthy"] is True
    assert tunnel["recovery_in_progress"] is False
    assert tunnel["last_recovery_attempt_at"] == 1_700_123_000
