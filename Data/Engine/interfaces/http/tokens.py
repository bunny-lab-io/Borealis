"""Token management HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask


blueprint = Blueprint("engine_tokens", __name__, url_prefix="/api/tokens")


def register(app: Flask) -> None:
    """Attach token management routes to *app*.

    Implementation will be introduced as authentication services are migrated.
    """

    if "engine_tokens" not in app.blueprints:
        app.register_blueprint(blueprint)


__all__ = ["register", "blueprint"]
