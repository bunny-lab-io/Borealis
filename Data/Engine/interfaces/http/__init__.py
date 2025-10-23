"""HTTP interface registration for the Borealis Engine."""

from __future__ import annotations

from flask import Flask

from Data.Engine.services.container import EngineServiceContainer

from . import admin, agents, auth, enrollment, github, health, job_management, tokens

_REGISTRARS = (
    health.register,
    agents.register,
    enrollment.register,
    tokens.register,
    job_management.register,
    github.register,
    auth.register,
    admin.register,
)


def register_http_interfaces(app: Flask, services: EngineServiceContainer) -> None:
    """Attach HTTP blueprints to *app*.

    The implementation is intentionally minimal for the initial scaffolding.
    """

    registrars = list(_REGISTRARS)
    if app.config.get("ENGINE_LEGACY_BRIDGE_ACTIVE"):
        registrars = [r for r in registrars if r is not job_management.register]

    for registrar in registrars:
        registrar(app, services)


__all__ = ["register_http_interfaces"]
