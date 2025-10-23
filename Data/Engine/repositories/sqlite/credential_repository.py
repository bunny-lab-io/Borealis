"""SQLite access for operator credential metadata."""

from __future__ import annotations

import json
import logging
import sqlite3
from contextlib import closing
from typing import Dict, List, Optional

from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory

__all__ = ["SQLiteCredentialRepository"]


class SQLiteCredentialRepository:
    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.credentials")

    def list_credentials(
        self,
        *,
        site_id: Optional[int] = None,
        connection_type: Optional[str] = None,
    ) -> List[Dict[str, object]]:
        sql = """
            SELECT c.id,
                   c.name,
                   c.description,
                   c.credential_type,
                   c.connection_type,
                   c.username,
                   c.site_id,
                   s.name AS site_name,
                   c.become_method,
                   c.become_username,
                   c.metadata_json,
                   c.created_at,
                   c.updated_at,
                   c.password_encrypted,
                   c.private_key_encrypted,
                   c.private_key_passphrase_encrypted,
                   c.become_password_encrypted
              FROM credentials c
         LEFT JOIN sites s ON s.id = c.site_id
        """
        clauses: List[str] = []
        params: List[object] = []
        if site_id is not None:
            clauses.append("c.site_id = ?")
            params.append(site_id)
        if connection_type:
            clauses.append("LOWER(c.connection_type) = LOWER(?)")
            params.append(connection_type)
        if clauses:
            sql += " WHERE " + " AND ".join(clauses)
        sql += " ORDER BY LOWER(c.name) ASC"

        with closing(self._connections()) as conn:
            conn.row_factory = sqlite3.Row  # type: ignore[attr-defined]
            cur = conn.cursor()
            cur.execute(sql, params)
            rows = cur.fetchall()

        results: List[Dict[str, object]] = []
        for row in rows:
            metadata_json = row["metadata_json"] if "metadata_json" in row.keys() else None
            metadata = {}
            if metadata_json:
                try:
                    candidate = json.loads(metadata_json)
                    if isinstance(candidate, dict):
                        metadata = candidate
                except Exception:
                    metadata = {}
            results.append(
                {
                    "id": row["id"],
                    "name": row["name"],
                    "description": row["description"] or "",
                    "credential_type": row["credential_type"] or "machine",
                    "connection_type": row["connection_type"] or "ssh",
                    "site_id": row["site_id"],
                    "site_name": row["site_name"],
                    "username": row["username"] or "",
                    "become_method": row["become_method"] or "",
                    "become_username": row["become_username"] or "",
                    "metadata": metadata,
                    "created_at": int(row["created_at"] or 0),
                    "updated_at": int(row["updated_at"] or 0),
                    "has_password": bool(row["password_encrypted"]),
                    "has_private_key": bool(row["private_key_encrypted"]),
                    "has_private_key_passphrase": bool(row["private_key_passphrase_encrypted"]),
                    "has_become_password": bool(row["become_password_encrypted"]),
                }
            )
        return results
