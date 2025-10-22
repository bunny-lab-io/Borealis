"""SQLite-backed enrollment repository for Engine services."""

from __future__ import annotations

import logging
from contextlib import closing
from datetime import datetime, timezone
from typing import Optional

from Data.Engine.domain.device_auth import DeviceFingerprint, DeviceGuid
from Data.Engine.domain.device_enrollment import (
    EnrollmentApproval,
    EnrollmentApprovalStatus,
    EnrollmentCode,
)
from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory

__all__ = ["SQLiteEnrollmentRepository"]


class SQLiteEnrollmentRepository:
    """Persistence adapter that manages enrollment codes and approvals."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.enrollment")

    # ------------------------------------------------------------------
    # Enrollment install codes
    # ------------------------------------------------------------------
    def fetch_install_code(self, code: str) -> Optional[EnrollmentCode]:
        """Load an enrollment install code by its public value."""

        code_value = (code or "").strip()
        if not code_value:
            return None

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id,
                       code,
                       expires_at,
                       used_at,
                       used_by_guid,
                       max_uses,
                       use_count,
                       last_used_at
                  FROM enrollment_install_codes
                 WHERE code = ?
                """,
                (code_value,),
            )
            row = cur.fetchone()

        if not row:
            return None

        record = {
            "id": row[0],
            "code": row[1],
            "expires_at": row[2],
            "used_at": row[3],
            "used_by_guid": row[4],
            "max_uses": row[5],
            "use_count": row[6],
            "last_used_at": row[7],
        }
        try:
            return EnrollmentCode.from_mapping(record)
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("invalid enrollment code record for code=%s: %s", code_value, exc)
            return None

    def update_install_code_usage(
        self,
        record_id: str,
        *,
        use_count_increment: int,
        last_used_at: datetime,
        used_by_guid: Optional[DeviceGuid] = None,
        mark_first_use: bool = False,
    ) -> None:
        """Increment usage counters and usage metadata for an install code."""

        if use_count_increment <= 0:
            raise ValueError("use_count_increment must be positive")

        last_used_iso = self._isoformat(last_used_at)
        guid_value = used_by_guid.value if used_by_guid else ""
        mark_flag = 1 if mark_first_use else 0

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE enrollment_install_codes
                   SET use_count = use_count + ?,
                       last_used_at = ?,
                       used_by_guid = COALESCE(NULLIF(?, ''), used_by_guid),
                       used_at = CASE WHEN ? = 1 AND used_at IS NULL THEN ? ELSE used_at END
                 WHERE id = ?
                """,
                (
                    use_count_increment,
                    last_used_iso,
                    guid_value,
                    mark_flag,
                    last_used_iso,
                    record_id,
                ),
            )
            conn.commit()

    # ------------------------------------------------------------------
    # Device approvals
    # ------------------------------------------------------------------
    def fetch_device_approval_by_reference(self, reference: str) -> Optional[EnrollmentApproval]:
        """Load a device approval using its operator-visible reference."""

        ref_value = (reference or "").strip()
        if not ref_value:
            return None
        return self._fetch_device_approval("approval_reference = ?", (ref_value,))

    def fetch_device_approval(self, record_id: str) -> Optional[EnrollmentApproval]:
        record_value = (record_id or "").strip()
        if not record_value:
            return None
        return self._fetch_device_approval("id = ?", (record_value,))

    def create_device_approval(
        self,
        *,
        record_id: str,
        reference: str,
        claimed_hostname: str,
        claimed_fingerprint: DeviceFingerprint,
        enrollment_code_id: Optional[str],
        client_nonce: bytes,
        server_nonce: bytes,
        agent_pubkey_der: bytes,
        created_at: datetime,
        status: EnrollmentApprovalStatus = EnrollmentApprovalStatus.PENDING,
        guid: Optional[DeviceGuid] = None,
    ) -> EnrollmentApproval:
        created_iso = self._isoformat(created_at)
        guid_value = guid.value if guid else None

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO device_approvals (
                    id,
                    approval_reference,
                    guid,
                    hostname_claimed,
                    ssl_key_fingerprint_claimed,
                    enrollment_code_id,
                    status,
                    created_at,
                    updated_at,
                    client_nonce,
                    server_nonce,
                    agent_pubkey_der
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    record_id,
                    reference,
                    guid_value,
                    claimed_hostname,
                    claimed_fingerprint.value,
                    enrollment_code_id,
                    status.value,
                    created_iso,
                    created_iso,
                    client_nonce,
                    server_nonce,
                    agent_pubkey_der,
                ),
            )
            conn.commit()

        approval = self.fetch_device_approval(record_id)
        if approval is None:
            raise RuntimeError("failed to load device approval after insert")
        return approval

    def update_device_approval_status(
        self,
        record_id: str,
        *,
        status: EnrollmentApprovalStatus,
        updated_at: datetime,
        approved_by: Optional[str] = None,
        guid: Optional[DeviceGuid] = None,
    ) -> None:
        """Transition an approval to a new status."""

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE device_approvals
                   SET status = ?,
                       updated_at = ?,
                       guid = COALESCE(?, guid),
                       approved_by_user_id = COALESCE(?, approved_by_user_id)
                 WHERE id = ?
                """,
                (
                    status.value,
                    self._isoformat(updated_at),
                    guid.value if guid else None,
                    approved_by,
                    record_id,
                ),
            )
            conn.commit()

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------
    def _fetch_device_approval(self, where: str, params: tuple) -> Optional[EnrollmentApproval]:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT id,
                       approval_reference,
                       guid,
                       hostname_claimed,
                       ssl_key_fingerprint_claimed,
                       enrollment_code_id,
                       created_at,
                       updated_at,
                       status,
                       approved_by_user_id
                  FROM device_approvals
                 WHERE {where}
                """,
                params,
            )
            row = cur.fetchone()

        if not row:
            return None

        record = {
            "id": row[0],
            "approval_reference": row[1],
            "guid": row[2],
            "hostname_claimed": row[3],
            "ssl_key_fingerprint_claimed": row[4],
            "enrollment_code_id": row[5],
            "created_at": row[6],
            "updated_at": row[7],
            "status": row[8],
            "approved_by_user_id": row[9],
        }

        try:
            return EnrollmentApproval.from_mapping(record)
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning(
                "invalid device approval record id=%s reference=%s: %s",
                row[0],
                row[1],
                exc,
            )
            return None

    @staticmethod
    def _isoformat(value: datetime) -> str:
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return value.astimezone(timezone.utc).isoformat()
