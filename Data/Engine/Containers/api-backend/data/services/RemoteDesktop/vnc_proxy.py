# ======================================================
# Data\Engine\services\RemoteDesktop\vnc_proxy.py
# Description: Guacamole tunnel proxy for Borealis VNC sessions.
#
# API Endpoints (if applicable): None
# ======================================================

"""Guacamole WebSocket proxy that bridges browser sessions to local guacd."""
from __future__ import annotations

import asyncio
import logging
import threading
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, Optional, Tuple
from urllib.parse import parse_qs, urlsplit

import websockets

from .guacamole_proxy import (
    DEFAULT_GUACD_HOST,
    DEFAULT_GUACD_PORT,
    GUACAMOLE_WS_PATH,
    GuacamoleSessionRegistry,
    proxy_guacamole_vnc_session,
)

GUACAMOLE_WEBSOCKET_SUBPROTOCOL = "guacamole"
_MAX_MESSAGE_SIZE = 100_000_000


@dataclass
class ActiveVncConnection:
    connection_id: str
    websocket: Any
    session_id: str
    participant_id: str
    agent_id: str
    operator_id: str
    role: str
    viewer: str = "guacamole"


class VncProxyServer:
    def __init__(
        self,
        *,
        host: str,
        port: int,
        guacamole_registry: GuacamoleSessionRegistry,
        logger: logging.Logger,
        emit_agent_event: Optional[Callable[[str, str, Any], bool]] = None,
        resolver: Optional[Callable[[str], Optional[Tuple[str, int]]]] = None,
        guacamole_path: str = GUACAMOLE_WS_PATH,
        guacd_host: str = DEFAULT_GUACD_HOST,
        guacd_port: int = DEFAULT_GUACD_PORT,
        ssl_context: Optional[Any] = None,
    ) -> None:
        self.host = host
        self.port = port
        self.guacamole_registry = guacamole_registry
        self.logger = logger
        self._emit_agent_event = emit_agent_event
        self._resolver = resolver
        self.guacamole_path = str(guacamole_path or GUACAMOLE_WS_PATH)
        self.guacd_host = str(guacd_host or DEFAULT_GUACD_HOST)
        self.guacd_port = int(guacd_port or DEFAULT_GUACD_PORT)
        self.ssl_context = ssl_context
        self._thread: Optional[threading.Thread] = None
        self._loop: Optional[asyncio.AbstractEventLoop] = None
        self._ready = threading.Event()
        self._failed = threading.Event()
        self._active_connections_lock = threading.Lock()
        self._active_connections: Dict[str, ActiveVncConnection] = {}

    def ensure_started(self, timeout: float = 3.0) -> bool:
        if self._thread and self._thread.is_alive():
            return not self._failed.is_set()
        self._failed.clear()
        self._ready.clear()
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()
        self._ready.wait(timeout)
        return not self._failed.is_set()

    def _run(self) -> None:
        try:
            asyncio.run(self._serve())
        except Exception as exc:
            self._failed.set()
            self.logger.error("Guacamole VNC proxy server failed: %s", exc)
            self._ready.set()

    async def _serve(self) -> None:
        self.logger.info("Starting Guacamole VNC proxy on %s:%s", self.host, self.port)
        self._loop = asyncio.get_running_loop()
        try:
            server = await websockets.serve(
                self._handle_client,
                self.host,
                self.port,
                ssl=self.ssl_context,
                max_size=_MAX_MESSAGE_SIZE,
                ping_interval=20,
                ping_timeout=20,
                subprotocols=[GUACAMOLE_WEBSOCKET_SUBPROTOCOL],
            )
        except Exception:
            self._failed.set()
            self._ready.set()
            raise
        self._ready.set()
        await server.wait_closed()

    async def _handle_client(self, websocket, path: Optional[str] = None) -> None:
        raw_path = path or getattr(websocket, "path", "") or ""
        if not raw_path:
            request = getattr(websocket, "request", None)
            raw_path = getattr(request, "path", "") or raw_path
        parsed = urlsplit(raw_path)
        normalized_path = parsed.path or ""
        if normalized_path and not normalized_path.startswith("/"):
            normalized_path = f"/{normalized_path}"
        if normalized_path != self.guacamole_path:
            self.logger.warning("Guacamole VNC proxy rejected request with invalid path: %s", raw_path)
            await websocket.close(code=1008, reason="guacamole_required")
            return
        await self._handle_guacamole_client(websocket, parsed, raw_path)

    async def _handle_guacamole_client(self, websocket: Any, parsed: Any, raw_path: str) -> None:
        query = parse_qs(parsed.query or "")
        token = (query.get("token") or [""])[0]
        session = self.guacamole_registry.consume(token)
        host = session.host if session else ""
        port = session.port if session else 0
        agent_id = session.agent_id if session else ""
        if not host or not port or not agent_id:
            token_hint = token[:8] if token else "-"
            self.logger.warning("Guacamole proxy rejected session (token=%s path=%s)", token_hint, raw_path)
            await websocket.close(code=1008, reason="invalid_session")
            return

        logger = self.logger.getChild("guacamole")
        connection_id = ""
        close_reason = "session_end"
        logger.info(
            "Guacamole VNC session start agent_id=%s session_id=%s participant_id=%s role=%s",
            agent_id,
            session.session_id or "-",
            session.participant_id or "-",
            session.role or "-",
        )
        try:
            connection_id = self._register_active_connection(session, websocket)
            try:
                await proxy_guacamole_vnc_session(
                    websocket=websocket,
                    session=session,
                    logger=logger,
                    guacd_host=self.guacd_host,
                    guacd_port=self.guacd_port,
                )
            except websockets.exceptions.ConnectionClosed as exc:
                close_reason = str(getattr(exc, "reason", "") or "client_closed").strip()[:120]
            except Exception as exc:
                logger.warning("Guacamole VNC bridge failed: %s", exc)
                close_reason = "guacamole_unavailable"
                await websocket.close(code=1011, reason="guacamole_unavailable")
                return
        finally:
            websocket_close_reason = getattr(websocket, "close_reason", None)
            if websocket_close_reason and close_reason == "session_end":
                close_reason = str(websocket_close_reason).strip()[:120] or close_reason
            if connection_id:
                self._unregister_active_connection(connection_id)
            if callable(session.on_close):
                try:
                    session.on_close(close_reason)
                except Exception:
                    self.logger.debug(
                        "Failed to notify Guacamole VNC session close agent_id=%s",
                        session.agent_id,
                        exc_info=True,
                    )
            logger.info(
                "Guacamole VNC session ended agent_id=%s session_id=%s participant_id=%s role=%s reason=%s",
                agent_id,
                session.session_id or "-",
                session.participant_id or "-",
                session.role or "-",
                close_reason,
            )

    def _register_active_connection(self, session: Any, websocket: Any) -> str:
        connection_id = uuid.uuid4().hex
        record = ActiveVncConnection(
            connection_id=connection_id,
            websocket=websocket,
            session_id=session.session_id or "",
            participant_id=session.participant_id or "",
            agent_id=session.agent_id or "",
            operator_id=session.operator_id or "",
            role=session.role or "",
        )
        with self._active_connections_lock:
            self._active_connections[connection_id] = record
        return connection_id

    def _unregister_active_connection(self, connection_id: str) -> None:
        with self._active_connections_lock:
            self._active_connections.pop(connection_id, None)

    async def _close_websocket(self, websocket: Any, *, reason: str) -> None:
        try:
            await websocket.close(code=4000, reason=(reason or "vnc_session_closed")[:120])
        except Exception:
            pass

    def _close_matching_connections(self, predicate: Callable[[ActiveVncConnection], bool], *, reason: str) -> int:
        loop = self._loop
        if loop is None or loop.is_closed():
            return 0
        with self._active_connections_lock:
            targets = [record.websocket for record in self._active_connections.values() if predicate(record)]
        for websocket in targets:
            try:
                asyncio.run_coroutine_threadsafe(
                    self._close_websocket(websocket, reason=reason),
                    loop,
                )
            except Exception:
                self.logger.debug("Failed to schedule Guacamole VNC websocket close.", exc_info=True)
        return len(targets)

    def disconnect_session(self, session_id: str, *, reason: str = "session_closed") -> int:
        normalized_session_id = str(session_id or "").strip()
        if not normalized_session_id:
            return 0
        return self._close_matching_connections(
            lambda record: record.session_id == normalized_session_id,
            reason=reason,
        )

    def disconnect_participant(
        self,
        session_id: str,
        participant_id: str,
        *,
        reason: str = "participant_disconnect",
    ) -> int:
        normalized_session_id = str(session_id or "").strip()
        normalized_participant_id = str(participant_id or "").strip()
        if not normalized_session_id or not normalized_participant_id:
            return 0
        return self._close_matching_connections(
            lambda record: (
                record.session_id == normalized_session_id
                and record.participant_id == normalized_participant_id
            ),
            reason=reason,
        )


def _ensure_guacamole_registry(context: Any, logger: logging.Logger) -> GuacamoleSessionRegistry:
    registry = getattr(context, "guacamole_vnc_registry", None)
    if registry is None:
        ttl = int(getattr(context, "vnc_session_ttl_seconds", 120))
        registry = GuacamoleSessionRegistry(ttl_seconds=ttl, logger=logger)
        setattr(context, "guacamole_vnc_registry", registry)
    return registry


def ensure_guacamole_vnc_proxy(
    context: Any,
    *,
    logger: Optional[logging.Logger] = None,
    resolver: Optional[Callable[[str], Optional[Tuple[str, int]]]] = None,
) -> Optional[GuacamoleSessionRegistry]:
    if logger is None:
        logger = context.logger if hasattr(context, "logger") else logging.getLogger("borealis.engine.vnc")

    guacamole_registry = _ensure_guacamole_registry(context, logger)
    proxy = getattr(context, "vnc_proxy", None)
    if proxy is None:
        proxy = VncProxyServer(
            host=str(getattr(context, "vnc_ws_host", "0.0.0.0")),
            port=int(getattr(context, "vnc_ws_port", 4823)),
            logger=logger.getChild("vnc_proxy"),
            guacamole_registry=guacamole_registry,
            emit_agent_event=getattr(context, "emit_agent_event", None),
            resolver=resolver,
            guacamole_path=str(getattr(context, "guacamole_vnc_ws_path", GUACAMOLE_WS_PATH)),
            guacd_host=str(getattr(context, "guacd_host", DEFAULT_GUACD_HOST)),
            guacd_port=int(getattr(context, "guacd_port", DEFAULT_GUACD_PORT)),
            ssl_context=None,
        )
        setattr(context, "vnc_proxy", proxy)
    else:
        proxy.guacamole_registry = guacamole_registry
        proxy.guacamole_path = str(getattr(context, "guacamole_vnc_ws_path", GUACAMOLE_WS_PATH))
        proxy.guacd_host = str(getattr(context, "guacd_host", DEFAULT_GUACD_HOST))
        proxy.guacd_port = int(getattr(context, "guacd_port", DEFAULT_GUACD_PORT))
        if resolver is not None:
            try:
                proxy._resolver = resolver
            except Exception:
                pass

    if not proxy.ensure_started():
        logger.warning("Guacamole VNC proxy failed to start")
        return None
    return guacamole_registry


__all__ = [
    "GUACAMOLE_WEBSOCKET_SUBPROTOCOL",
    "VncProxyServer",
    "ensure_guacamole_vnc_proxy",
]
