"""Job management WebSocket event handlers."""

from __future__ import annotations

import logging
from typing import Any, Optional

from Data.Engine.services.container import EngineServiceContainer


def register(socketio: Any, services: EngineServiceContainer) -> None:
    if socketio is None:  # pragma: no cover - guard
        return

    handlers = _JobEventHandlers(socketio, services)
    socketio.on_event("quick_job_result", handlers.on_quick_job_result)


class _JobEventHandlers:
    def __init__(self, socketio: Any, services: EngineServiceContainer) -> None:
        self._socketio = socketio
        self._services = services
        self._log = logging.getLogger("borealis.engine.ws.jobs")

    def on_quick_job_result(self, data: Optional[dict]) -> None:
        self._log.info("quick-job-result received; scheduler migration pending")
        # Step 10 will introduce full persistence + broadcast logic.


__all__ = ["register"]
