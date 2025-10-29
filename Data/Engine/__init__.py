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
"""

from .server import create_app, EngineContext  # re-export for convenience

__all__ = ["create_app", "EngineContext"]
