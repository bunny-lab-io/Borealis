"""Bootstrap entry point for the Borealis server."""

from __future__ import annotations

from typing import Tuple

from flask import Flask
from flask_socketio import SocketIO

from .app import create_app
from .app.config import BaseConfig, resolve_config
from .app.logging import configure_logging
from .app.scheduler import SchedulerCoordinator
from .app.services import ServiceContainer


def bootstrap(config_name: str | None = None) -> Tuple[Flask, SocketIO, ServiceContainer, SchedulerCoordinator]:
    """Initialise logging and return the configured app/runtime tuple."""

    config: BaseConfig = resolve_config(config_name)
    configure_logging(config)
    return create_app(config, init_logging=False)


__all__ = ["bootstrap"]
