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
import socket
import threading
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, Optional

_CONNECT_ATTEMPTS = 8
_CONNECT_TIMEOUT_SECONDS = 2.0
_RETRY_DELAY_SECONDS = 1.0
_REEMIT_START_ATTEMPTS = {0, 3}


def _b64encode(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii").strip()


def _b64decode(value: str) -> bytes:
    return base64.b64decode(value.encode("ascii"))


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
    _closed: bool = False
    session_id: str = field(default_factory=lambda: uuid.uuid4().hex)

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
                        self.output_lines += 1
                        self.output_bytes += len(line)
                        self.socketio.emit(
                            "vpn_shell_output",
                            {
                                "data": decoded,
                                "agent_id": self.agent_id,
                                "session_id": self.session_id,
                            },
                            to=self.sid,
                        )
        finally:
            self._closed = True
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

    def send(self, payload: str) -> None:
        payload_bytes = payload.encode("utf-8")
        data = json.dumps({"type": "stdin", "data": _b64encode(payload_bytes)})
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
        host = str(status.get("virtual_ip") or "").split("/")[0]
        port = int(self.context.wireguard_shell_port)
        tcp = None
        last_error: Optional[Exception] = None
        for attempt in range(_CONNECT_ATTEMPTS):
            self._service_log_event(
                "vpn_shell_connect_attempt agent_id={0} sid={1} host={2} port={3} attempt={4}".format(
                    agent_id,
                    sid,
                    host,
                    port,
                    attempt + 1,
                )
            )
            try:
                tcp = socket.create_connection((host, port), timeout=_CONNECT_TIMEOUT_SECONDS)
                break
            except Exception as exc:
                last_error = exc
                if attempt in _REEMIT_START_ATTEMPTS:
                    try:
                        service.request_agent_start(agent_id)
                        self._service_log_event(
                            "vpn_shell_agent_start_emit agent_id={0} sid={1} trigger_attempt={2}".format(
                                agent_id,
                                sid,
                                attempt + 1,
                            )
                        )
                    except Exception:
                        self.logger.debug("Failed to re-emit vpn_tunnel_start for agent=%s", agent_id, exc_info=True)
                        self._service_log_event(
                            "vpn_shell_agent_start_failed agent_id={0} sid={1} trigger_attempt={2}".format(
                                agent_id,
                                sid,
                                attempt + 1,
                            ),
                            level="WARNING",
                        )
                if attempt < (_CONNECT_ATTEMPTS - 1):
                    time.sleep(_RETRY_DELAY_SECONDS)
        if tcp is None:
            self._service_log_event(
                "vpn_shell_connect_failed agent_id={0} sid={1} host={2} port={3} error={4}".format(
                    agent_id,
                    sid,
                    host,
                    port,
                    str(last_error) if last_error else "-",
                ),
                level="WARNING",
            )
            self.logger.warning("Failed to connect vpn shell to %s:%s", host, port, exc_info=last_error)
            return None
        session = ShellSession(
            sid=sid,
            agent_id=agent_id,
            socketio=self.socketio,
            tcp=tcp,
            service_log=self.service_log,
            on_closed=self._on_session_closed,
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
        return session

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
        session.send(payload)
        try:
            payload_len = len(str(payload))
        except Exception:
            payload_len = 0
        self._service_log_event(
            "vpn_shell_send agent_id={0} sid={1} bytes={2}".format(
                session.agent_id,
                sid,
                payload_len,
            )
        )
        service = getattr(self.context, "vpn_tunnel_service", None)
        if service:
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
