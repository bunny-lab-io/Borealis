"""SQLite repository for operator accounts."""

from __future__ import annotations

import logging
import sqlite3
from dataclasses import dataclass
from typing import Optional

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
        except sqlite3.Error as exc:  # pragma: no cover - defensive
            self._log.error("failed to load user %s: %s", username, exc)
            return None
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
