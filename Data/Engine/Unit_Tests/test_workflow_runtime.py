# ======================================================
# Data\Engine\Unit_Tests\test_workflow_runtime.py
# Description: Focused workflow runtime dispatch regression tests.
# ======================================================

from __future__ import annotations

import base64
import logging
import time
from pathlib import Path
from types import SimpleNamespace

from flask import Flask

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.API.workflows import management as workflow_management
from Data.Engine.services.auth import DevModeManager
from Data.Engine.services.workflows.runtime import WorkflowRuntimeService


class _DummyAssemblyRuntime:
    pass


class _RecordingSocket:
    def __init__(self) -> None:
        self.events = []

    def emit(self, event: str, payload=None) -> None:
        self.events.append((event, payload))


class _FakeSigner:
    def __init__(self) -> None:
        self.calls = []

    def sign(self, payload: bytes) -> bytes:
        self.calls.append(payload)
        return b"signed-workflow-payload"

    def public_base64_spki(self) -> str:
        return "workflow-public-key"


class _FakeWorkflowRuntime:
    def __init__(self) -> None:
        self.calls = []

    def resolve_stuck_run(
        self,
        run_id: int,
        *,
        status: str | None = None,
        actor: str = "",
        recovery_reason: str = "manual_admin_resolve",
    ):
        self.calls.append(
            {
                "run_id": int(run_id),
                "status": status,
                "actor": actor,
                "recovery_reason": recovery_reason,
            }
        )
        resolved_status = str(status or "Failed")
        return {
            "resolved": True,
            "reason": recovery_reason,
            "status": resolved_status,
            "run": {
                "id": int(run_id),
                "status": resolved_status,
            },
        }


def _create_activity_table(db_path: Path) -> None:
    conn = sqlite3.connect(str(db_path))
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
        conn.commit()
    finally:
        conn.close()


def _seed_orphaned_run(
    runtime: WorkflowRuntimeService,
    *,
    run_status: str,
    node_status: str,
    node_timeout_seconds: int = 0,
    started_ts: int | None = None,
) -> tuple[int, str]:
    graph_snapshot = {
        "tab_name": "Recovery Test Workflow",
        "nodes": [
            {
                "id": "assembly-1",
                "type": "workflow_execute_assembly",
                "data": {
                    "label": "Execute Assembly",
                    "assembly_guid": "ASM-1",
                },
            }
        ],
        "edges": [],
    }
    run_id = runtime._create_run_row(
        workflow_guid="WF-RECOVERY-1",
        workflow_name="Recovery Test Workflow",
        source_type="manual",
        source_metadata={},
        graph_snapshot=graph_snapshot,
        status="Pending",
        created_at=started_ts or int(time.time()),
    )
    node_run_ids = runtime._create_initial_node_runs(run_id, graph_snapshot["nodes"])
    node_id = "assembly-1"
    node_run_id = int(node_run_ids[node_id])
    seed_ts = int(started_ts or time.time())

    conn = runtime._conn()
    try:
        cur = conn.cursor()
        cur.execute(
            """
            UPDATE workflow_runs
               SET status=?,
                   started_ts=?,
                   updated_at=?
             WHERE id=?
            """,
            (
                run_status,
                seed_ts if str(run_status).strip().lower() == "running" else None,
                seed_ts,
                int(run_id),
            ),
        )
        cur.execute(
            """
            UPDATE workflow_node_runs
               SET status=?,
                   timeout_seconds=?,
                   started_ts=?,
                   updated_at=?
             WHERE id=?
            """,
            (
                node_status,
                int(node_timeout_seconds),
                seed_ts if str(node_status).strip().lower() == "running" else None,
                seed_ts,
                node_run_id,
            ),
        )
        conn.commit()
    finally:
        conn.close()
    return int(run_id), node_id


def test_workflow_script_dispatch_includes_signature_payload(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    socket = _RecordingSocket()
    signer = _FakeSigner()
    runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        script_signer=signer,
        socketio=socket,
        logger=logging.getLogger("workflow-runtime-test"),
    )

    script_bytes = b"Write-Host 'hello from workflow'"
    activity_id, error = runtime._dispatch_script_activity(
        hostname="LAB-AIO-01",
        script_name="Workflow Test",
        script_path="Scripts/workflow-test.ps1",
        script_type="powershell",
        script_bytes=script_bytes,
        script_content_b64=base64.b64encode(script_bytes).decode("ascii"),
        environment={},
        variables=[],
        timeout_seconds=30,
        files=[],
        run_mode="system",
        context={"workflow_run_id": 1},
    )

    assert activity_id is not None
    assert error == ""
    assert signer.calls == [script_bytes]
    quick_job_events = [payload for event, payload in socket.events if event == "quick_job_run"]
    assert len(quick_job_events) == 1
    payload = quick_job_events[0]
    assert payload["signature"] == base64.b64encode(b"signed-workflow-payload").decode("ascii")
    assert payload["sig_alg"] == "ed25519"
    assert payload["signing_key"] == "workflow-public-key"
    assert payload["target_context"] == "system"


def test_workflow_validation_rejects_legacy_generic_wiring(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )

    errors = runtime.validate_workflow_document(
        workflow_guid="WF-LEGACY",
        source_type="manual",
        workflow_payload={
            "nodes": [
                {"id": "trigger-1", "type": "workflow_trigger_manual", "data": {"label": "Trigger - Manual"}},
                {
                    "id": "targets-1",
                    "type": "workflow_agent_array",
                    "data": {"label": "Agent Array", "selected_devices": [{"hostname": "LAB-AIO-01"}]},
                },
                {
                    "id": "assembly-1",
                    "type": "workflow_execute_assembly",
                    "data": {"label": "Execute Assembly", "assembly_guid": "ASM-1"},
                },
            ],
            "edges": [
                {"id": "edge-trigger", "source": "trigger-1", "target": "assembly-1", "data": {"route_on": "always"}},
                {"id": "edge-targets", "source": "targets-1", "target": "assembly-1"},
            ],
        },
    )

    assert any("legacy output wiring" in error for error in errors)
    assert any("legacy input wiring" in error for error in errors)


def test_job_output_route_filters_matching_targets(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )

    source_output = runtime._build_output_envelope(
        status="Failed",
        data={
            "job_output": [
                {
                    "hostname": "LAB-AIO-01",
                    "device_guid": "GUID-1",
                    "site_id": 1,
                    "site_name": "Bunny Lab",
                    "agent_id": "AGENT-1",
                    "status": "Failed",
                    "stderr": "Boom",
                },
                {
                    "hostname": "LAB-OPERATOR-01",
                    "device_guid": "GUID-2",
                    "site_id": 1,
                    "site_name": "Bunny Lab",
                    "agent_id": "AGENT-2",
                    "status": "Success",
                    "stderr": "",
                },
            ]
        },
        metadata={"target_count": 2},
        artifacts={},
    )

    input_envelope, ignored_inputs, matched_count = runtime._build_input_envelope(
        node_id="assembly-2",
        incoming_edges=[
            {
                "id": "edge-job-output-failed",
                "source": "assembly-1",
                "sourceHandle": "job_output",
                "target": "assembly-2",
                "targetHandle": "targets",
                "data": {"route_on": "on_failed"},
            }
        ],
        outputs={"assembly-1": source_output},
        node_map={
            "assembly-1": {
                "id": "assembly-1",
                "type": "workflow_execute_assembly",
                "data": {"label": "Execute Assembly"},
            },
            "assembly-2": {
                "id": "assembly-2",
                "type": "workflow_execute_assembly",
                "data": {"label": "Execute Assembly", "assembly_guid": "ASM-2"},
            },
        },
    )

    assert matched_count == 1
    assert ignored_inputs == []
    payload = input_envelope["data"]["inputs_by_port"]["targets"]["inputs"][0]["output"]["data"]
    assert len(payload["job_output"]) == 1
    assert payload["job_output"][0]["hostname"] == "LAB-AIO-01"
    assert len(payload["targets"]) == 1
    assert payload["targets"][0]["hostname"] == "LAB-AIO-01"


def test_execute_assembly_accepts_multiple_trigger_inputs(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )

    errors = runtime.validate_workflow_document(
        workflow_guid="WF-MULTI-TRIGGER",
        source_type="manual",
        workflow_payload={
            "nodes": [
                {"id": "manual-trigger", "type": "workflow_trigger_manual", "data": {"label": "Trigger - Manual"}},
                {"id": "scheduled-trigger", "type": "workflow_trigger_scheduled_job", "data": {"label": "Trigger - Scheduled Job"}},
                {
                    "id": "targets-1",
                    "type": "workflow_agent_array",
                    "data": {"label": "Agent Array", "selected_devices": [{"hostname": "LAB-AIO-01"}]},
                },
                {
                    "id": "assembly-1",
                    "type": "workflow_execute_assembly",
                    "data": {"label": "Execute Assembly", "assembly_guid": "ASM-1"},
                },
            ],
            "edges": [
                {
                    "id": "edge-trigger-direct",
                    "source": "manual-trigger",
                    "sourceHandle": "action",
                    "target": "assembly-1",
                    "targetHandle": "trigger",
                    "data": {"route_on": "always"},
                },
                {
                    "id": "edge-trigger-filter",
                    "source": "scheduled-trigger",
                    "sourceHandle": "action",
                    "target": "assembly-1",
                    "targetHandle": "trigger",
                    "data": {"route_on": "always"},
                },
                {
                    "id": "edge-targets",
                    "source": "targets-1",
                    "sourceHandle": "targets",
                    "target": "assembly-1",
                    "targetHandle": "targets",
                },
            ],
        },
    )

    assert not any("allows only one 'Trigger' input connection" in error for error in errors)


def test_runtime_startup_recovers_timed_out_orphaned_run(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    initial_runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )
    stale_started_ts = int(time.time()) - 120
    run_id, node_id = _seed_orphaned_run(
        initial_runtime,
        run_status="Running",
        node_status="Running",
        node_timeout_seconds=30,
        started_ts=stale_started_ts,
    )

    recovered_runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )
    run = recovered_runtime.get_run(run_id)
    node_run = recovered_runtime.get_node_run(run_id, node_id)

    assert run is not None
    assert node_run is not None
    assert run["status"] == "Timed Out"
    assert run["final_metadata"]["recovery_reason"] == "runtime_startup"
    assert run["final_metadata"]["recovered"] is True
    assert node_run["status"] == "Timed Out"
    assert "Timed Out" in run["error"]


def test_runtime_startup_recovers_pending_orphaned_run_as_failed(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    initial_runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )
    run_id, node_id = _seed_orphaned_run(
        initial_runtime,
        run_status="Pending",
        node_status="Pending",
        node_timeout_seconds=0,
    )

    recovered_runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )
    run = recovered_runtime.get_run(run_id)
    node_run = recovered_runtime.get_node_run(run_id, node_id)

    assert run is not None
    assert node_run is not None
    assert run["status"] == "Failed"
    assert run["final_metadata"]["recovery_reason"] == "runtime_startup"
    assert node_run["status"] == "Skipped"
    assert node_run["skip_reason"] == "workflow_run_recovered"


def test_admin_can_resolve_stuck_workflow_run_via_api(monkeypatch, tmp_path: Path) -> None:
    app = Flask(__name__)
    app.config["SECRET_KEY"] = "workflow-api-test"

    fake_runtime = _FakeWorkflowRuntime()
    monkeypatch.setattr(workflow_management, "ensure_workflow_runtime", lambda _app, _adapters: fake_runtime)

    auth_db_path = tmp_path / "workflow_auth.sqlite3"
    conn = sqlite3.connect(str(auth_db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE users (
                username TEXT PRIMARY KEY,
                role TEXT,
                auth_source TEXT,
                directory_disabled INTEGER
            )
            """
        )
        cur.execute(
            """
            INSERT INTO users (username, role, auth_source, directory_disabled)
            VALUES (?, ?, ?, ?)
            """,
            ("admin", "Admin", "local", 0),
        )
        conn.commit()
    finally:
        conn.close()

    adapters = SimpleNamespace(
        context=SimpleNamespace(logger=logging.getLogger("workflow-management-test"), workflow_runtime=None),
        db_conn_factory=lambda: sqlite3.connect(str(auth_db_path)),
        dev_mode_manager=DevModeManager(logger=logging.getLogger("workflow-management-test")),
        config={},
        service_log=lambda *args, **kwargs: None,
    )
    workflow_management.register_management(app, adapters)

    client = app.test_client()
    with client.session_transaction() as session:
        session["username"] = "admin"
        session["role"] = "Admin"

    response = client.post("/api/workflows/runs/42/resolve", json={"status": "Timed Out"})
    payload = response.get_json()

    assert response.status_code == 200
    assert payload["resolved"] is True
    assert payload["status"] == "Timed Out"
    assert fake_runtime.calls == [
        {
            "run_id": 42,
            "status": "Timed Out",
            "actor": "admin (Admin)",
            "recovery_reason": "manual_admin_resolve",
        }
    ]


def test_workflow_ansible_target_build_requests_ssh_port_in_vpn_prepare(tmp_path: Path) -> None:
    db_path = tmp_path / "workflow_runtime.sqlite3"
    _create_activity_table(db_path)

    runtime = WorkflowRuntimeService(
        db_conn_factory=lambda: sqlite3.connect(str(db_path)),
        assembly_runtime=_DummyAssemblyRuntime(),
        logger=logging.getLogger("workflow-runtime-test"),
    )

    captured: dict[str, object] = {}

    def _capture_prepare(agent_ids, required_ports=None):
        captured["agent_ids"] = list(agent_ids)
        captured["required_ports"] = list(required_ports or [])
        return {"agent-1": {"virtual_ip": "10.77.0.25/32"}}

    runtime.set_vpn_session_prepare(_capture_prepare)

    target_specifications, runtime_files = runtime._build_ansible_target_specifications(
        execution_mode="ssh",
        active_targets=[
            {
                "agent_id": "agent-1",
                "hostname": "lab-docs-01",
                "site_name": "Bunny Lab",
                "site_id": 1,
                "connection_endpoint": "",
            }
        ],
        credential={
            "username": "ubuntu",
            "password": "secret",
            "private_key": "",
            "private_key_passphrase": "",
            "become_method": "",
            "become_username": "",
            "become_password": "",
        },
        use_service_account=False,
    )

    assert captured["agent_ids"] == ["agent-1"]
    assert captured["required_ports"] == [22]
    assert runtime_files == []
    assert len(target_specifications) == 1
    assert target_specifications[0]["host_vars"]["ansible_host"] == "10.77.0.25"
