import hashlib
from pathlib import Path

import pytest

pytest.importorskip("flask")
pytest.importorskip("jwt")

from Data.Engine.config.environment import (
    DatabaseSettings,
    EngineSettings,
    FlaskSettings,
    GitHubSettings,
    ServerSettings,
    SocketIOSettings,
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


def _login(client) -> dict:
    payload = {
        "username": "admin",
        "password_sha512": hashlib.sha512("Password".encode()).hexdigest(),
    }
    resp = client.post("/api/auth/login", json=payload)
    assert resp.status_code == 200
    data = resp.get_json()
    assert isinstance(data, dict)
    return data


def test_auth_me_returns_session_user(prepared_app):
    client = prepared_app.test_client()

    _login(client)
    resp = client.get("/api/auth/me")
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {
        "username": "admin",
        "display_name": "admin",
        "role": "Admin",
    }


def test_auth_me_uses_token_when_session_missing(prepared_app):
    client = prepared_app.test_client()
    login_data = _login(client)
    token = login_data.get("token")
    assert token

    # New client without session
    other_client = prepared_app.test_client()
    other_client.set_cookie(server_name="localhost", key="borealis_auth", value=token)

    resp = other_client.get("/api/auth/me")
    assert resp.status_code == 200
    body = resp.get_json()
    assert body == {
        "username": "admin",
        "display_name": "admin",
        "role": "Admin",
    }


def test_auth_me_requires_authentication(prepared_app):
    client = prepared_app.test_client()
    resp = client.get("/api/auth/me")
    assert resp.status_code == 401
    body = resp.get_json()
    assert body == {"error": "not_authenticated"}
