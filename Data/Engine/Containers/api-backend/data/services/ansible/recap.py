"""Helpers for parsing Ansible play recap output."""

from __future__ import annotations

import re
from typing import Any, Dict, Mapping


_RECAP_COUNTER_KEYS = (
    "ok",
    "changed",
    "unreachable",
    "failed",
    "skipped",
    "rescued",
    "ignored",
)
_RECAP_COUNTER_PATTERN = re.compile(
    r"(?P<key>ok|changed|unreachable|failed|skipped|rescued|ignored)\s*=\s*(?P<value>\d+)",
    re.IGNORECASE,
)


def _coerce_counter(value: Any) -> int:
    try:
        return max(0, int(value))
    except Exception:
        return 0


def _normalize_host_key(value: Any) -> str:
    return str(value or "").strip().lower()


def normalize_host_recap_map(raw_value: Any) -> Dict[str, Dict[str, int]]:
    if not isinstance(raw_value, Mapping):
        return {}
    normalized: Dict[str, Dict[str, int]] = {}
    for raw_host, raw_stats in raw_value.items():
        host_key = _normalize_host_key(raw_host)
        if not host_key or not isinstance(raw_stats, Mapping):
            continue
        normalized[host_key] = {
            key: _coerce_counter(raw_stats.get(key))
            for key in _RECAP_COUNTER_KEYS
        }
    return normalized


def parse_play_recap(raw_text: Any) -> Dict[str, Dict[str, int]]:
    text = str(raw_text or "").replace("\r\n", "\n").replace("\r", "\n")
    if not text.strip():
        return {}

    recap_by_host: Dict[str, Dict[str, int]] = {}
    for raw_line in text.splitlines():
        if ":" not in raw_line or "=" not in raw_line:
            continue
        host_part, stats_part = raw_line.split(":", 1)
        host_key = _normalize_host_key(host_part)
        if not host_key or "ok=" not in stats_part:
            continue
        matches = list(_RECAP_COUNTER_PATTERN.finditer(stats_part))
        if not matches:
            continue
        stats = {key: 0 for key in _RECAP_COUNTER_KEYS}
        for match in matches:
            stats[str(match.group("key") or "").strip().lower()] = _coerce_counter(match.group("value"))
        recap_by_host[host_key] = stats
    return recap_by_host


def extract_host_recap_map(
    *,
    recap_payload: Any = None,
    recap_text: Any = None,
) -> Dict[str, Dict[str, int]]:
    if isinstance(recap_payload, Mapping):
        parsed = normalize_host_recap_map(recap_payload.get("host_recap"))
        if parsed:
            return parsed
    return parse_play_recap(recap_text)


def host_recap_status(raw_stats: Any) -> str:
    stats = normalize_host_recap_map({"host": raw_stats}).get("host") or {}
    if _coerce_counter(stats.get("unreachable")) > 0:
        return "Failed"
    if _coerce_counter(stats.get("failed")) > 0:
        return "Failed"
    return "Success"
