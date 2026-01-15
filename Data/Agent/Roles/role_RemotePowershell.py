# ======================================================
# Data\Agent\Roles\role_RemotePowershell.py
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

ROLE_NAME = "RemotePowershell"
ROLE_CONTEXTS = ["system"]


def _log_path() -> Path:
    # Keep shell logs alongside other VPN tunnel artifacts.
    root = Path(__file__).resolve().parents[2] / "Logs" / "VPN_Tunnel"
    root.mkdir(parents=True, exist_ok=True)
    return root / "remote_shell.log"


def _write_log(message: str) -> None:
    # Lightweight file logger for the shell bridge; avoid raising on failures.
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [vpn-shell] {message}\n")
    except Exception:
        pass


def _b64encode(data: bytes) -> str:
    # Wire payloads are JSON lines; encode binary stdout safely.
    return base64.b64encode(data).decode("ascii").strip()


def _b64decode(value: str) -> bytes:
    # Decode base64-encoded stdin payloads from the engine.
    return base64.b64decode(value.encode("ascii"))

def _resolve_shell_port() -> int:
    # Use the configured port when present, otherwise default to 47002.
    raw = os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT")
    try:
        value = int(raw) if raw is not None else 47002
    except Exception:
        value = 47002
    if value < 1 or value > 65535:
        return 47002
    return value


class ShellSession:
    def __init__(self, conn: socket.socket, address: tuple[str, int]) -> None:
        self.conn = conn
        self.address = address
        self.proc: Optional[subprocess.Popen] = None
        self._stop = threading.Event()
        self.input_messages = 0
        self.input_bytes = 0
        self.output_lines = 0
        self.output_bytes = 0

    def start(self) -> None:
        # Spawn an interactive PowerShell process and bridge stdin/stdout.
        _write_log(f"Shell session starting for {self.address[0]}:{self.address[1]}")
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
        # Forward PowerShell stdout to the engine as JSONL payloads.
        if not self.proc or not self.proc.stdout:
            return
        try:
            while not self._stop.is_set():
                chunk = self.proc.stdout.readline()
                if not chunk:
                    break
                self.output_lines += 1
                self.output_bytes += len(chunk)
                payload = json.dumps({"type": "stdout", "data": _b64encode(chunk)})
                try:
                    self.conn.sendall(payload.encode("utf-8") + b"\n")
                except Exception as exc:
                    _write_log(f"Shell stdout send failed: {exc}")
                    break
                _write_log(f"Shell stdout forwarded bytes={len(chunk)}")
        except Exception as exc:
            _write_log(f"Shell stdout error: {exc}")

    def _writer_loop(self) -> None:
        # Read JSONL stdin from the engine and feed it into PowerShell.
        buffer = b""
        try:
            while not self._stop.is_set():
                try:
                    data = self.conn.recv(4096)
                except Exception as exc:
                    _write_log(f"Shell stdin recv error: {exc}")
                    break
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
                                decoded = _b64decode(str(payload))
                                self.proc.stdin.write(decoded)
                                self.proc.stdin.flush()
                                self.input_messages += 1
                                self.input_bytes += len(decoded)
                                _write_log(f"Shell stdin received bytes={len(decoded)}")
                            except Exception:
                                _write_log("Shell stdin write failed.")
                    if msg.get("type") == "close":
                        self._stop.set()
                        _write_log("Shell close requested by engine.")
                        break
        finally:
            self.close()

    def close(self) -> None:
        # Ensure the TCP connection and PowerShell child are cleaned up.
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
        _write_log(
            "Shell session closed inputs={0} input_bytes={1} output_lines={2} output_bytes={3}".format(
                self.input_messages,
                self.input_bytes,
                self.output_lines,
                self.output_bytes,
            )
        )


class ShellServer:
    def __init__(self, host: str = "0.0.0.0", port: Optional[int] = None) -> None:
        self.host = host
        self.port = port or _resolve_shell_port()
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()
        _write_log(f"VPN shell server listening on {self.host}:{self.port}")

    def _serve(self) -> None:
        # Accept TCP shell connections; restrict to the WireGuard subnet.
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
        # Start the shell server immediately when the role loads.
        self.ctx = ctx
        self.server = ShellServer()

    def register_events(self) -> None:
        return

    def stop_all(self) -> None:
        return
