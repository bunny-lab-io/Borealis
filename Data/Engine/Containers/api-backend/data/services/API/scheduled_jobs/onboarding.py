# ======================================================
# Data\Engine\services\API\scheduled_jobs\onboarding.py
# Description: Automatic device onboarding target parsing and output hygiene.
#
# API Endpoints (if applicable): None
# ======================================================

"""Helpers for scheduler-backed automatic device onboarding."""

from __future__ import annotations

import ipaddress
import re
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple


DEFAULT_ONBOARDING_SSH_PORT = 22
DEFAULT_ONBOARDING_WINDOWS_PORT = 445
DEFAULT_ONBOARDING_WINRM_PORT = 5985
DEFAULT_ONBOARDING_TARGET_CAP = 512
DEFAULT_ONBOARDING_CONCURRENCY = 5
ONBOARDING_SNIPPET_LIMIT = 1600


_FQDN_RE = re.compile(
    r"^(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*"
    r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$"
)


def _split_scope_text(value: Any) -> List[str]:
    if value is None:
        return []
    if isinstance(value, str):
        raw = value
    else:
        raw = str(value)
    entries: List[str] = []
    for line in re.split(r"[\n\r]+", raw):
        line_without_comment = str(line or "").split("#", 1)[0]
        for piece in re.split(r"[,;]+", line_without_comment):
            token = piece.strip()
            if token:
                entries.append(token)
    return entries


def coerce_scope_entries(value: Any) -> List[str]:
    """Return user-entered scope tokens from list/dict/string payloads."""
    if isinstance(value, dict):
        for key in ("entries", "scope", "targets", "discovery_scope", "discoveryScope"):
            if key in value:
                return coerce_scope_entries(value.get(key))
        return []
    if isinstance(value, (list, tuple, set)):
        entries: List[str] = []
        for item in value:
            if isinstance(item, dict):
                raw = item.get("input") or item.get("target") or item.get("host") or item.get("hostname")
                entries.extend(_split_scope_text(raw))
            else:
                entries.extend(_split_scope_text(item))
        return entries
    return _split_scope_text(value)


def _normalize_port(value: Any, default_port: int = DEFAULT_ONBOARDING_SSH_PORT) -> int:
    try:
        parsed = int(str(value).strip())
        if 1 <= parsed <= 65535:
            return parsed
    except Exception:
        pass
    return int(default_port or DEFAULT_ONBOARDING_SSH_PORT)


def _split_host_port(token: str, default_port: int) -> Tuple[str, int]:
    candidate = str(token or "").strip()
    port = _normalize_port(default_port)
    if candidate.lower().startswith("ssh://"):
        candidate = candidate[6:]
    if candidate.startswith("["):
        return candidate, port
    if candidate.count(":") == 1:
        host_part, port_part = candidate.rsplit(":", 1)
        if host_part and port_part.isdigit():
            parsed_port = _normalize_port(port_part, port)
            if parsed_port != port or 1 <= int(port_part) <= 65535:
                return host_part.strip(), parsed_port
    return candidate, port


def _target_record(source: str, host: str, port: int, kind: str) -> Dict[str, Any]:
    return {
        "input": str(source or "").strip(),
        "target": str(host or "").strip(),
        "host": str(host or "").strip(),
        "port": int(port),
        "kind": kind,
    }


def _is_ipv4(value: str) -> bool:
    try:
        return isinstance(ipaddress.ip_address(value), ipaddress.IPv4Address)
    except Exception:
        return False


def _expand_ipv4_range(source: str, default_port: int, max_remaining: int) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    host_token, port = _split_host_port(source, default_port)
    if "-" not in host_token:
        return [], None
    start_raw, end_raw = [part.strip() for part in host_token.split("-", 1)]
    try:
        start_ip = ipaddress.IPv4Address(start_raw)
        if _is_ipv4(end_raw):
            end_ip = ipaddress.IPv4Address(end_raw)
        else:
            prefix, _last = start_raw.rsplit(".", 1)
            end_ip = ipaddress.IPv4Address(f"{prefix}.{end_raw}")
    except Exception:
        return [], f"invalid_ipv4_range:{source}"
    if int(end_ip) < int(start_ip):
        return [], f"invalid_ipv4_range:{source}"
    total = int(end_ip) - int(start_ip) + 1
    if total > max_remaining:
        return [], f"target_cap_exceeded:{source}"
    return [
        _target_record(source, str(ipaddress.IPv4Address(int(start_ip) + offset)), port, "ipv4")
        for offset in range(total)
    ], None


def _expand_token(source: str, default_port: int, max_remaining: int) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    token = str(source or "").strip()
    if not token:
        return [], None

    if "-" in token:
        range_records, range_error = _expand_ipv4_range(token, default_port, max_remaining)
        if range_records or range_error:
            return range_records, range_error

    host_token, port = _split_host_port(token, default_port)
    if "/" in host_token:
        try:
            network = ipaddress.ip_network(host_token, strict=False)
        except Exception:
            return [], f"invalid_cidr:{source}"
        if not isinstance(network, ipaddress.IPv4Network):
            return [], f"unsupported_network_family:{source}"
        hosts = [str(host) for host in network.hosts()]
        if not hosts and network.prefixlen == 32:
            hosts = [str(network.network_address)]
        if len(hosts) > max_remaining:
            return [], f"target_cap_exceeded:{source}"
        return [_target_record(source, host, port, "ipv4") for host in hosts], None

    try:
        parsed_ip = ipaddress.ip_address(host_token)
        if not isinstance(parsed_ip, ipaddress.IPv4Address):
            return [], f"unsupported_network_family:{source}"
        return [_target_record(source, str(parsed_ip), port, "ipv4")], None
    except Exception:
        pass

    if _FQDN_RE.match(host_token):
        return [_target_record(source, host_token.lower(), port, "fqdn")], None
    return [], f"invalid_target:{source}"


def parse_onboarding_scope(
    entries: Any,
    *,
    default_port: int = DEFAULT_ONBOARDING_SSH_PORT,
    max_targets: int = DEFAULT_ONBOARDING_TARGET_CAP,
) -> Tuple[List[Dict[str, Any]], List[str]]:
    """Expand onboarding scope entries into deduped target records."""
    normalized_port = _normalize_port(default_port)
    cap = max(1, int(max_targets or DEFAULT_ONBOARDING_TARGET_CAP))
    targets: List[Dict[str, Any]] = []
    errors: List[str] = []
    seen: set[str] = set()
    for token in coerce_scope_entries(entries):
        remaining = cap - len(targets)
        if remaining <= 0:
            errors.append("target_cap_exceeded")
            break
        expanded, error = _expand_token(token, normalized_port, remaining)
        if error:
            errors.append(error)
            continue
        for record in expanded:
            key = f"{str(record.get('host') or '').lower()}:{int(record.get('port') or normalized_port)}"
            if key in seen:
                continue
            seen.add(key)
            targets.append(record)
    return targets, errors


def sanitize_output(value: Any, *, redactions: Optional[Iterable[Any]] = None, limit: int = ONBOARDING_SNIPPET_LIMIT) -> str:
    text = str(value or "")
    if not text:
        return ""
    for raw_secret in redactions or []:
        secret = str(raw_secret or "")
        if len(secret) >= 4:
            text = text.replace(secret, "[redacted]")
    text = text.replace("\r", "\n")
    text = re.sub(r"\n{4,}", "\n\n\n", text)
    text = re.sub(r"[^\S\n]+", " ", text).strip()
    max_len = max(120, int(limit or ONBOARDING_SNIPPET_LIMIT))
    if len(text) > max_len:
        return f"{text[:max_len]}..."
    return text


__all__ = [
    "DEFAULT_ONBOARDING_CONCURRENCY",
    "DEFAULT_ONBOARDING_SSH_PORT",
    "DEFAULT_ONBOARDING_WINDOWS_PORT",
    "DEFAULT_ONBOARDING_WINRM_PORT",
    "DEFAULT_ONBOARDING_TARGET_CAP",
    "ONBOARDING_SNIPPET_LIMIT",
    "coerce_scope_entries",
    "parse_onboarding_scope",
    "sanitize_output",
]
