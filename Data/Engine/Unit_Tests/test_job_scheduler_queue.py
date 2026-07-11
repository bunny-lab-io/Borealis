from __future__ import annotations

import inspect
import logging
import time
from pathlib import Path

import pytest

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.job_scheduler import worker as site_worker
from Data.Engine.services.job_scheduler.queue import (
    LANE_SCHEDULED_JOB,
    WORK_KIND_AGENT_MAINTENANCE_RUN,
    WORK_KIND_ONBOARDING_RUN,
    WORK_KIND_SCHEDULED_RUN,
    WORK_KIND_SCHEDULED_WORKFLOW_RUN,
    WORK_STATUS_QUEUED,
    WORK_STATUS_RUNNING,
    WORKER_ROUTE_STATUS_ACTIVE,
    WORKER_ROUTE_STATUS_LOST,
    WORKER_ROUTE_STATUS_RETIRED,
    WORKER_STATUS_LOST,
    WORKER_STATUS_RUNNING,
    WORKER_STATUS_STOPPED,
    active_worker_route_for_site,
    claim_next_work_item,
    enqueue_agent_maintenance_run,
    enqueue_onboarding_run,
    enqueue_scheduled_run,
    enqueue_scheduled_workflow_run,
    ensure_job_scheduler_tables,
    expire_stale_leases,
    list_worker_routes,
    mark_missing_workers_lost,
    prune_worker_history,
    register_worker,
    requeue_work_item,
    site_worker_remote_desktop_port,
    site_worker_remote_ops_port,
    stop_worker,
    upsert_worker_route,
    worker_route_for_worker,
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


def test_site_worker_source_excludes_go_owned_work_item_claims(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    now = int(time.time())
    try:
        maintenance_id = enqueue_agent_maintenance_run(
            conn,
            job_id=91,
            run_id=9101,
            scheduled_ts=now,
            site_id=7,
            hostname="LAB-OPERATOR-01",
            operation_id="op-filtered",
            action="update_now",
            release_channel="stable",
            branch="main",
            event_payload={"operation_id": "op-filtered"},
        )
        workflow_id = enqueue_scheduled_workflow_run(
            conn,
            job_id=92,
            run_id=9201,
            scheduled_ts=now,
            site_id=7,
            workflow_component={"assembly_guid": "wf-123", "name": "Workflow"},
        )
        scheduled_id = enqueue_scheduled_run(
            conn,
            job_id=93,
            run_id=9301,
            scheduled_ts=now,
            site_id=7,
            run_mode="system",
            script_components=[{"name": "Script"}],
            ansible_components=[],
            credential_id=None,
        )
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_runs (
                id INTEGER PRIMARY KEY,
                status TEXT,
                started_ts INTEGER,
                updated_at INTEGER
            )
            """
        )
        conn.execute(
            "INSERT INTO scheduled_job_runs(id, status, started_ts, updated_at) VALUES(?, ?, ?, ?)",
            (9401, "Pending", None, now),
        )
        enqueue_onboarding_run(
            conn,
            job_id=94,
            run_id=9401,
            scheduled_ts=now,
            site_id=7,
            components=[],
            targets=[],
            credential_id=None,
        )
        conn.commit()

        worker_source = inspect.getsource(site_worker)
        assert "claim_next_work_item" not in worker_source
        assert "WORK_KIND_ONBOARDING_RUN" not in worker_source
        assert "_run_onboarding_item" not in worker_source
        assert "_build_worker_scheduler" not in worker_source

        cur = conn.cursor()
        cur.execute("SELECT status, kind FROM job_scheduler_work_items WHERE id=?", (maintenance_id,))
        assert cur.fetchone() == (WORK_STATUS_QUEUED, WORK_KIND_AGENT_MAINTENANCE_RUN)
        cur.execute("SELECT status, kind FROM job_scheduler_work_items WHERE id=?", (workflow_id,))
        assert cur.fetchone() == (WORK_STATUS_QUEUED, WORK_KIND_SCHEDULED_WORKFLOW_RUN)
        cur.execute("SELECT status, kind FROM job_scheduler_work_items WHERE id=?", (scheduled_id,))
        assert cur.fetchone() == (WORK_STATUS_QUEUED, WORK_KIND_SCHEDULED_RUN)
    finally:
        conn.close()


class _HeartbeatFakeConn:
    def __init__(self) -> None:
        self.commits = 0
        self.rollbacks = 0

    def commit(self) -> None:
        self.commits += 1

    def rollback(self) -> None:
        self.rollbacks += 1


def test_site_worker_heartbeat_retries_transient_db_deadlock(monkeypatch) -> None:
    conn = _HeartbeatFakeConn()
    calls = []
    sleeps = []

    def fake_heartbeat(_conn, **kwargs):
        calls.append(kwargs)
        if len(calls) == 1:
            raise sqlite3.Error("deadlock detected")

    monkeypatch.setattr(site_worker, "heartbeat_worker", fake_heartbeat)

    site_worker._commit_worker_heartbeat(
        logging.getLogger("test.site_worker"),
        conn,
        worker_guid="worker-deadlock-retry",
        status=WORKER_STATUS_RUNNING,
        lanes=[site_worker.LANE_AGENT_SOCKETS],
        task_links=[],
        idle_since=None,
        claimed_count=0,
        sleep_fn=sleeps.append,
    )

    assert len(calls) == 2
    assert conn.rollbacks == 1
    assert conn.commits == 1
    assert sleeps == [0.25]


def test_site_worker_heartbeat_does_not_retry_non_transient_db_error(monkeypatch) -> None:
    conn = _HeartbeatFakeConn()
    calls = []

    def fake_heartbeat(_conn, **kwargs):
        calls.append(kwargs)
        raise sqlite3.Error("syntax error near worker heartbeat")

    monkeypatch.setattr(site_worker, "heartbeat_worker", fake_heartbeat)

    with pytest.raises(sqlite3.Error, match="syntax error"):
        site_worker._commit_worker_heartbeat(
            logging.getLogger("test.site_worker"),
            conn,
            worker_guid="worker-fatal-error",
            status=WORKER_STATUS_RUNNING,
            lanes=[site_worker.LANE_AGENT_SOCKETS],
            task_links=[],
            idle_since=None,
            claimed_count=0,
            sleep_fn=lambda _seconds: None,
        )

    assert len(calls) == 1
    assert conn.rollbacks == 1
    assert conn.commits == 0


def test_register_worker_creates_active_route_record(tmp_path: Path, monkeypatch) -> None:
    dynamic_dir = tmp_path / "dynamic"
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(dynamic_dir))
    conn = _connect_queue_db(tmp_path)
    try:
        register_worker(
            conn,
            worker_guid="worker-registry",
            container_name="site-worker-worker-registry",
            site_id=7,
            status=WORKER_STATUS_RUNNING,
        )
        conn.commit()

        route = worker_route_for_worker(conn, worker_guid="worker-registry")
        assert route is not None
        assert route["worker_guid"] == "worker-registry"
        assert route["site_id"] == 7
        assert route["container_name"] == "site-worker-worker-registry"
        assert route["status"] == WORKER_ROUTE_STATUS_ACTIVE
        assert route["generation"] == 1
        assert route["route_name"] == "borealis-site-worker-worker-registry"
        assert route["route_path_prefix"] == "/_borealis/site-workers/worker-registry"
        assert route["route_file_path"] == str(dynamic_dir / "site-worker-worker-registry.yml")
        assert route["upstream_scheme"] == "http"
        assert route["upstream_host"] == "127.0.0.1"
        assert route["upstream_port"] == 0
        assert route["metadata"]["lifecycle_owner"] == "job-scheduler"
        assert route["metadata"]["route_kind"] == "site_worker"

        active = active_worker_route_for_site(conn, site_id=7)
        assert active is not None
        assert active["worker_guid"] == "worker-registry"
    finally:
        conn.close()


def test_worker_route_upsert_updates_generation_only_when_metadata_changes(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(tmp_path / "dynamic"))
    conn = _connect_queue_db(tmp_path)
    try:
        first = upsert_worker_route(
            conn,
            worker_guid="worker-upsert",
            container_name="site-worker-worker-upsert",
            site_id=12,
        )
        same = upsert_worker_route(
            conn,
            worker_guid="worker-upsert",
            container_name="site-worker-worker-upsert",
            site_id=12,
        )
        changed = upsert_worker_route(
            conn,
            worker_guid="worker-upsert",
            container_name="site-worker-worker-upsert-v2",
            site_id=12,
            upstream_port=8123,
            metadata={"listener": "remote-op"},
        )
        conn.commit()

        assert first is not None
        assert same is not None
        assert changed is not None
        assert first["generation"] == 1
        assert same["generation"] == 1
        assert changed["generation"] == 2
        assert changed["container_name"] == "site-worker-worker-upsert-v2"
        assert changed["upstream_port"] == 8123
        assert changed["metadata"]["listener"] == "remote-op"

        routes = list_worker_routes(conn, site_id=12, statuses=[WORKER_ROUTE_STATUS_ACTIVE])
        assert [route["worker_guid"] for route in routes] == ["worker-upsert"]
    finally:
        conn.close()


def test_new_active_worker_route_retires_older_same_site_route(tmp_path: Path, monkeypatch) -> None:
    dynamic_dir = tmp_path / "dynamic"
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(dynamic_dir))
    conn = _connect_queue_db(tmp_path)
    try:
        old_route = upsert_worker_route(
            conn,
            worker_guid="worker-old-route",
            container_name="site-worker-worker-old-route",
            site_id=12,
            upstream_port=58111,
        )
        assert old_route is not None
        old_route_file = Path(str(old_route["route_file_path"]))
        assert old_route_file.exists()

        new_route = upsert_worker_route(
            conn,
            worker_guid="worker-new-route",
            container_name="site-worker-worker-new-route",
            site_id=12,
            upstream_port=58112,
        )
        conn.commit()

        assert new_route is not None
        active_routes = list_worker_routes(conn, site_id=12, statuses=[WORKER_ROUTE_STATUS_ACTIVE])
        assert [route["worker_guid"] for route in active_routes] == ["worker-new-route"]
        retired_route = worker_route_for_worker(conn, worker_guid="worker-old-route")
        assert retired_route is not None
        assert retired_route["status"] == WORKER_ROUTE_STATUS_RETIRED
        assert retired_route["retired_at"] is not None
        assert not old_route_file.exists()
        assert Path(str(new_route["route_file_path"])).exists()
    finally:
        conn.close()


def test_worker_route_upsert_writes_and_removes_traefik_route_file(tmp_path: Path, monkeypatch) -> None:
    dynamic_dir = tmp_path / "dynamic"
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(dynamic_dir))
    monkeypatch.setenv("BOREALIS_PUBLIC_HOSTNAME", "engine.example.test")
    monkeypatch.setenv("BOREALIS_PUBLIC_HOSTNAME_ALIASES", "engine.example.test,alias.example.test")
    conn = _connect_queue_db(tmp_path)
    try:
        route = upsert_worker_route(
            conn,
            worker_guid="worker-route-file",
            container_name="site-worker-worker-route-file",
            site_id=12,
            upstream_port=58123,
            metadata={
                "listener": "remote-op",
                "remote_desktop_guacamole": {
                    "host": "127.0.0.1",
                    "scheme": "http",
                    "path_prefix": "/remote-desktop/vnc",
                    "port": 61234,
                },
            },
        )
        conn.commit()

        assert route is not None
        route_file = Path(str(route["route_file_path"]))
        assert route_file.exists()
        route_text = route_file.read_text(encoding="utf-8")
        assert "borealis-site-worker-worker-route-file" in route_text
        assert "Host(`engine.example.test`,`alias.example.test`)" in route_text
        assert "PathPrefix(`/_borealis/site-workers/worker-route-file`)" in route_text
        assert "PathPrefix(`/_borealis/site-workers/worker-route-file/remote-desktop/vnc`)" in route_text
        assert 'url: "http://127.0.0.1:58123"' in route_text
        assert 'url: "http://127.0.0.1:61234"' in route_text
        assert "stripPrefix:" in route_text

        stop_worker(conn, worker_guid="worker-route-file")
        conn.commit()

        assert not route_file.exists()
    finally:
        conn.close()


def test_site_worker_remote_ops_port_is_deterministic(monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_BASE", "57000")
    monkeypatch.setenv("BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_RANGE", "100")

    first = site_worker_remote_ops_port("worker-port", 4)
    second = site_worker_remote_ops_port("worker-port", 4)
    other = site_worker_remote_ops_port("worker-port-other", 4)

    assert first == second
    assert 57000 <= first < 57100
    assert 57000 <= other < 57100


def test_site_worker_remote_desktop_port_is_deterministic(monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_BASE", "62000")
    monkeypatch.setenv("BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_RANGE", "100")

    first = site_worker_remote_desktop_port("worker-port", 4)
    second = site_worker_remote_desktop_port("worker-port", 4)
    other = site_worker_remote_desktop_port("worker-port-other", 4)

    assert first == second
    assert 62000 <= first < 62100
    assert 62000 <= other < 62100


def test_worker_route_upsert_recovers_missing_registry_row_for_live_worker(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(tmp_path / "dynamic"))
    conn = _connect_queue_db(tmp_path)
    try:
        register_worker(
            conn,
            worker_guid="worker-recover",
            container_name="site-worker-worker-recover",
            site_id=15,
            status=WORKER_STATUS_RUNNING,
        )
        conn.execute("DELETE FROM job_scheduler_worker_routes WHERE worker_guid=?", ("worker-recover",))
        conn.commit()

        assert worker_route_for_worker(conn, worker_guid="worker-recover") is None

        recovered = upsert_worker_route(
            conn,
            worker_guid="worker-recover",
            container_name="site-worker-worker-recover",
            site_id=15,
        )
        conn.commit()

        assert recovered is not None
        assert recovered["status"] == WORKER_ROUTE_STATUS_ACTIVE
        assert recovered["generation"] == 1
        assert recovered["route_file_path"].endswith("site-worker-worker-recover.yml")
    finally:
        conn.close()


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
        route = worker_route_for_worker(conn, worker_guid="worker-stop")
        assert route is not None
        assert route["status"] == WORKER_ROUTE_STATUS_RETIRED
        assert route["retired_at"] is not None
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
        route = worker_route_for_worker(conn, worker_guid="worker-missing")
        assert route is not None
        assert route["status"] == WORKER_ROUTE_STATUS_LOST
        assert route["retired_at"] is not None
    finally:
        conn.close()


def test_prune_worker_history_removes_old_terminal_route_records(tmp_path: Path) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        register_worker(
            conn,
            worker_guid="worker-prune",
            container_name="site-worker-worker-prune",
            site_id=17,
            status=WORKER_STATUS_RUNNING,
        )
        stop_worker(conn, worker_guid="worker-prune")
        old_ts = int(time.time()) - 300
        conn.execute(
            """
            UPDATE job_scheduler_workers
               SET stopped_at=?, last_seen_at=?, updated_at=?
             WHERE worker_guid=?
            """,
            (old_ts, old_ts, old_ts, "worker-prune"),
        )
        conn.execute(
            """
            UPDATE job_scheduler_worker_routes
               SET retired_at=?, updated_at=?
             WHERE worker_guid=?
            """,
            (old_ts, old_ts, "worker-prune"),
        )
        conn.commit()

        deleted = prune_worker_history(conn, retention_seconds=60)
        conn.commit()

        assert deleted == 1
        assert worker_route_for_worker(conn, worker_guid="worker-prune") is None
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
