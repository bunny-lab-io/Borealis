from __future__ import annotations

import logging
import time
from pathlib import Path

from Data.Engine.auth import device_purge_state, jwt_service
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.job_scheduler.worker_socket import SiteWorkerSocketRuntime


def _service_log(_service: str, _message: str, scope=None, *, level: str = "INFO") -> None:
    return None


def _db_factory(db_path: Path):
    def _factory():
        return sqlite3.connect(str(db_path))

    return _factory


def _seed_device(conn, *, guid: str, hostname: str, site_id: int, fingerprint: str = "fingerprint-1") -> None:
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS devices (
            guid TEXT PRIMARY KEY,
            hostname TEXT,
            ssl_key_fingerprint TEXT,
            token_version INTEGER,
            status TEXT
        )
        """
    )
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS device_sites (
            device_hostname TEXT PRIMARY KEY,
            site_id INTEGER NOT NULL,
            assigned_at INTEGER
        )
        """
    )
    device_purge_state.ensure_table(conn)
    conn.execute(
        """
        INSERT INTO devices(guid, hostname, ssl_key_fingerprint, token_version, status)
        VALUES(?, ?, ?, 1, 'active')
        """,
        (guid, hostname, fingerprint),
    )
    conn.execute(
        """
        INSERT INTO device_sites(device_hostname, site_id, assigned_at)
        VALUES(?, ?, ?)
        """,
        (hostname, int(site_id), int(time.time())),
    )
    conn.commit()


def _runtime(tmp_path: Path, *, site_id: int = 7) -> tuple[SiteWorkerSocketRuntime, str]:
    db_path = tmp_path / "worker-socket.sqlite3"
    guid = "11111111-2222-3333-4444-555555555555"
    hostname = "LAB-ONE"
    fingerprint = "fingerprint-1"
    conn = sqlite3.connect(str(db_path))
    try:
        _seed_device(conn, guid=guid, hostname=hostname, site_id=site_id, fingerprint=fingerprint)
    finally:
        conn.close()
    service = jwt_service.load_service()
    token = service.issue_access_token(guid.upper(), fingerprint, 1)
    runtime = SiteWorkerSocketRuntime(
        worker_guid="worker-socket-test",
        site_id=site_id,
        host="127.0.0.1",
        port=59001,
        db_conn_factory=_db_factory(db_path),
        logger=logging.getLogger("test.site_worker_socket"),
        service_log=_service_log,
    )
    return runtime, token


def test_site_worker_socket_registers_agent_and_dispatches_event(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)

    client = runtime.socketio.test_client(
        runtime.app,
        headers={"Authorization": f"Bearer {token}"},
    )
    assert client.is_connected()

    ack = client.emit(
        "connect_agent",
        {
            "agent_id": "LAB-ONE_11111111-2222-3333-4444-555555555555_SYSTEM",
            "hostname": "LAB-ONE",
            "service_mode": "system",
            "capabilities": {"helper_contexts": ["currentuser"]},
        },
        callback=True,
    )

    assert ack["status"] == "ok"
    assert runtime.has_host_service_socket("lab-one", "system")
    assert runtime.emit_host_service_event("LAB-ONE", "system", "agent_maintenance_request", {"operation_id": "op1"})
    received = client.get_received()
    assert any(item["name"] == "agent_maintenance_request" for item in received)

    client.disconnect()
    assert not runtime.has_host_service_socket("lab-one", "system")


def test_site_worker_socket_rejects_cross_site_agent(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path, site_id=7)
    runtime.site_id = 8

    client = runtime.socketio.test_client(
        runtime.app,
        headers={"Authorization": f"Bearer {token}"},
    )

    ack = client.emit(
        "connect_agent",
        {
            "agent_id": "LAB-ONE_11111111-2222-3333-4444-555555555555_SYSTEM",
            "hostname": "LAB-ONE",
            "service_mode": "system",
        },
        callback=True,
    )

    assert ack["error"] == "device_site_mismatch"
    assert not runtime.has_host_service_socket("lab-one", "system")
    client.disconnect()
