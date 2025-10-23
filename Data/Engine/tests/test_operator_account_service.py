"""Tests for the operator account management service."""

from __future__ import annotations

import hashlib
import sqlite3
from pathlib import Path
from typing import Callable

import pytest

pytest.importorskip("jwt")

from Data.Engine.repositories.sqlite.connection import connection_factory
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository
from Data.Engine.services.auth.operator_account_service import (
    AccountNotFoundError,
    CannotModifySelfError,
    InvalidPasswordHashError,
    InvalidRoleError,
    LastAdminError,
    LastUserError,
    OperatorAccountService,
    UsernameAlreadyExistsError,
)


def _prepare_db(path: Path) -> Callable[[], sqlite3.Connection]:
    conn = sqlite3.connect(path)
    conn.execute(
        """
        CREATE TABLE users (
            id TEXT PRIMARY KEY,
            username TEXT UNIQUE,
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


def _service(factory: Callable[[], sqlite3.Connection]) -> OperatorAccountService:
    repo = SQLiteUserRepository(factory)
    return OperatorAccountService(repo)


def test_list_accounts_returns_users(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    password_hash = hashlib.sha512(b"password").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)

    service = _service(factory)
    records = service.list_accounts()

    assert len(records) == 1
    assert records[0].username == "admin"
    assert records[0].role == "Admin"


def test_create_account_enforces_uniqueness(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    service = _service(factory)
    password_hash = hashlib.sha512(b"pw").hexdigest()

    service.create_account(username="admin", password_sha512=password_hash, role="Admin")

    with pytest.raises(UsernameAlreadyExistsError):
        service.create_account(username="admin", password_sha512=password_hash, role="Admin")


def test_create_account_validates_password_hash(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    service = _service(factory)

    with pytest.raises(InvalidPasswordHashError):
        service.create_account(username="user", password_sha512="abc", role="User")


def test_delete_account_protects_last_user(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    password_hash = hashlib.sha512(b"pw").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)

    service = _service(factory)

    with pytest.raises(LastUserError):
        service.delete_account("admin")


def test_delete_account_prevents_self_deletion(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    password_hash = hashlib.sha512(b"pw").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)
    _insert_user(factory, user_id="2", username="user", password_hash=password_hash, role="User")

    service = _service(factory)

    with pytest.raises(CannotModifySelfError):
        service.delete_account("admin", actor="admin")


def test_delete_account_prevents_last_admin_removal(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    password_hash = hashlib.sha512(b"pw").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)
    _insert_user(factory, user_id="2", username="user", password_hash=password_hash, role="User")

    service = _service(factory)

    with pytest.raises(LastAdminError):
        service.delete_account("admin")


def test_change_role_demotes_only_when_valid(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    password_hash = hashlib.sha512(b"pw").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)
    _insert_user(factory, user_id="2", username="backup", password_hash=password_hash)

    service = _service(factory)
    service.change_role("backup", "User")

    with pytest.raises(LastAdminError):
        service.change_role("admin", "User")

    with pytest.raises(InvalidRoleError):
        service.change_role("admin", "invalid")


def test_reset_password_validates_hash(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    password_hash = hashlib.sha512(b"pw").hexdigest()
    _insert_user(factory, user_id="1", username="admin", password_hash=password_hash)

    service = _service(factory)

    with pytest.raises(InvalidPasswordHashError):
        service.reset_password("admin", "abc")

    new_hash = hashlib.sha512(b"new").hexdigest()
    service.reset_password("admin", new_hash)


def test_update_mfa_raises_for_unknown_user(tmp_path):
    db = tmp_path / "users.db"
    factory = _prepare_db(db)
    service = _service(factory)

    with pytest.raises(AccountNotFoundError):
        service.update_mfa("missing", enabled=True, reset_secret=False)
