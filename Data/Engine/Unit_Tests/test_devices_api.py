# ======================================================
# Data\Engine\Unit_Tests\test_devices_api.py
# Description: Exercises device management endpoints covering lists, views, site workflows, and approvals.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from typing import Any

import pytest
from Data.Engine.auth import jwt_service as jwt_service_module
from Data.Engine.integrations import github as github_integration
from Data.Engine.services.API.devices import management as device_management

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


def test_site_enrollment_code_rotation(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    sites_resp = client.get("/api/sites")
    assert sites_resp.status_code == 200
    sites = sites_resp.get_json()["sites"]
    assert sites and sites[0]["enrollment_code"]
    site_id = sites[0]["id"]
    original_code = sites[0]["enrollment_code"]

    rotate_resp = client.post("/api/sites/rotate_code", json={"site_id": site_id})
    assert rotate_resp.status_code == 200
    rotated = rotate_resp.get_json()
    assert rotated["id"] == site_id
    assert rotated["enrollment_code"] and rotated["enrollment_code"] != original_code


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
