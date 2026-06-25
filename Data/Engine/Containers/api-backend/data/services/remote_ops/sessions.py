"""Signed remote-operation session tokens shared by api-backend and site-workers."""

from __future__ import annotations

import os
import time
import uuid
from typing import Any, Dict, Mapping, Optional, Sequence

import jwt

REMOTE_OP_SESSION_ISSUER = "borealis-api-backend"
REMOTE_OP_SESSION_AUDIENCE = "borealis-site-worker"
REMOTE_OP_SESSION_TOKEN_TYPE = "remote-op-session"
DEFAULT_REMOTE_OP_SESSION_TTL_SECONDS = 300
MAX_REMOTE_OP_SESSION_TTL_SECONDS = 900

CAPABILITY_ALIASES = {
    "shell": "remote_shell",
    "remote-shell": "remote_shell",
    "remote_shell": "remote_shell",
    "powershell": "remote_shell",
    "desktop": "remote_desktop",
    "remote-desktop": "remote_desktop",
    "remote_desktop": "remote_desktop",
    "vnc": "remote_desktop",
    "guacamole": "remote_desktop",
    "files": "remote_files",
    "file": "remote_files",
    "remote-files": "remote_files",
    "remote_files": "remote_files",
    "file_management": "remote_files",
    "process": "process_management",
    "processes": "process_management",
    "process-management": "process_management",
    "process_management": "process_management",
    "service": "service_management",
    "services": "service_management",
    "service-management": "service_management",
    "service_management": "service_management",
    "software": "software_management",
    "software-management": "software_management",
    "software_management": "software_management",
    "agent-maintenance": "agent_maintenance",
    "agent_maintenance": "agent_maintenance",
    "quick-job": "quick_job",
    "quick_job": "quick_job",
}
VALID_REMOTE_OP_CAPABILITIES = frozenset(CAPABILITY_ALIASES.values())


class RemoteOpSessionError(ValueError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


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


def _configured_ttl(value: Optional[Any] = None) -> int:
    default_raw = os.environ.get("BOREALIS_REMOTE_OP_SESSION_TTL_SECONDS")
    max_raw = os.environ.get("BOREALIS_REMOTE_OP_SESSION_MAX_TTL_SECONDS")
    try:
        default_ttl = int(default_raw) if default_raw else DEFAULT_REMOTE_OP_SESSION_TTL_SECONDS
    except Exception:
        default_ttl = DEFAULT_REMOTE_OP_SESSION_TTL_SECONDS
    try:
        max_ttl = int(max_raw) if max_raw else MAX_REMOTE_OP_SESSION_TTL_SECONDS
    except Exception:
        max_ttl = MAX_REMOTE_OP_SESSION_TTL_SECONDS
    try:
        ttl = int(value) if value is not None else default_ttl
    except Exception:
        ttl = default_ttl
    return min(max(30, ttl), max(30, max_ttl))


def normalize_remote_op_capabilities(value: Any) -> list[str]:
    if isinstance(value, str):
        candidates: Sequence[Any] = [value]
    elif isinstance(value, Sequence) and not isinstance(value, (bytes, bytearray)):
        candidates = value
    else:
        candidates = []
    normalized: list[str] = []
    seen = set()
    for item in candidates:
        key = _normalize_text(item).lower().replace(" ", "_")
        if not key:
            continue
        capability = CAPABILITY_ALIASES.get(key) or CAPABILITY_ALIASES.get(key.replace("_", "-"))
        if not capability or capability in seen:
            continue
        seen.add(capability)
        normalized.append(capability)
    return normalized


def _require_capabilities(capabilities: Any) -> list[str]:
    normalized = normalize_remote_op_capabilities(capabilities)
    if not normalized:
        raise RemoteOpSessionError("invalid_capability", "At least one valid remote-operation capability is required.")
    return normalized


def issue_remote_op_session(
    jwt_service,
    *,
    user: Mapping[str, Any],
    device: Mapping[str, Any],
    worker_route: Mapping[str, Any],
    capabilities: Any,
    ttl_seconds: Optional[int] = None,
    now: Optional[int] = None,
) -> Dict[str, Any]:
    caps = _require_capabilities(capabilities)
    issued_at = int(now if now is not None else time.time())
    ttl = _configured_ttl(ttl_seconds)
    session_id = uuid.uuid4().hex
    site_id = _normalize_int(device.get("site_id"))
    route_generation = _normalize_int(worker_route.get("generation")) or 0
    claims: Dict[str, Any] = {
        "iss": REMOTE_OP_SESSION_ISSUER,
        "aud": REMOTE_OP_SESSION_AUDIENCE,
        "typ": REMOTE_OP_SESSION_TOKEN_TYPE,
        "sub": f"remote-op:{session_id}",
        "jti": session_id,
        "iat": issued_at,
        "nbf": issued_at,
        "exp": issued_at + ttl,
        "user": _normalize_text(user.get("username")),
        "role": _normalize_text(user.get("role")) or "User",
        "site_id": site_id,
        "device_guid": _normalize_text(device.get("guid") or device.get("device_guid")),
        "hostname": _normalize_text(device.get("hostname")),
        "agent_id": _normalize_text(device.get("agent_id")),
        "worker_guid": _normalize_text(worker_route.get("worker_guid")),
        "route_generation": route_generation,
        "capabilities": caps,
    }
    token = jwt_service.issue_claims(claims)
    return {
        "token": token,
        "claims": claims,
        "session_id": session_id,
        "expires_at": issued_at + ttl,
        "expires_in": ttl,
        "issued_at": issued_at,
        "capabilities": caps,
    }


def verify_remote_op_session(
    jwt_service,
    token: str,
    *,
    required_capability: Optional[str] = None,
    worker_guid: Optional[str] = None,
    site_id: Optional[int] = None,
    device_guid: Optional[str] = None,
    hostname: Optional[str] = None,
) -> Dict[str, Any]:
    token_value = _normalize_text(token)
    if not token_value:
        raise RemoteOpSessionError("missing_token", "Remote-operation token is required.")
    try:
        claims = jwt_service.decode(token_value, audience=REMOTE_OP_SESSION_AUDIENCE)
    except jwt.ExpiredSignatureError as exc:
        raise RemoteOpSessionError("token_expired", "Remote-operation token has expired.") from exc
    except jwt.InvalidTokenError as exc:
        raise RemoteOpSessionError("invalid_token", "Remote-operation token is invalid.") from exc
    except Exception as exc:
        raise RemoteOpSessionError("invalid_token", "Remote-operation token could not be decoded.") from exc

    if _normalize_text(claims.get("iss")) != REMOTE_OP_SESSION_ISSUER:
        raise RemoteOpSessionError("invalid_issuer", "Remote-operation token issuer is invalid.")
    if _normalize_text(claims.get("typ")) != REMOTE_OP_SESSION_TOKEN_TYPE:
        raise RemoteOpSessionError("invalid_token_type", "Remote-operation token type is invalid.")
    capabilities = normalize_remote_op_capabilities(claims.get("capabilities") or [])
    if not capabilities:
        raise RemoteOpSessionError("invalid_capability", "Remote-operation token has no valid capability.")
    if required_capability:
        required = normalize_remote_op_capabilities(required_capability)
        if not required or required[0] not in capabilities:
            raise RemoteOpSessionError("capability_denied", "Remote-operation token is not scoped for this capability.")
    if worker_guid and _normalize_text(claims.get("worker_guid")) != _normalize_text(worker_guid):
        raise RemoteOpSessionError("worker_mismatch", "Remote-operation token is not scoped for this worker.")
    expected_site_id = _normalize_int(site_id)
    if expected_site_id is not None and _normalize_int(claims.get("site_id")) != expected_site_id:
        raise RemoteOpSessionError("site_mismatch", "Remote-operation token is not scoped for this site.")
    if device_guid and _normalize_text(claims.get("device_guid")).upper() != _normalize_text(device_guid).upper():
        raise RemoteOpSessionError("device_mismatch", "Remote-operation token is not scoped for this device.")
    if hostname and _normalize_text(claims.get("hostname")).lower() != _normalize_text(hostname).lower():
        raise RemoteOpSessionError("device_mismatch", "Remote-operation token is not scoped for this hostname.")
    return dict(claims)
