"""Shared pytest fixtures for Engine HTTP interface tests."""

from __future__ import annotations

from pathlib import Path

import pytest

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
    """Provision an EngineSettings instance backed by a temporary project root."""

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
    """Create a Flask app instance with registered Engine interfaces."""

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

