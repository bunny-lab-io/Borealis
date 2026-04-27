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

import Data.Engine.services.WebSocket.vpn_shell as vpn_shell_module
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


class _ClosingSocket(_DummySocket):
    def recv(self, _size: int) -> bytes:
        return b""


class _StdoutSocket(_DummySocket):
    def __init__(self, payloads: list[dict]) -> None:
        super().__init__()
        self._payloads = [json.dumps(payload).encode("utf-8") + b"\n" for payload in payloads]

    def recv(self, _size: int) -> bytes:
        if self._payloads:
            return self._payloads.pop(0)
        return b""


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


class _ErrorOnClosedSocket(_DummySocket):
    def recv(self, _size: int) -> bytes:
        deadline = time.time() + 0.5
        while time.time() < deadline:
            if self.closed:
                raise OSError(9, "Bad file descriptor")
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
        self.mark_calls: list[tuple[str, str]] = []
        self.recover_calls: list[tuple[str, str, str]] = []
        self.status_payload = {"virtual_ip": "10.255.0.20/32"}

    def status(self, _agent_id: str):
        return dict(self.status_payload)

    def request_agent_start(
        self,
        agent_id: str,
        *,
        force_restart: bool = False,
        reason: str | None = None,
    ):
        self.start_calls.append((agent_id, bool(force_restart), str(reason or "")))
        return {"status": "ok"}

    def mark_transport_required(self, agent_id: str, *, reason: str | None = None) -> bool:
        self.mark_calls.append((agent_id, str(reason or "")))
        return True

    def bump_activity(self, _agent_id: str) -> None:
        return

    def confirm_transport_success(self, agent_id: str, *, reason: str | None = None) -> None:
        self.confirm_calls.append((agent_id, str(reason or "")))

    def recover_transport(self, agent_id: str, *, trigger: str, reason: str | None = None):
        self.recover_calls.append((agent_id, str(trigger or ""), str(reason or "")))
        return {"status": "ok"}


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
    assert payload["session_id"] == session.session_id
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


def test_open_session_waits_when_probe_grace_pending(monkeypatch) -> None:
    tcp = _PongSocket()
    socketio = _DummySocketIO()
    tunnel_service = _DummyTunnelService()
    tunnel_service.status_payload.update(
        {
            "peer_health_reason": "probe_grace",
            "transport_ready": True,
        }
    )
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
    assert tunnel_service.start_calls == [("agent-1", False, "shell_connect_retry")]
    assert tunnel_service.recover_calls == []


def test_open_session_forces_recovery_when_transport_is_stale(monkeypatch) -> None:
    tcp = _PongSocket()
    socketio = _DummySocketIO()
    tunnel_service = _DummyTunnelService()
    tunnel_service.status_payload.update(
        {
            "peer_health_reason": "stale_handshake",
            "transport_ready": False,
        }
    )
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
    assert tunnel_service.start_calls[0] == ("agent-1", True, "shell_connect_retry")
    assert tunnel_service.recover_calls == [("agent-1", "vpn_shell_connect", "shell_connect_retry")]


def test_open_session_does_not_force_recovery_after_single_healthy_connect_failure(monkeypatch) -> None:
    tcp = _PongSocket()
    socketio = _DummySocketIO()
    tunnel_service = _DummyTunnelService()
    tunnel_service.status_payload.update(
        {
            "peer_health_reason": "recent_transport_success",
            "transport_ready": True,
            "last_transport_confirmed_at": int(time.time()),
            "confirmed_age_seconds": 1,
        }
    )
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
    attempts = 0

    def fake_create_connection(*_args, **_kwargs):
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            raise TimeoutError("stale shell path")
        return tcp

    monkeypatch.setattr(socket, "create_connection", fake_create_connection)
    monkeypatch.setattr(vpn_shell_module, "_RETRY_DELAY_SECONDS", 0.001)

    session = bridge.open_session("sid-1", "agent-1")

    assert session is not None
    assert tunnel_service.start_calls == [("agent-1", False, "shell_connect_retry")]
    assert tunnel_service.recover_calls == []


def test_open_session_replaces_existing_agent_session(monkeypatch) -> None:
    tcp_first = _PongSocket()
    tcp_second = _PongSocket()
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
    sockets = [tcp_first, tcp_second]

    monkeypatch.setattr(socket, "create_connection", lambda *_args, **_kwargs: sockets.pop(0))

    first = bridge.open_session("sid-1", "agent-1")
    second = bridge.open_session("sid-2", "agent-1")

    assert first is not None
    assert second is not None
    assert second is not first
    assert tcp_first.closed is True
    assert "sid-1" not in bridge._sessions
    assert bridge._sessions.get("sid-2") is second
    assert bridge._sessions_by_agent.get("agent-1") is second
    second.close()


def test_shell_session_logs_warning_when_inputs_close_without_output() -> None:
    tcp = _ClosingSocket()
    logs: list[tuple[str, str | None]] = []
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=_DummySocketIO(),
        tcp=tcp,
        service_log=lambda _path, message, level=None: logs.append((message, level)),
    )

    session.send("whoami\r\n")
    session.start_reader()
    time.sleep(0.05)

    assert any("vpn_shell_no_output_after_input" in message for message, _level in logs)


def test_shell_output_confirms_transport_success_with_keyword_reason() -> None:
    tcp = _StdoutSocket(
        [
            {
                "type": "stdout",
                "data": "bnQgYXV0aG9yaXR5XFxzeXN0ZW0NCg==",
                "message_id": "msg-1",
                "sent_at_ms": 1000,
                "agent_received_at_ms": 1001,
                "agent_stdout_at_ms": 1002,
            }
        ]
    )
    tunnel_service = _DummyTunnelService()
    logs: list[tuple[str, str | None]] = []
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=_DummySocketIO(),
        tcp=tcp,
        service_log=lambda _path, message, level=None: logs.append((message, level)),
        on_transport_confirmed=tunnel_service.confirm_transport_success,
    )

    session.start_reader()
    time.sleep(0.05)

    assert ("agent-1", "shell_output") in tunnel_service.confirm_calls
    assert not any("vpn_shell_transport_confirm_failed" in message for message, _level in logs)


def test_shell_session_idle_keepalive_confirms_transport_success(monkeypatch) -> None:
    tcp = _PongSocket()
    tunnel_service = _DummyTunnelService()
    monkeypatch.setattr(vpn_shell_module, "_IDLE_PING_IDLE_SECONDS", 0.01)
    monkeypatch.setattr(vpn_shell_module, "_IDLE_PING_INTERVAL_SECONDS", 0.01)
    monkeypatch.setattr(vpn_shell_module, "_IDLE_PING_TIMEOUT_SECONDS", 0.1)
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=_DummySocketIO(),
        tcp=tcp,
        on_transport_confirmed=tunnel_service.confirm_transport_success,
    )

    session.start_reader()
    deadline = time.time() + 0.5
    while not tunnel_service.confirm_calls and time.time() < deadline:
        time.sleep(0.01)
    session.close()

    ping_payloads = [
        json.loads(payload.decode("utf-8").strip())
        for payload in tcp.sent
        if payload and json.loads(payload.decode("utf-8").strip()).get("type") == "ping"
    ]

    assert any(call == ("agent-1", "shell_keepalive") for call in tunnel_service.confirm_calls)
    assert any(payload.get("reason") == "idle_keepalive" for payload in ping_payloads)


def test_shell_session_suppresses_expected_read_error_after_close_request() -> None:
    tcp = _ErrorOnClosedSocket()
    logs: list[tuple[str, str | None]] = []
    session = ShellSession(
        sid="sid-1",
        agent_id="agent-1",
        socketio=_DummySocketIO(),
        tcp=tcp,
        service_log=lambda _path, message, level=None: logs.append((message, level)),
    )

    session.start_reader()
    time.sleep(0.02)
    session.close_with_reason("close_request")
    deadline = time.time() + 0.5
    while not any("vpn_shell_closed" in message for message, _level in logs) and time.time() < deadline:
        time.sleep(0.01)

    assert not any("vpn_shell_read_error" in message for message, _level in logs)
    assert any("vpn_shell_closed agent_id=agent-1 sid=sid-1 reason=close_request" in message for message, _level in logs)
