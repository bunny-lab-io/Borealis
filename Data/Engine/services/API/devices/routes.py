# ======================================================
# Data\Engine\services\API\devices\routes.py
# Description: Agent heartbeat and script polling endpoints aligned with device management APIs.
#
# API Endpoints (if applicable):
# - POST /api/agent/heartbeat (Device Authenticated) - Updates device last-seen metadata and inventory snapshots.
# - POST /api/agent/script/request (Device Authenticated) - Provides script execution payloads or idle signals to agents.
# - POST /api/agent/vpn/ensure (Device Authenticated) - Ensures persistent WireGuard tunnel material.
# - POST /api/agent/vnc/ensure (Device Authenticated) - Ensures VNC credentials for always-on agent VNC.
# ======================================================

"""Device-affiliated agent endpoints for the Borealis Engine runtime."""
from __future__ import annotations

import json
import secrets
from Data.Engine.db import dbapi as sqlite3
import time
from urllib.parse import urlsplit
from typing import TYPE_CHECKING, Any, Dict, Optional

from flask import Blueprint, jsonify, request, g

from ....auth.device_auth import AGENT_CONTEXT_HEADER, require_device_auth
from ....auth.guid_utils import normalize_guid
from .agent_role_health import merge_agent_role_health, serialize_agent_role_health
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


def _infer_endpoint_host(req) -> str:
    forwarded = (req.headers.get("X-Forwarded-Host") or req.headers.get("X-Original-Host") or "").strip()
    host = forwarded.split(",")[0].strip() if forwarded else (req.host or "").strip()
    if not host:
        return ""
    try:
        parsed = urlsplit(f"//{host}")
        if parsed.hostname:
            return parsed.hostname
    except Exception:
        return host
    return host


def register_agents(app, adapters: "EngineServiceAdapters") -> None:
    """Register agent heartbeat and script polling routes."""

    blueprint = Blueprint("agents", __name__)
    auth_manager = adapters.device_auth_manager
    log = adapters.service_log
    db_conn_factory = adapters.db_conn_factory
    script_signer = adapters.script_signer

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

    def _load_vnc_password(agent_id: str) -> Optional[str]:
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT agent_vnc_password FROM devices WHERE agent_id=? ORDER BY last_seen DESC LIMIT 1",
                (agent_id,),
            )
            row = cur.fetchone()
            if row and row[0]:
                return str(row[0]).strip()
        except Exception:
            log("VNC", f"vnc_agent_password_load_failed agent_id={agent_id}", _context_hint())
        finally:
            conn.close()
        return None

    def _store_vnc_password(agent_id: str, password: str) -> None:
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET agent_vnc_password=? WHERE agent_id=?",
                (password, agent_id),
            )
            conn.commit()
        except Exception:
            log("VNC", f"vnc_agent_password_store_failed agent_id={agent_id}", _context_hint())
        finally:
            conn.close()

    def _ensure_vnc_password(agent_id: str) -> str:
        vnc_password = _load_vnc_password(agent_id)
        if not vnc_password:
            vnc_password = secrets.token_hex(4)
            _store_vnc_password(agent_id, vnc_password)
        if len(vnc_password) > 8:
            vnc_password = vnc_password[:8]
            _store_vnc_password(agent_id, vnc_password)
        return vnc_password

    @blueprint.route("/api/agent/heartbeat", methods=["POST"])
    @require_device_auth(auth_manager)
    def heartbeat():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        payload = request.get_json(force=True, silent=True) or {}
        context_label = _context_hint(ctx)
        now_ts = int(time.time())

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
        if metrics.get("uptime") is not None:
            try:
                updates["uptime"] = int(metrics["uptime"])
            except Exception:
                pass

        for field in ("external_ip", "internal_ip", "device_type"):
            if payload.get(field):
                updates[field] = str(payload[field])

        agent_build_id = payload.get("agent_build_id") or payload.get("installed_build_id") or payload.get("agent_hash")
        if isinstance(agent_build_id, str) and agent_build_id.strip():
            updates["agent_hash"] = agent_build_id.strip()

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
            conn.commit()
        finally:
            conn.close()

        return jsonify({"status": "ok", "poll_after_ms": 15000})

    @blueprint.route("/api/agent/script/request", methods=["POST"])
    @require_device_auth(auth_manager)
    def script_request():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        signing_key = ""
        if script_signer is not None:
            try:
                signing_key = script_signer.public_base64_spki()
            except Exception:
                signing_key = ""

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

    @blueprint.route("/api/agent/vpn/ensure", methods=["POST"])
    @require_device_auth(auth_manager)
    def vpn_ensure():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        body = request.get_json(silent=True) or {}
        requested_agent = (body.get("agent_id") or "").strip()
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
            endpoint_host = _infer_endpoint_host(request)
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
        vnc_password = _ensure_vnc_password(resolved_agent)
        response_payload = dict(payload)
        response_payload["vnc_password"] = vnc_password
        response_payload["vnc_port"] = vnc_port

        log(
            "VPN_Tunnel/tunnel",
            "vpn_agent_ensure_response agent_id={0} tunnel_id={1}".format(
                response_payload.get("agent_id", resolved_agent),
                response_payload.get("tunnel_id", "-"),
            ),
            _context_hint(ctx),
        )
        return jsonify(response_payload), 200

    @blueprint.route("/api/agent/vnc/ensure", methods=["POST"])
    @require_device_auth(auth_manager)
    def vnc_ensure():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500

        body = request.get_json(silent=True) or {}
        requested_agent = (body.get("agent_id") or "").strip()
        guid = normalize_guid(ctx.guid)

        resolved_agent = _resolve_agent_id_for_guid(guid, requested_agent)
        if not resolved_agent:
            log("VNC", f"vnc_agent_ensure_missing_agent guid={guid}", _context_hint(ctx), level="ERROR")
            return jsonify({"error": "agent_id_missing"}), 404

        if requested_agent and requested_agent != resolved_agent:
            log(
                "VNC",
                "vnc_agent_ensure_agent_mismatch requested={0} resolved={1}".format(
                    requested_agent, resolved_agent
                ),
                _context_hint(ctx),
                level="WARNING",
            )

        try:
            tunnel_service = _get_tunnel_service(adapters)
            session_payload = tunnel_service.session_payload(resolved_agent, include_token=False)
            if not session_payload:
                session_payload = tunnel_service.connect(
                    agent_id=resolved_agent,
                    operator_id=None,
                    endpoint_host=_infer_endpoint_host(request),
                )
        except Exception as exc:
            log(
                "VNC",
                "vnc_agent_ensure_tunnel_failed agent_id={0} error={1}".format(resolved_agent, str(exc)),
                _context_hint(ctx),
                level="ERROR",
            )
            return jsonify({"error": "tunnel_down", "detail": str(exc)}), 409

        vnc_port = int(getattr(adapters.context, "vnc_port", 5900))

        allowed_ips = session_payload.get("allowed_ips") or session_payload.get("engine_virtual_ip")
        if isinstance(allowed_ips, list):
            allowed_ips = allowed_ips[0] if allowed_ips else ""
        allowed_ips = str(allowed_ips or "").strip()

        vnc_password = _ensure_vnc_password(resolved_agent)

        log(
            "VNC",
            "vnc_agent_ensure_response agent_id={0} port={1}".format(resolved_agent, vnc_port),
            _context_hint(ctx),
        )

        return (
            jsonify(
                {
                    "status": "ok",
                    "agent_id": resolved_agent,
                    "vnc_password": vnc_password,
                    "vnc_port": vnc_port,
                    "allowed_ips": allowed_ips,
                    "tunnel_id": session_payload.get("tunnel_id"),
                    "engine_virtual_ip": session_payload.get("engine_virtual_ip"),
                }
            ),
            200,
        )

    app.register_blueprint(blueprint)
