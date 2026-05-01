"""Device factories and setup helpers for Engine unit tests."""

from __future__ import annotations

import json
import time
from dataclasses import asdict, dataclass
from typing import Any

from Data.Engine.services.API.devices.service_inventory import serialize_device_services

from .engine import execute_sql

DEFAULT_DEVICE_GUID = "GUID-TEST-0001"
DEFAULT_DEVICE_HOSTNAME = "test-device"
DEFAULT_DEVICE_AGENT_ID = "test-device-agent"
DEFAULT_DEVICE_SITE_ID = 1


@dataclass(frozen=True)
class TestDevice:
    guid: str = DEFAULT_DEVICE_GUID
    hostname: str = DEFAULT_DEVICE_HOSTNAME
    description: str = "Test device for Engine API"
    created_at: int = 1_700_000_000
    agent_hash: str = "hash-123"
    memory: str = json.dumps([{"slot": "DIMM1", "size_gb": 16}])
    network: str = json.dumps([{"iface": "eth0", "mac": "00:11:22:33:44:55"}])
    software: str = json.dumps(["sample-app"])
    storage: str = json.dumps([{"drive": "C", "size_gb": 256}])
    cpu: str = json.dumps({"name": "Intel", "cores": 8})
    device_type: str = "Workstation"
    domain: str = "example.local"
    external_ip: str = "203.0.113.5"
    internal_ip: str = "10.0.0.10"
    last_reboot: str = "2025-10-01T00:00:00Z"
    last_seen: int = 1_700_000_500
    last_user: str = "Alice"
    operating_system: str = "Windows 11 Pro"
    uptime: int = 7200
    agent_id: str = DEFAULT_DEVICE_AGENT_ID
    connection_type: str = ""
    connection_endpoint: str = ""
    ssl_key_fingerprint: str = "FF:FF:FF"
    token_version: int = 1
    status: str = "active"
    key_added_at: str = "2025-10-01T00:00:00Z"


_DEVICE_INSERT_COLUMNS = tuple(TestDevice.__dataclass_fields__.keys())
_DEVICE_UPDATE_COLUMNS = set(_DEVICE_INSERT_COLUMNS) | {
    "cpu_percent",
    "memory_percent",
    "processes",
    "services",
    "sessions",
}


def make_device(**overrides: Any) -> TestDevice:
    """Build device row with valid Borealis defaults and explicit overrides."""
    unknown = set(overrides) - set(_DEVICE_INSERT_COLUMNS)
    if unknown:
        raise ValueError(f"Unknown TestDevice fields: {', '.join(sorted(unknown))}")
    return TestDevice(**overrides)


def insert_device(harness: Any, device: TestDevice | None = None, *, replace: bool = False, **overrides: Any) -> TestDevice:
    """Insert complete device row into harness DB."""
    device = device or make_device(**overrides)
    values = asdict(device)
    placeholders = ", ".join("?" for _column in _DEVICE_INSERT_COLUMNS)
    columns = ", ".join(_DEVICE_INSERT_COLUMNS)
    verb = "INSERT OR REPLACE" if replace else "INSERT"
    execute_sql(
        harness,
        f"{verb} INTO devices ({columns}) VALUES ({placeholders})",
        [values[column] for column in _DEVICE_INSERT_COLUMNS],
    )
    return device


def update_device(harness: Any, *, hostname: str = DEFAULT_DEVICE_HOSTNAME, **fields: Any) -> None:
    """Update one seeded device by hostname."""
    if not fields:
        return
    unknown = set(fields) - _DEVICE_UPDATE_COLUMNS
    if unknown:
        raise ValueError(f"Unknown device columns: {', '.join(sorted(unknown))}")
    assignments = ", ".join(f"{column} = ?" for column in fields)
    execute_sql(
        harness,
        f"UPDATE devices SET {assignments} WHERE hostname = ?",
        [*fields.values(), hostname],
    )


def set_seed_device_guid(harness: Any, guid: str) -> None:
    update_device(harness, guid=guid)


def set_seed_device_agent_id(harness: Any, agent_id: str) -> None:
    update_device(harness, agent_id=agent_id)


def set_device_storage(harness: Any, *, hostname: str = DEFAULT_DEVICE_HOSTNAME, entries: list[dict] | None = None) -> None:
    update_device(
        harness,
        hostname=hostname,
        storage=json.dumps(
            entries
            or [
                {
                    "drive": "C",
                    "total": 1000,
                    "used": 930,
                    "free": 70,
                    "usage": 93,
                    "disk_type": "Fixed Disk",
                }
            ]
        ),
    )


def set_device_services(harness: Any, payload: Any, *, hostname: str = DEFAULT_DEVICE_HOSTNAME) -> None:
    update_device(harness, hostname=hostname, services=serialize_device_services(payload))


def set_device_software(harness: Any, payload: Any, *, hostname: str = DEFAULT_DEVICE_HOSTNAME) -> None:
    update_device(harness, hostname=hostname, software=json.dumps(payload or []))


def set_device_sessions(
    harness: Any,
    *,
    hostname: str = DEFAULT_DEVICE_HOSTNAME,
    sessions: list[dict],
    reported_at: int | None = None,
) -> None:
    update_device(
        harness,
        hostname=hostname,
        sessions=json.dumps({"reported_at": int(reported_at or time.time()), "sessions": sessions}),
    )


def set_device_processes(
    harness: Any,
    *,
    hostname: str = DEFAULT_DEVICE_HOSTNAME,
    processes: list[dict],
    reported_at: int | None = None,
) -> None:
    update_device(
        harness,
        hostname=hostname,
        processes=json.dumps({"reported_at": int(reported_at or time.time()), "processes": processes}),
    )


def set_device_last_seen(harness: Any, *, hostname: str = DEFAULT_DEVICE_HOSTNAME, last_seen: int) -> None:
    update_device(harness, hostname=hostname, last_seen=int(last_seen))


def set_device_uptime(harness: Any, *, hostname: str = DEFAULT_DEVICE_HOSTNAME, uptime: int) -> None:
    update_device(harness, hostname=hostname, uptime=int(uptime))


def set_device_metrics(
    harness: Any,
    *,
    hostname: str = DEFAULT_DEVICE_HOSTNAME,
    cpu_percent: float | None = None,
    memory_percent: float | None = None,
) -> None:
    fields: dict[str, float] = {}
    if cpu_percent is not None:
        fields["cpu_percent"] = float(cpu_percent)
    if memory_percent is not None:
        fields["memory_percent"] = float(memory_percent)
    update_device(harness, hostname=hostname, **fields)
