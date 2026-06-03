# ======================================================
# Data\Engine\services\API\notifications\management.py
# Description: Notification dispatch endpoint used to surface authenticated toast events to the WebUI via Socket.IO.
#
# API Endpoints (if applicable):
# - POST /api/internal/notifications/broadcast (Internal Token) - Broadcasts Go-originated notification payloads to connected operators.
# ======================================================

"""Notification endpoints for the Borealis Engine runtime."""
from __future__ import annotations

from typing import TYPE_CHECKING, Any, Dict

from flask import Flask, jsonify, request

from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, validate_internal_token

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def register_notifications(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Expose internal notification fanout for Go-owned notification routes."""

    def _broadcast(notification: Dict[str, Any]) -> None:
        socketio = getattr(adapters.context, "socketio", None)
        if not socketio:
            return
        try:
            socketio.emit("borealis_notification", notification)
        except Exception:
            adapters.context.logger.debug("Failed to emit notification payload.", exc_info=True)

    def _require_internal() -> bool:
        try:
            secret = require_app_secret(app)
        except Exception:
            return False
        return validate_internal_token(secret, request.headers.get(INTERNAL_TOKEN_HEADER))

    @app.route("/api/internal/notifications/broadcast", methods=["POST"])
    def internal_broadcast() -> Any:
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        body = request.get_json(silent=True) or {}
        notification = body.get("notification")
        if not isinstance(notification, dict):
            return jsonify({"error": "invalid_payload"}), 400
        _broadcast(notification)
        adapters.service_log(
            "notifications",
            f"Notification broadcast by Go api-backend: {notification.get('title') or 'Notification'}",
        )
        return jsonify({"status": "sent"})

    adapters.service_log("notifications", "Registered notification internal broadcast endpoint.")
