from __future__ import annotations

import time
from pathlib import Path

from Data.Engine.db import dbapi as sqlite3
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
    stop_worker,
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
