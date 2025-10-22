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
    socketio.on_event("job_status_request", handlers.on_job_status_request)


class _JobEventHandlers:
    def __init__(self, socketio: Any, services: EngineServiceContainer) -> None:
        self._socketio = socketio
        self._services = services
        self._log = logging.getLogger("borealis.engine.ws.jobs")

    def on_quick_job_result(self, data: Optional[dict]) -> None:
        self._log.info("quick-job-result received; scheduler migration pending")
        # Step 10 will introduce full persistence + broadcast logic.

    def on_job_status_request(self, _: Optional[dict]) -> None:
        jobs = self._services.scheduler_service.list_jobs()
        try:
            self._socketio.emit("job_status", {"jobs": jobs})
        except Exception:
            self._log.debug("job-status emit failed")


__all__ = ["register"]
