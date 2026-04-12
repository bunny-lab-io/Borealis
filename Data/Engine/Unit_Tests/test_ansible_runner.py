# ======================================================
# Data\Engine\Unit_Tests\test_ansible_runner.py
# Description: Covers Engine-side Ansible runner timeout handling and recap persistence.
# ======================================================

from __future__ import annotations

import json

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
        "run",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(TimeoutExpired()),
    )

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
