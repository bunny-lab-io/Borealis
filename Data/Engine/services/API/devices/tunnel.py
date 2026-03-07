# ======================================================
# Data\Engine\services\API\devices\tunnel.py
# Description: WireGuard VPN tunnel API (connect/status).
#
# API Endpoints (if applicable):
# - POST /api/tunnel/connect (Token Authenticated) - Issues VPN session material for an agent.
# - GET /api/tunnel/status (Token Authenticated) - Returns VPN status for an agent.
# - GET /api/tunnel/active (Token Authenticated) - Lists active VPN tunnel sessions.
# ======================================================

"""WireGuard VPN tunnel API (Engine side)."""
from __future__ import annotations

import os
from urllib.parse import urlsplit
from pathlib import Path
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....auth.guid_utils import normalize_guid
from ...auth.secrets import require_app_secret
from ...VPN import WireGuardServerConfig, WireGuardServerManager, VpnTunnelService

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


def _get_tunnel_service(adapters: "EngineServiceAdapters") -> VpnTunnelService:
    service = getattr(adapters.context, "vpn_tunnel_service", None) or getattr(adapters, "_vpn_tunnel_service", None)
    if service is None:
        manager = getattr(adapters.context, "wireguard_server_manager", None)
        if manager is None:
            try:
                manager = WireGuardServerManager(
                    WireGuardServerConfig(
                        port=adapters.context.wireguard_port,
                        engine_virtual_ip=adapters.context.wireguard_engine_virtual_ip,
                        peer_network=adapters.context.wireguard_peer_network,
                        private_key_path=Path(adapters.context.wireguard_server_private_key_path),
                        public_key_path=Path(adapters.context.wireguard_server_public_key_path),
                        acl_allowlist_ports=tuple(adapters.context.wireguard_port_allowlist),
                        log_path=Path(adapters.context.vpn_tunnel_log_path),
                    )
                )
                adapters.context.wireguard_server_manager = manager
            except Exception as exc:
                adapters.context.logger.error("Failed to initialize WireGuard server manager on demand.", exc_info=True)
                raise RuntimeError("wireguard_manager_unavailable") from exc
        service = VpnTunnelService(
            context=adapters.context,
            wireguard_manager=manager,
            db_conn_factory=adapters.db_conn_factory,
            socketio=getattr(adapters.context, "socketio", None),
            service_log=adapters.service_log,
            signer=getattr(adapters, "script_signer", None),
        )
        setattr(adapters, "_vpn_tunnel_service", service)
        setattr(adapters.context, "vpn_tunnel_service", service)
    return service


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


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


def _down_status_payload(agent_id: str, *, agent_socket: bool) -> Dict[str, Any]:
    return {
        "status": "down",
        "agent_id": agent_id,
        "agent_socket": agent_socket,
        "listener_healthy": False,
        "recovery_in_progress": False,
        "last_recovery_attempt_at": None,
        "last_recovery_attempt_at_iso": "",
    }


def _guid_candidate(value: Any) -> str:
    text = _normalize_text(value)
    if not text:
        return ""
    normalized = normalize_guid(text)
    if not normalized:
        return ""
    return normalized if normalized == text.strip().strip("{}").upper() else ""


def _resolve_requested_agent_id(adapters: "EngineServiceAdapters", requested_agent_id: Any) -> str:
    agent_id = _normalize_text(requested_agent_id)
    if not agent_id:
        return ""

    guid = _guid_candidate(agent_id)
    if not guid:
        return agent_id

    conn_factory = getattr(adapters, "db_conn_factory", None)
    if not callable(conn_factory):
        return agent_id

    conn = None
    try:
        conn = conn_factory()
        cur = conn.cursor()
        cur.execute(
            "SELECT agent_id FROM devices WHERE UPPER(guid) = ? ORDER BY last_seen DESC LIMIT 1",
            (guid,),
        )
        row = cur.fetchone()
        resolved = _normalize_text(row[0] if row else "")
        return resolved or agent_id
    except Exception:
        return agent_id
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def register_tunnel(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("vpn_tunnel", __name__)
    logger = adapters.context.logger.getChild("vpn_tunnel.api")
    service_log = adapters.service_log

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("VPN_Tunnel/tunnel", message, level=level)
        except Exception:
            logger.debug("vpn_tunnel service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    @blueprint.route("/api/tunnel/connect", methods=["POST"])
    def connect_tunnel():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        requested_agent_id = _normalize_text(body.get("agent_id"))
        if not requested_agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)

        try:
            tunnel_service = _get_tunnel_service(adapters)
            endpoint_host = _infer_endpoint_host(request)
            _service_log_event(
                "vpn_api_connect_request requested_agent_id={0} resolved_agent_id={1} operator={2} endpoint_host={3} remote={4}".format(
                    requested_agent_id,
                    agent_id,
                    operator_id or "-",
                    endpoint_host or "-",
                    _request_remote() or "-",
                )
            )
            payload = tunnel_service.connect(
                agent_id=agent_id,
                operator_id=operator_id,
                endpoint_host=endpoint_host,
            )
        except Exception as exc:
            _service_log_event(
                "vpn_api_connect_failed agent_id={0} operator={1} error={2}".format(
                    agent_id,
                    operator_id or "-",
                    str(exc),
                ),
                level="ERROR",
            )
            logger.warning("vpn connect failed for agent_id=%s: %s", agent_id, exc)
            return jsonify({"error": "connect_failed", "detail": str(exc)}), 500

        _service_log_event(
            "vpn_api_connect_response agent_id={0} tunnel_id={1} status=ok".format(
                payload.get("agent_id", agent_id),
                payload.get("tunnel_id", "-"),
            )
        )
        return jsonify(payload), 200

    @blueprint.route("/api/tunnel/status", methods=["GET"])
    def tunnel_status():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        requested_agent_id = _normalize_text(request.args.get("agent_id") or "")
        if not requested_agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)

        tunnel_service = _get_tunnel_service(adapters)
        payload = tunnel_service.status(agent_id)
        agent_socket = False
        registry = getattr(adapters.context, "agent_socket_registry", None)
        if registry and hasattr(registry, "is_registered"):
            try:
                agent_socket = bool(registry.is_registered(agent_id))
            except Exception:
                agent_socket = False
        bump = _normalize_text(request.args.get("bump") or "")
        _service_log_event(
            "vpn_api_status_request requested_agent_id={0} resolved_agent_id={1} bump={2} remote={3}".format(
                requested_agent_id,
                agent_id,
                "true" if bump else "false",
                _request_remote() or "-",
            )
        )
        if not payload:
            _service_log_event(
                "vpn_api_status_response agent_id={0} status=down".format(agent_id)
            )
            return jsonify(_down_status_payload(agent_id, agent_socket=agent_socket)), 200
        payload["status"] = "up"
        payload["agent_socket"] = agent_socket
        if bump:
            tunnel_service.bump_activity(agent_id)
        _service_log_event(
            "vpn_api_status_response agent_id={0} status=up tunnel_id={1}".format(
                agent_id,
                payload.get("tunnel_id", "-"),
            )
        )
        return jsonify(payload), 200

    @blueprint.route("/api/tunnel/connect/status", methods=["GET"])
    def tunnel_connect_status():
        return tunnel_status()

    @blueprint.route("/api/tunnel/active", methods=["GET"])
    def tunnel_active():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        tunnel_service = _get_tunnel_service(adapters)
        sessions = list(tunnel_service.list_sessions())
        registry = getattr(adapters.context, "agent_socket_registry", None)
        enriched_sessions = []
        for session_payload in sessions:
            payload = dict(session_payload or {})
            agent_id = _normalize_text(payload.get("agent_id"))
            agent_socket = False
            if agent_id and registry and hasattr(registry, "is_registered"):
                try:
                    agent_socket = bool(registry.is_registered(agent_id))
                except Exception:
                    agent_socket = False
            payload["agent_socket"] = agent_socket
            enriched_sessions.append(payload)
        sessions = enriched_sessions
        _service_log_event(
            "vpn_api_active_response count={0} remote={1}".format(
                len(sessions),
                _request_remote() or "-",
            )
        )
        return jsonify({"count": len(sessions), "tunnels": sessions}), 200

    app.register_blueprint(blueprint)
