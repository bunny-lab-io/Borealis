# ======================================================
# Data\Engine\services\API\access_management\github.py
# Description: GitHub API token management endpoints for Engine access-management parity.
#
# API Endpoints (if applicable):
# - GET /api/github/token (Token Authenticated (Admin)) - Returns stored GitHub API token details and verification status.
# - POST /api/github/token (Token Authenticated (Admin)) - Updates the stored GitHub API token and triggers verification.
# ======================================================

"""GitHub token administration endpoints for the Borealis Engine."""
from __future__ import annotations

import os
import time
from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from flask import Blueprint, Flask, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters

from ...auth.secrets import require_app_secret
from ...aegis_cipher import (
    AegisCipherServiceError,
    AegisDataCorruptionError,
    AegisLockedError,
    AegisNotConfiguredError,
)

def _now_ts() -> int:
    return int(time.time())


class GitHubTokenService:
    """Admin endpoints for storing and validating GitHub REST API tokens."""

    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.github = adapters.github_integration
        self.logger = adapters.context.logger
        self.aegis = adapters.aegis_cipher_service

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

    def get_token(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        aegis_status = self.aegis.status()
        storage_state = self.github.token_storage_state()
        if aegis_status["configured"] and aegis_status["locked"]:
            payload = {
                "token": "",
                "has_token": False,
                "valid": False,
                "message": "Aegis Cipher is locked. Enter it from Access Management > Credentials to manage the GitHub token.",
                "status": "locked",
                "rate_limit": None,
                "error": None,
                "checked_at": _now_ts(),
                "configured": aegis_status["configured"],
                "locked": aegis_status["locked"],
                "reset_required": False,
                "reset_at": 0,
            }
            return jsonify(payload)

        token = self.github.load_token(force_refresh=True)
        verification = self.github.verify_token(token)
        reset_required = bool(storage_state.get("reset_required"))
        reset_at = int(storage_state.get("reset_at") or 0)
        if reset_required and not token:
            message = "Aegis Cipher reset removed the stored GitHub token. Please re-enter the Personal Access Token."
            status = "reset_required"
        else:
            message = verification.get("message") or ("API Token Invalid" if token else "API Token Not Configured")
            status = verification.get("status") or ("missing" if not token else "unknown")
        payload = {
            "token": token or "",
            "has_token": bool(token),
            "valid": bool(verification.get("valid")),
            "message": message,
            "status": status,
            "rate_limit": verification.get("rate_limit"),
            "error": verification.get("error"),
            "checked_at": _now_ts(),
            "configured": aegis_status["configured"],
            "locked": aegis_status["locked"],
            "reset_required": reset_required,
            "reset_at": reset_at,
        }
        return jsonify(payload)

    def update_token(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        try:
            self.aegis.require_secret_storage_ready()
        except AegisNotConfiguredError as exc:
            return jsonify({"error": "aegis_not_configured", "message": str(exc)}), 409
        except AegisLockedError as exc:
            return jsonify({"error": "aegis_locked", "message": str(exc)}), 423
        except AegisDataCorruptionError as exc:
            return jsonify({"error": "corrupt_secret_store", "message": str(exc)}), 500
        except AegisCipherServiceError as exc:
            return jsonify({"error": "aegis_error", "message": str(exc)}), 500

        data = request.get_json(silent=True) or {}
        token = str(data.get("token") or "").strip()
        try:
            self.github.store_token(token or None)
        except RuntimeError as exc:
            self.logger.debug("Failed to store GitHub token", exc_info=True)
            return jsonify({"error": str(exc)}), 500

        verification = self.github.verify_token(token or None)
        message = verification.get("message") or ("API Token Invalid" if token else "API Token Not Configured")

        try:
            self.github.refresh_default_repo_hash(force=True)
        except Exception:
            self.logger.debug("Failed to refresh default repo hash after token update", exc_info=True)

        payload = {
            "token": token,
            "has_token": bool(token),
            "valid": bool(verification.get("valid")),
            "message": message,
            "status": verification.get("status") or ("missing" if not token else "unknown"),
            "rate_limit": verification.get("rate_limit"),
            "error": verification.get("error"),
            "checked_at": _now_ts(),
            "configured": True,
            "locked": False,
            "reset_required": False,
            "reset_at": 0,
        }
        return jsonify(payload)


def register_github_token_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register GitHub API token administration endpoints."""

    service = GitHubTokenService(app, adapters)
    blueprint = Blueprint("github_access", __name__)

    @blueprint.route("/api/github/token", methods=["GET"])
    def _github_token_get():
        return service.get_token()

    @blueprint.route("/api/github/token", methods=["POST"])
    def _github_token_post():
        return service.update_token()

    app.register_blueprint(blueprint)
