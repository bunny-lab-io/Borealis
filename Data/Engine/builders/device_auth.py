"""Builders for device authentication and token refresh inputs."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from Data.Engine.domain.device_auth import (
    DeviceAuthErrorCode,
    DeviceAuthFailure,
    DeviceGuid,
    sanitize_service_context,
)

__all__ = [
    "DeviceAuthRequest",
    "DeviceAuthRequestBuilder",
    "RefreshTokenRequest",
    "RefreshTokenRequestBuilder",
]


@dataclass(frozen=True, slots=True)
class DeviceAuthRequest:
    """Normalized authentication inputs derived from an HTTP request."""

    access_token: str
    http_method: str
    htu: str
    service_context: Optional[str]
    dpop_proof: Optional[str]


class DeviceAuthRequestBuilder:
    """Validate and normalize HTTP headers for device authentication."""

    _authorization: Optional[str]
    _http_method: Optional[str]
    _htu: Optional[str]
    _service_context: Optional[str]
    _dpop_proof: Optional[str]

    def __init__(self) -> None:
        self._authorization = None
        self._http_method = None
        self._htu = None
        self._service_context = None
        self._dpop_proof = None

    def with_authorization(self, header_value: Optional[str]) -> "DeviceAuthRequestBuilder":
        if header_value is None:
            self._authorization = None
        else:
            self._authorization = header_value.strip()
        return self

    def with_http_method(self, method: Optional[str]) -> "DeviceAuthRequestBuilder":
        self._http_method = (method or "").strip().upper()
        return self

    def with_htu(self, url: Optional[str]) -> "DeviceAuthRequestBuilder":
        self._htu = (url or "").strip()
        return self

    def with_service_context(self, header_value: Optional[str]) -> "DeviceAuthRequestBuilder":
        self._service_context = sanitize_service_context(header_value)
        return self

    def with_dpop_proof(self, proof: Optional[str]) -> "DeviceAuthRequestBuilder":
        self._dpop_proof = (proof or "").strip() or None
        return self

    def build(self) -> DeviceAuthRequest:
        token = self._parse_authorization(self._authorization)
        method = (self._http_method or "").strip().upper()
        if not method:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_TOKEN, detail="missing HTTP method")
        url = (self._htu or "").strip()
        if not url:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_TOKEN, detail="missing request URL")
        return DeviceAuthRequest(
            access_token=token,
            http_method=method,
            htu=url,
            service_context=self._service_context,
            dpop_proof=self._dpop_proof,
        )

    @staticmethod
    def _parse_authorization(header_value: Optional[str]) -> str:
        header = (header_value or "").strip()
        if not header:
            raise DeviceAuthFailure(DeviceAuthErrorCode.MISSING_AUTHORIZATION)
        prefix = "Bearer "
        if not header.startswith(prefix):
            raise DeviceAuthFailure(DeviceAuthErrorCode.MISSING_AUTHORIZATION)
        token = header[len(prefix) :].strip()
        if not token:
            raise DeviceAuthFailure(DeviceAuthErrorCode.MISSING_AUTHORIZATION)
        return token


@dataclass(frozen=True, slots=True)
class RefreshTokenRequest:
    """Validated refresh token payload supplied by an agent."""

    guid: DeviceGuid
    refresh_token: str
    http_method: str
    htu: str
    dpop_proof: Optional[str]


class RefreshTokenRequestBuilder:
    """Helper to normalize refresh token JSON payloads."""

    _guid: Optional[str]
    _refresh_token: Optional[str]
    _http_method: Optional[str]
    _htu: Optional[str]
    _dpop_proof: Optional[str]

    def __init__(self) -> None:
        self._guid = None
        self._refresh_token = None
        self._http_method = None
        self._htu = None
        self._dpop_proof = None

    def with_payload(self, payload: Optional[dict[str, object]]) -> "RefreshTokenRequestBuilder":
        payload = payload or {}
        self._guid = str(payload.get("guid") or "").strip()
        self._refresh_token = str(payload.get("refresh_token") or "").strip()
        return self

    def with_http_method(self, method: Optional[str]) -> "RefreshTokenRequestBuilder":
        self._http_method = (method or "").strip().upper()
        return self

    def with_htu(self, url: Optional[str]) -> "RefreshTokenRequestBuilder":
        self._htu = (url or "").strip()
        return self

    def with_dpop_proof(self, proof: Optional[str]) -> "RefreshTokenRequestBuilder":
        self._dpop_proof = (proof or "").strip() or None
        return self

    def build(self) -> RefreshTokenRequest:
        if not self._guid:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_CLAIMS, detail="missing guid")
        if not self._refresh_token:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_CLAIMS, detail="missing refresh token")
        method = (self._http_method or "").strip().upper()
        if not method:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_TOKEN, detail="missing HTTP method")
        url = (self._htu or "").strip()
        if not url:
            raise DeviceAuthFailure(DeviceAuthErrorCode.INVALID_TOKEN, detail="missing request URL")
        return RefreshTokenRequest(
            guid=DeviceGuid(self._guid),
            refresh_token=self._refresh_token,
            http_method=method,
            htu=url,
            dpop_proof=self._dpop_proof,
        )
