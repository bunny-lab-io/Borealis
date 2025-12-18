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
from dataclasses import dataclass
from typing import Any, Dict, Optional


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
    _reader: Optional[threading.Thread] = None

    def start_reader(self) -> None:
        t = threading.Thread(target=self._read_loop, daemon=True)
        t.start()
        self._reader = t

    def _read_loop(self) -> None:
        buffer = b""
        try:
            while True:
                data = self.tcp.recv(4096)
                if not data:
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
                        self.socketio.emit("vpn_shell_output", {"data": decoded}, to=self.sid)
        finally:
            self.socketio.emit("vpn_shell_closed", {"agent_id": self.agent_id}, to=self.sid)
            try:
                self.tcp.close()
            except Exception:
                pass

    def send(self, payload: str) -> None:
        data = json.dumps({"type": "stdin", "data": _b64encode(payload.encode("utf-8"))})
        self.tcp.sendall(data.encode("utf-8") + b"\n")

    def close(self) -> None:
        try:
            data = json.dumps({"type": "close"})
            self.tcp.sendall(data.encode("utf-8") + b"\n")
        except Exception:
            pass
        try:
            self.tcp.close()
        except Exception:
            pass


class VpnShellBridge:
    def __init__(self, socketio, context) -> None:
        self.socketio = socketio
        self.context = context
        self._sessions: Dict[str, ShellSession] = {}
        self.logger = context.logger.getChild("vpn_shell")

    def open_session(self, sid: str, agent_id: str) -> Optional[ShellSession]:
        service = getattr(self.context, "vpn_tunnel_service", None)
        if service is None:
            return None
        status = service.status(agent_id)
        if not status:
            return None
        host = str(status.get("virtual_ip") or "").split("/")[0]
        port = int(self.context.wireguard_shell_port)
        try:
            tcp = socket.create_connection((host, port), timeout=5)
        except Exception:
            self.logger.debug("Failed to connect vpn shell to %s:%s", host, port, exc_info=True)
            return None
        session = ShellSession(sid=sid, agent_id=agent_id, socketio=self.socketio, tcp=tcp)
        self._sessions[sid] = session
        session.start_reader()
        return session

    def send(self, sid: str, payload: str) -> None:
        session = self._sessions.get(sid)
        if not session:
            return
        session.send(payload)
        service = getattr(self.context, "vpn_tunnel_service", None)
        if service:
            service.bump_activity(session.agent_id)

    def close(self, sid: str) -> None:
        session = self._sessions.pop(sid, None)
        if not session:
            return
        session.close()

