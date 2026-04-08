# ======================================================
# Data\Engine\services\API\notifications\management.py
# Description: Notification dispatch endpoint used to surface authenticated toast events to the WebUI via Socket.IO.
#
# API Endpoints (if applicable):
# - POST /api/notifications/notify (Token Authenticated) - Broadcasts a transient notification payload to connected operators.
# ======================================================

"""Notification endpoints for the Borealis Engine runtime."""
from __future__ import annotations

import time
from typing import TYPE_CHECKING, Any, Dict

from flask import Blueprint, Flask, jsonify, request

from ...auth import RequestAuthContext

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def _clean_text(value: Any, fallback: str = "") -> str:
    text = fallback
    if isinstance(value, str):
        text = value.strip() or fallback
    elif value is not None:
        try:
            text = str(value).strip() or fallback
        except Exception:
            text = fallback
    return text


def register_notifications(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Expose an authenticated notification endpoint that fans out to WebSocket clients."""

    blueprint = Blueprint("notifications", __name__, url_prefix="/api/notifications")
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )

    def _broadcast(notification: Dict[str, Any]) -> None:
        socketio = getattr(adapters.context, "socketio", None)
        if not socketio:
            return
        try:
            socketio.emit("borealis_notification", notification)
        except Exception:
            adapters.context.logger.debug("Failed to emit notification payload.", exc_info=True)

    @blueprint.route("/notify", methods=["POST"])
    def notify() -> Any:
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]

        data = request.get_json(silent=True) or {}
        title = _clean_text(data.get("title"), "Notification")
        message = _clean_text(data.get("message"))
        icon = _clean_text(data.get("icon"))
        variant_raw = _clean_text(data.get("variant") or data.get("type") or data.get("severity") or "info", "info")
        variant = variant_raw.lower()
        if variant not in {"info", "warning", "error"}:
            variant = "info"

        if not message:
            return (
                jsonify(
                    {
                        "error": "invalid_payload",
                        "message": "Notification message is required.",
                    }
                ),
                400,
            )

        now_ts = int(time.time())
        payload = {
            "id": f"notif-{now_ts}-{int(time.time() * 1000) % 1000}",
            "title": title,
            "message": message,
            "icon": icon or "NotificationsActive",
            "variant": variant,
            "username": user.get("username"),
            "role": user.get("role") or "User",
            "created_at": now_ts,
        }

        _broadcast(payload)
        adapters.service_log("notifications", f"Notification sent by {user.get('username')}: {title}")
        return jsonify({"status": "sent", "notification": payload})

    app.register_blueprint(blueprint)
    adapters.service_log("notifications", "Registered notification endpoints.")
