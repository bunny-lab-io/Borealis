# ======================================================
# Data\Engine\tests\assemblies\test_import_export.py
# Description: Ensures assembly import/export endpoints round-trip legacy JSON documents.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64

from flask.testing import FlaskClient

from Data.Engine.Unit_Tests.conftest import EngineTestHarness


def _user_client(harness: EngineTestHarness) -> FlaskClient:
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "importer"
        sess["role"] = "User"
    return client


def _script_document(name: str = "Import Script") -> dict:
    payload = 'Write-Host "round trip export"'
    encoded = base64.b64encode(payload.encode("utf-8")).decode("ascii")
    return {
        "version": 2,
        "name": name,
        "description": "Import/export test script.",
        "category": "script",
        "type": "powershell",
        "script": encoded,
        "timeout_seconds": 45,
        "sites": {"mode": "all", "values": []},
        "variables": [{"name": "example", "label": "Example", "type": "string", "default": ""}],
        "files": [],
        "script_encoding": "base64",
    }


def _workflow_document(name: str = "Import Workflow") -> dict:
    return {
        "tab_name": name,
        "description": "Import/export workflow test.",
        "nodes": [
            {
                "id": "node-1",
                "type": "DataNode",
                "position": {"x": 10, "y": 20},
                "data": {"label": "Input", "value": "example"},
            }
        ],
        "edges": [],
    }


def test_script_import_export_round_trip(engine_harness: EngineTestHarness) -> None:
    client = _user_client(engine_harness)
    document = _script_document()

    import_response = client.post(
        "/api/assemblies/import",
        json={"domain": "user", "document": document},
    )
    assert import_response.status_code == 201
    imported = import_response.get_json()
    assembly_guid = imported["assembly_guid"]
    assert imported["payload_json"]["script"] == document["script"]

    export_response = client.get(f"/api/assemblies/{assembly_guid}/export")
    assert export_response.status_code == 200
    exported = export_response.get_json()
    assert exported["assembly_guid"] == assembly_guid
    assert exported["payload"]["script"] == document["script"]
    assert exported["display_name"] == document["name"]
    assert exported["payload"]["variables"][0]["name"] == "example"
    assert isinstance(exported["queue"], list)


def test_workflow_import_export_round_trip(engine_harness: EngineTestHarness) -> None:
    client = _user_client(engine_harness)
    document = _workflow_document()

    response = client.post(
        "/api/assemblies/import",
        json={"domain": "user", "document": document},
    )
    assert response.status_code == 201
    payload = response.get_json()
    assembly_guid = payload["assembly_guid"]
    assert payload["payload_json"]["nodes"][0]["id"] == "node-1"

    export_response = client.get(f"/api/assemblies/{assembly_guid}/export")
    assert export_response.status_code == 200
    exported = export_response.get_json()
    assert exported["payload"]["nodes"][0]["id"] == "node-1"
    assert exported["display_name"] == document["tab_name"]
