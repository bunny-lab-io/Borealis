"""HTTP builder for configuring the Flask application."""

from __future__ import annotations

from flask import Flask

from ..services import ServiceContainer


def configure_http(app: Flask, services: ServiceContainer) -> None:
    app.config.setdefault("BOREALIS_ENV", services.config.environment)
    app.config.setdefault("JSON_SORT_KEYS", False)


__all__ = ["configure_http"]
