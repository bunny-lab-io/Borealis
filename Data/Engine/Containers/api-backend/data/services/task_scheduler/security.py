"""Internal job-scheduler authentication helpers."""

from __future__ import annotations

import hmac
from hashlib import sha256
from typing import Any

INTERNAL_TOKEN_HEADER = "X-Borealis-Internal-Token"
_TOKEN_CONTEXT = b"borealis-job-scheduler-internal-v1"


def internal_token(secret: Any) -> str:
    value = str(secret or "").strip().encode("utf-8")
    if not value:
        return ""
    return hmac.new(value, _TOKEN_CONTEXT, sha256).hexdigest()


def validate_internal_token(secret: Any, presented: Any) -> bool:
    expected = internal_token(secret)
    candidate = str(presented or "").strip()
    return bool(expected and candidate and hmac.compare_digest(expected, candidate))


__all__ = ["INTERNAL_TOKEN_HEADER", "internal_token", "validate_internal_token"]
