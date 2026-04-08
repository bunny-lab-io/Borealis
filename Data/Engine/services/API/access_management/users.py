# ======================================================
# Data\Engine\services\API\access_management\users.py
# Description: Operator user CRUD endpoints for the Engine auth group, mirroring the legacy server behaviour.
#
# API Endpoints (if applicable):
# - GET /api/users (Token Authenticated (Admin)) - Lists operator accounts.
# - POST /api/users (Token Authenticated (Admin)) - Creates a new operator account.
# - DELETE /api/users/<username> (Token Authenticated (Admin)) - Deletes an operator account.
# - POST /api/users/<username>/reset_password (Token Authenticated (Admin)) - Resets an operator password hash.
# - POST /api/auth/password/reset (Token Authenticated) - Verifies the current operator password and replaces it with a new password hash.
# - POST /api/users/<username>/role (Token Authenticated (Admin)) - Updates an operator role.
# ======================================================

"""Operator user management endpoints for the Borealis Engine."""
from __future__ import annotations

import hashlib
import os
from Data.Engine.db import dbapi as sqlite3
import time
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional, Sequence, Tuple

from flask import Blueprint, Flask, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters

from ...aegis_cipher import AegisDataCorruptionError
from ...auth.bootstrap_state import operator_auth_allowed
from ...auth.secrets import require_app_secret
from .passkeys import delete_user_passkeys

def _now_ts() -> int:
    return int(time.time())


def _sha512_hex(value: str) -> str:
    return hashlib.sha512((value or "").encode("utf-8")).hexdigest()


def _row_to_user(row: Sequence[Any]) -> Mapping[str, Any]:
    """Convert a database row into a user payload."""
    return {
        "id": row[0],
        "username": row[1],
        "display_name": row[2] or row[1],
        "role": row[3] or "User",
        "last_login": row[4] or 0,
        "created_at": row[5] or 0,
        "updated_at": row[6] or 0,
        "mfa_enabled": 1 if (row[7] or 0) else 0,
        "auth_reset_required": 1 if (row[8] or 0) else 0,
        "auth_reset_at": row[9] or 0,
    }


class UserManagementService:
    """Utility wrapper that performs admin-authenticated user CRUD operations."""

    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.logger = adapters.context.logger
        self.aegis_cipher_service = adapters.aegis_cipher_service

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, Any]]:
        if not operator_auth_allowed(
            db_conn_factory=self.db_conn_factory,
            aegis_cipher_service=self.aegis_cipher_service,
        ):
            return None

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

    def _extract_password_hash(self, data: Mapping[str, Any], plain_key: str, hash_key: str) -> str:
        password_sha512 = (data.get(hash_key) or "").strip().lower()
        if password_sha512:
            return password_sha512

        raw_password = data.get(plain_key)
        if raw_password is None:
            return ""
        raw_password_text = str(raw_password)
        if not raw_password_text:
            return ""
        return _sha512_hex(raw_password_text)

    def _encrypt_password_hash(self, password_sha512: str) -> str:
        return self.aegis_cipher_service.encrypt_secret_for_text(password_sha512) or ""

    def _decrypt_password_hash(self, stored_value: Any) -> str:
        text = str(stored_value or "")
        if not text:
            return ""
        return self.aegis_cipher_service.decrypt_secret_text(text)

    # ------------------------------------------------------------------ #
    # Endpoint implementations
    # ------------------------------------------------------------------ #

    def list_users(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
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
                    CASE WHEN COALESCE(mfa_disabled, 0) = 1 THEN 0 ELSE 1 END AS mfa_enabled,
                    COALESCE(auth_reset_required, 0) AS auth_reset_required,
                    COALESCE(auth_reset_at, 0) AS auth_reset_at
                FROM users
                ORDER BY LOWER(username) ASC
                """
            )
            rows = cur.fetchall()
            users: List[Mapping[str, Any]] = [_row_to_user(row) for row in rows]
            return jsonify({"users": users})
        except Exception as exc:
            self.logger.debug("Failed to list users", exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()

    def create_user(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        data = request.get_json(silent=True) or {}
        username = (data.get("username") or "").strip()
        display_name = (data.get("display_name") or username).strip()
        role = (data.get("role") or "User").strip().title()
        password_sha512 = (data.get("password_sha512") or "").strip().lower()

        if not username or not password_sha512:
            return jsonify({"error": "username and password_sha512 are required"}), 400
        if role not in ("User", "Admin"):
            return jsonify({"error": "invalid role"}), 400

        now_ts = _now_ts()
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            encrypted_password = self._encrypt_password_hash(password_sha512)
            cur.execute(
                "INSERT INTO users(username, display_name, password_sha512, role, created_at, updated_at, mfa_enabled, mfa_disabled, auth_reset_required, auth_reset_at) "
                "VALUES(?,?,?,?,?,?,?,?,?,?)",
                (username, display_name or username, encrypted_password, role, now_ts, now_ts, 0, 0, 0, None),
            )
            conn.commit()
            return jsonify({"status": "ok"})
        except sqlite3.IntegrityError:
            return jsonify({"error": "username already exists"}), 409
        except Exception as exc:
            self.logger.debug("Failed to create user %s", username, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()

    def delete_user(self, username: str):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        username_norm = (username or "").strip()
        if not username_norm:
            return jsonify({"error": "invalid username"}), 400

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()

            me = self._current_user()
            if me and (me.get("username", "").lower() == username_norm.lower()):
                return (
                    jsonify({"error": "You cannot delete the user you are currently logged in as."}),
                    400,
                )

            cur.execute("SELECT COUNT(*) FROM users")
            total_users = cur.fetchone()[0] or 0
            if total_users <= 1:
                return (
                    jsonify(
                        {
                            "error": "There is only one user currently configured, you cannot delete this user until you have created another."
                        }
                    ),
                    400,
                )

            cur.execute("SELECT id FROM users WHERE LOWER(username)=LOWER(?)", (username_norm,))
            row = cur.fetchone()
            user_id = row[0] if row else None
            if user_id is not None:
                cur.execute("DELETE FROM user_site_assignments WHERE user_id = ?", (user_id,))
            cur.execute("DELETE FROM users WHERE LOWER(username)=LOWER(?)", (username_norm,))
            deleted = cur.rowcount or 0
            conn.commit()
            if deleted == 0:
                return jsonify({"error": "user not found"}), 404
            return jsonify({"status": "ok"})
        except Exception as exc:
            self.logger.debug("Failed to delete user %s", username_norm, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()

    def reset_password(self, username: str):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        data = request.get_json(silent=True) or {}
        password_sha512 = (data.get("password_sha512") or "").strip().lower()
        if not password_sha512 or len(password_sha512) != 128:
            return jsonify({"error": "invalid password hash"}), 400

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            encrypted_password = self._encrypt_password_hash(password_sha512)
            now_ts = _now_ts()
            cur.execute(
                """
                UPDATE users
                   SET password_sha512=?,
                       mfa_secret=NULL,
                       mfa_enabled=0,
                       mfa_disabled=0,
                       auth_reset_required=0,
                       auth_reset_at=NULL,
                       updated_at=?
                 WHERE LOWER(username)=LOWER(?)
                """,
                (encrypted_password, now_ts, username),
            )
            if cur.rowcount == 0:
                return jsonify({"error": "user not found"}), 404
            delete_user_passkeys(conn, username)
            conn.commit()
            return jsonify({"status": "ok"})
        except Exception as exc:
            self.logger.debug("Failed to reset password for %s", username, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()

    def reset_own_password(self):
        user = self._current_user()
        if not user:
            return jsonify({"error": "unauthorized"}), 401

        username = (user.get("username") or "").strip()
        if not username:
            return jsonify({"error": "unauthorized"}), 401

        data = request.get_json(silent=True) or {}
        current_password_sha512 = self._extract_password_hash(
            data,
            plain_key="current_password",
            hash_key="current_password_sha512",
        )
        new_password_sha512 = self._extract_password_hash(
            data,
            plain_key="new_password",
            hash_key="new_password_sha512",
        )

        if len(current_password_sha512) != 128:
            return jsonify({"error": "invalid current password hash"}), 400
        if len(new_password_sha512) != 128:
            return jsonify({"error": "invalid new password hash"}), 400

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            cur.execute(
                "SELECT COALESCE(password_sha512, ''), COALESCE(auth_reset_required, 0) FROM users WHERE LOWER(username)=LOWER(?)",
                (username,),
            )
            row = cur.fetchone()
            if not row:
                return jsonify({"error": "user not found"}), 404

            if bool(row[1] or 0):
                return jsonify({"error": "auth_reset_required"}), 423

            try:
                stored_hash = self._decrypt_password_hash(row[0]).strip().lower()
            except AegisDataCorruptionError:
                self.aegis_cipher_service.migrate_operator_auth_if_needed()
                cur.execute(
                    "SELECT COALESCE(password_sha512, ''), COALESCE(auth_reset_required, 0) FROM users WHERE LOWER(username)=LOWER(?)",
                    (username,),
                )
                row = cur.fetchone()
                if not row:
                    return jsonify({"error": "user not found"}), 404
                stored_hash = self._decrypt_password_hash(row[0]).strip().lower()
            if stored_hash != current_password_sha512:
                return jsonify({"error": "invalid current password"}), 401
            if stored_hash == new_password_sha512:
                return jsonify({"error": "new password must differ from the current password"}), 400

            now_ts = _now_ts()
            cur.execute(
                "UPDATE users SET password_sha512=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
                (self._encrypt_password_hash(new_password_sha512), now_ts, username),
            )
            conn.commit()
            return jsonify({"status": "ok"})
        except Exception as exc:
            self.logger.debug("Failed to reset own password for %s", username, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()

    def change_role(self, username: str):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        data = request.get_json(silent=True) or {}
        role = (data.get("role") or "").strip().title()
        if role not in ("User", "Admin"):
            return jsonify({"error": "invalid role"}), 400

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()

            if role == "User":
                cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
                admin_count = cur.fetchone()[0] or 0
                cur.execute(
                    "SELECT LOWER(role) FROM users WHERE LOWER(username)=LOWER(?)",
                    (username,),
                )
                row = cur.fetchone()
                current_role = (row[0] or "").lower() if row else ""
                if current_role == "admin" and admin_count <= 1:
                    return jsonify({"error": "cannot demote the last admin"}), 400

            now_ts = _now_ts()
            cur.execute(
                "UPDATE users SET role=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
                (role, now_ts, username),
            )
            if cur.rowcount == 0:
                return jsonify({"error": "user not found"}), 404
            conn.commit()

            me = self._current_user()
            if me and me.get("username", "").lower() == (username or "").lower():
                session["role"] = role

            return jsonify({"status": "ok"})
        except Exception as exc:
            self.logger.debug("Failed to update role for %s", username, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn:
                conn.close()


def register_user_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register user management endpoints."""

    service = UserManagementService(app, adapters)
    blueprint = Blueprint("access_mgmt_users", __name__)

    @blueprint.route("/api/users", methods=["GET"])
    def _list_users():
        return service.list_users()

    @blueprint.route("/api/users", methods=["POST"])
    def _create_user():
        return service.create_user()

    @blueprint.route("/api/users/<username>", methods=["DELETE"])
    def _delete_user(username: str):
        return service.delete_user(username)

    @blueprint.route("/api/users/<username>/reset_password", methods=["POST"])
    def _reset_password(username: str):
        return service.reset_password(username)

    @blueprint.route("/api/auth/password/reset", methods=["POST"])
    def _reset_own_password():
        return service.reset_own_password()

    @blueprint.route("/api/users/<username>/role", methods=["POST"])
    def _change_role(username: str):
        return service.change_role(username)

    app.register_blueprint(blueprint)
