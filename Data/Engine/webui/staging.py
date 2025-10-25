"""Helpers for staging the Engine web interface assets."""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass
from pathlib import Path
from typing import Optional


@dataclass(frozen=True, slots=True)
class WebUIStagingResult:
    """Summary of the web interface staging operation."""

    source: Path
    destination: Path
    copied: bool
    reason: Optional[str] = None


def _resolve_repository_root(project_root: Path) -> Path:
    """Return the repository checkout that contains *project_root*.

    When the Engine is executed from its runtime copy (``<root>/Engine``)
    ``project_root`` may already point at that directory.  To avoid staging
    into ``Engine/Engine/web-interface`` we walk up the tree until we locate
    the checkout root that contains the ``Data`` directory.
    """

    resolved = project_root.resolve()
    if (resolved / "Data").is_dir():
        return resolved

    for parent in resolved.parents:
        if (parent / "Data").is_dir():
            return parent

    return resolved


def _resolve_runtime_root(project_root: Path, repo_root: Path) -> Path:
    """Determine where the Engine should materialise staged assets."""

    # Honour explicit runtime overrides so staging aligns with the rest of
    # the Engine filesystem helpers.
    override = (
        os.getenv("BOREALIS_ENGINE_ROOT")
        or os.getenv("BOREALIS_SERVER_ROOT")
    )
    if override:
        return Path(override).expanduser().resolve()

    candidate = project_root.resolve()
    runtime_root = repo_root / "Engine"
    if candidate == runtime_root:
        return candidate

    return runtime_root


def stage_web_interface(project_root: Path) -> WebUIStagingResult:
    """Populate the Engine web-interface directory from the legacy assets.

    The Engine should serve and develop the React application from
    ``Engine/web-interface`` instead of reading directly from the legacy
    ``Data/Server/WebUI`` tree.  To keep the copy fresh we always purge the
    Engine staging directory before copying.
    """

    repo_root = _resolve_repository_root(project_root)
    runtime_root = _resolve_runtime_root(project_root, repo_root)

    source = (repo_root / "Data" / "Server" / "WebUI").resolve()
    destination = (runtime_root / "web-interface").resolve()

    destination.parent.mkdir(parents=True, exist_ok=True)

    if not source.exists():
        destination.mkdir(parents=True, exist_ok=True)
        return WebUIStagingResult(
            source=source,
            destination=destination,
            copied=False,
            reason="source-missing",
        )

    if destination.exists():
        shutil.rmtree(destination)

    shutil.copytree(source, destination)

    return WebUIStagingResult(
        source=source,
        destination=destination,
        copied=True,
    )


__all__ = ["stage_web_interface", "WebUIStagingResult"]
