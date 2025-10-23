"""Server metadata endpoints."""

from __future__ import annotations

from datetime import datetime, timezone

from flask import Blueprint, Flask, jsonify

from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_server_info", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_server_info" not in app.blueprints:
        app.register_blueprint(blueprint)


@blueprint.route("/api/server/time", methods=["GET"])
def server_time() -> object:
    now_local = datetime.now().astimezone()
    now_utc = datetime.now(timezone.utc)
    tzinfo = now_local.tzinfo
    offset = tzinfo.utcoffset(now_local) if tzinfo else None

    def _ordinal(n: int) -> str:
        if 11 <= (n % 100) <= 13:
            suffix = "th"
        else:
            suffix = {1: "st", 2: "nd", 3: "rd"}.get(n % 10, "th")
        return f"{n}{suffix}"

    month = now_local.strftime("%B")
    day_disp = _ordinal(now_local.day)
    year = now_local.strftime("%Y")
    hour24 = now_local.hour
    hour12 = hour24 % 12 or 12
    minute = now_local.minute
    ampm = "AM" if hour24 < 12 else "PM"
    display = f"{month} {day_disp} {year} @ {hour12}:{minute:02d}{ampm}"

    payload = {
        "epoch": int(now_local.timestamp()),
        "iso": now_local.isoformat(),
        "utc_iso": now_utc.isoformat().replace("+00:00", "Z"),
        "timezone": str(tzinfo) if tzinfo else "",
        "offset_seconds": int(offset.total_seconds()) if offset else 0,
        "display": display,
    }
    return jsonify(payload)


__all__ = ["register", "blueprint"]
