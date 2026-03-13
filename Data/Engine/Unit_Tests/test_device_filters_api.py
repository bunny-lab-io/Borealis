# ======================================================
# Data\Engine\Unit_Tests\test_device_filters_api.py
# Description: Exercises the overhauled device filter API and scheduler
#              snapshot behavior for software-aware targeting.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import json
import sqlite3
import time
from flask import Flask

from Data.Engine.server import create_app
from Data.Engine.services.API.scheduled_jobs import job_scheduler

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _filter_client_with_admin_session(harness: EngineTestHarness):
    config = {
        "DATABASE_PATH": str(harness.db_path),
        "TLS_CERT_PATH": harness.app.config["TLS_CERT_PATH"],
        "TLS_KEY_PATH": harness.app.config["TLS_KEY_PATH"],
        "TLS_BUNDLE_PATH": harness.app.config["TLS_BUNDLE_PATH"],
        "SECRET_KEY": harness.app.config["SECRET_KEY"],
        "LOG_FILE": harness.app.config["LOG_FILE"],
        "ERROR_LOG_FILE": harness.app.config["ERROR_LOG_FILE"],
        "STATIC_FOLDER": harness.app.config["STATIC_FOLDER"],
        "API_GROUPS": ("core", "auth", "tokens", "enrollment", "devices", "assemblies", "filters", "scheduled_jobs"),
    }
    app, _socketio, _context = create_app(config)
    app.config.update(TESTING=True)
    client = app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _seed_filter_inventory(harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET software = ?,
                   operating_system = ?,
                   device_type = ?,
                   last_seen = ?
             WHERE hostname = ?
            """,
            (
                json.dumps(
                    [
                        {"name": "Google Chrome", "version": "124.0.6367.92", "source": "local_installed"},
                        {"name": "Contoso.App", "version": "1.2.0", "source": "windows_store"},
                    ]
                ),
                "Windows 11 Pro",
                "Workstation",
                int(time.time()),
                "test-device",
            ),
        )
        cur.execute("DELETE FROM device_software_inventory")
        cur.executemany(
            """
            INSERT INTO device_software_inventory(
                device_guid,
                name,
                name_normalized,
                version,
                source,
                captured_at,
                metadata_json
            ) VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            [
                (
                    "GUID-TEST-0001",
                    "Google Chrome",
                    "google chrome",
                    "124.0.6367.92",
                    "local_installed",
                    int(time.time()),
                    "{}",
                ),
                (
                    "GUID-TEST-0001",
                    "Contoso.App",
                    "contoso.app",
                    "1.2.0",
                    "windows_store",
                    int(time.time()),
                    "{}",
                ),
                (
                    "GUID-TEST-0002",
                    "openssl",
                    "openssl",
                    "3.0.0",
                    "rpm",
                    int(time.time()),
                    "{}",
                ),
            ],
        )
        cur.execute(
            """
            INSERT INTO devices(
                guid,
                hostname,
                description,
                created_at,
                agent_hash,
                memory,
                network,
                software,
                storage,
                cpu,
                device_type,
                domain,
                external_ip,
                internal_ip,
                last_reboot,
                last_seen,
                last_user,
                operating_system,
                uptime,
                agent_id,
                connection_type,
                connection_endpoint,
                ssl_key_fingerprint,
                token_version,
                status,
                key_added_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "GUID-TEST-0002",
                "linux-node",
                "Linux node for filter tests",
                1_700_000_100,
                "hash-linux",
                json.dumps([]),
                json.dumps([]),
                json.dumps([{"name": "openssl", "version": "3.0.0", "source": "rpm"}]),
                json.dumps([]),
                json.dumps({"name": "AMD EPYC"}),
                "Server",
                "example.local",
                "",
                "10.0.0.20",
                "",
                int(time.time()),
                "root",
                "Rocky Linux 9",
                3600,
                "linux-node-agent",
                "",
                "",
                "AA:BB:CC",
                1,
                "active",
                "2025-10-01T00:00:00Z",
            ),
        )
        cur.execute(
            "INSERT INTO sites (id, name, description, created_at, enrollment_code) VALUES (?, ?, ?, ?, ?)",
            (2, "Branch Lab", "Secondary integration site", 1_700_000_100, "SITE-BRANCH-CODE"),
        )
        cur.execute(
            "INSERT INTO device_sites (device_hostname, site_id, assigned_at) VALUES (?, ?, ?)",
            ("linux-node", 2, int(time.time())),
        )
        conn.commit()
    finally:
        conn.close()


class _DummySocketIO:
    def start_background_task(self, *_args, **_kwargs):
        return None

    def emit(self, *_args, **_kwargs):
        return None


def test_filter_preview_create_clone_and_usage_lock(engine_harness: EngineTestHarness) -> None:
    _seed_filter_inventory(engine_harness)
    client = _filter_client_with_admin_session(engine_harness)

    metadata_response = client.get("/api/device_filters/metadata")
    assert metadata_response.status_code == 200
    metadata_body = metadata_response.get_json()
    fields = metadata_body["fields"]
    assert any(field["value"] == "installed_software" for field in fields)
    assert any(op["value"] == "does_not_contain" for op in metadata_body["operators"]["text"])

    payload = {
        "name": "Chrome on Main Lab",
        "description": "Chrome software filter",
        "site_mode": "specific_sites",
        "site_ids": [1],
        "criteria": {
            "groups": [
                {
                    "join_with": "",
                    "conditions": [
                        {
                            "field": "installed_software",
                            "operator": "contains",
                            "value": "chrome",
                            "software_source": "local_installed",
                        }
                    ],
                }
            ]
        },
    }

    preview_response = client.post("/api/device_filters/preview", json=payload)
    assert preview_response.status_code == 200
    preview_body = preview_response.get_json()
    assert preview_body["matched_device_count"] == 1
    assert [device["hostname"] for device in preview_body["devices"]] == ["test-device"]

    create_response = client.post("/api/device_filters", json=payload)
    assert create_response.status_code == 201
    created_filter = create_response.get_json()["filter"]
    assert created_filter["matching_device_count"] == 1
    assert created_filter["usage"]["job_count"] == 0
    filter_id = int(created_filter["id"])

    clone_response = client.post(f"/api/device_filters/{filter_id}/clone")
    assert clone_response.status_code == 201
    cloned_filter = clone_response.get_json()["filter"]
    assert cloned_filter["archived"] is False
    assert cloned_filter["name"].startswith("(Clone) Chrome on Main Lab")

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        conn.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                start_ts,
                duration_stop_enabled,
                expiration,
                execution_context,
                credential_id,
                use_service_account,
                enabled,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                44,
                "Chrome Audit Job",
                json.dumps([{"type": "script", "path": "Scripts/chrome.ps1"}]),
                json.dumps([{"kind": "filter", "filter_id": filter_id, "name": created_filter["name"]}]),
                "immediately",
                None,
                0,
                "no_expire",
                "system",
                None,
                0,
                1,
                int(time.time()),
                int(time.time()),
            ),
        )
        conn.commit()
    finally:
        conn.close()

    usage_response = client.get(f"/api/device_filters/{filter_id}/usage")
    assert usage_response.status_code == 200
    usage_body = usage_response.get_json()["usage"]
    assert usage_body["job_count"] == 1
    assert usage_body["jobs"][0]["name"] == "Chrome Audit Job"

    archive_response = client.post(f"/api/device_filters/{filter_id}/archive")
    assert archive_response.status_code == 409
    archive_body = archive_response.get_json()
    assert archive_body["error"] == "filter_in_use"
    assert archive_body["jobs"][0]["name"] == "Chrome Audit Job"

    delete_response = client.delete(f"/api/device_filters/{filter_id}")
    assert delete_response.status_code == 409
    delete_body = delete_response.get_json()
    assert delete_body["error"] == "filter_in_use"


def test_scheduler_marks_zero_target_filter_runs_as_skipped(engine_harness: EngineTestHarness) -> None:
    client = _filter_client_with_admin_session(engine_harness)
    create_response = client.post(
        "/api/device_filters",
        json={
            "name": "No Match Filter",
            "description": "Matches nothing",
            "site_mode": "global",
            "criteria": {
                "groups": [
                    {
                        "join_with": "",
                        "conditions": [
                            {
                                "field": "installed_software",
                                "operator": "contains",
                                "value": "definitely-not-installed",
                            }
                        ],
                    }
                ]
            },
        },
    )
    assert create_response.status_code == 201
    filter_id = int(create_response.get_json()["filter"]["id"])

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        conn.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                start_ts,
                duration_stop_enabled,
                expiration,
                execution_context,
                credential_id,
                use_service_account,
                enabled,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                88,
                "Zero Target Job",
                json.dumps([{"type": "script", "path": "Scripts/zero-target.ps1"}]),
                json.dumps([{"kind": "filter", "filter_id": filter_id, "name": "No Match Filter"}]),
                "immediately",
                None,
                0,
                "30m",
                "system",
                None,
                0,
                1,
                int(time.time()),
                int(time.time()),
            ),
        )
        conn.commit()
    finally:
        conn.close()

    scheduler = job_scheduler.JobScheduler(Flask(__name__), _DummySocketIO(), str(engine_harness.db_path))
    scheduler._tick_once()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "SELECT status, skip_reason, target_hostname FROM scheduled_job_runs WHERE job_id=?",
            (88,),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row is not None
    assert row[0] == "Skipped"
    assert row[1] == "no_devices_targeted"
    assert row[2] in (None, "")


def test_filter_preview_supports_does_not_contain(engine_harness: EngineTestHarness) -> None:
    _seed_filter_inventory(engine_harness)
    client = _filter_client_with_admin_session(engine_harness)

    response = client.post(
        "/api/device_filters/preview",
        json={
            "name": "Windows Workstations Without Server",
            "description": "Negative string matching",
            "site_mode": "global",
            "criteria": {
                "groups": [
                    {
                        "join_with": "",
                        "conditions": [
                            {
                                "field": "operating_system",
                                "operator": "contains",
                                "value": "windows",
                                "join_with": "",
                            },
                            {
                                "field": "operating_system",
                                "operator": "does_not_contain",
                                "value": "server",
                                "join_with": "AND",
                            },
                        ],
                    }
                ]
            },
        },
    )

    assert response.status_code == 200
    body = response.get_json()
    assert body["matched_device_count"] == 1
    assert [device["hostname"] for device in body["devices"]] == ["test-device"]
