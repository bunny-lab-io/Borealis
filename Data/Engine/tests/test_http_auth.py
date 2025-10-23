import hashlib

import pytest

pytest.importorskip("flask")
pytest.importorskip("jwt")

def _login(client) -> dict:
    payload = {
        "username": "admin",
        "password_sha512": hashlib.sha512("Password".encode()).hexdigest(),
    }
    resp = client.post("/api/auth/login", json=payload)
    assert resp.status_code == 200
    data = resp.get_json()
    assert isinstance(data, dict)
    return data


def test_auth_me_returns_session_user(prepared_app):
    client = prepared_app.test_client()

    _login(client)
    resp = client.get("/api/auth/me")
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {
        "username": "admin",
        "display_name": "Administrator",
        "role": "Admin",
    }


def test_auth_me_uses_token_when_session_missing(prepared_app):
    client = prepared_app.test_client()
    login_data = _login(client)
    token = login_data.get("token")
    assert token

    # New client without session
    other_client = prepared_app.test_client()
    other_client.set_cookie("borealis_auth", token)

    resp = other_client.get("/api/auth/me")
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {
        "username": "admin",
        "display_name": "Administrator",
        "role": "Admin",
    }


def test_auth_me_requires_authentication(prepared_app):
    client = prepared_app.test_client()
    resp = client.get("/api/auth/me")
    assert resp.status_code == 401
    body = resp.get_json()
    assert body == {"error": "not_authenticated"}
