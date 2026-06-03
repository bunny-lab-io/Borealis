# ======================================================
# Data\Engine\services\API\access_management\login.py
# Description: Primary authentication blueprint used by the Engine auth group for sessions, MFA, and logout.
#
# API Endpoints (if applicable):
# - GET /api/internal/bootstrap/state (Internal Token) - Returns bootstrap state for Go auth bridge.
# - POST /api/auth/login (No Authentication) - Authenticates operator credentials and starts a session token or MFA setup/verification challenge.
# - POST /api/auth/mfa/verify (Token Authenticated (MFA pending)) - Verifies TOTP codes during multifactor setup or login.
# - POST /api/auth/passkeys/register/options (Token Authenticated) - Starts a passkey registration ceremony for the current operator.
# - POST /api/auth/passkeys/register/verify (Token Authenticated) - Verifies a passkey registration response and stores the credential.
# - POST /api/auth/passkeys/authenticate/options (No Authentication) - Starts a passkey sign-in ceremony for passwordless operator login.
# - POST /api/auth/passkeys/authenticate/verify (No Authentication) - Verifies a passkey sign-in response and completes operator login.
# ======================================================

"""Authentication endpoints for the Borealis Engine API."""
from __future__ import annotations

import base64
import hashlib
import io
import json
import logging
import os
from Data.Engine.db import dbapi as sqlite3
import time
import uuid
from urllib.parse import urlsplit
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

try:
    from webauthn import (  # type: ignore
        base64url_to_bytes,
        generate_authentication_options,
        generate_registration_options,
        options_to_json,
        verify_authentication_response,
        verify_registration_response,
    )
    from webauthn.helpers.structs import (  # type: ignore
        AuthenticatorSelectionCriteria,
        PublicKeyCredentialDescriptor,
        ResidentKeyRequirement,
        UserVerificationRequirement,
    )
except Exception:  # pragma: no cover - optional dependency
    base64url_to_bytes = None  # type: ignore
    generate_authentication_options = None  # type: ignore
    generate_registration_options = None  # type: ignore
    options_to_json = None  # type: ignore
    verify_authentication_response = None  # type: ignore
    verify_registration_response = None  # type: ignore
    AuthenticatorSelectionCriteria = None  # type: ignore
    PublicKeyCredentialDescriptor = None  # type: ignore
    ResidentKeyRequirement = None  # type: ignore
    UserVerificationRequirement = None  # type: ignore

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from Data.Engine.services.API import EngineServiceAdapters

from ....public_endpoints import public_base_url
from ...aegis_cipher import (
    AegisCipherServiceError,
    AegisDataCorruptionError,
    AegisLockedError,
    AegisNotConfiguredError,
)
from ...auth.bootstrap_state import (
    BOOTSTRAP_PHASE_ADMIN_RECOVERY_REQUIRED,
    BOOTSTRAP_PHASE_ADMIN_SETUP_REQUIRED,
    determine_bootstrap_state,
    operator_auth_allowed,
)
from ...auth.context import revalidate_operator_identity
from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, validate_internal_token
from .aegis import register_aegis_cipher_management
from .credentials import register_credential_management
from .directory_services import (
    DIRECTORY_AUTH_SOURCE,
    DirectoryAuthenticationManager,
    DirectoryAuthError,
    register_directory_services,
)
from .github import register_github_token_management
from .passkeys import (
    build_passkey_lookup_hmac,
    build_webauthn_user_id,
    count_user_passkeys,
    credential_lookup_candidates,
    deserialize_passkey_secret_bundle,
    delete_user_passkeys,
    get_passkey_by_lookup_hmac,
    get_passkey_by_credential_id,
    list_user_passkeys,
    normalize_webauthn_storage_value,
    serialize_passkey_secret_bundle,
    serialize_transports,
)

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


def _bytes_to_base64url(value: bytes) -> str:
    encoded = base64.urlsafe_b64encode(value or b"").decode("ascii")
    return encoded.rstrip("=")


def _passkey_to_dict(item: Any) -> Dict[str, Any]:
    label = str(getattr(item, "label", "") or "").strip() or "Passkey"
    transports = getattr(item, "transports", None)
    if not isinstance(transports, list):
        transports = []
    return {
        "id": int(getattr(item, "id", 0) or 0),
        "label": label,
        "created_at": int(getattr(item, "created_at", 0) or 0),
        "last_used_at": int(getattr(item, "last_used_at", 0) or 0),
        "transports": [str(value).strip() for value in transports if str(value).strip()],
    }


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

    def _create_bootstrap_pending(
        self,
        *,
        flow: str,
        username: str,
        role: str,
        password_sha512: str,
        display_name: str = "",
    ) -> Dict[str, Any]:
        secret = _generate_totp_secret()
        pending_token = uuid.uuid4().hex
        pending = {
            "flow": flow,
            "username": username,
            "display_name": display_name or username,
            "role": role,
            "password_sha512": password_sha512,
            "secret": secret,
            "token": pending_token,
            "expires": _now_ts() + 300,
        }
        session["bootstrap_admin_pending"] = pending
        session.pop("mfa_pending", None)
        session.pop("passkey_pending", None)
        session.modified = True
        return {
            "status": "mfa_required",
            "pending_token": pending_token,
            "stage": "setup",
            "username": username,
            "role": role,
            "preferred_method": "totp",
            "available_methods": ["totp"],
            "secret": secret,
            "otpauth_url": _totp_provisioning_uri(secret, username),
            "qr_image": _totp_qr_data_uri(_totp_provisioning_uri(secret, username) or ""),
        }

    def _clear_operator_session(self) -> None:
        session.pop("username", None)
        session.pop("role", None)
        session.pop("mfa_pending", None)
        session.pop("passkey_pending", None)

    def _clear_all_auth_sessions(self):
        session.clear()
        response = jsonify({"status": "ok"})
        response.set_cookie("borealis_auth", "", expires=0, path="/")
        return response

    def _passkeys_available(self) -> bool:
        return all(
            (
                base64url_to_bytes,
                generate_authentication_options,
                generate_registration_options,
                options_to_json,
                verify_authentication_response,
                verify_registration_response,
                AuthenticatorSelectionCriteria,
                PublicKeyCredentialDescriptor,
                ResidentKeyRequirement,
                UserVerificationRequirement,
            )
        )

    def _passkey_rp_name(self) -> str:
        return str(os.environ.get("BOREALIS_PASSKEY_RP_NAME", "Borealis") or "Borealis").strip() or "Borealis"

    def _passkey_origin(self) -> str:
        base = public_base_url(self.context, req=request) or str(self.app.config.get("PUBLIC_BASE_URL") or "").strip()
        if base:
            parsed = urlsplit(base if "://" in base else f"https://{base}")
            scheme = parsed.scheme or "https"
            hostname = parsed.hostname or (request.host.split(":", 1)[0] if request.host else "")
            if parsed.port and parsed.port not in (80, 443):
                netloc = f"{hostname}:{parsed.port}"
            else:
                netloc = hostname
            if netloc:
                return f"{scheme}://{netloc}".rstrip("/")
        scheme = "https" if getattr(request, "is_secure", False) else "https"
        host = str(request.host or "").strip()
        return f"{scheme}://{host}".rstrip("/")

    def _passkey_rp_id(self) -> str:
        origin = self._passkey_origin()
        parsed = urlsplit(origin)
        return parsed.hostname or (str(request.host or "").split(":", 1)[0].strip())

    def _passkey_count(self, username: str) -> int:
        if not username:
            return 0
        try:
            conn = self._db_conn()
            try:
                return count_user_passkeys(conn, username)
            finally:
                conn.close()
        except Exception:
            self.logger.debug("Failed to count passkeys for %s", username, exc_info=True)
            return 0

    def _load_pending_mfa(self, token: str):
        pending = session.get("mfa_pending") or {}
        if not pending or not isinstance(pending, dict):
            return None, ({"error": "mfa_pending"}, 401)
        if not token or token != pending.get("token"):
            return None, ({"error": "invalid_session"}, 401)
        if pending.get("expires", 0) < _now_ts():
            session.pop("mfa_pending", None)
            session.pop("passkey_pending", None)
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
        session.pop("passkey_pending", None)
        session.pop("bootstrap_admin_pending", None)
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
        session.pop("passkey_pending", None)

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

    def _directory_login(self, username: str, password: str):
        try:
            result = DirectoryAuthenticationManager(
                db_conn_factory=self.db_conn_factory,
                aegis_cipher_service=self.aegis_cipher_service,
                logger=self.logger,
                service_log=self.service_log,
            ).authenticate_login(username, password)
        except DirectoryAuthError as exc:
            return jsonify({"error": exc.code, "message": str(exc)}), exc.status_code
        except Exception as exc:
            self.logger.debug("Directory authentication failed for %s.", username, exc_info=True)
            return jsonify({"error": "directory_auth_failed", "message": str(exc)}), 502

        row = self._load_login_row(result.username)
        if not row:
            return jsonify({"error": "directory_cache_failed"}), 500
        try:
            existing_secret = self._decrypt_auth_secret(row[9]).strip()
        except Exception:
            existing_secret = ""
        return self._begin_mfa_or_finalize(
            username=row[1],
            role=row[4] or result.role or "User",
            existing_secret=existing_secret,
            mfa_disabled=False,
        )

    def bootstrap_state(self):
        return jsonify(self._public_bootstrap_state())

    def bootstrap_admin_setup(self):
        state = self._bootstrap_state()
        if state["phase"] != BOOTSTRAP_PHASE_ADMIN_SETUP_REQUIRED:
            return jsonify({"error": "invalid_phase", **self._public_bootstrap_state()}), 409

        payload = request.get_json(silent=True) or {}
        username = str(payload.get("username") or "").strip()
        display_name = str(payload.get("display_name") or username).strip()
        password_sha512 = str(payload.get("password_sha512") or "").strip().lower()
        if not username or len(password_sha512) != 128:
            return jsonify({"error": "username and password_sha512 are required"}), 400

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM users")
            if int((cur.fetchone() or [0])[0] or 0) > 0:
                return jsonify({"error": "bootstrap_already_initialized", **self._public_bootstrap_state()}), 409
        finally:
            conn.close()

        return jsonify(
            self._create_bootstrap_pending(
                flow="setup",
                username=username,
                display_name=display_name or username,
                role="Admin",
                password_sha512=password_sha512,
            )
        )

    def bootstrap_admin_recover(self):
        state = self._bootstrap_state()
        if state["phase"] != BOOTSTRAP_PHASE_ADMIN_RECOVERY_REQUIRED:
            return jsonify({"error": "invalid_phase", **self._public_bootstrap_state()}), 409

        payload = request.get_json(silent=True) or {}
        username = str(payload.get("username") or "").strip()
        password_sha512 = str(payload.get("password_sha512") or "").strip().lower()
        if not username or len(password_sha512) != 128:
            return jsonify({"error": "username and password_sha512 are required"}), 400

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT COALESCE(display_name, username), COALESCE(auth_reset_required, 0)
                  FROM users
                 WHERE LOWER(username)=LOWER(?)
                   AND LOWER(role)='admin'
                 LIMIT 1
                """,
                (username,),
            )
            row = cur.fetchone()
        finally:
            conn.close()
        if not row:
            return jsonify({"error": "admin_not_found"}), 404
        if not bool(row[1] or 0):
            return jsonify({"error": "admin_recovery_not_required"}), 409

        return jsonify(
            self._create_bootstrap_pending(
                flow="recover",
                username=username,
                display_name=str(row[0] or username),
                role="Admin",
                password_sha512=password_sha512,
            )
        )

    def bootstrap_admin_mfa_verify(self):
        pending = session.get("bootstrap_admin_pending") or {}
        if not pending or not isinstance(pending, dict):
            return jsonify({"error": "bootstrap_pending"}), 401

        payload = request.get_json(silent=True) or {}
        token = str(payload.get("pending_token") or "").strip()
        code = "".join(ch for ch in str(payload.get("code") or "").strip() if ch.isdigit())
        if not token or token != pending.get("token"):
            return jsonify({"error": "invalid_session"}), 401
        if pending.get("expires", 0) < _now_ts():
            session.pop("bootstrap_admin_pending", None)
            return jsonify({"error": "expired"}), 401
        if len(code) < 6:
            return jsonify({"error": "invalid_code"}), 400

        secret = str(pending.get("secret") or "")
        try:
            if not _totp_for_secret(secret).verify(code, valid_window=1):
                return jsonify({"error": "invalid_code"}), 401
        except Exception as exc:
            return jsonify({"error": str(exc) or "mfa_unavailable"}), 500

        username = str(pending.get("username") or "").strip()
        display_name = str(pending.get("display_name") or username).strip() or username
        role = str(pending.get("role") or "Admin").strip() or "Admin"
        password_sha512 = str(pending.get("password_sha512") or "").strip().lower()
        if not username or len(password_sha512) != 128:
            return jsonify({"error": "invalid_session"}), 401

        try:
            encrypted_password = self._encrypt_auth_secret(password_sha512)
            encrypted_mfa_secret = self._encrypt_auth_secret(secret)
        except AegisCipherServiceError:
            return self._bootstrap_error_response()

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            now_ts = _now_ts()
            if str(pending.get("flow") or "") == "setup":
                cur.execute("SELECT COUNT(*) FROM users")
                if int((cur.fetchone() or [0])[0] or 0) > 0:
                    conn.rollback()
                    return jsonify({"error": "bootstrap_already_initialized", **self._public_bootstrap_state()}), 409
                cur.execute(
                    """
                    INSERT INTO users(
                        username,
                        display_name,
                        password_sha512,
                        role,
                        created_at,
                        updated_at,
                        mfa_enabled,
                        mfa_disabled,
                        mfa_secret,
                        auth_reset_required,
                        auth_reset_at
                    ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
                    """,
                    (
                        username,
                        display_name,
                        encrypted_password,
                        role,
                        now_ts,
                        now_ts,
                        1,
                        0,
                        encrypted_mfa_secret,
                        0,
                        None,
                    ),
                )
            else:
                cur.execute(
                    """
                    UPDATE users
                       SET password_sha512=?,
                           mfa_secret=?,
                           mfa_enabled=1,
                           mfa_disabled=0,
                           auth_reset_required=0,
                           auth_reset_at=NULL,
                           updated_at=?
                     WHERE LOWER(username)=LOWER(?)
                       AND LOWER(role)='admin'
                    """,
                    (encrypted_password, encrypted_mfa_secret, now_ts, username),
                )
                if int(cur.rowcount or 0) <= 0:
                    conn.rollback()
                    return jsonify({"error": "admin_not_found"}), 404
                delete_user_passkeys(conn, username)
            conn.commit()
        except sqlite3.IntegrityError:
            conn.rollback()
            return jsonify({"error": "username already exists"}), 409
        finally:
            conn.close()

        session.pop("bootstrap_admin_pending", None)
        session.modified = True
        return self._finalize_login(username, role)

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
            if not password:
                return jsonify({"error": "directory_password_required"}), 400
            return self._directory_login(username, str(password))

        auth_source = str(row[12] or "local").strip().lower() if len(row) > 12 else "local"
        if auth_source == DIRECTORY_AUTH_SOURCE:
            if bool(row[13] or 0):
                return jsonify({"error": "directory_user_disabled"}), 403
            if not password:
                return jsonify({"error": "directory_password_required"}), 400
            return self._directory_login(username, str(password))

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
            session.pop("passkey_pending", None)
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

    def passkey_register_options(self):
        if not self._operator_auth_allowed():
            return self._bootstrap_error_response()
        if not self._passkeys_available():
            return jsonify({"error": "passkeys_unavailable"}), 503

        payload = request.get_json(silent=True) or {}
        label = str(payload.get("label") or "").strip()

        current_user = self._current_user()
        if not current_user:
            return jsonify({"error": "unauthorized"}), 401

        username = str(current_user.get("username") or "").strip()
        role = str(current_user.get("role") or "User").strip() or "User"

        identity = self._load_user_identity(username)
        if not identity:
            return jsonify({"error": "user_not_found"}), 404
        if str(identity.get("auth_source") or "local").lower() == DIRECTORY_AUTH_SOURCE:
            return jsonify({"error": "passkeys_local_users_only"}), 403
        if bool(identity.get("directory_disabled")):
            return jsonify({"error": "user_disabled"}), 403

        conn = self._db_conn()
        try:
            stored_passkeys = list_user_passkeys(conn, identity["username"])
        finally:
            conn.close()

        exclude_credentials = []
        for item in stored_passkeys:
            normalized_credential_id = ""
            if item.secret_encrypted:
                try:
                    bundle = deserialize_passkey_secret_bundle(
                        self._decrypt_auth_secret(item.secret_encrypted)
                    )
                    normalized_credential_id = str(bundle.get("credential_id") or "")
                except Exception:
                    normalized_credential_id = ""
            if not normalized_credential_id:
                normalized_credential_id = normalize_webauthn_storage_value(item.credential_id)
            if not normalized_credential_id:
                continue
            try:
                exclude_credentials.append(
                    PublicKeyCredentialDescriptor(id=base64url_to_bytes(normalized_credential_id))
                )
            except Exception:
                self.logger.debug(
                    "Skipping invalid stored passkey credential %s for %s",
                    getattr(item, "id", 0),
                    identity["username"],
                    exc_info=True,
                )

        try:
            options = generate_registration_options(
                rp_id=self._passkey_rp_id(),
                rp_name=self._passkey_rp_name(),
                user_id=build_webauthn_user_id(identity["id"], identity["username"]),
                user_name=identity["username"],
                user_display_name=identity["display_name"],
                exclude_credentials=exclude_credentials,
                authenticator_selection=AuthenticatorSelectionCriteria(
                    resident_key=ResidentKeyRequirement.REQUIRED,
                    user_verification=UserVerificationRequirement.REQUIRED,
                ),
            )
        except Exception as exc:
            self.logger.debug("Failed to generate passkey registration options for %s", identity["username"], exc_info=True)
            return jsonify({"error": str(exc) or "passkey_setup_unavailable"}), 500

        request_id = uuid.uuid4().hex
        session["passkey_pending"] = {
            "flow": "register",
            "request_id": request_id,
            "username": identity["username"],
            "role": role,
            "challenge": _bytes_to_base64url(options.challenge),
            "label": label,
            "expires": _now_ts() + 300,
        }
        session.modified = True

        return jsonify(
            {
                "status": "ok",
                "request_id": request_id,
                "options": json.loads(options_to_json(options)),
            }
        )

    def passkey_register_verify(self):
        if not self._operator_auth_allowed():
            return self._bootstrap_error_response()
        if not self._passkeys_available():
            return jsonify({"error": "passkeys_unavailable"}), 503

        ceremony = session.get("passkey_pending") or {}
        if not ceremony or not isinstance(ceremony, dict) or ceremony.get("flow") != "register":
            return jsonify({"error": "passkey_pending"}), 401

        payload = request.get_json(silent=True) or {}
        request_id = str(payload.get("request_id") or "").strip()
        credential = payload.get("credential") or {}

        if not request_id or request_id != ceremony.get("request_id"):
            return jsonify({"error": "invalid_session"}), 401
        if ceremony.get("expires", 0) < _now_ts():
            session.pop("passkey_pending", None)
            return jsonify({"error": "expired"}), 401

        username = str(ceremony.get("username") or "").strip()
        role = str(ceremony.get("role") or "User").strip() or "User"
        current_user = self._current_user()
        if not current_user:
            session.pop("passkey_pending", None)
            return jsonify({"error": "unauthorized"}), 401
        if str(current_user.get("username") or "").strip().lower() != username.lower():
            session.pop("passkey_pending", None)
            return jsonify({"error": "invalid_session"}), 401

        identity = self._load_user_identity(username)
        if not identity:
            session.pop("passkey_pending", None)
            return jsonify({"error": "user_not_found"}), 404

        try:
            verification = verify_registration_response(
                credential=credential,
                expected_challenge=base64url_to_bytes(str(ceremony.get("challenge") or "")),
                expected_rp_id=self._passkey_rp_id(),
                expected_origin=self._passkey_origin(),
                require_user_verification=True,
            )
        except Exception as exc:
            return jsonify({"error": str(exc)}), 400

        credential_id = normalize_webauthn_storage_value(
            getattr(verification, "credential_id", None) or credential.get("id") or ""
        )
        public_key = normalize_webauthn_storage_value(getattr(verification, "credential_public_key", None) or "")
        sign_count = int(getattr(verification, "sign_count", 0) or 0)
        aaguid = str(getattr(verification, "aaguid", "") or "").strip()
        transports = []
        response_payload = credential.get("response") or {}
        if isinstance(response_payload, dict):
            transports = response_payload.get("transports") or []

        if not credential_id or not public_key:
            return jsonify({"error": "invalid_passkey"}), 400

        label = str(payload.get("label") or ceremony.get("label") or "").strip()
        now_ts = _now_ts()
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            secret_encrypted = self._encrypt_auth_secret(
                serialize_passkey_secret_bundle(
                    credential_id=credential_id,
                    public_key=public_key,
                    sign_count=sign_count,
                    aaguid=aaguid,
                )
            )
            cur.execute(
                """
                INSERT INTO user_passkeys(
                    user_id,
                    credential_id,
                    public_key,
                    sign_count,
                    label,
                    transports_json,
                    aaguid,
                    created_at,
                    last_used_at,
                    credential_lookup_hmac,
                    secret_encrypted
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    identity["id"],
                    "",
                    "",
                    0,
                    label or "Passkey",
                    serialize_transports(transports),
                    "",
                    now_ts,
                    now_ts,
                    build_passkey_lookup_hmac(require_app_secret(self.app), credential_id),
                    secret_encrypted,
                ),
            )
            total_passkeys = count_user_passkeys(conn, identity["username"])
            conn.commit()
        except sqlite3.IntegrityError:
            conn.rollback()
            return jsonify({"error": "passkey_already_registered"}), 409
        finally:
            conn.close()

        session.pop("passkey_pending", None)
        session.modified = True
        return jsonify(
            {
                "status": "ok",
                "username": identity["username"],
                "passkey_count": total_passkeys,
            }
        )

    def passkey_authenticate_options(self):
        if not self._operator_auth_allowed():
            return self._bootstrap_error_response()
        if not self._passkeys_available():
            return jsonify({"error": "passkeys_unavailable"}), 503

        try:
            options = generate_authentication_options(
                rp_id=self._passkey_rp_id(),
                user_verification=UserVerificationRequirement.REQUIRED,
            )
        except Exception as exc:
            self.logger.debug("Failed to generate passkey authentication options", exc_info=True)
            return jsonify({"error": str(exc) or "passkey_auth_unavailable"}), 500

        request_id = uuid.uuid4().hex
        session["passkey_pending"] = {
            "flow": "authenticate_primary",
            "request_id": request_id,
            "challenge": _bytes_to_base64url(options.challenge),
            "expires": _now_ts() + 300,
        }
        session.modified = True

        return jsonify(
            {
                "status": "ok",
                "request_id": request_id,
                "options": json.loads(options_to_json(options)),
            }
        )

    def passkey_authenticate_verify(self):
        if not self._operator_auth_allowed():
            return self._bootstrap_error_response()
        if not self._passkeys_available():
            return jsonify({"error": "passkeys_unavailable"}), 503

        ceremony = session.get("passkey_pending") or {}
        if not ceremony or not isinstance(ceremony, dict) or ceremony.get("flow") != "authenticate_primary":
            return jsonify({"error": "passkey_pending"}), 401

        payload = request.get_json(silent=True) or {}
        request_id = str(payload.get("request_id") or "").strip()
        credential = payload.get("credential") or {}
        credential_id = normalize_webauthn_storage_value(credential.get("id") or "")

        if not request_id or request_id != ceremony.get("request_id"):
            return jsonify({"error": "invalid_session"}), 401
        if ceremony.get("expires", 0) < _now_ts():
            session.pop("passkey_pending", None)
            return jsonify({"error": "expired"}), 401
        if not credential_id:
            return jsonify({"error": "invalid_passkey"}), 400

        now_ts = _now_ts()

        conn = self._db_conn()
        try:
            stored_passkey = None
            for candidate in credential_lookup_candidates(credential_id):
                lookup_hmac = build_passkey_lookup_hmac(require_app_secret(self.app), candidate)
                stored_passkey = get_passkey_by_lookup_hmac(conn, lookup_hmac)
                if not stored_passkey:
                    stored_passkey = get_passkey_by_credential_id(conn, candidate)
                if stored_passkey:
                    break
            if not stored_passkey:
                return jsonify({"error": "passkey_not_configured"}), 404

            identity = self._load_user_identity_by_id(stored_passkey.user_id)
            if not identity:
                return jsonify({"error": "user_not_found"}), 404
            if str(identity.get("auth_source") or "local").lower() == DIRECTORY_AUTH_SOURCE:
                return jsonify({"error": "passkeys_local_users_only"}), 403
            if bool(identity.get("directory_disabled")):
                return jsonify({"error": "user_disabled"}), 403

            bundle = deserialize_passkey_secret_bundle(
                self._decrypt_auth_secret(stored_passkey.secret_encrypted)
                if stored_passkey.secret_encrypted
                else serialize_passkey_secret_bundle(
                    credential_id=stored_passkey.credential_id,
                    public_key=stored_passkey.public_key,
                    sign_count=stored_passkey.sign_count,
                    aaguid=stored_passkey.aaguid,
                )
            )
            normalized_stored_credential_id = normalize_webauthn_storage_value(bundle.get("credential_id"))
            normalized_public_key = normalize_webauthn_storage_value(bundle.get("public_key"))
            if not normalized_stored_credential_id or not normalized_public_key:
                return jsonify({"error": "invalid_passkey"}), 400

            verification = verify_authentication_response(
                credential=credential,
                expected_challenge=base64url_to_bytes(str(ceremony.get("challenge") or "")),
                expected_rp_id=self._passkey_rp_id(),
                expected_origin=self._passkey_origin(),
                credential_public_key=base64url_to_bytes(normalized_public_key),
                credential_current_sign_count=int(bundle.get("sign_count") or 0),
                require_user_verification=True,
            )

            cur = conn.cursor()
            cur.execute(
                """
                UPDATE user_passkeys
                   SET credential_id='',
                       public_key='',
                       sign_count=0,
                       aaguid='',
                       credential_lookup_hmac=?,
                       secret_encrypted=?,
                       last_used_at=?
                 WHERE id=?
                """,
                (
                    build_passkey_lookup_hmac(require_app_secret(self.app), normalized_stored_credential_id),
                    self._encrypt_auth_secret(
                        serialize_passkey_secret_bundle(
                            credential_id=normalized_stored_credential_id,
                            public_key=normalized_public_key,
                            sign_count=int(getattr(verification, "new_sign_count", bundle.get("sign_count", 0)) or 0),
                            aaguid=bundle.get("aaguid") or "",
                        )
                    ),
                    now_ts,
                    stored_passkey.id,
                ),
            )
            conn.commit()
        except Exception as exc:
            conn.rollback()
            return jsonify({"error": str(exc)}), 400
        finally:
            conn.close()

        return self._finalize_login(identity["username"], identity["role"])

def register_auth(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register authentication endpoints for the Engine."""

    service = _AuthService(app, adapters)
    blueprint = Blueprint("auth", __name__)

    def _require_internal() -> bool:
        try:
            secret = require_app_secret(app)
        except Exception:
            return False
        return validate_internal_token(secret, request.headers.get(INTERNAL_TOKEN_HEADER))

    @blueprint.route("/api/internal/bootstrap/state", methods=["GET"])
    def _internal_bootstrap_state():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        return service.bootstrap_state()

    @blueprint.route("/api/bootstrap/admin/setup", methods=["POST"])
    def _bootstrap_admin_setup():
        return service.bootstrap_admin_setup()

    @blueprint.route("/api/bootstrap/admin/recover", methods=["POST"])
    def _bootstrap_admin_recover():
        return service.bootstrap_admin_recover()

    @blueprint.route("/api/bootstrap/admin/mfa/verify", methods=["POST"])
    def _bootstrap_admin_mfa_verify():
        return service.bootstrap_admin_mfa_verify()

    @blueprint.route("/api/auth/login", methods=["POST"])
    def _login():
        return service.login()

    @blueprint.route("/api/auth/mfa/verify", methods=["POST"])
    def _mfa_verify():
        return service.mfa_verify()

    @blueprint.route("/api/auth/passkeys/register/options", methods=["POST"])
    def _passkey_register_options():
        return service.passkey_register_options()

    @blueprint.route("/api/auth/passkeys/register/verify", methods=["POST"])
    def _passkey_register_verify():
        return service.passkey_register_verify()

    @blueprint.route("/api/auth/passkeys/authenticate/options", methods=["POST"])
    def _passkey_authenticate_options():
        return service.passkey_authenticate_options()

    @blueprint.route("/api/auth/passkeys/authenticate/verify", methods=["POST"])
    def _passkey_authenticate_verify():
        return service.passkey_authenticate_verify()

    app.register_blueprint(blueprint)
    register_aegis_cipher_management(app, adapters)
    register_github_token_management(app, adapters)
    register_credential_management(app, adapters)
    register_directory_services(app, adapters)
