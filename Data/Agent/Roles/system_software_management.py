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
except ModuleNotFoundError as exc:  # pragma: no cover - package import fallback
    if not str(getattr(exc, "name", "") or "").startswith("Roles"):
        raise
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


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _software_metadata(row: Any) -> Dict[str, Any]:
    if not isinstance(row, dict):
        return {}
    return row.get("metadata") if isinstance(row.get("metadata"), dict) else {}


def _matches_any_substring(value: Any, candidates: Any) -> bool:
    value_lower = _clean_text(value).lower()
    for item in candidates or []:
        needle = _clean_text(item).lower()
        if needle and needle in value_lower:
            return True
    return False


def _software_matches_icon_override(row: Dict[str, Any], rule: Dict[str, Any]) -> bool:
    metadata = _software_metadata(row)
    source = _clean_text(row.get("source")).lower()
    name = _clean_text(row.get("name"))
    version = _clean_text(row.get("version"))
    publisher = _clean_text(metadata.get("publisher"))
    install_location = _clean_text(metadata.get("install_location"))

    expected_source = _clean_text(rule.get("source")).lower()
    if expected_source and expected_source != source:
        return False

    expected_name = _clean_text(rule.get("name"))
    if expected_name and expected_name.lower() != name.lower():
        return False

    expected_version = _clean_text(rule.get("version"))
    if expected_version and expected_version.lower() != version.lower():
        return False

    publishers = rule.get("publisher_contains_any") if isinstance(rule.get("publisher_contains_any"), list) else []
    if publishers and not _matches_any_substring(publisher, publishers):
        return False

    names = rule.get("name_contains_any") if isinstance(rule.get("name_contains_any"), list) else []
    if names and not _matches_any_substring(name, names):
        return False

    install_markers = (
        rule.get("install_location_contains_any") if isinstance(rule.get("install_location_contains_any"), list) else []
    )
    if install_markers and not _matches_any_substring(install_location, install_markers):
        return False

    return True


def apply_software_icon_overrides(rows: Any, overrides: Any) -> List[Dict[str, Any]]:
    normalized_rows = rows if isinstance(rows, list) else []
    normalized_overrides = overrides if isinstance(overrides, list) else []
    for row in normalized_rows:
        if not isinstance(row, dict):
            continue
        metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        row["metadata"] = metadata
        for rule in normalized_overrides:
            if not isinstance(rule, dict):
                continue
            display_icon = normalize_display_icon_hint(rule.get("display_icon") or rule.get("icon_location"))
            if not display_icon:
                continue
            if not _software_matches_icon_override(row, rule):
                continue
            original_display_icon = _clean_text(metadata.get("display_icon"))
            if original_display_icon and original_display_icon != display_icon and not metadata.get("original_display_icon"):
                metadata["original_display_icon"] = original_display_icon
            metadata["display_icon"] = display_icon
            metadata["display_icon_override"] = display_icon
            metadata["display_icon_override_rule_id"] = _clean_text(rule.get("rule_id"))
            break
    return normalized_rows


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
    icon_overrides: List[Dict[str, Any]] | None = None,
) -> Dict[str, Any]:
    rows = collect_software()
    if icon_overrides:
        rows = apply_software_icon_overrides(rows, icon_overrides)
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
