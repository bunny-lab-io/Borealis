from __future__ import annotations

from typing import Any, Dict, List, Tuple

try:
    from Roles.role_system_device_auditor import (  # type: ignore
        _normalize_display_icon_hint as _legacy_normalize_display_icon_hint,
    )
    from Roles.role_system_device_auditor import (  # type: ignore
        _software_icon_signature as _legacy_software_icon_signature,
    )
    from Roles.role_system_device_auditor import attach_windows_software_icons as _legacy_attach_windows_software_icons  # type: ignore
    from Roles.role_system_device_auditor import collect_software as _legacy_collect_software  # type: ignore
except Exception:  # pragma: no cover - package import fallback
    from Data.Agent.Roles.role_system_device_auditor import (  # type: ignore
        _normalize_display_icon_hint as _legacy_normalize_display_icon_hint,
    )
    from Data.Agent.Roles.role_system_device_auditor import (  # type: ignore
        _software_icon_signature as _legacy_software_icon_signature,
    )
    from Data.Agent.Roles.role_system_device_auditor import attach_windows_software_icons as _legacy_attach_windows_software_icons  # type: ignore
    from Data.Agent.Roles.role_system_device_auditor import collect_software as _legacy_collect_software  # type: ignore


def normalize_display_icon_hint(value: Any) -> str:
    return _legacy_normalize_display_icon_hint(value)


def software_icon_signature(rows: Any) -> str:
    return _legacy_software_icon_signature(rows)


def attach_windows_software_icons(
    rows: Any,
    *,
    previous_icon_hash_by_key: Any = None,
) -> Tuple[List[Dict[str, Any]], Dict[str, str]]:
    return _legacy_attach_windows_software_icons(rows, previous_icon_hash_by_key=previous_icon_hash_by_key)


def collect_software() -> List[Dict[str, Any]]:
    return _legacy_collect_software()


def build_software_inventory_snapshot(
    *,
    previous_icon_hash_by_key: Dict[str, str] | None = None,
    previous_signature: str = "",
) -> Dict[str, Any]:
    rows = collect_software()
    signature = software_icon_signature(rows)
    icon_payloads, icon_hash_by_key = attach_windows_software_icons(
        rows,
        previous_icon_hash_by_key=previous_icon_hash_by_key if signature == previous_signature else {},
    )
    icon_candidate_count = 0
    for row in rows:
        if not isinstance(row, dict):
            continue
        if str(row.get("source") or "").strip().lower() != "local_installed":
            continue
        metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        if normalize_display_icon_hint(metadata.get("display_icon")):
            icon_candidate_count += 1
    return {
        "software": rows,
        "software_icon_payloads": icon_payloads,
        "software_icon_hash_by_key": icon_hash_by_key,
        "software_icon_signature": signature,
        "software_icon_candidate_count": icon_candidate_count,
    }
