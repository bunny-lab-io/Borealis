# ======================================================
# Data\Engine\services\API\devices\runtime_override_merge.py
# Description: Merge staged and runtime software override JSON files during Engine restaging.
#
# API Endpoints (if applicable):
# - None.
# ======================================================

"""Merge staged and runtime software override payloads for Engine restaging."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any, Dict, List


_KIND_TO_TOP_LEVEL_KEY = {
    "software_icons": "windows_icon_overrides",
    "software_uninstall_overrides": "windows_uninstall_overrides",
    "software_uninstall_blocklist": "windows_quiet_uninstall_blocklist",
}


def normalize_text(value: Any) -> str:
    try:
        return str(value or "").strip()
    except Exception:
        return ""


def _extract_rule_name_keys(rule: Any) -> List[str]:
    if not isinstance(rule, dict):
        return []
    keys: List[str] = []
    exact_name = normalize_text(rule.get("name")).lower()
    if exact_name:
        keys.append(exact_name)
    raw_name_contains_any = rule.get("name_contains_any")
    if isinstance(raw_name_contains_any, list):
        for item in raw_name_contains_any:
            name_key = normalize_text(item).lower()
            if name_key and name_key not in keys:
                keys.append(name_key)
    return keys


def _load_payload(path: Path | str) -> Dict[str, Any]:
    payload_path = Path(path)
    if not payload_path.is_file():
        return {}
    with payload_path.open("r", encoding="utf-8") as handle:
        payload = json.load(handle)
    return payload if isinstance(payload, dict) else {}


def merge_override_payloads(
    source_payload: Dict[str, Any] | None,
    runtime_payload: Dict[str, Any] | None,
    *,
    kind: str,
) -> Dict[str, Any]:
    if kind not in _KIND_TO_TOP_LEVEL_KEY:
        raise ValueError(f"Unsupported override merge kind '{kind}'")

    top_level_key = _KIND_TO_TOP_LEVEL_KEY[kind]
    staged_payload = dict(source_payload or {}) if isinstance(source_payload, dict) else {}
    live_payload = dict(runtime_payload or {}) if isinstance(runtime_payload, dict) else {}

    merged_payload: Dict[str, Any] = dict(live_payload)
    merged_payload.update(staged_payload)

    merged_rows: List[Dict[str, Any]] = []
    merged_keys: List[set[str]] = []

    def overlay_rows(rows: Any) -> None:
        iterable = rows if isinstance(rows, list) else []
        for raw_rule in iterable:
            if not isinstance(raw_rule, dict):
                continue
            rule = dict(raw_rule)
            name_keys = set(_extract_rule_name_keys(rule))
            if not name_keys:
                merged_rows.append(rule)
                merged_keys.append(set())
                continue

            overlapping_indexes = [
                index for index, existing_keys in enumerate(merged_keys) if existing_keys and existing_keys.intersection(name_keys)
            ]
            if not overlapping_indexes:
                merged_rows.append(rule)
                merged_keys.append(set(name_keys))
                continue

            primary_index = overlapping_indexes[0]
            merged_rows[primary_index] = rule
            merged_keys[primary_index] = set(name_keys)
            for removed_count, index in enumerate(overlapping_indexes[1:]):
                adjusted_index = index - removed_count
                del merged_rows[adjusted_index]
                del merged_keys[adjusted_index]

    overlay_rows(staged_payload.get(top_level_key))
    overlay_rows(live_payload.get(top_level_key))

    merged_payload[top_level_key] = merged_rows
    return merged_payload


def merge_override_files(
    *,
    source_path: Path | str,
    runtime_path: Path | str,
    output_path: Path | str,
    kind: str,
) -> Dict[str, Any]:
    merged_payload = merge_override_payloads(
        _load_payload(source_path),
        _load_payload(runtime_path),
        kind=kind,
    )
    destination = Path(output_path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(merged_payload, indent=2) + "\n", encoding="utf-8")
    return merged_payload


def _build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Merge staged and runtime software override files.")
    parser.add_argument("--kind", required=True, choices=sorted(_KIND_TO_TOP_LEVEL_KEY))
    parser.add_argument("--source", required=True, help="Path to the staged/source JSON file.")
    parser.add_argument("--runtime", required=True, help="Path to the preserved runtime JSON file.")
    parser.add_argument("--output", required=True, help="Path to write the merged JSON file.")
    return parser


def main(argv: List[str] | None = None) -> int:
    parser = _build_arg_parser()
    args = parser.parse_args(argv)
    merge_override_files(
        source_path=args.source,
        runtime_path=args.runtime,
        output_path=args.output,
        kind=args.kind,
    )
    return 0


if __name__ == "__main__":  # pragma: no cover - CLI entrypoint
    raise SystemExit(main())
