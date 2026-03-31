# ======================================================
# Data\Engine\services\WebSocket\vpn_shell.py
# Description: Socket.IO handlers bridging UI shell to agent TCP server over WireGuard.
#
# API Endpoints (if applicable): None
# ======================================================

"""WireGuard VPN PowerShell bridge (Engine side)."""

from __future__ import annotations

import base64
import json
import math
import socket
import threading
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, Optional

_CONNECT_WAIT_WINDOW_SECONDS = 20.0
_CONNECT_TIMEOUT_SECONDS = 1.0
_RETRY_DELAY_SECONDS = 0.25
_REEMIT_START_AFTER_SECONDS = (0.0, 2.0, 5.0, 10.0)
_FORCE_RESTART_AFTER_SECONDS = 5.0


def _b64encode(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii").strip()


def _b64decode(value: str) -> bytes:
    return base64.b64decode(value.encode("ascii"))


def _now_ms() -> int:
    return int(time.time() * 1000)


def _coerce_int(value: Any) -> Optional[int]:
    try:
        if value in (None, ""):
            return None
        return int(value)
    except Exception:
        return None


def _configure_tcp_socket(sock: socket.socket) -> None:
    try:
        sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    except Exception:
        pass


def _cooperative_sleep(seconds: float) -> None:
    try:
        import eventlet  # type: ignore

        eventlet.sleep(seconds)
    except Exception:
        time.sleep(seconds)


@dataclass
class ShellSession:
    sid: str
    agent_id: str
    socketio: Any
    tcp: socket.socket
    service_log: Optional[Callable[[str, str, Optional[str]], None]] = None
    output_lines: int = 0
    output_bytes: int = 0
    input_messages: int = 0
    input_bytes: int = 0
    _reader: Optional[threading.Thread] = None
    on_closed: Optional[Callable[[str, "ShellSession"], None]] = None
    on_transport_confirmed: Optional[Callable[[str, str], None]] = None
    _closed: bool = False
    session_id: str = field(default_factory=lambda: uuid.uuid4().hex)
    _message_sequence: int = 0
    _timed_output_ids: set[str] = field(default_factory=set)
    _ready_event: threading.Event = field(default_factory=threading.Event)
    _pending_ping_id: Optional[str] = None

    def start_reader(self) -> None:
        starter = getattr(self.socketio, "start_background_task", None)
        if callable(starter):
            self._reader = starter(self._read_loop)
        else:
            t = threading.Thread(target=self._read_loop, daemon=True)
            t.start()
            self._reader = t

    def is_active(self) -> bool:
        if self._closed:
            return False
        try:
            return self.tcp.fileno() >= 0
        except Exception:
            return False

    def _notify_closed(self) -> None:
        if not callable(self.on_closed):
            return
        try:
            self.on_closed(self.sid, self)
        except Exception:
            pass

    def _service_log_event(self, message: str, *, level: str = "INFO") -> None:
        if not callable(self.service_log):
            return
        try:
            self.service_log("VPN_Tunnel/remote_shell", message, level=level)
        except Exception:
            pass

    def _read_loop(self) -> None:
        buffer = b""
        reason = "remote_closed"
        error_detail = ""
        try:
            while True:
                try:
                    data = self.tcp.recv(4096)
                except (socket.timeout, TimeoutError):
                    # No data ready; keep the session alive.
                    continue
                except Exception as exc:
                    reason = "read_error"
                    error_detail = f"{type(exc).__name__}:{exc}"
                    break
                if not data:
                    reason = "remote_closed"
                    break
                buffer += data
                while b"\n" in buffer:
                    line, buffer = buffer.split(b"\n", 1)
                    if not line:
                        continue
                    try:
                        msg = json.loads(line.decode("utf-8"))
                    except Exception:
                        continue
                    if msg.get("type") == "stdout":
                        payload = msg.get("data") or ""
                        try:
                            decoded = _b64decode(str(payload)).decode("utf-8", errors="replace")
                        except Exception:
                            decoded = ""
                        message_id = str(msg.get("message_id") or "").strip()
                        sent_at_ms = _coerce_int(msg.get("sent_at_ms"))
                        agent_received_at_ms = _coerce_int(msg.get("agent_received_at_ms"))
                        agent_stdout_at_ms = _coerce_int(msg.get("agent_stdout_at_ms"))
                        engine_received_at_ms = _now_ms()
                        self.output_lines += 1
                        self.output_bytes += len(line)
                        if message_id and message_id not in self._timed_output_ids:
                            self._timed_output_ids.add(message_id)
                            engine_to_agent_ms = (
                                max(0, agent_received_at_ms - sent_at_ms)
                                if sent_at_ms is not None and agent_received_at_ms is not None
                                else None
                            )
                            agent_exec_ms = (
                                max(0, agent_stdout_at_ms - agent_received_at_ms)
                                if agent_received_at_ms is not None and agent_stdout_at_ms is not None
                                else None
                            )
                            agent_to_engine_ms = (
                                max(0, engine_received_at_ms - agent_stdout_at_ms)
                                if agent_stdout_at_ms is not None
                                else None
                            )
                            round_trip_ms = (
                                max(0, engine_received_at_ms - sent_at_ms)
                                if sent_at_ms is not None
                                else None
                            )
                            self._service_log_event(
                                "vpn_shell_output_timing agent_id={0} sid={1} message_id={2} round_trip_ms={3} engine_to_agent_ms={4} agent_exec_ms={5} agent_to_engine_ms={6}".format(
                                    self.agent_id,
                                    self.sid,
                                    message_id,
                                    round_trip_ms if round_trip_ms is not None else "-",
                                    engine_to_agent_ms if engine_to_agent_ms is not None else "-",
                                    agent_exec_ms if agent_exec_ms is not None else "-",
                                    agent_to_engine_ms if agent_to_engine_ms is not None else "-",
                                )
                            )
                        self.socketio.emit(
                            "vpn_shell_output",
                            {
                                "data": decoded,
                                "agent_id": self.agent_id,
                                "session_id": self.session_id,
                            },
                            to=self.sid,
                        )
                        if self._pending_ping_id:
                            self._ready_event.set()
                        if callable(self.on_transport_confirmed):
                            try:
                                self.on_transport_confirmed(self.agent_id, "shell_output")
                            except Exception:
                                pass
                    elif msg.get("type") == "pong":
                        ping_id = str(msg.get("ping_id") or "").strip()
                        if ping_id and ping_id == self._pending_ping_id:
                            self._service_log_event(
                                "vpn_shell_ready_pong agent_id={0} sid={1} ping_id={2}".format(
                                    self.agent_id,
                                    self.sid,
                                    ping_id,
                                )
                            )
                            self._ready_event.set()
        finally:
            self._closed = True
            self._ready_event.set()
            if reason == "read_error":
                self._service_log_event(
                    "vpn_shell_read_error agent_id={0} sid={1} reason={2} error={3}".format(
                        self.agent_id,
                        self.sid,
                        reason,
                        error_detail or "-",
                    ),
                    level="WARNING",
                )
            self._service_log_event(
                "vpn_shell_closed agent_id={0} sid={1} reason={2}".format(
                    self.agent_id,
                    self.sid,
                    reason,
                )
            )
            self._service_log_event(
                "vpn_shell_output_summary agent_id={0} sid={1} lines={2} bytes={3} inputs={4} input_bytes={5}".format(
                    self.agent_id,
                    self.sid,
                    self.output_lines,
                    self.output_bytes,
                    self.input_messages,
                    self.input_bytes,
                )
            )
            self.socketio.emit(
                "vpn_shell_closed",
                {
                    "agent_id": self.agent_id,
                    "session_id": self.session_id,
                },
                to=self.sid,
            )
            try:
                self.tcp.close()
            except Exception:
                pass
            self._notify_closed()

    def send(self, payload: str) -> Dict[str, Any]:
        payload_bytes = payload.encode("utf-8")
        self._message_sequence += 1
        message_id = f"{self.session_id}-{self._message_sequence}"
        sent_at_ms = _now_ms()
        data = json.dumps(
            {
                "type": "stdin",
                "data": _b64encode(payload_bytes),
                "message_id": message_id,
                "sent_at_ms": sent_at_ms,
            }
        )
        self.input_messages += 1
        self.input_bytes += len(payload_bytes)
        try:
            self.tcp.sendall(data.encode("utf-8") + b"\n")
        except Exception as exc:
            self._service_log_event(
                "vpn_shell_send_failed agent_id={0} sid={1} error={2}".format(
                    self.agent_id,
                    self.sid,
                    f"{type(exc).__name__}:{exc}",
                ),
                level="WARNING",
            )
            return {"message_id": message_id, "sent_at_ms": sent_at_ms, "send_failed": True}
        return {"message_id": message_id, "sent_at_ms": sent_at_ms, "send_failed": False}

    def wait_for_ready(self, timeout: float = 1.5) -> bool:
        ping_id = uuid.uuid4().hex
        self._pending_ping_id = ping_id
        self._ready_event.clear()
        sent_at_ms = _now_ms()
        try:
            data = json.dumps(
                {
                    "type": "ping",
                    "ping_id": ping_id,
                    "sent_at_ms": sent_at_ms,
                }
            )
            self.tcp.sendall(data.encode("utf-8") + b"\n")
        except Exception:
            self._pending_ping_id = None
            return False
        deadline = time.monotonic() + max(0.05, float(timeout or 0.0))
        try:
            while True:
                if self._ready_event.is_set():
                    return self.is_active()
                if not self.is_active():
                    return False
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return False
                _cooperative_sleep(min(0.05, remaining))
        finally:
            self._pending_ping_id = None

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        try:
            data = json.dumps({"type": "close"})
            self.tcp.sendall(data.encode("utf-8") + b"\n")
        except Exception:
            pass
        try:
            self.tcp.close()
        except Exception:
            pass
        self._notify_closed()


class VpnShellBridge:
    def __init__(self, socketio, context, service_log=None) -> None:
        self.socketio = socketio
        self.context = context
        self._sessions: Dict[str, ShellSession] = {}
        self._lock = threading.Lock()
        self.logger = context.logger.getChild("vpn_shell")
        self.service_log = service_log

    def _on_session_closed(self, sid: str, session: ShellSession) -> None:
        with self._lock:
            current = self._sessions.get(sid)
            if current is session:
                self._sessions.pop(sid, None)

    def _service_log_event(self, message: str, *, level: str = "INFO") -> None:
        if not callable(self.service_log):
            return
        try:
            self.service_log("VPN_Tunnel/remote_shell", message, level=level)
        except Exception:
            self.logger.debug("vpn_shell service log write failed", exc_info=True)

    def open_session(self, sid: str, agent_id: str) -> Optional[ShellSession]:
        service = getattr(self.context, "vpn_tunnel_service", None)
        if service is None:
            return None
        existing = None
        with self._lock:
            existing = self._sessions.get(sid)
        if existing:
            if existing.agent_id == agent_id and existing.is_active():
                self._service_log_event(
                    "vpn_shell_reuse_session agent_id={0} sid={1}".format(
                        existing.agent_id,
                        sid,
                    )
                )
                return existing
            with self._lock:
                current = self._sessions.get(sid)
                if current is existing:
                    self._sessions.pop(sid, None)
            self._service_log_event(
                "vpn_shell_replace_session agent_id={0} sid={1}".format(
                    existing.agent_id,
                    sid,
                ),
                level="WARNING",
            )
            existing.close()
        status = service.status(agent_id)
        if not status:
            try:
                status = service.connect(agent_id=agent_id, operator_id=None, endpoint_host=None)
            except Exception:
                return None
        try:
            service.mark_transport_required(agent_id, reason="shell_connect")
        except Exception:
            self.logger.debug("Failed to mark shell transport activity for agent=%s", agent_id, exc_info=True)
        host = str(status.get("virtual_ip") or "").split("/")[0]
        port = int(self.context.wireguard_shell_port)
        tcp = None
        last_error: Optional[Exception] = None
        attempts = 0
        connect_started_at = time.monotonic()
        connect_deadline = connect_started_at + _CONNECT_WAIT_WINDOW_SECONDS
        reemit_index = 0
        while True:
            now = time.monotonic()
            remaining = connect_deadline - now
            if remaining <= 0:
                break
            elapsed = max(0.0, now - connect_started_at)
            while reemit_index < len(_REEMIT_START_AFTER_SECONDS):
                trigger_after = _REEMIT_START_AFTER_SECONDS[reemit_index]
                if elapsed + 0.001 < trigger_after:
                    break
                force_restart = trigger_after >= _FORCE_RESTART_AFTER_SECONDS
                try:
                    service.request_agent_start(
                        agent_id,
                        force_restart=force_restart,
                        reason="shell_connect_retry",
                    )
                    self._service_log_event(
                        "vpn_shell_agent_start_emit agent_id={0} sid={1} trigger_elapsed={2} force_restart={3}".format(
                            agent_id,
                            sid,
                            int(math.floor(trigger_after)),
                            str(bool(force_restart)).lower(),
                        )
                    )
                except Exception:
                    self.logger.debug("Failed to re-emit vpn_tunnel_start for agent=%s", agent_id, exc_info=True)
                    self._service_log_event(
                        "vpn_shell_agent_start_failed agent_id={0} sid={1} trigger_elapsed={2} force_restart={3}".format(
                            agent_id,
                            sid,
                            int(math.floor(trigger_after)),
                            str(bool(force_restart)).lower(),
                        ),
                        level="WARNING",
                    )
                if force_restart:
                    try:
                        service.recover_transport(
                            agent_id,
                            trigger="vpn_shell_connect",
                            reason="shell_connect_retry",
                        )
                        self._service_log_event(
                            "vpn_shell_transport_recovery agent_id={0} sid={1} trigger_elapsed={2}".format(
                                agent_id,
                                sid,
                                int(math.floor(trigger_after)),
                            ),
                            level="WARNING",
                        )
                    except Exception:
                        self.logger.debug(
                            "Failed to force WireGuard transport recovery for agent=%s",
                            agent_id,
                            exc_info=True,
                        )
                        self._service_log_event(
                            "vpn_shell_transport_recovery_failed agent_id={0} sid={1} trigger_elapsed={2}".format(
                                agent_id,
                                sid,
                                int(math.floor(trigger_after)),
                            ),
                            level="WARNING",
                        )
                reemit_index += 1

            attempts += 1
            self._service_log_event(
                "vpn_shell_connect_attempt agent_id={0} sid={1} host={2} port={3} attempt={4}".format(
                    agent_id,
                    sid,
                    host,
                    port,
                    attempts,
                )
            )
            try:
                timeout = min(_CONNECT_TIMEOUT_SECONDS, max(0.5, remaining))
                tcp = socket.create_connection((host, port), timeout=timeout)
                _configure_tcp_socket(tcp)
            except Exception as exc:
                last_error = exc
                remaining = connect_deadline - time.monotonic()
                if remaining > 0:
                    _cooperative_sleep(min(_RETRY_DELAY_SECONDS, remaining))
                continue

            session = ShellSession(
                sid=sid,
                agent_id=agent_id,
                socketio=self.socketio,
                tcp=tcp,
                service_log=self.service_log,
                on_closed=self._on_session_closed,
                on_transport_confirmed=(
                    getattr(service, "confirm_transport_success", None) if service is not None else None
                ),
            )
            try:
                session.tcp.settimeout(15)
            except Exception:
                pass
            with self._lock:
                self._sessions[sid] = session
            self._service_log_event(
                "vpn_shell_connect_success agent_id={0} sid={1} host={2} port={3}".format(
                    agent_id,
                    sid,
                    host,
                    port,
                )
            )
            session.start_reader()
            ready_timeout = min(2.0, max(0.5, connect_deadline - time.monotonic()))
            if not session.wait_for_ready(timeout=ready_timeout):
                self._service_log_event(
                    "vpn_shell_ready_probe_failed agent_id={0} sid={1} host={2} port={3} attempt={4}".format(
                        agent_id,
                        sid,
                        host,
                        port,
                        attempts,
                    ),
                    level="WARNING",
                )
                with self._lock:
                    current = self._sessions.get(sid)
                    if current is session:
                        self._sessions.pop(sid, None)
                session.close()
                last_error = RuntimeError("shell_ready_probe_failed")
                remaining = connect_deadline - time.monotonic()
                if remaining > 0:
                    _cooperative_sleep(min(_RETRY_DELAY_SECONDS, remaining))
                tcp = None
                continue
            if service is not None:
                try:
                    service.confirm_transport_success(agent_id, reason="shell_connect_success")
                except Exception:
                    self.logger.debug(
                        "Failed to confirm shell transport success for agent=%s",
                        agent_id,
                        exc_info=True,
                    )
            return session

        elapsed_seconds = int(math.ceil(max(0.0, time.monotonic() - connect_started_at)))
        self._service_log_event(
            "vpn_shell_connect_failed agent_id={0} sid={1} host={2} port={3} attempts={4} waited_seconds={5} error={6}".format(
                agent_id,
                sid,
                host,
                port,
                attempts,
                elapsed_seconds,
                str(last_error) if last_error else "-",
            ),
            level="WARNING",
        )
        self.logger.warning("Failed to connect vpn shell to %s:%s", host, port, exc_info=last_error)
        return None

    def send(self, sid: str, payload: str) -> None:
        with self._lock:
            session = self._sessions.get(sid)
        if session and not session.is_active():
            with self._lock:
                current = self._sessions.get(sid)
                if current is session:
                    self._sessions.pop(sid, None)
            session = None
        if not session:
            self._service_log_event(
                "vpn_shell_send_missing sid={0}".format(sid or "-"),
                level="WARNING",
            )
            return
        send_meta = session.send(payload)
        try:
            payload_len = len(str(payload))
        except Exception:
            payload_len = 0
        self._service_log_event(
            "vpn_shell_send agent_id={0} sid={1} bytes={2} message_id={3} sent_at_ms={4} send_failed={5}".format(
                session.agent_id,
                sid,
                payload_len,
                send_meta.get("message_id", "-"),
                send_meta.get("sent_at_ms", "-"),
                str(bool(send_meta.get("send_failed"))).lower(),
            )
        )
        service = getattr(self.context, "vpn_tunnel_service", None)
        if service:
            try:
                service.mark_transport_required(session.agent_id, reason="shell_input")
            except Exception:
                self.logger.debug(
                    "Failed to mark shell transport activity from stdin for agent=%s",
                    session.agent_id,
                    exc_info=True,
                )
            service.bump_activity(session.agent_id)

    def close(self, sid: str) -> None:
        with self._lock:
            session = self._sessions.pop(sid, None)
        if not session:
            return
        self._service_log_event(
            "vpn_shell_close_request agent_id={0} sid={1}".format(
                session.agent_id,
                sid,
            )
        )
        session.close()
