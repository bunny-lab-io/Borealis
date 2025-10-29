# ======================================================
# Data\Engine\auth\rate_limit.py
# Description: Sliding-window rate limiter for Engine authentication endpoints.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine-native in-memory rate limiter suitable for single-process development."""

from __future__ import annotations

import time
from collections import deque
from dataclasses import dataclass
from threading import Lock
from typing import Deque, Dict


@dataclass
class RateLimitDecision:
    allowed: bool
    retry_after: float


class SlidingWindowRateLimiter:
    """Simple sliding-window limiter to guard authentication endpoints."""

    def __init__(self) -> None:
        self._buckets: Dict[str, Deque[float]] = {}
        self._lock = Lock()

    def check(self, key: str, limit: int, window_seconds: float) -> RateLimitDecision:
        now = time.monotonic()
        with self._lock:
            bucket = self._buckets.get(key)
            if bucket is None:
                bucket = deque()
                self._buckets[key] = bucket

            while bucket and now - bucket[0] > window_seconds:
                bucket.popleft()

            if len(bucket) >= limit:
                retry_after = max(0.0, window_seconds - (now - bucket[0]))
                return RateLimitDecision(False, retry_after)

            bucket.append(now)
            return RateLimitDecision(True, 0.0)


__all__ = ["RateLimitDecision", "SlidingWindowRateLimiter"]
