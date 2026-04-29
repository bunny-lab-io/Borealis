# ======================================================
# Data\Engine\services\RemoteDesktop\guacamole_proxy.py
# Description: Apache Guacamole tunnel support for Borealis VNC sessions.
#
# API Endpoints (if applicable): None
# ======================================================

"""Server-side Guacamole tunnel bridge for UltraVNC over Borealis WireGuard."""
from __future__ import annotations

import asyncio
import logging
import socket
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional, Tuple


GUACAMOLE_WS_PATH = "/remote-desktop/vnc/guacamole"
DEFAULT_GUACD_HOST = "127.0.0.1"
DEFAULT_GUACD_PORT = 4822
_GUACD_CONNECT_TIMEOUT_SECONDS = 3.0
_GUACD_HANDSHAKE_TIMEOUT_SECONDS = 5.0
_GUACD_READY_ATTEMPTS = 7
_GUACD_READY_RETRY_DELAY_SECONDS = 1.25


@dataclass
class GuacamoleVncSession:
    token: str
    agent_id: str
    host: str
    port: int
    password: str
    created_at: float
    expires_at: float
    operator_id: Optional[str] = None
    session_id: str = ""
    participant_id: str = ""
    role: str = ""
    width: int = 1024
    height: int = 768
    dpi: int = 96
    restart_tunnel: Optional[Callable[[str], None]] = None
    confirm_transport: Optional[Callable[[str], None]] = None
    on_open: Optional[Callable[[], None]] = None
    on_close: Optional[Callable[[str], None]] = None


class GuacamoleSessionRegistry:
    def __init__(self, ttl_seconds: int, logger: logging.Logger) -> None:
        self.ttl_seconds = max(30, int(ttl_seconds))
        self.logger = logger
        self._lock = threading.Lock()
        self._sessions: Dict[str, GuacamoleVncSession] = {}

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
        password: str,
        operator_id: Optional[str] = None,
        session_id: str = "",
        participant_id: str = "",
        role: str = "",
        width: int = 1024,
        height: int = 768,
        dpi: int = 96,
        restart_tunnel: Optional[Callable[[str], None]] = None,
        confirm_transport: Optional[Callable[[str], None]] = None,
        on_open: Optional[Callable[[], None]] = None,
        on_close: Optional[Callable[[str], None]] = None,
    ) -> GuacamoleVncSession:
        token = uuid.uuid4().hex
        now = time.time()
        session = GuacamoleVncSession(
            token=token,
            agent_id=agent_id,
            host=host,
            port=port,
            password=password,
            created_at=now,
            expires_at=now + self.ttl_seconds,
            operator_id=operator_id,
            session_id=session_id,
            participant_id=participant_id,
            role=role,
            width=max(1, int(width or 1024)),
            height=max(1, int(height or 768)),
            dpi=max(1, int(dpi or 96)),
            restart_tunnel=restart_tunnel,
            confirm_transport=confirm_transport,
            on_open=on_open,
            on_close=on_close,
        )
        with self._lock:
            self._cleanup(now)
            self._sessions[token] = session
        return session

    def consume(self, token: str) -> Optional[GuacamoleVncSession]:
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


def encode_instruction(opcode: Any, *args: Any) -> str:
    parts = ["" if opcode is None else str(opcode)]
    parts.extend("" if arg is None else str(arg) for arg in args)
    encoded = [f"{len(part)}.{part}" for part in parts]
    return ",".join(encoded) + ";"


class GuacamoleProtocolParser:
    def __init__(self) -> None:
        self._buffer = ""
        self._elements: List[str] = []

    def feed(self, data: Any) -> List[Tuple[str, List[str]]]:
        if isinstance(data, bytes):
            self._buffer += data.decode("utf-8", errors="replace")
        else:
            self._buffer += str(data or "")
        instructions: List[Tuple[str, List[str]]] = []
        while True:
            dot_index = self._buffer.find(".")
            if dot_index < 0:
                break
            length_text = self._buffer[:dot_index]
            if not length_text.isdigit():
                raise ValueError("invalid_guacamole_element_length")
            element_length = int(length_text)
            value_start = dot_index + 1
            value_end = value_start + element_length
            if len(self._buffer) <= value_end:
                break
            value = self._buffer[value_start:value_end]
            delimiter = self._buffer[value_end]
            if delimiter not in {",", ";"}:
                raise ValueError("invalid_guacamole_element_delimiter")
            self._buffer = self._buffer[value_end + 1:]
            self._elements.append(value)
            if delimiter == ";":
                elements = self._elements
                self._elements = []
                if elements:
                    instructions.append((elements[0], elements[1:]))
        return instructions


def guacamole_connect_arguments(session: GuacamoleVncSession, names: List[str]) -> List[str]:
    values: Dict[str, str] = {
        "hostname": session.host,
        "port": str(int(session.port)),
        "password": session.password,
        "username": "",
        "read-only": "true" if str(session.role or "").lower() == "view_only" else "",
        "disable-display-resize": "true",
        "color-depth": "24",
        "swap-red-blue": "",
        "cursor": "remote",
        "clipboard-encoding": "UTF-8",
        "autoretry": "3",
    }
    return [values.get(str(name or ""), "") for name in names]


def guacd_health(context: Any, *, timeout_seconds: float = 0.35) -> Dict[str, Any]:
    enabled = bool(getattr(context, "guacamole_enabled", False))
    host = str(getattr(context, "guacd_host", DEFAULT_GUACD_HOST) or DEFAULT_GUACD_HOST).strip()
    try:
        port = int(getattr(context, "guacd_port", DEFAULT_GUACD_PORT) or DEFAULT_GUACD_PORT)
    except Exception:
        port = DEFAULT_GUACD_PORT
    payload = {
        "enabled": enabled,
        "available": False,
        "host": host,
        "port": port,
        "reason": "disabled" if not enabled else "unavailable",
    }
    if not enabled:
        return payload
    try:
        with socket.create_connection((host, port), timeout=max(0.1, timeout_seconds)):
            payload["available"] = True
            payload["reason"] = "ready"
    except Exception as exc:
        payload["reason"] = str(exc)[:160] or "unavailable"
    return payload


async def _read_instruction(
    reader: Any,
    parser: GuacamoleProtocolParser,
    *,
    timeout_seconds: float,
) -> Tuple[str, List[str]]:
    while True:
        data = await asyncio.wait_for(reader.read(8192), timeout=timeout_seconds)
        if not data:
            raise RuntimeError("guacd_closed")
        instructions = parser.feed(data)
        if instructions:
            return instructions[0]


async def _write_instruction(writer: Any, opcode: Any, *args: Any) -> None:
    writer.write(encode_instruction(opcode, *args).encode("utf-8"))
    await writer.drain()


async def _close_writer(writer: Any) -> None:
    if writer is None:
        return
    try:
        writer.close()
        await writer.wait_closed()
    except Exception:
        pass


async def _handshake_guacd(
    *,
    reader: Any,
    writer: Any,
    session: GuacamoleVncSession,
) -> str:
    parser = GuacamoleProtocolParser()
    await _write_instruction(writer, "select", "vnc")
    opcode, args = await _read_instruction(
        reader,
        parser,
        timeout_seconds=_GUACD_HANDSHAKE_TIMEOUT_SECONDS,
    )
    if opcode == "error":
        raise RuntimeError(args[0] if args else "guacd_error")
    if opcode != "args":
        raise RuntimeError(f"guacd_unexpected_{opcode or 'empty'}")

    await _write_instruction(writer, "size", session.width, session.height, session.dpi)
    await _write_instruction(writer, "image", "image/png", "image/jpeg", "image/webp")
    await _write_instruction(writer, "timezone", "UTC")
    await _write_instruction(writer, "name", f"Borealis VNC {session.agent_id}")
    await _write_instruction(writer, "connect", *guacamole_connect_arguments(session, args))

    opcode, ready_args = await _read_instruction(
        reader,
        parser,
        timeout_seconds=_GUACD_HANDSHAKE_TIMEOUT_SECONDS,
    )
    if opcode == "error":
        raise RuntimeError(ready_args[0] if ready_args else "guacd_error")
    if opcode != "ready":
        raise RuntimeError(f"guacd_unexpected_{opcode or 'empty'}")
    return ready_args[0] if ready_args else uuid.uuid4().hex


async def _open_ready_guacd(
    *,
    session: GuacamoleVncSession,
    logger: logging.Logger,
    guacd_host: str,
    guacd_port: int,
) -> Tuple[Any, Any, str]:
    last_error: Optional[BaseException] = None
    for attempt in range(1, _GUACD_READY_ATTEMPTS + 1):
        reader: Any = None
        writer: Any = None
        try:
            reader, writer = await asyncio.wait_for(
                asyncio.open_connection(guacd_host, int(guacd_port)),
                timeout=_GUACD_CONNECT_TIMEOUT_SECONDS,
            )
            uuid_value = await _handshake_guacd(reader=reader, writer=writer, session=session)
            if attempt > 1:
                logger.info(
                    "Guacamole VNC backend became ready after retry agent_id=%s session_id=%s attempt=%s",
                    session.agent_id,
                    session.session_id or "-",
                    attempt,
                )
            return reader, writer, uuid_value
        except Exception as exc:
            last_error = exc
            await _close_writer(writer)
            if attempt >= _GUACD_READY_ATTEMPTS:
                break
            logger.warning(
                "Guacamole VNC backend not ready agent_id=%s session_id=%s attempt=%s/%s error=%s",
                session.agent_id,
                session.session_id or "-",
                attempt,
                _GUACD_READY_ATTEMPTS,
                str(exc)[:180],
            )
            await asyncio.sleep(_GUACD_READY_RETRY_DELAY_SECONDS)
    if last_error is not None:
        raise RuntimeError(str(last_error) or "guacd_unavailable") from last_error
    raise RuntimeError("guacd_unavailable")


async def proxy_guacamole_vnc_session(
    *,
    websocket: Any,
    session: GuacamoleVncSession,
    logger: logging.Logger,
    guacd_host: str,
    guacd_port: int,
) -> None:
    reader: Any = None
    writer: Any = None
    try:
        reader, writer, uuid_value = await _open_ready_guacd(
            session=session,
            logger=logger,
            guacd_host=guacd_host,
            guacd_port=guacd_port,
        )
        if callable(session.confirm_transport):
            try:
                session.confirm_transport("vnc_backend_connect")
            except Exception:
                logger.debug("Failed to confirm Guacamole VNC transport agent_id=%s", session.agent_id, exc_info=True)
        await websocket.send(encode_instruction("", uuid_value))
        if callable(session.on_open):
            try:
                session.on_open()
            except Exception:
                logger.debug("Failed to notify Guacamole VNC session open agent_id=%s", session.agent_id, exc_info=True)

        async def _ws_to_guacd() -> None:
            async for message in websocket:
                if isinstance(message, bytes):
                    raw_bytes = message
                    raw_text = message.decode("utf-8", errors="replace")
                else:
                    raw_text = str(message or "")
                    raw_bytes = raw_text.encode("utf-8")

                instructions: List[Tuple[str, List[str]]] = []
                try:
                    instructions = GuacamoleProtocolParser().feed(raw_text)
                except ValueError:
                    logger.debug("Forwarding unparsable Guacamole client payload to guacd")

                if len(instructions) == 1:
                    opcode, args = instructions[0]
                    if opcode == "" and args and args[0] == "ping":
                        await websocket.send(encode_instruction("", *args))
                        continue

                if raw_bytes:
                    writer.write(raw_bytes)
                    await writer.drain()
                if any(opcode == "disconnect" for opcode, _args in instructions):
                    return

        async def _guacd_to_ws() -> None:
            while True:
                data = await reader.read(8192)
                if not data:
                    break
                await websocket.send(data.decode("utf-8", errors="replace"))

        tasks = [asyncio.create_task(_ws_to_guacd()), asyncio.create_task(_guacd_to_ws())]
        done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        for task in pending:
            task.cancel()
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)
        for task in done:
            task.result()
    finally:
        await _close_writer(writer)


__all__ = [
    "DEFAULT_GUACD_HOST",
    "DEFAULT_GUACD_PORT",
    "GUACAMOLE_WS_PATH",
    "GuacamoleProtocolParser",
    "GuacamoleSessionRegistry",
    "GuacamoleVncSession",
    "encode_instruction",
    "guacamole_connect_arguments",
    "guacd_health",
    "proxy_guacamole_vnc_session",
]
