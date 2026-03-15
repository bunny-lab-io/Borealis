# ======================================================
# Data\Engine\auth\device_auth.py
# Description: Engine-native device authentication manager and decorators.
#
# API Endpoints (if applicable): None
# ======================================================

"""Device authentication helpers for the Borealis Engine runtime."""

from __future__ import annotations

import functools
from Data.Engine.db import dbapi as sqlite3
import time
from contextlib import closing
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Callable, Dict, Optional

import jwt
from flask import g, jsonify, request

from .dpop import DPoPReplayError, DPoPValidator, DPoPVerificationError
from .guid_utils import normalize_guid
from .rate_limit import SlidingWindowRateLimiter

AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


def _canonical_context(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    cleaned = "".join(ch for ch in str(value) if ch.isalnum() or ch in ("_", "-"))
    if not cleaned:
        return None
    return cleaned.upper()


@dataclass
class DeviceAuthContext:
    guid: str
    ssl_key_fingerprint: str
    token_version: int
    access_token: str
    claims: Dict[str, Any]
    dpop_jkt: Optional[str]
    status: str
    service_mode: Optional[str]


class DeviceAuthError(Exception):
    status_code = 401
    error_code = "unauthorized"

    def __init__(
        self,
        message: str = "unauthorized",
        *,
        status_code: Optional[int] = None,
        retry_after: Optional[float] = None,
    ):
        super().__init__(message)
        if status_code is not None:
            self.status_code = status_code
        self.message = message
        self.retry_after = retry_after


class DeviceAuthManager:
    def __init__(
        self,
        *,
        db_conn_factory: Callable[[], Any],
        jwt_service,
        dpop_validator: Optional[DPoPValidator],
        log: Callable[[str, str, Optional[str]], None],
        rate_limiter: Optional[SlidingWindowRateLimiter] = None,
    ) -> None:
        self._db_conn_factory = db_conn_factory
        self._jwt_service = jwt_service
        self._dpop_validator = dpop_validator
        self._log = log
        self._rate_limiter = rate_limiter

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

        raw_guid = str(claims.get("guid") or "").strip()
        guid = normalize_guid(raw_guid)
        fingerprint = str(claims.get("ssl_key_fingerprint") or "").lower().strip()
        token_version = int(claims.get("token_version") or 0)
        if not guid or not fingerprint or token_version <= 0:
            raise DeviceAuthError("invalid_claims")

        if self._rate_limiter:
            decision = self._rate_limiter.check(f"fp:{fingerprint}", 60, 60.0)
            if not decision.allowed:
                raise DeviceAuthError(
                    "rate_limited",
                    status_code=429,
                    retry_after=decision.retry_after,
                )

        context_label = _canonical_context(request.headers.get(AGENT_CONTEXT_HEADER))

        with closing(self._db_conn_factory()) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT guid, ssl_key_fingerprint, token_version, status
                  FROM devices
                 WHERE UPPER(guid) = ?
                """,
                (guid,),
            )
            rows = cur.fetchall()
            row = None
            for candidate in rows or []:
                candidate_guid = normalize_guid(candidate[0])
                if candidate_guid == guid:
                    row = candidate
                    break
            if row is None and rows:
                row = rows[0]

            if row is None:
                row = self._recover_device_record(conn, guid, fingerprint, token_version, context_label)

            if row is None:
                raise DeviceAuthError("device_not_found", status_code=404)

            stored_guid, stored_fingerprint, stored_version, status = row
            stored_guid = normalize_guid(stored_guid)
            if stored_guid != guid:
                raise DeviceAuthError("device_mismatch", status_code=401)
            if (stored_fingerprint or "").lower().strip() != fingerprint:
                raise DeviceAuthError("fingerprint_mismatch", status_code=403)
            if int(stored_version or 0) != token_version:
                raise DeviceAuthError("token_version_mismatch", status_code=403)

            status_norm = (status or "active").strip().lower()
            if status_norm in {"revoked", "decommissioned"}:
                raise DeviceAuthError("device_revoked", status_code=403)

            dpop_proof = request.headers.get("DPoP")
            jkt = None
            if dpop_proof and self._dpop_validator:
                try:
                    jkt = self._dpop_validator.verify(request.method, request.url, dpop_proof, access_token=token)
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
                dpop_jkt=jkt,
                status=status_norm,
                service_mode=context_label,
            )
        return ctx

    def _recover_device_record(
        self,
        conn: sqlite3.Connection,
        guid: str,
        fingerprint: str,
        token_version: int,
        context_label: Optional[str],
    ) -> Optional[tuple]:
        """Attempt to recreate a missing device row for an authenticated token."""

        guid = normalize_guid(guid)
        fingerprint = (fingerprint or "").strip()
        if not guid or not fingerprint:
            return None

        cur = conn.cursor()
        now_ts = int(time.time())
        try:
            now_iso = datetime.now(tz=timezone.utc).isoformat()
        except Exception:
            now_iso = datetime.utcnow().isoformat()

        base_hostname = f"RECOVERED-{guid[:12].upper()}" if guid else "RECOVERED"

        for attempt in range(6):
            hostname = base_hostname if attempt == 0 else f"{base_hostname}-{attempt}"
            try:
                cur.execute(
                    """
                    INSERT INTO devices (
                        guid,
                        hostname,
                        created_at,
                        last_seen,
                        ssl_key_fingerprint,
                        token_version,
                        status,
                        key_added_at
                    )
                    VALUES (?, ?, ?, ?, ?, ?, 'active', ?)
                    """,
                    (
                        guid,
                        hostname,
                        now_ts,
                        now_ts,
                        fingerprint,
                        max(token_version or 1, 1),
                        now_iso,
                    ),
                )
            except sqlite3.IntegrityError as exc:
                message = str(exc).lower()
                if "hostname" in message and "unique" in message:
                    continue
                self._log(
                    "server",
                    f"device auth failed to recover guid={guid} due to integrity error: {exc}",
                    context_label,
                )
                conn.rollback()
                return None
            except Exception as exc:
                self._log(
                    "server",
                    f"device auth unexpected error recovering guid={guid}: {exc}",
                    context_label,
                )
                conn.rollback()
                return None
            else:
                conn.commit()
                break
        else:
            self._log(
                "server",
                f"device auth could not recover guid={guid}; hostname collisions persisted",
                context_label,
            )
            conn.rollback()
            return None

        cur.execute(
            """
            SELECT guid, ssl_key_fingerprint, token_version, status
              FROM devices
             WHERE guid = ?
            """,
            (guid,),
        )
        row = cur.fetchone()
        if not row:
            self._log(
                "server",
                f"device auth recovery for guid={guid} committed but row still missing",
                context_label,
            )
        return row


def require_device_auth(manager: DeviceAuthManager):
    def decorator(func):
        @functools.wraps(func)
        def wrapper(*args, **kwargs):
            try:
                ctx = manager.authenticate()
            except DeviceAuthError as exc:
                response = jsonify({"error": exc.message})
                response.status_code = exc.status_code
                retry_after = getattr(exc, "retry_after", None)
                if retry_after:
                    try:
                        response.headers["Retry-After"] = str(max(1, int(retry_after)))
                    except Exception:
                        response.headers["Retry-After"] = "1"
                return response

            g.device_auth = ctx
            return func(*args, **kwargs)

        return wrapper

    return decorator


__all__ = [
    "AGENT_CONTEXT_HEADER",
    "DeviceAuthContext",
    "DeviceAuthError",
    "DeviceAuthManager",
    "require_device_auth",
]
