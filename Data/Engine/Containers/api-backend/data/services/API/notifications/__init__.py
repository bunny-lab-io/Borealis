# ======================================================
# Data\Engine\services\API\notifications\__init__.py
# Description: Package init for notification API endpoints.
#
# API Endpoints (if applicable): None
# ======================================================

"""Notification API package for the Borealis Engine."""

from .management import register_notifications

__all__ = ["register_notifications"]
