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
from Data.Engine.services.RemoteDesktop.guacamole_proxy import GuacamoleSessionRegistry


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


def test_vnc_proxy_rejects_raw_vnc_path() -> None:
    proxy = _build_proxy()
    websocket = _FakeWebSocket()

    asyncio.run(proxy._handle_client(websocket, "/remote-desktop/vnc?token=abc"))

    assert websocket.closed == (1008, "guacamole_required")


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
