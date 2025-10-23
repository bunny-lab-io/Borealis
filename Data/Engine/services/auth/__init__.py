"""Authentication services for the Borealis Engine."""

from __future__ import annotations

from .device_auth_service import DeviceAuthService, DeviceRecord
from .dpop import DPoPReplayError, DPoPVerificationError, DPoPValidator
from .jwt_service import JWTService, load_service as load_jwt_service
from .token_service import (
    RefreshTokenRecord,
    TokenRefreshError,
    TokenRefreshErrorCode,
    TokenService,
)
from .operator_auth_service import (
    InvalidCredentialsError,
    InvalidMFACodeError,
    MFAUnavailableError,
    MFASessionError,
    OperatorAuthError,
    OperatorAuthService,
)

__all__ = [
    "DeviceAuthService",
    "DeviceRecord",
    "DPoPReplayError",
    "DPoPVerificationError",
    "DPoPValidator",
    "JWTService",
    "load_jwt_service",
    "RefreshTokenRecord",
    "TokenRefreshError",
    "TokenRefreshErrorCode",
    "TokenService",
    "OperatorAuthService",
    "OperatorAuthError",
    "InvalidCredentialsError",
    "InvalidMFACodeError",
    "MFAUnavailableError",
    "MFASessionError",
]
