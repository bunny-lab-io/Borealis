# ======================================================
# Data\Engine\services\API\devices\routes.py
# Description: Agent heartbeat and script polling endpoints aligned with device management APIs.
#
# API Endpoints (if applicable):
# - POST /api/agent/heartbeat (Device Authenticated) - Updates device last-seen metadata and inventory snapshots.
# - POST /api/agent/status (Device Authenticated) - Updates startup status timeline telemetry.
# ======================================================

"""Device-affiliated agent endpoints for the Borealis Engine runtime."""
from __future__ import annotations

import json
from Data.Engine.db import dbapi as sqlite3
import threading
import time
from typing import TYPE_CHECKING, Any, Dict, Optional

from flask import Blueprint, jsonify, request, g

from ....auth.device_auth import AGENT_CONTEXT_HEADER, require_device_auth
from ....auth.guid_utils import normalize_guid
from ...activity_history import update_activity_history_row
from ...metadata_fields import process_agent_metadata_sync
from .agent_role_health import merge_agent_role_health, normalize_agent_role_health, serialize_agent_role_health

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def _canonical_context(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    cleaned = "".join(ch for ch in str(value) if ch.isalnum() or ch in ("_", "-"))
    if not cleaned:
        return None
    return cleaned.upper()


def _json_or_none(value: Any) -> Optional[str]:
    if value is None:
        return None
    try:
        return json.dumps(value)
    except Exception:
        return None


def _normalize_status_code(value: Any) -> str:
    text = str(value or "").strip().lower().replace(" ", "_").replace("-", "_")
    aliases = {
        "ok": "healthy",
        "online": "healthy",
        "complete": "healthy",
        "completed": "healthy",
        "ready": "healthy",
        "starting": "recovering",
        "active": "recovering",
        "pending": "recovering",
        "failed": "unhealthy",
        "error": "unhealthy",
    }
    normalized = aliases.get(text, text)
    return normalized if normalized in {"healthy", "recovering", "unhealthy", "pending", "unknown"} else "recovering"


def _upsert_single_role_health(existing_raw: Any, role: Dict[str, Any]) -> str:
    normalized = normalize_agent_role_health(existing_raw)
    role_id = str(role.get("role_id") or "").strip()
    roles = [
        item
        for item in (normalized.get("roles") or [])
        if str(item.get("role_id") or "").strip() != role_id
    ]
    roles.append(role)
    reported_at = max(
        int(normalized.get("reported_at") or 0),
        int(role.get("last_checked_at") or 0),
        int(time.time()),
    )
    return serialize_agent_role_health({"roles": roles, "reported_at": reported_at})


def _clean_agent_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def register_agents(app, adapters: "EngineServiceAdapters") -> None:
    """Register agent heartbeat and script polling routes."""

    blueprint = Blueprint("agents", __name__)
    auth_manager = adapters.device_auth_manager
    log = adapters.service_log
    db_conn_factory = adapters.db_conn_factory
    status_cache_lock = threading.Lock()
    status_cache: Dict[str, Dict[str, Any]] = {}

    STATUS_DUPLICATE_SUPPRESS_SECONDS = 30.0
    STATUS_EMIT_MIN_INTERVAL_SECONDS = 10.0
    STATUS_CACHE_MAX_ENTRIES = 2048

    def _status_cache_key(guid: str, service_mode: str) -> str:
        return f"{normalize_guid(guid) or str(guid or '').strip()}|{str(service_mode or '').strip().lower()}"

    def _status_signature(payload: Dict[str, Any]) -> str:
        try:
            return json.dumps(payload, ensure_ascii=True, sort_keys=True, separators=(",", ":"))
        except Exception:
            return str(payload)

    def _cached_status_response(cache_key: str, signature: str, now_mono: float) -> Optional[Dict[str, Any]]:
        with status_cache_lock:
            cached = status_cache.get(cache_key)
            if not cached:
                return None
            if cached.get("signature") != signature:
                return None
            seen_at = float(cached.get("seen_at") or 0.0)
            if now_mono - seen_at > STATUS_DUPLICATE_SUPPRESS_SECONDS:
                return None
            cached["seen_at"] = now_mono
            return {
                "status": "ok",
                "poll_after_ms": 30000,
                "site_id": cached.get("site_id"),
                "site_name": cached.get("site_name") or "",
                "coalesced": True,
            }

    def _remember_status_response(
        cache_key: str,
        signature: str,
        now_mono: float,
        *,
        site_id: Optional[int],
        site_name: str,
    ) -> bool:
        emit_allowed = False
        with status_cache_lock:
            cached = status_cache.get(cache_key) or {}
            last_emit_at = float(cached.get("last_emit_at") or 0.0)
            if now_mono - last_emit_at >= STATUS_EMIT_MIN_INTERVAL_SECONDS:
                emit_allowed = True
                cached["last_emit_at"] = now_mono
            cached.update(
                {
                    "signature": signature,
                    "seen_at": now_mono,
                    "site_id": site_id,
                    "site_name": site_name,
                }
            )
            status_cache[cache_key] = cached
            if len(status_cache) > STATUS_CACHE_MAX_ENTRIES:
                stale_keys = sorted(status_cache, key=lambda key: float(status_cache[key].get("seen_at") or 0.0))
                for stale_key in stale_keys[: max(1, len(status_cache) - STATUS_CACHE_MAX_ENTRIES)]:
                    status_cache.pop(stale_key, None)
        return emit_allowed

    def _reconcile_agent_maintenance_operation(
        *,
        hostname: str,
        update_status: Dict[str, Any],
        release_channel: str,
        branch: str,
        installed_build_id: str,
    ) -> None:
        operation_id = _clean_agent_text(update_status.get("operation_id"))
        if not operation_id:
            return
        raw_state = _clean_agent_text(update_status.get("state") or update_status.get("status")).lower()
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
            stderr = _clean_agent_text(update_status.get("last_error")) or "Agent update operation failed."

        conn = db_conn_factory()
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
        except Exception:
            try:
                conn.rollback()
            except Exception:
                pass
            log("agents", f"agent maintenance reconciliation failed hostname={hostname}", "system", level="DEBUG")
        finally:
            conn.close()

    def _context_hint(ctx: Optional[Any] = None) -> Optional[str]:
        if ctx is not None and getattr(ctx, "service_mode", None):
            return _canonical_context(getattr(ctx, "service_mode", None))
        return _canonical_context(request.headers.get(AGENT_CONTEXT_HEADER))

    def _auth_context() -> Any:
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            log("agents", f"device auth context missing for {request.path}", _context_hint())
        return ctx

    @blueprint.route("/api/agent/heartbeat", methods=["POST"])
    @require_device_auth(auth_manager)
    def heartbeat():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        payload = request.get_json(force=True, silent=True) or {}
        context_label = _context_hint(ctx)
        now_ts = int(time.time())
        normalized_guid = normalize_guid(ctx.guid)
        guid_lookup = normalized_guid or str(ctx.guid or "").strip()

        updates: Dict[str, Optional[str]] = {"last_seen": now_ts}

        hostname = payload.get("hostname")
        if isinstance(hostname, str) and hostname.strip():
            updates["hostname"] = hostname.strip()

        inventory = payload.get("inventory") if isinstance(payload.get("inventory"), dict) else {}
        for key in ("memory", "network", "software", "storage", "cpu"):
            if key in inventory and inventory[key] is not None:
                encoded = _json_or_none(inventory[key])
                if encoded is not None:
                    updates[key] = encoded

        metrics = payload.get("metrics") if isinstance(payload.get("metrics"), dict) else {}
        incoming_metadata_fields = payload.get("metadata_fields") if "metadata_fields" in payload else None
        incoming_role_health = payload.get("agent_role_health")
        incoming_service_mode = (
            payload.get("service_mode")
            or metrics.get("service_mode")
            or getattr(ctx, "service_mode", None)
        )
        if metrics.get("last_user"):
            updates["last_user"] = str(metrics["last_user"])
        if metrics.get("domain"):
            updates["domain"] = str(metrics["domain"])
        if metrics.get("operating_system"):
            updates["operating_system"] = str(metrics["operating_system"])
        if metrics.get("last_reboot"):
            updates["last_reboot"] = str(metrics["last_reboot"])
        if metrics.get("uptime") is not None:
            try:
                updates["uptime"] = int(metrics["uptime"])
            except Exception:
                pass
        if metrics.get("cpu_percent") is not None:
            try:
                updates["cpu_percent"] = float(metrics["cpu_percent"])
            except Exception:
                pass
        if metrics.get("memory_percent") is not None:
            try:
                updates["memory_percent"] = float(metrics["memory_percent"])
            except Exception:
                pass

        for field in ("external_ip", "internal_ip", "device_type"):
            if payload.get(field):
                updates[field] = str(payload[field])

        agent_build_id = payload.get("agent_build_id") or payload.get("installed_build_id") or payload.get("agent_hash")
        if isinstance(agent_build_id, str) and agent_build_id.strip():
            updates["agent_hash"] = agent_build_id.strip()
        incoming_update_status = payload.get("agent_update_status") if isinstance(payload.get("agent_update_status"), dict) else {}
        incoming_agent_release_channel = _clean_agent_text(payload.get("agent_release_channel"))
        incoming_agent_branch = _clean_agent_text(payload.get("agent_branch"))
        if incoming_agent_release_channel:
            updates["agent_release_channel"] = incoming_agent_release_channel
        if incoming_agent_branch:
            updates["agent_branch"] = incoming_agent_branch
        if incoming_update_status:
            update_channel = (
                incoming_update_status.get("target_channel")
                or incoming_update_status.get("effective_channel")
                or ""
            )
            update_target_build_id = incoming_update_status.get("target_build_id") or ""
            update_state = incoming_update_status.get("state") or ""
            update_error = incoming_update_status.get("last_error") or ""
            update_source = incoming_update_status.get("last_source") or ""
            updates["agent_update_channel"] = str(update_channel).strip()
            updates["agent_update_target_build_id"] = str(update_target_build_id).strip()
            updates["agent_update_state"] = str(update_state).strip()
            updates["agent_update_error"] = str(update_error).strip()
            updates["agent_update_source"] = str(update_source).strip()

        conn = db_conn_factory()
        metadata_sync_response: Dict[str, Any] = {"updates": {}, "acks": []}
        target_guid_for_sync: Optional[str] = None
        try:
            cur = conn.cursor()

            def _apply_updates() -> int:
                nonlocal target_guid_for_sync
                if not updates and incoming_role_health is None:
                    return 0
                pending_updates = dict(updates)
                normalized_guid = normalize_guid(ctx.guid)
                selected_guid: Optional[str] = None
                if normalized_guid:
                    cur.execute(
                        "SELECT guid FROM devices WHERE UPPER(guid) = ?",
                        (normalized_guid,),
                    )
                    rows = cur.fetchall()
                    for (stored_guid,) in rows or []:
                        if stored_guid == ctx.guid:
                            selected_guid = stored_guid
                            break
                    if not selected_guid and rows:
                        selected_guid = rows[0][0]
                target_guid = selected_guid or ctx.guid
                target_guid_for_sync = target_guid
                if incoming_role_health is not None:
                    existing_role_health = None
                    try:
                        cur.execute(
                            "SELECT agent_role_health FROM devices WHERE guid = ?",
                            (target_guid,),
                        )
                        existing_row = cur.fetchone()
                        if existing_row:
                            existing_role_health = existing_row[0]
                    except Exception:
                        existing_role_health = None
                    pending_updates["agent_role_health"] = serialize_agent_role_health(
                        merge_agent_role_health(
                            existing_role_health,
                            incoming_role_health,
                            incoming_context=incoming_service_mode,
                        )
                    )
                columns = ", ".join(f"{col} = ?" for col in pending_updates.keys())
                values = list(pending_updates.values())
                cur.execute(
                    f"UPDATE devices SET {columns} WHERE guid = ?",
                    values + [target_guid],
                )
                updated = cur.rowcount
                if updated > 0 and normalized_guid and target_guid != normalized_guid:
                    try:
                        cur.execute(
                            "UPDATE devices SET guid = ? WHERE guid = ?",
                            (normalized_guid, target_guid),
                        )
                    except sqlite3.IntegrityError:
                        pass
                return updated

            try:
                rowcount = _apply_updates()
            except sqlite3.IntegrityError as exc:
                if "devices.hostname" in str(exc) and "UNIQUE" in str(exc).upper():
                    existing_guid_for_hostname: Optional[str] = None
                    if "hostname" in updates:
                        try:
                            cur.execute(
                                "SELECT guid FROM devices WHERE hostname = ?",
                                (updates["hostname"],),
                            )
                            row = cur.fetchone()
                            if row and row[0]:
                                existing_guid_for_hostname = normalize_guid(row[0])
                        except Exception:
                            existing_guid_for_hostname = None
                    updates.pop("hostname", None)
                    rowcount = _apply_updates()
                    try:
                        current_guid = normalize_guid(ctx.guid)
                    except Exception:
                        current_guid = ctx.guid
                    if (
                        existing_guid_for_hostname
                        and current_guid
                        and existing_guid_for_hostname != current_guid
                    ):
                        log(
                            "agents",
                            f"heartbeat hostname collision ignored for guid={ctx.guid}",
                            context_label,
                            level="WARNING",
                        )
                else:
                    raise

            if rowcount == 0:
                log("agents", f"heartbeat missing device record guid={ctx.guid}", context_label, level="ERROR")
                return jsonify({"error": "device_not_registered"}), 404
            if incoming_metadata_fields is not None:
                try:
                    metadata_sync_response = process_agent_metadata_sync(
                        conn,
                        target_guid_for_sync or ctx.guid,
                        incoming_metadata_fields,
                        now_ts=now_ts,
                    )
                except Exception:
                    log("agents", f"metadata field sync failed guid={ctx.guid}", context_label, level="WARNING")
            if guid_lookup:
                try:
                    cur.execute(
                        """
                        SELECT ds.site_id, s.name
                          FROM devices AS d
                     LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                     LEFT JOIN sites AS s ON s.id = ds.site_id
                         WHERE UPPER(d.guid) = UPPER(?)
                         LIMIT 1
                        """,
                        (guid_lookup,),
                    )
                    row = cur.fetchone()
                    if row:
                        try:
                            site_id = int(row[0]) if row[0] is not None else None
                        except Exception:
                            site_id = None
                        site_name = str(row[1] or "").strip()
                except Exception:
                    site_id = None
                    site_name = ""
            conn.commit()
        finally:
            conn.close()

        if incoming_update_status:
            try:
                _reconcile_agent_maintenance_operation(
                    hostname=str(updates.get("hostname") or hostname or "").strip(),
                    update_status=incoming_update_status,
                    release_channel=_clean_agent_text(
                        incoming_update_status.get("target_channel")
                        or incoming_update_status.get("effective_channel")
                        or incoming_agent_release_channel
                    ),
                    branch=_clean_agent_text(incoming_update_status.get("target_branch") or incoming_agent_branch),
                    installed_build_id=_clean_agent_text(agent_build_id),
                )
            except Exception:
                log("agents", "agent maintenance heartbeat reconciliation failed", context_label, level="DEBUG")

        return jsonify(
            {
                "status": "ok",
                "poll_after_ms": 15000,
                "site_id": site_id,
                "site_name": site_name,
                "metadata_fields": metadata_sync_response.get("updates") or {},
                "metadata_field_acks": metadata_sync_response.get("acks") or [],
            }
        )

    @blueprint.route("/api/agent/status", methods=["POST"])
    @require_device_auth(auth_manager)
    def agent_status():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        payload = request.get_json(force=True, silent=True) or {}
        context_label = _context_hint(ctx)
        now_ts = int(time.time())
        normalized_guid = normalize_guid(ctx.guid)
        guid_lookup = normalized_guid or str(ctx.guid or "").strip()
        hostname = str(payload.get("hostname") or "").strip()
        service_mode = str(payload.get("service_mode") or getattr(ctx, "service_mode", None) or "system").strip().lower() or "system"
        phase = str(payload.get("phase") or "").strip()
        message = str(payload.get("message") or "").strip()
        status_code = _normalize_status_code(payload.get("status"))
        milestones = payload.get("milestones") if isinstance(payload.get("milestones"), list) else []
        last_error = payload.get("last_error") if isinstance(payload.get("last_error"), (dict, list, str)) else None
        boot_id = str(payload.get("boot_id") or "").strip()
        details = {
            "boot_id": boot_id,
            "phase": phase,
            "message": message,
            "milestones_json": json.dumps(milestones, ensure_ascii=True, sort_keys=True),
            "last_error_json": json.dumps(last_error, ensure_ascii=True, sort_keys=True) if last_error else "",
        }
        status_cache_key = _status_cache_key(guid_lookup, service_mode)
        status_signature = _status_signature(
            {
                "boot_id": boot_id,
                "phase": phase,
                "message": message,
                "status": status_code,
                "milestones": milestones,
                "last_error": last_error,
            }
        )
        now_mono = time.monotonic()
        cached_response = _cached_status_response(status_cache_key, status_signature, now_mono)
        if cached_response is not None:
            return jsonify(cached_response)

        role = {
            "role_id": "system:system_heartbeat",
            "role_name": "system_heartbeat",
            "role_label": "Startup Timeline",
            "context": "system",
            "status_code": status_code,
            "status": status_code,
            "detail": message or phase or "Startup status updated.",
            "details": details,
            "last_checked_at": now_ts,
        }

        site_id = None
        site_name = ""
        emitted_hostname = hostname
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            selected_guid: Optional[str] = None
            existing_role_health = None
            existing_hostname = ""
            if normalized_guid:
                cur.execute(
                    "SELECT guid, hostname, agent_role_health FROM devices WHERE UPPER(guid) = ?",
                    (normalized_guid,),
                )
                rows = cur.fetchall()
                for row in rows or []:
                    if row[0] == ctx.guid:
                        selected_guid = row[0]
                        existing_hostname = str(row[1] or "").strip()
                        existing_role_health = row[2]
                        break
                if not selected_guid and rows:
                    selected_guid = rows[0][0]
                    existing_hostname = str(rows[0][1] or "").strip()
                    existing_role_health = rows[0][2]
            target_guid = selected_guid or ctx.guid
            if not selected_guid:
                log("agents", f"status missing device record guid={ctx.guid}", context_label, level="ERROR")
                return jsonify({"error": "device_not_registered"}), 404
            merged_role_health = _upsert_single_role_health(existing_role_health, role)
            cur.execute(
                "UPDATE devices SET last_seen = ?, agent_role_health = ? WHERE guid = ?",
                (now_ts, merged_role_health, target_guid),
            )
            if cur.rowcount == 0:
                log("agents", f"status update missed device record guid={ctx.guid}", context_label, level="ERROR")
                return jsonify({"error": "device_not_registered"}), 404
            if guid_lookup:
                cur.execute(
                    """
                    SELECT ds.site_id, s.name, d.hostname
                      FROM devices AS d
                 LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 LEFT JOIN sites AS s ON s.id = ds.site_id
                     WHERE UPPER(d.guid) = UPPER(?)
                     LIMIT 1
                    """,
                    (guid_lookup,),
                )
                row = cur.fetchone()
                if row:
                    try:
                        site_id = int(row[0]) if row[0] is not None else None
                    except Exception:
                        site_id = None
                    site_name = str(row[1] or "").strip()
                    emitted_hostname = str(row[2] or existing_hostname or hostname).strip()
            conn.commit()
        finally:
            conn.close()

        emit_allowed = _remember_status_response(
            status_cache_key,
            status_signature,
            time.monotonic(),
            site_id=site_id,
            site_name=site_name,
        )
        socketio = getattr(adapters.context, "socketio", None)
        if socketio is not None and emit_allowed:
            try:
                socketio.emit(
                    "agent_status_changed",
                    {
                        "hostname": emitted_hostname,
                        "guid": guid_lookup,
                        "phase": phase,
                        "status": status_code,
                        "changed_at": now_ts,
                    },
                )
            except Exception:
                log("agents", f"agent_status_changed emit failed guid={guid_lookup}", context_label, level="DEBUG")

        return jsonify(
            {
                "status": "ok",
                "poll_after_ms": 15000,
                "site_id": site_id,
                "site_name": site_name,
            }
        )

    app.register_blueprint(blueprint)
