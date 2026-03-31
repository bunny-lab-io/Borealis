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
import sys
import threading
import time
import types

from Data.Engine.services.WebSocket.vpn_shell import ShellSession, VpnShellBridge


class _DummySocket:
    def __init__(self) -> None:
        self.sent: list[bytes] = []
        self.sockopts: list[tuple[int, int, int]] = []
        self.timeout = None
        self.closed = False

    def sendall(self, data: bytes) -> None:
        self.sent.append(data)

    def recv(self, _size: int) -> bytes:
        time.sleep(0.01)
        raise socket.timeout()

    def setsockopt(self, level: int, option: int, value: int) -> None:
        self.sockopts.append((level, option, value))

    def settimeout(self, value: float) -> None:
        self.timeout = value

    def fileno(self) -> int:
        return -1 if self.closed else 1

    def close(self) -> None:
        self.closed = True


class _PongSocket(_DummySocket):
    def __init__(self) -> None:
        super().__init__()
        self._recv_queue: list[bytes] = []
        self._recv_lock = threading.Lock()

    def sendall(self, data: bytes) -> None:
        super().sendall(data)
        try:
            payload = json.loads(data.decode("utf-8").strip())
        except Exception:
            return
        if payload.get("type") != "ping":
            return
        pong = {
            "type": "pong",
            "ping_id": payload.get("ping_id"),
            "sent_at_ms": payload.get("sent_at_ms"),
            "agent_received_at_ms": payload.get("sent_at_ms"),
            "agent_pong_at_ms": payload.get("sent_at_ms"),
        }
        with self._recv_lock:
            self._recv_queue.append(json.dumps(pong).encode("utf-8") + b"\n")

    def recv(self, _size: int) -> bytes:
        deadline = time.time() + 0.5
        while time.time() < deadline:
            with self._recv_lock:
                if self._recv_queue:
                    return self._recv_queue.pop(0)
            time.sleep(0.01)
        raise socket.timeout()


class _DummySocketIO:
    def __init__(self) -> None:
        self.emits: list[tuple[tuple, dict]] = []

    def emit(self, *args, **kwargs) -> None:
        self.emits.append((args, kwargs))

    def start_background_task(self, _target, *_args, **_kwargs):
        thread = threading.Thread(target=_target, args=_args, kwargs=_kwargs, daemon=True)
        thread.start()
        return thread


class _DeferredSocketIO(_DummySocketIO):
    def __init__(self) -> None:
        super().__init__()
        self._pending: list[tuple[object, tuple, dict]] = []

    def start_background_task(self, target, *args, **kwargs):
        self._pending.append((target, args, kwargs))
        return object()

    def run_pending(self) -> None:
        pending = list(self._pending)
        self._pending.clear()
        for target, args, kwargs in pending:
            thread = threading.Thread(target=target, args=args, kwargs=kwargs, daemon=True)
            thread.start()


class _DummyTunnelService:
    def __init__(self) -> None:
        self.start_calls: list[tuple[str, bool, str]] = []
        self.confirm_calls: list[tuple[str, str]] = []

    def status(self, _agent_id: str):
        return {"virtual_ip": "10.255.0.20/32"}

    def request_agent_start(
        self,
        agent_id: str,
        *,
        force_restart: bool = False,
        reason: str | None = None,
    ):
        self.start_calls.append((agent_id, bool(force_restart), str(reason or "")))
        return {"status": "ok"}

    def bump_activity(self, _agent_id: str) -> None:
        return

    def confirm_transport_success(self, agent_id: str, *, reason: str | None = None) -> None:
        self.confirm_calls.append((agent_id, str(reason or "")))


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


def test_shell_session_wait_for_ready_sends_ping_and_accepts_pong() -> None:
    tcp = _PongSocket()
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=_DummySocketIO(),
        tcp=tcp,
    )

    session.start_reader()

    assert session.wait_for_ready(timeout=1.0) is True
    payload = json.loads(tcp.sent[0].decode("utf-8").strip())
    assert payload["type"] == "ping"
    assert payload["ping_id"]


def test_shell_session_wait_for_ready_yields_to_eventlet_background_tasks(monkeypatch) -> None:
    tcp = _PongSocket()
    socketio = _DeferredSocketIO()
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=socketio,
        tcp=tcp,
    )
    session.start_reader()

    fake_eventlet = types.SimpleNamespace(
        sleep=lambda _seconds: (socketio.run_pending(), time.sleep(0.01)),
    )
    monkeypatch.setitem(sys.modules, "eventlet", fake_eventlet)

    assert session.wait_for_ready(timeout=0.5) is True


def test_open_session_enables_tcp_nodelay(monkeypatch) -> None:
    tcp = _PongSocket()
    socketio = _DummySocketIO()
    tunnel_service = _DummyTunnelService()
    context = type(
        "Ctx",
        (),
        {
            "logger": logging.getLogger("test.vpn_shell"),
            "wireguard_shell_port": 47002,
            "vpn_tunnel_service": tunnel_service,
        },
    )()
    bridge = VpnShellBridge(socketio, context)

    monkeypatch.setattr(socket, "create_connection", lambda *_args, **_kwargs: tcp)

    session = bridge.open_session("sid-1", "agent-1")

    assert session is not None
    assert (socket.IPPROTO_TCP, socket.TCP_NODELAY, 1) in tcp.sockopts
    assert tcp.timeout == 15
    assert tunnel_service.start_calls == [("agent-1", False, "shell_connect_retry")]
    assert tunnel_service.confirm_calls == [("agent-1", "shell_connect_success")]
