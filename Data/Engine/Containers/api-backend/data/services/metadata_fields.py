# ======================================================
# Data\Engine\services\metadata_fields.py
# Description: Shared Agent Metadata Field helpers for definitions, device values, and agent heartbeat sync.
#
# API Endpoints (if applicable): None
# ======================================================

"""Shared helpers for fixed Agent Metadata Fields."""

from __future__ import annotations

import base64
import binascii
import re
import time
from typing import Any, Dict, Iterable, List, Mapping, Optional, Tuple

from Data.Engine.db import dbapi as sqlite3

METADATA_FIELD_COUNT = 500
METADATA_VALUE_MAX_LENGTH = 1024
METADATA_FUTURE_SKEW_SECONDS = 300
RESERVED_METADATA_FIELD_TOOLTIP = (
    "Reserved Borealis Metadata Field - Create a scheduled job using the hyperlinked assembly to collect data for this field."
)
RESERVED_METADATA_PLACEHOLDER_TOOLTIP = "Reserved Borealis Metadata Field - Reserved for future Borealis use."
RESERVED_METADATA_FIELDS = {
    1: {
        "description": "Server Roles",
        "assembly_name": "Detect Server Roles [WIN]",
        "assembly_guid": "628f6686-c7c4-477d-bf9a-13c73d8246ba",
        "assembly_type": "script",
    },
    2: {
        "description": "Bitlocker Drive Encryption",
        "assembly_name": "Audit Bitlocker / TPM Status [WIN]",
        "assembly_guid": "c4f97974-1d9c-4e89-8257-8a139637e51f",
        "assembly_type": "script",
    },
    3: {"description": "Reserved"},
    4: {"description": "Reserved"},
    5: {"description": "Reserved"},
    6: {"description": "Reserved"},
    7: {"description": "Reserved"},
    8: {"description": "Reserved"},
    9: {"description": "Reserved"},
    10: {"description": "Reserved"},
}

_FIELD_KEY_PATTERN = re.compile(r"field[_\s-]*(\d{1,3})$", re.IGNORECASE)


def metadata_field_key(field_number: int) -> str:
    return f"field_{int(field_number):03d}"


def metadata_field_label(field_number: int) -> str:
    return f"Field {int(field_number):03d}"


def reserved_metadata_assembly_path(reserved: Mapping[str, Any]) -> str:
    assembly_type = str(reserved.get("assembly_type") or "script").strip().lower()
    assembly_guid = str(reserved.get("assembly_guid") or "").strip()
    if not assembly_guid:
        return ""
    if assembly_type == "ansible_playbook":
        return f"/assemblies/ansible_playbooks/{assembly_guid}"
    if assembly_type == "workflow":
        return f"/assemblies/workflows/{assembly_guid}"
    return f"/assemblies/scripts/{assembly_guid}"


def normalize_field_number(value: Any) -> Optional[int]:
    if isinstance(value, bool):
        return None
    try:
        if isinstance(value, (int, float)):
            parsed = int(value)
            if float(value) != float(parsed):
                return None
            return parsed if 1 <= parsed <= METADATA_FIELD_COUNT else None
    except Exception:
        return None
    text = str(value or "").strip()
    if not text:
        return None
    match = _FIELD_KEY_PATTERN.search(text)
    if match:
        text = match.group(1)
    else:
        text = text.lower().replace("metadata", "").replace("field", "").strip(" _-")
    if not text.isdigit():
        return None
    parsed = int(text)
    return parsed if 1 <= parsed <= METADATA_FIELD_COUNT else None


def reserved_metadata_tooltip(reserved: Mapping[str, Any]) -> str:
    assembly_guid = str(reserved.get("assembly_guid") or "").strip()
    return RESERVED_METADATA_FIELD_TOOLTIP if assembly_guid else RESERVED_METADATA_PLACEHOLDER_TOOLTIP


def normalize_metadata_value(value: Any) -> str:
    if value is None:
        return ""
    try:
        text = str(value)
    except Exception:
        text = ""
    if len(text) > METADATA_VALUE_MAX_LENGTH:
        text = text[:METADATA_VALUE_MAX_LENGTH]
    return text


def encode_metadata_value(value: Any) -> str:
    text = normalize_metadata_value(value)
    if not text:
        return ""
    return base64.b64encode(text.encode("utf-8")).decode("ascii")


def decode_metadata_value(value: Any) -> str:
    if value is None:
        return ""
    try:
        encoded = str(value)
    except Exception:
        return ""
    if encoded == "":
        return ""
    try:
        decoded = base64.b64decode(encoded.encode("ascii"), validate=True)
        return normalize_metadata_value(decoded.decode("utf-8", errors="replace"))
    except (binascii.Error, UnicodeEncodeError, ValueError):
        return normalize_metadata_value(encoded)


def normalize_encoded_metadata_value(value: Any) -> str:
    return encode_metadata_value(decode_metadata_value(value))


def normalize_metadata_description(value: Any) -> str:
    text = normalize_metadata_value(value).strip()
    if not text:
        return ""
    return " ".join(line.strip() for line in text.splitlines() if line.strip())[:METADATA_VALUE_MAX_LENGTH]


def normalize_modified_at(value: Any, *, now_ts: Optional[int] = None, clamp_future: bool = True) -> int:
    now_value = int(now_ts if now_ts is not None else time.time())
    try:
        parsed = int(float(value))
    except Exception:
        parsed = now_value
    if parsed <= 0:
        parsed = now_value
    if clamp_future and parsed > now_value + METADATA_FUTURE_SKEW_SECONDS:
        return now_value
    return parsed


def normalize_agent_metadata_payload(
    raw_fields: Any,
    *,
    now_ts: Optional[int] = None,
    clamp_future: bool = True,
) -> Dict[int, Dict[str, Any]]:
    if not isinstance(raw_fields, Mapping):
        return {}
    normalized: Dict[int, Dict[str, Any]] = {}
    now_value = int(now_ts if now_ts is not None else time.time())
    for raw_key, raw_value in raw_fields.items():
        field_number = normalize_field_number(raw_key)
        if field_number is None and isinstance(raw_value, Mapping):
            field_number = normalize_field_number(
                raw_value.get("field_number")
                or raw_value.get("fieldNumber")
                or raw_value.get("number")
                or raw_value.get("field_key")
                or raw_value.get("fieldKey")
            )
        if field_number is None:
            continue
        if isinstance(raw_value, Mapping):
            value = raw_value.get("value", "")
            modified_at = normalize_modified_at(
                raw_value.get("modified_at") or raw_value.get("modifiedAt") or raw_value.get("modified"),
                now_ts=now_value,
                clamp_future=clamp_future,
            )
            source = str(raw_value.get("source") or "agent").strip() or "agent"
            actor = str(raw_value.get("actor") or raw_value.get("modified_by") or raw_value.get("modifiedBy") or "").strip()
        else:
            value = raw_value
            modified_at = now_value
            source = "agent"
            actor = ""
        normalized[field_number] = {
            "field_number": field_number,
            "field_key": metadata_field_key(field_number),
            "value": normalize_encoded_metadata_value(value),
            "modified_at": modified_at,
            "source": source[:64],
            "actor": actor[:255],
        }
    return normalized


def list_metadata_definitions(conn: sqlite3.Connection) -> List[Dict[str, Any]]:
    descriptions: Dict[int, Dict[str, Any]] = {}
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT field_number, description, updated_at, updated_by
              FROM metadata_field_definitions
             WHERE field_number BETWEEN 1 AND ?
            """,
            (METADATA_FIELD_COUNT,),
        )
        for row in cur.fetchall() or []:
            field_number = normalize_field_number(row[0])
            if field_number is None:
                continue
            descriptions[field_number] = {
                "description": str(row[1] or ""),
                "updated_at": int(row[2] or 0),
                "updated_by": str(row[3] or ""),
            }
    except Exception:
        descriptions = {}

    fields: List[Dict[str, Any]] = []
    for field_number in range(1, METADATA_FIELD_COUNT + 1):
        default_label = metadata_field_label(field_number)
        definition = descriptions.get(field_number) or {}
        description = str(definition.get("description") or "").strip()
        reserved = RESERVED_METADATA_FIELDS.get(field_number)
        if reserved:
            description = str(reserved["description"])
        fields.append(
            {
                "field_number": field_number,
                "field_key": metadata_field_key(field_number),
                "default_label": default_label,
                "label": description or default_label,
                "description": description,
                "updated_at": 0 if reserved else int(definition.get("updated_at") or 0),
                "updated_by": "Borealis" if reserved else str(definition.get("updated_by") or ""),
                "value_limit": METADATA_VALUE_MAX_LENGTH,
                "reserved": bool(reserved),
                **reserved_metadata_payload(reserved),
            }
        )
    return fields


def reserved_metadata_payload(reserved: Optional[Mapping[str, Any]]) -> Dict[str, Any]:
    if not reserved:
        return {}
    payload: Dict[str, Any] = {"reserved_tooltip": reserved_metadata_tooltip(reserved)}
    assembly_guid = str(reserved.get("assembly_guid") or "").strip()
    if assembly_guid:
        assembly_name = str(reserved.get("assembly_name") or "").strip()
        assembly_type = str(reserved.get("assembly_type") or "script").strip() or "script"
        payload.update(
            {
                "linked_assembly": {
                    "guid": assembly_guid,
                    "name": assembly_name,
                    "type": assembly_type,
                    "path": reserved_metadata_assembly_path(reserved),
                },
                "linked_assembly_guid": assembly_guid,
                "linked_assembly_name": assembly_name,
                "linked_assembly_type": assembly_type,
                "linked_assembly_path": reserved_metadata_assembly_path(reserved),
            }
        )
    return payload


def upsert_metadata_definition(
    conn: sqlite3.Connection,
    field_number: int,
    description: Any,
    *,
    actor: str = "",
    updated_at: Optional[int] = None,
) -> Dict[str, Any]:
    parsed = normalize_field_number(field_number)
    if parsed is None:
        raise ValueError("field_number must be between 1 and 500")
    if parsed in RESERVED_METADATA_FIELDS:
        raise ValueError(reserved_metadata_tooltip(RESERVED_METADATA_FIELDS[parsed]))
    now_ts = int(updated_at if updated_at is not None else time.time())
    clean_description = normalize_metadata_description(description)
    conn.execute(
        """
        INSERT INTO metadata_field_definitions(field_number, description, updated_at, updated_by)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(field_number) DO UPDATE SET
            description = excluded.description,
            updated_at = excluded.updated_at,
            updated_by = excluded.updated_by
        """,
        (parsed, clean_description, now_ts, str(actor or "")[:255]),
    )
    definition = next(item for item in list_metadata_definitions(conn) if item["field_number"] == parsed)
    return definition


def fetch_device_metadata_values(conn: sqlite3.Connection, device_guid: str) -> Dict[int, Dict[str, Any]]:
    guid = str(device_guid or "").strip()
    if not guid:
        return {}
    values: Dict[int, Dict[str, Any]] = {}
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT field_number, field_key, value, modified_at, source, actor, created_at, updated_at
              FROM device_metadata_fields
             WHERE device_guid = ?
               AND field_number BETWEEN 1 AND ?
            """,
            (guid, METADATA_FIELD_COUNT),
        )
        for row in cur.fetchall() or []:
            field_number = normalize_field_number(row[0])
            if field_number is None:
                continue
            values[field_number] = {
                "field_number": field_number,
                "field_key": row[1] or metadata_field_key(field_number),
                "value": normalize_encoded_metadata_value(row[2]),
                "modified_at": int(row[3] or 0),
                "source": str(row[4] or ""),
                "actor": str(row[5] or ""),
                "created_at": int(row[6] or 0),
                "updated_at": int(row[7] or 0),
            }
    except Exception:
        return {}
    return values


def upsert_device_metadata_value(
    conn: sqlite3.Connection,
    device_guid: str,
    field_number: int,
    value: Any,
    *,
    modified_at: Optional[int] = None,
    source: str = "engine",
    actor: str = "",
    value_is_encoded: bool = False,
) -> Dict[str, Any]:
    guid = str(device_guid or "").strip()
    parsed = normalize_field_number(field_number)
    if not guid:
        raise ValueError("device_guid is required")
    if parsed is None:
        raise ValueError("field_number must be between 1 and 500")
    now_ts = int(time.time())
    modified_value = normalize_modified_at(modified_at, now_ts=now_ts, clamp_future=True)
    field_key = metadata_field_key(parsed)
    clean_value = normalize_encoded_metadata_value(value) if value_is_encoded else encode_metadata_value(value)
    conn.execute(
        """
        INSERT INTO device_metadata_fields(
            device_guid, field_number, field_key, value, modified_at, source, actor, created_at, updated_at
        )
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(device_guid, field_number) DO UPDATE SET
            field_key = excluded.field_key,
            value = excluded.value,
            modified_at = excluded.modified_at,
            source = excluded.source,
            actor = excluded.actor,
            updated_at = excluded.updated_at
        """,
        (
            guid,
            parsed,
            field_key,
            clean_value,
            modified_value,
            str(source or "engine")[:64],
            str(actor or "")[:255],
            now_ts,
            now_ts,
        ),
    )
    return fetch_device_metadata_values(conn, guid).get(parsed, {})


def device_metadata_rows(conn: sqlite3.Connection, device_guid: str) -> List[Dict[str, Any]]:
    definitions = list_metadata_definitions(conn)
    values = fetch_device_metadata_values(conn, device_guid)
    rows: List[Dict[str, Any]] = []
    for definition in definitions:
        field_number = int(definition["field_number"])
        value_record = values.get(field_number) or {}
        rows.append(
            {
                **definition,
                "value": decode_metadata_value(value_record.get("value", "")),
                "modified_at": int(value_record.get("modified_at") or 0),
                "source": value_record.get("source", ""),
                "actor": value_record.get("actor", ""),
                "has_value": bool(decode_metadata_value(value_record.get("value", ""))),
            }
        )
    return rows


def sparse_device_metadata_payload(conn: sqlite3.Connection, device_guid: str) -> Dict[str, Dict[str, Any]]:
    values = fetch_device_metadata_values(conn, device_guid)
    payload: Dict[str, Dict[str, Any]] = {}
    for field_number, record in values.items():
        encoded_value = normalize_encoded_metadata_value(record.get("value"))
        if not decode_metadata_value(encoded_value):
            continue
        key = metadata_field_key(field_number)
        payload[key] = {
            "value": encoded_value,
            "modified_at": int(record.get("modified_at") or 0),
            "source": str(record.get("source") or "engine"),
        }
    return payload


def process_agent_metadata_sync(
    conn: sqlite3.Connection,
    device_guid: str,
    raw_fields: Any,
    *,
    now_ts: Optional[int] = None,
) -> Dict[str, Any]:
    if not isinstance(raw_fields, Mapping):
        return {"updates": {}, "acks": []}
    guid = str(device_guid or "").strip()
    if not guid:
        return {"updates": {}, "acks": []}
    now_value = int(now_ts if now_ts is not None else time.time())
    incoming = normalize_agent_metadata_payload(raw_fields, now_ts=now_value, clamp_future=True)
    existing = fetch_device_metadata_values(conn, guid)
    incoming_numbers = set(incoming.keys())

    for field_number, record in incoming.items():
        current = existing.get(field_number)
        if current is None or int(record["modified_at"]) > int(current.get("modified_at") or 0):
            upsert_device_metadata_value(
                conn,
                guid,
                field_number,
                record.get("value", ""),
                modified_at=int(record.get("modified_at") or now_value),
                source="agent",
                actor=record.get("actor") or record.get("source") or "agent",
                value_is_encoded=True,
            )
        elif current and int(record["modified_at"]) == int(current.get("modified_at") or 0):
            if normalize_encoded_metadata_value(record.get("value")) == normalize_encoded_metadata_value(current.get("value")):
                continue

    latest = fetch_device_metadata_values(conn, guid)
    acks: List[str] = []
    for field_number in incoming_numbers:
        current = latest.get(field_number) or {}
        current_modified = int(current.get("modified_at") or 0)
        incoming_record = incoming.get(field_number)
        key = metadata_field_key(field_number)
        if incoming_record is None or not current:
            continue
        incoming_modified = int(incoming_record.get("modified_at") or 0)
        if current_modified >= incoming_modified:
            acks.append(key)
    return {"updates": {}, "acks": sorted(set(acks))}


def metadata_value_lookup_for_devices(
    conn: sqlite3.Connection,
    device_guids: Iterable[str],
) -> Dict[str, Dict[str, str]]:
    unique = sorted({str(guid or "").strip() for guid in device_guids if str(guid or "").strip()})
    if not unique:
        return {}
    lookup: Dict[str, Dict[str, str]] = {}
    try:
        placeholders = ",".join("?" for _ in unique)
        cur = conn.cursor()
        cur.execute(
            f"""
            SELECT device_guid, field_number, value
              FROM device_metadata_fields
             WHERE device_guid IN ({placeholders})
            """,
            tuple(unique),
        )
        for row in cur.fetchall() or []:
            guid = str(row[0] or "").strip()
            field_number = normalize_field_number(row[1])
            if not guid or field_number is None:
                continue
            lookup.setdefault(guid, {})[metadata_field_key(field_number)] = decode_metadata_value(row[2])
    except Exception:
        return {}
    return lookup
