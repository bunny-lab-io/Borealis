# ======================================================
# Data\Engine\services\API\devices\service_inventory.py
# Description: Normalization and merge helpers for cached device service inventory.
#
# API Endpoints (if applicable): None
# ======================================================

"""Helpers for cached device service inventory and pending control actions."""
from __future__ import annotations

import json
from typing import Any, Dict, List, Optional


STATUS_LABELS = {
    "running": "Running",
    "stopped": "Stopped",
    "starting": "Starting",
    "stopping": "Stopping",
    "paused": "Paused",
    "failed": "Failed",
    "unknown": "Unknown",
}

ACTION_LABELS = {
    "start": "Starting...",
    "stop": "Stopping...",
    "restart": "Restarting...",
}

DESIRED_STATUS_BY_ACTION = {
    "start": "running",
    "stop": "stopped",
    "restart": "running",
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


def _service_id_for_name(name: Any) -> str:
    return _clean_text(name).lower()


def _normalize_status_code(value: Any) -> str:
    text = _clean_text(value).lower().replace(" ", "_").replace("-", "_")
    aliases = {
        "active": "running",
        "running": "running",
        "up": "running",
        "online": "running",
        "inactive": "stopped",
        "stopped": "stopped",
        "dead": "stopped",
        "down": "stopped",
        "disabled": "stopped",
        "activating": "starting",
        "start_pending": "starting",
        "starting": "starting",
        "reloading": "starting",
        "deactivating": "stopping",
        "stop_pending": "stopping",
        "stopping": "stopping",
        "paused": "paused",
        "pause_pending": "paused",
        "continue_pending": "starting",
        "failed": "failed",
        "error": "failed",
    }
    normalized = aliases.get(text, text)
    if normalized in STATUS_LABELS:
        return normalized
    return "unknown"


def normalize_service_action(value: Any) -> str:
    text = _clean_text(value).lower()
    if text in ACTION_LABELS:
        return text
    return ""


def desired_status_for_action(action: Any) -> str:
    return DESIRED_STATUS_BY_ACTION.get(normalize_service_action(action), "")


def action_label(action: Any) -> str:
    return ACTION_LABELS.get(normalize_service_action(action), "")


def _normalize_payload_shape(raw: Any) -> Dict[str, Any]:
    candidate = raw
    if isinstance(candidate, str):
        text = candidate.strip()
        if not text:
            return {"services": [], "reported_at": 0}
        try:
            candidate = json.loads(text)
        except Exception:
            return {"services": [], "reported_at": 0}
    if isinstance(candidate, list):
        return {"services": candidate, "reported_at": 0}
    if isinstance(candidate, dict):
        services = candidate.get("services")
        return {
            "services": services if isinstance(services, list) else [],
            "reported_at": _coerce_int(candidate.get("reported_at"), 0),
        }
    return {"services": [], "reported_at": 0}


def _normalized_service_entry(item: Any, *, default_captured_at: int = 0) -> Optional[Dict[str, Any]]:
    if not isinstance(item, dict):
        return None
    name = (
        _clean_text(item.get("name"))
        or _clean_text(item.get("service_name"))
        or _clean_text(item.get("id"))
    )
    if not name:
        return None
    status_code = _normalize_status_code(item.get("status_code") or item.get("status") or item.get("state"))
    captured_at = _coerce_int(item.get("captured_at") or item.get("reported_at"), default_captured_at)
    pending_action = normalize_service_action(item.get("pending_action") or item.get("action"))
    desired_status = _normalize_status_code(
        item.get("desired_status") or desired_status_for_action(pending_action) or ""
    )
    if desired_status not in {"running", "stopped"}:
        desired_status = desired_status_for_action(pending_action)
    return {
        "service_id": _clean_text(item.get("service_id")) or _service_id_for_name(name),
        "name": name,
        "display_name": _clean_text(item.get("display_name") or item.get("displayName") or item.get("label")),
        "description": _clean_text(item.get("description") or item.get("detail")),
        "status_code": status_code,
        "status": STATUS_LABELS.get(status_code, "Unknown"),
        "captured_at": captured_at,
        "pending_action": pending_action,
        "desired_status": desired_status,
        "pending_requested_at": _coerce_int(item.get("pending_requested_at"), 0),
        "pending_requested_by": _clean_text(item.get("pending_requested_by")),
    }


def normalize_device_services(raw: Any, *, default_captured_at: int = 0) -> Dict[str, Any]:
    payload = _normalize_payload_shape(raw)
    reported_at = _coerce_int(payload.get("reported_at"), default_captured_at)
    services: Dict[str, Dict[str, Any]] = {}
    for item in payload.get("services") or []:
        normalized = _normalized_service_entry(item, default_captured_at=reported_at or default_captured_at)
        if not normalized:
            continue
        services[normalized["service_id"]] = normalized
        reported_at = max(reported_at, _coerce_int(normalized.get("captured_at"), 0))
    normalized_services = sorted(
        services.values(),
        key=lambda entry: (
            str(entry.get("display_name") or entry.get("name") or "").lower(),
            str(entry.get("name") or "").lower(),
            str(entry.get("description") or "").lower(),
        ),
    )
    return {
        "services": normalized_services,
        "reported_at": reported_at,
    }


def _pending_reached_target(entry: Dict[str, Any], *, desired_status: str, requested_at: int) -> bool:
    if not desired_status or requested_at <= 0:
        return False
    current_status = _normalize_status_code(entry.get("status_code") or entry.get("status"))
    captured_at = _coerce_int(entry.get("captured_at"), 0)
    return captured_at >= requested_at and current_status == desired_status


def merge_device_services(existing_raw: Any, incoming_raw: Any) -> Dict[str, Any]:
    existing = normalize_device_services(existing_raw)
    incoming = normalize_device_services(incoming_raw)
    existing_by_id = {entry["service_id"]: entry for entry in existing.get("services") or []}
    merged_services: List[Dict[str, Any]] = []

    for incoming_entry in incoming.get("services") or []:
        existing_entry = existing_by_id.get(incoming_entry.get("service_id") or "")
        merged_entry = dict(incoming_entry)
        pending_action = normalize_service_action(
            incoming_entry.get("pending_action") or (existing_entry or {}).get("pending_action")
        )
        desired_status = desired_status_for_action(pending_action)
        if not desired_status:
            desired_status = _clean_text(
                incoming_entry.get("desired_status") or (existing_entry or {}).get("desired_status")
            ).lower()
        requested_at = max(
            _coerce_int((existing_entry or {}).get("pending_requested_at"), 0),
            _coerce_int(incoming_entry.get("pending_requested_at"), 0),
        )
        requested_by = _clean_text(incoming_entry.get("pending_requested_by")) or _clean_text(
            (existing_entry or {}).get("pending_requested_by")
        )
        display_name = _clean_text(incoming_entry.get("display_name")) or _clean_text(
            (existing_entry or {}).get("display_name")
        )
        if display_name:
            merged_entry["display_name"] = display_name

        if pending_action and not _pending_reached_target(
            merged_entry,
            desired_status=desired_status,
            requested_at=requested_at,
        ):
            merged_entry["pending_action"] = pending_action
            merged_entry["desired_status"] = desired_status
            merged_entry["pending_requested_at"] = requested_at
            merged_entry["pending_requested_by"] = requested_by
        else:
            merged_entry["pending_action"] = ""
            merged_entry["desired_status"] = ""
            merged_entry["pending_requested_at"] = 0
            merged_entry["pending_requested_by"] = ""
        merged_services.append(merged_entry)

    reported_at = max(
        _coerce_int(existing.get("reported_at"), 0),
        _coerce_int(incoming.get("reported_at"), 0),
        max((_coerce_int(entry.get("captured_at"), 0) for entry in merged_services), default=0),
    )
    return {
        "services": sorted(
            merged_services,
            key=lambda entry: (
                str(entry.get("display_name") or entry.get("name") or "").lower(),
                str(entry.get("name") or "").lower(),
                str(entry.get("description") or "").lower(),
            ),
        ),
        "reported_at": reported_at,
    }


def mark_service_control_pending(
    existing_raw: Any,
    service_name: str,
    action: str,
    *,
    requested_at: int,
    requested_by: Optional[str] = None,
) -> Optional[Dict[str, Any]]:
    normalized = normalize_device_services(existing_raw)
    service_id = _service_id_for_name(service_name)
    pending_action = normalize_service_action(action)
    desired_status = desired_status_for_action(pending_action)
    if not service_id or not pending_action or not desired_status:
        return None

    updated = False
    services: List[Dict[str, Any]] = []
    for entry in normalized.get("services") or []:
        next_entry = dict(entry)
        if next_entry.get("service_id") == service_id:
            next_entry["pending_action"] = pending_action
            next_entry["desired_status"] = desired_status
            next_entry["pending_requested_at"] = _coerce_int(requested_at, 0)
            next_entry["pending_requested_by"] = _clean_text(requested_by)
            updated = True
        services.append(next_entry)
    if not updated:
        return None
    return {
        "services": services,
        "reported_at": max(
            _coerce_int(normalized.get("reported_at"), 0),
            max((_coerce_int(entry.get("captured_at"), 0) for entry in services), default=0),
        ),
    }


def serialize_device_services(payload: Any) -> str:
    normalized = normalize_device_services(payload)
    return json.dumps(normalized, ensure_ascii=True, sort_keys=True)
