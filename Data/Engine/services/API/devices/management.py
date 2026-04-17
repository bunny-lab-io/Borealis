# ======================================================
# Data\Engine\services\API\devices\management.py
# Description: Device inventory, list view, site management, and repository hash endpoints for the Engine API transition layer.
#
# API Endpoints (if applicable):
# - POST /api/agent/details (Device Authenticated) - Ingests hardware and inventory payloads from enrolled agents.
# - GET /api/agents (Token Authenticated) - Lists online collectors grouped by hostname and run context.
# - GET /api/devices (Token Authenticated) - Returns a summary list of known devices for the WebUI transition.
# - GET /api/devices/search (Token Authenticated) - Returns hostname search matches scoped to the operator's assigned sites unless the operator is an admin.
# - GET /api/devices/<guid> (Token Authenticated) - Retrieves a single device record by GUID, including summary fields.
# - POST /api/devices/<guid>/purge (Token Authenticated (Admin)) - Holistically purges a device, its trust records, and scheduled-job references.
# - GET /api/device/details/<hostname> (Token Authenticated) - Returns full device details keyed by hostname.
# - POST /api/device/description/<hostname> (Token Authenticated) - Updates the human-readable description for a device.
# - GET /api/device_list_views (Token Authenticated) - Lists saved device table view definitions.
# - GET /api/device_list_views/<int:view_id> (Token Authenticated) - Retrieves a specific saved device table view definition.
# - POST /api/device_list_views (Token Authenticated) - Creates a custom device list view for the signed-in operator.
# - PUT /api/device_list_views/<int:view_id> (Token Authenticated) - Updates an existing device list view definition.
# - DELETE /api/device_list_views/<int:view_id> (Token Authenticated) - Deletes a saved device list view.
# - GET /api/sites (Token Authenticated) - Lists known sites, their summary metadata, and public install-command metadata.
# - POST /api/sites (Token Authenticated (Admin)) - Creates a new site for grouping devices.
# - POST /api/sites/delete (Token Authenticated (Admin)) - Deletes one or more sites by identifier.
# - GET /api/sites/device_map (Token Authenticated) - Provides hostname to site assignment mapping data.
# - POST /api/sites/assign (Token Authenticated (Admin)) - Assigns a set of devices to a given site.
# - POST /api/sites/rename (Token Authenticated (Admin)) - Renames an existing site record.
# - GET /api/repo/current_hash (Device or Token Authenticated) - Fetches the current agent repository hash (with caching).
# - GET/POST /api/agent/hash (Device Authenticated) - Retrieves or updates an agent hash record bound to the authenticated device.
# - GET /api/agent/hash_list (Token Authenticated (Admin + Loopback)) - Returns stored agent hash metadata for localhost diagnostics.
# ======================================================

"""Device management endpoints for the Borealis Engine API."""
from __future__ import annotations

import json
import logging
import os
import secrets
from Data.Engine.db import dbapi as sqlite3
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, session, g
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....auth import device_purge_state
from ....auth.guid_utils import normalize_guid
from ....auth.device_auth import DeviceAuthError, require_device_auth
from ....public_endpoints import public_base_url as resolve_public_base_url
from ....public_endpoints import public_hostname as resolve_public_hostname
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ..scheduled_jobs.targets import prune_device_targets
from .agent_role_health import (
    merge_agent_role_health,
    normalize_agent_role_health,
    serialize_agent_role_health,
)
from .process_inventory import normalize_device_processes, serialize_device_processes
from .session_inventory import normalize_device_sessions, serialize_device_sessions
from .service_inventory import (
    merge_device_services,
    normalize_device_services,
    serialize_device_services,
)

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def _safe_json(raw: Optional[str], default: Any) -> Any:
    if raw is None:
        return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    try:
        parsed = json.loads(raw)
    except Exception:
        return default
    if isinstance(default, list) and isinstance(parsed, list):
        return parsed
    if isinstance(default, dict) and isinstance(parsed, dict):
        return parsed
    return default


def _ts_to_iso(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        from datetime import datetime, timezone

        return datetime.fromtimestamp(int(ts), timezone.utc).isoformat()
    except Exception:
        return ""


def _status_from_last_seen(last_seen: Optional[int]) -> str:
    if not last_seen:
        return "Offline"
    try:
        if (time.time() - float(last_seen)) <= 300:
            return "Online"
    except Exception:
        pass
    return "Offline"


def _escape_like(value: str) -> str:
    return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


def _normalize_service_mode(value: Any, agent_id: Optional[str] = None) -> str:
    try:
        text = str(value or "").strip().lower()
    except Exception:
        text = ""
    if not text and agent_id:
        try:
            aid = agent_id.lower()
            if "-svc-" in aid or aid.endswith("-svc"):
                return "system"
        except Exception:
            pass
    if text in {"system", "svc", "service", "system_service"}:
        return "system"
    if text in {"interactive", "currentuser", "user", "current_user"}:
        return "currentuser"
    return "currentuser"


def _is_internal_request(remote_addr: Optional[str]) -> bool:
    addr = (remote_addr or "").strip()
    if not addr:
        return False
    if addr in {"127.0.0.1", "::1"}:
        return True
    if addr.startswith("127."):
        return True
    if addr.startswith("::ffff:"):
        mapped = addr.split("::ffff:", 1)[-1]
        if mapped in {"127.0.0.1"} or mapped.startswith("127."):
            return True
    return False


def _generate_install_code() -> str:
    raw = secrets.token_hex(16).upper()
    return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))


def _row_to_site(row: Tuple[Any, ...]) -> Dict[str, Any]:
    return {
        "id": row[0],
        "name": row[1],
        "description": row[2] or "",
        "created_at": row[3] or 0,
        "device_count": row[4] or 0,
        "enrollment_code": row[5] or "",
    }


DEVICE_TABLE = "devices"
_DEVICE_JSON_LIST_FIELDS: Dict[str, Any] = {
    "memory": [],
    "network": [],
    "software": [],
    "services": [],
    "storage": [],
}
_DEVICE_JSON_OBJECT_FIELDS: Dict[str, Any] = {
    "cpu": {},
    "sessions": {"sessions": [], "reported_at": 0},
    "processes": {"processes": [], "reported_at": 0},
}
_SOFTWARE_SOURCE_ALIASES: Dict[str, str] = {
    "local": "local_installed",
    "installed": "local_installed",
    "local_installed": "local_installed",
    "registry": "local_installed",
    "uninstall_registry": "local_installed",
    "appx": "windows_store",
    "ms_store": "windows_store",
    "windows_store": "windows_store",
    "store": "windows_store",
    "dpkg": "dpkg",
    "rpm": "rpm",
}


def _is_empty(value: Any) -> bool:
    return value is None or value == "" or value == [] or value == {}


def _deep_merge_preserve(prev: Dict[str, Any], incoming: Dict[str, Any]) -> Dict[str, Any]:
    out: Dict[str, Any] = dict(prev or {})
    for key, value in (incoming or {}).items():
        if isinstance(value, dict):
            out[key] = _deep_merge_preserve(out.get(key) or {}, value)
        elif isinstance(value, list):
            if value:
                out[key] = value
        else:
            if not _is_empty(value):
                out[key] = value
    return out


def _serialize_device_json(value: Any, default: Any) -> str:
    candidate = value
    if candidate is None:
        candidate = default
    if not isinstance(candidate, (list, dict)):
        candidate = default
    try:
        return json.dumps(candidate)
    except Exception:
        try:
            return json.dumps(default)
        except Exception:
            return "{}" if isinstance(default, dict) else "[]"


def _clean_device_str(value: Any) -> Optional[str]:
    if value is None:
        return None
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        text = str(value)
    elif isinstance(value, str):
        text = value
    else:
        try:
            text = str(value)
        except Exception:
            return None
    text = text.strip()
    return text or None


def _normalize_software_source(value: Any) -> str:
    text = _clean_device_str(value)
    if not text:
        return "local_installed"
    return _SOFTWARE_SOURCE_ALIASES.get(text.strip().lower(), "local_installed")


def _normalize_software_inventory(raw: Any) -> List[Dict[str, Any]]:
    entries = raw if isinstance(raw, list) else []
    normalized: List[Dict[str, Any]] = []
    seen: set[Tuple[str, str, str]] = set()
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        name = _clean_device_str(entry.get("name"))
        if not name:
            continue
        version = _clean_device_str(entry.get("version")) or ""
        source = _normalize_software_source(entry.get("source"))
        metadata = entry.get("metadata") if isinstance(entry.get("metadata"), dict) else {}
        if not metadata:
            metadata = {
                str(key): value
                for key, value in entry.items()
                if key not in {"name", "version", "source", "metadata"} and value not in (None, "", [], {})
            }
        key = (name.strip().lower(), version.strip().lower(), source)
        if key in seen:
            continue
        seen.add(key)
        normalized.append(
            {
                "name": name,
                "version": version,
                "source": source,
                "metadata": metadata if isinstance(metadata, dict) else {},
            }
        )
    normalized.sort(
        key=lambda item: (
            str(item.get("name") or "").lower(),
            str(item.get("source") or "").lower(),
            str(item.get("version") or "").lower(),
        )
    )
    return normalized


def _sync_device_software_inventory(cur: sqlite3.Cursor, device_guid: Optional[str], software_entries: Any) -> None:
    normalized_guid = _clean_device_str(device_guid)
    if normalized_guid:
        try:
            normalized_guid = normalize_guid(normalized_guid)
        except Exception:
            pass
    if not normalized_guid:
        return
    normalized_entries = _normalize_software_inventory(software_entries)
    cur.execute("DELETE FROM device_software_inventory WHERE device_guid = ?", (normalized_guid,))
    if not normalized_entries:
        return
    now_ts = int(time.time())
    cur.executemany(
        """
        INSERT INTO device_software_inventory(
            device_guid,
            name,
            name_normalized,
            version,
            source,
            captured_at,
            metadata_json
        ) VALUES (?,?,?,?,?,?,?)
        """,
        [
            (
                normalized_guid,
                entry["name"],
                str(entry["name"]).strip().lower(),
                entry["version"],
                entry["source"],
                now_ts,
                json.dumps(entry.get("metadata") or {}),
            )
            for entry in normalized_entries
        ],
    )


def _coerce_int(value: Any) -> Optional[int]:
    if value is None:
        return None
    try:
        if isinstance(value, str) and value.strip() == "":
            return None
        return int(float(value))
    except (ValueError, TypeError):
        return None


def _extract_device_columns(details: Dict[str, Any]) -> Dict[str, Any]:
    summary = details.get("summary") or {}
    payload: Dict[str, Any] = {}

    for field, default in _DEVICE_JSON_LIST_FIELDS.items():
        payload[field] = _serialize_device_json(details.get(field), default)
    for field, default in _DEVICE_JSON_OBJECT_FIELDS.items():
        if field == "cpu":
            payload[field] = _serialize_device_json(summary.get("cpu") or details.get("cpu"), default)
        else:
            payload[field] = _serialize_device_json(details.get(field), default)

    payload["device_type"] = _clean_device_str(summary.get("device_type") or summary.get("type"))
    payload["domain"] = _clean_device_str(summary.get("domain"))
    payload["external_ip"] = _clean_device_str(summary.get("external_ip") or summary.get("public_ip"))
    payload["internal_ip"] = _clean_device_str(summary.get("internal_ip") or summary.get("private_ip"))
    payload["last_reboot"] = _clean_device_str(summary.get("last_reboot") or summary.get("last_boot"))
    payload["last_seen"] = _coerce_int(summary.get("last_seen"))
    cpu_percent_value = summary.get("cpu_percent")
    memory_percent_value = summary.get("memory_percent")
    try:
        payload["cpu_percent"] = None if cpu_percent_value in (None, "") else float(cpu_percent_value)
    except (ValueError, TypeError):
        payload["cpu_percent"] = None
    try:
        payload["memory_percent"] = None if memory_percent_value in (None, "") else float(memory_percent_value)
    except (ValueError, TypeError):
        payload["memory_percent"] = None
    payload["last_user"] = _clean_device_str(
        summary.get("last_user") or summary.get("last_user_name") or summary.get("username")
    )
    payload["operating_system"] = _clean_device_str(
        summary.get("operating_system") or summary.get("agent_operating_system") or summary.get("os")
    )
    uptime_value = summary.get("uptime_sec") or summary.get("uptime_seconds") or summary.get("uptime")
    payload["uptime"] = _coerce_int(uptime_value)
    payload["agent_id"] = _clean_device_str(summary.get("agent_id"))
    payload["connection_type"] = _clean_device_str(summary.get("connection_type") or summary.get("remote_type"))
    payload["connection_endpoint"] = _clean_device_str(
        summary.get("connection_endpoint")
        or summary.get("connection_address")
        or summary.get("address")
        or summary.get("external_ip")
        or summary.get("internal_ip")
    )
    return payload


def _device_upsert(
    cur: sqlite3.Cursor,
    hostname: str,
    description: Optional[str],
    merged_details: Dict[str, Any],
    created_at: Optional[int],
    *,
    agent_hash: Optional[str] = None,
    agent_role_health: Optional[Any] = None,
    guid: Optional[str] = None,
) -> None:
    if not hostname:
        return
    column_values = _extract_device_columns(merged_details or {})

    normalized_description = description if description is not None else ""
    try:
        normalized_description = str(normalized_description)
    except Exception:
        normalized_description = ""

    normalized_hash = _clean_device_str(agent_hash) or None
    normalized_role_health = serialize_agent_role_health(agent_role_health) if agent_role_health is not None else None
    normalized_guid = _clean_device_str(guid) or None
    if normalized_guid:
        try:
            normalized_guid = normalize_guid(normalized_guid)
        except Exception:
            pass

    created_ts = _coerce_int(created_at)
    if not created_ts:
        created_ts = int(time.time())

    sql = f"""
        INSERT INTO {DEVICE_TABLE}(
            hostname,
            description,
            created_at,
            agent_hash,
            agent_role_health,
            guid,
            memory,
            network,
            software,
            services,
            storage,
            cpu,
            sessions,
            processes,
            device_type,
            domain,
            external_ip,
            internal_ip,
            last_reboot,
            last_seen,
            cpu_percent,
            memory_percent,
            last_user,
            operating_system,
            uptime,
            agent_id,
            connection_type,
            connection_endpoint
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(hostname) DO UPDATE SET
            description=excluded.description,
            created_at=COALESCE({DEVICE_TABLE}.created_at, excluded.created_at),
            agent_hash=COALESCE(NULLIF(excluded.agent_hash, ''), {DEVICE_TABLE}.agent_hash),
            agent_role_health=COALESCE(NULLIF(excluded.agent_role_health, ''), {DEVICE_TABLE}.agent_role_health),
            guid=COALESCE(NULLIF(excluded.guid, ''), {DEVICE_TABLE}.guid),
            memory=excluded.memory,
            network=excluded.network,
            software=excluded.software,
            services=excluded.services,
            storage=excluded.storage,
            cpu=excluded.cpu,
            sessions=excluded.sessions,
            processes=excluded.processes,
            device_type=COALESCE(NULLIF(excluded.device_type, ''), {DEVICE_TABLE}.device_type),
            domain=COALESCE(NULLIF(excluded.domain, ''), {DEVICE_TABLE}.domain),
            external_ip=COALESCE(NULLIF(excluded.external_ip, ''), {DEVICE_TABLE}.external_ip),
            internal_ip=COALESCE(NULLIF(excluded.internal_ip, ''), {DEVICE_TABLE}.internal_ip),
            last_reboot=COALESCE(NULLIF(excluded.last_reboot, ''), {DEVICE_TABLE}.last_reboot),
            last_seen=COALESCE(NULLIF(excluded.last_seen, 0), {DEVICE_TABLE}.last_seen),
            cpu_percent=COALESCE(excluded.cpu_percent, {DEVICE_TABLE}.cpu_percent),
            memory_percent=COALESCE(excluded.memory_percent, {DEVICE_TABLE}.memory_percent),
            last_user=COALESCE(NULLIF(excluded.last_user, ''), {DEVICE_TABLE}.last_user),
            operating_system=COALESCE(NULLIF(excluded.operating_system, ''), {DEVICE_TABLE}.operating_system),
            uptime=COALESCE(NULLIF(excluded.uptime, 0), {DEVICE_TABLE}.uptime),
            agent_id=COALESCE(NULLIF(excluded.agent_id, ''), {DEVICE_TABLE}.agent_id),
            connection_type=COALESCE(NULLIF(excluded.connection_type, ''), {DEVICE_TABLE}.connection_type),
            connection_endpoint=COALESCE(NULLIF(excluded.connection_endpoint, ''), {DEVICE_TABLE}.connection_endpoint)
    """

    params: List[Any] = [
        hostname,
        normalized_description,
        created_ts,
        normalized_hash,
        normalized_role_health,
        normalized_guid,
        column_values.get("memory"),
        column_values.get("network"),
        column_values.get("software"),
        column_values.get("services"),
        column_values.get("storage"),
        column_values.get("cpu"),
        column_values.get("sessions"),
        column_values.get("processes"),
        column_values.get("device_type"),
        column_values.get("domain"),
        column_values.get("external_ip"),
        column_values.get("internal_ip"),
        column_values.get("last_reboot"),
        column_values.get("last_seen"),
        column_values.get("cpu_percent"),
        column_values.get("memory_percent"),
        column_values.get("last_user"),
        column_values.get("operating_system"),
        column_values.get("uptime"),
        column_values.get("agent_id"),
        column_values.get("connection_type"),
        column_values.get("connection_endpoint"),
    ]
    cur.execute(sql, params)


class DeviceManagementService:
    """Encapsulates database access for device-focused API routes."""

    _DEVICE_COLUMNS: Tuple[str, ...] = (
        "guid",
        "hostname",
        "description",
        "created_at",
        "last_enrollment_at",
        "agent_hash",
        "agent_role_health",
        "memory",
        "network",
        "software",
        "services",
        "storage",
        "cpu",
        "sessions",
        "processes",
        "device_type",
        "domain",
        "external_ip",
        "internal_ip",
        "last_reboot",
        "last_seen",
        "cpu_percent",
        "memory_percent",
        "last_user",
        "operating_system",
        "uptime",
        "agent_id",
        "connection_type",
        "connection_endpoint",
    )

    def __init__(self, app, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger or logging.getLogger(__name__)
        self.repo_cache = adapters.github_integration
        self._repo_hash_cache: Dict[str, Any] = {"sha": "", "expires_at": 0}
        self.site_access = UserSiteAccessManager(self.db_conn_factory, logger=self.logger)

    def _emit_device_services_changed(self, hostname: str, *, change: str) -> None:
        socketio = getattr(self.adapters.context, "socketio", None)
        normalized_hostname = _clean_device_str(hostname) or ""
        normalized_change = _clean_device_str(change) or ""
        if socketio is None or not normalized_hostname or not normalized_change:
            return
        try:
            socketio.emit(
                "device_services_changed",
                {
                    "hostname": normalized_hostname,
                    "change": normalized_change,
                },
            )
        except Exception:
            self.logger.debug("Failed to emit device_services_changed for hostname=%s", normalized_hostname, exc_info=True)

    def _target_repo_config(self) -> Tuple[str, str]:
        repo_name = (os.environ.get("BOREALIS_UPDATE_REPO") or "bunny-lab-io/Borealis").strip()
        branch_name = (os.environ.get("BOREALIS_UPDATE_BRANCH") or "main").strip()
        return repo_name or "bunny-lab-io/Borealis", branch_name or "main"

    def _current_target_repo_hash(self) -> str:
        now_ts = int(time.time())
        cached_sha = (_clean_device_str(self._repo_hash_cache.get("sha")) or "").lower()
        try:
            expires_at = int(self._repo_hash_cache.get("expires_at") or 0)
        except Exception:
            expires_at = 0
        if expires_at > now_ts:
            return cached_sha
        repo_name, branch_name = self._target_repo_config()
        sha = ""
        try:
            payload, status = self.repo_cache.current_repo_hash(repo_name, branch_name)
            if status == 200 and isinstance(payload, dict):
                sha = (_clean_device_str(payload.get("sha")) or "").lower()
        except Exception:
            sha = ""
        self._repo_hash_cache = {"sha": sha, "expires_at": now_ts + 60}
        return sha

    def _compute_agent_version_status(self, installed_build_id: Any, target_build_id: Optional[str] = None) -> str:
        installed = (_clean_device_str(installed_build_id) or "").lower()
        target = (target_build_id or "").strip().lower()
        if installed and target and installed == target:
            return "Up-to-Date"
        return "Needs Updated"

    def _attach_agent_version_status(
        self,
        payload: Dict[str, Any],
        *,
        target_build_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        if not isinstance(payload, dict):
            return payload
        if target_build_id is None:
            target_build_id = self._current_target_repo_hash()
        summary = payload.get("summary") if isinstance(payload.get("summary"), dict) else {}
        installed_build_id = (
            _clean_device_str(payload.get("agent_build_id"))
            or _clean_device_str(payload.get("agent_hash"))
            or _clean_device_str(summary.get("agent_build_id"))
            or _clean_device_str(summary.get("agent_hash"))
        )
        status = self._compute_agent_version_status(installed_build_id, target_build_id)
        payload["agent_version_status"] = status
        if installed_build_id:
            payload["agent_build_id"] = installed_build_id
        if isinstance(summary, dict):
            summary["agent_version_status"] = status
            if installed_build_id and not summary.get("agent_build_id"):
                summary["agent_build_id"] = installed_build_id
        details = payload.get("details")
        if isinstance(details, dict):
            detail_summary = details.setdefault("summary", {})
            if isinstance(detail_summary, dict):
                detail_summary["agent_version_status"] = status
                if installed_build_id and not detail_summary.get("agent_build_id"):
                    detail_summary["agent_build_id"] = installed_build_id
        return payload

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, str]]:
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
            data = self._token_serializer().loads(
                token,
                max_age=int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30)),
            )
            username = data.get("u")
            role = data.get("r") or "User"
            if username:
                return {"username": username, "role": role}
        except (BadSignature, SignatureExpired, Exception):
            return None
        return None


    def _require_login(self) -> Optional[Tuple[Dict[str, Any], int]]:
        if not self._current_user():
            return {"error": "unauthorized"}, 401
        return None

    def _require_device_or_login(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if user:
            return None

        manager = getattr(self.adapters, "device_auth_manager", None)
        if manager is None:
            return {"error": "unauthorized"}, 401

        try:
            ctx = manager.authenticate()
            g.device_auth = ctx
            return None
        except DeviceAuthError as exc:
            payload: Dict[str, Any] = {"error": exc.message}
            retry_after = getattr(exc, "retry_after", None)
            if retry_after:
                payload["retry_after"] = retry_after
            return payload, getattr(exc, "status_code", 401) or 401
        except Exception:
            self.service_log("server", "/api/repo/current_hash auth failure", level="ERROR")
            return {"error": "unauthorized"}, 401

    def _require_admin(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if (user.get("role") or "").lower() != "admin":
            return {"error": "forbidden"}, 403
        return None

    def _build_device_payload(
        self,
        row: Tuple[Any, ...],
        site_row: Tuple[Optional[int], Optional[str], Optional[str]],
    ) -> Dict[str, Any]:
        mapping = dict(zip(self._DEVICE_COLUMNS, row))
        created_at = mapping.get("created_at") or 0
        last_enrollment_at = mapping.get("last_enrollment_at") or created_at or 0
        last_seen = mapping.get("last_seen") or 0
        role_health = normalize_agent_role_health(mapping.get("agent_role_health"))
        services_payload = normalize_device_services(mapping.get("services"))
        sessions_payload = normalize_device_sessions(mapping.get("sessions"))
        processes_payload = normalize_device_processes(mapping.get("processes"))
        summary = {
            "hostname": mapping.get("hostname") or "",
            "description": mapping.get("description") or "",
            "agent_hash": (mapping.get("agent_hash") or "").strip(),
            "agent_build_id": (mapping.get("agent_hash") or "").strip(),
            "agent_role_health": role_health,
            "agent_guid": normalize_guid(mapping.get("guid")) or "",
            "agent_id": (mapping.get("agent_id") or "").strip(),
            "device_type": mapping.get("device_type") or "",
            "domain": mapping.get("domain") or "",
            "external_ip": mapping.get("external_ip") or "",
            "internal_ip": mapping.get("internal_ip") or "",
            "last_reboot": mapping.get("last_reboot") or "",
            "last_seen": last_seen or 0,
            "cpu_percent": mapping.get("cpu_percent"),
            "memory_percent": mapping.get("memory_percent"),
            "last_user": mapping.get("last_user") or "",
            "operating_system": mapping.get("operating_system") or "",
            "uptime": mapping.get("uptime") or 0,
            "created_at": created_at or 0,
            "last_enrollment_at": last_enrollment_at or 0,
            "connection_type": mapping.get("connection_type") or "",
            "connection_endpoint": mapping.get("connection_endpoint") or "",
        }
        details = {
            "summary": summary,
            "memory": _safe_json(mapping.get("memory"), []),
            "network": _safe_json(mapping.get("network"), []),
            "software": _normalize_software_inventory(_safe_json(mapping.get("software"), [])),
            "services": services_payload.get("services") or [],
            "storage": _safe_json(mapping.get("storage"), []),
            "cpu": _safe_json(mapping.get("cpu"), {}),
            "sessions": sessions_payload.get("sessions") or [],
            "processes": processes_payload.get("processes") or [],
        }
        site_id, site_name, site_description = site_row
        payload = {
            "hostname": summary["hostname"],
            "description": summary["description"],
            "details": details,
            "summary": summary,
            "created_at": created_at or 0,
            "created_at_iso": _ts_to_iso(created_at),
            "last_enrollment_at": last_enrollment_at or 0,
            "last_enrollment_at_iso": _ts_to_iso(last_enrollment_at),
            "agent_hash": summary["agent_hash"],
            "agent_build_id": summary["agent_build_id"],
            "agent_role_health": role_health,
            "agent_guid": summary["agent_guid"],
            "guid": summary["agent_guid"],
            "memory": details["memory"],
            "network": details["network"],
            "software": details["software"],
            "services": details["services"],
            "services_reported_at": services_payload.get("reported_at") or 0,
            "storage": details["storage"],
            "cpu": details["cpu"],
            "sessions": details["sessions"],
            "sessions_reported_at": sessions_payload.get("reported_at") or 0,
            "processes": details["processes"],
            "processes_reported_at": processes_payload.get("reported_at") or 0,
            "device_type": summary["device_type"],
            "domain": summary["domain"],
            "external_ip": summary["external_ip"],
            "internal_ip": summary["internal_ip"],
            "last_reboot": summary["last_reboot"],
            "last_seen": last_seen or 0,
            "last_seen_iso": _ts_to_iso(last_seen),
            "cpu_percent": summary["cpu_percent"],
            "memory_percent": summary["memory_percent"],
            "last_user": summary["last_user"],
            "operating_system": summary["operating_system"],
            "uptime": summary["uptime"],
            "agent_id": summary["agent_id"],
            "connection_type": summary["connection_type"],
            "connection_endpoint": summary["connection_endpoint"],
            "site_id": site_id,
            "site_name": site_name or "",
            "site_description": site_description or "",
            "status": _status_from_last_seen(last_seen or 0),
        }
        return payload

    def _fetch_devices(
        self,
        *,
        connection_type: Optional[str] = None,
        hostname: Optional[str] = None,
        only_agents: bool = False,
        allowed_site_ids: Optional[set[int]] = None,
    ) -> List[Dict[str, Any]]:
        if allowed_site_ids is not None and not allowed_site_ids:
            return []
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
            sql = f"""
                SELECT {columns_sql}, s.id, s.name, s.description
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
            """
            clauses: List[str] = []
            params: List[Any] = []
            if connection_type:
                clauses.append("LOWER(d.connection_type) = LOWER(?)")
                params.append(connection_type)
            if hostname:
                clauses.append("LOWER(d.hostname) = LOWER(?)")
                params.append(hostname.lower())
            if only_agents:
                clauses.append("(d.connection_type IS NULL OR TRIM(d.connection_type) = '')")
            if allowed_site_ids is not None:
                placeholders = ",".join("?" for _ in sorted(allowed_site_ids))
                clauses.append(f"ds.site_id IN ({placeholders})")
                params.extend(sorted(allowed_site_ids))
            if clauses:
                sql += " WHERE " + " AND ".join(clauses)
            cur.execute(sql, params)
            rows = cur.fetchall()
        finally:
            conn.close()
        devices: List[Dict[str, Any]] = []
        for row in rows:
            device_tuple = row[: len(self._DEVICE_COLUMNS)]
            site_tuple = row[len(self._DEVICE_COLUMNS):]
            devices.append(self._build_device_payload(device_tuple, site_tuple))
        return devices
    def list_devices(self) -> Tuple[Dict[str, Any], int]:
        try:
            only_agents = request.args.get("only_agents") in {"1", "true", "yes"}
            allowed_site_ids = self.site_access.site_ids_for_user(self._current_user())
            devices = self._fetch_devices(
                connection_type=request.args.get("connection_type"),
                hostname=request.args.get("hostname"),
                only_agents=only_agents,
                allowed_site_ids=allowed_site_ids,
            )
            return {"devices": devices}, 200
        except Exception as exc:
            self.logger.debug("Failed to list devices", exc_info=True)
            return {"error": str(exc)}, 500

    def search_devices_by_hostname(self, query: str) -> Tuple[Dict[str, Any], int]:
        normalized_query = _clean_device_str(query) or ""
        if len(normalized_query) < 3:
            return {"devices": [], "query": normalized_query, "count": 0}, 200

        allowed_site_ids = self.site_access.site_ids_for_user(self._current_user())
        if allowed_site_ids is not None and not allowed_site_ids:
            return {"devices": [], "query": normalized_query, "count": 0}, 200

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            sql = """
                SELECT d.guid, d.hostname, d.agent_id, d.connection_type, s.id, s.name, s.description
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
                 WHERE LOWER(d.hostname) LIKE ? ESCAPE '\\'
            """
            params: List[Any] = [f"%{_escape_like(normalized_query.lower())}%"]
            if allowed_site_ids is not None:
                placeholders = ",".join("?" for _ in sorted(allowed_site_ids))
                sql += f" AND ds.site_id IN ({placeholders})"
                params.extend(sorted(allowed_site_ids))
            cur.execute(sql, params)
            rows = cur.fetchall()
        except Exception as exc:
            self.logger.debug("Failed to search devices by hostname", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()
        seen: set[tuple[str, str, Any, str]] = set()
        matches: List[Dict[str, Any]] = []
        query_lc = normalized_query.lower()
        for row in rows:
            raw_guid = _clean_device_str(row[0]) or ""
            try:
                agent_guid = normalize_guid(raw_guid) if raw_guid else ""
            except Exception:
                agent_guid = raw_guid
            hostname = _clean_device_str(row[1]) or ""
            if not hostname:
                continue
            agent_id = _clean_device_str(row[2]) or ""
            site_id = row[4]
            dedupe_key = (
                agent_guid.lower(),
                hostname.lower(),
                site_id,
                agent_id.lower(),
            )
            if dedupe_key in seen:
                continue
            seen.add(dedupe_key)
            matches.append(
                {
                    "agent_guid": agent_guid,
                    "agent_id": agent_id,
                    "hostname": hostname,
                    "connection_type": _clean_device_str(row[3]) or "",
                    "site_id": site_id,
                    "site_name": _clean_device_str(row[5]) or "",
                    "site_description": _clean_device_str(row[6]) or "",
                }
            )

        matches.sort(
            key=lambda item: (
                0 if item["hostname"].lower() == query_lc else 1,
                0 if item["hostname"].lower().startswith(query_lc) else 1,
                item["hostname"].lower(),
                item["site_name"].lower(),
            )
        )
        return {"devices": matches, "query": normalized_query, "count": len(matches)}, 200

    def list_agents(self) -> Tuple[Dict[str, Any], int]:
        try:
            devices = self._fetch_devices(
                only_agents=True,
                allowed_site_ids=self.site_access.site_ids_for_user(self._current_user()),
            )
            grouped: Dict[str, Dict[str, Dict[str, Any]]] = {}
            now = time.time()
            for record in devices:
                hostname = (record.get("hostname") or "").strip() or "unknown"
                agent_id = (record.get("agent_id") or "").strip()
                mode = _normalize_service_mode(record.get("service_mode"), agent_id)
                if mode != "currentuser":
                    lowered = agent_id.lower()
                    if lowered.endswith("-script"):
                        continue
                last_seen_raw = record.get("last_seen") or 0
                try:
                    last_seen = int(last_seen_raw)
                except Exception:
                    last_seen = 0
                collector_active = bool(last_seen and (now - float(last_seen)) < 130)
                agent_guid = normalize_guid(record.get("agent_guid")) if record.get("agent_guid") else ""
                status_value = record.get("status")
                if status_value in (None, ""):
                    status = "Online" if collector_active else "Offline"
                else:
                    status = str(status_value)
                payload = {
                    "hostname": hostname,
                    "agent_hostname": hostname,
                    "service_mode": mode,
                    "collector_active": collector_active,
                    "collector_active_ts": last_seen,
                    "last_seen": last_seen,
                    "status": status,
                    "agent_id": agent_id,
                    "agent_guid": agent_guid or "",
                    "agent_hash": record.get("agent_hash") or "",
                    "connection_type": record.get("connection_type") or "",
                    "connection_endpoint": record.get("connection_endpoint") or "",
                    "device_type": record.get("device_type") or "",
                    "domain": record.get("domain") or "",
                    "external_ip": record.get("external_ip") or "",
                    "internal_ip": record.get("internal_ip") or "",
                    "last_reboot": record.get("last_reboot") or "",
                    "last_user": record.get("last_user") or "",
                    "operating_system": record.get("operating_system") or "",
                    "uptime": record.get("uptime") or 0,
                    "site_id": record.get("site_id"),
                    "site_name": record.get("site_name") or "",
                    "site_description": record.get("site_description") or "",
                }
                bucket = grouped.setdefault(hostname, {})
                existing = bucket.get(mode)
                if not existing or last_seen >= existing.get("last_seen", 0):
                    bucket[mode] = payload

            agents: Dict[str, Dict[str, Any]] = {}
            for bucket in grouped.values():
                for payload in bucket.values():
                    agent_key = payload.get("agent_id") or payload.get("agent_guid")
                    if not agent_key:
                        agent_key = f"{payload['hostname']}|{payload['service_mode']}"
                    if not payload.get("agent_id"):
                        payload["agent_id"] = agent_key
                    agents[agent_key] = payload

            # The legacy server exposed /api/agents as a mapping keyed by
            # agent identifier. The Engine WebUI expects the same structure,
            # so we return the flattened dictionary directly instead of
            # wrapping it in another object.
            return agents, 200
        except Exception as exc:
            self.logger.debug("Failed to list agents", exc_info=True)
            return {"error": str(exc)}, 500

    def get_device_by_guid(self, guid: str) -> Tuple[Dict[str, Any], int]:
        normalized_guid = normalize_guid(guid)
        if not normalized_guid:
            return {"error": "invalid guid"}, 400
        current_user = self._current_user()
        try:
            conn = self._db_conn()
            try:
                cur = conn.cursor()
                columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
                cur.execute(
                    f"""
                    SELECT {columns_sql}, s.id, s.name, s.description
                      FROM devices AS d
                 LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 LEFT JOIN sites AS s ON s.id = ds.site_id
                     WHERE LOWER(d.guid) = ?
                    """,
                    (normalized_guid.lower(),),
                )
                row = cur.fetchone()
            finally:
                conn.close()
            if not row:
                return {"error": "not found"}, 404
            device_tuple = row[: len(self._DEVICE_COLUMNS)]
            site_tuple = row[len(self._DEVICE_COLUMNS):]
            if not self.site_access.user_can_access_site(current_user, site_tuple[0]):
                return {"error": "not found"}, 404
            payload = self._build_device_payload(device_tuple, site_tuple)
            payload = self._attach_agent_version_status(payload)
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device by guid", exc_info=True)
            return {"error": str(exc)}, 500

    def save_agent_details(self) -> Tuple[Dict[str, Any], int]:
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            self.service_log("server", "/api/agent/details missing device auth context", level="ERROR")
            return {"error": "auth_context_missing"}, 500

        payload = request.get_json(silent=True) or {}
        details = payload.get("details")
        if not isinstance(details, dict):
            return {"error": "invalid payload"}, 400
        if "software" in details:
            details["software"] = _normalize_software_inventory(details.get("software"))
        incoming_services_raw = None
        if "services" in details:
            incoming_services_raw = normalize_device_services(details.get("services"))
            details["services"] = incoming_services_raw.get("services") or []
        if "sessions" in details:
            details["sessions"] = normalize_device_sessions(details.get("sessions"))
        if "processes" in details:
            details["processes"] = normalize_device_processes(details.get("processes"))

        hostname = _clean_device_str(payload.get("hostname"))
        if not hostname:
            summary_host = (details.get("summary") or {}).get("hostname")
            hostname = _clean_device_str(summary_host)
        if not hostname:
            return {"error": "invalid payload"}, 400
        incoming_hostname = hostname

        agent_id = _clean_device_str(payload.get("agent_id"))
        agent_hash = (
            _clean_device_str(payload.get("agent_build_id"))
            or _clean_device_str(payload.get("installed_build_id"))
            or _clean_device_str(payload.get("agent_hash"))
            or _clean_device_str((details.get("summary") or {}).get("agent_build_id"))
            or _clean_device_str((details.get("summary") or {}).get("installed_build_id"))
            or _clean_device_str((details.get("summary") or {}).get("agent_hash"))
        )
        incoming_role_health = (
            payload.get("agent_role_health")
            if payload.get("agent_role_health") is not None
            else payload.get("role_health")
        )
        if incoming_role_health is None:
            incoming_role_health = details.get("agent_role_health")
        if incoming_role_health is None:
            incoming_role_health = (details.get("summary") or {}).get("agent_role_health")
        incoming_service_mode = (
            _clean_device_str(payload.get("service_mode"))
            or _clean_device_str((details.get("summary") or {}).get("service_mode"))
            or _clean_device_str(getattr(ctx, "service_mode", None))
        )

        raw_guid = getattr(ctx, "guid", None)
        try:
            auth_guid = normalize_guid(raw_guid) if raw_guid else None
        except Exception:
            auth_guid = None

        fingerprint = _clean_device_str(getattr(ctx, "ssl_key_fingerprint", None))
        fingerprint_lower = fingerprint.lower() if fingerprint else ""
        scope_hint = getattr(ctx, "service_mode", None)

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
            cur.execute(
                f"SELECT {columns_sql}, d.ssl_key_fingerprint FROM {DEVICE_TABLE} AS d WHERE d.hostname = ?",
                (hostname,),
            )
            row = cur.fetchone()
            if not row and auth_guid:
                try:
                    cur.execute(
                        f"SELECT {columns_sql}, d.ssl_key_fingerprint FROM {DEVICE_TABLE} AS d WHERE LOWER(d.guid) = LOWER(?)",
                        (auth_guid,),
                    )
                    row = cur.fetchone()
                    if row:
                        existing_host = _clean_device_str(row[1])
                        if existing_host:
                            hostname = existing_host
                            if incoming_hostname and incoming_hostname != existing_host:
                                self.service_log(
                                    "server",
                                    f"/api/agent/details hostname reconciled guid={auth_guid} incoming={incoming_hostname} stored={existing_host}",
                                    scope_hint,
                                    level="INFO",
                                )
                except Exception:
                    row = None

            prev_details: Dict[str, Any] = {}
            description = ""
            created_at = 0
            existing_guid = None
            existing_agent_hash = None
            existing_role_health = None
            existing_services_raw = None
            db_fp = ""

            if row:
                device_tuple = row[: len(self._DEVICE_COLUMNS)]
                previous = self._build_device_payload(device_tuple, (None, None, None))
                try:
                    prev_details = json.loads(json.dumps(previous.get("details", {})))
                except Exception:
                    prev_details = previous.get("details", {}) or {}
                description = previous.get("description") or ""
                created_at = _coerce_int(previous.get("created_at")) or 0
                existing_guid_raw = previous.get("agent_guid") or ""
                try:
                    existing_guid = normalize_guid(existing_guid_raw) if existing_guid_raw else None
                except Exception:
                    existing_guid = None
                existing_agent_hash = _clean_device_str(previous.get("agent_hash")) or None
                existing_role_health = previous.get("agent_role_health")
                existing_services_raw = previous.get("services")
                db_fp = (row[-1] or "").strip().lower() if row[-1] else ""
            if db_fp and fingerprint_lower and db_fp != fingerprint_lower:
                self.service_log(
                    "server",
                    f"/api/agent/details fingerprint mismatch host={hostname} guid={auth_guid or existing_guid or ''}",
                    scope_hint,
                    level="WARN",
                )
                return {"error": "fingerprint_mismatch"}, 403

            if existing_guid and auth_guid and existing_guid != auth_guid:
                self.service_log(
                    "server",
                    f"/api/agent/details guid mismatch host={hostname} expected={existing_guid} provided={auth_guid}",
                    scope_hint,
                    level="WARN",
                )
                return {"error": "guid_mismatch"}, 403

            incoming_summary = details.setdefault("summary", {})
            if agent_id and not incoming_summary.get("agent_id"):
                incoming_summary["agent_id"] = agent_id
            if hostname and not incoming_summary.get("hostname"):
                incoming_summary["hostname"] = hostname
            if agent_hash:
                incoming_summary["agent_hash"] = agent_hash
                incoming_summary["agent_build_id"] = agent_hash

            effective_guid = auth_guid or existing_guid
            if effective_guid:
                incoming_summary["agent_guid"] = effective_guid
            if fingerprint:
                incoming_summary.setdefault("ssl_key_fingerprint", fingerprint)
            merged_role_health = (
                merge_agent_role_health(
                    existing_role_health,
                    incoming_role_health,
                    incoming_context=incoming_service_mode,
                )
                if incoming_role_health is not None
                else normalize_agent_role_health(existing_role_health)
            )

            prev_summary = prev_details.get("summary") if isinstance(prev_details, dict) else {}
            if isinstance(prev_summary, dict):
                if _is_empty(incoming_summary.get("last_seen")) and not _is_empty(prev_summary.get("last_seen")):
                    try:
                        incoming_summary["last_seen"] = int(prev_summary.get("last_seen"))
                    except Exception:
                        pass
                if _is_empty(incoming_summary.get("last_user")) and not _is_empty(prev_summary.get("last_user")):
                    incoming_summary["last_user"] = prev_summary.get("last_user")

            merged = _deep_merge_preserve(prev_details, details)
            merged_services_payload = (
                merge_device_services(existing_services_raw, incoming_services_raw)
                if incoming_services_raw is not None
                else normalize_device_services(existing_services_raw)
            )
            merged["services"] = merged_services_payload.get("services") or []
            merged_summary = merged.setdefault("summary", {})
            if hostname:
                merged_summary.setdefault("hostname", hostname)
            if agent_id:
                merged_summary.setdefault("agent_id", agent_id)
            if agent_hash and _is_empty(merged_summary.get("agent_hash")):
                merged_summary["agent_hash"] = agent_hash
            if agent_hash and _is_empty(merged_summary.get("agent_build_id")):
                merged_summary["agent_build_id"] = agent_hash
            if effective_guid:
                merged_summary["agent_guid"] = effective_guid
            if fingerprint:
                merged_summary.setdefault("ssl_key_fingerprint", fingerprint)
            if description and _is_empty(merged_summary.get("description")):
                merged_summary["description"] = description
            if existing_agent_hash and _is_empty(merged_summary.get("agent_hash")):
                merged_summary["agent_hash"] = existing_agent_hash
            if existing_agent_hash and _is_empty(merged_summary.get("agent_build_id")):
                merged_summary["agent_build_id"] = existing_agent_hash

            if created_at <= 0:
                created_at = int(time.time())
            try:
                merged_summary.setdefault(
                    "created",
                    datetime.fromtimestamp(created_at, timezone.utc).strftime("%Y-%m-%d %H:%M:%S"),
                )
            except Exception:
                pass
            merged_summary.setdefault("created_at", created_at)
            services_changed = serialize_device_services(existing_services_raw) != serialize_device_services(
                merged_services_payload
            )

            _device_upsert(
                cur,
                hostname,
                description,
                merged,
                created_at,
                agent_hash=agent_hash or existing_agent_hash,
                agent_role_health=merged_role_health,
                guid=effective_guid,
            )
            _sync_device_software_inventory(cur, effective_guid, merged.get("software"))

            if effective_guid and fingerprint:
                now_iso = datetime.now(timezone.utc).isoformat()
                cur.execute(
                    """
                    UPDATE devices
                       SET ssl_key_fingerprint = ?,
                           key_added_at = COALESCE(key_added_at, ?)
                     WHERE guid = ?
                    """,
                    (fingerprint, now_iso, effective_guid),
                )
                cur.execute(
                    """
                    INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
                    VALUES (?, ?, ?, ?)
                    """,
                    (str(uuid.uuid4()), effective_guid, fingerprint, now_iso),
                )

            conn.commit()
            if services_changed:
                self._emit_device_services_changed(hostname, change="updated")
            return {"status": "ok"}, 200
        except Exception as exc:
            try:
                conn.rollback()
            except Exception:
                pass
            self.logger.debug("Failed to save agent details", exc_info=True)
            self.service_log("server", f"/api/agent/details error: {exc}", scope_hint, level="ERROR")
            return {"error": "internal error"}, 500
        finally:
            conn.close()

    def get_device_details(self, hostname: str) -> Tuple[Dict[str, Any], int]:
        current_user = self._current_user()
        try:
            conn = self._db_conn()
            try:
                cur = conn.cursor()
                columns_sql = ", ".join(f"d.{col}" for col in self._DEVICE_COLUMNS)
                cur.execute(
                    f"""
                    SELECT {columns_sql}, s.id, s.name, s.description
                      FROM devices AS d
                 LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 LEFT JOIN sites AS s ON s.id = ds.site_id
                     WHERE d.hostname = ?
                    """,
                    (hostname,),
                )
                row = cur.fetchone()
            finally:
                conn.close()
            if not row:
                return {}, 200
            device_tuple = row[: len(self._DEVICE_COLUMNS)]
            site_tuple = row[len(self._DEVICE_COLUMNS):]
            if not self.site_access.user_can_access_site(current_user, site_tuple[0]):
                return {}, 200
            payload = self._build_device_payload(device_tuple, site_tuple)
            payload = self._attach_agent_version_status(payload)
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device details", exc_info=True)
            return {"error": str(exc)}, 500

    def set_device_description(self, hostname: str, description: str) -> Tuple[Dict[str, Any], int]:
        current_user = self._current_user()
        if not self.site_access.user_can_access_hostname(current_user, hostname):
            return {"error": "not found"}, 404
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET description = ? WHERE hostname = ?",
                (description, hostname),
            )
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to update device description", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def _delete_where_any(
        self,
        cur: sqlite3.Cursor,
        table: str,
        predicates: List[Tuple[str, Tuple[Any, ...]]],
    ) -> int:
        active_predicates = [
            (clause, tuple(params or ()))
            for clause, params in (predicates or [])
            if clause and tuple(params or ())
        ]
        if not active_predicates:
            return 0
        sql = " OR ".join(f"({clause})" for clause, _ in active_predicates)
        params: List[Any] = []
        for _clause, clause_params in active_predicates:
            params.extend(clause_params)
        cur.execute(f"DELETE FROM {table} WHERE {sql}", tuple(params))
        return int(cur.rowcount or 0)

    def _delete_by_id_list(
        self,
        cur: sqlite3.Cursor,
        table: str,
        column: str,
        values: List[Any],
    ) -> int:
        items = [value for value in (values or []) if value not in (None, "")]
        if not items:
            return 0
        placeholders = ",".join("?" for _ in items)
        cur.execute(
            f"DELETE FROM {table} WHERE {column} IN ({placeholders})",
            tuple(items),
        )
        return int(cur.rowcount or 0)

    def _load_device_purge_record(
        self,
        cur: sqlite3.Cursor,
        guid: str,
    ) -> Optional[Dict[str, Any]]:
        normalized_guid = normalize_guid(guid)
        if not normalized_guid:
            return None
        cur.execute(
            """
            SELECT d.guid,
                   d.hostname,
                   d.agent_id,
                   d.ssl_key_fingerprint,
                   d.token_version,
                   ds.site_id
              FROM devices AS d
         LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             WHERE UPPER(d.guid) = ?
            """,
            (normalized_guid,),
        )
        row = cur.fetchone()
        if not row:
            return None
        try:
            site_id = int(row[5]) if row[5] is not None else None
        except Exception:
            site_id = None
        try:
            token_version = max(1, int(row[4] or 1))
        except Exception:
            token_version = 1
        return {
            "guid": normalize_guid(row[0]) or normalized_guid,
            "hostname": (row[1] or "").strip(),
            "agent_id": (row[2] or "").strip(),
            "ssl_key_fingerprint": (row[3] or "").strip().lower(),
            "token_version": token_version,
            "site_id": site_id,
        }

    def _rewrite_scheduled_jobs_for_purge(
        self,
        cur: sqlite3.Cursor,
        *,
        device_guid: str,
        hostname: str,
        site_id: Optional[int],
    ) -> Dict[str, int]:
        cur.execute("SELECT id, targets_json FROM scheduled_jobs")
        rows = cur.fetchall()
        now_ts = int(time.time())
        summary = {
            "updated": 0,
            "deleted": 0,
            "targets_removed": 0,
        }
        for job_id, targets_json in rows or []:
            try:
                raw_targets = json.loads(targets_json or "[]")
            except Exception:
                raw_targets = []
            updated_targets, removed_count = prune_device_targets(
                raw_targets,
                device_guid=device_guid,
                hostname=hostname,
                site_id=site_id,
            )
            if removed_count <= 0:
                continue
            summary["targets_removed"] += int(removed_count)
            if updated_targets:
                cur.execute(
                    """
                    UPDATE scheduled_jobs
                       SET targets_json = ?,
                           updated_at = ?
                     WHERE id = ?
                    """,
                    (json.dumps(updated_targets or []), now_ts, job_id),
                )
                if int(cur.rowcount or 0):
                    summary["updated"] += 1
            else:
                cur.execute("DELETE FROM scheduled_jobs WHERE id = ?", (job_id,))
                if int(cur.rowcount or 0):
                    summary["deleted"] += 1
        return summary

    def _purge_device_rows(
        self,
        cur: sqlite3.Cursor,
        *,
        device_guid: str,
        hostname: str,
        agent_id: str,
        fingerprint: str,
    ) -> Dict[str, int]:
        normalized_guid = normalize_guid(device_guid) or ""
        normalized_hostname = (hostname or "").strip()
        normalized_agent_id = (agent_id or "").strip()
        normalized_fingerprint = (fingerprint or "").strip().lower()
        deleted: Dict[str, int] = {}

        activity_ids: List[int] = []
        if normalized_hostname:
            cur.execute(
                """
                SELECT id
                  FROM activity_history
                 WHERE LOWER(hostname) = LOWER(?)
                """,
                (normalized_hostname,),
            )
            activity_ids = [int(row[0]) for row in (cur.fetchall() or []) if row and row[0] is not None]

        target_run_ids: List[int] = []
        if normalized_hostname:
            cur.execute(
                """
                SELECT id
                  FROM scheduled_job_runs
                 WHERE LOWER(target_hostname) = LOWER(?)
                """,
                (normalized_hostname,),
            )
            target_run_ids = [int(row[0]) for row in (cur.fetchall() or []) if row and row[0] is not None]

        deleted["scheduled_job_run_targets"] = self._delete_by_id_list(
            cur,
            "scheduled_job_run_targets",
            "run_id",
            target_run_ids,
        )
        deleted["scheduled_job_run_targets"] += self._delete_where_any(
            cur,
            "scheduled_job_run_targets",
            [
                ("UPPER(device_guid) = ?", (normalized_guid,)) if normalized_guid else ("", ()),
                ("LOWER(hostname) = LOWER(?)", (normalized_hostname,)) if normalized_hostname else ("", ()),
            ],
        )

        deleted["scheduled_job_run_activity"] = self._delete_by_id_list(
            cur,
            "scheduled_job_run_activity",
            "run_id",
            target_run_ids,
        )
        deleted["scheduled_job_run_activity"] += self._delete_by_id_list(
            cur,
            "scheduled_job_run_activity",
            "activity_id",
            activity_ids,
        )

        deleted["scheduled_job_runs"] = self._delete_by_id_list(
            cur,
            "scheduled_job_runs",
            "id",
            target_run_ids,
        )
        deleted["activity_history"] = self._delete_by_id_list(
            cur,
            "activity_history",
            "id",
            activity_ids,
        )

        recap_predicates: List[Tuple[str, Tuple[Any, ...]]] = []
        if target_run_ids:
            placeholders = ",".join("?" for _ in target_run_ids)
            recap_predicates.append((f"scheduled_run_id IN ({placeholders})", tuple(target_run_ids)))
        if normalized_hostname:
            recap_predicates.append(("LOWER(hostname) = LOWER(?)", (normalized_hostname,)))
        if normalized_agent_id:
            recap_predicates.append(("agent_id = ?", (normalized_agent_id,)))
        deleted["ansible_play_recaps"] = self._delete_where_any(
            cur,
            "ansible_play_recaps",
            recap_predicates,
        )

        deleted["workflow_child_jobs"] = self._delete_where_any(
            cur,
            "workflow_child_jobs",
            [
                ("LOWER(target_hostname) = LOWER(?)", (normalized_hostname,)) if normalized_hostname else ("", ()),
            ],
        )
        deleted["device_software_inventory"] = self._delete_where_any(
            cur,
            "device_software_inventory",
            [
                ("UPPER(device_guid) = ?", (normalized_guid,)) if normalized_guid else ("", ()),
            ],
        )
        deleted["device_sites"] = self._delete_where_any(
            cur,
            "device_sites",
            [
                ("LOWER(device_hostname) = LOWER(?)", (normalized_hostname,)) if normalized_hostname else ("", ()),
            ],
        )
        deleted["device_approvals"] = self._delete_where_any(
            cur,
            "device_approvals",
            [
                ("UPPER(guid) = ?", (normalized_guid,)) if normalized_guid else ("", ()),
                ("LOWER(hostname_claimed) = LOWER(?)", (normalized_hostname,)) if normalized_hostname else ("", ()),
                (
                    "LOWER(ssl_key_fingerprint_claimed) = LOWER(?)",
                    (normalized_fingerprint,),
                )
                if normalized_fingerprint
                else ("", ()),
            ],
        )
        deleted["refresh_tokens"] = self._delete_where_any(
            cur,
            "refresh_tokens",
            [
                ("UPPER(guid) = ?", (normalized_guid,)) if normalized_guid else ("", ()),
            ],
        )
        deleted["device_keys"] = self._delete_where_any(
            cur,
            "device_keys",
            [
                ("UPPER(guid) = ?", (normalized_guid,)) if normalized_guid else ("", ()),
            ],
        )
        deleted["device_vpn_config"] = self._delete_where_any(
            cur,
            "device_vpn_config",
            [
                ("agent_id = ?", (normalized_agent_id,)) if normalized_agent_id else ("", ()),
            ],
        )
        deleted["device_vpn_ip_leases"] = self._delete_where_any(
            cur,
            "device_vpn_ip_leases",
            [
                ("agent_id = ?", (normalized_agent_id,)) if normalized_agent_id else ("", ()),
            ],
        )
        deleted["agent_service_account"] = self._delete_where_any(
            cur,
            "agent_service_account",
            [
                ("agent_id = ?", (normalized_agent_id,)) if normalized_agent_id else ("", ()),
            ],
        )
        deleted["devices"] = self._delete_where_any(
            cur,
            "devices",
            [
                ("UPPER(guid) = ?", (normalized_guid,)) if normalized_guid else ("", ()),
            ],
        )
        return deleted

    def _disconnect_live_device_runtime(self, agent_id: str) -> Dict[str, Any]:
        normalized_agent_id = (agent_id or "").strip()
        summary: Dict[str, Any] = {
            "vpn_disconnected": False,
            "vnc_sessions_revoked": 0,
            "vnc_collaboration_session_closed": False,
            "vnc_connections_closed": 0,
        }
        if not normalized_agent_id:
            return summary

        service = (
            getattr(self.adapters.context, "vpn_tunnel_service", None)
            or getattr(self.adapters, "_vpn_tunnel_service", None)
        )
        if service is not None and hasattr(service, "disconnect"):
            try:
                summary["vpn_disconnected"] = bool(
                    service.disconnect(
                        normalized_agent_id,
                        reason="device_purged",
                        force=True,
                    )
                )
            except Exception:
                self.logger.debug(
                    "Failed to disconnect active tunnel for purged agent_id=%s",
                    normalized_agent_id,
                    exc_info=True,
                )

        registry = (
            getattr(self.adapters.context, "vnc_registry", None)
            or getattr(getattr(self.adapters.context, "vnc_proxy", None), "registry", None)
        )
        if registry is not None and hasattr(registry, "revoke_agent"):
            try:
                summary["vnc_sessions_revoked"] = int(registry.revoke_agent(normalized_agent_id) or 0)
            except Exception:
                self.logger.debug(
                    "Failed to revoke VNC sessions for purged agent_id=%s",
                    normalized_agent_id,
                    exc_info=True,
                )

        collaboration_manager = getattr(self.adapters.context, "vnc_collaboration_manager", None)
        collaboration_result = None
        if collaboration_manager is not None and hasattr(collaboration_manager, "revoke_agent"):
            try:
                collaboration_result = collaboration_manager.revoke_agent(
                    normalized_agent_id,
                    reason="device_purged",
                )
                summary["vnc_collaboration_session_closed"] = bool(collaboration_result)
            except Exception:
                self.logger.debug(
                    "Failed to revoke VNC collaboration session for purged agent_id=%s",
                    normalized_agent_id,
                    exc_info=True,
                )
        proxy = getattr(self.adapters.context, "vnc_proxy", None)
        if (
            collaboration_result
            and proxy is not None
            and hasattr(proxy, "disconnect_session")
        ):
            try:
                session = collaboration_result.get("session") if isinstance(collaboration_result, dict) else None
                session_id = str(getattr(session, "session_id", "") or "").strip()
                if session_id:
                    summary["vnc_connections_closed"] = int(
                        proxy.disconnect_session(
                            session_id,
                            reason="device_purged",
                        )
                        or 0
                    )
            except Exception:
                self.logger.debug(
                    "Failed to close live VNC collaboration connections for purged agent_id=%s",
                    normalized_agent_id,
                    exc_info=True,
                )
        return summary

    def purge_device(self, guid: str) -> Tuple[Dict[str, Any], int]:
        normalized_guid = normalize_guid(guid)
        if not normalized_guid:
            return {"error": "invalid guid"}, 400

        current_user = self._current_user() or {}
        purged_by = (current_user.get("username") or "").strip() or None
        conn = self._db_conn()
        device_record: Optional[Dict[str, Any]] = None
        barrier_summary: Dict[str, Any] = {}
        scheduled_job_summary: Dict[str, int] = {
            "updated": 0,
            "deleted": 0,
            "targets_removed": 0,
        }
        deleted_rows: Dict[str, int] = {}
        try:
            device_purge_state.ensure_table(conn)
            cur = conn.cursor()
            device_record = self._load_device_purge_record(cur, normalized_guid)
            if not device_record:
                conn.rollback()
                return {"error": "not found"}, 404

            barrier_summary = device_purge_state.upsert_barrier(
                cur,
                guid=device_record["guid"],
                required_token_version=int(device_record.get("token_version") or 1) + 1,
                purged_by=purged_by,
                last_hostname=device_record.get("hostname"),
                last_agent_id=device_record.get("agent_id"),
            )
            scheduled_job_summary = self._rewrite_scheduled_jobs_for_purge(
                cur,
                device_guid=device_record["guid"],
                hostname=device_record.get("hostname") or "",
                site_id=device_record.get("site_id"),
            )
            deleted_rows = self._purge_device_rows(
                cur,
                device_guid=device_record["guid"],
                hostname=device_record.get("hostname") or "",
                agent_id=device_record.get("agent_id") or "",
                fingerprint=device_record.get("ssl_key_fingerprint") or "",
            )
            conn.commit()
        except Exception as exc:
            try:
                conn.rollback()
            except Exception:
                pass
            self.logger.debug("Failed to purge device guid=%s", normalized_guid, exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

        runtime_cleanup = self._disconnect_live_device_runtime(device_record.get("agent_id") or "")
        self.service_log(
            "server",
            (
                f"/api/devices/{normalized_guid}/purge completed "
                f"hostname={device_record.get('hostname') or '-'} "
                f"jobs_updated={scheduled_job_summary.get('updated', 0)} "
                f"jobs_deleted={scheduled_job_summary.get('deleted', 0)}"
            ),
            level="INFO",
        )
        return (
            {
                "status": "purged",
                "device_guid": device_record.get("guid") or normalized_guid,
                "hostname": device_record.get("hostname") or "",
                "required_token_version": barrier_summary.get("required_token_version") or 1,
                "scheduled_jobs": scheduled_job_summary,
                "deleted_rows": deleted_rows,
                "runtime_cleanup": runtime_cleanup,
            },
            200,
        )

    def list_views(self) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, name, columns_json, filters_json, created_at, updated_at
                  FROM device_list_views
              ORDER BY LOWER(name) ASC, id ASC
                """
            )
            rows = cur.fetchall()
            views = []
            for row in rows:
                views.append(
                    {
                        "id": row[0],
                        "name": row[1],
                        "columns": json.loads(row[2] or "[]"),
                        "filters": json.loads(row[3] or "{}"),
                        "created_at": row[4],
                        "updated_at": row[5],
                    }
                )
            return {"views": views}, 200
        except Exception as exc:
            self.logger.debug("Failed to list device views", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def get_view(self, view_id: int) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, name, columns_json, filters_json, created_at, updated_at
                  FROM device_list_views
                 WHERE id = ?
                """,
                (view_id,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "not found"}, 404
            payload = {
                "id": row[0],
                "name": row[1],
                "columns": json.loads(row[2] or "[]"),
                "filters": json.loads(row[3] or "{}"),
                "created_at": row[4],
                "updated_at": row[5],
            }
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def create_view(self, name: str, columns: List[str], filters: Dict[str, Any]) -> Tuple[Dict[str, Any], int]:
        now = int(time.time())
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO device_list_views(name, columns_json, filters_json, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?)
                """,
                (name, json.dumps(columns), json.dumps(filters), now, now),
            )
            view_id = cur.lastrowid
            conn.commit()
            cur.execute(
                """
                SELECT id, name, columns_json, filters_json, created_at, updated_at
                  FROM device_list_views
                 WHERE id = ?
                """,
                (view_id,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "creation_failed"}, 500
            payload = {
                "id": row[0],
                "name": row[1],
                "columns": json.loads(row[2] or "[]"),
                "filters": json.loads(row[3] or "{}"),
                "created_at": row[4],
                "updated_at": row[5],
            }
            return payload, 201
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to create device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def update_view(
        self,
        view_id: int,
        *,
        name: Optional[str] = None,
        columns: Optional[List[str]] = None,
        filters: Optional[Dict[str, Any]] = None,
    ) -> Tuple[Dict[str, Any], int]:
        fields: List[str] = []
        params: List[Any] = []
        if name is not None:
            fields.append("name = ?")
            params.append(name)
        if columns is not None:
            fields.append("columns_json = ?")
            params.append(json.dumps(columns))
        if filters is not None:
            fields.append("filters_json = ?")
            params.append(json.dumps(filters))
        fields.append("updated_at = ?")
        params.append(int(time.time()))
        params.append(view_id)
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                f"UPDATE device_list_views SET {', '.join(fields)} WHERE id = ?",
                params,
            )
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            conn.commit()
            return self.get_view(view_id)
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to update device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def delete_view(self, view_id: int) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("DELETE FROM device_list_views WHERE id = ?", (view_id,))
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to delete device view", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    # ------------------------------------------------------------------
    # Site management helpers
    # ------------------------------------------------------------------

    def _site_select_sql(self) -> str:
        return """
            SELECT s.id,
                   s.name,
                   s.description,
                   s.created_at,
                   COALESCE(ds.cnt, 0) AS device_count,
                   s.enrollment_code
              FROM sites AS s
         LEFT JOIN (
                   SELECT site_id, COUNT(*) AS cnt
                     FROM device_sites
                 GROUP BY site_id
               ) AS ds ON ds.site_id = s.id
        """

    def _fetch_site_row(self, cur: sqlite3.Cursor, site_id: int) -> Optional[Tuple[Any, ...]]:
        cur.execute(self._site_select_sql() + " WHERE s.id = ?", (site_id,))
        return cur.fetchone()

    def _site_install_metadata(self) -> Dict[str, str]:
        context = getattr(self.adapters, "context", None)
        base_url = ""
        public_hostname = ""
        if context is not None:
            try:
                base_url = str(resolve_public_base_url(context, req=request) or "").strip().rstrip("/")
            except Exception:
                base_url = ""
            try:
                public_hostname = str(resolve_public_hostname(context, req=request) or "").strip()
            except Exception:
                public_hostname = ""
        return {
            "public_base_url": base_url,
            "public_hostname": public_hostname,
        }

    def list_sites(self) -> Tuple[Dict[str, Any], int]:
        allowed_site_ids = self.site_access.site_ids_for_user(self._current_user())
        if allowed_site_ids is not None and not allowed_site_ids:
            return {"sites": [], **self._site_install_metadata()}, 200
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            sql = self._site_select_sql()
            params: List[Any] = []
            if allowed_site_ids is not None:
                placeholders = ",".join("?" for _ in sorted(allowed_site_ids))
                sql += f" WHERE s.id IN ({placeholders})"
                params.extend(sorted(allowed_site_ids))
            sql += " ORDER BY LOWER(s.name) ASC"
            cur.execute(sql, tuple(params))
            rows = cur.fetchall()
            sites = [_row_to_site(row) for row in rows]
            return {"sites": sites, **self._site_install_metadata()}, 200
        except Exception as exc:
            self.logger.debug("Failed to list sites", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def create_site(self, name: str, description: str) -> Tuple[Dict[str, Any], int]:
        if not name:
            return {"error": "name is required"}, 400
        now = int(time.time())
        user = self._current_user() or {}
        creator = user.get("username") or "system"
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            site_id: Optional[int] = None
            code_value = ""
            for _ in range(12):
                code_value = _generate_install_code()
                try:
                    cur.execute(
                        "INSERT INTO sites(name, description, created_at, enrollment_code) VALUES (?, ?, ?, ?)",
                        (name, description, now, code_value),
                    )
                    site_id = cur.lastrowid
                    break
                except sqlite3.IntegrityError as exc:
                    message = str(exc).lower()
                    if "sites.name" in message:
                        conn.rollback()
                        return {"error": "name already exists"}, 409
                    if "sites.enrollment_code" in message or "uq_sites_enrollment_code" in message:
                        continue
                    raise
            if site_id is None:
                conn.rollback()
                return {"error": "unable_to_generate_enrollment_code"}, 500
            conn.commit()
            row = self._fetch_site_row(cur, site_id)
            if not row:
                return {"error": "creation_failed"}, 500
            self.service_log(
                "server",
                f"site created id={site_id} code={code_value} by={creator}",
            )
            return _row_to_site(row), 201
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to create site", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def delete_sites(self, ids: List[Any]) -> Tuple[Dict[str, Any], int]:
        if not isinstance(ids, list) or not all(isinstance(x, (int, str)) for x in ids):
            return {"error": "ids must be a list"}, 400
        norm_ids: List[int] = []
        for value in ids:
            try:
                norm_ids.append(int(value))
            except Exception:
                return {"error": "invalid id"}, 400
        if not norm_ids:
            return {"status": "ok", "deleted": 0}, 200
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" * len(norm_ids))
            cur.execute(
                f"DELETE FROM user_site_assignments WHERE site_id IN ({placeholders})",
                tuple(norm_ids),
            )
            cur.execute(
                f"DELETE FROM device_sites WHERE site_id IN ({placeholders})",
                tuple(norm_ids),
            )
            cur.execute(
                f"DELETE FROM sites WHERE id IN ({placeholders})",
                tuple(norm_ids),
            )
            deleted = cur.rowcount
            conn.commit()
            return {"status": "ok", "deleted": deleted}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to delete sites", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def sites_device_map(self, hostnames: Optional[str]) -> Tuple[Dict[str, Any], int]:
        allowed_site_ids = self.site_access.site_ids_for_user(self._current_user())
        if allowed_site_ids is not None and not allowed_site_ids:
            return {"mapping": {}}, 200
        filter_set: set[str] = set()
        if hostnames:
            for part in hostnames.split(","):
                candidate = part.strip()
                if candidate:
                    filter_set.add(candidate)
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            params: List[Any] = []
            site_clause = ""
            if allowed_site_ids is not None:
                placeholders = ",".join("?" for _ in sorted(allowed_site_ids))
                site_clause = f" AND ds.site_id IN ({placeholders})"
                params.extend(sorted(allowed_site_ids))
            if filter_set:
                placeholders = ",".join("?" * len(filter_set))
                cur.execute(
                    f"""
                    SELECT ds.device_hostname, s.id, s.name
                      FROM device_sites ds
                      JOIN sites s ON s.id = ds.site_id
                     WHERE ds.device_hostname IN ({placeholders})
                       {site_clause}
                    """,
                    tuple(list(filter_set) + params),
                )
            else:
                cur.execute(
                    f"""
                    SELECT ds.device_hostname, s.id, s.name
                      FROM device_sites ds
                      JOIN sites s ON s.id = ds.site_id
                     WHERE 1 = 1
                       {site_clause}
                    """,
                    tuple(params),
                )
            mapping: Dict[str, Dict[str, Any]] = {}
            for hostname, site_id, site_name in cur.fetchall():
                mapping[str(hostname)] = {"site_id": site_id, "site_name": site_name}
            return {"mapping": mapping}, 200
        except Exception as exc:
            self.logger.debug("Failed to build site device map", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def assign_devices(self, site_id: Any, hostnames: List[str]) -> Tuple[Dict[str, Any], int]:
        try:
            site_id_int = int(site_id)
        except Exception:
            return {"error": "invalid site_id"}, 400
        if not isinstance(hostnames, list) or not all(isinstance(h, str) and h.strip() for h in hostnames):
            return {"error": "hostnames must be a list of strings"}, 400
        now = int(time.time())
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT 1 FROM sites WHERE id = ?", (site_id_int,))
            if not cur.fetchone():
                return {"error": "site not found"}, 404
            for hostname in hostnames:
                hn = hostname.strip()
                if not hn:
                    continue
                cur.execute(
                    """
                    INSERT INTO device_sites(device_hostname, site_id, assigned_at)
                    VALUES (?, ?, ?)
                    ON CONFLICT(device_hostname)
                    DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at
                    """,
                    (hn, site_id_int, now),
                )
            conn.commit()
            return {"status": "ok"}, 200
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to assign devices to site", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def rename_site(self, site_id: Any, new_name: str) -> Tuple[Dict[str, Any], int]:
        try:
            site_id_int = int(site_id)
        except Exception:
            return {"error": "invalid id"}, 400
        if not new_name:
            return {"error": "new_name is required"}, 400
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("UPDATE sites SET name = ? WHERE id = ?", (new_name, site_id_int))
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "site not found"}, 404
            conn.commit()
            cur.execute(
                self._site_select_sql() + " WHERE s.id = ?",
                (site_id_int,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "site not found"}, 404
            return _row_to_site(row), 200
        except sqlite3.IntegrityError:
            conn.rollback()
            return {"error": "name already exists"}, 409
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to rename site", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

    def repo_current_hash(self) -> Tuple[Dict[str, Any], int]:
        refresh_flag = (request.args.get("refresh") or "").strip().lower()
        force_refresh = refresh_flag in {"1", "true", "yes", "force", "refresh"}
        payload, status = self.repo_cache.current_repo_hash(
            request.args.get("repo"),
            request.args.get("branch"),
            ttl=request.args.get("ttl"),
            force_refresh=force_refresh,
        )
        return payload, status

    def agent_hash_lookup(self, ctx) -> Tuple[Dict[str, Any], int]:
        if ctx is None:
            self.service_log("server", "/api/agent/hash missing device auth context", level="ERROR")
            return {"error": "auth_context_missing"}, 500

        auth_guid = normalize_guid(getattr(ctx, "guid", None))
        if not auth_guid:
            return {"error": "guid_required"}, 403

        agent_guid = normalize_guid(request.args.get("agent_guid"))
        agent_id = _clean_device_str(request.args.get("agent_id") or request.args.get("id"))
        if not agent_guid and not agent_id:
            body = request.get_json(silent=True) or {}
            if agent_guid is None:
                agent_guid = normalize_guid((body.get("agent_guid") if isinstance(body, dict) else None))
            if not agent_id:
                agent_id = _clean_device_str((body.get("agent_id") if isinstance(body, dict) else None))

        if agent_guid and agent_guid != auth_guid:
            return {"error": "guid_mismatch"}, 403

        effective_guid = agent_guid or auth_guid

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            row = None
            if effective_guid:
                cur.execute(
                    """
                    SELECT guid, hostname, agent_hash, agent_id
                      FROM devices
                     WHERE LOWER(guid) = ?
                    """,
                    (effective_guid.lower(),),
                )
                row = cur.fetchone()
            if row is None and agent_id:
                cur.execute(
                    """
                    SELECT guid, hostname, agent_hash, agent_id
                      FROM devices
                     WHERE agent_id = ?
                     ORDER BY last_seen DESC, created_at DESC
                     LIMIT 1
                    """,
                    (agent_id,),
                )
                row = cur.fetchone()
            if row is None:
                return {"error": "agent hash not found"}, 404

            stored_guid, hostname, agent_hash, stored_agent_id = row
            normalized_guid = normalize_guid(stored_guid)
            if normalized_guid and normalized_guid != auth_guid:
                return {"error": "guid_mismatch"}, 403

            payload: Dict[str, Any] = {
                "agent_hash": (agent_hash or "").strip() or None,
                "agent_guid": normalized_guid or effective_guid,
            }
            resolved_agent_id = _clean_device_str(stored_agent_id) or agent_id
            if resolved_agent_id:
                payload["agent_id"] = resolved_agent_id
            if hostname:
                payload["hostname"] = hostname
            return payload, 200
        except Exception as exc:
            self.service_log("server", f"/api/agent/hash lookup error: {exc}")
            return {"error": "internal error"}, 500
        finally:
            conn.close()

    def agent_hash_update(self, ctx) -> Tuple[Dict[str, Any], int]:
        if ctx is None:
            self.service_log("server", "/api/agent/hash missing device auth context", level="ERROR")
            return {"error": "auth_context_missing"}, 500

        auth_guid = normalize_guid(getattr(ctx, "guid", None))
        if not auth_guid:
            return {"error": "guid_required"}, 403

        payload = request.get_json(silent=True) or {}
        agent_hash = _clean_device_str(payload.get("agent_hash"))
        agent_id = _clean_device_str(payload.get("agent_id"))
        requested_guid = normalize_guid(payload.get("agent_guid"))

        if not agent_hash:
            return {"error": "agent_hash required"}, 400

        if requested_guid and requested_guid != auth_guid:
            return {"error": "guid_mismatch"}, 403

        effective_guid = requested_guid or auth_guid
        resolved_agent_id = agent_id or ""

        if not effective_guid and not resolved_agent_id:
            return {"error": "agent_hash and agent_guid or agent_id required"}, 400

        conn = self._db_conn()
        hostname: Optional[str] = None
        try:
            cur = conn.cursor()
            target_guid: Optional[str] = None

            if effective_guid:
                cur.execute(
                    """
                    SELECT guid, hostname, agent_id
                      FROM devices
                     WHERE LOWER(guid) = ?
                    """,
                    (effective_guid.lower(),),
                )
                row = cur.fetchone()
                if row:
                    target_guid = row[0] or effective_guid
                    hostname = (row[1] or "").strip() or None
                    stored_agent_id = _clean_device_str(row[2])
                    if not resolved_agent_id and stored_agent_id:
                        resolved_agent_id = stored_agent_id
                    normalized_guid = normalize_guid(target_guid)
                    if normalized_guid:
                        effective_guid = normalized_guid

            if target_guid is None and resolved_agent_id:
                cur.execute(
                    """
                    SELECT guid, hostname, agent_id
                      FROM devices
                     WHERE agent_id = ?
                     ORDER BY last_seen DESC, created_at DESC
                     LIMIT 1
                    """,
                    (resolved_agent_id,),
                )
                row = cur.fetchone()
                if row:
                    target_guid = row[0] or ""
                    hostname = (row[1] or "").strip() or hostname
                    stored_agent_id = _clean_device_str(row[2])
                    if not resolved_agent_id and stored_agent_id:
                        resolved_agent_id = stored_agent_id
                    normalized_guid = normalize_guid(row[0])
                    if normalized_guid:
                        if auth_guid and normalized_guid != auth_guid:
                            return {"error": "guid_mismatch"}, 403
                        effective_guid = normalized_guid

            if target_guid is None:
                ignored_payload: Dict[str, Any] = {
                    "status": "ignored",
                    "agent_hash": agent_hash,
                }
                if effective_guid:
                    ignored_payload["agent_guid"] = effective_guid
                if resolved_agent_id:
                    ignored_payload["agent_id"] = resolved_agent_id
                return ignored_payload, 200

            if resolved_agent_id:
                cur.execute(
                    """
                    UPDATE devices
                       SET agent_hash = ?,
                           agent_id = ?
                     WHERE guid = ?
                    """,
                    (agent_hash, resolved_agent_id, target_guid),
                )
            else:
                cur.execute(
                    """
                    UPDATE devices
                       SET agent_hash = ?
                     WHERE guid = ?
                    """,
                    (agent_hash, target_guid),
                )

            if cur.rowcount == 0:
                cur.execute(
                    """
                    UPDATE devices
                       SET agent_hash = ?
                     WHERE LOWER(guid) = ?
                    """,
                    (agent_hash, effective_guid.lower()),
                )
                if resolved_agent_id and cur.rowcount > 0:
                    cur.execute(
                        """
                        UPDATE devices
                           SET agent_id = ?
                         WHERE LOWER(guid) = ?
                        """,
                        (resolved_agent_id, effective_guid.lower()),
                    )

            conn.commit()
        except Exception as exc:
            try:
                conn.rollback()
            except Exception:
                pass
            self.service_log("server", f"/api/agent/hash error: {exc}")
            return {"error": "internal error"}, 500
        finally:
            conn.close()

        response: Dict[str, Any] = {
            "status": "ok",
            "agent_hash": agent_hash,
        }
        if resolved_agent_id:
            response["agent_id"] = resolved_agent_id
        if effective_guid:
            response["agent_guid"] = effective_guid
        if hostname:
            response["hostname"] = hostname
        return response, 200

    def agent_hash_list(self) -> Tuple[Dict[str, Any], int]:
        if not _is_internal_request(request.remote_addr):
            remote_addr = (request.remote_addr or "unknown").strip() or "unknown"
            self.service_log(
                "server",
                f"/api/agent/hash_list denied non-local request from {remote_addr}",
                level="WARN",
            )
            return {"error": "forbidden"}, 403
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT guid, hostname, agent_hash, agent_id FROM devices",
            )
            agents = []
            for guid, hostname, agent_hash, agent_id in cur.fetchall():
                agents.append(
                    {
                        "agent_guid": normalize_guid(guid) or None,
                        "hostname": hostname or None,
                        "agent_hash": (agent_hash or "").strip() or None,
                        "agent_id": (agent_id or "").strip() or None,
                        "source": "database",
                    }
                )
            agents.sort(key=lambda rec: (rec.get("hostname") or "", rec.get("agent_id") or ""))
            return {"agents": agents}, 200
        except Exception as exc:
            self.service_log("server", f"/api/agent/hash_list error: {exc}")
            return {"error": "internal error"}, 500
        finally:
            conn.close()

def register_management(app, adapters: "EngineServiceAdapters") -> None:
    """Register device management endpoints onto the Flask app."""

    service = DeviceManagementService(app, adapters)
    blueprint = Blueprint("devices", __name__)

    @blueprint.route("/api/agent/details", methods=["POST"])
    @require_device_auth(adapters.device_auth_manager)
    def _agent_details():
        payload, status = service.save_agent_details()
        return jsonify(payload), status

    @blueprint.route("/api/agent/hash", methods=["GET", "POST"])
    @require_device_auth(adapters.device_auth_manager)
    def _agent_hash():
        ctx = getattr(g, "device_auth", None)
        if request.method == "GET":
            payload, status = service.agent_hash_lookup(ctx)
        else:
            payload, status = service.agent_hash_update(ctx)
        return jsonify(payload), status

    @blueprint.route("/api/agents", methods=["GET"])
    def _list_agents():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.list_agents()
        return jsonify(payload), status

    @blueprint.route("/api/devices", methods=["GET"])
    def _list_devices():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.list_devices()
        return jsonify(payload), status

    @blueprint.route("/api/devices/search", methods=["GET"])
    def _search_devices():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.search_devices_by_hostname(request.args.get("hostname") or "")
        return jsonify(payload), status

    @blueprint.route("/api/devices/<guid>", methods=["GET"])
    def _device_by_guid(guid: str):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.get_device_by_guid(guid)
        return jsonify(payload), status

    @blueprint.route("/api/devices/<guid>/purge", methods=["POST"])
    def _device_purge(guid: str):
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.purge_device(guid)
        return jsonify(payload), status

    @blueprint.route("/api/device/details/<hostname>", methods=["GET"])
    def _device_details(hostname: str):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.get_device_details(hostname)
        return jsonify(payload), status

    @blueprint.route("/api/device/description/<hostname>", methods=["POST"])
    def _set_description(hostname: str):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        description = (body.get("description") or "").strip()
        payload, status = service.set_device_description(hostname, description)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views", methods=["GET"])
    def _list_views():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.list_views()
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views/<int:view_id>", methods=["GET"])
    def _get_view(view_id: int):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.get_view(view_id)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views", methods=["POST"])
    def _create_view():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        name = (data.get("name") or "").strip()
        columns = data.get("columns") or []
        filters = data.get("filters") or {}
        if not name:
            return jsonify({"error": "name is required"}), 400
        if name.lower() == "default view":
            return jsonify({"error": "reserved name"}), 400
        if not isinstance(columns, list) or not all(isinstance(col, str) for col in columns):
            return jsonify({"error": "columns must be a list of strings"}), 400
        if not isinstance(filters, dict):
            return jsonify({"error": "filters must be an object"}), 400
        payload, status = service.create_view(name, columns, filters)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views/<int:view_id>", methods=["PUT"])
    def _update_view(view_id: int):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        name = data.get("name")
        columns = data.get("columns")
        filters = data.get("filters")
        if name is not None:
            name = (name or "").strip()
            if not name:
                return jsonify({"error": "name cannot be empty"}), 400
            if name.lower() == "default view":
                return jsonify({"error": "reserved name"}), 400
        if columns is not None:
            if not isinstance(columns, list) or not all(isinstance(col, str) for col in columns):
                return jsonify({"error": "columns must be a list of strings"}), 400
        if filters is not None and not isinstance(filters, dict):
            return jsonify({"error": "filters must be an object"}), 400
        payload, status = service.update_view(view_id, name=name, columns=columns, filters=filters)
        return jsonify(payload), status

    @blueprint.route("/api/device_list_views/<int:view_id>", methods=["DELETE"])
    def _delete_view(view_id: int):
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.delete_view(view_id)
        return jsonify(payload), status

    @blueprint.route("/api/sites", methods=["GET"])
    def _sites_list():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.list_sites()
        return jsonify(payload), status

    @blueprint.route("/api/sites", methods=["POST"])
    def _sites_create():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        name = (data.get("name") or "").strip()
        description = (data.get("description") or "").strip()
        payload, status = service.create_site(name, description)
        return jsonify(payload), status

    @blueprint.route("/api/sites/delete", methods=["POST"])
    def _sites_delete():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        ids = data.get("ids") or []
        payload, status = service.delete_sites(ids)
        return jsonify(payload), status

    @blueprint.route("/api/sites/device_map", methods=["GET"])
    def _sites_device_map():
        requirement = service._require_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.sites_device_map(request.args.get("hostnames"))
        return jsonify(payload), status

    @blueprint.route("/api/sites/assign", methods=["POST"])
    def _sites_assign():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        payload, status = service.assign_devices(data.get("site_id"), data.get("hostnames") or [])
        return jsonify(payload), status

    @blueprint.route("/api/sites/rename", methods=["POST"])
    def _sites_rename():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        payload, status = service.rename_site(data.get("id"), (data.get("new_name") or "").strip())
        return jsonify(payload), status

    @blueprint.route("/api/repo/current_hash", methods=["GET"])
    def _repo_current_hash():
        requirement = service._require_device_or_login()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.repo_current_hash()
        return jsonify(payload), status

    @blueprint.route("/api/agent/hash_list", methods=["GET"])
    def _agent_hash_list():
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.agent_hash_list()
        return jsonify(payload), status

    app.register_blueprint(blueprint)
