"""SQLite-backed refresh token repository for the Engine."""

from __future__ import annotations

import logging
from contextlib import closing
from datetime import datetime, timezone
from typing import Optional

from Data.Engine.domain.device_auth import DeviceGuid
from Data.Engine.repositories.sqlite.connection import SQLiteConnectionFactory
from Data.Engine.services.auth.token_service import RefreshTokenRecord

__all__ = ["SQLiteRefreshTokenRepository"]


class SQLiteRefreshTokenRepository:
    """Persistence adapter for refresh token records."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.tokens")

    def fetch(self, guid: DeviceGuid, token_hash: str) -> Optional[RefreshTokenRecord]:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, guid, token_hash, dpop_jkt, created_at, expires_at, revoked_at
                  FROM refresh_tokens
                 WHERE guid = ?
                   AND token_hash = ?
                """,
                (guid.value, token_hash),
            )
            row = cur.fetchone()

        if not row:
            return None

        return self._row_to_record(row)

    def clear_dpop_binding(self, record_id: str) -> None:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                "UPDATE refresh_tokens SET dpop_jkt = NULL WHERE id = ?",
                (record_id,),
            )
            conn.commit()

    def touch(
        self,
        record_id: str,
        *,
        last_used_at: datetime,
        dpop_jkt: Optional[str],
    ) -> None:
        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE refresh_tokens
                   SET last_used_at = ?,
                       dpop_jkt = COALESCE(NULLIF(?, ''), dpop_jkt)
                 WHERE id = ?
                """,
                (
                    self._isoformat(last_used_at),
                    (dpop_jkt or "").strip(),
                    record_id,
                ),
            )
            conn.commit()

    def _row_to_record(self, row: tuple) -> Optional[RefreshTokenRecord]:
        try:
            guid = DeviceGuid(row[1])
        except Exception as exc:
            self._log.warning("invalid refresh token row guid=%s: %s", row[1], exc)
            return None

        created_at = self._parse_iso(row[4])
        expires_at = self._parse_iso(row[5])
        revoked_at = self._parse_iso(row[6])

        if created_at is None:
            created_at = datetime.now(tz=timezone.utc)

        return RefreshTokenRecord.from_row(
            record_id=str(row[0]),
            guid=guid,
            token_hash=str(row[2]),
            dpop_jkt=str(row[3]) if row[3] is not None else None,
            created_at=created_at,
            expires_at=expires_at,
            revoked_at=revoked_at,
        )

    @staticmethod
    def _parse_iso(value: Optional[str]) -> Optional[datetime]:
        if not value:
            return None
        raw = str(value).strip()
        if not raw:
            return None
        try:
            parsed = datetime.fromisoformat(raw)
        except Exception:
            return None
        if parsed.tzinfo is None:
            return parsed.replace(tzinfo=timezone.utc)
        return parsed

    @staticmethod
    def _isoformat(value: datetime) -> str:
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return value.astimezone(timezone.utc).isoformat()
