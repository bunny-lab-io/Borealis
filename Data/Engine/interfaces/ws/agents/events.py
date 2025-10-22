"""Agent WebSocket event handlers for the Borealis Engine."""

from __future__ import annotations

import logging
import time
from typing import Any, Dict, Iterable, Optional

from flask import request

from Data.Engine.services.container import EngineServiceContainer

try:  # pragma: no cover - optional dependency guard
    from flask_socketio import emit, join_room
except Exception:  # pragma: no cover - optional dependency guard
    emit = None  # type: ignore[assignment]
    join_room = None  # type: ignore[assignment]

_AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


def register(socketio: Any, services: EngineServiceContainer) -> None:
    if socketio is None:  # pragma: no cover - guard
        return

    handlers = _AgentEventHandlers(socketio, services)
    socketio.on_event("connect", handlers.on_connect)
    socketio.on_event("disconnect", handlers.on_disconnect)
    socketio.on_event("agent_screenshot_task", handlers.on_agent_screenshot_task)
    socketio.on_event("connect_agent", handlers.on_connect_agent)
    socketio.on_event("agent_heartbeat", handlers.on_agent_heartbeat)
    socketio.on_event("collector_status", handlers.on_collector_status)
    socketio.on_event("request_config", handlers.on_request_config)
    socketio.on_event("screenshot", handlers.on_screenshot)
    socketio.on_event("macro_status", handlers.on_macro_status)
    socketio.on_event("list_agent_windows", handlers.on_list_agent_windows)
    socketio.on_event("agent_window_list", handlers.on_agent_window_list)
    socketio.on_event("ansible_playbook_cancel", handlers.on_ansible_playbook_cancel)
    socketio.on_event("ansible_playbook_run", handlers.on_ansible_playbook_run)


class _AgentEventHandlers:
    def __init__(self, socketio: Any, services: EngineServiceContainer) -> None:
        self._socketio = socketio
        self._services = services
        self._realtime = services.agent_realtime
        self._log = logging.getLogger("borealis.engine.ws.agents")

    # ------------------------------------------------------------------
    # Connection lifecycle
    # ------------------------------------------------------------------
    def on_connect(self) -> None:
        sid = getattr(request, "sid", "<unknown>")
        remote_addr = getattr(request, "remote_addr", None)
        transport = None
        try:
            transport = request.args.get("transport")  # type: ignore[attr-defined]
        except Exception:
            transport = None
        query = self._render_query()
        headers = _summarize_socket_headers(getattr(request, "headers", {}))
        scope = _canonical_scope(getattr(request.headers, "get", lambda *_: None)(_AGENT_CONTEXT_HEADER))
        self._log.info(
            "socket-connect sid=%s ip=%s transport=%r query=%s headers=%s scope=%s",
            sid,
            remote_addr,
            transport,
            query,
            headers,
            scope or "<none>",
        )

    def on_disconnect(self) -> None:
        sid = getattr(request, "sid", "<unknown>")
        remote_addr = getattr(request, "remote_addr", None)
        self._log.info("socket-disconnect sid=%s ip=%s", sid, remote_addr)

    # ------------------------------------------------------------------
    # Agent coordination
    # ------------------------------------------------------------------
    def on_agent_screenshot_task(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        agent_id = payload.get("agent_id")
        node_id = payload.get("node_id")
        image = payload.get("image_base64", "")

        if not agent_id or not node_id:
            self._log.warning("screenshot-task missing identifiers: %s", payload)
            return

        if image:
            self._realtime.store_task_screenshot(agent_id, node_id, image)

        try:
            self._socketio.emit("agent_screenshot_task", payload)
        except Exception as exc:  # pragma: no cover - network guard
            self._log.warning("socket emit failed for agent_screenshot_task: %s", exc)

    def on_connect_agent(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        agent_id = payload.get("agent_id")
        if not agent_id:
            return

        service_mode = payload.get("service_mode")
        record = self._realtime.register_connection(agent_id, service_mode)

        if join_room is not None:  # pragma: no branch - optional dependency guard
            try:
                join_room(agent_id)
            except Exception as exc:  # pragma: no cover - dependency guard
                self._log.debug("join_room failed for %s: %s", agent_id, exc)

        self._log.info(
            "agent-connected agent_id=%s mode=%s status=%s",
            agent_id,
            record.service_mode,
            record.status,
        )

    def on_agent_heartbeat(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        record = self._realtime.heartbeat(payload)
        if record:
            self._log.debug(
                "agent-heartbeat agent_id=%s host=%s mode=%s", record.agent_id, record.hostname, record.service_mode
            )

    def on_collector_status(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        self._realtime.collector_status(payload)

    def on_request_config(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        agent_id = payload.get("agent_id")
        if not agent_id:
            return
        config = self._realtime.get_agent_config(agent_id)
        if config and emit is not None:
            try:
                emit("agent_config", {**config, "agent_id": agent_id})
            except Exception as exc:  # pragma: no cover - dependency guard
                self._log.debug("emit(agent_config) failed for %s: %s", agent_id, exc)

    # ------------------------------------------------------------------
    # Media + relay events
    # ------------------------------------------------------------------
    def on_screenshot(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        agent_id = payload.get("agent_id")
        image = payload.get("image_base64")
        if agent_id and image:
            self._realtime.store_agent_screenshot(agent_id, image)
            try:
                self._socketio.emit("new_screenshot", {"agent_id": agent_id, "image_base64": image})
            except Exception as exc:  # pragma: no cover - dependency guard
                self._log.warning("socket emit failed for new_screenshot: %s", exc)

    def on_macro_status(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        agent_id = payload.get("agent_id")
        node_id = payload.get("node_id")
        success = payload.get("success")
        message = payload.get("message")
        self._log.info(
            "macro-status agent=%s node=%s success=%s message=%s",
            agent_id,
            node_id,
            success,
            message,
        )
        try:
            self._socketio.emit("macro_status", payload)
        except Exception as exc:  # pragma: no cover - dependency guard
            self._log.warning("socket emit failed for macro_status: %s", exc)

    def on_list_agent_windows(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        try:
            self._socketio.emit("list_agent_windows", payload)
        except Exception as exc:  # pragma: no cover - dependency guard
            self._log.warning("socket emit failed for list_agent_windows: %s", exc)

    def on_agent_window_list(self, data: Optional[Dict[str, Any]]) -> None:
        payload = data or {}
        try:
            self._socketio.emit("agent_window_list", payload)
        except Exception as exc:  # pragma: no cover - dependency guard
            self._log.warning("socket emit failed for agent_window_list: %s", exc)

    def on_ansible_playbook_cancel(self, data: Optional[Dict[str, Any]]) -> None:
        try:
            self._socketio.emit("ansible_playbook_cancel", data or {})
        except Exception as exc:  # pragma: no cover - dependency guard
            self._log.warning("socket emit failed for ansible_playbook_cancel: %s", exc)

    def on_ansible_playbook_run(self, data: Optional[Dict[str, Any]]) -> None:
        try:
            self._socketio.emit("ansible_playbook_run", data or {})
        except Exception as exc:  # pragma: no cover - dependency guard
            self._log.warning("socket emit failed for ansible_playbook_run: %s", exc)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------
    def _render_query(self) -> str:
        try:
            pairs = [f"{k}={v}" for k, v in request.args.items()]  # type: ignore[attr-defined]
        except Exception:
            return "<unavailable>"
        return "&".join(pairs) if pairs else "<none>"


def _canonical_scope(raw: Optional[str]) -> Optional[str]:
    if not raw:
        return None
    value = "".join(ch for ch in str(raw) if ch.isalnum() or ch in ("_", "-"))
    if not value:
        return None
    return value.upper()


def _mask_value(value: str, *, prefix: int = 4, suffix: int = 4) -> str:
    try:
        if not value:
            return ""
        stripped = value.strip()
        if len(stripped) <= prefix + suffix:
            return "*" * len(stripped)
        return f"{stripped[:prefix]}***{stripped[-suffix:]}"
    except Exception:
        return "***"


def _summarize_socket_headers(headers: Any) -> str:
    try:
        items: Iterable[tuple[str, Any]]
        if isinstance(headers, dict):
            items = headers.items()
        else:
            items = getattr(headers, "items", lambda: [])()
    except Exception:
        items = []

    rendered = []
    for key, value in items:
        lowered = str(key).lower()
        display = value
        if lowered == "authorization":
            token = str(value or "")
            if token.lower().startswith("bearer "):
                display = f"Bearer {_mask_value(token.split(' ', 1)[1])}"
            else:
                display = _mask_value(token)
        elif lowered == "cookie":
            display = "<redacted>"
        rendered.append(f"{key}={display}")
    return ", ".join(rendered) if rendered else "<no-headers>"


__all__ = ["register"]
