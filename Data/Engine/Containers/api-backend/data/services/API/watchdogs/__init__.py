# ======================================================
# Data\Engine\services\API\watchdogs\__init__.py
# Description: Watchdog policy and incident API package exports.
#
# API Endpoints (if applicable): None
# ======================================================

"""Watchdog API compatibility exports for the Borealis Engine."""

from .management import register_management

__all__ = ["register_management"]
