# ======================================================
# Data\Engine\services\__init__.py
# Description: Aggregates API, WebSocket, and WebUI service registration helpers for the Engine runtime.
#
# API Endpoints (if applicable): None
# ======================================================

"""Service registration hooks for the Borealis Engine runtime."""

from __future__ import annotations

import importlib
from typing import Any

__all__ = ["API", "WebSocket", "WebUI"]


def __getattr__(name: str) -> Any:
    if name in __all__:
        return importlib.import_module(f"{__name__}.{name}")
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
