
from __future__ import annotations

from typing import Any

import pytest
from Data.Engine.services.API.devices import management as device_management

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


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

    def fake_get(url: str, headers: Any, timeout: int) -> DummyResponse:
        calls["count"] += 1
        if calls["count"] == 1:
            return DummyResponse(200, {"commit": {"sha": "abc123"}})
        raise device_management.requests.RequestException("network error")

    monkeypatch.setattr(device_management.requests, "get", fake_get)

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
