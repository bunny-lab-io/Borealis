"""WebSocket interface factory for the Borealis Engine."""

from __future__ import annotations

from typing import Any, Optional

from flask import Flask

from ...config import SocketIOSettings
from .agents import register as register_agent_events
from .job_management import register as register_job_events

try:  # pragma: no cover - import guard
    from flask_socketio import SocketIO
except Exception:  # pragma: no cover - optional dependency
    SocketIO = None  # type: ignore[assignment]


def create_socket_server(app: Flask, settings: SocketIOSettings) -> Optional[SocketIO]:
    """Create a Socket.IO server bound to *app* if dependencies are available."""

    if SocketIO is None:
        return None

    cors_allowed = settings.cors_allowed_origins or ("*",)
    socketio = SocketIO(
        app,
        cors_allowed_origins=cors_allowed,
        async_mode=None,
        logger=False,
        engineio_logger=False,
    )
    return socketio


def register_ws_interfaces(socketio: Any) -> None:
    """Attach placeholder namespaces for the Engine Socket.IO server."""

    if socketio is None:  # pragma: no cover - guard
        return

    for registrar in (register_agent_events, register_job_events):
        registrar(socketio)


__all__ = ["create_socket_server", "register_ws_interfaces"]
