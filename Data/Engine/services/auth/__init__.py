# ======================================================
# Data\Engine\services\auth\__init__.py
# Description: Exposes shared authentication helpers for Engine REST services.
#
# API Endpoints (if applicable): None
# ======================================================

"""Authentication utilities for Borealis Engine services."""

from .context import RequestAuthContext, PermissionResult
from .dev_mode import DevModeEntry, DevModeManager
from .site_scope import UserSiteAccessManager, UserSiteScope

__all__ = [
    "RequestAuthContext",
    "PermissionResult",
    "DevModeEntry",
    "DevModeManager",
    "UserSiteAccessManager",
    "UserSiteScope",
]
