from __future__ import annotations

import json
import re
from typing import Any, Dict, List, Optional


STATUS_LABELS = {
    "healthy": "Healthy",
    "recovering": "Recovering",
    "unhealthy": "Unhealthy",
    "pending": "Pending",
    "loaded": "Loaded",
    "unsupported": "Unsupported",
    "unknown": "Unknown",
}

ROLE_NAME_ALIASES = {
    "script_exec_system": "context_system",
    "script_exec_currentuser": "context_currentuser",
    "device_audit": "device_auditor",
    "service_control": "service_management",
    "remoteshell": "remote_shell",
    "wireguardtunnel": "wireguard",
    "macro": "macros",
    "screenshot": "node_screenshot",
}


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _coerce_int(value: Any, default: int = 0) -> int:
    try:
        if value in (None, ""):
            raise ValueError
        return int(float(value))
    except Exception:
        return default


def _clean_details_map(value: Any) -> Dict[str, str]:
    if not isinstance(value, dict):
        return {}
    cleaned: Dict[str, str] = {}
    for raw_key, raw_value in value.items():
        key = _clean_text(raw_key)
        if not key:
            continue
        cleaned[key] = _clean_text(raw_value)
    return cleaned


def _normalize_context(value: Any) -> str:
    text = _clean_text(value).lower().replace("-", "_")
    if text in {"system", "svc", "service", "system_service"}:
        return "system"
    if text in {"interactive", "currentuser", "current_user", "user"}:
        return "currentuser"
    return text or "unknown"


def _normalize_status_code(value: Any) -> str:
    text = _clean_text(value).lower().replace(" ", "_").replace("-", "_")
    aliases = {
        "ok": "healthy",
        "up": "healthy",
        "ready": "healthy",
        "running": "healthy",
        "online": "healthy",
        "warning": "recovering",
        "degraded": "recovering",
        "healing": "recovering",
        "starting": "recovering",
        "bootstrapping": "pending",
        "initializing": "pending",
        "idle": "pending",
        "down": "unhealthy",
        "failed": "unhealthy",
        "error": "unhealthy",
        "broken": "unhealthy",
    }
    normalized = aliases.get(text, text)
    if normalized in STATUS_LABELS:
        return normalized
    return "unknown"


def _normalize_role_name(value: Any) -> str:
    text = _clean_text(value)
    if not text:
        return ""
    lowered = text.lower()
    return ROLE_NAME_ALIASES.get(lowered, text)


def _humanize_role_name(value: Any) -> str:
    text = _clean_text(value)
    if not text:
        return "Unknown Role"
    text = text.replace("_", " ")
    text = re.sub(r"(?<=[a-z0-9])(?=[A-Z])", " ", text)
    text = re.sub(r"\s+", " ", text).strip()
    parts = []
    for part in text.split():
        parts.append(part if part.isupper() else part.capitalize())
    return " ".join(parts) or "Unknown Role"


def _normalize_payload_shape(raw: Any) -> Dict[str, Any]:
    candidate = raw
    if isinstance(candidate, str):
        text = candidate.strip()
        if not text:
            return {"roles": [], "reported_at": 0}
        try:
            candidate = json.loads(text)
        except Exception:
            return {"roles": [], "reported_at": 0}
    if isinstance(candidate, list):
        return {"roles": candidate, "reported_at": 0}
    if isinstance(candidate, dict):
        roles = candidate.get("roles")
        if isinstance(roles, list):
            return {
                "roles": roles,
                "reported_at": _coerce_int(candidate.get("reported_at"), 0),
            }
        return {
            "roles": [],
            "reported_at": _coerce_int(candidate.get("reported_at"), 0),
        }
    return {"roles": [], "reported_at": 0}


def normalize_agent_role_health(raw: Any, *, default_context: Optional[str] = None) -> Dict[str, Any]:
    payload = _normalize_payload_shape(raw)
    normalized_context = _normalize_context(default_context)
    reported_at = _coerce_int(payload.get("reported_at"), 0)
    roles: List[Dict[str, Any]] = []
    for item in payload.get("roles") or []:
        if not isinstance(item, dict):
            continue
        role_name = _normalize_role_name(
            _clean_text(item.get("role_name"))
            or _clean_text(item.get("role"))
            or _clean_text(item.get("name"))
            or _clean_text(item.get("role_label"))
            or _clean_text(item.get("label"))
        )
        if not role_name:
            continue
        context = _normalize_context(item.get("context") or normalized_context)
        role_id = f"{context}:{role_name}"
        status_code = _normalize_status_code(item.get("status_code") or item.get("status"))
        last_checked_at = _coerce_int(
            item.get("last_checked_at") or item.get("checked_at") or reported_at,
            reported_at,
        )
        role = {
            "role_id": role_id,
            "role_name": role_name,
            "role_label": _clean_text(item.get("role_label") or item.get("label")) or _humanize_role_name(role_name),
            "context": context,
            "status_code": status_code,
            "status": STATUS_LABELS.get(status_code, "Unknown"),
            "detail": _clean_text(item.get("detail") or item.get("message")),
            "details": _clean_details_map(item.get("details") or item.get("metadata") or item.get("info")),
            "last_checked_at": last_checked_at,
        }
        roles.append(role)
    deduped: Dict[str, Dict[str, Any]] = {}
    for role in roles:
        deduped[role["role_id"]] = role
    normalized_roles = sorted(
        deduped.values(),
        key=lambda item: (
            str(item.get("role_label") or "").lower(),
            str(item.get("context") or "").lower(),
        ),
    )
    if not reported_at and normalized_roles:
        reported_at = max(_coerce_int(item.get("last_checked_at"), 0) for item in normalized_roles)
    return {
        "roles": normalized_roles,
        "reported_at": reported_at,
    }


def merge_agent_role_health(
    existing_raw: Any,
    incoming_raw: Any,
    *,
    incoming_context: Optional[str] = None,
) -> Dict[str, Any]:
    existing = normalize_agent_role_health(existing_raw)
    normalized_context = _normalize_context(incoming_context)
    incoming = normalize_agent_role_health(incoming_raw, default_context=normalized_context)
    replace_contexts = {
        _normalize_context(item.get("context"))
        for item in incoming.get("roles") or []
        if _normalize_context(item.get("context")) != "unknown"
    }
    if normalized_context and normalized_context != "unknown":
        replace_contexts.add(normalized_context)
    kept_roles = [
        item
        for item in (existing.get("roles") or [])
        if not replace_contexts or _normalize_context(item.get("context")) not in replace_contexts
    ]
    merged: Dict[str, Dict[str, Any]] = {}
    for item in kept_roles:
        merged[str(item.get("role_id") or "")] = item
    for item in incoming.get("roles") or []:
        merged[str(item.get("role_id") or "")] = item
    merged_roles = sorted(
        [item for item in merged.values() if item.get("role_id")],
        key=lambda item: (
            str(item.get("role_label") or "").lower(),
            str(item.get("context") or "").lower(),
        ),
    )
    reported_at = max(
        _coerce_int(existing.get("reported_at"), 0),
        _coerce_int(incoming.get("reported_at"), 0),
        max((_coerce_int(item.get("last_checked_at"), 0) for item in merged_roles), default=0),
    )
    return {
        "roles": merged_roles,
        "reported_at": reported_at,
    }


def serialize_agent_role_health(payload: Any) -> str:
    normalized = normalize_agent_role_health(payload)
    return json.dumps(normalized, ensure_ascii=True, sort_keys=True)
