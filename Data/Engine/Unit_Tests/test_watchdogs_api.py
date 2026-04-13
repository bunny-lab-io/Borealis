# ======================================================
# Data\Engine\Unit_Tests\test_watchdogs_api.py
# Description: Exercises the Borealis watchdog authoring, preview,
#              incident, and per-device override API flows.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import json
import time

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.API.watchdogs import runtime as watchdog_runtime_module

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _seed_watchdog_filter(harness: EngineTestHarness, *, filter_id: int = 1) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO device_filters (
                id,
                name,
                description,
                archived,
                criteria_mode,
                site_mode,
                basic_criteria_json,
                advanced_criteria_json,
                last_edited_by,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, 0, 'advanced', 'global', ?, ?, ?, ?, ?)
            """,
            (
                filter_id,
                "All Devices",
                "Matches the seeded test devices",
                json.dumps({"criteria": []}),
                json.dumps({"groups": []}),
                "admin",
                int(time.time()),
                int(time.time()),
            ),
        )
        conn.commit()
    finally:
        conn.close()


def _seed_storage_usage(harness: EngineTestHarness, *, entries: list[dict] | None = None) -> None:
    storage_entries = entries or [
        {
            "drive": "C",
            "total": 1000,
            "used": 930,
            "free": 70,
            "usage": 93,
            "disk_type": "Fixed Disk",
        }
    ]
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET storage = ?
            WHERE hostname = ?
            """,
            (
                json.dumps(storage_entries),
                "test-device",
            ),
        )
        conn.commit()
    finally:
        conn.close()


def _set_device_last_seen(harness: EngineTestHarness, *, hostname: str, last_seen: int) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET last_seen = ?
             WHERE hostname = ?
            """,
            (int(last_seen), hostname),
        )
        conn.commit()
    finally:
        conn.close()


def _set_device_uptime(harness: EngineTestHarness, *, hostname: str, uptime: int) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET uptime = ?
             WHERE hostname = ?
            """,
            (int(uptime), hostname),
        )
        conn.commit()
    finally:
        conn.close()


def _set_device_metrics(
    harness: EngineTestHarness,
    *,
    hostname: str,
    cpu_percent: float | None = None,
    memory_percent: float | None = None,
) -> None:
    assignments: list[str] = []
    params: list[object] = []
    if cpu_percent is not None:
        assignments.append("cpu_percent = ?")
        params.append(float(cpu_percent))
    if memory_percent is not None:
        assignments.append("memory_percent = ?")
        params.append(float(memory_percent))
    if not assignments:
        return
    params.append(hostname)
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            f"""
            UPDATE devices
               SET {", ".join(assignments)}
             WHERE hostname = ?
            """,
            tuple(params),
        )
        conn.commit()
    finally:
        conn.close()


def _seed_sessions(harness: EngineTestHarness, *, hostname: str, sessions: list[dict], reported_at: int | None = None) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET sessions = ?
             WHERE hostname = ?
            """,
            (
                json.dumps(
                    {
                        "reported_at": int(reported_at or time.time()),
                        "sessions": sessions,
                    }
                ),
                hostname,
            ),
        )
        conn.commit()
    finally:
        conn.close()


def _seed_processes(harness: EngineTestHarness, *, hostname: str, processes: list[dict], reported_at: int | None = None) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET processes = ?
             WHERE hostname = ?
            """,
            (
                json.dumps(
                    {
                        "reported_at": int(reported_at or time.time()),
                        "processes": processes,
                    }
                ),
                hostname,
            ),
        )
        conn.commit()
    finally:
        conn.close()


def _offline_watchdog_payload(*, targets) -> dict:
    return {
        "name": "Offline Watchdog",
        "description": "Opens when the device has not checked in recently.",
        "enabled": True,
        "archived": False,
        "severity": "warning",
        "site_mode": "global",
        "site_ids": [],
        "evaluation_interval_seconds": 60,
        "cooldown_seconds": 0,
        "auto_resolve_after_seconds": 60,
        "min_consecutive_matches": 1,
        "boot_grace_seconds": 0,
        "criteria": {
            "match_mode": "all",
            "rules": [
                {
                    "id": "rule-offline",
                    "type": "device_offline",
                    "offline_after_seconds": 60,
                }
            ],
        },
        "actions": {
            "actions": [
                {
                    "id": "notify-1",
                    "type": "notification",
                    "enabled": True,
                    "variant": "warning",
                    "title": "Device Offline",
                    "message_template": "{{hostname}} has missed heartbeats.",
                }
            ]
        },
        "targets": targets,
    }


def test_watchdog_preview_and_create_supports_filter_targets(engine_harness: EngineTestHarness) -> None:
    _seed_watchdog_filter(engine_harness, filter_id=17)
    client = _client_with_admin_session(engine_harness)

    payload = _offline_watchdog_payload(
        targets=[
            {
                "kind": "filter",
                "filter_id": 17,
                "name": "All Devices",
            }
        ]
    )

    preview_response = client.post("/api/watchdogs/preview", json=payload)
    assert preview_response.status_code == 200
    preview_body = preview_response.get_json()
    assert preview_body["device_count"] == 1
    assert preview_body["matched_count"] == 1
    assert preview_body["devices"][0]["hostname"] == "test-device"
    assert preview_body["devices"][0]["state"] == "triggered"

    create_response = client.post("/api/watchdogs", json=payload)
    assert create_response.status_code == 201
    created = create_response.get_json()
    assert created["name"] == "Offline Watchdog"
    assert created["target_device_count"] == 1
    assert created["open_incident_count"] == 1
    assert created["targets"][0]["kind"] == "filter"

    list_response = client.get("/api/watchdogs")
    assert list_response.status_code == 200
    items = list_response.get_json()["items"]
    assert len(items) == 1
    assert items[0]["id"] == created["id"]

    incident_response = client.get("/api/watchdogs/incidents?state=open")
    assert incident_response.status_code == 200
    incidents = incident_response.get_json()["items"]
    assert len(incidents) == 1
    assert incidents[0]["watchdog_id"] == created["id"]
    assert incidents[0]["hostname"] == "test-device"


def test_watchdog_storage_preview_returns_threshold_sample(engine_harness: EngineTestHarness) -> None:
    _seed_storage_usage(engine_harness)
    client = _client_with_admin_session(engine_harness)

    payload = {
        "name": "Disk Watchdog",
        "description": "Highlights high storage usage.",
        "enabled": True,
        "severity": "error",
        "site_mode": "global",
        "criteria": {
            "match_mode": "all",
            "rules": [
                {
                    "id": "rule-storage",
                    "type": "storage_usage_percent",
                    "drive": "C",
                    "threshold": 90,
                }
            ],
        },
        "actions": {"actions": []},
        "targets": [
            {
                "kind": "device",
                "device_guid": "GUID-TEST-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
            }
        ],
    }

    response = client.post("/api/watchdogs/preview", json=payload)
    assert response.status_code == 200
    body = response.get_json()
    assert body["device_count"] == 1
    assert body["matched_count"] == 1
    device = body["devices"][0]
    assert device["state"] == "triggered"
    assert device["sample"]["results"][0]["sample"]["drive"] == "C"
    assert device["sample"]["results"][0]["sample"]["usage_percent"] == 93.0
    assert device["sample"]["results"][0]["sample"]["threshold"] == 90.0


def test_watchdog_storage_specific_drive_does_not_fallback_to_other_disks(
    engine_harness: EngineTestHarness,
) -> None:
    _seed_storage_usage(
        engine_harness,
        entries=[
            {
                "drive": "C:",
                "total": 1000,
                "used": 400,
                "free": 600,
                "usage": 40,
                "disk_type": "Fixed Disk",
            },
            {
                "drive": "D:",
                "total": 1000,
                "used": 960,
                "free": 40,
                "usage": 96,
                "disk_type": "Fixed Disk",
            },
        ],
    )
    client = _client_with_admin_session(engine_harness)

    specific_drive_response = client.post(
        "/api/watchdogs/preview",
        json={
            "name": "Specific Disk Watchdog",
            "description": "Only checks the selected drive.",
            "enabled": True,
            "severity": "warning",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [
                    {
                        "id": "rule-storage-specific",
                        "type": "storage_usage_percent",
                        "drive_mode": "specific",
                        "drive": "C",
                        "threshold": 90,
                    }
                ],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )
    assert specific_drive_response.status_code == 200
    specific_body = specific_drive_response.get_json()
    assert specific_body["matched_count"] == 0
    specific_rule = specific_body["devices"][0]["sample"]["results"][0]
    assert specific_rule["sample"]["drive_scope"] == "specific"
    assert specific_rule["sample"]["drive"] == "C:"
    assert specific_rule["sample"]["usage_percent"] == 40.0

    all_drives_response = client.post(
        "/api/watchdogs/preview",
        json={
            "name": "All Disk Watchdog",
            "description": "Checks every drive in scope.",
            "enabled": True,
            "severity": "warning",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [
                    {
                        "id": "rule-storage-all",
                        "type": "storage_usage_percent",
                        "drive_mode": "all",
                        "threshold": 90,
                    }
                ],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )
    assert all_drives_response.status_code == 200
    all_drives_body = all_drives_response.get_json()
    assert all_drives_body["matched_count"] == 1
    all_drives_rule = all_drives_body["devices"][0]["sample"]["results"][0]
    assert all_drives_rule["sample"]["drive_scope"] == "all"
    assert all_drives_rule["sample"]["highest_drive"] == "D:"
    assert all_drives_rule["sample"]["highest_usage_percent"] == 96.0
    assert all_drives_rule["sample"]["matched_drives"][0]["drive"] == "D:"


def test_watchdog_all_devices_target_resolves_scope(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)

    payload = _offline_watchdog_payload(
        targets=[
            {
                "kind": "all_devices",
                "name": "All Devices in Scope",
            }
        ]
    )

    preview_response = client.post("/api/watchdogs/preview", json=payload)
    assert preview_response.status_code == 200
    preview_body = preview_response.get_json()
    assert preview_body["device_count"] == 1
    assert preview_body["devices"][0]["hostname"] == "test-device"

    create_response = client.post("/api/watchdogs", json=payload)
    assert create_response.status_code == 201
    created = create_response.get_json()
    assert created["target_device_count"] == 1
    assert created["targets"][0]["kind"] == "all_devices"


def test_watchdog_do_nothing_action_persists_as_incident_only(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)

    payload = _offline_watchdog_payload(
        targets=[
            {
                "kind": "device",
                "device_guid": "GUID-TEST-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
            }
        ]
    )
    payload["actions"] = {
        "actions": [
            {
                "id": "noop-1",
                "type": "do_nothing",
                "enabled": True,
            }
        ]
    }

    response = client.post("/api/watchdogs", json=payload)
    assert response.status_code == 201
    body = response.get_json()
    assert body["actions"]["actions"][0]["type"] == "do_nothing"
    assert "Incident only" in body["action_summaries"][0]


def test_watchdog_run_assembly_action_preserves_variable_values(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)

    payload = _offline_watchdog_payload(
        targets=[
            {
                "kind": "device",
                "device_guid": "GUID-TEST-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
            }
        ]
    )
    payload["actions"] = {
        "actions": [
            {
                "id": "assembly-1",
                "type": "assembly",
                "enabled": True,
                "assembly_guid": "TEST-ASSEMBLY-GUID",
                "run_mode": "system",
                "execution_context": "local",
                "variable_values": {
                    "command": "hostname",
                    "use_shell": True,
                },
            }
        ]
    }

    response = client.post("/api/watchdogs", json=payload)
    assert response.status_code == 201
    body = response.get_json()
    action = body["actions"]["actions"][0]
    assert action["type"] == "assembly"
    assert action["assembly_guid"] == "test-assembly-guid"
    assert action["variable_values"] == {
        "command": "hostname",
        "use_shell": True,
    }


def test_watchdog_incident_suppressed_queue_counts_and_reopen_round_trip(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)

    create_response = client.post(
        "/api/watchdogs",
        json=_offline_watchdog_payload(
            targets=[
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ]
        ),
    )
    assert create_response.status_code == 201

    all_response = client.get("/api/watchdogs/incidents?state=all")
    assert all_response.status_code == 200
    all_payload = all_response.get_json()
    assert all_payload["counts"]["open"] == 1
    assert all_payload["counts"]["suppressed"] == 0
    assert all_payload["counts"]["resolved"] == 0
    incident_id = all_payload["items"][0]["id"]

    suppress_response = client.post(
        f"/api/watchdogs/incidents/{incident_id}/state",
        json={"state": "suppressed", "reason": "Temporarily suppressed from Alerts."},
    )
    assert suppress_response.status_code == 200
    suppressed = suppress_response.get_json()
    assert suppressed["state"] == "suppressed"
    assert suppressed["resolution_reason"] == "Temporarily suppressed from Alerts."

    suppressed_list = client.get("/api/watchdogs/incidents?state=suppressed")
    assert suppressed_list.status_code == 200
    suppressed_payload = suppressed_list.get_json()
    assert suppressed_payload["counts"]["open"] == 0
    assert suppressed_payload["counts"]["suppressed"] == 1
    assert suppressed_payload["counts"]["resolved"] == 0
    assert len(suppressed_payload["items"]) == 1

    reopen_response = client.post(
        f"/api/watchdogs/incidents/{incident_id}/state",
        json={"state": "open"},
    )
    assert reopen_response.status_code == 200
    reopened = reopen_response.get_json()
    assert reopened["state"] == "open"
    assert reopened["resolved_at"] is None

    reopened_list = client.get("/api/watchdogs/incidents?state=all")
    assert reopened_list.status_code == 200
    reopened_payload = reopened_list.get_json()
    assert reopened_payload["counts"]["open"] == 1
    assert reopened_payload["counts"]["suppressed"] == 0
    assert reopened_payload["counts"]["resolved"] == 0


def test_watchdog_incident_acknowledge_and_device_override_round_trip(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)

    create_response = client.post(
        "/api/watchdogs",
        json=_offline_watchdog_payload(
            targets=[
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ]
        ),
    )
    assert create_response.status_code == 201
    created = create_response.get_json()

    incidents_response = client.get("/api/watchdogs/incidents?state=open")
    assert incidents_response.status_code == 200
    incidents = incidents_response.get_json()["items"]
    assert len(incidents) == 1
    incident_id = incidents[0]["id"]

    acknowledge_response = client.post(f"/api/watchdogs/incidents/{incident_id}/acknowledge")
    assert acknowledge_response.status_code == 200
    acknowledged = acknowledge_response.get_json()
    assert acknowledged["id"] == incident_id
    assert acknowledged["acknowledged_by"] == "admin"
    assert acknowledged["acknowledged_at"] is not None

    device_watchdogs_response = client.get("/api/devices/test-device/watchdogs")
    assert device_watchdogs_response.status_code == 200
    device_payload = device_watchdogs_response.get_json()
    assert device_payload["device"]["hostname"] == "test-device"
    assert len(device_payload["assignments"]) == 1
    assert len(device_payload["incidents"]) == 1

    suppress_response = client.post(
        "/api/devices/test-device/watchdogs/overrides",
        json={
            "watchdog_id": created["id"],
            "state": "suppressed",
            "reason": "Temporary maintenance window.",
        },
    )
    assert suppress_response.status_code == 200
    suppressed_payload = suppress_response.get_json()
    assert len(suppressed_payload["overrides"]) == 1
    assert suppressed_payload["overrides"][0]["watchdog_id"] == created["id"]
    assert suppressed_payload["overrides"][0]["reason"] == "Temporary maintenance window."
    assert suppressed_payload["incidents"] == []

    clear_response = client.post(
        "/api/devices/test-device/watchdogs/overrides",
        json={
            "watchdog_id": created["id"],
            "clear": True,
        },
    )
    assert clear_response.status_code == 200
    cleared_payload = clear_response.get_json()
    assert cleared_payload["overrides"] == []
    assert len(cleared_payload["assignments"]) == 1


def test_offline_watchdog_incident_is_deleted_when_device_recovers(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)

    create_response = client.post(
        "/api/watchdogs",
        json=_offline_watchdog_payload(
            targets=[
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ]
        ),
    )
    assert create_response.status_code == 201
    created = create_response.get_json()

    incidents_response = client.get("/api/watchdogs/incidents?state=all")
    assert incidents_response.status_code == 200
    incidents_payload = incidents_response.get_json()
    assert incidents_payload["counts"]["open"] == 1
    assert len(incidents_payload["items"]) == 1
    incident_id = incidents_payload["items"][0]["id"]

    _set_device_last_seen(engine_harness, hostname="test-device", last_seen=int(time.time()))
    runtime = engine_harness.context.watchdog_runtime
    assert runtime is not None
    runtime.evaluate_watchdog(created["id"])

    after_response = client.get("/api/watchdogs/incidents?state=all")
    assert after_response.status_code == 200
    after_payload = after_response.get_json()
    assert after_payload["counts"]["open"] == 0
    assert after_payload["counts"]["suppressed"] == 0
    assert after_payload["counts"]["resolved"] == 0
    assert after_payload["items"] == []

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT COUNT(*) FROM watchdog_incidents WHERE id = ?", (incident_id,))
        remaining = cur.fetchone()[0]
    finally:
        conn.close()
    assert remaining == 0


def test_startup_cleanup_purges_resolved_offline_watchdog_incidents(
    engine_harness: EngineTestHarness,
) -> None:
    client = _client_with_admin_session(engine_harness)

    create_response = client.post(
        "/api/watchdogs",
        json=_offline_watchdog_payload(
            targets=[
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ]
        ),
    )
    assert create_response.status_code == 201

    incidents_response = client.get("/api/watchdogs/incidents?state=all")
    assert incidents_response.status_code == 200
    incident_id = incidents_response.get_json()["items"][0]["id"]

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE watchdog_incidents
               SET state = 'resolved',
                   resolved_at = ?,
                   resolution_reason = 'cleared'
             WHERE id = ?
            """,
            (int(time.time()), incident_id),
        )
        conn.commit()
    finally:
        conn.close()

    runtime = engine_harness.context.watchdog_runtime
    assert runtime is not None
    purged_count = runtime._purge_resolved_offline_incidents()
    assert purged_count == 1

    after_response = client.get("/api/watchdogs/incidents?state=all")
    assert after_response.status_code == 200
    after_payload = after_response.get_json()
    assert after_payload["items"] == []
    assert after_payload["counts"]["resolved"] == 0


def test_watchdog_user_session_match_normalized_blocklist_preview(engine_harness: EngineTestHarness) -> None:
    _seed_sessions(
        engine_harness,
        hostname="test-device",
        sessions=[
            {
                "username": "EXAMPLE\\alice",
                "session_id": 1,
                "session_name": "console",
                "state": "active",
                "protocol": "console",
            }
        ],
    )
    client = _client_with_admin_session(engine_harness)

    response = client.post(
        "/api/watchdogs/preview",
        json={
            "name": "Blocked User Watchdog",
            "description": "Alerts on blocked logins.",
            "enabled": True,
            "severity": "warning",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [
                    {
                        "id": "rule-user-session",
                        "type": "user_session_match",
                        "match_mode": "blocklist",
                        "pattern_mode": "normalized",
                        "patterns": ["alice"],
                    }
                ],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )

    assert response.status_code == 200
    body = response.get_json()
    assert body["matched_count"] == 1
    rule = body["devices"][0]["sample"]["results"][0]
    assert rule["matched"] is True
    assert rule["sample"]["violating_users"][0]["username"] == "EXAMPLE\\alice"


def test_watchdog_process_presence_preview_matches_normalized_executable_name(engine_harness: EngineTestHarness) -> None:
    _seed_processes(
        engine_harness,
        hostname="test-device",
        processes=[
            {
                "name": "explorer.exe",
                "count": 2,
            }
        ],
    )
    client = _client_with_admin_session(engine_harness)

    response = client.post(
        "/api/watchdogs/preview",
        json={
            "name": "Explorer Presence",
            "description": "Alerts if explorer is running.",
            "enabled": True,
            "severity": "info",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [
                    {
                        "id": "rule-process",
                        "type": "process_presence",
                        "process_name": "explorer",
                        "expectation": "present",
                    }
                ],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )

    assert response.status_code == 200
    body = response.get_json()
    assert body["matched_count"] == 1
    rule = body["devices"][0]["sample"]["results"][0]
    assert rule["sample"]["present"] is True
    assert rule["sample"]["count"] == 2


def test_watchdog_reboot_detected_uses_saved_baseline(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)

    create_response = client.post(
        "/api/watchdogs",
        json={
            "name": "Reboot Watchdog",
            "description": "Alerts when uptime drops.",
            "enabled": True,
            "severity": "warning",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [{"id": "rule-reboot", "type": "reboot_detected"}],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )
    assert create_response.status_code == 201
    created = create_response.get_json()
    assert created["open_incident_count"] == 0

    _set_device_uptime(engine_harness, hostname="test-device", uptime=120)
    runtime = engine_harness.context.watchdog_runtime
    assert runtime is not None
    runtime.evaluate_watchdog(created["id"])

    incidents_response = client.get("/api/watchdogs/incidents?state=open")
    assert incidents_response.status_code == 200
    incidents = incidents_response.get_json()["items"]
    assert len(incidents) == 1
    assert "reboot detected" in incidents[0]["message"].lower()


def test_watchdog_cpu_usage_duration_requires_elapsed_time(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    fixed_now = 2_000_000_000
    _set_device_last_seen(engine_harness, hostname="test-device", last_seen=fixed_now)
    _set_device_metrics(engine_harness, hostname="test-device", cpu_percent=95.0)
    monkeypatch.setattr(watchdog_runtime_module, "_now_ts", lambda: fixed_now)
    client = _client_with_admin_session(engine_harness)

    create_response = client.post(
        "/api/watchdogs",
        json={
            "name": "CPU Watchdog",
            "description": "Alerts on sustained CPU pressure.",
            "enabled": True,
            "severity": "warning",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [
                    {
                        "id": "rule-cpu",
                        "type": "cpu_usage_percent",
                        "threshold": 90,
                        "duration_seconds": 300,
                    }
                ],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )
    assert create_response.status_code == 201
    created = create_response.get_json()
    assert created["open_incident_count"] == 0

    monkeypatch.setattr(watchdog_runtime_module, "_now_ts", lambda: fixed_now + 301)
    runtime = engine_harness.context.watchdog_runtime
    assert runtime is not None
    runtime.evaluate_watchdog(created["id"])

    incidents_response = client.get("/api/watchdogs/incidents?state=open")
    assert incidents_response.status_code == 200
    incidents = incidents_response.get_json()["items"]
    assert len(incidents) == 1
    assert "cpu usage is 95.0%" in incidents[0]["message"].lower()


def test_watchdog_drive_presence_specific_detects_missing_and_unexpected_drives_preview(
    engine_harness: EngineTestHarness,
) -> None:
    _seed_storage_usage(
        engine_harness,
        entries=[
            {
                "drive": "C:",
                "total": 1000,
                "used": 400,
                "free": 600,
                "usage": 40,
                "disk_type": "Fixed Disk",
            },
            {
                "drive": "F:",
                "total": 512,
                "used": 20,
                "free": 492,
                "usage": 4,
                "disk_type": "Removable",
            },
        ],
    )
    client = _client_with_admin_session(engine_harness)

    response = client.post(
        "/api/watchdogs/preview",
        json={
            "name": "Drive Presence Watchdog",
            "description": "Alerts on drive topology mismatches.",
            "enabled": True,
            "severity": "warning",
            "site_mode": "global",
            "criteria": {
                "match_mode": "all",
                "rules": [
                    {
                        "id": "rule-drive-presence",
                        "type": "drive_presence_change",
                        "storage_scope": "all",
                        "watch_mode": "specific",
                        "change_types": ["added", "removed"],
                        "drive_list": ["C:", "E:"],
                    }
                ],
            },
            "actions": {"actions": []},
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
        },
    )

    assert response.status_code == 200
    body = response.get_json()
    assert body["matched_count"] == 1
    rule = body["devices"][0]["sample"]["results"][0]
    detected_changes = rule["sample"]["detected_changes"]
    assert {entry["change_type"] for entry in detected_changes} == {"added", "removed"}
    assert any(entry.get("drive") == "E:" for entry in detected_changes)
    assert any(entry.get("drive") == "F:" for entry in detected_changes)
