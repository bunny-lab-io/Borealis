# ======================================================
# Data\Engine\Unit_Tests\test_access_management_api.py
# Description: Exercises access-management endpoints covering Aegis, credentials, and GitHub token administration.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import hashlib
import json
from typing import Any, Dict

import pytest
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.server import create_app

from Data.Engine.crypto.aegis import ENVELOPE_PREFIX
from Data.Engine.integrations import github as github_integration
from Data.Engine.services.API.access_management import login as access_login

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


def _sha512_hex(value: str) -> str:
    return hashlib.sha512((value or "").encode("utf-8")).hexdigest()


def _bootstrap_state(client):
    response = client.get("/api/bootstrap/state")
    assert response.status_code == 200
    return response.get_json()


def _setup_aegis(client, cipher: str = "correct horse battery staple"):
    response = client.post("/api/bootstrap/aegis/setup", json={"cipher": cipher})
    assert response.status_code == 200
    return response.get_json()


def _unlock_aegis(client, cipher: str):
    response = client.post("/api/bootstrap/aegis/unlock", json={"cipher": cipher})
    return response


def _migrate_operator_auth(harness: EngineTestHarness) -> None:
    if not harness.aegis_cipher:
        return
    service = harness.context.aegis_cipher_service
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        service._migrate_legacy_operator_auth(
            cur,
            service._require_active_key(required_configured=True),
        )
        conn.commit()
    finally:
        conn.close()


def _decrypt_auth_secret(harness: EngineTestHarness, value: Any) -> str:
    text = _decode_db_text(value)
    if not text:
        return ""
    if not harness.aegis_cipher:
        return text
    return harness.context.aegis_cipher_service.decrypt_secret_text(text)


def _enable_fake_passkeys(monkeypatch: pytest.MonkeyPatch) -> None:
    class DummyOptions:
        challenge = b"dummy-challenge"

    class DummyRegistrationVerification:
        credential_id = "dummy-credential"
        credential_public_key = "dummy-public-key"
        sign_count = 0
        aaguid = ""

    class DummyAuthenticationVerification:
        new_sign_count = 0

    class DummyDescriptor:
        def __init__(self, id=None, **kwargs):
            self.id = id
            self.kwargs = kwargs

    class DummyAuthenticatorSelectionCriteria:
        def __init__(self, **kwargs):
            self.kwargs = kwargs

    class DummyResidentKeyRequirement:
        PREFERRED = "preferred"
        REQUIRED = "required"

    class DummyUserVerificationRequirement:
        REQUIRED = "required"

    monkeypatch.setattr(access_login, "base64url_to_bytes", lambda value: f"bytes:{value}".encode("utf-8"))
    monkeypatch.setattr(access_login, "PublicKeyCredentialDescriptor", DummyDescriptor)
    monkeypatch.setattr(access_login, "AuthenticatorSelectionCriteria", DummyAuthenticatorSelectionCriteria)
    monkeypatch.setattr(access_login, "ResidentKeyRequirement", DummyResidentKeyRequirement)
    monkeypatch.setattr(access_login, "UserVerificationRequirement", DummyUserVerificationRequirement)
    def fake_generate_registration_options(
        *,
        rp_id,
        rp_name,
        user_name,
        user_id=None,
        user_display_name=None,
        challenge=None,
        timeout=60000,
        attestation=None,
        authenticator_selection=None,
        exclude_credentials=None,
        supported_pub_key_algs=None,
        hints=None,
    ):
        return DummyOptions()

    def fake_generate_authentication_options(
        *,
        rp_id,
        challenge=None,
        timeout=60000,
        allow_credentials=None,
        user_verification=None,
    ):
        return DummyOptions()

    monkeypatch.setattr(access_login, "generate_registration_options", fake_generate_registration_options)
    monkeypatch.setattr(access_login, "generate_authentication_options", fake_generate_authentication_options)
    monkeypatch.setattr(
        access_login,
        "options_to_json",
        lambda options: json.dumps({"challenge": "dummy-challenge", "rpId": "localhost"}),
    )
    monkeypatch.setattr(
        access_login,
        "verify_registration_response",
        lambda **kwargs: DummyRegistrationVerification(),
    )
    monkeypatch.setattr(
        access_login,
        "verify_authentication_response",
        lambda **kwargs: DummyAuthenticationVerification(),
    )


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
    assert payload["configured"] is True
    assert payload["locked"] is False


def test_github_token_update_requires_aegis_setup(
    unconfigured_engine_harness: EngineTestHarness,
) -> None:
    client = _admin_client(unconfigured_engine_harness)
    response = client.post("/api/github/token", json={"token": "ghp_test"})
    assert response.status_code == 401
    payload = response.get_json()
    assert payload["error"] == "unauthorized"
    assert _bootstrap_state(client)["phase"] == "aegis_setup_required"


def test_github_token_update_encrypts_after_aegis_setup(
    unconfigured_engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _mock_github_verify_ok(monkeypatch)
    client = _admin_client(unconfigured_engine_harness)
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

    conn = sqlite3.connect(str(unconfigured_engine_harness.db_path))
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
    unconfigured_engine_harness: EngineTestHarness,
) -> None:
    conn = sqlite3.connect(str(unconfigured_engine_harness.db_path))
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

    client = _admin_client(unconfigured_engine_harness)

    status_before = _bootstrap_state(client)
    assert status_before["configured"] is False
    assert status_before["phase"] == "aegis_setup_required"

    payload = _setup_aegis(client, cipher="cipher-one")
    assert payload["configured"] is True
    assert payload["locked"] is False

    conn = sqlite3.connect(str(unconfigured_engine_harness.db_path))
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

    service = unconfigured_engine_harness.context.aegis_cipher_service
    assert service.decrypt_secret_blob(credential_row[0]) == "legacy-password"
    assert service.decrypt_secret_blob(credential_row[1]).startswith("-----BEGIN OPENSSH PRIVATE KEY-----")
    assert service.decrypt_secret_blob(credential_row[2]) == "legacy-key-passphrase"
    assert service.decrypt_secret_blob(credential_row[3]) == "legacy-become-password"
    assert service.decrypt_secret_text(token_row[0]) == "ghp_legacy"

    fresh_client, fresh_context = _fresh_admin_client(unconfigured_engine_harness)
    fresh_payload = _bootstrap_state(fresh_client)
    assert fresh_payload["configured"] is True
    assert fresh_payload["locked"] is True
    assert fresh_payload["phase"] == "aegis_unlock_required"
    assert fresh_context.aegis_cipher_service.is_locked() is True

    wrong_unlock = _unlock_aegis(fresh_client, "wrong-cipher")
    assert wrong_unlock.status_code == 401
    assert wrong_unlock.get_json()["error"] == "invalid_cipher"

    correct_unlock = _unlock_aegis(fresh_client, "cipher-one")
    assert correct_unlock.status_code == 200
    assert correct_unlock.get_json()["locked"] is False
    assert fresh_context.aegis_cipher_service.decrypt_secret_text(token_row[0]) == "ghp_legacy"


def test_unlock_migrates_legacy_operator_auth_for_existing_aegis_install(
    unconfigured_engine_harness: EngineTestHarness,
) -> None:
    client = _admin_client(unconfigured_engine_harness)
    _setup_aegis(client, cipher="cipher-upgrade")

    password_hash = _sha512_hex("admin-password")
    conn = sqlite3.connect(str(unconfigured_engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE users
               SET password_sha512=?,
                   mfa_secret=?,
                   mfa_enabled=1,
                   mfa_disabled=0
             WHERE LOWER(username)=LOWER(?)
            """,
            (password_hash, "LEGACY-MFA-SECRET", "admin"),
        )
        conn.commit()
    finally:
        conn.close()

    fresh_client, fresh_context = _fresh_admin_client(unconfigured_engine_harness)
    locked_state = _bootstrap_state(fresh_client)
    assert locked_state["phase"] == "aegis_unlock_required"

    unlock_response = _unlock_aegis(fresh_client, "cipher-upgrade")
    assert unlock_response.status_code == 200

    conn = sqlite3.connect(str(unconfigured_engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT password_sha512, COALESCE(mfa_secret, '') FROM users WHERE LOWER(username)=LOWER(?)",
            ("admin",),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert _decode_db_text(row[0]).startswith(ENVELOPE_PREFIX)
    assert _decode_db_text(row[1]).startswith(ENVELOPE_PREFIX)
    assert fresh_context.aegis_cipher_service.decrypt_secret_text(row[0]) == password_hash
    assert fresh_context.aegis_cipher_service.decrypt_secret_text(row[1]) == "LEGACY-MFA-SECRET"

    login_response = fresh_client.post(
        "/api/auth/login",
        json={"username": "admin", "password_sha512": password_hash},
    )
    assert login_response.status_code == 200
    payload = login_response.get_json()
    assert payload["status"] == "mfa_required"
    assert payload["stage"] == "verify"


def test_credentials_admin_crud_round_trip_with_aegis(engine_harness: EngineTestHarness) -> None:
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
    unconfigured_engine_harness: EngineTestHarness,
) -> None:
    client = _admin_client(unconfigured_engine_harness)

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
    assert unconfigured_create.status_code == 401
    assert unconfigured_create.get_json()["error"] == "unauthorized"

    unconfigured_token = client.post("/api/github/token", json={"token": "ghp_blocked"})
    assert unconfigured_token.status_code == 401
    assert unconfigured_token.get_json()["error"] == "unauthorized"

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

    fresh_client, _fresh_context = _fresh_admin_client(unconfigured_engine_harness)

    status_response = _bootstrap_state(fresh_client)
    assert status_response["locked"] is True
    assert status_response["phase"] == "aegis_unlock_required"

    list_response = fresh_client.get("/api/credentials")
    assert list_response.status_code == 401

    detail_response = fresh_client.get(f"/api/credentials/{credential_id}")
    assert detail_response.status_code == 401

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
    assert locked_create.status_code == 401
    assert locked_create.get_json()["error"] == "unauthorized"

    locked_update = fresh_client.put(
        f"/api/credentials/{credential_id}",
        json={"description": "Should not apply"},
    )
    assert locked_update.status_code == 401
    assert locked_update.get_json()["error"] == "unauthorized"

    locked_delete = fresh_client.delete(f"/api/credentials/{credential_id}")
    assert locked_delete.status_code == 401
    assert locked_delete.get_json()["error"] == "unauthorized"

    locked_github_get = fresh_client.get("/api/github/token")
    assert locked_github_get.status_code == 401
    assert locked_github_get.get_json()["error"] == "unauthorized"

    locked_github_post = fresh_client.post("/api/github/token", json={"token": "ghp_locked"})
    assert locked_github_post.status_code == 401
    assert locked_github_post.get_json()["error"] == "unauthorized"


def test_aegis_rotation_invalidates_old_cipher_and_preserves_secret_access(
    engine_harness: EngineTestHarness,
) -> None:
    client = _admin_client(engine_harness)
    current_cipher = str(engine_harness.aegis_cipher or "unit-test-aegis-cipher")

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
        json={"current_cipher": current_cipher, "new_cipher": "new-cipher"},
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
    fresh_status = _bootstrap_state(fresh_client)
    assert fresh_status["phase"] == "aegis_unlock_required"
    assert fresh_status["locked"] is True

    old_unlock = _unlock_aegis(fresh_client, current_cipher)
    assert old_unlock.status_code == 401
    assert old_unlock.get_json()["error"] == "invalid_cipher"

    new_unlock = _unlock_aegis(fresh_client, "new-cipher")
    assert new_unlock.status_code == 200
    assert new_unlock.get_json()["locked"] is False

    assert fresh_context.aegis_cipher_service.decrypt_secret_blob(credential_row[0]) == "rotate-me"
    assert fresh_context.aegis_cipher_service.decrypt_secret_text(token_row[0]) == "ghp_rotated"


def test_aegis_rotation_rolls_back_on_reencryption_failure(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _admin_client(engine_harness)
    current_cipher = str(engine_harness.aegis_cipher or "unit-test-aegis-cipher")

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
        service.rotate(current_cipher, "new-rollback-cipher")

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
    assert int(reset_payload["affected_users"] or 0) >= 1
    assert int(reset_payload["removed_passkeys"] or 0) == 0
    assert _bootstrap_state(client)["phase"] == "aegis_setup_required"

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

    reconfigure_response = client.post("/api/bootstrap/aegis/setup", json={"cipher": "force-reset-new"})
    assert reconfigure_response.status_code == 200
    assert reconfigure_response.get_json()["configured"] is True
    assert reconfigure_response.get_json()["locked"] is False
    assert reconfigure_response.get_json()["phase"] == "admin_recovery_required"

    class _DummyTotp:
        def verify(self, code: str, valid_window: int = 0) -> bool:
            return str(code) == "123456"

    monkeypatch.setattr(access_login, "_generate_totp_secret", lambda: "JBSWY3DPEHPK3PXP")
    monkeypatch.setattr(access_login, "_totp_for_secret", lambda secret: _DummyTotp())
    monkeypatch.setattr(
        access_login,
        "_totp_provisioning_uri",
        lambda secret, username: f"otpauth://totp/Borealis:{username}?secret={secret}",
    )
    monkeypatch.setattr(access_login, "_totp_qr_data_uri", lambda payload: "data:image/png;base64,test")

    recover_start = client.post(
        "/api/bootstrap/admin/recover",
        json={
            "username": "admin",
            "password_sha512": _sha512_hex("force-reset-recovered-admin"),
        },
    )
    assert recover_start.status_code == 200
    recover_payload = recover_start.get_json()
    assert recover_payload["status"] == "mfa_required"
    assert recover_payload["pending_token"]

    recover_verify = client.post(
        "/api/bootstrap/admin/mfa/verify",
        json={"pending_token": recover_payload["pending_token"], "code": "123456"},
    )
    assert recover_verify.status_code == 200
    assert recover_verify.get_json()["status"] == "ok"
    assert _bootstrap_state(client)["phase"] == "login_required"

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


def test_auth_me_reports_current_user_mfa_state(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", "test", "User", 0, 0, 0, 0, 0, None),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.get("/api/auth/me")
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["username"] == "operator"
    assert payload["display_name"] == "Operator One"
    assert payload["role"] == "User"
    assert payload["mfa_enabled"] is True


def test_user_can_reset_own_mfa_without_disabling_it(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", "test", "User", 0, 0, 0, 1, 0, "existing-secret"),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.post("/api/auth/mfa/reset")
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["username"] == "operator"
    assert payload["mfa_enabled"] is True
    assert payload["setup_required_on_next_login"] is True

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT COALESCE(mfa_enabled, 0), COALESCE(mfa_disabled, 0), mfa_secret FROM users WHERE LOWER(username)=LOWER(?)",
            ("operator",),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert int(row[0] or 0) == 0
    assert int(row[1] or 0) == 0
    assert row[2] is None


def test_user_mfa_reset_requires_login(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    response = client.post("/api/auth/mfa/reset")
    assert response.status_code == 401


def test_login_requires_mfa_setup_by_default(engine_harness: EngineTestHarness, monkeypatch: pytest.MonkeyPatch) -> None:
    password_hash = _sha512_hex("operator-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", password_hash, "User", 0, 0, 0, 0, 0, None),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(access_login, "_generate_totp_secret", lambda: "JBSWY3DPEHPK3PXP")
    monkeypatch.setattr(
        access_login,
        "_totp_provisioning_uri",
        lambda secret, username: f"otpauth://totp/Borealis:{username}?secret={secret}",
    )
    monkeypatch.setattr(access_login, "_totp_qr_data_uri", lambda payload: "data:image/png;base64,test")

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/auth/login",
        json={"username": "operator", "password_sha512": password_hash},
    )
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "mfa_required"
    assert payload["stage"] == "setup"
    assert payload["pending_token"]
    assert payload["secret"] == "JBSWY3DPEHPK3PXP"


def test_login_self_heals_legacy_operator_auth_on_unlocked_engine(
    engine_harness: EngineTestHarness,
) -> None:
    password_hash = _sha512_hex("operator-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", password_hash, "User", 0, 0, 0, 1, 0, "LEGACYSECRET"),
        )
        conn.commit()
    finally:
        conn.close()

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/auth/login",
        json={"username": "operator", "password_sha512": password_hash},
    )
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "mfa_required"
    assert payload["stage"] == "verify"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT password_sha512, COALESCE(mfa_secret, '') FROM users WHERE LOWER(username)=LOWER(?)",
            ("operator",),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert _decode_db_text(row[0]).startswith(ENVELOPE_PREFIX)
    assert _decode_db_text(row[1]).startswith(ENVELOPE_PREFIX)


def test_login_skips_mfa_when_admin_has_disabled_it(engine_harness: EngineTestHarness) -> None:
    password_hash = _sha512_hex("operator-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", password_hash, "User", 0, 0, 0, 1, 1, "EXISTINGSECRET"),
        )
        conn.commit()
    finally:
        conn.close()

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/auth/login",
        json={"username": "operator", "password_sha512": password_hash},
    )
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["username"] == "operator"
    assert payload["role"] == "User"
    assert payload["token"]


def test_mfa_setup_verification_marks_user_as_configured(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    password_hash = _sha512_hex("operator-password")
    expected_secret = "JBSWY3DPEHPK3PXP"

    class _DummyTotp:
        def verify(self, code: str, valid_window: int = 0) -> bool:
            return str(code) == "123456"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", password_hash, "User", 0, 0, 0, 0, 0, None),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(access_login, "_generate_totp_secret", lambda: expected_secret)
    monkeypatch.setattr(
        access_login,
        "_totp_provisioning_uri",
        lambda secret, username: f"otpauth://totp/Borealis:{username}?secret={secret}",
    )
    monkeypatch.setattr(access_login, "_totp_qr_data_uri", lambda payload: "data:image/png;base64,test")
    monkeypatch.setattr(access_login, "_totp_for_secret", lambda secret: _DummyTotp())

    client = engine_harness.app.test_client()
    login_response = client.post(
        "/api/auth/login",
        json={"username": "operator", "password_sha512": password_hash},
    )
    assert login_response.status_code == 200
    login_payload = login_response.get_json()
    assert login_payload["status"] == "mfa_required"
    assert login_payload["stage"] == "setup"

    verify_response = client.post(
        "/api/auth/mfa/verify",
        json={"pending_token": login_payload["pending_token"], "code": "123456"},
    )
    assert verify_response.status_code == 200
    verify_payload = verify_response.get_json()
    assert verify_payload["status"] == "ok"
    assert verify_payload["username"] == "operator"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT COALESCE(mfa_enabled, 0), COALESCE(mfa_disabled, 0), COALESCE(mfa_secret, '') FROM users WHERE LOWER(username)=LOWER(?)",
            ("operator",),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert int(row[0] or 0) == 1
    assert int(row[1] or 0) == 0
    assert (row[2] or "") == expected_secret


def test_password_login_with_existing_passkey_still_requires_totp(engine_harness: EngineTestHarness) -> None:
    password_hash = _sha512_hex("operator-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", password_hash, "User", 0, 0, 0, 1, 0, "EXISTINGSECRET"),
        )
        cur.execute(
            """
            INSERT INTO user_passkeys (
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "cred-operator", "public-key-operator", 3, "Desk Passkey", "[]", "", 1_700_000_000, 1_700_000_000),
        )
        conn.commit()
    finally:
        conn.close()

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/auth/login",
        json={"username": "operator", "password_sha512": password_hash},
    )
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "mfa_required"
    assert payload["stage"] == "verify"
    assert payload["available_methods"] == ["totp"]
    assert payload["preferred_method"] == "totp"


def test_password_login_without_totp_setup_still_prompts_for_totp(engine_harness: EngineTestHarness) -> None:
    password_hash = _sha512_hex("operator-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", password_hash, "User", 0, 0, 0, 1, 0, None),
        )
        cur.execute(
            """
            INSERT INTO user_passkeys (
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "cred-operator", "public-key-operator", 3, "Desk Passkey", "[]", "", 1_700_000_000, 1_700_000_000),
        )
        conn.commit()
    finally:
        conn.close()

    client = engine_harness.app.test_client()
    response = client.post(
        "/api/auth/login",
        json={"username": "operator", "password_sha512": password_hash},
    )
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "mfa_required"
    assert payload["stage"] == "setup"
    assert payload["available_methods"] == ["totp"]
    assert payload["preferred_method"] == "totp"
    assert payload["secret"]


def test_authenticated_user_can_register_passkey(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _enable_fake_passkeys(monkeypatch)

    class DummyRegistrationOptions:
        challenge = b"register-challenge"

    class DummyRegistrationVerification:
        credential_id = b"cred-setup"
        credential_public_key = b"public-key-setup"
        sign_count = 4
        aaguid = "aaguid-setup"

    def fake_generate_registration_options(
        *,
        rp_id,
        rp_name,
        user_name,
        user_id=None,
        user_display_name=None,
        challenge=None,
        timeout=60000,
        attestation=None,
        authenticator_selection=None,
        exclude_credentials=None,
        supported_pub_key_algs=None,
        hints=None,
    ):
        return DummyRegistrationOptions()

    monkeypatch.setattr(access_login, "generate_registration_options", fake_generate_registration_options)
    monkeypatch.setattr(
        access_login,
        "options_to_json",
        lambda options: json.dumps({"challenge": "register-challenge", "rpId": "localhost"}),
    )
    monkeypatch.setattr(
        access_login,
        "verify_registration_response",
        lambda **kwargs: DummyRegistrationVerification(),
    )

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, "EXISTINGSECRET"),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    options_response = client.post("/api/auth/passkeys/register/options", json={})
    assert options_response.status_code == 200
    options_payload = options_response.get_json()

    verify_response = client.post(
        "/api/auth/passkeys/register/verify",
        json={
            "request_id": options_payload["request_id"],
            "credential": {
                "id": access_login._bytes_to_base64url(DummyRegistrationVerification.credential_id),
                "response": {"transports": ["internal"]},
            },
            "label": "Laptop Passkey",
        },
    )
    assert verify_response.status_code == 200
    verify_payload = verify_response.get_json()
    assert verify_payload["status"] == "ok"
    assert verify_payload["username"] == "operator"
    assert verify_payload["passkey_count"] == 1

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT credential_id, public_key, sign_count, label FROM user_passkeys WHERE user_id=?",
            (2,),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == (
        access_login._bytes_to_base64url(DummyRegistrationVerification.credential_id),
        access_login._bytes_to_base64url(DummyRegistrationVerification.credential_public_key),
        4,
        "Laptop Passkey",
    )


def test_passkey_authentication_completes_primary_login(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _enable_fake_passkeys(monkeypatch)

    class DummyAuthenticationOptions:
        challenge = b"authenticate-challenge"

    class DummyAuthenticationVerification:
        new_sign_count = 9

    def fake_generate_authentication_options(
        *,
        rp_id,
        challenge=None,
        timeout=60000,
        allow_credentials=None,
        user_verification=None,
    ):
        return DummyAuthenticationOptions()

    monkeypatch.setattr(access_login, "generate_authentication_options", fake_generate_authentication_options)
    monkeypatch.setattr(
        access_login,
        "options_to_json",
        lambda options: json.dumps({"challenge": "authenticate-challenge", "rpId": "localhost"}),
    )
    monkeypatch.setattr(
        access_login,
        "verify_authentication_response",
        lambda **kwargs: DummyAuthenticationVerification(),
    )

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, None),
        )
        cur.execute(
            """
            INSERT INTO user_passkeys (
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "cred-login", "public-key-login", 5, "Desk Passkey", "[]", "", 1_700_000_000, 1_700_000_000),
        )
        conn.commit()
    finally:
        conn.close()

    client = engine_harness.app.test_client()
    options_response = client.post("/api/auth/passkeys/authenticate/options", json={})
    assert options_response.status_code == 200
    options_payload = options_response.get_json()

    verify_response = client.post(
        "/api/auth/passkeys/authenticate/verify",
        json={
            "request_id": options_payload["request_id"],
            "credential": {"id": "cred-login"},
        },
    )
    assert verify_response.status_code == 200
    verify_payload = verify_response.get_json()
    assert verify_payload["status"] == "ok"
    assert verify_payload["username"] == "operator"
    assert verify_payload["token"]

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT sign_count FROM user_passkeys WHERE credential_id=?", ("cred-login",))
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == (9,)


def test_passkey_authentication_recovers_legacy_stored_credential_format(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _enable_fake_passkeys(monkeypatch)
    monkeypatch.setattr(
        access_login,
        "base64url_to_bytes",
        lambda value: access_login.base64.urlsafe_b64decode(
            f"{value}{'=' * (-len(value) % 4)}".encode("ascii")
        ),
    )

    class DummyAuthenticationOptions:
        challenge = b"authenticate-challenge"

    class DummyAuthenticationVerification:
        new_sign_count = 11

    def fake_generate_authentication_options(
        *,
        rp_id,
        challenge=None,
        timeout=60000,
        allow_credentials=None,
        user_verification=None,
    ):
        return DummyAuthenticationOptions()

    monkeypatch.setattr(access_login, "generate_authentication_options", fake_generate_authentication_options)
    monkeypatch.setattr(
        access_login,
        "options_to_json",
        lambda options: json.dumps({"challenge": "authenticate-challenge", "rpId": "localhost"}),
    )
    monkeypatch.setattr(
        access_login,
        "verify_authentication_response",
        lambda **kwargs: DummyAuthenticationVerification(),
    )

    raw_credential_id = b"cred-login"
    raw_public_key = b"public-key-login"
    canonical_credential_id = access_login._bytes_to_base64url(raw_credential_id)
    canonical_public_key = access_login._bytes_to_base64url(raw_public_key)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, None),
        )
        cur.execute(
            """
            INSERT INTO user_passkeys (
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, str(raw_credential_id), str(raw_public_key), 5, "Desk Passkey", "[]", "", 1_700_000_000, 1_700_000_000),
        )
        conn.commit()
    finally:
        conn.close()

    client = engine_harness.app.test_client()
    options_response = client.post("/api/auth/passkeys/authenticate/options", json={})
    assert options_response.status_code == 200
    options_payload = options_response.get_json()

    verify_response = client.post(
        "/api/auth/passkeys/authenticate/verify",
        json={
            "request_id": options_payload["request_id"],
            "credential": {"id": canonical_credential_id},
        },
    )
    assert verify_response.status_code == 200
    verify_payload = verify_response.get_json()
    assert verify_payload["status"] == "ok"
    assert verify_payload["username"] == "operator"
    assert verify_payload["token"]

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT credential_id, public_key, sign_count FROM user_passkeys WHERE user_id=?",
            (2,),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == (canonical_credential_id, canonical_public_key, 11)


def test_current_user_can_list_enrolled_passkeys(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, None),
        )
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (3, "other", "Other User", _sha512_hex("other-password"), "User", 0, 0, 0, 1, 0, None),
        )
        cur.executemany(
            """
            INSERT INTO user_passkeys (
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            [
                (2, "cred-alpha", "public-alpha", 3, "Laptop", '["internal"]', "", 1_700_000_100, 1_700_000_200),
                (2, "cred-beta", "public-beta", 4, "Phone", '["hybrid"]', "", 1_700_000_300, 0),
                (3, "cred-other", "public-other", 1, "Other User Key", "[]", "", 1_700_000_400, 0),
            ],
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.get("/api/auth/passkeys")
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["passkey_count"] == 2
    assert [item["label"] for item in payload["passkeys"]] == ["Laptop", "Phone"]
    assert payload["passkeys"][0]["transports"] == ["internal"]
    assert payload["passkeys"][1]["transports"] == ["hybrid"]


def test_current_user_can_rename_enrolled_passkey(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, None),
        )
        cur.execute(
            """
            INSERT INTO user_passkeys (
                id,
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (11, 2, "cred-rename", "public-rename", 2, "Old Label", "[]", "", 1_700_000_500, 0),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.patch("/api/auth/passkeys/11", json={"label": "Desk YubiKey"})
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["passkey"]["label"] == "Desk YubiKey"
    assert payload["passkey_count"] == 1

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT label FROM user_passkeys WHERE id=11")
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == ("Desk YubiKey",)


def test_current_user_can_remove_enrolled_passkey(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, None),
        )
        cur.executemany(
            """
            INSERT INTO user_passkeys (
                id,
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            [
                (21, 2, "cred-remove", "public-remove", 2, "Old Laptop", "[]", "", 1_700_000_600, 0),
                (22, 2, "cred-keep", "public-keep", 2, "Phone", "[]", "", 1_700_000_700, 0),
            ],
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.delete("/api/auth/passkeys/21")
    assert response.status_code == 200

    payload = response.get_json()
    assert payload["status"] == "ok"
    assert payload["removed"] is True
    assert payload["passkey_count"] == 1

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT id, label FROM user_passkeys ORDER BY id ASC")
        rows = cur.fetchall()
    finally:
        conn.close()

    assert rows == [(22, "Phone")]


def test_reset_own_mfa_preserves_passkeys(engine_harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at,
                mfa_enabled,
                mfa_disabled,
                mfa_secret
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", _sha512_hex("operator-password"), "User", 0, 0, 0, 1, 0, "EXISTINGSECRET"),
        )
        cur.execute(
            """
            INSERT INTO user_passkeys (
                user_id,
                credential_id,
                public_key,
                sign_count,
                label,
                transports_json,
                aaguid,
                created_at,
                last_used_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "cred-reset", "public-key-reset", 2, "Desk Passkey", "[]", "", 1_700_000_000, 1_700_000_000),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.post("/api/auth/mfa/reset")
    assert response.status_code == 200

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT COUNT(*) FROM user_passkeys WHERE user_id=?", (2,))
        remaining = cur.fetchone()[0]
    finally:
        conn.close()

    assert remaining == 1


def test_user_can_reset_own_password_with_current_password(engine_harness: EngineTestHarness) -> None:
    original_hash = _sha512_hex("old-password")
    replacement_hash = _sha512_hex("new-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", original_hash, "User", 0, 0, 0),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.post(
        "/api/auth/password/reset",
        json={
            "current_password_sha512": original_hash,
            "new_password_sha512": replacement_hash,
        },
    )
    assert response.status_code == 200
    assert response.get_json()["status"] == "ok"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT password_sha512 FROM users WHERE LOWER(username)=LOWER(?)",
            ("operator",),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert (row[0] or "").lower() == replacement_hash


def test_user_password_reset_rejects_invalid_current_password(engine_harness: EngineTestHarness) -> None:
    original_hash = _sha512_hex("old-password")
    replacement_hash = _sha512_hex("new-password")
    invalid_hash = _sha512_hex("wrong-password")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (
                id,
                username,
                display_name,
                password_sha512,
                role,
                last_login,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator One", original_hash, "User", 0, 0, 0),
        )
        conn.commit()
    finally:
        conn.close()

    client = _user_client(engine_harness)
    response = client.post(
        "/api/auth/password/reset",
        json={
            "current_password_sha512": invalid_hash,
            "new_password_sha512": replacement_hash,
        },
    )
    assert response.status_code == 401
    assert response.get_json()["error"] == "invalid current password"


def test_user_password_reset_requires_login(engine_harness: EngineTestHarness) -> None:
    client = engine_harness.app.test_client()
    response = client.post(
        "/api/auth/password/reset",
        json={
            "current_password_sha512": _sha512_hex("old-password"),
            "new_password_sha512": _sha512_hex("new-password"),
        },
    )
    assert response.status_code == 401
