from __future__ import annotations

import base64
import json
import logging
import queue
import socket
import threading
import time
from pathlib import Path

from Data.Engine.auth import device_purge_state, jwt_service
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token
from Data.Engine.services.job_scheduler.worker_socket import SiteWorkerSocketRuntime
from Data.Engine.services.remote_ops.sessions import issue_remote_op_session


AGENT_GUID = "11111111-2222-3333-4444-555555555555"
AGENT_HOSTNAME = "LAB-ONE"
AGENT_ID = f"{AGENT_HOSTNAME}_{AGENT_GUID}_SYSTEM"


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
    fingerprint = "fingerprint-1"
    conn = sqlite3.connect(str(db_path))
    try:
        _seed_device(conn, guid=AGENT_GUID, hostname=AGENT_HOSTNAME, site_id=site_id, fingerprint=fingerprint)
    finally:
        conn.close()
    service = jwt_service.load_service()
    token = service.issue_access_token(AGENT_GUID.upper(), fingerprint, 1)
    runtime = SiteWorkerSocketRuntime(
        worker_guid="worker-socket-test",
        site_id=site_id,
        host="127.0.0.1",
        port=59001,
        guacamole_host="127.0.0.1",
        guacamole_port=61001,
        internal_secret="unit-internal-secret",
        internal_api_base_url="http://127.0.0.1:5000",
        db_conn_factory=_db_factory(db_path),
        logger=logging.getLogger("test.site_worker_socket"),
        service_log=_service_log,
    )
    return runtime, token


def _issue_shell_token(
    runtime: SiteWorkerSocketRuntime,
    *,
    agent_id: str = AGENT_ID,
    site_id: int = 7,
    worker_guid: str = "worker-socket-test",
) -> str:
    issued = issue_remote_op_session(
        runtime.jwt_service,
        user={"username": "unit", "role": "Admin"},
        device={
            "guid": AGENT_GUID,
            "hostname": AGENT_HOSTNAME,
            "agent_id": agent_id,
            "site_id": site_id,
        },
        worker_route={"worker_guid": worker_guid, "generation": 1},
        capabilities=["remote_shell"],
        now=int(time.time()),
    )
    return issued["token"]


def _issue_remote_desktop_token(
    runtime: SiteWorkerSocketRuntime,
    *,
    agent_id: str = AGENT_ID,
    site_id: int = 7,
    worker_guid: str = "worker-socket-test",
) -> str:
    issued = issue_remote_op_session(
        runtime.jwt_service,
        user={"username": "unit", "role": "Admin"},
        device={
            "guid": AGENT_GUID,
            "hostname": AGENT_HOSTNAME,
            "agent_id": agent_id,
            "site_id": site_id,
        },
        worker_route={"worker_guid": worker_guid, "generation": 1},
        capabilities=["remote_desktop"],
        now=int(time.time()),
    )
    return issued["token"]


def _seed_vpn_lease(runtime: SiteWorkerSocketRuntime, *, agent_id: str = AGENT_ID, virtual_ip: str = "127.0.0.1") -> None:
    conn = runtime.db_conn_factory()
    try:
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS device_vpn_ip_leases (
                agent_id TEXT PRIMARY KEY,
                virtual_ip TEXT NOT NULL,
                updated_at INTEGER
            )
            """
        )
        conn.execute(
            """
            INSERT INTO device_vpn_ip_leases(agent_id, virtual_ip, updated_at)
            VALUES(?, ?, ?)
            ON CONFLICT(agent_id) DO UPDATE SET
                virtual_ip=excluded.virtual_ip,
                updated_at=excluded.updated_at
            """,
            (agent_id, virtual_ip, int(time.time())),
        )
        conn.commit()
    finally:
        conn.close()


class _FakeShellServer:
    def __init__(self) -> None:
        self.messages: "queue.Queue[dict]" = queue.Queue()
        self._stop = threading.Event()
        self._listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self._listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self._listener.bind(("127.0.0.1", 0))
        self._listener.listen(1)
        self.port = int(self._listener.getsockname()[1])
        self._conn: socket.socket | None = None
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def close(self) -> None:
        self._stop.set()
        for sock in (self._conn, self._listener):
            try:
                if sock is not None:
                    sock.close()
            except Exception:
                pass
        self._thread.join(timeout=2.0)

    def __enter__(self) -> "_FakeShellServer":
        return self

    def __exit__(self, _exc_type, _exc, _tb) -> None:
        self.close()

    def _serve(self) -> None:
        try:
            self._listener.settimeout(2.0)
            conn, _addr = self._listener.accept()
        except Exception:
            return
        self._conn = conn
        with conn:
            stream = conn.makefile("rwb", buffering=0)
            while not self._stop.is_set():
                try:
                    line = stream.readline()
                except Exception:
                    break
                if not line:
                    break
                try:
                    msg = json.loads(line.decode("utf-8"))
                except Exception:
                    continue
                self.messages.put(msg)
                if msg.get("type") == "ping":
                    response = {
                        "type": "pong",
                        "ping_id": msg.get("ping_id"),
                        "sent_at_ms": msg.get("sent_at_ms"),
                    }
                    try:
                        stream.write(json.dumps(response).encode("utf-8") + b"\n")
                    except Exception:
                        break

    def wait_for_message(self, message_type: str, *, timeout: float = 2.0) -> dict:
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                msg = self.messages.get(timeout=max(0.01, deadline - time.time()))
            except queue.Empty:
                break
            if msg.get("type") == message_type:
                return msg
        raise AssertionError(f"missing shell message type {message_type}")


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
            "agent_id": AGENT_ID,
            "hostname": AGENT_HOSTNAME,
            "service_mode": "system",
            "capabilities": {"helper_contexts": ["currentuser"]},
        },
        callback=True,
    )

    assert ack["status"] == "ok"
    assert runtime.has_host_service_socket("lab-one", "system")
    assert runtime.has_registered_agents()
    assert runtime.emit_host_service_event("LAB-ONE", "system", "agent_maintenance_request", {"operation_id": "op1"})
    received = client.get_received()
    assert any(item["name"] == "agent_maintenance_request" for item in received)

    client.disconnect()
    assert not runtime.has_host_service_socket("lab-one", "system")
    assert not runtime.has_registered_agents()


def test_site_worker_socket_counts_unique_registered_devices(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, _token = _runtime(tmp_path)

    runtime.registry.register(
        AGENT_ID,
        "sid-system",
        service_mode="system",
        hostname=AGENT_HOSTNAME,
        guid=AGENT_GUID,
    )
    runtime.registry.register(
        f"{AGENT_HOSTNAME}_{AGENT_GUID}_CURRENTUSER",
        "sid-currentuser",
        service_mode="currentuser",
        hostname=AGENT_HOSTNAME,
        guid=AGENT_GUID,
    )

    assert runtime.has_registered_agents()
    assert runtime.registered_device_count() == 1

    runtime.registry.register(
        "LAB-TWO_22222222-3333-4444-5555-666666666666_SYSTEM",
        "sid-lab-two",
        service_mode="system",
        hostname="LAB-TWO",
        guid="22222222-3333-4444-5555-666666666666",
    )

    assert runtime.registered_device_count() == 2


def test_site_worker_internal_host_service_call_bridge(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, _token = _runtime(tmp_path)
    calls: list[dict] = []

    def _call_host_service_event(hostname, service_mode, event_name, payload, *, timeout=30.0):
        calls.append(
            {
                "hostname": hostname,
                "service_mode": service_mode,
                "event_name": event_name,
                "payload": dict(payload or {}),
                "timeout": timeout,
            }
        )
        return {"status": "ok", "request_id": payload.get("request_id"), "controller_password": "unitpass"}

    monkeypatch.setattr(runtime, "call_host_service_event", _call_host_service_event)

    response = runtime.app.test_client().post(
        "/remote-ops/host-service/call",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={
            "hostname": "LAB-ONE",
            "service_mode": "system",
            "event_name": "vnc_credential_request",
            "payload": {"request_id": "req-1"},
            "timeout_seconds": 3,
        },
    )

    assert response.status_code == 200
    assert response.get_json() == {
        "called": True,
        "response": {"status": "ok", "request_id": "req-1", "controller_password": "unitpass"},
    }
    assert calls == [
        {
            "hostname": "LAB-ONE",
            "service_mode": "system",
            "event_name": "vnc_credential_request",
            "payload": {"request_id": "req-1"},
            "timeout": 3.0,
        }
    ]


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
            "agent_id": AGENT_ID,
            "hostname": AGENT_HOSTNAME,
            "service_mode": "system",
        },
        callback=True,
    )

    assert ack["error"] == "device_site_mismatch"
    assert not runtime.has_host_service_socket("lab-one", "system")
    client.disconnect()


def test_site_worker_shell_rejects_missing_remote_op_token(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)

    client = runtime.socketio.test_client(runtime.app)
    assert client.is_connected()

    ack = client.emit("vpn_shell_open", {"agent_id": AGENT_ID}, callback=True)

    assert ack["error"] == "missing_token"
    client.disconnect()


def test_site_worker_remote_desktop_registers_worker_guacamole_session(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    operation_token = _issue_remote_desktop_token(runtime)
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)

    client = runtime.app.test_client()
    response = client.post(
        "/remote-desktop/vnc/session",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={
            "operation_token": operation_token,
            "agent_id": AGENT_ID,
            "host": "10.255.0.20",
            "port": 5900,
            "password": "secretpw",
            "operator_id": "unit",
            "session_id": "vnc-session-1",
            "participant_id": "participant-1",
            "role": "controller",
            "width": 1920,
            "height": 1080,
            "performance_preference": 2,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["status"] == "ok"
    token = payload["token"]
    session = runtime._guacamole_registry.consume(token)
    assert session is not None
    assert session.agent_id == AGENT_ID
    assert session.host == "10.255.0.20"
    assert session.port == 5900
    assert session.password == "secretpw"
    assert session.session_id == "vnc-session-1"
    assert session.participant_id == "participant-1"
    assert session.performance_preference == 2


def test_site_worker_remote_desktop_rejects_wrong_worker_token(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    operation_token = _issue_remote_desktop_token(runtime, worker_guid="other-worker")
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)

    client = runtime.app.test_client()
    response = client.post(
        "/remote-desktop/vnc/session",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={
            "operation_token": operation_token,
            "agent_id": AGENT_ID,
            "host": "10.255.0.20",
            "port": 5900,
            "password": "secretpw",
            "session_id": "vnc-session-1",
            "participant_id": "participant-1",
        },
    )

    assert response.status_code == 403
    assert response.get_json()["error"] == "worker_mismatch"


def test_site_worker_shell_bridges_valid_session_to_agent_tcp(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    _seed_vpn_lease(runtime)
    operation_token = _issue_shell_token(runtime)

    client = runtime.socketio.test_client(runtime.app)
    assert client.is_connected()

    with _FakeShellServer() as server:
        ack = client.emit(
            "vpn_shell_open",
            {
                "agent_id": AGENT_ID,
                "operation_token": operation_token,
                "shell_port": server.port,
                "tunnel": {"agent_id": AGENT_ID, "virtual_ip": "127.0.0.1/32"},
            },
            callback=True,
        )

        assert ack["status"] == "ok"
        assert ack["session_id"]
        assert server.wait_for_message("ping")["ping_id"]

        send_ack = client.emit("vpn_shell_send", {"data": "whoami\n"}, callback=True)
        assert send_ack["status"] == "ok"
        stdin = server.wait_for_message("stdin")
        assert base64.b64decode(stdin["data"]).decode("utf-8") == "whoami\n"

        close_ack = client.emit("vpn_shell_close", {}, callback=True)
        assert close_ack["status"] == "ok"

    client.disconnect()
    assert runtime._shell_sessions == {}
    assert runtime._shell_sessions_by_agent == {}
