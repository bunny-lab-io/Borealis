"""Long-lived job-scheduler process."""

from __future__ import annotations

import json
import os
import shlex
import shutil
import subprocess
import time
import uuid
from pathlib import Path
from typing import Any, Dict, List, Mapping, Optional, Sequence

import requests

from Data.Engine import database
from Data.Engine.config import PROJECT_ROOT, initialise_engine_logger, load_runtime_config
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.security import signing
from Data.Engine.services.API.scheduled_jobs import job_scheduler
from Data.Engine.services.ansible import EngineAnsibleRunner
from Data.Engine.services.assemblies.service import AssemblyRuntimeService
from Data.Engine.assembly_management import initialise_assembly_runtime

from .queue import (
    WORK_STATUS_FAILED,
    WORK_STATUS_SUCCEEDED,
    WORK_KIND_SCHEDULED_RUN,
    WORK_KIND_SCHEDULED_WORKFLOW_RUN,
    LANE_SCHEDULED_JOB,
    WORKER_STATUS_RUNNING,
    active_worker_for_site,
    claim_next_work_item,
    claim_service_action,
    complete_work_item,
    enqueue_onboarding_run,
    enqueue_scheduled_run,
    enqueue_scheduled_workflow_run,
    ensure_job_scheduler_tables,
    expire_stale_leases,
    mark_missing_workers_lost,
    mark_lost_workers,
    prune_worker_history,
    queued_site_ids,
    register_worker,
    replace_service_snapshots,
    update_worker_docker_state,
    heartbeat_worker,
)
from .security import INTERNAL_TOKEN_HEADER, internal_token


class _TaskSchedulerApp:
    def __init__(self, *, logger, secret_key: str) -> None:
        self.logger = logger
        self.secret_key = secret_key


class _NoopSocketIO:
    def start_background_task(self, target, *args, **kwargs):
        import threading

        thread = threading.Thread(target=target, args=args, kwargs=kwargs, daemon=True)
        thread.start()
        return thread

    def emit(self, *_args, **_kwargs) -> None:
        return None


def _now_ts() -> int:
    return int(time.time())


def _service_log(logger):
    def _log(service: str, message: str, scope: Optional[str] = None, level: str = "INFO") -> None:
        numeric = getattr(__import__("logging"), str(level or "INFO").upper(), None)
        try:
            logger.log(numeric or 20, "[service:%s]%s %s", service, f"[{scope}]" if scope else "", message)
        except Exception:
            pass

    return _log


def _db_factory(database_url: str):
    def _factory():
        return sqlite3.connect(database_url, timeout=30)

    return _factory


def _api_base_url() -> str:
    return str(os.environ.get("BOREALIS_INTERNAL_API_BASE_URL") or "http://127.0.0.1:5000").rstrip("/")


def _internal_headers(secret: str) -> Dict[str, str]:
    return {INTERNAL_TOKEN_HEADER: internal_token(secret)}


def _post_internal(path: str, *, secret: str, payload: Mapping[str, Any], timeout: float = 15.0) -> Dict[str, Any]:
    response = requests.post(
        f"{_api_base_url()}{path}",
        headers=_internal_headers(secret),
        json=dict(payload or {}),
        timeout=timeout,
    )
    response.raise_for_status()
    try:
        data = response.json()
    except Exception:
        data = {}
    return data if isinstance(data, dict) else {}


def _get_internal(path: str, *, secret: str, timeout: float = 15.0) -> Dict[str, Any]:
    response = requests.get(
        f"{_api_base_url()}{path}",
        headers=_internal_headers(secret),
        timeout=timeout,
    )
    response.raise_for_status()
    try:
        data = response.json()
    except Exception:
        data = {}
    return data if isinstance(data, dict) else {}


def _fetch_credential(secret: str, credential_id: int) -> Dict[str, Any]:
    payload = _get_internal(
        f"/api/internal/job-scheduler/credential/{int(credential_id)}",
        secret=secret,
        timeout=30.0,
    )
    credential = payload.get("credential") if isinstance(payload.get("credential"), Mapping) else None
    if not isinstance(credential, Mapping):
        raise RuntimeError("credential payload unavailable")
    return dict(credential)


def _make_host_service_emitter(secret: str):
    def _emit(hostname: str, service_mode: str, event_name: str, payload: Any) -> bool:
        try:
            result = _post_internal(
                "/api/internal/job-scheduler/host-service-event",
                secret=secret,
                payload={
                    "hostname": hostname,
                    "service_mode": service_mode,
                    "event_name": event_name,
                    "payload": payload,
                },
                timeout=20.0,
            )
            return bool(result.get("emitted"))
        except Exception:
            return False

    return _emit


def _make_workflow_launcher(secret: str):
    def _launch(**kwargs):
        return _post_internal(
            "/api/internal/job-scheduler/workflow/start",
            secret=secret,
            payload=kwargs,
            timeout=30.0,
        )

    return _launch


def _make_vpn_session_lookup(secret: str):
    def _lookup():
        payload = _get_internal("/api/internal/job-scheduler/vpn-sessions", secret=secret, timeout=15.0)
        sessions = payload.get("sessions") if isinstance(payload.get("sessions"), Mapping) else {}
        return dict(sessions or {})

    return _lookup


def _make_vpn_session_prepare(secret: str):
    def _prepare(agent_ids: Sequence[str], required_ports: Optional[Sequence[int]] = None):
        ports = []
        for item in required_ports or []:
            try:
                port = int(item)
            except Exception:
                continue
            if port > 0:
                ports.append(port)
        payload = _post_internal(
            "/api/internal/job-scheduler/vpn-prepare",
            secret=secret,
            payload={
                "agent_ids": [str(item) for item in (agent_ids or []) if str(item).strip()],
                "required_ports": ports,
            },
            timeout=90.0,
        )
        sessions = payload.get("sessions") if isinstance(payload.get("sessions"), Mapping) else {}
        return dict(sessions or {})

    return _prepare


def _make_online_lookup(db_factory, logger):
    def _lookup() -> Sequence[str]:
        threshold = _now_ts() - 300
        conn = db_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT hostname FROM devices WHERE last_seen IS NOT NULL AND last_seen >= ?",
                (threshold,),
            )
            rows = cur.fetchall()
        except Exception as exc:
            logger.error("online host snapshot lookup failed: %s", exc)
            rows = []
        finally:
            conn.close()
        seen = set()
        hostnames = []
        for row in rows or []:
            try:
                name = str(row[0] if isinstance(row, (list, tuple)) else row or "").strip()
            except Exception:
                name = ""
            if not name:
                continue
            for variant in (name, name.upper(), name.lower()):
                if variant and variant not in seen:
                    seen.add(variant)
                    hostnames.append(variant)
        return hostnames

    return _lookup


def _resolve_public_base_url(settings, secret: str) -> str:
    try:
        payload = _get_internal("/api/internal/job-scheduler/public-base-url", secret=secret, timeout=10.0)
        value = str(payload.get("public_base_url") or "").strip()
        if value:
            return value
    except Exception:
        pass
    return str(settings.public_base_url or "").strip()


def _build_scheduler(settings, logger):
    database.initialise_engine_database(settings.database_url, logger=logger)
    db_factory = _db_factory(settings.database_url)
    conn = db_factory()
    try:
        ensure_job_scheduler_tables(conn)
        conn.commit()
    finally:
        conn.close()

    app = _TaskSchedulerApp(logger=logger, secret_key=settings.secret_key)
    assembly_cache = initialise_assembly_runtime(logger=logger, config=settings.as_dict())
    assembly_cache.reload()
    assembly_runtime = AssemblyRuntimeService(assembly_cache, logger=logger)
    try:
        script_signer = signing.load_signer()
    except Exception:
        script_signer = None

    scheduler = job_scheduler.register(
        app,
        _NoopSocketIO(),
        db_factory,
        script_signer=script_signer,
        service_logger=_service_log(logger),
        assembly_runtime=assembly_runtime,
        register_routes=False,
    )
    secret = str(settings.secret_key or "")
    job_scheduler.set_host_service_emitter(scheduler, _make_host_service_emitter(secret))
    job_scheduler.set_workflow_run_launcher(scheduler, _make_workflow_launcher(secret))
    job_scheduler.set_public_base_url_lookup(scheduler, lambda: _resolve_public_base_url(settings, secret))
    job_scheduler.set_online_lookup(scheduler, _make_online_lookup(db_factory, logger))
    job_scheduler.set_credential_fetcher(scheduler, lambda credential_id: _fetch_credential(secret, int(credential_id)))
    job_scheduler.set_vpn_session_lookup(scheduler, _make_vpn_session_lookup(secret))
    job_scheduler.set_vpn_session_prepare(scheduler, _make_vpn_session_prepare(secret))
    ansible_runner = EngineAnsibleRunner(
        socketio=_NoopSocketIO(),
        db_conn_factory=db_factory,
        service_log=_service_log(logger),
        logger=logger.getChild("ansible.runner"),
    )
    job_scheduler.set_server_ansible_runner(scheduler, ansible_runner.queue_run)

    def _enqueue_onboarding(**kwargs):
        components = list(kwargs.get("components") or [])
        targets = list(kwargs.get("targets") or [])
        config, config_error = scheduler._onboarding_scope_config(components=components, targets=targets)
        conn2 = db_factory()
        try:
            if config_error:
                now = _now_ts()
                cur = conn2.cursor()
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?, finished_ts=?, updated_at=?, error=?
                     WHERE id=?
                    """,
                    (job_scheduler.RUN_STATUS_FAILED, now, now, str(config_error), int(kwargs.get("run_row_id") or 0)),
                )
                conn2.commit()
                return None
            work_id = enqueue_onboarding_run(
                conn2,
                job_id=int(kwargs.get("job_id") or 0),
                run_id=int(kwargs.get("run_row_id") or 0),
                scheduled_ts=int(kwargs.get("scheduled_ts") or 0),
                site_id=config.get("site_id"),
                components=components,
                targets=targets,
                credential_id=kwargs.get("credential_id"),
            )
            conn2.commit()
            return work_id
        finally:
            conn2.close()

    job_scheduler.set_onboarding_run_dispatcher(scheduler, _enqueue_onboarding)

    def _enqueue_scheduled_run(**kwargs):
        conn2 = db_factory()
        try:
            work_id = enqueue_scheduled_run(conn2, **kwargs)
            conn2.commit()
            return work_id
        finally:
            conn2.close()

    def _enqueue_scheduled_workflow(**kwargs):
        conn2 = db_factory()
        try:
            work_id = enqueue_scheduled_workflow_run(conn2, **kwargs)
            conn2.commit()
            return work_id
        finally:
            conn2.close()

    job_scheduler.set_scheduled_run_dispatcher(scheduler, _enqueue_scheduled_run)
    job_scheduler.set_scheduled_workflow_dispatcher(scheduler, _enqueue_scheduled_workflow)
    return scheduler, db_factory


def _compose_env_file() -> Path:
    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    return Path(os.environ.get("BOREALIS_RUNTIME_ENV_FILE") or project_root / "Engine" / "Deploy" / "compose.env")


def _worker_image() -> str:
    return str(os.environ.get("BOREALIS_SITE_WORKER_IMAGE") or "borealis-engine/site-worker:local").strip()


def _env_value(env_items: Sequence[Any], key: str) -> str:
    prefix = f"{key}="
    for item in env_items or []:
        value = str(item or "")
        if value.startswith(prefix):
            return value[len(prefix):].strip()
    return ""


def _docker_site_worker_snapshots(logger) -> Optional[List[Dict[str, Any]]]:
    docker_bin = shutil.which("docker") or ""
    if not docker_bin:
        logger.warning("docker CLI unavailable; site-worker adoption skipped")
        return None
    ps_args = [
        docker_bin,
        "ps",
        "--filter",
        "label=borealis.role=site-worker",
        "--format",
        "{{.ID}}",
    ]
    try:
        completed = subprocess.run(ps_args, capture_output=True, text=True, check=False, timeout=10)
    except Exception as exc:
        logger.warning("site-worker docker ps failed: %s", exc)
        return None
    if completed.returncode != 0:
        logger.warning("site-worker docker ps failed: %s", (completed.stderr or completed.stdout or "").strip())
        return None
    container_ids = [line.strip() for line in str(completed.stdout or "").splitlines() if line.strip()]
    if not container_ids:
        return []
    inspect_args = [docker_bin, "inspect", *container_ids]
    try:
        inspected = subprocess.run(inspect_args, capture_output=True, text=True, check=False, timeout=15)
    except Exception as exc:
        logger.warning("site-worker docker inspect failed: %s", exc)
        return None
    if inspected.returncode != 0:
        logger.warning("site-worker docker inspect failed: %s", (inspected.stderr or inspected.stdout or "").strip())
        return None
    try:
        parsed = json.loads(str(inspected.stdout or "[]"))
    except Exception as exc:
        logger.warning("site-worker docker inspect parse failed: %s", exc)
        return None
    snapshots: List[Dict[str, Any]] = []
    for item in parsed if isinstance(parsed, list) else []:
        if not isinstance(item, Mapping):
            continue
        config = item.get("Config") if isinstance(item.get("Config"), Mapping) else {}
        labels = config.get("Labels") if isinstance(config.get("Labels"), Mapping) else {}
        env_items = config.get("Env") if isinstance(config.get("Env"), list) else []
        worker_guid = str(labels.get("borealis.worker_guid") or _env_value(env_items, "BOREALIS_SITE_WORKER_GUID") or "").strip()
        site_raw = str(labels.get("borealis.site_id") or _env_value(env_items, "BOREALIS_SITE_WORKER_SITE_ID") or "0").strip()
        try:
            site_id = int(site_raw)
        except Exception:
            site_id = 0
        name = str(item.get("Name") or "").strip().lstrip("/")
        if not name:
            names = item.get("Names") if isinstance(item.get("Names"), list) else []
            name = str(names[0] if names else "").strip().lstrip("/")
        state = item.get("State") if isinstance(item.get("State"), Mapping) else {}
        docker_state = str(state.get("Status") or ("running" if state.get("Running") else "") or "").strip()
        exit_code = state.get("ExitCode")
        try:
            exit_code = int(exit_code) if exit_code is not None else None
        except Exception:
            exit_code = None
        if not worker_guid or site_id <= 0:
            logger.warning("ignoring malformed site-worker container name=%s worker_guid=%s site_id=%s", name, worker_guid or "-", site_id)
            continue
        snapshots.append(
            {
                "worker_guid": worker_guid,
                "site_id": site_id,
                "container_name": name or f"site-worker-{worker_guid}",
                "docker_state": docker_state or "running",
                "exit_code": exit_code,
            }
        )
    return snapshots


def _reconcile_site_workers(db_factory, logger) -> None:
    snapshots = _docker_site_worker_snapshots(logger)
    if snapshots is None:
        return
    live_worker_guids = [str(item.get("worker_guid") or "") for item in snapshots if str(item.get("worker_guid") or "").strip()]
    seen_sites: Dict[int, str] = {}
    duplicate_sites: Dict[int, List[str]] = {}
    conn = db_factory()
    try:
        for snapshot in snapshots:
            worker_guid = str(snapshot.get("worker_guid") or "").strip()
            container_name = str(snapshot.get("container_name") or f"site-worker-{worker_guid}").strip()
            site_id = int(snapshot.get("site_id") or 0)
            if not worker_guid or site_id <= 0:
                continue
            if site_id in seen_sites:
                duplicate_sites.setdefault(site_id, [seen_sites[site_id]]).append(container_name)
            else:
                seen_sites[site_id] = container_name
            cur = conn.cursor()
            cur.execute("SELECT 1 FROM job_scheduler_workers WHERE worker_guid=? LIMIT 1", (worker_guid,))
            if not cur.fetchone():
                register_worker(
                    conn,
                    worker_guid=worker_guid,
                    container_name=container_name,
                    site_id=site_id,
                    status=WORKER_STATUS_RUNNING,
                )
            update_worker_docker_state(
                conn,
                worker_guid=worker_guid,
                docker_state=str(snapshot.get("docker_state") or "running"),
                exit_code=snapshot.get("exit_code"),
            )
        lost_count = mark_missing_workers_lost(conn, live_worker_guids=live_worker_guids)
        conn.commit()
    finally:
        conn.close()
    if snapshots:
        logger.info("site-worker reconcile live=%s lost=%s", len(snapshots), lost_count)
    elif lost_count:
        logger.info("site-worker reconcile live=0 lost=%s", lost_count)
    for site_id, names in duplicate_sites.items():
        logger.warning("multiple live site-workers detected for site_id=%s containers=%s", site_id, ",".join(names))


def _refresh_service_snapshots(db_factory, logger) -> None:
    docker_bin = shutil.which("docker") or ""
    if not docker_bin:
        return
    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    compose_file = project_root / "Data" / "Engine" / "Containers" / "compose.yaml"
    env_file = _compose_env_file()
    project_name = str(os.environ.get("BOREALIS_COMPOSE_PROJECT_NAME") or "borealis-engine")
    if not compose_file.is_file() or not env_file.is_file():
        return
    args = [
        docker_bin,
        "compose",
        "--project-name",
        project_name,
        "--env-file",
        str(env_file),
        "-f",
        str(compose_file),
        "ps",
        "--format",
        "json",
    ]
    completed = subprocess.run(args, capture_output=True, text=True, check=False, timeout=10)
    if completed.returncode != 0:
        return
    snapshots = []
    text = str(completed.stdout or "").strip()
    if not text:
        return
    try:
        parsed = json.loads(text)
        if isinstance(parsed, list):
            snapshots = [item for item in parsed if isinstance(item, Mapping)]
        elif isinstance(parsed, Mapping):
            snapshots = [parsed]
    except Exception:
        for line in text.splitlines():
            try:
                row = json.loads(line)
            except Exception:
                continue
            if isinstance(row, Mapping):
                snapshots.append(row)
    if not snapshots:
        return
    conn = db_factory()
    try:
        replace_service_snapshots(conn, snapshots)
        conn.commit()
    finally:
        conn.close()


def _spawn_site_worker(db_factory, *, site_id: int, logger) -> None:
    conn = db_factory()
    try:
        if active_worker_for_site(conn, site_id=int(site_id)):
            return
        worker_guid = str(uuid.uuid4())
        container_name = f"site-worker-{worker_guid}"
        register_worker(conn, worker_guid=worker_guid, container_name=container_name, site_id=int(site_id))
        conn.commit()
    finally:
        conn.close()

    docker_bin = shutil.which("docker") or ""
    if not docker_bin:
        logger.error("docker CLI unavailable; cannot launch %s", container_name)
        return

    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    env_file = _compose_env_file()
    image = _worker_image()
    args = [
        docker_bin,
        "run",
        "--rm",
        "-d",
        "--name",
        container_name,
        "--network",
        "host",
        "--label",
        "borealis.role=site-worker",
        "--label",
        f"borealis.site_id={int(site_id)}",
        "--label",
        f"borealis.worker_guid={worker_guid}",
        "--label",
        "borealis.created_by=job-scheduler",
    ]
    if env_file.is_file():
        args.extend(["--env-file", str(env_file)])
    args.extend(
        [
            "-e",
            f"BOREALIS_SITE_WORKER_GUID={worker_guid}",
            "-e",
            f"BOREALIS_SITE_WORKER_SITE_ID={int(site_id)}",
            "-e",
            f"BOREALIS_SITE_WORKER_CONTAINER_NAME={container_name}",
            "-e",
            "BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS=60",
            "-e",
            "BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=7",
            "-e",
            f"BOREALIS_INTERNAL_API_BASE_URL={_api_base_url()}",
            "-e",
            "BOREALIS_LOG_FILE=/tmp/borealis-site-worker.log",
            "-e",
            "BOREALIS_ERROR_LOG_FILE=/tmp/borealis-site-worker-error.log",
            "-e",
            "BOREALIS_API_LOG_FILE=/tmp/borealis-site-worker-api.log",
            "-e",
            "BOREALIS_VPN_TUNNEL_LOG_FILE=/tmp/borealis-site-worker-vpn.log",
            "-v",
            f"{project_root}/Engine/Services/api-backend/secrets:/opt/Borealis/Engine/Services/api-backend/secrets:ro",
            "-v",
            f"{project_root}/Engine/Services/api-backend/config:/opt/Borealis/Engine/Services/api-backend/config:ro",
            image,
        ]
    )
    completed = subprocess.run(args, capture_output=True, text=True, check=False, timeout=20)
    if completed.returncode != 0:
        logger.error("failed to launch %s: %s", container_name, (completed.stderr or completed.stdout or "").strip())
        conn = db_factory()
        try:
            from .queue import stop_worker

            stop_worker(conn, worker_guid=worker_guid, status="lost")
            conn.commit()
        finally:
            conn.close()
        return
    logger.info("launched %s for site_id=%s", container_name, site_id)


def _heartbeat_manager(db_factory) -> None:
    worker_guid = str(os.environ.get("BOREALIS_JOB_SCHEDULER_GUID") or "job-scheduler").strip() or "job-scheduler"
    container_name = str(os.environ.get("BOREALIS_JOB_SCHEDULER_CONTAINER_NAME") or "borealis-engine-job-scheduler").strip()
    conn = db_factory()
    try:
        register_worker(
            conn,
            worker_guid=worker_guid,
            container_name=container_name,
            site_id=0,
            status=WORKER_STATUS_RUNNING,
        )
        heartbeat_worker(
            conn,
            worker_guid=worker_guid,
            status=WORKER_STATUS_RUNNING,
            lanes=["scheduled_tick", "worker_reconcile", "service_action"],
            task_links=[
                {
                    "kind": "manager",
                    "label": "Job Scheduler Manager",
                    "path": "/server-info",
                }
            ],
        )
        conn.commit()
    finally:
        conn.close()


def _run_service_action(payload: Mapping[str, Any], *, logger) -> str:
    service_key = str(payload.get("service_key") or "").strip().lower()
    action = payload.get("action") if isinstance(payload.get("action"), Mapping) else {}
    action_name = str(action.get("action") or "").strip().lower()
    action_mode = str(action.get("mode") or "").strip().lower()
    if not service_key or not action_name:
        raise RuntimeError("service action payload incomplete")
    docker_bin = shutil.which("docker") or ""
    if not docker_bin:
        raise RuntimeError("docker CLI unavailable")
    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    image = str(os.environ.get("BOREALIS_API_BACKEND_IMAGE") or "borealis-engine/api-backend:local").strip()
    command_parts = ["bash", "Engine.sh", "--service", service_key, action_name]
    if action_mode:
        command_parts.append(action_mode)
    shell_command = f"sleep 2; {shlex.join(command_parts)}"
    helper_name = f"borealis-engine-action-{service_key}-{uuid.uuid4().hex[:8]}"
    args = [
        docker_bin,
        "run",
        "--rm",
        "-d",
        "--name",
        helper_name,
        "--network",
        "host",
        "-v",
        "/var/run/docker.sock:/var/run/docker.sock",
        "-v",
        f"{project_root}:{project_root}",
        "-w",
        str(project_root),
        "--entrypoint",
        "/bin/bash",
        image,
        "-lc",
        shell_command,
    ]
    completed = subprocess.run(args, capture_output=True, text=True, check=False, timeout=20)
    if completed.returncode != 0:
        raise RuntimeError((completed.stderr or completed.stdout or "docker helper launch failed").strip())
    helper_id = str(completed.stdout or "").strip().splitlines()[0] if str(completed.stdout or "").strip() else helper_name
    logger.info("queued service action helper=%s service=%s action=%s", helper_id, service_key, action_name)
    return helper_id


def _process_service_actions(db_factory, logger) -> None:
    conn = db_factory()
    try:
        item = claim_service_action(conn, lease_owner="job-scheduler", lease_seconds=300)
        conn.commit()
    finally:
        conn.close()
    if not item:
        return
    status = WORK_STATUS_SUCCEEDED
    error = ""
    try:
        _run_service_action(item.get("payload") or {}, logger=logger)
    except Exception as exc:
        status = WORK_STATUS_FAILED
        error = str(exc)
        logger.error("service action failed: %s", error)
    conn = db_factory()
    try:
        complete_work_item(conn, work_id=int(item["id"]), status=status, error=error)
        conn.commit()
    finally:
        conn.close()


def _run_scheduler_work_item(scheduler, item: Mapping[str, Any]) -> str:
    payload = item.get("payload") if isinstance(item.get("payload"), Mapping) else {}
    kind = str(item.get("kind") or "")
    if kind == WORK_KIND_SCHEDULED_WORKFLOW_RUN:
        scheduler._dispatch_workflow_run(
            job_id=int(payload.get("job_id") or item.get("job_id") or 0),
            run_row_id=int(payload.get("run_id") or item.get("run_id") or 0),
            scheduled_ts=int(payload.get("scheduled_ts") or 0),
            workflow_component=dict(payload.get("workflow_component") or {}),
            workflow_site_scope=dict(payload.get("workflow_site_scope") or {}),
        )
        return WORK_STATUS_SUCCEEDED
    if kind == WORK_KIND_SCHEDULED_RUN:
        job_id = int(payload.get("job_id") or item.get("job_id") or 0)
        run_id = int(payload.get("run_id") or item.get("run_id") or 0)
        scheduled_ts = int(payload.get("scheduled_ts") or 0)
        run_mode = str(payload.get("run_mode") or "system")
        script_components = list(payload.get("script_components") or [])
        ansible_components = list(payload.get("ansible_components") or [])
        component_index = payload.get("component_index")
        if bool(payload.get("shared_execution")):
            try:
                component_index_int = int(component_index) if component_index is not None else 0
            except Exception:
                component_index_int = 0
            component = ansible_components[component_index_int] if 0 <= component_index_int < len(ansible_components) else None
            if isinstance(component, Mapping):
                link = scheduler._dispatch_shared_ansible(
                    job_id=job_id,
                    run_row_id=run_id,
                    scheduled_ts=scheduled_ts,
                    run_mode=run_mode,
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
            conn = scheduler._conn()
            try:
                cur = conn.cursor()
                cur.execute("SELECT target_hostname FROM scheduled_job_runs WHERE id=?", (run_id,))
                row = cur.fetchone()
            finally:
                conn.close()
            host = str(row[0] if row else "").strip()
            if host:
                scheduler._dispatch_run_activities(
                    job_id=job_id,
                    run_row_id=run_id,
                    scheduled_ts=scheduled_ts,
                    hostname=host,
                    run_mode=run_mode,
                    script_components=script_components,
                    ansible_components=ansible_components,
                    credential_id=payload.get("credential_id"),
                    use_service_account=bool(payload.get("use_service_account")),
                    component_index=component_index,
                )
        return WORK_STATUS_SUCCEEDED
    raise RuntimeError(f"unsupported scheduler work kind {kind}")


def _process_global_scheduled_work(scheduler, db_factory, logger) -> None:
    conn = db_factory()
    try:
        item = claim_next_work_item(conn, site_id=0, lanes=[LANE_SCHEDULED_JOB], lease_owner="job-scheduler", lease_seconds=300)
        conn.commit()
    finally:
        conn.close()
    if not item:
        return
    status = WORK_STATUS_SUCCEEDED
    error = ""
    try:
        status = _run_scheduler_work_item(scheduler, item)
    except Exception as exc:
        status = WORK_STATUS_FAILED
        error = str(exc)
        logger.exception("global scheduled work failed")
    conn = db_factory()
    try:
        complete_work_item(conn, work_id=int(item["id"]), status=status, error=error)
        conn.commit()
    finally:
        conn.close()


def main() -> None:
    settings = load_runtime_config()
    logger = initialise_engine_logger(settings, name="borealis.job_scheduler")
    scheduler, db_factory = _build_scheduler(settings, logger)
    logger.info("job-scheduler starting")
    try:
        _heartbeat_manager(db_factory)
    except Exception:
        logger.exception("job-scheduler manager heartbeat failed")
    try:
        _reconcile_site_workers(db_factory, logger)
    except Exception:
        logger.exception("site-worker startup reconcile failed")
    next_tick = 0
    next_worker_reconcile = 0
    worker_reconcile_interval = max(10, int(str(os.environ.get("BOREALIS_SITE_WORKER_RECONCILE_SECONDS") or "30").strip() or "30"))
    worker_history_seconds = max(60, int(str(os.environ.get("BOREALIS_WORKER_HISTORY_SECONDS") or "60").strip() or "60"))
    while True:
        now = _now_ts()
        try:
            _heartbeat_manager(db_factory)
        except Exception:
            logger.exception("job-scheduler manager heartbeat failed")
        if now >= next_tick:
            try:
                scheduler._tick_once()
            except Exception:
                logger.exception("scheduled tick failed")
            next_tick = now + max(5, 60 - (now % 60))
        if now >= next_worker_reconcile:
            try:
                _reconcile_site_workers(db_factory, logger)
            except Exception:
                logger.exception("site-worker reconcile failed")
            next_worker_reconcile = now + worker_reconcile_interval
        conn = db_factory()
        try:
            expire_stale_leases(conn)
            mark_lost_workers(conn)
            prune_worker_history(conn, retention_seconds=worker_history_seconds)
            site_ids = queued_site_ids(conn)
            conn.commit()
        finally:
            conn.close()
        for site_id in site_ids:
            try:
                _spawn_site_worker(db_factory, site_id=int(site_id), logger=logger)
            except Exception:
                logger.exception("failed to reconcile worker for site_id=%s", site_id)
        try:
            _process_service_actions(db_factory, logger)
        except Exception:
            logger.exception("failed to process service action queue")
        try:
            _process_global_scheduled_work(scheduler, db_factory, logger)
        except Exception:
            logger.exception("failed to process global scheduled work")
        try:
            _refresh_service_snapshots(db_factory, logger)
        except Exception:
            logger.exception("failed to refresh service snapshots")
        time.sleep(2.0)


if __name__ == "__main__":
    main()
