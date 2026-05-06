# ======================================================
# Data\Engine\services\API\server\info.py
# Description: Server information endpoints surfaced for administrative UX.
#
# API Endpoints (if applicable):
# - GET /api/server/time (Operator Session) - Returns the server clock in multiple formats.
# - GET /api/server/timezones (Operator Admin Session) - Returns the current server timezone and the selectable timezone inventory.
# - POST /api/server/timezone (Operator Admin Session) - Changes the engine host timezone.
# - GET /api/server/overview (Operator Admin Session) - Returns a Borealis Engine server/admin dashboard snapshot, including Compose-backed service rows in container mode.
# - POST /api/server/services/<service_key>/action (Operator Admin Session) - Queues a Compose-backed Engine.sh service action in container mode.
# - POST /api/server/services/<service_key>/restart (Operator Admin Session) - Queues a safe detached service restart via systemd-run for non-container installs.
# - POST /api/server/wireguard/recover (Operator Admin Session) - Forces a WireGuard listener recovery attempt when active tunnels exist.
# ======================================================

from __future__ import annotations

import base64
import json
import os
import platform
import re
import shlex
import shutil
import socket
import subprocess
import threading
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional, Sequence, Tuple
from zoneinfo import available_timezones

from cryptography import x509
from flask import Blueprint, Flask, jsonify, request

from Data.Engine.db import dbapi as sqlite3
from ....edge_runtime import DEFAULT_ACME_STORAGE_PATH, PROJECT_ROOT, load_settings
from ....public_endpoints import public_hostname as resolve_public_hostname
from ....public_endpoints import public_vnc_path as resolve_public_vnc_path
from ...RemoteDesktop.guacamole_proxy import guacd_health
from ....public_endpoints import wireguard_endpoint
from ....security import signing
from ...task_scheduler.queue import enqueue_service_action, list_service_snapshots, list_worker_snapshots
from ...ansible.runtime_settings import (
    DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT,
    DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT,
    load_ansible_runner_settings,
    save_ansible_runner_settings,
)
from ...VPN import WireGuardServerConfig, WireGuardServerManager, VpnTunnelService
from ...auth import RequestAuthContext

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


_SYSTEMD_SHOW_PROPERTIES = (
    "Id",
    "LoadState",
    "ActiveState",
    "SubState",
    "UnitFileState",
    "MainPID",
    "ExecMainStartTimestampUSec",
    "ActiveEnterTimestampUSec",
    "ExecMainStartTimestamp",
    "ActiveEnterTimestamp",
    "FragmentPath",
)
_POSTGRESQL_INSTANCE_PATTERN = re.compile(r"^postgresql@(.+)\.service$", re.IGNORECASE)
_PEM_CERT_PATTERN = re.compile(
    rb"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----",
    re.DOTALL,
)
_SERVICE_ACTION_TTL_SECONDS = 60
_SERVICE_RESTART_DELAY_SECONDS = 2
_CONTAINER_ACTION_DELAY_SECONDS = 2
_CERT_WARNING_DAYS = 30
_CERT_CRITICAL_DAYS = 14
_SYSTEMD_COMMAND_TIMEOUT_SECONDS = 8

_COMPOSE_SERVICE_SPECS: Tuple[Tuple[str, str], ...] = (
    ("api-backend", "API Backend"),
    ("job-scheduler", "Job Scheduler"),
    ("webui-frontend", "WebUI Frontend"),
    ("traefik-edge", "Traefik Edge"),
    ("postgres-db", "PostgreSQL"),
    ("remote-desktop-guacd", "Remote Desktop guacd"),
    ("wireguard-tunnel", "WireGuard Tunnel"),
)
_COMPOSE_SERVICE_ACTIONS: Mapping[str, Tuple[Mapping[str, str], ...]] = {
    "api-backend": (
        {"id": "restart", "label": "Restart", "action": "restart"},
    ),
    "job-scheduler": (
        {"id": "restart", "label": "Restart", "action": "restart"},
    ),
    "webui-frontend": (
        {"id": "rebuild_prod", "label": "Rebuild Prod", "action": "rebuild", "mode": "prod"},
        {"id": "rebuild_dev", "label": "Rebuild Dev", "action": "rebuild", "mode": "dev"},
    ),
    "traefik-edge": (
        {"id": "reload", "label": "Reload", "action": "reload"},
    ),
    "postgres-db": (
        {"id": "restart", "label": "Restart", "action": "restart"},
    ),
    "remote-desktop-guacd": (
        {"id": "restart", "label": "Restart", "action": "restart"},
    ),
    "wireguard-tunnel": (
        {"id": "reconcile", "label": "Reconcile", "action": "reconcile"},
    ),
}


@dataclass
class PendingServerAction:
    action: str
    created_at: int
    expires_at: int
    unit_name: str
    instance: str = ""


class ServerActionTracker:
    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._entries: Dict[Tuple[str, str], PendingServerAction] = {}

    def mark_pending(
        self,
        *,
        service_key: str,
        unit_name: str,
        action: str,
        instance: str = "",
        ttl_seconds: int = _SERVICE_ACTION_TTL_SECONDS,
    ) -> None:
        normalized_key = str(service_key or "").strip().lower()
        normalized_instance = str(instance or "").strip().lower()
        if not normalized_key or not unit_name or not action:
            return
        now_ts = _now_ts()
        with self._lock:
            self._entries[(normalized_key, normalized_instance)] = PendingServerAction(
                action=str(action).strip().lower(),
                created_at=now_ts,
                expires_at=now_ts + max(5, int(ttl_seconds)),
                unit_name=str(unit_name).strip(),
                instance=normalized_instance,
            )

    def get_pending(
        self,
        *,
        service_key: str,
        instance: str = "",
    ) -> Optional[Dict[str, Any]]:
        normalized_key = str(service_key or "").strip().lower()
        normalized_instance = str(instance or "").strip().lower()
        if not normalized_key:
            return None
        now_ts = _now_ts()
        with self._lock:
            record = self._entries.get((normalized_key, normalized_instance))
            if record is None:
                return None
            if record.expires_at <= now_ts:
                self._entries.pop((normalized_key, normalized_instance), None)
                return None
            return {
                "action": record.action,
                "created_at": record.created_at,
                "expires_at": record.expires_at,
                "unit_name": record.unit_name,
                "instance": record.instance or None,
            }


def _now_ts() -> int:
    return int(time.time())


def _error_response(error: str, message: str, status: int):
    return jsonify({"error": error, "message": message}), status


def _current_timezone_id() -> str:
    timedatectl_bin = shutil.which("timedatectl") or ""
    if timedatectl_bin:
        code, out, _err = _run_command(
            [timedatectl_bin, "show", "--property=Timezone", "--value"],
            timeout=5,
        )
        if code == 0:
            timezone_id = str(out or "").strip()
            if timezone_id:
                return timezone_id

    env_timezone = str(os.environ.get("TZ") or "").strip()
    if env_timezone:
        return env_timezone

    tzinfo = datetime.now().astimezone().tzinfo
    zone_key = getattr(tzinfo, "key", None)
    if zone_key:
        normalized_key = str(zone_key).strip()
        if normalized_key:
            return normalized_key

    timezone_file = Path("/etc/timezone")
    if timezone_file.is_file():
        try:
            value = timezone_file.read_text(encoding="utf-8", errors="replace").strip()
        except Exception:
            value = ""
        if value:
            return value

    localtime_path = Path("/etc/localtime")
    try:
        if localtime_path.is_symlink():
            resolved = localtime_path.resolve(strict=False)
            parts = resolved.parts
            if "zoneinfo" in parts:
                index = parts.index("zoneinfo")
                timezone_id = "/".join(parts[index + 1 :]).strip()
                if timezone_id:
                    return timezone_id
    except Exception:
        pass

    return str(datetime.now().astimezone().tzname() or "").strip()


def _serialize_time(now_local: datetime, now_utc: datetime, *, timezone_id: str = "") -> Dict[str, Any]:
    tz_label = now_local.tzname()
    display = now_local.strftime("%Y-%m-%d %H:%M:%S %Z").strip()
    if not display:
        display = now_local.isoformat()
    return {
        "epoch": int(now_local.timestamp()),
        "iso": now_local.isoformat(),
        "utc": now_utc.isoformat(),
        "timezone": tz_label,
        "timezone_id": str(timezone_id or "").strip(),
        "display": display,
    }


def _list_available_timezones() -> List[str]:
    try:
        zones = sorted(str(item).strip() for item in available_timezones() if str(item or "").strip())
    except Exception:
        zones = []
    return zones


def _timezone_change_supported() -> bool:
    return bool(shutil.which("timedatectl"))


def _set_system_timezone(timezone_id: str) -> Tuple[bool, str]:
    normalized = str(timezone_id or "").strip()
    if not normalized:
        return False, "A timezone identifier is required."

    timedatectl_bin = shutil.which("timedatectl") or ""
    if not timedatectl_bin:
        return False, "timedatectl is unavailable on this engine host."

    code, _out, err = _run_command(
        [timedatectl_bin, "set-timezone", normalized],
        timeout=_SYSTEMD_COMMAND_TIMEOUT_SECONDS,
    )
    if code != 0:
        return False, str(err or "Unable to set server timezone.").strip()

    try:
        os.environ.pop("TZ", None)
        if hasattr(time, "tzset"):
            time.tzset()
    except Exception:
        pass
    return True, ""


def _safe_int(value: Any, default: int = 0) -> int:
    try:
        return int(value)
    except Exception:
        return default


def _safe_float(value: Any, default: float = 0.0) -> float:
    try:
        return float(value)
    except Exception:
        return default


def _bytes_summary(total_bytes: int, free_bytes: int, *, path: str = "") -> Dict[str, Any]:
    total = max(0, int(total_bytes or 0))
    free = max(0, int(free_bytes or 0))
    used = max(0, total - free)
    used_percent = round((used / total) * 100.0, 2) if total > 0 else 0.0
    payload = {
        "total_bytes": total,
        "used_bytes": used,
        "free_bytes": free,
        "used_percent": used_percent,
    }
    if path:
        payload["path"] = path
    return payload


def _meminfo_kib() -> Dict[str, int]:
    path = Path("/proc/meminfo")
    values: Dict[str, int] = {}
    if not path.is_file():
        return values
    try:
        for raw_line in path.read_text(encoding="utf-8", errors="replace").splitlines():
            if ":" not in raw_line:
                continue
            key, raw_value = raw_line.split(":", 1)
            parts = raw_value.strip().split()
            if not parts:
                continue
            values[str(key).strip()] = _safe_int(parts[0], 0)
    except Exception:
        return {}
    return values


def _uptime_seconds() -> int:
    path = Path("/proc/uptime")
    if not path.is_file():
        return 0
    try:
        raw = path.read_text(encoding="utf-8", errors="replace").split()
        return max(0, int(float(raw[0]))) if raw else 0
    except Exception:
        return 0


def _iso_from_usec(value: Any) -> str:
    usec = _safe_int(value, 0)
    if usec <= 0:
        return ""
    try:
        return datetime.fromtimestamp(usec / 1_000_000.0, tz=timezone.utc).isoformat()
    except Exception:
        return ""


def _systemd_timestamp_value(value: Any) -> str:
    text = str(value or "").strip()
    if not text or text.lower() in {"n/a", "0"}:
        return ""
    return text


def _run_command(args: Sequence[str], *, timeout: int = _SYSTEMD_COMMAND_TIMEOUT_SECONDS) -> Tuple[int, str, str]:
    try:
        completed = subprocess.run(  # noqa: PLW1510 - explicit check=False
            list(args),
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
        return completed.returncode, completed.stdout or "", completed.stderr or ""
    except Exception as exc:
        return 1, "", repr(exc)


def _systemctl_show(systemctl_bin: str, unit_name: str) -> Dict[str, str]:
    args = [
        systemctl_bin,
        "show",
        unit_name,
        "--no-pager",
        "--property",
        ",".join(_SYSTEMD_SHOW_PROPERTIES),
    ]
    code, out, _err = _run_command(args)
    if code != 0 and not out.strip():
        return {}
    payload: Dict[str, str] = {}
    for raw_line in out.splitlines():
        line = raw_line.strip()
        if not line or "=" not in line:
            continue
        key, value = line.split("=", 1)
        payload[key] = value
    return payload


def _parse_systemd_list_units(output: str) -> List[str]:
    units: List[str] = []
    for raw_line in str(output or "").splitlines():
        line = raw_line.strip()
        if not line:
            continue
        unit_name = line.split()[0].strip()
        if unit_name:
            units.append(unit_name)
    return units


def _discover_postgresql_cluster_units(systemctl_bin: str) -> List[str]:
    discovered: List[str] = []
    commands = [
        [
            systemctl_bin,
            "list-unit-files",
            "postgresql@*.service",
            "--no-legend",
            "--no-pager",
        ],
        [
            systemctl_bin,
            "list-units",
            "postgresql@*.service",
            "--all",
            "--no-legend",
            "--no-pager",
        ],
    ]
    seen: set[str] = set()
    for command in commands:
        _code, out, _err = _run_command(command)
        for unit_name in _parse_systemd_list_units(out):
            match = _POSTGRESQL_INSTANCE_PATTERN.match(unit_name)
            if not match:
                continue
            instance = str(match.group(1) or "").strip()
            if not instance:
                continue
            normalized_unit = unit_name.strip()
            if normalized_unit not in seen:
                seen.add(normalized_unit)
                discovered.append(normalized_unit)
    return sorted(discovered)


def _normalize_service_status(show_payload: Mapping[str, Any]) -> str:
    load_state = str(show_payload.get("LoadState") or "").strip().lower()
    active_state = str(show_payload.get("ActiveState") or "").strip().lower()
    if load_state in {"not-found", "error", "bad-setting"}:
        return "critical"
    if active_state == "active":
        return "healthy"
    if active_state in {"activating", "reloading", "deactivating"}:
        return "warning"
    if active_state in {"failed", "inactive"}:
        return "critical"
    return "unknown"


def _get_action_tracker(context: Any) -> ServerActionTracker:
    existing = getattr(context, "_server_action_tracker", None)
    if isinstance(existing, ServerActionTracker):
        return existing
    tracker = ServerActionTracker()
    setattr(context, "_server_action_tracker", tracker)
    return tracker


def _service_restart_supported(*, systemctl_bin: str, systemd_run_bin: str, show_payload: Mapping[str, Any]) -> bool:
    if not systemctl_bin or not systemd_run_bin:
        return False
    load_state = str(show_payload.get("LoadState") or "").strip().lower()
    return load_state not in {"", "not-found", "error", "bad-setting"}


def _service_row(
    *,
    service_key: str,
    label: str,
    unit_name: str,
    show_payload: Mapping[str, Any],
    tracker: ServerActionTracker,
    restart_supported: bool,
    instance: str = "",
) -> Dict[str, Any]:
    started_at = (
        _iso_from_usec(show_payload.get("ExecMainStartTimestampUSec"))
        or _iso_from_usec(show_payload.get("ActiveEnterTimestampUSec"))
        or _systemd_timestamp_value(show_payload.get("ExecMainStartTimestamp"))
        or _systemd_timestamp_value(show_payload.get("ActiveEnterTimestamp"))
    )
    return {
        "key": service_key,
        "label": label,
        "instance": instance or None,
        "unit_name": unit_name,
        "active_state": str(show_payload.get("ActiveState") or "").strip().lower() or "unknown",
        "sub_state": str(show_payload.get("SubState") or "").strip().lower() or "unknown",
        "enabled_state": str(show_payload.get("UnitFileState") or "").strip().lower() or "unknown",
        "main_pid": _safe_int(show_payload.get("MainPID"), 0),
        "started_at": started_at or None,
        "fragment_path": str(show_payload.get("FragmentPath") or "").strip() or None,
        "restart_supported": bool(restart_supported),
        "pending_action": tracker.get_pending(service_key=service_key, instance=instance),
        "status": _normalize_service_status(show_payload),
    }


def _containerized_engine_enabled() -> bool:
    raw = str(os.environ.get("BOREALIS_ENGINE_CONTAINERIZED") or "").strip().lower()
    if raw in {"1", "true", "yes", "on"}:
        return True
    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    return (project_root / "Engine" / "Deploy" / "deploy-manifest.json").is_file()


def _compose_runtime_paths() -> Tuple[Path, Path, str]:
    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    compose_file = project_root / "Data" / "Engine" / "Containers" / "compose.yaml"
    env_file = project_root / "Engine" / "Deploy" / "compose.env"
    project_name = str(os.environ.get("BOREALIS_COMPOSE_PROJECT_NAME") or "borealis-engine")
    return compose_file, env_file, project_name


def _compose_service_actions(service_key: str) -> List[Dict[str, str]]:
    normalized = str(service_key or "").strip().lower()
    return [dict(item) for item in _COMPOSE_SERVICE_ACTIONS.get(normalized, ())]


def _parse_compose_ps_json(raw: str) -> Dict[str, Mapping[str, Any]]:
    text = str(raw or "").strip()
    if not text:
        return {}
    records: List[Mapping[str, Any]] = []
    try:
        parsed = json.loads(text)
        if isinstance(parsed, list):
            records = [item for item in parsed if isinstance(item, Mapping)]
        elif isinstance(parsed, Mapping):
            records = [parsed]
    except Exception:
        for line in text.splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                parsed_line = json.loads(line)
            except Exception:
                continue
            if isinstance(parsed_line, Mapping):
                records.append(parsed_line)
    by_service: Dict[str, Mapping[str, Any]] = {}
    for record in records:
        service = str(record.get("Service") or record.get("service") or "").strip()
        if service:
            by_service[service] = record
    return by_service


def _compose_ps_rows(context: Any = None) -> Dict[str, Mapping[str, Any]]:
    docker_bin = shutil.which("docker") or ""
    compose_file, env_file, project_name = _compose_runtime_paths()
    if not docker_bin or not Path("/var/run/docker.sock").exists() or not compose_file.is_file() or not env_file.is_file():
        return _compose_ps_rows_from_scheduler_snapshot(context)
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
    code, out, _err = _run_command(args, timeout=8)
    if code != 0:
        return _compose_ps_rows_from_scheduler_snapshot(context)
    return _parse_compose_ps_json(out)


def _compose_ps_rows_from_scheduler_snapshot(context: Any = None) -> Dict[str, Mapping[str, Any]]:
    database_url = str(getattr(context, "database_url", "") or "").strip()
    if not database_url:
        return {}
    conn = sqlite3.connect(database_url, timeout=5)
    try:
        snapshots = dict(list_service_snapshots(conn))
        conn.commit()
        return snapshots
    except Exception:
        return {}
    finally:
        conn.close()


def _docker_inspect_container(container_name: str) -> Mapping[str, Any]:
    normalized_name = str(container_name or "").strip()
    docker_bin = shutil.which("docker") or ""
    if not docker_bin or not normalized_name or not Path("/var/run/docker.sock").exists():
        return {}
    code, out, _err = _run_command([docker_bin, "inspect", normalized_name], timeout=8)
    if code != 0 or not str(out or "").strip():
        return {}
    try:
        parsed = json.loads(out)
    except Exception:
        return {}
    if isinstance(parsed, list) and parsed and isinstance(parsed[0], Mapping):
        return parsed[0]
    return {}


def _docker_image_exists(docker_bin: str, image: str) -> bool:
    normalized_image = str(image or "").strip()
    if not docker_bin or not normalized_image:
        return False
    code, _out, _err = _run_command([docker_bin, "image", "inspect", normalized_image], timeout=8)
    return code == 0


def _read_api_backend_image_from_manifest(path: Path) -> str:
    if not path.is_file():
        return ""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return ""
    candidates = [
        ((data.get("service_images") or {}).get("api-backend") or {}).get("image"),
        ((data.get("services") or {}).get("api-backend") or {}).get("image"),
    ]
    for candidate in candidates:
        image = str(candidate or "").strip()
        if image:
            return image
    return ""


def _resolve_api_backend_helper_image(docker_bin: str, project_root: Path) -> str:
    env_image = str(os.environ.get("BOREALIS_API_BACKEND_IMAGE") or "").strip()
    if env_image and _docker_image_exists(docker_bin, env_image):
        return env_image

    inspected = _docker_inspect_container("borealis-engine-api-backend")
    config_payload = inspected.get("Config") if isinstance(inspected.get("Config"), Mapping) else {}
    config_image = str(config_payload.get("Image") or "").strip()
    if config_image and _docker_image_exists(docker_bin, config_image):
        return config_image

    image_id = str(inspected.get("Image") or "").strip()
    if image_id and _docker_image_exists(docker_bin, image_id):
        return image_id

    for manifest_name in ("deploy-manifest.json", "image-manifest.json"):
        manifest_image = _read_api_backend_image_from_manifest(project_root / "Engine" / "Deploy" / manifest_name)
        if manifest_image and _docker_image_exists(docker_bin, manifest_image):
            return manifest_image

    local_image = "borealis-engine/api-backend:local"
    if _docker_image_exists(docker_bin, local_image):
        return local_image

    return ""


def _compose_status(state: str, health: str) -> str:
    normalized_state = str(state or "").strip().lower()
    normalized_health = str(health or "").strip().lower()
    if normalized_health in {"unhealthy"}:
        return "critical"
    if normalized_state in {"running"} and normalized_health not in {"starting"}:
        return "healthy"
    if normalized_state in {"restarting", "created"} or normalized_health in {"starting"}:
        return "warning"
    if normalized_state in {"exited", "dead", "removing", "paused"}:
        return "critical"
    return "unknown"


def _compose_display_status(state: str, health: str) -> str:
    normalized_health = str(health or "").strip().lower()
    normalized_state = str(state or "").strip().lower()
    if normalized_health:
        return format_title_case_for_api(normalized_health)
    if normalized_state:
        return format_title_case_for_api(normalized_state)
    return "Unknown"


def _normalize_webui_mode(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized in {"dev", "developer", "development"}:
        return "development"
    if normalized in {"prod", "production"}:
        return "production"
    return normalized or "unknown"


def _env_value_from_container(container_name: str, key: str) -> str:
    inspected = _docker_inspect_container(container_name)
    config_payload = inspected.get("Config") if isinstance(inspected.get("Config"), Mapping) else {}
    env_values = config_payload.get("Env") if isinstance(config_payload.get("Env"), list) else []
    prefix = f"{key}="
    for raw in env_values:
        text = str(raw or "")
        if text.startswith(prefix):
            return text[len(prefix) :].strip()
    return ""


def _current_webui_mode() -> str:
    mode = _env_value_from_container("borealis-engine-webui-frontend", "BOREALIS_WEBUI_MODE")
    if mode:
        return _normalize_webui_mode(mode)
    mode = str(os.environ.get("BOREALIS_WEBUI_MODE") or "").strip()
    if mode:
        return _normalize_webui_mode(mode)
    return "unknown"


def format_title_case_for_api(value: Any) -> str:
    raw = str(value or "").strip().replace("_", " ").replace("-", " ")
    if not raw:
        return "Unknown"
    return " ".join(part[:1].upper() + part[1:].lower() for part in raw.split())


def _compose_service_rows(context: Any) -> List[Dict[str, Any]]:
    tracker = _get_action_tracker(context)
    ps_rows = _compose_ps_rows(context)
    rows: List[Dict[str, Any]] = []
    for service_key, label in _COMPOSE_SERVICE_SPECS:
        record = ps_rows.get(service_key) or {}
        state = str(record.get("State") or record.get("state") or "").strip().lower() or "unknown"
        health = str(record.get("Health") or record.get("health") or "").strip().lower()
        container_name = str(
            record.get("Name")
            or record.get("Names")
            or record.get("ContainerName")
            or f"borealis-engine-{service_key}"
        ).strip()
        inspected = _docker_inspect_container(container_name)
        state_payload = inspected.get("State") if isinstance(inspected.get("State"), Mapping) else {}
        inspected_state = str(state_payload.get("Status") or "").strip().lower()
        inspected_health_payload = state_payload.get("Health") if isinstance(state_payload.get("Health"), Mapping) else {}
        inspected_health = str(inspected_health_payload.get("Status") or "").strip().lower()
        if inspected_state:
            state = inspected_state
        if inspected_health:
            health = inspected_health
        started_at = str(state_payload.get("StartedAt") or "").strip() if state_payload else ""
        display_status = _compose_display_status(state, health)
        rows.append(
            {
                "key": service_key,
                "label": label,
                "instance": None,
                "unit_name": container_name,
                "compose_service": service_key,
                "runtime": "compose",
                "docker_state": state,
                "docker_health": health or None,
                "docker_status": display_status,
                "display_status": display_status,
                "active_state": state,
                "sub_state": health or state,
                "enabled_state": "compose",
                "main_pid": _safe_int(state_payload.get("Pid"), 0) if state_payload else 0,
                "started_at": started_at if started_at and not started_at.startswith("0001-") else None,
                "fragment_path": None,
                "restart_supported": any(action.get("action") == "restart" for action in _compose_service_actions(service_key)),
                "actions": _compose_service_actions(service_key),
                "pending_action": tracker.get_pending(service_key=service_key),
                "status": _compose_status(state, health),
            }
        )
    return rows


def _collect_service_rows(context: Any) -> List[Dict[str, Any]]:
    if _containerized_engine_enabled():
        return _compose_service_rows(context)

    systemctl_bin = shutil.which("systemctl") or ""
    systemd_run_bin = shutil.which("systemd-run") or ""
    tracker = _get_action_tracker(context)

    service_specs = [
        ("borealis_engine", "Borealis Engine", "borealis-engine.service"),
        ("borealis_traefik", "Traefik", "borealis-traefik.service"),
    ]
    rows: List[Dict[str, Any]] = []
    for service_key, label, unit_name in service_specs:
        show_payload = _systemctl_show(systemctl_bin, unit_name) if systemctl_bin else {}
        rows.append(
            _service_row(
                service_key=service_key,
                label=label,
                unit_name=unit_name,
                show_payload=show_payload,
                tracker=tracker,
                restart_supported=_service_restart_supported(
                    systemctl_bin=systemctl_bin,
                    systemd_run_bin=systemd_run_bin,
                    show_payload=show_payload,
                ),
            )
        )

    postgres_units = _discover_postgresql_cluster_units(systemctl_bin) if systemctl_bin else []
    for unit_name in postgres_units:
        instance = ""
        match = _POSTGRESQL_INSTANCE_PATTERN.match(unit_name)
        if match:
            instance = str(match.group(1) or "").strip()
        show_payload = _systemctl_show(systemctl_bin, unit_name) if systemctl_bin else {}
        rows.append(
            _service_row(
                service_key="postgresql_cluster",
                label=f"PostgreSQL {instance}" if instance else "PostgreSQL",
                unit_name=unit_name,
                show_payload=show_payload,
                tracker=tracker,
                restart_supported=_service_restart_supported(
                    systemctl_bin=systemctl_bin,
                    systemd_run_bin=systemd_run_bin,
                    show_payload=show_payload,
                ),
                instance=instance,
            )
        )
    return rows


def _linux_interface_state(interface_name: str) -> Dict[str, Any]:
    normalized_name = str(interface_name or "").strip()
    if not normalized_name:
        return {
            "interface_present": False,
            "interface_up": False,
        }
    ip_bin = shutil.which("ip") or ""
    if not ip_bin:
        return {
            "interface_present": False,
            "interface_up": False,
        }
    code, out, _err = _run_command([ip_bin, "-details", "link", "show", "dev", normalized_name], timeout=5)
    if code != 0:
        return {
            "interface_present": False,
            "interface_up": False,
        }
    line = ""
    for raw_line in out.splitlines():
        if raw_line.strip():
            line = raw_line.strip()
            break
    upper_line = line.upper()
    return {
        "interface_present": True,
        "interface_up": "UP" in upper_line and "STATE DOWN" not in upper_line,
    }


def _get_tunnel_service_init_lock(context: Any) -> threading.Lock:
    existing = getattr(context, "_vpn_tunnel_service_init_lock", None)
    if existing is not None:
        return existing
    try:
        from eventlet import semaphore as eventlet_semaphore  # type: ignore

        lock = eventlet_semaphore.Semaphore(1)
    except Exception:
        lock = threading.Lock()
    current = getattr(context, "_vpn_tunnel_service_init_lock", None)
    if current is not None:
        return current
    setattr(context, "_vpn_tunnel_service_init_lock", lock)
    return lock


def _get_tunnel_service(adapters: "EngineServiceAdapters") -> Optional[VpnTunnelService]:
    context = adapters.context
    service = getattr(context, "vpn_tunnel_service", None)
    if service is not None:
        return service

    with _get_tunnel_service_init_lock(context):
        service = getattr(context, "vpn_tunnel_service", None)
        if service is not None:
            return service

        manager = getattr(context, "wireguard_server_manager", None)
        if manager is None:
            try:
                manager = WireGuardServerManager(
                    WireGuardServerConfig(
                        port=context.wireguard_port,
                        engine_virtual_ip=context.wireguard_engine_virtual_ip,
                        peer_network=context.wireguard_peer_network,
                        private_key_path=Path(context.wireguard_server_private_key_path),
                        public_key_path=Path(context.wireguard_server_public_key_path),
                        acl_allowlist_ports=tuple(context.wireguard_port_allowlist),
                        log_path=Path(context.vpn_tunnel_log_path),
                    )
                )
                setattr(context, "wireguard_server_manager", manager)
            except Exception:
                context.logger.error("Failed to initialize WireGuard server manager on demand.", exc_info=True)
                return None

        try:
            signer = signing.load_signer()
        except Exception:
            signer = None

        try:
            service = VpnTunnelService(
                context=context,
                wireguard_manager=manager,
                db_conn_factory=adapters.db_conn_factory,
                socketio=getattr(context, "socketio", None),
                service_log=adapters.service_log,
                signer=signer,
            )
        except Exception:
            context.logger.error("Failed to initialize VPN tunnel service on demand.", exc_info=True)
            return None
        setattr(context, "vpn_tunnel_service", service)
        return service


def _collect_wireguard_payload(adapters: "EngineServiceAdapters") -> Dict[str, Any]:
    service = _get_tunnel_service(adapters)
    context = adapters.context
    endpoint_host, endpoint_port = wireguard_endpoint(context)
    payload: Dict[str, Any] = {
        "interface_name": "borealis-wg",
        "interface_present": False,
        "interface_up": False,
        "active_tunnel_count": 0,
        "listener_healthy": False,
        "listener_reason": "unavailable",
        "listener_service_state": None,
        "recovery_in_progress": False,
        "last_recovery_attempt_at": None,
        "last_recovery_attempt_at_iso": "",
        "shell_port": int(getattr(context, "wireguard_shell_port", 0) or 0),
        "vnc_port": int(getattr(context, "vnc_port", 0) or 0),
        "vnc_ws_port": int(getattr(context, "vnc_ws_port", 0) or 0),
        "wireguard_endpoint": {
            "host": endpoint_host,
            "port": endpoint_port,
            "display": f"{endpoint_host}:{endpoint_port}" if endpoint_host else "",
        },
        "recover_supported": False,
        "active_tunnels": [],
    }
    if service is None:
        return payload

    sessions = service.list_sessions()
    wg_manager = getattr(service, "wg", None)
    interface_name = str(
        getattr(wg_manager, "interface_name", None)
        or getattr(wg_manager, "_interface_name", None)
        or "borealis-wg"
    ).strip() or "borealis-wg"
    payload["interface_name"] = interface_name
    payload.update(_linux_interface_state(interface_name))
    payload["active_tunnels"] = sessions
    payload["active_tunnel_count"] = len(sessions)
    payload["recover_supported"] = bool(sessions)

    low_level_health = {}
    if wg_manager is not None and hasattr(wg_manager, "check_listener_health"):
        try:
            low_level_health = dict(wg_manager.check_listener_health())
        except Exception:
            low_level_health = {}

    listener_status = {}
    if hasattr(service, "listener_status"):
        try:
            listener_status = dict(service.listener_status(refresh=bool(sessions)))
        except Exception:
            listener_status = {}

    payload["listener_healthy"] = bool(listener_status.get("listener_healthy"))
    payload["recovery_in_progress"] = bool(listener_status.get("recovery_in_progress"))
    payload["last_recovery_attempt_at"] = listener_status.get("last_recovery_attempt_at")
    payload["last_recovery_attempt_at_iso"] = str(listener_status.get("last_recovery_attempt_at_iso") or "")
    payload["listener_service_state"] = low_level_health.get("service_state")

    if not sessions:
        payload["listener_reason"] = "idle"
        return payload

    payload["listener_reason"] = str(
        low_level_health.get("reason")
        or listener_status.get("reason")
        or "listener_unhealthy"
    )
    return payload


def _load_acme_json(path: Path) -> Dict[str, Any]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return {}
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def _wildcard_matches_hostname(pattern: str, hostname: str) -> bool:
    normalized_pattern = str(pattern or "").strip().lower()
    normalized_host = str(hostname or "").strip().lower()
    if not normalized_pattern or not normalized_host:
        return False
    if normalized_pattern == normalized_host:
        return True
    if normalized_pattern.startswith("*."):
        suffix = normalized_pattern[1:]
        return normalized_host.endswith(suffix) and normalized_host.count(".") >= normalized_pattern.count(".")
    return False


def _decode_leaf_certificate(value: Any) -> Optional[x509.Certificate]:
    text = str(value or "").strip()
    if not text:
        return None

    candidates: List[bytes] = [text.encode("utf-8", errors="ignore")]
    try:
        decoded = base64.b64decode(text)
    except Exception:
        decoded = b""
    if decoded:
        candidates.insert(0, decoded)

    for blob in candidates:
        if not blob:
            continue
        pem_match = _PEM_CERT_PATTERN.search(blob)
        if pem_match:
            try:
                return x509.load_pem_x509_certificate(pem_match.group(0))
            except Exception:
                pass
        try:
            return x509.load_der_x509_certificate(blob)
        except Exception:
            continue
    return None


def _certificate_not_valid_after(cert: x509.Certificate) -> Optional[datetime]:
    value = getattr(cert, "not_valid_after_utc", None)
    if isinstance(value, datetime):
        return value
    try:
        raw = cert.not_valid_after
    except Exception:
        return None
    if raw is None:
        return None
    if raw.tzinfo is None:
        return raw.replace(tzinfo=timezone.utc)
    return raw.astimezone(timezone.utc)


def _certificate_severity(days_remaining: Optional[int]) -> str:
    if days_remaining is None:
        return "unknown"
    if days_remaining < _CERT_CRITICAL_DAYS:
        return "critical"
    if days_remaining <= _CERT_WARNING_DAYS:
        return "warning"
    return "healthy"


def _collect_public_certificates(acme_path: Path, *, fqdn: str) -> List[Dict[str, Any]]:
    if not acme_path.is_file():
        return []
    raw = _load_acme_json(acme_path)
    if not raw:
        return []

    normalized_fqdn = str(fqdn or "").strip().lower()
    now_utc = datetime.now(timezone.utc)
    results: List[Dict[str, Any]] = []
    for resolver_name, resolver_payload in raw.items():
        if not isinstance(resolver_payload, Mapping):
            continue
        certificates = resolver_payload.get("Certificates")
        if not isinstance(certificates, list):
            continue
        for entry in certificates:
            if not isinstance(entry, Mapping):
                continue
            domain_block = entry.get("domain")
            if isinstance(domain_block, Mapping):
                main_domain = str(domain_block.get("main") or "").strip()
                sans = [
                    str(item).strip()
                    for item in (domain_block.get("sans") or [])
                    if str(item or "").strip()
                ]
            else:
                main_domain = ""
                sans = []
            domains = [domain for domain in [main_domain, *sans] if domain]
            if normalized_fqdn and not any(_wildcard_matches_hostname(domain, normalized_fqdn) for domain in domains):
                continue

            certificate = _decode_leaf_certificate(entry.get("certificate"))
            expires_at: Optional[datetime] = _certificate_not_valid_after(certificate) if certificate else None
            days_remaining = None
            if expires_at is not None:
                days_remaining = int((expires_at - now_utc).total_seconds() // 86400)
            results.append(
                {
                    "resolver": str(resolver_name or "").strip(),
                    "main_domain": main_domain or (domains[0] if domains else ""),
                    "domains": domains,
                    "expires_at": expires_at.isoformat() if expires_at is not None else None,
                    "days_remaining": days_remaining,
                    "severity": _certificate_severity(days_remaining),
                }
            )

    return sorted(
        results,
        key=lambda item: (
            item.get("days_remaining") is None,
            int(item.get("days_remaining") or 0),
            str(item.get("main_domain") or ""),
        ),
    )


def _collect_public_edge_payload(context: Any) -> Dict[str, Any]:
    settings = None
    settings_path_value = str(getattr(context, "letsencrypt_settings_path", "") or "").strip()
    settings_path = Path(settings_path_value).expanduser() if settings_path_value else None
    if settings_path is not None and settings_path.is_file():
        try:
            settings = load_settings(settings_path)
        except Exception:
            settings = None

    fqdn = str(
        (getattr(settings, "public_hostname", None) if settings is not None else None)
        or resolve_public_hostname(context)
        or ""
    ).strip()
    endpoint_host, endpoint_port = wireguard_endpoint(context)
    acme_path = Path(
        str(getattr(settings, "acme_storage_path", "") or DEFAULT_ACME_STORAGE_PATH)
    ).expanduser()
    return {
        "enabled": bool(getattr(context, "public_edge_enabled", False)),
        "fqdn": fqdn,
        "acme_email": str(getattr(settings, "acme_email", "") or "").strip(),
        "public_base_url": str(getattr(context, "public_base_url", "") or "").strip(),
        "public_vnc_path": resolve_public_vnc_path(context),
        "wireguard_endpoint": f"{endpoint_host}:{endpoint_port}" if endpoint_host else "",
        "certificates": _collect_public_certificates(acme_path, fqdn=fqdn),
    }


def _collect_host_payload(context: Any) -> Dict[str, Any]:
    now_utc = datetime.now(timezone.utc)
    now_local = now_utc.astimezone()
    timezone_id = _current_timezone_id()
    return {
        "hostname": socket.gethostname(),
        "kernel": platform.release(),
        "architecture": platform.machine(),
        "engine_mode": str(
            os.environ.get("BOREALIS_ENGINE_MODE")
            or context.config.get("ENGINE_MODE")
            or "unknown"
        ).strip()
        or "unknown",
        "webui_mode": _current_webui_mode(),
        "server_time": _serialize_time(now_local, now_utc, timezone_id=timezone_id),
        "timezone": now_local.tzname() or "",
        "timezone_id": timezone_id,
        "timezone_change_supported": _timezone_change_supported(),
        "uptime_seconds": _uptime_seconds(),
        "public_base_url": str(getattr(context, "public_base_url", "") or "").strip(),
        "public_hostname": str(resolve_public_hostname(context) or "").strip(),
        "public_https_port": int(getattr(context, "public_https_port", 443) or 443),
    }


def _collect_resource_payload() -> Dict[str, Any]:
    meminfo = _meminfo_kib()
    memory_total = int(meminfo.get("MemTotal", 0)) * 1024
    memory_free = int(meminfo.get("MemAvailable", meminfo.get("MemFree", 0))) * 1024
    swap_total = int(meminfo.get("SwapTotal", 0)) * 1024
    swap_free = int(meminfo.get("SwapFree", 0)) * 1024
    try:
        load_average = [round(value, 2) for value in os.getloadavg()]
    except Exception:
        load_average = [0.0, 0.0, 0.0]
    root_usage = shutil.disk_usage("/")
    project_usage = shutil.disk_usage(str(PROJECT_ROOT))
    return {
        "load_average": load_average,
        "cpu_count": int(os.cpu_count() or 0),
        "memory": _bytes_summary(memory_total, memory_free),
        "swap": _bytes_summary(swap_total, swap_free),
        "disk_root": _bytes_summary(root_usage.total, root_usage.free, path="/"),
        "disk_project": _bytes_summary(project_usage.total, project_usage.free, path=str(PROJECT_ROOT)),
    }


def _collect_operator_session_count(adapters: "EngineServiceAdapters") -> int:
    registry = getattr(adapters.context, "operator_presence_registry", None)
    if registry is None:
        return 0
    try:
        if hasattr(registry, "count_sessions"):
            return max(0, _safe_int(registry.count_sessions(), 0))
        if hasattr(registry, "list_sessions"):
            return len(registry.list_sessions())
    except Exception:
        adapters.context.logger.debug("Failed to read operator presence registry.", exc_info=True)
        return 0
    return 0


def _collect_vnc_session_payload(adapters: "EngineServiceAdapters") -> Dict[str, Any]:
    manager = getattr(adapters.context, "vnc_collaboration_manager", None)
    guacamole = guacd_health(adapters.context)
    if manager is None or not hasattr(manager, "list_sessions") or not hasattr(manager, "session_snapshot"):
        return {
            "active_session_count": 0,
            "active_sessions": [],
            "viewers": {
                "guacamole": {
                    "enabled": bool(guacamole.get("enabled")),
                    "available": bool(guacamole.get("enabled")) and bool(guacamole.get("available")),
                    "reason": str(guacamole.get("reason") or ""),
                },
            },
        }
    try:
        sessions = list(manager.list_sessions())
        snapshots = [manager.session_snapshot(session) for session in sessions]
    except Exception:
        adapters.context.logger.debug("Failed to collect VNC collaboration sessions.", exc_info=True)
        snapshots = []
    return {
        "active_session_count": len(snapshots),
        "active_sessions": snapshots,
        "viewers": {
            "guacamole": {
                "enabled": bool(guacamole.get("enabled")),
                "available": bool(guacamole.get("enabled")) and bool(guacamole.get("available")),
                "reason": str(guacamole.get("reason") or ""),
            },
        },
    }


def _collect_security_payload(adapters: "EngineServiceAdapters") -> Dict[str, Any]:
    aegis_service = getattr(adapters, "aegis_cipher_service", None)
    if aegis_service is None or not hasattr(aegis_service, "status"):
        return {
            "aegis": {
                "configured": False,
                "locked": False,
                "unlock_scope": "engine_global",
                "secret_scope": ["credentials", "github_token"],
                "updated_at": 0,
            }
        }
    try:
        aegis_payload = dict(aegis_service.status())
    except Exception:
        aegis_payload = {
            "configured": False,
            "locked": False,
            "unlock_scope": "engine_global",
            "secret_scope": ["credentials", "github_token"],
            "updated_at": 0,
        }
    return {"aegis": aegis_payload}


def _collect_ansible_runner_payload() -> Dict[str, Any]:
    try:
        settings = load_ansible_runner_settings()
    except Exception:
        settings = {
            "job_concurrency_limit": DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT,
            "global_concurrency_limit": DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT,
        }
    return {
        "job_concurrency_limit": max(
            1,
            _safe_int(
                settings.get("job_concurrency_limit"),
                DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT,
            ),
        ),
        "global_concurrency_limit": max(
            1,
            _safe_int(
                settings.get("global_concurrency_limit"),
                DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT,
            ),
        ),
    }


def _build_overview_payload(adapters: "EngineServiceAdapters") -> Dict[str, Any]:
    return {
        "collected_at": datetime.now(timezone.utc).isoformat(),
        "host": _collect_host_payload(adapters.context),
        "resources": _collect_resource_payload(),
        "services": _collect_service_rows(adapters.context),
        "wireguard": _collect_wireguard_payload(adapters),
        "public_edge": _collect_public_edge_payload(adapters.context),
        "security": _collect_security_payload(adapters),
        "ansible_runner": _collect_ansible_runner_payload(),
        "agent_release_channels": _collect_agent_release_channels_payload(adapters),
        "remote_desktop": _collect_vnc_session_payload(adapters),
        "operator_session_count": _collect_operator_session_count(adapters),
        "workers": _collect_worker_payload(adapters),
    }


def _collect_worker_payload(adapters: "EngineServiceAdapters") -> Dict[str, Any]:
    conn = adapters.db_conn_factory()
    try:
        rows = list_worker_snapshots(conn)
        conn.commit()
    finally:
        conn.close()
    active = [row for row in rows if str(row.get("status") or "").lower() in {"starting", "running", "idle"}]
    return {
        "active_count": len(active),
        "workers": rows,
    }


def _collect_agent_release_channels_payload(adapters: "EngineServiceAdapters") -> Dict[str, Any]:
    manager = getattr(adapters, "agent_release_manager", None)
    if manager is None:
        return {
            "default_channel": "stable",
            "github": {"repo": "", "default_branch": ""},
            "channels": {"stable": {}, "unstable": {}},
            "github_token": {"has_token": False, "reset_required": False, "reset_at": 0},
            "last_refresh_started_at": 0,
            "last_refresh_completed_at": 0,
            "last_refresh_error": "release channel manager unavailable",
        }
    try:
        return manager.get_settings()
    except Exception as exc:
        return {
            "default_channel": "stable",
            "github": {"repo": "", "default_branch": ""},
            "channels": {"stable": {}, "unstable": {}},
            "github_token": {"has_token": False, "reset_required": False, "reset_at": 0},
            "last_refresh_started_at": 0,
            "last_refresh_completed_at": 0,
            "last_refresh_error": str(exc),
        }


def _resolve_restart_unit(
    *,
    service_key: str,
    body: Mapping[str, Any],
    service_rows: Sequence[Mapping[str, Any]],
) -> Tuple[Optional[str], str]:
    normalized_key = str(service_key or "").strip().lower()
    if normalized_key == "borealis_engine":
        return "borealis-engine.service", ""
    if normalized_key == "borealis_traefik":
        return "borealis-traefik.service", ""
    if normalized_key != "postgresql_cluster":
        return None, ""

    instance = str(body.get("instance") or "").strip()
    if not instance:
        return None, ""
    for row in service_rows:
        if str(row.get("key") or "").strip().lower() != "postgresql_cluster":
            continue
        if str(row.get("instance") or "").strip().lower() == instance.lower():
            return str(row.get("unit_name") or "").strip(), instance
    return None, instance


def _queue_detached_restart(
    *,
    service_key: str,
    unit_name: str,
) -> Tuple[bool, str, str]:
    systemd_run_bin = shutil.which("systemd-run") or ""
    systemctl_bin = shutil.which("systemctl") or ""
    if not systemd_run_bin or not systemctl_bin:
        return False, "", "systemd-run or systemctl is unavailable on this engine host."

    job_unit = f"borealis-admin-restart-{service_key}-{uuid.uuid4().hex[:8]}"
    shell_command = f"sleep {_SERVICE_RESTART_DELAY_SECONDS}; {shlex.quote(systemctl_bin)} restart {shlex.quote(unit_name)}"
    args = [
        systemd_run_bin,
        f"--unit={job_unit}",
        "--collect",
        "--service-type=oneshot",
        "/bin/bash",
        "-lc",
        shell_command,
    ]
    code, out, err = _run_command(args, timeout=_SYSTEMD_COMMAND_TIMEOUT_SECONDS)
    if code != 0:
        message = str(err or out or "systemd-run failed").strip()
        return False, "", message
    return True, f"{job_unit}.service", ""


def _resolve_compose_service_action(service_key: str, body: Mapping[str, Any]) -> Optional[Dict[str, str]]:
    normalized_service = str(service_key or "").strip().lower()
    requested_action = str(body.get("action") or "").strip().lower()
    requested_mode = str(body.get("mode") or "").strip().lower()
    requested_id = str(body.get("id") or body.get("action_id") or "").strip().lower()
    for action in _compose_service_actions(normalized_service):
        action_name = str(action.get("action") or "").strip().lower()
        action_mode = str(action.get("mode") or "").strip().lower()
        action_id = str(action.get("id") or "").strip().lower()
        if requested_id and requested_id == action_id:
            return action
        if requested_action != action_name:
            continue
        if action_mode and requested_mode and requested_mode != action_mode:
            continue
        if action_mode and not requested_mode and requested_action == "rebuild":
            continue
        return action
    return None


def _queue_compose_service_action(
    *,
    service_key: str,
    action: Mapping[str, str],
) -> Tuple[bool, str, str]:
    docker_bin = shutil.which("docker") or ""
    if not docker_bin:
        return False, "", "docker CLI is unavailable inside the API backend container."

    project_root = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or PROJECT_ROOT)
    image = _resolve_api_backend_helper_image(docker_bin, project_root)
    if not image:
        return False, "", "Unable to resolve the running api-backend helper image."

    action_name = str(action.get("action") or "").strip().lower()
    action_mode = str(action.get("mode") or "").strip().lower()
    if not action_name:
        return False, "", "A service action is required."

    command_parts = ["bash", "Engine.sh", "--service", service_key, action_name]
    if action_mode:
        command_parts.append(action_mode)
    shell_command = f"sleep {_CONTAINER_ACTION_DELAY_SECONDS}; {shlex.join(command_parts)}"
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
    code, out, err = _run_command(args, timeout=15)
    if code != 0:
        return False, "", str(err or out or "docker helper launch failed").strip()
    helper_id = str(out or "").strip().splitlines()[0] if str(out or "").strip() else helper_name
    return True, helper_id, ""


def _queue_task_scheduler_service_action(
    adapters: "EngineServiceAdapters",
    *,
    service_key: str,
    action: Mapping[str, str],
) -> Tuple[bool, str, str]:
    conn = adapters.db_conn_factory()
    try:
        work_id = enqueue_service_action(conn, service_key=service_key, action=action)
        conn.commit()
    except Exception as exc:
        try:
            conn.rollback()
        except Exception:
            pass
        return False, "", str(exc)
    finally:
        conn.close()
    return True, str(work_id), ""


def register_info(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Expose server telemetry endpoints used by the admin interface."""

    blueprint = Blueprint("engine_server_info", __name__)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )

    @blueprint.route("/api/server/time", methods=["GET"])
    def server_time() -> Any:
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        now_utc = datetime.now(timezone.utc)
        now_local = now_utc.astimezone()
        payload = _serialize_time(now_local, now_utc, timezone_id=_current_timezone_id())
        return jsonify(payload)

    @blueprint.route("/api/server/timezones", methods=["GET"])
    def server_timezones() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        current_timezone = _current_timezone_id()
        return jsonify(
            {
                "current_timezone": current_timezone,
                "change_supported": _timezone_change_supported(),
                "timezones": _list_available_timezones(),
            }
        )

    @blueprint.route("/api/server/timezone", methods=["POST"])
    def set_server_timezone() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]

        body = request.get_json(silent=True) or {}
        timezone_id = str(body.get("timezone") or "").strip()
        if not timezone_id:
            return _error_response("timezone_required", "A timezone identifier is required.", 400)

        available = _list_available_timezones()
        if timezone_id not in available:
            return _error_response("invalid_timezone", "Unsupported timezone identifier.", 400)

        if not _timezone_change_supported():
            return _error_response(
                "timezone_change_unsupported",
                "Timezone changes are unavailable on this engine host.",
                409,
            )

        changed, error_message = _set_system_timezone(timezone_id)
        if not changed:
            return _error_response("timezone_change_failed", error_message or "Unable to change server timezone.", 500)

        host_payload = _collect_host_payload(adapters.context)
        return jsonify(
            {
                "status": "ok",
                "timezone": timezone_id,
                "host": host_payload,
            }
        )

    @blueprint.route("/api/server/overview", methods=["GET"])
    def server_overview() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        return jsonify(_build_overview_payload(adapters))

    @blueprint.route("/api/server/workers", methods=["GET"])
    def server_workers() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        return jsonify(_collect_worker_payload(adapters))

    @blueprint.route("/api/server/agent-release-channels", methods=["GET"])
    def get_agent_release_channels() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        manager = getattr(adapters, "agent_release_manager", None)
        if manager is None:
            return _error_response("release_channels_unavailable", "Agent release channels are unavailable on this engine.", 503)
        return jsonify(manager.get_settings())

    @blueprint.route("/api/server/agent-release-channels", methods=["PUT"])
    def update_agent_release_channels() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        manager = getattr(adapters, "agent_release_manager", None)
        if manager is None:
            return _error_response("release_channels_unavailable", "Agent release channels are unavailable on this engine.", 503)
        body = request.get_json(silent=True) or {}
        default_channel = body.get("default_channel") if "default_channel" in body else None
        repo = body.get("repo") if "repo" in body else None
        return jsonify(manager.set_settings(default_channel=default_channel, repo=repo))

    @blueprint.route("/api/server/agent-release-channels/refresh", methods=["POST"])
    def refresh_agent_release_channels() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        manager = getattr(adapters, "agent_release_manager", None)
        if manager is None:
            return _error_response("release_channels_unavailable", "Agent release channels are unavailable on this engine.", 503)
        return jsonify(manager.refresh_channels(force=True))

    @blueprint.route("/api/server/ansible-runner-settings", methods=["GET"])
    def get_ansible_runner_settings() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        return jsonify(_collect_ansible_runner_payload())

    @blueprint.route("/api/server/ansible-runner-settings", methods=["PUT"])
    def update_ansible_runner_settings() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        body = request.get_json(silent=True) or {}
        normalized: Dict[str, int] = {}
        for field_name, label in (
            ("job_concurrency_limit", "Per-job concurrency"),
            ("global_concurrency_limit", "Global concurrency"),
        ):
            raw_value = body.get(field_name)
            if raw_value in {None, ""}:
                return _error_response(
                    "invalid_ansible_runner_settings",
                    f"{label} is required.",
                    400,
                )
            try:
                parsed_value = int(raw_value)
            except Exception:
                return _error_response(
                    "invalid_ansible_runner_settings",
                    f"{label} must be a whole number greater than 0.",
                    400,
                )
            if parsed_value < 1:
                return _error_response(
                    "invalid_ansible_runner_settings",
                    f"{label} must be a whole number greater than 0.",
                    400,
                )
            normalized[field_name] = parsed_value
        save_ansible_runner_settings(
            normalized
        )
        return jsonify({"status": "ok", "ansible_runner": _collect_ansible_runner_payload()})

    @blueprint.route("/api/server/services/<service_key>/action", methods=["POST"])
    def run_service_action(service_key: str) -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]

        if not _containerized_engine_enabled():
            return _error_response(
                "service_action_unsupported",
                "Generic service actions are available for containerized Engine deployments only.",
                409,
            )

        normalized_key = str(service_key or "").strip().lower()
        if normalized_key not in dict(_COMPOSE_SERVICE_SPECS):
            return _error_response("invalid_service_key", "Unsupported service key.", 404)

        body = request.get_json(silent=True) or {}
        action = _resolve_compose_service_action(normalized_key, body)
        if action is None:
            return _error_response("invalid_service_action", "Unsupported action for this service.", 400)

        queued, helper_id, error_message = _queue_task_scheduler_service_action(
            adapters,
            service_key=normalized_key,
            action=action,
        )
        if not queued:
            return _error_response("service_action_failed", error_message or "Unable to queue service action.", 500)

        tracker = _get_action_tracker(adapters.context)
        action_name = str(action.get("action") or "").strip().lower()
        mode = str(action.get("mode") or "").strip().lower()
        tracker.mark_pending(
            service_key=normalized_key,
            unit_name=f"Engine.sh --service {normalized_key} {action_name}{f' {mode}' if mode else ''}",
            action=action_name,
        )
        scheduled_for = (datetime.now(timezone.utc) + timedelta(seconds=_CONTAINER_ACTION_DELAY_SECONDS)).isoformat()
        return (
            jsonify(
                {
                    "queued": True,
                    "service_key": normalized_key,
                    "action": action_name,
                    "mode": mode or None,
                    "work_item_id": helper_id,
                    "scheduled_for": scheduled_for,
                }
            ),
            202,
        )

    @blueprint.route("/api/server/services/<service_key>/restart", methods=["POST"])
    def restart_service(service_key: str) -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]

        normalized_key = str(service_key or "").strip().lower()
        if _containerized_engine_enabled():
            action = _resolve_compose_service_action(normalized_key, {"action": "restart"})
            if action is None:
                return _error_response("invalid_service_key", "Unsupported service key.", 404)
            queued, helper_id, error_message = _queue_task_scheduler_service_action(
                adapters,
                service_key=normalized_key,
                action=action,
            )
            if not queued:
                return _error_response("restart_failed", error_message or "Unable to queue restart.", 500)
            tracker = _get_action_tracker(adapters.context)
            tracker.mark_pending(
                service_key=normalized_key,
                unit_name=f"Engine.sh --service {normalized_key} restart",
                action="restart",
            )
            scheduled_for = (datetime.now(timezone.utc) + timedelta(seconds=_CONTAINER_ACTION_DELAY_SECONDS)).isoformat()
            return (
                jsonify(
                    {
                        "queued": True,
                        "service_key": normalized_key,
                        "action": "restart",
                        "work_item_id": helper_id,
                        "scheduled_for": scheduled_for,
                    }
                ),
                202,
            )

        if normalized_key not in {"borealis_engine", "borealis_traefik", "postgresql_cluster"}:
            return _error_response("invalid_service_key", "Unsupported service key.", 404)

        body = request.get_json(silent=True) or {}
        service_rows = _collect_service_rows(adapters.context)
        unit_name, instance = _resolve_restart_unit(
            service_key=normalized_key,
            body=body,
            service_rows=service_rows,
        )
        if not unit_name:
            if normalized_key == "postgresql_cluster":
                return _error_response(
                    "invalid_postgresql_instance",
                    "A valid PostgreSQL cluster instance is required.",
                    400,
                )
            return _error_response("service_unavailable", "The requested service unit could not be resolved.", 404)

        matching_row = next(
            (
                row
                for row in service_rows
                if str(row.get("key") or "").strip().lower() == normalized_key
                and str(row.get("unit_name") or "").strip() == unit_name
            ),
            None,
        )
        if not matching_row or not bool(matching_row.get("restart_supported")):
            return _error_response(
                "restart_unsupported",
                "This service cannot be restarted safely on the current engine host.",
                409,
            )

        queued, job_unit, error_message = _queue_detached_restart(
            service_key=normalized_key,
            unit_name=unit_name,
        )
        if not queued:
            return _error_response("restart_failed", error_message or "Unable to queue restart.", 500)

        tracker = _get_action_tracker(adapters.context)
        tracker.mark_pending(
            service_key=normalized_key,
            unit_name=unit_name,
            action="restart",
            instance=instance,
        )
        scheduled_for = (datetime.now(timezone.utc) + timedelta(seconds=_SERVICE_RESTART_DELAY_SECONDS)).isoformat()
        return (
            jsonify(
                {
                    "queued": True,
                    "service_key": normalized_key,
                    "unit_name": unit_name,
                    "job_unit": job_unit,
                    "scheduled_for": scheduled_for,
                }
            ),
            202,
        )

    @blueprint.route("/api/server/wireguard/recover", methods=["POST"])
    def recover_wireguard_listener() -> Any:
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]

        service = _get_tunnel_service(adapters)
        if service is None:
            return _error_response(
                "wireguard_unavailable",
                "The Borealis WireGuard tunnel service is unavailable on this engine host.",
                503,
            )

        sessions = service.list_sessions()
        if not sessions:
            return _error_response(
                "no_active_sessions",
                "Recover Listener is only available while Borealis has active VPN sessions.",
                409,
            )

        payload = service.recover_listener(
            trigger="admin_dashboard",
            reason="manual_admin_recovery",
            force=True,
        )
        return jsonify({"status": "ok", "wireguard": payload})

    app.register_blueprint(blueprint)
