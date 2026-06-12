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
# - POST /api/scheduled_jobs/<int:job_id>/rerun (Token Authenticated) - Queues a fresh immediate occurrence for an enabled job.
# - DELETE /api/scheduled_jobs/<int:job_id> (Token Authenticated) - Deletes a job.
# - GET /api/scheduled_jobs/<int:job_id>/runs (Token Authenticated) - Lists run history for a job.
# - GET /api/scheduled_jobs/<int:job_id>/devices (Token Authenticated) - Summarises device results for a run.
# - DELETE /api/scheduled_jobs/<int:job_id>/runs (Token Authenticated) - Clears run history for a job.
# ======================================================

"""Scheduled job management integration for the Borealis Engine runtime."""
from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
from urllib.parse import quote, urlsplit
from typing import TYPE_CHECKING, List, Mapping, Optional

from flask import jsonify, request

from ...ansible.worker_dispatch import WorkerAnsibleDispatcher
from ...assemblies.service import AssemblyRuntimeService
from ...aegis_cipher import AegisDataCorruptionError, AegisLockedError, AegisSecretResetRequiredError
from ....public_endpoints import public_base_url
from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token, validate_internal_token
from ..workflows import management as workflows_management
from . import job_scheduler

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


_SHARED_ANSIBLE_VPN_READY_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_VPN_READY_TIMEOUT_SECONDS"
_SHARED_ANSIBLE_VPN_READY_POLL_INTERVAL_ENV = "BOREALIS_SHARED_ANSIBLE_VPN_READY_POLL_INTERVAL_SECONDS"
_LEGACY_SHARED_ANSIBLE_VPN_PREP_WAIT_ENV = "BOREALIS_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS"
_LEGACY_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_ENV = "BOREALIS_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS"
_DEFAULT_SHARED_ANSIBLE_VPN_READY_TIMEOUT_SECONDS = 45.0
_DEFAULT_SHARED_ANSIBLE_VPN_READY_POLL_INTERVAL_SECONDS = 0.5
_WORK_KIND_ONBOARDING_RUN = "onboarding_run"
_WORK_KIND_SCHEDULED_RUN = "scheduled_run"
_WORK_KIND_SCHEDULED_WORKFLOW_RUN = "scheduled_workflow_run"


def _scheduler_loop_enabled() -> bool:
    raw_value = str(os.getenv("BOREALIS_SCHEDULED_JOBS_START_LOOP", "1") or "1").strip().lower()
    return raw_value not in {"0", "false", "no", "off"}


def _shared_ansible_vpn_ready_timeout_seconds() -> float:
    raw_value = str(
        os.getenv(_SHARED_ANSIBLE_VPN_READY_TIMEOUT_ENV, "")
        or os.getenv(_LEGACY_SHARED_ANSIBLE_VPN_PREP_WAIT_ENV, "")
        or ""
    ).strip()
    if not raw_value:
        return _DEFAULT_SHARED_ANSIBLE_VPN_READY_TIMEOUT_SECONDS
    try:
        return max(0.0, float(raw_value))
    except Exception:
        return _DEFAULT_SHARED_ANSIBLE_VPN_READY_TIMEOUT_SECONDS


def _shared_ansible_vpn_ready_poll_interval_seconds() -> float:
    raw_value = str(
        os.getenv(_SHARED_ANSIBLE_VPN_READY_POLL_INTERVAL_ENV, "")
        or os.getenv(_LEGACY_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_ENV, "")
        or ""
    ).strip()
    if not raw_value:
        return _DEFAULT_SHARED_ANSIBLE_VPN_READY_POLL_INTERVAL_SECONDS
    try:
        return max(0.1, float(raw_value))
    except Exception:
        return _DEFAULT_SHARED_ANSIBLE_VPN_READY_POLL_INTERVAL_SECONDS


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


def _internal_api_base() -> str:
    configured = str(os.getenv("BOREALIS_INTERNAL_API_BASE_URL") or "").strip()
    if configured:
        return configured.rstrip("/")
    host = str(os.getenv("BOREALIS_GO_API_HOST") or "127.0.0.1").strip() or "127.0.0.1"
    port = str(os.getenv("BOREALIS_GO_API_PORT") or "5000").strip() or "5000"
    return f"http://{host}:{port}"


def _internal_api_json(app: "Flask", path: str, *, payload: Optional[dict] = None, timeout: float = 10.0) -> dict:
    secret = require_app_secret(app)
    body = None
    method = "GET"
    headers = {
        "Accept": "application/json",
        INTERNAL_TOKEN_HEADER: internal_token(secret),
    }
    if payload is not None:
        method = "POST"
        body = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request_obj = urllib.request.Request(
        f"{_internal_api_base()}{path}",
        data=body,
        headers=headers,
        method=method,
    )
    try:
        with urllib.request.urlopen(request_obj, timeout=timeout) as response:
            decoded = json.loads(response.read().decode("utf-8") or "{}")
            return decoded if isinstance(decoded, dict) else {}
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, json.JSONDecodeError):
        return {}


def _internal_api_json_strict(app: "Flask", path: str, *, payload: Optional[dict] = None, timeout: float = 10.0) -> dict:
    secret = require_app_secret(app)
    body = None
    method = "GET"
    headers = {
        "Accept": "application/json",
        INTERNAL_TOKEN_HEADER: internal_token(secret),
    }
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        method = "POST"
        headers["Content-Type"] = "application/json"
    request_obj = urllib.request.Request(
        f"{_internal_api_base()}{path}",
        data=body,
        headers=headers,
        method=method,
    )
    try:
        with urllib.request.urlopen(request_obj, timeout=timeout) as response:
            decoded = json.loads(response.read().decode("utf-8") or "{}")
            return decoded if isinstance(decoded, dict) else {}
    except urllib.error.HTTPError as exc:
        try:
            payload = json.loads(exc.read().decode("utf-8") or "{}")
        except Exception:
            payload = {}
        error_code = str(payload.get("error") or "").strip()
        message = str(payload.get("message") or error_code or exc).strip()
        if exc.code == 423 and error_code == "credential_reset_required":
            raise AegisSecretResetRequiredError(message)
        if exc.code == 423 and error_code == "aegis_locked":
            raise AegisLockedError(message)
        if error_code == "corrupt_secret_store":
            raise AegisDataCorruptionError(message)
        raise RuntimeError(message)
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        raise RuntimeError(str(exc)) from exc


def _vpn_snapshot_ready(snapshot: object, requested_ids: List[str]) -> bool:
    if not requested_ids:
        return True
    if not isinstance(snapshot, dict) or not snapshot:
        return False
    for agent_id in requested_ids:
        payload = snapshot.get(agent_id)
        if not isinstance(payload, dict):
            return False
        if "dispatch_ready" in payload:
            if not bool(payload.get("dispatch_ready")):
                return False
        elif bool(payload.get("recovery_in_progress")) or not bool(payload.get("listener_healthy")):
            return False
    return True


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
    ansible_dispatcher = WorkerAnsibleDispatcher(
        app=app,
        adapters=adapters,
        logger=adapters.context.logger.getChild("ansible.worker_dispatch"),
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
        payload = _internal_api_json(app, "/api/internal/job-scheduler/vpn-sessions", timeout=10.0)
        sessions_payload = payload.get("sessions") if isinstance(payload, dict) else {}
        if isinstance(sessions_payload, dict):
            return {
                str(agent_id or "").strip(): dict(session)
                for agent_id, session in sessions_payload.items()
                if str(agent_id or "").strip() and isinstance(session, dict)
            }
        if payload:
            adapters.service_log(
                "scheduled_jobs",
                "vpn session snapshot lookup returned invalid payload",
                level="ERROR",
            )
        return {}

    def _prepare_vpn_session_snapshot(agent_ids: List[str], required_ports: Optional[List[int] | tuple[int, ...]] = None):
        requested_ids = sorted({str(agent_id or "").strip() for agent_id in (agent_ids or []) if str(agent_id or "").strip()})
        snapshot = _active_vpn_session_snapshot()
        endpoint_host = _public_vpn_endpoint_host()
        if not endpoint_host:
            for payload in snapshot.values():
                endpoint_host = _endpoint_host_from_session_payload(payload)
                if endpoint_host:
                    break
        payload = _internal_api_json(
            app,
            "/api/internal/job-scheduler/vpn-prepare",
            payload={
                "agent_ids": requested_ids,
                "required_ports": list(required_ports or []),
                "endpoint_host": endpoint_host,
                "reason": "shared_ansible_prepare",
                "timeout_seconds": _shared_ansible_vpn_ready_timeout_seconds(),
                "poll_interval_seconds": _shared_ansible_vpn_ready_poll_interval_seconds(),
            },
            timeout=_shared_ansible_vpn_ready_timeout_seconds() + 5.0,
        )
        sessions_payload = payload.get("sessions") if isinstance(payload, dict) else {}
        if isinstance(sessions_payload, dict):
            snapshot = {
                str(agent_id or "").strip(): dict(session)
                for agent_id, session in sessions_payload.items()
                if str(agent_id or "").strip() and isinstance(session, dict)
            }
        if not _vpn_snapshot_ready(snapshot, requested_ids):
            adapters.service_log(
                "scheduled_jobs",
                "vpn dispatch readiness timed out before shared ansible dispatch",
                level="WARNING",
            )
        for agent_id in requested_ids:
            payload = snapshot.get(agent_id)
            if isinstance(payload, dict):
                payload["_requested_start"] = True
        return snapshot

    def _load_decrypted_credential(credential_id: int):
        payload = _internal_api_json_strict(
            app,
            f"/api/internal/job-scheduler/credential/{int(credential_id)}",
            timeout=30.0,
        )
        credential = payload.get("credential") if isinstance(payload, Mapping) else None
        return dict(credential) if isinstance(credential, Mapping) else None

    def _load_service_account(agent_id: str):
        agent_key = str(agent_id or "").strip()
        if not agent_key:
            return None
        payload = _internal_api_json(
            app,
            f"/api/internal/job-scheduler/service-account/{quote(agent_key, safe='')}",
            timeout=30.0,
        )
        service_account = payload.get("service_account") if isinstance(payload, Mapping) else None
        return dict(service_account) if isinstance(service_account, Mapping) else None

    def _scheduler_public_base_url() -> str:
        return str(public_base_url(adapters.context) or "").strip()

    def _enqueue_scheduler_work_item(kind: str, **kwargs):
        payload = dict(kwargs or {})
        payload["kind"] = kind
        result = _internal_api_json_strict(
            app,
            "/api/internal/job-scheduler/work-items",
            payload=payload,
            timeout=30.0,
        )
        try:
            return int(result.get("work_id") or 0) or None
        except Exception:
            return None

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
    job_scheduler.set_server_ansible_runner(scheduler, ansible_dispatcher.queue_run)
    job_scheduler.set_credential_fetcher(scheduler, _load_decrypted_credential)
    job_scheduler.set_service_account_fetcher(scheduler, _load_service_account)
    job_scheduler.set_public_base_url_lookup(scheduler, _scheduler_public_base_url)

    def _enqueue_onboarding_run(**kwargs):
        components = list(kwargs.get("components") or [])
        targets = list(kwargs.get("targets") or [])
        config, config_error = scheduler._onboarding_scope_config(components=components, targets=targets)
        if config_error:
            now = int(time.time())
            conn = adapters.db_conn_factory()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=?,
                           updated_at=?,
                           error=?
                     WHERE id=?
                    """,
                    (
                        job_scheduler.RUN_STATUS_FAILED,
                        now,
                        now,
                        str(config_error),
                        int(kwargs.get("run_row_id") or 0),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None
        payload = dict(kwargs or {})
        payload["site_id"] = config.get("site_id")
        return _enqueue_scheduler_work_item(_WORK_KIND_ONBOARDING_RUN, **payload)

    job_scheduler.set_onboarding_run_dispatcher(scheduler, _enqueue_onboarding_run)

    def _enqueue_scheduled_run(**kwargs):
        return _enqueue_scheduler_work_item(_WORK_KIND_SCHEDULED_RUN, **kwargs)

    def _enqueue_scheduled_workflow(**kwargs):
        return _enqueue_scheduler_work_item(_WORK_KIND_SCHEDULED_WORKFLOW_RUN, **kwargs)

    job_scheduler.set_scheduled_run_dispatcher(scheduler, _enqueue_scheduled_run)
    job_scheduler.set_scheduled_workflow_dispatcher(scheduler, _enqueue_scheduled_workflow)
    emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
    if callable(emit_host_service_event):
        job_scheduler.set_host_service_emitter(scheduler, emit_host_service_event)
    workflow_runtime = workflows_management.ensure_workflow_runtime(app, adapters)
    job_scheduler.set_workflow_run_launcher(scheduler, workflow_runtime.start_run)
    job_scheduler.set_workflow_document_validator(scheduler, workflow_runtime.validate_saved_workflow)
    _register_internal_job_scheduler_routes(app, adapters)
    if _scheduler_loop_enabled():
        scheduler.start()
    else:
        adapters.service_log("scheduled_jobs", "scheduler loop disabled in api-backend; job-scheduler owns ticks", level="INFO")
    adapters.context.scheduler = scheduler
    adapters.service_log("scheduled_jobs", "engine scheduler initialised", level="INFO")
    return scheduler


def _register_internal_job_scheduler_routes(
    app: "Flask",
    adapters: "EngineServiceAdapters",
) -> None:
    if getattr(app, "_borealis_job_scheduler_internal_routes", False):
        return

    def _internal_error(message: str, status: int = 500):
        return jsonify({"error": message}), status

    def _require_internal():
        try:
            secret = require_app_secret(app)
        except Exception:
            return False
        return validate_internal_token(secret, request.headers.get(INTERNAL_TOKEN_HEADER))

    @app.route("/api/internal/job-scheduler/workflow/start", methods=["POST"])
    def _internal_job_scheduler_workflow_start():
        if not _require_internal():
            return _internal_error("unauthorized", 401)
        runtime = getattr(adapters.context, "workflow_runtime", None)
        if runtime is None or not hasattr(runtime, "start_run"):
            return _internal_error("workflow_runtime_unavailable", 503)
        body = request.get_json(silent=True) or {}
        try:
            result = runtime.start_run(**body)
        except Exception as exc:
            return _internal_error(str(exc), 500)
        return jsonify(result if isinstance(result, dict) else {"result": result})

    setattr(app, "_borealis_job_scheduler_internal_routes", True)


def get_scheduler(adapters: "EngineServiceAdapters"):
    scheduler = getattr(adapters.context, "scheduler", None)
    if scheduler is None:
        raise RuntimeError("Scheduled job service has not been initialised.")
    return scheduler


def register_management(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Ensure scheduled job routes are registered via the Engine scheduler."""

    ensure_scheduler(app, adapters)
