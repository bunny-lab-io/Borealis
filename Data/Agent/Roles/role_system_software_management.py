from __future__ import annotations

import os
import platform
import shutil
import socket
import threading
import time
from typing import Any, Dict, Optional, Tuple

try:
    from Roles.system_software_management import build_software_inventory_snapshot
except ModuleNotFoundError as exc:  # pragma: no cover - package import fallback
    if not str(getattr(exc, "name", "") or "").startswith("Roles"):
        raise
    from Data.Agent.Roles.system_software_management import build_software_inventory_snapshot


ROLE_NAME = "software_management"
ROLE_CONTEXTS = ["system"]

IS_WINDOWS = os.name == "nt"
IS_LINUX = platform.system().lower() == "linux"
SOFTWARE_REFRESH_INTERVAL_SECONDS = 300
BOOST_REFRESH_INTERVAL_SECONDS = 5
BOOST_WINDOW_SECONDS = 45


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _hostname() -> str:
    try:
        return str(socket.gethostname() or "").strip()
    except Exception:
        return ""


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = "Software Management"
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
        self._last_software_count = 0
        self._last_icon_payload_count = 0
        self._fast_poll_until = 0.0
        self._last_software_icon_signature = ""
        self._last_software_icon_hash_by_key: Dict[str, str] = {}
        self._supported, self._unsupported_reason = self._detect_support()
        if self._supported:
            self._thread = threading.Thread(target=self._poll_loop, daemon=True)
            self._thread.start()
        else:
            self._log(self._unsupported_reason or "Software management role is unsupported on this platform.")

    def _detect_support(self) -> Tuple[bool, str]:
        if IS_WINDOWS:
            return True, ""
        if IS_LINUX and (shutil.which("dpkg-query") or shutil.which("rpm")):
            return True, ""
        if IS_LINUX:
            return False, "No supported package inventory tools are available on this Linux agent."
        return False, f"Unsupported software-management platform '{platform.system()}'."

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="software_management.log")
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

    def _record_error(self, message: str) -> None:
        with self._lock:
            self._last_error = message
        self._log(message, error=True)

    def _publish_snapshot(self, snapshot: Dict[str, Any]) -> None:
        client = self._http_client()
        if client is None:
            raise RuntimeError("HTTP client unavailable.")
        software_rows = snapshot.get("software") if isinstance(snapshot.get("software"), list) else []
        icon_payloads = (
            snapshot.get("software_icon_payloads")
            if isinstance(snapshot.get("software_icon_payloads"), list)
            else []
        )
        payload = {
            "agent_id": self.ctx.agent_id,
            "hostname": _hostname(),
            "details": {
                "summary": {
                    "hostname": _hostname(),
                    "agent_id": self.ctx.agent_id,
                },
                "software": software_rows,
            },
            "agent_build_id": self._agent_build_id(),
            "service_mode": self._service_mode,
        }
        if icon_payloads:
            payload["details"]["software_icon_payloads"] = icon_payloads
        client.post_json("/api/agent/details", payload, require_auth=True)
        with self._lock:
            self._last_refresh_at = int(time.time())
            self._last_software_count = len(software_rows)
            self._last_icon_payload_count = len(icon_payloads)
            self._last_error = ""

    def _collect_and_publish(self) -> None:
        snapshot = build_software_inventory_snapshot(
            previous_icon_hash_by_key=self._last_software_icon_hash_by_key,
            previous_signature=self._last_software_icon_signature,
        )
        self._last_software_icon_signature = str(snapshot.get("software_icon_signature") or "")
        hash_by_key = snapshot.get("software_icon_hash_by_key")
        self._last_software_icon_hash_by_key = hash_by_key if isinstance(hash_by_key, dict) else {}
        self._publish_snapshot(snapshot)

    def _poll_loop(self) -> None:
        while not self._stop.is_set():
            try:
                self._collect_and_publish()
            except Exception as exc:
                self._record_error(f"Software inventory refresh failed: {exc}")
            with self._lock:
                boost_active = time.time() < self._fast_poll_until
            timeout = BOOST_REFRESH_INTERVAL_SECONDS if boost_active else SOFTWARE_REFRESH_INTERVAL_SECONDS
            self._wakeup.wait(timeout)
            self._wakeup.clear()

    def request_refresh(self, *, reason: str = "") -> None:
        with self._lock:
            self._fast_poll_until = max(self._fast_poll_until, time.time() + BOOST_WINDOW_SECONDS)
        self._wakeup.set()
        if reason:
            self._log(f"Software inventory refresh requested (reason={reason}).")

    def health_report(self) -> Dict[str, Any]:
        if not self._supported:
            return {
                "status": "unsupported",
                "role_label": self.role_health_label,
                "detail": self._unsupported_reason or "Software management is unsupported on this platform.",
                "details": {
                    "running_status": "Unsupported",
                },
            }
        thread_alive = bool(self._thread and self._thread.is_alive())
        with self._lock:
            last_error = self._last_error
            last_refresh_at = self._last_refresh_at
            software_count = self._last_software_count
            icon_payload_count = self._last_icon_payload_count
        details = {
            "running_status": "Running" if thread_alive else "Stopped",
            "software_count": str(software_count),
            "icon_payload_count": str(icon_payload_count),
            "last_refresh_at": str(last_refresh_at or 0),
        }
        if last_error:
            details["last_error"] = last_error
        if not thread_alive:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "Software inventory refresh loop stopped.",
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
                "detail": "Waiting for initial software inventory snapshot.",
                "details": details,
            }
        return {
            "status": "healthy",
            "role_label": self.role_health_label,
            "detail": "Software inventory refresh loop active.",
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
