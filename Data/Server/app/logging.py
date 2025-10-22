"""Logging bootstrap utilities for the Borealis server."""

from __future__ import annotations

import logging
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Iterable

from .config import BaseConfig

_LOG_FORMAT = "%(asctime)s-%(name)s-%(message)s"
_DATE_FORMAT = "%Y-%m-%d %H:%M:%S"


def _logs_root() -> Path:
    """Return the root folder for server logs, creating it if needed."""

    # ``Data/Server/app/logging.py`` → project root two parents up from ``Data``.
    base = Path(__file__).resolve()
    for candidate in (
        base.parents[3] / "Logs" / "Server",
        base.parents[2] / "Logs" / "Server",
    ):
        try:
            candidate.mkdir(parents=True, exist_ok=True)
        except Exception:
            continue
        else:
            return candidate
    raise RuntimeError("Unable to determine log output directory for Borealis server")


def _remove_existing_handlers(handlers: Iterable[logging.Handler]) -> None:
    for handler in list(handlers):
        try:
            handler.close()
        finally:
            logging.getLogger().removeHandler(handler)


def configure_logging(config: BaseConfig) -> Path:
    """Configure root logging with daily rotation.

    Returns the path to the server log file for convenience.
    """

    log_path = _logs_root() / config.log_file
    root_logger = logging.getLogger()
    if root_logger.handlers:
        _remove_existing_handlers(root_logger.handlers)

    handler = TimedRotatingFileHandler(
        log_path,
        when="midnight",
        backupCount=0,
        encoding="utf-8",
    )
    formatter = logging.Formatter(_LOG_FORMAT, _DATE_FORMAT)
    handler.setFormatter(formatter)

    root_logger.addHandler(handler)
    console = logging.StreamHandler()
    console.setFormatter(formatter)
    root_logger.addHandler(console)

    root_logger.setLevel(getattr(logging, config.log_level.upper(), logging.INFO))
    logging.captureWarnings(True)
    root_logger.debug("Logging initialised at %s", log_path)
    return log_path
