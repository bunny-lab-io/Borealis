# ======================================================
# Data\Engine\services\API\devices\vnc.py
# Description: VNC session bootstrap for noVNC WebSocket tunnels.
#
# API Endpoints (if applicable):
# - POST /api/vnc/establish (Token Authenticated) - Establish a VNC session for noVNC.
# - POST /api/vnc/disconnect (Token Authenticated) - Disconnect the operator VNC session.
# - POST /api/vnc/session (Token Authenticated) - Legacy alias for establish.
# ======================================================

"""VNC session bootstrap endpoints for the Borealis Engine."""
from __future__ import annotations

import os
import secrets
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....public_endpoints import build_websocket_url, public_vnc_path, wireguard_endpoint
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...RemoteDesktop.vnc_proxy import ensure_vnc_proxy
from .tunnel import _get_tunnel_service, _resolve_requested_agent_id

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


def _normalize_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    if isinstance(value, str):
        cleaned = value.strip().lower()
        if cleaned in {"1", "true", "yes", "y", "on"}:
            return True
        if cleaned in {"0", "false", "no", "n", "off"}:
            return False
    return default


def _generate_vnc_password() -> str:
    # UltraVNC uses the first 8 characters for VNC auth; keep the token to 8 for compatibility.
    return secrets.token_hex(4)


def _load_vnc_password(adapters: "EngineServiceAdapters", agent_id: str) -> Optional[str]:
    conn = adapters.db_conn_factory()
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
        adapters.context.logger.debug("Failed to load agent VNC password", exc_info=True)
    finally:
        try:
            conn.close()
        except Exception:
            pass
    return None


def _store_vnc_password(adapters: "EngineServiceAdapters", agent_id: str, password: str) -> None:
    conn = adapters.db_conn_factory()
    try:
        cur = conn.cursor()
        cur.execute(
            "UPDATE devices SET agent_vnc_password=? WHERE agent_id=?",
            (password, agent_id),
        )
        conn.commit()
    except Exception:
        adapters.context.logger.debug("Failed to store agent VNC password", exc_info=True)
    finally:
        try:
            conn.close()
        except Exception:
            pass


def register_vnc(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("vnc", __name__)
    logger = adapters.context.logger.getChild("vnc.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("VNC", message, level=level)
        except Exception:
            logger.debug("vnc service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    def _issue_session(
        agent_id: str,
        operator_id: Optional[str],
        *,
        remove_wallpaper: bool,
    ) -> Tuple[Dict[str, Any], int]:
        _ = remove_wallpaper
        tunnel_service = _get_tunnel_service(adapters)
        session_payload = tunnel_service.session_payload(agent_id, include_token=False)
        if not session_payload:
            try:
                session_payload = tunnel_service.connect(
                    agent_id=agent_id,
                    operator_id=operator_id,
                    endpoint_host=wireguard_endpoint(adapters.context, req=request)[0],
                )
            except Exception:
                return {"error": "tunnel_down"}, 409
        def _restart_tunnel(reason: str) -> None:
            tunnel_service.mark_transport_required(agent_id, reason=reason)
            tunnel_service.request_agent_start(
                agent_id,
                force_restart=True,
                reason=reason,
            )
            if str(reason or "").strip().lower() == "vnc_connect_retry":
                tunnel_service.recover_transport(
                    agent_id,
                    trigger="vnc_connect",
                    reason=reason,
                )

        try:
            _restart_tunnel("vnc_bootstrap")
        except Exception:
            logger.debug("Failed to re-emit vpn_tunnel_start before VNC bootstrap", exc_info=True)

        vnc_port = int(getattr(adapters.context, "vnc_port", 5900))

        virtual_ip = _normalize_text(session_payload.get("virtual_ip"))
        host = virtual_ip.split("/")[0] if virtual_ip else ""
        if not host:
            return {"error": "virtual_ip_missing"}, 500

        vnc_password = _load_vnc_password(adapters, agent_id)
        if not vnc_password:
            vnc_password = _generate_vnc_password()
            _store_vnc_password(adapters, agent_id, vnc_password)
        if len(vnc_password) > 8:
            vnc_password = vnc_password[:8]
            _store_vnc_password(adapters, agent_id, vnc_password)

        registry = ensure_vnc_proxy(adapters.context, logger=logger)
        if registry is None:
            return {"error": "vnc_proxy_unavailable"}, 503

        _service_log_event(
            "vnc_establish_request agent_id={0} operator={1} remote={2}".format(
                agent_id,
                operator_id or "-",
                _request_remote() or "-",
            )
        )

        vnc_session = registry.create(
            agent_id=agent_id,
            host=host,
            port=vnc_port,
            operator_id=operator_id,
            restart_tunnel=_restart_tunnel,
        )
        ws_path = public_vnc_path(adapters.context)
        ws_url = build_websocket_url(
            adapters.context,
            request,
            ws_path,
            query={"token": vnc_session.token},
        )

        _service_log_event("vnc_session_ready agent_id={0}".format(agent_id))

        return (
            {
                "ws_url": ws_url,
                "ws_path": ws_path,
                "token": vnc_session.token,
                "virtual_ip": host,
                "tunnel_id": session_payload.get("tunnel_id"),
                "engine_virtual_ip": session_payload.get("engine_virtual_ip"),
                "vnc_password": vnc_password,
                "vnc_port": vnc_port,
            },
            200,
        )

    @blueprint.route("/api/vnc/establish", methods=["POST"])
    def vnc_establish():
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
        if not site_access.user_can_access_agent_id(user, agent_id):
            return jsonify({"error": "not found"}), 404

        remove_wallpaper = _normalize_bool(body.get("remove_wallpaper"), default=True)

        payload, status = _issue_session(
            agent_id,
            operator_id,
            remove_wallpaper=remove_wallpaper,
        )
        return jsonify(payload), status

    @blueprint.route("/api/vnc/session", methods=["POST"])
    def vnc_session():
        return vnc_establish()

    @blueprint.route("/api/vnc/disconnect", methods=["POST"])
    def vnc_disconnect():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        agent_id = _normalize_text(body.get("agent_id"))
        reason = _normalize_text(body.get("reason") or "operator_disconnect")

        if not agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        resolved_agent_id = _resolve_requested_agent_id(adapters, agent_id)
        if not site_access.user_can_access_agent_id(user, resolved_agent_id):
            return jsonify({"error": "not found"}), 404

        _service_log_event(
            "vnc_disconnect agent_id={0} operator={1} reason={2}".format(
                resolved_agent_id,
                operator_id or "-",
                reason or "-",
            )
        )

        return jsonify({"status": "disconnected", "reason": reason}), 200

    app.register_blueprint(blueprint)
