# ======================================================
# Data\Engine\services\API\devices\approval.py
# Description: Administrative device enrollment endpoints covering code lifecycle and approval queue actions.
#
# API Endpoints (if applicable):
# - GET /api/admin/enrollment-codes (Token Authenticated (Admin)) - Lists static site enrollment codes.
# - POST /api/admin/enrollment-codes (Token Authenticated (Admin)) - Deprecated (returns 410; use site APIs).
# - DELETE /api/admin/enrollment-codes/<code_id> (Token Authenticated (Admin)) - Deprecated (returns 410; use site APIs).
# - GET /api/admin/device-approvals (Token Authenticated) - Enumerates pending/historical approvals and admin wrong-code sightings.
# - POST /api/admin/device-approvals/<approval_id>/approve (Token Authenticated (Admin)) - Approves a pending device and handles hostname conflicts.
# - POST /api/admin/device-approvals/<approval_id>/deny (Token Authenticated (Admin)) - Denies a pending device approval request.
# ======================================================

"""Admin-focused device enrollment and approval endpoints."""
from __future__ import annotations

import os
from Data.Engine.db import dbapi as sqlite3
from datetime import datetime, timedelta, timezone
from typing import TYPE_CHECKING, Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....auth.guid_utils import normalize_guid
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters


def _now() -> datetime:
    return datetime.now(tz=timezone.utc)


def _iso(dt: datetime) -> str:
    return dt.isoformat()


class AdminDeviceService:
    """Utility wrapper for admin device APIs."""

    def __init__(self, app, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.service_log = adapters.service_log
        self.logger = adapters.context.logger
        self.site_access = UserSiteAccessManager(self.db_conn_factory, logger=self.logger)

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
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

    def require_user(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
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
            cur.execute(
                """
                SELECT id, name, enrollment_code, created_at
                  FROM sites
                 WHERE COALESCE(TRIM(enrollment_code), '') != ''
                 ORDER BY LOWER(name) ASC
                """
            )
            rows = cur.fetchall()
        finally:
            conn.close()

        records = []
        for row in rows:
            records.append(
                {
                    "id": f"site:{row[0]}",
                    "site_id": row[0],
                    "site_name": row[1],
                    "code": row[2],
                    "created_at": row[3],
                }
            )
        return {"codes": records}, 200

    def create_enrollment_code(self, ttl_hours: int, max_uses: int) -> Tuple[Dict[str, Any], int]:
        return {"error": "legacy_endpoint_removed_use_sites_api"}, 410

    def delete_enrollment_code(self, code_id: str) -> Tuple[Dict[str, Any], int]:
        return {"error": "legacy_endpoint_removed_use_sites_api"}, 410

    # ------------------------------------------------------------------ #
    # Device approval helpers
    # ------------------------------------------------------------------ #

    def list_device_approvals(self, status_filter: Optional[str]) -> Tuple[Dict[str, Any], int]:
        approvals: List[Dict[str, Any]] = []
        current_user = self._current_user() or {}
        status_norm = (status_filter or "").strip().lower()
        if status_norm == "wrong_code":
            if (current_user.get("role") or "").strip().lower() != "admin":
                return {"approvals": []}, 200
            return self.list_wrong_code_attempts()

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
                    da.enrollment_code,
                    da.site_id,
                    da.status,
                    da.client_nonce,
                    da.server_nonce,
                    da.created_at,
                    da.updated_at,
                    da.approved_by_user_id,
                    u.username AS approved_by_username,
                    s.name AS site_name,
                    da.onboarding_job_id,
                    da.onboarding_run_id,
                    da.onboarding_target
                  FROM device_approvals AS da
             LEFT JOIN users AS u
                    ON (
                        CAST(da.approved_by_user_id AS TEXT) = CAST(u.id AS TEXT)
                        OR LOWER(da.approved_by_user_id) = LOWER(u.username)
                    )
             LEFT JOIN sites AS s
                    ON s.id = da.site_id
            """
            if status_norm and status_norm != "all":
                sql += " WHERE LOWER(da.status) = ?"
                params.append(status_norm)
            sql += " ORDER BY da.created_at ASC"
            cur.execute(sql, params)
            rows = cur.fetchall()
            for row in rows:
                if not self.site_access.user_can_access_site(current_user, row[6]):
                    continue
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
                        "enrollment_code": row[5],
                        "site_id": row[6],
                        "status": row[7],
                        "client_nonce": row[8],
                        "server_nonce": row[9],
                        "created_at": row[10],
                        "updated_at": row[11],
                        "approved_by_user_id": row[12],
                        "hostname_conflict": conflict,
                        "alternate_hostname": alternate,
                        "conflict_requires_prompt": requires_prompt,
                        "fingerprint_match": fingerprint_match,
                        "approved_by_username": row[13],
                        "site_name": row[14],
                        "onboarding_job_id": row[15],
                        "onboarding_run_id": row[16],
                        "onboarding_target": row[17] or "",
                    }
                )
        finally:
            conn.close()
        return {"approvals": approvals}, 200

    def list_wrong_code_attempts(self, window_seconds: int = 300) -> Tuple[Dict[str, Any], int]:
        cutoff = _iso(_now() - timedelta(seconds=max(60, int(window_seconds or 300))))
        rows = []
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    id,
                    hostname_claimed,
                    ssl_key_fingerprint_claimed,
                    enrollment_code_mask,
                    remote_addr,
                    first_seen_at,
                    last_seen_at,
                    attempt_count,
                    last_error
                  FROM enrollment_code_failures
                 WHERE last_seen_at >= ?
                 ORDER BY last_seen_at DESC
                """,
                (cutoff,),
            )
            rows = cur.fetchall()
        finally:
            conn.close()

        approvals = []
        for row in rows:
            approvals.append(
                {
                    "id": row[0],
                    "approval_reference": None,
                    "guid": None,
                    "hostname_claimed": row[1],
                    "ssl_key_fingerprint_claimed": row[2],
                    "enrollment_code": row[3],
                    "site_id": None,
                    "status": "wrong_code",
                    "client_nonce": None,
                    "server_nonce": None,
                    "created_at": row[5],
                    "updated_at": row[6],
                    "approved_by_user_id": None,
                    "hostname_conflict": None,
                    "alternate_hostname": None,
                    "conflict_requires_prompt": False,
                    "fingerprint_match": False,
                    "approved_by_username": None,
                    "site_name": None,
                    "remote_addr": row[4],
                    "first_seen_at": row[5],
                    "last_seen_at": row[6],
                    "wrong_code_attempt_count": row[7],
                    "last_error": row[8],
                }
            )
        return {"approvals": approvals}, 200

    def _approval_accessible(
        self,
        cur: sqlite3.Cursor,
        user: Optional[Dict[str, Any]],
        approval_id: str,
    ) -> bool:
        cur.execute(
            "SELECT site_id FROM device_approvals WHERE id = ?",
            (approval_id,),
        )
        row = cur.fetchone()
        if not row:
            return False
        return self.site_access.user_can_access_site(user or {}, row[0])

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
            if not self._approval_accessible(cur, user, approval_id):
                return {"error": "not found"}, 404
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


def register_admin_endpoints(app, adapters: "EngineServiceAdapters") -> None:
    """Register admin enrollment + approval endpoints."""

    service = AdminDeviceService(app, adapters)
    blueprint = Blueprint("device_admin", __name__)

    @blueprint.route("/api/admin/enrollment-codes", methods=["GET"])
    def _admin_enrollment_codes():
        requirement = service.require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.list_enrollment_codes(request.args.get("status"))
        return jsonify(payload), status

    @blueprint.route("/api/admin/enrollment-codes", methods=["POST"])
    def _admin_create_enrollment_code():
        requirement = service.require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.create_enrollment_code(0, 0)
        return jsonify(payload), status

    @blueprint.route("/api/admin/enrollment-codes/<code_id>", methods=["DELETE"])
    def _admin_delete_enrollment_code(code_id: str):
        requirement = service.require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.delete_enrollment_code(code_id)
        return jsonify(payload), status

    @blueprint.route("/api/admin/device-approvals", methods=["GET"])
    def _admin_list_device_approvals():
        requirement = service.require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.list_device_approvals(request.args.get("status"))
        return jsonify(payload), status

    @blueprint.route("/api/admin/device-approvals/<approval_id>/approve", methods=["POST"])
    def _admin_approve_device(approval_id: str):
        requirement = service.require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(force=True, silent=True) or {}
        payload, status = service.approve_device(approval_id, data)
        return jsonify(payload), status

    @blueprint.route("/api/admin/device-approvals/<approval_id>/deny", methods=["POST"])
    def _admin_deny_device(approval_id: str):
        requirement = service.require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload, status = service.deny_device(approval_id)
        return jsonify(payload), status

    app.register_blueprint(blueprint)
