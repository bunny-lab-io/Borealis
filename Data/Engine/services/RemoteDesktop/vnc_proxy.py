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
import ssl
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, Optional, Tuple
from urllib.parse import parse_qs, urlsplit

import websockets

VNC_WS_PATH = "/vnc"
_MAX_MESSAGE_SIZE = 100_000_000


@dataclass
class VncSession:
    token: str
    agent_id: str
    host: str
    port: int
    created_at: float
    expires_at: float
    operator_id: Optional[str] = None


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
        ssl_context: Optional[ssl.SSLContext] = None,
    ) -> None:
        self.host = host
        self.port = port
        self.registry = registry
        self.logger = logger
        self._emit_agent_event = emit_agent_event
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
        parsed = urlsplit(raw_path)
        if parsed.path != VNC_WS_PATH:
            self.logger.warning("VNC proxy rejected request with invalid path: %s", raw_path)
            await websocket.close(code=1008, reason="invalid_path")
            return
        query = parse_qs(parsed.query or "")
        token = (query.get("token") or [""])[0]
        session = self.registry.consume(token)
        if not session:
            token_hint = token[:8] if token else "-"
            self.logger.warning("VNC proxy rejected session (token=%s)", token_hint)
            await websocket.close(code=1008, reason="invalid_session")
            return

        logger = self.logger.getChild("session")
        logger.info("VNC session start agent_id=%s", session.agent_id)

        try:
            try:
                reader, writer = await self._connect_vnc(session.host, session.port)
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
            logger.info("VNC session ended agent_id=%s", session.agent_id)
            self._notify_agent_session_end(session, reason="vnc_session_end")

    async def _connect_vnc(self, host: str, port: int) -> Tuple[Any, Any]:
        attempts = 5
        delay = 0.5
        last_exc: Optional[Exception] = None
        for attempt in range(attempts):
            try:
                return await asyncio.open_connection(host, port)
            except Exception as exc:
                last_exc = exc
                if attempt < attempts - 1:
                    await asyncio.sleep(delay)
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


def _build_ssl_context(cert_path: Optional[str], key_path: Optional[str]) -> Optional[ssl.SSLContext]:
    if not cert_path or not key_path:
        return None
    try:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(certfile=cert_path, keyfile=key_path)
        return context
    except Exception:
        return None


def ensure_vnc_proxy(context: Any, *, logger: Optional[logging.Logger] = None) -> Optional[VncSessionRegistry]:
    if logger is None:
        logger = context.logger if hasattr(context, "logger") else logging.getLogger("borealis.engine.vnc")

    registry = getattr(context, "vnc_registry", None)
    if registry is None:
        ttl = int(getattr(context, "vnc_session_ttl_seconds", 120))
        registry = VncSessionRegistry(ttl_seconds=ttl, logger=logger)
        setattr(context, "vnc_registry", registry)

    proxy = getattr(context, "vnc_proxy", None)
    if proxy is None:
        cert_path = getattr(context, "tls_bundle_path", None) or getattr(context, "tls_cert_path", None)
        ssl_context = _build_ssl_context(
            cert_path,
            getattr(context, "tls_key_path", None),
        )
        proxy = VncProxyServer(
            host=str(getattr(context, "vnc_ws_host", "0.0.0.0")),
            port=int(getattr(context, "vnc_ws_port", 4823)),
            registry=registry,
            logger=logger.getChild("vnc_proxy"),
            emit_agent_event=getattr(context, "emit_agent_event", None),
            ssl_context=ssl_context,
        )
        setattr(context, "vnc_proxy", proxy)

    if not proxy.ensure_started():
        logger.error("VNC proxy failed to start; VNC sessions unavailable.")
        return None
    return registry


__all__ = ["VNC_WS_PATH", "VncSessionRegistry", "VncProxyServer", "ensure_vnc_proxy"]
