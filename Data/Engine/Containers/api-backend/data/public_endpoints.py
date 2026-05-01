"""Helpers for resolving Borealis public HTTPS and WireGuard endpoints."""

from __future__ import annotations

from typing import Any, Mapping, Optional
from urllib.parse import urlencode, urlsplit, urlunsplit


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _request_scheme(req: Any) -> str:
    try:
        if getattr(req, "is_secure", False):
            return "https"
    except Exception:
        pass
    try:
        forwarded = _normalize_text(req.headers.get("X-Forwarded-Proto"))
        if forwarded:
            return forwarded.split(",")[0].strip().lower() or "https"
    except Exception:
        pass
    return "https"


def _request_host(req: Any) -> str:
    try:
        forwarded = _normalize_text(req.headers.get("X-Forwarded-Host") or req.headers.get("X-Original-Host"))
        host = forwarded.split(",")[0].strip() if forwarded else _normalize_text(getattr(req, "host", ""))
        if not host:
            return ""
        parsed = urlsplit(f"//{host}")
        if parsed.hostname:
            return parsed.hostname
    except Exception:
        pass
    return _normalize_text(getattr(req, "host", ""))


def public_base_url(context: Any, req: Optional[Any] = None) -> str:
    configured = _normalize_text(getattr(context, "public_base_url", None))
    if configured:
        return configured.rstrip("/")
    host = public_hostname(context, req=req)
    if not host:
        return ""
    scheme = _request_scheme(req) if req is not None else "https"
    port = int(getattr(context, "public_https_port", 443) or 443)
    netloc = host if port in {0, 443} else f"{host}:{port}"
    return urlunsplit((scheme, netloc, "", "", "")).rstrip("/")


def public_hostname(context: Any, req: Optional[Any] = None) -> str:
    candidates = (
        getattr(context, "public_hostname", None),
        getattr(context, "public_wireguard_host", None),
    )
    for candidate in candidates:
        text = _normalize_text(candidate)
        if text:
            return text
    if req is not None:
        return _request_host(req)
    return ""


def public_vnc_path(context: Any) -> str:
    path = _normalize_text(getattr(context, "public_vnc_path", None)) or "/remote-desktop/vnc"
    if not path.startswith("/"):
        path = f"/{path}"
    if len(path) > 1 and path.endswith("/"):
        path = path.rstrip("/")
    return path


def public_guacamole_vnc_path(context: Any) -> str:
    path = _normalize_text(getattr(context, "guacamole_vnc_ws_path", None))
    if not path:
        path = f"{public_vnc_path(context)}/guacamole"
    if not path.startswith("/"):
        path = f"/{path}"
    if len(path) > 1 and path.endswith("/"):
        path = path.rstrip("/")
    return path


def wireguard_endpoint(context: Any, req: Optional[Any] = None) -> tuple[str, int]:
    host = _normalize_text(getattr(context, "public_wireguard_host", None)) or public_hostname(context, req=req)
    port = int(getattr(context, "public_wireguard_port", None) or getattr(context, "wireguard_port", 30000))
    return host, port


def build_websocket_url(
    context: Any,
    req: Any,
    path: str,
    *,
    query: Optional[Mapping[str, Any]] = None,
) -> str:
    base = public_base_url(context, req=req)
    if not base:
        return ""
    scheme = "wss" if base.startswith("https://") else "ws"
    parts = urlsplit(base)
    query_string = urlencode({key: value for key, value in (query or {}).items() if value is not None})
    return urlunsplit((scheme, parts.netloc, path, query_string, ""))
