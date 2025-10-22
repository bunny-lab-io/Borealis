"""Scheduler coordination helpers for the modular server bootstrap."""

from __future__ import annotations

from flask_socketio import SocketIO

from ..services import ServiceContainer


class SchedulerCoordinator:
    """Thin wrapper around the active scheduler instance."""

    def __init__(self, scheduler: object | None) -> None:
        self._scheduler = scheduler

    def start(self) -> None:
        start = getattr(self._scheduler, "start", None)
        if callable(start):
            start()

    def stop(self) -> None:
        stop = getattr(self._scheduler, "stop", None)
        if callable(stop):
            stop()

    def __bool__(self) -> bool:  # pragma: no cover - trivial
        return self._scheduler is not None


def build_scheduler(_: ServiceContainer, __: SocketIO, scheduler: object | None) -> SchedulerCoordinator:
    return SchedulerCoordinator(scheduler)


__all__ = ["SchedulerCoordinator", "build_scheduler"]
