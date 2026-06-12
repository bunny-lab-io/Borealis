# ======================================================
# Data\Engine\services\API\watchdogs\management.py
# Description: Watchdog runtime bootstrap retained for background evaluation.
#
# API Endpoints (if applicable): None
# ======================================================

"""Watchdog policy and incident API registration for the Borealis Engine."""

from __future__ import annotations

from typing import TYPE_CHECKING

from flask import Flask

from .runtime import WatchdogRuntimeService

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def ensure_watchdog_runtime(app: Flask, adapters: "EngineServiceAdapters") -> WatchdogRuntimeService:
    runtime = getattr(adapters.context, "watchdog_runtime", None)
    if runtime is not None:
        return runtime
    runtime = WatchdogRuntimeService(
        db_conn_factory=adapters.db_conn_factory,
        socketio=getattr(adapters.context, "socketio", None),
        service_log=adapters.service_log,
        logger=adapters.context.logger.getChild("watchdogs"),
        assembly_cache=adapters.context.assembly_cache,
        app=app,
        adapters=adapters,
        context=adapters.context,
        github_integration=adapters.github_integration,
        agent_release_manager=getattr(adapters, "agent_release_manager", None),
    )
    runtime.start()
    adapters.context.watchdog_runtime = runtime
    adapters.service_log("watchdogs", "watchdog runtime initialised", level="INFO")
    return runtime


def register_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    ensure_watchdog_runtime(app, adapters)
