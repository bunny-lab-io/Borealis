# ======================================================
# Data\Engine\Unit_Tests\test_rbac_api.py
# Description: Covers Borealis RBAC site-assignment flows and operator
#              site scoping across inventory, approvals, filters, and jobs.
# ======================================================

from __future__ import annotations

import json

import pytest
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.server import create_app
from Data.Engine.services.API.devices import tunnel as tunnel_api

from .conftest import EngineTestHarness


ADMIN_SELECTION_MESSAGE = (
    "An administrator was selected, admins inherantly have access to all managed sites.  "
    "Please unselect the admin and try again."
)
MIXED_ASSIGNMENT_WARNING = (
    "The users selected for site assignment are members of different sites.  "
    "Changes made here will overwrite existing site assignments for the selected users."
)
RBAC_API_GROUPS = (
    "core",
    "auth",
    "tokens",
    "enrollment",
    "devices",
    "assemblies",
    "filters",
    "scheduled_jobs",
)


def _client_with_session(app, username: str, role: str):
    client = app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = username
        sess["role"] = role
    return client


def _admin_client(harness: EngineTestHarness):
    return _client_with_session(harness.app, "admin", "Admin")


def _operator_client(harness: EngineTestHarness):
    return _client_with_session(harness.app, "operator", "User")


def _fresh_client(
    harness: EngineTestHarness,
    *,
    username: str,
    role: str,
    api_groups=RBAC_API_GROUPS,
):
    config = {
        "DATABASE_URL": f"sqlite:///{harness.db_path.as_posix()}",
        "TLS_CERT_PATH": harness.app.config["TLS_CERT_PATH"],
        "TLS_KEY_PATH": harness.app.config["TLS_KEY_PATH"],
        "TLS_BUNDLE_PATH": harness.app.config["TLS_BUNDLE_PATH"],
        "SECRET_KEY": harness.app.config["SECRET_KEY"],
        "LOG_FILE": harness.app.config["LOG_FILE"],
        "ERROR_LOG_FILE": harness.app.config["ERROR_LOG_FILE"],
        "API_LOG_FILE": harness.app.config["API_LOG_FILE"],
        "VPN_TUNNEL_LOG_FILE": harness.app.config["VPN_TUNNEL_LOG_FILE"],
        "STATIC_FOLDER": harness.app.config["STATIC_FOLDER"],
        "API_GROUPS": tuple(api_groups),
    }
    app, _socketio, context = create_app(config)
    app.config.update(TESTING=True)
    return _client_with_session(app, username, role), context


def _insert_device(
    cur: sqlite3.Cursor,
    *,
    guid: str,
    hostname: str,
    agent_id: str,
    description: str,
    connection_type: str = "",
    site_id: int | None = None,
) -> None:
    cur.execute(
        """
        INSERT INTO devices (
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
            guid,
            hostname,
            description,
            1_700_000_100,
            f"hash-{hostname}",
            json.dumps([]),
            json.dumps([]),
            json.dumps([]),
            json.dumps([]),
            json.dumps({"name": "AMD Ryzen"}),
            "Workstation",
            "example.local",
            "",
            f"10.0.0.{len(hostname) + 10}",
            "2025-10-01T00:00:00Z",
            1_700_000_900,
            "Taylor",
            "Windows 11 Pro",
            1200,
            agent_id,
            connection_type,
            "",
            f"FP-{hostname}",
            1,
            "active",
            "2025-10-01T00:00:00Z",
        ),
    )
    if site_id is not None:
        cur.execute(
            "INSERT INTO device_sites (device_hostname, site_id, assigned_at) VALUES (?, ?, ?)",
            (hostname, site_id, 1_700_000_950),
        )


def _seed_rbac_inventory(harness: EngineTestHarness) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO sites (id, name, description, created_at, enrollment_code) VALUES (?, ?, ?, ?, ?)",
            (2, "Branch Lab", "Secondary integration site", 1_700_000_100, "SITE-BRANCH-CODE"),
        )
        _insert_device(
            cur,
            guid="GUID-OTHER-0002",
            hostname="other-device",
            agent_id="other-device-agent",
            description="Operator out-of-scope device",
            site_id=2,
        )
        _insert_device(
            cur,
            guid="GUID-UNASSIGNED-0003",
            hostname="unassigned-device",
            agent_id="unassigned-device-agent",
            description="Admin-only unassigned device",
            site_id=None,
        )
        cur.executemany(
            """
            INSERT INTO users (id, username, display_name, password_sha512, role, last_login, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                (2, "operator", "Operator", "test", "User", 0, 0, 0),
                (3, "operator-two", "Operator Two", "test", "User", 0, 0, 0),
            ),
        )
        cur.executemany(
            "INSERT INTO user_site_assignments (user_id, site_id, assigned_at) VALUES (?, ?, ?)",
            (
                (2, 1, 1_700_000_999),
                (3, 2, 1_700_001_000),
            ),
        )
        cur.execute(
            """
            INSERT INTO device_approvals (
                id,
                approval_reference,
                guid,
                hostname_claimed,
                ssl_key_fingerprint_claimed,
                enrollment_code,
                site_id,
                status,
                client_nonce,
                server_nonce,
                agent_pubkey_der,
                created_at,
                updated_at,
                approved_by_user_id
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "approval-2",
                "APP-REF-2",
                None,
                "branch-pending-device",
                "ee:ff:gg:hh",
                "SITE-BRANCH-CODE",
                2,
                "pending",
                "branch-client",
                "branch-server",
                None,
                "2025-01-02T00:00:00Z",
                "2025-01-02T00:00:00Z",
                None,
            ),
        )
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
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                55,
                "All Devices Filter",
                "Global filter for RBAC scheduled job tests",
                0,
                "basic",
                "global",
                json.dumps({}),
                json.dumps({"groups": []}),
                "admin",
                1_700_001_100,
                1_700_001_100,
            ),
        )
        conn.commit()
    finally:
        conn.close()


def _insert_filter(
    harness: EngineTestHarness,
    *,
    filter_id: int,
    name: str,
    site_mode: str,
    site_ids: list[int] | None = None,
    updated_at: int | None = None,
) -> None:
    conn = sqlite3.connect(str(harness.db_path))
    try:
        cur = conn.cursor()
        ts = updated_at or (1_700_002_000 + int(filter_id))
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
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                int(filter_id),
                name,
                f"{name} description",
                0,
                "advanced",
                site_mode,
                json.dumps({"criteria": []}),
                json.dumps(
                    {
                        "groups": [
                            {
                                "join_with": "",
                                "conditions": [
                                    {
                                        "field": "hostname",
                                        "operator": "contains",
                                        "value": "device",
                                    }
                                ],
                            }
                        ]
                    }
                ),
                "admin",
                ts,
                ts,
            ),
        )
        for site_id in site_ids or []:
            cur.execute(
                "INSERT INTO device_filter_sites(filter_id, site_id) VALUES (?, ?)",
                (int(filter_id), int(site_id)),
            )
        conn.commit()
    finally:
        conn.close()


class _FakeTunnelService:
    def __init__(self) -> None:
        self.bumped_agents: list[str] = []

    def status(self, agent_id: str):
        return {
            "status": "up",
            "agent_id": agent_id,
            "endpoint": "engine.local:30000",
            "listener_healthy": True,
        }

    def connect(self, *, agent_id: str, operator_id=None, endpoint_host=None):
        return {
            "tunnel_id": "tun-1",
            "agent_id": agent_id,
            "virtual_ip": "10.255.0.2/32",
            "engine_virtual_ip": "10.255.0.1/32",
            "endpoint": "engine.local:30000",
        }

    def list_sessions(self):
        return [
            {
                "tunnel_id": "tun-main",
                "agent_id": "test-device-agent",
                "status": "up",
                "listener_healthy": True,
            },
            {
                "tunnel_id": "tun-branch",
                "agent_id": "other-device-agent",
                "status": "up",
                "listener_healthy": True,
            },
        ]

    def bump_activity(self, agent_id: str) -> None:
        self.bumped_agents.append(agent_id)


def test_user_site_assignment_selection_rejects_admin_selection(engine_harness: EngineTestHarness) -> None:
    _seed_rbac_inventory(engine_harness)
    client = _admin_client(engine_harness)

    response = client.post(
        "/api/user_site_assignments/selection",
        json={"usernames": ["admin", "operator"]},
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert payload["error"] == "admin_selected"
    assert payload["message"] == ADMIN_SELECTION_MESSAGE


def test_user_site_assignment_selection_and_replace_assignments(engine_harness: EngineTestHarness) -> None:
    _seed_rbac_inventory(engine_harness)
    client = _admin_client(engine_harness)

    mixed_response = client.post(
        "/api/user_site_assignments/selection",
        json={"usernames": ["operator", "operator-two"]},
    )

    assert mixed_response.status_code == 200
    mixed_payload = mixed_response.get_json()
    assert mixed_payload["has_mixed_assignments"] is True
    assert mixed_payload["selected_site_ids"] == []
    assert mixed_payload["warning"] == MIXED_ASSIGNMENT_WARNING

    assign_response = client.post(
        "/api/user_site_assignments/assign",
        json={"usernames": ["operator", "operator-two"], "site_ids": [1]},
    )

    assert assign_response.status_code == 200
    assign_payload = assign_response.get_json()
    assert assign_payload["assigned_user_count"] == 2
    assert assign_payload["assigned_site_ids"] == [1]

    identical_response = client.post(
        "/api/user_site_assignments/selection",
        json={"usernames": ["operator", "operator-two"]},
    )

    assert identical_response.status_code == 200
    identical_payload = identical_response.get_json()
    assert identical_payload["has_mixed_assignments"] is False
    assert identical_payload["selected_site_ids"] == [1]
    assert identical_payload["warning"] == ""

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        rows = conn.execute(
            "SELECT user_id, site_id FROM user_site_assignments ORDER BY user_id, site_id"
        ).fetchall()
    finally:
        conn.close()

    assert rows == [(2, 1), (3, 1)]


def test_operator_inventory_endpoints_hide_out_of_scope_and_unassigned_devices(
    engine_harness: EngineTestHarness,
) -> None:
    _seed_rbac_inventory(engine_harness)
    client = _operator_client(engine_harness)

    devices_response = client.get("/api/devices")
    assert devices_response.status_code == 200
    hostnames = [row["hostname"] for row in devices_response.get_json()["devices"]]
    assert hostnames == ["test-device"]

    sites_response = client.get("/api/sites")
    assert sites_response.status_code == 200
    sites = sites_response.get_json()["sites"]
    assert [site["id"] for site in sites] == [1]
    assert [site["name"] for site in sites] == ["Main Lab"]

    detail_response = client.get("/api/device/details/other-device")
    assert detail_response.status_code == 200
    assert detail_response.get_json() == {}

    unassigned_detail_response = client.get("/api/device/details/unassigned-device")
    assert unassigned_detail_response.status_code == 200
    assert unassigned_detail_response.get_json() == {}

    description_response = client.post(
        "/api/device/description/other-device",
        json={"description": "Nope"},
    )
    assert description_response.status_code == 404

    site_map_response = client.get(
        "/api/sites/device_map?hostnames=test-device,other-device,unassigned-device"
    )
    assert site_map_response.status_code == 200
    assert site_map_response.get_json()["mapping"] == {
        "test-device": {"site_id": 1, "site_name": "Main Lab"}
    }


def test_operator_approval_queue_and_tunnel_access_are_site_scoped(
    engine_harness: EngineTestHarness,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _seed_rbac_inventory(engine_harness)
    client = _operator_client(engine_harness)

    approvals_response = client.get("/api/admin/device-approvals")
    assert approvals_response.status_code == 200
    approvals = approvals_response.get_json()["approvals"]
    assert [record["id"] for record in approvals] == ["approval-1"]

    deny_response = client.post("/api/admin/device-approvals/approval-2/deny")
    assert deny_response.status_code == 404

    fake_service = _FakeTunnelService()
    monkeypatch.setattr(tunnel_api, "_get_tunnel_service", lambda _adapters: fake_service)

    status_response = client.get("/api/tunnel/status?agent_id=other-device-agent&bump=1")
    assert status_response.status_code == 404

    active_response = client.get("/api/tunnel/active")
    assert active_response.status_code == 200
    tunnels = active_response.get_json()["tunnels"]
    assert [row["agent_id"] for row in tunnels] == ["test-device-agent"]


def test_operator_filter_preview_rejects_out_of_scope_sites(engine_harness: EngineTestHarness) -> None:
    _seed_rbac_inventory(engine_harness)
    client, _context = _fresh_client(engine_harness, username="operator", role="User")

    response = client.post(
        "/api/device_filters/preview",
        json={
            "name": "Branch Devices",
            "description": "Should be rejected for operator scope",
            "site_mode": "specific_sites",
            "site_ids": [2],
            "criteria": {
                "groups": [
                    {
                        "join_with": "",
                        "conditions": [
                            {
                                "field": "hostname",
                                "operator": "contains",
                                "value": "other-device",
                            }
                        ],
                    }
                ]
            },
        },
    )

    assert response.status_code == 403
    payload = response.get_json()
    assert payload["error"] == "out_of_scope_sites"


def test_operator_filter_visibility_uses_full_effective_filter_scope(
    engine_harness: EngineTestHarness,
) -> None:
    _seed_rbac_inventory(engine_harness)
    _insert_filter(
        engine_harness,
        filter_id=56,
        name="Main Only Filter",
        site_mode="specific_sites",
        site_ids=[1],
    )
    _insert_filter(
        engine_harness,
        filter_id=57,
        name="Main And Branch Filter",
        site_mode="specific_sites",
        site_ids=[1, 2],
    )
    _insert_filter(
        engine_harness,
        filter_id=58,
        name="Exclude Branch Filter",
        site_mode="global_exclusions",
        site_ids=[2],
    )
    _insert_filter(
        engine_harness,
        filter_id=59,
        name="Exclude Main Filter",
        site_mode="global_exclusions",
        site_ids=[1],
    )

    client, _context = _fresh_client(engine_harness, username="operator", role="User")

    list_response = client.get("/api/device_filters")
    assert list_response.status_code == 200
    filters = list_response.get_json()["filters"]
    visible_ids = {int(record["id"]) for record in filters}
    visible_names = {record["name"] for record in filters}

    assert visible_ids == {56, 58}
    assert visible_names == {"Main Only Filter", "Exclude Branch Filter"}

    assert client.get("/api/device_filters/56").status_code == 200
    assert client.get("/api/device_filters/58").status_code == 200
    assert client.get("/api/device_filters/55").status_code == 404
    assert client.get("/api/device_filters/57").status_code == 404
    assert client.get("/api/device_filters/59").status_code == 404


def test_operator_can_create_filter_when_exclusions_reduce_scope_to_visible_sites(
    engine_harness: EngineTestHarness,
) -> None:
    _seed_rbac_inventory(engine_harness)
    client, _context = _fresh_client(engine_harness, username="operator", role="User")

    allowed_response = client.post(
        "/api/device_filters",
        json={
            "name": "Operator Main Scope",
            "description": "Visible exclusion filter",
            "site_mode": "global_exclusions",
            "site_ids": [2],
            "criteria": {
                "groups": [
                    {
                        "join_with": "",
                        "conditions": [
                            {
                                "field": "hostname",
                                "operator": "contains",
                                "value": "test-device",
                            }
                        ],
                    }
                ]
            },
        },
    )

    assert allowed_response.status_code == 201
    created_filter = allowed_response.get_json()["filter"]
    assert created_filter["site_mode"] == "global_exclusions"
    assert created_filter["site_ids"] == [2]

    rejected_response = client.post(
        "/api/device_filters",
        json={
            "name": "Operator Global Scope",
            "description": "Should be hidden because it spans every site",
            "site_mode": "global",
            "criteria": {
                "groups": [
                    {
                        "join_with": "",
                        "conditions": [
                            {
                                "field": "hostname",
                                "operator": "contains",
                                "value": "device",
                            }
                        ],
                    }
                ]
            },
        },
    )

    assert rejected_response.status_code == 403
    rejected_payload = rejected_response.get_json()
    assert rejected_payload["error"] == "out_of_scope_sites"


def test_operator_scheduled_job_targets_reject_out_of_scope_devices_and_persist_filter_scope(
    engine_harness: EngineTestHarness,
) -> None:
    _seed_rbac_inventory(engine_harness)
    client, _context = _fresh_client(engine_harness, username="operator", role="User")

    rejected_response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Branch Device Job",
            "components": [{"type": "ansible", "name": "Playbook A"}],
            "targets": ["other-device"],
            "schedule": {"type": "immediately"},
            "execution_context": "local",
            "enabled": True,
        },
    )

    assert rejected_response.status_code == 403
    rejected_payload = rejected_response.get_json()
    assert rejected_payload["error"] == "out_of_scope_targets"
    assert "outside your assigned sites" in rejected_payload["message"]

    created_response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Scoped Filter Job",
            "components": [{"type": "ansible", "name": "Playbook A"}],
            "targets": [{"kind": "filter", "filter_id": 55, "name": "All Devices Filter"}],
            "schedule": {"type": "immediately"},
            "execution_context": "local",
            "enabled": True,
        },
    )

    assert created_response.status_code == 200
    job_payload = created_response.get_json()["job"]
    assert job_payload["targets"] == [
        {
            "kind": "filter",
            "filter_id": 55,
            "name": "All Devices Filter",
            "allowed_site_ids": [1],
        }
    ]

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        row = conn.execute(
            "SELECT targets_json FROM scheduled_jobs WHERE name = ?",
            ("Scoped Filter Job",),
        ).fetchone()
    finally:
        conn.close()

    assert row is not None
    assert json.loads(row[0]) == [
        {
            "kind": "filter",
            "filter_id": 55,
            "name": "All Devices Filter",
            "allowed_site_ids": [1],
        }
    ]
