"""Job management WebSocket event placeholders for the Engine."""

from __future__ import annotations

from typing import Any


def register(socketio: Any) -> None:
    """Register job management namespaces on *socketio*.

    Concrete handlers will be migrated in later phases.
    """

    if socketio is None:  # pragma: no cover - guard
        return
    return


__all__ = ["register"]
