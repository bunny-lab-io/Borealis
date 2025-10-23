import json
from datetime import datetime, timezone
import sqlite3
import time

import pytest

pytest.importorskip("flask")

from .test_http_auth import _login


def _ensure_admin_session(client):
    _login(client)


def test_sites_crud_flow(prepared_app):
    client = prepared_app.test_client()
    _ensure_admin_session(client)

    resp = client.get("/api/sites")
    assert resp.status_code == 200
    assert resp.get_json() == {"sites": []}

    create = client.post("/api/sites", json={"name": "HQ", "description": "Primary"})
    assert create.status_code == 201
    created = create.get_json()
    assert created["name"] == "HQ"

    listing = client.get("/api/sites")
    sites = listing.get_json()["sites"]
    assert len(sites) == 1

    resp = client.post("/api/sites/assign", json={"site_id": created["id"], "hostnames": ["device-1"]})
    assert resp.status_code == 200

    mapping = client.get("/api/sites/device_map?hostnames=device-1")
    data = mapping.get_json()["mapping"]
    assert data["device-1"]["site_id"] == created["id"]

    rename = client.post("/api/sites/rename", json={"id": created["id"], "new_name": "Main"})
    assert rename.status_code == 200
    assert rename.get_json()["name"] == "Main"

    delete = client.post("/api/sites/delete", json={"ids": [created["id"]]})
    assert delete.status_code == 200
    assert delete.get_json()["deleted"] == 1


def test_devices_listing(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _ensure_admin_session(client)

    now = datetime.now(tz=timezone.utc)
    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()
    cur.execute(
        """
        INSERT INTO devices (
            guid,
            hostname,
            description,
            created_at,
            agent_hash,
            last_seen,
            connection_type,
            connection_endpoint
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            "11111111-1111-1111-1111-111111111111",
            "test-device",
            "Test Device",
            int(now.timestamp()),
            "hashvalue",
            int(now.timestamp()),
            "",
            "",
        ),
    )
    conn.commit()
    conn.close()

    resp = client.get("/api/devices")
    assert resp.status_code == 200
    devices = resp.get_json()["devices"]
    assert any(device["hostname"] == "test-device" for device in devices)


def test_agent_hash_list_requires_local_request(prepared_app):
    client = prepared_app.test_client()
    _ensure_admin_session(client)

    resp = client.get("/api/agent/hash_list", environ_overrides={"REMOTE_ADDR": "203.0.113.5"})
    assert resp.status_code == 403

    resp = client.get("/api/agent/hash_list", environ_overrides={"REMOTE_ADDR": "127.0.0.1"})
    assert resp.status_code == 200
    assert resp.get_json() == {"agents": []}


def test_credentials_list_requires_admin(prepared_app):
    client = prepared_app.test_client()
    resp = client.get("/api/credentials")
    assert resp.status_code == 401

    _ensure_admin_session(client)
    resp = client.get("/api/credentials")
    assert resp.status_code == 200
    assert resp.get_json() == {"credentials": []}


def test_device_description_update(prepared_app, engine_settings):
    client = prepared_app.test_client()
    hostname = "device-desc"
    guid = "A3D3F1E5-9B8C-4C6F-80F1-4D5E6F7A8B9C"

    now = int(time.time())
    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()
    cur.execute(
        """
        INSERT INTO devices (
            guid,
            hostname,
            description,
            created_at,
            last_seen
        ) VALUES (?, ?, '', ?, ?)
        """,
        (guid, hostname, now, now),
    )
    conn.commit()
    conn.close()

    resp = client.post(
        f"/api/device/description/{hostname}",
        json={"description": "Primary workstation"},
    )

    assert resp.status_code == 200
    assert resp.get_json() == {"status": "ok"}

    conn = sqlite3.connect(engine_settings.database.path)
    row = conn.execute(
        "SELECT description FROM devices WHERE hostname = ?",
        (hostname,),
    ).fetchone()
    conn.close()

    assert row is not None
    assert row[0] == "Primary workstation"


def test_device_details_returns_inventory(prepared_app, engine_settings):
    client = prepared_app.test_client()
    _ensure_admin_session(client)

    hostname = "inventory-1"
    guid = "B9F0A1C2-D3E4-5F67-890A-BCDEF1234567"
    now = int(time.time())

    memory = [{"slot": "DIMM1", "size_gb": 16}]
    network = [{"name": "Ethernet", "mac": "AA:BB:CC:DD:EE:FF"}]
    software = [{"name": "Agent", "version": "1.0.0"}]
    storage = [{"model": "Disk", "size_gb": 512}]
    cpu = {"model": "Intel", "cores": 8}

    conn = sqlite3.connect(engine_settings.database.path)
    cur = conn.cursor()
    cur.execute(
        """
        INSERT INTO devices (
            guid,
            hostname,
            description,
            created_at,
            agent_hash,
            memory,
            network,
            software,
            storage,
            cpu,
            device_type,
            domain,
            external_ip,
            internal_ip,
            last_reboot,
            last_seen,
            last_user,
            operating_system,
            uptime,
            agent_id
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            guid,
            hostname,
            "Workstation",
            now,
            "hashvalue",
            json.dumps(memory),
            json.dumps(network),
            json.dumps(software),
            json.dumps(storage),
            json.dumps(cpu),
            "Laptop",
            "ACME",
            "203.0.113.10",
            "192.0.2.10",
            "2024-01-01 12:00:00",
            now,
            "ACME\\tech",
            "Windows 11",
            7200,
            "agent-001",
        ),
    )
    conn.commit()
    conn.close()

    resp = client.get(f"/api/device/details/{hostname}")
    assert resp.status_code == 200
    data = resp.get_json()

    assert data["memory"] == memory
    assert data["network"] == network
    assert data["software"] == software
    assert data["storage"] == storage
    assert data["cpu"] == cpu
    assert data["description"] == "Workstation"
    assert data["agent_hash"] == "hashvalue"
    assert data["agent_guid"].lower() == guid.lower()
    assert data["last_user"] == "ACME\\tech"
    assert data["operating_system"] == "Windows 11"
    assert data["uptime"] == 7200
    assert data["summary"]["hostname"] == hostname
    assert data["details"]["memory"] == memory
