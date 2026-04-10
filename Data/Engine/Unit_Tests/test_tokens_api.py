# ======================================================
# Data\Engine\Unit_Tests\test_tokens_api.py
# Description: Validates refresh token issuance, expiry handling, and error cases.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import hashlib
from Data.Engine.db import dbapi as sqlite3
from datetime import datetime, timedelta, timezone

from flask.testing import FlaskClient

from .conftest import EngineTestHarness


def _iso(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).isoformat()


def test_refresh_token_success(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    guid = "54E8C9E2-6B3D-4B51-A456-4ACB94C45F00"
    refresh_token = "refresh-token-value"
    token_hash = hashlib.sha256(refresh_token.encode("utf-8")).hexdigest()
    now = datetime.now(tz=timezone.utc)
    expires_at = now + timedelta(days=1)

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO devices (guid, hostname, created_at, last_seen, ssl_key_fingerprint,
                                 token_version, status, key_added_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                guid,
                "device-one",
                int(now.timestamp()),
                int(now.timestamp()),
                "fingerprint",
                1,
                "active",
                _iso(now),
            ),
        )
        cur.execute(
            """
            INSERT INTO refresh_tokens (id, guid, token_hash, created_at, expires_at, revoked_at, last_used_at)
            VALUES (?, ?, ?, ?, ?, NULL, NULL)
            """,
            (
                "token-row",
                guid,
                token_hash,
                _iso(now),
                _iso(expires_at),
            ),
        )
        conn.commit()

    response = client.post(
        "/api/agent/token/refresh",
        json={"guid": guid, "refresh_token": refresh_token},
    )
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["token_type"] == "Bearer"
    assert payload["expires_in"] == 900
    assert isinstance(payload["access_token"], str) and payload["access_token"]

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            "SELECT last_used_at, revoked_at, expires_at FROM refresh_tokens WHERE guid = ?",
            (guid,),
        )
        row = cur.fetchone()
    assert row is not None
    last_used_at, revoked_at, bumped_expires_at = row
    assert last_used_at is not None
    assert revoked_at is None
    refreshed_expiry = datetime.fromisoformat(bumped_expires_at)
    assert refreshed_expiry > now + timedelta(days=80)


def test_refresh_token_requires_payload(engine_harness: EngineTestHarness) -> None:
    client: FlaskClient = engine_harness.app.test_client()

    response = client.post(
        "/api/agent/token/refresh",
        json={"guid": "", "refresh_token": ""},
    )
    assert response.status_code == 400
    payload = response.get_json()
    assert payload["error"] == "invalid_request"


def test_refresh_token_returns_device_purged_when_barrier_exists(engine_harness: EngineTestHarness) -> None:
    harness = engine_harness
    client: FlaskClient = harness.app.test_client()

    guid = "54E8C9E2-6B3D-4B51-A456-4ACB94C45F00"
    refresh_token = "stale-refresh-token"
    token_hash = hashlib.sha256(refresh_token.encode("utf-8")).hexdigest()
    now = datetime.now(tz=timezone.utc)
    expires_at = now + timedelta(days=1)

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
            INSERT OR REPLACE INTO refresh_tokens (id, guid, token_hash, created_at, expires_at, revoked_at, last_used_at)
            VALUES (?, ?, ?, ?, ?, NULL, NULL)
            """,
            (
                "stale-refresh-row",
                guid,
                token_hash,
                _iso(now),
                _iso(expires_at),
            ),
        )
        conn.execute(
            """
            INSERT OR REPLACE INTO device_purge_barriers (
                guid, required_token_version, purged_at, purged_by, last_hostname, last_agent_id
            )
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (
                guid,
                2,
                _iso(now),
                "admin",
                "device-one",
                "device-one-agent",
            ),
        )
        conn.commit()

    response = client.post(
        "/api/agent/token/refresh",
        json={"guid": guid, "refresh_token": refresh_token},
    )

    assert response.status_code == 401
    payload = response.get_json()
    assert payload["error"] == "device_purged"
