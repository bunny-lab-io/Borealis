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
import json
import logging
import re
import uuid
from typing import Any, Dict, Iterable, List, Mapping, Optional, Set, Union

from ...assembly_management.bootstrap import AssemblyCache
from ...assembly_management.models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadDescriptor
from .official_catalog import compute_record_content_hash
from .serialization import (
    AssemblySerializationError,
    prepare_import_request,
    record_to_legacy_payload,
)


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
        assembly_type: Optional[str] = None,
    ) -> List[Dict[str, Any]]:
        domain_filter = _coerce_domain(domain) if domain else None
        entries = self._cache.list_entries(domain=domain_filter)
        results: List[Dict[str, Any]] = []
        for entry in entries:
            record = entry.record
            if assembly_type and record.assembly_type.lower() != assembly_type.lower():
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

    def resolve_document_by_guid(
        self,
        assembly_guid: str,
        *,
        include_payload: bool = True,
    ) -> Optional[Dict[str, Any]]:
        guid = _coerce_guid(assembly_guid)
        if not guid:
            return None
        entry = self._cache.get_entry(guid)
        if not entry:
            return None
        payload_text = self._read_payload_text(guid) if include_payload else None
        return self._serialize_entry(entry, include_payload=include_payload, payload_text=payload_text)

    def resolve_document_by_source_path(
        self,
        source_path: str,
        *,
        include_payload: bool = True,
    ) -> Optional[Dict[str, Any]]:
        """Return an assembly record whose virtual path matches the provided value."""

        normalized = _normalize_source_path(source_path)
        if not normalized:
            return None
        lookup_key = normalized.lower()
        try:
            entries = self._cache.list_entries()
        except Exception:
            entries = []
        for entry in entries:
            for candidate in _iter_virtual_paths(entry.record):
                if candidate.lower() != lookup_key:
                    continue
                payload_text = self._read_payload_text(entry.record.assembly_guid) if include_payload else None
                return self._serialize_entry(entry, include_payload=include_payload, payload_text=payload_text)
        return None

    def export_assembly(self, assembly_guid: str) -> Dict[str, Any]:
        entry = self._cache.get_entry(assembly_guid)
        if not entry:
            raise ValueError(f"Assembly '{assembly_guid}' not found")
        payload_text = self._read_payload_text(assembly_guid)
        return record_to_legacy_payload(entry.record, domain=entry.domain, payload_text=payload_text)

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
        now = _utcnow()
        descriptor = PayloadDescriptor(
            assembly_guid=clone_guid,
            file_name="payload.json",
            file_extension=".json",
            size_bytes=len(payload_text.encode("utf-8")),
            created_at=now,
            updated_at=now,
        )
        record = AssemblyRecord(
            assembly_guid=clone_guid,
            display_name=source_entry.record.display_name,
            summary=source_entry.record.summary,
            assembly_type=source_entry.record.assembly_type,
            assembly_subtype=source_entry.record.assembly_subtype,
            payload=descriptor,
            payload_json=payload_text,
            created_at=now,
            updated_at=now,
        )
        self._cache.stage_upsert(domain, record)
        return self.get_assembly(clone_guid) or {}

    def flush_writes(self) -> None:
        self._cache.flush_now()

    def import_assembly(
        self,
        *,
        domain: AssemblyDomain,
        document: Union[str, Mapping[str, Any]],
        assembly_guid: Optional[str] = None,
    ) -> Dict[str, Any]:
        resolved_guid, payload = prepare_import_request(
            document,
            domain=domain,
            assembly_guid=assembly_guid,
        )
        if resolved_guid and self._cache.get_entry(resolved_guid):
            return self.update_assembly(resolved_guid, payload)
        return self.create_assembly(payload)

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
        assembly_type = _normalize_assembly_type(
            payload.get("assembly_type") or (existing.record.assembly_type if existing else "") or "script"
        )
        display_name = payload.get("display_name") or (existing.record.display_name if existing else None)
        summary = payload.get("summary")

        payload_content = payload.get("payload")
        if isinstance(payload_content, Mapping):
            payload_content = dict(payload_content)
            payload_content["assembly_guid"] = assembly_guid

        if summary is None and isinstance(payload_content, Mapping):
            summary = _coerce_optional_text(payload_content.get("description") or payload_content.get("summary"))
        if summary is None and existing:
            summary = existing.record.summary
        assembly_subtype = _normalize_optional_text(
            payload.get("assembly_subtype") or (existing.record.assembly_subtype if existing else None)
        )
        if not assembly_subtype:
            assembly_subtype = _default_assembly_subtype(assembly_type)

        source_repo = _coerce_optional_text(payload.get("source_repo"))
        if source_repo is None and existing:
            source_repo = existing.record.source_repo
        source_path = _coerce_optional_text(payload.get("source_path"))
        if source_path is None and existing:
            source_path = existing.record.source_path
        source_version = _coerce_optional_text(payload.get("source_version"))
        if source_version is None and existing:
            source_version = existing.record.source_version
        payload_text = _serialize_payload(payload_content) if payload_content is not None else None

        if existing:
            if payload_text is None:
                # Keep existing payload descriptor/content
                descriptor = existing.record.payload
                payload_text = existing.record.payload_json
            else:
                descriptor = copy.deepcopy(existing.record.payload)
                descriptor.size_bytes = len(payload_text.encode("utf-8"))
                descriptor.updated_at = now
        else:
            if payload_text is None:
                raise ValueError("payload content required for new assemblies")
            descriptor = PayloadDescriptor(
                assembly_guid=assembly_guid,
                file_name="payload.json",
                file_extension=".json",
                size_bytes=len(payload_text.encode("utf-8")),
                created_at=now,
                updated_at=now,
            )

        record = AssemblyRecord(
            assembly_guid=assembly_guid,
            display_name=display_name or assembly_guid,
            summary=summary,
            assembly_type=assembly_type,
            assembly_subtype=assembly_subtype,
            payload=descriptor,
            payload_json=payload_text,
            source_repo=source_repo,
            source_path=source_path,
            source_version=source_version,
            created_at=existing.record.created_at if existing else now,
            updated_at=now,
        )
        record.content_hash = compute_record_content_hash(record)
        return record

    def _serialize_entry(
        self,
        entry: CachedAssembly,
        *,
        include_payload: bool,
        payload_text: Optional[str] = None,
    ) -> Dict[str, Any]:
        record = entry.record
        canonical_summary = _canonical_summary(record.summary, payload_text or record.payload_json)
        data: Dict[str, Any] = {
            "assembly_guid": record.assembly_guid,
            "name": record.display_name,
            "description": canonical_summary,
            "display_name": record.display_name,
            "summary": canonical_summary,
            "assembly_type": record.assembly_type,
            "assembly_subtype": record.assembly_subtype,
            "source": entry.domain.value,
            "is_dirty": entry.is_dirty,
            "dirty_since": entry.dirty_since.isoformat() if entry.dirty_since else None,
            "last_persisted": entry.last_persisted.isoformat() if entry.last_persisted else None,
            "payload_guid": record.payload.assembly_guid,
            "source_repo": record.source_repo,
            "source_path": record.source_path,
            "source_version": record.source_version,
            "virtual_path": record.source_path or _fallback_source_path(record),
            "created_at": record.created_at.isoformat(),
            "updated_at": record.updated_at.isoformat(),
            "content_hash": record.content_hash or compute_record_content_hash(record),
        }
        data["path"] = data["virtual_path"]
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


def _normalize_source_path(value: Any) -> str:
    """Normalise virtual assembly paths for comparison."""

    if value is None:
        return ""
    text = str(value).replace("\\", "/").strip()
    if not text:
        return ""
    segments = []
    for part in text.split("/"):
        candidate = part.strip()
        if not candidate or candidate == ".":
            continue
        if candidate == "..":
            return ""
        segments.append(candidate)
    if not segments:
        return ""
    return "/".join(segments)


def _iter_virtual_paths(record: AssemblyRecord) -> Iterable[str]:
    """Yield canonical virtual paths for the provided assembly record."""

    seen: Set[str] = set()
    canonical_source_path = _normalize_source_path(record.source_path)
    if canonical_source_path:
        lowered_source = canonical_source_path.lower()
        if lowered_source not in seen:
            seen.add(lowered_source)
            yield canonical_source_path
    fallback = _fallback_source_path(record)
    if fallback:
        lowered = fallback.lower()
        if lowered not in seen:
            seen.add(lowered)
            yield fallback
    fallback_leaf = fallback.split("/", 1)[1] if "/" in fallback else fallback
    if fallback_leaf:
        lowered_leaf = fallback_leaf.lower()
        if lowered_leaf not in seen:
            seen.add(lowered_leaf)
            yield fallback_leaf


def _fallback_source_path(record: AssemblyRecord) -> str:
    prefix = _type_prefix(record.assembly_type)
    fallback_name = (
        record.display_name
        or record.summary
        or record.assembly_guid
        or "Assembly"
    )
    safe_name = _sanitize_name_for_path(fallback_name)
    candidate = f"{prefix}/{safe_name}"
    return _normalize_source_path(candidate)


def _type_prefix(assembly_type: Optional[str]) -> str:
    key = (assembly_type or "").strip().lower()
    if key == "ansible":
        return "Ansible_Playbooks"
    if key == "workflow":
        return "Workflows"
    return "Scripts"


_PATH_SANITIZE_PATTERN = re.compile(r"[^A-Za-z0-9._-]+")


def _sanitize_name_for_path(value: Any, fallback: str = "Assembly") -> str:
    text = str(value or "").strip()
    if not text:
        return fallback
    sanitized = _PATH_SANITIZE_PATTERN.sub("_", text)
    sanitized = sanitized.strip()
    return sanitized or fallback


def _serialize_payload(value: Any) -> str:
    if isinstance(value, (dict, list)):
        return json.dumps(value, indent=2, sort_keys=True)
    if isinstance(value, str):
        return value
    raise ValueError("payload must be JSON object, array, or string")


def _default_assembly_subtype(assembly_type: str) -> str:
    lowered = str(assembly_type or "").strip().lower()
    if lowered == "workflow":
        return "workflow"
    if lowered == "ansible":
        return "ansible"
    return "powershell"


def _normalize_assembly_type(value: Any) -> str:
    lowered = str(value or "").strip().lower()
    if lowered in {"workflow", "ansible", "script"}:
        return lowered
    return "script"


def _normalize_optional_text(value: Any) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip().lower()
    return text or None


def _coerce_optional_text(value: Any) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _canonical_summary(summary: Any, payload_text: Optional[str]) -> Optional[str]:
    payload_summary = None
    if payload_text:
        try:
            payload = json.loads(payload_text)
        except Exception:
            payload = None
        if isinstance(payload, Mapping):
            payload_summary = _coerce_optional_text(payload.get("description") or payload.get("summary"))
    return payload_summary or _coerce_optional_text(summary)


def _utcnow() -> _dt.datetime:
    return _dt.datetime.utcnow().replace(microsecond=0)
