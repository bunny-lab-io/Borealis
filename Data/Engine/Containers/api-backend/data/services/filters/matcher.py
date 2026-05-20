# ======================================================
# Data\Engine\services\filters\matcher.py
# Description: Shared helpers for evaluating device filters and resolving
#              target lists for scheduled jobs and filter list summaries.
#
# API Endpoints (if applicable): None
# ======================================================

"""Utilities that evaluate typed device filters against the Engine inventory."""

from __future__ import annotations

import json
import re
from Data.Engine.db import dbapi as sqlite3
import time
from copy import deepcopy
from typing import Any, Callable, Dict, Iterable, List, Optional, Sequence, Tuple

try:  # Prefer PCRE-like semantics when available.
    import regex as _regex_engine  # type: ignore
except Exception:  # pragma: no cover - fallback for constrained environments
    _regex_engine = re

try:
    from packaging.version import InvalidVersion, Version
except Exception:  # pragma: no cover - fallback when dependency is unavailable
    Version = None  # type: ignore[assignment]

    class InvalidVersion(Exception):
        pass

from Data.Engine.auth.guid_utils import normalize_guid
from Data.Engine.services.metadata_fields import (
    METADATA_FIELD_COUNT,
    list_metadata_definitions,
    metadata_field_key,
    metadata_value_lookup_for_devices,
    normalize_field_number,
)


SITE_MODE_GLOBAL = "global"
SITE_MODE_SPECIFIC = "specific_sites"
SITE_MODE_EXCLUSIONS = "global_exclusions"
VALID_SITE_MODES = {SITE_MODE_GLOBAL, SITE_MODE_SPECIFIC, SITE_MODE_EXCLUSIONS}

CRITERIA_MODE_BASIC = "basic"
CRITERIA_MODE_ADVANCED = "advanced"
VALID_CRITERIA_MODES = {CRITERIA_MODE_BASIC, CRITERIA_MODE_ADVANCED}

TEXT_OPERATORS = [
    "contains",
    "does_not_contain",
    "equals",
    "begins_with",
    "ends_with",
]
NUMERIC_OPERATORS = [
    "equals",
    "greater_than",
    "greater_than_or_equal",
    "less_than",
    "less_than_or_equal",
]
SOFTWARE_VERSION_OPERATORS = {"matches", "older_than", "newer_than"}
SOFTWARE_SOURCES = {
    "local_installed": "Locally Installed",
    "windows_store": "Windows Store",
    "dpkg": "DPKG",
    "rpm": "RPM",
}

FILTER_FIELD_METADATA: List[Dict[str, Any]] = [
    {
        "value": "hostname",
        "label": "Hostname",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "description",
        "label": "Description",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "site_name",
        "label": "Site",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "operating_system",
        "label": "Operating System",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "device_type",
        "label": "Device Type",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "status",
        "label": "Status",
        "kind": "enum",
        "operators": ["equals"],
        "supports_regex": False,
    },
    {
        "value": "last_seen_age_minutes",
        "label": "Last Seen Age (Minutes)",
        "kind": "number",
        "operators": list(NUMERIC_OPERATORS),
        "supports_regex": False,
    },
    {
        "value": "last_user",
        "label": "Last User",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "internal_ip",
        "label": "Internal IP",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "external_ip",
        "label": "External IP",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "domain",
        "label": "Domain",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "total_ram_gb",
        "label": "Total RAM (GB)",
        "kind": "number",
        "operators": list(NUMERIC_OPERATORS),
        "supports_regex": False,
    },
    {
        "value": "storage_free_percent",
        "label": "Storage Free %",
        "kind": "number",
        "operators": list(NUMERIC_OPERATORS),
        "supports_regex": False,
    },
    {
        "value": "cpu_model",
        "label": "CPU Model",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "agent_version",
        "label": "Agent Version",
        "kind": "text",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
    },
    {
        "value": "installed_software",
        "label": "Installed Software",
        "kind": "software",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
        "supports_source": True,
        "supports_version": True,
    },
    {
        "value": "metadata_field",
        "label": "Metadata Field",
        "kind": "metadata_field",
        "operators": list(TEXT_OPERATORS),
        "supports_regex": True,
        "supports_field_picker": True,
    },
]

FILTER_FIELD_BY_ID: Dict[str, Dict[str, Any]] = {entry["value"]: entry for entry in FILTER_FIELD_METADATA}

FILTER_METADATA_PAYLOAD: Dict[str, Any] = {
    "site_modes": [
        {"value": SITE_MODE_GLOBAL, "label": "Global"},
        {"value": SITE_MODE_SPECIFIC, "label": "Specific Sites"},
        {"value": SITE_MODE_EXCLUSIONS, "label": "Global w/ Exclusions"},
    ],
    "fields": FILTER_FIELD_METADATA,
    "operators": {
        "text": [
            {"value": "contains", "label": "Contains"},
            {"value": "does_not_contain", "label": "Does Not Contain"},
            {"value": "equals", "label": "Equals"},
            {"value": "begins_with", "label": "Begins With"},
            {"value": "ends_with", "label": "Ends With"},
        ],
        "number": [
            {"value": "equals", "label": "Equals"},
            {"value": "greater_than", "label": "Greater Than"},
            {"value": "greater_than_or_equal", "label": "Greater Than or Equal"},
            {"value": "less_than", "label": "Less Than"},
            {"value": "less_than_or_equal", "label": "Less Than or Equal"},
        ],
        "enum": [
            {"value": "equals", "label": "Equals"},
        ],
        "software_version": [
            {"value": "matches", "label": "Matches"},
            {"value": "older_than", "label": "Older Than"},
            {"value": "newer_than", "label": "Newer Than"},
        ],
    },
    "software_sources": [
        {"value": key, "label": label} for key, label in SOFTWARE_SOURCES.items()
    ],
}

_DEVICE_SELECT_SQL = """
    SELECT
        d.guid,
        d.hostname,
        d.description,
        d.created_at,
        d.agent_hash,
        d.memory,
        d.network,
        d.software,
        d.storage,
        d.cpu,
        d.device_type,
        d.domain,
        d.external_ip,
        d.internal_ip,
        d.last_reboot,
        d.last_seen,
        d.last_user,
        d.operating_system,
        d.uptime,
        d.agent_id,
        d.connection_type,
        d.connection_endpoint,
        s.id AS site_id,
        s.name AS site_name,
        s.description AS site_description
    FROM devices AS d
    LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
    LEFT JOIN sites AS s ON s.id = ds.site_id
"""


def filter_metadata(*, db_conn_factory: Optional[Callable[[], sqlite3.Connection]] = None) -> Dict[str, Any]:
    """Return a deep copy of the filter metadata payload."""

    payload = deepcopy(FILTER_METADATA_PAYLOAD)
    fields: List[Dict[str, Any]] = []
    if db_conn_factory is not None:
        conn = db_conn_factory()
        try:
            fields = list_metadata_definitions(conn)
        except Exception:
            fields = []
        finally:
            conn.close()
    if not fields:
        fields = [
            {
                "field_number": number,
                "field_key": metadata_field_key(number),
                "default_label": f"Field {number:03d}",
                "label": f"Field {number:03d}",
                "description": "",
                "value_limit": 1024,
            }
            for number in range(1, METADATA_FIELD_COUNT + 1)
        ]
    payload["metadata_fields"] = fields
    return payload


def normalize_software_name(value: Any) -> str:
    try:
        text = str(value or "").strip().lower()
    except Exception:
        return ""
    return re.sub(r"\s+", " ", text)


def _clone_json(value: Any) -> Any:
    try:
        return json.loads(json.dumps(value))
    except Exception:
        return value


def _safe_json(raw: Optional[str], default: Any) -> Any:
    if raw is None:
        return _clone_json(default)
    try:
        parsed = json.loads(raw)
    except Exception:
        return _clone_json(default)
    if isinstance(default, list) and isinstance(parsed, list):
        return parsed
    if isinstance(default, dict) and isinstance(parsed, dict):
        return parsed
    return _clone_json(default)


def _status_from_last_seen(last_seen: Optional[int]) -> str:
    if not last_seen:
        return "Offline"
    try:
        if (time.time() - float(last_seen)) <= 300:
            return "Online"
    except Exception:
        pass
    return "Offline"


def _ts_to_iso(ts: Optional[int]) -> str:
    if not ts:
        return ""
    try:
        from datetime import datetime, timezone

        return datetime.fromtimestamp(int(ts), timezone.utc).isoformat()
    except Exception:
        return ""


def _coerce_int(value: Any) -> Optional[int]:
    try:
        if value is None or (isinstance(value, str) and not value.strip()):
            return None
        return int(float(value))
    except (TypeError, ValueError):
        return None


def _coerce_float(value: Any) -> Optional[float]:
    try:
        if value is None or (isinstance(value, str) and not value.strip()):
            return None
        return float(value)
    except (TypeError, ValueError):
        return None


def _coerce_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    text = str(value or "").strip().lower()
    return text in {"1", "true", "yes", "on"}


def _normalize_string(value: Any) -> str:
    try:
        return str(value or "").strip()
    except Exception:
        return ""


def _storage_free_percent(storage_entries: Sequence[Any]) -> Optional[float]:
    total = 0.0
    free = 0.0
    for entry in storage_entries or []:
        if not isinstance(entry, dict):
            continue
        total_val = _coerce_float(entry.get("total"))
        free_val = _coerce_float(entry.get("free"))
        if total_val and total_val > 0:
            total += total_val
            free += free_val or 0.0
    if total <= 0:
        return None
    return (free / total) * 100.0


def _total_ram_gb(memory_entries: Sequence[Any], summary: Dict[str, Any]) -> Optional[float]:
    total_bytes = 0.0
    for entry in memory_entries or []:
        if not isinstance(entry, dict):
            continue
        total_bytes += _coerce_float(entry.get("capacity")) or 0.0
    if total_bytes <= 0:
        summary_total = _coerce_float(summary.get("total_ram"))
        if summary_total and summary_total > 0:
            total_bytes = summary_total
    if total_bytes <= 0:
        return None
    return total_bytes / (1024.0 ** 3)


def _cpu_model(cpu: Dict[str, Any], summary: Dict[str, Any]) -> str:
    return _normalize_string(cpu.get("name") if isinstance(cpu, dict) else "") or _normalize_string(
        summary.get("processor")
    )


def _last_seen_age_minutes(last_seen: Any) -> Optional[float]:
    value = _coerce_float(last_seen)
    if not value or value <= 0:
        return None
    age_seconds = max(0.0, time.time() - value)
    return age_seconds / 60.0


def _regex_search(pattern: str, value: str) -> bool:
    try:
        return bool(_regex_engine.search(pattern, value))
    except Exception:
        return False


def _version_compare(lhs: str, rhs: str) -> Optional[int]:
    lhs_text = _normalize_string(lhs)
    rhs_text = _normalize_string(rhs)
    if not lhs_text or not rhs_text or Version is None:
        return None
    try:
        lhs_version = Version(lhs_text)
        rhs_version = Version(rhs_text)
    except InvalidVersion:
        return None
    if lhs_version < rhs_version:
        return -1
    if lhs_version > rhs_version:
        return 1
    return 0


def _text_match(operator: str, field_value: str, value: str, *, use_regex: bool = False) -> bool:
    haystack = _normalize_string(field_value)
    needle = _normalize_string(value)
    if use_regex:
        matched = _regex_search(needle, haystack)
        if operator == "does_not_contain":
            return not matched
        return matched
    haystack_lc = haystack.lower()
    needle_lc = needle.lower()
    if operator == "contains":
        return needle_lc in haystack_lc
    if operator == "does_not_contain":
        return needle_lc not in haystack_lc
    if operator == "equals":
        return haystack_lc == needle_lc
    if operator == "begins_with":
        return haystack_lc.startswith(needle_lc)
    if operator == "ends_with":
        return haystack_lc.endswith(needle_lc)
    return False


def _numeric_match(operator: str, field_value: Any, value: Any) -> bool:
    lhs = _coerce_float(field_value)
    rhs = _coerce_float(value)
    if lhs is None or rhs is None:
        return False
    if operator == "equals":
        return lhs == rhs
    if operator == "greater_than":
        return lhs > rhs
    if operator == "greater_than_or_equal":
        return lhs >= rhs
    if operator == "less_than":
        return lhs < rhs
    if operator == "less_than_or_equal":
        return lhs <= rhs
    return False


class DeviceFilterMatcher:
    """Evaluates typed device filters against the Engine inventory data."""

    def __init__(
        self,
        *,
        db_conn_factory: Optional[Callable[[], sqlite3.Connection]] = None,
        db_path: Optional[str] = None,
    ) -> None:
        if db_conn_factory is not None:
            self._conn_factory: Callable[[], sqlite3.Connection] = db_conn_factory
        elif db_path:

            def _factory() -> sqlite3.Connection:
                return sqlite3.connect(db_path)

            self._conn_factory = _factory
        else:  # pragma: no cover - defensive guard
            raise ValueError("DeviceFilterMatcher requires a db_conn_factory or db_path.")

    def _conn(self) -> sqlite3.Connection:
        conn = self._conn_factory()
        conn.row_factory = sqlite3.Row
        return conn

    # ---------- Device loading ----------
    def fetch_devices(self, *, allowed_site_ids: Optional[Iterable[Any]] = None) -> List[Dict[str, Any]]:
        normalized_site_ids = {
            parsed
            for parsed in (_coerce_int(value) for value in (allowed_site_ids or []))
            if parsed is not None
        } if allowed_site_ids is not None else None
        if normalized_site_ids is not None and not normalized_site_ids:
            return []
        rows = []
        conn = self._conn()
        try:
            cur = conn.cursor()
            sql = _DEVICE_SELECT_SQL
            params: tuple[Any, ...] = ()
            if normalized_site_ids is not None:
                placeholders = ",".join("?" for _ in sorted(normalized_site_ids))
                sql += f" WHERE ds.site_id IN ({placeholders})"
                params = tuple(sorted(normalized_site_ids))
            cur.execute(sql, params)
            rows = cur.fetchall()
        finally:
            conn.close()
        guids = [normalize_guid(row["guid"]) or "" for row in rows]
        guid_values = [guid for guid in guids if guid]
        software_map = self._load_software_by_guid(guid_values)
        metadata_map = self._load_metadata_by_guid(guid_values)
        return [self._row_to_device(row, software_map=software_map, metadata_map=metadata_map) for row in rows]

    def _load_software_by_guid(self, guids: Sequence[str]) -> Dict[str, List[Dict[str, Any]]]:
        lookup: Dict[str, List[Dict[str, Any]]] = {}
        unique = [guid for guid in {normalize_guid(value) or "" for value in guids if value}]
        if not unique:
            return lookup
        rows = []
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in unique)
            cur.execute(
                f"""
                SELECT device_guid, name, version, source, metadata_json
                  FROM device_software_inventory
                 WHERE device_guid IN ({placeholders})
                 ORDER BY LOWER(name) ASC, LOWER(source) ASC
                """,
                tuple(unique),
            )
            rows = cur.fetchall()
        except Exception:
            return {}
        finally:
            conn.close()
        for row in rows:
            guid = normalize_guid(row["device_guid"]) or ""
            if not guid:
                continue
            lookup.setdefault(guid, []).append(
                {
                    "name": _normalize_string(row["name"]),
                    "version": _normalize_string(row["version"]),
                    "source": _normalize_string(row["source"]),
                    "metadata": _safe_json(row["metadata_json"], {}),
                }
            )
        return lookup

    def _load_metadata_by_guid(self, guids: Sequence[str]) -> Dict[str, Dict[str, str]]:
        unique = [guid for guid in {normalize_guid(value) or str(value or "").strip() for value in guids if value}]
        if not unique:
            return {}
        conn = self._conn()
        try:
            return metadata_value_lookup_for_devices(conn, unique)
        except Exception:
            return {}
        finally:
            conn.close()

    def _row_to_device(
        self,
        row: sqlite3.Row,
        *,
        software_map: Optional[Dict[str, List[Dict[str, Any]]]] = None,
        metadata_map: Optional[Dict[str, Dict[str, str]]] = None,
    ) -> Dict[str, Any]:
        device_guid = normalize_guid(row["guid"]) or ""
        last_seen = row["last_seen"] or 0
        created_at = row["created_at"] or 0
        summary = {
            "hostname": row["hostname"] or "",
            "description": row["description"] or "",
            "agent_hash": _normalize_string(row["agent_hash"]),
            "agent_guid": device_guid,
            "agent_id": _normalize_string(row["agent_id"]),
            "device_type": row["device_type"] or "",
            "domain": row["domain"] or "",
            "external_ip": row["external_ip"] or "",
            "internal_ip": row["internal_ip"] or "",
            "last_reboot": row["last_reboot"] or "",
            "last_seen": last_seen or 0,
            "last_user": row["last_user"] or "",
            "operating_system": row["operating_system"] or "",
            "uptime": row["uptime"] or 0,
            "created_at": created_at or 0,
            "connection_type": row["connection_type"] or "",
            "connection_endpoint": row["connection_endpoint"] or "",
        }
        memory = _safe_json(row["memory"], [])
        network = _safe_json(row["network"], [])
        software = _safe_json(row["software"], [])
        storage = _safe_json(row["storage"], [])
        cpu = _safe_json(row["cpu"], {})
        normalized_software = list((software_map or {}).get(device_guid) or [])
        metadata_fields = dict((metadata_map or {}).get(device_guid) or {})
        if not normalized_software:
            # Fallback to the raw stored detail payload when the normalized
            # inventory table is empty or not yet refreshed.
            seen_raw: set[tuple[str, str, str]] = set()
            for item in software:
                if not isinstance(item, dict):
                    continue
                name = _normalize_string(item.get("name"))
                version = _normalize_string(item.get("version"))
                source = _normalize_string(item.get("source")) or "local_installed"
                key = (name.lower(), version.lower(), source.lower())
                if not name or key in seen_raw:
                    continue
                seen_raw.add(key)
                normalized_software.append(
                    {
                        "name": name,
                        "version": version,
                        "source": source,
                        "metadata": {},
                    }
                )

        total_ram_gb = _total_ram_gb(memory, summary)
        storage_free_percent = _storage_free_percent(storage)
        cpu_model = _cpu_model(cpu, summary)
        last_seen_age_minutes = _last_seen_age_minutes(last_seen)

        details = {
            "summary": summary,
            "memory": memory,
            "network": network,
            "software": software,
            "storage": storage,
            "cpu": cpu,
        }
        payload = {
            "hostname": summary["hostname"],
            "description": summary["description"],
            "details": details,
            "summary": summary,
            "created_at": created_at or 0,
            "created_at_iso": _ts_to_iso(created_at),
            "device_guid": device_guid,
            "agent_hash": summary["agent_hash"],
            "agent_guid": summary["agent_guid"],
            "guid": summary["agent_guid"],
            "memory": memory,
            "network": network,
            "software": software,
            "software_records": normalized_software,
            "metadata_fields": metadata_fields,
            "storage": storage,
            "cpu": cpu,
            "device_type": summary["device_type"],
            "domain": summary["domain"],
            "external_ip": summary["external_ip"],
            "internal_ip": summary["internal_ip"],
            "last_reboot": summary["last_reboot"],
            "last_seen": last_seen or 0,
            "last_seen_iso": _ts_to_iso(last_seen),
            "last_seen_age_minutes": last_seen_age_minutes,
            "last_user": summary["last_user"],
            "operating_system": summary["operating_system"],
            "uptime": summary["uptime"],
            "agent_id": summary["agent_id"],
            "agent_version": summary["agent_hash"],
            "connection_type": summary["connection_type"],
            "connection_endpoint": summary["connection_endpoint"],
            "site_id": row["site_id"],
            "site_name": row["site_name"] or "",
            "site_description": row["site_description"] or "",
            "status": _status_from_last_seen(last_seen or 0),
            "total_ram_gb": total_ram_gb,
            "storage_free_percent": storage_free_percent,
            "cpu_model": cpu_model,
        }
        return payload

    # ---------- Filter normalization and validation ----------
    def normalize_filter_record(self, filter_record: Optional[Dict[str, Any]]) -> Dict[str, Any]:
        record = dict(filter_record or {})
        site_mode = _normalize_string(record.get("site_mode") or SITE_MODE_GLOBAL).lower()
        if site_mode not in VALID_SITE_MODES:
            site_mode = SITE_MODE_GLOBAL

        site_ids = self._normalize_site_ids(
            record.get("site_ids")
            or record.get("sites")
            or record.get("site_scope_values")
            or record.get("site_scope_value")
            or record.get("site_id")
        )
        basic_payload = record.get("basic_criteria")
        if not isinstance(basic_payload, dict):
            basic_payload = record.get("basic_criteria_json") if isinstance(record.get("basic_criteria_json"), dict) else {}
        if not isinstance(basic_payload, dict):
            basic_payload = {"criteria": []}
        normalized_basic_payload = self._normalize_basic_payload(basic_payload)

        advanced_payload = record.get("criteria") if isinstance(record.get("criteria"), dict) else record.get("advanced_criteria")
        if not isinstance(advanced_payload, dict):
            advanced_payload = (
                record.get("advanced_criteria_json") if isinstance(record.get("advanced_criteria_json"), dict) else {}
            )
        if not isinstance(advanced_payload, dict):
            groups = record.get("groups")
            if isinstance(groups, list):
                advanced_payload = {"groups": groups}
            else:
                advanced_payload = {"groups": []}
        normalized_advanced_payload = self._normalize_advanced_payload(advanced_payload)
        merged_criteria_payload = self._merge_criteria_payloads(normalized_basic_payload, normalized_advanced_payload)

        return {
            "id": record.get("id"),
            "name": _normalize_string(record.get("name")),
            "description": _normalize_string(record.get("description")),
            "archived": _coerce_bool(record.get("archived")),
            "criteria_mode": CRITERIA_MODE_ADVANCED,
            "site_mode": site_mode,
            "site_ids": site_ids,
            "basic_criteria": normalized_basic_payload,
            "advanced_criteria": merged_criteria_payload,
            "criteria": merged_criteria_payload,
            "last_edited_by": _normalize_string(record.get("last_edited_by")),
            "created_at": _coerce_int(record.get("created_at")) or 0,
            "updated_at": _coerce_int(record.get("updated_at")) or 0,
        }

    def _normalize_site_ids(self, raw: Any) -> List[int]:
        candidates: List[Any]
        if isinstance(raw, list):
            candidates = list(raw)
        elif raw is None:
            candidates = []
        else:
            candidates = [raw]
        results: List[int] = []
        seen: set[int] = set()
        for entry in candidates:
            value = entry
            if isinstance(entry, dict):
                value = entry.get("site_id") or entry.get("id") or entry.get("value")
            parsed = _coerce_int(value)
            if parsed is None or parsed in seen:
                continue
            seen.add(parsed)
            results.append(parsed)
        return results

    def _normalize_basic_payload(self, payload: Dict[str, Any]) -> Dict[str, Any]:
        criteria = payload.get("criteria")
        if not isinstance(criteria, list):
            criteria = []
        return {
            "criteria": [self._normalize_criterion(item) for item in criteria if isinstance(item, dict)],
        }

    def _normalize_advanced_payload(self, payload: Dict[str, Any]) -> Dict[str, Any]:
        groups = payload.get("groups")
        if not isinstance(groups, list):
            groups = []
        normalized_groups: List[Dict[str, Any]] = []
        for index, group in enumerate(groups):
            if not isinstance(group, dict):
                continue
            join_with = _normalize_string(group.get("join_with") or group.get("joinWith") or ("OR" if index else "")).upper()
            if index == 0:
                join_with = ""
            elif join_with not in {"AND", "OR"}:
                join_with = "OR"
            conditions = group.get("conditions")
            if not isinstance(conditions, list):
                conditions = []
            normalized_groups.append(
                {
                    "join_with": join_with or None,
                    "conditions": [self._normalize_advanced_condition(item, i) for i, item in enumerate(conditions) if isinstance(item, dict)],
                }
            )
        return {"groups": normalized_groups}

    def _merge_criteria_payloads(self, basic_payload: Dict[str, Any], advanced_payload: Dict[str, Any]) -> Dict[str, Any]:
        groups = list(advanced_payload.get("groups") or [])
        if groups:
            return {"groups": groups}
        basic_criteria = list(basic_payload.get("criteria") or [])
        if not basic_criteria:
            return {"groups": []}
        return {
            "groups": [
                {
                    "join_with": None,
                    "conditions": [
                        {
                            **criterion,
                            "join_with": None if index == 0 else "AND",
                        }
                        for index, criterion in enumerate(basic_criteria)
                    ],
                }
            ]
        }

    def _normalize_criterion(self, item: Dict[str, Any]) -> Dict[str, Any]:
        normalized = {
            "field": _normalize_string(item.get("field")),
            "operator": _normalize_string(item.get("operator") or "contains").lower(),
            "value": item.get("value", ""),
            "use_regex": _coerce_bool(item.get("use_regex") or item.get("useRegex")),
        }
        if normalized["field"] == "installed_software":
            normalized["software_source"] = _normalize_string(
                item.get("software_source") or item.get("softwareSource") or item.get("source")
            ).lower()
            normalized["version_operator"] = _normalize_string(
                item.get("version_operator") or item.get("versionOperator")
            ).lower()
            normalized["version_value"] = _normalize_string(item.get("version_value") or item.get("versionValue"))
        if normalized["field"] == "metadata_field":
            normalized["metadata_field_number"] = normalize_field_number(
                item.get("metadata_field_number")
                or item.get("metadataFieldNumber")
                or item.get("field_number")
                or item.get("fieldNumber")
            )
        return normalized

    def _normalize_advanced_condition(self, item: Dict[str, Any], index: int) -> Dict[str, Any]:
        normalized = self._normalize_criterion(item)
        join_with = _normalize_string(item.get("join_with") or item.get("joinWith") or ("AND" if index else "")).upper()
        if index == 0:
            join_with = ""
        elif join_with not in {"AND", "OR"}:
            join_with = "AND"
        normalized["join_with"] = join_with or None
        return normalized

    def validate_filter_record(self, filter_record: Optional[Dict[str, Any]]) -> List[str]:
        record = self.normalize_filter_record(filter_record)
        errors: List[str] = []
        if record["site_mode"] in {SITE_MODE_SPECIFIC, SITE_MODE_EXCLUSIONS} and not record["site_ids"]:
            errors.append("Select at least one site for the chosen site mode.")
        errors.extend(self._validate_advanced_payload(record["advanced_criteria"]))
        return errors

    def _validate_basic_payload(self, payload: Dict[str, Any]) -> List[str]:
        errors: List[str] = []
        for index, criterion in enumerate(payload.get("criteria") or []):
            errors.extend(self._validate_criterion(criterion, path=f"Basic criterion {index + 1}"))
        return errors

    def _validate_advanced_payload(self, payload: Dict[str, Any]) -> List[str]:
        errors: List[str] = []
        for group_index, group in enumerate(payload.get("groups") or []):
            join_with = _normalize_string(group.get("join_with")).upper()
            if group_index > 0 and join_with not in {"AND", "OR"}:
                errors.append(f"Advanced group {group_index + 1} must specify AND or OR.")
            for cond_index, condition in enumerate(group.get("conditions") or []):
                if cond_index > 0:
                    cond_join = _normalize_string(condition.get("join_with")).upper()
                    if cond_join not in {"AND", "OR"}:
                        errors.append(
                            f"Advanced group {group_index + 1} condition {cond_index + 1} must specify AND or OR."
                        )
                errors.extend(
                    self._validate_criterion(
                        condition,
                        path=f"Advanced group {group_index + 1} condition {cond_index + 1}",
                    )
                )
        return errors

    def _validate_criterion(self, criterion: Dict[str, Any], *, path: str) -> List[str]:
        errors: List[str] = []
        field = FILTER_FIELD_BY_ID.get(_normalize_string(criterion.get("field")))
        if not field:
            return [f"{path}: field is invalid."]
        operator = _normalize_string(criterion.get("operator")).lower() or "contains"
        if operator not in set(field.get("operators") or []):
            errors.append(f"{path}: operator is invalid for {field['label']}.")
        use_regex = _coerce_bool(criterion.get("use_regex"))
        if use_regex and not field.get("supports_regex"):
            errors.append(f"{path}: regex is not supported for {field['label']}.")
        value = criterion.get("value")
        if field["kind"] in {"text", "enum", "software"}:
            if not _normalize_string(value):
                errors.append(f"{path}: value is required.")
        elif field["kind"] == "number":
            if _coerce_float(value) is None:
                errors.append(f"{path}: numeric value is required.")
        elif field["kind"] == "metadata_field":
            field_number = normalize_field_number(
                criterion.get("metadata_field_number")
                or criterion.get("metadataFieldNumber")
                or criterion.get("field_number")
                or criterion.get("fieldNumber")
            )
            if field_number is None:
                errors.append(f"{path}: metadata field is required.")
            if not _normalize_string(value) and operator != "equals":
                errors.append(f"{path}: value is required.")
        if use_regex:
            try:
                _regex_engine.compile(_normalize_string(value))
            except Exception as exc:
                errors.append(f"{path}: regex is invalid ({exc}).")
        if field["kind"] == "software":
            source = _normalize_string(criterion.get("software_source")).lower()
            if source and source not in SOFTWARE_SOURCES:
                errors.append(f"{path}: software source is invalid.")
            version_operator = _normalize_string(criterion.get("version_operator")).lower()
            version_value = _normalize_string(criterion.get("version_value"))
            if version_value and not version_operator:
                version_operator = "matches"
            if version_operator and version_operator not in SOFTWARE_VERSION_OPERATORS:
                errors.append(f"{path}: version operator is invalid.")
            if version_operator and not version_value:
                errors.append(f"{path}: version value is required when a version operator is set.")
            if version_operator in {"older_than", "newer_than"} and Version is None:
                errors.append(f"{path}: version ordering is unavailable because packaging is not installed.")
        return errors

    # ---------- Filter evaluation ----------
    def count_filter_devices(
        self,
        filter_record: Dict[str, Any],
        devices: Optional[Sequence[Dict[str, Any]]] = None,
    ) -> int:
        return len(self.match_filter_devices(filter_record, devices=devices))

    def match_filter_devices(
        self,
        filter_record: Dict[str, Any],
        devices: Optional[Sequence[Dict[str, Any]]] = None,
    ) -> List[Dict[str, Any]]:
        dataset = list(devices) if devices is not None else self.fetch_devices()
        if not dataset:
            return []
        record = self.normalize_filter_record(filter_record)
        if self.validate_filter_record(record):
            return []
        matches: List[Dict[str, Any]] = []
        seen_hosts: set[str] = set()
        for device in dataset:
            hostname = _normalize_string(device.get("hostname"))
            if not hostname:
                continue
            if not self._device_matches_site_mode(record, device):
                continue
            if self._device_matches_active_criteria(record, device):
                key = hostname.lower()
                if key in seen_hosts:
                    continue
                seen_hosts.add(key)
                matches.append(device)
        return matches

    def _device_matches_site_mode(self, filter_record: Dict[str, Any], device: Dict[str, Any]) -> bool:
        site_mode = filter_record.get("site_mode") or SITE_MODE_GLOBAL
        site_ids = {int(value) for value in (filter_record.get("site_ids") or []) if _coerce_int(value) is not None}
        device_site_id = _coerce_int(device.get("site_id"))
        if site_mode == SITE_MODE_GLOBAL:
            return True
        if site_mode == SITE_MODE_SPECIFIC:
            return device_site_id in site_ids if device_site_id is not None else False
        if site_mode == SITE_MODE_EXCLUSIONS:
            if device_site_id is None:
                return True
            return device_site_id not in site_ids
        return True

    def _device_matches_active_criteria(self, filter_record: Dict[str, Any], device: Dict[str, Any]) -> bool:
        return self._evaluate_advanced(device, filter_record.get("advanced_criteria") or {})

    def _evaluate_basic(self, device: Dict[str, Any], payload: Dict[str, Any]) -> bool:
        criteria = payload.get("criteria") or []
        for criterion in criteria:
            if not self._evaluate_criterion(device, criterion):
                return False
        return True

    def _evaluate_advanced(self, device: Dict[str, Any], payload: Dict[str, Any]) -> bool:
        groups = payload.get("groups") or []
        if not groups:
            return True
        result: Optional[bool] = None
        for group in groups:
            group_result = self._evaluate_advanced_group(device, group)
            if result is None:
                result = group_result
                continue
            join_with = _normalize_string(group.get("join_with")).upper() or "OR"
            if join_with == "AND":
                result = bool(result and group_result)
            else:
                result = bool(result or group_result)
        return bool(result)

    def _evaluate_advanced_group(self, device: Dict[str, Any], group: Dict[str, Any]) -> bool:
        conditions = group.get("conditions") or []
        if not conditions:
            return True
        result: Optional[bool] = None
        for condition in conditions:
            condition_result = self._evaluate_criterion(device, condition)
            if result is None:
                result = condition_result
                continue
            join_with = _normalize_string(condition.get("join_with")).upper() or "AND"
            if join_with == "OR":
                result = bool(result or condition_result)
            else:
                result = bool(result and condition_result)
        return bool(result)

    def _evaluate_criterion(self, device: Dict[str, Any], criterion: Dict[str, Any]) -> bool:
        field_id = _normalize_string(criterion.get("field"))
        spec = FILTER_FIELD_BY_ID.get(field_id)
        if not spec:
            return False
        if spec["kind"] == "software":
            return self._evaluate_software(device, criterion)
        if spec["kind"] == "metadata_field":
            return self._evaluate_metadata_field(device, criterion)
        operator = _normalize_string(criterion.get("operator")).lower() or "contains"
        value = criterion.get("value")
        use_regex = _coerce_bool(criterion.get("use_regex"))
        field_value = self._get_field_value(device, field_id)
        if spec["kind"] in {"text", "enum"}:
            return _text_match(operator, _normalize_string(field_value), _normalize_string(value), use_regex=use_regex)
        if spec["kind"] == "number":
            return _numeric_match(operator, field_value, value)
        return False

    def _evaluate_metadata_field(self, device: Dict[str, Any], criterion: Dict[str, Any]) -> bool:
        field_number = normalize_field_number(
            criterion.get("metadata_field_number")
            or criterion.get("metadataFieldNumber")
            or criterion.get("field_number")
            or criterion.get("fieldNumber")
        )
        if field_number is None:
            return False
        operator = _normalize_string(criterion.get("operator")).lower() or "contains"
        value = criterion.get("value", "")
        use_regex = _coerce_bool(criterion.get("use_regex"))
        metadata_fields = device.get("metadata_fields") if isinstance(device.get("metadata_fields"), dict) else {}
        field_value = metadata_fields.get(metadata_field_key(field_number), "")
        return _text_match(operator, _normalize_string(field_value), _normalize_string(value), use_regex=use_regex)

    def _evaluate_software(self, device: Dict[str, Any], criterion: Dict[str, Any]) -> bool:
        operator = _normalize_string(criterion.get("operator")).lower() or "contains"
        value = _normalize_string(criterion.get("value"))
        if not value:
            return False
        use_regex = _coerce_bool(criterion.get("use_regex"))
        source = _normalize_string(criterion.get("software_source")).lower()
        version_operator = _normalize_string(criterion.get("version_operator")).lower()
        version_value = _normalize_string(criterion.get("version_value"))
        if version_value and not version_operator:
            version_operator = "matches"
        positive_operator = "contains" if operator == "does_not_contain" else operator
        matched = False
        for record in device.get("software_records") or []:
            if not isinstance(record, dict):
                continue
            record_source = _normalize_string(record.get("source")).lower()
            if source and record_source != source:
                continue
            name = _normalize_string(record.get("name"))
            if not _text_match(positive_operator, name, value, use_regex=use_regex):
                continue
            if version_operator:
                if not self._match_software_version(
                    _normalize_string(record.get("version")),
                    version_operator,
                    version_value,
                ):
                    continue
            matched = True
            break
        if operator == "does_not_contain":
            return not matched
        return matched

    def _match_software_version(self, device_version: str, operator: str, value: str) -> bool:
        if operator == "matches":
            compare = _version_compare(device_version, value)
            if compare is not None:
                return compare == 0
            return _normalize_string(device_version).lower() == _normalize_string(value).lower()
        compare = _version_compare(device_version, value)
        if compare is None:
            return False
        if operator == "older_than":
            return compare < 0
        if operator == "newer_than":
            return compare > 0
        return False

    def _get_field_value(self, device: Dict[str, Any], field_id: str) -> Any:
        summary = device.get("summary") if isinstance(device.get("summary"), dict) else {}
        if field_id == "hostname":
            return device.get("hostname") or summary.get("hostname")
        if field_id == "description":
            return device.get("description") or summary.get("description")
        if field_id == "site_name":
            return device.get("site_name") or summary.get("site_name") or summary.get("site")
        if field_id == "operating_system":
            return device.get("operating_system") or summary.get("operating_system")
        if field_id == "device_type":
            return device.get("device_type") or summary.get("device_type")
        if field_id == "status":
            return device.get("status") or summary.get("status")
        if field_id == "last_seen_age_minutes":
            return device.get("last_seen_age_minutes")
        if field_id == "last_user":
            return device.get("last_user") or summary.get("last_user")
        if field_id == "internal_ip":
            return device.get("internal_ip") or summary.get("internal_ip")
        if field_id == "external_ip":
            return device.get("external_ip") or summary.get("external_ip")
        if field_id == "domain":
            return device.get("domain") or summary.get("domain")
        if field_id == "total_ram_gb":
            return device.get("total_ram_gb")
        if field_id == "storage_free_percent":
            return device.get("storage_free_percent")
        if field_id == "cpu_model":
            return device.get("cpu_model")
        if field_id == "agent_version":
            return device.get("agent_version") or summary.get("agent_hash")
        return device.get(field_id) or summary.get(field_id)

    # ---------- Filter lookup ----------
    def load_filters(
        self,
        filter_ids: Optional[Iterable[Any]] = None,
        *,
        include_archived: bool = True,
    ) -> Dict[int, Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            params: Tuple[Any, ...]
            clauses: List[str] = []
            params_list: List[Any] = []
            if filter_ids:
                ids = [int(fid) for fid in filter_ids if _coerce_int(fid) is not None]
                if not ids:
                    return {}
                placeholders = ",".join("?" for _ in ids)
                clauses.append(f"id IN ({placeholders})")
                params_list.extend(ids)
            if not include_archived:
                clauses.append("COALESCE(archived, 0) = 0")
            where_sql = ""
            if clauses:
                where_sql = "WHERE " + " AND ".join(clauses)
            cur.execute(
                f"""
                    SELECT id, name, description, archived, criteria_mode, site_mode,
                           basic_criteria_json, advanced_criteria_json, last_edited_by,
                           created_at, updated_at
                      FROM device_filters
                    {where_sql}
                """,
                tuple(params_list),
            )
            rows = cur.fetchall()
            records: Dict[int, Dict[str, Any]] = {}
            for row in rows:
                record = {
                    "id": row["id"],
                    "name": row["name"],
                    "description": row["description"] or "",
                    "archived": bool(row["archived"] or 0),
                    "criteria_mode": row["criteria_mode"] or CRITERIA_MODE_ADVANCED,
                    "site_mode": row["site_mode"] or SITE_MODE_GLOBAL,
                    "basic_criteria": _safe_json(row["basic_criteria_json"], {"criteria": []}),
                    "advanced_criteria": _safe_json(row["advanced_criteria_json"], {"groups": []}),
                    "last_edited_by": row["last_edited_by"] or "",
                    "created_at": row["created_at"] or 0,
                    "updated_at": row["updated_at"] or 0,
                    "site_ids": [],
                    "site_names": [],
                    "sites": [],
                }
                records[int(row["id"])] = self.normalize_filter_record(record)
            if not records:
                return {}
            site_lookup = self._load_filter_sites(list(records.keys()))
            for filter_id, record in records.items():
                sites = site_lookup.get(int(filter_id)) or []
                record["site_ids"] = [site["id"] for site in sites]
                record["site_names"] = [site["name"] for site in sites]
                record["sites"] = sites
            return records
        finally:
            conn.close()

    def _load_filter_sites(self, filter_ids: Sequence[int]) -> Dict[int, List[Dict[str, Any]]]:
        if not filter_ids:
            return {}
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in filter_ids)
            cur.execute(
                f"""
                    SELECT dfs.filter_id, s.id, s.name
                      FROM device_filter_sites AS dfs
                      JOIN sites AS s ON s.id = dfs.site_id
                     WHERE dfs.filter_id IN ({placeholders})
                     ORDER BY LOWER(s.name) ASC
                """,
                tuple(int(filter_id) for filter_id in filter_ids),
            )
            mapping: Dict[int, List[Dict[str, Any]]] = {}
            for row in cur.fetchall():
                mapping.setdefault(int(row[0]), []).append({"id": int(row[1]), "name": row[2] or ""})
            return mapping
        finally:
            conn.close()

    # ---------- Target resolution ----------
    def resolve_target_entries(
        self,
        raw_targets: Sequence[Any],
        *,
        devices: Optional[Sequence[Dict[str, Any]]] = None,
        filters_by_id: Optional[Dict[int, Dict[str, Any]]] = None,
    ) -> Tuple[List[str], Dict[str, Any]]:
        dataset = list(devices) if devices is not None else self.fetch_devices()
        device_by_guid: Dict[str, Dict[str, Any]] = {}
        devices_by_host: Dict[str, List[Dict[str, Any]]] = {}
        devices_by_site_host: Dict[Tuple[int, str], List[Dict[str, Any]]] = {}
        for device in dataset:
            device_guid = _normalize_string(device.get("device_guid") or device.get("guid"))
            if device_guid:
                device_by_guid[device_guid.lower()] = device
            hostname = _normalize_string(device.get("hostname"))
            if hostname:
                lowered = hostname.lower()
                devices_by_host.setdefault(lowered, []).append(device)
                site_id = _coerce_int(device.get("site_id"))
                if site_id is not None:
                    devices_by_site_host.setdefault((site_id, lowered), []).append(device)

        target_hosts: List[str] = []
        target_records: List[Dict[str, Any]] = []
        filter_targets: List[Dict[str, Any]] = []
        explicit_targets: List[Dict[str, Any]] = []
        for entry in raw_targets or []:
            parsed = self._normalize_target_entry(entry)
            if parsed["kind"] == "device":
                explicit_targets.append(parsed)
            elif parsed["kind"] == "filter" and parsed.get("filter_id") is not None:
                filter_targets.append(parsed)

        host_details: Dict[str, Dict[str, Any]] = {}
        target_identity_map: Dict[str, Dict[str, Any]] = {}

        def _identity_for_target(record: Dict[str, Any]) -> str:
            guid_value = _normalize_string(record.get("device_guid") or record.get("guid")).lower()
            if guid_value:
                return f"guid:{guid_value}"
            hostname_value = _normalize_string(record.get("hostname")).lower()
            site_id_value = _coerce_int(record.get("site_id"))
            if hostname_value and site_id_value is not None:
                return f"site:{site_id_value}:{hostname_value}"
            if hostname_value:
                return f"host:{hostname_value}"
            return ""

        def _build_target_record(
            device: Optional[Dict[str, Any]],
            *,
            hostname_override: str = "",
            site_id_override: Optional[int] = None,
            site_name_override: str = "",
        ) -> Dict[str, Any]:
            hostname_value = _normalize_string(hostname_override) or _normalize_string((device or {}).get("hostname"))
            site_id_value = site_id_override if site_id_override is not None else _coerce_int((device or {}).get("site_id"))
            site_name_value = _normalize_string(site_name_override) or _normalize_string((device or {}).get("site_name"))
            return {
                "hostname": hostname_value,
                "device_guid": _normalize_string((device or {}).get("device_guid") or (device or {}).get("guid")),
                "site_id": site_id_value,
                "site_name": site_name_value,
                "agent_id": _normalize_string((device or {}).get("agent_id")),
                "connection_type": _normalize_string((device or {}).get("connection_type")),
                "connection_endpoint": _normalize_string((device or {}).get("connection_endpoint")),
                "operating_system": _normalize_string((device or {}).get("operating_system")),
                "resolved_from_filter_ids": [],
            }

        def _append_target_record(record: Dict[str, Any]) -> Dict[str, Any]:
            identity = _identity_for_target(record)
            if identity and identity in target_identity_map:
                existing = target_identity_map[identity]
                for field in (
                    "hostname",
                    "device_guid",
                    "site_name",
                    "agent_id",
                    "connection_type",
                    "connection_endpoint",
                    "operating_system",
                ):
                    if not existing.get(field) and record.get(field):
                        existing[field] = record.get(field)
                if existing.get("site_id") is None and record.get("site_id") is not None:
                    existing["site_id"] = record.get("site_id")
                return existing

            if identity:
                target_identity_map[identity] = record
            target_records.append(record)
            if record.get("hostname"):
                target_hosts.append(str(record["hostname"]))
                details_key = str(record["hostname"]).lower()
                if details_key in host_details:
                    site_fragment = record.get("site_id")
                    guid_fragment = record.get("device_guid") or ""
                    details_key = f"{details_key}:{site_fragment if site_fragment is not None else guid_fragment}"
                host_details[details_key] = record
            return record

        for explicit_target in explicit_targets:
            hostname = _normalize_string(explicit_target.get("hostname"))
            site_id = _coerce_int(explicit_target.get("site_id"))
            site_name = _normalize_string(explicit_target.get("site_name"))
            device_guid = _normalize_string(explicit_target.get("device_guid"))
            matches: List[Dict[str, Any]] = []
            if device_guid:
                match = device_by_guid.get(device_guid.lower())
                if match:
                    matches = [match]
            elif hostname and site_id is not None:
                matches = list(devices_by_site_host.get((site_id, hostname.lower()), []))
            elif hostname:
                matches = list(devices_by_host.get(hostname.lower(), []))

            if not matches:
                _append_target_record(
                    _build_target_record(
                        None,
                        hostname_override=hostname,
                        site_id_override=site_id,
                        site_name_override=site_name,
                    )
                )
                continue

            for match in matches:
                _append_target_record(
                    _build_target_record(
                        match,
                        hostname_override=hostname,
                        site_id_override=site_id if site_id is not None else _coerce_int(match.get("site_id")),
                        site_name_override=site_name or _normalize_string(match.get("site_name")),
                    )
                )

        filter_matches: Dict[int, List[str]] = {}
        if filter_targets:
            filter_ids = [int(target["filter_id"]) for target in filter_targets if target.get("filter_id") is not None]
            filters = filters_by_id or self.load_filters(filter_ids, include_archived=False)
            for filter_target in filter_targets:
                filter_id = int(filter_target["filter_id"])
                record = filters.get(filter_id)
                if not record or record.get("archived"):
                    continue
                allowed_site_ids = {
                    int(value)
                    for value in self._normalize_site_ids(
                        filter_target.get("allowed_site_ids") or filter_target.get("scope_site_ids")
                    )
                }
                if allowed_site_ids:
                    scoped_dataset = [
                        device
                        for device in dataset
                        if _coerce_int(device.get("site_id")) in allowed_site_ids
                    ]
                else:
                    scoped_dataset = dataset
                matches = self.match_filter_devices(record, devices=scoped_dataset)
                resolved_hosts: List[str] = []
                for device in matches:
                    details = _append_target_record(_build_target_record(device))
                    hostname = _normalize_string(details.get("hostname"))
                    if not hostname:
                        continue
                    if int(filter_id) not in details["resolved_from_filter_ids"]:
                        details["resolved_from_filter_ids"].append(int(filter_id))
                    resolved_hosts.append(hostname)
                filter_matches[int(filter_id)] = resolved_hosts

        metadata = {
            "filters_resolved": filter_matches,
            "resolved_host_details": host_details,
            "resolved_targets": target_records,
            "total_hosts": len(target_hosts),
        }
        return target_hosts, metadata

    def _normalize_target_entry(self, entry: Any) -> Dict[str, Any]:
        if isinstance(entry, str):
            return {"kind": "device", "hostname": entry.strip()}
        if isinstance(entry, (int, float)):
            return {"kind": "device", "hostname": str(entry)}
        if isinstance(entry, dict):
            kind = _normalize_string(entry.get("kind") or entry.get("type")).lower()
            if kind == "filter" or entry.get("filter_id") is not None:
                filter_id = _coerce_int(entry.get("filter_id") or entry.get("id"))
                return {
                    "kind": "filter",
                    "filter_id": filter_id,
                    "name": entry.get("name"),
                    "allowed_site_ids": self._normalize_site_ids(
                        entry.get("allowed_site_ids") or entry.get("scope_site_ids")
                    ),
                }
            hostname = _normalize_string(entry.get("hostname"))
            if hostname:
                return {
                    "kind": "device",
                    "hostname": hostname,
                    "device_guid": _normalize_string(entry.get("device_guid") or entry.get("guid")),
                    "site_id": _coerce_int(entry.get("site_id")),
                    "site_name": _normalize_string(entry.get("site_name") or entry.get("site")),
                }
        return {"kind": "unknown"}


__all__ = [
    "CRITERIA_MODE_ADVANCED",
    "CRITERIA_MODE_BASIC",
    "DeviceFilterMatcher",
    "FILTER_FIELD_BY_ID",
    "FILTER_METADATA_PAYLOAD",
    "SITE_MODE_EXCLUSIONS",
    "SITE_MODE_GLOBAL",
    "SITE_MODE_SPECIFIC",
    "filter_metadata",
    "normalize_software_name",
]
