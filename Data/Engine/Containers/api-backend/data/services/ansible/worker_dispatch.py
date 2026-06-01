"""Route Engine-side Ansible queue requests to active site-workers."""

from __future__ import annotations

import logging
from collections.abc import Sequence
from typing import Any, Dict, Mapping, Optional

from ..job_scheduler.queue import (
    WORKER_ROUTE_STATUS_ACTIVE,
    active_worker_route_for_site,
    ensure_job_scheduler_tables,
    list_worker_routes,
)
from ..remote_ops.worker_bridge import post_worker_json, worker_route_for_hostname


ENGINE_LOCAL_ALIASES = {"", "localhost", "127.0.0.1", "::1", "borealis-engine-01"}


def _coerce_site_id(value: Any) -> int:
    try:
        parsed = int(value)
    except Exception:
        return 0
    return parsed if parsed > 0 else 0


def _target_spec_site_ids(target_specifications: Sequence[Mapping[str, Any]]) -> set[int]:
    site_ids: set[int] = set()
    for spec in target_specifications or []:
        if not isinstance(spec, Mapping):
            continue
        site_id = _coerce_site_id(spec.get("site_id") or spec.get("siteId"))
        if site_id > 0:
            site_ids.add(site_id)
    return site_ids


def _clean_payload(payload: Mapping[str, Any]) -> Dict[str, Any]:
    allowed = {
        "hostname",
        "playbook_rel_path",
        "playbook_name",
        "playbook_abs_path",
        "playbook_content",
        "credential_id",
        "variable_values",
        "payload_files",
        "target_specifications",
        "runtime_files",
        "source",
        "activity_id",
        "scheduled_job_id",
        "scheduled_run_id",
        "scheduled_job_run_row_id",
        "connection",
    }
    return {key: payload.get(key) for key in allowed if key in payload}


class WorkerAnsibleDispatcher:
    """`EngineAnsibleRunner.queue_run` compatible dispatcher backed by site-workers."""

    def __init__(self, *, app: Any, adapters: Any, logger: Optional[logging.Logger] = None) -> None:
        self._app = app
        self._adapters = adapters
        self._logger = logger or logging.getLogger(__name__)

    def _active_route_for_site(self, site_id: int) -> Optional[Dict[str, Any]]:
        conn = None
        try:
            conn = self._adapters.db_conn_factory()
            ensure_job_scheduler_tables(conn)
            route = active_worker_route_for_site(conn, site_id=int(site_id))
            return dict(route) if route else None
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def _first_active_route(self) -> Optional[Dict[str, Any]]:
        conn = None
        try:
            conn = self._adapters.db_conn_factory()
            ensure_job_scheduler_tables(conn)
            routes = list_worker_routes(conn, statuses=[WORKER_ROUTE_STATUS_ACTIVE])
            return dict(routes[0]) if routes else None
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def _route_for_payload(self, payload: Mapping[str, Any]) -> Dict[str, Any]:
        target_specifications = payload.get("target_specifications")
        if not isinstance(target_specifications, Sequence) or isinstance(target_specifications, (str, bytes, bytearray)):
            target_specifications = []
        site_ids = _target_spec_site_ids([spec for spec in target_specifications if isinstance(spec, Mapping)])
        if len(site_ids) > 1:
            raise RuntimeError("Ansible worker dispatch requires one site per queued run.")
        if site_ids:
            site_id = next(iter(site_ids))
            route = self._active_route_for_site(site_id)
            if route:
                return route
            raise RuntimeError(f"No active site-worker route is available for site {site_id}.")

        hostname = str(payload.get("hostname") or "").strip()
        if hostname and hostname.lower() not in ENGINE_LOCAL_ALIASES:
            route, route_error, _record = worker_route_for_hostname(self._adapters, hostname)
            if route_error is None and route is not None:
                return dict(route)
            error_payload = route_error[0] if route_error else {}
            message = str(error_payload.get("message") or error_payload.get("error") or "site_worker_unavailable")
            raise RuntimeError(message)

        route = self._first_active_route()
        if route:
            return route
        raise RuntimeError("No active site-worker route is available for Engine-side Ansible execution.")

    def queue_run(self, **kwargs: Any) -> str:
        payload = _clean_payload(kwargs)
        route = self._route_for_payload(payload)
        response, error = post_worker_json(
            self._app,
            route,
            "/automation/ansible/run",
            {"queue_run": payload},
            timeout=10.0,
        )
        if error is not None:
            error_payload, _status = error
            message = str(error_payload.get("message") or error_payload.get("error") or "site_worker_ansible_dispatch_failed")
            raise RuntimeError(message)
        run_id = str((response or {}).get("run_id") or "").strip()
        if not run_id:
            raise RuntimeError("Site-worker did not return an Ansible run id.")
        self._logger.info(
            "queued ansible run through site-worker worker_guid=%s run_id=%s",
            route.get("worker_guid") or "-",
            run_id,
        )
        return run_id


__all__ = ["WorkerAnsibleDispatcher"]
