# ======================================================
# Data\Engine\services\API\devices\vnc.py
# Description: VNC session bootstrap for Apache Guacamole tunnels.
#
# API Endpoints (if applicable):
# - GET /api/vnc/viewers (Token Authenticated) - Report Apache Guacamole availability.
# - POST /api/vnc/establish (Token Authenticated) - Establish or join a collaboration-aware Apache Guacamole VNC session.
# - POST /api/vnc/disconnect (Token Authenticated) - Leave or close a collaboration-aware VNC session.
# - POST /api/vnc/handoff (Token Authenticated) - Reassign session-owner metadata to another session participant.
# - GET /api/vnc/sessions (Token Authenticated) - List active collaboration-aware VNC sessions.
# - POST /api/vnc/session (Token Authenticated) - Legacy alias for establish.
# ======================================================

"""VNC session bootstrap endpoints for the Borealis Engine."""
from __future__ import annotations

import os
import socket
import threading
import time
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....public_endpoints import build_websocket_url, public_guacamole_vnc_path, wireguard_endpoint
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...RemoteDesktop.guacamole_proxy import guacd_health, normalize_guacamole_performance_preference
from ...RemoteDesktop.rfb_probe import (
    VncAuthProbeResult as _VncAuthProbeResult,
    wait_for_vnc_auth_ready as _wait_for_backend_auth_ready,
)
from ...RemoteDesktop.vnc_proxy import ensure_guacamole_vnc_proxy
from ...RemoteDesktop.vnc_sessions import ensure_vnc_collaboration_manager
from .tunnel import _get_tunnel_service, _resolve_requested_agent_id

if False:  # pragma: no cover - hint for type checkers
    from .. import EngineServiceAdapters


_VNC_AUTH_RATE_LIMIT_LOCK = threading.Lock()
_VNC_AUTH_RATE_LIMITS: dict[str, tuple[float, str]] = {}
_VNC_AUTH_REFRESH_BACKOFF_LOCK = threading.Lock()
_VNC_AUTH_REFRESH_BACKOFFS: dict[str, tuple[float, str]] = {}


def _current_user(app) -> Optional[Dict[str, str]]:
    username = session.get("username")
    role = session.get("role") or "User"
    if username:
        return {"username": username, "role": role}

    token = None
    auth_header = request.headers.get("Authorization") or ""
    if auth_header.lower().startswith("bearer "):
        token = auth_header.split(" ", 1)[1].strip()
    if not token:
        token = request.cookies.get("borealis_auth")
    if not token:
        return None

    try:
        serializer = URLSafeTimedSerializer(require_app_secret(app), salt="borealis-auth")
        token_ttl = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
        data = serializer.loads(token, max_age=token_ttl)
        username = data.get("u")
        role = data.get("r") or "User"
        if username:
            return {"username": username, "role": role}
    except (BadSignature, SignatureExpired, Exception):
        return None
    return None


def _require_login(app) -> Optional[Tuple[Dict[str, Any], int]]:
    user = _current_user(app)
    if not user:
        return {"error": "unauthorized"}, 401
    return None


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    if isinstance(value, str):
        cleaned = value.strip().lower()
        if cleaned in {"1", "true", "yes", "y", "on"}:
            return True
        if cleaned in {"0", "false", "no", "n", "off"}:
            return False
    return default


def _clone_display_topology(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    cloned: list[dict[str, Any]] = []
    for item in value:
        if isinstance(item, dict):
            cloned.append(dict(item))
    return cloned


def _clone_display_virtual_bounds(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict):
        return {}
    return dict(value)


def _normalize_viewer(value: Any) -> str:
    viewer = _normalize_text(value).lower()
    if viewer in {"guacamole", "apache-guacamole", "apache_guacamole", "guac"}:
        return "guacamole"
    if not viewer:
        return "guacamole"
    return viewer


def _initial_display_size(bounds: Any, topology: Any) -> tuple[int, int]:
    if isinstance(bounds, dict):
        try:
            width = int(bounds.get("width") or 0)
            height = int(bounds.get("height") or 0)
            if width > 0 and height > 0:
                return width, height
        except Exception:
            pass
    if isinstance(topology, list):
        for item in topology:
            if not isinstance(item, dict):
                continue
            try:
                width = int(item.get("width") or 0)
                height = int(item.get("height") or 0)
                if width > 0 and height > 0:
                    return width, height
            except Exception:
                continue
    return 1024, 768


def _coerce_timeout(value: Any, default: float) -> float:
    try:
        parsed = float(value)
    except Exception:
        return default
    if parsed <= 0:
        return default
    return parsed


def _coerce_nonnegative_timeout(value: Any, default: float) -> float:
    try:
        parsed = float(value)
    except Exception:
        return default
    if parsed < 0:
        return default
    return parsed


def _is_vnc_auth_rate_limited(reason: Any) -> bool:
    normalized = _normalize_text(reason).replace("_", " ").lower()
    return ("too many" in normalized or "to many" in normalized) and (
        "attempt" in normalized or "auth" in normalized
    )


def _clear_vnc_auth_rate_limits() -> None:
    with _VNC_AUTH_RATE_LIMIT_LOCK:
        _VNC_AUTH_RATE_LIMITS.clear()
    with _VNC_AUTH_REFRESH_BACKOFF_LOCK:
        _VNC_AUTH_REFRESH_BACKOFFS.clear()


def _remember_vnc_auth_rate_limit(agent_id: Any, *, retry_after_seconds: float, reason: Any) -> float:
    key = _normalize_text(agent_id)
    retry_after = max(0.0, float(retry_after_seconds or 0.0))
    if not key or retry_after <= 0:
        return retry_after
    with _VNC_AUTH_RATE_LIMIT_LOCK:
        _VNC_AUTH_RATE_LIMITS[key] = (
            time.monotonic() + retry_after,
            _normalize_text(reason) or "vnc_auth_rate_limited",
        )
    return retry_after


def _cached_vnc_auth_rate_limit(agent_id: Any) -> Optional[dict[str, Any]]:
    key = _normalize_text(agent_id)
    if not key:
        return None
    now = time.monotonic()
    with _VNC_AUTH_RATE_LIMIT_LOCK:
        entry = _VNC_AUTH_RATE_LIMITS.get(key)
        if entry is None:
            return None
        retry_at, reason = entry
        remaining = retry_at - now
        if remaining <= 0:
            _VNC_AUTH_RATE_LIMITS.pop(key, None)
            return None
    return {
        "reason": reason or "vnc_auth_rate_limited",
        "retry_after_seconds": max(0.0, remaining),
    }


def _remember_vnc_auth_refresh_backoff(agent_id: Any, *, retry_after_seconds: float, reason: Any) -> float:
    key = _normalize_text(agent_id)
    retry_after = max(0.0, float(retry_after_seconds or 0.0))
    if not key or retry_after <= 0:
        return retry_after
    with _VNC_AUTH_REFRESH_BACKOFF_LOCK:
        _VNC_AUTH_REFRESH_BACKOFFS[key] = (
            time.monotonic() + retry_after,
            _normalize_text(reason) or "vnc_backend_auth_refresh_pending",
        )
    return retry_after


def _cached_vnc_auth_refresh_backoff(agent_id: Any) -> Optional[dict[str, Any]]:
    key = _normalize_text(agent_id)
    if not key:
        return None
    now = time.monotonic()
    with _VNC_AUTH_REFRESH_BACKOFF_LOCK:
        entry = _VNC_AUTH_REFRESH_BACKOFFS.get(key)
        if entry is None:
            return None
        retry_at, reason = entry
        remaining = retry_at - now
        if remaining <= 0:
            _VNC_AUTH_REFRESH_BACKOFFS.pop(key, None)
            return None
    return {
        "reason": reason or "vnc_backend_auth_refresh_pending",
        "retry_after_seconds": max(0.0, remaining),
    }


def _probe_tcp_listener(host: str, port: int, timeout_seconds: float) -> bool:
    host_value = _normalize_text(host)
    try:
        port_value = int(port)
    except Exception:
        return False
    if not host_value or port_value < 1 or port_value > 65535:
        return False
    try:
        with socket.create_connection((host_value, port_value), timeout=max(0.1, timeout_seconds)):
            return True
    except Exception:
        return False


def _wait_for_agent_credential(
    manager: Any,
    agent_id: str,
    *,
    timeout_seconds: float,
    poll_interval_seconds: float = 0.25,
    previous_revision: Optional[int] = None,
    previous_password: Optional[str] = None,
) -> Optional[Any]:
    deadline = time.monotonic() + max(0.25, timeout_seconds)
    last_credential: Optional[Any] = None
    previous_password_value = _normalize_text(previous_password)[:8] if previous_password is not None else ""
    while time.monotonic() < deadline:
        credential = manager.get_agent_credential(agent_id)
        if credential is not None and _normalize_text(getattr(credential, "controller_password", "")):
            last_credential = credential
            if previous_revision is None and previous_password is None:
                return credential
            try:
                revision_changed = int(getattr(credential, "credential_revision", 0) or 0) > int(previous_revision or 0)
            except Exception:
                revision_changed = False
            password_changed = (
                bool(previous_password_value)
                and _normalize_text(getattr(credential, "controller_password", ""))[:8] != previous_password_value
            )
            if revision_changed or password_changed:
                return credential
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        time.sleep(min(max(0.05, poll_interval_seconds), remaining))
    if previous_revision is not None or previous_password is not None:
        return None
    return last_credential


def _wait_for_backend_ready(
    host: str,
    port: int,
    *,
    timeout_seconds: float,
    poll_interval_seconds: float,
) -> bool:
    deadline = time.monotonic() + max(0.5, timeout_seconds)
    while time.monotonic() < deadline:
        if _probe_tcp_listener(host, port, min(0.75, timeout_seconds)):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        time.sleep(min(max(0.1, poll_interval_seconds), remaining))
    return False


def _recent_backend_ready_age_seconds(collaboration_session: Any) -> Optional[float]:
    try:
        last_ready = float(getattr(collaboration_session, "last_backend_ready_at", 0.0) or 0.0)
    except Exception:
        return None
    if last_ready <= 0:
        return None
    return max(0.0, float(time.time()) - last_ready)


def _ready_wait_profile(
    *,
    collaboration_session: Any,
    created: bool,
    recent_credential_refresh: bool = False,
    credential_cached: bool = False,
    socket_registered: Optional[bool] = None,
) -> Dict[str, Any]:
    cold_ready_wait_seconds = _coerce_timeout(
        os.environ.get("BOREALIS_VNC_READY_WAIT_SECONDS"),
        12.0,
    )
    cold_retry_wait_seconds = _coerce_timeout(
        os.environ.get("BOREALIS_VNC_RETRY_READY_WAIT_SECONDS"),
        8.0,
    )
    cold_poll_seconds = _coerce_timeout(
        os.environ.get("BOREALIS_VNC_READY_POLL_INTERVAL_SECONDS"),
        0.35,
    )
    warm_session_window_seconds = _coerce_timeout(
        os.environ.get("BOREALIS_VNC_WARM_SESSION_WINDOW_SECONDS"),
        75.0,
    )
    recent_backend_ready_age = _recent_backend_ready_age_seconds(collaboration_session)
    recent_backend_ready = (
        recent_backend_ready_age is not None
        and recent_backend_ready_age <= warm_session_window_seconds
    )
    warm_reconnect = bool((not created) and recent_backend_ready)
    if warm_reconnect:
        warm_ready_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_WARM_READY_WAIT_SECONDS"),
            min(cold_ready_wait_seconds, 3.0),
        )
        warm_retry_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_WARM_RETRY_READY_WAIT_SECONDS"),
            min(cold_retry_wait_seconds, 3.0),
        )
        warm_poll_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_WARM_READY_POLL_INTERVAL_SECONDS"),
            min(cold_poll_seconds, 0.2),
        )
        return {
            "mode": "warm_reconnect",
            "initial_wait_seconds": warm_ready_wait_seconds,
            "retry_wait_seconds": warm_retry_wait_seconds,
            "poll_seconds": warm_poll_seconds,
            "post_bootstrap_grace_seconds": _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_WARM_POST_BOOTSTRAP_GRACE_SECONDS"),
                1.5,
            ),
            "post_bootstrap_grace_poll_seconds": _coerce_timeout(
                os.environ.get("BOREALIS_VNC_WARM_POST_BOOTSTRAP_GRACE_POLL_INTERVAL_SECONDS"),
                min(warm_poll_seconds, 0.15),
            ),
            "soft_retry_wait_seconds": _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_WARM_SOFT_RETRY_WAIT_SECONDS"),
                1.5,
            ),
            "soft_retry_poll_seconds": _coerce_timeout(
                os.environ.get("BOREALIS_VNC_WARM_SOFT_RETRY_POLL_INTERVAL_SECONDS"),
                min(warm_poll_seconds, 0.15),
            ),
            "recent_backend_ready": True,
            "recent_backend_ready_age": recent_backend_ready_age,
            "warm_session_window_seconds": warm_session_window_seconds,
        }
    if recent_credential_refresh:
        refresh_ready_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_REFRESH_READY_WAIT_SECONDS"),
            min(cold_ready_wait_seconds, 3.0),
        )
        refresh_retry_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_REFRESH_RETRY_READY_WAIT_SECONDS"),
            min(cold_retry_wait_seconds, 3.0),
        )
        refresh_poll_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_REFRESH_READY_POLL_INTERVAL_SECONDS"),
            min(cold_poll_seconds, 0.2),
        )
        return {
            "mode": "fresh_refresh",
            "initial_wait_seconds": refresh_ready_wait_seconds,
            "retry_wait_seconds": refresh_retry_wait_seconds,
            "poll_seconds": refresh_poll_seconds,
            "post_bootstrap_grace_seconds": _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_REFRESH_POST_BOOTSTRAP_GRACE_SECONDS"),
                1.0,
            ),
            "post_bootstrap_grace_poll_seconds": _coerce_timeout(
                os.environ.get("BOREALIS_VNC_REFRESH_POST_BOOTSTRAP_GRACE_POLL_INTERVAL_SECONDS"),
                min(refresh_poll_seconds, 0.15),
            ),
            "soft_retry_wait_seconds": _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_REFRESH_SOFT_RETRY_WAIT_SECONDS"),
                1.0,
            ),
            "soft_retry_poll_seconds": _coerce_timeout(
                os.environ.get("BOREALIS_VNC_REFRESH_SOFT_RETRY_POLL_INTERVAL_SECONDS"),
                min(refresh_poll_seconds, 0.15),
            ),
            "recent_backend_ready": bool(recent_backend_ready),
            "recent_backend_ready_age": recent_backend_ready_age,
            "warm_session_window_seconds": warm_session_window_seconds,
        }
    if credential_cached and socket_registered is True:
        socket_ready_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_SOCKET_READY_WAIT_SECONDS"),
            min(cold_ready_wait_seconds, 3.0),
        )
        socket_retry_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_SOCKET_RETRY_READY_WAIT_SECONDS"),
            min(cold_retry_wait_seconds, 3.0),
        )
        socket_poll_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_SOCKET_READY_POLL_INTERVAL_SECONDS"),
            min(cold_poll_seconds, 0.2),
        )
        return {
            "mode": "socket_online",
            "initial_wait_seconds": socket_ready_wait_seconds,
            "retry_wait_seconds": socket_retry_wait_seconds,
            "poll_seconds": socket_poll_seconds,
            "post_bootstrap_grace_seconds": _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_SECONDS"),
                1.5,
            ),
            "post_bootstrap_grace_poll_seconds": _coerce_timeout(
                os.environ.get("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_POLL_INTERVAL_SECONDS"),
                min(socket_poll_seconds, 0.15),
            ),
            "soft_retry_wait_seconds": _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_SOCKET_SOFT_RETRY_WAIT_SECONDS"),
                1.5,
            ),
            "soft_retry_poll_seconds": _coerce_timeout(
                os.environ.get("BOREALIS_VNC_SOCKET_SOFT_RETRY_POLL_INTERVAL_SECONDS"),
                min(socket_poll_seconds, 0.15),
            ),
            "recent_backend_ready": bool(recent_backend_ready),
            "recent_backend_ready_age": recent_backend_ready_age,
            "warm_session_window_seconds": warm_session_window_seconds,
        }
    return {
        "mode": "standard",
        "initial_wait_seconds": cold_ready_wait_seconds,
        "retry_wait_seconds": cold_retry_wait_seconds,
        "poll_seconds": cold_poll_seconds,
        "post_bootstrap_grace_seconds": 0.0,
        "post_bootstrap_grace_poll_seconds": cold_poll_seconds,
        "soft_retry_wait_seconds": 0.0,
        "soft_retry_poll_seconds": cold_poll_seconds,
        "recent_backend_ready": bool(recent_backend_ready),
        "recent_backend_ready_age": recent_backend_ready_age,
        "warm_session_window_seconds": warm_session_window_seconds,
    }


def _context_emit_agent_event(context: Any, agent_id: str, event: str, payload: Dict[str, Any]) -> bool:
    emitter = getattr(context, "emit_agent_event", None)
    if not callable(emitter):
        return False
    try:
        return bool(emitter(agent_id, event, payload))
    except Exception:
        if hasattr(context, "logger"):
            context.logger.debug("Failed to emit agent event %s for %s", event, agent_id, exc_info=True)
        return False


def _agent_socket_registered(context: Any, agent_id: str) -> Optional[bool]:
    registry = getattr(context, "agent_socket_registry", None)
    if registry is None or not hasattr(registry, "is_registered"):
        return None
    try:
        return bool(registry.is_registered(agent_id))
    except Exception:
        if hasattr(context, "logger"):
            context.logger.debug("Failed to inspect agent socket registration for %s", agent_id, exc_info=True)
        return None


def _should_prewarm_vnc_backend(
    *,
    had_tunnel_payload: bool,
    socket_registered: Optional[bool],
    wait_profile: Dict[str, Any],
) -> bool:
    if not had_tunnel_payload or socket_registered is not True:
        return False
    return _normalize_text(wait_profile.get("mode")) in {
        "fresh_refresh",
        "socket_online",
        "warm_reconnect",
    }


def register_vnc(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("vnc", __name__)
    logger = adapters.context.logger.getChild("vnc.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("VNC", message, level=level)
        except Exception:
            logger.debug("vnc service log write failed", exc_info=True)

    def _trace(step: str, *, level: str = "INFO", **fields: Any) -> None:
        parts = [f"vnc_trace step={_normalize_text(step) or '-'}"]
        for key, value in fields.items():
            if isinstance(value, bool):
                normalized = "true" if value else "false"
            elif value is None:
                normalized = "-"
            else:
                normalized = _normalize_text(value) or "-"
            normalized = normalized.replace(" ", "_")
            parts.append(f"{key}={normalized}")
        _service_log_event(" ".join(parts), level=level)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    def _resolve_allowed_ips(
        *,
        session_payload: Optional[Dict[str, Any]] = None,
        collaboration_session: Optional[Any] = None,
    ) -> str:
        if collaboration_session is not None:
            value = (
                getattr(collaboration_session, "allowed_ips", None)
                or getattr(collaboration_session, "engine_virtual_ip", None)
            )
            normalized = _normalize_text(value)
            if normalized:
                return normalized
        payload = session_payload if isinstance(session_payload, dict) else {}
        value = payload.get("allowed_ips") or payload.get("engine_virtual_ip")
        if isinstance(value, list):
            value = value[0] if value else ""
        return _normalize_text(value)

    def _issue_session(
        agent_id: str,
        operator_id: Optional[str],
        *,
        remove_wallpaper: bool,
        viewer: str = "guacamole",
        performance_preference: int = 0,
    ) -> Tuple[Dict[str, Any], int]:
        normalized_viewer = _normalize_viewer(viewer)
        if normalized_viewer != "guacamole":
            return {"error": "invalid_viewer"}, 400
        normalized_performance_preference = normalize_guacamole_performance_preference(performance_preference)
        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        _trace(
            "E01",
            agent_id=agent_id,
            operator_id=operator_id or "-",
            remote=_request_remote() or "-",
            remove_wallpaper=remove_wallpaper,
            viewer=normalized_viewer,
            performance_preference=normalized_performance_preference,
        )
        agent_credential = manager.get_agent_credential(agent_id)
        recent_credential_refresh = False
        _trace(
            "E02",
            agent_id=agent_id,
            credential_cached=agent_credential is not None,
            credential_revision=(getattr(agent_credential, "credential_revision", 0) if agent_credential else 0),
            topology_count=(len(getattr(agent_credential, "display_topology", []) or []) if agent_credential else 0),
        )
        if agent_credential is None or not _normalize_text(agent_credential.controller_password):
            refresh_wait_seconds = _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_CREDENTIAL_REFRESH_WAIT_SECONDS"),
                4.0,
            )
            refresh_requested = _context_emit_agent_event(
                adapters.context,
                agent_id,
                "vnc_refresh",
                {"agent_id": agent_id, "reason": "engine_credential_refresh"},
            )
            _trace(
                "E03",
                agent_id=agent_id,
                refresh_emit=refresh_requested,
                wait_seconds=refresh_wait_seconds,
            )
            if refresh_requested:
                agent_credential = _wait_for_agent_credential(
                    manager,
                    agent_id,
                    timeout_seconds=refresh_wait_seconds,
                )
                if agent_credential is not None and _normalize_text(getattr(agent_credential, "controller_password", "")):
                    recent_credential_refresh = True
            _trace(
                "E04",
                agent_id=agent_id,
                credential_after_refresh=agent_credential is not None,
                credential_revision=(getattr(agent_credential, "credential_revision", 0) if agent_credential else 0),
                topology_count=(len(getattr(agent_credential, "display_topology", []) or []) if agent_credential else 0),
            )
            if agent_credential is None or not _normalize_text(agent_credential.controller_password):
                _trace("E04F", agent_id=agent_id, result="vnc_agent_credentials_unavailable", level="WARNING")
                return {"error": "vnc_agent_credentials_unavailable"}, 503
        try:
            collaboration_session, participant, _created = manager.ensure_session(
                agent_id=agent_id,
                operator_id=operator_id or "",
                controller_password=agent_credential.controller_password,
                credential_revision=agent_credential.credential_revision,
                remove_wallpaper=remove_wallpaper,
            )
        except ValueError:
            _trace("E05F", agent_id=agent_id, result="operator_required", level="WARNING")
            return {"error": "operator_required"}, 401
        _trace(
            "E05",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            participant_id=participant.participant_id,
            created=_created,
            session_state=collaboration_session.state,
            credential_revision=collaboration_session.credential_revision,
        )
        _ = remove_wallpaper
        tunnel_service = _get_tunnel_service(adapters)
        session_payload = tunnel_service.session_payload(agent_id, include_token=False)
        had_tunnel_payload = bool(session_payload)
        if not session_payload:
            try:
                session_payload = tunnel_service.connect(
                    agent_id=agent_id,
                    operator_id=operator_id,
                    endpoint_host=wireguard_endpoint(adapters.context, req=request)[0],
                )
            except Exception:
                _trace("E06F", agent_id=agent_id, result="tunnel_down", level="WARNING")
                return {"error": "tunnel_down"}, 409
        allowed_ips = _resolve_allowed_ips(session_payload=session_payload)
        engine_virtual_ip = _normalize_text(session_payload.get("engine_virtual_ip"))
        vnc_port = int(getattr(adapters.context, "vnc_port", 5900))
        virtual_ip = _normalize_text(session_payload.get("virtual_ip"))
        host = virtual_ip.split("/")[0] if virtual_ip else ""
        _trace(
            "E06",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            tunnel_payload_cached=had_tunnel_payload,
            tunnel_id=session_payload.get("tunnel_id"),
            virtual_ip=virtual_ip or "-",
            backend_host=host or "-",
            engine_virtual_ip=engine_virtual_ip or "-",
            allowed_ips=allowed_ips or "-",
            vnc_port=vnc_port,
        )
        if not host:
            _trace("E06X", agent_id=agent_id, session_id=collaboration_session.session_id, result="virtual_ip_missing", level="WARNING")
            return {"error": "virtual_ip_missing"}, 500

        def _restart_tunnel(reason: str) -> None:
            force_restart = str(reason or "").strip().lower() == "vnc_connect_retry"
            tunnel_service.mark_transport_required(agent_id, reason=reason)
            tunnel_service.request_agent_start(
                agent_id,
                force_restart=force_restart,
                reason=reason,
            )
            if force_restart:
                tunnel_service.recover_transport(
                    agent_id,
                    trigger="vnc_connect",
                    reason=reason,
                )

        def _confirm_transport(reason: str) -> None:
            tunnel_service.confirm_transport_success(agent_id, reason=reason)

        def _emit_vnc_start(reason: str) -> bool:
            return _context_emit_agent_event(
                adapters.context,
                agent_id,
                "vnc_start",
                {
                    "agent_id": agent_id,
                    "session_id": collaboration_session.session_id,
                    "controller_password": collaboration_session.controller_password,
                    "view_only_password": "",
                    "port": int(getattr(adapters.context, "vnc_port", 5900)),
                    "allowed_ips": allowed_ips,
                    "remove_wallpaper": bool(remove_wallpaper),
                    "credential_revision": collaboration_session.credential_revision,
                    "reason": reason,
                },
            )

        socket_registered = _agent_socket_registered(adapters.context, agent_id)
        wait_profile = _ready_wait_profile(
            collaboration_session=collaboration_session,
            created=_created,
            recent_credential_refresh=recent_credential_refresh,
            credential_cached=agent_credential is not None,
            socket_registered=socket_registered,
        )
        fast_ready_wait = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_FAST_READY_WAIT_SECONDS"),
            0.75,
        )
        fast_ready_poll = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_FAST_READY_POLL_INTERVAL_SECONDS"),
            0.15,
        )
        prewarm_backend = _should_prewarm_vnc_backend(
            had_tunnel_payload=had_tunnel_payload,
            socket_registered=socket_registered,
            wait_profile=wait_profile,
        )
        if prewarm_backend:
            fast_ready_wait = max(
                fast_ready_wait,
                _coerce_timeout(
                    os.environ.get("BOREALIS_VNC_PREWARM_FAST_READY_WAIT_SECONDS"),
                    2.0,
                ),
            )
            fast_ready_poll = min(
                fast_ready_poll,
                _coerce_timeout(
                    os.environ.get("BOREALIS_VNC_PREWARM_FAST_READY_POLL_INTERVAL_SECONDS"),
                    0.15,
                ),
            )
        _trace(
            "E07",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            backend_host=host,
            vnc_port=vnc_port,
            fast_wait_seconds=fast_ready_wait,
            fast_poll_seconds=fast_ready_poll,
            ready_profile=wait_profile["mode"],
            ready_wait_seconds=wait_profile["initial_wait_seconds"],
            retry_wait_seconds=wait_profile["retry_wait_seconds"],
            ready_poll_seconds=wait_profile["poll_seconds"],
            recent_backend_ready=wait_profile["recent_backend_ready"],
            recent_backend_ready_age=(
                round(float(wait_profile["recent_backend_ready_age"]), 3)
                if isinstance(wait_profile["recent_backend_ready_age"], (int, float))
                else "-"
            ),
        )
        if prewarm_backend:
            prewarm_reason = "vnc_backend_prewarm"
            prewarm_ok = False
            try:
                _restart_tunnel(prewarm_reason)
                prewarm_ok = True
            except Exception:
                logger.debug("Failed to prewarm VNC backend tunnel agent_id=%s", agent_id, exc_info=True)
            _trace(
                "E07P",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                reason=prewarm_reason,
                prewarm_ok=prewarm_ok,
                ready_profile=wait_profile["mode"],
                fast_wait_seconds=fast_ready_wait,
            )
        fast_ready = _wait_for_backend_ready(
            host,
            vnc_port,
            timeout_seconds=fast_ready_wait,
            poll_interval_seconds=fast_ready_poll,
        )
        initial_ready = fast_ready
        _trace(
            "E08",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            fast_ready=fast_ready,
        )
        if fast_ready:
            _service_log_event(
                "vnc_backend_fast_ready agent_id={0} session_id={1} credential_revision={2}".format(
                    agent_id,
                    collaboration_session.session_id,
                    collaboration_session.credential_revision,
                )
            )
        else:
            _trace(
                "E09",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                socket_registered=socket_registered if socket_registered is not None else "unknown",
            )
            if socket_registered is False:
                _service_log_event(
                    "vnc_backend_bootstrap_blocked agent_id={0} session_id={1} reason=agent_socket_missing".format(
                        agent_id,
                        collaboration_session.session_id,
                    ),
                    level="WARNING",
                )
                _trace("E09F", agent_id=agent_id, session_id=collaboration_session.session_id, result="agent_socket_missing", level="WARNING")
                return {"error": "agent_socket_missing"}, 409
            _service_log_event(
                "vnc_backend_bootstrap_required agent_id={0} session_id={1} credential_revision={2}".format(
                    agent_id,
                    collaboration_session.session_id,
                    collaboration_session.credential_revision,
                )
            )
            ready_wait_seconds = float(wait_profile["initial_wait_seconds"])
            ready_poll_seconds = float(wait_profile["poll_seconds"])
            try:
                _trace("E10", agent_id=agent_id, session_id=collaboration_session.session_id, reason="vnc_bootstrap")
                _restart_tunnel("vnc_bootstrap")
                emitted = _emit_vnc_start("vnc_bootstrap")
                settle_seconds = _coerce_nonnegative_timeout(
                    os.environ.get("BOREALIS_VNC_BOOTSTRAP_SETTLE_SECONDS"),
                    0.0,
                )
                _trace(
                    "E11",
                    agent_id=agent_id,
                    session_id=collaboration_session.session_id,
                    reason="vnc_bootstrap",
                    emit_ok=emitted,
                    settle_seconds=settle_seconds,
                    created=_created,
                )
                if not emitted:
                    _service_log_event(
                        "vnc_backend_bootstrap_blocked agent_id={0} session_id={1} reason=agent_socket_missing".format(
                            agent_id,
                            collaboration_session.session_id,
                        ),
                        level="WARNING",
                    )
                    _trace("E11F", agent_id=agent_id, session_id=collaboration_session.session_id, result="agent_socket_missing", level="WARNING")
                    return {"error": "agent_socket_missing"}, 409
                if _created and settle_seconds > 0:
                    time.sleep(settle_seconds)
            except Exception:
                logger.debug("Failed to re-emit vpn_tunnel_start before VNC bootstrap", exc_info=True)

            initial_ready = _wait_for_backend_ready(
                host,
                vnc_port,
                timeout_seconds=ready_wait_seconds,
                poll_interval_seconds=ready_poll_seconds,
            )
            _trace(
                "E12",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                ready=initial_ready,
                wait_seconds=ready_wait_seconds,
                poll_seconds=ready_poll_seconds,
            )
            if not initial_ready:
                post_bootstrap_grace_seconds = float(wait_profile.get("post_bootstrap_grace_seconds", 0.0) or 0.0)
                post_bootstrap_grace_poll_seconds = float(
                    wait_profile.get("post_bootstrap_grace_poll_seconds", wait_profile["poll_seconds"]) or wait_profile["poll_seconds"]
                )
                if post_bootstrap_grace_seconds > 0:
                    initial_ready = _wait_for_backend_ready(
                        host,
                        vnc_port,
                        timeout_seconds=post_bootstrap_grace_seconds,
                        poll_interval_seconds=post_bootstrap_grace_poll_seconds,
                    )
                    _trace(
                        "E12G",
                        agent_id=agent_id,
                        session_id=collaboration_session.session_id,
                        ready=initial_ready,
                        wait_seconds=post_bootstrap_grace_seconds,
                        poll_seconds=post_bootstrap_grace_poll_seconds,
                    )
            if not initial_ready:
                soft_retry_wait_seconds = float(wait_profile.get("soft_retry_wait_seconds", 0.0) or 0.0)
                soft_retry_poll_seconds = float(
                    wait_profile.get("soft_retry_poll_seconds", wait_profile["poll_seconds"]) or wait_profile["poll_seconds"]
                )
                if soft_retry_wait_seconds > 0:
                    try:
                        _trace(
                            "E13S",
                            agent_id=agent_id,
                            session_id=collaboration_session.session_id,
                            reason="vnc_connect_retry_soft",
                        )
                        _restart_tunnel("vnc_connect_retry_soft")
                        emitted = _emit_vnc_start("vnc_connect_retry_soft")
                        _trace(
                            "E14S",
                            agent_id=agent_id,
                            session_id=collaboration_session.session_id,
                            reason="vnc_connect_retry_soft",
                            emit_ok=emitted,
                        )
                        if not emitted:
                            _service_log_event(
                                "vnc_backend_soft_retry_blocked agent_id={0} session_id={1} reason=agent_socket_missing".format(
                                    agent_id,
                                    collaboration_session.session_id,
                                ),
                                level="WARNING",
                            )
                            _trace(
                                "E14SF",
                                agent_id=agent_id,
                                session_id=collaboration_session.session_id,
                                result="agent_socket_missing",
                                level="WARNING",
                            )
                            return {"error": "agent_socket_missing"}, 409
                    except Exception:
                        logger.debug("Failed to request VNC soft retry agent_id=%s", agent_id, exc_info=True)
                    initial_ready = _wait_for_backend_ready(
                        host,
                        vnc_port,
                        timeout_seconds=soft_retry_wait_seconds,
                        poll_interval_seconds=soft_retry_poll_seconds,
                    )
                    _trace(
                        "E15S",
                        agent_id=agent_id,
                        session_id=collaboration_session.session_id,
                        ready=initial_ready,
                        wait_seconds=soft_retry_wait_seconds,
                        poll_seconds=soft_retry_poll_seconds,
                    )
        if not initial_ready:
            retry_wait_seconds = float(wait_profile["retry_wait_seconds"])
            retry_poll_seconds = float(wait_profile["poll_seconds"])
            try:
                _trace("E13", agent_id=agent_id, session_id=collaboration_session.session_id, reason="vnc_connect_retry")
                _restart_tunnel("vnc_connect_retry")
                emitted = _emit_vnc_start("vnc_connect_retry")
                _trace(
                    "E14",
                    agent_id=agent_id,
                    session_id=collaboration_session.session_id,
                    reason="vnc_connect_retry",
                    emit_ok=emitted,
                )
                if not emitted:
                    _service_log_event(
                        "vnc_backend_retry_blocked agent_id={0} session_id={1} reason=agent_socket_missing".format(
                            agent_id,
                            collaboration_session.session_id,
                        ),
                        level="WARNING",
                    )
                    _trace("E14F", agent_id=agent_id, session_id=collaboration_session.session_id, result="agent_socket_missing", level="WARNING")
                    return {"error": "agent_socket_missing"}, 409
            except Exception:
                logger.debug("Failed to request VNC connect retry agent_id=%s", agent_id, exc_info=True)
            initial_ready = _wait_for_backend_ready(
                host,
                vnc_port,
                timeout_seconds=retry_wait_seconds,
                poll_interval_seconds=retry_poll_seconds,
            )
            _trace(
                "E15",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                ready=initial_ready,
                wait_seconds=retry_wait_seconds,
                poll_seconds=retry_poll_seconds,
            )
        if not initial_ready:
            manager.record_error(collaboration_session.session_id, "backend_not_ready")
            _trace(
                "E16",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                result="vnc_backend_unavailable",
                level="WARNING",
            )
            return {"error": "vnc_backend_unavailable"}, 503

        cached_auth_rate_limit = _cached_vnc_auth_rate_limit(agent_id)
        if cached_auth_rate_limit is not None:
            retry_after_seconds = float(cached_auth_rate_limit["retry_after_seconds"])
            reason = _normalize_text(cached_auth_rate_limit["reason"]) or "vnc_auth_rate_limited"
            manager.record_error(collaboration_session.session_id, "backend_auth_rate_limited")
            _trace(
                "E16L",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                result="vnc_backend_auth_rate_limited",
                cached=True,
                retry_after_seconds=round(retry_after_seconds, 3),
                auth_reason=reason,
                level="WARNING",
            )
            _service_log_event(
                "vnc_backend_auth_rate_limited_cached agent_id={0} session_id={1} retry_after_seconds={2} reason={3}".format(
                    agent_id,
                    collaboration_session.session_id,
                    round(retry_after_seconds, 3),
                    reason,
                ),
                level="WARNING",
            )
            return {
                "error": "vnc_backend_auth_rate_limited",
                "detail": reason,
                "retry_after_seconds": retry_after_seconds,
            }, 503

        cached_auth_refresh_backoff = _cached_vnc_auth_refresh_backoff(agent_id)
        if cached_auth_refresh_backoff is not None:
            retry_after_seconds = float(cached_auth_refresh_backoff["retry_after_seconds"])
            reason = _normalize_text(cached_auth_refresh_backoff["reason"]) or "vnc_backend_auth_refresh_pending"
            manager.record_error(collaboration_session.session_id, "backend_auth_refresh_pending")
            _trace(
                "E16B",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                result="vnc_backend_auth_refresh_pending",
                cached=True,
                retry_after_seconds=round(retry_after_seconds, 3),
                auth_reason=reason,
                level="WARNING",
            )
            _service_log_event(
                "vnc_backend_auth_refresh_pending_cached agent_id={0} session_id={1} retry_after_seconds={2} reason={3}".format(
                    agent_id,
                    collaboration_session.session_id,
                    round(retry_after_seconds, 3),
                    reason,
                ),
                level="WARNING",
            )
            return {
                "error": "vnc_backend_auth_refresh_pending",
                "detail": reason,
                "retry_after_seconds": retry_after_seconds,
            }, 503

        auth_probe_wait_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_AUTH_PROBE_WAIT_SECONDS"),
            3.0,
        )
        auth_probe_poll_seconds = _coerce_timeout(
            os.environ.get("BOREALIS_VNC_AUTH_PROBE_POLL_INTERVAL_SECONDS"),
            0.25,
        )
        auth_probe = _wait_for_backend_auth_ready(
            host,
            vnc_port,
            collaboration_session.controller_password,
            timeout_seconds=auth_probe_wait_seconds,
            poll_interval_seconds=auth_probe_poll_seconds,
        )
        _trace(
            "E16A",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            auth_checked=auth_probe.checked,
            auth_ready=auth_probe.ok,
            auth_reason=auth_probe.reason,
            wait_seconds=auth_probe_wait_seconds,
        )
        if auth_probe.checked and not auth_probe.ok:
            if _is_vnc_auth_rate_limited(auth_probe.reason):
                retry_after_seconds = _coerce_nonnegative_timeout(
                    os.environ.get("BOREALIS_VNC_AUTH_RATE_LIMIT_RETRY_SECONDS"),
                    120.0,
                )
                _remember_vnc_auth_rate_limit(
                    agent_id,
                    retry_after_seconds=retry_after_seconds,
                    reason=auth_probe.reason,
                )
                manager.record_error(collaboration_session.session_id, "backend_auth_rate_limited")
                _service_log_event(
                    "vnc_backend_auth_rate_limited agent_id={0} session_id={1} retry_after_seconds={2} reason={3}".format(
                        agent_id,
                        collaboration_session.session_id,
                        retry_after_seconds,
                        auth_probe.reason,
                    ),
                    level="WARNING",
                )
                return {
                    "error": "vnc_backend_auth_rate_limited",
                    "detail": auth_probe.reason,
                    "retry_after_seconds": retry_after_seconds,
                }, 503
            refresh_wait_seconds = _coerce_nonnegative_timeout(
                os.environ.get("BOREALIS_VNC_AUTH_REFRESH_WAIT_SECONDS"),
                10.0,
            )
            auth_retry_previous_revision = collaboration_session.credential_revision
            auth_retry_previous_password = collaboration_session.controller_password
            refresh_emitted = _context_emit_agent_event(
                adapters.context,
                agent_id,
                "vnc_refresh",
                {"agent_id": agent_id, "reason": "vnc_auth_retry"},
            )
            _trace(
                "E16Q",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                refresh_emit=refresh_emitted,
                wait_seconds=refresh_wait_seconds,
                auth_reason=auth_probe.reason,
            )
            if refresh_emitted:
                refreshed_credential = _wait_for_agent_credential(
                    manager,
                    agent_id,
                    timeout_seconds=refresh_wait_seconds,
                    previous_revision=auth_retry_previous_revision,
                    previous_password=auth_retry_previous_password,
                )
                if refreshed_credential is not None and _normalize_text(
                    getattr(refreshed_credential, "controller_password", "")
                ):
                    agent_credential = refreshed_credential
                    collaboration_session.controller_password = refreshed_credential.controller_password
                    collaboration_session.credential_revision = refreshed_credential.credential_revision
                    if getattr(refreshed_credential, "display_topology", None) is not None:
                        agent_credential.display_topology = _clone_display_topology(
                            refreshed_credential.display_topology
                        )
                        agent_credential.display_virtual_bounds = _clone_display_virtual_bounds(
                            refreshed_credential.display_virtual_bounds
                        )
                _trace(
                    "E16P",
                    agent_id=agent_id,
                    session_id=collaboration_session.session_id,
                    credential_revision=collaboration_session.credential_revision,
                    credential_refreshed=refreshed_credential is not None,
                )
                if refreshed_credential is None:
                    manager.record_error(collaboration_session.session_id, "backend_auth_refresh_failed")
                    _service_log_event(
                        "vnc_backend_auth_refresh_failed agent_id={0} session_id={1} reason=credential_unchanged".format(
                            agent_id,
                            collaboration_session.session_id,
                        ),
                        level="WARNING",
                    )
                    return {
                        "error": "vnc_backend_auth_refresh_failed",
                        "detail": "credential_unchanged",
                    }, 503
                refresh_backoff_seconds = _coerce_nonnegative_timeout(
                    os.environ.get("BOREALIS_VNC_AUTH_REFRESH_BACKOFF_SECONDS"),
                    20.0,
                )
                refresh_backoff_seconds = _remember_vnc_auth_refresh_backoff(
                    agent_id,
                    retry_after_seconds=refresh_backoff_seconds,
                    reason=auth_probe.reason,
                )
                manager.record_error(collaboration_session.session_id, "backend_auth_refresh_pending")
                _trace(
                    "E16B",
                    agent_id=agent_id,
                    session_id=collaboration_session.session_id,
                    result="vnc_backend_auth_refresh_pending",
                    cached=False,
                    retry_after_seconds=round(refresh_backoff_seconds, 3),
                    auth_reason=auth_probe.reason,
                    credential_revision=collaboration_session.credential_revision,
                    level="WARNING",
                )
                _service_log_event(
                    "vnc_backend_auth_refresh_pending agent_id={0} session_id={1} retry_after_seconds={2} reason={3}".format(
                        agent_id,
                        collaboration_session.session_id,
                        refresh_backoff_seconds,
                        auth_probe.reason,
                    ),
                    level="WARNING",
                )
                return {
                    "error": "vnc_backend_auth_refresh_pending",
                    "detail": auth_probe.reason,
                    "retry_after_seconds": refresh_backoff_seconds,
                }, 503
            retry_wait_seconds = _coerce_timeout(
                os.environ.get("BOREALIS_VNC_AUTH_RETRY_WAIT_SECONDS"),
                max(20.0, float(wait_profile["retry_wait_seconds"])),
            )
            retry_poll_seconds = _coerce_timeout(
                os.environ.get("BOREALIS_VNC_AUTH_RETRY_POLL_INTERVAL_SECONDS"),
                auth_probe_poll_seconds,
            )
            if not refresh_emitted:
                try:
                    _trace(
                        "E16R",
                        agent_id=agent_id,
                        session_id=collaboration_session.session_id,
                        reason="vnc_auth_retry",
                        auth_reason=auth_probe.reason,
                    )
                    emitted = _emit_vnc_start("vnc_auth_retry")
                    _trace(
                        "E16S",
                        agent_id=agent_id,
                        session_id=collaboration_session.session_id,
                        reason="vnc_auth_retry",
                        emit_ok=emitted,
                        wait_seconds=retry_wait_seconds,
                    )
                    if not emitted:
                        _service_log_event(
                            "vnc_backend_auth_retry_blocked agent_id={0} session_id={1} reason=agent_socket_missing".format(
                                agent_id,
                                collaboration_session.session_id,
                            ),
                            level="WARNING",
                        )
                        _trace(
                            "E16SF",
                            agent_id=agent_id,
                            session_id=collaboration_session.session_id,
                            result="agent_socket_missing",
                            level="WARNING",
                        )
                        return {"error": "agent_socket_missing"}, 409
                except Exception:
                    logger.debug("Failed to request VNC auth retry agent_id=%s", agent_id, exc_info=True)
            else:
                emitted = True
                _trace(
                    "E16S",
                    agent_id=agent_id,
                    session_id=collaboration_session.session_id,
                    reason="vnc_auth_retry",
                    emit_ok=emitted,
                    wait_seconds=retry_wait_seconds,
                    refresh_only=True,
                )
            auth_probe = _wait_for_backend_auth_ready(
                host,
                vnc_port,
                collaboration_session.controller_password,
                timeout_seconds=retry_wait_seconds,
                poll_interval_seconds=retry_poll_seconds,
            )
            _trace(
                "E16T",
                agent_id=agent_id,
                session_id=collaboration_session.session_id,
                auth_checked=auth_probe.checked,
                auth_ready=auth_probe.ok,
                auth_reason=auth_probe.reason,
                wait_seconds=retry_wait_seconds,
            )
            if auth_probe.checked and not auth_probe.ok:
                if _is_vnc_auth_rate_limited(auth_probe.reason):
                    retry_after_seconds = _coerce_nonnegative_timeout(
                        os.environ.get("BOREALIS_VNC_AUTH_RATE_LIMIT_RETRY_SECONDS"),
                        120.0,
                    )
                    _remember_vnc_auth_rate_limit(
                        agent_id,
                        retry_after_seconds=retry_after_seconds,
                        reason=auth_probe.reason,
                    )
                    manager.record_error(collaboration_session.session_id, "backend_auth_rate_limited")
                    _service_log_event(
                        "vnc_backend_auth_rate_limited agent_id={0} session_id={1} retry_after_seconds={2} reason={3}".format(
                            agent_id,
                            collaboration_session.session_id,
                            retry_after_seconds,
                            auth_probe.reason,
                        ),
                        level="WARNING",
                    )
                    return {
                        "error": "vnc_backend_auth_rate_limited",
                        "detail": auth_probe.reason,
                        "retry_after_seconds": retry_after_seconds,
                    }, 503
                manager.record_error(collaboration_session.session_id, "backend_auth_failed")
                _service_log_event(
                    "vnc_backend_auth_failed agent_id={0} session_id={1} reason={2}".format(
                        agent_id,
                        collaboration_session.session_id,
                        auth_probe.reason,
                    ),
                    level="WARNING",
                )
                return {
                    "error": "vnc_backend_auth_failed",
                    "detail": auth_probe.reason,
                }, 503

        manager.record_backend_ready(
            collaboration_session.session_id,
            tunnel_id=_normalize_text(session_payload.get("tunnel_id")),
            allowed_ips=allowed_ips,
            engine_virtual_ip=engine_virtual_ip,
        )
        _trace(
            "E17",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            tunnel_id=session_payload.get("tunnel_id"),
            allowed_ips=allowed_ips or "-",
            engine_virtual_ip=engine_virtual_ip or "-",
        )
        try:
            _confirm_transport("vnc_backend_ready")
        except Exception:
            logger.debug("Failed to confirm VNC backend readiness agent_id=%s", agent_id, exc_info=True)

        _service_log_event(
            "vnc_establish_request agent_id={0} operator={1} role={2} session_id={3} remote={4} viewer={5}".format(
                agent_id,
                operator_id or "-",
                participant.role,
                collaboration_session.session_id,
                _request_remote() or "-",
                normalized_viewer,
            )
        )

        session_snapshot = manager.session_snapshot(
            collaboration_session,
            current_operator_id=operator_id or "",
        )
        session_snapshot["display_topology"] = _clone_display_topology(agent_credential.display_topology)
        session_snapshot["display_virtual_bounds"] = _clone_display_virtual_bounds(
            agent_credential.display_virtual_bounds
        )
        _trace(
            "E18",
            agent_id=agent_id,
            session_id=collaboration_session.session_id,
            participant_id=participant.participant_id,
            participant_role=participant.role,
            display_count=len(session_snapshot["display_topology"] or []),
            session_state=collaboration_session.state,
            viewer=normalized_viewer,
        )
        display_topology = _clone_display_topology(agent_credential.display_topology)
        display_virtual_bounds = _clone_display_virtual_bounds(agent_credential.display_virtual_bounds)
        base_payload = {
            "viewer": normalized_viewer,
            "session_id": collaboration_session.session_id,
            "participant_id": participant.participant_id,
            "participant_role": participant.role,
            "view_only": False,
            "session_state": collaboration_session.state,
            "controller_operator_id": collaboration_session.controller_operator_id or "",
            "credential_revision": collaboration_session.credential_revision,
            "session": session_snapshot,
            "display_topology": display_topology,
            "display_virtual_bounds": display_virtual_bounds,
            "virtual_ip": host,
            "tunnel_id": session_payload.get("tunnel_id"),
            "engine_virtual_ip": session_payload.get("engine_virtual_ip"),
            "vnc_port": vnc_port,
            "performance_preference": normalized_performance_preference,
        }

        def _on_open() -> None:
            manager.record_proxy_open(
                collaboration_session.session_id,
                participant.participant_id,
            )

        def _on_close(reason: str) -> None:
            manager.record_proxy_close(
                collaboration_session.session_id,
                participant.participant_id,
                reason=reason,
            )

        health = guacd_health(adapters.context)
        if not bool(health.get("enabled")) or not bool(health.get("available")):
            return {
                "error": "guacamole_unavailable",
                "detail": health.get("reason") or "unavailable",
            }, 503
        registry = ensure_guacamole_vnc_proxy(adapters.context, logger=logger)
        if registry is None:
            return {"error": "guacamole_proxy_unavailable"}, 503
        width, height = _initial_display_size(display_virtual_bounds, display_topology)
        guacamole_session = registry.create(
            agent_id=agent_id,
            host=host,
            port=vnc_port,
            password=collaboration_session.controller_password,
            operator_id=operator_id,
            session_id=collaboration_session.session_id,
            participant_id=participant.participant_id,
            role=participant.role,
            width=width,
            height=height,
            performance_preference=normalized_performance_preference,
            restart_tunnel=_restart_tunnel,
            confirm_transport=_confirm_transport,
            on_open=_on_open,
            on_close=_on_close,
        )
        ws_path = public_guacamole_vnc_path(adapters.context)
        ws_url = build_websocket_url(adapters.context, request, ws_path)
        _service_log_event(
            "vnc_session_ready agent_id={0} session_id={1} role={2} credential_revision={3} viewer=guacamole".format(
                agent_id,
                collaboration_session.session_id,
                participant.role,
                collaboration_session.credential_revision,
            )
        )
        base_payload.update(
            {
                "guacamole_ws_url": ws_url,
                "guacamole_ws_path": ws_path,
                "token": guacamole_session.token,
            }
        )
        return base_payload, 200

    @blueprint.route("/api/vnc/establish", methods=["POST"])
    def vnc_establish():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        requested_agent_id = _normalize_text(body.get("agent_id"))

        if not requested_agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)
        if not site_access.user_can_access_agent_id(user, agent_id):
            return jsonify({"error": "not found"}), 404

        remove_wallpaper = _normalize_bool(body.get("remove_wallpaper"), default=True)
        viewer = _normalize_viewer(body.get("viewer"))
        if viewer != "guacamole":
            return jsonify({"error": "invalid_viewer"}), 400
        performance_preference = normalize_guacamole_performance_preference(
            body.get("performance_preference")
        )

        payload, status = _issue_session(
            agent_id,
            operator_id,
            remove_wallpaper=remove_wallpaper,
            viewer=viewer,
            performance_preference=performance_preference,
        )
        return jsonify(payload), status

    @blueprint.route("/api/vnc/viewers", methods=["GET"])
    def vnc_viewers():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        health = guacd_health(adapters.context)
        return jsonify(
            {
                "default_viewer": "guacamole",
                "viewers": [
                    {
                        "id": "guacamole",
                        "label": "Apache Guacamole",
                        "enabled": bool(health.get("enabled")),
                        "available": bool(health.get("enabled")) and bool(health.get("available")),
                        "reason": health.get("reason") or "",
                    },
                ],
                "guacamole": {
                    "enabled": bool(health.get("enabled")),
                    "available": bool(health.get("enabled")) and bool(health.get("available")),
                    "host": health.get("host") or "",
                    "port": int(health.get("port") or 0),
                    "ws_path": public_guacamole_vnc_path(adapters.context),
                    "reason": health.get("reason") or "",
                },
            }
        ), 200

    @blueprint.route("/api/vnc/session", methods=["POST"])
    def vnc_session():
        return vnc_establish()

    @blueprint.route("/api/vnc/disconnect", methods=["POST"])
    def vnc_disconnect():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        agent_id = _normalize_text(body.get("agent_id"))
        session_id = _normalize_text(body.get("session_id"))
        reason = _normalize_text(body.get("reason") or "operator_disconnect")
        close_session = _normalize_bool(body.get("close_session"), default=False)

        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        collaboration_session = manager.get_session_by_id(session_id) if session_id else None
        resolved_agent_id = collaboration_session.agent_id if collaboration_session is not None else ""
        if not resolved_agent_id:
            if not agent_id:
                return jsonify({"error": "agent_id_or_session_id_required"}), 400
            resolved_agent_id = _resolve_requested_agent_id(adapters, agent_id)
            collaboration_session = manager.get_session_for_agent(resolved_agent_id)
        if not site_access.user_can_access_agent_id(user, resolved_agent_id):
            return jsonify({"error": "not found"}), 404
        if collaboration_session is None:
            return jsonify({"error": "session_not_found"}), 404

        current_snapshot = manager.session_snapshot(
            collaboration_session,
            current_operator_id=operator_id or "",
        )
        user_is_admin = str(user.get("role") or "").strip().lower() == "admin"
        if close_session and not (
            user_is_admin
            or (_normalize_text(operator_id) and _normalize_text(operator_id) == _normalize_text(collaboration_session.controller_operator_id))
        ):
            return jsonify({"error": "controller_required"}), 403

        try:
            if close_session and user_is_admin and current_snapshot.get("current_operator_role") != "controller":
                result = manager.close_session(
                    session_id=collaboration_session.session_id,
                    reason=reason or "session_closed",
                )
                result["closed"] = True
                result["controller_vacant"] = False
                result["participant_id"] = ""
            else:
                result = manager.leave_or_close(
                    session_id=collaboration_session.session_id,
                    operator_id=operator_id or "",
                    close_session=close_session,
                )
        except PermissionError:
            return jsonify({"error": "participant_required"}), 403
        except KeyError:
            return jsonify({"error": "session_not_found"}), 404

        proxy = getattr(adapters.context, "vnc_proxy", None)
        if bool(result.get("closed")):
            if proxy is not None and hasattr(proxy, "disconnect_session"):
                try:
                    proxy.disconnect_session(
                        collaboration_session.session_id,
                        reason=reason or "session_closed",
                    )
                except Exception:
                    logger.debug("Failed to disconnect VNC session %s", collaboration_session.session_id, exc_info=True)
            _context_emit_agent_event(
                adapters.context,
                resolved_agent_id,
                "vnc_stop",
                {"agent_id": resolved_agent_id, "reason": reason or "session_closed"},
            )
        else:
            left_participant_id = _normalize_text(result.get("participant_id"))
            if proxy is not None and hasattr(proxy, "disconnect_participant") and left_participant_id:
                try:
                    proxy.disconnect_participant(
                        collaboration_session.session_id,
                        left_participant_id,
                        reason=reason or "participant_left",
                    )
                except Exception:
                    logger.debug(
                        "Failed to disconnect VNC participant %s/%s",
                        collaboration_session.session_id,
                        left_participant_id,
                        exc_info=True,
                    )
            if bool(result.get("reconnect_pending")):
                _context_emit_agent_event(
                    adapters.context,
                    resolved_agent_id,
                    "vnc_stop",
                    {"agent_id": resolved_agent_id, "reason": reason or "operator_disconnect"},
                )
            if bool(result.get("controller_vacant")):
                refreshed_session = manager.get_session_by_id(collaboration_session.session_id)
                if refreshed_session is not None:
                    tunnel_service = _get_tunnel_service(adapters)
                    refreshed_payload = None
                    if hasattr(tunnel_service, "session_payload"):
                        refreshed_payload = tunnel_service.session_payload(
                            resolved_agent_id,
                            include_token=False,
                        )
                    _context_emit_agent_event(
                        adapters.context,
                        resolved_agent_id,
                        "vnc_start",
                        {
                            "agent_id": resolved_agent_id,
                            "session_id": refreshed_session.session_id,
                            "controller_password": refreshed_session.controller_password,
                            "view_only_password": "",
                            "port": int(getattr(adapters.context, "vnc_port", 5900)),
                            "allowed_ips": _resolve_allowed_ips(
                                session_payload=refreshed_payload,
                                collaboration_session=refreshed_session,
                            ),
                            "remove_wallpaper": bool(refreshed_session.remove_wallpaper),
                            "credential_revision": refreshed_session.credential_revision,
                            "reason": "controller_vacated",
                        },
                    )
                    if proxy is not None and hasattr(proxy, "disconnect_session"):
                        try:
                            proxy.disconnect_session(
                                refreshed_session.session_id,
                                reason="controller_reconnect_required",
                            )
                        except Exception:
                            logger.debug(
                                "Failed to force VNC reconnect after controller leave %s",
                                refreshed_session.session_id,
                                exc_info=True,
                            )

        _service_log_event(
            "vnc_disconnect agent_id={0} operator={1} session_id={2} close_session={3} reason={4}".format(
                resolved_agent_id,
                operator_id or "-",
                collaboration_session.session_id,
                str(bool(close_session)).lower(),
                reason or "-",
            )
        )

        response_payload = {
            "status": "closed" if bool(result.get("closed")) else "left",
            "reason": reason,
            "session_id": collaboration_session.session_id,
            "controller_vacant": bool(result.get("controller_vacant")),
            "reconnect_pending": bool(result.get("reconnect_pending")),
        }
        if not bool(result.get("closed")):
            refreshed_session = manager.get_session_by_id(collaboration_session.session_id)
            if refreshed_session is not None:
                refreshed_credential = manager.get_agent_credential(refreshed_session.agent_id)
                response_payload["session"] = manager.session_snapshot(
                    refreshed_session,
                    current_operator_id=operator_id or "",
                )
                if refreshed_credential is not None:
                    response_payload["session"]["display_topology"] = _clone_display_topology(
                        refreshed_credential.display_topology
                    )
                    response_payload["session"]["display_virtual_bounds"] = _clone_display_virtual_bounds(
                        refreshed_credential.display_virtual_bounds
                    )
        return jsonify(response_payload), 200

    @blueprint.route("/api/vnc/handoff", methods=["POST"])
    def vnc_handoff():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None
        body = request.get_json(silent=True) or {}
        session_id = _normalize_text(body.get("session_id"))
        target_operator_id = _normalize_text(body.get("target_operator_id"))
        if not session_id:
            return jsonify({"error": "session_id_required"}), 400

        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        collaboration_session = manager.get_session_by_id(session_id)
        if collaboration_session is None:
            return jsonify({"error": "session_not_found"}), 404
        if not site_access.user_can_access_agent_id(user, collaboration_session.agent_id):
            return jsonify({"error": "not found"}), 404

        try:
            handoff_result = manager.handoff(
                session_id=session_id,
                actor_operator_id=operator_id or "",
                target_operator_id=target_operator_id or None,
            )
        except PermissionError:
            return jsonify({"error": "controller_required"}), 403
        except KeyError as exc:
            error_key = str(exc).strip("'")
            return jsonify({"error": error_key or "target_not_found"}), 404
        except ValueError:
            return jsonify({"error": "target_already_controller"}), 409

        refreshed_session = handoff_result["session"]
        tunnel_service = _get_tunnel_service(adapters)
        session_payload = None
        if hasattr(tunnel_service, "session_payload"):
            session_payload = tunnel_service.session_payload(refreshed_session.agent_id, include_token=False)
        allowed_ips = _resolve_allowed_ips(
            session_payload=session_payload if isinstance(session_payload, dict) else None,
            collaboration_session=refreshed_session,
        )

        _service_log_event(
            "vnc_handoff agent_id={0} session_id={1} from={2} to={3}".format(
                refreshed_session.agent_id,
                refreshed_session.session_id,
                operator_id or "-",
                refreshed_session.controller_operator_id or "-",
            )
        )
        refreshed_credential = manager.get_agent_credential(refreshed_session.agent_id)
        refreshed_snapshot = manager.session_snapshot(
            refreshed_session,
            current_operator_id=operator_id or "",
        )
        if refreshed_credential is not None:
            refreshed_snapshot["display_topology"] = _clone_display_topology(
                refreshed_credential.display_topology
            )
            refreshed_snapshot["display_virtual_bounds"] = _clone_display_virtual_bounds(
                refreshed_credential.display_virtual_bounds
            )
        return (
            jsonify(
                {
                    "status": "ok",
                    "participant_role": refreshed_snapshot.get("current_operator_role") or "",
                    "session": refreshed_snapshot,
                    "reconnect_required": False,
                    "allowed_ips": allowed_ips,
                    "display_topology": _clone_display_topology(
                        refreshed_credential.display_topology if refreshed_credential is not None else []
                    ),
                    "display_virtual_bounds": _clone_display_virtual_bounds(
                        refreshed_credential.display_virtual_bounds if refreshed_credential is not None else {}
                    ),
                }
            ),
            200,
        )

    @blueprint.route("/api/vnc/sessions", methods=["GET"])
    def vnc_sessions():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or ""
        requested_agent_id = _normalize_text(request.args.get("agent_id"))
        requested_session_id = _normalize_text(request.args.get("session_id"))
        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        sessions = manager.list_sessions()
        if requested_session_id:
            sessions = [session for session in sessions if session.session_id == requested_session_id]
        if requested_agent_id:
            resolved_agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)
            if not site_access.user_can_access_agent_id(user, resolved_agent_id):
                return jsonify({"error": "not found"}), 404
            sessions = [session for session in sessions if session.agent_id == resolved_agent_id]
        visible_sessions = []
        for active_session in sessions:
            if not site_access.user_can_access_agent_id(user, active_session.agent_id):
                continue
            snapshot = manager.session_snapshot(active_session, current_operator_id=operator_id)
            credential = manager.get_agent_credential(active_session.agent_id)
            if credential is not None:
                snapshot["display_topology"] = _clone_display_topology(credential.display_topology)
                snapshot["display_virtual_bounds"] = _clone_display_virtual_bounds(
                    credential.display_virtual_bounds
                )
            visible_sessions.append(snapshot)
        return jsonify({"sessions": visible_sessions, "count": len(visible_sessions)}), 200

    app.register_blueprint(blueprint)
