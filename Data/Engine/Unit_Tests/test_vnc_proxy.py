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


def test_spectator_input_filter_strips_keyboard_pointer_and_clipboard_messages() -> None:
    spectator_filter = vnc_proxy.SpectatorClientInputFilter()

    version = b"RFB 003.008\n"
    security_type = b"\x02"
    auth_response = b"a" * 16
    client_init = b"\x01"
    set_pixel_format = bytes([0]) + (b"\x00" * 19)
    framebuffer_update = bytes([3, 0, 0, 0, 0, 0, 0, 10, 0, 10])
    key_event = bytes([4, 1, 0, 0, 0, 0, 0, 65])
    pointer_event = bytes([5, 0, 0, 5, 0, 5])
    client_cut_text = bytes([6, 0, 0, 0, 0, 0, 0, 4]) + b"test"

    first_pass = spectator_filter.filter(version[:8])
    second_pass = spectator_filter.filter(
        version[8:]
        + security_type
        + auth_response
        + client_init
        + set_pixel_format
        + key_event
        + framebuffer_update
        + pointer_event
        + client_cut_text
    )

    filtered = first_pass + second_pass
    assert version in filtered
    assert security_type in filtered
    assert auth_response in filtered
    assert client_init in filtered
    assert set_pixel_format in filtered
    assert framebuffer_update in filtered
    assert key_event not in filtered
    assert pointer_event not in filtered
    assert client_cut_text not in filtered
