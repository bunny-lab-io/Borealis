# ======================================================
# Data\Engine\services\RemoteDesktop\vnc_proxy.py
# Description: VNC tunnel proxy (WebSocket -> TCP) for noVNC sessions.
#
# API Endpoints (if applicable): None
# ======================================================

"""VNC WebSocket proxy that bridges browser sessions to agent VNC servers."""
from __future__ import annotations

import asyncio
import logging
import socket
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, Optional, Tuple
from urllib.parse import parse_qs, urlsplit

import websockets

VNC_WS_PATH = "/remote-desktop/vnc"
_MAX_MESSAGE_SIZE = 100_000_000
_CONNECT_WAIT_WINDOW_SECONDS = 20.0
_CONNECT_TIMEOUT_SECONDS = 1.0
_CONNECT_RETRY_DELAY_SECONDS = 0.25
# Delay forced transport recovery until the backend VNC socket has genuinely
# stalled, and only request it once per browser session to avoid restart churn
# on the shared WireGuard listener.
_CONNECT_RECOVERY_AFTER_SECONDS = 5.0
_CONNECT_RECOVERY_MAX_ATTEMPTS = 1


@dataclass
class VncSession:
    token: str
    agent_id: str
    host: str
    port: int
    created_at: float
    expires_at: float
    operator_id: Optional[str] = None
    restart_tunnel: Optional[Callable[[str], None]] = None
    confirm_transport: Optional[Callable[[str], None]] = None


class VncSessionRegistry:
    def __init__(self, ttl_seconds: int, logger: logging.Logger) -> None:
        self.ttl_seconds = max(30, int(ttl_seconds))
        self.logger = logger
        self._lock = threading.Lock()
        self._sessions: Dict[str, VncSession] = {}

    def _cleanup(self, now: Optional[float] = None) -> None:
        current = now if now is not None else time.time()
        expired = [token for token, session in self._sessions.items() if session.expires_at <= current]
        for token in expired:
            self._sessions.pop(token, None)

    def create(
        self,
        *,
        agent_id: str,
        host: str,
        port: int,
        operator_id: Optional[str] = None,
        restart_tunnel: Optional[Callable[[str], None]] = None,
        confirm_transport: Optional[Callable[[str], None]] = None,
    ) -> VncSession:
        token = uuid.uuid4().hex
        now = time.time()
        expires_at = now + self.ttl_seconds
        session = VncSession(
            token=token,
            agent_id=agent_id,
            host=host,
            port=port,
            created_at=now,
            expires_at=expires_at,
            operator_id=operator_id,
            restart_tunnel=restart_tunnel,
            confirm_transport=confirm_transport,
        )
        with self._lock:
            self._cleanup(now)
            self._sessions[token] = session
        return session

    def consume(self, token: str) -> Optional[VncSession]:
        if not token:
            return None
        with self._lock:
            self._cleanup()
            session = self._sessions.pop(token, None)
        return session

    def revoke_agent(self, agent_id: str) -> int:
        if not agent_id:
            return 0
        removed = 0
        with self._lock:
            self._cleanup()
            tokens = [token for token, session in self._sessions.items() if session.agent_id == agent_id]
            for token in tokens:
                if self._sessions.pop(token, None):
                    removed += 1
        return removed


class VncProxyServer:
    def __init__(
        self,
        *,
        host: str,
        port: int,
        registry: VncSessionRegistry,
        logger: logging.Logger,
        emit_agent_event: Optional[Callable[[str, str, Any], bool]] = None,
        resolver: Optional[Callable[[str], Optional[Tuple[str, int]]]] = None,
        path: str = VNC_WS_PATH,
        ssl_context: Optional[Any] = None,
    ) -> None:
        self.host = host
        self.port = port
        self.registry = registry
        self.logger = logger
        self._emit_agent_event = emit_agent_event
        self._resolver = resolver
        self.path = path or VNC_WS_PATH
        self.ssl_context = ssl_context
        self._thread: Optional[threading.Thread] = None
        self._ready = threading.Event()
        self._failed = threading.Event()

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
            self.logger.error("VNC proxy server failed: %s", exc)
            self._ready.set()

    async def _serve(self) -> None:
        self.logger.info("Starting VNC proxy on %s:%s", self.host, self.port)
        try:
            server = await websockets.serve(
                self._handle_client,
                self.host,
                self.port,
                ssl=self.ssl_context,
                max_size=_MAX_MESSAGE_SIZE,
                ping_interval=20,
                ping_timeout=20,
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
        if normalized_path != self.path:
            self.logger.warning("VNC proxy rejected request with invalid path: %s", raw_path)
            await websocket.close(code=1008, reason="invalid_path")
            return
        query = parse_qs(parsed.query or "")
        token = (query.get("token") or [""])[0]
        session = self.registry.consume(token)
        host = session.host if session else ""
        port = session.port if session else 0
        agent_id = session.agent_id if session else ""
        if not host or not port or not agent_id:
            token_hint = token[:8] if token else "-"
            self.logger.warning(
                "VNC proxy rejected session (token=%s)", token_hint
            )
            await websocket.close(code=1008, reason="invalid_session")
            return

        logger = self.logger.getChild("session")
        logger.info("VNC session start agent_id=%s", agent_id)

        try:
            try:
                reader, writer = await self._connect_vnc(session)
                self._configure_writer_socket(writer)
            except Exception as exc:
                logger.warning("VNC connect failed: %s", exc)
                await websocket.close(code=1011, reason="vnc_unavailable")
                return

            async def _ws_to_tcp() -> None:
                try:
                    async for message in websocket:
                        if message is None:
                            break
                        if isinstance(message, str):
                            data = message.encode("utf-8")
                        else:
                            data = bytes(message)
                        writer.write(data)
                        await writer.drain()
                finally:
                    try:
                        writer.close()
                    except Exception:
                        pass

            async def _tcp_to_ws() -> None:
                try:
                    while True:
                        data = await reader.read(8192)
                        if not data:
                            break
                        await websocket.send(data)
                finally:
                    try:
                        await websocket.close()
                    except Exception:
                        pass

            await asyncio.wait(
                [asyncio.create_task(_ws_to_tcp()), asyncio.create_task(_tcp_to_ws())],
                return_when=asyncio.FIRST_COMPLETED,
            )
        finally:
            logger.info("VNC session ended agent_id=%s", agent_id)

    def _configure_writer_socket(self, writer: Any) -> None:
        try:
            sock = writer.get_extra_info("socket")
            if sock is not None:
                sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        except Exception:
            self.logger.debug("Failed to configure VNC TCP_NODELAY", exc_info=True)

    async def _connect_vnc(self, session: VncSession) -> Tuple[Any, Any]:
        host = session.host
        port = session.port
        started_at = time.monotonic()
        deadline = started_at + _CONNECT_WAIT_WINDOW_SECONDS
        recovery_attempts = 0
        last_exc: Optional[Exception] = None
        while True:
            now = time.monotonic()
            remaining = deadline - now
            if remaining <= 0:
                break
            try:
                timeout = min(_CONNECT_TIMEOUT_SECONDS, max(0.5, remaining))
                reader, writer = await asyncio.wait_for(
                    asyncio.open_connection(host, port),
                    timeout=timeout,
                )
                if callable(session.confirm_transport):
                    try:
                        session.confirm_transport("vnc_backend_connect")
                    except Exception:
                        self.logger.debug(
                            "Failed to confirm VNC transport success agent_id=%s",
                            session.agent_id,
                            exc_info=True,
                        )
                return reader, writer
            except Exception as exc:
                last_exc = exc
                if (
                    recovery_attempts < _CONNECT_RECOVERY_MAX_ATTEMPTS
                    and callable(session.restart_tunnel)
                    and (time.monotonic() - started_at) >= _CONNECT_RECOVERY_AFTER_SECONDS
                ):
                    recovery_attempts += 1
                    try:
                        session.restart_tunnel("vnc_connect_retry")
                    except Exception:
                        self.logger.debug(
                            "Failed to request tunnel restart during VNC connect agent_id=%s",
                            session.agent_id,
                            exc_info=True,
                        )
                remaining = deadline - time.monotonic()
                if remaining > 0:
                    await asyncio.sleep(min(_CONNECT_RETRY_DELAY_SECONDS, remaining))
        if last_exc:
            raise last_exc
        raise RuntimeError("vnc_connect_failed")

    def _notify_agent_session_end(self, session: VncSession, reason: str) -> None:
        if not self._emit_agent_event:
            return
        payload = {"agent_id": session.agent_id, "reason": reason}
        try:
            self._emit_agent_event(session.agent_id, "vnc_stop", payload)
        except Exception:
            self.logger.debug("Failed to emit vnc_stop for agent_id=%s", session.agent_id, exc_info=True)

def ensure_vnc_proxy(
    context: Any,
    *,
    logger: Optional[logging.Logger] = None,
    resolver: Optional[Callable[[str], Optional[Tuple[str, int]]]] = None,
) -> Optional[VncSessionRegistry]:
    if logger is None:
        logger = context.logger if hasattr(context, "logger") else logging.getLogger("borealis.engine.vnc")

    registry = getattr(context, "vnc_registry", None)
    if registry is None:
        ttl = int(getattr(context, "vnc_session_ttl_seconds", 120))
        registry = VncSessionRegistry(ttl_seconds=ttl, logger=logger)
        setattr(context, "vnc_registry", registry)

    proxy = getattr(context, "vnc_proxy", None)
    if proxy is None:
        proxy = VncProxyServer(
            host=str(getattr(context, "vnc_ws_host", "0.0.0.0")),
            port=int(getattr(context, "vnc_ws_port", 4823)),
            registry=registry,
            logger=logger.getChild("vnc_proxy"),
            emit_agent_event=getattr(context, "emit_agent_event", None),
            resolver=resolver,
            path=str(getattr(context, "public_vnc_path", VNC_WS_PATH)),
            ssl_context=None,
        )
        setattr(context, "vnc_proxy", proxy)
    elif resolver is not None:
        try:
            proxy._resolver = resolver  # type: ignore[attr-defined]
        except Exception:
            pass

    if not proxy.ensure_started():
        logger.error("VNC proxy failed to start; VNC sessions unavailable.")
        return None
    return registry


__all__ = ["VNC_WS_PATH", "VncSessionRegistry", "VncProxyServer", "ensure_vnc_proxy"]
