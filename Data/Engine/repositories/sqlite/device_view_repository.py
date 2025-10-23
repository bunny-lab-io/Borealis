"""SQLite persistence for device list views."""

from __future__ import annotations

import json
import logging
import sqlite3
import time
from contextlib import closing
from typing import Dict, Iterable, List, Optional

from Data.Engine.domain.device_views import DeviceListView
from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory

__all__ = ["SQLiteDeviceViewRepository"]


class SQLiteDeviceViewRepository:
    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.device_views")

    def list_views(self) -> List[DeviceListView]:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                "SELECT id, name, columns_json, filters_json, created_at, updated_at\n"
                "  FROM device_list_views ORDER BY name COLLATE NOCASE ASC"
            )
            rows = cur.fetchall()
        return [self._row_to_view(row) for row in rows]

    def get_view(self, view_id: int) -> Optional[DeviceListView]:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                "SELECT id, name, columns_json, filters_json, created_at, updated_at\n"
                "  FROM device_list_views WHERE id = ?",
                (view_id,),
            )
            row = cur.fetchone()
        return self._row_to_view(row) if row else None

    def create_view(self, name: str, columns: List[str], filters: Dict[str, object]) -> DeviceListView:
        now = int(time.time())
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            try:
                cur.execute(
                    "INSERT INTO device_list_views(name, columns_json, filters_json, created_at, updated_at)\n"
                    "VALUES (?, ?, ?, ?, ?)",
                    (name, json.dumps(columns), json.dumps(filters), now, now),
                )
            except sqlite3.IntegrityError as exc:
                raise ValueError("duplicate") from exc
            view_id = cur.lastrowid
            conn.commit()
            cur.execute(
                "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
                (view_id,),
            )
            row = cur.fetchone()
        if not row:
            raise RuntimeError("view missing after insert")
        return self._row_to_view(row)

    def update_view(
        self,
        view_id: int,
        *,
        name: Optional[str] = None,
        columns: Optional[List[str]] = None,
        filters: Optional[Dict[str, object]] = None,
    ) -> DeviceListView:
        fields: List[str] = []
        params: List[object] = []
        if name is not None:
            fields.append("name = ?")
            params.append(name)
        if columns is not None:
            fields.append("columns_json = ?")
            params.append(json.dumps(columns))
        if filters is not None:
            fields.append("filters_json = ?")
            params.append(json.dumps(filters))
        fields.append("updated_at = ?")
        params.append(int(time.time()))
        params.append(view_id)

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            try:
                cur.execute(
                    f"UPDATE device_list_views SET {', '.join(fields)} WHERE id = ?",
                    params,
                )
            except sqlite3.IntegrityError as exc:
                raise ValueError("duplicate") from exc
            if cur.rowcount == 0:
                raise LookupError("not_found")
            conn.commit()
            cur.execute(
                "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
                (view_id,),
            )
            row = cur.fetchone()
        if not row:
            raise LookupError("not_found")
        return self._row_to_view(row)

    def delete_view(self, view_id: int) -> bool:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute("DELETE FROM device_list_views WHERE id = ?", (view_id,))
            deleted = cur.rowcount
            conn.commit()
        return bool(deleted)

    def _row_to_view(self, row: Optional[Iterable[object]]) -> DeviceListView:
        if row is None:
            raise ValueError("row required")
        view_id, name, columns_json, filters_json, created_at, updated_at = row
        try:
            columns = json.loads(columns_json or "[]")
        except Exception:
            columns = []
        try:
            filters = json.loads(filters_json or "{}")
        except Exception:
            filters = {}
        return DeviceListView(
            id=int(view_id),
            name=str(name or ""),
            columns=list(columns) if isinstance(columns, list) else [],
            filters=dict(filters) if isinstance(filters, dict) else {},
            created_at=int(created_at or 0),
            updated_at=int(updated_at or 0),
        )
