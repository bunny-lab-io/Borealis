"""Builder utilities for device enrollment payloads."""

from __future__ import annotations

import base64
from dataclasses import dataclass
from typing import Optional

from Data.Engine.domain.device_auth import DeviceFingerprint, sanitize_service_context
from Data.Engine.domain.device_enrollment import ProofChallenge
from Data.Engine.integrations.crypto import keys as crypto_keys
from Data.Engine.services.enrollment.errors import EnrollmentValidationError

__all__ = [
    "EnrollmentRequestBuilder",
    "EnrollmentRequestInput",
    "ProofChallengeBuilder",
]


@dataclass(frozen=True, slots=True)
class EnrollmentRequestInput:
    """Structured enrollment request payload ready for the service layer."""

    hostname: str
    enrollment_code: str
    fingerprint: DeviceFingerprint
    client_nonce: bytes
    client_nonce_b64: str
    agent_public_key_der: bytes
    service_context: Optional[str]


class EnrollmentRequestBuilder:
    """Normalize agent enrollment JSON payloads into domain objects."""

    def __init__(self) -> None:
        self._hostname: Optional[str] = None
        self._enrollment_code: Optional[str] = None
        self._agent_pubkey_b64: Optional[str] = None
        self._client_nonce_b64: Optional[str] = None
        self._service_context: Optional[str] = None

    def with_payload(self, payload: Optional[dict[str, object]]) -> "EnrollmentRequestBuilder":
        payload = payload or {}
        self._hostname = str(payload.get("hostname") or "").strip()
        self._enrollment_code = str(payload.get("enrollment_code") or "").strip()
        agent_pubkey = payload.get("agent_pubkey")
        self._agent_pubkey_b64 = agent_pubkey if isinstance(agent_pubkey, str) else None
        client_nonce = payload.get("client_nonce")
        self._client_nonce_b64 = client_nonce if isinstance(client_nonce, str) else None
        return self

    def with_service_context(self, value: Optional[str]) -> "EnrollmentRequestBuilder":
        self._service_context = value
        return self

    def build(self) -> EnrollmentRequestInput:
        if not self._hostname:
            raise EnrollmentValidationError("hostname_required")
        if not self._enrollment_code:
            raise EnrollmentValidationError("enrollment_code_required")
        if not self._agent_pubkey_b64:
            raise EnrollmentValidationError("agent_pubkey_required")
        if not self._client_nonce_b64:
            raise EnrollmentValidationError("client_nonce_required")

        try:
            agent_pubkey_der = crypto_keys.spki_der_from_base64(self._agent_pubkey_b64)
        except Exception as exc:  # pragma: no cover - invalid input path
            raise EnrollmentValidationError("invalid_agent_pubkey") from exc

        if len(agent_pubkey_der) < 10:
            raise EnrollmentValidationError("invalid_agent_pubkey")

        try:
            client_nonce_bytes = base64.b64decode(self._client_nonce_b64, validate=True)
        except Exception as exc:  # pragma: no cover - invalid input path
            raise EnrollmentValidationError("invalid_client_nonce") from exc

        if len(client_nonce_bytes) < 16:
            raise EnrollmentValidationError("invalid_client_nonce")

        fingerprint_value = crypto_keys.fingerprint_from_spki_der(agent_pubkey_der)

        return EnrollmentRequestInput(
            hostname=self._hostname,
            enrollment_code=self._enrollment_code,
            fingerprint=DeviceFingerprint(fingerprint_value),
            client_nonce=client_nonce_bytes,
            client_nonce_b64=self._client_nonce_b64,
            agent_public_key_der=agent_pubkey_der,
            service_context=sanitize_service_context(self._service_context),
        )


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
