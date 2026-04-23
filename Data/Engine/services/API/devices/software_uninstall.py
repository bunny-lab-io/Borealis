# ======================================================
# Data\Engine\services\API\devices\software_uninstall.py
# Description: Shared Windows software uninstall capability resolution and
# command derivation used by device details and uninstall actions.
# ======================================================

"""Shared Windows software uninstall capability resolution for Borealis."""
from __future__ import annotations

import json
import logging
import re
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


_SOFTWARE_SOURCE_ALIASES = {
    "appx": "windows_store",
    "installed": "local_installed",
    "local": "local_installed",
    "local_installed": "local_installed",
    "ms_store": "windows_store",
    "registry": "local_installed",
    "store": "windows_store",
    "uninstall_registry": "local_installed",
    "windows_store": "windows_store",
}

_WINDOWS_PRODUCT_CODE_RE = re.compile(
    r"^\{[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}$"
)
_WINDOWS_STORE_GUID_NAME_RE = re.compile(
    r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
)
_WINDOWS_PRODUCT_CODE_IN_TEXT_RE = re.compile(
    r"\{[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}"
)
_WINDOWS_QUIET_SWITCH_RE = re.compile(
    r"(?i)(^|\s)(/quiet|/qn|/qb!?|/passive|/s(\s|$)|/silent|/verysilent|--silent|--quiet|/suppressmsgboxes)(\s|$)"
)
_STEAM_UNINSTALL_PROTOCOL_RE = re.compile(r"(?i)\bsteam://uninstall/(?P<app_id>\d+)\b")
_STEAM_LIBRARY_PATH_RE = re.compile(r"(?i)(^|[\\/])steamapps[\\/]+common([\\/]|$)")
_WINDOWS_QUOTED_COMMAND_RE = re.compile(r'^\s*"(?P<exe>[^"]+)"\s*(?P<args>.*)$')
_WINDOWS_COMMAND_WITH_EXTENSION_RE = re.compile(
    r"^\s*(?P<exe>(?:(?:[A-Za-z]:|\\\\[^\\\/]+\\[^\\\/]+)[^\r\n\"]*?\.(?:exe|com|cmd|bat|msi|ps1)|[^\\/\s\"']+\.(?:exe|com|cmd|bat|msi|ps1)))\s*(?P<args>.*)$",
    re.IGNORECASE,
)

_WINDOWS_UNINSTALL_RULES: List[Dict[str, Any]] = [
    {
        "rule_id": "7zip_uninstall_silent",
        "source": "local_installed",
        "publisher_contains_any": ["igor pavlov"],
        "name_contains_any": ["7-zip"],
        "exe_names": ["uninstall.exe"],
        "append_args": ["/S"],
        "summary": "7-Zip uninstall supports /S.",
    },
    {
        "rule_id": "mozilla_helper_silent",
        "source": "local_installed",
        "publisher_contains_any": ["mozilla", "betterbird project"],
        "exe_names": ["helper.exe"],
        "append_args": ["/S"],
        "summary": "Mozilla helper.exe supports /S silent uninstall.",
    },
    {
        "rule_id": "irfanview_uninstall_silent",
        "source": "local_installed",
        "publisher_contains_any": ["irfan skiljan"],
        "name_contains_any": ["irfanview"],
        "exe_names": ["iv_uninstall.exe"],
        "append_args": ["/silent"],
        "summary": "IrfanView uninstall supports /silent.",
    },
    {
        "rule_id": "edge_setup_force_uninstall",
        "source": "local_installed",
        "publisher_contains_any": ["microsoft corporation"],
        "name_contains_any": ["microsoft edge", "webview2 runtime"],
        "exe_names": ["setup.exe"],
        "uninstall_contains_any": ["--uninstall", "--msedge", "--msedgewebview"],
        "append_args": ["--force-uninstall"],
        "summary": "Edge setup.exe uninstall can be forced silent.",
    },
    {
        "rule_id": "chrome_setup_force_uninstall",
        "source": "local_installed",
        "publisher_contains_any": ["google llc"],
        "name_contains_any": ["google chrome"],
        "exe_names": ["setup.exe"],
        "uninstall_contains_any": ["--uninstall"],
        "append_args": ["--force-uninstall"],
        "summary": "Chrome setup.exe uninstall can be forced silent.",
    },
]

logger = logging.getLogger(__name__)

_UNINSTALL_BLOCKLIST_PATH = Path(__file__).resolve().with_name("software_uninstall_blocklist.json")
_UNINSTALL_OVERRIDES_PATH = Path(__file__).resolve().with_name("software_uninstall_overrides.json")
_UNINSTALL_BLOCKLIST_CACHE_MTIME_NS: Optional[int] = None
_UNINSTALL_BLOCKLIST_CACHE: Dict[str, List[Dict[str, Any]]] = {
    "windows_quiet_uninstall_blocklist": [],
}
_UNINSTALL_OVERRIDES_CACHE_MTIME_NS: Optional[int] = None
_UNINSTALL_OVERRIDES_CACHE: Dict[str, List[Dict[str, Any]]] = {
    "windows_uninstall_overrides": [],
}


def normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_blocklist_rows(value: Any) -> List[Dict[str, Any]]:
    rows = value if isinstance(value, list) else []
    normalized: List[Dict[str, Any]] = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        normalized.append({str(key): item for key, item in row.items() if normalize_text(key)})
    return normalized


def _normalize_uninstall_override_rows(value: Any) -> List[Dict[str, Any]]:
    rows = value if isinstance(value, list) else []
    normalized: List[Dict[str, Any]] = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        normalized.append({str(key): item for key, item in row.items() if normalize_text(key)})
    return normalized


def _load_uninstall_blocklist() -> Dict[str, List[Dict[str, Any]]]:
    global _UNINSTALL_BLOCKLIST_CACHE_MTIME_NS, _UNINSTALL_BLOCKLIST_CACHE

    try:
        current_mtime_ns = _UNINSTALL_BLOCKLIST_PATH.stat().st_mtime_ns
    except OSError:
        current_mtime_ns = None

    if current_mtime_ns is not None and _UNINSTALL_BLOCKLIST_CACHE_MTIME_NS == current_mtime_ns:
        return _UNINSTALL_BLOCKLIST_CACHE

    next_cache = {
        "windows_quiet_uninstall_blocklist": [],
    }
    if current_mtime_ns is None:
        _UNINSTALL_BLOCKLIST_CACHE = next_cache
        _UNINSTALL_BLOCKLIST_CACHE_MTIME_NS = None
        return _UNINSTALL_BLOCKLIST_CACHE

    try:
        with _UNINSTALL_BLOCKLIST_PATH.open("r", encoding="utf-8") as handle:
            payload = json.load(handle)
    except Exception as exc:
        logger.warning("Failed to load uninstall blocklist from %s: %s", _UNINSTALL_BLOCKLIST_PATH, exc)
        _UNINSTALL_BLOCKLIST_CACHE_MTIME_NS = current_mtime_ns
        return _UNINSTALL_BLOCKLIST_CACHE

    if isinstance(payload, dict):
        next_cache["windows_quiet_uninstall_blocklist"] = _normalize_blocklist_rows(
            payload.get("windows_quiet_uninstall_blocklist")
        )
    else:
        logger.warning("Uninstall blocklist payload is not an object: %s", _UNINSTALL_BLOCKLIST_PATH)
        _UNINSTALL_BLOCKLIST_CACHE_MTIME_NS = current_mtime_ns
        return _UNINSTALL_BLOCKLIST_CACHE

    _UNINSTALL_BLOCKLIST_CACHE = next_cache
    _UNINSTALL_BLOCKLIST_CACHE_MTIME_NS = current_mtime_ns
    return _UNINSTALL_BLOCKLIST_CACHE


def _load_uninstall_overrides() -> Dict[str, List[Dict[str, Any]]]:
    global _UNINSTALL_OVERRIDES_CACHE_MTIME_NS, _UNINSTALL_OVERRIDES_CACHE

    try:
        current_mtime_ns = _UNINSTALL_OVERRIDES_PATH.stat().st_mtime_ns
    except OSError:
        current_mtime_ns = None

    if current_mtime_ns is not None and _UNINSTALL_OVERRIDES_CACHE_MTIME_NS == current_mtime_ns:
        return _UNINSTALL_OVERRIDES_CACHE

    next_cache = {
        "windows_uninstall_overrides": [],
    }
    if current_mtime_ns is None:
        _UNINSTALL_OVERRIDES_CACHE = next_cache
        _UNINSTALL_OVERRIDES_CACHE_MTIME_NS = None
        return _UNINSTALL_OVERRIDES_CACHE

    try:
        with _UNINSTALL_OVERRIDES_PATH.open("r", encoding="utf-8") as handle:
            payload = json.load(handle)
    except Exception as exc:
        logger.warning("Failed to load uninstall overrides from %s: %s", _UNINSTALL_OVERRIDES_PATH, exc)
        _UNINSTALL_OVERRIDES_CACHE_MTIME_NS = current_mtime_ns
        return _UNINSTALL_OVERRIDES_CACHE

    if isinstance(payload, dict):
        next_cache["windows_uninstall_overrides"] = _normalize_uninstall_override_rows(
            payload.get("windows_uninstall_overrides")
        )
    else:
        logger.warning("Uninstall overrides payload is not an object: %s", _UNINSTALL_OVERRIDES_PATH)
        _UNINSTALL_OVERRIDES_CACHE_MTIME_NS = current_mtime_ns
        return _UNINSTALL_OVERRIDES_CACHE

    _UNINSTALL_OVERRIDES_CACHE = next_cache
    _UNINSTALL_OVERRIDES_CACHE_MTIME_NS = current_mtime_ns
    return _UNINSTALL_OVERRIDES_CACHE


def normalize_software_source(value: Any) -> str:
    normalized = normalize_text(value).lower()
    return _SOFTWARE_SOURCE_ALIASES.get(normalized, normalized or "local_installed")


def _metadata_value_present(value: Any) -> bool:
    if value is None:
        return False
    if value == "":
        return False
    if isinstance(value, (list, tuple, set, dict)) and not value:
        return False
    return True


def software_metadata(entry: Any) -> Dict[str, Any]:
    if not isinstance(entry, dict):
        return {}
    metadata: Dict[str, Any] = {}
    raw_metadata = entry.get("metadata")
    if isinstance(raw_metadata, dict):
        metadata = {str(key): value for key, value in raw_metadata.items() if normalize_text(key)}
    for key, value in entry.items():
        key_text = normalize_text(key)
        if key_text in {
            "name",
            "version",
            "source",
            "metadata",
            "uninstall",
            "distribution_platform",
            "distribution_app_id",
        }:
            continue
        if not _metadata_value_present(value):
            continue
        if not _metadata_value_present(metadata.get(key_text)):
            metadata[key_text] = value
    return metadata


def normalize_software_inventory(raw: Any) -> List[Dict[str, Any]]:
    entries = raw if isinstance(raw, list) else []
    normalized: List[Dict[str, Any]] = []
    seen: set[Tuple[str, str, str]] = set()
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        name = normalize_text(entry.get("name"))
        if not name:
            continue
        version = normalize_text(entry.get("version"))
        source = normalize_software_source(entry.get("source"))
        if source == "windows_store" and _WINDOWS_STORE_GUID_NAME_RE.match(name):
            continue
        metadata = software_metadata(entry)
        key = (name.lower(), version.lower(), source)
        if key in seen:
            continue
        seen.add(key)
        normalized.append(
            {
                "name": name,
                "version": version,
                "source": source,
                "metadata": metadata if isinstance(metadata, dict) else {},
            }
        )
    normalized.sort(
        key=lambda item: (
            normalize_text(item.get("name")).lower(),
            normalize_text(item.get("source")).lower(),
            normalize_text(item.get("version")).lower(),
        )
    )
    return normalized


def coerce_optional_bool(value: Any) -> Optional[bool]:
    if isinstance(value, bool):
        return value
    text = normalize_text(value).lower()
    if not text:
        return None
    if text in {"1", "true", "yes", "y"}:
        return True
    if text in {"0", "false", "no", "n"}:
        return False
    return None


def command_has_quiet_switch(text: Any) -> bool:
    return bool(_WINDOWS_QUIET_SWITCH_RE.search(normalize_text(text)))


def find_msi_product_code(text: Any) -> str:
    match = _WINDOWS_PRODUCT_CODE_IN_TEXT_RE.search(normalize_text(text))
    return match.group(0).upper() if match else ""


def split_windows_command_line(command_line: Any) -> Optional[Dict[str, str]]:
    command = normalize_text(command_line)
    if not command:
        return None
    match = _WINDOWS_QUOTED_COMMAND_RE.match(command)
    if match:
        file_path = normalize_text(match.group("exe"))
        return {
            "file_path": file_path,
            "arguments": normalize_text(match.group("args")),
            "executable_name": file_path.rsplit("\\", 1)[-1],
        }
    match = _WINDOWS_COMMAND_WITH_EXTENSION_RE.match(command)
    if match:
        file_path = normalize_text(match.group("exe"))
        return {
            "file_path": file_path,
            "arguments": normalize_text(match.group("args")),
            "executable_name": file_path.rsplit("\\", 1)[-1],
        }
    parts = command.split(None, 1)
    file_path = normalize_text(parts[0]) if parts else ""
    if not file_path:
        return None
    return {
        "file_path": file_path,
        "arguments": normalize_text(parts[1]) if len(parts) > 1 else "",
        "executable_name": file_path.rsplit("\\", 1)[-1],
    }


def option_present(arguments_text: Any, option: str) -> bool:
    option_text = normalize_text(option)
    if not option_text:
        return False
    return re.search(r"(?i)(^|\s)%s(\s|$)" % re.escape(option_text), normalize_text(arguments_text)) is not None


def _quote_windows_token(value: str) -> str:
    text = normalize_text(value)
    if not text:
        return ""
    if text.startswith('"') and text.endswith('"'):
        return text
    if any(ch.isspace() for ch in text):
        return f'"{text}"'
    return text


def build_windows_command(file_path: str, arguments: str = "", extra_args: Optional[List[str]] = None) -> str:
    command_parts = [_quote_windows_token(file_path)]
    if normalize_text(arguments):
        command_parts.append(normalize_text(arguments))
    for arg in extra_args or []:
        normalized = normalize_text(arg)
        if normalized:
            command_parts.append(_quote_windows_token(normalized))
    return " ".join(part for part in command_parts if part).strip()


def canonicalize_windows_command(command_line: Any, *, extra_args: Optional[List[str]] = None) -> str:
    parsed = split_windows_command_line(command_line)
    if not parsed:
        return normalize_text(command_line)
    return build_windows_command(parsed["file_path"], parsed["arguments"], extra_args=extra_args)


def _trim_windows_path(path: Any) -> str:
    return normalize_text(path).rstrip("\\/")


def _join_windows_path(base: Any, *parts: str) -> str:
    normalized_base = _trim_windows_path(base)
    clean_parts = [normalize_text(part).strip("\\/") for part in parts if normalize_text(part)]
    if not normalized_base:
        return "\\".join(clean_parts)
    if not clean_parts:
        return normalized_base
    return normalized_base + "\\" + "\\".join(clean_parts)


def detect_software_distribution(entry: Dict[str, Any]) -> Dict[str, str]:
    if not isinstance(entry, dict):
        return {"platform": "", "app_id": ""}
    metadata = software_metadata(entry)
    source = normalize_software_source(entry.get("source"))
    if source != "local_installed":
        return {"platform": "", "app_id": ""}
    uninstall_string = normalize_text(metadata.get("uninstall_string"))
    install_location = _trim_windows_path(metadata.get("install_location"))
    match = _STEAM_UNINSTALL_PROTOCOL_RE.search(uninstall_string)
    app_id = normalize_text(match.group("app_id")) if match else ""
    if match or _STEAM_LIBRARY_PATH_RE.search(install_location):
        return {"platform": "steam", "app_id": app_id}
    return {"platform": "", "app_id": ""}


def _unsupported(reason: str) -> Dict[str, Any]:
    return {
        "supported": False,
        "reason": normalize_text(reason),
        "summary": "",
        "strategy": "",
        "rule_id": "",
        "quiet_uninstall_string": "",
        "uninstall_string": "",
        "product_code": "",
        "package_family_name": "",
    }


def _supported(
    *,
    strategy: str,
    summary: str,
    rule_id: str = "",
    quiet_uninstall_string: str = "",
    uninstall_string: str = "",
    product_code: str = "",
    package_family_name: str = "",
) -> Dict[str, Any]:
    return {
        "supported": True,
        "reason": "",
        "summary": normalize_text(summary),
        "strategy": normalize_text(strategy),
        "rule_id": normalize_text(rule_id),
        "quiet_uninstall_string": normalize_text(quiet_uninstall_string),
        "uninstall_string": normalize_text(uninstall_string),
        "product_code": normalize_text(product_code),
        "package_family_name": normalize_text(package_family_name),
    }


def _matches_any_substring(value: str, candidates: List[str]) -> bool:
    value_lower = normalize_text(value).lower()
    return any(normalize_text(item).lower() in value_lower for item in candidates if normalize_text(item))


def _software_matches_rule(
    entry: Dict[str, Any],
    rule: Dict[str, Any],
    *,
    executable_name: str = "",
    uninstall_text: str = "",
    quiet_arguments: str = "",
) -> bool:
    metadata = software_metadata(entry)
    source = normalize_software_source(entry.get("source"))
    name = normalize_text(entry.get("name"))
    version = normalize_text(entry.get("version"))
    publisher = normalize_text(metadata.get("publisher"))
    install_location = _trim_windows_path(metadata.get("install_location"))
    product_code = normalize_text(metadata.get("product_code"))
    exe_name = normalize_text(executable_name).lower()
    uninstall_lower = normalize_text(uninstall_text).lower()

    expected_source_raw = normalize_text(rule.get("source")).lower()
    expected_source = _SOFTWARE_SOURCE_ALIASES.get(expected_source_raw, expected_source_raw)
    if expected_source and expected_source != source:
        return False

    exact_name = normalize_text(rule.get("name"))
    if exact_name and exact_name.lower() != name.lower():
        return False

    exact_version = normalize_text(rule.get("version"))
    if exact_version and exact_version.lower() != version.lower():
        return False

    exact_product_code = normalize_text(rule.get("product_code"))
    if exact_product_code and exact_product_code.upper() != product_code.upper():
        return False

    publishers = [normalize_text(item).lower() for item in rule.get("publisher_contains_any") or [] if normalize_text(item)]
    if publishers and not any(item in publisher.lower() for item in publishers):
        return False

    names = [normalize_text(item).lower() for item in rule.get("name_contains_any") or [] if normalize_text(item)]
    if names and not any(item in name.lower() for item in names):
        return False

    install_markers = [
        normalize_text(item).lower() for item in rule.get("install_location_contains_any") or [] if normalize_text(item)
    ]
    if install_markers and not any(item in install_location.lower() for item in install_markers):
        return False

    exe_names = [normalize_text(item).lower() for item in rule.get("exe_names") or [] if normalize_text(item)]
    if exe_names and exe_name not in exe_names:
        return False

    uninstall_markers = [normalize_text(item).lower() for item in rule.get("uninstall_contains_any") or [] if normalize_text(item)]
    if uninstall_markers and not any(item in uninstall_lower for item in uninstall_markers):
        return False

    quiet_args = [normalize_text(item) for item in rule.get("quiet_args_any") or [] if normalize_text(item)]
    if quiet_args and not any(option_present(quiet_arguments, item) for item in quiet_args):
        return False

    return True


def _match_windows_rule(entry: Dict[str, Any], uninstall_string: str, executable_name: str) -> Optional[Dict[str, Any]]:
    for rule in _WINDOWS_UNINSTALL_RULES:
        if _software_matches_rule(
            entry,
            rule,
            executable_name=executable_name,
            uninstall_text=uninstall_string,
        ):
            return rule
    return None


def _match_blocked_quiet_uninstall(entry: Dict[str, Any], quiet_uninstall_string: str) -> Optional[Dict[str, Any]]:
    parsed = split_windows_command_line(quiet_uninstall_string)
    executable_name = normalize_text((parsed or {}).get("executable_name")).lower()
    arguments = normalize_text((parsed or {}).get("arguments"))
    blocklist = _load_uninstall_blocklist()

    for rule in blocklist.get("windows_quiet_uninstall_blocklist") or []:
        if _software_matches_rule(
            entry,
            rule,
            executable_name=executable_name,
            uninstall_text=quiet_uninstall_string,
            quiet_arguments=arguments,
        ):
            return rule
    return None


def _match_windows_uninstall_override(entry: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    metadata = software_metadata(entry)
    quiet_uninstall_string = normalize_text(metadata.get("quiet_uninstall_string"))
    uninstall_string = normalize_text(metadata.get("uninstall_string"))
    parsed = split_windows_command_line(quiet_uninstall_string or uninstall_string)
    executable_name = normalize_text((parsed or {}).get("executable_name"))
    arguments = normalize_text((parsed or {}).get("arguments"))
    overrides = _load_uninstall_overrides()

    for rule in overrides.get("windows_uninstall_overrides") or []:
        if _software_matches_rule(
            entry,
            rule,
            executable_name=executable_name,
            uninstall_text=quiet_uninstall_string or uninstall_string,
            quiet_arguments=arguments,
        ):
            return rule
    return None


def _resolve_windows_install_location_rule(entry: Dict[str, Any], metadata: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    name = normalize_text(entry.get("name"))
    name_lower = name.lower()
    publisher = normalize_text(metadata.get("publisher")).lower()
    install_location = _trim_windows_path(metadata.get("install_location"))
    version = normalize_text(entry.get("version"))

    if install_location and "igor pavlov" in publisher and "7-zip" in name_lower:
        return _supported(
            strategy="direct_command",
            summary="Derived 7-Zip uninstall from install location.",
            rule_id="install_location_7zip",
            quiet_uninstall_string=build_windows_command(
                _join_windows_path(install_location, "Uninstall.exe"),
                "",
                extra_args=["/S"],
            ),
        )

    if install_location and "betterbird project" in publisher and "betterbird" in name_lower:
        return _supported(
            strategy="direct_command",
            summary="Derived Betterbird uninstall from install location.",
            rule_id="install_location_betterbird_helper",
            quiet_uninstall_string=build_windows_command(
                _join_windows_path(install_location, "uninstall", "helper.exe"),
                "",
                extra_args=["/S"],
            ),
        )

    if install_location and "mozilla" in publisher and "firefox" in name_lower:
        return _supported(
            strategy="direct_command",
            summary="Derived Firefox uninstall from install location.",
            rule_id="install_location_firefox_helper",
            quiet_uninstall_string=build_windows_command(
                _join_windows_path(install_location, "uninstall", "helper.exe"),
                "",
                extra_args=["/S"],
            ),
        )

    if install_location and "irfan skiljan" in publisher and "irfanview" in name_lower:
        return _supported(
            strategy="direct_command",
            summary="Derived IrfanView uninstall from install location.",
            rule_id="install_location_irfanview",
            quiet_uninstall_string=build_windows_command(
                _join_windows_path(install_location, "iv_uninstall.exe"),
                "",
                extra_args=["/silent"],
            ),
        )

    if install_location and "microsoft corporation" in publisher and version:
        if "microsoft edge webview2 runtime" in name_lower:
            return _supported(
                strategy="direct_command",
                summary="Derived WebView2 uninstall from install location and version.",
                rule_id="install_location_edge_webview_setup",
                quiet_uninstall_string=build_windows_command(
                    _join_windows_path(install_location, version, "Installer", "setup.exe"),
                    "",
                    extra_args=["--uninstall", "--msedgewebview", "--system-level", "--force-uninstall"],
                ),
            )
        if name_lower == "microsoft edge":
            return _supported(
                strategy="direct_command",
                summary="Derived Edge uninstall from install location and version.",
                rule_id="install_location_edge_setup",
                quiet_uninstall_string=build_windows_command(
                    _join_windows_path(install_location, version, "Installer", "setup.exe"),
                    "",
                    extra_args=["--uninstall", "--msedge", "--system-level", "--force-uninstall"],
                ),
            )

    return None


def resolve_windows_uninstall_plan(entry: Dict[str, Any]) -> Dict[str, Any]:
    metadata = software_metadata(entry)
    source = normalize_software_source(entry.get("source"))
    distribution = detect_software_distribution(entry)

    if source == "windows_store":
        package_family_name = normalize_text(metadata.get("package_family_name"))
        non_removable = coerce_optional_bool(metadata.get("non_removable"))
        if non_removable is True:
            return _unsupported("Windows marks this Store package as non-removable.")
        if non_removable is False and package_family_name:
            return _supported(
                strategy="windows_store",
                summary="Windows Store package uninstall.",
                rule_id="metadata_windows_store",
                package_family_name=package_family_name,
            )
        if package_family_name:
            return _supported(
                strategy="windows_store",
                summary="Windows Store package uninstall.",
                rule_id="metadata_windows_store_family_name",
                package_family_name=package_family_name,
            )
        if not package_family_name:
            return _unsupported("This Windows Store entry does not include enough package metadata yet.")

    if source != "local_installed":
        return _unsupported("This software source is not part of the first Windows uninstall release.")

    if distribution.get("platform") == "steam":
        return _unsupported("Steam manages this title, and Borealis does not yet have a verified unattended uninstall path.")

    quiet_uninstall_string = normalize_text(metadata.get("quiet_uninstall_string"))
    uninstall_string = normalize_text(metadata.get("uninstall_string"))
    product_code = normalize_text(metadata.get("product_code"))
    uninstall_override = _match_windows_uninstall_override(entry)

    if uninstall_override is not None:
        strategy = normalize_text(uninstall_override.get("strategy")).lower() or "direct_command"
        rule_id = normalize_text(uninstall_override.get("rule_id"))
        summary = normalize_text(uninstall_override.get("summary")) or "Uses a custom uninstall override."
        override_quiet_string = normalize_text(uninstall_override.get("quiet_uninstall_string"))
        override_uninstall_string = normalize_text(uninstall_override.get("uninstall_string")) or uninstall_string
        override_product_code = normalize_text(uninstall_override.get("product_code"))
        override_package_family_name = normalize_text(uninstall_override.get("package_family_name"))

        if strategy == "direct_command" and override_quiet_string:
            return _supported(
                strategy="direct_command",
                summary=summary,
                rule_id=rule_id,
                quiet_uninstall_string=canonicalize_windows_command(override_quiet_string),
                uninstall_string=override_uninstall_string,
                product_code=override_product_code,
            )
        if strategy == "msi_product_code" and _WINDOWS_PRODUCT_CODE_RE.match(override_product_code):
            return _supported(
                strategy="msi_product_code",
                summary=summary,
                rule_id=rule_id,
                uninstall_string=override_uninstall_string,
                product_code=override_product_code.upper(),
            )
        if strategy == "windows_store" and override_package_family_name:
            return _supported(
                strategy="windows_store",
                summary=summary,
                rule_id=rule_id,
                package_family_name=override_package_family_name,
            )

    if quiet_uninstall_string:
        blocked_quiet_rule = _match_blocked_quiet_uninstall(entry, quiet_uninstall_string)
        if blocked_quiet_rule is not None:
            return _unsupported(normalize_text(blocked_quiet_rule.get("reason")))
        return _supported(
            strategy="direct_command",
            summary="Uses the registry quiet uninstall string.",
            rule_id="metadata_quiet_uninstall_string",
            quiet_uninstall_string=canonicalize_windows_command(quiet_uninstall_string),
            uninstall_string=uninstall_string,
            product_code=product_code if _WINDOWS_PRODUCT_CODE_RE.match(product_code) else "",
        )

    if product_code and _WINDOWS_PRODUCT_CODE_RE.match(product_code):
        return _supported(
            strategy="msi_product_code",
            summary="Uses the MSI product code from inventory.",
            rule_id="metadata_product_code",
            uninstall_string=uninstall_string,
            product_code=product_code.upper(),
        )

    guid_in_uninstall = find_msi_product_code(uninstall_string)
    if guid_in_uninstall:
        return _supported(
            strategy="msi_product_code",
            summary="Derived MSI uninstall from the registry uninstall string.",
            rule_id="metadata_msi_guid",
            uninstall_string=uninstall_string,
            product_code=guid_in_uninstall,
        )

    parsed_uninstall = split_windows_command_line(uninstall_string)
    executable_name = normalize_text((parsed_uninstall or {}).get("executable_name")).lower()
    existing_arguments = normalize_text((parsed_uninstall or {}).get("arguments"))

    if uninstall_string and command_has_quiet_switch(uninstall_string):
        return _supported(
            strategy="direct_command",
            summary="The registry uninstall string already includes quiet flags.",
            rule_id="metadata_quiet_flags",
            quiet_uninstall_string=canonicalize_windows_command(uninstall_string),
            uninstall_string=uninstall_string,
        )

    if parsed_uninstall and executable_name.startswith("unins"):
        return _supported(
            strategy="direct_command",
            summary="Derived Inno Setup silent uninstall.",
            rule_id="builtin_inno_uninstall",
            quiet_uninstall_string=build_windows_command(
                parsed_uninstall["file_path"],
                existing_arguments,
                extra_args=["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
            ),
            uninstall_string=uninstall_string,
        )

    if parsed_uninstall and executable_name == "update.exe":
        extra_args: List[str] = []
        if not option_present(existing_arguments, "--uninstall"):
            extra_args.append("--uninstall")
        if not option_present(existing_arguments, "--silent") and not option_present(existing_arguments, "--quiet"):
            extra_args.append("--silent")
        return _supported(
            strategy="direct_command",
            summary="Derived Squirrel-style silent uninstall.",
            rule_id="builtin_squirrel_update",
            quiet_uninstall_string=build_windows_command(
                parsed_uninstall["file_path"],
                existing_arguments,
                extra_args=extra_args,
            ),
            uninstall_string=uninstall_string,
        )

    rule = _match_windows_rule(entry, uninstall_string, executable_name)
    if rule and parsed_uninstall:
        extra_args = [normalize_text(item) for item in rule.get("append_args") or [] if normalize_text(item)]
        missing_args = [item for item in extra_args if not option_present(existing_arguments, item)]
        return _supported(
            strategy="direct_command",
            summary=normalize_text(rule.get("summary")) or "Derived uninstall command from the Borealis rule catalog.",
            rule_id=normalize_text(rule.get("rule_id")),
            quiet_uninstall_string=build_windows_command(
                parsed_uninstall["file_path"],
                existing_arguments,
                extra_args=missing_args,
            ),
            uninstall_string=uninstall_string,
        )

    install_location_rule = _resolve_windows_install_location_rule(entry, metadata)
    if install_location_rule is not None:
        return install_location_rule

    if uninstall_string:
        return _unsupported("Borealis could not derive a silent uninstall command for this software yet.")
    return _unsupported("This software row does not expose a usable uninstall command yet.")


def resolve_software_uninstall_capability(entry: Dict[str, Any], operating_system: Any) -> Dict[str, Any]:
    os_text = normalize_text(operating_system).lower()
    if not os_text:
        return _unsupported("Borealis has not received the device platform yet.")
    if "windows" not in os_text:
        return _unsupported("Windows uninstall support ships first. Linux support comes next.")
    return resolve_windows_uninstall_plan(entry)


def enrich_software_entry_with_uninstall(entry: Dict[str, Any], operating_system: Any) -> Dict[str, Any]:
    if not isinstance(entry, dict):
        return {}
    enriched = {
        "name": normalize_text(entry.get("name")),
        "version": normalize_text(entry.get("version")),
        "source": normalize_software_source(entry.get("source")),
        "metadata": dict(software_metadata(entry)),
    }
    distribution = detect_software_distribution(entry)
    if distribution.get("platform"):
        enriched["distribution_platform"] = distribution["platform"]
    if distribution.get("app_id"):
        enriched["distribution_app_id"] = distribution["app_id"]
    enriched["uninstall"] = resolve_software_uninstall_capability(enriched, operating_system)
    return enriched


def enrich_software_inventory_with_uninstall(rows: List[Dict[str, Any]], operating_system: Any) -> List[Dict[str, Any]]:
    return [enrich_software_entry_with_uninstall(row, operating_system) for row in rows if isinstance(row, dict)]


def find_software_entry(software_raw: Any, *, name: str, version: str, source: str) -> Optional[Dict[str, Any]]:
    normalized_name = normalize_text(name).lower()
    normalized_version = normalize_text(version)
    normalized_source = normalize_software_source(source)
    if not normalized_name:
        return None

    software_payload = software_raw
    if isinstance(software_payload, str):
        try:
            software_payload = json.loads(software_payload)
        except Exception:
            software_payload = []

    rows = normalize_software_inventory(software_payload)
    exact_match: Optional[Dict[str, Any]] = None
    fallback_matches: List[Dict[str, Any]] = []
    for row in rows:
        row_name = normalize_text(row.get("name")).lower()
        row_source = normalize_software_source(row.get("source"))
        if row_name != normalized_name or row_source != normalized_source:
            continue
        fallback_matches.append(row)
        if normalized_version and normalize_text(row.get("version")) == normalized_version:
            exact_match = row
            break

    if exact_match is not None:
        return exact_match
    if len(fallback_matches) == 1:
        return fallback_matches[0]
    return None
