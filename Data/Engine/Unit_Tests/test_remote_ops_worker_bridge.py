from __future__ import annotations

import base64
from typing import Any

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.job_scheduler.queue import WORKER_STATUS_RUNNING, register_worker
from Data.Engine.services.job_scheduler import worker as site_worker
from Data.Engine.services.ansible import worker_dispatch
from Data.Engine.services.remote_ops import worker_bridge

from .conftest import EngineTestHarness
from .support.engine import admin_client, db_connection


class _FakeSocketRuntime:
    def __init__(
        self,
        *,
        emit_result: bool = False,
        registered: bool = False,
        call_response: Any = None,
    ) -> None:
        self.emit_result = emit_result
        self.registered = registered
        self.call_response = call_response
        self.emitted: list[dict[str, Any]] = []
        self.queued: list[dict[str, Any]] = []
        self.called: list[dict[str, Any]] = []

    def emit_host_service_event(self, hostname: str, service_mode: str, event_name: str, payload: Any) -> bool:
        self.emitted.append(
            {
                "hostname": hostname,
                "service_mode": service_mode,
                "event_name": event_name,
                "payload": payload,
            }
        )
        return self.emit_result

    def has_host_service_socket(self, hostname: str, service_mode: str) -> bool:
        return self.registered

    def call_host_service_event(
        self,
        hostname: str,
        service_mode: str,
        event_name: str,
        payload: Any,
        *,
        timeout: float = 30.0,
    ) -> Any:
        self.called.append(
            {
                "hostname": hostname,
                "service_mode": service_mode,
                "event_name": event_name,
                "payload": payload,
                "timeout": timeout,
            }
        )
        return self.call_response

    def queue_host_service_event(
        self,
        hostname: str,
        service_mode: str,
        event_name: str,
        payload: Any,
        *,
        ttl_seconds: Any = None,
    ) -> bool:
        self.queued.append(
            {
                "hostname": hostname,
                "service_mode": service_mode,
                "event_name": event_name,
                "payload": payload,
                "ttl_seconds": ttl_seconds,
            }
        )
        return True


def _seed_worker_route(
    harness: EngineTestHarness,
    monkeypatch,
    tmp_path,
    *,
    worker_guid: str = "worker-m11-bridge",
    port: int = 61234,
) -> None:
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(tmp_path / "routes"))
    with db_connection(harness) as conn:
        register_worker(
            conn,
            worker_guid=worker_guid,
            container_name=f"site-worker-{worker_guid}",
            site_id=1,
            status=WORKER_STATUS_RUNNING,
            upstream_host="127.0.0.1",
            upstream_port=port,
        )
        conn.commit()


def _script_document() -> dict[str, Any]:
    return {
        "name": "M11 Quick Run",
        "type": "powershell",
        "script": "Write-Output 'm11 quick job'\n",
        "variables": [],
        "timeout_seconds": 30,
        "files": [],
        "script_encoding": "base64",
    }


def test_context_host_service_bridge_targets_active_site_worker(
    engine_harness: EngineTestHarness,
    monkeypatch,
    tmp_path,
) -> None:
    _seed_worker_route(engine_harness, monkeypatch, tmp_path, port=61235)
    calls: list[dict[str, Any]] = []

    def _post_worker_json(_app, route, path, payload, *, timeout=30.0):
        calls.append(
            {
                "route": dict(route or {}),
                "path": path,
                "payload": dict(payload or {}),
                "timeout": timeout,
            }
        )
        if path == "/remote-ops/host-service/status":
            return {"registered": True}, None
        if path == "/remote-ops/host-service/call":
            return {"called": True, "response": {"ok": True, "request_id": payload["payload"]["request_id"]}}, None
        return {"emitted": True}, None

    monkeypatch.setattr(worker_bridge, "post_worker_json", _post_worker_json)

    assert getattr(engine_harness.context, "site_worker_host_service_bridge_enabled", False) is True
    assert engine_harness.context.has_host_service_socket("test-device", "system") is True
    assert engine_harness.context.call_host_service_event(
        "test-device",
        "system",
        "unit_call",
        {"request_id": "req-1"},
        timeout=4,
    ) == {"ok": True, "request_id": "req-1"}
    assert engine_harness.context.emit_host_service_event(
        "test-device",
        "system",
        "quick_job_run",
        {"job_id": 77},
    ) is True

    assert [call["path"] for call in calls] == [
        "/remote-ops/host-service/status",
        "/remote-ops/host-service/call",
        "/remote-ops/host-service/event",
    ]
    assert all(call["route"]["worker_guid"] == "worker-m11-bridge" for call in calls)
    assert all(call["route"]["upstream_port"] == 61235 for call in calls)
    assert calls[-1]["payload"]["hostname"] == "test-device"
    assert calls[-1]["payload"]["service_mode"] == "system"
    assert calls[-1]["payload"]["event_name"] == "quick_job_run"
    assert calls[-1]["payload"]["payload"]["job_id"] == 77
    assert calls[-1]["payload"]["allow_pending"] is True
    assert calls[-1]["payload"]["pending_ttl_seconds"] == 180


def test_realtime_bootstrap_preserves_site_worker_host_service_bridge(
    engine_harness: EngineTestHarness,
) -> None:
    assert callable(getattr(engine_harness.context, "legacy_emit_host_service_event", None))
    assert engine_harness.context.emit_host_service_event is not engine_harness.context.legacy_emit_host_service_event
    assert getattr(engine_harness.context, "site_worker_host_service_bridge_enabled", False) is True


def test_quick_run_dispatches_through_site_worker_bridge(
    engine_harness: EngineTestHarness,
    monkeypatch,
    tmp_path,
) -> None:
    _seed_worker_route(engine_harness, monkeypatch, tmp_path, port=61236)
    worker_calls: list[dict[str, Any]] = []

    def _post_worker_json(_app, route, path, payload, *, timeout=30.0):
        worker_calls.append(
            {
                "route": dict(route or {}),
                "path": path,
                "payload": dict(payload or {}),
                "timeout": timeout,
            }
        )
        return {"emitted": True}, None

    monkeypatch.setattr(worker_bridge, "post_worker_json", _post_worker_json)
    socket_events: list[tuple[str, Any, Any]] = []
    monkeypatch.setattr(
        engine_harness.context.socketio,
        "emit",
        lambda event, payload, to=None: socket_events.append((event, payload, to)),
    )

    client = admin_client(engine_harness)
    create_response = client.post(
        "/api/assemblies",
        json={
            "domain": "user",
            "assembly_type": "script",
            "assembly_subtype": "powershell",
            "display_name": "M11 Quick Run",
            "summary": "M11 quick job bridge coverage",
            "payload": _script_document(),
        },
    )
    assert create_response.status_code == 201

    response = client.post(
        "/api/scripts/quick_run",
        json={
            "assembly_guid": create_response.get_json()["assembly_guid"],
            "hostnames": ["test-device"],
            "run_mode": "system",
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["results"][0]["status"] == "Running"
    assert len(worker_calls) == 1
    call = worker_calls[0]
    assert call["path"] == "/remote-ops/host-service/event"
    assert call["route"]["worker_guid"] == "worker-m11-bridge"
    assert call["payload"]["event_name"] == "quick_job_run"
    assert call["payload"]["allow_pending"] is True
    assert call["payload"]["pending_ttl_seconds"] == 180
    dispatched = call["payload"]["payload"]
    assert dispatched["target_hostname"] == "test-device"
    assert dispatched["script_type"] == "powershell"
    assert dispatched["target_context"] == "system"
    assert base64.b64decode(dispatched["script_content"]).decode("utf-8") == "Write-Output 'm11 quick job'\n"
    assert not any(event == "quick_job_run" for event, _payload, _to in socket_events)
    assert any(event == "device_activity_changed" for event, _payload, _to in socket_events)


def test_ansible_dispatcher_queues_through_active_site_worker(
    engine_harness: EngineTestHarness,
    monkeypatch,
    tmp_path,
) -> None:
    _seed_worker_route(engine_harness, monkeypatch, tmp_path, worker_guid="worker-m12-ansible", port=61237)
    calls: list[dict[str, Any]] = []

    def _post_worker_json(_app, route, path, payload, *, timeout=30.0):
        calls.append(
            {
                "route": dict(route or {}),
                "path": path,
                "payload": dict(payload or {}),
                "timeout": timeout,
            }
        )
        return {"queued": True, "run_id": "ansible-run-1"}, None

    monkeypatch.setattr(worker_dispatch, "post_worker_json", _post_worker_json)

    dispatcher = worker_dispatch.WorkerAnsibleDispatcher(
        app=engine_harness.app,
        adapters=type("Adapters", (), {"db_conn_factory": lambda _self: sqlite3.connect(str(engine_harness.db_path))})(),
    )

    run_id = dispatcher.queue_run(
        hostname="borealis-engine-01",
        playbook_rel_path="Ansible_Playbooks/ping.yml",
        playbook_name="Ping",
        playbook_content="- hosts: all\n",
        target_specifications=[
            {
                "hostname": "test-device",
                "inventory_hostname": "main_lab__test_device",
                "site_id": 1,
                "host_vars": {"ansible_connection": "ssh"},
            }
        ],
        source="scheduled_job",
        connection="ssh",
    )

    assert run_id == "ansible-run-1"
    assert len(calls) == 1
    assert calls[0]["path"] == "/automation/ansible/run"
    assert calls[0]["route"]["worker_guid"] == "worker-m12-ansible"
    assert calls[0]["payload"]["queue_run"]["playbook_name"] == "Ping"
    assert calls[0]["payload"]["queue_run"]["target_specifications"][0]["site_id"] == 1


def test_site_worker_local_dispatch_queues_quick_job_when_socket_reconnecting(monkeypatch) -> None:
    monkeypatch.delenv("BOREALIS_SITE_WORKER_PENDING_EVENT_TTL_SECONDS", raising=False)
    runtime = _FakeSocketRuntime(emit_result=False)

    delivered, delivery_state = site_worker._emit_or_queue_socket_runtime_event(
        runtime,
        "LAB-OPERATOR-01",
        "system",
        "quick_job_run",
        {"job_id": 106},
    )

    assert delivered is True
    assert delivery_state == "queued"
    assert runtime.emitted[0]["hostname"] == "LAB-OPERATOR-01"
    assert runtime.queued[0]["event_name"] == "quick_job_run"
    assert runtime.queued[0]["ttl_seconds"] == 180


def test_site_worker_agent_maintenance_calls_registered_agent_socket() -> None:
    runtime = _FakeSocketRuntime(
        registered=True,
        call_response={"status": "ok", "operation_id": "op-maintenance-1"},
    )

    delivered, delivery_state, response = site_worker._call_or_queue_socket_runtime_event(
        runtime,
        "LAB-OPERATOR-01",
        "system",
        "agent_maintenance_request",
        {"operation_id": "op-maintenance-1", "release_channel": "unstable"},
    )

    assert delivered is True
    assert delivery_state == "called"
    assert response == {"status": "ok", "operation_id": "op-maintenance-1"}
    assert runtime.called[0]["event_name"] == "agent_maintenance_request"
    assert runtime.emitted == []
    assert runtime.queued == []


def test_site_worker_agent_maintenance_surfaces_agent_error() -> None:
    runtime = _FakeSocketRuntime(
        registered=True,
        call_response={"status": "error", "detail": "release_channel missing"},
    )

    delivered, delivery_state, response = site_worker._call_or_queue_socket_runtime_event(
        runtime,
        "LAB-OPERATOR-01",
        "system",
        "agent_maintenance_request",
        {"operation_id": "op-maintenance-2"},
    )

    assert delivered is False
    assert delivery_state == "agent_error"
    assert response == {"status": "error", "detail": "release_channel missing"}
    assert runtime.called
    assert runtime.emitted == []
    assert runtime.queued == []


def test_site_worker_agent_maintenance_queues_when_socket_reconnecting(monkeypatch) -> None:
    monkeypatch.delenv("BOREALIS_SITE_WORKER_PENDING_EVENT_TTL_SECONDS", raising=False)
    runtime = _FakeSocketRuntime(emit_result=False, registered=False)

    delivered, delivery_state, response = site_worker._call_or_queue_socket_runtime_event(
        runtime,
        "LAB-OPERATOR-01",
        "system",
        "agent_maintenance_request",
        {"operation_id": "op-maintenance-3"},
    )

    assert delivered is True
    assert delivery_state == "queued"
    assert response is None
    assert runtime.called == []
    assert runtime.emitted[0]["event_name"] == "agent_maintenance_request"
    assert runtime.queued[0]["event_name"] == "agent_maintenance_request"
    assert runtime.queued[0]["ttl_seconds"] == 180


def test_site_worker_agent_maintenance_queues_after_registered_socket_timeout(monkeypatch) -> None:
    monkeypatch.delenv("BOREALIS_SITE_WORKER_PENDING_EVENT_TTL_SECONDS", raising=False)
    runtime = _FakeSocketRuntime(
        emit_result=False,
        registered=True,
        call_response=None,
    )

    delivered, delivery_state, response = site_worker._call_or_queue_socket_runtime_event(
        runtime,
        "LAB-OPERATOR-01",
        "system",
        "agent_maintenance_request",
        {"operation_id": "op-maintenance-stale"},
    )

    assert delivered is True
    assert delivery_state == "queued_after_no_response"
    assert response is None
    assert runtime.called[0]["event_name"] == "agent_maintenance_request"
    assert runtime.emitted == []
    assert runtime.queued[0]["event_name"] == "agent_maintenance_request"
    assert runtime.queued[0]["payload"]["operation_id"] == "op-maintenance-stale"
    assert runtime.queued[0]["ttl_seconds"] == 180


def test_site_worker_local_dispatch_does_not_queue_unlisted_events() -> None:
    runtime = _FakeSocketRuntime(emit_result=False)

    delivered, delivery_state = site_worker._emit_or_queue_socket_runtime_event(
        runtime,
        "LAB-OPERATOR-01",
        "system",
        "process_control_request",
        {"request_id": "req-1"},
    )

    assert delivered is False
    assert delivery_state == "missing_socket"
    assert runtime.emitted
    assert runtime.queued == []
