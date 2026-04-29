# ======================================================
# Data\Engine\Unit_Tests\test_vnc_proxy.py
# Description: Validates VNC backend connect recovery and transport confirmation behavior.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import asyncio
import logging

from Data.Engine.services.RemoteDesktop.guacamole_proxy import GuacamoleSessionRegistry
from Data.Engine.services.RemoteDesktop import vnc_proxy


class _FakeReader:
    pass


class _FakeWriter:
    pass


def _build_proxy() -> vnc_proxy.VncProxyServer:
    registry = vnc_proxy.VncSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.vnc.registry"))
    return vnc_proxy.VncProxyServer(
        host="127.0.0.1",
        port=4823,
        registry=registry,
        logger=logging.getLogger("test.vnc.proxy"),
    )


def test_connect_vnc_immediate_success_confirms_transport_without_recovery(monkeypatch) -> None:
    proxy = _build_proxy()
    restart_reasons: list[str] = []
    confirm_reasons: list[str] = []
    session = vnc_proxy.VncSession(
        token="token-1",
        agent_id="agent-1",
        host="10.255.0.3",
        port=5900,
        created_at=0.0,
        expires_at=120.0,
        restart_tunnel=lambda reason: restart_reasons.append(reason),
        confirm_transport=lambda reason: confirm_reasons.append(reason),
    )

    async def _fake_open_connection(host: str, port: int):
        assert host == "10.255.0.3"
        assert port == 5900
        return _FakeReader(), _FakeWriter()

    async def _fake_wait_for(awaitable, timeout: float):
        _ = timeout
        return await awaitable

    monkeypatch.setattr(vnc_proxy.asyncio, "open_connection", _fake_open_connection)
    monkeypatch.setattr(vnc_proxy.asyncio, "wait_for", _fake_wait_for)
    monkeypatch.setattr(vnc_proxy.time, "monotonic", lambda: 0.0)

    reader, writer = asyncio.run(proxy._connect_vnc(session))

    assert isinstance(reader, _FakeReader)
    assert isinstance(writer, _FakeWriter)
    assert restart_reasons == []
    assert confirm_reasons == ["vnc_backend_connect"]


def test_connect_vnc_requests_single_recovery_after_repeated_failures(monkeypatch) -> None:
    proxy = _build_proxy()
    restart_reasons: list[str] = []
    confirm_reasons: list[str] = []
    session = vnc_proxy.VncSession(
        token="token-2",
        agent_id="agent-2",
        host="10.255.0.4",
        port=5900,
        created_at=0.0,
        expires_at=120.0,
        restart_tunnel=lambda reason: restart_reasons.append(reason),
        confirm_transport=lambda reason: confirm_reasons.append(reason),
    )

    attempts = {"count": 0}

    async def _fake_open_connection(host: str, port: int):
        assert host == "10.255.0.4"
        assert port == 5900
        attempts["count"] += 1
        if attempts["count"] < 4:
            raise ConnectionRefusedError("not ready")
        return _FakeReader(), _FakeWriter()

    async def _fake_wait_for(awaitable, timeout: float):
        _ = timeout
        return await awaitable

    async def _fake_sleep(delay: float):
        _ = delay
        return None

    monkeypatch.setattr(vnc_proxy.asyncio, "open_connection", _fake_open_connection)
    monkeypatch.setattr(vnc_proxy.asyncio, "wait_for", _fake_wait_for)
    monkeypatch.setattr(vnc_proxy.asyncio, "sleep", _fake_sleep)
    monkeypatch.setattr(vnc_proxy.time, "monotonic", lambda: 0.0)
    monkeypatch.setattr(vnc_proxy, "_CONNECT_RECOVERY_AFTER_SECONDS", 0.0)

    reader, writer = asyncio.run(proxy._connect_vnc(session))

    assert isinstance(reader, _FakeReader)
    assert isinstance(writer, _FakeWriter)
    assert attempts["count"] == 4
    assert restart_reasons == ["vnc_connect_retry"]
    assert confirm_reasons == ["vnc_backend_connect"]


def test_connect_vnc_suppresses_duplicate_recovery_for_same_agent_within_cooldown(monkeypatch) -> None:
    proxy = _build_proxy()
    restart_reasons_one: list[str] = []
    restart_reasons_two: list[str] = []

    session_one = vnc_proxy.VncSession(
        token="token-3",
        agent_id="agent-3",
        host="10.255.0.5",
        port=5900,
        created_at=0.0,
        expires_at=120.0,
        restart_tunnel=lambda reason: restart_reasons_one.append(reason),
        confirm_transport=lambda _reason: None,
    )
    session_two = vnc_proxy.VncSession(
        token="token-4",
        agent_id="agent-3",
        host="10.255.0.5",
        port=5900,
        created_at=0.0,
        expires_at=120.0,
        restart_tunnel=lambda reason: restart_reasons_two.append(reason),
        confirm_transport=lambda _reason: None,
    )

    attempts = {"count": 0}

    async def _fake_open_connection(host: str, port: int):
        assert host == "10.255.0.5"
        assert port == 5900
        attempts["count"] += 1
        if attempts["count"] in {1, 2}:
            raise ConnectionRefusedError("not ready")
        return _FakeReader(), _FakeWriter()

    async def _fake_wait_for(awaitable, timeout: float):
        _ = timeout
        return await awaitable

    async def _fake_sleep(delay: float):
        _ = delay
        return None

    monkeypatch.setattr(vnc_proxy.asyncio, "open_connection", _fake_open_connection)
    monkeypatch.setattr(vnc_proxy.asyncio, "wait_for", _fake_wait_for)
    monkeypatch.setattr(vnc_proxy.asyncio, "sleep", _fake_sleep)
    monkeypatch.setattr(vnc_proxy.time, "monotonic", lambda: 0.0)
    monkeypatch.setattr(vnc_proxy, "_CONNECT_RECOVERY_AFTER_SECONDS", 0.0)
    monkeypatch.setattr(vnc_proxy, "_CONNECT_RECOVERY_COOLDOWN_SECONDS", 60.0)

    asyncio.run(proxy._connect_vnc(session_one))
    asyncio.run(proxy._connect_vnc(session_two))

    assert restart_reasons_one == ["vnc_connect_retry"]
    assert restart_reasons_two == []


def test_guacamole_registry_consumes_tokens_once() -> None:
    registry = GuacamoleSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.guac.registry"))

    session = registry.create(
        agent_id="agent-1",
        host="10.255.0.6",
        port=5900,
        password="secretpw",
        operator_id="admin",
        session_id="session-1",
        participant_id="participant-1",
        role="controller",
    )

    assert registry.consume(session.token) is not None
    assert registry.consume(session.token) is None


def test_vnc_proxy_accepts_guacamole_websocket_subprotocol(monkeypatch) -> None:
    proxy = _build_proxy()
    captured: dict[str, object] = {}

    class _FakeServer:
        async def wait_closed(self) -> None:
            return None

    async def _fake_serve(handler, host: str, port: int, **kwargs):
        captured["handler"] = handler
        captured["host"] = host
        captured["port"] = port
        captured.update(kwargs)
        return _FakeServer()

    monkeypatch.setattr(vnc_proxy.websockets, "serve", _fake_serve)

    asyncio.run(proxy._serve())

    assert captured["subprotocols"] == [vnc_proxy.GUACAMOLE_WEBSOCKET_SUBPROTOCOL]
