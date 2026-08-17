"""Agent Socket.IO registry helpers shared by api-backend and site-workers."""

from __future__ import annotations

import re
import threading
from typing import Any, Dict, Optional


_AGENT_ID_HOST_PATTERN = re.compile(
    r"^(?P<hostname>.+)_(?P<guid>[0-9A-F-]+)_(?P<context>[A-Z0-9_-]+)$",
    re.IGNORECASE,
)


def normalize_host_key(value: Any) -> str:
    return str(value or "").strip().lower()


def normalize_service_mode(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized in {"system", "sys"}:
        return "system"
    if normalized in {"currentuser", "current_user", "interactive", "user"}:
        return "currentuser"
    return ""


def normalize_helper_contexts(value: Any) -> tuple[str, ...]:
    contexts = []
    candidates = list(value) if isinstance(value, (list, tuple, set)) else [value]
    seen = set()
    for candidate in candidates:
        normalized = normalize_service_mode(candidate)
        if not normalized or normalized in seen:
            continue
        seen.add(normalized)
        contexts.append(normalized)
    return tuple(contexts)


def infer_hostname_from_agent_id(agent_id: Any) -> str:
    raw = str(agent_id or "").strip()
    if not raw:
        return ""
    match = _AGENT_ID_HOST_PATTERN.match(raw)
    if match:
        return str(match.group("hostname") or "").strip()
    parts = raw.rsplit("_", 2)
    if len(parts) == 3:
        return str(parts[0] or "").strip()
    return ""


def infer_guid_from_agent_id(agent_id: Any) -> str:
    raw = str(agent_id or "").strip()
    if not raw:
        return ""
    match = _AGENT_ID_HOST_PATTERN.match(raw)
    if match:
        return str(match.group("guid") or "").strip().upper()
    parts = raw.rsplit("_", 2)
    if len(parts) == 3:
        return str(parts[1] or "").strip().upper()
    return ""


class AgentSocketRegistry:
    def __init__(self, socketio: Any, logger: Any) -> None:
        self.socketio = socketio
        self.logger = logger
        self._lock = threading.RLock()
        self._sid_by_agent: Dict[str, str] = {}
        self._agent_by_sid: Dict[str, str] = {}
        self._sid_by_host_mode: Dict[tuple[str, str], str] = {}
        self._host_mode_by_sid: Dict[str, tuple[str, str]] = {}
        self._meta_by_sid: Dict[str, Dict[str, Any]] = {}

    def register(
        self,
        agent_id: str,
        sid: str,
        *,
        service_mode: str = "",
        hostname: str = "",
        helper_contexts: Any = None,
        guid: str = "",
    ) -> None:
        if not agent_id or not sid:
            return
        with self._lock:
            previous = self._sid_by_agent.get(agent_id)
            if previous and previous != sid:
                self._agent_by_sid.pop(previous, None)
                route = self._host_mode_by_sid.pop(previous, None)
                if route and self._sid_by_host_mode.get(route) == previous:
                    self._sid_by_host_mode.pop(route, None)
            self._sid_by_agent[agent_id] = sid
            self._agent_by_sid[sid] = agent_id
            host_key = normalize_host_key(hostname or infer_hostname_from_agent_id(agent_id))
            mode_key = normalize_service_mode(service_mode)
            previous_route = self._host_mode_by_sid.pop(sid, None)
            if previous_route and self._sid_by_host_mode.get(previous_route) == sid:
                self._sid_by_host_mode.pop(previous_route, None)
            if host_key and mode_key:
                route = (host_key, mode_key)
                prior_sid = self._sid_by_host_mode.get(route)
                if prior_sid and prior_sid != sid:
                    self._host_mode_by_sid.pop(prior_sid, None)
                self._sid_by_host_mode[route] = sid
                self._host_mode_by_sid[sid] = route
            self._meta_by_sid[sid] = {
                "guid": str(guid or infer_guid_from_agent_id(agent_id)).strip().upper(),
                "hostname": host_key,
                "service_mode": mode_key,
                "helper_contexts": normalize_helper_contexts(helper_contexts),
            }

    def snapshot(self) -> Dict[str, Dict[str, Any]]:
        with self._lock:
            pairs = list(self._sid_by_agent.items())
            meta_by_sid = dict(self._meta_by_sid)
        snapshot: Dict[str, Dict[str, Any]] = {}
        for agent_id, sid in pairs:
            meta = meta_by_sid.get(sid, {})
            snapshot[str(agent_id)] = {
                "agent_id": str(agent_id),
                "sid": str(sid),
                "guid": str(meta.get("guid") or ""),
                "hostname": str(meta.get("hostname") or ""),
                "service_mode": str(meta.get("service_mode") or ""),
                "helper_contexts": list(normalize_helper_contexts(meta.get("helper_contexts"))),
            }
        return snapshot

    def unregister(self, sid: str) -> Optional[str]:
        with self._lock:
            agent_id = self._agent_by_sid.pop(sid, None)
            if agent_id and self._sid_by_agent.get(agent_id) == sid:
                self._sid_by_agent.pop(agent_id, None)
            route = self._host_mode_by_sid.pop(sid, None)
            if route and self._sid_by_host_mode.get(route) == sid:
                self._sid_by_host_mode.pop(route, None)
            self._meta_by_sid.pop(sid, None)
        return agent_id

    def _resolve_sid_for_host_mode(self, hostname: str, service_mode: str) -> str:
        host_key = normalize_host_key(hostname)
        mode_key = normalize_service_mode(service_mode)
        if not host_key or not mode_key:
            return ""
        with self._lock:
            direct_sid = self._sid_by_host_mode.get((host_key, mode_key))
            if direct_sid:
                return direct_sid
            if mode_key == "currentuser":
                system_sid = self._sid_by_host_mode.get((host_key, "system"))
                system_meta = self._meta_by_sid.get(system_sid or "") if system_sid else None
                helper_contexts = normalize_helper_contexts((system_meta or {}).get("helper_contexts"))
                if system_sid and "currentuser" in helper_contexts:
                    return system_sid
        return ""

    def is_registered(self, agent_id: str) -> bool:
        with self._lock:
            return bool(self._sid_by_agent.get(agent_id))

    def is_host_mode_registered(self, hostname: str, service_mode: str) -> bool:
        return bool(self._resolve_sid_for_host_mode(hostname, service_mode))

    def get_agent_id_for_host_mode(self, hostname: str, service_mode: str) -> str:
        sid = self._resolve_sid_for_host_mode(hostname, service_mode)
        if not sid:
            return ""
        with self._lock:
            return str(self._agent_by_sid.get(sid) or "")

    def emit(self, agent_id: str, event: str, payload: Any) -> bool:
        with self._lock:
            sid = self._sid_by_agent.get(agent_id)
        if not sid:
            return False
        try:
            self.socketio.emit(event, payload, to=sid)
            return True
        except Exception:
            self.logger.debug("Failed to emit %s to agent_id=%s", event, agent_id, exc_info=True)
            return False

    def call(self, agent_id: str, event: str, payload: Any, *, timeout: float = 30.0) -> Any:
        with self._lock:
            sid = self._sid_by_agent.get(agent_id)
        if not sid or not hasattr(self.socketio, "call"):
            return None
        try:
            return self.socketio.call(event, payload, to=sid, timeout=timeout)
        except Exception:
            self.logger.debug("Failed to call %s for agent_id=%s", event, agent_id, exc_info=True)
            return None

    def emit_to_host(self, hostname: str, service_mode: str, event: str, payload: Any) -> bool:
        sid = self._resolve_sid_for_host_mode(hostname, service_mode)
        if not sid:
            return False
        try:
            self.socketio.emit(event, payload, to=sid)
            return True
        except Exception:
            self.logger.debug(
                "Failed to emit %s to hostname=%s service_mode=%s",
                event,
                hostname,
                service_mode,
                exc_info=True,
            )
            return False

    def call_to_host(self, hostname: str, service_mode: str, event: str, payload: Any, *, timeout: float = 30.0) -> Any:
        sid = self._resolve_sid_for_host_mode(hostname, service_mode)
        if not sid or not hasattr(self.socketio, "call"):
            return None
        try:
            return self.socketio.call(event, payload, to=sid, timeout=timeout)
        except Exception:
            self.logger.debug(
                "Failed to call %s for hostname=%s service_mode=%s",
                event,
                hostname,
                service_mode,
                exc_info=True,
            )
            return None


__all__ = [
    "AgentSocketRegistry",
    "infer_guid_from_agent_id",
    "infer_hostname_from_agent_id",
    "normalize_helper_contexts",
    "normalize_host_key",
    "normalize_service_mode",
]
