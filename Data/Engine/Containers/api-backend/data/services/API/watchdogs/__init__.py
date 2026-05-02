# ======================================================
# Data\Engine\services\API\watchdogs\__init__.py
# Description: Watchdog policy and incident API package exports.
#
# API Endpoints (if applicable): None
# ======================================================

"""Watchdog API exports for the Borealis Engine."""

from .management import ensure_watchdog_runtime, register_management

__all__ = ["ensure_watchdog_runtime", "register_management"]
