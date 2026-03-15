# ======================================================
# Data\Engine\services\API\access_management\login.py
# Description: Primary authentication blueprint used by the Engine auth group for sessions, MFA, and logout.
#
# API Endpoints (if applicable):
# - POST /api/auth/login (No Authentication) - Authenticates operator credentials and starts a session token.
# - POST /api/auth/logout (Token Authenticated) - Clears the active operator session and authentication cookie.
# - POST /api/auth/mfa/verify (Token Authenticated (MFA pending)) - Verifies TOTP codes during multifactor setup or login.
# - GET /api/auth/me (Token Authenticated) - Returns the currently authenticated operator profile.
# ======================================================

"""Authentication endpoints for the Borealis Engine API."""
from __future__ import annotations

import base64
import hashlib
import io
import logging
import os
from Data.Engine.db import dbapi as sqlite3
import time
import uuid
from typing import Any, Dict, Mapping, Optional, Sequence, TYPE_CHECKING

from flask import Blueprint, Flask, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

try:
    import pyotp  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    pyotp = None  # type: ignore

try:
    import qrcode  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    qrcode = None  # type: ignore

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from Data.Engine.services.API import EngineServiceAdapters

from ...auth.secrets import require_app_secret
from .github import register_github_token_management
from .multi_factor_authentication import register_mfa_management
from .users import register_user_management

_logger = logging.getLogger(__name__)
_qr_logger_warning_emitted = False


def _now_ts() -> int:
    return int(time.time())


def _sha512_hex(value: str) -> str:
    return hashlib.sha512((value or "").encode("utf-8")).hexdigest()


def _generate_totp_secret() -> str:
    if not pyotp:
        raise RuntimeError("pyotp is not installed; MFA unavailable")
    return pyotp.random_base32()


def _totp_for_secret(secret: str):
    if not pyotp:
        raise RuntimeError("pyotp is not installed; MFA unavailable")
    normalized = (secret or "").replace(" ", "").strip().upper()
    if not normalized:
        raise ValueError("empty MFA secret")
    return pyotp.TOTP(normalized, digits=6, interval=30)


def _totp_provisioning_uri(secret: str, username: str) -> Optional[str]:
    try:
        totp = _totp_for_secret(secret)
    except Exception:
        return None
    issuer = os.environ.get("BOREALIS_MFA_ISSUER", "Borealis")
    return totp.provisioning_uri(name=username or "user", issuer_name=issuer)


def _totp_qr_data_uri(payload: str) -> Optional[str]:
    global _qr_logger_warning_emitted
    if not payload:
        return None
    if qrcode is None:
        if not _qr_logger_warning_emitted:
            _logger.warning("MFA QR generation skipped: 'qrcode' dependency not available.")
            _qr_logger_warning_emitted = True
        return None
    try:
        image = qrcode.make(payload, box_size=6, border=4)
        buffer = io.BytesIO()
        image.save(buffer, format="PNG")
        encoded = base64.b64encode(buffer.getvalue()).decode("ascii")
        return f"data:image/png;base64,{encoded}"
    except Exception as exc:
        if not _qr_logger_warning_emitted:
            _logger.warning("Failed to generate MFA QR code: %s", exc, exc_info=True)
            _qr_logger_warning_emitted = True
        return None


def _user_row_to_dict(row: Sequence[Any]) -> Mapping[str, Any]:
    mfa_enabled = 0
    if len(row) > 7:
        try:
            mfa_enabled = 1 if (row[7] or 0) else 0
        except Exception:
            mfa_enabled = 0
    return {
        "id": row[0],
        "username": row[1],
        "display_name": row[2] or row[1],
        "role": row[3] or "User",
        "last_login": row[4] or 0,
        "created_at": row[5] or 0,
        "updated_at": row[6] or 0,
        "mfa_enabled": mfa_enabled,
    }


class _AuthService:
    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.context = adapters.context
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _make_token(self, username: str, role: str) -> str:
        serializer = self._token_serializer()
        payload = {"u": username, "r": role or "User", "ts": _now_ts()}
        return serializer.dumps(payload)

    def _verify_token(self, token: str) -> Optional[Mapping[str, Any]]:
        try:
            serializer = self._token_serializer()
            max_age = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
            data = serializer.loads(token, max_age=max_age)
            return {"username": data.get("u"), "role": data.get("r") or "User"}
        except (BadSignature, SignatureExpired, Exception):
            return None

    def _current_user(self) -> Optional[Mapping[str, Any]]:
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
        if token:
            return self._verify_token(token)
        return None

    def _update_last_login(self, username: str) -> None:
        if not username:
            return
        try:
            conn = self._db_conn()
            try:
                cur = conn.cursor()
                now_ts = _now_ts()
                cur.execute(
                    "UPDATE users SET last_login=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
                    (now_ts, now_ts, username),
                )
                conn.commit()
            finally:
                conn.close()
        except Exception:
            self.logger.debug("Failed to update last_login for %s", username, exc_info=True)

    def _finalize_login(self, username: str, role: str):
        session.pop("mfa_pending", None)
        session["username"] = username
        session["role"] = role
        self._update_last_login(username)

        token = self._make_token(username, role or "User")
        response = jsonify({"status": "ok", "username": username, "role": role, "token": token})

        samesite = self.app.config.get("SESSION_COOKIE_SAMESITE", "Lax")
        secure = bool(self.app.config.get("SESSION_COOKIE_SECURE", False))
        domain = self.app.config.get("SESSION_COOKIE_DOMAIN")
        response.set_cookie(
            "borealis_auth",
            token,
            httponly=False,
            samesite=samesite,
            secure=secure,
            domain=domain,
            path="/",
        )
        return response

    def login(self):
        payload = request.get_json(silent=True) or {}
        username = (payload.get("username") or "").strip()
        password = payload.get("password")
        password_sha512 = (payload.get("password_sha512") or "").strip().lower()

        if not username or (not password and not password_sha512):
            return jsonify({"error": "missing credentials"}), 400

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    username,
                    display_name,
                    password_sha512,
                    role,
                    last_login,
                    created_at,
                    updated_at,
                    COALESCE(mfa_enabled, 0) AS mfa_enabled,
                    COALESCE(mfa_secret, '') AS mfa_secret
                FROM users WHERE LOWER(username)=LOWER(?)
                """,
                (username,),
            )
            row = cur.fetchone()
        finally:
            conn.close()

        if not row:
            return jsonify({"error": "invalid username or password"}), 401

        stored_hash = (row[3] or "").lower()
        check_hash = password_sha512 or _sha512_hex(password or "")
        if stored_hash != (check_hash or "").lower():
            return jsonify({"error": "invalid username or password"}), 401

        role = row[4] or "User"
        mfa_enabled = bool(row[8] or 0)
        existing_secret = (row[9] or "").strip()

        session.pop("username", None)
        session.pop("role", None)

        if not mfa_enabled:
            session.pop("mfa_pending", None)
            return self._finalize_login(row[1], role)

        stage = "verify" if existing_secret else "setup"
        pending_token = uuid.uuid4().hex
        pending = {
            "username": row[1],
            "role": role,
            "token": pending_token,
            "stage": stage,
            "expires": _now_ts() + 300,
        }

        secret = None
        otpauth_url = None
        qr_image = None

        if stage == "setup":
            try:
                secret = _generate_totp_secret()
            except Exception as exc:
                return jsonify({"error": f"MFA setup unavailable: {exc}"}), 500
            pending["secret"] = secret
            otpauth_url = _totp_provisioning_uri(secret, row[1])
            if otpauth_url:
                qr_image = _totp_qr_data_uri(otpauth_url)
        else:
            pending["secret"] = None

        session["mfa_pending"] = pending
        session.modified = True

        response_payload: Dict[str, Any] = {
            "status": "mfa_required",
            "stage": stage,
            "pending_token": pending_token,
            "username": row[1],
            "role": role,
        }
        if stage == "setup":
            response_payload.update(
                {
                    "secret": secret,
                    "otpauth_url": otpauth_url,
                    "qr_image": qr_image,
                }
            )
        return jsonify(response_payload)

    def logout(self):
        session.clear()
        response = jsonify({"status": "ok"})
        response.set_cookie("borealis_auth", "", expires=0, path="/")
        return response

    def mfa_verify(self):
        pending = session.get("mfa_pending") or {}
        if not pending or not isinstance(pending, dict):
            return jsonify({"error": "mfa_pending"}), 401

        payload = request.get_json(silent=True) or {}
        token = (payload.get("pending_token") or "").strip()
        code_raw = str(payload.get("code") or "").strip()
        code = "".join(ch for ch in code_raw if ch.isdigit())

        if not token or token != pending.get("token"):
            return jsonify({"error": "invalid_session"}), 401
        if pending.get("expires", 0) < _now_ts():
            session.pop("mfa_pending", None)
            return jsonify({"error": "expired"}), 401
        if len(code) < 6:
            return jsonify({"error": "invalid_code"}), 400

        username = pending.get("username") or ""
        role = pending.get("role") or "User"
        stage = pending.get("stage") or "verify"

        try:
            if stage == "setup":
                secret = pending.get("secret") or ""
                totp = _totp_for_secret(secret)
                if not totp.verify(code, valid_window=1):
                    return jsonify({"error": "invalid_code"}), 401
                now_ts = _now_ts()
                conn = self._db_conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        "UPDATE users SET mfa_secret=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
                        (secret, now_ts, username),
                    )
                    conn.commit()
                finally:
                    conn.close()
            else:
                conn = self._db_conn()
                try:
                    cur = conn.cursor()
                    cur.execute(
                        "SELECT COALESCE(mfa_secret,'') FROM users WHERE LOWER(username)=LOWER(?)",
                        (username,),
                    )
                    row = cur.fetchone()
                finally:
                    conn.close()

                secret = (row[0] or "").strip() if row else ""
                if not secret:
                    return jsonify({"error": "mfa_not_configured"}), 403
                totp = _totp_for_secret(secret)
                if not totp.verify(code, valid_window=1):
                    return jsonify({"error": "invalid_code"}), 401
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

        return self._finalize_login(username, role)

    def me(self):
        user = self._current_user()
        if not user:
            return jsonify({"error": "unauthorized"}), 401

        username = (user.get("username") or "").strip()
        try:
            conn = self._db_conn()
            try:
                cur = conn.cursor()
                cur.execute(
                    "SELECT id, username, display_name, role, last_login, created_at, updated_at FROM users WHERE LOWER(username)=LOWER(?)",
                    (username,),
                )
                row = cur.fetchone()
            finally:
                conn.close()
            if row:
                info = _user_row_to_dict(row)
                return jsonify(
                    {
                        "username": info["username"],
                        "display_name": info["display_name"],
                        "role": info["role"],
                    }
                )
        except Exception:
            self.logger.debug("Failed to fetch user record for %s", username, exc_info=True)

        return jsonify(
            {
                "username": username,
                "display_name": username,
                "role": user.get("role") or "User",
            }
        )


def register_auth(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register authentication endpoints for the Engine."""

    service = _AuthService(app, adapters)
    blueprint = Blueprint("auth", __name__)

    @blueprint.route("/api/auth/login", methods=["POST"])
    def _login():
        return service.login()

    @blueprint.route("/api/auth/logout", methods=["POST"])
    def _logout():
        return service.logout()

    @blueprint.route("/api/auth/mfa/verify", methods=["POST"])
    def _mfa_verify():
        return service.mfa_verify()

    @blueprint.route("/api/auth/me", methods=["GET"])
    def _me():
        return service.me()

    app.register_blueprint(blueprint)
    register_user_management(app, adapters)
    register_mfa_management(app, adapters)
    register_github_token_management(app, adapters)
