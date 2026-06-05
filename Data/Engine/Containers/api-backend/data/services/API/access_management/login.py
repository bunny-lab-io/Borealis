# ======================================================
# Data\Engine\services\API\access_management\login.py
# Description: Primary authentication blueprint used by the Engine auth group for sessions, MFA, and logout.
#
# API Endpoints (if applicable):
# - POST /api/auth/login (No Authentication) - Authenticates operator credentials and starts a session token or MFA setup/verification challenge.
# - POST /api/auth/mfa/verify (Token Authenticated (MFA pending)) - Verifies TOTP codes during multifactor setup or login.
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
from typing import Any, Dict, Mapping, Optional, TYPE_CHECKING

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

from ...aegis_cipher import (
    AegisCipherServiceError,
    AegisDataCorruptionError,
    AegisLockedError,
    AegisNotConfiguredError,
)
from ...auth.bootstrap_state import (
    determine_bootstrap_state,
    operator_auth_allowed,
)
from ...auth.context import revalidate_operator_identity
from ...auth.secrets import require_app_secret
from .aegis import register_aegis_cipher_management

DIRECTORY_AUTH_SOURCE = "directory"

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


class _AuthService:
    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.context = adapters.context
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger
        self.aegis_cipher_service = adapters.aegis_cipher_service

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

    def _bootstrap_state(self) -> Dict[str, Any]:
        return determine_bootstrap_state(
            db_conn_factory=self.db_conn_factory,
            aegis_cipher_service=self.aegis_cipher_service,
        )

    def _public_bootstrap_state(self) -> Dict[str, Any]:
        state = self._bootstrap_state()
        return {
            "phase": state["phase"],
            "configured": bool(state["configured"]),
            "locked": bool(state["locked"]),
        }

    def _operator_auth_allowed(self) -> bool:
        return operator_auth_allowed(
            db_conn_factory=self.db_conn_factory,
            aegis_cipher_service=self.aegis_cipher_service,
        )

    def _bootstrap_error_response(self, *, status_code: int = 423):
        payload = self._public_bootstrap_state()
        return jsonify({"error": "bootstrap_required", **payload}), status_code

    def _encrypt_auth_secret(self, value: str) -> str:
        try:
            return self.aegis_cipher_service.encrypt_secret_for_text(value) or ""
        except AegisNotConfiguredError as exc:
            raise AegisCipherServiceError(str(exc)) from exc
        except AegisLockedError as exc:
            raise AegisCipherServiceError(str(exc)) from exc

    def _decrypt_auth_secret(self, value: Any) -> str:
        text = str(value or "")
        if text == "":
            return ""
        return self.aegis_cipher_service.decrypt_secret_text(text)

    def _clear_operator_session(self) -> None:
        session.pop("username", None)
        session.pop("role", None)
        session.pop("mfa_pending", None)

    def _clear_all_auth_sessions(self):
        session.clear()
        response = jsonify({"status": "ok"})
        response.set_cookie("borealis_auth", "", expires=0, path="/")
        return response

    def _load_pending_mfa(self, token: str):
        pending = session.get("mfa_pending") or {}
        if not pending or not isinstance(pending, dict):
            return None, ({"error": "mfa_pending"}, 401)
        if not token or token != pending.get("token"):
            return None, ({"error": "invalid_session"}, 401)
        if pending.get("expires", 0) < _now_ts():
            session.pop("mfa_pending", None)
            return None, ({"error": "expired"}, 401)
        return pending, None

    def _load_user_identity(self, username: str) -> Optional[Mapping[str, Any]]:
        username_norm = str(username or "").strip()
        if not username_norm:
            return None
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    username,
                    COALESCE(display_name, username),
                    COALESCE(role, 'User'),
                    COALESCE(auth_source, 'local'),
                    COALESCE(directory_disabled, 0)
                FROM users
                WHERE LOWER(username)=LOWER(?)
                LIMIT 1
                """,
                (username_norm,),
            )
            row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            return None
        return {
            "id": int(row[0] or 0),
            "username": str(row[1] or username_norm),
            "display_name": str(row[2] or row[1] or username_norm),
            "role": str(row[3] or "User"),
            "auth_source": str(row[4] or "local"),
            "directory_disabled": int(row[5] or 0),
        }

    def _load_user_identity_by_id(self, user_id: Any) -> Optional[Mapping[str, Any]]:
        try:
            normalized_user_id = int(user_id or 0)
        except Exception:
            return None
        if normalized_user_id <= 0:
            return None
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    username,
                    COALESCE(display_name, username),
                    COALESCE(role, 'User'),
                    COALESCE(auth_source, 'local'),
                    COALESCE(directory_disabled, 0)
                FROM users
                WHERE id=?
                LIMIT 1
                """,
                (normalized_user_id,),
            )
            row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            return None
        return {
            "id": int(row[0] or 0),
            "username": str(row[1] or ""),
            "display_name": str(row[2] or row[1] or ""),
            "role": str(row[3] or "User"),
            "auth_source": str(row[4] or "local"),
            "directory_disabled": int(row[5] or 0),
        }

    def _load_login_row(self, username: str):
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
                    COALESCE(mfa_secret, '') AS mfa_secret,
                    COALESCE(mfa_disabled, 0) AS mfa_disabled,
                    COALESCE(auth_reset_required, 0) AS auth_reset_required,
                    COALESCE(auth_source, 'local') AS auth_source,
                    COALESCE(directory_disabled, 0) AS directory_disabled
                FROM users WHERE LOWER(username)=LOWER(?)
                """,
                (username,),
            )
            return cur.fetchone()
        finally:
            conn.close()

    def _current_user(self) -> Optional[Mapping[str, Any]]:
        if not self._operator_auth_allowed():
            return None

        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return revalidate_operator_identity(
                self.db_conn_factory,
                username=str(username),
                role=str(role),
                logger=self.logger,
            )

        token = None
        auth_header = request.headers.get("Authorization") or ""
        if auth_header.lower().startswith("bearer "):
            token = auth_header.split(" ", 1)[1].strip()
        if not token:
            token = request.cookies.get("borealis_auth")
        if token:
            verified = self._verify_token(token)
            if not verified:
                return None
            return revalidate_operator_identity(
                self.db_conn_factory,
                username=str(verified.get("username") or ""),
                role=str(verified.get("role") or "User"),
                logger=self.logger,
            )
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

    def _begin_mfa_or_finalize(self, *, username: str, role: str, existing_secret: str, mfa_disabled: bool):
        available_methods = ["totp"] if existing_secret else []
        setup_methods = ["totp"]

        session.pop("username", None)
        session.pop("role", None)

        if mfa_disabled:
            session.pop("mfa_pending", None)
            return self._finalize_login(username, role)

        stage = "verify" if available_methods else "setup"
        pending_token = uuid.uuid4().hex
        pending = {
            "username": username,
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
            otpauth_url = _totp_provisioning_uri(secret, username)
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
            "username": username,
            "role": role,
            "available_methods": available_methods if stage == "verify" else setup_methods,
            "preferred_method": "totp",
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

    def login(self):
        if not self._operator_auth_allowed():
            return self._bootstrap_error_response()

        payload = request.get_json(silent=True) or {}
        username = (payload.get("username") or "").strip()
        password = payload.get("password")
        password_sha512 = (payload.get("password_sha512") or "").strip().lower()

        if not username or (not password and not password_sha512):
            return jsonify({"error": "missing credentials"}), 400

        row = self._load_login_row(username)

        if not row:
            return jsonify({"error": "invalid username or password"}), 401

        auth_source = str(row[12] or "local").strip().lower() if len(row) > 12 else "local"
        if auth_source == DIRECTORY_AUTH_SOURCE:
            if bool(row[13] or 0):
                return jsonify({"error": "directory_user_disabled"}), 403
            return jsonify({"error": "directory_auth_go_owned"}), 410

        if bool(row[11] or 0):
            return jsonify({"error": "auth_reset_required"}), 423

        try:
            stored_hash = self._decrypt_auth_secret(row[3]).lower()
            existing_secret = self._decrypt_auth_secret(row[9]).strip()
        except AegisDataCorruptionError:
            self.aegis_cipher_service.migrate_operator_auth_if_needed()
            row = self._load_login_row(username)
            if not row:
                return jsonify({"error": "invalid username or password"}), 401
            stored_hash = self._decrypt_auth_secret(row[3]).lower()
            existing_secret = self._decrypt_auth_secret(row[9]).strip()
        except Exception as exc:
            return jsonify({"error": str(exc) or "auth_secret_unavailable"}), 500
        check_hash = password_sha512 or _sha512_hex(password or "")
        if stored_hash != (check_hash or "").lower():
            return jsonify({"error": "invalid username or password"}), 401

        role = row[4] or "User"
        mfa_disabled = bool(row[10] or 0)
        return self._begin_mfa_or_finalize(
            username=row[1],
            role=role,
            existing_secret=existing_secret,
            mfa_disabled=mfa_disabled,
        )

    def mfa_verify(self):
        if not self._operator_auth_allowed():
            return self._bootstrap_error_response()

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
                        "UPDATE users SET mfa_enabled=1, mfa_disabled=0, mfa_secret=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
                        (self._encrypt_auth_secret(secret), now_ts, username),
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

                secret = self._decrypt_auth_secret(row[0]) if row else ""
                if not secret:
                    return jsonify({"error": "mfa_not_configured"}), 403
                totp = _totp_for_secret(secret)
                if not totp.verify(code, valid_window=1):
                    return jsonify({"error": "invalid_code"}), 401
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

        identity = revalidate_operator_identity(
            self.db_conn_factory,
            username=str(username),
            role=str(role),
            logger=self.logger,
        )
        if not identity:
            session.pop("mfa_pending", None)
            return jsonify({"error": "user_disabled"}), 403

        return self._finalize_login(username, role)

def register_auth(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register authentication endpoints for the Engine."""

    service = _AuthService(app, adapters)
    blueprint = Blueprint("auth", __name__)

    @blueprint.route("/api/auth/login", methods=["POST"])
    def _login():
        return service.login()

    @blueprint.route("/api/auth/mfa/verify", methods=["POST"])
    def _mfa_verify():
        return service.mfa_verify()

    app.register_blueprint(blueprint)
    register_aegis_cipher_management(app, adapters)
