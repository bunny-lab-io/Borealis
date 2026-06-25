"""Agent-facing remote-operation route payload helpers."""

from __future__ import annotations

from typing import Any, Dict, Mapping, Optional

from ...public_endpoints import public_base_url
from ..job_scheduler.queue import active_worker_route_for_site, ensure_job_scheduler_tables


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_int(value: Any) -> Optional[int]:
    if value in (None, "", "null"):
        return None
    try:
        return int(value)
    except Exception:
        return None


def _context_config_value(context: Any, key: str) -> str:
    try:
        config = getattr(context, "config", None)
        if config is not None:
            return _normalize_text(config.get(key))
    except Exception:
        pass
    return ""


def _resolved_public_base_url(context: Any, req: Any) -> str:
    configured = _normalize_text(getattr(context, "public_base_url", None))
    if not configured:
        configured = _context_config_value(context, "PUBLIC_BASE_URL")
    if configured:
        return configured.rstrip("/")
    return public_base_url(context, req=req)


def join_url(base: str, path: str) -> str:
    base_value = _normalize_text(base).rstrip("/")
    path_value = _normalize_text(path)
    if not path_value:
        return base_value
    if not path_value.startswith("/"):
        path_value = f"/{path_value}"
    return f"{base_value}{path_value}"


def site_worker_route_urls(context: Any, req: Any, route: Mapping[str, Any]) -> Dict[str, str]:
    base = _resolved_public_base_url(context, req)
    worker_base = join_url(base, _normalize_text(route.get("route_path_prefix")))
    return {
        "base": worker_base,
        "socket_io": join_url(worker_base, "/socket.io/"),
    }


def fetch_active_site_worker_route(conn, *, site_id: int) -> Optional[Dict[str, Any]]:
    ensure_job_scheduler_tables(conn)
    route = active_worker_route_for_site(conn, site_id=int(site_id))
    return dict(route) if route else None


def build_agent_remote_ops_route_payload(
    context: Any,
    req: Any,
    *,
    site_id: Optional[int],
    route: Optional[Mapping[str, Any]],
    reason: str = "site_worker_unavailable",
) -> Dict[str, Any]:
    normalized_site_id = _normalize_int(site_id)
    if not route:
        return {
            "available": False,
            "site_id": normalized_site_id,
            "reason": _normalize_text(reason) or "site_worker_unavailable",
        }

    route_site_id = _normalize_int(route.get("site_id")) or normalized_site_id
    urls = site_worker_route_urls(context, req, route)
    return {
        "available": True,
        "site_id": route_site_id,
        "worker_guid": _normalize_text(route.get("worker_guid")),
        "route_generation": _normalize_int(route.get("generation")) or 0,
        "route_path_prefix": _normalize_text(route.get("route_path_prefix")),
        "base_url": urls["base"],
        "socket_url": urls["socket_io"],
        "urls": urls,
    }


__all__ = [
    "build_agent_remote_ops_route_payload",
    "fetch_active_site_worker_route",
    "join_url",
    "site_worker_route_urls",
]
