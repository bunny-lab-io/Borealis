# ======================================================
# Data\Engine\services\API\devices\session_inventory.py
# Description: Normalization helpers for cached device session snapshots.
#
# API Endpoints (if applicable): None
# ======================================================

"""Helpers for cached device user session inventory."""
from __future__ import annotations

import json
from typing import Any, Dict, List


STATE_LABELS = {
    "active": "Active",
    "locked": "Locked",
    "disconnected": "Disconnected",
    "idle": "Idle",
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


def _coerce_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    text = _clean_text(value).lower()
    if text in {"1", "true", "yes", "on"}:
        return True
    if text in {"0", "false", "no", "off"}:
        return False
    return default


def _normalize_payload_shape(raw: Any) -> Dict[str, Any]:
    candidate = raw
    if isinstance(candidate, str):
        text = candidate.strip()
        if not text:
            return {"sessions": [], "reported_at": 0}
        try:
            candidate = json.loads(text)
        except Exception:
            return {"sessions": [], "reported_at": 0}
    if isinstance(candidate, list):
        return {"sessions": candidate, "reported_at": 0}
    if isinstance(candidate, dict):
        sessions = candidate.get("sessions")
        return {
            "sessions": sessions if isinstance(sessions, list) else [],
            "reported_at": _coerce_int(candidate.get("reported_at"), 0),
        }
    return {"sessions": [], "reported_at": 0}


def _normalize_state_code(value: Any) -> str:
    text = _clean_text(value).lower().replace(" ", "_").replace("-", "_")
    aliases = {
        "active": "active",
        "act": "active",
        "running": "active",
        "locked": "locked",
        "lock": "locked",
        "disc": "disconnected",
        "disconnected": "disconnected",
        "idle": "idle",
    }
    normalized = aliases.get(text, text)
    if normalized in STATE_LABELS:
        return normalized
    return "unknown"


def _normalize_protocol(value: Any, *, session_name: str = "") -> str:
    text = _clean_text(value).lower()
    if text in {"rdp", "remote_desktop"}:
        return "rdp"
    if text in {"console", "local"}:
        return "console"
    name = _clean_text(session_name).lower()
    if name.startswith("rdp-") or name.startswith("ica-"):
        return "rdp"
    if name == "console":
        return "console"
    return "other"


def _normalize_session_entry(item: Any) -> Dict[str, Any] | None:
    if not isinstance(item, dict):
        return None
    username = _clean_text(item.get("username") or item.get("user") or item.get("name"))
    session_name = _clean_text(item.get("session_name") or item.get("sessionName"))
    session_id = _coerce_int(item.get("session_id") or item.get("id"), 0)
    state_code = _normalize_state_code(item.get("state_code") or item.get("state"))
    protocol = _normalize_protocol(item.get("protocol"), session_name=session_name)
    is_rdp = _coerce_bool(item.get("is_rdp"), protocol == "rdp")
    if protocol == "rdp":
        is_rdp = True
    return {
        "session_id": session_id if session_id > 0 else 0,
        "username": username,
        "session_name": session_name,
        "state_code": state_code,
        "state": STATE_LABELS.get(state_code, "Unknown"),
        "protocol": protocol,
        "is_rdp": is_rdp,
    }


def normalize_device_sessions(raw: Any, *, default_reported_at: int = 0) -> Dict[str, Any]:
    payload = _normalize_payload_shape(raw)
    reported_at = _coerce_int(payload.get("reported_at"), default_reported_at)
    deduped: Dict[str, Dict[str, Any]] = {}
    for item in payload.get("sessions") or []:
        normalized = _normalize_session_entry(item)
        if not normalized:
            continue
        key = "{0}:{1}:{2}".format(
            _coerce_int(normalized.get("session_id"), 0),
            _clean_text(normalized.get("username")).lower(),
            _clean_text(normalized.get("session_name")).lower(),
        )
        deduped[key] = normalized
    return {
        "sessions": sorted(
            deduped.values(),
            key=lambda entry: (
                str(entry.get("username") or "").lower(),
                _coerce_int(entry.get("session_id"), 0),
                str(entry.get("session_name") or "").lower(),
            ),
        ),
        "reported_at": reported_at,
    }


def serialize_device_sessions(payload: Any) -> str:
    normalized = normalize_device_sessions(payload)
    return json.dumps(normalized, ensure_ascii=True, sort_keys=True)
