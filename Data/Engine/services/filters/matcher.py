# ======================================================
# Data\Engine\services\filters\matcher.py
# Description: Shared helpers for evaluating device filters and resolving
#              target lists for scheduled jobs and filter list summaries.
#
# API Endpoints (if applicable): None
# ======================================================

"""Utilities that evaluate device filters against the Engine inventory."""

from __future__ import annotations

import json
import sqlite3
import time
from typing import Any, Callable, Dict, Iterable, List, Optional, Sequence, Tuple

from Data.Engine.auth.guid_utils import normalize_guid


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


def _safe_json(raw: Optional[str], default: Any) -> Any:
    if raw is None:
        return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    try:
        parsed = json.loads(raw)
    except Exception:
        return default
    if isinstance(default, list) and isinstance(parsed, list):
        return parsed
    if isinstance(default, dict) and isinstance(parsed, dict):
        return parsed
    return default


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


class DeviceFilterMatcher:
    """Evaluates device filters against the Engine inventory data."""

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
    def fetch_devices(self) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(_DEVICE_SELECT_SQL)
            return [self._row_to_device(row) for row in cur.fetchall()]
        finally:
            conn.close()

    def _row_to_device(self, row: sqlite3.Row) -> Dict[str, Any]:
        last_seen = row["last_seen"] or 0
        created_at = row["created_at"] or 0
        summary = {
            "hostname": row["hostname"] or "",
            "description": row["description"] or "",
            "agent_hash": (row["agent_hash"] or "").strip(),
            "agent_guid": normalize_guid(row["guid"]) or "",
            "agent_id": (row["agent_id"] or "").strip(),
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
        details = {
            "summary": summary,
            "memory": _safe_json(row["memory"], []),
            "network": _safe_json(row["network"], []),
            "software": _safe_json(row["software"], []),
            "storage": _safe_json(row["storage"], []),
            "cpu": _safe_json(row["cpu"], {}),
        }
        payload = {
            "hostname": summary["hostname"],
            "description": summary["description"],
            "details": details,
            "summary": summary,
            "created_at": created_at or 0,
            "created_at_iso": _ts_to_iso(created_at),
            "agent_hash": summary["agent_hash"],
            "agent_guid": summary["agent_guid"],
            "guid": summary["agent_guid"],
            "memory": details["memory"],
            "network": details["network"],
            "software": details["software"],
            "storage": details["storage"],
            "cpu": details["cpu"],
            "device_type": summary["device_type"],
            "domain": summary["domain"],
            "external_ip": summary["external_ip"],
            "internal_ip": summary["internal_ip"],
            "last_reboot": summary["last_reboot"],
            "last_seen": last_seen or 0,
            "last_seen_iso": _ts_to_iso(last_seen),
            "last_user": summary["last_user"],
            "operating_system": summary["operating_system"],
            "uptime": summary["uptime"],
            "agent_id": summary["agent_id"],
            "connection_type": summary["connection_type"],
            "connection_endpoint": summary["connection_endpoint"],
            "site_id": row["site_id"],
            "site_name": row["site_name"] or "",
            "site_description": row["site_description"] or "",
            "status": _status_from_last_seen(last_seen or 0),
            "agentVersion": summary["agent_hash"] or "",
        }
        return payload

    # ---------- Filter evaluation ----------
    def count_filter_devices(
        self,
        filter_record: Dict[str, Any],
        devices: Optional[Sequence[Dict[str, Any]]] = None,
    ) -> int:
        matches = self.match_filter_devices(filter_record, devices=devices)
        return len(matches)

    def match_filter_devices(
        self,
        filter_record: Dict[str, Any],
        devices: Optional[Sequence[Dict[str, Any]]] = None,
    ) -> List[Dict[str, Any]]:
        dataset = list(devices) if devices is not None else self.fetch_devices()
        if not dataset:
            return []
        normalized_groups = self._normalize_groups(filter_record.get("groups"))
        site_limit = self._resolve_site_limit(filter_record)
        matches: List[Dict[str, Any]] = []
        seen_hosts: set[str] = set()
        for device in dataset:
            hostname = (device.get("hostname") or "").strip()
            if not hostname:
                continue
            if site_limit and not self._site_matches(site_limit, device):
                continue
            if self._device_matches_groups(device, normalized_groups):
                key = hostname.lower()
                if key in seen_hosts:
                    continue
                seen_hosts.add(key)
                matches.append(device)
        return matches

    def _resolve_site_limit(self, filter_record: Dict[str, Any]) -> Optional[str]:
        scope = (
            filter_record.get("site_scope")
            or filter_record.get("scope")
            or filter_record.get("type")
            or ""
        )
        normalized_scope = str(scope).strip().lower()
        if normalized_scope != "scoped":
            return None
        site_value = (
            filter_record.get("site")
            or filter_record.get("site_name")
            or filter_record.get("site_scope_value")
        )
        if not site_value:
            return None
        return str(site_value).strip().lower()

    def _site_matches(self, expected: str, device: Dict[str, Any]) -> bool:
        site_candidates = [
            device.get("site_name"),
            device.get("site"),
            device.get("summary", {}).get("site"),
        ]
        for candidate in site_candidates:
            if candidate and str(candidate).strip().lower() == expected:
                return True
        return False

    def _normalize_groups(self, raw_groups: Any) -> List[Dict[str, Any]]:
        if not isinstance(raw_groups, list) or not raw_groups:
            return []
        normalized: List[Dict[str, Any]] = []
        for idx, group in enumerate(raw_groups):
            conditions = group.get("conditions") if isinstance(group, dict) else None
            normalized_conditions: List[Dict[str, Any]] = []
            if isinstance(conditions, list) and conditions:
                for c_idx, cond in enumerate(conditions):
                    if not isinstance(cond, dict):
                        continue
                    normalized_conditions.append(
                        {
                            "field": (cond.get("field") or "hostname").strip(),
                            "operator": str(cond.get("operator") or "contains").strip().lower(),
                            "value": "" if cond.get("value") is None else cond.get("value"),
                            "join": (cond.get("join_with") or cond.get("joinWith") or ("AND" if c_idx else None)),
                        }
                    )
            if not normalized_conditions:
                # Empty group matches everything by default.
                normalized_conditions = [
                    {
                        "field": "hostname",
                        "operator": "not_empty",
                        "value": "",
                        "join": None,
                    }
                ]
            normalized.append(
                {
                    "join": (group.get("join_with") or group.get("joinWith") or ("OR" if idx else None)),
                    "conditions": normalized_conditions,
                }
            )
        return normalized

    def _device_matches_groups(self, device: Dict[str, Any], groups: List[Dict[str, Any]]) -> bool:
        if not groups:
            return True
        result = self._evaluate_group(device, groups[0])
        for group in groups[1:]:
            join = str(group.get("join") or "OR").upper()
            res = self._evaluate_group(device, group)
            if join == "AND":
                result = result and res
            else:
                result = result or res
        return result

    def _evaluate_group(self, device: Dict[str, Any], group: Dict[str, Any]) -> bool:
        conditions = group.get("conditions") or []
        if not conditions:
            return True
        result = self._evaluate_condition(device, conditions[0])
        for cond in conditions[1:]:
            join = str(cond.get("join") or "AND").upper()
            res = self._evaluate_condition(device, cond)
            if join == "OR":
                result = result or res
            else:
                result = result and res
        return result

    def _evaluate_condition(self, device: Dict[str, Any], condition: Dict[str, Any]) -> bool:
        operator = str(condition.get("operator") or "contains").lower()
        raw_value = condition.get("value")
        value = "" if raw_value is None else str(raw_value)
        field_value_raw = self._get_device_field(device, condition.get("field"))
        field_value = "" if field_value_raw is None else str(field_value_raw)
        lc_field = field_value.lower()
        lc_value = value.lower()

        if operator == "contains":
            return lc_value in lc_field
        if operator == "not_contains":
            return lc_value not in lc_field
        if operator == "empty":
            return lc_field == ""
        if operator == "not_empty":
            return lc_field != ""
        if operator == "begins_with":
            return lc_field.startswith(lc_value)
        if operator == "not_begins_with":
            return not lc_field.startswith(lc_value)
        if operator == "ends_with":
            return lc_field.endswith(lc_value)
        if operator == "not_ends_with":
            return not lc_field.endswith(lc_value)
        if operator == "equals":
            return lc_field == lc_value
        if operator == "not_equals":
            return lc_field != lc_value
        return False

    def _get_device_field(self, device: Dict[str, Any], field: Any) -> Any:
        summary = device.get("summary") if isinstance(device.get("summary"), dict) else {}
        name = str(field or "").strip()
        if name == "status":
            return device.get("status") or summary.get("status")
        if name == "site":
            return (
                device.get("site_name")
                or device.get("site")
                or summary.get("site")
                or ""
            )
        if name == "hostname":
            return device.get("hostname") or summary.get("hostname")
        if name == "description":
            return device.get("description") or summary.get("description")
        if name == "os":
            return device.get("operating_system") or summary.get("operating_system")
        if name == "type":
            return device.get("device_type") or summary.get("device_type")
        if name == "agentVersion":
            return device.get("agentVersion") or summary.get("agent_hash")
        if name == "lastUser":
            return device.get("last_user") or summary.get("last_user")
        if name == "internalIp":
            return device.get("internal_ip") or summary.get("internal_ip")
        if name == "externalIp":
            return device.get("external_ip") or summary.get("external_ip")
        if name == "lastReboot":
            return device.get("last_reboot") or summary.get("last_reboot")
        if name == "lastSeen":
            return device.get("last_seen") or summary.get("last_seen")
        if name == "domain":
            return device.get("domain") or summary.get("domain")
        if name == "memory":
            return device.get("memory")
        if name == "network":
            return device.get("network")
        if name == "software":
            return device.get("software")
        if name == "storage":
            return device.get("storage")
        if name == "cpu":
            return device.get("cpu")
        if name == "agentId":
            return device.get("agent_id") or summary.get("agent_id")
        if name == "agentGuid":
            return device.get("agent_guid") or summary.get("agent_guid")
        return device.get(name) or summary.get(name)

    # ---------- Filter lookup ----------
    def load_filters(
        self,
        filter_ids: Optional[Iterable[Any]] = None,
    ) -> Dict[int, Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            params: Tuple[Any, ...]
            if filter_ids:
                ids = [int(fid) for fid in filter_ids if str(fid).strip()]
                if not ids:
                    return {}
                placeholders = ",".join("?" for _ in ids)
                sql = f"""
                    SELECT id, name, site_scope, site_name,
                           criteria_json, last_edited_by, last_edited,
                           created_at, updated_at
                      FROM device_filters
                     WHERE id IN ({placeholders})
                """
                params = tuple(ids)
                cur.execute(sql, params)
            else:
                cur.execute(
                    """
                    SELECT id, name, site_scope, site_name,
                           criteria_json, last_edited_by, last_edited,
                           created_at, updated_at
                      FROM device_filters
                    """
                )
            results: Dict[int, Dict[str, Any]] = {}
            for row in cur.fetchall():
                entry = {
                    "id": row["id"],
                    "name": row["name"],
                    "site_scope": row["site_scope"],
                    "site_name": row["site_name"],
                    "criteria_json": row["criteria_json"],
                    "last_edited_by": row["last_edited_by"],
                    "last_edited": row["last_edited"],
                    "created_at": row["created_at"],
                    "updated_at": row["updated_at"],
                }
                try:
                    entry["groups"] = json.loads(entry.get("criteria_json") or "[]")
                except Exception:
                    entry["groups"] = []
                results[int(entry["id"])] = entry
            return results
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
        target_hosts: List[str] = []
        host_set: set[str] = set()
        filter_ids: List[int] = []
        for entry in raw_targets or []:
            parsed = self._normalize_target_entry(entry)
            if parsed["kind"] == "device":
                hostname = parsed.get("hostname")
                if not hostname:
                    continue
                lowered = hostname.lower()
                if lowered in host_set:
                    continue
                host_set.add(lowered)
                target_hosts.append(hostname)
            elif parsed["kind"] == "filter":
                filter_id = parsed.get("filter_id")
                if filter_id is None:
                    continue
                filter_ids.append(int(filter_id))

        filter_matches: Dict[int, List[str]] = {}
        dataset = devices
        if filter_ids:
            if dataset is None:
                dataset = self.fetch_devices()
            filters = filters_by_id or self.load_filters(filter_ids)
            for filter_id in filter_ids:
                record = filters.get(int(filter_id))
                if not record:
                    continue
                matches = self.match_filter_devices(record, devices=dataset)
                hostnames = [
                    (device.get("hostname") or "").strip() for device in matches
                ]
                final_hosts = []
                for hostname in hostnames:
                    if not hostname:
                        continue
                    lowered = hostname.lower()
                    if lowered in host_set:
                        continue
                    host_set.add(lowered)
                    final_hosts.append(hostname)
                    target_hosts.append(hostname)
                filter_matches[int(filter_id)] = final_hosts

        metadata = {
            "filters_resolved": filter_matches,
            "total_hosts": len(target_hosts),
        }
        return target_hosts, metadata

    def _normalize_target_entry(self, entry: Any) -> Dict[str, Any]:
        if isinstance(entry, str):
            return {"kind": "device", "hostname": entry.strip()}
        if isinstance(entry, (int, float)):
            return {"kind": "device", "hostname": str(entry)}
        if isinstance(entry, dict):
            kind = (entry.get("kind") or entry.get("type") or "").strip().lower()
            if kind == "filter" or entry.get("filter_id") is not None:
                filter_id = entry.get("filter_id") or entry.get("id")
                try:
                    filter_id = int(filter_id)
                except Exception:
                    filter_id = None
                return {
                    "kind": "filter",
                    "filter_id": filter_id,
                    "name": entry.get("name"),
                    "site_scope": entry.get("site_scope"),
                    "site": entry.get("site") or entry.get("site_name"),
                }
            hostname = entry.get("hostname")
            if hostname:
                return {"kind": "device", "hostname": str(hostname).strip()}
        return {"kind": "unknown"}


__all__ = ["DeviceFilterMatcher"]
