"""Remote-operation session broker endpoints."""

from __future__ import annotations

from typing import Any, Dict, Mapping, Optional

from flask import Blueprint, jsonify, request

from ...auth import RequestAuthContext, UserSiteAccessManager
from ...job_scheduler.queue import active_worker_route_for_site, ensure_job_scheduler_tables
from ...remote_ops.agent_routes import site_worker_route_urls
from ...remote_ops.sessions import (
    REMOTE_OP_SESSION_AUDIENCE,
    REMOTE_OP_SESSION_ISSUER,
    REMOTE_OP_SESSION_TOKEN_TYPE,
    RemoteOpSessionError,
    issue_remote_op_session,
    normalize_remote_op_capabilities,
)

if False:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_int(value: Any) -> Optional[int]:
    if value in (None, "", "null"):
        return None
    try:
        return int(value)
    except Exception:
        return None


def _lookup_device(conn, body: Mapping[str, Any]) -> Optional[Dict[str, Any]]:
    guid = _normalize_text(body.get("device_guid") or body.get("guid"))
    hostname = _normalize_text(body.get("hostname") or body.get("device_hostname"))
    agent_id = _normalize_text(body.get("agent_id"))
    clauses = []
    params = []
    if guid:
        clauses.append("UPPER(d.guid)=UPPER(?)")
        params.append(guid)
    if hostname:
        clauses.append("LOWER(d.hostname)=LOWER(?)")
        params.append(hostname)
    if agent_id:
        clauses.append("LOWER(d.agent_id)=LOWER(?)")
        params.append(agent_id)
    if not clauses:
        return None
    where_sql = " OR ".join(clauses)
    cur = conn.cursor()
    cur.execute(
        f"""
        SELECT d.guid, d.hostname, d.agent_id, ds.site_id, s.name
          FROM devices AS d
     LEFT JOIN device_sites AS ds ON ds.device_hostname=d.hostname
     LEFT JOIN sites AS s ON s.id=ds.site_id
         WHERE {where_sql}
      ORDER BY COALESCE(d.last_seen, 0) DESC, d.hostname ASC
         LIMIT 1
        """,
        tuple(params),
    )
    row = cur.fetchone()
    if not row:
        return None
    return {
        "guid": _normalize_text(row[0]),
        "hostname": _normalize_text(row[1]),
        "agent_id": _normalize_text(row[2]),
        "site_id": _normalize_int(row[3]),
        "site_name": _normalize_text(row[4]),
    }


def register_remote_ops_sessions(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("remote_ops_sessions", __name__)
    logger = adapters.context.logger.getChild("remote_ops.sessions")
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )

    @blueprint.route("/api/remote-ops/session", methods=["POST"])
    def create_remote_op_session():
        user, auth_error = auth.require_user()
        if auth_error:
            payload, status = auth_error
            return jsonify(payload), status
        assert user is not None

        body = request.get_json(silent=True) or {}
        capabilities = normalize_remote_op_capabilities(body.get("capabilities") or body.get("capability"))
        if not capabilities:
            return jsonify({"error": "invalid_capability", "message": "Valid remote-operation capability is required."}), 400

        conn = adapters.db_conn_factory()
        try:
            ensure_job_scheduler_tables(conn)
            device = _lookup_device(conn, body)
            if not device:
                return jsonify({"error": "not_found", "message": "Device was not found."}), 404
            site_id = _normalize_int(device.get("site_id"))
            if site_id is None:
                return jsonify({"error": "device_site_unassigned", "message": "Device is not assigned to a site."}), 409
            route = active_worker_route_for_site(conn, site_id=site_id)
        finally:
            conn.close()

        if not site_access.user_can_access_site(user, device.get("site_id")):
            return jsonify({"error": "not_found", "message": "Device was not found."}), 404
        if not route:
            return jsonify({"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."}), 409

        try:
            issued = issue_remote_op_session(
                adapters.jwt_service,
                user=user,
                device=device,
                worker_route=route,
                capabilities=capabilities,
                ttl_seconds=body.get("ttl_seconds"),
            )
        except RemoteOpSessionError as exc:
            return jsonify({"error": exc.code, "message": exc.message}), 400

        urls = site_worker_route_urls(adapters.context, request, route)
        session_payload = {
            "session_id": issued["session_id"],
            "token_type": "Bearer",
            "token": issued["token"],
            "issuer": REMOTE_OP_SESSION_ISSUER,
            "audience": REMOTE_OP_SESSION_AUDIENCE,
            "operation_token_type": REMOTE_OP_SESSION_TOKEN_TYPE,
            "issued_at": issued["issued_at"],
            "expires_at": issued["expires_at"],
            "expires_in": issued["expires_in"],
            "capabilities": issued["capabilities"],
            "user": {
                "username": _normalize_text(user.get("username")),
                "role": _normalize_text(user.get("role")) or "User",
            },
            "device": {
                "guid": device.get("guid") or "",
                "hostname": device.get("hostname") or "",
                "agent_id": device.get("agent_id") or "",
                "site_id": device.get("site_id"),
                "site_name": device.get("site_name") or "",
            },
            "worker": {
                "worker_guid": route.get("worker_guid") or "",
                "route_generation": route.get("generation") or 0,
                "route_path_prefix": route.get("route_path_prefix") or "",
                "base_url": urls["base"],
                "urls": urls,
            },
        }
        return jsonify({"status": "ok", "session": session_payload}), 200

    app.register_blueprint(blueprint)


__all__ = ["register_remote_ops_sessions"]
