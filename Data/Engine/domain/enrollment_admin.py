"""Administrative enrollment domain models."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Mapping, Optional

from Data.Engine.domain.device_auth import DeviceGuid, normalize_guid

__all__ = [
    "EnrollmentCodeRecord",
    "DeviceApprovalRecord",
    "HostnameConflict",
]


def _parse_iso8601(value: Optional[str]) -> Optional[datetime]:
    if not value:
        return None
    raw = str(value).strip()
    if not raw:
        return None
    try:
        dt = datetime.fromisoformat(raw)
    except Exception as exc:  # pragma: no cover - defensive parsing
        raise ValueError(f"invalid ISO8601 timestamp: {raw}") from exc
    if dt.tzinfo is None:
        return dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def _isoformat(value: Optional[datetime]) -> Optional[str]:
    if value is None:
        return None
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).isoformat()


@dataclass(frozen=True, slots=True)
class EnrollmentCodeRecord:
    """Installer code metadata exposed to administrative clients."""

    record_id: str
    code: str
    expires_at: datetime
    max_uses: int
    use_count: int
    created_by_user_id: Optional[str]
    used_at: Optional[datetime]
    used_by_guid: Optional[DeviceGuid]
    last_used_at: Optional[datetime]

    @classmethod
    def from_row(cls, row: Mapping[str, Any]) -> "EnrollmentCodeRecord":
        record_id = str(row.get("id") or "").strip()
        code = str(row.get("code") or "").strip()
        if not record_id or not code:
            raise ValueError("invalid enrollment install code record")

        used_by = row.get("used_by_guid")
        used_by_guid = DeviceGuid(str(used_by)) if used_by else None

        return cls(
            record_id=record_id,
            code=code,
            expires_at=_parse_iso8601(row.get("expires_at")) or datetime.now(tz=timezone.utc),
            max_uses=int(row.get("max_uses") or 1),
            use_count=int(row.get("use_count") or 0),
            created_by_user_id=str(row.get("created_by_user_id") or "").strip() or None,
            used_at=_parse_iso8601(row.get("used_at")),
            used_by_guid=used_by_guid,
            last_used_at=_parse_iso8601(row.get("last_used_at")),
        )

    def status(self, *, now: Optional[datetime] = None) -> str:
        reference = now or datetime.now(tz=timezone.utc)
        if self.use_count >= self.max_uses:
            return "used"
        if self.expires_at <= reference:
            return "expired"
        return "active"

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.record_id,
            "code": self.code,
            "expires_at": _isoformat(self.expires_at),
            "max_uses": self.max_uses,
            "use_count": self.use_count,
            "created_by_user_id": self.created_by_user_id,
            "used_at": _isoformat(self.used_at),
            "used_by_guid": self.used_by_guid.value if self.used_by_guid else None,
            "last_used_at": _isoformat(self.last_used_at),
            "status": self.status(),
        }


@dataclass(frozen=True, slots=True)
class HostnameConflict:
    """Existing device details colliding with a pending approval."""

    guid: Optional[str]
    ssl_key_fingerprint: Optional[str]
    site_id: Optional[int]
    site_name: str
    fingerprint_match: bool
    requires_prompt: bool

    def to_dict(self) -> dict[str, Any]:
        return {
            "guid": self.guid,
            "ssl_key_fingerprint": self.ssl_key_fingerprint,
            "site_id": self.site_id,
            "site_name": self.site_name,
            "fingerprint_match": self.fingerprint_match,
            "requires_prompt": self.requires_prompt,
        }


@dataclass(frozen=True, slots=True)
class DeviceApprovalRecord:
    """Administrative projection of a device approval entry."""

    record_id: str
    reference: str
    status: str
    claimed_hostname: str
    claimed_fingerprint: str
    created_at: datetime
    updated_at: datetime
    enrollment_code_id: Optional[str]
    guid: Optional[str]
    approved_by_user_id: Optional[str]
    approved_by_username: Optional[str]
    client_nonce: str
    server_nonce: str
    hostname_conflict: Optional[HostnameConflict]
    alternate_hostname: Optional[str]
    conflict_requires_prompt: bool
    fingerprint_match: bool

    @classmethod
    def from_row(
        cls,
        row: Mapping[str, Any],
        *,
        conflict: Optional[HostnameConflict] = None,
        alternate_hostname: Optional[str] = None,
        fingerprint_match: bool = False,
        requires_prompt: bool = False,
    ) -> "DeviceApprovalRecord":
        record_id = str(row.get("id") or "").strip()
        reference = str(row.get("approval_reference") or "").strip()
        hostname = str(row.get("hostname_claimed") or "").strip()
        fingerprint = str(row.get("ssl_key_fingerprint_claimed") or "").strip().lower()
        if not record_id or not reference or not hostname or not fingerprint:
            raise ValueError("invalid device approval record")

        guid_raw = normalize_guid(row.get("guid")) or None

        return cls(
            record_id=record_id,
            reference=reference,
            status=str(row.get("status") or "pending").strip().lower(),
            claimed_hostname=hostname,
            claimed_fingerprint=fingerprint,
            created_at=_parse_iso8601(row.get("created_at")) or datetime.now(tz=timezone.utc),
            updated_at=_parse_iso8601(row.get("updated_at")) or datetime.now(tz=timezone.utc),
            enrollment_code_id=str(row.get("enrollment_code_id") or "").strip() or None,
            guid=guid_raw,
            approved_by_user_id=str(row.get("approved_by_user_id") or "").strip() or None,
            approved_by_username=str(row.get("approved_by_username") or "").strip() or None,
            client_nonce=str(row.get("client_nonce") or "").strip(),
            server_nonce=str(row.get("server_nonce") or "").strip(),
            hostname_conflict=conflict,
            alternate_hostname=alternate_hostname,
            conflict_requires_prompt=requires_prompt,
            fingerprint_match=fingerprint_match,
        )

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "id": self.record_id,
            "approval_reference": self.reference,
            "status": self.status,
            "hostname_claimed": self.claimed_hostname,
            "ssl_key_fingerprint_claimed": self.claimed_fingerprint,
            "created_at": _isoformat(self.created_at),
            "updated_at": _isoformat(self.updated_at),
            "enrollment_code_id": self.enrollment_code_id,
            "guid": self.guid,
            "approved_by_user_id": self.approved_by_user_id,
            "approved_by_username": self.approved_by_username,
            "client_nonce": self.client_nonce,
            "server_nonce": self.server_nonce,
            "conflict_requires_prompt": self.conflict_requires_prompt,
            "fingerprint_match": self.fingerprint_match,
        }
        if self.hostname_conflict is not None:
            payload["hostname_conflict"] = self.hostname_conflict.to_dict()
        if self.alternate_hostname:
            payload["alternate_hostname"] = self.alternate_hostname
        return payload

