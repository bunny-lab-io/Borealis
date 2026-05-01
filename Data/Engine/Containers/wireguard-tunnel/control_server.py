#!/usr/bin/env python3
"""Unix socket command proxy for Borealis WireGuard runtime operations."""

from __future__ import annotations

import json
import os
import selectors
import shutil
import signal
import socket
import subprocess
import sys
import time
from pathlib import Path
from typing import Any


PROJECT_ROOT = Path(os.environ.get("BOREALIS_PROJECT_ROOT") or "/opt/Borealis")
SERVICE_ROOT = PROJECT_ROOT / "Engine" / "Services" / "wireguard-tunnel"
RUN_DIR = SERVICE_ROOT / "run"
LOG_DIR = SERVICE_ROOT / "logs"
SOCKET_PATH = Path(os.environ.get("BOREALIS_WIREGUARD_CONTROL_SOCKET") or RUN_DIR / "control.sock")
ALLOWED_COMMANDS = {"wg", "wg-quick", "ip", "iptables", "firewall-cmd"}
STOP = False


def log(message: str) -> None:
    LOG_DIR.mkdir(parents=True, exist_ok=True)
    line = f"[{time.strftime('%Y-%m-%dT%H:%M:%S%z')}] {message}\n"
    with (LOG_DIR / "control.log").open("a", encoding="utf-8") as handle:
        handle.write(line)
    print(line, end="", flush=True)


def _resolve_executable(raw: str) -> str:
    name = Path(str(raw or "")).name
    if name not in ALLOWED_COMMANDS:
        raise ValueError(f"command_not_allowed:{name}")
    resolved = shutil.which(name)
    if not resolved:
        raise ValueError(f"command_not_found:{name}")
    return resolved


def _run_command(args: list[Any], timeout: int) -> dict[str, Any]:
    if not args:
        return {"returncode": 2, "stdout": "", "stderr": "missing command"}
    try:
        command = [_resolve_executable(str(args[0])), *[str(item) for item in args[1:]]]
    except ValueError as exc:
        return {"returncode": 126, "stdout": "", "stderr": str(exc)}
    try:
        proc = subprocess.run(command, capture_output=True, text=True, check=False, timeout=max(1, timeout))
    except subprocess.TimeoutExpired as exc:
        return {
            "returncode": 124,
            "stdout": (exc.stdout or "").strip() if isinstance(exc.stdout, str) else "",
            "stderr": "command timed out",
        }
    except Exception as exc:
        return {"returncode": 1, "stdout": "", "stderr": str(exc)}
    return {
        "returncode": proc.returncode,
        "stdout": (proc.stdout or "").strip(),
        "stderr": (proc.stderr or "").strip(),
    }


def _status() -> dict[str, Any]:
    wg = shutil.which("wg")
    ip = shutil.which("ip")
    if not wg or not ip:
        return {"returncode": 1, "stdout": "", "stderr": "wg or ip missing"}
    proc = subprocess.run([wg, "show", "borealis-wg"], capture_output=True, text=True, check=False)
    return {
        "returncode": proc.returncode,
        "stdout": (proc.stdout or "").strip(),
        "stderr": (proc.stderr or "").strip(),
    }


def _handle_request(raw: bytes) -> bytes:
    try:
        payload = json.loads(raw.decode("utf-8"))
    except Exception as exc:
        response = {"returncode": 2, "stdout": "", "stderr": f"invalid json: {exc}"}
        return (json.dumps(response) + "\n").encode("utf-8")

    command = str(payload.get("command") or "run")
    if command == "run":
        response = _run_command(list(payload.get("args") or []), int(payload.get("timeout") or 30))
    elif command in {"status", "reconcile"}:
        response = _status()
    else:
        response = {"returncode": 2, "stdout": "", "stderr": f"unsupported command: {command}"}
    return (json.dumps(response) + "\n").encode("utf-8")


def _signal_handler(_signum: int, _frame: Any) -> None:
    global STOP
    STOP = True


def main() -> int:
    signal.signal(signal.SIGTERM, _signal_handler)
    signal.signal(signal.SIGINT, _signal_handler)
    RUN_DIR.mkdir(parents=True, exist_ok=True)
    LOG_DIR.mkdir(parents=True, exist_ok=True)
    if SOCKET_PATH.exists():
        SOCKET_PATH.unlink()

    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(str(SOCKET_PATH))
    os.chmod(SOCKET_PATH, 0o660)
    server.listen(16)
    server.setblocking(False)
    selector = selectors.DefaultSelector()
    selector.register(server, selectors.EVENT_READ)
    log(f"WireGuard control socket listening at {SOCKET_PATH}")

    try:
        while not STOP:
            for key, _events in selector.select(timeout=1.0):
                if key.fileobj is not server:
                    continue
                conn, _addr = server.accept()
                with conn:
                    chunks: list[bytes] = []
                    while True:
                        data = conn.recv(65536)
                        if not data:
                            break
                        chunks.append(data)
                        if b"\n" in data:
                            break
                    response = _handle_request(b"".join(chunks).splitlines()[0] if chunks else b"{}")
                    conn.sendall(response)
    finally:
        selector.close()
        server.close()
        try:
            SOCKET_PATH.unlink()
        except FileNotFoundError:
            pass
    log("WireGuard control socket stopped")
    return 0


if __name__ == "__main__":
    sys.exit(main())
