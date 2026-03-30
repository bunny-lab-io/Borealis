# ======================================================
# Data\Engine\Unit_Tests\test_vpn_shell.py
# Description: Validates VPN shell transport metadata and low-latency socket tuning.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import json
import logging
import socket

from Data.Engine.services.WebSocket.vpn_shell import ShellSession, VpnShellBridge


class _DummySocket:
    def __init__(self) -> None:
        self.sent: list[bytes] = []
        self.sockopts: list[tuple[int, int, int]] = []
        self.timeout = None

    def sendall(self, data: bytes) -> None:
        self.sent.append(data)

    def setsockopt(self, level: int, option: int, value: int) -> None:
        self.sockopts.append((level, option, value))

    def settimeout(self, value: float) -> None:
        self.timeout = value

    def fileno(self) -> int:
        return 1

    def close(self) -> None:
        return


class _DummySocketIO:
    def __init__(self) -> None:
        self.emits: list[tuple[tuple, dict]] = []

    def emit(self, *args, **kwargs) -> None:
        self.emits.append((args, kwargs))

    def start_background_task(self, _target, *_args, **_kwargs):
        return None


class _DummyTunnelService:
    def status(self, _agent_id: str):
        return {"virtual_ip": "10.255.0.20/32"}

    def request_agent_start(self, *_args, **_kwargs):
        return {"status": "ok"}

    def bump_activity(self, _agent_id: str) -> None:
        return


def test_shell_session_send_includes_message_metadata() -> None:
    tcp = _DummySocket()
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=_DummySocketIO(),
        tcp=tcp,
    )

    meta = session.send("hostname\r\n")

    assert meta["send_failed"] is False
    payload = json.loads(tcp.sent[0].decode("utf-8").strip())
    assert payload["type"] == "stdin"
    assert payload["message_id"] == meta["message_id"]
    assert payload["sent_at_ms"] == meta["sent_at_ms"]
    assert isinstance(payload["sent_at_ms"], int)


def test_open_session_enables_tcp_nodelay(monkeypatch) -> None:
    tcp = _DummySocket()
    socketio = _DummySocketIO()
    context = type(
        "Ctx",
        (),
        {
            "logger": logging.getLogger("test.vpn_shell"),
            "wireguard_shell_port": 47002,
            "vpn_tunnel_service": _DummyTunnelService(),
        },
    )()
    bridge = VpnShellBridge(socketio, context)

    monkeypatch.setattr(socket, "create_connection", lambda *_args, **_kwargs: tcp)

    session = bridge.open_session("sid-1", "agent-1")

    assert session is not None
    assert (socket.IPPROTO_TCP, socket.TCP_NODELAY, 1) in tcp.sockopts
    assert tcp.timeout == 15
