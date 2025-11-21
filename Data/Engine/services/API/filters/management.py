# ======================================================
# Data\Engine\services\API\filters\management.py
# Description: Device filter management endpoints backed by the Engine database.
#
# API Endpoints (if applicable):
# - GET    /api/device_filters
# - GET    /api/device_filters/<filter_id>
# - POST   /api/device_filters
# - PUT    /api/device_filters/<filter_id>
# ======================================================

from __future__ import annotations

import json
import sqlite3
import time
from typing import Any, Dict, TYPE_CHECKING, List

from flask import Blueprint, Flask, jsonify, request

from Data.Engine.services.filters.matcher import DeviceFilterMatcher

if TYPE_CHECKING:
    from .. import EngineServiceAdapters


def register_filters(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """
    Register device filter endpoints backed by the Engine database.
    """

    def _conn() -> sqlite3.Connection:
        conn = adapters.db_conn_factory()
        conn.row_factory = sqlite3.Row
        return conn

    def _row_to_filter(row: sqlite3.Row) -> Dict[str, Any]:
        try:
            groups = json.loads(row["criteria_json"] or "[]")
        except Exception:
            groups = []
        scope = (row["site_scope"] or "global").lower()
        site_value = row["site_scope"] or row["site_name"]
        return {
            "id": row["id"],
            "name": row["name"],
            "scope": scope,
            "type": "site" if scope == "scoped" else "global",
            "applyToAllSites": scope != "scoped",
            "site": site_value,
            "site_name": site_value,
            "site_scope": site_value,
            "groups": groups,
            "last_edited_by": row["last_edited_by"],
            "last_edited": row["last_edited"],
            "created_at": row["created_at"],
            "updated_at": row["updated_at"],
        }

    def _select_filter(filter_id: str) -> Dict[str, Any] | None:
        conn = _conn()
        try:
            cur = conn.execute(
                """
                SELECT id, name, site_scope, site_name,
                       criteria_json, last_edited_by, last_edited, created_at, updated_at
                  FROM device_filters
                 WHERE id = ?
                """,
                (filter_id,),
            )
            row = cur.fetchone()
            return _row_to_filter(row) if row else None
        finally:
            conn.close()

    def _normalize_payload(data: Dict[str, Any], existing: Dict[str, Any] | None = None) -> Dict[str, Any]:
        now_iso = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        base = existing or {}
        scope = (data.get("site_scope") or data.get("scope") or data.get("type") or base.get("scope") or "global").lower()
        site_name = (
            data.get("site")
            or data.get("site_name")
            or data.get("target_site")
            or base.get("site")
            or base.get("site_name")
        )
        site_scope = scope if scope in ("global", "scoped") else (base.get("site_scope") or "global")
        groups = data.get("groups")
        if not isinstance(groups, list):
            groups = base.get("groups") if isinstance(base.get("groups"), list) else []
        last_edited = data.get("last_edited") or base.get("last_edited") or now_iso
        return {
            "name": (data.get("name") or base.get("name") or "Unnamed Filter").strip(),
            "site_scope": site_scope,
            "site_name": site_name,
            "site_scope": site_scope,
            "criteria_json": json.dumps(groups or []),
            "last_edited_by": data.get("last_edited_by") or data.get("owner") or base.get("last_edited_by") or "Unknown",
            "last_edited": last_edited,
            "created_at": base.get("created_at") or now_iso,
            "updated_at": now_iso,
        }

    matcher = DeviceFilterMatcher(db_conn_factory=adapters.db_conn_factory)

    def _attach_match_counts(records: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        if not records:
            return records
        try:
            devices = matcher.fetch_devices()
        except Exception:
            devices = None
        for record in records:
            try:
                record["matching_device_count"] = matcher.count_filter_devices(record, devices=devices)
            except Exception as exc:  # pragma: no cover - defensive log path
                record["matching_device_count"] = 0
                try:
                    adapters.service_log(
                        "device_filters",
                        f"failed to compute device match count for filter {record.get('id')}: {exc}",
                        level="ERROR",
                    )
                except Exception:
                    pass
        return records

    blueprint = Blueprint("device_filters", __name__, url_prefix="/api/device_filters")

    @blueprint.route("", methods=["GET"])
    def list_filters() -> Any:
        conn = _conn()
        try:
            cur = conn.execute(
                """
                SELECT id, name, site_scope, site_name,
                       criteria_json, last_edited_by, last_edited, created_at, updated_at
                  FROM device_filters
                 ORDER BY COALESCE(updated_at, created_at) DESC, id DESC
                """
            )
            rows = cur.fetchall()
            filters = [_row_to_filter(r) for r in rows]
            _attach_match_counts(filters)
            return jsonify({"filters": filters})
        finally:
            conn.close()

    @blueprint.route("/<filter_id>", methods=["GET"])
    def get_filter(filter_id: str) -> Any:
        record = _select_filter(filter_id)
        if not record:
            return jsonify({"error": "Filter not found"}), 404
        _attach_match_counts([record])
        return jsonify({"filter": record})

    @blueprint.route("", methods=["POST"])
    def create_filter() -> Any:
        data = request.get_json(silent=True) or {}
        payload = _normalize_payload(data)
        conn = _conn()
        try:
            cur = conn.execute(
                """
                INSERT INTO device_filters (
                    name, site_scope, site_name, criteria_json,
                    last_edited_by, last_edited, created_at, updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    payload["name"],
                    payload["site_scope"],
                    payload["site_name"],
                    payload["criteria_json"],
                    payload["last_edited_by"],
                    payload["last_edited"],
                    payload["created_at"],
                    payload["updated_at"],
                ),
            )
            conn.commit()
            record = _select_filter(cur.lastrowid)
            if record:
                _attach_match_counts([record])
            adapters.service_log("device_filters", f"Created device filter '{payload['name']}'.")
            return jsonify({"filter": record or payload}), 201
        finally:
            conn.close()

    @blueprint.route("/<filter_id>", methods=["PUT"])
    def update_filter(filter_id: str) -> Any:
        existing = _select_filter(filter_id)
        if not existing:
            return jsonify({"error": "Filter not found"}), 404
        data = request.get_json(silent=True) or {}
        payload = _normalize_payload(data, existing=existing)
        conn = _conn()
        try:
            conn.execute(
                """
                UPDATE device_filters
                   SET name = ?, site_scope = ?, site_name = ?, criteria_json = ?,
                       last_edited_by = ?, last_edited = ?, updated_at = ?
                 WHERE id = ?
                """,
                (
                    payload["name"],
                    payload["site_scope"],
                    payload["site_name"],
                    payload["criteria_json"],
                    payload["last_edited_by"],
                    payload["last_edited"],
                    payload["updated_at"],
                    filter_id,
                ),
            )
            conn.commit()
            record = _select_filter(filter_id)
            if record:
                _attach_match_counts([record])
            adapters.service_log("device_filters", f"Updated device filter '{payload['name']}' (id={filter_id}).")
            return jsonify({"filter": record or payload})
        finally:
            conn.close()

    app.register_blueprint(blueprint)
    adapters.service_log("device_filters", "Registered device filter endpoints.")
