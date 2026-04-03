from __future__ import annotations

import Data.Agent.Roles.role_VNC as vnc_role


class _CompletedProcess:
    def __init__(self, *, returncode: int, stderr: str = "") -> None:
        self.returncode = returncode
        self.stderr = stderr


def test_ensure_firewall_success_is_silent(monkeypatch) -> None:
    logs: list[str] = []
    calls: list[list[str]] = []
    manager = vnc_role.VncManager()

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(manager, "_normalize_firewall_remote", lambda _allowed_ips: "10.255.0.1/32")
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))

    def _fake_run(command, capture_output, text, check):
        calls.append(command)
        return _CompletedProcess(returncode=0)

    monkeypatch.setattr(vnc_role.subprocess, "run", _fake_run)

    manager._ensure_firewall("10.255.0.1/32", 5900)

    assert calls
    assert logs == []


def test_ensure_firewall_failure_still_logs(monkeypatch) -> None:
    logs: list[str] = []
    manager = vnc_role.VncManager()

    monkeypatch.setattr(vnc_role.os, "name", "nt", raising=False)
    monkeypatch.setattr(manager, "_normalize_firewall_remote", lambda _allowed_ips: "10.255.0.1/32")
    monkeypatch.setattr(vnc_role, "_write_log", lambda message: logs.append(message))
    monkeypatch.setattr(
        vnc_role.subprocess,
        "run",
        lambda _command, capture_output, text, check: _CompletedProcess(
            returncode=1,
            stderr="boom",
        ),
    )

    manager._ensure_firewall("10.255.0.1/32", 5900)

    assert logs == ["Failed to ensure VNC firewall rule: boom"]
