"""Tests for the operator authentication service."""

from __future__ import annotations

import hashlib
import sqlite3
from pathlib import Path
from typing import Callable

import pytest

pyotp = pytest.importorskip("pyotp")

from Data.Engine.builders import (
    OperatorLoginRequest,
    OperatorMFAVerificationRequest,
)
from Data.Engine.repositories.sqlite.connection import connection_factory
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository
from Data.Engine.services.auth.operator_auth_service import (
    InvalidCredentialsError,
    InvalidMFACodeError,
    OperatorAuthService,
)


def _prepare_db(path: Path) -> Callable[[], sqlite3.Connection]:
    conn = sqlite3.connect(path)
    conn.execute(
        """
        CREATE TABLE users (
            id TEXT PRIMARY KEY,
            username TEXT,
            display_name TEXT,
            password_sha512 TEXT,
            role TEXT,
            last_login INTEGER,
            created_at INTEGER,
            updated_at INTEGER,
            mfa_enabled INTEGER,
            mfa_secret TEXT
        )
        """
    )
    conn.commit()
    conn.close()
    return connection_factory(path)


def _insert_user(
    factory: Callable[[], sqlite3.Connection],
    *,
    user_id: str,
    username: str,
    password_hash: str,
    role: str = "Admin",
    mfa_enabled: int = 0,
    mfa_secret: str = "",
) -> None:
    conn = factory()
    conn.execute(
        """
        INSERT INTO users (
            id, username, display_name, password_sha512, role,
            last_login, created_at, updated_at, mfa_enabled, mfa_secret
        ) VALUES (?, ?, ?, ?, ?, 0, 0, 0, ?, ?)
        """,
        (user_id, username, username, password_hash, role, mfa_enabled, mfa_secret),
    )
    conn.commit()
    conn.close()


def test_authenticate_success_updates_last_login(tmp_path):
    db_path = tmp_path / "auth.db"
    factory = _prepare_db(db_path)
    password_hash = hashlib.sha512(b"password").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)

    repo = SQLiteUserRepository(factory)
    service = OperatorAuthService(repo)

    request = OperatorLoginRequest(username="admin", password_sha512=password_hash)
    result = service.authenticate(request)

    assert result.username == "admin"

    conn = factory()
    row = conn.execute("SELECT last_login FROM users WHERE username=?", ("admin",)).fetchone()
    conn.close()
    assert row[0] > 0


def test_authenticate_invalid_credentials(tmp_path):
    db_path = tmp_path / "auth.db"
    factory = _prepare_db(db_path)
    repo = SQLiteUserRepository(factory)
    service = OperatorAuthService(repo)

    request = OperatorLoginRequest(username="missing", password_sha512="abc")
    with pytest.raises(InvalidCredentialsError):
        service.authenticate(request)


def test_mfa_verify_flow(tmp_path):
    db_path = tmp_path / "auth.db"
    factory = _prepare_db(db_path)
    secret = pyotp.random_base32()
    password_hash = hashlib.sha512(b"password").hexdigest()
    _insert_user(
        factory,
        user_id="1",
        username="admin",
        password_hash=password_hash,
        mfa_enabled=1,
        mfa_secret=secret,
    )

    repo = SQLiteUserRepository(factory)
    service = OperatorAuthService(repo)
    login_request = OperatorLoginRequest(username="admin", password_sha512=password_hash)

    challenge = service.authenticate(login_request)
    assert challenge.stage == "verify"

    totp = pyotp.TOTP(secret)
    verify_request = OperatorMFAVerificationRequest(
        pending_token=challenge.pending_token,
        code=totp.now(),
    )

    result = service.verify_mfa(challenge, verify_request)
    assert result.username == "admin"


def test_mfa_setup_flow_persists_secret(tmp_path):
    db_path = tmp_path / "auth.db"
    factory = _prepare_db(db_path)
    password_hash = hashlib.sha512(b"password").hexdigest()
    _insert_user(
        factory,
        user_id="1",
        username="admin",
        password_hash=password_hash,
        mfa_enabled=1,
        mfa_secret="",
    )

    repo = SQLiteUserRepository(factory)
    service = OperatorAuthService(repo)

    challenge = service.authenticate(OperatorLoginRequest(username="admin", password_sha512=password_hash))
    assert challenge.stage == "setup"
    assert challenge.secret

    totp = pyotp.TOTP(challenge.secret)
    verify_request = OperatorMFAVerificationRequest(
        pending_token=challenge.pending_token,
        code=totp.now(),
    )

    result = service.verify_mfa(challenge, verify_request)
    assert result.username == "admin"

    conn = factory()
    stored_secret = conn.execute(
        "SELECT mfa_secret FROM users WHERE username=?", ("admin",)
    ).fetchone()[0]
    conn.close()
    assert stored_secret


def test_mfa_invalid_code_raises(tmp_path):
    db_path = tmp_path / "auth.db"
    factory = _prepare_db(db_path)
    secret = pyotp.random_base32()
    password_hash = hashlib.sha512(b"password").hexdigest()
    _insert_user(
        factory,
        user_id="1",
        username="admin",
        password_hash=password_hash,
        mfa_enabled=1,
        mfa_secret=secret,
    )

    repo = SQLiteUserRepository(factory)
    service = OperatorAuthService(repo)
    challenge = service.authenticate(OperatorLoginRequest(username="admin", password_sha512=password_hash))

    verify_request = OperatorMFAVerificationRequest(
        pending_token=challenge.pending_token,
        code="000000",
    )

    with pytest.raises(InvalidMFACodeError):
        service.verify_mfa(challenge, verify_request)
