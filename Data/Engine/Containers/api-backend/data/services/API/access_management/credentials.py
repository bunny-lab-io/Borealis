# ======================================================
# Data\Engine\services\API\access_management\credentials.py
# Description: Credential-management endpoints for reusable SSH, Windows, and WinRM authentication records.
#
# API Endpoints (if applicable):
# - POST /api/credentials (Token Authenticated (Admin)) - Creates a stored credential record.
# - PUT /api/credentials/<int:credential_id> (Token Authenticated (Admin)) - Updates a stored credential record.
# - DELETE /api/credentials/<int:credential_id> (Token Authenticated (Admin)) - Deletes a stored credential record.
# ======================================================

"""Credential-management endpoints for the Borealis Engine."""
from __future__ import annotations

import json
import os
import time
from typing import TYPE_CHECKING, Any, Dict, Mapping, Optional, Sequence, Tuple

from Data.Engine.db import dbapi as sqlite3
from flask import Blueprint, Flask, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters

from ...auth.bootstrap_state import operator_auth_allowed
from ...auth.secrets import require_app_secret
from ...aegis_cipher import (
    AegisCipherServiceError,
    AegisDataCorruptionError,
    AegisLockedError,
    AegisNotConfiguredError,
    apply_credential_secret_reset_metadata,
    credential_secret_reset_details,
)

_ALLOWED_CREDENTIAL_TYPES = {"machine", "domain", "token"}
_ALLOWED_CONNECTION_TYPES = {"ssh", "windows", "winrm"}
_AFFECTED_SECRET_FIELDS = ("password", "private_key", "private_key_passphrase", "become_password")
_AEGIS_RESET_METADATA_KEYS = (
    "aegis_secret_state",
    "aegis_lost_secret_fields",
    "aegis_reset_at",
)


def _now_ts() -> int:
    return int(time.time())


def _normalize_secret_blob(value: Any) -> Optional[bytes]:
    if value is None:
        return None
    text = str(value)
    if text == "":
        return None
    return text.encode("utf-8")


def _normalize_private_key_text(value: Any) -> str:
    text = "" if value is None else str(value)
    if not text:
        return ""
    normalized = text.lstrip("\ufeff").replace("\r\n", "\n").replace("\r", "\n")
    if normalized and not normalized.endswith("\n"):
        normalized += "\n"
    return normalized


def _normalize_private_key_blob(value: Any) -> Optional[bytes]:
    normalized = _normalize_private_key_text(value)
    if not normalized:
        return None
    return normalized.encode("utf-8")


def _secret_present(value: Any) -> bool:
    if value in (None, ""):
        return False
    if isinstance(value, memoryview):
        value = value.tobytes()
    if isinstance(value, bytes):
        return len(value) > 0
    return bool(str(value))


def _parse_metadata(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return dict(value)
    if isinstance(value, str) and value.strip():
        try:
            parsed = json.loads(value)
        except Exception:
            return {}
        if isinstance(parsed, dict):
            return parsed
    return {}


def _credential_row_to_dict(row: Sequence[Any]) -> Mapping[str, Any]:
    metadata = _parse_metadata(row[14] if len(row) > 14 else None)
    reset_details = credential_secret_reset_details(metadata)
    return {
        "id": int(row[0]),
        "name": row[1] or "",
        "description": row[2] or "",
        "site_id": row[3],
        "site_name": row[4] or "",
        "credential_type": (row[5] or "machine").lower(),
        "connection_type": (row[6] or "ssh").lower(),
        "username": row[7] or "",
        "has_password": _secret_present(row[8]),
        "has_private_key": _secret_present(row[9]),
        "has_private_key_passphrase": _secret_present(row[10]),
        "become_method": row[11] or "",
        "become_username": row[12] or "",
        "has_become_password": _secret_present(row[13]),
        "metadata": metadata,
        "secret_reset_required": bool(reset_details["reset_required"]),
        "lost_secret_fields": list(reset_details["lost_fields"]),
        "reset_at": int(reset_details["reset_at"] or 0),
        "created_at": row[15] or 0,
        "updated_at": row[16] or 0,
    }


class CredentialManagementService:
    """CRUD helper for stored remote-execution credentials."""

    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.logger = adapters.context.logger
        self.aegis = adapters.aegis_cipher_service

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, Any]]:
        if not operator_auth_allowed(
            db_conn_factory=self.db_conn_factory,
            aegis_cipher_service=self.aegis,
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

    def _require_login(self) -> Optional[Tuple[Dict[str, Any], int]]:
        if self._current_user():
            return None
        return {"error": "unauthorized"}, 401

    def _require_admin(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if (user.get("role") or "").lower() != "admin":
            return {"error": "forbidden"}, 403
        return None

    def _normalize_site_id(self, raw_value: Any) -> Optional[int]:
        if raw_value in (None, "", "null"):
            return None
        value = int(raw_value)
        return value if value > 0 else None

    def _normalize_connection_type(self, raw_value: Any) -> str:
        value = str(raw_value or "ssh").strip().lower()
        if value not in _ALLOWED_CONNECTION_TYPES:
            raise ValueError("invalid connection_type")
        return value

    def _normalize_credential_type(self, raw_value: Any) -> str:
        value = str(raw_value or "machine").strip().lower()
        if value not in _ALLOWED_CREDENTIAL_TYPES:
            raise ValueError("invalid credential_type")
        return value

    def _normalize_metadata(self, payload: Mapping[str, Any], existing: Optional[Dict[str, Any]] = None) -> Tuple[str, Dict[str, Any]]:
        metadata = dict(existing or {})
        metadata_updated = False

        if "metadata" in payload:
            candidate = payload.get("metadata")
            if candidate in (None, ""):
                metadata = {}
            elif isinstance(candidate, dict):
                metadata = dict(candidate)
            else:
                raise ValueError("metadata must be an object")
            metadata_updated = True
        elif "metadata_json" in payload:
            candidate = payload.get("metadata_json")
            if candidate in (None, ""):
                metadata = {}
            else:
                parsed = _parse_metadata(candidate)
                if not parsed and str(candidate or "").strip():
                    raise ValueError("metadata_json must be valid JSON")
                metadata = parsed
            metadata_updated = True

        if "winrm_transport" in payload:
            transport = str(payload.get("winrm_transport") or "").strip().lower()
            if transport:
                metadata["winrm_transport"] = transport
            else:
                metadata.pop("winrm_transport", None)
            metadata_updated = True

        return json.dumps(metadata if metadata_updated or existing is not None else {}, sort_keys=True), metadata

    def _strip_aegis_reset_metadata(self, metadata: Optional[Dict[str, Any]]) -> Dict[str, Any]:
        cleaned = dict(metadata or {})
        for key in _AEGIS_RESET_METADATA_KEYS:
            cleaned.pop(key, None)
        return cleaned

    def _merge_aegis_reset_metadata(
        self,
        metadata: Optional[Dict[str, Any]],
        *,
        existing_metadata: Optional[Dict[str, Any]] = None,
        resolved_fields: Sequence[str] = (),
    ) -> Dict[str, Any]:
        cleaned = self._strip_aegis_reset_metadata(metadata)
        details = credential_secret_reset_details(existing_metadata if existing_metadata is not None else metadata)
        if not details["reset_required"]:
            return cleaned
        normalized_resolved = []
        for field in resolved_fields:
            candidate = str(field or "").strip().lower()
            if candidate in _AFFECTED_SECRET_FIELDS and candidate not in normalized_resolved:
                normalized_resolved.append(candidate)
        remaining = [field for field in details["lost_fields"] if field not in normalized_resolved]
        return apply_credential_secret_reset_metadata(
            cleaned,
            lost_fields=remaining,
            reset_at=details["reset_at"] or _now_ts(),
        )

    def _protected_mutation_block(self) -> Optional[Tuple[Dict[str, Any], int]]:
        try:
            self.aegis.require_secret_storage_ready()
        except AegisNotConfiguredError as exc:
            return {"error": "aegis_not_configured", "message": str(exc)}, 409
        except AegisLockedError as exc:
            return {"error": "aegis_locked", "message": str(exc)}, 423
        except AegisDataCorruptionError as exc:
            return {"error": "corrupt_secret_store", "message": str(exc)}, 500
        except AegisCipherServiceError as exc:
            return {"error": "aegis_error", "message": str(exc)}, 500
        return None

    def _credential_query(self) -> str:
        return """
            SELECT
                c.id,
                c.name,
                c.description,
                c.site_id,
                s.name,
                c.credential_type,
                c.connection_type,
                c.username,
                c.password_encrypted,
                c.private_key_encrypted,
                c.private_key_passphrase_encrypted,
                c.become_method,
                c.become_username,
                c.become_password_encrypted,
                c.metadata_json,
                c.created_at,
                c.updated_at
              FROM credentials c
         LEFT JOIN sites s ON s.id = c.site_id
        """

    def _load_credential_row(self, cur, credential_id: int) -> Optional[Sequence[Any]]:
        cur.execute(
            self._credential_query() + " WHERE c.id=?",
            (int(credential_id),),
        )
        return cur.fetchone()

    def _ensure_site_exists(self, cur, site_id: Optional[int]) -> None:
        if site_id is None:
            return
        cur.execute("SELECT 1 FROM sites WHERE id=?", (int(site_id),))
        if not cur.fetchone():
            raise LookupError("site not found")

    def create_credential(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        mutation_block = self._protected_mutation_block()
        if mutation_block:
            payload, status = mutation_block
            return jsonify(payload), status

        payload = request.get_json(silent=True) or {}
        name = str(payload.get("name") or "").strip()
        description = str(payload.get("description") or "").strip()
        username = str(payload.get("username") or "").strip()
        become_method = str(payload.get("become_method") or "").strip().lower()
        become_username = str(payload.get("become_username") or "").strip()

        if not name:
            return jsonify({"error": "name is required"}), 400

        try:
            site_id = self._normalize_site_id(payload.get("site_id"))
            credential_type = self._normalize_credential_type(payload.get("credential_type"))
            connection_type = self._normalize_connection_type(payload.get("connection_type"))
            _metadata_json, _metadata = self._normalize_metadata(payload)
            metadata = self._merge_aegis_reset_metadata(_metadata, resolved_fields=_AFFECTED_SECRET_FIELDS)
            metadata_json = json.dumps(metadata, sort_keys=True)
            password_blob = self.aegis.encrypt_secret_for_blob(payload.get("password"))
            private_key_blob = self.aegis.encrypt_secret_for_blob(
                _normalize_private_key_text(payload.get("private_key")) or None
            )
            passphrase_blob = self.aegis.encrypt_secret_for_blob(payload.get("private_key_passphrase"))
            become_password_blob = self.aegis.encrypt_secret_for_blob(payload.get("become_password"))
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except Exception as exc:
            self.logger.debug("Failed to prepare credential %s", name, exc_info=True)
            return jsonify({"error": str(exc)}), 500

        now_ts = _now_ts()
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            self._ensure_site_exists(cur, site_id)
            cur.execute(
                """
                INSERT INTO credentials(
                    name,
                    description,
                    site_id,
                    credential_type,
                    connection_type,
                    username,
                    password_encrypted,
                    private_key_encrypted,
                    private_key_passphrase_encrypted,
                    become_method,
                    become_username,
                    become_password_encrypted,
                    metadata_json,
                    created_at,
                    updated_at
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    name,
                    description,
                    site_id,
                    credential_type,
                    connection_type,
                    username,
                    password_blob,
                    private_key_blob,
                    passphrase_blob,
                    become_method,
                    become_username,
                    become_password_blob,
                    metadata_json,
                    now_ts,
                    now_ts,
                ),
            )
            credential_id = int(cur.lastrowid or 0)
            if credential_id <= 0:
                cur.execute("SELECT id FROM credentials WHERE LOWER(name)=LOWER(?)", (name,))
                row = cur.fetchone()
                credential_id = int(row[0]) if row and row[0] is not None else 0
            row = self._load_credential_row(cur, credential_id)
            conn.commit()
            if not row:
                return jsonify({"error": "credential creation failed"}), 500
            return jsonify({"status": "ok", "credential": _credential_row_to_dict(row)})
        except LookupError:
            return jsonify({"error": "site not found"}), 400
        except sqlite3.IntegrityError:
            return jsonify({"error": "credential name already exists"}), 409
        except Exception as exc:
            self.logger.debug("Failed to create credential %s", name, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

    def update_credential(self, credential_id: int):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        mutation_block = self._protected_mutation_block()
        if mutation_block:
            payload, status = mutation_block
            return jsonify(payload), status

        payload = request.get_json(silent=True) or {}
        conn: Optional[sqlite3.Connection] = None
        existing: Optional[Sequence[Any]] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    name,
                    description,
                    site_id,
                    credential_type,
                    connection_type,
                    username,
                    password_encrypted,
                    private_key_encrypted,
                    private_key_passphrase_encrypted,
                    become_method,
                    become_username,
                    become_password_encrypted,
                    metadata_json
                  FROM credentials
                 WHERE id=?
                """,
                (int(credential_id),),
            )
            existing = cur.fetchone()
        except Exception as exc:
            self.logger.debug("Failed to load credential %s for update", credential_id, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

        if not existing:
            return jsonify({"error": "credential not found"}), 404

        name = str(payload.get("name") if "name" in payload else existing[1] or "").strip()
        description = str(payload.get("description") if "description" in payload else existing[2] or "").strip()
        username = str(payload.get("username") if "username" in payload else existing[6] or "").strip()
        become_method = str(payload.get("become_method") if "become_method" in payload else existing[10] or "").strip().lower()
        become_username = str(payload.get("become_username") if "become_username" in payload else existing[11] or "").strip()
        if not name:
            return jsonify({"error": "name is required"}), 400

        try:
            site_id = self._normalize_site_id(payload.get("site_id")) if "site_id" in payload else existing[3]
            site_id = int(site_id) if site_id is not None else None
            credential_type = (
                self._normalize_credential_type(payload.get("credential_type"))
                if "credential_type" in payload
                else str(existing[4] or "machine").lower()
            )
            connection_type = (
                self._normalize_connection_type(payload.get("connection_type"))
                if "connection_type" in payload
                else str(existing[5] or "ssh").lower()
            )
            existing_metadata = _parse_metadata(existing[13])
            _metadata_json, _metadata = self._normalize_metadata(payload, existing=existing_metadata)
            password_blob = (
                self.aegis.encrypt_secret_for_blob(payload.get("password"))
                if "password" in payload
                else (None if payload.get("clear_password") else existing[7])
            )
            private_key_blob = (
                self.aegis.encrypt_secret_for_blob(
                    _normalize_private_key_text(payload.get("private_key")) or None
                )
                if "private_key" in payload
                else (None if payload.get("clear_private_key") else existing[8])
            )
            passphrase_blob = (
                self.aegis.encrypt_secret_for_blob(payload.get("private_key_passphrase"))
                if "private_key_passphrase" in payload
                else (None if payload.get("clear_private_key_passphrase") else existing[9])
            )
            become_password_blob = (
                self.aegis.encrypt_secret_for_blob(payload.get("become_password"))
                if "become_password" in payload
                else (None if payload.get("clear_become_password") else existing[12])
            )
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except Exception as exc:
            self.logger.debug("Failed to prepare credential update %s", credential_id, exc_info=True)
            return jsonify({"error": str(exc)}), 500

        resolved_secret_fields = []
        if "password" in payload or payload.get("clear_password"):
            resolved_secret_fields.append("password")
        if "private_key" in payload or payload.get("clear_private_key"):
            resolved_secret_fields.append("private_key")
        if "private_key_passphrase" in payload or payload.get("clear_private_key_passphrase"):
            resolved_secret_fields.append("private_key_passphrase")
        if "become_password" in payload or payload.get("clear_become_password"):
            resolved_secret_fields.append("become_password")
        metadata = self._merge_aegis_reset_metadata(
            _metadata,
            existing_metadata=existing_metadata,
            resolved_fields=resolved_secret_fields,
        )
        metadata_json = json.dumps(metadata, sort_keys=True)

        now_ts = _now_ts()
        conn = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            self._ensure_site_exists(cur, site_id)
            cur.execute(
                """
                UPDATE credentials
                   SET name=?,
                       description=?,
                       site_id=?,
                       credential_type=?,
                       connection_type=?,
                       username=?,
                       password_encrypted=?,
                       private_key_encrypted=?,
                       private_key_passphrase_encrypted=?,
                       become_method=?,
                       become_username=?,
                       become_password_encrypted=?,
                       metadata_json=?,
                       updated_at=?
                 WHERE id=?
                """,
                (
                    name,
                    description,
                    site_id,
                    credential_type,
                    connection_type,
                    username,
                    password_blob,
                    private_key_blob,
                    passphrase_blob,
                    become_method,
                    become_username,
                    become_password_blob,
                    metadata_json,
                    now_ts,
                    int(credential_id),
                ),
            )
            row = self._load_credential_row(cur, int(credential_id))
            conn.commit()
            if not row:
                return jsonify({"error": "credential not found"}), 404
            return jsonify({"status": "ok", "credential": _credential_row_to_dict(row)})
        except LookupError:
            return jsonify({"error": "site not found"}), 400
        except sqlite3.IntegrityError:
            return jsonify({"error": "credential name already exists"}), 409
        except Exception as exc:
            self.logger.debug("Failed to update credential %s", credential_id, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

    def delete_credential(self, credential_id: int):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        mutation_block = self._protected_mutation_block()
        if mutation_block:
            payload, status = mutation_block
            return jsonify(payload), status

        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            cur.execute("DELETE FROM credentials WHERE id=?", (int(credential_id),))
            deleted = int(cur.rowcount or 0)
            conn.commit()
            if deleted == 0:
                return jsonify({"error": "credential not found"}), 404
            return jsonify({"status": "ok"})
        except Exception as exc:
            self.logger.debug("Failed to delete credential %s", credential_id, exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()


def register_credential_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register credential management endpoints."""

    service = CredentialManagementService(app, adapters)
    blueprint = Blueprint("credentials_access", __name__)

    @blueprint.route("/api/credentials", methods=["POST"])
    def _credential_create():
        return service.create_credential()

    @blueprint.route("/api/credentials/<int:credential_id>", methods=["PUT"])
    def _credential_update(credential_id: int):
        return service.update_credential(credential_id)

    @blueprint.route("/api/credentials/<int:credential_id>", methods=["DELETE"])
    def _credential_delete(credential_id: int):
        return service.delete_credential(credential_id)

    app.register_blueprint(blueprint)
