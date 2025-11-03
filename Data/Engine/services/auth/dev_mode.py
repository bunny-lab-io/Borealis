# ======================================================
# Data\Engine\services\auth\dev_mode.py
# Description: Manages in-memory Dev Mode state for operator sessions with TTL enforcement.
#
# API Endpoints (if applicable): None
# ======================================================

"""Server-side Dev Mode state tracking for operator sessions."""

from __future__ import annotations

import datetime as _dt
import logging
import threading
from dataclasses import dataclass
from typing import Dict, Optional, Tuple


def _utcnow() -> _dt.datetime:
    return _dt.datetime.utcnow().replace(microsecond=0)


def _normalize_username(value: str) -> str:
    return (value or "").strip().lower()


@dataclass(frozen=True)
class DevModeEntry:
    """Represents a Dev Mode grant for an operator."""

    username: str
    role: str
    session_token: str
    enabled_at: _dt.datetime
    expires_at: _dt.datetime

    def is_expired(self, *, now: Optional[_dt.datetime] = None) -> bool:
        reference = now or _utcnow()
        return reference >= self.expires_at


class DevModeManager:
    """Tracks Dev Mode enablement for operator sessions."""

    def __init__(self, *, logger: Optional[logging.Logger] = None, default_ttl_seconds: int = 900) -> None:
        ttl = int(default_ttl_seconds or 0)
        if ttl < 60:
            ttl = 60
        self._default_ttl = ttl
        self._logger = logger or logging.getLogger(__name__)
        self._entries: Dict[Tuple[str, str], DevModeEntry] = {}
        self._lock = threading.RLock()

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------
    def enable(
        self,
        *,
        username: str,
        role: str,
        session_token: str,
        ttl_seconds: Optional[int] = None,
    ) -> DevModeEntry:
        """Enable Dev Mode for the specified operator session."""

        normalized = _normalize_username(username)
        if not normalized:
            raise ValueError("username required for Dev Mode enablement")
        token = (session_token or "").strip()
        if not token:
            raise ValueError("session token required for Dev Mode enablement")

        ttl = ttl_seconds if ttl_seconds and ttl_seconds > 0 else self._default_ttl
        if ttl < 60:
            ttl = 60
        now = _utcnow()
        entry = DevModeEntry(
            username=normalized,
            role=(role or "User").strip() or "User",
            session_token=token,
            enabled_at=now,
            expires_at=now + _dt.timedelta(seconds=ttl),
        )

        with self._lock:
            self._prune_locked(now=now)
            self._entries[(normalized, token)] = entry

        self._logger.info(
            "Dev Mode enabled for user='%s' role='%s' session_suffix='%s' ttl_seconds=%s",
            normalized,
            entry.role,
            token[-6:],
            ttl,
        )
        return entry

    def disable(self, *, username: str, session_token: str) -> bool:
        """Disable Dev Mode for the specified operator session."""

        normalized = _normalize_username(username)
        token = (session_token or "").strip()
        if not normalized or not token:
            return False

        removed = False
        with self._lock:
            removed = self._entries.pop((normalized, token), None) is not None

        if removed:
            self._logger.info(
                "Dev Mode disabled for user='%s' session_suffix='%s'",
                normalized,
                token[-6:],
            )
        return removed

    def is_enabled(self, *, username: str, session_token: str) -> bool:
        """Return whether Dev Mode is currently enabled for the operator session."""

        normalized = _normalize_username(username)
        token = (session_token or "").strip()
        if not normalized or not token:
            return False

        now = _utcnow()
        with self._lock:
            entry = self._entries.get((normalized, token))
            if not entry:
                self._prune_locked(now=now)
                return False

            if entry.is_expired(now=now):
                self._entries.pop((normalized, token), None)
                self._logger.info(
                    "Dev Mode expired for user='%s' session_suffix='%s'",
                    normalized,
                    token[-6:],
                )
                return False

        return True

    def describe(self) -> Dict[str, Dict[str, str]]:
        """Return a serialisable snapshot of active Dev Mode entries."""

        snapshot: Dict[str, Dict[str, str]] = {}
        now = _utcnow()
        with self._lock:
            self._prune_locked(now=now)
            for entry in self._entries.values():
                key = f"{entry.username}:{entry.session_token[-6:]}"
                snapshot[key] = {
                    "username": entry.username,
                    "role": entry.role,
                    "enabled_at": entry.enabled_at.isoformat(),
                    "expires_at": entry.expires_at.isoformat(),
                }
        return snapshot

    def clean_expired(self) -> None:
        """Remove expired Dev Mode entries."""

        with self._lock:
            self._prune_locked(now=_utcnow())

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _prune_locked(self, *, now: Optional[_dt.datetime] = None) -> None:
        reference = now or _utcnow()
        expired_keys = [
            key for key, entry in self._entries.items() if entry.is_expired(now=reference)
        ]
        for key in expired_keys:
            entry = self._entries.pop(key, None)
            if entry:
                self._logger.info(
                    "Dev Mode expired for user='%s' session_suffix='%s' (prune)",
                    entry.username,
                    entry.session_token[-6:],
                )


__all__ = ["DevModeManager", "DevModeEntry"]

