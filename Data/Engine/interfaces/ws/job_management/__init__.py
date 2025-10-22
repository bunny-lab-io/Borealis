"""Job management WebSocket namespace wiring for the Engine."""

from __future__ import annotations

from typing import Any

from . import events


def register(socketio: Any) -> None:
    """Register job management namespaces on the given Socket.IO *socketio*."""

    events.register(socketio)


__all__ = ["register"]
