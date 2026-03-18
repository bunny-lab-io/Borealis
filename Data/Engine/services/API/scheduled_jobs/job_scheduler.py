# ======================================================
# Data\Engine\services\API\scheduled_jobs\job_scheduler.py
# Description: Engine-native scheduled job management service that
#              provides CRUD endpoints, background execution, and run
#              tracking without relying on the legacy server module.
# ======================================================

"""Engine scheduled job orchestration and API bindings."""

from __future__ import annotations

import base64
import json
import logging
import os
import re
import time
import uuid
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Sequence, Tuple

from ...assemblies.service import AssemblyRuntimeService
from ....db import dbapi as sqlite3
from ...filters.matcher import DeviceFilterMatcher

_WINRM_USERNAME_VAR = "__borealis_winrm_username"
_WINRM_PASSWORD_VAR = "__borealis_winrm_password"
_WINRM_TRANSPORT_VAR = "__borealis_winrm_transport"

RUN_STATUS_PENDING = "Pending"
RUN_STATUS_RUNNING = "Running"
RUN_STATUS_SUCCESS = "Success"
RUN_STATUS_FAILED = "Failed"
RUN_STATUS_EXPIRED = "Expired"
RUN_STATUS_TIMED_OUT = "Timed Out"
RUN_STATUS_SKIPPED = "Skipped"
SKIP_REASON_NO_TARGETS = "no_devices_targeted"
TERMINAL_RUN_STATUSES = {
    RUN_STATUS_SUCCESS,
    RUN_STATUS_FAILED,
    RUN_STATUS_EXPIRED,
    RUN_STATUS_TIMED_OUT,
    RUN_STATUS_SKIPPED,
}


def _now_ts() -> int:
    return int(time.time())


def _env_string(value: Any) -> str:
    if isinstance(value, bool):
        return "True" if value else "False"
    if value is None:
        return ""
    return str(value)


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

    def _generated_ansible_root(self) -> Path:
        project_root_raw = os.environ.get("BOREALIS_PROJECT_ROOT")
        if project_root_raw:
            project_root = Path(project_root_raw).expanduser().resolve()
        else:
            project_root = Path(__file__).resolve().parents[5]
        return project_root / "Engine" / "Generated" / "Ansible_Playbooks"

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
            stage_relative = rel_norm
            if stage_relative.lower().startswith("ansible_playbooks/"):
                stage_relative = stage_relative.split("/", 1)[1] if "/" in stage_relative else ""
            stage_parts = [part for part in stage_relative.split("/") if part not in {"", ".", ".."}]
            if not stage_parts:
                stage_parts = [f"{assembly_guid or uuid.uuid4().hex}.yml"]
            elif "." not in stage_parts[-1]:
                stage_parts[-1] = f"{stage_parts[-1]}.yml"
            generated_root = self._generated_ansible_root().resolve()
            abs_path = (generated_root.joinpath(*stage_parts)).resolve()
            if not str(abs_path).startswith(str(generated_root)):
                raise RuntimeError("Resolved Ansible staging path escapes the generated runtime root.")
            friendly_name = (doc.get("name") or "").strip()
            if not friendly_name:
                friendly_name = os.path.basename(rel_norm) if rel_norm else f"Job-{scheduled_job_id}"
            if not friendly_name:
                friendly_name = f"Job-{scheduled_job_id}"
            content = doc.get("script") or ""
            normalized_script = (content or "").replace("\r\n", "\n")
            script_bytes = normalized_script.encode("utf-8")
            encoded_content = base64.b64encode(script_bytes).decode("ascii") if script_bytes or normalized_script == "" else ""
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
            variables = doc.get("variables") or []
            files = doc.get("files") or []
            run_mode_norm = (run_mode or "system").strip().lower()
            server_run = run_mode_norm == "ssh"
            agent_winrm = run_mode_norm == "winrm"

            if agent_winrm and not use_service_account:
                if not credential_id:
                    raise RuntimeError("WinRM execution requires a credential_id")
                if not callable(self._credential_fetcher):
                    raise RuntimeError("Credential fetcher is not configured")
                cred_detail = self._credential_fetcher(int(credential_id))
                if not cred_detail:
                    raise RuntimeError("Credential not found")
                try:
                    overrides_map = _inject_winrm_credential(overrides_map, cred_detail)
                finally:
                    try:
                        cred_detail.clear()  # type: ignore[attr-defined]
                    except Exception:
                        pass

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
                act_id = cur.lastrowid
                conn.commit()
            finally:
                conn.close()

            if server_run and not os.path.isfile(abs_path):
                try:
                    abs_path.parent.mkdir(parents=True, exist_ok=True)
                    with abs_path.open("w", encoding="utf-8") as fh:
                        fh.write(normalized_script)
                except Exception as exc:
                    raise RuntimeError(f"Unable to stage Ansible playbook for server execution: {exc}")

            if server_run:
                if not credential_id:
                    raise RuntimeError("Remote execution requires a credential_id")
                if not callable(self._server_ansible_runner):
                    raise RuntimeError("Server-side Ansible runner is not configured")
                try:
                    self._server_ansible_runner(
                        hostname=str(hostname),
                        playbook_abs_path=str(abs_path),
                        playbook_rel_path=rel_norm,
                        playbook_name=friendly_name,
                        credential_id=int(credential_id),
                        variable_values=overrides_map,
                        source="scheduled_job",
                        activity_id=act_id,
                        scheduled_job_id=scheduled_job_id,
                        scheduled_run_id=scheduled_run_row_id,
                        scheduled_job_run_row_id=scheduled_run_row_id,
                    )
                    self._log_event(
                        "queued server ansible execution",
                        job_id=int(scheduled_job_id),
                        host=str(hostname),
                        run_id=scheduled_run_row_id,
                        extra={
                            "run_mode": run_mode_norm,
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
            else:
                payload = {
                    "run_id": uuid.uuid4().hex,
                    "target_hostname": str(hostname),
                    "playbook_name": friendly_name,
                    "playbook_content": encoded_content,
                    "playbook_encoding": "base64",
                    "activity_job_id": act_id,
                    "scheduled_job_id": int(scheduled_job_id),
                    "scheduled_run_id": int(scheduled_run_row_id),
                    "connection": "winrm",
                    "variables": variables,
                    "files": files,
                    "variable_values": overrides_map,
                    "context": {
                        "scheduled_job_id": int(scheduled_job_id),
                        "scheduled_job_run_id": int(scheduled_run_row_id),
                    },
                }
                try:
                    self.socketio.emit("ansible_playbook_run", payload)
                    self._log_event(
                        "emitted ansible payload",
                        job_id=int(scheduled_job_id),
                        host=str(hostname),
                        run_id=scheduled_run_row_id,
                        extra={
                            "run_mode": run_mode_norm,
                            "assembly_source": assembly_source,
                            "assembly_guid": assembly_guid or "",
                        },
                    )
                except Exception:
                    pass
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
            stype = (doc.get("type") or "powershell").lower()
            # For now, only PowerShell is supported by agents for scheduled jobs
            if stype != "powershell":
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
            content = _rewrite_powershell_script(content, literal_lookup)
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
            cur = conn.cursor()
            try:
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        str(hostname),
                        path_norm,
                        friendly_name,
                        stype,
                        now,
                        "Running",
                        "",
                        "",
                    ),
                )
                act_id = cur.lastrowid
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
                        fail_cur = fail_conn.cursor()
                        fail_cur.execute(
                            "UPDATE activity_history SET status=?, stderr=? WHERE id=?",
                            (RUN_STATUS_FAILED, failure_text, int(act_id)),
                        )
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
                CREATE TABLE IF NOT EXISTS scheduled_job_run_targets (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    run_id INTEGER NOT NULL,
                    device_guid TEXT,
                    hostname TEXT NOT NULL,
                    site_id INTEGER,
                    resolved_from_filter_id INTEGER,
                    created_at INTEGER NOT NULL,
                    FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_run ON scheduled_job_run_targets(run_id)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_filter ON scheduled_job_run_targets(resolved_from_filter_id)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_host ON scheduled_job_run_targets(hostname)")
        except Exception:
            pass
        conn.commit()
        conn.close()

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
                SELECT id, target_hostname, scheduled_ts, started_ts, finished_ts, status, error, skip_reason
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
    ) -> None:
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

            remote_requires_cred = (run_mode == "ssh") or (run_mode == "winrm" and not use_service_account)
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
                return

            activity_links: List[Dict[str, Any]] = []
            dispatch_errors: List[str] = []

            for component in script_components:
                try:
                    link = self._dispatch_script(job_id, run_row_id, scheduled_ts, hostname, component, run_mode)
                except Exception as exc:
                    dispatch_errors.append(str(exc))
                    link = None
                if link and link.get("activity_id"):
                    activity_links.append(
                        {
                            "run_id": int(run_row_id),
                            "activity_id": int(link["activity_id"]),
                            "component_kind": link.get("component_kind") or "script",
                            "script_type": link.get("script_type") or "powershell",
                            "component_path": link.get("component_path") or "",
                            "component_name": link.get("component_name") or "",
                        }
                    )

            for component in ansible_components:
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
                if link and link.get("activity_id"):
                    activity_links.append(
                        {
                            "run_id": int(run_row_id),
                            "activity_id": int(link["activity_id"]),
                            "component_kind": link.get("component_kind") or "ansible",
                            "script_type": link.get("script_type") or "ansible",
                            "component_path": link.get("component_path") or "",
                            "component_name": link.get("component_name") or "",
                        }
                    )

            if activity_links:
                for link in activity_links:
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
                            int(link["run_id"]),
                            int(link["activity_id"]),
                            link.get("component_kind") or "",
                            link.get("script_type") or "",
                            link.get("component_path") or "",
                            link.get("component_name") or "",
                            ts_now,
                        ),
                    )
                conn.commit()
                return

            error_text = dispatch_errors[0] if dispatch_errors else "No runnable activities were dispatched"
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       finished_ts=?,
                       updated_at=?,
                       error=?
                 WHERE id=?
                """,
                (RUN_STATUS_FAILED, ts_now, ts_now, str(error_text)[:512], int(run_row_id)),
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
                SELECT r.id, r.job_id, r.target_hostname, r.started_ts, j.expiration
                  FROM scheduled_job_runs r
                  JOIN scheduled_jobs j ON j.id = r.job_id
                 WHERE r.status = ?
                """,
                (RUN_STATUS_RUNNING,),
            )
            rows = cur.fetchall()
            for rid, row_job_id, row_host, started_ts, expiration in rows:
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
                       execution_context, credential_id, use_service_account, created_at
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

                script_components: List[Dict[str, Any]] = []
                ansible_components: List[Dict[str, Any]] = []
                for component in components:
                    if not isinstance(component, dict):
                        continue
                    component_type = str(component.get("type") or "").strip().lower()
                    if component_type == "script":
                        script_components.append(dict(component))
                    elif component_type == "ansible":
                        ansible_components.append(dict(component))

                if not script_components and not ansible_components:
                    continue

                run_mode = (execution_context or "system").strip().lower()
                job_use_service_account = bool(use_service_account_flag) if run_mode == "winrm" else False
                try:
                    job_credential_id = int(credential_id) if credential_id is not None else None
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
                    host = str(run.get("target_hostname") or "").strip()
                    if not host:
                        continue
                    if host in online:
                        self._dispatch_run_activities(
                            job_id=int(job_id),
                            run_row_id=int(run["id"]),
                            scheduled_ts=int(occurrence_ts),
                            hostname=host,
                            run_mode=run_mode,
                            script_components=script_components,
                            ansible_components=ansible_components,
                            credential_id=job_credential_id,
                            use_service_account=job_use_service_account,
                        )
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
                        SELECT id, target_hostname, scheduled_ts, started_ts, finished_ts, status, error, skip_reason
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
                        }
                        for row in c.fetchall()
                    ]
                    _aggregated_by_host, aggregated_counts, has_no_targets_skip = _aggregate_occurrence_runs(occurrence_rows)
                    if total_targets <= 0:
                        total_targets = int(aggregated_counts.get("total_targets") or 0)
                    result_counts["total_targets"] = total_targets
                    result_counts["pending"] = int(aggregated_counts.get("pending") or 0)
                    result_counts["running"] = int(aggregated_counts.get("running") or 0)
                    result_counts["success"] = int(aggregated_counts.get("success") or 0)
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
                    elif result_counts["expired"]:
                        summary_status = RUN_STATUS_EXPIRED
                    elif result_counts["pending"]:
                        summary_status = RUN_STATUS_PENDING
                    elif result_counts["success"]:
                        summary_status = RUN_STATUS_SUCCESS
                    elif result_counts["skipped"] or has_no_targets_skip:
                        summary_status = "No Devices Targeted"
                    last_run_ts = int(max_occ)
                conn.close()
            except Exception:
                summary_status = None
                last_run_ts = None
                result_counts = {
                    "pending": len(base.get("targets") or []),
                    "running": 0,
                    "success": 0,
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
            return base

        def _normalize_targets_for_save(raw_targets: Any) -> List[Any]:
            normalized: List[Any] = []
            if not isinstance(raw_targets, list):
                raw_list = [raw_targets]
            else:
                raw_list = raw_targets
            seen_hosts: set[str] = set()
            seen_filters: set[int] = set()
            for entry in raw_list:
                if isinstance(entry, str):
                    host = entry.strip()
                    if not host:
                        continue
                    lowered = host.lower()
                    if lowered in seen_hosts:
                        continue
                    seen_hosts.add(lowered)
                    normalized.append(host)
                    continue
                if isinstance(entry, (int, float)):
                    host = str(entry).strip()
                    if not host:
                        continue
                    lowered = host.lower()
                    if lowered in seen_hosts:
                        continue
                    seen_hosts.add(lowered)
                    normalized.append(host)
                    continue
                if not isinstance(entry, dict):
                    continue
                kind = str(entry.get("kind") or entry.get("type") or "").strip().lower()
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
                if hostname:
                    host = str(hostname).strip()
                    if host:
                        lowered = host.lower()
                        if lowered in seen_hosts:
                            continue
                        seen_hosts.add(lowered)
                        normalized.append(host)
            return normalized

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

        @app.route("/api/scheduled_jobs", methods=["GET"])
        def api_scheduled_jobs_list():
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, credential_id,
                           use_service_account, enabled, created_at, updated_at
                    FROM scheduled_jobs
                    ORDER BY created_at DESC
                    """
                )
                rows = [ _job_row_to_dict(r) for r in cur.fetchall() ]
                conn.close()
                return json.dumps({"jobs": rows}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs", methods=["POST"])
        def api_scheduled_jobs_create():
            data = self._json_body()
            name = (data.get("name") or "").strip()
            components = data.get("components") or []
            targets = _normalize_targets_for_save(data.get("targets") or [])
            schedule_type = (data.get("schedule", {}).get("type") or data.get("schedule_type") or "immediately").strip().lower()
            start = data.get("schedule", {}).get("start") or data.get("start") or None
            start_ts = _parse_ts(start) if start else None
            duration_stop_enabled = int(bool((data.get("duration") or {}).get("stopAfterEnabled") or data.get("duration_stop_enabled")))
            expiration = (data.get("duration") or {}).get("expiration") or data.get("expiration") or "no_expire"
            execution_context = (data.get("execution_context") or "system").strip().lower()
            credential_id = data.get("credential_id")
            try:
                credential_id = int(credential_id) if credential_id is not None else None
            except Exception:
                credential_id = None
            use_service_account_raw = data.get("use_service_account")
            use_service_account = 1 if (execution_context == "winrm" and (use_service_account_raw is None or bool(use_service_account_raw))) else 0
            enabled = int(bool(data.get("enabled", True)))
            if not name or not components or not targets:
                return json.dumps({"error": "name, components, targets required"}), 400, {"Content-Type": "application/json"}
            target_error = _validate_targets_for_save(targets)
            if target_error:
                return json.dumps({"error": target_error}), 400, {"Content-Type": "application/json"}
            now = _now_ts()
            components_json = json.dumps(components)
            targets_json = json.dumps(targets)
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    INSERT INTO scheduled_jobs
                    (name, components_json, targets_json, schedule_type, start_ts, duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at)
                    VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
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
                               duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at
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
                               duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at
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
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["GET"])
        def api_scheduled_jobs_get(job_id: int):
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at
                    FROM scheduled_jobs WHERE id=?
                    """,
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                if not row:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["PUT"])
        def api_scheduled_jobs_update(job_id: int):
            data = self._json_body()
            fields: Dict[str, Any] = {}
            if "name" in data:
                fields["name"] = (data.get("name") or "").strip()
            if "components" in data:
                fields["components_json"] = json.dumps(data.get("components") or [])
            if "targets" in data:
                normalized_targets = _normalize_targets_for_save(data.get("targets") or [])
                if not normalized_targets:
                    return json.dumps({"error": "targets required"}), 400, {"Content-Type": "application/json"}
                target_error = _validate_targets_for_save(normalized_targets)
                if target_error:
                    return json.dumps({"error": target_error}), 400, {"Content-Type": "application/json"}
                fields["targets_json"] = json.dumps(normalized_targets)
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
                if exec_ctx_val != "winrm":
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
            if not fields:
                return json.dumps({"error": "no fields to update"}), 400, {"Content-Type": "application/json"}
            try:
                conn = self._conn()
                cur = conn.cursor()
                sets = ", ".join([f"{k}=?" for k in fields.keys()])
                params = list(fields.values()) + [_now_ts(), job_id]
                cur.execute(f"UPDATE scheduled_jobs SET {sets}, updated_at=? WHERE id=?", params)
                if cur.rowcount == 0:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                conn.commit()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at
                    FROM scheduled_jobs WHERE id=?
                    """,
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/toggle", methods=["POST"])
        def api_scheduled_jobs_toggle(job_id: int):
            data = self._json_body()
            enabled = int(bool(data.get("enabled", True)))
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute("UPDATE scheduled_jobs SET enabled=?, updated_at=? WHERE id=?", (enabled, _now_ts(), job_id))
                if cur.rowcount == 0:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                conn.commit()
                cur.execute(
                    "SELECT id, name, components_json, targets_json, schedule_type, start_ts, duration_stop_enabled, expiration, execution_context, credential_id, use_service_account, enabled, created_at, updated_at FROM scheduled_jobs WHERE id=?",
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["DELETE"])
        def api_scheduled_jobs_delete(job_id: int):
            try:
                conn = self._conn()
                cur = conn.cursor()
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
                cur.execute(
                    """
                    SELECT id, scheduled_ts, started_ts, finished_ts, status, error, skip_reason
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
                    }
                    for r in rows
                ]
                return json.dumps({"runs": runs}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/devices", methods=["GET"])
        def api_scheduled_job_devices(job_id: int):
            """Return per-target status for the latest occurrence (or specified via ?occurrence=epoch)."""
            try:
                from flask import request
                occurrence = request.args.get('occurrence')
                occ = int(occurrence) if occurrence else None

                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    "SELECT targets_json FROM scheduled_jobs WHERE id=?",
                    (job_id,)
                )
                row = cur.fetchone()
                if not row:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                try:
                    raw_targets = json.loads(row[0] or "[]")
                except Exception:
                    raw_targets = []
                try:
                    targets, target_meta = self._filter_matcher.resolve_target_entries(raw_targets)
                except Exception as exc:
                    self._log_event(
                        "failed to resolve targets for devices endpoint",
                        job_id=job_id,
                        level="ERROR",
                        extra={"error": str(exc)},
                    )
                    targets = [str(t) for t in raw_targets if isinstance(t, (str, int))]

                # Determine occurrence if not provided
                if occ is None:
                    cur.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
                    occ_row = cur.fetchone()
                    occ = int(occ_row[0]) if occ_row and occ_row[0] else None

                # Sites lookup
                site_by_host: Dict[str, str] = {}
                try:
                    cur.execute(
                        "SELECT device_hostname, sites.name FROM device_sites JOIN sites ON sites.id = device_sites.site_id"
                    )
                    for h, n in cur.fetchall():
                        site_by_host[_normalize_host_key(h)] = n or ""
                except Exception:
                    pass

                # Status per target for occurrence
                run_groups_by_host: Dict[str, List[Dict[str, Any]]] = {}
                run_ids: List[int] = []
                snapshot_targets: List[str] = []
                occurrence_has_snapshot = False
                if occ is not None:
                    try:
                        cur.execute(
                            """
                            SELECT DISTINCT t.hostname
                              FROM scheduled_job_run_targets t
                              JOIN scheduled_job_runs r ON r.id = t.run_id
                             WHERE r.job_id=? AND r.scheduled_ts=?
                            """,
                            (job_id, occ),
                        )
                        snapshot_targets = sorted(
                            [str(row[0]) for row in cur.fetchall() if str(row[0]).strip()],
                            key=lambda value: value.lower(),
                        )
                        cur.execute(
                            "SELECT id, target_hostname, status, started_ts, finished_ts, skip_reason FROM scheduled_job_runs WHERE job_id=? AND scheduled_ts=? ORDER BY id DESC",
                            (job_id, occ)
                        )
                        rows = cur.fetchall()
                        occurrence_has_snapshot = bool(rows) or bool(snapshot_targets)
                        for rid, h, st, st_ts, fin_ts, skip_reason in rows:
                            host_name = str(h or "").strip()
                            host_key = _normalize_host_key(host_name)
                            if not host_key:
                                continue
                            run_ids.append(int(rid))
                            run_groups_by_host.setdefault(host_key, []).append(
                                {
                                    "id": int(rid),
                                    "target_hostname": host_name,
                                    "status": st or "",
                                    "started_ts": st_ts,
                                    "finished_ts": fin_ts,
                                    "run_id": int(rid),
                                    "skip_reason": skip_reason or "",
                                }
                            )
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
                        for rid, act_id, kind, stype, path, name, so_len, se_len in cur.fetchall():
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

                aggregated_run_by_host, _aggregated_counts, _has_no_targets_skip = _aggregate_occurrence_runs(
                    [run for group in run_groups_by_host.values() for run in group]
                )
                activities_by_host: Dict[str, List[Dict[str, Any]]] = {}
                for host_key, grouped_runs in run_groups_by_host.items():
                    seen_activity_ids = set()
                    merged_activities: List[Dict[str, Any]] = []
                    for run in grouped_runs:
                        for activity in activities_by_run.get(int(run.get("run_id") or 0), []):
                            activity_id = int(activity.get("activity_id") or 0)
                            dedupe_key = activity_id if activity_id else f"{activity.get('component_path')}::{activity.get('component_name')}"
                            if dedupe_key in seen_activity_ids:
                                continue
                            seen_activity_ids.add(dedupe_key)
                            merged_activities.append(activity)
                    activities_by_host[host_key] = merged_activities

                conn.close()

                # Online snapshot
                online = set()
                try:
                    if callable(self._online_lookup):
                        online = {_normalize_host_key(host) for host in (self._online_lookup() or [])}
                except Exception:
                    online = set()

                out = []
                effective_targets = snapshot_targets if occurrence_has_snapshot else targets
                seen_hosts = set()
                normalized_targets: List[Tuple[str, str]] = []
                for host in effective_targets:
                    display_host = str(host or "").strip()
                    host_key = _normalize_host_key(display_host)
                    if not host_key or host_key in seen_hosts:
                        continue
                    seen_hosts.add(host_key)
                    normalized_targets.append((host_key, display_host))
                if not normalized_targets and aggregated_run_by_host:
                    for host_key, rec in aggregated_run_by_host.items():
                        normalized_targets.append((host_key, str(rec.get("target_hostname") or host_key)))
                for host_key, display_host in normalized_targets:
                    rec = aggregated_run_by_host.get(host_key, {})
                    job_status = rec.get("status") or RUN_STATUS_PENDING
                    if job_status == RUN_STATUS_SKIPPED and rec.get("skip_reason") == SKIP_REASON_NO_TARGETS:
                        job_status = "No Devices Targeted"
                    ran_on = rec.get("finished_ts") or rec.get("started_ts")
                    activities = activities_by_host.get(host_key, [])
                    has_stdout = any(a.get("has_stdout") for a in activities)
                    has_stderr = any(a.get("has_stderr") for a in activities)
                    out.append({
                        "hostname": display_host,
                        "online": host_key in online,
                        "site": site_by_host.get(host_key, ""),
                        "ran_on": ran_on,
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
            try:
                conn = self._conn()
                cur = conn.cursor()
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
                    cur.execute(f"DELETE FROM scheduled_job_runs WHERE id IN ({placeholders})", tuple(old_run_ids))
                    cleared = cur.rowcount or 0
                else:
                    cleared = 0
                conn.commit()
                conn.close()
                return json.dumps({"status": "ok", "cleared": int(cleared), "kept_occurrence": latest}), 200, {"Content-Type": "application/json"}
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
