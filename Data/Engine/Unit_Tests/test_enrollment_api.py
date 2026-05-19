# ======================================================
# Data\Engine\Unit_Tests\test_enrollment_api.py
# Description: Covers device enrollment request and poll flows including cryptographic proof handling.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64
import os
from Data.Engine.db import dbapi as sqlite3
from datetime import datetime, timezone

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519
from flask.testing import FlaskClient

from Data.Engine.crypto import keys as crypto_keys

from .conftest import EngineTestHarness
from .support.engine import admin_client


def _now() -> datetime:
    return datetime.now(tz=timezone.utc)


def _iso(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).isoformat()


def _seed_install_code(db_path: os.PathLike[str], code: str, site_id: int = 1) -> str:
    baseline = _now()
    with sqlite3.connect(str(db_path)) as conn:
        columns = {row[1] for row in conn.execute("PRAGMA table_info(sites)")}
        if "enrollment_code" not in columns:
            conn.execute("ALTER TABLE sites ADD COLUMN enrollment_code TEXT")
        conn.execute(
            """
            INSERT OR IGNORE INTO sites (id, name, description, created_at, enrollment_code)
            VALUES (?, ?, ?, ?, ?)
            """,
            (site_id, f"Test Site {site_id}", "Seeded site", int(baseline.timestamp()), code),
        )
        conn.execute(
            """
            UPDATE sites
               SET enrollment_code = ?
             WHERE id = ?
            """,
            (code, site_id),
        )
        conn.commit()
    return code


def _generate_agent_material() -> tuple[ed25519.Ed25519PrivateKey, bytes, str]:
    private_key = ed25519.Ed25519PrivateKey.generate()
    public_der = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    public_b64 = base64.b64encode(public_der).decode("ascii")
    return private_key, public_der, public_b64


def test_enrollment_request_creates_pending_approval(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    install_code = "INSTALL-CODE-001"
    _seed_install_code(harness.db_path, install_code)
    private_key, public_der, public_b64 = _generate_agent_material()
    client_nonce_bytes = os.urandom(32)
    client_nonce_b64 = base64.b64encode(client_nonce_bytes).decode("ascii")

    response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-node-01",
            "enrollment_code": install_code,
            "agent_pubkey": public_b64,
            "client_nonce": client_nonce_b64,
        },
        headers={"X-Borealis-Agent-Context": "interactive"},
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "pending"
    approval_reference = payload["approval_reference"]

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT hostname_claimed, ssl_key_fingerprint_claimed, client_nonce, status, enrollment_code, site_id
              FROM device_approvals
             WHERE approval_reference = ?
            """,
            (approval_reference,),
        )
        row = cur.fetchone()

    assert row is not None
    hostname_claimed, fingerprint, stored_client_nonce, status, stored_code, stored_site_id = row
    assert hostname_claimed == "agent-node-01"
    assert stored_client_nonce == client_nonce_b64
    assert status == "pending"
    assert stored_code == install_code
    assert stored_site_id == 1
    expected_fingerprint = crypto_keys.fingerprint_from_spki_der(public_der)
    assert fingerprint == expected_fingerprint


def test_enrollment_request_supersedes_pending_nonce_without_mismatch(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    install_code = "INSTALL-CODE-RACE"
    _seed_install_code(harness.db_path, install_code)
    private_key, _public_der, public_b64 = _generate_agent_material()
    first_nonce = os.urandom(32)
    second_nonce = os.urandom(32)
    first_nonce_b64 = base64.b64encode(first_nonce).decode("ascii")
    second_nonce_b64 = base64.b64encode(second_nonce).decode("ascii")

    first_response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-node-race",
            "enrollment_code": install_code,
            "agent_pubkey": public_b64,
            "client_nonce": first_nonce_b64,
        },
    )
    assert first_response.status_code == 200
    first_payload = first_response.get_json()
    first_reference = first_payload["approval_reference"]
    first_server_nonce = first_payload["server_nonce"]

    second_response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-node-race",
            "enrollment_code": install_code,
            "agent_pubkey": public_b64,
            "client_nonce": second_nonce_b64,
        },
    )
    assert second_response.status_code == 200
    second_payload = second_response.get_json()
    assert second_payload["approval_reference"] != first_reference

    message = base64.b64decode(first_server_nonce, validate=True) + first_reference.encode("utf-8") + first_nonce
    proof_sig_b64 = base64.b64encode(private_key.sign(message)).decode("ascii")
    poll_response = client.post(
        "/api/agent/enroll/poll",
        json={
            "approval_reference": first_reference,
            "client_nonce": first_nonce_b64,
            "proof_sig": proof_sig_b64,
        },
    )

    assert poll_response.status_code == 200
    assert poll_response.get_json()["status"] == "expired"

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT status FROM device_approvals WHERE approval_reference = ?",
            (first_reference,),
        )
        first_status = cur.fetchone()[0]
        cur.execute(
            "SELECT client_nonce, status FROM device_approvals WHERE approval_reference = ?",
            (second_payload["approval_reference"],),
        )
        second_row = cur.fetchone()

    assert first_status == "expired"
    assert second_row == (second_nonce_b64, "pending")


def test_invalid_enrollment_code_surfaces_wrong_code_status(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    _private_key, public_der, public_b64 = _generate_agent_material()
    client_nonce_b64 = base64.b64encode(os.urandom(32)).decode("ascii")

    response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-wrong-code-01",
            "enrollment_code": "WRONG-CODE-1234",
            "agent_pubkey": public_b64,
            "client_nonce": client_nonce_b64,
        },
        headers={"X-Borealis-Agent-Context": "system"},
    )

    assert response.status_code == 400
    assert response.get_json()["error"] == "invalid_enrollment_code"
    expected_fingerprint = crypto_keys.fingerprint_from_spki_der(public_der)

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT hostname_claimed, ssl_key_fingerprint_claimed, enrollment_code_mask, attempt_count, last_error
              FROM enrollment_code_failures
             WHERE ssl_key_fingerprint_claimed = ?
            """,
            (expected_fingerprint,),
        )
        row = cur.fetchone()

    assert row is not None
    assert row[0] == "agent-wrong-code-01"
    assert row[1] == expected_fingerprint
    assert row[2] == "WRO***234"
    assert row[3] == 1
    assert row[4] == "invalid_enrollment_code"

    admin = admin_client(harness)
    list_resp = admin.get("/api/admin/device-approvals?status=wrong_code")
    assert list_resp.status_code == 200
    wrong_code_records = list_resp.get_json()["approvals"]
    assert len(wrong_code_records) == 1
    assert wrong_code_records[0]["status"] == "wrong_code"
    assert wrong_code_records[0]["hostname_claimed"] == "agent-wrong-code-01"
    assert wrong_code_records[0]["wrong_code_attempt_count"] == 1

    install_code = "VALID-CODE-001"
    _seed_install_code(harness.db_path, install_code)
    valid_response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-wrong-code-01",
            "enrollment_code": install_code,
            "agent_pubkey": public_b64,
            "client_nonce": base64.b64encode(os.urandom(32)).decode("ascii"),
        },
        headers={"X-Borealis-Agent-Context": "system"},
    )

    assert valid_response.status_code == 200
    list_resp = admin.get("/api/admin/device-approvals?status=wrong_code")
    assert list_resp.status_code == 200
    assert list_resp.get_json()["approvals"] == []


def test_enrollment_poll_finalizes_when_approved(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    install_code = "INSTALL-CODE-002"
    _seed_install_code(harness.db_path, install_code)
    private_key, public_der, public_b64 = _generate_agent_material()
    client_nonce_bytes = os.urandom(32)
    client_nonce_b64 = base64.b64encode(client_nonce_bytes).decode("ascii")

    request_response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-node-02",
            "enrollment_code": install_code,
            "agent_pubkey": public_b64,
            "client_nonce": client_nonce_b64,
        },
        headers={"X-Borealis-Agent-Context": "system"},
    )
    assert request_response.status_code == 200
    request_payload = request_response.get_json()
    approval_reference = request_payload["approval_reference"]
    server_nonce_b64 = request_payload["server_nonce"]

    approved_at = _iso(_now())
    with sqlite3.connect(str(harness.db_path)) as conn:
        conn.execute(
            """
            UPDATE device_approvals
               SET status = 'approved',
                   updated_at = ?,
                   approved_by_user_id = 'operator'
             WHERE approval_reference = ?
            """,
            (approved_at, approval_reference),
        )
        conn.commit()

    message = base64.b64decode(server_nonce_b64, validate=True) + approval_reference.encode("utf-8") + client_nonce_bytes
    proof_sig = private_key.sign(message)
    proof_sig_b64 = base64.b64encode(proof_sig).decode("ascii")

    poll_response = client.post(
        "/api/agent/enroll/poll",
        json={
            "approval_reference": approval_reference,
            "client_nonce": client_nonce_b64,
            "proof_sig": proof_sig_b64,
        },
    )

    assert poll_response.status_code == 200
    poll_payload = poll_response.get_json()
    assert poll_payload["status"] == "approved"
    assert poll_payload["token_type"] == "Bearer"

    final_guid = poll_payload["guid"]
    assert isinstance(final_guid, str) and len(final_guid) == 36

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT guid, status, site_id FROM device_approvals WHERE approval_reference = ?",
            (approval_reference,),
        )
        approval_row = cur.fetchone()
        cur.execute(
            "SELECT hostname, ssl_key_fingerprint, token_version FROM devices WHERE guid = ?",
            (final_guid,),
        )
        device_row = cur.fetchone()
        cur.execute(
            "SELECT site_id FROM device_sites WHERE device_hostname = ?",
            (device_row[0] if device_row else None,),
        )
        site_row = cur.fetchone()
        cur.execute(
            "SELECT COUNT(*) FROM refresh_tokens WHERE guid = ?",
            (final_guid,),
        )
        refresh_count = cur.fetchone()[0]
        cur.execute(
            "SELECT COUNT(*) FROM device_keys WHERE guid = ?",
            (final_guid,),
        )
        key_count = cur.fetchone()[0]

    assert approval_row is not None
    approval_guid, approval_status, approval_site_id = approval_row
    assert approval_status == "completed"
    assert approval_guid == final_guid
    assert approval_site_id == 1

    assert device_row is not None
    hostname, fingerprint, token_version = device_row
    assert hostname == "agent-node-02"
    assert fingerprint == crypto_keys.fingerprint_from_spki_der(public_der)
    assert token_version >= 1
    assert site_row is not None
    assert site_row[0] == 1

    assert refresh_count == 1
    assert key_count == 1


def test_enrollment_poll_reuses_purged_guid_with_bumped_token_version(
    engine_harness: EngineTestHarness,
) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    install_code = "INSTALL-CODE-003"
    purged_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    required_token_version = 4
    _seed_install_code(harness.db_path, install_code)
    private_key, _public_der, public_b64 = _generate_agent_material()
    client_nonce_bytes = os.urandom(32)
    client_nonce_b64 = base64.b64encode(client_nonce_bytes).decode("ascii")

    request_response = client.post(
        "/api/agent/enroll/request",
        json={
            "hostname": "agent-node-03",
            "enrollment_code": install_code,
            "agent_pubkey": public_b64,
            "client_nonce": client_nonce_b64,
        },
        headers={"X-Borealis-Agent-Context": "system"},
    )
    assert request_response.status_code == 200
    request_payload = request_response.get_json()
    approval_reference = request_payload["approval_reference"]
    server_nonce_b64 = request_payload["server_nonce"]

    approved_at = _iso(_now())
    with sqlite3.connect(str(harness.db_path)) as conn:
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS device_purge_barriers (
                guid TEXT PRIMARY KEY,
                required_token_version INTEGER NOT NULL,
                purged_at TEXT NOT NULL,
                purged_by TEXT,
                last_hostname TEXT,
                last_agent_id TEXT
            )
            """
        )
        conn.execute(
            """
            INSERT OR REPLACE INTO device_purge_barriers (
                guid, required_token_version, purged_at, purged_by, last_hostname, last_agent_id
            )
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (
                purged_guid,
                required_token_version,
                approved_at,
                "admin",
                "agent-node-03",
                "agent-node-03-service",
            ),
        )
        conn.execute(
            """
            UPDATE device_approvals
               SET guid = ?,
                   status = 'approved',
                   updated_at = ?,
                   approved_by_user_id = 'operator'
             WHERE approval_reference = ?
            """,
            (purged_guid, approved_at, approval_reference),
        )
        conn.commit()

    message = base64.b64decode(server_nonce_b64, validate=True) + approval_reference.encode("utf-8") + client_nonce_bytes
    proof_sig = private_key.sign(message)
    proof_sig_b64 = base64.b64encode(proof_sig).decode("ascii")

    poll_response = client.post(
        "/api/agent/enroll/poll",
        json={
            "approval_reference": approval_reference,
            "client_nonce": client_nonce_b64,
            "proof_sig": proof_sig_b64,
        },
    )

    assert poll_response.status_code == 200
    poll_payload = poll_response.get_json()
    assert poll_payload["status"] == "approved"
    assert poll_payload["guid"] == purged_guid

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT hostname, token_version, status FROM devices WHERE guid = ?",
            (purged_guid,),
        )
        device_row = cur.fetchone()
        cur.execute(
            "SELECT COUNT(*) FROM refresh_tokens WHERE guid = ?",
            (purged_guid,),
        )
        refresh_count = cur.fetchone()[0]
        cur.execute(
            "SELECT COUNT(*) FROM device_purge_barriers WHERE guid = ?",
            (purged_guid,),
        )
        barrier_count = cur.fetchone()[0]

    assert device_row is not None
    hostname, token_version, status = device_row
    assert hostname == "agent-node-03"
    assert token_version == required_token_version
    assert status == "active"
    assert refresh_count == 1
    assert barrier_count == 0
