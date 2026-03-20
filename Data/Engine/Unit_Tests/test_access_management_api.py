# ======================================================
# Data\Engine\Unit_Tests\test_access_management_api.py
# Description: Exercises access-management endpoints covering Aegis, credentials, and GitHub token administration.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import json
from typing import Any, Dict

import pytest
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.server import create_app

from Data.Engine.crypto.aegis import ENVELOPE_PREFIX
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


def _fresh_admin_client(harness: EngineTestHarness):
    config = {
        "DATABASE_URL": f"sqlite:///{harness.db_path.as_posix()}",
        "TLS_CERT_PATH": harness.app.config["TLS_CERT_PATH"],
        "TLS_KEY_PATH": harness.app.config["TLS_KEY_PATH"],
        "TLS_BUNDLE_PATH": harness.app.config["TLS_BUNDLE_PATH"],
        "SECRET_KEY": harness.app.config["SECRET_KEY"],
        "LOG_FILE": harness.app.config["LOG_FILE"],
        "ERROR_LOG_FILE": harness.app.config["ERROR_LOG_FILE"],
        "API_LOG_FILE": harness.app.config["API_LOG_FILE"],
        "VPN_TUNNEL_LOG_FILE": harness.app.config["VPN_TUNNEL_LOG_FILE"],
        "STATIC_FOLDER": harness.app.config["STATIC_FOLDER"],
        "API_GROUPS": tuple(harness.context.api_groups),
    }
    app, _socketio, context = create_app(config)
    app.config.update(TESTING=True)
    client = app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client, context


def _decode_db_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, memoryview):
        value = value.tobytes()
    if isinstance(value, bytes):
        return value.decode("utf-8")
    return str(value)


def _setup_aegis(client, cipher: str = "correct horse battery staple"):
    response = client.post("/api/aegis/setup", json={"cipher": cipher})
    assert response.status_code == 200
    return response.get_json()


def _mock_github_verify_ok(monkeypatch: pytest.MonkeyPatch) -> None:
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


def test_github_token_get_without_value(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    response = client.get("/api/github/token")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["has_token"] is False
    assert payload["status"] == "missing"
    assert payload["token"] == ""
    assert payload["configured"] is False
    assert payload["locked"] is False


def test_github_token_update_requires_aegis_setup(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    response = client.post("/api/github/token", json={"token": "ghp_test"})
    assert response.status_code == 409
    payload = response.get_json()
    assert payload["error"] == "aegis_not_configured"


def test_github_token_update_encrypts_after_aegis_setup(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _mock_github_verify_ok(monkeypatch)
    client = _admin_client(engine_harness)
    _setup_aegis(client)

    response = client.post("/api/github/token", json={"token": "ghp_test"})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["has_token"] is True
    assert payload["valid"] is True
    assert payload["status"] == "ok"
    assert payload["token"] == "ghp_test"
    assert payload["configured"] is True
    assert payload["locked"] is False

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT token FROM github_token LIMIT 1")
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert _decode_db_text(row[0]).startswith(ENVELOPE_PREFIX)

    verify_response = client.get("/api/github/token")
    assert verify_response.status_code == 200
    verify_payload = verify_response.get_json()
    assert verify_payload["has_token"] is True
    assert verify_payload["token"] == "ghp_test"


def test_aegis_setup_encrypts_legacy_plaintext_and_restart_relocks(
    engine_harness: EngineTestHarness,
) -> None:
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
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_method,
                become_username,
                become_password_encrypted,
                metadata_json,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "Legacy SSH",
                "Legacy plaintext credential",
                1,
                "machine",
                "ssh",
                "automation",
                b"legacy-password",
                b"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
                b"legacy-key-passphrase",
                "sudo",
                "root",
                b"legacy-become-password",
                "{}",
                1_700_000_000,
                1_700_000_000,
            ),
        )
        cur.execute("INSERT INTO github_token(token) VALUES (?)", ("ghp_legacy",))
        conn.commit()
    finally:
        conn.close()

    client = _admin_client(engine_harness)

    status_before = client.get("/api/aegis/status")
    assert status_before.status_code == 200
    assert status_before.get_json()["configured"] is False

    response = client.post("/api/aegis/setup", json={"cipher": "cipher-one"})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["configured"] is True
    assert payload["locked"] is False

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_password_encrypted
              FROM credentials
             WHERE name=?
            """,
            ("Legacy SSH",),
        )
        credential_row = cur.fetchone()
        cur.execute("SELECT token FROM github_token LIMIT 1")
        token_row = cur.fetchone()
        cur.execute(
            """
            SELECT kdf_name, kdf_params_json, verification_token
              FROM aegis_cipher_state
             WHERE id=1
            """
        )
        state_row = cur.fetchone()
    finally:
        conn.close()

    assert credential_row is not None
    assert token_row is not None
    assert state_row is not None
    assert state_row[0] == "scrypt"
    assert '"salt_b64"' in str(state_row[1] or "")
    assert str(state_row[2] or "").startswith(ENVELOPE_PREFIX)

    for value in credential_row:
        assert _decode_db_text(value).startswith(ENVELOPE_PREFIX)
    assert _decode_db_text(token_row[0]).startswith(ENVELOPE_PREFIX)

    service = engine_harness.context.aegis_cipher_service
    assert service.decrypt_secret_blob(credential_row[0]) == "legacy-password"
    assert service.decrypt_secret_blob(credential_row[1]).startswith("-----BEGIN OPENSSH PRIVATE KEY-----")
    assert service.decrypt_secret_blob(credential_row[2]) == "legacy-key-passphrase"
    assert service.decrypt_secret_blob(credential_row[3]) == "legacy-become-password"
    assert service.decrypt_secret_text(token_row[0]) == "ghp_legacy"

    fresh_client, fresh_context = _fresh_admin_client(engine_harness)
    fresh_status = fresh_client.get("/api/aegis/status")
    assert fresh_status.status_code == 200
    fresh_payload = fresh_status.get_json()
    assert fresh_payload["configured"] is True
    assert fresh_payload["locked"] is True
    assert fresh_context.aegis_cipher_service.is_locked() is True

    wrong_unlock = fresh_client.post("/api/aegis/unlock", json={"cipher": "wrong-cipher"})
    assert wrong_unlock.status_code == 401
    assert wrong_unlock.get_json()["error"] == "invalid_cipher"

    correct_unlock = fresh_client.post("/api/aegis/unlock", json={"cipher": "cipher-one"})
    assert correct_unlock.status_code == 200
    assert correct_unlock.get_json()["locked"] is False
    assert fresh_context.aegis_cipher_service.decrypt_secret_text(token_row[0]) == "ghp_legacy"


def test_credentials_admin_crud_round_trip_with_aegis(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    _setup_aegis(client)

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

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_password_encrypted
              FROM credentials
             WHERE id=?
            """,
            (created["id"],),
        )
        secret_row = cur.fetchone()
    finally:
        conn.close()

    assert secret_row is not None
    for value in secret_row:
        assert _decode_db_text(value).startswith(ENVELOPE_PREFIX)

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


def test_credentials_and_github_mutations_block_while_unconfigured_or_locked(
    engine_harness: EngineTestHarness,
) -> None:
    client = _admin_client(engine_harness)

    unconfigured_create = client.post(
        "/api/credentials",
        json={
            "name": "Blocked Credential",
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "secret",
        },
    )
    assert unconfigured_create.status_code == 409
    assert unconfigured_create.get_json()["error"] == "aegis_not_configured"

    unconfigured_token = client.post("/api/github/token", json={"token": "ghp_blocked"})
    assert unconfigured_token.status_code == 409
    assert unconfigured_token.get_json()["error"] == "aegis_not_configured"

    _setup_aegis(client, cipher="cipher-two")

    create_response = client.post(
        "/api/credentials",
        json={
            "name": "Locked Metadata Credential",
            "description": "Visible while locked",
            "site_id": 1,
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "super-secret",
        },
    )
    assert create_response.status_code == 200
    credential_id = create_response.get_json()["credential"]["id"]

    fresh_client, _fresh_context = _fresh_admin_client(engine_harness)

    status_response = fresh_client.get("/api/aegis/status")
    assert status_response.status_code == 200
    assert status_response.get_json()["locked"] is True

    list_response = fresh_client.get("/api/credentials")
    assert list_response.status_code == 200
    listed = list_response.get_json()["credentials"]
    assert len(listed) == 1
    assert listed[0]["name"] == "Locked Metadata Credential"

    detail_response = fresh_client.get(f"/api/credentials/{credential_id}")
    assert detail_response.status_code == 200
    assert detail_response.get_json()["credential"]["name"] == "Locked Metadata Credential"

    locked_create = fresh_client.post(
        "/api/credentials",
        json={
            "name": "Blocked While Locked",
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "secret",
        },
    )
    assert locked_create.status_code == 423
    assert locked_create.get_json()["error"] == "aegis_locked"

    locked_update = fresh_client.put(
        f"/api/credentials/{credential_id}",
        json={"description": "Should not apply"},
    )
    assert locked_update.status_code == 423
    assert locked_update.get_json()["error"] == "aegis_locked"

    locked_delete = fresh_client.delete(f"/api/credentials/{credential_id}")
    assert locked_delete.status_code == 423
    assert locked_delete.get_json()["error"] == "aegis_locked"

    locked_github_get = fresh_client.get("/api/github/token")
    assert locked_github_get.status_code == 200
    locked_github_payload = locked_github_get.get_json()
    assert locked_github_payload["status"] == "locked"
    assert locked_github_payload["token"] == ""
    assert locked_github_payload["locked"] is True

    locked_github_post = fresh_client.post("/api/github/token", json={"token": "ghp_locked"})
    assert locked_github_post.status_code == 423
    assert locked_github_post.get_json()["error"] == "aegis_locked"


def test_aegis_rotation_invalidates_old_cipher_and_preserves_secret_access(
    engine_harness: EngineTestHarness,
) -> None:
    client = _admin_client(engine_harness)
    _setup_aegis(client, cipher="old-cipher")

    create_response = client.post(
        "/api/credentials",
        json={
            "name": "Rotated Credential",
            "description": "Rotation test",
            "site_id": 1,
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "rotate-me",
        },
    )
    assert create_response.status_code == 200
    credential_id = create_response.get_json()["credential"]["id"]

    service = engine_harness.context.aegis_cipher_service
    encrypted_token = service.encrypt_secret_for_text("ghp_rotated")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("DELETE FROM github_token")
        cur.execute("INSERT INTO github_token(token) VALUES (?)", (encrypted_token,))
        conn.commit()
    finally:
        conn.close()

    rotate_response = client.post(
        "/api/aegis/rotate",
        json={"current_cipher": "old-cipher", "new_cipher": "new-cipher"},
    )
    assert rotate_response.status_code == 200
    rotate_payload = rotate_response.get_json()
    assert rotate_payload["configured"] is True
    assert rotate_payload["locked"] is False

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT password_encrypted FROM credentials WHERE id=?", (credential_id,))
        credential_row = cur.fetchone()
        cur.execute("SELECT token FROM github_token LIMIT 1")
        token_row = cur.fetchone()
    finally:
        conn.close()

    assert credential_row is not None
    assert token_row is not None
    assert service.decrypt_secret_blob(credential_row[0]) == "rotate-me"
    assert service.decrypt_secret_text(token_row[0]) == "ghp_rotated"

    fresh_client, fresh_context = _fresh_admin_client(engine_harness)

    old_unlock = fresh_client.post("/api/aegis/unlock", json={"cipher": "old-cipher"})
    assert old_unlock.status_code == 401
    assert old_unlock.get_json()["error"] == "invalid_cipher"

    new_unlock = fresh_client.post("/api/aegis/unlock", json={"cipher": "new-cipher"})
    assert new_unlock.status_code == 200
    assert new_unlock.get_json()["locked"] is False

    assert fresh_context.aegis_cipher_service.decrypt_secret_blob(credential_row[0]) == "rotate-me"
    assert fresh_context.aegis_cipher_service.decrypt_secret_text(token_row[0]) == "ghp_rotated"


def test_aegis_rotation_rolls_back_on_reencryption_failure(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _admin_client(engine_harness)
    _setup_aegis(client, cipher="rollback-cipher")

    create_response = client.post(
        "/api/credentials",
        json={
            "name": "Rollback Credential",
            "description": "Rollback test",
            "site_id": 1,
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "rollback-secret",
        },
    )
    assert create_response.status_code == 200
    credential_id = create_response.get_json()["credential"]["id"]

    service = engine_harness.context.aegis_cipher_service

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT verification_token FROM aegis_cipher_state WHERE id=1")
        original_verification = cur.fetchone()[0]
        cur.execute("SELECT password_encrypted FROM credentials WHERE id=?", (credential_id,))
        original_secret = cur.fetchone()[0]
    finally:
        conn.close()

    def _boom(cur, *, old_key, new_key):
        raise RuntimeError("forced rotation failure")

    monkeypatch.setattr(service, "_reencrypt_github_token", _boom)

    with pytest.raises(RuntimeError, match="forced rotation failure"):
        service.rotate("rollback-cipher", "new-rollback-cipher")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT verification_token FROM aegis_cipher_state WHERE id=1")
        current_verification = cur.fetchone()[0]
        cur.execute("SELECT password_encrypted FROM credentials WHERE id=?", (credential_id,))
        current_secret = cur.fetchone()[0]
    finally:
        conn.close()

    assert _decode_db_text(current_verification) == _decode_db_text(original_verification)
    assert _decode_db_text(current_secret) == _decode_db_text(original_secret)
    assert service.status()["locked"] is False
    assert service.decrypt_secret_blob(current_secret) == "rollback-secret"


def test_aegis_force_reset_preserves_records_and_requires_secret_reentry(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _mock_github_verify_ok(monkeypatch)
    client = _admin_client(engine_harness)
    _setup_aegis(client, cipher="force-reset-old")

    create_response = client.post(
        "/api/credentials",
        json={
            "name": "Force Reset Credential",
            "description": "Needs recovery after reset",
            "site_id": 1,
            "credential_type": "machine",
            "connection_type": "ssh",
            "username": "automation",
            "password": "force-reset-password",
            "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
        },
    )
    assert create_response.status_code == 200
    credential_id = create_response.get_json()["credential"]["id"]

    github_response = client.post("/api/github/token", json={"token": "ghp_force_reset"})
    assert github_response.status_code == 200

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                credential_id,
                enabled,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?)
            """,
            (
                91,
                "Credential Dependent Job",
                "[]",
                json.dumps(["test-device"]),
                "once",
                "ssh",
                credential_id,
                1,
                1_773_900_000,
                1_773_900_000,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    reset_response = client.post("/api/aegis/force_reset", json={})
    assert reset_response.status_code == 200
    reset_payload = reset_response.get_json()
    assert reset_payload["configured"] is False
    assert reset_payload["locked"] is False
    assert reset_payload["force_reset"] is True
    assert reset_payload["affected_credentials"] == 1
    assert reset_payload["disabled_jobs"] == 1
    assert reset_payload["github_token_reset"] is True

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_password_encrypted,
                metadata_json
              FROM credentials
             WHERE id=?
            """,
            (credential_id,),
        )
        credential_row = cur.fetchone()
        cur.execute("SELECT token, reset_required, reset_at FROM github_token LIMIT 1")
        github_row = cur.fetchone()
        cur.execute("SELECT enabled FROM scheduled_jobs WHERE id=?", (91,))
        job_row = cur.fetchone()
        cur.execute("SELECT COUNT(*) FROM aegis_cipher_state")
        state_count = int(cur.fetchone()[0] or 0)
    finally:
        conn.close()

    assert credential_row is not None
    assert credential_row[0] is None
    assert credential_row[1] is None
    assert credential_row[2] is None
    assert credential_row[3] is None
    credential_metadata = json.loads(credential_row[4] or "{}")
    assert credential_metadata["aegis_secret_state"] == "reset_required"
    assert set(credential_metadata["aegis_lost_secret_fields"]) == {"password", "private_key"}
    assert int(credential_metadata["aegis_reset_at"]) > 0

    assert github_row is not None
    assert github_row[0] is None
    assert int(github_row[1] or 0) == 1
    assert int(github_row[2] or 0) > 0

    assert job_row is not None
    assert int(job_row[0] or 0) == 0
    assert state_count == 0

    reconfigure_response = client.post("/api/aegis/setup", json={"cipher": "force-reset-new"})
    assert reconfigure_response.status_code == 200
    assert reconfigure_response.get_json()["configured"] is True
    assert reconfigure_response.get_json()["locked"] is False

    github_status = client.get("/api/github/token")
    assert github_status.status_code == 200
    github_payload = github_status.get_json()
    assert github_payload["status"] == "reset_required"
    assert github_payload["reset_required"] is True
    assert github_payload["token"] == ""

    credential_detail = client.get(f"/api/credentials/{credential_id}")
    assert credential_detail.status_code == 200
    detail_payload = credential_detail.get_json()["credential"]
    assert detail_payload["secret_reset_required"] is True
    assert set(detail_payload["lost_secret_fields"]) == {"password", "private_key"}

    recover_response = client.put(
        f"/api/credentials/{credential_id}",
        json={
            "password": "replacement-password",
            "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nrecovery\n-----END OPENSSH PRIVATE KEY-----",
        },
    )
    assert recover_response.status_code == 200
    recovered_payload = recover_response.get_json()["credential"]
    assert recovered_payload["secret_reset_required"] is False
    assert recovered_payload["lost_secret_fields"] == []
    assert recovered_payload["has_password"] is True
    assert recovered_payload["has_private_key"] is True

    github_restore = client.post("/api/github/token", json={"token": "ghp_restored"})
    assert github_restore.status_code == 200
    github_restore_payload = github_restore.get_json()
    assert github_restore_payload["reset_required"] is False
    assert github_restore_payload["status"] == "ok"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT metadata_json, password_encrypted, private_key_encrypted FROM credentials WHERE id=?", (credential_id,))
        recovered_row = cur.fetchone()
        cur.execute("SELECT token, reset_required, reset_at FROM github_token LIMIT 1")
        restored_github_row = cur.fetchone()
    finally:
        conn.close()

    assert recovered_row is not None
    recovered_metadata = json.loads(recovered_row[0] or "{}")
    assert "aegis_secret_state" not in recovered_metadata
    assert _decode_db_text(recovered_row[1]).startswith(ENVELOPE_PREFIX)
    assert _decode_db_text(recovered_row[2]).startswith(ENVELOPE_PREFIX)
    assert restored_github_row is not None
    assert _decode_db_text(restored_github_row[0]).startswith(ENVELOPE_PREFIX)
    assert int(restored_github_row[1] or 0) == 0
    assert restored_github_row[2] is None


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
