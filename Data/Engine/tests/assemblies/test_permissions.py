# ======================================================
# Data\Engine\tests\assemblies\test_permissions.py
# Description: Verifies Assembly API domain guards and Dev Mode permissions.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64

from flask.testing import FlaskClient

from Data.Engine.assembly_management.models import AssemblyDomain

from Data.Engine.Unit_Tests.conftest import EngineTestHarness


def _script_document(name: str = "Permission Script") -> dict:
    script = 'Write-Host "permissions"'
    encoded = base64.b64encode(script.encode("utf-8")).decode("ascii")
    return {
        "version": 1,
        "name": name,
        "description": "Permission test script.",
        "category": "script",
        "type": "powershell",
        "script": encoded,
        "timeout_seconds": 60,
        "sites": {"mode": "all", "values": []},
        "variables": [],
        "files": [],
        "script_encoding": "base64",
    }


def _user_client(harness: EngineTestHarness) -> FlaskClient:
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "operator"
        sess["role"] = "User"
    return client


def _admin_client(harness: EngineTestHarness) -> FlaskClient:
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client


def test_non_admin_cannot_write_official_domain(engine_harness: EngineTestHarness) -> None:
    client = _user_client(engine_harness)
    response = client.post(
        "/api/assemblies",
        json={
            "domain": AssemblyDomain.OFFICIAL.value,
            "assembly_kind": "script",
            "display_name": "User Attempt",
            "summary": "Should fail",
            "category": "script",
            "assembly_type": "powershell",
            "version": 1,
            "metadata": {},
            "payload": _script_document("User Attempt"),
        },
    )
    assert response.status_code == 403
    payload = response.get_json()
    assert payload["error"] == "forbidden"


def test_admin_requires_dev_mode_for_official_mutation(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    response = client.post(
        "/api/assemblies",
        json={
            "domain": AssemblyDomain.OFFICIAL.value,
            "assembly_kind": "script",
            "display_name": "Dev Mode Required",
            "summary": "Should request dev mode",
            "category": "script",
            "assembly_type": "powershell",
            "version": 1,
            "metadata": {},
            "payload": _script_document("Dev Mode Required"),
        },
    )
    assert response.status_code == 403
    payload = response.get_json()
    assert payload["error"] == "dev_mode_required"


def test_admin_with_dev_mode_can_mutate_official(engine_harness: EngineTestHarness) -> None:
    client = _admin_client(engine_harness)
    response = client.post("/api/assemblies/dev-mode/switch", json={"enabled": True})
    assert response.status_code == 200
    assert response.get_json()["dev_mode"] is True

    create_response = client.post(
        "/api/assemblies",
        json={
            "domain": AssemblyDomain.OFFICIAL.value,
            "assembly_kind": "script",
            "display_name": "Official Dev Mode Script",
            "summary": "Created while Dev Mode enabled",
            "category": "script",
            "assembly_type": "powershell",
            "version": 1,
            "metadata": {},
            "payload": _script_document("Official Dev Mode Script"),
        },
    )
    assert create_response.status_code == 201
    record = create_response.get_json()
    assert record["source"] == AssemblyDomain.OFFICIAL.value
    assert record["is_dirty"] is True

    flush_response = client.post("/api/assemblies/dev-mode/write")
    assert flush_response.status_code == 200
    assert flush_response.get_json()["status"] == "flushed"
