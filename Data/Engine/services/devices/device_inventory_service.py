"""Mirrors the legacy device inventory HTTP behaviour."""

from __future__ import annotations

import logging
import sqlite3
import time
from collections.abc import Mapping
from typing import Any, Dict, List, Optional

from Data.Engine.repositories.sqlite.device_inventory_repository import (
    SQLiteDeviceInventoryRepository,
)
from Data.Engine.domain.device_auth import DeviceAuthContext
from Data.Engine.domain.devices import clean_device_str, coerce_int

__all__ = ["DeviceInventoryService", "RemoteDeviceError", "DeviceHeartbeatError"]


class RemoteDeviceError(Exception):
    def __init__(self, code: str, message: Optional[str] = None) -> None:
        super().__init__(message or code)
        self.code = code


class DeviceHeartbeatError(Exception):
    def __init__(self, code: str, message: Optional[str] = None) -> None:
        super().__init__(message or code)
        self.code = code


class DeviceInventoryService:
    def __init__(
        self,
        repository: SQLiteDeviceInventoryRepository,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._repo = repository
        self._log = logger or logging.getLogger("borealis.engine.services.devices")

    def list_devices(self) -> List[Dict[str, object]]:
        return self._repo.fetch_devices()

    def list_agent_devices(self) -> List[Dict[str, object]]:
        return self._repo.fetch_devices(only_agents=True)

    def list_remote_devices(self, connection_type: str) -> List[Dict[str, object]]:
        return self._repo.fetch_devices(connection_type=connection_type)

    def get_device_by_guid(self, guid: str) -> Optional[Dict[str, object]]:
        snapshot = self._repo.load_snapshot(guid=guid)
        if not snapshot:
            return None
        devices = self._repo.fetch_devices(hostname=snapshot.get("hostname"))
        return devices[0] if devices else None

    def collect_agent_hash_records(self) -> List[Dict[str, object]]:
        records: List[Dict[str, object]] = []
        key_to_index: Dict[str, int] = {}

        for device in self._repo.fetch_devices():
            summary = device.get("summary", {}) if isinstance(device, dict) else {}
            agent_id = (summary.get("agent_id") or "").strip()
            agent_guid = (summary.get("agent_guid") or "").strip()
            hostname = (summary.get("hostname") or device.get("hostname") or "").strip()
            agent_hash = (summary.get("agent_hash") or device.get("agent_hash") or "").strip()

            keys: List[str] = []
            if agent_id:
                keys.append(f"id:{agent_id.lower()}")
            if agent_guid:
                keys.append(f"guid:{agent_guid.lower()}")
            if hostname:
                keys.append(f"host:{hostname.lower()}")

            payload = {
                "agent_id": agent_id or None,
                "agent_guid": agent_guid or None,
                "hostname": hostname or None,
                "agent_hash": agent_hash or None,
                "source": "database",
            }

            if not keys:
                records.append(payload)
                continue

            existing_index = None
            for key in keys:
                if key in key_to_index:
                    existing_index = key_to_index[key]
                    break

            if existing_index is None:
                existing_index = len(records)
                records.append(payload)
                for key in keys:
                    key_to_index[key] = existing_index
                continue

            merged = records[existing_index]
            for key in ("agent_id", "agent_guid", "hostname", "agent_hash"):
                if not merged.get(key) and payload.get(key):
                    merged[key] = payload[key]

        return records

    def upsert_remote_device(
        self,
        connection_type: str,
        hostname: str,
        address: Optional[str],
        description: Optional[str],
        os_hint: Optional[str],
        *,
        ensure_existing_type: Optional[str],
    ) -> Dict[str, object]:
        normalized_type = (connection_type or "").strip().lower()
        if not normalized_type:
            raise RemoteDeviceError("invalid_type", "connection type required")
        normalized_host = (hostname or "").strip()
        if not normalized_host:
            raise RemoteDeviceError("invalid_hostname", "hostname is required")

        existing = self._repo.load_snapshot(hostname=normalized_host)
        existing_type = (existing or {}).get("summary", {}).get("connection_type") or ""
        existing_type = existing_type.strip().lower()

        if ensure_existing_type and existing_type != ensure_existing_type.lower():
            raise RemoteDeviceError("not_found", "device not found")
        if ensure_existing_type is None and existing_type and existing_type != normalized_type:
            raise RemoteDeviceError("conflict", "device already exists with different connection type")

        created_ts = None
        if existing:
            created_ts = existing.get("summary", {}).get("created_at")

        endpoint = (address or "").strip() or (existing or {}).get("summary", {}).get("connection_endpoint") or ""
        if not endpoint:
            raise RemoteDeviceError("address_required", "address is required")

        description_val = description if description is not None else (existing or {}).get("summary", {}).get("description")
        os_value = os_hint or (existing or {}).get("summary", {}).get("operating_system")
        os_value = (os_value or "").strip()

        device_type_label = "SSH Remote" if normalized_type == "ssh" else "WinRM Remote"

        summary_payload = {
            "connection_type": normalized_type,
            "connection_endpoint": endpoint,
            "internal_ip": endpoint,
            "external_ip": endpoint,
            "device_type": device_type_label,
            "operating_system": os_value or "",
            "last_seen": 0,
            "description": (description_val or ""),
        }

        try:
            self._repo.upsert_device(
                normalized_host,
                description_val,
                {"summary": summary_payload},
                created_ts,
            )
        except sqlite3.DatabaseError as exc:  # type: ignore[name-defined]
            raise RemoteDeviceError("storage_error", str(exc)) from exc
        except Exception as exc:  # pragma: no cover - defensive
            raise RemoteDeviceError("storage_error", str(exc)) from exc

        devices = self._repo.fetch_devices(hostname=normalized_host)
        if not devices:
            raise RemoteDeviceError("reload_failed", "failed to load device after upsert")
        return devices[0]

    def delete_remote_device(self, connection_type: str, hostname: str) -> None:
        normalized_host = (hostname or "").strip()
        if not normalized_host:
            raise RemoteDeviceError("invalid_hostname", "invalid hostname")
        existing = self._repo.load_snapshot(hostname=normalized_host)
        if not existing:
            raise RemoteDeviceError("not_found", "device not found")
        existing_type = (existing.get("summary", {}) or {}).get("connection_type") or ""
        if (existing_type or "").strip().lower() != (connection_type or "").strip().lower():
            raise RemoteDeviceError("not_found", "device not found")
        self._repo.delete_device_by_hostname(normalized_host)

    # ------------------------------------------------------------------
    # Agent heartbeats
    # ------------------------------------------------------------------
    def record_heartbeat(
        self,
        *,
        context: DeviceAuthContext,
        payload: Mapping[str, Any],
    ) -> None:
        guid = context.identity.guid.value
        snapshot = self._repo.load_snapshot(guid=guid)
        if not snapshot:
            raise DeviceHeartbeatError("device_not_registered", "device not registered")

        summary = dict(snapshot.get("summary") or {})
        details = dict(snapshot.get("details") or {})

        now_ts = int(time.time())
        summary["last_seen"] = now_ts
        summary["agent_guid"] = guid

        existing_hostname = clean_device_str(summary.get("hostname")) or clean_device_str(
            snapshot.get("hostname")
        )
        incoming_hostname = clean_device_str(payload.get("hostname"))
        raw_metrics = payload.get("metrics")
        metrics = raw_metrics if isinstance(raw_metrics, Mapping) else {}
        metrics_hostname = clean_device_str(metrics.get("hostname")) if metrics else None
        hostname = incoming_hostname or metrics_hostname or existing_hostname
        if not hostname:
            hostname = f"RECOVERED-{guid[:12]}"
        summary["hostname"] = hostname

        if metrics:
            last_user = metrics.get("last_user") or metrics.get("username")
            if last_user:
                cleaned_user = clean_device_str(last_user)
                if cleaned_user:
                    summary["last_user"] = cleaned_user
            operating_system = metrics.get("operating_system")
            if operating_system:
                cleaned_os = clean_device_str(operating_system)
                if cleaned_os:
                    summary["operating_system"] = cleaned_os
            uptime = metrics.get("uptime")
            if uptime is not None:
                coerced = coerce_int(uptime)
                if coerced is not None:
                    summary["uptime"] = coerced
            agent_id = metrics.get("agent_id")
            if agent_id:
                cleaned_agent = clean_device_str(agent_id)
                if cleaned_agent:
                    summary["agent_id"] = cleaned_agent

        for field in ("external_ip", "internal_ip", "device_type"):
            value = payload.get(field)
            cleaned = clean_device_str(value)
            if cleaned:
                summary[field] = cleaned

        summary.setdefault("description", summary.get("description") or "")
        created_at = coerce_int(summary.get("created_at"))
        if created_at is None:
            created_at = coerce_int(snapshot.get("created_at"))
        if created_at is None:
            created_at = now_ts
        summary["created_at"] = created_at

        raw_inventory = payload.get("inventory")
        inventory = raw_inventory if isinstance(raw_inventory, Mapping) else {}
        memory = inventory.get("memory") if isinstance(inventory.get("memory"), list) else details.get("memory")
        network = inventory.get("network") if isinstance(inventory.get("network"), list) else details.get("network")
        software = (
            inventory.get("software") if isinstance(inventory.get("software"), list) else details.get("software")
        )
        storage = inventory.get("storage") if isinstance(inventory.get("storage"), list) else details.get("storage")
        cpu = inventory.get("cpu") if isinstance(inventory.get("cpu"), Mapping) else details.get("cpu")

        merged_details: Dict[str, Any] = {
            "summary": summary,
            "memory": memory,
            "network": network,
            "software": software,
            "storage": storage,
            "cpu": cpu,
        }

        try:
            self._repo.upsert_device(
                summary["hostname"],
                summary.get("description"),
                merged_details,
                summary.get("created_at"),
                agent_hash=clean_device_str(summary.get("agent_hash")),
                guid=guid,
            )
        except sqlite3.IntegrityError as exc:
            self._log.warning(
                "device-heartbeat-conflict guid=%s hostname=%s error=%s",
                guid,
                summary["hostname"],
                exc,
            )
            raise DeviceHeartbeatError("storage_conflict", str(exc)) from exc
        except Exception as exc:  # pragma: no cover - defensive
            self._log.exception(
                "device-heartbeat-failure guid=%s hostname=%s",
                guid,
                summary["hostname"],
                exc_info=exc,
            )
            raise DeviceHeartbeatError("storage_error", "failed to persist heartbeat") from exc

