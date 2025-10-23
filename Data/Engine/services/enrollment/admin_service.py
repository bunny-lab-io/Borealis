"""Administrative helpers for enrollment workflows."""

from __future__ import annotations

import logging
import secrets
import uuid
from datetime import datetime, timedelta, timezone
from typing import Callable, List, Optional

from Data.Engine.domain.enrollment_admin import DeviceApprovalRecord, EnrollmentCodeRecord
from Data.Engine.repositories.sqlite.enrollment_repository import SQLiteEnrollmentRepository
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository

__all__ = ["EnrollmentAdminService"]


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

