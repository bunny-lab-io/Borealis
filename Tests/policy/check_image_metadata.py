#!/usr/bin/env python3
"""Validate lightweight contracts for locally built Borealis images."""

from __future__ import annotations

import argparse
import json
import platform
import sys
from pathlib import Path


EXPECTED = {
    "api-backend": ("borealis-api-backend", "5000/tcp"),
    "borealis-operator": ("borealis-api-backend-go", "8088/tcp"),
    "job-scheduler": ("borealis-job-scheduler", None),
    "postgres-db": ("docker-entrypoint.sh", "5432/tcp"),
    "remote-desktop-guacd": ("borealis-guacd-entrypoint", "4822/tcp"),
    "site-worker": ("borealis-site-worker", None),
    "traefik-edge": ("borealis-traefik-edge", "443/tcp"),
    "webui-frontend": ("borealis-webui-frontend", "8000/tcp"),
    "wireguard-tunnel": ("borealis-wireguard-control", "30000/udp"),
}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--service", required=True, choices=sorted(EXPECTED))
    parser.add_argument("--target", default="")
    parser.add_argument("--inspect", required=True, type=Path)
    args = parser.parse_args()
    payload = json.loads(args.inspect.read_text(encoding="utf-8"))[0]
    config = payload.get("Config", {})
    entrypoint = " ".join(config.get("Entrypoint") or [])
    expected_entrypoint, expected_port = EXPECTED[args.service]
    failures = []
    if expected_entrypoint not in entrypoint:
        failures.append(f"entrypoint {entrypoint!r} lacks {expected_entrypoint!r}")
    exposed = config.get("ExposedPorts") or {}
    if expected_port and expected_port not in exposed:
        failures.append(f"exposed ports {sorted(exposed)} lack {expected_port}")
    machine = platform.machine().lower()
    expected_arch = "arm64" if machine in {"aarch64", "arm64"} else "amd64"
    if payload.get("Architecture") != expected_arch:
        failures.append(f"architecture {payload.get('Architecture')!r}, expected {expected_arch!r}")
    if args.service == "borealis-operator" and config.get("User") not in {"borealis-engine:borealis-engine", "64646:64646"}:
        failures.append(f"operator image user is not documented non-root identity: {config.get('User')!r}")
    if args.service == "webui-frontend" and args.target == "prod" and config.get("Env"):
        if "NODE_ENV=production" not in config["Env"]:
            failures.append("production WebUI image lacks NODE_ENV=production")
    if failures:
        print(f"IMAGE POLICY FAIL: {args.service}{':' + args.target if args.target else ''}: " + "; ".join(failures), file=sys.stderr)
        return 1
    print(f"Image metadata passed: {args.service}{':' + args.target if args.target else ''}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
