# ======================================================
# Data\Engine\services\API\access_management\site_assignments.py
# Description: Admin-only user site-assignment endpoints for Borealis RBAC.
#
# API Endpoints (if applicable):
# - POST /api/user_site_assignments/selection (Token Authenticated (Admin)) - Loads site-assignment state for selected operators.
# - POST /api/user_site_assignments/assign (Token Authenticated (Admin)) - Replaces site assignments for selected operators.
# ======================================================

"""User site-assignment endpoints for Borealis Engine RBAC."""

from __future__ import annotations

import time
from Data.Engine.db import dbapi as sqlite3
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional, Sequence

from flask import Blueprint, Flask, jsonify, request

from ...auth.context import RequestAuthContext

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters


ADMIN_SELECTION_MESSAGE = (
    "An administrator was selected, admins inherantly have access to all managed sites.  "
    "Please unselect the admin and try again."
)
MIXED_ASSIGNMENT_WARNING = (
    "The users selected for site assignment are members of different sites.  "
    "Changes made here will overwrite existing site assignments for the selected users."
)


def _now_ts() -> int:
    return int(time.time())


def _normalize_usernames(raw: Any) -> List[str]:
    if not isinstance(raw, list):
        return []
    usernames: List[str] = []
    seen: set[str] = set()
    for entry in raw:
        candidate = str(entry or "").strip()
        lowered = candidate.lower()
        if not candidate or lowered in seen:
            continue
        seen.add(lowered)
        usernames.append(candidate)
    return usernames


def _normalize_site_ids(raw: Any) -> List[int]:
    if not isinstance(raw, list):
        return []
    site_ids: List[int] = []
    seen: set[int] = set()
    for entry in raw:
        try:
            parsed = int(entry)
        except (TypeError, ValueError):
            continue
        if parsed in seen:
            continue
        seen.add(parsed)
        site_ids.append(parsed)
    return site_ids


def _row_to_user(row: Sequence[Any]) -> Mapping[str, Any]:
    return {
        "id": row[0],
        "username": row[1],
        "display_name": row[2] or row[1],
        "role": row[3] or "User",
    }


def _row_to_site(row: Sequence[Any]) -> Mapping[str, Any]:
    return {
        "id": row[0],
        "name": row[1],
        "description": row[2] or "",
        "created_at": row[3] or 0,
        "device_count": row[4] or 0,
        "enrollment_code": row[5] or "",
    }


class UserSiteAssignmentService:
    """Loads and replaces per-user site assignments."""

    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self._db_conn_factory = adapters.db_conn_factory
        self._logger = adapters.context.logger
        self._service_log = adapters.service_log
        self._auth = RequestAuthContext(
            app=app,
            dev_mode_manager=adapters.dev_mode_manager,
            config=adapters.config,
            logger=adapters.context.logger,
            db_conn_factory=adapters.db_conn_factory,
            aegis_cipher_service=adapters.aegis_cipher_service,
        )

    def _conn(self) -> sqlite3.Connection:
        conn = self._db_conn_factory()
        try:
            conn.row_factory = sqlite3.Row
        except Exception:
            pass
        return conn

    def _require_admin(self):
        return self._auth.require_admin()

    def _load_selected_users(
        self,
        cur: sqlite3.Cursor,
        usernames: List[str],
    ) -> List[Mapping[str, Any]]:
        if not usernames:
            return []
        placeholders = ",".join("?" for _ in usernames)
        cur.execute(
            f"""
            SELECT id, username, display_name, role
              FROM users
             WHERE LOWER(username) IN ({placeholders})
            """,
            tuple(username.lower() for username in usernames),
        )
        rows = [_row_to_user(row) for row in cur.fetchall()]
        by_username = {str(row["username"]).lower(): row for row in rows}
        return [by_username[name.lower()] for name in usernames if name.lower() in by_username]

    def _reject_if_admin_selected(self, users: Sequence[Mapping[str, Any]]) -> Optional[tuple[Dict[str, Any], int]]:
        for user in users:
            if str(user.get("role") or "").strip().lower() == "admin":
                return {"error": "admin_selected", "message": ADMIN_SELECTION_MESSAGE}, 400
        return None

    def _load_site_assignments(
        self,
        cur: sqlite3.Cursor,
        users: Sequence[Mapping[str, Any]],
    ) -> Dict[str, List[Mapping[str, Any]]]:
        user_ids = [int(user["id"]) for user in users if user.get("id") is not None]
        if not user_ids:
            return {}
        placeholders = ",".join("?" for _ in user_ids)
        cur.execute(
            f"""
            SELECT usa.user_id, s.id, s.name
              FROM user_site_assignments AS usa
              JOIN sites AS s ON s.id = usa.site_id
             WHERE usa.user_id IN ({placeholders})
          ORDER BY LOWER(s.name) ASC
            """,
            tuple(user_ids),
        )
        usernames_by_id = {int(user["id"]): str(user["username"]) for user in users if user.get("id") is not None}
        assignments: Dict[str, List[Mapping[str, Any]]] = {str(user["username"]): [] for user in users}
        for row in cur.fetchall():
            username = usernames_by_id.get(int(row[0]))
            if not username:
                continue
            assignments.setdefault(username, []).append({"id": int(row[1]), "name": row[2] or ""})
        return assignments

    def _load_all_sites(self, cur: sqlite3.Cursor) -> List[Mapping[str, Any]]:
        cur.execute(
            """
            SELECT s.id,
                   s.name,
                   s.description,
                   s.created_at,
                   COALESCE(ds.cnt, 0) AS device_count,
                   s.enrollment_code
              FROM sites AS s
         LEFT JOIN (
                   SELECT site_id, COUNT(*) AS cnt
                     FROM device_sites
                 GROUP BY site_id
               ) AS ds ON ds.site_id = s.id
          ORDER BY LOWER(s.name) ASC
            """
        )
        return [_row_to_site(row) for row in cur.fetchall()]

    def load_selection(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        usernames = _normalize_usernames((request.get_json(silent=True) or {}).get("usernames"))
        if not usernames:
            return jsonify({"error": "No users were selected."}), 400

        conn = self._conn()
        try:
            cur = conn.cursor()
            users = self._load_selected_users(cur, usernames)
            if len(users) != len(usernames):
                return jsonify({"error": "One or more selected users no longer exists."}), 404
            admin_error = self._reject_if_admin_selected(users)
            if admin_error:
                payload, status = admin_error
                return jsonify(payload), status

            assignments = self._load_site_assignments(cur, users)
            assignment_signatures = {
                tuple(sorted(int(site["id"]) for site in assignments.get(str(user["username"]), [])))
                for user in users
            }
            has_mixed_assignments = len(assignment_signatures) > 1
            selected_site_ids = [] if has_mixed_assignments else list(next(iter(assignment_signatures), ()))
            sites = self._load_all_sites(cur)
            return jsonify(
                {
                    "users": users,
                    "sites": sites,
                    "existing_assignments": assignments,
                    "selected_site_ids": selected_site_ids,
                    "has_mixed_assignments": has_mixed_assignments,
                    "warning": MIXED_ASSIGNMENT_WARNING if has_mixed_assignments else "",
                }
            )
        except Exception as exc:
            self._logger.debug("Failed to load user site assignment selection", exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            conn.close()

    def assign_sites(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        payload = request.get_json(silent=True) or {}
        usernames = _normalize_usernames(payload.get("usernames"))
        site_ids = _normalize_site_ids(payload.get("site_ids"))
        if not usernames:
            return jsonify({"error": "No users were selected."}), 400

        conn = self._conn()
        try:
            cur = conn.cursor()
            users = self._load_selected_users(cur, usernames)
            if len(users) != len(usernames):
                return jsonify({"error": "One or more selected users no longer exists."}), 404
            admin_error = self._reject_if_admin_selected(users)
            if admin_error:
                payload, status = admin_error
                return jsonify(payload), status

            if site_ids:
                placeholders = ",".join("?" for _ in site_ids)
                cur.execute(
                    f"SELECT id FROM sites WHERE id IN ({placeholders})",
                    tuple(site_ids),
                )
                existing_ids = {int(row[0]) for row in cur.fetchall()}
                missing_ids = [site_id for site_id in site_ids if site_id not in existing_ids]
                if missing_ids:
                    return jsonify({"error": f"One or more selected sites no longer exists: {missing_ids}"}), 404

            user_ids = [int(user["id"]) for user in users]
            delete_placeholders = ",".join("?" for _ in user_ids)
            cur.execute(
                f"DELETE FROM user_site_assignments WHERE user_id IN ({delete_placeholders})",
                tuple(user_ids),
            )
            now_ts = _now_ts()
            if site_ids:
                cur.executemany(
                    """
                    INSERT INTO user_site_assignments(user_id, site_id, assigned_at)
                    VALUES (?, ?, ?)
                    """,
                    [
                        (int(user_id), int(site_id), now_ts)
                        for user_id in user_ids
                        for site_id in site_ids
                    ],
                )
            conn.commit()
            self._service_log(
                "access_management",
                "updated user site assignments "
                f"users={','.join(str(user['username']) for user in users)} "
                f"site_ids={','.join(str(site_id) for site_id in site_ids) or '-'}",
            )
            return jsonify(
                {
                    "status": "ok",
                    "assigned_user_count": len(users),
                    "assigned_site_ids": site_ids,
                }
            )
        except Exception as exc:
            try:
                conn.rollback()
            except Exception:
                pass
            self._logger.debug("Failed to assign user sites", exc_info=True)
            return jsonify({"error": str(exc)}), 500
        finally:
            conn.close()


def register_user_site_assignment_management(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register user site-assignment endpoints."""

    service = UserSiteAssignmentService(app, adapters)
    blueprint = Blueprint("access_mgmt_user_site_assignments", __name__)

    @blueprint.route("/api/user_site_assignments/selection", methods=["POST"])
    def _selection():
        return service.load_selection()

    @blueprint.route("/api/user_site_assignments/assign", methods=["POST"])
    def _assign():
        return service.assign_sites()

    app.register_blueprint(blueprint)
