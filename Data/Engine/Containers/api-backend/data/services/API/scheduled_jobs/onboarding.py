# ======================================================
# Data\Engine\services\API\scheduled_jobs\onboarding.py
# Description: Automatic device onboarding parsing, execution, and API helpers.
#
# API Endpoints (if applicable): None
# ======================================================

"""Helpers for scheduler-backed automatic device onboarding."""

from __future__ import annotations

import base64
import hashlib
import html
import io
import ipaddress
import json
import math
import multiprocessing
import os
import queue as queue_module
import re
import shlex
import shutil
import socket
import tempfile
import threading
import time
import uuid
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from ...ansible.ssh_auth import apply_ssh_credential_host_vars
from ...aegis_cipher import AegisDataCorruptionError, AegisLockedError, AegisSecretResetRequiredError


RUN_STATUS_PENDING = "Pending"
RUN_STATUS_RUNNING = "Running"
RUN_STATUS_SUCCESS = "Success"
RUN_STATUS_WARNING = "Warning"
RUN_STATUS_FAILED = "Failed"
RUN_STATUS_SKIPPED = "Skipped"
SKIP_REASON_NO_TARGETS = "no_devices_targeted"

_SSH_BANNER_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_BANNER_TIMEOUT_SECONDS"
_SSH_SESSION_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS"
_DEFAULT_SHARED_ANSIBLE_SSH_BANNER_TIMEOUT_SECONDS = 20.0
_DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS = 20.0

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
        head_len = max(60, max_len // 2)
        tail_len = max(60, max_len - head_len)
        return f"{text[:head_len]}...\n[truncated]\n...{text[-tail_len:]}"
    return text


JOB_KIND_ONBOARDING = "onboarding"
ONBOARDING_COMPONENT_KIND = "device_onboarding"
AGENT_EXE_NAME = "Agent.exe"
DEFAULT_BOREALIS_REPO_GIT_URL = "https://github.com/bunny-lab-io/Borealis.git"
ONBOARDING_COMPONENT_NAME = "Device Onboarding"
ONBOARDING_STATUS_PENDING = "pending"
ONBOARDING_STATUS_RUNNING = "running"
ONBOARDING_STATUS_WAITING_APPROVAL = "waiting_approval"
ONBOARDING_STATUS_SKIPPED = "skipped"
ONBOARDING_STATUS_FAILED = "failed"
ONBOARDING_STATUS_UNSUPPORTED_OS = "unsupported_os"
ONBOARDING_STATUS_UNREACHABLE = "unreachable"
ONBOARDING_PLATFORM_AUTO = "auto"
ONBOARDING_PLATFORM_LINUX = "linux"
ONBOARDING_PLATFORM_WINDOWS = "windows"
ONBOARDING_WINDOWS_METHOD_SMB_SCM = "smb_scm"
ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK = "scheduled_task"
ONBOARDING_WINDOWS_METHOD_WMI_DCOM = "wmi_dcom"
ONBOARDING_WINDOWS_METHOD_WINRM = "winrm"
ONBOARDING_WINDOWS_METHODS = (
    ONBOARDING_WINDOWS_METHOD_SMB_SCM,
    ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK,
    ONBOARDING_WINDOWS_METHOD_WMI_DCOM,
    ONBOARDING_WINDOWS_METHOD_WINRM,
)


def _onboarding_failure_hint(*, stdout: Any = "", stderr: Any = "", redactions: Optional[Sequence[Any]] = None) -> str:
    clean = sanitize_output(
        "\n".join(str(part or "") for part in (stderr, stdout) if str(part or "")),
        redactions=redactions,
        limit=5000,
    )
    if not clean:
        return ""
    lines = [line.strip() for line in clean.splitlines() if line.strip()]
    if not lines:
        return ""
    ignore_prefixes = (
        "warning: permanently added",
        "from https://github.com/",
        "* branch ",
        "* [new branch]",
        "head is now at ",
        "switched to ",
    )
    candidates = [line for line in lines if not line.lower().startswith(ignore_prefixes)]
    if not candidates:
        candidates = lines
    important = [
        line
        for line in candidates
        if "ERROR:" in line
        or "/dev/tty" in line
        or "failed" in line.lower()
        or "required" in line.lower()
        or "refusing" in line.lower()
        or "permission denied" in line.lower()
        or "access denied" in line.lower()
    ]
    return sanitize_output((important or candidates)[-1], redactions=redactions, limit=240)


def _windows_onboarding_auth_failure(*, stdout: Any = "", stderr: Any = "") -> bool:
    combined = f"{stdout or ''}\n{stderr or ''}".lower()
    auth_tokens = (
        "status_logon_failure",
        "0xc000006d",
        "logon failure",
        "bad username",
        "authentication information",
        "access is denied",
        "rpc_s_access_denied",
        "unauthorized",
        "401",
        "401 unauthorized",
        "credentials were rejected",
        "specified credentials were rejected",
        "server rejected the specified credentials",
        "invalid credentials",
        "invalid password",
        "authorization failed",
        "authentication failed",
        "smb login:",
        "winrm login:",
    )
    return any(token in combined for token in auth_tokens)


def _powershell_single_quoted(value: Any) -> str:
    return "'" + str(value or "").replace("'", "''") + "'"


def _windows_method_label(method: Any) -> str:
    normalized = str(method or "").strip().lower()
    if normalized == ONBOARDING_WINDOWS_METHOD_SMB_SCM:
        return "SMB service"
    if normalized == ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK:
        return "scheduled task"
    if normalized == ONBOARDING_WINDOWS_METHOD_WMI_DCOM:
        return "WMI/DCOM"
    if normalized == ONBOARDING_WINDOWS_METHOD_WINRM:
        return "WinRM"
    return normalized.replace("_", " ") or "Windows remote install"


def _windows_onboarding_methods_with_required_fallbacks(methods: Sequence[str]) -> List[str]:
    normalized = [method for method in methods if method in ONBOARDING_WINDOWS_METHODS]
    if (
        ONBOARDING_WINDOWS_METHOD_WMI_DCOM not in normalized
        and ONBOARDING_WINDOWS_METHOD_SMB_SCM in normalized
        and ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK in normalized
        and ONBOARDING_WINDOWS_METHOD_WINRM in normalized
    ):
        insert_at = normalized.index(ONBOARDING_WINDOWS_METHOD_WINRM)
        normalized.insert(insert_at, ONBOARDING_WINDOWS_METHOD_WMI_DCOM)
    return normalized or list(ONBOARDING_WINDOWS_METHODS)


def _windows_onboarding_skip_detail(*, stdout: Any = "", stderr: Any = "") -> str:
    combined = f"{stdout or ''}\n{stderr or ''}"
    if "__BOREALIS_ONBOARDING_ALREADY_RUNNING__=1" in combined:
        return "Another Borealis onboarding deployment is already running on this target."
    if "__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1" in combined:
        return "Existing Borealis Agent is already enrolled and active."
    if "__BOREALIS_ONBOARDING_AGENT_REPAIRED__=1" in combined:
        return "Existing Borealis Agent repaired and started."
    if "__BOREALIS_ONBOARDING_ALREADY_PENDING__=1" in combined:
        if re.search(r"__BOREALIS_ONBOARDING_ALREADY_PENDING__=1[^\r\n]*status=running\b", combined, re.IGNORECASE):
            return ""
        return "Previous Borealis onboarding attempt is already pending approval on this target."
    return ""


def _windows_onboarding_existing_task_running_without_redeploy(*, stdout: Any = "", stderr: Any = "") -> bool:
    combined = f"{stdout or ''}\n{stderr or ''}"
    if "__BOREALIS_ONBOARDING_REDEPLOY_REQUIRED__=1" in combined:
        return False
    return re.search(r"__BOREALIS_AGENT_TASK_STATE__\s*=\s*Running\b", combined, re.IGNORECASE) is not None


def _onboarding_target_without_port(value: Any) -> str:
    text = str(value or "").strip().lower()
    if text.count(":") == 1:
        host, port = text.rsplit(":", 1)
        if host and port.isdigit():
            return host.strip()
    return text


def _windows_onboarding_repair_succeeded(*, stdout: Any = "", stderr: Any = "") -> bool:
    combined = f"{stdout or ''}\n{stderr or ''}"
    return "__BOREALIS_ONBOARDING_AGENT_REPAIRED__=1" in combined


def _windows_onboarding_local_bootstrap_completed(*, stdout: Any = "", stderr: Any = "", detail: Any = "") -> bool:
    combined = f"{stdout or ''}\n{stderr or ''}\n{detail or ''}".lower()
    return any(
        marker in combined
        for marker in (
            "__borealis_windows_onboarding_exit_code__=0",
            "windows_onboarding_approval_callback_timeout",
            "agent completed local bootstrap",
            "agent installed through windows",
        )
    )


def _onboarding_raw_input_map(entries: Any, *, default_port: int) -> Dict[str, str]:
    if isinstance(entries, str):
        raw_entries = [line for line in re.split(r"[\r\n]+", entries) if str(line or "").strip()]
    elif isinstance(entries, (list, tuple, set)):
        raw_entries = [str(entry or "") for entry in entries if str(entry or "").strip()]
    else:
        raw_entries = [str(entries or "")] if str(entries or "").strip() else []
    mapped: Dict[str, str] = {}
    for raw_entry in raw_entries:
        raw_text = str(raw_entry or "").strip()
        if not raw_text:
            continue
        expanded, _errors = parse_onboarding_scope(
            [raw_text],
            default_port=int(default_port or DEFAULT_ONBOARDING_SSH_PORT),
            max_targets=DEFAULT_ONBOARDING_TARGET_CAP,
        )
        for target in expanded:
            key = f"{str(target.get('host') or '').strip().lower()}:{int(target.get('port') or default_port or DEFAULT_ONBOARDING_SSH_PORT)}"
            mapped.setdefault(key, raw_text)
    return mapped


def _onboarding_approval_lookup_candidates(row: Mapping[str, Any]) -> List[str]:
    candidates: List[str] = []

    def _add(value: Any) -> None:
        text = str(value or "").strip()
        if not text:
            return
        if text not in candidates:
            candidates.append(text)

    _add(row.get("target_address"))
    _add(row.get("target_hostname"))
    target_input = str(row.get("target_input") or "").strip()
    _add(target_input)
    if "#" in target_input:
        comment = target_input.split("#", 1)[1].strip()
        _add(comment)
        first_comment_token = comment.split()[0].strip() if comment.split() else ""
        _add(first_comment_token)
    return candidates


def _windows_service_start_error_allows_output_poll(error: Any) -> bool:
    normalized = str(error or "").strip().lower()
    if not normalized:
        return False
    return any(
        marker in normalized
        for marker in (
            "1053",
            "timely fashion",
            "netbios connection",
            "timed out",
            "timeout",
            "connection reset",
            "connection aborted",
            "rpc_x_call_failed",
            "error_service_not_active",
            "error_service_specific_error",
            "service process could not connect",
        )
    )


def _windows_onboarding_result_may_have_created_approval(*, method: Any, stdout: Any = "", stderr: Any = "") -> bool:
    combined = f"{stdout or ''}\n{stderr or ''}".lower()
    normalized_method = str(method or "").strip().lower()
    if normalized_method == ONBOARDING_WINDOWS_METHOD_SMB_SCM:
        return "service start:" in combined and any(marker in combined for marker in ("timed out", "netbios", "rpc_x_call_failed"))
    if normalized_method == ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK:
        return "task start:" in combined or "task output polling" in combined
    if normalized_method == ONBOARDING_WINDOWS_METHOD_WMI_DCOM:
        return "wmi process creation:" in combined or "wmi output polling" in combined
    return False


def _parse_windows_credential_parts(credential: Mapping[str, Any]) -> Tuple[str, str, str]:
    username = str(credential.get("username") or "").strip()
    metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
    domain = str(metadata.get("domain") or metadata.get("windows_domain") or "").strip()
    if "\\" in username:
        domain_part, user_part = username.split("\\", 1)
        return domain_part.strip(), user_part.strip(), str(credential.get("password") or "")
    if "@" in username and not domain:
        user_part, domain_part = username.split("@", 1)
        return domain_part.strip(), user_part.strip(), str(credential.get("password") or "")
    return domain, username, str(credential.get("password") or "")


def _windows_smb_remote_names(host: str, credential: Mapping[str, Any]) -> List[str]:
    metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
    candidates = [
        metadata.get("netbios_name"),
        metadata.get("windows_netbios_name"),
        metadata.get("smb_remote_name"),
        "*SMBSERVER",
        host,
    ]
    names: List[str] = []
    for candidate in candidates:
        value = str(candidate or "").strip()
        if value and value not in names:
            names.append(value)
    return names or [str(host or "").strip()]


def _windows_exit_code_from_output(output: Any) -> Optional[int]:
    text = str(output or "")
    matches = re.findall(r"__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__\s*=\s*(-?\d+)", text)
    if not matches:
        return None
    try:
        return int(matches[-1])
    except Exception:
        return None


def _clean_onboarding_reported_hostname(value: Any) -> str:
    text = str(value or "").strip().strip("[]")
    if not text:
        return ""
    text = re.split(r"\s+", text, maxsplit=1)[0].strip().strip("[]")
    if not text or len(text) > 255:
        return ""
    if re.fullmatch(r"\d{1,3}(?:\.\d{1,3}){3}", text):
        return ""
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,254}", text):
        return ""
    return text


def _windows_onboarding_reported_hostname(
    *,
    stdout: Any = "",
    stderr: Any = "",
    state: Optional[Mapping[str, Any]] = None,
    events: Optional[Sequence[Mapping[str, Any]]] = None,
) -> str:
    candidates: List[Any] = []
    if isinstance(state, Mapping):
        candidates.extend([state.get("hostname"), state.get("computer_name"), state.get("device_hostname")])
    for event in reversed(list(events or [])):
        if isinstance(event, Mapping):
            candidates.extend([event.get("hostname"), event.get("computer_name"), event.get("device_hostname")])
    combined = f"{stdout or ''}\n{stderr or ''}"
    candidates.extend(re.findall(r"__BOREALIS_ONBOARDING_HOSTNAME__\s*=\s*([^\r\n]+)", combined, re.IGNORECASE))
    for candidate in candidates:
        hostname = _clean_onboarding_reported_hostname(candidate)
        if hostname:
            return hostname
    return ""


def _windows_output_is_launcher_marker_only(output: Any) -> bool:
    lines = [line.strip() for line in str(output or "").splitlines() if line.strip()]
    launch_markers = {
        "__BOREALIS_AGENT_EXE_STAGED__=1",
        "__BOREALIS_WINDOWS_ONBOARDING_STAGED__=1",
        "__BOREALIS_WINDOWS_ONBOARDING_LAUNCHER_STARTED__=1",
    }
    return bool(lines) and all(line in launch_markers for line in lines)


def _windows_smb_object_missing_error(error: Any) -> bool:
    normalized = str(error or "").strip().lower()
    return "status_object_name_not_found" in normalized or "0xc0000034" in normalized


def _windows_smb_sharing_violation_error(error: Any) -> bool:
    normalized = str(error or "").strip().lower()
    return "status_sharing_violation" in normalized or "0xc0000043" in normalized


def _windows_smb_invalid_parameter_error(error: Any) -> bool:
    normalized = str(error or "").strip().lower()
    return "status_invalid_parameter" in normalized or "0xc000000d" in normalized


def _ssh_banner_is_windows(banner: Any) -> bool:
    normalized = str(banner or "").strip().lower()
    return "openssh_for_windows" in normalized or "windows" in normalized


def _ssh_banner_is_unix_like(banner: Any) -> bool:
    normalized = str(banner or "").strip().lower()
    if not normalized.startswith("ssh-") or _ssh_banner_is_windows(normalized):
        return False
    return any(token in normalized for token in ("openssh", "dropbear", "libssh"))


def _ssh_banner_is_management_endpoint(banner: Any) -> bool:
    normalized = str(banner or "").strip().lower()
    return any(token in normalized for token in ("mpssh", "ilo", "integrated lights-out"))


def _ssh_error_is_unsupported_endpoint(error: Any, *, banner: Any = "") -> bool:
    normalized = str(error or "").strip().lower()
    if not normalized:
        return False
    if _ssh_banner_is_management_endpoint(banner):
        return True
    unsupported_tokens = (
        "no matching key exchange method",
        "no matching host key type",
        "unable to negotiate",
        "invalid_ssh_banner",
        "protocol major versions differ",
        "kex_exchange_identification",
    )
    if any(token in normalized for token in unsupported_tokens):
        return True
    if normalized == "ssh_session_timeout" and _ssh_banner_is_unix_like(banner):
        return True
    return False


def _ssh_unsupported_endpoint_detail(error: Any, *, banner: Any = "") -> str:
    banner_text = str(banner or "").strip()
    error_text = str(error or "").strip()
    if _ssh_banner_is_management_endpoint(banner_text):
        return "Unsupported OS: SSH endpoint looks like an iLO or network management interface, not an agent-capable operating system."
    if error_text == "ssh_session_timeout" and _ssh_banner_is_unix_like(banner_text):
        return "Unsupported OS: SSH endpoint did not complete noninteractive agent preflight; target may be an appliance or restricted shell."
    return "Unsupported OS: SSH endpoint is not compatible with Borealis agent onboarding."


def _ssh_preflight_failure_detail(error: Any) -> str:
    normalized = str(error or "").strip().lower()
    if normalized in {"sudo_password_required"}:
        return "SSH connection succeeded, but sudo password is required or rejected."
    if normalized in {"sudo_unavailable"}:
        return "SSH connection succeeded, but sudo is unavailable on the target."
    if normalized in {"permission_denied", "ssh_password_required"}:
        return "SSH authentication failed for all stored Linux credentials."
    if normalized == "ssh_session_timeout":
        return "SSH session timed out before Borealis could verify agent-capable shell access."
    if normalized.startswith("ssh_session_failed:"):
        return "SSH authentication failed for all stored Linux credentials."
    return "SSH connection failed for all stored Linux credentials."


def _infer_onboarding_platform_from_inventory(operating_system: Any = "", connection_type: Any = "") -> str:
    combined = f"{operating_system or ''} {connection_type or ''}".strip().lower()
    if not combined:
        return ""
    if any(token in combined for token in ("windows", "winrm", "smb")):
        return "windows"
    if any(token in combined for token in ("linux", "ubuntu", "debian", "rocky", "red hat", "rhel", "centos", "alma", "fedora", "ssh")):
        return "linux"
    return ""


def _linux_onboarding_privilege_probe(username: Any, become_method: Any) -> Tuple[str, str]:
    normalized_username = str(username or "").strip().lower()
    if normalized_username == "root":
        return "", "root"
    normalized_method = str(become_method or "").strip().lower()
    if normalized_method == "sudo":
        return "sudo", "root"
    return "", "root"


def _onboarding_progress_status(status: Any) -> str:
    normalized = str(status or "pending").strip().lower()
    if normalized in {"ssh_unreachable", "unreachable"}:
        return ONBOARDING_STATUS_UNREACHABLE
    if normalized in {"failed", "failure", "error"}:
        return ONBOARDING_STATUS_FAILED
    if normalized in {"skipped", "denied", "expired", "already_enrolled", "already_pending", "unsupported_os"}:
        return ONBOARDING_STATUS_SKIPPED
    if normalized in {"waiting_approval"}:
        return ONBOARDING_STATUS_WAITING_APPROVAL
    if normalized in {"completed", "approved", "success", "installed"}:
        return "completed"
    if normalized in {"running"}:
        return ONBOARDING_STATUS_RUNNING
    return ONBOARDING_STATUS_PENDING


def _onboarding_progress_status_is_active(status: Any) -> bool:
    return str(status or "").strip().lower() in {
        ONBOARDING_STATUS_PENDING,
        ONBOARDING_STATUS_RUNNING,
    }


def _onboarding_progress_task_from_output(*, stdout: Any = "", stderr: Any = "") -> str:
    text = f"{stdout or ''}\n{stderr or ''}"
    if not text.strip():
        return ""
    marker_matches = re.findall(r"__BOREALIS_AGENT_STEP_(?:STARTED|COMPLETED|FAILED|DEFERRED)__=([^\r\n]+)", text, re.IGNORECASE)
    if marker_matches:
        task = _onboarding_agent_step_task_name(marker_matches[-1])
        if task:
            return task
    dependency_matches = re.findall(r"Dependency:\s*([^\r\n]+)", text, re.IGNORECASE)
    if dependency_matches:
        task = _onboarding_agent_step_task_name(f"Dependency: {dependency_matches[-1]}")
        if task:
            return task
    combined = text.lower()
    if "__borealis_onboarding_already_enrolled__=1" in combined or "already enrolled and active" in combined:
        return "Already Enrolled and Active"
    if "__borealis_windows_onboarding_exit_code__=0" in combined or "agent installed" in combined or "approval pending" in combined:
        return "Agent Ready and Awaiting Approval"
    if "__borealis_onboarding_agent_repaired__=1" in combined or "successfully repaired agent" in combined:
        return "Successfully Repaired Agent"
    if "__borealis_onboarding_redeploy_required__=1" in combined or "unable to repair agent" in combined:
        return "Unable to Repair Agent > Re-Deploying"
    if "__borealis_onboarding_existing_agent_detected__=1" in combined or "existing agent detected" in combined:
        return "Existing Agent Detected"
    if "dependency: python" in combined:
        return _onboarding_dependency_task_name("Python")
    if "ensuring agent dependencies exist" in combined:
        return "Installing Agent Dependencies"
    if "deploying borealis agent" in combined:
        return "Running Agent Bootstrap"
    if "launching" in combined and "agent.ps1" in combined:
        return "Running Agent Bootstrap"
    if "syncing borealis repository into" in combined:
        return "Running Agent Bootstrap"
    if "downloading windows agent bootstrap" in combined:
        return "Running Agent Bootstrap"
    if "__borealis_agent_exe_started__" in combined:
        return "Running Agent Bootstrap"
    if "__borealis_agent_exe_staged__" in combined:
        return "Uploading Agent.exe to Remote Device"
    if "__borealis_onboarding_temp_cleaned__" in combined:
        return "Spinning-Up Site-Worker Container"
    if "syncing borealis ref" in combined:
        return "Running Agent Bootstrap"
    if "__borealis_onboarding_stale_process_killed__" in combined:
        return "Spinning-Up Site-Worker Container"
    return ""


def _onboarding_agent_step_task_name(value: Any) -> str:
    text = re.sub(r"\s+", " ", str(value or "")).strip()
    if not text:
        return ""
    text = re.sub(r"\s+completed$", "", text, flags=re.IGNORECASE).strip()
    text = re.sub(r"\s+started$", "", text, flags=re.IGNORECASE).strip()
    text = re.sub(r"\s+failed:.*$", "", text, flags=re.IGNORECASE).strip()
    if text.lower().startswith("dependency:"):
        return _onboarding_dependency_task_name(text.split(":", 1)[1])
    if text.lower().startswith("installing agent dependencies:"):
        return _onboarding_dependency_task_name(text.split(":", 1)[1])
    return text


def _onboarding_dependency_task_name(value: Any) -> str:
    label = re.sub(r"\s+", " ", str(value or "")).strip().strip(".:")
    label = re.sub(r"^[✅❌⏳\s]+", "", label).strip()
    label = re.sub(r"\s+-\s+(failed|completed|deferred).*$", "", label, flags=re.IGNORECASE).strip()
    label = re.sub(r"\s+dependency\s*$", "", label, flags=re.IGNORECASE).strip()
    lower = label.lower()
    if not label:
        return "Installing Agent Dependencies"
    if "wireguard" in lower:
        label = "WireGuard"
    elif "ultravnc" in lower or "vnc" in lower:
        label = "UltraVNC"
    elif "git" in lower:
        label = "Git"
    elif "python" in lower:
        label = "Python"
    return f"Installing Agent Dependencies: {label}"


def _onboarding_windows_protocol_name(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if "smb service" in normalized or "smb" in normalized:
        return "SMB Service"
    if "scheduled task" in normalized:
        return "Scheduled Task"
    if "wmi" in normalized or "dcom" in normalized:
        return "WMI/DCOM"
    if "winrm" in normalized:
        return "WinRM"
    if "ssh" in normalized:
        return "SSH"
    return ""


def _onboarding_progress_task(*, status: Any = "", detail: Any = "", stdout: Any = "", stderr: Any = "") -> str:
    normalized_status = str(status or "pending").strip().lower()
    if normalized_status == ONBOARDING_STATUS_WAITING_APPROVAL:
        return "Agent Ready and Awaiting Approval"
    if normalized_status in {"approved", "completed", "success", "installed"}:
        output_task = _onboarding_progress_task_from_output(stdout=stdout, stderr=stderr)
        detail_text = str(detail or "").strip()
        completed_detail_lower = str(detail or "").strip().lower()
        if "device approved" in completed_detail_lower or "enrollment completed" in completed_detail_lower:
            return "Device Enrollment Approved"
        if "successfully repaired agent" in completed_detail_lower or "agent repaired" in completed_detail_lower:
            return "Successfully Repaired Agent"
        if detail_text:
            detail_task = _onboarding_progress_task(status=ONBOARDING_STATUS_RUNNING, detail=detail_text)
            if detail_task:
                return detail_task
        if output_task == "Successfully Repaired Agent":
            return output_task
        return "Device Enrollment Approved"
    if normalized_status in {"failed", "failure", "error"}:
        failed_detail_lower = str(detail or "").strip().lower()
        failed_output_lower = f"{stdout or ''}\n{stderr or ''}".lower()
        if "unsupported os" in failed_detail_lower or "__borealis_unsupported_os__" in failed_output_lower:
            return "Unsupported OS"
        return "Onboarding Failed"
    if normalized_status in {"unreachable", "ssh_unreachable"}:
        return "Remote Device Unreachable"
    if normalized_status in {"skipped", "already_enrolled", "already_pending", "denied", "expired", "unsupported_os"}:
        output_task = _onboarding_progress_task_from_output(stdout=stdout, stderr=stderr)
        if output_task in {"Already Enrolled and Active", "Successfully Repaired Agent", "Existing Agent Detected"}:
            return output_task
        detail_text = str(detail or "").strip()
        skipped_detail_lower = detail_text.lower()
        if "unsupported os" in skipped_detail_lower:
            return detail_text or "Unsupported OS"
        if "network management interface" in skipped_detail_lower or "agent-capable operating system" in skipped_detail_lower:
            return "Unsupported OS: SSH endpoint looks like an iLO or network management interface."
        if "already enrolled and active" in skipped_detail_lower or "already appears enrolled" in skipped_detail_lower:
            return "Already Enrolled and Active"
        if "successfully repaired agent" in skipped_detail_lower or "agent repaired" in skipped_detail_lower:
            return "Successfully Repaired Agent"
        if "existing borealis agent" in skipped_detail_lower or "existing agent detected" in skipped_detail_lower:
            return "Existing Agent Detected"
        return "Onboarding Skipped"

    detail_text = str(detail or "").strip()
    detail_lower = detail_text.lower()
    if detail_lower.startswith("auto-detecting remote os"):
        return "Auto-Detecting Remote OS"
    if detail_lower.startswith("establishing connection to remote device"):
        return detail_text
    if detail_lower.startswith("detected remote operating system"):
        return detail_text
    if detail_lower.startswith("dependency:"):
        task = _onboarding_agent_step_task_name(detail_text)
        if task:
            return task
    if "waiting for onboarding work" in detail_lower or "trying windows remote enrollment" in detail_lower:
        return "Spinning-Up Site-Worker Container"
    if detail_lower.startswith("trying windows") and "enrollment" in detail_lower:
        return "Establishing Connection to Remote Device"
    if "connection established" in detail_lower:
        if detail_lower.startswith("connection established using") and ":" in detail_text:
            return detail_text
        protocol = _onboarding_windows_protocol_name(detail_lower)
        if protocol:
            return f"Connection Established using {protocol}"
    if "connecting to windows smb" in detail_lower or "connecting to ssh" in detail_lower:
        return "Establishing Connection to Remote Device"
    if "staging borealis agent.exe" in detail_lower:
        return "Uploading Agent.exe to Remote Device"
    if "staging borealis onboarding script" in detail_lower:
        return "Uploading Agent.exe to Remote Device"
    if "downloading agent.ps1" in detail_lower:
        return "Running Agent Bootstrap"
    if "cleaning onboarding temp" in detail_lower:
        return "Spinning-Up Site-Worker Container"
    if "stale onboarding process" in detail_lower:
        return "Spinning-Up Site-Worker Container"
    if "binding to windows service control manager" in detail_lower or "creating transient borealis onboarding service" in detail_lower:
        return "Creating Windows Service to Run Agent.exe using SMB Service"
    if "starting transient borealis onboarding service" in detail_lower:
        return "Ensuring Windows Service is Running"
    if "scheduled task" in detail_lower:
        if "registering" in detail_lower:
            return "Creating Remote Scheduled Task"
        if "starting" in detail_lower:
            return "Ensuring Remote Scheduled Task is Running"
        return "Connection Established using Scheduled Task"
    if "wmi" in detail_lower or "dcom" in detail_lower:
        return "Connection Established using WMI/DCOM"
    if "winrm" in detail_lower:
        return "Connection Established using WinRM"
    if (
        "agent installed through" in detail_lower
        or "approval pending" in detail_lower
        or "approval queue" in detail_lower
        or "approval callback" in detail_lower
        or "waiting for borealis approval" in detail_lower
    ):
        return "Agent Ready and Awaiting Approval"
    if "device approved" in detail_lower or "enrollment completed" in detail_lower:
        return "Device Enrollment Approved"
    if "successfully repaired agent" in detail_lower or "agent repaired" in detail_lower:
        return "Successfully Repaired Agent"
    if "unable to repair agent" in detail_lower or "re-deploying" in detail_lower:
        return "Unable to Repair Agent > Re-Deploying"
    if "existing borealis agent" in detail_lower or "existing agent detected" in detail_lower:
        return "Existing Agent Detected"
    if "running agent bootstrap" in detail_lower or "running agent.ps1" in detail_lower or "agent.exe started" in detail_lower:
        return "Running Agent Bootstrap"
    if "installing agent dependencies" in detail_lower:
        if ":" in detail_text:
            return _onboarding_dependency_task_name(detail_text.split(":", 1)[1])
        installing_match = re.search(r"installing\s+(.+?)(?:\.|$)", detail_text, re.IGNORECASE)
        if installing_match:
            return _onboarding_dependency_task_name(installing_match.group(1))
        return "Installing Agent Dependencies"
    if "waiting for windows" in detail_lower or "output file lock" in detail_lower:
        return _onboarding_progress_task_from_output(stdout=stdout, stderr=stderr) or "Running Agent Bootstrap"
    if detail_lower:
        return detail_text
    return _onboarding_progress_task_from_output(stdout=stdout, stderr=stderr) or "Spinning-Up Site-Worker Container"


def _onboarding_timeline_compaction_key(event: Mapping[str, Any]) -> str:
    task = str(event.get("task") or "").strip()
    lower = task.lower()
    if not task:
        return ""
    if lower.startswith("connection established using"):
        protocol = task
        if ":" in protocol:
            protocol = protocol.split(":", 1)[0]
        return f"connection:{protocol.strip().lower()}"
    if lower.startswith("installing agent dependencies:"):
        return f"dependency:{lower}"
    if lower in {
        "spinning-up site-worker container",
        "auto-detecting remote os",
        "running agent bootstrap",
        "existing agent detected",
        "unable to repair agent > re-deploying",
        "agent ready and awaiting approval",
        "already enrolled and active",
        "device enrollment approved",
    }:
        return lower
    return ""


def _compact_onboarding_timeline(timeline: Sequence[Mapping[str, Any]]) -> List[Dict[str, Any]]:
    has_approval = any(str(event.get("task") or "").strip().lower() == "device enrollment approved" for event in timeline)
    compacted: List[Dict[str, Any]] = []
    keyed_indexes: Dict[str, int] = {}
    for raw_event in timeline:
        event = dict(raw_event or {})
        if has_approval and str(event.get("task") or "").strip().lower() == "onboarding failed":
            combined = "\n".join(
                str(event.get(field) or "")
                for field in ("detail", "stdout_snippet", "stderr_snippet", "stdout", "stderr")
            ).lower()
            if "approval_callback_timeout" in combined or "approval" in combined:
                continue
        key = _onboarding_timeline_compaction_key(event)
        if key and key in keyed_indexes:
            existing = compacted[keyed_indexes[key]]
            event_task = str(event.get("task") or "").strip()
            existing_task = str(existing.get("task") or "").strip()
            if ":" in event_task and ":" not in existing_task:
                existing["task"] = event_task
            for field in ("detail", "stdout_snippet", "stderr_snippet", "stdout", "stderr"):
                if event.get(field):
                    existing[field] = event.get(field)
            if event.get("status"):
                existing["status"] = event.get("status")
            for field in ("finished_at", "updated_at"):
                try:
                    existing[field] = max(int(existing.get(field) or 0), int(event.get(field) or 0)) or existing.get(field)
                except Exception:
                    if event.get(field):
                        existing[field] = event.get(field)
            continue
        if key:
            keyed_indexes[key] = len(compacted)
        compacted.append(event)
    return compacted


def _set_impacket_timeout(obj: Any, timeout_seconds: float) -> None:
    timeout = max(3, min(60, int(float(timeout_seconds or 0.0) or 10)))
    for method_name in ("set_timeout", "setTimeout", "set_connect_timeout", "setConnectTimeout"):
        method = getattr(obj, method_name, None)
        if not callable(method):
            continue
        try:
            method(timeout)
            return
        except Exception:
            continue
    get_socket = getattr(obj, "get_socket", None)
    if callable(get_socket):
        try:
            sock = get_socket()
            if hasattr(sock, "settimeout"):
                sock.settimeout(timeout)
        except Exception:
            pass


def _windows_onboarding_child_entry(result_queue: Any, method: Callable[..., Dict[str, Any]], kwargs: Dict[str, Any]) -> None:
    def _status_update(
        detail: str,
        stdout: str = "",
        stderr: str = "",
        target_hostname: str = "",
        event_timestamp: Optional[int] = None,
        event_status: str = "",
    ) -> None:
        try:
            result_queue.put(
                {
                    "type": "status",
                    "detail": str(detail or ""),
                    "stdout": str(stdout or ""),
                    "stderr": str(stderr or ""),
                    "target_hostname": str(target_hostname or ""),
                    "event_timestamp": event_timestamp,
                    "event_status": str(event_status or ""),
                },
                block=False,
            )
        except Exception:
            pass

    child_kwargs = dict(kwargs or {})
    child_kwargs["approval_check"] = None
    child_kwargs["status_update"] = _status_update
    try:
        result = method(**child_kwargs)
        if not isinstance(result, dict):
            result = {"exit_code": 1, "stdout": "", "stderr": "windows_onboarding_child_invalid_result"}
    except BaseException as exc:
        result = {
            "exit_code": 1,
            "stdout": "",
            "stderr": f"{exc.__class__.__name__}: {exc}",
        }
    try:
        result_queue.put({"type": "result", "result": result}, block=True, timeout=2.0)
    except Exception:
        pass


def _now_ts() -> int:
    return int(time.time())


def _parse_onboarding_event_timestamp(value: Any) -> Optional[int]:
    text = str(value or "").strip()
    if not text:
        return None
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    try:
        parsed = datetime.fromisoformat(text)
    except Exception:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    try:
        return int(parsed.timestamp())
    except Exception:
        return None


def _normalize_ssh_private_key_text(value: Any) -> str:
    text = "" if value is None else str(value)
    if not text:
        return ""
    normalized = text.lstrip("\ufeff").replace("\r\n", "\n").replace("\r", "\n")
    if normalized and not normalized.endswith("\n"):
        normalized += "\n"
    return normalized


def _onboarding_status_bucket(status: Any) -> str:
    normalized = str(status or "").strip().lower()
    if normalized in {ONBOARDING_STATUS_WAITING_APPROVAL, "approved", "completed", "installed", "success"}:
        return "success"
    if normalized == ONBOARDING_STATUS_RUNNING:
        return "running"
    if normalized in {"pending", "queued"}:
        return "pending"
    if normalized in {"already_enrolled", "already_pending", ONBOARDING_STATUS_SKIPPED, "unsupported_os"}:
        return "skipped"
    return "failed" if normalized else "pending"


def _env_positive_int(name: str, default: int) -> int:
    try:
        value = int(str(os.environ.get(name, "")).strip())
        if value > 0:
            return value
    except Exception:
        pass
    return int(default)


def _env_non_negative_float(name: str, default: float) -> float:
    try:
        value = float(str(os.environ.get(name, "")).strip())
        if value >= 0:
            return value
    except Exception:
        pass
    return float(default)


def _normalize_onboarding_job_kind(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized in {"device_onboarding", "automatic_onboarding", "ssh_onboarding", JOB_KIND_ONBOARDING}:
        return JOB_KIND_ONBOARDING
    return normalized


def _now_minute_ts() -> int:
    return int(_now_ts() // 60) * 60


class OnboardingSchedulerMixin:
    def _register_onboarding_routes(self, app: Any) -> None:
        @app.route("/api/onboarding/jobs/<int:job_id>/redeploy", methods=["POST"])
        def api_onboarding_job_redeploy(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        """
                        SELECT components_json,
                               targets_json,
                               credential_id,
                               job_kind
                          FROM scheduled_jobs
                         WHERE id=?
                        """,
                        (int(job_id),),
                    )
                    row = cur.fetchone()
                    if not row:
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if not self._job_visible_to_user(user, row[1]):
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if _normalize_onboarding_job_kind(row[3]) != JOB_KIND_ONBOARDING:
                        return json.dumps({"error": "not onboarding job"}), 400, {"Content-Type": "application/json"}
                    components_json = row[0] or "[]"
                    targets_json = row[1] or "[]"
                    credential_id = row[2]
                finally:
                    conn.close()

                try:
                    components = json.loads(components_json or "[]")
                except Exception:
                    components = []
                try:
                    targets = json.loads(targets_json or "[]")
                except Exception:
                    targets = []
                try:
                    job_credential_id = int(credential_id) if credential_id is not None else None
                except Exception:
                    job_credential_id = None

                cleared = self._clear_scheduled_job_run_history(int(job_id))
                occurrence_ts = _now_minute_ts()
                created_at = _now_ts()
                self._record_onboarding_occurrence_snapshot(
                    job_id=int(job_id),
                    scheduled_ts=int(occurrence_ts),
                    created_at=created_at,
                )
                occurrence_runs = self._load_occurrence_runs(int(job_id), int(occurrence_ts))
                for run in occurrence_runs:
                    self._dispatch_onboarding_run(
                        job_id=int(job_id),
                        run_row_id=int(run["id"]),
                        scheduled_ts=int(occurrence_ts),
                        components=components,
                        targets=targets,
                        credential_id=job_credential_id,
                    )
                return json.dumps(
                    {
                        "status": "ok",
                        "cleared": int(cleared),
                        "occurrence": int(occurrence_ts),
                        "run_ids": [int(run["id"]) for run in occurrence_runs],
                    }
                ), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/onboarding/jobs/<int:job_id>/targets", methods=["GET"])
        def api_onboarding_job_targets(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                from flask import request

                occurrence = request.args.get("occurrence")
                occ = int(occurrence) if occurrence else None
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute("SELECT targets_json, job_kind FROM scheduled_jobs WHERE id=?", (job_id,))
                    row = cur.fetchone()
                    if not row:
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if _normalize_onboarding_job_kind(row[1]) != JOB_KIND_ONBOARDING:
                        return json.dumps({"error": "not onboarding job"}), 400, {"Content-Type": "application/json"}
                    if not self._job_visible_to_user(user, row[0]):
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if occ is None:
                        cur.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
                        occ_row = cur.fetchone()
                        occ = int(occ_row[0]) if occ_row and occ_row[0] else None
                finally:
                    conn.close()
                rows = self._load_onboarding_target_rows(int(job_id), int(occ)) if occ is not None else []
                rows = self._backfill_onboarding_target_approval_references(rows)
                return json.dumps({"occurrence": occ, "targets": rows}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

    def _score_remote_onboarding_protocols(
        self,
        *,
        host: str,
        ssh_port: int,
        smb_port: int,
        winrm_port: int,
    ) -> Dict[str, Any]:
        scores = {"windows": 0, "linux": 0, "unsupported": 0}
        signals: List[str] = []
        errors: Dict[str, str] = {}
        unsupported_detail = ""
        normalized_host = str(host or "").strip()
        if not normalized_host:
            return {
                "scores": scores,
                "signals": ["missing host"],
                "detail": "Auto-Detecting Remote OS: missing host",
                "classification": "unknown",
                "unsupported_detail": "",
                "errors": errors,
            }

        ssh_error = self._preflight_remote_port(host=normalized_host, port=int(ssh_port), attempts=1, timeout_seconds=2.5)
        errors["ssh"] = ssh_error
        if ssh_error:
            signals.append(f"SSH {int(ssh_port)} closed ({ssh_error})")
        else:
            scores["linux"] += 20
            signals.append(f"SSH {int(ssh_port)} open (+20 Linux)")
            banner, banner_error = self._read_ssh_banner(
                host=normalized_host,
                port=int(ssh_port),
                timeout_seconds=_env_non_negative_float(_SSH_BANNER_TIMEOUT_ENV, 4.0),
            )
            errors["ssh_banner"] = banner_error
            if banner:
                safe_banner = banner.replace(";", ",")[:80]
                signals.append(f"SSH banner '{safe_banner}'")
                if _ssh_banner_is_management_endpoint(banner):
                    scores["unsupported"] += 100
                    unsupported_detail = _ssh_unsupported_endpoint_detail("", banner=banner)
                    signals.append("management SSH banner (+100 Unsupported)")
                elif _ssh_banner_is_windows(banner):
                    scores["windows"] += 35
                    signals.append("Windows SSH banner (+35 Windows)")
                elif _ssh_banner_is_unix_like(banner):
                    scores["linux"] += 45
                    signals.append("Unix-like SSH banner (+45 Linux)")
            elif banner_error:
                signals.append(f"SSH banner unavailable ({banner_error})")
                if _ssh_error_is_unsupported_endpoint(banner_error):
                    scores["unsupported"] += 90
                    unsupported_detail = _ssh_unsupported_endpoint_detail(banner_error)
                    signals.append("unsupported SSH endpoint response (+90 Unsupported)")

        smb_error = self._preflight_remote_port(host=normalized_host, port=int(smb_port), attempts=1, timeout_seconds=2.5)
        errors["smb"] = smb_error
        if smb_error:
            signals.append(f"SMB {int(smb_port)} closed ({smb_error})")
        else:
            scores["windows"] += 45
            signals.append(f"SMB {int(smb_port)} open (+45 Windows)")

        winrm_error = self._preflight_remote_port(host=normalized_host, port=int(winrm_port), attempts=1, timeout_seconds=2.5)
        errors["winrm"] = winrm_error
        if winrm_error:
            signals.append(f"WinRM {int(winrm_port)} closed ({winrm_error})")
        else:
            scores["windows"] += 35
            signals.append(f"WinRM {int(winrm_port)} open (+35 Windows)")

        rpc_error = self._preflight_remote_port(host=normalized_host, port=135, attempts=1, timeout_seconds=2.5)
        errors["rpc"] = rpc_error
        if not rpc_error:
            scores["windows"] += 20
            signals.append("RPC 135 open (+20 Windows)")

        classification = "windows" if scores["windows"] >= scores["linux"] else "linux"
        if scores[classification] <= 0:
            classification = "unknown"
        if (
            scores["unsupported"] >= 90
            and scores["unsupported"] >= max(scores["windows"], scores["linux"])
            and (
                "management interface" in unsupported_detail.lower()
                or (scores["windows"] <= 0 and scores["linux"] <= 20)
            )
        ):
            classification = "unsupported"
        detail = (
            "Auto-Detecting Remote OS: "
            f"Windows={scores['windows']} Linux={scores['linux']} Unsupported={scores['unsupported']} | "
            + "; ".join(signals)
        )
        return {
            "scores": scores,
            "signals": signals,
            "detail": detail,
            "classification": classification,
            "unsupported_detail": unsupported_detail,
            "errors": errors,
        }

    def _onboarding_target_cap(self) -> int:
        return _env_positive_int("BOREALIS_ONBOARDING_TARGET_CAP", DEFAULT_ONBOARDING_TARGET_CAP)

    def _onboarding_concurrency(self) -> int:
        return _env_positive_int("BOREALIS_ONBOARDING_CONCURRENCY", DEFAULT_ONBOARDING_CONCURRENCY)

    def _onboarding_install_timeout_seconds(self) -> float:
        return _env_non_negative_float("BOREALIS_ONBOARDING_INSTALL_TIMEOUT_SECONDS", 900.0) or 900.0

    def _windows_onboarding_observation_timeout_seconds(self) -> float:
        configured = _env_non_negative_float("BOREALIS_WINDOWS_ONBOARDING_OBSERVATION_TIMEOUT_SECONDS", 0.0)
        if configured > 0:
            return configured
        return self._onboarding_install_timeout_seconds()

    def _windows_onboarding_launch_grace_seconds(self) -> float:
        configured = _env_non_negative_float("BOREALIS_WINDOWS_ONBOARDING_LAUNCH_GRACE_SECONDS", 45.0) or 45.0
        observation_timeout = self._windows_onboarding_observation_timeout_seconds()
        return max(10.0, min(float(configured), float(observation_timeout)))

    def _windows_onboarding_approval_wait_seconds(self) -> float:
        configured = _env_non_negative_float("BOREALIS_WINDOWS_ONBOARDING_APPROVAL_WAIT_SECONDS", 300.0) or 300.0
        observation_timeout = self._windows_onboarding_observation_timeout_seconds()
        return max(5.0, min(float(configured), float(observation_timeout)))

    def _windows_wmi_dcom_timeout_seconds(self) -> float:
        configured = _env_non_negative_float("BOREALIS_WINDOWS_WMI_DCOM_TIMEOUT_SECONDS", 0.0)
        if configured > 0:
            return max(10.0, configured)
        return max(20.0, min(self._windows_onboarding_observation_timeout_seconds(), 75.0))

    def _run_windows_onboarding_method_in_child(
        self,
        *,
        method: Callable[..., Dict[str, Any]],
        method_kwargs: Dict[str, Any],
        timeout_seconds: float,
        timeout_label: str,
        approval_check: Optional[Callable[[], str]] = None,
        status_update: Optional[Callable[..., None]] = None,
    ) -> Dict[str, Any]:
        try:
            context = multiprocessing.get_context("fork")
        except Exception:
            return method(**method_kwargs)

        result_queue = context.Queue()
        process = context.Process(
            target=_windows_onboarding_child_entry,
            args=(result_queue, method, dict(method_kwargs or {})),
        )
        process.daemon = True
        process.start()
        deadline = time.monotonic() + max(10.0, float(timeout_seconds or 0.0))
        result: Optional[Dict[str, Any]] = None
        approval_reference = ""

        def _drain_messages() -> None:
            nonlocal result
            while True:
                try:
                    message = result_queue.get_nowait()
                except queue_module.Empty:
                    break
                except Exception:
                    break
                if not isinstance(message, dict):
                    continue
                if message.get("type") == "status" and callable(status_update):
                    status_update(
                        str(message.get("detail") or ""),
                        str(message.get("stdout") or ""),
                        str(message.get("stderr") or ""),
                        str(message.get("target_hostname") or ""),
                        message.get("event_timestamp"),
                        str(message.get("event_status") or ""),
                    )
                elif message.get("type") == "result":
                    payload = message.get("result")
                    result = dict(payload) if isinstance(payload, dict) else {
                        "exit_code": 1,
                        "stdout": "",
                        "stderr": "windows_onboarding_child_invalid_result",
                    }

        while time.monotonic() < deadline:
            process.join(timeout=0.5)
            _drain_messages()
            if result is not None or not process.is_alive():
                break
            if callable(approval_check):
                try:
                    approval_reference = str(approval_check() or "").strip()
                except Exception:
                    approval_reference = ""
                if approval_reference:
                    break

        _drain_messages()
        if approval_reference:
            if process.is_alive():
                process.terminate()
                process.join(timeout=3.0)
            return {
                "exit_code": 0,
                "stdout": "Borealis approval detected while Windows remote method was still running.",
                "stderr": "",
                "approval_reference": approval_reference,
            }
        if result is not None:
            if process.is_alive():
                process.join(timeout=1.0)
            return result
        if process.is_alive():
            if callable(status_update):
                status_update(f"{timeout_label} timed out; falling back to next Windows enrollment method.", "", "")
            process.terminate()
            process.join(timeout=3.0)
            if process.is_alive():
                try:
                    process.kill()
                    process.join(timeout=3.0)
                except Exception:
                    pass
            return {
                "exit_code": 124,
                "stdout": "",
                "stderr": f"{timeout_label.lower().replace('/', '_').replace(' ', '_')}_timeout",
            }
        exit_code = process.exitcode
        return {
            "exit_code": 1,
            "stdout": "",
            "stderr": f"{timeout_label} exited without result (exit_code={exit_code}).",
        }

    def _load_onboarding_site(self, site_id: Optional[int]) -> Optional[Dict[str, Any]]:
        if site_id is None:
            return None
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, name, enrollment_code
                  FROM sites
                 WHERE id=?
                """,
                (int(site_id),),
            )
            row = cur.fetchone()
            if not row:
                return None
            return {
                "id": int(row[0]),
                "name": row[1] or "",
                "enrollment_code": row[2] or "",
            }
        finally:
            conn.close()

    def _onboarding_component(self, components: Sequence[Any]) -> Dict[str, Any]:
        for component in components or []:
            if not isinstance(component, dict):
                continue
            kind = str(
                component.get("kind")
                or component.get("type")
                or component.get("component_type")
                or component.get("assembly_type")
                or ""
            ).strip().lower()
            if kind in {ONBOARDING_COMPONENT_KIND, JOB_KIND_ONBOARDING, "automatic_device_onboarding"}:
                return dict(component)
        return dict(components[0]) if components and isinstance(components[0], dict) else {}

    def _onboarding_scope_config(
        self,
        *,
        components: Sequence[Any],
        targets: Sequence[Any],
    ) -> Tuple[Dict[str, Any], Optional[str]]:
        component = self._onboarding_component(components)
        site_id: Optional[int] = None
        site_name = ""
        scope_entries: List[str] = []
        exclusion_entries: List[str] = []
        for target in targets or []:
            if not isinstance(target, dict):
                continue
            if str(target.get("kind") or target.get("type") or "").strip().lower() != "onboarding_scope":
                continue
            try:
                site_id = int(target.get("site_id") or target.get("siteId"))
            except Exception:
                site_id = None
            site_name = str(target.get("site_name") or target.get("site") or site_name or "").strip()
            raw_entries = target.get("entries")
            if isinstance(raw_entries, str):
                scope_entries.extend([line.strip() for line in raw_entries.replace(",", "\n").replace(";", "\n").splitlines() if line.strip()])
            elif isinstance(raw_entries, (list, tuple)):
                scope_entries.extend([str(value).strip() for value in raw_entries if str(value).strip()])
            raw_exclusions = (
                target.get("exclusions")
                or target.get("exclude_entries")
                or target.get("exclusion_scope")
                or target.get("exclusionScope")
                or []
            )
            if isinstance(raw_exclusions, str):
                exclusion_entries.extend([line.strip() for line in raw_exclusions.replace(",", "\n").replace(";", "\n").splitlines() if line.strip()])
            elif isinstance(raw_exclusions, (list, tuple)):
                exclusion_entries.extend([str(value).strip() for value in raw_exclusions if str(value).strip()])
        if site_id is None:
            return {}, "Onboarding jobs require a site."
        if not scope_entries:
            return {}, "Onboarding jobs require at least one IP address, CIDR, range, or FQDN."
        branch = str(
            component.get("install_branch")
            or component.get("repo_branch")
            or component.get("branch")
            or "main"
        ).strip() or "main"
        if not re.match(r"^[A-Za-z0-9._/\-]+$", branch):
            return {}, "Install branch contains unsupported characters."
        platform = str(
            component.get("agent_platform")
            or component.get("target_os")
            or component.get("platform")
            or component.get("os")
            or ONBOARDING_PLATFORM_LINUX
        ).strip().lower()
        if platform in {"auto", "detect", "automatic", "autodetect", "auto_detect"}:
            platform = ONBOARDING_PLATFORM_AUTO
        elif platform in {"linux_ssh", "ssh"}:
            platform = ONBOARDING_PLATFORM_LINUX
        elif platform in {"windows_remote", "windows_smb", "smb", "winrm"}:
            platform = ONBOARDING_PLATFORM_WINDOWS
        if platform not in {ONBOARDING_PLATFORM_AUTO, ONBOARDING_PLATFORM_LINUX, ONBOARDING_PLATFORM_WINDOWS}:
            return {}, "Agent platform must be Auto, Linux, or Windows."
        ssh_port = DEFAULT_ONBOARDING_SSH_PORT
        for key in ("ssh_port", "port"):
            if component.get(key) not in (None, ""):
                try:
                    parsed = int(component.get(key))
                    if 1 <= parsed <= 65535:
                        ssh_port = parsed
                        break
                except Exception:
                    pass
        windows_port = DEFAULT_ONBOARDING_WINDOWS_PORT
        for key in ("windows_port", "smb_port", "port"):
            if component.get(key) not in (None, ""):
                try:
                    parsed = int(component.get(key))
                    if 1 <= parsed <= 65535:
                        windows_port = parsed
                        break
                except Exception:
                    pass
        winrm_port = DEFAULT_ONBOARDING_WINRM_PORT
        for key in ("winrm_port", "windows_winrm_port"):
            if component.get(key) not in (None, ""):
                try:
                    parsed = int(component.get(key))
                    if 1 <= parsed <= 65535:
                        winrm_port = parsed
                        break
                except Exception:
                    pass
        methods: List[str] = []
        if platform in {ONBOARDING_PLATFORM_AUTO, ONBOARDING_PLATFORM_WINDOWS}:
            raw_methods = component.get("onboarding_methods") or component.get("windows_methods") or []
            if isinstance(raw_methods, str):
                method_candidates = [piece.strip().lower() for piece in re.split(r"[,;\s]+", raw_methods) if piece.strip()]
            elif isinstance(raw_methods, (list, tuple, set)):
                method_candidates = [str(piece or "").strip().lower() for piece in raw_methods if str(piece or "").strip()]
            else:
                method_candidates = []
            if not method_candidates:
                method_candidates = list(ONBOARDING_WINDOWS_METHODS)
            method_aliases = {
                "service": ONBOARDING_WINDOWS_METHOD_SMB_SCM,
                "scm": ONBOARDING_WINDOWS_METHOD_SMB_SCM,
                "psexec": ONBOARDING_WINDOWS_METHOD_SMB_SCM,
                "smb": ONBOARDING_WINDOWS_METHOD_SMB_SCM,
                "task": ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK,
                "tasks": ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK,
                "schtask": ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK,
                "schtasks": ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK,
                "dcom": ONBOARDING_WINDOWS_METHOD_WMI_DCOM,
                "wmi": ONBOARDING_WINDOWS_METHOD_WMI_DCOM,
                "wmi_dcom": ONBOARDING_WINDOWS_METHOD_WMI_DCOM,
                "wmic": ONBOARDING_WINDOWS_METHOD_WMI_DCOM,
                "winrm": ONBOARDING_WINDOWS_METHOD_WINRM,
            }
            for method in method_candidates:
                if method in {"ssh", "linux", "linux_ssh"}:
                    continue
                normalized_method = method_aliases.get(method, method)
                if normalized_method not in ONBOARDING_WINDOWS_METHODS:
                    return {}, "Unsupported Windows onboarding method."
                if normalized_method not in methods:
                    methods.append(normalized_method)
            methods = _windows_onboarding_methods_with_required_fallbacks(methods)
        else:
            methods = ["ssh"]
        transport_port = ssh_port if platform in {ONBOARDING_PLATFORM_AUTO, ONBOARDING_PLATFORM_LINUX} else windows_port
        def _credential_id_list(*keys: str) -> List[int]:
            values: List[int] = []
            for key in keys:
                raw_value = component.get(key)
                if raw_value in (None, ""):
                    continue
                if isinstance(raw_value, str):
                    candidates = [piece.strip() for piece in re.split(r"[,;\s]+", raw_value) if piece.strip()]
                elif isinstance(raw_value, (list, tuple, set)):
                    candidates = list(raw_value)
                else:
                    candidates = [raw_value]
                for candidate in candidates:
                    try:
                        parsed = int(candidate)
                    except Exception:
                        continue
                    if parsed > 0 and parsed not in values:
                        values.append(parsed)
            return values
        windows_credential_ids = _credential_id_list("windows_credential_ids", "stored_windows_credential_ids", "windows_credentials")
        linux_credential_ids = _credential_id_list("linux_credential_ids", "stored_linux_credential_ids", "linux_credentials")
        concurrency = self._onboarding_concurrency()
        for key in ("onboarding_concurrency", "device_onboarding_concurrency", "concurrency", "max_concurrency"):
            if component.get(key) not in (None, ""):
                try:
                    parsed = int(component.get(key))
                    if 1 <= parsed <= 100:
                        concurrency = parsed
                        break
                except Exception:
                    return {}, "Device onboarding concurrency must be 1-100."
                return {}, "Device onboarding concurrency must be 1-100."
        return {
            "component": component,
            "site_id": site_id,
            "site_name": site_name,
            "entries": scope_entries,
            "exclusions": exclusion_entries,
            "install_branch": branch,
            "agent_platform": platform,
            "ssh_port": ssh_port,
            "windows_port": windows_port,
            "winrm_port": winrm_port,
            "transport_port": transport_port,
            "onboarding_methods": methods,
            "windows_credential_ids": windows_credential_ids,
            "linux_credential_ids": linux_credential_ids,
            "onboarding_concurrency": concurrency,
        }, None

    def _record_onboarding_occurrence_snapshot(
        self,
        *,
        job_id: int,
        scheduled_ts: int,
        created_at: int,
    ) -> None:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT COUNT(*) FROM scheduled_job_runs WHERE job_id=? AND scheduled_ts=?",
                (int(job_id), int(scheduled_ts)),
            )
            existing_count = int(cur.fetchone()[0] or 0)
            if existing_count > 0:
                return
            cur.execute(
                """
                INSERT INTO scheduled_job_runs(
                    job_id,
                    scheduled_ts,
                    status,
                    skip_reason,
                    created_at,
                    updated_at,
                    shared_execution,
                    component_index,
                    component_kind,
                    component_name
                ) VALUES (?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    int(job_id),
                    int(scheduled_ts),
                    RUN_STATUS_PENDING,
                    "",
                    int(created_at),
                    int(created_at),
                    1,
                    0,
                    ONBOARDING_COMPONENT_KIND,
                    ONBOARDING_COMPONENT_NAME,
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def _load_onboarding_target_rows(self, job_id: int, scheduled_ts: int) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    run_id,
                    job_id,
                    scheduled_ts,
                    site_id,
                    target_input,
                    target_address,
                    target_hostname,
                    ssh_port,
                    status,
                    detail,
                    stdout_snippet,
                    stderr_snippet,
                    approval_reference,
                    created_at,
                    updated_at,
                    finished_at
                  FROM scheduled_job_onboarding_targets
                 WHERE job_id=? AND scheduled_ts=?
              ORDER BY id ASC
                """,
                (int(job_id), int(scheduled_ts)),
            )
            rows = [
                {
                    "id": int(row[0]),
                    "run_id": int(row[1]),
                    "job_id": int(row[2]),
                    "scheduled_ts": int(row[3]),
                    "site_id": row[4],
                    "target_input": row[5] or "",
                    "target_address": row[6] or "",
                    "target_hostname": row[7] or "",
                    "ssh_port": int(row[8] or DEFAULT_ONBOARDING_SSH_PORT),
                    "status": row[9] or "",
                    "detail": row[10] or "",
                    "stdout_snippet": row[11] or "",
                    "stderr_snippet": row[12] or "",
                    "approval_reference": row[13] or "",
                    "created_at": row[14],
                    "updated_at": row[15],
                    "finished_at": row[16],
                }
                for row in cur.fetchall()
            ]
        finally:
            conn.close()
        known_metadata_by_row_id = self._bulk_lookup_known_onboarding_target_metadata(rows)
        timelines = self._load_onboarding_target_event_rows([int(row["id"]) for row in rows])
        for row in rows:
            known_metadata = known_metadata_by_row_id.get(int(row["id"]), {})
            known_hostname = _clean_onboarding_reported_hostname(known_metadata.get("hostname") if isinstance(known_metadata, Mapping) else "")
            if known_hostname and not _clean_onboarding_reported_hostname(row.get("target_hostname")):
                row["target_hostname"] = known_hostname
            if isinstance(known_metadata, Mapping):
                detected_platform = str(known_metadata.get("detected_platform") or "").strip().lower()
                operating_system = str(known_metadata.get("operating_system") or "").strip()
                if detected_platform:
                    row["detected_platform"] = detected_platform
                if operating_system:
                    row["operating_system"] = operating_system
            timeline = timelines.get(int(row["id"]), [])
            created_at = int(row.get("created_at") or 0)
            if created_at > 0:
                timeline = [
                    event
                    for event in timeline
                    if int(event.get("started_at") or created_at) >= created_at - 300
                ]
            timeline = _compact_onboarding_timeline(timeline)
            row["timeline"] = timeline
            row["events"] = timeline
        return rows

    def _load_onboarding_target_event_rows(self, target_row_ids: Sequence[int]) -> Dict[int, List[Dict[str, Any]]]:
        row_ids = [int(row_id) for row_id in target_row_ids if int(row_id or 0) > 0]
        if not row_ids:
            return {}
        placeholders = ",".join(["?"] * len(row_ids))
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT
                    id,
                    target_row_id,
                    run_id,
                    job_id,
                    status,
                    task,
                    detail,
                    stdout_snippet,
                    stderr_snippet,
                    started_at,
                    finished_at,
                    created_at,
                    updated_at
                  FROM scheduled_job_onboarding_target_events
                 WHERE target_row_id IN ({placeholders})
              ORDER BY target_row_id ASC, id ASC
                """,
                tuple(row_ids),
            )
            timelines: Dict[int, List[Dict[str, Any]]] = {row_id: [] for row_id in row_ids}
            for row in cur.fetchall():
                target_row_id = int(row[1])
                timelines.setdefault(target_row_id, []).append(
                    {
                        "id": int(row[0]),
                        "target_row_id": target_row_id,
                        "run_id": int(row[2]),
                        "job_id": int(row[3]),
                        "status": row[4] or "",
                        "task": row[5] or "",
                        "detail": row[6] or "",
                        "stdout_snippet": row[7] or "",
                        "stderr_snippet": row[8] or "",
                        "started_at": row[9],
                        "finished_at": row[10],
                        "created_at": row[11],
                        "updated_at": row[12],
                    }
                )
            return timelines
        finally:
            conn.close()

    def _record_onboarding_target_event(
        self,
        cur,
        *,
        row_id: int,
        status: str,
        detail: str = "",
        stdout: str = "",
        stderr: str = "",
        now: Optional[int] = None,
        finished: bool = False,
    ) -> None:
        target_row_id = int(row_id or 0)
        if target_row_id <= 0:
            return
        timestamp = int(now or _now_ts())
        progress_status = _onboarding_progress_status(status)
        task = _onboarding_progress_task(status=status, detail=detail, stdout=stdout, stderr=stderr)
        insert_finished = bool(finished) or not _onboarding_progress_status_is_active(progress_status)
        cur.execute(
            "SELECT run_id, job_id FROM scheduled_job_onboarding_targets WHERE id=?",
            (target_row_id,),
        )
        target_row = cur.fetchone()
        if not target_row:
            return
        run_id = int(target_row[0])
        job_id = int(target_row[1])
        cur.execute(
            """
            SELECT id, status, task, finished_at
              FROM scheduled_job_onboarding_target_events
             WHERE target_row_id=?
          ORDER BY id DESC
             LIMIT 1
            """,
            (target_row_id,),
        )
        previous = cur.fetchone()
        if previous and str(previous[2] or "") == task:
            cur.execute(
                """
                UPDATE scheduled_job_onboarding_target_events
                   SET status=?,
                       detail=?,
                       stdout_snippet=?,
                       stderr_snippet=?,
                       finished_at=?,
                       updated_at=?
                 WHERE id=?
                """,
                (
                    progress_status,
                    detail,
                    stdout,
                    stderr,
                    timestamp if insert_finished else previous[3],
                    timestamp,
                    int(previous[0]),
                ),
            )
            return

        if previous and previous[3] is None:
            previous_status = str(previous[1] or "")
            closing_status = "completed" if _onboarding_progress_status_is_active(previous_status) else previous_status
            cur.execute(
                """
                UPDATE scheduled_job_onboarding_target_events
                   SET status=?,
                       finished_at=?,
                       updated_at=?
                 WHERE id=?
                """,
                (closing_status, timestamp, timestamp, int(previous[0])),
            )

        cur.execute(
            """
            INSERT INTO scheduled_job_onboarding_target_events(
                target_row_id,
                run_id,
                job_id,
                status,
                task,
                detail,
                stdout_snippet,
                stderr_snippet,
                started_at,
                finished_at,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                target_row_id,
                run_id,
                job_id,
                progress_status,
                task,
                detail,
                stdout,
                stderr,
                timestamp,
                timestamp if insert_finished else None,
                timestamp,
                timestamp,
            ),
        )

    def _update_onboarding_target_row(
        self,
        row_id: int,
        *,
        status: str,
        detail: str = "",
        stdout: str = "",
        stderr: str = "",
        approval_reference: str = "",
        target_hostname: Optional[str] = None,
        finished: bool = False,
        redactions: Optional[Sequence[Any]] = None,
        event_timestamp: Optional[int] = None,
        event_status: Optional[str] = None,
    ) -> None:
        now = _now_ts()
        event_now = now
        if event_timestamp is not None:
            try:
                parsed_event_now = int(event_timestamp)
                if parsed_event_now > 0:
                    event_now = parsed_event_now
            except Exception:
                event_now = now
        clean_detail = sanitize_output(detail, redactions=redactions, limit=500)
        clean_stdout = sanitize_output(stdout, redactions=redactions)
        clean_stderr = sanitize_output(stderr, redactions=redactions)
        clean_target_hostname = _clean_onboarding_reported_hostname(target_hostname)
        conn = self._conn()
        try:
            cur = conn.cursor()
            if clean_target_hostname:
                cur.execute(
                    """
                    UPDATE scheduled_job_onboarding_targets
                       SET status=?,
                           detail=?,
                           stdout_snippet=?,
                           stderr_snippet=?,
                           approval_reference=?,
                           target_hostname=?,
                           updated_at=?,
                           finished_at=?
                     WHERE id=?
                    """,
                    (
                        str(status or ""),
                        clean_detail,
                        clean_stdout,
                        clean_stderr,
                        str(approval_reference or ""),
                        clean_target_hostname,
                        now,
                        now if finished else None,
                        int(row_id),
                    ),
                )
            else:
                cur.execute(
                    """
                    UPDATE scheduled_job_onboarding_targets
                       SET status=?,
                           detail=?,
                           stdout_snippet=?,
                           stderr_snippet=?,
                           approval_reference=?,
                           updated_at=?,
                           finished_at=?
                     WHERE id=?
                    """,
                    (
                        str(status or ""),
                        clean_detail,
                        clean_stdout,
                        clean_stderr,
                        str(approval_reference or ""),
                        now,
                        now if finished else None,
                        int(row_id),
                    ),
                )
            self._record_onboarding_target_event(
                cur,
                row_id=int(row_id),
                status=str(event_status or status or ""),
                detail=clean_detail,
                stdout=clean_stdout,
                stderr=clean_stderr,
                now=event_now,
                finished=finished,
            )
            conn.commit()
        finally:
            conn.close()

    def _set_onboarding_target_hostname(self, row_id: int, target_hostname: Any) -> None:
        clean_target_hostname = _clean_onboarding_reported_hostname(target_hostname)
        if not clean_target_hostname:
            return
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE scheduled_job_onboarding_targets
                   SET target_hostname=?,
                       updated_at=?
                 WHERE id=?
                """,
                (clean_target_hostname, _now_ts(), int(row_id)),
            )
            conn.commit()
        finally:
            conn.close()

    def _set_onboarding_target_hostnames(self, hostnames_by_row_id: Mapping[int, Any]) -> None:
        cleaned: Dict[int, str] = {}
        for row_id, target_hostname in dict(hostnames_by_row_id or {}).items():
            clean_target_hostname = _clean_onboarding_reported_hostname(target_hostname)
            if not clean_target_hostname:
                continue
            try:
                clean_row_id = int(row_id)
            except Exception:
                continue
            if clean_row_id > 0:
                cleaned[clean_row_id] = clean_target_hostname
        if not cleaned:
            return
        conn = self._conn()
        try:
            cur = conn.cursor()
            now = _now_ts()
            for row_id, target_hostname in cleaned.items():
                cur.execute(
                    """
                    UPDATE scheduled_job_onboarding_targets
                       SET target_hostname=?,
                           updated_at=?
                     WHERE id=?
                       AND (
                            COALESCE(target_hostname, '')=''
                            OR LOWER(COALESCE(target_hostname, ''))=LOWER(COALESCE(target_address, ''))
                            OR LOWER(COALESCE(target_hostname, ''))=LOWER(COALESCE(target_input, ''))
                       )
                    """,
                    (target_hostname, now, int(row_id)),
                )
            conn.commit()
        finally:
            conn.close()

    def _bulk_lookup_known_onboarding_target_metadata(self, rows: Sequence[Mapping[str, Any]]) -> Dict[int, Dict[str, str]]:
        row_site_ids: Dict[int, Optional[int]] = {}
        value_to_row_ids: Dict[str, List[int]] = {}
        for row in rows or []:
            try:
                row_id = int(row.get("id") or 0)
            except Exception:
                continue
            if row_id <= 0:
                continue
            try:
                row_site_ids[row_id] = int(row.get("site_id")) if row.get("site_id") is not None else None
            except Exception:
                row_site_ids[row_id] = None
            seen_values: set[str] = set()
            for field in ("target_address", "target_hostname", "target_input"):
                value = _onboarding_target_without_port(row.get(field))
                if not value:
                    continue
                key = value.strip().lower()
                if not key or key in seen_values:
                    continue
                seen_values.add(key)
                value_to_row_ids.setdefault(key, []).append(row_id)
        if not value_to_row_ids:
            return {}

        resolved: Dict[int, Dict[str, str]] = {}

        def _site_matches(row_id: int, found_site_id: Any) -> bool:
            desired_site_id = row_site_ids.get(int(row_id))
            if desired_site_id is None or found_site_id is None:
                return True
            try:
                return int(desired_site_id) == int(found_site_id)
            except Exception:
                return True

        def _assign(
            key: Any,
            hostname: Any,
            found_site_id: Any = None,
            operating_system: Any = "",
            connection_type: Any = "",
        ) -> None:
            lookup_key = str(key or "").strip().lower()
            clean_hostname = _clean_onboarding_reported_hostname(hostname)
            detected_platform = _infer_onboarding_platform_from_inventory(operating_system, connection_type)
            if not lookup_key or (not clean_hostname and not detected_platform):
                return
            for row_id in value_to_row_ids.get(lookup_key, []):
                if row_id in resolved:
                    continue
                if not _site_matches(row_id, found_site_id):
                    continue
                resolved[row_id] = {
                    "hostname": clean_hostname,
                    "operating_system": str(operating_system or "").strip(),
                    "detected_platform": detected_platform,
                }

        values = list(value_to_row_ids.keys())
        conn = self._conn()
        try:
            cur = conn.cursor()
            chunk_size = 300
            for start in range(0, len(values), chunk_size):
                chunk = values[start : start + chunk_size]
                placeholders = ",".join(["?"] * len(chunk))
                cur.execute(
                    f"""
                    SELECT d.hostname,
                           d.internal_ip,
                           d.external_ip,
                           d.operating_system,
                           d.connection_type,
                           ds.site_id
                      FROM devices d
                 LEFT JOIN device_sites ds
                        ON ds.device_hostname = d.hostname
                     WHERE LOWER(COALESCE(d.hostname, '')) IN ({placeholders})
                        OR LOWER(COALESCE(d.internal_ip, '')) IN ({placeholders})
                        OR LOWER(COALESCE(d.external_ip, '')) IN ({placeholders})
                    """,
                    tuple([*chunk, *chunk, *chunk]),
                )
                for hostname, internal_ip, external_ip, operating_system, connection_type, found_site_id in cur.fetchall():
                    for key in (internal_ip, external_ip, hostname):
                        _assign(key, hostname, found_site_id, operating_system, connection_type)
            unresolved_values = [
                value
                for value, row_ids in value_to_row_ids.items()
                if any(row_id not in resolved for row_id in row_ids)
            ]
            for start in range(0, len(unresolved_values), chunk_size):
                chunk = unresolved_values[start : start + chunk_size]
                if not chunk:
                    continue
                placeholders = ",".join(["?"] * len(chunk))
                cur.execute(
                    f"""
                    SELECT hostname_claimed,
                           onboarding_target,
                           da.site_id,
                           d.operating_system,
                           d.connection_type
                      FROM device_approvals da
                 LEFT JOIN devices d
                        ON LOWER(COALESCE(d.hostname, ''))=LOWER(COALESCE(da.hostname_claimed, ''))
                     WHERE (
                            LOWER(COALESCE(da.status, '')) = 'pending'
                            OR TRIM(COALESCE(d.hostname, '')) != ''
                       )
                       AND TRIM(COALESCE(da.hostname_claimed, '')) != ''
                       AND (
                            LOWER(COALESCE(da.hostname_claimed, '')) IN ({placeholders})
                            OR LOWER(COALESCE(da.onboarding_target, '')) IN ({placeholders})
                       )
                     ORDER BY da.updated_at DESC, da.created_at DESC
                    """,
                    tuple([*chunk, *chunk]),
                )
                for hostname, onboarding_target, found_site_id, operating_system, connection_type in cur.fetchall():
                    for key in (onboarding_target, hostname):
                        _assign(key, hostname, found_site_id, operating_system, connection_type)
        except Exception:
            return resolved
        finally:
            conn.close()
        return resolved

    def _bulk_lookup_known_onboarding_target_hostnames(self, rows: Sequence[Mapping[str, Any]]) -> Dict[int, str]:
        metadata = self._bulk_lookup_known_onboarding_target_metadata(rows)
        return {
            int(row_id): _clean_onboarding_reported_hostname(value.get("hostname") if isinstance(value, Mapping) else "")
            for row_id, value in metadata.items()
            if _clean_onboarding_reported_hostname(value.get("hostname") if isinstance(value, Mapping) else "")
        }

    def _lookup_known_onboarding_target_hostname(self, host: str, site_id: Optional[int]) -> str:
        value = _onboarding_target_without_port(host)
        if not value:
            return ""
        lookup_value = value.lower()
        conn = self._conn()
        try:
            cur = conn.cursor()
            params: List[Any] = [lookup_value, lookup_value, lookup_value]
            sql = """
                SELECT d.hostname
                  FROM devices d
             LEFT JOIN device_sites ds
                    ON ds.device_hostname = d.hostname
                 WHERE (
                        LOWER(COALESCE(d.hostname, '')) = ?
                        OR LOWER(COALESCE(d.internal_ip, '')) = ?
                        OR LOWER(COALESCE(d.external_ip, '')) = ?
                       )
                   AND TRIM(COALESCE(d.hostname, '')) != ''
            """
            if site_id is not None:
                sql += " AND (ds.site_id = ? OR ds.site_id IS NULL)"
                params.append(int(site_id))
            sql += " ORDER BY CASE WHEN LOWER(COALESCE(d.internal_ip, '')) = ? THEN 0 ELSE 1 END LIMIT 1"
            params.append(lookup_value)
            cur.execute(sql, tuple(params))
            row = cur.fetchone()
            hostname = _clean_onboarding_reported_hostname(row[0] if row else "")
            if hostname:
                return hostname

            approval_params: List[Any] = [lookup_value, lookup_value]
            approval_sql = """
                SELECT da.hostname_claimed
                  FROM device_approvals da
             LEFT JOIN devices d
                    ON LOWER(COALESCE(d.hostname, ''))=LOWER(COALESCE(da.hostname_claimed, ''))
                 WHERE (
                        LOWER(COALESCE(da.status, '')) = 'pending'
                        OR TRIM(COALESCE(d.hostname, '')) != ''
                   )
                   AND (
                        LOWER(COALESCE(da.hostname_claimed, '')) = ?
                        OR LOWER(COALESCE(da.onboarding_target, '')) = ?
                   )
                   AND TRIM(COALESCE(da.hostname_claimed, '')) != ''
            """
            if site_id is not None:
                approval_sql += " AND (da.site_id = ? OR da.site_id IS NULL)"
                approval_params.append(int(site_id))
            approval_sql += " ORDER BY da.updated_at DESC, da.created_at DESC LIMIT 1"
            cur.execute(approval_sql, tuple(approval_params))
            row = cur.fetchone()
            return _clean_onboarding_reported_hostname(row[0] if row else "")
        except Exception:
            return ""
        finally:
            conn.close()

    def _lookup_known_onboarding_target_hostname_from_candidates(
        self,
        candidates: Sequence[Any],
        site_id: Optional[int],
    ) -> str:
        for candidate in candidates:
            hostname = self._lookup_known_onboarding_target_hostname(str(candidate or ""), site_id)
            if hostname:
                return hostname
        return ""

    def _lookup_active_onboarding_target_hostname(self, host: str, site_id: Optional[int]) -> str:
        value = _onboarding_target_without_port(host)
        if not value:
            return ""
        lookup_value = value.lower()
        active_cutoff = _now_ts() - (24 * 60 * 60)
        conn = self._conn()
        try:
            cur = conn.cursor()
            params: List[Any] = [lookup_value, lookup_value, lookup_value, active_cutoff]
            sql = """
                SELECT d.hostname
                  FROM devices d
             LEFT JOIN device_sites ds
                    ON ds.device_hostname = d.hostname
                 WHERE (
                        LOWER(COALESCE(d.hostname, '')) = ?
                        OR LOWER(COALESCE(d.internal_ip, '')) = ?
                        OR LOWER(COALESCE(d.external_ip, '')) = ?
                       )
                   AND TRIM(COALESCE(d.hostname, '')) != ''
                   AND LOWER(COALESCE(d.status, '')) IN ('active', 'online', 'running', 'connected')
                   AND COALESCE(d.last_seen, 0) >= ?
            """
            if site_id is not None:
                sql += " AND (ds.site_id = ? OR ds.site_id IS NULL)"
                params.append(int(site_id))
            sql += " ORDER BY COALESCE(d.last_seen, 0) DESC LIMIT 1"
            cur.execute(sql, tuple(params))
            row = cur.fetchone()
            return _clean_onboarding_reported_hostname(row[0] if row else "")
        except Exception:
            return ""
        finally:
            conn.close()

    def _lookup_active_onboarding_target_hostname_from_candidates(
        self,
        candidates: Sequence[Any],
        site_id: Optional[int],
    ) -> str:
        for candidate in candidates:
            hostname = self._lookup_active_onboarding_target_hostname(str(candidate or ""), site_id)
            if hostname:
                return hostname
        return ""

    def _onboarding_target_already_known(self, host: str, site_id: Optional[int]) -> bool:
        if self._lookup_known_onboarding_target_hostname(host, site_id):
            return True
        value = _onboarding_target_without_port(host)
        if not value:
            return False
        lookup_value = value.lower()
        conn = self._conn()
        try:
            cur = conn.cursor()
            params: List[Any] = [lookup_value, lookup_value, lookup_value]
            sql = """
                SELECT 1
                  FROM devices d
             LEFT JOIN device_sites ds
                    ON ds.device_hostname = d.hostname
                 WHERE (
                        LOWER(COALESCE(d.hostname, '')) = ?
                        OR LOWER(COALESCE(d.internal_ip, '')) = ?
                        OR LOWER(COALESCE(d.external_ip, '')) = ?
                       )
            """
            if site_id is not None:
                sql += " AND (ds.site_id = ? OR ds.site_id IS NULL)"
                params.append(int(site_id))
            sql += " LIMIT 1"
            cur.execute(sql, tuple(params))
            if cur.fetchone() is not None:
                return True
            approval_params: List[Any] = [lookup_value, lookup_value]
            approval_sql = """
                SELECT 1
                  FROM device_approvals
                 WHERE LOWER(COALESCE(status, '')) = 'pending'
                   AND (
                        LOWER(COALESCE(hostname_claimed, '')) = ?
                        OR LOWER(COALESCE(onboarding_target, '')) = ?
                   )
            """
            if site_id is not None:
                approval_sql += " AND (site_id = ? OR site_id IS NULL)"
                approval_params.append(int(site_id))
            approval_sql += " LIMIT 1"
            cur.execute(approval_sql, tuple(approval_params))
            return cur.fetchone() is not None
        except Exception:
            return False
        finally:
            conn.close()

    def _lookup_onboarding_approval(self, *, job_id: int, run_id: int, target: str) -> str:
        context = self._lookup_onboarding_approval_context(job_id=job_id, run_id=run_id, target=target)
        return str(context.get("approval_reference") or "").strip()

    def _lookup_onboarding_approval_context(
        self,
        *,
        job_id: int,
        run_id: int,
        target: str,
        approval_reference: str = "",
        site_id: Optional[int] = None,
    ) -> Dict[str, Any]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            normalized_reference = str(approval_reference or "").strip()
            if normalized_reference:
                cur.execute(
                    """
                    SELECT id,
                           approval_reference,
                           status,
                           hostname_claimed,
                           updated_at,
                           approved_by_user_id,
                           onboarding_job_id,
                           onboarding_run_id,
                           onboarding_target
                      FROM device_approvals
                     WHERE approval_reference=?
                  ORDER BY updated_at DESC
                     LIMIT 1
                    """,
                    (normalized_reference,),
                )
            else:
                normalized_target = str(target or "").strip()
                target_port_pattern = f"{normalized_target}:%" if normalized_target else ""
                cur.execute(
                    """
                    SELECT id,
                           approval_reference,
                           status,
                           hostname_claimed,
                           updated_at,
                           approved_by_user_id,
                           onboarding_job_id,
                           onboarding_run_id,
                           onboarding_target
                      FROM device_approvals
                     WHERE onboarding_job_id=?
                       AND onboarding_run_id=?
                       AND (
                            LOWER(COALESCE(onboarding_target, ''))=LOWER(?)
                            OR (?<>'' AND LOWER(COALESCE(onboarding_target, '')) LIKE LOWER(?))
                       )
                  ORDER BY updated_at DESC
                     LIMIT 1
                    """,
                    (int(job_id), int(run_id), normalized_target, target_port_pattern, target_port_pattern),
                )
            row = cur.fetchone()
            if not row and not normalized_reference:
                normalized_target = str(target or "").strip()
                target_port_pattern = f"{normalized_target}:%" if normalized_target else ""
                fallback_params: List[Any] = [normalized_target, normalized_target, normalized_target, target_port_pattern, target_port_pattern, normalized_target, normalized_target]
                fallback_site_clause = ""
                if site_id is not None:
                    fallback_site_clause = " AND (da.site_id=? OR da.site_id IS NULL)"
                    fallback_params.append(int(site_id))
                cur.execute(
                    f"""
                    SELECT da.id,
                           da.approval_reference,
                           da.status,
                           da.hostname_claimed,
                           da.updated_at,
                           da.approved_by_user_id,
                           da.onboarding_job_id,
                           da.onboarding_run_id,
                           da.onboarding_target
                      FROM device_approvals AS da
                 LEFT JOIN devices AS d
                        ON LOWER(COALESCE(d.hostname, ''))=LOWER(COALESCE(da.hostname_claimed, ''))
                        OR LOWER(COALESCE(d.guid, ''))=LOWER(COALESCE(da.guid, ''))
                     WHERE (
                            LOWER(COALESCE(da.onboarding_target, ''))=LOWER(?)
                            OR LOWER(COALESCE(da.hostname_claimed, ''))=LOWER(?)
                            OR LOWER(COALESCE(d.hostname, ''))=LOWER(?)
                            OR (?<>'' AND LOWER(COALESCE(da.onboarding_target, '')) LIKE LOWER(?))
                            OR LOWER(COALESCE(d.internal_ip, ''))=LOWER(?)
                            OR LOWER(COALESCE(d.external_ip, ''))=LOWER(?)
                           )
                       {fallback_site_clause}
                       AND LOWER(COALESCE(da.status, ''))='pending'
                  ORDER BY da.updated_at DESC
                     LIMIT 1
                    """,
                    tuple(fallback_params),
                )
                row = cur.fetchone()
            if not row:
                return {}
            if normalized_reference:
                try:
                    reference_job_id = int(row[6]) if row[6] is not None else 0
                except Exception:
                    reference_job_id = 0
                try:
                    reference_run_id = int(row[7]) if row[7] is not None else 0
                except Exception:
                    reference_run_id = 0
                if reference_job_id != int(job_id) or reference_run_id != int(run_id):
                    reference_status = str(row[2] or "").strip().lower()
                    if reference_status != "pending":
                        return {
                            "approval_reference_stale": normalized_reference,
                            "approval_status": reference_status,
                            "approval_hostname": row[3] or "",
                            "approval_job_id": reference_job_id,
                            "approval_run_id": reference_run_id,
                            "approval_target": row[8] or "",
                        }
            return {
                "approval_id": row[0] or "",
                "approval_reference": row[1] or "",
                "approval_status": row[2] or "",
                "approval_hostname": row[3] or "",
                "approval_updated_at": row[4] or "",
                "approved_by_user_id": row[5] or "",
                "approval_job_id": row[6] or "",
                "approval_run_id": row[7] or "",
                "approval_target": row[8] or "",
            }
        except Exception:
            return {}
        finally:
            conn.close()

    def _update_onboarding_target_approval_reference(self, row_id: int, approval_reference: str) -> None:
        value = str(approval_reference or "").strip()
        if not value:
            return
        conn = self._conn()
        try:
            cur = conn.cursor()
            now = _now_ts()
            cur.execute(
                """
                UPDATE scheduled_job_onboarding_targets
                   SET approval_reference=?,
                       updated_at=?
                 WHERE id=?
                   AND COALESCE(approval_reference, '')=''
                """,
                (value, now, int(row_id)),
            )
            conn.commit()
        finally:
            conn.close()

    def _backfill_onboarding_target_approval_references(self, rows: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
        hydrated: List[Dict[str, Any]] = []
        known_hostnames_by_row_id = self._bulk_lookup_known_onboarding_target_hostnames(rows)
        hostname_updates: Dict[int, str] = {}
        for row in rows or []:
            try:
                row_id = int(row.get("id") or 0)
            except Exception:
                continue
            if row_id <= 0:
                continue
            if _clean_onboarding_reported_hostname(row.get("target_hostname")):
                continue
            known_hostname = known_hostnames_by_row_id.get(row_id, "")
            if known_hostname:
                hostname_updates[row_id] = known_hostname
        if hostname_updates:
            self._set_onboarding_target_hostnames(hostname_updates)
        for row in rows:
            next_row = dict(row)
            row_id = int(next_row.get("id") or 0)
            status = str(next_row.get("status") or "").strip().lower()
            detail_current = str(next_row.get("detail") or "").strip()
            approval_reference_current = str(next_row.get("approval_reference") or "").strip()
            site_id = int(next_row.get("site_id")) if next_row.get("site_id") is not None else None
            existing_target_hostname = _clean_onboarding_reported_hostname(next_row.get("target_hostname"))
            if not existing_target_hostname:
                known_hostname = known_hostnames_by_row_id.get(row_id, "")
                if known_hostname:
                    next_row["target_hostname"] = known_hostname
            local_bootstrap_completed = _windows_onboarding_local_bootstrap_completed(
                stdout=next_row.get("stdout_snippet") or "",
                stderr=next_row.get("stderr_snippet") or "",
                detail=detail_current,
            )
            if _windows_onboarding_existing_task_running_without_redeploy(
                stdout=next_row.get("stdout_snippet") or "",
                stderr=next_row.get("stderr_snippet") or "",
            ):
                known_candidates = [
                    next_row.get("target_address"),
                    next_row.get("target_hostname"),
                    next_row.get("target_input"),
                ]
                known_hostname = _clean_onboarding_reported_hostname(next_row.get("target_hostname")) or known_hostnames_by_row_id.get(row_id, "")
                if known_hostname or any(self._onboarding_target_already_known(str(candidate or ""), site_id) for candidate in known_candidates):
                    skipped_detail = "Existing Borealis Agent is already enrolled and active."
                    next_row["status"] = "already_enrolled"
                    next_row["detail"] = skipped_detail
                    next_row["approval_reference"] = ""
                    if known_hostname:
                        next_row["target_hostname"] = known_hostname
                    self._update_onboarding_target_row(
                        row_id,
                        status="already_enrolled",
                        detail=skipped_detail,
                        stdout=next_row.get("stdout_snippet") or "",
                        stderr=next_row.get("stderr_snippet") or "",
                        approval_reference="",
                        target_hostname=known_hostname,
                        finished=True,
                    )
                    timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                    next_row["timeline"] = timeline
                    next_row["events"] = timeline
                    hydrated.append(next_row)
                    continue
            if status in {
                ONBOARDING_STATUS_PENDING,
                ONBOARDING_STATUS_RUNNING,
                ONBOARDING_STATUS_WAITING_APPROVAL,
                "approved",
                "completed",
            } or approval_reference_current or (status == ONBOARDING_STATUS_FAILED and local_bootstrap_completed):
                approval_context: Dict[str, Any] = {}
                lookup_candidates = _onboarding_approval_lookup_candidates(next_row) or [""]
                for target in lookup_candidates:
                    approval_context = self._lookup_onboarding_approval_context(
                        job_id=int(next_row.get("job_id") or 0),
                        run_id=int(next_row.get("run_id") or 0),
                        target=target,
                        approval_reference=approval_reference_current,
                        site_id=site_id,
                    )
                    if approval_context:
                        break
                if (
                    status == ONBOARDING_STATUS_FAILED
                    and local_bootstrap_completed
                    and not approval_context
                    and not approval_reference_current
                ):
                    row_id = int(next_row.get("id") or 0)
                    known_candidates = [
                        next_row.get("target_address"),
                        next_row.get("target_hostname"),
                        next_row.get("target_input"),
                    ]
                    known_hostname = _clean_onboarding_reported_hostname(next_row.get("target_hostname")) or known_hostnames_by_row_id.get(row_id, "")
                    if known_hostname or any(self._onboarding_target_already_known(str(candidate or ""), site_id) for candidate in known_candidates):
                        completed_detail = "Device approved and enrollment completed."
                        next_row["status"] = "completed"
                        next_row["detail"] = completed_detail
                        next_row["approval_reference"] = ""
                        if known_hostname:
                            next_row["target_hostname"] = known_hostname
                        self._update_onboarding_target_row(
                            row_id,
                            status="completed",
                            detail=completed_detail,
                            stdout=next_row.get("stdout_snippet") or "",
                            stderr=next_row.get("stderr_snippet") or "",
                            approval_reference="",
                            target_hostname=known_hostname,
                            finished=True,
                        )
                        timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                        next_row["timeline"] = timeline
                        next_row["events"] = timeline
                        hydrated.append(next_row)
                        continue
                if (
                    status == ONBOARDING_STATUS_WAITING_APPROVAL
                    and not approval_context
                    and not approval_reference_current
                ):
                    row_id = int(next_row.get("id") or 0)
                    known_candidates = [
                        next_row.get("target_address"),
                        next_row.get("target_hostname"),
                        next_row.get("target_input"),
                    ]
                    active_hostname = self._lookup_active_onboarding_target_hostname_from_candidates(known_candidates, site_id)
                    if active_hostname:
                        skipped_detail = "Existing Borealis Agent is already enrolled and active."
                        next_row["status"] = "already_enrolled"
                        next_row["detail"] = skipped_detail
                        next_row["approval_reference"] = ""
                        next_row["target_hostname"] = active_hostname
                        self._update_onboarding_target_row(
                            row_id,
                            status="already_enrolled",
                            detail=skipped_detail,
                            stdout=next_row.get("stdout_snippet") or "",
                            stderr=next_row.get("stderr_snippet") or "",
                            approval_reference="",
                            target_hostname=active_hostname,
                            finished=True,
                        )
                        timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                        next_row["timeline"] = timeline
                        next_row["events"] = timeline
                        hydrated.append(next_row)
                        continue
                    failure_detail = "Agent completed local bootstrap, but Borealis Engine did not receive an approval request."
                    stderr_current = str(next_row.get("stderr_snippet") or "")
                    if "windows_onboarding_approval_callback_timeout" not in stderr_current:
                        stderr_current = "\n\n".join(
                            [part for part in (stderr_current, "windows_onboarding_approval_callback_timeout") if part]
                        )
                    next_row["status"] = ONBOARDING_STATUS_FAILED
                    next_row["detail"] = failure_detail
                    next_row["approval_reference"] = ""
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_FAILED,
                        detail=failure_detail,
                        stdout=next_row.get("stdout_snippet") or "",
                        stderr=stderr_current,
                        approval_reference="",
                        finished=True,
                    )
                    timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                    next_row["timeline"] = timeline
                    next_row["events"] = timeline
                    hydrated.append(next_row)
                    continue
                if approval_context.get("approval_reference_stale"):
                    row_id = int(next_row.get("id") or 0)
                    known_candidates = [
                        next_row.get("target_address"),
                        next_row.get("target_hostname"),
                        next_row.get("target_input"),
                        approval_context.get("approval_target"),
                        approval_context.get("approval_hostname"),
                    ]
                    known_hostname = (
                        _clean_onboarding_reported_hostname(next_row.get("target_hostname"))
                        or _clean_onboarding_reported_hostname(approval_context.get("approval_hostname"))
                        or known_hostnames_by_row_id.get(row_id, "")
                    )
                    if known_hostname or any(self._onboarding_target_already_known(str(candidate or ""), site_id) for candidate in known_candidates):
                        skipped_detail = "Existing Borealis Agent is already enrolled and active."
                        next_row["status"] = "already_enrolled"
                        next_row["detail"] = skipped_detail
                        next_row["approval_reference"] = ""
                        if known_hostname:
                            next_row["target_hostname"] = known_hostname
                        self._update_onboarding_target_row(
                            row_id,
                            status="already_enrolled",
                            detail=skipped_detail,
                            stdout=next_row.get("stdout_snippet") or "",
                            stderr=next_row.get("stderr_snippet") or "",
                            approval_reference="",
                            target_hostname=known_hostname,
                            finished=True,
                        )
                        timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                        next_row["timeline"] = timeline
                        next_row["events"] = timeline
                    else:
                        stale_detail = "Approval callback matched a previous onboarding run, but no active pending approval or enrolled device matches this target."
                        next_row["status"] = ONBOARDING_STATUS_FAILED
                        next_row["detail"] = stale_detail
                        next_row["approval_reference"] = ""
                        self._update_onboarding_target_row(
                            row_id,
                            status=ONBOARDING_STATUS_FAILED,
                            detail=stale_detail,
                            stdout=next_row.get("stdout_snippet") or "",
                            stderr=next_row.get("stderr_snippet") or "",
                            approval_reference="",
                            finished=True,
                        )
                        timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                        next_row["timeline"] = timeline
                        next_row["events"] = timeline
                    hydrated.append(next_row)
                    continue
                next_row.update(approval_context)
                approval_hostname = _clean_onboarding_reported_hostname(approval_context.get("approval_hostname"))
                if approval_hostname and not _clean_onboarding_reported_hostname(next_row.get("target_hostname")):
                    next_row["target_hostname"] = approval_hostname
                    self._set_onboarding_target_hostname(int(next_row.get("id") or 0), approval_hostname)
                approval_reference = str(approval_context.get("approval_reference") or "").strip()
                if approval_reference:
                    next_row["approval_reference"] = approval_reference
                    self._update_onboarding_target_approval_reference(int(next_row.get("id") or 0), approval_reference)
                approval_status = str(approval_context.get("approval_status") or "").strip().lower()
                approval_detail = ""
                if approval_status == "pending":
                    next_row["status"] = ONBOARDING_STATUS_WAITING_APPROVAL
                    approval_detail = "Agent ready and awaiting approval."
                    next_row["detail"] = approval_detail
                elif approval_status == "approved":
                    next_row["status"] = "approved"
                    approval_detail = "Device approved. Agent finalizing enrollment."
                    next_row["detail"] = approval_detail
                elif approval_status == "completed":
                    next_row["status"] = "completed"
                    approval_detail = "Device approved and enrollment completed."
                    next_row["detail"] = approval_detail
                elif approval_status in {"denied", "expired"}:
                    next_row["status"] = approval_status
                    approval_detail = f"Device approval {approval_status}."
                    next_row["detail"] = approval_detail
                if approval_detail:
                    row_id = int(next_row.get("id") or 0)
                    next_status = str(next_row.get("status") or approval_status)
                    should_update_row = (
                        status != next_status
                        or detail_current != approval_detail
                        or bool(approval_reference and approval_reference != approval_reference_current)
                    )
                    if should_update_row:
                        self._update_onboarding_target_row(
                            row_id,
                            status=next_status,
                            detail=approval_detail,
                            stdout=next_row.get("stdout_snippet") or "",
                            stderr=next_row.get("stderr_snippet") or "",
                            approval_reference=approval_reference or str(next_row.get("approval_reference") or ""),
                            target_hostname=approval_hostname,
                            finished=approval_status in {"pending", "approved", "completed", "denied", "expired"},
                        )
                        timeline = self._load_onboarding_target_event_rows([row_id]).get(row_id, [])
                        next_row["timeline"] = timeline
                        next_row["events"] = timeline
            hydrated.append(next_row)
        return hydrated

    def _remote_onboarding_command(
        self,
        *,
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
    ) -> str:
        agent_url = f"https://raw.githubusercontent.com/bunny-lab-io/Borealis/{branch}/Agent.sh"
        env_parts = [
            f"BOREALIS_ONBOARDING_JOB_ID={shlex.quote(str(int(job_id)))}",
            f"BOREALIS_ONBOARDING_RUN_ID={shlex.quote(str(int(run_id)))}",
            f"BOREALIS_ONBOARDING_TARGET={shlex.quote(str(target or '').strip())}",
            "BOREALIS_AGENT_NONINTERACTIVE=1",
            "BOREALIS_AGENT_NO_TTY=1",
        ]
        args = [
            "--repo-branch",
            branch,
            "deploy",
            "--serverurl",
            server_url,
            "--enrollmentcode",
            enrollment_code,
            "--newEngine",
        ]
        quoted_args = " ".join(shlex.quote(str(arg)) for arg in args)
        quoted_url = shlex.quote(agent_url)
        env_prefix = " ".join(env_parts)
        sudo_marker = "__BOREALIS_ONBOARDING_SUDO_PROMPT__"
        return "\n".join(
            [
                "set -eu",
                "detected_os=\"$(uname -s 2>/dev/null || true)\"",
                "if [ \"$detected_os\" != \"Linux\" ]; then echo \"__BOREALIS_UNSUPPORTED_OS__=${detected_os:-unknown}\" >&2; exit 42; fi",
                "tmp_file=\"$(mktemp /tmp/borealis-agent.XXXXXX)\"",
                "cleanup() { rm -f \"$tmp_file\"; }",
                "trap cleanup EXIT",
                f"if command -v curl >/dev/null 2>&1; then curl -fsSL {quoted_url} -o \"$tmp_file\"; "
                f"elif command -v wget >/dev/null 2>&1; then wget -qO \"$tmp_file\" {quoted_url}; "
                "else echo 'curl_or_wget_required' >&2; exit 43; fi",
                "chmod 700 \"$tmp_file\"",
                f"if [ \"$(id -u)\" -eq 0 ]; then env {env_prefix} bash \"$tmp_file\" {quoted_args}; "
                f"else command -v sudo >/dev/null 2>&1 || {{ echo 'sudo_required' >&2; exit 44; }}; "
                f"sudo -S -p {shlex.quote(sudo_marker)} env {env_prefix} bash \"$tmp_file\" {quoted_args}; fi",
            ]
        )

    def _execute_onboarding_ssh_command(
        self,
        *,
        host: str,
        port: int,
        username: str,
        password: str,
        private_key_text: str,
        private_key_passphrase: str,
        command: str,
        timeout_seconds: float,
        become_password: str = "",
    ) -> Dict[str, Any]:
        ssh_bin = shutil.which("ssh")
        if not ssh_bin:
            return {"exit_code": 127, "stdout": "", "stderr": "ssh_client_unavailable"}
        try:
            import pexpect  # type: ignore
        except Exception:
            return {"exit_code": 127, "stdout": "", "stderr": "ssh_probe_dependency_unavailable"}

        normalized_timeout = max(5.0, float(timeout_seconds or 0.0))
        connect_timeout = max(1, min(60, int(math.ceil(normalized_timeout))))
        login_marker = "__BOREALIS_ONBOARDING_LOGIN__"
        sudo_marker = "__BOREALIS_ONBOARDING_SUDO_PROMPT__"
        remote_command = f"printf '%s\\n' {shlex.quote(login_marker)} && /bin/sh -c {shlex.quote(command)}"

        probe_root = Path(tempfile.mkdtemp(prefix="borealis-onboarding-ssh-"))
        known_hosts_path = probe_root / "known_hosts"
        key_path = probe_root / "id_borealis_ssh"
        try:
            known_hosts_path.touch(exist_ok=True)
            try:
                os.chmod(known_hosts_path, 0o600)
            except Exception:
                pass
            args = [
                ssh_bin,
                "-T",
                "-o",
                f"ConnectTimeout={connect_timeout}",
                "-o",
                "ConnectionAttempts=1",
                "-o",
                f"UserKnownHostsFile={known_hosts_path}",
                "-o",
                "GlobalKnownHostsFile=/dev/null",
                "-o",
                "StrictHostKeyChecking=no",
                "-o",
                "UpdateHostKeys=no",
                "-o",
                "PreferredAuthentications=publickey,password,keyboard-interactive",
                "-o",
                "PubkeyAuthentication=yes",
                "-o",
                "PasswordAuthentication=yes",
                "-o",
                "KbdInteractiveAuthentication=yes",
                "-o",
                "NumberOfPasswordPrompts=2",
                "-o",
                "ServerAliveInterval=15",
                "-o",
                "ServerAliveCountMax=2",
                "-p",
                str(int(port)),
            ]
            if private_key_text:
                key_path.write_text(str(private_key_text), encoding="utf-8")
                try:
                    os.chmod(key_path, 0o600)
                except Exception:
                    pass
                args.extend(["-o", f"IdentityFile={key_path}", "-o", "IdentitiesOnly=yes"])
            elif password:
                args.extend(
                    [
                        "-o",
                        "PreferredAuthentications=password,keyboard-interactive",
                        "-o",
                        "PubkeyAuthentication=no",
                        "-o",
                        "IdentitiesOnly=yes",
                        "-o",
                        "IdentityAgent=none",
                    ]
                )
            args.append(f"{username}@{host}")
            args.append(remote_command)

            child = pexpect.spawn(args[0], args[1:], encoding="utf-8", timeout=normalized_timeout)
            stdout_parts: List[str] = []
            stderr_parts: List[str] = []
            login_seen = False
            ssh_secret_sent = False
            sudo_secret_sent = False
            patterns = [
                pexpect.EOF,
                pexpect.TIMEOUT,
                r"(?i)are you sure you want to continue connecting",
                sudo_marker,
                r"(?i)(?:password|passphrase).*:",
                login_marker,
                r"(?i)permission denied",
            ]
            while True:
                index = child.expect(patterns)
                before = str(child.before or "")
                if before:
                    if login_seen:
                        stdout_parts.append(before)
                    else:
                        stderr_parts.append(before)
                if index == 0:
                    break
                if index == 1:
                    try:
                        child.close(force=True)
                    except Exception:
                        pass
                    return {
                        "exit_code": 124,
                        "stdout": "".join(stdout_parts),
                        "stderr": "ssh_command_timeout\n" + "".join(stderr_parts),
                    }
                if index == 2:
                    child.sendline("yes")
                    continue
                if index == 3:
                    secret = str(become_password or password or "").strip()
                    if not secret or sudo_secret_sent:
                        return {
                            "exit_code": 45,
                            "stdout": "".join(stdout_parts),
                            "stderr": "sudo_password_required\n" + "".join(stderr_parts),
                        }
                    child.sendline(secret)
                    sudo_secret_sent = True
                    continue
                if index == 4:
                    if login_seen:
                        secret = str(become_password or password or "").strip()
                        if not secret or sudo_secret_sent:
                            return {
                                "exit_code": 45,
                                "stdout": "".join(stdout_parts),
                                "stderr": "sudo_password_required\n" + "".join(stderr_parts),
                            }
                        child.sendline(secret)
                        sudo_secret_sent = True
                    else:
                        secret = str(private_key_passphrase or password or "").strip()
                        if not secret or ssh_secret_sent:
                            return {
                                "exit_code": 46,
                                "stdout": "".join(stdout_parts),
                                "stderr": "ssh_password_required\n" + "".join(stderr_parts),
                            }
                        child.sendline(secret)
                        ssh_secret_sent = True
                    continue
                if index == 5:
                    login_seen = True
                    continue
                if index == 6:
                    return {
                        "exit_code": 255,
                        "stdout": "".join(stdout_parts),
                        "stderr": "permission_denied\n" + "".join(stderr_parts),
                    }
            try:
                child.close()
            except Exception:
                pass
            exit_code = child.exitstatus
            if exit_code is None:
                exit_code = 255 if child.signalstatus else 0
            return {
                "exit_code": int(exit_code),
                "stdout": "".join(stdout_parts),
                "stderr": "".join(stderr_parts),
            }
        finally:
            shutil.rmtree(str(probe_root), ignore_errors=True)

    def _detect_ssh_operating_system(
        self,
        *,
        host: str,
        port: int,
        username: str,
        password: str,
        private_key_text: str,
        private_key_passphrase: str,
        timeout_seconds: float = 30.0,
    ) -> Dict[str, Any]:
        command = "\n".join(
            [
                "set +e",
                "uname_value=\"$(uname -s 2>/dev/null || true)\"",
                "pretty_value=\"\"",
                "if command -v hostnamectl >/dev/null 2>&1; then "
                "pretty_value=\"$(hostnamectl 2>/dev/null | awk -F: '/Operating System/ { sub(/^[ \t]+/, \"\", $2); print $2; exit }')\"; "
                "fi",
                "if [ -z \"$pretty_value\" ] && [ -r /etc/os-release ]; then "
                "pretty_value=\"$(. /etc/os-release 2>/dev/null; printf '%s' \"${PRETTY_NAME:-${NAME:-${ID:-}}}\")\"; "
                "fi",
                "if [ -z \"$pretty_value\" ] && command -v freebsd-version >/dev/null 2>&1; then "
                "pretty_value=\"FreeBSD $(freebsd-version 2>/dev/null || true)\"; "
                "fi",
                "if [ -z \"$pretty_value\" ]; then pretty_value=\"$uname_value\"; fi",
                "printf '__BOREALIS_REMOTE_UNAME__=%s\\n' \"$uname_value\"",
                "printf '__BOREALIS_REMOTE_OS__=%s\\n' \"$pretty_value\"",
                "exit 0",
            ]
        )
        result = self._execute_onboarding_ssh_command(
            host=host,
            port=port,
            username=username,
            password=password,
            private_key_text=private_key_text,
            private_key_passphrase=private_key_passphrase,
            command=command,
            timeout_seconds=timeout_seconds,
        )
        stdout = str(result.get("stdout") or "")
        stderr = str(result.get("stderr") or "")

        def _marker(name: str) -> str:
            match = re.search(rf"^{re.escape(name)}=(.*)$", stdout, re.MULTILINE)
            return str(match.group(1) if match else "").strip()

        uname_value = _marker("__BOREALIS_REMOTE_UNAME__")
        pretty_value = _marker("__BOREALIS_REMOTE_OS__")
        display_value = pretty_value or uname_value
        normalized_uname = uname_value.strip().lower()
        normalized_display = display_value.strip().lower()
        unsupported_tokens = ("freebsd", "truenas", "pfsense", "opnsense", "openbsd", "netbsd", "darwin")
        unsupported = bool(normalized_uname and normalized_uname != "linux") or any(
            token in normalized_display for token in unsupported_tokens
        )
        return {
            "exit_code": int(result.get("exit_code") or 0),
            "stdout": stdout,
            "stderr": stderr,
            "uname": uname_value,
            "pretty": pretty_value,
            "display": display_value,
            "unsupported": unsupported,
        }

    def _onboarding_credential_label(self, credential: Mapping[str, Any]) -> str:
        name = str(credential.get("name") or "").strip()
        if name:
            return name
        username = str(credential.get("username") or "").strip()
        if username:
            return username
        credential_id = credential.get("id")
        return f"Credential {credential_id}" if credential_id not in (None, "") else "Stored Credential"

    def _onboarding_credential_redactions(self, credentials: Sequence[Mapping[str, Any]], *extra: Any) -> List[Any]:
        redactions: List[Any] = [value for value in extra if value]
        for credential in credentials or []:
            for key in ("password", "private_key", "private_key_passphrase", "become_password"):
                value = credential.get(key)
                if value:
                    redactions.append(value)
        return redactions

    def _load_onboarding_credential_lists(
        self,
        *,
        config: Mapping[str, Any],
        credential_id: Optional[int],
    ) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]]]:
        def _ids(raw_value: Any) -> List[int]:
            values: List[int] = []
            if isinstance(raw_value, str):
                candidates = [piece.strip() for piece in re.split(r"[,;\s]+", raw_value) if piece.strip()]
            elif isinstance(raw_value, (list, tuple, set)):
                candidates = list(raw_value)
            elif raw_value not in (None, ""):
                candidates = [raw_value]
            else:
                candidates = []
            for candidate in candidates:
                try:
                    parsed = int(candidate)
                except Exception:
                    continue
                if parsed > 0 and parsed not in values:
                    values.append(parsed)
            return values

        windows_ids = _ids(config.get("windows_credential_ids"))
        linux_ids = _ids(config.get("linux_credential_ids"))
        fallback_credential: Optional[Dict[str, Any]] = None
        if credential_id and not windows_ids and not linux_ids:
            fallback_credential = self._load_credential(credential_id)
            if not fallback_credential:
                raise RuntimeError("Selected stored credential could not be loaded.")
            connection_type = str(fallback_credential.get("connection_type") or "").strip().lower()
            if connection_type == "ssh":
                linux_ids.append(int(fallback_credential.get("id") or credential_id))
            elif connection_type in {"windows", "winrm"}:
                windows_ids.append(int(fallback_credential.get("id") or credential_id))
            else:
                raise RuntimeError("Selected stored credential is not usable for onboarding.")

        cache: Dict[int, Dict[str, Any]] = {}

        def _load(credential_id_value: int) -> Dict[str, Any]:
            if credential_id_value in cache:
                return cache[credential_id_value]
            if fallback_credential and int(fallback_credential.get("id") or 0) == int(credential_id_value):
                credential = fallback_credential
            else:
                credential = self._load_credential(credential_id_value)
            if not credential:
                raise RuntimeError(f"Stored credential {credential_id_value} could not be loaded.")
            if not str(credential.get("username") or "").strip():
                raise RuntimeError(f"Stored credential {credential_id_value} has no username.")
            cache[credential_id_value] = credential
            return credential

        windows_credentials: List[Dict[str, Any]] = []
        for current_id in windows_ids:
            credential = _load(current_id)
            connection_type = str(credential.get("connection_type") or "").strip().lower()
            if connection_type not in {"windows", "winrm"}:
                raise RuntimeError("Stored Windows Credential list contains a non-Windows credential.")
            if not str(credential.get("password") or "").strip():
                raise RuntimeError("Windows onboarding requires credential passwords.")
            windows_credentials.append(credential)

        linux_credentials: List[Dict[str, Any]] = []
        for current_id in linux_ids:
            credential = _load(current_id)
            connection_type = str(credential.get("connection_type") or "").strip().lower()
            if connection_type != "ssh":
                raise RuntimeError("Stored Linux Credential list contains a non-SSH credential.")
            if not str(credential.get("password") or "").strip() and not str(credential.get("private_key") or "").strip():
                raise RuntimeError("Linux onboarding requires a password or private key.")
            linux_credentials.append(credential)

        return windows_credentials, linux_credentials

    def _run_single_onboarding_target(
        self,
        *,
        row: Dict[str, Any],
        site: Dict[str, Any],
        branch: str,
        server_url: str,
        job_id: int,
        run_id: int,
        platform: str = ONBOARDING_PLATFORM_LINUX,
        credential: Optional[Dict[str, Any]] = None,
        linux_credentials: Optional[Sequence[Dict[str, Any]]] = None,
        windows_credentials: Optional[Sequence[Dict[str, Any]]] = None,
        ssh_port: int = DEFAULT_ONBOARDING_SSH_PORT,
        windows_port: int = DEFAULT_ONBOARDING_WINDOWS_PORT,
        windows_methods: Optional[Sequence[str]] = None,
        winrm_port: int = DEFAULT_ONBOARDING_WINRM_PORT,
    ) -> str:
        normalized_platform = str(platform or ONBOARDING_PLATFORM_LINUX).strip().lower()
        linux_candidates = list(linux_credentials or [])
        windows_candidates = list(windows_credentials or [])
        if credential and not linux_candidates and not windows_candidates:
            connection_type = str(credential.get("connection_type") or "").strip().lower()
            if connection_type == "ssh":
                linux_candidates = [credential]
            elif connection_type in {"windows", "winrm"}:
                windows_candidates = [credential]
        all_credentials = [*linux_candidates, *windows_candidates]
        redactions = self._onboarding_credential_redactions(all_credentials, site.get("enrollment_code"), server_url)
        row_id = int(row["id"])
        host = str(row.get("target_address") or row.get("target_hostname") or "").strip()
        site_id = int(site.get("id")) if site.get("id") is not None else None
        known_target_hostname = self._lookup_known_onboarding_target_hostname_from_candidates(
            [row.get("target_address"), row.get("target_hostname"), row.get("target_input"), host],
            site_id,
        )
        if known_target_hostname:
            self._set_onboarding_target_hostname(row_id, known_target_hostname)
        active_target_hostname = self._lookup_active_onboarding_target_hostname_from_candidates(
            [row.get("target_address"), known_target_hostname, row.get("target_hostname"), row.get("target_input"), host],
            site_id,
        )
        if active_target_hostname:
            self._update_onboarding_target_row(
                row_id,
                status="already_enrolled",
                detail="Existing Borealis Agent is already enrolled and active.",
                target_hostname=active_target_hostname,
                finished=True,
                redactions=redactions,
            )
            return "already_enrolled"

        ssh_target_port = int(ssh_port or row.get("ssh_port") or DEFAULT_ONBOARDING_SSH_PORT)
        smb_target_port = int(windows_port or DEFAULT_ONBOARDING_WINDOWS_PORT)
        winrm_target_port = int(winrm_port or DEFAULT_ONBOARDING_WINRM_PORT)
        protocol_score = self._score_remote_onboarding_protocols(
            host=host,
            ssh_port=ssh_target_port,
            smb_port=smb_target_port,
            winrm_port=winrm_target_port,
        )
        self._update_onboarding_target_row(
            row_id,
            status=ONBOARDING_STATUS_RUNNING,
            detail=str(protocol_score.get("detail") or "Auto-Detecting Remote OS"),
            redactions=redactions,
        )
        protocol_unsupported_detail = str(protocol_score.get("unsupported_detail") or "").strip()
        if str(protocol_score.get("classification") or "").strip().lower() == "unsupported" and protocol_unsupported_detail:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_UNSUPPORTED_OS,
                detail=protocol_unsupported_detail,
                stderr="\n".join(str(item) for item in protocol_score.get("signals") or []),
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_SKIPPED

        linux_session_established = False
        linux_last_error = ""
        linux_ssh_banner = ""
        linux_definitive_unix = False
        linux_unsupported_detail = ""

        def _record_detected_platform(label: str) -> None:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail=f"Detected Remote Operating System: {label}",
                redactions=redactions,
            )

        def _remote_port_status_detail(
            *,
            ssh_error: str = "",
            smb_error: str = "",
            winrm_error: str = "",
            linux_auth_error: str = "",
        ) -> str:
            segments: List[str] = []
            ssh_target_port = int(ssh_port or row.get("ssh_port") or DEFAULT_ONBOARDING_SSH_PORT)
            smb_target_port = int(windows_port or DEFAULT_ONBOARDING_WINDOWS_PORT)
            winrm_target_port = int(winrm_port or DEFAULT_ONBOARDING_WINRM_PORT)
            if ssh_error:
                segments.append(f"SSH {ssh_target_port}={ssh_error}")
            elif linux_auth_error:
                segments.append(f"SSH {ssh_target_port}=authentication failed")
            if smb_error:
                segments.append(f"SMB {smb_target_port}={smb_error}")
            if winrm_error:
                segments.append(f"WinRM {winrm_target_port}={winrm_error}")
            if not segments:
                return "Remote device unreachable."
            return f"Remote device unreachable: {'; '.join(segments)}"

        def _mark_remote_unreachable(
            *,
            ssh_error: str = "",
            smb_error: str = "",
            winrm_error: str = "",
            linux_auth_error: str = "",
        ) -> str:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_UNREACHABLE,
                detail=_remote_port_status_detail(
                    ssh_error=ssh_error,
                    smb_error=smb_error,
                    winrm_error=winrm_error,
                    linux_auth_error=linux_auth_error,
                ),
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_UNREACHABLE

        def _try_linux_candidates(*, final_on_failure: bool = True) -> str:
            nonlocal linux_last_error, linux_session_established, linux_ssh_banner, linux_definitive_unix, linux_unsupported_detail
            if not linux_candidates:
                return ""
            tcp_error = self._preflight_remote_port(host=host, port=ssh_target_port, attempts=1, timeout_seconds=3.0)
            if tcp_error:
                linux_last_error = tcp_error
                return ONBOARDING_STATUS_UNREACHABLE
            banner, banner_error = self._read_ssh_banner(
                host=host,
                port=ssh_target_port,
                timeout_seconds=_env_non_negative_float(
                    _SSH_BANNER_TIMEOUT_ENV,
                    min(5.0, _DEFAULT_SHARED_ANSIBLE_SSH_BANNER_TIMEOUT_SECONDS),
                ),
            )
            linux_ssh_banner = banner or linux_ssh_banner
            linux_definitive_unix = bool(banner and _ssh_banner_is_unix_like(banner))
            if _ssh_banner_is_management_endpoint(banner) or _ssh_error_is_unsupported_endpoint(banner_error, banner=banner):
                linux_last_error = banner_error or banner
                linux_unsupported_detail = _ssh_unsupported_endpoint_detail(linux_last_error, banner=banner)
                if final_on_failure:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_UNSUPPORTED_OS,
                        detail=linux_unsupported_detail,
                        stderr=linux_last_error,
                        finished=True,
                        redactions=redactions,
                    )
                return ONBOARDING_STATUS_SKIPPED
            linux_total = max(1, len(linux_candidates))
            for credential_index, candidate in enumerate(linux_candidates, start=1):
                label = self._onboarding_credential_label(candidate)
                attempt_label = f"Credential {credential_index}/{linux_total} Attempted: {label}"
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_RUNNING,
                    detail=f"Establishing Connection to Remote Device: {attempt_label}",
                    redactions=redactions,
                )
                username = str(candidate.get("username") or "").strip()
                password = str(candidate.get("password") or "")
                private_key = _normalize_ssh_private_key_text(candidate.get("private_key") or "")
                become_method = str(candidate.get("become_method") or "").strip().lower()
                probe_become_method, probe_become_user = _linux_onboarding_privilege_probe(username, become_method)
                become_password = str(candidate.get("become_password") or password or "")
                auth_private_key = private_key
                if private_key and password:
                    auth_mode = self._resolve_mixed_ssh_auth_mode(
                        host=host,
                        port=ssh_target_port,
                        username=username,
                        password=password,
                        private_key_text=private_key,
                    )
                    if auth_mode == "password":
                        auth_private_key = ""
                session_error = self._preflight_ssh_session(
                    host=host,
                    port=ssh_target_port,
                    username=username,
                    password=password,
                    private_key_text=auth_private_key,
                    timeout_seconds=_env_non_negative_float(
                        _SSH_SESSION_TIMEOUT_ENV,
                        _DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS,
                    ),
                    become_method=probe_become_method,
                    become_user=probe_become_user,
                    become_password=become_password,
                )
                if session_error:
                    linux_last_error = session_error
                    if _ssh_error_is_unsupported_endpoint(session_error, banner=linux_ssh_banner):
                        linux_unsupported_detail = _ssh_unsupported_endpoint_detail(session_error, banner=linux_ssh_banner)
                        if final_on_failure:
                            self._update_onboarding_target_row(
                                row_id,
                                status=ONBOARDING_STATUS_UNSUPPORTED_OS,
                                detail=linux_unsupported_detail,
                                stderr=linux_last_error,
                                finished=True,
                                redactions=redactions,
                            )
                        return ONBOARDING_STATUS_SKIPPED
                    continue
                linux_session_established = True
                _record_detected_platform("Linux")
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_RUNNING,
                    detail=f"Connection Established using SSH: Credential {credential_index}/{linux_total} {label}",
                    redactions=redactions,
                )
                row_for_linux = dict(row)
                row_for_linux["ssh_port"] = ssh_target_port
                return self._run_linux_onboarding_target(
                    row=row_for_linux,
                    site=site,
                    credential=candidate,
                    branch=branch,
                    server_url=server_url,
                    job_id=job_id,
                    run_id=run_id,
                    credential_label=label,
                    skip_session_preflight=True,
                )
            if linux_unsupported_detail:
                if final_on_failure:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_UNSUPPORTED_OS,
                        detail=linux_unsupported_detail,
                        stderr=linux_last_error,
                        finished=True,
                        redactions=redactions,
                    )
                return ONBOARDING_STATUS_SKIPPED
            if linux_last_error:
                if final_on_failure:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_FAILED,
                        detail=f"Establishing Connection to Remote Device: Credential {len(linux_candidates)}/{len(linux_candidates)} Failed",
                        stderr=linux_last_error,
                        finished=True,
                        redactions=redactions,
                    )
                return ONBOARDING_STATUS_FAILED
            return ""

        def _try_windows_candidates(
            *,
            mark_unreachable: bool = True,
            smb_error_override: Optional[str] = None,
            winrm_error_override: Optional[str] = None,
        ) -> str:
            if not windows_candidates:
                return ""
            smb_error = (
                smb_error_override
                if smb_error_override is not None
                else self._preflight_remote_port(host=host, port=smb_target_port, attempts=1, timeout_seconds=3.0)
            )
            winrm_error = (
                winrm_error_override
                if winrm_error_override is not None
                else self._preflight_remote_port(host=host, port=winrm_target_port, attempts=1, timeout_seconds=3.0)
            )
            if smb_error and winrm_error:
                if mark_unreachable:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_UNREACHABLE,
                        detail=f"Windows remote enrollment ports unreachable: SMB {smb_target_port}={smb_error}; WinRM {winrm_target_port}={winrm_error}",
                        finished=True,
                        redactions=redactions,
                    )
                return ONBOARDING_STATUS_UNREACHABLE
            last_error = ""
            if not smb_error:
                remote_hostname = self._probe_windows_smb_server_name(host=host, port=smb_target_port)
                if remote_hostname:
                    self._set_onboarding_target_hostname(row_id, remote_hostname)
                    active_hostname = self._lookup_active_onboarding_target_hostname_from_candidates(
                        [host, remote_hostname, row.get("target_address"), row.get("target_hostname"), row.get("target_input")],
                        site_id,
                    )
                    if active_hostname:
                        self._update_onboarding_target_row(
                            row_id,
                            status="already_enrolled",
                            detail="Existing Borealis Agent is already enrolled and active.",
                            target_hostname=active_hostname,
                            finished=True,
                            redactions=redactions,
                        )
                        return "already_enrolled"
            windows_total = max(1, len(windows_candidates))
            for credential_index, candidate in enumerate(windows_candidates, start=1):
                label = self._onboarding_credential_label(candidate)
                attempt_label = f"Credential {credential_index}/{windows_total} Attempted: {label}"
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_RUNNING,
                    detail=f"Establishing Connection to Remote Device: {attempt_label}",
                    redactions=redactions,
                )
                if not smb_error:
                    smb = None
                    try:
                        smb = self._open_windows_smb_connection(host=host, port=smb_target_port, credential=candidate)
                        remote_hostname = ""
                        try:
                            remote_hostname = _clean_onboarding_reported_hostname(smb.getServerName())
                        except Exception:
                            remote_hostname = ""
                        if remote_hostname:
                            self._set_onboarding_target_hostname(row_id, remote_hostname)
                            active_hostname = self._lookup_active_onboarding_target_hostname_from_candidates(
                                [host, remote_hostname, row.get("target_address"), row.get("target_hostname"), row.get("target_input")],
                                site_id,
                            )
                            if active_hostname:
                                self._update_onboarding_target_row(
                                    row_id,
                                    status="already_enrolled",
                                    detail="Existing Borealis Agent is already enrolled and active.",
                                    target_hostname=active_hostname,
                                    finished=True,
                                    redactions=redactions,
                                )
                                return "already_enrolled"
                    except Exception as exc:
                        last_error = str(exc).strip() or exc.__class__.__name__
                        continue
                    finally:
                        if smb is not None:
                            try:
                                smb.logoff()
                            except Exception:
                                pass
                    _record_detected_platform("Windows")
                windows_result = self._run_windows_onboarding_target(
                    row=row,
                    site=site,
                    credential=candidate,
                    branch=branch,
                    server_url=server_url,
                    job_id=job_id,
                    run_id=run_id,
                    windows_methods=windows_methods or ONBOARDING_WINDOWS_METHODS,
                    winrm_port=winrm_target_port,
                    smb_port=smb_target_port,
                    credential_label=f"Credential {credential_index}/{windows_total} {label}",
                    final_on_failure=credential_index >= windows_total,
                )
                if windows_result == "credential_failed":
                    continue
                return windows_result
            if last_error:
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_FAILED,
                    detail=f"Establishing Connection to Remote Device: Credential {len(windows_candidates)}/{len(windows_candidates)} Failed",
                    stderr=last_error,
                    finished=True,
                    redactions=redactions,
                )
                return ONBOARDING_STATUS_FAILED
            return ""

        if normalized_platform == ONBOARDING_PLATFORM_AUTO:
            ssh_error = self._preflight_remote_port(host=host, port=ssh_target_port, attempts=1, timeout_seconds=3.0)
            if not ssh_error:
                linux_result = _try_linux_candidates(final_on_failure=False)
                if linux_result and linux_session_established:
                    return linux_result
                if linux_result == ONBOARDING_STATUS_FAILED and linux_definitive_unix:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_FAILED,
                        detail=_ssh_preflight_failure_detail(linux_last_error),
                        stderr=linux_last_error,
                        finished=True,
                        redactions=redactions,
                    )
                    return ONBOARDING_STATUS_FAILED
                smb_error = self._preflight_remote_port(host=host, port=smb_target_port, attempts=1, timeout_seconds=3.0)
                winrm_error = self._preflight_remote_port(host=host, port=winrm_target_port, attempts=1, timeout_seconds=3.0)
                if not (smb_error and winrm_error):
                    windows_result = _try_windows_candidates(
                        mark_unreachable=False,
                        smb_error_override=smb_error,
                        winrm_error_override=winrm_error,
                    )
                    if windows_result and windows_result != ONBOARDING_STATUS_UNREACHABLE:
                        return windows_result
                if linux_result == ONBOARDING_STATUS_SKIPPED:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_SKIPPED,
                        detail=linux_unsupported_detail or "SSH endpoint is not compatible with Borealis agent onboarding.",
                        stderr=linux_last_error,
                        finished=True,
                        redactions=redactions,
                    )
                    return ONBOARDING_STATUS_SKIPPED
                if linux_result == ONBOARDING_STATUS_FAILED:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_FAILED,
                        detail=_ssh_preflight_failure_detail(linux_last_error),
                        stderr=linux_last_error,
                        finished=True,
                        redactions=redactions,
                    )
                    return ONBOARDING_STATUS_FAILED
                if linux_result == ONBOARDING_STATUS_UNREACHABLE:
                    return _mark_remote_unreachable(
                        ssh_error=linux_last_error or "unreachable",
                        smb_error=smb_error,
                        winrm_error=winrm_error,
                    )
                return _mark_remote_unreachable(
                    smb_error=smb_error,
                    winrm_error=winrm_error,
                    linux_auth_error=linux_last_error,
                )
            smb_error = self._preflight_remote_port(host=host, port=smb_target_port, attempts=1, timeout_seconds=3.0)
            winrm_error = self._preflight_remote_port(host=host, port=winrm_target_port, attempts=1, timeout_seconds=3.0)
            rpc_error = self._preflight_remote_port(host=host, port=135, attempts=1, timeout_seconds=3.0)
            windows_result = _try_windows_candidates(
                mark_unreachable=False,
                smb_error_override=smb_error,
                winrm_error_override=winrm_error,
            )
            if windows_result and windows_result != ONBOARDING_STATUS_UNREACHABLE:
                return windows_result
            if windows_result == ONBOARDING_STATUS_UNREACHABLE:
                if not rpc_error and smb_error and winrm_error:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_UNREACHABLE,
                        detail=(
                            "Windows RPC endpoint reachable, but SMB C$ and WinRM are unavailable; "
                            "remote onboarding cannot stage Agent.exe."
                        ),
                        finished=True,
                        redactions=redactions,
                    )
                    return ONBOARDING_STATUS_UNREACHABLE
                return _mark_remote_unreachable(ssh_error=ssh_error, smb_error=smb_error, winrm_error=winrm_error)
            if windows_result:
                return windows_result
            if not rpc_error and smb_error and winrm_error:
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_UNREACHABLE,
                    detail=(
                        "Windows RPC endpoint reachable, but SMB C$ and WinRM are unavailable; "
                        "remote onboarding cannot stage Agent.exe."
                    ),
                    finished=True,
                    redactions=redactions,
                )
                return ONBOARDING_STATUS_UNREACHABLE
            return _mark_remote_unreachable(ssh_error=ssh_error, smb_error=smb_error, winrm_error=winrm_error)

        if normalized_platform == ONBOARDING_PLATFORM_WINDOWS:
            if not windows_candidates:
                raise RuntimeError("Windows onboarding requires at least one Stored Windows Credential.")
            return _try_windows_candidates(mark_unreachable=True)
        if not linux_candidates:
            raise RuntimeError("Linux onboarding requires at least one Stored Linux Credential.")
        return self._run_linux_onboarding_target(
            row=row,
            site=site,
            credential=linux_candidates[0],
            branch=branch,
            server_url=server_url,
            job_id=job_id,
            run_id=run_id,
            credential_label=self._onboarding_credential_label(linux_candidates[0]),
        )

    def _run_linux_onboarding_target(
        self,
        *,
        row: Dict[str, Any],
        site: Dict[str, Any],
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        job_id: int,
        run_id: int,
        credential_label: str = "",
        skip_session_preflight: bool = False,
    ) -> str:
        row_id = int(row["id"])
        host = str(row.get("target_address") or row.get("target_hostname") or "").strip()
        port = int(row.get("ssh_port") or DEFAULT_ONBOARDING_SSH_PORT)
        redactions = [site.get("enrollment_code"), server_url, credential.get("password"), credential.get("private_key")]
        connection_label = str(credential_label or self._onboarding_credential_label(credential)).strip()
        connection_detail = f"Establishing Connection to Remote Device: {connection_label}" if connection_label else "Connecting to SSH."
        if not skip_session_preflight:
            self._update_onboarding_target_row(row_id, status=ONBOARDING_STATUS_RUNNING, detail=connection_detail, redactions=redactions)
        known_hostname = self._lookup_known_onboarding_target_hostname(host, site.get("id"))
        if known_hostname or self._onboarding_target_already_known(host, site.get("id")):
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_SKIPPED,
                detail="Target already appears enrolled for this site.",
                target_hostname=known_hostname,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_SKIPPED

        tcp_error = self._preflight_remote_port(host=host, port=port, attempts=1, timeout_seconds=3.0)
        if tcp_error:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_UNREACHABLE,
                detail=f"Remote port unreachable: {tcp_error}",
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_UNREACHABLE

        username = str(credential.get("username") or "").strip()
        password = str(credential.get("password") or "")
        private_key = _normalize_ssh_private_key_text(credential.get("private_key") or "")
        auth_private_key = private_key
        if private_key and password:
            auth_mode = self._resolve_mixed_ssh_auth_mode(
                host=host,
                port=port,
                username=username,
                password=password,
                private_key_text=private_key,
            )
            if auth_mode == "password":
                auth_private_key = ""
        become_method = str(credential.get("become_method") or "").strip().lower()
        probe_become_method, probe_become_user = _linux_onboarding_privilege_probe(username, become_method)
        become_password = str(credential.get("become_password") or password or "")
        session_error = ""
        if not skip_session_preflight:
            session_error = self._preflight_ssh_session(
                host=host,
                port=port,
                username=username,
                password=password,
                private_key_text=auth_private_key,
                timeout_seconds=_env_non_negative_float(
                    _SSH_SESSION_TIMEOUT_ENV,
                    _DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS,
                ),
                become_method=probe_become_method,
                become_user=probe_become_user,
                become_password=become_password,
            )
        if session_error:
            detail = "SSH authentication failed." if session_error in {"permission_denied", "ssh_password_required"} else session_error
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_FAILED,
                detail=detail,
                stderr=session_error,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_FAILED
        if not skip_session_preflight:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail="Connection Established using SSH",
                redactions=redactions,
            )

        os_probe = self._detect_ssh_operating_system(
            host=host,
            port=port,
            username=username,
            password=password,
            private_key_text=auth_private_key,
            private_key_passphrase=str(credential.get("private_key_passphrase") or ""),
            timeout_seconds=min(30.0, self._onboarding_install_timeout_seconds()),
        )
        detected_os = str(os_probe.get("display") or os_probe.get("uname") or "").strip()
        if detected_os:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail=f"Detected Remote Operating System: {detected_os}",
                stdout=os_probe.get("stdout") or "",
                stderr=os_probe.get("stderr") or "",
                redactions=redactions,
            )
        if os_probe.get("unsupported"):
            unsupported_detail = "Unsupported OS"
            unsupported_stderr = f"Detected remote operating system is unsupported: {detected_os or 'unknown'}"
            if os_probe.get("stderr"):
                unsupported_stderr = f"{unsupported_stderr}\n{os_probe.get('stderr')}"
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_FAILED,
                detail=unsupported_detail,
                stdout=os_probe.get("stdout") or "",
                stderr=unsupported_stderr,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_FAILED

        command = self._remote_onboarding_command(
            branch=branch,
            server_url=server_url,
            enrollment_code=str(site.get("enrollment_code") or ""),
            job_id=job_id,
            run_id=run_id,
            target=host,
        )
        result = self._execute_onboarding_ssh_command(
            host=host,
            port=port,
            username=username,
            password=password,
            private_key_text=auth_private_key,
            private_key_passphrase=str(credential.get("private_key_passphrase") or ""),
            command=command,
            timeout_seconds=self._onboarding_install_timeout_seconds(),
            become_password=become_password,
        )
        exit_code = int(result.get("exit_code") or 0)
        stdout = result.get("stdout") or ""
        stderr = result.get("stderr") or ""
        if exit_code == 42:
            unsupported_match = re.search(r"__BOREALIS_UNSUPPORTED_OS__=([^\r\n]+)", str(stderr or stdout or ""), re.IGNORECASE)
            detected_unsupported_os = str(unsupported_match.group(1) if unsupported_match else "").strip()
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_FAILED,
                detail="Unsupported OS",
                stdout=stdout,
                stderr=stderr or (f"Detected remote operating system is unsupported: {detected_unsupported_os}" if detected_unsupported_os else ""),
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_FAILED
        if exit_code != 0:
            detail = f"Agent install failed with exit code {exit_code}."
            if exit_code == 43:
                detail = "Target needs curl or wget to download Agent.sh."
            elif exit_code == 44:
                detail = "Target user needs root or sudo for agent deployment."
            elif exit_code == 45:
                detail = "sudo password required or rejected."
            output_hint = _onboarding_failure_hint(stdout=stdout, stderr=stderr, redactions=redactions)
            if output_hint:
                detail = f"{detail} {output_hint}"
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_FAILED,
                detail=detail,
                stdout=stdout,
                stderr=stderr,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_FAILED

        approval_reference = ""
        deadline = time.monotonic() + 10.0
        while time.monotonic() < deadline:
            approval_reference = self._lookup_onboarding_approval(job_id=job_id, run_id=run_id, target=host)
            if approval_reference:
                break
            time.sleep(1.0)
        self._update_onboarding_target_row(
            row_id,
            status=ONBOARDING_STATUS_WAITING_APPROVAL,
            detail="Agent installed. Device approval pending operator action.",
            stdout=stdout,
            stderr=stderr,
            approval_reference=approval_reference,
            finished=True,
            redactions=redactions,
        )
        return ONBOARDING_STATUS_WAITING_APPROVAL

    def _windows_task_xml(self, *, task_name: str, command: str, arguments: str) -> str:
        now = time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime())
        return "\n".join(
            [
                '<?xml version="1.0" encoding="UTF-16"?>',
                '<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">',
                "  <RegistrationInfo>",
                f"    <Date>{html.escape(now)}</Date>",
                "    <Author>Borealis</Author>",
                "  </RegistrationInfo>",
                "  <Triggers>",
                "    <TimeTrigger>",
                f"      <StartBoundary>{html.escape(now)}</StartBoundary>",
                "      <Enabled>true</Enabled>",
                "    </TimeTrigger>",
                "  </Triggers>",
                "  <Principals>",
                "    <Principal id=\"LocalSystem\">",
                "      <UserId>S-1-5-18</UserId>",
                "      <RunLevel>HighestAvailable</RunLevel>",
                "    </Principal>",
                "  </Principals>",
                "  <Settings>",
                "    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
                "    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
                "    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
                "    <AllowHardTerminate>true</AllowHardTerminate>",
                "    <StartWhenAvailable>true</StartWhenAvailable>",
                "    <AllowStartOnDemand>true</AllowStartOnDemand>",
                "    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>",
                "    <Enabled>true</Enabled>",
                "    <Hidden>true</Hidden>",
                "    <RunOnlyIfIdle>false</RunOnlyIfIdle>",
                "    <WakeToRun>false</WakeToRun>",
                "    <ExecutionTimeLimit>PT1H</ExecutionTimeLimit>",
                "    <Priority>7</Priority>",
                "  </Settings>",
                "  <Actions Context=\"LocalSystem\">",
                "    <Exec>",
                f"      <Command>{html.escape(command)}</Command>",
                f"      <Arguments>{html.escape(arguments)}</Arguments>",
                "    </Exec>",
                "  </Actions>",
                "</Task>",
            ]
        )

    def _open_windows_smb_connection(self, *, host: str, port: int, credential: Mapping[str, Any]):
        try:
            from impacket.smbconnection import SMBConnection  # type: ignore
        except Exception as exc:
            raise RuntimeError(f"impacket_unavailable:{exc}") from exc
        domain, username, password = _parse_windows_credential_parts(credential)
        connect_timeout = max(3, min(30, int(_env_non_negative_float("BOREALIS_WINDOWS_SMB_TIMEOUT_SECONDS", 8.0) or 8.0)))
        login_attempts = max(1, min(3, _env_positive_int("BOREALIS_WINDOWS_SMB_LOGIN_ATTEMPTS", 1)))
        last_error = ""
        for remote_name in _windows_smb_remote_names(host, credential):
            for attempt in range(login_attempts):
                smb = None
                try:
                    smb = SMBConnection(remote_name, host, sess_port=int(port), timeout=connect_timeout)
                    smb.login(username, password, domain)
                    return smb
                except Exception as exc:
                    last_error = str(exc).strip() or exc.__class__.__name__
                    if smb is not None:
                        try:
                            smb.close()
                        except Exception:
                            pass
                if attempt < login_attempts - 1:
                    time.sleep(1.0)
        raise RuntimeError(f"smb_connection_failed:{last_error or 'connection_failed'}")

    def _probe_windows_smb_server_name(self, *, host: str, port: int) -> str:
        try:
            from impacket.smbconnection import SMBConnection  # type: ignore
        except Exception:
            return ""
        connect_timeout = max(3, min(30, int(_env_non_negative_float("BOREALIS_WINDOWS_SMB_TIMEOUT_SECONDS", 8.0) or 8.0)))
        for remote_name in ("*SMBSERVER", str(host or "").strip()):
            smb = None
            try:
                smb = SMBConnection(remote_name, host, sess_port=int(port), timeout=connect_timeout)
                hostname = _clean_onboarding_reported_hostname(smb.getServerName())
                if hostname:
                    return hostname
            except Exception:
                continue
            finally:
                if smb is not None:
                    try:
                        smb.close()
                    except Exception:
                        pass
        return ""

    def _read_windows_smb_file(self, smb: Any, path: str, *, share: str = "ADMIN$") -> str:
        def _read_with_open_file() -> Optional[bytes]:
            file_id = None
            tree_id = None
            share_name_inner = str(share or "ADMIN$")
            try:
                tree_id = smb.connectTree(share_name_inner)
                file_id = smb.openFile(
                    tree_id,
                    path,
                    desiredAccess=0x00000001,
                    shareMode=0x00000007,
                )
                chunks: List[bytes] = []
                offset = 0
                chunk_size = 65536
                while True:
                    chunk = smb.readFile(tree_id, file_id, offset, chunk_size)
                    if not chunk:
                        break
                    if isinstance(chunk, str):
                        chunk = chunk.encode("utf-8", errors="replace")
                    chunks.append(bytes(chunk))
                    offset += len(chunk)
                    if len(chunk) < chunk_size:
                        break
                return b"".join(chunks)
            except TypeError:
                return None
            except Exception as exc:
                if _windows_smb_invalid_parameter_error(exc):
                    return None
                raise
            finally:
                if file_id is not None:
                    try:
                        smb.closeFile(tree_id, file_id)
                    except Exception:
                        pass
                if tree_id is not None:
                    try:
                        smb.disconnectTree(tree_id)
                    except Exception:
                        pass

        buffer = io.BytesIO()
        share_name = str(share or "ADMIN$")
        raw = _read_with_open_file()
        if raw is not None:
            return raw.decode("utf-8", errors="replace")
        try:
            smb.getFile(share_name, path, buffer.write, shareAccessMode=0x7)
        except TypeError:
            smb.getFile(share_name, path, buffer.write)
        except Exception as exc:
            if not _windows_smb_invalid_parameter_error(exc):
                raise
            buffer = io.BytesIO()
            smb.getFile(share_name, path, buffer.write)
        return buffer.getvalue().decode("utf-8", errors="replace")

    def _windows_agent_exe_path(self) -> Optional[Path]:
        override = str(os.environ.get("BOREALIS_WINDOWS_AGENT_EXE") or "").strip()
        if override:
            candidate = Path(override).expanduser()
            if candidate.is_file():
                return candidate
        current = Path(__file__).resolve()
        roots: List[Path] = []
        for parent in [current.parent, *current.parents]:
            if parent not in roots:
                roots.append(parent)
        env_root = str(os.environ.get("BOREALIS_PROJECT_ROOT") or "").strip()
        if env_root:
            roots.insert(0, Path(env_root).expanduser())
        for root in roots:
            for candidate in (
                root / "Data" / "Agent" / "Bootstrap" / AGENT_EXE_NAME,
                root / "Engine" / "Services" / "api-backend" / "data" / "Data" / "Agent" / "Bootstrap" / AGENT_EXE_NAME,
                root / "Engine" / "Services" / "api-backend" / "data" / AGENT_EXE_NAME,
            ):
                if candidate.is_file():
                    return candidate
        return None

    def _windows_agent_exe_unavailable_result(self) -> Dict[str, Any]:
        return {
            "exit_code": 127,
            "stdout": "",
            "stderr": (
                f"{AGENT_EXE_NAME} unavailable. Build "
                "Data/Agent/Bootstrap/Agent.exe, or set "
                "BOREALIS_WINDOWS_AGENT_EXE."
            ),
        }

    def _windows_quote_command_arg(self, value: Any) -> str:
        text = str(value or "")
        if not text:
            return '""'
        if not re.search(r'[\s"]', text):
            return text
        return '"' + text.replace('"', r'\"') + '"'

    def _windows_agent_prelaunch_cmd_args(self, exe_abs: str) -> Tuple[str, str]:
        quoted_exe = self._windows_quote_command_arg(exe_abs)
        prelaunch = (
            'schtasks.exe /End /TN "Borealis Agent" >NUL 2>&1 & '
            'schtasks.exe /End /TN "Borealis Agent (AutoUpdater)" >NUL 2>&1 & '
            "taskkill.exe /F /IM Agent.exe /T >NUL 2>&1 & "
            f"{quoted_exe}"
        )
        return "cmd.exe", f"/C {prelaunch}"

    def _windows_create_agent_payload_bundle(self) -> Tuple[Path, str]:
        current = Path(__file__).resolve()
        source_root = None
        env_root = str(os.environ.get("BOREALIS_PROJECT_ROOT") or "").strip()
        candidates = []
        if env_root:
            candidates.append(Path(env_root).expanduser())
        candidates.extend([current.parent, *current.parents])
        for candidate in candidates:
            probe = candidate / "Data" / "Agent"
            if (probe / "agent.py").is_file():
                source_root = probe
                break
            probe = candidate / "Data" / "Engine" / "Data" / "Agent"
            if (probe / "agent.py").is_file():
                source_root = probe
                break
        if source_root is None:
            raise FileNotFoundError("Data/Agent/agent.py")
        if not (source_root / "agent.py").is_file():
            raise FileNotFoundError("Data/Agent/agent.py")
        temp_dir = Path(tempfile.mkdtemp(prefix="borealis-agent-payload-"))
        bundle_path = temp_dir / "agent-payload.zip"
        excluded_dirs = {"Unit_Tests", "Bootstrap", "Logs", "__pycache__", ".pytest_cache"}
        excluded_files: set[str] = set()
        with zipfile.ZipFile(bundle_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for path in source_root.rglob("*"):
                rel_to_agent = path.relative_to(source_root)
                if any(part in excluded_dirs for part in rel_to_agent.parts):
                    continue
                if path.name in excluded_files:
                    continue
                arcname = Path("Data") / "Agent" / rel_to_agent
                if path.is_dir():
                    continue
                archive.write(path, arcname.as_posix())
        digest = hashlib.sha256(bundle_path.read_bytes()).hexdigest()
        return bundle_path, digest

    def _windows_smb_stage_agent_exe(
        self,
        smb: Any,
        *,
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
        timeout_seconds: float,
        service_name: str = "",
    ) -> Tuple[str, str, str]:
        agent_exe_path = self._windows_agent_exe_path()
        if agent_exe_path is None:
            raise FileNotFoundError(AGENT_EXE_NAME)
        branch_ref = str(branch or "main").strip() or "main"
        for remote_dir in ("Borealis", "Borealis\\Temp", "Borealis\\Temp\\Onboarding"):
            try:
                smb.createDirectory("C$", remote_dir)
            except Exception:
                pass
        exe_path = f"Borealis\\Temp\\Onboarding\\Agent-{int(run_id)}-{uuid.uuid4().hex[:8]}.exe"
        config_path = f"Borealis\\Temp\\Onboarding\\bootstrapper-config.json"
        payload_path = f"Borealis\\Temp\\Onboarding\\agent-payload.zip"
        manifest_path = f"Borealis\\Temp\\Onboarding\\agent-payload-manifest.json"
        output_path = f"Borealis\\Temp\\Onboarding\\stdout.log"
        exe_abs = f"C:\\{exe_path}"
        config_abs = f"C:\\{config_path}"
        payload_abs = f"C:\\{payload_path}"
        manifest_abs = f"C:\\{manifest_path}"
        output_abs = f"C:\\{output_path}"
        state_abs = "C:\\Borealis\\Temp\\Onboarding\\state.json"
        events_abs = "C:\\Borealis\\Temp\\Onboarding\\events.jsonl"
        bundle_path, bundle_sha256 = self._windows_create_agent_payload_bundle()
        config = {
            "install_dir": "C:\\Borealis",
            "repo_url": DEFAULT_BOREALIS_REPO_GIT_URL,
            "repo_ref": branch_ref,
            "server_url": str(server_url or ""),
            "site_enrollment_code": str(enrollment_code or ""),
            "agent_bundle_path": payload_abs,
            "agent_bundle_sha256": bundle_sha256,
            "manifest_path": manifest_abs,
            "state_path": state_abs,
            "events_path": events_abs,
            "stdout_path": output_abs,
            "stderr_path": output_abs,
            "timeout_seconds": max(60, int(max(60.0, float(timeout_seconds or 900.0)) - 30)),
            "job_id": int(job_id),
            "run_id": int(run_id),
            "target": str(target or "").strip(),
            "service_name": str(service_name or "").strip(),
            "noninteractive": True,
        }
        manifest = {
            "repo_ref": branch_ref,
            "sha256": bundle_sha256,
            "created_at": int(time.time()),
        }
        try:
            for stale_path in (
                output_path,
                "Borealis\\Temp\\Onboarding\\state.json",
                "Borealis\\Temp\\Onboarding\\events.jsonl",
            ):
                try:
                    smb.deleteFile("C$", stale_path)
                except Exception:
                    pass
            with open(agent_exe_path, "rb") as handle:
                smb.putFile("C$", exe_path, handle.read)
            with open(bundle_path, "rb") as handle:
                smb.putFile("C$", payload_path, handle.read)
            config_bytes = io.BytesIO(json.dumps(config, separators=(",", ":")).encode("utf-8"))
            manifest_bytes = io.BytesIO(json.dumps(manifest, separators=(",", ":")).encode("utf-8"))
            output_bytes = io.BytesIO(b"__BOREALIS_AGENT_EXE_STAGED__=1\r\n")
            events_bytes = io.BytesIO(b"")
            smb.putFile("C$", config_path, config_bytes.read)
            smb.putFile("C$", manifest_path, manifest_bytes.read)
            smb.putFile("C$", output_path, output_bytes.read)
            smb.putFile("C$", "Borealis\\Temp\\Onboarding\\events.jsonl", events_bytes.read)
        finally:
            try:
                shutil.rmtree(bundle_path.parent)
            except Exception:
                pass
        return exe_abs, config_abs, output_path

    def _read_windows_onboarding_state(self, smb: Any) -> Dict[str, Any]:
        try:
            text = self._read_windows_smb_file(smb, "Borealis\\Temp\\Onboarding\\state.json", share="C$")
            parsed = json.loads(text)
        except Exception:
            return {}
        return dict(parsed) if isinstance(parsed, Mapping) else {}

    def _read_windows_onboarding_events(self, smb: Any) -> List[Dict[str, Any]]:
        try:
            text = self._read_windows_smb_file(smb, "Borealis\\Temp\\Onboarding\\events.jsonl", share="C$")
        except Exception:
            return []
        events: List[Dict[str, Any]] = []
        for line in str(text or "").splitlines()[-50:]:
            if not line.strip():
                continue
            try:
                parsed = json.loads(line)
            except Exception:
                continue
            if isinstance(parsed, Mapping):
                events.append(dict(parsed))
        return events

    def _poll_windows_smb_onboarding_output(
        self,
        *,
        smb: Any,
        output_path: str,
        timeout_seconds: float,
        approval_check: Optional[Callable[[], str]] = None,
        status_update: Optional[Callable[..., None]] = None,
    ) -> Dict[str, Any]:
        deadline = time.monotonic() + max(30.0, float(timeout_seconds or 0.0))
        started_at = time.monotonic()
        launch_grace_seconds = self._windows_onboarding_launch_grace_seconds()
        last_output = ""
        last_error = ""
        last_status_update = 0.0
        seen_event_keys = set()
        saw_output_share_lock = False
        success_seen = False
        success_wait_deadline = 0.0
        success_stdout = ""
        timeline_seen = False

        def _approval_reference() -> str:
            if not callable(approval_check):
                return ""
            try:
                return str(approval_check() or "").strip()
            except Exception:
                return ""

        def _read_agent_log_tail(path: str, label: str) -> str:
            try:
                text = self._read_windows_smb_file(smb, path, share="C$")
            except Exception:
                return ""
            lines = str(text or "").splitlines()
            if not lines:
                return ""
            tail = "\n".join(lines[-80:])
            if len(tail) > 12000:
                tail = tail[-12000:]
            return f"[{label}]\n{tail}"

        def _windows_diagnostic_bundle(reason: str) -> str:
            parts = [reason]
            for path, label in (
                ("Borealis\\Agent\\Logs\\bootstrap.log", "bootstrap.log"),
                ("Borealis\\Agent\\Logs\\agent.error.log", "agent.error.log"),
                ("Borealis\\Agent\\Logs\\agent.log", "agent.log"),
                ("Borealis\\Agent\\Logs\\service_wrapper.log", "service_wrapper.log"),
                ("Borealis\\Agent\\Logs\\service.out.log", "service.out.log"),
                ("Borealis\\Agent\\Logs\\service.err.log", "service.err.log"),
                ("Borealis\\Temp\\Onboarding\\state.json", "onboarding state.json"),
                ("Borealis\\Temp\\Onboarding\\events.jsonl", "onboarding events.jsonl"),
            ):
                tail = _read_agent_log_tail(path, label)
                if tail:
                    parts.append(tail)
            return "\n\n".join(parts)

        def _success_timeout_result(reason: str, state: Optional[Mapping[str, Any]] = None, events: Optional[Sequence[Mapping[str, Any]]] = None) -> Dict[str, Any]:
            stdout = success_stdout or last_output
            if stdout:
                stdout += "\n"
            stdout += reason
            return {
                "exit_code": 1,
                "stdout": stdout,
                "stderr": _windows_diagnostic_bundle("windows_onboarding_approval_callback_timeout"),
                "state": dict(state or {}),
                "events": list(events or []),
                "target_hostname": _windows_onboarding_reported_hostname(stdout=stdout, stderr="", state=state, events=events),
            }

        def _note_success(stdout: str, detail: str) -> None:
            nonlocal success_seen, success_wait_deadline, success_stdout, last_status_update
            success_seen = True
            success_stdout = stdout or success_stdout or last_output
            if success_wait_deadline <= 0.0:
                success_wait_deadline = min(deadline, time.monotonic() + self._windows_onboarding_approval_wait_seconds())
            if callable(status_update) and (time.monotonic() - last_status_update) >= 5.0:
                status_update(detail, success_stdout, "", "")
                last_status_update = time.monotonic()

        while time.monotonic() < deadline:
            try:
                output = self._read_windows_smb_file(smb, output_path, share="C$")
                if output:
                    last_output = output
                    last_error = ""
                    saw_output_share_lock = False
                    exit_code = _windows_exit_code_from_output(output)
                    if exit_code is not None:
                        if exit_code == 73 and "__BOREALIS_ONBOARDING_ALREADY_RUNNING__=1" in output:
                            pass
                        elif exit_code == 0 and callable(approval_check):
                            approval_reference = _approval_reference()
                            if approval_reference:
                                return {
                                    "exit_code": 0,
                                    "stdout": output,
                                    "stderr": "",
                                    "approval_reference": approval_reference,
                                    "target_hostname": _windows_onboarding_reported_hostname(stdout=output),
                                }
                            _note_success(output, "Agent bootstrap completed; waiting for Borealis approval callback.")
                        else:
                            return {
                                "exit_code": exit_code,
                                "stdout": output,
                                "stderr": "",
                                "target_hostname": _windows_onboarding_reported_hostname(stdout=output),
                            }
            except Exception as exc:
                if _windows_smb_sharing_violation_error(exc):
                    saw_output_share_lock = True
                    last_error = ""
                elif not _windows_smb_object_missing_error(exc):
                    last_error = str(exc).strip() or exc.__class__.__name__
            state = self._read_windows_onboarding_state(smb)
            state_status = str(state.get("status") or "").strip().lower()
            state_detail = str(state.get("detail") or "").strip()
            events = self._read_windows_onboarding_events(smb)
            reported_hostname = _windows_onboarding_reported_hostname(stdout=last_output, stderr=last_error, state=state, events=events)
            if events and callable(status_update):
                for event in events:
                    try:
                        event_key = json.dumps(event, sort_keys=True)
                    except Exception:
                        event_key = str(event)
                    if event_key in seen_event_keys:
                        continue
                    seen_event_keys.add(event_key)
                    event_task = str(event.get("task") or "").strip()
                    event_detail = str(event.get("detail") or "").strip()
                    event_status = str(event.get("status") or "").strip()
                    event_timestamp = _parse_onboarding_event_timestamp(
                        event.get("created_at") or event.get("createdAt")
                    )
                    status_update(
                        event_task or event_detail or state_detail or "Running Agent Bootstrap",
                        last_output,
                        last_error,
                        reported_hostname,
                        event_timestamp,
                        event_status,
                    )
                    last_status_update = time.monotonic()
                    timeline_seen = True
            state_exit_code: Optional[int] = None
            try:
                state_exit_code = int(state.get("exit_code")) if state.get("exit_code") is not None else None
            except Exception:
                state_exit_code = None
            if state_status in {"already_enrolled", "already_pending"}:
                stdout = last_output
                if stdout:
                    stdout += "\n"
                if state_status == "already_enrolled":
                    stdout += "__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1"
                else:
                    stdout += f"__BOREALIS_ONBOARDING_ALREADY_PENDING__=1 status={state_status}"
                return {"exit_code": 73, "stdout": stdout, "stderr": "", "state": state, "events": events, "target_hostname": reported_hostname}
            if state_status == "failed":
                stdout = last_output
                if stdout:
                    stdout += "\n"
                stdout += "Borealis Windows onboarding state=failed."
                return {
                    "exit_code": state_exit_code if state_exit_code is not None else 1,
                    "stdout": stdout,
                    "stderr": _windows_diagnostic_bundle("windows_onboarding_state_failed"),
                    "state": state,
                    "events": events,
                    "target_hostname": reported_hostname,
                }
            approval_reference = _approval_reference()
            if approval_reference:
                stdout = last_output
                if stdout:
                    stdout += "\n"
                stdout += "Borealis approval detected before Windows remote output completed."
                return {
                    "exit_code": 0,
                    "stdout": stdout,
                    "stderr": "",
                    "approval_reference": approval_reference,
                    "state": state,
                    "events": events,
                    "target_hostname": reported_hostname,
                }
            if state_status in {"pending_approval", "completed", "success"}:
                stdout = last_output
                if stdout:
                    stdout += "\n"
                stdout += f"Borealis Windows onboarding state={state_status}."
                if not callable(approval_check):
                    return {"exit_code": 0, "stdout": stdout, "stderr": "", "state": state, "events": events, "target_hostname": reported_hostname}
                _note_success(stdout, "Agent bootstrap completed; waiting for Borealis approval callback.")
            if success_seen and success_wait_deadline > 0.0 and time.monotonic() >= success_wait_deadline:
                return _success_timeout_result(
                    "Agent bootstrap completed, but Borealis Engine did not receive an enrollment approval request.",
                    state=state,
                    events=events,
                )
            if not state_status and not saw_output_share_lock and (time.monotonic() - started_at) >= launch_grace_seconds and (
                not last_output or _windows_output_is_launcher_marker_only(last_output)
            ):
                detail = (
                    "Windows remote launch produced no installer output, state marker, or approval callback before launch grace expired."
                )
                if callable(status_update):
                    status_update(detail, last_output, last_error, reported_hostname)
                return {
                    "exit_code": 124,
                    "stdout": last_output,
                    "stderr": _windows_diagnostic_bundle(
                        f"windows_onboarding_child_launch_timeout{': ' + last_error if last_error else ''}"
                    ),
                }
            if callable(status_update) and not timeline_seen and (time.monotonic() - last_status_update) >= 5.0:
                detail = "Waiting for Windows SMB service enrollment output or approval callback."
                if state_status == "running" and state_detail:
                    detail = state_detail
                elif state_status:
                    detail = f"Waiting for Windows onboarding state '{state_status}' to complete."
                elif saw_output_share_lock:
                    detail = "Waiting for Windows onboarding output file lock to clear."
                elif "__BOREALIS_ONBOARDING_ALREADY_RUNNING__=1" in last_output:
                    detail = "Another Borealis onboarding deployment is active on this target; waiting for state or approval."
                status_update(detail, last_output, last_error, reported_hostname)
                last_status_update = time.monotonic()
            time.sleep(2.0)
        return {
            "exit_code": 124,
            "stdout": last_output,
            "stderr": _windows_diagnostic_bundle(f"windows_onboarding_timeout{': ' + last_error if last_error else ''}"),
            "target_hostname": _windows_onboarding_reported_hostname(stdout=last_output, stderr=last_error),
        }

    def _execute_windows_smb_scm_onboarding(
        self,
        *,
        host: str,
        port: int,
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
        timeout_seconds: float,
        approval_check: Optional[Callable[[], str]] = None,
        status_update: Optional[Callable[..., None]] = None,
    ) -> Dict[str, Any]:
        try:
            from impacket.dcerpc.v5 import scmr, transport  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"impacket_scm_unavailable:{exc}"}
        if self._windows_agent_exe_path() is None:
            return self._windows_agent_exe_unavailable_result()
        smb = None
        dce = None
        service_handle = None
        scm_handle = None
        stage = "SMB login"
        rpc_timeout = _env_non_negative_float("BOREALIS_WINDOWS_RPC_TIMEOUT_SECONDS", 10.0) or 10.0
        try:
            if callable(status_update):
                status_update("Connecting to Windows SMB for service enrollment.")
            smb = self._open_windows_smb_connection(host=host, port=port, credential=credential)
            if callable(status_update):
                status_update("Windows SMB service connection established.")
            stage = "C$ Agent.exe staging"
            if callable(status_update):
                status_update("Staging Borealis Agent.exe over C$.")
            service_name = f"BorealisOnboarding{uuid.uuid4().hex[:12]}"
            exe_abs, _config_abs, output_path = self._windows_smb_stage_agent_exe(
                smb,
                branch=branch,
                server_url=server_url,
                enrollment_code=enrollment_code,
                job_id=job_id,
                run_id=run_id,
                target=target,
                timeout_seconds=timeout_seconds,
                service_name=service_name,
            )
            start_warning = ""
            stage = "svcctl RPC bind"
            if callable(status_update):
                status_update("Binding to Windows Service Control Manager.")
            rpc_transport = transport.SMBTransport(host, int(port), r"\svcctl", smb_connection=smb)
            _set_impacket_timeout(rpc_transport, rpc_timeout)
            dce = rpc_transport.get_dce_rpc()
            _set_impacket_timeout(dce, rpc_timeout)
            dce.connect()
            dce.bind(scmr.MSRPC_UUID_SCMR)
            stage = "service creation"
            if callable(status_update):
                status_update("Creating transient Borealis onboarding service.")
            scm_resp = scmr.hROpenSCManagerW(dce)
            scm_handle = scm_resp["lpScHandle"]
            service_resp = scmr.hRCreateServiceW(
                dce,
                scm_handle,
                service_name,
                service_name,
                lpBinaryPathName=self._windows_quote_command_arg(exe_abs),
                dwStartType=scmr.SERVICE_DEMAND_START,
            )
            service_handle = service_resp["lpServiceHandle"]
            try:
                stage = "service start"
                if callable(status_update):
                    status_update("Starting transient Borealis onboarding service.")
                scmr.hRStartServiceW(dce, service_handle)
            except Exception as exc:
                if not _windows_service_start_error_allows_output_poll(exc):
                    raise
                start_warning = f"service start: {exc}"
            stage = "service output polling"
            if callable(status_update):
                status_update("Waiting for Windows SMB service enrollment output or approval callback.")
            result = self._poll_windows_smb_onboarding_output(
                smb=smb,
                output_path=output_path,
                timeout_seconds=timeout_seconds,
                approval_check=approval_check,
                status_update=status_update,
            )
            if start_warning and int(result.get("exit_code") or 0) != 0:
                stderr = str(result.get("stderr") or "")
                result["stderr"] = start_warning + (("\n" + stderr) if stderr else "")
            return result
        except Exception as exc:
            return {"exit_code": 1, "stdout": "", "stderr": f"{stage}: {exc}"}
        finally:
            if dce is not None and service_handle is not None:
                try:
                    scmr.hRDeleteService(dce, service_handle)
                except Exception:
                    pass
                try:
                    scmr.hRCloseServiceHandle(dce, service_handle)
                except Exception:
                    pass
            if dce is not None and scm_handle is not None:
                try:
                    scmr.hRCloseServiceHandle(dce, scm_handle)
                except Exception:
                    pass
            if dce is not None:
                try:
                    dce.disconnect()
                except Exception:
                    pass
            if smb is not None:
                try:
                    smb.logoff()
                except Exception:
                    pass

    def _execute_windows_scheduled_task_onboarding(
        self,
        *,
        host: str,
        port: int,
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
        timeout_seconds: float,
        approval_check: Optional[Callable[[], str]] = None,
        status_update: Optional[Callable[..., None]] = None,
    ) -> Dict[str, Any]:
        try:
            from impacket.dcerpc.v5 import tsch, transport  # type: ignore
            from impacket.dcerpc.v5.dtypes import NULL  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"impacket_tsch_unavailable:{exc}"}
        if self._windows_agent_exe_path() is None:
            return self._windows_agent_exe_unavailable_result()
        smb = None
        dce = None
        task_path = f"\\BorealisOnboarding{uuid.uuid4().hex[:12]}"
        stage = "SMB login"
        rpc_timeout = _env_non_negative_float("BOREALIS_WINDOWS_RPC_TIMEOUT_SECONDS", 10.0) or 10.0
        try:
            if callable(status_update):
                status_update("Connecting to Windows SMB for scheduled task enrollment.")
            smb = self._open_windows_smb_connection(host=host, port=port, credential=credential)
            if callable(status_update):
                status_update("Windows scheduled task connection established.")
            stage = "C$ Agent.exe staging"
            if callable(status_update):
                status_update("Staging Borealis Agent.exe over C$.")
            exe_abs, _config_abs, output_path = self._windows_smb_stage_agent_exe(
                smb,
                branch=branch,
                server_url=server_url,
                enrollment_code=enrollment_code,
                job_id=job_id,
                run_id=run_id,
                target=target,
                timeout_seconds=timeout_seconds,
            )
            command, arguments = self._windows_agent_prelaunch_cmd_args(exe_abs)
            xml = self._windows_task_xml(task_name=task_path, command=command, arguments=arguments)
            stage = "Task Scheduler RPC bind"
            if callable(status_update):
                status_update("Binding to Windows Task Scheduler RPC.")
            rpc_transport = transport.SMBTransport(host, int(port), r"\atsvc", smb_connection=smb)
            _set_impacket_timeout(rpc_transport, rpc_timeout)
            dce = rpc_transport.get_dce_rpc()
            _set_impacket_timeout(dce, rpc_timeout)
            dce.connect()
            dce.bind(tsch.MSRPC_UUID_TSCHS)
            stage = "task registration"
            if callable(status_update):
                status_update("Registering transient Borealis onboarding scheduled task.")
            tsch.hSchRpcRegisterTask(dce, task_path, xml, tsch.TASK_CREATE, NULL, tsch.TASK_LOGON_NONE)
            stage = "task start"
            if callable(status_update):
                status_update("Starting transient Borealis onboarding scheduled task.")
            tsch.hSchRpcRun(dce, task_path)
            stage = "task output polling"
            if callable(status_update):
                status_update("Waiting for Windows scheduled task enrollment output or approval callback.")
            result = self._poll_windows_smb_onboarding_output(
                smb=smb,
                output_path=output_path,
                timeout_seconds=timeout_seconds,
                approval_check=approval_check,
                status_update=status_update,
            )
            try:
                tsch.hSchRpcDelete(dce, task_path)
            except Exception:
                pass
            return result
        except Exception as exc:
            return {"exit_code": 1, "stdout": "", "stderr": f"{stage}: {exc}"}
        finally:
            if dce is not None:
                try:
                    dce.disconnect()
                except Exception:
                    pass
            if smb is not None:
                try:
                    smb.logoff()
                except Exception:
                    pass

    def _execute_windows_wmi_dcom_onboarding(
        self,
        *,
        host: str,
        port: int,
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
        timeout_seconds: float,
        approval_check: Optional[Callable[[], str]] = None,
        status_update: Optional[Callable[..., None]] = None,
    ) -> Dict[str, Any]:
        try:
            from impacket.dcerpc.v5.dcom import wmi  # type: ignore
            from impacket.dcerpc.v5.dcomrt import DCOMConnection  # type: ignore
            from impacket.dcerpc.v5.dtypes import NULL  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"impacket_wmi_unavailable:{exc}"}
        if self._windows_agent_exe_path() is None:
            return self._windows_agent_exe_unavailable_result()
        domain, username, password = _parse_windows_credential_parts(credential)
        smb = None
        dcom = None
        login = None
        stage = "SMB login"
        rpc_timeout = _env_non_negative_float("BOREALIS_WINDOWS_RPC_TIMEOUT_SECONDS", 10.0) or 10.0
        try:
            if callable(status_update):
                status_update("Connecting to Windows SMB for WMI/DCOM enrollment.")
            smb = self._open_windows_smb_connection(host=host, port=port, credential=credential)
            if callable(status_update):
                status_update("Windows WMI/DCOM connection established.")
            stage = "C$ Agent.exe staging"
            if callable(status_update):
                status_update("Staging Borealis Agent.exe over C$.")
            exe_abs, _config_abs, output_path = self._windows_smb_stage_agent_exe(
                smb,
                branch=branch,
                server_url=server_url,
                enrollment_code=enrollment_code,
                job_id=job_id,
                run_id=run_id,
                target=target,
                timeout_seconds=timeout_seconds,
            )
            command, arguments = self._windows_agent_prelaunch_cmd_args(exe_abs)
            command = f"{command} {arguments}"
            stage = "DCOM connect"
            if callable(status_update):
                status_update("Connecting to Windows WMI/DCOM.")
            dcom = DCOMConnection(host, username, password, domain, oxidResolver=False)
            _set_impacket_timeout(dcom, rpc_timeout)
            stage = "WMI login"
            if callable(status_update):
                status_update("Authenticating to Windows WMI.")
            interface = dcom.CoCreateInstanceEx(wmi.CLSID_WbemLevel1Login, wmi.IID_IWbemLevel1Login)
            _set_impacket_timeout(interface, rpc_timeout)
            login = wmi.IWbemLevel1Login(interface)
            services = login.NTLMLogin("//./root/cimv2", NULL, NULL)
            login.RemRelease()
            login = None
            stage = "WMI process creation"
            if callable(status_update):
                status_update("Creating Borealis onboarding process through WMI.")
            win32_process, _ = services.GetObject("Win32_Process")
            win32_process.Create(command, "C:\\Windows", None)
            stage = "WMI output polling"
            if callable(status_update):
                status_update("Waiting for Windows WMI/DCOM enrollment output or approval callback.")
            return self._poll_windows_smb_onboarding_output(
                smb=smb,
                output_path=output_path,
                timeout_seconds=timeout_seconds,
                approval_check=approval_check,
                status_update=status_update,
            )
        except Exception as exc:
            return {"exit_code": 1, "stdout": "", "stderr": f"{stage}: {exc}"}
        finally:
            if login is not None:
                try:
                    login.RemRelease()
                except Exception:
                    pass
            if dcom is not None:
                try:
                    dcom.disconnect()
                except Exception:
                    pass
            if smb is not None:
                try:
                    smb.logoff()
                except Exception:
                    pass

    def _winrm_run_ps_checked(self, session: Any, script: str, *, stage: str) -> None:
        quiet_script = (
            "$ProgressPreference='SilentlyContinue';"
            "$VerbosePreference='SilentlyContinue';"
            "$InformationPreference='SilentlyContinue';"
            f"{script}"
        )
        result = session.run_ps(quiet_script)
        stdout_raw = getattr(result, "std_out", b"") or b""
        stderr_raw = getattr(result, "std_err", b"") or b""
        stdout = stdout_raw.decode("utf-8", errors="replace") if isinstance(stdout_raw, bytes) else str(stdout_raw or "")
        stderr = stderr_raw.decode("utf-8", errors="replace") if isinstance(stderr_raw, bytes) else str(stderr_raw or "")
        exit_code = int(getattr(result, "status_code", 1) or 0)
        if exit_code != 0:
            raise RuntimeError(f"{stage}: {stderr or stdout or 'WinRM PowerShell command failed'}")

    def _winrm_stage_bytes(self, session: Any, remote_path: str, payload: bytes, *, stage: str) -> None:
        path_literal = _powershell_single_quoted(remote_path)
        init_script = (
            "$ErrorActionPreference='Stop';"
            f"$path={path_literal};"
            "$parent=Split-Path -Parent $path;"
            "New-Item -ItemType Directory -Force -Path $parent | Out-Null;"
            "$tmp=$path+'.b64';"
            "Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue;"
            "Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue;"
        )
        self._winrm_run_ps_checked(session, init_script, stage=f"{stage} init")
        encoded = base64.b64encode(payload).decode("ascii")
        chunk_size = 60000
        for offset in range(0, len(encoded), chunk_size):
            chunk = encoded[offset : offset + chunk_size]
            append_script = (
                "$ErrorActionPreference='Stop';"
                f"$tmp={_powershell_single_quoted(remote_path + '.b64')};"
                f"Add-Content -LiteralPath $tmp -Value {_powershell_single_quoted(chunk)} -NoNewline -Encoding ASCII;"
            )
            self._winrm_run_ps_checked(session, append_script, stage=f"{stage} chunk")
        finalize_script = (
            "$ErrorActionPreference='Stop';"
            f"$path={path_literal};"
            "$tmp=$path+'.b64';"
            "$raw=Get-Content -LiteralPath $tmp -Raw;"
            "[IO.File]::WriteAllBytes($path,[Convert]::FromBase64String($raw));"
            "Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue;"
        )
        self._winrm_run_ps_checked(session, finalize_script, stage=f"{stage} finalize")

    def _execute_windows_winrm_onboarding(
        self,
        *,
        host: str,
        port: int,
        smb_port: int,
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
        timeout_seconds: float,
        approval_check: Optional[Callable[[], str]] = None,
        status_update: Optional[Callable[..., None]] = None,
    ) -> Dict[str, Any]:
        try:
            import winrm  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"pywinrm_unavailable:{exc}"}
        if self._windows_agent_exe_path() is None:
            return self._windows_agent_exe_unavailable_result()
        agent_exe_path = self._windows_agent_exe_path()
        if agent_exe_path is None:
            return self._windows_agent_exe_unavailable_result()
        metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
        transport_mode = str(metadata.get("winrm_transport") or "ntlm").strip().lower() or "ntlm"
        scheme = "https" if int(port) == 5986 or transport_mode in {"ssl", "credssp"} else "http"
        endpoint = f"{scheme}://{host}:{int(port)}/wsman"
        bundle_path: Optional[Path] = None
        try:
            session = winrm.Session(
                endpoint,
                auth=(str(credential.get("username") or ""), str(credential.get("password") or "")),
                transport=transport_mode,
                server_cert_validation="ignore",
                operation_timeout_sec=max(20, min(600, int(timeout_seconds or 900))),
                read_timeout_sec=max(30, min(660, int(timeout_seconds or 900) + 30)),
            )
            if callable(status_update):
                status_update("Windows WinRM connection established.")
            branch_ref = str(branch or "main").strip() or "main"
            exe_abs = f"C:\\Borealis\\Temp\\Onboarding\\Agent-{int(run_id)}-{uuid.uuid4().hex[:8]}.exe"
            config_abs = "C:\\Borealis\\Temp\\Onboarding\\bootstrapper-config.json"
            payload_abs = "C:\\Borealis\\Temp\\Onboarding\\agent-payload.zip"
            manifest_abs = "C:\\Borealis\\Temp\\Onboarding\\agent-payload-manifest.json"
            output_abs = "C:\\Borealis\\Temp\\Onboarding\\stdout.log"
            state_abs = "C:\\Borealis\\Temp\\Onboarding\\state.json"
            events_abs = "C:\\Borealis\\Temp\\Onboarding\\events.jsonl"
            bundle_path, bundle_sha256 = self._windows_create_agent_payload_bundle()
            config = {
                "install_dir": "C:\\Borealis",
                "repo_url": DEFAULT_BOREALIS_REPO_GIT_URL,
                "repo_ref": branch_ref,
                "server_url": str(server_url or ""),
                "site_enrollment_code": str(enrollment_code or ""),
                "agent_bundle_path": payload_abs,
                "agent_bundle_sha256": bundle_sha256,
                "manifest_path": manifest_abs,
                "state_path": state_abs,
                "events_path": events_abs,
                "stdout_path": output_abs,
                "stderr_path": output_abs,
                "timeout_seconds": max(60, int(max(60.0, float(timeout_seconds or 900.0)) - 30)),
                "job_id": int(job_id),
                "run_id": int(run_id),
                "target": str(target or "").strip(),
                "noninteractive": True,
            }
            manifest = {
                "repo_ref": branch_ref,
                "sha256": bundle_sha256,
                "created_at": int(time.time()),
            }
            cleanup_script = (
                "$ErrorActionPreference='Stop';"
                "New-Item -ItemType Directory -Force -Path 'C:\\Borealis\\Temp\\Onboarding' | Out-Null;"
                "Remove-Item -LiteralPath 'C:\\Borealis\\Temp\\Onboarding\\state.json' -Force -ErrorAction SilentlyContinue;"
                "Remove-Item -LiteralPath 'C:\\Borealis\\Temp\\Onboarding\\events.jsonl' -Force -ErrorAction SilentlyContinue;"
                "Remove-Item -LiteralPath 'C:\\Borealis\\Temp\\Onboarding\\stdout.log' -Force -ErrorAction SilentlyContinue;"
            )
            self._winrm_run_ps_checked(session, cleanup_script, stage="WinRM onboarding workspace cleanup")
            if callable(status_update):
                status_update("Staging Borealis Agent.exe over WinRM.")
            self._winrm_stage_bytes(session, exe_abs, agent_exe_path.read_bytes(), stage="WinRM Agent.exe staging")
            self._winrm_stage_bytes(session, payload_abs, bundle_path.read_bytes(), stage="WinRM payload staging")
            self._winrm_stage_bytes(
                session,
                config_abs,
                json.dumps(config, separators=(",", ":")).encode("utf-8"),
                stage="WinRM bootstrap config staging",
            )
            self._winrm_stage_bytes(
                session,
                manifest_abs,
                json.dumps(manifest, separators=(",", ":")).encode("utf-8"),
                stage="WinRM bootstrap manifest staging",
            )
            if callable(status_update):
                status_update("Running Agent Bootstrap")
            command, arguments = self._windows_agent_prelaunch_cmd_args(exe_abs)
            result = session.run_cmd(command, [arguments])
            stdout_raw = getattr(result, "std_out", b"") or b""
            stderr_raw = getattr(result, "std_err", b"") or b""
            stdout = stdout_raw.decode("utf-8", errors="replace") if isinstance(stdout_raw, bytes) else str(stdout_raw or "")
            stderr = stderr_raw.decode("utf-8", errors="replace") if isinstance(stderr_raw, bytes) else str(stderr_raw or "")
            exit_code = int(getattr(result, "status_code", 1) or 0)
            marker_code = _windows_exit_code_from_output(stdout)
            if marker_code is not None:
                exit_code = marker_code
            return {
                "exit_code": exit_code,
                "stdout": stdout,
                "stderr": stderr,
                "target_hostname": _windows_onboarding_reported_hostname(stdout=stdout, stderr=stderr),
            }
        except Exception as exc:
            return {"exit_code": 1, "stdout": "", "stderr": str(exc)}
        finally:
            if bundle_path is not None:
                try:
                    shutil.rmtree(bundle_path.parent)
                except Exception:
                    pass

    def _run_windows_onboarding_target(
        self,
        *,
        row: Dict[str, Any],
        site: Dict[str, Any],
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        job_id: int,
        run_id: int,
        windows_methods: Sequence[str],
        winrm_port: int,
        smb_port: Optional[int] = None,
        credential_label: str = "",
        final_on_failure: bool = True,
    ) -> str:
        row_id = int(row["id"])
        host = str(row.get("target_address") or row.get("target_hostname") or "").strip()
        smb_port = int(smb_port or row.get("ssh_port") or DEFAULT_ONBOARDING_WINDOWS_PORT)
        redactions = [site.get("enrollment_code"), server_url, credential.get("password")]
        connection_label = str(credential_label or self._onboarding_credential_label(credential)).strip()
        self._update_onboarding_target_row(
            row_id,
            status=ONBOARDING_STATUS_RUNNING,
            detail=f"Establishing Connection to Remote Device: {connection_label}" if connection_label else "Trying Windows remote enrollment.",
            redactions=redactions,
        )

        timeout_seconds = self._windows_onboarding_observation_timeout_seconds()
        normalized_methods = _windows_onboarding_methods_with_required_fallbacks(
            [
                method
                for method in (str(item or "").strip().lower() for item in windows_methods or ONBOARDING_WINDOWS_METHODS)
                if method in ONBOARDING_WINDOWS_METHODS
            ]
        )
        stdout_parts: List[str] = []
        stderr_parts: List[str] = []
        port_failures = 0
        attempted_methods = 0

        def _approval_check() -> str:
            return self._lookup_onboarding_approval(job_id=job_id, run_id=run_id, target=host)

        def _status_update(
            detail: str,
            stdout: str = "",
            stderr: str = "",
            target_hostname: str = "",
            event_timestamp: Optional[int] = None,
            event_status: str = "",
        ) -> None:
            clean_update_detail = str(detail or "")
            update_detail_lower = clean_update_detail.strip().lower()
            if "connection established" in update_detail_lower and connection_label and ":" not in clean_update_detail:
                protocol = _onboarding_windows_protocol_name(update_detail_lower)
                if protocol:
                    clean_update_detail = f"Connection Established using {protocol}: {connection_label}"
            merged_stdout = "\n\n".join([part for part in [*stdout_parts, str(stdout or "")] if part])
            merged_stderr = "\n\n".join([part for part in [*stderr_parts, str(stderr or "")] if part])
            reported_hostname = _windows_onboarding_reported_hostname(
                stdout=merged_stdout,
                stderr=merged_stderr,
            ) or _clean_onboarding_reported_hostname(target_hostname)
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail=clean_update_detail,
                stdout=merged_stdout,
                stderr=merged_stderr,
                target_hostname=reported_hostname,
                redactions=redactions,
                event_timestamp=event_timestamp,
                event_status=event_status,
            )

        def _mark_already_enrolled_if_active(combined_stdout: str, combined_stderr: str, reported_hostname: str = "") -> bool:
            known_candidates = [
                host,
                reported_hostname,
                row.get("target_address"),
                row.get("target_hostname"),
                row.get("target_input"),
            ]
            active_hostname = self._lookup_active_onboarding_target_hostname_from_candidates(known_candidates, site.get("id"))
            if not active_hostname:
                return False
            self._update_onboarding_target_row(
                row_id,
                status="already_enrolled",
                detail="Existing Borealis Agent is already enrolled and active.",
                stdout=combined_stdout,
                stderr=combined_stderr,
                approval_reference="",
                target_hostname=active_hostname,
                finished=True,
                redactions=redactions,
            )
            return True

        for method in normalized_methods:
            method_label = _windows_method_label(method)
            if method == ONBOARDING_WINDOWS_METHOD_WINRM:
                method_ports = [int(winrm_port)]
            elif method == ONBOARDING_WINDOWS_METHOD_WMI_DCOM:
                method_ports = [smb_port, 135]
            else:
                method_ports = [smb_port]
            method_port_failures = []
            for method_port in method_ports:
                tcp_error = self._preflight_remote_port(host=host, port=method_port, attempts=1, timeout_seconds=3.0)
                if tcp_error:
                    method_port_failures.append((method_port, tcp_error))
            if method_port_failures:
                port_failures += 1
                for method_port, tcp_error in method_port_failures:
                    stderr_parts.append(f"[{method_label}] port {method_port} unreachable: {tcp_error}")
                continue
            attempted_methods += 1
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail=f"Establishing Connection to Remote Device: {connection_label}" if connection_label else f"Trying Windows {method_label} enrollment.",
                stdout="\n\n".join(stdout_parts),
                stderr="\n\n".join(stderr_parts),
                redactions=redactions,
            )
            if method == ONBOARDING_WINDOWS_METHOD_SMB_SCM:
                result = self._execute_windows_smb_scm_onboarding(
                    host=host,
                    port=smb_port,
                    credential=credential,
                    branch=branch,
                    server_url=server_url,
                    enrollment_code=str(site.get("enrollment_code") or ""),
                    job_id=job_id,
                    run_id=run_id,
                    target=host,
                    timeout_seconds=timeout_seconds,
                    approval_check=_approval_check,
                    status_update=_status_update,
                )
            elif method == ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK:
                result = self._execute_windows_scheduled_task_onboarding(
                    host=host,
                    port=smb_port,
                    credential=credential,
                    branch=branch,
                    server_url=server_url,
                    enrollment_code=str(site.get("enrollment_code") or ""),
                    job_id=job_id,
                    run_id=run_id,
                    target=host,
                    timeout_seconds=timeout_seconds,
                    approval_check=_approval_check,
                    status_update=_status_update,
                )
            elif method == ONBOARDING_WINDOWS_METHOD_WMI_DCOM:
                result = self._run_windows_onboarding_method_in_child(
                    method=self._execute_windows_wmi_dcom_onboarding,
                    method_kwargs={
                        "host": host,
                        "port": smb_port,
                        "credential": credential,
                        "branch": branch,
                        "server_url": server_url,
                        "enrollment_code": str(site.get("enrollment_code") or ""),
                        "job_id": job_id,
                        "run_id": run_id,
                        "target": host,
                        "timeout_seconds": timeout_seconds,
                    },
                    timeout_seconds=self._windows_wmi_dcom_timeout_seconds(),
                    timeout_label="Windows WMI/DCOM enrollment",
                    approval_check=_approval_check,
                    status_update=_status_update,
                )
            else:
                result = self._execute_windows_winrm_onboarding(
                    host=host,
                    port=int(winrm_port),
                    smb_port=smb_port,
                    credential=credential,
                    branch=branch,
                    server_url=server_url,
                    enrollment_code=str(site.get("enrollment_code") or ""),
                    job_id=job_id,
                    run_id=run_id,
                    target=host,
                    timeout_seconds=timeout_seconds,
                    approval_check=_approval_check,
                    status_update=_status_update,
                )
            exit_code = int(result.get("exit_code") or 0)
            stdout = str(result.get("stdout") or "")
            stderr = str(result.get("stderr") or "")
            reported_hostname = (
                _clean_onboarding_reported_hostname(result.get("target_hostname"))
                or _windows_onboarding_reported_hostname(
                    stdout=stdout,
                    stderr=stderr,
                    state=result.get("state") if isinstance(result.get("state"), Mapping) else None,
                    events=result.get("events") if isinstance(result.get("events"), (list, tuple)) else None,
                )
            )
            if stdout:
                stdout_parts.append(f"[{method_label}]\n{stdout}")
            if stderr:
                stderr_parts.append(f"[{method_label}]\n{stderr}")
            if exit_code == 0:
                combined_stdout = "\n\n".join(stdout_parts)
                combined_stderr = "\n\n".join(stderr_parts)
                if _windows_onboarding_existing_task_running_without_redeploy(stdout=combined_stdout, stderr=combined_stderr):
                    known_candidates = [host, reported_hostname]
                    known_hostname = self._lookup_known_onboarding_target_hostname_from_candidates(known_candidates, site.get("id"))
                    if known_hostname or any(self._onboarding_target_already_known(str(candidate or ""), site.get("id")) for candidate in known_candidates):
                        terminal_status = "already_enrolled"
                        reported_hostname = reported_hostname or known_hostname
                        self._update_onboarding_target_row(
                            row_id,
                            status=terminal_status,
                            detail="Existing Borealis Agent is already enrolled and active.",
                            stdout=combined_stdout,
                            stderr=combined_stderr,
                            approval_reference="",
                            target_hostname=reported_hostname,
                            finished=True,
                            redactions=redactions,
                        )
                        return terminal_status
                approval_reference = str(result.get("approval_reference") or "").strip()
                deadline = time.monotonic() + self._windows_onboarding_approval_wait_seconds()
                last_active_check = 0.0
                while time.monotonic() < deadline:
                    approval_reference = approval_reference or _approval_check()
                    if approval_reference:
                        break
                    if (time.monotonic() - last_active_check) >= 5.0:
                        last_active_check = time.monotonic()
                        if _mark_already_enrolled_if_active(combined_stdout, combined_stderr, reported_hostname):
                            return "already_enrolled"
                    time.sleep(1.0)
                if not approval_reference:
                    if _mark_already_enrolled_if_active(combined_stdout, combined_stderr, reported_hostname):
                        return "already_enrolled"
                    failure_detail = "Agent completed local bootstrap, but Borealis Engine did not receive an approval request."
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_FAILED,
                        detail=failure_detail,
                        stdout=combined_stdout,
                        stderr="\n\n".join([part for part in (combined_stderr, "windows_onboarding_approval_callback_timeout") if part]),
                        approval_reference="",
                        target_hostname=reported_hostname,
                        finished=True,
                        redactions=redactions,
                    )
                    return ONBOARDING_STATUS_FAILED
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_WAITING_APPROVAL,
                    detail=f"Agent installed through Windows {method_label}. Device approval pending operator action.",
                    stdout=combined_stdout,
                    stderr=combined_stderr,
                    approval_reference=approval_reference,
                    target_hostname=reported_hostname,
                    finished=True,
                    redactions=redactions,
                )
                return ONBOARDING_STATUS_WAITING_APPROVAL
            if "windows_onboarding_approval_callback_timeout" in f"{stdout}\n{stderr}":
                if _mark_already_enrolled_if_active("\n\n".join(stdout_parts), "\n\n".join(stderr_parts), reported_hostname):
                    return "already_enrolled"
                self._update_onboarding_target_row(
                    row_id,
                    status=ONBOARDING_STATUS_FAILED,
                    detail="Agent completed local bootstrap, but Borealis Engine did not receive an approval request.",
                    stdout="\n\n".join(stdout_parts),
                    stderr="\n\n".join(stderr_parts),
                    approval_reference="",
                    target_hostname=reported_hostname,
                    finished=True,
                    redactions=redactions,
                )
                return ONBOARDING_STATUS_FAILED
            skip_detail = _windows_onboarding_skip_detail(stdout=stdout, stderr=stderr)
            if skip_detail:
                repaired = _windows_onboarding_repair_succeeded(stdout=stdout, stderr=stderr)
                already_enrolled = "__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1" in f"{stdout}\n{stderr}"
                terminal_status = "already_enrolled" if already_enrolled else ("completed" if repaired else ONBOARDING_STATUS_SKIPPED)
                self._update_onboarding_target_row(
                    row_id,
                    status=terminal_status,
                    detail=skip_detail,
                    stdout="\n\n".join(stdout_parts),
                    stderr="\n\n".join(stderr_parts),
                    target_hostname=reported_hostname,
                    finished=True,
                    redactions=redactions,
                )
                return terminal_status
            if _windows_onboarding_result_may_have_created_approval(method=method, stdout=stdout, stderr=stderr):
                approval_reference = str(result.get("approval_reference") or "").strip()
                deadline = time.monotonic() + 10.0
                while time.monotonic() < deadline:
                    approval_reference = approval_reference or _approval_check()
                    if approval_reference:
                        break
                    time.sleep(1.0)
                if approval_reference:
                    self._update_onboarding_target_row(
                        row_id,
                        status=ONBOARDING_STATUS_WAITING_APPROVAL,
                        detail=f"Agent reached approval queue through Windows {method_label}; remote status channel timed out after launch.",
                        stdout="\n\n".join(stdout_parts),
                        stderr="\n\n".join(stderr_parts),
                        approval_reference=approval_reference,
                        target_hostname=reported_hostname,
                        finished=True,
                        redactions=redactions,
                    )
                    return ONBOARDING_STATUS_WAITING_APPROVAL
            failure_hint = _onboarding_failure_hint(stdout=stdout, stderr=stderr, redactions=redactions)
            stderr_parts.append(f"[{method_label}] failed with exit code {exit_code}.{(' ' + failure_hint) if failure_hint else ''}")

        combined_stdout = "\n\n".join(stdout_parts)
        combined_stderr = "\n\n".join(stderr_parts)
        combined_reported_hostname = _windows_onboarding_reported_hostname(stdout=combined_stdout, stderr=combined_stderr)
        if port_failures and attempted_methods == 0:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_UNREACHABLE,
                detail="Windows remote enrollment ports unreachable.",
                stdout=combined_stdout,
                stderr=combined_stderr,
                target_hostname=combined_reported_hostname,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_UNREACHABLE

        auth_failure = _windows_onboarding_auth_failure(stdout=combined_stdout, stderr=combined_stderr)
        if not final_on_failure:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail=f"Establishing Connection to Remote Device: {connection_label} Failed",
                stdout=combined_stdout,
                stderr=combined_stderr,
                target_hostname=combined_reported_hostname,
                redactions=redactions,
            )
            return "credential_failed"

        self._update_onboarding_target_row(
            row_id,
            status=ONBOARDING_STATUS_FAILED,
            detail=(
                "Windows authentication failed for all stored Windows credentials."
                if auth_failure
                else (
                    "Windows automatic onboarding failed through SMB service, scheduled task, WMI/DCOM, and WinRM. "
                    "Manual agent installation required; target security policy appears too locked down for remote enrollment."
                )
            ),
            stdout=combined_stdout,
            stderr=combined_stderr,
            target_hostname=combined_reported_hostname,
            finished=True,
            redactions=redactions,
        )
        return ONBOARDING_STATUS_FAILED

    def _dispatch_onboarding_run(
        self,
        *,
        job_id: int,
        run_row_id: int,
        scheduled_ts: int,
        components: Sequence[Any],
        targets: Sequence[Any],
        credential_id: Optional[int],
    ) -> None:
        if callable(self._onboarding_run_dispatcher):
            self._onboarding_run_dispatcher(
                job_id=int(job_id),
                run_row_id=int(run_row_id),
                scheduled_ts=int(scheduled_ts),
                components=list(components or []),
                targets=list(targets or []),
                credential_id=credential_id,
            )
            return
        with self._onboarding_dispatch_lock:
            if int(run_row_id) in self._onboarding_running_runs:
                return
            self._onboarding_running_runs.add(int(run_row_id))

        def _runner():
            try:
                self._run_onboarding_job(
                    job_id=int(job_id),
                    run_row_id=int(run_row_id),
                    scheduled_ts=int(scheduled_ts),
                    components=components,
                    targets=targets,
                    credential_id=credential_id,
                )
            finally:
                with self._onboarding_dispatch_lock:
                    self._onboarding_running_runs.discard(int(run_row_id))

        threading.Thread(
            target=_runner,
            name=f"borealis-onboarding-run-{int(run_row_id)}",
            daemon=True,
        ).start()

    def _run_onboarding_job(
        self,
        *,
        job_id: int,
        run_row_id: int,
        scheduled_ts: int,
        components: Sequence[Any],
        targets: Sequence[Any],
        credential_id: Optional[int],
    ) -> None:
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?, started_ts=?, updated_at=?
                 WHERE id=?
                """,
                (RUN_STATUS_RUNNING, now, now, int(run_row_id)),
            )
            conn.commit()
        finally:
            conn.close()

        final_status = RUN_STATUS_FAILED
        final_error = ""
        try:
            config, config_error = self._onboarding_scope_config(components=components, targets=targets)
            if config_error:
                raise RuntimeError(config_error)
            site = self._load_onboarding_site(config.get("site_id"))
            if not site or not str(site.get("enrollment_code") or "").strip():
                raise RuntimeError("Selected site has no enrollment code.")
            server_url = self._public_base_url()
            if not server_url:
                raise RuntimeError("Engine public base URL is not configured.")
            platform = str(config.get("agent_platform") or ONBOARDING_PLATFORM_LINUX).strip().lower()
            windows_credentials, linux_credentials = self._load_onboarding_credential_lists(
                config=config,
                credential_id=credential_id,
            )
            if platform == ONBOARDING_PLATFORM_LINUX and not linux_credentials:
                raise RuntimeError("Linux onboarding requires at least one Stored Linux Credential.")
            if platform == ONBOARDING_PLATFORM_WINDOWS and not windows_credentials:
                raise RuntimeError("Windows onboarding requires at least one Stored Windows Credential.")
            if platform == ONBOARDING_PLATFORM_AUTO and not windows_credentials and not linux_credentials:
                raise RuntimeError("Automatic onboarding requires at least one stored credential.")

            expanded_targets, parse_errors = parse_onboarding_scope(
                config.get("entries") or [],
                default_port=int(config.get("transport_port") or DEFAULT_ONBOARDING_SSH_PORT),
                max_targets=self._onboarding_target_cap(),
            )
            excluded_targets, exclusion_errors = parse_onboarding_scope(
                config.get("exclusions") or [],
                default_port=int(config.get("transport_port") or DEFAULT_ONBOARDING_SSH_PORT),
                max_targets=max(self._onboarding_target_cap(), len(expanded_targets) or 1),
            )
            if excluded_targets:
                excluded_keys = {
                    f"{str(target.get('host') or '').strip().lower()}:{int(target.get('port') or config.get('transport_port') or DEFAULT_ONBOARDING_SSH_PORT)}"
                    for target in excluded_targets
                }
                expanded_targets = [
                    target
                    for target in expanded_targets
                    if f"{str(target.get('host') or '').strip().lower()}:{int(target.get('port') or config.get('transport_port') or DEFAULT_ONBOARDING_SSH_PORT)}" not in excluded_keys
                ]
            if exclusion_errors:
                parse_errors.extend([f"exclusion_{error}" for error in exclusion_errors])
            if not expanded_targets:
                final_status = RUN_STATUS_SKIPPED
                final_error = "; ".join(parse_errors) if parse_errors else SKIP_REASON_NO_TARGETS
                return

            existing_rows = self._load_onboarding_target_rows(job_id, scheduled_ts)
            if not existing_rows:
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    insert_now = _now_ts()
                    raw_input_by_target = _onboarding_raw_input_map(
                        config.get("entries") or [],
                        default_port=int(config.get("transport_port") or DEFAULT_ONBOARDING_SSH_PORT),
                    )
                    for target in expanded_targets:
                        target_host = str(target.get("host") or "").strip()
                        target_port = int(target.get("port") or config.get("transport_port") or DEFAULT_ONBOARDING_SSH_PORT)
                        target_key = f"{target_host.lower()}:{target_port}"
                        cur.execute(
                            """
                            INSERT INTO scheduled_job_onboarding_targets(
                                run_id,
                                job_id,
                                scheduled_ts,
                                site_id,
                                target_input,
                                target_address,
                                target_hostname,
                                ssh_port,
                                status,
                                detail,
                                created_at,
                                updated_at
                            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
                            """,
                            (
                                int(run_row_id),
                                int(job_id),
                                int(scheduled_ts),
                                int(site["id"]),
                                raw_input_by_target.get(target_key) or target.get("input") or target_host,
                                target_host,
                                target_host,
                                target_port,
                                ONBOARDING_STATUS_PENDING,
                                "",
                                insert_now,
                                insert_now,
                            ),
                        )
                        row_id = int(cur.lastrowid or 0)
                        if row_id > 0:
                            self._record_onboarding_target_event(
                                cur,
                                row_id=row_id,
                                status=ONBOARDING_STATUS_PENDING,
                                detail="Spinning-Up Site-Worker Container",
                                now=insert_now,
                            )
                    conn.commit()
                finally:
                    conn.close()
                existing_rows = self._load_onboarding_target_rows(job_id, scheduled_ts)

            statuses: List[str] = []
            configured_concurrency = int(config.get("onboarding_concurrency") or self._onboarding_concurrency())
            max_workers = max(1, min(configured_concurrency, len(existing_rows)))
            import concurrent.futures

            with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
                futures = [
                    executor.submit(
                        self._run_single_onboarding_target,
                        row=row,
                        site=site,
                        branch=str(config.get("install_branch") or "main"),
                        server_url=server_url,
                        job_id=job_id,
                        run_id=run_row_id,
                        platform=platform,
                        linux_credentials=linux_credentials,
                        windows_credentials=windows_credentials,
                        ssh_port=int(config.get("ssh_port") or DEFAULT_ONBOARDING_SSH_PORT),
                        windows_port=int(config.get("windows_port") or DEFAULT_ONBOARDING_WINDOWS_PORT),
                        windows_methods=list(config.get("onboarding_methods") or ONBOARDING_WINDOWS_METHODS),
                        winrm_port=int(config.get("winrm_port") or DEFAULT_ONBOARDING_WINRM_PORT),
                    )
                    for row in existing_rows
                ]
                for future in concurrent.futures.as_completed(futures):
                    try:
                        statuses.append(str(future.result() or ""))
                    except Exception as exc:
                        statuses.append(ONBOARDING_STATUS_FAILED)
                        self._log_event(
                            "onboarding target worker failed",
                            level="ERROR",
                            job_id=job_id,
                            run_id=run_row_id,
                            extra={"error": str(exc)},
                        )
            buckets = [_onboarding_status_bucket(status) for status in statuses]
            if buckets and all(bucket == "success" for bucket in buckets):
                final_status = RUN_STATUS_SUCCESS
            elif buckets and any(bucket == "success" for bucket in buckets):
                final_status = RUN_STATUS_WARNING
            elif buckets and all(bucket == "skipped" for bucket in buckets):
                final_status = RUN_STATUS_SKIPPED
            else:
                final_status = RUN_STATUS_FAILED
            if parse_errors and final_status == RUN_STATUS_SUCCESS:
                final_status = RUN_STATUS_WARNING
            final_error = "; ".join(parse_errors[:5]) if parse_errors else ""
        except (AegisLockedError, AegisDataCorruptionError, AegisSecretResetRequiredError) as exc:
            final_status = RUN_STATUS_FAILED
            final_error = str(exc)
        except Exception as exc:
            final_status = RUN_STATUS_FAILED
            final_error = str(exc)
        finally:
            finished = _now_ts()
            conn = self._conn()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=?,
                           updated_at=?,
                           error=?
                     WHERE id=?
                    """,
                    (final_status, finished, finished, final_error, int(run_row_id)),
                )
                conn.commit()
            finally:
                conn.close()




__all__ = [
    "DEFAULT_ONBOARDING_CONCURRENCY",
    "DEFAULT_ONBOARDING_SSH_PORT",
    "DEFAULT_ONBOARDING_WINDOWS_PORT",
    "DEFAULT_ONBOARDING_WINRM_PORT",
    "DEFAULT_ONBOARDING_TARGET_CAP",
    "JOB_KIND_ONBOARDING",
    "ONBOARDING_COMPONENT_KIND",
    "ONBOARDING_COMPONENT_NAME",
    "ONBOARDING_STATUS_PENDING",
    "ONBOARDING_STATUS_RUNNING",
    "ONBOARDING_STATUS_WAITING_APPROVAL",
    "ONBOARDING_STATUS_SKIPPED",
    "ONBOARDING_STATUS_FAILED",
    "ONBOARDING_STATUS_UNSUPPORTED_OS",
    "ONBOARDING_STATUS_UNREACHABLE",
    "ONBOARDING_PLATFORM_AUTO",
    "ONBOARDING_PLATFORM_LINUX",
    "ONBOARDING_PLATFORM_WINDOWS",
    "ONBOARDING_WINDOWS_METHODS",
    "OnboardingSchedulerMixin",
    "ONBOARDING_SNIPPET_LIMIT",
    "coerce_scope_entries",
    "parse_onboarding_scope",
    "sanitize_output",
]
