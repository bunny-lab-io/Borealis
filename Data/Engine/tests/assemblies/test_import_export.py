# ======================================================
# Data\Engine\tests\assemblies\test_import_export.py
# Description: Ensures assembly import/export endpoints round-trip legacy JSON documents.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64
import datetime as dt
import json

from flask.testing import FlaskClient

from Data.Engine.Unit_Tests.conftest import EngineTestHarness
from Data.Engine.assembly_management.models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadDescriptor
from Data.Engine.services.assemblies.serialization import prepare_import_request
from Data.Engine.services.assemblies.service import AssemblyRuntimeService


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
        "assembly_guid": "import-script-guid",
        "name": name,
        "description": "Import/export test script.",
        "type": "powershell",
        "script": encoded,
        "timeout_seconds": 45,
        "variables": [{"name": "example", "label": "Example", "type": "string", "default": ""}],
        "files": [],
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
    assert exported["script"] == document["script"]
    assert exported["name"] == document["name"]
    assert exported["variables"][0]["name"] == "example"
    assert "payload" not in exported
    assert "display_name" not in exported
    assert "summary" not in exported
    assert "script_encoding" not in exported
    assert "sites" not in exported
    assert "category" not in exported


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
    assert exported["nodes"][0]["id"] == "node-1"
    assert exported["tab_name"] == document["tab_name"]
    assert "payload" not in exported
    assert "display_name" not in exported


def test_import_without_guid_generates_and_persists_nested_guid(engine_harness: EngineTestHarness) -> None:
    client = _user_client(engine_harness)
    document = _script_document(name="Generated GUID Script")
    document.pop("assembly_guid", None)

    response = client.post(
        "/api/assemblies/import",
        json={"domain": "user", "document": document},
    )
    assert response.status_code == 201
    payload = response.get_json()
    assembly_guid = payload["assembly_guid"]
    assert assembly_guid
    assert payload["payload_json"]["assembly_guid"] == assembly_guid

    export_response = client.get(f"/api/assemblies/{assembly_guid}/export")
    assert export_response.status_code == 200
    exported = export_response.get_json()
    assert exported["assembly_guid"] == assembly_guid
    assert exported["assembly_guid"] == assembly_guid


def test_prepare_import_request_prefers_nested_payload_description() -> None:
    encoded = base64.b64encode(b'Write-Host "description precedence"').decode("ascii")
    guid, payload = prepare_import_request(
        {
            "assembly_guid": "summary-priority-guid",
            "display_name": "Summary Priority Script",
            "summary": "Stale top-level summary",
            "assembly_type": "script",
            "assembly_subtype": "powershell",
            "payload": {
                "assembly_guid": "summary-priority-guid",
                "name": "Summary Priority Script",
                "description": "Fresh nested description",
                "type": "powershell",
                "category": "script",
                "script": encoded,
                "script_encoding": "base64",
                "timeout_seconds": 30,
                "variables": [],
                "files": [],
            },
        },
        domain=AssemblyDomain.OFFICIAL,
    )

    assert guid == "summary-priority-guid"
    assert payload["summary"] == "Fresh nested description"


def test_runtime_serialization_prefers_nested_payload_description_for_summary() -> None:
    now = dt.datetime(2026, 3, 17, 0, 0, 0)
    payload_json = json.dumps(
        {
            "assembly_guid": "summary-priority-guid",
            "name": "Summary Priority Script",
            "description": "Fresh nested description",
            "type": "powershell",
            "category": "script",
            "script": base64.b64encode(b'Write-Host "description precedence"').decode("ascii"),
            "script_encoding": "base64",
        }
    )
    record = AssemblyRecord(
        assembly_guid="summary-priority-guid",
        display_name="Summary Priority Script",
        summary="Stale top-level summary",
        assembly_type="script",
        assembly_subtype="powershell",
        payload=PayloadDescriptor(
            assembly_guid="summary-priority-guid",
            file_name="payload.json",
            file_extension=".json",
            size_bytes=len(payload_json.encode("utf-8")),
            created_at=now,
            updated_at=now,
        ),
        payload_json=payload_json,
        created_at=now,
        updated_at=now,
    )
    runtime = AssemblyRuntimeService(object())

    serialized = runtime._serialize_entry(
        CachedAssembly(domain=AssemblyDomain.OFFICIAL, record=record),
        include_payload=False,
    )

    assert serialized["summary"] == "Fresh nested description"
