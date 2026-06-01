"""Internal site-worker bridge helpers for api-backend remote-operation brokers."""

from __future__ import annotations

from typing import Any, Dict, Mapping, Optional, Tuple

import requests

from ..auth.secrets import require_app_secret
from ..job_scheduler.queue import active_worker_route_for_site, ensure_job_scheduler_tables
from ..job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def worker_route_url(worker_route: Mapping[str, Any], path: str) -> str:
    scheme = _normalize_text(worker_route.get("upstream_scheme")) or "http"
    host = _normalize_text(worker_route.get("upstream_host")) or "127.0.0.1"
    try:
        port = int(worker_route.get("upstream_port") or 0)
    except Exception:
        port = 0
    if port <= 0:
        raise RuntimeError("site_worker_route_missing_port")
    normalized_path = _normalize_text(path)
    if not normalized_path.startswith("/"):
        normalized_path = f"/{normalized_path}"
    return f"{scheme}://{host}:{port}{normalized_path}"


def worker_route_for_device(
    adapters: Any,
    record: Mapping[str, Any],
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    try:
        site_id = int(record.get("site_id") or 0)
    except Exception:
        site_id = 0
    if site_id <= 0:
        return None, ({"error": "device_site_unassigned"}, 409)

    conn = None
    try:
        conn = adapters.db_conn_factory()
        ensure_job_scheduler_tables(conn)
        route = active_worker_route_for_site(conn, site_id=site_id)
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass
    if not route:
        return None, (
            {"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."},
            503,
        )
    return dict(route), None


def worker_internal_headers(app: Any) -> Dict[str, str]:
    return {
        INTERNAL_TOKEN_HEADER: internal_token(require_app_secret(app)),
        "Accept": "application/json",
    }


def post_worker_json(
    app: Any,
    worker_route: Mapping[str, Any],
    path: str,
    payload: Mapping[str, Any],
    *,
    timeout: float = 30.0,
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    try:
        response = requests.post(
            worker_route_url(worker_route, path),
            headers={**worker_internal_headers(app), "Content-Type": "application/json"},
            json=dict(payload or {}),
            timeout=max(1.0, float(timeout)),
        )
    except Exception:
        return None, ({"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}, 503)
    try:
        data = response.json() if response.headers.get("content-type", "").lower().startswith("application/json") else {}
    except Exception:
        data = {}
    if response.status_code >= 400:
        return None, (data if isinstance(data, dict) else {"error": "site_worker_error"}, int(response.status_code))
    return data if isinstance(data, dict) else {}, None


def worker_host_service_registered(
    app: Any,
    worker_route: Mapping[str, Any],
    *,
    hostname: str,
    service_mode: str = "system",
) -> bool:
    payload, error = post_worker_json(
        app,
        worker_route,
        "/remote-ops/host-service/status",
        {"hostname": hostname, "service_mode": service_mode or "system"},
        timeout=5.0,
    )
    if error:
        return False
    return bool((payload or {}).get("registered"))


def call_worker_host_service_event(
    app: Any,
    worker_route: Mapping[str, Any],
    *,
    hostname: str,
    service_mode: str,
    event: str,
    payload: Mapping[str, Any],
    timeout_seconds: float,
) -> Tuple[Optional[Any], Optional[Tuple[Dict[str, Any], int]]]:
    response_payload, error = post_worker_json(
        app,
        worker_route,
        "/remote-ops/host-service/call",
        {
            "hostname": hostname,
            "service_mode": service_mode or "system",
            "event_name": event,
            "payload": dict(payload or {}),
            "timeout_seconds": max(0.5, float(timeout_seconds)),
        },
        timeout=max(1.0, float(timeout_seconds) + 1.0),
    )
    if error:
        return None, error
    if not bool((response_payload or {}).get("called")):
        return None, None
    return (response_payload or {}).get("response"), None


def emit_worker_host_service_event(
    app: Any,
    worker_route: Mapping[str, Any],
    *,
    hostname: str,
    service_mode: str,
    event: str,
    payload: Mapping[str, Any],
) -> bool:
    response_payload, error = post_worker_json(
        app,
        worker_route,
        "/remote-ops/host-service/event",
        {
            "hostname": hostname,
            "service_mode": service_mode or "system",
            "event_name": event,
            "payload": dict(payload or {}),
        },
        timeout=5.0,
    )
    if error:
        return False
    return bool((response_payload or {}).get("emitted"))


__all__ = [
    "call_worker_host_service_event",
    "emit_worker_host_service_event",
    "post_worker_json",
    "worker_host_service_registered",
    "worker_internal_headers",
    "worker_route_for_device",
    "worker_route_url",
]
