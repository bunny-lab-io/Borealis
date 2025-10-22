"""Health check HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask


blueprint = Blueprint("engine_health", __name__)


def register(app: Flask) -> None:
    """Attach health-related routes to *app*.

    Routes will be populated in later migration phases.
    """

    if "engine_health" not in app.blueprints:
        app.register_blueprint(blueprint)


__all__ = ["register", "blueprint"]
