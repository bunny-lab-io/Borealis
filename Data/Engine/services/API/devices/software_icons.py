"""Software icon cache helpers for Borealis device inventory."""

from __future__ import annotations

import base64
import hashlib
import re
import time
from typing import Any, Dict, List, Optional


_SOFTWARE_ICON_HASH_RE = re.compile(r"^[0-9a-f]{64}$")
_ALLOWED_SOFTWARE_ICON_MIME_TYPES = {
    "image/png",
    "image/x-icon",
    "image/vnd.microsoft.icon",
}
_MAX_SOFTWARE_ICON_BYTES = 512 * 1024


def normalize_text(value: Any) -> str:
    try:
        return str(value or "").strip()
    except Exception:
        return ""


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
