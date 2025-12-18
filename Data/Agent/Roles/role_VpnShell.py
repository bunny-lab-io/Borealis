# ======================================================
# Data\Agent\Roles\role_VpnShell.py
# Description: PowerShell TCP server for VPN shell access (Engine connects over WireGuard /32).
#
# API Endpoints (if applicable): None
# ======================================================

"""VPN PowerShell server for the WireGuard tunnel."""

from __future__ import annotations

import base64
import json
import socket
import subprocess
import threading
import time
from pathlib import Path
from typing import Any, Optional
import os

ROLE_NAME = "VpnShell"
ROLE_CONTEXTS = ["system"]


def _log_path() -> Path:
    root = Path(__file__).resolve().parents[2] / "Logs"
    root.mkdir(parents=True, exist_ok=True)
    return root / "reverse_tunnel.log"


def _write_log(message: str) -> None:
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [vpn-shell] {message}\n")
    except Exception:
        pass


def _b64encode(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii").strip()


def _b64decode(value: str) -> bytes:
    return base64.b64decode(value.encode("ascii"))

def _resolve_shell_port() -> int:
    raw = os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT")
    try:
        value = int(raw) if raw is not None else 47001
    except Exception:
        value = 47001
    if value < 1 or value > 65535:
        return 47001
    return value


class ShellSession:
    def __init__(self, conn: socket.socket, address: tuple[str, int]) -> None:
        self.conn = conn
        self.address = address
        self.proc: Optional[subprocess.Popen] = None
        self._stop = threading.Event()

    def start(self) -> None:
        self.proc = subprocess.Popen(
            ["powershell.exe", "-NoLogo", "-NoProfile", "-NoExit", "-Command", "-"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            bufsize=0,
        )
        threading.Thread(target=self._reader_loop, daemon=True).start()
        self._writer_loop()

    def _reader_loop(self) -> None:
        if not self.proc or not self.proc.stdout:
            return
        try:
            while not self._stop.is_set():
                chunk = self.proc.stdout.readline()
                if not chunk:
                    break
                payload = json.dumps({"type": "stdout", "data": _b64encode(chunk)})
                self.conn.sendall(payload.encode("utf-8") + b"\n")
        except Exception:
            pass

    def _writer_loop(self) -> None:
        buffer = b""
        try:
            while not self._stop.is_set():
                data = self.conn.recv(4096)
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
                    if msg.get("type") == "stdin":
                        payload = msg.get("data") or ""
                        if self.proc and self.proc.stdin:
                            try:
                                self.proc.stdin.write(_b64decode(str(payload)))
                                self.proc.stdin.flush()
                            except Exception:
                                pass
                    if msg.get("type") == "close":
                        self._stop.set()
                        break
        finally:
            self.close()

    def close(self) -> None:
        self._stop.set()
        try:
            self.conn.close()
        except Exception:
            pass
        if self.proc:
            try:
                self.proc.terminate()
            except Exception:
                pass


class ShellServer:
    def __init__(self, host: str = "0.0.0.0", port: Optional[int] = None) -> None:
        self.host = host
        self.port = port or _resolve_shell_port()
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()
        _write_log(f"VPN shell server listening on {self.host}:{self.port}")

    def _serve(self) -> None:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server:
            server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            server.bind((self.host, self.port))
            server.listen(5)
            while True:
                conn, addr = server.accept()
                remote_ip = addr[0]
                if not remote_ip.startswith("10.255."):
                    _write_log(f"Rejected shell connection from {remote_ip}")
                    conn.close()
                    continue
                _write_log(f"Accepted shell connection from {remote_ip}")
                session = ShellSession(conn, addr)
                threading.Thread(target=session.start, daemon=True).start()


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.server = ShellServer()

    def register_events(self) -> None:
        return

    def stop_all(self) -> None:
        return
