"""Helpers for staging the Engine web interface assets."""

from __future__ import annotations

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


def stage_web_interface(project_root: Path) -> WebUIStagingResult:
    """Populate the Engine web-interface directory from the legacy assets.

    The Engine should serve and develop the React application from
    ``Engine/web-interface`` instead of reading directly from the legacy
    ``Data/Server/WebUI`` tree.  To keep the copy fresh we always purge the
    Engine staging directory before copying.
    """

    source = (project_root / "Data" / "Server" / "WebUI").resolve()
    destination = (project_root / "Engine" / "web-interface").resolve()

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
