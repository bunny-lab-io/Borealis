# ======================================================
# Data\Engine\services\workflows\runtime.py
# Description: Executes Borealis workflows as one-shot server-side runs with
#              persisted workflow/node history, child job linkage, and trigger routing.
# ======================================================

"""Workflow runtime services for the Borealis Engine."""

from __future__ import annotations

import base64
import json
import logging
import secrets
import threading
import time
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from ...db import dbapi as sqlite3
from ..API.assemblies.execution import (
    _SUPPORTED_AGENT_SCRIPT_TYPES,
    _load_assembly_document,
    _normalize_agent_script_type,
    _normalize_script_relpath,
    _normalize_target_service_mode,
    _rewrite_script_for_dispatch,
    prepare_variable_context,
)
from ..API.devices.session_dispatch import build_currentuser_dispatch_fields
from ..ansible.ssh_auth import apply_ssh_credential_host_vars
from ..assemblies.service import AssemblyRuntimeService
from ..filters.matcher import DeviceFilterMatcher


WORKFLOW_STATUS_PENDING = "Pending"
WORKFLOW_STATUS_RUNNING = "Running"
WORKFLOW_STATUS_SUCCESS = "Success"
WORKFLOW_STATUS_WARNING = "Warning"
WORKFLOW_STATUS_FAILED = "Failed"
WORKFLOW_STATUS_TIMED_OUT = "Timed Out"
WORKFLOW_STATUS_SKIPPED = "Skipped"

WORKFLOW_TERMINAL_STATUSES = {
    WORKFLOW_STATUS_SUCCESS,
    WORKFLOW_STATUS_WARNING,
    WORKFLOW_STATUS_FAILED,
    WORKFLOW_STATUS_TIMED_OUT,
    WORKFLOW_STATUS_SKIPPED,
}

NODE_TYPE_TRIGGER_MANUAL = "workflow_trigger_manual"
NODE_TYPE_TRIGGER_SCHEDULED = "workflow_trigger_scheduled_job"
NODE_TYPE_TRIGGER_WEBHOOK = "workflow_trigger_webhook"
NODE_TYPE_AGENT_FILTER = "workflow_agent_filter"
NODE_TYPE_AGENT_ARRAY = "workflow_agent_array"
NODE_TYPE_EXECUTE_ASSEMBLY = "workflow_execute_assembly"
NODE_TYPE_EXECUTE_SUBWORKFLOW = "workflow_execute_subworkflow"

SUPPORTED_NODE_TYPES = {
    NODE_TYPE_TRIGGER_MANUAL,
    NODE_TYPE_TRIGGER_SCHEDULED,
    NODE_TYPE_TRIGGER_WEBHOOK,
    NODE_TYPE_AGENT_FILTER,
    NODE_TYPE_AGENT_ARRAY,
    NODE_TYPE_EXECUTE_ASSEMBLY,
    NODE_TYPE_EXECUTE_SUBWORKFLOW,
}

EDGE_ROUTE_ALWAYS = "always"
EDGE_ROUTE_ON_SUCCESS = "on_success"
EDGE_ROUTE_ON_WARNING = "on_warning"
EDGE_ROUTE_ON_FAILED = "on_failed"
VALID_EDGE_ROUTES = {
    EDGE_ROUTE_ALWAYS,
    EDGE_ROUTE_ON_SUCCESS,
    EDGE_ROUTE_ON_WARNING,
    EDGE_ROUTE_ON_FAILED,
}

PORT_DIRECTION_INPUT = "input"
PORT_DIRECTION_OUTPUT = "output"
PORT_KIND_ACTION = "action"
PORT_KIND_DATA = "data"
PORT_CARDINALITY_SINGLE = "single"
PORT_CARDINALITY_MULTI = "multi"


def _port(
    port_id: str,
    label: str,
    *,
    direction: str,
    kind: str,
    cardinality: str = PORT_CARDINALITY_MULTI,
    required: bool = False,
) -> Dict[str, Any]:
    return {
        "id": str(port_id),
        "label": str(label),
        "direction": direction,
        "kind": kind,
        "cardinality": cardinality,
        "required": bool(required),
    }


NODE_PORTS: Dict[str, Dict[str, List[Dict[str, Any]]]] = {
    NODE_TYPE_TRIGGER_MANUAL: {
        PORT_DIRECTION_INPUT: [],
        PORT_DIRECTION_OUTPUT: [
            _port("action", "Action", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_ACTION),
        ],
    },
    NODE_TYPE_TRIGGER_SCHEDULED: {
        PORT_DIRECTION_INPUT: [],
        PORT_DIRECTION_OUTPUT: [
            _port("action", "Action", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_ACTION),
        ],
    },
    NODE_TYPE_TRIGGER_WEBHOOK: {
        PORT_DIRECTION_INPUT: [],
        PORT_DIRECTION_OUTPUT: [
            _port("action", "Action", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_ACTION),
        ],
    },
    NODE_TYPE_AGENT_FILTER: {
        PORT_DIRECTION_INPUT: [],
        PORT_DIRECTION_OUTPUT: [
            _port("targets", "Targets", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_DATA),
        ],
    },
    NODE_TYPE_AGENT_ARRAY: {
        PORT_DIRECTION_INPUT: [],
        PORT_DIRECTION_OUTPUT: [
            _port("targets", "Targets", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_DATA),
        ],
    },
    NODE_TYPE_EXECUTE_ASSEMBLY: {
        PORT_DIRECTION_INPUT: [
            _port(
                "trigger",
                "Trigger",
                direction=PORT_DIRECTION_INPUT,
                kind=PORT_KIND_ACTION,
                required=True,
            ),
            _port("targets", "Targets", direction=PORT_DIRECTION_INPUT, kind=PORT_KIND_DATA, required=True),
        ],
        PORT_DIRECTION_OUTPUT: [
            _port("action", "Action", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_ACTION),
            _port("job_output", "Job Output", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_DATA),
        ],
    },
    NODE_TYPE_EXECUTE_SUBWORKFLOW: {
        PORT_DIRECTION_INPUT: [
            _port(
                "trigger",
                "Trigger",
                direction=PORT_DIRECTION_INPUT,
                kind=PORT_KIND_ACTION,
                required=True,
            ),
        ],
        PORT_DIRECTION_OUTPUT: [
            _port("action", "Action", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_ACTION),
            _port("job_output", "Job Output", direction=PORT_DIRECTION_OUTPUT, kind=PORT_KIND_DATA),
        ],
    },
}

TRIGGER_NODE_TYPE_BY_SOURCE = {
    "manual": NODE_TYPE_TRIGGER_MANUAL,
    "scheduled_job": NODE_TYPE_TRIGGER_SCHEDULED,
    "webhook": NODE_TYPE_TRIGGER_WEBHOOK,
    # Child workflow launches reuse the manual trigger path in v1.
    "subworkflow": NODE_TYPE_TRIGGER_MANUAL,
}

ENGINE_LOCAL_ALIAS = "borealis-engine-01"
SCRIPT_RUNNING_STATUSES = {
    WORKFLOW_STATUS_PENDING,
    WORKFLOW_STATUS_RUNNING,
    "",
}
REMOTE_ONLINE_LOOKBACK_SECONDS = 300
POLL_INTERVAL_SECONDS = 0.5
MAX_STDIO_SUMMARY_CHARS = 200000


def _now_ts() -> int:
    return int(time.time())


def _truncate_text(value: Any, *, limit: int = MAX_STDIO_SUMMARY_CHARS) -> str:
    text = str(value or "")
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 3)] + "..."


def _json_dumps(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def _parse_json_object(value: Any, *, default: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
    if isinstance(value, dict):
        return dict(value)
    if isinstance(value, str):
        raw = value.strip()
        if not raw:
            return dict(default or {})
        try:
            parsed = json.loads(raw)
        except Exception:
            return dict(default or {})
        if isinstance(parsed, dict):
            return parsed
    return dict(default or {})


def _parse_json_array(value: Any) -> List[Any]:
    if isinstance(value, list):
        return list(value)
    if isinstance(value, tuple):
        return list(value)
    if isinstance(value, str):
        raw = value.strip()
        if not raw:
            return []
        try:
            parsed = json.loads(raw)
        except Exception:
            return []
        if isinstance(parsed, list):
            return parsed
    return []


def _decode_base64_json(value: Any) -> Optional[Dict[str, Any]]:
    if isinstance(value, dict):
        return dict(value)
    if not isinstance(value, str):
        return None
    text = value.strip()
    if not text:
        return {}
    try:
        decoded = base64.b64decode(text.encode("ascii"), validate=True).decode("utf-8")
    except Exception:
        decoded = text
    try:
        parsed = json.loads(decoded)
    except Exception:
        return None
    if isinstance(parsed, dict):
        return parsed
    return None


def _normalize_status(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized in {"success", "succeeded"}:
        return WORKFLOW_STATUS_SUCCESS
    if normalized in {"warning", "warn"}:
        return WORKFLOW_STATUS_WARNING
    if normalized in {"failed", "failure", "error"}:
        return WORKFLOW_STATUS_FAILED
    if normalized in {"timed out", "timed_out", "timeout"}:
        return WORKFLOW_STATUS_TIMED_OUT
    if normalized in {"skipped", "inactive"}:
        return WORKFLOW_STATUS_SKIPPED
    if normalized in {"running"}:
        return WORKFLOW_STATUS_RUNNING
    if normalized in {"pending"}:
        return WORKFLOW_STATUS_PENDING
    return WORKFLOW_STATUS_FAILED if normalized else WORKFLOW_STATUS_PENDING


def _status_priority(status: str) -> int:
    order = {
        WORKFLOW_STATUS_FAILED: 60,
        WORKFLOW_STATUS_TIMED_OUT: 50,
        WORKFLOW_STATUS_WARNING: 40,
        WORKFLOW_STATUS_SUCCESS: 30,
        WORKFLOW_STATUS_RUNNING: 20,
        WORKFLOW_STATUS_PENDING: 10,
        WORKFLOW_STATUS_SKIPPED: 0,
    }
    return order.get(_normalize_status(status), 0)


def _rollup_status(statuses: Iterable[Any]) -> str:
    normalized = [_normalize_status(status) for status in statuses if str(status or "").strip()]
    if not normalized:
        return WORKFLOW_STATUS_PENDING
    if any(status == WORKFLOW_STATUS_FAILED for status in normalized):
        return WORKFLOW_STATUS_FAILED
    if any(status == WORKFLOW_STATUS_TIMED_OUT for status in normalized):
        return WORKFLOW_STATUS_TIMED_OUT
    if any(status == WORKFLOW_STATUS_WARNING for status in normalized):
        return WORKFLOW_STATUS_WARNING
    if any(status == WORKFLOW_STATUS_RUNNING for status in normalized):
        return WORKFLOW_STATUS_RUNNING
    if any(status == WORKFLOW_STATUS_PENDING for status in normalized):
        return WORKFLOW_STATUS_PENDING
    if any(status == WORKFLOW_STATUS_SUCCESS for status in normalized):
        return WORKFLOW_STATUS_SUCCESS
    return WORKFLOW_STATUS_SKIPPED


def _edge_route(edge: Mapping[str, Any]) -> str:
    data = edge.get("data") if isinstance(edge.get("data"), Mapping) else {}
    route = str(data.get("route_on") or data.get("routeOn") or EDGE_ROUTE_ALWAYS).strip().lower()
    return route if route in VALID_EDGE_ROUTES else EDGE_ROUTE_ALWAYS


def _port_id(value: Any) -> str:
    return str(value or "").strip().lower()


def _node_ports(node_type: str, *, direction: str) -> List[Dict[str, Any]]:
    contract = NODE_PORTS.get(str(node_type or "").strip(), {})
    ports = contract.get(direction) if isinstance(contract, Mapping) else []
    return [dict(port) for port in (ports or []) if isinstance(port, Mapping)]


def _node_port(node_type: str, *, direction: str, port_id: Any) -> Optional[Dict[str, Any]]:
    normalized = _port_id(port_id)
    for port in _node_ports(node_type, direction=direction):
        if _port_id(port.get("id")) == normalized:
            return port
    return None


def _edge_source_port(edge: Mapping[str, Any], node_map: Mapping[str, Mapping[str, Any]]) -> Optional[Dict[str, Any]]:
    source_node = node_map.get(str(edge.get("source") or "").strip()) or {}
    return _node_port(
        str(source_node.get("type") or "").strip(),
        direction=PORT_DIRECTION_OUTPUT,
        port_id=edge.get("sourceHandle"),
    )


def _edge_target_port(edge: Mapping[str, Any], node_map: Mapping[str, Mapping[str, Any]]) -> Optional[Dict[str, Any]]:
    target_node = node_map.get(str(edge.get("target") or "").strip()) or {}
    return _node_port(
        str(target_node.get("type") or "").strip(),
        direction=PORT_DIRECTION_INPUT,
        port_id=edge.get("targetHandle"),
    )


def _is_job_output_route_edge(edge: Mapping[str, Any], node_map: Mapping[str, Mapping[str, Any]]) -> bool:
    source_port = _edge_source_port(edge, node_map)
    target_port = _edge_target_port(edge, node_map)
    return bool(
        source_port
        and target_port
        and str(source_port.get("kind") or "") == PORT_KIND_DATA
        and str(target_port.get("kind") or "") == PORT_KIND_DATA
        and _port_id(source_port.get("id")) == "job_output"
    )


def _match_edge_route(route: str, status: str) -> bool:
    normalized_status = _normalize_status(status)
    if normalized_status == WORKFLOW_STATUS_SKIPPED:
        return False
    if route == EDGE_ROUTE_ALWAYS:
        return normalized_status in {
            WORKFLOW_STATUS_SUCCESS,
            WORKFLOW_STATUS_WARNING,
            WORKFLOW_STATUS_FAILED,
            WORKFLOW_STATUS_TIMED_OUT,
        }
    if route == EDGE_ROUTE_ON_SUCCESS:
        return normalized_status == WORKFLOW_STATUS_SUCCESS
    if route == EDGE_ROUTE_ON_WARNING:
        return normalized_status == WORKFLOW_STATUS_WARNING
    if route == EDGE_ROUTE_ON_FAILED:
        return normalized_status in {WORKFLOW_STATUS_FAILED, WORKFLOW_STATUS_TIMED_OUT}
    return False


def _match_data_edge(status: str) -> bool:
    return _normalize_status(status) in {
        WORKFLOW_STATUS_SUCCESS,
        WORKFLOW_STATUS_WARNING,
        WORKFLOW_STATUS_FAILED,
        WORKFLOW_STATUS_TIMED_OUT,
    }


def _coerce_int(value: Any, *, default: int = 0) -> int:
    try:
        return int(value)
    except Exception:
        return default


def _coerce_optional_int(value: Any) -> Optional[int]:
    try:
        if value in (None, "", "null"):
            return None
        return int(value)
    except Exception:
        return None


def _coerce_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    text = str(value or "").strip().lower()
    return text in {"1", "true", "yes", "on"}


def _node_label(node: Mapping[str, Any]) -> str:
    data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
    return str(data.get("label") or node.get("label") or node.get("type") or "Node")


class WorkflowRuntimeService:
    """Execute saved Borealis workflow assemblies as persisted server-side runs."""

    def __init__(
        self,
        *,
        db_conn_factory: Callable[[], sqlite3.Connection],
        assembly_runtime: AssemblyRuntimeService,
        script_signer: Any = None,
        socketio: Any = None,
        service_log: Optional[Callable[[str, str, Optional[str]], None]] = None,
        logger: Optional[logging.Logger] = None,
        ansible_runner: Optional[Any] = None,
    ) -> None:
        self._db_conn_factory = db_conn_factory
        self._assembly_runtime = assembly_runtime
        self._script_signer = script_signer
        self._socketio = socketio
        self._service_log = service_log
        self._logger = logger or logging.getLogger(__name__)
        self._filter_matcher = DeviceFilterMatcher(db_conn_factory=db_conn_factory)
        self._ansible_runner = ansible_runner
        self._emit_host_service_event: Optional[Callable[[str, str, str, Any], bool]] = None
        self._online_lookup: Optional[Callable[[], List[str]]] = None
        self._credential_fetcher: Optional[Callable[[int], Optional[Dict[str, Any]]]] = None
        self._vpn_session_lookup: Optional[Callable[[], Dict[str, Dict[str, Any]]]] = None
        self._vpn_session_prepare: Optional[
            Callable[[Sequence[str], Optional[Sequence[int]]], Dict[str, Dict[str, Any]]]
        ] = None
        self._lock = threading.Lock()
        self._init_tables()
        self._recover_orphaned_runs_on_startup()

    # ------------------------------------------------------------------
    # Runtime configuration
    # ------------------------------------------------------------------
    def set_emit_host_service_event(self, fn: Optional[Callable[[str, str, str, Any], bool]]) -> None:
        self._emit_host_service_event = fn

    def set_online_lookup(self, fn: Optional[Callable[[], List[str]]]) -> None:
        self._online_lookup = fn

    def set_credential_fetcher(self, fn: Optional[Callable[[int], Optional[Dict[str, Any]]]]) -> None:
        self._credential_fetcher = fn

    def set_vpn_session_lookup(self, fn: Optional[Callable[[], Dict[str, Dict[str, Any]]]]) -> None:
        self._vpn_session_lookup = fn

    def set_vpn_session_prepare(
        self,
        fn: Optional[Callable[[Sequence[str], Optional[Sequence[int]]], Dict[str, Dict[str, Any]]]],
    ) -> None:
        self._vpn_session_prepare = fn

    # ------------------------------------------------------------------
    # Core persistence
    # ------------------------------------------------------------------
    def _conn(self) -> sqlite3.Connection:
        return self._db_conn_factory()

    def _log(self, message: str, *, level: str = "INFO", workflow_guid: str = "", run_id: Optional[int] = None) -> None:
        details = []
        if workflow_guid:
            details.append(f"workflow={workflow_guid}")
        if run_id is not None:
            details.append(f"run={run_id}")
        payload = message if not details else f"{message} | {' '.join(details)}"
        if callable(self._service_log):
            try:
                self._service_log("workflows", payload, scope=f"workflow-{workflow_guid}" if workflow_guid else None, level=level)
                return
            except Exception:
                self._logger.debug("Failed to write workflow service log", exc_info=True)
        numeric_level = getattr(logging, level.upper(), logging.INFO)
        self._logger.log(numeric_level, "%s", payload)

    def _init_tables(self) -> None:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS workflow_runs (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    workflow_guid TEXT NOT NULL,
                    workflow_name TEXT,
                    source_type TEXT NOT NULL,
                    source_metadata_json TEXT,
                    graph_snapshot_json TEXT NOT NULL,
                    status TEXT NOT NULL,
                    error TEXT,
                    skip_reason TEXT,
                    final_payload_json TEXT,
                    final_metadata_json TEXT,
                    parent_workflow_run_id INTEGER,
                    parent_node_id TEXT,
                    scheduled_job_id INTEGER,
                    scheduled_job_run_id INTEGER,
                    webhook_id INTEGER,
                    created_by TEXT,
                    created_at INTEGER NOT NULL,
                    started_ts INTEGER,
                    finished_ts INTEGER,
                    updated_at INTEGER NOT NULL
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_runs_guid ON workflow_runs(workflow_guid)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_runs_created ON workflow_runs(created_at)")
            try:
                cur.execute("PRAGMA table_info(workflow_runs)")
                columns = {row[1] for row in cur.fetchall()}
                if "parent_workflow_run_id" not in columns:
                    cur.execute("ALTER TABLE workflow_runs ADD COLUMN parent_workflow_run_id INTEGER")
                if "parent_node_id" not in columns:
                    cur.execute("ALTER TABLE workflow_runs ADD COLUMN parent_node_id TEXT")
                if "scheduled_job_id" not in columns:
                    cur.execute("ALTER TABLE workflow_runs ADD COLUMN scheduled_job_id INTEGER")
                if "scheduled_job_run_id" not in columns:
                    cur.execute("ALTER TABLE workflow_runs ADD COLUMN scheduled_job_run_id INTEGER")
                if "webhook_id" not in columns:
                    cur.execute("ALTER TABLE workflow_runs ADD COLUMN webhook_id INTEGER")
                if "created_by" not in columns:
                    cur.execute("ALTER TABLE workflow_runs ADD COLUMN created_by TEXT")
            except Exception:
                pass

            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS workflow_node_runs (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    workflow_run_id INTEGER NOT NULL,
                    node_id TEXT NOT NULL,
                    node_type TEXT,
                    node_label TEXT,
                    node_snapshot_json TEXT,
                    status TEXT NOT NULL,
                    skip_reason TEXT,
                    error TEXT,
                    timeout_seconds INTEGER,
                    input_envelope_json TEXT,
                    output_envelope_json TEXT,
                    ignored_inputs_json TEXT,
                    linked_child_summary_json TEXT,
                    created_at INTEGER NOT NULL,
                    started_ts INTEGER,
                    finished_ts INTEGER,
                    updated_at INTEGER NOT NULL,
                    FOREIGN KEY(workflow_run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_node_runs_run ON workflow_node_runs(workflow_run_id)")
            cur.execute(
                "CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_node_runs_identity ON workflow_node_runs(workflow_run_id, node_id)"
            )

            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS workflow_child_jobs (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    workflow_run_id INTEGER NOT NULL,
                    workflow_node_run_id INTEGER NOT NULL,
                    child_kind TEXT NOT NULL,
                    child_identifier TEXT,
                    activity_id INTEGER,
                    child_workflow_run_id INTEGER,
                    target_hostname TEXT,
                    component_guid TEXT,
                    component_name TEXT,
                    component_kind TEXT,
                    status TEXT,
                    stdout_summary TEXT,
                    stderr_summary TEXT,
                    payload_json TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL,
                    FOREIGN KEY(workflow_run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE,
                    FOREIGN KEY(workflow_node_run_id) REFERENCES workflow_node_runs(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_child_jobs_run ON workflow_child_jobs(workflow_run_id)")
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_child_jobs_node ON workflow_child_jobs(workflow_node_run_id)")

            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS workflow_webhooks (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    workflow_guid TEXT NOT NULL,
                    opaque_token TEXT NOT NULL UNIQUE,
                    created_at INTEGER NOT NULL,
                    creator_username TEXT,
                    creator_role TEXT,
                    last_used_at INTEGER
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_webhooks_guid ON workflow_webhooks(workflow_guid)")
            conn.commit()
        finally:
            conn.close()

    def _list_active_run_ids(self) -> List[int]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id
                  FROM workflow_runs
                 WHERE status IN (?, ?)
              ORDER BY COALESCE(started_ts, created_at, 0) ASC, id ASC
                """,
                (
                    WORKFLOW_STATUS_PENDING,
                    WORKFLOW_STATUS_RUNNING,
                ),
            )
            return [int(row[0]) for row in cur.fetchall() if row and row[0] is not None]
        finally:
            conn.close()

    def _timed_out_orphaned_nodes(
        self,
        node_runs: Sequence[Mapping[str, Any]],
        *,
        now: int,
    ) -> List[Dict[str, Any]]:
        timed_out: List[Dict[str, Any]] = []
        for node_run in node_runs:
            if _normalize_status(node_run.get("status")) != WORKFLOW_STATUS_RUNNING:
                continue
            timeout_seconds = max(0, _coerce_int(node_run.get("timeout_seconds"), default=0))
            started_ts = _coerce_optional_int(node_run.get("started_ts"))
            if timeout_seconds <= 0 or started_ts is None:
                continue
            if started_ts + timeout_seconds > int(now):
                continue
            timed_out.append(
                {
                    "node_id": str(node_run.get("node_id") or "").strip(),
                    "node_label": str(node_run.get("node_label") or node_run.get("node_id") or "Node").strip(),
                    "timeout_seconds": timeout_seconds,
                    "started_ts": started_ts,
                }
            )
        return timed_out

    def _determine_stuck_run_terminal_status(
        self,
        run: Mapping[str, Any],
        node_runs: Sequence[Mapping[str, Any]],
        *,
        now: int,
    ) -> str:
        del run
        if self._timed_out_orphaned_nodes(node_runs, now=now):
            return WORKFLOW_STATUS_TIMED_OUT
        return WORKFLOW_STATUS_FAILED

    def _stuck_run_error_text(
        self,
        *,
        current_status: str,
        resolved_status: str,
        actor: str,
        recovery_reason: str,
        timed_out_nodes: Sequence[Mapping[str, Any]],
    ) -> str:
        previous_status = _normalize_status(current_status)
        if recovery_reason == "runtime_startup":
            actor_label = "Workflow runtime startup"
        else:
            actor_label = str(actor or "").strip() or "Administrator"

        if resolved_status == WORKFLOW_STATUS_TIMED_OUT:
            if timed_out_nodes:
                labels = ", ".join(str(item.get("node_label") or item.get("node_id") or "Node") for item in timed_out_nodes)
                return (
                    f"{actor_label} recovered an orphaned workflow run that was still {previous_status}. "
                    f"Borealis marked it Timed Out because node timeout(s) had already elapsed: {labels}."
                )
            return (
                f"{actor_label} recovered an orphaned workflow run that was still {previous_status} "
                "and marked it Timed Out."
            )
        return (
            f"{actor_label} recovered an orphaned workflow run that was still {previous_status} "
            "and marked it Failed."
        )

    def _resolve_stuck_run_locked(
        self,
        run_id: int,
        *,
        requested_status: Optional[str],
        actor: str,
        recovery_reason: str,
    ) -> Dict[str, Any]:
        run = self.get_run(run_id)
        if not run:
            raise LookupError("workflow run not found")

        current_status = _normalize_status(run.get("status"))
        if current_status in WORKFLOW_TERMINAL_STATUSES:
            return {
                "resolved": False,
                "reason": "run_not_active",
                "status": current_status,
                "run": run,
            }

        node_runs = list(run.get("node_runs") or [])
        now = _now_ts()
        timed_out_nodes = self._timed_out_orphaned_nodes(node_runs, now=now)
        resolved_status = _normalize_status(
            requested_status or self._determine_stuck_run_terminal_status(run, node_runs, now=now)
        )
        if resolved_status not in {WORKFLOW_STATUS_FAILED, WORKFLOW_STATUS_TIMED_OUT}:
            resolved_status = WORKFLOW_STATUS_FAILED

        error_text = self._stuck_run_error_text(
            current_status=current_status,
            resolved_status=resolved_status,
            actor=actor,
            recovery_reason=recovery_reason,
            timed_out_nodes=timed_out_nodes,
        )
        started_ts = _coerce_optional_int(run.get("started_ts")) or _coerce_optional_int(run.get("created_at")) or now

        recovery_metadata = {
            "recovered": True,
            "recovery_reason": recovery_reason,
            "recovery_actor": str(actor or "").strip(),
            "previous_status": current_status,
            "resolved_status": resolved_status,
            "recovered_at": now,
            "timed_out_nodes": list(timed_out_nodes),
        }

        node_statuses: Dict[str, str] = {}
        recovered_node_count = 0
        recovered_child_count = 0

        conn = self._conn()
        try:
            cur = conn.cursor()
            for node_run in node_runs:
                node_run_id = _coerce_optional_int(node_run.get("id"))
                if node_run_id is None:
                    continue
                node_id = str(node_run.get("node_id") or "").strip()
                previous_node_status = _normalize_status(node_run.get("status"))
                if previous_node_status in WORKFLOW_TERMINAL_STATUSES:
                    node_statuses[node_id] = previous_node_status
                    continue

                if previous_node_status == WORKFLOW_STATUS_RUNNING:
                    node_status = resolved_status
                    skip_reason = ""
                    node_error = error_text
                else:
                    node_status = WORKFLOW_STATUS_SKIPPED
                    skip_reason = "workflow_run_recovered"
                    node_error = ""

                node_statuses[node_id] = node_status
                output = self._build_output_envelope(
                    status=node_status,
                    data=None,
                    metadata={
                        "reason": "workflow_run_recovered",
                        "recovery_reason": recovery_reason,
                        "recovery_actor": str(actor or "").strip(),
                        "previous_status": previous_node_status,
                    },
                    artifacts={},
                )
                cur.execute(
                    """
                    UPDATE workflow_node_runs
                       SET status=?,
                           skip_reason=?,
                           error=?,
                           output_envelope_json=?,
                           finished_ts=?,
                           updated_at=?
                     WHERE id=?
                    """,
                    (
                        node_status,
                        skip_reason,
                        node_error,
                        _json_dumps(dict(output)),
                        now,
                        now,
                        int(node_run_id),
                    ),
                )
                recovered_node_count += 1

            cur.execute(
                """
                SELECT id, status, stderr_summary
                  FROM workflow_child_jobs
                 WHERE workflow_run_id=?
                """,
                (int(run_id),),
            )
            for child_row in cur.fetchall():
                child_job_id = _coerce_optional_int(child_row[0] if child_row else None)
                if child_job_id is None:
                    continue
                child_status = _normalize_status(child_row[1] if len(child_row) > 1 else "")
                if child_status in WORKFLOW_TERMINAL_STATUSES:
                    continue
                stderr_summary = str((child_row[2] if len(child_row) > 2 else "") or "").strip() or error_text
                cur.execute(
                    """
                    UPDATE workflow_child_jobs
                       SET status=?,
                           stderr_summary=?,
                           updated_at=?
                     WHERE id=?
                    """,
                    (
                        resolved_status,
                        _truncate_text(stderr_summary),
                        now,
                        int(child_job_id),
                    ),
                )
                recovered_child_count += 1

            recovery_metadata["recovered_node_count"] = recovered_node_count
            recovery_metadata["recovered_child_count"] = recovered_child_count
            recovery_metadata["node_statuses"] = dict(node_statuses)
            final_payload = self._build_output_envelope(
                status=resolved_status,
                data={
                    "recovery": dict(recovery_metadata),
                    "node_statuses": dict(node_statuses),
                },
                metadata=dict(recovery_metadata),
                artifacts={},
            )
            final_metadata = dict(recovery_metadata)
            cur.execute(
                """
                UPDATE workflow_runs
                   SET status=?,
                       error=?,
                       skip_reason=?,
                       final_payload_json=?,
                       final_metadata_json=?,
                       started_ts=COALESCE(started_ts, ?),
                       finished_ts=?,
                       updated_at=?
                 WHERE id=?
                """,
                (
                    resolved_status,
                    error_text,
                    "",
                    _json_dumps(dict(final_payload)),
                    _json_dumps(dict(final_metadata)),
                    int(started_ts),
                    now,
                    now,
                    int(run_id),
                ),
            )
            conn.commit()
        finally:
            conn.close()

        self._mirror_scheduled_job_run(
            workflow_run_id=run_id,
            source_metadata=run.get("source_metadata") if isinstance(run.get("source_metadata"), Mapping) else {},
            status=resolved_status,
            error=error_text,
        )
        self._log(
            f"resolved orphaned workflow run reason={recovery_reason} status={resolved_status} actor={actor or 'system'}",
            level="WARNING",
            workflow_guid=str(run.get("workflow_guid") or ""),
            run_id=run_id,
        )
        return {
            "resolved": True,
            "reason": recovery_reason,
            "status": resolved_status,
            "run": self.get_run(run_id) or {"id": int(run_id), "status": resolved_status},
        }

    def _recover_orphaned_runs_on_startup(self) -> None:
        active_run_ids = self._list_active_run_ids()
        if not active_run_ids:
            return
        recovered_count = 0
        with self._lock:
            for run_id in active_run_ids:
                try:
                    result = self._resolve_stuck_run_locked(
                        int(run_id),
                        requested_status=None,
                        actor="Workflow Runtime Startup",
                        recovery_reason="runtime_startup",
                    )
                except Exception:
                    self._logger.exception("Failed to recover orphaned workflow run id=%s", run_id)
                    continue
                if result.get("resolved"):
                    recovered_count += 1
        if recovered_count:
            self._log(
                f"workflow runtime startup recovered {recovered_count} orphaned workflow run(s)",
                level="WARNING",
            )

    # ------------------------------------------------------------------
    # Public query surface
    # ------------------------------------------------------------------
    def resolve_stuck_run(
        self,
        run_id: int,
        *,
        status: Optional[str] = None,
        actor: str = "",
        recovery_reason: str = "manual_admin_resolve",
    ) -> Dict[str, Any]:
        requested_status: Optional[str] = None
        raw_status = str(status or "").strip()
        if raw_status and raw_status.lower() != "auto":
            requested_status = _normalize_status(raw_status)
            if requested_status not in {WORKFLOW_STATUS_FAILED, WORKFLOW_STATUS_TIMED_OUT}:
                raise ValueError("status must be Failed, Timed Out, or auto.")
        with self._lock:
            return self._resolve_stuck_run_locked(
                int(run_id),
                requested_status=requested_status,
                actor=actor,
                recovery_reason=recovery_reason,
            )

    def list_runs(self, workflow_guid: str, *, limit: int = 100) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    workflow_guid,
                    workflow_name,
                    source_type,
                    source_metadata_json,
                    status,
                    error,
                    skip_reason,
                    final_payload_json,
                    final_metadata_json,
                    parent_workflow_run_id,
                    parent_node_id,
                    scheduled_job_id,
                    scheduled_job_run_id,
                    webhook_id,
                    created_by,
                    created_at,
                    started_ts,
                    finished_ts,
                    updated_at
                  FROM workflow_runs
                 WHERE LOWER(workflow_guid)=LOWER(?)
              ORDER BY COALESCE(started_ts, created_at, 0) DESC, id DESC
                 LIMIT ?
                """,
                (str(workflow_guid or "").strip(), max(1, int(limit))),
            )
            return [self._row_to_workflow_run(row) for row in cur.fetchall()]
        finally:
            conn.close()

    def get_run(self, run_id: int) -> Optional[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    workflow_guid,
                    workflow_name,
                    source_type,
                    source_metadata_json,
                    status,
                    error,
                    skip_reason,
                    final_payload_json,
                    final_metadata_json,
                    parent_workflow_run_id,
                    parent_node_id,
                    scheduled_job_id,
                    scheduled_job_run_id,
                    webhook_id,
                    created_by,
                    created_at,
                    started_ts,
                    finished_ts,
                    updated_at,
                    graph_snapshot_json
                  FROM workflow_runs
                 WHERE id=?
                """,
                (int(run_id),),
            )
            row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            return None
        payload = self._row_to_workflow_run(row[:20], graph_snapshot_json=row[20])
        payload["node_runs"] = self._list_node_runs_for_run(int(run_id))
        return payload

    def get_node_run(self, run_id: int, node_id: str) -> Optional[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    workflow_run_id,
                    node_id,
                    node_type,
                    node_label,
                    node_snapshot_json,
                    status,
                    skip_reason,
                    error,
                    timeout_seconds,
                    input_envelope_json,
                    output_envelope_json,
                    ignored_inputs_json,
                    linked_child_summary_json,
                    created_at,
                    started_ts,
                    finished_ts,
                    updated_at
                  FROM workflow_node_runs
                 WHERE workflow_run_id=? AND node_id=?
                """,
                (int(run_id), str(node_id or "")),
            )
            row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            return None
        payload = self._row_to_node_run(row)
        payload["child_jobs"] = self._list_child_jobs_for_node_run(int(payload["id"]))
        return payload

    def list_webhooks(self, workflow_guid: str) -> List[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
                  FROM workflow_webhooks
                 WHERE LOWER(workflow_guid)=LOWER(?)
              ORDER BY created_at DESC, id DESC
                """,
                (str(workflow_guid or "").strip(),),
            )
            return [self._row_to_webhook(row) for row in cur.fetchall()]
        finally:
            conn.close()

    def create_webhook(self, workflow_guid: str, *, creator: Optional[Mapping[str, Any]] = None) -> Dict[str, Any]:
        created_at = _now_ts()
        token = secrets.token_urlsafe(36)
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO workflow_webhooks(
                    workflow_guid,
                    opaque_token,
                    created_at,
                    creator_username,
                    creator_role,
                    last_used_at
                ) VALUES (?,?,?,?,?,?)
                """,
                (
                    str(workflow_guid or "").strip(),
                    token,
                    created_at,
                    str((creator or {}).get("username") or "").strip(),
                    str((creator or {}).get("role") or "").strip(),
                    None,
                ),
            )
            webhook_id = int(cur.lastrowid or 0)
            conn.commit()
            cur.execute(
                """
                SELECT id, workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
                  FROM workflow_webhooks
                 WHERE id=?
                """,
                (webhook_id,),
            )
            row = cur.fetchone()
            return self._row_to_webhook(row) if row else {}
        finally:
            conn.close()

    def delete_webhook(self, workflow_guid: str, webhook_id: int) -> bool:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "DELETE FROM workflow_webhooks WHERE id=? AND LOWER(workflow_guid)=LOWER(?)",
                (int(webhook_id), str(workflow_guid or "").strip()),
            )
            deleted = cur.rowcount > 0
            conn.commit()
            return deleted
        finally:
            conn.close()

    def find_webhook_by_token(self, opaque_token: str) -> Optional[Dict[str, Any]]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
                  FROM workflow_webhooks
                 WHERE opaque_token=?
                """,
                (str(opaque_token or "").strip(),),
            )
            row = cur.fetchone()
            if not row:
                return None
            return self._row_to_webhook(row)
        finally:
            conn.close()

    # ------------------------------------------------------------------
    # Run lifecycle
    # ------------------------------------------------------------------
    def load_workflow_snapshot(self, workflow_guid: str) -> Tuple[Dict[str, Any], Dict[str, Any]]:
        workflow_export = self._assembly_runtime.export_assembly(str(workflow_guid or "").strip())
        workflow_payload = _decode_base64_json(workflow_export.get("workflow"))
        if workflow_payload is None:
            raise ValueError("Selected workflow assembly does not contain a valid workflow document.")
        return dict(workflow_export), dict(workflow_payload)

    def validate_saved_workflow(
        self,
        workflow_guid: str,
        *,
        source_type: str,
        source_metadata: Optional[Mapping[str, Any]] = None,
    ) -> List[str]:
        workflow_guid = str(workflow_guid or "").strip()
        if not workflow_guid:
            return ["workflow_guid is required"]
        try:
            _workflow_export, workflow_payload = self.load_workflow_snapshot(workflow_guid)
        except Exception as exc:
            return [str(exc)]
        return self.validate_workflow_document(
            workflow_guid=workflow_guid,
            workflow_payload=workflow_payload,
            source_type=source_type,
            source_metadata=source_metadata,
        )

    def start_run(
        self,
        *,
        workflow_guid: str,
        source_type: str,
        source_metadata: Optional[Mapping[str, Any]] = None,
        created_by: str = "",
        execute_async: bool = True,
    ) -> Dict[str, Any]:
        workflow_guid = str(workflow_guid or "").strip()
        source_type = str(source_type or "").strip().lower() or "manual"
        if not workflow_guid:
            raise ValueError("workflow_guid is required")
        if source_type not in TRIGGER_NODE_TYPE_BY_SOURCE:
            raise ValueError(f"Unsupported workflow trigger source '{source_type}'.")

        workflow_export, workflow_payload = self.load_workflow_snapshot(workflow_guid)

        validation = self.validate_workflow_document(
            workflow_guid=workflow_guid,
            workflow_payload=workflow_payload,
            source_type=source_type,
            source_metadata=source_metadata,
        )
        if validation:
            raise ValueError("; ".join(validation))

        created_at = _now_ts()
        source_metadata_json = dict(source_metadata or {})
        with self._lock:
            active_run = self._active_run_for_workflow(workflow_guid, source_metadata=source_metadata_json)
            if active_run is not None:
                skipped_id = self._create_run_row(
                    workflow_guid=workflow_guid,
                    workflow_name=str(workflow_export.get("name") or workflow_payload.get("tab_name") or "Workflow"),
                    source_type=source_type,
                    source_metadata=source_metadata_json,
                    graph_snapshot=workflow_payload,
                    status=WORKFLOW_STATUS_SKIPPED,
                    skip_reason="workflow_already_running",
                    created_by=created_by,
                    created_at=created_at,
                )
                skipped_run = self.get_run(skipped_id) or {"id": skipped_id, "status": WORKFLOW_STATUS_SKIPPED}
                self._mirror_scheduled_job_run(
                    workflow_run_id=skipped_id,
                    source_metadata=source_metadata_json,
                    status=WORKFLOW_STATUS_SKIPPED,
                    error="A workflow run is already active for this saved workflow.",
                )
                self._log(
                    "workflow trigger skipped because a run is already active",
                    level="WARNING",
                    workflow_guid=workflow_guid,
                    run_id=skipped_id,
                )
                return {"started": False, "run": skipped_run}

            run_id = self._create_run_row(
                workflow_guid=workflow_guid,
                workflow_name=str(workflow_export.get("name") or workflow_payload.get("tab_name") or "Workflow"),
                source_type=source_type,
                source_metadata=source_metadata_json,
                graph_snapshot=workflow_payload,
                status=WORKFLOW_STATUS_PENDING,
                created_by=created_by,
                created_at=created_at,
            )

        run_snapshot = self.get_run(run_id) or {"id": run_id, "status": WORKFLOW_STATUS_PENDING}
        if execute_async:
            self._spawn_background_task(self._execute_run, run_id)
            return {"started": True, "run": run_snapshot}
        self._execute_run(run_id)
        return {"started": True, "run": self.get_run(run_id) or run_snapshot}

    def validate_workflow_document(
        self,
        *,
        workflow_guid: str,
        workflow_payload: Mapping[str, Any],
        source_type: str,
        source_metadata: Optional[Mapping[str, Any]] = None,
    ) -> List[str]:
        errors: List[str] = []
        nodes = list(workflow_payload.get("nodes") or [])
        edges = list(workflow_payload.get("edges") or [])
        node_ids = set()
        node_map: Dict[str, Mapping[str, Any]] = {}
        trigger_counts: Dict[str, int] = {}
        incoming_port_counts: Dict[str, Dict[str, int]] = {}
        outgoing_port_counts: Dict[str, Dict[str, int]] = {}
        for node in nodes:
            if not isinstance(node, Mapping):
                errors.append("Workflow nodes must be objects.")
                continue
            node_id = str(node.get("id") or "").strip()
            if not node_id:
                errors.append("Workflow contains a node with no id.")
                continue
            if node_id in node_ids:
                errors.append(f"Duplicate node id '{node_id}'.")
            node_ids.add(node_id)
            node_map[node_id] = node
            node_type = str(node.get("type") or "").strip()
            if node_type not in SUPPORTED_NODE_TYPES:
                errors.append(f"Unsupported executable node '{_node_label(node)}' ({node_type}).")
            if node_type in TRIGGER_NODE_TYPE_BY_SOURCE.values():
                trigger_counts[node_type] = trigger_counts.get(node_type, 0) + 1
            data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
            if node_type == NODE_TYPE_EXECUTE_ASSEMBLY and not str(
                data.get("assembly_guid") or data.get("assemblyGuid") or ""
            ).strip():
                errors.append(f"Execute Assembly node '{_node_label(node)}' is missing an assembly selection.")
            if node_type == NODE_TYPE_AGENT_FILTER:
                if _coerce_optional_int(
                    data.get("filter_id") or data.get("agent_filter_id") or data.get("selected_filter_id")
                ) is None:
                    errors.append(f"Device Filter node '{_node_label(node)}' is missing a device filter selection.")
            if node_type == NODE_TYPE_AGENT_ARRAY and not self._node_agent_array_entries(node):
                errors.append(f"List of Devices node '{_node_label(node)}' does not contain any selected devices.")
            if node_type == NODE_TYPE_EXECUTE_SUBWORKFLOW:
                child_guid = str(data.get("workflow_guid") or data.get("workflowGuid") or "").strip()
                if not child_guid:
                    errors.append(f"Execute Subworkflow node '{_node_label(node)}' is missing a workflow selection.")
                ancestry = [str(value).strip().lower() for value in (source_metadata or {}).get("workflow_ancestry") or []]
                if child_guid.lower() in ancestry or child_guid.lower() == workflow_guid.lower():
                    errors.append(f"Subworkflow node '{_node_label(node)}' would recurse into an ancestor workflow.")
        for trigger_type, count in trigger_counts.items():
            if count > 1:
                errors.append(f"Workflow may contain at most one '{trigger_type}' trigger node.")

        active_trigger_type = TRIGGER_NODE_TYPE_BY_SOURCE.get(source_type)
        if active_trigger_type and trigger_counts.get(active_trigger_type, 0) != 1:
            source_label = source_type.replace("_", " ").title()
            errors.append(f"Workflow requires exactly one '{source_label}' trigger node for this launch source.")

        indegree = {node_id: 0 for node_id in node_ids}
        adjacency: Dict[str, List[str]] = {node_id: [] for node_id in node_ids}
        for edge in edges:
            if not isinstance(edge, Mapping):
                errors.append("Workflow edges must be objects.")
                continue
            source_id = str(edge.get("source") or "").strip()
            target_id = str(edge.get("target") or "").strip()
            if source_id not in node_ids or target_id not in node_ids:
                errors.append("Workflow contains an edge that references a missing node.")
                continue
            edge_label = str(edge.get("id") or f"{source_id}-{target_id}").strip()
            source_node = node_map.get(source_id) or {}
            target_node = node_map.get(target_id) or {}
            source_type_value = str(source_node.get("type") or "").strip()
            target_type_value = str(target_node.get("type") or "").strip()
            source_port = _edge_source_port(edge, node_map)
            target_port = _edge_target_port(edge, node_map)

            if source_type_value in SUPPORTED_NODE_TYPES and source_port is None:
                errors.append(
                    f"Workflow edge '{edge_label}' uses legacy output wiring on '{_node_label(source_node)}'. Reconnect it to a named output port."
                )
            if target_type_value in SUPPORTED_NODE_TYPES and target_port is None:
                errors.append(
                    f"Workflow edge '{edge_label}' uses legacy input wiring on '{_node_label(target_node)}'. Reconnect it to a named input port."
                )

            route = _edge_route(edge)
            if source_port and target_port:
                if str(source_port.get("kind")) != str(target_port.get("kind")):
                    errors.append(
                        f"Workflow edge '{edge_label}' connects incompatible ports ('{source_port.get('label')}' to '{target_port.get('label')}')."
                    )
                if (
                    str(source_port.get("kind")) == PORT_KIND_ACTION and route not in VALID_EDGE_ROUTES
                ):
                    errors.append(f"Workflow edge '{edge_label}' has an invalid route.")

                target_port_id = str(target_port.get("id") or "")
                source_port_id = str(source_port.get("id") or "")
                incoming_port_counts.setdefault(target_id, {})
                outgoing_port_counts.setdefault(source_id, {})
                incoming_port_counts[target_id][target_port_id] = (
                    incoming_port_counts[target_id].get(target_port_id, 0) + 1
                )
                outgoing_port_counts[source_id][source_port_id] = (
                    outgoing_port_counts[source_id].get(source_port_id, 0) + 1
                )
            indegree[target_id] = indegree.get(target_id, 0) + 1
            adjacency.setdefault(source_id, []).append(target_id)

        for node_id, node in node_map.items():
            node_type = str(node.get("type") or "").strip()
            if node_type not in SUPPORTED_NODE_TYPES:
                continue
            for port in _node_ports(node_type, direction=PORT_DIRECTION_INPUT):
                port_id = str(port.get("id") or "")
                count = incoming_port_counts.get(node_id, {}).get(port_id, 0)
                if bool(port.get("required")) and count <= 0:
                    errors.append(
                        f"Node '{_node_label(node)}' requires a '{port.get('label')}' input connection."
                    )
                if str(port.get("cardinality") or PORT_CARDINALITY_MULTI) == PORT_CARDINALITY_SINGLE and count > 1:
                    errors.append(
                        f"Node '{_node_label(node)}' allows only one '{port.get('label')}' input connection."
                    )
            for port in _node_ports(node_type, direction=PORT_DIRECTION_OUTPUT):
                port_id = str(port.get("id") or "")
                count = outgoing_port_counts.get(node_id, {}).get(port_id, 0)
                if str(port.get("cardinality") or PORT_CARDINALITY_MULTI) == PORT_CARDINALITY_SINGLE and count > 1:
                    errors.append(
                        f"Node '{_node_label(node)}' allows only one '{port.get('label')}' output connection."
                    )

        queue = [node_id for node_id, degree in indegree.items() if degree == 0]
        visited = 0
        while queue:
            current = queue.pop(0)
            visited += 1
            for child in adjacency.get(current, []):
                indegree[child] -= 1
                if indegree[child] == 0:
                    queue.append(child)
        if node_ids and visited != len(node_ids):
            errors.append("Workflow graph contains a cycle. Workflow Runtime v1 only supports acyclic graphs.")

        return errors

    # ------------------------------------------------------------------
    # Execution engine
    # ------------------------------------------------------------------
    def _spawn_background_task(self, fn: Callable[..., Any], *args: Any) -> None:
        if self._socketio is not None:
            try:
                self._socketio.start_background_task(fn, *args)
                return
            except Exception:
                self._logger.debug("Workflow background task fallback to thread", exc_info=True)
        thread = threading.Thread(target=fn, args=args, daemon=True)
        thread.start()

    def _active_run_for_workflow(
        self,
        workflow_guid: str,
        *,
        source_metadata: Optional[Mapping[str, Any]] = None,
    ) -> Optional[int]:
        scope = source_metadata.get("workflow_site_scope") if isinstance(source_metadata, Mapping) else None
        try:
            scoped_site_id = int(scope.get("site_id")) if isinstance(scope, Mapping) and scope.get("site_id") not in (None, "", "null") else 0
        except Exception:
            scoped_site_id = 0
        if scoped_site_id > 0 and _coerce_optional_int((source_metadata or {}).get("scheduled_job_run_id")) is not None:
            return None
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id
                  FROM workflow_runs
                 WHERE LOWER(workflow_guid)=LOWER(?)
                   AND status IN (?, ?)
              ORDER BY id DESC
                 LIMIT 1
                """,
                (
                    str(workflow_guid or "").strip(),
                    WORKFLOW_STATUS_PENDING,
                    WORKFLOW_STATUS_RUNNING,
                ),
            )
            row = cur.fetchone()
            return int(row[0]) if row else None
        finally:
            conn.close()

    def _create_run_row(
        self,
        *,
        workflow_guid: str,
        workflow_name: str,
        source_type: str,
        source_metadata: Mapping[str, Any],
        graph_snapshot: Mapping[str, Any],
        status: str,
        created_by: str = "",
        created_at: Optional[int] = None,
        skip_reason: str = "",
    ) -> int:
        ts = int(created_at or _now_ts())
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO workflow_runs(
                    workflow_guid,
                    workflow_name,
                    source_type,
                    source_metadata_json,
                    graph_snapshot_json,
                    status,
                    error,
                    skip_reason,
                    final_payload_json,
                    final_metadata_json,
                    parent_workflow_run_id,
                    parent_node_id,
                    scheduled_job_id,
                    scheduled_job_run_id,
                    webhook_id,
                    created_by,
                    created_at,
                    started_ts,
                    finished_ts,
                    updated_at
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    workflow_guid,
                    workflow_name,
                    source_type,
                    _json_dumps(dict(source_metadata or {})),
                    _json_dumps(dict(graph_snapshot or {})),
                    status,
                    "",
                    skip_reason,
                    "",
                    "",
                    _coerce_optional_int(source_metadata.get("parent_workflow_run_id")),
                    str(source_metadata.get("parent_node_id") or "").strip() or None,
                    _coerce_optional_int(source_metadata.get("scheduled_job_id")),
                    _coerce_optional_int(source_metadata.get("scheduled_job_run_id")),
                    _coerce_optional_int(source_metadata.get("webhook_id")),
                    created_by or None,
                    ts,
                    None,
                    ts if status in WORKFLOW_TERMINAL_STATUSES else None,
                    ts,
                ),
            )
            run_id = int(cur.lastrowid or 0)
            conn.commit()
            return run_id
        finally:
            conn.close()

    def _execute_run(self, run_id: int) -> None:
        run = self.get_run(run_id)
        if not run:
            return
        workflow_guid = str(run.get("workflow_guid") or "")
        graph_snapshot = run.get("graph_snapshot") if isinstance(run.get("graph_snapshot"), Mapping) else {}
        nodes = list(graph_snapshot.get("nodes") or [])
        edges = list(graph_snapshot.get("edges") or [])
        source_type = str(run.get("source_type") or "manual").strip().lower()
        source_metadata = run.get("source_metadata") if isinstance(run.get("source_metadata"), Mapping) else {}

        started_ts = _now_ts()
        self._update_run_row(
            run_id,
            status=WORKFLOW_STATUS_RUNNING,
            started_ts=started_ts,
            updated_at=started_ts,
        )
        self._mirror_scheduled_job_run(
            workflow_run_id=run_id,
            source_metadata=source_metadata,
            status=WORKFLOW_STATUS_RUNNING,
        )
        self._log("workflow run starting", workflow_guid=workflow_guid, run_id=run_id)

        node_map = {str(node.get("id") or "").strip(): dict(node) for node in nodes if isinstance(node, Mapping)}
        edge_map_by_target: Dict[str, List[Dict[str, Any]]] = {}
        for edge in edges:
            if not isinstance(edge, Mapping):
                continue
            edge_map_by_target.setdefault(str(edge.get("target") or "").strip(), []).append(dict(edge))

        device_snapshot = self._filter_matcher.fetch_devices()
        online_lookup = list(self._online_hostnames_snapshot())
        frozen_targets = self._freeze_assembly_target_snapshots(
            nodes,
            devices=device_snapshot,
            online_lookup=online_lookup,
        )
        node_run_ids = self._create_initial_node_runs(run_id, nodes)
        outputs: Dict[str, Dict[str, Any]] = {}
        statuses: Dict[str, str] = {}
        processed: set[str] = set()
        active_trigger_type = TRIGGER_NODE_TYPE_BY_SOURCE.get(source_type)

        try:
            while len(processed) < len(node_map):
                progressed = False
                for node_id, node in node_map.items():
                    if node_id in processed:
                        continue
                    node_type = str(node.get("type") or "").strip()
                    incoming = edge_map_by_target.get(node_id, [])
                    if node_type in TRIGGER_NODE_TYPE_BY_SOURCE.values():
                        if node_type != active_trigger_type:
                            output = self._build_output_envelope(
                                status=WORKFLOW_STATUS_SKIPPED,
                                data=None,
                                metadata={"reason": "inactive_trigger", "source_type": source_type},
                                artifacts={},
                            )
                            self._finalize_node_run(
                                node_run_ids[node_id],
                                status=WORKFLOW_STATUS_SKIPPED,
                                output_envelope=output,
                                skip_reason="inactive_trigger",
                            )
                            outputs[node_id] = output
                            statuses[node_id] = WORKFLOW_STATUS_SKIPPED
                            processed.add(node_id)
                            progressed = True
                            continue
                        output = self._build_output_envelope(
                            status=WORKFLOW_STATUS_SUCCESS,
                            data={"trigger_source": source_type},
                            metadata={"source_type": source_type, "source_metadata": source_metadata},
                            artifacts={},
                        )
                        self._finalize_node_run(
                            node_run_ids[node_id],
                            status=WORKFLOW_STATUS_SUCCESS,
                            output_envelope=output,
                        )
                        outputs[node_id] = output
                        statuses[node_id] = WORKFLOW_STATUS_SUCCESS
                        processed.add(node_id)
                        progressed = True
                        continue

                    predecessor_ids = [str(edge.get("source") or "").strip() for edge in incoming]
                    if any(predecessor_id not in processed for predecessor_id in predecessor_ids):
                        continue

                    input_envelope, ignored_inputs, matched_count = self._build_input_envelope(
                        node_id=node_id,
                        incoming_edges=incoming,
                        outputs=outputs,
                        node_map=node_map,
                    )

                    self._mark_node_running(
                        node_run_ids[node_id],
                        input_envelope=input_envelope,
                        ignored_inputs=ignored_inputs,
                    )

                    missing_required_ports = self._missing_required_input_ports(node=node, input_envelope=input_envelope)
                    if predecessor_ids and (matched_count == 0 or missing_required_ports):
                        skip_reason = (
                            "no_routed_inputs"
                            if any(str(item.get("kind") or "") == PORT_KIND_ACTION for item in missing_required_ports)
                            or matched_count == 0
                            else "missing_required_inputs"
                        )
                        output = self._build_output_envelope(
                            status=WORKFLOW_STATUS_SKIPPED,
                            data=None,
                            metadata={
                                "reason": skip_reason,
                                "ignored_inputs": ignored_inputs,
                                "missing_required_ports": missing_required_ports,
                            },
                            artifacts={},
                        )
                        self._finalize_node_run(
                            node_run_ids[node_id],
                            status=WORKFLOW_STATUS_SKIPPED,
                            output_envelope=output,
                            input_envelope=input_envelope,
                            ignored_inputs=ignored_inputs,
                            skip_reason=skip_reason,
                        )
                        outputs[node_id] = output
                        statuses[node_id] = WORKFLOW_STATUS_SKIPPED
                        processed.add(node_id)
                        progressed = True
                        continue

                    result = self._execute_node(
                        run_id=run_id,
                        node_run_id=node_run_ids[node_id],
                        node=node,
                        input_envelope=input_envelope,
                        source_metadata=source_metadata,
                        frozen_targets=frozen_targets.get(node_id) or {},
                        device_snapshot=device_snapshot,
                        online_lookup=online_lookup,
                    )
                    outputs[node_id] = result["output_envelope"]
                    statuses[node_id] = result["status"]
                    processed.add(node_id)
                    progressed = True

                if not progressed:
                    for node_id, node in node_map.items():
                        if node_id in processed:
                            continue
                        output = self._build_output_envelope(
                            status=WORKFLOW_STATUS_FAILED,
                            data=None,
                            metadata={"reason": "workflow_deadlock"},
                            artifacts={},
                        )
                        self._finalize_node_run(
                            node_run_ids[node_id],
                            status=WORKFLOW_STATUS_FAILED,
                            output_envelope=output,
                            error="Workflow deadlock detected while waiting for executable nodes.",
                        )
                        outputs[node_id] = output
                        statuses[node_id] = WORKFLOW_STATUS_FAILED
                        processed.add(node_id)
                    break

            exports = self._collect_exports(node_map=node_map, outputs=outputs)
            sink_nodes = self._sink_node_outputs(node_map=node_map, edges=edges, outputs=outputs)
            terminal_job_output = self._collect_terminal_job_output(sink_nodes)
            final_status = _rollup_status(statuses.values())
            final_payload = self._build_output_envelope(
                status=final_status,
                data={"exports": exports, "terminal_nodes": sink_nodes, "job_output": terminal_job_output},
                metadata={"exports": exports, "node_statuses": statuses, "job_output_count": len(terminal_job_output)},
                artifacts={"frozen_targets": frozen_targets},
            )
            finished_ts = _now_ts()
            self._update_run_row(
                run_id,
                status=final_status,
                final_payload=final_payload,
                final_metadata={"exports": exports, "node_statuses": statuses},
                finished_ts=finished_ts,
                updated_at=finished_ts,
            )
            self._mirror_scheduled_job_run(
                workflow_run_id=run_id,
                source_metadata=source_metadata,
                status=final_status,
                error="" if final_status in {WORKFLOW_STATUS_SUCCESS, WORKFLOW_STATUS_WARNING, WORKFLOW_STATUS_SKIPPED} else "Workflow execution failed.",
            )
            self._log("workflow run finished", workflow_guid=workflow_guid, run_id=run_id)
        except Exception as exc:
            finished_ts = _now_ts()
            self._update_run_row(
                run_id,
                status=WORKFLOW_STATUS_FAILED,
                error=str(exc),
                finished_ts=finished_ts,
                updated_at=finished_ts,
            )
            self._mirror_scheduled_job_run(
                workflow_run_id=run_id,
                source_metadata=source_metadata,
                status=WORKFLOW_STATUS_FAILED,
                error=str(exc),
            )
            self._log(
                f"workflow run failed err={exc}",
                level="ERROR",
                workflow_guid=workflow_guid,
                run_id=run_id,
            )

    def _create_initial_node_runs(self, run_id: int, nodes: Sequence[Mapping[str, Any]]) -> Dict[str, int]:
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            result: Dict[str, int] = {}
            for node in nodes:
                node_id = str(node.get("id") or "").strip()
                if not node_id:
                    continue
                timeout_seconds = self._node_timeout_seconds(node)
                cur.execute(
                    """
                    INSERT INTO workflow_node_runs(
                        workflow_run_id,
                        node_id,
                        node_type,
                        node_label,
                        node_snapshot_json,
                        status,
                        skip_reason,
                        error,
                        timeout_seconds,
                        input_envelope_json,
                        output_envelope_json,
                        ignored_inputs_json,
                        linked_child_summary_json,
                        created_at,
                        started_ts,
                        finished_ts,
                        updated_at
                    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                    """,
                    (
                        int(run_id),
                        node_id,
                        str(node.get("type") or "").strip(),
                        _node_label(node),
                        _json_dumps(dict(node)),
                        WORKFLOW_STATUS_PENDING,
                        "",
                        "",
                        timeout_seconds,
                        "",
                        "",
                        "",
                        "",
                        now,
                        None,
                        None,
                        now,
                    ),
                )
                result[node_id] = int(cur.lastrowid or 0)
            conn.commit()
            return result
        finally:
            conn.close()

    def _mark_node_running(
        self,
        node_run_id: int,
        *,
        input_envelope: Mapping[str, Any],
        ignored_inputs: Sequence[Any],
    ) -> None:
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE workflow_node_runs
                   SET status=?,
                       input_envelope_json=?,
                       ignored_inputs_json=?,
                       started_ts=COALESCE(started_ts, ?),
                       updated_at=?
                 WHERE id=?
                """,
                (
                    WORKFLOW_STATUS_RUNNING,
                    _json_dumps(dict(input_envelope)),
                    _json_dumps(list(ignored_inputs or [])),
                    now,
                    now,
                    int(node_run_id),
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def _finalize_node_run(
        self,
        node_run_id: int,
        *,
        status: str,
        output_envelope: Mapping[str, Any],
        input_envelope: Optional[Mapping[str, Any]] = None,
        ignored_inputs: Optional[Sequence[Any]] = None,
        linked_child_summary: Optional[Mapping[str, Any]] = None,
        skip_reason: str = "",
        error: str = "",
    ) -> None:
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            fields = {
                "status": _normalize_status(status),
                "output_envelope_json": _json_dumps(dict(output_envelope)),
                "skip_reason": skip_reason,
                "error": error,
                "finished_ts": now,
                "updated_at": now,
            }
            if input_envelope is not None:
                fields["input_envelope_json"] = _json_dumps(dict(input_envelope))
            if ignored_inputs is not None:
                fields["ignored_inputs_json"] = _json_dumps(list(ignored_inputs))
            if linked_child_summary is not None:
                fields["linked_child_summary_json"] = _json_dumps(dict(linked_child_summary))
            sets = ", ".join(f"{key}=?" for key in fields.keys())
            cur.execute(
                f"UPDATE workflow_node_runs SET {sets} WHERE id=?",
                (*fields.values(), int(node_run_id)),
            )
            conn.commit()
        finally:
            conn.close()

    def _update_run_row(
        self,
        run_id: int,
        *,
        status: Optional[str] = None,
        error: Optional[str] = None,
        skip_reason: Optional[str] = None,
        final_payload: Optional[Mapping[str, Any]] = None,
        final_metadata: Optional[Mapping[str, Any]] = None,
        started_ts: Optional[int] = None,
        finished_ts: Optional[int] = None,
        updated_at: Optional[int] = None,
    ) -> None:
        fields: Dict[str, Any] = {}
        if status is not None:
            fields["status"] = _normalize_status(status)
        if error is not None:
            fields["error"] = str(error or "")
        if skip_reason is not None:
            fields["skip_reason"] = str(skip_reason or "")
        if final_payload is not None:
            fields["final_payload_json"] = _json_dumps(dict(final_payload))
        if final_metadata is not None:
            fields["final_metadata_json"] = _json_dumps(dict(final_metadata))
        if started_ts is not None:
            fields["started_ts"] = int(started_ts)
        if finished_ts is not None:
            fields["finished_ts"] = int(finished_ts)
        fields["updated_at"] = int(updated_at or _now_ts())
        if not fields:
            return
        conn = self._conn()
        try:
            cur = conn.cursor()
            sets = ", ".join(f"{key}=?" for key in fields.keys())
            cur.execute(f"UPDATE workflow_runs SET {sets} WHERE id=?", (*fields.values(), int(run_id)))
            conn.commit()
        finally:
            conn.close()

    def _build_input_envelope(
        self,
        *,
        node_id: str,
        incoming_edges: Sequence[Mapping[str, Any]],
        outputs: Mapping[str, Mapping[str, Any]],
        node_map: Mapping[str, Mapping[str, Any]],
    ) -> Tuple[Dict[str, Any], List[Dict[str, Any]], int]:
        matched_inputs: List[Dict[str, Any]] = []
        ignored_inputs: List[Dict[str, Any]] = []
        inputs_by_port: Dict[str, Dict[str, Any]] = {}
        matched_action_count = 0
        matched_data_count = 0
        for edge in incoming_edges:
            source_id = str(edge.get("source") or "").strip()
            route = _edge_route(edge)
            source_output = outputs.get(source_id) or {}
            source_status = _normalize_status(source_output.get("status"))
            source_port = _edge_source_port(edge, node_map)
            target_port = _edge_target_port(edge, node_map)
            port_kind = str((target_port or source_port or {}).get("kind") or PORT_KIND_DATA)
            source_port_id = str((source_port or {}).get("id") or "")
            target_port_id = str((target_port or {}).get("id") or "")
            target_port_label = str((target_port or {}).get("label") or target_port_id or "Input").strip()
            edge_output = source_output
            edge_status = source_status
            job_output_match_count = None
            if _is_job_output_route_edge(edge, node_map):
                edge_output, job_output_match_count = self._build_routed_job_output_output(source_output, route=route)
                edge_status = _normalize_status(edge_output.get("status"))
            edge_summary = {
                "edge_id": str(edge.get("id") or ""),
                "source_node_id": source_id,
                "source_port_id": source_port_id,
                "source_port_label": str((source_port or {}).get("label") or "").strip(),
                "target_port_id": target_port_id,
                "target_port_label": target_port_label,
                "port_kind": port_kind,
                "route_on": route,
                "status": edge_status,
                "output": edge_output,
            }
            if port_kind == PORT_KIND_ACTION:
                matched = _match_edge_route(route, source_status)
            elif source_port_id == "job_output":
                matched = bool(job_output_match_count)
            else:
                matched = _match_data_edge(source_status)
            if matched:
                matched_inputs.append(edge_summary)
                if target_port_id:
                    inputs_by_port.setdefault(
                        target_port_id,
                        {
                            "label": target_port_label,
                            "kind": port_kind,
                            "inputs": [],
                        },
                    )
                    inputs_by_port[target_port_id]["inputs"].append(edge_summary)
                if port_kind == PORT_KIND_ACTION:
                    matched_action_count += 1
                else:
                    matched_data_count += 1
            else:
                ignored_inputs.append(edge_summary)
        return (
            self._build_output_envelope(
                status=_rollup_status([item.get("status") for item in matched_inputs]) if matched_inputs else WORKFLOW_STATUS_PENDING,
                data={
                    "inputs": matched_inputs,
                    "inputs_by_port": inputs_by_port,
                },
                metadata={
                    "matched_input_count": len(matched_inputs),
                    "matched_action_input_count": matched_action_count,
                    "matched_data_input_count": matched_data_count,
                    "matched_port_count": len(inputs_by_port),
                    "target_node_id": node_id,
                },
                artifacts={},
            ),
            ignored_inputs,
            len(matched_inputs),
        )

    def _missing_required_input_ports(
        self,
        *,
        node: Mapping[str, Any],
        input_envelope: Mapping[str, Any],
    ) -> List[Dict[str, Any]]:
        node_type = str(node.get("type") or "").strip()
        inputs_by_port = {}
        if isinstance(input_envelope.get("data"), Mapping):
            raw_value = input_envelope.get("data", {}).get("inputs_by_port")
            if isinstance(raw_value, Mapping):
                inputs_by_port = raw_value
        missing: List[Dict[str, Any]] = []
        for port in _node_ports(node_type, direction=PORT_DIRECTION_INPUT):
            if not bool(port.get("required")):
                continue
            port_id = str(port.get("id") or "")
            port_items = []
            port_entry = inputs_by_port.get(port_id)
            if isinstance(port_entry, Mapping):
                raw_inputs = port_entry.get("inputs")
                if isinstance(raw_inputs, list):
                    port_items = [dict(item) for item in raw_inputs if isinstance(item, Mapping)]
            if port_items:
                continue
            missing.append(
                {
                    "id": port_id,
                    "label": str(port.get("label") or port_id),
                    "kind": str(port.get("kind") or ""),
                }
            )
        return missing

    def _build_output_envelope(
        self,
        *,
        status: str,
        data: Any,
        metadata: Optional[Mapping[str, Any]] = None,
        artifacts: Optional[Mapping[str, Any]] = None,
    ) -> Dict[str, Any]:
        return {
            "status": _normalize_status(status),
            "data": data,
            "metadata": dict(metadata or {}),
            "artifacts": dict(artifacts or {}),
        }

    def _execute_node(
        self,
        *,
        run_id: int,
        node_run_id: int,
        node: Mapping[str, Any],
        input_envelope: Mapping[str, Any],
        source_metadata: Mapping[str, Any],
        frozen_targets: Mapping[str, Any],
        device_snapshot: Sequence[Mapping[str, Any]],
        online_lookup: Sequence[str],
    ) -> Dict[str, Any]:
        node_type = str(node.get("type") or "").strip()
        if node_type == NODE_TYPE_AGENT_FILTER:
            return self._execute_agent_filter_node(
                node_run_id=node_run_id,
                node=node,
                input_envelope=input_envelope,
                source_metadata=source_metadata,
                device_snapshot=device_snapshot,
            )
        if node_type == NODE_TYPE_AGENT_ARRAY:
            return self._execute_agent_array_node(
                node_run_id=node_run_id,
                node=node,
                input_envelope=input_envelope,
                source_metadata=source_metadata,
                device_snapshot=device_snapshot,
            )
        if node_type == NODE_TYPE_EXECUTE_ASSEMBLY:
            return self._execute_assembly_node(
                run_id=run_id,
                node_run_id=node_run_id,
                node=node,
                input_envelope=input_envelope,
                frozen_targets=frozen_targets,
                device_snapshot=device_snapshot,
                online_lookup=online_lookup,
            )
        if node_type == NODE_TYPE_EXECUTE_SUBWORKFLOW:
            return self._execute_subworkflow_node(
                run_id=run_id,
                node_run_id=node_run_id,
                node=node,
                input_envelope=input_envelope,
                source_metadata=source_metadata,
            )
        output = self._build_output_envelope(
            status=WORKFLOW_STATUS_FAILED,
            data=None,
            metadata={"reason": "unsupported_node"},
            artifacts={},
        )
        self._finalize_node_run(
            node_run_id,
            status=WORKFLOW_STATUS_FAILED,
            output_envelope=output,
            input_envelope=input_envelope,
            error=f"Unsupported executable node type '{node_type}'.",
        )
        return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

    # ------------------------------------------------------------------
    # Workflow-level helpers
    # ------------------------------------------------------------------
    def _freeze_assembly_target_snapshots(
        self,
        nodes: Sequence[Mapping[str, Any]],
        *,
        devices: Optional[Sequence[Dict[str, Any]]] = None,
        online_lookup: Optional[Sequence[str]] = None,
    ) -> Dict[str, Dict[str, Any]]:
        devices = list(devices) if devices is not None else self._filter_matcher.fetch_devices()
        online_lookup = list(online_lookup) if online_lookup is not None else self._online_hostnames_snapshot()
        snapshots: Dict[str, Dict[str, Any]] = {}
        for node in nodes:
            if str(node.get("type") or "").strip() != NODE_TYPE_EXECUTE_ASSEMBLY:
                continue
            node_id = str(node.get("id") or "").strip()
            data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
            target_definition = self._node_target_definition(node)
            execution_mode = self._node_execution_mode(node)
            try:
                _hosts, metadata = self._filter_matcher.resolve_target_entries(target_definition, devices=devices)
                requested = list(metadata.get("resolved_targets") or [])
            except Exception as exc:
                requested = []
                metadata = {"resolution_error": str(exc), "resolved_targets": []}

            active_targets, skipped_targets = self._classify_execution_targets(
                requested_targets=requested,
                execution_mode=execution_mode,
                online_lookup=online_lookup,
            )
            snapshots[node_id] = {
                "requested_targets": requested,
                "active_targets": active_targets,
                "skipped_targets": skipped_targets,
                "resolution_metadata": metadata,
            }
        return snapshots

    def _node_timeout_seconds(self, node: Mapping[str, Any]) -> int:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        for key in ("timeout_override_seconds", "timeout_seconds", "timeout", "timeoutSeconds"):
            value = _coerce_optional_int(data.get(key))
            if value is not None and value >= 0:
                return value
        return 0

    def _node_target_definition(self, node: Mapping[str, Any]) -> List[Any]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        for key in ("target_definition", "targetDefinition", "targets", "targets_json", "targetsJson"):
            raw_value = data.get(key)
            if isinstance(raw_value, list):
                return list(raw_value)
            if isinstance(raw_value, dict):
                return [dict(raw_value)]
            if isinstance(raw_value, str):
                parsed = _parse_json_array(raw_value)
                if parsed:
                    return parsed
                parsed_object = _parse_json_object(raw_value)
                if parsed_object:
                    return [parsed_object]
        return []

    def _node_agent_filter_id(self, node: Mapping[str, Any]) -> Optional[int]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        return _coerce_optional_int(
            data.get("filter_id")
            or data.get("agent_filter_id")
            or data.get("selected_filter_id")
            or data.get("id")
        )

    def _node_agent_filter_summary(self, node: Mapping[str, Any]) -> Dict[str, Any]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        site_names = [str(value).strip() for value in (data.get("site_names") or data.get("siteNames") or []) if str(value).strip()]
        return {
            "filter_id": self._node_agent_filter_id(node),
            "name": str(
                data.get("filter_name")
                or data.get("selected_filter_name")
                or data.get("name")
                or data.get("label")
                or "Device Filter"
            ).strip(),
            "site_mode": str(data.get("site_mode") or data.get("siteMode") or "global").strip().lower() or "global",
            "site_names": site_names,
            "scope_summary": str(data.get("scope_summary") or data.get("scopeSummary") or "").strip(),
        }

    def _node_agent_array_entries(self, node: Mapping[str, Any]) -> List[Dict[str, Any]]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        raw_entries = (
            data.get("selected_devices")
            or data.get("selectedDevices")
            or data.get("devices")
            or data.get("targets")
            or []
        )
        if isinstance(raw_entries, str):
            raw_entries = _parse_json_array(raw_entries)
        if not isinstance(raw_entries, list):
            return []
        entries: List[Dict[str, Any]] = []
        for entry in raw_entries:
            if not isinstance(entry, Mapping):
                continue
            hostname = str(entry.get("hostname") or "").strip()
            device_guid = str(entry.get("device_guid") or entry.get("guid") or "").strip()
            if not hostname and not device_guid:
                continue
            entries.append(
                {
                    "kind": "device",
                    "hostname": hostname,
                    "device_guid": device_guid,
                    "site_id": _coerce_optional_int(entry.get("site_id")),
                    "site_name": str(entry.get("site_name") or entry.get("site") or "").strip(),
                    "agent_id": str(entry.get("agent_id") or "").strip(),
                    "connection_type": str(entry.get("connection_type") or "").strip(),
                }
            )
        return entries

    def _dedupe_target_records(self, records: Sequence[Mapping[str, Any]]) -> List[Dict[str, Any]]:
        deduped: List[Dict[str, Any]] = []
        seen: set[str] = set()
        for record in records or []:
            entry = dict(record or {})
            device_guid = str(entry.get("device_guid") or entry.get("guid") or "").strip().lower()
            hostname = str(entry.get("hostname") or "").strip().lower()
            site_id = _coerce_optional_int(entry.get("site_id"))
            if device_guid:
                key = f"guid:{device_guid}"
            elif hostname and site_id is not None:
                key = f"site:{site_id}:{hostname}"
            elif hostname:
                key = f"host:{hostname}"
            else:
                continue
            if key in seen:
                continue
            seen.add(key)
            deduped.append(entry)
        return deduped

    def _site_scope_targets(
        self,
        records: Sequence[Mapping[str, Any]],
        *,
        source_metadata: Mapping[str, Any],
    ) -> List[Dict[str, Any]]:
        scope = source_metadata.get("workflow_site_scope") if isinstance(source_metadata, Mapping) else None
        if not isinstance(scope, Mapping):
            return [dict(record or {}) for record in (records or []) if isinstance(record, Mapping)]
        try:
            site_id = int(scope.get("site_id")) if scope.get("site_id") not in (None, "", "null") else 0
        except Exception:
            site_id = 0
        if site_id <= 0:
            return [dict(record or {}) for record in (records or []) if isinstance(record, Mapping)]
        scoped = []
        for record in records or []:
            if not isinstance(record, Mapping):
                continue
            try:
                record_site_id = int(record.get("site_id")) if record.get("site_id") not in (None, "", "null") else 0
            except Exception:
                record_site_id = 0
            if record_site_id == site_id:
                scoped.append(dict(record))
        return scoped

    def _classify_execution_targets(
        self,
        *,
        requested_targets: Sequence[Mapping[str, Any]],
        execution_mode: str,
        online_lookup: Sequence[str],
    ) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]]]:
        active_targets: List[Dict[str, Any]] = []
        skipped_targets: List[Dict[str, Any]] = []
        online = {str(value or "").strip().lower() for value in (online_lookup or []) if str(value or "").strip()}
        local_aliases = {ENGINE_LOCAL_ALIAS.lower(), "localhost", "127.0.0.1", "::1"}
        for target in self._dedupe_target_records(requested_targets):
            hostname = str(target.get("hostname") or "").strip()
            host_key = hostname.lower()
            reason = ""
            if execution_mode in {"system", "currentuser"}:
                if host_key not in online:
                    reason = "offline_at_workflow_start"
            elif execution_mode == "local":
                if host_key not in local_aliases:
                    reason = "local_mode_requires_engine_host"
            else:
                if host_key not in online:
                    reason = "offline_at_workflow_start"
                elif not str(target.get("agent_id") or "").strip():
                    reason = "missing_agent_id"
            if reason:
                skipped_targets.append({**dict(target), "reason": reason})
            else:
                active_targets.append(dict(target))
        return active_targets, skipped_targets

    def _port_inputs(self, input_envelope: Mapping[str, Any], port_id: str) -> List[Dict[str, Any]]:
        data = input_envelope.get("data") if isinstance(input_envelope.get("data"), Mapping) else {}
        inputs_by_port = data.get("inputs_by_port") if isinstance(data.get("inputs_by_port"), Mapping) else {}
        port_payload = inputs_by_port.get(str(port_id or ""))
        if isinstance(port_payload, Mapping):
            raw_inputs = port_payload.get("inputs")
            if isinstance(raw_inputs, list):
                return [dict(item) for item in raw_inputs if isinstance(item, Mapping)]
        return []

    def _job_output_records_to_targets(self, records: Sequence[Mapping[str, Any]]) -> List[Dict[str, Any]]:
        return self._dedupe_target_records(
            [
                {
                    "kind": "device",
                    "hostname": str(record.get("hostname") or "").strip(),
                    "device_guid": str(record.get("device_guid") or "").strip(),
                    "site_id": _coerce_optional_int(record.get("site_id")),
                    "site_name": str(record.get("site_name") or "").strip(),
                    "agent_id": str(record.get("agent_id") or "").strip(),
                }
                for record in records or []
                if isinstance(record, Mapping)
                and (str(record.get("hostname") or "").strip() or str(record.get("device_guid") or "").strip())
            ]
        )

    def _build_routed_job_output_output(
        self,
        source_output: Mapping[str, Any],
        *,
        route: str,
    ) -> Tuple[Dict[str, Any], int]:
        output_data = source_output.get("data") if isinstance(source_output.get("data"), Mapping) else {}
        raw_job_output = output_data.get("job_output") if isinstance(output_data.get("job_output"), list) else []
        matching_records = [
            dict(record)
            for record in raw_job_output
            if isinstance(record, Mapping) and _match_edge_route(route, record.get("status"))
        ]
        targets = self._job_output_records_to_targets(matching_records)
        routed_status = (
            _rollup_status([record.get("status") for record in matching_records])
            if matching_records
            else WORKFLOW_STATUS_SKIPPED
        )
        routed_output = self._build_output_envelope(
            status=routed_status,
            data={
                "job_output": matching_records,
                "targets": targets,
            },
            metadata={
                **dict(source_output.get("metadata") or {}),
                "job_output_route": route,
                "filtered_job_output_count": len(matching_records),
                "target_count": len(targets),
                "source_output_status": _normalize_status(source_output.get("status")),
            },
            artifacts=dict(source_output.get("artifacts") or {}),
        )
        return routed_output, len(matching_records)

    def _extract_targets_from_input_envelope(self, input_envelope: Mapping[str, Any]) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]]]:
        matched_inputs = self._port_inputs(input_envelope, "targets")
        requested_targets: List[Dict[str, Any]] = []
        source_summaries: List[Dict[str, Any]] = []
        for item in matched_inputs or []:
            if not isinstance(item, Mapping):
                continue
            output = item.get("output") if isinstance(item.get("output"), Mapping) else {}
            output_data = output.get("data") if isinstance(output.get("data"), Mapping) else {}
            targets = output_data.get("targets") if isinstance(output_data.get("targets"), list) else []
            if not targets:
                continue
            requested_targets.extend(dict(target) for target in targets if isinstance(target, Mapping))
            source_summaries.append(
                {
                    "source_node_id": str(item.get("source_node_id") or "").strip(),
                    "source_port_id": str(item.get("source_port_id") or "").strip(),
                    "source_port_label": str(item.get("source_port_label") or "").strip(),
                    "route_on": str(item.get("route_on") or "").strip(),
                    "target_count": len(targets),
                    "status": _normalize_status(output.get("status")),
                    "target_node_type": str(
                        (output.get("metadata") or {}).get("target_node_type")
                        if isinstance(output.get("metadata"), Mapping)
                        else ""
                    ).strip(),
                }
            )
        return self._dedupe_target_records(requested_targets), source_summaries

    def _extract_job_output_records(self, input_envelope: Mapping[str, Any]) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]]]:
        matched_inputs = self._port_inputs(input_envelope, "job_output")
        records: List[Dict[str, Any]] = []
        source_summaries: List[Dict[str, Any]] = []
        for item in matched_inputs or []:
            if not isinstance(item, Mapping):
                continue
            output = item.get("output") if isinstance(item.get("output"), Mapping) else {}
            output_data = output.get("data") if isinstance(output.get("data"), Mapping) else {}
            job_output = output_data.get("job_output")
            if isinstance(job_output, list):
                records.extend(dict(entry) for entry in job_output if isinstance(entry, Mapping))
            source_summaries.append(
                {
                    "source_node_id": str(item.get("source_node_id") or "").strip(),
                    "status": _normalize_status(output.get("status")),
                    "port_kind": str(item.get("port_kind") or ""),
                    "result_count": len(job_output) if isinstance(job_output, list) else 0,
                }
            )
        return records, source_summaries

    def _node_execution_mode(self, node: Mapping[str, Any]) -> str:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        for key in ("execution_mode", "executionMode", "run_mode", "runMode", "execution_context"):
            value = str(data.get(key) or "").strip().lower()
            if value:
                return value
        return "system"

    def _build_job_output_records(
        self,
        *,
        results: Sequence[Mapping[str, Any]],
        requested_targets: Sequence[Mapping[str, Any]],
        skipped_targets: Sequence[Mapping[str, Any]],
    ) -> List[Dict[str, Any]]:
        target_lookup: Dict[str, Dict[str, Any]] = {}
        for target in requested_targets or []:
            if not isinstance(target, Mapping):
                continue
            hostname = str(target.get("hostname") or "").strip().lower()
            if hostname:
                target_lookup[hostname] = dict(target)
        records: List[Dict[str, Any]] = []
        seen_keys: set[str] = set()
        for result in results or []:
            if not isinstance(result, Mapping):
                continue
            hostname = str(result.get("hostname") or "").strip()
            target = dict(target_lookup.get(hostname.lower()) or {})
            device_guid = str(target.get("device_guid") or target.get("guid") or "").strip()
            site_id = _coerce_optional_int(target.get("site_id"))
            dedupe_key = f"result:{device_guid or hostname.lower()}:{_normalize_status(result.get('status'))}"
            if dedupe_key in seen_keys:
                continue
            seen_keys.add(dedupe_key)
            records.append(
                {
                    "hostname": hostname,
                    "device_guid": device_guid,
                    "site_id": site_id,
                    "site_name": str(target.get("site_name") or "").strip(),
                    "agent_id": str(target.get("agent_id") or "").strip(),
                    "status": _normalize_status(result.get("status")),
                    "activity_id": _coerce_optional_int(result.get("activity_id")),
                    "stdout": _truncate_text(result.get("stdout")),
                    "stderr": _truncate_text(result.get("stderr")),
                }
            )
        for target in skipped_targets or []:
            if not isinstance(target, Mapping):
                continue
            hostname = str(target.get("hostname") or "").strip()
            device_guid = str(target.get("device_guid") or target.get("guid") or "").strip()
            dedupe_key = f"skipped:{device_guid or hostname.lower()}"
            if dedupe_key in seen_keys:
                continue
            seen_keys.add(dedupe_key)
            records.append(
                {
                    "hostname": hostname,
                    "device_guid": device_guid,
                    "site_id": _coerce_optional_int(target.get("site_id")),
                    "site_name": str(target.get("site_name") or "").strip(),
                    "agent_id": str(target.get("agent_id") or "").strip(),
                    "status": WORKFLOW_STATUS_WARNING,
                    "activity_id": None,
                    "stdout": "",
                    "stderr": str(target.get("reason") or "Target was skipped before execution."),
                }
            )
        return records

    def _collect_exports(
        self,
        *,
        node_map: Mapping[str, Mapping[str, Any]],
        outputs: Mapping[str, Mapping[str, Any]],
    ) -> Dict[str, Any]:
        exports: Dict[str, Any] = {}
        for node_id, node in node_map.items():
            data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
            export_key = str(data.get("export_key") or data.get("exportKey") or "").strip()
            if not export_key:
                continue
            output = outputs.get(node_id)
            if not output:
                continue
            exports[export_key] = output
        return exports

    def _sink_node_outputs(
        self,
        *,
        node_map: Mapping[str, Mapping[str, Any]],
        edges: Sequence[Mapping[str, Any]],
        outputs: Mapping[str, Mapping[str, Any]],
    ) -> List[Dict[str, Any]]:
        source_ids = {str(edge.get("source") or "").strip() for edge in edges if isinstance(edge, Mapping)}
        sink_ids = [node_id for node_id in node_map if node_id not in source_ids]
        terminal = []
        for node_id in sink_ids:
            output = outputs.get(node_id)
            if output:
                terminal.append({"node_id": node_id, "output": output})
        terminal.sort(key=lambda item: _status_priority(item["output"].get("status") or ""), reverse=True)
        return terminal

    def _collect_terminal_job_output(self, sink_nodes: Sequence[Mapping[str, Any]]) -> List[Dict[str, Any]]:
        collected: List[Dict[str, Any]] = []
        for item in sink_nodes or []:
            output = item.get("output") if isinstance(item, Mapping) else {}
            data = output.get("data") if isinstance(output, Mapping) and isinstance(output.get("data"), Mapping) else {}
            job_output = data.get("job_output") if isinstance(data.get("job_output"), list) else []
            collected.extend(dict(entry) for entry in job_output if isinstance(entry, Mapping))
        deduped: List[Dict[str, Any]] = []
        seen: set[str] = set()
        for entry in collected:
            hostname = str(entry.get("hostname") or "").strip().lower()
            device_guid = str(entry.get("device_guid") or "").strip().lower()
            status = _normalize_status(entry.get("status"))
            key = f"{device_guid or hostname}:{status}"
            if not key.strip(":") or key in seen:
                continue
            seen.add(key)
            deduped.append(entry)
        return deduped

    # ------------------------------------------------------------------
    # Target node execution
    # ------------------------------------------------------------------
    def _execute_agent_filter_node(
        self,
        *,
        node_run_id: int,
        node: Mapping[str, Any],
        input_envelope: Mapping[str, Any],
        source_metadata: Mapping[str, Any],
        device_snapshot: Sequence[Mapping[str, Any]],
    ) -> Dict[str, Any]:
        filter_summary = self._node_agent_filter_summary(node)
        filter_id = filter_summary.get("filter_id")
        if filter_id is None:
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_FAILED,
                data=None,
                metadata={"reason": "missing_filter_id"},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_FAILED,
                output_envelope=output,
                input_envelope=input_envelope,
                error="Device Filter node is missing a selected device filter.",
            )
            return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

        filters = self._filter_matcher.load_filters([int(filter_id)], include_archived=False)
        filter_record = filters.get(int(filter_id))
        if not filter_record or filter_record.get("archived"):
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_FAILED,
                data=None,
                metadata={"reason": "filter_not_found", "filter_id": int(filter_id)},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_FAILED,
                output_envelope=output,
                input_envelope=input_envelope,
                error=f"Selected device filter '{filter_summary.get('name') or filter_id}' was not found.",
            )
            return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

        _hosts, metadata = self._filter_matcher.resolve_target_entries(
            [{"kind": "filter", "filter_id": int(filter_id)}],
            devices=device_snapshot,
            filters_by_id={int(filter_id): filter_record},
        )
        targets = self._site_scope_targets(
            self._dedupe_target_records(metadata.get("resolved_targets") or []),
            source_metadata=source_metadata,
        )
        status = WORKFLOW_STATUS_SUCCESS if targets else WORKFLOW_STATUS_WARNING
        output = self._build_output_envelope(
            status=status,
            data={
                "target_definition": [{"kind": "filter", "filter_id": int(filter_id)}],
                "targets": targets,
                "filter": {
                    "id": int(filter_id),
                    "name": str(filter_record.get("name") or filter_summary.get("name") or "").strip(),
                    "site_mode": str(filter_record.get("site_mode") or filter_summary.get("site_mode") or "global"),
                    "site_names": list(filter_record.get("site_names") or filter_summary.get("site_names") or []),
                },
            },
            metadata={
                "target_node_type": NODE_TYPE_AGENT_FILTER,
                "target_count": len(targets),
                "scope_summary": filter_summary.get("scope_summary") or "",
            },
            artifacts={},
        )
        self._finalize_node_run(
            node_run_id,
            status=status,
            output_envelope=output,
            input_envelope=input_envelope,
            error="" if status == WORKFLOW_STATUS_SUCCESS else "Selected device filter resolved zero devices.",
        )
        return {"status": status, "output_envelope": output}

    def _execute_agent_array_node(
        self,
        *,
        node_run_id: int,
        node: Mapping[str, Any],
        input_envelope: Mapping[str, Any],
        source_metadata: Mapping[str, Any],
        device_snapshot: Sequence[Mapping[str, Any]],
    ) -> Dict[str, Any]:
        selected_devices = self._node_agent_array_entries(node)
        if not selected_devices:
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_WARNING,
                data={"target_definition": [], "targets": []},
                metadata={"reason": "no_selected_devices", "target_node_type": NODE_TYPE_AGENT_ARRAY, "target_count": 0},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_WARNING,
                output_envelope=output,
                input_envelope=input_envelope,
                error="List of Devices node has no selected devices.",
            )
            return {"status": WORKFLOW_STATUS_WARNING, "output_envelope": output}

        _hosts, metadata = self._filter_matcher.resolve_target_entries(selected_devices, devices=device_snapshot)
        targets = self._site_scope_targets(
            self._dedupe_target_records(metadata.get("resolved_targets") or []),
            source_metadata=source_metadata,
        )
        status = WORKFLOW_STATUS_SUCCESS if targets else WORKFLOW_STATUS_WARNING
        output = self._build_output_envelope(
            status=status,
            data={
                "target_definition": selected_devices,
                "targets": targets,
            },
            metadata={
                "target_node_type": NODE_TYPE_AGENT_ARRAY,
                "target_count": len(targets),
                "selected_count": len(selected_devices),
            },
            artifacts={},
        )
        self._finalize_node_run(
            node_run_id,
            status=status,
            output_envelope=output,
            input_envelope=input_envelope,
            error="" if status == WORKFLOW_STATUS_SUCCESS else "List of Devices node resolved zero devices.",
        )
        return {"status": status, "output_envelope": output}

    # ------------------------------------------------------------------
    # Assembly node execution
    # ------------------------------------------------------------------
    def _execute_assembly_node(
        self,
        *,
        run_id: int,
        node_run_id: int,
        node: Mapping[str, Any],
        input_envelope: Mapping[str, Any],
        frozen_targets: Mapping[str, Any],
        device_snapshot: Sequence[Mapping[str, Any]],
        online_lookup: Sequence[str],
    ) -> Dict[str, Any]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        assembly_guid = str(data.get("assembly_guid") or data.get("assemblyGuid") or "").strip()
        if not assembly_guid:
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_FAILED,
                data=None,
                metadata={"reason": "missing_assembly_guid"},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_FAILED,
                output_envelope=output,
                input_envelope=input_envelope,
                error="Execute Assembly node is missing a selected assembly.",
            )
            return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

        export_doc = self._assembly_runtime.export_assembly(assembly_guid)
        assembly_type = str(export_doc.get("type") or export_doc.get("assembly_type") or "").strip().lower()
        if assembly_type == "workflow":
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_FAILED,
                data=None,
                metadata={"reason": "workflow_requires_subworkflow_node"},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_FAILED,
                output_envelope=output,
                input_envelope=input_envelope,
                error="Workflow assemblies must be executed with Execute Subworkflow nodes.",
            )
            return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

        execution_mode = self._node_execution_mode(node)
        input_targets, target_source_summaries = self._extract_targets_from_input_envelope(input_envelope)
        requested_targets = list(input_targets)
        target_resolution_metadata: Dict[str, Any] = {
            "target_sources": target_source_summaries,
        }
        if not requested_targets:
            legacy_definition = self._node_target_definition(node)
            if legacy_definition:
                try:
                    _hosts, metadata = self._filter_matcher.resolve_target_entries(legacy_definition, devices=device_snapshot)
                    requested_targets = self._dedupe_target_records(metadata.get("resolved_targets") or [])
                    target_resolution_metadata["legacy_target_definition"] = legacy_definition
                    target_resolution_metadata["legacy_resolution_metadata"] = metadata
                except Exception as exc:
                    target_resolution_metadata["legacy_resolution_error"] = str(exc)
            elif frozen_targets:
                requested_targets = list(frozen_targets.get("requested_targets") or [])
                target_resolution_metadata["legacy_resolution_metadata"] = dict(
                    frozen_targets.get("resolution_metadata") or {}
                )
        active_targets, skipped_targets = self._classify_execution_targets(
            requested_targets=requested_targets,
            execution_mode=execution_mode,
            online_lookup=online_lookup,
        )
        timeout_seconds = self._resolved_assembly_timeout(export_doc, node)

        try:
            if assembly_type == "ansible":
                result = self._execute_ansible_assembly(
                    run_id=run_id,
                    node_run_id=node_run_id,
                    node=node,
                    export_doc=export_doc,
                    execution_mode=execution_mode,
                    active_targets=active_targets,
                    skipped_targets=skipped_targets,
                    timeout_seconds=timeout_seconds,
                )
            else:
                result = self._execute_script_assembly(
                    run_id=run_id,
                    node_run_id=node_run_id,
                    node=node,
                    export_doc=export_doc,
                    execution_mode=execution_mode,
                    active_targets=active_targets,
                    skipped_targets=skipped_targets,
                    timeout_seconds=timeout_seconds,
                )
        except Exception as exc:
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_FAILED,
                data=None,
                metadata={"reason": "assembly_execution_exception"},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_FAILED,
                output_envelope=output,
                input_envelope=input_envelope,
                error=str(exc),
            )
            return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

        output_envelope = result.get("output_envelope") if isinstance(result.get("output_envelope"), Mapping) else {}
        output_data = output_envelope.get("data") if isinstance(output_envelope.get("data"), Mapping) else {}
        if isinstance(output_data, Mapping):
            output_data = dict(output_data)
            output_data["job_output"] = self._build_job_output_records(
                results=output_data.get("results") if isinstance(output_data.get("results"), list) else [],
                requested_targets=requested_targets,
                skipped_targets=skipped_targets,
            )
            output_envelope = {
                **dict(output_envelope),
                "data": output_data,
            }
        merged_metadata = dict(output_envelope.get("metadata") or {})
        merged_metadata["requested_targets"] = requested_targets
        merged_metadata.update(target_resolution_metadata)
        output_envelope = {
            **dict(output_envelope),
            "metadata": merged_metadata,
        }
        result = {**result, "output_envelope": output_envelope}

        self._finalize_node_run(
            node_run_id,
            status=result["status"],
            output_envelope=result["output_envelope"],
            input_envelope=input_envelope,
            linked_child_summary=result.get("child_summary"),
            error=result.get("error", ""),
        )
        return {"status": result["status"], "output_envelope": result["output_envelope"]}

    def _resolved_assembly_timeout(self, export_doc: Mapping[str, Any], node: Mapping[str, Any]) -> int:
        node_timeout = self._node_timeout_seconds(node)
        if node_timeout > 0:
            return node_timeout
        document = _load_assembly_document(
            str(export_doc.get("name") or "Assembly"),
            str(export_doc.get("type") or "powershell"),
            payload=dict(export_doc),
        )
        return max(0, _coerce_int(document.get("timeout_seconds"), default=0))

    def _execute_script_assembly(
        self,
        *,
        run_id: int,
        node_run_id: int,
        node: Mapping[str, Any],
        export_doc: Mapping[str, Any],
        execution_mode: str,
        active_targets: Sequence[Mapping[str, Any]],
        skipped_targets: Sequence[Mapping[str, Any]],
        timeout_seconds: int,
    ) -> Dict[str, Any]:
        if execution_mode not in {"system", "currentuser"}:
            raise RuntimeError("Script assembly workflow nodes currently support only system/currentuser execution modes.")
        if not active_targets:
            status = WORKFLOW_STATUS_WARNING if skipped_targets else WORKFLOW_STATUS_WARNING
            output = self._build_output_envelope(
                status=status,
                data={"results": []},
                metadata={"skipped_targets": list(skipped_targets), "reason": "no_active_targets"},
                artifacts={},
            )
            return {
                "status": status,
                "output_envelope": output,
                "child_summary": {"count": 0, "status": status},
                "error": "",
            }

        rel_path = _normalize_script_relpath(export_doc.get("path") or export_doc.get("script_path") or export_doc.get("scriptPath"))
        source_identifier = rel_path or str(export_doc.get("name") or "Assembly")
        doc = _load_assembly_document(source_identifier, str(export_doc.get("type") or "powershell"), payload=dict(export_doc))
        script_type = _normalize_agent_script_type(doc.get("type"))
        if script_type not in _SUPPORTED_AGENT_SCRIPT_TYPES:
            raise RuntimeError(f"Unsupported script assembly type '{script_type}'.")
        content = str(doc.get("script") or "")
        overrides = self._variable_override_map(node)
        env_map, variables, literal_lookup = prepare_variable_context(
            doc.get("variables") if isinstance(doc.get("variables"), list) else [],
            overrides,
        )
        normalized_script = _rewrite_script_for_dispatch(content, script_type, literal_lookup)
        script_bytes = normalized_script.encode("utf-8")
        encoded_content = base64.b64encode(script_bytes).decode("ascii") if script_bytes or normalized_script == "" else ""
        results: List[Dict[str, Any]] = []
        child_jobs: List[Dict[str, Any]] = []
        for target in active_targets:
            hostname = str(target.get("hostname") or "").strip()
            if not hostname:
                continue
            activity_id, dispatch_error = self._dispatch_script_activity(
                hostname=hostname,
                script_name=str(doc.get("name") or export_doc.get("name") or "Assembly"),
                script_path=rel_path or str(export_doc.get("name") or "Assembly"),
                script_type=script_type,
                script_bytes=script_bytes,
                script_content_b64=encoded_content,
                environment=env_map,
                variables=variables,
                timeout_seconds=timeout_seconds,
                files=doc.get("files") if isinstance(doc.get("files"), list) else [],
                run_mode=execution_mode,
                context={
                    "workflow_run_id": int(run_id),
                    "workflow_node_run_id": int(node_run_id),
                    "assembly_guid": str(export_doc.get("assembly_guid") or ""),
                },
            )
            child_job_id = self._create_child_job(
                workflow_run_id=run_id,
                workflow_node_run_id=node_run_id,
                child_kind="script",
                target_hostname=hostname,
                component_guid=str(export_doc.get("assembly_guid") or ""),
                component_name=str(doc.get("name") or export_doc.get("name") or "Assembly"),
                component_kind="script",
                activity_id=activity_id,
                child_identifier=str(activity_id or ""),
                status=WORKFLOW_STATUS_RUNNING if activity_id and not dispatch_error else WORKFLOW_STATUS_FAILED,
                payload={"hostname": hostname, "dispatch_error": dispatch_error or ""},
            )
            if activity_id and not dispatch_error:
                child_jobs.append({"child_job_id": child_job_id, "hostname": hostname, "activity_id": activity_id})
            else:
                self._update_child_job(
                    child_job_id,
                    status=WORKFLOW_STATUS_FAILED,
                    stderr_summary=dispatch_error or "Unable to dispatch script assembly.",
                )
                results.append(
                    {
                        "hostname": hostname,
                        "activity_id": activity_id,
                        "status": WORKFLOW_STATUS_FAILED,
                        "stdout": "",
                        "stderr": dispatch_error or "Unable to dispatch script assembly.",
                    }
                )

        if child_jobs:
            results.extend(self._wait_for_activity_results(child_jobs, timeout_seconds=timeout_seconds))
        for result in results:
            child_job_id = next(
                (
                    child["child_job_id"]
                    for child in child_jobs
                    if int(child.get("activity_id") or 0) == int(result.get("activity_id") or 0)
                ),
                None,
            )
            if child_job_id:
                self._update_child_job(
                    int(child_job_id),
                    status=result.get("status") or WORKFLOW_STATUS_FAILED,
                    stdout_summary=result.get("stdout"),
                    stderr_summary=result.get("stderr"),
                    payload=result,
                )

        node_status = self._rollup_child_results(results, skipped_targets=skipped_targets)
        output = self._build_output_envelope(
            status=node_status,
            data={"results": results},
            metadata={"skipped_targets": list(skipped_targets), "execution_mode": execution_mode},
            artifacts={"activity_ids": [result.get("activity_id") for result in results if result.get("activity_id")]},
        )
        return {
            "status": node_status,
            "output_envelope": output,
            "child_summary": {"count": len(results), "status": node_status},
            "error": "",
        }

    def _execute_ansible_assembly(
        self,
        *,
        run_id: int,
        node_run_id: int,
        node: Mapping[str, Any],
        export_doc: Mapping[str, Any],
        execution_mode: str,
        active_targets: Sequence[Mapping[str, Any]],
        skipped_targets: Sequence[Mapping[str, Any]],
        timeout_seconds: int,
    ) -> Dict[str, Any]:
        if self._ansible_runner is None:
            raise RuntimeError("Engine-side Ansible runner is not configured.")
        if execution_mode not in {"local", "ssh", "winrm"}:
            raise RuntimeError("Ansible workflow nodes require execution mode local, ssh, or winrm.")

        credential_id = _coerce_optional_int(
            (node.get("data") or {}).get("credential_id")
            if isinstance(node.get("data"), Mapping)
            else None
        )
        use_service_account = _coerce_bool(
            (node.get("data") or {}).get("use_service_account")
            if isinstance(node.get("data"), Mapping)
            else False
        )

        doc = _load_assembly_document(
            str(export_doc.get("name") or "Assembly"),
            "ansible",
            payload=dict(export_doc),
        )
        normalized_script = str(doc.get("script") or "").replace("\r\n", "\n")
        runtime_files: List[Dict[str, Any]] = []
        credential = self._load_credential(credential_id) if credential_id is not None else None

        if execution_mode == "local":
            local_site_ids = {
                _coerce_int(target.get("site_id"), default=0)
                for target in active_targets or []
                if isinstance(target, Mapping) and _coerce_int(target.get("site_id"), default=0) > 0
            }
            local_site_id = next(iter(local_site_ids), 0) if len(local_site_ids) == 1 else 0
            target_specifications = [
                {
                    "hostname": ENGINE_LOCAL_ALIAS,
                    "inventory_hostname": ENGINE_LOCAL_ALIAS,
                    "site_group": "site_local",
                    "site_id": local_site_id,
                    "host_vars": {"ansible_connection": "local"},
                }
            ]
        else:
            if not active_targets:
                status = WORKFLOW_STATUS_WARNING if skipped_targets else WORKFLOW_STATUS_WARNING
                output = self._build_output_envelope(
                    status=status,
                    data={"results": []},
                    metadata={"skipped_targets": list(skipped_targets), "reason": "no_active_targets"},
                    artifacts={},
                )
                return {
                    "status": status,
                    "output_envelope": output,
                    "child_summary": {"count": 0, "status": status},
                    "error": "",
                }
            target_specifications, runtime_files = self._build_ansible_target_specifications(
                execution_mode=execution_mode,
                active_targets=active_targets,
                credential=credential,
                use_service_account=use_service_account,
            )

        activity_id = self._create_activity_history_row(
            hostname=ENGINE_LOCAL_ALIAS,
            script_path=str(export_doc.get("path") or export_doc.get("name") or "Assembly"),
            script_name=str(doc.get("name") or export_doc.get("name") or "Assembly"),
            script_type="ansible",
        )
        child_job_id = self._create_child_job(
            workflow_run_id=run_id,
            workflow_node_run_id=node_run_id,
            child_kind="ansible",
            target_hostname=ENGINE_LOCAL_ALIAS,
            component_guid=str(export_doc.get("assembly_guid") or ""),
            component_name=str(doc.get("name") or export_doc.get("name") or "Assembly"),
            component_kind="ansible",
            activity_id=activity_id,
            child_identifier=str(activity_id),
            status=WORKFLOW_STATUS_RUNNING,
            payload={"execution_mode": execution_mode},
        )

        self._ansible_runner.queue_run(
            hostname=ENGINE_LOCAL_ALIAS,
            playbook_rel_path=str(export_doc.get("path") or export_doc.get("name") or "workflow-node-playbook"),
            playbook_name=str(doc.get("name") or export_doc.get("name") or "Assembly"),
            playbook_content=normalized_script,
            credential_id=credential_id,
            variable_values=self._variable_override_map(node),
            payload_files=doc.get("files") if isinstance(doc.get("files"), list) else [],
            target_specifications=target_specifications,
            runtime_files=runtime_files,
            source="workflow_run",
            activity_id=activity_id,
            connection=execution_mode,
        )
        results = self._wait_for_activity_results(
            [{"child_job_id": child_job_id, "hostname": ENGINE_LOCAL_ALIAS, "activity_id": activity_id}],
            timeout_seconds=timeout_seconds,
        )
        result = results[0] if results else {
            "hostname": ENGINE_LOCAL_ALIAS,
            "activity_id": activity_id,
            "status": WORKFLOW_STATUS_FAILED,
            "stdout": "",
            "stderr": "Ansible execution returned no result.",
        }
        self._update_child_job(
            child_job_id,
            status=result.get("status") or WORKFLOW_STATUS_FAILED,
            stdout_summary=result.get("stdout"),
            stderr_summary=result.get("stderr"),
            payload=result,
        )
        node_status = self._rollup_child_results([result], skipped_targets=skipped_targets)
        output = self._build_output_envelope(
            status=node_status,
            data={"results": [result]},
            metadata={"skipped_targets": list(skipped_targets), "execution_mode": execution_mode},
            artifacts={"activity_id": activity_id},
        )
        return {
            "status": node_status,
            "output_envelope": output,
            "child_summary": {"count": 1, "status": node_status},
            "error": "",
        }

    def _build_ansible_target_specifications(
        self,
        *,
        execution_mode: str,
        active_targets: Sequence[Mapping[str, Any]],
        credential: Optional[Mapping[str, Any]],
        use_service_account: bool,
    ) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]]]:
        runtime_files: List[Dict[str, Any]] = []
        normalized_private_key = ""
        if execution_mode == "ssh" and credential:
            normalized_private_key = str(credential.get("private_key") or "").replace("\r\n", "\n").replace("\r", "\n")
            if normalized_private_key and not normalized_private_key.endswith("\n"):
                normalized_private_key += "\n"
            passphrase = str(credential.get("private_key_passphrase") or "").strip()
            if normalized_private_key and passphrase and not str(credential.get("password") or "").strip():
                raise RuntimeError(
                    "Passphrase-protected SSH private keys are not yet supported for workflow Ansible runs."
                )
            if passphrase and str(credential.get("password") or "").strip():
                normalized_private_key = ""
        private_key_path = ""
        if execution_mode == "ssh" and normalized_private_key:
            private_key_path = "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
            runtime_files.append(
                {
                    "relative_path": "auth/id_borealis_ssh",
                    "content": normalized_private_key,
                    "mode": 0o600,
                }
            )

        required_vpn_ports: List[int] = []
        for target in active_targets:
            endpoint_port = _coerce_optional_int(str(target.get("connection_endpoint") or "").rsplit(":", 1)[-1])
            if execution_mode == "ssh":
                required_vpn_ports.append(endpoint_port or 22)
            elif execution_mode == "winrm":
                required_vpn_ports.append(endpoint_port or 5985)
        sessions = self._prepare_vpn_sessions(
            [str(target.get("agent_id") or "").strip() for target in active_targets],
            required_ports=required_vpn_ports,
        )
        target_specifications: List[Dict[str, Any]] = []
        for target in active_targets:
            hostname = str(target.get("hostname") or "").strip()
            if not hostname:
                continue
            session = sessions.get(str(target.get("agent_id") or "").strip()) or {}
            peer_ip = str(session.get("virtual_ip") or "").split("/", 1)[0]
            if not peer_ip:
                raise RuntimeError(f"WireGuard connectivity is unavailable for '{hostname}'.")
            endpoint_port = _coerce_optional_int(str(target.get("connection_endpoint") or "").rsplit(":", 1)[-1])
            site_id = _coerce_optional_int(target.get("site_id"))
            site_name = str(target.get("site_name") or "").strip()
            host_vars: Dict[str, Any] = {
                "ansible_host": peer_ip,
                "ansible_connection": execution_mode,
            }
            if execution_mode == "ssh":
                if endpoint_port and endpoint_port != 22:
                    host_vars["ansible_port"] = endpoint_port
                apply_ssh_credential_host_vars(host_vars, credential, private_key_path=private_key_path)
            elif execution_mode == "winrm":
                username = ""
                password = ""
                transport = "ntlm"
                if use_service_account:
                    raise RuntimeError("Workflow Ansible WinRM nodes do not yet support service-account resolution.")
                if credential:
                    username = str(credential.get("username") or "").strip()
                    password = str(credential.get("password") or "").strip()
                    metadata = credential.get("metadata") if isinstance(credential.get("metadata"), Mapping) else {}
                    transport = str(metadata.get("winrm_transport") or "ntlm").strip().lower() or "ntlm"
                if not username or not password:
                    raise RuntimeError(f"WinRM workflow nodes require a credential with username and password for '{hostname}'.")
                host_vars.update(
                    {
                        "ansible_user": username,
                        "ansible_password": password,
                        "ansible_winrm_transport": transport,
                        "ansible_winrm_server_cert_validation": "ignore",
                    }
                )
                if endpoint_port and endpoint_port != 5985:
                    host_vars["ansible_port"] = endpoint_port
            target_specifications.append(
                {
                    "hostname": hostname,
                    "inventory_hostname": self._inventory_hostname(hostname, site_name=site_name, site_id=site_id),
                    "site_group": self._site_group(site_name, site_id=site_id),
                    "site_id": site_id,
                    "host_vars": host_vars,
                }
            )
        if not target_specifications:
            raise RuntimeError("No eligible Ansible targets were available for this workflow node.")
        return target_specifications, runtime_files

    def _load_credential(self, credential_id: Optional[int]) -> Optional[Dict[str, Any]]:
        if credential_id is None:
            return None
        if callable(self._credential_fetcher):
            return self._credential_fetcher(int(credential_id))
        return None

    def _prepare_vpn_sessions(
        self,
        agent_ids: Sequence[str],
        *,
        required_ports: Optional[Sequence[int]] = None,
    ) -> Dict[str, Dict[str, Any]]:
        normalized_ids = [str(agent_id or "").strip() for agent_id in agent_ids if str(agent_id or "").strip()]
        if not normalized_ids:
            return {}
        if callable(self._vpn_session_prepare):
            try:
                return self._vpn_session_prepare(normalized_ids, required_ports) or {}
            except Exception:
                self._logger.debug("Workflow VPN prepare callback failed", exc_info=True)
        if callable(self._vpn_session_lookup):
            try:
                return self._vpn_session_lookup() or {}
            except Exception:
                self._logger.debug("Workflow VPN lookup callback failed", exc_info=True)
        return {}

    def _variable_override_map(self, node: Mapping[str, Any]) -> Dict[str, Any]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        raw_value = data.get("variable_values") or data.get("variableValues") or data.get("variables_json")
        if isinstance(raw_value, Mapping):
            return {str(key): value for key, value in raw_value.items() if str(key).strip()}
        if isinstance(raw_value, str):
            parsed = _parse_json_object(raw_value)
            return {str(key): value for key, value in parsed.items() if str(key).strip()}
        return {}

    # ------------------------------------------------------------------
    # Subworkflow execution
    # ------------------------------------------------------------------
    def _execute_subworkflow_node(
        self,
        *,
        run_id: int,
        node_run_id: int,
        node: Mapping[str, Any],
        input_envelope: Mapping[str, Any],
        source_metadata: Mapping[str, Any],
    ) -> Dict[str, Any]:
        data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
        workflow_guid = str(data.get("workflow_guid") or data.get("workflowGuid") or "").strip()
        if not workflow_guid:
            output = self._build_output_envelope(
                status=WORKFLOW_STATUS_FAILED,
                data=None,
                metadata={"reason": "missing_workflow_guid"},
                artifacts={},
            )
            self._finalize_node_run(
                node_run_id,
                status=WORKFLOW_STATUS_FAILED,
                output_envelope=output,
                input_envelope=input_envelope,
                error="Execute Subworkflow node is missing a selected workflow.",
            )
            return {"status": WORKFLOW_STATUS_FAILED, "output_envelope": output}

        ancestry = [
            str(item).strip()
            for item in list(source_metadata.get("workflow_ancestry") or [])
            if str(item).strip()
        ]
        next_metadata = dict(source_metadata or {})
        next_metadata["workflow_ancestry"] = ancestry + [str(source_metadata.get("workflow_guid") or "")]
        next_metadata["parent_workflow_run_id"] = int(run_id)
        next_metadata["parent_node_id"] = str(node.get("id") or "").strip()
        child_start = self.start_run(
            workflow_guid=workflow_guid,
            source_type="subworkflow",
            source_metadata=next_metadata,
            created_by=str(source_metadata.get("created_by") or ""),
            execute_async=True,
        )
        child_run = child_start.get("run") if isinstance(child_start.get("run"), Mapping) else {}
        child_run_id = _coerce_int(child_run.get("id"), default=0)
        child_job_id = self._create_child_job(
            workflow_run_id=run_id,
            workflow_node_run_id=node_run_id,
            child_kind="workflow",
            child_workflow_run_id=child_run_id if child_run_id > 0 else None,
            component_guid=workflow_guid,
            component_name=str(child_run.get("workflow_name") or workflow_guid),
            component_kind="workflow",
            child_identifier=str(child_run_id or ""),
            status=str(child_run.get("status") or WORKFLOW_STATUS_PENDING),
            payload={"workflow_guid": workflow_guid},
        )
        child_timeout = self._node_timeout_seconds(node)
        child_result = self._wait_for_workflow_run(child_run_id, timeout_seconds=child_timeout)
        self._update_child_job(
            child_job_id,
            status=child_result.get("status") or WORKFLOW_STATUS_FAILED,
            payload=child_result,
        )
        child_status = _normalize_status(child_result.get("status"))
        exports = {}
        final_payload = child_result.get("final_payload") if isinstance(child_result.get("final_payload"), Mapping) else {}
        child_job_output = []
        if isinstance(final_payload.get("data"), Mapping):
            exports = dict(final_payload.get("data", {}).get("exports") or {})
            raw_job_output = final_payload.get("data", {}).get("job_output")
            if isinstance(raw_job_output, list):
                child_job_output = [dict(item) for item in raw_job_output if isinstance(item, Mapping)]
        output = self._build_output_envelope(
            status=child_status,
            data={
                "workflow_run_id": child_run_id,
                "final_payload": final_payload,
                "exports": exports,
                "job_output": child_job_output,
            },
            metadata={"workflow_guid": workflow_guid},
            artifacts={"child_workflow_run_id": child_run_id},
        )
        self._finalize_node_run(
            node_run_id,
            status=child_status,
            output_envelope=output,
            input_envelope=input_envelope,
            linked_child_summary={"child_workflow_run_id": child_run_id, "status": child_status},
            error=str(child_result.get("error") or ""),
        )
        return {"status": child_status, "output_envelope": output}

    def _wait_for_workflow_run(self, run_id: int, *, timeout_seconds: int) -> Dict[str, Any]:
        start = time.monotonic()
        while True:
            run = self.get_run(run_id)
            if not run:
                return {"id": run_id, "status": WORKFLOW_STATUS_FAILED, "error": "Child workflow run was not found."}
            status = _normalize_status(run.get("status"))
            if status in WORKFLOW_TERMINAL_STATUSES:
                return run
            if timeout_seconds > 0 and (time.monotonic() - start) >= timeout_seconds:
                finished_ts = _now_ts()
                self._update_run_row(
                    run_id,
                    status=WORKFLOW_STATUS_TIMED_OUT,
                    error="Child workflow run timed out.",
                    finished_ts=finished_ts,
                    updated_at=finished_ts,
                )
                return self.get_run(run_id) or {"id": run_id, "status": WORKFLOW_STATUS_TIMED_OUT}
            time.sleep(POLL_INTERVAL_SECONDS)

    # ------------------------------------------------------------------
    # Child activity helpers
    # ------------------------------------------------------------------
    def _create_activity_history_row(
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
            cur.execute(
                """
                INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                VALUES(?,?,?,?,?,?,?,?)
                """,
                (
                    hostname,
                    script_path,
                    script_name,
                    script_type,
                    _now_ts(),
                    WORKFLOW_STATUS_RUNNING,
                    "",
                    "",
                ),
            )
            activity_id = int(cur.lastrowid or 0)
            conn.commit()
            return activity_id
        finally:
            conn.close()

    def _dispatch_script_activity(
        self,
        *,
        hostname: str,
        script_name: str,
        script_path: str,
        script_type: str,
        script_bytes: bytes,
        script_content_b64: str,
        environment: Mapping[str, Any],
        variables: Sequence[Mapping[str, Any]],
        timeout_seconds: int,
        files: Sequence[Mapping[str, Any]],
        run_mode: str,
        context: Mapping[str, Any],
    ) -> Tuple[Optional[int], str]:
        if self._socketio is None:
            return None, "Realtime transport is unavailable; cannot dispatch workflow script jobs."
        activity_id = self._create_activity_history_row(
            hostname=hostname,
            script_path=script_path,
            script_name=script_name,
            script_type=script_type,
        )
        signature_b64 = ""
        signing_key_b64 = ""
        if self._script_signer is not None:
            try:
                signature_raw = self._script_signer.sign(script_bytes)
                signature_b64 = base64.b64encode(signature_raw).decode("ascii")
                signing_key_b64 = self._script_signer.public_base64_spki()
            except Exception:
                signature_b64 = ""
                signing_key_b64 = ""
        payload = {
            "job_id": activity_id,
            "target_hostname": hostname,
            "script_type": script_type,
            "script_name": script_name,
            "script_path": script_path,
            "script_content": script_content_b64,
            "script_encoding": "base64",
            "environment": dict(environment or {}),
            "variables": list(variables or []),
            "timeout_seconds": max(0, int(timeout_seconds or 0)),
            "files": list(files or []),
            "run_mode": run_mode,
            "admin_user": "",
            "admin_pass": "",
            "context": dict(context or {}),
        }
        payload.update(
            build_currentuser_dispatch_fields(
                run_mode=run_mode,
                session_target="all_active_sessions",
            )
        )
        if signature_b64:
            payload["signature"] = signature_b64
            payload["sig_alg"] = "ed25519"
        if signing_key_b64:
            payload["signing_key"] = signing_key_b64
        emitted = False
        delivery_mode = _normalize_target_service_mode(run_mode)
        if delivery_mode and callable(self._emit_host_service_event):
            try:
                emitted = bool(self._emit_host_service_event(hostname, delivery_mode, "quick_job_run", payload))
            except Exception:
                emitted = False
        if not emitted:
            if delivery_mode and callable(self._emit_host_service_event):
                error_text = (
                    f"No {delivery_mode} agent socket is registered for host {hostname}; unable to dispatch workflow script job."
                )
                self._finalize_activity_row(activity_id, status=WORKFLOW_STATUS_FAILED, stdout="", stderr=error_text)
                return activity_id, error_text
            try:
                self._socketio.emit("quick_job_run", payload)
            except Exception as exc:
                error_text = f"Failed to emit quick_job_run payload: {exc}"
                self._finalize_activity_row(activity_id, status=WORKFLOW_STATUS_FAILED, stdout="", stderr=error_text)
                return activity_id, error_text
        try:
            self._socketio.emit(
                "device_activity_changed",
                {
                    "hostname": hostname,
                    "activity_id": activity_id,
                    "change": "created",
                    "source": "workflow_run",
                },
            )
        except Exception:
            self._logger.debug("Failed to emit workflow device activity creation event", exc_info=True)
        return activity_id, ""

    def _finalize_activity_row(self, activity_id: int, *, status: str, stdout: str, stderr: str) -> None:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "UPDATE activity_history SET status=?, stdout=?, stderr=? WHERE id=?",
                (_normalize_status(status), stdout, stderr, int(activity_id)),
            )
            conn.commit()
        finally:
            conn.close()

    def _wait_for_activity_results(
        self,
        child_jobs: Sequence[Mapping[str, Any]],
        *,
        timeout_seconds: int,
    ) -> List[Dict[str, Any]]:
        pending = {
            int(item.get("activity_id")): dict(item)
            for item in child_jobs
            if _coerce_optional_int(item.get("activity_id")) is not None
        }
        results: List[Dict[str, Any]] = []
        start = time.monotonic()
        while pending:
            resolved_ids: List[int] = []
            for activity_id, item in list(pending.items()):
                activity = self._load_activity_row(activity_id)
                status = _normalize_status(activity.get("status"))
                if status in WORKFLOW_TERMINAL_STATUSES - {WORKFLOW_STATUS_SKIPPED} and status != WORKFLOW_STATUS_PENDING:
                    results.append(
                        {
                            "hostname": str(item.get("hostname") or activity.get("hostname") or ""),
                            "activity_id": activity_id,
                            "status": status,
                            "stdout": _truncate_text(activity.get("stdout")),
                            "stderr": _truncate_text(activity.get("stderr")),
                        }
                    )
                    resolved_ids.append(activity_id)
            for activity_id in resolved_ids:
                pending.pop(activity_id, None)
            if not pending:
                break
            if timeout_seconds > 0 and (time.monotonic() - start) >= timeout_seconds:
                for activity_id, item in list(pending.items()):
                    self._finalize_activity_row(
                        activity_id,
                        status=WORKFLOW_STATUS_TIMED_OUT,
                        stdout="",
                        stderr="Workflow node timed out while waiting for activity completion.",
                    )
                    activity = self._load_activity_row(activity_id)
                    results.append(
                        {
                            "hostname": str(item.get("hostname") or activity.get("hostname") or ""),
                            "activity_id": activity_id,
                            "status": WORKFLOW_STATUS_TIMED_OUT,
                            "stdout": _truncate_text(activity.get("stdout")),
                            "stderr": _truncate_text(activity.get("stderr")),
                        }
                    )
                    pending.pop(activity_id, None)
                break
            time.sleep(POLL_INTERVAL_SECONDS)
        return results

    def _load_activity_row(self, activity_id: int) -> Dict[str, Any]:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, hostname, status, stdout, stderr
                  FROM activity_history
                 WHERE id=?
                """,
                (int(activity_id),),
            )
            row = cur.fetchone()
            if not row:
                return {"id": int(activity_id), "hostname": "", "status": WORKFLOW_STATUS_FAILED, "stdout": "", "stderr": ""}
            return {
                "id": int(row[0]),
                "hostname": row[1] or "",
                "status": row[2] or "",
                "stdout": row[3] or "",
                "stderr": row[4] or "",
            }
        finally:
            conn.close()

    def _create_child_job(
        self,
        *,
        workflow_run_id: int,
        workflow_node_run_id: int,
        child_kind: str,
        child_identifier: str = "",
        activity_id: Optional[int] = None,
        child_workflow_run_id: Optional[int] = None,
        target_hostname: str = "",
        component_guid: str = "",
        component_name: str = "",
        component_kind: str = "",
        status: str = WORKFLOW_STATUS_PENDING,
        payload: Optional[Mapping[str, Any]] = None,
    ) -> int:
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO workflow_child_jobs(
                    workflow_run_id,
                    workflow_node_run_id,
                    child_kind,
                    child_identifier,
                    activity_id,
                    child_workflow_run_id,
                    target_hostname,
                    component_guid,
                    component_name,
                    component_kind,
                    status,
                    stdout_summary,
                    stderr_summary,
                    payload_json,
                    created_at,
                    updated_at
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    int(workflow_run_id),
                    int(workflow_node_run_id),
                    child_kind,
                    child_identifier or None,
                    activity_id,
                    child_workflow_run_id,
                    target_hostname or None,
                    component_guid or None,
                    component_name or None,
                    component_kind or None,
                    _normalize_status(status),
                    "",
                    "",
                    _json_dumps(dict(payload or {})),
                    now,
                    now,
                ),
            )
            child_job_id = int(cur.lastrowid or 0)
            conn.commit()
            return child_job_id
        finally:
            conn.close()

    def _update_child_job(
        self,
        child_job_id: int,
        *,
        status: str,
        stdout_summary: Any = None,
        stderr_summary: Any = None,
        payload: Optional[Mapping[str, Any]] = None,
    ) -> None:
        fields = {
            "status": _normalize_status(status),
            "updated_at": _now_ts(),
        }
        if stdout_summary is not None:
            fields["stdout_summary"] = _truncate_text(stdout_summary)
        if stderr_summary is not None:
            fields["stderr_summary"] = _truncate_text(stderr_summary)
        if payload is not None:
            fields["payload_json"] = _json_dumps(dict(payload))
        conn = self._conn()
        try:
            cur = conn.cursor()
            sets = ", ".join(f"{key}=?" for key in fields.keys())
            cur.execute(f"UPDATE workflow_child_jobs SET {sets} WHERE id=?", (*fields.values(), int(child_job_id)))
            conn.commit()
        finally:
            conn.close()

    def _rollup_child_results(
        self,
        results: Sequence[Mapping[str, Any]],
        *,
        skipped_targets: Sequence[Mapping[str, Any]],
    ) -> str:
        statuses = [_normalize_status(result.get("status")) for result in results]
        if not statuses:
            return WORKFLOW_STATUS_WARNING if skipped_targets else WORKFLOW_STATUS_SKIPPED
        if any(status in {WORKFLOW_STATUS_FAILED, WORKFLOW_STATUS_TIMED_OUT} for status in statuses):
            return WORKFLOW_STATUS_FAILED
        if skipped_targets or any(str(result.get("stderr") or "").strip() for result in results):
            return WORKFLOW_STATUS_WARNING
        return WORKFLOW_STATUS_SUCCESS

    # ------------------------------------------------------------------
    # Scheduled job mirroring
    # ------------------------------------------------------------------
    def _mirror_scheduled_job_run(
        self,
        *,
        workflow_run_id: int,
        source_metadata: Mapping[str, Any],
        status: str,
        error: str = "",
    ) -> None:
        scheduled_job_run_id = _coerce_optional_int(source_metadata.get("scheduled_job_run_id"))
        if scheduled_job_run_id is None:
            return
        now = _now_ts()
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       started_ts=COALESCE(started_ts, ?),
                       finished_ts=?,
                       updated_at=?,
                       error=?,
                       workflow_run_id=?
                 WHERE id=?
                """,
                (
                    _normalize_status(status),
                    now,
                    now if _normalize_status(status) in WORKFLOW_TERMINAL_STATUSES else None,
                    now,
                    _truncate_text(error, limit=512),
                    int(workflow_run_id),
                    int(scheduled_job_run_id),
                ),
            )
            conn.commit()
        except Exception:
            self._logger.debug("Failed to mirror scheduled workflow status", exc_info=True)
        finally:
            conn.close()

    # ------------------------------------------------------------------
    # Representation helpers
    # ------------------------------------------------------------------
    def _row_to_workflow_run(
        self,
        row: Sequence[Any],
        *,
        graph_snapshot_json: Optional[str] = None,
    ) -> Dict[str, Any]:
        payload = {
            "id": int(row[0]),
            "workflow_guid": row[1] or "",
            "workflow_name": row[2] or "",
            "source_type": row[3] or "",
            "source_metadata": _parse_json_object(row[4]),
            "status": row[5] or "",
            "error": row[6] or "",
            "skip_reason": row[7] or "",
            "final_payload": _parse_json_object(row[8]),
            "final_metadata": _parse_json_object(row[9]),
            "parent_workflow_run_id": row[10],
            "parent_node_id": row[11] or "",
            "scheduled_job_id": row[12],
            "scheduled_job_run_id": row[13],
            "webhook_id": row[14],
            "created_by": row[15] or "",
            "created_at": row[16] or 0,
            "started_ts": row[17],
            "finished_ts": row[18],
            "updated_at": row[19] or 0,
        }
        graph_text = graph_snapshot_json
        if graph_text is None:
            conn = self._conn()
            try:
                cur = conn.cursor()
                cur.execute("SELECT graph_snapshot_json FROM workflow_runs WHERE id=?", (int(payload["id"]),))
                snapshot_row = cur.fetchone()
                graph_text = snapshot_row[0] if snapshot_row else "{}"
            finally:
                conn.close()
        payload["graph_snapshot"] = _parse_json_object(graph_text)
        return payload

    def _row_to_node_run(self, row: Sequence[Any]) -> Dict[str, Any]:
        return {
            "id": int(row[0]),
            "workflow_run_id": int(row[1]),
            "node_id": row[2] or "",
            "node_type": row[3] or "",
            "node_label": row[4] or "",
            "node_snapshot": _parse_json_object(row[5]),
            "status": row[6] or "",
            "skip_reason": row[7] or "",
            "error": row[8] or "",
            "timeout_seconds": row[9] or 0,
            "input_envelope": _parse_json_object(row[10]),
            "output_envelope": _parse_json_object(row[11]),
            "ignored_inputs": _parse_json_array(row[12]),
            "linked_child_summary": _parse_json_object(row[13]),
            "created_at": row[14] or 0,
            "started_ts": row[15],
            "finished_ts": row[16],
            "updated_at": row[17] or 0,
        }

    def _row_to_webhook(self, row: Sequence[Any]) -> Dict[str, Any]:
        return {
            "id": int(row[0]),
            "workflow_guid": row[1] or "",
            "opaque_token": row[2] or "",
            "created_at": row[3] or 0,
            "creator_username": row[4] or "",
            "creator_role": row[5] or "",
            "last_used_at": row[6],
        }

    def _list_node_runs_for_run(
        self,
        run_id: int,
        *,
        conn: Optional[sqlite3.Connection] = None,
    ) -> List[Dict[str, Any]]:
        owns_conn = conn is None
        conn = conn or self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    workflow_run_id,
                    node_id,
                    node_type,
                    node_label,
                    node_snapshot_json,
                    status,
                    skip_reason,
                    error,
                    timeout_seconds,
                    input_envelope_json,
                    output_envelope_json,
                    ignored_inputs_json,
                    linked_child_summary_json,
                    created_at,
                    started_ts,
                    finished_ts,
                    updated_at
                  FROM workflow_node_runs
                 WHERE workflow_run_id=?
              ORDER BY id ASC
                """,
                (int(run_id),),
            )
            return [self._row_to_node_run(row) for row in cur.fetchall()]
        finally:
            if owns_conn:
                conn.close()

    def _list_child_jobs_for_node_run(
        self,
        node_run_id: int,
        *,
        conn: Optional[sqlite3.Connection] = None,
    ) -> List[Dict[str, Any]]:
        owns_conn = conn is None
        conn = conn or self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    workflow_run_id,
                    workflow_node_run_id,
                    child_kind,
                    child_identifier,
                    activity_id,
                    child_workflow_run_id,
                    target_hostname,
                    component_guid,
                    component_name,
                    component_kind,
                    status,
                    stdout_summary,
                    stderr_summary,
                    payload_json,
                    created_at,
                    updated_at
                  FROM workflow_child_jobs
                 WHERE workflow_node_run_id=?
              ORDER BY id ASC
                """,
                (int(node_run_id),),
            )
            return [
                {
                    "id": int(row[0]),
                    "workflow_run_id": int(row[1]),
                    "workflow_node_run_id": int(row[2]),
                    "child_kind": row[3] or "",
                    "child_identifier": row[4] or "",
                    "activity_id": row[5],
                    "child_workflow_run_id": row[6],
                    "target_hostname": row[7] or "",
                    "component_guid": row[8] or "",
                    "component_name": row[9] or "",
                    "component_kind": row[10] or "",
                    "status": row[11] or "",
                    "stdout_summary": row[12] or "",
                    "stderr_summary": row[13] or "",
                    "payload": _parse_json_object(row[14]),
                    "created_at": row[15] or 0,
                    "updated_at": row[16] or 0,
                }
                for row in cur.fetchall()
            ]
        finally:
            if owns_conn:
                conn.close()

    # ------------------------------------------------------------------
    # Inventory helpers
    # ------------------------------------------------------------------
    def _online_hostnames_snapshot(self) -> List[str]:
        if callable(self._online_lookup):
            try:
                return [str(host) for host in (self._online_lookup() or []) if str(host).strip()]
            except Exception:
                self._logger.debug("Workflow online lookup callback failed", exc_info=True)
        threshold = _now_ts() - REMOTE_ONLINE_LOOKBACK_SECONDS
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT hostname FROM devices WHERE last_seen IS NOT NULL AND last_seen >= ?",
                (threshold,),
            )
            rows = cur.fetchall()
            return [str(row[0]).strip() for row in rows if row and str(row[0]).strip()]
        finally:
            conn.close()

    def _site_group(self, site_name: str, *, site_id: Optional[int]) -> str:
        if site_name:
            return "site_" + "".join(ch.lower() if ch.isalnum() else "_" for ch in site_name).strip("_")
        if site_id is not None:
            return f"site_{int(site_id)}"
        return "site_unassigned"

    def _inventory_hostname(self, hostname: str, *, site_name: str, site_id: Optional[int]) -> str:
        base = "".join(ch if ch.isalnum() or ch in {"-", "_", "."} else "-" for ch in str(hostname or ""))
        base = base.strip(".-") or "host"
        if site_name:
            prefix = "".join(ch.lower() if ch.isalnum() else "_" for ch in site_name).strip("_")
            if prefix:
                return f"{prefix}__{base}"
        if site_id is not None:
            return f"site_{int(site_id)}__{base}"
        return f"unassigned__{base}"
