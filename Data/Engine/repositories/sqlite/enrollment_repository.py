"""SQLite-backed enrollment repository for Engine services."""

from __future__ import annotations

import logging
from contextlib import closing
from datetime import datetime, timezone
from typing import Any, List, Optional, Tuple

from Data.Engine.domain.device_auth import DeviceFingerprint, DeviceGuid, normalize_guid
from Data.Engine.domain.device_enrollment import (
    EnrollmentApproval,
    EnrollmentApprovalStatus,
    EnrollmentCode,
)
from Data.Engine.domain.enrollment_admin import (
    DeviceApprovalRecord,
    EnrollmentCodeRecord,
    HostnameConflict,
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

    def fetch_install_code_by_id(self, record_id: str) -> Optional[EnrollmentCode]:
        record_value = (record_id or "").strip()
        if not record_value:
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
                 WHERE id = ?
                """,
                (record_value,),
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
            self._log.warning("invalid enrollment code record for id=%s: %s", record_value, exc)
            return None

    def list_install_codes(
        self,
        *,
        status: Optional[str] = None,
        now: Optional[datetime] = None,
    ) -> List[EnrollmentCodeRecord]:
        reference = now or datetime.now(tz=timezone.utc)
        status_filter = (status or "").strip().lower()
        params: List[str] = []

        sql = """
            SELECT id,
                   code,
                   expires_at,
                   created_by_user_id,
                   used_at,
                   used_by_guid,
                   max_uses,
                   use_count,
                   last_used_at
              FROM enrollment_install_codes
        """

        if status_filter in {"active", "expired", "used"}:
            sql += " WHERE "
            if status_filter == "active":
                sql += "use_count < max_uses AND expires_at > ?"
                params.append(self._isoformat(reference))
            elif status_filter == "expired":
                sql += "use_count < max_uses AND expires_at <= ?"
                params.append(self._isoformat(reference))
            else:  # used
                sql += "use_count >= max_uses"

        sql += " ORDER BY expires_at ASC"

        rows: List[EnrollmentCodeRecord] = []
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, params)
            for raw in cur.fetchall():
                record = {
                    "id": raw[0],
                    "code": raw[1],
                    "expires_at": raw[2],
                    "created_by_user_id": raw[3],
                    "used_at": raw[4],
                    "used_by_guid": raw[5],
                    "max_uses": raw[6],
                    "use_count": raw[7],
                    "last_used_at": raw[8],
                }
                try:
                    rows.append(EnrollmentCodeRecord.from_row(record))
                except Exception as exc:  # pragma: no cover - defensive logging
                    self._log.warning("invalid enrollment install code row id=%s: %s", record.get("id"), exc)
        return rows

    def get_install_code_record(self, record_id: str) -> Optional[EnrollmentCodeRecord]:
        identifier = (record_id or "").strip()
        if not identifier:
            return None

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id,
                       code,
                       expires_at,
                       created_by_user_id,
                       used_at,
                       used_by_guid,
                       max_uses,
                       use_count,
                       last_used_at
                  FROM enrollment_install_codes
                 WHERE id = ?
                """,
                (identifier,),
            )
            row = cur.fetchone()

        if not row:
            return None

        payload = {
            "id": row[0],
            "code": row[1],
            "expires_at": row[2],
            "created_by_user_id": row[3],
            "used_at": row[4],
            "used_by_guid": row[5],
            "max_uses": row[6],
            "use_count": row[7],
            "last_used_at": row[8],
        }

        try:
            return EnrollmentCodeRecord.from_row(payload)
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("invalid enrollment install code record id=%s: %s", identifier, exc)
            return None

    def insert_install_code(
        self,
        *,
        record_id: str,
        code: str,
        expires_at: datetime,
        created_by: Optional[str],
        max_uses: int,
    ) -> EnrollmentCodeRecord:
        expires_iso = self._isoformat(expires_at)

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO enrollment_install_codes (
                    id,
                    code,
                    expires_at,
                    created_by_user_id,
                    max_uses,
                    use_count
                ) VALUES (?, ?, ?, ?, ?, 0)
                """,
                (record_id, code, expires_iso, created_by, max_uses),
            )
            conn.commit()

        record = self.get_install_code_record(record_id)
        if record is None:
            raise RuntimeError("failed to load install code after insert")
        return record

    def delete_install_code_if_unused(self, record_id: str) -> bool:
        identifier = (record_id or "").strip()
        if not identifier:
            return False

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                "DELETE FROM enrollment_install_codes WHERE id = ? AND use_count = 0",
                (identifier,),
            )
            deleted = cur.rowcount > 0
            conn.commit()
            return deleted

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
    def list_device_approvals(
        self,
        *,
        status: Optional[str] = None,
    ) -> List[DeviceApprovalRecord]:
        status_filter = (status or "").strip().lower()
        params: List[str] = []

        sql = """
            SELECT
                da.id,
                da.approval_reference,
                da.guid,
                da.hostname_claimed,
                da.ssl_key_fingerprint_claimed,
                da.enrollment_code_id,
                da.status,
                da.client_nonce,
                da.server_nonce,
                da.created_at,
                da.updated_at,
                da.approved_by_user_id,
                u.username AS approved_by_username
              FROM device_approvals AS da
         LEFT JOIN users AS u
                ON (
                    CAST(da.approved_by_user_id AS TEXT) = CAST(u.id AS TEXT)
                    OR LOWER(da.approved_by_user_id) = LOWER(u.username)
                )
        """

        if status_filter and status_filter not in {"all", "*"}:
            sql += " WHERE LOWER(da.status) = ?"
            params.append(status_filter)

        sql += " ORDER BY da.created_at ASC"

        approvals: List[DeviceApprovalRecord] = []
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(sql, params)
            rows = cur.fetchall()

            for raw in rows:
                record = {
                    "id": raw[0],
                    "approval_reference": raw[1],
                    "guid": raw[2],
                    "hostname_claimed": raw[3],
                    "ssl_key_fingerprint_claimed": raw[4],
                    "enrollment_code_id": raw[5],
                    "status": raw[6],
                    "client_nonce": raw[7],
                    "server_nonce": raw[8],
                    "created_at": raw[9],
                    "updated_at": raw[10],
                    "approved_by_user_id": raw[11],
                    "approved_by_username": raw[12],
                }

                conflict, fingerprint_match, requires_prompt = self._compute_hostname_conflict(
                    conn,
                    record.get("hostname_claimed"),
                    record.get("guid"),
                    record.get("ssl_key_fingerprint_claimed") or "",
                )

                alternate = None
                if conflict and requires_prompt:
                    alternate = self._suggest_alternate_hostname(
                        conn,
                        record.get("hostname_claimed"),
                        record.get("guid"),
                    )

                try:
                    approvals.append(
                        DeviceApprovalRecord.from_row(
                            record,
                            conflict=conflict,
                            alternate_hostname=alternate,
                            fingerprint_match=fingerprint_match,
                            requires_prompt=requires_prompt,
                        )
                    )
                except Exception as exc:  # pragma: no cover - defensive logging
                    self._log.warning(
                        "invalid device approval record id=%s: %s",
                        record.get("id"),
                        exc,
                    )

        return approvals

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

    def fetch_pending_approval_by_fingerprint(
        self, fingerprint: DeviceFingerprint
    ) -> Optional[EnrollmentApproval]:
        return self._fetch_device_approval(
            "ssl_key_fingerprint_claimed = ? AND status = 'pending'",
            (fingerprint.value,),
        )

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
    ) -> None:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE device_approvals
                   SET hostname_claimed = ?,
                       guid = ?,
                       enrollment_code_id = ?,
                       client_nonce = ?,
                       server_nonce = ?,
                       agent_pubkey_der = ?,
                       updated_at = ?
                 WHERE id = ?
                """,
                (
                    hostname,
                    guid.value if guid else None,
                    enrollment_code_id,
                    client_nonce_b64,
                    server_nonce_b64,
                    agent_pubkey_der,
                    self._isoformat(updated_at),
                    record_id,
                ),
            )
            conn.commit()

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
                    client_nonce_b64,
                    server_nonce_b64,
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
                       approved_by_user_id,
                       client_nonce,
                       server_nonce,
                       agent_pubkey_der
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
            "client_nonce": row[10],
            "server_nonce": row[11],
            "agent_pubkey_der": row[12],
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

    def _compute_hostname_conflict(
        self,
        conn,
        hostname: Optional[str],
        pending_guid: Optional[str],
        claimed_fp: str,
    ) -> Tuple[Optional[HostnameConflict], bool, bool]:
        normalized_host = (hostname or "").strip()
        if not normalized_host:
            return None, False, False

        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT d.guid,
                       d.ssl_key_fingerprint,
                       ds.site_id,
                       s.name
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
                 WHERE d.hostname = ?
                """,
                (normalized_host,),
            )
            row = cur.fetchone()
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("failed to inspect hostname conflict for %s: %s", normalized_host, exc)
            return None, False, False

        if not row:
            return None, False, False

        existing_guid = normalize_guid(row[0])
        pending_norm = normalize_guid(pending_guid)
        if existing_guid and pending_norm and existing_guid == pending_norm:
            return None, False, False

        stored_fp = (row[1] or "").strip().lower()
        claimed_fp_normalized = (claimed_fp or "").strip().lower()
        fingerprint_match = bool(stored_fp and claimed_fp_normalized and stored_fp == claimed_fp_normalized)

        site_id = None
        if row[2] is not None:
            try:
                site_id = int(row[2])
            except (TypeError, ValueError):  # pragma: no cover - defensive
                site_id = None

        site_name = str(row[3] or "").strip()
        requires_prompt = not fingerprint_match

        conflict = HostnameConflict(
            guid=existing_guid or None,
            ssl_key_fingerprint=stored_fp or None,
            site_id=site_id,
            site_name=site_name,
            fingerprint_match=fingerprint_match,
            requires_prompt=requires_prompt,
        )

        return conflict, fingerprint_match, requires_prompt

    def _suggest_alternate_hostname(
        self,
        conn,
        hostname: Optional[str],
        pending_guid: Optional[str],
    ) -> Optional[str]:
        base = (hostname or "").strip()
        if not base:
            return None
        base = base[:253]
        candidate = base
        pending_norm = normalize_guid(pending_guid)
        suffix = 1

        cur = conn.cursor()
        while True:
            cur.execute("SELECT guid FROM devices WHERE hostname = ?", (candidate,))
            row = cur.fetchone()
            if not row:
                return candidate
            existing_guid = normalize_guid(row[0])
            if pending_norm and existing_guid == pending_norm:
                return candidate
            candidate = f"{base}-{suffix}"
            suffix += 1
            if suffix > 50:
                return pending_norm or candidate

    @staticmethod
    def _isoformat(value: datetime) -> str:
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return value.astimezone(timezone.utc).isoformat()
