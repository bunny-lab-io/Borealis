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

import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from werkzeug.test import EnvironBuilder
from werkzeug.exceptions import ClientDisconnected

from Data.Engine.auth import device_purge_state, jwt_service
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.RemoteDesktop import rfb_probe
from Data.Engine.services.RemoteDesktop.rfb_probe import VncAuthProbeResult
from Data.Engine.services.activity_history import insert_activity_history_row
from Data.Engine.services.job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token
from Data.Engine.services.job_scheduler import worker_socket
from Data.Engine.services.job_scheduler.worker_socket import SiteWorkerSocketRuntime
from Data.Engine.services.remote_ops.sessions import issue_remote_op_session
from Data.Engine.Unit_Tests.test_worker_admission import _initialize, seed_worker
from Data.Engine.services.job_scheduler.worker_admission import AdmissionError, AdmissionTicket


AGENT_GUID = "11111111-2222-3333-4444-555555555555"
AGENT_HOSTNAME = "LAB-ONE"
AGENT_ID = f"{AGENT_HOSTNAME}_{AGENT_GUID}_SYSTEM"


@pytest.fixture(autouse=True)
def _isolated_runtime_files(tmp_path, monkeypatch):
    monkeypatch.setattr(worker_socket.tempfile, "gettempdir", lambda: str(tmp_path))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES", "0")


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
    factory = _db_factory(db_path)
    _initialize(factory)
    owner = seed_worker(factory, site=site_id, worker="22222222-2222-4222-8222-222222222222")
    service = jwt_service.load_service()
    token = service.issue_access_token(AGENT_GUID.upper(), fingerprint, 1)
    runtime = SiteWorkerSocketRuntime(
        worker_guid="22222222-2222-4222-8222-222222222222",
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
        pod_uid=owner.pod_uid,
        node_name=owner.node_name,
    )
    return runtime, token


def _registered_agent(runtime, token, *, version=1, file_version=None, helper_contexts=()):
    client = runtime.socketio.test_client(runtime.app, headers={"Authorization":f"Bearer {token}"})
    assert client.emit("connect_agent", {"agent_id":AGENT_ID,"hostname":AGENT_HOSTNAME,"service_mode":"system","worker_operation_version":version,"worker_operation_file_transfer_version":file_version,"helper_contexts":list(helper_contexts)}, callback=True)["status"] == "ok"
    client.get_received()
    return client


def _claim_count(runtime):
    with runtime.db_conn_factory() as conn:
        return conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?", (runtime.worker_guid,)).fetchone()[0]


def _upload_headers(hostname=AGENT_HOSTNAME, device_guid=AGENT_GUID, agent_id=AGENT_ID):
    return {INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret"),"X-Borealis-Upload-Hostname-B64":base64.urlsafe_b64encode(hostname.encode("utf-8")).decode("ascii").rstrip("="),"X-Borealis-Upload-Device-Guid":device_guid.upper(),"X-Borealis-Upload-Agent-Id-B64":base64.urlsafe_b64encode(agent_id.encode("utf-8")).decode("ascii").rstrip("=")}


def _start_file_transfer(runtime, direction="upload", **overrides):
    values = {"hostname":AGENT_HOSTNAME,"device_guid":AGENT_GUID,"agent_id":AGENT_ID,"operator_id":"unit","transfer_base_url":"https://engine.example/worker"}
    values.update(overrides)
    headers = {INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}
    if direction == "upload":
        headers = _upload_headers(values["hostname"],values["device_guid"],values["agent_id"])
        values.update(target_path=r"C:\Temp",files=(io.BytesIO(b"payload"),"payload.txt"))
        response = runtime.app.test_client().post("/remote-files/transfers/upload",headers=headers,data=values,content_type="multipart/form-data")
    else:
        values.update(items=[{"path":r"C:\Temp\payload.txt"}])
        response = runtime.app.test_client().post("/remote-files/transfers/download",headers=headers,json=values)
    response.get_data()
    response.close()
    return response


def _file_packet(agent):
    return next(item["args"][0] for item in agent.get_received() if item["name"] == "file_management_request")


def _grant_file(agent, packet):
    request = {"job_id":packet["job_id"],"status":"Running","context":packet["context"],"worker_operation_preflight":{"version":1,"nonce":"d"*32}}
    assert agent.emit("quick_job_progress",request,callback=True)["status"] == "execute"


def _file_receipt(packet, status="Completed"):
    return {"job_id":packet["job_id"],"status":status,"stdout":"","stderr":"","context":packet["context"]}


@pytest.mark.parametrize("version", (None, True, 1.0, "1", 0, 2))
@pytest.mark.parametrize("direction", ("upload", "download"))
def test_file_transfer_requires_exact_optional_capability_before_staging(tmp_path, monkeypatch, version, direction):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=version)
    def forbidden(**_kwargs):
        raise AssertionError("staged transfer before capability proof")
    monkeypatch.setattr(runtime._file_transfer_store,"create_"+direction+"_session",forbidden)
    response = _start_file_transfer(runtime,direction)
    assert response.status_code == 503
    assert response.get_json() == {"error":"worker_file_transfer_capability_required"}
    assert _claim_count(runtime) == 0
    assert agent.get_received() == []
    agent.disconnect()


def test_file_transfer_terminal_waits_for_last_lazy_stream_and_seals_new_requests(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    response = _start_file_transfer(runtime)
    assert response.status_code == 202
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    record = runtime._file_transfer_store.get_session(packet["transfer_id"])
    path = f"/api/agent/files/transfers/{packet['transfer_id']}/upload-item/{record['upload_items'][0]['item_id']}"
    headers = {"Authorization":f"Bearer {token}"}
    first = runtime.app.test_client().get(path,headers=headers,buffered=False)
    second = runtime.app.test_client().get(path,headers=headers,buffered=False)
    assert first.status_code == second.status_code == 200
    assert _claim_count(runtime) == 1
    receipt = _file_receipt(packet)
    assert agent.emit("quick_job_result",receipt,callback=True) == {"error":"worker_receipt_uncommitted"}
    blocked = runtime.app.test_client().get(path,headers=headers)
    assert blocked.status_code == 503
    assert blocked.get_json() == {"error":"worker_file_transfer_sealed"}
    first.close()
    assert agent.emit("quick_job_result",receipt,callback=True) == {"error":"worker_receipt_uncommitted"}
    assert _claim_count(runtime) == 1
    second.close()
    agent.disconnect()

    agent = _registered_agent(runtime,token,file_version=1)
    assert agent.emit("quick_job_result",receipt,callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    runtime._file_custody.clear()
    runtime._file_custody_ids.clear()
    assert agent.emit("quick_job_result",receipt,callback=True)["status"] == "committed"
    agent.disconnect()


def test_file_transfer_raw_wsgi_iterable_close_owns_direct_passthrough_lifetime(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    assert _start_file_transfer(runtime).status_code == 202
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    record = runtime._file_transfer_store.get_session(packet["transfer_id"])
    path = f"/api/agent/files/transfers/{packet['transfer_id']}/upload-item/{record['upload_items'][0]['item_id']}"
    builder = EnvironBuilder(path=path,headers={"Authorization":f"Bearer {token}"})
    statuses = []
    iterable = runtime.app.wsgi_app(builder.get_environ(),lambda status,headers,exc_info=None: statuses.append(status))
    assert statuses == ["200 OK"]
    assert b"".join(iterable) == b"payload"
    assert _claim_count(runtime) == 1
    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True) == {"error":"worker_receipt_uncommitted"}
    state = runtime._file_custody[packet["context"]["worker_operation"]["token"]]
    assert state["active"] == 1
    iterable.close()
    iterable.close()
    builder.close()
    assert state["active"] == 0
    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    agent.disconnect()


def test_file_transfer_creation_request_retains_charge_until_raw_wsgi_close(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    builder = EnvironBuilder(path="/remote-files/transfers/upload",method="POST",headers=_upload_headers(),data={"hostname":AGENT_HOSTNAME,"device_guid":AGENT_GUID,"agent_id":AGENT_ID,"target_path":r"C:\Temp","files":(io.BytesIO(b"payload"),"payload.txt")},content_type="multipart/form-data")
    statuses = []
    iterable = runtime.app.wsgi_app(builder.get_environ(),lambda status,headers,exc_info=None: statuses.append(status))
    assert statuses == ["202 ACCEPTED"]
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    assert json.loads(b"".join(iterable))["transfer_id"] == packet["transfer_id"]
    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True) == {"error":"worker_receipt_uncommitted"}
    assert _claim_count(runtime) == 2
    iterable.close()
    iterable.close()
    builder.close()
    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    agent.disconnect()


@pytest.mark.parametrize("lost", ("missing", "replaced"))
def test_file_transfer_missing_original_custody_never_becomes_zero(tmp_path, monkeypatch, lost):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    assert _start_file_transfer(runtime).status_code == 202
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    record = runtime._file_transfer_store.get_session(packet["transfer_id"])
    if lost == "missing":
        runtime._file_custody.clear()
    else:
        runtime._file_transfer_store._sessions[packet["transfer_id"]] = dict(record)
    path = f"/api/agent/files/transfers/{packet['transfer_id']}/upload-item/{record['upload_items'][0]['item_id']}"
    response = runtime.app.test_client().get(path,headers={"Authorization":f"Bearer {token}"})
    assert response.status_code == 503
    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True) == {"error":"worker_receipt_uncommitted"}
    assert _claim_count(runtime) == 1
    agent.disconnect()


def test_file_transfer_browser_content_has_independent_local_charge(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    assert _start_file_transfer(runtime,"download").status_code == 202
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    transfer_id = packet["transfer_id"]
    response = runtime.app.test_client().post(f"/api/agent/files/transfers/{transfer_id}/content",headers={"Authorization":f"Bearer {token}"},data={"artifact":(io.BytesIO(b"download artifact"),"artifact.zip")},content_type="multipart/form-data")
    assert response.status_code == 200
    response.close()
    browser_path = f"/remote-files/transfers/{transfer_id}/content"
    browser_headers = {INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}
    premature = runtime.app.test_client().get(browser_path,headers=browser_headers)
    assert premature.status_code == 503
    assert premature.get_json() == {"error":"worker_file_transfer_terminal_pending"}
    assert _claim_count(runtime) == 1
    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    browser_builder = EnvironBuilder(path=browser_path,headers=browser_headers)
    browser_status = []
    browser = runtime.app.wsgi_app(browser_builder.get_environ(),lambda status,headers,exc_info=None: browser_status.append(status))
    assert browser_status == ["200 OK"]
    assert _claim_count(runtime) == 1
    concurrent = runtime.app.test_client().get(browser_path,headers=browser_headers)
    assert concurrent.status_code == 503
    assert concurrent.get_json() == {"error":"worker_file_content_active"}
    assert _claim_count(runtime) == 1
    assert b"".join(browser) == b"download artifact"
    assert _claim_count(runtime) == 1
    browser.close()
    browser.close()
    browser_builder.close()
    assert _claim_count(runtime) == 0
    assert runtime._file_transfer_store.get_session(transfer_id) is None
    agent.disconnect()


@pytest.mark.parametrize("direction", ("upload", "download"))
@pytest.mark.parametrize("lost", (None, "missing", "replaced", "token_alias", "transfer_alias"))
def test_file_transfer_public_completion_waits_for_original_committed_custody(tmp_path, monkeypatch, direction, lost):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    assert _start_file_transfer(runtime,direction).status_code == 202
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    transfer_id = packet["transfer_id"]
    agent_path = f"/api/agent/files/transfers/{transfer_id}"
    public_path = f"/remote-files/transfers/{transfer_id}"
    agent_headers = {"Authorization":f"Bearer {token}"}
    public_headers = {INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}
    client = runtime.app.test_client()
    if direction == "download":
        progress = client.post(agent_path+"/content",headers=agent_headers,data={"artifact":(io.BytesIO(b"download artifact"),"artifact.zip")},content_type="multipart/form-data")
    else:
        progress = client.post(agent_path+"/progress",headers=agent_headers,json={"status":"completed","bytes_complete":7,"bytes_total":7})
    assert progress.status_code == 200 and progress.get_json()["status"] == "completed"
    progress.close()
    stored = runtime._file_transfer_store.get_session(transfer_id)
    snapshot = worker_socket._transfer_status_snapshot(stored)
    assert snapshot["download_ready"] is (direction == "download")
    assert client.get(public_path+"/status",headers=public_headers).get_json() == {**snapshot,"status":"running","download_ready":False}
    assert runtime._file_transfer_store.get_session(transfer_id) == stored
    assert client.get(agent_path+"/status",headers=agent_headers).get_json() == snapshot
    cancel = client.post(public_path+"/cancel",headers=public_headers)
    stored = runtime._file_transfer_store.get_session(transfer_id)
    snapshot = worker_socket._transfer_status_snapshot(stored)
    assert stored["cancel_requested"] is True and stored["status"] == "completed"
    assert cancel.get_json() == {**snapshot,"status":"running","download_ready":False}
    assert _claim_count(runtime) == 1

    assert agent.emit("quick_job_result",_file_receipt(packet),callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    operation_token = packet["context"]["worker_operation"]["token"]
    state = runtime._file_custody[operation_token]
    if lost == "missing":
        runtime._file_custody.clear()
    elif lost == "replaced":
        runtime._file_transfer_store._sessions[transfer_id] = dict(state["record"])
    elif lost == "token_alias":
        runtime._file_custody["f"*32] = runtime._file_custody.pop(operation_token)
        runtime._file_custody_ids[transfer_id] = "f"*32
    elif lost == "transfer_alias":
        state["transfer_id"] = "different-transfer"
    expected = {**snapshot,"status":"running","download_ready":False} if lost else snapshot
    stored = runtime._file_transfer_store.get_session(transfer_id)
    assert client.get(public_path+"/status",headers=public_headers).get_json() == expected
    assert runtime._file_transfer_store.get_session(transfer_id) == stored
    cancel = client.post(public_path+"/cancel",headers=public_headers)
    snapshot = worker_socket._transfer_status_snapshot(runtime._file_transfer_store.get_session(transfer_id))
    assert cancel.get_json() == ({**snapshot,"status":"running","download_ready":False} if lost else snapshot)
    assert snapshot["status"] == "completed" and snapshot["download_ready"] is (direction == "download")
    assert client.get(agent_path+"/status",headers=agent_headers).get_json() == snapshot
    if not lost and direction == "download":
        content = client.get(public_path+"/content",headers=public_headers,buffered=False)
        assert content.status_code == 200 and content.get_data() == b"download artifact"
        assert _claim_count(runtime) == 1
        content.close()
        assert _claim_count(runtime) == 0
    agent.disconnect()


@pytest.mark.parametrize("status", ("failed", "canceled"))
def test_file_transfer_public_error_status_keeps_existing_semantics_and_charge(tmp_path, monkeypatch, status):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    assert _start_file_transfer(runtime).status_code == 202
    packet = _file_packet(agent)
    transfer_id = packet["transfer_id"]
    client = runtime.app.test_client()
    progress = client.post(f"/api/agent/files/transfers/{transfer_id}/progress",headers={"Authorization":f"Bearer {token}"},json={"status":status,"error":"fixture stop"})
    assert progress.status_code == 200
    public_path = f"/remote-files/transfers/{transfer_id}"
    headers = {INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}
    for response in (client.get(public_path+"/status",headers=headers), client.post(public_path+"/cancel",headers=headers)):
        snapshot = response.get_json()
        assert snapshot["status"] == status and snapshot["error"] == "fixture stop"
        assert snapshot["download_ready"] is False
    assert _claim_count(runtime) == 1
    agent.disconnect()


@pytest.mark.parametrize("direction", ("upload", "download"))
def test_file_transfer_staging_runs_after_admission_and_failure_keeps_unknown_charge(tmp_path, monkeypatch, direction):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    def fail(**_kwargs):
        assert _claim_count(runtime) == (2 if direction == "upload" else 1)
        raise ValueError("synthetic staging failure")
    monkeypatch.setattr(runtime._file_transfer_store,"create_"+direction+"_session",fail)
    response = _start_file_transfer(runtime,direction)
    assert response.status_code == (400 if direction == "upload" else 500)
    assert _claim_count(runtime) == 1
    assert agent.get_received() == []
    assert len(runtime._file_custody) == 1
    assert next(iter(runtime._file_custody.values()))["active"] == 0
    agent.disconnect()


def test_file_transfer_manifest_mismatch_rejects_before_admission(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    def forbidden(**_kwargs):
        raise AssertionError("invalid manifest reached staging")
    monkeypatch.setattr(runtime._file_transfer_store,"create_upload_session",forbidden)
    response = _start_file_transfer(runtime,manifest=json.dumps([{"name":"one.txt","relative_path":"one.txt","size_bytes":7},{"name":"two.txt","relative_path":"two.txt","size_bytes":9}]))
    assert response.status_code == 400
    assert response.get_json() == {"error":"upload_manifest_mismatch"}
    assert _claim_count(runtime) == 0
    assert runtime._file_custody == {}
    assert agent.get_received() == []
    agent.disconnect()


@pytest.mark.parametrize("case", ("missing", "padding", "alias_bits", "oversize", "invalid_utf8", "control", "guid", "legacy_agent"))
def test_upload_private_identity_and_capability_reject_before_body_read(tmp_path, monkeypatch, case):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=None if case=="legacy_agent" else 1)
    headers = _upload_headers()
    if case == "missing":
        headers.pop("X-Borealis-Upload-Hostname-B64")
    elif case == "padding":
        headers["X-Borealis-Upload-Hostname-B64"] += "="
    elif case == "alias_bits":
        headers["X-Borealis-Upload-Hostname-B64"] = "YR"
    elif case == "oversize":
        headers["X-Borealis-Upload-Hostname-B64"] = "a"*341
    elif case == "invalid_utf8":
        headers["X-Borealis-Upload-Hostname-B64"] = "_w"
    elif case == "control":
        headers["X-Borealis-Upload-Hostname-B64"] = base64.urlsafe_b64encode(b"host\x00name").decode().rstrip("=")
    elif case == "guid":
        headers["X-Borealis-Upload-Device-Guid"] = "malformed"
    builder = EnvironBuilder(path="/remote-files/transfers/upload",method="POST",headers=headers,content_type="multipart/form-data; boundary=fixture",content_length=100)
    class UnreadBody:
        def read(self,*_args): raise AssertionError("body read before identity/capability proof")
        def readinto(self,*_args): raise AssertionError("body read before identity/capability proof")
    environ = builder.get_environ()
    environ["wsgi.input"] = UnreadBody()
    statuses = []
    iterable = runtime.app.wsgi_app(environ,lambda status,headers,exc_info=None: statuses.append(status))
    assert statuses == ["503 SERVICE UNAVAILABLE"]
    assert "error" in json.loads(b"".join(iterable))
    iterable.close()
    builder.close()
    assert _claim_count(runtime) == 0
    assert runtime._file_custody == {}
    agent.disconnect()


def test_upload_stage_charge_precedes_multipart_read_and_failed_parse_releases_on_close(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    builder = EnvironBuilder(path="/remote-files/transfers/upload",method="POST",headers=_upload_headers(),content_type="multipart/form-data; boundary=fixture",content_length=100)
    reads = []
    class CanceledBody:
        def read(self,*_args):
            reads.append(True)
            assert _claim_count(runtime) == 1
            raise ClientDisconnected()
        def readinto(self,*_args): return self.read()
    environ = builder.get_environ()
    environ["wsgi.input"] = CanceledBody()
    statuses = []
    iterable = runtime.app.wsgi_app(environ,lambda status,headers,exc_info=None: statuses.append(status))
    assert statuses == ["400 BAD REQUEST"] and reads
    assert _claim_count(runtime) == 1
    assert runtime._file_custody == {}
    assert agent.get_received() == []
    b"".join(iterable)
    assert _claim_count(runtime) == 1
    iterable.close()
    iterable.close()
    builder.close()
    assert _claim_count(runtime) == 0
    with runtime.db_conn_factory() as conn:
        status,metadata = conn.execute("SELECT status,metadata_json FROM activity_history").fetchone()
        binding = json.loads(metadata)["worker_operation"]
        assert status == "Failed" and binding["kind"] == "file_stage"
        assert binding["agent_guid"] == "" and "grant_nonce" not in binding
    agent.disconnect()


def test_upload_form_identity_must_equal_trusted_header_before_remote_admission(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    response = runtime.app.test_client().post("/remote-files/transfers/upload",headers=_upload_headers(),data={"hostname":"other-device","device_guid":AGENT_GUID,"agent_id":AGENT_ID,"target_path":r"C:\Temp","files":(io.BytesIO(b"payload"),"payload.txt")},content_type="multipart/form-data",buffered=False)
    assert response.status_code == 400
    assert response.get_json() == {"error":"worker_file_upload_identity_mismatch"}
    assert _claim_count(runtime) == 1
    assert runtime._file_custody == {}
    response.close()
    assert _claim_count(runtime) == 0
    assert agent.get_received() == []
    agent.disconnect()


def test_file_transfer_cancelled_content_post_keeps_custody_until_handler_and_response_close(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    assert _start_file_transfer(runtime,"download").status_code == 202
    packet = _file_packet(agent)
    _grant_file(agent,packet)
    transfer_id = packet["transfer_id"]
    entered, interrupted = threading.Event(), threading.Event()
    responses = []
    statuses = []
    builder = EnvironBuilder(path=f"/api/agent/files/transfers/{transfer_id}/content",method="POST",headers={"Authorization":f"Bearer {token}"},data={"artifact":(io.BytesIO(b"partial"),"artifact.zip")},content_type="multipart/form-data")
    def cancelled_save(*_args,**_kwargs):
        entered.set()
        assert interrupted.wait(3)
        raise ConnectionResetError("synthetic canceled upload")
    monkeypatch.setattr(runtime._file_transfer_store,"store_download_artifact",cancelled_save)
    def post_content():
        responses.append(runtime.app.wsgi_app(builder.get_environ(),lambda status,headers,exc_info=None: statuses.append(status)))
    thread = threading.Thread(target=post_content)
    thread.start()
    try:
        assert entered.wait(3)
        cancel = runtime.app.test_client().post(f"/remote-files/transfers/{transfer_id}/cancel",headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")})
        assert cancel.status_code == 200
        receipt = _file_receipt(packet,"Failed")
        assert agent.emit("quick_job_result",receipt,callback=True) == {"error":"worker_receipt_uncommitted"}
        assert _claim_count(runtime) == 1
    finally:
        interrupted.set()
        thread.join(3)
    assert not thread.is_alive()
    assert len(responses) == 1 and statuses == ["500 INTERNAL SERVER ERROR"]
    assert b"Internal Server Error" in b"".join(responses[0])
    assert agent.emit("quick_job_result",receipt,callback=True) == {"error":"worker_receipt_uncommitted"}
    responses[0].close()
    responses[0].close()
    builder.close()
    assert agent.emit("quick_job_result",receipt,callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    agent.disconnect()


def test_file_transfer_uncertain_emit_retains_original_ticket(tmp_path, monkeypatch):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    emit = runtime.socketio.emit
    def uncertain(event,*args,**kwargs):
        result = emit(event,*args,**kwargs)
        if event == "file_management_request":
            raise RuntimeError("reply lost after accepted dispatch")
        return result
    monkeypatch.setattr(runtime.socketio,"emit",uncertain)
    response = _start_file_transfer(runtime)
    assert response.status_code == 503
    assert response.get_json() == {"error":"worker_dispatch_uncertain"}
    packet = _file_packet(agent)
    assert _claim_count(runtime) == 1
    _grant_file(agent,packet)
    cancel = runtime.app.test_client().post(f"/remote-files/transfers/{packet['transfer_id']}/cancel",headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")})
    assert cancel.status_code == 200
    assert _claim_count(runtime) == 1
    assert agent.emit("quick_job_result",_file_receipt(packet,"Failed"),callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    agent.disconnect()


@pytest.mark.parametrize("protocol", ("vnc", "rdp"))
def test_worker_desktop_session_blocks_before_proxy_probe_or_registration(tmp_path, monkeypatch, protocol):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, _token = _runtime(tmp_path)
    def forbidden(*_args, **_kwargs):
        raise AssertionError("desktop effect before terminal custody")
    monkeypatch.setattr(runtime,"_ensure_guacamole_proxy",forbidden)
    monkeypatch.setattr(runtime._guacamole_registry,"create",forbidden)
    monkeypatch.setattr(worker_socket,"probe_vnc_security",forbidden)
    response = runtime.app.test_client().post("/remote-desktop/vnc/session",headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")},json={"operation_token":_issue_remote_desktop_token(runtime),"agent_id":AGENT_ID,"protocol":protocol})
    assert response.status_code == 503
    assert response.get_json() == {"error":"worker_desktop_terminal_unavailable"}
    assert _claim_count(runtime) == 0


@pytest.mark.parametrize("event,payload,error", (
    *(("quick_job_run",{"run_mode":mode},"worker_currentuser_terminal_unavailable") for mode in ("current_user", "interactive", "user", "unknown", True, ["currentuser"], {"mode":"currentuser"})),
    ("file_management_request",{"action":"upload_start"},"worker_file_transfer_managed_request_required"),
    ("file_management_request",{"action":"download_start"},"worker_file_transfer_managed_request_required"),
    ("patch_install_request",{},"worker_operation_unsupported"),
    ("patch_reboot_request",{},"worker_operation_unsupported"),
    ("patch_policy_apply",{},"worker_operation_unsupported"),
    ("agent_update_request",{},"worker_operation_unsupported"),
    ("agent_maintenance_request",{},"worker_operation_unsupported"),
    *((event,{},"worker_operation_terminal_unavailable") for event in ("vnc_start","vnc_stop","vnc_refresh","vnc_credential_request")),
))
def test_worker_active_events_without_terminal_adapter_reject_before_effects(tmp_path, monkeypatch, event, payload, error):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    try:
        for path in ("event", "call"):
            response = runtime.app.test_client().post("/remote-ops/host-service/"+path, headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}, json={"hostname":AGENT_HOSTNAME,"event_name":event,"payload":payload,"allow_pending":True})
            assert response.status_code == 503
            assert response.get_json() == {"error":error}
        assert _claim_count(runtime) == 0
        assert agent.get_received() == []
        assert runtime._pending_host_events == {}
    finally:
        agent.disconnect()


@pytest.mark.parametrize("event,payload", (
    ("quick_job_run",{"run_mode":"system","context":{"assembly_guid":"fixture-assembly"}}),
    ("quick_job_run",{"run_mode":"currentuser","context":{"assembly_guid":"fixture-assembly"}}),
    ("service_control_action",{"service_name":"fixture","action":"restart"}),
    ("software_inventory_refresh_request",{}),
    ("patch_inventory_refresh_request",{}),
    ("process_management_request",{"action":"list"}),
    ("registry_management_request",{"action":"list"}),
    ("file_management_request",{"action":"list"}),
    ("remote_shell_restart",{}),
    ("patch_policy_enforcement_request",{"request_id":"fixture-policy","enforcement_mode":"managed"}),
    ("rdp_start",{"agent_id":AGENT_ID,"rdp_port":3389}),
))
def test_worker_terminal_receipt_and_preflight_use_authenticated_socket(tmp_path, monkeypatch, event, payload):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    unaffiliated = runtime.socketio.test_client(runtime.app)
    try:
        assert runtime.emit_host_service_event(AGENT_HOSTNAME,"system",event,payload)
        packet = next(row["args"][0] for row in agent.get_received() if row["name"] == event)
        if "context" in payload:
            assert packet["context"]["assembly_guid"] == "fixture-assembly"
        ticket = AdmissionTicket(packet["job_id"], packet["context"]["worker_operation"]["token"])
        request = {"job_id":ticket.job_id,"status":"Running","context":{"worker_operation":ticket.wire()},"worker_operation_preflight":{"version":1,"nonce":"a"*32}}
        assert _claim_count(runtime) == 1
        assert unaffiliated.emit("quick_job_progress",request,callback=True) == {"error":"worker_preflight_unavailable"}
        assert agent.emit("quick_job_progress",request,callback=True)["status"] == "execute"
        agent.emit("quick_job_progress",{"job_id":ticket.job_id,"status":"Completed","context":{"worker_operation":ticket.wire()},"metadata":{"worker_operation":{"receipt_sha256":"f"*64}}},callback=True)
        assert _claim_count(runtime) == 1
        with runtime.db_conn_factory() as conn:
            binding = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (ticket.job_id,)).fetchone()[0])["worker_operation"]
            assert binding["grant_nonce"] == "a"*32
            assert "receipt_sha256" not in binding
        receipt = {"job_id":ticket.job_id,"status":"Completed","stdout":"done","stderr":"","context":{"worker_operation":ticket.wire()}}
        assert unaffiliated.emit("quick_job_result",receipt,callback=True) == {"error":"worker_receipt_uncommitted"}
        assert _claim_count(runtime) == 1
        ack = agent.emit("quick_job_result",receipt,callback=True)
        assert ack == {"worker_operation":ticket.wire(),"job_id":ticket.job_id,"status":"committed"}
        assert _claim_count(runtime) == 0
        assert agent.emit("quick_job_result",receipt,callback=True) == ack
    finally:
        unaffiliated.disconnect()
        agent.disconnect()


@pytest.mark.parametrize("event,payload", (("service_control_action", {"action":"restart"}), ("remote_shell_restart", {}), ("quick_job_run", {"run_mode":"currentuser"}), ("patch_policy_enforcement_request", {"enforcement_mode":"managed"}), ("rdp_start", {"agent_id":AGENT_ID,"rdp_port":3389})))
def test_worker_capability_and_uncertain_emit_do_not_release_or_requeue(tmp_path, monkeypatch, event, payload):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    client = runtime.app.test_client()
    args = {"headers":{INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")},"json":{"hostname":AGENT_HOSTNAME,"event_name":event,"payload":payload,"allow_pending":True}}
    agent = _registered_agent(runtime, token, version=0)
    assert client.post("/remote-ops/host-service/event",**args).get_json() == {"error":"worker_operation_capability_required"}
    assert _claim_count(runtime) == 0
    assert runtime.emit_host_service_event(AGENT_HOSTNAME,"system","vpn_tunnel_activity",{})
    agent.disconnect()
    agent = _registered_agent(runtime, token)
    monkeypatch.setattr(runtime.registry, "emit_to_host", lambda *_args,**_kwargs: False)
    response = client.post("/remote-ops/host-service/event",**args)
    assert response.status_code == 503
    assert response.get_json() == {"error":"worker_dispatch_uncertain"}
    assert _claim_count(runtime) == 1
    assert runtime._pending_host_events == {}
    agent.disconnect()
    assert _claim_count(runtime) == 1


@pytest.mark.parametrize("event,payload", (("service_control_action", {"action":"restart"}), ("remote_shell_restart", {}), ("quick_job_run", {"run_mode":"currentuser"}), ("patch_policy_enforcement_request", {"enforcement_mode":"managed"}), ("rdp_start", {"agent_id":AGENT_ID,"rdp_port":3389})))
def test_worker_database_connections_return_before_dispatch(tmp_path, monkeypatch, event, payload):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    open_connections = []
    factory = runtime.admission.db_factory
    class TrackedConnection:
        def __init__(self):
            self.conn = factory()
            open_connections.append(self)
        def __getattr__(self, name):
            return getattr(self.conn, name)
        def close(self):
            self.conn.close()
            open_connections.remove(self)
    runtime.admission.db_factory = TrackedConnection
    def emit(*_args,**_kwargs):
        assert open_connections == []
        assert _claim_count(runtime) == 1
        return True
    monkeypatch.setattr(runtime.registry,"emit_to_host",emit)
    assert runtime.emit_host_service_event(AGENT_HOSTNAME,"system",event,payload)
    assert open_connections == []
    agent.disconnect()


def _issue_shell_token(
    runtime: SiteWorkerSocketRuntime,
    *,
    agent_id: str = AGENT_ID,
    site_id: int = 7,
    worker_guid: str = "22222222-2222-4222-8222-222222222222",
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
    worker_guid: str = "22222222-2222-4222-8222-222222222222",
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
                if msg.get("type") == "worker_operation_bind":
                    response = {"type":"worker_operation_bound", "session_id":msg["session_id"], "job_id":msg["job_id"], "worker_operation":msg["context"]["worker_operation"], "status":"accepted"}
                    stream.write(json.dumps(response).encode("utf-8") + b"\n")
                elif msg.get("type") == "ping":
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
            "worker_operation_version": 1,
        },
        callback=True,
    )

    assert ack["status"] == "ok"
    assert runtime.has_host_service_socket("lab-one", "system")
    assert runtime.has_registered_agents()
    assert runtime.emit_host_service_event("LAB-ONE", "system", "service_control_action", {"service_name":"unit", "action":"restart"})
    received = client.get_received()
    assert any(item["name"] == "service_control_action" for item in received)

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


def test_site_worker_internal_ansible_route_hides_runner_exception(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, _token = _runtime(tmp_path)

    class _Runner:
        def queue_run(self, **_kwargs):
            raise RuntimeError("stack trace includes /tmp/secret-playbook.yml")

    runtime.set_ansible_runner(_Runner())
    client = runtime.app.test_client()
    response = client.post(
        "/automation/ansible/run",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={"queue_run": {"hostname":"borealis-engine-01", "playbook_name": "Ping"}},
    )

    assert response.status_code == 500
    payload = response.get_json()
    assert payload["error"] == "ansible_queue_failed"
    assert payload["message"] == "Unable to queue Ansible run."
    assert "secret-playbook" not in response.get_data(as_text=True)


@pytest.mark.parametrize("message,expected", (
    ("worker_capacity_exhausted", "worker_capacity_exhausted"),
    ("worker_capacity_exhausted\nprivate_password=fixture-secret /private/stack.py", "worker_operation_unavailable"),
))
@pytest.mark.parametrize("endpoint", ("event", "call", "ansible", "upload", "download", "desktop"))
def test_worker_http_errors_return_only_literal_codes(tmp_path, monkeypatch, endpoint, message, expected):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, _token = _runtime(tmp_path)
    def fail(*_args,**_kwargs):
        raise AdmissionError(message) from ValueError("private chained diagnostic")
    headers = {INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}
    client = runtime.app.test_client()
    if endpoint in {"event", "call"}:
        method = "emit_host_service_event" if endpoint == "event" else "call_host_service_event"
        monkeypatch.setattr(runtime,method,fail)
        response = client.post("/remote-ops/host-service/"+endpoint,headers=headers,json={"hostname":AGENT_HOSTNAME,"event_name":"service_control_action","payload":{}})
    elif endpoint == "ansible":
        runtime.set_ansible_runner(types.SimpleNamespace(queue_run=lambda **_kwargs: pytest.fail("runner reached after admission failure")))
        monkeypatch.setattr(runtime.admission,"admit",fail)
        response = client.post("/automation/ansible/run",headers=headers,json={"hostname":AGENT_HOSTNAME})
    elif endpoint in {"upload", "download"}:
        monkeypatch.setattr(runtime,"_begin_upload_stage" if endpoint == "upload" else "_admit_file_transfer",fail)
        response = _start_file_transfer(runtime,endpoint)
    else:
        monkeypatch.setattr(runtime,"_require_desktop_terminal_adapter",fail)
        response = client.post("/remote-desktop/vnc/session",headers=headers,json={"operation_token":_issue_remote_desktop_token(runtime),"agent_id":AGENT_ID})
    assert response.status_code == 503
    assert response.get_json() == {"error":expected}
    assert "private" not in response.get_data(as_text=True) and "fixture-secret" not in response.get_data(as_text=True)
    assert _claim_count(runtime) == 0


@pytest.mark.parametrize("error,expected", (
    (ValueError("upload_files_required"), "upload_files_required"),
    (ValueError("upload_manifest_mismatch"), "upload_manifest_mismatch"),
    (RuntimeError("worker_shell_operation_unbound"), "worker_shell_operation_unbound"),
    (AdmissionError("worker_file_transfer_custody_missing"), "worker_file_transfer_custody_missing"),
    (AdmissionError("worker_file_transfer_terminal_pending"), "worker_file_transfer_terminal_pending"),
    (AdmissionError("worker_dispatch_uncertain"), "worker_dispatch_uncertain"),
    (AdmissionError("worker_capacity_exhausted", "private diagnostic"), "worker_operation_unavailable"),
    (AdmissionError(), "worker_operation_unavailable"),
    (ValueError("private_password=fixture-secret /private/stack.py"), "worker_operation_unavailable"),
    (RuntimeError("private_password=fixture-secret /private/stack.py"), "worker_operation_unavailable"),
))
def test_worker_error_code_preserves_known_codes_without_diagnostics(error, expected):
    assert worker_socket._worker_error_code(error) == expected


def test_worker_error_code_never_formats_exception():
    class DiagnosticError(AdmissionError):
        def __str__(self):
            raise AssertionError("exception formatting must not reach response projection")
    assert worker_socket._worker_error_code(DiagnosticError("worker_capacity_exhausted")) == "worker_capacity_exhausted"
    assert worker_socket._worker_error_code(DiagnosticError("private diagnostic")) == "worker_operation_unavailable"


@pytest.mark.parametrize("message,expected", (
    ("upload_files_required", "upload_files_required"),
    ("upload_manifest_mismatch", "upload_manifest_mismatch"),
    ("private_password=fixture-secret /private/stack.py", "worker_operation_unavailable"),
))
def test_worker_upload_validation_error_hides_unexpected_diagnostics(tmp_path, monkeypatch, message, expected):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    def fail(**_kwargs):
        raise ValueError(message)
    monkeypatch.setattr(runtime._file_transfer_store,"create_upload_session",fail)
    response = _start_file_transfer(runtime)
    assert response.status_code == 400 and response.get_json() == {"error":expected}
    # Error shaping cannot fabricate remote completion for an admitted request.
    assert _claim_count(runtime) == 1
    agent.disconnect()


@pytest.mark.parametrize("message,expected", (
    ("worker_shell_operation_unbound", "worker_shell_operation_unbound"),
    ("private_password=fixture-secret /private/stack.py", "worker_operation_unavailable"),
))
def test_worker_shell_send_error_hides_runtime_diagnostics(tmp_path, monkeypatch, message, expected):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT",str(tmp_path/"tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE","threading")
    runtime, _token = _runtime(tmp_path)
    client = runtime.socketio.test_client(runtime.app)
    def fail(_payload):
        raise RuntimeError(message)
    session = types.SimpleNamespace(is_active=lambda: True,send=fail)
    sid = runtime.socketio.server.manager.sid_from_eio_sid(client.eio_sid,"/")
    runtime._shell_sessions[sid] = session
    assert client.emit("vpn_shell_send",{"data":"fixture"},callback=True) == {"error":expected}
    runtime._shell_sessions.clear()
    client.disconnect()


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
            "worker_operation_version": 1,
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


def test_site_worker_file_upload_rejects_missing_terminal_adapter_before_effects(tmp_path: Path, monkeypatch) -> None:
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
            "transfer_base_url": "https://engine.example/_borealis/site-workers/22222222-2222-4222-8222-222222222222",
            "files": (io.BytesIO(b"payload"), "payload.txt"),
        },
        content_type="multipart/form-data",
    )

    assert response.status_code == 503
    assert response.get_json() == {"error":"worker_file_upload_identity_required"}
    assert emitted == []
    assert not list(runtime._file_transfer_store._root_dir.iterdir())


def test_site_worker_agent_upload_item_endpoint_serves_worker_local_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime,token,file_version=1)
    started = _start_file_transfer(runtime)
    assert started.status_code == 202
    snapshot = started.get_json()
    session = runtime._file_transfer_store.get_session(snapshot["transfer_id"])
    assert session is not None
    item_id = session["upload_items"][0]["item_id"]

    response = runtime.app.test_client().get(
        f"/api/agent/files/transfers/{snapshot['transfer_id']}/upload-item/{item_id}",
        headers={"Authorization": f"Bearer {token}"},
    )

    assert response.status_code == 200
    assert response.data == b"payload"
    response.close()
    agent.disconnect()


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
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
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
            "image_codec": "png",
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
    assert session.image_codec == "png"
    assert session.restart_tunnel is None


def test_site_worker_remote_desktop_registers_rdp_without_vnc_probe(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
    operation_token = _issue_remote_desktop_token(runtime)
    monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", lambda: True)
    monkeypatch.setattr(
        worker_socket,
        "probe_vnc_security",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("RDP must not use VNC preflight")),
    )
    monkeypatch.setattr(
        worker_socket,
        "wait_for_vnc_auth_ready",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("RDP must not use VNC auth probe")),
    )

    response = runtime.app.test_client().post(
        "/remote-desktop/vnc/session",
        headers={INTERNAL_TOKEN_HEADER: internal_token("unit-internal-secret")},
        json={
            "operation_token": operation_token,
            "protocol": "rdp",
            "agent_id": AGENT_ID,
            "host": "10.255.0.20",
            "port": 3389,
            "username": "nicole",
            "password": "domain-password",
            "domain": "LAB",
            "security": "nla",
            "ignore_cert": True,
            "operator_id": "unit",
            "session_id": "rdp-session-1",
            "participant_id": "participant-1",
            "role": "controller",
        },
    )

    assert response.status_code == 200
    session = runtime._guacamole_registry.consume(response.get_json()["token"])
    assert session is not None
    assert session.protocol == "rdp"
    assert session.port == 3389
    assert session.username == "nicole"
    assert session.password == "domain-password"
    assert session.domain == "LAB"
    assert session.security == "nla"
    assert session.ignore_cert is True


def test_site_worker_remote_desktop_honors_request_scoped_vnc_auth_probe(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "0")
    runtime, _token = _runtime(tmp_path)
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
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


def test_vnc_auth_challenge_response_matches_rfb_vncauth_vector() -> None:
    challenge = bytes(range(rfb_probe.RFB_VNC_AUTH_CHALLENGE_BYTES))

    response = rfb_probe._vnc_auth_challenge_response("password", challenge)
    truncated_response = rfb_probe._vnc_auth_challenge_response("password-more", challenge)

    assert response == bytes.fromhex("b866924125c8eebb9debc1db61c538e2")
    assert truncated_response == response


def test_vnc_auth_challenge_response_rejects_non_rfb_challenge_lengths() -> None:
    assert rfb_probe._vnc_auth_challenge_response("password", b"short") is None
    assert rfb_probe._vnc_auth_challenge_response("password", bytes(range(17))) is None


def test_site_worker_remote_desktop_rejects_passwordless_vnc_before_guacd(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, _token = _runtime(tmp_path)
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
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
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
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
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
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
    # Exercise retained transport internals behind separately tested unavailable gate.
    monkeypatch.setattr(runtime, "_require_desktop_terminal_adapter", lambda: None)
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
    runtime, token = _runtime(tmp_path)
    agent_client = runtime.socketio.test_client(runtime.app, headers={"Authorization":f"Bearer {token}"})
    assert agent_client.emit("connect_agent", {"agent_id":AGENT_ID,"hostname":AGENT_HOSTNAME,"service_mode":"system","worker_operation_version":1}, callback=True)["status"] == "ok"
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
        binding = server.wait_for_message("worker_operation_bind")
        assert _claim_count(runtime) == 1
        assert agent_client.emit("quick_job_progress",{"job_id":binding["job_id"],"status":"Running","context":binding["context"],"worker_operation_preflight":{"version":1,"nonce":"c"*32}},callback=True)["status"] == "execute"

        send_ack = client.emit("vpn_shell_send", {"data": "whoami\n"}, callback=True)
        assert send_ack["status"] == "ok"
        stdin = server.wait_for_message("stdin")
        assert base64.b64decode(stdin["data"]).decode("utf-8") == "whoami\n"

        close_ack = client.emit("vpn_shell_close", {}, callback=True)
        assert close_ack["status"] == "ok"
        assert _claim_count(runtime) == 1

    client.disconnect()
    assert runtime._shell_sessions == {}
    assert runtime._shell_sessions_by_agent == {}
    assert _claim_count(runtime) == 1
    receipt = {"job_id":binding["job_id"],"status":"Completed","stdout":"","stderr":"","context":binding["context"]}
    assert agent_client.emit("quick_job_result",receipt,callback=True)["status"] == "committed"
    assert _claim_count(runtime) == 0
    agent_client.disconnect()


@pytest.mark.parametrize("ack_status,terminal_status", (("accepted", "Completed"), ("pending", "Failed")))
def test_worker_shell_restart_receipt_preserves_original_shell_custody(tmp_path, monkeypatch, ack_status, terminal_status):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    browser = runtime.socketio.test_client(runtime.app)
    _seed_vpn_lease(runtime)

    def binding(packet):
        with runtime.db_conn_factory() as conn:
            return json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (packet["job_id"],)).fetchone()[0])["worker_operation"]

    def grant(packet, nonce):
        return agent.emit("quick_job_progress", {"job_id":packet["job_id"], "status":"Running", "context":packet["context"], "worker_operation_preflight":{"version":1, "nonce":nonce}}, callback=True)

    def receipt(packet, status="Completed"):
        return {"job_id":packet["job_id"], "status":status, "stdout":"", "stderr":"", "context":packet["context"]}

    try:
        with _FakeShellServer() as server:
            opened = browser.emit("vpn_shell_open", {"agent_id":AGENT_ID, "operation_token":_issue_shell_token(runtime), "shell_port":server.port, "tunnel":{"agent_id":AGENT_ID, "virtual_ip":"127.0.0.1/32"}}, callback=True)
            assert opened["status"] == "ok"
            old_shell = server.wait_for_message("worker_operation_bind")
            assert grant(old_shell, "c" * 32)["status"] == "execute"
            old_binding = binding(old_shell)
            assert old_binding["kind"] == "shell"
            sent = []

            def dispatch(hostname, mode, event, packet, *, timeout):
                assert (hostname, mode, event) == (AGENT_HOSTNAME, "system", "remote_shell_restart")
                sent.append(packet)
                assert _claim_count(runtime) == 2
                # No Agent completion may precede its durable nonce grant.
                assert agent.emit("quick_job_result", receipt(packet), callback=True) == {"error":"worker_receipt_uncommitted"}
                assert grant(packet, "d" * 32)["status"] == "execute"
                assert grant(packet, "d" * 32)["status"] == "execute"
                assert grant(packet, "e" * 32)["status"] == "deny"
                return {"ok":True, "status":ack_status}

            monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
            assert runtime.call_host_service_event(AGENT_HOSTNAME, "system", "remote_shell_restart", {}) == {"ok":True, "status":ack_status}
            assert _claim_count(runtime) == 2, "restart dispatch ACK released custody"
            assert len(sent) == 1
            restart = sent[0]
            assert restart["job_id"] != old_shell["job_id"]
            assert restart["context"]["worker_operation"] != old_shell["context"]["worker_operation"]
            restart_binding = binding(restart)
            assert restart_binding == {
                "version":1, "token":restart["context"]["worker_operation"]["token"],
                "job_id":restart["job_id"], "worker_guid":runtime.worker_guid,
                "node_name":runtime.admission.node_name, "pod_uid":runtime.admission.pod_uid,
                "route_generation":1, "site_id":runtime.site_id, "kind":"remote_shell_restart",
                "agent_guid":AGENT_GUID.upper(), "grant_nonce":"d" * 32,
            }
            assert binding(old_shell) == old_binding
            for forged in (
                {**receipt(restart), "context":old_shell["context"]},
                {**receipt(old_shell), "context":restart["context"]},
                {**receipt(restart), "status":"accepted"},
            ):
                assert agent.emit("quick_job_result", forged, callback=True) == {"error":"worker_receipt_uncommitted"}
                assert _claim_count(runtime) == 2
            terminal = receipt(restart, terminal_status)
            ack = agent.emit("quick_job_result", terminal, callback=True)
            assert ack == {"worker_operation":restart["context"]["worker_operation"], "job_id":restart["job_id"], "status":"committed"}
            assert _claim_count(runtime) == 1, "restart receipt released old shell custody"
            assert binding(old_shell) == old_binding
            assert agent.emit("quick_job_result", terminal, callback=True) == ack
            assert agent.emit("quick_job_result", {**terminal, "stdout":"forged replacement receipt"}, callback=True) == {"error":"worker_receipt_uncommitted"}
            with runtime.db_conn_factory() as conn:
                ledger = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?", (runtime.worker_guid,)).fetchone()[0])["worker_admission"]["operations"]
            assert set(ledger) == {old_binding["token"]}
            assert browser.emit("vpn_shell_close", {}, callback=True)["status"] == "ok"
        browser.disconnect()
        assert _claim_count(runtime) == 1, "shell transport close released custody"
        assert binding(old_shell) == old_binding
        assert agent.emit("quick_job_result", receipt(old_shell), callback=True)["status"] == "committed"
        assert _claim_count(runtime) == 0
    finally:
        if browser.is_connected():
            browser.disconnect()
        agent.disconnect()


@pytest.mark.parametrize("response_kind", ("timeout", "legacy_rejection"))
def test_worker_shell_restart_uncertain_call_retains_charge(tmp_path, monkeypatch, response_kind):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    sent = []

    def dispatch(_hostname, _mode, event, packet, *, timeout):
        assert event == "remote_shell_restart"
        sent.append(packet)
        if response_kind == "timeout":
            raise TimeoutError("restart dispatch response lost")
        # Older Agent with base worker-operation support rejects this tracked
        # event after grant but before effects. Hold its terminal callback to
        # prove the ordinary error ACK cannot substitute for that receipt.
        assert agent.emit("quick_job_progress", {"job_id":packet["job_id"], "status":"Running", "context":packet["context"], "worker_operation_preflight":{"version":1, "nonce":"d" * 32}}, callback=True)["status"] == "execute"
        return {"ok":False, "status":"rejected", "error":"worker_operation_unavailable"}

    monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
    try:
        if response_kind == "timeout":
            with pytest.raises(TimeoutError, match="restart dispatch response lost"):
                runtime.call_host_service_event(AGENT_HOSTNAME, "system", "remote_shell_restart", {})
        else:
            assert runtime.call_host_service_event(AGENT_HOSTNAME, "system", "remote_shell_restart", {}) == {"ok":False, "status":"rejected", "error":"worker_operation_unavailable"}
        assert len(sent) == 1
        assert _claim_count(runtime) == 1
        assert runtime._pending_host_events == {}
        packet = sent[0]
        terminal = {"job_id":packet["job_id"], "status":"Failed", "stdout":"", "stderr":"", "context":packet["context"]}
        if response_kind == "timeout":
            assert agent.emit("quick_job_result", terminal, callback=True) == {"error":"worker_receipt_uncommitted"}
            assert _claim_count(runtime) == 1
        else:
            assert agent.emit("quick_job_result", terminal, callback=True)["status"] == "committed"
            assert _claim_count(runtime) == 0
    finally:
        agent.disconnect()
    assert _claim_count(runtime) == (1 if response_kind == "timeout" else 0)


def _signed_currentuser_job(runtime):
    script = b"Write-Output 'fixture-current-user'\r\n"
    signer = Ed25519PrivateKey.generate()
    metadata = {"assembly_guid":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "requested_by":"fixture-operator"}
    with runtime.db_conn_factory() as conn:
        job_id = insert_activity_history_row(conn, hostname=AGENT_HOSTNAME, script_path="fixture.ps1", script_name="Current user fixture", script_type="powershell", ran_at=int(time.time()), status="Running", metadata=metadata)
        conn.commit()
    assert job_id
    return {
        "job_id":job_id, "target_hostname":AGENT_HOSTNAME, "run_mode":"currentuser",
        "target_context":"currentuser", "session_target":"all_active_sessions",
        "script_type":"powershell", "script_content":base64.b64encode(script).decode(), "script_encoding":"base64",
        "signature":base64.b64encode(signer.sign(script)).decode(), "sig_alg":"ed25519",
        "signing_key":base64.b64encode(signer.public_key().public_bytes(serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo)).decode(),
        "environment":{"FIXTURE_VALUE":"unchanged"}, "context":{"assembly_guid":metadata["assembly_guid"], "activity_metadata":metadata},
    }


@pytest.mark.parametrize("terminal_status", ("Completed", "Failed"))
def test_worker_currentuser_signed_job_preserves_original_custody(tmp_path, monkeypatch, terminal_status):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token, helper_contexts=("currentuser",))
    unaffiliated = runtime.socketio.test_client(runtime.app)
    payload = _signed_currentuser_job(runtime)
    original = json.loads(json.dumps(payload))
    try:
        assert runtime.registry.get_agent_id_for_host_mode(AGENT_HOSTNAME, "currentuser") == AGENT_ID
        response = runtime.app.test_client().post("/remote-ops/host-service/event", headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}, json={"hostname":AGENT_HOSTNAME, "service_mode":"currentuser", "event_name":"quick_job_run", "payload":payload})
        assert response.status_code == 200
        assert response.get_json()["emitted"] is True
        packet = next(row["args"][0] for row in agent.get_received() if row["name"] == "quick_job_run")
        operation = packet["context"]["worker_operation"]
        assert payload == original
        assert {key:value for key,value in packet.items() if key != "context"} == {key:value for key,value in original.items() if key != "context"}
        assert {key:value for key,value in packet["context"].items() if key != "worker_operation"} == original["context"]
        # Worker preserves the actual Ed25519/SPKI payload; Agent companion
        # tests own signature validation and native process completion proof.
        public_key = serialization.load_der_public_key(base64.b64decode(packet["signing_key"]))
        public_key.verify(base64.b64decode(packet["signature"]), base64.b64decode(packet["script_content"]))
        request = {"job_id":packet["job_id"], "status":"Running", "context":{"worker_operation":operation}, "worker_operation_preflight":{"version":1, "nonce":"a" * 32}}
        receipt = {"job_id":packet["job_id"], "status":terminal_status, "stdout":"fixture-output", "stderr":"", "context":packet["context"]}
        assert agent.emit("quick_job_result", receipt, callback=True) == {"error":"worker_receipt_uncommitted"}
        assert unaffiliated.emit("quick_job_progress", request, callback=True) == {"error":"worker_preflight_unavailable"}
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        assert agent.emit("quick_job_progress", {**request, "worker_operation_preflight":{"version":1, "nonce":"b" * 32}}, callback=True)["status"] == "deny"
        with runtime.db_conn_factory() as conn:
            metadata = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (payload["job_id"],)).fetchone()[0])
            assert conn.execute("SELECT COUNT(*) FROM activity_history").fetchone()[0] == 1
        assert metadata["worker_operation"] == {
            "version":1, "token":operation["token"], "job_id":payload["job_id"], "worker_guid":runtime.worker_guid,
            "node_name":runtime.admission.node_name, "pod_uid":runtime.admission.pod_uid, "route_generation":1,
            "site_id":runtime.site_id, "kind":"quick_job_run", "agent_guid":AGENT_GUID.upper(), "grant_nonce":"a" * 32,
        }
        assert metadata["assembly_guid"] == payload["context"]["assembly_guid"]
        assert unaffiliated.emit("quick_job_result", receipt, callback=True) == {"error":"worker_receipt_uncommitted"}
        forged = {**receipt, "context":{"worker_operation":{"version":1, "token":"f" * 32}}}
        assert agent.emit("quick_job_result", forged, callback=True) == {"error":"worker_receipt_uncommitted"}
        agent.emit("quick_job_progress", {**receipt, "metadata":{"worker_operation":{"receipt_sha256":"f" * 64}}}, callback=True)
        assert _claim_count(runtime) == 1
        with pytest.raises(AdmissionError, match="worker_operation_already_admitted"):
            runtime.emit_host_service_event(AGENT_HOSTNAME, "currentuser", "quick_job_run", payload)
        assert not any(row["name"] == "quick_job_run" for row in agent.get_received())
        ack = agent.emit("quick_job_result", receipt, callback=True)
        assert ack == {"worker_operation":operation, "job_id":payload["job_id"], "status":"committed"}
        assert _claim_count(runtime) == 0
        assert agent.emit("quick_job_result", receipt, callback=True) == ack
        assert agent.emit("quick_job_result", {**receipt, "stdout":"forged current-user receipt"}, callback=True) == {"error":"worker_receipt_uncommitted"}
        assert _claim_count(runtime) == 0
    finally:
        unaffiliated.disconnect()
        agent.disconnect()


@pytest.mark.parametrize("response_kind", ("missing", "accepted", "pending", "rejected", "timeout"))
def test_worker_currentuser_nonterminal_dispatch_retains_charge(tmp_path, monkeypatch, response_kind):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token, helper_contexts=("currentuser",))
    payload = _signed_currentuser_job(runtime)
    sent = []
    response = None if response_kind == "missing" else {"ok":response_kind != "rejected", "status":response_kind}

    def dispatch(hostname, mode, event, packet, *, timeout):
        assert (hostname, mode, event) == (AGENT_HOSTNAME, "currentuser", "quick_job_run")
        assert packet["job_id"] == payload["job_id"]
        sent.append(packet)
        request = {"job_id":packet["job_id"], "status":"Running", "context":{"worker_operation":packet["context"]["worker_operation"]}, "worker_operation_preflight":{"version":1, "nonce":"c" * 32}}
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        if response_kind == "timeout":
            raise TimeoutError("current-user dispatch response lost after grant")
        return response

    monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
    try:
        if response_kind == "timeout":
            with pytest.raises(TimeoutError, match="response lost after grant"):
                runtime.call_host_service_event(AGENT_HOSTNAME, "currentuser", "quick_job_run", payload)
        else:
            assert runtime.call_host_service_event(AGENT_HOSTNAME, "currentuser", "quick_job_run", payload) == response
        assert _claim_count(runtime) == 1, "current-user response released custody without terminal receipt"
        assert len(sent) == 1
        assert runtime._pending_host_events == {}
        with runtime.db_conn_factory() as conn:
            binding = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (payload["job_id"],)).fetchone()[0])["worker_operation"]
        assert binding["grant_nonce"] == "c" * 32
        assert "receipt_sha256" not in binding
    finally:
        agent.disconnect()
    assert _claim_count(runtime) == 1


@pytest.mark.parametrize("helper_contexts,version", (((), 1), (("currentuser",), None), (("currentuser",), 0)))
def test_worker_currentuser_requires_helper_route_and_operation_capability(tmp_path, monkeypatch, helper_contexts, version):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token, version=version, helper_contexts=helper_contexts)
    payload = _signed_currentuser_job(runtime)
    try:
        if not helper_contexts:
            assert runtime.emit_host_service_event(AGENT_HOSTNAME, "currentuser", "quick_job_run", payload) is False
        else:
            with pytest.raises(AdmissionError, match="worker_operation_capability_required"):
                runtime.emit_host_service_event(AGENT_HOSTNAME, "currentuser", "quick_job_run", payload)
        assert _claim_count(runtime) == 0
        assert agent.get_received() == []
        assert runtime._pending_host_events == {}
    finally:
        agent.disconnect()


@pytest.mark.parametrize("mode,terminal_status", (("managed", "Completed"), ("frozen", "Completed"), ("unmanaged", "Completed"), ("managed", "Failed")))
def test_worker_patch_policy_response_requires_original_terminal_receipt(tmp_path, monkeypatch, mode, terminal_status):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    request_id = "fixture-policy-request"
    with runtime.db_conn_factory() as conn:
        job_id = insert_activity_history_row(conn, hostname=AGENT_HOSTNAME, script_path="", script_name="Policy enforcement fixture", script_type="", ran_at=int(time.time()), status="Running", metadata={"request_id":request_id})
        conn.commit()
    payload = {"job_id":job_id, "hostname":AGENT_HOSTNAME, "request_id":request_id, "enforcement_mode":mode, "context":{"policy_id":7}}
    original = json.loads(json.dumps(payload))
    policy_response = {"ok":True, "status":"applied", "request_id":request_id, "mode":mode, "stdout":"", "stderr":"", "exit_code":0, "drift_detected":False}
    if terminal_status == "Failed":
        policy_response.update(ok=False, status="failed", exit_code=1, error="patch_policy_enforcement_failed")
    sent = []

    def dispatch(hostname, service_mode, event, packet, *, timeout):
        assert (hostname, service_mode, event) == (AGENT_HOSTNAME, "system", "patch_policy_enforcement_request")
        sent.append(packet)
        assert {key:value for key,value in packet.items() if key != "context"} == {key:value for key,value in original.items() if key != "context"}
        assert {key:value for key,value in packet["context"].items() if key != "worker_operation"} == original["context"]
        request = {"job_id":job_id, "status":"Running", "context":{"worker_operation":packet["context"]["worker_operation"]}, "worker_operation_preflight":{"version":1, "nonce":"a" * 32}}
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        assert agent.emit("quick_job_progress", {**request, "worker_operation_preflight":{"version":1, "nonce":"b" * 32}}, callback=True)["status"] == "deny"
        return policy_response

    monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
    try:
        result = runtime.app.test_client().post("/remote-ops/host-service/call", headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}, json={"hostname":AGENT_HOSTNAME, "event_name":"patch_policy_enforcement_request", "payload":payload})
        assert result.status_code == 200, result.get_json()
        assert result.get_json() == {"called":True, "response":policy_response}
        assert _claim_count(runtime) == 1, "policy response released custody without terminal receipt"
        assert payload == original
        assert len(sent) == 1
        packet = sent[0]
        operation = packet["context"]["worker_operation"]
        with runtime.db_conn_factory() as conn:
            metadata = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (job_id,)).fetchone()[0])
            assert conn.execute("SELECT COUNT(*) FROM activity_history").fetchone()[0] == 1
        assert metadata["request_id"] == request_id
        assert metadata["worker_operation"] == {
            "version":1, "token":operation["token"], "job_id":job_id, "worker_guid":runtime.worker_guid,
            "node_name":runtime.admission.node_name, "pod_uid":runtime.admission.pod_uid, "route_generation":1,
            "site_id":runtime.site_id, "kind":"patch_policy_enforcement_request", "agent_guid":AGENT_GUID.upper(), "grant_nonce":"a" * 32,
        }
        with pytest.raises(AdmissionError, match="worker_operation_already_admitted"):
            runtime.call_host_service_event(AGENT_HOSTNAME, "system", "patch_policy_enforcement_request", payload)
        assert len(sent) == 1
        receipt = {"job_id":job_id, "status":terminal_status, "stdout":"", "stderr":"", "context":packet["context"]}
        assert agent.emit("quick_job_result", {**receipt, "status":"applied"}, callback=True) == {"error":"worker_receipt_uncommitted"}
        assert _claim_count(runtime) == 1
        ack = agent.emit("quick_job_result", receipt, callback=True)
        assert ack == {"worker_operation":operation, "job_id":job_id, "status":"committed"}
        assert _claim_count(runtime) == 0
        assert agent.emit("quick_job_result", receipt, callback=True) == ack
        assert agent.emit("quick_job_result", {**receipt, "stdout":"forged policy receipt"}, callback=True) == {"error":"worker_receipt_uncommitted"}
        assert _claim_count(runtime) == 0
    finally:
        agent.disconnect()


@pytest.mark.parametrize("response_kind", ("missing", "malformed", "pending", "rejected", "timeout"))
def test_worker_patch_policy_uncertain_dispatch_retains_charge(tmp_path, monkeypatch, response_kind):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    payload = {"hostname":AGENT_HOSTNAME, "request_id":"fixture-policy-uncertain", "enforcement_mode":"managed"}
    response = None if response_kind == "missing" else {"ok":response_kind != "rejected", "status":response_kind}
    if response_kind == "malformed":
        response = ["not", "a", "policy", "result"]
    elif response_kind == "pending":
        response = {"ok":False, "status":"pending", "error":"patch_policy_completion_unconfirmed"}
    elif response_kind == "rejected":
        response = {"ok":False, "status":"rejected", "error":"worker_operation_unavailable"}
    sent = []

    def dispatch(_hostname, _mode, event, packet, *, timeout):
        assert event == "patch_policy_enforcement_request"
        sent.append(packet)
        request = {"job_id":packet["job_id"], "status":"Running", "context":packet["context"], "worker_operation_preflight":{"version":1, "nonce":"c" * 32}}
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        if response_kind == "timeout":
            raise TimeoutError("policy dispatch response lost after grant")
        return response

    monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
    try:
        if response_kind == "timeout":
            with pytest.raises(TimeoutError, match="response lost after grant"):
                runtime.call_host_service_event(AGENT_HOSTNAME, "system", "patch_policy_enforcement_request", payload)
        else:
            assert runtime.call_host_service_event(AGENT_HOSTNAME, "system", "patch_policy_enforcement_request", payload) == response
        assert _claim_count(runtime) == 1
        assert len(sent) == 1
        assert runtime._pending_host_events == {}
        with runtime.db_conn_factory() as conn:
            binding = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (sent[0]["job_id"],)).fetchone()[0])["worker_operation"]
        assert binding["grant_nonce"] == "c" * 32
        assert "receipt_sha256" not in binding
    finally:
        agent.disconnect()
    assert _claim_count(runtime) == 1


def _rdp_readiness_payload():
    return {"agent_id":AGENT_ID, "port":3389, "rdp_port":3389, "allowed_ips":"10.255.0.1/32", "engine_virtual_ip":"10.255.0.1/32", "virtual_ip":"10.255.0.2/32", "reason":"rdp_establish", "timeout_seconds":60}


@pytest.mark.parametrize("terminal_status", ("Completed", "Failed"))
def test_worker_rdp_readiness_requires_receipt_and_keeps_desktop_blocked(tmp_path, monkeypatch, terminal_status):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    payload = _rdp_readiness_payload()
    original = dict(payload)
    response = {"status":"ok", "ready":True, "port":3389}
    if terminal_status == "Failed":
        response = {"status":"error", "error":"rdp_service_not_ready", "detail":"fixture readiness timeout", "ready":False}
    sent = []

    def dispatch(hostname, mode, event, packet, *, timeout):
        assert (hostname, mode, event) == (AGENT_HOSTNAME, "system", "rdp_start")
        assert {key:value for key,value in packet.items() if key not in {"job_id", "context"}} == original
        sent.append(packet)
        request = {"job_id":packet["job_id"], "status":"Running", "context":packet["context"], "worker_operation_preflight":{"version":1, "nonce":"a" * 32}}
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        assert agent.emit("quick_job_progress", {**request, "worker_operation_preflight":{"version":1, "nonce":"b" * 32}}, callback=True)["status"] == "deny"
        return response

    monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
    try:
        result = runtime.app.test_client().post("/remote-ops/host-service/call", headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}, json={"hostname":AGENT_HOSTNAME, "event_name":"rdp_start", "payload":payload, "timeout_seconds":60})
        assert result.status_code == 200, result.get_json()
        assert result.get_json() == {"called":True, "response":response}
        assert _claim_count(runtime) == 1, "RDP readiness response released custody without terminal receipt"
        assert payload == original and len(sent) == 1
        packet = sent[0]
        operation = packet["context"]["worker_operation"]
        with runtime.db_conn_factory() as conn:
            binding = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (packet["job_id"],)).fetchone()[0])["worker_operation"]
        assert binding == {
            "version":1, "token":operation["token"], "job_id":packet["job_id"], "worker_guid":runtime.worker_guid,
            "node_name":runtime.admission.node_name, "pod_uid":runtime.admission.pod_uid, "route_generation":1,
            "site_id":runtime.site_id, "kind":"rdp_start", "agent_guid":AGENT_GUID.upper(), "grant_nonce":"a" * 32,
        }
        with pytest.raises(AdmissionError, match="worker_operation_already_admitted"):
            runtime.call_host_service_event(AGENT_HOSTNAME, "system", "rdp_start", {**payload, "job_id":packet["job_id"]})
        assert len(sent) == 1
        receipt = {"job_id":packet["job_id"], "status":terminal_status, "stdout":"", "stderr":"", "context":packet["context"]}
        assert agent.emit("quick_job_result", {**receipt, "status":"ok", "ready":True}, callback=True) == {"error":"worker_receipt_uncommitted"}
        assert _claim_count(runtime) == 1
        ack = agent.emit("quick_job_result", receipt, callback=True)
        assert ack == {"worker_operation":operation, "job_id":packet["job_id"], "status":"committed"}
        assert _claim_count(runtime) == 0
        assert agent.emit("quick_job_result", receipt, callback=True) == ack
        assert agent.emit("quick_job_result", {**receipt, "stdout":"forged RDP readiness receipt"}, callback=True) == {"error":"worker_receipt_uncommitted"}

        def forbidden(*_args, **_kwargs):
            raise AssertionError("RDP readiness enabled a desktop session effect")

        monkeypatch.setattr(runtime, "_ensure_guacamole_proxy", forbidden)
        monkeypatch.setattr(runtime._guacamole_registry, "create", forbidden)
        monkeypatch.setattr(worker_socket, "probe_vnc_security", forbidden)
        for protocol in ("vnc", "rdp"):
            desktop = runtime.app.test_client().post("/remote-desktop/vnc/session", headers={INTERNAL_TOKEN_HEADER:internal_token("unit-internal-secret")}, json={"operation_token":_issue_remote_desktop_token(runtime), "agent_id":AGENT_ID, "protocol":protocol})
            assert desktop.status_code == 503
            assert desktop.get_json() == {"error":"worker_desktop_terminal_unavailable"}
        assert _claim_count(runtime) == 0
    finally:
        agent.disconnect()


@pytest.mark.parametrize("response_kind", ("ready", "readiness_timeout", "missing", "malformed", "pending", "rejected", "timeout"))
def test_worker_rdp_readiness_uncertainty_retains_charge(tmp_path, monkeypatch, response_kind):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "tokens"))
    monkeypatch.setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
    runtime, token = _runtime(tmp_path)
    agent = _registered_agent(runtime, token)
    responses = {
        "ready":{"status":"ok", "ready":True, "port":3389},
        "readiness_timeout":{"status":"error", "error":"rdp_service_not_ready", "ready":False},
        "missing":None, "malformed":["not", "a", "readiness", "result"],
        "pending":{"status":"pending", "error":"rdp_completion_unconfirmed", "ready":False},
        "rejected":{"ok":False, "status":"rejected", "error":"worker_operation_unavailable"},
    }
    sent = []

    def dispatch(_hostname, _mode, event, packet, *, timeout):
        assert event == "rdp_start"
        sent.append(packet)
        request = {"job_id":packet["job_id"], "status":"Running", "context":packet["context"], "worker_operation_preflight":{"version":1, "nonce":"c" * 32}}
        assert agent.emit("quick_job_progress", request, callback=True)["status"] == "execute"
        if response_kind == "timeout":
            raise TimeoutError("RDP readiness response lost after grant")
        return responses[response_kind]

    monkeypatch.setattr(runtime.registry, "call_to_host", dispatch)
    try:
        if response_kind == "timeout":
            with pytest.raises(TimeoutError, match="response lost after grant"):
                runtime.call_host_service_event(AGENT_HOSTNAME, "system", "rdp_start", _rdp_readiness_payload())
        else:
            assert runtime.call_host_service_event(AGENT_HOSTNAME, "system", "rdp_start", _rdp_readiness_payload()) == responses[response_kind]
        assert _claim_count(runtime) == 1
        assert len(sent) == 1 and runtime._pending_host_events == {}
        with runtime.db_conn_factory() as conn:
            binding = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (sent[0]["job_id"],)).fetchone()[0])["worker_operation"]
        assert binding["grant_nonce"] == "c" * 32
        assert "receipt_sha256" not in binding
    finally:
        agent.disconnect()
    assert _claim_count(runtime) == 1
