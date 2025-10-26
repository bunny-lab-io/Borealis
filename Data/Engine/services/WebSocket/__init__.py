"""WebSocket service stubs for the Borealis Engine runtime.

Future stages will move Socket.IO namespaces and event handlers here. Stage 1
only keeps a placeholder so the Engine bootstrapper can stub registration
without touching legacy behaviour.
"""
from __future__ import annotations

from flask_socketio import SocketIO

from ...server import EngineContext


def register_realtime(socket_server: SocketIO, context: EngineContext) -> None:
    """Placeholder hook for Socket.IO namespace registration."""

    context.logger.debug("Engine WebSocket services are not yet implemented.")
