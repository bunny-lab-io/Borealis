"""Device domain helpers mirroring the legacy server payloads."""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Dict, List, Mapping, Optional, Sequence

__all__ = [
    "DEVICE_TABLE_COLUMNS",
    "DEVICE_TABLE",
    "DeviceSnapshot",
    "assemble_device_snapshot",
    "row_to_device_dict",
    "serialize_device_json",
    "clean_device_str",
    "coerce_int",
    "ts_to_iso",
    "device_column_sql",
    "ts_to_human",
]


DEVICE_TABLE = "devices"

DEVICE_JSON_LIST_FIELDS: Mapping[str, List[Any]] = {
    "memory": [],
    "network": [],
    "software": [],
    "storage": [],
}

DEVICE_JSON_OBJECT_FIELDS: Mapping[str, Dict[str, Any]] = {
    "cpu": {},
}

DEVICE_TABLE_COLUMNS: Sequence[str] = (
    "guid",
    "hostname",
    "description",
    "created_at",
    "agent_hash",
    "memory",
    "network",
    "software",
    "storage",
    "cpu",
    "device_type",
    "domain",
    "external_ip",
    "internal_ip",
    "last_reboot",
    "last_seen",
    "last_user",
    "operating_system",
    "uptime",
    "agent_id",
    "ansible_ee_ver",
    "connection_type",
    "connection_endpoint",
    "ssl_key_fingerprint",
    "token_version",
    "status",
    "key_added_at",
)


@dataclass(frozen=True)
class DeviceSnapshot:
    hostname: str
    description: str
    created_at: int
    created_at_iso: str
    agent_hash: str
    agent_guid: str
    guid: str
    memory: List[Dict[str, Any]]
    network: List[Dict[str, Any]]
    software: List[Dict[str, Any]]
    storage: List[Dict[str, Any]]
    cpu: Dict[str, Any]
    device_type: str
    domain: str
    external_ip: str
    internal_ip: str
    last_reboot: str
    last_seen: int
    last_seen_iso: str
    last_user: str
    operating_system: str
    uptime: int
    agent_id: str
    connection_type: str
    connection_endpoint: str
    details: Dict[str, Any]
    summary: Dict[str, Any]

    def to_dict(self) -> Dict[str, Any]:
        return {
            "hostname": self.hostname,
            "description": self.description,
            "created_at": self.created_at,
            "created_at_iso": self.created_at_iso,
            "agent_hash": self.agent_hash,
            "agent_guid": self.agent_guid,
            "guid": self.guid,
            "memory": self.memory,
            "network": self.network,
            "software": self.software,
            "storage": self.storage,
            "cpu": self.cpu,
            "device_type": self.device_type,
            "domain": self.domain,
            "external_ip": self.external_ip,
            "internal_ip": self.internal_ip,
            "last_reboot": self.last_reboot,
            "last_seen": self.last_seen,
            "last_seen_iso": self.last_seen_iso,
            "last_user": self.last_user,
            "operating_system": self.operating_system,
            "uptime": self.uptime,
            "agent_id": self.agent_id,
            "connection_type": self.connection_type,
            "connection_endpoint": self.connection_endpoint,
            "details": self.details,
            "summary": self.summary,
        }


def ts_to_iso(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        return datetime.fromtimestamp(int(ts), timezone.utc).isoformat()
    except Exception:
        return ""


def _ts_to_human(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        return datetime.utcfromtimestamp(int(ts)).strftime("%Y-%m-%d %H:%M:%S")
    except Exception:
        return ""


def _parse_device_json(raw: Optional[str], default: Any) -> Any:
    if raw is None:
        return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    try:
        data = json.loads(raw)
    except Exception:
        data = None
    if isinstance(default, list):
        if isinstance(data, list):
            return data
        return []
    if isinstance(default, dict):
        if isinstance(data, dict):
            return data
        return {}
    return default


def serialize_device_json(value: Any, default: Any) -> str:
    candidate = value
    if candidate is None:
        candidate = default
    if not isinstance(candidate, (list, dict)):
        candidate = default
    try:
        return json.dumps(candidate)
    except Exception:
        try:
            return json.dumps(default)
        except Exception:
            return "{}" if isinstance(default, dict) else "[]"


def clean_device_str(value: Any) -> Optional[str]:
    if value is None:
        return None
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        text = str(value)
    elif isinstance(value, str):
        text = value
    else:
        try:
            text = str(value)
        except Exception:
            return None
    text = text.strip()
    return text or None


def coerce_int(value: Any) -> Optional[int]:
    if value is None:
        return None
    try:
        if isinstance(value, str) and value.strip() == "":
            return None
        return int(float(value))
    except (ValueError, TypeError):
        return None


def row_to_device_dict(row: Sequence[Any], columns: Sequence[str]) -> Dict[str, Any]:
    return {columns[idx]: row[idx] for idx in range(min(len(row), len(columns)))}


def assemble_device_snapshot(record: Mapping[str, Any]) -> Dict[str, Any]:
    summary = {
        "hostname": record.get("hostname") or "",
        "description": record.get("description") or "",
        "device_type": record.get("device_type") or "",
        "domain": record.get("domain") or "",
        "external_ip": record.get("external_ip") or "",
        "internal_ip": record.get("internal_ip") or "",
        "last_reboot": record.get("last_reboot") or "",
        "last_seen": record.get("last_seen") or 0,
        "last_user": record.get("last_user") or "",
        "operating_system": record.get("operating_system") or "",
        "uptime": record.get("uptime") or 0,
        "agent_id": record.get("agent_id") or "",
        "agent_hash": record.get("agent_hash") or "",
        "agent_guid": record.get("guid") or record.get("agent_guid") or "",
        "connection_type": record.get("connection_type") or "",
        "connection_endpoint": record.get("connection_endpoint") or "",
        "created_at": record.get("created_at") or 0,
    }

    created_ts = coerce_int(summary.get("created_at")) or 0
    last_seen_ts = coerce_int(summary.get("last_seen")) or 0
    uptime_val = coerce_int(summary.get("uptime")) or 0

    parsed_lists = {
        key: _parse_device_json(record.get(key), default)
        for key, default in DEVICE_JSON_LIST_FIELDS.items()
    }
    cpu_obj = _parse_device_json(record.get("cpu"), DEVICE_JSON_OBJECT_FIELDS["cpu"])

    details = {
        "memory": parsed_lists["memory"],
        "network": parsed_lists["network"],
        "software": parsed_lists["software"],
        "storage": parsed_lists["storage"],
        "cpu": cpu_obj,
    }

    payload: Dict[str, Any] = {
        "hostname": summary["hostname"],
        "description": summary.get("description", ""),
        "created_at": created_ts,
        "created_at_iso": ts_to_iso(created_ts),
        "agent_hash": summary.get("agent_hash", ""),
        "agent_guid": summary.get("agent_guid", ""),
        "guid": summary.get("agent_guid", ""),
        "memory": parsed_lists["memory"],
        "network": parsed_lists["network"],
        "software": parsed_lists["software"],
        "storage": parsed_lists["storage"],
        "cpu": cpu_obj,
        "device_type": summary.get("device_type", ""),
        "domain": summary.get("domain", ""),
        "external_ip": summary.get("external_ip", ""),
        "internal_ip": summary.get("internal_ip", ""),
        "last_reboot": summary.get("last_reboot", ""),
        "last_seen": last_seen_ts,
        "last_seen_iso": ts_to_iso(last_seen_ts),
        "last_user": summary.get("last_user", ""),
        "operating_system": summary.get("operating_system", ""),
        "uptime": uptime_val,
        "agent_id": summary.get("agent_id", ""),
        "connection_type": summary.get("connection_type", ""),
        "connection_endpoint": summary.get("connection_endpoint", ""),
        "details": details,
        "summary": summary,
    }
    return payload


def device_column_sql(alias: Optional[str] = None) -> str:
    if alias:
        return ", ".join(f"{alias}.{col}" for col in DEVICE_TABLE_COLUMNS)
    return ", ".join(DEVICE_TABLE_COLUMNS)


def ts_to_human(ts: Optional[int]) -> str:
    return _ts_to_human(ts)
