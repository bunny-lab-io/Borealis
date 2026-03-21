# ======================================================
# Data\Engine\tests\assemblies\test_execution_types.py
# Description: Verifies agent quick-run dispatch accepts supported non-PowerShell
#              script types and routes them through the Engine runtime.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64

import pytest
from flask.testing import FlaskClient

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.Unit_Tests.conftest import EngineTestHarness


def _admin_client(harness: EngineTestHarness) -> FlaskClient:
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def _script_document(*, name: str, script_type: str, script_body: str) -> dict:
    encoded = base64.b64encode(script_body.encode("utf-8")).decode("ascii")
    return {
        "version": 1,
        "name": name,
        "description": f"{script_type} quick-run test",
        "category": "script",
        "type": script_type,
        "script": encoded,
        "timeout_seconds": 60,
        "variables": [{"name": "greeting", "type": "string", "default": "hello"}],
        "files": [],
        "script_encoding": "base64",
    }


@pytest.mark.parametrize(
    ("script_type", "script_body"),
    [
        ("batch", "@echo off\r\necho %greeting%\r\n"),
        ("bash", 'echo "$greeting"\n'),
    ],
)
def test_quick_run_accepts_batch_and_bash(
    engine_harness: EngineTestHarness,
    monkeypatch,
    script_type: str,
    script_body: str,
) -> None:
    client = _admin_client(engine_harness)
    create_response = client.post(
        "/api/assemblies",
        json={
            "domain": "user",
            "assembly_type": "script",
            "assembly_subtype": script_type,
            "display_name": f"{script_type.title()} Quick Run",
            "summary": f"{script_type} quick run coverage",
            "payload": _script_document(
                name=f"{script_type.title()} Quick Run",
                script_type=script_type,
                script_body=script_body,
            ),
        },
    )
    assert create_response.status_code == 201
    assembly_guid = create_response.get_json()["assembly_guid"]

    targeted_events = []
    monkeypatch.setattr(
        engine_harness.context,
        "emit_host_service_event",
        lambda hostname, service_mode, event, payload: (
            targeted_events.append((hostname, service_mode, event, payload)) or True
        ),
    )
    socket_events = []
    monkeypatch.setattr(
        engine_harness.context.socketio,
        "emit",
        lambda event, payload, to=None: socket_events.append((event, payload, to)),
    )

    response = client.post(
        "/api/scripts/quick_run",
        json={
            "assembly_guid": assembly_guid,
            "hostnames": ["test-device"],
            "run_mode": "system",
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["results"][0]["status"] == "Running"
    assert len(targeted_events) == 1
    hostname, service_mode, event_name, dispatched = targeted_events[0]
    assert hostname == "test-device"
    assert service_mode == "system"
    assert event_name == "quick_job_run"
    assert dispatched["script_type"] == script_type
    decoded_content = base64.b64decode(dispatched["script_content"]).decode("utf-8")
    assert decoded_content == script_body.replace("\r\n", "\n")
    assert any(event_name == "device_activity_changed" for event_name, _payload, _to in socket_events)

    conn = sqlite3.connect(str(engine_harness.db_path))
    try:
        cur = conn.cursor()
        cur.execute("SELECT script_type, status FROM activity_history ORDER BY id DESC LIMIT 1")
        row = cur.fetchone()
        assert row[0] == script_type
        assert row[1] == "Running"
    finally:
        conn.close()
