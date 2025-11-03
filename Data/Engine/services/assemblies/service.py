# ======================================================
# Data\Engine\services\assemblies\service.py
# Description: Provides assembly CRUD helpers backed by the AssemblyCache and SQLite persistence domains.
#
# API Endpoints (if applicable): None
# ======================================================

"""Runtime assembly management helpers for API routes."""

from __future__ import annotations

import copy
import datetime as _dt
import hashlib
import json
import logging
import uuid
from typing import Any, Dict, List, Mapping, Optional

from Data.Engine.assembly_management.bootstrap import AssemblyCache
from Data.Engine.assembly_management.models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadType
from Data.Engine.assembly_management.sync import sync_official_domain


class AssemblyRuntimeService:
    """High-level assembly operations backed by :class:`AssemblyCache`."""

    def __init__(self, cache: AssemblyCache, *, logger: Optional[logging.Logger] = None) -> None:
        if cache is None:
            raise RuntimeError("Assembly cache is not initialised; assemble the Engine runtime first.")
        self._cache = cache
        self._logger = logger or logging.getLogger(__name__)

    # ------------------------------------------------------------------
    # Query helpers
    # ------------------------------------------------------------------
    def list_assemblies(
        self,
        *,
        domain: Optional[str] = None,
        kind: Optional[str] = None,
    ) -> List[Dict[str, Any]]:
        domain_filter = _coerce_domain(domain) if domain else None
        entries = self._cache.list_entries(domain=domain_filter)
        results: List[Dict[str, Any]] = []
        for entry in entries:
            record = entry.record
            if kind and record.assembly_kind.lower() != kind.lower():
                continue
            results.append(self._serialize_entry(entry, include_payload=False))
        return results

    def get_assembly(self, assembly_guid: str) -> Optional[Dict[str, Any]]:
        entry = self._cache.get_entry(assembly_guid)
        if not entry:
            return None
        payload_text = self._read_payload_text(entry.record.assembly_guid)
        data = self._serialize_entry(entry, include_payload=True, payload_text=payload_text)
        return data

    def get_cached_entry(self, assembly_guid: str) -> Optional[CachedAssembly]:
        return self._cache.get_entry(assembly_guid)

    def queue_snapshot(self) -> List[Dict[str, Any]]:
        return self._cache.describe()

    # ------------------------------------------------------------------
    # Mutations
    # ------------------------------------------------------------------
    def create_assembly(self, payload: Mapping[str, Any]) -> Dict[str, Any]:
        assembly_guid = _coerce_guid(payload.get("assembly_guid"))
        if not assembly_guid:
            assembly_guid = uuid.uuid4().hex
        if self._cache.get_entry(assembly_guid):
            raise ValueError(f"Assembly '{assembly_guid}' already exists")

        domain = _expect_domain(payload.get("domain"))
        record = self._build_record(
            assembly_guid=assembly_guid,
            domain=domain,
            payload=payload,
            existing=None,
        )
        self._cache.stage_upsert(domain, record)
        return self.get_assembly(assembly_guid) or {}

    def update_assembly(self, assembly_guid: str, payload: Mapping[str, Any]) -> Dict[str, Any]:
        existing = self._cache.get_entry(assembly_guid)
        if not existing:
            raise ValueError(f"Assembly '{assembly_guid}' not found")
        record = self._build_record(
            assembly_guid=assembly_guid,
            domain=existing.domain,
            payload=payload,
            existing=existing,
        )
        self._cache.stage_upsert(existing.domain, record)
        return self.get_assembly(assembly_guid) or {}

    def delete_assembly(self, assembly_guid: str) -> None:
        entry = self._cache.get_entry(assembly_guid)
        if not entry:
            raise ValueError(f"Assembly '{assembly_guid}' not found")
        self._cache.stage_delete(assembly_guid)

    def clone_assembly(
        self,
        assembly_guid: str,
        *,
        target_domain: str,
        new_assembly_guid: Optional[str] = None,
    ) -> Dict[str, Any]:
        source_entry = self._cache.get_entry(assembly_guid)
        if not source_entry:
            raise ValueError(f"Assembly '{assembly_guid}' not found")

        domain = _expect_domain(target_domain)
        clone_guid = _coerce_guid(new_assembly_guid)
        if not clone_guid:
            clone_guid = uuid.uuid4().hex
        if self._cache.get_entry(clone_guid):
            raise ValueError(f"Assembly '{clone_guid}' already exists; provide a unique identifier.")

        payload_text = self._read_payload_text(assembly_guid)
        descriptor = self._cache.payload_manager.store_payload(
            _payload_type_from_kind(source_entry.record.assembly_kind),
            payload_text,
            assembly_guid=clone_guid,
            extension=".json",
        )

        now = _utcnow()
        record = AssemblyRecord(
            assembly_guid=clone_guid,
            display_name=source_entry.record.display_name,
            summary=source_entry.record.summary,
            category=source_entry.record.category,
            assembly_kind=source_entry.record.assembly_kind,
            assembly_type=source_entry.record.assembly_type,
            version=source_entry.record.version,
            payload=descriptor,
            metadata=copy.deepcopy(source_entry.record.metadata),
            tags=copy.deepcopy(source_entry.record.tags),
            checksum=hashlib.sha256(payload_text.encode("utf-8")).hexdigest(),
            created_at=now,
            updated_at=now,
        )
        self._cache.stage_upsert(domain, record)
        return self.get_assembly(clone_guid) or {}

    def flush_writes(self) -> None:
        self._cache.flush_now()

    def sync_official(self) -> None:
        db_manager = self._cache.database_manager
        payload_manager = self._cache.payload_manager
        staging_root = db_manager.staging_root
        sync_official_domain(db_manager, payload_manager, staging_root, logger=self._logger)
        self._cache.reload()

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _build_record(
        self,
        *,
        assembly_guid: str,
        domain: AssemblyDomain,
        payload: Mapping[str, Any],
        existing: Optional[CachedAssembly],
    ) -> AssemblyRecord:
        now = _utcnow()
        kind = str(payload.get("assembly_kind") or (existing.record.assembly_kind if existing else "") or "script").lower()
        metadata = _coerce_dict(payload.get("metadata"))
        display_name = payload.get("display_name") or metadata.get("display_name")
        summary = payload.get("summary") or metadata.get("summary")
        category = payload.get("category") or metadata.get("category")
        assembly_type = payload.get("assembly_type") or metadata.get("assembly_type")
        version = _coerce_int(payload.get("version"), default=existing.record.version if existing else 1)
        tags = _coerce_dict(payload.get("tags") or (existing.record.tags if existing else {}))

        payload_content = payload.get("payload")
        payload_text = _serialize_payload(payload_content) if payload_content is not None else None

        if existing:
            metadata = _merge_metadata(existing.record.metadata, metadata)
            if payload_text is None:
                # Keep existing payload descriptor/content
                descriptor = existing.record.payload
                payload_text = self._read_payload_text(existing.record.assembly_guid)
            else:
                descriptor = self._cache.payload_manager.update_payload(existing.record.payload, payload_text)
        else:
            if payload_text is None:
                raise ValueError("payload content required for new assemblies")
            descriptor = self._cache.payload_manager.store_payload(
                _payload_type_from_kind(kind),
                payload_text,
                assembly_guid=assembly_guid,
                extension=".json",
            )

        checksum = hashlib.sha256(payload_text.encode("utf-8")).hexdigest()
        record = AssemblyRecord(
            assembly_guid=assembly_guid,
            display_name=display_name or assembly_guid,
            summary=summary,
            category=category,
            assembly_kind=kind,
            assembly_type=assembly_type,
            version=version,
            payload=descriptor,
            metadata=metadata,
            tags=tags,
            checksum=checksum,
            created_at=existing.record.created_at if existing else now,
            updated_at=now,
        )
        return record

    def _serialize_entry(
        self,
        entry: CachedAssembly,
        *,
        include_payload: bool,
        payload_text: Optional[str] = None,
    ) -> Dict[str, Any]:
        record = entry.record
        data: Dict[str, Any] = {
            "assembly_guid": record.assembly_guid,
            "display_name": record.display_name,
            "summary": record.summary,
            "category": record.category,
            "assembly_kind": record.assembly_kind,
            "assembly_type": record.assembly_type,
            "version": record.version,
            "source": entry.domain.value,
            "is_dirty": entry.is_dirty,
            "dirty_since": entry.dirty_since.isoformat() if entry.dirty_since else None,
            "last_persisted": entry.last_persisted.isoformat() if entry.last_persisted else None,
            "payload_guid": record.payload.assembly_guid,
            "created_at": record.created_at.isoformat(),
            "updated_at": record.updated_at.isoformat(),
            "metadata": copy.deepcopy(record.metadata),
            "tags": copy.deepcopy(record.tags),
        }
        data.setdefault("assembly_id", record.assembly_guid)  # legacy alias for older clients
        if include_payload:
            payload_text = payload_text if payload_text is not None else self._read_payload_text(record.assembly_guid)
            data["payload"] = payload_text
            try:
                data["payload_json"] = json.loads(payload_text)
            except Exception:
                data["payload_json"] = None
        return data

    def _read_payload_text(self, assembly_guid: str) -> str:
        payload_bytes = self._cache.read_payload_bytes(assembly_guid)
        try:
            return payload_bytes.decode("utf-8")
        except UnicodeDecodeError:
            return payload_bytes.decode("utf-8", errors="replace")


def _coerce_domain(value: Any) -> Optional[AssemblyDomain]:
    if value is None:
        return None
    text = str(value).strip().lower()
    for domain in AssemblyDomain:
        if domain.value == text:
            return domain
    return None


def _expect_domain(value: Any) -> AssemblyDomain:
    domain = _coerce_domain(value)
    if domain is None:
        raise ValueError("invalid domain")
    return domain


def _coerce_guid(value: Any) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip()
    if not text:
        return None
    return text.lower()


def _payload_type_from_kind(kind: str) -> PayloadType:
    kind_lower = (kind or "").lower()
    if kind_lower == "workflow":
        return PayloadType.WORKFLOW
    if kind_lower == "script":
        return PayloadType.SCRIPT
    if kind_lower == "ansible":
        return PayloadType.BINARY
    return PayloadType.UNKNOWN


def _serialize_payload(value: Any) -> str:
    if isinstance(value, (dict, list)):
        return json.dumps(value, indent=2, sort_keys=True)
    if isinstance(value, str):
        return value
    raise ValueError("payload must be JSON object, array, or string")


def _coerce_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return copy.deepcopy(value)
    return {}


def _merge_metadata(existing: Dict[str, Any], new_values: Dict[str, Any]) -> Dict[str, Any]:
    combined = copy.deepcopy(existing or {})
    for key, val in (new_values or {}).items():
        combined[key] = val
    return combined


def _coerce_int(value: Any, *, default: int = 0) -> int:
    try:
        return int(value)
    except Exception:
        return default


def _utcnow() -> _dt.datetime:
    return _dt.datetime.utcnow().replace(microsecond=0)

