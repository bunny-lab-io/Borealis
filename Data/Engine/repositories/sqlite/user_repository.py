"""SQLite repository for operator accounts."""

from __future__ import annotations

import logging
import sqlite3
from dataclasses import dataclass
from typing import Iterable, Optional

from Data.Engine.domain import OperatorAccount

from .connection import SQLiteConnectionFactory


@dataclass(frozen=True, slots=True)
class _UserRow:
    id: str
    username: str
    display_name: str
    password_sha512: str
    role: str
    last_login: int
    created_at: int
    updated_at: int
    mfa_enabled: int
    mfa_secret: str


class SQLiteUserRepository:
    """Expose CRUD helpers for operator accounts stored in SQLite."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connection_factory = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.users")

    def fetch_by_username(self, username: str) -> Optional[OperatorAccount]:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    username,
                    display_name,
                    COALESCE(password_sha512, '') as password_sha512,
                    COALESCE(role, 'User') as role,
                    COALESCE(last_login, 0) as last_login,
                    COALESCE(created_at, 0) as created_at,
                    COALESCE(updated_at, 0) as updated_at,
                    COALESCE(mfa_enabled, 0) as mfa_enabled,
                    COALESCE(mfa_secret, '') as mfa_secret
                FROM users
                WHERE LOWER(username) = LOWER(?)
                """,
                (username,),
            )
            row = cur.fetchone()
            if not row:
                return None
            record = _UserRow(*row)
            return _row_to_account(record)
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to load user %s: %s", username, exc)
            return None
        finally:
            conn.close()

    def resolve_identifier(self, username: str) -> Optional[str]:
        normalized = (username or "").strip()
        if not normalized:
            return None

        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT id FROM users WHERE LOWER(username) = LOWER(?)",
                (normalized,),
            )
            row = cur.fetchone()
            if not row:
                return None
            return str(row[0]) if row[0] is not None else None
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to resolve identifier for %s: %s", username, exc)
            return None
        finally:
            conn.close()

    def username_for_identifier(self, identifier: str) -> Optional[str]:
        token = (identifier or "").strip()
        if not token:
            return None

        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT username
                  FROM users
                 WHERE CAST(id AS TEXT) = ?
                    OR LOWER(username) = LOWER(?)
                 LIMIT 1
                """,
                (token, token),
            )
            row = cur.fetchone()
            if not row:
                return None
            username = str(row[0] or "").strip()
            return username or None
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to resolve username for %s: %s", identifier, exc)
            return None
        finally:
            conn.close()

    def list_accounts(self) -> list[OperatorAccount]:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    username,
                    display_name,
                    COALESCE(password_sha512, '') as password_sha512,
                    COALESCE(role, 'User') as role,
                    COALESCE(last_login, 0) as last_login,
                    COALESCE(created_at, 0) as created_at,
                    COALESCE(updated_at, 0) as updated_at,
                    COALESCE(mfa_enabled, 0) as mfa_enabled,
                    COALESCE(mfa_secret, '') as mfa_secret
                FROM users
                ORDER BY LOWER(username) ASC
                """
            )
            rows = [_UserRow(*row) for row in cur.fetchall()]
            return [_row_to_account(row) for row in rows]
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to enumerate users: %s", exc)
            return []
        finally:
            conn.close()

    def create_account(
        self,
        *,
        username: str,
        display_name: str,
        password_sha512: str,
        role: str,
        timestamp: int,
    ) -> None:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO users (
                    username,
                    display_name,
                    password_sha512,
                    role,
                    created_at,
                    updated_at
                ) VALUES (?, ?, ?, ?, ?, ?)
                """,
                (username, display_name, password_sha512, role, timestamp, timestamp),
            )
            conn.commit()
        finally:
            conn.close()

    def delete_account(self, username: str) -> bool:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute("DELETE FROM users WHERE LOWER(username) = LOWER(?)", (username,))
            deleted = cur.rowcount > 0
            conn.commit()
            return deleted
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to delete user %s: %s", username, exc)
            return False
        finally:
            conn.close()

    def update_password(self, username: str, password_sha512: str, *, timestamp: int) -> bool:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE users
                   SET password_sha512 = ?,
                       updated_at = ?
                 WHERE LOWER(username) = LOWER(?)
                """,
                (password_sha512, timestamp, username),
            )
            conn.commit()
            return cur.rowcount > 0
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to update password for %s: %s", username, exc)
            return False
        finally:
            conn.close()

    def update_role(self, username: str, role: str, *, timestamp: int) -> bool:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE users
                   SET role = ?,
                       updated_at = ?
                 WHERE LOWER(username) = LOWER(?)
                """,
                (role, timestamp, username),
            )
            conn.commit()
            return cur.rowcount > 0
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to update role for %s: %s", username, exc)
            return False
        finally:
            conn.close()

    def update_mfa(
        self,
        username: str,
        *,
        enabled: bool,
        reset_secret: bool,
        timestamp: int,
    ) -> bool:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            secret_clause = "mfa_secret = NULL" if reset_secret else None
            assignments: list[str] = ["mfa_enabled = ?", "updated_at = ?"]
            params: list[object] = [1 if enabled else 0, timestamp]
            if secret_clause is not None:
                assignments.append(secret_clause)
            query = "UPDATE users SET " + ", ".join(assignments) + " WHERE LOWER(username) = LOWER(?)"
            params.append(username)
            cur.execute(query, tuple(params))
            conn.commit()
            return cur.rowcount > 0
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to update MFA for %s: %s", username, exc)
            return False
        finally:
            conn.close()

    def count_accounts(self) -> int:
        return self._scalar("SELECT COUNT(*) FROM users", ())

    def count_admins(self) -> int:
        return self._scalar("SELECT COUNT(*) FROM users WHERE LOWER(role) = 'admin'", ())

    def _scalar(self, query: str, params: Iterable[object]) -> int:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(query, tuple(params))
            row = cur.fetchone()
            if not row:
                return 0
            return int(row[0] or 0)
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("scalar query failed: %s", exc)
            return 0
        finally:
            conn.close()

    def update_last_login(self, username: str, timestamp: int) -> None:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE users
                   SET last_login = ?,
                       updated_at = ?
                 WHERE LOWER(username) = LOWER(?)
                """,
                (timestamp, timestamp, username),
            )
            conn.commit()
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.warning("failed to update last_login for %s: %s", username, exc)
        finally:
            conn.close()

    def store_mfa_secret(self, username: str, secret: str, *, timestamp: int) -> None:
        conn = self._connection_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE users
                   SET mfa_secret = ?,
                       updated_at = ?
                 WHERE LOWER(username) = LOWER(?)
                """,
                (secret, timestamp, username),
            )
            conn.commit()
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.warning("failed to persist MFA secret for %s: %s", username, exc)
        finally:
            conn.close()


__all__ = ["SQLiteUserRepository"]


def _row_to_account(record: _UserRow) -> OperatorAccount:
    return OperatorAccount(
        username=record.username,
        display_name=record.display_name or record.username,
        password_sha512=(record.password_sha512 or "").lower(),
        role=record.role or "User",
        last_login=int(record.last_login or 0),
        created_at=int(record.created_at or 0),
        updated_at=int(record.updated_at or 0),
        mfa_enabled=bool(record.mfa_enabled),
        mfa_secret=(record.mfa_secret or "") or None,
    )
