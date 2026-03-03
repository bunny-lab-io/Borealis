# ======================================================
# Data\Engine\services\API\access_management\multi_factor_authentication.py
# Description: Multifactor administration endpoints for enabling, disabling, or resetting operator MFA state.
#
# API Endpoints (if applicable):
# - POST /api/users/<username>/mfa (Token Authenticated (Admin)) - Toggles MFA and optionally resets shared secrets.
# ======================================================

"""Multifactor administrative endpoints for the Borealis Engine."""
from __future__ import annotations

import os
import sqlite3
import time
from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from flask import Blueprint, Flask, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters

from ...auth.secrets import require_app_secret

def _now_ts() -> int:
    return int(time.time())


class MultiFactorAdministrationService:
    """Admin-focused MFA utility wrapper."""

    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.logger = adapters.context.logger

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, Any]]:
        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return {"username": username, "role": role}

        token = None
        auth_header = request.headers.get("Authorization") or ""
        if auth_header.lower().startswith("bearer "):
            token = auth_header.split(" ", 1)[1].strip()
        if not token:
            token = request.cookies.get("borealis_auth")
        if not token:
            return None
        try:
            data = self._token_serializer().loads(
                token,
                max_age=int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30)),
            )
            username = data.get("u")
            role = data.get("r") or "User"
            if username:
                return {"username": username, "role": role}
        except (BadSignature, SignatureExpired, Exception):
            return None
        return None

    def _require_admin(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if (user.get("role") or "").lower() != "admin":
            return {"error": "forbidden"}, 403
        return None

    def toggle_mfa(self, username: str):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        username_norm = (username or "").strip()
        if not username_norm:
            return jsonify({"error": "invalid username"}), 400

        payload = request.get_json(silent=True) or {}
        enabled = bool(payload.get("enabled"))
        reset_secret = bool(payload.get("reset_secret", False))

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            now_ts = _now_ts()

            if enabled:
                if reset_secret:
                    cur.execute(
                        "UPDATE users SET mfa_enabled=1, mfa_secret=NULL, updated_at=? WHERE LOWER(username)=LOWER(?)",
                        (now_ts, username_norm),
                    )
                else:
                    cur.execute(
                        "UPDATE users SET mfa_enabled=1, updated_at=? WHERE LOWER(username)=LOWER(?)",
                        (now_ts, username_norm),
                    )
            else:
                if reset_secret:
                    cur.execute(
                        "UPDATE users SET mfa_enabled=0, mfa_secret=NULL, updated_at=? WHERE LOWER(username)=LOWER(?)",
                        (now_ts, username_norm),
                    )
                else:
                    cur.execute(
                        "UPDATE users SET mfa_enabled=0, updated_at=? WHERE LOWER(username)=LOWER(?)",
                        (now_ts, username_norm),
                    )

            if cur.rowcount == 0:
                return jsonify({"error": "user not found"}), 404

            conn.commit()

            me = self._current_user()
            if me and me.get("username", "").lower() == username_norm.lower() and not enabled:
                session.pop("mfa_pending", None)

            return jsonify({"status": "ok"})
        except Exception as exc:
            self.logger.debug("Failed to update MFA for %s", username_norm, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()


def register_mfa_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register MFA administration endpoints."""

    service = MultiFactorAdministrationService(app, adapters)
    blueprint = Blueprint("access_mgmt_mfa", __name__)

    @blueprint.route("/api/users/<username>/mfa", methods=["POST"])
    def _toggle_mfa(username: str):
        return service.toggle_mfa(username)

    app.register_blueprint(blueprint)
