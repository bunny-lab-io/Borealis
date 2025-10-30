# ======================================================
# Data\Engine\services\API\scheduled_jobs\management.py
# Description: Integrates the legacy job scheduler for CRUD operations within the Engine API.
#
# API Endpoints (if applicable):
# - GET /api/scheduled_jobs (Token Authenticated) - Lists scheduled jobs with summary metadata.
# - POST /api/scheduled_jobs (Token Authenticated) - Creates a new scheduled job definition.
# - GET /api/scheduled_jobs/<int:job_id> (Token Authenticated) - Retrieves a scheduled job.
# - PUT /api/scheduled_jobs/<int:job_id> (Token Authenticated) - Updates a scheduled job.
# - POST /api/scheduled_jobs/<int:job_id>/toggle (Token Authenticated) - Enables or disables a job.
# - DELETE /api/scheduled_jobs/<int:job_id> (Token Authenticated) - Deletes a job.
# - GET /api/scheduled_jobs/<int:job_id>/runs (Token Authenticated) - Lists run history for a job.
# - GET /api/scheduled_jobs/<int:job_id>/devices (Token Authenticated) - Summarises device results for a run.
# - DELETE /api/scheduled_jobs/<int:job_id>/runs (Token Authenticated) - Clears run history for a job.
# ======================================================

"""Scheduled job management integration for the Borealis Engine runtime."""
from __future__ import annotations

from typing import TYPE_CHECKING, Any

try:  # pragma: no cover - legacy module import guard
    import job_scheduler as legacy_job_scheduler  # type: ignore
except Exception as exc:  # pragma: no cover - runtime guard
    legacy_job_scheduler = None  # type: ignore
    _SCHEDULER_IMPORT_ERROR = exc
else:
    _SCHEDULER_IMPORT_ERROR = None

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


def _raise_scheduler_import() -> None:
    if _SCHEDULER_IMPORT_ERROR is not None:
        raise RuntimeError(
            "Legacy job scheduler module could not be imported; ensure Data/Server/job_scheduler.py "
            "remains available during the Engine migration."
        ) from _SCHEDULER_IMPORT_ERROR


def ensure_scheduler(app: "Flask", adapters: "EngineServiceAdapters"):
    """Instantiate the legacy job scheduler and attach it to the Engine context."""

    if getattr(adapters.context, "scheduler", None) is not None:
        return adapters.context.scheduler

    _raise_scheduler_import()

    socketio = getattr(adapters.context, "socketio", None)
    if socketio is None:
        raise RuntimeError("Socket.IO instance is required to initialise the scheduled job service.")

    database_path = adapters.context.database_path
    script_signer = adapters.script_signer

    scheduler = legacy_job_scheduler.register(
        app,
        socketio,
        database_path,
        script_signer=script_signer,
    )
    scheduler.start()
    adapters.context.scheduler = scheduler
    adapters.service_log("scheduled_jobs", "legacy scheduler initialised", level="INFO")
    return scheduler


def get_scheduler(adapters: "EngineServiceAdapters"):
    scheduler = getattr(adapters.context, "scheduler", None)
    if scheduler is None:
        raise RuntimeError("Scheduled job service has not been initialised.")
    return scheduler


def register_management(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Ensure scheduled job routes are registered via the legacy scheduler."""

    ensure_scheduler(app, adapters)

