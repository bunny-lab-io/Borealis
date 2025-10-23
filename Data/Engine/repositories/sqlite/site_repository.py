"""SQLite persistence for site management."""

from __future__ import annotations

import logging
import sqlite3
import time
from contextlib import closing
from typing import Dict, Iterable, List, Optional, Sequence

from Data.Engine.domain.sites import SiteDeviceMapping, SiteSummary
from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory

__all__ = ["SQLiteSiteRepository"]


class SQLiteSiteRepository:
    """Repository exposing site CRUD and device assignment helpers."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.sites")

    def list_sites(self) -> List[SiteSummary]:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT s.id, s.name, s.description, s.created_at,
                       COALESCE(ds.cnt, 0) AS device_count
                  FROM sites s
             LEFT JOIN (
                        SELECT site_id, COUNT(*) AS cnt
                          FROM device_sites
                      GROUP BY site_id
                    ) ds
                    ON ds.site_id = s.id
              ORDER BY LOWER(s.name) ASC
                """
            )
            rows = cur.fetchall()
        return [self._row_to_site(row) for row in rows]

    def create_site(self, name: str, description: str) -> SiteSummary:
        now = int(time.time())
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            try:
                cur.execute(
                    "INSERT INTO sites(name, description, created_at) VALUES (?, ?, ?)",
                    (name, description, now),
                )
            except sqlite3.IntegrityError as exc:
                raise ValueError("duplicate") from exc
            site_id = cur.lastrowid
            conn.commit()

            cur.execute(
                "SELECT id, name, description, created_at, 0 FROM sites WHERE id = ?",
                (site_id,),
            )
            row = cur.fetchone()
        if not row:
            raise RuntimeError("site not found after insert")
        return self._row_to_site(row)

    def delete_sites(self, ids: Sequence[int]) -> int:
        if not ids:
            return 0
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in ids)
            try:
                cur.execute(
                    f"DELETE FROM device_sites WHERE site_id IN ({placeholders})",
                    tuple(ids),
                )
                cur.execute(
                    f"DELETE FROM sites WHERE id IN ({placeholders})",
                    tuple(ids),
                )
            except sqlite3.DatabaseError as exc:
                conn.rollback()
                raise
            deleted = cur.rowcount
            conn.commit()
        return deleted

    def rename_site(self, site_id: int, new_name: str) -> SiteSummary:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            try:
                cur.execute("UPDATE sites SET name = ? WHERE id = ?", (new_name, site_id))
            except sqlite3.IntegrityError as exc:
                raise ValueError("duplicate") from exc
            if cur.rowcount == 0:
                raise LookupError("not_found")
            conn.commit()
            cur.execute(
                """
                SELECT s.id, s.name, s.description, s.created_at,
                       COALESCE(ds.cnt, 0) AS device_count
                  FROM sites s
             LEFT JOIN (
                        SELECT site_id, COUNT(*) AS cnt
                          FROM device_sites
                      GROUP BY site_id
                    ) ds
                    ON ds.site_id = s.id
                 WHERE s.id = ?
                """,
                (site_id,),
            )
            row = cur.fetchone()
        if not row:
            raise LookupError("not_found")
        return self._row_to_site(row)

    def map_devices(self, hostnames: Optional[Iterable[str]] = None) -> Dict[str, SiteDeviceMapping]:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            if hostnames:
                normalized = [hn.strip() for hn in hostnames if hn and hn.strip()]
                if not normalized:
                    return {}
                placeholders = ",".join("?" for _ in normalized)
                cur.execute(
                    f"""
                    SELECT ds.device_hostname, s.id, s.name
                      FROM device_sites ds
                INNER JOIN sites s ON s.id = ds.site_id
                     WHERE ds.device_hostname IN ({placeholders})
                    """,
                    tuple(normalized),
                )
            else:
                cur.execute(
                    """
                    SELECT ds.device_hostname, s.id, s.name
                      FROM device_sites ds
                INNER JOIN sites s ON s.id = ds.site_id
                    """
                )
            rows = cur.fetchall()
        mapping: Dict[str, SiteDeviceMapping] = {}
        for hostname, site_id, site_name in rows:
            mapping[str(hostname)] = SiteDeviceMapping(
                hostname=str(hostname),
                site_id=int(site_id) if site_id is not None else None,
                site_name=str(site_name or ""),
            )
        return mapping

    def assign_devices(self, site_id: int, hostnames: Sequence[str]) -> None:
        now = int(time.time())
        normalized = [hn.strip() for hn in hostnames if isinstance(hn, str) and hn.strip()]
        if not normalized:
            return
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute("SELECT 1 FROM sites WHERE id = ?", (site_id,))
            if not cur.fetchone():
                raise LookupError("not_found")
            for hostname in normalized:
                cur.execute(
                    """
                    INSERT INTO device_sites(device_hostname, site_id, assigned_at)
                    VALUES (?, ?, ?)
                    ON CONFLICT(device_hostname)
                    DO UPDATE SET site_id = excluded.site_id,
                                  assigned_at = excluded.assigned_at
                    """,
                    (hostname, site_id, now),
                )
            conn.commit()

    def _row_to_site(self, row: Sequence[object]) -> SiteSummary:
        return SiteSummary(
            id=int(row[0]),
            name=str(row[1] or ""),
            description=str(row[2] or ""),
            created_at=int(row[3] or 0),
            device_count=int(row[4] or 0),
        )
