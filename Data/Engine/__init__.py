# ======================================================
# Data\Engine\__init__.py
# Description: Package shim that exposes container-owned Engine source for local tooling.
#
# API Endpoints (if applicable): None
# ======================================================

"""Borealis Engine package shim.

Committed Engine runtime source now lives under the owning container source
tree. Local tests and tools still import ``Data.Engine.*`` through this
package path so container packaging and developer imports stay aligned.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

_CONTAINER_ENGINE_DATA = Path(__file__).resolve().parent / "Containers" / "api-backend" / "data"
if _CONTAINER_ENGINE_DATA.is_dir():
    container_data_path = str(_CONTAINER_ENGINE_DATA)
    if container_data_path not in __path__:
        __path__.append(container_data_path)

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
