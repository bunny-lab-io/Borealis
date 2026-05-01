# ======================================================
# Data\Engine\services\API\devices\process_inventory.py
# Description: Normalization helpers for cached device process snapshots.
#
# API Endpoints (if applicable): None
# ======================================================

"""Helpers for cached device process inventory."""
from __future__ import annotations

import json
from typing import Any, Dict, List


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


def _normalize_payload_shape(raw: Any) -> Dict[str, Any]:
    candidate = raw
    if isinstance(candidate, str):
        text = candidate.strip()
        if not text:
            return {"processes": [], "reported_at": 0}
        try:
            candidate = json.loads(text)
        except Exception:
            return {"processes": [], "reported_at": 0}
    if isinstance(candidate, list):
        return {"processes": candidate, "reported_at": 0}
    if isinstance(candidate, dict):
        processes = candidate.get("processes")
        return {
            "processes": processes if isinstance(processes, list) else [],
            "reported_at": _coerce_int(candidate.get("reported_at"), 0),
        }
    return {"processes": [], "reported_at": 0}


def _normalize_process_entry(item: Any) -> Dict[str, Any] | None:
    if not isinstance(item, dict):
        return None
    name = _clean_text(item.get("name") or item.get("process_name") or item.get("image_name"))
    if not name:
        return None
    count = max(1, _coerce_int(item.get("count"), 1))
    return {
        "name": name,
        "count": count,
    }


def normalize_device_processes(raw: Any, *, default_reported_at: int = 0) -> Dict[str, Any]:
    payload = _normalize_payload_shape(raw)
    reported_at = _coerce_int(payload.get("reported_at"), default_reported_at)
    deduped: Dict[str, Dict[str, Any]] = {}
    for item in payload.get("processes") or []:
        normalized = _normalize_process_entry(item)
        if not normalized:
            continue
        key = _clean_text(normalized.get("name")).lower()
        if not key:
            continue
        existing = deduped.get(key)
        if existing is None:
            deduped[key] = normalized
            continue
        existing["count"] = max(_coerce_int(existing.get("count"), 1), _coerce_int(normalized.get("count"), 1))
    return {
        "processes": sorted(
            deduped.values(),
            key=lambda entry: str(entry.get("name") or "").lower(),
        ),
        "reported_at": reported_at,
    }


def serialize_device_processes(payload: Any) -> str:
    normalized = normalize_device_processes(payload)
    return json.dumps(normalized, ensure_ascii=True, sort_keys=True)
