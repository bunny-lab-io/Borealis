# ======================================================
# Data\Engine\services\API\scheduled_jobs\management.py
# Description: Integrates the Engine job scheduler for CRUD operations within the Engine API.
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

import time
from typing import TYPE_CHECKING, List

from ...assemblies.service import AssemblyRuntimeService
from . import job_scheduler

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


def ensure_scheduler(app: "Flask", adapters: "EngineServiceAdapters"):
    """Instantiate the Engine job scheduler and attach it to the Engine context."""

    if getattr(adapters.context, "scheduler", None) is not None:
        return adapters.context.scheduler

    socketio = getattr(adapters.context, "socketio", None)
    if socketio is None:
        raise RuntimeError("Socket.IO instance is required to initialise the scheduled job service.")

    assembly_cache = adapters.context.assembly_cache
    if assembly_cache is None:
        raise RuntimeError("Assembly cache is required to initialise the scheduled job service.")
    assembly_runtime = AssemblyRuntimeService(assembly_cache, logger=adapters.context.logger)

    database_path = adapters.context.database_path
    script_signer = adapters.script_signer

    def _online_hostnames_snapshot() -> List[str]:
        """Return hostnames deemed online based on recent agent heartbeats."""
        threshold = int(time.time()) - 300
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                "SELECT hostname FROM devices WHERE last_seen IS NOT NULL AND last_seen >= ?",
                (threshold,),
            )
            rows = cur.fetchall()
        except Exception as exc:
            adapters.service_log(
                "scheduled_jobs",
                f"online host snapshot lookup failed err={exc}",
                level="ERROR",
            )
            rows = []
        finally:
            try:
                if conn is not None:
                    conn.close()
            except Exception:
                pass

        seen = set()
        hostnames: List[str] = []
        for row in rows or []:
            try:
                raw = row[0] if isinstance(row, (list, tuple)) else row
                name = str(raw or "").strip()
            except Exception:
                name = ""
            if not name:
                continue
            for variant in (name, name.upper(), name.lower()):
                if variant and variant not in seen:
                    seen.add(variant)
                    hostnames.append(variant)
        return hostnames

    scheduler = job_scheduler.register(
        app,
        socketio,
        database_path,
        script_signer=script_signer,
        service_logger=adapters.service_log,
        assembly_runtime=assembly_runtime,
    )
    job_scheduler.set_online_lookup(scheduler, _online_hostnames_snapshot)
    scheduler.start()
    adapters.context.scheduler = scheduler
    adapters.service_log("scheduled_jobs", "engine scheduler initialised", level="INFO")
    return scheduler


def get_scheduler(adapters: "EngineServiceAdapters"):
    scheduler = getattr(adapters.context, "scheduler", None)
    if scheduler is None:
        raise RuntimeError("Scheduled job service has not been initialised.")
    return scheduler


def register_management(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Ensure scheduled job routes are registered via the Engine scheduler."""

    ensure_scheduler(app, adapters)
