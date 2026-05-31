# ======================================================
# Data\Engine\services\API\devices\shell.py
# Description: Remote PowerShell session endpoints for persistent WireGuard tunnels.
#
# API Endpoints (if applicable):
# - POST /api/shell/establish (Token Authenticated) - Ensure shell readiness over WireGuard.
# - POST /api/shell/disconnect (Token Authenticated) - Disconnect the operator shell session.
# ======================================================

"""Remote PowerShell session endpoints for the Borealis Engine."""
from __future__ import annotations

import os
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....public_endpoints import wireguard_endpoint
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...job_scheduler.queue import active_worker_route_for_site, ensure_job_scheduler_tables
from ...remote_ops.agent_routes import site_worker_route_urls
from ...remote_ops.sessions import issue_remote_op_session
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


def _infer_endpoint_host(adapters: "EngineServiceAdapters", req) -> str:
    host, _port = wireguard_endpoint(adapters.context, req=req)
    return host


def _lookup_shell_device_and_route(adapters: "EngineServiceAdapters", agent_id: str) -> Tuple[Optional[Dict[str, Any]], Optional[Dict[str, Any]]]:
    conn = adapters.db_conn_factory()
    try:
        ensure_job_scheduler_tables(conn)
        cur = conn.cursor()
        cur.execute(
            """
            SELECT d.guid, d.hostname, d.agent_id, ds.site_id, s.name
              FROM devices AS d
         LEFT JOIN device_sites AS ds ON ds.device_hostname=d.hostname
         LEFT JOIN sites AS s ON s.id=ds.site_id
             WHERE LOWER(d.agent_id)=LOWER(?)
             LIMIT 1
            """,
            (agent_id,),
        )
        row = cur.fetchone()
        if not row:
            return None, None
        device = {
            "guid": _normalize_text(row[0]),
            "hostname": _normalize_text(row[1]),
            "agent_id": _normalize_text(row[2]),
            "site_id": row[3],
            "site_name": _normalize_text(row[4]),
        }
        site_id = int(row[3]) if row[3] is not None else 0
        route = active_worker_route_for_site(conn, site_id=site_id) if site_id > 0 else None
        return device, dict(route) if route else None
    finally:
        conn.close()


def register_shell(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("vpn_shell", __name__)
    logger = adapters.context.logger.getChild("vpn_shell.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("VPN_Tunnel/remote_shell", message, level=level)
        except Exception:
            logger.debug("vpn_shell service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    @blueprint.route("/api/shell/establish", methods=["POST"])
    def shell_establish():
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
        device, worker_route = _lookup_shell_device_and_route(adapters, agent_id)
        if not device:
            return jsonify({"error": "not found"}), 404
        if not worker_route:
            return jsonify({"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."}), 409

        try:
            tunnel_service = _get_tunnel_service(adapters)
            endpoint_host = _infer_endpoint_host(adapters, request)
            _service_log_event(
                "vpn_shell_establish_request requested_agent_id={0} resolved_agent_id={1} operator={2} endpoint_host={3} remote={4}".format(
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
                "vpn_shell_establish_failed agent_id={0} operator={1} error={2}".format(
                    agent_id,
                    operator_id or "-",
                    str(exc),
                ),
                level="ERROR",
            )
            return jsonify({"error": "establish_failed", "detail": str(exc)}), 500

        issued = issue_remote_op_session(
            adapters.jwt_service,
            user=user,
            device=device,
            worker_route=worker_route,
            capabilities=["remote_shell"],
            ttl_seconds=body.get("ttl_seconds"),
        )
        urls = site_worker_route_urls(adapters.context, request, worker_route)

        response = dict(payload)
        response["status"] = "ok"
        response["agent_socket"] = True
        response["shell_port"] = int(getattr(adapters.context, "wireguard_shell_port", 47002) or 47002)
        response["remote_ops_session"] = {
            "session_id": issued["session_id"],
            "token_type": "Bearer",
            "token": issued["token"],
            "issued_at": issued["issued_at"],
            "expires_at": issued["expires_at"],
            "expires_in": issued["expires_in"],
            "capabilities": issued["capabilities"],
            "device": {
                "guid": device.get("guid") or "",
                "hostname": device.get("hostname") or "",
                "agent_id": device.get("agent_id") or "",
                "site_id": device.get("site_id"),
                "site_name": device.get("site_name") or "",
            },
            "worker": {
                "worker_guid": worker_route.get("worker_guid") or "",
                "route_generation": worker_route.get("generation") or 0,
                "route_path_prefix": worker_route.get("route_path_prefix") or "",
                "base_url": urls["base"],
                "urls": urls,
            },
        }
        _service_log_event(
            "vpn_shell_establish_response agent_id={0} tunnel_id={1} worker_guid={2}".format(
                agent_id,
                response.get("tunnel_id", "-"),
                worker_route.get("worker_guid", "-"),
            )
        )
        return jsonify(response), 200

    @blueprint.route("/api/shell/disconnect", methods=["POST"])
    def shell_disconnect():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        body = request.get_json(silent=True) or {}
        agent_id = _normalize_text(body.get("agent_id"))
        reason = _normalize_text(body.get("reason") or "operator_disconnect")
        operator_id = (_current_user(app) or {}).get("username") or None

        if not agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        resolved_agent_id = _resolve_requested_agent_id(adapters, agent_id)
        if not site_access.user_can_access_agent_id(_current_user(app) or {}, resolved_agent_id):
            return jsonify({"error": "not found"}), 404

        _service_log_event(
            "vpn_shell_disconnect_request agent_id={0} operator={1} reason={2} remote={3}".format(
                resolved_agent_id,
                operator_id or "-",
                reason or "-",
                _request_remote() or "-",
            )
        )
        return jsonify({"status": "disconnected", "reason": reason}), 200

    app.register_blueprint(blueprint)


__all__ = ["register_shell"]
