from __future__ import annotations

import asyncio

import Data.Agent.agent as agent_module


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

