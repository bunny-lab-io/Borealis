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

import os
import time
from urllib.parse import urlsplit
from typing import TYPE_CHECKING, List

from ...ansible import EngineAnsibleRunner
from ...assemblies.service import AssemblyRuntimeService
from . import job_scheduler

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


_SHARED_ANSIBLE_VPN_PREP_WAIT_ENV = "BOREALIS_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS"
_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_ENV = "BOREALIS_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS"
_DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS = 10.0
_DEFAULT_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS = 0.5


def _shared_ansible_vpn_prep_wait_seconds() -> float:
    raw_value = str(os.getenv(_SHARED_ANSIBLE_VPN_PREP_WAIT_ENV, "") or "").strip()
    if not raw_value:
        return _DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS
    try:
        return max(0.0, float(raw_value))
    except Exception:
        return _DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS


def _shared_ansible_vpn_prep_poll_interval_seconds() -> float:
    raw_value = str(os.getenv(_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_ENV, "") or "").strip()
    if not raw_value:
        return _DEFAULT_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS
    try:
        return max(0.1, float(raw_value))
    except Exception:
        return _DEFAULT_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS


def _public_vpn_endpoint_host() -> str:
    for env_name in ("BOREALIS_AGENT_PUBLIC_BASE_URL", "BOREALIS_PUBLIC_BASE_URL"):
        raw_value = str(os.getenv(env_name, "") or "").strip()
        if not raw_value:
            continue
        try:
            parsed = urlsplit(raw_value if "://" in raw_value else f"//{raw_value}")
            if parsed.hostname:
                return str(parsed.hostname).strip()
        except Exception:
            pass
    return ""


def _endpoint_host_from_session_payload(payload: object) -> str:
    if not isinstance(payload, dict):
        return ""
    raw_endpoint = str(payload.get("endpoint") or "").strip()
    if not raw_endpoint:
        return ""
    try:
        parsed = urlsplit(f"//{raw_endpoint}")
        if parsed.hostname:
            return str(parsed.hostname).strip()
    except Exception:
        pass
    return raw_endpoint.rsplit(":", 1)[0].strip().strip("[]")


def _vpn_snapshot_ready(snapshot: object, requested_ids: List[str]) -> bool:
    if not requested_ids:
        return True
    if not isinstance(snapshot, dict) or not snapshot:
        return False
    for agent_id in requested_ids:
        payload = snapshot.get(agent_id)
        if not isinstance(payload, dict):
            return False
    sample_payload = next((payload for payload in snapshot.values() if isinstance(payload, dict)), None)
    if not isinstance(sample_payload, dict):
        return False
    if bool(sample_payload.get("recovery_in_progress")):
        return False
    return bool(sample_payload.get("listener_healthy"))


def _bootstrap_vpn_session(
    tunnel_service,
    *,
    agent_id: str,
    endpoint_host: str,
) -> None:
    last_error: Exception | None = None
    for attempt in range(2):
        try:
            tunnel_service.connect(
                agent_id=agent_id,
                operator_id=None,
                endpoint_host=endpoint_host or None,
            )
            return
        except Exception as exc:
            last_error = exc
            if attempt == 0:
                time.sleep(1.0)
                continue
    if last_error is not None:
        raise last_error


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
    ansible_runner = EngineAnsibleRunner(
        socketio=socketio,
        db_conn_factory=adapters.db_conn_factory,
        service_log=adapters.service_log,
        logger=adapters.context.logger.getChild("ansible.runner"),
    )

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

    def _active_vpn_session_snapshot():
        try:
            from ..devices.tunnel import _get_tunnel_service

            tunnel_service = _get_tunnel_service(adapters)
            sessions = tunnel_service.list_sessions() or []
        except Exception as exc:
            adapters.service_log(
                "scheduled_jobs",
                f"vpn session snapshot lookup failed err={exc}",
                level="ERROR",
            )
            sessions = []

        snapshot = {}
        for session in sessions:
            if not isinstance(session, dict):
                continue
            agent_id = str(session.get("agent_id") or "").strip()
            if not agent_id:
                continue
            snapshot[agent_id] = dict(session)
        return snapshot

    def _prepare_vpn_session_snapshot(agent_ids: List[str]):
        try:
            from ..devices.tunnel import _get_tunnel_service

            tunnel_service = _get_tunnel_service(adapters)
        except Exception as exc:
            adapters.service_log(
                "scheduled_jobs",
                f"vpn session preparation unavailable err={exc}",
                level="ERROR",
            )
            return _active_vpn_session_snapshot()

        requested_ids = sorted({str(agent_id or "").strip() for agent_id in (agent_ids or []) if str(agent_id or "").strip()})
        snapshot = _active_vpn_session_snapshot()
        endpoint_host = _public_vpn_endpoint_host()
        if not endpoint_host:
            for payload in snapshot.values():
                endpoint_host = _endpoint_host_from_session_payload(payload)
                if endpoint_host:
                    break
        requested_start = False
        for agent_id in requested_ids:
            try:
                session_payload = tunnel_service.session_payload(agent_id, include_token=False)
                if session_payload:
                    tunnel_service.request_agent_start(agent_id)
                else:
                    _bootstrap_vpn_session(
                        tunnel_service,
                        agent_id=agent_id,
                        endpoint_host=endpoint_host,
                    )
                requested_start = True
            except Exception as exc:
                adapters.service_log(
                    "scheduled_jobs",
                    f"vpn session prime failed agent_id={agent_id} endpoint_host={endpoint_host or '-'} err={exc}",
                    level="WARNING",
                )
        snapshot = _active_vpn_session_snapshot()
        if requested_start:
            deadline = time.monotonic() + _shared_ansible_vpn_prep_wait_seconds()
            poll_interval = _shared_ansible_vpn_prep_poll_interval_seconds()
            while True:
                snapshot = _active_vpn_session_snapshot()
                if _vpn_snapshot_ready(snapshot, requested_ids):
                    break
                if time.monotonic() >= deadline:
                    adapters.service_log(
                        "scheduled_jobs",
                        "vpn preparation readiness timed out before shared ansible dispatch",
                        level="WARNING",
                    )
                    break
                time.sleep(poll_interval)
        for agent_id in requested_ids:
            payload = snapshot.get(agent_id)
            if isinstance(payload, dict):
                payload["_requested_start"] = True
        return snapshot

    scheduler = job_scheduler.register(
        app,
        socketio,
        adapters.db_conn_factory,
        script_signer=script_signer,
        service_logger=adapters.service_log,
        assembly_runtime=assembly_runtime,
    )
    job_scheduler.set_online_lookup(scheduler, _online_hostnames_snapshot)
    job_scheduler.set_vpn_session_lookup(scheduler, _active_vpn_session_snapshot)
    job_scheduler.set_vpn_session_prepare(scheduler, _prepare_vpn_session_snapshot)
    job_scheduler.set_server_ansible_runner(scheduler, ansible_runner.queue_run)
    emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
    if callable(emit_host_service_event):
        job_scheduler.set_host_service_emitter(scheduler, emit_host_service_event)
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
