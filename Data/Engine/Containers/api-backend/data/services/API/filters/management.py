# ======================================================
# Data\Engine\services\API\filters\management.py
# Description: Device filter management endpoints backed by the Engine database.
#
# API Endpoints (if applicable):
# - GET    /api/device_filters
# - GET    /api/device_filters/metadata
# - GET    /api/device_filters/search
# - POST   /api/device_filters/preview
# - GET    /api/device_filters/<filter_id>
# - POST   /api/device_filters
# - PUT    /api/device_filters/<filter_id>
# - DELETE /api/device_filters/<filter_id>
# - POST   /api/device_filters/<filter_id>/clone
# - POST   /api/device_filters/<filter_id>/archive
# - POST   /api/device_filters/<filter_id>/unarchive
# - GET    /api/device_filters/<filter_id>/usage
# ======================================================

from __future__ import annotations

import json
from Data.Engine.db import dbapi as sqlite3
import time
from typing import Any, Dict, Iterable, List, Optional, TYPE_CHECKING

from flask import Blueprint, Flask, jsonify, request

from Data.Engine.services.auth import RequestAuthContext, UserSiteAccessManager
from Data.Engine.services.filters.matcher import (
    DeviceFilterMatcher,
    filter_metadata,
)

if TYPE_CHECKING:
    from .. import EngineServiceAdapters


def register_filters(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register device filter endpoints backed by the Engine database."""

    matcher = DeviceFilterMatcher(db_conn_factory=adapters.db_conn_factory)
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=adapters.context.logger)
    auth_context = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )

    def _conn() -> sqlite3.Connection:
        conn = adapters.db_conn_factory()
        conn.row_factory = sqlite3.Row
        return conn

    def _require_user() -> tuple[Optional[Dict[str, Any]], Optional[tuple[Dict[str, Any], int]]]:
        return auth_context.require_user()

    def _load_all_site_ids() -> set[int]:
        conn = _conn()
        try:
            cur = conn.cursor()
            cur.execute("SELECT id FROM sites")
            return {
                int(row[0])
                for row in cur.fetchall()
                if row and row[0] is not None
            }
        finally:
            conn.close()

    def _effective_filter_site_ids(
        record: Dict[str, Any],
        *,
        all_site_ids: Optional[set[int]] = None,
    ) -> set[int]:
        site_mode = str(record.get("site_mode") or "global").strip().lower()
        configured_site_ids = {
            int(value)
            for value in (record.get("site_ids") or [])
            if value is not None
        }
        if site_mode == "specific_sites":
            return configured_site_ids
        current_site_ids = set(all_site_ids) if all_site_ids is not None else _load_all_site_ids()
        if site_mode == "global_exclusions":
            return current_site_ids.difference(configured_site_ids)
        return current_site_ids

    def _filter_visible_to_user(
        record: Dict[str, Any],
        user: Optional[Dict[str, Any]],
        *,
        all_site_ids: Optional[set[int]] = None,
    ) -> bool:
        allowed_site_ids = site_access.site_ids_for_user(user)
        if allowed_site_ids is None:
            return True
        effective_site_ids = _effective_filter_site_ids(record, all_site_ids=all_site_ids)
        return effective_site_ids.issubset(allowed_site_ids)

    def _validate_filter_scope(record: Dict[str, Any], user: Optional[Dict[str, Any]]) -> Optional[tuple[Dict[str, Any], int]]:
        if _filter_visible_to_user(record, user):
            return None
        return (
            {
                "error": "out_of_scope_sites",
                "message": "One or more selected sites is outside your assigned site scope.",
            },
            403,
        )

    def _trim_single_line(value: Any) -> str:
        text = str(value or "").strip()
        if not text:
            return ""
        return " ".join(line.strip() for line in text.splitlines() if line.strip()).strip()

    def _normalize_request_record(
        data: Dict[str, Any],
        *,
        existing: Optional[Dict[str, Any]] = None,
        user: Optional[Dict[str, Any]] = None,
    ) -> Dict[str, Any]:
        base = existing or {}
        now_ts = int(time.time())
        site_mode = str(
            data.get("site_mode")
            or data.get("siteMode")
            or base.get("site_mode")
            or "global"
        ).strip().lower()
        record = matcher.normalize_filter_record(
            {
                "id": data.get("id") or base.get("id"),
                "name": _trim_single_line(data.get("name") or base.get("name")),
                "description": _trim_single_line(data.get("description") or base.get("description")),
                "archived": data.get("archived") if "archived" in data else base.get("archived", False),
                "criteria_mode": "advanced",
                "site_mode": site_mode,
                "site_ids": (
                    data.get("site_ids")
                    or data.get("sites")
                    or data.get("siteIds")
                    or data.get("site_scope_values")
                    or base.get("site_ids")
                    or []
                ),
                "criteria": (
                    data.get("criteria")
                    or data.get("criteria_payload")
                    or ({"groups": data.get("groups")} if isinstance(data.get("groups"), list) else None)
                    or base.get("criteria")
                    or base.get("advanced_criteria")
                    or {"groups": []}
                ),
                "basic_criteria": (
                    data.get("basic_criteria")
                    or data.get("basicCriteria")
                    or base.get("basic_criteria")
                    or {"criteria": []}
                ),
                "advanced_criteria": (
                    data.get("advanced_criteria")
                    or data.get("advancedCriteria")
                    or ({"groups": data.get("groups")} if isinstance(data.get("groups"), list) else None)
                    or base.get("advanced_criteria")
                    or {"groups": []}
                ),
                "last_edited_by": (user or {}).get("username") or base.get("last_edited_by") or "Unknown",
                "created_at": base.get("created_at") or now_ts,
                "updated_at": now_ts,
            }
        )
        if not record["name"]:
            record["name"] = "Unnamed Filter"
        return record

    def _load_ordered_filter_ids(*, archived: Optional[bool] = None) -> List[int]:
        conn = _conn()
        try:
            cur = conn.cursor()
            clauses: List[str] = []
            params: List[Any] = []
            if archived is not None:
                clauses.append("COALESCE(archived, 0) = ?")
                params.append(1 if archived else 0)
            where_sql = f"WHERE {' AND '.join(clauses)}" if clauses else ""
            cur.execute(
                f"""
                    SELECT id
                      FROM device_filters
                    {where_sql}
                  ORDER BY COALESCE(updated_at, created_at, 0) DESC, id DESC
                """,
                tuple(params),
            )
            return [int(row[0]) for row in cur.fetchall()]
        finally:
            conn.close()

    def _usage_for_filters(
        filter_ids: Iterable[int],
        *,
        user: Optional[Dict[str, Any]] = None,
    ) -> Dict[int, Dict[str, Any]]:
        ids = {int(value) for value in filter_ids if value is not None}
        usage = {filter_id: {"job_count": 0, "jobs": []} for filter_id in ids}
        if not ids:
            return usage
        conn = _conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                    SELECT id, name, targets_json
                      FROM scheduled_jobs
                  ORDER BY LOWER(name) ASC
                """
            )
            rows = site_access.filter_job_rows_for_user(user, cur.fetchall(), targets_index=2)
            for row in rows:
                job_id = int(row["id"])
                job_name = row["name"] or f"Job {job_id}"
                try:
                    targets = json.loads(row["targets_json"] or "[]")
                except Exception:
                    targets = []
                referenced: set[int] = set()
                for target in targets or []:
                    if not isinstance(target, dict):
                        continue
                    kind = str(target.get("kind") or target.get("type") or "").strip().lower()
                    if kind != "filter" and target.get("filter_id") is None:
                        continue
                    filter_id = target.get("filter_id") or target.get("id")
                    try:
                        filter_id_int = int(filter_id)
                    except (TypeError, ValueError):
                        continue
                    if filter_id_int in ids:
                        referenced.add(filter_id_int)
                for filter_id in referenced:
                    usage.setdefault(filter_id, {"job_count": 0, "jobs": []})
                    usage[filter_id]["jobs"].append(
                        {
                            "id": job_id,
                            "name": job_name,
                            "path": f"/scheduling/job/{job_id}",
                        }
                    )
            for filter_id, payload in usage.items():
                payload["jobs"].sort(key=lambda item: item["name"].lower())
                payload["job_count"] = len(payload["jobs"])
            return usage
        finally:
            conn.close()

    def _attach_counts_and_usage(
        records: List[Dict[str, Any]],
        *,
        user: Optional[Dict[str, Any]] = None,
    ) -> List[Dict[str, Any]]:
        if not records:
            return records
        try:
            devices = matcher.fetch_devices(allowed_site_ids=site_access.site_ids_for_user(user))
        except Exception:
            devices = []
        usage_lookup = _usage_for_filters(
            (record["id"] for record in records if record.get("id") is not None),
            user=user,
        )
        for record in records:
            try:
                record["matching_device_count"] = matcher.count_filter_devices(record, devices=devices)
            except Exception as exc:  # pragma: no cover - defensive log path
                record["matching_device_count"] = 0
                adapters.service_log(
                    "device_filters",
                    f"failed to compute device match count for filter {record.get('id')}: {exc}",
                    level="ERROR",
                )
            usage = usage_lookup.get(int(record["id"])) if record.get("id") is not None else None
            record["usage"] = usage or {"job_count": 0, "jobs": []}
        return records

    def _select_filter(filter_id: int, *, user: Optional[Dict[str, Any]] = None) -> Optional[Dict[str, Any]]:
        records = matcher.load_filters([filter_id], include_archived=True)
        record = records.get(int(filter_id))
        if not record:
            return None
        if not _filter_visible_to_user(record, user):
            return None
        _attach_counts_and_usage([record], user=user)
        return record

    def _sync_filter_sites(cur: sqlite3.Cursor, filter_id: int, site_ids: List[int]) -> None:
        cur.execute("DELETE FROM device_filter_sites WHERE filter_id = ?", (int(filter_id),))
        if not site_ids:
            return
        cur.executemany(
            "INSERT INTO device_filter_sites(filter_id, site_id) VALUES (?, ?)",
            [(int(filter_id), int(site_id)) for site_id in site_ids],
        )

    def _ensure_name_available(name: str, *, existing_filter_id: Optional[int] = None) -> Optional[str]:
        conn = _conn()
        try:
            cur = conn.cursor()
            if existing_filter_id is None:
                cur.execute("SELECT id FROM device_filters WHERE LOWER(name) = LOWER(?)", (name,))
            else:
                cur.execute(
                    "SELECT id FROM device_filters WHERE LOWER(name) = LOWER(?) AND id != ?",
                    (name, int(existing_filter_id)),
                )
            row = cur.fetchone()
            if row:
                return "A filter with this name already exists."
            return None
        finally:
            conn.close()

    def _resolve_clone_name(source_name: str) -> str:
        prefix = f"(Clone) {_trim_single_line(source_name) or 'Filter'}"
        candidate = prefix
        suffix = 2
        while _ensure_name_available(candidate) is not None:
            candidate = f"{prefix} {suffix}"
            suffix += 1
        return candidate

    def _write_filter(
        record: Dict[str, Any],
        *,
        existing_filter_id: Optional[int] = None,
        user: Optional[Dict[str, Any]] = None,
    ) -> Dict[str, Any]:
        conn = _conn()
        try:
            cur = conn.cursor()
            if existing_filter_id is None:
                cur.execute(
                    """
                    INSERT INTO device_filters (
                        name, description, archived, criteria_mode, site_mode,
                        basic_criteria_json, advanced_criteria_json,
                        last_edited_by, created_at, updated_at
                    )
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        record["name"],
                        record["description"],
                        1 if record["archived"] else 0,
                        "advanced",
                        record["site_mode"],
                        json.dumps({"criteria": []}),
                        json.dumps(record["advanced_criteria"] or {"groups": []}),
                        record["last_edited_by"],
                        int(record["created_at"]),
                        int(record["updated_at"]),
                    ),
                )
                filter_id = int(cur.lastrowid)
            else:
                filter_id = int(existing_filter_id)
                cur.execute(
                    """
                    UPDATE device_filters
                       SET name = ?,
                           description = ?,
                           archived = ?,
                           criteria_mode = ?,
                           site_mode = ?,
                           basic_criteria_json = ?,
                           advanced_criteria_json = ?,
                           last_edited_by = ?,
                           updated_at = ?
                     WHERE id = ?
                    """,
                    (
                        record["name"],
                        record["description"],
                        1 if record["archived"] else 0,
                        "advanced",
                        record["site_mode"],
                        json.dumps({"criteria": []}),
                        json.dumps(record["advanced_criteria"] or {"groups": []}),
                        record["last_edited_by"],
                        int(record["updated_at"]),
                        filter_id,
                    ),
                )
            _sync_filter_sites(cur, filter_id, list(record.get("site_ids") or []))
            conn.commit()
        finally:
            conn.close()
        saved = _select_filter(filter_id, user=user)
        if not saved:
            raise RuntimeError("Saved filter could not be reloaded.")
        return saved

    def _usage_conflict(
        filter_id: int,
        *,
        user: Optional[Dict[str, Any]] = None,
    ) -> Optional[Dict[str, Any]]:
        usage = _usage_for_filters([filter_id], user=user).get(int(filter_id)) or {"job_count": 0, "jobs": []}
        if usage["job_count"] <= 0:
            return None
        return {
            "error": "filter_in_use",
            "message": "This filter is referenced by scheduled jobs.",
            "jobs": usage["jobs"],
        }

    blueprint = Blueprint("device_filters", __name__, url_prefix="/api/device_filters")

    @blueprint.route("", methods=["GET"])
    def list_filters() -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        archived_raw = str(request.args.get("archived") or "0").strip().lower()
        archived = archived_raw in {"1", "true", "yes", "archived"}
        site_raw = request.args.get("site") or request.args.get("site_id")
        try:
            selected_site_id = int(str(site_raw).strip()) if site_raw not in (None, "") else None
        except Exception:
            selected_site_id = None
        if selected_site_id is not None and selected_site_id <= 0:
            selected_site_id = None
        if selected_site_id is not None and not site_access.user_can_access_site(
            user,
            selected_site_id,
            allow_unassigned_admin_only=False,
        ):
            return jsonify({"filters": [], "archived": archived})
        ids = _load_ordered_filter_ids(archived=archived)
        records = matcher.load_filters(ids, include_archived=True)
        all_site_ids = _load_all_site_ids()
        ordered = [
            records[int(filter_id)]
            for filter_id in ids
            if int(filter_id) in records and _filter_visible_to_user(records[int(filter_id)], user, all_site_ids=all_site_ids)
        ]
        if selected_site_id is not None and ordered:
            try:
                visible_devices = matcher.fetch_devices(allowed_site_ids=site_access.site_ids_for_user(user))
            except Exception:
                visible_devices = []
            selected_site_key = str(selected_site_id)
            site_scoped: List[Dict[str, Any]] = []
            for record in ordered:
                try:
                    matched_devices = matcher.match_filter_devices(record, devices=visible_devices)
                except Exception:
                    matched_devices = []
                if any(str(device.get("site_id") or "") == selected_site_key for device in matched_devices):
                    site_scoped.append(record)
            ordered = site_scoped
        _attach_counts_and_usage(ordered, user=user)
        return jsonify({"filters": ordered, "archived": archived})

    @blueprint.route("/metadata", methods=["GET"])
    def get_metadata() -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        return jsonify(filter_metadata(db_conn_factory=adapters.db_conn_factory))

    @blueprint.route("/search", methods=["GET"])
    def search_filters() -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        query = str(request.args.get("query") or request.args.get("name") or "").strip()
        if len(query) < 3:
            return jsonify({"filters": [], "query": query, "count": 0})

        ids = _load_ordered_filter_ids(archived=False)
        records = matcher.load_filters(ids, include_archived=True)
        all_site_ids = _load_all_site_ids()
        try:
            visible_devices = matcher.fetch_devices(allowed_site_ids=site_access.site_ids_for_user(user))
        except Exception:
            visible_devices = []
        query_lc = query.lower()
        visible_matches: List[Dict[str, Any]] = []
        for filter_id in ids:
            record = records.get(int(filter_id))
            if not record:
                continue
            if not _filter_visible_to_user(record, user, all_site_ids=all_site_ids):
                continue
            name = str(record.get("name") or "").strip()
            if not name or query_lc not in name.lower():
                continue
            try:
                matching_device_count = matcher.count_filter_devices(record, devices=visible_devices)
            except Exception:
                matching_device_count = 0
            visible_matches.append(
                {
                    "id": int(record.get("id") or filter_id),
                    "name": name,
                    "description": str(record.get("description") or "").strip(),
                    "site_mode": str(record.get("site_mode") or "global"),
                    "site_ids": list(record.get("site_ids") or []),
                    "site_names": list(record.get("site_names") or []),
                    "scope_summary": "",
                    "matching_device_count": matching_device_count,
                }
            )

        def _sort_key(record: Dict[str, Any]) -> tuple[int, int, str]:
            name_lc = str(record.get("name") or "").lower()
            return (
                0 if name_lc == query_lc else 1,
                0 if name_lc.startswith(query_lc) else 1,
                name_lc,
            )

        visible_matches.sort(key=_sort_key)
        for record in visible_matches:
            site_names = [str(value).strip() for value in (record.get("site_names") or []) if str(value).strip()]
            site_mode = str(record.get("site_mode") or "global").strip().lower()
            if site_mode == "specific_sites":
                scope_summary = f"Specific Sites: {', '.join(site_names)}" if site_names else "Specific Sites"
            elif site_mode == "global_exclusions":
                scope_summary = (
                    f"Global w/ Exclusions: {', '.join(site_names)}" if site_names else "Global w/ Exclusions"
                )
            else:
                scope_summary = "Global"
            record["scope_summary"] = scope_summary

        return jsonify({"filters": visible_matches[:25], "query": query, "count": len(visible_matches)})

    @blueprint.route("/preview", methods=["POST"])
    def preview_filter() -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        filter_id = data.get("filter_id") or data.get("id")
        if filter_id is not None and not any(
            key in data
            for key in {"name", "criteria_mode", "criteriaMode", "site_mode", "siteMode", "criteria", "basic_criteria", "advanced_criteria", "groups"}
        ):
            record = _select_filter(int(filter_id), user=user)
            if not record:
                return jsonify({"error": "Filter not found"}), 404
            draft = record
        else:
            draft = _normalize_request_record(data, user=user)
            scope_error = _validate_filter_scope(draft, user)
            if scope_error:
                payload, status = scope_error
                return jsonify(payload), status
        errors = matcher.validate_filter_record(draft)
        if errors:
            return jsonify({"error": "validation_failed", "validation_errors": errors}), 400
        devices = matcher.match_filter_devices(
            draft,
            devices=matcher.fetch_devices(allowed_site_ids=site_access.site_ids_for_user(user)),
        )
        return jsonify(
            {
                "matched_device_count": len(devices),
                "devices": devices,
                "site_mode": draft.get("site_mode"),
            }
        )

    @blueprint.route("/<int:filter_id>", methods=["GET"])
    def get_filter(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        record = _select_filter(filter_id, user=user)
        if not record:
            return jsonify({"error": "Filter not found"}), 404
        return jsonify({"filter": record})

    @blueprint.route("/<int:filter_id>/usage", methods=["GET"])
    def get_filter_usage(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        record = _select_filter(filter_id, user=user)
        if not record:
            return jsonify({"error": "Filter not found"}), 404
        return jsonify({"usage": record.get("usage") or {"job_count": 0, "jobs": []}})

    @blueprint.route("", methods=["POST"])
    def create_filter() -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        data = request.get_json(silent=True) or {}
        record = _normalize_request_record(data, user=user)
        scope_error = _validate_filter_scope(record, user)
        if scope_error:
            payload, status = scope_error
            return jsonify(payload), status
        errors = matcher.validate_filter_record(record)
        if errors:
            return jsonify({"error": "validation_failed", "validation_errors": errors}), 400
        name_error = _ensure_name_available(record["name"])
        if name_error:
            return jsonify({"error": "duplicate_name", "message": name_error}), 409
        try:
            saved = _write_filter(record, user=user)
        except sqlite3.IntegrityError:
            return jsonify({"error": "duplicate_name", "message": "A filter with this name already exists."}), 409
        adapters.service_log("device_filters", f"Created device filter '{saved['name']}'.")
        return jsonify({"filter": saved}), 201

    @blueprint.route("/<int:filter_id>", methods=["PUT"])
    def update_filter(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        existing = _select_filter(filter_id, user=user)
        if not existing:
            return jsonify({"error": "Filter not found"}), 404
        data = request.get_json(silent=True) or {}
        record = _normalize_request_record(data, existing=existing, user=user)
        scope_error = _validate_filter_scope(record, user)
        if scope_error:
            payload, status = scope_error
            return jsonify(payload), status
        errors = matcher.validate_filter_record(record)
        if errors:
            return jsonify({"error": "validation_failed", "validation_errors": errors}), 400
        name_error = _ensure_name_available(record["name"], existing_filter_id=filter_id)
        if name_error:
            return jsonify({"error": "duplicate_name", "message": name_error}), 409
        saved = _write_filter(record, existing_filter_id=filter_id, user=user)
        adapters.service_log("device_filters", f"Updated device filter '{saved['name']}' (id={filter_id}).")
        return jsonify({"filter": saved})

    @blueprint.route("/<int:filter_id>/clone", methods=["POST"])
    def clone_filter(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        existing = _select_filter(filter_id, user=user)
        if not existing:
            return jsonify({"error": "Filter not found"}), 404
        clone_record = matcher.normalize_filter_record(existing)
        clone_record["id"] = None
        clone_record["name"] = _resolve_clone_name(existing.get("name") or "Filter")
        clone_record["archived"] = False
        clone_record["last_edited_by"] = (user or {}).get("username") or existing.get("last_edited_by") or "Unknown"
        clone_record["created_at"] = int(time.time())
        clone_record["updated_at"] = clone_record["created_at"]
        saved = _write_filter(clone_record, user=user)
        adapters.service_log("device_filters", f"Cloned device filter '{existing['name']}' into '{saved['name']}'.")
        return jsonify({"filter": saved}), 201

    @blueprint.route("/<int:filter_id>/archive", methods=["POST"])
    def archive_filter(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        existing = _select_filter(filter_id, user=user)
        if not existing:
            return jsonify({"error": "Filter not found"}), 404
        conflict = _usage_conflict(filter_id, user=user)
        if conflict:
            return jsonify(conflict), 409
        existing["archived"] = True
        existing["updated_at"] = int(time.time())
        existing["last_edited_by"] = (user or {}).get("username") or existing.get("last_edited_by") or "Unknown"
        saved = _write_filter(existing, existing_filter_id=filter_id, user=user)
        adapters.service_log("device_filters", f"Archived device filter '{saved['name']}' (id={filter_id}).")
        return jsonify({"filter": saved})

    @blueprint.route("/<int:filter_id>/unarchive", methods=["POST"])
    def unarchive_filter(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        existing = _select_filter(filter_id, user=user)
        if not existing:
            return jsonify({"error": "Filter not found"}), 404
        existing["archived"] = False
        existing["updated_at"] = int(time.time())
        existing["last_edited_by"] = (user or {}).get("username") or existing.get("last_edited_by") or "Unknown"
        saved = _write_filter(existing, existing_filter_id=filter_id, user=user)
        adapters.service_log("device_filters", f"Unarchived device filter '{saved['name']}' (id={filter_id}).")
        return jsonify({"filter": saved})

    @blueprint.route("/<int:filter_id>", methods=["DELETE"])
    def delete_filter(filter_id: int) -> Any:
        user, requirement = _require_user()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        existing = _select_filter(filter_id, user=user)
        if not existing:
            return jsonify({"error": "Filter not found"}), 404
        conflict = _usage_conflict(filter_id, user=user)
        if conflict:
            return jsonify(conflict), 409
        conn = _conn()
        try:
            conn.execute("DELETE FROM device_filter_sites WHERE filter_id = ?", (filter_id,))
            conn.execute("DELETE FROM device_filters WHERE id = ?", (filter_id,))
            conn.commit()
        finally:
            conn.close()
        adapters.service_log("device_filters", f"Deleted device filter '{existing['name']}' (id={filter_id}).")
        return jsonify({"status": "ok"})

    app.register_blueprint(blueprint)
    adapters.service_log("device_filters", "Registered device filter endpoints.")
