from __future__ import annotations

import base64
import io
import json
import logging
import queue
import socket
import sys
import threading
import time
import types
from pathlib import Path

from Data.Engine.auth import device_purge_state, jwt_service
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.RemoteDesktop import rfb_probe
from Data.Engine.services.RemoteDesktop.rfb_probe import VncAuthProbeResult
from Data.Engine.services.job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token
from Data.Engine.services.job_scheduler import worker_socket
from Data.Engine.services.job_scheduler.worker_socket import SiteWorkerSocketRuntime
from Data.Engine.services.remote_ops.sessions import issue_remote_op_session


AGENT_GUID = "11111111-2222-3333-4444-555555555555"
AGENT_HOSTNAME = "LAB-ONE"
AGENT_ID = f"{AGENT_HOSTNAME}_{AGENT_GUID}_SYSTEM"


def _service_log(_service: str, _message: str, scope=None, *, level: str = "INFO") -> None:
    return None


def test_site_worker_socketio_async_mode_defaults_to_eventlet(monkeypatch) -> None:
    calls = []

    monkeypatch.delenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", raising=False)
    monkeypatch.delenv("BOREALIS_SITE_WORKER_EVENTLET_PREPATCHED", raising=False)
    monkeypatch.setattr(worker_socket.importlib.util, "find_spec", lambda _name: object())
    monkeypatch.setattr(worker_socket.importlib, "import_module", lambda _name: object())
    monkeypatch.setitem(
        sys.modules,
        "eventlet",
        types.SimpleNamespace(monkey_patch=lambda **kwargs: calls.append(kwargs)),
    )

    assert worker_socket._resolve_socketio_async_mode() == "eventlet"
    assert calls == [{"thread": False}]


def test_site_worker_socketio_async_mode_reuses_prepatched_eventlet(monkeypatch) -> None:
    monkeypatch.delenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", raising=False)
    monkeypatch.setenv("BOREALIS_SITE_WORKER_EVENTLET_PREPATCHED", "1")
    monkeypatch.setattr(
        worker_socket.importlib.util,
        "find_spec",
        lambda _name: (_ for _ in ()).throw(AssertionError("eventlet discovery should not run")),
    )

    assert worker_socket._resolve_socketio_async_mode() == "eventlet"


def test_site_worker_socketio_async_mode_falls_back_to_threading_without_eventlet(monkeypatch) -> None:
    monkeypatch.delenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", raising=False)
    monkeypatch.delenv("BOREALIS_SITE_WORKER_EVENTLET_PREPATCHED", raising=False)
    monkeypatch.setattr(worker_socket.importlib.util, "find_spec", lambda _name: None)

    assert worker_socket._resolve_socketio_async_mode() == "threading"


def _db_factory(db_path: Path):
    def _factory():
        return sqlite3.connect(str(db_path))

    return _factory


def _seed_device(conn, *, guid: str, hostname: str, site_id: int, fingerprint: str = "fingerprint-1", status: str = "active") -> None:
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
        VALUES(?, ?, ?, 1, ?)
        """,
        (guid, hostname, fingerprint, status),
    )
    conn.execute(
        """
        INSERT INTO device_sites(device_hostname, site_id, assigned_at)
        VALUES(?, ?, ?)
        """,
        (hostname, int(site_id), int(time.time())),
    )
    conn.commit()


def _runtime(tmp_path: Path, *, site_id: int = 7, device_status: str = "active") -> tuple[SiteWorkerSocketRuntime, str]:
    db_path = tmp_path / "worker-socket.sqlite3"
    fingerprint = "fingerprint-1"
    conn = sqlite3.connect(str(db_path))
    try:
        _seed_device(conn, guid=AGENT_GUID, hostname=AGENT_HOSTNAME, site_id=site_id, fingerprint=fingerprint, status=device_status)
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


def test_site_worker_internal_ansible_route_queues_runner(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, _token = _runtime(tmp_path)
    calls: list[dict] = []

    class _Runner:
        def queue_run(self, **kwargs):
            calls.append(dict(kwargs))
            return "runner-queued-1"

    runtime.set_ansible_runner(_Runner())
    client = runtime.app.test_client()
    response = client.post(
        "/automation/ansible/run",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={
            "queue_run": {
                "hostname": "borealis-engine-01",
                "playbook_rel_path": "Ansible_Playbooks/ping.yml",
                "playbook_name": "Ping",
                "playbook_content": "- hosts: all\n",
                "target_specifications": [{"hostname": "borealis-engine-01", "site_id": 7}],
                "source": "workflow_run",
                "connection": "local",
            }
        },
    )

    assert response.status_code == 200
    assert response.get_json()["run_id"] == "runner-queued-1"
    assert calls[0]["playbook_name"] == "Ping"
    assert calls[0]["target_specifications"][0]["site_id"] == 7


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


def test_site_worker_queues_pending_host_service_event_until_agent_registers(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)

    response = runtime.app.test_client().post(
        "/remote-ops/host-service/event",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={
            "hostname": AGENT_HOSTNAME,
            "service_mode": "system",
            "event_name": "software_inventory_refresh_request",
            "payload": {"reason": "unit"},
            "allow_pending": True,
            "pending_ttl_seconds": 180,
        },
    )

    assert response.status_code == 200
    assert response.get_json() == {"emitted": False, "queued": True}

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
        },
        callback=True,
    )

    assert ack["status"] == "ok"
    received = client.get_received()
    assert any(
        item["name"] == "software_inventory_refresh_request"
        and item["args"]
        and item["args"][0].get("reason") == "unit"
        for item in received
    )
    client.disconnect()


def test_site_worker_file_upload_route_stores_transfer_and_emits_worker_url(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, _token = _runtime(tmp_path)
    emitted: list[dict] = []

    def _emit_host_service_event(hostname, service_mode, event_name, payload):
        emitted.append(
            {
                "hostname": hostname,
                "service_mode": service_mode,
                "event_name": event_name,
                "payload": dict(payload or {}),
            }
        )
        return True

    monkeypatch.setattr(runtime, "emit_host_service_event", _emit_host_service_event)

    response = runtime.app.test_client().post(
        "/remote-files/transfers/upload",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        data={
            "hostname": AGENT_HOSTNAME,
            "agent_id": AGENT_ID,
            "operator_id": "unit",
            "target_path": r"C:\Temp",
            "transfer_base_url": "https://engine.example/_borealis/site-workers/worker-socket-test",
            "files": (io.BytesIO(b"payload"), "payload.txt"),
        },
        content_type="multipart/form-data",
    )

    assert response.status_code == 202
    snapshot = response.get_json()
    transfer_id = snapshot["transfer_id"]
    stored_session = runtime._file_transfer_store.get_session(transfer_id)
    assert stored_session is not None
    assert stored_session["hostname"] == AGENT_HOSTNAME
    assert stored_session["device_guid"] == AGENT_GUID.upper()
    assert emitted == [
        {
            "hostname": AGENT_HOSTNAME,
            "service_mode": "system",
            "event_name": "file_management_request",
            "payload": {
                "action": "upload_start",
                "hostname": AGENT_HOSTNAME,
                "agent_id": AGENT_ID,
                "requested_by": "unit",
                "transfer_id": transfer_id,
                "target_path": r"C:\Temp",
                "transfer_base_url": "https://engine.example/_borealis/site-workers/worker-socket-test",
                "items": [
                    {
                        "item_id": stored_session["upload_items"][0]["item_id"],
                        "client_key": "payload.txt",
                        "name": "payload.txt",
                        "relative_path": "payload.txt",
                        "size_bytes": len(b"payload"),
                        "overwrite_existing": False,
                    }
                ],
            },
        }
    ]


def test_site_worker_agent_upload_item_endpoint_serves_worker_local_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, token = _runtime(tmp_path)
    snapshot = runtime._file_transfer_store.create_upload_session(
        hostname=AGENT_HOSTNAME,
        device_guid=AGENT_GUID,
        agent_id=AGENT_ID,
        operator_id="unit",
        target_path=r"C:\Temp",
        files=[type("_Storage", (), {"filename": "payload.txt", "save": lambda _self, dest: Path(dest).write_bytes(b"payload")})()],
    )
    session = runtime._file_transfer_store.get_session(snapshot["transfer_id"])
    assert session is not None
    item_id = session["upload_items"][0]["item_id"]

    response = runtime.app.test_client().get(
        f"/api/agent/files/transfers/{snapshot['transfer_id']}/upload-item/{item_id}",
        headers={"Authorization": f"Bearer {token}"},
    )

    assert response.status_code == 200
    assert response.data == b"payload"


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


def test_site_worker_socket_rejects_quarantined_agent(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path, device_status="quarantined")

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

    assert ack["error"] == "device_quarantined"
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
    monkeypatch.setattr(
        worker_socket,
        "probe_vnc_security",
        lambda *_args, **_kwargs: VncAuthProbeResult(True, True, "security_types_available", "security", "RFB 003.008", (17, 117, 2)),
    )
    monkeypatch.setattr(
        worker_socket,
        "wait_for_vnc_auth_ready",
        lambda *_args, **_kwargs: VncAuthProbeResult(False, True, "auth_probe_disabled"),
    )

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
    assert session.restart_tunnel is None


def test_site_worker_remote_desktop_honors_request_scoped_vnc_auth_probe(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "0")
    runtime, _token = _runtime(tmp_path)
    operation_token = _issue_remote_desktop_token(runtime)
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)
    calls: list[dict[str, object]] = []

    def _probe(*_args, **kwargs):
        calls.append(dict(kwargs))
        return VncAuthProbeResult(True, True, "server_init_ok")

    monkeypatch.setattr(worker_socket, "wait_for_vnc_auth_ready", _probe)
    monkeypatch.setattr(
        worker_socket,
        "probe_vnc_security",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("security preflight should be skipped for auth_probe")),
    )

    response = runtime.app.test_client().post(
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
            "auth_probe": True,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert {key: payload["auth_probe"][key] for key in ("checked", "ok", "reason")} == {
        "checked": True,
        "ok": True,
        "reason": "server_init_ok",
    }
    assert calls == [
        {
            "timeout_seconds": 5.0,
            "poll_interval_seconds": 0.5,
            "enabled": True,
        }
    ]


def test_vnc_auth_probe_disabled_by_default(monkeypatch) -> None:
    called = False

    def _probe(*_args, **_kwargs):
        nonlocal called
        called = True
        return VncAuthProbeResult(True, True, "server_init_ok")

    monkeypatch.delenv("BOREALIS_VNC_AUTH_PROBE", raising=False)
    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", _probe)

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.20",
        5900,
        "secretpw",
        timeout_seconds=0.25,
        poll_interval_seconds=0.1,
    )

    assert result == VncAuthProbeResult(False, True, "auth_probe_disabled")
    assert called is False


def test_vnc_auth_probe_can_be_enabled(monkeypatch) -> None:
    called = False

    def _probe(*_args, **_kwargs):
        nonlocal called
        called = True
        return VncAuthProbeResult(True, True, "server_init_ok")

    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "1")
    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", _probe)

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.20",
        5900,
        "secretpw",
        timeout_seconds=0.25,
        poll_interval_seconds=0.1,
    )

    assert result == VncAuthProbeResult(True, True, "server_init_ok")
    assert called is True


def test_vnc_auth_probe_can_be_disabled(monkeypatch) -> None:
    called = False

    def _probe(*_args, **_kwargs):
        nonlocal called
        called = True
        return VncAuthProbeResult(True, False, "unexpected_probe")

    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "0")
    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", _probe)

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.20",
        5900,
        "secretpw",
        timeout_seconds=0.25,
        poll_interval_seconds=0.1,
    )

    assert result == VncAuthProbeResult(False, True, "auth_probe_disabled")
    assert called is False


def test_vnc_auth_probe_enabled_argument_overrides_env(monkeypatch) -> None:
    called = False

    def _probe(*_args, **_kwargs):
        nonlocal called
        called = True
        return VncAuthProbeResult(True, True, "server_init_ok")

    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "0")
    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", _probe)

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.20",
        5900,
        "secretpw",
        timeout_seconds=0.25,
        poll_interval_seconds=0.1,
        enabled=True,
    )

    assert result == VncAuthProbeResult(True, True, "server_init_ok")
    assert called is True


def test_vnc_auth_probe_rejection_text_maps_to_actionable_errors() -> None:
    assert (
        worker_socket._vnc_auth_probe_error("too_many_auth_failures:Your connection has been rejected to many attempts.")
        == "vnc_auth_lockout"
    )
    assert worker_socket._vnc_auth_probe_error("auth_rejected:Your connection has been rejected.") == "vnc_auth_failed"
    assert (
        worker_socket._vnc_auth_probe_error(
            "This server does not have a valid password enabled.Until a password is set, incoming connections cannot be accepted."
        )
        == "vnc_password_not_enabled"
    )
    assert worker_socket._vnc_auth_probe_status("vnc_password_not_enabled") == 409
    assert worker_socket._vnc_auth_probe_status("vnc_auth_lockout") == 423
    assert worker_socket._vnc_auth_probe_status("vnc_auth_failed") == 503


def test_site_worker_remote_desktop_rejects_passwordless_vnc_before_guacd(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    operation_token = _issue_remote_desktop_token(runtime)
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)
    rejection = (
        "This server does not have a valid password enabled."
        "Until a password is set, incoming connections cannot be accepted."
    )
    monkeypatch.setattr(
        worker_socket,
        "probe_vnc_security",
        lambda *_args, **_kwargs: VncAuthProbeResult(True, False, rejection, "security", "RFB 003.008"),
    )
    monkeypatch.setattr(
        worker_socket,
        "wait_for_vnc_auth_ready",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("auth probe should not run after security preflight failure")),
    )

    response = runtime.app.test_client().post(
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
        },
    )

    assert response.status_code == 409
    payload = response.get_json()
    assert payload["error"] == "vnc_password_not_enabled"
    assert payload["detail"] == rejection
    assert {key: payload["auth_probe"][key] for key in ("checked", "ok", "reason", "stage")} == {
        "checked": True,
        "ok": False,
        "reason": rejection,
        "stage": "security",
    }


def test_site_worker_remote_desktop_rejects_vnc_lockout_before_guacd(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    operation_token = _issue_remote_desktop_token(runtime)
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)
    rejection = "too_many_auth_failures:Your connection has been rejected to many attempts."
    monkeypatch.setattr(
        worker_socket,
        "probe_vnc_security",
        lambda *_args, **_kwargs: VncAuthProbeResult(True, False, rejection, "security", "RFB 003.008"),
    )
    monkeypatch.setattr(
        worker_socket,
        "wait_for_vnc_auth_ready",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("auth probe should not run after security preflight failure")),
    )

    response = runtime.app.test_client().post(
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
        },
    )

    assert response.status_code == 423
    payload = response.get_json()
    assert payload["error"] == "vnc_auth_lockout"
    assert payload["detail"] == rejection
    assert {key: payload["auth_probe"][key] for key in ("checked", "ok", "reason", "stage")} == {
        "checked": True,
        "ok": False,
        "reason": rejection,
        "stage": "security",
    }


def test_site_worker_remote_desktop_rejects_failed_vnc_auth_probe(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    operation_token = _issue_remote_desktop_token(runtime)
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)
    monkeypatch.setattr(
        worker_socket,
        "probe_vnc_security",
        lambda *_args, **_kwargs: VncAuthProbeResult(True, True, "security_types_available", "security", "RFB 003.008", (17, 117, 2)),
    )
    monkeypatch.setattr(
        worker_socket,
        "wait_for_vnc_auth_ready",
        lambda *_args, **_kwargs: VncAuthProbeResult(True, False, "auth_failed"),
    )

    response = runtime.app.test_client().post(
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
        },
    )

    assert response.status_code == 503
    payload = response.get_json()
    assert payload["error"] == "vnc_auth_failed"
    assert {key: payload["auth_probe"][key] for key in ("checked", "ok", "reason")} == {
        "checked": True,
        "ok": False,
        "reason": "auth_failed",
    }


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
