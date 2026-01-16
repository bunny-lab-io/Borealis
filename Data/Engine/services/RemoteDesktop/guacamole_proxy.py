# ======================================================
# Data\Engine\services\RemoteDesktop\guacamole_proxy.py
# Description: Guacamole tunnel proxy (WebSocket -> guacd) for RDP sessions.
#
# API Endpoints (if applicable): None
# ======================================================

"""Guacamole WebSocket proxy that bridges browser tunnels to guacd."""
from __future__ import annotations

import asyncio
import logging
import ssl
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any, Dict, Optional, Tuple
from urllib.parse import parse_qs, urlsplit

import websockets

GUAC_WS_PATH = "/guacamole"
_MAX_MESSAGE_SIZE = 100_000_000


@dataclass
class RdpSession:
    token: str
    agent_id: str
    host: str
    port: int
    protocol: str
    username: str
    password: str
    ignore_cert: bool
    created_at: float
    expires_at: float
    operator_id: Optional[str] = None
    domain: Optional[str] = None
    security: Optional[str] = None


class RdpSessionRegistry:
    def __init__(self, ttl_seconds: int, logger: logging.Logger) -> None:
        self.ttl_seconds = max(30, int(ttl_seconds))
        self.logger = logger
        self._lock = threading.Lock()
        self._sessions: Dict[str, RdpSession] = {}

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
        username: str,
        password: str,
        protocol: str = "rdp",
        ignore_cert: bool = True,
        operator_id: Optional[str] = None,
        domain: Optional[str] = None,
        security: Optional[str] = None,
    ) -> RdpSession:
        token = uuid.uuid4().hex
        now = time.time()
        expires_at = now + self.ttl_seconds
        session = RdpSession(
            token=token,
            agent_id=agent_id,
            host=host,
            port=port,
            protocol=protocol,
            username=username,
            password=password,
            ignore_cert=ignore_cert,
            created_at=now,
            expires_at=expires_at,
            operator_id=operator_id,
            domain=domain,
            security=security,
        )
        with self._lock:
            self._cleanup(now)
            self._sessions[token] = session
        return session

    def consume(self, token: str) -> Optional[RdpSession]:
        if not token:
            return None
        with self._lock:
            self._cleanup()
            session = self._sessions.pop(token, None)
        return session


class GuacamoleProxyServer:
    def __init__(
        self,
        *,
        host: str,
        port: int,
        guacd_host: str,
        guacd_port: int,
        registry: RdpSessionRegistry,
        logger: logging.Logger,
        ssl_context: Optional[ssl.SSLContext] = None,
    ) -> None:
        self.host = host
        self.port = port
        self.guacd_host = guacd_host
        self.guacd_port = guacd_port
        self.registry = registry
        self.logger = logger
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
            self.logger.error("Guacamole proxy server failed: %s", exc)
            self._ready.set()

    async def _serve(self) -> None:
        self.logger.info(
            "Starting Guacamole proxy on %s:%s (guacd %s:%s)",
            self.host,
            self.port,
            self.guacd_host,
            self.guacd_port,
        )
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

    async def _handle_client(self, websocket, path: str) -> None:
        parsed = urlsplit(path)
        if parsed.path != GUAC_WS_PATH:
            await websocket.close(code=1008, reason="invalid_path")
            return
        query = parse_qs(parsed.query or "")
        token = (query.get("token") or [""])[0]
        session = self.registry.consume(token)
        if not session:
            await websocket.close(code=1008, reason="invalid_session")
            return

        logger = self.logger.getChild("session")
        logger.info("Guacamole session start agent_id=%s protocol=%s", session.agent_id, session.protocol)

        try:
            reader, writer = await asyncio.open_connection(self.guacd_host, self.guacd_port)
        except Exception as exc:
            logger.warning("guacd connect failed: %s", exc)
            await websocket.close(code=1011, reason="guacd_unavailable")
            return

        try:
            await self._perform_handshake(reader, writer, session)
        except Exception as exc:
            logger.warning("guacd handshake failed: %s", exc)
            try:
                writer.close()
                await writer.wait_closed()
            except Exception:
                pass
            await websocket.close(code=1011, reason="handshake_failed")
            return

        async def _ws_to_guacd() -> None:
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

        async def _guacd_to_ws() -> None:
            try:
                while True:
                    data = await reader.read(8192)
                    if not data:
                        break
                    await websocket.send(data.decode("utf-8", errors="ignore"))
            finally:
                try:
                    await websocket.close()
                except Exception:
                    pass

        await asyncio.wait(
            [asyncio.create_task(_ws_to_guacd()), asyncio.create_task(_guacd_to_ws())],
            return_when=asyncio.FIRST_COMPLETED,
        )

        logger.info("Guacamole session ended agent_id=%s", session.agent_id)

    async def _perform_handshake(self, reader, writer, session: RdpSession) -> None:
        writer.write(_encode_instruction("select", session.protocol))
        await writer.drain()

        buffer = b""
        args = None
        deadline = time.time() + 8

        while time.time() < deadline:
            parts, buffer = await _read_instruction(reader, buffer)
            if not parts:
                continue
            op = parts[0]
            if op == "args":
                args = parts[1:]
                break
            if op == "error":
                raise RuntimeError("guacd_error:" + " ".join(parts[1:]))
        if not args:
            raise RuntimeError("guacd_args_timeout")

        params = {
            "hostname": session.host,
            "port": str(session.port),
            "username": session.username or "",
            "password": session.password or "",
        }
        if session.domain:
            params["domain"] = session.domain
        if session.security:
            params["security"] = session.security
        if session.ignore_cert:
            params["ignore-cert"] = "true"

        values = [params.get(name, "") for name in args]
        writer.write(_encode_instruction("connect", *values))
        await writer.drain()


def _encode_instruction(*elements: str) -> bytes:
    parts = []
    for element in elements:
        text = "" if element is None else str(element)
        parts.append(f"{len(text)}.{text}".encode("utf-8"))
    return b",".join(parts) + b";"


def _parse_instruction(raw: bytes) -> Tuple[str, ...]:
    parts = []
    idx = 0
    length = len(raw)
    while idx < length:
        dot = raw.find(b".", idx)
        if dot < 0:
            break
        try:
            element_len = int(raw[idx:dot].decode("ascii") or "0")
        except Exception:
            break
        start = dot + 1
        end = start + element_len
        if end > length:
            break
        parts.append(raw[start:end].decode("utf-8", errors="ignore"))
        idx = end
        if idx < length and raw[idx:idx + 1] == b",":
            idx += 1
    return tuple(parts)


async def _read_instruction(reader, buffer: bytes) -> Tuple[Tuple[str, ...], bytes]:
    while b";" not in buffer:
        chunk = await reader.read(4096)
        if not chunk:
            break
        buffer += chunk
    if b";" not in buffer:
        return tuple(), buffer
    instruction, remainder = buffer.split(b";", 1)
    if not instruction:
        return tuple(), remainder
    return _parse_instruction(instruction), remainder


def _build_ssl_context(cert_path: Optional[str], key_path: Optional[str]) -> Optional[ssl.SSLContext]:
    if not cert_path or not key_path:
        return None
    try:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(certfile=cert_path, keyfile=key_path)
        return context
    except Exception:
        return None


def ensure_guacamole_proxy(context: Any, *, logger: Optional[logging.Logger] = None) -> Optional[RdpSessionRegistry]:
    if logger is None:
        logger = context.logger if hasattr(context, "logger") else logging.getLogger("borealis.engine.rdp")

    registry = getattr(context, "rdp_registry", None)
    if registry is None:
        ttl = int(getattr(context, "rdp_session_ttl_seconds", 120))
        registry = RdpSessionRegistry(ttl_seconds=ttl, logger=logger)
        setattr(context, "rdp_registry", registry)

    proxy = getattr(context, "rdp_proxy", None)
    if proxy is None:
        cert_path = getattr(context, "tls_bundle_path", None) or getattr(context, "tls_cert_path", None)
        ssl_context = _build_ssl_context(
            cert_path,
            getattr(context, "tls_key_path", None),
        )
        proxy = GuacamoleProxyServer(
            host=str(getattr(context, "rdp_ws_host", "0.0.0.0")),
            port=int(getattr(context, "rdp_ws_port", 4823)),
            guacd_host=str(getattr(context, "guacd_host", "127.0.0.1")),
            guacd_port=int(getattr(context, "guacd_port", 4822)),
            registry=registry,
            logger=logger.getChild("guacamole_proxy"),
            ssl_context=ssl_context,
        )
        setattr(context, "rdp_proxy", proxy)

    if not proxy.ensure_started():
        logger.error("Guacamole proxy failed to start; RDP sessions unavailable.")
        return None
    return registry


__all__ = ["GUAC_WS_PATH", "RdpSessionRegistry", "GuacamoleProxyServer", "ensure_guacamole_proxy"]
