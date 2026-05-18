# ======================================================
# Data\Engine\Unit_Tests\test_patch_management_api.py
# Description: Exercises Windows patch-management policy, report ingestion, catalog, holds, and dispatch APIs.
#
# API Endpoints (if applicable):
# - POST /api/agent/patch-management/policy
# - POST /api/agent/patch-management/report
# - GET /api/patch-management/catalog
# - POST /api/patch-management/catalog/hold
# - POST /api/patch-management/catalog/release
# - POST /api/device/patches/<hostname>/action
# ======================================================

from __future__ import annotations

import json
from typing import Any

import pytest

from Data.Engine.auth import jwt_service as jwt_service_module

from .conftest import EngineTestHarness
from .support.engine import admin_client, db_connection, fetch_all, fetch_one


def _device_headers(guid: str = "GUID-TEST-0001") -> dict[str, str]:
    jwt_service = jwt_service_module.load_service()
    token = jwt_service.issue_access_token(
        guid,
        "ff:ff:ff",
        1,
        expires_in=900,
    )
    return {"Authorization": f"Bearer {token}"}


def _insert_policy(
    harness: EngineTestHarness,
    *,
    name: str,
    scope_type: str,
    site_id: int | None = None,
    device_guid: str | None = None,
    class_toggles: dict[str, Any] | None = None,
    updated_at: int = 1_800_000_000,
) -> int:
    toggles = {
        "security": True,
        "critical": True,
        "cumulative": True,
        "definition": True,
        "driver": True,
        "feature": True,
        "optional": True,
        "service_pack": True,
        "update_rollup": True,
        "updates": True,
    }
    toggles.update(class_toggles or {})
    reboot = {
        "mode": "maintenance_window",
        "maintenance_window_start": "21:00",
        "maintenance_window_end": "03:00",
        "deferral_deadline_hours": 48,
        "user_prompt": True,
    }
    with db_connection(harness) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO patch_policies(name, description, enabled, class_toggles_json, reboot_policy_json, created_at, updated_at)
            VALUES (?, ?, 1, ?, ?, ?, ?)
            """,
            (
                name,
                f"{name} test policy",
                json.dumps(toggles),
                json.dumps(reboot),
                updated_at - 10,
                updated_at,
            ),
        )
        policy_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO patch_policy_bindings(policy_id, scope_type, site_id, device_guid, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (policy_id, scope_type, site_id, device_guid, updated_at - 10, updated_at),
        )
        conn.commit()
        return policy_id


def test_patch_policy_endpoint_resolves_device_site_global_precedence(
    engine_harness: EngineTestHarness,
) -> None:
    client = engine_harness.app.test_client()

    response = client.post("/api/agent/patch-management/policy", headers=_device_headers(), json={})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["policy_name"] == "Global Default"
    assert payload["effective_reason"] == "global"
    assert payload["class_toggles"]["driver"] is True

    site_policy_id = _insert_policy(
        engine_harness,
        name="Site Ring",
        scope_type="site",
        site_id=1,
        class_toggles={"driver": False},
        updated_at=1_800_000_100,
    )
    response = client.post("/api/agent/patch-management/policy", headers=_device_headers(), json={})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["policy_id"] == str(site_policy_id)
    assert payload["policy_name"] == "Site Ring"
    assert payload["effective_reason"] == "site"
    assert payload["class_toggles"]["driver"] is False

    device_policy_id = _insert_policy(
        engine_harness,
        name="Device Override",
        scope_type="device",
        device_guid="GUID-TEST-0001",
        class_toggles={"driver": True, "feature": False},
        updated_at=1_800_000_200,
    )
    response = client.post("/api/agent/patch-management/policy", headers=_device_headers(), json={})
    assert response.status_code == 200
    payload = response.get_json()
    assert payload["policy_id"] == str(device_policy_id)
    assert payload["policy_name"] == "Device Override"
    assert payload["effective_reason"] == "device"
    assert payload["class_toggles"]["driver"] is True
    assert payload["class_toggles"]["feature"] is False


def test_patch_report_ingests_catalog_device_state_and_holds(
    engine_harness: EngineTestHarness,
) -> None:
    client = engine_harness.app.test_client()
    report = {
        "hostname": "test-device",
        "policy_id": "1",
        "policy_version": "v1",
        "scan_started_at": 1_800_001_000,
        "scan_completed_at": 1_800_001_030,
        "updates": [
            {
                "update_id": "update-test-1",
                "revision_number": 2,
                "title": "2026-05 Cumulative Update for Windows 11",
                "kb_article_ids": ["KB5000001"],
                "classifications": ["Security Updates"],
                "categories": ["Windows 11"],
                "msrc_severity": "Critical",
                "update_type": "Software",
                "size_bytes": 5_000_000_000,
                "support_url": "https://support.example/KB5000001",
                "approved": True,
                "downloaded": True,
            }
        ],
        "install": {
            "result_code": "failed",
            "started_at": 1_800_001_100,
            "finished_at": 1_800_001_130,
            "results": [
                {
                    "update_id": "update-test-1",
                    "revision_number": 2,
                    "result_code": "failed",
                    "hresult": "0x80240022",
                    "reboot_required": True,
                }
            ],
        },
    }

    response = client.post(
        "/api/agent/patch-management/report",
        headers=_device_headers(),
        json=report,
    )
    assert response.status_code == 200
    assert response.get_json()["count"] == 1

    catalog_row = fetch_one(
        engine_harness,
        "SELECT title, kb_articles_json, size_bytes FROM patch_catalog WHERE update_id = ? AND revision_number = ?",
        ("update-test-1", 2),
    )
    assert catalog_row is not None
    assert catalog_row[0] == "2026-05 Cumulative Update for Windows 11"
    assert json.loads(catalog_row[1]) == ["KB5000001"]
    assert catalog_row[2] == 5_000_000_000

    state_row = fetch_one(
        engine_harness,
        """
        SELECT status, approved, downloaded, reboot_required, result_code, hresult
          FROM device_patch_state
         WHERE device_guid = ? AND update_id = ? AND revision_number = ?
        """,
        ("GUID-TEST-0001", "update-test-1", 2),
    )
    assert state_row == ("failed", 1, 1, 1, "failed", "0x80240022")

    operator = admin_client(engine_harness)
    catalog_response = operator.get("/api/patch-management/catalog")
    assert catalog_response.status_code == 200
    updates = catalog_response.get_json()["updates"]
    assert updates[0]["update_id"] == "update-test-1"
    assert updates[0]["affected_devices"] == 1
    assert updates[0]["failed_count"] == 1
    assert updates[0]["pending_reboot_count"] == 1

    hold_response = operator.post(
        "/api/patch-management/catalog/hold",
        json={
            "update_id": "update-test-1",
            "revision_number": 2,
            "title": "2026-05 Cumulative Update for Windows 11",
            "reason": "Pilot hold",
        },
    )
    assert hold_response.status_code == 200
    assert hold_response.get_json()["changed"] == 1

    release_response = operator.post(
        "/api/patch-management/catalog/release",
        json={"update_id": "update-test-1"},
    )
    assert release_response.status_code == 200
    assert release_response.get_json()["changed"] == 1


def test_patch_report_marks_approved_updates_pending_install(
    engine_harness: EngineTestHarness,
) -> None:
    client = engine_harness.app.test_client()
    report = {
        "hostname": "test-device",
        "policy_id": "1",
        "policy_version": "v1",
        "scan_started_at": 1_800_002_000,
        "scan_completed_at": 1_800_002_030,
        "updates": [
            {
                "update_id": "update-pending-install",
                "revision_number": 1,
                "title": "Approved Definition Update",
                "kb_article_ids": ["KB5000002"],
                "classifications": ["Security Updates"],
                "categories": ["Windows 11"],
                "approved": True,
                "downloaded": False,
                "installed": False,
            }
        ],
    }

    response = client.post(
        "/api/agent/patch-management/report",
        headers=_device_headers(),
        json=report,
    )
    assert response.status_code == 200

    state_row = fetch_one(
        engine_harness,
        """
        SELECT status, approved, downloaded, installed
          FROM device_patch_state
         WHERE device_guid = ? AND update_id = ? AND revision_number = ?
        """,
        ("GUID-TEST-0001", "update-pending-install", 1),
    )
    assert state_row == ("pending_install", 1, 0, 0)

    operator = admin_client(engine_harness)
    catalog_response = operator.get("/api/patch-management/catalog")
    assert catalog_response.status_code == 200
    update = next(
        item for item in catalog_response.get_json()["updates"] if item["update_id"] == "update-pending-install"
    )
    assert update["missing_count"] == 1


def test_patch_report_prunes_absent_noninstalled_updates(
    engine_harness: EngineTestHarness,
) -> None:
    client = engine_harness.app.test_client()
    first_report = {
        "hostname": "test-device",
        "policy_id": "1",
        "policy_version": "v1",
        "scan_started_at": 1_800_003_000,
        "scan_completed_at": 1_800_003_030,
        "updates": [
            {
                "update_id": "defender-version-675",
                "revision_number": 1,
                "title": "Security Intelligence Update for Microsoft Defender Antivirus - KB2267602 (Version 1.449.675.0)",
                "kb_article_ids": ["KB2267602"],
                "classifications": ["Definition Updates"],
                "approved": True,
                "installed": False,
            },
            {
                "update_id": "msrt-installed",
                "revision_number": 1,
                "title": "Windows Malicious Software Removal Tool x64 - KB890830",
                "kb_article_ids": ["KB890830"],
                "classifications": ["Update Rollups"],
                "installed": True,
                "result_code": "success",
                "hresult": "0x0",
            },
        ],
    }
    response = client.post(
        "/api/agent/patch-management/report",
        headers=_device_headers(),
        json=first_report,
    )
    assert response.status_code == 200

    second_report = {
        "hostname": "test-device",
        "policy_id": "1",
        "policy_version": "v1",
        "scan_started_at": 1_800_003_100,
        "scan_completed_at": 1_800_003_130,
        "updates": [
            {
                "update_id": "defender-version-676",
                "revision_number": 1,
                "title": "Security Intelligence Update for Microsoft Defender Antivirus - KB2267602 (Version 1.449.676.0)",
                "kb_article_ids": ["KB2267602"],
                "classifications": ["Definition Updates"],
                "approved": True,
                "installed": False,
            }
        ],
    }
    response = client.post(
        "/api/agent/patch-management/report",
        headers=_device_headers(),
        json=second_report,
    )
    assert response.status_code == 200

    rows = fetch_all(
        engine_harness,
        """
        SELECT update_id, status
          FROM device_patch_state
         WHERE device_guid = ?
      ORDER BY update_id
        """,
        ("GUID-TEST-0001",),
    )
    assert rows == [
        ("defender-version-676", "pending_install"),
        ("msrt-installed", "installed"),
    ]

    operator = admin_client(engine_harness)
    catalog_response = operator.get("/api/patch-management/catalog")
    assert catalog_response.status_code == 200
    update_ids = {item["update_id"] for item in catalog_response.get_json()["updates"]}
    assert "defender-version-675" not in update_ids
    assert "defender-version-676" in update_ids
    assert "msrt-installed" in update_ids


def test_patch_device_action_dispatches_socket_and_records_history(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls: list[tuple[str, str, str, dict[str, Any]]] = []

    def emit_host_service_event(hostname: str, context: str, event: str, payload: dict[str, Any]) -> bool:
        calls.append((hostname, context, event, payload))
        return True

    monkeypatch.setattr(engine_harness.context, "emit_host_service_event", emit_host_service_event, raising=False)

    client = admin_client(engine_harness)
    response = client.post(
        "/api/device/patches/test-device/action",
        json={"action": "install", "update_ids": ["update-test-1"], "delay_seconds": 30},
    )

    assert response.status_code == 200
    assert response.get_json()["action"] == "install"
    assert len(calls) == 1
    hostname, context, event, payload = calls[0]
    assert hostname == "test-device"
    assert context == "system"
    assert event == "patch_management_request"
    assert payload["action"] == "install"
    assert payload["update_ids"] == ["update-test-1"]
    assert payload["delay_seconds"] == 30

    history = fetch_one(
        engine_harness,
        "SELECT action, status, requested_by FROM patch_action_history WHERE hostname = ?",
        ("test-device",),
    )
    assert history == ("install", "dispatched", "admin")
