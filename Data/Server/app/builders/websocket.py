"""WebSocket builder for configuring Socket.IO namespaces."""

from __future__ import annotations

from flask_socketio import SocketIO

from ..services import ServiceContainer


def configure_websockets(socketio: SocketIO, services: ServiceContainer) -> None:
    socketio.server_options.setdefault("cors_allowed_origins", "*")
    socketio.logger.debug("Socket.IO configured for %s", services.config.environment)


__all__ = ["configure_websockets"]
