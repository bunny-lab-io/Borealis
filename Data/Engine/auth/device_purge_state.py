# ======================================================
# Data\Engine\auth\device_purge_state.py
# Description: Shared helpers for persisted device-purge auth barriers.
#
# API Endpoints (if applicable): None
# ======================================================

"""Persist and query auth barriers that block stale device credentials after purge."""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, Dict, Optional

from Data.Engine.db import dbapi as sqlite3

from .guid_utils import normalize_guid


TABLE_NAME = "device_purge_barriers"


def _iso_now() -> str:
    return datetime.now(tz=timezone.utc).isoformat()


def ensure_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            f"""
            CREATE TABLE IF NOT EXISTS {TABLE_NAME} (
                guid TEXT PRIMARY KEY,
                required_token_version INTEGER NOT NULL,
                purged_at TEXT NOT NULL,
                purged_by TEXT,
                last_hostname TEXT,
                last_agent_id TEXT
            )
            """
        )
        cur.execute(
            f"""
            CREATE INDEX IF NOT EXISTS idx_{TABLE_NAME}_required_token_version
                ON {TABLE_NAME}(required_token_version)
            """
        )
    finally:
        cur.close()


def get_barrier(cur: sqlite3.Cursor, guid: str) -> Optional[Dict[str, Any]]:
    normalized_guid = normalize_guid(guid)
    if not normalized_guid:
        return None
    cur.execute(
        f"""
        SELECT guid, required_token_version, purged_at, purged_by, last_hostname, last_agent_id
          FROM {TABLE_NAME}
         WHERE UPPER(guid) = ?
        """,
        (normalized_guid,),
    )
    row = cur.fetchone()
    if not row:
        return None
    return {
        "guid": normalize_guid(row[0]),
        "required_token_version": int(row[1] or 1),
        "purged_at": row[2] or "",
        "purged_by": row[3] or "",
        "last_hostname": row[4] or "",
        "last_agent_id": row[5] or "",
    }


def get_required_token_version(cur: sqlite3.Cursor, guid: str) -> Optional[int]:
    barrier = get_barrier(cur, guid)
    if not barrier:
        return None
    try:
        return max(1, int(barrier.get("required_token_version") or 1))
    except Exception:
        return 1


def upsert_barrier(
    cur: sqlite3.Cursor,
    *,
    guid: str,
    required_token_version: int,
    purged_by: Optional[str] = None,
    purged_at: Optional[str] = None,
    last_hostname: Optional[str] = None,
    last_agent_id: Optional[str] = None,
) -> Dict[str, Any]:
    normalized_guid = normalize_guid(guid)
    if not normalized_guid:
        raise ValueError("valid guid is required")
    previous = get_barrier(cur, normalized_guid)
    next_required = max(
        1,
        int(required_token_version or 1),
        int(previous.get("required_token_version") or 1) if previous else 1,
    )
    purged_at_value = (purged_at or "").strip() or _iso_now()
    purged_by_value = (purged_by or "").strip() or (previous.get("purged_by") if previous else "") or ""
    last_hostname_value = (
        (last_hostname or "").strip()
        or (previous.get("last_hostname") if previous else "")
        or ""
    )
    last_agent_id_value = (
        (last_agent_id or "").strip()
        or (previous.get("last_agent_id") if previous else "")
        or ""
    )
    cur.execute(
        f"""
        INSERT INTO {TABLE_NAME} (
            guid,
            required_token_version,
            purged_at,
            purged_by,
            last_hostname,
            last_agent_id
        )
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(guid) DO UPDATE SET
            required_token_version = excluded.required_token_version,
            purged_at = excluded.purged_at,
            purged_by = excluded.purged_by,
            last_hostname = excluded.last_hostname,
            last_agent_id = excluded.last_agent_id
        """,
        (
            normalized_guid,
            next_required,
            purged_at_value,
            purged_by_value,
            last_hostname_value,
            last_agent_id_value,
        ),
    )
    return {
        "guid": normalized_guid,
        "required_token_version": next_required,
        "purged_at": purged_at_value,
        "purged_by": purged_by_value,
        "last_hostname": last_hostname_value,
        "last_agent_id": last_agent_id_value,
    }


def clear_barrier(cur: sqlite3.Cursor, guid: str) -> int:
    normalized_guid = normalize_guid(guid)
    if not normalized_guid:
        return 0
    cur.execute(
        f"DELETE FROM {TABLE_NAME} WHERE UPPER(guid) = ?",
        (normalized_guid,),
    )
    return int(cur.rowcount or 0)


__all__ = [
    "TABLE_NAME",
    "clear_barrier",
    "ensure_table",
    "get_barrier",
    "get_required_token_version",
    "upsert_barrier",
]
