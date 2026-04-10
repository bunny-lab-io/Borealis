# ======================================================
# Data\Engine\Unit_Tests\test_device_purge_api.py
# Description: Verifies holistic device purge behavior, stale auth blocking,
#              and scheduled-job target rewrites.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import hashlib
import json
from Data.Engine.db import dbapi as sqlite3

from Data.Engine.auth import jwt_service as jwt_service_module

from .conftest import EngineTestHarness


def _client_with_admin_session(harness: EngineTestHarness):
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _device_headers_for_guid(guid: str) -> dict[str, str]:
    jwt_service = jwt_service_module.load_service()
    token = jwt_service.issue_access_token(
        guid,
        "ff:ff:ff",
        1,
        expires_in=900,
    )
    return {"Authorization": f"Bearer {token}"}


def _ensure_device_purge_tables(db_path) -> None:
    with sqlite3.connect(str(db_path)) as conn:
        conn.executescript(
            """
            CREATE TABLE IF NOT EXISTS device_purge_barriers (
                guid TEXT PRIMARY KEY,
                required_token_version INTEGER NOT NULL,
                purged_at TEXT NOT NULL,
                purged_by TEXT,
                last_hostname TEXT,
                last_agent_id TEXT
            );
            CREATE TABLE IF NOT EXISTS device_vpn_config (
                agent_id TEXT PRIMARY KEY,
                allowed_ports TEXT,
                updated_at TEXT,
                updated_by TEXT
            );
            CREATE TABLE IF NOT EXISTS ansible_play_recaps (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id TEXT UNIQUE NOT NULL,
                hostname TEXT,
                agent_id TEXT,
                playbook_path TEXT,
                playbook_name TEXT,
                scheduled_job_id INTEGER,
                scheduled_run_id INTEGER,
                activity_job_id INTEGER,
                status TEXT,
                recap_text TEXT,
                recap_json TEXT,
                started_ts INTEGER,
                finished_ts INTEGER,
                created_at INTEGER,
                updated_at INTEGER
            );
            CREATE TABLE IF NOT EXISTS agent_service_account (
                agent_id TEXT PRIMARY KEY,
                username TEXT NOT NULL,
                password_hash BLOB,
                password_encrypted BLOB NOT NULL,
                last_rotated_utc TEXT NOT NULL,
                version INTEGER NOT NULL DEFAULT 1
            );
            """
        )
        conn.commit()


def test_device_purge_removes_references_and_blocks_stale_auth(
    engine_harness: EngineTestHarness,
) -> None:
    harness = engine_harness
    _ensure_device_purge_tables(harness.db_path)

    valid_guid = "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
    other_guid = "54E8C9E2-6B3D-4B51-A456-4ACB94C45F00"
    agent_id = "test-device-agent"
    refresh_token = "purge-refresh-token"
    refresh_hash = hashlib.sha256(refresh_token.encode("utf-8")).hexdigest()

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET guid = ?,
                   ssl_key_fingerprint = ?,
                   token_version = ?,
                   agent_id = ?,
                   status = 'active',
                   key_added_at = ?
             WHERE hostname = ?
            """,
            (
                valid_guid,
                "ff:ff:ff",
                1,
                agent_id,
                "2025-01-01T00:00:00+00:00",
                "test-device",
            ),
        )
        cur.execute(
            """
            INSERT INTO device_software_inventory (
                device_guid, name, name_normalized, version, source, captured_at, metadata_json
            )
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (
                valid_guid,
                "Borealis Agent",
                "borealis agent",
                "1.0.0",
                "local_installed",
                1_700_000_600,
                "{}",
            ),
        )
        cur.execute(
            """
            INSERT INTO refresh_tokens (id, guid, token_hash, created_at, expires_at, revoked_at, last_used_at)
            VALUES (?, ?, ?, ?, ?, NULL, NULL)
            """,
            (
                "purge-refresh-token-row",
                valid_guid,
                refresh_hash,
                "2025-01-01T00:00:00+00:00",
                "2026-01-01T00:00:00+00:00",
            ),
        )
        cur.execute(
            """
            INSERT INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
            VALUES (?, ?, ?, ?)
            """,
            (
                "purge-device-key",
                valid_guid,
                "ff:ff:ff",
                "2025-01-01T00:00:00+00:00",
            ),
        )
        cur.execute(
            """
            INSERT INTO device_approvals (
                id, approval_reference, guid, hostname_claimed, ssl_key_fingerprint_claimed,
                enrollment_code, site_id, status, client_nonce, server_nonce, agent_pubkey_der,
                created_at, updated_at, approved_by_user_id
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "purge-approval",
                "PURGE-REF-1",
                valid_guid,
                "test-device",
                "ff:ff:ff",
                "SITE-MAIN-CODE",
                1,
                "completed",
                "client",
                "server",
                None,
                "2025-01-01T00:00:00+00:00",
                "2025-01-01T00:00:00+00:00",
                "admin",
            ),
        )
        cur.execute(
            """
            INSERT INTO activity_history (
                hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "test-device",
                "Scripts/noop.ps1",
                "noop.ps1",
                "powershell",
                1_700_000_700,
                "success",
                "ok",
                "",
            ),
        )
        activity_id = int(cur.lastrowid)

        component_json = json.dumps([{"type": "script", "path": "Scripts/noop.ps1", "name": "noop"}])
        cur.execute(
            """
            INSERT INTO scheduled_jobs (
                name, components_json, targets_json, schedule_type, start_ts,
                duration_stop_enabled, expiration, execution_context, credential_id,
                use_service_account, enabled, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Single Device Job",
                component_json,
                json.dumps(
                    [
                        {
                            "kind": "device",
                            "device_guid": valid_guid,
                            "hostname": "test-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        }
                    ]
                ),
                "once",
                1_700_000_800,
                0,
                "no_expire",
                "system",
                None,
                0,
                1,
                1_700_000_800,
                1_700_000_800,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs (
                name, components_json, targets_json, schedule_type, start_ts,
                duration_stop_enabled, expiration, execution_context, credential_id,
                use_service_account, enabled, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Mixed Device Job",
                component_json,
                json.dumps(
                    [
                        {
                            "kind": "device",
                            "device_guid": valid_guid,
                            "hostname": "test-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                        {
                            "kind": "device",
                            "device_guid": other_guid,
                            "hostname": "survivor-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                    ]
                ),
                "once",
                1_700_000_810,
                0,
                "no_expire",
                "system",
                None,
                0,
                1,
                1_700_000_810,
                1_700_000_810,
            ),
        )
        mixed_job_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO scheduled_jobs (
                name, components_json, targets_json, schedule_type, start_ts,
                duration_stop_enabled, expiration, execution_context, credential_id,
                use_service_account, enabled, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Filter Only Job",
                component_json,
                json.dumps([{"kind": "filter", "filter_id": 7, "name": "Windows Devices"}]),
                "once",
                1_700_000_820,
                0,
                "no_expire",
                "system",
                None,
                0,
                1,
                1_700_000_820,
                1_700_000_820,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs (
                name, components_json, targets_json, schedule_type, start_ts,
                duration_stop_enabled, expiration, execution_context, credential_id,
                use_service_account, enabled, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Device Plus Filter Job",
                component_json,
                json.dumps(
                    [
                        {
                            "kind": "device",
                            "hostname": "test-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                        {"kind": "filter", "filter_id": 7, "name": "Windows Devices"},
                    ]
                ),
                "once",
                1_700_000_830,
                0,
                "no_expire",
                "system",
                None,
                0,
                1,
                1_700_000_830,
                1_700_000_830,
            ),
        )

        cur.execute(
            """
            INSERT INTO scheduled_job_runs (
                job_id, scheduled_ts, started_ts, finished_ts, status, error,
                created_at, updated_at, target_hostname, skip_reason, shared_execution
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                mixed_job_id,
                1_700_000_840,
                1_700_000_841,
                1_700_000_842,
                "success",
                "",
                1_700_000_840,
                1_700_000_842,
                "test-device",
                "",
                0,
            ),
        )
        targeted_run_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO scheduled_job_runs (
                job_id, scheduled_ts, started_ts, finished_ts, status, error,
                created_at, updated_at, target_hostname, skip_reason, shared_execution
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                mixed_job_id,
                1_700_000_850,
                1_700_000_851,
                1_700_000_852,
                "success",
                "",
                1_700_000_850,
                1_700_000_852,
                None,
                "",
                1,
            ),
        )
        shared_run_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets (
                run_id, device_guid, hostname, site_id, created_at
            )
            VALUES (?, ?, ?, ?, ?)
            """,
            (targeted_run_id, valid_guid, "test-device", 1, 1_700_000_840),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets (
                run_id, device_guid, hostname, site_id, created_at
            )
            VALUES (?, ?, ?, ?, ?)
            """,
            (shared_run_id, valid_guid, "test-device", 1, 1_700_000_850),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets (
                run_id, device_guid, hostname, site_id, created_at
            )
            VALUES (?, ?, ?, ?, ?)
            """,
            (shared_run_id, other_guid, "survivor-device", 1, 1_700_000_850),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_activity (
                run_id, activity_id, component_kind, script_type, component_path, component_name, created_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (
                targeted_run_id,
                activity_id,
                "script",
                "powershell",
                "Scripts/noop.ps1",
                "noop",
                1_700_000_842,
            ),
        )
        cur.execute(
            """
            INSERT INTO ansible_play_recaps (
                run_id, hostname, agent_id, playbook_path, playbook_name, scheduled_job_id,
                scheduled_run_id, activity_job_id, status, recap_text, recap_json, started_ts,
                finished_ts, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "ansible-run-1",
                "test-device",
                agent_id,
                "Playbooks/sample.yml",
                "sample.yml",
                mixed_job_id,
                targeted_run_id,
                activity_id,
                "success",
                "ok",
                "{}",
                1_700_000_841,
                1_700_000_842,
                1_700_000_842,
                1_700_000_842,
            ),
        )
        cur.execute(
            """
            INSERT INTO workflow_runs (
                workflow_guid, workflow_name, source_type, graph_snapshot_json, status, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "workflow-guid-1",
                "Workflow 1",
                "scheduled_job",
                "{}",
                "success",
                1_700_000_860,
                1_700_000_860,
            ),
        )
        workflow_run_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO workflow_node_runs (
                workflow_run_id, node_id, node_type, node_label, status, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (
                workflow_run_id,
                "node-1",
                "script",
                "Node 1",
                "success",
                1_700_000_861,
                1_700_000_861,
            ),
        )
        workflow_node_run_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO workflow_child_jobs (
                workflow_run_id, workflow_node_run_id, child_kind, child_identifier, activity_id,
                target_hostname, component_guid, component_name, component_kind, status,
                stdout_summary, stderr_summary, payload_json, created_at, updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                workflow_run_id,
                workflow_node_run_id,
                "script",
                "child-1",
                activity_id,
                "test-device",
                "component-guid-1",
                "Child Job",
                "script",
                "success",
                "ok",
                "",
                "{}",
                1_700_000_862,
                1_700_000_862,
            ),
        )
        cur.execute(
            """
            INSERT INTO device_vpn_config (agent_id, allowed_ports, updated_at, updated_by)
            VALUES (?, ?, ?, ?)
            """,
            (agent_id, "22,3389", "2025-01-01T00:00:00+00:00", "admin"),
        )
        cur.execute(
            """
            INSERT INTO device_vpn_ip_leases (agent_id, virtual_ip, updated_at)
            VALUES (?, ?, ?)
            """,
            (agent_id, "10.255.0.25/32", "2025-01-01T00:00:00+00:00"),
        )
        cur.execute(
            """
            INSERT INTO agent_service_account (
                agent_id, username, password_hash, password_encrypted, last_rotated_utc, version
            )
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (agent_id, "svc-test-device", b"hash", b"encrypted", "2025-01-01T00:00:00+00:00", 1),
        )
        conn.commit()

    class _PurgeTunnelService:
        def __init__(self) -> None:
            self.disconnect_calls: list[tuple[str, str, bool]] = []

        def disconnect(
            self,
            agent_id_value: str,
            reason: str = "operator_stop",
            *,
            force: bool = False,
            operator_id=None,
        ):
            self.disconnect_calls.append((agent_id_value, reason, force))
            return True

    class _PurgeVncRegistry:
        def __init__(self) -> None:
            self.revoked: list[str] = []

        def revoke_agent(self, agent_id_value: str) -> int:
            self.revoked.append(agent_id_value)
            return 2

    tunnel_service = _PurgeTunnelService()
    vnc_registry = _PurgeVncRegistry()
    harness.context.vpn_tunnel_service = tunnel_service
    harness.context.vnc_registry = vnc_registry

    client = _client_with_admin_session(harness)
    response = client.post(f"/api/devices/{valid_guid}/purge")

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "purged"
    assert payload["device_guid"] == valid_guid
    assert payload["hostname"] == "test-device"
    assert payload["required_token_version"] == 2
    assert payload["scheduled_jobs"] == {"updated": 2, "deleted": 1, "targets_removed": 3}
    assert payload["deleted_rows"]["devices"] == 1
    assert payload["runtime_cleanup"]["vpn_disconnected"] is True
    assert payload["runtime_cleanup"]["vnc_sessions_revoked"] == 2
    assert tunnel_service.disconnect_calls == [(agent_id, "device_purged", True)]
    assert vnc_registry.revoked == [agent_id]

    listed = client.get("/api/devices")
    assert listed.status_code == 200
    listed_guids = {
        (item.get("agent_guid") or item.get("guid") or "").upper()
        for item in listed.get_json()["devices"]
    }
    assert valid_guid not in listed_guids

    stale_client = harness.app.test_client()
    stale_auth = stale_client.get(
        "/api/repo/current_hash?repo=test/test&branch=main",
        headers=_device_headers_for_guid(valid_guid),
    )
    assert stale_auth.status_code == 401
    assert stale_auth.get_json()["error"] == "device_purged"

    with sqlite3.connect(str(harness.db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT required_token_version, last_hostname, last_agent_id
              FROM device_purge_barriers
             WHERE guid = ?
            """,
            (valid_guid,),
        )
        barrier_row = cur.fetchone()
        cur.execute("SELECT COUNT(*) FROM devices WHERE guid = ?", (valid_guid,))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM device_keys WHERE guid = ?", (valid_guid,))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM refresh_tokens WHERE guid = ?", (valid_guid,))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM device_software_inventory WHERE device_guid = ?", (valid_guid,))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM device_sites WHERE LOWER(device_hostname) = LOWER(?)", ("test-device",))
        assert cur.fetchone()[0] == 0
        cur.execute(
            "SELECT COUNT(*) FROM device_approvals WHERE guid = ? OR LOWER(hostname_claimed) = LOWER(?)",
            (valid_guid, "test-device"),
        )
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM activity_history WHERE LOWER(hostname) = LOWER(?)", ("test-device",))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM scheduled_job_runs WHERE LOWER(target_hostname) = LOWER(?)", ("test-device",))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM scheduled_job_run_activity")
        assert cur.fetchone()[0] == 0
        cur.execute(
            """
            SELECT hostname
              FROM scheduled_job_run_targets
          ORDER BY hostname ASC
            """
        )
        remaining_target_hosts = [row[0] for row in cur.fetchall()]
        cur.execute(
            "SELECT COUNT(*) FROM ansible_play_recaps WHERE LOWER(hostname) = LOWER(?) OR agent_id = ?",
            ("test-device", agent_id),
        )
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM workflow_child_jobs WHERE LOWER(target_hostname) = LOWER(?)", ("test-device",))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM device_vpn_config WHERE agent_id = ?", (agent_id,))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM device_vpn_ip_leases WHERE agent_id = ?", (agent_id,))
        assert cur.fetchone()[0] == 0
        cur.execute("SELECT COUNT(*) FROM agent_service_account WHERE agent_id = ?", (agent_id,))
        assert cur.fetchone()[0] == 0
        cur.execute(
            """
            SELECT name, targets_json
              FROM scheduled_jobs
          ORDER BY name ASC
            """
        )
        scheduled_jobs = {name: json.loads(targets_json or "[]") for name, targets_json in cur.fetchall()}

    assert barrier_row == (2, "test-device", agent_id)
    assert remaining_target_hosts == ["survivor-device"]
    assert "Single Device Job" not in scheduled_jobs
    assert scheduled_jobs["Mixed Device Job"] == [
        {
            "kind": "device",
            "device_guid": other_guid.lower(),
            "hostname": "survivor-device",
            "site_id": 1,
            "site_name": "Main Lab",
        }
    ]
    assert scheduled_jobs["Filter Only Job"] == [
        {"kind": "filter", "filter_id": 7, "name": "Windows Devices"}
    ]
    assert scheduled_jobs["Device Plus Filter Job"] == [
        {"kind": "filter", "filter_id": 7, "name": "Windows Devices"}
    ]
