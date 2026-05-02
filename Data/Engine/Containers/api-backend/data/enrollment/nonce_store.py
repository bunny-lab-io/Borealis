# ======================================================
# Data\Engine\enrollment\nonce_store.py
# Description: Short-lived nonce cache preventing replay during Engine enrollment flows.
#
# API Endpoints (if applicable): None
# ======================================================

"""Short-lived nonce cache to defend against enrollment replay attacks."""

from __future__ import annotations

import time
from threading import Lock
from typing import Dict


class NonceCache:
    def __init__(self, ttl_seconds: float = 300.0) -> None:
        self._ttl = ttl_seconds
        self._entries: Dict[str, float] = {}
        self._lock = Lock()

    def consume(self, key: str) -> bool:
        """
        Attempt to consume the nonce identified by `key`.

        Returns True on first use within TTL, False if already consumed.
        """

        now = time.monotonic()
        with self._lock:
            expire_at = self._entries.get(key)
            if expire_at and expire_at > now:
                return False
            self._entries[key] = now + self._ttl
            stale = [nonce for nonce, expiry in self._entries.items() if expiry <= now]
            for nonce in stale:
                self._entries.pop(nonce, None)
            return True


__all__ = ["NonceCache"]
