# ======================================================
# Data\Engine\Unit_Tests\test_vnc_proxy.py
# Description: Validates VNC backend connect recovery and transport confirmation behavior.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import asyncio
import logging

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
