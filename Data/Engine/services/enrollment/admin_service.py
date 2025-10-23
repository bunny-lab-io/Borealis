"""Administrative helpers for enrollment workflows."""

from __future__ import annotations

import logging
import secrets
import uuid
from datetime import datetime, timedelta, timezone
from typing import Callable, List, Optional

from dataclasses import dataclass

from Data.Engine.domain.device_auth import DeviceGuid, normalize_guid
from Data.Engine.domain.device_enrollment import EnrollmentApprovalStatus
from Data.Engine.domain.enrollment_admin import DeviceApprovalRecord, EnrollmentCodeRecord
from Data.Engine.repositories.sqlite.enrollment_repository import SQLiteEnrollmentRepository
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository

__all__ = ["EnrollmentAdminService", "DeviceApprovalActionResult"]


@dataclass(frozen=True, slots=True)
class DeviceApprovalActionResult:
    """Outcome metadata returned after mutating an approval."""

    status: str
    conflict_resolution: Optional[str] = None

    def to_dict(self) -> dict[str, str]:
        payload = {"status": self.status}
        if self.conflict_resolution:
            payload["conflict_resolution"] = self.conflict_resolution
        return payload


class EnrollmentAdminService:
    """Expose administrative enrollment operations."""

    _VALID_TTL_HOURS = {1, 3, 6, 12, 24}

    def __init__(
        self,
        *,
        repository: SQLiteEnrollmentRepository,
        user_repository: SQLiteUserRepository,
        logger: Optional[logging.Logger] = None,
        clock: Optional[Callable[[], datetime]] = None,
    ) -> None:
        self._repository = repository
        self._users = user_repository
        self._log = logger or logging.getLogger("borealis.engine.services.enrollment_admin")
        self._clock = clock or (lambda: datetime.now(tz=timezone.utc))

    # ------------------------------------------------------------------
    # Enrollment install codes
    # ------------------------------------------------------------------
    def list_install_codes(self, *, status: Optional[str] = None) -> List[EnrollmentCodeRecord]:
        return self._repository.list_install_codes(status=status, now=self._clock())

    def create_install_code(
        self,
        *,
        ttl_hours: int,
        max_uses: int,
        created_by: Optional[str],
    ) -> EnrollmentCodeRecord:
        if ttl_hours not in self._VALID_TTL_HOURS:
            raise ValueError("invalid_ttl")

        normalized_max = self._normalize_max_uses(max_uses)

        now = self._clock()
        expires_at = now + timedelta(hours=ttl_hours)
        record_id = str(uuid.uuid4())
        code = self._generate_install_code()

        created_by_identifier = None
        if created_by:
            created_by_identifier = self._users.resolve_identifier(created_by)
            if not created_by_identifier:
                created_by_identifier = created_by.strip() or None

        record = self._repository.insert_install_code(
            record_id=record_id,
            code=code,
            expires_at=expires_at,
            created_by=created_by_identifier,
            max_uses=normalized_max,
        )

        self._log.info(
            "install code created id=%s ttl=%sh max_uses=%s",
            record.record_id,
            ttl_hours,
            normalized_max,
        )

        return record

    def delete_install_code(self, record_id: str) -> bool:
        deleted = self._repository.delete_install_code_if_unused(record_id)
        if deleted:
            self._log.info("install code deleted id=%s", record_id)
        return deleted

    # ------------------------------------------------------------------
    # Device approvals
    # ------------------------------------------------------------------
    def list_device_approvals(self, *, status: Optional[str] = None) -> List[DeviceApprovalRecord]:
        return self._repository.list_device_approvals(status=status)

    def approve_device_approval(
        self,
        record_id: str,
        *,
        actor: Optional[str],
        guid: Optional[str] = None,
        conflict_resolution: Optional[str] = None,
    ) -> DeviceApprovalActionResult:
        return self._set_device_approval_status(
            record_id,
            EnrollmentApprovalStatus.APPROVED,
            actor=actor,
            guid=guid,
            conflict_resolution=conflict_resolution,
        )

    def deny_device_approval(
        self,
        record_id: str,
        *,
        actor: Optional[str],
    ) -> DeviceApprovalActionResult:
        return self._set_device_approval_status(
            record_id,
            EnrollmentApprovalStatus.DENIED,
            actor=actor,
            guid=None,
            conflict_resolution=None,
        )

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------
    @staticmethod
    def _generate_install_code() -> str:
        raw = secrets.token_hex(16).upper()
        return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))

    @staticmethod
    def _normalize_max_uses(value: int) -> int:
        try:
            count = int(value)
        except Exception:
            count = 2
        if count < 1:
            return 1
        if count > 10:
            return 10
        return count

    def _set_device_approval_status(
        self,
        record_id: str,
        status: EnrollmentApprovalStatus,
        *,
        actor: Optional[str],
        guid: Optional[str],
        conflict_resolution: Optional[str],
    ) -> DeviceApprovalActionResult:
        approval = self._repository.fetch_device_approval(record_id)
        if approval is None:
            raise LookupError("not_found")

        if approval.status is not EnrollmentApprovalStatus.PENDING:
            raise ValueError("approval_not_pending")

        normalized_guid = normalize_guid(guid) or (approval.guid.value if approval.guid else "")
        resolution_normalized = (conflict_resolution or "").strip().lower() or None

        fingerprint_match = False
        conflict_guid: Optional[str] = None

        if status is EnrollmentApprovalStatus.APPROVED:
            pending_records = self._repository.list_device_approvals(status="pending")
            current_record = next(
                (record for record in pending_records if record.record_id == approval.record_id),
                None,
            )

            conflict = current_record.hostname_conflict if current_record else None
            if conflict:
                conflict_guid = normalize_guid(conflict.guid)
                fingerprint_match = bool(conflict.fingerprint_match)

                if fingerprint_match:
                    normalized_guid = conflict_guid or normalized_guid or ""
                    if resolution_normalized is None:
                        resolution_normalized = "auto_merge_fingerprint"
                elif resolution_normalized == "overwrite":
                    normalized_guid = conflict_guid or normalized_guid or ""
                elif resolution_normalized == "coexist":
                    pass
                else:
                    raise ValueError("conflict_resolution_required")

        if normalized_guid:
            try:
                guid_value = DeviceGuid(normalized_guid)
            except ValueError as exc:
                raise ValueError("invalid_guid") from exc
        else:
            guid_value = None

        actor_identifier = None
        if actor:
            actor_identifier = self._users.resolve_identifier(actor)
            if not actor_identifier:
                actor_identifier = actor.strip() or None
        if not actor_identifier:
            actor_identifier = "system"

        self._repository.update_device_approval_status(
            approval.record_id,
            status=status,
            updated_at=self._clock(),
            approved_by=actor_identifier,
            guid=guid_value,
        )

        if status is EnrollmentApprovalStatus.APPROVED:
            self._log.info(
                "device approval %s approved resolution=%s guid=%s",
                approval.record_id,
                resolution_normalized or "",
                guid_value.value if guid_value else normalized_guid or "",
            )
        else:
            self._log.info("device approval %s denied", approval.record_id)

        return DeviceApprovalActionResult(
            status=status.value,
            conflict_resolution=resolution_normalized,
        )

