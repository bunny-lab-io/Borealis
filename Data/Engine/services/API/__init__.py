"""API service stubs for the Borealis Engine runtime.

Stage 1 only establishes the package layout. Future stages will populate this
module with blueprint factories that wrap the legacy API helpers.
"""
from __future__ import annotations

from flask import Flask

from ...server import EngineContext


def register_api(app: Flask, context: EngineContext) -> None:
    """Placeholder hook for API blueprint registration.

    Later migration stages will import domain-specific blueprint modules and
    attach them to ``app`` using the shared :class:`EngineContext`. For now we
    simply log the intent so tooling can verify the hook is wired.
    """

    context.logger.debug("Engine API services are not yet implemented.")
