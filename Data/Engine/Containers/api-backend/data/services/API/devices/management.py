# ======================================================
# Data\Engine\services\API\devices\management.py
# Description: Device inventory and repository hash endpoints for the Engine API transition layer.
#
# API Endpoints (if applicable):
# - POST /api/agent/details (Device Authenticated) - Ingests hardware and inventory payloads from enrolled agents.
# - GET /api/agents (Token Authenticated) - Lists online collectors grouped by hostname and run context.
# - GET /api/devices (Token Authenticated) - Returns a summary list of known devices for the WebUI transition.
# - GET /api/devices/search (Token Authenticated) - Returns hostname search matches scoped to the operator's assigned sites unless the operator is an admin.
# - GET /api/devices/<guid> (Token Authenticated) - Retrieves a single device record by GUID, including summary fields.
# - POST /api/devices/<guid>/purge (Token Authenticated (Admin)) - Holistically purges a device, its trust records, and scheduled-job references.
# - PUT /api/devices/<guid>/agent-release-channel (Token Authenticated (Admin)) - Updates an agent release channel override and optional source branch.
# - GET /api/device/details/<hostname> (Token Authenticated) - Returns full device details keyed by hostname.
# - POST /api/device/description/<hostname> (Token Authenticated) - Updates the human-readable description for a device.
# ======================================================

"""Device management endpoints for the Borealis Engine API."""
from __future__ import annotations

import json
import hashlib
import logging
import os
from Data.Engine.db import dbapi as sqlite3
import threading
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, session, g
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....auth import device_purge_state
from ....auth.guid_utils import normalize_guid
from ....auth.device_auth import require_device_auth
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...activity_history import update_activity_history_row
from ..scheduled_jobs.targets import prune_device_targets
from .agent_role_health import (
    merge_agent_role_health,
    normalize_agent_role_health,
    serialize_agent_role_health,
)
from .software_uninstall import (
    enrich_software_inventory_with_uninstall,
    normalize_software_inventory as _shared_normalize_software_inventory,
)
from .software_icons import (
    apply_engine_global_icon_overrides,
    normalize_software_icon_payloads,
    upsert_software_icon_assets,
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


def _normalize_host_key(value: Any) -> str:
    try:
        return str(value or "").strip().lower()
    except Exception:
        return ""


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
    existing = prev if isinstance(prev, dict) else {}
    incoming_map = incoming if isinstance(incoming, dict) else {}
    out: Dict[str, Any] = dict(existing)
    for key, value in incoming_map.items():
        if isinstance(value, dict):
            prior_value = out.get(key)
            out[key] = _deep_merge_preserve(prior_value if isinstance(prior_value, dict) else {}, value)
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


def _normalize_agent_branch(value: Any) -> Optional[str]:
    text = _clean_device_str(value)
    if not text:
        return None
    if len(text) > 160:
        return None
    if any(ord(ch) < 32 or ch.isspace() for ch in text):
        return None
    if (
        text.startswith(("/", "."))
        or text.endswith(("/", "."))
        or ".." in text
        or "//" in text
        or "@{" in text
        or "\\" in text
        or ":" in text
    ):
        return None
    allowed = set("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._/-")
    if any(ch not in allowed for ch in text):
        return None
    return text


def _normalize_software_source(value: Any) -> str:
    text = _clean_device_str(value)
    if not text:
        return "local_installed"
    return _SOFTWARE_SOURCE_ALIASES.get(text.strip().lower(), "local_installed")


def _normalize_software_inventory(raw: Any) -> List[Dict[str, Any]]:
    return _shared_normalize_software_inventory(raw)


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
    payload["agent_release_channel"] = _clean_device_str(
        summary.get("agent_release_channel")
        or summary.get("release_channel")
    )
    payload["agent_branch"] = _normalize_agent_branch(
        summary.get("agent_branch")
        or summary.get("branch")
        or summary.get("repo_ref")
        or summary.get("repo_branch")
    )
    payload["agent_update_channel"] = _clean_device_str(
        summary.get("agent_update_channel")
        or summary.get("target_channel")
        or summary.get("agent_release_channel_effective")
    )
    payload["agent_update_target_build_id"] = _clean_device_str(
        summary.get("agent_update_target_build_id")
        or summary.get("target_build_id")
        or summary.get("agent_target_build_id")
    )
    payload["agent_update_state"] = _clean_device_str(summary.get("agent_update_state") or summary.get("update_state"))
    payload["agent_update_error"] = _clean_device_str(summary.get("agent_update_error") or summary.get("last_update_error"))
    payload["agent_update_source"] = _clean_device_str(summary.get("agent_update_source") or summary.get("update_source"))
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
            connection_endpoint,
            agent_release_channel,
            agent_branch,
            agent_update_channel,
            agent_update_target_build_id,
            agent_update_state,
            agent_update_error,
            agent_update_source
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
            connection_endpoint=COALESCE(NULLIF(excluded.connection_endpoint, ''), {DEVICE_TABLE}.connection_endpoint),
            agent_release_channel=COALESCE(NULLIF(excluded.agent_release_channel, ''), {DEVICE_TABLE}.agent_release_channel),
            agent_branch=COALESCE(NULLIF(excluded.agent_branch, ''), {DEVICE_TABLE}.agent_branch),
            agent_update_channel=COALESCE(NULLIF(excluded.agent_update_channel, ''), {DEVICE_TABLE}.agent_update_channel),
            agent_update_target_build_id=COALESCE(NULLIF(excluded.agent_update_target_build_id, ''), {DEVICE_TABLE}.agent_update_target_build_id),
            agent_update_state=COALESCE(NULLIF(excluded.agent_update_state, ''), {DEVICE_TABLE}.agent_update_state),
            agent_update_error=COALESCE(NULLIF(excluded.agent_update_error, ''), {DEVICE_TABLE}.agent_update_error),
            agent_update_source=COALESCE(NULLIF(excluded.agent_update_source, ''), {DEVICE_TABLE}.agent_update_source)
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
        column_values.get("agent_release_channel"),
        column_values.get("agent_branch"),
        column_values.get("agent_update_channel"),
        column_values.get("agent_update_target_build_id"),
        column_values.get("agent_update_state"),
        column_values.get("agent_update_error"),
        column_values.get("agent_update_source"),
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
        "agent_release_channel_override",
        "agent_release_channel",
        "agent_branch",
        "agent_update_channel",
        "agent_update_target_build_id",
        "agent_update_state",
        "agent_update_error",
        "agent_update_source",
    )

    def __init__(self, app, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger or logging.getLogger(__name__)
        self.repo_cache = adapters.github_integration
        self.agent_release_manager = getattr(adapters, "agent_release_manager", None)
        self._repo_hash_cache: Dict[str, Any] = {"sha": "", "expires_at": 0}
        self._details_ingest_lock = threading.Lock()
        self._details_ingest_cache: Dict[str, Dict[str, Any]] = {}
        self.site_access = UserSiteAccessManager(self.db_conn_factory, logger=self.logger)

    def _details_cache_key(self, guid: Optional[str], hostname: str, service_mode: Optional[str]) -> str:
        identity = normalize_guid(guid) if guid else ""
        if not identity:
            identity = (_clean_device_str(hostname) or "").lower()
        return f"{identity}|{(_clean_device_str(service_mode) or '').lower()}"

    def _details_duplicate_recent(self, cache_key: str, payload_hash: str, now_mono: float) -> bool:
        if not cache_key or not payload_hash:
            return False
        with self._details_ingest_lock:
            cached = self._details_ingest_cache.get(cache_key)
            if not cached:
                return False
            if cached.get("payload_hash") != payload_hash:
                return False
            try:
                accepted_at = float(cached.get("accepted_at") or 0.0)
            except Exception:
                accepted_at = 0.0
            if now_mono - accepted_at > 60.0:
                return False
            cached["seen_at"] = now_mono
            return True

    def _remember_details_ingest(self, cache_key: str, payload_hash: str, now_mono: float) -> None:
        if not cache_key or not payload_hash:
            return
        with self._details_ingest_lock:
            self._details_ingest_cache[cache_key] = {
                "payload_hash": payload_hash,
                "accepted_at": now_mono,
                "seen_at": now_mono,
            }
            if len(self._details_ingest_cache) > 2048:
                stale_keys = sorted(
                    self._details_ingest_cache,
                    key=lambda key: float(self._details_ingest_cache[key].get("seen_at") or 0.0),
                )
                for stale_key in stale_keys[: max(1, len(self._details_ingest_cache) - 2048)]:
                    self._details_ingest_cache.pop(stale_key, None)

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

    def _emit_device_inventory_changed(self, hostname: str, *, change: str) -> None:
        socketio = getattr(self.adapters.context, "socketio", None)
        normalized_hostname = _clean_device_str(hostname) or ""
        normalized_change = _clean_device_str(change) or ""
        if socketio is None or not normalized_hostname or not normalized_change:
            return
        try:
            socketio.emit(
                "device_inventory_changed",
                {
                    "hostname": normalized_hostname,
                    "change": normalized_change,
                },
            )
        except Exception:
            self.logger.debug("Failed to emit device_inventory_changed for hostname=%s", normalized_hostname, exc_info=True)

    def _emit_agent_release_channel_changed(self, hostname: str, payload: Dict[str, Any]) -> None:
        normalized_hostname = _clean_device_str(hostname) or ""
        if not normalized_hostname:
            return
        emitter = getattr(self.adapters.context, "emit_host_service_event", None)
        if not callable(emitter):
            return
        try:
            emitter(normalized_hostname, "system", "agent_release_channel_changed", payload)
        except Exception:
            self.logger.debug("Failed to emit agent_release_channel_changed for hostname=%s", normalized_hostname, exc_info=True)

    def _reconcile_agent_maintenance_operation(
        self,
        *,
        hostname: str,
        update_status: Dict[str, Any],
        release_channel: str,
        branch: str,
        installed_build_id: str,
    ) -> None:
        operation_id = _clean_device_str(update_status.get("operation_id"))
        if not operation_id:
            return
        raw_state = _clean_device_str(update_status.get("state") or update_status.get("status")).lower()
        if not raw_state:
            return
        terminal_success = raw_state in {"success", "completed", "complete", "up_to_date", "applied"}
        terminal_failed = raw_state in {"failed", "error"}
        run_status = "Running"
        activity_status = "Running"
        finished_at = None
        now = int(time.time())
        stdout = (
            f"Agent reported operation_id={operation_id} state={raw_state} "
            f"release_channel={release_channel or '-'} branch={branch or '-'} "
            f"installed_build_id={installed_build_id or '-'}\n"
        )
        stderr = ""
        if terminal_success:
            run_status = "Success"
            activity_status = "Success"
            finished_at = now
        elif terminal_failed:
            run_status = "Failed"
            activity_status = "Failed"
            finished_at = now
            stderr = _clean_device_str(update_status.get("last_error")) or "Agent update operation failed."

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            like_token = f"%{operation_id}%"
            cur.execute(
                """
                SELECT r.id, h.id
                  FROM scheduled_job_runs r
                  JOIN scheduled_job_run_activity s ON s.run_id = r.id
                  JOIN activity_history h ON h.id = s.activity_id
                  JOIN scheduled_jobs j ON j.id = r.job_id
                 WHERE LOWER(COALESCE(h.hostname, '')) = LOWER(?)
                   AND (COALESCE(h.metadata_json, '') LIKE ? OR COALESCE(h.stdout, '') LIKE ?)
                   AND COALESCE(j.job_kind, '') = 'agent_maintenance'
                   AND LOWER(COALESCE(r.status, '')) NOT IN ('success', 'failed', 'skipped')
                """,
                (hostname, like_token, like_token),
            )
            rows = cur.fetchall() or []
            for run_id, activity_id in rows:
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           updated_at=?,
                           finished_ts=COALESCE(?, finished_ts),
                           error=?
                     WHERE id=?
                    """,
                    (run_status, now, finished_at, stderr[:512], run_id),
                )
                cur.execute(
                    """
                    UPDATE scheduled_job_run_targets
                       SET resolution_status=?,
                           resolution_reason=?
                     WHERE run_id=?
                    """,
                    ("eligible" if not terminal_failed else "unresolved", stderr[:512], run_id),
                )
                update_activity_history_row(
                    conn,
                    activity_id,
                    status=activity_status,
                    stdout=stdout,
                    stderr=(stderr + "\n") if stderr else "",
                    append_output=True,
                    updated_at=now,
                    finished_at=finished_at,
                )
            conn.commit()
        finally:
            conn.close()

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

    def _resolve_agent_target(self, channel_override: Any) -> Tuple[str, str, str]:
        normalized_override = _clean_device_str(channel_override)
        if self.agent_release_manager is not None:
            try:
                effective_channel = self.agent_release_manager.resolve_effective_channel(normalized_override)
                target = self.agent_release_manager.target_for_override(normalized_override)
                return (
                    effective_channel,
                    (_clean_device_str(target.get("build_id")) or "").lower(),
                    _clean_device_str(target.get("published_at")),
                )
            except Exception:
                pass
        return "", self._current_target_repo_hash(), ""

    def _attach_agent_version_status(
        self,
        payload: Dict[str, Any],
        *,
        target_build_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        if not isinstance(payload, dict):
            return payload
        summary = payload.get("summary") if isinstance(payload.get("summary"), dict) else {}
        channel_override = (
            _clean_device_str(payload.get("agent_release_channel_override"))
            or _clean_device_str(summary.get("agent_release_channel_override"))
        )
        effective_channel = ""
        target_published_at = ""
        if target_build_id is None:
            effective_channel, target_build_id, target_published_at = self._resolve_agent_target(channel_override)
        installed_build_id = (
            _clean_device_str(payload.get("agent_build_id"))
            or _clean_device_str(payload.get("agent_hash"))
            or _clean_device_str(summary.get("agent_build_id"))
            or _clean_device_str(summary.get("agent_hash"))
        )
        status = self._compute_agent_version_status(installed_build_id, target_build_id)
        payload["agent_version_status"] = status
        payload["agent_target_build_id"] = target_build_id or ""
        payload["agent_target_published_at"] = target_published_at or ""
        payload["agent_release_channel_override"] = channel_override or None
        payload["agent_release_channel"] = (
            _clean_device_str(payload.get("agent_release_channel"))
            or _clean_device_str(summary.get("agent_release_channel"))
            or ""
        )
        payload["agent_branch"] = _normalize_agent_branch(
            payload.get("agent_branch")
            or summary.get("agent_branch")
        ) or ""
        payload["agent_release_channel_effective"] = effective_channel or (
            _clean_device_str(summary.get("agent_release_channel_effective"))
            or _clean_device_str(payload.get("agent_release_channel_effective"))
        )
        if installed_build_id:
            payload["agent_build_id"] = installed_build_id
        if isinstance(summary, dict):
            summary["agent_version_status"] = status
            summary["agent_target_build_id"] = target_build_id or ""
            summary["agent_target_published_at"] = target_published_at or ""
            summary["agent_release_channel_override"] = channel_override or None
            summary["agent_release_channel"] = payload.get("agent_release_channel") or ""
            summary["agent_branch"] = payload.get("agent_branch") or ""
            if payload.get("agent_release_channel_effective"):
                summary["agent_release_channel_effective"] = payload.get("agent_release_channel_effective")
            if installed_build_id and not summary.get("agent_build_id"):
                summary["agent_build_id"] = installed_build_id
        details = payload.get("details")
        if isinstance(details, dict):
            detail_summary = details.setdefault("summary", {})
            if isinstance(detail_summary, dict):
                detail_summary["agent_version_status"] = status
                detail_summary["agent_target_build_id"] = target_build_id or ""
                detail_summary["agent_target_published_at"] = target_published_at or ""
                detail_summary["agent_release_channel_override"] = channel_override or None
                detail_summary["agent_release_channel"] = payload.get("agent_release_channel") or ""
                detail_summary["agent_branch"] = payload.get("agent_branch") or ""
                if payload.get("agent_release_channel_effective"):
                    detail_summary["agent_release_channel_effective"] = payload.get("agent_release_channel_effective")
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
        role_health = normalize_agent_role_health(
            mapping.get("agent_role_health"),
            mark_stale=True,
            now_ts=int(time.time()),
        )
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
            "agent_release_channel_override": mapping.get("agent_release_channel_override") or "",
            "agent_release_channel": mapping.get("agent_release_channel") or "",
            "agent_branch": mapping.get("agent_branch") or "",
            "agent_update_channel": mapping.get("agent_update_channel") or "",
            "agent_update_target_build_id": mapping.get("agent_update_target_build_id") or "",
            "agent_update_state": mapping.get("agent_update_state") or "",
            "agent_update_error": mapping.get("agent_update_error") or "",
            "agent_update_source": mapping.get("agent_update_source") or "",
        }
        software_rows = enrich_software_inventory_with_uninstall(
            _normalize_software_inventory(_safe_json(mapping.get("software"), [])),
            summary.get("operating_system") or "",
        )
        details = {
            "summary": summary,
            "memory": _safe_json(mapping.get("memory"), []),
            "network": _safe_json(mapping.get("network"), []),
            "software": software_rows,
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
            "agent_release_channel_override": summary["agent_release_channel_override"],
            "agent_release_channel": summary["agent_release_channel"],
            "agent_branch": summary["agent_branch"],
            "agent_update_channel": summary["agent_update_channel"],
            "agent_update_target_build_id": summary["agent_update_target_build_id"],
            "agent_update_state": summary["agent_update_state"],
            "agent_update_error": summary["agent_update_error"],
            "agent_update_source": summary["agent_update_source"],
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
            registry = getattr(getattr(self.adapters, "context", None), "agent_socket_registry", None)
            registry_snapshot: Dict[str, Dict[str, Any]] = {}
            if registry is not None and callable(getattr(registry, "snapshot", None)):
                try:
                    registry_snapshot = registry.snapshot() or {}
                except Exception:
                    self.logger.debug("Failed to snapshot agent socket registry", exc_info=True)
                    registry_snapshot = {}
            helper_contexts_by_host: Dict[str, Tuple[str, ...]] = {}
            system_socket_by_host: Dict[str, Dict[str, Any]] = {}
            for socket_record in registry_snapshot.values():
                host_key = _normalize_host_key(socket_record.get("hostname"))
                mode_key = _normalize_service_mode(
                    socket_record.get("service_mode"),
                    socket_record.get("agent_id"),
                )
                if host_key and mode_key == "system":
                    system_socket_by_host[host_key] = dict(socket_record)
                    helper_contexts_by_host[host_key] = tuple(
                        str(item or "").strip().lower()
                        for item in (socket_record.get("helper_contexts") or [])
                        if str(item or "").strip()
                    )
            grouped: Dict[str, Dict[str, Dict[str, Any]]] = {}
            now = time.time()
            for record in devices:
                hostname = (record.get("hostname") or "").strip() or "unknown"
                host_key = _normalize_host_key(hostname)
                agent_id = (record.get("agent_id") or "").strip()
                mode = _normalize_service_mode(record.get("service_mode"), agent_id)
                helper_contexts = list(helper_contexts_by_host.get(host_key, ()))
                if mode == "currentuser" and "currentuser" in helper_contexts:
                    mode = "system"
                    socket_record = system_socket_by_host.get(host_key) or {}
                    socket_agent_id = str(socket_record.get("agent_id") or "").strip()
                    if socket_agent_id:
                        agent_id = socket_agent_id
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
                    "helper_contexts": helper_contexts,
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
            details = payload.get("details") if isinstance(payload.get("details"), dict) else {}
            software_rows = details.get("software") if isinstance(details.get("software"), list) else []
            if software_rows:
                details["software"] = apply_engine_global_icon_overrides(self._db_conn, software_rows)
                payload["software"] = details["software"]
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

        raw_body = request.get_data(cache=True) or b""
        payload_hash = hashlib.sha256(raw_body).hexdigest() if raw_body else ""
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
        software_icon_payloads = normalize_software_icon_payloads(details.pop("software_icon_payloads", None))

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
        incoming_update_status = (
            payload.get("agent_update_status")
            if isinstance(payload.get("agent_update_status"), dict)
            else (details.get("summary") or {}).get("agent_update_status")
        )
        if not isinstance(incoming_update_status, dict):
            incoming_update_status = {}
        incoming_agent_release_channel = _clean_device_str(
            payload.get("agent_release_channel")
            or (details.get("summary") or {}).get("agent_release_channel")
        )
        incoming_agent_branch = _normalize_agent_branch(
            payload.get("agent_branch")
            or (details.get("summary") or {}).get("agent_branch")
        )

        raw_guid = getattr(ctx, "guid", None)
        try:
            auth_guid = normalize_guid(raw_guid) if raw_guid else None
        except Exception:
            auth_guid = None

        fingerprint = _clean_device_str(getattr(ctx, "ssl_key_fingerprint", None))
        fingerprint_lower = fingerprint.lower() if fingerprint else ""
        scope_hint = getattr(ctx, "service_mode", None)
        details_cache_key = self._details_cache_key(auth_guid, hostname, incoming_service_mode or scope_hint)
        details_now_mono = time.monotonic()
        if self._details_duplicate_recent(details_cache_key, payload_hash, details_now_mono):
            return {"status": "ok", "coalesced": True}, 200

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
            if incoming_agent_release_channel:
                incoming_summary["agent_release_channel"] = incoming_agent_release_channel
            if incoming_agent_branch:
                incoming_summary["agent_branch"] = incoming_agent_branch
            if incoming_update_status:
                update_channel = _clean_device_str(
                    incoming_update_status.get("target_channel")
                    or incoming_update_status.get("effective_channel")
                )
                update_target_build_id = _clean_device_str(incoming_update_status.get("target_build_id"))
                update_state = _clean_device_str(incoming_update_status.get("state"))
                update_error = _clean_device_str(incoming_update_status.get("last_error"))
                update_source = _clean_device_str(incoming_update_status.get("last_source"))
                if update_channel:
                    incoming_summary["agent_update_channel"] = update_channel
                if update_target_build_id:
                    incoming_summary["agent_update_target_build_id"] = update_target_build_id
                if update_state:
                    incoming_summary["agent_update_state"] = update_state
                if update_error:
                    incoming_summary["agent_update_error"] = update_error
                if update_source:
                    incoming_summary["agent_update_source"] = update_source

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
            software_changed = json.dumps(
                _shared_normalize_software_inventory(prev_details.get("software") if isinstance(prev_details, dict) else []),
                sort_keys=True,
            ) != json.dumps(
                _shared_normalize_software_inventory(merged.get("software")),
                sort_keys=True,
            )
            if software_icon_payloads:
                upsert_software_icon_assets(cur, software_icon_payloads)

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
            if incoming_update_status:
                try:
                    self._reconcile_agent_maintenance_operation(
                        hostname=hostname,
                        update_status=incoming_update_status,
                        release_channel=(
                            incoming_agent_release_channel
                            or _clean_device_str(merged_summary.get("agent_release_channel"))
                            or ""
                        ),
                        branch=(
                            incoming_agent_branch
                            or _clean_device_str(merged_summary.get("agent_branch"))
                            or ""
                        ),
                        installed_build_id=(
                            agent_hash
                            or _clean_device_str(merged_summary.get("agent_build_id"))
                            or _clean_device_str(merged_summary.get("agent_hash"))
                            or ""
                        ),
                    )
                except Exception:
                    self.logger.debug("Failed to reconcile agent maintenance operation", exc_info=True)
            if services_changed:
                self._emit_device_services_changed(hostname, change="updated")
            if software_changed:
                self._emit_device_inventory_changed(hostname, change="software_updated")
            self._remember_details_ingest(details_cache_key, payload_hash, time.monotonic())
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
            details = payload.get("details") if isinstance(payload.get("details"), dict) else {}
            software_rows = details.get("software") if isinstance(details.get("software"), list) else []
            if software_rows:
                details["software"] = apply_engine_global_icon_overrides(self._db_conn, software_rows)
                payload["software"] = details["software"]
            payload = self._attach_agent_version_status(payload)
            return payload, 200
        except Exception as exc:
            self.logger.debug("Failed to load device details", exc_info=True)
            return {"error": str(exc)}, 500

    def set_agent_release_channel_override(
        self,
        guid: str,
        channel_override: Any,
        branch_override: Any = None,
    ) -> Tuple[Dict[str, Any], int]:
        normalized_guid = normalize_guid(guid) if _clean_device_str(guid) else ""
        if not normalized_guid:
            return {"error": "invalid_guid"}, 400

        raw_override = (_clean_device_str(channel_override) or "").lower()
        if raw_override in {"release", "releases"}:
            cleaned_override = "stable"
        elif raw_override in {"source", "branch", "repo", "repository"}:
            cleaned_override = "unstable"
        else:
            cleaned_override = raw_override
        if cleaned_override not in {"", "stable", "unstable"}:
            return {"error": "invalid_channel"}, 400

        branch_supplied = branch_override is not None
        supplied_branch = _normalize_agent_branch(branch_override) if branch_supplied else None
        if branch_supplied and not supplied_branch:
            return {"error": "invalid_branch"}, 400

        target_branch = ""
        target_build_id = ""
        target_published_at = ""
        if supplied_branch:
            cleaned_override = "unstable"
            effective_channel = "unstable"
            release_channel = "source" if raw_override in {"source", "branch", "repo", "repository"} else "unstable"
            target_branch = supplied_branch
        else:
            effective_channel, target_build_id, target_published_at = self._resolve_agent_target(cleaned_override)
            if self.agent_release_manager is not None:
                try:
                    target = self.agent_release_manager.target_for_override(cleaned_override)
                    if isinstance(target, dict):
                        target_branch = _normalize_agent_branch(target.get("branch")) or ""
                except Exception:
                    target_branch = ""
            release_channel = "unstable" if (_clean_device_str(effective_channel) or "").lower() == "unstable" else "stable"

        stored_override = cleaned_override or None

        conn = self._db_conn()
        hostname = ""
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE devices
                   SET agent_release_channel_override = ?,
                       agent_release_channel = ?,
                       agent_branch = ?
                 WHERE UPPER(guid) = ?
                """,
                (stored_override, release_channel, target_branch, normalized_guid),
            )
            if cur.rowcount == 0:
                conn.rollback()
                return {"error": "not found"}, 404
            cur.execute(
                "SELECT hostname FROM devices WHERE UPPER(guid) = ? LIMIT 1",
                (normalized_guid,),
            )
            row = cur.fetchone()
            hostname = _clean_device_str(row[0]) if row else ""
            conn.commit()
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to update agent release channel override", exc_info=True)
            return {"error": str(exc)}, 500
        finally:
            conn.close()

        self._emit_agent_release_channel_changed(
            hostname,
            {
                "hostname": hostname,
                "guid": normalized_guid,
                "effective_channel": effective_channel,
                "target_channel": effective_channel,
                "release_channel": release_channel,
                "branch": target_branch,
                "target_build_id": target_build_id or "",
            },
        )
        return {
            "status": "ok",
            "guid": normalized_guid,
            "hostname": hostname,
            "agent_release_channel_override": stored_override,
            "agent_release_channel_effective": effective_channel,
            "agent_release_channel": release_channel,
            "agent_branch": target_branch,
            "agent_target_build_id": target_build_id or "",
            "agent_target_published_at": target_published_at or "",
        }, 200

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

def register_management(app, adapters: "EngineServiceAdapters") -> None:
    """Register device management endpoints onto the Flask app."""

    service = DeviceManagementService(app, adapters)
    blueprint = Blueprint("devices", __name__)

    @blueprint.route("/api/agent/details", methods=["POST"])
    @require_device_auth(adapters.device_auth_manager)
    def _agent_details():
        payload, status = service.save_agent_details()
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

    @blueprint.route("/api/devices/<guid>/agent-release-channel", methods=["PUT"])
    def _set_device_agent_release_channel(guid: str):
        requirement = service._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        payload, status = service.set_agent_release_channel_override(guid, body.get("channel"), body.get("branch"))
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

    app.register_blueprint(blueprint)
