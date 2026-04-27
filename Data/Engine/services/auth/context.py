# ======================================================
# Data\Engine\services\auth\context.py
# Description: Provides request-scoped authentication helpers with Dev Mode state integration.
#
# API Endpoints (if applicable): None
# ======================================================

"""Shared authentication helpers for Engine REST services."""

from __future__ import annotations

import logging
import os
import uuid
from typing import Any, Callable, Dict, Mapping, Optional, Tuple

from flask import Request, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from .bootstrap_state import operator_auth_allowed
from .dev_mode import DevModeManager
from .secrets import require_app_secret

PermissionResult = Tuple[Dict[str, Any], int]


def revalidate_operator_identity(
    db_conn_factory: Optional[Callable[[], Any]],
    *,
    username: str,
    role: str = "User",
    logger: Optional[logging.Logger] = None,
) -> Optional[Dict[str, Any]]:
    """Return current DB-backed operator identity or None when account is inactive."""

    username_norm = str(username or "").strip()
    if not username_norm or not callable(db_conn_factory):
        return None
    try:
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    username,
                    COALESCE(role, ?),
                    COALESCE(auth_source, 'local'),
                    COALESCE(directory_disabled, 0)
                FROM users
                WHERE LOWER(username)=LOWER(?)
                LIMIT 1
                """,
                (role or "User", username_norm),
            )
            row = cur.fetchone()
        finally:
            conn.close()
    except Exception:
        if logger:
            logger.debug("Failed to revalidate operator identity for %s.", username_norm, exc_info=True)
        return None
    if not row:
        return None
    auth_source = str(row[2] or "local").strip().lower()
    if auth_source == "directory" and bool(row[3] or 0):
        return None
    return {
        "username": str(row[0] or username_norm),
        "role": str(row[1] or role or "User"),
        "auth_source": auth_source,
    }


def _coerce_positive_int(value: Any, default: int) -> int:
    try:
        candidate = int(value)
        if candidate > 0:
            return candidate
    except (TypeError, ValueError):
        pass
    return default


class RequestAuthContext:
    """Resolves the current operator and Dev Mode state for the active request."""

    def __init__(
        self,
        *,
        app,
        dev_mode_manager: DevModeManager,
        config: Optional[Mapping[str, Any]] = None,
        logger: Optional[logging.Logger] = None,
        db_conn_factory: Optional[Callable[[], Any]] = None,
        aegis_cipher_service: Any = None,
    ) -> None:
        self._app = app
        self._dev_mode_manager = dev_mode_manager
        self._config = config or {}
        self._logger = logger or logging.getLogger(__name__)
        self._db_conn_factory = db_conn_factory
        self._aegis_cipher_service = aegis_cipher_service
        secret = require_app_secret(app)
        self._serializer = URLSafeTimedSerializer(secret, salt="borealis-auth")
        default_token_ttl = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
        self._token_ttl = _coerce_positive_int(
            self._config.get("auth_token_ttl_seconds"), default_token_ttl
        )
        default_dev_mode_ttl = int(os.environ.get("BOREALIS_DEV_MODE_TTL_SECONDS", 900))
        self._dev_mode_ttl = _coerce_positive_int(
            self._config.get("assemblies_dev_mode_ttl_seconds"), default_dev_mode_ttl
        )

    # ------------------------------------------------------------------
    # Session helpers
    # ------------------------------------------------------------------
    def ensure_session_token(self) -> str:
        """Ensure an opaque session token exists for Dev Mode tracking."""

        token = session.get("dev_mode_session")
        if not token:
            token = uuid.uuid4().hex
            session["dev_mode_session"] = token
            session.modified = True
        return token

    def current_user(self) -> Optional[Dict[str, Any]]:
        """Return the authenticated operator profile, if any."""

        if (
            callable(self._db_conn_factory)
            and self._aegis_cipher_service is not None
            and not operator_auth_allowed(
                db_conn_factory=self._db_conn_factory,
                aegis_cipher_service=self._aegis_cipher_service,
            )
        ):
            return None

        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return revalidate_operator_identity(
                self._db_conn_factory,
                username=str(username),
                role=str(role),
                logger=self._logger,
            )

        token = self._bearer_token(request)
        if not token:
            return None
        try:
            data = self._serializer.loads(token, max_age=self._token_ttl)
        except (BadSignature, SignatureExpired):
            self._logger.debug("Bearer token invalid or expired.")
            return None
        except Exception:  # pragma: no cover - defensive logging
            self._logger.debug("Unexpected error validating bearer token.", exc_info=True)
            return None

        username = (data.get("u") or "").strip()
        role = (data.get("r") or "User").strip() or "User"
        if not username:
            return None
        return revalidate_operator_identity(
            self._db_conn_factory,
            username=username,
            role=role,
            logger=self._logger,
        )

    def dev_mode_enabled(self, *, user: Optional[Dict[str, Any]] = None) -> bool:
        """Return whether Dev Mode is currently active for the operator."""

        profile = user or self.current_user()
        if not profile:
            return False
        token = self.ensure_session_token()
        return self._dev_mode_manager.is_enabled(username=profile["username"], session_token=token)

    def set_dev_mode(self, enabled: bool, *, ttl_seconds: Optional[int] = None) -> bool:
        """Enable or disable Dev Mode for the active operator session."""

        profile = self.current_user()
        if not profile:
            raise PermissionError("Authentication required to modify Dev Mode state.")  # type: ignore[return-value]
        token = self.ensure_session_token()
        if enabled:
            ttl = ttl_seconds if ttl_seconds and ttl_seconds > 0 else self._dev_mode_ttl
            self._dev_mode_manager.enable(
                username=profile["username"],
                role=profile.get("role") or "User",
                session_token=token,
                ttl_seconds=ttl,
            )
        else:
            self._dev_mode_manager.disable(username=profile["username"], session_token=token)
        return self.dev_mode_enabled(user=profile)

    def clear_dev_mode(self) -> None:
        """Explicitly disable Dev Mode for the active operator."""

        profile = self.current_user()
        if not profile:
            return
        token = self.ensure_session_token()
        self._dev_mode_manager.disable(username=profile["username"], session_token=token)

    # ------------------------------------------------------------------
    # Permission helpers
    # ------------------------------------------------------------------
    def require_user(self) -> Tuple[Optional[Dict[str, Any]], Optional[PermissionResult]]:
        user = self.current_user()
        if not user:
            return None, (
                {
                    "error": "unauthorized",
                    "message": "Authentication required. Please sign in and retry.",
                },
                401,
            )
        return user, None

    def require_admin(self, *, dev_mode_required: bool = False) -> Optional[PermissionResult]:
        user = self.current_user()
        if not user:
            return (
                {
                    "error": "unauthorized",
                    "message": "Authentication required. Please sign in and retry.",
                },
                401,
            )
        if not self.is_admin(user):
            return (
                {
                    "error": "forbidden",
                    "message": "Administrator permissions are required for this action.",
                },
                403,
            )
        if dev_mode_required and not self.dev_mode_enabled(user=user):
            return (
                {
                    "error": "dev_mode_required",
                    "message": "Enable Dev Mode from the Assemblies admin controls to continue.",
                },
                403,
            )
        return None

    # ------------------------------------------------------------------
    # Static helpers
    # ------------------------------------------------------------------
    @staticmethod
    def is_admin(user: Dict[str, Any]) -> bool:
        role = (user.get("role") or "").strip().lower()
        return role == "admin"

    @staticmethod
    def _bearer_token(req: Request) -> Optional[str]:
        header = (req.headers.get("Authorization") or "").strip()
        if header.lower().startswith("bearer "):
            return header.split(" ", 1)[1].strip()
        cookie_token = req.cookies.get("borealis_auth")
        if cookie_token:
            return cookie_token
        return None


__all__ = ["RequestAuthContext", "PermissionResult", "revalidate_operator_identity"]
