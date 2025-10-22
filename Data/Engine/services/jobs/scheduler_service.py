"""Background scheduler service for the Borealis Engine."""

from __future__ import annotations

import calendar
import logging
import threading
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Iterable, Mapping, Optional

from Data.Engine.builders.job_fabricator import JobFabricator, JobManifest
from Data.Engine.repositories.sqlite.job_repository import (
    ScheduledJobRecord,
    ScheduledJobRunRecord,
    SQLiteJobRepository,
)

__all__ = ["SchedulerService", "SchedulerRuntime"]


def _now_ts() -> int:
    return int(time.time())


def _floor_minute(ts: int) -> int:
    ts = int(ts or 0)
    return ts - (ts % 60)


def _parse_ts(val: Any) -> Optional[int]:
    if val is None:
        return None
    if isinstance(val, (int, float)):
        return int(val)
    try:
        s = str(val).strip().replace("Z", "+00:00")
        return int(datetime.fromisoformat(s).timestamp())
    except Exception:
        return None


def _parse_expiration(raw: Optional[str]) -> Optional[int]:
    if not raw or raw == "no_expire":
        return None
    try:
        s = raw.strip().lower()
        unit = s[-1]
        value = int(s[:-1])
        if unit == "m":
            return value * 60
        if unit == "h":
            return value * 3600
        if unit == "d":
            return value * 86400
        return int(s) * 60
    except Exception:
        return None


def _add_months(dt: datetime, months: int) -> datetime:
    year = dt.year + (dt.month - 1 + months) // 12
    month = ((dt.month - 1 + months) % 12) + 1
    last_day = calendar.monthrange(year, month)[1]
    day = min(dt.day, last_day)
    return dt.replace(year=year, month=month, day=day)


def _add_years(dt: datetime, years: int) -> datetime:
    year = dt.year + years
    month = dt.month
    last_day = calendar.monthrange(year, month)[1]
    day = min(dt.day, last_day)
    return dt.replace(year=year, month=month, day=day)


def _compute_next_run(
    schedule_type: str,
    start_ts: Optional[int],
    last_run_ts: Optional[int],
    now_ts: int,
) -> Optional[int]:
    st = (schedule_type or "immediately").strip().lower()
    start_floor = _floor_minute(start_ts) if start_ts else None
    last_floor = _floor_minute(last_run_ts) if last_run_ts else None
    now_floor = _floor_minute(now_ts)

    if st == "immediately":
        return None if last_floor else now_floor
    if st == "once":
        if not start_floor:
            return None
        return start_floor if not last_floor else None
    if not start_floor:
        return None

    last = last_floor if last_floor is not None else None
    if st in {
        "every_5_minutes",
        "every_10_minutes",
        "every_15_minutes",
        "every_30_minutes",
        "every_hour",
    }:
        period_map = {
            "every_5_minutes": 5 * 60,
            "every_10_minutes": 10 * 60,
            "every_15_minutes": 15 * 60,
            "every_30_minutes": 30 * 60,
            "every_hour": 60 * 60,
        }
        period = period_map.get(st)
        candidate = (last + period) if last else start_floor
        while candidate is not None and candidate <= now_floor - 1:
            candidate += period
        return candidate
    if st == "daily":
        period = 86400
        candidate = (last + period) if last else start_floor
        while candidate is not None and candidate <= now_floor - 1:
            candidate += period
        return candidate
    if st == "weekly":
        period = 7 * 86400
        candidate = (last + period) if last else start_floor
        while candidate is not None and candidate <= now_floor - 1:
            candidate += period
        return candidate
    if st == "monthly":
        base = datetime.utcfromtimestamp(last) if last else datetime.utcfromtimestamp(start_floor)
        candidate = _add_months(base, 1 if last else 0)
        while int(candidate.timestamp()) <= now_floor - 1:
            candidate = _add_months(candidate, 1)
        return int(candidate.timestamp())
    if st == "yearly":
        base = datetime.utcfromtimestamp(last) if last else datetime.utcfromtimestamp(start_floor)
        candidate = _add_years(base, 1 if last else 0)
        while int(candidate.timestamp()) <= now_floor - 1:
            candidate = _add_years(candidate, 1)
        return int(candidate.timestamp())
    return None


@dataclass
class SchedulerRuntime:
    thread: Optional[threading.Thread]
    stop_event: threading.Event


class SchedulerService:
    """Evaluate and dispatch scheduled jobs using Engine repositories."""

    def __init__(
        self,
        *,
        job_repository: SQLiteJobRepository,
        assemblies_root: Path,
        logger: Optional[logging.Logger] = None,
        poll_interval: int = 30,
    ) -> None:
        self._jobs = job_repository
        self._fabricator = JobFabricator(assemblies_root=assemblies_root, logger=logger)
        self._log = logger or logging.getLogger("borealis.engine.scheduler")
        self._poll_interval = max(5, poll_interval)
        self._socketio: Optional[Any] = None
        self._runtime = SchedulerRuntime(thread=None, stop_event=threading.Event())

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------
    def start(self, socketio: Optional[Any] = None) -> None:
        self._socketio = socketio
        if self._runtime.thread and self._runtime.thread.is_alive():
            return
        self._runtime.stop_event.clear()
        thread = threading.Thread(target=self._run_loop, name="borealis-engine-scheduler", daemon=True)
        thread.start()
        self._runtime.thread = thread
        self._log.info("scheduler-started")

    def stop(self) -> None:
        self._runtime.stop_event.set()
        thread = self._runtime.thread
        if thread and thread.is_alive():
            thread.join(timeout=5)
        self._log.info("scheduler-stopped")

    # ------------------------------------------------------------------
    # HTTP orchestration helpers
    # ------------------------------------------------------------------
    def list_jobs(self) -> list[dict[str, Any]]:
        return [self._serialize_job(job) for job in self._jobs.list_jobs()]

    def get_job(self, job_id: int) -> Optional[dict[str, Any]]:
        record = self._jobs.fetch_job(job_id)
        return self._serialize_job(record) if record else None

    def create_job(self, payload: Mapping[str, Any]) -> dict[str, Any]:
        fields = self._normalize_payload(payload)
        record = self._jobs.create_job(**fields)
        return self._serialize_job(record)

    def update_job(self, job_id: int, payload: Mapping[str, Any]) -> Optional[dict[str, Any]]:
        fields = self._normalize_payload(payload)
        record = self._jobs.update_job(job_id, **fields)
        return self._serialize_job(record) if record else None

    def toggle_job(self, job_id: int, enabled: bool) -> None:
        self._jobs.set_enabled(job_id, enabled)

    def delete_job(self, job_id: int) -> None:
        self._jobs.delete_job(job_id)

    def list_runs(self, job_id: int, *, days: Optional[int] = None) -> list[dict[str, Any]]:
        runs = self._jobs.list_runs(job_id, days=days)
        return [self._serialize_run(run) for run in runs]

    def purge_runs(self, job_id: int) -> None:
        self._jobs.purge_runs(job_id)

    # ------------------------------------------------------------------
    # Scheduling loop
    # ------------------------------------------------------------------
    def tick(self, *, now_ts: Optional[int] = None) -> None:
        self._evaluate_jobs(now_ts=now_ts or _now_ts())

    def _run_loop(self) -> None:
        stop_event = self._runtime.stop_event
        while not stop_event.wait(timeout=self._poll_interval):
            try:
                self._evaluate_jobs(now_ts=_now_ts())
            except Exception as exc:  # pragma: no cover - safety net
                self._log.exception("scheduler-loop-error: %s", exc)

    def _evaluate_jobs(self, *, now_ts: int) -> None:
        for job in self._jobs.list_enabled_jobs():
            try:
                self._evaluate_job(job, now_ts=now_ts)
            except Exception as exc:
                self._log.exception("job-evaluation-error job_id=%s error=%s", job.id, exc)

    def _evaluate_job(self, job: ScheduledJobRecord, *, now_ts: int) -> None:
        last_run = self._jobs.fetch_last_run(job.id)
        last_ts = None
        if last_run:
            last_ts = last_run.scheduled_ts or last_run.started_ts or last_run.created_at
        next_run = _compute_next_run(job.schedule_type, job.start_ts, last_ts, now_ts)
        if next_run is None or next_run > now_ts:
            return

        expiration_window = _parse_expiration(job.expiration)
        if expiration_window and job.start_ts:
            if job.start_ts + expiration_window <= now_ts:
                self._log.info(
                    "job-expired",
                    extra={"job_id": job.id, "start_ts": job.start_ts, "expiration": job.expiration},
                )
                return

        manifest = self._fabricator.build(job, occurrence_ts=next_run)
        targets = manifest.targets or ("<unassigned>",)
        for target in targets:
            run_id = self._jobs.create_run(job.id, next_run, target_hostname=None if target == "<unassigned>" else target)
            self._jobs.mark_run_started(run_id, started_ts=now_ts)
            self._emit_run_event("job_run_started", job, run_id, target, manifest)
            self._jobs.mark_run_finished(run_id, status="Success", finished_ts=now_ts)
            self._emit_run_event("job_run_completed", job, run_id, target, manifest)

    def _emit_run_event(
        self,
        event: str,
        job: ScheduledJobRecord,
        run_id: int,
        target: str,
        manifest: JobManifest,
    ) -> None:
        payload = {
            "job_id": job.id,
            "run_id": run_id,
            "target": target,
            "schedule_type": job.schedule_type,
            "occurrence_ts": manifest.occurrence_ts,
        }
        if self._socketio is not None:
            try:
                self._socketio.emit(event, payload)  # type: ignore[attr-defined]
            except Exception:
                self._log.debug("socketio-emit-failed event=%s payload=%s", event, payload)

    # ------------------------------------------------------------------
    # Serialization helpers
    # ------------------------------------------------------------------
    def _serialize_job(self, job: ScheduledJobRecord) -> dict[str, Any]:
        return {
            "id": job.id,
            "name": job.name,
            "components": job.components,
            "targets": job.targets,
            "schedule": {
                "type": job.schedule_type,
                "start": job.start_ts,
            },
            "schedule_type": job.schedule_type,
            "start_ts": job.start_ts,
            "duration_stop_enabled": job.duration_stop_enabled,
            "expiration": job.expiration or "no_expire",
            "execution_context": job.execution_context,
            "credential_id": job.credential_id,
            "use_service_account": job.use_service_account,
            "enabled": job.enabled,
            "created_at": job.created_at,
            "updated_at": job.updated_at,
        }

    def _serialize_run(self, run: ScheduledJobRunRecord) -> dict[str, Any]:
        return {
            "id": run.id,
            "job_id": run.job_id,
            "scheduled_ts": run.scheduled_ts,
            "started_ts": run.started_ts,
            "finished_ts": run.finished_ts,
            "status": run.status,
            "error": run.error,
            "target_hostname": run.target_hostname,
            "created_at": run.created_at,
            "updated_at": run.updated_at,
        }

    def _normalize_payload(self, payload: Mapping[str, Any]) -> dict[str, Any]:
        name = str(payload.get("name") or "").strip()
        components = payload.get("components") or []
        targets = payload.get("targets") or []
        schedule_block = payload.get("schedule") if isinstance(payload.get("schedule"), Mapping) else {}
        schedule_type = str(schedule_block.get("type") or payload.get("schedule_type") or "immediately").strip().lower()
        start_value = schedule_block.get("start") if isinstance(schedule_block, Mapping) else None
        if start_value is None:
            start_value = payload.get("start")
        start_ts = _parse_ts(start_value)
        duration_block = payload.get("duration") if isinstance(payload.get("duration"), Mapping) else {}
        duration_stop = bool(duration_block.get("stopAfterEnabled") or payload.get("duration_stop_enabled"))
        expiration = str(duration_block.get("expiration") or payload.get("expiration") or "no_expire").strip()
        execution_context = str(payload.get("execution_context") or "system").strip().lower()
        credential_id = payload.get("credential_id")
        try:
            credential_id = int(credential_id) if credential_id is not None else None
        except Exception:
            credential_id = None
        use_service_account_raw = payload.get("use_service_account")
        use_service_account = bool(use_service_account_raw) if execution_context == "winrm" else False
        enabled = bool(payload.get("enabled", True))

        if not name:
            raise ValueError("job name is required")
        if not isinstance(components, Iterable) or not list(components):
            raise ValueError("at least one component is required")
        if not isinstance(targets, Iterable) or not list(targets):
            raise ValueError("at least one target is required")

        return {
            "name": name,
            "components": list(components),
            "targets": list(targets),
            "schedule_type": schedule_type,
            "start_ts": start_ts,
            "duration_stop_enabled": duration_stop,
            "expiration": expiration,
            "execution_context": execution_context,
            "credential_id": credential_id,
            "use_service_account": use_service_account,
            "enabled": enabled,
        }
