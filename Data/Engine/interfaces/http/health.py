"""Health check HTTP interface for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask, jsonify

from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_health", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach health-related routes to *app*."""

    if "engine_health" not in app.blueprints:
        app.register_blueprint(blueprint)


@blueprint.route("/health", methods=["GET"])
def health() -> object:
    """Return a basic liveness response."""

    return jsonify({"status": "ok"})


__all__ = ["register", "blueprint"]
