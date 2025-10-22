"""Application factory for the Borealis server."""

from __future__ import annotations

from typing import Tuple

from flask import Flask
from flask_socketio import SocketIO

from .builders import configure_runtime
from .config import BaseConfig, resolve_config
from .runtime import ServerRuntime, load_runtime
from .logging import configure_logging
from .scheduler import SchedulerCoordinator, build_scheduler
from .services import ServiceContainer


def create_app(
    config: BaseConfig | None = None,
    *,
    init_logging: bool = False,
) -> Tuple[Flask, SocketIO, ServiceContainer, SchedulerCoordinator]:
    """Instantiate the Borealis server runtime."""

    resolved = config or resolve_config(None)
    if init_logging:
        configure_logging(resolved)

    services = ServiceContainer(resolved)
    runtime: ServerRuntime = load_runtime()
    configure_runtime(runtime, services)
    scheduler = build_scheduler(services, runtime.socketio, runtime.scheduler)

    runtime.app.logger.debug("Borealis app created for %s", resolved.environment)
    return runtime.app, runtime.socketio, services, scheduler


__all__ = ["create_app"]
