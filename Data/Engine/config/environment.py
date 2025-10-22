"""Environment detection for the Borealis Engine."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Tuple


@dataclass(frozen=True, slots=True)
class EngineSettings:
    """Immutable container describing the Engine runtime configuration."""

    project_root: Path
    database_path: Path
    static_root: Path
    cors_allowed_origins: Tuple[str, ...]
    secret_key: str
    debug: bool
    host: str
    port: int

    @property
    def logs_root(self) -> Path:
        """Return the directory where Engine-specific logs should live."""

        return self.project_root / "Logs" / "Server"


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


def _resolve_static_root(project_root: Path) -> Path:
    candidate = os.getenv("BOREALIS_STATIC_ROOT")
    if candidate:
        return Path(candidate).expanduser().resolve()
    return (project_root / "Data" / "Server" / "dist").resolve()


def _parse_origins(raw: str | None) -> Tuple[str, ...]:
    if not raw:
        return ("*",)
    parts: Iterable[str] = (segment.strip() for segment in raw.split(","))
    filtered = tuple(part for part in parts if part)
    return filtered or ("*",)


def load_environment() -> EngineSettings:
    """Load Engine settings from environment variables and filesystem hints."""

    project_root = _resolve_project_root()
    database_path = _resolve_database_path(project_root)
    static_root = _resolve_static_root(project_root)
    cors_allowed_origins = _parse_origins(os.getenv("BOREALIS_CORS_ALLOWED_ORIGINS"))
    secret_key = os.getenv("BOREALIS_FLASK_SECRET_KEY", "change-me")
    debug = os.getenv("BOREALIS_DEBUG", "false").lower() in {"1", "true", "yes", "on"}
    host = os.getenv("BOREALIS_HOST", "127.0.0.1")
    try:
        port = int(os.getenv("BOREALIS_PORT", "5000"))
    except ValueError:
        port = 5000

    return EngineSettings(
        project_root=project_root,
        database_path=database_path,
        static_root=static_root,
        cors_allowed_origins=cors_allowed_origins,
        secret_key=secret_key,
        debug=debug,
        host=host,
        port=port,
    )


__all__ = ["EngineSettings", "load_environment"]
