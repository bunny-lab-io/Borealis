# ======================================================
# Data\Engine\Unit_Tests\test_devices_api.py
# Description: Exercises device management endpoints covering lists, views, site workflows, and approvals.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import sqlite3
from typing import Any, Dict

import pytest
from Data.Engine.auth import jwt_service
from Data.Engine.integrations import github as github_integration
from Data.Engine.services.API.devices import management as device_management

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _device_auth_headers() -> Dict[str, str]:
    service = jwt_service.load_service()
    token = service.issue_access_token("GUID-TEST-0001", "FF:FF:FF", 1)
    return {"Authorization": f"Bearer {token}"}


def test_list_devices(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    response = client.get("/api/devices")
    assert response.status_code == 200
    payload = response.get_json()
    assert isinstance(payload, dict)
    devices = payload.get("devices")
    assert isinstance(devices, list) and devices
    device = devices[0]
    assert device["hostname"] == "test-device"
    assert "summary" in device and isinstance(device["summary"], dict)


def test_device_details(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
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

    client = engine_harness.app.test_client()
    first = client.get("/api/repo/current_hash?repo=test/test&branch=main")
    assert first.status_code == 200
    assert first.get_json()["sha"] == "abc123"
    second = client.get("/api/repo/current_hash?repo=test/test&branch=main")
    assert second.status_code == 200
    second_payload = second.get_json()
    assert second_payload["sha"] == "abc123"
    assert second_payload["cached"] is True or second_payload["source"].startswith("cache")
    assert calls["count"] == 1


def test_agent_hash_list_permissions(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    forbidden = client.get("/api/agent/hash_list", environ_base={"REMOTE_ADDR": "192.0.2.10"})
    assert forbidden.status_code == 403
    allowed = client.get("/api/agent/hash_list", environ_base={"REMOTE_ADDR": "127.0.0.1"})
    assert allowed.status_code == 200
    agents = allowed.get_json()["agents"]
    assert agents and agents[0]["hostname"] == "test-device"


def test_agent_hash_update_sets_value(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    headers = _device_auth_headers()
    response = client.post(
        "/api/agent/hash",
        json={"agent_hash": "hash-999", "agent_id": "test-device-agent"},
        headers=headers,
    )
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["agent_hash"] == "hash-999"
    assert payload["agent_guid"] == "GUID-TEST-0001"

    with sqlite3.connect(str(engine_harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute("SELECT agent_hash FROM devices WHERE guid = ?", ("GUID-TEST-0001",))
        row = cur.fetchone()
    assert row is not None
    assert row[0] == "hash-999"


def test_agent_hash_get_returns_record(engine_harness: EngineTestHarness) -> None:
    with sqlite3.connect(str(engine_harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            "UPDATE devices SET agent_hash = ?, agent_id = ? WHERE guid = ?",
            ("hash-555", "test-device-agent", "GUID-TEST-0001"),
        )
        conn.commit()

    client = engine_harness.app.test_client()
    headers = _device_auth_headers()
    response = client.get("/api/agent/hash", headers=headers)
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["agent_hash"] == "hash-555"
    assert payload["agent_guid"] == "GUID-TEST-0001"
    assert payload["agent_id"] == "test-device-agent"


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


def test_admin_enrollment_code_flow(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    create_resp = client.post(
        "/api/admin/enrollment-codes",
        json={"ttl_hours": 1, "max_uses": 2},
    )
    assert create_resp.status_code == 201
    code_id = create_resp.get_json()["id"]

    list_resp = client.get("/api/admin/enrollment-codes")
    codes = list_resp.get_json()["codes"]
    assert any(code["id"] == code_id for code in codes)

    delete_resp = client.delete(f"/api/admin/enrollment-codes/{code_id}")
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
