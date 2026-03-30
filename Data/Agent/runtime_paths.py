"""Shared runtime path helpers for the Borealis agent."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Optional


def find_project_root(start: Optional[Path] = None) -> Path:
    override = os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT")
    if override:
        try:
            override_path = Path(override).expanduser().resolve()
            if override_path.is_dir():
                return override_path
        except Exception:
            pass

    current = Path(start or __file__).resolve()
    for parent in (current.parent, *current.parents):
        try:
            if (
                (parent / "Borealis.ps1").is_file()
                or (parent / "Borealis.sh").is_file()
                or (parent / "users.json").is_file()
                or (parent / ".git").is_dir()
            ):
                return parent
        except Exception:
            continue

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
