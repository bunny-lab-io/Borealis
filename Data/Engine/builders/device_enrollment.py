"""Builder utilities for device enrollment payloads."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from Data.Engine.domain.device_auth import DeviceFingerprint
from Data.Engine.domain.device_enrollment import EnrollmentRequest, ProofChallenge

__all__ = [
    "EnrollmentRequestBuilder",
    "ProofChallengeBuilder",
]


@dataclass(frozen=True, slots=True)
class _EnrollmentPayload:
    hostname: str
    enrollment_code: str
    fingerprint: str
    client_nonce: bytes
    server_nonce: bytes


class EnrollmentRequestBuilder:
    """Normalize agent enrollment JSON payloads into domain objects."""

    def __init__(self) -> None:
        self._payload: Optional[_EnrollmentPayload] = None

    def with_payload(self, payload: Optional[dict[str, object]]) -> "EnrollmentRequestBuilder":
        payload = payload or {}
        hostname = str(payload.get("hostname") or "").strip()
        enrollment_code = str(payload.get("enrollment_code") or "").strip()
        fingerprint = str(payload.get("fingerprint") or "").strip()
        client_nonce = self._coerce_bytes(payload.get("client_nonce"))
        server_nonce = self._coerce_bytes(payload.get("server_nonce"))
        self._payload = _EnrollmentPayload(
            hostname=hostname,
            enrollment_code=enrollment_code,
            fingerprint=fingerprint,
            client_nonce=client_nonce,
            server_nonce=server_nonce,
        )
        return self

    def build(self) -> EnrollmentRequest:
        if not self._payload:
            raise ValueError("payload has not been provided")
        return EnrollmentRequest.from_payload(
            hostname=self._payload.hostname,
            enrollment_code=self._payload.enrollment_code,
            fingerprint=self._payload.fingerprint,
            client_nonce=self._payload.client_nonce,
            server_nonce=self._payload.server_nonce,
        )

    @staticmethod
    def _coerce_bytes(value: object) -> bytes:
        if isinstance(value, (bytes, bytearray)):
            return bytes(value)
        if isinstance(value, str):
            return value.encode("utf-8")
        raise ValueError("nonce values must be bytes or base strings")


class ProofChallengeBuilder:
    """Construct proof challenges during enrollment approval."""

    def __init__(self) -> None:
        self._server_nonce: Optional[bytes] = None
        self._client_nonce: Optional[bytes] = None
        self._fingerprint: Optional[DeviceFingerprint] = None

    def with_server_nonce(self, nonce: Optional[bytes]) -> "ProofChallengeBuilder":
        self._server_nonce = bytes(nonce or b"")
        return self

    def with_client_nonce(self, nonce: Optional[bytes]) -> "ProofChallengeBuilder":
        self._client_nonce = bytes(nonce or b"")
        return self

    def with_fingerprint(self, fingerprint: Optional[str]) -> "ProofChallengeBuilder":
        if fingerprint:
            self._fingerprint = DeviceFingerprint(fingerprint)
        else:
            self._fingerprint = None
        return self

    def build(self) -> ProofChallenge:
        if self._server_nonce is None or self._client_nonce is None:
            raise ValueError("both server and client nonces are required")
        if not self._fingerprint:
            raise ValueError("fingerprint is required")
        return ProofChallenge(
            client_nonce=self._client_nonce,
            server_nonce=self._server_nonce,
            fingerprint=self._fingerprint,
        )
