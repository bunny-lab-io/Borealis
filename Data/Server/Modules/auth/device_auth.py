from __future__ import annotations

import functools
from dataclasses import dataclass
from typing import Any, Callable, Dict, Optional

import jwt
from flask import g, jsonify, request

from Modules.auth.dpop import DPoPValidator, DPoPVerificationError, DPoPReplayError


@dataclass
class DeviceAuthContext:
    guid: str
    ssl_key_fingerprint: str
    token_version: int
    access_token: str
    claims: Dict[str, Any]
    dpop_jkt: Optional[str]
    status: str


class DeviceAuthError(Exception):
    status_code = 401
    error_code = "unauthorized"

    def __init__(self, message: str = "unauthorized", *, status_code: Optional[int] = None):
        super().__init__(message)
        if status_code is not None:
            self.status_code = status_code
        self.message = message


class DeviceAuthManager:
    def __init__(
        self,
        *,
        db_conn_factory: Callable[[], Any],
        jwt_service,
        dpop_validator: Optional[DPoPValidator],
        log: Callable[[str, str], None],
    ) -> None:
        self._db_conn_factory = db_conn_factory
        self._jwt_service = jwt_service
        self._dpop_validator = dpop_validator
        self._log = log

    def authenticate(self) -> DeviceAuthContext:
        auth_header = request.headers.get("Authorization", "")
        if not auth_header.startswith("Bearer "):
            raise DeviceAuthError("missing_authorization")
        token = auth_header[len("Bearer ") :].strip()
        if not token:
            raise DeviceAuthError("missing_authorization")

        try:
            claims = self._jwt_service.decode(token)
        except jwt.ExpiredSignatureError:
            raise DeviceAuthError("token_expired")
        except Exception:
            raise DeviceAuthError("invalid_token")

        guid = str(claims.get("guid") or "").strip()
        fingerprint = str(claims.get("ssl_key_fingerprint") or "").lower().strip()
        token_version = int(claims.get("token_version") or 0)
        if not guid or not fingerprint or token_version <= 0:
            raise DeviceAuthError("invalid_claims")

        conn = self._db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT guid, ssl_key_fingerprint, token_version, status
                  FROM devices
                 WHERE guid = ?
                """,
                (guid,),
            )
            row = cur.fetchone()
        finally:
            conn.close()

        if not row:
            raise DeviceAuthError("device_not_found", status_code=403)

        db_guid, db_fp, db_token_version, status = row

        if str(db_guid or "").lower() != guid.lower():
            raise DeviceAuthError("device_guid_mismatch", status_code=403)

        db_fp = (db_fp or "").lower().strip()
        if db_fp and db_fp != fingerprint:
            raise DeviceAuthError("fingerprint_mismatch", status_code=403)

        if db_token_version and db_token_version > token_version:
            raise DeviceAuthError("token_version_revoked", status_code=401)

        status_normalized = (status or "active").strip().lower()
        allowed_statuses = {"active", "quarantined"}
        if status_normalized not in allowed_statuses:
            raise DeviceAuthError("device_revoked", status_code=403)
        if status_normalized == "quarantined":
            self._log("server", f"device {guid} is quarantined; limited access for {request.path}")

        dpop_jkt: Optional[str] = None
        dpop_proof = request.headers.get("DPoP")
        if dpop_proof:
            if not self._dpop_validator:
                raise DeviceAuthError("dpop_not_supported", status_code=400)
            try:
                htu = request.url
                dpop_jkt = self._dpop_validator.verify(request.method, htu, dpop_proof, token)
            except DPoPReplayError:
                raise DeviceAuthError("dpop_replayed", status_code=400)
            except DPoPVerificationError:
                raise DeviceAuthError("dpop_invalid", status_code=400)

        ctx = DeviceAuthContext(
            guid=guid,
            ssl_key_fingerprint=fingerprint,
            token_version=token_version,
            access_token=token,
            claims=claims,
            dpop_jkt=dpop_jkt,
            status=status_normalized,
        )
        return ctx


def require_device_auth(manager: DeviceAuthManager):
    def decorator(func):
        @functools.wraps(func)
        def wrapper(*args, **kwargs):
            try:
                ctx = manager.authenticate()
            except DeviceAuthError as exc:
                response = jsonify({"error": exc.message})
                response.status_code = exc.status_code
                return response

            g.device_auth = ctx
            return func(*args, **kwargs)

        return wrapper

    return decorator
