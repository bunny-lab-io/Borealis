from __future__ import annotations

import time
from pathlib import Path

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.API.scheduled_jobs import job_scheduler
from Data.Engine.services.job_scheduler.queue import (
    LANE_SCHEDULED_JOB,
    WORK_STATUS_QUEUED,
    WORK_STATUS_RUNNING,
    WORKER_STATUS_LOST,
    WORKER_STATUS_RUNNING,
    WORKER_STATUS_STOPPED,
    claim_next_work_item,
    enqueue_scheduled_run,
    ensure_job_scheduler_tables,
    expire_stale_leases,
    mark_missing_workers_lost,
    register_worker,
    requeue_work_item,
    stop_worker,
)
from Data.Engine.services.job_scheduler.worker import (
    _reset_scheduled_run_for_retry,
    _transient_scheduled_run_retry_reason,
)


def _connect_queue_db(tmp_path: Path):
    conn = sqlite3.connect(str(tmp_path / "queue.sqlite3"))
    ensure_job_scheduler_tables(conn)
    conn.commit()
    return conn


def _seed_running_site_work(conn, *, worker_guid: str = "worker-1", site_id: int = 7) -> int:
    now = int(time.time())
    register_worker(
        conn,
        worker_guid=worker_guid,
        container_name=f"site-worker-{worker_guid}",
        site_id=site_id,
        status=WORKER_STATUS_RUNNING,
    )
    enqueue_scheduled_run(
        conn,
        job_id=90,
        run_id=9001,
        scheduled_ts=now,
        site_id=site_id,
        run_mode="system",
        script_components=[],
        ansible_components=[{"name": "Ansible Ping/Pong"}],
        credential_id=None,
    )
    conn.commit()
    item = claim_next_work_item(
        conn,
        site_id=site_id,
        lanes=[LANE_SCHEDULED_JOB],
        lease_owner=worker_guid,
        lease_seconds=3600,
    )
    assert item is not None
    assert item["status"] == WORK_STATUS_RUNNING
    return int(item["id"])


def _work_item_row(conn, work_id: int):
    cur = conn.cursor()
    cur.execute(
        """
        SELECT status, lease_owner, lease_expires_at, heartbeat_at, worker_guid, container_name, error
          FROM job_scheduler_work_items
         WHERE id=?
        """,
        (int(work_id),),
    )
    return cur.fetchone()


def _seed_transient_skipped_run(conn, *, run_id: int = 42) -> None:
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS scheduled_job_runs (
            id INTEGER PRIMARY KEY,
            status TEXT,
            finished_ts INTEGER,
            skip_reason TEXT,
            error TEXT,
            updated_at INTEGER
        )
        """
    )
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS scheduled_job_run_targets (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            run_id INTEGER,
            resolution_status TEXT,
            resolution_reason TEXT
        )
        """
    )
    conn.execute(
        """
        INSERT INTO scheduled_job_runs(id, status, finished_ts, skip_reason, error, updated_at)
        VALUES(?, ?, ?, ?, ?, ?)
        """,
        (
            int(run_id),
            job_scheduler.RUN_STATUS_SKIPPED,
            int(time.time()),
            job_scheduler.SKIP_REASON_NO_ELIGIBLE_TARGETS,
            "WireGuard session is unavailable for lab-host",
            int(time.time()),
        ),
    )
    conn.execute(
        """
        INSERT INTO scheduled_job_run_targets(run_id, resolution_status, resolution_reason)
        VALUES(?, ?, ?)
        """,
        (
            int(run_id),
            job_scheduler.RESOLUTION_STATUS_SKIPPED,
            "wireguard_unavailable",
        ),
    )


def test_stop_worker_requeues_running_work_item(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        work_id = _seed_running_site_work(conn, worker_guid="worker-stop")

        stop_worker(conn, worker_guid="worker-stop")
        conn.commit()

        row = _work_item_row(conn, work_id)
        assert row[0] == WORK_STATUS_QUEUED
        assert row[1] is None
        assert row[2] is None
        assert row[3] is None
        assert row[4] is None
        assert row[5] is None
        assert "site worker stopped" in row[6]
    finally:
        conn.close()


def test_mark_missing_worker_requeues_running_work_item(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        work_id = _seed_running_site_work(conn, worker_guid="worker-missing")

        marked = mark_missing_workers_lost(conn, live_worker_guids=[])
        conn.commit()

        assert marked == 1
        worker_row = conn.execute(
            "SELECT status, docker_state FROM job_scheduler_workers WHERE worker_guid=?",
            ("worker-missing",),
        ).fetchone()
        assert worker_row == (WORKER_STATUS_LOST, "missing")
        row = _work_item_row(conn, work_id)
        assert row[0] == WORK_STATUS_QUEUED
        assert row[1] is None
        assert row[4] is None
        assert "container disappeared" in row[6]
    finally:
        conn.close()


def test_expire_stale_leases_requeues_terminal_worker_item_before_timeout(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        work_id = _seed_running_site_work(conn, worker_guid="worker-terminal")
        conn.execute(
            "UPDATE job_scheduler_workers SET status=?, updated_at=? WHERE worker_guid=?",
            (WORKER_STATUS_STOPPED, int(time.time()), "worker-terminal"),
        )
        conn.commit()

        released = expire_stale_leases(conn)
        conn.commit()

        assert released == 1
        row = _work_item_row(conn, work_id)
        assert row[0] == WORK_STATUS_QUEUED
        assert row[1] is None
        assert row[2] is None
        assert row[4] is None
        assert "site worker stopped" in row[6]
    finally:
        conn.close()


def test_requeue_work_item_preserves_attempt_count_and_clears_lease(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        work_id = _seed_running_site_work(conn, worker_guid="worker-retry")
        conn.execute(
            "UPDATE job_scheduler_work_items SET finished_at=? WHERE id=?",
            (int(time.time()), work_id),
        )
        conn.commit()

        requeue_work_item(conn, work_id=work_id, delay_seconds=30, error="transient wireguard preparation")
        conn.commit()

        row = conn.execute(
            """
            SELECT status, lease_owner, lease_expires_at, heartbeat_at, worker_guid, container_name,
                   attempt_count, finished_at, error, available_at, updated_at
              FROM job_scheduler_work_items
             WHERE id=?
            """,
            (work_id,),
        ).fetchone()
        assert row[0] == WORK_STATUS_QUEUED
        assert row[1] is None
        assert row[2] is None
        assert row[3] is None
        assert row[4] is None
        assert row[5] is None
        assert row[6] == 1
        assert row[7] is None
        assert row[8] == "transient wireguard preparation"
        assert int(row[9]) >= int(row[10])
    finally:
        conn.close()


def test_transient_scheduled_run_retry_reason_detects_wireguard_skip(tmp_path: Path, monkeypatch) -> None:
    db_path = tmp_path / "queue.sqlite3"
    conn = _connect_queue_db(tmp_path)
    try:
        _seed_transient_skipped_run(conn, run_id=42)
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setenv("BOREALIS_SITE_WORKER_TRANSIENT_RUN_RETRY_ATTEMPTS", "3")
    db_factory = lambda: sqlite3.connect(str(db_path))
    reason = _transient_scheduled_run_retry_reason(db_factory, run_id=42, attempt_count=1)
    exhausted = _transient_scheduled_run_retry_reason(db_factory, run_id=42, attempt_count=3)

    assert reason == "wireguard_unavailable"
    assert exhausted == ""


def test_reset_scheduled_run_for_retry_returns_transient_targets_to_pending(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        _seed_transient_skipped_run(conn, run_id=43)
        conn.commit()

        _reset_scheduled_run_for_retry(conn, run_id=43, reason="wireguard_unavailable")
        conn.commit()

        run_row = conn.execute(
            "SELECT status, finished_ts, skip_reason, error FROM scheduled_job_runs WHERE id=?",
            (43,),
        ).fetchone()
        target_row = conn.execute(
            "SELECT resolution_status, resolution_reason FROM scheduled_job_run_targets WHERE run_id=?",
            (43,),
        ).fetchone()

        assert run_row[0] == job_scheduler.RUN_STATUS_PENDING
        assert run_row[1] is None
        assert run_row[2] == ""
        assert "Retrying after transient worker preparation failure" in run_row[3]
        assert target_row == (job_scheduler.RESOLUTION_STATUS_PENDING, "")
    finally:
        conn.close()
