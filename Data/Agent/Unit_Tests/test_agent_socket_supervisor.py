from __future__ import annotations

import asyncio
import os
from pathlib import Path

os.environ.setdefault("BOREALIS_AGENT_MODE", "system")

import Data.Agent.agent as agent_module


class _RegistrationCaptureClient:
    def __init__(self, reconnection=False) -> None:
        self.reconnection = reconnection
        self.registrations = []

    def event(self, handler):
        self.registrations.append(("event", handler.__name__))
        return handler

    def on(self, event_name, handler=None, namespace=None):
        if handler is not None:
            self.registrations.append(("on", event_name, handler.__name__))
            return handler

        def _decorator(decorated):
            self.registrations.append(("on", event_name, decorated.__name__))
            return decorated

        return _decorator


class _FakeClient:
    def __init__(self) -> None:
        self.guid = "guid-1"
        self.access_token = "access-token"
        self.refresh_token = "refresh-token"
        self.access_expires_at = 1234567890
        self.session = type("Session", (), {"verify": True})()
        self.ensure_calls = 0
        self.configure_calls = 0

    def ensure_authenticated(self) -> None:
        self.ensure_calls += 1

    def configure_socketio(self, client) -> None:
        self.configure_calls += 1

    @staticmethod
    def websocket_base_url() -> str:
        return "https://borealis.example.invalid"

    @staticmethod
    def auth_headers():
        return {"Authorization": "Bearer access-token"}

    @staticmethod
    def socketio_ssl_params():
        return {}


class _FakeSio:
    def __init__(self) -> None:
        self.connected = False
        self.connection_error = None
        self.sid = "fake-sid"
        self.transport = "websocket"
        self.connect_calls = 0
        self.wait_calls = 0
        self.last_headers = None

    async def connect(self, url, transports=None, headers=None, **kwargs) -> None:
        self.connect_calls += 1
        self.last_headers = headers
        self.connected = True

    async def wait(self) -> None:
        self.wait_calls += 1
        self.connected = False
        raise asyncio.CancelledError()


def test_deferred_socketio_client_replays_import_time_handlers() -> None:
    proxy = agent_module._SocketIORegistrationProxy()

    @proxy.event
    async def connect():
        return None

    @proxy.on("agent_config")
    async def on_agent_config(payload):
        return payload

    created_clients = []
    original_sio = agent_module.sio
    original_registrations = agent_module._SOCKETIO_REGISTRATIONS
    original_async_client = agent_module.socketio.AsyncClient
    agent_module.sio = proxy
    agent_module._SOCKETIO_REGISTRATIONS = proxy
    agent_module.socketio.AsyncClient = lambda reconnection=False: created_clients.append(
        _RegistrationCaptureClient(reconnection=reconnection)
    ) or created_clients[-1]

    try:
        client = agent_module._create_socketio_client(loop_ref=object())
    finally:
        agent_module.sio = original_sio
        agent_module._SOCKETIO_REGISTRATIONS = original_registrations
        agent_module.socketio.AsyncClient = original_async_client

    assert client.reconnection is False
    assert ("event", "connect") in client.registrations
    assert ("on", "agent_config", "on_agent_config") in client.registrations


def test_connect_loop_waits_for_socket_end_after_successful_connect() -> None:
    fake_client = _FakeClient()
    fake_sio = _FakeSio()
    log_messages: list[str] = []

    original_sio = agent_module.sio
    original_http_client = agent_module.http_client
    original_log_agent = agent_module._log_agent

    agent_module.sio = fake_sio
    agent_module.http_client = lambda: fake_client
    agent_module._log_agent = lambda message, **kwargs: log_messages.append(str(message))

    try:
        try:
            asyncio.run(agent_module.connect_loop())
        except asyncio.CancelledError:
            pass
    finally:
        agent_module.sio = original_sio
        agent_module.http_client = original_http_client
        agent_module._log_agent = original_log_agent

    assert fake_client.ensure_calls == 1
    assert fake_client.configure_calls == 1
    assert fake_sio.connect_calls == 1
    assert fake_sio.wait_calls == 1
    assert fake_sio.last_headers == {"Authorization": "Bearer access-token"}
    assert any("starting authentication phase" in message for message in log_messages)
    assert any("sio.connect completed successfully" in message for message in log_messages)


def test_periodic_startup_telemetry_refresh_flushes_status(monkeypatch) -> None:
    calls = []

    monkeypatch.setattr(agent_module, "SYSTEM_SERVICE_MODE", True)
    monkeypatch.setattr(
        agent_module,
        "_heartbeat_complete",
        lambda key, detail, **kwargs: calls.append(("complete", key, detail)),
    )
    monkeypatch.setattr(
        agent_module,
        "_heartbeat_flush",
        lambda reason="": calls.append(("flush", reason)) or True,
    )

    assert agent_module._refresh_startup_telemetry_once("periodic_agent_health") is True
    assert calls == [
        ("complete", "steady_state_online", "Periodic agent health refresh accepted."),
        ("flush", "periodic_agent_health"),
    ]


def test_get_server_url_requires_public_https_fqdn(monkeypatch, tmp_path: Path) -> None:
    settings_dir = tmp_path / "Agent" / "Borealis" / "Settings"
    settings_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.setattr(agent_module, "_settings_dir", lambda: str(settings_dir))
    monkeypatch.delenv("BOREALIS_SERVER_URL", raising=False)

    (settings_dir / "server_url.txt").write_text("https://borealis.example.com/\n", encoding="utf-8")
    assert agent_module.get_server_url() == "https://borealis.example.com"

    (settings_dir / "server_url.txt").write_text("https://192.168.3.252:5000\n", encoding="utf-8")
    assert agent_module.get_server_url() == ""

    (settings_dir / "server_url.txt").write_text("https://localhost:5000\n", encoding="utf-8")
    assert agent_module.get_server_url() == ""
