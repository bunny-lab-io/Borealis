# ======================================================
# Data\Engine\Unit_Tests\test_scheduled_jobs_api.py
# Description: Covers scheduled job API behavior across Borealis
#              DB-API adapter paths.
# ======================================================

from __future__ import annotations

import json
import socket
import sys
from types import SimpleNamespace

import pytest

from Data.Engine.services.API.scheduled_jobs import job_scheduler as scheduled_job_module
from Data.Engine.services.API.scheduled_jobs.onboarding import parse_onboarding_scope
from Data.Engine.services.API.scheduled_jobs.targets import normalize_targets_for_save
from Data.Engine.services.auth import UserSiteAccessManager
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.server import create_app

from .conftest import EngineTestHarness


def _scheduled_jobs_client(engine_harness: EngineTestHarness):
    client, scheduler, _context = _scheduled_jobs_client_with_context(engine_harness)
    return client, scheduler


def _scheduled_jobs_client_with_context(engine_harness: EngineTestHarness):
    config = {
        "DATABASE_URL": f"sqlite:///{engine_harness.db_path.as_posix()}",
        "TLS_CERT_PATH": engine_harness.app.config["TLS_CERT_PATH"],
        "TLS_KEY_PATH": engine_harness.app.config["TLS_KEY_PATH"],
        "TLS_BUNDLE_PATH": engine_harness.app.config["TLS_BUNDLE_PATH"],
        "SECRET_KEY": engine_harness.app.config["SECRET_KEY"],
        "LOG_FILE": engine_harness.app.config["LOG_FILE"],
        "ERROR_LOG_FILE": engine_harness.app.config["ERROR_LOG_FILE"],
        "STATIC_FOLDER": engine_harness.app.config["STATIC_FOLDER"],
        "API_GROUPS": ("core", "auth", "tokens", "enrollment", "devices", "assemblies", "scheduled_jobs"),
    }
    app, _socketio, context = create_app(config)
    app.config.update(TESTING=True)
    if getattr(context, "scheduler", None) is not None:
        context.scheduler.RETENTION_DAYS = 3650
    client = app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client, context.scheduler, context


class _MissingLastRowIdCursor:
    def __init__(self, inner) -> None:
        self._inner = inner
        self.lastrowid = None
        self.rowcount = getattr(inner, "rowcount", 0)

    def execute(self, sql, params=()):
        result = self._inner.execute(sql, params)
        normalized = " ".join(str(sql or "").strip().upper().split())
        if normalized.startswith("INSERT INTO SCHEDULED_JOBS"):
            self.lastrowid = None
        else:
            self.lastrowid = getattr(self._inner, "lastrowid", None)
        self.rowcount = getattr(self._inner, "rowcount", 0)
        return result

    def fetchone(self):
        return self._inner.fetchone()

    def fetchall(self):
        return self._inner.fetchall()

    def __getattr__(self, name):
        return getattr(self._inner, name)


class _MissingLastRowIdConnection:
    def __init__(self, inner) -> None:
        self._inner = inner

    def cursor(self):
        return _MissingLastRowIdCursor(self._inner.cursor())

    def __getattr__(self, name):
        return getattr(self._inner, name)


class _FakePreflightSocket:
    def __init__(self, responses) -> None:
        self._responses = list(responses)
        self.closed = False
        self.timeouts = []

    def settimeout(self, value) -> None:
        self.timeouts.append(value)

    def recv(self, _size: int) -> bytes:
        if not self._responses:
            return b""
        response = self._responses.pop(0)
        if isinstance(response, BaseException):
            raise response
        return response

    def close(self) -> None:
        self.closed = True


def test_onboarding_scope_parser_expands_supported_targets() -> None:
    targets, errors = parse_onboarding_scope(
        ["192.168.10.5", "192.168.10.10-12", "10.0.0.0/30", "server01.lab"],
        max_targets=16,
    )

    assert errors == []
    assert [target["host"] for target in targets] == [
        "192.168.10.5",
        "192.168.10.10",
        "192.168.10.11",
        "192.168.10.12",
        "10.0.0.1",
        "10.0.0.2",
        "server01.lab",
    ]


def test_onboarding_scope_target_normalization_and_site_scope(engine_harness: EngineTestHarness) -> None:
    normalized = normalize_targets_for_save(
        [
            {
                "kind": "onboarding_scope",
                "site_id": 1,
                "site_name": "Main Lab",
                "entries": "192.168.1.10;server01.lab",
                "exclusions": "192.168.1.1;printer01.lab",
            }
        ]
    )
    assert normalized == [
        {
            "kind": "onboarding_scope",
            "site_id": 1,
            "site_name": "Main Lab",
            "entries": ["192.168.1.10", "server01.lab"],
            "exclusions": ["192.168.1.1", "printer01.lab"],
        }
    ]

    conn = sqlite3.connect(engine_harness.db_path)
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO users (id, username, display_name, password_sha512, role, last_login, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "site-user", "Site User", "test", "User", 0, 0, 0),
        )
        cur.execute(
            "INSERT INTO user_site_assignments (user_id, site_id, assigned_at) VALUES (?, ?, ?)",
            (2, 1, 1_700_000_000),
        )
        conn.commit()
    finally:
        conn.close()

    manager = UserSiteAccessManager(lambda: sqlite3.connect(engine_harness.db_path))
    scoped, error = manager.scope_job_targets_for_persistence(
        {"username": "site-user", "role": "User"},
        normalized,
    )

    assert error is None
    assert scoped[0]["kind"] == "onboarding_scope"
    assert scoped[0]["allowed_site_ids"] == [1]
    assert manager.job_targets_fit_scope({"username": "site-user", "role": "User"}, scoped)


def test_scheduled_jobs_api_creates_onboarding_job(engine_harness: EngineTestHarness) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "job_kind": "onboarding",
            "name": "Automatic Device Onboarding Main Lab",
            "targets": [
                {
                    "kind": "onboarding_scope",
                    "site_id": 1,
                    "site_name": "Main Lab",
                    "entries": ["192.168.10.5", "server01.lab"],
                    "exclusions": ["192.168.10.99"],
                }
            ],
            "components": [
                {
                    "kind": "device_onboarding",
                    "install_branch": "main",
                    "ssh_port": 22,
                }
            ],
            "credential_id": 77,
            "execution_context": "onboarding_linux_ssh",
            "schedule": {"type": "immediately"},
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    job = payload["job"]
    assert job["job_kind"] == "onboarding"
    assert job["execution_context"] == "onboarding_linux_ssh"
    assert job["components"][0]["kind"] == "device_onboarding"
    assert job["targets"][0]["kind"] == "onboarding_scope"
    assert job["targets"][0]["exclusions"] == ["192.168.10.99"]


def test_onboarding_target_status_hydrates_approved_context(engine_harness: EngineTestHarness) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    now = 1_700_000_100
    conn = sqlite3.connect(engine_harness.db_path)
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_job_onboarding_targets (
                run_id,
                job_id,
                scheduled_ts,
                site_id,
                target_input,
                target_address,
                target_hostname,
                ssh_port,
                status,
                detail,
                approval_reference,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                55,
                44,
                now,
                1,
                "192.168.3.8",
                "192.168.3.8",
                "192.168.3.8",
                22,
                "waiting_approval",
                "Agent installed. Device approval pending operator action.",
                "APP-ONBOARD-1",
                now,
                now,
            ),
        )
        cur.execute(
            """
            INSERT INTO device_approvals (
                id,
                approval_reference,
                hostname_claimed,
                ssl_key_fingerprint_claimed,
                enrollment_code,
                site_id,
                status,
                created_at,
                updated_at,
                approved_by_user_id,
                onboarding_job_id,
                onboarding_run_id,
                onboarding_target
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "approval-onboard-1",
                "APP-ONBOARD-1",
                "lab-docs-01",
                "11:22:33:44",
                "SITE-MAIN-CODE",
                1,
                "approved",
                "2026-05-05T03:00:00Z",
                "2026-05-05T03:05:00Z",
                "admin",
                44,
                55,
                "192.168.3.8",
            ),
        )
        conn.commit()
    finally:
        conn.close()

    rows = scheduler._load_onboarding_target_rows(44, now)
    hydrated = scheduler._backfill_onboarding_target_approval_references(rows)

    assert hydrated[0]["status"] == "approved"
    assert hydrated[0]["approval_id"] == "approval-onboard-1"
    assert hydrated[0]["approval_status"] == "approved"
    assert hydrated[0]["detail"] == "Device approved. Agent finalizing enrollment."


def test_onboarding_redeploy_clears_history_and_dispatches(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client, scheduler = _scheduled_jobs_client(engine_harness)
    response = client.post(
        "/api/scheduled_jobs",
        json={
            "job_kind": "onboarding",
            "name": "Automatic Device Onboarding Main Lab",
            "targets": [
                {
                    "kind": "onboarding_scope",
                    "site_id": 1,
                    "site_name": "Main Lab",
                    "entries": ["192.168.10.5"],
                }
            ],
            "components": [{"kind": "device_onboarding", "install_branch": "main", "ssh_port": 22}],
            "credential_id": 77,
            "execution_context": "onboarding_linux_ssh",
            "schedule": {"type": "immediately"},
        },
    )
    assert response.status_code == 200
    job_id = int(response.get_json()["job"]["id"])

    conn = sqlite3.connect(engine_harness.db_path)
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_job_runs (
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?)
            """,
            (job_id, 1_700_000_000, "Success", 1_700_000_000, 1_700_000_001),
        )
        old_run_id = int(cur.lastrowid)
        cur.execute(
            """
            INSERT INTO scheduled_job_onboarding_targets (
                run_id,
                job_id,
                scheduled_ts,
                site_id,
                target_input,
                target_address,
                target_hostname,
                ssh_port,
                status,
                created_at,
                updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                old_run_id,
                job_id,
                1_700_000_000,
                1,
                "192.168.10.5",
                "192.168.10.5",
                "192.168.10.5",
                22,
                "waiting_approval",
                1_700_000_000,
                1_700_000_001,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    dispatched = []

    def fake_dispatch(**kwargs):
        dispatched.append(kwargs)

    monkeypatch.setattr(scheduler, "_dispatch_onboarding_run", fake_dispatch)

    redeploy = client.post(f"/api/onboarding/jobs/{job_id}/redeploy", json={})

    assert redeploy.status_code == 200
    payload = redeploy.get_json()
    assert payload["cleared"] == 1
    assert len(payload["run_ids"]) == 1
    assert dispatched and dispatched[0]["job_id"] == job_id

    conn = sqlite3.connect(engine_harness.db_path)
    try:
        cur = conn.cursor()
        cur.execute("SELECT COUNT(*) FROM scheduled_job_onboarding_targets WHERE run_id=?", (old_run_id,))
        assert int(cur.fetchone()[0]) == 0
        cur.execute("SELECT COUNT(*) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
        assert int(cur.fetchone()[0]) == 1
    finally:
        conn.close()


def test_onboarding_remote_command_marks_agent_noninteractive(engine_harness: EngineTestHarness) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    command = scheduler._remote_onboarding_command(
        branch="feature/test",
        server_url="https://engine.example",
        enrollment_code="secret-code",
        job_id=11,
        run_id=38,
        target="192.168.3.8",
    )

    assert "BOREALIS_AGENT_NONINTERACTIVE=1" in command
    assert "BOREALIS_AGENT_NO_TTY=1" in command
    assert "BOREALIS_ONBOARDING_TARGET=192.168.3.8" in command


def test_onboarding_failure_hint_prefers_actionable_output() -> None:
    hint = scheduled_job_module._onboarding_failure_hint(
        stdout=(
            "Warning: Permanently added '192.168.3.8' (ED25519) to the list of known hosts.\n"
            "[2026-05-05T03:01:04] Launching /opt/Borealis/Agent.sh deploy --serverurl secret-url.\n"
            "/tmp/borealis-agent.64Rvmd: line 77: /dev/tty: No such device or address\n"
        ),
        stderr="nicole@192.168.3.8's password:",
        redactions=["secret-url"],
    )

    assert "/dev/tty" in hint
    assert "secret-url" not in hint


def test_preflight_remote_port_accepts_valid_ssh_banner(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    fake_socket = _FakePreflightSocket([b"SSH-2.0-OpenSSH_9.6\r\n"])

    monkeypatch.setattr(
        scheduled_job_module.socket,
        "create_connection",
        lambda *_args, **_kwargs: fake_socket,
    )

    result = scheduler._preflight_remote_port(
        host="10.255.0.10",
        port=22,
        attempts=1,
        timeout_seconds=1.0,
        probe="ssh_banner",
    )

    assert result == ""
    assert fake_socket.closed is True


def test_preflight_remote_port_rejects_ssh_targets_without_banner(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    fake_socket = _FakePreflightSocket([socket.timeout()])

    monkeypatch.setattr(
        scheduled_job_module.socket,
        "create_connection",
        lambda *_args, **_kwargs: fake_socket,
    )

    result = scheduler._preflight_remote_port(
        host="10.255.0.10",
        port=22,
        attempts=1,
        timeout_seconds=1.0,
        probe="ssh_banner",
    )

    assert result == "ssh_banner_timeout"
    assert fake_socket.closed is True


def test_preflight_remote_port_uses_configured_ssh_banner_timeout_once(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    fake_socket = _FakePreflightSocket([socket.timeout()])
    create_calls = {"count": 0}
    captured_timeouts: list[float] = []

    def _fake_create_connection(*_args, **_kwargs):
        create_calls["count"] += 1
        captured_timeouts.append(float(_kwargs.get("timeout") or 0.0))
        return fake_socket

    monkeypatch.setattr(
        scheduled_job_module.socket,
        "create_connection",
        _fake_create_connection,
    )

    result = scheduler._preflight_remote_port(
        host="10.255.0.10",
        port=22,
        attempts=5,
        timeout_seconds=1.25,
        retry_delay_seconds=1.0,
        probe="ssh_banner",
        banner_timeout_seconds=15.0,
    )

    assert result == "ssh_banner_timeout"
    assert create_calls["count"] == 1
    assert captured_timeouts
    assert captured_timeouts[0] >= 14.0
    assert fake_socket.closed is True
    assert fake_socket.timeouts
    assert fake_socket.timeouts[0] >= 14.0


def test_preflight_ssh_session_uses_full_timeout_for_connecttimeout(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    captured: dict[str, object] = {}

    class _FakeSpawn:
        def __init__(self, command, args, encoding=None, timeout=None) -> None:
            captured["command"] = command
            captured["args"] = list(args)
            captured["encoding"] = encoding
            captured["timeout"] = timeout
            self.before = ""

        def expect(self, _patterns):
            return 1

        def sendline(self, _text):
            return None

        def close(self):
            return None

    monkeypatch.setattr(scheduled_job_module.shutil, "which", lambda _name: "/usr/bin/ssh")
    monkeypatch.setitem(
        sys.modules,
        "pexpect",
        SimpleNamespace(
            EOF=object(),
            TIMEOUT=object(),
            spawn=_FakeSpawn,
        ),
    )

    result = scheduler._preflight_ssh_session(
        host="10.255.0.10",
        port=22,
        username="ubuntu",
        timeout_seconds=20.0,
    )

    assert result == "ssh_session_timeout"
    args = [str(item) for item in captured["args"]]
    assert "ConnectTimeout=20" in args


def test_shared_ansible_dispatch_selects_key_auth_for_mixed_ssh_credential(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                started_ts,
                finished_ts,
                status,
                error,
                created_at,
                updated_at,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                75,
                9,
                "",
                1_773_782_700,
                1_773_782_700,
                None,
                "Running",
                "",
                1_773_782_700,
                1_773_782_700,
                1,
                0,
                "ansible",
                "Playbook SSH Deferred Probe",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                id,
                run_id,
                device_guid,
                hostname,
                site_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                205,
                75,
                "GUID-TEST-0001",
                "test-device",
                1,
                "main_lab__test_device",
                "",
                "",
                "pending",
                "",
                json.dumps([]),
                1_773_782_700,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32", "_requested_start": True}},
    )
    monkeypatch.setattr(
        scheduler,
        "_preflight_remote_port",
        lambda **_kwargs: (_ for _ in ()).throw(AssertionError("SSH preflight should not run")),
    )
    captured_probe: dict[str, object] = {}
    monkeypatch.setattr(
        scheduler,
        "_preflight_ssh_session",
        lambda **kwargs: captured_probe.setdefault("kwargs", kwargs) and "",
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 15,
            "name": "SSH Deferred Probe",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
            "private_key_passphrase": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook SSH Deferred Probe",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {"virtual_path": rel_path},
        ),
    )

    captured: dict[str, object] = {}
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    result = scheduler._dispatch_shared_ansible(
        job_id=9,
        run_row_id=75,
        scheduled_ts=1_773_782_700,
        run_mode="ssh",
        component={"name": "Playbook SSH Deferred Probe", "path": "probe.yml"},
        credential_id=15,
        use_service_account=False,
    )

    assert result is not None
    assert "kwargs" in captured
    target_specifications = captured["kwargs"]["target_specifications"]
    assert len(target_specifications) == 1
    host_vars = target_specifications[0]["host_vars"]
    assert host_vars["ansible_connection"] == "ssh"
    assert host_vars["ansible_host"] == "10.77.0.15"
    assert host_vars["ansible_user"] == "ubuntu"
    assert "ansible_password" not in host_vars
    assert "ansible_ssh_password_mechanism" not in host_vars
    assert host_vars["ansible_ssh_private_key_file"] == "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
    assert "IdentitiesOnly=yes" in host_vars["ansible_ssh_extra_args"]
    assert "BatchMode=yes" in host_vars["ansible_ssh_extra_args"]
    assert "PreferredAuthentications=publickey" in host_vars["ansible_ssh_extra_args"]
    assert "PasswordAuthentication=no" in host_vars["ansible_ssh_extra_args"]
    assert captured_probe["kwargs"]["password"] == ""
    assert captured_probe["kwargs"]["private_key_text"]

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason
              FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (75,),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row[0] == "eligible"
    assert row[1] == ""


def test_mixed_ssh_auth_mode_falls_back_to_password_when_key_probe_needs_password(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    captured: list[dict[str, object]] = []

    def _capture_probe(**kwargs):
        captured.append(kwargs)
        if len(captured) == 1:
            return "ssh_session_failed: Permission denied (publickey)"
        return ""

    monkeypatch.setattr(scheduler, "_preflight_ssh_session", _capture_probe)

    mode = scheduler._resolve_mixed_ssh_auth_mode(
        host="10.77.0.15",
        port=22,
        username="ubuntu",
        password="secret",
        private_key_text="-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
    )

    assert mode == "password"
    assert len(captured) == 2
    assert captured[0]["password"] == ""
    assert captured[0]["private_key_text"].startswith("-----BEGIN")
    assert captured[1]["password"] == "secret"
    assert captured[1]["private_key_text"] == ""


def test_mixed_ssh_auth_mode_keeps_key_when_password_probe_fails(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    captured: list[dict[str, object]] = []

    def _capture_probe(**kwargs):
        captured.append(kwargs)
        if len(captured) == 1:
            return "permission_denied"
        return "permission_denied"

    monkeypatch.setattr(scheduler, "_preflight_ssh_session", _capture_probe)

    mode = scheduler._resolve_mixed_ssh_auth_mode(
        host="10.77.0.15",
        port=22,
        username="ubuntu",
        password="secret",
        private_key_text="-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
    )

    assert mode == "key"
    assert len(captured) == 2
    assert captured[0]["password"] == ""
    assert captured[0]["private_key_text"].startswith("-----BEGIN")
    assert captured[1]["password"] == "secret"
    assert captured[1]["private_key_text"] == ""


def test_mixed_ssh_auth_mode_keeps_key_when_key_probe_is_inconclusive(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    captured: list[dict[str, object]] = []

    def _capture_probe(**kwargs):
        captured.append(kwargs)
        if len(captured) == 1:
            return "ssh_session_timeout"
        return "permission_denied"

    monkeypatch.setattr(scheduler, "_preflight_ssh_session", _capture_probe)

    mode = scheduler._resolve_mixed_ssh_auth_mode(
        host="10.77.0.15",
        port=22,
        username="ubuntu",
        password="secret",
        private_key_text="-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
    )

    assert mode == "key"
    assert len(captured) == 2
    assert captured[0]["password"] == ""
    assert captured[0]["private_key_text"].startswith("-----BEGIN")
    assert captured[1]["password"] == "secret"
    assert captured[1]["private_key_text"] == ""


def test_mixed_ssh_auth_mode_uses_password_when_key_probe_times_out_and_password_works(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    captured: list[dict[str, object]] = []

    def _capture_probe(**kwargs):
        captured.append(kwargs)
        if len(captured) == 1:
            return "ssh_session_timeout"
        return ""

    monkeypatch.setattr(scheduler, "_preflight_ssh_session", _capture_probe)

    mode = scheduler._resolve_mixed_ssh_auth_mode(
        host="10.77.0.15",
        port=22,
        username="ubuntu",
        password="secret",
        private_key_text="-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
    )

    assert mode == "password"
    assert len(captured) == 2
    assert captured[0]["password"] == ""
    assert captured[0]["private_key_text"].startswith("-----BEGIN")
    assert captured[1]["password"] == "secret"
    assert captured[1]["private_key_text"] == ""


def test_mixed_ssh_auth_mode_password_probe_handles_denial_transcript(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    captured: list[dict[str, object]] = []

    def _capture_probe(**kwargs):
        captured.append(kwargs)
        if len(captured) == 1:
            return "ssh_session_failed:Permission denied (publickey,password)."
        return ""

    monkeypatch.setattr(scheduler, "_preflight_ssh_session", _capture_probe)

    mode = scheduler._resolve_mixed_ssh_auth_mode(
        host="10.77.0.15",
        port=22,
        username="ubuntu",
        password="secret",
        private_key_text="-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
    )

    assert mode == "password"
    assert len(captured) == 2
    assert captured[0]["password"] == ""
    assert captured[0]["private_key_text"].startswith("-----BEGIN")
    assert captured[1]["password"] == "secret"
    assert captured[1]["private_key_text"] == ""


def test_scheduled_job_create_recovers_when_lastrowid_is_missing(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client, scheduler = _scheduled_jobs_client(engine_harness)

    def _conn_with_missing_lastrowid():
        return _MissingLastRowIdConnection(sqlite3.connect(str(engine_harness.db_path)))

    monkeypatch.setattr(scheduler, "_conn", _conn_with_missing_lastrowid)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Nightly Inventory",
            "components": [{"kind": "script", "script": "Write-Host 'hello'"}],
            "targets": ["test-device"],
            "schedule": {"type": "immediately"},
            "execution_context": "system",
            "enabled": True,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["job"]["id"] is not None
    assert payload["job"]["name"] == "Nightly Inventory"
    assert payload["job"]["targets"] == ["test-device"]


def test_scheduled_job_device_status_prefers_terminal_host_state_and_preserves_output(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?)
            """,
            (
                1,
                "Install 7-Zip",
                json.dumps([]),
                json.dumps(["test-device"]),
                "once",
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.executemany(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                started_ts,
                finished_ts,
                status,
                created_at,
                updated_at,
                skip_reason
            ) VALUES (?,?,?,?,?,?,?,?,?,?)
            """,
            (
                (10, 1, "TEST-DEVICE", 1_773_782_700, 1_773_782_760, None, "Pending", 1_773_782_700, 1_773_782_760, ""),
                (11, 1, "test-device", 1_773_782_700, 1_773_782_760, 1_773_782_767, "Success", 1_773_782_700, 1_773_782_767, ""),
            ),
        )
        cur.executemany(
            """
            INSERT INTO scheduled_job_run_targets(run_id, device_guid, hostname, site_id, resolved_from_filter_id, created_at)
            VALUES (?,?,?,?,?,?)
            """,
            (
                (10, "GUID-TEST-0001", "test-device", None, None, 1_773_782_700),
                (11, "GUID-TEST-0001", "test-device", None, None, 1_773_782_700),
            ),
        )
        cur.execute(
            """
            INSERT INTO activity_history(id, hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
            VALUES (?,?,?,?,?,?,?,?,?)
            """,
            (
                500,
                "test-device",
                "Scripts/7zip.ps1",
                "7-Zip [WIN]",
                "powershell",
                1_773_782_767,
                "Success",
                "Installed 7-Zip",
                "",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_activity(run_id, activity_id, component_kind, script_type, component_path, component_name, created_at)
            VALUES (?,?,?,?,?,?,?)
            """,
            (11, 500, "script", "powershell", "Scripts/7zip.ps1", "7-Zip [WIN]", 1_773_782_767),
        )
        conn.commit()
    finally:
        conn.close()

    job_response = client.get("/api/scheduled_jobs/1")
    assert job_response.status_code == 200
    job_payload = job_response.get_json()["job"]
    assert job_payload["last_status"] == "Success"
    assert job_payload["result_counts"]["success"] == 1
    assert job_payload["result_counts"]["pending"] == 0

    devices_response = client.get("/api/scheduled_jobs/1/devices")
    assert devices_response.status_code == 200
    devices_payload = devices_response.get_json()
    assert len(devices_payload["devices"]) == 1
    device_row = devices_payload["devices"][0]
    assert device_row["hostname"] == "test-device"
    assert device_row["job_status"] == "Success"
    assert device_row["has_stdout"] is True


def test_scheduled_job_summary_stays_pending_until_all_hosts_finish(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?)
            """,
            (
                2,
                "Mixed Host State",
                json.dumps([]),
                json.dumps(["test-device", "other-device"]),
                "once",
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.executemany(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                started_ts,
                finished_ts,
                status,
                created_at,
                updated_at,
                skip_reason
            ) VALUES (?,?,?,?,?,?,?,?,?,?)
            """,
            (
                (20, 2, "test-device", 1_773_782_700, 1_773_782_760, 1_773_782_767, "Success", 1_773_782_700, 1_773_782_767, ""),
                (21, 2, "other-device", 1_773_782_700, 1_773_782_760, None, "Pending", 1_773_782_700, 1_773_782_760, ""),
            ),
        )
        cur.executemany(
            """
            INSERT INTO scheduled_job_run_targets(run_id, device_guid, hostname, site_id, resolved_from_filter_id, created_at)
            VALUES (?,?,?,?,?,?)
            """,
            (
                (20, "GUID-TEST-0001", "test-device", None, None, 1_773_782_700),
                (21, "GUID-TEST-0002", "other-device", None, None, 1_773_782_700),
            ),
        )
        conn.commit()
    finally:
        conn.close()

    response = client.get("/api/scheduled_jobs/2")
    assert response.status_code == 200
    payload = response.get_json()["job"]
    assert payload["last_status"] == "Pending"
    assert payload["result_counts"]["success"] == 1
    assert payload["result_counts"]["pending"] == 1


def test_scheduled_job_summary_reports_no_eligible_targets_for_remote_preflight_skips(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?)
            """,
            (
                3,
                "Remote SSH Check",
                json.dumps([]),
                json.dumps(["lab-linux-01"]),
                "once",
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                started_ts,
                finished_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                error
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                30,
                3,
                "lab-linux-01",
                1_773_782_900,
                1_773_782_905,
                1_773_782_910,
                "Skipped",
                1_773_782_900,
                1_773_782_910,
                "no_eligible_targets",
                "No eligible devices were available for this Ansible run.",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                30,
                "GUID-LINUX-0001",
                "lab-linux-01",
                1,
                None,
                "lab__lab_linux_01",
                "10.255.0.44",
                "ssh",
                "skipped",
                "remote_preflight_failed",
                json.dumps([]),
                1_773_782_900,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    response = client.get("/api/scheduled_jobs/3")
    assert response.status_code == 200
    payload = response.get_json()["job"]
    assert payload["last_status"] == "No Eligible Targets"
    assert payload["result_counts"]["skipped"] == 1
    assert payload["result_counts"]["total_targets"] == 1


def test_scheduled_job_create_rejects_mixed_components_for_remote_ansible_context(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Mixed Remote Job",
            "components": [
                {"type": "ansible", "name": "Playbook A"},
                {"type": "script", "name": "Script B"},
            ],
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
            "schedule": {"type": "immediately"},
            "execution_context": "ssh",
            "credential_id": 5,
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert "cannot mix script assemblies with Ansible" in payload["error"]


def test_scheduled_job_create_rejects_ansible_components_for_agent_context(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Invalid Agent Context",
            "components": [
                {"type": "ansible", "name": "Playbook A"},
            ],
            "targets": ["test-device"],
            "schedule": {"type": "immediately"},
            "execution_context": "system",
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert "must contain only script assemblies" in payload["error"]


@pytest.mark.parametrize(
    ("execution_context", "credential_id", "use_service_account"),
    (
        ("ssh_individual", 5, False),
        ("winrm_individual", None, True),
    ),
)
def test_scheduled_job_create_accepts_individual_ansible_contexts(
    engine_harness: EngineTestHarness,
    execution_context: str,
    credential_id: int | None,
    use_service_account: bool,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": f"Individual {execution_context}",
            "components": [
                {"type": "ansible", "name": "Playbook A"},
            ],
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
            "schedule": {"type": "immediately"},
            "execution_context": execution_context,
            "credential_id": credential_id,
            "use_service_account": use_service_account,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()["job"]
    assert payload["execution_context"] == execution_context
    assert payload["credential_id"] == credential_id
    assert payload["use_service_account"] is bool(use_service_account if "winrm" in execution_context else False)


def test_scheduled_job_update_rejects_mixed_script_and_ansible_components(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                enabled,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?)
            """,
            (
                91,
                "Script Only Job",
                json.dumps([{"type": "script", "name": "Script A"}]),
                json.dumps(["test-device"]),
                "once",
                "system",
                1,
                1_773_782_800,
                1_773_782_800,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    response = client.put(
        "/api/scheduled_jobs/91",
        json={
            "components": [
                {"type": "script", "name": "Script A"},
                {"type": "ansible", "name": "Playbook B"},
            ]
        },
    )

    assert response.status_code == 400
    payload = response.get_json()
    assert "cannot mix script assemblies with Ansible" in payload["error"]


def test_scheduled_job_create_persists_structured_device_targets(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Structured Device Targets",
            "components": [
                {"type": "ansible", "name": "Playbook A"},
            ],
            "targets": [
                {
                    "kind": "device",
                    "device_guid": "GUID-TEST-0001",
                    "hostname": "test-device",
                    "site_id": 1,
                    "site_name": "Main Lab",
                }
            ],
            "schedule": {"type": "immediately"},
            "execution_context": "local",
        },
    )

    assert response.status_code == 200
    payload = response.get_json()["job"]
    assert payload["targets"] == [
        {
            "kind": "device",
            "device_guid": "guid-test-0001",
            "hostname": "test-device",
            "site_id": 1,
            "site_name": "Main Lab",
        }
    ]


def test_scheduled_job_devices_preserves_duplicate_hostnames_for_shared_ansible_runs(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            "INSERT INTO sites (id, name, description, created_at, enrollment_code) VALUES (?,?,?,?,?)",
            (2, "Deep Lab", "Secondary site", 1_700_000_000, "SITE-DEEP-CODE"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?,?)
            """,
            (
                4,
                "Shared Ansible",
                json.dumps([{"type": "ansible", "name": "Playbook A"}]),
                json.dumps([]),
                "once",
                "ssh",
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                started_ts,
                finished_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                40,
                4,
                1_773_782_700,
                1_773_782_760,
                1_773_782_767,
                "Success",
                1_773_782_700,
                1_773_782_767,
                "",
                1,
                0,
                "ansible",
                "Playbook A",
            ),
        )
        cur.executemany(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                (
                    40,
                    "GUID-BUNNY-0001",
                    "host01",
                    1,
                    None,
                    "bunny_lab__host01",
                    "10.255.0.11",
                    "ssh",
                    "eligible",
                    "",
                    json.dumps([7]),
                    1_773_782_700,
                ),
                (
                    40,
                    "GUID-DEEP-0001",
                    "host01",
                    2,
                    None,
                    "deep_lab__host01",
                    "10.255.0.12",
                    "ssh",
                    "eligible",
                    "",
                    json.dumps([7]),
                    1_773_782_700,
                ),
            ),
        )
        cur.execute(
            """
            INSERT INTO activity_history(id, hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
            VALUES (?,?,?,?,?,?,?,?,?)
            """,
            (
                540,
                "borealis-engine-01",
                "Ansible_Playbooks/playbook.yml",
                "Playbook A",
                "ansible",
                1_773_782_767,
                "Success",
                "ok",
                "",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_activity(run_id, activity_id, component_kind, script_type, component_path, component_name, created_at)
            VALUES (?,?,?,?,?,?,?)
            """,
            (40, 540, "ansible", "ansible", "Ansible_Playbooks/playbook.yml", "Playbook A", 1_773_782_767),
        )
        conn.commit()
    finally:
        conn.close()

    response = client.get("/api/scheduled_jobs/4/devices?occurrence=1773782700")
    assert response.status_code == 200
    payload = response.get_json()
    assert len(payload["devices"]) == 2
    inventory_hostnames = {row["inventory_hostname"] for row in payload["devices"]}
    assert inventory_hostnames == {"bunny_lab__host01", "deep_lab__host01"}
    sites = {row["site"] for row in payload["devices"]}
    assert sites == {"Main Lab", "Deep Lab"}


def test_shared_ansible_dispatch_uses_execution_context_for_remote_transport(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_type='winrm',
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                60,
                6,
                1_773_782_700,
                "Pending",
                1_773_782_700,
                1_773_782_700,
                "",
                1,
                0,
                "ansible",
                "Playbook A",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                60,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_700,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    captured: dict[str, object] = {}

    monkeypatch.setattr(
        scheduler,
        "_vpn_session_lookup",
        lambda: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook A",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-0001",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_a.yml",
            },
        ),
    )
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    result = scheduler._dispatch_shared_ansible(
        job_id=6,
        run_row_id=60,
        scheduled_ts=1_773_782_700,
        run_mode="ssh",
        component={"name": "Playbook A", "path": "playbook_a.yml"},
        credential_id=5,
        use_service_account=False,
    )

    assert result is not None
    assert "kwargs" in captured
    assert captured["kwargs"]["playbook_content"].startswith("---\n- hosts: all\n")
    assert captured["kwargs"]["playbook_abs_path"] == ""
    target_specifications = captured["kwargs"]["target_specifications"]
    assert len(target_specifications) == 1
    assert target_specifications[0]["inventory_hostname"] == "main_lab__test_device"
    assert target_specifications[0]["host_vars"]["ansible_connection"] == "ssh"
    assert target_specifications[0]["host_vars"]["ansible_host"] == "10.77.0.15"
    assert target_specifications[0]["host_vars"]["ansible_ssh_retries"] == 3
    assert target_specifications[0]["host_vars"]["ansible_ssh_timeout"] == 20
    assert target_specifications[0]["host_vars"]["ansible_ssh_transfer_method"] == "scp"
    assert target_specifications[0]["host_vars"]["ansible_scp_extra_args"] == "-O"
    assert target_specifications[0]["host_vars"]["ansible_user"] == "ubuntu"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason, wireguard_peer_ip, resolved_connection
              FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (60,),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row[0] == "eligible"
    assert row[1] == ""


def test_shared_ansible_dispatch_requests_ssh_port_in_vpn_prepare(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                61,
                16,
                1_773_782_701,
                "Pending",
                1_773_782_701,
                1_773_782_701,
                "",
                1,
                0,
                "ansible",
                "Playbook Port Probe",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                61,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_701,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    captured_prepare: dict[str, object] = {}

    def _capture_prepare(_agent_ids, required_ports=None):
        captured_prepare["required_ports"] = list(required_ports or [])
        return {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}}

    monkeypatch.setattr(scheduler, "_prepare_vpn_sessions", _capture_prepare)
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook Port Probe",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-0001",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_port_probe.yml",
            },
        ),
    )
    monkeypatch.setattr(scheduler, "_server_ansible_runner", lambda **_kwargs: "runner-1")

    result = scheduler._dispatch_shared_ansible(
        job_id=16,
        run_row_id=61,
        scheduled_ts=1_773_782_701,
        run_mode="ssh",
        component={"name": "Playbook Port Probe", "path": "playbook_port_probe.yml"},
        credential_id=5,
        use_service_account=False,
    )

    assert result is not None
    assert captured_prepare["required_ports"] == [22]


def test_shared_ansible_dispatch_reports_aegis_locked_and_recovers_after_unlock(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler, context = _scheduled_jobs_client_with_context(engine_harness)
    service = context.aegis_cipher_service
    cipher = str(engine_harness.aegis_cipher or "shared-ansible-cipher")
    if not service.is_configured():
        service.setup(cipher)
    elif service.is_locked():
        service.unlock(cipher)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO credentials(
                id,
                name,
                description,
                site_id,
                credential_type,
                connection_type,
                username,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_method,
                become_username,
                become_password_encrypted,
                metadata_json,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                5,
                "Shared SSH Credential",
                "Scheduler decrypt test",
                1,
                "machine",
                "ssh",
                "ubuntu",
                service.encrypt_secret_for_blob("secret"),
                None,
                None,
                "",
                "",
                None,
                "{}",
                1_773_782_704,
                1_773_782_704,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                64,
                10,
                1_773_782_704,
                "Pending",
                1_773_782_704,
                1_773_782_704,
                "",
                1,
                0,
                "ansible",
                "Playbook Locked",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                64,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_704,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                65,
                10,
                1_773_782_705,
                "Pending",
                1_773_782_705,
                1_773_782_705,
                "",
                1,
                0,
                "ansible",
                "Playbook Unlocked",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                65,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_705,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    service.clear_memory_key()

    locked_result = scheduler._dispatch_shared_ansible(
        job_id=10,
        run_row_id=64,
        scheduled_ts=1_773_782_704,
        run_mode="ssh",
        component={"name": "Playbook Locked", "path": "playbook_locked.yml"},
        credential_id=5,
        use_service_account=False,
    )

    assert locked_result is None

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, error FROM scheduled_job_runs WHERE id=?", (64,))
        locked_run = cur.fetchone()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason
              FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (64,),
        )
        locked_target = cur.fetchone()
    finally:
        conn.close()

    assert locked_run[0] == "Failed"
    assert locked_run[1] == "Aegis Cipher has not been entered; credential-backed execution is disabled."
    assert locked_target[0] == "unresolved"
    assert locked_target[1] == "credential_locked"

    captured: dict[str, object] = {}
    service.unlock(cipher)
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_preflight_remote_port",
        lambda **_kwargs: "",
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook Unlocked",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-0001",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_unlocked.yml",
            },
        ),
    )
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    unlocked_result = scheduler._dispatch_shared_ansible(
        job_id=10,
        run_row_id=65,
        scheduled_ts=1_773_782_705,
        run_mode="ssh",
        component={"name": "Playbook Unlocked", "path": "playbook_unlocked.yml"},
        credential_id=5,
        use_service_account=False,
    )

    assert unlocked_result is not None
    assert "kwargs" in captured
    target_specifications = captured["kwargs"]["target_specifications"]
    assert len(target_specifications) == 1
    assert target_specifications[0]["host_vars"]["ansible_user"] == "ubuntu"
    assert target_specifications[0]["host_vars"]["ansible_password"] == "secret"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason
              FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (65,),
        )
        unlocked_target = cur.fetchone()
    finally:
        conn.close()

    assert unlocked_target[0] == "eligible"
    assert unlocked_target[1] == ""


def test_shared_ansible_dispatch_reports_credential_reset_required(
    engine_harness: EngineTestHarness,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO credentials(
                id,
                name,
                description,
                site_id,
                credential_type,
                connection_type,
                username,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_method,
                become_username,
                become_password_encrypted,
                metadata_json,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                6,
                "Reset Required Credential",
                "Lost during reset",
                1,
                "machine",
                "ssh",
                "ubuntu",
                None,
                None,
                None,
                "",
                "",
                None,
                json.dumps(
                    {
                        "aegis_secret_state": "reset_required",
                        "aegis_lost_secret_fields": ["password"],
                        "aegis_reset_at": 1_773_782_704,
                    }
                ),
                1_773_782_704,
                1_773_782_704,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                66,
                10,
                1_773_782_706,
                "Pending",
                1_773_782_706,
                1_773_782_706,
                "",
                1,
                0,
                "ansible",
                "Playbook Reset Required",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                66,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_706,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    result = scheduler._dispatch_shared_ansible(
        job_id=10,
        run_row_id=66,
        scheduled_ts=1_773_782_706,
        run_mode="ssh",
        component={"name": "Playbook Reset Required", "path": "playbook_reset_required.yml"},
        credential_id=6,
        use_service_account=False,
    )

    assert result is None

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, error FROM scheduled_job_runs WHERE id=?", (66,))
        run_row = cur.fetchone()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason
              FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (66,),
        )
        target_row = cur.fetchone()
    finally:
        conn.close()

    assert run_row[0] == "Failed"
    assert run_row[1] == (
        "The credential associated with this scheduled job can no longer be decrypted due to the "
        "Aegis Cipher being reset, please update the credential with the data it is missing."
    )
    assert target_row[0] == "unresolved"
    assert target_row[1] == "credential_reset_required"


def test_scheduled_jobs_warn_and_stay_disabled_when_credential_requires_reset(
    engine_harness: EngineTestHarness,
) -> None:
    client, _scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO credentials(
                id,
                name,
                description,
                site_id,
                credential_type,
                connection_type,
                username,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_method,
                become_username,
                become_password_encrypted,
                metadata_json,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                7,
                "Reset Marker Credential",
                "Disabled job warning",
                1,
                "machine",
                "ssh",
                "ubuntu",
                None,
                None,
                None,
                "",
                "",
                None,
                json.dumps(
                    {
                        "aegis_secret_state": "reset_required",
                        "aegis_lost_secret_fields": ["password"],
                        "aegis_reset_at": 1_773_782_800,
                    }
                ),
                1_773_782_800,
                1_773_782_800,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                credential_id,
                enabled,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?)
            """,
            (
                77,
                "Disabled By Reset",
                json.dumps([{"kind": "ansible", "name": "Playbook", "path": "playbook.yml"}]),
                json.dumps(["test-device"]),
                "once",
                "ssh",
                7,
                0,
                1_773_782_800,
                1_773_782_800,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    list_response = client.get("/api/scheduled_jobs")
    assert list_response.status_code == 200
    jobs = list_response.get_json()["jobs"]
    assert len(jobs) == 1
    assert jobs[0]["enabled"] is False
    assert jobs[0]["warning_code"] == "credential_reset_required"
    assert "Aegis Cipher being reset" in jobs[0]["warning_message"]

    toggle_response = client.post("/api/scheduled_jobs/77/toggle", json={"enabled": True})
    assert toggle_response.status_code == 409
    toggle_payload = toggle_response.get_json()
    assert toggle_payload["error"] == "credential_reset_required"

    create_response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Created While Reset Required",
            "components": [{"kind": "ansible", "name": "Playbook", "path": "playbook.yml"}],
            "targets": ["test-device"],
            "schedule": {"type": "once"},
            "execution_context": "ssh",
            "credential_id": 7,
            "enabled": True,
        },
    )
    assert create_response.status_code == 200
    created_payload = create_response.get_json()
    assert created_payload["job"]["enabled"] is False
    assert created_payload["warning_code"] == "credential_reset_required"


def test_shared_ansible_dispatch_normalizes_private_key_runtime_file(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                62,
                8,
                1_773_782_702,
                "Pending",
                1_773_782_702,
                1_773_782_702,
                "",
                1,
                0,
                "ansible",
                "Playbook C",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                62,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_702,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    captured: dict[str, object] = {}

    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_preflight_remote_port",
        lambda **_kwargs: "",
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 6,
            "name": "SSH Key Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "",
            "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\r\nabc\r\n-----END OPENSSH PRIVATE KEY-----\r\n",
            "private_key_passphrase": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook C",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-0001",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_c.yml",
            },
        ),
    )
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    result = scheduler._dispatch_shared_ansible(
        job_id=8,
        run_row_id=62,
        scheduled_ts=1_773_782_702,
        run_mode="ssh",
        component={"name": "Playbook C", "path": "playbook_c.yml"},
        credential_id=6,
        use_service_account=False,
    )

    assert result is not None
    runtime_files = captured["kwargs"]["runtime_files"]
    assert len(runtime_files) == 1
    assert runtime_files[0]["relative_path"] == "auth/id_borealis_ssh"
    assert "\r" not in runtime_files[0]["content"]
    assert runtime_files[0]["content"].endswith("\n")
    target_specifications = captured["kwargs"]["target_specifications"]
    host_vars = target_specifications[0]["host_vars"]
    assert host_vars["ansible_ssh_private_key_file"] == "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
    assert "IdentitiesOnly=yes" in host_vars["ansible_ssh_extra_args"]


def test_shared_ansible_dispatch_fails_for_passphrase_only_ssh_key(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                63,
                9,
                1_773_782_703,
                "Pending",
                1_773_782_703,
                1_773_782_703,
                "",
                1,
                0,
                "ansible",
                "Playbook D",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                63,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_703,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 7,
            "name": "Encrypted SSH Key",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "",
            "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n",
            "private_key_passphrase": "hunter2",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )

    result = scheduler._dispatch_shared_ansible(
        job_id=9,
        run_row_id=63,
        scheduled_ts=1_773_782_703,
        run_mode="ssh",
        component={"name": "Playbook D", "path": "playbook_d.yml"},
        credential_id=7,
        use_service_account=False,
    )

    assert result is None

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT status, error
              FROM scheduled_job_runs
             WHERE id=?
            """,
            (63,),
        )
        run_row = cur.fetchone()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason
              FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (63,),
        )
        target_row = cur.fetchone()
    finally:
        conn.close()

    assert run_row[0] == "Failed"
    assert "Passphrase-protected SSH private keys are not yet supported" in run_row[1]
    assert target_row[0] == "unresolved"
    assert target_row[1] == "credential_private_key_passphrase_unsupported"


def test_shared_ansible_dispatch_skips_winrm_targets_with_preflight_failures(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Windows Server 2022", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                61,
                7,
                1_773_782_701,
                "Pending",
                1_773_782_701,
                1_773_782_701,
                "",
                1,
                0,
                "ansible",
                "Playbook B",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                61,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "winrm",
                "pending",
                "",
                json.dumps([]),
                1_773_782_701,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    captured: dict[str, object] = {}

    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32", "_requested_start": True}},
    )
    monkeypatch.setattr(
        scheduler,
        "_preflight_remote_port",
        lambda **_kwargs: "[Errno 113] EHOSTUNREACH",
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "WinRM Test Credential",
            "connection_type": "winrm",
            "username": "Administrator",
            "password": "secret",
            "private_key": "",
            "metadata": {"winrm_transport": "ntlm"},
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook B",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-0001",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_b.yml",
            },
        ),
    )
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    result = scheduler._dispatch_shared_ansible(
        job_id=7,
        run_row_id=61,
        scheduled_ts=1_773_782_701,
        run_mode="winrm",
        component={"name": "Playbook B", "path": "playbook_b.yml"},
        credential_id=5,
        use_service_account=False,
    )

    assert result is None
    assert "kwargs" not in captured

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT resolution_status, resolution_reason
             FROM scheduled_job_run_targets
             WHERE run_id=?
            """,
            (61,),
        )
        row = cur.fetchone()
        cur.execute(
            """
            SELECT status, skip_reason, error
              FROM scheduled_job_runs
             WHERE id=?
            """,
            (61,),
        )
        run_row = cur.fetchone()
    finally:
        conn.close()

    assert row[0] == "skipped"
    assert row[1] == "remote_preflight_failed"
    assert run_row[0] == "Skipped"
    assert run_row[1] == "no_eligible_targets"
    assert run_row[2] == "No eligible devices were available for this Ansible run."


def test_tick_persists_activity_links_for_shared_ansible_runs(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                credential_id,
                use_service_account,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                7,
                "Shared SSH Job",
                json.dumps([{"type": "ansible", "name": "Playbook A", "path": "playbook_a.yml"}]),
                json.dumps(
                    [
                        {
                            "kind": "device",
                            "device_guid": "GUID-TEST-0001",
                            "hostname": "test-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        }
                    ]
                ),
                "immediately",
                "ssh",
                5,
                0,
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(
        scheduler,
        "_resolve_occurrence_for_tick",
        lambda **_kwargs: 1_773_782_700,
    )
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_preflight_remote_port",
        lambda **_kwargs: "",
    )
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "resolve_target_entries",
        lambda raw_targets, devices=None: (
            ["test-device"],
            {
                "resolved_targets": [
                    {
                        "device_guid": "GUID-TEST-0001",
                        "hostname": "test-device",
                        "site_id": 1,
                        "site_name": "Main Lab",
                        "resolved_from_filter_ids": [],
                    }
                ]
            },
        ),
    )
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "fetch_devices",
        lambda: [
            {
                "device_guid": "GUID-TEST-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent",
                "operating_system": "Ubuntu 24.04 LTS",
                "connection_endpoint": "",
            }
        ],
    )
    monkeypatch.setattr(
        scheduler,
        "_vpn_session_lookup",
        lambda: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook A",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-0001",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_a.yml",
            },
        ),
    )
    captured: dict[str, object] = {}
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    scheduler._tick_once()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT id, status, shared_execution, component_name
              FROM scheduled_job_runs
             WHERE job_id=?
            """,
            (7,),
        )
        run_row = cur.fetchone()
        assert run_row is not None
        cur.execute(
            """
            SELECT run_id, activity_id, component_kind, script_type, component_name
              FROM scheduled_job_run_activity
             WHERE run_id=?
            """,
            (int(run_row[0]),),
        )
        activity_link = cur.fetchone()
    finally:
        conn.close()

    assert run_row[1] == "Running"
    assert bool(run_row[2]) is True
    assert run_row[3] == "Playbook A"
    assert activity_link is not None
    assert activity_link[0] == int(run_row[0])
    assert activity_link[2] == "ansible"
    assert activity_link[3] == "ansible"
    assert activity_link[4] == "Playbook A"
    assert "kwargs" in captured


def test_tick_persists_activity_links_for_individual_ansible_runs(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    scheduler._running = False

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                credential_id,
                use_service_account,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                70,
                "Individual SSH Job",
                json.dumps(
                    [
                        {"type": "ansible", "name": "Playbook A", "path": "playbook_a.yml"},
                        {"type": "ansible", "name": "Playbook B", "path": "playbook_b.yml"},
                    ]
                ),
                json.dumps(
                    [
                        {
                            "kind": "device",
                            "device_guid": "GUID-TEST-0001",
                            "hostname": "test-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                        {
                            "kind": "device",
                            "device_guid": "GUID-TEST-0002",
                            "hostname": "test-device-02",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                    ]
                ),
                "immediately",
                "ssh_individual",
                5,
                0,
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(scheduler, "_resolve_occurrence_for_tick", lambda **_kwargs: 1_773_782_700)
    monkeypatch.setattr(scheduled_job_module, "load_ansible_runner_settings", lambda: {"job_concurrency_limit": 20, "global_concurrency_limit": 50})
    monkeypatch.setattr(scheduler, "_online_lookup", lambda: ["test-device", "test-device-02"])
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "resolve_target_entries",
        lambda raw_targets, devices=None: (
            ["test-device", "test-device-02"],
            {
                "resolved_targets": [
                    {
                        "device_guid": "guid-test-0001",
                        "hostname": "test-device",
                        "site_id": 1,
                        "site_name": "Main Lab",
                        "resolved_from_filter_ids": [],
                    },
                    {
                        "device_guid": "guid-test-0002",
                        "hostname": "test-device-02",
                        "site_id": 1,
                        "site_name": "Main Lab",
                        "resolved_from_filter_ids": [],
                    },
                ]
            },
        ),
    )
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "fetch_devices",
        lambda: [
            {
                "device_guid": "guid-test-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent",
                "operating_system": "Ubuntu 24.04 LTS",
                "connection_endpoint": "",
            },
            {
                "device_guid": "guid-test-0002",
                "hostname": "test-device-02",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent-02",
                "operating_system": "Rocky Linux 9.7",
                "connection_endpoint": "",
            },
        ],
    )
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {
            "test-device-agent": {"virtual_ip": "10.77.0.15/32"},
            "test-device-agent-02": {"virtual_ip": "10.77.0.16/32"},
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook B" if str(rel_path).endswith("playbook_b.yml") else "Playbook A",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or f"guid-{rel_path}",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_a.yml",
            },
        ),
    )

    captured: list[dict[str, object]] = []
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.append(kwargs) or f"runner-{len(captured)}",
    )

    scheduler._tick_once()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT target_hostname, status, shared_execution, component_index, component_name
              FROM scheduled_job_runs
             WHERE job_id=?
          ORDER BY target_hostname ASC, component_index ASC
            """,
            (70,),
        )
        run_rows = cur.fetchall()
        cur.execute(
            """
            SELECT run_id, hostname, resolution_status, resolved_connection, wireguard_peer_ip
              FROM scheduled_job_run_targets
             WHERE run_id IN (SELECT id FROM scheduled_job_runs WHERE job_id=?)
          ORDER BY run_id ASC
            """,
            (70,),
        )
        target_rows = cur.fetchall()
        cur.execute(
            """
            SELECT run_id, component_kind, script_type, component_name
              FROM scheduled_job_run_activity
             WHERE run_id IN (SELECT id FROM scheduled_job_runs WHERE job_id=?)
          ORDER BY run_id ASC
            """,
            (70,),
        )
        activity_rows = cur.fetchall()
    finally:
        conn.close()

    assert len(run_rows) == 4
    assert all(row[1] == "Running" for row in run_rows)
    assert all(bool(row[2]) is False for row in run_rows)
    assert {(row[0], row[3], row[4]) for row in run_rows} == {
        ("test-device", 0, "Playbook A"),
        ("test-device", 1, "Playbook B"),
        ("test-device-02", 0, "Playbook A"),
        ("test-device-02", 1, "Playbook B"),
    }
    assert len(target_rows) == 4
    assert all(row[2] == "eligible" for row in target_rows)
    assert all(row[3] == "ssh" for row in target_rows)
    assert {row[4] for row in target_rows} == {"10.77.0.15", "10.77.0.16"}
    assert len(activity_rows) == 4
    assert all(row[1] == "ansible" for row in activity_rows)
    assert all(row[2] == "ansible" for row in activity_rows)

    assert len(captured) == 4
    assert all(call["connection"] == "ssh" for call in captured)
    assert all(len(call["target_specifications"]) == 1 for call in captured)
    assert all(call["target_specifications"][0]["host_vars"]["ansible_ssh_transfer_method"] == "scp" for call in captured)
    assert all(call["target_specifications"][0]["host_vars"]["ansible_scp_extra_args"] == "-O" for call in captured)
    assert {(call["hostname"], call["playbook_name"]) for call in captured} == {
        ("test-device", "Playbook A"),
        ("test-device", "Playbook B"),
        ("test-device-02", "Playbook A"),
        ("test-device-02", "Playbook B"),
    }


def test_shared_ansible_dispatch_uses_configured_scp_transfer_method(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                status,
                created_at,
                updated_at,
                skip_reason,
                shared_execution,
                component_index,
                component_kind,
                component_name
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                62,
                17,
                1_773_782_702,
                "Pending",
                1_773_782_702,
                1_773_782_702,
                "",
                1,
                0,
                "ansible",
                "Playbook SCP",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_run_targets(
                run_id,
                device_guid,
                hostname,
                site_id,
                resolved_from_filter_id,
                inventory_hostname,
                wireguard_peer_ip,
                resolved_connection,
                resolution_status,
                resolution_reason,
                resolved_from_filter_ids_json,
                created_at
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                62,
                "GUID-TEST-0001",
                "test-device",
                1,
                None,
                "main_lab__test_device",
                "",
                "ssh",
                "pending",
                "",
                json.dumps([]),
                1_773_782_702,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setenv("BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD", "scp")
    monkeypatch.setenv("BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS", "-O")
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {"test-device-agent": {"virtual_ip": "10.77.0.15/32"}},
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook SCP",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-test-scp",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_scp.yml",
            },
        ),
    )

    captured: dict[str, object] = {}
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.setdefault("kwargs", kwargs),
    )

    result = scheduler._dispatch_shared_ansible(
        job_id=17,
        run_row_id=62,
        scheduled_ts=1_773_782_702,
        run_mode="ssh",
        component={"name": "Playbook SCP", "path": "playbook_scp.yml"},
        credential_id=5,
        use_service_account=False,
    )

    assert result is not None
    target_specifications = captured["kwargs"]["target_specifications"]
    assert target_specifications[0]["host_vars"]["ansible_ssh_transfer_method"] == "scp"
    assert target_specifications[0]["host_vars"]["ansible_scp_extra_args"] == "-O"


def test_tick_respects_individual_ansible_job_concurrency_limit(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    scheduler._running = False

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                credential_id,
                use_service_account,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                71,
                "Limited Individual SSH Job",
                json.dumps([{"type": "ansible", "name": "Playbook A", "path": "playbook_a.yml"}]),
                json.dumps(
                    [
                        {
                            "kind": "device",
                            "device_guid": "GUID-TEST-0001",
                            "hostname": "test-device",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                        {
                            "kind": "device",
                            "device_guid": "GUID-TEST-0002",
                            "hostname": "test-device-02",
                            "site_id": 1,
                            "site_name": "Main Lab",
                        },
                    ]
                ),
                "immediately",
                "ssh_individual",
                5,
                0,
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(scheduler, "_resolve_occurrence_for_tick", lambda **_kwargs: 1_773_782_701)
    monkeypatch.setattr(scheduled_job_module, "load_ansible_runner_settings", lambda: {"job_concurrency_limit": 1, "global_concurrency_limit": 50})
    monkeypatch.setattr(scheduler, "_online_lookup", lambda: ["test-device", "test-device-02"])
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "resolve_target_entries",
        lambda raw_targets, devices=None: (
            ["test-device", "test-device-02"],
            {
                "resolved_targets": [
                    {
                        "device_guid": "guid-test-0001",
                        "hostname": "test-device",
                        "site_id": 1,
                        "site_name": "Main Lab",
                        "resolved_from_filter_ids": [],
                    },
                    {
                        "device_guid": "guid-test-0002",
                        "hostname": "test-device-02",
                        "site_id": 1,
                        "site_name": "Main Lab",
                        "resolved_from_filter_ids": [],
                    },
                ]
            },
        ),
    )
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "fetch_devices",
        lambda: [
            {
                "device_guid": "guid-test-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent",
                "operating_system": "Ubuntu 24.04 LTS",
                "connection_endpoint": "",
            },
            {
                "device_guid": "guid-test-0002",
                "hostname": "test-device-02",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent-02",
                "operating_system": "Rocky Linux 9.7",
                "connection_endpoint": "",
            },
        ],
    )
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {
            "test-device-agent": {"virtual_ip": "10.77.0.15/32"},
            "test-device-agent-02": {"virtual_ip": "10.77.0.16/32"},
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook A",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-playbook-a",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_a.yml",
            },
        ),
    )

    captured: list[dict[str, object]] = []
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.append(kwargs) or "runner-1",
    )

    scheduler._tick_once()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT target_hostname, status
              FROM scheduled_job_runs
             WHERE job_id=?
          ORDER BY id ASC
            """,
            (71,),
        )
        run_rows = cur.fetchall()
    finally:
        conn.close()

    assert run_rows == [
        ("test-device", "Running"),
        ("test-device-02", "Pending"),
    ]
    assert captured
    assert all(call["hostname"] == "test-device" for call in captured)


def test_tick_respects_individual_ansible_global_concurrency_limit(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)
    scheduler._running = False

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.executemany(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                execution_context,
                credential_id,
                use_service_account,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                (
                    72,
                    "Global Limit Job A",
                    json.dumps([{"type": "ansible", "name": "Playbook A", "path": "playbook_a.yml"}]),
                    json.dumps(
                        [
                            {
                                "kind": "device",
                                "device_guid": "GUID-TEST-0001",
                                "hostname": "test-device",
                                "site_id": 1,
                                "site_name": "Main Lab",
                            }
                        ]
                    ),
                    "immediately",
                    "ssh_individual",
                    5,
                    0,
                    1_773_780_000,
                    1_773_780_000,
                    1,
                ),
                (
                    73,
                    "Global Limit Job B",
                    json.dumps([{"type": "ansible", "name": "Playbook A", "path": "playbook_a.yml"}]),
                    json.dumps(
                        [
                            {
                                "kind": "device",
                                "device_guid": "GUID-TEST-0002",
                                "hostname": "test-device-02",
                                "site_id": 1,
                                "site_name": "Main Lab",
                            }
                        ]
                    ),
                    "immediately",
                    "ssh_individual",
                    5,
                    0,
                    1_773_780_000,
                    1_773_780_000,
                    1,
                ),
            ),
        )
        cur.execute(
            """
            UPDATE devices
               SET operating_system=?,
                   agent_id=?,
                   connection_endpoint=''
             WHERE guid=?
            """,
            ("Ubuntu 24.04 LTS", "test-device-agent", "GUID-TEST-0001"),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(scheduler, "_resolve_occurrence_for_tick", lambda **kwargs: 1_773_782_702)
    monkeypatch.setattr(scheduled_job_module, "load_ansible_runner_settings", lambda: {"job_concurrency_limit": 20, "global_concurrency_limit": 1})
    monkeypatch.setattr(scheduler, "_online_lookup", lambda: ["test-device", "test-device-02"])
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "resolve_target_entries",
        lambda raw_targets, devices=None: (
            [target.get("hostname") for target in raw_targets],
            {
                "resolved_targets": [
                    {
                        "device_guid": str(target.get("device_guid") or "").strip().lower(),
                        "hostname": target.get("hostname"),
                        "site_id": target.get("site_id"),
                        "site_name": target.get("site_name"),
                        "resolved_from_filter_ids": [],
                    }
                    for target in raw_targets
                ]
            },
        ),
    )
    monkeypatch.setattr(
        scheduler._filter_matcher,
        "fetch_devices",
        lambda: [
            {
                "device_guid": "guid-test-0001",
                "hostname": "test-device",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent",
                "operating_system": "Ubuntu 24.04 LTS",
                "connection_endpoint": "",
            },
            {
                "device_guid": "guid-test-0002",
                "hostname": "test-device-02",
                "site_id": 1,
                "site_name": "Main Lab",
                "agent_id": "test-device-agent-02",
                "operating_system": "Rocky Linux 9.7",
                "connection_endpoint": "",
            },
        ],
    )
    monkeypatch.setattr(
        scheduler,
        "_prepare_vpn_sessions",
        lambda _agent_ids, required_ports=None: {
            "test-device-agent": {"virtual_ip": "10.77.0.15/32"},
            "test-device-agent-02": {"virtual_ip": "10.77.0.16/32"},
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_load_credential",
        lambda _credential_id: {
            "id": 5,
            "name": "SSH Test Credential",
            "connection_type": "ssh",
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
    )
    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, _default_type, assembly_guid=None: (
            {
                "name": "Playbook A",
                "script": "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - ansible.builtin.ping:\n",
                "variables": [],
                "files": [],
            },
            {
                "assembly_guid": assembly_guid or "guid-playbook-a",
                "virtual_path": rel_path or "Ansible_Playbooks/playbook_a.yml",
            },
        ),
    )

    captured: list[dict[str, object]] = []
    monkeypatch.setattr(
        scheduler,
        "_server_ansible_runner",
        lambda **kwargs: captured.append(kwargs) or f"runner-{len(captured)}",
    )

    scheduler._tick_once()

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT job_id, target_hostname, status
              FROM scheduled_job_runs
             WHERE job_id IN (?, ?)
          ORDER BY job_id ASC, id ASC
            """,
            (72, 73),
        )
        run_rows = cur.fetchall()
    finally:
        conn.close()

    assert run_rows == [
        (72, "test-device", "Running"),
        (73, "test-device-02", "Pending"),
    ]
    assert captured
    assert all(call["hostname"] == "test-device" for call in captured)


class _FakeSigner:
    def sign(self, payload: bytes) -> bytes:
        return b"signature"

    def public_base64_spki(self) -> str:
        return "public-key"


@pytest.mark.parametrize("reason", ["stale_handshake", "no_recent_handshake", "transport_probe_pending"])
def test_wireguard_soft_dispatch_reasons_allow_remote_attempt(reason: str) -> None:
    assert scheduled_job_module._wireguard_session_allows_remote_attempt(
        {
            "dispatch_ready": False,
            "dispatch_ready_reason": reason,
            "agent_ready": True,
            "peer_present": True,
        },
        "10.255.0.4",
    )


@pytest.mark.parametrize("reason", ["agent_ready_missing", "agent_ready_missing_required_ports", "peer_missing"])
def test_wireguard_hard_dispatch_reasons_still_skip_remote_attempt(reason: str) -> None:
    assert not scheduled_job_module._wireguard_session_allows_remote_attempt(
        {
            "dispatch_ready": False,
            "dispatch_ready_reason": reason,
            "agent_ready": reason != "agent_ready_missing",
            "peer_present": reason != "peer_missing",
        },
        "10.255.0.4",
    )


def test_scheduled_job_dispatch_fails_cleanly_when_system_socket_is_missing(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?)
            """,
            (
                3,
                "System Context Run",
                json.dumps([]),
                json.dumps(["LAB-AIO-01"]),
                "once",
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                status,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?)
            """,
            (
                30,
                3,
                "LAB-AIO-01",
                1_773_782_700,
                "Pending",
                1_773_782_700,
                1_773_782_700,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, default_type, assembly_guid=None: (
            {
                "type": "powershell",
                "script": "Write-Host 'hello'",
                "variables": [],
                "timeout_seconds": 60,
                "files": [],
                "name": "Hello Script",
            },
            {
                "assembly_guid": "GUID-HELLO-0001",
                "virtual_path": "Scripts/hello.ps1",
            },
        ),
    )
    scheduler._script_signer = _FakeSigner()
    scheduler._emit_host_service_event = lambda hostname, service_mode, event, payload: False
    emitted_events = []
    monkeypatch.setattr(
        scheduler.socketio,
        "emit",
        lambda event, payload, to=None: emitted_events.append((event, payload, to)),
    )

    link = scheduler._dispatch_script(
        3,
        30,
        1_773_782_700,
        "LAB-AIO-01",
        {"path": "hello.ps1"},
        "system",
    )

    assert link is not None
    assert link["component_name"] == "Hello Script"
    assert all(event_name != "quick_job_run" for event_name, _payload, _to in emitted_events)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, finished_ts, error FROM scheduled_job_runs WHERE id=?", (30,))
        run_row = cur.fetchone()
        assert run_row[0] == "Failed"
        assert int(run_row[1] or 0) > 0
        assert "No system agent socket is registered" in (run_row[2] or "")

        cur.execute("SELECT status, stdout, stderr FROM activity_history ORDER BY id DESC LIMIT 1")
        activity_row = cur.fetchone()
        assert activity_row[0] == "Failed"
        assert activity_row[1] == ""
        assert "No system agent socket is registered" in (activity_row[2] or "")
    finally:
        conn.close()


def test_scheduled_job_dispatch_preserves_ansible_preflight_skip(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                status,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?)
            """,
            (
                33,
                5,
                "LAB-DOCS-01",
                1_773_782_800,
                "Pending",
                1_773_782_800,
                1_773_782_800,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    def _finalize_ansible_skip(*_args, **_kwargs):
        conn_update = sqlite3.connect(str(engine_harness.db_path))
        try:
            cur_update = conn_update.cursor()
            cur_update.execute(
                """
                UPDATE scheduled_job_runs
                   SET status=?,
                       finished_ts=?,
                       updated_at=?,
                       skip_reason=?,
                       error=?
                 WHERE id=?
                """,
                (
                    "Skipped",
                    1_773_782_845,
                    1_773_782_845,
                    "no_eligible_targets",
                    "Managed WireGuard session is not ready for this target (stale_handshake).",
                    33,
                ),
            )
            conn_update.commit()
        finally:
            conn_update.close()
        return None

    monkeypatch.setattr(scheduler, "_dispatch_ansible", _finalize_ansible_skip)

    dispatched = scheduler._dispatch_run_activities(
        job_id=5,
        run_row_id=33,
        scheduled_ts=1_773_782_800,
        hostname="LAB-DOCS-01",
        run_mode="ssh",
        script_components=[],
        ansible_components=[{"path": "Ansible_Playbooks/playbooks/linux/ping-pong.json"}],
        credential_id=1,
        use_service_account=False,
    )

    assert dispatched is False
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, finished_ts, skip_reason, error FROM scheduled_job_runs WHERE id=?", (33,))
        row = cur.fetchone()
        assert row[0] == "Skipped"
        assert row[1] == 1_773_782_845
        assert row[2] == "no_eligible_targets"
        assert "stale_handshake" in row[3]
    finally:
        conn.close()


def test_scheduled_job_dispatch_uses_fresh_finished_ts_for_no_activity_failure(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                status,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?)
            """,
            (
                34,
                6,
                "LAB-AIO-01",
                1_773_782_900,
                "Pending",
                1_773_782_900,
                1_773_782_900,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    now_values = iter([1_773_782_900, 1_773_782_945])

    def _stable_now() -> int:
        import inspect

        if any(frame.function == "_loop" for frame in inspect.stack()):
            return 1_773_782_900
        try:
            return next(now_values)
        except StopIteration:
            return 1_773_782_945

    monkeypatch.setattr(scheduled_job_module, "_now_ts", _stable_now)

    dispatched = scheduler._dispatch_run_activities(
        job_id=6,
        run_row_id=34,
        scheduled_ts=1_773_782_900,
        hostname="LAB-AIO-01",
        run_mode="system",
        script_components=[],
        ansible_components=[],
        credential_id=None,
        use_service_account=True,
    )

    assert dispatched is False
    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT started_ts, finished_ts, status, error FROM scheduled_job_runs WHERE id=?", (34,))
        row = cur.fetchone()
        assert row[0] == 1_773_782_900
        assert row[1] == 1_773_782_945
        assert row[2] == "Failed"
        assert row[3] == "No runnable activities were dispatched"
    finally:
        conn.close()


@pytest.mark.parametrize(
    ("script_type", "script_body", "script_path"),
    [
        ("batch", "@echo off\r\necho scheduled\r\n", "Scripts/hello.bat"),
        ("bash", "echo scheduled\n", "Scripts/hello.sh"),
    ],
)
def test_scheduled_job_dispatch_accepts_batch_and_bash(
    engine_harness: EngineTestHarness,
    monkeypatch,
    script_type: str,
    script_body: str,
    script_path: str,
) -> None:
    _client, scheduler = _scheduled_jobs_client(engine_harness)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO scheduled_jobs(
                id,
                name,
                components_json,
                targets_json,
                schedule_type,
                created_at,
                updated_at,
                enabled
            ) VALUES (?,?,?,?,?,?,?,?)
            """,
            (
                4,
                f"{script_type.title()} Context Run",
                json.dumps([]),
                json.dumps(["LAB-AIO-01"]),
                "once",
                1_773_780_000,
                1_773_780_000,
                1,
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                target_hostname,
                scheduled_ts,
                status,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?)
            """,
            (
                31 if script_type == "batch" else 32,
                4,
                "LAB-AIO-01",
                1_773_782_710,
                "Pending",
                1_773_782_710,
                1_773_782_710,
            ),
        )
        conn.commit()
    finally:
        conn.close()

    monkeypatch.setattr(
        scheduler,
        "_resolve_runtime_document",
        lambda rel_path, default_type, assembly_guid=None: (
            {
                "type": script_type,
                "script": script_body,
                "variables": [],
                "timeout_seconds": 60,
                "files": [],
                "name": f"{script_type.title()} Script",
            },
            {
                "assembly_guid": "GUID-SCRIPT-TYPE-0001",
                "virtual_path": script_path,
            },
        ),
    )
    scheduler._script_signer = _FakeSigner()
    dispatched: list[tuple[str, str, str, dict]] = []
    scheduler._emit_host_service_event = lambda hostname, service_mode, event, payload: (
        dispatched.append((hostname, service_mode, event, payload)) or True
    )

    link = scheduler._dispatch_script(
        4,
        31 if script_type == "batch" else 32,
        1_773_782_710,
        "LAB-AIO-01",
        {"path": script_path},
        "system",
    )

    assert link is not None
    assert link["component_name"] == f"{script_type.title()} Script"
    assert len(dispatched) == 1
    hostname, service_mode, event_name, payload = dispatched[0]
    assert hostname == "LAB-AIO-01"
    assert service_mode == "system"
    assert event_name == "quick_job_run"
    assert payload["script_type"] == script_type
    assert payload["target_context"] == "system"

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT script_type, status FROM activity_history ORDER BY id DESC LIMIT 1")
        row = cur.fetchone()
        assert row[0] == script_type
        assert row[1] == "Queued"
    finally:
        conn.close()
