# ======================================================
# Data\Engine\services\API\enrollment\__init__.py
# Description: Engine enrollment API registration helpers.
#
# API Endpoints (if applicable): None
# ======================================================

"""Expose Engine-native enrollment API routes."""

from .routes import register

__all__ = ["register"]
