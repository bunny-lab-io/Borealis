# ======================================================
# Data\Engine\Unit_Tests\test_reverse_tunnel.py
# Description: Validates reverse tunnel lease API basics (allocation, token contents, and domain limit).
#
# API Endpoints (if applicable):
# - POST /api/tunnel/request
# ======================================================

from __future__ import annotations

import base64
import json

import pytest

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _decode_token_segment(token: str) -> dict:
    """Decode the unsigned payload segment from the tunnel token."""
    if not token:
        return {}
    segment = token.split(".")[0]
    padding = "=" * (-len(segment) % 4)
    raw = base64.urlsafe_b64decode(segment + padding)
    try:
        return json.loads(raw.decode("utf-8"))
    except Exception:
        return {}


@pytest.mark.parametrize("agent_id", ["test-device-agent"])
def test_tunnel_request_happy_path(engine_harness: EngineTestHarness, agent_id: str) -> None:
    client = _client_with_admin_session(engine_harness)
    resp = client.post(
        "/api/tunnel/request",
        json={"agent_id": agent_id, "protocol": "ps", "domain": "ps"},
    )
    assert resp.status_code == 200
    payload = resp.get_json()
    assert payload["agent_id"] == agent_id
    assert payload["protocol"] == "ps"
    assert payload["domain"] == "ps"
    assert isinstance(payload["port"], int) and payload["port"] >= 30000
    assert payload.get("token")

    claims = _decode_token_segment(payload["token"])
    assert claims.get("agent_id") == agent_id
    assert claims.get("protocol") == "ps"
    assert claims.get("domain") == "ps"
    assert claims.get("tunnel_id") == payload["tunnel_id"]
    assert claims.get("assigned_port") == payload["port"]


def test_tunnel_request_domain_limit(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    first = client.post(
        "/api/tunnel/request",
        json={"agent_id": "test-device-agent", "protocol": "ps", "domain": "ps"},
    )
    assert first.status_code == 200

    second = client.post(
        "/api/tunnel/request",
        json={"agent_id": "test-device-agent", "protocol": "ps", "domain": "ps"},
    )
    assert second.status_code == 409
    data = second.get_json()
    assert data.get("error") == "domain_limit"


def test_tunnel_request_includes_timeouts(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    resp = client.post(
        "/api/tunnel/request",
        json={"agent_id": "test-device-agent", "protocol": "ps", "domain": "ps"},
    )
    assert resp.status_code == 200
    payload = resp.get_json()
    assert payload.get("idle_seconds") and payload["idle_seconds"] > 0
    assert payload.get("grace_seconds") and payload["grace_seconds"] > 0
    assert payload.get("expires_at") and int(payload["expires_at"]) > 0
