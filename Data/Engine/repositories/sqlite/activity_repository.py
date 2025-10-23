"""Persistence helpers for device activity history."""

from __future__ import annotations

import logging
from contextlib import closing
from typing import Any, Dict, List, Optional

from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory

__all__ = ["SQLiteDeviceActivityRepository"]


class SQLiteDeviceActivityRepository:
    """Interact with the ``activity_history`` table."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.activity")

    def fetch_for_hostname(self, hostname: str) -> List[Dict[str, Any]]:
        sql = (
            "SELECT id, script_name, script_path, script_type, ran_at, status, "
            "LENGTH(COALESCE(stdout, '')), LENGTH(COALESCE(stderr, '')) "
            "FROM activity_history WHERE LOWER(hostname) = LOWER(?) "
            "ORDER BY ran_at DESC, id DESC"
        )
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, (hostname,))
            rows = cur.fetchall()

        history: List[Dict[str, Any]] = []
        for row in rows:
            try:
                (
                    activity_id,
                    script_name,
                    script_path,
                    script_type,
                    ran_at,
                    status,
                    stdout_len,
                    stderr_len,
                ) = row
            except Exception:  # pragma: no cover - defensive
                self._log.debug("malformed activity_history row encountered: %r", row)
                continue

            history.append(
                {
                    "id": activity_id,
                    "script_name": script_name,
                    "script_path": script_path,
                    "script_type": script_type,
                    "ran_at": ran_at,
                    "status": status,
                    "has_stdout": bool(stdout_len),
                    "has_stderr": bool(stderr_len),
                }
            )

        return history

    def delete_for_hostname(self, hostname: str) -> int:
        sql = "DELETE FROM activity_history WHERE LOWER(hostname) = LOWER(?)"
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, (hostname,))
            conn.commit()
            return cur.rowcount

    def fetch_by_id(self, activity_id: int) -> Optional[Dict[str, Any]]:
        sql = (
            "SELECT id, hostname, script_name, script_path, script_type, ran_at, status, stdout, stderr "
            "FROM activity_history WHERE id = ?"
        )
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, (activity_id,))
            row = cur.fetchone()

        if not row:
            return None

        (
            activity_id,
            hostname,
            script_name,
            script_path,
            script_type,
            ran_at,
            status,
            stdout,
            stderr,
        ) = row

        return {
            "id": activity_id,
            "hostname": hostname,
            "script_name": script_name,
            "script_path": script_path,
            "script_type": script_type,
            "ran_at": ran_at,
            "status": status,
            "stdout": stdout or "",
            "stderr": stderr or "",
        }
