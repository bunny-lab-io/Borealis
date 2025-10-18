import base64
import os
import pathlib
import sqlite3
import sys
import uuid

import pytest
try:  # pragma: no cover - optional dependency
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric import ed25519
    _CRYPTO_IMPORT_ERROR: Exception | None = None
except Exception as exc:  # pragma: no cover - dependency unavailable
    serialization = None  # type: ignore
    ed25519 = None  # type: ignore
    _CRYPTO_IMPORT_ERROR = exc
try:  # pragma: no cover - optional dependency
    from flask import Flask
    _FLASK_IMPORT_ERROR: Exception | None = None
except Exception as exc:  # pragma: no cover - dependency unavailable
    Flask = None  # type: ignore
    _FLASK_IMPORT_ERROR = exc

ROOT = pathlib.Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from Data.Server.Modules import db_migrations
from Data.Server.Modules.auth.rate_limit import SlidingWindowRateLimiter
from Data.Server.Modules.enrollment.nonce_store import NonceCache

if Flask is not None:  # pragma: no cover - dependency unavailable
    from Data.Server.Modules.enrollment import routes as enrollment_routes
else:  # pragma: no cover - dependency unavailable
    enrollment_routes = None  # type: ignore


class _DummyJWTService:
    def issue_access_token(self, guid: str, fingerprint: str, token_version: int, expires_in: int = 900, extra_claims=None):
        return f"token-{guid}"


class _DummySigner:
    def public_base64_spki(self) -> str:
        return ""


def _make_app(db_path: str, tls_path: str):
    if Flask is None or enrollment_routes is None:  # pragma: no cover - dependency unavailable
        pytest.skip(f"flask unavailable: {_FLASK_IMPORT_ERROR}")
    app = Flask(__name__)

    def _factory():
        conn = sqlite3.connect(db_path)
        conn.row_factory = sqlite3.Row
        return conn

    enrollment_routes.register(
        app,
        db_conn_factory=_factory,
        log=lambda channel, message: None,
        jwt_service=_DummyJWTService(),
        tls_bundle_path=tls_path,
        ip_rate_limiter=SlidingWindowRateLimiter(),
        fp_rate_limiter=SlidingWindowRateLimiter(),
        nonce_cache=NonceCache(ttl_seconds=30.0),
        script_signer=_DummySigner(),
    )
    return app, _factory


def _create_install_code(conn: sqlite3.Connection, code: str, *, max_uses: int = 2):
    cur = conn.cursor()
    record_id = str(uuid.uuid4())
    cur.execute(
        """
        INSERT INTO enrollment_install_codes (
            id, code, expires_at, created_by_user_id, max_uses, use_count
        )
        VALUES (?, ?, datetime('now', '+6 hours'), 'test-user', ?, 0)
        """,
        (record_id, code, max_uses),
    )
    conn.commit()
    return record_id


def _perform_enrollment_cycle(app, factory, code: str, private_key):
    client = app.test_client()
    public_der = private_key.public_key().public_bytes(
        serialization.Encoding.DER,
        serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    public_b64 = base64.b64encode(public_der).decode("ascii")
    client_nonce = os.urandom(32)
    payload = {
        "hostname": "unit-test-host",
        "enrollment_code": code,
        "agent_pubkey": public_b64,
        "client_nonce": base64.b64encode(client_nonce).decode("ascii"),
    }
    request_resp = client.post("/api/agent/enroll/request", json=payload)
    assert request_resp.status_code == 200
    request_data = request_resp.get_json()
    approval_reference = request_data["approval_reference"]

    with factory() as conn:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE device_approvals
               SET status = 'approved',
                   approved_by_user_id = 'tester'
             WHERE approval_reference = ?
            """,
            (approval_reference,),
        )
        cur.execute(
            """
            SELECT server_nonce, client_nonce
              FROM device_approvals
             WHERE approval_reference = ?
            """,
            (approval_reference,),
        )
        row = cur.fetchone()
        assert row is not None
        server_nonce_b64 = row["server_nonce"]

    server_nonce = base64.b64decode(server_nonce_b64)
    proof_message = server_nonce + approval_reference.encode("utf-8") + client_nonce
    proof_sig = private_key.sign(proof_message)
    poll_payload = {
        "approval_reference": approval_reference,
        "client_nonce": base64.b64encode(client_nonce).decode("ascii"),
        "proof_sig": base64.b64encode(proof_sig).decode("ascii"),
    }
    poll_resp = client.post("/api/agent/enroll/poll", json=poll_payload)
    assert poll_resp.status_code == 200
    return poll_resp.get_json()


@pytest.mark.parametrize("max_uses", [2])
@pytest.mark.skipif(ed25519 is None, reason=f"cryptography unavailable: {_CRYPTO_IMPORT_ERROR}")
@pytest.mark.skipif(Flask is None, reason=f"flask unavailable: {_FLASK_IMPORT_ERROR}")
def test_install_code_allows_multiple_and_reuse(tmp_path, max_uses):
    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    db_migrations.apply_all(conn)
    _create_install_code(conn, "TEST-CODE-1234", max_uses=max_uses)
    conn.close()

    tls_path = tmp_path / "tls.pem"
    tls_path.write_text("TEST CERT")

    app, factory = _make_app(str(db_path), str(tls_path))

    private_key = ed25519.Ed25519PrivateKey.generate()

    # First enrollment consumes one use but keeps the code active.
    first = _perform_enrollment_cycle(app, factory, "TEST-CODE-1234", private_key)
    assert first["status"] == "approved"

    with factory() as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT use_count, max_uses, used_at, last_used_at FROM enrollment_install_codes WHERE code = ?",
            ("TEST-CODE-1234",),
        )
        row = cur.fetchone()
        assert row is not None
        assert row["use_count"] == 1
        assert row["max_uses"] == max_uses
        assert row["used_at"] is None
        assert row["last_used_at"] is not None

    # Second enrollment hits the configured max uses.
    second = _perform_enrollment_cycle(app, factory, "TEST-CODE-1234", private_key)
    assert second["status"] == "approved"

    with factory() as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT use_count, used_at, last_used_at, used_by_guid FROM enrollment_install_codes WHERE code = ?",
            ("TEST-CODE-1234",),
        )
        row = cur.fetchone()
        assert row is not None
        assert row["use_count"] == max_uses
        assert row["used_at"] is not None
        assert row["last_used_at"] is not None
        consumed_guid = row["used_by_guid"]
        assert consumed_guid

    # Additional enrollments from the same identity reuse the stored GUID even after consumption.
    third = _perform_enrollment_cycle(app, factory, "TEST-CODE-1234", private_key)
    assert third["status"] == "approved"

    with factory() as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT use_count, used_at, last_used_at, used_by_guid FROM enrollment_install_codes WHERE code = ?",
            ("TEST-CODE-1234",),
        )
        row = cur.fetchone()
        assert row is not None
        assert row["use_count"] == max_uses + 1
        assert row["used_by_guid"] == consumed_guid
        assert row["used_at"] is not None
        assert row["last_used_at"] is not None

        cur.execute("SELECT COUNT(*) FROM devices WHERE guid = ?", (consumed_guid,))
        assert cur.fetchone()[0] == 1
