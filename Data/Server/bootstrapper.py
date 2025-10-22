"""Bootstrap entry point for the Borealis server."""

from __future__ import annotations

from typing import Tuple

if __package__ in {None, ""}:  # pragma: no cover - allow execution as a script
    import pathlib
    import sys

    current_dir = pathlib.Path(__file__).resolve().parent
    if str(current_dir) not in sys.path:
        sys.path.insert(0, str(current_dir))

from flask import Flask
from flask_socketio import SocketIO

try:  # pragma: no cover - package import path
    from .app import create_app
    from .app.config import BaseConfig, resolve_config
    from .app.logging import configure_logging
    from .app.scheduler import SchedulerCoordinator
    from .app.services import ServiceContainer
except ImportError:  # pragma: no cover - executed when run as script
    from app import create_app
    from app.config import BaseConfig, resolve_config
    from app.logging import configure_logging
    from app.scheduler import SchedulerCoordinator
    from app.services import ServiceContainer


def bootstrap(config_name: str | None = None) -> Tuple[Flask, SocketIO, ServiceContainer, SchedulerCoordinator]:
    """Initialise logging and return the configured app/runtime tuple."""

    config: BaseConfig = resolve_config(config_name)
    configure_logging(config)
    return create_app(config, init_logging=False)


__all__ = ["bootstrap"]
