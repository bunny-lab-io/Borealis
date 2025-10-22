"""SQLite-backed persistence for Engine job scheduling."""

from __future__ import annotations

import json
import logging
import time
from dataclasses import dataclass
from typing import Any, Iterable, Optional, Sequence

import sqlite3

from .connection import SQLiteConnectionFactory

__all__ = [
    "ScheduledJobRecord",
    "ScheduledJobRunRecord",
    "SQLiteJobRepository",
]


def _now_ts() -> int:
    return int(time.time())


def _json_dumps(value: Any) -> str:
    try:
        return json.dumps(value or [])
    except Exception:
        return "[]"


def _json_loads(value: Optional[str]) -> list[Any]:
    if not value:
        return []
    try:
        data = json.loads(value)
        if isinstance(data, list):
            return data
        return []
    except Exception:
        return []


@dataclass(frozen=True, slots=True)
class ScheduledJobRecord:
    id: int
    name: str
    components: list[dict[str, Any]]
    targets: list[str]
    schedule_type: str
    start_ts: Optional[int]
    duration_stop_enabled: bool
    expiration: Optional[str]
    execution_context: str
    credential_id: Optional[int]
    use_service_account: bool
    enabled: bool
    created_at: Optional[int]
    updated_at: Optional[int]


@dataclass(frozen=True, slots=True)
class ScheduledJobRunRecord:
    id: int
    job_id: int
    scheduled_ts: Optional[int]
    started_ts: Optional[int]
    finished_ts: Optional[int]
    status: Optional[str]
    error: Optional[str]
    target_hostname: Optional[str]
    created_at: Optional[int]
    updated_at: Optional[int]


class SQLiteJobRepository:
    """Persistence adapter for Engine job scheduling."""

    def __init__(
        self,
        factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._factory = factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.jobs")

    # ------------------------------------------------------------------
    # Job CRUD
    # ------------------------------------------------------------------
    def list_jobs(self) -> list[ScheduledJobRecord]:
        query = (
            "SELECT id, name, components_json, targets_json, schedule_type, start_ts, "
            "duration_stop_enabled, expiration, execution_context, credential_id, "
            "use_service_account, enabled, created_at, updated_at FROM scheduled_jobs "
            "ORDER BY id ASC"
        )
        return [self._row_to_job(row) for row in self._fetchall(query)]

    def list_enabled_jobs(self) -> list[ScheduledJobRecord]:
        query = (
            "SELECT id, name, components_json, targets_json, schedule_type, start_ts, "
            "duration_stop_enabled, expiration, execution_context, credential_id, "
            "use_service_account, enabled, created_at, updated_at FROM scheduled_jobs "
            "WHERE enabled=1 ORDER BY id ASC"
        )
        return [self._row_to_job(row) for row in self._fetchall(query)]

    def fetch_job(self, job_id: int) -> Optional[ScheduledJobRecord]:
        query = (
            "SELECT id, name, components_json, targets_json, schedule_type, start_ts, "
            "duration_stop_enabled, expiration, execution_context, credential_id, "
            "use_service_account, enabled, created_at, updated_at FROM scheduled_jobs "
            "WHERE id=?"
        )
        rows = self._fetchall(query, (job_id,))
        return self._row_to_job(rows[0]) if rows else None

    def create_job(
        self,
        *,
        name: str,
        components: Sequence[dict[str, Any]],
        targets: Sequence[Any],
        schedule_type: str,
        start_ts: Optional[int],
        duration_stop_enabled: bool,
        expiration: Optional[str],
        execution_context: str,
        credential_id: Optional[int],
        use_service_account: bool,
        enabled: bool = True,
    ) -> ScheduledJobRecord:
        now = _now_ts()
        payload = (
            name,
            _json_dumps(list(components)),
            _json_dumps(list(targets)),
            schedule_type,
            start_ts,
            1 if duration_stop_enabled else 0,
            expiration,
            execution_context,
            credential_id,
            1 if use_service_account else 0,
            1 if enabled else 0,
            now,
            now,
        )
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO scheduled_jobs
                    (name, components_json, targets_json, schedule_type, start_ts,
                     duration_stop_enabled, expiration, execution_context, credential_id,
                     use_service_account, enabled, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                payload,
            )
            job_id = cur.lastrowid
            conn.commit()
        record = self.fetch_job(int(job_id))
        if record is None:
            raise RuntimeError("failed to create scheduled job")
        return record

    def update_job(
        self,
        job_id: int,
        *,
        name: str,
        components: Sequence[dict[str, Any]],
        targets: Sequence[Any],
        schedule_type: str,
        start_ts: Optional[int],
        duration_stop_enabled: bool,
        expiration: Optional[str],
        execution_context: str,
        credential_id: Optional[int],
        use_service_account: bool,
    ) -> Optional[ScheduledJobRecord]:
        now = _now_ts()
        payload = (
            name,
            _json_dumps(list(components)),
            _json_dumps(list(targets)),
            schedule_type,
            start_ts,
            1 if duration_stop_enabled else 0,
            expiration,
            execution_context,
            credential_id,
            1 if use_service_account else 0,
            now,
            job_id,
        )
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE scheduled_jobs
                SET name=?, components_json=?, targets_json=?, schedule_type=?,
                    start_ts=?, duration_stop_enabled=?, expiration=?, execution_context=?,
                    credential_id=?, use_service_account=?, updated_at=?
                WHERE id=?
                """,
                payload,
            )
            conn.commit()
        return self.fetch_job(job_id)

    def set_enabled(self, job_id: int, enabled: bool) -> None:
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute(
                "UPDATE scheduled_jobs SET enabled=?, updated_at=? WHERE id=?",
                (1 if enabled else 0, _now_ts(), job_id),
            )
            conn.commit()

    def delete_job(self, job_id: int) -> None:
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute("DELETE FROM scheduled_jobs WHERE id=?", (job_id,))
            conn.commit()

    # ------------------------------------------------------------------
    # Run history
    # ------------------------------------------------------------------
    def list_runs(self, job_id: int, *, days: Optional[int] = None) -> list[ScheduledJobRunRecord]:
        params: list[Any] = [job_id]
        where = "WHERE job_id=?"
        if days is not None and days > 0:
            cutoff = _now_ts() - (days * 86400)
            where += " AND COALESCE(finished_ts, scheduled_ts, started_ts, 0) >= ?"
            params.append(cutoff)

        query = (
            "SELECT id, job_id, scheduled_ts, started_ts, finished_ts, status, error, "
            "target_hostname, created_at, updated_at FROM scheduled_job_runs "
            f"{where} ORDER BY COALESCE(scheduled_ts, created_at, id) DESC"
        )
        return [self._row_to_run(row) for row in self._fetchall(query, tuple(params))]

    def fetch_last_run(self, job_id: int) -> Optional[ScheduledJobRunRecord]:
        query = (
            "SELECT id, job_id, scheduled_ts, started_ts, finished_ts, status, error, "
            "target_hostname, created_at, updated_at FROM scheduled_job_runs "
            "WHERE job_id=? ORDER BY COALESCE(started_ts, scheduled_ts, created_at, id) DESC LIMIT 1"
        )
        rows = self._fetchall(query, (job_id,))
        return self._row_to_run(rows[0]) if rows else None

    def purge_runs(self, job_id: int) -> None:
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute("DELETE FROM scheduled_job_run_activity WHERE run_id IN (SELECT id FROM scheduled_job_runs WHERE job_id=?)", (job_id,))
            cur.execute("DELETE FROM scheduled_job_runs WHERE job_id=?", (job_id,))
            conn.commit()

    def create_run(self, job_id: int, scheduled_ts: int, *, target_hostname: Optional[str] = None) -> int:
        now = _now_ts()
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO scheduled_job_runs
                    (job_id, scheduled_ts, created_at, updated_at, target_hostname, status)
                VALUES (?, ?, ?, ?, ?, 'Pending')
                """,
                (job_id, scheduled_ts, now, now, target_hostname),
            )
            run_id = int(cur.lastrowid)
            conn.commit()
        return run_id

    def mark_run_started(self, run_id: int, *, started_ts: Optional[int] = None) -> None:
        started = started_ts or _now_ts()
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute(
                "UPDATE scheduled_job_runs SET started_ts=?, status='Running', updated_at=? WHERE id=?",
                (started, _now_ts(), run_id),
            )
            conn.commit()

    def mark_run_finished(
        self,
        run_id: int,
        *,
        status: str,
        error: Optional[str] = None,
        finished_ts: Optional[int] = None,
    ) -> None:
        finished = finished_ts or _now_ts()
        with self._connect() as conn:
            cur = conn.cursor()
            cur.execute(
                "UPDATE scheduled_job_runs SET finished_ts=?, status=?, error=?, updated_at=? WHERE id=?",
                (finished, status, error, _now_ts(), run_id),
            )
            conn.commit()

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _connect(self) -> sqlite3.Connection:
        return self._factory()

    def _fetchall(self, query: str, params: Optional[Iterable[Any]] = None) -> list[sqlite3.Row]:
        with self._connect() as conn:
            conn.row_factory = sqlite3.Row
            cur = conn.cursor()
            cur.execute(query, tuple(params or ()))
            rows = cur.fetchall()
        return rows

    def _row_to_job(self, row: sqlite3.Row) -> ScheduledJobRecord:
        components = _json_loads(row["components_json"])
        targets_raw = _json_loads(row["targets_json"])
        targets = [str(t) for t in targets_raw if isinstance(t, (str, int))]
        credential_id = row["credential_id"]
        return ScheduledJobRecord(
            id=int(row["id"]),
            name=str(row["name"] or ""),
            components=[c for c in components if isinstance(c, dict)],
            targets=targets,
            schedule_type=str(row["schedule_type"] or "immediately"),
            start_ts=int(row["start_ts"]) if row["start_ts"] is not None else None,
            duration_stop_enabled=bool(row["duration_stop_enabled"]),
            expiration=str(row["expiration"]) if row["expiration"] else None,
            execution_context=str(row["execution_context"] or "system"),
            credential_id=int(credential_id) if credential_id is not None else None,
            use_service_account=bool(row["use_service_account"]),
            enabled=bool(row["enabled"]),
            created_at=int(row["created_at"]) if row["created_at"] is not None else None,
            updated_at=int(row["updated_at"]) if row["updated_at"] is not None else None,
        )

    def _row_to_run(self, row: sqlite3.Row) -> ScheduledJobRunRecord:
        return ScheduledJobRunRecord(
            id=int(row["id"]),
            job_id=int(row["job_id"]),
            scheduled_ts=int(row["scheduled_ts"]) if row["scheduled_ts"] is not None else None,
            started_ts=int(row["started_ts"]) if row["started_ts"] is not None else None,
            finished_ts=int(row["finished_ts"]) if row["finished_ts"] is not None else None,
            status=str(row["status"]) if row["status"] else None,
            error=str(row["error"]) if row["error"] else None,
            target_hostname=str(row["target_hostname"]) if row["target_hostname"] else None,
            created_at=int(row["created_at"]) if row["created_at"] is not None else None,
            updated_at=int(row["updated_at"]) if row["updated_at"] is not None else None,
        )
