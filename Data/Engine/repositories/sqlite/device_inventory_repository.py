"""Device inventory operations backed by SQLite."""

from __future__ import annotations

import logging
import sqlite3
import time
import uuid
from contextlib import closing
from typing import Any, Dict, List, Optional, Tuple

from Data.Engine.domain.devices import (
    DEVICE_TABLE,
    DEVICE_TABLE_COLUMNS,
    assemble_device_snapshot,
    clean_device_str,
    coerce_int,
    device_column_sql,
    row_to_device_dict,
    serialize_device_json,
)
from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory

__all__ = ["SQLiteDeviceInventoryRepository"]


class SQLiteDeviceInventoryRepository:
    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.device_inventory")

    def fetch_devices(
        self,
        *,
        connection_type: Optional[str] = None,
        hostname: Optional[str] = None,
        only_agents: bool = False,
    ) -> List[Dict[str, Any]]:
        sql = f"""
            SELECT {device_column_sql('d')}, s.id, s.name, s.description
              FROM {DEVICE_TABLE} d
         LEFT JOIN device_sites ds ON ds.device_hostname = d.hostname
         LEFT JOIN sites s ON s.id = ds.site_id
        """
        clauses: List[str] = []
        params: List[Any] = []
        if connection_type:
            clauses.append("LOWER(d.connection_type) = LOWER(?)")
            params.append(connection_type)
        if hostname:
            clauses.append("LOWER(d.hostname) = LOWER(?)")
            params.append(hostname.lower())
        if only_agents:
            clauses.append("(d.connection_type IS NULL OR TRIM(d.connection_type) = '')")
        if clauses:
            sql += " WHERE " + " AND ".join(clauses)

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, params)
            rows = cur.fetchall()

        now = time.time()
        devices: List[Dict[str, Any]] = []
        for row in rows:
            core = row[: len(DEVICE_TABLE_COLUMNS)]
            site_id, site_name, site_description = row[len(DEVICE_TABLE_COLUMNS) :]
            record = row_to_device_dict(core, DEVICE_TABLE_COLUMNS)
            snapshot = assemble_device_snapshot(record)
            summary = snapshot.get("summary", {})
            last_seen = snapshot.get("last_seen") or 0
            status = "Offline"
            try:
                if last_seen and (now - float(last_seen)) <= 300:
                    status = "Online"
            except Exception:
                pass
            devices.append(
                {
                    **snapshot,
                    "site_id": site_id,
                    "site_name": site_name or "",
                    "site_description": site_description or "",
                    "status": status,
                }
            )
        return devices

    def load_snapshot(self, *, hostname: Optional[str] = None, guid: Optional[str] = None) -> Optional[Dict[str, Any]]:
        if not hostname and not guid:
            return None
        sql = None
        params: Tuple[Any, ...]
        if hostname:
            sql = f"SELECT {device_column_sql()} FROM {DEVICE_TABLE} WHERE hostname = ?"
            params = (hostname,)
        else:
            sql = f"SELECT {device_column_sql()} FROM {DEVICE_TABLE} WHERE LOWER(guid) = LOWER(?)"
            params = (guid,)
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, params)
            row = cur.fetchone()
        if not row:
            return None
        record = row_to_device_dict(row, DEVICE_TABLE_COLUMNS)
        return assemble_device_snapshot(record)

    def upsert_device(
        self,
        hostname: str,
        description: Optional[str],
        merged_details: Dict[str, Any],
        created_at: Optional[int],
        *,
        agent_hash: Optional[str] = None,
        guid: Optional[str] = None,
    ) -> None:
        if not hostname:
            return

        column_values = self._extract_device_columns(merged_details or {})
        normalized_description = description if description is not None else ""
        try:
            normalized_description = str(normalized_description)
        except Exception:
            normalized_description = ""

        normalized_hash = clean_device_str(agent_hash) or None
        normalized_guid = clean_device_str(guid) or None
        created_ts = coerce_int(created_at) or int(time.time())

        sql = f"""
            INSERT INTO {DEVICE_TABLE}(
                hostname,
                description,
                created_at,
                agent_hash,
                guid,
                memory,
                network,
                software,
                storage,
                cpu,
                device_type,
                domain,
                external_ip,
                internal_ip,
                last_reboot,
                last_seen,
                last_user,
                operating_system,
                uptime,
                agent_id,
                ansible_ee_ver,
                connection_type,
                connection_endpoint,
                ssl_key_fingerprint,
                token_version,
                status,
                key_added_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            ON CONFLICT(hostname) DO UPDATE SET
                description=excluded.description,
                created_at=COALESCE({DEVICE_TABLE}.created_at, excluded.created_at),
                agent_hash=COALESCE(NULLIF(excluded.agent_hash, ''), {DEVICE_TABLE}.agent_hash),
                guid=COALESCE(NULLIF(excluded.guid, ''), {DEVICE_TABLE}.guid),
                memory=excluded.memory,
                network=excluded.network,
                software=excluded.software,
                storage=excluded.storage,
                cpu=excluded.cpu,
                device_type=COALESCE(NULLIF(excluded.device_type, ''), {DEVICE_TABLE}.device_type),
                domain=COALESCE(NULLIF(excluded.domain, ''), {DEVICE_TABLE}.domain),
                external_ip=COALESCE(NULLIF(excluded.external_ip, ''), {DEVICE_TABLE}.external_ip),
                internal_ip=COALESCE(NULLIF(excluded.internal_ip, ''), {DEVICE_TABLE}.internal_ip),
                last_reboot=COALESCE(NULLIF(excluded.last_reboot, ''), {DEVICE_TABLE}.last_reboot),
                last_seen=COALESCE(NULLIF(excluded.last_seen, 0), {DEVICE_TABLE}.last_seen),
                last_user=COALESCE(NULLIF(excluded.last_user, ''), {DEVICE_TABLE}.last_user),
                operating_system=COALESCE(NULLIF(excluded.operating_system, ''), {DEVICE_TABLE}.operating_system),
                uptime=COALESCE(NULLIF(excluded.uptime, 0), {DEVICE_TABLE}.uptime),
                agent_id=COALESCE(NULLIF(excluded.agent_id, ''), {DEVICE_TABLE}.agent_id),
                ansible_ee_ver=COALESCE(NULLIF(excluded.ansible_ee_ver, ''), {DEVICE_TABLE}.ansible_ee_ver),
                connection_type=COALESCE(NULLIF(excluded.connection_type, ''), {DEVICE_TABLE}.connection_type),
                connection_endpoint=COALESCE(NULLIF(excluded.connection_endpoint, ''), {DEVICE_TABLE}.connection_endpoint),
                ssl_key_fingerprint=COALESCE(NULLIF(excluded.ssl_key_fingerprint, ''), {DEVICE_TABLE}.ssl_key_fingerprint),
                token_version=COALESCE(NULLIF(excluded.token_version, 0), {DEVICE_TABLE}.token_version),
                status=COALESCE(NULLIF(excluded.status, ''), {DEVICE_TABLE}.status),
                key_added_at=COALESCE(NULLIF(excluded.key_added_at, ''), {DEVICE_TABLE}.key_added_at)
        """

        params: List[Any] = [
            hostname,
            normalized_description,
            created_ts,
            normalized_hash,
            normalized_guid,
            column_values.get("memory"),
            column_values.get("network"),
            column_values.get("software"),
            column_values.get("storage"),
            column_values.get("cpu"),
            column_values.get("device_type"),
            column_values.get("domain"),
            column_values.get("external_ip"),
            column_values.get("internal_ip"),
            column_values.get("last_reboot"),
            column_values.get("last_seen"),
            column_values.get("last_user"),
            column_values.get("operating_system"),
            column_values.get("uptime"),
            column_values.get("agent_id"),
            column_values.get("ansible_ee_ver"),
            column_values.get("connection_type"),
            column_values.get("connection_endpoint"),
            column_values.get("ssl_key_fingerprint"),
            column_values.get("token_version"),
            column_values.get("status"),
            column_values.get("key_added_at"),
        ]

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, params)
            conn.commit()

    def delete_device_by_hostname(self, hostname: str) -> None:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute("DELETE FROM device_sites WHERE device_hostname = ?", (hostname,))
            cur.execute(f"DELETE FROM {DEVICE_TABLE} WHERE hostname = ?", (hostname,))
            conn.commit()

    def record_device_fingerprint(self, guid: Optional[str], fingerprint: Optional[str], added_at: str) -> None:
        normalized_guid = clean_device_str(guid)
        normalized_fp = clean_device_str(fingerprint)
        if not normalized_guid or not normalized_fp:
            return

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
                VALUES (?, ?, ?, ?)
                """,
                (str(uuid.uuid4()), normalized_guid, normalized_fp.lower(), added_at),
            )
            cur.execute(
                """
                UPDATE device_keys
                   SET retired_at = ?
                 WHERE guid = ?
                   AND ssl_key_fingerprint != ?
                   AND retired_at IS NULL
                """,
                (added_at, normalized_guid, normalized_fp.lower()),
            )
            cur.execute(
                """
                UPDATE devices
                   SET ssl_key_fingerprint = COALESCE(LOWER(?), ssl_key_fingerprint),
                       key_added_at = COALESCE(key_added_at, ?)
                 WHERE LOWER(guid) = LOWER(?)
                """,
                (normalized_fp, added_at, normalized_guid),
            )
            conn.commit()

    def _extract_device_columns(self, details: Dict[str, Any]) -> Dict[str, Any]:
        summary = details.get("summary") or {}
        payload: Dict[str, Any] = {}
        for field in ("memory", "network", "software", "storage"):
            payload[field] = serialize_device_json(details.get(field), [])
        payload["cpu"] = serialize_device_json(summary.get("cpu") or details.get("cpu"), {})
        payload["device_type"] = clean_device_str(summary.get("device_type") or summary.get("type"))
        payload["domain"] = clean_device_str(summary.get("domain"))
        payload["external_ip"] = clean_device_str(summary.get("external_ip") or summary.get("public_ip"))
        payload["internal_ip"] = clean_device_str(summary.get("internal_ip") or summary.get("private_ip"))
        payload["last_reboot"] = clean_device_str(summary.get("last_reboot") or summary.get("last_boot"))
        payload["last_seen"] = coerce_int(summary.get("last_seen"))
        payload["last_user"] = clean_device_str(
            summary.get("last_user")
            or summary.get("last_user_name")
            or summary.get("logged_in_user")
        )
        payload["operating_system"] = clean_device_str(
            summary.get("operating_system") or summary.get("os")
        )
        payload["uptime"] = coerce_int(summary.get("uptime"))
        payload["agent_id"] = clean_device_str(summary.get("agent_id"))
        payload["ansible_ee_ver"] = clean_device_str(summary.get("ansible_ee_ver"))
        payload["connection_type"] = clean_device_str(summary.get("connection_type"))
        payload["connection_endpoint"] = clean_device_str(
            summary.get("connection_endpoint") or summary.get("endpoint")
        )
        payload["ssl_key_fingerprint"] = clean_device_str(summary.get("ssl_key_fingerprint"))
        payload["token_version"] = coerce_int(summary.get("token_version")) or 0
        payload["status"] = clean_device_str(summary.get("status"))
        payload["key_added_at"] = clean_device_str(summary.get("key_added_at"))
        return payload
