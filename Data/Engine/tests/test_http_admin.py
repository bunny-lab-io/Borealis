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


def test_device_approval_requires_resolution(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _login(client)

    now = datetime.now(tz=timezone.utc)
    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()

    cur.execute(
        """
        INSERT INTO devices (
            guid,
            hostname,
            created_at,
            last_seen,
            ssl_key_fingerprint,
            status
        ) VALUES (?, ?, ?, ?, ?, 'active')
        """,
        (
            "33333333-3333-3333-3333-333333333333",
            "conflict-host",
            int(now.timestamp()),
            int(now.timestamp()),
            "existingfp",
        ),
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
            "approval-conflict",
            "REF-CONFLICT",
            None,
            "conflict-host",
            "newfinger",
            "code-conflict",
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

    resp = client.post("/api/admin/device-approvals/approval-conflict/approve", json={})
    assert resp.status_code == 409
    assert resp.get_json().get("error") == "conflict_resolution_required"

    resp = client.post(
        "/api/admin/device-approvals/approval-conflict/approve",
        json={"conflict_resolution": "overwrite"},
    )
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {"status": "approved", "conflict_resolution": "overwrite"}

    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()
    cur.execute(
        "SELECT status, guid, approved_by_user_id FROM device_approvals WHERE id = ?",
        ("approval-conflict",),
    )
    row = cur.fetchone()
    conn.close()
    assert row[0] == "approved"
    assert row[1] == "33333333-3333-3333-3333-333333333333"
    assert row[2]

    resp = client.post(
        "/api/admin/device-approvals/approval-conflict/approve",
        json={"conflict_resolution": "overwrite"},
    )
    assert resp.status_code == 409
    assert resp.get_json().get("error") == "approval_not_pending"


def test_device_approval_auto_merge(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _login(client)

    now = datetime.now(tz=timezone.utc)
    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()

    cur.execute(
        """
        INSERT INTO devices (
            guid,
            hostname,
            created_at,
            last_seen,
            ssl_key_fingerprint,
            status
        ) VALUES (?, ?, ?, ?, ?, 'active')
        """,
        (
            "44444444-4444-4444-4444-444444444444",
            "merge-host",
            int(now.timestamp()),
            int(now.timestamp()),
            "deadbeef",
        ),
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
            "approval-merge",
            "REF-MERGE",
            None,
            "merge-host",
            "deadbeef",
            "code-merge",
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

    resp = client.post("/api/admin/device-approvals/approval-merge/approve", json={})
    assert resp.status_code == 200
    body = resp.get_json()
    assert body.get("status") == "approved"
    assert body.get("conflict_resolution") == "auto_merge_fingerprint"

    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()
    cur.execute(
        "SELECT guid, status FROM device_approvals WHERE id = ?",
        ("approval-merge",),
    )
    row = cur.fetchone()
    conn.close()
    assert row[1] == "approved"
    assert row[0] == "44444444-4444-4444-4444-444444444444"


def test_device_approval_deny(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _login(client)

    now = datetime.now(tz=timezone.utc)
    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()

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
            "approval-deny",
            "REF-DENY",
            None,
            "deny-host",
            "cafebabe",
            "code-deny",
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

    resp = client.post("/api/admin/device-approvals/approval-deny/deny", json={})
    assert resp.status_code == 200
    assert resp.get_json() == {"status": "denied"}

    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()
    cur.execute(
        "SELECT status FROM device_approvals WHERE id = ?",
        ("approval-deny",),
    )
    row = cur.fetchone()
    conn.close()
    assert row[0] == "denied"
