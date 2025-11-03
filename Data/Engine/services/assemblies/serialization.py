# ======================================================
# Data\Engine\services\assemblies\serialization.py
# Description: Converts assembly records to and from legacy JSON documents for import/export.
#
# API Endpoints (if applicable): None
# ======================================================

"""Legacy assembly serialization helpers."""

from __future__ import annotations

import json
from typing import Any, Dict, Mapping, Optional, Tuple, Union

from ...assembly_management.models import AssemblyDomain, AssemblyRecord


MAX_DOCUMENT_BYTES = 1_048_576  # 1 MiB safety limit for import payloads


class AssemblySerializationError(ValueError):
    """Raised when legacy assembly serialization/deserialization fails."""


LegacyDocument = Dict[str, Any]


def record_to_legacy_payload(
    record: AssemblyRecord,
    *,
    domain: AssemblyDomain,
    payload_text: str,
) -> Dict[str, Any]:
    """Convert an assembly record into an export-friendly legacy JSON payload."""

    payload_body: Union[LegacyDocument, str]
    try:
        payload_body = json.loads(payload_text)
    except json.JSONDecodeError:
        payload_body = payload_text

    return {
        "assembly_guid": record.assembly_guid,
        "domain": domain.value,
        "assembly_kind": record.assembly_kind,
        "assembly_type": record.assembly_type,
        "version": record.version,
        "display_name": record.display_name,
        "summary": record.summary,
        "category": record.category,
        "metadata": dict(record.metadata or {}),
        "tags": dict(record.tags or {}),
        "payload": payload_body,
        "payload_type": record.payload.payload_type.value,
        "payload_guid": record.payload.assembly_guid,
        "payload_checksum": record.payload.checksum,
        "created_at": record.created_at.isoformat(),
        "updated_at": record.updated_at.isoformat(),
    }


def prepare_import_request(
    document: Union[str, Mapping[str, Any]],
    *,
    domain: AssemblyDomain,
    assembly_guid: Optional[str] = None,
    metadata_override: Optional[Mapping[str, Any]] = None,
    tags_override: Optional[Mapping[str, Any]] = None,
) -> Tuple[str, Dict[str, Any]]:
    """
    Validate a legacy assembly document and convert it into a runtime payload suitable
    for AssemblyRuntimeService create/update calls.

    Returns the resolved assembly GUID plus the payload dictionary to pass into the runtime service.
    """

    payload_json = _coerce_document(document)
    _enforce_size_limit(payload_json)
    assembly_kind = _infer_kind(payload_json)
    if assembly_kind == "unknown":
        raise AssemblySerializationError("Unable to determine assembly kind from legacy JSON document.")

    metadata = _metadata_from_document(assembly_kind, payload_json, source_path=None)
    if metadata_override:
        metadata.update({k: v for k, v in metadata_override.items() if v is not None})

    tags = dict(tags_override or {})
    display_name = _coerce_str(
        metadata.get("display_name")
        or payload_json.get("name")
        or payload_json.get("tab_name")
        or "Imported Assembly"
    )
    summary = _coerce_optional_str(metadata.get("summary") or payload_json.get("description"))
    category = _coerce_optional_str(metadata.get("category") or payload_json.get("category"))
    assembly_type = _coerce_optional_str(metadata.get("assembly_type") or payload_json.get("type"))
    version = _coerce_positive_int(payload_json.get("version"), default=1)

    resolved_guid = _coerce_guid(assembly_guid)

    payload = {
        "assembly_guid": resolved_guid,
        "domain": domain.value,
        "assembly_kind": assembly_kind,
        "display_name": display_name,
        "summary": summary,
        "category": category,
        "assembly_type": assembly_type,
        "version": version,
        "metadata": metadata,
        "tags": tags,
        "payload": payload_json,
    }

    return resolved_guid, payload


# ----------------------------------------------------------------------
# Helpers
# ----------------------------------------------------------------------
def _coerce_document(document: Union[str, Mapping[str, Any]]) -> LegacyDocument:
    if isinstance(document, Mapping):
        return dict(document)
    if isinstance(document, str):
        try:
            value = json.loads(document)
        except json.JSONDecodeError as exc:
            raise AssemblySerializationError("Import document is not valid JSON.") from exc
        if not isinstance(value, Mapping):
            raise AssemblySerializationError("Import document must decode to a JSON object.")
        return dict(value)
    raise AssemblySerializationError("Import document must be a JSON object or string.")


def _enforce_size_limit(document: Mapping[str, Any]) -> None:
    encoded = json.dumps(document, separators=(",", ":")).encode("utf-8")
    if len(encoded) > MAX_DOCUMENT_BYTES:
        raise AssemblySerializationError(
            f"Import document exceeds maximum allowed size of {MAX_DOCUMENT_BYTES} bytes."
        )


def _infer_kind(document: Mapping[str, Any]) -> str:
    kind_hint = _coerce_optional_str(document.get("assembly_kind") or document.get("kind"))
    if kind_hint:
        lowercase = kind_hint.lower()
        if lowercase in {"script", "workflow", "ansible"}:
            return lowercase
    if "nodes" in document and "edges" in document:
        return "workflow"
    if "script" in document:
        return "script"
    if "playbook" in document or "tasks" in document or "roles" in document:
        return "ansible"
    return "unknown"


def _metadata_from_document(kind: str, document: Mapping[str, Any], source_path: Optional[str]) -> Dict[str, Any]:
    metadata: Dict[str, Any] = {
        "source_path": source_path,
        "display_name": None,
        "summary": None,
        "category": None,
        "assembly_type": None,
    }

    if kind == "workflow":
        metadata.update(
            {
                "display_name": document.get("tab_name") or document.get("name"),
                "summary": document.get("description"),
                "category": "workflow",
                "assembly_type": "workflow",
            }
        )
    elif kind == "script":
        metadata.update(
            {
                "display_name": document.get("name") or document.get("display_name"),
                "summary": document.get("description"),
                "category": (document.get("category") or "script"),
                "assembly_type": (document.get("type") or "powershell"),
            }
        )
    elif kind == "ansible":
        metadata.update(
            {
                "display_name": document.get("name") or document.get("display_name"),
                "summary": document.get("description"),
                "category": "ansible",
                "assembly_type": "ansible",
            }
        )

    # Carry additional legacy fields through metadata for round-trip fidelity.
    for key in ("sites", "variables", "files", "timeout_seconds", "script_encoding"):
        if key in document:
            metadata[key] = document[key]

    metadata = {key: value for key, value in metadata.items() if value is not None}
    return metadata


def _coerce_guid(value: Optional[str]) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _coerce_str(value: Any, default: str = "") -> str:
    if value is None:
        return default
    text = str(value).strip()
    return text if text else default


def _coerce_optional_str(value: Any) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _coerce_positive_int(value: Any, *, default: int) -> int:
    try:
        candidate = int(value)
        if candidate > 0:
            return candidate
    except (TypeError, ValueError):
        pass
    return default


__all__ = [
    "AssemblySerializationError",
    "MAX_DOCUMENT_BYTES",
    "prepare_import_request",
    "record_to_legacy_payload",
]
