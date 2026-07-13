# ======================================================
# Data\Engine\Unit_Tests\test_vnc_proxy.py
# Description: Validates Guacamole VNC proxy behavior.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import asyncio
import logging

from Data.Engine.services.RemoteDesktop import vnc_proxy
from Data.Engine.services.RemoteDesktop.guacamole_proxy import GuacdBackendRetryableError, GuacamoleSessionRegistry


def _build_proxy() -> vnc_proxy.VncProxyServer:
    registry = GuacamoleSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.guac.registry"))
    return vnc_proxy.VncProxyServer(
        host="127.0.0.1",
        port=4823,
        guacamole_registry=registry,
        logger=logging.getLogger("test.vnc.proxy"),
    )


class _FakeWebSocket:
    def __init__(self) -> None:
        self.closed: tuple[int, str] | None = None

    async def close(self, code: int, reason: str) -> None:
        self.closed = (code, reason)


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

    assert registry.lookup(session.token) is not None
    assert registry.consume(session.token) is not None
    assert registry.consume(session.token) is None
    assert registry.lookup(session.token) is None


def test_guacamole_registry_revoke_removes_token() -> None:
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

    assert registry.revoke(session.token) is True
    assert registry.lookup(session.token) is None
    assert registry.revoke(session.token) is False


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


def test_vnc_proxy_rejects_raw_vnc_path() -> None:
    proxy = _build_proxy()
    websocket = _FakeWebSocket()

    asyncio.run(proxy._handle_client(websocket, "/remote-desktop/vnc?token=abc"))

    assert websocket.closed == (1008, "guacamole_required")


def test_vnc_proxy_keeps_token_when_bridge_fails_before_transport_confirm(monkeypatch) -> None:
    registry = GuacamoleSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.guac.registry"))
    proxy = vnc_proxy.VncProxyServer(
        host="127.0.0.1",
        port=4823,
        guacamole_registry=registry,
        logger=logging.getLogger("test.vnc.proxy"),
    )
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

    async def _fail_before_confirm(**_kwargs):
        raise RuntimeError("backend_down")

    monkeypatch.setattr(vnc_proxy, "proxy_guacamole_vnc_session", _fail_before_confirm)
    websocket = _FakeWebSocket()

    asyncio.run(proxy._handle_client(websocket, f"/remote-desktop/vnc/guacamole?token={session.token}"))

    assert registry.lookup(session.token) is session
    assert websocket.closed == (1011, "guacamole_unavailable")


def test_vnc_proxy_reports_retryable_backend_error_as_auth_failure(monkeypatch) -> None:
    registry = GuacamoleSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.guac.registry"))
    close_reasons: list[str] = []
    proxy = vnc_proxy.VncProxyServer(
        host="127.0.0.1",
        port=4823,
        guacamole_registry=registry,
        logger=logging.getLogger("test.vnc.proxy"),
    )
    session = registry.create(
        agent_id="agent-1",
        host="10.255.0.6",
        port=5900,
        password="secretpw",
        operator_id="admin",
        session_id="session-1",
        participant_id="participant-1",
        role="controller",
        on_close=lambda reason: close_reasons.append(reason),
    )

    async def _fail_with_retryable_backend(**_kwargs):
        raise GuacdBackendRetryableError("519", "Aborted. See logs.")

    monkeypatch.setattr(vnc_proxy, "proxy_guacamole_vnc_session", _fail_with_retryable_backend)
    websocket = _FakeWebSocket()

    asyncio.run(proxy._handle_client(websocket, f"/remote-desktop/vnc/guacamole?token={session.token}"))

    assert registry.lookup(session.token) is session
    assert websocket.closed == (1011, "vnc_auth_failed")
    assert close_reasons == ["vnc_auth_failed"]


def test_vnc_proxy_revokes_token_after_transport_confirm(monkeypatch) -> None:
    registry = GuacamoleSessionRegistry(ttl_seconds=120, logger=logging.getLogger("test.guac.registry"))
    proxy = vnc_proxy.VncProxyServer(
        host="127.0.0.1",
        port=4823,
        guacamole_registry=registry,
        logger=logging.getLogger("test.vnc.proxy"),
    )
    confirmations: list[str] = []
    session = registry.create(
        agent_id="agent-1",
        host="10.255.0.6",
        port=5900,
        password="secretpw",
        operator_id="admin",
        session_id="session-1",
        participant_id="participant-1",
        role="controller",
        confirm_transport=lambda reason: confirmations.append(reason),
    )

    async def _confirm_transport(**kwargs):
        kwargs["session"].confirm_transport("vnc_backend_connect")

    monkeypatch.setattr(vnc_proxy, "proxy_guacamole_vnc_session", _confirm_transport)
    websocket = _FakeWebSocket()

    asyncio.run(proxy._handle_client(websocket, f"/remote-desktop/vnc/guacamole?token={session.token}"))

    assert registry.lookup(session.token) is None
    assert confirmations == ["vnc_backend_connect"]


def test_ensure_guacamole_vnc_proxy_creates_shared_registry(monkeypatch) -> None:
    started: list[bool] = []

    def _fake_ensure_started(self):
        started.append(True)
        return True

    monkeypatch.setattr(vnc_proxy.VncProxyServer, "ensure_started", _fake_ensure_started)

    class _Context:
        logger = logging.getLogger("test.context")
        vnc_session_ttl_seconds = 120
        vnc_ws_host = "127.0.0.1"
        vnc_ws_port = 4823
        guacamole_vnc_ws_path = "/remote-desktop/vnc/guacamole"
        guacd_host = "127.0.0.1"
        guacd_port = 4822

    context = _Context()

    registry = vnc_proxy.ensure_guacamole_vnc_proxy(context, logger=context.logger)

    assert isinstance(registry, GuacamoleSessionRegistry)
    assert context.guacamole_vnc_registry is registry
    assert started == [True]
