from __future__ import annotations

import json
import time
from typing import Any, Dict, List, Optional, Sequence, Set


_UNSET = object()
_ACTIVE_STATUSES = {"queued", "running", "pending", "created", "started", "in_progress"}
_TERMINAL_STATUSES = {"success", "failed", "completed", "complete", "timed_out", "timeout", "skipped"}


def _now_ts() -> int:
    return int(time.time())


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _coerce_int(value: Any, default: int = 0) -> int:
    try:
        if value in (None, ""):
            raise ValueError
        return int(float(value))
    except Exception:
        return default


def normalize_activity_status(value: Any, *, default: str = "Queued") -> str:
    text = _clean_text(value)
    if not text:
        return default
    lowered = text.lower().replace("-", "_").replace(" ", "_")
    mapping = {
        "queued": "Queued",
        "pending": "Queued",
        "created": "Queued",
        "running": "Running",
        "started": "Running",
        "in_progress": "Running",
        "success": "Success",
        "completed": "Success",
        "complete": "Success",
        "failed": "Failed",
        "error": "Failed",
        "timed_out": "Timed Out",
        "timeout": "Timed Out",
        "skipped": "Skipped",
    }
    return mapping.get(lowered, text)


def status_is_active(value: Any) -> bool:
    return _clean_text(value).lower().replace("-", "_").replace(" ", "_") in _ACTIVE_STATUSES


def status_is_terminal(value: Any) -> bool:
    return _clean_text(value).lower().replace("-", "_").replace(" ", "_") in _TERMINAL_STATUSES


def serialize_activity_metadata(value: Any) -> str:
    if not isinstance(value, dict) or not value:
        return ""
    try:
        return json.dumps(value, sort_keys=True, separators=(",", ":"))
    except Exception:
        return ""


def parse_activity_metadata(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return dict(value)
    text = _clean_text(value)
    if not text:
        return {}
    try:
        parsed = json.loads(text)
    except Exception:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def get_activity_history_columns(conn: Any) -> Set[str]:
    cur = conn.cursor()
    try:
        cur.execute("PRAGMA table_info(activity_history)")
        return {
            _clean_text(row[1]).lower()
            for row in cur.fetchall() or []
            if len(row) > 1 and _clean_text(row[1])
        }
    finally:
        try:
            cur.close()
        except Exception:
            pass


def insert_activity_history_row(
    conn: Any,
    *,
    hostname: Any,
    script_path: Any,
    script_name: Any,
    script_type: Any,
    ran_at: Any,
    status: Any = "Queued",
    stdout: Any = "",
    stderr: Any = "",
    queue_lane: Any = "",
    activity_kind: Any = "",
    metadata: Optional[Dict[str, Any]] = None,
    started_at: Any = None,
    updated_at: Any = None,
    finished_at: Any = None,
) -> Optional[int]:
    columns = get_activity_history_columns(conn)
    normalized_status = normalize_activity_status(status)
    normalized_ran_at = _coerce_int(ran_at, _now_ts())
    resolved_updated_at = _coerce_int(updated_at, normalized_ran_at)
    resolved_started_at = _coerce_int(started_at, 0)
    resolved_finished_at = _coerce_int(finished_at, 0)
    if not resolved_started_at and normalized_status.lower() == "running":
        resolved_started_at = resolved_updated_at or normalized_ran_at
    if not resolved_finished_at and status_is_terminal(normalized_status):
        resolved_finished_at = resolved_updated_at or normalized_ran_at

    insert_columns: List[str] = [
        "hostname",
        "script_path",
        "script_name",
        "script_type",
        "ran_at",
        "status",
        "stdout",
        "stderr",
    ]
    insert_values: List[Any] = [
        _clean_text(hostname),
        _clean_text(script_path),
        _clean_text(script_name),
        _clean_text(script_type),
        normalized_ran_at,
        normalized_status,
        "" if stdout is None else str(stdout),
        "" if stderr is None else str(stderr),
    ]
    if "queue_lane" in columns:
        insert_columns.append("queue_lane")
        insert_values.append(_clean_text(queue_lane))
    if "activity_kind" in columns:
        insert_columns.append("activity_kind")
        insert_values.append(_clean_text(activity_kind))
    if "metadata_json" in columns:
        insert_columns.append("metadata_json")
        insert_values.append(serialize_activity_metadata(metadata))
    if "started_at" in columns:
        insert_columns.append("started_at")
        insert_values.append(resolved_started_at or None)
    if "updated_at" in columns:
        insert_columns.append("updated_at")
        insert_values.append(resolved_updated_at or None)
    if "finished_at" in columns:
        insert_columns.append("finished_at")
        insert_values.append(resolved_finished_at or None)

    placeholders = ",".join("?" for _ in insert_columns)
    sql = f"INSERT INTO activity_history({','.join(insert_columns)}) VALUES({placeholders})"
    cur = conn.cursor()
    try:
        cur.execute(sql, tuple(insert_values))
        lastrowid = getattr(cur, "lastrowid", None)
        if lastrowid not in (None, ""):
            try:
                return int(lastrowid)
            except Exception:
                return None
    finally:
        try:
            cur.close()
        except Exception:
            pass
    return None


def update_activity_history_row(
    conn: Any,
    activity_id: Any,
    *,
    status: Any = _UNSET,
    stdout: Any = _UNSET,
    stderr: Any = _UNSET,
    append_output: bool = False,
    queue_lane: Any = _UNSET,
    activity_kind: Any = _UNSET,
    metadata: Any = _UNSET,
    started_at: Any = _UNSET,
    updated_at: Any = _UNSET,
    finished_at: Any = _UNSET,
    ran_at: Any = _UNSET,
) -> int:
    columns = get_activity_history_columns(conn)
    sets: List[str] = []
    params: List[Any] = []

    if status is not _UNSET:
        sets.append("status=?")
        params.append(normalize_activity_status(status, default=""))
    if stdout is not _UNSET:
        if append_output:
            sets.append("stdout=COALESCE(stdout, '') || ?")
        else:
            sets.append("stdout=?")
        params.append("" if stdout is None else str(stdout))
    if stderr is not _UNSET:
        if append_output:
            sets.append("stderr=COALESCE(stderr, '') || ?")
        else:
            sets.append("stderr=?")
        params.append("" if stderr is None else str(stderr))
    if queue_lane is not _UNSET and "queue_lane" in columns:
        sets.append("queue_lane=?")
        params.append(_clean_text(queue_lane))
    if activity_kind is not _UNSET and "activity_kind" in columns:
        sets.append("activity_kind=?")
        params.append(_clean_text(activity_kind))
    if metadata is not _UNSET and "metadata_json" in columns:
        sets.append("metadata_json=?")
        params.append(serialize_activity_metadata(metadata))
    if started_at is not _UNSET and "started_at" in columns:
        sets.append("started_at=?")
        params.append(_coerce_int(started_at, 0) or None)
    if updated_at is not _UNSET and "updated_at" in columns:
        sets.append("updated_at=?")
        params.append(_coerce_int(updated_at, 0) or None)
    if finished_at is not _UNSET and "finished_at" in columns:
        sets.append("finished_at=?")
        params.append(_coerce_int(finished_at, 0) or None)
    if ran_at is not _UNSET:
        sets.append("ran_at=?")
        params.append(_coerce_int(ran_at, 0) or None)
    if not sets:
        return 0
    params.append(int(activity_id))
    cur = conn.cursor()
    try:
        cur.execute(f"UPDATE activity_history SET {', '.join(sets)} WHERE id=?", tuple(params))
        return int(getattr(cur, "rowcount", 0) or 0)
    finally:
        try:
            cur.close()
        except Exception:
            pass


def list_activity_history_rows(conn: Any, hostname: Any) -> List[Dict[str, Any]]:
    columns = get_activity_history_columns(conn)
    queue_lane_expr = "queue_lane" if "queue_lane" in columns else "NULL"
    activity_kind_expr = "activity_kind" if "activity_kind" in columns else "NULL"
    metadata_expr = "metadata_json" if "metadata_json" in columns else "NULL"
    started_expr = "started_at" if "started_at" in columns else "NULL"
    updated_expr = "updated_at" if "updated_at" in columns else "NULL"
    finished_expr = "finished_at" if "finished_at" in columns else "NULL"
    order_timestamp_expr = (
        "COALESCE(updated_at, started_at, ran_at, 0)"
        if "updated_at" in columns or "started_at" in columns
        else "COALESCE(ran_at, 0)"
    )
    cur = conn.cursor()
    try:
        cur.execute(
            f"""
            SELECT id,
                   hostname,
                   script_name,
                   script_path,
                   script_type,
                   ran_at,
                   status,
                   LENGTH(stdout) AS stdout_len,
                   LENGTH(stderr) AS stderr_len,
                   {queue_lane_expr} AS queue_lane,
                   {activity_kind_expr} AS activity_kind,
                   {metadata_expr} AS metadata_json,
                   {started_expr} AS started_at,
                   {updated_expr} AS updated_at,
                   {finished_expr} AS finished_at
              FROM activity_history
             WHERE hostname = ?
             ORDER BY CASE
                        WHEN LOWER(COALESCE(status, '')) IN ('queued', 'running', 'pending', 'created', 'started', 'in_progress')
                        THEN 0
                        ELSE 1
                      END ASC,
                      {order_timestamp_expr} DESC,
                      id DESC
            """,
            (_clean_text(hostname),),
        )
        rows = cur.fetchall() or []
    finally:
        try:
            cur.close()
        except Exception:
            pass
    history: List[Dict[str, Any]] = []
    for row in rows:
        history.append(
            {
                "id": int(row[0]),
                "hostname": _clean_text(row[1]),
                "script_name": _clean_text(row[2]),
                "script_path": _clean_text(row[3]),
                "script_type": _clean_text(row[4]),
                "ran_at": _coerce_int(row[5], 0),
                "status": normalize_activity_status(row[6], default=_clean_text(row[6]) or "Unknown"),
                "has_stdout": bool(_coerce_int(row[7], 0)),
                "has_stderr": bool(_coerce_int(row[8], 0)),
                "queue_lane": _clean_text(row[9]),
                "activity_kind": _clean_text(row[10]),
                "metadata": parse_activity_metadata(row[11]),
                "started_at": _coerce_int(row[12], 0),
                "updated_at": _coerce_int(row[13], 0),
                "finished_at": _coerce_int(row[14], 0),
            }
        )
    return history


def get_activity_history_row(conn: Any, activity_id: Any) -> Optional[Dict[str, Any]]:
    columns = get_activity_history_columns(conn)
    queue_lane_expr = "queue_lane" if "queue_lane" in columns else "NULL"
    activity_kind_expr = "activity_kind" if "activity_kind" in columns else "NULL"
    metadata_expr = "metadata_json" if "metadata_json" in columns else "NULL"
    started_expr = "started_at" if "started_at" in columns else "NULL"
    updated_expr = "updated_at" if "updated_at" in columns else "NULL"
    finished_expr = "finished_at" if "finished_at" in columns else "NULL"
    cur = conn.cursor()
    try:
        cur.execute(
            f"""
            SELECT id,
                   hostname,
                   script_name,
                   script_path,
                   script_type,
                   ran_at,
                   status,
                   stdout,
                   stderr,
                   {queue_lane_expr} AS queue_lane,
                   {activity_kind_expr} AS activity_kind,
                   {metadata_expr} AS metadata_json,
                   {started_expr} AS started_at,
                   {updated_expr} AS updated_at,
                   {finished_expr} AS finished_at
              FROM activity_history
             WHERE id = ?
            """,
            (int(activity_id),),
        )
        row = cur.fetchone()
    finally:
        try:
            cur.close()
        except Exception:
            pass
    if not row:
        return None
    return {
        "id": int(row[0]),
        "hostname": _clean_text(row[1]),
        "script_name": _clean_text(row[2]),
        "script_path": _clean_text(row[3]),
        "script_type": _clean_text(row[4]),
        "ran_at": _coerce_int(row[5], 0),
        "status": normalize_activity_status(row[6], default=_clean_text(row[6]) or "Unknown"),
        "stdout": "" if row[7] is None else str(row[7]),
        "stderr": "" if row[8] is None else str(row[8]),
        "queue_lane": _clean_text(row[9]),
        "activity_kind": _clean_text(row[10]),
        "metadata": parse_activity_metadata(row[11]),
        "started_at": _coerce_int(row[12], 0),
        "updated_at": _coerce_int(row[13], 0),
        "finished_at": _coerce_int(row[14], 0),
    }
