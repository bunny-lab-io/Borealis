# ======================================================
# Data\Engine\Unit_Tests\test_ansible_runner.py
# Description: Covers Engine-side Ansible runner timeout handling and recap persistence.
# ======================================================

from __future__ import annotations

import json
from pathlib import Path
import signal
import subprocess
import tempfile

from Data.Engine.db import dbapi as sqlite3
import Data.Engine.services.ansible.runner as ansible_runner_module
from Data.Engine.services.ansible.runner import EngineAnsibleRunner, RUN_STATUS_TIMED_OUT

from .conftest import EngineTestHarness


class _DummySocketIO:
    def emit(self, _event: str, _payload: object) -> None:
        return None


class TimeoutExpired(Exception):
    __module__ = "subprocess"

    def __init__(self) -> None:
        super().__init__("Command '['ansible-playbook']' timed out after 1 seconds")
        self.cmd = ["ansible-playbook"]
        self.timeout = 1
        self.stdout = None
        self.stderr = None


class _FakeProcess:
    def __init__(
        self,
        *,
        exception_to_raise: Exception | None = None,
        stdout: str = "",
        stderr: str = "",
        returncode: int | None = 0,
    ) -> None:
        self.pid = 4321
        self._exception_to_raise = exception_to_raise
        self._stdout = stdout
        self._stderr = stderr
        self.returncode = returncode
        self._communicate_calls = 0
        self.wait_calls: list[float | None] = []
        self.terminated = False
        self.killed = False

    def communicate(self, timeout: float | None = None) -> tuple[str, str]:
        self._communicate_calls += 1
        if self._communicate_calls == 1 and self._exception_to_raise is not None:
            raise self._exception_to_raise
        return self._stdout, self._stderr

    def wait(self, timeout: float | None = None) -> int:
        self.wait_calls.append(timeout)
        self.returncode = self.returncode if self.returncode is not None else -signal.SIGTERM
        return int(self.returncode)

    def poll(self) -> int | None:
        return self.returncode

    def terminate(self) -> None:
        self.terminated = True
        self.returncode = -signal.SIGTERM

    def kill(self) -> None:
        self.killed = True
        self.returncode = -signal.SIGKILL


def _ensure_ansible_runner_tables(engine_harness: EngineTestHarness) -> None:
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
            )
            """
        )
        cur.execute(
            """
            INSERT INTO activity_history(
                id,
                hostname,
                script_path,
                script_name,
                script_type,
                ran_at,
                status,
                stdout,
                stderr
            ) VALUES (?,?,?,?,?,?,?,?,?)
            """,
            (
                700,
                "borealis-engine-01",
                "Ansible_Playbooks/test-timeout.yml",
                "Timeout Test",
                "ansible",
                1_773_782_700,
                "Running",
                "",
                "",
            ),
        )
        cur.execute(
            """
            INSERT INTO scheduled_job_runs(
                id,
                job_id,
                scheduled_ts,
                started_ts,
                status,
                created_at,
                updated_at
            ) VALUES (?,?,?,?,?,?,?)
            """,
            (
                70,
                7,
                1_773_782_700,
                1_773_782_700,
                "Running",
                1_773_782_700,
                1_773_782_700,
            ),
        )
        conn.commit()
    finally:
        conn.close()


def test_runner_normalizes_eventlet_wrapped_timeout_exceptions(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _ensure_ansible_runner_tables(engine_harness)

    runner = EngineAnsibleRunner(
        socketio=_DummySocketIO(),
        db_conn_factory=lambda: sqlite3.connect(str(engine_harness.db_path)),
    )

    workspace_root = engine_harness.db_path.parent / "ansible-timeout-workspace"
    project_dir = workspace_root / "project"
    runtime_dir = workspace_root / "runtime"
    project_dir.mkdir(parents=True, exist_ok=True)
    runtime_dir.mkdir(parents=True, exist_ok=True)
    playbook_path = project_dir / "playbook.yml"
    inventory_path = workspace_root / "inventory.yml"
    cfg_path = workspace_root / "ansible.cfg"
    ssh_known_hosts_path = workspace_root / "ssh_known_hosts"
    extra_vars_path = workspace_root / "extra_vars.json"
    playbook_path.write_text("---\n- hosts: all\n  gather_facts: false\n  tasks: []\n", encoding="utf-8")
    inventory_path.write_text("{}", encoding="utf-8")
    cfg_path.write_text("[defaults]\n", encoding="utf-8")
    ssh_known_hosts_path.write_text("", encoding="utf-8")

    monkeypatch.setattr(
        runner,
        "_prepare_workspace",
        lambda **_kwargs: {
            "root": workspace_root,
            "project_dir": project_dir,
            "playbook_path": playbook_path,
            "inventory_path": inventory_path,
            "cfg_path": cfg_path,
            "ssh_known_hosts_path": ssh_known_hosts_path,
            "extra_vars_path": extra_vars_path,
            "runtime_dir": runtime_dir,
            "limit_name": "borealis_targets",
            "inventory_hosts": ["borealis-engine-01"],
        },
    )
    monkeypatch.setattr(
        ansible_runner_module.subprocess,
        "Popen",
        lambda *_args, **_kwargs: _FakeProcess(exception_to_raise=TimeoutExpired(), returncode=None),
    )
    monkeypatch.setattr(ansible_runner_module.os, "killpg", lambda *_args, **_kwargs: None)

    runner._run_playbook(
        run_id="run-timeout-1",
        hostname="borealis-engine-01",
        playbook_abs_path="",
        playbook_content="---\n- hosts: all\n  gather_facts: false\n  tasks: []\n",
        playbook_rel_path="Ansible_Playbooks/test-timeout.yml",
        playbook_name="Timeout Test",
        credential_id=None,
        variable_values={},
        payload_files=[],
        target_specifications=[
            {
                "hostname": "borealis-engine-01",
                "inventory_hostname": "borealis-engine-01",
                "site_group": "site_local",
                "host_vars": {"ansible_connection": "local"},
            }
        ],
        runtime_files=[],
        source="scheduled_job",
        activity_id=700,
        scheduled_job_id=7,
        scheduled_run_id=70,
        scheduled_job_run_row_id=70,
        connection="local",
    )

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, error FROM scheduled_job_runs WHERE id=?", (70,))
        run_row = cur.fetchone()
        cur.execute("SELECT status, stderr FROM activity_history WHERE id=?", (700,))
        activity_row = cur.fetchone()
        cur.execute("SELECT status, recap_json FROM ansible_play_recaps WHERE run_id=?", ("run-timeout-1",))
        recap_row = cur.fetchone()
    finally:
        conn.close()

    assert run_row[0] == RUN_STATUS_TIMED_OUT
    assert run_row[1].startswith("Ansible playbook execution exceeded 900 seconds and was terminated.")
    assert "timed out after 1 seconds" not in run_row[1]
    assert activity_row[0] == RUN_STATUS_TIMED_OUT
    assert activity_row[1].startswith("Ansible playbook execution exceeded 900 seconds and was terminated.")
    assert "timed out after 1 seconds" not in activity_row[1]
    assert recap_row[0] == RUN_STATUS_TIMED_OUT
    recap_payload = json.loads(recap_row[1])
    assert recap_payload["timed_out"] is True
    assert recap_payload["timeout_seconds"] == 900


def test_runner_writes_isolated_ssh_control_path_dir(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    runner = EngineAnsibleRunner(
        socketio=_DummySocketIO(),
        db_conn_factory=lambda: sqlite3.connect(str(engine_harness.db_path)),
    )

    generated_root = engine_harness.db_path.parent / "ansible-generated"
    collections_root = engine_harness.db_path.parent / "collections"
    collections_root.mkdir(parents=True, exist_ok=True)
    monkeypatch.setattr(runner, "_generated_runtime_root", lambda: generated_root)
    monkeypatch.setattr(runner, "_collections_root", lambda: collections_root)
    monkeypatch.setattr(tempfile, "gettempdir", lambda: str(engine_harness.db_path.parent / "tmp"))

    workspace = runner._prepare_workspace(
        run_id="run-kex-1",
        hostname="borealis-engine-01",
        playbook_abs_path="",
        playbook_content="---\n- hosts: all\n  gather_facts: false\n  tasks: []\n",
        playbook_rel_path="Ansible_Playbooks/test-kex.yml",
        playbook_name="Control Path Test",
        payload_files=[],
        runtime_files=[],
        target_specifications=[
            {
                "hostname": "example-host",
                "inventory_hostname": "example_host",
                "site_group": "site_test",
                "host_vars": {
                    "ansible_host": "10.255.0.10",
                    "ansible_connection": "ssh",
                },
            }
        ],
        connection="ssh",
    )

    cfg_text = workspace["cfg_path"].read_text(encoding="utf-8")
    assert "-o KexAlgorithms=" not in cfg_text
    expected_control_dir = engine_harness.db_path.parent / "tmp" / "ansible_controlplane" / "run-kex-1"
    assert f"control_path_dir = {expected_control_dir}" in cfg_text


def test_runner_kills_process_group_on_timeout(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    _ensure_ansible_runner_tables(engine_harness)

    runner = EngineAnsibleRunner(
        socketio=_DummySocketIO(),
        db_conn_factory=lambda: sqlite3.connect(str(engine_harness.db_path)),
    )

    workspace_root = engine_harness.db_path.parent / "ansible-timeout-cleanup-workspace"
    project_dir = workspace_root / "project"
    runtime_dir = workspace_root / "runtime"
    project_dir.mkdir(parents=True, exist_ok=True)
    runtime_dir.mkdir(parents=True, exist_ok=True)
    playbook_path = project_dir / "playbook.yml"
    inventory_path = workspace_root / "inventory.yml"
    cfg_path = workspace_root / "ansible.cfg"
    ssh_known_hosts_path = workspace_root / "ssh_known_hosts"
    extra_vars_path = workspace_root / "extra_vars.json"
    playbook_path.write_text("---\n- hosts: all\n  gather_facts: false\n  tasks: []\n", encoding="utf-8")
    inventory_path.write_text("{}", encoding="utf-8")
    cfg_path.write_text("[defaults]\n", encoding="utf-8")
    ssh_known_hosts_path.write_text("", encoding="utf-8")

    monkeypatch.setattr(
        runner,
        "_prepare_workspace",
        lambda **_kwargs: {
            "root": workspace_root,
            "project_dir": project_dir,
            "playbook_path": playbook_path,
            "inventory_path": inventory_path,
            "cfg_path": cfg_path,
            "ssh_known_hosts_path": ssh_known_hosts_path,
            "extra_vars_path": extra_vars_path,
            "runtime_dir": runtime_dir,
            "limit_name": "borealis_targets",
            "inventory_hosts": ["example-host"],
        },
    )

    fake_timeout = subprocess.TimeoutExpired(
        cmd=["ansible-playbook"],
        timeout=900,
        output="partial stdout",
        stderr="partial stderr",
    )
    fake_process = _FakeProcess(exception_to_raise=fake_timeout, returncode=None)
    popen_kwargs: dict[str, object] = {}
    killpg_calls: list[tuple[int, signal.Signals]] = []

    def _fake_popen(*_args, **kwargs):
        popen_kwargs.update(kwargs)
        return fake_process

    monkeypatch.setattr(ansible_runner_module.subprocess, "Popen", _fake_popen)
    monkeypatch.setattr(
        ansible_runner_module.os,
        "killpg",
        lambda pid, sig: killpg_calls.append((pid, sig)),
    )

    runner._run_playbook(
        run_id="run-timeout-cleanup-1",
        hostname="example-host",
        playbook_abs_path="",
        playbook_content="---\n- hosts: all\n  gather_facts: false\n  tasks: []\n",
        playbook_rel_path="Ansible_Playbooks/test-timeout-cleanup.yml",
        playbook_name="Timeout Cleanup Test",
        credential_id=None,
        variable_values={},
        payload_files=[],
        target_specifications=[
            {
                "hostname": "example-host",
                "inventory_hostname": "example-host",
                "site_group": "site_remote",
                "host_vars": {
                    "ansible_connection": "ssh",
                    "ansible_host": "10.255.0.10",
                },
            }
        ],
        runtime_files=[],
        source="scheduled_job",
        activity_id=700,
        scheduled_job_id=7,
        scheduled_run_id=70,
        scheduled_job_run_row_id=70,
        connection="ssh",
    )

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT status, error FROM scheduled_job_runs WHERE id=?", (70,))
        run_row = cur.fetchone()
    finally:
        conn.close()

    assert popen_kwargs["start_new_session"] is True
    assert killpg_calls == [(fake_process.pid, signal.SIGTERM)]
    assert run_row[0] == RUN_STATUS_TIMED_OUT
    assert run_row[1].startswith("Ansible playbook execution exceeded 900 seconds and was terminated.")
