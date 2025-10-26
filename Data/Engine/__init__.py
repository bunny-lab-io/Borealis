"""Borealis Engine runtime package.

This package houses the next-generation server runtime that will gradually
replace :mod:`Data.Server.server`. Stage 1 focuses on providing a skeleton
application factory and service placeholders so later stages can port
features incrementally.
"""

from .server import create_app, EngineContext  # re-export for convenience

__all__ = ["create_app", "EngineContext"]
