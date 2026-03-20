# ======================================================
# Data\Engine\services\aegis_cipher.py
# Description: Engine-global Aegis Cipher service for encrypted secret storage and runtime unlock state.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine-global Aegis Cipher service for protected secret storage."""

from __future__ import annotations

import base64
import json
import logging
import threading
import time
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence

from Data.Engine.db import dbapi as sqlite3

from ..crypto.aegis import (
    AegisCryptoError,
    KEY_LENGTH_BYTES,
    SCRYPT_NAME,
    SCRYPT_N,
    SCRYPT_P,
    SCRYPT_R,
    decrypt_text,
    derive_key,
    encrypt_text,
    is_aegis_envelope,
    random_salt,
)

_STATE_ROW_ID = 1
_VERIFICATION_PLAINTEXT = "7f5c2a1d-6e8b-4f3b-a0d1-9c3f77b34d52"
_CREDENTIAL_SECRET_STATE_KEY = "aegis_secret_state"
_CREDENTIAL_LOST_FIELDS_KEY = "aegis_lost_secret_fields"
_CREDENTIAL_RESET_AT_KEY = "aegis_reset_at"
_CREDENTIAL_SECRET_RESET_STATE = "reset_required"
_CREDENTIAL_SECRET_FIELDS = (
    "password",
    "private_key",
    "private_key_passphrase",
    "become_password",
)
def _now_ts() -> int:
    return int(time.time())


class AegisCipherServiceError(RuntimeError):
    """Base exception for Aegis service failures."""


class AegisNotConfiguredError(AegisCipherServiceError):
    """Raised when a protected mutation requires an Aegis setup."""


class AegisLockedError(AegisCipherServiceError):
    """Raised when a configured Engine is still locked."""


class AegisInvalidCipherError(AegisCipherServiceError):
    """Raised when the provided cipher does not match the stored verification token."""


class AegisDataCorruptionError(AegisCipherServiceError):
    """Raised when protected storage contains unexpected or undecryptable data."""


class AegisSecretResetRequiredError(AegisCipherServiceError):
    """Raised when a credential record exists but its stored secret material was destroyed."""


def _normalize_lost_secret_fields(fields: Iterable[Any]) -> List[str]:
    allowed = set(_CREDENTIAL_SECRET_FIELDS)
    normalized: List[str] = []
    for value in fields or ():
        candidate = str(value or "").strip().lower()
        if candidate in allowed and candidate not in normalized:
            normalized.append(candidate)
    return normalized


def credential_secret_reset_details(metadata: Optional[Mapping[str, Any]]) -> Dict[str, Any]:
    source = dict(metadata or {})
    state = str(source.get(_CREDENTIAL_SECRET_STATE_KEY) or "").strip().lower()
    lost_fields = _normalize_lost_secret_fields(source.get(_CREDENTIAL_LOST_FIELDS_KEY) or [])
    try:
        reset_at = int(source.get(_CREDENTIAL_RESET_AT_KEY) or 0)
    except Exception:
        reset_at = 0
    reset_required = state == _CREDENTIAL_SECRET_RESET_STATE and bool(lost_fields)
    return {
        "reset_required": reset_required,
        "lost_fields": lost_fields if reset_required else [],
        "reset_at": reset_at if reset_required else 0,
    }


def credential_secret_reset_required(metadata: Optional[Mapping[str, Any]]) -> bool:
    return bool(credential_secret_reset_details(metadata).get("reset_required"))


def apply_credential_secret_reset_metadata(
    metadata: Optional[Mapping[str, Any]],
    *,
    lost_fields: Iterable[Any],
    reset_at: Optional[int],
) -> Dict[str, Any]:
    next_metadata = dict(metadata or {})
    normalized_fields = _normalize_lost_secret_fields(lost_fields)
    if normalized_fields:
        next_metadata[_CREDENTIAL_SECRET_STATE_KEY] = _CREDENTIAL_SECRET_RESET_STATE
        next_metadata[_CREDENTIAL_LOST_FIELDS_KEY] = normalized_fields
        next_metadata[_CREDENTIAL_RESET_AT_KEY] = int(reset_at or _now_ts())
        return next_metadata
    next_metadata.pop(_CREDENTIAL_SECRET_STATE_KEY, None)
    next_metadata.pop(_CREDENTIAL_LOST_FIELDS_KEY, None)
    next_metadata.pop(_CREDENTIAL_RESET_AT_KEY, None)
    return next_metadata


def clear_credential_secret_reset_metadata(
    metadata: Optional[Mapping[str, Any]],
    *,
    resolved_fields: Iterable[Any],
) -> Dict[str, Any]:
    details = credential_secret_reset_details(metadata)
    if not details["reset_required"]:
        return dict(metadata or {})
    resolved = set(_normalize_lost_secret_fields(resolved_fields))
    remaining = [field for field in details["lost_fields"] if field not in resolved]
    return apply_credential_secret_reset_metadata(
        metadata,
        lost_fields=remaining,
        reset_at=details["reset_at"] or None,
    )


class AegisCipherService:
    """Manage Aegis Cipher setup, unlock state, and secret serialization."""

    secret_scope = ("credentials", "github_token")
    unlock_scope = "engine_global"

    def __init__(
        self,
        *,
        db_conn_factory: Callable[[], sqlite3.Connection],
        logger: Optional[logging.Logger] = None,
        service_log: Optional[Callable[[str, str, Optional[str]], None]] = None,
    ) -> None:
        self._db_conn_factory = db_conn_factory
        self._logger = logger or logging.getLogger(__name__)
        self._service_log = service_log
        self._lock = threading.RLock()
        self._active_key: Optional[bytes] = None

    # ------------------------------------------------------------------
    # Status helpers
    # ------------------------------------------------------------------
    def status(self) -> Dict[str, Any]:
        state = self._state()
        with self._lock:
            locked = bool(state and self._active_key is None)
        return {
            "configured": bool(state),
            "locked": locked,
            "unlock_scope": self.unlock_scope,
            "secret_scope": list(self.secret_scope),
            "updated_at": int(state.get("updated_at") or 0) if state else 0,
        }

    def is_configured(self) -> bool:
        return bool(self._state())

    def is_locked(self) -> bool:
        state = self._state()
        if not state:
            return False
        with self._lock:
            return self._active_key is None

    def clear_memory_key(self) -> None:
        with self._lock:
            self._active_key = None

    # ------------------------------------------------------------------
    # Mutation gates
    # ------------------------------------------------------------------
    def require_secret_storage_ready(self) -> None:
        state = self._state()
        if not state:
            raise AegisNotConfiguredError(
                "Aegis Cipher must be set up before protected secrets can be stored."
            )
        self._require_active_key()

    # ------------------------------------------------------------------
    # Setup / unlock / rotation
    # ------------------------------------------------------------------
    def setup(self, cipher: str) -> Dict[str, Any]:
        self._validate_cipher(cipher)
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn_factory()
            cur = conn.cursor()
            state = self._state_from_cursor(cur)
            if state:
                raise AegisCipherServiceError("Aegis Cipher is already configured.")

            salt = random_salt()
            params = self._build_kdf_params(salt=salt)
            key = derive_key(cipher, salt=salt, n=params["n"], r=params["r"], p=params["p"], length=params["length"])
            verification_token = encrypt_text(_VERIFICATION_PLAINTEXT, key=key)

            self._migrate_legacy_credentials(cur, key)
            self._migrate_legacy_github_token(cur, key)

            now_ts = _now_ts()
            cur.execute(
                """
                INSERT INTO aegis_cipher_state(
                    id,
                    kdf_name,
                    kdf_params_json,
                    verification_token,
                    created_at,
                    updated_at
                ) VALUES (?,?,?,?,?,?)
                """,
                (
                    _STATE_ROW_ID,
                    SCRYPT_NAME,
                    json.dumps(params, sort_keys=True),
                    verification_token,
                    now_ts,
                    now_ts,
                ),
            )
            conn.commit()
            with self._lock:
                self._active_key = key
        except Exception:
            if conn is not None:
                try:
                    conn.rollback()
                except Exception:
                    pass
            raise
        finally:
            if conn is not None:
                conn.close()
        self._log("Aegis Cipher configured and Engine secrets encrypted.")
        return self.status()

    def unlock(self, cipher: str) -> Dict[str, Any]:
        self._validate_cipher(cipher)
        state = self._state(required=True)
        try:
            key = self._derive_key_from_state(cipher, state)
            verification_plaintext = decrypt_text(str(state["verification_token"] or ""), key=key)
        except AegisCryptoError as exc:
            raise AegisInvalidCipherError("Incorrect Aegis Cipher.") from exc
        if verification_plaintext != _VERIFICATION_PLAINTEXT:
            raise AegisInvalidCipherError("Incorrect Aegis Cipher.")
        with self._lock:
            self._active_key = key
        self._log("Aegis Cipher accepted; Engine secrets unlocked in memory.")
        return self.status()

    def rotate(self, current_cipher: str, new_cipher: str) -> Dict[str, Any]:
        self._validate_cipher(current_cipher)
        self._validate_cipher(new_cipher)
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn_factory()
            cur = conn.cursor()
            state = self._state_from_cursor(cur)
            if not state:
                raise AegisNotConfiguredError("Aegis Cipher is not configured.")
            try:
                old_key = self._derive_key_from_state(current_cipher, state)
                verification_plaintext = decrypt_text(str(state["verification_token"] or ""), key=old_key)
            except AegisCryptoError as exc:
                raise AegisInvalidCipherError("Current Aegis Cipher is incorrect.") from exc
            if verification_plaintext != _VERIFICATION_PLAINTEXT:
                raise AegisInvalidCipherError("Current Aegis Cipher is incorrect.")

            salt = random_salt()
            params = self._build_kdf_params(salt=salt)
            new_key = derive_key(
                new_cipher,
                salt=salt,
                n=params["n"],
                r=params["r"],
                p=params["p"],
                length=params["length"],
            )
            self._reencrypt_credentials(cur, old_key=old_key, new_key=new_key)
            self._reencrypt_github_token(cur, old_key=old_key, new_key=new_key)

            now_ts = _now_ts()
            cur.execute(
                """
                UPDATE aegis_cipher_state
                   SET kdf_name=?,
                       kdf_params_json=?,
                       verification_token=?,
                       updated_at=?
                 WHERE id=?
                """,
                (
                    SCRYPT_NAME,
                    json.dumps(params, sort_keys=True),
                    encrypt_text(_VERIFICATION_PLAINTEXT, key=new_key),
                    now_ts,
                    _STATE_ROW_ID,
                ),
            )
            conn.commit()
            with self._lock:
                self._active_key = new_key
        except Exception:
            if conn is not None:
                try:
                    conn.rollback()
                except Exception:
                    pass
            raise
        finally:
            if conn is not None:
                conn.close()
        self._log("Aegis Cipher rotated and protected secrets re-encrypted.")
        return self.status()

    def force_reset(self) -> Dict[str, Any]:
        state = self._state(required=True)
        conn: Optional[sqlite3.Connection] = None
        affected_credential_ids: List[int] = []
        affected_credentials = 0
        disabled_jobs = 0
        github_token_reset = False
        now_ts = _now_ts()
        try:
            conn = self._db_conn_factory()
            cur = conn.cursor()

            cur.execute(
                """
                SELECT
                    id,
                    password_encrypted,
                    private_key_encrypted,
                    private_key_passphrase_encrypted,
                    become_password_encrypted,
                    metadata_json
                  FROM credentials
                 ORDER BY id ASC
                """
            )
            rows = cur.fetchall() or []
            for row in rows:
                credential_id = int(row[0])
                password_text = self._decode_db_text(row[1])
                private_key_text = self._decode_db_text(row[2])
                passphrase_text = self._decode_db_text(row[3])
                become_password_text = self._decode_db_text(row[4])
                metadata = self._parse_metadata_json(row[5])
                lost_fields = self._lost_secret_fields_for_reset(
                    password=password_text,
                    private_key=private_key_text,
                    private_key_passphrase=passphrase_text,
                    become_password=become_password_text,
                )
                if not lost_fields:
                    continue
                metadata = apply_credential_secret_reset_metadata(
                    metadata,
                    lost_fields=lost_fields,
                    reset_at=now_ts,
                )
                cur.execute(
                    """
                    UPDATE credentials
                       SET password_encrypted=NULL,
                           private_key_encrypted=NULL,
                           private_key_passphrase_encrypted=NULL,
                           become_password_encrypted=NULL,
                           metadata_json=?,
                           updated_at=?
                     WHERE id=?
                    """,
                    (
                        json.dumps(metadata, sort_keys=True),
                        now_ts,
                        credential_id,
                    ),
                )
                affected_credential_ids.append(credential_id)

            affected_credentials = len(affected_credential_ids)

            cur.execute("SELECT token, reset_required, reset_at FROM github_token LIMIT 1")
            github_row = cur.fetchone()
            github_token_text = self._decode_db_text(github_row[0]) if github_row else ""
            if github_token_text:
                self._validate_encrypted_or_empty(github_token_text)
                github_token_reset = True
            cur.execute("DELETE FROM github_token")
            if github_token_reset:
                cur.execute(
                    """
                    INSERT INTO github_token(token, reset_required, reset_at)
                    VALUES (?,?,?)
                    """,
                    (None, 1, now_ts),
                )

            if affected_credential_ids:
                placeholders = ",".join("?" for _ in affected_credential_ids)
                cur.execute(
                    f"""
                    UPDATE scheduled_jobs
                       SET enabled=0,
                           updated_at=?
                     WHERE credential_id IN ({placeholders})
                       AND COALESCE(enabled, 0) <> 0
                    """,
                    (now_ts, *affected_credential_ids),
                )
                disabled_jobs = int(cur.rowcount or 0)

            cur.execute("DELETE FROM aegis_cipher_state WHERE id=?", (_STATE_ROW_ID,))
            conn.commit()
            with self._lock:
                self._active_key = None
        except Exception:
            if conn is not None:
                try:
                    conn.rollback()
                except Exception:
                    pass
            raise
        finally:
            if conn is not None:
                conn.close()

        self._log(
            "Aegis Cipher force reset completed. Protected secret material was destroyed and affected scheduled jobs were disabled.",
            level="WARNING",
        )
        return {
            **self.status(),
            "force_reset": True,
            "affected_credentials": affected_credentials,
            "disabled_jobs": disabled_jobs,
            "github_token_reset": github_token_reset,
        }

    # ------------------------------------------------------------------
    # Secret serialization helpers
    # ------------------------------------------------------------------
    def encrypt_secret_for_blob(self, value: Any) -> Optional[bytes]:
        normalized = self._coerce_secret_text(value)
        if normalized is None:
            return None
        key = self._require_active_key(required_configured=True)
        return encrypt_text(normalized, key=key).encode("utf-8")

    def encrypt_secret_for_text(self, value: Any) -> Optional[str]:
        normalized = self._coerce_secret_text(value)
        if normalized is None:
            return None
        key = self._require_active_key(required_configured=True)
        return encrypt_text(normalized, key=key)

    def decrypt_secret_blob(self, value: Any) -> str:
        text = self._decode_db_text(value)
        return self._decrypt_secret_text_internal(text)

    def decrypt_secret_text(self, value: Any) -> str:
        text = self._decode_db_text(value)
        return self._decrypt_secret_text_internal(text)

    # ------------------------------------------------------------------
    # Internal DB helpers
    # ------------------------------------------------------------------
    def _db_conn(self) -> sqlite3.Connection:
        return self._db_conn_factory()

    def _state(self, *, required: bool = False) -> Optional[Dict[str, Any]]:
        conn: Optional[sqlite3.Connection] = None
        try:
            conn = self._db_conn()
            cur = conn.cursor()
            state = self._state_from_cursor(cur)
        finally:
            if conn is not None:
                conn.close()
        if required and not state:
            raise AegisNotConfiguredError("Aegis Cipher is not configured.")
        return state

    def _state_from_cursor(self, cur) -> Optional[Dict[str, Any]]:
        cur.execute(
            """
            SELECT id, kdf_name, kdf_params_json, verification_token, created_at, updated_at
              FROM aegis_cipher_state
             WHERE id=?
            """,
            (_STATE_ROW_ID,),
        )
        row = cur.fetchone()
        if not row:
            return None
        return {
            "id": int(row[0]),
            "kdf_name": row[1] or "",
            "kdf_params_json": row[2] or "{}",
            "verification_token": row[3] or "",
            "created_at": int(row[4] or 0),
            "updated_at": int(row[5] or 0),
        }

    def _build_kdf_params(self, *, salt: bytes) -> Dict[str, Any]:
        return {
            "salt_b64": base64.b64encode(salt).decode("ascii"),
            "n": SCRYPT_N,
            "r": SCRYPT_R,
            "p": SCRYPT_P,
            "length": KEY_LENGTH_BYTES,
        }

    def _parse_kdf_params(self, state: Dict[str, Any]) -> Dict[str, Any]:
        raw = state.get("kdf_params_json") or "{}"
        try:
            params = json.loads(raw)
        except Exception as exc:
            raise AegisDataCorruptionError("Stored Aegis KDF parameters are invalid JSON.") from exc
        if not isinstance(params, dict):
            raise AegisDataCorruptionError("Stored Aegis KDF parameters are invalid.")
        if str(state.get("kdf_name") or "").strip().lower() != SCRYPT_NAME:
            raise AegisDataCorruptionError("Stored Aegis KDF is not supported.")
        salt_b64 = str(params.get("salt_b64") or "").strip()
        if not salt_b64:
            raise AegisDataCorruptionError("Stored Aegis salt is missing.")
        try:
            salt = base64.b64decode(salt_b64, validate=True)
        except Exception as exc:
            raise AegisDataCorruptionError("Stored Aegis salt is not valid Base64.") from exc
        return {
            "salt": salt,
            "n": int(params.get("n") or SCRYPT_N),
            "r": int(params.get("r") or SCRYPT_R),
            "p": int(params.get("p") or SCRYPT_P),
            "length": int(params.get("length") or KEY_LENGTH_BYTES),
        }

    def _derive_key_from_state(self, cipher: str, state: Dict[str, Any]) -> bytes:
        params = self._parse_kdf_params(state)
        return derive_key(
            cipher,
            salt=params["salt"],
            n=params["n"],
            r=params["r"],
            p=params["p"],
            length=params["length"],
        )

    def _require_active_key(self, *, required_configured: bool = False) -> bytes:
        if required_configured and not self._state():
            raise AegisNotConfiguredError(
                "Aegis Cipher must be set up before protected secrets can be stored."
            )
        state = self._state()
        if state:
            with self._lock:
                if self._active_key is None:
                    raise AegisLockedError(
                        "Aegis Cipher has not been entered; protected secrets remain locked."
                    )
                return self._active_key
        if required_configured:
            raise AegisNotConfiguredError(
                "Aegis Cipher must be set up before protected secrets can be stored."
            )
        raise AegisLockedError("Aegis Cipher key is unavailable.")

    def _validate_cipher(self, cipher: str) -> None:
        if str(cipher or "") == "":
            raise AegisCipherServiceError("Aegis Cipher is required.")

    def _decode_db_text(self, value: Any) -> str:
        if value is None:
            return ""
        if isinstance(value, memoryview):
            value = value.tobytes()
        if isinstance(value, bytes):
            try:
                return value.decode("utf-8")
            except Exception as exc:
                raise AegisDataCorruptionError("Stored protected value is not valid UTF-8.") from exc
        return str(value)

    def _coerce_secret_text(self, value: Any) -> Optional[str]:
        if value is None:
            return None
        text = str(value)
        if text == "":
            return None
        return text

    def _decrypt_secret_text_internal(self, text: str) -> str:
        state = self._state()
        if not text:
            return ""
        if not state:
            if is_aegis_envelope(text):
                raise AegisDataCorruptionError(
                    "Aegis-encrypted secret exists before Aegis Cipher setup."
                )
            return text
        if not is_aegis_envelope(text):
            raise AegisDataCorruptionError(
                "Protected secret is not stored as an Aegis envelope."
            )
        key = self._require_active_key()
        try:
            return decrypt_text(text, key=key)
        except AegisCryptoError as exc:
            raise AegisDataCorruptionError("Protected secret could not be decrypted.") from exc

    def _parse_metadata_json(self, value: Any) -> Dict[str, Any]:
        if isinstance(value, dict):
            return dict(value)
        if not str(value or "").strip():
            return {}
        try:
            parsed = json.loads(str(value))
        except Exception:
            return {}
        if isinstance(parsed, dict):
            return dict(parsed)
        return {}

    def _validate_encrypted_or_empty(self, text: str) -> None:
        if not text:
            return
        if not is_aegis_envelope(text):
            raise AegisDataCorruptionError(
                "Protected secret is not stored as an Aegis envelope."
            )

    def _lost_secret_fields_for_reset(
        self,
        *,
        password: str,
        private_key: str,
        private_key_passphrase: str,
        become_password: str,
    ) -> List[str]:
        lost_fields: List[str] = []
        for field_name, value in (
            ("password", password),
            ("private_key", private_key),
            ("private_key_passphrase", private_key_passphrase),
            ("become_password", become_password),
        ):
            if not value:
                continue
            self._validate_encrypted_or_empty(value)
            lost_fields.append(field_name)
        return lost_fields

    def _migrate_legacy_credentials(self, cur, key: bytes) -> None:
        cur.execute(
            """
            SELECT
                id,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_password_encrypted
              FROM credentials
             ORDER BY id ASC
            """
        )
        rows = cur.fetchall() or []
        for row in rows:
            credential_id = int(row[0])
            values = [self._legacy_secret_or_raise(value) for value in row[1:]]
            cur.execute(
                """
                UPDATE credentials
                   SET password_encrypted=?,
                       private_key_encrypted=?,
                       private_key_passphrase_encrypted=?,
                       become_password_encrypted=?
                 WHERE id=?
                """,
                (
                    self._encrypt_blob_with_key(values[0], key),
                    self._encrypt_blob_with_key(values[1], key),
                    self._encrypt_blob_with_key(values[2], key),
                    self._encrypt_blob_with_key(values[3], key),
                    credential_id,
                ),
            )

    def _reencrypt_credentials(self, cur, *, old_key: bytes, new_key: bytes) -> None:
        cur.execute(
            """
            SELECT
                id,
                password_encrypted,
                private_key_encrypted,
                private_key_passphrase_encrypted,
                become_password_encrypted
              FROM credentials
             ORDER BY id ASC
            """
        )
        rows = cur.fetchall() or []
        for row in rows:
            credential_id = int(row[0])
            plaintexts = [
                self._decrypt_encrypted_or_raise(value, old_key) for value in row[1:]
            ]
            cur.execute(
                """
                UPDATE credentials
                   SET password_encrypted=?,
                       private_key_encrypted=?,
                       private_key_passphrase_encrypted=?,
                       become_password_encrypted=?
                 WHERE id=?
                """,
                (
                    self._encrypt_blob_with_key(plaintexts[0], new_key),
                    self._encrypt_blob_with_key(plaintexts[1], new_key),
                    self._encrypt_blob_with_key(plaintexts[2], new_key),
                    self._encrypt_blob_with_key(plaintexts[3], new_key),
                    credential_id,
                ),
            )

    def _migrate_legacy_github_token(self, cur, key: bytes) -> None:
        cur.execute("SELECT token, reset_required, reset_at FROM github_token LIMIT 1")
        row = cur.fetchone()
        if not row:
            return
        token = self._legacy_secret_or_raise(row[0])
        reset_required = int(row[1] or 0) if len(row) > 1 else 0
        reset_at = int(row[2] or 0) if len(row) > 2 and row[2] is not None else None
        cur.execute("DELETE FROM github_token")
        if token is not None:
            cur.execute(
                "INSERT INTO github_token (token, reset_required, reset_at) VALUES (?,?,?)",
                (encrypt_text(token, key=key), 0, None),
            )
        elif reset_required:
            cur.execute(
                "INSERT INTO github_token (token, reset_required, reset_at) VALUES (?,?,?)",
                (None, 1, reset_at),
            )

    def _reencrypt_github_token(self, cur, *, old_key: bytes, new_key: bytes) -> None:
        cur.execute("SELECT token, reset_required, reset_at FROM github_token LIMIT 1")
        row = cur.fetchone()
        token = self._decrypt_encrypted_or_raise(row[0], old_key) if row else None
        reset_required = int(row[1] or 0) if row and len(row) > 1 else 0
        reset_at = int(row[2] or 0) if row and len(row) > 2 and row[2] is not None else None
        cur.execute("DELETE FROM github_token")
        if token is not None:
            cur.execute(
                "INSERT INTO github_token (token, reset_required, reset_at) VALUES (?,?,?)",
                (encrypt_text(token, key=new_key), 0, None),
            )
        elif reset_required:
            cur.execute(
                "INSERT INTO github_token (token, reset_required, reset_at) VALUES (?,?,?)",
                (None, 1, reset_at),
            )

    def _legacy_secret_or_raise(self, value: Any) -> Optional[str]:
        text = self._decode_db_text(value)
        if text == "":
            return None
        if is_aegis_envelope(text):
            raise AegisDataCorruptionError(
                "Protected secret is already Aegis-encrypted before setup."
            )
        return text

    def _decrypt_encrypted_or_raise(self, value: Any, key: bytes) -> Optional[str]:
        text = self._decode_db_text(value)
        if text == "":
            return None
        if not is_aegis_envelope(text):
            raise AegisDataCorruptionError(
                "Protected secret is not stored as an Aegis envelope."
            )
        try:
            return decrypt_text(text, key=key)
        except AegisCryptoError as exc:
            raise AegisDataCorruptionError("Protected secret could not be decrypted.") from exc

    def _encrypt_blob_with_key(self, value: Optional[str], key: bytes) -> Optional[bytes]:
        if value is None:
            return None
        return encrypt_text(value, key=key).encode("utf-8")

    def _log(self, message: str, *, level: str = "INFO") -> None:
        if callable(self._service_log):
            try:
                self._service_log("aegis_cipher", message, scope="ADMIN", level=level)
            except Exception:
                self._logger.debug("Failed to write Aegis service log.", exc_info=True)
        try:
            numeric = getattr(logging, level.upper(), logging.INFO)
            self._logger.log(numeric, message)
        except Exception:
            pass


__all__ = [
    "AegisCipherService",
    "AegisCipherServiceError",
    "AegisDataCorruptionError",
    "AegisInvalidCipherError",
    "AegisLockedError",
    "AegisNotConfiguredError",
    "AegisSecretResetRequiredError",
    "apply_credential_secret_reset_metadata",
    "clear_credential_secret_reset_metadata",
    "credential_secret_reset_details",
    "credential_secret_reset_required",
]
