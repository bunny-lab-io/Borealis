# ======================================================
# Data\Engine\Unit_Tests\test_access_management_api.py
# Description: Exercises access-management endpoints covering GitHub API token administration.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from typing import Any, Dict

import pytest
from Data.Engine.db import dbapi as sqlite3

from Data.Engine.integrations import github as github_integration

from .conftest import EngineTestHarness


def _admin_client(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _user_client(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "operator"
        sess["role"] = "User"
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


def test_credentials_admin_crud_round_trip(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)

    create_response = client.post(
        "/api/credentials",
        json={
            "name": "Lab SSH",
            "description": "Primary SSH credential",
            "site_id": 1,
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "super-secret",
            "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
            "private_key_passphrase": "key-passphrase",
            "become_method": "sudo",
            "become_username": "root",
            "become_password": "sudo-secret",
        },
    )
    assert create_response.status_code == 200
    created = create_response.get_json()["credential"]
    assert created["name"] == "Lab SSH"
    assert created["site_name"] == "Main Lab"
    assert created["connection_type"] == "ssh"
    assert created["has_password"] is True
    assert created["has_private_key"] is True
    assert created["has_private_key_passphrase"] is True
    assert created["has_become_password"] is True

    list_response = client.get("/api/credentials")
    assert list_response.status_code == 200
    listed = list_response.get_json()["credentials"]
    assert len(listed) == 1
    assert listed[0]["name"] == "Lab SSH"
    assert listed[0]["site_name"] == "Main Lab"

    detail_response = client.get(f"/api/credentials/{created['id']}")
    assert detail_response.status_code == 200
    detail = detail_response.get_json()["credential"]
    assert detail["username"] == "automation"
    assert detail["has_private_key"] is True

    update_response = client.put(
        f"/api/credentials/{created['id']}",
        json={
            "description": "Updated description",
            "clear_password": True,
            "clear_private_key": True,
            "clear_private_key_passphrase": True,
            "clear_become_password": True,
        },
    )
    assert update_response.status_code == 200
    updated = update_response.get_json()["credential"]
    assert updated["description"] == "Updated description"
    assert updated["has_password"] is False
    assert updated["has_private_key"] is False
    assert updated["has_private_key_passphrase"] is False
    assert updated["has_become_password"] is False

    delete_response = client.delete(f"/api/credentials/{created['id']}")
    assert delete_response.status_code == 200

    final_list = client.get("/api/credentials")
    assert final_list.status_code == 200
    assert final_list.get_json()["credentials"] == []


def test_credentials_listing_requires_login_and_writes_require_admin(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO credentials(
                name,
                description,
                site_id,
                credential_type,
                connection_type,
                username,
                password_encrypted,
                metadata_json,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "WinRM Ops",
                "WinRM credential",
                1,
                "machine",
                "winrm",
                "corp\\ops",
                b"pw",
                '{"winrm_transport":"ntlm"}',
                1_700_000_000,
                1_700_000_000,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    anon_client = engine_harness.app.test_client()
    anon_response = anon_client.get("/api/credentials")
    assert anon_response.status_code == 401

    user_client = _user_client(engine_harness)
    list_response = user_client.get("/api/credentials")
    assert list_response.status_code == 200
    listed = list_response.get_json()["credentials"]
    assert len(listed) == 1
    assert listed[0]["name"] == "WinRM Ops"
    assert listed[0]["metadata"]["winrm_transport"] == "ntlm"

    create_response = user_client.post(
        "/api/credentials",
        json={
            "name": "Should Fail",
            "connection_type": "ssh",
        },
    )
    assert create_response.status_code == 403
