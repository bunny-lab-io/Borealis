"""Postgres-backed queue and worker state for job-scheduler/site-workers."""

from __future__ import annotations

import json
import time
from typing import Any, Dict, List, Mapping, Optional, Sequence

from Data.Engine.db import dbapi as sqlite3

WORK_STATUS_QUEUED = "queued"
WORK_STATUS_RUNNING = "running"
WORK_STATUS_SUCCEEDED = "succeeded"
WORK_STATUS_FAILED = "failed"
WORK_STATUS_CANCELLED = "cancelled"

WORKER_STATUS_STARTING = "starting"
WORKER_STATUS_RUNNING = "running"
WORKER_STATUS_IDLE = "idle"
WORKER_STATUS_STOPPED = "stopped"
WORKER_STATUS_LOST = "lost"
WORKER_ACTIVE_STATUSES = (
    WORKER_STATUS_STARTING,
    WORKER_STATUS_RUNNING,
    WORKER_STATUS_IDLE,
)
WORKER_TERMINAL_STATUSES = (
    WORKER_STATUS_STOPPED,
    WORKER_STATUS_LOST,
)

LANE_ONBOARDING = "onboarding"
LANE_SCHEDULED_JOB = "scheduled_job"
LANE_SERVICE_ACTION = "service_action"

WORK_KIND_ONBOARDING_RUN = "onboarding_run"
WORK_KIND_SCHEDULED_RUN = "scheduled_run"
WORK_KIND_SCHEDULED_WORKFLOW_RUN = "scheduled_workflow_run"


def _now_ts() -> int:
    return int(time.time())


def _json_dumps(value: Any) -> str:
    try:
        return json.dumps(value if value is not None else {}, separators=(",", ":"), sort_keys=True)
    except Exception:
        return "{}"


def _json_loads(value: Any, default: Any) -> Any:
    try:
        parsed = json.loads(str(value or ""))
    except Exception:
        return default
    return parsed if parsed is not None else default


def _positive_ints(values: Sequence[Any]) -> List[int]:
    results: List[int] = []
    for value in values or []:
        try:
            parsed = int(value)
        except Exception:
            continue
        if parsed > 0:
            results.append(parsed)
    return results


def _payload_target_count(payload: Mapping[str, Any]) -> Optional[int]:
    for key in ("target_row_ids", "targets"):
        values = payload.get(key)
        if isinstance(values, Sequence) and not isinstance(values, (str, bytes, bytearray)):
            return len([value for value in values if value is not None])
    return None


def _payload_task_type(kind: str, payload: Mapping[str, Any]) -> str:
    normalized_kind = str(kind or "").strip().lower()
    if normalized_kind == WORK_KIND_ONBOARDING_RUN:
        return "Onboarding"
    if normalized_kind == WORK_KIND_SCHEDULED_WORKFLOW_RUN:
        return "Workflow"
    if normalized_kind == WORK_KIND_SCHEDULED_RUN:
        script_components = payload.get("script_components")
        ansible_components = payload.get("ansible_components")
        has_scripts = isinstance(script_components, Sequence) and not isinstance(script_components, (str, bytes, bytearray)) and len(script_components) > 0
        has_ansible = isinstance(ansible_components, Sequence) and not isinstance(ansible_components, (str, bytes, bytearray)) and len(ansible_components) > 0
        if has_ansible and not has_scripts:
            return "Playbook"
        return "Assembly"
    return ""


def ensure_job_scheduler_tables(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS job_scheduler_work_items (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            dedupe_key TEXT,
            kind TEXT NOT NULL,
            site_id INTEGER,
            lane TEXT NOT NULL,
            job_id INTEGER,
            run_id INTEGER,
            target_id INTEGER,
            payload_json TEXT NOT NULL,
            status TEXT NOT NULL,
            attempt_count INTEGER NOT NULL DEFAULT 0,
            priority INTEGER NOT NULL DEFAULT 0,
            available_at INTEGER NOT NULL,
            lease_owner TEXT,
            lease_expires_at INTEGER,
            heartbeat_at INTEGER,
            worker_guid TEXT,
            container_name TEXT,
            error TEXT,
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL,
            started_at INTEGER,
            finished_at INTEGER
        )
        """
    )
    cur.execute("PRAGMA table_info(job_scheduler_work_items)")
    work_cols = {str(row[1]) for row in cur.fetchall() or [] if len(row) > 1}
    for column_name, sql in (
        ("dedupe_key", "ALTER TABLE job_scheduler_work_items ADD COLUMN dedupe_key TEXT"),
        ("lease_owner", "ALTER TABLE job_scheduler_work_items ADD COLUMN lease_owner TEXT"),
        ("lease_expires_at", "ALTER TABLE job_scheduler_work_items ADD COLUMN lease_expires_at INTEGER"),
        ("heartbeat_at", "ALTER TABLE job_scheduler_work_items ADD COLUMN heartbeat_at INTEGER"),
        ("worker_guid", "ALTER TABLE job_scheduler_work_items ADD COLUMN worker_guid TEXT"),
        ("container_name", "ALTER TABLE job_scheduler_work_items ADD COLUMN container_name TEXT"),
        ("error", "ALTER TABLE job_scheduler_work_items ADD COLUMN error TEXT"),
        ("started_at", "ALTER TABLE job_scheduler_work_items ADD COLUMN started_at INTEGER"),
        ("finished_at", "ALTER TABLE job_scheduler_work_items ADD COLUMN finished_at INTEGER"),
    ):
        if column_name not in work_cols:
            cur.execute(sql)
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS job_scheduler_workers (
            worker_guid TEXT PRIMARY KEY,
            container_name TEXT NOT NULL,
            site_id INTEGER,
            status TEXT NOT NULL,
            started_at INTEGER NOT NULL,
            last_seen_at INTEGER NOT NULL,
            idle_since INTEGER,
            stopped_at INTEGER,
            current_lanes_json TEXT,
            claimed_count INTEGER NOT NULL DEFAULT 0,
            task_links_json TEXT,
            docker_state TEXT,
            exit_code INTEGER,
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL
        )
        """
    )
    cur.execute("CREATE UNIQUE INDEX IF NOT EXISTS idx_job_scheduler_work_dedupe ON job_scheduler_work_items(dedupe_key)")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_job_scheduler_work_claim ON job_scheduler_work_items(site_id, lane, status, available_at, priority)")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_job_scheduler_work_lease ON job_scheduler_work_items(status, lease_expires_at)")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_job_scheduler_workers_site ON job_scheduler_workers(site_id, status, last_seen_at)")
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS job_scheduler_service_snapshots (
            service_key TEXT PRIMARY KEY,
            payload_json TEXT NOT NULL,
            updated_at INTEGER NOT NULL
        )
        """
    )


def _insert_work_item(
    conn: sqlite3.Connection,
    *,
    dedupe_key: str,
    kind: str,
    site_id: Optional[int],
    lane: str,
    job_id: Optional[int],
    run_id: Optional[int],
    target_id: Optional[int],
    payload: Mapping[str, Any],
    priority: int = 0,
) -> int:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    if dedupe_key:
        cur.execute(
            "SELECT id FROM job_scheduler_work_items WHERE dedupe_key=?",
            (dedupe_key,),
        )
        row = cur.fetchone()
        if row:
            cur.execute(
                "UPDATE job_scheduler_work_items SET updated_at=? WHERE id=?",
                (now, int(row[0])),
            )
            return int(row[0])
    cur.execute(
        """
        INSERT INTO job_scheduler_work_items(
            dedupe_key, kind, site_id, lane, job_id, run_id, target_id, payload_json,
            status, attempt_count, priority, available_at, created_at, updated_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        """,
        (
            dedupe_key or None,
            str(kind or "").strip(),
            site_id,
            str(lane or "").strip(),
            job_id,
            run_id,
            target_id,
            _json_dumps(dict(payload or {})),
            WORK_STATUS_QUEUED,
            0,
            int(priority or 0),
            now,
            now,
            now,
        ),
    )
    return int(cur.lastrowid or 0)


def enqueue_onboarding_run(
    conn: sqlite3.Connection,
    *,
    job_id: int,
    run_id: int,
    scheduled_ts: int,
    site_id: Optional[int],
    components: Sequence[Any],
    targets: Sequence[Any],
    credential_id: Optional[int],
) -> int:
    payload = {
        "job_id": int(job_id),
        "run_id": int(run_id),
        "scheduled_ts": int(scheduled_ts),
        "components": list(components or []),
        "targets": list(targets or []),
        "credential_id": credential_id,
    }
    work_id = _insert_work_item(
        conn,
        dedupe_key=f"onboarding:{int(run_id)}",
        kind=WORK_KIND_ONBOARDING_RUN,
        site_id=site_id,
        lane=LANE_ONBOARDING,
        job_id=int(job_id),
        run_id=int(run_id),
        target_id=None,
        payload=payload,
        priority=50,
    )
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE scheduled_job_runs
           SET status=?,
               started_ts=COALESCE(started_ts, ?),
               updated_at=?
         WHERE id=?
        """,
        ("Running", now, now, int(run_id)),
    )
    return work_id


def enqueue_scheduled_run(
    conn: sqlite3.Connection,
    *,
    job_id: int,
    run_id: int,
    scheduled_ts: int,
    site_id: Optional[int],
    run_mode: str,
    script_components: Sequence[Any],
    ansible_components: Sequence[Any],
    credential_id: Optional[int],
    use_service_account: bool = False,
    shared_execution: bool = False,
    component_index: Optional[int] = None,
    target_row_ids: Optional[Sequence[int]] = None,
    task_link: Optional[Mapping[str, Any]] = None,
) -> int:
    payload = {
        "job_id": int(job_id),
        "run_id": int(run_id),
        "scheduled_ts": int(scheduled_ts),
        "run_mode": str(run_mode or "system").strip().lower() or "system",
        "script_components": list(script_components or []),
        "ansible_components": list(ansible_components or []),
        "credential_id": credential_id,
        "use_service_account": bool(use_service_account),
        "shared_execution": bool(shared_execution),
        "component_index": component_index,
        "target_row_ids": _positive_ints(target_row_ids or []),
        "task_link": dict(task_link or {}),
    }
    target_suffix = ",".join(str(value) for value in payload["target_row_ids"]) or "all"
    return _insert_work_item(
        conn,
        dedupe_key=f"scheduled-run:{int(run_id)}:{target_suffix}",
        kind=WORK_KIND_SCHEDULED_RUN,
        site_id=site_id,
        lane=LANE_SCHEDULED_JOB,
        job_id=int(job_id),
        run_id=int(run_id),
        target_id=None,
        payload=payload,
        priority=40,
    )


def enqueue_scheduled_workflow_run(
    conn: sqlite3.Connection,
    *,
    job_id: int,
    run_id: int,
    scheduled_ts: int,
    site_id: Optional[int],
    workflow_component: Mapping[str, Any],
    task_link: Optional[Mapping[str, Any]] = None,
) -> int:
    payload = {
        "job_id": int(job_id),
        "run_id": int(run_id),
        "scheduled_ts": int(scheduled_ts),
        "workflow_component": dict(workflow_component or {}),
        "workflow_site_scope": {"site_id": site_id},
        "task_link": dict(task_link or {}),
    }
    site_suffix = int(site_id or 0)
    return _insert_work_item(
        conn,
        dedupe_key=f"scheduled-workflow:{int(run_id)}:{site_suffix}",
        kind=WORK_KIND_SCHEDULED_WORKFLOW_RUN,
        site_id=site_id,
        lane=LANE_SCHEDULED_JOB,
        job_id=int(job_id),
        run_id=int(run_id),
        target_id=None,
        payload=payload,
        priority=40,
    )


def enqueue_service_action(
    conn: sqlite3.Connection,
    *,
    service_key: str,
    action: Mapping[str, Any],
) -> int:
    now = _now_ts()
    normalized_service = str(service_key or "").strip().lower()
    action_name = str(action.get("action") or "").strip().lower()
    action_mode = str(action.get("mode") or "").strip().lower()
    dedupe = f"service-action:{normalized_service}:{action_name}:{action_mode}:{now // 60}"
    return _insert_work_item(
        conn,
        dedupe_key=dedupe,
        kind="service_action",
        site_id=0,
        lane=LANE_SERVICE_ACTION,
        job_id=None,
        run_id=None,
        target_id=None,
        payload={"service_key": normalized_service, "action": dict(action or {})},
        priority=100,
    )


def claim_next_work_item(
    conn: sqlite3.Connection,
    *,
    site_id: int,
    lanes: Sequence[str],
    lease_owner: str,
    lease_seconds: int = 300,
) -> Optional[Dict[str, Any]]:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    lanes_norm = [str(lane or "").strip() for lane in lanes if str(lane or "").strip()]
    if not lanes_norm:
        return None
    placeholders = ",".join("?" for _ in lanes_norm)
    select_sql = f"""
        SELECT id
          FROM job_scheduler_work_items
         WHERE site_id=?
           AND lane IN ({placeholders})
           AND status=?
           AND available_at<=?
         ORDER BY priority DESC, available_at ASC, id ASC
         LIMIT 1
         FOR UPDATE SKIP LOCKED
    """
    cur = conn.cursor()
    try:
        cur.execute("BEGIN")
        cur.execute(select_sql, [int(site_id), *lanes_norm, WORK_STATUS_QUEUED, now])
    except Exception:
        try:
            conn.rollback()
        except Exception:
            pass
        cur = conn.cursor()
        cur.execute("BEGIN")
        cur.execute(
            f"""
            SELECT id
              FROM job_scheduler_work_items
             WHERE site_id=?
               AND lane IN ({placeholders})
               AND status=?
               AND available_at<=?
             ORDER BY priority DESC, available_at ASC, id ASC
             LIMIT 1
            """,
            [int(site_id), *lanes_norm, WORK_STATUS_QUEUED, now],
        )
    row = cur.fetchone()
    if not row:
        conn.commit()
        return None
    work_id = int(row[0])
    lease_expires = now + max(30, int(lease_seconds or 300))
    cur.execute(
        """
        UPDATE job_scheduler_work_items
           SET status=?,
               lease_owner=?,
               lease_expires_at=?,
               heartbeat_at=?,
               worker_guid=?,
               container_name=?,
               attempt_count=attempt_count + 1,
               started_at=COALESCE(started_at, ?),
               updated_at=?
         WHERE id=?
        """,
        (
            WORK_STATUS_RUNNING,
            lease_owner,
            lease_expires,
            now,
            lease_owner,
            None,
            now,
            now,
            work_id,
        ),
    )
    cur.execute(
        """
        SELECT id, kind, site_id, lane, job_id, run_id, target_id, payload_json, status, attempt_count
          FROM job_scheduler_work_items
         WHERE id=?
        """,
        (work_id,),
    )
    claimed = cur.fetchone()
    conn.commit()
    if not claimed:
        return None
    return {
        "id": int(claimed[0]),
        "kind": claimed[1] or "",
        "site_id": claimed[2],
        "lane": claimed[3] or "",
        "job_id": claimed[4],
        "run_id": claimed[5],
        "target_id": claimed[6],
        "payload": _json_loads(claimed[7], {}),
        "status": claimed[8] or "",
        "attempt_count": int(claimed[9] or 0),
    }


def complete_work_item(conn: sqlite3.Connection, *, work_id: int, status: str, error: str = "") -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    normalized = status if status in {WORK_STATUS_SUCCEEDED, WORK_STATUS_FAILED, WORK_STATUS_CANCELLED} else WORK_STATUS_FAILED
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_work_items
           SET status=?,
               lease_expires_at=NULL,
               heartbeat_at=?,
               error=?,
               finished_at=?,
               updated_at=?
         WHERE id=?
        """,
        (normalized, now, str(error or "")[:2000], now, now, int(work_id)),
    )


def heartbeat_work_item(conn: sqlite3.Connection, *, work_id: int, lease_owner: str, lease_seconds: int = 300) -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_work_items
           SET heartbeat_at=?,
               lease_expires_at=?,
               updated_at=?
         WHERE id=? AND lease_owner=?
        """,
        (now, now + max(30, int(lease_seconds or 300)), now, int(work_id), str(lease_owner or "")),
    )


def expire_stale_leases(conn: sqlite3.Connection) -> int:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_work_items
           SET status=?,
               lease_owner=NULL,
               lease_expires_at=NULL,
               updated_at=?
         WHERE status=?
           AND lease_expires_at IS NOT NULL
           AND lease_expires_at<?
        """,
        (WORK_STATUS_QUEUED, now, WORK_STATUS_RUNNING, now),
    )
    return max(0, int(getattr(cur, "rowcount", 0) or 0))


def queued_site_ids(conn: sqlite3.Connection) -> List[int]:
    ensure_job_scheduler_tables(conn)
    cur = conn.cursor()
    cur.execute(
        """
        SELECT DISTINCT site_id
          FROM job_scheduler_work_items
         WHERE status=?
           AND site_id IS NOT NULL
           AND site_id>0
         ORDER BY site_id ASC
        """,
        (WORK_STATUS_QUEUED,),
    )
    return [int(row[0]) for row in cur.fetchall() or [] if row and row[0] is not None]


def claim_service_action(conn: sqlite3.Connection, *, lease_owner: str, lease_seconds: int = 300) -> Optional[Dict[str, Any]]:
    return claim_next_work_item(conn, site_id=0, lanes=[LANE_SERVICE_ACTION], lease_owner=lease_owner, lease_seconds=lease_seconds)


def register_worker(
    conn: sqlite3.Connection,
    *,
    worker_guid: str,
    container_name: str,
    site_id: int,
    status: str = WORKER_STATUS_STARTING,
) -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        INSERT INTO job_scheduler_workers(
            worker_guid, container_name, site_id, status, started_at, last_seen_at,
            idle_since, stopped_at, current_lanes_json, claimed_count, task_links_json,
            docker_state, exit_code, created_at, updated_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT (worker_guid) DO UPDATE SET
            container_name=EXCLUDED.container_name,
            site_id=EXCLUDED.site_id,
            status=EXCLUDED.status,
            last_seen_at=EXCLUDED.last_seen_at,
            stopped_at=NULL,
            updated_at=EXCLUDED.updated_at
        """,
        (
            str(worker_guid or ""),
            str(container_name or ""),
            int(site_id),
            str(status or WORKER_STATUS_STARTING),
            now,
            now,
            None,
            None,
            "[]",
            0,
            "[]",
            "",
            None,
            now,
            now,
        ),
    )


def heartbeat_worker(
    conn: sqlite3.Connection,
    *,
    worker_guid: str,
    status: str,
    lanes: Sequence[str] = (),
    task_links: Sequence[Mapping[str, Any]] = (),
    idle_since: Optional[int] = None,
    claimed_count: Optional[int] = None,
) -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_workers
           SET status=?,
               last_seen_at=?,
               idle_since=?,
               current_lanes_json=?,
               task_links_json=?,
               claimed_count=COALESCE(?, claimed_count),
               updated_at=?
         WHERE worker_guid=?
        """,
        (
            str(status or WORKER_STATUS_RUNNING),
            now,
            idle_since,
            _json_dumps(list(lanes or [])),
            _json_dumps(list(task_links or [])),
            claimed_count,
            now,
            str(worker_guid or ""),
        ),
    )


def stop_worker(conn: sqlite3.Connection, *, worker_guid: str, status: str = WORKER_STATUS_STOPPED) -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_workers
           SET status=?,
               stopped_at=?,
               last_seen_at=?,
               updated_at=?
         WHERE worker_guid=?
        """,
        (str(status or WORKER_STATUS_STOPPED), now, now, now, str(worker_guid or "")),
    )


def update_worker_docker_state(
    conn: sqlite3.Connection,
    *,
    worker_guid: str,
    docker_state: str,
    exit_code: Optional[int] = None,
) -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_workers
           SET docker_state=?,
               exit_code=?,
               updated_at=?
         WHERE worker_guid=?
        """,
        (str(docker_state or ""), exit_code, now, str(worker_guid or "")),
    )


def active_worker_for_site(conn: sqlite3.Connection, *, site_id: int, stale_after_seconds: int = 180) -> Optional[Dict[str, Any]]:
    ensure_job_scheduler_tables(conn)
    cutoff = _now_ts() - max(30, int(stale_after_seconds or 180))
    cur = conn.cursor()
    cur.execute(
        """
        SELECT worker_guid, container_name, site_id, status, started_at, last_seen_at
          FROM job_scheduler_workers
         WHERE site_id=?
           AND status IN (?,?,?)
           AND last_seen_at>=?
         ORDER BY last_seen_at DESC
         LIMIT 1
        """,
        (int(site_id), *WORKER_ACTIVE_STATUSES, cutoff),
    )
    row = cur.fetchone()
    if not row:
        return None
    return {
        "worker_guid": row[0] or "",
        "container_name": row[1] or "",
        "site_id": row[2],
        "status": row[3] or "",
        "started_at": row[4],
        "last_seen_at": row[5],
    }


def mark_lost_workers(conn: sqlite3.Connection, *, stale_after_seconds: int = 300) -> int:
    ensure_job_scheduler_tables(conn)
    cutoff = _now_ts() - max(60, int(stale_after_seconds or 300))
    now = _now_ts()
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_workers
           SET status=?,
               stopped_at=COALESCE(stopped_at, ?),
               updated_at=?
         WHERE status IN (?,?,?)
           AND last_seen_at<?
        """,
        (WORKER_STATUS_LOST, now, now, *WORKER_ACTIVE_STATUSES, cutoff),
    )
    return max(0, int(getattr(cur, "rowcount", 0) or 0))


def mark_missing_workers_lost(conn: sqlite3.Connection, *, live_worker_guids: Sequence[str]) -> int:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    live = [str(item or "").strip() for item in live_worker_guids or [] if str(item or "").strip()]
    cur = conn.cursor()
    base_sql = """
        UPDATE job_scheduler_workers
           SET status=?,
               docker_state=?,
               stopped_at=COALESCE(stopped_at, ?),
               updated_at=?
         WHERE status IN (?,?,?)
           AND COALESCE(site_id, 0)>0
    """
    params: List[Any] = [WORKER_STATUS_LOST, "missing", now, now, *WORKER_ACTIVE_STATUSES]
    if live:
        placeholders = ",".join("?" for _ in live)
        base_sql += f" AND worker_guid NOT IN ({placeholders})"
        params.extend(live)
    cur.execute(base_sql, tuple(params))
    return max(0, int(getattr(cur, "rowcount", 0) or 0))


def prune_worker_history(conn: sqlite3.Connection, *, retention_seconds: int = 60) -> int:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cutoff = now - max(0, int(retention_seconds or 60))
    cur = conn.cursor()
    cur.execute(
        """
        UPDATE job_scheduler_workers
           SET stopped_at=COALESCE(stopped_at, last_seen_at, updated_at, ?),
               updated_at=?
         WHERE status IN (?, ?)
           AND stopped_at IS NULL
        """,
        (now, now, *WORKER_TERMINAL_STATUSES),
    )
    cur.execute(
        """
        DELETE FROM job_scheduler_workers
         WHERE COALESCE(site_id, 0)>0
           AND status IN (?, ?)
           AND COALESCE(stopped_at, last_seen_at, updated_at, started_at, created_at, 0) < ?
        """,
        (*WORKER_TERMINAL_STATUSES, cutoff),
    )
    return max(0, int(getattr(cur, "rowcount", 0) or 0))


def list_worker_snapshots(conn: sqlite3.Connection, *, include_stopped_since: int = 86400) -> List[Dict[str, Any]]:
    ensure_job_scheduler_tables(conn)
    cutoff = _now_ts() - max(0, int(include_stopped_since or 0))
    cur = conn.cursor()
    cur.execute(
        """
        SELECT
            worker_guid, container_name, site_id, status, started_at, last_seen_at,
            idle_since, stopped_at, current_lanes_json, claimed_count, task_links_json,
            docker_state, exit_code
          FROM job_scheduler_workers
         WHERE status NOT IN (?, ?)
            OR COALESCE(stopped_at, last_seen_at, updated_at, started_at, 0)>=?
         ORDER BY COALESCE(stopped_at, last_seen_at, started_at) DESC
        """,
        (*WORKER_TERMINAL_STATUSES, cutoff),
    )
    rows = []
    for row in cur.fetchall() or []:
        rows.append(
            {
                "worker_guid": row[0] or "",
                "container_name": row[1] or "",
                "site_id": row[2],
                "status": row[3] or "",
                "started_at": row[4],
                "last_seen_at": row[5],
                "idle_since": row[6],
                "stopped_at": row[7],
                "current_lanes": _json_loads(row[8], []),
                "claimed_count": int(row[9] or 0),
                "task_links": _json_loads(row[10], []),
                "docker_state": row[11] or "",
                "exit_code": row[12],
            }
        )
    return rows


def list_active_work_items(conn: sqlite3.Connection) -> List[Dict[str, Any]]:
    ensure_job_scheduler_tables(conn)
    cur = conn.cursor()
    cur.execute(
        """
        SELECT id, kind, site_id, lane, job_id, run_id, target_id, status, lease_owner,
               worker_guid, container_name, attempt_count, heartbeat_at, started_at,
               payload_json
          FROM job_scheduler_work_items
         WHERE status=?
      ORDER BY started_at DESC, id DESC
        """,
        (WORK_STATUS_RUNNING,),
    )
    rows = []
    for row in cur.fetchall() or []:
        payload = _json_loads(row[14], {})
        payload_map = payload if isinstance(payload, Mapping) else {}
        rows.append(
            {
                "id": int(row[0]),
                "kind": row[1] or "",
                "site_id": row[2],
                "lane": row[3] or "",
                "job_id": row[4],
                "run_id": row[5],
                "target_id": row[6],
                "status": row[7] or "",
                "lease_owner": row[8] or "",
                "worker_guid": row[9] or "",
                "container_name": row[10] or "",
                "attempt_count": int(row[11] or 0),
                "heartbeat_at": row[12],
                "started_at": row[13],
                "target_count": _payload_target_count(payload_map),
                "task_type": _payload_task_type(row[1] or "", payload_map),
            }
        )
    return rows


def list_recent_work_items(conn: sqlite3.Connection, *, history_seconds: int = 600) -> List[Dict[str, Any]]:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cutoff = now - max(0, int(history_seconds or 600))
    cur = conn.cursor()
    cur.execute(
        """
        SELECT id, kind, site_id, lane, job_id, run_id, target_id, status, lease_owner,
               worker_guid, container_name, attempt_count, heartbeat_at, started_at,
               finished_at, updated_at, payload_json, error
          FROM job_scheduler_work_items
         WHERE status IN (?,?,?,?,?)
           AND COALESCE(finished_at, heartbeat_at, started_at, updated_at, created_at, 0) >= ?
      ORDER BY COALESCE(finished_at, heartbeat_at, started_at, updated_at, created_at, 0) DESC, id DESC
        """,
        (
            WORK_STATUS_QUEUED,
            WORK_STATUS_RUNNING,
            WORK_STATUS_SUCCEEDED,
            WORK_STATUS_FAILED,
            WORK_STATUS_CANCELLED,
            cutoff,
        ),
    )
    rows = []
    for row in cur.fetchall() or []:
        payload = _json_loads(row[16], {})
        payload_map = payload if isinstance(payload, Mapping) else {}
        task_link = payload.get("task_link") if isinstance(payload, Mapping) and isinstance(payload.get("task_link"), Mapping) else {}
        target_count = _payload_target_count(payload_map)
        rows.append(
            {
                "id": int(row[0]),
                "kind": row[1] or "",
                "site_id": row[2],
                "lane": row[3] or "",
                "job_id": row[4],
                "run_id": row[5],
                "target_id": row[6],
                "status": row[7] or "",
                "lease_owner": row[8] or "",
                "worker_guid": row[9] or "",
                "container_name": row[10] or "",
                "attempt_count": int(row[11] or 0),
                "heartbeat_at": row[12],
                "started_at": row[13],
                "finished_at": row[14],
                "updated_at": row[15],
                "task_link": dict(task_link or {}),
                "target_count": target_count,
                "task_type": _payload_task_type(row[1] or "", payload_map),
                "error": row[17] or "",
            }
        )
    return rows


def replace_service_snapshots(conn: sqlite3.Connection, snapshots: Sequence[Mapping[str, Any]]) -> None:
    ensure_job_scheduler_tables(conn)
    now = _now_ts()
    cur = conn.cursor()
    for snapshot in snapshots or []:
        service_key = str(snapshot.get("Service") or snapshot.get("service") or snapshot.get("service_key") or "").strip()
        if not service_key:
            continue
        cur.execute(
            """
            INSERT INTO job_scheduler_service_snapshots(service_key, payload_json, updated_at)
            VALUES (?,?,?)
            ON CONFLICT (service_key) DO UPDATE SET
                payload_json=EXCLUDED.payload_json,
                updated_at=EXCLUDED.updated_at
            """,
            (service_key, _json_dumps(dict(snapshot)), now),
        )


def list_service_snapshots(conn: sqlite3.Connection, *, max_age_seconds: int = 120) -> Dict[str, Mapping[str, Any]]:
    ensure_job_scheduler_tables(conn)
    cutoff = _now_ts() - max(10, int(max_age_seconds or 120))
    cur = conn.cursor()
    cur.execute(
        """
        SELECT service_key, payload_json
          FROM job_scheduler_service_snapshots
         WHERE updated_at>=?
        """,
        (cutoff,),
    )
    results: Dict[str, Mapping[str, Any]] = {}
    for service_key, payload_json in cur.fetchall() or []:
        payload = _json_loads(payload_json, {})
        if isinstance(payload, Mapping):
            results[str(service_key or "")] = payload
    return results
