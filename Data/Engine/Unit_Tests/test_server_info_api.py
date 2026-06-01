# ======================================================
# Data\Engine\Unit_Tests\test_server_info_api.py
# Description: Covers Server Info admin API behavior, certificate parsing, and operator presence socket flows.
# ======================================================

from __future__ import annotations

import base64
import json
import sqlite3
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives.serialization import Encoding
from cryptography.x509.oid import NameOID

from Data.Engine.services.API.server import info as server_info_api
from Data.Engine.services.RemoteDesktop.vnc_sessions import ensure_vnc_collaboration_manager
from Data.Engine.services.ansible import runtime_settings as ansible_runtime_settings
from Data.Engine.services.job_scheduler.queue import ensure_job_scheduler_tables, prune_worker_history
from Data.Engine.services.WebSocket.__init__ import OperatorPresenceRegistry

from .conftest import EngineTestHarness


def _admin_client(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _user_client(harness: EngineTestHarness):
    conn = sqlite3.connect(harness.db_path)
    try:
        conn.execute(
            """
            INSERT OR IGNORE INTO users (username, display_name, password_sha512, role, last_login, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            ("operator", "Operator", "test", "User", 0, 0, 0),
        )
        conn.commit()
    finally:
        conn.close()
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "operator"
        sess["role"] = "User"
    return client


def _certificate_blob_base64(common_name: str, *, days_valid: int) -> str:
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    now = datetime.now(timezone.utc)
    subject = issuer = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, common_name)])
    cert = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(issuer)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(days=1))
        .not_valid_after(now + timedelta(days=days_valid))
        .add_extension(x509.SubjectAlternativeName([x509.DNSName(common_name)]), critical=False)
        .sign(private_key=key, algorithm=hashes.SHA256())
    )
    return base64.b64encode(cert.public_bytes(Encoding.PEM)).decode("ascii")


def test_server_overview_requires_admin(engine_harness: EngineTestHarness) -> None:
    anonymous_client = engine_harness.app.test_client()
    response = anonymous_client.get("/api/server/overview")
    assert response.status_code == 401

    operator_client = _user_client(engine_harness)
    operator_response = operator_client.get("/api/server/overview")
    assert operator_response.status_code == 403


def test_server_timezones_requires_admin(engine_harness: EngineTestHarness) -> None:
    anonymous_client = engine_harness.app.test_client()
    response = anonymous_client.get("/api/server/timezones")
    assert response.status_code == 401

    operator_client = _user_client(engine_harness)
    operator_response = operator_client.post("/api/server/timezone", json={"timezone": "UTC"})
    assert operator_response.status_code == 403


def test_server_timezones_lists_options(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    monkeypatch.setattr(server_info_api, "_current_timezone_id", lambda: "America/Denver")
    monkeypatch.setattr(server_info_api, "_timezone_change_supported", lambda: True)
    monkeypatch.setattr(server_info_api, "_list_available_timezones", lambda: ["America/Denver", "UTC"])

    response = client.get("/api/server/timezones")
    assert response.status_code == 200
    payload = response.get_json()

    assert payload["current_timezone"] == "America/Denver"
    assert payload["change_supported"] is True
    assert payload["timezones"] == ["America/Denver", "UTC"]


def test_set_server_timezone_validates_and_changes(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    monkeypatch.setattr(server_info_api, "_list_available_timezones", lambda: ["UTC", "America/Denver"])
    monkeypatch.setattr(server_info_api, "_timezone_change_supported", lambda: True)

    missing = client.post("/api/server/timezone", json={})
    assert missing.status_code == 400

    invalid = client.post("/api/server/timezone", json={"timezone": "Mars/Olympus"})
    assert invalid.status_code == 400

    calls = {}

    def _fake_set_system_timezone(timezone_id: str):
        calls["timezone"] = timezone_id
        return True, ""

    monkeypatch.setattr(server_info_api, "_set_system_timezone", _fake_set_system_timezone)
    monkeypatch.setattr(
        server_info_api,
        "_collect_host_payload",
        lambda _context: {
            "timezone": "MDT",
            "timezone_id": "America/Denver",
            "timezone_change_supported": True,
            "server_time": {
                "display": "2026-04-05 02:55:00 MDT",
                "timezone": "MDT",
                "timezone_id": "America/Denver",
            },
        },
    )

    response = client.post("/api/server/timezone", json={"timezone": "America/Denver"})
    assert response.status_code == 200
    payload = response.get_json()

    assert payload["status"] == "ok"
    assert payload["timezone"] == "America/Denver"
    assert payload["host"]["timezone_id"] == "America/Denver"
    assert calls["timezone"] == "America/Denver"


def test_server_overview_includes_operator_session_count(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    registry = OperatorPresenceRegistry()
    registry.register_or_update(
        "sid-admin",
        username="admin",
        role="Admin",
        current_page="Server Info",
    )
    engine_harness.context.operator_presence_registry = registry

    monkeypatch.setattr(
        server_info_api,
        "_collect_service_rows",
        lambda _context: [
            {
                "key": "borealis_engine",
                "label": "Borealis Engine",
                "unit_name": "borealis-engine.service",
                "status": "healthy",
                "active_state": "active",
                "sub_state": "running",
                "enabled_state": "enabled",
                "main_pid": 100,
                "started_at": "2026-04-05T00:00:00+00:00",
                "fragment_path": "/etc/systemd/system/borealis-engine.service",
                "restart_supported": True,
                "pending_action": None,
            }
        ],
    )
    monkeypatch.setattr(
        server_info_api,
        "_collect_wireguard_payload",
        lambda _adapters: {
            "interface_name": "borealis-wg",
            "interface_present": True,
            "interface_up": True,
            "active_tunnel_count": 0,
            "listener_healthy": False,
            "listener_reason": "idle",
            "listener_service_state": "RUNNING",
            "recovery_in_progress": False,
            "last_recovery_attempt_at": None,
            "last_recovery_attempt_at_iso": "",
            "shell_port": 47002,
            "vnc_port": 5900,
            "vnc_ws_port": 4823,
            "wireguard_endpoint": {"host": "borealis.example.com", "port": 30000, "display": "borealis.example.com:30000"},
            "recover_supported": False,
            "active_tunnels": [],
        },
    )
    monkeypatch.setattr(
        server_info_api,
        "_collect_public_edge_payload",
        lambda _context: {
            "enabled": True,
            "fqdn": "borealis.example.com",
            "acme_email": "ops@example.com",
            "public_base_url": "https://borealis.example.com",
            "public_vnc_path": "/remote-desktop/vnc",
            "wireguard_endpoint": "borealis.example.com:30000",
            "certificates": [],
        },
    )

    response = client.get("/api/server/overview")
    assert response.status_code == 200
    payload = response.get_json()

    assert payload["operator_session_count"] == 1
    assert payload["security"]["aegis"]["unlock_scope"] == "engine_global"
    assert payload["services"][0]["unit_name"] == "borealis-engine.service"


def test_server_workers_includes_recent_terminal_work(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    now = int(time.time())
    conn = sqlite3.connect(engine_harness.db_path)
    try:
        ensure_job_scheduler_tables(conn)
        cur = conn.cursor()
        cur.execute(
            "INSERT OR IGNORE INTO sites(id, name, description, created_at) VALUES (?,?,?,?)",
            (7, "Bunny Lab", "", now),
        )
        cur.execute(
            """
            INSERT INTO job_scheduler_workers(
                worker_guid, container_name, site_id, status, started_at, last_seen_at,
                current_lanes_json, claimed_count, task_links_json, created_at, updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "worker-1",
                "site-worker-worker-1",
                7,
                "stopped",
                now - 120,
                now - 10,
                json.dumps(["scheduled_job"]),
                1,
                "[]",
                now - 120,
                now - 5,
            ),
        )
        cur.execute(
            """
            INSERT INTO job_scheduler_work_items(
                dedupe_key, kind, site_id, lane, job_id, run_id, target_id, payload_json,
                status, attempt_count, priority, available_at, lease_owner, worker_guid,
                created_at, updated_at, started_at, finished_at, error
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "scheduled-run:55",
                "scheduled_run",
                7,
                "scheduled_job",
                55,
                88,
                None,
                json.dumps(
                    {
                        "target_row_ids": [101, 102],
                        "task_link": {"label": "Scheduled Job 55", "path": "/jobs/55?tab=job_history"},
                    }
                ),
                "succeeded",
                1,
                0,
                now - 80,
                "worker-1",
                "worker-1",
                now - 80,
                now - 20,
                now - 75,
                now - 20,
                "",
            ),
        )
        cur.execute(
            """
            INSERT INTO job_scheduler_work_items(
                dedupe_key, kind, site_id, lane, job_id, run_id, target_id, payload_json,
                status, attempt_count, priority, available_at, created_at, updated_at, finished_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "scheduled-run:old",
                "scheduled_run",
                7,
                "scheduled_job",
                56,
                89,
                None,
                "{}",
                "failed",
                1,
                0,
                now - 5000,
                now - 5000,
                now - 5000,
                now - 5000,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    response = client.get("/api/server/workers?history_seconds=600")

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["worker_idle_ttl_seconds"] == 60
    recent_ids = {row["id"] for row in payload["recent_work"]}
    assert len(recent_ids) == 1
    recent = payload["recent_work"][0]
    assert recent["kind"] == "scheduled_run"
    assert recent["status"] == "succeeded"
    assert recent["site_name"] == "Bunny Lab"
    assert recent["target_count"] == 2
    assert recent["task_type"] == "Assembly"
    assert recent["task_link"]["path"] == "/jobs/55?tab=job_history"


def test_server_workers_includes_short_container_id(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    now = int(time.time())
    conn = sqlite3.connect(engine_harness.db_path)
    try:
        ensure_job_scheduler_tables(conn)
        conn.execute(
            """
            INSERT INTO job_scheduler_workers(
                worker_guid, container_name, site_id, status, started_at, last_seen_at,
                current_lanes_json, claimed_count, task_links_json, created_at, updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "worker-container-id",
                "site-worker-worker-container-id",
                7,
                "running",
                now - 60,
                now,
                "[]",
                0,
                "[]",
                now - 60,
                now,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(
        server_info_api,
        "_docker_inspect_container",
        lambda _name: {
            "Id": "fca35d6fd0d6b7d37d62c0d16f47f941d4e49ee644b19a2664898403c30e5efc",
            "Config": {"Image": "borealis-engine/site-worker:test"},
        },
    )

    response = client.get("/api/server/workers?history_seconds=60")

    assert response.status_code == 200
    worker = response.get_json()["workers"][0]
    assert worker["container_id"] == "fca35d6fd0d6"
    assert worker["container_id_full"].startswith("fca35d6fd0d6")
    assert worker["container_image"] == "borealis-engine/site-worker:test"


def test_recreate_site_worker_queues_scheduler_action(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    now = int(time.time())
    conn = sqlite3.connect(engine_harness.db_path)
    try:
        ensure_job_scheduler_tables(conn)
        conn.execute(
            """
            INSERT INTO job_scheduler_workers(
                worker_guid, container_name, site_id, status, started_at, last_seen_at,
                current_lanes_json, claimed_count, task_links_json, created_at, updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "worker-recreate",
                "site-worker-worker-recreate",
                7,
                "running",
                now - 60,
                now,
                "[]",
                0,
                "[]",
                now - 60,
                now,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    response = client.post("/api/server/workers/worker-recreate/recreate", json={})

    assert response.status_code == 202
    payload = response.get_json()
    assert payload["queued"] is True
    assert payload["worker_guid"] == "worker-recreate"
    assert payload["container_name"] == "site-worker-worker-recreate"

    conn = sqlite3.connect(engine_harness.db_path)
    try:
        row = conn.execute(
            """
            SELECT payload_json
              FROM job_scheduler_work_items
             WHERE kind='service_action'
             ORDER BY id DESC
             LIMIT 1
            """
        ).fetchone()
    finally:
        conn.close()
    assert row is not None
    action_payload = json.loads(row[0])
    assert action_payload["service_key"] == "site-worker"
    assert action_payload["action"]["action"] == "recreate"
    assert action_payload["action"]["worker_guid"] == "worker-recreate"
    assert action_payload["action"]["container_name"] == "site-worker-worker-recreate"


def test_worker_history_prunes_old_terminal_site_workers(engine_harness: EngineTestHarness) -> None:
    now = int(time.time())
    conn = sqlite3.connect(engine_harness.db_path)
    try:
        ensure_job_scheduler_tables(conn)
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO job_scheduler_workers(
                worker_guid, container_name, site_id, status, started_at, last_seen_at,
                current_lanes_json, claimed_count, task_links_json, created_at, updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                "old-worker",
                "site-worker-old",
                4,
                "stopped",
                now - 7200,
                now - 7200,
                "[]",
                1,
                "[]",
                now - 7200,
                now - 7200,
            ),
        )
        deleted = prune_worker_history(conn, retention_seconds=600)
        conn.commit()
        assert deleted == 1
        cur.execute("SELECT COUNT(*) FROM job_scheduler_workers WHERE worker_guid='old-worker'")
        assert int(cur.fetchone()[0]) == 0
    finally:
        conn.close()


def test_server_overview_includes_active_vnc_sessions(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    manager = ensure_vnc_collaboration_manager(engine_harness.context, logger=engine_harness.context.logger)
    session, _participant, _created = manager.ensure_session(
        agent_id="agent-1",
        operator_id="admin",
        controller_password="bootpass",
        credential_revision=42,
        remove_wallpaper=True,
    )
    manager.record_backend_ready(
        session.session_id,
        tunnel_id="tun-vnc-1",
        allowed_ips="10.255.0.1/32",
        engine_virtual_ip="10.255.0.1/32",
    )

    monkeypatch.setattr(server_info_api, "_collect_service_rows", lambda _context: [])
    monkeypatch.setattr(
        server_info_api,
        "_collect_wireguard_payload",
        lambda _adapters: {
            "interface_name": "borealis-wg",
            "interface_present": True,
            "interface_up": True,
            "active_tunnel_count": 1,
            "listener_healthy": True,
            "listener_reason": "",
            "listener_service_state": "RUNNING",
            "recovery_in_progress": False,
            "last_recovery_attempt_at": None,
            "last_recovery_attempt_at_iso": "",
            "shell_port": 47002,
            "vnc_port": 5900,
            "vnc_ws_port": 4823,
            "wireguard_endpoint": {"host": "borealis.example.com", "port": 30000, "display": "borealis.example.com:30000"},
            "recover_supported": True,
            "active_tunnels": [],
        },
    )
    monkeypatch.setattr(
        server_info_api,
        "_collect_public_edge_payload",
        lambda _context: {
            "enabled": True,
            "fqdn": "borealis.example.com",
            "acme_email": "ops@example.com",
            "public_base_url": "https://borealis.example.com",
            "public_vnc_path": "/remote-desktop/vnc",
            "wireguard_endpoint": "borealis.example.com:30000",
            "certificates": [],
        },
    )

    response = client.get("/api/server/overview")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["remote_desktop"]["active_session_count"] == 1
    assert payload["remote_desktop"]["active_sessions"][0]["session_id"] == session.session_id
    assert payload["remote_desktop"]["active_sessions"][0]["controller_operator_id"] == "admin"


def test_server_ansible_runner_settings_round_trip(
    engine_harness: EngineTestHarness,
    monkeypatch,
    tmp_path,
) -> None:
    client = _admin_client(engine_harness)
    settings_path = tmp_path / "ansible_runner_settings.json"

    monkeypatch.setattr(
        server_info_api,
        "load_ansible_runner_settings",
        lambda: ansible_runtime_settings.load_ansible_runner_settings(settings_path),
    )
    monkeypatch.setattr(
        server_info_api,
        "save_ansible_runner_settings",
        lambda mapping: ansible_runtime_settings.save_ansible_runner_settings(mapping, settings_path),
    )

    initial_response = client.get("/api/server/ansible-runner-settings")
    assert initial_response.status_code == 200
    assert initial_response.get_json() == {
        "job_concurrency_limit": 5,
        "global_concurrency_limit": 50,
    }

    invalid_response = client.put(
        "/api/server/ansible-runner-settings",
        json={"job_concurrency_limit": 0, "global_concurrency_limit": 50},
    )
    assert invalid_response.status_code == 400
    invalid_payload = invalid_response.get_json()
    assert invalid_payload["error"] == "invalid_ansible_runner_settings"

    update_response = client.put(
        "/api/server/ansible-runner-settings",
        json={"job_concurrency_limit": 12, "global_concurrency_limit": 37},
    )
    assert update_response.status_code == 200
    update_payload = update_response.get_json()
    assert update_payload["ansible_runner"] == {
        "job_concurrency_limit": 12,
        "global_concurrency_limit": 37,
    }

    overview_response = client.get("/api/server/overview")
    assert overview_response.status_code == 200
    overview_payload = overview_response.get_json()
    assert overview_payload["ansible_runner"] == {
        "job_concurrency_limit": 12,
        "global_concurrency_limit": 37,
    }


def test_server_site_worker_settings_are_profile_managed_read_only(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _admin_client(engine_harness)
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY", "12")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_PROFILE", "MSP / Production")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "2")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_CPU_RANK", "2")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_MEMORY_RANK", "2")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_HOST_VCPU", "16")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_MIB", "32768")
    monkeypatch.setenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_GIB", "32.0")

    initial_response = client.get("/api/server/site-worker-settings")
    assert initial_response.status_code == 200
    initial_payload = initial_response.get_json()
    assert initial_payload["scheduled_task_concurrency_limit"] == 12
    assert initial_payload["max_scheduled_task_concurrency_limit"] == 32
    assert initial_payload["editable"] is False
    assert initial_payload["managed_by"] == "deployment_profile"
    assert initial_payload["deployment_profile"] == {
        "name": "MSP / Production",
        "rank": 2,
        "cpu_rank": 2,
        "memory_rank": 2,
        "host_vcpu": 16,
        "host_memory_mib": 32768,
        "host_memory_gib": "32.0",
    }

    update_response = client.put("/api/server/site-worker-settings", json={"scheduled_task_concurrency_limit": 7})
    assert update_response.status_code == 405

    overview_response = client.get("/api/server/overview")
    assert overview_response.status_code == 200
    overview_payload = overview_response.get_json()
    assert overview_payload["site_worker_settings"] == initial_payload
    assert overview_payload["host"]["deployment_profile"]["name"] == "MSP / Production"


def test_discover_postgresql_cluster_units_ignores_template(monkeypatch) -> None:
    outputs = iter(
        [
            (
                0,
                "postgresql@.service static\npostgresql@17-main.service enabled\n",
                "",
            ),
            (
                0,
                "postgresql@17-main.service loaded active running PostgreSQL Cluster 17-main\n",
                "",
            ),
        ]
    )

    monkeypatch.setattr(server_info_api, "_run_command", lambda *_args, **_kwargs: next(outputs))

    units = server_info_api._discover_postgresql_cluster_units("/usr/bin/systemctl")

    assert units == ["postgresql@17-main.service"]


def test_collect_service_rows_uses_compose_manifest(tmp_path: Path, engine_harness: EngineTestHarness, monkeypatch) -> None:
    compose_file = tmp_path / "Data" / "Engine" / "Containers" / "compose.yaml"
    env_file = tmp_path / "Engine" / "Deploy" / "compose.env"
    deploy_manifest = tmp_path / "Engine" / "Deploy" / "deploy-manifest.json"
    compose_file.parent.mkdir(parents=True)
    env_file.parent.mkdir(parents=True)
    compose_file.write_text("services: {}\n", encoding="utf-8")
    env_file.write_text("BOREALIS_COMPOSE_PROJECT_NAME=borealis-engine\n", encoding="utf-8")
    deploy_manifest.write_text("{}", encoding="utf-8")

    monkeypatch.setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
    monkeypatch.setenv("BOREALIS_PROJECT_ROOT", str(tmp_path))
    monkeypatch.setattr(server_info_api.shutil, "which", lambda name: "/usr/bin/docker" if name == "docker" else "")

    def _fake_run_command(args, **_kwargs):
        if args[:2] == ["/usr/bin/docker", "inspect"]:
            return 1, "", "not found"
        assert args[:2] == ["/usr/bin/docker", "compose"]
        return (
            0,
            json.dumps(
                [
                    {"Service": "api-backend", "Name": "borealis-engine-api-backend", "State": "running", "Health": "healthy"},
                    {"Service": "webui-frontend", "Name": "borealis-engine-webui-frontend", "State": "running"},
                ]
            ),
            "",
        )

    monkeypatch.setattr(server_info_api, "_run_command", _fake_run_command)

    rows = server_info_api._collect_service_rows(engine_harness.context)
    by_key = {row["key"]: row for row in rows}

    assert by_key["api-backend"]["runtime"] == "compose"
    assert by_key["api-backend"]["status"] == "healthy"
    assert by_key["webui-frontend"]["unit_name"] == "borealis-engine-webui-frontend"
    assert by_key["postgres-db"]["status"] == "unknown"


def test_collect_service_rows_uses_docker_proxy(engine_harness: EngineTestHarness, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
    monkeypatch.setenv("BOREALIS_DOCKER_PROXY_URL", "http://127.0.0.1:2375")
    monkeypatch.setattr(server_info_api.shutil, "which", lambda _name: "")

    def _fake_proxy_json(path, params=None):
        if path == "/containers/json":
            return [
                {
                    "Names": ["/borealis-engine-api-backend"],
                    "State": "running",
                    "Status": "Up 2 minutes (healthy)",
                    "Labels": {
                        "com.docker.compose.project": "borealis-engine",
                        "com.docker.compose.service": "api-backend",
                    },
                },
                {
                    "Names": ["/borealis-engine-docker-proxy"],
                    "State": "running",
                    "Status": "Up 3 minutes",
                    "Labels": {
                        "com.docker.compose.project": "borealis-engine",
                        "com.docker.compose.service": "docker-proxy",
                    },
                },
            ]
        if path == "/containers/borealis-engine-api-backend/json":
            return {
                "State": {
                    "Status": "running",
                    "Health": {"Status": "healthy"},
                    "StartedAt": "2026-05-13T06:00:00Z",
                    "Pid": 101,
                }
            }
        if path == "/containers/borealis-engine-docker-proxy/json":
            return {
                "State": {
                    "Status": "running",
                    "StartedAt": "2026-05-13T05:59:00Z",
                    "Pid": 102,
                }
            }
        return None

    monkeypatch.setattr(server_info_api, "_docker_proxy_get_json", _fake_proxy_json)

    rows = server_info_api._collect_service_rows(engine_harness.context)
    by_key = {row["key"]: row for row in rows}

    assert by_key["api-backend"]["status"] == "healthy"
    assert by_key["api-backend"]["docker_status_text"] == "Up 2 minutes (healthy)"
    assert by_key["api-backend"]["started_at"] == "2026-05-13T06:00:00Z"
    assert by_key["docker-proxy"]["runtime"] == "compose"
    assert by_key["docker-proxy"]["docker_status_text"] == "Up 3 minutes"


def test_service_row_falls_back_to_human_systemd_timestamp() -> None:
    tracker = server_info_api.ServerActionTracker()

    row = server_info_api._service_row(
        service_key="borealis_engine",
        label="Borealis Engine",
        unit_name="borealis-engine.service",
        show_payload={
            "ActiveState": "active",
            "SubState": "running",
            "UnitFileState": "enabled",
            "MainPID": "101",
            "ExecMainStartTimestampUSec": "",
            "ActiveEnterTimestampUSec": "",
            "ExecMainStartTimestamp": "Sun 2026-04-05 03:03:37 MDT",
            "FragmentPath": "/etc/systemd/system/borealis-engine.service",
        },
        tracker=tracker,
        restart_supported=True,
    )

    assert row["started_at"] == "Sun 2026-04-05 03:03:37 MDT"
    assert row["active_state"] == "active"


def test_restart_service_queues_detached_systemd_run(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    monkeypatch.setattr(server_info_api, "_containerized_engine_enabled", lambda: False)
    monkeypatch.setattr(
        server_info_api,
        "_collect_service_rows",
        lambda _context: [
            {
                "key": "borealis_engine",
                "label": "Borealis Engine",
                "unit_name": "borealis-engine.service",
                "restart_supported": True,
            }
        ],
    )

    captured = {}

    def _fake_which(name: str) -> str:
        if name in {"systemctl", "systemd-run"}:
            return f"/usr/bin/{name}"
        return ""

    def _fake_run_command(args, **_kwargs):
        captured["args"] = list(args)
        return 0, "queued", ""

    monkeypatch.setattr(server_info_api.shutil, "which", _fake_which)
    monkeypatch.setattr(server_info_api, "_run_command", _fake_run_command)

    response = client.post("/api/server/services/borealis_engine/restart", json={})
    assert response.status_code == 202
    payload = response.get_json()

    assert payload["queued"] is True
    assert payload["unit_name"] == "borealis-engine.service"
    assert payload["job_unit"].endswith(".service")
    assert captured["args"][0] == "/usr/bin/systemd-run"
    assert captured["args"][1].startswith("--unit=borealis-admin-restart-borealis_engine-")
    assert captured["args"][-2] == "-lc"
    assert "sleep 2; /usr/bin/systemctl restart borealis-engine.service" in captured["args"][-1]

    tracker = server_info_api._get_action_tracker(engine_harness.context)
    pending = tracker.get_pending(service_key="borealis_engine")
    assert pending is not None
    assert pending["action"] == "restart"


def test_restart_postgresql_requires_valid_instance(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _admin_client(engine_harness)
    monkeypatch.setattr(server_info_api, "_containerized_engine_enabled", lambda: False)
    monkeypatch.setattr(
        server_info_api,
        "_collect_service_rows",
        lambda _context: [
            {
                "key": "postgresql_cluster",
                "label": "PostgreSQL 17-main",
                "unit_name": "postgresql@17-main.service",
                "instance": "17-main",
                "restart_supported": True,
            }
        ],
    )

    missing_response = client.post("/api/server/services/postgresql_cluster/restart", json={})
    assert missing_response.status_code == 400

    invalid_response = client.post(
        "/api/server/services/postgresql_cluster/restart",
        json={"instance": "15-main"},
    )
    assert invalid_response.status_code == 400


def test_wireguard_recover_rejects_when_no_active_sessions(engine_harness: EngineTestHarness, monkeypatch) -> None:
    class _FakeTunnelService:
        def list_sessions(self):
            return []

    client = _admin_client(engine_harness)
    monkeypatch.setattr(server_info_api, "_get_tunnel_service", lambda _adapters: _FakeTunnelService())

    response = client.post("/api/server/wireguard/recover")
    assert response.status_code == 409


def test_wireguard_recover_calls_public_wrapper(engine_harness: EngineTestHarness, monkeypatch) -> None:
    calls = {}

    class _FakeTunnelService:
        def list_sessions(self):
            return [{"agent_id": "device-1"}]

        def recover_listener(self, *, trigger: str, reason: str, force: bool):
            calls["trigger"] = trigger
            calls["reason"] = reason
            calls["force"] = force
            return {
                "listener_healthy": False,
                "reason": "listener_unhealthy",
                "active_tunnel_count": 1,
            }

    client = _admin_client(engine_harness)
    monkeypatch.setattr(server_info_api, "_get_tunnel_service", lambda _adapters: _FakeTunnelService())

    response = client.post("/api/server/wireguard/recover")
    assert response.status_code == 200
    payload = response.get_json()

    assert payload["status"] == "ok"
    assert payload["wireguard"]["active_tunnel_count"] == 1
    assert calls == {
        "trigger": "admin_dashboard",
        "reason": "manual_admin_recovery",
        "force": True,
    }


def test_collect_public_certificates_filters_public_edge_and_parses_expiry(tmp_path) -> None:
    acme_path = tmp_path / "acme.json"
    acme_payload = {
        "letsencrypt": {
            "Certificates": [
                {
                    "domain": {
                        "main": "borealis.example.com",
                        "sans": ["www.borealis.example.com"],
                    },
                    "certificate": _certificate_blob_base64("borealis.example.com", days_valid=45),
                },
                {
                    "domain": {
                        "main": "internal.example.com",
                    },
                    "certificate": _certificate_blob_base64("internal.example.com", days_valid=45),
                },
            ]
        }
    }
    acme_path.write_text(json.dumps(acme_payload), encoding="utf-8")

    certificates = server_info_api._collect_public_certificates(
        acme_path,
        fqdn="borealis.example.com",
    )

    assert len(certificates) == 1
    assert certificates[0]["main_domain"] == "borealis.example.com"
    assert certificates[0]["severity"] == "healthy"
    assert certificates[0]["days_remaining"] is not None


def test_collect_public_certificates_handles_missing_and_invalid_storage(tmp_path) -> None:
    missing = server_info_api._collect_public_certificates(tmp_path / "missing.json", fqdn="borealis.example.com")
    assert missing == []

    invalid_path = tmp_path / "invalid.json"
    invalid_path.write_text("{broken", encoding="utf-8")
    invalid = server_info_api._collect_public_certificates(invalid_path, fqdn="borealis.example.com")
    assert invalid == []


def test_operator_presence_socket_sync_and_disconnect_cleanup(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    socket_client = engine_harness.context.socketio.test_client(
        engine_harness.app,
        flask_test_client=client,
    )

    assert socket_client.is_connected()

    socket_client.emit("operator_presence_sync", {"current_page": "Server Info"})
    registry = engine_harness.context.operator_presence_registry
    sessions = registry.list_sessions()
    assert len(sessions) == 1
    assert sessions[0]["current_page"] == "Server Info"
    assert any(item["name"] == "server_operator_presence_changed" for item in socket_client.get_received())

    socket_client.emit("operator_presence_sync", {"current_page": "Server Info"})
    assert socket_client.get_received() == []

    socket_client.emit("operator_presence_sync", {"current_page": "Log Management"})
    updated_sessions = registry.list_sessions()
    assert updated_sessions[0]["current_page"] == "Log Management"
    assert any(item["name"] == "server_operator_presence_changed" for item in socket_client.get_received())

    socket_client.disconnect()
    assert registry.list_sessions() == []
