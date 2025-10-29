# ======================================================
# Data\Engine\services\API\devices\approval.py
# Description: Administrative device enrollment endpoints covering code lifecycle and approval queue actions.
#
# API Endpoints (if applicable):
# - GET /api/admin/enrollment-codes (Token Authenticated (Admin)) - Lists enrollment/install codes with optional status filtering.
# - POST /api/admin/enrollment-codes (Token Authenticated (Admin)) - Creates a new enrollment/install code with TTL and usage limits.
# - DELETE /api/admin/enrollment-codes/<code_id> (Token Authenticated (Admin)) - Deletes an enrollment/install code record.
# - GET /api/admin/device-approvals (Token Authenticated (Admin)) - Enumerates pending and historical device approval records.
# - POST /api/admin/device-approvals/<approval_id>/approve (Token Authenticated (Admin)) - Approves a pending device and handles hostname conflicts.
# - POST /api/admin/device-approvals/<approval_id>/deny (Token Authenticated (Admin)) - Denies a pending device approval request.
# ======================================================

"""Admin-focused device enrollment and approval endpoints."""
from __future__ import annotations

import os
import secrets
import sqlite3
import uuid
from datetime import datetime, timedelta, timezone
from typing import TYPE_CHECKING, Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from Modules.guid_utils import normalize_guid

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import LegacyServiceAdapters


VALID_TTL_HOURS = {1, 3, 6, 12, 24}


def _now() -> datetime:
    return datetime.now(tz=timezone.utc)


def _iso(dt: datetime) -> str:
    return dt.isoformat()


def _generate_install_code() -> str:
    raw = secrets.token_hex(16).upper()
    return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))


class AdminDeviceService:
    """Utility wrapper for admin device APIs."""

    def __init__(self, app, adapters: "LegacyServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = self.app.secret_key or "borealis-dev-secret"
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, Any]]:
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

    def require_admin(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if (user.get("role") or "").lower() != "admin":
            return {"error": "forbidden"}, 403
        return None

    def _lookup_user_id(self, cur: sqlite3.Cursor, username: str) -> Optional[str]:
        if not username:
            return None
        cur.execute(
            "SELECT id FROM users WHERE LOWER(username) = LOWER(?)",
            (username,),
        )
        row = cur.fetchone()
        if row:
            return str(row[0])
        return None

    def _hostname_conflict(
        self,
        cur: sqlite3.Cursor,
        hostname: Optional[str],
        pending_guid: Optional[str],
    ) -> Optional[Dict[str, Any]]:
        if not hostname:
            return None
        cur.execute(
            """
            SELECT d.guid, d.ssl_key_fingerprint, ds.site_id, s.name
              FROM devices d
         LEFT JOIN device_sites ds ON ds.device_hostname = d.hostname
         LEFT JOIN sites s ON s.id = ds.site_id
             WHERE d.hostname = ?
            """,
            (hostname,),
        )
        row = cur.fetchone()
        if not row:
            return None
        existing_guid = normalize_guid(row[0])
        existing_fingerprint = (row[1] or "").strip().lower()
        pending_norm = normalize_guid(pending_guid)
        if existing_guid and pending_norm and existing_guid == pending_norm:
            return None
        site_id_raw = row[2]
        try:
            site_id = int(site_id_raw) if site_id_raw is not None else None
        except Exception:
            site_id = None
        site_name = row[3] or ""
        return {
            "guid": existing_guid or None,
            "ssl_key_fingerprint": existing_fingerprint or None,
            "site_id": site_id,
            "site_name": site_name,
        }

    def _suggest_alternate_hostname(
        self,
        cur: sqlite3.Cursor,
        hostname: Optional[str],
        pending_guid: Optional[str],
    ) -> Optional[str]:
        base = (hostname or "").strip()
        if not base:
            return None
        base = base[:253]
        candidate = base
        pending_norm = normalize_guid(pending_guid)
        suffix = 1
        while True:
            cur.execute(
                "SELECT guid FROM devices WHERE hostname = ?",
                (candidate,),
            )
            row = cur.fetchone()
            if not row:
                return candidate
            existing_guid = normalize_guid(row[0])
            if pending_norm and existing_guid == pending_norm:
                return candidate
            candidate = f"{base}-{suffix}"
            suffix += 1
            if suffix > 50:
                return pending_norm or candidate

    # ------------------------------------------------------------------ #
    # Enrollment code management
    # ------------------------------------------------------------------ #

    def list_enrollment_codes(self, status_filter: Optional[str]) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            sql = """
                SELECT id,
                       code,
                       expires_at,
                       created_by_user_id,
                       used_at,
                       used_by_guid,
                       max_uses,
                       use_count,
                       last_used_at
                  FROM enrollment_install_codes
            """
            params: List[str] = []
            now_iso = _iso(_now())
            if status_filter == "active":
                sql += " WHERE use_count < max_uses AND expires_at > ?"
                params.append(now_iso)
            elif status_filter == "expired":
                sql += " WHERE use_count < max_uses AND expires_at <= ?"
                params.append(now_iso)
            elif status_filter == "used":
                sql += " WHERE use_count >= max_uses"
            sql += " ORDER BY expires_at ASC"
            cur.execute(sql, params)
            rows = cur.fetchall()
        finally:
            conn.close()

        records = []
        for row in rows:
            records.append(
                {
                    "id": row[0],
                    "code": row[1],
                    "expires_at": row[2],
                    "created_by_user_id": row[3],
                    "used_at": row[4],
                    "used_by_guid": row[5],
                    "max_uses": row[6],
                    "use_count": row[7],
                    "last_used_at": row[8],
                }
            )
        return {"codes": records}, 200

    def create_enrollment_code(self, ttl_hours: int, max_uses: int) -> Tuple[Dict[str, Any], int]:
        if ttl_hours not in VALID_TTL_HOURS:
            return {"error": "invalid_ttl"}, 400
        max_uses = max(1, min(int(max_uses or 1), 10))

        user = self._current_user() or {}
        username = user.get("username") or ""

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            created_by = self._lookup_user_id(cur, username) or username or "system"
            code_value = _generate_install_code()
            expires_at = _now() + timedelta(hours=ttl_hours)
            record_id = str(uuid.uuid4())
            cur.execute(
                """
                INSERT INTO enrollment_install_codes (
                    id, code, expires_at, created_by_user_id, max_uses, use_count
                )
                VALUES (?, ?, ?, ?, ?, 0)
                """,
                (record_id, code_value, _iso(expires_at), created_by, max_uses),
            )
            conn.commit()
        finally:
            conn.close()

        self.service_log(
            "server",
            f"installer code created id={record_id} by={username} ttl={ttl_hours}h max_uses={max_uses}",
        )
        return (
            {
                "id": record_id,
                "code": code_value,
                "expires_at": _iso(expires_at),
                "max_uses": max_uses,
                "use_count": 0,
                "last_used_at": None,
            },
            201,
        )

    def delete_enrollment_code(self, code_id: str) -> Tuple[Dict[str, Any], int]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "DELETE FROM enrollment_install_codes WHERE id = ? AND use_count = 0",
                (code_id,),
            )
            deleted = cur.rowcount
            conn.commit()
        finally:
            conn.close()

        if not deleted:
            return {"error": "not_found"}, 404
        self.service_log("server", f"installer code deleted id={code_id}")
        return {"status": "deleted"}, 200

    # ------------------------------------------------------------------ #
    # Device approval helpers
    # ------------------------------------------------------------------ #

    def list_device_approvals(self, status_filter: Optional[str]) -> Tuple[Dict[str, Any], int]:
        approvals: List[Dict[str, Any]] = []
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            params: List[str] = []
            sql = """
                SELECT
                    da.id,
                    da.approval_reference,
                    da.guid,
                    da.hostname_claimed,
                    da.ssl_key_fingerprint_claimed,
                    da.enrollment_code_id,
                    da.status,
                    da.client_nonce,
                    da.server_nonce,
                    da.created_at,
                    da.updated_at,
                    da.approved_by_user_id,
                    u.username AS approved_by_username
                  FROM device_approvals AS da
             LEFT JOIN users AS u
                    ON (
                        CAST(da.approved_by_user_id AS TEXT) = CAST(u.id AS TEXT)
                        OR LOWER(da.approved_by_user_id) = LOWER(u.username)
                    )
            """
            status_norm = (status_filter or "").strip().lower()
            if status_norm and status_norm != "all":
                sql += " WHERE LOWER(da.status) = ?"
                params.append(status_norm)
            sql += " ORDER BY da.created_at ASC"
            cur.execute(sql, params)
            rows = cur.fetchall()
            for row in rows:
                record_guid = row[2]
                hostname = row[3]
                fingerprint_claimed = row[4]
                claimed_fp_norm = (fingerprint_claimed or "").strip().lower()
                conflict_raw = self._hostname_conflict(cur, hostname, record_guid)
                fingerprint_match = False
                requires_prompt = False
                conflict = None
                if conflict_raw:
                    conflict_fp = (conflict_raw.get("ssl_key_fingerprint") or "").strip().lower()
                    fingerprint_match = bool(conflict_fp and claimed_fp_norm) and conflict_fp == claimed_fp_norm
                    requires_prompt = not fingerprint_match
                    conflict = {
                        **conflict_raw,
                        "fingerprint_match": fingerprint_match,
                        "requires_prompt": requires_prompt,
                    }
                alternate = (
                    self._suggest_alternate_hostname(cur, hostname, record_guid)
                    if conflict_raw and requires_prompt
                    else None
                )
                approvals.append(
                    {
                        "id": row[0],
                        "approval_reference": row[1],
                        "guid": record_guid,
                        "hostname_claimed": hostname,
                        "ssl_key_fingerprint_claimed": fingerprint_claimed,
                        "enrollment_code_id": row[5],
                        "status": row[6],
                        "client_nonce": row[7],
                        "server_nonce": row[8],
                        "created_at": row[9],
                        "updated_at": row[10],
                        "approved_by_user_id": row[11],
                        "hostname_conflict": conflict,
                        "alternate_hostname": alternate,
                        "conflict_requires_prompt": requires_prompt,
                        "fingerprint_match": fingerprint_match,
                        "approved_by_username": row[12],
                    }
                )
        finally:
            conn.close()
        return {"approvals": approvals}, 200

    def _set_approval_status(
        self,
        approval_id: str,
        status: str,
        *,
        guid: Optional[str] = None,
        resolution: Optional[str] = None,
    ) -> Tuple[Dict[str, Any], int]:
        user = self._current_user() or {}
        username = user.get("username") or ""

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT status,
                       guid,
                       hostname_claimed,
                       ssl_key_fingerprint_claimed
                  FROM device_approvals
                 WHERE id = ?
                """,
                (approval_id,),
            )
            row = cur.fetchone()
            if not row:
                return {"error": "not_found"}, 404
            existing_status = (row[0] or "").strip().lower()
            if existing_status != "pending":
                return {"error": "approval_not_pending"}, 409
            stored_guid = row[1]
            hostname_claimed = row[2]
            fingerprint_claimed = (row[3] or "").strip().lower()

            guid_effective = normalize_guid(guid) if guid else normalize_guid(stored_guid)
            resolution_effective = (resolution.strip().lower() if isinstance(resolution, str) else None)

            if status == "approved":
                conflict = self._hostname_conflict(cur, hostname_claimed, guid_effective)
                if conflict:
                    conflict_fp = (conflict.get("ssl_key_fingerprint") or "").strip().lower()
                    fingerprint_match = bool(conflict_fp and fingerprint_claimed) and conflict_fp == fingerprint_claimed
                    if fingerprint_match:
                        guid_effective = conflict.get("guid") or guid_effective
                        if not resolution_effective:
                            resolution_effective = "auto_merge_fingerprint"
                    elif resolution_effective == "overwrite":
                        guid_effective = conflict.get("guid") or guid_effective
                    elif resolution_effective == "coexist":
                        pass
                    else:
                        return {
                            "error": "conflict_resolution_required",
                            "hostname": hostname_claimed,
                        }, 409

            guid_to_store = guid_effective or normalize_guid(stored_guid) or None
            approved_by = self._lookup_user_id(cur, username) or username or "system"
            cur.execute(
                """
                UPDATE device_approvals
                   SET status = ?,
                       guid = ?,
                       approved_by_user_id = ?,
                       updated_at = ?
                 WHERE id = ?
                """,
                (
                    status,
                    guid_to_store,
                    approved_by,
                    _iso(_now()),
                    approval_id,
                ),
            )
            conn.commit()
        finally:
            conn.close()

        resolution_note = f" ({resolution_effective})" if resolution_effective else ""
        self.service_log("server", f"device approval {approval_id} -> {status}{resolution_note} by {username}")
        payload: Dict[str, Any] = {"status": status}
        if resolution_effective:
            payload["conflict_resolution"] = resolution_effective
        return payload, 200

    def approve_device(self, approval_id: str, payload: Dict[str, Any]) -> Tuple[Dict[str, Any], int]:
        guid = (payload.get("guid") or "").strip() or None
        resolution_raw = payload.get("conflict_resolution")
        resolution = resolution_raw.strip() if isinstance(resolution_raw, str) else None
        return self._set_approval_status(approval_id, "approved", guid=guid, resolution=resolution)

    def deny_device(self, approval_id: str) -> Tuple[Dict[str, Any], int]:
        return self._set_approval_status(approval_id, "denied")


def register_admin_endpoints(app, adapters: "LegacyServiceAdapters") -> None:
    """Register admin enrollment + approval endpoints."""

    service = AdminDeviceService(app, adapters)
    blueprint = Blueprint("device_admin", __name__)

    @blueprint.before_request
    def _ensure_admin():
        requirement = service.require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        return None

    @blueprint.route("/api/admin/enrollment-codes", methods=["GET"])
    def _admin_enrollment_codes():
        payload, status = service.list_enrollment_codes(request.args.get("status"))
        return jsonify(payload), status

    @blueprint.route("/api/admin/enrollment-codes", methods=["POST"])
    def _admin_create_enrollment_code():
        data = request.get_json(force=True, silent=True) or {}
        ttl_hours = int(data.get("ttl_hours") or 1)
        max_uses_value = data.get("max_uses")
        if max_uses_value is None:
            max_uses_value = data.get("allowed_uses")
        try:
            max_uses = int(max_uses_value) if max_uses_value is not None else 2
        except Exception:
            max_uses = 2
        payload, status = service.create_enrollment_code(ttl_hours, max_uses)
        return jsonify(payload), status

    @blueprint.route("/api/admin/enrollment-codes/<code_id>", methods=["DELETE"])
    def _admin_delete_enrollment_code(code_id: str):
        payload, status = service.delete_enrollment_code(code_id)
        return jsonify(payload), status

    @blueprint.route("/api/admin/device-approvals", methods=["GET"])
    def _admin_list_device_approvals():
        payload, status = service.list_device_approvals(request.args.get("status"))
        return jsonify(payload), status

    @blueprint.route("/api/admin/device-approvals/<approval_id>/approve", methods=["POST"])
    def _admin_approve_device(approval_id: str):
        data = request.get_json(force=True, silent=True) or {}
        payload, status = service.approve_device(approval_id, data)
        return jsonify(payload), status

    @blueprint.route("/api/admin/device-approvals/<approval_id>/deny", methods=["POST"])
    def _admin_deny_device(approval_id: str):
        payload, status = service.deny_device(approval_id)
        return jsonify(payload), status

    app.register_blueprint(blueprint)
