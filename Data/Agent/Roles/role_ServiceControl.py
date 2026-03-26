import json
import os
import platform
import shutil
import socket
import subprocess
import threading
import time
from typing import Any, Dict, List, Optional, Tuple


ROLE_NAME = "service_control"
ROLE_CONTEXTS = ["system"]

IS_WINDOWS = os.name == "nt"
IS_LINUX = platform.system().lower() == "linux"
SERVICE_REFRESH_INTERVAL_SECONDS = 60
BOOST_REFRESH_INTERVAL_SECONDS = 3
BOOST_WINDOW_SECONDS = 30
WINDOWS_NO_WINDOW = 0x08000000 if IS_WINDOWS else 0

STATUS_LABELS = {
    "running": "Running",
    "stopped": "Stopped",
    "starting": "Starting",
    "stopping": "Stopping",
    "paused": "Paused",
    "failed": "Failed",
    "unknown": "Unknown",
}


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_status_code(value: Any) -> str:
    text = _clean_text(value).lower().replace(" ", "_").replace("-", "_")
    aliases = {
        "active": "running",
        "running": "running",
        "up": "running",
        "inactive": "stopped",
        "stopped": "stopped",
        "dead": "stopped",
        "disabled": "stopped",
        "activating": "starting",
        "start_pending": "starting",
        "starting": "starting",
        "reloading": "starting",
        "deactivating": "stopping",
        "stop_pending": "stopping",
        "stopping": "stopping",
        "paused": "paused",
        "failed": "failed",
        "error": "failed",
    }
    normalized = aliases.get(text, text)
    if normalized in STATUS_LABELS:
        return normalized
    return "unknown"


def _normalize_action(value: Any) -> str:
    text = _clean_text(value).lower()
    if text in {"start", "stop", "restart"}:
        return text
    return ""


def _hostname() -> str:
    try:
        return str(socket.gethostname() or "").strip()
    except Exception:
        return ""


def _windows_service_command(action: str, service_name: str) -> str:
    escaped_name = service_name.replace("'", "''")
    if action == "start":
        return f"$ErrorActionPreference='Stop'; Start-Service -Name '{escaped_name}'"
    if action == "stop":
        return f"$ErrorActionPreference='Stop'; Stop-Service -Name '{escaped_name}' -Force"
    return f"$ErrorActionPreference='Stop'; Restart-Service -Name '{escaped_name}' -Force"


def _linux_status_code(active_state: str, sub_state: str) -> str:
    active = _clean_text(active_state).lower()
    sub = _clean_text(sub_state).lower()
    if active == "active":
        if sub in {"running", "listening", "exited"}:
            return "running"
        return "running"
    if active == "inactive":
        return "stopped"
    if active == "failed":
        return "failed"
    if active == "activating":
        return "starting"
    if active == "deactivating":
        return "stopping"
    return _normalize_status_code(sub or active)


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = "Service Control"
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")
        self._build_id_getter = hooks.get("get_agent_build_id")
        self._service_mode = _clean_text(hooks.get("service_mode") or getattr(ctx, "service_mode", None)) or "system"
        self._stop = threading.Event()
        self._wakeup = threading.Event()
        self._lock = threading.RLock()
        self._thread: Optional[threading.Thread] = None
        self._last_error = ""
        self._last_refresh_at = 0
        self._last_service_count = 0
        self._fast_poll_until = 0.0
        self._supported, self._unsupported_reason = self._detect_support()
        if self._supported:
            self._thread = threading.Thread(target=self._poll_loop, daemon=True)
            self._thread.start()
        else:
            self._log(self._unsupported_reason or "Service control role is unsupported on this platform.")

    def _detect_support(self) -> Tuple[bool, str]:
        if IS_WINDOWS:
            return True, ""
        if IS_LINUX and shutil.which("systemctl"):
            return True, ""
        if IS_LINUX:
            return False, "systemctl is unavailable on this Linux agent."
        return False, f"Unsupported service-control platform '{platform.system()}'."

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="service_control.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass

    def _http_client(self) -> Optional[Any]:
        if callable(self._http_client_factory):
            try:
                return self._http_client_factory()
            except Exception:
                return None
        return None

    def _agent_build_id(self) -> str:
        if callable(self._build_id_getter):
            try:
                return _clean_text(self._build_id_getter())
            except Exception:
                return ""
        return ""

    def _run_subprocess(self, args: List[str], *, timeout: int = 120) -> subprocess.CompletedProcess:
        return subprocess.run(
            args,
            capture_output=True,
            text=True,
            timeout=timeout,
            creationflags=WINDOWS_NO_WINDOW,
            check=False,
        )

    def _query_windows_services(self) -> List[Dict[str, Any]]:
        command = (
            "$services = Get-CimInstance Win32_Service | "
            "Select-Object Name,DisplayName,Description,State; "
            "$services | ConvertTo-Json -Depth 3 -Compress"
        )
        result = self._run_subprocess(["powershell.exe", "-NoProfile", "-Command", command], timeout=180)
        if result.returncode != 0:
            detail = _clean_text(result.stderr) or _clean_text(result.stdout) or f"exit {result.returncode}"
            raise RuntimeError(detail)
        payload = json.loads(result.stdout or "[]")
        if isinstance(payload, dict):
            payload = [payload]
        captured_at = int(time.time())
        services: List[Dict[str, Any]] = []
        for item in payload or []:
            if not isinstance(item, dict):
                continue
            name = _clean_text(item.get("Name"))
            if not name:
                continue
            display_name = _clean_text(item.get("DisplayName"))
            description = _clean_text(item.get("Description"))
            status_code = _normalize_status_code(item.get("State"))
            services.append(
                {
                    "name": name,
                    "display_name": display_name,
                    "description": description,
                    "status_code": status_code,
                    "status": STATUS_LABELS.get(status_code, "Unknown"),
                    "captured_at": captured_at,
                }
            )
        services.sort(key=lambda entry: (entry.get("display_name") or entry["name"]).lower())
        return services

    def _query_linux_services(self) -> List[Dict[str, Any]]:
        result = self._run_subprocess(
            [
                "systemctl",
                "list-units",
                "--type=service",
                "--all",
                "--no-pager",
                "--no-legend",
                "--plain",
                "--full",
            ],
            timeout=180,
        )
        if result.returncode != 0:
            detail = _clean_text(result.stderr) or _clean_text(result.stdout) or f"exit {result.returncode}"
            raise RuntimeError(detail)
        captured_at = int(time.time())
        services: List[Dict[str, Any]] = []
        for raw_line in (result.stdout or "").splitlines():
            line = raw_line.strip()
            if not line:
                continue
            parts = line.split(None, 4)
            if len(parts) < 4:
                continue
            name = _clean_text(parts[0])
            if not name:
                continue
            active_state = parts[2] if len(parts) > 2 else ""
            sub_state = parts[3] if len(parts) > 3 else ""
            description = parts[4] if len(parts) > 4 else ""
            status_code = _linux_status_code(active_state, sub_state)
            services.append(
                {
                    "name": name,
                    "display_name": _clean_text(description),
                    "description": _clean_text(description),
                    "status_code": status_code,
                    "status": STATUS_LABELS.get(status_code, "Unknown"),
                    "captured_at": captured_at,
                }
            )
        services.sort(key=lambda entry: (entry.get("display_name") or entry["name"]).lower())
        return services

    def _collect_services(self) -> List[Dict[str, Any]]:
        if IS_WINDOWS:
            return self._query_windows_services()
        if IS_LINUX:
            return self._query_linux_services()
        raise RuntimeError(self._unsupported_reason or "Unsupported service-control platform.")

    def _run_service_action(self, service_name: str, action: str) -> None:
        if IS_WINDOWS:
            command = _windows_service_command(action, service_name)
            result = self._run_subprocess(["powershell.exe", "-NoProfile", "-Command", command], timeout=180)
        else:
            result = self._run_subprocess(["systemctl", action, service_name], timeout=180)
        if result.returncode != 0:
            detail = _clean_text(result.stderr) or _clean_text(result.stdout) or f"exit {result.returncode}"
            raise RuntimeError(detail)

    def _publish_services(self, services: List[Dict[str, Any]]) -> None:
        client = self._http_client()
        if client is None:
            raise RuntimeError("HTTP client unavailable.")
        payload = {
            "agent_id": self.ctx.agent_id,
            "hostname": _hostname(),
            "details": {
                "summary": {
                    "hostname": _hostname(),
                    "agent_id": self.ctx.agent_id,
                },
                "services": services,
            },
            "agent_build_id": self._agent_build_id(),
            "service_mode": self._service_mode,
        }
        client.post_json("/api/agent/details", payload, require_auth=True)
        with self._lock:
            self._last_refresh_at = int(time.time())
            self._last_service_count = len(services)
            self._last_error = ""

    def _collect_and_publish(self) -> None:
        services = self._collect_services()
        self._publish_services(services)

    def _request_boost(self, *, wake_now: bool = True) -> None:
        with self._lock:
            self._fast_poll_until = max(self._fast_poll_until, time.time() + BOOST_WINDOW_SECONDS)
        if wake_now:
            self._wakeup.set()

    def _record_error(self, message: str) -> None:
        with self._lock:
            self._last_error = message
        self._log(message, error=True)

    def _poll_loop(self) -> None:
        while not self._stop.is_set():
            try:
                self._collect_and_publish()
            except Exception as exc:
                self._record_error(f"Service inventory refresh failed: {exc}")
            with self._lock:
                boost_active = time.time() < self._fast_poll_until
            timeout = BOOST_REFRESH_INTERVAL_SECONDS if boost_active else SERVICE_REFRESH_INTERVAL_SECONDS
            self._wakeup.wait(timeout)
            self._wakeup.clear()

    def _action_worker(self, service_name: str, action: str, requested_by: str) -> None:
        try:
            self._log(
                "Service control request action={0} service_name={1} requested_by={2}".format(
                    action,
                    service_name,
                    requested_by or "-",
                )
            )
            self._run_service_action(service_name, action)
        except Exception as exc:
            self._record_error(
                "Service control failed action={0} service_name={1} error={2}".format(
                    action,
                    service_name,
                    exc,
                )
            )
        try:
            self._collect_and_publish()
        except Exception as exc:
            self._record_error(f"Post-action service refresh failed: {exc}")
        self._request_boost()

    def register_events(self) -> None:
        sio = self.ctx.sio

        @sio.on("service_control_action")
        async def _service_control_action(payload):
            if not isinstance(payload, dict):
                return
            target_agent = _clean_text(payload.get("agent_id"))
            if target_agent and target_agent != _clean_text(self.ctx.agent_id):
                return
            action = _normalize_action(payload.get("action"))
            service_name = _clean_text(payload.get("service_name"))
            requested_by = _clean_text(payload.get("requested_by"))
            if not action or not service_name:
                return
            worker = threading.Thread(
                target=self._action_worker,
                args=(service_name, action, requested_by),
                daemon=True,
            )
            worker.start()

    def health_report(self) -> Dict[str, Any]:
        if not self._supported:
            return {
                "status": "unsupported",
                "role_label": self.role_health_label,
                "detail": self._unsupported_reason or "Service control is unsupported on this platform.",
                "details": {
                    "running_status": "Unsupported",
                },
            }
        thread_alive = bool(self._thread and self._thread.is_alive())
        with self._lock:
            last_error = self._last_error
            last_refresh_at = self._last_refresh_at
            service_count = self._last_service_count
        details = {
            "running_status": "Running" if thread_alive else "Stopped",
            "service_count": str(service_count),
            "last_refresh_at": str(last_refresh_at or 0),
        }
        if last_error:
            details["last_error"] = last_error
        if not thread_alive:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "Service control refresh loop stopped.",
                "details": details,
            }
        if last_error:
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": last_error,
                "details": details,
            }
        if not last_refresh_at:
            return {
                "status": "pending",
                "role_label": self.role_health_label,
                "detail": "Waiting for initial service inventory snapshot.",
                "details": details,
            }
        return {
            "status": "healthy",
            "role_label": self.role_health_label,
            "detail": "Service inventory refresh loop active.",
            "details": details,
        }

    def stop_all(self) -> None:
        try:
            self._stop.set()
        except Exception:
            pass
        try:
            self._wakeup.set()
        except Exception:
            pass
