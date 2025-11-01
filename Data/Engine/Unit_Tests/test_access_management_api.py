# ======================================================
# Data\Engine\Unit_Tests\test_access_management_api.py
# Description: Exercises access-management endpoints covering GitHub API token administration.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from typing import Any, Dict

import pytest

from Data.Engine.integrations import github as github_integration

from .conftest import EngineTestHarness


def _admin_client(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def test_github_token_get_without_value(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    response = client.get("/api/github/token")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["has_token"] is False
    assert payload["status"] == "missing"
    assert payload["token"] == ""


def test_github_token_update(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    class DummyResponse:
        def __init__(self, status_code: int, payload: Dict[str, Any]):
            self.status_code = status_code
            self._payload = payload
            self.headers = {"X-RateLimit-Limit": "5000"}
            self.text = ""

        def json(self) -> Dict[str, Any]:
            return self._payload

    def fake_get(url: str, headers: Any = None, timeout: Any = None) -> DummyResponse:
        return DummyResponse(200, {"commit": {"sha": "abc123"}})

    monkeypatch.setattr(github_integration.requests, "get", fake_get)

    client = _admin_client(engine_harness)
    response = client.post("/api/github/token", json={"token": "ghp_test"})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["has_token"] is True
    assert payload["valid"] is True
    assert payload["status"] == "ok"
    assert payload["token"] == "ghp_test"

    verify_response = client.get("/api/github/token")
    assert verify_response.status_code == 200
    verify_payload = verify_response.get_json()
    assert verify_payload["has_token"] is True
    assert verify_payload["token"] == "ghp_test"
