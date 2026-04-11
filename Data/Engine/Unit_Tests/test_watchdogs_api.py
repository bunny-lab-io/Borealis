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


def _seed_storage_usage(harness: EngineTestHarness) -> None:
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
                json.dumps(
                    [
                        {
                            "drive": "C",
                            "total": 1000,
                            "used": 930,
                            "free": 70,
                            "usage": 93,
                            "disk_type": "Fixed Disk",
                        }
                    ]
                ),
                "test-device",
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
