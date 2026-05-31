from __future__ import annotations

import time

import pytest
from cryptography.hazmat.primitives.asymmetric import ed25519

from Data.Engine.auth import jwt_service as jwt_service_module
from Data.Engine.services.job_scheduler.queue import WORKER_STATUS_RUNNING, register_worker
from Data.Engine.services.remote_ops.sessions import (
    RemoteOpSessionError,
    issue_remote_op_session,
    verify_remote_op_session,
)

from .conftest import EngineTestHarness
from .support.engine import admin_client, client_with_session, db_connection


@pytest.fixture(autouse=True)
def _isolated_token_root(tmp_path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "auth-tokens"))
    monkeypatch.setenv("BOREALIS_PUBLIC_BASE_URL", "https://engine.example.test")


def _jwt_service() -> jwt_service_module.JWTService:
    return jwt_service_module.JWTService(ed25519.Ed25519PrivateKey.generate(), "unit-test-key")


def _device() -> dict[str, object]:
    return {
        "guid": "GUID-TEST-0001",
        "hostname": "test-device",
        "agent_id": "test-device-agent",
        "site_id": 1,
    }


def _worker_route() -> dict[str, object]:
    return {
        "worker_guid": "worker-remote-ops",
        "generation": 3,
        "route_path_prefix": "/_borealis/site-workers/worker-remote-ops",
    }


def _seed_worker_route(harness: EngineTestHarness) -> None:
    with db_connection(harness) as conn:
        register_worker(
            conn,
            worker_guid="worker-remote-ops",
            container_name="site-worker-worker-remote-ops",
            site_id=1,
            status=WORKER_STATUS_RUNNING,
        )
        conn.commit()


def test_remote_op_session_token_scopes_device_worker_and_capability() -> None:
    service = _jwt_service()
    issued = issue_remote_op_session(
        service,
        user={"username": "admin", "role": "Admin"},
        device=_device(),
        worker_route=_worker_route(),
        capabilities=["remote_shell", "remote_desktop"],
        ttl_seconds=120,
    )

    claims = verify_remote_op_session(
        service,
        issued["token"],
        required_capability="shell",
        worker_guid="worker-remote-ops",
        site_id=1,
        device_guid="guid-test-0001",
        hostname="TEST-DEVICE",
    )

    assert claims["user"] == "admin"
    assert claims["site_id"] == 1
    assert claims["worker_guid"] == "worker-remote-ops"
    assert claims["device_guid"] == "GUID-TEST-0001"
    assert claims["route_generation"] == 3
    assert claims["capabilities"] == ["remote_shell", "remote_desktop"]


def test_remote_op_session_verifier_rejects_missing_capability() -> None:
    service = _jwt_service()
    issued = issue_remote_op_session(
        service,
        user={"username": "admin", "role": "Admin"},
        device=_device(),
        worker_route=_worker_route(),
        capabilities="remote_shell",
    )

    with pytest.raises(RemoteOpSessionError) as exc_info:
        verify_remote_op_session(service, issued["token"], required_capability="remote_files")

    assert exc_info.value.code == "capability_denied"


def test_remote_op_session_verifier_rejects_expired_token() -> None:
    service = _jwt_service()
    issued = issue_remote_op_session(
        service,
        user={"username": "admin", "role": "Admin"},
        device=_device(),
        worker_route=_worker_route(),
        capabilities="remote_shell",
        ttl_seconds=30,
        now=int(time.time()) - 120,
    )

    with pytest.raises(RemoteOpSessionError) as exc_info:
        verify_remote_op_session(service, issued["token"], required_capability="remote_shell")

    assert exc_info.value.code == "token_expired"


def test_remote_op_session_endpoint_issues_worker_scoped_token(engine_harness: EngineTestHarness) -> None:
    _seed_worker_route(engine_harness)
    client = admin_client(engine_harness)

    response = client.post(
        "/api/remote-ops/session",
        json={"hostname": "test-device", "capability": "remote_shell", "ttl_seconds": 120},
        headers={"X-Forwarded-Host": "engine.example.test", "X-Forwarded-Proto": "https"},
    )

    assert response.status_code == 200
    payload = response.get_json()
    session = payload["session"]
    assert session["expires_in"] == 120
    assert session["capabilities"] == ["remote_shell"]
    assert session["device"]["guid"] == "GUID-TEST-0001"
    assert session["device"]["site_id"] == 1
    assert session["worker"]["worker_guid"] == "worker-remote-ops"
    assert session["worker"]["route_path_prefix"] == "/_borealis/site-workers/worker-remote-ops"
    assert session["worker"]["urls"]["base"] == "https://engine.example.test/_borealis/site-workers/worker-remote-ops"
    assert session["token"] not in session["worker"]["urls"]["base"]

    claims = verify_remote_op_session(
        jwt_service_module.load_service(),
        session["token"],
        required_capability="remote_shell",
        worker_guid="worker-remote-ops",
        site_id=1,
        device_guid="GUID-TEST-0001",
    )
    assert claims["user"] == "admin"
    assert claims["aud"] == "borealis-site-worker"
    assert claims["iss"] == "borealis-api-backend"


def test_remote_op_session_endpoint_requires_authenticated_user(engine_harness: EngineTestHarness) -> None:
    _seed_worker_route(engine_harness)
    client = engine_harness.app.test_client()

    response = client.post(
        "/api/remote-ops/session",
        json={"hostname": "test-device", "capability": "remote_shell"},
    )

    assert response.status_code == 401


def test_remote_op_session_endpoint_rejects_invalid_capability(engine_harness: EngineTestHarness) -> None:
    _seed_worker_route(engine_harness)
    client = admin_client(engine_harness)

    response = client.post(
        "/api/remote-ops/session",
        json={"hostname": "test-device", "capability": "clipboard"},
    )

    assert response.status_code == 400
    assert response.get_json()["error"] == "invalid_capability"


def test_remote_op_session_endpoint_requires_active_worker_route(engine_harness: EngineTestHarness) -> None:
    client = admin_client(engine_harness)

    response = client.post(
        "/api/remote-ops/session",
        json={"hostname": "test-device", "capability": "remote_shell"},
    )

    assert response.status_code == 409
    assert response.get_json()["error"] == "site_worker_unavailable"


def test_remote_op_session_endpoint_hides_out_of_scope_device(engine_harness: EngineTestHarness) -> None:
    _seed_worker_route(engine_harness)
    with db_connection(engine_harness) as conn:
        conn.execute(
            """
            INSERT INTO users (id, username, display_name, password_sha512, role, last_login, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (2, "operator", "Operator", "test", "User", 0, 0, 0),
        )
        conn.commit()
    client = client_with_session(engine_harness, username="operator", role="User")

    response = client.post(
        "/api/remote-ops/session",
        json={"hostname": "test-device", "capability": "remote_shell"},
    )

    assert response.status_code == 404
    assert response.get_json()["error"] == "not_found"
