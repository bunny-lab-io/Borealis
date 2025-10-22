"""Enrollment HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask


blueprint = Blueprint("engine_enrollment", __name__, url_prefix="/api/enrollment")


def register(app: Flask) -> None:
    """Attach enrollment routes to *app*.

    Implementation will be ported during later migration phases.
    """

    if "engine_enrollment" not in app.blueprints:
        app.register_blueprint(blueprint)


__all__ = ["register", "blueprint"]
