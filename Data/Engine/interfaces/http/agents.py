"""Agent HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask

from Data.Engine.services.container import EngineServiceContainer


blueprint = Blueprint("engine_agents", __name__, url_prefix="/api/agents")


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach agent management routes to *app*.

    Implementation will be populated as services migrate from the legacy server.
    """

    if "engine_agents" not in app.blueprints:
        app.register_blueprint(blueprint)


__all__ = ["register", "blueprint"]
