# ======================================================
# Data\Engine\services\API\scheduled_jobs\job_scheduler.py
# Description: Engine-native scheduled job management service that
#              provides CRUD endpoints, background execution, and run
#              tracking without relying on the legacy server module.
# ======================================================

"""Engine scheduled job orchestration and API bindings."""

from __future__ import annotations

import base64
import html
import io
import json
import logging
import math
import os
import re
import shlex
import shutil
import socket
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Callable, Dict, List, Mapping, Optional, Sequence, Tuple
from urllib.parse import quote as url_quote

from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ...ansible.runtime_settings import load_ansible_runner_settings
from ...ansible.ssh_auth import apply_ssh_credential_host_vars
from ...assemblies.service import AssemblyRuntimeService
from ...aegis_cipher import (
    AegisDataCorruptionError,
    AegisLockedError,
    AegisSecretResetRequiredError,
    credential_secret_reset_required,
)
from ....db import dbapi as sqlite3
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...filters.matcher import DeviceFilterMatcher
from ...activity_history import insert_activity_history_row, update_activity_history_row
from ..devices.session_dispatch import build_currentuser_dispatch_fields
from .onboarding import (
    DEFAULT_ONBOARDING_CONCURRENCY,
    DEFAULT_ONBOARDING_SSH_PORT,
    DEFAULT_ONBOARDING_WINDOWS_PORT,
    DEFAULT_ONBOARDING_WINRM_PORT,
    DEFAULT_ONBOARDING_TARGET_CAP,
    parse_onboarding_scope,
    sanitize_output,
)
from .targets import normalize_targets_for_save

_WINRM_USERNAME_VAR = "__borealis_winrm_username"
_WINRM_PASSWORD_VAR = "__borealis_winrm_password"
_WINRM_TRANSPORT_VAR = "__borealis_winrm_transport"
_PREPARED_REMOTE_PREFLIGHT_ATTEMPTS_ENV = "BOREALIS_SHARED_ANSIBLE_PREPARED_PREFLIGHT_ATTEMPTS"
_PREPARED_REMOTE_PREFLIGHT_RETRY_DELAY_ENV = "BOREALIS_SHARED_ANSIBLE_PREPARED_PREFLIGHT_RETRY_DELAY_SECONDS"
_SSH_BANNER_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_BANNER_TIMEOUT_SECONDS"
_SSH_SESSION_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS"
_SHARED_ANSIBLE_SSH_RETRIES_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_RETRIES"
_SHARED_ANSIBLE_SSH_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS"
_SHARED_ANSIBLE_SSH_TRANSFER_METHOD_ENV = "BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD"
_SHARED_ANSIBLE_SCP_EXTRA_ARGS_ENV = "BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS"
_DEFAULT_PREPARED_REMOTE_PREFLIGHT_ATTEMPTS = 5
_DEFAULT_PREPARED_REMOTE_PREFLIGHT_RETRY_DELAY_SECONDS = 1.0
_DEFAULT_SHARED_ANSIBLE_SSH_BANNER_TIMEOUT_SECONDS = 20.0
_DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS = 20.0
_DEFAULT_SHARED_ANSIBLE_SSH_RETRIES = 3
_DEFAULT_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS = 20
_DEFAULT_SHARED_ANSIBLE_SSH_TRANSFER_METHOD = "scp"
_DEFAULT_SHARED_ANSIBLE_SCP_EXTRA_ARGS = "-O"
_DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT = 20
_DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT = 50

RUN_STATUS_PENDING = "Pending"
RUN_STATUS_RUNNING = "Running"
RUN_STATUS_SUCCESS = "Success"
RUN_STATUS_WARNING = "Warning"
RUN_STATUS_FAILED = "Failed"
RUN_STATUS_EXPIRED = "Expired"
RUN_STATUS_TIMED_OUT = "Timed Out"
RUN_STATUS_SKIPPED = "Skipped"
SKIP_REASON_NO_TARGETS = "no_devices_targeted"
SKIP_REASON_NO_ELIGIBLE_TARGETS = "no_eligible_targets"
TERMINAL_RUN_STATUSES = {
    RUN_STATUS_SUCCESS,
    RUN_STATUS_WARNING,
    RUN_STATUS_FAILED,
    RUN_STATUS_EXPIRED,
    RUN_STATUS_TIMED_OUT,
    RUN_STATUS_SKIPPED,
}

RESOLUTION_STATUS_PENDING = "pending"
RESOLUTION_STATUS_ELIGIBLE = "eligible"
RESOLUTION_STATUS_SKIPPED = "skipped"
RESOLUTION_STATUS_UNRESOLVED = "unresolved"
RESOLUTION_REASON_REMOTE_PREFLIGHT_FAILED = "remote_preflight_failed"
RESOLUTION_REASON_WIREGUARD_NOT_READY = "wireguard_not_ready"
_SOFT_WIREGUARD_DISPATCH_REASONS = {
    "no_recent_handshake",
    "stale_handshake",
    "transport_probe_pending",
}
ENGINE_LOCAL_ALIAS = "borealis-engine-01"
CREDENTIAL_RESET_REQUIRED_MESSAGE = (
    "The credential associated with this scheduled job can no longer be decrypted due to the "
    "Aegis Cipher being reset, please update the credential with the data it is missing."
)
JOB_KIND_AUTOMATION = "automation"
JOB_KIND_ONBOARDING = "onboarding"
ONBOARDING_COMPONENT_KIND = "device_onboarding"
ONBOARDING_COMPONENT_NAME = "Device Onboarding"
ONBOARDING_STATUS_PENDING = "pending"
ONBOARDING_STATUS_RUNNING = "running"
ONBOARDING_STATUS_WAITING_APPROVAL = "waiting_approval"
ONBOARDING_STATUS_SKIPPED = "skipped"
ONBOARDING_STATUS_FAILED = "failed"
ONBOARDING_STATUS_UNREACHABLE = "unreachable"
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


def _now_ts() -> int:
    return int(time.time())


def _env_string(value: Any) -> str:
    if isinstance(value, bool):
        return "True" if value else "False"
    if value is None:
        return ""
    return str(value)


def _env_positive_int(name: str, default: int) -> int:
    raw_value = str(os.getenv(name, "") or "").strip()
    if not raw_value:
        return default
    try:
        return max(1, int(raw_value))
    except Exception:
        return default


def _env_non_negative_float(name: str, default: float) -> float:
    raw_value = str(os.getenv(name, "") or "").strip()
    if not raw_value:
        return default
    try:
        return max(0.0, float(raw_value))
    except Exception:
        return default


def _shared_ansible_ssh_transfer_method() -> str:
    raw_value = str(
        os.getenv(
            _SHARED_ANSIBLE_SSH_TRANSFER_METHOD_ENV,
            _DEFAULT_SHARED_ANSIBLE_SSH_TRANSFER_METHOD,
        )
        or ""
    ).strip().lower()
    if raw_value in {"smart", "sftp", "scp", "piped"}:
        return raw_value
    return _DEFAULT_SHARED_ANSIBLE_SSH_TRANSFER_METHOD


def _shared_ansible_scp_extra_args() -> str:
    raw_value = os.getenv(_SHARED_ANSIBLE_SCP_EXTRA_ARGS_ENV)
    if raw_value is None:
        return _DEFAULT_SHARED_ANSIBLE_SCP_EXTRA_ARGS
    return str(raw_value or "").strip()


def _normalize_ansible_transport(run_mode: Any) -> str:
    normalized = str(run_mode or "").strip().lower()
    if normalized in {"ssh", "ssh_individual"}:
        return "ssh"
    if normalized in {"winrm", "winrm_individual"}:
        return "winrm"
    if normalized == "local":
        return "local"
    return normalized


def _normalize_job_kind(value: Any) -> str:
    normalized = str(value or JOB_KIND_AUTOMATION).strip().lower()
    if normalized in {"device_onboarding", "automatic_onboarding", "ssh_onboarding", JOB_KIND_ONBOARDING}:
        return JOB_KIND_ONBOARDING
    return JOB_KIND_AUTOMATION


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


def _is_individual_ansible_context(run_mode: Any) -> bool:
    return str(run_mode or "").strip().lower() in {"ssh_individual", "winrm_individual"}


def _safe_inventory_slug(value: Any, *, fallback: str) -> str:
    raw = str(value or "").strip().lower()
    if not raw:
        return fallback
    cleaned = re.sub(r"[^a-z0-9]+", "_", raw)
    cleaned = re.sub(r"_+", "_", cleaned).strip("_")
    return cleaned or fallback


def _safe_inventory_host_label(value: Any, *, fallback: str) -> str:
    raw = str(value or "").strip()
    if not raw:
        return fallback
    cleaned = "".join(ch if ch.isalnum() or ch in {"-", "_", "."} else "-" for ch in raw)
    cleaned = cleaned.strip(".-")
    return cleaned or fallback


def _inventory_hostname_for_target(
    hostname: Any,
    *,
    site_name: Any,
    site_id: Any,
    connection: str,
) -> str:
    host_slug = _safe_inventory_slug(hostname, fallback="host")
    if str(connection or "").strip().lower() == "local":
        return _safe_inventory_host_label(hostname or ENGINE_LOCAL_ALIAS, fallback=ENGINE_LOCAL_ALIAS)
    site_slug = _safe_inventory_slug(site_name, fallback="")
    if site_slug:
        return f"{site_slug}__{host_slug}"
    try:
        if site_id is not None:
            return f"site_{int(site_id)}__{host_slug}"
    except Exception:
        pass
    return f"unassigned__{host_slug}"


def _site_group_name(site_name: Any, site_id: Any) -> str:
    site_slug = _safe_inventory_slug(site_name, fallback="")
    if site_slug:
        return f"site_{site_slug}"
    try:
        if site_id is not None:
            return f"site_{int(site_id)}"
    except Exception:
        pass
    return "site_unassigned"


def _extract_endpoint_port(value: Any) -> Optional[int]:
    text = str(value or "").strip()
    if not text:
        return None
    try:
        if text.isdigit():
            parsed = int(text)
            return parsed if 1 <= parsed <= 65535 else None
        if "://" in text:
            from urllib.parse import urlsplit

            parsed = urlsplit(text)
            if parsed.port:
                return int(parsed.port)
            return None
        if text.count(":") == 1:
            host_part, port_part = text.rsplit(":", 1)
            if host_part and port_part.isdigit():
                parsed = int(port_part)
                return parsed if 1 <= parsed <= 65535 else None
    except Exception:
        return None
    return None


def _decode_base64_text(value: Any) -> Optional[str]:
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    try:
        cleaned = re.sub(r"\s+", "", stripped)
    except Exception:
        cleaned = stripped
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode("utf-8")
    except Exception:
        return decoded.decode("utf-8", errors="replace")


def _normalize_ssh_private_key_text(value: Any) -> str:
    text = "" if value is None else str(value)
    if not text:
        return ""
    normalized = text.lstrip("\ufeff").replace("\r\n", "\n").replace("\r", "\n")
    if normalized and not normalized.endswith("\n"):
        normalized += "\n"
    return normalized


def _inject_winrm_credential(
    base_values: Optional[Dict[str, Any]],
    credential: Optional[Dict[str, Any]],
) -> Dict[str, Any]:
    values: Dict[str, Any] = dict(base_values or {})
    if not credential:
        return values

    username = str(credential.get("username") or "")
    password = str(credential.get("password") or "")
    metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
    transport = metadata.get("winrm_transport") if isinstance(metadata, dict) else None
    transport_str = str(transport or "ntlm").strip().lower() or "ntlm"

    values[_WINRM_USERNAME_VAR] = username
    values[_WINRM_PASSWORD_VAR] = password
    values[_WINRM_TRANSPORT_VAR] = transport_str
    return values


def _decode_script_content(value: Any, encoding_hint: str = "") -> str:
    encoding = (encoding_hint or "").strip().lower()
    if isinstance(value, str):
        if encoding in ("base64", "b64", "base-64"):
            decoded = _decode_base64_text(value)
            if decoded is not None:
                return decoded.replace("\r\n", "\n")
        decoded = _decode_base64_text(value)
        if decoded is not None:
            return decoded.replace("\r\n", "\n")
        return value.replace("\r\n", "\n")
    return ""


def _encode_script_content(script_text: Any) -> str:
    if not isinstance(script_text, str):
        if script_text is None:
            script_text = ""
        else:
            script_text = str(script_text)
    normalized = script_text.replace("\r\n", "\n")
    if not normalized:
        return ""
    encoded = base64.b64encode(normalized.encode("utf-8"))
    return encoded.decode("ascii")


def _canonical_env_key(name: Any) -> str:
    try:
        return re.sub(r"[^A-Za-z0-9_]", "_", str(name or "").strip()).upper()
    except Exception:
        return ""


def _expand_env_aliases(env_map: Dict[str, str], variables: List[Dict[str, Any]]) -> Dict[str, str]:
    expanded: Dict[str, str] = dict(env_map or {})
    if not isinstance(variables, list):
        return expanded
    for var in variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        canonical = _canonical_env_key(name)
        if not canonical or canonical not in expanded:
            continue
        value = expanded[canonical]
        alias = re.sub(r"[^A-Za-z0-9_]", "_", name)
        if alias and alias not in expanded:
            expanded[alias] = value
        if alias != name and re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", name) and name not in expanded:
            expanded[name] = value
    return expanded


def _powershell_literal(value: Any, var_type: str) -> str:
    typ = str(var_type or "string").lower()
    if typ == "boolean":
        if isinstance(value, bool):
            truthy = value
        elif value is None:
            truthy = False
        elif isinstance(value, (int, float)):
            truthy = value != 0
        else:
            s = str(value).strip().lower()
            if s in {"true", "1", "yes", "y", "on"}:
                truthy = True
            elif s in {"false", "0", "no", "n", "off", ""}:
                truthy = False
            else:
                truthy = bool(s)
        return "$true" if truthy else "$false"
    if typ == "number":
        if value is None or value == "":
            return "0"
        return str(value)
    s = "" if value is None else str(value)
    return "'" + s.replace("'", "''") + "'"


def _extract_variable_default(var: Dict[str, Any]) -> Any:
    for key in ("value", "default", "defaultValue", "default_value"):
        if key in var:
            val = var.get(key)
            return "" if val is None else val
    return ""


def _prepare_variable_context(doc_variables: List[Dict[str, Any]], overrides: Dict[str, Any]):
    env_map: Dict[str, str] = {}
    variables: List[Dict[str, Any]] = []
    literal_lookup: Dict[str, str] = {}
    doc_names: Dict[str, bool] = {}

    overrides = overrides or {}

    if not isinstance(doc_variables, list):
        doc_variables = []

    for var in doc_variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        doc_names[name] = True
        canonical = _canonical_env_key(name)
        var_type = str(var.get("type") or "string").lower()
        default_val = _extract_variable_default(var)
        final_val = overrides[name] if name in overrides else default_val
        if canonical:
            env_map[canonical] = _env_string(final_val)
            literal_lookup[canonical] = _powershell_literal(final_val, var_type)
        if name in overrides:
            new_var = dict(var)
            new_var["value"] = overrides[name]
            variables.append(new_var)
        else:
            variables.append(var)

    for name, val in overrides.items():
        if name in doc_names:
            continue
        canonical = _canonical_env_key(name)
        if canonical:
            env_map[canonical] = _env_string(val)
            literal_lookup[canonical] = _powershell_literal(val, "string")
        variables.append({"name": name, "value": val, "type": "string"})

    env_map = _expand_env_aliases(env_map, variables)
    return env_map, variables, literal_lookup


_ENV_VAR_PATTERN = re.compile(r"(?i)\$env:(\{)?([A-Za-z0-9_\-]+)(?(1)\})")


def _rewrite_powershell_script(content: str, literal_lookup: Dict[str, str]) -> str:
    if not content or not literal_lookup:
        return content

    def _replace(match: Any) -> str:
        name = match.group(2)
        canonical = _canonical_env_key(name)
        if not canonical:
            return match.group(0)
        literal = literal_lookup.get(canonical)
        if literal is None:
            return match.group(0)
        return literal

    return _ENV_VAR_PATTERN.sub(_replace, content)


_SUPPORTED_AGENT_SCRIPT_TYPES = {"powershell", "batch", "bash"}


def _normalize_agent_script_type(value: Any) -> str:
    normalized = str(value or "powershell").strip().lower()
    return normalized or "powershell"


def _rewrite_script_for_dispatch(content: str, script_type: str, literal_lookup: Dict[str, str]) -> str:
    normalized_content = (content or "").replace("\r\n", "\n")
    if script_type == "powershell":
        return _rewrite_powershell_script(normalized_content, literal_lookup)
    return normalized_content


def _parse_ts(val: Any) -> Optional[int]:
    """Best effort to parse ISO-ish datetime string or numeric seconds to epoch seconds."""
    if val is None:
        return None
    if isinstance(val, (int, float)):
        return int(val)
    try:
        from datetime import datetime
        s = str(val).strip().replace("Z", "+00:00")
        dt = datetime.fromisoformat(s)
        return int(dt.timestamp())
    except Exception:
        return None


def _parse_expiration(s: Optional[str]) -> Optional[int]:
    """Parse expiration shorthand to seconds.
    Examples: '30m' -> 1800, '1h' -> 3600, '2d' -> 172800.
    Returns None for 'no_expire' or invalid input.
    """
    if not s or s == "no_expire":
        return None
    try:
        s = s.strip().lower()
        unit = s[-1]
        value = int(s[:-1])
        if unit == 'm':
            return value * 60
        if unit == 'h':
            return value * 3600
        if unit == 'd':
            return value * 86400
        # Fallback: treat as minutes if only a number
        return int(s) * 60
    except Exception:
        return None


def _floor_minute(ts: int) -> int:
    ts = int(ts or 0)
    return ts - (ts % 60)


def _now_minute() -> int:
    return _floor_minute(_now_ts())


def _add_months(dt_tuple: Tuple[int, int, int, int, int, int], months: int = 1) -> int:
    """Advance a date by N months and return epoch seconds.
    dt_tuple = (year, month, day, hour, minute, second)
    Handles month-end clamping.
    """
    from calendar import monthrange
    from datetime import datetime, timezone

    y, m, d, hh, mm, ss = dt_tuple
    m2 = m + months
    y += (m2 - 1) // 12
    m2 = ((m2 - 1) % 12) + 1
    # Clamp day to last day of new month
    last_day = monthrange(y, m2)[1]
    d = min(d, last_day)
    try:
        return int(datetime(y, m2, d, hh, mm, ss, tzinfo=timezone.utc).timestamp())
    except Exception:
        # Fallback to first of month if something odd
        return int(datetime(y, m2, 1, hh, mm, ss, tzinfo=timezone.utc).timestamp())


def _add_years(dt_tuple: Tuple[int, int, int, int, int, int], years: int = 1) -> int:
    from datetime import datetime, timezone
    y, m, d, hh, mm, ss = dt_tuple
    y += years
    # Handle Feb 29 -> Feb 28 if needed
    try:
        return int(datetime(y, m, d, hh, mm, ss, tzinfo=timezone.utc).timestamp())
    except Exception:
        # clamp day to 28
        d2 = 28 if (m == 2 and d > 28) else 1
        return int(datetime(y, m, d2, hh, mm, ss, tzinfo=timezone.utc).timestamp())


def _to_dt_tuple(ts: int) -> Tuple[int, int, int, int, int, int]:
    from datetime import datetime, timezone
    dt = datetime.fromtimestamp(int(ts), tz=timezone.utc)
    return (dt.year, dt.month, dt.day, dt.hour, dt.minute, dt.second)


def _normalize_host_key(value: Any) -> str:
    return str(value or "").strip().lower()


def _status_bucket_for_run(status: Any) -> Optional[str]:
    normalized = str(status or "").strip()
    if normalized == RUN_STATUS_PENDING:
        return "pending"
    if normalized == RUN_STATUS_RUNNING:
        return "running"
    if normalized == RUN_STATUS_SUCCESS:
        return "success"
    if normalized == RUN_STATUS_WARNING:
        return "warning"
    if normalized == RUN_STATUS_FAILED:
        return "failed"
    if normalized == RUN_STATUS_EXPIRED:
        return "expired"
    if normalized == RUN_STATUS_TIMED_OUT:
        return "timed_out"
    if normalized == RUN_STATUS_SKIPPED:
        return "skipped"
    return None


def _normalize_target_service_mode(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized == "current_user":
        return "currentuser"
    if normalized in {"system", "currentuser"}:
        return normalized
    return ""


def _host_run_priority(run: Mapping[str, Any]) -> Tuple[int, int, int, int]:
    status = str(run.get("status") or "").strip()
    priority_map = {
        RUN_STATUS_RUNNING: 70,
        RUN_STATUS_FAILED: 60,
        RUN_STATUS_TIMED_OUT: 50,
        RUN_STATUS_WARNING: 45,
        RUN_STATUS_EXPIRED: 40,
        RUN_STATUS_SUCCESS: 30,
        RUN_STATUS_PENDING: 20,
        RUN_STATUS_SKIPPED: 10,
    }
    return (
        priority_map.get(status, 0),
        1 if run.get("finished_ts") is not None else 0,
        int(run.get("finished_ts") or run.get("started_ts") or run.get("scheduled_ts") or 0),
        int(run.get("id") or 0),
    )


def _aggregate_occurrence_runs(
    rows: Sequence[Mapping[str, Any]],
) -> Tuple[Dict[str, Dict[str, Any]], Dict[str, int], bool]:
    grouped: Dict[str, List[Dict[str, Any]]] = {}
    has_no_targets_skip = False

    for row in rows:
        run = dict(row or {})
        host_key = _normalize_host_key(run.get("target_hostname"))
        if not host_key:
            if (
                str(run.get("status") or "").strip() == RUN_STATUS_SKIPPED
                and str(run.get("skip_reason") or "").strip().lower() == SKIP_REASON_NO_TARGETS
            ):
                has_no_targets_skip = True
            continue
        grouped.setdefault(host_key, []).append(run)

    aggregated: Dict[str, Dict[str, Any]] = {}
    counts = {
        "pending": 0,
        "running": 0,
        "success": 0,
        "warning": 0,
        "failed": 0,
        "expired": 0,
        "timed_out": 0,
        "skipped": 0,
        "total_targets": len(grouped),
    }

    for host_key, host_runs in grouped.items():
        preferred = max(host_runs, key=_host_run_priority)
        aggregated[host_key] = preferred
        bucket = _status_bucket_for_run(preferred.get("status"))
        if bucket and bucket in counts:
            counts[bucket] += 1

    return aggregated, counts, has_no_targets_skip


def _aggregate_statuses(statuses: Sequence[Any]) -> str:
    normalized = {str(item or "").strip() for item in statuses if str(item or "").strip()}
    if not normalized:
        return RUN_STATUS_PENDING
    if RUN_STATUS_RUNNING in normalized:
        return RUN_STATUS_RUNNING
    if RUN_STATUS_FAILED in normalized:
        return RUN_STATUS_FAILED
    if RUN_STATUS_TIMED_OUT in normalized:
        return RUN_STATUS_TIMED_OUT
    if RUN_STATUS_WARNING in normalized:
        return RUN_STATUS_WARNING
    if RUN_STATUS_EXPIRED in normalized:
        return RUN_STATUS_EXPIRED
    if RUN_STATUS_PENDING in normalized:
        return RUN_STATUS_PENDING
    if RUN_STATUS_SUCCESS in normalized:
        return RUN_STATUS_SUCCESS
    if RUN_STATUS_SKIPPED in normalized:
        return RUN_STATUS_SKIPPED
    return next(iter(normalized))


def _resolve_activity_history_insert_id(
    cur,
    *,
    hostname: Any,
    script_path: Any,
    script_name: Any,
    script_type: Any,
    ran_at: Any,
) -> Optional[int]:
    lastrowid = getattr(cur, "lastrowid", None)
    try:
        if lastrowid not in (None, ""):
            return int(lastrowid)
    except Exception:
        pass
    try:
        cur.execute(
            """
            SELECT id
              FROM activity_history
             WHERE hostname=?
               AND script_path=?
               AND script_name=?
               AND script_type=?
               AND ran_at=?
             ORDER BY id DESC
             LIMIT 1
            """,
            (
                str(hostname or ""),
                str(script_path or ""),
                str(script_name or ""),
                str(script_type or ""),
                int(ran_at or 0),
            ),
        )
        row = cur.fetchone()
        if row and row[0] is not None:
            return int(row[0])
    except Exception:
        return None
    return None


def _all_skipped_for_reason(
    aggregated_rows: Mapping[str, Mapping[str, Any]],
    *,
    resolution_reason: str = "",
) -> bool:
    rows = list((aggregated_rows or {}).values())
    if not rows:
        return False
    normalized_reason = str(resolution_reason or "").strip().lower()
    for row in rows:
        if str(row.get("status") or "").strip() != RUN_STATUS_SKIPPED:
            return False
        if normalized_reason and str(row.get("resolution_reason") or "").strip().lower() != normalized_reason:
            return False
    return True


def _wireguard_session_allows_remote_attempt(session: Mapping[str, Any], wireguard_peer_ip: str) -> bool:
    if not str(wireguard_peer_ip or "").strip():
        return False
    if session.get("dispatch_ready") is not False:
        return True
    reason = str(session.get("dispatch_ready_reason") or "").strip().lower()
    if reason not in _SOFT_WIREGUARD_DISPATCH_REASONS:
        return False
    if session.get("agent_ready") is False:
        return False
    if session.get("peer_present") is False:
        return False
    return True


class JobScheduler:
    def __init__(
        self,
        app,
        socketio,
        db_conn_factory: Callable[[], sqlite3.Connection] | str,
        *,
        script_signer=None,
        service_logger: Optional[Callable[[str, str, Optional[str]], None]] = None,
        assembly_runtime: Optional[AssemblyRuntimeService] = None,
    ):
        self.app = app
        self.socketio = socketio
        if callable(db_conn_factory):
            self._db_conn_factory = db_conn_factory
        else:
            database_value = str(db_conn_factory or "")

            def _legacy_conn_factory() -> sqlite3.Connection:
                return sqlite3.connect(database_value)

            self._db_conn_factory = _legacy_conn_factory
        self._filter_matcher = DeviceFilterMatcher(db_conn_factory=self._db_conn_factory)
        self._site_access = UserSiteAccessManager(self._db_conn_factory, logger=self.app.logger)
        self._script_signer = script_signer
        self._running = False
        self._service_log = service_logger
        self._assembly_runtime = assembly_runtime
        # Retention for run history (days)
        self.RETENTION_DAYS = int(os.environ.get("BOREALIS_JOB_HISTORY_DAYS", "30"))
        # Callback to retrieve current set of online hostnames
        self._online_lookup: Optional[Callable[[], List[str]]] = None
        # Optional callback to emit directly to a host + service-mode Socket.IO route.
        self._emit_host_service_event: Optional[Callable[[str, str, str, Any], bool]] = None
        # Optional callback to execute Ansible directly from the server
        self._server_ansible_runner: Optional[Callable[..., str]] = None
        # Optional callback to fetch stored credentials (with decrypted secrets)
        self._credential_fetcher: Optional[Callable[[int], Optional[Dict[str, Any]]]] = None
        # Optional callback to resolve the Engine URL agents should enroll against.
        self._public_base_url_lookup: Optional[Callable[[], str]] = None
        # Optional callback to fetch active WireGuard sessions keyed by agent_id.
        self._vpn_session_lookup: Optional[Callable[[], Dict[str, Dict[str, Any]]]] = None
        # Optional callback to re-prime existing WireGuard sessions for selected agents.
        self._vpn_session_prepare: Optional[
            Callable[[Sequence[str], Optional[Sequence[int]]], Dict[str, Dict[str, Any]]]
        ] = None
        # Optional callback to launch workflow-backed scheduled jobs.
        self._workflow_run_launcher: Optional[Callable[..., Dict[str, Any]]] = None
        # Optional callback to validate a saved workflow document before jobs reference it.
        self._workflow_document_validator: Optional[Callable[..., List[str]]] = None
        self._onboarding_dispatch_lock = threading.Lock()
        self._onboarding_running_runs: set[int] = set()

        # Ensure run-history table exists
        self._init_tables()

        # Bind routes
        self._register_routes()
        self._log_event(
            "scheduler initialised",
            extra={
                "has_script_signer": bool(self._script_signer),
                "retention_days": self.RETENTION_DAYS,
            },
        )

    def _targets_include_filters(self, entries: Sequence[Any]) -> bool:
        if not isinstance(entries, (list, tuple)):
            return False
        for entry in entries:
            if isinstance(entry, dict):
                kind = str(entry.get("kind") or entry.get("type") or "").strip().lower()
                if kind == "filter" or entry.get("filter_id") is not None:
                    return True
        return False

    def _log_event(
        self,
        message: str,
        *,
        level: str = "INFO",
        job_id: Optional[int] = None,
        host: Optional[str] = None,
        run_id: Optional[int] = None,
        extra: Optional[Dict[str, Any]] = None,
    ) -> None:
        fragments: List[str] = []
        if job_id is not None:
            fragments.append(f"job={job_id}")
        if run_id is not None:
            fragments.append(f"run={run_id}")
        if host:
            fragments.append(f"host={host}")
        if extra:
            for key, value in extra.items():
                fragments.append(f"{key}={value}")
        payload = message
        if fragments:
            payload = f"{message} | " + " ".join(str(fragment) for fragment in fragments)
        scope = f"job-{job_id}" if job_id is not None else None
        try:
            if callable(self._service_log):
                self._service_log("scheduled_jobs", payload, scope=scope, level=level)
                return
        except Exception:
            pass

    def _current_user(self) -> Optional[Dict[str, str]]:
        from flask import request, session

        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return {"username": username, "role": role}

        token = None
        auth_header = request.headers.get("Authorization") or ""
        if auth_header.lower().startswith("bearer "):
            token = auth_header.split(" ", 1)[1].strip()
        if not token:
            token = request.cookies.get("borealis_auth")
        if not token:
            return None

        try:
            serializer = URLSafeTimedSerializer(require_app_secret(self.app), salt="borealis-auth")
            token_ttl = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
            data = serializer.loads(token, max_age=token_ttl)
            username = data.get("u")
            role = data.get("r") or "User"
            if username:
                return {"username": username, "role": role}
        except (BadSignature, SignatureExpired, Exception):
            return None
        return None

    def _require_user(self) -> Optional[Tuple[Dict[str, Any], int]]:
        if not self._current_user():
            return {"error": "unauthorized"}, 401
        return None

    def _job_visible_to_user(self, user: Optional[Mapping[str, Any]], raw_targets: Any) -> bool:
        if isinstance(raw_targets, str):
            try:
                targets = json.loads(raw_targets or "[]")
            except Exception:
                targets = []
        elif isinstance(raw_targets, list):
            targets = raw_targets
        else:
            targets = []
        return self._site_access.job_targets_fit_scope(user, targets)
        try:
            numeric_level = getattr(logging, level.upper(), logging.INFO)
            self.app.logger.log(numeric_level, "[Scheduler] %s", payload)
        except Exception:
            pass

    # ---------- Helpers for dispatching scripts ----------
    def _is_valid_scripts_relpath(self, rel_path: str) -> bool:
        try:
            p = (rel_path or "").replace("\\", "/").lstrip("/")
            if not p:
                return False
            top = p.split("/", 1)[0]
            return top in ("Scripts",)
        except Exception:
            return False

    def _resolve_runtime_document(
        self,
        rel_path: str,
        default_type: str,
        assembly_guid: Optional[str] = None,
    ) -> Tuple[Optional[Dict[str, Any]], Optional[Dict[str, Any]]]:
        runtime = self._assembly_runtime
        if runtime is None:
            return None, None
        record = None
        try:
            guid_lookup = str(assembly_guid or "").strip().lower()
            if guid_lookup:
                record = runtime.resolve_document_by_guid(guid_lookup)
            if record is None and rel_path:
                record = runtime.resolve_document_by_source_path(rel_path)
        except Exception as exc:
            self._log_event(
                "assembly cache lookup failed",
                level="ERROR",
                extra={"error": str(exc), "path": rel_path, "assembly_guid": assembly_guid or ""},
            )
            return None, None
        if not record:
            self._log_event(
                "assembly not found in cache",
                level="ERROR",
                extra={"path": rel_path, "assembly_guid": assembly_guid or ""},
            )
            return None, None
        payload_doc = record.get("payload_json")
        if not isinstance(payload_doc, dict):
            raw_payload = record.get("payload")
            if isinstance(raw_payload, str):
                try:
                    payload_doc = json.loads(raw_payload)
                except Exception:
                    payload_doc = None
        if not isinstance(payload_doc, dict):
            self._log_event(
                "assembly payload missing",
                level="ERROR",
                extra={"path": rel_path},
            )
            return None, None
        source_identifier = (
            rel_path
            or str(record.get("virtual_path") or "").strip()
            or str(record.get("assembly_guid") or "").strip()
            or "Assembly"
        )
        doc = self._load_assembly_document(source_identifier, default_type, payload=payload_doc)
        if doc:
            doc["assembly_guid"] = record.get("assembly_guid")
            if not doc.get("name"):
                doc["name"] = record.get("display_name") or doc.get("name")
        return doc, record

    def _detect_script_type(self, filename: str) -> str:
        fn_lower = (filename or "").lower()
        if fn_lower.endswith(".json") and os.path.isfile(filename):
            try:
                with open(filename, "r", encoding="utf-8") as fh:
                    data = json.load(fh)
                if isinstance(data, dict):
                    typ = str(data.get("type") or data.get("script_type") or "").strip().lower()
                    if typ in ("powershell", "batch", "bash", "ansible"):
                        return typ
            except Exception:
                pass
            return "powershell"
        if fn_lower.endswith(".yml"):
            return "ansible"
        if fn_lower.endswith(".ps1"):
            return "powershell"
        if fn_lower.endswith(".bat"):
            return "batch"
        if fn_lower.endswith(".sh"):
            return "bash"
        return "unknown"

    def _load_assembly_document(
        self,
        source_identifier: str,
        default_type: str,
        payload: Optional[Dict[str, Any]] = None,
    ) -> Dict[str, Any]:
        base_name = os.path.splitext(os.path.basename(source_identifier))[0]
        doc: Dict[str, Any] = {
            "name": base_name,
            "description": "",
            "category": "application" if default_type == "ansible" else "script",
            "type": default_type,
            "script": "",
            "variables": [],
            "files": [],
            "timeout_seconds": 3600,
        }
        data: Dict[str, Any] = {}
        if isinstance(payload, dict):
            data = payload
        elif source_identifier.lower().endswith(".json") and os.path.isfile(source_identifier):
            try:
                with open(source_identifier, "r", encoding="utf-8") as fh:
                    data = json.load(fh)
            except Exception:
                data = {}
        if isinstance(data, dict) and data:
            doc["name"] = str(data.get("name") or doc["name"])
            doc["description"] = str(data.get("description") or "")
            cat = str(data.get("category") or doc["category"]).strip().lower()
            if cat in ("application", "script"):
                doc["category"] = cat
            typ = str(data.get("type") or data.get("script_type") or default_type).strip().lower()
            if typ in ("powershell", "batch", "bash", "ansible"):
                doc["type"] = typ
            script_val = data.get("script")
            content_val = data.get("content")
            script_lines = data.get("script_lines")
            if isinstance(script_lines, list):
                try:
                    doc["script"] = "\n".join(str(line) for line in script_lines)
                except Exception:
                    doc["script"] = ""
            elif isinstance(script_val, str):
                doc["script"] = script_val
            elif isinstance(content_val, str):
                doc["script"] = content_val
            encoding_hint = str(data.get("script_encoding") or data.get("scriptEncoding") or "").strip().lower()
            doc["script"] = _decode_script_content(doc.get("script"), encoding_hint)
            if encoding_hint in ("base64", "b64", "base-64"):
                doc["script_encoding"] = "base64"
            else:
                probe_source = ""
                if isinstance(script_val, str) and script_val:
                    probe_source = script_val
                elif isinstance(content_val, str) and content_val:
                    probe_source = content_val
                decoded_probe = _decode_base64_text(probe_source) if probe_source else None
                if decoded_probe is not None:
                    doc["script_encoding"] = "base64"
                    doc["script"] = decoded_probe.replace("\r\n", "\n")
                else:
                    doc["script_encoding"] = "plain"
            try:
                timeout_raw = data.get("timeout_seconds", data.get("timeout"))
                if timeout_raw is None:
                    doc["timeout_seconds"] = 3600
                else:
                    doc["timeout_seconds"] = max(0, int(timeout_raw))
            except Exception:
                doc["timeout_seconds"] = 3600
            vars_in = data.get("variables") if isinstance(data.get("variables"), list) else []
            doc["variables"] = []
            for v in vars_in:
                if not isinstance(v, dict):
                    continue
                name = str(v.get("name") or v.get("key") or "").strip()
                if not name:
                    continue
                vtype = str(v.get("type") or "string").strip().lower()
                if vtype not in ("string", "number", "boolean", "credential"):
                    vtype = "string"
                doc["variables"].append({
                    "name": name,
                    "label": str(v.get("label") or ""),
                    "type": vtype,
                    "default": v.get("default", v.get("default_value")),
                    "required": bool(v.get("required")),
                    "description": str(v.get("description") or ""),
                })
            files_in = data.get("files") if isinstance(data.get("files"), list) else []
            doc["files"] = []
            for f in files_in:
                if not isinstance(f, dict):
                    continue
                fname = f.get("file_name") or f.get("name")
                if not fname or not isinstance(f.get("data"), str):
                    continue
                try:
                    size_val = int(f.get("size") or 0)
                except Exception:
                    size_val = 0
                doc["files"].append({
                    "file_name": str(fname),
                    "size": size_val,
                    "mime_type": str(f.get("mime_type") or f.get("mimeType") or ""),
                    "data": f.get("data"),
                })
            return doc
        if os.path.isfile(source_identifier):
            try:
                with open(source_identifier, "r", encoding="utf-8", errors="replace") as fh:
                    content = fh.read()
            except Exception:
                content = ""
            doc["script"] = (content or "").replace("\r\n", "\n")
        else:
            doc["script"] = ""
        return doc

    def _dispatch_ansible(
        self,
        hostname: str,
        component: Dict[str, Any],
        scheduled_job_id: int,
        scheduled_run_row_id: int,
        run_mode: str,
        credential_id: Optional[int] = None,
        use_service_account: bool = False,
    ) -> Optional[Dict[str, Any]]:
        try:
            rel_path = ""
            overrides_map: Dict[str, Any] = {}
            assembly_guid_hint = ""
            if isinstance(component, dict):
                assembly_guid_hint = str(component.get("assembly_guid") or component.get("assemblyGuid") or "").strip().lower()
                rel_path = component.get("path") or component.get("playbook_path") or component.get("script_path") or ""
                raw_overrides = component.get("variable_values")
                if isinstance(raw_overrides, dict):
                    for key, val in raw_overrides.items():
                        name = str(key or "").strip()
                        if not name:
                            continue
                        overrides_map[name] = val
                comp_vars = component.get("variables")
                if isinstance(comp_vars, list):
                    for var in comp_vars:
                        if not isinstance(var, dict):
                            continue
                        name = str(var.get("name") or "").strip()
                        if not name or name in overrides_map:
                            continue
                        if "value" in var:
                            overrides_map[name] = var.get("value")
            else:
                rel_path = str(component or "")
            rel_norm = (rel_path or "").replace("\\", "/").strip()
            rel_norm = rel_norm.lstrip("/")
            if rel_norm and not rel_norm.lower().startswith("ansible_playbooks/"):
                rel_norm = f"Ansible_Playbooks/{rel_norm}"
            doc, record = self._resolve_runtime_document(rel_norm, "ansible", assembly_guid=assembly_guid_hint)
            if not doc:
                return None
            resolved_virtual_path = str(record.get("virtual_path") or "").strip() if isinstance(record, dict) else ""
            if not rel_norm:
                rel_norm = resolved_virtual_path
            assembly_source = "runtime"
            assembly_guid = str(record.get("assembly_guid") or "").strip().lower() if isinstance(record, dict) else ""
            friendly_name = (doc.get("name") or "").strip()
            if not friendly_name:
                friendly_name = os.path.basename(rel_norm) if rel_norm else f"Job-{scheduled_job_id}"
            if not friendly_name:
                friendly_name = f"Job-{scheduled_job_id}"
            normalized_script = (doc.get("script") or "").replace("\r\n", "\n")
            files = doc.get("files") or []
            run_mode_norm = (run_mode or "system").strip().lower()
            transport_mode = _normalize_ansible_transport(run_mode_norm)
            if transport_mode not in {"local", "ssh", "winrm"}:
                raise RuntimeError(
                    f"Unsupported Ansible execution context '{run_mode_norm}'. "
                    "Use execution_context='local', 'ssh', 'ssh_individual', 'winrm', or 'winrm_individual'."
                )

            run_targets = self._load_run_targets(int(scheduled_run_row_id))
            target_row_ids = [int(item.get("id") or 0) for item in run_targets if int(item.get("id") or 0)]
            primary_target = None
            normalized_hostname = str(hostname or "").strip().lower()
            for target in run_targets:
                target_host = str(target.get("hostname") or "").strip().lower()
                if normalized_hostname and target_host == normalized_hostname:
                    primary_target = dict(target)
                    break
            if primary_target is None and run_targets:
                primary_target = dict(run_targets[0])

            def _update_target_rows(
                *,
                inventory_hostname: str = "",
                wireguard_peer_ip: str = "",
                resolved_connection: str = "",
                resolution_status: str = "",
                resolution_reason: str = "",
            ) -> None:
                if not target_row_ids:
                    return
                conn_update = self._conn()
                try:
                    cur_update = conn_update.cursor()
                    cur_update.executemany(
                        """
                        UPDATE scheduled_job_run_targets
                           SET inventory_hostname=?,
                               wireguard_peer_ip=?,
                               resolved_connection=?,
                               resolution_status=?,
                               resolution_reason=?
                         WHERE id=?
                        """,
                        [
                            (
                                inventory_hostname,
                                wireguard_peer_ip,
                                resolved_connection,
                                resolution_status,
                                resolution_reason,
                                row_id,
                            )
                            for row_id in target_row_ids
                        ],
                    )
                    conn_update.commit()
                finally:
                    conn_update.close()

            def _finalize_dispatch_failure(
                *,
                run_status: str,
                error: str,
                skip_reason: str = "",
            ) -> None:
                ts_now = _now_ts()
                conn_fail = self._conn()
                try:
                    cur_fail = conn_fail.cursor()
                    cur_fail.execute(
                        """
                        UPDATE scheduled_job_runs
                           SET status=?,
                               finished_ts=?,
                               updated_at=?,
                               skip_reason=?,
                               error=?
                         WHERE id=?
                        """,
                        (
                            run_status,
                            ts_now,
                            ts_now,
                            skip_reason,
                            str(error or "")[:512],
                            int(scheduled_run_row_id),
                        ),
                    )
                    conn_fail.commit()
                finally:
                    conn_fail.close()

            credential = None
            normalized_private_key = ""
            remote_requires_cred = transport_mode == "ssh" or (transport_mode == "winrm" and not use_service_account)
            if remote_requires_cred and not credential_id:
                _update_target_rows(
                    resolved_connection=transport_mode,
                    resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                    resolution_reason="credential_missing",
                )
                _finalize_dispatch_failure(
                    run_status=RUN_STATUS_FAILED,
                    error="Credential required for remote execution",
                )
                return None

            if transport_mode in {"ssh", "winrm"}:
                try:
                    credential = self._load_credential(credential_id) if credential_id is not None else None
                except AegisLockedError:
                    _update_target_rows(
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                        resolution_reason="credential_locked",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error="Aegis Cipher has not been entered; credential-backed execution is disabled.",
                    )
                    return None
                except AegisSecretResetRequiredError:
                    _update_target_rows(
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                        resolution_reason="credential_reset_required",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error=CREDENTIAL_RESET_REQUIRED_MESSAGE,
                    )
                    return None
                except AegisDataCorruptionError as exc:
                    _update_target_rows(
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                        resolution_reason="credential_corrupt",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error=str(exc) or "Credential data is corrupted.",
                    )
                    return None
                if not credential:
                    _update_target_rows(
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                        resolution_reason="credential_unavailable",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error="Selected credential is unavailable.",
                    )
                    return None
                if str(credential.get("connection_type") or "").strip().lower() not in {"", transport_mode}:
                    _update_target_rows(
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                        resolution_reason="credential_connection_mismatch",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error="Selected credential does not match the execution context.",
                    )
                    return None

            private_key_path = ""
            runtime_files: List[Dict[str, Any]] = []
            if transport_mode == "ssh" and credential:
                normalized_private_key = _normalize_ssh_private_key_text(credential.get("private_key") or "")
                private_key_passphrase = str(credential.get("private_key_passphrase") or "").strip()
                if normalized_private_key and private_key_passphrase:
                    credential_password = str(credential.get("password") or "").strip()
                    if credential_password:
                        self._log_event(
                            "ssh credential includes passphrase-protected private key; falling back to password auth",
                            job_id=int(scheduled_job_id),
                            host=str(hostname),
                            run_id=int(scheduled_run_row_id),
                            level="WARNING",
                            extra={
                                "credential_id": int(credential_id or 0),
                                "run_mode": run_mode_norm,
                            },
                        )
                        normalized_private_key = ""
                    else:
                        _update_target_rows(
                            resolved_connection=transport_mode,
                            resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                            resolution_reason="credential_private_key_passphrase_unsupported",
                        )
                        _finalize_dispatch_failure(
                            run_status=RUN_STATUS_FAILED,
                            error=(
                                "Passphrase-protected SSH private keys are not yet supported for Engine Ansible runs. "
                                "Use an unencrypted test key or add a password to the selected credential."
                            ),
                        )
                        return None
                if normalized_private_key:
                    private_key_path = "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
                    runtime_files.append(
                        {
                            "relative_path": "auth/id_borealis_ssh",
                            "content": normalized_private_key,
                            "mode": 0o600,
                        }
                    )

            target_specifications: List[Dict[str, Any]] = []
            if transport_mode == "local":
                target_specifications = []
                _update_target_rows(
                    inventory_hostname=_safe_inventory_host_label(hostname or ENGINE_LOCAL_ALIAS, fallback=ENGINE_LOCAL_ALIAS),
                    resolved_connection="local",
                    resolution_status=RESOLUTION_STATUS_ELIGIBLE,
                    resolution_reason="",
                )
            else:
                if not primary_target:
                    _update_target_rows(
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_UNRESOLVED,
                        resolution_reason="target_snapshot_missing",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error="No target snapshot exists for this Ansible run.",
                    )
                    return None

                try:
                    device_lookup = self._lookup_devices_for_targets(run_targets or [primary_target])
                except Exception as exc:
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_FAILED,
                        error=f"Unable to load device state: {exc}",
                    )
                    return None

                device = device_lookup.get(self._device_lookup_key_for_target(primary_target)) or {}
                site_id = primary_target.get("site_id")
                try:
                    site_id = int(site_id) if site_id is not None else None
                except Exception:
                    site_id = None
                site_name = str(device.get("site_name") or primary_target.get("site_name") or "").strip()
                agent_id = str(device.get("agent_id") or primary_target.get("agent_id") or "").strip()
                connection_endpoint = str(
                    device.get("connection_endpoint") or primary_target.get("connection_endpoint") or ""
                ).strip()
                host_alias = str(primary_target.get("inventory_hostname") or "").strip() or _inventory_hostname_for_target(
                    hostname,
                    site_name=site_name,
                    site_id=site_id,
                    connection=transport_mode,
                )
                default_port = 22 if transport_mode == "ssh" else 5985
                requested_port = _extract_endpoint_port(connection_endpoint) or default_port
                vpn_sessions = self._prepare_vpn_sessions(
                    [agent_id] if agent_id else [],
                    required_ports=[requested_port],
                )
                session = vpn_sessions.get(agent_id) or {}
                wireguard_peer_ip = str(session.get("virtual_ip") or "").split("/", 1)[0]
                if session and not _wireguard_session_allows_remote_attempt(session, wireguard_peer_ip):
                    ready_reason = str(session.get("dispatch_ready_reason") or "not_ready")
                    _update_target_rows(
                        inventory_hostname=host_alias,
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_SKIPPED,
                        resolution_reason=RESOLUTION_REASON_WIREGUARD_NOT_READY,
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_SKIPPED,
                        skip_reason=SKIP_REASON_NO_ELIGIBLE_TARGETS,
                        error=f"Managed WireGuard session is not ready for this target ({ready_reason}).",
                    )
                    return None
                if not wireguard_peer_ip:
                    _update_target_rows(
                        inventory_hostname=host_alias,
                        resolved_connection=transport_mode,
                        resolution_status=RESOLUTION_STATUS_SKIPPED,
                        resolution_reason="wireguard_unavailable",
                    )
                    _finalize_dispatch_failure(
                        run_status=RUN_STATUS_SKIPPED,
                        skip_reason=SKIP_REASON_NO_ELIGIBLE_TARGETS,
                        error="Managed WireGuard session is unavailable for this target.",
                    )
                    return None

                host_vars: Dict[str, Any] = {
                    "ansible_host": wireguard_peer_ip,
                    "ansible_connection": transport_mode,
                }
                if transport_mode == "ssh":
                    transfer_method = _shared_ansible_ssh_transfer_method()
                    host_vars["ansible_ssh_retries"] = _env_positive_int(
                        _SHARED_ANSIBLE_SSH_RETRIES_ENV,
                        _DEFAULT_SHARED_ANSIBLE_SSH_RETRIES,
                    )
                    host_vars["ansible_ssh_timeout"] = _env_positive_int(
                        _SHARED_ANSIBLE_SSH_TIMEOUT_ENV,
                        _DEFAULT_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS,
                    )
                    host_vars["ansible_ssh_transfer_method"] = transfer_method
                    if transfer_method in {"scp", "smart"}:
                        scp_extra_args = _shared_ansible_scp_extra_args()
                        if scp_extra_args:
                            host_vars["ansible_scp_extra_args"] = scp_extra_args
                    if requested_port != 22:
                        host_vars["ansible_port"] = requested_port
                    ssh_auth_mode = "combined"
                    if credential and private_key_path:
                        username = str(credential.get("username") or "").strip()
                        password = str(credential.get("password") or "").strip()
                        if password:
                            ssh_auth_mode = self._resolve_mixed_ssh_auth_mode(
                                host=wireguard_peer_ip,
                                port=requested_port,
                                username=username,
                                password=password,
                                private_key_text=normalized_private_key,
                            )
                    apply_ssh_credential_host_vars(
                        host_vars,
                        credential,
                        private_key_path=private_key_path,
                        include_password=ssh_auth_mode != "key",
                        include_private_key=ssh_auth_mode != "password",
                    )
                else:
                    username = ""
                    password = ""
                    transport = "ntlm"
                    if use_service_account:
                        service_account = self._load_service_account(agent_id)
                        username = str((service_account or {}).get("username") or "").strip()
                        password = str((service_account or {}).get("password") or "").strip()
                        if not username or not password:
                            _update_target_rows(
                                inventory_hostname=host_alias,
                                resolved_connection=transport_mode,
                                resolution_status=RESOLUTION_STATUS_SKIPPED,
                                resolution_reason="service_account_unavailable",
                            )
                            _finalize_dispatch_failure(
                                run_status=RUN_STATUS_SKIPPED,
                                skip_reason=SKIP_REASON_NO_ELIGIBLE_TARGETS,
                                error="No service account is available for this WinRM target.",
                            )
                            return None
                    elif credential:
                        username = str(credential.get("username") or "").strip()
                        password = str(credential.get("password") or "").strip()
                        metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
                        transport = str(metadata.get("winrm_transport") or "ntlm").strip().lower() or "ntlm"
                        if not username or not password:
                            _update_target_rows(
                                inventory_hostname=host_alias,
                                resolved_connection=transport_mode,
                                resolution_status=RESOLUTION_STATUS_SKIPPED,
                                resolution_reason="credential_incomplete",
                            )
                            _finalize_dispatch_failure(
                                run_status=RUN_STATUS_SKIPPED,
                                skip_reason=SKIP_REASON_NO_ELIGIBLE_TARGETS,
                                error="Selected WinRM credential is incomplete.",
                            )
                            return None
                    preflight_kwargs: Dict[str, Any] = {}
                    if bool(session.get("_requested_start")):
                        preflight_kwargs["attempts"] = _env_positive_int(
                            _PREPARED_REMOTE_PREFLIGHT_ATTEMPTS_ENV,
                            _DEFAULT_PREPARED_REMOTE_PREFLIGHT_ATTEMPTS,
                        )
                        preflight_kwargs["retry_delay_seconds"] = _env_non_negative_float(
                            _PREPARED_REMOTE_PREFLIGHT_RETRY_DELAY_ENV,
                            _DEFAULT_PREPARED_REMOTE_PREFLIGHT_RETRY_DELAY_SECONDS,
                        )
                    preflight_error = self._preflight_remote_port(
                        host=wireguard_peer_ip,
                        port=requested_port,
                        **preflight_kwargs,
                    )
                    if preflight_error:
                        _update_target_rows(
                            inventory_hostname=host_alias,
                            wireguard_peer_ip=wireguard_peer_ip,
                            resolved_connection=transport_mode,
                            resolution_status=RESOLUTION_STATUS_SKIPPED,
                            resolution_reason=RESOLUTION_REASON_REMOTE_PREFLIGHT_FAILED,
                        )
                        _finalize_dispatch_failure(
                            run_status=RUN_STATUS_SKIPPED,
                            skip_reason=SKIP_REASON_NO_ELIGIBLE_TARGETS,
                            error="No eligible devices were available for this Ansible run.",
                        )
                        return None
                    host_vars.update(
                        {
                            "ansible_user": username,
                            "ansible_password": password,
                            "ansible_winrm_transport": transport,
                            "ansible_winrm_server_cert_validation": "ignore",
                        }
                    )
                    if requested_port:
                        host_vars["ansible_port"] = requested_port
                    if credential and not use_service_account:
                        overrides_map = _inject_winrm_credential(overrides_map, credential)

                _update_target_rows(
                    inventory_hostname=host_alias,
                    wireguard_peer_ip=wireguard_peer_ip,
                    resolved_connection=transport_mode,
                    resolution_status=RESOLUTION_STATUS_ELIGIBLE,
                    resolution_reason="",
                )
                target_specifications = [
                    {
                        "hostname": str(hostname),
                        "inventory_hostname": host_alias,
                        "site_group": _site_group_name(site_name, site_id),
                        "host_vars": host_vars,
                    }
                ]

            # Record in activity_history for UI parity
            now = _now_ts()
            act_id = None
            conn = self._conn()
            cur = conn.cursor()
            try:
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        str(hostname),
                        rel_norm,
                        friendly_name,
                        "ansible",
                        now,
                        "Running",
                        "",
                        "",
                    ),
                )
                act_id = _resolve_activity_history_insert_id(
                    cur,
                    hostname=str(hostname),
                    script_path=rel_norm,
                    script_name=friendly_name,
                    script_type="ansible",
                    ran_at=now,
                )
                conn.commit()
            finally:
                conn.close()

            if not callable(self._server_ansible_runner):
                raise RuntimeError("Server-side Ansible runner is not configured")
            try:
                self._server_ansible_runner(
                    hostname=str(hostname),
                    playbook_rel_path=rel_norm,
                    playbook_name=friendly_name,
                    playbook_abs_path="",
                    playbook_content=normalized_script,
                    credential_id=credential_id,
                    variable_values=overrides_map,
                    payload_files=files,
                    target_specifications=target_specifications,
                    runtime_files=runtime_files,
                    source="scheduled_job",
                    activity_id=act_id,
                    scheduled_job_id=scheduled_job_id,
                    scheduled_run_id=scheduled_run_row_id,
                    scheduled_job_run_row_id=scheduled_run_row_id,
                    connection=transport_mode,
                )
                self._log_event(
                    "queued server ansible execution",
                    job_id=int(scheduled_job_id),
                    host=str(hostname),
                    run_id=scheduled_run_row_id,
                    extra={
                        "run_mode": run_mode_norm,
                        "transport_mode": transport_mode,
                        "assembly_source": assembly_source,
                        "assembly_guid": assembly_guid or "",
                    },
                )
            except Exception as exc:
                try:
                    self.app.logger.warning(
                        "[Scheduler] Server-side Ansible queue failed job=%s run=%s host=%s err=%s",
                        scheduled_job_id,
                        scheduled_run_row_id,
                        hostname,
                        exc,
                    )
                except Exception:
                    print(f"[Scheduler] Server-side Ansible queue failed job={scheduled_job_id} host={hostname} err={exc}")
                if act_id:
                    try:
                        conn_fail = self._conn()
                        cur_fail = conn_fail.cursor()
                        cur_fail.execute(
                            "UPDATE activity_history SET status='Failed', stderr=?, ran_at=? WHERE id=?",
                            (str(exc), _now_ts(), act_id),
                        )
                        conn_fail.commit()
                        conn_fail.close()
                    except Exception:
                        pass
                raise
            if act_id:
                return {
                    "activity_id": int(act_id),
                    "component_name": friendly_name,
                    "component_path": rel_norm,
                    "script_type": "ansible",
                    "component_kind": "ansible",
                }
            return None
        except Exception:
            pass

    def _dispatch_script(
        self,
        job_id: int,
        run_row_id: int,
        scheduled_ts: int,
        hostname: str,
        component: Dict[str, Any],
        run_mode: str,
    ) -> Optional[Dict[str, Any]]:
        """Emit a quick_job_run event to agents for the given script/host.
        Mirrors /api/scripts/quick_run behavior for scheduled jobs.
        """
        try:
            rel_path_raw = ""
            assembly_guid_hint = ""
            if isinstance(component, dict):
                assembly_guid_hint = str(component.get("assembly_guid") or component.get("assemblyGuid") or "").strip().lower()
                rel_path_raw = str(component.get("path") or component.get("script_path") or "")
            else:
                rel_path_raw = str(component or "")
            path_norm = (rel_path_raw or "").replace("\\", "/").strip()
            if path_norm and not path_norm.startswith("Scripts/"):
                path_norm = f"Scripts/{path_norm}"
            if path_norm and not self._is_valid_scripts_relpath(path_norm):
                self._log_event(
                    "script component path rejected",
                    job_id=job_id,
                    host=str(hostname),
                    run_id=run_row_id,
                    level="ERROR",
                    extra={"script_path": path_norm},
                )
                return None
            if not path_norm and not assembly_guid_hint:
                self._log_event(
                    "script component missing path and assembly_guid",
                    job_id=job_id,
                    host=str(hostname),
                    run_id=run_row_id,
                    level="ERROR",
                )
                return None
            doc, record = self._resolve_runtime_document(path_norm, "powershell", assembly_guid=assembly_guid_hint)
            if not doc:
                return None
            if not path_norm and isinstance(record, dict):
                path_norm = str(record.get("virtual_path") or "").strip()
            assembly_source = "runtime"
            stype = _normalize_agent_script_type(doc.get("type"))
            if stype not in _SUPPORTED_AGENT_SCRIPT_TYPES:
                return None
            content = doc.get("script") or ""
            doc_variables = doc.get("variables") if isinstance(doc.get("variables"), list) else []

            overrides: Dict[str, Any] = {}
            if isinstance(component, dict):
                if isinstance(component.get("variable_values"), dict):
                    for key, val in component.get("variable_values").items():
                        name = str(key or "").strip()
                        if name:
                            overrides[name] = val
                if isinstance(component.get("variables"), list):
                    for var in component.get("variables"):
                        if not isinstance(var, dict):
                            continue
                        name = str(var.get("name") or "").strip()
                        if not name:
                            continue
                        if "value" in var:
                            overrides[name] = var.get("value")

            env_map, variables, literal_lookup = _prepare_variable_context(doc_variables, overrides)
            content = _rewrite_script_for_dispatch(content, stype, literal_lookup)
            encoded_content = _encode_script_content(content)
            if self._script_signer is None:
                self._log_event(
                    "script signer unavailable; cannot dispatch payload",
                    job_id=job_id,
                    host=str(hostname),
                    run_id=run_row_id,
                    level="ERROR",
                    extra={"script_path": path_norm},
                )
                return None
            script_bytes = content.encode("utf-8")
            signature_b64: Optional[str] = None
            sig_alg: Optional[str] = None
            signing_key_b64: Optional[str] = None
            if self._script_signer is not None:
                try:
                    signature = self._script_signer.sign(script_bytes)
                    signature_b64 = base64.b64encode(signature).decode("ascii")
                    sig_alg = "ed25519"
                    signing_key_b64 = self._script_signer.public_base64_spki()
                except Exception:
                    signature_b64 = None
                    sig_alg = None
                    signing_key_b64 = None
            timeout_seconds = 0
            try:
                timeout_seconds = max(0, int(doc.get("timeout_seconds") or 0))
            except Exception:
                timeout_seconds = 0

            friendly_name = (doc.get("name") or "").strip()
            if not friendly_name:
                friendly_name = os.path.basename(path_norm) if path_norm else f"Job-{job_id}"
            if not friendly_name:
                friendly_name = f"Job-{job_id}"
            normalized_run_mode = (run_mode or "system").strip().lower()
            target_service_mode = _normalize_target_service_mode(normalized_run_mode)
            # Insert into activity_history for device for parity with Quick Job
            now = _now_ts()
            act_id = None
            conn = self._conn()
            try:
                act_id = insert_activity_history_row(
                    conn,
                    hostname=str(hostname),
                    script_path=path_norm,
                    script_name=friendly_name,
                    script_type=stype,
                    ran_at=now,
                    status="Queued",
                    stdout="",
                    stderr="",
                    queue_lane="scheduled_job_system",
                    activity_kind="scheduled_job",
                    metadata={
                        "assembly_source": assembly_source,
                        "scheduled_job_id": int(job_id),
                        "scheduled_job_run_id": int(run_row_id),
                        "scheduled_ts": int(scheduled_ts or 0),
                        "component_kind": "script",
                        "component_name": friendly_name,
                    },
                    updated_at=now,
                )
                conn.commit()
            finally:
                conn.close()

            payload = {
                "job_id": act_id,
                "target_hostname": str(hostname),
                "script_type": stype,
                "script_name": friendly_name,
                "script_path": path_norm,
                "script_content": encoded_content,
                "script_encoding": "base64",
                "environment": env_map,
                "variables": variables,
                "timeout_seconds": timeout_seconds,
                "files": doc.get("files") or [],
                "run_mode": normalized_run_mode,
                "admin_user": "",
                "admin_pass": "",
            }
            payload.update(
                build_currentuser_dispatch_fields(
                    run_mode=normalized_run_mode,
                    session_target="all_active_sessions",
                )
            )
            if signature_b64:
                payload["signature"] = signature_b64
            if sig_alg:
                payload["sig_alg"] = sig_alg
            if signing_key_b64:
                payload["signing_key"] = signing_key_b64
            payload["context"] = {
                "scheduled_job_id": int(job_id),
                "scheduled_job_run_id": int(run_row_id),
                "scheduled_ts": int(scheduled_ts or 0),
                "queue_lane": "scheduled_job_system",
                "activity_kind": "scheduled_job",
                "activity_metadata": {
                    "assembly_source": assembly_source,
                    "scheduled_job_id": int(job_id),
                    "scheduled_job_run_id": int(run_row_id),
                    "scheduled_ts": int(scheduled_ts or 0),
                    "component_kind": "script",
                    "component_name": friendly_name,
                },
            }
            assembly_guid = str(record.get("assembly_guid") or "").strip().lower() if isinstance(record, dict) else ""
            if assembly_guid:
                payload["context"]["assembly_guid"] = assembly_guid
            try:
                emitted = False
                delivery_mode = "broadcast"
                if target_service_mode and callable(self._emit_host_service_event):
                    emitted = bool(
                        self._emit_host_service_event(
                            str(hostname),
                            target_service_mode,
                            "quick_job_run",
                            payload,
                        )
                    )
                    delivery_mode = f"targeted:{target_service_mode}"
                else:
                    delivery_mode = "broadcast"

                if not emitted and target_service_mode and callable(self._emit_host_service_event):
                    failure_text = (
                        f"No {target_service_mode} agent socket is registered for host {hostname}; "
                        f"unable to dispatch scheduled job."
                    )
                    fail_conn = self._conn()
                    try:
                        update_activity_history_row(
                            fail_conn,
                            int(act_id),
                            status=RUN_STATUS_FAILED,
                            stderr=failure_text,
                            updated_at=now,
                            finished_at=now,
                        )
                        fail_cur = fail_conn.cursor()
                        fail_cur.execute(
                            """
                            UPDATE scheduled_job_runs
                               SET status=?,
                                   finished_ts=?,
                                   updated_at=?,
                                   error=?
                             WHERE id=?
                            """,
                            (
                                RUN_STATUS_FAILED,
                                now,
                                now,
                                failure_text,
                                int(run_row_id),
                            ),
                        )
                        fail_conn.commit()
                    finally:
                        fail_conn.close()
                    try:
                        self.socketio.emit(
                            "device_activity_changed",
                            {
                                "hostname": str(hostname),
                                "activity_id": int(act_id),
                                "change": "updated",
                                "source": "scheduled_job",
                            },
                        )
                    except Exception:
                        pass
                    self._log_event(
                        "quick job dispatch failed",
                        job_id=int(job_id),
                        host=str(hostname),
                        run_id=run_row_id,
                        level="ERROR",
                        extra={
                            "error": failure_text,
                            "script_path": path_norm,
                            "scheduled_ts": int(scheduled_ts or 0),
                            "assembly_source": assembly_source,
                            "delivery_mode": delivery_mode,
                        },
                    )
                    return {
                        "activity_id": int(act_id),
                        "component_kind": "script",
                        "script_type": stype,
                        "component_path": path_norm,
                        "component_name": friendly_name,
                    }

                if not emitted:
                    self.socketio.emit("quick_job_run", payload)
                    delivery_mode = "broadcast"

                if act_id:
                    try:
                        self.socketio.emit(
                            "device_activity_changed",
                            {
                                "hostname": str(hostname),
                                "activity_id": int(act_id),
                                "change": "created",
                                "source": "scheduled_job",
                            },
                        )
                    except Exception:
                        pass
                self._log_event(
                    "emitted quick job payload",
                    job_id=int(job_id),
                    host=str(hostname),
                    run_id=run_row_id,
                    extra={
                        "has_signature": bool(signature_b64),
                        "run_mode": normalized_run_mode,
                        "scheduled_ts": int(scheduled_ts or 0),
                        "assembly_source": assembly_source,
                        "assembly_guid": assembly_guid or "",
                        "delivery_mode": delivery_mode,
                    },
                )
            except Exception as exc:
                self._log_event(
                    "quick job dispatch failed",
                    job_id=int(job_id),
                    host=str(hostname),
                    run_id=run_row_id,
                    level="ERROR",
                    extra={
                        "error": str(exc),
                        "script_path": path_norm,
                        "scheduled_ts": int(scheduled_ts or 0),
                        "assembly_source": assembly_source,
                    },
                )
            if act_id:
                return {
                    "activity_id": int(act_id),
                    "component_name": friendly_name,
                    "component_path": path_norm,
                    "script_type": stype,
                    "component_kind": "script",
                }
            return None
        except Exception as exc:
            # Keep scheduler resilient
            self._log_event(
                "unhandled exception during script dispatch",
                job_id=int(job_id),
                host=str(hostname),
                run_id=run_row_id,
                level="ERROR",
                extra={"error": str(exc)},
            )
        return None

    # ---------- DB helpers ----------
    def _conn(self):
        return self._db_conn_factory()

    def set_host_service_emitter(self, fn: Optional[Callable[[str, str, str, Any], bool]]):
        self._emit_host_service_event = fn

    def set_credential_fetcher(self, fn: Optional[Callable[[int], Optional[Dict[str, Any]]]]):
        self._credential_fetcher = fn

    def set_public_base_url_lookup(self, fn: Optional[Callable[[], str]]):
        self._public_base_url_lookup = fn

    def set_vpn_session_lookup(self, fn: Optional[Callable[[], Dict[str, Dict[str, Any]]]]):
        self._vpn_session_lookup = fn

    def set_vpn_session_prepare(
        self,
        fn: Optional[Callable[[Sequence[str], Optional[Sequence[int]]], Dict[str, Dict[str, Any]]]],
    ):
        self._vpn_session_prepare = fn

    def set_workflow_run_launcher(self, fn: Optional[Callable[..., Dict[str, Any]]]):
        self._workflow_run_launcher = fn

    def _init_tables(self):
        conn = self._conn()
        cur = conn.cursor()
        # Runs table captures each firing
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                job_id INTEGER NOT NULL,
                scheduled_ts INTEGER,
                started_ts INTEGER,
                finished_ts INTEGER,
                status TEXT,
                error TEXT,
                created_at INTEGER,
                updated_at INTEGER,
                target_hostname TEXT,
                skip_reason TEXT,
                shared_execution INTEGER NOT NULL DEFAULT 0,
                component_index INTEGER,
                component_kind TEXT,
                component_name TEXT,
                workflow_run_id INTEGER,
                FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE CASCADE
            )
            """
        )
        # Add columns incrementally for existing databases.
        try:
            cur.execute("PRAGMA table_info(scheduled_job_runs)")
            cols = {row[1] for row in cur.fetchall()}
            if "target_hostname" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN target_hostname TEXT")
            if "skip_reason" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN skip_reason TEXT")
            if "shared_execution" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN shared_execution INTEGER NOT NULL DEFAULT 0")
            if "component_index" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN component_index INTEGER")
            if "component_kind" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN component_kind TEXT")
            if "component_name" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN component_name TEXT")
            if "workflow_run_id" not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN workflow_run_id INTEGER")
        except Exception:
            pass
        # Helpful index for lookups
        try:
            cur.execute("CREATE INDEX IF NOT EXISTS idx_runs_job_sched_target ON scheduled_job_runs(job_id, scheduled_ts, target_hostname)")
        except Exception:
            pass
        try:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS scheduled_job_run_activity (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    run_id INTEGER NOT NULL,
                    activity_id INTEGER NOT NULL,
                    component_kind TEXT,
                    script_type TEXT,
                    component_path TEXT,
                    component_name TEXT,
                    created_at INTEGER,
                    FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                    FOREIGN KEY(activity_id) REFERENCES activity_history(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_run_activity_run ON scheduled_job_run_activity(run_id)")
            cur.execute("CREATE UNIQUE INDEX IF NOT EXISTS idx_run_activity_activity ON scheduled_job_run_activity(activity_id)")
        except Exception:
            pass
        try:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS scheduled_job_onboarding_targets (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    run_id INTEGER NOT NULL,
                    job_id INTEGER NOT NULL,
                    scheduled_ts INTEGER NOT NULL,
                    site_id INTEGER,
                    target_input TEXT NOT NULL,
                    target_address TEXT,
                    target_hostname TEXT,
                    ssh_port INTEGER NOT NULL DEFAULT 22,
                    status TEXT NOT NULL,
                    detail TEXT,
                    stdout_snippet TEXT,
                    stderr_snippet TEXT,
                    approval_reference TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL,
                    finished_at INTEGER,
                    FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                    FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_onboarding_targets_run ON scheduled_job_onboarding_targets(run_id)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_onboarding_targets_job ON scheduled_job_onboarding_targets(job_id, scheduled_ts)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_onboarding_targets_status ON scheduled_job_onboarding_targets(status)")
        except Exception:
            pass
        try:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS scheduled_job_run_targets (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    run_id INTEGER NOT NULL,
                    device_guid TEXT,
                    hostname TEXT NOT NULL,
                    site_id INTEGER,
                    resolved_from_filter_id INTEGER,
                    inventory_hostname TEXT,
                    wireguard_peer_ip TEXT,
                    resolved_connection TEXT,
                    resolution_status TEXT,
                    resolution_reason TEXT,
                    resolved_from_filter_ids_json TEXT,
                    created_at INTEGER NOT NULL,
                    FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("PRAGMA table_info(scheduled_job_run_targets)")
            target_cols = {row[1] for row in cur.fetchall()}
            if "inventory_hostname" not in target_cols:
                cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN inventory_hostname TEXT")
            if "wireguard_peer_ip" not in target_cols:
                cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN wireguard_peer_ip TEXT")
            if "resolved_connection" not in target_cols:
                cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolved_connection TEXT")
            if "resolution_status" not in target_cols:
                cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolution_status TEXT")
            if "resolution_reason" not in target_cols:
                cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolution_reason TEXT")
            if "resolved_from_filter_ids_json" not in target_cols:
                cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolved_from_filter_ids_json TEXT")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_run ON scheduled_job_run_targets(run_id)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_filter ON scheduled_job_run_targets(resolved_from_filter_id)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_host ON scheduled_job_run_targets(hostname)")
        except Exception:
            pass
        conn.commit()
        conn.close()

    def _decode_secret_blob(self, value: Any) -> str:
        if value is None:
            return ""
        if isinstance(value, memoryview):
            value = value.tobytes()
        if isinstance(value, bytes):
            try:
                return value.decode("utf-8")
            except Exception:
                return value.decode("utf-8", errors="replace")
        return str(value)

    def _parse_metadata_json(self, value: Any) -> Dict[str, Any]:
        if isinstance(value, dict):
            return dict(value)
        raw = str(value or "").strip()
        if not raw:
            return {}
        try:
            parsed = json.loads(raw)
        except Exception:
            return {}
        if isinstance(parsed, dict):
            return dict(parsed)
        return {}

    def _credential_reset_warning(self, credential_id: Optional[int]) -> Optional[Dict[str, str]]:
        if credential_id is None:
            return None
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT metadata_json FROM credentials WHERE id=?", (int(credential_id),))
            row = cur.fetchone()
            if not row:
                return None
            metadata = self._parse_metadata_json(row[0])
            if not credential_secret_reset_required(metadata):
                return None
            return {
                "warning_code": "credential_reset_required",
                "warning_message": CREDENTIAL_RESET_REQUIRED_MESSAGE,
            }
        except Exception:
            return None
        finally:
            conn.close()

    def _load_credential(self, credential_id: Optional[int]) -> Optional[Dict[str, Any]]:
        if credential_id is None:
            return None
        if callable(self._credential_fetcher):
            try:
                return self._credential_fetcher(int(credential_id))
            except (AegisLockedError, AegisDataCorruptionError, AegisSecretResetRequiredError):
                raise
            except Exception as exc:
                self._log_event(
                    "credential fetcher failed",
                    level="ERROR",
                    extra={"credential_id": credential_id, "error": str(exc)},
                )
                return None

        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    name,
                    site_id,
                    credential_type,
                    connection_type,
                    username,
                    password_encrypted,
                    private_key_encrypted,
                    private_key_passphrase_encrypted,
                    become_method,
                    become_username,
                    become_password_encrypted,
                    metadata_json
                  FROM credentials
                 WHERE id=?
                """,
                (int(credential_id),),
            )
            row = cur.fetchone()
            if not row:
                return None
            metadata = {}
            metadata = self._parse_metadata_json(row[12])
            if credential_secret_reset_required(metadata):
                raise AegisSecretResetRequiredError(CREDENTIAL_RESET_REQUIRED_MESSAGE)
            return {
                "id": int(row[0]),
                "name": row[1] or "",
                "site_id": row[2],
                "credential_type": row[3] or "",
                "connection_type": row[4] or "",
                "username": row[5] or "",
                "password": self._decode_secret_blob(row[6]),
                "private_key": self._decode_secret_blob(row[7]),
                "private_key_passphrase": self._decode_secret_blob(row[8]),
                "become_method": row[9] or "",
                "become_username": row[10] or "",
                "become_password": self._decode_secret_blob(row[11]),
                "metadata": metadata if isinstance(metadata, dict) else {},
            }
        except Exception as exc:
            self._log_event(
                "failed to load credential from database",
                level="ERROR",
                extra={"credential_id": credential_id, "error": str(exc)},
            )
            return None
        finally:
            conn.close()

    def _load_service_account(self, agent_id: str) -> Optional[Dict[str, Any]]:
        agent_key = str(agent_id or "").strip()
        if not agent_key:
            return None
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT agent_id, username, password_encrypted
                  FROM agent_service_account
                 WHERE agent_id=?
                """,
                (agent_key,),
            )
            row = cur.fetchone()
            if not row:
                return None
            return {
                "agent_id": row[0] or "",
                "username": row[1] or "",
                "password": self._decode_secret_blob(row[2]),
            }
        except Exception as exc:
            self._log_event(
                "failed to load agent service account",
                level="ERROR",
                extra={"agent_id": agent_key, "error": str(exc)},
            )
            return None
        finally:
            conn.close()

    def _active_vpn_sessions(self) -> Dict[str, Dict[str, Any]]:
        if not callable(self._vpn_session_lookup):
            return {}
        try:
            sessions = self._vpn_session_lookup() or {}
        except Exception as exc:
            self._log_event(
                "failed to gather active vpn sessions",
                level="ERROR",
                extra={"error": str(exc)},
            )
            return {}
        normalized: Dict[str, Dict[str, Any]] = {}
        for agent_id, payload in sessions.items():
            key = str(agent_id or "").strip()
            if not key or not isinstance(payload, dict):
                continue
            normalized[key] = payload
        return normalized

    def _prepare_vpn_sessions(
        self,
        agent_ids: Sequence[str],
        *,
        required_ports: Optional[Sequence[int]] = None,
    ) -> Dict[str, Dict[str, Any]]:
        normalized_agent_ids = sorted({str(agent_id or "").strip() for agent_id in agent_ids if str(agent_id or "").strip()})
        if not normalized_agent_ids:
            return self._active_vpn_sessions()
        if not callable(self._vpn_session_prepare):
            return self._active_vpn_sessions()
        try:
            sessions = self._vpn_session_prepare(normalized_agent_ids, required_ports) or {}
        except Exception as exc:
            self._log_event(
                "failed to prepare vpn sessions for shared ansible run",
                level="ERROR",
                extra={
                    "agent_count": len(normalized_agent_ids),
                    "required_ports": [int(port) for port in (required_ports or []) if str(port).strip()],
                    "error": str(exc),
                },
            )
            return self._active_vpn_sessions()
        normalized: Dict[str, Dict[str, Any]] = {}
        for agent_id, payload in sessions.items():
            key = str(agent_id or "").strip()
            if not key or not isinstance(payload, dict):
                continue
            normalized[key] = payload
        return normalized

    def _device_lookup_key_for_target(self, target: Mapping[str, Any]) -> str:
        hostname = str(target.get("hostname") or "").strip()
        site_id = target.get("site_id")
        device_guid = str(target.get("device_guid") or "").strip().lower()
        if device_guid:
            return f"guid:{device_guid}"
        if hostname:
            try:
                if site_id is not None:
                    return f"site:{int(site_id)}:{hostname.lower()}"
            except Exception:
                pass
            return f"host:{hostname.lower()}"
        return ""

    def _preflight_remote_port(
        self,
        *,
        host: str,
        port: int,
        attempts: int = 3,
        timeout_seconds: float = 1.25,
        retry_delay_seconds: float = 0.5,
        probe: str = "tcp",
        banner_timeout_seconds: Optional[float] = None,
    ) -> str:
        normalized_host = str(host or "").strip()
        if not normalized_host:
            return "missing_host"
        normalized_port = int(port)
        normalized_probe = str(probe or "tcp").strip().lower() or "tcp"
        resolved_banner_timeout = (
            max(0.1, float(banner_timeout_seconds))
            if banner_timeout_seconds is not None
            else max(0.1, float(timeout_seconds or 0.0))
        )
        connect_timeout_seconds = max(0.1, float(timeout_seconds or 0.0))
        normalized_attempts = max(1, int(attempts))
        if normalized_probe == "ssh_banner":
            # Slow WireGuard-backed SSH targets can need a single sustained
            # connect window. Reconnecting every ~1s can falsely reject hosts
            # that Ansible can still reach successfully.
            connect_timeout_seconds = max(connect_timeout_seconds, resolved_banner_timeout)
            normalized_attempts = 1
        last_error = ""
        for attempt in range(normalized_attempts):
            sock = None
            try:
                sock = socket.create_connection((normalized_host, normalized_port), timeout=connect_timeout_seconds)
                if normalized_probe == "ssh_banner":
                    banner_error = self._preflight_ssh_banner(sock, timeout_seconds=resolved_banner_timeout)
                    if banner_error:
                        last_error = banner_error
                        return last_error
                return ""
            except Exception as exc:
                last_error = str(exc).strip() or exc.__class__.__name__
            finally:
                if sock is not None:
                    try:
                        sock.close()
                    except Exception:
                        pass
            if attempt + 1 < normalized_attempts:
                time.sleep(max(0.0, float(retry_delay_seconds)))
        return last_error or "connection_failed"

    def _preflight_ssh_banner(self, sock: socket.socket, *, timeout_seconds: float) -> str:
        deadline = time.monotonic() + max(0.1, float(timeout_seconds or 0.0))
        received = b""
        while len(received) < 255:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            try:
                sock.settimeout(remaining)
                chunk = sock.recv(128)
            except socket.timeout:
                break
            except Exception as exc:
                return str(exc).strip() or exc.__class__.__name__
            if not chunk:
                break
            received += chunk
            for raw_line in received.splitlines():
                if raw_line.startswith(b"SSH-"):
                    return ""
        if received:
            compact = received.decode("utf-8", errors="replace").replace("\r", "\\r").replace("\n", "\\n").strip()
            if compact:
                return f"invalid_ssh_banner:{compact[:80]}"
        return "ssh_banner_timeout"

    def _preflight_ssh_session(
        self,
        *,
        host: str,
        port: int,
        username: str,
        password: str = "",
        private_key_text: str = "",
        timeout_seconds: float = _DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS,
        become_method: str = "",
        become_user: str = "",
        become_password: str = "",
    ) -> str:
        normalized_host = str(host or "").strip()
        normalized_username = str(username or "").strip()
        if not normalized_host or not normalized_username:
            return ""

        ssh_bin = shutil.which("ssh")
        if not ssh_bin:
            return "ssh_client_unavailable"

        try:
            import pexpect  # type: ignore
        except Exception:
            return "ssh_probe_dependency_unavailable"

        login_marker = "__BOREALIS_LOGIN_OK__"
        sudo_prompt_marker = "__BOREALIS_SUDO_PROMPT__"
        ready_marker = "__BOREALIS_READY__"
        normalized_timeout = max(1.0, float(timeout_seconds or 0.0))
        connect_timeout = max(1, int(math.ceil(normalized_timeout)))

        remote_steps = [
            f"printf '%s\\n' {login_marker}",
            "mkdir -p /tmp/.ansible-borealis",
        ]
        normalized_become_method = str(become_method or "").strip().lower()
        if normalized_become_method == "sudo":
            target_user = str(become_user or "root").strip() or "root"
            remote_steps.append(
                "sudo -S -p {prompt} -u {user} /bin/sh -lc {command}".format(
                    prompt=shlex.quote(sudo_prompt_marker),
                    user=shlex.quote(target_user),
                    command=shlex.quote("true"),
                )
            )
        remote_steps.append(f"printf '%s\\n' {ready_marker}")
        remote_command = " && ".join(remote_steps)

        probe_root = Path(tempfile.mkdtemp(prefix="borealis-ssh-preflight-"))
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
                "NumberOfPasswordPrompts=1",
                "-o",
                "ServerAliveInterval=5",
                "-o",
                "ServerAliveCountMax=1",
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
                if not password:
                    args.extend(
                        [
                            "-o",
                            "BatchMode=yes",
                            "-o",
                            "PreferredAuthentications=publickey",
                            "-o",
                            "PasswordAuthentication=no",
                            "-o",
                            "KbdInteractiveAuthentication=no",
                        ]
                    )
            args.append(f"{normalized_username}@{normalized_host}")
            args.append(remote_command)

            child = pexpect.spawn(args[0], args[1:], encoding="utf-8", timeout=normalized_timeout)
            transcript_parts: List[str] = []
            login_seen = False
            ready_seen = False
            ssh_password_sent = False
            sudo_password_sent = False

            patterns = [
                pexpect.EOF,
                pexpect.TIMEOUT,
                r"(?i)are you sure you want to continue connecting",
                sudo_prompt_marker,
                r"(?i)(?:password|passphrase).*:",
                login_marker,
                ready_marker,
                r"(?i)permission denied",
            ]

            while True:
                index = child.expect(patterns)
                before = str(child.before or "")
                if before:
                    transcript_parts.append(before)

                if index == 0:
                    break
                if index == 1:
                    return "ssh_session_timeout"
                if index == 2:
                    child.sendline("yes")
                    continue
                if index == 3:
                    secret = str(become_password or password or "").strip()
                    if not secret or sudo_password_sent:
                        return "sudo_password_required"
                    child.sendline(secret)
                    sudo_password_sent = True
                    continue
                if index == 4:
                    if login_seen:
                        secret = str(become_password or password or "").strip()
                        if not secret or sudo_password_sent:
                            return "sudo_password_required"
                        child.sendline(secret)
                        sudo_password_sent = True
                    else:
                        secret = str(password or "").strip()
                        if not secret or ssh_password_sent:
                            return "ssh_password_required"
                        child.sendline(secret)
                        ssh_password_sent = True
                    continue
                if index == 5:
                    login_seen = True
                    continue
                if index == 6:
                    ready_seen = True
                    continue
                if index == 7:
                    return "permission_denied"

            trailing = str(child.before or "")
            if trailing:
                transcript_parts.append(trailing)
            try:
                child.close()
            except Exception:
                pass

            if ready_seen:
                return ""

            transcript = " ".join(part.strip() for part in transcript_parts if str(part).strip())
            compact = transcript.replace("\r", " ").replace("\n", " ").strip()
            if compact:
                return f"ssh_session_failed:{compact[:80]}"
            return "ssh_session_failed"
        finally:
            shutil.rmtree(str(probe_root), ignore_errors=True)

    def _resolve_mixed_ssh_auth_mode(
        self,
        *,
        host: str,
        port: int,
        username: str,
        password: str,
        private_key_text: str,
    ) -> str:
        if not host or not username or not password or not private_key_text:
            return "combined"
        probe_timeout = _env_non_negative_float(
            _SSH_SESSION_TIMEOUT_ENV,
            _DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS,
        )
        key_probe_result = self._preflight_ssh_session(
            host=host,
            port=port,
            username=username,
            password="",
            private_key_text=private_key_text,
            timeout_seconds=probe_timeout,
        )
        password_probe_result = ""
        if not key_probe_result:
            mode = "key"
        else:
            mode = "key"
            password_probe_result = self._preflight_ssh_session(
                host=host,
                port=port,
                username=username,
                password=password,
                private_key_text="",
                timeout_seconds=probe_timeout,
            )
            if not password_probe_result:
                mode = "password"
        self._log_event(
            "mixed ssh credential auth probe selected mode",
            host=host,
            extra={
                "port": int(port),
                "auth_mode": mode,
                "probe_result": key_probe_result or "key_accepted",
                "key_probe_result": key_probe_result or "key_accepted",
                "password_probe_result": password_probe_result or ("not_run" if mode == "key" else "password_accepted"),
            },
        )
        return mode

    def _onboarding_target_cap(self) -> int:
        return _env_positive_int("BOREALIS_ONBOARDING_TARGET_CAP", DEFAULT_ONBOARDING_TARGET_CAP)

    def _onboarding_concurrency(self) -> int:
        return _env_positive_int("BOREALIS_ONBOARDING_CONCURRENCY", DEFAULT_ONBOARDING_CONCURRENCY)

    def _onboarding_install_timeout_seconds(self) -> float:
        return _env_non_negative_float("BOREALIS_ONBOARDING_INSTALL_TIMEOUT_SECONDS", 900.0) or 900.0

    def _public_base_url(self) -> str:
        if callable(self._public_base_url_lookup):
            try:
                value = str(self._public_base_url_lookup() or "").strip()
                if value:
                    return value.rstrip("/")
            except Exception as exc:
                self._log_event(
                    "public base url lookup failed",
                    level="WARNING",
                    extra={"error": str(exc)},
                )
        for env_name in ("BOREALIS_AGENT_PUBLIC_BASE_URL", "BOREALIS_PUBLIC_BASE_URL", "BOREALIS_SERVER_URL"):
            value = str(os.getenv(env_name, "") or "").strip()
            if value:
                return value.rstrip("/")
        return ""

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
        if platform in {"linux_ssh", "ssh"}:
            platform = ONBOARDING_PLATFORM_LINUX
        elif platform in {"windows_remote", "windows_smb", "smb", "winrm"}:
            platform = ONBOARDING_PLATFORM_WINDOWS
        if platform not in {ONBOARDING_PLATFORM_LINUX, ONBOARDING_PLATFORM_WINDOWS}:
            return {}, "Agent platform must be Linux or Windows."
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
        if platform == ONBOARDING_PLATFORM_WINDOWS:
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
                normalized_method = method_aliases.get(method, method)
                if normalized_method not in ONBOARDING_WINDOWS_METHODS:
                    return {}, "Unsupported Windows onboarding method."
                if normalized_method not in methods:
                    methods.append(normalized_method)
            methods = _windows_onboarding_methods_with_required_fallbacks(methods)
        else:
            methods = ["ssh"]
        transport_port = ssh_port if platform == ONBOARDING_PLATFORM_LINUX else windows_port
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
            return [
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

    def _clear_scheduled_job_run_history(self, job_id: int) -> int:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT id FROM scheduled_job_runs WHERE job_id=?", (int(job_id),))
            run_ids = [int(row[0]) for row in cur.fetchall()]
            if not run_ids:
                return 0
            placeholders = ",".join(["?"] * len(run_ids))
            params = tuple(run_ids)
            cur.execute(f"DELETE FROM scheduled_job_run_activity WHERE run_id IN ({placeholders})", params)
            cur.execute(f"DELETE FROM scheduled_job_run_targets WHERE run_id IN ({placeholders})", params)
            cur.execute(f"DELETE FROM scheduled_job_onboarding_targets WHERE run_id IN ({placeholders})", params)
            cur.execute(f"DELETE FROM scheduled_job_runs WHERE id IN ({placeholders})", params)
            cleared = int(cur.rowcount or 0)
            conn.commit()
            return cleared
        finally:
            conn.close()

    def _update_onboarding_target_row(
        self,
        row_id: int,
        *,
        status: str,
        detail: str = "",
        stdout: str = "",
        stderr: str = "",
        approval_reference: str = "",
        finished: bool = False,
        redactions: Optional[Sequence[Any]] = None,
    ) -> None:
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
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
                    sanitize_output(detail, redactions=redactions, limit=500),
                    sanitize_output(stdout, redactions=redactions),
                    sanitize_output(stderr, redactions=redactions),
                    str(approval_reference or ""),
                    now,
                    now if finished else None,
                    int(row_id),
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def _onboarding_target_already_known(self, host: str, site_id: Optional[int]) -> bool:
        value = str(host or "").strip().lower()
        if not value:
            return False
        conn = self._conn()
        try:
            cur = conn.cursor()
            params: List[Any] = [value, value, value]
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
            approval_params: List[Any] = [value, value]
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
                           approved_by_user_id
                      FROM device_approvals
                     WHERE approval_reference=?
                  ORDER BY updated_at DESC
                     LIMIT 1
                    """,
                    (normalized_reference,),
                )
            else:
                cur.execute(
                    """
                    SELECT id,
                           approval_reference,
                           status,
                           hostname_claimed,
                           updated_at,
                           approved_by_user_id
                      FROM device_approvals
                     WHERE onboarding_job_id=?
                       AND onboarding_run_id=?
                       AND onboarding_target=?
                  ORDER BY updated_at DESC
                     LIMIT 1
                    """,
                    (int(job_id), int(run_id), str(target or "").strip()),
                )
            row = cur.fetchone()
            if not row:
                return {}
            return {
                "approval_id": row[0] or "",
                "approval_reference": row[1] or "",
                "approval_status": row[2] or "",
                "approval_hostname": row[3] or "",
                "approval_updated_at": row[4] or "",
                "approved_by_user_id": row[5] or "",
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
        for row in rows:
            next_row = dict(row)
            status = str(next_row.get("status") or "").strip().lower()
            if status == ONBOARDING_STATUS_WAITING_APPROVAL:
                target = str(
                    next_row.get("target_address")
                    or next_row.get("target_hostname")
                    or next_row.get("target_input")
                    or ""
                ).strip()
                approval_context = self._lookup_onboarding_approval_context(
                    job_id=int(next_row.get("job_id") or 0),
                    run_id=int(next_row.get("run_id") or 0),
                    target=target,
                    approval_reference=str(next_row.get("approval_reference") or "").strip(),
                )
                next_row.update(approval_context)
                approval_reference = str(approval_context.get("approval_reference") or "").strip()
                if approval_reference:
                    next_row["approval_reference"] = approval_reference
                    self._update_onboarding_target_approval_reference(int(next_row.get("id") or 0), approval_reference)
                approval_status = str(approval_context.get("approval_status") or "").strip().lower()
                if approval_status == "approved":
                    next_row["status"] = "approved"
                    next_row["detail"] = "Device approved. Agent finalizing enrollment."
                elif approval_status == "completed":
                    next_row["status"] = "completed"
                    next_row["detail"] = "Device approved and enrollment completed."
                elif approval_status in {"denied", "expired"}:
                    next_row["status"] = approval_status
                    next_row["detail"] = f"Device approval {approval_status}."
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
                "if [ \"$(uname -s 2>/dev/null || true)\" != \"Linux\" ]; then echo '__BOREALIS_UNSUPPORTED_OS__' >&2; exit 42; fi",
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
        remote_command = f"printf '%s\\n' {shlex.quote(login_marker)} && /bin/sh -lc {shlex.quote(command)}"

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

    def _run_single_onboarding_target(
        self,
        *,
        row: Dict[str, Any],
        site: Dict[str, Any],
        credential: Dict[str, Any],
        branch: str,
        server_url: str,
        job_id: int,
        run_id: int,
        platform: str = ONBOARDING_PLATFORM_LINUX,
        windows_methods: Optional[Sequence[str]] = None,
        winrm_port: int = DEFAULT_ONBOARDING_WINRM_PORT,
    ) -> str:
        normalized_platform = str(platform or ONBOARDING_PLATFORM_LINUX).strip().lower()
        if normalized_platform == ONBOARDING_PLATFORM_WINDOWS:
            return self._run_windows_onboarding_target(
                row=row,
                site=site,
                credential=credential,
                branch=branch,
                server_url=server_url,
                job_id=job_id,
                run_id=run_id,
                windows_methods=windows_methods or ONBOARDING_WINDOWS_METHODS,
                winrm_port=winrm_port,
            )
        return self._run_linux_onboarding_target(
            row=row,
            site=site,
            credential=credential,
            branch=branch,
            server_url=server_url,
            job_id=job_id,
            run_id=run_id,
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
    ) -> str:
        row_id = int(row["id"])
        host = str(row.get("target_address") or row.get("target_hostname") or "").strip()
        port = int(row.get("ssh_port") or DEFAULT_ONBOARDING_SSH_PORT)
        redactions = [site.get("enrollment_code"), server_url, credential.get("password"), credential.get("private_key")]
        self._update_onboarding_target_row(row_id, status=ONBOARDING_STATUS_RUNNING, detail="Connecting to SSH.", redactions=redactions)
        if self._onboarding_target_already_known(host, site.get("id")):
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_SKIPPED,
                detail="Target already appears enrolled for this site.",
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
        become_method = str(credential.get("become_method") or "").strip().lower()
        become_user = str(credential.get("become_username") or "root").strip() or "root"
        become_password = str(credential.get("become_password") or password or "")
        session_error = self._preflight_ssh_session(
            host=host,
            port=port,
            username=username,
            password=password,
            private_key_text=private_key,
            timeout_seconds=_env_non_negative_float(
                _SSH_SESSION_TIMEOUT_ENV,
                _DEFAULT_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS,
            ),
            become_method="sudo" if become_method == "sudo" else "",
            become_user=become_user,
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
            private_key_text=private_key,
            private_key_passphrase=str(credential.get("private_key_passphrase") or ""),
            command=command,
            timeout_seconds=self._onboarding_install_timeout_seconds(),
            become_password=become_password,
        )
        exit_code = int(result.get("exit_code") or 0)
        stdout = result.get("stdout") or ""
        stderr = result.get("stderr") or ""
        if exit_code == 42:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_SKIPPED,
                detail="Target is not Linux.",
                stdout=stdout,
                stderr=stderr,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_SKIPPED
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

    def _windows_onboarding_script(
        self,
        *,
        branch: str,
        server_url: str,
        enrollment_code: str,
        job_id: int,
        run_id: int,
        target: str,
    ) -> str:
        branch_ref = str(branch or "main").strip() or "main"
        zip_url = f"https://codeload.github.com/bunny-lab-io/Borealis/zip/refs/heads/{url_quote(branch_ref, safe='/._-')}"
        return "\n".join(
            [
                "$ErrorActionPreference = 'Stop'",
                "$ProgressPreference = 'SilentlyContinue'",
                "$exitCode = 1",
                "try {",
                "  $root = Join-Path $env:ProgramData 'Borealis\\Onboarding'",
                "  New-Item -ItemType Directory -Force -Path $root | Out-Null",
                f"  $zipPath = Join-Path $root { _powershell_single_quoted(f'Borealis-{int(run_id)}.zip') }",
                f"  $extractPath = Join-Path $root { _powershell_single_quoted(f'source-{int(run_id)}') }",
                "  if (Test-Path $zipPath) { Remove-Item -Force $zipPath }",
                "  if (Test-Path $extractPath) { Remove-Item -Recurse -Force $extractPath }",
                "  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12",
                f"  Write-Output ('Syncing Borealis ref {branch_ref} from {zip_url}.')",
                f"  Invoke-WebRequest -UseBasicParsing -Uri { _powershell_single_quoted(zip_url) } -OutFile $zipPath",
                "  Expand-Archive -Path $zipPath -DestinationPath $extractPath -Force",
                "  $deployScript = Get-ChildItem -Path $extractPath -Filter 'Borealis.ps1' -Recurse | Select-Object -First 1",
                "  if (-not $deployScript) { throw 'Borealis.ps1 missing from branch archive.' }",
                f"  $env:BOREALIS_ONBOARDING_JOB_ID = { _powershell_single_quoted(str(int(job_id))) }",
                f"  $env:BOREALIS_ONBOARDING_RUN_ID = { _powershell_single_quoted(str(int(run_id))) }",
                f"  $env:BOREALIS_ONBOARDING_TARGET = { _powershell_single_quoted(str(target or '').strip()) }",
                "  $env:BOREALIS_AGENT_NONINTERACTIVE = '1'",
                "  $env:BOREALIS_AGENT_NO_TTY = '1'",
                "  Write-Output ('Launching ' + $deployScript.FullName + ' -Agent -ServerUrl [redacted] -EnrollmentCode [redacted] -NewEngine.')",
                f"  & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $deployScript.FullName -Agent -ServerUrl { _powershell_single_quoted(server_url) } -EnrollmentCode { _powershell_single_quoted(enrollment_code) } -NewEngine",
                "  if ($null -ne $LASTEXITCODE) { $exitCode = [int]$LASTEXITCODE } else { $exitCode = 0 }",
                "} catch {",
                "  Write-Error $_.Exception.Message",
                "  if ($exitCode -eq 0) { $exitCode = 1 }",
                "} finally {",
                "  Write-Output \"__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=$exitCode\"",
                "}",
                "exit $exitCode",
            ]
        )

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
        last_error = ""
        for remote_name in _windows_smb_remote_names(host, credential):
            for attempt in range(3):
                smb = None
                try:
                    smb = SMBConnection(remote_name, host, sess_port=int(port), timeout=20)
                    smb.login(username, password, domain)
                    return smb
                except Exception as exc:
                    last_error = str(exc).strip() or exc.__class__.__name__
                    if smb is not None:
                        try:
                            smb.close()
                        except Exception:
                            pass
                    if attempt < 2:
                        time.sleep(1.0)
        raise RuntimeError(f"smb_connection_failed:{last_error or 'connection_failed'}")

    def _windows_smb_stage_script(self, smb: Any, script: str) -> Tuple[str, str, str]:
        stem = f"BorealisOnboard-{uuid.uuid4().hex}"
        remote_dir = "Temp\\BorealisOnboarding"
        try:
            smb.createDirectory("ADMIN$", remote_dir)
        except Exception:
            pass
        script_path = f"{remote_dir}\\{stem}.ps1"
        output_path = f"{remote_dir}\\{stem}.log"
        script_bytes = io.BytesIO(str(script or "").encode("utf-8-sig"))
        smb.putFile("ADMIN$", script_path, script_bytes.read)
        return (
            f"C:\\Windows\\{script_path}",
            f"C:\\Windows\\{output_path}",
            output_path,
        )

    def _read_windows_smb_file(self, smb: Any, path: str) -> str:
        buffer = io.BytesIO()
        smb.getFile("ADMIN$", path, buffer.write)
        return buffer.getvalue().decode("utf-8", errors="replace")

    def _poll_windows_smb_onboarding_output(self, *, smb: Any, output_path: str, timeout_seconds: float) -> Dict[str, Any]:
        deadline = time.monotonic() + max(30.0, float(timeout_seconds or 0.0))
        last_output = ""
        last_error = ""
        while time.monotonic() < deadline:
            try:
                output = self._read_windows_smb_file(smb, output_path)
                if output:
                    last_output = output
                    exit_code = _windows_exit_code_from_output(output)
                    if exit_code is not None:
                        return {"exit_code": exit_code, "stdout": output, "stderr": ""}
            except Exception as exc:
                last_error = str(exc).strip() or exc.__class__.__name__
            time.sleep(2.0)
        return {
            "exit_code": 124,
            "stdout": last_output,
            "stderr": f"windows_onboarding_timeout{': ' + last_error if last_error else ''}",
        }

    def _execute_windows_smb_scm_onboarding(
        self,
        *,
        host: str,
        port: int,
        credential: Dict[str, Any],
        script: str,
        timeout_seconds: float,
    ) -> Dict[str, Any]:
        try:
            from impacket.dcerpc.v5 import scmr, transport  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"impacket_scm_unavailable:{exc}"}
        smb = None
        dce = None
        service_handle = None
        scm_handle = None
        stage = "SMB login"
        try:
            smb = self._open_windows_smb_connection(host=host, port=port, credential=credential)
            stage = "ADMIN$ script staging"
            script_abs, output_abs, output_path = self._windows_smb_stage_script(smb, script)
            service_name = f"BorealisOnboarding{uuid.uuid4().hex[:12]}"
            command = f'cmd.exe /Q /C powershell.exe -NoProfile -ExecutionPolicy Bypass -File "{script_abs}" > "{output_abs}" 2>&1'
            stage = "svcctl RPC bind"
            rpc_transport = transport.SMBTransport(host, int(port), r"\svcctl", smb_connection=smb)
            dce = rpc_transport.get_dce_rpc()
            dce.connect()
            dce.bind(scmr.MSRPC_UUID_SCMR)
            stage = "service creation"
            scm_resp = scmr.hROpenSCManagerW(dce)
            scm_handle = scm_resp["lpScHandle"]
            service_resp = scmr.hRCreateServiceW(
                dce,
                scm_handle,
                service_name,
                service_name,
                lpBinaryPathName=command,
                dwStartType=scmr.SERVICE_DEMAND_START,
            )
            service_handle = service_resp["lpServiceHandle"]
            try:
                stage = "service start"
                scmr.hRStartServiceW(dce, service_handle)
            except Exception as exc:
                start_error = str(exc).lower()
                if "1053" not in start_error and "timely fashion" not in start_error:
                    raise
            stage = "service output polling"
            return self._poll_windows_smb_onboarding_output(
                smb=smb,
                output_path=output_path,
                timeout_seconds=timeout_seconds,
            )
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
        script: str,
        timeout_seconds: float,
    ) -> Dict[str, Any]:
        try:
            from impacket.dcerpc.v5 import tsch, transport  # type: ignore
            from impacket.dcerpc.v5.dtypes import NULL  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"impacket_tsch_unavailable:{exc}"}
        smb = None
        dce = None
        task_path = f"\\BorealisOnboarding{uuid.uuid4().hex[:12]}"
        stage = "SMB login"
        try:
            smb = self._open_windows_smb_connection(host=host, port=port, credential=credential)
            stage = "ADMIN$ script staging"
            script_abs, output_abs, output_path = self._windows_smb_stage_script(smb, script)
            command = "cmd.exe"
            arguments = f'/Q /C powershell.exe -NoProfile -ExecutionPolicy Bypass -File "{script_abs}" > "{output_abs}" 2>&1'
            xml = self._windows_task_xml(task_name=task_path, command=command, arguments=arguments)
            stage = "Task Scheduler RPC bind"
            rpc_transport = transport.SMBTransport(host, int(port), r"\atsvc", smb_connection=smb)
            dce = rpc_transport.get_dce_rpc()
            dce.connect()
            dce.bind(tsch.MSRPC_UUID_TSCHS)
            stage = "task registration"
            tsch.hSchRpcRegisterTask(dce, task_path, xml, tsch.TASK_CREATE, NULL, tsch.TASK_LOGON_NONE)
            stage = "task start"
            tsch.hSchRpcRun(dce, task_path)
            stage = "task output polling"
            result = self._poll_windows_smb_onboarding_output(
                smb=smb,
                output_path=output_path,
                timeout_seconds=timeout_seconds,
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
        script: str,
        timeout_seconds: float,
    ) -> Dict[str, Any]:
        try:
            from impacket.dcerpc.v5.dcom import wmi  # type: ignore
            from impacket.dcerpc.v5.dcomrt import DCOMConnection  # type: ignore
            from impacket.dcerpc.v5.dtypes import NULL  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"impacket_wmi_unavailable:{exc}"}
        domain, username, password = _parse_windows_credential_parts(credential)
        smb = None
        dcom = None
        login = None
        stage = "SMB login"
        try:
            smb = self._open_windows_smb_connection(host=host, port=port, credential=credential)
            stage = "ADMIN$ script staging"
            script_abs, output_abs, output_path = self._windows_smb_stage_script(smb, script)
            command = f'cmd.exe /Q /C powershell.exe -NoProfile -ExecutionPolicy Bypass -File "{script_abs}" > "{output_abs}" 2>&1'
            stage = "DCOM connect"
            dcom = DCOMConnection(host, username, password, domain, oxidResolver=False)
            stage = "WMI login"
            interface = dcom.CoCreateInstanceEx(wmi.CLSID_WbemLevel1Login, wmi.IID_IWbemLevel1Login)
            login = wmi.IWbemLevel1Login(interface)
            services = login.NTLMLogin("//./root/cimv2", NULL, NULL)
            login.RemRelease()
            login = None
            stage = "WMI process creation"
            win32_process, _ = services.GetObject("Win32_Process")
            win32_process.Create(command, "C:\\Windows", None)
            stage = "WMI output polling"
            return self._poll_windows_smb_onboarding_output(
                smb=smb,
                output_path=output_path,
                timeout_seconds=timeout_seconds,
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

    def _execute_windows_winrm_onboarding(
        self,
        *,
        host: str,
        port: int,
        credential: Dict[str, Any],
        script: str,
        timeout_seconds: float,
    ) -> Dict[str, Any]:
        try:
            import winrm  # type: ignore
        except Exception as exc:
            return {"exit_code": 127, "stdout": "", "stderr": f"pywinrm_unavailable:{exc}"}
        metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
        transport_mode = str(metadata.get("winrm_transport") or "ntlm").strip().lower() or "ntlm"
        scheme = "https" if int(port) == 5986 or transport_mode in {"ssl", "credssp"} else "http"
        endpoint = f"{scheme}://{host}:{int(port)}/wsman"
        try:
            session = winrm.Session(
                endpoint,
                auth=(str(credential.get("username") or ""), str(credential.get("password") or "")),
                transport=transport_mode,
                server_cert_validation="ignore",
                operation_timeout_sec=max(20, min(600, int(timeout_seconds or 900))),
                read_timeout_sec=max(30, min(660, int(timeout_seconds or 900) + 30)),
            )
            result = session.run_ps(script)
            stdout_raw = getattr(result, "std_out", b"") or b""
            stderr_raw = getattr(result, "std_err", b"") or b""
            stdout = stdout_raw.decode("utf-8", errors="replace") if isinstance(stdout_raw, bytes) else str(stdout_raw or "")
            stderr = stderr_raw.decode("utf-8", errors="replace") if isinstance(stderr_raw, bytes) else str(stderr_raw or "")
            exit_code = int(getattr(result, "status_code", 1) or 0)
            marker_code = _windows_exit_code_from_output(stdout)
            if marker_code is not None:
                exit_code = marker_code
            return {"exit_code": exit_code, "stdout": stdout, "stderr": stderr}
        except Exception as exc:
            return {"exit_code": 1, "stdout": "", "stderr": str(exc)}

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
    ) -> str:
        row_id = int(row["id"])
        host = str(row.get("target_address") or row.get("target_hostname") or "").strip()
        smb_port = int(row.get("ssh_port") or DEFAULT_ONBOARDING_WINDOWS_PORT)
        redactions = [site.get("enrollment_code"), server_url, credential.get("password")]
        self._update_onboarding_target_row(
            row_id,
            status=ONBOARDING_STATUS_RUNNING,
            detail="Trying Windows remote enrollment.",
            redactions=redactions,
        )
        if self._onboarding_target_already_known(host, site.get("id")):
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_SKIPPED,
                detail="Target already appears enrolled for this site.",
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_SKIPPED

        script = self._windows_onboarding_script(
            branch=branch,
            server_url=server_url,
            enrollment_code=str(site.get("enrollment_code") or ""),
            job_id=job_id,
            run_id=run_id,
            target=host,
        )
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
        timeout_seconds = self._onboarding_install_timeout_seconds()

        for method in normalized_methods:
            label = _windows_method_label(method)
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
                    stderr_parts.append(f"[{label}] port {method_port} unreachable: {tcp_error}")
                continue
            attempted_methods += 1
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_RUNNING,
                detail=f"Trying Windows {label} enrollment.",
                stdout="\n\n".join(stdout_parts),
                stderr="\n\n".join(stderr_parts),
                redactions=redactions,
            )
            if method == ONBOARDING_WINDOWS_METHOD_SMB_SCM:
                result = self._execute_windows_smb_scm_onboarding(
                    host=host,
                    port=smb_port,
                    credential=credential,
                    script=script,
                    timeout_seconds=timeout_seconds,
                )
            elif method == ONBOARDING_WINDOWS_METHOD_SCHEDULED_TASK:
                result = self._execute_windows_scheduled_task_onboarding(
                    host=host,
                    port=smb_port,
                    credential=credential,
                    script=script,
                    timeout_seconds=timeout_seconds,
                )
            elif method == ONBOARDING_WINDOWS_METHOD_WMI_DCOM:
                result = self._execute_windows_wmi_dcom_onboarding(
                    host=host,
                    port=smb_port,
                    credential=credential,
                    script=script,
                    timeout_seconds=timeout_seconds,
                )
            else:
                result = self._execute_windows_winrm_onboarding(
                    host=host,
                    port=int(winrm_port),
                    credential=credential,
                    script=script,
                    timeout_seconds=timeout_seconds,
                )
            exit_code = int(result.get("exit_code") or 0)
            stdout = str(result.get("stdout") or "")
            stderr = str(result.get("stderr") or "")
            if stdout:
                stdout_parts.append(f"[{label}]\n{stdout}")
            if stderr:
                stderr_parts.append(f"[{label}]\n{stderr}")
            if exit_code == 0:
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
                    detail=f"Agent installed through Windows {label}. Device approval pending operator action.",
                    stdout="\n\n".join(stdout_parts),
                    stderr="\n\n".join(stderr_parts),
                    approval_reference=approval_reference,
                    finished=True,
                    redactions=redactions,
                )
                return ONBOARDING_STATUS_WAITING_APPROVAL
            failure_hint = _onboarding_failure_hint(stdout=stdout, stderr=stderr, redactions=redactions)
            stderr_parts.append(f"[{label}] failed with exit code {exit_code}.{(' ' + failure_hint) if failure_hint else ''}")

        combined_stdout = "\n\n".join(stdout_parts)
        combined_stderr = "\n\n".join(stderr_parts)
        if port_failures and attempted_methods == 0:
            self._update_onboarding_target_row(
                row_id,
                status=ONBOARDING_STATUS_UNREACHABLE,
                detail="Windows remote enrollment ports unreachable.",
                stdout=combined_stdout,
                stderr=combined_stderr,
                finished=True,
                redactions=redactions,
            )
            return ONBOARDING_STATUS_UNREACHABLE

        self._update_onboarding_target_row(
            row_id,
            status=ONBOARDING_STATUS_FAILED,
            detail=(
                "Windows automatic onboarding failed through SMB service, scheduled task, WMI/DCOM, and WinRM. "
                "Manual agent installation required; target security policy appears too locked down for remote enrollment."
            ),
            stdout=combined_stdout,
            stderr=combined_stderr,
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

        try:
            self.socketio.start_background_task(_runner)
        except Exception:
            threading.Thread(target=_runner, daemon=True).start()

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
            if credential_id is None:
                raise RuntimeError("Onboarding jobs require one stored credential.")
            credential = self._load_credential(credential_id)
            if not credential:
                raise RuntimeError("Selected stored credential could not be loaded.")
            platform = str(config.get("agent_platform") or ONBOARDING_PLATFORM_LINUX).strip().lower()
            connection_type = str(credential.get("connection_type") or "").strip().lower()
            if platform == ONBOARDING_PLATFORM_LINUX and connection_type != "ssh":
                raise RuntimeError("Linux onboarding requires an SSH credential.")
            if platform == ONBOARDING_PLATFORM_WINDOWS and connection_type not in {"windows", "winrm"}:
                raise RuntimeError("Windows onboarding requires a Windows or WinRM credential.")
            if not str(credential.get("username") or "").strip():
                raise RuntimeError("Selected stored credential has no username.")
            if platform == ONBOARDING_PLATFORM_WINDOWS and not str(credential.get("password") or "").strip():
                raise RuntimeError("Windows onboarding requires a credential password.")

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
                    for target in expanded_targets:
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
                                target.get("input") or target.get("host") or "",
                                target.get("host") or "",
                                target.get("host") or "",
                                int(target.get("port") or config.get("transport_port") or DEFAULT_ONBOARDING_SSH_PORT),
                                ONBOARDING_STATUS_PENDING,
                                "",
                                insert_now,
                                insert_now,
                            ),
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
                        credential=credential,
                        branch=str(config.get("install_branch") or "main"),
                        server_url=server_url,
                        job_id=job_id,
                        run_id=run_row_id,
                        platform=platform,
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

    def _resolved_targets_from_meta(self, resolution_meta: Optional[Dict[str, Any]]) -> List[Dict[str, Any]]:
        entries = []
        if isinstance(resolution_meta, dict):
            raw_entries = resolution_meta.get("resolved_targets")
            if isinstance(raw_entries, list):
                entries = raw_entries
        results: List[Dict[str, Any]] = []
        for item in entries or []:
            if not isinstance(item, dict):
                continue
            hostname = str(item.get("hostname") or "").strip()
            if not hostname:
                continue
            results.append(
                {
                    "hostname": hostname,
                    "device_guid": str(item.get("device_guid") or "").strip(),
                    "site_id": item.get("site_id"),
                    "site_name": str(item.get("site_name") or "").strip(),
                    "agent_id": str(item.get("agent_id") or "").strip(),
                    "connection_type": str(item.get("connection_type") or "").strip().lower(),
                    "connection_endpoint": str(item.get("connection_endpoint") or "").strip(),
                    "operating_system": str(item.get("operating_system") or "").strip(),
                    "resolved_from_filter_ids": [
                        int(value)
                        for value in (item.get("resolved_from_filter_ids") or [])
                        if str(value).strip().isdigit()
                    ],
                }
            )
        return results

    def _component_name_for_display(self, component: Dict[str, Any], *, fallback: str) -> str:
        if not isinstance(component, dict):
            return fallback
        for key in ("name", "component_name", "displayName", "script_name", "script_path", "path"):
            value = str(component.get(key) or "").strip()
            if value:
                return value
        return fallback

    def _should_use_shared_ansible_runs(
        self,
        *,
        run_mode: str,
        script_components: Sequence[Dict[str, Any]],
        ansible_components: Sequence[Dict[str, Any]],
    ) -> bool:
        return bool(ansible_components) and not bool(script_components) and str(run_mode or "").strip().lower() in {
            "local",
            "ssh",
            "winrm",
        }

    def _individual_ansible_runner_limits(self) -> Dict[str, int]:
        defaults = {
            "job_concurrency_limit": _DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT,
            "global_concurrency_limit": _DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT,
        }
        try:
            settings = load_ansible_runner_settings()
        except Exception:
            return defaults
        return {
            "job_concurrency_limit": max(
                1,
                int(settings.get("job_concurrency_limit") or defaults["job_concurrency_limit"]),
            ),
            "global_concurrency_limit": max(
                1,
                int(settings.get("global_concurrency_limit") or defaults["global_concurrency_limit"]),
            ),
        }

    def _running_ansible_run_counts(self) -> Tuple[int, Dict[int, int]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT job_id, COUNT(*)
                  FROM scheduled_job_runs
                 WHERE status=? AND LOWER(COALESCE(component_kind, ''))='ansible'
              GROUP BY job_id
                """,
                (RUN_STATUS_RUNNING,),
            )
            per_job: Dict[int, int] = {}
            total = 0
            for job_id, count_value in cur.fetchall():
                try:
                    normalized_job_id = int(job_id)
                    normalized_count = max(0, int(count_value or 0))
                except Exception:
                    continue
                per_job[normalized_job_id] = normalized_count
                total += normalized_count
            return total, per_job
        finally:
            conn.close()

    def _can_dispatch_ansible_run(
        self,
        *,
        job_id: int,
        global_running: int,
        running_by_job: Mapping[int, int],
        limits: Mapping[str, int],
    ) -> bool:
        job_limit = max(1, int(limits.get("job_concurrency_limit") or _DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT))
        global_limit = max(
            1,
            int(limits.get("global_concurrency_limit") or _DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT),
        )
        if int(global_running) >= global_limit:
            return False
        if int(running_by_job.get(int(job_id), 0) or 0) >= job_limit:
            return False
        return True

    def _load_run_targets(self, run_id: int) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    run_id,
                    device_guid,
                    hostname,
                    site_id,
                    resolved_from_filter_id,
                    inventory_hostname,
                    wireguard_peer_ip,
                    resolved_connection,
                    resolution_status,
                    resolution_reason,
                    resolved_from_filter_ids_json,
                    created_at
                  FROM scheduled_job_run_targets
                 WHERE run_id=?
              ORDER BY id ASC
                """,
                (int(run_id),),
            )
            rows = []
            for row in cur.fetchall():
                try:
                    filter_ids = json.loads(row[11] or "[]")
                except Exception:
                    filter_ids = []
                rows.append(
                    {
                        "id": int(row[0]),
                        "run_id": int(row[1]),
                        "device_guid": row[2] or "",
                        "hostname": row[3] or "",
                        "site_id": row[4],
                        "resolved_from_filter_id": row[5],
                        "inventory_hostname": row[6] or "",
                        "wireguard_peer_ip": row[7] or "",
                        "resolved_connection": row[8] or "",
                        "resolution_status": row[9] or "",
                        "resolution_reason": row[10] or "",
                        "resolved_from_filter_ids": filter_ids if isinstance(filter_ids, list) else [],
                        "created_at": row[12],
                    }
                )
            return rows
        finally:
            conn.close()

    def _lookup_devices_for_targets(self, run_targets: Sequence[Mapping[str, Any]]) -> Dict[str, Dict[str, Any]]:
        dataset = self._filter_matcher.fetch_devices()
        lookup: Dict[str, Dict[str, Any]] = {}
        for device in dataset:
            guid_value = str(device.get("device_guid") or device.get("guid") or "").strip().lower()
            hostname_value = str(device.get("hostname") or "").strip().lower()
            site_id = device.get("site_id")
            try:
                site_id = int(site_id) if site_id is not None else None
            except Exception:
                site_id = None
            if guid_value:
                lookup[f"guid:{guid_value}"] = device
            if hostname_value and site_id is not None:
                lookup[f"site:{site_id}:{hostname_value}"] = device
            if hostname_value and f"host:{hostname_value}" not in lookup:
                lookup[f"host:{hostname_value}"] = device
        return lookup

    def _record_shared_ansible_occurrence_snapshot(
        self,
        *,
        job_id: int,
        scheduled_ts: int,
        run_mode: str,
        ansible_components: Sequence[Dict[str, Any]],
        resolved_targets: Sequence[Dict[str, Any]],
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

            for component_index, component in enumerate(ansible_components):
                component_name = self._component_name_for_display(
                    dict(component or {}),
                    fallback=f"Ansible Playbook {component_index + 1}",
                )
                initial_status = RUN_STATUS_PENDING if resolved_targets else RUN_STATUS_SKIPPED
                initial_skip_reason = "" if resolved_targets else SKIP_REASON_NO_TARGETS
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
                        initial_status,
                        initial_skip_reason,
                        int(created_at),
                        int(created_at),
                        1,
                        int(component_index),
                        "ansible",
                        component_name,
                    ),
                )
                run_id = int(cur.lastrowid or 0)
                for target in resolved_targets:
                    filter_ids = []
                    for raw_filter_id in target.get("resolved_from_filter_ids") or []:
                        try:
                            filter_ids.append(int(raw_filter_id))
                        except Exception:
                            continue
                    site_id = target.get("site_id")
                    try:
                        site_id = int(site_id) if site_id is not None else None
                    except Exception:
                        site_id = None
                    inventory_hostname = _inventory_hostname_for_target(
                        target.get("hostname"),
                        site_name=target.get("site_name"),
                        site_id=site_id,
                        connection=run_mode,
                    )
                    cur.execute(
                        """
                        INSERT INTO scheduled_job_run_targets(
                            run_id,
                            device_guid,
                            hostname,
                            site_id,
                            resolved_from_filter_id,
                            inventory_hostname,
                            wireguard_peer_ip,
                            resolved_connection,
                            resolution_status,
                            resolution_reason,
                            resolved_from_filter_ids_json,
                            created_at
                        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
                        """,
                        (
                            run_id,
                            str(target.get("device_guid") or "").strip(),
                            str(target.get("hostname") or "").strip(),
                            site_id,
                            (filter_ids[0] if filter_ids else None),
                            inventory_hostname,
                            "",
                            str(run_mode or "").strip().lower(),
                            RESOLUTION_STATUS_PENDING if resolved_targets else RESOLUTION_STATUS_UNRESOLVED,
                            "",
                            json.dumps(filter_ids),
                            int(created_at),
                        ),
                    )
            conn.commit()
        finally:
            conn.close()

    def _record_individual_ansible_occurrence_snapshot(
        self,
        *,
        job_id: int,
        scheduled_ts: int,
        run_mode: str,
        ansible_components: Sequence[Dict[str, Any]],
        resolved_targets: Sequence[Dict[str, Any]],
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

            transport_mode = _normalize_ansible_transport(run_mode)
            if not resolved_targets:
                for component_index, component in enumerate(ansible_components):
                    component_name = self._component_name_for_display(
                        dict(component or {}),
                        fallback=f"Ansible Playbook {component_index + 1}",
                    )
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
                            RUN_STATUS_SKIPPED,
                            SKIP_REASON_NO_TARGETS,
                            int(created_at),
                            int(created_at),
                            0,
                            int(component_index),
                            "ansible",
                            component_name,
                        ),
                    )
                conn.commit()
                return

            for target in resolved_targets:
                hostname = str(target.get("hostname") or "").strip()
                if not hostname:
                    continue
                filter_ids: List[int] = []
                for raw_filter_id in target.get("resolved_from_filter_ids") or []:
                    try:
                        filter_ids.append(int(raw_filter_id))
                    except Exception:
                        continue
                site_id = target.get("site_id")
                try:
                    site_id = int(site_id) if site_id is not None else None
                except Exception:
                    site_id = None
                inventory_hostname = _inventory_hostname_for_target(
                    hostname,
                    site_name=target.get("site_name"),
                    site_id=site_id,
                    connection=transport_mode,
                )
                for component_index, component in enumerate(ansible_components):
                    component_name = self._component_name_for_display(
                        dict(component or {}),
                        fallback=f"Ansible Playbook {component_index + 1}",
                    )
                    cur.execute(
                        """
                        INSERT INTO scheduled_job_runs(
                            job_id,
                            target_hostname,
                            scheduled_ts,
                            status,
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
                            hostname,
                            int(scheduled_ts),
                            RUN_STATUS_PENDING,
                            int(created_at),
                            int(created_at),
                            0,
                            int(component_index),
                            "ansible",
                            component_name,
                        ),
                    )
                    run_id = int(cur.lastrowid or 0)
                    cur.execute(
                        """
                        INSERT INTO scheduled_job_run_targets(
                            run_id,
                            device_guid,
                            hostname,
                            site_id,
                            resolved_from_filter_id,
                            inventory_hostname,
                            wireguard_peer_ip,
                            resolved_connection,
                            resolution_status,
                            resolution_reason,
                            resolved_from_filter_ids_json,
                            created_at
                        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
                        """,
                        (
                            run_id,
                            str(target.get("device_guid") or "").strip(),
                            hostname,
                            site_id,
                            (filter_ids[0] if filter_ids else None),
                            inventory_hostname,
                            "",
                            transport_mode,
                            RESOLUTION_STATUS_PENDING,
                            "",
                            json.dumps(filter_ids),
                            int(created_at),
                        ),
                    )
            conn.commit()
        finally:
            conn.close()

    def _load_occurrence_target_rows(self, job_id: int, scheduled_ts: int) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    t.id,
                    t.run_id,
                    t.device_guid,
                    t.hostname,
                    t.site_id,
                    s.name,
                    t.inventory_hostname,
                    t.wireguard_peer_ip,
                    t.resolved_connection,
                    t.resolution_status,
                    t.resolution_reason,
                    t.resolved_from_filter_ids_json,
                    r.status,
                    r.started_ts,
                    r.finished_ts,
                    r.skip_reason,
                    r.shared_execution,
                    r.component_name
                  FROM scheduled_job_run_targets t
                  JOIN scheduled_job_runs r ON r.id = t.run_id
             LEFT JOIN sites s ON s.id = t.site_id
                 WHERE r.job_id=? AND r.scheduled_ts=?
              ORDER BY t.id ASC
                """,
                (int(job_id), int(scheduled_ts)),
            )
            rows: List[Dict[str, Any]] = []
            for row in cur.fetchall():
                try:
                    resolved_filter_ids = json.loads(row[11] or "[]")
                except Exception:
                    resolved_filter_ids = []
                rows.append(
                    {
                        "id": int(row[0]),
                        "run_id": int(row[1]),
                        "device_guid": row[2] or "",
                        "hostname": row[3] or "",
                        "site_id": row[4],
                        "site_name": row[5] or "",
                        "inventory_hostname": row[6] or "",
                        "wireguard_peer_ip": row[7] or "",
                        "resolved_connection": row[8] or "",
                        "resolution_status": row[9] or "",
                        "resolution_reason": row[10] or "",
                        "resolved_from_filter_ids": resolved_filter_ids if isinstance(resolved_filter_ids, list) else [],
                        "run_status": row[12] or "",
                        "started_ts": row[13],
                        "finished_ts": row[14],
                        "skip_reason": row[15] or "",
                        "shared_execution": bool(row[16] or 0),
                        "component_name": row[17] or "",
                    }
                )
            return rows
        finally:
            conn.close()

    def _aggregate_occurrence_targets(
        self,
        occurrence_runs: Sequence[Mapping[str, Any]],
        occurrence_target_rows: Sequence[Mapping[str, Any]],
    ) -> Tuple[Dict[str, Dict[str, Any]], Dict[str, int], bool]:
        if occurrence_target_rows:
            grouped: Dict[str, Dict[str, Any]] = {}
            for row in occurrence_target_rows:
                hostname = str(row.get("hostname") or "").strip()
                if not hostname:
                    continue
                device_guid = str(row.get("device_guid") or "").strip().lower()
                site_id = row.get("site_id")
                try:
                    site_id = int(site_id) if site_id is not None else None
                except Exception:
                    site_id = None
                if device_guid:
                    group_key = f"guid:{device_guid}"
                elif site_id is not None:
                    group_key = f"site:{site_id}:{hostname.lower()}"
                else:
                    group_key = f"host:{hostname.lower()}"
                group = grouped.setdefault(
                    group_key,
                    {
                        "hostname": hostname,
                        "site_id": site_id,
                        "site_name": str(row.get("site_name") or "").strip(),
                        "inventory_hostname": str(row.get("inventory_hostname") or "").strip(),
                        "wireguard_peer_ip": str(row.get("wireguard_peer_ip") or "").strip(),
                        "resolved_connection": str(row.get("resolved_connection") or "").strip(),
                        "resolution_status": "",
                        "resolution_reason": "",
                        "eligible_runs": [],
                        "run_ids": set(),
                        "started_ts": [],
                        "finished_ts": [],
                    },
                )
                group["run_ids"].add(int(row.get("run_id") or 0))
                if row.get("started_ts") is not None:
                    group["started_ts"].append(int(row.get("started_ts") or 0))
                if row.get("finished_ts") is not None:
                    group["finished_ts"].append(int(row.get("finished_ts") or 0))
                if not group.get("site_name") and row.get("site_name"):
                    group["site_name"] = str(row.get("site_name") or "").strip()
                if not group.get("inventory_hostname") and row.get("inventory_hostname"):
                    group["inventory_hostname"] = str(row.get("inventory_hostname") or "").strip()
                if not group.get("wireguard_peer_ip") and row.get("wireguard_peer_ip"):
                    group["wireguard_peer_ip"] = str(row.get("wireguard_peer_ip") or "").strip()
                if not group.get("resolved_connection") and row.get("resolved_connection"):
                    group["resolved_connection"] = str(row.get("resolved_connection") or "").strip()

                resolution_status = str(row.get("resolution_status") or "").strip().lower()
                if resolution_status not in {"", RESOLUTION_STATUS_PENDING, RESOLUTION_STATUS_ELIGIBLE}:
                    if not group.get("resolution_status"):
                        group["resolution_status"] = resolution_status
                        group["resolution_reason"] = str(row.get("resolution_reason") or "").strip()
                    continue
                group["eligible_runs"].append(
                    {
                        "id": int(row.get("run_id") or 0),
                        "status": str(row.get("run_status") or "").strip(),
                        "started_ts": row.get("started_ts"),
                        "finished_ts": row.get("finished_ts"),
                    }
                )
                if not group.get("resolution_status"):
                    group["resolution_status"] = RESOLUTION_STATUS_ELIGIBLE
                    group["resolution_reason"] = ""

            aggregated: Dict[str, Dict[str, Any]] = {}
            counts = {
                "pending": 0,
                "running": 0,
                "success": 0,
                "warning": 0,
                "failed": 0,
                "expired": 0,
                "timed_out": 0,
                "skipped": 0,
                "total_targets": len(grouped),
            }
            for group_key, group in grouped.items():
                if group["eligible_runs"]:
                    status = max(group["eligible_runs"], key=_host_run_priority).get("status") or RUN_STATUS_PENDING
                elif group.get("resolution_status") in {RESOLUTION_STATUS_SKIPPED, RESOLUTION_STATUS_UNRESOLVED}:
                    status = RUN_STATUS_SKIPPED
                else:
                    status = RUN_STATUS_PENDING
                aggregated[group_key] = {
                    "target_hostname": group["hostname"],
                    "hostname": group["hostname"],
                    "site_id": group["site_id"],
                    "site_name": group["site_name"],
                    "inventory_hostname": group["inventory_hostname"],
                    "wireguard_peer_ip": group["wireguard_peer_ip"],
                    "resolved_connection": group["resolved_connection"],
                    "resolution_status": group.get("resolution_status") or "",
                    "resolution_reason": group.get("resolution_reason") or "",
                    "status": status,
                    "started_ts": max(group["started_ts"]) if group["started_ts"] else None,
                    "finished_ts": max(group["finished_ts"]) if group["finished_ts"] else None,
                    "run_ids": sorted([rid for rid in group["run_ids"] if rid]),
                }
                bucket = _status_bucket_for_run(status)
                if bucket and bucket in counts:
                    counts[bucket] += 1
            has_no_targets_skip = False
            if not aggregated:
                has_no_targets_skip = any(
                    str(run.get("status") or "").strip() == RUN_STATUS_SKIPPED
                    and str(run.get("skip_reason") or "").strip().lower() == SKIP_REASON_NO_TARGETS
                    for run in occurrence_runs
                )
            return aggregated, counts, has_no_targets_skip

        return _aggregate_occurrence_runs(occurrence_runs)

    # ---------- Scheduling core ----------
    def _get_last_run(self, job_id: int) -> Optional[Dict[str, Any]]:
        conn = self._conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, scheduled_ts, started_ts, finished_ts, status FROM scheduled_job_runs WHERE job_id=? ORDER BY COALESCE(started_ts, scheduled_ts, 0) DESC, id DESC LIMIT 1",
            (job_id,)
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return None
        return {
            "id": row[0],
            "scheduled_ts": row[1],
            "started_ts": row[2],
            "finished_ts": row[3],
            "status": row[4] or "",
        }

    def _is_terminal_run_status(self, status: Any) -> bool:
        return str(status or "").strip() in TERMINAL_RUN_STATUSES

    def _get_latest_occurrence_ts(self, job_id: int) -> Optional[int]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
            row = cur.fetchone()
            return int(row[0]) if row and row[0] is not None else None
        finally:
            conn.close()

    def _load_occurrence_runs(self, job_id: int, scheduled_ts: int) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    target_hostname,
                    scheduled_ts,
                    started_ts,
                    finished_ts,
                    status,
                    error,
                    skip_reason,
                    shared_execution,
                    component_index,
                    component_kind,
                    component_name,
                    workflow_run_id
                  FROM scheduled_job_runs
                 WHERE job_id=? AND scheduled_ts=?
              ORDER BY id ASC
                """,
                (job_id, int(scheduled_ts)),
            )
            return [
                {
                    "id": int(row[0]),
                    "target_hostname": row[1] or "",
                    "scheduled_ts": row[2],
                    "started_ts": row[3],
                    "finished_ts": row[4],
                    "status": row[5] or "",
                    "error": row[6] or "",
                    "skip_reason": row[7] or "",
                    "shared_execution": bool(row[8] or 0),
                    "component_index": row[9],
                    "component_kind": row[10] or "",
                    "component_name": row[11] or "",
                    "workflow_run_id": row[12],
                }
                for row in cur.fetchall()
            ]
        finally:
            conn.close()

    def _resolve_occurrence_for_tick(
        self,
        *,
        job_id: int,
        schedule_type: str,
        start_ts: Optional[int],
        created_at: Optional[int],
        now_min: int,
    ) -> Optional[int]:
        latest_occ = self._get_latest_occurrence_ts(job_id)
        if latest_occ is None:
            if (schedule_type or "").lower() == "immediately":
                occ = _floor_minute(created_at or now_min)
            else:
                st_min = _floor_minute(start_ts) if start_ts else None
                occ = st_min if st_min is not None else self._compute_next_run(schedule_type, start_ts, None, now_min)
            if occ is None or now_min < occ:
                return None
            return occ

        latest_runs = self._load_occurrence_runs(job_id, int(latest_occ))
        if any(not self._is_terminal_run_status(run.get("status")) for run in latest_runs):
            return int(latest_occ)

        next_occ = self._compute_next_run(schedule_type, start_ts, int(latest_occ), now_min)
        if next_occ is None or now_min < next_occ:
            return None
        return int(next_occ)

    def _record_occurrence_snapshot(
        self,
        *,
        job_id: int,
        scheduled_ts: int,
        targets: Sequence[str],
        resolution_meta: Optional[Dict[str, Any]],
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

            if not targets:
                cur.execute(
                    """
                    INSERT INTO scheduled_job_runs(
                        job_id,
                        scheduled_ts,
                        status,
                        skip_reason,
                        created_at,
                        updated_at
                    ) VALUES (?,?,?,?,?,?)
                    """,
                    (
                        int(job_id),
                        int(scheduled_ts),
                        RUN_STATUS_SKIPPED,
                        SKIP_REASON_NO_TARGETS,
                        int(created_at),
                        int(created_at),
                    ),
                )
                conn.commit()
                return

            host_details = (resolution_meta or {}).get("resolved_host_details") or {}
            for hostname in targets:
                cur.execute(
                    """
                    INSERT INTO scheduled_job_runs(
                        job_id,
                        target_hostname,
                        scheduled_ts,
                        status,
                        created_at,
                        updated_at
                    ) VALUES (?,?,?,?,?,?)
                    """,
                    (
                        int(job_id),
                        str(hostname),
                        int(scheduled_ts),
                        RUN_STATUS_PENDING,
                        int(created_at),
                        int(created_at),
                    ),
                )
                run_id = int(cur.lastrowid or 0)
                details = host_details.get(str(hostname).lower()) or {}
                device_guid = str(details.get("device_guid") or "").strip()
                site_id = details.get("site_id")
                try:
                    site_id = int(site_id) if site_id is not None else None
                except Exception:
                    site_id = None
                resolved_filter_ids = []
                for raw_filter_id in details.get("resolved_from_filter_ids") or []:
                    try:
                        resolved_filter_ids.append(int(raw_filter_id))
                    except Exception:
                        continue
                if not resolved_filter_ids:
                    cur.execute(
                        """
                        INSERT INTO scheduled_job_run_targets(
                            run_id,
                            device_guid,
                            hostname,
                            site_id,
                            resolved_from_filter_id,
                            created_at
                        ) VALUES (?,?,?,?,?,?)
                        """,
                        (
                            run_id,
                            device_guid,
                            str(hostname),
                            site_id,
                            None,
                            int(created_at),
                        ),
                    )
                    continue
                for filter_id in sorted(set(resolved_filter_ids)):
                    cur.execute(
                        """
                        INSERT INTO scheduled_job_run_targets(
                            run_id,
                            device_guid,
                            hostname,
                            site_id,
                            resolved_from_filter_id,
                            created_at
                        ) VALUES (?,?,?,?,?,?)
                        """,
                        (
                            run_id,
                            device_guid,
                            str(hostname),
                            site_id,
                            int(filter_id),
                            int(created_at),
                        ),
                    )
            conn.commit()
        finally:
            conn.close()

    def _record_workflow_occurrence_snapshot(
        self,
        *,
        job_id: int,
        scheduled_ts: int,
        workflow_component: Mapping[str, Any],
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
            component_name = self._component_name_for_display(
                dict(workflow_component or {}),
                fallback="Workflow",
            )
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
                    component_name,
                    workflow_run_id
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    int(job_id),
                    int(scheduled_ts),
                    RUN_STATUS_PENDING,
                    "",
                    int(created_at),
                    int(created_at),
                    0,
                    0,
                    "workflow",
                    component_name,
                    None,
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def _dispatch_workflow_run(
        self,
        *,
        job_id: int,
        run_row_id: int,
        scheduled_ts: int,
        workflow_component: Mapping[str, Any],
    ) -> Optional[Dict[str, Any]]:
        workflow_guid = str(
            workflow_component.get("assembly_guid")
            or workflow_component.get("assemblyGuid")
            or workflow_component.get("workflow_guid")
            or workflow_component.get("workflowGuid")
            or ""
        ).strip().lower()
        ts_now = _now_ts()
        if not workflow_guid:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        "Workflow component is missing an assembly GUID.",
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        if not callable(self._workflow_run_launcher):
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        "Workflow runtime launcher is unavailable.",
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       started_ts=COALESCE(started_ts, ?),
                       finished_ts=NULL,
                       error='',
                       updated_at=?
                 WHERE id=?
                """,
                (RUN_STATUS_RUNNING, ts_now, ts_now, int(run_row_id)),
            )
            conn.commit()
        finally:
            conn.close()

        source_metadata = {
            "scheduled_job_id": int(job_id),
            "scheduled_job_run_id": int(run_row_id),
            "scheduled_ts": int(scheduled_ts),
            "component_name": self._component_name_for_display(dict(workflow_component or {}), fallback="Workflow"),
        }
        try:
            launch_result = self._workflow_run_launcher(
                workflow_guid=workflow_guid,
                source_type="scheduled_job",
                source_metadata=source_metadata,
                created_by="scheduler",
                execute_async=True,
            )
            run_payload = launch_result.get("run") if isinstance(launch_result, Mapping) else None
            workflow_run_id = None
            if isinstance(run_payload, Mapping):
                try:
                    workflow_run_id = int(run_payload.get("id"))
                except Exception:
                    workflow_run_id = None
            if workflow_run_id is not None:
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        "UPDATE scheduled_job_runs SET workflow_run_id=?, updated_at=? WHERE id=?",
                        (workflow_run_id, _now_ts(), int(run_row_id)),
                    )
                    conn.commit()
                finally:
                    conn.close()
            return dict(launch_result) if isinstance(launch_result, Mapping) else None
        except Exception as exc:
            failure_text = str(exc)[:512]
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
                    (RUN_STATUS_FAILED, ts_now, ts_now, failure_text, int(run_row_id)),
                )
                conn.commit()
            finally:
                conn.close()
            self._log_event(
                "workflow dispatch failed",
                job_id=int(job_id),
                run_id=int(run_row_id),
                level="ERROR",
                extra={"workflow_guid": workflow_guid, "scheduled_ts": int(scheduled_ts), "error": failure_text},
            )
            return None

    def _dispatch_shared_ansible(
        self,
        *,
        job_id: int,
        run_row_id: int,
        scheduled_ts: int,
        run_mode: str,
        component: Dict[str, Any],
        credential_id: Optional[int],
        use_service_account: bool,
    ) -> Optional[Dict[str, Any]]:
        run_mode_norm = str(run_mode or "local").strip().lower() or "local"
        ts_now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       started_ts=COALESCE(started_ts, ?),
                       finished_ts=NULL,
                       error='',
                       updated_at=?
                 WHERE id=?
                """,
                (RUN_STATUS_RUNNING, ts_now, ts_now, int(run_row_id)),
            )
            conn.commit()
        finally:
            conn.close()

        run_targets = self._load_run_targets(run_row_id)
        if not run_targets:
            conn = self._conn()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=?,
                           updated_at=?,
                           skip_reason=?,
                           error=?
                     WHERE id=?
                    """,
                    (
                        RUN_STATUS_SKIPPED,
                        ts_now,
                        ts_now,
                        SKIP_REASON_NO_TARGETS,
                        "No devices were targeted for this Ansible run.",
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        remote_requires_cred = (run_mode_norm == "ssh") or (run_mode_norm == "winrm" and not use_service_account)
        if remote_requires_cred and not credential_id:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        "Credential required for remote execution",
                        int(run_row_id),
                    ),
                )
                cur.execute(
                    """
                    UPDATE scheduled_job_run_targets
                       SET resolution_status=?,
                           resolution_reason=?,
                           resolved_connection=?
                     WHERE run_id=?
                    """,
                    (
                        RESOLUTION_STATUS_UNRESOLVED,
                        "credential_missing",
                        run_mode_norm,
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        try:
            credential = self._load_credential(credential_id) if credential_id is not None else None
        except AegisLockedError:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        "Aegis Cipher has not been entered; credential-backed execution is disabled.",
                        int(run_row_id),
                    ),
                )
                cur.execute(
                    """
                    UPDATE scheduled_job_run_targets
                       SET resolution_status=?,
                           resolution_reason=?,
                           resolved_connection=?
                     WHERE run_id=?
                    """,
                    (
                        RESOLUTION_STATUS_UNRESOLVED,
                        "credential_locked",
                        run_mode_norm,
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None
        except AegisSecretResetRequiredError:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        CREDENTIAL_RESET_REQUIRED_MESSAGE,
                        int(run_row_id),
                    ),
                )
                cur.execute(
                    """
                    UPDATE scheduled_job_run_targets
                       SET resolution_status=?,
                           resolution_reason=?,
                           resolved_connection=?
                     WHERE run_id=?
                    """,
                    (
                        RESOLUTION_STATUS_UNRESOLVED,
                        "credential_reset_required",
                        run_mode_norm,
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None
        except AegisDataCorruptionError as exc:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        str(exc)[:512],
                        int(run_row_id),
                    ),
                )
                cur.execute(
                    """
                    UPDATE scheduled_job_run_targets
                       SET resolution_status=?,
                           resolution_reason=?,
                           resolved_connection=?
                     WHERE run_id=?
                    """,
                    (
                        RESOLUTION_STATUS_UNRESOLVED,
                        "credential_corrupt",
                        run_mode_norm,
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None
        if remote_requires_cred and not credential:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        "Selected credential is unavailable.",
                        int(run_row_id),
                    ),
                )
                cur.execute(
                    """
                    UPDATE scheduled_job_run_targets
                       SET resolution_status=?,
                           resolution_reason=?,
                           resolved_connection=?
                     WHERE run_id=?
                    """,
                    (
                        RESOLUTION_STATUS_UNRESOLVED,
                        "credential_unavailable",
                        run_mode_norm,
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        if credential and str(credential.get("connection_type") or "").strip().lower() not in {"", run_mode_norm}:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        "Selected credential does not match the execution context.",
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        try:
            device_lookup = self._lookup_devices_for_targets(run_targets)
        except Exception as exc:
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
                    (
                        RUN_STATUS_FAILED,
                        ts_now,
                        ts_now,
                        f"Unable to load device state: {exc}",
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        normalized_private_key = ""
        private_key_passphrase = ""
        credential_password = ""
        if credential:
            credential_password = str(credential.get("password") or "").strip()
        if run_mode_norm == "ssh" and credential:
            normalized_private_key = _normalize_ssh_private_key_text(credential.get("private_key") or "")
            private_key_passphrase = str(credential.get("private_key_passphrase") or "").strip()
            if normalized_private_key and private_key_passphrase:
                if credential_password:
                    self._log_event(
                        "ssh credential includes passphrase-protected private key; falling back to password auth",
                        job_id=int(job_id),
                        run_id=int(run_row_id),
                        level="WARNING",
                        extra={
                            "credential_id": int(credential_id or 0),
                            "run_mode": run_mode_norm,
                        },
                    )
                    normalized_private_key = ""
                else:
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
                            (
                                RUN_STATUS_FAILED,
                                ts_now,
                                ts_now,
                                "Passphrase-protected SSH private keys are not yet supported for Engine Ansible runs. "
                                "Use an unencrypted test key or add a password to the selected credential.",
                                int(run_row_id),
                            ),
                        )
                        cur.execute(
                            """
                            UPDATE scheduled_job_run_targets
                               SET resolution_status=?,
                                   resolution_reason=?,
                                   resolved_connection=?
                             WHERE run_id=?
                            """,
                            (
                                RESOLUTION_STATUS_UNRESOLVED,
                                "credential_private_key_passphrase_unsupported",
                                run_mode_norm,
                                int(run_row_id),
                            ),
                        )
                        conn.commit()
                    finally:
                        conn.close()
                    return None

        vpn_sessions: Dict[str, Dict[str, Any]] = {}
        runtime_files: List[Dict[str, Any]] = []
        private_key_path = ""
        if run_mode_norm == "ssh" and normalized_private_key:
            private_key_path = "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
            runtime_files.append(
                {
                    "relative_path": "auth/id_borealis_ssh",
                    "content": normalized_private_key,
                    "mode": 0o600,
                }
            )

        if run_mode_norm in {"ssh", "winrm"}:
            candidate_agent_ids: List[str] = []
            required_vpn_ports: List[int] = []
            for target in run_targets:
                device = device_lookup.get(self._device_lookup_key_for_target(target)) or {}
                agent_id = str(device.get("agent_id") or target.get("agent_id") or "").strip()
                if agent_id:
                    candidate_agent_ids.append(agent_id)
                connection_endpoint = str(device.get("connection_endpoint") or target.get("connection_endpoint") or "").strip()
                default_port = 22 if run_mode_norm == "ssh" else 5985
                required_vpn_ports.append(_extract_endpoint_port(connection_endpoint) or default_port)
            vpn_sessions = self._prepare_vpn_sessions(candidate_agent_ids, required_ports=required_vpn_ports)

        target_specifications: List[Dict[str, Any]] = []
        target_updates: List[Tuple[str, str, str, str, str, int]] = []
        skipped_targets_for_log: List[Dict[str, Any]] = []
        preflight_failed_targets_for_log: List[Dict[str, Any]] = []
        local_allowed = {"localhost", "127.0.0.1", "::1", ENGINE_LOCAL_ALIAS}
        for target in run_targets:
            hostname = str(target.get("hostname") or "").strip()
            inventory_hostname = str(target.get("inventory_hostname") or "").strip()
            site_id = target.get("site_id")
            device_key = self._device_lookup_key_for_target(target)
            device = device_lookup.get(device_key) or {}
            site_name = str(device.get("site_name") or target.get("site_name") or "").strip()
            agent_id = str(device.get("agent_id") or target.get("agent_id") or "").strip()
            operating_system = str(device.get("operating_system") or target.get("operating_system") or "").strip()
            device_connection_type = str(device.get("connection_type") or target.get("connection_type") or "").strip().lower()
            connection_endpoint = str(device.get("connection_endpoint") or target.get("connection_endpoint") or "").strip()
            if not inventory_hostname:
                inventory_hostname = _inventory_hostname_for_target(
                    hostname,
                    site_name=site_name,
                    site_id=site_id,
                    connection=run_mode_norm,
                )
            resolution_status = RESOLUTION_STATUS_ELIGIBLE
            resolution_reason = ""
            wireguard_peer_ip = ""
            preflight_error = ""
            host_vars: Dict[str, Any] = {}

            if run_mode_norm == "local":
                if hostname.lower() not in local_allowed:
                    resolution_status = RESOLUTION_STATUS_SKIPPED
                    resolution_reason = "local_target_not_engine"
                else:
                    host_vars = {
                        "ansible_connection": "local",
                    }
            elif run_mode_norm == "ssh":
                session = vpn_sessions.get(agent_id) or {}
                wireguard_peer_ip = str(session.get("virtual_ip") or "").split("/", 1)[0]
                if session and not _wireguard_session_allows_remote_attempt(session, wireguard_peer_ip):
                    resolution_status = RESOLUTION_STATUS_SKIPPED
                    resolution_reason = RESOLUTION_REASON_WIREGUARD_NOT_READY
                    preflight_error = str(session.get("dispatch_ready_reason") or "not_ready")
                elif not wireguard_peer_ip:
                    resolution_status = RESOLUTION_STATUS_SKIPPED
                    resolution_reason = "wireguard_unavailable"
                else:
                    ssh_port = _extract_endpoint_port(connection_endpoint) or 22
                    transfer_method = _shared_ansible_ssh_transfer_method()
                    host_vars = {
                        "ansible_host": wireguard_peer_ip,
                        "ansible_connection": "ssh",
                        "ansible_ssh_retries": _env_positive_int(
                            _SHARED_ANSIBLE_SSH_RETRIES_ENV,
                            _DEFAULT_SHARED_ANSIBLE_SSH_RETRIES,
                        ),
                        "ansible_ssh_timeout": _env_positive_int(
                            _SHARED_ANSIBLE_SSH_TIMEOUT_ENV,
                            _DEFAULT_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS,
                        ),
                        "ansible_ssh_transfer_method": transfer_method,
                    }
                    if transfer_method in {"scp", "smart"}:
                        scp_extra_args = _shared_ansible_scp_extra_args()
                        if scp_extra_args:
                            host_vars["ansible_scp_extra_args"] = scp_extra_args
                    if ssh_port != 22:
                        host_vars["ansible_port"] = ssh_port
                    ssh_auth_mode = "combined"
                    if credential and private_key_path:
                        username = str(credential.get("username") or "").strip()
                        password = str(credential.get("password") or "").strip()
                        if password:
                            ssh_auth_mode = self._resolve_mixed_ssh_auth_mode(
                                host=wireguard_peer_ip,
                                port=ssh_port,
                                username=username,
                                password=password,
                                private_key_text=normalized_private_key,
                            )
                    apply_ssh_credential_host_vars(
                        host_vars,
                        credential,
                        private_key_path=private_key_path,
                        include_password=ssh_auth_mode != "key",
                        include_private_key=ssh_auth_mode != "password",
                    )
            elif run_mode_norm == "winrm":
                session = vpn_sessions.get(agent_id) or {}
                wireguard_peer_ip = str(session.get("virtual_ip") or "").split("/", 1)[0]
                if session and not _wireguard_session_allows_remote_attempt(session, wireguard_peer_ip):
                    resolution_status = RESOLUTION_STATUS_SKIPPED
                    resolution_reason = RESOLUTION_REASON_WIREGUARD_NOT_READY
                    preflight_error = str(session.get("dispatch_ready_reason") or "not_ready")
                elif not wireguard_peer_ip:
                    resolution_status = RESOLUTION_STATUS_SKIPPED
                    resolution_reason = "wireguard_unavailable"
                else:
                    username = ""
                    password = ""
                    transport = "ntlm"
                    if use_service_account:
                        service_account = self._load_service_account(agent_id)
                        username = str((service_account or {}).get("username") or "").strip()
                        password = str((service_account or {}).get("password") or "").strip()
                        if not username or not password:
                            resolution_status = RESOLUTION_STATUS_SKIPPED
                            resolution_reason = "service_account_unavailable"
                    elif credential:
                        username = str(credential.get("username") or "").strip()
                        password = str(credential.get("password") or "").strip()
                        metadata = credential.get("metadata") if isinstance(credential.get("metadata"), dict) else {}
                        transport = str(metadata.get("winrm_transport") or "ntlm").strip().lower() or "ntlm"
                        if not username or not password:
                            resolution_status = RESOLUTION_STATUS_SKIPPED
                            resolution_reason = "credential_incomplete"
                    if resolution_status == RESOLUTION_STATUS_ELIGIBLE:
                        winrm_port = _extract_endpoint_port(connection_endpoint) or 5985
                        preflight_kwargs = {}
                        if bool(session.get("_requested_start")):
                            preflight_kwargs["attempts"] = _env_positive_int(
                                _PREPARED_REMOTE_PREFLIGHT_ATTEMPTS_ENV,
                                _DEFAULT_PREPARED_REMOTE_PREFLIGHT_ATTEMPTS,
                            )
                            preflight_kwargs["retry_delay_seconds"] = _env_non_negative_float(
                                _PREPARED_REMOTE_PREFLIGHT_RETRY_DELAY_ENV,
                                _DEFAULT_PREPARED_REMOTE_PREFLIGHT_RETRY_DELAY_SECONDS,
                            )
                        preflight_error = self._preflight_remote_port(
                            host=wireguard_peer_ip,
                            port=winrm_port,
                            **preflight_kwargs,
                        )
                        host_vars = {
                            "ansible_host": wireguard_peer_ip,
                            "ansible_connection": "winrm",
                            "ansible_user": username,
                            "ansible_password": password,
                            "ansible_winrm_transport": transport,
                            "ansible_winrm_server_cert_validation": "ignore",
                        }
                        if winrm_port:
                            host_vars["ansible_port"] = winrm_port
                        if preflight_error:
                            resolution_status = RESOLUTION_STATUS_SKIPPED
                            resolution_reason = RESOLUTION_REASON_REMOTE_PREFLIGHT_FAILED
                            preflight_failed_targets_for_log.append(
                                {
                                    "hostname": hostname,
                                    "inventory_hostname": inventory_hostname,
                                    "site_name": site_name,
                                    "agent_id": agent_id,
                                    "wireguard_peer_ip": wireguard_peer_ip,
                                    "connection_endpoint": connection_endpoint,
                                    "preflight_error": preflight_error,
                                }
                            )

            target_updates.append(
                (
                    inventory_hostname,
                    wireguard_peer_ip,
                    run_mode_norm,
                    resolution_status,
                    resolution_reason,
                    int(target["id"]),
                )
            )
            if resolution_status != RESOLUTION_STATUS_ELIGIBLE:
                skipped_targets_for_log.append(
                    {
                        "hostname": hostname,
                        "inventory_hostname": inventory_hostname,
                        "site_name": site_name,
                        "agent_id": agent_id,
                        "device_connection_type": device_connection_type,
                        "operating_system": operating_system,
                        "wireguard_peer_ip": wireguard_peer_ip,
                        "reason": resolution_reason or "unknown",
                        "connection_endpoint": connection_endpoint,
                        "preflight_error": preflight_error,
                    }
                )
                continue
            target_specifications.append(
                {
                    "hostname": hostname,
                    "inventory_hostname": inventory_hostname,
                    "site_group": _site_group_name(site_name, site_id),
                    "host_vars": host_vars,
                }
            )

        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.executemany(
                """
                UPDATE scheduled_job_run_targets
                   SET inventory_hostname=?,
                       wireguard_peer_ip=?,
                       resolved_connection=?,
                       resolution_status=?,
                       resolution_reason=?
                 WHERE id=?
                """,
                target_updates,
            )
            conn.commit()
        finally:
            conn.close()

        if preflight_failed_targets_for_log:
            self._log_event(
                "shared ansible run skipped targets that failed remote preflight",
                job_id=int(job_id),
                run_id=int(run_row_id),
                level="WARNING",
                extra={
                    "run_mode": run_mode_norm,
                    "scheduled_ts": int(scheduled_ts or 0),
                    "component_name": self._component_name_for_display(
                        dict(component or {}),
                        fallback=f"Ansible Playbook {int(run_row_id)}",
                    ),
                    "warning_count": len(preflight_failed_targets_for_log),
                    "warning_details": json.dumps(preflight_failed_targets_for_log, sort_keys=True),
                },
            )

        if not target_specifications:
            self._log_event(
                "shared ansible run resolved no eligible targets",
                job_id=int(job_id),
                run_id=int(run_row_id),
                level="WARNING",
                extra={
                    "run_mode": run_mode_norm,
                    "scheduled_ts": int(scheduled_ts or 0),
                    "component_name": self._component_name_for_display(
                        dict(component or {}),
                        fallback=f"Ansible Playbook {int(run_row_id)}",
                    ),
                    "target_count": len(run_targets),
                    "resolution_details": json.dumps(skipped_targets_for_log, sort_keys=True),
                },
            )
            conn = self._conn()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=?,
                           updated_at=?,
                           skip_reason=?,
                           error=?
                     WHERE id=?
                    """,
                    (
                        RUN_STATUS_SKIPPED,
                        ts_now,
                        ts_now,
                        SKIP_REASON_NO_ELIGIBLE_TARGETS,
                        "No eligible devices were available for this Ansible run.",
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            return None

        try:
            rel_path = ""
            overrides_map: Dict[str, Any] = {}
            assembly_guid_hint = ""
            if isinstance(component, dict):
                assembly_guid_hint = str(component.get("assembly_guid") or component.get("assemblyGuid") or "").strip().lower()
                rel_path = component.get("path") or component.get("playbook_path") or component.get("script_path") or ""
                raw_overrides = component.get("variable_values")
                if isinstance(raw_overrides, dict):
                    for key, val in raw_overrides.items():
                        name = str(key or "").strip()
                        if not name:
                            continue
                        overrides_map[name] = val
                comp_vars = component.get("variables")
                if isinstance(comp_vars, list):
                    for var in comp_vars:
                        if not isinstance(var, dict):
                            continue
                        name = str(var.get("name") or "").strip()
                        if not name or name in overrides_map:
                            continue
                        if "value" in var:
                            overrides_map[name] = var.get("value")
            else:
                rel_path = str(component or "")
            rel_norm = (rel_path or "").replace("\\", "/").strip().lstrip("/")
            if rel_norm and not rel_norm.lower().startswith("ansible_playbooks/"):
                rel_norm = f"Ansible_Playbooks/{rel_norm}"
            doc, record = self._resolve_runtime_document(rel_norm, "ansible", assembly_guid=assembly_guid_hint)
            if not doc:
                return None
            resolved_virtual_path = str(record.get("virtual_path") or "").strip() if isinstance(record, dict) else ""
            if not rel_norm:
                rel_norm = resolved_virtual_path
            friendly_name = (doc.get("name") or "").strip() or self._component_name_for_display(component, fallback=f"Job-{job_id}")
            if not friendly_name:
                friendly_name = f"Job-{job_id}"
            normalized_script = (doc.get("script") or "").replace("\r\n", "\n")
            doc_variables = doc.get("variables") if isinstance(doc.get("variables"), list) else []
            files = doc.get("files") or []
            if run_mode_norm == "winrm" and credential and not use_service_account:
                overrides_map = _inject_winrm_credential(overrides_map, credential)

            now = _now_ts()
            act_id = None
            conn = self._conn()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        ENGINE_LOCAL_ALIAS,
                        rel_norm,
                        friendly_name,
                        "ansible",
                        now,
                        "Running",
                        "",
                        "",
                    ),
                )
                act_id = _resolve_activity_history_insert_id(
                    cur,
                    hostname=ENGINE_LOCAL_ALIAS,
                    script_path=rel_norm,
                    script_name=friendly_name,
                    script_type="ansible",
                    ran_at=now,
                )
                conn.commit()
            finally:
                conn.close()

            if not callable(self._server_ansible_runner):
                raise RuntimeError("Server-side Ansible runner is not configured")

            self._server_ansible_runner(
                hostname=ENGINE_LOCAL_ALIAS,
                playbook_rel_path=rel_norm,
                playbook_name=friendly_name,
                playbook_abs_path="",
                playbook_content=normalized_script,
                credential_id=credential_id,
                variable_values=overrides_map,
                payload_files=files,
                target_specifications=target_specifications,
                runtime_files=runtime_files,
                source="scheduled_job",
                activity_id=act_id,
                scheduled_job_id=job_id,
                scheduled_run_id=run_row_id,
                scheduled_job_run_row_id=run_row_id,
                connection=run_mode_norm,
            )
            return {
                "activity_id": int(act_id) if act_id is not None else None,
                "component_kind": "ansible",
                "script_type": "ansible",
                "component_path": rel_norm,
                "component_name": friendly_name,
            }
        except Exception as exc:
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
                    (
                        RUN_STATUS_FAILED,
                        _now_ts(),
                        _now_ts(),
                        str(exc)[:512],
                        int(run_row_id),
                    ),
                )
                conn.commit()
            finally:
                conn.close()
            self._log_event(
                "shared ansible dispatch failed",
                job_id=int(job_id),
                run_id=int(run_row_id),
                level="ERROR",
                extra={"error": str(exc), "scheduled_ts": int(scheduled_ts or 0)},
            )
            return None

    def _dispatch_run_activities(
        self,
        *,
        job_id: int,
        run_row_id: int,
        scheduled_ts: int,
        hostname: str,
        run_mode: str,
        script_components: Sequence[Dict[str, Any]],
        ansible_components: Sequence[Dict[str, Any]],
        credential_id: Optional[int],
        use_service_account: bool,
        component_index: Optional[int] = None,
    ) -> bool:
        conn = self._conn()
        try:
            cur = conn.cursor()
            ts_now = _now_ts()
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       started_ts=COALESCE(started_ts, ?),
                       finished_ts=NULL,
                       error='',
                       updated_at=?
                 WHERE id=?
                """,
                (RUN_STATUS_RUNNING, ts_now, ts_now, int(run_row_id)),
            )
            conn.commit()

            transport_mode = _normalize_ansible_transport(run_mode)
            remote_requires_cred = (transport_mode == "ssh") or (transport_mode == "winrm" and not use_service_account)
            if remote_requires_cred and not credential_id:
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=?,
                           updated_at=?,
                           error=?
                     WHERE id=?
                    """,
                    (RUN_STATUS_FAILED, ts_now, ts_now, "Credential required for remote execution", int(run_row_id)),
                )
                conn.commit()
                return False

            activity_links: List[Dict[str, Any]] = []
            dispatch_errors: List[str] = []

            selected_ansible_components = list(ansible_components or [])
            if component_index is not None:
                try:
                    normalized_component_index = int(component_index)
                except Exception:
                    normalized_component_index = -1
                if 0 <= normalized_component_index < len(selected_ansible_components):
                    selected_ansible_components = [selected_ansible_components[normalized_component_index]]
                else:
                    selected_ansible_components = []

            for component in script_components:
                try:
                    link = self._dispatch_script(job_id, run_row_id, scheduled_ts, hostname, component, run_mode)
                except Exception as exc:
                    dispatch_errors.append(str(exc))
                    link = None
                normalized_link = self._normalize_run_activity_link(
                    run_row_id=int(run_row_id),
                    link=link,
                    default_component_kind="script",
                    default_script_type="powershell",
                )
                if normalized_link:
                    activity_links.append(normalized_link)

            for component in selected_ansible_components:
                try:
                    link = self._dispatch_ansible(
                        hostname,
                        component,
                        job_id,
                        run_row_id,
                        run_mode,
                        credential_id,
                        use_service_account,
                    )
                except Exception as exc:
                    dispatch_errors.append(str(exc))
                    link = None
                normalized_link = self._normalize_run_activity_link(
                    run_row_id=int(run_row_id),
                    link=link,
                    default_component_kind="ansible",
                    default_script_type="ansible",
                )
                if normalized_link:
                    activity_links.append(normalized_link)

            if activity_links:
                conn.close()
                conn = None
                self._persist_run_activity_links(activity_links, created_at=ts_now)
                return True

            cur.execute(
                """
                SELECT status, finished_ts
                  FROM scheduled_job_runs
                 WHERE id=?
                """,
                (int(run_row_id),),
            )
            current_run = cur.fetchone()
            current_status = ""
            current_finished_ts = None
            if current_run:
                try:
                    current_status = str(current_run["status"] or "").strip()
                    current_finished_ts = current_run["finished_ts"]
                except Exception:
                    current_status = str(current_run[0] or "").strip()
                    current_finished_ts = current_run[1]
            if current_finished_ts is not None and current_status in {
                RUN_STATUS_SKIPPED,
                RUN_STATUS_FAILED,
                RUN_STATUS_EXPIRED,
                RUN_STATUS_TIMED_OUT,
            }:
                conn.commit()
                return False

            error_text = dispatch_errors[0] if dispatch_errors else "No runnable activities were dispatched"
            finished_ts = _now_ts()
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       finished_ts=?,
                       updated_at=?,
                       error=?
                 WHERE id=?
                """,
                (RUN_STATUS_FAILED, finished_ts, finished_ts, str(error_text)[:512], int(run_row_id)),
            )
            conn.commit()
            return False
        finally:
            if conn is not None:
                conn.close()

    def _normalize_run_activity_link(
        self,
        *,
        run_row_id: int,
        link: Optional[Mapping[str, Any]],
        default_component_kind: str,
        default_script_type: str,
    ) -> Optional[Dict[str, Any]]:
        if not isinstance(link, Mapping):
            return None
        activity_id = link.get("activity_id")
        if activity_id is None:
            return None
        try:
            normalized_activity_id = int(activity_id)
        except Exception:
            return None
        return {
            "run_id": int(run_row_id),
            "activity_id": normalized_activity_id,
            "component_kind": str(link.get("component_kind") or default_component_kind),
            "script_type": str(link.get("script_type") or default_script_type),
            "component_path": str(link.get("component_path") or ""),
            "component_name": str(link.get("component_name") or ""),
        }

    def _persist_run_activity_links(
        self,
        activity_links: Sequence[Mapping[str, Any]],
        *,
        created_at: Optional[int] = None,
    ) -> None:
        rows: List[Tuple[int, int, str, str, str, str]] = []
        for raw_link in activity_links:
            if not isinstance(raw_link, Mapping):
                continue
            try:
                run_id = int(raw_link.get("run_id"))
                activity_id = int(raw_link.get("activity_id"))
            except Exception:
                continue
            rows.append(
                (
                    run_id,
                    activity_id,
                    str(raw_link.get("component_kind") or ""),
                    str(raw_link.get("script_type") or ""),
                    str(raw_link.get("component_path") or ""),
                    str(raw_link.get("component_name") or ""),
                )
            )
        if not rows:
            return

        ts_now = int(created_at) if created_at is not None else _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            for run_id, activity_id, component_kind, script_type, component_path, component_name in rows:
                cur.execute(
                    """
                    INSERT OR IGNORE INTO scheduled_job_run_activity(
                        run_id,
                        activity_id,
                        component_kind,
                        script_type,
                        component_path,
                        component_name,
                        created_at
                    ) VALUES (?,?,?,?,?,?,?)
                    """,
                    (
                        run_id,
                        activity_id,
                        component_kind,
                        script_type,
                        component_path,
                        component_name,
                        ts_now,
                    ),
                )
            conn.commit()
        finally:
            conn.close()

    def _compute_next_run(self, schedule_type: str, start_ts: Optional[int], last_run_ts: Optional[int], now_ts: int) -> Optional[int]:
        st = (schedule_type or "immediately").strip().lower()
        start_ts = _floor_minute(start_ts) if start_ts else None
        last_run_ts = _floor_minute(last_run_ts) if last_run_ts else None
        now_ts = _floor_minute(now_ts)
        if st == "immediately":
            # Run once asap if never ran
            return None if last_run_ts else now_ts
        if st == "once":
            if not start_ts:
                return None
            # If never ran and time in future/now
            if not last_run_ts:
                return start_ts
            return None
        if not start_ts:
            return None

        # For recurring types, base off start_ts and last_run_ts
        last = last_run_ts if last_run_ts else None
        # Minute/Hour intervals
        if st in ("every_5_minutes", "every_10_minutes", "every_15_minutes", "every_30_minutes", "every_hour"):
            period_map = {
                "every_5_minutes": 5 * 60,
                "every_10_minutes": 10 * 60,
                "every_15_minutes": 15 * 60,
                "every_30_minutes": 30 * 60,
                "every_hour": 60 * 60,
            }
            period = period_map.get(st)
            if last is None:
                return start_ts
            candidate = last + period
            return candidate
        if st == "daily":
            period = 86400
            if last is None:
                return start_ts
            candidate = last + period
            return candidate if candidate <= now_ts else candidate
        if st == "weekly":
            period = 7 * 86400
            if last is None:
                return start_ts
            candidate = last + period
            return candidate if candidate <= now_ts else candidate
        if st == "monthly":
            if last is None:
                return start_ts
            base = _to_dt_tuple(last)
            candidate = _add_months(base, 1)
            if candidate <= now_ts:
                return candidate
            return candidate
        if st == "yearly":
            if last is None:
                return start_ts
            base = _to_dt_tuple(last)
            candidate = _add_years(base, 1)
            if candidate <= now_ts:
                return candidate
            return candidate
        return None

    def _should_expire(self, started_ts: Optional[int], expiration: Optional[str], now_ts: int) -> bool:
        if not started_ts:
            return False
        seconds = _parse_expiration(expiration)
        if not seconds:
            return False
        return (started_ts + seconds) <= now_ts

    def _tick_once(self):
        now = _now_ts()
        self._log_event("tick begin", extra={"now_ts": now})

        conn = self._conn()
        cur = conn.cursor()
        try:
            cur.execute(
                """
                SELECT r.id, r.job_id, r.target_hostname, r.started_ts, j.expiration, r.component_kind
                  FROM scheduled_job_runs r
                  JOIN scheduled_jobs j ON j.id = r.job_id
                 WHERE r.status = ?
                """,
                (RUN_STATUS_RUNNING,),
            )
            rows = cur.fetchall()
            for rid, row_job_id, row_host, started_ts, expiration, component_kind in rows:
                if str(component_kind or "").strip().lower() == "workflow":
                    continue
                if self._should_expire(started_ts, expiration, now):
                    cur.execute(
                        "UPDATE scheduled_job_runs SET status=?, finished_ts=?, updated_at=? WHERE id=?",
                        (RUN_STATUS_TIMED_OUT, now, now, rid),
                    )
            conn.commit()
        except Exception as exc:
            self._log_event(
                "failed to evaluate running runs for expiration",
                level="ERROR",
                extra={"error": str(exc)},
            )

        try:
            cutoff = now - (self.RETENTION_DAYS * 86400)
            cur.execute(
                "SELECT id FROM scheduled_job_runs WHERE COALESCE(finished_ts, started_ts, scheduled_ts, 0) < ?",
                (cutoff,),
            )
            stale_run_ids = [int(row[0]) for row in cur.fetchall()]
            if stale_run_ids:
                placeholders = ",".join("?" for _ in stale_run_ids)
                cur.execute(f"DELETE FROM scheduled_job_run_activity WHERE run_id IN ({placeholders})", tuple(stale_run_ids))
                cur.execute(f"DELETE FROM scheduled_job_run_targets WHERE run_id IN ({placeholders})", tuple(stale_run_ids))
                cur.execute(f"DELETE FROM scheduled_job_onboarding_targets WHERE run_id IN ({placeholders})", tuple(stale_run_ids))
                cur.execute(f"DELETE FROM scheduled_job_runs WHERE id IN ({placeholders})", tuple(stale_run_ids))
                conn.commit()
        except Exception as exc:
            self._log_event(
                "failed to purge scheduled_job_runs history",
                level="ERROR",
                extra={"error": str(exc)},
            )

        try:
            cur.execute(
                """
                SELECT id, components_json, targets_json, schedule_type, start_ts, expiration,
                       execution_context, credential_id, use_service_account, created_at, job_kind
                  FROM scheduled_jobs
                 WHERE enabled=1
              ORDER BY id ASC
                """
            )
            jobs = cur.fetchall()
        except Exception as exc:
            jobs = []
            self._log_event(
                "failed to load enabled scheduled jobs for tick",
                level="ERROR",
                extra={"error": str(exc)},
            )
        finally:
            try:
                conn.close()
            except Exception:
                pass

        online: set[str] = set()
        try:
            if callable(self._online_lookup):
                online = {str(host) for host in (self._online_lookup() or []) if str(host).strip()}
        except Exception as exc:
            self._log_event(
                "failed to gather online host snapshot",
                level="ERROR",
                extra={"error": str(exc)},
            )
            online = set()

        now_min = _now_minute()
        device_inventory_cache: Optional[List[Dict[str, Any]]] = None
        ansible_runner_limits = self._individual_ansible_runner_limits()
        global_running_ansible, running_ansible_by_job = self._running_ansible_run_counts()

        for (
            job_id,
            components_json,
            targets_json,
            schedule_type,
            start_ts,
            expiration,
            execution_context,
            credential_id,
            use_service_account_flag,
            created_at,
            job_kind,
        ) in jobs:
            try:
                try:
                    raw_targets = json.loads(targets_json or "[]")
                except Exception:
                    raw_targets = []
                try:
                    components = json.loads(components_json or "[]")
                except Exception:
                    components = []

                normalized_job_kind = _normalize_job_kind(job_kind)
                if normalized_job_kind == JOB_KIND_ONBOARDING:
                    occurrence_ts = self._resolve_occurrence_for_tick(
                        job_id=int(job_id),
                        schedule_type=str(schedule_type or ""),
                        start_ts=start_ts,
                        created_at=created_at,
                        now_min=now_min,
                    )
                    if occurrence_ts is None:
                        continue
                    occurrence_runs = self._load_occurrence_runs(int(job_id), int(occurrence_ts))
                    if not occurrence_runs:
                        self._record_onboarding_occurrence_snapshot(
                            job_id=int(job_id),
                            scheduled_ts=int(occurrence_ts),
                            created_at=now,
                        )
                        occurrence_runs = self._load_occurrence_runs(int(job_id), int(occurrence_ts))
                    try:
                        job_credential_id = int(credential_id) if credential_id is not None else None
                    except Exception:
                        job_credential_id = None
                    for run in occurrence_runs:
                        status = str(run.get("status") or "").strip()
                        if self._is_terminal_run_status(status) or status == RUN_STATUS_RUNNING:
                            continue
                        self._dispatch_onboarding_run(
                            job_id=int(job_id),
                            run_row_id=int(run["id"]),
                            scheduled_ts=int(occurrence_ts),
                            components=components,
                            targets=raw_targets,
                            credential_id=job_credential_id,
                        )
                    continue

                workflow_components: List[Dict[str, Any]] = []
                script_components: List[Dict[str, Any]] = []
                ansible_components: List[Dict[str, Any]] = []
                for component in components:
                    if not isinstance(component, dict):
                        continue
                    component_type = str(
                        component.get("type")
                        or component.get("component_type")
                        or component.get("assembly_type")
                        or ""
                    ).strip().lower()
                    component_subtype = str(
                        component.get("assembly_subtype")
                        or component.get("assemblySubtype")
                        or component.get("script_type")
                        or ""
                    ).strip().lower()
                    is_workflow = component_type == "workflow" or component_subtype == "workflow"
                    is_ansible = component_type in {"ansible", "playbook"} or component_subtype in {"ansible", "playbook"}
                    if is_workflow:
                        workflow_components.append(dict(component))
                    elif is_ansible:
                        ansible_components.append(dict(component))
                    else:
                        script_components.append(dict(component))

                if workflow_components and (len(workflow_components) != 1 or script_components or ansible_components):
                    self._log_event(
                        "skipping invalid workflow-backed scheduled job configuration",
                        job_id=int(job_id),
                        level="ERROR",
                    )
                    continue
                if not workflow_components and not script_components and not ansible_components:
                    continue

                is_workflow_job = bool(workflow_components)
                workflow_component = workflow_components[0] if workflow_components else None
                run_mode = (execution_context or "system").strip().lower()
                individual_ansible_mode = (
                    (not is_workflow_job)
                    and bool(ansible_components)
                    and not bool(script_components)
                    and _is_individual_ansible_context(run_mode)
                )
                shared_ansible_mode = (not is_workflow_job) and self._should_use_shared_ansible_runs(
                    run_mode=run_mode,
                    script_components=script_components,
                    ansible_components=ansible_components,
                )
                job_use_service_account = bool(use_service_account_flag) if (_normalize_ansible_transport(run_mode) == "winrm" and not is_workflow_job) else False
                try:
                    job_credential_id = int(credential_id) if (credential_id is not None and not is_workflow_job) else None
                except Exception:
                    job_credential_id = None

                occurrence_ts = self._resolve_occurrence_for_tick(
                    job_id=int(job_id),
                    schedule_type=str(schedule_type or ""),
                    start_ts=start_ts,
                    created_at=created_at,
                    now_min=now_min,
                )
                if occurrence_ts is None:
                    continue

                occurrence_runs = self._load_occurrence_runs(int(job_id), int(occurrence_ts))
                if not occurrence_runs:
                    if is_workflow_job and isinstance(workflow_component, dict):
                        self._record_workflow_occurrence_snapshot(
                            job_id=int(job_id),
                            scheduled_ts=int(occurrence_ts),
                            workflow_component=workflow_component,
                            created_at=now,
                        )
                    else:
                        include_filters = self._targets_include_filters(raw_targets)
                        if include_filters and device_inventory_cache is None:
                            try:
                                device_inventory_cache = self._filter_matcher.fetch_devices()
                            except Exception:
                                device_inventory_cache = []
                        try:
                            targets, resolution_meta = self._filter_matcher.resolve_target_entries(
                                raw_targets,
                                devices=device_inventory_cache if include_filters else None,
                            )
                        except Exception as exc:
                            self._log_event(
                                "failed to resolve job targets",
                                job_id=int(job_id),
                                level="ERROR",
                                extra={"error": str(exc), "scheduled_ts": int(occurrence_ts)},
                            )
                            targets = []
                            resolution_meta = {}
                        if shared_ansible_mode:
                            self._record_shared_ansible_occurrence_snapshot(
                                job_id=int(job_id),
                                scheduled_ts=int(occurrence_ts),
                                run_mode=run_mode,
                                ansible_components=ansible_components,
                                resolved_targets=self._resolved_targets_from_meta(resolution_meta),
                                created_at=now,
                            )
                        elif individual_ansible_mode:
                            self._record_individual_ansible_occurrence_snapshot(
                                job_id=int(job_id),
                                scheduled_ts=int(occurrence_ts),
                                run_mode=run_mode,
                                ansible_components=ansible_components,
                                resolved_targets=self._resolved_targets_from_meta(resolution_meta),
                                created_at=now,
                            )
                        else:
                            self._record_occurrence_snapshot(
                                job_id=int(job_id),
                                scheduled_ts=int(occurrence_ts),
                                targets=[str(host) for host in targets if str(host).strip()],
                                resolution_meta=resolution_meta,
                                created_at=now,
                            )
                    occurrence_runs = self._load_occurrence_runs(int(job_id), int(occurrence_ts))

                exp_seconds = _parse_expiration(expiration)
                for run in occurrence_runs:
                    status = str(run.get("status") or "").strip()
                    if self._is_terminal_run_status(status):
                        continue
                    if status == RUN_STATUS_RUNNING:
                        continue
                    if is_workflow_job and isinstance(workflow_component, dict):
                        self._dispatch_workflow_run(
                            job_id=int(job_id),
                            run_row_id=int(run["id"]),
                            scheduled_ts=int(occurrence_ts),
                            workflow_component=workflow_component,
                        )
                        continue
                    if shared_ansible_mode and bool(run.get("shared_execution")):
                        component_index = run.get("component_index")
                        try:
                            component_index = int(component_index) if component_index is not None else 0
                        except Exception:
                            component_index = 0
                        component = ansible_components[component_index] if 0 <= component_index < len(ansible_components) else None
                        if not isinstance(component, dict):
                            conn2 = self._conn()
                            try:
                                cur2 = conn2.cursor()
                                cur2.execute(
                                    """
                                    UPDATE scheduled_job_runs
                                       SET status=?,
                                           finished_ts=?,
                                           updated_at=?,
                                           error=?
                                     WHERE id=?
                                    """,
                                    (
                                        RUN_STATUS_FAILED,
                                        now,
                                        now,
                                        "Unable to resolve Ansible component for shared execution.",
                                        int(run["id"]),
                                    ),
                                )
                                conn2.commit()
                            finally:
                                conn2.close()
                            continue
                        if not self._can_dispatch_ansible_run(
                            job_id=int(job_id),
                            global_running=global_running_ansible,
                            running_by_job=running_ansible_by_job,
                            limits=ansible_runner_limits,
                        ):
                            continue
                        link = self._dispatch_shared_ansible(
                            job_id=int(job_id),
                            run_row_id=int(run["id"]),
                            scheduled_ts=int(occurrence_ts),
                            run_mode=run_mode,
                            component=component,
                            credential_id=job_credential_id,
                            use_service_account=job_use_service_account,
                        )
                        normalized_link = self._normalize_run_activity_link(
                            run_row_id=int(run["id"]),
                            link=link,
                            default_component_kind="ansible",
                            default_script_type="ansible",
                        )
                        if normalized_link:
                            self._persist_run_activity_links([normalized_link], created_at=now)
                            global_running_ansible += 1
                            running_ansible_by_job[int(job_id)] = int(running_ansible_by_job.get(int(job_id), 0) or 0) + 1
                        continue
                    host = str(run.get("target_hostname") or "").strip()
                    if not host:
                        continue
                    if host in online:
                        if individual_ansible_mode and not self._can_dispatch_ansible_run(
                            job_id=int(job_id),
                            global_running=global_running_ansible,
                            running_by_job=running_ansible_by_job,
                            limits=ansible_runner_limits,
                        ):
                            continue
                        dispatched = self._dispatch_run_activities(
                            job_id=int(job_id),
                            run_row_id=int(run["id"]),
                            scheduled_ts=int(occurrence_ts),
                            hostname=host,
                            run_mode=run_mode,
                            script_components=script_components,
                            ansible_components=ansible_components,
                            credential_id=job_credential_id,
                            use_service_account=job_use_service_account,
                            component_index=run.get("component_index") if individual_ansible_mode else None,
                        )
                        if individual_ansible_mode and dispatched:
                            global_running_ansible += 1
                            running_ansible_by_job[int(job_id)] = int(running_ansible_by_job.get(int(job_id), 0) or 0) + 1
                        continue
                    if exp_seconds is not None and (int(occurrence_ts) + exp_seconds) <= now:
                        conn2 = self._conn()
                        try:
                            cur2 = conn2.cursor()
                            cur2.execute(
                                """
                                UPDATE scheduled_job_runs
                                   SET status=?,
                                       finished_ts=?,
                                       updated_at=?,
                                       error=?
                                 WHERE id=?
                                """,
                                (RUN_STATUS_EXPIRED, now, now, "Device offline", int(run["id"])),
                            )
                            conn2.commit()
                        finally:
                            conn2.close()
            except Exception as exc:
                self._log_event(
                    "unhandled exception while processing job during tick",
                    job_id=int(job_id),
                    level="ERROR",
                    extra={"error": str(exc)},
                )

        self._log_event("tick end", extra={"now_ts": now})

    def start(self):
        if self._running:
            self._log_event("start requested but scheduler already running")
            return
        self._running = True
        self._log_event("scheduler loop starting")

        def _loop():
            # cooperative loop aligned to minutes
            while self._running:
                try:
                    self._tick_once()
                except Exception as exc:
                    self._log_event("unhandled exception during scheduler tick", level="ERROR", extra={"error": repr(exc)})
                # Sleep until next minute boundary
                delay = 60 - (_now_ts() % 60)
                try:
                    import eventlet
                    eventlet.sleep(delay)
                except Exception:
                    time.sleep(delay)

        # Use SocketIO helper so it integrates with eventlet
        try:
            self.socketio.start_background_task(_loop)
            self._log_event("scheduler loop spawned via socketio task")
        except Exception:
            # Fallback to thread
            import threading
            threading.Thread(target=_loop, daemon=True).start()
            self._log_event("scheduler loop spawned via threading fallback")

    # ---------- Route registration ----------
    def _register_routes(self):
        app = self.app

        # Utility: job row
        def _job_row_to_dict(r) -> Dict[str, Any]:
            base = {
                "id": r[0],
                "name": r[1],
                "components": json.loads(r[2] or "[]"),
                "targets": json.loads(r[3] or "[]"),
                "schedule_type": r[4] or "immediately",
                "start_ts": r[5],
                "duration_stop_enabled": bool(r[6] or 0),
                "expiration": r[7] or "no_expire",
                "execution_context": r[8] or "system",
                "credential_id": r[9],
                "use_service_account": bool(r[10] or 0),
                "enabled": bool(r[11] or 0),
                "created_at": r[12] or 0,
                "updated_at": r[13] or 0,
                "job_kind": _normalize_job_kind(r[14] if len(r) > 14 else JOB_KIND_AUTOMATION),
            }
            # Attach computed status summary for latest occurrence
            try:
                conn = self._conn()
                c = conn.cursor()
                c.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (base["id"],))
                max_occ = c.fetchone()[0]
                summary_status = None
                last_run_ts = None
                total_targets = len(base.get("targets") or [])
                result_counts = {
                    "pending": 0,
                    "running": 0,
                    "success": 0,
                    "warning": 0,
                    "failed": 0,
                    "expired": 0,
                    "timed_out": 0,
                    "skipped": 0,
                    "total_targets": total_targets,
                }
                if max_occ:
                    c.execute(
                        """
                        SELECT COUNT(DISTINCT t.hostname)
                          FROM scheduled_job_run_targets t
                          JOIN scheduled_job_runs r ON r.id = t.run_id
                         WHERE r.job_id=? AND r.scheduled_ts=?
                        """,
                        (base["id"], max_occ),
                    )
                    total_targets = int(c.fetchone()[0] or 0)
                    if total_targets <= 0:
                        c.execute(
                            """
                            SELECT COUNT(DISTINCT target_hostname)
                              FROM scheduled_job_runs
                             WHERE job_id=? AND scheduled_ts=? AND target_hostname IS NOT NULL AND TRIM(target_hostname) != ''
                            """,
                            (base["id"], max_occ),
                        )
                        total_targets = int(c.fetchone()[0] or 0)
                    c.execute(
                        """
                        SELECT id, target_hostname, scheduled_ts, started_ts, finished_ts, status, error, skip_reason,
                               shared_execution, component_index, component_kind, component_name
                          FROM scheduled_job_runs
                         WHERE job_id=? AND scheduled_ts=?
                        """,
                        (base["id"], max_occ),
                    )
                    occurrence_rows = [
                        {
                            "id": int(row[0]),
                            "target_hostname": row[1] or "",
                            "scheduled_ts": row[2],
                            "started_ts": row[3],
                            "finished_ts": row[4],
                            "status": row[5] or "",
                            "error": row[6] or "",
                            "skip_reason": row[7] or "",
                            "shared_execution": bool(row[8] or 0),
                            "component_index": row[9],
                            "component_kind": row[10] or "",
                            "component_name": row[11] or "",
                        }
                        for row in c.fetchall()
                    ]
                    if base.get("job_kind") == JOB_KIND_ONBOARDING:
                        onboarding_rows = self._load_onboarding_target_rows(base["id"], int(max_occ))
                        result_counts = {
                            "pending": 0,
                            "running": 0,
                            "success": 0,
                            "warning": 0,
                            "failed": 0,
                            "expired": 0,
                            "timed_out": 0,
                            "skipped": 0,
                            "total_targets": len(onboarding_rows),
                        }
                        for target_row in onboarding_rows:
                            bucket = _onboarding_status_bucket(target_row.get("status"))
                            if bucket in result_counts:
                                result_counts[bucket] += 1
                        if not onboarding_rows:
                            for occurrence_row in occurrence_rows:
                                bucket = _status_bucket_for_run(occurrence_row.get("status"))
                                if bucket and bucket in result_counts:
                                    result_counts[bucket] += 1
                            result_counts["total_targets"] = max(1, len(occurrence_rows))
                        if result_counts["running"]:
                            summary_status = RUN_STATUS_RUNNING
                        elif result_counts["failed"]:
                            summary_status = RUN_STATUS_FAILED
                        elif result_counts["warning"]:
                            summary_status = RUN_STATUS_WARNING
                        elif result_counts["pending"]:
                            summary_status = RUN_STATUS_PENDING
                        elif result_counts["success"]:
                            summary_status = RUN_STATUS_SUCCESS
                        elif result_counts["skipped"]:
                            summary_status = RUN_STATUS_SKIPPED
                        else:
                            summary_status = occurrence_rows[0].get("status") if occurrence_rows else None
                        last_run_ts = int(max_occ)
                        conn.close()
                        base["last_run_ts"] = last_run_ts
                        base["last_status"] = summary_status or ("Scheduled" if base.get("start_ts") else "")
                        base["latest_occurrence"] = last_run_ts
                        base["result_counts"] = result_counts
                        try:
                            base["next_run_ts"] = self._compute_next_run(
                                base["schedule_type"], base.get("start_ts"), base.get("last_run_ts"), _now_ts()
                            )
                        except Exception:
                            base["next_run_ts"] = None
                        warning = self._credential_reset_warning(base.get("credential_id"))
                        base["warning_code"] = (warning or {}).get("warning_code") or ""
                        base["warning_message"] = (warning or {}).get("warning_message") or ""
                        return base
                    occurrence_target_rows = self._load_occurrence_target_rows(base["id"], int(max_occ))
                    workflow_occurrence_rows = [
                        row for row in occurrence_rows if str(row.get("component_kind") or "").strip().lower() == "workflow"
                    ]
                    has_workflow_only_occurrence = bool(workflow_occurrence_rows) and not occurrence_target_rows and not any(
                        str(row.get("target_hostname") or "").strip() for row in occurrence_rows
                    )
                    if has_workflow_only_occurrence:
                        result_counts["total_targets"] = len(workflow_occurrence_rows)
                        for occurrence_row in workflow_occurrence_rows:
                            bucket = _status_bucket_for_run(occurrence_row.get("status"))
                            if bucket and bucket in result_counts:
                                result_counts[bucket] += 1
                        if result_counts["running"]:
                            summary_status = RUN_STATUS_RUNNING
                        elif result_counts["failed"]:
                            summary_status = RUN_STATUS_FAILED
                        elif result_counts["timed_out"]:
                            summary_status = RUN_STATUS_TIMED_OUT
                        elif result_counts["warning"]:
                            summary_status = RUN_STATUS_WARNING
                        elif result_counts["expired"]:
                            summary_status = RUN_STATUS_EXPIRED
                        elif result_counts["pending"]:
                            summary_status = RUN_STATUS_PENDING
                        elif result_counts["success"]:
                            summary_status = RUN_STATUS_SUCCESS
                        elif result_counts["skipped"]:
                            summary_status = RUN_STATUS_SKIPPED
                        has_no_targets_skip = False
                    else:
                        aggregated_by_host, aggregated_counts, has_no_targets_skip = self._aggregate_occurrence_targets(
                            occurrence_rows,
                            occurrence_target_rows,
                        )
                        has_no_eligible_targets_skip = _all_skipped_for_reason(
                            aggregated_by_host,
                            resolution_reason=RESOLUTION_REASON_REMOTE_PREFLIGHT_FAILED,
                        )
                        total_targets = int(aggregated_counts.get("total_targets") or total_targets or 0)
                        result_counts["total_targets"] = total_targets
                        result_counts["pending"] = int(aggregated_counts.get("pending") or 0)
                        result_counts["running"] = int(aggregated_counts.get("running") or 0)
                        result_counts["success"] = int(aggregated_counts.get("success") or 0)
                        result_counts["warning"] = int(aggregated_counts.get("warning") or 0)
                        result_counts["failed"] = int(aggregated_counts.get("failed") or 0)
                        result_counts["expired"] = int(aggregated_counts.get("expired") or 0)
                        result_counts["timed_out"] = int(aggregated_counts.get("timed_out") or 0)
                        result_counts["skipped"] = int(aggregated_counts.get("skipped") or 0)
                        if result_counts["running"]:
                            summary_status = RUN_STATUS_RUNNING
                        elif result_counts["failed"]:
                            summary_status = RUN_STATUS_FAILED
                        elif result_counts["timed_out"]:
                            summary_status = RUN_STATUS_TIMED_OUT
                        elif result_counts["warning"]:
                            summary_status = RUN_STATUS_WARNING
                        elif result_counts["expired"]:
                            summary_status = RUN_STATUS_EXPIRED
                        elif result_counts["pending"]:
                            summary_status = RUN_STATUS_PENDING
                        elif result_counts["success"]:
                            summary_status = RUN_STATUS_SUCCESS
                        elif has_no_eligible_targets_skip:
                            summary_status = "No Eligible Targets"
                        elif has_no_targets_skip:
                            summary_status = "No Devices Targeted"
                        elif result_counts["skipped"]:
                            summary_status = RUN_STATUS_SKIPPED
                    last_run_ts = int(max_occ)
                conn.close()
            except Exception:
                summary_status = None
                last_run_ts = None
                result_counts = {
                    "pending": len(base.get("targets") or []),
                    "running": 0,
                    "success": 0,
                    "warning": 0,
                    "failed": 0,
                    "expired": 0,
                    "timed_out": 0,
                    "skipped": 0,
                    "total_targets": len(base.get("targets") or []),
                }
            base["last_run_ts"] = last_run_ts
            base["last_status"] = summary_status or ("Scheduled" if base.get("start_ts") else "")
            base["latest_occurrence"] = last_run_ts
            base["result_counts"] = result_counts
            try:
                base["next_run_ts"] = self._compute_next_run(
                    base["schedule_type"], base.get("start_ts"), base.get("last_run_ts"), _now_ts()
                )
            except Exception:
                base["next_run_ts"] = None
            warning = self._credential_reset_warning(base.get("credential_id"))
            base["warning_code"] = (warning or {}).get("warning_code") or ""
            base["warning_message"] = (warning or {}).get("warning_message") or ""
            return base

        def _normalize_targets_for_save(raw_targets: Any) -> List[Any]:
            if not isinstance(raw_targets, list):
                raw_list = [raw_targets]
            else:
                raw_list = raw_targets
            numeric_normalized: List[Any] = []
            for entry in raw_list:
                if isinstance(entry, (int, float)) and not isinstance(entry, bool):
                    numeric_normalized.append(str(entry).strip())
                else:
                    numeric_normalized.append(entry)
            return normalize_targets_for_save(numeric_normalized)

        def _validate_targets_for_save(targets: Sequence[Any]) -> Optional[str]:
            filter_ids: List[int] = []
            for entry in targets:
                if not isinstance(entry, dict):
                    continue
                kind = str(entry.get("kind") or entry.get("type") or "").strip().lower()
                if kind != "filter" and entry.get("filter_id") is None:
                    continue
                try:
                    filter_ids.append(int(entry.get("filter_id") or entry.get("id")))
                except Exception:
                    return "One or more selected filters is invalid."
            if not filter_ids:
                return None
            filters = self._filter_matcher.load_filters(filter_ids, include_archived=True)
            for filter_id in filter_ids:
                record = filters.get(int(filter_id))
                if not record:
                    return f"Filter #{filter_id} does not exist."
                if record.get("archived"):
                    return f'Filter "{record.get("name") or filter_id}" is archived and cannot be scheduled.'
            return None

        def _parse_site_query(raw_value: Any) -> Optional[int]:
            if raw_value in (None, ""):
                return None
            try:
                site_id = int(str(raw_value).strip())
            except Exception:
                return None
            return site_id if site_id > 0 else None

        def _job_targets_match_site(
            raw_targets: Any,
            site_id: int,
            *,
            device_inventory_cache: Optional[Sequence[Dict[str, Any]]] = None,
        ) -> bool:
            try:
                targets = json.loads(raw_targets or "[]") if isinstance(raw_targets, str) else list(raw_targets or [])
            except Exception:
                targets = []
            if not targets:
                return False
            selected_site_id = int(site_id)
            for target in targets:
                if not isinstance(target, dict):
                    continue
                try:
                    target_site_id = int(str(target.get("site_id") or target.get("siteId") or "").strip())
                except Exception:
                    target_site_id = None
                if target_site_id == selected_site_id:
                    return True
            try:
                _target_hosts, target_meta = self._filter_matcher.resolve_target_entries(
                    targets,
                    devices=list(device_inventory_cache or []),
                )
            except Exception:
                return False
            for target in (target_meta or {}).get("resolved_targets") or []:
                if not isinstance(target, dict):
                    continue
                try:
                    target_site_id = int(str(target.get("site_id") or "").strip())
                except Exception:
                    target_site_id = None
                if target_site_id == selected_site_id:
                    return True
            return False

        def _is_workflow_component(component: Any) -> bool:
            if not isinstance(component, dict):
                return False
            raw_values = [
                component.get("kind"),
                component.get("type"),
                component.get("component_type"),
                component.get("assembly_type"),
                component.get("assemblyType"),
                component.get("assembly_subtype"),
                component.get("assemblySubtype"),
                component.get("script_type"),
            ]
            normalized = {str(value or "").strip().lower() for value in raw_values if str(value or "").strip()}
            return "workflow" in normalized

        def _is_ansible_component(component: Any) -> bool:
            if not isinstance(component, dict):
                return False
            raw_values = [
                component.get("kind"),
                component.get("type"),
                component.get("component_type"),
                component.get("assembly_type"),
                component.get("assemblyType"),
                component.get("assembly_subtype"),
                component.get("assemblySubtype"),
                component.get("script_type"),
            ]
            normalized = {str(value or "").strip().lower() for value in raw_values if str(value or "").strip()}
            return "ansible" in normalized or "playbook" in normalized

        def _component_execution_domain(component: Any) -> str:
            if not isinstance(component, dict):
                return ""
            if _is_workflow_component(component):
                return "workflow"
            if _is_ansible_component(component):
                return "ansible"
            return "script"

        def _workflow_components(components: Sequence[Any]) -> List[Dict[str, Any]]:
            return [dict(component) for component in (components or []) if _is_workflow_component(component)]

        def _validate_components_for_context(components: Sequence[Any], execution_context: str) -> Optional[str]:
            context_value = str(execution_context or "").strip().lower()
            workflow_components = _workflow_components(components)
            if workflow_components:
                return None
            component_domains = {
                _component_execution_domain(component)
                for component in (components or [])
                if _component_execution_domain(component)
            }
            if "script" in component_domains and "ansible" in component_domains:
                return (
                    "Scheduled jobs cannot mix script assemblies with Ansible playbook assemblies. "
                    "Remove the cross-domain assemblies or split them into separate jobs."
                )
            if context_value not in {"local", "ssh", "winrm", "ssh_individual", "winrm_individual", "system", "current_user"}:
                return None
            if context_value in {"local", "ssh", "winrm", "ssh_individual", "winrm_individual"} and component_domains and component_domains != {"ansible"}:
                return (
                    "Jobs using local, ssh, winrm, ssh_individual, or winrm_individual execution contexts must contain only Ansible components."
                )
            if context_value in {"system", "current_user"} and component_domains and component_domains != {"script"}:
                return "Jobs using agent execution contexts must contain only script assemblies."
            return None

        def _validate_workflow_job_configuration(
            components: Sequence[Any],
            targets: Sequence[Any],
            execution_context: Any,
            credential_id: Any,
            use_service_account: Any,
        ) -> Optional[str]:
            workflow_components = _workflow_components(components)
            if not workflow_components:
                return None
            if len(workflow_components) != 1:
                return "Workflow-backed scheduled jobs must contain exactly one workflow component."
            if len(workflow_components) != len(list(components or [])):
                return "Workflow-backed scheduled jobs cannot mix workflow, script, or Ansible components."
            if targets:
                return "Workflow-backed scheduled jobs cannot define scheduler-level targets. Configure targets inside workflow nodes instead."
            execution_context_value = str(execution_context or "system").strip().lower() or "system"
            if execution_context_value not in {"", "system"}:
                return "Workflow-backed scheduled jobs do not support scheduler-level execution contexts."
            if credential_id not in (None, "", "null"):
                return "Workflow-backed scheduled jobs do not support scheduler-level credentials."
            if bool(use_service_account):
                return "Workflow-backed scheduled jobs do not support scheduler-level service account targeting."
            workflow_component = workflow_components[0]
            workflow_guid = str(
                workflow_component.get("assembly_guid")
                or workflow_component.get("assemblyGuid")
                or workflow_component.get("workflow_guid")
                or workflow_component.get("workflowGuid")
                or ""
            ).strip()
            if not workflow_guid:
                return "Workflow-backed scheduled jobs require a saved workflow assembly selection."
            if callable(self._workflow_document_validator):
                try:
                    workflow_errors = self._workflow_document_validator(
                        workflow_guid,
                        source_type="scheduled_job",
                    ) or []
                except Exception as exc:
                    return f"Unable to validate workflow-backed scheduled job: {exc}"
                if workflow_errors:
                    return "; ".join(str(item) for item in workflow_errors if str(item).strip())
            return None

        @app.route("/api/scheduled_jobs", methods=["GET"])
        def api_scheduled_jobs_list():
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                from flask import request
                selected_site_id = _parse_site_query(request.args.get("site") or request.args.get("site_id"))
                if selected_site_id is not None and not self._site_access.user_can_access_site(
                    user,
                    selected_site_id,
                    allow_unassigned_admin_only=False,
                ):
                    return json.dumps({"jobs": []}), 200, {"Content-Type": "application/json"}
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        """
                        SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                               duration_stop_enabled, expiration, execution_context, credential_id,
                               use_service_account, enabled, created_at, updated_at, job_kind
                        FROM scheduled_jobs
                        ORDER BY created_at DESC
                        """
                    )
                    rows_raw = cur.fetchall()
                finally:
                    conn.close()
                device_inventory_cache = None
                if selected_site_id is not None:
                    try:
                        device_inventory_cache = self._filter_matcher.fetch_devices()
                    except Exception:
                        device_inventory_cache = []
                rows = []
                for r in rows_raw:
                    if not self._job_visible_to_user(user, r[3]):
                        continue
                    if selected_site_id is not None and not _job_targets_match_site(
                        r[3],
                        selected_site_id,
                        device_inventory_cache=device_inventory_cache,
                    ):
                        continue
                    rows.append(_job_row_to_dict(r))
                return json.dumps({"jobs": rows}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs", methods=["POST"])
        def api_scheduled_jobs_create():
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            data = self._json_body()
            name = (data.get("name") or "").strip()
            components = data.get("components") or []
            job_kind = _normalize_job_kind(data.get("job_kind") or data.get("kind"))
            if job_kind == JOB_KIND_ONBOARDING and not components:
                components = [{"kind": ONBOARDING_COMPONENT_KIND, "install_branch": "main"}]
            targets = _normalize_targets_for_save(data.get("targets") or [])
            scoped_targets, scope_error = self._site_access.scope_job_targets_for_persistence(user, targets)
            if scope_error:
                return (
                    json.dumps({"error": "out_of_scope_targets", "message": scope_error}),
                    403,
                    {"Content-Type": "application/json"},
                )
            targets = scoped_targets or []
            schedule_type = (data.get("schedule", {}).get("type") or data.get("schedule_type") or "immediately").strip().lower()
            start = data.get("schedule", {}).get("start") or data.get("start") or None
            start_ts = _parse_ts(start) if start else None
            duration_stop_enabled = int(bool((data.get("duration") or {}).get("stopAfterEnabled") or data.get("duration_stop_enabled")))
            expiration = (data.get("duration") or {}).get("expiration") or data.get("expiration") or "no_expire"
            execution_context = (data.get("execution_context") or "system").strip().lower()
            if job_kind == JOB_KIND_ONBOARDING:
                execution_context = "onboarding_local_network"
            credential_id = data.get("credential_id")
            try:
                credential_id = int(credential_id) if credential_id is not None else None
            except Exception:
                credential_id = None
            use_service_account_raw = data.get("use_service_account")
            use_service_account = 1 if (_normalize_ansible_transport(execution_context) == "winrm" and (use_service_account_raw is None or bool(use_service_account_raw))) else 0
            if job_kind == JOB_KIND_ONBOARDING:
                use_service_account = 0
            enabled = int(bool(data.get("enabled", True)))
            credential_warning = None
            workflow_job_error = None
            is_workflow_job = False
            if job_kind != JOB_KIND_ONBOARDING:
                workflow_job_error = _validate_workflow_job_configuration(
                    components,
                    targets,
                    execution_context,
                    credential_id,
                    bool(use_service_account),
                )
                is_workflow_job = workflow_job_error is None and bool(_workflow_components(components))
            if not name or not components:
                return json.dumps({"error": "name and components are required"}), 400, {"Content-Type": "application/json"}
            if not is_workflow_job and not targets:
                return json.dumps({"error": "targets required"}), 400, {"Content-Type": "application/json"}
            if job_kind == JOB_KIND_ONBOARDING:
                _cfg, onboarding_error = self._onboarding_scope_config(components=components, targets=targets)
                if onboarding_error:
                    return json.dumps({"error": onboarding_error}), 400, {"Content-Type": "application/json"}
                if credential_id is None:
                    return json.dumps({"error": "Onboarding jobs require one stored credential."}), 400, {"Content-Type": "application/json"}
            if workflow_job_error:
                return json.dumps({"error": workflow_job_error}), 400, {"Content-Type": "application/json"}
            if not is_workflow_job:
                target_error = _validate_targets_for_save(targets)
                if target_error:
                    return json.dumps({"error": target_error}), 400, {"Content-Type": "application/json"}
            component_error = _validate_components_for_context(components, execution_context)
            if component_error:
                return json.dumps({"error": component_error}), 400, {"Content-Type": "application/json"}
            credential_warning = None if is_workflow_job else self._credential_reset_warning(credential_id)
            if credential_warning and enabled:
                enabled = 0
            now = _now_ts()
            components_json = json.dumps(components)
            targets_json = json.dumps(targets)
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    INSERT INTO scheduled_jobs
                    (name, components_json, targets_json, schedule_type, start_ts, duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, job_kind, enabled, created_at, updated_at)
                    VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                    """,
                    (
                        name,
                        components_json,
                        targets_json,
                        schedule_type,
                        start_ts,
                        duration_stop_enabled,
                        expiration,
                        execution_context,
                        credential_id,
                        use_service_account,
                        job_kind,
                        enabled,
                        now,
                        now,
                    ),
                )
                job_id = cur.lastrowid
                conn.commit()
                row = None
                if job_id not in (None, ""):
                    cur.execute(
                        """
                        SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                               duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at, job_kind
                        FROM scheduled_jobs WHERE id=?
                        """,
                        (job_id,),
                    )
                    row = cur.fetchone()
                if not row:
                    # PostgreSQL compatibility layers may not expose lastrowid for every insert path.
                    where_clauses = [
                        "name=?",
                        "components_json=?",
                        "targets_json=?",
                        "schedule_type=?",
                        "duration_stop_enabled=?",
                        "expiration=?",
                        "execution_context=?",
                        "use_service_account=?",
                        "job_kind=?",
                        "enabled=?",
                        "created_at=?",
                        "updated_at=?",
                    ]
                    params = [
                        name,
                        components_json,
                        targets_json,
                        schedule_type,
                        duration_stop_enabled,
                        expiration,
                        execution_context,
                        use_service_account,
                        job_kind,
                        enabled,
                        now,
                        now,
                    ]
                    if start_ts is None:
                        where_clauses.append("start_ts IS NULL")
                    else:
                        where_clauses.append("start_ts=?")
                        params.append(start_ts)
                    if credential_id is None:
                        where_clauses.append("credential_id IS NULL")
                    else:
                        where_clauses.append("credential_id=?")
                        params.append(credential_id)
                    cur.execute(
                        f"""
                        SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                               duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at, job_kind
                          FROM scheduled_jobs
                         WHERE {" AND ".join(where_clauses)}
                         ORDER BY id DESC
                         LIMIT 1
                        """,
                        tuple(params),
                    )
                    row = cur.fetchone()
                conn.close()
                if not row:
                    return (
                        json.dumps({"error": "scheduled job was created but could not be reloaded"}),
                        500,
                        {"Content-Type": "application/json"},
                    )
                payload = {"job": _job_row_to_dict(row)}
                if credential_warning:
                    payload.update(credential_warning)
                return json.dumps(payload), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["GET"])
        def api_scheduled_jobs_get(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at, job_kind
                    FROM scheduled_jobs WHERE id=?
                    """,
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                if not row:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                if not self._job_visible_to_user(user, row[3]):
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["PUT"])
        def api_scheduled_jobs_update(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            data = self._json_body()
            fields: Dict[str, Any] = {}
            next_components: Optional[List[Any]] = None
            credential_warning = None
            if "name" in data:
                fields["name"] = (data.get("name") or "").strip()
            if "components" in data:
                next_components = list(data.get("components") or [])
                fields["components_json"] = json.dumps(next_components)
            if "targets" in data:
                normalized_targets = _normalize_targets_for_save(data.get("targets") or [])
                scoped_targets, scope_error = self._site_access.scope_job_targets_for_persistence(user, normalized_targets)
                if scope_error:
                    return (
                        json.dumps({"error": "out_of_scope_targets", "message": scope_error}),
                        403,
                        {"Content-Type": "application/json"},
                    )
                if normalized_targets:
                    target_error = _validate_targets_for_save(normalized_targets)
                    if target_error:
                        return json.dumps({"error": target_error}), 400, {"Content-Type": "application/json"}
                fields["targets_json"] = json.dumps(scoped_targets or [])
            if "schedule" in data or "schedule_type" in data:
                schedule_type = (data.get("schedule", {}).get("type") or data.get("schedule_type") or "immediately").strip().lower()
                fields["schedule_type"] = schedule_type
                start = data.get("schedule", {}).get("start") or data.get("start") or None
                fields["start_ts"] = _parse_ts(start) if start else None
            if "duration" in data or "duration_stop_enabled" in data:
                fields["duration_stop_enabled"] = int(bool((data.get("duration") or {}).get("stopAfterEnabled") or data.get("duration_stop_enabled")))
            if "expiration" in data or (data.get("duration") and "expiration" in data.get("duration")):
                fields["expiration"] = (data.get("duration") or {}).get("expiration") or data.get("expiration") or "no_expire"
            if "execution_context" in data:
                exec_ctx_val = (data.get("execution_context") or "system").strip().lower()
                fields["execution_context"] = exec_ctx_val
                if _normalize_ansible_transport(exec_ctx_val) != "winrm":
                    fields["use_service_account"] = 0
            if "credential_id" in data:
                cred_val = data.get("credential_id")
                if cred_val in (None, "", "null"):
                    fields["credential_id"] = None
                else:
                    try:
                        fields["credential_id"] = int(cred_val)
                    except Exception:
                        fields["credential_id"] = None
            if "use_service_account" in data:
                fields["use_service_account"] = 1 if bool(data.get("use_service_account")) else 0
            if "enabled" in data:
                fields["enabled"] = int(bool(data.get("enabled")))
            if "job_kind" in data or "kind" in data:
                fields["job_kind"] = _normalize_job_kind(data.get("job_kind") or data.get("kind"))
            if not fields:
                return json.dumps({"error": "no fields to update"}), 400, {"Content-Type": "application/json"}
            try:
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        """
                        SELECT credential_id, enabled, components_json, execution_context, targets_json, job_kind
                          FROM scheduled_jobs
                         WHERE id=?
                        """,
                        (job_id,),
                    )
                    current_row = cur.fetchone()
                finally:
                    conn.close()
                if not current_row:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                if not self._job_visible_to_user(user, current_row[4]):
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                if next_components is None:
                    try:
                        next_components = json.loads(current_row[2] or "[]")
                    except Exception:
                        next_components = []
                effective_targets_json = fields.get("targets_json", current_row[4])
                try:
                    effective_targets = json.loads(effective_targets_json or "[]")
                except Exception:
                    effective_targets = []
                effective_context = fields.get("execution_context") or current_row[3] or "system"
                effective_credential_id = fields.get("credential_id", current_row[0])
                effective_use_service_account = fields.get("use_service_account", 0)
                effective_job_kind = _normalize_job_kind(fields.get("job_kind", current_row[5] if len(current_row) > 5 else JOB_KIND_AUTOMATION))
                workflow_job_error = None
                is_workflow_job = False
                if effective_job_kind != JOB_KIND_ONBOARDING:
                    workflow_job_error = _validate_workflow_job_configuration(
                        next_components or [],
                        effective_targets or [],
                        effective_context,
                        effective_credential_id,
                        bool(effective_use_service_account),
                    )
                    is_workflow_job = workflow_job_error is None and bool(_workflow_components(next_components or []))
                if workflow_job_error:
                    return json.dumps({"error": workflow_job_error}), 400, {"Content-Type": "application/json"}
                if not is_workflow_job and not effective_targets:
                    return json.dumps({"error": "targets required"}), 400, {"Content-Type": "application/json"}
                if effective_job_kind == JOB_KIND_ONBOARDING:
                    _cfg, onboarding_error = self._onboarding_scope_config(components=next_components or [], targets=effective_targets or [])
                    if onboarding_error:
                        return json.dumps({"error": onboarding_error}), 400, {"Content-Type": "application/json"}
                    if effective_credential_id in (None, "", "null"):
                        return json.dumps({"error": "Onboarding jobs require one stored credential."}), 400, {"Content-Type": "application/json"}
                    fields["execution_context"] = "onboarding_local_network"
                    fields["use_service_account"] = 0
                else:
                    component_error = _validate_components_for_context(next_components or [], str(effective_context))
                    if component_error:
                        return json.dumps({"error": component_error}), 400, {"Content-Type": "application/json"}
                effective_enabled = int(fields.get("enabled", current_row[1] or 0))
                credential_warning = None if is_workflow_job else self._credential_reset_warning(effective_credential_id)
                if credential_warning and effective_enabled:
                    fields["enabled"] = 0
                sets = ", ".join([f"{k}=?" for k in fields.keys()])
                params = list(fields.values()) + [_now_ts(), job_id]
                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute(f"UPDATE scheduled_jobs SET {sets}, updated_at=? WHERE id=?", params)
                    if cur.rowcount == 0:
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    conn.commit()
                    cur.execute(
                        """
                        SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                               duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at, job_kind
                        FROM scheduled_jobs WHERE id=?
                        """,
                        (job_id,),
                    )
                    row = cur.fetchone()
                finally:
                    conn.close()
                if not row:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                payload = {"job": _job_row_to_dict(row)}
                if credential_warning:
                    payload.update(credential_warning)
                return json.dumps(payload), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/toggle", methods=["POST"])
        def api_scheduled_jobs_toggle(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            data = self._json_body()
            enabled = int(bool(data.get("enabled", True)))
            try:
                conn = self._conn()
                cur = conn.cursor()
                if enabled:
                    cur.execute("SELECT credential_id, targets_json FROM scheduled_jobs WHERE id=?", (job_id,))
                    existing_row = cur.fetchone()
                    if not existing_row:
                        conn.close()
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if not self._job_visible_to_user(user, existing_row[1]):
                        conn.close()
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    credential_warning = self._credential_reset_warning(existing_row[0])
                    if credential_warning:
                        conn.close()
                        return (
                            json.dumps(
                                {
                                    "error": credential_warning["warning_code"],
                                    "message": credential_warning["warning_message"],
                                }
                            ),
                            409,
                            {"Content-Type": "application/json"},
                        )
                else:
                    cur.execute("SELECT targets_json FROM scheduled_jobs WHERE id=?", (job_id,))
                    existing_row = cur.fetchone()
                    if not existing_row:
                        conn.close()
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if not self._job_visible_to_user(user, existing_row[0]):
                        conn.close()
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                cur.execute("UPDATE scheduled_jobs SET enabled=?, updated_at=? WHERE id=?", (enabled, _now_ts(), job_id))
                if cur.rowcount == 0:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                conn.commit()
                cur.execute(
                    "SELECT id, name, components_json, targets_json, schedule_type, start_ts, duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at, job_kind FROM scheduled_jobs WHERE id=?",
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["DELETE"])
        def api_scheduled_jobs_delete(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute("SELECT targets_json FROM scheduled_jobs WHERE id=?", (job_id,))
                existing_row = cur.fetchone()
                if not existing_row:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                if not self._job_visible_to_user(user, existing_row[0]):
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                cur.execute("DELETE FROM scheduled_jobs WHERE id=?", (job_id,))
                deleted = cur.rowcount
                conn.commit()
                conn.close()
                if deleted == 0:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                return json.dumps({"status": "ok"}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/runs", methods=["GET"])
        def api_scheduled_job_runs(job_id: int):
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                from flask import request
                days = request.args.get('days', default=str(self.RETENTION_DAYS))
                try:
                    days = max(1, int(days))
                except Exception:
                    days = self.RETENTION_DAYS
                cutoff = _now_ts() - (days * 86400)

                conn = self._conn()
                cur = conn.cursor()
                cur.execute("SELECT targets_json FROM scheduled_jobs WHERE id=?", (job_id,))
                visibility_row = cur.fetchone()
                if not visibility_row:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                if not self._job_visible_to_user(user, visibility_row[0]):
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                cur.execute(
                    """
                    SELECT
                        id,
                        scheduled_ts,
                        started_ts,
                        finished_ts,
                        status,
                        error,
                        skip_reason,
                        target_hostname,
                        shared_execution,
                        component_index,
                        component_kind,
                        component_name,
                        workflow_run_id
                    FROM scheduled_job_runs
                    WHERE job_id=? AND COALESCE(finished_ts, started_ts, scheduled_ts, 0) >= ?
                    ORDER BY COALESCE(started_ts, scheduled_ts, 0) DESC, id DESC
                    LIMIT 500
                    """,
                    (job_id, cutoff)
                )
                rows = cur.fetchall()
                conn.close()
                runs = [
                    {
                        "id": r[0],
                        "scheduled_ts": r[1],
                        "started_ts": r[2],
                        "finished_ts": r[3],
                        "status": r[4] or "",
                        "error": r[5] or "",
                        "skip_reason": r[6] or "",
                        "target_hostname": r[7] or "",
                        "shared_execution": bool(r[8] or 0),
                        "component_index": r[9],
                        "component_kind": r[10] or "",
                        "component_name": r[11] or "",
                        "workflow_run_id": r[12],
                    }
                    for r in rows
                ]
                return json.dumps({"runs": runs}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/devices", methods=["GET"])
        def api_scheduled_job_devices(job_id: int):
            """Return per-target status for the latest occurrence (or specified via ?occurrence=epoch)."""
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                from flask import request
                occurrence = request.args.get('occurrence')
                occ = int(occurrence) if occurrence else None

                conn = self._conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        "SELECT targets_json, execution_context, job_kind FROM scheduled_jobs WHERE id=?",
                        (job_id,)
                    )
                    row = cur.fetchone()
                    if not row:
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if not self._job_visible_to_user(user, row[0]):
                        return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                    if occ is None:
                        cur.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
                        occ_row = cur.fetchone()
                        occ = int(occ_row[0]) if occ_row and occ_row[0] else None
                finally:
                    conn.close()
                try:
                    raw_targets = json.loads(row[0] or "[]")
                except Exception:
                    raw_targets = []
                execution_context_value = str(row[1] or "").strip().lower()
                job_kind_value = _normalize_job_kind(row[2] if len(row) > 2 else JOB_KIND_AUTOMATION)
                if job_kind_value == JOB_KIND_ONBOARDING:
                    onboarding_rows = self._load_onboarding_target_rows(int(job_id), int(occ)) if occ is not None else []
                    out = [
                        {
                            "hostname": target_row.get("target_hostname") or target_row.get("target_address") or "",
                            "online": False,
                            "site_id": target_row.get("site_id"),
                            "site_name": "",
                            "site": "",
                            "inventory_hostname": target_row.get("target_hostname") or "",
                            "wireguard_peer_ip": "",
                            "resolved_connection": "local_network_onboarding",
                            "resolution_status": target_row.get("status") or "",
                            "resolution_reason": target_row.get("detail") or "",
                            "ran_on": target_row.get("finished_at") or target_row.get("updated_at"),
                            "job_status": target_row.get("status") or ONBOARDING_STATUS_PENDING,
                            "has_stdout": bool(target_row.get("stdout_snippet")),
                            "has_stderr": bool(target_row.get("stderr_snippet")),
                            "target_input": target_row.get("target_input") or "",
                            "ssh_port": target_row.get("ssh_port") or DEFAULT_ONBOARDING_SSH_PORT,
                            "detail": target_row.get("detail") or "",
                            "stdout_snippet": target_row.get("stdout_snippet") or "",
                            "stderr_snippet": target_row.get("stderr_snippet") or "",
                            "approval_reference": target_row.get("approval_reference") or "",
                            "activities": [],
                        }
                        for target_row in onboarding_rows
                    ]
                    return json.dumps({"occurrence": occ, "devices": out, "job_kind": JOB_KIND_ONBOARDING}), 200, {"Content-Type": "application/json"}
                include_filters = self._targets_include_filters(raw_targets)
                device_inventory_cache = None
                if include_filters:
                    try:
                        device_inventory_cache = self._filter_matcher.fetch_devices()
                    except Exception:
                        device_inventory_cache = []
                try:
                    targets, target_meta = self._filter_matcher.resolve_target_entries(
                        raw_targets,
                        devices=device_inventory_cache if include_filters else None,
                    )
                except Exception as exc:
                    self._log_event(
                        "failed to resolve targets for devices endpoint",
                        job_id=job_id,
                        level="ERROR",
                        extra={"error": str(exc)},
                    )
                    targets = [str(t) for t in raw_targets if isinstance(t, (str, int))]
                    target_meta = {"resolved_targets": []}

                occurrence_runs: List[Dict[str, Any]] = []
                occurrence_target_rows: List[Dict[str, Any]] = []
                aggregated_devices: Dict[str, Dict[str, Any]] = {}
                run_ids: List[int] = []
                if occ is not None:
                    try:
                        occurrence_runs = self._load_occurrence_runs(int(job_id), int(occ))
                        occurrence_target_rows = self._load_occurrence_target_rows(int(job_id), int(occ))
                        aggregated_devices, _aggregated_counts, _has_no_targets_skip = self._aggregate_occurrence_targets(
                            occurrence_runs,
                            occurrence_target_rows,
                        )
                        run_ids = sorted({int(run.get("id") or 0) for run in occurrence_runs if int(run.get("id") or 0)})
                    except Exception as exc:
                        self._log_event(
                            "failed to load scheduled job device snapshot",
                            job_id=job_id,
                            level="ERROR",
                            extra={"occurrence": occ, "error": str(exc)},
                        )

                activities_by_run: Dict[int, List[Dict[str, Any]]] = {}
                if run_ids:
                    try:
                        placeholders = ",".join(["?"] * len(run_ids))
                        conn = self._conn()
                        try:
                            cur = conn.cursor()
                            cur.execute(
                                f"""
                                SELECT
                                    s.run_id,
                                    s.activity_id,
                                    s.component_kind,
                                    s.script_type,
                                    s.component_path,
                                    s.component_name,
                                    COALESCE(LENGTH(h.stdout), 0),
                                    COALESCE(LENGTH(h.stderr), 0)
                                FROM scheduled_job_run_activity s
                                LEFT JOIN activity_history h ON h.id = s.activity_id
                                WHERE s.run_id IN ({placeholders})
                                """,
                                run_ids,
                            )
                            activity_rows = cur.fetchall()
                        finally:
                            conn.close()
                        for rid, act_id, kind, stype, path, name, so_len, se_len in activity_rows:
                            rid = int(rid)
                            entry = {
                                "activity_id": int(act_id),
                                "component_kind": kind or "",
                                "script_type": stype or "",
                                "component_path": path or "",
                                "component_name": name or "",
                                "has_stdout": bool(so_len),
                                "has_stderr": bool(se_len),
                            }
                            activities_by_run.setdefault(rid, []).append(entry)
                    except Exception as exc:
                        self._log_event(
                            "failed to load scheduled job activity links",
                            job_id=job_id,
                            level="ERROR",
                            extra={"occurrence": occ, "error": str(exc)},
                        )

                # Online snapshot
                online = set()
                try:
                    if callable(self._online_lookup):
                        online = {_normalize_host_key(host) for host in (self._online_lookup() or [])}
                except Exception:
                    online = set()

                out = []
                effective_records: List[Dict[str, Any]] = []
                if aggregated_devices:
                    effective_records = list(aggregated_devices.values())
                else:
                    for target in self._resolved_targets_from_meta(target_meta):
                        hostname = str(target.get("hostname") or "").strip()
                        if not hostname:
                            continue
                        effective_records.append(
                            {
                                "hostname": hostname,
                                "site_id": target.get("site_id"),
                                "site_name": str(target.get("site_name") or "").strip(),
                                "inventory_hostname": _inventory_hostname_for_target(
                                    hostname,
                                    site_name=target.get("site_name"),
                                    site_id=target.get("site_id"),
                                    connection=execution_context_value,
                                ),
                                "resolution_status": "",
                                "resolution_reason": "",
                                "status": RUN_STATUS_PENDING,
                                "started_ts": None,
                                "finished_ts": None,
                                "run_ids": [],
                            }
                        )
                    if not effective_records:
                        for host in targets:
                            display_host = str(host or "").strip()
                            if not display_host:
                                continue
                            effective_records.append(
                                {
                                    "hostname": display_host,
                                    "site_id": None,
                                    "site_name": "",
                                    "inventory_hostname": "",
                                    "resolution_status": "",
                                    "resolution_reason": "",
                                    "status": RUN_STATUS_PENDING,
                                    "started_ts": None,
                                    "finished_ts": None,
                                    "run_ids": [],
                                }
                            )

                for rec in effective_records:
                    host_key = _normalize_host_key(rec.get("hostname"))
                    seen_activity_ids = set()
                    activities: List[Dict[str, Any]] = []
                    for run_id in rec.get("run_ids") or []:
                        for activity in activities_by_run.get(int(run_id or 0), []):
                            activity_id = int(activity.get("activity_id") or 0)
                            dedupe_key = activity_id if activity_id else f"{activity.get('component_path')}::{activity.get('component_name')}"
                            if dedupe_key in seen_activity_ids:
                                continue
                            seen_activity_ids.add(dedupe_key)
                            activities.append(activity)
                    job_status = rec.get("status") or RUN_STATUS_PENDING
                    has_stdout = any(a.get("has_stdout") for a in activities)
                    has_stderr = any(a.get("has_stderr") for a in activities)
                    out.append({
                        "hostname": rec.get("hostname") or "",
                        "online": host_key in online,
                        "site_id": rec.get("site_id"),
                        "site_name": rec.get("site_name") or "",
                        "site": rec.get("site_name") or "",
                        "inventory_hostname": rec.get("inventory_hostname") or "",
                        "wireguard_peer_ip": rec.get("wireguard_peer_ip") or "",
                        "resolved_connection": rec.get("resolved_connection") or "",
                        "resolution_status": rec.get("resolution_status") or "",
                        "resolution_reason": rec.get("resolution_reason") or "",
                        "ran_on": rec.get("finished_ts") or rec.get("started_ts"),
                        "job_status": job_status,
                        "has_stdout": has_stdout,
                        "has_stderr": has_stderr,
                        "activities": activities,
                    })

                return json.dumps({"occurrence": occ, "devices": out}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/runs", methods=["DELETE"])
        def api_scheduled_job_runs_clear(job_id: int):
            """Clear all historical runs for a job except the most recent occurrence.

            We keep all rows that belong to the latest occurrence (by scheduled_ts)
            and delete everything older. If there is no occurrence, no-op.
            """
            requirement = self._require_user()
            if requirement:
                return json.dumps(requirement[0]), requirement[1], {"Content-Type": "application/json"}
            user = self._current_user() or {}
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute("SELECT targets_json FROM scheduled_jobs WHERE id=?", (job_id,))
                visibility_row = cur.fetchone()
                if not visibility_row:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                if not self._job_visible_to_user(user, visibility_row[0]):
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                # Determine latest occurrence for this job
                cur.execute(
                    "SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?",
                    (job_id,)
                )
                row = cur.fetchone()
                latest = int(row[0]) if row and row[0] is not None else None

                if latest is None:
                    # Nothing to clear
                    conn.close()
                    return json.dumps({"status": "ok", "cleared": 0}), 200, {"Content-Type": "application/json"}

                # Delete all runs for older occurrences
                cur.execute(
                    "SELECT id FROM scheduled_job_runs WHERE job_id=? AND COALESCE(scheduled_ts, 0) < ?",
                    (job_id, latest),
                )
                old_run_ids = [int(run_row[0]) for run_row in cur.fetchall()]
                if old_run_ids:
                    placeholders = ",".join(["?"] * len(old_run_ids))
                    cur.execute(f"DELETE FROM scheduled_job_run_activity WHERE run_id IN ({placeholders})", tuple(old_run_ids))
                    cur.execute(f"DELETE FROM scheduled_job_run_targets WHERE run_id IN ({placeholders})", tuple(old_run_ids))
                    cur.execute(f"DELETE FROM scheduled_job_onboarding_targets WHERE run_id IN ({placeholders})", tuple(old_run_ids))
                    cur.execute(f"DELETE FROM scheduled_job_runs WHERE id IN ({placeholders})", tuple(old_run_ids))
                    cleared = cur.rowcount or 0
                else:
                    cleared = 0
                conn.commit()
                conn.close()
                return json.dumps({"status": "ok", "cleared": int(cleared), "kept_occurrence": latest}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

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
                    if _normalize_job_kind(row[3]) != JOB_KIND_ONBOARDING:
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
                occurrence_ts = _now_minute()
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
                    if _normalize_job_kind(row[1]) != JOB_KIND_ONBOARDING:
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

    # ---------- Request helpers ----------
    def _json_body(self) -> Dict[str, Any]:
        try:
            from flask import request
            return request.get_json(silent=True) or {}
        except Exception:
            return {}


def register(
    app,
    socketio,
    db_conn_factory: Callable[[], sqlite3.Connection],
    *,
    script_signer=None,
    service_logger: Optional[Callable[[str, str, Optional[str]], None]] = None,
    assembly_runtime: Optional[AssemblyRuntimeService] = None,
) -> JobScheduler:
    """Factory to create and return a JobScheduler instance."""
    return JobScheduler(
        app,
        socketio,
        db_conn_factory,
        script_signer=script_signer,
        service_logger=service_logger,
        assembly_runtime=assembly_runtime,
    )


def set_online_lookup(scheduler: JobScheduler, fn: Callable[[], List[str]]):
    scheduler._online_lookup = fn


def set_host_service_emitter(scheduler: JobScheduler, fn: Callable[[str, str, str, Any], bool]):
    scheduler._emit_host_service_event = fn


def set_server_ansible_runner(scheduler: JobScheduler, fn: Callable[..., str]):
    scheduler._server_ansible_runner = fn


def set_credential_fetcher(scheduler: JobScheduler, fn: Callable[[int], Optional[Dict[str, Any]]]):
    scheduler._credential_fetcher = fn


def set_public_base_url_lookup(scheduler: JobScheduler, fn: Callable[[], str]):
    scheduler._public_base_url_lookup = fn


def set_vpn_session_lookup(scheduler: JobScheduler, fn: Callable[[], Dict[str, Dict[str, Any]]]):
    scheduler._vpn_session_lookup = fn


def set_vpn_session_prepare(
    scheduler: JobScheduler,
    fn: Callable[[Sequence[str], Optional[Sequence[int]]], Dict[str, Dict[str, Any]]],
):
    scheduler._vpn_session_prepare = fn


def set_workflow_run_launcher(scheduler: JobScheduler, fn: Callable[..., Dict[str, Any]]):
    scheduler._workflow_run_launcher = fn


def set_workflow_document_validator(scheduler: JobScheduler, fn: Callable[..., List[str]]):
    scheduler._workflow_document_validator = fn
