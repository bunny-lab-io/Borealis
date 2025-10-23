import pytest

pytest.importorskip("flask")

from .test_http_auth import _login


def test_assembly_crud_flow(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _login(client)

    resp = client.post(
        "/api/assembly/create",
        json={"island": "scripts", "kind": "folder", "path": "Utilities"},
    )
    assert resp.status_code == 200

    resp = client.post(
        "/api/assembly/create",
        json={
            "island": "scripts",
            "kind": "file",
            "path": "Utilities/sample",
            "content": {"name": "Sample", "script": "Write-Output 'Hello'", "type": "powershell"},
        },
    )
    assert resp.status_code == 200
    body = resp.get_json()
    rel_path = body.get("rel_path")
    assert rel_path and rel_path.endswith(".json")

    resp = client.get("/api/assembly/list?island=scripts")
    assert resp.status_code == 200
    listing = resp.get_json()
    assert any(item["rel_path"] == rel_path for item in listing.get("items", []))

    resp = client.get(f"/api/assembly/load?island=scripts&path={rel_path}")
    assert resp.status_code == 200
    loaded = resp.get_json()
    assert loaded.get("assembly", {}).get("name") == "Sample"

    resp = client.post(
        "/api/assembly/rename",
        json={
            "island": "scripts",
            "kind": "file",
            "path": rel_path,
            "new_name": "renamed",
        },
    )
    assert resp.status_code == 200
    renamed_rel = resp.get_json().get("rel_path")
    assert renamed_rel and renamed_rel.endswith(".json")

    resp = client.post(
        "/api/assembly/move",
        json={
            "island": "scripts",
            "path": renamed_rel,
            "new_path": "Utilities/Nested/renamed.json",
            "kind": "file",
        },
    )
    assert resp.status_code == 200

    resp = client.post(
        "/api/assembly/delete",
        json={
            "island": "scripts",
            "path": "Utilities/Nested/renamed.json",
            "kind": "file",
        },
    )
    assert resp.status_code == 200

    resp = client.get("/api/assembly/list?island=scripts")
    remaining = resp.get_json().get("items", [])
    assert all(item["rel_path"] != "Utilities/Nested/renamed.json" for item in remaining)


def test_server_time_endpoint(prepared_app):
    client = prepared_app.test_client()
    resp = client.get("/api/server/time")
    assert resp.status_code == 200
    body = resp.get_json()
    assert set(["epoch", "iso", "utc_iso", "timezone", "offset_seconds", "display"]).issubset(body)
