from __future__ import annotations

import os

os.environ.setdefault("BOREALIS_AGENT_MODE", "system")

import Data.Agent.agent as agent_module


class _FakeTrayState:
    RESTART_REQUEST_TTL_SECONDS = 120

    def __init__(self) -> None:
        self.payload = {
            "request_id": "restart-1",
            "requested_at": 1000,
            "service_mode": "system",
        }
        self.cleared: list[str] = []

    def load_restart_request(self, service_mode: str):
        if service_mode != "system":
            return {}
        return dict(self.payload)

    def clear_restart_request(self, service_mode: str) -> None:
        self.cleared.append(service_mode)


def test_process_tray_restart_request_consumes_own_scope(monkeypatch) -> None:
    fake_tray_state = _FakeTrayState()
    calls: list[str] = []

    monkeypatch.setattr(agent_module, "_tray_state", fake_tray_state)
    monkeypatch.setattr(agent_module, "SERVICE_MODE", "system")
    monkeypatch.setattr(agent_module, "_launch_replacement_agent_process", lambda mode: calls.append(mode) or True)
    monkeypatch.setattr(agent_module, "_shutdown_for_tray_restart", lambda: calls.append("shutdown"))
    monkeypatch.setattr(agent_module.time, "time", lambda: 1005)

    processed = agent_module._process_tray_restart_request_now()

    assert processed is True
    assert fake_tray_state.cleared == ["system"]
    assert calls == ["system", "shutdown"]
