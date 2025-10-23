"""Operator authentication HTTP endpoints."""

from __future__ import annotations

from typing import Any, Dict

from flask import Blueprint, Flask, current_app, jsonify, request, session

from Data.Engine.builders import build_login_request, build_mfa_request
from Data.Engine.domain import OperatorLoginSuccess, OperatorMFAChallenge
from Data.Engine.services.auth import (
    InvalidCredentialsError,
    InvalidMFACodeError,
    MFAUnavailableError,
    MFASessionError,
    OperatorAuthService,
)
from Data.Engine.services.container import EngineServiceContainer


def _service(container: EngineServiceContainer) -> OperatorAuthService:
    return container.operator_auth_service


def register(app: Flask, services: EngineServiceContainer) -> None:
    bp = Blueprint("auth", __name__)

    @bp.route("/api/auth/login", methods=["POST"])
    def login() -> Any:
        payload = request.get_json(silent=True) or {}
        try:
            login_request = build_login_request(payload)
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400

        service = _service(services)

        try:
            result = service.authenticate(login_request)
        except InvalidCredentialsError:
            return jsonify({"error": "invalid username or password"}), 401
        except MFAUnavailableError as exc:
            current_app.logger.error("mfa unavailable: %s", exc)
            return jsonify({"error": str(exc)}), 500

        session.pop("username", None)
        session.pop("role", None)

        if isinstance(result, OperatorLoginSuccess):
            session.pop("mfa_pending", None)
            session["username"] = result.username
            session["role"] = result.role or "User"
            response = jsonify(
                {"status": "ok", "username": result.username, "role": result.role, "token": result.token}
            )
            _set_auth_cookie(response, result.token)
            return response

        challenge = result
        session["mfa_pending"] = {
            "username": challenge.username,
            "role": challenge.role,
            "stage": challenge.stage,
            "token": challenge.pending_token,
            "expires": challenge.expires_at,
            "secret": challenge.secret,
        }
        session.modified = True

        payload: Dict[str, Any] = {
            "status": "mfa_required",
            "stage": challenge.stage,
            "pending_token": challenge.pending_token,
            "username": challenge.username,
            "role": challenge.role,
        }
        if challenge.stage == "setup":
            if challenge.secret:
                payload["secret"] = challenge.secret
            if challenge.otpauth_url:
                payload["otpauth_url"] = challenge.otpauth_url
            if challenge.qr_image:
                payload["qr_image"] = challenge.qr_image
        return jsonify(payload)

    @bp.route("/api/auth/logout", methods=["POST"])
    def logout() -> Any:
        session.clear()
        response = jsonify({"status": "ok"})
        _set_auth_cookie(response, "", expires=0)
        return response

    @bp.route("/api/auth/me", methods=["GET"])
    def me() -> Any:
        service = _service(services)

        account = None
        username = session.get("username")
        if isinstance(username, str) and username:
            account = service.fetch_account(username)

        if account is None:
            token = request.cookies.get("borealis_auth", "")
            if not token:
                auth_header = request.headers.get("Authorization", "")
                if auth_header.lower().startswith("bearer "):
                    token = auth_header.split(None, 1)[1]
            account = service.resolve_token(token)
            if account is not None:
                session["username"] = account.username
                session["role"] = account.role or "User"

        if account is None:
            return jsonify({"error": "not_authenticated"}), 401

        payload = {
            "username": account.username,
            "display_name": account.display_name or account.username,
            "role": account.role,
        }
        return jsonify(payload)

    @bp.route("/api/auth/mfa/verify", methods=["POST"])
    def verify_mfa() -> Any:
        pending = session.get("mfa_pending")
        if not isinstance(pending, dict):
            return jsonify({"error": "mfa_pending"}), 401

        try:
            request_payload = build_mfa_request(request.get_json(silent=True) or {})
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400

        challenge = OperatorMFAChallenge(
            username=str(pending.get("username") or ""),
            role=str(pending.get("role") or "User"),
            stage=str(pending.get("stage") or "verify"),
            pending_token=str(pending.get("token") or ""),
            expires_at=int(pending.get("expires") or 0),
            secret=str(pending.get("secret") or "") or None,
        )

        service = _service(services)

        try:
            result = service.verify_mfa(challenge, request_payload)
        except MFASessionError as exc:
            error_key = str(exc)
            status = 401 if error_key != "mfa_not_configured" else 403
            if error_key not in {"expired", "invalid_session", "mfa_not_configured"}:
                error_key = "invalid_session"
            session.pop("mfa_pending", None)
            return jsonify({"error": error_key}), status
        except InvalidMFACodeError as exc:
            return jsonify({"error": str(exc) or "invalid_code"}), 401
        except MFAUnavailableError as exc:
            current_app.logger.error("mfa unavailable: %s", exc)
            return jsonify({"error": str(exc)}), 500
        except InvalidCredentialsError:
            session.pop("mfa_pending", None)
            return jsonify({"error": "invalid username or password"}), 401

        session.pop("mfa_pending", None)
        session["username"] = result.username
        session["role"] = result.role or "User"
        payload = {
            "status": "ok",
            "username": result.username,
            "role": result.role,
            "token": result.token,
        }
        response = jsonify(payload)
        _set_auth_cookie(response, result.token)
        return response

    app.register_blueprint(bp)


def _set_auth_cookie(response, value: str, *, expires: int | None = None) -> None:
    same_site = current_app.config.get("SESSION_COOKIE_SAMESITE", "Lax")
    secure = bool(current_app.config.get("SESSION_COOKIE_SECURE", False))
    domain = current_app.config.get("SESSION_COOKIE_DOMAIN", None)
    response.set_cookie(
        "borealis_auth",
        value,
        httponly=False,
        samesite=same_site,
        secure=secure,
        domain=domain,
        path="/",
        expires=expires,
    )


__all__ = ["register"]
