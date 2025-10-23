"""Operator account management service."""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass
from typing import Optional

from Data.Engine.domain import OperatorAccount
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository


class OperatorAccountError(Exception):
    """Base class for operator account management failures."""


class UsernameAlreadyExistsError(OperatorAccountError):
    """Raised when attempting to create an operator with a duplicate username."""


class AccountNotFoundError(OperatorAccountError):
    """Raised when the requested operator account cannot be located."""


class LastAdminError(OperatorAccountError):
    """Raised when attempting to demote or delete the last remaining admin."""


class LastUserError(OperatorAccountError):
    """Raised when attempting to delete the final operator account."""


class CannotModifySelfError(OperatorAccountError):
    """Raised when the caller attempts to delete themselves."""


class InvalidRoleError(OperatorAccountError):
    """Raised when a role value is invalid."""


class InvalidPasswordHashError(OperatorAccountError):
    """Raised when a password hash is malformed."""


@dataclass(frozen=True, slots=True)
class OperatorAccountRecord:
    username: str
    display_name: str
    role: str
    last_login: int
    created_at: int
    updated_at: int
    mfa_enabled: bool


class OperatorAccountService:
    """High-level operations for managing operator accounts."""

    def __init__(
        self,
        repository: SQLiteUserRepository,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._repository = repository
        self._log = logger or logging.getLogger("borealis.engine.services.operator_accounts")

    def list_accounts(self) -> list[OperatorAccountRecord]:
        return [_to_record(account) for account in self._repository.list_accounts()]

    def create_account(
        self,
        *,
        username: str,
        password_sha512: str,
        role: str,
        display_name: Optional[str] = None,
    ) -> OperatorAccountRecord:
        normalized_role = self._normalize_role(role)
        username = (username or "").strip()
        password_sha512 = (password_sha512 or "").strip().lower()
        display_name = (display_name or username or "").strip()

        if not username or not password_sha512:
            raise InvalidPasswordHashError("username and password are required")
        if len(password_sha512) != 128:
            raise InvalidPasswordHashError("password hash must be 128 hex characters")

        now = int(time.time())
        try:
            self._repository.create_account(
                username=username,
                display_name=display_name or username,
                password_sha512=password_sha512,
                role=normalized_role,
                timestamp=now,
            )
        except Exception as exc:  # pragma: no cover - sqlite integrity errors are deterministic
            import sqlite3

            if isinstance(exc, sqlite3.IntegrityError):
                raise UsernameAlreadyExistsError("username already exists") from exc
            raise

        account = self._repository.fetch_by_username(username)
        if not account:  # pragma: no cover - sanity guard
            raise AccountNotFoundError("account creation failed")
        return _to_record(account)

    def delete_account(self, username: str, *, actor: Optional[str] = None) -> None:
        username = (username or "").strip()
        if not username:
            raise AccountNotFoundError("invalid username")

        if actor and actor.strip().lower() == username.lower():
            raise CannotModifySelfError("cannot delete yourself")

        total_accounts = self._repository.count_accounts()
        if total_accounts <= 1:
            raise LastUserError("cannot delete the last user")

        target = self._repository.fetch_by_username(username)
        if not target:
            raise AccountNotFoundError("user not found")

        if target.role.lower() == "admin" and self._repository.count_admins() <= 1:
            raise LastAdminError("cannot delete the last admin")

        if not self._repository.delete_account(username):
            raise AccountNotFoundError("user not found")

    def reset_password(self, username: str, password_sha512: str) -> None:
        username = (username or "").strip()
        password_sha512 = (password_sha512 or "").strip().lower()
        if len(password_sha512) != 128:
            raise InvalidPasswordHashError("invalid password hash")

        now = int(time.time())
        if not self._repository.update_password(username, password_sha512, timestamp=now):
            raise AccountNotFoundError("user not found")

    def change_role(self, username: str, role: str, *, actor: Optional[str] = None) -> OperatorAccountRecord:
        username = (username or "").strip()
        normalized_role = self._normalize_role(role)

        account = self._repository.fetch_by_username(username)
        if not account:
            raise AccountNotFoundError("user not found")

        if account.role.lower() == "admin" and normalized_role.lower() != "admin":
            if self._repository.count_admins() <= 1:
                raise LastAdminError("cannot demote the last admin")

        now = int(time.time())
        if not self._repository.update_role(username, normalized_role, timestamp=now):
            raise AccountNotFoundError("user not found")

        updated = self._repository.fetch_by_username(username)
        if not updated:  # pragma: no cover - guard
            raise AccountNotFoundError("user not found")

        record = _to_record(updated)
        if actor and actor.strip().lower() == username.lower():
            self._log.info("actor-role-updated", extra={"username": username, "role": record.role})
        return record

    def update_mfa(self, username: str, *, enabled: bool, reset_secret: bool) -> None:
        username = (username or "").strip()
        if not username:
            raise AccountNotFoundError("invalid username")

        now = int(time.time())
        if not self._repository.update_mfa(username, enabled=enabled, reset_secret=reset_secret, timestamp=now):
            raise AccountNotFoundError("user not found")

    def fetch_account(self, username: str) -> Optional[OperatorAccountRecord]:
        account = self._repository.fetch_by_username(username)
        return _to_record(account) if account else None

    def _normalize_role(self, role: str) -> str:
        normalized = (role or "").strip().title() or "User"
        if normalized not in {"User", "Admin"}:
            raise InvalidRoleError("invalid role")
        return normalized


def _to_record(account: OperatorAccount) -> OperatorAccountRecord:
    return OperatorAccountRecord(
        username=account.username,
        display_name=account.display_name or account.username,
        role=account.role or "User",
        last_login=int(account.last_login or 0),
        created_at=int(account.created_at or 0),
        updated_at=int(account.updated_at or 0),
        mfa_enabled=bool(account.mfa_enabled),
    )


__all__ = [
    "OperatorAccountService",
    "OperatorAccountError",
    "UsernameAlreadyExistsError",
    "AccountNotFoundError",
    "LastAdminError",
    "LastUserError",
    "CannotModifySelfError",
    "InvalidRoleError",
    "InvalidPasswordHashError",
    "OperatorAccountRecord",
]
