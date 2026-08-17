#!/usr/bin/env python3
"""Validate Borealis Engine retired Docker Compose policy."""

from __future__ import annotations

import json
import pathlib
import subprocess
import sys
import os
from typing import Any


ROOT = pathlib.Path(__file__).resolve().parents[3]
COMPOSE_FILE = ROOT / "Data" / "Engine" / "Containers" / "compose.yaml"
ENV_FILE = ROOT / "Data" / "Engine" / "Containers" / "compose.env.example"


def fail(message: str) -> None:
    print(f"POLICY FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def compose_config() -> dict[str, Any]:
    command = (["sudo"] if os.environ.get("BOREALIS_DOCKER_USE_SUDO") == "1" else []) + [
        "docker",
        "compose",
        "--env-file",
        str(ENV_FILE),
        "-f",
        str(COMPOSE_FILE),
        "config",
        "--format",
        "json",
    ]
    result = subprocess.run(command, cwd=ROOT, text=True, capture_output=True, check=False)
    if result.returncode != 0:
        fail(result.stderr.strip() or result.stdout.strip() or "docker compose config failed")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        fail(f"docker compose config did not return JSON: {exc}")


def assert_compose_manifest_retired(config: dict[str, Any]) -> None:
    services = config.get("services") or {}
    if not isinstance(services, dict):
        fail("docker compose config missing services map")
    if services:
        service_list = ", ".join(sorted(str(name) for name in services))
        fail(f"compose.yaml must remain retired with no services: {service_list}")


def main() -> int:
    assert_compose_manifest_retired(compose_config())
    print("POLICY PASS: Engine Compose manifest retired")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
