from __future__ import annotations

import secrets
import sqlite3
import uuid
from datetime import datetime, timedelta, timezone
from typing import Any, Callable, Dict, List, Optional

from flask import Blueprint, jsonify, request


VALID_TTL_HOURS = {1, 3, 6, 12, 24}


def register(
    app,
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    require_admin: Callable[[], Optional[Any]],
    current_user: Callable[[], Optional[Dict[str, str]]],
    log: Callable[[str, str], None],
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
                SELECT id, code, expires_at, created_by_user_id, used_at, used_by_guid
                  FROM enrollment_install_codes
            """
            params: List[str] = []
            if status_filter == "active":
                sql += " WHERE used_at IS NULL AND expires_at > ?"
                params.append(_iso(_now()))
            elif status_filter == "expired":
                sql += " WHERE used_at IS NULL AND expires_at <= ?"
                params.append(_iso(_now()))
            elif status_filter == "used":
                sql += " WHERE used_at IS NOT NULL"
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
                }
            )
        return jsonify({"codes": records})

    @blueprint.route("/api/admin/enrollment-codes", methods=["POST"])
    def create_enrollment_code():
        payload = request.get_json(force=True, silent=True) or {}
        ttl_hours = int(payload.get("ttl_hours") or 1)
        if ttl_hours not in VALID_TTL_HOURS:
            return jsonify({"error": "invalid_ttl"}), 400

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
                    id, code, expires_at, created_by_user_id
                )
                VALUES (?, ?, ?, ?)
                """,
                (record_id, code_value, _iso(expires_at), created_by),
            )
            conn.commit()
        finally:
            conn.close()

        log("server", f"installer code created id={record_id} by={username} ttl={ttl_hours}h")
        return jsonify(
            {
                "id": record_id,
                "code": code_value,
                "expires_at": _iso(expires_at),
            }
        )

    @blueprint.route("/api/admin/enrollment-codes/<code_id>", methods=["DELETE"])
    def delete_enrollment_code(code_id: str):
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                "DELETE FROM enrollment_install_codes WHERE id = ? AND used_at IS NULL",
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
        status = request.args.get("status", "pending")
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
            if status:
                sql += " WHERE status = ?"
                params.append(status)
            sql += " ORDER BY created_at ASC"
            cur.execute(sql, params)
            rows = cur.fetchall()
        finally:
            conn.close()

        approvals = []
        for row in rows:
            approvals.append(
                {
                    "id": row[0],
                    "approval_reference": row[1],
                    "guid": row[2],
                    "hostname_claimed": row[3],
                    "ssl_key_fingerprint_claimed": row[4],
                    "enrollment_code_id": row[5],
                    "status": row[6],
                    "client_nonce": row[7],
                    "server_nonce": row[8],
                    "created_at": row[9],
                    "updated_at": row[10],
                    "approved_by_user_id": row[11],
                }
            )
        return jsonify({"approvals": approvals})

    def _set_approval_status(approval_id: str, status: str, guid: Optional[str] = None):
        user = current_user() or {}
        username = user.get("username") or ""

        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT status
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
            approved_by = _lookup_user_id(cur, username) or username or "system"
            cur.execute(
                """
                UPDATE device_approvals
                   SET status = ?,
                       guid = COALESCE(?, guid),
                       approved_by_user_id = ?,
                       updated_at = ?
                 WHERE id = ?
                """,
                (
                    status,
                    guid,
                    approved_by,
                    _iso(_now()),
                    approval_id,
                ),
            )
            conn.commit()
        finally:
            conn.close()
        log("server", f"device approval {approval_id} -> {status} by {username}")
        return {"status": status}, 200

    @blueprint.route("/api/admin/device-approvals/<approval_id>/approve", methods=["POST"])
    def approve_device(approval_id: str):
        payload = request.get_json(force=True, silent=True) or {}
        guid = payload.get("guid")
        if guid:
            guid = str(guid).strip()
        result, status_code = _set_approval_status(approval_id, "approved", guid=guid)
        return jsonify(result), status_code

    @blueprint.route("/api/admin/device-approvals/<approval_id>/deny", methods=["POST"])
    def deny_device(approval_id: str):
        result, status_code = _set_approval_status(approval_id, "denied")
        return jsonify(result), status_code

    app.register_blueprint(blueprint)


def _generate_install_code() -> str:
    raw = secrets.token_hex(16).upper()
    return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))
