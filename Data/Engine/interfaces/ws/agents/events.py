"""Agent WebSocket event placeholders for the Engine."""

from __future__ import annotations

from typing import Any


def register(socketio: Any) -> None:
    """Register agent-related namespaces on *socketio*.

    The concrete event handlers will be migrated in later phases.
    """

    if socketio is None:  # pragma: no cover - guard
        return
    # Placeholder for namespace registration, e.g. ``socketio.on_namespace(...)``.
    return


__all__ = ["register"]
