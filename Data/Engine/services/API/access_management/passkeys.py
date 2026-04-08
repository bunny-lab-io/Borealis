"""Helpers for storing Borealis operator passkeys."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any, Optional, Sequence

from Data.Engine.db import dbapi as sqlite3


@dataclass(frozen=True)
class StoredPasskey:
    id: int
    user_id: int
    credential_id: str
    public_key: str
    sign_count: int
    label: str
    transports_json: str
    aaguid: str
    created_at: int
    last_used_at: int

    @property
    def transports(self) -> list[str]:
        try:
            parsed = json.loads(self.transports_json or "[]")
        except Exception:
            return []
        if not isinstance(parsed, list):
            return []
        return [str(item).strip() for item in parsed if str(item).strip()]


def build_webauthn_user_id(user_id: Any, username: str) -> bytes:
    value = f"{int(user_id or 0)}:{str(username or '').strip().lower()}"
    return hashlib.sha256(value.encode("utf-8")).digest()[:32]


def serialize_transports(transports: Any) -> str:
    if not transports:
        return "[]"
    if isinstance(transports, str):
        transports = [transports]
    normalized = [str(item).strip() for item in transports if str(item).strip()]
    return json.dumps(normalized)


def count_user_passkeys(conn: sqlite3.Connection, username: str) -> int:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            SELECT COUNT(*)
            FROM user_passkeys up
            JOIN users u ON u.id = up.user_id
            WHERE LOWER(u.username)=LOWER(?)
            """,
            (username,),
        )
        row = cur.fetchone()
        return int((row or [0])[0] or 0)
    finally:
        cur.close()


def list_user_passkeys(conn: sqlite3.Connection, username: str) -> list[StoredPasskey]:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            SELECT
                up.id,
                up.user_id,
                COALESCE(up.credential_id, ''),
                COALESCE(up.public_key, ''),
                COALESCE(up.sign_count, 0),
                COALESCE(up.label, ''),
                COALESCE(up.transports_json, '[]'),
                COALESCE(up.aaguid, ''),
                COALESCE(up.created_at, 0),
                COALESCE(up.last_used_at, 0)
            FROM user_passkeys up
            JOIN users u ON u.id = up.user_id
            WHERE LOWER(u.username)=LOWER(?)
            ORDER BY up.created_at ASC, up.id ASC
            """,
            (username,),
        )
        rows = cur.fetchall()
    finally:
        cur.close()
    return [_row_to_passkey(row) for row in rows]


def get_user_passkey_by_credential_id(
    conn: sqlite3.Connection,
    username: str,
    credential_id: str,
) -> Optional[StoredPasskey]:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            SELECT
                up.id,
                up.user_id,
                COALESCE(up.credential_id, ''),
                COALESCE(up.public_key, ''),
                COALESCE(up.sign_count, 0),
                COALESCE(up.label, ''),
                COALESCE(up.transports_json, '[]'),
                COALESCE(up.aaguid, ''),
                COALESCE(up.created_at, 0),
                COALESCE(up.last_used_at, 0)
            FROM user_passkeys up
            JOIN users u ON u.id = up.user_id
            WHERE LOWER(u.username)=LOWER(?) AND up.credential_id=?
            LIMIT 1
            """,
            (username, credential_id),
        )
        row = cur.fetchone()
    finally:
        cur.close()
    if not row:
        return None
    return _row_to_passkey(row)


def delete_user_passkeys(conn: sqlite3.Connection, username: str) -> int:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            DELETE FROM user_passkeys
            WHERE user_id IN (
                SELECT id FROM users WHERE LOWER(username)=LOWER(?)
            )
            """,
            (username,),
        )
        return int(cur.rowcount or 0)
    finally:
        cur.close()


def _row_to_passkey(row: Sequence[Any]) -> StoredPasskey:
    return StoredPasskey(
        id=int(row[0] or 0),
        user_id=int(row[1] or 0),
        credential_id=str(row[2] or ""),
        public_key=str(row[3] or ""),
        sign_count=int(row[4] or 0),
        label=str(row[5] or ""),
        transports_json=str(row[6] or "[]"),
        aaguid=str(row[7] or ""),
        created_at=int(row[8] or 0),
        last_used_at=int(row[9] or 0),
    )

