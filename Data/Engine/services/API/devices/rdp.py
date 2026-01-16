# ======================================================
# Data\Engine\services\API\devices\rdp.py
# Description: RDP session bootstrap for Guacamole WebSocket tunnels.
#
# API Endpoints (if applicable):
# - POST /api/rdp/session (Token Authenticated) - Issues a one-time Guacamole tunnel token for RDP.
# ======================================================

"""RDP session bootstrap endpoints for the Borealis Engine."""
from __future__ import annotations

import os
from typing import Any, Dict, Optional, Tuple
from urllib.parse import urlsplit

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ...RemoteDesktop.guacamole_proxy import GUAC_WS_PATH, ensure_guacamole_proxy
from .tunnel import _get_tunnel_service

if False:  # pragma: no cover - hint for type checkers
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


def _is_secure(req) -> bool:
    if req.is_secure:
        return True
    forwarded = (req.headers.get("X-Forwarded-Proto") or "").split(",")[0].strip().lower()
    return forwarded == "https"


def register_rdp(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("rdp", __name__)
    logger = adapters.context.logger.getChild("rdp.api")
    service_log = adapters.service_log

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("RDP", message, level=level)
        except Exception:
            logger.debug("rdp service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    @blueprint.route("/api/rdp/session", methods=["POST"])
    def rdp_session():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        agent_id = _normalize_text(body.get("agent_id"))
        protocol = _normalize_text(body.get("protocol") or "rdp").lower()
        username = _normalize_text(body.get("username"))
        password = str(body.get("password") or "")

        if not agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        if protocol != "rdp":
            return jsonify({"error": "unsupported_protocol"}), 400

        tunnel_service = _get_tunnel_service(adapters)
        session_payload = tunnel_service.session_payload(agent_id, include_token=False)
        if not session_payload:
            return jsonify({"error": "tunnel_down"}), 409

        allowed_ports = session_payload.get("allowed_ports") or []
        if 3389 not in allowed_ports:
            return jsonify({"error": "rdp_not_allowed"}), 403

        virtual_ip = _normalize_text(session_payload.get("virtual_ip"))
        host = virtual_ip.split("/")[0] if virtual_ip else ""
        if not host:
            return jsonify({"error": "virtual_ip_missing"}), 500

        registry = ensure_guacamole_proxy(adapters.context, logger=logger)
        if registry is None:
            return jsonify({"error": "rdp_proxy_unavailable"}), 503

        _service_log_event(
            "rdp_session_request agent_id={0} operator={1} protocol={2} remote={3}".format(
                agent_id,
                operator_id or "-",
                protocol,
                _request_remote() or "-",
            )
        )

        rdp_session = registry.create(
            agent_id=agent_id,
            host=host,
            port=3389,
            username=username,
            password=password,
            protocol=protocol,
            operator_id=operator_id,
            ignore_cert=True,
        )

        ws_scheme = "wss" if _is_secure(request) else "ws"
        ws_host = _infer_endpoint_host(request)
        ws_port = int(getattr(adapters.context, "rdp_ws_port", 4823))
        ws_url = f"{ws_scheme}://{ws_host}:{ws_port}{GUAC_WS_PATH}"

        _service_log_event(
            "rdp_session_ready agent_id={0} token={1} expires_at={2}".format(
                agent_id,
                rdp_session.token[:8],
                int(rdp_session.expires_at),
            )
        )

        return (
            jsonify(
                {
                    "token": rdp_session.token,
                    "ws_url": ws_url,
                    "expires_at": int(rdp_session.expires_at),
                    "virtual_ip": host,
                    "tunnel_id": session_payload.get("tunnel_id"),
                }
            ),
            200,
        )

    app.register_blueprint(blueprint)
