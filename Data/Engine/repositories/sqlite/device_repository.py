"""SQLite-backed device repository for the Engine authentication services."""

from __future__ import annotations

import logging
import sqlite3
import time
import uuid
from contextlib import closing
from datetime import datetime, timezone
from typing import Optional

from Data.Engine.domain.device_auth import (
    DeviceFingerprint,
    DeviceGuid,
    DeviceIdentity,
    DeviceStatus,
)
from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory
from Data.Engine.services.auth.device_auth_service import DeviceRecord

__all__ = ["SQLiteDeviceRepository"]


class SQLiteDeviceRepository:
    """Persistence adapter that reads and recovers device rows."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.devices")

    def fetch_by_guid(self, guid: DeviceGuid) -> Optional[DeviceRecord]:
        """Fetch a device row by GUID, normalizing legacy case variance."""

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT guid, ssl_key_fingerprint, token_version, status
                  FROM devices
                 WHERE UPPER(guid) = ?
                """,
                (guid.value.upper(),),
            )
            rows = cur.fetchall()

        if not rows:
            return None

        for row in rows:
            record = self._row_to_record(row)
            if record and record.identity.guid.value == guid.value:
                return record

        # Fall back to the first row if normalization failed to match exactly.
        return self._row_to_record(rows[0])

    def recover_missing(
        self,
        guid: DeviceGuid,
        fingerprint: DeviceFingerprint,
        token_version: int,
        service_context: Optional[str],
    ) -> Optional[DeviceRecord]:
        """Attempt to recreate a missing device row for a valid token."""

        now_ts = int(time.time())
        now_iso = datetime.now(tz=timezone.utc).isoformat()
        base_hostname = f"RECOVERED-{guid.value[:12]}"

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            for attempt in range(6):
                hostname = base_hostname if attempt == 0 else f"{base_hostname}-{attempt}"
                try:
                    cur.execute(
                        """
                        INSERT INTO devices (
                            guid,
                            hostname,
                            created_at,
                            last_seen,
                            ssl_key_fingerprint,
                            token_version,
                            status,
                            key_added_at
                        )
                        VALUES (?, ?, ?, ?, ?, ?, 'active', ?)
                        """,
                        (
                            guid.value,
                            hostname,
                            now_ts,
                            now_ts,
                            fingerprint.value,
                            max(token_version or 1, 1),
                            now_iso,
                        ),
                    )
                except sqlite3.IntegrityError as exc:
                    message = str(exc).lower()
                    if "hostname" in message and "unique" in message:
                        continue
                    self._log.warning(
                        "device auth failed to recover guid=%s (context=%s): %s",
                        guid.value,
                        service_context or "none",
                        exc,
                    )
                    conn.rollback()
                    return None
                except Exception as exc:  # pragma: no cover - defensive logging
                    self._log.exception(
                        "device auth unexpected error recovering guid=%s (context=%s)",
                        guid.value,
                        service_context or "none",
                        exc_info=exc,
                    )
                    conn.rollback()
                    return None
                else:
                    conn.commit()
                    break
            else:
                self._log.warning(
                    "device auth could not recover guid=%s; hostname collisions persisted",
                    guid.value,
                )
                conn.rollback()
                return None

            cur.execute(
                """
                SELECT guid, ssl_key_fingerprint, token_version, status
                  FROM devices
                 WHERE guid = ?
                """,
                (guid.value,),
            )
            row = cur.fetchone()

        if not row:
            self._log.warning(
                "device auth recovery committed but row missing for guid=%s",
                guid.value,
            )
            return None

        return self._row_to_record(row)

    def ensure_device_record(
        self,
        *,
        guid: DeviceGuid,
        hostname: str,
        fingerprint: DeviceFingerprint,
    ) -> DeviceRecord:
        now_iso = datetime.now(tz=timezone.utc).isoformat()
        now_ts = int(time.time())

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT guid, hostname, token_version, status, ssl_key_fingerprint, key_added_at
                  FROM devices
                 WHERE UPPER(guid) = ?
                """,
                (guid.value.upper(),),
            )
            row = cur.fetchone()

            if row:
                stored_fp = (row[4] or "").strip().lower()
                new_fp = fingerprint.value
                if not stored_fp:
                    cur.execute(
                        "UPDATE devices SET ssl_key_fingerprint = ?, key_added_at = ? WHERE guid = ?",
                        (new_fp, now_iso, row[0]),
                    )
                elif stored_fp != new_fp:
                    token_version = self._coerce_int(row[2], default=1) + 1
                    cur.execute(
                        """
                        UPDATE devices
                           SET ssl_key_fingerprint = ?,
                               key_added_at = ?,
                               token_version = ?,
                               status = 'active'
                         WHERE guid = ?
                        """,
                        (new_fp, now_iso, token_version, row[0]),
                    )
                    cur.execute(
                        """
                        UPDATE refresh_tokens
                           SET revoked_at = ?
                         WHERE guid = ?
                           AND revoked_at IS NULL
                        """,
                        (now_iso, row[0]),
                    )
                conn.commit()
            else:
                resolved_hostname = self._resolve_hostname(cur, hostname, guid)
                cur.execute(
                    """
                    INSERT INTO devices (
                        guid,
                        hostname,
                        created_at,
                        last_seen,
                        ssl_key_fingerprint,
                        token_version,
                        status,
                        key_added_at
                    )
                    VALUES (?, ?, ?, ?, ?, 1, 'active', ?)
                    """,
                    (
                        guid.value,
                        resolved_hostname,
                        now_ts,
                        now_ts,
                        fingerprint.value,
                        now_iso,
                    ),
                )
                conn.commit()

            cur.execute(
                """
                SELECT guid, ssl_key_fingerprint, token_version, status
                  FROM devices
                 WHERE UPPER(guid) = ?
                """,
                (guid.value.upper(),),
            )
            latest = cur.fetchone()

        if not latest:
            raise RuntimeError("device record could not be ensured")

        record = self._row_to_record(latest)
        if record is None:
            raise RuntimeError("device record invalid after ensure")
        return record

    def record_device_key(
        self,
        *,
        guid: DeviceGuid,
        fingerprint: DeviceFingerprint,
        added_at: datetime,
    ) -> None:
        added_iso = added_at.astimezone(timezone.utc).isoformat()
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
                VALUES (?, ?, ?, ?)
                """,
                (str(uuid.uuid4()), guid.value, fingerprint.value, added_iso),
            )
            cur.execute(
                """
                UPDATE device_keys
                   SET retired_at = ?
                 WHERE guid = ?
                   AND ssl_key_fingerprint != ?
                   AND retired_at IS NULL
                """,
                (added_iso, guid.value, fingerprint.value),
            )
            conn.commit()

    def _row_to_record(self, row: tuple) -> Optional[DeviceRecord]:
        try:
            guid = DeviceGuid(row[0])
            fingerprint_value = (row[1] or "").strip()
            if not fingerprint_value:
                self._log.warning(
                    "device row %s missing TLS fingerprint; skipping",
                    row[0],
                )
                return None
            fingerprint = DeviceFingerprint(fingerprint_value)
        except Exception as exc:
            self._log.warning("invalid device row for guid=%s: %s", row[0], exc)
            return None

        token_version_raw = row[2]
        try:
            token_version = int(token_version_raw or 0)
        except Exception:
            token_version = 0

        status = DeviceStatus.from_string(row[3])
        identity = DeviceIdentity(guid=guid, fingerprint=fingerprint)

        return DeviceRecord(
            identity=identity,
            token_version=max(token_version, 1),
            status=status,
        )

    @staticmethod
    def _coerce_int(value: object, *, default: int = 0) -> int:
        try:
            return int(value)
        except Exception:
            return default

    def _resolve_hostname(self, cur: sqlite3.Cursor, hostname: str, guid: DeviceGuid) -> str:
        base = (hostname or "").strip() or guid.value
        base = base[:253]
        candidate = base
        suffix = 1
        while True:
            cur.execute(
                "SELECT guid FROM devices WHERE hostname = ?",
                (candidate,),
            )
            row = cur.fetchone()
            if not row:
                return candidate
            existing = (row[0] or "").strip().upper()
            if existing == guid.value:
                return candidate
            candidate = f"{base}-{suffix}"
            suffix += 1
            if suffix > 50:
                return guid.value
