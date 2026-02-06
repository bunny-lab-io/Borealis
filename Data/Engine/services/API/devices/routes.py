# ======================================================
# Data\Engine\services\API\devices\routes.py
# Description: Agent heartbeat and script polling endpoints aligned with device management APIs.
#
# API Endpoints (if applicable):
# - POST /api/agent/heartbeat (Device Authenticated) - Updates device last-seen metadata and inventory snapshots.
# - POST /api/agent/script/request (Device Authenticated) - Provides script execution payloads or idle signals to agents.
# - POST /api/agent/vpn/ensure (Device Authenticated) - Ensures persistent WireGuard tunnel material.
# ======================================================

"""Device-affiliated agent endpoints for the Borealis Engine runtime."""
from __future__ import annotations

import json
import sqlite3
import time
from urllib.parse import urlsplit
from typing import TYPE_CHECKING, Any, Dict, Optional

from flask import Blueprint, jsonify, request, g

from ....auth.device_auth import AGENT_CONTEXT_HEADER, require_device_auth
from ....auth.guid_utils import normalize_guid
from .tunnel import _get_tunnel_service

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

        conn = db_conn_factory()
        try:
            cur = conn.cursor()

            def _apply_updates() -> int:
                if not updates:
                    return 0
                columns = ", ".join(f"{col} = ?" for col in updates.keys())
                values = list(updates.values())
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

        conn = db_conn_factory()
        resolved_agent = ""
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT agent_id FROM devices WHERE UPPER(guid) = ? ORDER BY last_seen DESC LIMIT 1",
                (guid,),
            )
            row = cur.fetchone()
            if row and row[0]:
                resolved_agent = str(row[0]).strip()
        finally:
            conn.close()

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

        log(
            "VPN_Tunnel/tunnel",
            "vpn_agent_ensure_response agent_id={0} tunnel_id={1}".format(
                payload.get("agent_id", resolved_agent), payload.get("tunnel_id", "-")
            ),
            _context_hint(ctx),
        )
        return jsonify(payload), 200

    app.register_blueprint(blueprint)
