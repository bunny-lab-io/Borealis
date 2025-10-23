"""Service exposing CRUD for saved device list views."""

from __future__ import annotations

import logging
from typing import List, Optional

from Data.Engine.domain.device_views import DeviceListView
from Data.Engine.repositories.sqlite.device_view_repository import SQLiteDeviceViewRepository

__all__ = ["DeviceViewService"]


class DeviceViewService:
    def __init__(
        self,
        repository: SQLiteDeviceViewRepository,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._repo = repository
        self._log = logger or logging.getLogger("borealis.engine.services.device_views")

    def list_views(self) -> List[DeviceListView]:
        return self._repo.list_views()

    def get_view(self, view_id: int) -> Optional[DeviceListView]:
        return self._repo.get_view(view_id)

    def create_view(self, name: str, columns: List[str], filters: dict) -> DeviceListView:
        normalized_name = (name or "").strip()
        if not normalized_name:
            raise ValueError("missing_name")
        if normalized_name.lower() == "default view":
            raise ValueError("reserved")
        return self._repo.create_view(normalized_name, list(columns), dict(filters))

    def update_view(
        self,
        view_id: int,
        *,
        name: Optional[str] = None,
        columns: Optional[List[str]] = None,
        filters: Optional[dict] = None,
    ) -> DeviceListView:
        updates: dict = {}
        if name is not None:
            normalized = (name or "").strip()
            if not normalized:
                raise ValueError("missing_name")
            if normalized.lower() == "default view":
                raise ValueError("reserved")
            updates["name"] = normalized
        if columns is not None:
            if not isinstance(columns, list) or not all(isinstance(col, str) for col in columns):
                raise ValueError("invalid_columns")
            updates["columns"] = list(columns)
        if filters is not None:
            if not isinstance(filters, dict):
                raise ValueError("invalid_filters")
            updates["filters"] = dict(filters)
        if not updates:
            raise ValueError("no_fields")
        return self._repo.update_view(
            view_id,
            name=updates.get("name"),
            columns=updates.get("columns"),
            filters=updates.get("filters"),
        )

    def delete_view(self, view_id: int) -> bool:
        return self._repo.delete_view(view_id)

