from __future__ import annotations

import time
from pathlib import Path

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.API.scheduled_jobs import job_scheduler
from Data.Engine.services.job_scheduler import runtime_settings as site_worker_runtime_settings
from Data.Engine.services.job_scheduler.queue import (
    LANE_SCHEDULED_JOB,
    WORK_KIND_AGENT_MAINTENANCE_RUN,
    WORK_KIND_SCHEDULED_RUN,
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
    enqueue_scheduled_run,
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
from Data.Engine.services.job_scheduler.worker import (
    _fail_scheduled_run_after_transient_retries,
    _reset_scheduled_run_for_retry,
    _site_worker_scheduled_concurrency,
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


def test_claim_next_work_item_filters_kinds_for_site_worker(tmp_path: Path) -> None:
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
        enqueue_scheduled_run(
            conn,
            job_id=92,
            run_id=9201,
            scheduled_ts=now,
            site_id=7,
            run_mode="system",
            script_components=[{"name": "Script"}],
            ansible_components=[],
            credential_id=None,
        )
        conn.commit()

        item = claim_next_work_item(
            conn,
            site_id=7,
            lanes=[LANE_SCHEDULED_JOB],
            lease_owner="worker-1",
            kinds=[WORK_KIND_SCHEDULED_RUN],
        )
        assert item is not None
        assert item["kind"] == WORK_KIND_SCHEDULED_RUN

        cur = conn.cursor()
        cur.execute("SELECT status, kind FROM job_scheduler_work_items WHERE id=?", (maintenance_id,))
        assert cur.fetchone() == (WORK_STATUS_QUEUED, WORK_KIND_AGENT_MAINTENANCE_RUN)
    finally:
        conn.close()


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


def test_site_worker_scheduled_concurrency_uses_config_env_and_default(tmp_path: Path, monkeypatch) -> None:
    settings_path = tmp_path / "site_worker_settings.json"
    monkeypatch.setenv(site_worker_runtime_settings.SITE_WORKER_SETTINGS_PATH_ENV, str(settings_path))
    monkeypatch.delenv(site_worker_runtime_settings.SITE_WORKER_SCHEDULED_CONCURRENCY_ENV, raising=False)

    assert _site_worker_scheduled_concurrency() == 5

    site_worker_runtime_settings.save_site_worker_settings(
        {"scheduled_task_concurrency_limit": 7},
        settings_path,
    )
    assert _site_worker_scheduled_concurrency() == 7

    monkeypatch.setenv(site_worker_runtime_settings.SITE_WORKER_SCHEDULED_CONCURRENCY_ENV, "9")
    assert _site_worker_scheduled_concurrency() == 9

    monkeypatch.setenv(site_worker_runtime_settings.SITE_WORKER_SCHEDULED_CONCURRENCY_ENV, "99")
    assert _site_worker_scheduled_concurrency() == 32

    monkeypatch.setenv(site_worker_runtime_settings.SITE_WORKER_SCHEDULED_CONCURRENCY_ENV, "0")
    assert _site_worker_scheduled_concurrency() == 1


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


def test_fail_scheduled_run_after_transient_retries_marks_run_failed(tmp_path: Path, monkeypatch) -> None:
    conn = _connect_queue_db(tmp_path)
    try:
        _seed_transient_skipped_run(conn, run_id=44)
        conn.commit()

        monkeypatch.setenv("BOREALIS_SITE_WORKER_TRANSIENT_RUN_RETRY_ATTEMPTS", "3")
        message = _fail_scheduled_run_after_transient_retries(
            conn,
            run_id=44,
            reason="wireguard_unavailable",
            attempt_count=3,
        )
        conn.commit()

        run_row = conn.execute(
            "SELECT status, skip_reason, error FROM scheduled_job_runs WHERE id=?",
            (44,),
        ).fetchone()
        target_row = conn.execute(
            "SELECT resolution_status, resolution_reason FROM scheduled_job_run_targets WHERE run_id=?",
            (44,),
        ).fetchone()

        assert "Transient worker preparation failed after 3 attempts" in message
        assert run_row[0] == job_scheduler.RUN_STATUS_FAILED
        assert run_row[1] == ""
        assert "Transient worker preparation failed after 3 attempts" in run_row[2]
        assert target_row == (job_scheduler.RESOLUTION_STATUS_UNRESOLVED, "wireguard_unavailable")
    finally:
        conn.close()
