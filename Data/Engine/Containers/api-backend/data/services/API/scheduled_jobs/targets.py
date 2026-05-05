# ======================================================
# Data\Engine\services\API\scheduled_jobs\targets.py
# Description: Shared scheduled-job target normalization and device-pruning helpers.
#
# API Endpoints (if applicable): None
# ======================================================

"""Helpers for saved scheduled-job target definitions."""

from __future__ import annotations

from typing import Any, Dict, List, Optional, Sequence, Tuple

from ....auth.guid_utils import normalize_guid


def _normalize_site_id(value: Any) -> Optional[int]:
    try:
        return int(value) if value not in (None, "", "null") else None
    except Exception:
        return None


def normalize_targets_for_save(entries: Sequence[Any]) -> List[Any]:
    normalized: List[Any] = []
    seen_filters: set[int] = set()
    seen_devices: set[str] = set()
    seen_onboarding_scopes: set[str] = set()
    include_all_devices = False
    if not isinstance(entries, (list, tuple)):
        return normalized
    for entry in entries:
        if isinstance(entry, str):
            host = entry.strip()
            if not host:
                continue
            lowered = host.lower()
            if lowered in seen_devices:
                continue
            seen_devices.add(lowered)
            normalized.append(host)
            continue
        if not isinstance(entry, dict):
            continue
        kind = str(entry.get("kind") or entry.get("type") or "").strip().lower()
        if kind == "onboarding_scope":
            site_id_value = _normalize_site_id(entry.get("site_id") or entry.get("siteId"))
            if site_id_value is None:
                continue
            raw_entries = entry.get("entries")
            if raw_entries is None:
                raw_entries = entry.get("scope") or entry.get("targets") or entry.get("discovery_scope")
            if isinstance(raw_entries, str):
                scope_entries = [line.strip() for line in raw_entries.replace(",", "\n").splitlines() if line.strip()]
            elif isinstance(raw_entries, (list, tuple)):
                scope_entries = [str(value).strip() for value in raw_entries if str(value).strip()]
            else:
                scope_entries = []
            if not scope_entries:
                continue
            dedupe_key = f"onboarding:{site_id_value}:{'|'.join(value.lower() for value in scope_entries)}"
            if dedupe_key in seen_onboarding_scopes:
                continue
            seen_onboarding_scopes.add(dedupe_key)
            normalized.append(
                {
                    "kind": "onboarding_scope",
                    "site_id": site_id_value,
                    "site_name": entry.get("site_name") or entry.get("site") or "",
                    "entries": scope_entries,
                }
            )
            continue
        if kind == "all_devices" or entry.get("all_devices") is True:
            if include_all_devices:
                continue
            include_all_devices = True
            normalized.append(
                {
                    "kind": "all_devices",
                    "name": entry.get("name") or "All Devices in Scope",
                }
            )
            continue
        if kind == "filter" or entry.get("filter_id") is not None:
            filter_id = entry.get("filter_id") or entry.get("id")
            try:
                filter_id_int = int(filter_id)
            except (TypeError, ValueError):
                continue
            if filter_id_int in seen_filters:
                continue
            seen_filters.add(filter_id_int)
            normalized.append(
                {
                    "kind": "filter",
                    "filter_id": filter_id_int,
                    "name": entry.get("name"),
                }
            )
            continue
        hostname = entry.get("hostname")
        if hostname is None:
            continue
        host = str(hostname).strip()
        if not host:
            continue
        device_guid = normalize_guid(str(entry.get("device_guid") or entry.get("guid") or "").strip()) or ""
        site_id_value = _normalize_site_id(entry.get("site_id"))
        if device_guid:
            dedupe_key = f"guid:{device_guid.lower()}"
        elif site_id_value is not None:
            dedupe_key = f"site:{site_id_value}:{host.lower()}"
        else:
            dedupe_key = host.lower()
        if dedupe_key in seen_devices:
            continue
        seen_devices.add(dedupe_key)
        normalized.append(
            {
                "kind": "device",
                "device_guid": device_guid.lower(),
                "hostname": host,
                "site_id": site_id_value,
                "site_name": entry.get("site_name") or entry.get("site") or "",
            }
        )
    return normalized


def prune_device_targets(
    entries: Sequence[Any],
    *,
    device_guid: Optional[str],
    hostname: Optional[str],
    site_id: Optional[int] = None,
) -> Tuple[List[Any], int]:
    normalized_guid = normalize_guid(device_guid or "")
    normalized_host = str(hostname or "").strip().lower()
    normalized_site_id = _normalize_site_id(site_id)
    updated: List[Any] = []
    removed = 0
    for entry in normalize_targets_for_save(entries):
        drop = False
        if isinstance(entry, str):
            drop = bool(normalized_host and entry.strip().lower() == normalized_host)
        elif isinstance(entry, dict):
            kind = str(entry.get("kind") or entry.get("type") or "").strip().lower()
            if kind == "all_devices" or entry.get("all_devices") is True:
                drop = False
            elif kind == "filter" or entry.get("filter_id") is not None:
                drop = False
            else:
                entry_guid = normalize_guid(str(entry.get("device_guid") or entry.get("guid") or "").strip())
                entry_host = str(entry.get("hostname") or "").strip().lower()
                entry_site_id = _normalize_site_id(entry.get("site_id"))
                if normalized_guid and entry_guid and entry_guid == normalized_guid:
                    drop = True
                elif normalized_host and entry_host == normalized_host:
                    if normalized_site_id is None or entry_site_id is None or entry_site_id == normalized_site_id:
                        drop = True
        if drop:
            removed += 1
            continue
        updated.append(entry)
    return updated, removed


__all__ = ["normalize_targets_for_save", "prune_device_targets"]
