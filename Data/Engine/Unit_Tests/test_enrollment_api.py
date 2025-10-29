# ======================================================
# Data\Engine\Unit_Tests\test_enrollment_api.py
# Description: Covers device enrollment request and poll flows including cryptographic proof handling.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64
import os
import sqlite3
import uuid
from datetime import datetime, timedelta, timezone

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519
from flask.testing import FlaskClient

from Data.Engine.crypto import keys as crypto_keys

from .conftest import EngineTestHarness


def _now() -> datetime:
    return datetime.now(tz=timezone.utc)


def _iso(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).isoformat()


def _seed_install_code(db_path: os.PathLike[str], code: str) -> str:
    record_id = str(uuid.uuid4())
    expires_at = _iso(_now() + timedelta(days=1))
    with sqlite3.connect(str(db_path)) as conn:
        conn.execute(
            """
            INSERT INTO enrollment_install_codes (
                id, code, expires_at, used_at, used_by_guid, max_uses, use_count, last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (record_id, code, expires_at, None, None, 1, 0, None),
        )
        conn.commit()
    return record_id


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
    install_code_id = _seed_install_code(harness.db_path, install_code)
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
    assert payload["server_certificate"] == harness.bundle_contents
    approval_reference = payload["approval_reference"]

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT hostname_claimed, ssl_key_fingerprint_claimed, client_nonce, status, enrollment_code_id
              FROM device_approvals
             WHERE approval_reference = ?
            """,
            (approval_reference,),
        )
        row = cur.fetchone()

    assert row is not None
    hostname_claimed, fingerprint, stored_client_nonce, status, stored_code_id = row
    assert hostname_claimed == "agent-node-01"
    assert stored_client_nonce == client_nonce_b64
    assert status == "pending"
    assert stored_code_id == install_code_id
    expected_fingerprint = crypto_keys.fingerprint_from_spki_der(public_der)
    assert fingerprint == expected_fingerprint


def test_enrollment_poll_finalizes_when_approved(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    install_code = "INSTALL-CODE-002"
    install_code_id = _seed_install_code(harness.db_path, install_code)
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
    assert poll_payload["server_certificate"] == harness.bundle_contents

    final_guid = poll_payload["guid"]
    assert isinstance(final_guid, str) and len(final_guid) == 36

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT guid, status FROM device_approvals WHERE approval_reference = ?",
            (approval_reference,),
        )
        approval_row = cur.fetchone()
        cur.execute(
            "SELECT hostname, ssl_key_fingerprint, token_version FROM devices WHERE guid = ?",
            (final_guid,),
        )
        device_row = cur.fetchone()
        cur.execute(
            "SELECT COUNT(*) FROM refresh_tokens WHERE guid = ?",
            (final_guid,),
        )
        refresh_count = cur.fetchone()[0]
        cur.execute(
            "SELECT use_count, used_by_guid FROM enrollment_install_codes WHERE id = ?",
            (install_code_id,),
        )
        install_row = cur.fetchone()
        cur.execute(
            "SELECT COUNT(*) FROM device_keys WHERE guid = ?",
            (final_guid,),
        )
        key_count = cur.fetchone()[0]

    assert approval_row is not None
    approval_guid, approval_status = approval_row
    assert approval_status == "completed"
    assert approval_guid == final_guid

    assert device_row is not None
    hostname, fingerprint, token_version = device_row
    assert hostname == "agent-node-02"
    assert fingerprint == crypto_keys.fingerprint_from_spki_der(public_der)
    assert token_version >= 1

    assert refresh_count == 1
    assert install_row is not None
    use_count, used_by_guid = install_row
    assert use_count == 1
    assert used_by_guid == final_guid
    assert key_count == 1
