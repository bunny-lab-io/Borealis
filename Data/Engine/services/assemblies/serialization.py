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


MAX_DOCUMENT_BYTES = 950_000_000  # Practical upper bound under SQLite MAX_LENGTH=1,000,000,000 in this runtime


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
    if isinstance(payload_body, dict):
        payload_body = dict(payload_body)
        payload_body.setdefault("assembly_guid", record.assembly_guid)

    return {
        "assembly_guid": record.assembly_guid,
        "domain": domain.value,
        "assembly_type": record.assembly_type,
        "assembly_subtype": record.assembly_subtype,
        "display_name": record.display_name,
        "summary": record.summary,
        "payload": payload_body,
        "payload_guid": record.payload.assembly_guid,
        "created_at": record.created_at.isoformat(),
        "updated_at": record.updated_at.isoformat(),
    }


def prepare_import_request(
    document: Union[str, Mapping[str, Any]],
    *,
    domain: AssemblyDomain,
    assembly_guid: Optional[str] = None,
) -> Tuple[str, Dict[str, Any]]:
    """
    Validate a legacy assembly document and convert it into a runtime payload suitable
    for AssemblyRuntimeService create/update calls.

    Returns the resolved assembly GUID plus the payload dictionary to pass into the runtime service.
    """

    document_json = _coerce_document(document)
    payload_json = _extract_payload_document(document_json)
    _enforce_size_limit(payload_json)
    assembly_type = _infer_assembly_type({**document_json, **payload_json})
    if assembly_type == "unknown":
        raise AssemblySerializationError("Unable to determine assembly type from JSON document.")

    display_name = _coerce_str(
        document_json.get("display_name")
        or document_json.get("name")
        or document_json.get("tab_name")
        or payload_json.get("display_name")
        or payload_json.get("name")
        or payload_json.get("tab_name")
        or "Imported Assembly"
    )
    summary = _coerce_optional_str(
        document_json.get("summary")
        or document_json.get("description")
        or payload_json.get("summary")
        or payload_json.get("description")
    )
    assembly_subtype = _coerce_optional_str(
        document_json.get("assembly_subtype")
        or document_json.get("type")
        or payload_json.get("assembly_subtype")
        or payload_json.get("type")
    )
    if not assembly_subtype:
        if assembly_type == "workflow":
            assembly_subtype = "workflow"
        elif assembly_type == "ansible":
            assembly_subtype = "ansible"
        else:
            assembly_subtype = "powershell"

    resolved_guid = _coerce_guid(assembly_guid or document_json.get("assembly_guid") or payload_json.get("assembly_guid"))
    if resolved_guid:
        payload_json = dict(payload_json)
        payload_json.setdefault("assembly_guid", resolved_guid)

    payload = {
        "assembly_guid": resolved_guid,
        "domain": domain.value,
        "assembly_type": assembly_type,
        "display_name": display_name,
        "summary": summary,
        "assembly_subtype": assembly_subtype,
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


def _extract_payload_document(document: Mapping[str, Any]) -> LegacyDocument:
    payload = document.get("payload")
    if isinstance(payload, Mapping):
        return dict(payload)
    return dict(document)


def _enforce_size_limit(document: Mapping[str, Any]) -> None:
    encoded = json.dumps(document, separators=(",", ":")).encode("utf-8")
    if len(encoded) > MAX_DOCUMENT_BYTES:
        raise AssemblySerializationError(
            f"Import document exceeds maximum allowed size of {MAX_DOCUMENT_BYTES} bytes."
        )


def _infer_assembly_type(document: Mapping[str, Any]) -> str:
    type_hint = _coerce_optional_str(document.get("assembly_type") or document.get("kind"))
    if type_hint:
        type_lower = type_hint.lower()
        if type_lower in {"script", "workflow", "ansible"}:
            return type_lower
    subtype_hint = _coerce_optional_str(document.get("assembly_subtype") or document.get("type") or document.get("script_type"))
    if subtype_hint:
        subtype_lower = subtype_hint.lower()
        if subtype_lower in {"workflow", "ansible"}:
            return subtype_lower
    if "nodes" in document and "edges" in document:
        return "workflow"
    if "script" in document:
        return "script"
    if "playbook" in document or "tasks" in document or "roles" in document:
        return "ansible"
    return "unknown"


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


__all__ = [
    "AssemblySerializationError",
    "MAX_DOCUMENT_BYTES",
    "prepare_import_request",
    "record_to_legacy_payload",
]
