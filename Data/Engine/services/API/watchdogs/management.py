# ======================================================
# Data\Engine\services\API\watchdogs\management.py
# Description: Watchdog policy and incident API endpoints backed by the
#              Engine-native watchdog runtime.
#
# API Endpoints (if applicable):
# - GET /api/watchdogs (Token Authenticated) - Lists watchdog policies in the current operator site scope.
# - GET /api/watchdogs/metadata (Token Authenticated) - Returns rule/action metadata for the Watchdog editor.
# - POST /api/watchdogs/preview (Token Authenticated) - Evaluates an unsaved watchdog definition against current inventory.
# - GET /api/watchdogs/<int:watchdog_id> (Token Authenticated) - Returns one watchdog policy.
# - POST /api/watchdogs (Token Authenticated) - Creates a watchdog policy.
# - PUT /api/watchdogs/<int:watchdog_id> (Token Authenticated) - Updates a watchdog policy.
# - DELETE /api/watchdogs/<int:watchdog_id> (Token Authenticated) - Deletes a watchdog policy.
# - GET /api/watchdogs/incidents (Token Authenticated) - Lists watchdog incidents in open, suppressed, resolved, or all states in scope.
# - POST /api/watchdogs/incidents/<int:incident_id>/acknowledge (Token Authenticated) - Acknowledges one active incident.
# - POST /api/watchdogs/incidents/<int:incident_id>/state (Token Authenticated) - Moves an incident between the open and suppressed queues.
# - GET /api/devices/<device_id>/watchdogs (Token Authenticated) - Returns device-specific watchdog assignments, incidents, and overrides.
# - POST /api/devices/<device_id>/watchdogs/overrides (Token Authenticated) - Creates, updates, or clears a per-device watchdog override.
# ======================================================

"""Watchdog policy and incident API registration for the Borealis Engine."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from flask import Blueprint, Flask, jsonify, request

from ...auth import RequestAuthContext
from .runtime import WatchdogRuntimeService

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def ensure_watchdog_runtime(app: Flask, adapters: "EngineServiceAdapters") -> WatchdogRuntimeService:
    runtime = getattr(adapters.context, "watchdog_runtime", None)
    if runtime is not None:
        return runtime
    runtime = WatchdogRuntimeService(
        db_conn_factory=adapters.db_conn_factory,
        socketio=getattr(adapters.context, "socketio", None),
        service_log=adapters.service_log,
        logger=adapters.context.logger.getChild("watchdogs"),
        assembly_cache=adapters.context.assembly_cache,
        app=app,
        adapters=adapters,
        context=adapters.context,
        github_integration=adapters.github_integration,
        agent_release_manager=getattr(adapters, "agent_release_manager", None),
    )
    runtime.start()
    adapters.context.watchdog_runtime = runtime
    adapters.service_log("watchdogs", "watchdog runtime initialised", level="INFO")
    return runtime


def register_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    runtime = ensure_watchdog_runtime(app, adapters)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )
    blueprint = Blueprint("watchdogs", __name__)

    def _require_user() -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        return auth.require_user()

    def _site_query_arg() -> Optional[int]:
        raw = request.args.get("site") or request.args.get("site_id")
        if raw in (None, ""):
            return None
        try:
            site_id = int(str(raw).strip())
        except Exception:
            return None
        return site_id if site_id > 0 else None

    @blueprint.route("/api/watchdogs", methods=["GET"])
    def list_watchdogs():
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        archived_raw = request.args.get("archived")
        archived = None
        if archived_raw is not None:
            archived = str(archived_raw).strip().lower() in {"1", "true", "yes", "on"}
        return jsonify({"items": runtime.list_watchdogs(user=user, archived=archived, site_id=_site_query_arg())})

    @blueprint.route("/api/watchdogs/metadata", methods=["GET"])
    def watchdog_metadata():
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        del user
        return jsonify(
            {
                "site_modes": [
                    {"value": "global", "label": "Global"},
                    {"value": "specific_sites", "label": "Specific Sites"},
                    {"value": "global_exclusions", "label": "Global w/ Exclusions"},
                ],
                "match_modes": [
                    {"value": "all", "label": "All Rules"},
                    {"value": "any", "label": "Any Rule"},
                ],
                "severities": [
                    {"value": "info", "label": "Info"},
                    {"value": "warning", "label": "Warning"},
                    {"value": "error", "label": "Error"},
                ],
                "rule_types": [
                    {"value": "device_offline", "label": "Device Offline"},
                    {"value": "storage_usage_percent", "label": "Storage Usage"},
                    {"value": "service_state", "label": "Service State"},
                    {"value": "agent_role_health", "label": "Agent Role Health"},
                    {"value": "cpu_usage_percent", "label": "CPU Usage"},
                    {"value": "memory_usage_percent", "label": "Memory Usage"},
                    {"value": "uptime_above_seconds", "label": "Uptime"},
                    {"value": "reboot_detected", "label": "Reboot Detected"},
                    {"value": "service_pending_timeout", "label": "Service Pending Timeout"},
                    {"value": "user_session_match", "label": "Logged-In User Match"},
                    {"value": "process_presence", "label": "Process Presence"},
                    {"value": "session_state", "label": "Session State"},
                    {"value": "network_interface_change", "label": "Network Interface Change"},
                    {"value": "drive_presence_change", "label": "Drive Presence Change"},
                    {"value": "software_presence_or_version", "label": "Software Presence / Version"},
                    {"value": "agent_version_status", "label": "Agent Version Status"},
                ],
                "action_types": [
                    {"value": "do_nothing", "label": "Do Nothing"},
                    {"value": "notification", "label": "Engine Toast Notification"},
                    {"value": "service_control", "label": "Control Service"},
                    {"value": "assembly", "label": "Run Assembly"},
                ],
            }
        )

    @blueprint.route("/api/watchdogs/preview", methods=["POST"])
    def watchdog_preview():
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        record = runtime._normalize_watchdog_record(payload, username=(user or {}).get("username") or "Unknown")
        validation = runtime._validate_watchdog_record(record, user=user)
        if validation:
            return jsonify({"errors": validation}), 400
        return jsonify(runtime.evaluate_preview(record))

    @blueprint.route("/api/watchdogs/<int:watchdog_id>", methods=["GET"])
    def watchdog_detail(watchdog_id: int):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        record = runtime.get_watchdog(watchdog_id, user=user)
        if record is None:
            return jsonify({"error": "not_found"}), 404
        return jsonify(record)

    @blueprint.route("/api/watchdogs", methods=["POST"])
    def create_watchdog():
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        record, errors = runtime.save_watchdog(payload, user=user)
        if errors:
            return jsonify({"errors": errors}), 400
        return jsonify(record), 201

    @blueprint.route("/api/watchdogs/<int:watchdog_id>", methods=["PUT"])
    def update_watchdog(watchdog_id: int):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        payload["id"] = watchdog_id
        record, errors = runtime.save_watchdog(payload, user=user)
        if errors:
            return jsonify({"errors": errors}), 400
        return jsonify(record)

    @blueprint.route("/api/watchdogs/<int:watchdog_id>", methods=["DELETE"])
    def delete_watchdog(watchdog_id: int):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        deleted = runtime.delete_watchdog(watchdog_id, user=user)
        if not deleted:
            return jsonify({"error": "not_found"}), 404
        return jsonify({"status": "deleted"})

    @blueprint.route("/api/watchdogs/incidents", methods=["GET"])
    def list_watchdog_incidents():
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        state = request.args.get("state") or "open"
        site_id = _site_query_arg()
        return jsonify(
            {
                "items": runtime.list_incidents(user=user, state=state, site_id=site_id),
                "counts": runtime.list_incident_counts(user=user, site_id=site_id),
            }
        )

    @blueprint.route("/api/watchdogs/incidents/<int:incident_id>/acknowledge", methods=["POST"])
    def acknowledge_watchdog_incident(incident_id: int):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        incident = runtime.acknowledge_incident(incident_id, user=user)
        if incident is None:
            return jsonify({"error": "not_found"}), 404
        return jsonify(incident)

    @blueprint.route("/api/watchdogs/incidents/<int:incident_id>/state", methods=["POST"])
    def update_watchdog_incident_state(incident_id: int):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        incident, errors = runtime.update_incident_state(
            incident_id,
            state=payload.get("state") or "open",
            user=user,
            reason=payload.get("reason") or "",
        )
        if errors:
            if len(errors) == 1 and errors[0] == "Incident not found.":
                return jsonify({"error": "not_found"}), 404
            return jsonify({"errors": errors}), 400
        if incident is None:
            return jsonify({"error": "not_found"}), 404
        return jsonify(incident)

    @blueprint.route("/api/devices/<device_id>/watchdogs", methods=["GET"])
    def device_watchdogs(device_id: str):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = runtime.get_device_watchdogs(device_id, user=user)
        if payload is None:
            return jsonify({"error": "not_found"}), 404
        return jsonify(payload)

    @blueprint.route("/api/devices/<device_id>/watchdogs/overrides", methods=["POST"])
    def update_device_watchdog_override(device_id: str):
        user, error = _require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        result, errors = runtime.upsert_device_override(device_id, payload, user=user)
        if errors:
            return jsonify({"errors": errors}), 400
        return jsonify(result)

    app.register_blueprint(blueprint)
