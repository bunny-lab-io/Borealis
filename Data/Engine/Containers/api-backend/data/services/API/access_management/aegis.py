# ======================================================
# Data\Engine\services\API\access_management\aegis.py
# Description: Aegis Cipher status and unlock lifecycle endpoints for Engine secret storage.
#
# API Endpoints (if applicable):
# - GET /api/aegis/status (Token Authenticated) - Returns Aegis Cipher setup and lock state.
# - POST /api/aegis/setup (Token Authenticated (Admin)) - Configures Aegis Cipher and encrypts protected secrets.
# - POST /api/aegis/unlock (Token Authenticated (Admin)) - Unlocks Aegis-protected secrets for the current Engine process.
# - POST /api/aegis/rotate (Token Authenticated (Admin)) - Rotates the configured Aegis Cipher and re-encrypts protected secrets.
# - POST /api/aegis/force_reset (Token Authenticated (Admin)) - Destroys unrecoverable protected secrets and clears Aegis configuration.
# ======================================================

"""Aegis Cipher endpoints for the Borealis Engine runtime."""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, Tuple

from flask import Blueprint, Flask, jsonify, request

from ...auth import RequestAuthContext
from ...auth.secrets import require_app_secret
from ...aegis_cipher import (
    AegisCipherServiceError,
    AegisDataCorruptionError,
    AegisInvalidCipherError,
    AegisLockedError,
    AegisNotConfiguredError,
)
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, validate_internal_token

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters


def _error_payload(error: str, message: str, status: int) -> Tuple[Any, int]:
    return jsonify({"error": error, "message": message}), status


def register_aegis_cipher_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register Aegis Cipher lifecycle endpoints."""

    service = adapters.aegis_cipher_service
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )
    blueprint = Blueprint("aegis_access", __name__)

    def _require_internal() -> bool:
        try:
            secret = require_app_secret(app)
        except Exception:
            return False
        return validate_internal_token(secret, request.headers.get(INTERNAL_TOKEN_HEADER))

    @blueprint.route("/api/internal/aegis/status", methods=["GET"])
    def _aegis_internal_status():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        return jsonify(service.status())

    @blueprint.route("/api/internal/aegis/setup", methods=["POST"])
    def _aegis_internal_setup():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        data = request.get_json(silent=True) or {}
        cipher = str(data.get("cipher") or "")
        try:
            payload = service.setup(cipher)
        except AegisNotConfiguredError as exc:
            return _error_payload("not_configured", str(exc), 409)
        except AegisLockedError as exc:
            return _error_payload("locked", str(exc), 423)
        except AegisDataCorruptionError as exc:
            return _error_payload("corrupt_secret_store", str(exc), 500)
        except AegisCipherServiceError as exc:
            message = str(exc)
            error_key = "already_configured" if "already configured" in message.lower() else "invalid_request"
            status_code = 409 if error_key == "already_configured" else 400
            return _error_payload(error_key, message, status_code)
        return jsonify({"status": "ok", **payload})

    @blueprint.route("/api/internal/aegis/unlock", methods=["POST"])
    def _aegis_internal_unlock():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        data = request.get_json(silent=True) or {}
        cipher = str(data.get("cipher") or "")
        try:
            payload = service.unlock(cipher)
        except AegisInvalidCipherError as exc:
            return _error_payload("invalid_cipher", str(exc), 401)
        except AegisNotConfiguredError as exc:
            return _error_payload("not_configured", str(exc), 409)
        except AegisDataCorruptionError as exc:
            return _error_payload("corrupt_secret_store", str(exc), 500)
        except AegisCipherServiceError as exc:
            return _error_payload("invalid_request", str(exc), 400)
        return jsonify({"status": "ok", **payload})

    @blueprint.route("/api/aegis/status", methods=["GET"])
    def _aegis_status():
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = service.status()
        payload["user_role"] = user.get("role") or "User"
        return jsonify(payload)

    @blueprint.route("/api/aegis/setup", methods=["POST"])
    def _aegis_setup():
        user, login_error = auth.require_user()
        if login_error:
            return jsonify(login_error[0]), login_error[1]
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        data = request.get_json(silent=True) or {}
        cipher = str(data.get("cipher") or "")
        try:
            payload = service.setup(cipher)
        except AegisNotConfiguredError as exc:
            return _error_payload("not_configured", str(exc), 409)
        except AegisLockedError as exc:
            return _error_payload("locked", str(exc), 423)
        except AegisDataCorruptionError as exc:
            return _error_payload("corrupt_secret_store", str(exc), 500)
        except AegisCipherServiceError as exc:
            message = str(exc)
            error_key = "already_configured" if "already configured" in message.lower() else "invalid_request"
            status_code = 409 if error_key == "already_configured" else 400
            return _error_payload(error_key, message, status_code)
        adapters.service_log(
            "aegis_cipher",
            f"Aegis Cipher setup completed by {user.get('username')}.",
            scope="ADMIN",
        )
        return jsonify({"status": "ok", **payload})

    @blueprint.route("/api/aegis/unlock", methods=["POST"])
    def _aegis_unlock():
        user, login_error = auth.require_user()
        if login_error:
            return jsonify(login_error[0]), login_error[1]
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        data = request.get_json(silent=True) or {}
        cipher = str(data.get("cipher") or "")
        try:
            payload = service.unlock(cipher)
        except AegisInvalidCipherError as exc:
            return _error_payload("invalid_cipher", str(exc), 401)
        except AegisNotConfiguredError as exc:
            return _error_payload("not_configured", str(exc), 409)
        except AegisDataCorruptionError as exc:
            return _error_payload("corrupt_secret_store", str(exc), 500)
        except AegisCipherServiceError as exc:
            return _error_payload("invalid_request", str(exc), 400)
        adapters.service_log(
            "aegis_cipher",
            f"Aegis Cipher unlocked by {user.get('username')}.",
            scope="ADMIN",
        )
        return jsonify({"status": "ok", **payload})

    @blueprint.route("/api/aegis/rotate", methods=["POST"])
    def _aegis_rotate():
        user, login_error = auth.require_user()
        if login_error:
            return jsonify(login_error[0]), login_error[1]
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        data = request.get_json(silent=True) or {}
        current_cipher = str(data.get("current_cipher") or "")
        new_cipher = str(data.get("new_cipher") or "")
        try:
            payload = service.rotate(current_cipher, new_cipher)
        except AegisInvalidCipherError as exc:
            return _error_payload("invalid_cipher", str(exc), 401)
        except AegisNotConfiguredError as exc:
            return _error_payload("not_configured", str(exc), 409)
        except AegisLockedError as exc:
            return _error_payload("locked", str(exc), 423)
        except AegisDataCorruptionError as exc:
            return _error_payload("corrupt_secret_store", str(exc), 500)
        except AegisCipherServiceError as exc:
            return _error_payload("invalid_request", str(exc), 400)
        adapters.service_log(
            "aegis_cipher",
            f"Aegis Cipher rotated by {user.get('username')}.",
            scope="ADMIN",
        )
        return jsonify({"status": "ok", **payload})

    @blueprint.route("/api/aegis/force_reset", methods=["POST"])
    def _aegis_force_reset():
        user, login_error = auth.require_user()
        if login_error:
            return jsonify(login_error[0]), login_error[1]
        admin_error = auth.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        try:
            payload = service.force_reset()
        except AegisNotConfiguredError as exc:
            return _error_payload("not_configured", str(exc), 409)
        except AegisDataCorruptionError as exc:
            return _error_payload("corrupt_secret_store", str(exc), 500)
        except AegisCipherServiceError as exc:
            return _error_payload("invalid_request", str(exc), 400)
        adapters.service_log(
            "aegis_cipher",
            f"Aegis Cipher force reset by {user.get('username')}. Protected secret material was destroyed.",
            scope="ADMIN",
            level="WARNING",
        )
        return jsonify({"status": "ok", **payload})

    app.register_blueprint(blueprint)
