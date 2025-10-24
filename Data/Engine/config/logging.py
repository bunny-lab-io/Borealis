"""Logging bootstrap helpers for the Borealis Engine."""

from __future__ import annotations

import logging
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import List

from .environment import EngineSettings


_ENGINE_LOGGER_NAME = "borealis.engine"
_SERVICE_NAME = "engine"
_DEFAULT_FORMAT = "%(asctime)s-" + _SERVICE_NAME + "-%(message)s"


def _matching_handlers(logger: logging.Logger, log_path: Path) -> List[TimedRotatingFileHandler]:
    matches: List[TimedRotatingFileHandler] = []
    resolved_target = log_path.resolve()
    for handler in logger.handlers:
        if isinstance(handler, TimedRotatingFileHandler):
            handler_filename = getattr(handler, "baseFilename", None)
            if not handler_filename:
                continue
            handler_path = Path(handler_filename).resolve()
            if handler_path == resolved_target:
                matches.append(handler)
    return matches


def _remove_extra_handlers(logger: logging.Logger, log_path: Path) -> None:
    handlers = _matching_handlers(logger, log_path)
    for redundant in handlers[1:]:
        logger.removeHandler(redundant)
        redundant.close()


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
    log_path = (logs_root / "engine.log").resolve()

    logger = logging.getLogger(_ENGINE_LOGGER_NAME)
    logger.setLevel(logging.INFO if not settings.debug else logging.DEBUG)

    # Always route Engine loggers through the root handler to avoid duplicate
    # file handles that break rotation on Windows.  Remove any legacy
    # file-based handlers left over from previous configurations.
    for handler in _matching_handlers(logger, log_path):
        logger.removeHandler(handler)
        handler.close()
    logger.propagate = True

    # Ensure the root logger owns the rotating handler so third-party modules
    # inherit the same destination without each opening the log file.
    root_logger = logging.getLogger()
    existing_root_handlers = _matching_handlers(root_logger, log_path)
    if not existing_root_handlers:
        handler = _build_handler(log_path)
        root_logger.addHandler(handler)
    else:
        handler = existing_root_handlers[0]
        _remove_extra_handlers(root_logger, log_path)

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
