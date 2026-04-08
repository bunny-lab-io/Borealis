# ======================================================
# Data\Engine\services\API\access_management\login.py
# Description: Primary authentication blueprint used by the Engine auth group for sessions, MFA, and logout.
#
# API Endpoints (if applicable):
# - POST /api/auth/login (No Authentication) - Authenticates operator credentials and starts a session token or MFA setup/verification challenge.
# - POST /api/auth/logout (Token Authenticated) - Clears the active operator session and authentication cookie.
# - POST /api/auth/mfa/verify (Token Authenticated (MFA pending)) - Verifies TOTP codes during multifactor setup or login.
# - POST /api/auth/passkeys/register/options (Token Authenticated) - Starts a passkey registration ceremony for the current operator.
# - POST /api/auth/passkeys/register/verify (Token Authenticated) - Verifies a passkey registration response and stores the credential.
# - POST /api/auth/passkeys/authenticate/options (No Authentication) - Starts a passkey sign-in ceremony for passwordless operator login.
# - POST /api/auth/passkeys/authenticate/verify (No Authentication) - Verifies a passkey sign-in response and completes operator login.
# - GET /api/auth/passkeys (Token Authenticated) - Lists the current operator's enrolled passkeys.
# - PATCH /api/auth/passkeys/<passkey_id> (Token Authenticated) - Updates the display label for one of the current operator's passkeys.
# - DELETE /api/auth/passkeys/<passkey_id> (Token Authenticated) - Removes one of the current operator's passkeys.
# - GET /api/auth/me (Token Authenticated) - Returns the currently authenticated operator profile, including MFA state.
# ======================================================

"""Authentication endpoints for the Borealis Engine API."""
from __future__ import annotations

import ast
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
from ...auth.secrets import require_app_secret
from .aegis import register_aegis_cipher_management
from .credentials import register_credential_management
from .github import register_github_token_management
from .multi_factor_authentication import register_mfa_management
from .passkeys import (
    build_webauthn_user_id,
    count_user_passkeys,
    delete_user_passkey,
    get_passkey_by_credential_id,
    get_user_passkey_by_id,
    list_user_passkeys,
    serialize_transports,
    update_user_passkey_label,
)
from .site_assignments import register_user_site_assignment_management
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


def _bytes_to_base64url(value: bytes) -> str:
    encoded = base64.urlsafe_b64encode(value or b"").decode("ascii")
    return encoded.rstrip("=")


def _bytes_literal_to_bytes(value: Any) -> Optional[bytes]:
    if value is None:
        return None
    text = str(value or "").strip()
    if not text or not text.startswith("b"):
        return None
    try:
        parsed = ast.literal_eval(text)
    except Exception:
        return None
    if isinstance(parsed, bytearray):
        return bytes(parsed)
    if isinstance(parsed, bytes):
        return parsed
    return None


def _normalize_webauthn_storage_value(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, memoryview):
        value = value.tobytes()
    if isinstance(value, bytearray):
        value = bytes(value)
    if isinstance(value, bytes):
        return _bytes_to_base64url(value)

    text = str(value or "").strip()
    if not text:
        return ""

    literal_bytes = _bytes_literal_to_bytes(text)
    if literal_bytes is not None:
        return _bytes_to_base64url(literal_bytes)
    return text


def _credential_lookup_candidates(credential_id: Any) -> list[str]:
    normalized = _normalize_webauthn_storage_value(credential_id)
    candidates: list[str] = []

    def _add(value: str) -> None:
        if value and value not in candidates:
            candidates.append(value)

    _add(normalized)
    if normalized and base64url_to_bytes:
        try:
            _add(str(base64url_to_bytes(normalized)))
        except Exception:
            pass
    return candidates


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
                    COALESCE(role, 'User')
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
                    COALESCE(role, 'User')
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
        }

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
        session.pop("passkey_pending", None)
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
                    COALESCE(mfa_secret, '') AS mfa_secret,
                    COALESCE(mfa_disabled, 0) AS mfa_disabled
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
        existing_secret = (row[9] or "").strip()
        mfa_disabled = bool(row[10] or 0)
        available_methods = ["totp"] if existing_secret else []
        setup_methods = ["totp"]

        session.pop("username", None)
        session.pop("role", None)
        session.pop("passkey_pending", None)

        if mfa_disabled:
            session.pop("mfa_pending", None)
            return self._finalize_login(row[1], role)

        stage = "verify" if available_methods else "setup"
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

    def passkey_register_options(self):
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

        conn = self._db_conn()
        try:
            stored_passkeys = list_user_passkeys(conn, identity["username"])
        finally:
            conn.close()

        exclude_credentials = []
        for item in stored_passkeys:
            normalized_credential_id = _normalize_webauthn_storage_value(item.credential_id)
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

        credential_id = _normalize_webauthn_storage_value(
            getattr(verification, "credential_id", None) or credential.get("id") or ""
        )
        public_key = _normalize_webauthn_storage_value(getattr(verification, "credential_public_key", None) or "")
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
                    last_used_at
                ) VALUES (?,?,?,?,?,?,?,?,?)
                """,
                (
                    identity["id"],
                    credential_id,
                    public_key,
                    sign_count,
                    label or "Passkey",
                    serialize_transports(transports),
                    aaguid,
                    now_ts,
                    now_ts,
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
        if not self._passkeys_available():
            return jsonify({"error": "passkeys_unavailable"}), 503

        ceremony = session.get("passkey_pending") or {}
        if not ceremony or not isinstance(ceremony, dict) or ceremony.get("flow") != "authenticate_primary":
            return jsonify({"error": "passkey_pending"}), 401

        payload = request.get_json(silent=True) or {}
        request_id = str(payload.get("request_id") or "").strip()
        credential = payload.get("credential") or {}
        credential_id = _normalize_webauthn_storage_value(credential.get("id") or "")

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
            for candidate in _credential_lookup_candidates(credential_id):
                stored_passkey = get_passkey_by_credential_id(conn, candidate)
                if stored_passkey:
                    break
            if not stored_passkey:
                return jsonify({"error": "passkey_not_configured"}), 404

            identity = self._load_user_identity_by_id(stored_passkey.user_id)
            if not identity:
                return jsonify({"error": "user_not_found"}), 404

            normalized_stored_credential_id = _normalize_webauthn_storage_value(stored_passkey.credential_id)
            normalized_public_key = _normalize_webauthn_storage_value(stored_passkey.public_key)
            if not normalized_stored_credential_id or not normalized_public_key:
                return jsonify({"error": "invalid_passkey"}), 400

            verification = verify_authentication_response(
                credential=credential,
                expected_challenge=base64url_to_bytes(str(ceremony.get("challenge") or "")),
                expected_rp_id=self._passkey_rp_id(),
                expected_origin=self._passkey_origin(),
                credential_public_key=base64url_to_bytes(normalized_public_key),
                credential_current_sign_count=int(stored_passkey.sign_count or 0),
                require_user_verification=True,
            )

            cur = conn.cursor()
            cur.execute(
                "UPDATE user_passkeys SET credential_id=?, public_key=?, sign_count=?, last_used_at=? WHERE id=?",
                (
                    normalized_stored_credential_id,
                    normalized_public_key,
                    int(getattr(verification, "new_sign_count", stored_passkey.sign_count) or 0),
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

    def list_passkeys(self):
        user = self._current_user()
        if not user:
            return jsonify({"error": "unauthorized"}), 401

        username = str(user.get("username") or "").strip()
        if not username:
            return jsonify({"error": "unauthorized"}), 401

        conn = self._db_conn()
        try:
            stored_passkeys = list_user_passkeys(conn, username)
        finally:
            conn.close()

        return jsonify(
            {
                "status": "ok",
                "passkeys": [_passkey_to_dict(item) for item in stored_passkeys],
                "passkey_count": len(stored_passkeys),
            }
        )

    def update_passkey(self, passkey_id: int):
        user = self._current_user()
        if not user:
            return jsonify({"error": "unauthorized"}), 401

        username = str(user.get("username") or "").strip()
        if not username:
            return jsonify({"error": "unauthorized"}), 401

        payload = request.get_json(silent=True) or {}
        label = str(payload.get("label") or "").strip()
        if len(label) > 80:
            return jsonify({"error": "invalid_label"}), 400

        conn = self._db_conn()
        try:
            updated = update_user_passkey_label(conn, username, int(passkey_id or 0), label)
            if not updated:
                conn.rollback()
                return jsonify({"error": "passkey_not_found"}), 404
            count = count_user_passkeys(conn, username)
            conn.commit()
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to update passkey label for %s", username, exc_info=True)
            return jsonify({"error": str(exc) or "passkey_update_failed"}), 500
        finally:
            conn.close()

        return jsonify(
            {
                "status": "ok",
                "passkey": _passkey_to_dict(updated),
                "passkey_count": count,
            }
        )

    def delete_passkey(self, passkey_id: int):
        user = self._current_user()
        if not user:
            return jsonify({"error": "unauthorized"}), 401

        username = str(user.get("username") or "").strip()
        if not username:
            return jsonify({"error": "unauthorized"}), 401

        conn = self._db_conn()
        try:
            existing = get_user_passkey_by_id(conn, username, int(passkey_id or 0))
            if not existing:
                conn.rollback()
                return jsonify({"error": "passkey_not_found"}), 404
            removed = delete_user_passkey(conn, username, int(passkey_id or 0))
            count = count_user_passkeys(conn, username)
            conn.commit()
        except Exception as exc:
            conn.rollback()
            self.logger.debug("Failed to delete passkey for %s", username, exc_info=True)
            return jsonify({"error": str(exc) or "passkey_delete_failed"}), 500
        finally:
            conn.close()

        return jsonify(
            {
                "status": "ok",
                "removed": bool(removed),
                "passkey_count": count,
            }
        )

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
                    """
                    SELECT
                        id,
                        username,
                        display_name,
                        role,
                        last_login,
                        created_at,
                        updated_at,
                        CASE WHEN COALESCE(mfa_disabled, 0) = 1 THEN 0 ELSE 1 END AS mfa_enabled
                    FROM users
                    WHERE LOWER(username)=LOWER(?)
                    """,
                    (username,),
                )
                row = cur.fetchone()
                passkey_count = count_user_passkeys(conn, username)
            finally:
                conn.close()
            if row:
                info = _user_row_to_dict(row)
                return jsonify(
                    {
                        "username": info["username"],
                        "display_name": info["display_name"],
                        "role": info["role"],
                        "mfa_enabled": bool(info["mfa_enabled"]),
                        "passkey_count": passkey_count,
                    }
                )
        except Exception:
            self.logger.debug("Failed to fetch user record for %s", username, exc_info=True)

        return jsonify(
            {
                "username": username,
                "display_name": username,
                "role": user.get("role") or "User",
                "mfa_enabled": False,
                "passkey_count": self._passkey_count(username),
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

    @blueprint.route("/api/auth/passkeys", methods=["GET"])
    def _list_passkeys():
        return service.list_passkeys()

    @blueprint.route("/api/auth/passkeys/<int:passkey_id>", methods=["PATCH"])
    def _update_passkey(passkey_id: int):
        return service.update_passkey(passkey_id)

    @blueprint.route("/api/auth/passkeys/<int:passkey_id>", methods=["DELETE"])
    def _delete_passkey(passkey_id: int):
        return service.delete_passkey(passkey_id)

    @blueprint.route("/api/auth/me", methods=["GET"])
    def _me():
        return service.me()

    app.register_blueprint(blueprint)
    register_user_management(app, adapters)
    register_user_site_assignment_management(app, adapters)
    register_mfa_management(app, adapters)
    register_aegis_cipher_management(app, adapters)
    register_github_token_management(app, adapters)
    register_credential_management(app, adapters)
