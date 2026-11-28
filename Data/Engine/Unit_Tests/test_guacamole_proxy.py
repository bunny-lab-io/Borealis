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


def test_guacamole_rdp_connect_arguments_keep_credentials_server_side() -> None:
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.42",
        port=3389,
        password="domain-password",
        created_at=0,
        expires_at=120,
        protocol="rdp",
        username="nicole",
        domain="LAB",
        security="nla",
        ignore_cert=True,
    )

    values = guacamole_connect_arguments(
        session,
        [
            guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
            "hostname",
            "port",
            "username",
            "password",
            "domain",
            "security",
            "ignore-cert",
            "disable-audio",
            "resize-method",
        ],
    )

    assert values == [
        guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
        "10.255.0.42",
        "3389",
        "nicole",
        "domain-password",
        "LAB",
        "nla",
        "true",
        "true",
        "display-update",
    ]


@pytest.mark.parametrize(
    ("preference", "expected"),
    [
        (
            -2,
            [
                "16",
                "false",
                "false",
                "false",
                "false",
                "false",
                "false",
                "false",
                "false",
                "false",
                "false",
                "false",
            ],
        ),
        (
            2,
            [
                "24",
                "true",
                "true",
                "true",
                "true",
                "true",
                "true",
                "true",
                "false",
                "false",
                "false",
                "false",
            ],
        ),
    ],
)
def test_guacamole_rdp_connect_arguments_apply_visual_performance_profile(
    preference: int,
    expected: list[str],
) -> None:
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.42",
        port=3389,
        password="domain-password",
        created_at=0,
        expires_at=120,
        protocol="rdp",
        username="nicole",
        performance_preference=preference,
    )

    values = guacamole_connect_arguments(
        session,
        [
            "color-depth",
            "force-lossless",
            "enable-wallpaper",
            "enable-theming",
            "enable-font-smoothing",
            "enable-full-window-drag",
            "enable-desktop-composition",
            "enable-menu-animations",
            "disable-bitmap-caching",
            "disable-offscreen-caching",
            "disable-glyph-caching",
            "disable-gfx",
        ],
    )

    assert values == expected


def test_guacamole_rdp_handshake_selects_rdp(monkeypatch: pytest.MonkeyPatch) -> None:
    reader = _FakeReader(
        [
            encode_instruction(
                "args",
                guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
                "hostname",
                "port",
                "username",
                "password",
                "domain",
                "security",
                "ignore-cert",
            ).encode("utf-8"),
            encode_instruction("ready", "uuid-rdp").encode("utf-8"),
        ]
    )
    writer = _FakeWriter()
    monkeypatch.setattr(guacamole_proxy, "_GUACD_BACKEND_VERIFY_SECONDS", 0)
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.42",
        port=3389,
        password="domain-password",
        created_at=0,
        expires_at=120,
        protocol="rdp",
        username="nicole",
        domain="LAB",
        security="nla",
        ignore_cert=True,
    )

    uuid_value, _pending, _parser = asyncio.run(
        guacamole_proxy._handshake_guacd(reader=reader, writer=writer, session=session)
    )

    assert uuid_value == "uuid-rdp"
    handshake = GuacamoleProtocolParser().feed(b"".join(writer.writes).decode("utf-8"))
    assert handshake[0] == ("select", ["rdp"])
    assert next(args for opcode, args in handshake if opcode == "connect") == [
        guacamole_proxy.GUACAMOLE_PROTOCOL_VERSION,
        "10.255.0.42",
        "3389",
        "nicole",
        "domain-password",
        "LAB",
        "nla",
        "true",
    ]


def test_guacamole_image_mimetypes_follow_performance_preference() -> None:
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-2) == ("image/jpeg", "image/webp", "image/png")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-1) == ("image/jpeg", "image/webp", "image/png")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(0) == ("image/png", "image/jpeg", "image/webp")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(1) == ("image/webp", "image/png", "image/jpeg")
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(2) == ("image/png", "image/webp", "image/jpeg")


def test_guacamole_image_mimetypes_honor_explicit_codec() -> None:
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-2, "jpeg") == ("image/jpeg",)
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(2, "png") == ("image/png",)
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-2, "jpg") == ("image/jpeg",)
    assert guacamole_proxy.guacamole_vnc_image_mimetypes(-2, "unknown") == ("image/jpeg", "image/webp", "image/png")


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


def test_guacamole_proxy_uses_explicit_jpeg_codec(monkeypatch: pytest.MonkeyPatch) -> None:
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
        image_codec="jpeg",
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
    assert image_args == ["image/jpeg"]


def test_guacamole_proxy_uses_explicit_png_codec(monkeypatch: pytest.MonkeyPatch) -> None:
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
        performance_preference=2,
        image_codec="png",
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
    assert image_args == ["image/png"]


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


class _CleanupPump:
    def __init__(self) -> None:
        self.started = asyncio.Event()
        self.finish = asyncio.Event()
        self.cleanup_started = asyncio.Event()
        self.release_cleanup = asyncio.Event()
        self.task = None
        self.exited = False
        self.failure = None
        self.cleanup_failure = False

    async def run(self) -> None:
        self.task = asyncio.current_task()
        self.started.set()
        try:
            await self.finish.wait()
            if self.failure == "cancel":
                raise asyncio.CancelledError
            if self.failure:
                raise RuntimeError("sensitive forwarding fixture")
        finally:
            self.cleanup_started.set()
            await self.release_cleanup.wait()
            self.exited = True
            if self.cleanup_failure:
                raise RuntimeError("sensitive cleanup fixture")


class _CleanupWriter(_FakeWriter):
    def __init__(self, failure=None) -> None:
        super().__init__()
        self.failure = failure
        self.close_started = asyncio.Event()
        self.release_close = asyncio.Event()
        self.close_finished = False
        self.close_cancelled = False

    def close(self) -> None:
        super().close()
        if self.failure == "close":
            raise RuntimeError("sensitive writer fixture")

    async def wait_closed(self) -> None:
        self.close_started.set()
        try:
            await self.release_close.wait()
        except asyncio.CancelledError:
            self.close_cancelled = True
            raise
        self.close_finished = True
        if self.failure == "wait":
            raise RuntimeError("sensitive writer fixture")
        if self.failure == "cancel":
            raise asyncio.CancelledError


def _cleanup_session():
    return GuacamoleVncSession(token="fixture", agent_id="agent-1", host="10.255.0.4", port=5900, password="fixture", created_at=0, expires_at=120)


def _cleanup_bridge(websocket):
    return guacamole_proxy.proxy_guacamole_vnc_session(websocket=websocket, session=_cleanup_session(), logger=logging.getLogger("test.guacamole.cleanup"), guacd_host="unused.invalid", guacd_port=4822)


async def _assert_cleanup_pending(task, message):
    done, _pending = await asyncio.wait([task], timeout=0.025)
    assert not done, message


@pytest.mark.parametrize("ending", ("outer_cancel", "browser_error", "server_error", "child_cancel", "child_cancelling", "cleanup_error", "normal"))
def test_guacamole_bridge_joins_owned_forwarders(monkeypatch, caplog, ending):
    caplog.set_level(logging.INFO, logger="test.guacamole.cleanup")

    async def exercise():
        browser, server = _CleanupPump(), _CleanupPump()
        writer = _FakeWriter()

        class Reader:
            async def read(self, _size):
                await server.run()
                return b""

        class WebSocket(_FakeWebSocket):
            async def __anext__(self):
                await browser.run()
                raise StopAsyncIteration

        async def open_ready(**_kwargs):
            return Reader(), writer, "fixture", []

        monkeypatch.setattr(guacamole_proxy, "_open_ready_guacd", open_ready)
        bridge = asyncio.create_task(_cleanup_bridge(WebSocket([])))
        barrier = None
        try:
            await asyncio.wait_for(asyncio.gather(browser.started.wait(), server.started.wait()), 1)
            if ending == "outer_cancel":
                bridge.cancel()
            elif ending == "child_cancelling":
                browser.task.cancel()
                await asyncio.wait_for(browser.cleanup_started.wait(), 1)
                bridge.cancel()
            else:
                first = server if ending == "server_error" else browser
                first.failure = "cancel" if ending == "child_cancel" else ("error" if ending.endswith("_error") and ending != "cleanup_error" else None)
                first.release_cleanup.set()
                first.finish.set()
                if ending == "cleanup_error":
                    server.cleanup_failure = True
            barrier = asyncio.ensure_future(asyncio.gather(browser.cleanup_started.wait(), server.cleanup_started.wait()))
            await asyncio.wait([bridge, barrier], timeout=1, return_when=asyncio.FIRST_COMPLETED)
            assert not bridge.done(), "bridge escaped with owned forwarding tasks running"
            assert barrier.done(), "bridge did not cancel both owned forwarding tasks"
            assert not writer.closed, "writer closed before forwarding cleanup joined"
            if ending in {"outer_cancel", "child_cancelling"}:
                bridge.cancel()
                await _assert_cleanup_pending(bridge, "repeated cancellation detached forwarding cleanup")
                assert not browser.exited and not server.exited
                assert browser.task.cancelling() == server.task.cancelling() == 1
            browser.release_cleanup.set()
            server.release_cleanup.set()
            if ending == "outer_cancel":
                with pytest.raises(asyncio.CancelledError):
                    await bridge
            elif ending == "normal":
                await bridge
            else:
                with pytest.raises(RuntimeError, match="^guacd_forwarding_failed$"):
                    await bridge
            assert browser.exited and server.exited and writer.closed
            assert browser.task.done() and server.task.done()
        finally:
            browser.finish.set()
            server.finish.set()
            browser.release_cleanup.set()
            server.release_cleanup.set()
            for task in (bridge, browser.task, server.task):
                if task is not None and not task.done():
                    task.cancel()
            await asyncio.gather(*(task for task in (bridge, browser.task, server.task, barrier) if task is not None), return_exceptions=True)

    asyncio.run(exercise())
    completed = [record for record in caplog.records if "bridge completed" in record.message]
    assert bool(completed) == (ending == "normal")
    assert "sensitive" not in caplog.text


@pytest.mark.parametrize("ending", ("normal", "outer_cancel", "close", "wait", "cancel"))
def test_guacamole_bridge_waits_for_owned_writer(monkeypatch, caplog, ending):
    caplog.set_level(logging.INFO, logger="test.guacamole.cleanup")

    async def exercise():
        writer = _CleanupWriter(ending)

        async def open_ready(**_kwargs):
            return _FakeReader([]), writer, "fixture", []

        monkeypatch.setattr(guacamole_proxy, "_open_ready_guacd", open_ready)
        bridge = asyncio.create_task(_cleanup_bridge(_FakeWebSocket([])))
        try:
            await asyncio.wait_for(writer.close_started.wait(), 1)
            await _assert_cleanup_pending(bridge, "bridge returned before writer cleanup")
            assert not any("bridge completed" in record.message for record in caplog.records)
            if ending == "outer_cancel":
                bridge.cancel()
                await _assert_cleanup_pending(bridge, "cancellation detached writer cleanup")
                bridge.cancel()
                await _assert_cleanup_pending(bridge, "repeated cancellation detached writer cleanup")
                assert not writer.close_cancelled
            writer.release_close.set()
            if ending == "outer_cancel":
                with pytest.raises(asyncio.CancelledError):
                    await bridge
            elif ending == "normal":
                await bridge
            else:
                with pytest.raises(RuntimeError, match="^guacd_cleanup_failed$"):
                    await bridge
            assert writer.closed and writer.close_finished
        finally:
            writer.release_close.set()
            await asyncio.gather(bridge, return_exceptions=True)

    asyncio.run(exercise())
    assert bool([record for record in caplog.records if "bridge completed" in record.message]) == (ending == "normal")
    assert "sensitive" not in caplog.text


def test_guacamole_handshake_cancellation_joins_current_writer(monkeypatch):
    async def exercise():
        started = asyncio.Event()
        writer = _CleanupWriter()

        class Reader:
            async def read(self, _size):
                started.set()
                await asyncio.Event().wait()

        async def open_connection(_host, _port):
            return Reader(), writer

        monkeypatch.setattr(guacamole_proxy.asyncio, "open_connection", open_connection)
        bridge = asyncio.create_task(_cleanup_bridge(_FakeWebSocket([])))
        try:
            await asyncio.wait_for(started.wait(), 1)
            bridge.cancel()
            await asyncio.wait_for(writer.close_started.wait(), 1)
            bridge.cancel()
            await _assert_cleanup_pending(bridge, "handshake cancellation detached owned writer")
            assert not writer.close_cancelled
            writer.release_close.set()
            with pytest.raises(asyncio.CancelledError):
                await bridge
            assert writer.close_finished
        finally:
            writer.release_close.set()
            await asyncio.gather(bridge, return_exceptions=True)

    asyncio.run(exercise())


def test_guacamole_failed_writer_cleanup_stops_retry(monkeypatch):
    attempts = []

    async def exercise():
        writer = _CleanupWriter("wait")
        writer.release_close.set()

        async def open_connection(_host, _port):
            attempts.append(True)
            return _FakeReader([encode_instruction("unexpected").encode()]), writer

        monkeypatch.setattr(guacamole_proxy.asyncio, "open_connection", open_connection)
        with pytest.raises(RuntimeError, match="^guacd_cleanup_failed$"):
            await _cleanup_bridge(_FakeWebSocket([]))
        assert writer.closed and writer.close_finished

    asyncio.run(exercise())
    assert attempts == [True], "retry started before prior writer cleanup was proven"


@pytest.mark.parametrize("cleanup_fails", (False, True))
def test_guacamole_cleanup_preserves_websocket_close_after_join(monkeypatch, cleanup_fails):
    from websockets.exceptions import ConnectionClosedError
    from websockets.frames import Close
    from Data.Engine.services.RemoteDesktop import vnc_proxy

    async def exercise():
        writer = _CleanupWriter("wait" if cleanup_fails else None)
        closed = []
        callbacks = []
        registry = guacamole_proxy.GuacamoleSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.guac.registry"))
        session = registry.create(agent_id="agent-1", host="10.255.0.4", port=5900, password="fixture", on_close=lambda reason: callbacks.append((reason, writer.close_finished)))
        proxy = vnc_proxy.VncProxyServer(host="unused.invalid", port=4823, guacamole_registry=registry, logger=logging.getLogger("test.guacamole.cleanup"))

        class WebSocket(_FakeWebSocket):
            close_reason = None

            async def __anext__(self):
                raise ConnectionClosedError(Close(1001, "fixture close"), Close(1001, "fixture close"), True)

            async def close(self, *, code, reason):
                closed.append((code, reason))

        async def open_ready(**_kwargs):
            return _FakeReader([]), writer, "fixture", []

        monkeypatch.setattr(guacamole_proxy, "_open_ready_guacd", open_ready)
        handle = asyncio.create_task(proxy._handle_client(WebSocket([]), f"/remote-desktop/vnc/guacamole?token={session.token}"))
        try:
            await asyncio.wait_for(writer.close_started.wait(), 1)
            await _assert_cleanup_pending(handle, "session callback escaped writer cleanup")
            assert callbacks == [] and closed == []
            writer.release_close.set()
            await handle
            assert writer.closed and writer.close_finished
            assert callbacks == [("guacamole_unavailable" if cleanup_fails else "fixture close", True)]
            assert closed == ([(1011, "guacamole_unavailable")] if cleanup_fails else [])
        finally:
            writer.release_close.set()
            await asyncio.gather(handle, return_exceptions=True)

    asyncio.run(exercise())
