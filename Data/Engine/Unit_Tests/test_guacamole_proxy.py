# ======================================================
# Data\Engine\Unit_Tests\test_guacamole_proxy.py
# Description: Validates Apache Guacamole VNC tunnel protocol helpers.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import asyncio
import logging

import pytest

from Data.Engine.services.RemoteDesktop import guacamole_proxy
from Data.Engine.services.RemoteDesktop.guacamole_proxy import (
    GuacamoleProtocolParser,
    GuacamoleVncSession,
    encode_instruction,
    guacamole_connect_arguments,
)


class _FakeReader:
    def __init__(self, chunks: list[bytes]) -> None:
        self.chunks = list(chunks)

    async def read(self, _size: int) -> bytes:
        if self.chunks:
            return self.chunks.pop(0)
        await asyncio.sleep(60)
        return b""


class _FakeWriter:
    def __init__(self) -> None:
        self.writes: list[bytes] = []
        self.closed = False

    def write(self, data: bytes) -> None:
        self.writes.append(data)

    async def drain(self) -> None:
        return None

    def close(self) -> None:
        self.closed = True

    async def wait_closed(self) -> None:
        return None


class _FakeWebSocket:
    def __init__(self, messages: list[str]) -> None:
        self.messages = list(messages)
        self.sent: list[str] = []

    def __aiter__(self):
        return self

    async def __anext__(self) -> str:
        if self.messages:
            return self.messages.pop(0)
        raise StopAsyncIteration

    async def send(self, message: str) -> None:
        self.sent.append(message)


def test_guacamole_instruction_parser_handles_split_frames() -> None:
    parser = GuacamoleProtocolParser()

    assert parser.feed("4.arg") == []
    instructions = parser.feed("s,8.hostname,4.port;")

    assert instructions == [("args", ["hostname", "port"])]


def test_guacamole_encoder_round_trips_internal_ping() -> None:
    parser = GuacamoleProtocolParser()
    encoded = encode_instruction("", "ping", "123")

    assert parser.feed(encoded) == [("", ["ping", "123"])]


def test_guacamole_connect_arguments_are_server_side_only() -> None:
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        role="controller",
    )

    values = guacamole_connect_arguments(
        session,
        [
            guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
            "hostname",
            "port",
            "password",
            "username",
            "read-only",
            "disable-display-resize",
            "color-depth",
            "autoretry",
            "resize-method",
        ],
    )

    assert values == [
        guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
        "10.255.0.4",
        "5900",
        "secretpw",
        "",
        "",
        "true",
        "24",
        "3",
        "",
    ]


def test_guacamole_connect_arguments_include_performance_preference() -> None:
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        role="controller",
        performance_preference=2,
    )

    values = guacamole_connect_arguments(
        session,
        [
            guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
            "force-lossless",
            "compress-level",
            "quality-level",
        ],
    )

    assert values == [
        guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
        "true",
        "9",
        "9",
    ]


def test_guacamole_image_mimetypes_follow_performance_preference() -> None:
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-2) == ("image/jpeg", "image/webp", "image/png")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-1) == ("image/jpeg", "image/webp", "image/png")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(0) == ("image/png", "image/jpeg", "image/webp")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(1) == ("image/webp", "image/png", "image/jpeg")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(2) == ("image/png", "image/webp", "image/jpeg")


def test_guacamole_filters_internal_ping_before_guacd() -> None:
    payload = (
        encode_instruction("sync", "12345")
        + encode_instruction("", "ping", "67890")
        + encode_instruction("mouse", "10", "20", "1")
    )

    forwarded, ping_args, disconnect = guacamole_proxy._filter_client_payload_for_guacd(payload)

    assert forwarded == encode_instruction("sync", "12345") + encode_instruction("mouse", "10", "20", "1")
    assert ping_args == [["ping", "67890"]]
    assert disconnect is False


def test_guacamole_extracts_complete_instruction_strings() -> None:
    first = encode_instruction("sync", "12345")
    second = encode_instruction("mouse", "10", "20", "1")
    partial = "4."

    instructions, remaining = guacamole_proxy._extract_complete_guacamole_instruction_strings(first + second + partial)

    assert [(opcode, args) for _raw, opcode, args in instructions] == [
        ("sync", ["12345"]),
        ("mouse", ["10", "20", "1"]),
    ]
    assert "".join(raw for raw, _opcode, _args in instructions) == first + second
    assert remaining == partial


def test_guacamole_proxy_retries_until_backend_ready(monkeypatch: pytest.MonkeyPatch) -> None:
    first_reader = _FakeReader([encode_instruction("error", "Authentication failed.").encode("utf-8")])
    second_reader = _FakeReader(
        [
            encode_instruction(
                "args",
                guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
                "hostname",
                "port",
                "password",
            ).encode("utf-8"),
            encode_instruction("ready", "uuid-1").encode("utf-8"),
        ]
    )
    writers: list[_FakeWriter] = []
    readers = [first_reader, second_reader]

    async def _fake_open_connection(_host: str, _port: int):
        writer = _FakeWriter()
        writers.append(writer)
        return readers[len(writers) - 1], writer

    monkeypatch.setattr(guacamole_proxy.asyncio, "open_connection", _fake_open_connection)
    monkeypatch.setattr(guacamole_proxy, "_GUACD_READY_RETRY_DELAY_SECONDS", 0)
    monkeypatch.setattr(guacamole_proxy, "_GUACD_BACKEND_VERIFY_SECONDS", 0)
    opened: list[bool] = []
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        session_id="session-1",
        on_open=lambda: opened.append(True),
    )
    websocket = _FakeWebSocket([encode_instruction("disconnect")])

    asyncio.run(
        guacamole_proxy.proxy_guacamole_vnc_session(
            websocket=websocket,
            session=session,
            logger=logging.getLogger("test.guacamole.proxy"),
            guacd_host="127.0.0.1",
            guacd_port=4822,
        )
    )

    assert len(writers) == 2
    assert writers[0].closed is True
    assert websocket.sent[0] == encode_instruction("", "uuid-1")
    assert opened == [True]
    handshake = GuacamoleProtocolParser().feed(b"".join(writers[1].writes).decode("utf-8"))
    image_args = next(args for opcode, args in handshake if opcode == "image")
    connect_args = next(args for opcode, args in handshake if opcode == "connect")
    assert image_args == ["image/png", "image/jpeg", "image/webp"]
    assert connect_args == [
        guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
        "10.255.0.4",
        "5900",
        "secretpw",
    ]


def test_guacamole_proxy_does_not_stack_post_ready_backend_error(monkeypatch: pytest.MonkeyPatch) -> None:
    reader = _FakeReader(
        [
            encode_instruction(
                "args",
                guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
                "hostname",
                "port",
                "password",
            ).encode("utf-8"),
            encode_instruction("ready", "uuid-failed").encode("utf-8"),
            encode_instruction("error", "Aborted. See logs.", "519").encode("utf-8"),
        ]
    )
    writers: list[_FakeWriter] = []

    async def _fake_open_connection(_host: str, _port: int):
        writer = _FakeWriter()
        writers.append(writer)
        return reader, writer

    monkeypatch.setattr(guacamole_proxy.asyncio, "open_connection", _fake_open_connection)
    monkeypatch.setattr(guacamole_proxy, "_GUACD_READY_RETRY_DELAY_SECONDS", 0)
    first_frames: list[str] = []
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        session_id="session-1",
        on_first_frame=lambda opcode: first_frames.append(opcode),
    )
    websocket = _FakeWebSocket([encode_instruction("disconnect")])

    with pytest.raises(guacamole_proxy.GuacdBackendRetryableError):
        asyncio.run(
            guacamole_proxy.proxy_guacamole_vnc_session(
                websocket=websocket,
                session=session,
                logger=logging.getLogger("test.guacamole.proxy"),
                guacd_host="127.0.0.1",
                guacd_port=4822,
            )
        )

    assert len(writers) == 1
    assert writers[0].closed is True
    assert websocket.sent == []
    assert first_frames == []


def test_guacamole_proxy_prefers_lossy_images_for_speed_bias(monkeypatch: pytest.MonkeyPatch) -> None:
    reader = _FakeReader(
        [
            encode_instruction(
                "args",
                guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
                "hostname",
                "port",
                "password",
            ).encode("utf-8"),
            encode_instruction("ready", "uuid-1").encode("utf-8"),
        ]
    )
    writer = _FakeWriter()

    async def _fake_open_connection(_host: str, _port: int):
        return reader, writer

    monkeypatch.setattr(guacamole_proxy.asyncio, "open_connection", _fake_open_connection)
    monkeypatch.setattr(guacamole_proxy, "_GUACD_BACKEND_VERIFY_SECONDS", 0)
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        session_id="session-1",
        performance_preference=-2,
    )
    websocket = _FakeWebSocket([encode_instruction("disconnect")])

    asyncio.run(
        guacamole_proxy.proxy_guacamole_vnc_session(
            websocket=websocket,
            session=session,
            logger=logging.getLogger("test.guacamole.proxy"),
            guacd_host="127.0.0.1",
            guacd_port=4822,
        )
    )

    handshake = GuacamoleProtocolParser().feed(b"".join(writer.writes).decode("utf-8"))
    image_args = next(args for opcode, args in handshake if opcode == "image")
    assert image_args == ["image/jpeg", "image/webp", "image/png"]


def test_guacamole_proxy_forwards_ready_coalesced_display_instructions(monkeypatch: pytest.MonkeyPatch) -> None:
    reader = _FakeReader(
        [
            encode_instruction(
                "args",
                guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
                "hostname",
                "port",
                "password",
            ).encode("utf-8"),
            (
                encode_instruction("ready", "uuid-1")
                + encode_instruction("size", "0", "1024", "768")
                + encode_instruction("sync", "1234")
            ).encode("utf-8"),
        ]
    )
    writer = _FakeWriter()

    async def _fake_open_connection(_host: str, _port: int):
        return reader, writer

    monkeypatch.setattr(guacamole_proxy.asyncio, "open_connection", _fake_open_connection)
    first_frames: list[str] = []
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        session_id="session-1",
        on_first_frame=lambda opcode: first_frames.append(opcode),
    )
    websocket = _FakeWebSocket([encode_instruction("disconnect")])

    asyncio.run(
        guacamole_proxy.proxy_guacamole_vnc_session(
            websocket=websocket,
            session=session,
            logger=logging.getLogger("test.guacamole.proxy"),
            guacd_host="127.0.0.1",
            guacd_port=4822,
        )
    )

    assert websocket.sent[0] == encode_instruction("", "uuid-1")
    assert websocket.sent[1] == encode_instruction("size", "0", "1024", "768") + encode_instruction("sync", "1234")
    assert first_frames == ["size"]
