"""Agent WebSocket namespace wiring for the Engine."""

from __future__ import annotations

from typing import Any

from . import events


def register(socketio: Any, services) -> None:
    """Register agent namespaces on the given Socket.IO *socketio* instance."""

    events.register(socketio, services)


__all__ = ["register"]
