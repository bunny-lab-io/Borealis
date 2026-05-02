# ======================================================
# Data\Engine\__init__.py
# Description: Package initializer exposing create_app and EngineContext for the Borealis Engine.
#
# API Endpoints (if applicable): None
# ======================================================

"""Borealis Engine runtime package.

This package houses the next-generation server runtime that will gradually
replace :mod:`Data.Server.server`. Stage 1 delivered the structural skeleton
for the Flask/Socket.IO factory; Stage 2 layers in configuration loading and
logging parity via :mod:`Data.Engine.config` so Engine launches honour the
same environment variables and log destinations as the legacy server.

The package intentionally avoids importing the full Engine server stack at
module import time. Lightweight helpers such as ``Data.Engine.edge_runtime``
need to run from shell tooling before optional runtime dependencies are fully
available.
"""

from __future__ import annotations

from typing import Any

__all__ = ["create_app", "EngineContext"]


def __getattr__(name: str) -> Any:
    if name in {"create_app", "EngineContext"}:
        from .server import EngineContext, create_app

        exports = {
            "create_app": create_app,
            "EngineContext": EngineContext,
        }
        return exports[name]
    raise AttributeError(name)
