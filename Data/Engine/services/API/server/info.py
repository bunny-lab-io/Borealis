# ======================================================
# Data\Engine\services\API\server\info.py
# Description: Server information endpoints surfaced for administrative UX.
#
# API Endpoints (if applicable):
# - GET /api/server/time (Operator Session) - Returns the server clock in multiple formats.
# ======================================================

from __future__ import annotations

from datetime import datetime, timezone
from typing import TYPE_CHECKING, Any, Dict

from flask import Blueprint, Flask, jsonify

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def _serialize_time(now_local: datetime, now_utc: datetime) -> Dict[str, Any]:
    tz_label = now_local.tzname()
    display = now_local.strftime("%Y-%m-%d %H:%M:%S %Z").strip()
    if not display:
        display = now_local.isoformat()
    return {
        "epoch": int(now_local.timestamp()),
        "iso": now_local.isoformat(),
        "utc": now_utc.isoformat(),
        "timezone": tz_label,
        "display": display,
    }


def register_info(app: Flask, _adapters: "EngineServiceAdapters") -> None:
    """Expose server telemetry endpoints used by the admin interface."""

    blueprint = Blueprint("engine_server_info", __name__)

    @blueprint.route("/api/server/time", methods=["GET"])
    def server_time() -> Any:
        now_utc = datetime.now(timezone.utc)
        now_local = now_utc.astimezone()
        payload = _serialize_time(now_local, now_utc)
        return jsonify(payload)

    app.register_blueprint(blueprint)
