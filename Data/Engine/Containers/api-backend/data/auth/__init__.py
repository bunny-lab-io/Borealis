# ======================================================
# Data\Engine\auth\__init__.py
# Description: Engine-native authentication utilities and helpers decoupled from the legacy server modules.
#
# API Endpoints (if applicable): None
# ======================================================

"""Authentication utility package for the Borealis Engine."""

from .jwt_service import JWTService, load_service
from .dpop import DPoPValidator, DPoPVerificationError, DPoPReplayError
from .rate_limit import SlidingWindowRateLimiter, RateLimitDecision
from .device_auth import DeviceAuthManager, DeviceAuthError, DeviceAuthContext, require_device_auth

__all__ = [
    "JWTService",
    "load_service",
    "DPoPValidator",
    "DPoPVerificationError",
    "DPoPReplayError",
    "SlidingWindowRateLimiter",
    "RateLimitDecision",
    "DeviceAuthManager",
    "DeviceAuthError",
    "DeviceAuthContext",
    "require_device_auth",
]
