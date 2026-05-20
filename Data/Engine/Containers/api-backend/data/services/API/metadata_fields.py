# ======================================================
# Data\Engine\services\API\metadata_fields.py
# Description: REST endpoints for global Agent Metadata Field definitions and per-device values.
#
# API Endpoints (if applicable):
# - GET /api/metadata_fields
# - PUT /api/metadata_fields/<field_number>
# - GET /api/devices/<device_id>/metadata_fields
# - PUT /api/devices/<device_id>/metadata_fields/<field_number>
# ======================================================

from __future__ import annotations

import time
from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from flask import Blueprint, Flask, jsonify, request

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.activity_history import insert_activity_history_row
from Data.Engine.services.auth import RequestAuthContext, UserSiteAccessManager
from Data.Engine.services.metadata_fields import (
    METADATA_VALUE_MAX_LENGTH,
    decode_metadata_value,
    device_metadata_rows,
    list_metadata_definitions,
    metadata_field_key,
    metadata_field_label,
    normalize_field_number,
    normalize_metadata_value,
    upsert_device_metadata_value,
    upsert_metadata_definition,
)

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from . import EngineServiceAdapters


def register_metadata_fields(app: Flask, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("metadata_fields", __name__)
    auth_context = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=adapters.context.logger)

    def _conn() -> sqlite3.Connection:
        conn = adapters.db_conn_factory()
        conn.row_factory = sqlite3.Row
        return conn

    def _actor(user: Optional[Dict[str, Any]]) -> str:
        return str((user or {}).get("username") or "Unknown").strip() or "Unknown"

    def _resolve_device(
        conn: sqlite3.Connection,
        device_id: str,
    ) -> Optional[Dict[str, Any]]:
        token = str(device_id or "").strip()
        if not token:
            return None
        cur = conn.cursor()
        cur.execute(
            """
            SELECT d.guid, d.hostname, ds.site_id, s.name AS site_name
              FROM devices AS d
         LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
         LEFT JOIN sites AS s ON s.id = ds.site_id
             WHERE LOWER(d.guid) = LOWER(?)
                OR d.hostname = ?
                OR d.agent_id = ?
             LIMIT 1
            """,
            (token, token, token),
        )
        row = cur.fetchone()
        if not row:
            return None
        site_id = None
        try:
            site_id = int(row["site_id"]) if row["site_id"] is not None else None
        except Exception:
            site_id = None
        return {
            "guid": str(row["guid"] or ""),
            "hostname": str(row["hostname"] or ""),
            "site_id": site_id,
            "site_name": str(row["site_name"] or ""),
        }

    def _require_device_access(
        conn: sqlite3.Connection,
        device_id: str,
        user: Dict[str, Any],
    ) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        device = _resolve_device(conn, device_id)
        if not device:
            return None, ({"error": "not_found", "message": "Device not found."}, 404)
        if not site_access.user_can_access_site(user, device.get("site_id")):
            return None, ({"error": "not_found", "message": "Device not found."}, 404)
        return device, None

    @blueprint.route("/api/metadata_fields", methods=["GET"])
    def get_metadata_fields():
        user, error = auth_context.require_user()
        if error:
            return jsonify(error[0]), error[1]
        conn = _conn()
        try:
            fields = list_metadata_definitions(conn)
        finally:
            conn.close()
        return jsonify(
            {
                "fields": fields,
                "count": len(fields),
                "value_limit": METADATA_VALUE_MAX_LENGTH,
            }
        )

    @blueprint.route("/api/metadata_fields/<int:field_number>", methods=["PUT"])
    def update_metadata_field_definition(field_number: int):
        admin_error = auth_context.require_admin()
        if admin_error:
            return jsonify(admin_error[0]), admin_error[1]
        user, user_error = auth_context.require_user()
        if user_error:
            return jsonify(user_error[0]), user_error[1]
        parsed = normalize_field_number(field_number)
        if parsed is None:
            return jsonify({"error": "invalid_field", "message": "Field number must be between 1 and 500."}), 400
        data = request.get_json(force=True, silent=True) or {}
        description = data.get("description") if isinstance(data, dict) else ""
        conn = _conn()
        try:
            field = upsert_metadata_definition(
                conn,
                parsed,
                description,
                actor=_actor(user),
                updated_at=int(time.time()),
            )
            conn.commit()
        finally:
            conn.close()
        return jsonify({"field": field})

    @blueprint.route("/api/devices/<device_id>/metadata_fields", methods=["GET"])
    def get_device_metadata_fields(device_id: str):
        user, error = auth_context.require_user()
        if error:
            return jsonify(error[0]), error[1]
        conn = _conn()
        try:
            device, access_error = _require_device_access(conn, device_id, user or {})
            if access_error:
                return jsonify(access_error[0]), access_error[1]
            rows = device_metadata_rows(conn, device["guid"])
        finally:
            conn.close()
        return jsonify(
            {
                "device": device,
                "fields": rows,
                "count": len(rows),
                "value_limit": METADATA_VALUE_MAX_LENGTH,
            }
        )

    @blueprint.route("/api/devices/<device_id>/metadata_fields/<int:field_number>", methods=["PUT"])
    def update_device_metadata_field(device_id: str, field_number: int):
        user, error = auth_context.require_user()
        if error:
            return jsonify(error[0]), error[1]
        parsed = normalize_field_number(field_number)
        if parsed is None:
            return jsonify({"error": "invalid_field", "message": "Field number must be between 1 and 500."}), 400
        data = request.get_json(force=True, silent=True) or {}
        value = normalize_metadata_value(data.get("value", "") if isinstance(data, dict) else "")
        actor = _actor(user)
        now_ts = int(time.time())
        conn = _conn()
        try:
            device, access_error = _require_device_access(conn, device_id, user or {})
            if access_error:
                return jsonify(access_error[0]), access_error[1]
            row = upsert_device_metadata_value(
                conn,
                device["guid"],
                parsed,
                value,
                modified_at=now_ts,
                source="engine",
                actor=actor,
            )
            insert_activity_history_row(
                conn,
                hostname=device["hostname"],
                script_path="Internal/Metadata_Fields",
                script_name=f"{metadata_field_label(parsed)} Metadata Update",
                script_type="metadata_fields",
                ran_at=now_ts,
                status="Success",
                stdout=f"{metadata_field_label(parsed)} {'cleared' if value == '' else 'updated'}.",
                stderr="",
                queue_lane="metadata_fields",
                activity_kind="metadata_field_update",
                metadata={
                    "device_guid": device["guid"],
                    "field_number": parsed,
                    "field_key": metadata_field_key(parsed),
                    "source": "engine",
                    "actor": actor,
                    "cleared": value == "",
                },
                started_at=now_ts,
                updated_at=now_ts,
                finished_at=now_ts,
            )
            conn.commit()
            definitions = {item["field_number"]: item for item in list_metadata_definitions(conn)}
        finally:
            conn.close()
        definition = definitions.get(parsed) or {}
        return jsonify(
            {
                "field": {
                    **definition,
                    **row,
                    "label": definition.get("description") or definition.get("default_label") or metadata_field_label(parsed),
                    "value": decode_metadata_value(row.get("value", "")),
                    "has_value": bool(value),
                }
            }
        )

    app.register_blueprint(blueprint)
