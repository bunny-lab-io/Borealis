# ======================================================
# Data\Agent\Roles\role_system_heartbeat.py
# Description: Earliest-possible SYSTEM startup status timeline reporter.
#
# API Endpoints (if applicable):
# - POST /api/agent/status (Device Authenticated) - publishes startup phase
#   and milestone timeline telemetry.
# ======================================================

"""SYSTEM startup timeline role.

This module is intentionally safe to import before normal role loading.  The
agent imports it during early process bootstrap, records milestones in memory,
and flushes them once device authentication becomes available.  RoleManager
later loads this same module and exposes the singleton controller through the
normal role-health registry.
"""

from __future__ import annotations

import json
import socket
import threading
import time
import uuid
from collections import OrderedDict
from typing import Any, Dict, Optional


ROLE_NAME = "system_heartbeat"
ROLE_CONTEXTS = ["system"]

ROLE_LABEL = "Startup Timeline"
MIN_FLUSH_INTERVAL_SECONDS = 5

MILESTONE_DEFINITIONS = OrderedDict(
    [
        ("process_start", "Agent process started"),
        ("server_config_loaded", "Server configuration loaded"),
        ("identity_loaded", "Device identity loaded"),
        ("authenticating", "Authenticating with Engine"),
        ("authenticated", "Engine authentication complete"),
        ("status_channel_online", "Status channel online"),
        ("socket_connecting", "Opening Engine socket"),
        ("socket_connected", "Engine socket connected"),
        ("roles_loading", "Loading agent roles"),
        ("roles_ready", "Agent roles ready"),
        ("helper_broker_ready", "Current-user broker ready"),
        ("wireguard_starting", "WireGuard tunnel starting"),
        ("wireguard_online", "WireGuard tunnel online"),
        ("inventory_ready", "Inventory telemetry ready"),
        ("steady_state_online", "Agent steady state online"),
    ]
)

VALID_STATES = {"pending", "active", "complete", "failed", "skipped"}
COMPLETION_PREDECESSORS = {
    "authenticated": ("authenticating",),
    "status_channel_online": ("authenticated", "authenticating"),
    "socket_connected": ("socket_connecting", "authenticated", "authenticating"),
    "roles_ready": ("roles_loading",),
    "wireguard_online": ("wireguard_starting",),
    "steady_state_online": ("inventory_ready", "status_channel_online"),
}


def _now() -> int:
    return int(time.time())


def _clean_text(value: Any) -> str:
    try:
        return str(value or "").strip()
    except Exception:
        return ""


def _normalize_state(value: Any) -> str:
    text = _clean_text(value).lower().replace("-", "_")
    aliases = {
        "ok": "complete",
        "done": "complete",
        "healthy": "complete",
        "running": "active",
        "recovering": "active",
        "started": "active",
        "error": "failed",
        "unhealthy": "failed",
    }
    normalized = aliases.get(text, text)
    return normalized if normalized in VALID_STATES else "active"


class HeartbeatController:
    def __init__(self) -> None:
        self.boot_id = str(uuid.uuid4())
        self.role_health_label = ROLE_LABEL
        self._lock = threading.RLock()
        self._milestones: "OrderedDict[str, Dict[str, Any]]" = OrderedDict()
        self._phase = "process_start"
        self._status = "recovering"
        self._message = "Agent process started."
        self._last_error: Optional[Dict[str, Any]] = None
        self._last_flush_success_at = 0
        self._last_flush_attempt_at = 0
        self._last_payload_signature = ""
        self._dirty = True
        self._hooks: Dict[str, Any] = {}
        self._ctx = None
        self._service_mode = "system"
        for key, label in MILESTONE_DEFINITIONS.items():
            self._milestones[key] = {
                "key": key,
                "label": label,
                "state": "pending",
                "detail": "",
                "started_at": 0,
                "updated_at": 0,
                "completed_at": 0,
            }

    def configure(self, *, ctx: Any = None, hooks: Optional[Dict[str, Any]] = None, service_mode: str = "system") -> None:
        with self._lock:
            if ctx is not None:
                self._ctx = ctx
            if hooks:
                self._hooks.update(dict(hooks))
            mode = _clean_text(service_mode).lower()
            if mode:
                self._service_mode = mode

    def _log(self, message: str, *, error: bool = False) -> None:
        hook = self._hooks.get("log_agent")
        if not callable(hook):
            return
        try:
            hook(message, fname="agent.error.log" if error else "agent.log")
        except Exception:
            pass

    def _ensure_milestone(self, key: str) -> Dict[str, Any]:
        normalized = _clean_text(key)
        if not normalized:
            normalized = "unknown"
        if normalized not in self._milestones:
            self._milestones[normalized] = {
                "key": normalized,
                "label": normalized.replace("_", " ").title(),
                "state": "pending",
                "detail": "",
                "started_at": 0,
                "updated_at": 0,
                "completed_at": 0,
            }
        return self._milestones[normalized]

    def _complete_predecessors_locked(self, key: str, now: int) -> None:
        current_label = str(self._ensure_milestone(key).get("label") or key).strip()
        for predecessor_key in COMPLETION_PREDECESSORS.get(key, ()):
            predecessor = self._ensure_milestone(predecessor_key)
            previous_state = predecessor.get("state")
            if previous_state == "complete":
                continue
            if not predecessor.get("started_at"):
                predecessor["started_at"] = now
            predecessor["state"] = "complete"
            if not predecessor.get("detail"):
                predecessor["detail"] = f"Completed before {current_label.lower()}."
            elif previous_state == "failed":
                predecessor["detail"] = f"Recovered before {current_label.lower()}."
            predecessor["updated_at"] = now
            predecessor["completed_at"] = now

    def _recalculate_status_locked(self) -> None:
        has_failure = any(item.get("state") == "failed" for item in self._milestones.values())
        if has_failure:
            self._status = "unhealthy"
            return
        if isinstance(self._last_error, dict):
            failed_key = str(self._last_error.get("milestone") or "").strip()
            if failed_key and self._ensure_milestone(failed_key).get("state") != "failed":
                self._last_error = None
        steady_state = self._ensure_milestone("steady_state_online").get("state")
        self._status = "healthy" if steady_state == "complete" else "recovering"

    def record(self, key: str, state: str = "active", detail: str = "", *, message: str = "") -> None:
        normalized_key = _clean_text(key) or "unknown"
        normalized_state = _normalize_state(state)
        now = _now()
        with self._lock:
            item = self._ensure_milestone(normalized_key)
            previous_state = item.get("state")
            if normalized_state == "active" and previous_state == "complete":
                return
            if not item.get("started_at"):
                item["started_at"] = now
            item["state"] = normalized_state
            item["detail"] = _clean_text(detail)
            item["updated_at"] = now
            if normalized_state == "complete":
                self._complete_predecessors_locked(normalized_key, now)
                item["completed_at"] = int(item.get("completed_at") or now)
            elif normalized_state in {"active", "failed"}:
                item["completed_at"] = 0
            self._phase = normalized_key
            self._message = _clean_text(message) or _clean_text(detail) or str(item.get("label") or normalized_key)
            if normalized_state == "failed":
                self._status = "unhealthy"
                self._last_error = {
                    "milestone": normalized_key,
                    "detail": _clean_text(detail),
                    "at": now,
                }
            self._recalculate_status_locked()
            self._dirty = True

    def complete(self, key: str, detail: str = "", *, message: str = "") -> None:
        self.record(key, "complete", detail, message=message)

    def fail(self, key: str, detail: str = "", *, message: str = "") -> None:
        self.record(key, "failed", detail, message=message)

    def skip(self, key: str, detail: str = "", *, message: str = "") -> None:
        self.record(key, "skipped", detail, message=message)

    def _milestone_list_locked(self) -> list[Dict[str, Any]]:
        return [dict(item) for item in self._milestones.values()]

    def payload(self) -> Dict[str, Any]:
        with self._lock:
            return {
                "hostname": socket.gethostname(),
                "service_mode": self._service_mode,
                "boot_id": self.boot_id,
                "phase": self._phase,
                "status": self._status,
                "message": self._message,
                "milestones": self._milestone_list_locked(),
                "last_error": dict(self._last_error) if isinstance(self._last_error, dict) else None,
            }

    def _http_client(self) -> Any:
        factory = self._hooks.get("http_client")
        if callable(factory):
            return factory()
        return None

    def flush_now(self, *, reason: str = "") -> bool:
        payload = self.payload()
        try:
            payload_signature = json.dumps(payload, ensure_ascii=True, sort_keys=True, separators=(",", ":"))
        except Exception:
            payload_signature = ""
        with self._lock:
            now = _now()
            recent_success = now - int(self._last_flush_success_at or 0) < MIN_FLUSH_INTERVAL_SECONDS
            if recent_success and self._status != "unhealthy":
                return True
            if recent_success and payload_signature and payload_signature == self._last_payload_signature:
                return True
            self._last_flush_attempt_at = now
        client = self._http_client()
        if client is None:
            return False
        try:
            client.post_json("/api/agent/status", payload, require_auth=True)
            with self._lock:
                self._last_flush_success_at = _now()
                self._last_payload_signature = payload_signature
                self._dirty = False
                channel = self._ensure_milestone("status_channel_online")
                if channel.get("state") != "complete":
                    now = _now()
                    if not channel.get("started_at"):
                        channel["started_at"] = now
                    channel["state"] = "complete"
                    channel["detail"] = "Engine accepted startup status."
                    channel["updated_at"] = now
                    channel["completed_at"] = now
            return True
        except Exception as exc:
            self._log(f"Agent startup status flush failed reason={reason or '-'} error={exc}", error=True)
            return False

    def health_report(self) -> Dict[str, Any]:
        with self._lock:
            details = {
                "boot_id": self.boot_id,
                "phase": self._phase,
                "message": self._message,
                "milestones_json": json.dumps(self._milestone_list_locked(), ensure_ascii=True, sort_keys=True),
                "last_flush_success_at": str(self._last_flush_success_at or 0),
                "last_flush_attempt_at": str(self._last_flush_attempt_at or 0),
            }
            if self._last_error:
                details["last_error_json"] = json.dumps(self._last_error, ensure_ascii=True, sort_keys=True)
            return {
                "role_name": ROLE_NAME,
                "role_label": self.role_health_label,
                "context": "system",
                "status": self._status,
                "detail": self._message,
                "details": details,
            }


_CONTROLLER = HeartbeatController()


def controller() -> HeartbeatController:
    return _CONTROLLER


def initialize_early(*, hooks: Optional[Dict[str, Any]] = None, service_mode: str = "system") -> HeartbeatController:
    _CONTROLLER.configure(hooks=hooks or {}, service_mode=service_mode)
    _CONTROLLER.complete("process_start", "Agent process entered Python runtime.")
    return _CONTROLLER


def record_milestone(key: str, state: str = "active", detail: str = "", *, message: str = "") -> None:
    _CONTROLLER.record(key, state, detail, message=message)


def complete_milestone(key: str, detail: str = "", *, message: str = "") -> None:
    _CONTROLLER.complete(key, detail, message=message)


def fail_milestone(key: str, detail: str = "", *, message: str = "") -> None:
    _CONTROLLER.fail(key, detail, message=message)


def flush_status(*, reason: str = "") -> bool:
    return _CONTROLLER.flush_now(reason=reason)


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = ROLE_LABEL
        hooks = getattr(ctx, "hooks", {}) if ctx is not None else {}
        service_mode = getattr(ctx, "service_mode", "") or "system"
        _CONTROLLER.configure(ctx=ctx, hooks=hooks if isinstance(hooks, dict) else {}, service_mode=service_mode)

    def health_report(self) -> Dict[str, Any]:
        return _CONTROLLER.health_report()

    def record(self, key: str, state: str = "active", detail: str = "") -> None:
        _CONTROLLER.record(key, state, detail)

    def flush_now(self, reason: str = "") -> bool:
        return _CONTROLLER.flush_now(reason=reason)

    def stop_all(self) -> None:
        return None
