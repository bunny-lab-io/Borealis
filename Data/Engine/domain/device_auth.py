"""Domain primitives for device authentication and token validation."""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Any, Mapping, Optional
import string
import uuid

__all__ = [
    "DeviceGuid",
    "DeviceFingerprint",
    "DeviceIdentity",
    "DeviceStatus",
    "DeviceAuthErrorCode",
    "DeviceAuthFailure",
    "AccessTokenClaims",
    "DeviceAuthContext",
    "sanitize_service_context",
]


def _normalize_guid(value: Optional[str]) -> str:
    """Return a canonical GUID string or an empty string."""
    candidate = (value or "").strip()
    if not candidate:
        return ""
    candidate = candidate.strip("{}")
    try:
        return str(uuid.UUID(candidate)).upper()
    except Exception:
        cleaned = "".join(
            ch for ch in candidate if ch in string.hexdigits or ch == "-"
        ).strip("-")
        if cleaned:
            try:
                return str(uuid.UUID(cleaned)).upper()
            except Exception:
                pass
        return candidate.upper()


def _normalize_fingerprint(value: Optional[str]) -> str:
    return (value or "").strip().lower()


def sanitize_service_context(value: Optional[str]) -> Optional[str]:
    """Normalize the optional agent service context header value."""
    if not value:
        return None
    cleaned = "".join(
        ch for ch in str(value) if ch.isalnum() or ch in ("_", "-")
    )
    if not cleaned:
        return None
    return cleaned.upper()


@dataclass(frozen=True, slots=True)
class DeviceGuid:
    """Canonical GUID wrapper that enforces Borealis normalization."""

    value: str

    def __post_init__(self) -> None:  # pragma: no cover - simple data normalization
        normalized = _normalize_guid(self.value)
        if not normalized:
            raise ValueError("device GUID is required")
        object.__setattr__(self, "value", normalized)

    def __str__(self) -> str:
        return self.value


@dataclass(frozen=True, slots=True)
class DeviceFingerprint:
    """Normalized TLS key fingerprint associated with a device."""

    value: str

    def __post_init__(self) -> None:  # pragma: no cover - simple data normalization
        normalized = _normalize_fingerprint(self.value)
        if not normalized:
            raise ValueError("device fingerprint is required")
        object.__setattr__(self, "value", normalized)

    def __str__(self) -> str:
        return self.value


@dataclass(frozen=True, slots=True)
class DeviceIdentity:
    """Immutable pairing of device GUID and TLS key fingerprint."""

    guid: DeviceGuid
    fingerprint: DeviceFingerprint


class DeviceStatus(str, Enum):
    """Lifecycle markers mirrored from the legacy devices table."""

    ACTIVE = "active"
    QUARANTINED = "quarantined"
    REVOKED = "revoked"
    DECOMMISSIONED = "decommissioned"

    @classmethod
    def from_string(cls, value: Optional[str]) -> "DeviceStatus":
        normalized = (value or "active").strip().lower()
        try:
            return cls(normalized)
        except ValueError:
            return cls.ACTIVE

    @property
    def allows_access(self) -> bool:
        return self in {self.ACTIVE, self.QUARANTINED}


class DeviceAuthErrorCode(str, Enum):
    """Well-known authentication failure categories."""

    MISSING_AUTHORIZATION = "missing_authorization"
    TOKEN_EXPIRED = "token_expired"
    INVALID_TOKEN = "invalid_token"
    INVALID_CLAIMS = "invalid_claims"
    RATE_LIMITED = "rate_limited"
    DEVICE_NOT_FOUND = "device_not_found"
    DEVICE_GUID_MISMATCH = "device_guid_mismatch"
    FINGERPRINT_MISMATCH = "fingerprint_mismatch"
    TOKEN_VERSION_REVOKED = "token_version_revoked"
    DEVICE_REVOKED = "device_revoked"
    DPOP_NOT_SUPPORTED = "dpop_not_supported"
    DPOP_REPLAYED = "dpop_replayed"
    DPOP_INVALID = "dpop_invalid"


class DeviceAuthFailure(Exception):
    """Domain-level authentication error with HTTP metadata."""

    def __init__(
        self,
        code: DeviceAuthErrorCode,
        *,
        http_status: int = 401,
        retry_after: Optional[float] = None,
        detail: Optional[str] = None,
    ) -> None:
        self.code = code
        self.http_status = int(http_status)
        self.retry_after = retry_after
        self.detail = detail or code.value
        super().__init__(self.detail)

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {"error": self.code.value}
        if self.retry_after is not None:
            payload["retry_after"] = float(self.retry_after)
        if self.detail and self.detail != self.code.value:
            payload["detail"] = self.detail
        return payload


def _coerce_int(value: Any, *, minimum: Optional[int] = None) -> int:
    try:
        result = int(value)
    except (TypeError, ValueError):
        raise ValueError("expected integer value") from None
    if minimum is not None and result < minimum:
        raise ValueError("integer below minimum")
    return result


@dataclass(frozen=True, slots=True)
class AccessTokenClaims:
    """Validated subset of JWT claims issued to a device."""

    subject: str
    guid: DeviceGuid
    fingerprint: DeviceFingerprint
    token_version: int
    issued_at: int
    not_before: int
    expires_at: int
    raw: Mapping[str, Any]

    def __post_init__(self) -> None:
        if self.token_version <= 0:
            raise ValueError("token_version must be positive")
        if self.issued_at <= 0 or self.not_before <= 0 or self.expires_at <= 0:
            raise ValueError("temporal claims must be positive integers")
        if self.expires_at <= self.not_before:
            raise ValueError("token expiration must be after not-before")

    @classmethod
    def from_mapping(cls, claims: Mapping[str, Any]) -> "AccessTokenClaims":
        subject = str(claims.get("sub") or "").strip()
        if not subject:
            raise ValueError("missing token subject")
        guid = DeviceGuid(str(claims.get("guid") or ""))
        fingerprint = DeviceFingerprint(claims.get("ssl_key_fingerprint"))
        token_version = _coerce_int(claims.get("token_version"), minimum=1)
        issued_at = _coerce_int(claims.get("iat"), minimum=1)
        not_before = _coerce_int(claims.get("nbf"), minimum=1)
        expires_at = _coerce_int(claims.get("exp"), minimum=1)
        return cls(
            subject=subject,
            guid=guid,
            fingerprint=fingerprint,
            token_version=token_version,
            issued_at=issued_at,
            not_before=not_before,
            expires_at=expires_at,
            raw=dict(claims),
        )


@dataclass(frozen=True, slots=True)
class DeviceAuthContext:
    """Domain result emitted after successful authentication."""

    identity: DeviceIdentity
    access_token: str
    claims: AccessTokenClaims
    status: DeviceStatus
    service_context: Optional[str]
    dpop_jkt: Optional[str] = None

    def __post_init__(self) -> None:
        if not self.access_token:
            raise ValueError("access token is required")
        service = sanitize_service_context(self.service_context)
        object.__setattr__(self, "service_context", service)

    @property
    def is_quarantined(self) -> bool:
        return self.status is DeviceStatus.QUARANTINED

    @property
    def allows_access(self) -> bool:
        return self.status.allows_access
