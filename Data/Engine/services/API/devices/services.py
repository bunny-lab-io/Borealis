# ======================================================
# Data\Engine\services\API\devices\services.py
# Description: Device service inventory and control endpoints.
#
# API Endpoints (if applicable):
# - GET /api/device/services/<hostname> (Token Authenticated) - Returns cached service inventory for an in-scope device.
# - POST /api/device/services/<hostname>/action (Token Authenticated) - Start, stop, or restart a named service on an in-scope device.
# ======================================================

"""Device service inventory and control endpoints for the Borealis Engine."""
from __future__ import annotations

import time
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request

from ...auth import UserSiteAccessManager
from .service_inventory import (
    action_label,
    mark_service_control_pending,
    normalize_device_services,
    normalize_service_action,
    serialize_device_services,
)
from .tunnel import _current_user, _require_login, _resolve_requested_agent_id

if False:  # pragma: no cover - hint for type checkers
    from .. import EngineServiceAdapters


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def register_services(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("device_services", __name__)
    logger = adapters.context.logger.getChild("device_services.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("device_services", message, level=level)
        except Exception:
            logger.debug("device_services service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    def _notify_clients(hostname: str, change: str) -> None:
        socketio = getattr(adapters.context, "socketio", None)
        normalized_hostname = _normalize_text(hostname)
        normalized_change = _normalize_text(change)
        if socketio is None or not normalized_hostname or not normalized_change:
            return
        try:
            socketio.emit(
                "device_services_changed",
                {
                    "hostname": normalized_hostname,
                    "change": normalized_change,
                },
            )
        except Exception:
            logger.debug("device_services_changed emit failed hostname=%s", normalized_hostname, exc_info=True)

    def _load_device_record(hostname: str) -> Optional[Dict[str, Any]]:
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                """
                SELECT hostname, agent_id, services, last_seen
                  FROM devices
                 WHERE LOWER(hostname) = LOWER(?)
              ORDER BY last_seen DESC
                 LIMIT 1
                """,
                (hostname,),
            )
            row = cur.fetchone()
            if not row:
                return None
            return {
                "hostname": _normalize_text(row[0]),
                "agent_id": _normalize_text(row[1]),
                "services": row[2],
                "last_seen": row[3] or 0,
            }
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def _agent_socket_available(hostname: str, agent_id: str) -> bool:
        has_host_socket = getattr(adapters.context, "has_host_service_socket", None)
        if callable(has_host_socket):
            try:
                if bool(has_host_socket(hostname, "system")):
                    return True
            except Exception:
                logger.debug("has_host_service_socket failed hostname=%s", hostname, exc_info=True)
        registry = getattr(adapters.context, "agent_socket_registry", None)
        if registry and hasattr(registry, "is_registered") and agent_id:
            try:
                return bool(registry.is_registered(agent_id))
            except Exception:
                logger.debug("agent_socket_registry lookup failed agent_id=%s", agent_id, exc_info=True)
        return False

    @blueprint.route("/api/device/services/<hostname>", methods=["GET"])
    def get_device_services(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404

        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404

        services_payload = normalize_device_services(record.get("services"))
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        response = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "agent_socket": _agent_socket_available(record.get("hostname") or hostname, agent_id),
            "reported_at": services_payload.get("reported_at") or 0,
            "refresh_interval_seconds": 60,
            "count": len(services_payload.get("services") or []),
            "services": services_payload.get("services") or [],
        }
        return jsonify(response), 200

    @blueprint.route("/api/device/services/<hostname>/action", methods=["POST"])
    def service_action(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = _normalize_text(user.get("username"))
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404

        body = request.get_json(silent=True) or {}
        service_name = _normalize_text(body.get("service_name") or body.get("name"))
        action = normalize_service_action(body.get("action"))
        if not service_name:
            return jsonify({"error": "service_name_required"}), 400
        if not action:
            return jsonify({"error": "invalid_action"}), 400

        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404

        requested_at = int(time.time())
        updated_services = mark_service_control_pending(
            record.get("services"),
            service_name,
            action,
            requested_at=requested_at,
            requested_by=operator_id,
        )
        if updated_services is None:
            return jsonify({"error": "service_not_found"}), 404

        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
        emit_agent_event = getattr(adapters.context, "emit_agent_event", None)
        event_payload = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "service_name": service_name,
            "action": action,
            "requested_at": requested_at,
            "requested_by": operator_id,
        }

        emitted = False
        if callable(emit_host_service_event):
            try:
                emitted = bool(
                    emit_host_service_event(
                        record.get("hostname") or hostname,
                        "system",
                        "service_control_action",
                        event_payload,
                    )
                )
            except Exception:
                emitted = False
        if not emitted and callable(emit_agent_event) and agent_id:
            try:
                emitted = bool(emit_agent_event(agent_id, "service_control_action", event_payload))
            except Exception:
                emitted = False
        if not emitted:
            _service_log_event(
                "device_services_action_unavailable hostname={0} agent_id={1} service_name={2} action={3} operator={4} remote={5}".format(
                    record.get("hostname") or hostname,
                    agent_id or "-",
                    service_name,
                    action,
                    operator_id or "-",
                    _request_remote() or "-",
                ),
                level="WARNING",
            )
            return jsonify({"error": "agent_unavailable"}), 409

        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET services = ? WHERE LOWER(hostname) = LOWER(?)",
                (serialize_device_services(updated_services), hostname),
            )
            conn.commit()
        except Exception as exc:
            if conn is not None:
                try:
                    conn.rollback()
                except Exception:
                    pass
            logger.debug("Failed to persist pending service action", exc_info=True)
            return jsonify({"error": "persist_failed"}), 500
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

        _service_log_event(
            "device_services_action_request hostname={0} agent_id={1} service_name={2} action={3} operator={4} remote={5}".format(
                record.get("hostname") or hostname,
                agent_id or "-",
                service_name,
                action,
                operator_id or "-",
                _request_remote() or "-",
            )
        )
        _notify_clients(record.get("hostname") or hostname, "requested")

        response = normalize_device_services(updated_services)
        return jsonify(
            {
                "status": "ok",
                "hostname": record.get("hostname") or hostname,
                "agent_id": agent_id,
                "service_name": service_name,
                "action": action,
                "action_label": action_label(action),
                "requested_at": requested_at,
                "reported_at": response.get("reported_at") or 0,
                "count": len(response.get("services") or []),
                "services": response.get("services") or [],
            }
        ), 200

    app.register_blueprint(blueprint)

