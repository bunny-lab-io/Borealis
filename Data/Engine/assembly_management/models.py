# ======================================================
# Data\Engine\assembly_management\models.py
# Description: Dataclasses describing assemblies, payload descriptors, and cache state.
#
# API Endpoints (if applicable): None
# ======================================================

"""Model definitions for assembly persistence."""

from __future__ import annotations

import datetime as _dt
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Dict, Optional


class AssemblyDomain(str, Enum):
    """Logical source domains mapped to dedicated SQLite databases."""

    OFFICIAL = "official"
    COMMUNITY = "community"
    USER = "user"

    @property
    def database_name(self) -> str:
        mapping = {
            AssemblyDomain.OFFICIAL: "official.db",
            AssemblyDomain.COMMUNITY: "community.db",
            AssemblyDomain.USER: "user_created.db",
        }
        return mapping[self]


class PayloadType(str, Enum):
    """Supported payload classifications."""

    SCRIPT = "script"
    WORKFLOW = "workflow"
    BINARY = "binary"
    UNKNOWN = "unknown"


@dataclass(slots=True)
class PayloadDescriptor:
    """Represents on-disk payload material referenced by an assembly."""

    assembly_guid: str
    payload_type: PayloadType
    file_name: str
    file_extension: str
    size_bytes: int
    checksum: Optional[str]
    created_at: _dt.datetime
    updated_at: _dt.datetime

    def as_dict(self) -> Dict[str, Any]:
        return {
            "assembly_guid": self.assembly_guid,
            "payload_type": self.payload_type.value,
            "file_name": self.file_name,
            "file_extension": self.file_extension,
            "size_bytes": self.size_bytes,
            "checksum": self.checksum,
            "created_at": self.created_at.isoformat(),
            "updated_at": self.updated_at.isoformat(),
        }

    @property
    def guid(self) -> str:
        """Backwards-compatible accessor for legacy references."""

        return self.assembly_guid


@dataclass(slots=True)
class AssemblyRecord:
    """Represents an assembly row hydrated from persistence."""

    assembly_guid: str
    display_name: str
    summary: Optional[str]
    category: Optional[str]
    assembly_kind: str
    assembly_type: Optional[str]
    version: int
    payload: PayloadDescriptor
    metadata: Dict[str, Any] = field(default_factory=dict)
    tags: Dict[str, Any] = field(default_factory=dict)
    checksum: Optional[str] = None
    created_at: _dt.datetime = field(default_factory=_dt.datetime.utcnow)
    updated_at: _dt.datetime = field(default_factory=_dt.datetime.utcnow)


@dataclass(slots=True)
class CachedAssembly:
    """Wrapper stored in memory with dirty state tracking."""

    domain: AssemblyDomain
    record: AssemblyRecord
    is_dirty: bool = False
    last_persisted: Optional[_dt.datetime] = None
    dirty_since: Optional[_dt.datetime] = None

    def mark_dirty(self) -> None:
        now = _dt.datetime.utcnow()
        self.is_dirty = True
        self.dirty_since = self.dirty_since or now

    def mark_clean(self) -> None:
        now = _dt.datetime.utcnow()
        self.is_dirty = False
        self.last_persisted = now
        self.dirty_since = None
