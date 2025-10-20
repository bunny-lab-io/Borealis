from __future__ import annotations

import secrets
import sqlite3
import uuid
from datetime import datetime, timedelta, timezone
from typing import Any, Callable, Dict, List, Optional

from flask import Blueprint, jsonify, request

from Modules.guid_utils import normalize_guid


VALID_TTL_HOURS = {1, 3, 6, 12, 24}


def register(
    app,
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    require_admin: Callable[[], Optional[Any]],
    current_user: Callable[[], Optional[Dict[str, str]]],
    log: Callable[[str, str, Optional[str]], None],
) -> None:
    blueprint = Blueprint("admin", __name__)

    def _now() -> datetime:
        return datetime.now(tz=timezone.utc)

    def _iso(dt: datetime) -> str:
        return dt.isoformat()

    def _lookup_user_id(cur: sqlite3.Cursor, username: str) -> Optional[str]:
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
        site_id = None
        if site_id_raw is not None:
            try:
                site_id = int(site_id_raw)
            except (TypeError, ValueError):
                site_id = None
        site_name = row[3] or ""
        return {
            "guid": existing_guid or None,
            "ssl_key_fingerprint": existing_fingerprint or None,
            "site_id": site_id,
            "site_name": site_name,
        }

    def _suggest_alternate_hostname(
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

    @blueprint.before_request
    def _check_admin():
        result = require_admin()
        if result is not None:
            return result
        return None

    @blueprint.route("/api/admin/enrollment-codes", methods=["GET"])
    def list_enrollment_codes():
        status_filter = request.args.get("status")
        conn = db_conn_factory()
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
        return jsonify({"codes": records})

    @blueprint.route("/api/admin/enrollment-codes", methods=["POST"])
    def create_enrollment_code():
        payload = request.get_json(force=True, silent=True) or {}
        ttl_hours = int(payload.get("ttl_hours") or 1)
        if ttl_hours not in VALID_TTL_HOURS:
            return jsonify({"error": "invalid_ttl"}), 400

        max_uses_value = payload.get("max_uses")
        if max_uses_value is None:
            max_uses_value = payload.get("allowed_uses")
        try:
            max_uses = int(max_uses_value)
        except Exception:
            max_uses = 2
        if max_uses < 1:
            max_uses = 1
        if max_uses > 10:
            max_uses = 10

        user = current_user() or {}
        username = user.get("username") or ""

        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            created_by = _lookup_user_id(cur, username) or username or "system"
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

        log(
            "server",
            f"installer code created id={record_id} by={username} ttl={ttl_hours}h max_uses={max_uses}",
        )
        return jsonify(
            {
                "id": record_id,
                "code": code_value,
                "expires_at": _iso(expires_at),
                "max_uses": max_uses,
                "use_count": 0,
                "last_used_at": None,
            }
        )

    @blueprint.route("/api/admin/enrollment-codes/<code_id>", methods=["DELETE"])
    def delete_enrollment_code(code_id: str):
        conn = db_conn_factory()
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
            return jsonify({"error": "not_found"}), 404
        log("server", f"installer code deleted id={code_id}")
        return jsonify({"status": "deleted"})

    @blueprint.route("/api/admin/device-approvals", methods=["GET"])
    def list_device_approvals():
        status_raw = request.args.get("status")
        status = (status_raw or "").strip().lower()
        approvals: List[Dict[str, Any]] = []
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            params: List[str] = []
            sql = """
                SELECT
                    id,
                    approval_reference,
                    guid,
                    hostname_claimed,
                    ssl_key_fingerprint_claimed,
                    enrollment_code_id,
                    status,
                    client_nonce,
                    server_nonce,
                    created_at,
                    updated_at,
                    approved_by_user_id
                  FROM device_approvals
            """
            if status and status != "all":
                sql += " WHERE LOWER(status) = ?"
                params.append(status)
            sql += " ORDER BY created_at ASC"
            cur.execute(sql, params)
            rows = cur.fetchall()
            for row in rows:
                record_guid = row[2]
                hostname = row[3]
                fingerprint_claimed = row[4]
                claimed_fp_norm = (fingerprint_claimed or "").strip().lower()
                conflict_raw = _hostname_conflict(cur, hostname, record_guid)
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
                alternate_hostname = (
                    _suggest_alternate_hostname(cur, hostname, record_guid)
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
                        "alternate_hostname": alternate_hostname,
                        "conflict_requires_prompt": requires_prompt,
                        "fingerprint_match": fingerprint_match,
                    }
                )
        finally:
            conn.close()

        return jsonify({"approvals": approvals})

    def _set_approval_status(
        approval_id: str,
        status: str,
        *,
        guid: Optional[str] = None,
        resolution: Optional[str] = None,
    ):
        user = current_user() or {}
        username = user.get("username") or ""

        conn = db_conn_factory()
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

            conflict = None
            if status == "approved":
                conflict = _hostname_conflict(cur, hostname_claimed, guid_effective)
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

            approved_by = _lookup_user_id(cur, username) or username or "system"
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
        log("server", f"device approval {approval_id} -> {status}{resolution_note} by {username}")
        payload: Dict[str, Any] = {"status": status}
        if resolution_effective:
            payload["conflict_resolution"] = resolution_effective
        return payload, 200

    @blueprint.route("/api/admin/device-approvals/<approval_id>/approve", methods=["POST"])
    def approve_device(approval_id: str):
        payload = request.get_json(force=True, silent=True) or {}
        guid = payload.get("guid")
        if guid:
            guid = str(guid).strip()
        resolution_val = payload.get("conflict_resolution")
        resolution = None
        if isinstance(resolution_val, str):
            cleaned = resolution_val.strip().lower()
            if cleaned:
                resolution = cleaned
        result, status_code = _set_approval_status(
            approval_id,
            "approved",
            guid=guid,
            resolution=resolution,
        )
        return jsonify(result), status_code

    @blueprint.route("/api/admin/device-approvals/<approval_id>/deny", methods=["POST"])
    def deny_device(approval_id: str):
        result, status_code = _set_approval_status(approval_id, "denied")
        return jsonify(result), status_code

    app.register_blueprint(blueprint)


def _generate_install_code() -> str:
    raw = secrets.token_hex(16).upper()
    return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))
