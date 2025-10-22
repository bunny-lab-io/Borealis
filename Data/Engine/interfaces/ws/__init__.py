"""WebSocket interface factory for the Borealis Engine."""

from __future__ import annotations

from typing import Optional

from flask import Flask

from ...config import SocketIOSettings

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


__all__ = ["create_socket_server"]
