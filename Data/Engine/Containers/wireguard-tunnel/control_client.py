#!/usr/bin/env python3
"""Small CLI for Borealis WireGuard control socket checks."""

from __future__ import annotations

import json
import os
import socket
import sys
from pathlib import Path


def main(argv: list[str]) -> int:
    command = argv[1] if len(argv) > 1 else "status"
    socket_path = Path(
        os.environ.get("BOREALIS_WIREGUARD_CONTROL_SOCKET")
        or "/opt/Borealis/Engine/Services/wireguard-tunnel/run/control.sock"
    )
    payload = {"command": command}
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
        client.settimeout(10)
        client.connect(str(socket_path))
        client.sendall((json.dumps(payload) + "\n").encode("utf-8"))
        raw = client.recv(1024 * 1024)
    sys.stdout.write(raw.decode("utf-8", errors="replace"))
    try:
        response = json.loads(raw.decode("utf-8"))
    except Exception:
        return 1
    return int(response.get("returncode") or 0)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
