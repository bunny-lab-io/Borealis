"""Ephemeral site worker process."""

from __future__ import annotations

import os
import threading
import time
from typing import Any, Dict, Mapping

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
    WORK_KIND_SCHEDULED_RUN,
    WORK_KIND_SCHEDULED_WORKFLOW_RUN,
    WORKER_STATUS_IDLE,
    WORKER_STATUS_RUNNING,
    claim_next_work_item,
    complete_work_item,
    heartbeat_work_item,
    heartbeat_worker,
    register_worker,
    stop_worker,
)
from .security import INTERNAL_TOKEN_HEADER, internal_token


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


def _build_worker_scheduler(settings, logger, db_factory):
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
    if kind in {WORK_KIND_SCHEDULED_RUN, WORK_KIND_SCHEDULED_WORKFLOW_RUN} and job_id > 0:
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


def _heartbeat_until(stop_event: threading.Event, db_factory, *, worker_guid: str, work_id: int, task_links: list[Dict[str, Any]]) -> None:
    while not stop_event.wait(20.0):
        conn = db_factory()
        try:
            heartbeat_work_item(conn, work_id=int(work_id), lease_owner=worker_guid, lease_seconds=300)
            heartbeat_worker(
                conn,
                worker_guid=worker_guid,
                status=WORKER_STATUS_RUNNING,
                lanes=[LANE_ONBOARDING, LANE_SCHEDULED_JOB],
                task_links=task_links,
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
    idle_ttl = max(30, int(str(os.environ.get("BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS") or "60").strip() or "60"))
    if not worker_guid or site_id <= 0:
        raise RuntimeError("site worker requires BOREALIS_SITE_WORKER_GUID and BOREALIS_SITE_WORKER_SITE_ID")
    conn = db_factory()
    try:
        register_worker(conn, worker_guid=worker_guid, container_name=container_name, site_id=site_id, status=WORKER_STATUS_RUNNING)
        conn.commit()
    finally:
        conn.close()
    scheduler = _build_worker_scheduler(settings, logger, db_factory)
    idle_since = None
    claimed_count = 0
    try:
        while True:
            conn = db_factory()
            try:
                item = claim_next_work_item(
                    conn,
                    site_id=site_id,
                    lanes=[LANE_ONBOARDING, LANE_SCHEDULED_JOB],
                    lease_owner=worker_guid,
                    lease_seconds=300,
                )
                if item:
                    claimed_count += 1
                    task_links = _work_item_links(item)
                    heartbeat_worker(
                        conn,
                        worker_guid=worker_guid,
                        status=WORKER_STATUS_RUNNING,
                        lanes=[LANE_ONBOARDING, LANE_SCHEDULED_JOB],
                        task_links=task_links,
                        claimed_count=claimed_count,
                    )
                else:
                    if idle_since is None:
                        idle_since = _now_ts()
                    heartbeat_worker(
                        conn,
                        worker_guid=worker_guid,
                        status=WORKER_STATUS_IDLE,
                        lanes=[],
                        idle_since=idle_since,
                        claimed_count=claimed_count,
                    )
                conn.commit()
            finally:
                conn.close()
            if not item:
                if idle_since is not None and (_now_ts() - idle_since) >= idle_ttl:
                    logger.info("site worker idle ttl reached; exiting")
                    return
                time.sleep(3.0)
                continue
            idle_since = None
            error = ""
            final_status = WORK_STATUS_FAILED
            stop_event = threading.Event()
            task_links = _work_item_links(item)
            heartbeat = threading.Thread(
                target=_heartbeat_until,
                args=(stop_event, db_factory),
                kwargs={"worker_guid": worker_guid, "work_id": int(item["id"]), "task_links": task_links},
                daemon=True,
            )
            heartbeat.start()
            try:
                item_kind = str(item.get("kind") or "")
                if item_kind == WORK_KIND_ONBOARDING_RUN:
                    final_status = _run_onboarding_item(scheduler, item)
                elif item_kind == WORK_KIND_SCHEDULED_WORKFLOW_RUN:
                    payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
                    scheduler._dispatch_workflow_run(
                        job_id=int(payload.get("job_id") or item.get("job_id") or 0),
                        run_row_id=int(payload.get("run_id") or item.get("run_id") or 0),
                        scheduled_ts=int(payload.get("scheduled_ts") or 0),
                        workflow_component=dict(payload.get("workflow_component") or {}),
                        workflow_site_scope=dict(payload.get("workflow_site_scope") or {}),
                    )
                    final_status = WORK_STATUS_SUCCEEDED
                elif item_kind == WORK_KIND_SCHEDULED_RUN:
                    payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
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
                                run_row_id=int(payload.get("run_id") or item.get("run_id") or 0),
                                scheduled_ts=int(payload.get("scheduled_ts") or 0),
                                run_mode=str(payload.get("run_mode") or "system"),
                                component=dict(component),
                                credential_id=payload.get("credential_id"),
                                use_service_account=bool(payload.get("use_service_account")),
                                target_row_ids=list(payload.get("target_row_ids") or []),
                            )
                            normalized_link = scheduler._normalize_run_activity_link(
                                run_row_id=int(payload.get("run_id") or item.get("run_id") or 0),
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
                            cur_lookup.execute("SELECT target_hostname FROM scheduled_job_runs WHERE id=?", (int(payload.get("run_id") or item.get("run_id") or 0),))
                            row = cur_lookup.fetchone()
                        finally:
                            conn_lookup.close()
                        host = str(row[0] if row else "").strip()
                        if host:
                            scheduler._dispatch_run_activities(
                                job_id=int(payload.get("job_id") or item.get("job_id") or 0),
                                run_row_id=int(payload.get("run_id") or item.get("run_id") or 0),
                                scheduled_ts=int(payload.get("scheduled_ts") or 0),
                                hostname=host,
                                run_mode=str(payload.get("run_mode") or "system"),
                                script_components=list(payload.get("script_components") or []),
                                ansible_components=list(payload.get("ansible_components") or []),
                                credential_id=payload.get("credential_id"),
                                use_service_account=bool(payload.get("use_service_account")),
                                component_index=payload.get("component_index"),
                            )
                    final_status = WORK_STATUS_SUCCEEDED
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
                complete_work_item(conn, work_id=int(item["id"]), status=final_status, error=error)
                heartbeat_worker(
                    conn,
                    worker_guid=worker_guid,
                    status=WORKER_STATUS_RUNNING,
                    lanes=[],
                    claimed_count=claimed_count,
                )
                conn.commit()
            finally:
                conn.close()
    finally:
        conn = db_factory()
        try:
            stop_worker(conn, worker_guid=worker_guid)
            conn.commit()
        finally:
            conn.close()


if __name__ == "__main__":
    main()
