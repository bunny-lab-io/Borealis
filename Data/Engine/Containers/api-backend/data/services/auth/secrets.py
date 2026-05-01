# ======================================================
# Data\Engine\services\auth\secrets.py
# Description: Shared helper for retrieving the Engine app secret used by token/cookie serializers.
#
# API Endpoints (if applicable): None
# ======================================================

"""Authentication secret helpers for Engine services."""

from __future__ import annotations

from typing import Any


def require_app_secret(app: Any) -> str:
    """Return the configured Flask secret key or raise if unavailable."""

    secret = getattr(app, "secret_key", None)
    value = str(secret).strip() if secret is not None else ""
    if not value:
        raise RuntimeError("Engine secret key is not configured.")
    return value


__all__ = ["require_app_secret"]
