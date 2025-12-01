# ======================================================
# Data\Engine\services\API\devices\tunnel.py
# Description: Negotiation endpoint for reverse tunnel leases (operator-initiated; dormant until tunnel listener is wired).
#
# API Endpoints (if applicable):
# - POST /api/tunnel/request (Token Authenticated) - Allocates a reverse tunnel lease for the requested agent/protocol.
# ======================================================

"""Reverse tunnel negotiation API (Engine side)."""
from __future__ import annotations

import os
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ...WebSocket.Agent.ReverseTunnel import ReverseTunnelService

if False:  # pragma: no cover - import cycle hint for type checkers
    from .. import EngineServiceAdapters


def _current_user(app) -> Optional[Dict[str, str]]:
    """Resolve operator identity from session or signed token."""

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
        serializer = URLSafeTimedSerializer(app.secret_key or "borealis-dev-secret", salt="borealis-auth")
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


def _get_tunnel_service(adapters: "EngineServiceAdapters") -> ReverseTunnelService:
    service = getattr(adapters.context, "reverse_tunnel_service", None) or getattr(adapters, "_reverse_tunnel_service", None)
    if service is None:
        service = ReverseTunnelService(
            adapters.context,
            signer=getattr(adapters, "script_signer", None),
            db_conn_factory=adapters.db_conn_factory,
            socketio=getattr(adapters.context, "socketio", None),
        )
        service.start()
        setattr(adapters, "_reverse_tunnel_service", service)
        setattr(adapters.context, "reverse_tunnel_service", service)
    return service


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def register_tunnel(app, adapters: "EngineServiceAdapters") -> None:
    """Register reverse tunnel negotiation endpoints."""

    blueprint = Blueprint("reverse_tunnel", __name__)
    service_log = adapters.service_log
    logger = adapters.context.logger.getChild("tunnel.api")

    @blueprint.route("/api/tunnel/request", methods=["POST"])
    def request_tunnel():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        agent_id = _normalize_text(body.get("agent_id"))
        protocol = _normalize_text(body.get("protocol") or "ps").lower() or "ps"
        domain = _normalize_text(body.get("domain") or protocol).lower() or protocol

        if not agent_id:
            return jsonify({"error": "agent_id_required"}), 400

        tunnel_service = _get_tunnel_service(adapters)
        try:
            lease = tunnel_service.request_lease(
                agent_id=agent_id,
                protocol=protocol,
                domain=domain,
                operator_id=operator_id,
            )
        except RuntimeError as exc:
            message = str(exc)
            if message.startswith("domain_limit:"):
                domain_name = message.split(":", 1)[-1] if ":" in message else domain
                return jsonify({"error": "domain_limit", "domain": domain_name}), 409
            if message == "port_pool_exhausted":
                return jsonify({"error": "port_pool_exhausted"}), 503
            logger.warning("tunnel lease request failed for agent_id=%s: %s", agent_id, message)
            return jsonify({"error": "lease_allocation_failed"}), 500

        summary = tunnel_service.lease_summary(lease)
        summary["fixed_port"] = tunnel_service.fixed_port
        summary["heartbeat_seconds"] = tunnel_service.heartbeat_seconds

        service_log(
            "reverse_tunnel",
            f"lease created tunnel_id={lease.tunnel_id} agent_id={lease.agent_id} domain={lease.domain} protocol={lease.protocol}",
        )
        return jsonify(summary), 200

    app.register_blueprint(blueprint)
