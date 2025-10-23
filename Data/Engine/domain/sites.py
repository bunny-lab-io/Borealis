"""Domain models for operator site management."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Optional

__all__ = ["SiteSummary", "SiteDeviceMapping"]


@dataclass(frozen=True, slots=True)
class SiteSummary:
    """Representation of a site record including device counts."""

    id: int
    name: str
    description: str
    created_at: int
    device_count: int

    def to_dict(self) -> Dict[str, object]:
        return {
            "id": self.id,
            "name": self.name,
            "description": self.description,
            "created_at": self.created_at,
            "device_count": self.device_count,
        }


@dataclass(frozen=True, slots=True)
class SiteDeviceMapping:
    """Mapping entry describing which site a device belongs to."""

    hostname: str
    site_id: Optional[int]
    site_name: str

    def to_dict(self) -> Dict[str, object]:
        return {
            "site_id": self.site_id,
            "site_name": self.site_name,
        }
