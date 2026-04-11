# ======================================================
# Data\Engine\services\API\watchdogs\runtime.py
# Description: Engine-native watchdog policy storage, incident tracking,
#              remediation dispatch, and background evaluation loop.
#
# API Endpoints (if applicable): None
# ======================================================

"""Watchdog policy storage and runtime evaluation for the Borealis Engine."""

from __future__ import annotations

import base64
import json
import logging
import os
import re
import time
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from Data.Engine.auth.guid_utils import normalize_guid
from Data.Engine.db import dbapi as sqlite3

from ...ansible import EngineAnsibleRunner
from ...assemblies.service import AssemblyRuntimeService
from ...auth import UserSiteAccessManager
from ...filters.matcher import DeviceFilterMatcher
from ..assemblies.execution import (
    _SUPPORTED_AGENT_SCRIPT_TYPES,
    _load_assembly_document,
    _normalize_agent_script_type,
    _rewrite_script_for_dispatch,
    prepare_variable_context,
)
from ..devices.agent_role_health import normalize_agent_role_health
from ..devices.service_inventory import (
    mark_service_control_pending,
    normalize_device_services,
    normalize_service_action,
    serialize_device_services,
)
from ..scheduled_jobs.targets import normalize_targets_for_save
from ..workflows import management as workflows_management

VALID_WATCHDOG_SITE_MODES = {"global", "specific_sites", "global_exclusions"}
VALID_WATCHDOG_MATCH_MODES = {"all", "any"}
VALID_WATCHDOG_SEVERITIES = {"info", "warning", "error"}
VALID_WATCHDOG_RULE_TYPES = {
    "device_offline",
    "storage_usage_percent",
    "service_state",
    "agent_role_health",
    "software_presence_or_version",
    "agent_version_status",
}
VALID_WATCHDOG_ACTION_TYPES = {"notification", "service_control", "assembly", "do_nothing"}
VALID_INCIDENT_STATES = {"open", "suppressed", "resolved"}
VALID_INCIDENT_QUERY_STATES = VALID_INCIDENT_STATES | {"all"}
VALID_DEVICE_OVERRIDE_STATES = {"suppressed", "disabled"}
VALID_AGENT_VERSION_STATES = {"Up-to-Date", "Needs Updated"}
VALID_RULE_STATUSES = {"healthy", "recovering", "unhealthy", "pending", "unsupported", "unknown"}
VALID_SERVICE_EXPECTED_STATUSES = {"running", "stopped"}
VALID_SCRIPT_RUN_MODES = {"system", "currentuser"}
VALID_STORAGE_DRIVE_MODES = {"all", "specific"}

DEFAULT_WATCHDOG_CRITERIA = {"rules": [], "match_mode": "all"}
DEFAULT_WATCHDOG_ACTIONS = {"actions": []}
DEFAULT_WATCHDOG_EVALUATION_INTERVAL_SECONDS = 60
DEFAULT_WATCHDOG_COOLDOWN_SECONDS = 900
DEFAULT_WATCHDOG_AUTO_RESOLVE_SECONDS = 300
DEFAULT_WATCHDOG_MIN_CONSECUTIVE_MATCHES = 1
DEFAULT_WATCHDOG_BOOT_GRACE_SECONDS = 0
DEFAULT_DEVICE_OFFLINE_SECONDS = 300
DEFAULT_TELEMETRY_STALE_SECONDS = 900
MAX_RULES_PER_WATCHDOG = 24
EVALUATION_LOOP_INTERVAL_SECONDS = 30
ENGINE_LOCAL_ALIAS = "borealis-engine-01"

_GUID_RE = re.compile(r"^[0-9a-f-]+$", re.IGNORECASE)

try:
    from packaging.version import InvalidVersion, Version
except Exception:  # pragma: no cover - dependency fallback
    Version = None  # type: ignore[assignment]

    class InvalidVersion(Exception):
        pass


def _now_ts() -> int:
    return int(time.time())


def _safe_json_loads(raw: Any, default: Any) -> Any:
    if raw is None:
        return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    if isinstance(raw, (list, dict)):
        parsed = raw
    else:
        try:
            parsed = json.loads(str(raw))
        except Exception:
            return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default
    if isinstance(default, list) and isinstance(parsed, list):
        return parsed
    if isinstance(default, dict) and isinstance(parsed, dict):
        return parsed
    return json.loads(json.dumps(default)) if isinstance(default, (list, dict)) else default


def _safe_json_dumps(value: Any, default: Any = None) -> str:
    candidate = value if value is not None else default
    try:
        return json.dumps(candidate, ensure_ascii=True, sort_keys=True)
    except Exception:
        try:
            return json.dumps(default, ensure_ascii=True, sort_keys=True)
        except Exception:
            return "{}"


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _clean_single_line(value: Any) -> str:
    text = _clean_text(value)
    if not text:
        return ""
    return " ".join(segment.strip() for segment in text.splitlines() if segment.strip()).strip()


def _coerce_int(value: Any, default: int = 0) -> int:
    try:
        if value in (None, "", "null"):
            raise ValueError
        return int(float(value))
    except Exception:
        return default


def _coerce_optional_int(value: Any) -> Optional[int]:
    try:
        if value in (None, "", "null"):
            return None
        return int(float(value))
    except Exception:
        return None


def _coerce_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    text = _clean_text(value).lower()
    if text in {"1", "true", "yes", "on"}:
        return True
    if text in {"0", "false", "no", "off"}:
        return False
    return default


def _coerce_float(value: Any) -> Optional[float]:
    try:
        if value in (None, "", "null"):
            return None
        return float(value)
    except Exception:
        return None


def _normalize_site_ids(raw: Any) -> List[int]:
    values = raw if isinstance(raw, list) else ([] if raw is None else [raw])
    results: List[int] = []
    seen: set[int] = set()
    for item in values:
        if isinstance(item, dict):
            item = item.get("site_id") or item.get("id") or item.get("value")
        parsed = _coerce_optional_int(item)
        if parsed is None or parsed in seen:
            continue
        seen.add(parsed)
        results.append(parsed)
    return results


def _normalize_watchdog_site_mode(value: Any) -> str:
    normalized = _clean_text(value).lower() or "global"
    if normalized in VALID_WATCHDOG_SITE_MODES:
        return normalized
    return "global"


def _normalize_watchdog_match_mode(value: Any) -> str:
    normalized = _clean_text(value).lower() or "all"
    if normalized in VALID_WATCHDOG_MATCH_MODES:
        return normalized
    return "all"


def _normalize_watchdog_severity(value: Any) -> str:
    normalized = _clean_text(value).lower() or "warning"
    if normalized in VALID_WATCHDOG_SEVERITIES:
        return normalized
    return "warning"


def _normalize_timestamp_input(value: Any) -> Optional[int]:
    if value in (None, "", "null"):
        return None
    try:
        if isinstance(value, str) and value.strip().isdigit():
            return int(value.strip())
        if isinstance(value, (int, float)):
            return int(value)
        parsed = time.strptime(str(value).strip()[:19], "%Y-%m-%dT%H:%M:%S")
        return int(time.mktime(parsed))
    except Exception:
        return _coerce_optional_int(value)


def _normalize_rule_id(index: int, value: Any) -> str:
    text = _clean_text(value)
    if text:
        return text[:120]
    return f"rule-{index + 1}"


def _normalize_action_id(index: int, value: Any) -> str:
    text = _clean_text(value)
    if text:
        return text[:120]
    return f"action-{index + 1}"


def _normalize_version_compare(lhs: str, rhs: str) -> Optional[int]:
    if Version is None:
        return None
    lhs_text = _clean_text(lhs)
    rhs_text = _clean_text(rhs)
    if not lhs_text or not rhs_text:
        return None
    try:
        lhs_value = Version(lhs_text)
        rhs_value = Version(rhs_text)
    except InvalidVersion:
        return None
    if lhs_value < rhs_value:
        return -1
    if lhs_value > rhs_value:
        return 1
    return 0


def _normalize_storage_entries(raw: Any) -> List[Dict[str, Any]]:
    entries = raw if isinstance(raw, list) else []
    rows: List[Dict[str, Any]] = []
    for index, item in enumerate(entries):
        if not isinstance(item, dict):
            continue
        drive = _clean_text(item.get("drive") or item.get("label") or item.get("mount"))
        total = _coerce_float(item.get("total"))
        used = _coerce_float(item.get("used"))
        free = _coerce_float(item.get("free"))
        usage = _coerce_float(item.get("usage"))
        if usage is not None and usage <= 1:
            usage *= 100.0
        if usage is None and total and used is not None and total > 0:
            usage = max(0.0, min(100.0, (used / total) * 100.0))
        if usage is None and total and free is not None and total > 0:
            usage = max(0.0, min(100.0, ((total - free) / total) * 100.0))
        rows.append(
            {
                "id": f"{drive or 'drive'}-{index}",
                "drive": drive or f"Drive {index + 1}",
                "usage_percent": usage,
                "total": total,
                "used": used,
                "free": free,
                "disk_type": _clean_text(item.get("disk_type") or item.get("type") or "Fixed Disk"),
            }
        )
    return rows


def _normalize_targets(entries: Sequence[Any]) -> List[Any]:
    return normalize_targets_for_save(entries)


def _normalize_storage_drive_mode(value: Any, drive: Any = None) -> str:
    normalized = _clean_text(value).lower()
    if normalized in VALID_STORAGE_DRIVE_MODES:
        return normalized
    return "specific" if _clean_text(drive) else "all"


def _normalize_storage_drive_key(value: Any) -> str:
    text = _clean_text(value).lower()
    if not text:
        return ""
    text = text.replace("\\", "/")
    if text == "/":
        return text
    text = text.rstrip("/")
    if len(text) >= 2 and text[0].isalpha() and text[1] == ":":
        return text[0]
    if len(text) == 1 and text.isalpha():
        return text
    return text


def _normalize_rule(index: int, raw_rule: Any) -> Optional[Dict[str, Any]]:
    if not isinstance(raw_rule, dict):
        return None
    rule_type = _clean_text(raw_rule.get("type")).lower()
    if rule_type not in VALID_WATCHDOG_RULE_TYPES:
        return None
    rule_id = _normalize_rule_id(index, raw_rule.get("id"))
    base = {"id": rule_id, "type": rule_type}
    if rule_type == "device_offline":
        base["offline_after_seconds"] = max(
            60,
            _coerce_int(raw_rule.get("offline_after_seconds"), DEFAULT_DEVICE_OFFLINE_SECONDS),
        )
        return base
    if rule_type == "storage_usage_percent":
        threshold = _coerce_float(raw_rule.get("threshold"))
        base["threshold"] = 90.0 if threshold is None else max(1.0, min(100.0, threshold))
        base["drive"] = _clean_text(raw_rule.get("drive"))
        base["drive_mode"] = _normalize_storage_drive_mode(raw_rule.get("drive_mode"), raw_rule.get("drive"))
        return base
    if rule_type == "service_state":
        service_name = _clean_single_line(raw_rule.get("service_name") or raw_rule.get("name"))
        if not service_name:
            return None
        expected_status = _clean_text(raw_rule.get("expected_status") or "running").lower()
        if expected_status not in VALID_SERVICE_EXPECTED_STATUSES:
            expected_status = "running"
        base["service_name"] = service_name
        base["expected_status"] = expected_status
        return base
    if rule_type == "agent_role_health":
        statuses_raw = raw_rule.get("trigger_statuses") or raw_rule.get("statuses") or ["unhealthy"]
        statuses = []
        for item in statuses_raw if isinstance(statuses_raw, list) else [statuses_raw]:
            normalized = _clean_text(item).lower()
            if normalized in VALID_RULE_STATUSES and normalized not in statuses:
                statuses.append(normalized)
        if not statuses:
            statuses = ["unhealthy"]
        base["role_name"] = _clean_single_line(raw_rule.get("role_name") or raw_rule.get("role"))
        base["trigger_statuses"] = statuses
        return base
    if rule_type == "software_presence_or_version":
        software_name = _clean_single_line(raw_rule.get("software_name") or raw_rule.get("name"))
        if not software_name:
            return None
        version_operator = _clean_text(raw_rule.get("version_operator")).lower()
        if version_operator not in {"matches", "older_than", "newer_than"}:
            version_operator = ""
        base["software_name"] = software_name
        base["software_source"] = _clean_text(raw_rule.get("software_source") or raw_rule.get("source")).lower()
        base["require_present"] = _coerce_bool(raw_rule.get("require_present"), True)
        base["version_operator"] = version_operator
        base["version_value"] = _clean_single_line(raw_rule.get("version_value") or raw_rule.get("version"))
        return base
    if rule_type == "agent_version_status":
        expected = _clean_text(raw_rule.get("expected_status") or "Up-to-Date")
        if expected not in VALID_AGENT_VERSION_STATES:
            expected = "Up-to-Date"
        base["expected_status"] = expected
        return base
    return None


def _normalize_action(index: int, raw_action: Any) -> Optional[Dict[str, Any]]:
    if not isinstance(raw_action, dict):
        return None
    action_type = _clean_text(raw_action.get("type")).lower()
    if action_type not in VALID_WATCHDOG_ACTION_TYPES:
        return None
    base = {
        "id": _normalize_action_id(index, raw_action.get("id")),
        "type": action_type,
        "enabled": _coerce_bool(raw_action.get("enabled"), True),
    }
    if action_type == "notification":
        variant = _clean_text(raw_action.get("variant") or raw_action.get("severity") or "warning").lower()
        if variant not in VALID_WATCHDOG_SEVERITIES:
            variant = "warning"
        base["variant"] = variant
        base["title"] = _clean_single_line(raw_action.get("title"))
        base["message_template"] = _clean_text(raw_action.get("message_template"))
        return base
    if action_type == "do_nothing":
        return base
    if action_type == "service_control":
        action = normalize_service_action(raw_action.get("action"))
        service_name = _clean_single_line(raw_action.get("service_name") or raw_action.get("name"))
        if not action or not service_name:
            return None
        base["action"] = action
        base["service_name"] = service_name
        return base
    if action_type == "assembly":
        assembly_guid = _clean_text(raw_action.get("assembly_guid") or raw_action.get("assemblyGuid")).lower()
        if not assembly_guid:
            return None
        run_mode = _clean_text(raw_action.get("run_mode") or "system").lower()
        if run_mode not in VALID_SCRIPT_RUN_MODES:
            run_mode = "system"
        execution_context = _clean_text(raw_action.get("execution_context") or "local").lower() or "local"
        if execution_context not in {"local", "ssh", "winrm"}:
            execution_context = "local"
        base["assembly_guid"] = assembly_guid
        base["run_mode"] = run_mode
        base["execution_context"] = execution_context
        base["variable_values"] = (
            dict(raw_action.get("variable_values"))
            if isinstance(raw_action.get("variable_values"), dict)
            else {}
        )
        return base
    return None


def summarize_rule(rule: Mapping[str, Any]) -> str:
    rule_type = _clean_text(rule.get("type")).lower()
    if rule_type == "device_offline":
        seconds = _coerce_int(rule.get("offline_after_seconds"), DEFAULT_DEVICE_OFFLINE_SECONDS)
        return f"Device offline for at least {max(1, seconds // 60)} minute(s)"
    if rule_type == "storage_usage_percent":
        drive = _clean_text(rule.get("drive"))
        drive_mode = _normalize_storage_drive_mode(rule.get("drive_mode"), drive)
        prefix = f"{drive} " if drive_mode == "specific" and drive else "Any drive "
        return f"{prefix}usage at or above {int(_coerce_float(rule.get('threshold')) or 90)}%"
    if rule_type == "service_state":
        return f"Service {rule.get('service_name') or 'service'} not {rule.get('expected_status') or 'running'}"
    if rule_type == "agent_role_health":
        role_name = _clean_text(rule.get("role_name")) or "Any role"
        statuses = ", ".join(rule.get("trigger_statuses") or ["unhealthy"])
        return f"{role_name} health enters {statuses}"
    if rule_type == "software_presence_or_version":
        software_name = _clean_text(rule.get("software_name")) or "Software"
        version_operator = _clean_text(rule.get("version_operator"))
        version_value = _clean_text(rule.get("version_value"))
        if version_operator and version_value:
            return f"{software_name} version {version_operator.replace('_', ' ')} {version_value}"
        return f"{software_name} missing"
    if rule_type == "agent_version_status":
        return f"Agent version is not {_clean_text(rule.get('expected_status') or 'Up-to-Date')}"
    return "Watchdog rule"


def summarize_action(action: Mapping[str, Any]) -> str:
    action_type = _clean_text(action.get("type")).lower()
    if action_type == "notification":
        return "Engine toast notification"
    if action_type == "do_nothing":
        return "Incident only (no notification or remediation)"
    if action_type == "service_control":
        return f"{_clean_text(action.get('action')).capitalize()} service {_clean_text(action.get('service_name'))}"
    if action_type == "assembly":
        return "Run assembly remediation"
    return "Action"


def _build_rule_summaries(criteria: Mapping[str, Any]) -> List[str]:
    rules = criteria.get("rules") if isinstance(criteria, dict) else []
    return [summarize_rule(rule) for rule in rules if isinstance(rule, dict)]


def _build_action_summaries(actions: Mapping[str, Any]) -> List[str]:
    entries = actions.get("actions") if isinstance(actions, dict) else []
    return [summarize_action(action) for action in entries if isinstance(action, dict) and _coerce_bool(action.get("enabled"), True)]


class WatchdogRuntimeService:
    """Persist, evaluate, and remediate Borealis watchdog policies."""

    def __init__(
        self,
        *,
        db_conn_factory: Callable[[], sqlite3.Connection],
        socketio: Any,
        service_log: Optional[Callable[[str, str, Optional[str]], None]] = None,
        logger: Optional[logging.Logger] = None,
        assembly_cache: Any = None,
        app: Any = None,
        adapters: Any = None,
        context: Any = None,
        github_integration: Any = None,
    ) -> None:
        self._db_conn_factory = db_conn_factory
        self._socketio = socketio
        self._service_log = service_log
        self._logger = logger or logging.getLogger(__name__)
        self._site_access = UserSiteAccessManager(db_conn_factory, logger=self._logger)
        self._matcher = DeviceFilterMatcher(db_conn_factory=db_conn_factory)
        self._assembly_runtime = AssemblyRuntimeService(assembly_cache, logger=self._logger) if assembly_cache is not None else None
        self._ansible_runner = EngineAnsibleRunner(
            socketio=socketio,
            db_conn_factory=db_conn_factory,
            service_log=service_log,
            logger=self._logger.getChild("ansible"),
        )
        self._app = app
        self._adapters = adapters
        self._context = context
        self._github_integration = github_integration
        self._running = False
        self._repo_hash_cache: Dict[str, Any] = {"sha": "", "expires_at": 0}

    def start(self) -> None:
        if self._running:
            return
        self._running = True

        def _loop() -> None:
            while True:
                try:
                    self.evaluate_due_watchdogs()
                except Exception as exc:  # pragma: no cover - daemon guard
                    self._log(f"watchdog evaluator tick failed err={exc}", level="ERROR")
                time.sleep(EVALUATION_LOOP_INTERVAL_SECONDS)

        starter = getattr(self._socketio, "start_background_task", None)
        if callable(starter):
            try:
                starter(_loop)
                self._log("watchdog evaluator loop started via socketio task")
                return
            except Exception:
                self._logger.debug("Failed to start watchdog evaluator via socketio", exc_info=True)
        import threading

        threading.Thread(target=_loop, daemon=True).start()
        self._log("watchdog evaluator loop started via threading fallback")

    def _conn(self) -> sqlite3.Connection:
        conn = self._db_conn_factory()
        try:
            conn.row_factory = sqlite3.Row
        except Exception:
            pass
        return conn

    def _log(self, message: str, *, level: str = "INFO") -> None:
        if callable(self._service_log):
            try:
                self._service_log("watchdogs", message, level=level)
            except Exception:
                self._logger.debug("watchdogs service log write failed", exc_info=True)
        numeric_level = getattr(logging, level.upper(), logging.INFO)
        self._logger.log(numeric_level, "%s", message)

    def _current_target_repo_hash(self) -> str:
        now_ts = _now_ts()
        if _clean_text(self._repo_hash_cache.get("sha")) and _coerce_int(self._repo_hash_cache.get("expires_at"), 0) > now_ts:
            return str(self._repo_hash_cache["sha"])
        repo_name = (os.environ.get("BOREALIS_UPDATE_REPO") or "bunny-lab-io/Borealis").strip() or "bunny-lab-io/Borealis"
        branch_name = (os.environ.get("BOREALIS_UPDATE_BRANCH") or "main").strip() or "main"
        sha = ""
        try:
            if self._github_integration is not None:
                payload, status = self._github_integration.current_repo_hash(repo_name, branch_name)
                if status == 200 and isinstance(payload, dict):
                    sha = _clean_text(payload.get("sha")).lower()
        except Exception:
            sha = ""
        self._repo_hash_cache = {
            "sha": sha,
            "expires_at": now_ts + 60,
        }
        return sha

    def _emit_watchdog_refresh(self, *, hostname: str = "", watchdog_id: Optional[int] = None) -> None:
        if self._socketio is None:
            return
        payload = {"hostname": _clean_text(hostname), "watchdog_id": watchdog_id, "changed_at": _now_ts()}
        try:
            self._socketio.emit("watchdog_incidents_changed", payload)
            if payload["hostname"]:
                self._socketio.emit("device_watchdogs_changed", payload)
        except Exception:
            self._logger.debug("Failed to emit watchdog refresh payload", exc_info=True)

    def _emit_broadcast_notification(self, *, title: str, message: str, variant: str = "warning") -> None:
        if self._socketio is None:
            return
        try:
            self._socketio.emit(
                "borealis_notification",
                {
                    "id": f"watchdog-{_now_ts()}-{abs(hash((title, message))) % 1000}",
                    "title": title or "Watchdog Alert",
                    "message": message,
                    "variant": variant if variant in VALID_WATCHDOG_SEVERITIES else "warning",
                    "icon": "NotificationsActive",
                    "created_at": _now_ts(),
                },
            )
        except Exception:
            self._logger.debug("Failed to emit watchdog notification", exc_info=True)

    def _load_all_site_ids(self) -> set[int]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT id FROM sites")
            return {int(row[0]) for row in cur.fetchall() if row and row[0] is not None}
        finally:
            conn.close()

    def _load_site_name_map(self, site_ids: Iterable[int]) -> Dict[int, str]:
        normalized = sorted({int(site_id) for site_id in site_ids if site_id is not None})
        if not normalized:
            return {}
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in normalized)
            cur.execute(f"SELECT id, name FROM sites WHERE id IN ({placeholders})", tuple(normalized))
            return {int(row[0]): _clean_text(row[1]) for row in cur.fetchall()}
        finally:
            conn.close()

    def _load_watchdog_sites(self, watchdog_ids: Sequence[int]) -> Dict[int, List[int]]:
        normalized = [int(value) for value in watchdog_ids if value is not None]
        if not normalized:
            return {}
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in normalized)
            cur.execute(
                f"SELECT watchdog_id, site_id FROM watchdog_sites WHERE watchdog_id IN ({placeholders})",
                tuple(normalized),
            )
            lookup: Dict[int, List[int]] = {}
            for row in cur.fetchall():
                watchdog_id = _coerce_int(row[0], 0)
                site_id = _coerce_optional_int(row[1])
                if watchdog_id <= 0 or site_id is None:
                    continue
                lookup.setdefault(watchdog_id, []).append(site_id)
            return lookup
        finally:
            conn.close()

    def _load_watchdog_targets(self, watchdog_ids: Sequence[int]) -> Dict[int, List[Any]]:
        normalized = [int(value) for value in watchdog_ids if value is not None]
        if not normalized:
            return {}
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in normalized)
            cur.execute(
                f"SELECT watchdog_id, kind, target_json FROM watchdog_targets WHERE watchdog_id IN ({placeholders}) ORDER BY id ASC",
                tuple(normalized),
            )
            lookup: Dict[int, List[Any]] = {}
            for row in cur.fetchall():
                watchdog_id = _coerce_int(row[0], 0)
                kind = _clean_text(row[1]).lower()
                payload = _safe_json_loads(row[2], {})
                if watchdog_id <= 0:
                    continue
                if not isinstance(payload, dict):
                    payload = {}
                payload["kind"] = kind or _clean_text(payload.get("kind") or payload.get("type")).lower()
                lookup.setdefault(watchdog_id, []).append(payload)
            return lookup
        finally:
            conn.close()

    def _load_open_incident_counts(self, watchdog_ids: Sequence[int]) -> Dict[int, int]:
        normalized = [int(value) for value in watchdog_ids if value is not None]
        if not normalized:
            return {}
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in normalized)
            cur.execute(
                f"""
                SELECT watchdog_id, COUNT(*)
                  FROM watchdog_incidents
                 WHERE watchdog_id IN ({placeholders})
                   AND state = 'open'
              GROUP BY watchdog_id
                """,
                tuple(normalized),
            )
            return {int(row[0]): int(row[1]) for row in cur.fetchall()}
        finally:
            conn.close()

    def _load_current_state_counts(self, watchdog_ids: Sequence[int]) -> Dict[int, Dict[str, int]]:
        normalized = [int(value) for value in watchdog_ids if value is not None]
        if not normalized:
            return {}
        conn = self._conn()
        try:
            cur = conn.cursor()
            placeholders = ",".join("?" for _ in normalized)
            cur.execute(
                f"""
                SELECT watchdog_id, state, COUNT(*)
                  FROM watchdog_device_state
                 WHERE watchdog_id IN ({placeholders})
              GROUP BY watchdog_id, state
                """,
                tuple(normalized),
            )
            lookup: Dict[int, Dict[str, int]] = {}
            for row in cur.fetchall():
                watchdog_id = int(row[0])
                state = _clean_text(row[1]).lower()
                lookup.setdefault(watchdog_id, {})[state] = int(row[2])
            return lookup
        finally:
            conn.close()

    def _effective_watchdog_site_ids(
        self,
        record: Mapping[str, Any],
        *,
        all_site_ids: Optional[set[int]] = None,
    ) -> set[int]:
        site_mode = _normalize_watchdog_site_mode(record.get("site_mode"))
        site_ids = {int(site_id) for site_id in (record.get("site_ids") or []) if site_id is not None}
        available = set(all_site_ids) if all_site_ids is not None else self._load_all_site_ids()
        if site_mode == "specific_sites":
            return site_ids
        if site_mode == "global_exclusions":
            return available.difference(site_ids)
        return available

    def _watchdog_visible_to_user(
        self,
        record: Mapping[str, Any],
        user: Optional[Mapping[str, Any]],
        *,
        all_site_ids: Optional[set[int]] = None,
    ) -> bool:
        allowed_site_ids = self._site_access.site_ids_for_user(user)
        if allowed_site_ids is None:
            return True
        effective_site_ids = self._effective_watchdog_site_ids(record, all_site_ids=all_site_ids)
        return effective_site_ids.issubset(allowed_site_ids)

    def _load_filter_records(self, filter_ids: Sequence[int]) -> Dict[int, Dict[str, Any]]:
        ids = [int(value) for value in filter_ids if value is not None]
        if not ids:
            return {}
        records = self._matcher.load_filters(ids, include_archived=False)
        return {int(record["id"]): record for record in records if record.get("id") is not None}

    def _normalize_watchdog_record(
        self,
        payload: Mapping[str, Any],
        *,
        existing: Optional[Mapping[str, Any]] = None,
        username: str = "",
    ) -> Dict[str, Any]:
        base = dict(existing or {})
        now_ts = _now_ts()
        criteria_payload = payload.get("criteria") if isinstance(payload.get("criteria"), dict) else base.get("criteria") or {}
        rules_input = criteria_payload.get("rules") if isinstance(criteria_payload, dict) else payload.get("rules")
        rules = []
        for index, raw_rule in enumerate(rules_input if isinstance(rules_input, list) else []):
            normalized = _normalize_rule(index, raw_rule)
            if normalized is not None:
                rules.append(normalized)
            if len(rules) >= MAX_RULES_PER_WATCHDOG:
                break
        criteria = {
            "match_mode": _normalize_watchdog_match_mode(
                criteria_payload.get("match_mode") if isinstance(criteria_payload, dict) else payload.get("match_mode")
            ),
            "rules": rules,
        }
        actions_input = payload.get("actions") if isinstance(payload.get("actions"), dict) else base.get("actions") or {}
        actions_entries = actions_input.get("actions") if isinstance(actions_input, dict) else payload.get("action_list")
        actions = {"actions": []}
        for index, raw_action in enumerate(actions_entries if isinstance(actions_entries, list) else []):
            normalized = _normalize_action(index, raw_action)
            if normalized is not None:
                actions["actions"].append(normalized)
        normalized_record = {
            "id": _coerce_optional_int(payload.get("id") or base.get("id")),
            "name": _clean_single_line(payload.get("name") or base.get("name") or ""),
            "description": _clean_single_line(payload.get("description") or base.get("description") or ""),
            "archived": _coerce_bool(payload.get("archived"), _coerce_bool(base.get("archived"), False)),
            "enabled": _coerce_bool(payload.get("enabled"), _coerce_bool(base.get("enabled"), True)),
            "severity": _normalize_watchdog_severity(payload.get("severity") or base.get("severity") or "warning"),
            "site_mode": _normalize_watchdog_site_mode(payload.get("site_mode") or base.get("site_mode") or "global"),
            "site_ids": _normalize_site_ids(
                payload.get("site_ids")
                or payload.get("sites")
                or payload.get("site_scope_values")
                or base.get("site_ids")
                or []
            ),
            "criteria": criteria,
            "match_mode": criteria["match_mode"],
            "actions": actions,
            "targets": _normalize_targets(payload.get("targets") or base.get("targets") or []),
            "evaluation_interval_seconds": max(
                30,
                _coerce_int(
                    payload.get("evaluation_interval_seconds") or base.get("evaluation_interval_seconds"),
                    DEFAULT_WATCHDOG_EVALUATION_INTERVAL_SECONDS,
                ),
            ),
            "cooldown_seconds": max(
                0,
                _coerce_int(
                    payload.get("cooldown_seconds") or base.get("cooldown_seconds"),
                    DEFAULT_WATCHDOG_COOLDOWN_SECONDS,
                ),
            ),
            "auto_resolve_after_seconds": max(
                0,
                _coerce_int(
                    payload.get("auto_resolve_after_seconds") or base.get("auto_resolve_after_seconds"),
                    DEFAULT_WATCHDOG_AUTO_RESOLVE_SECONDS,
                ),
            ),
            "min_consecutive_matches": max(
                1,
                _coerce_int(
                    payload.get("min_consecutive_matches") or base.get("min_consecutive_matches"),
                    DEFAULT_WATCHDOG_MIN_CONSECUTIVE_MATCHES,
                ),
            ),
            "boot_grace_seconds": max(
                0,
                _coerce_int(
                    payload.get("boot_grace_seconds") or base.get("boot_grace_seconds"),
                    DEFAULT_WATCHDOG_BOOT_GRACE_SECONDS,
                ),
            ),
            "last_edited_by": username or _clean_text(base.get("last_edited_by")) or "Unknown",
            "created_at": _coerce_int(base.get("created_at"), now_ts),
            "updated_at": now_ts,
            "last_evaluated_at": _coerce_optional_int(base.get("last_evaluated_at")),
        }
        if not normalized_record["name"]:
            normalized_record["name"] = "Unnamed Watchdog"
        return normalized_record

    def _validate_watchdog_record(
        self,
        record: Mapping[str, Any],
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> List[str]:
        errors: List[str] = []
        if not _clean_single_line(record.get("name")):
            errors.append("Watchdog name is required.")
        rules = record.get("criteria", {}).get("rules") if isinstance(record.get("criteria"), dict) else []
        if not isinstance(rules, list) or not rules:
            errors.append("At least one watchdog rule is required.")
        for rule in rules if isinstance(rules, list) else []:
            if not isinstance(rule, dict):
                continue
            if _clean_text(rule.get("type")).lower() == "storage_usage_percent":
                drive_mode = _normalize_storage_drive_mode(rule.get("drive_mode"), rule.get("drive"))
                if drive_mode == "specific" and not _clean_text(rule.get("drive")):
                    errors.append("Storage usage rules using Specific Drive must include a drive letter or mount path.")
        targets = record.get("targets") if isinstance(record.get("targets"), list) else []
        if not targets:
            errors.append("At least one target device or filter is required.")
        allowed_site_ids = self._site_access.site_ids_for_user(user)
        if allowed_site_ids is not None:
            if not allowed_site_ids:
                errors.append("You do not have any assigned sites available for watchdog targeting.")
            configured_site_ids = {int(site_id) for site_id in (record.get("site_ids") or []) if site_id is not None}
            if configured_site_ids and not configured_site_ids.issubset(allowed_site_ids):
                errors.append("One or more selected scope sites is outside your assigned site scope.")
            filter_ids = []
            for target in targets:
                if isinstance(target, dict) and (_clean_text(target.get("kind") or target.get("type")).lower() == "filter" or target.get("filter_id") is not None):
                    filter_id = _coerce_optional_int(target.get("filter_id") or target.get("id"))
                    if filter_id is not None:
                        filter_ids.append(filter_id)
            filter_records = self._load_filter_records(filter_ids)
            for filter_id in filter_ids:
                filter_record = filter_records.get(filter_id)
                if filter_record is None or not self._watchdog_visible_to_user(filter_record, user):
                    errors.append(f"Filter {filter_id} is outside your assigned site scope.")
        return errors

    def _persist_watchdog(self, record: Mapping[str, Any]) -> Dict[str, Any]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            record_id = _coerce_optional_int(record.get("id"))
            payload = (
                _clean_single_line(record.get("name")),
                _clean_single_line(record.get("description")),
                1 if _coerce_bool(record.get("archived")) else 0,
                1 if _coerce_bool(record.get("enabled"), True) else 0,
                _normalize_watchdog_severity(record.get("severity")),
                _normalize_watchdog_match_mode(record.get("match_mode")),
                _normalize_watchdog_site_mode(record.get("site_mode")),
                _safe_json_dumps(record.get("criteria"), DEFAULT_WATCHDOG_CRITERIA),
                _safe_json_dumps(record.get("actions"), DEFAULT_WATCHDOG_ACTIONS),
                _coerce_int(record.get("evaluation_interval_seconds"), DEFAULT_WATCHDOG_EVALUATION_INTERVAL_SECONDS),
                _coerce_int(record.get("cooldown_seconds"), DEFAULT_WATCHDOG_COOLDOWN_SECONDS),
                _coerce_int(record.get("auto_resolve_after_seconds"), DEFAULT_WATCHDOG_AUTO_RESOLVE_SECONDS),
                _coerce_int(record.get("min_consecutive_matches"), DEFAULT_WATCHDOG_MIN_CONSECUTIVE_MATCHES),
                _coerce_int(record.get("boot_grace_seconds"), DEFAULT_WATCHDOG_BOOT_GRACE_SECONDS),
                _clean_text(record.get("last_edited_by")),
                _coerce_int(record.get("created_at"), _now_ts()),
                _coerce_int(record.get("updated_at"), _now_ts()),
                _coerce_optional_int(record.get("last_evaluated_at")),
            )
            if record_id is None:
                cur.execute(
                    """
                    INSERT INTO watchdogs (
                        name, description, archived, enabled, severity, match_mode, site_mode,
                        criteria_json, actions_json, evaluation_interval_seconds, cooldown_seconds,
                        auto_resolve_after_seconds, min_consecutive_matches, boot_grace_seconds,
                        last_edited_by, created_at, updated_at, last_evaluated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    payload,
                )
                record_id = int(cur.lastrowid)
            else:
                cur.execute(
                    """
                    UPDATE watchdogs
                       SET name=?,
                           description=?,
                           archived=?,
                           enabled=?,
                           severity=?,
                           match_mode=?,
                           site_mode=?,
                           criteria_json=?,
                           actions_json=?,
                           evaluation_interval_seconds=?,
                           cooldown_seconds=?,
                           auto_resolve_after_seconds=?,
                           min_consecutive_matches=?,
                           boot_grace_seconds=?,
                           last_edited_by=?,
                           created_at=?,
                           updated_at=?,
                           last_evaluated_at=?
                     WHERE id=?
                    """,
                    payload + (record_id,),
                )
                cur.execute("DELETE FROM watchdog_sites WHERE watchdog_id=?", (record_id,))
                cur.execute("DELETE FROM watchdog_targets WHERE watchdog_id=?", (record_id,))
            for site_id in record.get("site_ids") or []:
                cur.execute(
                    "INSERT INTO watchdog_sites (watchdog_id, site_id) VALUES (?, ?)",
                    (record_id, int(site_id)),
                )
            for target in record.get("targets") or []:
                cur.execute(
                    """
                    INSERT INTO watchdog_targets (watchdog_id, kind, target_json, created_at)
                    VALUES (?, ?, ?, ?)
                    """,
                    (
                        record_id,
                        _clean_text(target.get("kind") or target.get("type")).lower() or "device",
                        _safe_json_dumps(target, {}),
                        _now_ts(),
                    ),
                )
            conn.commit()
            saved = self.get_watchdog(record_id, user=None)
            if saved is None:
                raise RuntimeError("Watchdog save succeeded but reload failed.")
            return saved
        finally:
            conn.close()

    def list_watchdogs(
        self,
        *,
        user: Optional[Mapping[str, Any]] = None,
        archived: Optional[bool] = None,
    ) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            clauses: List[str] = []
            params: List[Any] = []
            if archived is not None:
                clauses.append("COALESCE(archived, 0) = ?")
                params.append(1 if archived else 0)
            where_sql = f"WHERE {' AND '.join(clauses)}" if clauses else ""
            cur.execute(
                f"""
                SELECT
                    id,
                    name,
                    description,
                    archived,
                    enabled,
                    severity,
                    match_mode,
                    site_mode,
                    criteria_json,
                    actions_json,
                    evaluation_interval_seconds,
                    cooldown_seconds,
                    auto_resolve_after_seconds,
                    min_consecutive_matches,
                    boot_grace_seconds,
                    last_edited_by,
                    created_at,
                    updated_at,
                    last_evaluated_at
                  FROM watchdogs
                  {where_sql}
              ORDER BY COALESCE(updated_at, created_at, 0) DESC, id DESC
                """,
                tuple(params),
            )
            rows = cur.fetchall()
        finally:
            conn.close()
        records = [self._row_to_watchdog(row) for row in rows]
        if not records:
            return []
        ids = [int(record["id"]) for record in records if record.get("id") is not None]
        all_site_ids = self._load_all_site_ids()
        site_ids = set()
        site_lookup = self._load_watchdog_sites(ids)
        target_lookup = self._load_watchdog_targets(ids)
        incident_counts = self._load_open_incident_counts(ids)
        state_counts = self._load_current_state_counts(ids)
        for values in site_lookup.values():
            site_ids.update(values)
        site_names = self._load_site_name_map(site_ids)
        hydrated: List[Dict[str, Any]] = []
        for record in records:
            record["site_ids"] = list(site_lookup.get(int(record["id"]), []))
            record["site_names"] = [site_names.get(site_id, f"Site {site_id}") for site_id in record["site_ids"]]
            record["targets"] = list(target_lookup.get(int(record["id"]), []))
            record["open_incident_count"] = incident_counts.get(int(record["id"]), 0)
            record["state_counts"] = state_counts.get(int(record["id"]), {})
            record["rule_summaries"] = _build_rule_summaries(record.get("criteria") or {})
            record["action_summaries"] = _build_action_summaries(record.get("actions") or {})
            if self._watchdog_visible_to_user(record, user, all_site_ids=all_site_ids):
                resolved_devices = self.resolve_targets(record)
                record["target_device_count"] = len(resolved_devices)
                hydrated.append(record)
        return hydrated

    def _row_to_watchdog(self, row: Any) -> Dict[str, Any]:
        return {
            "id": _coerce_int(row["id"] if isinstance(row, sqlite3.Row) else row[0], 0),
            "name": _clean_single_line(row["name"] if isinstance(row, sqlite3.Row) else row[1]),
            "description": _clean_single_line(row["description"] if isinstance(row, sqlite3.Row) else row[2]),
            "archived": _coerce_bool(row["archived"] if isinstance(row, sqlite3.Row) else row[3]),
            "enabled": _coerce_bool(row["enabled"] if isinstance(row, sqlite3.Row) else row[4], True),
            "severity": _normalize_watchdog_severity(row["severity"] if isinstance(row, sqlite3.Row) else row[5]),
            "match_mode": _normalize_watchdog_match_mode(row["match_mode"] if isinstance(row, sqlite3.Row) else row[6]),
            "site_mode": _normalize_watchdog_site_mode(row["site_mode"] if isinstance(row, sqlite3.Row) else row[7]),
            "criteria": _safe_json_loads(row["criteria_json"] if isinstance(row, sqlite3.Row) else row[8], DEFAULT_WATCHDOG_CRITERIA),
            "actions": _safe_json_loads(row["actions_json"] if isinstance(row, sqlite3.Row) else row[9], DEFAULT_WATCHDOG_ACTIONS),
            "evaluation_interval_seconds": _coerce_int(row["evaluation_interval_seconds"] if isinstance(row, sqlite3.Row) else row[10], DEFAULT_WATCHDOG_EVALUATION_INTERVAL_SECONDS),
            "cooldown_seconds": _coerce_int(row["cooldown_seconds"] if isinstance(row, sqlite3.Row) else row[11], DEFAULT_WATCHDOG_COOLDOWN_SECONDS),
            "auto_resolve_after_seconds": _coerce_int(row["auto_resolve_after_seconds"] if isinstance(row, sqlite3.Row) else row[12], DEFAULT_WATCHDOG_AUTO_RESOLVE_SECONDS),
            "min_consecutive_matches": _coerce_int(row["min_consecutive_matches"] if isinstance(row, sqlite3.Row) else row[13], DEFAULT_WATCHDOG_MIN_CONSECUTIVE_MATCHES),
            "boot_grace_seconds": _coerce_int(row["boot_grace_seconds"] if isinstance(row, sqlite3.Row) else row[14], DEFAULT_WATCHDOG_BOOT_GRACE_SECONDS),
            "last_edited_by": _clean_text(row["last_edited_by"] if isinstance(row, sqlite3.Row) else row[15]),
            "created_at": _coerce_int(row["created_at"] if isinstance(row, sqlite3.Row) else row[16], 0),
            "updated_at": _coerce_int(row["updated_at"] if isinstance(row, sqlite3.Row) else row[17], 0),
            "last_evaluated_at": _coerce_optional_int(row["last_evaluated_at"] if isinstance(row, sqlite3.Row) else row[18]),
            "site_ids": [],
            "site_names": [],
            "targets": [],
        }

    def get_watchdog(
        self,
        watchdog_id: Any,
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Optional[Dict[str, Any]]:
        try:
            watchdog_id_int = int(watchdog_id)
        except Exception:
            return None
        records = self.list_watchdogs(user=user)
        for record in records:
            if int(record["id"]) == watchdog_id_int:
                return record
        if user is None:
            # Fast path for internal callers that do not need filtered list.
            conn = self._conn()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT
                        id, name, description, archived, enabled, severity, match_mode, site_mode,
                        criteria_json, actions_json, evaluation_interval_seconds, cooldown_seconds,
                        auto_resolve_after_seconds, min_consecutive_matches, boot_grace_seconds,
                        last_edited_by, created_at, updated_at, last_evaluated_at
                      FROM watchdogs
                     WHERE id=?
                    """,
                    (watchdog_id_int,),
                )
                row = cur.fetchone()
            finally:
                conn.close()
            if not row:
                return None
            record = self._row_to_watchdog(row)
            site_ids = self._load_watchdog_sites([watchdog_id_int]).get(watchdog_id_int, [])
            record["site_ids"] = site_ids
            record["site_names"] = [self._load_site_name_map(site_ids).get(site_id, f"Site {site_id}") for site_id in site_ids]
            record["targets"] = self._load_watchdog_targets([watchdog_id_int]).get(watchdog_id_int, [])
            record["rule_summaries"] = _build_rule_summaries(record.get("criteria") or {})
            record["action_summaries"] = _build_action_summaries(record.get("actions") or {})
            record["target_device_count"] = len(self.resolve_targets(record))
            record["open_incident_count"] = self._load_open_incident_counts([watchdog_id_int]).get(watchdog_id_int, 0)
            record["state_counts"] = self._load_current_state_counts([watchdog_id_int]).get(watchdog_id_int, {})
            return record
        return None

    def save_watchdog(
        self,
        payload: Mapping[str, Any],
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Tuple[Optional[Dict[str, Any]], List[str]]:
        username = _clean_text((user or {}).get("username")) or "Unknown"
        existing = None
        if payload.get("id") is not None:
            existing = self.get_watchdog(payload.get("id"), user=None)
        record = self._normalize_watchdog_record(payload, existing=existing, username=username)
        errors = self._validate_watchdog_record(record, user=user)
        if errors:
            return None, errors
        saved = self._persist_watchdog(record)
        self.evaluate_watchdog(saved)
        return self.get_watchdog(saved["id"], user=user), []

    def delete_watchdog(self, watchdog_id: Any, *, user: Optional[Mapping[str, Any]] = None) -> bool:
        existing = self.get_watchdog(watchdog_id, user=user)
        if existing is None:
            return False
        state_lookup = self._load_watchdog_state(int(existing["id"]))
        incident_lookup = self._load_active_incidents(int(existing["id"]))
        affected_hosts = {
            _clean_text(state.get("hostname")) for state in state_lookup.values() if _clean_text(state.get("hostname"))
        }
        affected_hosts.update(
            _clean_text(incident.get("hostname"))
            for incident in incident_lookup.values()
            if _clean_text(incident.get("hostname"))
        )
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute("DELETE FROM watchdog_targets WHERE watchdog_id=?", (int(existing["id"]),))
            cur.execute("DELETE FROM watchdog_sites WHERE watchdog_id=?", (int(existing["id"]),))
            cur.execute("DELETE FROM watchdog_device_overrides WHERE watchdog_id=?", (int(existing["id"]),))
            cur.execute("DELETE FROM watchdog_device_state WHERE watchdog_id=?", (int(existing["id"]),))
            cur.execute("DELETE FROM watchdog_incidents WHERE watchdog_id=?", (int(existing["id"]),))
            cur.execute("DELETE FROM watchdogs WHERE id=?", (int(existing["id"]),))
            conn.commit()
        finally:
            conn.close()
        for hostname in sorted(affected_hosts):
            self._emit_watchdog_refresh(hostname=hostname, watchdog_id=int(existing["id"]))
        self._emit_watchdog_refresh(watchdog_id=int(existing["id"]))
        return True

    def _load_watchdog_devices(
        self,
        *,
        allowed_site_ids: Optional[Iterable[int]] = None,
    ) -> List[Dict[str, Any]]:
        devices = self._matcher.fetch_devices(allowed_site_ids=allowed_site_ids)
        if not devices:
            return []
        by_guid = {normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "": device for device in devices}
        by_host = {_clean_text(device.get("hostname")).lower(): device for device in devices if _clean_text(device.get("hostname"))}
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT guid, hostname, services, agent_role_health, last_seen, uptime, agent_hash, connection_type, connection_endpoint, agent_id
                  FROM devices
                """
            )
            repo_hash = self._current_target_repo_hash()
            for row in cur.fetchall():
                guid = normalize_guid(row[0]) or ""
                hostname = _clean_text(row[1])
                device = by_guid.get(guid) or by_host.get(hostname.lower())
                if not device:
                    continue
                services_payload = normalize_device_services(row[2], default_captured_at=_coerce_int(row[4], 0))
                role_health_payload = normalize_agent_role_health(row[3])
                installed_hash = _clean_text(row[6]).lower()
                agent_version_status = "Up-to-Date" if installed_hash and repo_hash and installed_hash == repo_hash else "Needs Updated"
                device["services_payload"] = services_payload
                device["services"] = services_payload.get("services") or []
                device["agent_role_health_payload"] = role_health_payload
                device["agent_role_health"] = role_health_payload.get("roles") or []
                device["agent_version_status"] = agent_version_status
                device["uptime"] = _coerce_int(row[5], device.get("uptime") or 0)
                device["agent_hash"] = _clean_text(row[6]) or _clean_text(device.get("agent_hash"))
                device["connection_type"] = _clean_text(row[7]).lower() or _clean_text(device.get("connection_type")).lower()
                device["connection_endpoint"] = _clean_text(row[8]) or _clean_text(device.get("connection_endpoint"))
                device["agent_id"] = _clean_text(row[9]) or _clean_text(device.get("agent_id"))
        finally:
            conn.close()
        return devices

    def resolve_targets(self, watchdog: Mapping[str, Any]) -> List[Dict[str, Any]]:
        effective_site_ids = self._effective_watchdog_site_ids(watchdog)
        devices = self._load_watchdog_devices(allowed_site_ids=effective_site_ids)
        if not devices:
            return []
        targets = watchdog.get("targets") if isinstance(watchdog.get("targets"), list) else []
        if not targets:
            return []
        if any(
            isinstance(target, dict)
            and (_clean_text(target.get("kind") or target.get("type")).lower() == "all_devices" or _coerce_bool(target.get("all_devices"), False))
            for target in targets
        ):
            return sorted(
                devices,
                key=lambda item: (
                    _clean_text(item.get("site_name")).lower(),
                    _clean_text(item.get("hostname")).lower(),
                ),
            )
        filter_ids = []
        explicit_targets: List[Dict[str, Any]] = []
        for target in targets:
            if not isinstance(target, dict):
                continue
            kind = _clean_text(target.get("kind") or target.get("type")).lower()
            if kind == "all_devices" or _coerce_bool(target.get("all_devices"), False):
                continue
            if kind == "filter" or target.get("filter_id") is not None:
                filter_id = _coerce_optional_int(target.get("filter_id") or target.get("id"))
                if filter_id is not None:
                    filter_ids.append(filter_id)
                continue
            explicit_targets.append(target)
        matches: Dict[str, Dict[str, Any]] = {}
        for target in explicit_targets:
            target_guid = normalize_guid(target.get("device_guid") or target.get("guid") or "") or ""
            target_hostname = _clean_text(target.get("hostname")).lower()
            for device in devices:
                device_guid = normalize_guid(device.get("guid") or device.get("agent_guid") or "") or ""
                device_hostname = _clean_text(device.get("hostname")).lower()
                if target_guid and device_guid and target_guid == device_guid:
                    matches[device_hostname or device_guid] = device
                elif target_hostname and target_hostname == device_hostname:
                    matches[device_hostname] = device
        if filter_ids:
            filters_by_id = self._load_filter_records(filter_ids)
            for filter_id in filter_ids:
                filter_record = filters_by_id.get(filter_id)
                if filter_record is None:
                    continue
                for device in self._matcher.match_filter_devices(filter_record, devices=devices):
                    device_hostname = _clean_text(device.get("hostname")).lower()
                    if device_hostname:
                        matches[device_hostname] = device
        return sorted(
            matches.values(),
            key=lambda item: (
                _clean_text(item.get("site_name")).lower(),
                _clean_text(item.get("hostname")).lower(),
            ),
        )

    def _load_active_overrides(self, watchdog_id: int) -> Dict[str, Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            now_ts = _now_ts()
            cur.execute(
                """
                SELECT id, device_guid, hostname, site_id, state, reason, created_by, created_at, expires_at, updated_at
                  FROM watchdog_device_overrides
                 WHERE watchdog_id=?
                   AND (expires_at IS NULL OR expires_at = 0 OR expires_at > ?)
              ORDER BY updated_at DESC, id DESC
                """,
                (int(watchdog_id), now_ts),
            )
            lookup: Dict[str, Dict[str, Any]] = {}
            for row in cur.fetchall():
                hostname = _clean_text(row[2]).lower()
                if not hostname:
                    continue
                lookup[hostname] = {
                    "id": _coerce_int(row[0], 0),
                    "device_guid": normalize_guid(row[1]) or "",
                    "hostname": _clean_text(row[2]),
                    "site_id": _coerce_optional_int(row[3]),
                    "state": _clean_text(row[4]).lower(),
                    "reason": _clean_text(row[5]),
                    "created_by": _clean_text(row[6]),
                    "created_at": _coerce_int(row[7], 0),
                    "expires_at": _coerce_optional_int(row[8]),
                    "updated_at": _coerce_int(row[9], 0),
                }
            return lookup
        finally:
            conn.close()

    def _load_watchdog_state(self, watchdog_id: int) -> Dict[str, Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id, device_guid, hostname, site_id, state, consecutive_matches,
                    first_matched_at, clear_started_at, last_evaluated_at, last_matched_at,
                    last_sample_json, current_incident_id, last_action_at, updated_at
                  FROM watchdog_device_state
                 WHERE watchdog_id=?
                """,
                (int(watchdog_id),),
            )
            lookup: Dict[str, Dict[str, Any]] = {}
            for row in cur.fetchall():
                hostname = _clean_text(row[2]).lower()
                if not hostname:
                    continue
                lookup[hostname] = {
                    "id": _coerce_int(row[0], 0),
                    "device_guid": normalize_guid(row[1]) or "",
                    "hostname": _clean_text(row[2]),
                    "site_id": _coerce_optional_int(row[3]),
                    "state": _clean_text(row[4]).lower(),
                    "consecutive_matches": _coerce_int(row[5], 0),
                    "first_matched_at": _coerce_optional_int(row[6]),
                    "clear_started_at": _coerce_optional_int(row[7]),
                    "last_evaluated_at": _coerce_int(row[8], 0),
                    "last_matched_at": _coerce_optional_int(row[9]),
                    "last_sample": _safe_json_loads(row[10], {}),
                    "current_incident_id": _coerce_optional_int(row[11]),
                    "last_action_at": _coerce_optional_int(row[12]),
                    "updated_at": _coerce_int(row[13], 0),
                }
            return lookup
        finally:
            conn.close()

    def _load_active_incidents(self, watchdog_id: int) -> Dict[str, Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id, device_guid, hostname, site_id, severity, state, title, message,
                    sample_json, rule_summary_json, action_summary_json, opened_at, updated_at,
                    resolved_at, resolution_reason, acknowledged_at, acknowledged_by, trigger_count
                  FROM watchdog_incidents
                 WHERE watchdog_id=?
                   AND state IN ('open', 'suppressed')
              ORDER BY updated_at DESC, id DESC
                """,
                (int(watchdog_id),),
            )
            lookup: Dict[str, Dict[str, Any]] = {}
            for row in cur.fetchall():
                hostname = _clean_text(row[2]).lower()
                if not hostname:
                    continue
                lookup[hostname] = self._row_to_incident(row)
            return lookup
        finally:
            conn.close()

    def _load_open_incidents(self, watchdog_id: int) -> Dict[str, Dict[str, Any]]:
        return {
            hostname: incident
            for hostname, incident in self._load_active_incidents(watchdog_id).items()
            if _clean_text(incident.get("state")).lower() == "open"
        }

    def _row_to_incident(self, row: Any) -> Dict[str, Any]:
        return {
            "id": _coerce_int(row["id"] if isinstance(row, sqlite3.Row) else row[0], 0),
            "device_guid": normalize_guid(row["device_guid"] if isinstance(row, sqlite3.Row) else row[1]) or "",
            "hostname": _clean_text(row["hostname"] if isinstance(row, sqlite3.Row) else row[2]),
            "site_id": _coerce_optional_int(row["site_id"] if isinstance(row, sqlite3.Row) else row[3]),
            "severity": _normalize_watchdog_severity(row["severity"] if isinstance(row, sqlite3.Row) else row[4]),
            "state": _clean_text(row["state"] if isinstance(row, sqlite3.Row) else row[5]).lower(),
            "title": _clean_text(row["title"] if isinstance(row, sqlite3.Row) else row[6]),
            "message": _clean_text(row["message"] if isinstance(row, sqlite3.Row) else row[7]),
            "sample": _safe_json_loads(row["sample_json"] if isinstance(row, sqlite3.Row) else row[8], {}),
            "rule_summary": _safe_json_loads(row["rule_summary_json"] if isinstance(row, sqlite3.Row) else row[9], []),
            "action_summary": _safe_json_loads(row["action_summary_json"] if isinstance(row, sqlite3.Row) else row[10], []),
            "opened_at": _coerce_int(row["opened_at"] if isinstance(row, sqlite3.Row) else row[11], 0),
            "updated_at": _coerce_int(row["updated_at"] if isinstance(row, sqlite3.Row) else row[12], 0),
            "resolved_at": _coerce_optional_int(row["resolved_at"] if isinstance(row, sqlite3.Row) else row[13]),
            "resolution_reason": _clean_text(row["resolution_reason"] if isinstance(row, sqlite3.Row) else row[14]),
            "acknowledged_at": _coerce_optional_int(row["acknowledged_at"] if isinstance(row, sqlite3.Row) else row[15]),
            "acknowledged_by": _clean_text(row["acknowledged_by"] if isinstance(row, sqlite3.Row) else row[16]),
            "trigger_count": _coerce_int(row["trigger_count"] if isinstance(row, sqlite3.Row) else row[17], 1),
        }

    def _services_stale(self, payload: Mapping[str, Any], now_ts: int) -> bool:
        reported_at = _coerce_int(payload.get("reported_at"), 0)
        return reported_at <= 0 or (now_ts - reported_at) > DEFAULT_TELEMETRY_STALE_SECONDS

    def _role_health_stale(self, payload: Mapping[str, Any], now_ts: int) -> bool:
        reported_at = _coerce_int(payload.get("reported_at"), 0)
        return reported_at <= 0 or (now_ts - reported_at) > DEFAULT_TELEMETRY_STALE_SECONDS

    def _evaluate_rule(self, rule: Mapping[str, Any], device: Mapping[str, Any], *, now_ts: int) -> Dict[str, Any]:
        rule_type = _clean_text(rule.get("type")).lower()
        if rule_type == "device_offline":
            offline_after_seconds = _coerce_int(rule.get("offline_after_seconds"), DEFAULT_DEVICE_OFFLINE_SECONDS)
            last_seen = _coerce_int(device.get("last_seen"), 0)
            age_seconds = max(0, now_ts - last_seen) if last_seen > 0 else offline_after_seconds
            matched = last_seen <= 0 or age_seconds >= offline_after_seconds
            message = (
                f"Device has not checked in for {max(1, age_seconds // 60)} minute(s)"
                if matched
                else f"Device heartbeat age is {age_seconds} seconds"
            )
            return {
                "rule_id": rule.get("id"),
                "type": rule_type,
                "matched": matched,
                "stale": False,
                "summary": message,
                "sample": {
                    "last_seen": last_seen,
                    "age_seconds": age_seconds,
                    "offline_after_seconds": offline_after_seconds,
                },
            }
        if rule_type == "storage_usage_percent":
            storage_rows = _normalize_storage_entries(device.get("storage"))
            if not storage_rows:
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": False,
                    "stale": True,
                    "summary": "Storage inventory is not available",
                    "sample": {},
                }
            threshold = float(_coerce_float(rule.get("threshold")) or 90.0)
            drive_mode = _normalize_storage_drive_mode(rule.get("drive_mode"), rule.get("drive"))
            selected_drive = _clean_text(rule.get("drive"))
            selected_drive_key = _normalize_storage_drive_key(selected_drive)
            evaluated_rows = [row for row in storage_rows if row.get("usage_percent") is not None]
            if drive_mode == "specific":
                evaluated_rows = [
                    row for row in evaluated_rows if _normalize_storage_drive_key(row.get("drive")) == selected_drive_key
                ]
                if not evaluated_rows:
                    return {
                        "rule_id": rule.get("id"),
                        "type": rule_type,
                        "matched": False,
                        "stale": False,
                        "summary": f"Drive {selected_drive or 'target drive'} is not present in storage inventory",
                        "sample": {
                            "drive_scope": "specific",
                            "drive": selected_drive,
                            "present": False,
                            "threshold": round(threshold, 2),
                        },
                    }
            if not evaluated_rows:
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": False,
                    "stale": True,
                    "summary": "Storage usage data is incomplete",
                    "sample": {},
                }
            ranked = sorted(evaluated_rows, key=lambda row: float(row.get("usage_percent") or 0), reverse=True)
            chosen = ranked[0]
            usage = float(chosen.get("usage_percent") or 0.0)
            matched_rows = [row for row in ranked if float(row.get("usage_percent") or 0.0) >= threshold]
            matched = bool(matched_rows)
            if drive_mode == "specific":
                matched = usage >= threshold
                summary = f"{chosen.get('drive')} usage is {usage:.1f}% (threshold {threshold:.1f}%)"
                sample = {
                    "drive_scope": "specific",
                    "drive": chosen.get("drive"),
                    "usage_percent": round(usage, 2),
                    "threshold": round(threshold, 2),
                    "present": True,
                }
            else:
                if matched_rows:
                    if len(matched_rows) == 1:
                        summary = (
                            f"{matched_rows[0].get('drive')} usage is "
                            f"{float(matched_rows[0].get('usage_percent') or 0.0):.1f}% "
                            f"(threshold {threshold:.1f}%)"
                        )
                    else:
                        summary = f"{len(matched_rows)} drives are at or above {threshold:.1f}% usage"
                else:
                    summary = f"Highest drive usage is {chosen.get('drive')} at {usage:.1f}% (threshold {threshold:.1f}%)"
                sample = {
                    "drive_scope": "all",
                    "threshold": round(threshold, 2),
                    "highest_drive": chosen.get("drive"),
                    "highest_usage_percent": round(usage, 2),
                    "matched_drives": [
                        {
                            "drive": row.get("drive"),
                            "usage_percent": round(float(row.get("usage_percent") or 0.0), 2),
                        }
                        for row in matched_rows
                    ],
                }
            return {
                "rule_id": rule.get("id"),
                "type": rule_type,
                "matched": matched,
                "stale": False,
                "summary": summary,
                "sample": sample,
            }
        if rule_type == "service_state":
            services_payload = device.get("services_payload") if isinstance(device.get("services_payload"), dict) else {}
            if self._services_stale(services_payload, now_ts):
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": False,
                    "stale": True,
                    "summary": "Service inventory is stale",
                    "sample": {},
                }
            service_name = _clean_text(rule.get("service_name")).lower()
            expected_status = _clean_text(rule.get("expected_status")).lower() or "running"
            match_entry = None
            for entry in services_payload.get("services") or []:
                if _clean_text(entry.get("name")).lower() == service_name:
                    match_entry = entry
                    break
            if match_entry is None:
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": True,
                    "stale": False,
                    "summary": f"Service {_clean_text(rule.get('service_name'))} is not present in the cached inventory",
                    "sample": {
                        "service_name": _clean_text(rule.get("service_name")),
                        "actual_status": "missing",
                        "expected_status": expected_status,
                    },
                }
            actual_status = _clean_text(match_entry.get("status_code")).lower() or "unknown"
            matched = actual_status != expected_status
            return {
                "rule_id": rule.get("id"),
                "type": rule_type,
                "matched": matched,
                "stale": False,
                "summary": f"Service {match_entry.get('name')} is {actual_status} (expected {expected_status})",
                "sample": {
                    "service_name": match_entry.get("name"),
                    "actual_status": actual_status,
                    "expected_status": expected_status,
                    "pending_action": _clean_text(match_entry.get("pending_action")),
                },
            }
        if rule_type == "agent_role_health":
            role_payload = device.get("agent_role_health_payload") if isinstance(device.get("agent_role_health_payload"), dict) else {}
            if self._role_health_stale(role_payload, now_ts):
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": False,
                    "stale": True,
                    "summary": "Agent role health telemetry is stale",
                    "sample": {},
                }
            role_name = _clean_text(rule.get("role_name")).lower()
            trigger_statuses = {str(item).lower() for item in (rule.get("trigger_statuses") or ["unhealthy"])}
            matched_role = None
            roles = role_payload.get("roles") if isinstance(role_payload.get("roles"), list) else []
            for entry in roles:
                candidate_name = _clean_text(entry.get("role_name")).lower()
                candidate_label = _clean_text(entry.get("role_label")).lower()
                if not role_name or role_name in {candidate_name, candidate_label}:
                    matched_role = entry
                    break
            if matched_role is None:
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": True,
                    "stale": False,
                    "summary": f"Role {_clean_text(rule.get('role_name')) or 'target role'} is not reporting health telemetry",
                    "sample": {
                        "role_name": _clean_text(rule.get("role_name")),
                        "status_code": "missing",
                        "trigger_statuses": sorted(trigger_statuses),
                    },
                }
            status_code = _clean_text(matched_role.get("status_code")).lower() or "unknown"
            return {
                "rule_id": rule.get("id"),
                "type": rule_type,
                "matched": status_code in trigger_statuses,
                "stale": False,
                "summary": f"{matched_role.get('role_label') or matched_role.get('role_name')} health is {status_code}",
                "sample": {
                    "role_name": matched_role.get("role_name"),
                    "role_label": matched_role.get("role_label"),
                    "status_code": status_code,
                    "trigger_statuses": sorted(trigger_statuses),
                    "detail": _clean_text(matched_role.get("detail")),
                },
            }
        if rule_type == "software_presence_or_version":
            software_name = _clean_text(rule.get("software_name")).lower()
            source = _clean_text(rule.get("software_source")).lower()
            require_present = _coerce_bool(rule.get("require_present"), True)
            version_operator = _clean_text(rule.get("version_operator")).lower()
            version_value = _clean_text(rule.get("version_value"))
            records = device.get("software_records") if isinstance(device.get("software_records"), list) else []
            candidates = []
            for entry in records:
                entry_name = _clean_text(entry.get("name")).lower()
                entry_source = _clean_text(entry.get("source")).lower()
                if software_name not in entry_name:
                    continue
                if source and source != entry_source:
                    continue
                candidates.append(entry)
            if not candidates:
                return {
                    "rule_id": rule.get("id"),
                    "type": rule_type,
                    "matched": require_present,
                    "stale": False,
                    "summary": f"{_clean_text(rule.get('software_name'))} is not installed",
                    "sample": {
                        "software_name": _clean_text(rule.get("software_name")),
                        "present": False,
                    },
                }
            sample_entry = candidates[0]
            version_text = _clean_text(sample_entry.get("version"))
            version_matched = False
            if version_operator and version_value:
                comparison = _normalize_version_compare(version_text, version_value)
                if version_operator == "matches":
                    version_matched = version_text == version_value
                elif comparison is not None and version_operator == "older_than":
                    version_matched = comparison < 0
                elif comparison is not None and version_operator == "newer_than":
                    version_matched = comparison > 0
            matched = False
            if require_present and not version_operator:
                matched = False
            elif version_operator and version_value:
                matched = version_matched
            summary = f"{sample_entry.get('name')} version {version_text or 'unknown'}"
            if version_operator and version_value:
                summary = f"{summary} ({version_operator.replace('_', ' ')} {version_value})"
            return {
                "rule_id": rule.get("id"),
                "type": rule_type,
                "matched": matched,
                "stale": False,
                "summary": summary,
                "sample": {
                    "software_name": sample_entry.get("name"),
                    "present": True,
                    "version": version_text,
                    "version_operator": version_operator,
                    "version_value": version_value,
                },
            }
        if rule_type == "agent_version_status":
            current_status = _clean_text(device.get("agent_version_status")) or "Needs Updated"
            expected_status = _clean_text(rule.get("expected_status") or "Up-to-Date")
            return {
                "rule_id": rule.get("id"),
                "type": rule_type,
                "matched": current_status != expected_status,
                "stale": False,
                "summary": f"Agent version status is {current_status}",
                "sample": {
                    "current_status": current_status,
                    "expected_status": expected_status,
                    "agent_hash": _clean_text(device.get("agent_hash")),
                },
            }
        return {
            "rule_id": rule.get("id"),
            "type": rule_type,
            "matched": False,
            "stale": True,
            "summary": "Unsupported rule",
            "sample": {},
        }

    def evaluate_preview(self, record: Mapping[str, Any]) -> Dict[str, Any]:
        targets = self.resolve_targets(record)
        overrides = self._load_active_overrides(int(record["id"])) if record.get("id") else {}
        results = []
        matched_count = 0
        for device in targets:
            hostname = _clean_text(device.get("hostname"))
            override = overrides.get(hostname.lower())
            evaluation = self._evaluate_device(record, device)
            if override:
                evaluation["state"] = _clean_text(override.get("state")).lower() or "suppressed"
                evaluation["message"] = override.get("reason") or "Watchdog is overridden for this device."
                evaluation["matched"] = False
            if evaluation.get("matched"):
                matched_count += 1
            results.append(
                {
                    "device_guid": normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "",
                    "hostname": hostname,
                    "site_id": _coerce_optional_int(device.get("site_id")),
                    "site_name": _clean_text(device.get("site_name")),
                    "status": _clean_text(device.get("status")) or "Offline",
                    "state": _clean_text(evaluation.get("state")).lower() or "normal",
                    "matched": bool(evaluation.get("matched")),
                    "message": _clean_text(evaluation.get("message")),
                    "sample": evaluation.get("sample") if isinstance(evaluation.get("sample"), dict) else {},
                    "rule_results": evaluation.get("rule_results") if isinstance(evaluation.get("rule_results"), list) else [],
                    "override": override or None,
                }
            )
        return {
            "devices": results,
            "device_count": len(results),
            "matched_count": matched_count,
        }

    def _evaluate_device(self, record: Mapping[str, Any], device: Mapping[str, Any]) -> Dict[str, Any]:
        now_ts = _now_ts()
        if _coerce_int(device.get("uptime"), 0) > 0 and _coerce_int(device.get("uptime"), 0) < _coerce_int(record.get("boot_grace_seconds"), 0):
            return {
                "matched": False,
                "state": "pending",
                "message": "Boot grace period is still active.",
                "sample": {
                    "uptime_seconds": _coerce_int(device.get("uptime"), 0),
                    "boot_grace_seconds": _coerce_int(record.get("boot_grace_seconds"), 0),
                },
                "rule_results": [],
            }
        criteria = record.get("criteria") if isinstance(record.get("criteria"), dict) else {}
        rules = criteria.get("rules") if isinstance(criteria.get("rules"), list) else []
        rule_results = [self._evaluate_rule(rule, device, now_ts=now_ts) for rule in rules if isinstance(rule, dict)]
        if not rule_results:
            return {
                "matched": False,
                "state": "normal",
                "message": "No rules configured.",
                "sample": {},
                "rule_results": [],
            }
        match_mode = _normalize_watchdog_match_mode(criteria.get("match_mode") or record.get("match_mode") or "all")
        stale_results = [result for result in rule_results if result.get("stale")]
        matched_results = [result for result in rule_results if result.get("matched")]
        if match_mode == "all":
            if stale_results:
                matched = False
                state = "stale_data"
            else:
                matched = len(matched_results) == len(rule_results)
                state = "triggered" if matched else "normal"
        else:
            matched = bool(matched_results)
            if matched:
                state = "triggered"
            elif stale_results:
                state = "stale_data"
            else:
                state = "normal"
        primary_results = matched_results if matched_results else stale_results if stale_results else rule_results
        message = "; ".join(_clean_text(item.get("summary")) for item in primary_results if _clean_text(item.get("summary")))
        sample = {
            "match_mode": match_mode,
            "results": [
                {
                    "rule_id": item.get("rule_id"),
                    "type": item.get("type"),
                    "matched": bool(item.get("matched")),
                    "stale": bool(item.get("stale")),
                    "summary": item.get("summary"),
                    "sample": item.get("sample") if isinstance(item.get("sample"), dict) else {},
                }
                for item in rule_results
            ],
        }
        return {
            "matched": matched,
            "state": state,
            "message": message,
            "sample": sample,
            "rule_results": sample["results"],
        }

    def _upsert_state_row(
        self,
        conn: sqlite3.Connection,
        *,
        watchdog_id: int,
        device_guid: str,
        hostname: str,
        site_id: Optional[int],
        state: str,
        consecutive_matches: int,
        first_matched_at: Optional[int],
        clear_started_at: Optional[int],
        last_evaluated_at: int,
        last_matched_at: Optional[int],
        last_sample: Mapping[str, Any],
        current_incident_id: Optional[int],
        last_action_at: Optional[int],
    ) -> None:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO watchdog_device_state (
                watchdog_id, device_guid, hostname, site_id, state, consecutive_matches,
                first_matched_at, clear_started_at, last_evaluated_at, last_matched_at,
                last_sample_json, current_incident_id, last_action_at, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(watchdog_id, hostname) DO UPDATE SET
                device_guid=excluded.device_guid,
                site_id=excluded.site_id,
                state=excluded.state,
                consecutive_matches=excluded.consecutive_matches,
                first_matched_at=excluded.first_matched_at,
                clear_started_at=excluded.clear_started_at,
                last_evaluated_at=excluded.last_evaluated_at,
                last_matched_at=excluded.last_matched_at,
                last_sample_json=excluded.last_sample_json,
                current_incident_id=excluded.current_incident_id,
                last_action_at=excluded.last_action_at,
                updated_at=excluded.updated_at
            """,
            (
                watchdog_id,
                device_guid,
                hostname,
                site_id,
                state,
                consecutive_matches,
                first_matched_at,
                clear_started_at,
                last_evaluated_at,
                last_matched_at,
                _safe_json_dumps(last_sample, {}),
                current_incident_id,
                last_action_at,
                last_evaluated_at,
            ),
        )

    def _resolve_open_incident(
        self,
        conn: sqlite3.Connection,
        *,
        incident_id: int,
        reason: str,
    ) -> None:
        now_ts = _now_ts()
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE watchdog_incidents
               SET state='resolved',
                   resolved_at=?,
                   resolution_reason=?,
                   updated_at=?
             WHERE id=?
            """,
            (now_ts, reason, now_ts, incident_id),
        )

    def _set_incident_state(
        self,
        conn: sqlite3.Connection,
        *,
        incident_id: int,
        state: str,
        reason: str = "",
    ) -> None:
        now_ts = _now_ts()
        normalized_state = _clean_text(state).lower()
        if normalized_state not in VALID_INCIDENT_STATES:
            normalized_state = "open"
        cur = conn.cursor()
        if normalized_state == "resolved":
            self._resolve_open_incident(conn, incident_id=incident_id, reason=reason)
            return
        cur.execute(
            """
            UPDATE watchdog_incidents
               SET state=?,
                   resolved_at=NULL,
                   resolution_reason=?,
                   updated_at=?
             WHERE id=?
            """,
            (normalized_state, _clean_single_line(reason), now_ts, int(incident_id)),
        )

    def _create_or_update_incident(
        self,
        conn: sqlite3.Connection,
        *,
        watchdog: Mapping[str, Any],
        device: Mapping[str, Any],
        current_incident: Optional[Mapping[str, Any]],
        evaluation: Mapping[str, Any],
        action_summary: Sequence[Any],
    ) -> Dict[str, Any]:
        now_ts = _now_ts()
        hostname = _clean_text(device.get("hostname"))
        title = f"{_clean_text(watchdog.get('name'))} on {hostname}"
        message = _clean_text(evaluation.get("message")) or title
        cur = conn.cursor()
        if current_incident and _coerce_int(current_incident.get("id"), 0) > 0:
            cur.execute(
                """
                UPDATE watchdog_incidents
                   SET message=?,
                       sample_json=?,
                       rule_summary_json=?,
                       action_summary_json=?,
                       updated_at=?,
                       trigger_count=COALESCE(trigger_count, 0) + 1
                 WHERE id=?
                """,
                (
                    message,
                    _safe_json_dumps(evaluation.get("sample"), {}),
                    _safe_json_dumps(evaluation.get("rule_results"), []),
                    _safe_json_dumps(list(action_summary), []),
                    now_ts,
                    int(current_incident["id"]),
                ),
            )
            refreshed = dict(current_incident)
            refreshed.update(
                {
                    "message": message,
                    "sample": evaluation.get("sample"),
                    "rule_summary": list(evaluation.get("rule_results") or []),
                    "action_summary": list(action_summary),
                    "updated_at": now_ts,
                    "trigger_count": _coerce_int(current_incident.get("trigger_count"), 1) + 1,
                }
            )
            return refreshed
        cur.execute(
            """
            INSERT INTO watchdog_incidents (
                watchdog_id, device_guid, hostname, site_id, severity, state, title, message,
                sample_json, rule_summary_json, action_summary_json, opened_at, updated_at, trigger_count
            ) VALUES (?, ?, ?, ?, ?, 'open', ?, ?, ?, ?, ?, ?, ?, 1)
            """,
            (
                int(watchdog["id"]),
                normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "",
                hostname,
                _coerce_optional_int(device.get("site_id")),
                _normalize_watchdog_severity(watchdog.get("severity")),
                title,
                message,
                _safe_json_dumps(evaluation.get("sample"), {}),
                _safe_json_dumps(evaluation.get("rule_results"), []),
                _safe_json_dumps(list(action_summary), []),
                now_ts,
                now_ts,
            ),
        )
        incident_id = int(cur.lastrowid)
        return {
            "id": incident_id,
            "device_guid": normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "",
            "hostname": hostname,
            "site_id": _coerce_optional_int(device.get("site_id")),
            "severity": _normalize_watchdog_severity(watchdog.get("severity")),
            "state": "open",
            "title": title,
            "message": message,
            "sample": evaluation.get("sample"),
            "rule_summary": list(evaluation.get("rule_results") or []),
            "action_summary": list(action_summary),
            "opened_at": now_ts,
            "updated_at": now_ts,
            "resolved_at": None,
            "resolution_reason": "",
            "acknowledged_at": None,
            "acknowledged_by": "",
            "trigger_count": 1,
        }

    def _record_activity_stub(
        self,
        *,
        hostname: str,
        script_path: str,
        script_name: str,
        script_type: str,
    ) -> int:
        conn = self._conn()
        try:
            cur = conn.cursor()
            now_ts = _now_ts()
            cur.execute(
                """
                INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (hostname, script_path, script_name, script_type, now_ts, "Running", "", ""),
            )
            conn.commit()
            activity_id = int(cur.lastrowid)
        finally:
            conn.close()
        try:
            if self._socketio is not None:
                self._socketio.emit(
                    "device_activity_changed",
                    {"hostname": hostname, "activity_id": activity_id, "change": "created", "source": "watchdog"},
                )
        except Exception:
            self._logger.debug("Failed to emit device_activity_changed for watchdog action", exc_info=True)
        return activity_id

    def _dispatch_script_assembly(
        self,
        *,
        device: Mapping[str, Any],
        assembly_record: Mapping[str, Any],
        action: Mapping[str, Any],
        incident: Mapping[str, Any],
    ) -> Dict[str, Any]:
        if self._assembly_runtime is None:
            return {"status": "failed", "message": "Assembly runtime is not available."}
        payload_doc = assembly_record.get("payload_json")
        if not isinstance(payload_doc, dict):
            payload_doc = _safe_json_loads(assembly_record.get("payload"), {})
        virtual_path = _clean_text(assembly_record.get("virtual_path") or "Scripts/Watchdog")
        document = _load_assembly_document(virtual_path, "powershell", payload=payload_doc)
        if not document:
            return {"status": "failed", "message": "Assembly payload could not be loaded."}
        script_type = _normalize_agent_script_type(document.get("type"))
        if script_type not in _SUPPORTED_AGENT_SCRIPT_TYPES:
            return {"status": "failed", "message": f"Unsupported quick-run script type '{script_type}'."}
        env_map, variables, literal_lookup = prepare_variable_context(
            document.get("variables") if isinstance(document.get("variables"), list) else [],
            dict(action.get("variable_values") or {}),
        )
        content = _rewrite_script_for_dispatch(document.get("script") or "", script_type, literal_lookup)
        encoded_content = base64.b64encode(content.encode("utf-8")).decode("ascii")
        hostname = _clean_text(device.get("hostname"))
        activity_id = self._record_activity_stub(
            hostname=hostname,
            script_path=virtual_path,
            script_name=_clean_text(document.get("name") or assembly_record.get("display_name") or "Watchdog Script"),
            script_type=script_type,
        )
        payload = {
            "job_id": activity_id,
            "target_hostname": hostname,
            "script_type": script_type,
            "script_name": _clean_text(document.get("name") or assembly_record.get("display_name") or "Watchdog Script"),
            "script_path": virtual_path,
            "script_content": encoded_content,
            "script_encoding": "base64",
            "environment": env_map,
            "variables": variables,
            "timeout_seconds": max(0, _coerce_int(document.get("timeout_seconds"), 0)),
            "files": document.get("files") if isinstance(document.get("files"), list) else [],
            "run_mode": _clean_text(action.get("run_mode") or "system"),
            "context": {
                "assembly_guid": _clean_text(assembly_record.get("assembly_guid") or action.get("assembly_guid")),
                "watchdog_id": incident.get("watchdog_id") if isinstance(incident, dict) else None,
                "incident_id": incident.get("id"),
                "trigger_source": "watchdog",
            },
        }
        emit_host_service_event = getattr(self._context, "emit_host_service_event", None) if self._context is not None else None
        emit_agent_event = getattr(self._context, "emit_agent_event", None) if self._context is not None else None
        emitted = False
        run_mode = _clean_text(action.get("run_mode") or "system").lower()
        if callable(emit_host_service_event) and run_mode in VALID_SCRIPT_RUN_MODES:
            emitted = bool(emit_host_service_event(hostname, run_mode, "quick_job_run", payload))
        if not emitted and callable(emit_agent_event) and _clean_text(device.get("agent_id")):
            emitted = bool(emit_agent_event(_clean_text(device.get("agent_id")), "quick_job_run", payload))
        if not emitted:
            return {"status": "failed", "message": "No compatible agent socket is connected for quick job delivery."}
        return {"status": "queued", "activity_id": activity_id, "message": "Script assembly queued successfully."}

    def _dispatch_workflow_assembly(
        self,
        *,
        device: Mapping[str, Any],
        assembly_record: Mapping[str, Any],
        action: Mapping[str, Any],
        incident: Mapping[str, Any],
    ) -> Dict[str, Any]:
        if self._app is None or self._adapters is None:
            return {"status": "failed", "message": "Workflow runtime is not available."}
        runtime = workflows_management.ensure_workflow_runtime(self._app, self._adapters)
        result = runtime.start_run(
            workflow_guid=_clean_text(assembly_record.get("assembly_guid")),
            source_type="manual",
            source_metadata={
                "trigger_source": "watchdog",
                "incident_id": incident.get("id"),
                "watchdog_id": incident.get("watchdog_id"),
                "hostname": _clean_text(device.get("hostname")),
                "device_guid": normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "",
                "variable_values": dict(action.get("variable_values") or {}),
            },
            created_by="watchdog",
            execute_async=True,
        )
        run = result.get("run") if isinstance(result, dict) else {}
        return {
            "status": "queued" if result.get("started") else "skipped",
            "run_id": run.get("id") if isinstance(run, dict) else None,
            "message": "Workflow queued successfully." if result.get("started") else "Workflow run was skipped.",
        }

    def _dispatch_ansible_assembly(
        self,
        *,
        assembly_record: Mapping[str, Any],
        action: Mapping[str, Any],
    ) -> Dict[str, Any]:
        payload_doc = assembly_record.get("payload_json")
        if not isinstance(payload_doc, dict):
            payload_doc = _safe_json_loads(assembly_record.get("payload"), {})
        document = _load_assembly_document(
            _clean_text(assembly_record.get("virtual_path") or "Ansible_Playbooks/Watchdog"),
            "ansible",
            payload=payload_doc,
        )
        if not document:
            return {"status": "failed", "message": "Playbook payload could not be loaded."}
        execution_context = _clean_text(action.get("execution_context") or "local").lower() or "local"
        if execution_context != "local":
            return {
                "status": "failed",
                "message": "Watchdog Ansible remediation currently supports local execution only.",
            }
        run_id = self._ansible_runner.queue_run(
            hostname=ENGINE_LOCAL_ALIAS,
            playbook_rel_path=_clean_text(assembly_record.get("virtual_path") or "Ansible_Playbooks/Watchdog"),
            playbook_name=_clean_text(document.get("name") or assembly_record.get("display_name") or "Watchdog Playbook"),
            playbook_content=(document.get("script") or "").replace("\r\n", "\n"),
            variable_values=dict(action.get("variable_values") or {}),
            payload_files=document.get("files") if isinstance(document.get("files"), list) else [],
            target_specifications=[],
            runtime_files=[],
            source="watchdog",
            activity_id=None,
            scheduled_job_id=None,
            scheduled_run_id=None,
            scheduled_job_run_row_id=None,
            credential_id=None,
            connection="local",
        )
        return {
            "status": "queued",
            "run_id": run_id,
            "message": "Ansible playbook queued successfully.",
        }

    def _run_actions(
        self,
        *,
        watchdog: Mapping[str, Any],
        device: Mapping[str, Any],
        evaluation: Mapping[str, Any],
        incident: Mapping[str, Any],
    ) -> List[Dict[str, Any]]:
        results: List[Dict[str, Any]] = []
        actions_payload = watchdog.get("actions") if isinstance(watchdog.get("actions"), dict) else {}
        actions = actions_payload.get("actions") if isinstance(actions_payload.get("actions"), list) else []
        for action in actions:
            if not isinstance(action, dict) or not _coerce_bool(action.get("enabled"), True):
                continue
            action_type = _clean_text(action.get("type")).lower()
            if action_type == "notification":
                title = _clean_text(action.get("title")) or f"{_clean_text(watchdog.get('name'))} triggered"
                message = _clean_text(evaluation.get("message")) or f"{_clean_text(device.get('hostname'))} triggered a watchdog incident."
                self._emit_broadcast_notification(title=title, message=message, variant=_clean_text(action.get("variant")).lower() or "warning")
                results.append({"type": action_type, "status": "sent", "message": title})
                continue
            if action_type == "do_nothing":
                results.append(
                    {
                        "type": action_type,
                        "status": "noop",
                        "message": "Incident recorded without notification or remediation.",
                    }
                )
                continue
            if action_type == "service_control":
                results.append(self._attempt_service_control(device=device, action=action))
                continue
            if action_type == "assembly":
                if self._assembly_runtime is None:
                    results.append({"type": action_type, "status": "failed", "message": "Assembly runtime unavailable."})
                    continue
                record = self._assembly_runtime.resolve_document_by_guid(_clean_text(action.get("assembly_guid")))
                if not isinstance(record, dict):
                    results.append({"type": action_type, "status": "failed", "message": "Assembly could not be resolved."})
                    continue
                assembly_type = _clean_text(record.get("assembly_type")).lower()
                if assembly_type == "workflow":
                    response = self._dispatch_workflow_assembly(
                        device=device,
                        assembly_record=record,
                        action=action,
                        incident=incident,
                    )
                elif assembly_type == "ansible":
                    response = self._dispatch_ansible_assembly(assembly_record=record, action=action)
                else:
                    response = self._dispatch_script_assembly(device=device, assembly_record=record, action=action, incident=incident)
                response["type"] = action_type
                response["assembly_type"] = assembly_type or "script"
                results.append(response)
        return results

    def _attempt_service_control(self, *, device: Mapping[str, Any], action: Mapping[str, Any]) -> Dict[str, Any]:
        hostname = _clean_text(device.get("hostname"))
        service_name = _clean_single_line(action.get("service_name"))
        service_action = normalize_service_action(action.get("action"))
        if not hostname or not service_name or not service_action:
            return {"type": "service_control", "status": "failed", "message": "Service control action is incomplete."}
        raw_payload = device.get("services_payload") if isinstance(device.get("services_payload"), dict) else {"services": device.get("services") or []}
        updated_services = mark_service_control_pending(raw_payload, service_name, service_action, requested_at=_now_ts(), requested_by="watchdog")
        if updated_services is None:
            return {"type": "service_control", "status": "failed", "message": f"Service {service_name} was not found."}
        emit_host_service_event = getattr(self._context, "emit_host_service_event", None) if self._context is not None else None
        emit_agent_event = getattr(self._context, "emit_agent_event", None) if self._context is not None else None
        event_payload = {
            "hostname": hostname,
            "agent_id": _clean_text(device.get("agent_id")),
            "service_name": service_name,
            "action": service_action,
            "requested_at": _now_ts(),
            "requested_by": "watchdog",
        }
        emitted = False
        if callable(emit_host_service_event):
            emitted = bool(emit_host_service_event(hostname, "system", "service_control_action", event_payload))
        if not emitted and callable(emit_agent_event) and _clean_text(device.get("agent_id")):
            emitted = bool(emit_agent_event(_clean_text(device.get("agent_id")), "service_control_action", event_payload))
        if not emitted:
            return {"type": "service_control", "status": "failed", "message": "No agent socket is connected for service remediation."}
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET services=? WHERE LOWER(hostname)=LOWER(?)",
                (serialize_device_services(updated_services), hostname),
            )
            conn.commit()
        finally:
            conn.close()
        try:
            if self._socketio is not None:
                self._socketio.emit("device_services_changed", {"hostname": hostname, "change": "updated"})
        except Exception:
            self._logger.debug("Failed to emit device_services_changed from watchdog", exc_info=True)
        return {
            "type": "service_control",
            "status": "queued",
            "message": f"{service_action.capitalize()} queued for {service_name}.",
        }

    def evaluate_watchdog(self, watchdog: Mapping[str, Any] | int) -> None:
        record = watchdog if isinstance(watchdog, dict) else self.get_watchdog(watchdog, user=None)
        if not isinstance(record, dict) or not record.get("id"):
            return
        if _coerce_bool(record.get("archived")) or not _coerce_bool(record.get("enabled"), True):
            current_state = self._load_watchdog_state(int(record["id"]))
            active_incidents = self._load_active_incidents(int(record["id"]))
            now_ts = _now_ts()
            deactivated_state = "disabled"
            resolution_reason = "archived" if _coerce_bool(record.get("archived")) else "disabled"
            conn = self._conn()
            try:
                cur = conn.cursor()
                for incident in active_incidents.values():
                    incident_id = _coerce_optional_int(incident.get("id"))
                    if incident_id:
                        self._resolve_open_incident(conn, incident_id=incident_id, reason=resolution_reason)
                for prior_state in current_state.values():
                    hostname = _clean_text(prior_state.get("hostname"))
                    if not hostname:
                        continue
                    self._upsert_state_row(
                        conn,
                        watchdog_id=int(record["id"]),
                        device_guid=_clean_text(prior_state.get("device_guid")),
                        hostname=hostname,
                        site_id=_coerce_optional_int(prior_state.get("site_id")),
                        state=deactivated_state,
                        consecutive_matches=0,
                        first_matched_at=None,
                        clear_started_at=None,
                        last_evaluated_at=now_ts,
                        last_matched_at=None,
                        last_sample={},
                        current_incident_id=None,
                        last_action_at=_coerce_optional_int(prior_state.get("last_action_at")),
                    )
                cur.execute("UPDATE watchdogs SET last_evaluated_at=? WHERE id=?", (now_ts, int(record["id"])))
                conn.commit()
            finally:
                conn.close()
            for prior_state in current_state.values():
                hostname = _clean_text(prior_state.get("hostname"))
                if hostname:
                    self._emit_watchdog_refresh(hostname=hostname, watchdog_id=int(record["id"]))
            if not current_state:
                self._emit_watchdog_refresh(watchdog_id=int(record["id"]))
            return
        resolved_devices = self.resolve_targets(record)
        overrides = self._load_active_overrides(int(record["id"]))
        current_state = self._load_watchdog_state(int(record["id"]))
        active_incidents = self._load_active_incidents(int(record["id"]))
        now_ts = _now_ts()
        touched_hosts: set[str] = set()
        conn = self._conn()
        try:
            cur = conn.cursor()
            for device in resolved_devices:
                hostname = _clean_text(device.get("hostname"))
                host_key = hostname.lower()
                if not host_key:
                    continue
                touched_hosts.add(host_key)
                prior_state = current_state.get(host_key) or {}
                override = overrides.get(host_key)
                incident = active_incidents.get(host_key)
                if override and _clean_text(override.get("state")).lower() in VALID_DEVICE_OVERRIDE_STATES:
                    if incident:
                        self._set_incident_state(
                            conn,
                            incident_id=int(incident["id"]),
                            state="suppressed",
                            reason=_clean_text(override.get("state")) or "suppressed",
                        )
                        incident = {
                            **incident,
                            "state": "suppressed",
                            "resolved_at": None,
                            "resolution_reason": _clean_text(override.get("state")) or "suppressed",
                            "updated_at": now_ts,
                        }
                    self._upsert_state_row(
                        conn,
                        watchdog_id=int(record["id"]),
                        device_guid=normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "",
                        hostname=hostname,
                        site_id=_coerce_optional_int(device.get("site_id")),
                        state=_clean_text(override.get("state")).lower(),
                        consecutive_matches=0,
                        first_matched_at=None,
                        clear_started_at=None,
                        last_evaluated_at=now_ts,
                        last_matched_at=None,
                        last_sample={"override": override},
                        current_incident_id=_coerce_optional_int(incident.get("id")) if isinstance(incident, dict) else None,
                        last_action_at=_coerce_optional_int(prior_state.get("last_action_at")),
                    )
                    continue
                evaluation = self._evaluate_device(record, device)
                matched = bool(evaluation.get("matched"))
                stale_state = _clean_text(evaluation.get("state")).lower() == "stale_data"
                consecutive_matches = _coerce_int(prior_state.get("consecutive_matches"), 0)
                first_matched_at = _coerce_optional_int(prior_state.get("first_matched_at"))
                clear_started_at = _coerce_optional_int(prior_state.get("clear_started_at"))
                last_action_at = _coerce_optional_int(prior_state.get("last_action_at"))
                incident_id = _coerce_optional_int(prior_state.get("current_incident_id"))
                if matched:
                    consecutive_matches += 1
                    if first_matched_at is None:
                        first_matched_at = now_ts
                    clear_started_at = None
                    if consecutive_matches >= _coerce_int(record.get("min_consecutive_matches"), 1):
                        action_summary: List[Dict[str, Any]] = []
                        incident = self._create_or_update_incident(
                            conn,
                            watchdog=record,
                            device=device,
                            current_incident=incident,
                            evaluation=evaluation,
                            action_summary=[],
                        )
                        incident_id = int(incident["id"])
                        incident_state = _clean_text(incident.get("state")).lower() or "open"
                        should_run_actions = incident_state == "open" and (
                            last_action_at is None
                            or _coerce_int(record.get("cooldown_seconds"), DEFAULT_WATCHDOG_COOLDOWN_SECONDS) <= 0
                            or (now_ts - int(last_action_at)) >= _coerce_int(record.get("cooldown_seconds"), DEFAULT_WATCHDOG_COOLDOWN_SECONDS)
                        )
                        if should_run_actions:
                            action_summary = self._run_actions(
                                watchdog=record,
                                device=device,
                                evaluation=evaluation,
                                incident={**incident, "watchdog_id": int(record["id"])},
                            )
                            if action_summary:
                                incident = self._create_or_update_incident(
                                    conn,
                                    watchdog=record,
                                    device=device,
                                    current_incident=incident,
                                    evaluation=evaluation,
                                    action_summary=action_summary,
                                )
                                incident_id = int(incident["id"])
                            last_action_at = now_ts
                        state_name = "suppressed" if incident_state == "suppressed" else "triggered"
                        last_matched_at = now_ts
                    else:
                        state_name = "pending"
                        last_matched_at = None
                else:
                    consecutive_matches = 0
                    first_matched_at = None
                    last_matched_at = None
                    state_name = _clean_text(evaluation.get("state")).lower() or ("stale_data" if stale_state else "normal")
                    if incident:
                        auto_resolve_after_seconds = _coerce_int(record.get("auto_resolve_after_seconds"), DEFAULT_WATCHDOG_AUTO_RESOLVE_SECONDS)
                        if stale_state:
                            clear_started_at = now_ts if clear_started_at is None else clear_started_at
                        elif clear_started_at is None:
                            clear_started_at = now_ts
                        if auto_resolve_after_seconds <= 0 or (now_ts - int(clear_started_at or now_ts)) >= auto_resolve_after_seconds:
                            self._resolve_open_incident(
                                conn,
                                incident_id=int(incident["id"]),
                                reason="cleared" if not stale_state else "telemetry_stale",
                            )
                            incident_id = None
                            clear_started_at = None
                    else:
                        clear_started_at = None
                        incident_id = None
                self._upsert_state_row(
                    conn,
                    watchdog_id=int(record["id"]),
                    device_guid=normalize_guid(device.get("guid") or device.get("agent_guid") or "") or "",
                    hostname=hostname,
                    site_id=_coerce_optional_int(device.get("site_id")),
                    state=state_name,
                    consecutive_matches=consecutive_matches,
                    first_matched_at=first_matched_at,
                    clear_started_at=clear_started_at,
                    last_evaluated_at=now_ts,
                    last_matched_at=last_matched_at,
                    last_sample=evaluation.get("sample") if isinstance(evaluation.get("sample"), dict) else {},
                    current_incident_id=incident_id,
                    last_action_at=last_action_at,
                )
            for host_key, prior_state in current_state.items():
                if host_key in touched_hosts:
                    continue
                incident_id = _coerce_optional_int(prior_state.get("current_incident_id"))
                if incident_id:
                    self._resolve_open_incident(conn, incident_id=incident_id, reason="target_removed")
                self._upsert_state_row(
                    conn,
                    watchdog_id=int(record["id"]),
                    device_guid=_clean_text(prior_state.get("device_guid")),
                    hostname=_clean_text(prior_state.get("hostname")),
                    site_id=_coerce_optional_int(prior_state.get("site_id")),
                    state="normal",
                    consecutive_matches=0,
                    first_matched_at=None,
                    clear_started_at=None,
                    last_evaluated_at=now_ts,
                    last_matched_at=None,
                    last_sample={},
                    current_incident_id=None,
                    last_action_at=_coerce_optional_int(prior_state.get("last_action_at")),
                )
            cur.execute("UPDATE watchdogs SET last_evaluated_at=? WHERE id=?", (now_ts, int(record["id"])))
            conn.commit()
        finally:
            conn.close()
        for device in resolved_devices:
            self._emit_watchdog_refresh(hostname=_clean_text(device.get("hostname")), watchdog_id=int(record["id"]))
        if not resolved_devices:
            self._emit_watchdog_refresh(watchdog_id=int(record["id"]))

    def evaluate_due_watchdogs(self) -> None:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, last_evaluated_at, evaluation_interval_seconds
                  FROM watchdogs
                 WHERE COALESCE(archived, 0) = 0
                   AND COALESCE(enabled, 1) = 1
                """
            )
            candidates = cur.fetchall()
        finally:
            conn.close()
        now_ts = _now_ts()
        for row in candidates:
            watchdog_id = _coerce_int(row[0], 0)
            last_evaluated_at = _coerce_int(row[1], 0)
            interval_seconds = max(30, _coerce_int(row[2], DEFAULT_WATCHDOG_EVALUATION_INTERVAL_SECONDS))
            if watchdog_id <= 0:
                continue
            if last_evaluated_at > 0 and (now_ts - last_evaluated_at) < interval_seconds:
                continue
            self.evaluate_watchdog(watchdog_id)

    def list_incidents(
        self,
        *,
        user: Optional[Mapping[str, Any]] = None,
        state: str = "open",
    ) -> List[Dict[str, Any]]:
        normalized_state = _clean_text(state).lower()
        if normalized_state not in VALID_INCIDENT_QUERY_STATES:
            normalized_state = "open"
        conn = self._conn()
        try:
            cur = conn.cursor()
            base_query = """
                SELECT
                    i.id, i.watchdog_id, i.device_guid, i.hostname, i.site_id, i.severity, i.state,
                    i.title, i.message, i.sample_json, i.rule_summary_json, i.action_summary_json,
                    i.opened_at, i.updated_at, i.resolved_at, i.resolution_reason,
                    i.acknowledged_at, i.acknowledged_by,
                    w.name, w.description
                  FROM watchdog_incidents AS i
                  JOIN watchdogs AS w ON w.id = i.watchdog_id
            """
            if normalized_state == "all":
                cur.execute(f"{base_query} ORDER BY i.updated_at DESC, i.id DESC")
            else:
                cur.execute(
                    f"{base_query} WHERE i.state = ? ORDER BY i.updated_at DESC, i.id DESC",
                    (normalized_state,),
                )
            rows = cur.fetchall()
        finally:
            conn.close()
        site_names = self._load_site_name_map(
            {_coerce_optional_int((row["site_id"] if isinstance(row, sqlite3.Row) else row[4])) for row in rows}
        )
        visible_watchdogs = {int(item["id"]) for item in self.list_watchdogs(user=user)}
        allowed_site_ids = self._site_access.site_ids_for_user(user)
        if allowed_site_ids is not None and not visible_watchdogs:
            return []
        incidents: List[Dict[str, Any]] = []
        for row in rows:
            watchdog_id = _coerce_int(row["watchdog_id"] if isinstance(row, sqlite3.Row) else row[1], 0)
            if allowed_site_ids is not None and watchdog_id not in visible_watchdogs:
                continue
            incident = {
                "id": _coerce_int(row["id"] if isinstance(row, sqlite3.Row) else row[0], 0),
                "watchdog_id": watchdog_id,
                "device_guid": normalize_guid(row["device_guid"] if isinstance(row, sqlite3.Row) else row[2]) or "",
                "hostname": _clean_text(row["hostname"] if isinstance(row, sqlite3.Row) else row[3]),
                "site_id": _coerce_optional_int(row["site_id"] if isinstance(row, sqlite3.Row) else row[4]),
                "severity": _normalize_watchdog_severity(row["severity"] if isinstance(row, sqlite3.Row) else row[5]),
                "state": _clean_text(row["state"] if isinstance(row, sqlite3.Row) else row[6]).lower(),
                "title": _clean_text(row["title"] if isinstance(row, sqlite3.Row) else row[7]),
                "message": _clean_text(row["message"] if isinstance(row, sqlite3.Row) else row[8]),
                "sample": _safe_json_loads(row["sample_json"] if isinstance(row, sqlite3.Row) else row[9], {}),
                "rule_summary": _safe_json_loads(row["rule_summary_json"] if isinstance(row, sqlite3.Row) else row[10], []),
                "action_summary": _safe_json_loads(row["action_summary_json"] if isinstance(row, sqlite3.Row) else row[11], []),
                "opened_at": _coerce_int(row["opened_at"] if isinstance(row, sqlite3.Row) else row[12], 0),
                "updated_at": _coerce_int(row["updated_at"] if isinstance(row, sqlite3.Row) else row[13], 0),
                "resolved_at": _coerce_optional_int(row["resolved_at"] if isinstance(row, sqlite3.Row) else row[14]),
                "resolution_reason": _clean_text(row["resolution_reason"] if isinstance(row, sqlite3.Row) else row[15]),
                "acknowledged_at": _coerce_optional_int(row["acknowledged_at"] if isinstance(row, sqlite3.Row) else row[16]),
                "acknowledged_by": _clean_text(row["acknowledged_by"] if isinstance(row, sqlite3.Row) else row[17]),
                "watchdog_name": _clean_text(row["name"] if isinstance(row, sqlite3.Row) else row[18]),
                "watchdog_description": _clean_text(row["description"] if isinstance(row, sqlite3.Row) else row[19]),
            }
            incident["site_name"] = site_names.get(incident["site_id"], "") if incident.get("site_id") is not None else ""
            incidents.append(incident)
        return incidents

    def list_incident_counts(
        self,
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Dict[str, int]:
        counts = {state: 0 for state in VALID_INCIDENT_STATES}
        for incident in self.list_incidents(user=user, state="all"):
            state = _clean_text(incident.get("state")).lower()
            if state in counts:
                counts[state] += 1
        return counts

    def acknowledge_incident(
        self,
        incident_id: Any,
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Optional[Dict[str, Any]]:
        incidents = self.list_incidents(user=user, state="open")
        target = next((item for item in incidents if int(item["id"]) == _coerce_int(incident_id, 0)), None)
        if target is None:
            return None
        conn = self._conn()
        try:
            cur = conn.cursor()
            now_ts = _now_ts()
            cur.execute(
                """
                UPDATE watchdog_incidents
                   SET acknowledged_at=?,
                       acknowledged_by=?,
                       updated_at=?
                 WHERE id=?
                """,
                (now_ts, _clean_text((user or {}).get("username")) or "Unknown", now_ts, int(target["id"])),
            )
            conn.commit()
        finally:
            conn.close()
        self._emit_watchdog_refresh(hostname=target.get("hostname") or "", watchdog_id=target.get("watchdog_id"))
        refreshed = self.list_incidents(user=user, state="open")
        return next((item for item in refreshed if int(item["id"]) == int(target["id"])), None)

    def update_incident_state(
        self,
        incident_id: Any,
        *,
        state: str,
        user: Optional[Mapping[str, Any]] = None,
        reason: str = "",
    ) -> Tuple[Optional[Dict[str, Any]], List[str]]:
        target_id = _coerce_int(incident_id, 0)
        desired_state = _clean_text(state).lower()
        if desired_state not in {"open", "suppressed"}:
            return None, ["Unsupported incident state transition."]
        incidents = self.list_incidents(user=user, state="all")
        target = next((item for item in incidents if int(item["id"]) == target_id), None)
        if target is None:
            return None, ["Incident not found."]
        current_state = _clean_text(target.get("state")).lower()
        if current_state == "resolved":
            return None, ["Resolved incidents are historical records and cannot be reopened."]
        if current_state == desired_state:
            return target, []

        conn = self._conn()
        try:
            cur = conn.cursor()
            if desired_state == "open":
                cur.execute(
                    "DELETE FROM watchdog_device_overrides WHERE watchdog_id=? AND LOWER(hostname)=LOWER(?)",
                    (int(target["watchdog_id"]), _clean_text(target.get("hostname"))),
                )
            self._set_incident_state(
                conn,
                incident_id=target_id,
                state=desired_state,
                reason=reason if desired_state == "suppressed" else "",
            )
            cur.execute(
                """
                SELECT
                    state, consecutive_matches, first_matched_at, clear_started_at, last_evaluated_at,
                    last_matched_at, last_sample_json, last_action_at, device_guid, hostname, site_id
                  FROM watchdog_device_state
                 WHERE watchdog_id=? AND LOWER(hostname)=LOWER(?)
                 LIMIT 1
                """,
                (int(target["watchdog_id"]), _clean_text(target.get("hostname"))),
            )
            state_row = cur.fetchone()
            if state_row:
                next_device_state = "suppressed" if desired_state == "suppressed" else "triggered"
                self._upsert_state_row(
                    conn,
                    watchdog_id=int(target["watchdog_id"]),
                    device_guid=normalize_guid(state_row[8]) or target.get("device_guid") or "",
                    hostname=_clean_text(state_row[9]) or _clean_text(target.get("hostname")),
                    site_id=_coerce_optional_int(state_row[10]) if state_row[10] is not None else _coerce_optional_int(target.get("site_id")),
                    state=next_device_state,
                    consecutive_matches=_coerce_int(state_row[1], 0),
                    first_matched_at=_coerce_optional_int(state_row[2]),
                    clear_started_at=None if desired_state == "open" else _coerce_optional_int(state_row[3]),
                    last_evaluated_at=max(_coerce_int(state_row[4], 0), _now_ts()),
                    last_matched_at=_coerce_optional_int(state_row[5]),
                    last_sample=_safe_json_loads(state_row[6], {}),
                    current_incident_id=target_id,
                    last_action_at=_coerce_optional_int(state_row[7]),
                )
            conn.commit()
        finally:
            conn.close()
        self._emit_watchdog_refresh(hostname=target.get("hostname") or "", watchdog_id=target.get("watchdog_id"))
        refreshed = self.list_incidents(user=user, state=desired_state)
        return next((item for item in refreshed if int(item["id"]) == target_id), None), []

    def _resolve_device_reference(
        self,
        device_id: Any,
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Optional[Dict[str, Any]]:
        raw = _clean_text(device_id)
        if not raw:
            return None
        conn = self._conn()
        try:
            cur = conn.cursor()
            if _GUID_RE.match(raw):
                cur.execute(
                    """
                    SELECT d.guid, d.hostname, ds.site_id, s.name
                      FROM devices AS d
                 LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 LEFT JOIN sites AS s ON s.id = ds.site_id
                     WHERE UPPER(d.guid) = UPPER(?)
                  ORDER BY d.last_seen DESC
                     LIMIT 1
                    """,
                    (normalize_guid(raw) or raw,),
                )
                row = cur.fetchone()
            else:
                cur.execute(
                    """
                    SELECT d.guid, d.hostname, ds.site_id, s.name
                      FROM devices AS d
                 LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 LEFT JOIN sites AS s ON s.id = ds.site_id
                     WHERE LOWER(d.hostname) = LOWER(?)
                  ORDER BY d.last_seen DESC
                     LIMIT 1
                    """,
                    (raw,),
                )
                row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            return None
        device = {
            "device_guid": normalize_guid(row[0]) or "",
            "hostname": _clean_text(row[1]),
            "site_id": _coerce_optional_int(row[2]),
            "site_name": _clean_text(row[3]),
        }
        if device["device_guid"] and not self._site_access.user_can_access_guid(user, device["device_guid"]):
            return None
        if not device["device_guid"] and not self._site_access.user_can_access_hostname(user, device["hostname"]):
            return None
        return device

    def get_device_watchdogs(
        self,
        device_id: Any,
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Optional[Dict[str, Any]]:
        device_ref = self._resolve_device_reference(device_id, user=user)
        if device_ref is None:
            return None
        hostname_key = _clean_text(device_ref.get("hostname")).lower()
        watchdogs = self.list_watchdogs(user=user)
        assignments: List[Dict[str, Any]] = []
        for watchdog in watchdogs:
            targets = self.resolve_targets(watchdog)
            target = next((entry for entry in targets if _clean_text(entry.get("hostname")).lower() == hostname_key), None)
            if target is None:
                continue
            state_lookup = self._load_watchdog_state(int(watchdog["id"]))
            override_lookup = self._load_active_overrides(int(watchdog["id"]))
            open_incident_lookup = self._load_open_incidents(int(watchdog["id"]))
            current_state = state_lookup.get(hostname_key) or {}
            current_override = override_lookup.get(hostname_key)
            current_incident = open_incident_lookup.get(hostname_key)
            assignments.append(
                {
                    "watchdog_id": int(watchdog["id"]),
                    "name": watchdog["name"],
                    "description": watchdog["description"],
                    "enabled": bool(watchdog.get("enabled")),
                    "severity": watchdog.get("severity"),
                    "state": _clean_text(current_state.get("state")).lower() or "normal",
                    "last_evaluated_at": _coerce_int(current_state.get("last_evaluated_at"), 0),
                    "rule_summaries": list(watchdog.get("rule_summaries") or []),
                    "action_summaries": list(watchdog.get("action_summaries") or []),
                    "sample": current_state.get("last_sample") if isinstance(current_state.get("last_sample"), dict) else {},
                    "override": current_override,
                    "active_incident": current_incident,
                }
            )
        incidents = [
            incident
            for incident in self.list_incidents(user=user, state="open")
            if _clean_text(incident.get("hostname")).lower() == hostname_key
        ]
        overrides = []
        for watchdog in watchdogs:
            override = self._load_active_overrides(int(watchdog["id"])).get(hostname_key)
            if override:
                overrides.append({**override, "watchdog_id": int(watchdog["id"]), "watchdog_name": watchdog["name"]})
        return {
            "device": device_ref,
            "assignments": assignments,
            "incidents": incidents,
            "overrides": overrides,
        }

    def upsert_device_override(
        self,
        device_id: Any,
        payload: Mapping[str, Any],
        *,
        user: Optional[Mapping[str, Any]] = None,
    ) -> Tuple[Optional[Dict[str, Any]], List[str]]:
        device_ref = self._resolve_device_reference(device_id, user=user)
        if device_ref is None:
            return None, ["Device not found."]
        watchdog_id = _coerce_optional_int(payload.get("watchdog_id") or payload.get("id"))
        watchdog = self.get_watchdog(watchdog_id, user=user) if watchdog_id is not None else None
        if watchdog is None:
            return None, ["Watchdog not found."]
        clear_override = _coerce_bool(payload.get("clear"), False)
        state = _clean_text(payload.get("state") or "suppressed").lower()
        if state not in VALID_DEVICE_OVERRIDE_STATES:
            state = "suppressed"
        conn = self._conn()
        try:
            cur = conn.cursor()
            if clear_override:
                cur.execute(
                    "DELETE FROM watchdog_device_overrides WHERE watchdog_id=? AND LOWER(hostname)=LOWER(?)",
                    (int(watchdog["id"]), device_ref["hostname"]),
                )
            else:
                cur.execute(
                    """
                    INSERT INTO watchdog_device_overrides (
                        watchdog_id, device_guid, hostname, site_id, state, reason, created_by,
                        created_at, expires_at, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        int(watchdog["id"]),
                        device_ref["device_guid"],
                        device_ref["hostname"],
                        device_ref.get("site_id"),
                        state,
                        _clean_single_line(payload.get("reason") or f"Temporarily {state} by operator."),
                        _clean_text((user or {}).get("username")) or "Unknown",
                        _now_ts(),
                        _normalize_timestamp_input(payload.get("expires_at")),
                        _now_ts(),
                    ),
                )
            conn.commit()
        finally:
            conn.close()
        self.evaluate_watchdog(watchdog)
        self._emit_watchdog_refresh(hostname=device_ref["hostname"], watchdog_id=int(watchdog["id"]))
        return self.get_device_watchdogs(device_ref["device_guid"] or device_ref["hostname"], user=user), []
