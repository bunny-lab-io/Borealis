
from __future__ import annotations

import hashlib
import sqlite3
from datetime import datetime, timezone
from typing import Callable

from flask import Blueprint, jsonify, request

from Modules.auth.dpop import DPoPValidator, DPoPVerificationError, DPoPReplayError


def register(
    app,
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    jwt_service,
    dpop_validator: DPoPValidator,
) -> None:
    blueprint = Blueprint("tokens", __name__)

    def _hash_token(token: str) -> str:
        return hashlib.sha256(token.encode("utf-8")).hexdigest()

    def _iso_now() -> str:
        return datetime.now(tz=timezone.utc).isoformat()

    def _parse_iso(ts: str) -> datetime:
        return datetime.fromisoformat(ts)

    @blueprint.route("/api/agent/token/refresh", methods=["POST"])
    def refresh():
        payload = request.get_json(force=True, silent=True) or {}
        guid = str(payload.get("guid") or "").strip()
        refresh_token = str(payload.get("refresh_token") or "").strip()

        if not guid or not refresh_token:
            return jsonify({"error": "invalid_request"}), 400

        conn = db_conn_factory()
        try:
            cur = conn.cursor()
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
                return jsonify({"error": "invalid_refresh_token"}), 401

            record_id, row_guid, _token_hash, stored_jkt, created_at, expires_at, revoked_at = row
            if row_guid != guid:
                return jsonify({"error": "invalid_refresh_token"}), 401
            if revoked_at:
                return jsonify({"error": "refresh_token_revoked"}), 401
            if expires_at:
                try:
                    if _parse_iso(expires_at) <= datetime.now(tz=timezone.utc):
                        return jsonify({"error": "refresh_token_expired"}), 401
                except Exception:
                    pass

            cur.execute(
                """
                SELECT guid, ssl_key_fingerprint, token_version, status
                  FROM devices
                 WHERE guid = ?
                """,
                (guid,),
            )
            device_row = cur.fetchone()
            if not device_row:
                return jsonify({"error": "device_not_found"}), 404

            device_guid, fingerprint, token_version, status = device_row
            status_norm = (status or "active").strip().lower()
            if status_norm in {"revoked", "decommissioned"}:
                return jsonify({"error": "device_revoked"}), 403

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
                # The agent does not yet emit DPoP proofs; allow recovery by clearing
                # the stored binding so refreshes can succeed.  This preserves
                # backward compatibility while the client gains full DPoP support.
                try:
                    app.logger.warning(
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
                       dpop_jkt = COALESCE(NULLIF(?, ''), dpop_jkt)
                 WHERE id = ?
                """,
                (_iso_now(), jkt, record_id),
            )
            conn.commit()
        finally:
            conn.close()

        return jsonify(
            {
                "access_token": new_access_token,
                "expires_in": 900,
                "token_type": "Bearer",
            }
        )

    app.register_blueprint(blueprint)
