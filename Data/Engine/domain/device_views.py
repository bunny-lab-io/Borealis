"""Domain objects for saved device list views."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List

__all__ = ["DeviceListView"]


@dataclass(frozen=True, slots=True)
class DeviceListView:
    id: int
    name: str
    columns: List[str]
    filters: Dict[str, object]
    created_at: int
    updated_at: int

    def to_dict(self) -> Dict[str, object]:
        return {
            "id": self.id,
            "name": self.name,
            "columns": self.columns,
            "filters": self.filters,
            "created_at": self.created_at,
            "updated_at": self.updated_at,
        }
