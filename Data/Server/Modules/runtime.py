"""Utility helpers for locating runtime storage paths.

The Borealis repository keeps the authoritative source code under ``Data/``
so that the bootstrap scripts can copy those assets into sibling ``Server/``
and ``Agent/`` directories for execution. Runtime artefacts such as TLS
certificates or signing keys must therefore live outside ``Data`` to avoid
polluting the template tree. This module centralises the path selection so
other modules can rely on a consistent location regardless of whether they
are executed from the copied runtime directory or directly from ``Data``
during development.
"""

from __future__ import annotations

import os
from functools import lru_cache
from pathlib import Path
from typing import Optional


def _env_path(name: str) -> Optional[Path]:
    """Return a resolved ``Path`` for the given environment variable."""

    value = os.environ.get(name)
    if not value:
        return None
    try:
        return Path(value).expanduser().resolve()
    except Exception:
        return None


@lru_cache(maxsize=None)
def project_root() -> Path:
    """Best-effort detection of the repository root."""

    env = _env_path("BOREALIS_PROJECT_ROOT")
    if env:
        return env

    current = Path(__file__).resolve()
    for parent in current.parents:
        if (parent / "Borealis.ps1").exists() or (parent / ".git").is_dir():
            return parent

    # Fallback to the ancestor that corresponds to ``<repo>/`` when the module
    # lives under ``Data/Server/Modules``.
    try:
        return current.parents[4]
    except IndexError:
        return current.parent


@lru_cache(maxsize=None)
def server_runtime_root() -> Path:
    """Location where the running server stores mutable artefacts."""

    env = _env_path("BOREALIS_SERVER_ROOT")
    if env:
        return env

    root = project_root()
    runtime = root / "Server" / "Borealis"
    return runtime


def runtime_path(*parts: str) -> Path:
    """Return a path relative to the server runtime root."""

    return server_runtime_root().joinpath(*parts)


def ensure_runtime_dir(*parts: str) -> Path:
    """Create (if required) and return a runtime directory."""

    path = runtime_path(*parts)
    path.mkdir(parents=True, exist_ok=True)
    return path


@lru_cache(maxsize=None)
def certificates_root() -> Path:
    """Base directory for persisted certificate material."""

    env = _env_path("BOREALIS_CERT_ROOT")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = project_root() / "Certificates"
    root.mkdir(parents=True, exist_ok=True)
    return root


def certificates_path(*parts: str) -> Path:
    """Return a path under the certificates root."""

    return certificates_root().joinpath(*parts)


def ensure_certificates_dir(*parts: str) -> Path:
    """Create (if required) and return a certificates subdirectory."""

    path = certificates_path(*parts)
    path.mkdir(parents=True, exist_ok=True)
    return path
