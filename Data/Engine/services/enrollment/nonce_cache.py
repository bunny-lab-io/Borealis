"""Nonce replay protection for enrollment workflows."""

from __future__ import annotations

import time
from threading import Lock
from typing import Dict

__all__ = ["NonceCache"]


class NonceCache:
    """Track recently observed nonces to prevent replay."""

    def __init__(self, ttl_seconds: float = 300.0) -> None:
        self._ttl = ttl_seconds
        self._entries: Dict[str, float] = {}
        self._lock = Lock()

    def consume(self, key: str) -> bool:
        """Consume *key* if it has not been seen recently."""

        now = time.monotonic()
        with self._lock:
            expiry = self._entries.get(key)
            if expiry and expiry > now:
                return False
            self._entries[key] = now + self._ttl
            stale = [nonce for nonce, ttl in self._entries.items() if ttl <= now]
            for nonce in stale:
                self._entries.pop(nonce, None)
            return True
