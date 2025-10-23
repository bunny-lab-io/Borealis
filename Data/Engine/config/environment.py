"""Environment detection for the Borealis Engine."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Tuple


@dataclass(frozen=True, slots=True)
class DatabaseSettings:
    """SQLite database configuration for the Engine."""

    path: Path
    apply_migrations: bool


@dataclass(frozen=True, slots=True)
class FlaskSettings:
    """Parameters that influence Flask application behavior."""

    secret_key: str
    static_root: Path
    cors_allowed_origins: Tuple[str, ...]


@dataclass(frozen=True, slots=True)
class SocketIOSettings:
    """Configuration for the optional Socket.IO server."""

    cors_allowed_origins: Tuple[str, ...]


@dataclass(frozen=True, slots=True)
class ServerSettings:
    """HTTP server binding configuration."""

    host: str
    port: int


@dataclass(frozen=True, slots=True)
class GitHubSettings:
    """Configuration surface for GitHub repository interactions."""

    default_repo: str
    default_branch: str
    refresh_interval_seconds: int
    cache_root: Path

    @property
    def cache_file(self) -> Path:
        """Location of the persisted repository-head cache."""

        return self.cache_root / "repo_hash_cache.json"


@dataclass(frozen=True, slots=True)
class EngineSettings:
    """Immutable container describing the Engine runtime configuration."""

    project_root: Path
    debug: bool
    database: DatabaseSettings
    flask: FlaskSettings
    socketio: SocketIOSettings
    server: ServerSettings
    github: GitHubSettings

    @property
    def logs_root(self) -> Path:
        """Return the directory where Engine-specific logs should live."""

        return self.project_root / "Logs" / "Server"

    @property
    def database_path(self) -> Path:
        """Convenience accessor for the database file path."""

        return self.database.path

    @property
    def apply_migrations(self) -> bool:
        """Return whether schema migrations should run at bootstrap."""

        return self.database.apply_migrations


def _resolve_project_root() -> Path:
    candidate = os.getenv("BOREALIS_ROOT")
    if candidate:
        return Path(candidate).expanduser().resolve()
    return Path(__file__).resolve().parents[2]


def _resolve_database_path(project_root: Path) -> Path:
    candidate = os.getenv("BOREALIS_DATABASE_PATH")
    if candidate:
        return Path(candidate).expanduser().resolve()
    return (project_root / "database.db").resolve()


def _should_apply_migrations() -> bool:
    raw = os.getenv("BOREALIS_ENGINE_AUTO_MIGRATE", "true")
    return raw.lower() in {"1", "true", "yes", "on"}


def _resolve_static_root(project_root: Path) -> Path:
    candidate = os.getenv("BOREALIS_STATIC_ROOT")
    if candidate:
        return Path(candidate).expanduser().resolve()

    candidates = (
        project_root / "Engine" / "web-interface" / "build",
        project_root / "Engine" / "web-interface" / "dist",
        project_root / "Engine" / "web-interface",
        project_root / "Data" / "Engine" / "WebUI" / "build",
        project_root / "Data" / "Engine" / "WebUI",
        project_root / "Data" / "Server" / "web-interface" / "build",
        project_root / "Data" / "Server" / "web-interface",
        project_root / "Data" / "Server" / "WebUI" / "build",
        project_root / "Data" / "Server" / "WebUI",
        project_root / "Data" / "WebUI" / "build",
        project_root / "Data" / "WebUI",
    )
    for path in candidates:
        resolved = path.resolve()
        if resolved.is_dir():
            return resolved

    # Fall back to the first candidate even if it does not yet exist so the
    # Flask factory still initialises; individual requests will surface 404s
    # until an asset build is available, matching the legacy behaviour.
    return candidates[0].resolve()


def _resolve_github_cache_root(project_root: Path) -> Path:
    candidate = os.getenv("BOREALIS_CACHE_DIR")
    if candidate:
        return Path(candidate).expanduser().resolve()
    return (project_root / "Data" / "Engine" / "cache").resolve()


def _parse_refresh_interval(raw: str | None) -> int:
    if not raw:
        return 60
    try:
        value = int(raw)
    except ValueError:
        value = 60
    return max(30, min(value, 3600))


def _parse_origins(raw: str | None) -> Tuple[str, ...]:
    if not raw:
        return ("*",)
    parts: Iterable[str] = (segment.strip() for segment in raw.split(","))
    filtered = tuple(part for part in parts if part)
    return filtered or ("*",)


def load_environment() -> EngineSettings:
    """Load Engine settings from environment variables and filesystem hints."""

    project_root = _resolve_project_root()
    database = DatabaseSettings(
        path=_resolve_database_path(project_root),
        apply_migrations=_should_apply_migrations(),
    )
    cors_allowed_origins = _parse_origins(os.getenv("BOREALIS_CORS_ALLOWED_ORIGINS"))
    flask_settings = FlaskSettings(
        secret_key=os.getenv("BOREALIS_FLASK_SECRET_KEY", "change-me"),
        static_root=_resolve_static_root(project_root),
        cors_allowed_origins=cors_allowed_origins,
    )
    socket_settings = SocketIOSettings(cors_allowed_origins=cors_allowed_origins)
    debug = os.getenv("BOREALIS_DEBUG", "false").lower() in {"1", "true", "yes", "on"}
    host = os.getenv("BOREALIS_HOST", "127.0.0.1")
    try:
        port = int(os.getenv("BOREALIS_PORT", "5000"))
    except ValueError:
        port = 5000
    server_settings = ServerSettings(host=host, port=port)
    github_settings = GitHubSettings(
        default_repo=os.getenv("BOREALIS_REPO", "bunny-lab-io/Borealis"),
        default_branch=os.getenv("BOREALIS_REPO_BRANCH", "main"),
        refresh_interval_seconds=_parse_refresh_interval(os.getenv("BOREALIS_REPO_HASH_REFRESH")),
        cache_root=_resolve_github_cache_root(project_root),
    )

    return EngineSettings(
        project_root=project_root,
        debug=debug,
        database=database,
        flask=flask_settings,
        socketio=socket_settings,
        server=server_settings,
        github=github_settings,
    )


__all__ = [
    "DatabaseSettings",
    "EngineSettings",
    "FlaskSettings",
    "GitHubSettings",
    "SocketIOSettings",
    "ServerSettings",
    "load_environment",
]
