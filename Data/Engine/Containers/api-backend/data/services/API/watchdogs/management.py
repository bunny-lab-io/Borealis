# ======================================================
# Data\Engine\services\API\watchdogs\management.py
# Description: Watchdog policy and incident API endpoints backed by the
#              Engine-native watchdog runtime.
#
# API Endpoints (if applicable):
# - POST /api/watchdogs (Token Authenticated) - Creates a watchdog policy.
# - PUT /api/watchdogs/<int:watchdog_id> (Token Authenticated) - Updates a watchdog policy.
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
