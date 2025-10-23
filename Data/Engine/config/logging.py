"""Logging bootstrap helpers for the Borealis Engine."""

from __future__ import annotations

import logging
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path

from .environment import EngineSettings


_ENGINE_LOGGER_NAME = "borealis.engine"
_SERVICE_NAME = "engine"
_DEFAULT_FORMAT = "%(asctime)s-" + _SERVICE_NAME + "-%(message)s"


def _handler_already_attached(logger: logging.Logger, log_path: Path) -> bool:
    for handler in logger.handlers:
        if isinstance(handler, TimedRotatingFileHandler):
            handler_path = Path(getattr(handler, "baseFilename", ""))
            if handler_path == log_path:
                return True
    return False


def _build_handler(log_path: Path) -> TimedRotatingFileHandler:
    handler = TimedRotatingFileHandler(
        log_path,
        when="midnight",
        backupCount=30,
        encoding="utf-8",
    )
    handler.setLevel(logging.INFO)
    handler.setFormatter(logging.Formatter(_DEFAULT_FORMAT))
    return handler


def configure_logging(settings: EngineSettings) -> logging.Logger:
    """Configure a rotating log handler for the Engine."""

    logs_root = settings.logs_root
    logs_root.mkdir(parents=True, exist_ok=True)
    log_path = logs_root / "engine.log"

    logger = logging.getLogger(_ENGINE_LOGGER_NAME)
    logger.setLevel(logging.INFO if not settings.debug else logging.DEBUG)

    if not _handler_already_attached(logger, log_path):
        handler = _build_handler(log_path)
        logger.addHandler(handler)
        logger.propagate = False

    # Also ensure the root logger follows suit so third-party modules inherit the handler.
    root_logger = logging.getLogger()
    if not _handler_already_attached(root_logger, log_path):
        handler = _build_handler(log_path)
        root_logger.addHandler(handler)
        if root_logger.level == logging.WARNING:
            # Default level is WARNING; lower it to INFO so our handler captures application messages.
            root_logger.setLevel(logging.INFO if not settings.debug else logging.DEBUG)

    # Quieten overly chatty frameworks unless debugging is explicitly requested.
    if not settings.debug:
        logging.getLogger("werkzeug").setLevel(logging.WARNING)
        logging.getLogger("engineio").setLevel(logging.WARNING)
        logging.getLogger("socketio").setLevel(logging.WARNING)

    return logger


__all__ = ["configure_logging"]
