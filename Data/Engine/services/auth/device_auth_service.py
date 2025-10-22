"""Device authentication service copied from the legacy server stack."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Mapping, Optional, Protocol
import logging

from Data.Engine.builders.device_auth import DeviceAuthRequest
from Data.Engine.domain.device_auth import (
    AccessTokenClaims,
    DeviceAuthContext,
    DeviceAuthErrorCode,
    DeviceAuthFailure,
    DeviceFingerprint,
    DeviceGuid,
    DeviceIdentity,
    DeviceStatus,
)

__all__ = [
    "DeviceAuthService",
    "DeviceRecord",
    "DPoPValidator",
    "DPoPVerificationError",
    "DPoPReplayError",
    "RateLimiter",
    "RateLimitDecision",
    "DeviceRepository",
]


class RateLimitDecision(Protocol):
    allowed: bool
    retry_after: Optional[float]


class RateLimiter(Protocol):
    def check(self, key: str, max_requests: int, window_seconds: float) -> RateLimitDecision:  # pragma: no cover - protocol
        ...


class JWTDecoder(Protocol):
    def decode(self, token: str) -> Mapping[str, object]:  # pragma: no cover - protocol
        ...


class DPoPValidator(Protocol):
    def verify(
        self,
        method: str,
        htu: str,
        proof: str,
        access_token: Optional[str] = None,
    ) -> str:  # pragma: no cover - protocol
        ...


class DPoPVerificationError(Exception):
    """Raised when a DPoP proof fails validation."""


class DPoPReplayError(DPoPVerificationError):
    """Raised when a DPoP proof is replayed."""


@dataclass(frozen=True, slots=True)
class DeviceRecord:
    """Snapshot of a device record required for authentication."""

    identity: DeviceIdentity
    token_version: int
    status: DeviceStatus


class DeviceRepository(Protocol):
    """Port that exposes the minimal device persistence operations."""

    def fetch_by_guid(self, guid: DeviceGuid) -> Optional[DeviceRecord]:  # pragma: no cover - protocol
        ...

    def recover_missing(
        self,
        guid: DeviceGuid,
        fingerprint: DeviceFingerprint,
        token_version: int,
        service_context: Optional[str],
    ) -> Optional[DeviceRecord]:  # pragma: no cover - protocol
        ...


class DeviceAuthService:
    """Authenticate devices using access tokens, repositories, and DPoP proofs."""

    def __init__(
        self,
        *,
        device_repository: DeviceRepository,
        jwt_service: JWTDecoder,
        logger: Optional[logging.Logger] = None,
        rate_limiter: Optional[RateLimiter] = None,
        dpop_validator: Optional[DPoPValidator] = None,
    ) -> None:
        self._repository = device_repository
        self._jwt = jwt_service
        self._log = logger or logging.getLogger("borealis.engine.auth")
        self._rate_limiter = rate_limiter
        self._dpop_validator = dpop_validator

    def authenticate(self, request: DeviceAuthRequest, *, path: str) -> DeviceAuthContext:
        """Authenticate an access token and return the resulting context."""

        claims = self._decode_claims(request.access_token)
        rate_limit_key = f"fp:{claims.fingerprint.value}"
        if self._rate_limiter is not None:
            decision = self._rate_limiter.check(rate_limit_key, 60, 60.0)
            if not decision.allowed:
                raise DeviceAuthFailure(
                    DeviceAuthErrorCode.RATE_LIMITED,
                    http_status=429,
                    retry_after=decision.retry_after,
                )

        record = self._repository.fetch_by_guid(claims.guid)
        if record is None:
            record = self._repository.recover_missing(
                claims.guid,
                claims.fingerprint,
                claims.token_version,
                request.service_context,
            )

        if record is None:
            raise DeviceAuthFailure(
                DeviceAuthErrorCode.DEVICE_NOT_FOUND,
                http_status=403,
            )

        self._validate_identity(record, claims)

        dpop_jkt = self._validate_dpop(request, record, claims)

        context = DeviceAuthContext(
            identity=record.identity,
            access_token=request.access_token,
            claims=claims,
            status=record.status,
            service_context=request.service_context,
            dpop_jkt=dpop_jkt,
        )

        if context.is_quarantined:
            self._log.warning(
                "device %s is quarantined; limited access for %s",
                record.identity.guid,
                path,
            )

        return context

    def _decode_claims(self, token: str) -> AccessTokenClaims:
        try:
            raw_claims = self._jwt.decode(token)
        except Exception as exc:  # pragma: no cover - defensive fallback
            if self._is_expired_signature(exc):
                raise DeviceAuthFailure(DeviceAuthErrorCode.TOKEN_EXPIRED) from exc
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_TOKEN) from exc

        try:
            return AccessTokenClaims.from_mapping(raw_claims)
        except Exception as exc:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_CLAIMS) from exc

    @staticmethod
    def _is_expired_signature(exc: Exception) -> bool:
        name = exc.__class__.__name__
        return name == "ExpiredSignatureError"

    def _validate_identity(
        self,
        record: DeviceRecord,
        claims: AccessTokenClaims,
    ) -> None:
        if record.identity.guid.value != claims.guid.value:
            raise DeviceAuthFailure(
                DeviceAuthErrorCode.DEVICE_GUID_MISMATCH,
                http_status=403,
            )

        if record.identity.fingerprint.value:
            if record.identity.fingerprint.value != claims.fingerprint.value:
                raise DeviceAuthFailure(
                    DeviceAuthErrorCode.FINGERPRINT_MISMATCH,
                    http_status=403,
                )

        if record.token_version > claims.token_version:
            raise DeviceAuthFailure(DeviceAuthErrorCode.TOKEN_VERSION_REVOKED)

        if not record.status.allows_access:
            raise DeviceAuthFailure(
                DeviceAuthErrorCode.DEVICE_REVOKED,
                http_status=403,
            )

    def _validate_dpop(
        self,
        request: DeviceAuthRequest,
        record: DeviceRecord,
        claims: AccessTokenClaims,
    ) -> Optional[str]:
        if not request.dpop_proof:
            return None

        if self._dpop_validator is None:
            raise DeviceAuthFailure(
                DeviceAuthErrorCode.DPOP_NOT_SUPPORTED,
                http_status=400,
            )

        try:
            return self._dpop_validator.verify(
                request.http_method,
                request.htu,
                request.dpop_proof,
                request.access_token,
            )
        except DPoPReplayError as exc:
            raise DeviceAuthFailure(
                DeviceAuthErrorCode.DPOP_REPLAYED,
                http_status=400,
            ) from exc
        except DPoPVerificationError as exc:
            raise DeviceAuthFailure(
                DeviceAuthErrorCode.DPOP_INVALID,
                http_status=400,
            ) from exc
