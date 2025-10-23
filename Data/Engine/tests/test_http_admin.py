import base64
import sqlite3
from datetime import datetime, timezone

from .test_http_auth import _login, prepared_app


def test_enrollment_codes_require_authentication(prepared_app):
    client = prepared_app.test_client()
    resp = client.get("/api/admin/enrollment-codes")
    assert resp.status_code == 401


def test_enrollment_code_workflow(prepared_app):
    client = prepared_app.test_client()
    _login(client)

    payload = {"ttl_hours": 3, "max_uses": 4}
    resp = client.post("/api/admin/enrollment-codes", json=payload)
    assert resp.status_code == 201
    created = resp.get_json()
    assert created["max_uses"] == 4
    assert created["status"] == "active"

    resp = client.get("/api/admin/enrollment-codes")
    assert resp.status_code == 200
    codes = resp.get_json().get("codes", [])
    assert any(code["id"] == created["id"] for code in codes)

    resp = client.delete(f"/api/admin/enrollment-codes/{created['id']}")
    assert resp.status_code == 200


def test_device_approvals_listing(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _login(client)

    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()

    now = datetime.now(tz=timezone.utc)
    cur.execute(
        "INSERT INTO sites (name, description, created_at) VALUES (?, ?, ?)",
        ("HQ", "Primary", int(now.timestamp())),
    )
    site_id = cur.lastrowid

    cur.execute(
        """
        INSERT INTO devices (guid, hostname, created_at, last_seen, ssl_key_fingerprint, status)
        VALUES (?, ?, ?, ?, ?, 'active')
        """,
        (
            "22222222-2222-2222-2222-222222222222",
            "approval-host",
            int(now.timestamp()),
            int(now.timestamp()),
            "deadbeef",
        ),
    )
    cur.execute(
        "INSERT INTO device_sites (device_hostname, site_id, assigned_at) VALUES (?, ?, ?)",
        ("approval-host", site_id, int(now.timestamp())),
    )

    now_iso = now.isoformat()
    cur.execute(
        """
        INSERT INTO device_approvals (
            id,
            approval_reference,
            guid,
            hostname_claimed,
            ssl_key_fingerprint_claimed,
            enrollment_code_id,
            status,
            client_nonce,
            server_nonce,
            created_at,
            updated_at,
            approved_by_user_id,
            agent_pubkey_der
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            "approval-http",
            "REFHTTP",
            None,
            "approval-host",
            "deadbeef",
            "code-http",
            "pending",
            base64.b64encode(b"client").decode(),
            base64.b64encode(b"server").decode(),
            now_iso,
            now_iso,
            None,
            b"pub",
        ),
    )
    conn.commit()
    conn.close()

    resp = client.get("/api/admin/device-approvals")
    assert resp.status_code == 200
    body = resp.get_json()
    approvals = body.get("approvals", [])
    assert any(a["id"] == "approval-http" for a in approvals)
    record = next(a for a in approvals if a["id"] == "approval-http")
    assert record.get("hostname_conflict", {}).get("fingerprint_match") is True

