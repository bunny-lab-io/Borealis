"""Administrative HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask

from Data.Engine.services.container import EngineServiceContainer


blueprint = Blueprint("engine_admin", __name__, url_prefix="/api/admin")


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach administrative routes to *app*.

    Concrete endpoints will be migrated in subsequent phases.
    """

    if "engine_admin" not in app.blueprints:
        app.register_blueprint(blueprint)


__all__ = ["register", "blueprint"]
