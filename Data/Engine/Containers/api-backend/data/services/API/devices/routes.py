# ======================================================
# Data\Engine\services\API\devices\routes.py
# Description: Agent heartbeat and script polling endpoints aligned with device management APIs.
#
# API Endpoints (if applicable):
# - POST /api/agent/heartbeat (Device Authenticated) - Updates device last-seen metadata and inventory snapshots.
# - POST /api/agent/status (Device Authenticated) - Updates startup status timeline telemetry.
# - POST /api/agent/script/request (Device Authenticated) - Provides script execution payloads or idle signals to agents.
# - GET /api/agent/software-management/overrides (Device Authenticated) - Returns file-backed software-management override hints for agent-side inventory/icon collection.
# - POST /api/agent/vpn/ensure (Device Authenticated) - Ensures persistent WireGuard tunnel material.
# - POST /api/agent/vpn/ready (Device Authenticated) - Records agent-side WireGuard readiness for the active tunnel.
# - POST /api/agent/vnc/ensure (Device Authenticated) - Ensures VNC readiness and reports listener/session metadata.
# ======================================================

"""Device-affiliated agent endpoints for the Borealis Engine runtime."""
from __future__ import annotations

import json
from Data.Engine.db import dbapi as sqlite3
import threading
import time
from typing import TYPE_CHECKING, Any, Dict, Optional

from flask import Blueprint, jsonify, request, g, send_file

from ....auth.device_auth import AGENT_CONTEXT_HEADER, require_device_auth
from ....auth.guid_utils import normalize_guid
from ....public_endpoints import wireguard_endpoint
from ...RemoteDesktop.vnc_sessions import ensure_vnc_collaboration_manager
from .software_icons import load_software_icon_overrides
from .agent_role_health import merge_agent_role_health, normalize_agent_role_health, serialize_agent_role_health
from .tunnel import _get_tunnel_service, _guid_from_agent_id, _load_device_agent_binding, _resolve_requested_agent_id

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


def _json_list(value: Any) -> list[Any]:
    if isinstance(value, list):
        return value
    if not value:
        return []
    try:
        parsed = json.loads(str(value))
    except Exception:
        return []
    return parsed if isinstance(parsed, list) else []


def _json_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return value
    if not value:
        return {}
    try:
        parsed = json.loads(str(value))
    except Exception:
        return {}
    return parsed if isinstance(parsed, dict) else {}


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


def _infer_endpoint_host(adapters: "EngineServiceAdapters", req) -> str:
    host, _port = wireguard_endpoint(adapters.context, req=req)
    return host


def _text_bool(value: Any) -> bool:
    normalized = str(value or "").strip().lower()
    return normalized in {"1", "true", "yes", "y", "on", "healthy", "ready", "listening"}


def register_agents(app, adapters: "EngineServiceAdapters") -> None:
    """Register agent heartbeat and script polling routes."""

    blueprint = Blueprint("agents", __name__)
    auth_manager = adapters.device_auth_manager
    log = adapters.service_log
    db_conn_factory = adapters.db_conn_factory
    script_signer = adapters.script_signer
    release_manager = getattr(adapters, "agent_release_manager", None)
    status_cache_lock = threading.Lock()
    status_cache: Dict[str, Dict[str, Any]] = {}
    script_signing_key_cache: Dict[str, Any] = {"value": "", "expires_at": 0.0}

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

    def _script_signing_key() -> str:
        if script_signer is None:
            return ""
        now_mono = time.monotonic()
        cached_value = str(script_signing_key_cache.get("value") or "")
        try:
            expires_at = float(script_signing_key_cache.get("expires_at") or 0.0)
        except Exception:
            expires_at = 0.0
        if cached_value and now_mono < expires_at:
            return cached_value
        try:
            signing_key = script_signer.public_base64_spki()
        except Exception:
            signing_key = ""
        script_signing_key_cache["value"] = signing_key
        script_signing_key_cache["expires_at"] = now_mono + 300.0
        return signing_key

    def _vnc_trace(step: str, context_hint: Optional[str], *, level: str = "INFO", **fields: Any) -> None:
        parts = [f"vnc_trace step={str(step or '-').strip() or '-'}"]
        for key, value in fields.items():
            if isinstance(value, bool):
                normalized = "true" if value else "false"
            elif value is None:
                normalized = "-"
            else:
                normalized = str(value).strip() or "-"
            normalized = normalized.replace(" ", "_")
            parts.append(f"{key}={normalized}")
        log("VNC", " ".join(parts), context_hint, level=level)

    def _context_hint(ctx: Optional[Any] = None) -> Optional[str]:
        if ctx is not None and getattr(ctx, "service_mode", None):
            return _canonical_context(getattr(ctx, "service_mode", None))
        return _canonical_context(request.headers.get(AGENT_CONTEXT_HEADER))

    def _auth_context() -> Any:
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            log("agents", f"device auth context missing for {request.path}", _context_hint())
        return ctx

    def _repair_agent_id_binding(guid: str, agent_id: str) -> None:
        normalized = normalize_guid(guid)
        repaired_agent_id = str(agent_id or "").strip()
        if not normalized or not repaired_agent_id:
            return
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET agent_id = ? WHERE UPPER(guid) = ?",
                (repaired_agent_id, normalized),
            )
            conn.commit()
        except Exception:
            log(
                "VPN_Tunnel/tunnel",
                f"vpn_agent_id_repair_failed guid={normalized}",
                _context_hint(),
                level="ERROR",
            )
        finally:
            conn.close()

    def _resolve_agent_id_for_guid(guid: str, requested_agent_id: str = "") -> str:
        normalized = normalize_guid(guid)
        if not normalized:
            return ""
        binding = _load_device_agent_binding(adapters, guid=normalized)
        if not any(binding.values()):
            return ""
        stored_agent_id = str(binding.get("agent_id") or "").strip()
        requested_agent_value = str(requested_agent_id or "").strip()
        if requested_agent_value and _guid_from_agent_id(requested_agent_value) == normalized:
            if requested_agent_value != stored_agent_id:
                _repair_agent_id_binding(normalized, requested_agent_value)
            return requested_agent_value
        resolved_agent_id = _resolve_requested_agent_id(adapters, normalized, expected_guid=normalized)
        if resolved_agent_id and resolved_agent_id != stored_agent_id:
            _repair_agent_id_binding(normalized, resolved_agent_id)
        return resolved_agent_id

    def _load_agent_vnc_health(agent_id: str) -> Dict[str, Any]:
        normalized_agent_id = str(agent_id or "").strip()
        default_payload = {
            "status": "unknown",
            "detail": "",
            "ready": False,
            "service_state": "",
            "listener_state": "",
            "last_ready_at": 0,
            "details": {},
        }
        if not normalized_agent_id:
            return dict(default_payload)
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT agent_role_health FROM devices WHERE agent_id=? ORDER BY last_seen DESC LIMIT 1",
                (normalized_agent_id,),
            )
            row = cur.fetchone()
            if not row or row[0] in (None, ""):
                return dict(default_payload)
            normalized = normalize_agent_role_health(row[0], mark_stale=True, now_ts=int(time.time()))
            for role in normalized.get("roles") or []:
                if str(role.get("role_name") or "").strip().lower() != "vnc":
                    continue
                details = role.get("details") if isinstance(role.get("details"), dict) else {}
                service_state = str(
                    details.get("service_state")
                    or details.get("running_status")
                    or ""
                ).strip()
                listener_state = str(
                    details.get("listener_state")
                    or ("listening" if _text_bool(details.get("listener_ready")) else "")
                ).strip()
                last_ready_at = 0
                try:
                    last_ready_at = int(float(details.get("last_ready_at") or 0))
                except Exception:
                    last_ready_at = 0
                ready = _text_bool(details.get("ready")) or (
                    str(role.get("status_code") or "").strip().lower() == "healthy"
                    and _text_bool(details.get("listener_ready") or listener_state)
                )
                return {
                    "status": str(role.get("status_code") or role.get("status") or "unknown").strip().lower() or "unknown",
                    "detail": str(role.get("detail") or "").strip(),
                    "ready": bool(ready),
                    "service_state": service_state,
                    "listener_state": listener_state,
                    "last_ready_at": last_ready_at,
                    "display_topology": _json_list(details.get("display_topology_json")),
                    "display_virtual_bounds": _json_dict(details.get("display_virtual_bounds_json")),
                    "details": details,
                }
        except Exception:
            log("VNC", f"vnc_agent_health_load_failed agent_id={normalized_agent_id}", _context_hint())
        finally:
            conn.close()
        return dict(default_payload)

    def _active_vnc_session_payload(agent_id: str) -> Optional[Dict[str, Any]]:
        manager = ensure_vnc_collaboration_manager(adapters.context)
        session = manager.get_session_for_agent(agent_id)
        if session is None:
            return None
        return {
            "session_id": session.session_id,
            "session_state": session.state,
            "controller_operator_id": session.controller_operator_id or "",
            "credential_revision": int(session.credential_revision or 0),
            "remove_wallpaper": bool(session.remove_wallpaper),
            "session": manager.session_snapshot(session),
        }

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
        incoming_role_health = payload.get("agent_role_health")
        incoming_service_mode = (
            payload.get("service_mode")
            or metrics.get("service_mode")
            or getattr(ctx, "service_mode", None)
        )
        if metrics.get("last_user"):
            updates["last_user"] = str(metrics["last_user"])
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
        try:
            cur = conn.cursor()

            def _apply_updates() -> int:
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

        return jsonify(
            {
                "status": "ok",
                "poll_after_ms": 15000,
                "site_id": site_id,
                "site_name": site_name,
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
            "role_id": "startup:system_heartbeat",
            "role_name": "system_heartbeat",
            "role_label": "Startup Timeline",
            "context": "startup",
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

    @blueprint.route("/api/agent/script/request", methods=["POST"])
    @require_device_auth(auth_manager)
    def script_request():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        signing_key = _script_signing_key()

        if ctx.status != "active":
            return jsonify(
                {
                    "status": "quarantined",
                    "poll_after_ms": 60000,
                    "sig_alg": "ed25519",
                    "signing_key": signing_key,
                }
            )

        return jsonify(
            {
                "status": "idle",
                "poll_after_ms": 30000,
                "sig_alg": "ed25519",
                "signing_key": signing_key,
            }
        )

    @blueprint.route("/api/agent/software-management/overrides", methods=["GET"])
    @require_device_auth(auth_manager)
    def software_management_overrides():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        return jsonify(
            {
                "windows_icon_overrides": load_software_icon_overrides(),
            }
        )

    @blueprint.route("/api/agent/update/manifest", methods=["GET"])
    @require_device_auth(auth_manager)
    def agent_update_manifest():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        if release_manager is None:
            return jsonify({"error": "release_channels_unavailable"}), 503
        installed_build_id = (
            request.args.get("installed_build_id")
            or request.args.get("current_build_id")
            or request.args.get("agent_build_id")
            or ""
        )
        try:
            payload = release_manager.manifest_for_device(
                guid=getattr(ctx, "guid", "") or "",
                hostname=request.args.get("hostname") or "",
                installed_build_id=installed_build_id,
            )
        except Exception as exc:
            log("agents", f"agent update manifest failed guid={getattr(ctx, 'guid', '')} err={exc}", _context_hint(ctx), level="ERROR")
            return jsonify({"error": "manifest_unavailable", "message": str(exc)}), 503
        return jsonify(payload), 200

    @blueprint.route("/api/agent/update/download/<artifact_id>", methods=["GET"])
    @require_device_auth(auth_manager)
    def agent_update_download(artifact_id: str):
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        if release_manager is None:
            return jsonify({"error": "release_channels_unavailable"}), 503
        artifact_path = release_manager.artifact_path_for_id(artifact_id)
        if artifact_path is None:
            return jsonify({"error": "artifact_not_found"}), 404
        return send_file(
            artifact_path,
            mimetype="application/zip",
            as_attachment=True,
            download_name=artifact_path.name,
            conditional=True,
        )

    @blueprint.route("/api/agent/vpn/ensure", methods=["POST"])
    @require_device_auth(auth_manager)
    def vpn_ensure():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        body = request.get_json(silent=True) or {}
        requested_agent = str(body.get("agent_id") or "").strip()
        guid = normalize_guid(ctx.guid)

        resolved_agent = _resolve_agent_id_for_guid(guid, requested_agent)

        if not resolved_agent:
            log("VPN_Tunnel/tunnel", f"vpn_agent_ensure_missing_agent guid={guid}", _context_hint(ctx), level="ERROR")
            return jsonify({"error": "agent_id_missing"}), 404

        if requested_agent and requested_agent != resolved_agent:
            log(
                "VPN_Tunnel/tunnel",
                "vpn_agent_ensure_agent_mismatch requested={0} resolved={1}".format(
                    requested_agent, resolved_agent
                ),
                _context_hint(ctx),
                level="WARNING",
            )

        try:
            tunnel_service = _get_tunnel_service(adapters)
            endpoint_host = _infer_endpoint_host(adapters, request)
            log(
                "VPN_Tunnel/tunnel",
                "vpn_agent_ensure_request agent_id={0} endpoint_host={1}".format(
                    resolved_agent, endpoint_host or "-"
                ),
                _context_hint(ctx),
            )
            payload = tunnel_service.connect(
                agent_id=resolved_agent,
                operator_id=None,
                endpoint_host=endpoint_host,
                mark_activity=False,
            )
        except Exception as exc:
            log(
                "VPN_Tunnel/tunnel",
                "vpn_agent_ensure_failed agent_id={0} error={1}".format(resolved_agent, str(exc)),
                _context_hint(ctx),
                level="ERROR",
            )
            return jsonify({"error": "tunnel_start_failed", "detail": str(exc)}), 500

        vnc_port = int(getattr(adapters.context, "vnc_port", 5900))
        active_vnc_session = _active_vnc_session_payload(resolved_agent)
        response_payload = dict(payload)
        response_payload["vnc_port"] = vnc_port
        response_payload["vnc_password"] = ""
        response_payload["view_only_password"] = ""
        response_payload["vnc_session_id"] = (
            str(active_vnc_session.get("session_id") or "").strip()
            if isinstance(active_vnc_session, dict)
            else ""
        )
        response_payload["vnc_credential_revision"] = (
            int(active_vnc_session.get("credential_revision") or 0)
            if isinstance(active_vnc_session, dict)
            else 0
        )

        log(
            "VPN_Tunnel/tunnel",
            "vpn_agent_ensure_response agent_id={0} tunnel_id={1}".format(
                response_payload.get("agent_id", resolved_agent),
                response_payload.get("tunnel_id", "-"),
            ),
            _context_hint(ctx),
        )
        return jsonify(response_payload), 200

    @blueprint.route("/api/agent/vpn/ready", methods=["POST"])
    @require_device_auth(auth_manager)
    def vpn_ready():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        body = request.get_json(silent=True) or {}
        requested_agent = str(body.get("agent_id") or "").strip()
        guid = normalize_guid(ctx.guid)
        resolved_agent = _resolve_agent_id_for_guid(guid, requested_agent)
        tunnel_id = str(body.get("tunnel_id") or "").strip()
        if not resolved_agent:
            log("VPN_Tunnel/tunnel", f"vpn_agent_ready_missing_agent guid={guid}", _context_hint(ctx), level="ERROR")
            return jsonify({"error": "agent_id_missing"}), 404
        if not tunnel_id:
            return jsonify({"error": "tunnel_id_required"}), 400
        if requested_agent and requested_agent != resolved_agent:
            log(
                "VPN_Tunnel/tunnel",
                "vpn_agent_ready_agent_mismatch requested={0} resolved={1}".format(
                    requested_agent, resolved_agent
                ),
                _context_hint(ctx),
                level="WARNING",
            )

        try:
            tunnel_service = _get_tunnel_service(adapters)
            payload = tunnel_service.record_agent_ready(
                resolved_agent,
                tunnel_id=tunnel_id,
                allowed_ports=body.get("allowed_ports") or (),
                reason=body.get("reason"),
                service_state=body.get("service_state"),
                virtual_ip=body.get("virtual_ip"),
            )
        except Exception as exc:
            log(
                "VPN_Tunnel/tunnel",
                "vpn_agent_ready_failed agent_id={0} tunnel_id={1} error={2}".format(
                    resolved_agent,
                    tunnel_id,
                    str(exc),
                ),
                _context_hint(ctx),
                level="ERROR",
            )
            return jsonify({"error": "ready_record_failed", "detail": str(exc)}), 500
        if not payload:
            return jsonify({"error": "tunnel_not_found"}), 404
        log(
            "VPN_Tunnel/tunnel",
            "vpn_agent_ready_response agent_id={0} tunnel_id={1} dispatch_ready={2}".format(
                resolved_agent,
                tunnel_id,
                str(bool(payload.get("dispatch_ready"))).lower() if isinstance(payload, dict) else "false",
            ),
            _context_hint(ctx),
        )
        return jsonify(dict(payload)), 200

    @blueprint.route("/api/agent/vnc/ensure", methods=["POST"])
    @require_device_auth(auth_manager)
    def vnc_ensure():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        body = request.get_json(silent=True) or {}
        requested_agent = (body.get("agent_id") or "").strip()
        guid = normalize_guid(ctx.guid)
        context_hint = _context_hint(ctx)
        advertised_password = str(
            body.get("controller_password")
            or body.get("vnc_password")
            or ""
        ).strip()
        advertised_revision = body.get("credential_revision")
        if advertised_revision in (None, ""):
            advertised_revision = body.get("vnc_credential_revision")
        advertised_display_topology = body.get("display_topology")
        if advertised_display_topology in (None, ""):
            advertised_display_topology = _json_list(body.get("display_topology_json"))

        resolved_agent = _resolve_agent_id_for_guid(guid, requested_agent)
        _vnc_trace(
            "R01",
            context_hint,
            guid=guid or "-",
            requested_agent=requested_agent or "-",
            resolved_agent=resolved_agent or "-",
            reason=body.get("reason") or "-",
            advertised_password=bool(advertised_password),
            advertised_revision=advertised_revision or 0,
            display_count=len(advertised_display_topology or []),
        )
        if not resolved_agent:
            log("VNC", f"vnc_agent_ensure_missing_agent guid={guid}", context_hint, level="ERROR")
            return jsonify({"error": "agent_id_missing"}), 404

        if requested_agent and requested_agent != resolved_agent:
            log(
                "VNC",
                "vnc_agent_ensure_agent_mismatch requested={0} resolved={1}".format(
                    requested_agent, resolved_agent
                ),
                context_hint,
                level="WARNING",
            )

        try:
            tunnel_service = _get_tunnel_service(adapters)
            session_payload = None
            if hasattr(tunnel_service, "session_payload"):
                session_payload = tunnel_service.session_payload(resolved_agent, include_token=False)
            if not session_payload:
                session_payload = tunnel_service.connect(
                    agent_id=resolved_agent,
                    operator_id=None,
                    endpoint_host=_infer_endpoint_host(adapters, request),
                    mark_activity=False,
                )
        except Exception as exc:
            log(
                "VNC",
                "vnc_agent_ensure_tunnel_failed agent_id={0} error={1}".format(resolved_agent, str(exc)),
                context_hint,
                level="ERROR",
            )
            _vnc_trace(
                "R02F",
                context_hint,
                level="ERROR",
                resolved_agent=resolved_agent,
                result="tunnel_down",
                error=str(exc),
            )
            return jsonify({"error": "tunnel_down", "detail": str(exc)}), 409

        vnc_port = int(getattr(adapters.context, "vnc_port", 5900))

        allowed_ips = session_payload.get("allowed_ips") or session_payload.get("engine_virtual_ip")
        if isinstance(allowed_ips, list):
            allowed_ips = allowed_ips[0] if allowed_ips else ""
        allowed_ips = str(allowed_ips or "").strip()
        _vnc_trace(
            "R02",
            context_hint,
            resolved_agent=resolved_agent,
            tunnel_id=session_payload.get("tunnel_id") or "-",
            engine_virtual_ip=session_payload.get("engine_virtual_ip") or "-",
            allowed_ips=allowed_ips or "-",
            vnc_port=vnc_port,
        )

        if advertised_password:
            _vnc_trace(
                "R03",
                context_hint,
                resolved_agent=resolved_agent,
                credential_revision=advertised_revision or 0,
                display_count=len(advertised_display_topology or []),
                credential_stored=False,
            )

        active_session = _active_vnc_session_payload(resolved_agent)
        role_health = _load_agent_vnc_health(resolved_agent)
        if isinstance(active_session, dict):
            active_session_payload = dict(active_session)
        else:
            active_session_payload = {}
        display_topology = []
        display_virtual_bounds: Dict[str, Any] = {}
        if not display_topology:
            display_topology = list(role_health.get("display_topology") or [])
        if not display_virtual_bounds:
            display_virtual_bounds = dict(role_health.get("display_virtual_bounds") or {})
        _vnc_trace(
            "R04",
            context_hint,
            resolved_agent=resolved_agent,
            cached_credential=False,
            active_session=bool(active_session_payload),
            ready=bool(role_health.get("ready")),
            service_state=role_health.get("service_state") or "-",
            listener_state=role_health.get("listener_state") or "-",
            display_count=len(display_topology or []),
        )

        log(
            "VNC",
            "vnc_agent_ensure_response agent_id={0} port={1}".format(resolved_agent, vnc_port),
            context_hint,
        )
        _vnc_trace(
            "R05",
            context_hint,
            resolved_agent=resolved_agent,
            session_id=active_session_payload.get("session_id") or "-",
            session_state=active_session_payload.get("session_state") or "-",
            credential_revision=active_session_payload.get("credential_revision") or 0,
            ready=bool(role_health.get("ready")),
        )

        return (
            jsonify(
                {
                    "status": "ok",
                    "agent_id": resolved_agent,
                    "vnc_port": vnc_port,
                    "allowed_ips": allowed_ips,
                    "tunnel_id": session_payload.get("tunnel_id"),
                    "engine_virtual_ip": session_payload.get("engine_virtual_ip"),
                    "ready": bool(role_health.get("ready")),
                    "service_state": str(role_health.get("service_state") or "").strip(),
                    "listener_state": str(role_health.get("listener_state") or "").strip(),
                    "last_ready_at": int(role_health.get("last_ready_at") or 0),
                    "health_status": str(role_health.get("status") or "").strip(),
                    "detail": str(role_health.get("detail") or "").strip(),
                    "session_id": str(active_session_payload.get("session_id") or "").strip(),
                    "session_state": str(active_session_payload.get("session_state") or "").strip(),
                    "controller_operator_id": str(active_session_payload.get("controller_operator_id") or "").strip(),
                    "controller_password": "",
                    "view_only_password": "",
                    "vnc_password": "",
                    "credential_revision": int(active_session_payload.get("credential_revision") or 0),
                    "remove_wallpaper": bool(active_session_payload.get("remove_wallpaper")),
                    "display_topology": display_topology,
                    "display_virtual_bounds": display_virtual_bounds,
                    "session": active_session_payload.get("session") or None,
                }
            ),
            200,
        )

    app.register_blueprint(blueprint)
