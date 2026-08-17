# ======================================================
# Data\Engine\auth\guid_utils.py
# Description: GUID normalisation helpers used by Engine authentication flows.
#
# API Endpoints (if applicable): None
# ======================================================

"""GUID normalisation helpers for Engine-managed authentication."""

from __future__ import annotations

import string
import uuid
from typing import Optional


def normalize_guid(value: Optional[str]) -> str:
    """
    Canonicalise GUID strings so Engine services treat different casings uniformly.
    """

    candidate = (value or "").strip()
    if not candidate:
        return ""
    candidate = candidate.strip("{}")
    try:
        return str(uuid.UUID(candidate)).upper()
    except Exception:
        cleaned = "".join(ch for ch in candidate if ch in string.hexdigits or ch == "-")
        cleaned = cleaned.strip("-")
        if cleaned:
            try:
                return str(uuid.UUID(cleaned)).upper()
            except Exception:
                pass
        return candidate.upper()


__all__ = ["normalize_guid"]
