"""Runtime filesystem helpers for the Borealis Engine."""

from __future__ import annotations

import os
from functools import lru_cache
from pathlib import Path
from typing import Optional

__all__ = [
    "agent_certificates_path",
    "ensure_agent_certificates_dir",
    "ensure_certificates_dir",
    "ensure_runtime_dir",
    "ensure_server_certificates_dir",
    "project_root",
    "runtime_path",
    "server_certificates_path",
]


def _env_path(name: str) -> Optional[Path]:
    value = os.environ.get(name)
    if not value:
        return None
    try:
        return Path(value).expanduser().resolve()
    except Exception:
        return None


@lru_cache(maxsize=None)
def project_root() -> Path:
    env = _env_path("BOREALIS_PROJECT_ROOT")
    if env:
        return env

    current = Path(__file__).resolve()
    for parent in current.parents:
        if (parent / "Borealis.ps1").exists() or (parent / ".git").is_dir():
            return parent

    try:
        return current.parents[1]
    except IndexError:
        return current.parent


@lru_cache(maxsize=None)
def server_runtime_root() -> Path:
    env = _env_path("BOREALIS_ENGINE_ROOT") or _env_path("BOREALIS_SERVER_ROOT")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = project_root() / "Engine"
    root.mkdir(parents=True, exist_ok=True)
    return root


def runtime_path(*parts: str) -> Path:
    return server_runtime_root().joinpath(*parts)


def ensure_runtime_dir(*parts: str) -> Path:
    path = runtime_path(*parts)
    path.mkdir(parents=True, exist_ok=True)
    return path


@lru_cache(maxsize=None)
def certificates_root() -> Path:
    env = _env_path("BOREALIS_CERTIFICATES_ROOT") or _env_path("BOREALIS_CERT_ROOT")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = project_root() / "Certificates"
    root.mkdir(parents=True, exist_ok=True)
    for name in ("Server", "Agent"):
        try:
            (root / name).mkdir(parents=True, exist_ok=True)
        except Exception:
            pass
    return root


@lru_cache(maxsize=None)
def server_certificates_root() -> Path:
    env = _env_path("BOREALIS_SERVER_CERT_ROOT")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = certificates_root() / "Server"
    root.mkdir(parents=True, exist_ok=True)
    return root


@lru_cache(maxsize=None)
def agent_certificates_root() -> Path:
    env = _env_path("BOREALIS_AGENT_CERT_ROOT")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = certificates_root() / "Agent"
    root.mkdir(parents=True, exist_ok=True)
    return root


def certificates_path(*parts: str) -> Path:
    return certificates_root().joinpath(*parts)


def ensure_certificates_dir(*parts: str) -> Path:
    path = certificates_path(*parts)
    path.mkdir(parents=True, exist_ok=True)
    return path


def server_certificates_path(*parts: str) -> Path:
    return server_certificates_root().joinpath(*parts)


def ensure_server_certificates_dir(*parts: str) -> Path:
    path = server_certificates_path(*parts)
    path.mkdir(parents=True, exist_ok=True)
    return path


def agent_certificates_path(*parts: str) -> Path:
    return agent_certificates_root().joinpath(*parts)


def ensure_agent_certificates_dir(*parts: str) -> Path:
    path = agent_certificates_path(*parts)
    path.mkdir(parents=True, exist_ok=True)
    return path
