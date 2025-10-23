"""Device activity history helpers."""

from __future__ import annotations

import logging
from typing import Any, Dict, List, Optional

from Data.Engine.domain.devices import clean_device_str
from Data.Engine.repositories.sqlite.activity_repository import (
    SQLiteDeviceActivityRepository,
)

__all__ = ["DeviceActivityService"]


class DeviceActivityService:
    """Expose activity history operations via higher-level helpers."""

    def __init__(
        self,
        *,
        repository: SQLiteDeviceActivityRepository,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._repo = repository
        self._log = logger or logging.getLogger("borealis.engine.services.devices.activity")

    def list_history(self, hostname: str) -> List[Dict[str, Any]]:
        normalized = clean_device_str(hostname)
        if not normalized:
            return []
        return self._repo.fetch_for_hostname(normalized)

    def clear_history(self, hostname: str) -> None:
        normalized = clean_device_str(hostname)
        if not normalized:
            return
        try:
            self._repo.delete_for_hostname(normalized)
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("device-activity-clear failed host=%s error=%s", normalized, exc)

    def get_activity(self, activity_id: int) -> Optional[Dict[str, Any]]:
        try:
            return self._repo.fetch_by_id(int(activity_id))
        except Exception:  # pragma: no cover - defensive conversion
            return None
