"""Software icon cache helpers for Borealis device inventory."""

from __future__ import annotations

import base64
import hashlib
import json
import logging
import re
from pathlib import Path
import threading
import time
from typing import Any, Dict, List, Optional


_SOFTWARE_ICON_HASH_RE = re.compile(r"^[0-9a-f]{64}$")
_ALLOWED_SOFTWARE_ICON_MIME_TYPES = {
    "image/png",
    "image/x-icon",
    "image/vnd.microsoft.icon",
}
_MAX_SOFTWARE_ICON_BYTES = 512 * 1024
_SOFTWARE_ICON_OVERRIDES_PATH = Path(__file__).resolve().with_name("software_icons_overrides.json")
_SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS: Optional[int] = None
_SOFTWARE_ICON_OVERRIDES_CACHE: Dict[str, List[Dict[str, Any]]] = {
    "windows_icon_overrides": [],
}
_SOFTWARE_ICON_OVERRIDES_WRITE_LOCK = threading.RLock()

logger = logging.getLogger(__name__)


def normalize_text(value: Any) -> str:
    try:
        return str(value or "").strip()
    except Exception:
        return ""


def _coerce_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    text = normalize_text(value).lower()
    return text in {"1", "true", "yes", "on"}


def _normalize_icon_override_rows(value: Any) -> List[Dict[str, Any]]:
    rows = value if isinstance(value, list) else []
    normalized: List[Dict[str, Any]] = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        normalized.append({str(key): item for key, item in row.items() if normalize_text(key)})
    return normalized


def _write_json_atomic(path: Path, payload: Dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp_path = path.with_suffix(path.suffix + ".tmp")
    temp_path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")
    temp_path.replace(path)


def canonicalize_software_icon_override_resource(value: Any) -> str:
    text = normalize_text(value)
    if not text:
        return ""
    lowered = text.lower()
    if lowered.endswith(".ico") or re.match(r'^\s*"?.+?\.ico"?\s*$', text, re.IGNORECASE):
        return text.strip().strip('"')
    resource_match = re.match(
        r'^\s*"?(?P<path>.+?\.(?:exe|dll|icl|cpl|ocx|scr))"?\s*(?:,\s*(?P<index>-?\d+))?\s*$',
        text,
        re.IGNORECASE,
    )
    if not resource_match:
        return ""
    resource_path = normalize_text(resource_match.group("path")).strip('"')
    if not resource_path:
        return ""
    try:
        icon_index = int(normalize_text(resource_match.group("index")) or "0")
    except Exception:
        icon_index = 0
    return f"{resource_path},{icon_index}"


def upsert_software_icon_override(rule: Dict[str, Any]) -> Dict[str, Any]:
    rule_id = normalize_text((rule or {}).get("rule_id"))
    if not rule_id:
        raise ValueError("rule_id is required")
    clear_icon = _coerce_bool((rule or {}).get("clear_icon") or (rule or {}).get("remove_icon"))
    display_icon = ""
    if not clear_icon:
        display_icon = canonicalize_software_icon_override_resource(
            (rule or {}).get("display_icon") or (rule or {}).get("icon_location")
        )
    if not clear_icon and not display_icon:
        raise ValueError("display_icon must be a valid EXE, DLL, ICO, or icon resource path")

    normalized_rule: Dict[str, Any] = {
        "rule_id": rule_id,
    }
    if clear_icon:
        normalized_rule["clear_icon"] = True
    else:
        normalized_rule["display_icon"] = display_icon
    for key in ("source", "name", "version", "product_code"):
        value = normalize_text((rule or {}).get(key))
        if value:
            normalized_rule[key] = value
    for key in ("publisher_contains_any", "name_contains_any", "install_location_contains_any"):
        raw_values = (rule or {}).get(key)
        if isinstance(raw_values, list):
            values = [normalize_text(item) for item in raw_values if normalize_text(item)]
            if values:
                normalized_rule[key] = values

    with _SOFTWARE_ICON_OVERRIDES_WRITE_LOCK:
        payload = {
            "windows_icon_overrides": load_software_icon_overrides(),
        }
        rows = [dict(item) for item in payload["windows_icon_overrides"] if isinstance(item, dict)]
        replaced = False
        next_rows: List[Dict[str, Any]] = []
        for existing in rows:
            if normalize_text(existing.get("rule_id")) == rule_id:
                next_rows.append(dict(normalized_rule))
                replaced = True
            else:
                next_rows.append(existing)
        if not replaced:
            next_rows.append(dict(normalized_rule))
        payload["windows_icon_overrides"] = next_rows
        _write_json_atomic(_SOFTWARE_ICON_OVERRIDES_PATH, payload)
        global _SOFTWARE_ICON_OVERRIDES_CACHE, _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS
        _SOFTWARE_ICON_OVERRIDES_CACHE = {
            "windows_icon_overrides": _normalize_icon_override_rows(next_rows),
        }
        try:
            _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS = _SOFTWARE_ICON_OVERRIDES_PATH.stat().st_mtime_ns
        except OSError:
            _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS = None
        return dict(normalized_rule)


def load_software_icon_overrides() -> List[Dict[str, Any]]:
    global _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS, _SOFTWARE_ICON_OVERRIDES_CACHE

    try:
        current_mtime_ns = _SOFTWARE_ICON_OVERRIDES_PATH.stat().st_mtime_ns
    except OSError:
        current_mtime_ns = None

    if current_mtime_ns is not None and _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS == current_mtime_ns:
        return list(_SOFTWARE_ICON_OVERRIDES_CACHE.get("windows_icon_overrides") or [])

    next_cache = {
        "windows_icon_overrides": [],
    }
    if current_mtime_ns is None:
        _SOFTWARE_ICON_OVERRIDES_CACHE = next_cache
        _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS = None
        return list(_SOFTWARE_ICON_OVERRIDES_CACHE["windows_icon_overrides"])

    try:
        with _SOFTWARE_ICON_OVERRIDES_PATH.open("r", encoding="utf-8") as handle:
            payload = json.load(handle)
    except Exception as exc:
        logger.warning("Failed to load software icon overrides from %s: %s", _SOFTWARE_ICON_OVERRIDES_PATH, exc)
        _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS = current_mtime_ns
        return list(_SOFTWARE_ICON_OVERRIDES_CACHE.get("windows_icon_overrides") or [])

    if isinstance(payload, dict):
        next_cache["windows_icon_overrides"] = _normalize_icon_override_rows(payload.get("windows_icon_overrides"))
    else:
        logger.warning("Software icon overrides payload is not an object: %s", _SOFTWARE_ICON_OVERRIDES_PATH)
        _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS = current_mtime_ns
        return list(_SOFTWARE_ICON_OVERRIDES_CACHE.get("windows_icon_overrides") or [])

    _SOFTWARE_ICON_OVERRIDES_CACHE = next_cache
    _SOFTWARE_ICON_OVERRIDES_CACHE_MTIME_NS = current_mtime_ns
    return list(_SOFTWARE_ICON_OVERRIDES_CACHE["windows_icon_overrides"])


def normalize_software_icon_hash(value: Any) -> str:
    text = normalize_text(value).lower()
    return text if _SOFTWARE_ICON_HASH_RE.match(text) else ""


def normalize_software_icon_payloads(raw: Any) -> List[Dict[str, Any]]:
    entries = raw if isinstance(raw, list) else []
    normalized: Dict[str, Dict[str, Any]] = {}
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        data_base64 = normalize_text(
            entry.get("data_base64")
            or entry.get("icon_data_base64")
            or entry.get("data")
        )
        if not data_base64:
            continue
        try:
            icon_bytes = base64.b64decode(data_base64, validate=True)
        except Exception:
            continue
        if not icon_bytes or len(icon_bytes) > _MAX_SOFTWARE_ICON_BYTES:
            continue
        icon_hash = hashlib.sha256(icon_bytes).hexdigest()
        mime_type = normalize_text(entry.get("mime_type")).lower() or "image/png"
        if mime_type not in _ALLOWED_SOFTWARE_ICON_MIME_TYPES:
            mime_type = "image/png"
        normalized[icon_hash] = {
            "icon_hash": icon_hash,
            "mime_type": mime_type,
            "icon_bytes": icon_bytes,
            "byte_size": len(icon_bytes),
        }
    return sorted(normalized.values(), key=lambda item: item["icon_hash"])


def upsert_software_icon_assets(cur, payloads: List[Dict[str, Any]]) -> int:
    count = 0
    now_ts = int(time.time())
    for payload in payloads or []:
        icon_hash = normalize_software_icon_hash(payload.get("icon_hash"))
        icon_bytes = payload.get("icon_bytes")
        if not icon_hash or not isinstance(icon_bytes, (bytes, bytearray)) or not icon_bytes:
            continue
        mime_type = normalize_text(payload.get("mime_type")).lower() or "image/png"
        if mime_type not in _ALLOWED_SOFTWARE_ICON_MIME_TYPES:
            mime_type = "image/png"
        byte_size = int(payload.get("byte_size") or len(icon_bytes))
        cur.execute(
            """
            INSERT INTO software_icon_assets (
                icon_hash,
                mime_type,
                icon_bytes,
                byte_size,
                created_at,
                updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?)
            ON CONFLICT(icon_hash) DO UPDATE SET
                mime_type = EXCLUDED.mime_type,
                icon_bytes = EXCLUDED.icon_bytes,
                byte_size = EXCLUDED.byte_size,
                updated_at = EXCLUDED.updated_at
            """,
            (
                icon_hash,
                mime_type,
                bytes(icon_bytes),
                byte_size,
                now_ts,
                now_ts,
            ),
        )
        count += 1
    return count


def load_software_icon_asset(db_conn_factory, icon_hash: Any) -> Optional[Dict[str, Any]]:
    normalized_hash = normalize_software_icon_hash(icon_hash)
    if not normalized_hash:
        return None
    conn = None
    try:
        conn = db_conn_factory()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT icon_hash, mime_type, icon_bytes, byte_size, updated_at
              FROM software_icon_assets
             WHERE icon_hash = ?
             LIMIT 1
            """,
            (normalized_hash,),
        )
        row = cur.fetchone()
        if not row:
            return None
        return {
            "icon_hash": normalize_software_icon_hash(row[0]),
            "mime_type": normalize_text(row[1]).lower() or "image/png",
            "icon_bytes": bytes(row[2] or b""),
            "byte_size": int(row[3] or 0),
            "updated_at": int(row[4] or 0),
        }
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass
