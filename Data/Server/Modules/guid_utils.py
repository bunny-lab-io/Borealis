from __future__ import annotations

import string
import uuid
from typing import Optional


def normalize_guid(value: Optional[str]) -> str:
    """
    Canonicalize GUID strings so the server treats different casings/formats uniformly.
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
