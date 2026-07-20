#!/usr/bin/env python3
"""Validate Borealis Engine container least-privilege policy."""

from __future__ import annotations

import json
import pathlib
import subprocess
import sys
from typing import Any


ROOT = pathlib.Path(__file__).resolve().parents[3]
COMPOSE_FILE = ROOT / "Data" / "Engine" / "Containers" / "compose.yaml"
ENV_FILE = ROOT / "Data" / "Engine" / "Containers" / "compose.env.example"
ORCHESTRATOR_SOURCE = (
    ROOT
    / "Data"
    / "Engine"
    / "Containers"
    / "api-backend"
    / "cmd"
    / "api-backend"
    / "site_worker_orchestrator.go"
)

EXPECTED_SERVICES = {
    "docker-proxy",
    "site-worker-orchestrator",
    "traefik-edge",
    "postgres-db",
    "remote-desktop-guacd",
    "wireguard-tunnel",
}
RETIRED_COMPOSE_SERVICES = {"api-backend", "job-scheduler", "webui-frontend"}

ROOT_SERVICES = {"traefik-edge", "wireguard-tunnel"}
COMPATIBILITY_EXCEPTIONS: dict[str, str] = {}
DOCKER_SOCKET_SERVICES = {
    "docker-proxy": True,
    "site-worker-orchestrator": False,
}
CAP_ADD_ALLOWLIST = {
    "traefik-edge": {"DAC_OVERRIDE", "NET_BIND_SERVICE"},
    "wireguard-tunnel": {"NET_ADMIN", "NET_RAW"},
}


def fail(message: str) -> None:
    print(f"POLICY FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def compose_config() -> dict[str, Any]:
    command = [
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


def as_string_set(value: Any) -> set[str]:
    if value is None:
        return set()
    if isinstance(value, list):
        return {str(item) for item in value}
    return {str(value)}


def has_no_new_privileges(service: dict[str, Any]) -> bool:
    options = as_string_set(service.get("security_opt"))
    normalized = {option.replace("=", ":").lower() for option in options}
    return "no-new-privileges:true" in normalized


def has_tmpfs_path(service: dict[str, Any], path: str) -> bool:
    for entry in service.get("tmpfs") or []:
        if isinstance(entry, str) and entry.split(":", 1)[0] == path:
            return True
        if isinstance(entry, dict) and entry.get("target") == path:
            return True
    return False


def volume_parts(entry: Any) -> tuple[str, str, bool]:
    if isinstance(entry, dict):
        source = str(entry.get("source") or "")
        target = str(entry.get("target") or "")
        read_only = bool(entry.get("read_only") or entry.get("readonly"))
        return source, target, read_only
    text = str(entry)
    parts = text.split(":")
    source = parts[0] if parts else ""
    target = parts[1] if len(parts) > 1 else ""
    modes = set(parts[2:]) if len(parts) > 2 else set()
    return source, target, "ro" in modes or "readonly" in modes


def mount_by_target(service: dict[str, Any]) -> dict[str, tuple[str, bool]]:
    mounts: dict[str, tuple[str, bool]] = {}
    for entry in service.get("volumes") or []:
        source, target, read_only = volume_parts(entry)
        if target:
            mounts[target] = (source, read_only)
    return mounts


def require_mount(
    mounts: dict[str, tuple[str, bool]],
    service_name: str,
    target: str,
    *,
    read_only: bool,
) -> None:
    if target not in mounts:
        fail(f"{service_name} missing required mount target {target}")
    _, actual_read_only = mounts[target]
    if actual_read_only != read_only:
        mode = "read-only" if read_only else "read-write"
        fail(f"{service_name} mount {target} must be {mode}")


def forbid_mount_target_prefix(
    mounts: dict[str, tuple[str, bool]],
    service_name: str,
    prefixes: set[str],
    allowed: set[str] | None = None,
) -> None:
    allowed = allowed or set()
    for target in mounts:
        if target in allowed:
            continue
        if any(target == prefix or target.startswith(prefix.rstrip("/") + "/") for prefix in prefixes):
            fail(f"{service_name} must not mount broad target {target}")


def service_env(service: dict[str, Any], key: str) -> str:
    environment = service.get("environment") or {}
    if isinstance(environment, dict):
        return str(environment.get(key) or "")
    if isinstance(environment, list):
        prefix = f"{key}="
        for item in environment:
            text = str(item)
            if text.startswith(prefix):
                return text[len(prefix) :]
    return ""


def is_root_user(user: Any) -> bool:
    text = str(user or "").strip()
    return text in {"", "0", "0:0", "root", "root:root"} or text.startswith("0:")


def assert_static_service_policy(services: dict[str, Any]) -> None:
    missing = EXPECTED_SERVICES - set(services)
    if missing:
        fail(f"compose missing services: {', '.join(sorted(missing))}")
    retired = RETIRED_COMPOSE_SERVICES & set(services)
    if retired:
        fail(f"retired services must not be present in Compose: {', '.join(sorted(retired))}")

    for name in sorted(EXPECTED_SERVICES):
        service = services[name]
        compatibility_exception = name in COMPATIBILITY_EXCEPTIONS
        if service.get("privileged"):
            fail(f"{name} sets privileged=true")
        if not compatibility_exception and not has_no_new_privileges(service):
            fail(f"{name} missing no-new-privileges")
        if not compatibility_exception and "ALL" not in as_string_set(service.get("cap_drop")):
            fail(f"{name} must drop all capabilities")
        if not compatibility_exception and service.get("read_only") is not True:
            fail(f"{name} must use read_only root filesystem")
        if not compatibility_exception and not has_tmpfs_path(service, "/tmp"):
            fail(f"{name} missing tmpfs /tmp")
        if name == "docker-proxy" and not has_tmpfs_path(service, "/run"):
            fail("docker-proxy missing tmpfs /run")
        if not compatibility_exception and not service.get("pids_limit"):
            fail(f"{name} missing pids_limit")
        if not compatibility_exception and not service.get("mem_limit"):
            fail(f"{name} missing mem_limit")
        if not compatibility_exception and service.get("cpus") in {None, "", 0, 0.0}:
            fail(f"{name} missing cpus limit")

        user_is_root = is_root_user(service.get("user"))
        if name in ROOT_SERVICES:
            if not user_is_root:
                fail(f"{name} must remain explicit root exception")
        elif user_is_root and not compatibility_exception:
            fail(f"{name} must run as a non-root runtime user")

        cap_add = as_string_set(service.get("cap_add"))
        allowed_cap_add = CAP_ADD_ALLOWLIST.get(name, set())
        if cap_add != allowed_cap_add:
            fail(f"{name} cap_add={sorted(cap_add)} expected {sorted(allowed_cap_add)}")

        devices = service.get("devices") or []
        if name == "wireguard-tunnel":
            rendered_devices = {str(device) for device in devices}
            if not any("/dev/net/tun" in device for device in rendered_devices):
                fail("wireguard-tunnel must declare /dev/net/tun")
        elif devices:
            fail(f"{name} must not declare devices")

        socket_mounts = [
            volume_parts(entry)
            for entry in service.get("volumes") or []
            if volume_parts(entry)[1] == "/var/run/docker.sock"
        ]
        if name in DOCKER_SOCKET_SERVICES:
            if len(socket_mounts) != 1:
                fail(f"{name} must declare exactly one Docker socket mount")
            _, _, read_only = socket_mounts[0]
            expected_read_only = DOCKER_SOCKET_SERVICES[name]
            if read_only != expected_read_only:
                mode = "read-only" if expected_read_only else "read-write"
                fail(f"{name} Docker socket mount must be {mode}")
            group_add = as_string_set(service.get("group_add"))
            if not group_add:
                fail(f"{name} must add Docker socket GID explicitly")
        elif socket_mounts:
            fail(f"{name} must not mount Docker socket")

        if name == "remote-desktop-guacd":
            if service_env(service, "BOREALIS_GUACD_BIND_HOST") != "127.0.0.1":
                fail("remote-desktop-guacd must bind guacd to 127.0.0.1")
            if service.get("volumes"):
                fail("remote-desktop-guacd must not use host bind mounts")
        if name == "traefik-edge":
            depends_on = service.get("depends_on") or {}
            if "api-backend" in depends_on:
                fail("traefik-edge must not depend on Compose api-backend after K3s API cutover")
            if "webui-frontend" in depends_on:
                fail("traefik-edge must not depend on Compose webui-frontend after K3s WebUI cutover")

        mounts = mount_by_target(service)
        if name == "postgres-db" and "/var/log/postgresql" in mounts:
            fail("postgres-db must not mount host log directory")
        if name == "site-worker-orchestrator":
            forbid_mount_target_prefix(
                mounts,
                name,
                {
                    "/opt/Borealis/Engine/Services/api-backend",
                    "/opt/Borealis/Engine/Services/site-worker-orchestrator",
                    "/opt/Borealis/Engine/Services/traefik-edge",
                },
                allowed={
                    "/opt/Borealis/Engine/Services/api-backend/cache",
                    "/opt/Borealis/Engine/Services/api-backend/logs/site-workers",
                    "/opt/Borealis/Engine/Services/api-backend/secrets",
                    "/opt/Borealis/Engine/Services/site-worker-orchestrator/run",
                },
            )
            require_mount(mounts, name, "/opt/Borealis/Engine/Services/api-backend/cache", read_only=False)
            require_mount(mounts, name, "/opt/Borealis/Engine/Services/api-backend/logs/site-workers", read_only=False)
            require_mount(mounts, name, "/opt/Borealis/Engine/Services/api-backend/secrets", read_only=True)
            require_mount(mounts, name, "/opt/Borealis/Engine/Services/site-worker-orchestrator/run", read_only=False)

def assert_dynamic_orchestrator_policy() -> None:
    source = ORCHESTRATOR_SOURCE.read_text(encoding="utf-8")
    required = {
        "site-worker non-root user": '"--user", schedulerRuntimeUserSpec()',
        "site-worker no-new-privileges": '"--security-opt", "no-new-privileges:true"',
        "site-worker cap drop": '"--cap-drop", "ALL"',
        "site-worker read-only": '"--read-only"',
        "site-worker tmpfs": '"/tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777"',
        "site-worker memory": '"BOREALIS_SITE_WORKER_MEMORY_LIMIT", "256m"',
        "site-worker cpu": '"BOREALIS_SITE_WORKER_CPU_LIMIT", "1.00"',
        "site-worker pids": '"BOREALIS_SITE_WORKER_PIDS_LIMIT", "128"',
        "site-worker route file writes disabled": '"BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES=0"',
        "site-worker home": '"HOME=/tmp"',
        "service helper no-new-privileges": '"BOREALIS_SERVICE_ACTION_HELPER_MEMORY_LIMIT", "512m"',
        "service helper socket path": '"BOREALIS_DOCKER_SOCKET_PATH", "/var/run/docker.sock"',
    }
    for label, snippet in required.items():
        if snippet not in source:
            fail(f"orchestrator launch policy missing {label}")

    launch_body = source.split("func orchestratorSiteWorkerImageAllowed", 1)[0]
    if "/var/run/docker.sock" in launch_body:
        fail("site-worker launch must not mount Docker socket")
    if "Engine\", \"Services\", \"traefik-edge\"" in launch_body:
        fail("site-worker launch must not mount Traefik config")
    if "--privileged" in launch_body or "--cap-add" in launch_body or "--device" in launch_body:
        fail("site-worker launch must not allow privileged Docker flags")


def main() -> int:
    config = compose_config()
    services = config.get("services") or {}
    if not isinstance(services, dict):
        fail("docker compose config missing services map")
    assert_static_service_policy(services)
    assert_dynamic_orchestrator_policy()
    print("POLICY PASS: Engine Compose and orchestrator launch hardening validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
