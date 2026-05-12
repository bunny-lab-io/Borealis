"""Shared runtime path helpers for the Borealis agent."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Optional


def _resolve_override_root() -> Optional[Path]:
    override = os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT")
    if not override:
        return None
    try:
        candidate = Path(override).expanduser().resolve()
    except Exception:
        return None
    if candidate.is_dir():
        return candidate
    return None


def _discover_project_root(current: Path) -> Optional[Path]:
    search_root = current if current.is_dir() else current.parent
    for parent in (search_root, *search_root.parents):
        try:
            if (
                (parent / "Agent.exe").is_file()
                or (parent / "Agent.sh").is_file()
                or (parent / "Engine.sh").is_file()
                or (parent / ".git").is_dir()
            ):
                return parent
        except Exception:
            continue
    return None


def _discover_installed_project_root(current: Path) -> Optional[Path]:
    search_root = current if current.is_dir() else current.parent
    for parent in (search_root, *search_root.parents):
        try:
            if parent.name.lower() != "borealis":
                continue
            agent_dir = parent.parent
            if agent_dir.name.lower() != "agent":
                continue
            install_root = agent_dir.parent
            if install_root == agent_dir or install_root == parent:
                continue
            return install_root
        except Exception:
            continue
    return None


def _path_within(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except Exception:
        return False


def find_project_root(start: Optional[Path] = None) -> Path:
    current = Path(start or __file__).resolve()
    installed_root = _discover_installed_project_root(current)
    discovered_root = _discover_project_root(current)
    override_root = _resolve_override_root()

    # Prefer the root discovered around the running file tree so stale
    # environment overrides cannot redirect the agent back to an old checkout.
    if installed_root is not None:
        if override_root is not None and _path_within(current, override_root):
            return override_root
        return installed_root
    if discovered_root is not None:
        if override_root is not None and _path_within(current, override_root):
            return override_root
        return discovered_root
    if override_root is not None:
        return override_root

    try:
        return current.parents[2]
    except Exception:
        return current.parent


def agent_runtime_root(start: Optional[Path] = None) -> Path:
    return find_project_root(start) / "Agent"


def agent_logs_root(start: Optional[Path] = None) -> Path:
    return agent_runtime_root(start) / "Logs"


def agent_borealis_root(start: Optional[Path] = None) -> Path:
    return agent_runtime_root(start) / "Borealis"
