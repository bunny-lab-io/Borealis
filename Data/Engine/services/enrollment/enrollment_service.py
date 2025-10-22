"""Enrollment workflow orchestration for the Borealis Engine."""

from __future__ import annotations

import base64
import hashlib
import logging
import secrets
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Callable, Optional, Protocol

from cryptography.hazmat.primitives import serialization

from Data.Engine.builders.device_enrollment import EnrollmentRequestInput
from Data.Engine.domain.device_auth import (
    DeviceFingerprint,
    DeviceGuid,
    sanitize_service_context,
)
from Data.Engine.domain.device_enrollment import (
    EnrollmentApproval,
    EnrollmentApprovalStatus,
    EnrollmentCode,
)
from Data.Engine.services.auth.device_auth_service import DeviceRecord
from Data.Engine.services.auth.token_service import JWTIssuer
from Data.Engine.services.enrollment.errors import EnrollmentValidationError
from Data.Engine.services.enrollment.nonce_cache import NonceCache
from Data.Engine.services.rate_limit import SlidingWindowRateLimiter

__all__ = [
    "EnrollmentRequestResult",
    "EnrollmentService",
    "EnrollmentStatus",
    "EnrollmentTokenBundle",
    "PollingResult",
]


class DeviceRepository(Protocol):
    def fetch_by_guid(self, guid: DeviceGuid) -> Optional[DeviceRecord]:  # pragma: no cover - protocol
        ...

    def ensure_device_record(
        self,
        *,
        guid: DeviceGuid,
        hostname: str,
        fingerprint: DeviceFingerprint,
    ) -> DeviceRecord:  # pragma: no cover - protocol
        ...

    def record_device_key(
        self,
        *,
        guid: DeviceGuid,
        fingerprint: DeviceFingerprint,
        added_at: datetime,
    ) -> None:  # pragma: no cover - protocol
        ...


class EnrollmentRepository(Protocol):
    def fetch_install_code(self, code: str) -> Optional[EnrollmentCode]:  # pragma: no cover - protocol
        ...

    def fetch_install_code_by_id(self, record_id: str) -> Optional[EnrollmentCode]:  # pragma: no cover - protocol
        ...

    def update_install_code_usage(
        self,
        record_id: str,
        *,
        use_count_increment: int,
        last_used_at: datetime,
        used_by_guid: Optional[DeviceGuid] = None,
        mark_first_use: bool = False,
    ) -> None:  # pragma: no cover - protocol
        ...

    def fetch_device_approval_by_reference(self, reference: str) -> Optional[EnrollmentApproval]:  # pragma: no cover - protocol
        ...

    def fetch_pending_approval_by_fingerprint(
        self, fingerprint: DeviceFingerprint
    ) -> Optional[EnrollmentApproval]:  # pragma: no cover - protocol
        ...

    def update_pending_approval(
        self,
        record_id: str,
        *,
        hostname: str,
        guid: Optional[DeviceGuid],
        enrollment_code_id: Optional[str],
        client_nonce_b64: str,
        server_nonce_b64: str,
        agent_pubkey_der: bytes,
        updated_at: datetime,
    ) -> None:  # pragma: no cover - protocol
        ...

    def create_device_approval(
        self,
        *,
        record_id: str,
        reference: str,
        claimed_hostname: str,
        claimed_fingerprint: DeviceFingerprint,
        enrollment_code_id: Optional[str],
        client_nonce_b64: str,
        server_nonce_b64: str,
        agent_pubkey_der: bytes,
        created_at: datetime,
        status: EnrollmentApprovalStatus = EnrollmentApprovalStatus.PENDING,
        guid: Optional[DeviceGuid] = None,
    ) -> EnrollmentApproval:  # pragma: no cover - protocol
        ...

    def update_device_approval_status(
        self,
        record_id: str,
        *,
        status: EnrollmentApprovalStatus,
        updated_at: datetime,
        approved_by: Optional[str] = None,
        guid: Optional[DeviceGuid] = None,
    ) -> None:  # pragma: no cover - protocol
        ...


class RefreshTokenRepository(Protocol):
    def create(
        self,
        *,
        record_id: str,
        guid: DeviceGuid,
        token_hash: str,
        created_at: datetime,
        expires_at: Optional[datetime],
    ) -> None:  # pragma: no cover - protocol
        ...


class ScriptSigner(Protocol):
    def public_base64_spki(self) -> str:  # pragma: no cover - protocol
        ...


class EnrollmentStatus(str):
    PENDING = "pending"
    APPROVED = "approved"
    DENIED = "denied"
    EXPIRED = "expired"
    UNKNOWN = "unknown"


@dataclass(frozen=True, slots=True)
class EnrollmentTokenBundle:
    guid: DeviceGuid
    access_token: str
    refresh_token: str
    expires_in: int
    token_type: str = "Bearer"


@dataclass(frozen=True, slots=True)
class EnrollmentRequestResult:
    status: EnrollmentStatus
    approval_reference: Optional[str] = None
    server_nonce: Optional[str] = None
    poll_after_ms: Optional[int] = None
    server_certificate: str
    signing_key: str
    http_status: int = 200
    retry_after: Optional[float] = None


@dataclass(frozen=True, slots=True)
class PollingResult:
    status: EnrollmentStatus
    http_status: int
    poll_after_ms: Optional[int] = None
    reason: Optional[str] = None
    detail: Optional[str] = None
    tokens: Optional[EnrollmentTokenBundle] = None
    server_certificate: Optional[str] = None
    signing_key: Optional[str] = None


class EnrollmentService:
    """Coordinate the Borealis device enrollment handshake."""

    def __init__(
        self,
        *,
        device_repository: DeviceRepository,
        enrollment_repository: EnrollmentRepository,
        token_repository: RefreshTokenRepository,
        jwt_service: JWTIssuer,
        tls_bundle_loader: Callable[[], str],
        ip_rate_limiter: SlidingWindowRateLimiter,
        fingerprint_rate_limiter: SlidingWindowRateLimiter,
        nonce_cache: NonceCache,
        script_signer: Optional[ScriptSigner] = None,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._devices = device_repository
        self._enrollment = enrollment_repository
        self._tokens = token_repository
        self._jwt = jwt_service
        self._load_tls_bundle = tls_bundle_loader
        self._ip_rate_limiter = ip_rate_limiter
        self._fp_rate_limiter = fingerprint_rate_limiter
        self._nonce_cache = nonce_cache
        self._signer = script_signer
        self._log = logger or logging.getLogger("borealis.engine.enrollment")

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------
    def request_enrollment(
        self,
        payload: EnrollmentRequestInput,
        *,
        remote_addr: str,
    ) -> EnrollmentRequestResult:
        context_hint = sanitize_service_context(payload.service_context)
        self._log.info(
            "enrollment-request ip=%s host=%s code_mask=%s", remote_addr, payload.hostname, self._mask_code(payload.enrollment_code)
        )

        self._enforce_rate_limit(self._ip_rate_limiter, f"ip:{remote_addr}")
        self._enforce_rate_limit(self._fp_rate_limiter, f"fp:{payload.fingerprint.value}")

        install_code = self._enrollment.fetch_install_code(payload.enrollment_code)
        reuse_guid = self._determine_reuse_guid(install_code, payload.fingerprint)

        server_nonce_bytes = secrets.token_bytes(32)
        server_nonce_b64 = base64.b64encode(server_nonce_bytes).decode("ascii")

        now = self._now()
        approval = self._enrollment.fetch_pending_approval_by_fingerprint(payload.fingerprint)
        if approval:
            self._enrollment.update_pending_approval(
                approval.record_id,
                hostname=payload.hostname,
                guid=reuse_guid,
                enrollment_code_id=install_code.identifier if install_code else None,
                client_nonce_b64=payload.client_nonce_b64,
                server_nonce_b64=server_nonce_b64,
                agent_pubkey_der=payload.agent_public_key_der,
                updated_at=now,
            )
            approval_reference = approval.reference
        else:
            record_id = str(uuid.uuid4())
            approval_reference = str(uuid.uuid4())
            approval = self._enrollment.create_device_approval(
                record_id=record_id,
                reference=approval_reference,
                claimed_hostname=payload.hostname,
                claimed_fingerprint=payload.fingerprint,
                enrollment_code_id=install_code.identifier if install_code else None,
                client_nonce_b64=payload.client_nonce_b64,
                server_nonce_b64=server_nonce_b64,
                agent_pubkey_der=payload.agent_public_key_der,
                created_at=now,
                guid=reuse_guid,
            )

        signing_key = self._signer.public_base64_spki() if self._signer else ""
        certificate = self._load_tls_bundle()

        return EnrollmentRequestResult(
            status=EnrollmentStatus.PENDING,
            approval_reference=approval.reference,
            server_nonce=server_nonce_b64,
            poll_after_ms=3000,
            server_certificate=certificate,
            signing_key=signing_key,
        )

    def poll_enrollment(
        self,
        *,
        approval_reference: str,
        client_nonce_b64: str,
        proof_signature_b64: str,
    ) -> PollingResult:
        if not approval_reference:
            raise EnrollmentValidationError("approval_reference_required")
        if not client_nonce_b64:
            raise EnrollmentValidationError("client_nonce_required")
        if not proof_signature_b64:
            raise EnrollmentValidationError("proof_sig_required")

        approval = self._enrollment.fetch_device_approval_by_reference(approval_reference)
        if approval is None:
            return PollingResult(status=EnrollmentStatus.UNKNOWN, http_status=404)

        client_nonce = self._decode_base64(client_nonce_b64, "invalid_client_nonce")
        server_nonce = self._decode_base64(approval.server_nonce_b64, "server_nonce_invalid")
        proof_sig = self._decode_base64(proof_signature_b64, "invalid_proof_sig")

        if approval.client_nonce_b64 != client_nonce_b64:
            raise EnrollmentValidationError("nonce_mismatch")

        self._verify_proof_signature(
            approval=approval,
            client_nonce=client_nonce,
            server_nonce=server_nonce,
            signature=proof_sig,
        )

        status = approval.status
        if status is EnrollmentApprovalStatus.PENDING:
            return PollingResult(
                status=EnrollmentStatus.PENDING,
                http_status=200,
                poll_after_ms=5000,
            )
        if status is EnrollmentApprovalStatus.DENIED:
            return PollingResult(
                status=EnrollmentStatus.DENIED,
                http_status=200,
                reason="operator_denied",
            )
        if status is EnrollmentApprovalStatus.EXPIRED:
            return PollingResult(status=EnrollmentStatus.EXPIRED, http_status=200)
        if status is EnrollmentApprovalStatus.COMPLETED:
            return PollingResult(
                status=EnrollmentStatus.APPROVED,
                http_status=200,
                detail="finalized",
            )
        if status is not EnrollmentApprovalStatus.APPROVED:
            return PollingResult(status=EnrollmentStatus.UNKNOWN, http_status=400)

        nonce_key = f"{approval.reference}:{proof_signature_b64}"
        if not self._nonce_cache.consume(nonce_key):
            raise EnrollmentValidationError("proof_replayed", http_status=409)

        token_bundle = self._finalize_approval(approval)
        signing_key = self._signer.public_base64_spki() if self._signer else ""
        certificate = self._load_tls_bundle()

        return PollingResult(
            status=EnrollmentStatus.APPROVED,
            http_status=200,
            tokens=token_bundle,
            server_certificate=certificate,
            signing_key=signing_key,
        )

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------
    def _enforce_rate_limit(
        self,
        limiter: SlidingWindowRateLimiter,
        key: str,
        *,
        limit: int = 60,
        window_seconds: float = 60.0,
    ) -> None:
        decision = limiter.check(key, limit, window_seconds)
        if not decision.allowed:
            raise EnrollmentValidationError(
                "rate_limited", http_status=429, retry_after=max(decision.retry_after, 1.0)
            )

    def _determine_reuse_guid(
        self,
        install_code: Optional[EnrollmentCode],
        fingerprint: DeviceFingerprint,
    ) -> Optional[DeviceGuid]:
        if install_code is None:
            raise EnrollmentValidationError("invalid_enrollment_code")
        if install_code.is_expired:
            raise EnrollmentValidationError("invalid_enrollment_code")
        if not install_code.is_exhausted:
            return None
        if not install_code.used_by_guid:
            raise EnrollmentValidationError("invalid_enrollment_code")

        existing = self._devices.fetch_by_guid(install_code.used_by_guid)
        if existing and existing.identity.fingerprint.value == fingerprint.value:
            return install_code.used_by_guid
        raise EnrollmentValidationError("invalid_enrollment_code")

    def _finalize_approval(self, approval: EnrollmentApproval) -> EnrollmentTokenBundle:
        now = self._now()
        effective_guid = approval.guid or DeviceGuid(str(uuid.uuid4()))
        device_record = self._devices.ensure_device_record(
            guid=effective_guid,
            hostname=approval.claimed_hostname,
            fingerprint=approval.claimed_fingerprint,
        )
        self._devices.record_device_key(
            guid=effective_guid,
            fingerprint=approval.claimed_fingerprint,
            added_at=now,
        )

        if approval.enrollment_code_id:
            code = self._enrollment.fetch_install_code_by_id(approval.enrollment_code_id)
            if code is not None:
                mark_first = code.used_at is None
                self._enrollment.update_install_code_usage(
                    approval.enrollment_code_id,
                    use_count_increment=1,
                    last_used_at=now,
                    used_by_guid=effective_guid,
                    mark_first_use=mark_first,
                )

        refresh_token = secrets.token_urlsafe(48)
        refresh_id = str(uuid.uuid4())
        expires_at = now + timedelta(days=30)
        token_hash = hashlib.sha256(refresh_token.encode("utf-8")).hexdigest()
        self._tokens.create(
            record_id=refresh_id,
            guid=effective_guid,
            token_hash=token_hash,
            created_at=now,
            expires_at=expires_at,
        )

        access_token = self._jwt.issue_access_token(
            effective_guid.value,
            device_record.identity.fingerprint.value,
            max(device_record.token_version, 1),
        )

        self._enrollment.update_device_approval_status(
            approval.record_id,
            status=EnrollmentApprovalStatus.COMPLETED,
            updated_at=now,
            guid=effective_guid,
        )

        return EnrollmentTokenBundle(
            guid=effective_guid,
            access_token=access_token,
            refresh_token=refresh_token,
            expires_in=900,
        )

    def _verify_proof_signature(
        self,
        *,
        approval: EnrollmentApproval,
        client_nonce: bytes,
        server_nonce: bytes,
        signature: bytes,
    ) -> None:
        message = server_nonce + approval.reference.encode("utf-8") + client_nonce
        try:
            public_key = serialization.load_der_public_key(approval.agent_pubkey_der)
        except Exception as exc:
            raise EnrollmentValidationError("agent_pubkey_invalid") from exc

        try:
            public_key.verify(signature, message)
        except Exception as exc:
            raise EnrollmentValidationError("invalid_proof") from exc

    @staticmethod
    def _decode_base64(value: str, error_code: str) -> bytes:
        try:
            return base64.b64decode(value, validate=True)
        except Exception as exc:
            raise EnrollmentValidationError(error_code) from exc

    @staticmethod
    def _mask_code(code: str) -> str:
        trimmed = (code or "").strip()
        if len(trimmed) <= 6:
            return "***"
        return f"{trimmed[:3]}***{trimmed[-3:]}"

    @staticmethod
    def _now() -> datetime:
        return datetime.now(tz=timezone.utc)
