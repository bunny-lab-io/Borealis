"""WebUI service stubs for the Borealis Engine runtime.

The future WebUI migration will centralise static asset serving, template
rendering, and dev-server proxying here. Stage 1 keeps the placeholder so the
application factory can stub out registration calls.
"""
from __future__ import annotations

from flask import Flask

from ...server import EngineContext


def register_web_ui(app: Flask, context: EngineContext) -> None:
    """Placeholder hook for WebUI route registration."""

    context.logger.debug("Engine WebUI services are not yet implemented.")
