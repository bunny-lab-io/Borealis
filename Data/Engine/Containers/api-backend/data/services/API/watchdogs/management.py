# ======================================================
# Data\Engine\services\API\watchdogs\management.py
# Description: Compatibility shim after Go watchdog runtime cutover.
#
# API Endpoints (if applicable): None
# ======================================================

"""Watchdog API compatibility shim for the Borealis Engine."""

from __future__ import annotations

from typing import TYPE_CHECKING

from flask import Flask

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def register_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Go api-backend owns watchdog routes, evaluation, and remediation runtime."""

    return None
