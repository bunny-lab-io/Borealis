from __future__ import annotations

import base64
from typing import Any

from Data.Engine.services.job_scheduler.queue import WORKER_STATUS_RUNNING, register_worker
from Data.Engine.services.remote_ops import worker_bridge

from .conftest import EngineTestHarness
from .support.engine import admin_client, db_connection


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
    dispatched = call["payload"]["payload"]
    assert dispatched["target_hostname"] == "test-device"
    assert dispatched["script_type"] == "powershell"
    assert dispatched["target_context"] == "system"
    assert base64.b64decode(dispatched["script_content"]).decode("utf-8") == "Write-Output 'm11 quick job'\n"
    assert not any(event == "quick_job_run" for event, _payload, _to in socket_events)
    assert any(event == "device_activity_changed" for event, _payload, _to in socket_events)
