# ======================================================
# Data\Engine\services\API\tokens\routes.py
# Description: Engine-native refresh token endpoints decoupled from legacy server modules.
#
# API Endpoints (if applicable):
# - POST /api/agent/token/refresh (Authenticated via refresh token) - Issues a new access token.
# ======================================================

"""Token management routes backed by the Engine authentication stack."""

from __future__ import annotations

import hashlib
from Data.Engine.db import dbapi as sqlite3
from datetime import datetime, timezone, timedelta
from typing import Any, Callable, Dict, Optional

from flask import Blueprint, current_app, jsonify, request

from ....auth import device_purge_state
from ....auth.dpop import DPoPReplayError, DPoPValidator, DPoPVerificationError
from ....auth.guid_utils import normalize_guid
from ...remote_ops.agent_routes import build_agent_remote_ops_route_payload, fetch_active_site_worker_route


def register(
    app,
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    jwt_service,
    dpop_validator: DPoPValidator,
) -> None:
    blueprint = Blueprint("tokens", __name__)
    REFRESH_TOKEN_TTL_DAYS = 90

    def _hash_token(token: str) -> str:
        return hashlib.sha256(token.encode("utf-8")).hexdigest()

    def _iso(dt: datetime) -> str:
        return dt.astimezone(timezone.utc).isoformat()

    def _iso_now() -> str:
        return datetime.now(tz=timezone.utc).isoformat()

    def _parse_iso(ts: str) -> datetime:
        return datetime.fromisoformat(ts)

    @blueprint.route("/api/agent/token/refresh", methods=["POST"])
    def refresh():
        payload = request.get_json(force=True, silent=True) or {}
        guid = normalize_guid(str(payload.get("guid") or "").strip())
        refresh_token = str(payload.get("refresh_token") or "").strip()

        if not guid or not refresh_token:
            return jsonify({"error": "invalid_request"}), 400

        remote_ops_site_id: Optional[int] = None
        remote_ops_route: Optional[Dict[str, Any]] = None
        remote_ops_reason = "site_worker_unavailable"
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            if device_purge_state.get_required_token_version(cur, guid) is not None:
                return jsonify({"error": "device_purged"}), 401
            cur.execute(
                """
                SELECT id, guid, token_hash, dpop_jkt, created_at, expires_at, revoked_at
                  FROM refresh_tokens
                 WHERE guid = ?
                   AND token_hash = ?
                """,
                (guid, _hash_token(refresh_token)),
            )
            row = cur.fetchone()
            if not row:
                try:
                    current_app.logger.warning("Refresh token lookup failed for guid=%s", guid)
                except Exception:
                    pass
                return jsonify({"error": "invalid_refresh_token"}), 401

            record_id, row_guid, _token_hash, stored_jkt, created_at, expires_at, revoked_at = row
            if normalize_guid(row_guid) != guid:
                return jsonify({"error": "invalid_refresh_token"}), 401
            if revoked_at:
                return jsonify({"error": "refresh_token_revoked"}), 401
            if expires_at:
                try:
                    parsed_expiry = _parse_iso(expires_at)
                    if parsed_expiry <= datetime.now(tz=timezone.utc):
                        return jsonify({"error": "refresh_token_expired"}), 401
                except Exception:
                    pass

            cur.execute(
                """
                SELECT d.guid, d.ssl_key_fingerprint, d.token_version, d.status, d.hostname, ds.site_id
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 WHERE d.guid = ?
                """,
                (guid,),
            )
            device_row = cur.fetchone()
            if not device_row:
                return jsonify({"error": "device_not_found"}), 404

            device_guid, fingerprint, token_version, status, _hostname, site_id = device_row
            status_norm = (status or "active").strip().lower()
            if status_norm in {"revoked", "decommissioned"}:
                return jsonify({"error": "device_revoked"}), 403
            try:
                remote_ops_site_id = int(site_id) if site_id is not None else None
            except Exception:
                remote_ops_site_id = None
            if remote_ops_site_id is None:
                remote_ops_reason = "device_site_unassigned"
            else:
                remote_ops_route = fetch_active_site_worker_route(conn, site_id=remote_ops_site_id)

            dpop_proof = request.headers.get("DPoP")
            jkt = stored_jkt or ""
            if dpop_proof:
                try:
                    jkt = dpop_validator.verify(request.method, request.url, dpop_proof, access_token=None)
                except DPoPReplayError:
                    return jsonify({"error": "dpop_replayed"}), 400
                except DPoPVerificationError:
                    return jsonify({"error": "dpop_invalid"}), 400
            elif stored_jkt:
                try:
                    current_app.logger.warning(
                        "Clearing stored DPoP binding for guid=%s due to missing proof",
                        guid,
                    )
                except Exception:
                    pass
                cur.execute(
                    "UPDATE refresh_tokens SET dpop_jkt = NULL WHERE id = ?",
                    (record_id,),
                )

            new_access_token = jwt_service.issue_access_token(
                guid,
                fingerprint or "",
                token_version or 1,
            )

            cur.execute(
                """
                UPDATE refresh_tokens
                   SET last_used_at = ?,
                       expires_at = ?,
                       dpop_jkt = COALESCE(NULLIF(?, ''), dpop_jkt)
                 WHERE id = ?
                """,
                (
                    _iso_now(),
                    _iso(datetime.now(tz=timezone.utc) + timedelta(days=REFRESH_TOKEN_TTL_DAYS)),
                    jkt,
                    record_id,
                ),
            )
            conn.commit()
        finally:
            conn.close()

        remote_ops_payload = build_agent_remote_ops_route_payload(
            app,
            request,
            site_id=remote_ops_site_id,
            route=remote_ops_route,
            reason=remote_ops_reason,
        )
        return jsonify(
            {
                "access_token": new_access_token,
                "expires_in": 900,
                "token_type": "Bearer",
                "remote_ops_route": remote_ops_payload,
            }
        )

    app.register_blueprint(blueprint)


__all__ = ["register"]
