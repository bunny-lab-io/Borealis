import pytest

pytest.importorskip("jwt")

import json
import sqlite3
import time
from datetime import datetime, timezone
from pathlib import Path

from Data.Engine.config.environment import (
    DatabaseSettings,
    EngineSettings,
    FlaskSettings,
    GitHubSettings,
    ServerSettings,
    SocketIOSettings,
)
from Data.Engine.domain.device_auth import (
    AccessTokenClaims,
    DeviceAuthContext,
    DeviceFingerprint,
    DeviceGuid,
    DeviceIdentity,
    DeviceStatus,
)
from Data.Engine.interfaces.http import register_http_interfaces
from Data.Engine.repositories.sqlite import connection as sqlite_connection
from Data.Engine.repositories.sqlite import migrations as sqlite_migrations
from Data.Engine.server import create_app
from Data.Engine.services.container import build_service_container


@pytest.fixture()
def engine_settings(tmp_path: Path) -> EngineSettings:
    project_root = tmp_path
    static_root = project_root / "static"
    static_root.mkdir()
    (static_root / "index.html").write_text("<html></html>", encoding="utf-8")

    database_path = project_root / "database.db"

    return EngineSettings(
        project_root=project_root,
        debug=False,
        database=DatabaseSettings(path=database_path, apply_migrations=False),
        flask=FlaskSettings(
            secret_key="test-key",
            static_root=static_root,
            cors_allowed_origins=("https://localhost",),
        ),
        socketio=SocketIOSettings(cors_allowed_origins=("https://localhost",)),
        server=ServerSettings(host="127.0.0.1", port=5000),
        github=GitHubSettings(
            default_repo="owner/repo",
            default_branch="main",
            refresh_interval_seconds=60,
            cache_root=project_root / "cache",
        ),
    )


@pytest.fixture()
def prepared_app(engine_settings: EngineSettings):
    settings = engine_settings
    settings.github.cache_root.mkdir(exist_ok=True, parents=True)

    db_factory = sqlite_connection.connection_factory(settings.database.path)
    with sqlite_connection.connection_scope(settings.database.path) as conn:
        sqlite_migrations.apply_all(conn)

    app = create_app(settings, db_factory=db_factory)
    services = build_service_container(settings, db_factory=db_factory)
    app.extensions["engine_services"] = services
    register_http_interfaces(app, services)
    app.config.update(TESTING=True)
    return app


def _insert_device(app, guid: str, fingerprint: str, hostname: str) -> None:
    db_path = Path(app.config["ENGINE_DATABASE_PATH"])
    now = int(time.time())
    with sqlite3.connect(db_path) as conn:
        conn.execute(
            """
            INSERT INTO devices (
                guid,
                hostname,
                created_at,
                last_seen,
                ssl_key_fingerprint,
                token_version,
                status,
                key_added_at
            ) VALUES (?, ?, ?, ?, ?, ?, 'active', ?)
            """,
            (
                guid,
                hostname,
                now,
                now,
                fingerprint.lower(),
                1,
                datetime.now(timezone.utc).isoformat(),
            ),
        )
        conn.commit()


def _build_context(guid: str, fingerprint: str, *, status: DeviceStatus = DeviceStatus.ACTIVE) -> DeviceAuthContext:
    now = int(time.time())
    claims = AccessTokenClaims(
        subject="device",
        guid=DeviceGuid(guid),
        fingerprint=DeviceFingerprint(fingerprint),
        token_version=1,
        issued_at=now,
        not_before=now,
        expires_at=now + 600,
        raw={"sub": "device"},
    )
    identity = DeviceIdentity(DeviceGuid(guid), DeviceFingerprint(fingerprint))
    return DeviceAuthContext(
        identity=identity,
        access_token="token",
        claims=claims,
        status=status,
        service_context="SYSTEM",
    )


def test_heartbeat_updates_device(prepared_app, monkeypatch):
    client = prepared_app.test_client()
    guid = "DE305D54-75B4-431B-ADB2-EB6B9E546014"
    fingerprint = "aa:bb:cc"
    hostname = "device-heartbeat"
    _insert_device(prepared_app, guid, fingerprint, hostname)

    services = prepared_app.extensions["engine_services"]
    context = _build_context(guid, fingerprint)
    monkeypatch.setattr(services.device_auth, "authenticate", lambda request, path: context)

    payload = {
        "hostname": hostname,
        "inventory": {"memory": [{"total": "16GB"}], "cpu": {"cores": 8}},
        "metrics": {"operating_system": "Windows", "last_user": "Admin", "uptime": 120},
        "external_ip": "1.2.3.4",
    }

    start = int(time.time())
    resp = client.post(
        "/api/agent/heartbeat",
        json=payload,
        headers={"Authorization": "Bearer token"},
    )
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {"status": "ok", "poll_after_ms": 15000}

    db_path = Path(prepared_app.config["ENGINE_DATABASE_PATH"])
    with sqlite3.connect(db_path) as conn:
        row = conn.execute(
            "SELECT last_seen, external_ip, memory, cpu FROM devices WHERE guid = ?",
            (guid,),
        ).fetchone()

    assert row is not None
    last_seen, external_ip, memory_json, cpu_json = row
    assert last_seen >= start
    assert external_ip == "1.2.3.4"
    assert json.loads(memory_json)[0]["total"] == "16GB"
    assert json.loads(cpu_json)["cores"] == 8


def test_heartbeat_returns_404_when_device_missing(prepared_app, monkeypatch):
    client = prepared_app.test_client()
    guid = "9E295C27-8339-40C8-AD1A-6ED95C164A4A"
    fingerprint = "11:22:33"
    services = prepared_app.extensions["engine_services"]
    context = _build_context(guid, fingerprint)
    monkeypatch.setattr(services.device_auth, "authenticate", lambda request, path: context)

    resp = client.post(
        "/api/agent/heartbeat",
        json={"hostname": "missing-device"},
        headers={"Authorization": "Bearer token"},
    )
    assert resp.status_code == 404
    assert resp.get_json() == {"error": "device_not_registered"}


def test_script_request_reports_status_and_signing_key(prepared_app, monkeypatch):
    client = prepared_app.test_client()
    guid = "2F8D76C0-38D4-4700-B247-3E90C03A67D7"
    fingerprint = "44:55:66"
    hostname = "device-script"
    _insert_device(prepared_app, guid, fingerprint, hostname)

    services = prepared_app.extensions["engine_services"]
    context = _build_context(guid, fingerprint)
    monkeypatch.setattr(services.device_auth, "authenticate", lambda request, path: context)

    class DummySigner:
        def public_base64_spki(self) -> str:
            return "PUBKEY"

    object.__setattr__(services, "script_signer", DummySigner())

    resp = client.post(
        "/api/agent/script/request",
        json={"guid": guid},
        headers={"Authorization": "Bearer token"},
    )
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {
        "status": "idle",
        "poll_after_ms": 30000,
        "sig_alg": "ed25519",
        "signing_key": "PUBKEY",
    }

    quarantined_context = _build_context(guid, fingerprint, status=DeviceStatus.QUARANTINED)
    monkeypatch.setattr(services.device_auth, "authenticate", lambda request, path: quarantined_context)

    resp = client.post(
        "/api/agent/script/request",
        json={},
        headers={"Authorization": "Bearer token"},
    )
    assert resp.status_code == 200
    assert resp.get_json()["status"] == "quarantined"
    assert resp.get_json()["poll_after_ms"] == 60000

