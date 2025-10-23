"""HTTP integration tests for operator account endpoints."""

from __future__ import annotations

import hashlib

from .test_http_auth import _login


def test_list_users_requires_authentication(prepared_app):
    client = prepared_app.test_client()
    resp = client.get("/api/users")
    assert resp.status_code == 401


def test_list_users_returns_accounts(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    resp = client.get("/api/users")
    assert resp.status_code == 200
    payload = resp.get_json()
    assert isinstance(payload, dict)
    assert "users" in payload
    assert any(user["username"] == "admin" for user in payload["users"])


def test_create_user_validates_payload(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    resp = client.post("/api/users", json={"username": "bob"})
    assert resp.status_code == 400

    payload = {
        "username": "bob",
        "password_sha512": hashlib.sha512(b"pw").hexdigest(),
        "role": "User",
    }
    resp = client.post("/api/users", json=payload)
    assert resp.status_code == 200

    # Duplicate username should conflict
    resp = client.post("/api/users", json=payload)
    assert resp.status_code == 409


def test_delete_user_handles_edge_cases(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    # cannot delete the only user
    resp = client.delete("/api/users/admin")
    assert resp.status_code == 400

    # create another user then delete them successfully
    payload = {
        "username": "alice",
        "password_sha512": hashlib.sha512(b"pw").hexdigest(),
        "role": "User",
    }
    client.post("/api/users", json=payload)

    resp = client.delete("/api/users/alice")
    assert resp.status_code == 200


def test_delete_user_prevents_self_deletion(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    payload = {
        "username": "charlie",
        "password_sha512": hashlib.sha512(b"pw").hexdigest(),
        "role": "User",
    }
    client.post("/api/users", json=payload)

    resp = client.delete("/api/users/admin")
    assert resp.status_code == 400


def test_change_role_updates_session(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    payload = {
        "username": "backup",
        "password_sha512": hashlib.sha512(b"pw").hexdigest(),
        "role": "Admin",
    }
    client.post("/api/users", json=payload)

    resp = client.post("/api/users/backup/role", json={"role": "User"})
    assert resp.status_code == 200

    resp = client.post("/api/users/admin/role", json={"role": "User"})
    assert resp.status_code == 400


def test_reset_password_requires_valid_hash(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    resp = client.post("/api/users/admin/reset_password", json={"password_sha512": "abc"})
    assert resp.status_code == 400

    resp = client.post(
        "/api/users/admin/reset_password",
        json={"password_sha512": hashlib.sha512(b"new").hexdigest()},
    )
    assert resp.status_code == 200


def test_update_mfa_returns_not_found_for_unknown_user(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    resp = client.post("/api/users/missing/mfa", json={"enabled": True})
    assert resp.status_code == 404
