#!/usr/bin/env python3
"""Unix socket command proxy for Borealis WireGuard runtime operations."""

from __future__ import annotations

import json
import os
import ipaddress
import re
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
ALLOWED_COMMANDS = {"wg", "wg-quick", "ip", "iptables"}
DEFAULT_ENGINE_PREFIX = "10.255.0.1/32"
DEFAULT_PEER_NETWORK = "10.255.0.0/16"
WIREGUARD_INTERFACE_RE = re.compile(r"^[A-Za-z0-9_.-]{1,15}$")
WIREGUARD_KEY_RE = re.compile(r"^[A-Za-z0-9+/]{40,60}={0,2}$")
WIREGUARD_CHAINS = {"BOREALIS-WG-INPUT", "BOREALIS-WG-FWD"}
WIREGUARD_PARENT_CHAINS = {"INPUT", "FORWARD"}
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


def _is_interface(value: Any) -> bool:
    return bool(WIREGUARD_INTERFACE_RE.fullmatch(str(value or "")))


def _is_port(value: Any) -> bool:
    try:
        port = int(str(value or "").strip())
    except Exception:
        return False
    return 1 <= port <= 65535


def _is_path_under(path: Any, root: Path, suffix: str = "") -> bool:
    try:
        resolved = Path(str(path or "")).resolve()
        root_resolved = root.resolve()
    except Exception:
        return False
    if suffix and resolved.suffix != suffix:
        return False
    return resolved == root_resolved or root_resolved in resolved.parents


def _parse_ipv4_prefix(value: Any) -> ipaddress.IPv4Network | None:
    try:
        network = ipaddress.ip_network(str(value or "").strip(), strict=False)
    except Exception:
        return None
    if network.version != 4:
        return None
    return network


def _is_private_ipv4(network: ipaddress.IPv4Network | None) -> bool:
    return network is not None and network.network_address.is_private


def _wireguard_networks() -> tuple[ipaddress.IPv4Network, ipaddress.IPv4Network]:
    engine = _parse_ipv4_prefix(os.environ.get("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP") or DEFAULT_ENGINE_PREFIX)
    peer = _parse_ipv4_prefix(os.environ.get("BOREALIS_WIREGUARD_PEER_NETWORK") or DEFAULT_PEER_NETWORK)
    if (
        engine is None
        or peer is None
        or engine.prefixlen != 32
        or not _is_private_ipv4(engine)
        or not _is_private_ipv4(peer)
        or peer.prefixlen < 16
        or peer.prefixlen > 30
        or engine.network_address not in peer
    ):
        return ipaddress.ip_network(DEFAULT_ENGINE_PREFIX), ipaddress.ip_network(DEFAULT_PEER_NETWORK)
    return engine, peer


def _is_wireguard_engine_prefix(value: Any) -> bool:
    engine, _ = _wireguard_networks()
    return _parse_ipv4_prefix(value) == engine


def _is_wireguard_peer_network(value: Any) -> bool:
    _, peer = _wireguard_networks()
    return _parse_ipv4_prefix(value) == peer


def _is_wireguard_peer_host(value: Any) -> bool:
    engine, peer = _wireguard_networks()
    network = _parse_ipv4_prefix(value)
    return network is not None and network.prefixlen == 32 and network.network_address in peer and network.network_address != engine.network_address


def _validate_wg(args: list[str]) -> None:
    if len(args) in {3, 4} and args[1] == "show" and _is_interface(args[2]):
        if len(args) == 3 or args[3] in {"peers", "latest-handshakes"}:
            return
    if len(args) == 7 and args[1] == "set" and _is_interface(args[2]) and args[3] == "peer" and WIREGUARD_KEY_RE.fullmatch(args[4]) and args[5] == "allowed-ips" and _is_wireguard_peer_host(args[6]):
        return
    if len(args) == 6 and args[1] == "set" and _is_interface(args[2]) and args[3] == "peer" and WIREGUARD_KEY_RE.fullmatch(args[4]) and args[5] == "remove":
        return
    if len(args) == 7 and args[1] == "set" and _is_interface(args[2]) and args[3] == "listen-port" and _is_port(args[4]) and args[5] == "private-key" and _is_path_under(args[6], SERVICE_ROOT / "secrets", ".key"):
        return
    raise ValueError("command_shape_not_allowed:wg")


def _validate_wg_quick(args: list[str]) -> None:
    if len(args) == 3 and args[1] == "up" and _is_path_under(args[2], SERVICE_ROOT / "config", ".conf"):
        return
    raise ValueError("command_shape_not_allowed:wg-quick")


def _validate_ip(args: list[str]) -> None:
    if len(args) == 6 and args[1:3] == ["address", "replace"] and _is_wireguard_engine_prefix(args[3]) and args[4] == "dev" and _is_interface(args[5]):
        return
    if len(args) == 6 and args[1:3] == ["route", "replace"] and _is_wireguard_peer_network(args[3]) and args[4] == "dev" and _is_interface(args[5]):
        return
    if len(args) == 6 and args[1:4] == ["link", "set", "up"] and args[4] == "dev" and _is_interface(args[5]):
        return
    if len(args) == 5 and args[1:4] == ["link", "show", "dev"] and _is_interface(args[4]):
        return
    raise ValueError("command_shape_not_allowed:ip")


def _validate_iptables(args: list[str]) -> None:
    if len(args) == 3 and args[1] in {"-N", "-F"} and args[2] in WIREGUARD_CHAINS:
        return
    if len(args) >= 4 and args[1] == "-A":
        chain = args[2]
        tail = args[3:]
        if chain == "BOREALIS-WG-INPUT":
            if tail == ["-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid ingress", "-j", "DROP"]:
                return
            if tail == ["-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established return", "-j", "ACCEPT"]:
                return
            if len(tail) == 8 and tail[0] == "-s" and _is_wireguard_peer_network(tail[1]) and tail[2:] == ["-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"]:
                return
        if chain == "BOREALIS-WG-FWD":
            if tail == ["-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid forward", "-j", "DROP"]:
                return
            if tail == ["-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established forward", "-j", "ACCEPT"]:
                return
            if len(tail) == 10 and tail[0] == "-i" and _is_interface(tail[1]) and tail[2] == "-o" and tail[1] == tail[3] and tail[4:] == ["-m", "comment", "--comment", "borealis deny agent lateral wg", "-j", "DROP"]:
                return
            if len(tail) == 8 and tail[0] == "-s" and _is_wireguard_peer_network(tail[1]) and tail[2:] == ["-m", "comment", "--comment", "borealis deny agent forwarding", "-j", "DROP"]:
                return
    if len(args) == 7 and args[1] == "-C" and args[2] in WIREGUARD_PARENT_CHAINS and args[3] == "-i" and _is_interface(args[4]) and args[5] == "-j" and args[6] in WIREGUARD_CHAINS:
        return
    if len(args) == 8 and args[1] == "-I" and args[2] in WIREGUARD_PARENT_CHAINS and args[3] == "1" and args[4] == "-i" and _is_interface(args[5]) and args[6] == "-j" and args[7] in WIREGUARD_CHAINS:
        return
    raise ValueError("command_shape_not_allowed:iptables")


def _validate_command(args: list[str]) -> None:
    name = Path(str(args[0] or "")).name if args else ""
    if name == "wg":
        _validate_wg(args)
    elif name == "wg-quick":
        _validate_wg_quick(args)
    elif name == "ip":
        _validate_ip(args)
    elif name == "iptables":
        _validate_iptables(args)
    else:
        raise ValueError(f"command_not_allowed:{name}")


def _run_command(args: list[Any], timeout: int) -> dict[str, Any]:
    if not args:
        return {"returncode": 2, "stdout": "", "stderr": "missing command"}
    try:
        command = [str(item) for item in args]
        _validate_command(command)
        command = [_resolve_executable(command[0]), *command[1:]]
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
    elif command == "ping":
        response = {"returncode": 0, "stdout": "pong", "stderr": ""}
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
