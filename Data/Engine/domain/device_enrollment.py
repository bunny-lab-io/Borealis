"""Domain types describing device enrollment flows."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
import base64
from enum import Enum
from typing import Any, Mapping, Optional

from .device_auth import DeviceFingerprint, DeviceGuid, sanitize_service_context

__all__ = [
    "EnrollmentCode",
    "EnrollmentApprovalStatus",
    "EnrollmentApproval",
    "EnrollmentRequest",
    "ProofChallenge",
]


def _parse_iso8601(value: Optional[str]) -> Optional[datetime]:
    if not value:
        return None
    raw = str(value).strip()
    if not raw:
        return None
    try:
        dt = datetime.fromisoformat(raw)
    except Exception as exc:  # pragma: no cover - error path
        raise ValueError(f"invalid ISO8601 timestamp: {raw}") from exc
    if dt.tzinfo is None:
        return dt.replace(tzinfo=timezone.utc)
    return dt


def _require(value: Optional[str], field: str) -> str:
    text = (value or "").strip()
    if not text:
        raise ValueError(f"{field} is required")
    return text


@dataclass(frozen=True, slots=True)
class EnrollmentCode:
    """Installer code metadata loaded from the persistence layer."""

    code: str
    expires_at: datetime
    max_uses: int
    use_count: int
    used_by_guid: Optional[DeviceGuid]
    last_used_at: Optional[datetime]
    used_at: Optional[datetime]
    record_id: Optional[str] = None

    def __post_init__(self) -> None:
        if not self.code:
            raise ValueError("code is required")
        if self.max_uses < 1:
            raise ValueError("max_uses must be >= 1")
        if self.use_count < 0:
            raise ValueError("use_count cannot be negative")
        if self.use_count > self.max_uses:
            raise ValueError("use_count cannot exceed max_uses")

    @classmethod
    def from_mapping(cls, record: Mapping[str, Any]) -> "EnrollmentCode":
        used_by = record.get("used_by_guid")
        used_by_guid = DeviceGuid(used_by) if used_by else None
        return cls(
            code=_require(record.get("code"), "code"),
            expires_at=_parse_iso8601(record.get("expires_at")) or datetime.now(tz=timezone.utc),
            max_uses=int(record.get("max_uses") or 1),
            use_count=int(record.get("use_count") or 0),
            used_by_guid=used_by_guid,
            last_used_at=_parse_iso8601(record.get("last_used_at")),
            used_at=_parse_iso8601(record.get("used_at")),
            record_id=str(record.get("id") or "") or None,
        )

    @property
    def remaining_uses(self) -> int:
        return max(self.max_uses - self.use_count, 0)

    @property
    def is_exhausted(self) -> bool:
        return self.remaining_uses == 0

    @property
    def is_expired(self) -> bool:
        return self.expires_at <= datetime.now(tz=timezone.utc)

    @property
    def identifier(self) -> Optional[str]:
        return self.record_id


class EnrollmentApprovalStatus(str, Enum):
    """Possible states for a device approval entry."""

    PENDING = "pending"
    APPROVED = "approved"
    DENIED = "denied"
    COMPLETED = "completed"
    EXPIRED = "expired"

    @classmethod
    def from_string(cls, value: Optional[str]) -> "EnrollmentApprovalStatus":
        normalized = (value or "pending").strip().lower()
        try:
            return cls(normalized)
        except ValueError:
            return cls.PENDING

    @property
    def is_terminal(self) -> bool:
        return self in {self.APPROVED, self.DENIED, self.COMPLETED, self.EXPIRED}


@dataclass(frozen=True, slots=True)
class ProofChallenge:
    """Client/server nonce pair distributed during enrollment."""

    client_nonce: bytes
    server_nonce: bytes

    def __post_init__(self) -> None:
        if not self.client_nonce or not self.server_nonce:
            raise ValueError("nonce payloads must be non-empty")

    @classmethod
    def from_base64(cls, *, client: bytes, server: bytes) -> "ProofChallenge":
        return cls(client_nonce=client, server_nonce=server)


@dataclass(frozen=True, slots=True)
class EnrollmentRequest:
    """Validated payload submitted by an agent during enrollment."""

    hostname: str
    enrollment_code: str
    fingerprint: DeviceFingerprint
    proof: ProofChallenge
    service_context: Optional[str]

    def __post_init__(self) -> None:
        if not self.hostname:
            raise ValueError("hostname is required")
        if not self.enrollment_code:
            raise ValueError("enrollment code is required")
        object.__setattr__(
            self,
            "service_context",
            sanitize_service_context(self.service_context),
        )

    @classmethod
    def from_payload(
        cls,
        *,
        hostname: str,
        enrollment_code: str,
        fingerprint: str,
        client_nonce: bytes,
        server_nonce: bytes,
        service_context: Optional[str] = None,
    ) -> "EnrollmentRequest":
        proof = ProofChallenge(client_nonce=client_nonce, server_nonce=server_nonce)
        return cls(
            hostname=_require(hostname, "hostname"),
            enrollment_code=_require(enrollment_code, "enrollment_code"),
            fingerprint=DeviceFingerprint(fingerprint),
            proof=proof,
            service_context=service_context,
        )


@dataclass(frozen=True, slots=True)
class EnrollmentApproval:
    """Pending or resolved approval tracked by operators."""

    record_id: str
    reference: str
    status: EnrollmentApprovalStatus
    claimed_hostname: str
    claimed_fingerprint: DeviceFingerprint
    enrollment_code_id: Optional[str]
    created_at: datetime
    updated_at: datetime
    client_nonce_b64: str
    server_nonce_b64: str
    agent_pubkey_der: bytes
    guid: Optional[DeviceGuid] = None
    approved_by: Optional[str] = None

    def __post_init__(self) -> None:
        if not self.record_id:
            raise ValueError("record identifier is required")
        if not self.reference:
            raise ValueError("approval reference is required")
        if not self.claimed_hostname:
            raise ValueError("claimed hostname is required")

    @classmethod
    def from_mapping(cls, record: Mapping[str, Any]) -> "EnrollmentApproval":
        guid_raw = record.get("guid")
        approved_raw = record.get("approved_by_user_id")
        return cls(
            record_id=_require(record.get("id"), "id"),
            reference=_require(record.get("approval_reference"), "approval_reference"),
            status=EnrollmentApprovalStatus.from_string(record.get("status")),
            claimed_hostname=_require(record.get("hostname_claimed"), "hostname_claimed"),
            claimed_fingerprint=DeviceFingerprint(record.get("ssl_key_fingerprint_claimed")),
            enrollment_code_id=record.get("enrollment_code_id"),
            created_at=_parse_iso8601(record.get("created_at")) or datetime.now(tz=timezone.utc),
            updated_at=_parse_iso8601(record.get("updated_at")) or datetime.now(tz=timezone.utc),
            guid=DeviceGuid(guid_raw) if guid_raw else None,
            approved_by=(approved_raw or None),
            client_nonce_b64=_require(record.get("client_nonce"), "client_nonce"),
            server_nonce_b64=_require(record.get("server_nonce"), "server_nonce"),
            agent_pubkey_der=bytes(record.get("agent_pubkey_der") or b""),
        )

    @property
    def is_pending(self) -> bool:
        return self.status is EnrollmentApprovalStatus.PENDING

    @property
    def is_completed(self) -> bool:
        return self.status in {
            EnrollmentApprovalStatus.APPROVED,
            EnrollmentApprovalStatus.COMPLETED,
        }

    @property
    def client_nonce_bytes(self) -> bytes:
        return base64.b64decode(self.client_nonce_b64.encode("ascii"), validate=True)

    @property
    def server_nonce_bytes(self) -> bytes:
        return base64.b64decode(self.server_nonce_b64.encode("ascii"), validate=True)
