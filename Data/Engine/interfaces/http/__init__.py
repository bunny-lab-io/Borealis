"""HTTP interface registration for the Borealis Engine."""

from __future__ import annotations

from flask import Flask

from Data.Engine.services.container import EngineServiceContainer

from . import admin, agents, enrollment, health, job_management, tokens

_REGISTRARS = (
    health.register,
    agents.register,
    enrollment.register,
    tokens.register,
    job_management.register,
    admin.register,
)


def register_http_interfaces(app: Flask, services: EngineServiceContainer) -> None:
    """Attach HTTP blueprints to *app*.

    The implementation is intentionally minimal for the initial scaffolding.
    """

    for registrar in _REGISTRARS:
        registrar(app, services)


__all__ = ["register_http_interfaces"]
