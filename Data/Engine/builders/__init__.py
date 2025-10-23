"""Builder utilities for constructing immutable Engine aggregates."""

from __future__ import annotations

from .device_auth import (
    DeviceAuthRequest,
    DeviceAuthRequestBuilder,
    RefreshTokenRequest,
    RefreshTokenRequestBuilder,
)
from .operator_auth import (
    OperatorLoginRequest,
    OperatorMFAVerificationRequest,
    build_login_request,
    build_mfa_request,
)

__all__ = [
    "DeviceAuthRequest",
    "DeviceAuthRequestBuilder",
    "RefreshTokenRequest",
    "RefreshTokenRequestBuilder",
    "OperatorLoginRequest",
    "OperatorMFAVerificationRequest",
    "build_login_request",
    "build_mfa_request",
]

try:  # pragma: no cover - optional dependency shim
    from .device_enrollment import (
        EnrollmentRequestBuilder,
        ProofChallengeBuilder,
    )
except ModuleNotFoundError as exc:  # pragma: no cover - executed when crypto deps missing
    _missing_reason = str(exc)

    def _missing_builder(*_args: object, **_kwargs: object) -> None:
        raise ModuleNotFoundError(
            "device enrollment builders require optional cryptography dependencies"
        ) from exc

    EnrollmentRequestBuilder = _missing_builder  # type: ignore[assignment]
    ProofChallengeBuilder = _missing_builder  # type: ignore[assignment]
else:
    __all__ += ["EnrollmentRequestBuilder", "ProofChallengeBuilder"]
