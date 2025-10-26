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


def stage_web_interface(repository_root: Path, runtime_root: Path) -> WebUIStagingResult:
    """Populate the Engine web-interface directory from the legacy assets."""

    # The Engine runtime must never read directly from the legacy WebUI source.
    # A fresh copy is staged under ``<runtime_root>/web-interface`` each time
    # the Engine boots so that both the production Flask server and the Vite
    # development server operate on the same runtime tree.

    source = (repository_root / "Data" / "Server" / "WebUI").resolve()
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
