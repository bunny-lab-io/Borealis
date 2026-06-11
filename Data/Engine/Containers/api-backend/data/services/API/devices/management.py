# ======================================================
# Data\Engine\services\API\devices\management.py
# Description: Retained internal Socket.IO bridge for Go-owned Agent ingest.
#
# API Endpoints (if applicable):
# - POST /api/internal/agent/status-changed (Internal) - Emits Agent status updates to operators.
# - POST /api/internal/agent/device-event (Internal) - Emits device inventory/service updates to operators.
# ======================================================

"""Internal Agent ingest fanout bridge for the Borealis Engine API."""

from __future__ import annotations

from typing import TYPE_CHECKING

from flask import Blueprint, jsonify, request

from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, validate_internal_token

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


_ALLOWED_DEVICE_EVENTS = {
    "device_inventory_changed",
    "device_services_changed",
}


def register_management(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Register retained internal fanout endpoints."""

    blueprint = Blueprint("devices", __name__)

    def _require_internal() -> bool:
        try:
            secret = require_app_secret(app)
        except Exception:
            return False
        return validate_internal_token(secret, request.headers.get(INTERNAL_TOKEN_HEADER))

    @blueprint.route("/api/internal/agent/status-changed", methods=["POST"])
    def _agent_status_changed():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        body = request.get_json(silent=True) or {}
        event = body.get("event")
        if not isinstance(event, dict):
            return jsonify({"error": "invalid_payload"}), 400
        socketio = getattr(adapters.context, "socketio", None)
        if socketio is not None:
            try:
                socketio.emit("agent_status_changed", event)
            except Exception:
                adapters.context.logger.debug("Failed to emit agent status event.", exc_info=True)
        return jsonify({"status": "sent"})

    @blueprint.route("/api/internal/agent/device-event", methods=["POST"])
    def _agent_device_event():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        body = request.get_json(silent=True) or {}
        event_name = str(body.get("event_name") or "").strip()
        payload = body.get("payload")
        if event_name not in _ALLOWED_DEVICE_EVENTS or not isinstance(payload, dict):
            return jsonify({"error": "invalid_payload"}), 400
        socketio = getattr(adapters.context, "socketio", None)
        if socketio is not None:
            try:
                socketio.emit(event_name, payload)
            except Exception:
                adapters.context.logger.debug("Failed to emit Agent device event.", exc_info=True)
        return jsonify({"status": "sent"})

    app.register_blueprint(blueprint)
