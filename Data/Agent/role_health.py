from __future__ import annotations

import re
import threading
import time
from typing import Any, Callable, Dict, List, Optional


_STATUS_LABELS = {
    "healthy": "Healthy",
    "recovering": "Recovering",
    "unhealthy": "Unhealthy",
    "pending": "Pending",
    "loaded": "Loaded",
    "unsupported": "Unsupported",
    "unknown": "Unknown",
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


def normalize_role_context(value: Any) -> str:
    text = _clean_text(value).lower().replace("-", "_")
    if text in {"system", "svc", "service", "system_service"}:
        return "system"
    if text in {"interactive", "currentuser", "current_user", "user"}:
        return "currentuser"
    return text or "unknown"


def humanize_role_name(value: Any) -> str:
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


def normalize_status_code(value: Any) -> str:
    text = _clean_text(value).lower().replace(" ", "_").replace("-", "_")
    aliases = {
        "ok": "healthy",
        "up": "healthy",
        "ready": "healthy",
        "running": "healthy",
        "online": "healthy",
        "warning": "recovering",
        "recover": "recovering",
        "recovering_now": "recovering",
        "degraded": "recovering",
        "healing": "recovering",
        "starting": "recovering",
        "bootstrapping": "pending",
        "initializing": "pending",
        "inactive": "pending",
        "idle": "pending",
        "down": "unhealthy",
        "failed": "unhealthy",
        "error": "unhealthy",
        "broken": "unhealthy",
    }
    normalized = aliases.get(text, text)
    if normalized in _STATUS_LABELS:
        return normalized
    return "unknown"


def status_label(value: Any) -> str:
    return _STATUS_LABELS.get(normalize_status_code(value), "Unknown")


def role_health_snapshot(
    *,
    role_name: Any,
    context: Any,
    status: Any = "healthy",
    role_label: Any = None,
    detail: Any = "",
    details: Any = None,
    last_checked_at: Any = None,
    role_id: Any = None,
) -> Dict[str, Any]:
    normalized_name = _clean_text(role_name) or "unknown"
    normalized_context = normalize_role_context(context)
    normalized_role_id = _clean_text(role_id) or f"{normalized_context}:{normalized_name}"
    normalized_status = normalize_status_code(status)
    checked_at = _coerce_int(last_checked_at, int(time.time()))
    return {
        "role_id": normalized_role_id,
        "role_name": normalized_name,
        "role_label": _clean_text(role_label) or humanize_role_name(normalized_name),
        "context": normalized_context,
        "status_code": normalized_status,
        "status": _STATUS_LABELS.get(normalized_status, "Unknown"),
        "detail": _clean_text(detail),
        "details": _clean_details_map(details),
        "last_checked_at": checked_at,
    }


class RoleHealthRegistry:
    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._entries: Dict[str, Dict[str, Any]] = {}

    def register_role(
        self,
        role_name: str,
        *,
        context: Any,
        reporter: Optional[Callable[[], Any]] = None,
        role_label: Optional[str] = None,
    ) -> str:
        normalized_context = normalize_role_context(context)
        role_id = f"{normalized_context}:{_clean_text(role_name) or 'unknown'}"
        with self._lock:
            self._entries[role_id] = {
                "role_id": role_id,
                "role_name": _clean_text(role_name) or "unknown",
                "context": normalized_context,
                "reporter": reporter,
                "role_label": _clean_text(role_label) or humanize_role_name(role_name),
            }
        return role_id

    def unregister_role(self, role_name: str, *, context: Any) -> None:
        role_id = f"{normalize_role_context(context)}:{_clean_text(role_name) or 'unknown'}"
        with self._lock:
            self._entries.pop(role_id, None)

    def snapshot(self) -> Dict[str, Any]:
        with self._lock:
            entries = list(self._entries.values())
        reported_at = int(time.time())
        roles: List[Dict[str, Any]] = []
        for entry in entries:
            role_name = entry.get("role_name") or "unknown"
            role_label = entry.get("role_label") or humanize_role_name(role_name)
            context = entry.get("context") or "unknown"
            reporter = entry.get("reporter")
            try:
                payload = reporter() if callable(reporter) else None
            except Exception as exc:
                payload = {
                    "status": "unhealthy",
                    "detail": f"Health reporter failed: {exc}",
                }
            if isinstance(payload, dict):
                snapshot = role_health_snapshot(
                    role_name=payload.get("role_name") or role_name,
                    context=payload.get("context") or context,
                    status=payload.get("status_code") or payload.get("status") or "healthy",
                    role_label=payload.get("role_label") or payload.get("label") or role_label,
                    detail=payload.get("detail") or payload.get("message") or "",
                    details=payload.get("details") or payload.get("metadata") or payload.get("info"),
                    last_checked_at=payload.get("last_checked_at") or payload.get("checked_at") or reported_at,
                    role_id=payload.get("role_id") or entry.get("role_id"),
                )
            elif isinstance(payload, str):
                snapshot = role_health_snapshot(
                    role_name=role_name,
                    context=context,
                    status=payload,
                    role_label=role_label,
                    last_checked_at=reported_at,
                    role_id=entry.get("role_id"),
                )
            else:
                snapshot = role_health_snapshot(
                    role_name=role_name,
                    context=context,
                    status="healthy",
                    role_label=role_label,
                    detail="Role loaded.",
                    last_checked_at=reported_at,
                    role_id=entry.get("role_id"),
                )
            roles.append(snapshot)
        roles.sort(key=lambda item: (str(item.get("role_label") or "").lower(), str(item.get("context") or "").lower()))
        return {
            "roles": roles,
            "reported_at": reported_at,
        }
