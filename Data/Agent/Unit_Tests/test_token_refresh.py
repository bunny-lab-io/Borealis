from __future__ import annotations

import os
import threading
from typing import Any

import pytest
import requests

os.environ.setdefault("BOREALIS_AGENT_MODE", "system")

import Data.Agent.agent as agent_module


class _FakeResponse:
    def __init__(self, status_code: int, *, json_payload: dict[str, Any] | None = None, text: str = "") -> None:
        self.status_code = status_code
        self._json_payload = json_payload
        self.text = text

    def json(self) -> dict[str, Any]:
        if self._json_payload is None:
            raise ValueError("no json payload")
        return self._json_payload

    @property
    def content(self) -> bytes:
        return self.text.encode("utf-8")

    def raise_for_status(self) -> None:
        if self.status_code >= 400:
            raise requests.HTTPError(f"HTTP {self.status_code}", response=self)


class _FakeSession:
    def __init__(self, responses: list[_FakeResponse]) -> None:
        self._responses = list(responses)
        self.headers: dict[str, str] = {}
        self.calls = 0

    def post(self, *_args, **_kwargs) -> _FakeResponse:
        self.calls += 1
        if not self._responses:
            raise AssertionError("unexpected post call")
        return self._responses.pop(0)


class _FakeKeyStore:
    def __init__(self) -> None:
        self.saved_access_tokens: list[tuple[str, int]] = []
        self.bindings: list[str] = []

    def save_access_token(self, access_token: str, *, expires_at: int) -> None:
        self.saved_access_tokens.append((access_token, expires_at))

    def set_access_binding(self, binding: str) -> None:
        self.bindings.append(binding)


def _make_client(session: _FakeSession, store: _FakeKeyStore) -> agent_module.AgentHttpClient:
    client = agent_module.AgentHttpClient.__new__(agent_module.AgentHttpClient)
    client.session = session
    client.key_store = store
    client.guid = "54E8C9E2-6B3D-4B51-A456-4ACB94C45F00"
    client.refresh_token = "refresh-token"
    client.access_token = "old-access-token"
    client.access_expires_at = 0
    client.base_url = "https://borealis.example.invalid"
    client._auth_lock = threading.RLock()
    client.auth_headers = lambda: {}
    client._reload_tokens_from_disk = lambda: None
    client._clear_tokens_locked = lambda: (_ for _ in ()).throw(AssertionError("unexpected clear"))
    client._perform_enrollment_locked = lambda: (_ for _ in ()).throw(AssertionError("unexpected enrollment"))
    return client


def test_refresh_access_token_retries_transient_502_then_succeeds(monkeypatch) -> None:
    session = _FakeSession(
        [
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(200, json_payload={"access_token": "new-access-token", "expires_in": 900}),
        ]
    )
    store = _FakeKeyStore()
    client = _make_client(session, store)
    client.access_expires_at = 150
    sleep_calls: list[float] = []
    log_messages: list[str] = []

    monkeypatch.setattr(agent_module.time, "sleep", lambda seconds: sleep_calls.append(seconds))
    monkeypatch.setattr(agent_module.time, "time", lambda: 100.0)
    monkeypatch.setattr(agent_module, "_log_agent", lambda message, **kwargs: log_messages.append(str(message)))

    client._refresh_access_token_locked()

    assert session.calls == 2
    assert sleep_calls == [1.0]
    assert client.access_token == "new-access-token"
    assert store.saved_access_tokens and store.saved_access_tokens[-1][0] == "new-access-token"
    assert any("retrying" in message for message in log_messages)
    assert any("recovered" in message for message in log_messages)


def test_refresh_access_token_keeps_existing_token_during_transient_outage(monkeypatch) -> None:
    session = _FakeSession(
        [
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(502, text="Bad Gateway"),
        ]
    )
    store = _FakeKeyStore()
    client = _make_client(session, store)
    client.access_token = "still-valid-token"
    client.access_expires_at = 200
    sleep_calls: list[float] = []
    log_messages: list[str] = []

    monkeypatch.setattr(agent_module.time, "sleep", lambda seconds: sleep_calls.append(seconds))
    monkeypatch.setattr(agent_module.time, "time", lambda: 100.0)
    monkeypatch.setattr(agent_module, "_log_agent", lambda message, **kwargs: log_messages.append(str(message)))

    client._refresh_access_token_locked()

    assert session.calls == 4
    assert sleep_calls == [1.0, 2.0, 5.0]
    assert client.access_token == "still-valid-token"
    assert not store.saved_access_tokens
    assert any("continuing with current access token" in message for message in log_messages)


def test_refresh_access_token_raises_after_transient_outage_when_token_expired(monkeypatch) -> None:
    session = _FakeSession(
        [
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(502, text="Bad Gateway"),
            _FakeResponse(502, text="Bad Gateway"),
        ]
    )
    store = _FakeKeyStore()
    client = _make_client(session, store)
    client.access_token = "expired-token"
    client.access_expires_at = 100

    monkeypatch.setattr(agent_module.time, "sleep", lambda _seconds: None)
    monkeypatch.setattr(agent_module.time, "time", lambda: 100.0)
    monkeypatch.setattr(agent_module, "_log_agent", lambda *_args, **_kwargs: None)

    with pytest.raises(requests.HTTPError):
        client._refresh_access_token_locked()
