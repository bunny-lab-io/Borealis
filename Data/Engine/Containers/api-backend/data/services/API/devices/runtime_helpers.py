# ======================================================
# Data\Engine\services\API\devices\runtime_helpers.py
# Description: Shared device API helpers retained after Go tunnel runtime cutover.
#
# API Endpoints (if applicable): None
# ======================================================

"""Shared helpers for Python device route modules that remain behind the Go gateway."""
from __future__ import annotations

import os
import re
from typing import Any, Dict, Optional, Tuple

from flask import request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....auth.guid_utils import normalize_guid
from ...auth.secrets import require_app_secret

if False:  # pragma: no cover - import cycle hint for type checkers
    from .. import EngineServiceAdapters


def _current_user(app) -> Optional[Dict[str, str]]:
    username = session.get("username")
    role = session.get("role") or "User"
    if username:
        return {"username": username, "role": role}

    token = None
    auth_header = request.headers.get("Authorization") or ""
    if auth_header.lower().startswith("bearer "):
        token = auth_header.split(" ", 1)[1].strip()
    if not token:
        token = request.cookies.get("borealis_auth")
    if not token:
        return None

    try:
        serializer = URLSafeTimedSerializer(require_app_secret(app), salt="borealis-auth")
        token_ttl = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
        data = serializer.loads(token, max_age=token_ttl)
        username = data.get("u")
        role = data.get("r") or "User"
        if username:
            return {"username": username, "role": role}
    except (BadSignature, SignatureExpired, Exception):
        return None
    return None


def _require_login(app) -> Optional[Tuple[Dict[str, Any], int]]:
    user = _current_user(app)
    if not user:
        return {"error": "unauthorized"}, 401
    return None


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _guid_candidate(value: Any) -> str:
    text = _normalize_text(value)
    if not text:
        return ""
    normalized = normalize_guid(text)
    if not normalized:
        return ""
    return normalized if normalized == text.strip().strip("{}").upper() else ""


_AGENT_ID_HOST_PATTERN = re.compile(
    r"^(?P<hostname>.+)_(?P<guid>[0-9A-F-]+)_(?P<context>[A-Z0-9_-]+)$",
    re.IGNORECASE,
)


def _guid_from_agent_id(value: Any) -> str:
    text = _normalize_text(value)
    if not text:
        return ""
    match = _AGENT_ID_HOST_PATTERN.match(text)
    if match:
        return _guid_candidate(match.group("guid"))
    parts = text.rsplit("_", 2)
    if len(parts) == 3:
        return _guid_candidate(parts[1])
    return ""


def _load_device_agent_binding(
    adapters: "EngineServiceAdapters",
    *,
    guid: Any = None,
    agent_id: Any = None,
) -> Dict[str, str]:
    guid_value = _guid_candidate(guid)
    agent_id_value = _normalize_text(agent_id)
    if not guid_value and not agent_id_value:
        return {"guid": "", "hostname": "", "agent_id": ""}

    conn_factory = getattr(adapters, "db_conn_factory", None)
    if not callable(conn_factory):
        return {"guid": "", "hostname": "", "agent_id": ""}

    conn = None
    try:
        conn = conn_factory()
        cur = conn.cursor()
        if guid_value:
            cur.execute(
                "SELECT guid, hostname, agent_id FROM devices WHERE UPPER(guid) = ? ORDER BY last_seen DESC LIMIT 1",
                (guid_value,),
            )
        else:
            cur.execute(
                "SELECT guid, hostname, agent_id FROM devices WHERE LOWER(agent_id) = LOWER(?) ORDER BY last_seen DESC LIMIT 1",
                (agent_id_value,),
            )
        row = cur.fetchone()
        return {
            "guid": _guid_candidate(row[0] if row else ""),
            "hostname": _normalize_text(row[1] if row else ""),
            "agent_id": _normalize_text(row[2] if row else ""),
        }
    except Exception:
        return {"guid": "", "hostname": "", "agent_id": ""}
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def _resolve_requested_agent_id(
    adapters: "EngineServiceAdapters",
    requested_agent_id: Any,
    *,
    expected_guid: Any = None,
) -> str:
    agent_id = _normalize_text(requested_agent_id)
    if not agent_id:
        return ""

    expected_guid_value = _guid_candidate(expected_guid)
    if expected_guid_value and _guid_from_agent_id(agent_id) == expected_guid_value:
        return agent_id

    guid = _guid_candidate(agent_id)
    binding = _load_device_agent_binding(adapters, guid=guid) if guid else _load_device_agent_binding(adapters, agent_id=agent_id)
    resolved = _normalize_text(binding.get("agent_id"))
    return resolved or agent_id
