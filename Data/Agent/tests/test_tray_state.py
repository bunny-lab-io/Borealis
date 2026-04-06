from __future__ import annotations

from pathlib import Path

import Data.Agent.tray_state as tray_state


def _setup_start_path(tmp_path: Path) -> Path:
    (tmp_path / "Borealis.ps1").write_text("", encoding="utf-8")
    start = tmp_path / "Data" / "Agent"
    start.mkdir(parents=True, exist_ok=True)
    return start


def _wireguard_role(status_code: str, detail: str) -> dict:
    return {
        "roles": [
            {
                "role_label": "WireGuard Service",
                "status_code": status_code,
                "detail": detail,
            }
        ],
        "reported_at": 100,
    }


def _snapshot(
    service_mode: str,
    *,
    now: int,
    started_at: int,
    auth_at: int = 0,
    heartbeat_at: int = 0,
    role_health: dict | None = None,
    error_kind: str = "",
    error_message: str = "",
    socket_connected: bool = True,
    verify_enabled: bool = True,
) -> dict:
    snapshot = tray_state.build_default_snapshot(
        service_mode,
        now=now,
        started_at=started_at,
        pid=1234,
        server_url="https://borealis.example.com",
    )
    snapshot["guid_present"] = True
    snapshot["last_auth_success_at"] = auth_at
    snapshot["last_heartbeat_success_at"] = heartbeat_at
    snapshot["role_health"] = role_health or {"roles": [], "reported_at": now}
    snapshot["last_error_kind"] = error_kind
    snapshot["last_error_message"] = error_message
    snapshot["socket_connected"] = socket_connected
    snapshot["verify_enabled"] = verify_enabled
    snapshot["updated_at"] = now
    return snapshot


def test_status_snapshot_round_trip(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    snapshot = _snapshot(
        "currentuser",
        now=101,
        started_at=95,
        auth_at=100,
        heartbeat_at=101,
    )

    tray_state.write_status_snapshot("currentuser", snapshot, start=start, now=101)
    loaded = tray_state.load_status_snapshot("currentuser", start=start)

    assert loaded["service_mode"] == "currentuser"
    assert loaded["server_host"] == "borealis.example.com"
    assert loaded["guid_present"] is True
    assert loaded["last_heartbeat_success_at"] == 101


def test_request_restart_and_consume_request(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)

    payload = tray_state.request_restart(
        ("currentuser", "system"),
        start=start,
        request_id="restart-123",
        requested_at=500,
        requested_by="tray-test",
        requested_by_pid=999,
    )

    assert payload["request_id"] == "restart-123"
    assert tray_state.load_restart_request("currentuser", start=start)["request_id"] == "restart-123"
    assert tray_state.load_restart_request("system", start=start)["request_id"] == "restart-123"

    consumed = tray_state.consume_restart_request("currentuser", start=start)

    assert consumed["request_id"] == "restart-123"
    assert tray_state.load_restart_request("currentuser", start=start) == {}
    assert tray_state.load_restart_request("system", start=start)["request_id"] == "restart-123"


def test_build_tray_view_connected_state(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    current = _snapshot("currentuser", now=200, started_at=60, auth_at=180, heartbeat_at=190)
    system = _snapshot(
        "system",
        now=200,
        started_at=60,
        auth_at=181,
        heartbeat_at=191,
        role_health=_wireguard_role("healthy", "Persistent tunnel active."),
    )

    view = tray_state.build_tray_view(
        current_snapshot=current,
        system_snapshot=system,
        busy_snapshot={"busy": False, "reasons": [], "entries": []},
        now=220,
        hostname="workstation-01",
        build_id="abc1234",
        agent_guid="guid-01",
        start=start,
    )

    labels = [entry["label"] for entry in view["menu_entries"] if entry.get("type") != "separator"]

    assert view["overall_status"] == "Connected"
    assert view["security_status"] == "Secure connection"
    assert view["activity_status"] == "Idle"
    assert view["wireguard_status"] == "Ready"
    assert labels == [
        "Borealis Agent",
        "Status: Connected",
        "Security: Secure connection",
        "Activity: Idle",
        "Connected to: borealis.example.com",
        "Last check-in: 29s ago",
        "View Status Details...",
        "Restart Agent...",
    ]
    assert "Quit Agent" not in labels


def test_build_tray_view_starting_state_uses_busy_activity(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    current = _snapshot("currentuser", now=200, started_at=190)
    system = _snapshot(
        "system",
        now=200,
        started_at=190,
        role_health=_wireguard_role("recovering", "Awaiting persistent tunnel session bootstrap."),
    )

    view = tray_state.build_tray_view(
        current_snapshot=current,
        system_snapshot=system,
        busy_snapshot={"busy": True, "reasons": ["remote_shell"], "entries": []},
        now=205,
        hostname="workstation-02",
        start=start,
    )

    assert view["overall_status"] == "Starting up"
    assert view["security_status"] == "Checking connection"
    assert view["activity_status"] == "Remote shell active"
    assert view["wireguard_status"] == "Starting"
    assert view["icon_tone"] == "neutral"


def test_build_tray_view_reports_tls_and_wireguard_attention(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    current = _snapshot(
        "currentuser",
        now=300,
        started_at=120,
        auth_at=290,
        heartbeat_at=291,
        error_kind="tls",
        error_message="CERTIFICATE_VERIFY_FAILED",
    )
    system = _snapshot(
        "system",
        now=300,
        started_at=120,
        auth_at=289,
        heartbeat_at=292,
        role_health=_wireguard_role("unhealthy", "Persistent ensure loop stopped."),
    )

    view = tray_state.build_tray_view(
        current_snapshot=current,
        system_snapshot=system,
        busy_snapshot={"busy": False, "reasons": [], "entries": []},
        now=305,
        hostname="workstation-03",
        start=start,
    )

    assert view["overall_status"] == "Needs attention"
    assert view["security_status"] == "Certificate trust issue"
    assert view["wireguard_status"] == "Needs attention"
    assert view["icon_tone"] == "error"
    assert "CERTIFICATE_VERIFY_FAILED" in view["warnings"]


def test_build_tray_view_reports_auth_attention(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    current = _snapshot(
        "currentuser",
        now=300,
        started_at=120,
        auth_at=290,
        heartbeat_at=291,
        error_kind="auth",
        error_message="Refresh token rejected",
    )
    system = _snapshot(
        "system",
        now=300,
        started_at=120,
        auth_at=289,
        heartbeat_at=292,
        role_health=_wireguard_role("healthy", "Persistent tunnel active."),
    )

    view = tray_state.build_tray_view(
        current_snapshot=current,
        system_snapshot=system,
        busy_snapshot={"busy": False, "reasons": [], "entries": []},
        now=305,
        start=start,
    )

    assert view["overall_status"] == "Needs attention"
    assert view["security_status"] == "Sign-in problem"
    assert "Refresh token rejected" in view["warnings"]


def test_build_tray_view_uses_configured_server_host_when_snapshots_are_missing(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    settings_dir = tmp_path / "Agent" / "Borealis" / "Settings"
    settings_dir.mkdir(parents=True, exist_ok=True)
    (settings_dir / "server_url.txt").write_text("borealis.example.com\n", encoding="utf-8")

    view = tray_state.build_tray_view(
        current_snapshot={},
        system_snapshot={},
        busy_snapshot={"busy": False, "reasons": [], "entries": []},
        now=305,
        start=start,
    )

    assert view["connected_host"] == "borealis.example.com"


def test_build_tray_view_with_one_missing_snapshot_stays_starting(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    current = _snapshot("currentuser", now=200, started_at=60, auth_at=180, heartbeat_at=190)

    view = tray_state.build_tray_view(
        current_snapshot=current,
        system_snapshot={},
        busy_snapshot={"busy": False, "reasons": [], "entries": []},
        now=220,
        start=start,
    )

    assert view["overall_status"] == "Starting up"
    assert view["security_status"] == "Checking connection"


def test_support_detail_order_is_fixed(tmp_path: Path) -> None:
    start = _setup_start_path(tmp_path)
    current = _snapshot("currentuser", now=200, started_at=60, auth_at=180, heartbeat_at=190)
    system = _snapshot(
        "system",
        now=200,
        started_at=60,
        auth_at=181,
        heartbeat_at=191,
        role_health=_wireguard_role("healthy", "Persistent tunnel active."),
    )
    view = tray_state.build_tray_view(
        current_snapshot=current,
        system_snapshot=system,
        busy_snapshot={"busy": False, "reasons": ["quick_job_system"], "entries": []},
        now=220,
        hostname="workstation-04",
        build_id="build-42",
        agent_guid="guid-42",
        start=start,
    )

    labels = [item["label"] for item in tray_state.build_support_details(view)]

    assert labels == [
        "Device",
        "Status",
        "Security",
        "Connected to",
        "Last check-in",
        "Activity",
        "WireGuard",
        "Build",
        "Agent ID",
        "Warnings",
    ]
