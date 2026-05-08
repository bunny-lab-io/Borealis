from __future__ import annotations

import json
import os
import socket
import time
import uuid
from pathlib import Path
from typing import Any, Dict, Iterable, List, Mapping, Optional, Sequence
from urllib.parse import urlparse

try:
    from runtime_paths import agent_borealis_root, agent_logs_root
except Exception:
    agent_borealis_root = None
    agent_logs_root = None

try:
    from update_state import get_busy_snapshot, read_installed_build_id, read_repo_build_id, read_update_status
except Exception:
    get_busy_snapshot = None
    read_installed_build_id = None
    read_repo_build_id = None
    read_update_status = None


SCHEMA_VERSION = 1
STATUS_FRESHNESS_SECONDS = 180
BOOT_GRACE_SECONDS = 120
RESTART_REQUEST_TTL_SECONDS = 120

_DEFAULT_ROLE_HEALTH = {"roles": [], "reported_at": 0}
_ACTIVITY_PRIORITY = (
    "vnc_session",
    "remote_shell",
    "quick_job_system",
    "quick_job_currentuser",
)
_ACTIVITY_LABELS = {
    "quick_job_system": "Running support task",
    "quick_job_currentuser": "Running support task",
    "remote_shell": "Remote shell active",
    "vnc_session": "Remote desktop active",
}


def _title_case_channel(value: Any) -> str:
    text = str(value or "").strip().lower()
    if not text:
        return "Unknown"
    return text.replace("_", " ").replace("-", " ").title()


def normalize_service_mode(value: Any) -> str:
    text = str(value or "").strip().lower().replace("-", "_")
    if text in {"system", "svc", "service", "system_service"}:
        return "system"
    if text in {"currentuser", "current_user", "interactive", "user"}:
        return "currentuser"
    return text or "currentuser"


def _ensure_dir(path: Path) -> Path:
    path.mkdir(parents=True, exist_ok=True)
    return path


def _resolve_project_root(start: Optional[Path] = None) -> Path:
    if callable(agent_borealis_root):
        try:
            return agent_borealis_root(start)
        except Exception:
            pass
    current = Path(start or __file__).resolve().parent
    for candidate in (current, *current.parents):
        if (
            (candidate / "Agent.exe").is_file()
            or (candidate / "Agent.ps1").is_file()
            or (candidate / "Agent.sh").is_file()
            or (candidate / "Engine.sh").is_file()
        ):
            return candidate / "Agent" / "Borealis"
    return current


def tray_settings_root(start: Optional[Path] = None) -> Path:
    return _ensure_dir(_resolve_project_root(start) / "Settings" / "Tray")


def status_path(service_mode: Any, start: Optional[Path] = None) -> Path:
    return tray_settings_root(start) / f"{normalize_service_mode(service_mode)}_status.json"


def restart_path(service_mode: Any, start: Optional[Path] = None) -> Path:
    return tray_settings_root(start) / f"restart_{normalize_service_mode(service_mode)}.json"


def agent_logs_directory(start: Optional[Path] = None) -> Path:
    if callable(agent_logs_root):
        try:
            return agent_logs_root(start)
        except Exception:
            pass
    return _resolve_project_root(start).parent / "Logs"


def read_agent_guid(start: Optional[Path] = None) -> str:
    path = _resolve_project_root(start) / "Settings" / "Agent_GUID.txt"
    if not path.is_file():
        return ""
    try:
        return path.read_text(encoding="utf-8", errors="ignore").strip()
    except Exception:
        return ""


def read_build_id() -> str:
    for reader in (read_installed_build_id, read_repo_build_id):
        if not callable(reader):
            continue
        try:
            value = str(reader() or "").strip()
        except Exception:
            value = ""
        if value:
            return value
    return ""


def extract_server_host(server_url: Any) -> str:
    raw = str(server_url or "").strip()
    if not raw:
        return ""
    try:
        parsed = urlparse(raw)
    except Exception:
        return raw
    return (parsed.hostname or "").strip()


def read_configured_server_url(start: Optional[Path] = None) -> str:
    path = _resolve_project_root(start) / "Settings" / "server_url.txt"
    if not path.is_file():
        return ""
    try:
        raw = path.read_text(encoding="utf-8-sig", errors="ignore").strip()
    except Exception:
        return ""
    if not raw:
        return ""
    if "://" not in raw:
        raw = "https://" + raw
    try:
        parsed = urlparse(raw)
    except Exception:
        return ""
    hostname = str(parsed.hostname or "").strip()
    if not hostname:
        return ""
    netloc = hostname
    if parsed.port:
        netloc = f"{hostname}:{parsed.port}"
    return parsed._replace(scheme="https", netloc=netloc, params="", fragment="").geturl().rstrip("/")


def _read_json(path: Path) -> Dict[str, Any]:
    if not path.is_file():
        return {}
    try:
        raw = path.read_text(encoding="utf-8", errors="ignore").strip()
    except Exception:
        return {}
    if not raw:
        return {}
    try:
        payload = json.loads(raw)
    except Exception:
        return {}
    if isinstance(payload, dict):
        return payload
    return {}


def _write_json_atomic(path: Path, payload: Mapping[str, Any]) -> None:
    _ensure_dir(path.parent)
    temp_path = path.with_name(f"{path.name}.{os.getpid()}.{uuid.uuid4().hex}.tmp")
    temp_path.write_text(json.dumps(payload, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    temp_path.replace(path)


def build_default_snapshot(
    service_mode: Any,
    *,
    now: Optional[int] = None,
    started_at: Optional[int] = None,
    pid: Optional[int] = None,
    server_url: str = "",
) -> Dict[str, Any]:
    timestamp = int(now or time.time())
    normalized_mode = normalize_service_mode(service_mode)
    normalized_server_url = str(server_url or "").strip()
    return {
        "schema_version": SCHEMA_VERSION,
        "service_mode": normalized_mode,
        "pid": int(pid or os.getpid()),
        "started_at": int(started_at or timestamp),
        "updated_at": timestamp,
        "server_url": normalized_server_url,
        "server_host": extract_server_host(normalized_server_url),
        "site_id": None,
        "site_name": "",
        "guid_present": False,
        "verify_enabled": True,
        "socket_connected": False,
        "last_auth_success_at": 0,
        "last_heartbeat_success_at": 0,
        "last_error_kind": "",
        "last_error_message": "",
        "role_health": dict(_DEFAULT_ROLE_HEALTH),
    }


def load_status_snapshot(service_mode: Any, *, start: Optional[Path] = None) -> Dict[str, Any]:
    payload = _read_json(status_path(service_mode, start))
    if not payload:
        return {}
    payload.setdefault("schema_version", SCHEMA_VERSION)
    payload["service_mode"] = normalize_service_mode(payload.get("service_mode") or service_mode)
    payload.setdefault("server_url", "")
    payload["server_host"] = extract_server_host(payload.get("server_url")) or str(payload.get("server_host") or "").strip()
    payload["site_name"] = str(payload.get("site_name") or "").strip()
    try:
        payload["site_id"] = int(payload.get("site_id")) if payload.get("site_id") is not None else None
    except Exception:
        payload["site_id"] = None
    if not isinstance(payload.get("role_health"), dict):
        payload["role_health"] = dict(_DEFAULT_ROLE_HEALTH)
    return payload


def write_status_snapshot(
    service_mode: Any,
    snapshot: Mapping[str, Any],
    *,
    start: Optional[Path] = None,
    now: Optional[int] = None,
) -> Dict[str, Any]:
    timestamp = int(now or time.time())
    normalized_mode = normalize_service_mode(service_mode)
    merged = build_default_snapshot(
        normalized_mode,
        now=timestamp,
        started_at=snapshot.get("started_at"),
        pid=snapshot.get("pid"),
        server_url=str(snapshot.get("server_url") or "").strip(),
    )
    for key, value in dict(snapshot).items():
        merged[key] = value
    merged["schema_version"] = SCHEMA_VERSION
    merged["service_mode"] = normalized_mode
    merged["updated_at"] = timestamp
    merged["server_host"] = extract_server_host(merged.get("server_url")) or str(merged.get("server_host") or "").strip()
    merged["site_name"] = str(merged.get("site_name") or "").strip()
    try:
        merged["site_id"] = int(merged.get("site_id")) if merged.get("site_id") is not None else None
    except Exception:
        merged["site_id"] = None
    if not isinstance(merged.get("role_health"), dict):
        merged["role_health"] = dict(_DEFAULT_ROLE_HEALTH)
    _write_json_atomic(status_path(normalized_mode, start), merged)
    return merged


def update_status_snapshot(
    service_mode: Any,
    updates: Mapping[str, Any],
    *,
    start: Optional[Path] = None,
    now: Optional[int] = None,
) -> Dict[str, Any]:
    snapshot = load_status_snapshot(service_mode, start=start)
    if not snapshot:
        snapshot = build_default_snapshot(service_mode, now=now)
    merged = dict(snapshot)
    for key, value in dict(updates or {}).items():
        merged[key] = value
    return write_status_snapshot(service_mode, merged, start=start, now=now)


def clear_status_snapshot(service_mode: Any, *, start: Optional[Path] = None) -> None:
    try:
        status_path(service_mode, start).unlink(missing_ok=True)
    except Exception:
        pass


def request_restart(
    service_modes: Sequence[Any] = ("currentuser", "system"),
    *,
    start: Optional[Path] = None,
    request_id: Optional[str] = None,
    requested_at: Optional[int] = None,
    requested_by: str = "tray",
    requested_by_pid: Optional[int] = None,
) -> Dict[str, Any]:
    timestamp = int(requested_at or time.time())
    normalized_request_id = str(request_id or uuid.uuid4().hex).strip() or uuid.uuid4().hex
    normalized_modes = [normalize_service_mode(mode) for mode in service_modes]
    written_files: Dict[str, str] = {}
    for mode in normalized_modes:
        payload = {
            "schema_version": SCHEMA_VERSION,
            "request_id": normalized_request_id,
            "requested_at": timestamp,
            "service_mode": mode,
            "requested_by": str(requested_by or "tray"),
            "requested_by_pid": int(requested_by_pid or os.getpid()),
        }
        path = restart_path(mode, start)
        _write_json_atomic(path, payload)
        written_files[mode] = str(path)
    return {
        "request_id": normalized_request_id,
        "requested_at": timestamp,
        "service_modes": normalized_modes,
        "files": written_files,
    }


def load_restart_request(service_mode: Any, *, start: Optional[Path] = None) -> Dict[str, Any]:
    payload = _read_json(restart_path(service_mode, start))
    if payload:
        payload["service_mode"] = normalize_service_mode(payload.get("service_mode") or service_mode)
    return payload


def clear_restart_request(service_mode: Any, *, start: Optional[Path] = None) -> None:
    try:
        restart_path(service_mode, start).unlink(missing_ok=True)
    except Exception:
        pass


def consume_restart_request(service_mode: Any, *, start: Optional[Path] = None) -> Dict[str, Any]:
    payload = load_restart_request(service_mode, start=start)
    if payload:
        clear_restart_request(service_mode, start=start)
    return payload


def active_restart_requests(
    *,
    start: Optional[Path] = None,
    now: Optional[int] = None,
    ttl_seconds: int = RESTART_REQUEST_TTL_SECONDS,
) -> Dict[str, Dict[str, Any]]:
    timestamp = int(now or time.time())
    active: Dict[str, Dict[str, Any]] = {}
    for service_mode in ("currentuser", "system"):
        payload = load_restart_request(service_mode, start=start)
        if not payload:
            continue
        requested_at = int(payload.get("requested_at") or 0)
        if requested_at <= 0:
            continue
        if (timestamp - requested_at) > ttl_seconds:
            continue
        active[service_mode] = payload
    return active


def snapshot_age(snapshot: Mapping[str, Any], *, now: Optional[int] = None) -> Optional[int]:
    updated_at = int(snapshot.get("updated_at") or 0)
    if updated_at <= 0:
        return None
    timestamp = int(now or time.time())
    return max(0, timestamp - updated_at)


def snapshot_is_fresh(
    snapshot: Mapping[str, Any],
    *,
    now: Optional[int] = None,
    freshness_seconds: int = STATUS_FRESHNESS_SECONDS,
) -> bool:
    age = snapshot_age(snapshot, now=now)
    return age is not None and age <= freshness_seconds


def snapshot_in_boot_grace(
    snapshot: Mapping[str, Any],
    *,
    now: Optional[int] = None,
    boot_grace_seconds: int = BOOT_GRACE_SECONDS,
) -> bool:
    started_at = int(snapshot.get("started_at") or 0)
    if started_at <= 0:
        return False
    timestamp = int(now or time.time())
    return max(0, timestamp - started_at) <= boot_grace_seconds


def _load_busy_snapshot() -> Dict[str, Any]:
    if callable(get_busy_snapshot):
        try:
            payload = get_busy_snapshot()
        except Exception:
            payload = {}
        if isinstance(payload, dict):
            return payload
    return {"busy": False, "reasons": [], "entries": []}


def activity_label_from_snapshot(busy_snapshot: Optional[Mapping[str, Any]] = None) -> str:
    payload = dict(busy_snapshot or _load_busy_snapshot())
    reasons = [str(item or "").strip() for item in payload.get("reasons") or [] if str(item or "").strip()]
    if not reasons and isinstance(payload.get("entries"), list):
        reasons = [str((entry or {}).get("reason") or "").strip() for entry in payload["entries"] if isinstance(entry, dict)]
    for reason in _ACTIVITY_PRIORITY:
        if reason in reasons:
            return _ACTIVITY_LABELS.get(reason, "Running support task")
    if reasons:
        return "Running support task"
    return "Idle"


def _extract_wireguard_snapshot(snapshot: Mapping[str, Any]) -> Dict[str, Any]:
    role_health = snapshot.get("role_health")
    if not isinstance(role_health, dict):
        return {}
    roles = role_health.get("roles")
    if not isinstance(roles, list):
        return {}
    for role in roles:
        if not isinstance(role, dict):
            continue
        if str(role.get("role_label") or "").strip() == "WireGuard Service":
            return role
    return {}


def wireguard_summary(snapshot: Optional[Mapping[str, Any]]) -> Dict[str, str]:
    payload = dict(snapshot or {})
    role = _extract_wireguard_snapshot(payload)
    if not role:
        return {"status_code": "unknown", "label": "Unavailable", "detail": "WireGuard status is unavailable."}
    status_code = str(role.get("status_code") or role.get("status") or "").strip().lower() or "unknown"
    detail = str(role.get("detail") or "").strip()
    if status_code == "healthy":
        return {"status_code": status_code, "label": "Connected", "detail": detail or "WireGuard tunnel is connected."}
    if status_code in {"recovering", "pending"}:
        return {"status_code": status_code, "label": "Starting", "detail": detail or "WireGuard is still starting."}
    if status_code == "unhealthy":
        return {"status_code": status_code, "label": "Needs attention", "detail": detail or "WireGuard needs attention."}
    return {"status_code": status_code, "label": "Unavailable", "detail": detail or "WireGuard status is unavailable."}


def _dedupe_preserve(items: Iterable[str]) -> List[str]:
    seen = set()
    result: List[str] = []
    for item in items:
        normalized = str(item or "").strip()
        if not normalized or normalized in seen:
            continue
        seen.add(normalized)
        result.append(normalized)
    return result


def _role_warnings(snapshot: Mapping[str, Any], prefix: str) -> List[str]:
    role_health = snapshot.get("role_health")
    if not isinstance(role_health, dict):
        return []
    roles = role_health.get("roles")
    if not isinstance(roles, list):
        return []
    warnings: List[str] = []
    for role in roles:
        if not isinstance(role, dict):
            continue
        status_code = str(role.get("status_code") or role.get("status") or "").strip().lower()
        if status_code not in {"recovering", "unhealthy"}:
            continue
        label = str(role.get("role_label") or role.get("role_name") or "Role").strip()
        detail = str(role.get("detail") or "").strip()
        if detail:
            warnings.append(f"{prefix}: {label} - {detail}")
        else:
            warnings.append(f"{prefix}: {label} needs attention.")
    return warnings


def format_relative_time(value: Any, *, now: Optional[int] = None) -> str:
    try:
        timestamp = int(value or 0)
    except Exception:
        timestamp = 0
    if timestamp <= 0:
        return "Never"
    current = int(now or time.time())
    delta = max(0, current - timestamp)
    if delta < 5:
        return "Just now"
    if delta < 60:
        return f"{delta}s ago"
    minutes = delta // 60
    if minutes < 60:
        return f"{minutes} min ago" if minutes == 1 else f"{minutes} mins ago"
    hours = minutes // 60
    if hours < 24:
        return f"{hours} hour ago" if hours == 1 else f"{hours} hours ago"
    days = hours // 24
    return f"{days} day ago" if days == 1 else f"{days} days ago"


def _status_problem_flags(
    current_snapshot: Mapping[str, Any],
    system_snapshot: Mapping[str, Any],
) -> Dict[str, bool]:
    current_error = str(current_snapshot.get("last_error_kind") or "").strip().lower()
    system_error = str(system_snapshot.get("last_error_kind") or "").strip().lower()
    tls_failure = "tls" in {current_error, system_error} or any(
        snapshot.get("verify_enabled") is False
        for snapshot in (current_snapshot, system_snapshot)
        if snapshot
    )
    auth_failure = "auth" in {current_error, system_error}
    return {
        "tls_failure": tls_failure,
        "auth_failure": auth_failure,
    }


def build_tray_view(
    *,
    current_snapshot: Optional[Mapping[str, Any]] = None,
    system_snapshot: Optional[Mapping[str, Any]] = None,
    current_session_active: bool = False,
    busy_snapshot: Optional[Mapping[str, Any]] = None,
    restart_requests: Optional[Mapping[str, Mapping[str, Any]]] = None,
    now: Optional[int] = None,
    hostname: Optional[str] = None,
    build_id: Optional[str] = None,
    agent_guid: Optional[str] = None,
    start: Optional[Path] = None,
) -> Dict[str, Any]:
    timestamp = int(now or time.time())
    current = dict(current_snapshot or load_status_snapshot("currentuser", start=start))
    system = dict(system_snapshot or load_status_snapshot("system", start=start))
    busy = dict(busy_snapshot or _load_busy_snapshot())
    restarting = dict(restart_requests or active_restart_requests(start=start, now=timestamp))
    device_name = str(hostname or socket.gethostname() or "Unknown Device").strip() or "Unknown Device"
    resolved_guid = str(agent_guid or read_agent_guid(start)).strip()
    resolved_build_id = str(build_id or read_build_id() or "").strip()
    update_status = read_update_status() if callable(read_update_status) else {}
    effective_channel = str(
        update_status.get("effective_channel")
        or update_status.get("target_channel")
        or update_status.get("channel")
        or ""
    ).strip().lower()
    configured_server_url = read_configured_server_url(start)
    configured_server_host = extract_server_host(configured_server_url)
    current_fresh = bool(current) and snapshot_is_fresh(current, now=timestamp)
    system_fresh = bool(system) and snapshot_is_fresh(system, now=timestamp)
    current_live = current_fresh or (bool(current_session_active) and bool(current))
    current_booting = bool(current) and snapshot_in_boot_grace(current, now=timestamp)
    system_booting = bool(system) and snapshot_in_boot_grace(system, now=timestamp)
    current_mode = normalize_service_mode(current.get("service_mode") or "currentuser")
    current_has_initial_contact = bool(int(current.get("last_auth_success_at") or 0) and int(current.get("last_heartbeat_success_at") or 0))
    if current_mode == "currentuser" and current_live:
        current_has_initial_contact = True
    system_has_initial_contact = bool(int(system.get("last_auth_success_at") or 0) and int(system.get("last_heartbeat_success_at") or 0))
    system_socket_connected = bool(system.get("socket_connected"))
    wireguard = wireguard_summary(system)
    flags = _status_problem_flags(current, system)
    current_issue = (not current and not current_booting) or (current and not current_live and not current_booting)
    system_issue = (not system and not system_booting) or (system and not system_fresh and not system_booting)
    if restarting:
        overall_status = "Restarting"
    else:
        if flags["tls_failure"] or flags["auth_failure"] or wireguard["status_code"] == "unhealthy" or (current_issue and system_issue):
            overall_status = "Needs attention"
        elif (
            current_issue
            or system_issue
            or current_booting
            or system_booting
            or not current
            or not system
            or not current_has_initial_contact
            or not system_has_initial_contact
            or wireguard["status_code"] in {"recovering", "pending"}
        ):
            overall_status = "Starting up"
        elif current_live and system_fresh:
            overall_status = "Connected"
        else:
            overall_status = "Needs attention"

    if flags["tls_failure"]:
        security_status = "Certificate trust issue"
    elif flags["auth_failure"]:
        security_status = "Sign-in problem"
    elif overall_status in {"Starting up", "Restarting"} or not current_has_initial_contact or not system_has_initial_contact:
        security_status = "Checking connection"
    else:
        security_status = "Secure connection"

    connected_host = (
        str(current.get("server_host") or "").strip()
        or str(system.get("server_host") or "").strip()
        or extract_server_host(current.get("server_url"))
        or extract_server_host(system.get("server_url"))
        or configured_server_host
        or "Not configured"
    )
    site_name = (
        str(current.get("site_name") or "").strip()
        or str(system.get("site_name") or "").strip()
    )
    site_id = current.get("site_id")
    if site_id is None:
        site_id = system.get("site_id")
    try:
        site_id = int(site_id) if site_id is not None else None
    except Exception:
        site_id = None
    last_check_in_at = max(
        int(current.get("last_heartbeat_success_at") or 0),
        int(system.get("last_heartbeat_success_at") or 0),
    )
    last_check_in = format_relative_time(last_check_in_at, now=timestamp)
    last_heartbeat_value = f"{max(0, timestamp - last_check_in_at)}s" if last_check_in_at > 0 else "Never"
    activity_status = activity_label_from_snapshot(busy)

    if current_live:
        helper_session_status = "Running"
        helper_session_status_code = "healthy"
    elif current_booting or bool(current_session_active):
        helper_session_status = "Loading"
        helper_session_status_code = "neutral"
    else:
        helper_session_status = "Unavailable"
        helper_session_status_code = "warning"

    if (
        overall_status == "Connected"
        and security_status == "Secure connection"
        and system_socket_connected
        and wireguard["status_code"] == "healthy"
        and helper_session_status_code == "healthy"
    ):
        connection_status = "Healthy"
        connection_status_code = "healthy"
    elif overall_status in {"Starting up", "Restarting"} or wireguard["status_code"] in {"recovering", "pending"}:
        connection_status = "Checking"
        connection_status_code = "neutral"
    else:
        connection_status = "Needs Attention"
        connection_status_code = "warning"

    warnings: List[str] = []
    if not current and not current_booting:
        warnings.append("User session helper status is unavailable.")
    if not system and not system_booting:
        warnings.append("System service status is unavailable.")
    if current and not current_live and not current_booting:
        warnings.append("User session helper status is stale.")
    if system and not system_fresh and not system_booting:
        warnings.append("System service status is stale.")
    if flags["tls_failure"]:
        message = str(current.get("last_error_message") or system.get("last_error_message") or "").strip()
        warnings.append(message or "The secure connection could not be verified.")
    if flags["auth_failure"]:
        message = ""
        for snapshot in (current, system):
            if str(snapshot.get("last_error_kind") or "").strip().lower() == "auth":
                message = str(snapshot.get("last_error_message") or "").strip()
                if message:
                    break
        warnings.append(message or "The agent could not sign in to Borealis.")
    warnings.extend(_role_warnings(current, "User session"))
    warnings.extend(_role_warnings(system, "System service"))
    if wireguard["status_code"] == "unhealthy":
        warnings.append(wireguard["detail"])
    warnings = _dedupe_preserve(warnings)

    icon_tone = "healthy"
    if security_status in {"Certificate trust issue", "Sign-in problem"}:
        icon_tone = "error"
    elif overall_status in {"Starting up", "Restarting"}:
        icon_tone = "neutral"
    elif overall_status == "Needs attention":
        icon_tone = "warning"

    view: Dict[str, Any] = {
        "device_name": device_name,
        "build_id": resolved_build_id or "Unknown",
        "agent_guid": resolved_guid or "Unknown",
        "overall_status": overall_status,
        "connection_status": connection_status,
        "connection_status_code": connection_status_code,
        "security_status": security_status,
        "activity_status": activity_status,
        "connected_host": connected_host,
        "site_name": site_name,
        "site_id": site_id,
        "last_check_in": last_check_in,
        "last_check_in_at": last_check_in_at,
        "last_heartbeat_value": last_heartbeat_value,
        "wireguard_status": wireguard["label"],
        "wireguard_detail": wireguard["detail"],
        "wireguard_status_code": wireguard["status_code"],
        "system_socket_connected": system_socket_connected,
        "helper_session_status": helper_session_status,
        "helper_session_status_code": helper_session_status_code,
        "release_channel": effective_channel,
        "release_channel_label": _title_case_channel(effective_channel),
        "warnings": warnings,
        "current_snapshot": current,
        "system_snapshot": system,
        "busy_snapshot": busy,
        "restart_requests": restarting,
        "icon_tone": icon_tone,
        "logs_dir": str(agent_logs_directory(start)),
    }
    view["menu_entries"] = build_menu_entries(view)
    view["support_details"] = build_support_details(view)
    view["support_text"] = format_support_details(view)
    view["tooltip"] = "Borealis Agent"
    return view


def build_menu_entries(view: Mapping[str, Any]) -> List[Dict[str, Any]]:
    return [
        {"type": "line", "label": "Borealis Agent", "enabled": False},
        {"type": "line", "label": f"Status: {view.get('overall_status') or 'Starting up'}", "enabled": False},
        {"type": "line", "label": f"Security: {view.get('security_status') or 'Checking connection'}", "enabled": False},
        {"type": "line", "label": f"Activity: {view.get('activity_status') or 'Idle'}", "enabled": False},
        {"type": "line", "label": f"Connected to: {view.get('connected_host') or 'Not configured'}", "enabled": False},
        {"type": "line", "label": f"Last check-in: {view.get('last_check_in') or 'Never'}", "enabled": False},
        {"type": "separator"},
        {"type": "action", "key": "view_status_details", "label": "View Status Details...", "enabled": True},
        {"type": "separator"},
        {"type": "action", "key": "restart_agent", "label": "Restart Agent...", "enabled": True},
    ]


def build_support_details(view: Mapping[str, Any]) -> List[Dict[str, str]]:
    warnings = view.get("warnings") or []
    warning_text = "; ".join(str(item).strip() for item in warnings if str(item).strip()) or "None"
    return [
        {"label": "Device", "value": str(view.get("device_name") or "Unknown Device")},
        {"label": "Site", "value": str(view.get("site_name") or "Not Configured")},
        {"label": "Status", "value": str(view.get("overall_status") or "Unknown")},
        {"label": "Connection", "value": str(view.get("connection_status") or "Unknown")},
        {"label": "Security", "value": str(view.get("security_status") or "Checking connection")},
        {"label": "Connected to", "value": str(view.get("connected_host") or "Not configured")},
        {"label": "Last check-in", "value": str(view.get("last_check_in") or "Never")},
        {"label": "Activity", "value": str(view.get("activity_status") or "Idle")},
        {"label": "Release Channel", "value": str(view.get("release_channel_label") or "Unknown")},
        {"label": "WireGuard", "value": str(view.get("wireguard_status") or "Unavailable")},
        {"label": "Build", "value": str(view.get("build_id") or "Unknown")},
        {"label": "Agent ID", "value": str(view.get("agent_guid") or "Unknown")},
        {"label": "Warnings", "value": warning_text},
    ]


def format_support_details(view: Mapping[str, Any]) -> str:
    lines = []
    for item in build_support_details(view):
        lines.append(f"{item['label']}: {item['value']}")
    return "\n".join(lines)
