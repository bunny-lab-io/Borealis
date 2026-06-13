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
from Data.Engine.services.ansible import EngineAnsibleRunner

from .queue import (
    WORKER_STATUS_IDLE,
    WORKER_STATUS_RUNNING,
    heartbeat_worker,
    register_worker,
    site_worker_remote_desktop_port,
    site_worker_remote_ops_port,
    stop_worker,
)
from .security import INTERNAL_TOKEN_HEADER, internal_token
from .worker_socket import SiteWorkerSocketRuntime

LANE_AGENT_SOCKETS = "agent_sockets"


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


def _get_internal(secret: str, path: str, *, timeout: float = 15.0) -> Dict[str, Any]:
    response = requests.get(f"{_api_base_url()}{path}", headers=_headers(secret), timeout=timeout)
    response.raise_for_status()
    payload = response.json()
    return dict(payload or {}) if isinstance(payload, Mapping) else {}


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


def _attach_ansible_runner(socket_runtime: SiteWorkerSocketRuntime, logger, db_factory) -> None:
    ansible_runner = EngineAnsibleRunner(
        socketio=_NoopSocketIO(),
        db_conn_factory=db_factory,
        service_log=_service_log(logger),
        logger=logger.getChild("ansible.runner"),
    )
    if hasattr(socket_runtime, "set_ansible_runner"):
        socket_runtime.set_ansible_runner(ansible_runner)


def _agent_online_window_seconds() -> int:
    try:
        return max(60, int(str(os.environ.get("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS") or "300").strip() or "300"))
    except Exception:
        return 300


def _online_site_device_count(secret: str, *, site_id: int, window_seconds: Optional[int] = None) -> int:
    if int(site_id or 0) <= 0:
        return 0
    window = int(window_seconds or _agent_online_window_seconds())
    try:
        payload = _get_internal(
            secret,
            f"/api/internal/job-scheduler/online-sites?site_id={int(site_id)}&window_seconds={window}",
            timeout=10.0,
        )
    except Exception:
        return 0
    counts = payload.get("site_online_device_counts") if isinstance(payload.get("site_online_device_counts"), Mapping) else {}
    try:
        return max(0, int(counts.get(str(int(site_id))) or 0))
    except Exception:
        return 0


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


def main() -> None:
    settings = load_runtime_config()
    logger = initialise_engine_logger(settings, name="borealis.site_worker")
    secret = str(settings.secret_key or "")
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
    idle_ttl = max(300, int(str(os.environ.get("BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS") or "300").strip() or "300"))
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
    _attach_ansible_runner(socket_runtime, logger, db_factory)
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

    def connected_device_count() -> int:
        try:
            return socket_runtime.registered_device_count()
        except Exception:
            return 0

    def online_device_count(_conn: sqlite3.Connection) -> int:
        return _online_site_device_count(secret, site_id=site_id)

    try:
        while True:
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
                    claimed_count=0,
                )
                conn.commit()
            finally:
                conn.close()
            if idle_since is not None and (_now_ts() - idle_since) >= idle_ttl:
                logger.info("site worker idle ttl reached; exiting")
                return
            time.sleep(3.0)
    finally:
        conn = db_factory()
        try:
            stop_worker(conn, worker_guid=worker_guid)
            conn.commit()
        finally:
            conn.close()


if __name__ == "__main__":
    main()
