"""Tests for the Engine logging configuration helpers."""

from __future__ import annotations

import logging
from pathlib import Path
from typing import List

from logging.handlers import TimedRotatingFileHandler

from Data.Engine.config.logging import configure_logging


def _collect_handlers(logger: logging.Logger, log_path: Path) -> List[TimedRotatingFileHandler]:
    resolved_target = log_path.resolve()
    matches: List[TimedRotatingFileHandler] = []
    for handler in logger.handlers:
        if isinstance(handler, TimedRotatingFileHandler):
            handler_path = getattr(handler, "baseFilename", None)
            if not handler_path:
                continue
            if Path(handler_path).resolve() == resolved_target:
                matches.append(handler)
    return matches


def _cleanup_handlers(log_path: Path) -> None:
    resolved_target = log_path.resolve()
    for logger in (logging.getLogger(), logging.getLogger("borealis.engine")):
        for handler in list(logger.handlers):
            if isinstance(handler, TimedRotatingFileHandler):
                handler_path = getattr(handler, "baseFilename", None)
                if handler_path and Path(handler_path).resolve() == resolved_target:
                    logger.removeHandler(handler)
                    handler.close()


def test_configure_logging_attaches_single_root_handler(engine_settings) -> None:
    settings = engine_settings
    log_path = (settings.logs_root / "engine.log").resolve()

    root_logger = logging.getLogger()
    previous_level = root_logger.level
    try:
        logger = configure_logging(settings)
        assert logger.name == "borealis.engine"

        root_handlers = _collect_handlers(root_logger, log_path)
        assert len(root_handlers) == 1

        engine_handlers = _collect_handlers(logger, log_path)
        assert engine_handlers == []
        assert logger.propagate is True
    finally:
        root_logger.setLevel(previous_level)
        _cleanup_handlers(log_path)


def test_configure_logging_is_idempotent(engine_settings) -> None:
    settings = engine_settings
    log_path = (settings.logs_root / "engine.log").resolve()

    root_logger = logging.getLogger()
    previous_level = root_logger.level
    try:
        configure_logging(settings)
        first_handler = _collect_handlers(root_logger, log_path)[0]

        configure_logging(settings)
        handlers_after = _collect_handlers(root_logger, log_path)
        assert handlers_after == [first_handler]
    finally:
        root_logger.setLevel(previous_level)
        _cleanup_handlers(log_path)
