"""Tests covering the agent WebSocket event handlers."""

from __future__ import annotations

import inspect

from Data.Engine.interfaces.ws.agents import events


def test_on_connect_accepts_optional_auth_argument() -> None:
    signature = inspect.signature(events._AgentEventHandlers.on_connect)
    parameters = list(signature.parameters.values())
    assert parameters[0].name == "self"
    assert parameters[1].name == "auth"
    assert parameters[1].default is None
