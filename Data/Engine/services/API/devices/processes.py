# ======================================================
# Data\Engine\services\API\devices\processes.py
# Description: Live process inventory and process control endpoints for managed devices.
#
# API Endpoints (if applicable):
# - GET /api/device/processes/<hostname> (Token Authenticated) - Returns a live process snapshot for an in-scope device.
# - POST /api/device/processes/<hostname>/terminate (Token Authenticated) - Requests process termination on an in-scope device.
# ======================================================

"""Live process inventory and operator-triggered process control endpoints."""
from __future__ import annotations

import time
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request

from ...auth import UserSiteAccessManager
from .tunnel import _current_user, _require_login, _resolve_requested_agent_id

if False:  # pragma: no cover - hint for type checkers
    from .. import EngineServiceAdapters


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _coerce_int(value: Any, default: int = 0) -> int:
    try:
        if value in (None, ""):
            raise ValueError
        return int(float(value))
    except Exception:
        return default


def _error_status(error_code: str) -> int:
    normalized = _clean_text(error_code).lower()
    if normalized in {"invalid_action", "invalid_request", "pid_required"}:
        return 400
    if normalized in {"process_not_found", "not_found"}:
        return 404
    if normalized in {"access_denied", "permission_denied"}:
        return 403
    if normalized in {"protected_process", "termination_failed"}:
        return 409
    if normalized in {"unsupported", "unsupported_platform"}:
        return 501
    if normalized in {"timeout"}:
        return 504
    if normalized in {"agent_unavailable", "socket_unavailable"}:
        return 503
    return 502


def register_processes(app, adapters: "EngineServiceAdapters") -> None:
    """Register live process-management routes."""

    blueprint = Blueprint("device_processes", __name__)
    logger = adapters.context.logger.getChild("device_processes.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("device_processes", message, level=level)
        except Exception:
            logger.debug("device_processes service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    def _notify_clients(hostname: str, change: str) -> None:
        socketio = getattr(adapters.context, "socketio", None)
        normalized_hostname = _clean_text(hostname)
        normalized_change = _clean_text(change)
        if socketio is None or not normalized_hostname or not normalized_change:
            return
        try:
            socketio.emit(
                "device_processes_changed",
                {
                    "hostname": normalized_hostname,
                    "change": normalized_change,
                    "changed_at": int(time.time()),
                },
            )
        except Exception:
            logger.debug("device_processes_changed emit failed hostname=%s", normalized_hostname, exc_info=True)

    def _load_device_record(hostname: str) -> Optional[Dict[str, Any]]:
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                """
                SELECT hostname, agent_id, operating_system, last_seen
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
                "hostname": _clean_text(row[0]),
                "agent_id": _clean_text(row[1]),
                "operating_system": _clean_text(row[2]),
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

    def _resolve_process_request_context(hostname: str) -> Tuple[Optional[Dict[str, Any]], Optional[Dict[str, Any]], int]:
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return None, payload, status

        user = _current_user(app) or {}
        if not site_access.user_can_access_hostname(user, hostname):
            return None, {"error": "not found"}, 404

        record = _load_device_record(hostname)
        if record is None:
            return None, {"error": "not found"}, 404

        return record, None, 200

    def _call_process_rpc(
        hostname: str,
        record: Dict[str, Any],
        payload: Dict[str, Any],
        *,
        timeout: float,
    ) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        normalized_hostname = record.get("hostname") or hostname
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        if not _agent_socket_available(normalized_hostname, agent_id):
            return None, ({"error": "agent_unavailable", "message": "The agent SYSTEM socket is not available."}, 503)

        response = None
        caller = getattr(adapters.context, "call_host_service_event", None)
        if callable(caller):
            try:
                response = caller(
                    normalized_hostname,
                    "system",
                    "process_management_request",
                    payload,
                    timeout=timeout,
                )
            except Exception:
                logger.debug("process-management host rpc failed hostname=%s", normalized_hostname, exc_info=True)

        if response is None and agent_id:
            agent_caller = getattr(adapters.context, "call_agent_event", None)
            if callable(agent_caller):
                try:
                    response = agent_caller(agent_id, "process_management_request", payload, timeout=timeout)
                except Exception:
                    logger.debug("process-management agent rpc failed agent_id=%s", agent_id, exc_info=True)

        if response is None:
            return None, ({"error": "timeout", "message": "The device did not answer the process request in time."}, 504)
        if not isinstance(response, dict):
            return None, ({"error": "invalid_agent_response", "message": "The device returned an invalid process response."}, 502)
        if response.get("ok") is False:
            error_code = _clean_text(response.get("error")) or "agent_error"
            message = _clean_text(response.get("message"))
            return None, ({"error": error_code, "message": message or error_code}, _error_status(error_code))
        return response, None

    @blueprint.route("/api/device/processes/<hostname>", methods=["GET"])
    def list_device_processes(hostname: str):
        record, error_payload, error_status = _resolve_process_request_context(hostname)
        if error_payload is not None:
            return jsonify(error_payload), error_status

        assert record is not None
        response, rpc_error = _call_process_rpc(
            hostname,
            record,
            {
                "action": "list",
                "requested_at": int(time.time()),
            },
            timeout=8.0,
        )
        if rpc_error is not None:
            payload, status = rpc_error
            return jsonify(payload), status

        processes = response.get("processes") if isinstance(response.get("processes"), list) else []
        return (
            jsonify(
                {
                    "status": "ok",
                    "hostname": record.get("hostname") or hostname,
                    "agent_id": _resolve_requested_agent_id(adapters, record.get("agent_id")),
                    "agent_socket": True,
                    "reported_at": _coerce_int(response.get("reported_at"), 0),
                    "refresh_interval_ms": max(5000, _coerce_int(response.get("refresh_interval_ms"), 5000)),
                    "count": len(processes),
                    "processes": processes,
                }
            ),
            200,
        )

    @blueprint.route("/api/device/processes/<hostname>/terminate", methods=["POST"])
    def terminate_device_process(hostname: str):
        record, error_payload, error_status = _resolve_process_request_context(hostname)
        if error_payload is not None:
            return jsonify(error_payload), error_status

        body = request.get_json(silent=True) or {}
        pid = _coerce_int(body.get("pid"), 0)
        include_children = bool(body.get("include_children"))
        if pid <= 0:
            return jsonify({"error": "pid_required"}), 400

        assert record is not None
        user = _current_user(app) or {}
        operator_id = _clean_text(user.get("username")) or "unknown"
        response, rpc_error = _call_process_rpc(
            hostname,
            record,
            {
                "action": "terminate",
                "pid": pid,
                "include_children": include_children,
                "requested_at": int(time.time()),
                "requested_by": operator_id,
            },
            timeout=15.0,
        )
        if rpc_error is not None:
            payload, status = rpc_error
            _service_log_event(
                "device_process_terminate_failed hostname={hostname} pid={pid} operator={operator} remote={remote} error={error}".format(
                    hostname=record.get("hostname") or hostname,
                    pid=pid,
                    operator=operator_id or "-",
                    remote=_request_remote() or "-",
                    error=payload.get("error") or "-",
                ),
                level="WARNING",
            )
            return jsonify(payload), status

        processes = response.get("processes") if isinstance(response.get("processes"), list) else []
        _service_log_event(
            "device_process_terminate_request hostname={hostname} pid={pid} include_children={include_children} operator={operator} remote={remote}".format(
                hostname=record.get("hostname") or hostname,
                pid=pid,
                include_children="true" if include_children else "false",
                operator=operator_id or "-",
                remote=_request_remote() or "-",
            )
        )
        _notify_clients(record.get("hostname") or hostname, "terminated")
        return (
            jsonify(
                {
                    "status": "ok",
                    "hostname": record.get("hostname") or hostname,
                    "agent_id": _resolve_requested_agent_id(adapters, record.get("agent_id")),
                    "terminated_pid": pid,
                    "reported_at": _coerce_int(response.get("reported_at"), 0),
                    "refresh_interval_ms": max(5000, _coerce_int(response.get("refresh_interval_ms"), 5000)),
                    "count": len(processes),
                    "processes": processes,
                }
            ),
            200,
        )

    app.register_blueprint(blueprint)
