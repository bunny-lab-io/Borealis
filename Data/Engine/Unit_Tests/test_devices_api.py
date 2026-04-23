# ======================================================
# Data\Engine\Unit_Tests\test_devices_api.py
# Description: Exercises device management endpoints covering lists, views, site workflows, and approvals.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations
import base64
import hashlib
import json
import logging
import threading
import time
from Data.Engine.db import dbapi as sqlite3
from typing import Any
from types import SimpleNamespace

import pytest
from Data.Engine import database as engine_database
from Data.Engine.auth import jwt_service as jwt_service_module
from Data.Engine.services.API.devices import management as device_management
from Data.Engine.services.API.devices import routes as device_routes
from Data.Engine.services.API.devices import software_icons as software_icons_module
from Data.Engine.services.API.devices import software_uninstall as software_uninstall_module
from Data.Engine.services.API.devices.service_inventory import serialize_device_services
from Data.Engine.services.API.devices import tunnel as tunnel_api
from Data.Engine.services.VPN.vpn_tunnel_service import (
    PEER_ACTIVITY_WINDOW_SECONDS,
    PEER_CONFIRMED_ACTIVITY_WINDOW_SECONDS,
    WIREGUARD_KEEPALIVE_SECONDS,
    VpnTunnelService,
)

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _device_headers() -> dict:
    return _device_headers_for_guid("GUID-TEST-0001")


def _device_headers_for_guid(guid: str) -> dict:
    jwt_service = jwt_service_module.load_service()
    token = jwt_service.issue_access_token(
        guid,
        "ff:ff:ff",
        1,
        expires_in=900,
    )
    return {"Authorization": f"Bearer {token}"}


def _patch_repo_call(monkeypatch: pytest.MonkeyPatch, calls: dict) -> None:
    from Data.Engine.integrations import github as github_integration

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


def _set_test_device_agent_id(engine_harness: EngineTestHarness, agent_id: str) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("UPDATE devices SET agent_id = ? WHERE hostname = ?", (agent_id, "test-device"))
        conn.commit()
    finally:
        conn.close()


def _set_test_device_services(engine_harness: EngineTestHarness, payload: Any) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "UPDATE devices SET services = ? WHERE hostname = ?",
            (serialize_device_services(payload), "test-device"),
        )
        conn.commit()
    finally:
        conn.close()


def _set_test_device_software(engine_harness: EngineTestHarness, payload: Any) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "UPDATE devices SET software = ? WHERE hostname = ?",
            (json.dumps(payload or []), "test-device"),
        )
        conn.commit()
    finally:
        conn.close()


def test_tunnel_service_creation_is_singleton_under_concurrency(monkeypatch: pytest.MonkeyPatch) -> None:
    manager_calls = {"count": 0}
    service_calls = {"count": 0}

    class _DummyManager:
        pass

    class _DummyService:
        pass

    def _build_manager(_config):
        manager_calls["count"] += 1
        time.sleep(0.02)
        return _DummyManager()

    def _build_service(**_kwargs):
        service_calls["count"] += 1
        time.sleep(0.02)
        return _DummyService()

    monkeypatch.setattr(tunnel_api, "WireGuardServerManager", _build_manager)
    monkeypatch.setattr(tunnel_api, "VpnTunnelService", _build_service)

    context = SimpleNamespace(
        logger=logging.getLogger("borealis.test.tunnel.singleton"),
        vpn_tunnel_service=None,
        wireguard_server_manager=None,
        wireguard_port=30000,
        wireguard_engine_virtual_ip="10.255.0.1/32",
        wireguard_peer_network="10.255.0.0/24",
        wireguard_server_private_key_path="/tmp/wg.key",
        wireguard_server_public_key_path="/tmp/wg.pub",
        wireguard_port_allowlist=(47002, 5900, 22),
        vpn_tunnel_log_path="/tmp/vpn_tunnel.log",
        socketio=None,
    )
    adapters = SimpleNamespace(
        context=context,
        db_conn_factory=lambda: None,
        service_log=lambda *_args, **_kwargs: None,
        script_signer=None,
    )

    results: list[Any] = []
    failures: list[str] = []

    def _target() -> None:
        try:
            results.append(tunnel_api._get_tunnel_service(adapters))
        except Exception as exc:
            failures.append(repr(exc))

    threads = [threading.Thread(target=_target) for _ in range(8)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert failures == []
    assert len(results) == 8
    assert len({id(item) for item in results}) == 1
    assert manager_calls["count"] == 1
    assert service_calls["count"] == 1
    assert adapters.context.vpn_tunnel_service is results[0]


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
        self.ensure_calls = 0
        self.reconcile_calls = 0
        self.upsert_calls = 0
        self.remove_calls = 0
        self.cleanup_calls = 0
        self.cleanup_removed: list[str] = []
        self.removed_rules = []
        self.current_peers: dict[str, dict[str, Any]] = {}
        self.peer_health_overrides: dict[str, dict[str, Any]] = {}

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

    def _mark_healthy(self) -> None:
        if not self.keep_unhealthy:
            self.health = {
                "healthy": True,
                "reason": "listener_running",
                "service_state": "RUNNING",
                "peer_count": len(self.current_peers),
            }

    def ensure_listener(self) -> None:
        self.ensure_calls += 1
        self._mark_healthy()

    def start_listener(self, peers: Any) -> None:
        self.start_calls += 1
        if self.fail_start:
            raise RuntimeError("listener start failed")
        self.current_peers = {
            str(peer.get("agent_id") or ""): dict(peer)
            for peer in (peers or [])
            if str(peer.get("agent_id") or "").strip()
        }
        self._mark_healthy()

    def reconcile_peers(self, peers: Any) -> None:
        self.reconcile_calls += 1
        self.start_listener(peers)

    def upsert_peer(self, peer: Any) -> None:
        self.upsert_calls += 1
        self.start_calls += 1
        if self.fail_start:
            raise RuntimeError("listener start failed")
        agent_id = str(peer.get("agent_id") or "").strip()
        if agent_id:
            self.current_peers[agent_id] = dict(peer)
        self._mark_healthy()

    def remove_peer(self, agent_id: str, *, public_key: str = "") -> None:
        self.remove_calls += 1
        self.stop_calls += 1
        self.current_peers.pop(str(agent_id or "").strip(), None)
        self._mark_healthy()

    def stop_listener(self) -> None:
        self.stop_calls += 1
        self.current_peers = {}

    def cleanup_stale_runtime(self) -> list[str]:
        self.cleanup_calls += 1
        return list(self.cleanup_removed)

    def managed_peers_snapshot(self) -> dict[str, dict[str, Any]]:
        return {
            str(agent_id): dict(peer)
            for agent_id, peer in self.current_peers.items()
            if str(agent_id).strip()
        }

    def check_listener_health(self) -> dict:
        payload = dict(self.health)
        payload.setdefault("peer_count", len(self.current_peers))
        return payload

    def check_peer_health(self, public_key: str) -> dict:
        normalized_key = str(public_key or "").strip()
        override = self.peer_health_overrides.get(normalized_key)
        if override is not None:
            return dict(override)
        peer_present = any(
            str(peer.get("public_key") or "").strip() == normalized_key
            for peer in self.current_peers.values()
        )
        if not self.health.get("healthy"):
            return {
                "healthy": False,
                "reason": str(self.health.get("reason") or "service_unhealthy"),
                "service_state": self.health.get("service_state"),
                "peer_present": peer_present,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }
        if not peer_present:
            return {
                "healthy": False,
                "reason": "peer_missing",
                "service_state": self.health.get("service_state"),
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }
        handshake_at = int(time.time())
        return {
            "healthy": True,
            "reason": "peer_ready",
            "service_state": self.health.get("service_state"),
            "peer_present": True,
            "last_handshake_at": handshake_at,
            "last_handshake_at_iso": "",
            "handshake_age_seconds": 0,
        }


class _ConcurrentFakeWireGuardManager(_FakeWireGuardManager):
    def __init__(self) -> None:
        super().__init__()
        self._active_starts = 0
        self._guard = threading.Lock()
        self.max_concurrent_starts = 0

    def _run_mutation(self, callback) -> None:
        with self._guard:
            self.start_calls += 1
            self._active_starts += 1
            self.max_concurrent_starts = max(self.max_concurrent_starts, self._active_starts)
        try:
            time.sleep(0.05)
            if self.fail_start:
                raise RuntimeError("listener start failed")
            callback()
            self._mark_healthy()
        finally:
            with self._guard:
                self._active_starts -= 1

    def start_listener(self, peers: Any) -> None:
        def _apply() -> None:
            self.current_peers = {
                str(peer.get("agent_id") or ""): dict(peer)
                for peer in (peers or [])
                if str(peer.get("agent_id") or "").strip()
            }

        self._run_mutation(_apply)

    def reconcile_peers(self, peers: Any) -> None:
        self.reconcile_calls += 1
        self.start_listener(peers)

    def upsert_peer(self, peer: Any) -> None:
        self.upsert_calls += 1
        def _apply() -> None:
            agent_id = str(peer.get("agent_id") or "").strip()
            if agent_id:
                self.current_peers[agent_id] = dict(peer)

        self._run_mutation(_apply)


class _FakeAgentSocketRegistry:
    def __init__(
        self,
        *,
        registered_agent_ids: set[str] | None = None,
        host_mode_routes: dict[tuple[str, str], str] | None = None,
    ) -> None:
        self._registered_agent_ids = set(registered_agent_ids or set())
        self._host_mode_routes = dict(host_mode_routes or {})

    def is_registered(self, agent_id: str) -> bool:
        return agent_id in self._registered_agent_ids

    def get_agent_id_for_host_mode(self, hostname: str, service_mode: str) -> str:
        return self._host_mode_routes.get((str(hostname or "").strip().lower(), str(service_mode or "").strip().lower()), "")


def _build_vpn_service(
    *,
    db_conn_factory: Any = None,
    wireguard_manager: Any = None,
) -> tuple[VpnTunnelService, Any, _DummySocketIO, list[tuple[str, str, str]]]:
    socketio = _DummySocketIO()
    wg = wireguard_manager or _FakeWireGuardManager()
    service_events: list[tuple[str, str, str]] = []
    context = SimpleNamespace(
        logger=logging.getLogger("borealis.test.vpn"),
        wireguard_port=30000,
        wireguard_engine_virtual_ip="10.255.0.1/24",
        wireguard_peer_network="10.255.0.0/24",
        wireguard_port_allowlist=(47002, 5900, 22),
    )
    service = VpnTunnelService(
        context=context,
        wireguard_manager=wg,
        db_conn_factory=db_conn_factory,
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
        self.connect_mark_activity_calls: list[bool] = []

    def status(self, agent_id: str) -> Any:
        if callable(self._status_payload):
            return self._status_payload(agent_id)
        return self._status_payload

    def connect(
        self,
        *,
        agent_id: str,
        operator_id: Any,
        endpoint_host: Any,
        mark_activity: bool = True,
    ) -> dict[str, Any]:
        self.connect_calls.append((agent_id, operator_id, endpoint_host))
        self.connect_mark_activity_calls.append(bool(mark_activity))
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


def test_vpn_service_startup_cleans_up_stale_runtime() -> None:
    service, wg, _socketio, service_events = _build_vpn_service()
    assert service is not None
    assert wg.cleanup_calls == 1
    assert ("VPN_Tunnel/tunnel", "vpn_listener_cleanup_complete removed=0", "INFO") in service_events


def test_vpn_service_startup_logs_removed_stale_interfaces() -> None:
    wg = _FakeWireGuardManager()
    wg.cleanup_removed = ["borealis"]
    service, _wg, _socketio, service_events = _build_vpn_service(wireguard_manager=wg)
    assert service is not None
    assert wg.cleanup_calls == 1
    assert ("VPN_Tunnel/tunnel", "vpn_listener_cleanup_complete removed=1 interfaces=borealis", "INFO") in service_events


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


def test_list_agents_includes_helper_contexts_for_upgraded_system_socket(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        snapshot=lambda: {
            "test-device-agent": {
                "agent_id": "test-device-agent",
                "hostname": "test-device",
                "service_mode": "system",
                "helper_contexts": ["currentuser"],
            }
        }
    )

    response = client.get("/api/agents")
    assert response.status_code == 200
    payload = response.get_json()
    assert isinstance(payload, dict)
    first_agent = next(iter(payload.values()))
    assert first_agent["hostname"] == "test-device"
    assert first_agent["helper_contexts"] == ["currentuser"]


def test_device_details(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    response = client.get("/api/device/details/test-device")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["summary"]["hostname"] == "test-device"


def test_device_services_action_and_refresh(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    now_ts = int(time.time())
    _set_test_device_services(
        engine_harness,
        {
            "reported_at": now_ts - 120,
            "services": [
                {
                    "name": "sshd.service",
                    "description": "OpenSSH server daemon",
                    "status": "running",
                    "captured_at": now_ts - 120,
                }
            ],
        },
    )
    engine_harness.context.emit_host_service_event = lambda hostname, mode, event, payload: True

    response = client.get("/api/device/services/test-device")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["count"] == 1
    assert payload["services"][0]["name"] == "sshd.service"

    action_response = client.post(
        "/api/device/services/test-device/action",
        json={"service_name": "sshd.service", "action": "restart"},
    )
    assert action_response.status_code == 200
    action_payload = action_response.get_json()
    service_entry = action_payload["services"][0]
    assert service_entry["pending_action"] == "restart"
    assert service_entry["desired_status"] == "running"

    device_client = engine_harness.app.test_client()
    refresh_response = device_client.post(
        "/api/agent/details",
        headers=_device_headers(),
        json={
            "hostname": "test-device",
            "service_mode": "system",
            "details": {
                "summary": {
                    "hostname": "test-device",
                },
                "services": [
                    {
                        "name": "sshd.service",
                        "description": "OpenSSH server daemon",
                        "status": "running",
                        "captured_at": int(time.time()) + 2,
                    }
                ],
            },
        },
    )
    assert refresh_response.status_code == 200

    refreshed_payload = client.get("/api/device/services/test-device").get_json()
    refreshed_entry = refreshed_payload["services"][0]
    assert refreshed_entry["status_code"] == "running"
    assert refreshed_entry.get("pending_action", "") == ""
    assert refreshed_entry.get("desired_status", "") == ""


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
                        "metadata": {
                            "estimated_size_kb": 987654,
                            "display_icon": r"C:\Program Files\Google\Chrome\Application\chrome.exe,0",
                            "quiet_uninstall_string": '"C:\\Program Files\\Google\\Chrome\\Application\\124.0.6367.92\\Installer\\setup.exe" --uninstall --multi-install --chrome --system-level --force-uninstall',
                            "uninstall_string": "MsiExec.exe /I{11111111-2222-3333-4444-555555555555}",
                            "product_code": "{11111111-2222-3333-4444-555555555555}",
                        },
                    },
                    {
                        "name": "Contoso.App",
                        "version": "1.2.0",
                        "source": "windows_store",
                        "metadata": {
                            "package_family_name": "Contoso.App_1234567890abc",
                            "non_removable": False,
                        },
                    },
                    {
                        "name": "1527c705-839a-4832-9118-54d4Bd6a0c89",
                        "version": "10.0.19640.1000",
                        "source": "windows_store",
                        "metadata": {
                            "package_family_name": "1527c705-839a-4832-9118-54d4Bd6a0c89_cw5n1h2txyewy",
                            "non_removable": True,
                        },
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
        {
            "name": "Contoso.App",
            "version": "1.2.0",
            "source": "windows_store",
            "metadata": {
                "package_family_name": "Contoso.App_1234567890abc",
                "non_removable": False,
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Windows Store package uninstall.",
                "strategy": "windows_store",
                "rule_id": "metadata_windows_store",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "Contoso.App_1234567890abc",
            },
        },
        {
            "name": "Google Chrome",
            "version": "124.0.6367.92",
            "source": "local_installed",
            "metadata": {
                "estimated_size_kb": 987654,
                "display_icon": r"C:\Program Files\Google\Chrome\Application\chrome.exe,0",
                "quiet_uninstall_string": '"C:\\Program Files\\Google\\Chrome\\Application\\124.0.6367.92\\Installer\\setup.exe" --uninstall --multi-install --chrome --system-level --force-uninstall',
                "uninstall_string": "MsiExec.exe /I{11111111-2222-3333-4444-555555555555}",
                "product_code": "{11111111-2222-3333-4444-555555555555}",
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Uses the registry quiet uninstall string.",
                "strategy": "direct_command",
                "rule_id": "metadata_quiet_uninstall_string",
                "quiet_uninstall_string": '"C:\\Program Files\\Google\\Chrome\\Application\\124.0.6367.92\\Installer\\setup.exe" --uninstall --multi-install --chrome --system-level --force-uninstall',
                "uninstall_string": "MsiExec.exe /I{11111111-2222-3333-4444-555555555555}",
                "product_code": "{11111111-2222-3333-4444-555555555555}",
                "package_family_name": "",
            },
        },
    ]


def test_agent_details_emits_device_inventory_changed_when_software_changes(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    emitted_events: list[tuple[str, Any, str]] = []
    monkeypatch.setattr(
        engine_harness.context.socketio,
        "emit",
        lambda event, payload, namespace="/": emitted_events.append((event, payload, namespace)),
    )

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/details",
        json={
            "hostname": "test-device",
            "details": {
                "summary": {
                    "hostname": "test-device",
                },
                "software": [
                    {
                        "name": "Adobe Acrobat (64-bit)",
                        "version": "26.001.21431",
                        "source": "local_installed",
                        "metadata": {
                            "publisher": "Adobe",
                            "install_location": "C:\\Program Files\\Adobe\\Acrobat DC\\",
                            "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe,0",
                        },
                    }
                ],
            },
        },
        headers=_device_headers(),
    )

    assert response.status_code == 200
    assert (
        "device_inventory_changed",
        {"hostname": "test-device", "change": "software_updated"},
        "/",
    ) in emitted_events


def test_agent_details_merge_top_level_software_metadata_into_existing_metadata(
    engine_harness: EngineTestHarness,
) -> None:
    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/details",
        json={
            "hostname": "test-device",
            "agent_hash": "hash-123",
            "details": {
                "summary": {
                    "hostname": "test-device",
                    "last_seen": 1_700_000_801,
                    "operating_system": "Windows 11 Pro",
                },
                "software": [
                    {
                        "name": "7-Zip 25.01 (x64)",
                        "version": "25.01",
                        "source": "local_installed",
                        "metadata": {
                            "publisher": "Igor Pavlov",
                            "install_location": "C:\\Program Files\\7-Zip\\",
                        },
                        "estimated_size_kb": 123456,
                        "display_icon": r"C:\Program Files\7-Zip\7zFM.exe,0",
                        "uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe"',
                        "quiet_uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe" /S',
                    }
                ],
            },
        },
        headers=_device_headers(),
    )
    assert response.status_code == 200

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT software FROM devices WHERE hostname = ?", ("test-device",))
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert json.loads(row[0]) == [
        {
            "name": "7-Zip 25.01 (x64)",
            "version": "25.01",
            "source": "local_installed",
            "metadata": {
                "publisher": "Igor Pavlov",
                "install_location": "C:\\Program Files\\7-Zip\\",
                "estimated_size_kb": 123456,
                "display_icon": r"C:\Program Files\7-Zip\7zFM.exe,0",
                "uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe"',
                "quiet_uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe" /S',
            },
        }
    ]

    admin_client = _client_with_admin_session(engine_harness)
    detail_response = admin_client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    assert detail_response.get_json()["software"] == [
        {
            "name": "7-Zip 25.01 (x64)",
            "version": "25.01",
            "source": "local_installed",
            "metadata": {
                "publisher": "Igor Pavlov",
                "install_location": "C:\\Program Files\\7-Zip\\",
                "estimated_size_kb": 123456,
                "display_icon": r"C:\Program Files\7-Zip\7zFM.exe,0",
                "uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe"',
                "quiet_uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe" /S',
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Uses the registry quiet uninstall string.",
                "strategy": "direct_command",
                "rule_id": "metadata_quiet_uninstall_string",
                "quiet_uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe" /S',
                "uninstall_string": r'"C:\Program Files\7-Zip\Uninstall.exe"',
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_agent_details_persists_software_icon_assets(engine_harness: EngineTestHarness) -> None:
    icon_bytes = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+qD4AAAAASUVORK5CYII="
    )
    icon_hash = hashlib.sha256(icon_bytes).hexdigest()
    icon_base64 = base64.b64encode(icon_bytes).decode("ascii")

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/details",
        json={
            "hostname": "test-device",
            "agent_hash": "hash-123",
            "details": {
                "summary": {
                    "hostname": "test-device",
                    "last_seen": 1_700_000_802,
                    "operating_system": "Windows 11 Pro",
                },
                "software": [
                    {
                        "name": "Contoso Agent",
                        "version": "3.1.4",
                        "source": "local_installed",
                        "metadata": {
                            "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
                            "icon_hash": icon_hash,
                        },
                    }
                ],
                "software_icon_payloads": [
                    {
                        "icon_hash": icon_hash,
                        "mime_type": "image/png",
                        "data_base64": icon_base64,
                    }
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
            "SELECT icon_hash, mime_type, icon_bytes, byte_size FROM software_icon_assets WHERE icon_hash = ?",
            (icon_hash,),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert row[0] == icon_hash
    assert row[1] == "image/png"
    assert bytes(row[2]) == icon_bytes
    assert row[3] == len(icon_bytes)

    admin_client = _client_with_admin_session(engine_harness)
    detail_response = admin_client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    assert detail_response.get_json()["software"] == [
        {
            "name": "Contoso Agent",
            "version": "3.1.4",
            "source": "local_installed",
            "metadata": {
                "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
                "icon_hash": icon_hash,
            },
            "uninstall": {
                "supported": False,
                "reason": "This software row does not expose a usable uninstall command yet.",
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_agent_details_accepts_session_and_process_shape_upgrade_without_blocking_software_metadata(
    engine_harness: EngineTestHarness,
) -> None:
    icon_bytes = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+qD4AAAAASUVORK5CYII="
    )
    icon_hash = hashlib.sha256(icon_bytes).hexdigest()
    icon_base64 = base64.b64encode(icon_bytes).decode("ascii")

    client = engine_harness.app.test_client()
    first_response = client.post(
        "/api/agent/details",
        json={
            "hostname": "test-device",
            "agent_hash": "hash-123",
            "details": {
                "summary": {
                    "hostname": "test-device",
                    "last_seen": 1_700_000_803,
                    "operating_system": "Windows 11 Pro",
                },
                "sessions": [
                    {
                        "session_id": 1,
                        "username": "operator",
                        "session_name": "Console",
                        "state": "Active",
                    }
                ],
                "processes": [
                    {
                        "name": "explorer.exe",
                        "count": 1,
                    }
                ],
            },
        },
        headers=_device_headers(),
    )
    assert first_response.status_code == 200

    second_response = client.post(
        "/api/agent/details",
        json={
            "hostname": "test-device",
            "agent_hash": "hash-123",
            "details": {
                "summary": {
                    "hostname": "test-device",
                    "last_seen": 1_700_000_804,
                    "operating_system": "Windows 11 Pro",
                },
                "sessions": [
                    {
                        "session_id": 1,
                        "username": "operator",
                        "session_name": "Console",
                        "state": "Active",
                    }
                ],
                "processes": [
                    {
                        "name": "explorer.exe",
                        "count": 1,
                    }
                ],
                "software": [
                    {
                        "name": "Contoso Agent",
                        "version": "3.1.4",
                        "source": "local_installed",
                        "metadata": {
                            "estimated_size_kb": 654321,
                            "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
                            "icon_hash": icon_hash,
                        },
                    }
                ],
                "software_icon_payloads": [
                    {
                        "icon_hash": icon_hash,
                        "mime_type": "image/png",
                        "data_base64": icon_base64,
                    }
                ],
            },
        },
        headers=_device_headers(),
    )
    assert second_response.status_code == 200

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT icon_hash, icon_bytes FROM software_icon_assets WHERE icon_hash = ?",
            (icon_hash,),
        )
        icon_row = cur.fetchone()
    finally:
        conn.close()

    assert icon_row is not None
    assert icon_row[0] == icon_hash
    assert bytes(icon_row[1]) == icon_bytes

    admin_client = _client_with_admin_session(engine_harness)
    detail_response = admin_client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    detail_payload = detail_response.get_json()
    assert detail_payload["sessions"] == [
        {
            "eligible_for_interactive": True,
            "helper_last_seen_at": 0,
            "helper_pid": 0,
            "helper_ready": False,
            "is_rdp": False,
            "protocol": "console",
            "session_id": 1,
            "session_name": "Console",
            "state": "Active",
            "state_code": "active",
            "username": "operator",
        }
    ]
    assert detail_payload["processes"] == [
        {
            "count": 1,
            "name": "explorer.exe",
        }
    ]
    assert detail_payload["software"] == [
        {
            "name": "Contoso Agent",
            "version": "3.1.4",
            "source": "local_installed",
            "metadata": {
                "estimated_size_kb": 654321,
                "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
                "icon_hash": icon_hash,
            },
            "uninstall": {
                "supported": False,
                "reason": "This software row does not expose a usable uninstall command yet.",
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_device_software_icon_endpoint_serves_cached_asset(engine_harness: EngineTestHarness) -> None:
    icon_bytes = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7+qD4AAAAASUVORK5CYII="
    )
    icon_hash = hashlib.sha256(icon_bytes).hexdigest()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO software_icon_assets (
                icon_hash,
                mime_type,
                icon_bytes,
                byte_size,
                created_at,
                updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (
                icon_hash,
                "image/png",
                icon_bytes,
                len(icon_bytes),
                1_700_000_800,
                1_700_000_800,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    client = _client_with_admin_session(engine_harness)
    response = client.get(f"/api/device/software/icon/{icon_hash}")

    assert response.status_code == 200
    assert response.mimetype == "image/png"
    assert response.data == icon_bytes


def test_device_software_uninstall_queues_quick_job(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Google Chrome",
                "version": "124.0.6367.92",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Google LLC",
                    "uninstall_string": "C:\\Program Files\\Google\\Chrome\\Application\\124.0.6367.92\\Installer\\setup.exe --uninstall --multi-install --chrome --system-level",
                },
            }
        ],
    )

    targeted_events: list[tuple[str, str, str, dict[str, Any]]] = []
    monkeypatch.setattr(
        engine_harness.context,
        "has_host_service_socket",
        lambda hostname, mode: hostname == "test-device" and mode == "system",
    )
    monkeypatch.setattr(
        engine_harness.context,
        "emit_host_service_event",
        lambda hostname, service_mode, event, payload: (
            targeted_events.append((hostname, service_mode, event, payload)) or True
        ),
    )
    socket_events: list[tuple[str, Any, str]] = []
    monkeypatch.setattr(
        engine_harness.context.socketio,
        "emit",
        lambda event, payload, to=None: socket_events.append((event, payload, to)),
    )

    response = client.post(
        "/api/device/software/test-device/uninstall",
        json={
            "name": "Google Chrome",
            "version": "124.0.6367.92",
            "source": "local_installed",
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "queued"
    assert payload["software"]["name"] == "Google Chrome"
    assert payload["uninstall"]["strategy"] == "direct_command"
    assert payload["uninstall"]["summary"] == "Chrome setup.exe uninstall can be forced silent."
    assert payload["uninstall"]["rule_id"] == "chrome_setup_force_uninstall"
    assert "setup.exe" in payload["uninstall"]["command_preview"]
    assert len(targeted_events) == 1
    hostname, service_mode, event_name, dispatched = targeted_events[0]
    assert hostname == "test-device"
    assert service_mode == "system"
    assert event_name == "quick_job_run"
    assert dispatched["script_type"] == "powershell"
    assert dispatched["target_context"] == "system"
    assert dispatched["environment"]["SOFTWARE_NAME"] == "Google Chrome"
    assert dispatched["environment"]["SOFTWARE_SOURCE"] == "local_installed"
    assert (
        dispatched["environment"]["QUIET_UNINSTALL_STRING"]
        == '"C:\\Program Files\\Google\\Chrome\\Application\\124.0.6367.92\\Installer\\setup.exe" --uninstall --multi-install --chrome --system-level --force-uninstall'
    )
    assert "Invoke-LocalInstalledUninstall" in base64.b64decode(dispatched["script_content"]).decode("utf-8")
    assert any(event_name == "device_activity_changed" for event_name, _payload, _to in socket_events)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT script_name, script_path, script_type, status, queue_lane, activity_kind
              FROM activity_history
             ORDER BY id DESC
             LIMIT 1
            """
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == (
        "Uninstall - Google Chrome",
        "Scripts/Internal/Software_Uninstall.ps1",
        "powershell",
        "Queued",
        "software_management",
        "software_uninstall",
    )


def test_device_software_uninstall_requires_supported_metadata(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Odd Vendor App",
                "version": "1.0.0",
                "source": "local_installed",
                "metadata": {},
            }
        ],
    )

    response = client.post(
        "/api/device/software/test-device/uninstall",
        json={
            "name": "Odd Vendor App",
            "version": "1.0.0",
            "source": "local_installed",
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert payload["error"] == "software_uninstall_unsupported"


def test_quick_job_progress_updates_activity_history_and_broadcasts(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    socket_client = engine_harness.context.socketio.test_client(
        engine_harness.app,
        flask_test_client=client,
    )
    assert socket_client.is_connected()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        engine_database._ensure_activity_history(conn, logger=None)
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO activity_history(
                hostname,
                script_path,
                script_name,
                script_type,
                ran_at,
                status,
                stdout,
                stderr,
                queue_lane,
                activity_kind,
                metadata_json
            ) VALUES(?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "test-device",
                "Scripts/Internal/Software_Uninstall.ps1",
                "Uninstall - Contoso Agent",
                "powershell",
                1_777_000_000,
                "Queued",
                "",
                "",
                "software_management",
                "software_uninstall",
                json.dumps({"software_name": "Contoso Agent"}),
            ),
        )
        activity_id = int(cur.lastrowid)
        conn.commit()
    finally:
        conn.close()

    socket_client.emit(
        "quick_job_progress",
        {
            "job_id": activity_id,
            "status": "Running",
            "queue_lane": "software_management",
            "activity_kind": "software_uninstall",
            "metadata": {
                "software_name": "Contoso Agent",
                "command_preview": "setup.exe --uninstall --force-uninstall",
            },
            "stdout": "Starting uninstall\n",
            "append_output": True,
        },
    )

    received = socket_client.get_received()
    assert any(
        item.get("name") == "device_activity_changed"
        and item.get("args")
        and item["args"][0].get("activity_id") == activity_id
        and item["args"][0].get("queue_lane") == "software_management"
        and item["args"][0].get("activity_kind") == "software_uninstall"
        and item["args"][0].get("status") == "Running"
        for item in received
    )

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT status, stdout, queue_lane, activity_kind, metadata_json, started_at, updated_at, finished_at
              FROM activity_history
             WHERE id=?
            """,
            (activity_id,),
        )
        row = cur.fetchone()
    finally:
        conn.close()
        socket_client.disconnect()

    assert row is not None
    assert row[0] == "Running"
    assert row[1] == "Starting uninstall\n"
    assert row[2] == "software_management"
    assert row[3] == "software_uninstall"
    metadata = json.loads(row[4] or "{}")
    assert metadata["software_name"] == "Contoso Agent"
    assert metadata["command_preview"] == "setup.exe --uninstall --force-uninstall"
    assert int(row[5] or 0) > 0
    assert int(row[6] or 0) > 0
    assert row[7] in (None, 0, "0")


def test_device_software_uninstall_rejects_non_removable_windows_store_package(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Microsoft.LockApp",
                "version": "10.0.0.0",
                "source": "windows_store",
                "metadata": {
                    "package_family_name": "Microsoft.LockApp_cw5n1h2txyewy",
                    "non_removable": True,
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "Microsoft.LockApp",
            "version": "10.0.0.0",
            "source": "windows_store",
            "metadata": {
                "package_family_name": "Microsoft.LockApp_cw5n1h2txyewy",
                "non_removable": True,
            },
            "uninstall": {
                "supported": False,
                "reason": "Windows marks this Store package as non-removable.",
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]

    response = client.post(
        "/api/device/software/test-device/uninstall",
        json={
            "name": "Microsoft.LockApp",
            "version": "10.0.0.0",
            "source": "windows_store",
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert payload["error"] == "software_uninstall_unsupported"
    assert payload["message"] == "Windows marks this Store package as non-removable."


def test_device_software_uninstall_supports_windows_store_package_family_without_removability_hint(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Clipchamp.Clipchamp",
                "version": "4.5.10020.0",
                "source": "windows_store",
                "metadata": {
                    "package_family_name": "Clipchamp.Clipchamp_yxz26nhyzhsrt",
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "Clipchamp.Clipchamp",
            "version": "4.5.10020.0",
            "source": "windows_store",
            "metadata": {
                "package_family_name": "Clipchamp.Clipchamp_yxz26nhyzhsrt",
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Windows Store package uninstall.",
                "strategy": "windows_store",
                "rule_id": "metadata_windows_store_family_name",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "Clipchamp.Clipchamp_yxz26nhyzhsrt",
            },
        }
    ]


def test_device_software_uninstall_supports_install_location_derived_windows_rules(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "7-Zip 25.01 (x64)",
                "version": "25.01",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Igor Pavlov",
                    "install_location": "C:\\Program Files\\7-Zip\\",
                },
            },
            {
                "name": "Mozilla Firefox (x64 en-US)",
                "version": "149.0.2",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Mozilla",
                    "install_location": "C:\\Program Files\\Mozilla Firefox",
                },
            },
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "7-Zip 25.01 (x64)",
            "version": "25.01",
            "source": "local_installed",
            "metadata": {
                "publisher": "Igor Pavlov",
                "install_location": "C:\\Program Files\\7-Zip\\",
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Derived 7-Zip uninstall from install location.",
                "strategy": "direct_command",
                "rule_id": "install_location_7zip",
                "quiet_uninstall_string": '"C:\\Program Files\\7-Zip\\Uninstall.exe" /S',
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        },
        {
            "name": "Mozilla Firefox (x64 en-US)",
            "version": "149.0.2",
            "source": "local_installed",
            "metadata": {
                "publisher": "Mozilla",
                "install_location": "C:\\Program Files\\Mozilla Firefox",
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Derived Firefox uninstall from install location.",
                "strategy": "direct_command",
                "rule_id": "install_location_firefox_helper",
                "quiet_uninstall_string": '"C:\\Program Files\\Mozilla Firefox\\uninstall\\helper.exe" /S',
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        },
    ]


def test_device_software_uninstall_applies_file_override(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_uninstall_overrides.json"
    override_path.write_text(
        json.dumps(
            {
                "windows_uninstall_overrides": [
                    {
                        "rule_id": "uninstall_override_fedora_media_writer",
                        "source": "local_installed",
                        "name": "Fedora Media Writer",
                        "version": "5.2.8",
                        "publisher_contains_any": ["Fedora Project"],
                        "strategy": "direct_command",
                        "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" remove --confirm-command',
                        "summary": "Uses a verified Fedora Media Writer unattended uninstall command override.",
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_uninstall_module,
        "_UNINSTALL_OVERRIDES_CACHE",
        {"windows_uninstall_overrides": []},
    )

    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Fedora Media Writer",
                "version": "5.2.8",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Fedora Project",
                    "install_location": "C:\\Program Files\\Fedora Media Writer",
                    "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" /S',
                    "uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe"',
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "Fedora Media Writer",
            "version": "5.2.8",
            "source": "local_installed",
            "metadata": {
                "publisher": "Fedora Project",
                "install_location": "C:\\Program Files\\Fedora Media Writer",
                "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" /S',
                "uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe"',
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Uses a verified Fedora Media Writer unattended uninstall command override.",
                "strategy": "direct_command",
                "rule_id": "uninstall_override_fedora_media_writer",
                "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" remove --confirm-command',
                "uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe"',
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_device_software_uninstall_blocks_known_interactive_quiet_string(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Fedora Media Writer",
                "version": "5.2.8",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Fedora Project",
                    "install_location": "C:\\Program Files\\Fedora Media Writer",
                    "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" /S',
                    "uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe"',
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "Fedora Media Writer",
            "version": "5.2.8",
            "source": "local_installed",
            "metadata": {
                "publisher": "Fedora Project",
                "install_location": "C:\\Program Files\\Fedora Media Writer",
                "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" /S',
                "uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe"',
            },
            "uninstall": {
                "supported": False,
                "reason": (
                    "Fedora Media Writer's registered QuietUninstallString still prompts for confirmation. "
                    "Borealis blocks automated uninstall for this title until a verified unattended command is known."
                ),
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]

    response = client.post(
        "/api/device/software/test-device/uninstall",
        json={
            "name": "Fedora Media Writer",
            "version": "5.2.8",
            "source": "local_installed",
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert payload["error"] == "software_uninstall_unsupported"
    assert (
        payload["message"]
        == "Fedora Media Writer's registered QuietUninstallString still prompts for confirmation. "
        "Borealis blocks automated uninstall for this title until a verified unattended command is known."
    )


def test_device_software_uninstall_marks_steam_protocol_titles_as_unsupported(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Garry's Mod",
                "version": "",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Facepunch Studios",
                    "install_location": "F:\\SteamLibrary\\steamapps\\common\\GarrysMod",
                    "uninstall_string": '"C:\\Program Files (x86)\\Steam\\steam.exe" steam://uninstall/4000',
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "Garry's Mod",
            "version": "",
            "source": "local_installed",
            "metadata": {
                "publisher": "Facepunch Studios",
                "install_location": "F:\\SteamLibrary\\steamapps\\common\\GarrysMod",
                "uninstall_string": '"C:\\Program Files (x86)\\Steam\\steam.exe" steam://uninstall/4000',
            },
            "distribution_platform": "steam",
            "distribution_app_id": "4000",
            "uninstall": {
                "supported": False,
                "reason": "Steam manages this title, and Borealis does not yet have a verified unattended uninstall path.",
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]

    response = client.post(
        "/api/device/software/test-device/uninstall",
        json={
            "name": "Garry's Mod",
            "version": "",
            "source": "local_installed",
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert payload["error"] == "software_uninstall_unsupported"
    assert (
        payload["message"]
        == "Steam manages this title, and Borealis does not yet have a verified unattended uninstall path."
    )


def test_device_software_uninstall_marks_install_location_only_steam_titles_as_unsupported(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Abiotic Factor",
                "version": "",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Deep Field Games",
                    "install_location": "F:\\SteamLibrary\\steamapps\\common\\AbioticFactor",
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software = detail_response.get_json()["software"]
    assert software == [
        {
            "name": "Abiotic Factor",
            "version": "",
            "source": "local_installed",
            "metadata": {
                "publisher": "Deep Field Games",
                "install_location": "F:\\SteamLibrary\\steamapps\\common\\AbioticFactor",
            },
            "distribution_platform": "steam",
            "uninstall": {
                "supported": False,
                "reason": "Steam manages this title, and Borealis does not yet have a verified unattended uninstall path.",
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_agent_heartbeat_returns_assigned_site(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/heartbeat",
        headers=_device_headers(),
        json={
            "hostname": "test-device",
            "service_mode": "system",
            "metrics": {},
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["site_name"] == "Main Lab"
    assert payload["site_id"] == 1


def test_agent_software_management_overrides_endpoint_returns_icon_overrides(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    override_path = tmp_path / "software_icons_overrides.json"
    override_payload = {
        "windows_icon_overrides": [
            {
                "rule_id": "icon_override_contoso_agent",
                "name": "Contoso Agent",
                "display_icon": r"C:\Program Files\Contoso Agent\branding\agent.ico",
            }
        ]
    }
    override_path.write_text(json.dumps(override_payload), encoding="utf-8")
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": []},
    )

    client = engine_harness.app.test_client()
    response = client.get(
        "/api/agent/software-management/overrides",
        headers=_device_headers(),
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload == override_payload


def test_device_software_icon_override_persists_rule_and_requests_refresh(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_icons_overrides.json"
    override_path.write_text(json.dumps({"windows_icon_overrides": []}), encoding="utf-8")
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Adobe Acrobat (64-bit)",
                "version": "26.001.21431",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Adobe",
                    "install_location": "C:\\Program Files\\Adobe\\Acrobat DC\\",
                    "product_code": "{AC76BA86-1033-FF00-7760-BC15014EA700}",
                },
            }
        ],
    )

    targeted_events: list[tuple[str, str, str, dict[str, Any]]] = []
    monkeypatch.setattr(
        engine_harness.context,
        "emit_host_service_event",
        lambda hostname, service_mode, event, payload: (
            targeted_events.append((hostname, service_mode, event, payload)) or True
        ),
    )

    response = client.post(
        "/api/device/software/test-device/icon-override",
        json={
            "name": "Adobe Acrobat (64-bit)",
            "version": "26.001.21431",
            "source": "local_installed",
            "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe",
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["refresh_requested"] is True
    assert payload["rule"]["rule_id"] == "icon_override_adobe_acrobat_64_bit"
    assert payload["rule"]["display_icon"] == r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe,0"
    assert payload["rule"]["name"] == "Adobe Acrobat (64-bit)"
    assert len(targeted_events) == 1
    target_hostname, service_mode, event_name, dispatched = targeted_events[0]
    assert target_hostname == "test-device"
    assert service_mode == "system"
    assert event_name == "software_inventory_refresh_request"
    assert dispatched["reason"].startswith("operator_icon_override:")

    agent_response = engine_harness.app.test_client().get(
        "/api/agent/software-management/overrides",
        headers=_device_headers(),
    )
    assert agent_response.status_code == 200
    override_payload = agent_response.get_json()
    assert override_payload["windows_icon_overrides"] == [
        {
            "rule_id": payload["rule"]["rule_id"],
            "name": "Adobe Acrobat (64-bit)",
            "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe,0",
        }
    ]


def test_device_software_icon_override_can_clear_icon_and_request_refresh(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_icons_overrides.json"
    override_path.write_text(json.dumps({"windows_icon_overrides": []}), encoding="utf-8")
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Adobe Acrobat (64-bit)",
                "version": "26.001.21431",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Adobe",
                    "install_location": "C:\\Program Files\\Adobe\\Acrobat DC\\",
                    "product_code": "{AC76BA86-1033-FF00-7760-BC15014EA700}",
                },
            }
        ],
    )

    targeted_events: list[tuple[str, str, str, dict[str, Any]]] = []
    monkeypatch.setattr(
        engine_harness.context,
        "emit_host_service_event",
        lambda hostname, service_mode, event, payload: (
            targeted_events.append((hostname, service_mode, event, payload)) or True
        ),
    )

    response = client.post(
        "/api/device/software/test-device/icon-override",
        json={
            "name": "Adobe Acrobat (64-bit)",
            "version": "26.001.21431",
            "source": "local_installed",
            "clear_icon": True,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["refresh_requested"] is True
    assert payload["rule"]["clear_icon"] is True
    assert "display_icon" not in payload["rule"]
    assert len(targeted_events) == 1
    target_hostname, service_mode, event_name, dispatched = targeted_events[0]
    assert target_hostname == "test-device"
    assert service_mode == "system"
    assert event_name == "software_inventory_refresh_request"
    assert dispatched["reason"].startswith("operator_icon_override:")

    agent_response = engine_harness.app.test_client().get(
        "/api/agent/software-management/overrides",
        headers=_device_headers(),
    )
    assert agent_response.status_code == 200
    override_payload = agent_response.get_json()
    assert override_payload["windows_icon_overrides"] == [
        {
            "rule_id": payload["rule"]["rule_id"],
            "name": "Adobe Acrobat (64-bit)",
            "clear_icon": True,
        }
    ]


def test_device_software_icon_override_replaces_legacy_same_name_rule(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_icons_overrides.json"
    override_path.write_text(
        json.dumps(
            {
                "windows_icon_overrides": [
                    {
                        "rule_id": "icon_override_adobe_acrobat_64_bit_26_001_11111",
                        "name": "Adobe Acrobat (64-bit)",
                        "version": "26.001.11111",
                        "publisher_contains_any": ["Adobe"],
                        "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Old\Acrobat.exe,0",
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Adobe Acrobat (64-bit)",
                "version": "26.001.21431",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Adobe",
                    "install_location": "C:\\Program Files\\Adobe\\Acrobat DC\\",
                },
            }
        ],
    )

    response = client.post(
        "/api/device/software/test-device/icon-override",
        json={
            "name": "Adobe Acrobat (64-bit)",
            "version": "26.001.21431",
            "source": "local_installed",
            "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe",
        },
    )

    assert response.status_code == 200

    persisted = json.loads(override_path.read_text(encoding="utf-8"))
    assert persisted == {
        "windows_icon_overrides": [
            {
                "rule_id": "icon_override_adobe_acrobat_64_bit",
                "name": "Adobe Acrobat (64-bit)",
                "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe,0",
            }
        ]
    }


def test_device_details_backfills_global_icon_override_hash_from_other_device_inventory(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_icons_overrides.json"
    override_path.write_text(
        json.dumps(
            {
                "windows_icon_overrides": [
                    {
                        "rule_id": "icon_override_wireguard",
                        "name": "WireGuard",
                        "display_icon": r"C:\Program Files\WireGuard\wireguard.exe,0",
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "WireGuard",
                "version": "0.5.3",
                "source": "local_installed",
                "metadata": {
                    "publisher": "WireGuard LLC",
                },
            }
        ],
    )
    known_hash = "ab" * 32
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO device_software_inventory(
                device_guid,
                name,
                name_normalized,
                version,
                source,
                captured_at,
                metadata_json
            ) VALUES (?,?,?,?,?,?,?)
            """,
            (
                "GUID-OTHER-DEVICE-0001",
                "WireGuard",
                "wireguard",
                "0.5.3",
                "local_installed",
                int(time.time()),
                json.dumps(
                    {
                        "icon_hash": known_hash,
                        "display_icon_override_rule_id": "icon_override_wireguard",
                        "display_icon_override": r"C:\Program Files\WireGuard\wireguard.exe,0",
                    }
                ),
            ),
        )
        conn.commit()
    finally:
        conn.close()

    detail_response = client.get("/api/device/details/test-device")

    assert detail_response.status_code == 200
    detail_payload = detail_response.get_json()
    software_row = detail_payload["details"]["software"][0]
    metadata = software_row["metadata"]
    assert metadata["icon_hash"] == known_hash
    assert metadata["display_icon"] == r"C:\Program Files\WireGuard\wireguard.exe,0"
    assert metadata["display_icon_override"] == r"C:\Program Files\WireGuard\wireguard.exe,0"
    assert metadata["display_icon_override_rule_id"] == "icon_override_wireguard"
    assert metadata["display_icon_override_cleared"] is False


def test_device_details_applies_clear_icon_override_even_for_stale_rows(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_icons_overrides.json"
    override_path.write_text(
        json.dumps(
            {
                "windows_icon_overrides": [
                    {
                        "rule_id": "icon_override_wireguard_blank",
                        "name": "WireGuard",
                        "clear_icon": True,
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_icons_module, "_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_icons_module,
        "_SOFTWARE_ICON_OVERRIDES_CACHE",
        {"windows_icon_overrides": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "WireGuard",
                "version": "0.5.3",
                "source": "local_installed",
                "metadata": {
                    "display_icon": r"C:\Program Files\WireGuard\wireguard.exe,0",
                    "icon_hash": "cd" * 32,
                },
            }
        ],
    )

    detail_response = client.get("/api/device/details/test-device")

    assert detail_response.status_code == 200
    detail_payload = detail_response.get_json()
    software_row = detail_payload["details"]["software"][0]
    metadata = software_row["metadata"]
    assert "icon_hash" not in metadata
    assert metadata["display_icon"] == ""
    assert metadata["display_icon_override"] == ""
    assert metadata["display_icon_override_rule_id"] == "icon_override_wireguard_blank"
    assert metadata["display_icon_override_cleared"] is True
    assert metadata["original_display_icon"] == r"C:\Program Files\WireGuard\wireguard.exe,0"


def test_device_software_uninstall_override_hotloads_into_detail_enrichment(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    override_path = tmp_path / "software_uninstall_overrides.json"
    override_path.write_text(json.dumps({"windows_uninstall_overrides": []}), encoding="utf-8")
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_OVERRIDES_PATH", override_path)
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_OVERRIDES_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_uninstall_module,
        "_UNINSTALL_OVERRIDES_CACHE",
        {"windows_uninstall_overrides": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Contoso Widget",
                "version": "1.2.3",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Contoso Ltd",
                    "install_location": "C:\\Program Files\\Contoso Widget",
                    "uninstall_string": '"C:\\Program Files\\Contoso Widget\\remove.exe"',
                },
            }
        ],
    )

    response = client.post(
        "/api/device/software/test-device/uninstall-override",
        json={
            "name": "Contoso Widget",
            "version": "1.2.3",
            "source": "local_installed",
            "application_path": r"C:\Program Files\Contoso Widget\remove.exe",
            "arguments": "/S /norestart",
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["rule"]["quiet_uninstall_string"] == r'"C:\Program Files\Contoso Widget\remove.exe" /S /norestart'

    detail_response = client.get("/api/device/details/test-device")
    assert detail_response.status_code == 200
    software_rows = detail_response.get_json()["software"]
    assert software_rows == [
        {
            "name": "Contoso Widget",
            "version": "1.2.3",
            "source": "local_installed",
            "metadata": {
                "publisher": "Contoso Ltd",
                "install_location": "C:\\Program Files\\Contoso Widget",
                "uninstall_string": '"C:\\Program Files\\Contoso Widget\\remove.exe"',
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Operator-defined global uninstall override.",
                "strategy": "direct_command",
                "rule_id": payload["rule"]["rule_id"],
                "quiet_uninstall_string": r'"C:\Program Files\Contoso Widget\remove.exe" /S /norestart',
                "uninstall_string": '"C:\\Program Files\\Contoso Widget\\remove.exe"',
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_device_software_uninstall_block_and_unblock_hotload(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path,
) -> None:
    client = _client_with_admin_session(engine_harness)
    blocklist_path = tmp_path / "software_uninstall_blocklist.json"
    blocklist_path.write_text(json.dumps({"windows_quiet_uninstall_blocklist": []}), encoding="utf-8")
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_BLOCKLIST_PATH", blocklist_path)
    monkeypatch.setattr(software_uninstall_module, "_UNINSTALL_BLOCKLIST_CACHE_MTIME_NS", None)
    monkeypatch.setattr(
        software_uninstall_module,
        "_UNINSTALL_BLOCKLIST_CACHE",
        {"windows_quiet_uninstall_blocklist": []},
    )
    _set_test_device_software(
        engine_harness,
        [
            {
                "name": "Widget Tool",
                "version": "9.0.0",
                "source": "local_installed",
                "metadata": {
                    "publisher": "Widget Labs",
                    "install_location": "C:\\Program Files\\Widget Tool",
                    "quiet_uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe" /S',
                    "uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe"',
                },
            }
        ],
    )

    block_response = client.post(
        "/api/device/software/test-device/uninstall-block",
        json={
            "name": "Widget Tool",
            "version": "9.0.0",
            "source": "local_installed",
            "reason": "Vendor uninstall still prompts for confirmation.",
        },
    )
    assert block_response.status_code == 200
    block_payload = block_response.get_json()
    assert block_payload["status"] == "ok"

    blocked_detail = client.get("/api/device/details/test-device")
    assert blocked_detail.status_code == 200
    blocked_software = blocked_detail.get_json()["software"]
    assert blocked_software == [
        {
            "name": "Widget Tool",
            "version": "9.0.0",
            "source": "local_installed",
            "metadata": {
                "publisher": "Widget Labs",
                "install_location": "C:\\Program Files\\Widget Tool",
                "quiet_uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe" /S',
                "uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe"',
            },
            "uninstall": {
                "supported": False,
                "reason": "Vendor uninstall still prompts for confirmation.",
                "summary": "",
                "strategy": "",
                "rule_id": "",
                "quiet_uninstall_string": "",
                "uninstall_string": "",
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]

    unblock_response = client.post(
        "/api/device/software/test-device/uninstall-unblock",
        json={
            "name": "Widget Tool",
            "version": "9.0.0",
            "source": "local_installed",
        },
    )
    assert unblock_response.status_code == 200
    unblock_payload = unblock_response.get_json()
    assert unblock_payload["status"] == "ok"
    assert unblock_payload["removed_rule_ids"] == [block_payload["rule"]["rule_id"]]

    unblocked_detail = client.get("/api/device/details/test-device")
    assert unblocked_detail.status_code == 200
    unblocked_software = unblocked_detail.get_json()["software"]
    assert unblocked_software == [
        {
            "name": "Widget Tool",
            "version": "9.0.0",
            "source": "local_installed",
            "metadata": {
                "publisher": "Widget Labs",
                "install_location": "C:\\Program Files\\Widget Tool",
                "quiet_uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe" /S',
                "uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe"',
            },
            "uninstall": {
                "supported": True,
                "reason": "",
                "summary": "Uses the registry quiet uninstall string.",
                "strategy": "direct_command",
                "rule_id": "metadata_quiet_uninstall_string",
                "quiet_uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe" /S',
                "uninstall_string": '"C:\\Program Files\\Widget Tool\\uninstall.exe"',
                "product_code": "",
                "package_family_name": "",
            },
        }
    ]


def test_device_software_refresh_route_requests_system_refresh(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    targeted_events: list[tuple[str, str, str, dict[str, Any]]] = []
    monkeypatch.setattr(
        engine_harness.context,
        "emit_host_service_event",
        lambda hostname, service_mode, event, payload: (
            targeted_events.append((hostname, service_mode, event, payload)) or True
        ),
    )

    response = client.post("/api/device/software/test-device/refresh")

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "queued"
    assert len(targeted_events) == 1
    hostname, service_mode, event_name, dispatched = targeted_events[0]
    assert hostname == "test-device"
    assert service_mode == "system"
    assert event_name == "software_inventory_refresh_request"
    assert dispatched["reason"] == "operator_query_software_updates"


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

    create_second_resp = client.post(
        "/api/device_list_views",
        json={"name": "alpha", "columns": ["hostname"], "filters": {}},
    )
    assert create_second_resp.status_code == 201

    fetch_resp = client.get("/api/device_list_views")
    view_names = [view["name"] for view in fetch_resp.get_json()["views"]]
    assert view_names == ["alpha", "Custom"]

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


def test_vpn_service_status_marks_peer_transport_recovering_when_handshake_never_arrives() -> None:
    service, wg, _socketio, _service_events = _build_vpn_service()
    payload = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.mark_transport_required("agent-1", reason="shell_connect")
    probe_age = max(40.0, float(WIREGUARD_KEEPALIVE_SECONDS) + 10.0)
    with service._lock:
        session = service._sessions_by_agent["agent-1"]
        session.created_at = time.time() - probe_age
        session.last_activity = time.time() - probe_age
        session.last_transport_probe_at = time.time() - probe_age
    wg.peer_health_overrides[payload["client_public_key"]] = {
        "healthy": False,
        "reason": "no_handshake",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": None,
        "last_handshake_at_iso": "",
        "handshake_age_seconds": None,
    }

    status = service.status("agent-1")

    assert status is not None
    assert status["status"] == "recovering"
    assert status["listener_healthy"] is False
    assert status["recovery_in_progress"] is True
    assert status["transport_ready"] is False
    assert status["peer_health_reason"] == "no_recent_handshake"


def test_vpn_service_recent_confirmed_transport_suppresses_handshake_recovery() -> None:
    service, wg, _socketio, _service_events = _build_vpn_service()
    payload = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.mark_transport_required("agent-1", reason="shell_connect")
    with service._lock:
        session = service._sessions_by_agent["agent-1"]
        session.created_at = time.time() - 10
        session.last_activity = time.time() - 10
    wg.peer_health_overrides[payload["client_public_key"]] = {
        "healthy": False,
        "reason": "no_handshake",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": None,
        "last_handshake_at_iso": "",
        "handshake_age_seconds": None,
    }

    assert service.confirm_transport_success("agent-1", reason="shell_output") is True

    start_calls = wg.start_calls
    service._watchdog_tick()
    status = service.status("agent-1")

    assert wg.start_calls == start_calls
    assert status is not None
    assert status["status"] == "up"
    assert status["listener_healthy"] is True
    assert status["recovery_in_progress"] is False
    assert status["transport_ready"] is True
    assert status["peer_health_reason"] == "recent_transport_success"
    assert status["last_transport_confirmed_at"] is not None
    assert status["confirmed_age_seconds"] is not None


def test_vpn_service_shell_keepalive_confirm_logging_is_throttled() -> None:
    service, _wg, _socketio, service_events = _build_vpn_service()
    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    assert service.confirm_transport_success("agent-1", reason="shell_keepalive") is True
    assert service.confirm_transport_success("agent-1", reason="shell_keepalive") is True

    keepalive_logs = [
        message
        for _name, message, _level in service_events
        if "vpn_transport_confirmed" in message and "reason=shell_keepalive" in message
    ]

    assert len(keepalive_logs) == 1


def test_vpn_service_recent_confirmed_transport_remains_healthy_for_active_probe_window() -> None:
    service, wg, _socketio, service_events = _build_vpn_service()
    payload = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.mark_transport_required("agent-1", reason="shell_connect")
    age_seconds = min(float(PEER_ACTIVITY_WINDOW_SECONDS) - 1.0, float(PEER_CONFIRMED_ACTIVITY_WINDOW_SECONDS) + 5.0)
    with service._lock:
        session = service._sessions_by_agent["agent-1"]
        session.created_at = time.time() - age_seconds
        session.last_activity = time.time() - age_seconds
        session.last_transport_probe_at = time.time() - age_seconds
        session.last_transport_confirmed_at = time.time() - age_seconds
    wg.peer_health_overrides[payload["client_public_key"]] = {
        "healthy": False,
        "reason": "no_handshake",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": None,
        "last_handshake_at_iso": "",
        "handshake_age_seconds": None,
    }

    start_calls = wg.start_calls
    status = service.status("agent-1")
    service._watchdog_tick()

    assert status is not None
    assert status["status"] == "up"
    assert status["transport_ready"] is True
    assert status["peer_health_reason"] == "recent_transport_success"
    assert wg.start_calls == start_calls
    assert not any("vpn_transport_watchdog_recovery" in message for _name, message, _level in service_events)


def test_vpn_service_probe_grace_respects_wireguard_keepalive_window() -> None:
    service, wg, _socketio, service_events = _build_vpn_service()
    payload = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.mark_transport_required("agent-1", reason="shell_input")
    now = time.time()
    with service._lock:
        session = service._sessions_by_agent["agent-1"]
        session.created_at = now - (PEER_ACTIVITY_WINDOW_SECONDS + 30)
        session.last_activity = now - 1
        session.last_transport_probe_at = now - max(6.0, float(WIREGUARD_KEEPALIVE_SECONDS) - 10.0)
        session.last_transport_confirmed_at = now - (PEER_ACTIVITY_WINDOW_SECONDS + 10.0)
    wg.peer_health_overrides[payload["client_public_key"]] = {
        "healthy": False,
        "reason": "no_handshake",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": now - max(1.0, float(WIREGUARD_KEEPALIVE_SECONDS) - 1.0),
        "last_handshake_at_iso": "",
        "handshake_age_seconds": int(max(1.0, float(WIREGUARD_KEEPALIVE_SECONDS) - 1.0)),
    }

    status = service.status("agent-1")
    start_calls = wg.start_calls
    service._watchdog_tick()

    assert status is not None
    assert status["status"] == "up"
    assert status["transport_ready"] is True
    assert status["peer_health_reason"] == "probe_grace"
    assert wg.start_calls == start_calls
    assert not any("vpn_transport_watchdog_recovery" in message for _name, message, _level in service_events)


def test_vpn_service_passive_session_stays_idle_without_transport_probe() -> None:
    service, wg, _socketio, _service_events = _build_vpn_service()
    payload = service.connect(
        agent_id="agent-1",
        operator_id=None,
        endpoint_host="engine.local",
        mark_activity=False,
    )
    wg.peer_health_overrides[payload["client_public_key"]] = {
        "healthy": False,
        "reason": "no_handshake",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": None,
        "last_handshake_at_iso": "",
        "handshake_age_seconds": None,
    }

    status = service.status("agent-1")

    assert status is not None
    assert status["status"] == "up"
    assert status["listener_healthy"] is True
    assert status["recovery_in_progress"] is False
    assert status["transport_ready"] is True
    assert status["requires_transport"] is False
    assert status["peer_health_reason"] == "idle"
    assert status["last_transport_probe_at"] is None


def test_vpn_service_passive_session_reuse_does_not_refresh_transport_probe() -> None:
    service, _wg, _socketio, _service_events = _build_vpn_service()
    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.mark_transport_required("agent-1", reason="shell_connect")
    with service._lock:
        session = service._sessions_by_agent["agent-1"]
        session.last_transport_probe_at = time.time() - (PEER_ACTIVITY_WINDOW_SECONDS + 10)
        prior_probe_at = session.last_transport_probe_at
    service.connect(
        agent_id="agent-1",
        operator_id=None,
        endpoint_host="engine.local",
        mark_activity=False,
    )

    status = service.status("agent-1")

    assert status is not None
    assert status["requires_transport"] is False
    assert status["peer_health_reason"] == "idle"
    assert status["last_transport_probe_at"] == int(prior_probe_at)


def test_vpn_service_watchdog_recovers_when_peer_transport_is_unhealthy() -> None:
    service, wg, _socketio, service_events = _build_vpn_service()
    payload = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.mark_transport_required("agent-1", reason="shell_connect")
    probe_age = max(40.0, float(WIREGUARD_KEEPALIVE_SECONDS) + 10.0)
    with service._lock:
        session = service._sessions_by_agent["agent-1"]
        session.created_at = time.time() - probe_age
        session.last_activity = time.time() - probe_age
        session.last_transport_probe_at = time.time() - probe_age
    wg.peer_health_overrides[payload["client_public_key"]] = {
        "healthy": False,
        "reason": "no_handshake",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": None,
        "last_handshake_at_iso": "",
        "handshake_age_seconds": None,
    }

    start_calls = wg.start_calls
    service._watchdog_tick()

    assert wg.start_calls == start_calls + 1
    assert any("vpn_transport_watchdog_recovery" in message for _name, message, _level in service_events)


def test_vpn_service_request_agent_start_can_force_restart() -> None:
    service, _wg, socketio, _service_events = _build_vpn_service()
    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    payload = service.request_agent_start("agent-1", force_restart=True, reason="shell_connect_retry")

    assert payload is not None
    assert payload["force_restart"] is True
    assert payload["restart_reason"] == "shell_connect_retry"
    assert socketio.emits[-1][0] == "vpn_tunnel_start"
    assert socketio.emits[-1][1]["force_restart"] is True


def test_vpn_service_request_agent_start_expands_allowed_ports_for_nondefault_ansible_transport() -> None:
    service, wg, socketio, _service_events = _build_vpn_service()
    initial = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    payload = service.request_agent_start(
        "agent-1",
        reason="shared_ansible_prepare",
        required_ports=[2222],
    )

    assert initial["allowed_ports"] == [47002, 5900, 22]
    assert payload is not None
    assert payload["allowed_ports"] == [47002, 5900, 22, 2222]
    assert wg.apply_calls == 2
    assert wg.removed_rules == [["rule-agent-1"]]
    assert socketio.emits[-1][0] == "vpn_tunnel_start"
    assert socketio.emits[-1][1]["allowed_ports"] == [47002, 5900, 22, 2222]


def test_vpn_service_live_upserts_additional_peers_without_full_reconcile() -> None:
    service, wg, _socketio, _service_events = _build_vpn_service()

    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    assert wg.reconcile_calls == 1
    assert wg.upsert_calls == 0

    service.connect(agent_id="agent-2", operator_id=None, endpoint_host="engine.local")

    assert wg.reconcile_calls == 1
    assert wg.upsert_calls == 1


def test_vpn_service_watchdog_recovers_when_listener_peer_count_drifts() -> None:
    service, wg, _socketio, _service_events = _build_vpn_service()

    service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.connect(agent_id="agent-2", operator_id=None, endpoint_host="engine.local")
    start_calls = wg.start_calls

    wg.current_peers.pop("agent-2", None)
    wg.health = {
        "healthy": True,
        "reason": "listener_running",
        "service_state": "RUNNING",
    }

    status = service.status("agent-1")
    assert status is not None
    assert status["listener_healthy"] is False

    service._watchdog_tick()

    assert wg.start_calls == start_calls + 1
    recovered = service.status("agent-1")
    assert recovered is not None
    assert recovered["listener_healthy"] is True


def test_vpn_service_serializes_listener_refreshes() -> None:
    concurrent_wg = _ConcurrentFakeWireGuardManager()
    service, _wg, _socketio, _service_events = _build_vpn_service(wireguard_manager=concurrent_wg)
    failures: list[str] = []

    def _connect(agent_id: str) -> None:
        try:
            service.connect(agent_id=agent_id, operator_id=None, endpoint_host="engine.local")
        except Exception as exc:
            failures.append(str(exc))

    threads = [
        threading.Thread(target=_connect, args=("agent-1",)),
        threading.Thread(target=_connect, args=("agent-2",)),
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert failures == []
    assert concurrent_wg.max_concurrent_starts == 1


def test_vpn_service_reuses_persisted_virtual_ip_leases_across_restarts(
    engine_harness: EngineTestHarness,
) -> None:
    db_factory = lambda: sqlite3.connect(str(engine_harness.db_path))
    service, _wg, _socketio, _service_events = _build_vpn_service(db_conn_factory=db_factory)

    first = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.disconnect("agent-1", reason="operator_stop", force=True)

    restarted_service, _wg2, _socketio2, _service_events2 = _build_vpn_service(db_conn_factory=db_factory)
    second = restarted_service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    assert second["virtual_ip"] == first["virtual_ip"]


def test_vpn_service_reuses_persisted_client_keys_across_restarts(
    engine_harness: EngineTestHarness,
) -> None:
    db_factory = lambda: sqlite3.connect(str(engine_harness.db_path))
    service, _wg, _socketio, _service_events = _build_vpn_service(db_conn_factory=db_factory)

    first = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    service.disconnect("agent-1", reason="operator_stop", force=True)

    restarted_service, _wg2, _socketio2, _service_events2 = _build_vpn_service(db_conn_factory=db_factory)
    second = restarted_service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    assert second["client_public_key"] == first["client_public_key"]
    assert second["client_private_key"] == first["client_private_key"]


def test_vpn_service_does_not_reassign_listener_ip_from_evicted_session() -> None:
    service, _wg, _socketio, _service_events = _build_vpn_service()

    first = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    second = service.connect(agent_id="agent-2", operator_id=None, endpoint_host="engine.local")

    with service._lock:
        removed = service._sessions_by_agent.pop("agent-1")
        service._sessions_by_tunnel.pop(removed.tunnel_id, None)

    third = service.connect(agent_id="agent-3", operator_id=None, endpoint_host="engine.local")

    assert third["virtual_ip"] != first["virtual_ip"]
    assert third["virtual_ip"] != second["virtual_ip"]


def test_vpn_service_reuses_listener_managed_ip_for_same_agent_after_session_loss() -> None:
    service, _wg, _socketio, _service_events = _build_vpn_service()

    first = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    with service._lock:
        removed = service._sessions_by_agent.pop("agent-1")
        service._sessions_by_tunnel.pop(removed.tunnel_id, None)

    second = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    assert second["virtual_ip"] == first["virtual_ip"]


def test_vpn_service_suppresses_device_activity_history(engine_harness: EngineTestHarness) -> None:
    service, _wg, socketio, _service_events = _build_vpn_service(
        db_conn_factory=lambda: sqlite3.connect(str(engine_harness.db_path))
    )

    service.connect(agent_id="test-device-agent", operator_id=None, endpoint_host="engine.local")
    service.disconnect("test-device-agent", reason="operator_stop", force=True)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT COUNT(*)
              FROM activity_history
             WHERE hostname = ?
               AND script_type = ?
            """,
            ("test-device", "vpn_tunnel"),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert int(row[0] or 0) == 0
    assert all(event_name != "device_activity_changed" for event_name, _payload, _namespace in socketio.emits)


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
    assert payload["status"] == "recovering"
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


def test_resolve_requested_agent_id_prefers_live_system_socket_for_stale_binding(
    engine_harness: EngineTestHarness,
) -> None:
    valid_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    stale_agent_id = "test-device_08FB4B0D-FE6B-4D41-B09B-7947851BFD7A_SYSTEM"
    live_agent_id = "test-device_3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9_SYSTEM"
    _set_test_device_guid(engine_harness, valid_guid)
    _set_test_device_agent_id(engine_harness, stale_agent_id)
    adapters = SimpleNamespace(
        db_conn_factory=lambda: sqlite3.connect(str(engine_harness.db_path)),
        context=SimpleNamespace(
            agent_socket_registry=_FakeAgentSocketRegistry(
                registered_agent_ids={live_agent_id},
                host_mode_routes={("test-device", "system"): live_agent_id},
            )
        ),
    )

    assert tunnel_api._resolve_requested_agent_id(adapters, valid_guid) == live_agent_id
    assert tunnel_api._resolve_requested_agent_id(adapters, stale_agent_id) == live_agent_id


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
    expected_host = getattr(engine_harness.context, "public_wireguard_host", None) or "localhost"
    assert fake_service.connect_calls == [("test-device-agent", "admin", expected_host)]


def test_agent_vpn_ensure_repairs_stale_agent_binding(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    valid_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    stale_agent_id = "test-device_08FB4B0D-FE6B-4D41-B09B-7947851BFD7A_SYSTEM"
    live_agent_id = "test-device_3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9_SYSTEM"
    _set_test_device_guid(engine_harness, valid_guid)
    _set_test_device_agent_id(engine_harness, stale_agent_id)

    fake_service = _FakeTunnelApiService(status_payload=None, active_payloads=[])
    monkeypatch.setattr(device_routes, "_get_tunnel_service", lambda _adapters: fake_service)

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/vpn/ensure",
        headers=_device_headers_for_guid(valid_guid),
        json={"agent_id": live_agent_id},
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["agent_id"] == live_agent_id
    expected_host = getattr(engine_harness.context, "public_wireguard_host", None) or "localhost"
    assert fake_service.connect_calls == [(live_agent_id, None, expected_host)]
    assert fake_service.connect_mark_activity_calls == [False]

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT agent_id FROM devices WHERE hostname = ?", ("test-device",))
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert row[0] == live_agent_id


def test_agent_vnc_ensure_creates_passive_tunnel_when_missing(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    valid_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    live_agent_id = "test-device_3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9_SYSTEM"
    _set_test_device_guid(engine_harness, valid_guid)
    _set_test_device_agent_id(engine_harness, live_agent_id)

    fake_service = _FakeTunnelApiService(status_payload=None, active_payloads=[])
    monkeypatch.setattr(device_routes, "_get_tunnel_service", lambda _adapters: fake_service)

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/agent/vnc/ensure",
        headers=_device_headers_for_guid(valid_guid),
        json={
            "agent_id": live_agent_id,
            "controller_password": "bootpass",
            "credential_revision": 42,
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
                    "primary": True,
                }
            ],
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    expected_host = getattr(engine_harness.context, "public_wireguard_host", None) or "localhost"
    assert fake_service.connect_calls == [(live_agent_id, None, expected_host)]
    assert fake_service.connect_mark_activity_calls == [False]
    assert payload["display_topology"][0]["label"] == "1"
    assert payload["display_virtual_bounds"]["width"] == 1920


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
