# ======================================================
# Data\Engine\services\RemoteDesktop\guacamole_proxy.py
# Description: Apache Guacamole tunnel support for Borealis VNC sessions.
#
# API Endpoints (if applicable): None
# ======================================================

"""Server-side Guacamole tunnel bridge for UltraVNC over Borealis WireGuard."""
from __future__ import annotations

import asyncio
import codecs
import logging
import socket
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional, Tuple


GUACAMOLE_WS_PATH = "/remote-desktop/vnc/guacamole"
GUACAMOLE_PROTOCOL_VERSION = "VERSION_1_5_0"
DEFAULT_GUACD_HOST = "127.0.0.1"
DEFAULT_GUACD_PORT = 4822
_GUACD_CONNECT_TIMEOUT_SECONDS = 3.0
_GUACD_HANDSHAKE_TIMEOUT_SECONDS = 5.0
_GUACD_READY_ATTEMPTS = 7
_GUACD_READY_RETRY_DELAY_SECONDS = 1.25
_GUACAMOLE_VNC_PERFORMANCE_ARGUMENTS: Dict[int, Dict[str, str]] = {
    -2: {"compress-level": "1", "quality-level": "3", "force-lossless": ""},
    -1: {"compress-level": "3", "quality-level": "5", "force-lossless": ""},
    0: {"compress-level": "", "quality-level": "", "force-lossless": ""},
    1: {"compress-level": "7", "quality-level": "8", "force-lossless": ""},
    2: {"compress-level": "9", "quality-level": "9", "force-lossless": "true"},
}


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
    performance_preference: int = 0
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
        performance_preference: int = 0,
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
            performance_preference=normalize_guacamole_performance_preference(performance_preference),
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


def normalize_guacamole_performance_preference(value: Any) -> int:
    try:
        if isinstance(value, bool):
            raise ValueError("bool_not_supported")
        normalized = int(value)
    except (TypeError, ValueError):
        normalized = 0
    return max(-2, min(2, normalized))


def guacamole_vnc_performance_arguments(preference: Any) -> Dict[str, str]:
    normalized = normalize_guacamole_performance_preference(preference)
    return dict(_GUACAMOLE_VNC_PERFORMANCE_ARGUMENTS.get(normalized) or _GUACAMOLE_VNC_PERFORMANCE_ARGUMENTS[0])


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
    values.update(guacamole_vnc_performance_arguments(session.performance_preference))
    resolved: List[str] = []
    for index, name in enumerate(names):
        normalized = str(name or "")
        if index == 0 and normalized.startswith("VERSION_"):
            resolved.append(GUACAMOLE_PROTOCOL_VERSION)
            continue
        resolved.append(values.get(normalized, ""))
    return resolved


def _extract_complete_guacamole_instruction_strings(
    raw_text: str,
) -> Tuple[List[Tuple[str, str, List[str]]], str]:
    position = 0
    instruction_start = 0
    elements: List[str] = []
    instructions: List[Tuple[str, str, List[str]]] = []
    while position < len(raw_text):
        dot_index = raw_text.find(".", position)
        if dot_index < 0:
            return instructions, raw_text[instruction_start:]
        length_text = raw_text[position:dot_index]
        if not length_text.isdigit():
            raise ValueError("invalid_guacamole_element_length")
        element_length = int(length_text)
        value_start = dot_index + 1
        value_end = value_start + element_length
        if len(raw_text) <= value_end:
            return instructions, raw_text[instruction_start:]
        value = raw_text[value_start:value_end]
        delimiter = raw_text[value_end]
        if delimiter not in {",", ";"}:
            raise ValueError("invalid_guacamole_element_delimiter")
        position = value_end + 1
        elements.append(value)
        if delimiter == ";":
            if elements:
                instructions.append((raw_text[instruction_start:position], elements[0], elements[1:]))
            elements = []
            instruction_start = position
    if elements:
        return instructions, raw_text[instruction_start:]
    return instructions, ""


def _split_guacamole_instruction_strings(raw_text: str) -> Optional[List[Tuple[str, str, List[str]]]]:
    instructions, remaining = _extract_complete_guacamole_instruction_strings(raw_text)
    if remaining:
        return None
    return instructions


def _filter_client_payload_for_guacd(raw_text: str) -> Tuple[str, List[List[str]], bool]:
    instructions = _split_guacamole_instruction_strings(raw_text)
    if instructions is None:
        return raw_text, [], False

    forwarded: List[str] = []
    ping_args: List[List[str]] = []
    disconnect = False
    for raw_instruction, opcode, args in instructions:
        if opcode == "" and args and args[0] == "ping":
            ping_args.append(args)
            continue
        if opcode == "disconnect":
            disconnect = True
        forwarded.append(raw_instruction)
    return "".join(forwarded), ping_args, disconnect


def _short_guacamole_arg(value: Any, limit: int = 120) -> str:
    text = str(value or "").replace("\r", "\\r").replace("\n", "\\n")
    if len(text) <= limit:
        return text
    return f"{text[: max(0, limit - 3)]}..."


def _guacd_error_detail(args: List[str]) -> Tuple[str, str]:
    if not args:
        return "guacd_error", "-"
    message = _short_guacamole_arg(args[0]) or "guacd_error"
    status = _short_guacamole_arg(args[1]) if len(args) > 1 else "-"
    return message, status or "-"


def _guacd_instruction_summary(opcode: str, args: List[str]) -> str:
    safe_args = [_short_guacamole_arg(arg, limit=80) for arg in args[:4]]
    if len(args) > len(safe_args):
        safe_args.append(f"...(+{len(args) - len(safe_args)})")
    return f"opcode={opcode or '-'} args={safe_args}"


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
) -> Tuple[str, List[str], List[Tuple[str, List[str]]]]:
    while True:
        data = await asyncio.wait_for(reader.read(8192), timeout=timeout_seconds)
        if not data:
            raise RuntimeError("guacd_closed")
        instructions = parser.feed(data)
        if instructions:
            opcode, args = instructions[0]
            return opcode, args, instructions[1:]


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
) -> Tuple[str, List[Tuple[str, List[str]]]]:
    parser = GuacamoleProtocolParser()
    await _write_instruction(writer, "select", "vnc")
    opcode, args, _extra = await _read_instruction(
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

    opcode, ready_args, pending = await _read_instruction(
        reader,
        parser,
        timeout_seconds=_GUACD_HANDSHAKE_TIMEOUT_SECONDS,
    )
    if opcode == "error":
        raise RuntimeError(ready_args[0] if ready_args else "guacd_error")
    if opcode != "ready":
        raise RuntimeError(f"guacd_unexpected_{opcode or 'empty'}")
    return ready_args[0] if ready_args else uuid.uuid4().hex, pending


async def _open_ready_guacd(
    *,
    session: GuacamoleVncSession,
    logger: logging.Logger,
    guacd_host: str,
    guacd_port: int,
) -> Tuple[Any, Any, str, List[Tuple[str, List[str]]]]:
    last_error: Optional[BaseException] = None
    for attempt in range(1, _GUACD_READY_ATTEMPTS + 1):
        reader: Any = None
        writer: Any = None
        try:
            reader, writer = await asyncio.wait_for(
                asyncio.open_connection(guacd_host, int(guacd_port)),
                timeout=_GUACD_CONNECT_TIMEOUT_SECONDS,
            )
            uuid_value, pending = await _handshake_guacd(reader=reader, writer=writer, session=session)
            if attempt > 1:
                logger.info(
                    "Guacamole VNC backend became ready after retry agent_id=%s session_id=%s attempt=%s",
                    session.agent_id,
                    session.session_id or "-",
                    attempt,
                )
            return reader, writer, uuid_value, pending
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
    client_instruction_count = 0
    client_ping_count = 0
    client_message_count = 0
    server_instruction_count = 0
    server_message_count = 0
    server_byte_count = 0
    try:
        reader, writer, uuid_value, pending_instructions = await _open_ready_guacd(
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
        if pending_instructions:
            pending_payload = "".join(
                encode_instruction(opcode, *args) for opcode, args in pending_instructions
            )
            server_instruction_count += len(pending_instructions)
            server_message_count += 1
            server_byte_count += len(pending_payload.encode("utf-8"))
            for opcode, args in pending_instructions[:3]:
                logger.info(
                    "Guacamole VNC backend initial instruction agent_id=%s session_id=%s %s",
                    session.agent_id,
                    session.session_id or "-",
                    _guacd_instruction_summary(opcode, args),
                )
            await websocket.send(pending_payload)
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
                nonlocal client_instruction_count, client_message_count, client_ping_count
                client_message_count += 1

                forward_text = raw_text
                ping_args: List[List[str]] = []
                disconnect_requested = False
                try:
                    forward_text, ping_args, disconnect_requested = _filter_client_payload_for_guacd(raw_text)
                except ValueError:
                    logger.debug("Forwarding unparsable Guacamole client payload to guacd")
                if ping_args:
                    client_ping_count += len(ping_args)
                if forward_text:
                    try:
                        instructions = _split_guacamole_instruction_strings(forward_text) or []
                        client_instruction_count += len(instructions)
                    except ValueError:
                        pass

                for args in ping_args:
                    await websocket.send(encode_instruction("", *args))

                if forward_text:
                    writer.write(forward_text.encode("utf-8"))
                    await writer.drain()
                elif raw_bytes and not ping_args:
                    writer.write(raw_bytes)
                    await writer.drain()

                if disconnect_requested:
                    return

        async def _guacd_to_ws() -> None:
            nonlocal server_byte_count, server_instruction_count, server_message_count
            decoder = codecs.getincrementaldecoder("utf-8")()
            instruction_buffer = ""
            while True:
                data = await reader.read(8192)
                if not data:
                    break
                server_byte_count += len(data)
                instruction_buffer += decoder.decode(data)
                instructions, instruction_buffer = _extract_complete_guacamole_instruction_strings(instruction_buffer)
                if not instructions:
                    continue
                server_instruction_count += len(instructions)
                server_message_count += 1
                for _raw_instruction, opcode, args in instructions:
                    if opcode == "error":
                        message, status = _guacd_error_detail(args)
                        logger.warning(
                            "Guacamole VNC backend error agent_id=%s session_id=%s guacd_status=%s guacd_message=%s",
                            session.agent_id,
                            session.session_id or "-",
                            status,
                            message,
                        )
                    elif opcode == "status":
                        logger.warning(
                            "Guacamole VNC backend status agent_id=%s session_id=%s %s",
                            session.agent_id,
                            session.session_id or "-",
                            _guacd_instruction_summary(opcode, args),
                        )
                    elif opcode == "disconnect":
                        logger.info(
                            "Guacamole VNC backend requested disconnect agent_id=%s session_id=%s",
                            session.agent_id,
                            session.session_id or "-",
                        )
                    elif server_instruction_count <= 3:
                        logger.info(
                            "Guacamole VNC backend instruction agent_id=%s session_id=%s %s",
                            session.agent_id,
                            session.session_id or "-",
                            _guacd_instruction_summary(opcode, args),
                        )
                await websocket.send("".join(raw_instruction for raw_instruction, _opcode, _args in instructions))
            logger.info(
                "Guacamole VNC backend stream closed agent_id=%s session_id=%s server_messages=%s server_instructions=%s server_bytes=%s",
                session.agent_id,
                session.session_id or "-",
                server_message_count,
                server_instruction_count,
                server_byte_count,
            )
            trailing = decoder.decode(b"", final=True)
            if trailing:
                instruction_buffer += trailing
            if instruction_buffer:
                logger.debug(
                    "Guacamole VNC bridge dropped incomplete guacd payload agent_id=%s session_id=%s bytes=%s",
                    session.agent_id,
                    session.session_id or "-",
                    len(instruction_buffer),
                )

        tasks = [
            asyncio.create_task(_ws_to_guacd(), name="browser_to_guacd"),
            asyncio.create_task(_guacd_to_ws(), name="guacd_to_browser"),
        ]
        done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        for task in pending:
            task.cancel()
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)
        logger.info(
            "Guacamole VNC bridge completed agent_id=%s session_id=%s done=%s client_messages=%s client_instructions=%s client_pings=%s server_messages=%s server_instructions=%s server_bytes=%s",
            session.agent_id,
            session.session_id or "-",
            ",".join(task.get_name() for task in done),
            client_message_count,
            client_instruction_count,
            client_ping_count,
            server_message_count,
            server_instruction_count,
            server_byte_count,
        )
        for task in done:
            task.result()
    finally:
        await _close_writer(writer)


__all__ = [
    "DEFAULT_GUACD_HOST",
    "DEFAULT_GUACD_PORT",
    "GUACAMOLE_PROTOCOL_VERSION",
    "GUACAMOLE_WS_PATH",
    "GuacamoleProtocolParser",
    "GuacamoleSessionRegistry",
    "GuacamoleVncSession",
    "encode_instruction",
    "guacamole_connect_arguments",
    "guacamole_vnc_performance_arguments",
    "guacd_health",
    "normalize_guacamole_performance_preference",
    "proxy_guacamole_vnc_session",
]
