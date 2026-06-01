"""Ephemeral site worker process."""

from __future__ import annotations

import os
import threading
import time
from typing import Any, Dict, Mapping, Optional

import requests

from Data.Engine import database
from Data.Engine.config import initialise_engine_logger, load_runtime_config
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.security import signing
from Data.Engine.services.ansible import EngineAnsibleRunner
from Data.Engine.services.assemblies.service import AssemblyRuntimeService
from Data.Engine.assembly_management import initialise_assembly_runtime
from Data.Engine.services.API.scheduled_jobs import job_scheduler

from .queue import (
    LANE_ONBOARDING,
    LANE_SCHEDULED_JOB,
    WORK_STATUS_FAILED,
    WORK_STATUS_SUCCEEDED,
    WORK_KIND_ONBOARDING_RUN,
    WORK_KIND_AGENT_MAINTENANCE_RUN,
    WORK_KIND_SCHEDULED_RUN,
    WORK_KIND_SCHEDULED_WORKFLOW_RUN,
    WORKER_STATUS_IDLE,
    WORKER_STATUS_RUNNING,
    claim_next_work_item,
    complete_work_item,
    heartbeat_work_item,
    heartbeat_worker,
    register_worker,
    requeue_work_item,
    site_worker_remote_desktop_port,
    site_worker_remote_ops_port,
    stop_worker,
)
from .runtime_settings import load_site_worker_settings
from .security import INTERNAL_TOKEN_HEADER, internal_token
from .worker_socket import SiteWorkerSocketRuntime

DEFAULT_RUN_WAIT_POLL_SECONDS = 2.0
DEFAULT_TRANSIENT_RUN_RETRY_ATTEMPTS = 8
DEFAULT_TRANSIENT_RUN_RETRY_DELAY_SECONDS = 30
TRANSIENT_RUN_RETRY_REASONS = {
    "wireguard_unavailable",
    "wireguard_session_not_ready",
    job_scheduler.RESOLUTION_REASON_WIREGUARD_NOT_READY,
    job_scheduler.RESOLUTION_REASON_REMOTE_PREFLIGHT_FAILED,
}
LANE_AGENT_SOCKETS = "agent_sockets"


class _WorkerApp:
    def __init__(self, *, logger, secret_key: str) -> None:
        self.logger = logger
        self.secret_key = secret_key


class _NoopSocketIO:
    def start_background_task(self, target, *args, **kwargs):
        thread = threading.Thread(target=target, args=args, kwargs=kwargs, daemon=True)
        thread.start()
        return thread

    def emit(self, *_args, **_kwargs) -> None:
        return None


def _now_ts() -> int:
    return int(time.time())


def _non_negative_float_env(name: str, default: float) -> float:
    raw = str(os.environ.get(name) or "").strip()
    if not raw:
        return default
    try:
        return max(0.0, float(raw))
    except Exception:
        return default


def _positive_int_env(name: str, default: int) -> int:
    raw = str(os.environ.get(name) or "").strip()
    if not raw:
        return default
    try:
        return max(1, int(raw))
    except Exception:
        return default


def _db_factory(database_url: str):
    def _factory():
        return sqlite3.connect(database_url, timeout=30)

    return _factory


def _api_base_url() -> str:
    return str(os.environ.get("BOREALIS_INTERNAL_API_BASE_URL") or "http://127.0.0.1:5000").rstrip("/")


def _headers(secret: str) -> Dict[str, str]:
    return {INTERNAL_TOKEN_HEADER: internal_token(secret)}


def _fetch_credential(secret: str, credential_id: int) -> Dict[str, Any]:
    response = requests.get(
        f"{_api_base_url()}/api/internal/job-scheduler/credential/{int(credential_id)}",
        headers=_headers(secret),
        timeout=30.0,
    )
    response.raise_for_status()
    payload = response.json()
    credential = payload.get("credential") if isinstance(payload, Mapping) else None
    if not isinstance(credential, Mapping):
        raise RuntimeError("credential payload unavailable")
    return dict(credential)


def _get_internal(secret: str, path: str, *, timeout: float = 15.0) -> Dict[str, Any]:
    response = requests.get(f"{_api_base_url()}{path}", headers=_headers(secret), timeout=timeout)
    response.raise_for_status()
    payload = response.json()
    return dict(payload or {}) if isinstance(payload, Mapping) else {}


def _post_internal(secret: str, path: str, payload: Mapping[str, Any], *, timeout: float = 15.0) -> Dict[str, Any]:
    response = requests.post(f"{_api_base_url()}{path}", headers=_headers(secret), json=dict(payload or {}), timeout=timeout)
    response.raise_for_status()
    parsed = response.json()
    return dict(parsed or {}) if isinstance(parsed, Mapping) else {}


def _positive_ints(values) -> list[int]:
    results = []
    for value in values or []:
        try:
            parsed = int(value)
        except Exception:
            continue
        if parsed > 0:
            results.append(parsed)
    return results


def _public_base_url(settings, secret: str) -> str:
    try:
        response = requests.get(
            f"{_api_base_url()}/api/internal/job-scheduler/public-base-url",
            headers=_headers(secret),
            timeout=10.0,
        )
        response.raise_for_status()
        payload = response.json()
        value = str(payload.get("public_base_url") or "").strip() if isinstance(payload, Mapping) else ""
        if value:
            return value
    except Exception:
        pass
    return str(settings.public_base_url or "").strip()


def _service_log(logger):
    def _log(service: str, message: str, scope=None, level: str = "INFO") -> None:
        numeric = getattr(__import__("logging"), str(level or "INFO").upper(), None)
        try:
            logger.log(numeric or 20, "[service:%s]%s %s", service, f"[{scope}]" if scope else "", message)
        except Exception:
            pass

    return _log


def _remote_ops_route_metadata(*, worker_guid: str, host: str, port: int, remote_desktop_port: int = 0) -> Dict[str, Any]:
    return {
        "remote_ops_socket": {
            "host": str(host or "127.0.0.1"),
            "path": "/socket.io/",
            "port": int(port or 0),
            "worker_guid": str(worker_guid or ""),
        },
        "remote_desktop_guacamole": {
            "host": str(host or "127.0.0.1"),
            "scheme": "http",
            "path": "/remote-desktop/vnc/guacamole",
            "path_prefix": "/remote-desktop/vnc",
            "port": int(remote_desktop_port or 0),
            "worker_guid": str(worker_guid or ""),
        },
    }


def _build_worker_scheduler(settings, logger, db_factory, *, socket_runtime: Optional[SiteWorkerSocketRuntime] = None):
    assembly_cache = initialise_assembly_runtime(logger=logger, config=settings.as_dict())
    assembly_cache.reload()
    assembly_runtime = AssemblyRuntimeService(assembly_cache, logger=logger)
    try:
        script_signer = signing.load_signer()
    except Exception:
        script_signer = None
    scheduler = job_scheduler.register(
        _WorkerApp(logger=logger, secret_key=settings.secret_key),
        _NoopSocketIO(),
        db_factory,
        script_signer=script_signer,
        service_logger=_service_log(logger),
        assembly_runtime=assembly_runtime,
        register_routes=False,
    )
    secret = str(settings.secret_key or "")
    job_scheduler.set_credential_fetcher(scheduler, lambda credential_id: _fetch_credential(secret, int(credential_id)))
    job_scheduler.set_public_base_url_lookup(scheduler, lambda: _public_base_url(settings, secret))
    if socket_runtime is not None:
        job_scheduler.set_host_service_emitter(scheduler, socket_runtime.emit_host_service_event)
    else:
        job_scheduler.set_host_service_emitter(
            scheduler,
            lambda hostname, service_mode, event_name, payload: bool(
                _post_internal(
                    secret,
                    "/api/internal/job-scheduler/host-service-event",
                    {
                        "hostname": hostname,
                        "service_mode": service_mode,
                        "event_name": event_name,
                        "payload": payload,
                    },
                    timeout=20.0,
                ).get("emitted")
            ),
        )
    job_scheduler.set_workflow_run_launcher(
        scheduler,
        lambda **kwargs: _post_internal(secret, "/api/internal/job-scheduler/workflow/start", kwargs, timeout=30.0),
    )
    job_scheduler.set_vpn_session_lookup(
        scheduler,
        lambda: dict(_get_internal(secret, "/api/internal/job-scheduler/vpn-sessions", timeout=15.0).get("sessions") or {}),
    )
    job_scheduler.set_vpn_session_prepare(
        scheduler,
        lambda agent_ids, required_ports=None: dict(
            _post_internal(
                secret,
                "/api/internal/job-scheduler/vpn-prepare",
                {
                    "agent_ids": [str(item) for item in (agent_ids or []) if str(item).strip()],
                    "required_ports": _positive_ints(required_ports or []),
                },
                timeout=90.0,
            ).get("sessions") or {}
        ),
    )
    ansible_runner = EngineAnsibleRunner(
        socketio=_NoopSocketIO(),
        db_conn_factory=db_factory,
        service_log=_service_log(logger),
        logger=logger.getChild("ansible.runner"),
    )
    job_scheduler.set_server_ansible_runner(scheduler, ansible_runner.queue_run)
    return scheduler


def _run_onboarding_item(scheduler, item: Mapping[str, Any]) -> str:
    payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
    scheduler._run_onboarding_job(
        job_id=int(payload.get("job_id") or item.get("job_id") or 0),
        run_row_id=int(payload.get("run_id") or item.get("run_id") or 0),
        scheduled_ts=int(payload.get("scheduled_ts") or 0),
        components=list(payload.get("components") or []),
        targets=list(payload.get("targets") or []),
        credential_id=payload.get("credential_id"),
    )
    run_id = int(payload.get("run_id") or item.get("run_id") or 0)
    conn = scheduler._conn()
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, error FROM scheduled_job_runs WHERE id=?", (run_id,))
        row = cur.fetchone()
    finally:
        conn.close()
    status = str((row[0] if row else "") or "").strip()
    if status in {job_scheduler.RUN_STATUS_FAILED, job_scheduler.RUN_STATUS_TIMED_OUT, job_scheduler.RUN_STATUS_EXPIRED}:
        return WORK_STATUS_FAILED
    return WORK_STATUS_SUCCEEDED


def _work_item_links(item: Mapping[str, Any]) -> list[Dict[str, Any]]:
    payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
    job_id = int(payload.get("job_id") or item.get("job_id") or 0)
    run_id = int(payload.get("run_id") or item.get("run_id") or 0)
    kind = str(item.get("kind") or "")
    if kind == WORK_KIND_ONBOARDING_RUN and job_id > 0:
        return [
            {
                "kind": "onboarding_run",
                "label": f"Onboarding Job {job_id}",
                "job_id": job_id,
                "run_id": run_id,
                "path": f"/jobs/onboarding/{job_id}?tab=discovered_devices",
            }
        ]
    if kind in {WORK_KIND_AGENT_MAINTENANCE_RUN, WORK_KIND_SCHEDULED_RUN, WORK_KIND_SCHEDULED_WORKFLOW_RUN} and job_id > 0:
        task_link = payload.get("task_link") if isinstance(payload.get("task_link"), Mapping) else {}
        if task_link:
            return [dict(task_link)]
        return [
            {
                "kind": kind,
                "label": f"Scheduled Job {job_id}",
                "job_id": job_id,
                "run_id": run_id,
                "path": f"/jobs/{job_id}?tab=job_history",
            }
        ]
    return []


def _site_worker_scheduled_concurrency() -> int:
    try:
        settings = load_site_worker_settings()
        value = int(settings.get("scheduled_task_concurrency_limit") or 5)
    except Exception:
        value = 5
    return min(32, max(1, value))


def _agent_online_window_seconds() -> int:
    try:
        return max(60, int(str(os.environ.get("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS") or "300").strip() or "300"))
    except Exception:
        return 300


def _online_site_device_count(conn: sqlite3.Connection, *, site_id: int, now_ts: Optional[int] = None) -> int:
    cutoff = int(now_ts if now_ts is not None else _now_ts()) - _agent_online_window_seconds()
    cur = conn.cursor()
    try:
        cur.execute(
            """
            SELECT COUNT(DISTINCT d.guid)
              FROM device_sites ds
              JOIN devices d ON lower(d.hostname)=lower(ds.device_hostname)
             WHERE ds.site_id=?
               AND d.last_seen IS NOT NULL
               AND d.last_seen>=?
               AND COALESCE(NULLIF(d.status, ''), 'active') <> 'purged'
            """,
            (int(site_id), cutoff),
        )
        row = cur.fetchone()
    except Exception:
        return 0
    try:
        return max(0, int(row[0] if row else 0))
    except Exception:
        return 0


def _active_task_links(active_items: Dict[int, Dict[str, Any]], active_lock: threading.Lock) -> list[Dict[str, Any]]:
    with active_lock:
        links: list[Dict[str, Any]] = []
        for entry in active_items.values():
            for link in entry.get("task_links") or []:
                if isinstance(link, Mapping):
                    links.append(dict(link))
        return links


def _agent_socket_task_links(connected_device_count: int) -> list[Dict[str, Any]]:
    count = max(0, int(connected_device_count or 0))
    if count <= 0:
        return []
    noun = "Device" if count == 1 else "Devices"
    return [
        {
            "kind": LANE_AGENT_SOCKETS,
            "label": f"{count} {noun} Connected",
            "count": count,
        }
    ]


def _agent_online_task_links(online_device_count: int) -> list[Dict[str, Any]]:
    count = max(0, int(online_device_count or 0))
    if count <= 0:
        return []
    noun = "Device" if count == 1 else "Devices"
    return [
        {
            "kind": "agent_online",
            "label": f"{count} {noun} Online",
            "count": count,
        }
    ]


def _lanes_with_agent_sockets(lanes: list[str], connected_device_count: int, online_device_count: int = 0) -> list[str]:
    current = [str(lane) for lane in lanes or [] if str(lane or "").strip()]
    if (connected_device_count > 0 or online_device_count > 0) and LANE_AGENT_SOCKETS not in current:
        current.append(LANE_AGENT_SOCKETS)
    return current


def _update_agent_maintenance_run(
    db_factory,
    *,
    run_id: int,
    status: str,
    stdout: str = "",
    stderr: str = "",
) -> None:
    now = _now_ts()
    finished = now if status in {"Success", "Failed", "Skipped"} else None
    conn = db_factory()
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE scheduled_job_runs
               SET status=?,
                   updated_at=?,
                   finished_ts=COALESCE(?, finished_ts),
                   error=?
             WHERE id=?
            """,
            (status, now, finished, stderr[:512], int(run_id)),
        )
        cur.execute(
            """
            UPDATE scheduled_job_run_targets
               SET resolution_status=?,
                   resolution_reason=?
             WHERE run_id=?
            """,
            ("eligible" if status != "Failed" else "unresolved", stderr[:512], int(run_id)),
        )
        cur.execute(
            """
            SELECT s.activity_id
              FROM scheduled_job_run_activity s
             WHERE s.run_id=?
             LIMIT 1
            """,
            (int(run_id),),
        )
        row = cur.fetchone()
        if row and row[0]:
            from Data.Engine.services.activity_history import update_activity_history_row

            update_activity_history_row(
                conn,
                int(row[0]),
                status=status,
                stdout=stdout,
                stderr=stderr,
                append_output=True,
                updated_at=now,
                finished_at=finished,
            )
        conn.commit()
    finally:
        conn.close()


def _run_agent_maintenance_item(
    settings,
    db_factory,
    item: Mapping[str, Any],
    *,
    socket_runtime: Optional[SiteWorkerSocketRuntime] = None,
) -> str:
    payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
    run_id = int(payload.get("run_id") or item.get("run_id") or 0)
    hostname = str(payload.get("hostname") or "").strip()
    operation_id = str(payload.get("operation_id") or "").strip()
    event_payload = payload.get("event_payload") if isinstance(payload.get("event_payload"), Mapping) else {}
    if not run_id or not hostname or not operation_id or not event_payload:
        raise RuntimeError("agent maintenance work item payload incomplete")
    service_mode = str(payload.get("service_mode") or "system")
    event_name = str(payload.get("event_name") or "agent_maintenance_request")
    if socket_runtime is not None:
        emitted = socket_runtime.emit_host_service_event(hostname, service_mode, event_name, dict(event_payload))
    else:
        response = _post_internal(
            str(settings.secret_key or ""),
            "/api/internal/job-scheduler/host-service-event",
            {
                "hostname": hostname,
                "service_mode": service_mode,
                "event_name": event_name,
                "payload": dict(event_payload),
            },
            timeout=30.0,
        )
        emitted = bool(response.get("emitted"))
    if not emitted:
        error = f"No system agent socket is registered for host {hostname}; unable to dispatch agent maintenance."
        _update_agent_maintenance_run(db_factory, run_id=run_id, status="Failed", stderr=error)
        raise RuntimeError(error)
    stdout = (
        f"Site worker emitted agent maintenance operation_id={operation_id} "
        f"action={payload.get('action') or '-'} release_channel={payload.get('release_channel') or '-'} "
        f"branch={payload.get('branch') or '-'}\n"
    )
    _update_agent_maintenance_run(db_factory, run_id=run_id, status="Running", stdout=stdout)
    return WORK_STATUS_SUCCEEDED


def _work_status_for_scheduled_run(status: str) -> str:
    normalized = str(status or "").strip()
    if normalized in {
        job_scheduler.RUN_STATUS_FAILED,
        job_scheduler.RUN_STATUS_EXPIRED,
        job_scheduler.RUN_STATUS_TIMED_OUT,
    }:
        return WORK_STATUS_FAILED
    return WORK_STATUS_SUCCEEDED


def _transient_run_retry_max_attempts() -> int:
    return _positive_int_env(
        "BOREALIS_SITE_WORKER_TRANSIENT_RUN_RETRY_ATTEMPTS",
        DEFAULT_TRANSIENT_RUN_RETRY_ATTEMPTS,
    )


def _transient_scheduled_run_skip_reason(db_factory, *, run_id: int) -> str:
    conn = db_factory()
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT status, skip_reason, error
              FROM scheduled_job_runs
             WHERE id=?
            """,
            (int(run_id),),
        )
        row = cur.fetchone()
        if not row:
            return ""
        try:
            status = str(row["status"] or "").strip()
            skip_reason = str(row["skip_reason"] or "").strip()
            error = str(row["error"] or "").strip()
        except Exception:
            status = str(row[0] or "").strip()
            skip_reason = str(row[1] or "").strip() if len(row) > 1 else ""
            error = str(row[2] or "").strip() if len(row) > 2 else ""
        cur.execute(
            """
            SELECT resolution_reason
              FROM scheduled_job_run_targets
             WHERE run_id=?
               AND resolution_status=?
            """,
            (int(run_id), job_scheduler.RESOLUTION_STATUS_SKIPPED),
        )
        target_reasons = {
            str(reason_row[0] or "").strip().lower()
            for reason_row in (cur.fetchall() or [])
            if reason_row and str(reason_row[0] or "").strip()
        }
    finally:
        conn.close()
    if status != job_scheduler.RUN_STATUS_SKIPPED:
        return ""
    if skip_reason != job_scheduler.SKIP_REASON_NO_ELIGIBLE_TARGETS:
        return ""
    if target_reasons.intersection(TRANSIENT_RUN_RETRY_REASONS):
        return ",".join(sorted(target_reasons.intersection(TRANSIENT_RUN_RETRY_REASONS)))
    error_lower = error.lower()
    if "wireguard session is unavailable" in error_lower or "wireguard session is not ready" in error_lower:
        return "wireguard_session_not_ready"
    return ""


def _transient_scheduled_run_retry_reason(db_factory, *, run_id: int, attempt_count: int) -> str:
    if int(attempt_count or 0) >= _transient_run_retry_max_attempts():
        return ""
    return _transient_scheduled_run_skip_reason(db_factory, run_id=run_id)


def _reset_scheduled_run_for_retry(conn, *, run_id: int, reason: str) -> None:
    now = _now_ts()
    retry_message = f"Retrying after transient worker preparation failure: {reason}"
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE scheduled_job_runs
           SET status=?,
               finished_ts=NULL,
               skip_reason='',
               error=?,
               updated_at=?
         WHERE id=?
        """,
        (job_scheduler.RUN_STATUS_PENDING, retry_message[:512], now, int(run_id)),
    )
    placeholders = ",".join("?" for _ in TRANSIENT_RUN_RETRY_REASONS)
    cur.execute(
        f"""
        UPDATE scheduled_job_run_targets
           SET resolution_status=?,
               resolution_reason=''
         WHERE run_id=?
           AND resolution_reason IN ({placeholders})
        """,
        (
            job_scheduler.RESOLUTION_STATUS_PENDING,
            int(run_id),
            *sorted(TRANSIENT_RUN_RETRY_REASONS),
        ),
    )


def _fail_scheduled_run_after_transient_retries(conn, *, run_id: int, reason: str, attempt_count: int) -> str:
    now = _now_ts()
    max_attempts = _transient_run_retry_max_attempts()
    message = (
        f"Transient worker preparation failed after {int(attempt_count or 0)} attempts "
        f"(max {max_attempts}): {reason}"
    )
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE scheduled_job_runs
           SET status=?,
               finished_ts=?,
               skip_reason='',
               error=?,
               updated_at=?
         WHERE id=?
        """,
        (job_scheduler.RUN_STATUS_FAILED, now, message[:512], now, int(run_id)),
    )
    placeholders = ",".join("?" for _ in TRANSIENT_RUN_RETRY_REASONS)
    cur.execute(
        f"""
        UPDATE scheduled_job_run_targets
           SET resolution_status=?
         WHERE run_id=?
           AND resolution_reason IN ({placeholders})
        """,
        (
            job_scheduler.RESOLUTION_STATUS_UNRESOLVED,
            int(run_id),
            *sorted(TRANSIENT_RUN_RETRY_REASONS),
        ),
    )
    return message


def _wait_for_scheduled_run_completion(
    db_factory,
    *,
    run_id: int,
    logger,
    poll_seconds: Optional[float] = None,
) -> str:
    if int(run_id or 0) <= 0:
        return WORK_STATUS_FAILED
    poll_interval = poll_seconds
    if poll_interval is None:
        poll_interval = _non_negative_float_env(
            "BOREALIS_SITE_WORKER_RUN_WAIT_POLL_SECONDS",
            DEFAULT_RUN_WAIT_POLL_SECONDS,
        )
    poll_interval = max(0.1, float(poll_interval or DEFAULT_RUN_WAIT_POLL_SECONDS))
    last_status = ""
    while True:
        conn = db_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT status, finished_ts, error
                  FROM scheduled_job_runs
                 WHERE id=?
                """,
                (int(run_id),),
            )
            row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            logger.error("scheduled run disappeared while site-worker waited run_id=%s", run_id)
            return WORK_STATUS_FAILED
        try:
            status = str(row["status"] or "").strip()
            finished_ts = row["finished_ts"]
            error_text = str(row["error"] or "").strip()
        except Exception:
            status = str(row[0] or "").strip()
            finished_ts = row[1]
            error_text = str(row[2] or "").strip() if len(row) > 2 else ""
        if status and status != last_status:
            logger.info("waiting for scheduled run completion run_id=%s status=%s", run_id, status)
            last_status = status
        if status in job_scheduler.TERMINAL_RUN_STATUSES:
            return _work_status_for_scheduled_run(status)
        if finished_ts is not None and status not in {job_scheduler.RUN_STATUS_PENDING, job_scheduler.RUN_STATUS_RUNNING}:
            return _work_status_for_scheduled_run(status)
        if finished_ts is not None and error_text:
            return WORK_STATUS_FAILED
        time.sleep(poll_interval)


def _heartbeat_until(
    stop_event: threading.Event,
    db_factory,
    *,
    worker_guid: str,
    work_id: int,
    task_links: list[Dict[str, Any]],
    task_links_getter=None,
) -> None:
    while not stop_event.wait(20.0):
        visible_links = task_links_getter() if callable(task_links_getter) else task_links
        conn = db_factory()
        try:
            heartbeat_work_item(conn, work_id=int(work_id), lease_owner=worker_guid, lease_seconds=300)
            heartbeat_worker(
                conn,
                worker_guid=worker_guid,
                status=WORKER_STATUS_RUNNING,
                lanes=[LANE_ONBOARDING, LANE_SCHEDULED_JOB],
                task_links=visible_links,
            )
            conn.commit()
        finally:
            conn.close()


def _execute_work_item(
    settings,
    logger,
    db_factory,
    *,
    worker_guid: str,
    item: Mapping[str, Any],
    task_links_getter=None,
    socket_runtime: Optional[SiteWorkerSocketRuntime] = None,
) -> None:
    scheduler = _build_worker_scheduler(settings, logger, db_factory, socket_runtime=socket_runtime)
    error = ""
    final_status = WORK_STATUS_FAILED
    retry_reason = ""
    retry_exhausted_reason = ""
    retry_attempt_count = int(item.get("attempt_count") or 0)
    stop_event = threading.Event()
    task_links = _work_item_links(item)
    heartbeat = threading.Thread(
        target=_heartbeat_until,
        args=(stop_event, db_factory),
        kwargs={"worker_guid": worker_guid, "work_id": int(item["id"]), "task_links": task_links, "task_links_getter": task_links_getter},
        daemon=True,
    )
    heartbeat.start()
    try:
        item_kind = str(item.get("kind") or "")
        if item_kind == WORK_KIND_ONBOARDING_RUN:
            final_status = _run_onboarding_item(scheduler, item)
        elif item_kind == WORK_KIND_AGENT_MAINTENANCE_RUN:
            final_status = _run_agent_maintenance_item(settings, db_factory, item, socket_runtime=socket_runtime)
        elif item_kind == WORK_KIND_SCHEDULED_WORKFLOW_RUN:
            payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
            run_id = int(payload.get("run_id") or item.get("run_id") or 0)
            scheduler._dispatch_workflow_run(
                job_id=int(payload.get("job_id") or item.get("job_id") or 0),
                run_row_id=run_id,
                scheduled_ts=int(payload.get("scheduled_ts") or 0),
                workflow_component=dict(payload.get("workflow_component") or {}),
                workflow_site_scope=dict(payload.get("workflow_site_scope") or {}),
            )
            final_status = _wait_for_scheduled_run_completion(db_factory, run_id=run_id, logger=logger)
        elif item_kind == WORK_KIND_SCHEDULED_RUN:
            payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
            run_id = int(payload.get("run_id") or item.get("run_id") or 0)
            if bool(payload.get("shared_execution")):
                ansible_components = list(payload.get("ansible_components") or [])
                try:
                    component_index = int(payload.get("component_index")) if payload.get("component_index") is not None else 0
                except Exception:
                    component_index = 0
                component = ansible_components[component_index] if 0 <= component_index < len(ansible_components) else None
                if isinstance(component, Mapping):
                    link = scheduler._dispatch_shared_ansible(
                        job_id=int(payload.get("job_id") or item.get("job_id") or 0),
                        run_row_id=run_id,
                        scheduled_ts=int(payload.get("scheduled_ts") or 0),
                        run_mode=str(payload.get("run_mode") or "system"),
                        component=dict(component),
                        credential_id=payload.get("credential_id"),
                        use_service_account=bool(payload.get("use_service_account")),
                        target_row_ids=list(payload.get("target_row_ids") or []),
                    )
                    normalized_link = scheduler._normalize_run_activity_link(
                        run_row_id=run_id,
                        link=link,
                        default_component_kind="ansible",
                        default_script_type="ansible",
                    )
                    if normalized_link:
                        scheduler._persist_run_activity_links([normalized_link], created_at=_now_ts())
            else:
                conn_lookup = scheduler._conn()
                try:
                    cur_lookup = conn_lookup.cursor()
                    cur_lookup.execute("SELECT target_hostname FROM scheduled_job_runs WHERE id=?", (run_id,))
                    row = cur_lookup.fetchone()
                finally:
                    conn_lookup.close()
                host = str(row[0] if row else "").strip()
                if host:
                    scheduler._dispatch_run_activities(
                        job_id=int(payload.get("job_id") or item.get("job_id") or 0),
                        run_row_id=run_id,
                        scheduled_ts=int(payload.get("scheduled_ts") or 0),
                        hostname=host,
                        run_mode=str(payload.get("run_mode") or "system"),
                        script_components=list(payload.get("script_components") or []),
                        ansible_components=list(payload.get("ansible_components") or []),
                        credential_id=payload.get("credential_id"),
                        use_service_account=bool(payload.get("use_service_account")),
                        component_index=payload.get("component_index"),
                    )
            final_status = _wait_for_scheduled_run_completion(db_factory, run_id=run_id, logger=logger)
            if final_status == WORK_STATUS_SUCCEEDED:
                retry_reason = _transient_scheduled_run_retry_reason(
                    db_factory,
                    run_id=run_id,
                    attempt_count=retry_attempt_count,
                )
                if not retry_reason and retry_attempt_count >= _transient_run_retry_max_attempts():
                    retry_exhausted_reason = _transient_scheduled_run_skip_reason(db_factory, run_id=run_id)
        else:
            error = f"unsupported work kind {item.get('kind')}"
    except Exception as exc:
        error = str(exc)
        logger.exception("work item failed")
    finally:
        stop_event.set()
        heartbeat.join(timeout=5.0)
    conn = db_factory()
    try:
        if retry_reason:
            retry_delay = _positive_int_env(
                "BOREALIS_SITE_WORKER_TRANSIENT_RUN_RETRY_DELAY_SECONDS",
                DEFAULT_TRANSIENT_RUN_RETRY_DELAY_SECONDS,
            )
            run_id_for_retry = int((item.get("payload") or {}).get("run_id") or item.get("run_id") or 0)
            _reset_scheduled_run_for_retry(conn, run_id=run_id_for_retry, reason=retry_reason)
            requeue_work_item(
                conn,
                work_id=int(item["id"]),
                delay_seconds=retry_delay,
                error=f"requeued transient scheduled run: {retry_reason}",
            )
            logger.warning(
                "requeued transient scheduled run work_id=%s run_id=%s reason=%s delay_seconds=%s",
                item.get("id"),
                run_id_for_retry,
                retry_reason,
                retry_delay,
            )
        elif retry_exhausted_reason:
            run_id_for_retry = int((item.get("payload") or {}).get("run_id") or item.get("run_id") or 0)
            error = _fail_scheduled_run_after_transient_retries(
                conn,
                run_id=run_id_for_retry,
                reason=retry_exhausted_reason,
                attempt_count=retry_attempt_count,
            )
            complete_work_item(conn, work_id=int(item["id"]), status=WORK_STATUS_FAILED, error=error)
            logger.error(
                "failed transient scheduled run after retries work_id=%s run_id=%s reason=%s attempts=%s",
                item.get("id"),
                run_id_for_retry,
                retry_exhausted_reason,
                retry_attempt_count,
            )
        else:
            complete_work_item(conn, work_id=int(item["id"]), status=final_status, error=error)
        heartbeat_worker(
            conn,
            worker_guid=worker_guid,
            status=WORKER_STATUS_RUNNING,
            lanes=[],
            task_links=task_links_getter() if callable(task_links_getter) else task_links,
        )
        conn.commit()
    finally:
        conn.close()


def main() -> None:
    settings = load_runtime_config()
    logger = initialise_engine_logger(settings, name="borealis.site_worker")
    database.initialise_engine_database(settings.database_url, logger=logger)
    db_factory = _db_factory(settings.database_url)
    worker_guid = str(os.environ.get("BOREALIS_SITE_WORKER_GUID") or "").strip()
    site_id = int(str(os.environ.get("BOREALIS_SITE_WORKER_SITE_ID") or "0").strip() or "0")
    container_name = str(os.environ.get("BOREALIS_SITE_WORKER_CONTAINER_NAME") or f"site-worker-{worker_guid}").strip()
    remote_ops_host = str(os.environ.get("BOREALIS_SITE_WORKER_REMOTE_OPS_HOST") or "127.0.0.1").strip() or "127.0.0.1"
    try:
        remote_ops_port = int(str(os.environ.get("BOREALIS_SITE_WORKER_REMOTE_OPS_PORT") or "0").strip() or "0")
    except Exception:
        remote_ops_port = 0
    if remote_ops_port <= 0:
        remote_ops_port = site_worker_remote_ops_port(worker_guid, site_id)
    try:
        remote_desktop_port = int(str(os.environ.get("BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT") or "0").strip() or "0")
    except Exception:
        remote_desktop_port = 0
    if remote_desktop_port <= 0:
        remote_desktop_port = site_worker_remote_desktop_port(worker_guid, site_id)
    idle_ttl = max(30, int(str(os.environ.get("BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS") or "60").strip() or "60"))
    if not worker_guid or site_id <= 0:
        raise RuntimeError("site worker requires BOREALIS_SITE_WORKER_GUID and BOREALIS_SITE_WORKER_SITE_ID")
    socket_runtime = SiteWorkerSocketRuntime(
        worker_guid=worker_guid,
        site_id=site_id,
        host=remote_ops_host,
        port=remote_ops_port,
        guacamole_host=remote_ops_host,
        guacamole_port=remote_desktop_port,
        internal_secret=settings.secret_key,
        internal_api_base_url=_api_base_url(),
        db_conn_factory=db_factory,
        logger=logger.getChild("remote_ops"),
        service_log=_service_log(logger),
    )
    socket_runtime.start()
    conn = db_factory()
    try:
        register_worker(
            conn,
            worker_guid=worker_guid,
            container_name=container_name,
            site_id=site_id,
            status=WORKER_STATUS_RUNNING,
            upstream_host=remote_ops_host,
            upstream_port=remote_ops_port,
            route_metadata=_remote_ops_route_metadata(
                worker_guid=worker_guid,
                host=remote_ops_host,
                port=remote_ops_port,
                remote_desktop_port=remote_desktop_port,
            ),
        )
        conn.commit()
    finally:
        conn.close()
    idle_since = None
    claimed_count = 0
    active_items: Dict[int, Dict[str, Any]] = {}
    active_lock = threading.Lock()

    def active_links() -> list[Dict[str, Any]]:
        return _active_task_links(active_items, active_lock)

    def connected_device_count() -> int:
        try:
            return socket_runtime.registered_device_count()
        except Exception:
            return 0

    def online_device_count(conn: sqlite3.Connection) -> int:
        return _online_site_device_count(conn, site_id=site_id)

    def visible_links() -> list[Dict[str, Any]]:
        count = connected_device_count()
        return active_links() + _agent_socket_task_links(count)

    def visible_links_for_conn(conn: sqlite3.Connection) -> list[Dict[str, Any]]:
        socket_count = connected_device_count()
        online_count = online_device_count(conn) if socket_count <= 0 else 0
        return active_links() + _agent_socket_task_links(socket_count) + _agent_online_task_links(online_count)

    def visible_lanes(lanes: list[str], conn: sqlite3.Connection) -> list[str]:
        socket_count = connected_device_count()
        online_count = online_device_count(conn) if socket_count <= 0 else 0
        return _lanes_with_agent_sockets(lanes, socket_count, online_count)

    def prune_active_items() -> None:
        with active_lock:
            finished_ids = [work_id for work_id, entry in active_items.items() if not entry["thread"].is_alive()]
            for work_id in finished_ids:
                active_items.pop(work_id, None)

    def active_lane_state() -> tuple[int, int, list[str]]:
        with active_lock:
            scheduled_count = 0
            onboarding_count = 0
            for entry in active_items.values():
                kind = str((entry.get("item") or {}).get("kind") or "")
                if kind == WORK_KIND_ONBOARDING_RUN:
                    onboarding_count += 1
                elif kind in {WORK_KIND_AGENT_MAINTENANCE_RUN, WORK_KIND_SCHEDULED_RUN, WORK_KIND_SCHEDULED_WORKFLOW_RUN}:
                    scheduled_count += 1
            lanes = []
            if onboarding_count:
                lanes.append(LANE_ONBOARDING)
            if scheduled_count:
                lanes.append(LANE_SCHEDULED_JOB)
            return scheduled_count, onboarding_count, lanes

    try:
        while True:
            prune_active_items()
            scheduled_concurrency = _site_worker_scheduled_concurrency()
            active_scheduled, active_onboarding, active_lanes = active_lane_state()
            claim_lanes = []
            if active_onboarding <= 0:
                if active_scheduled <= 0:
                    claim_lanes = [LANE_ONBOARDING, LANE_SCHEDULED_JOB]
                elif active_scheduled < scheduled_concurrency:
                    claim_lanes = [LANE_SCHEDULED_JOB]

            item = None
            if claim_lanes:
                task_links = []
                conn = db_factory()
                try:
                    item = claim_next_work_item(
                        conn,
                        site_id=site_id,
                        lanes=claim_lanes,
                        lease_owner=worker_guid,
                        lease_seconds=300,
                    )
                    if item:
                        claimed_count += 1
                        idle_since = None
                        task_links = _work_item_links(item)
                        item_kind = str(item.get("kind") or "")
                        current_lanes = list(active_lanes)
                        if item_kind == WORK_KIND_ONBOARDING_RUN and LANE_ONBOARDING not in current_lanes:
                            current_lanes.append(LANE_ONBOARDING)
                        if item_kind in {WORK_KIND_AGENT_MAINTENANCE_RUN, WORK_KIND_SCHEDULED_RUN, WORK_KIND_SCHEDULED_WORKFLOW_RUN} and LANE_SCHEDULED_JOB not in current_lanes:
                            current_lanes.append(LANE_SCHEDULED_JOB)
                        heartbeat_worker(
                            conn,
                            worker_guid=worker_guid,
                            status=WORKER_STATUS_RUNNING,
                            lanes=visible_lanes(current_lanes, conn),
                            task_links=visible_links_for_conn(conn) + task_links,
                            claimed_count=claimed_count,
                        )
                    conn.commit()
                finally:
                    conn.close()
                if item:
                    thread = threading.Thread(
                        target=_execute_work_item,
                        kwargs={
                            "settings": settings,
                            "logger": logger,
                            "db_factory": db_factory,
                            "worker_guid": worker_guid,
                            "item": item,
                            "task_links_getter": visible_links,
                            "socket_runtime": socket_runtime,
                        },
                        daemon=True,
                    )
                    with active_lock:
                        active_items[int(item["id"])] = {"thread": thread, "item": item, "task_links": task_links}
                    thread.start()

            prune_active_items()
            active_scheduled, active_onboarding, active_lanes = active_lane_state()
            if active_scheduled or active_onboarding:
                conn = db_factory()
                try:
                    heartbeat_worker(
                        conn,
                        worker_guid=worker_guid,
                        status=WORKER_STATUS_RUNNING,
                        lanes=visible_lanes(active_lanes, conn),
                        task_links=visible_links_for_conn(conn),
                        claimed_count=claimed_count,
                    )
                    conn.commit()
                finally:
                    conn.close()
                time.sleep(1.0 if active_scheduled < scheduled_concurrency else 3.0)
                continue

            agent_device_count = connected_device_count()
            conn = db_factory()
            try:
                online_device_total = online_device_count(conn) if agent_device_count <= 0 else 0
                if agent_device_count > 0 or online_device_total > 0:
                    idle_since = None
                elif idle_since is None:
                    idle_since = _now_ts()
                heartbeat_worker(
                    conn,
                    worker_guid=worker_guid,
                    status=WORKER_STATUS_RUNNING if (agent_device_count > 0 or online_device_total > 0) else WORKER_STATUS_IDLE,
                    lanes=_lanes_with_agent_sockets([], agent_device_count, online_device_total),
                    task_links=_agent_socket_task_links(agent_device_count) + _agent_online_task_links(online_device_total),
                    idle_since=None if online_device_total > 0 else idle_since,
                    claimed_count=claimed_count,
                )
                conn.commit()
            finally:
                conn.close()
            if idle_since is not None and (_now_ts() - idle_since) >= idle_ttl:
                logger.info("site worker idle ttl reached; exiting")
                return
            time.sleep(3.0)
    finally:
        with active_lock:
            active_threads = [entry["thread"] for entry in active_items.values()]
        for thread in active_threads:
            thread.join(timeout=5.0)
        conn = db_factory()
        try:
            stop_worker(conn, worker_guid=worker_guid)
            conn.commit()
        finally:
            conn.close()


if __name__ == "__main__":
    main()
