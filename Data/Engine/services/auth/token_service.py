"""Token refresh service extracted from the legacy blueprint."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Optional, Protocol
import hashlib
import logging

from Data.Engine.builders.device_auth import RefreshTokenRequest
from Data.Engine.domain.device_auth import DeviceGuid

from .device_auth_service import (
    DeviceRecord,
    DeviceRepository,
    DPoPReplayError,
    DPoPVerificationError,
    DPoPValidator,
)

__all__ = ["RefreshTokenRecord", "TokenService", "TokenRefreshError", "TokenRefreshErrorCode"]


class JWTIssuer(Protocol):
    def issue_access_token(self, guid: str, fingerprint: str, token_version: int) -> str:  # pragma: no cover - protocol
        ...


class TokenRefreshErrorCode(str):
    INVALID_REFRESH_TOKEN = "invalid_refresh_token"
    REFRESH_TOKEN_REVOKED = "refresh_token_revoked"
    REFRESH_TOKEN_EXPIRED = "refresh_token_expired"
    DEVICE_NOT_FOUND = "device_not_found"
    DEVICE_REVOKED = "device_revoked"
    DPOP_REPLAYED = "dpop_replayed"
    DPOP_INVALID = "dpop_invalid"


class TokenRefreshError(Exception):
    def __init__(self, code: str, *, http_status: int = 400) -> None:
        self.code = code
        self.http_status = http_status
        super().__init__(code)

    def to_dict(self) -> dict[str, str]:
        return {"error": self.code}


@dataclass(frozen=True, slots=True)
class RefreshTokenRecord:
    record_id: int
    guid: DeviceGuid
    token_hash: str
    dpop_jkt: Optional[str]
    created_at: datetime
    expires_at: Optional[datetime]
    revoked_at: Optional[datetime]

    @classmethod
    def from_row(
        cls,
        *,
        record_id: int,
        guid: DeviceGuid,
        token_hash: str,
        dpop_jkt: Optional[str],
        created_at: datetime,
        expires_at: Optional[datetime],
        revoked_at: Optional[datetime],
    ) -> "RefreshTokenRecord":
        return cls(
            record_id=record_id,
            guid=guid,
            token_hash=token_hash,
            dpop_jkt=dpop_jkt,
            created_at=created_at,
            expires_at=expires_at,
            revoked_at=revoked_at,
        )


class RefreshTokenRepository(Protocol):
    def fetch(self, guid: DeviceGuid, token_hash: str) -> Optional[RefreshTokenRecord]:  # pragma: no cover - protocol
        ...

    def clear_dpop_binding(self, record_id: int) -> None:  # pragma: no cover - protocol
        ...

    def touch(self, record_id: int, *, last_used_at: datetime, dpop_jkt: Optional[str]) -> None:  # pragma: no cover - protocol
        ...


@dataclass(frozen=True, slots=True)
class AccessTokenResponse:
    access_token: str
    expires_in: int
    token_type: str


class TokenService:
    def __init__(
        self,
        *,
        refresh_token_repository: RefreshTokenRepository,
        device_repository: DeviceRepository,
        jwt_service: JWTIssuer,
        dpop_validator: Optional[DPoPValidator] = None,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._refresh_tokens = refresh_token_repository
        self._devices = device_repository
        self._jwt = jwt_service
        self._dpop_validator = dpop_validator
        self._log = logger or logging.getLogger("borealis.engine.auth")

    def refresh_access_token(
        self,
        request: RefreshTokenRequest,
    ) -> AccessTokenResponse:
        record = self._refresh_tokens.fetch(
            request.guid,
            self._hash_token(request.refresh_token),
        )
        if record is None:
            raise TokenRefreshError(TokenRefreshErrorCode.INVALID_REFRESH_TOKEN, http_status=401)

        if record.guid.value != request.guid.value:
            raise TokenRefreshError(TokenRefreshErrorCode.INVALID_REFRESH_TOKEN, http_status=401)

        if record.revoked_at is not None:
            raise TokenRefreshError(TokenRefreshErrorCode.REFRESH_TOKEN_REVOKED, http_status=401)

        if record.expires_at is not None and record.expires_at <= self._now():
            raise TokenRefreshError(TokenRefreshErrorCode.REFRESH_TOKEN_EXPIRED, http_status=401)

        device = self._devices.fetch_by_guid(request.guid)
        if device is None:
            raise TokenRefreshError(TokenRefreshErrorCode.DEVICE_NOT_FOUND, http_status=404)

        if not device.status.allows_access:
            raise TokenRefreshError(TokenRefreshErrorCode.DEVICE_REVOKED, http_status=403)

        dpop_jkt = record.dpop_jkt or ""
        if request.dpop_proof:
            if self._dpop_validator is None:
                raise TokenRefreshError(TokenRefreshErrorCode.DPOP_INVALID)
            try:
                dpop_jkt = self._dpop_validator.verify(
                    request.http_method,
                    request.htu,
                    request.dpop_proof,
                    None,
                )
            except DPoPReplayError as exc:
                raise TokenRefreshError(TokenRefreshErrorCode.DPOP_REPLAYED) from exc
            except DPoPVerificationError as exc:
                raise TokenRefreshError(TokenRefreshErrorCode.DPOP_INVALID) from exc
        elif record.dpop_jkt:
            self._log.warning(
                "Clearing stored DPoP binding for guid=%s due to missing proof",
                request.guid.value,
            )
            self._refresh_tokens.clear_dpop_binding(record.record_id)

        access_token = self._jwt.issue_access_token(
            request.guid.value,
            device.identity.fingerprint.value,
            max(device.token_version, 1),
        )

        self._refresh_tokens.touch(
            record.record_id,
            last_used_at=self._now(),
            dpop_jkt=dpop_jkt or None,
        )

        return AccessTokenResponse(
            access_token=access_token,
            expires_in=900,
            token_type="Bearer",
        )

    @staticmethod
    def _hash_token(token: str) -> str:
        return hashlib.sha256(token.encode("utf-8")).hexdigest()

    @staticmethod
    def _now() -> datetime:
        return datetime.now(tz=timezone.utc)
