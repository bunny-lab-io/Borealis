"""Environment detection for the Borealis Engine."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Optional, Sequence, Tuple


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
    # ``environment.py`` lives under ``Data/Engine/config``.  The project
    # root is three levels above this module (the repository checkout).  The
    # previous implementation only walked up two levels which incorrectly
    # treated ``Data/`` as the root, breaking all filesystem discovery logic
    # that expects peers such as ``Data/Server`` to be available.
    return Path(__file__).resolve().parents[3]


def _resolve_database_path(project_root: Path) -> Path:
    candidate = os.getenv("BOREALIS_DATABASE_PATH")
    if candidate:
        return Path(candidate).expanduser().resolve()
    return (project_root / "database.db").resolve()


def _should_apply_migrations() -> bool:
    raw = os.getenv("BOREALIS_ENGINE_AUTO_MIGRATE", "true")
    return raw.lower() in {"1", "true", "yes", "on"}


def _looks_like_compiled_assets(path: Path) -> bool:
    """Return ``True`` when *path* resembles a built frontend bundle."""

    index_file = path / "index.html"
    if not index_file.is_file():
        return False

    # Vite builds always emit an ``assets`` directory containing the hashed
    # JavaScript/CSS bundles alongside ``index.html``.  Checking for this keeps
    # us from accidentally serving the raw source tree (which still has an
    # ``index.html`` entry point but depends on the Vite dev server to transform
    # imports at runtime).
    if (path / "assets").is_dir():
        return True

    # Some packaging flows rename the output folder (for example ``dist``) but
    # still emit a manifest describing the bundled assets.  Presence of either
    # ``manifest.json`` or Vite's ``.vite/manifest.json`` indicates we are
    # looking at compiled output.
    if (path / "manifest.json").is_file():
        return True
    if (path / ".vite" / "manifest.json").is_file():
        return True

    # As a last resort, treat directories without a ``src`` tree and without a
    # ``package.json`` file as potential compiled artefacts.  This still allows
    # packaged builds that were flattened during staging to be discovered.
    if not (path / "src").exists() and not (path / "package.json").exists():
        return True

    return False


def _iter_static_search_roots(project_root: Path) -> Sequence[Path]:
    """Return potential base directories that might contain frontend assets."""

    # Operators frequently set ``BOREALIS_ROOT`` to either the repository root
    # *or* to ``<root>/Data`` (mirroring the legacy packaging layout).  We
    # consider both possibilities plus their siblings so filesystem discovery
    # succeeds regardless of which convention is in effect.
    bases = [project_root]

    parent = project_root.parent
    if parent != project_root:
        bases.append(parent)

    bases.append(project_root / "Data")
    if parent != project_root:
        bases.append(parent / "Data")

    seen: list[Path] = []
    results: list[Path] = []
    for base in bases:
        resolved = base.resolve()
        if resolved in seen:
            continue
        seen.append(resolved)
        results.append(resolved)

    return tuple(results)


def _resolve_static_root(project_root: Path) -> Path:
    candidate = os.getenv("BOREALIS_STATIC_ROOT")
    if candidate:
        return Path(candidate).expanduser().resolve()

    search_roots = _iter_static_search_roots(project_root)
    candidates: tuple[Path, ...] = tuple(
        root / suffix
        for root in search_roots
        for suffix in (
            Path("Engine") / "web-interface" / "build",
            Path("Engine") / "web-interface" / "dist",
            Path("Engine") / "web-interface",
            Path("Data") / "Engine" / "WebUI" / "build",
            Path("Data") / "Engine" / "WebUI",
            Path("Server") / "web-interface" / "build",
            Path("Server") / "web-interface",
            Path("Server") / "WebUI" / "build",
            Path("Server") / "WebUI",
            Path("Data") / "Server" / "web-interface" / "build",
            Path("Data") / "Server" / "web-interface",
            Path("Data") / "Server" / "WebUI" / "build",
            Path("Data") / "Server" / "WebUI",
            Path("Data") / "WebUI" / "build",
            Path("Data") / "WebUI",
        )
    )

    fallback: Optional[Path] = None
    for path in candidates:
        resolved = path.resolve()
        if not resolved.is_dir():
            continue
        if fallback is None:
            fallback = resolved
        if _looks_like_compiled_assets(resolved):
            return resolved

    if fallback is not None:
        return fallback

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
