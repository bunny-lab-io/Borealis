from __future__ import annotations

import json
import time
import sqlite3
from typing import Any, Callable, Dict, Optional

from flask import Blueprint, jsonify, request, g

from Modules.auth.device_auth import DeviceAuthManager, require_device_auth
from Modules.crypto.signing import ScriptSigner
from Modules.guid_utils import normalize_guid

AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


def _canonical_context(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    cleaned = "".join(ch for ch in str(value) if ch.isalnum() or ch in ("_", "-"))
    if not cleaned:
        return None
    return cleaned.upper()


def register(
    app,
    *,
    db_conn_factory: Callable[[], Any],
    auth_manager: DeviceAuthManager,
    log: Callable[[str, str, Optional[str]], None],
    script_signer: ScriptSigner,
) -> None:
    blueprint = Blueprint("agents", __name__)

    def _json_or_none(value) -> Optional[str]:
        if value is None:
            return None
        try:
            return json.dumps(value)
        except Exception:
            return None

    def _context_hint(ctx=None) -> Optional[str]:
        if ctx is not None and getattr(ctx, "service_mode", None):
            return _canonical_context(getattr(ctx, "service_mode", None))
        return _canonical_context(request.headers.get(AGENT_CONTEXT_HEADER))

    def _auth_context():
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            log("server", f"device auth context missing for {request.path}", _context_hint())
        return ctx

    @blueprint.route("/api/agent/heartbeat", methods=["POST"])
    @require_device_auth(auth_manager)
    def heartbeat():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        payload = request.get_json(force=True, silent=True) or {}
        context_label = _context_hint(ctx)

        now_ts = int(time.time())
        updates: Dict[str, Optional[str]] = {"last_seen": now_ts}

        hostname = payload.get("hostname")
        if isinstance(hostname, str) and hostname.strip():
            updates["hostname"] = hostname.strip()

        inventory = payload.get("inventory") if isinstance(payload.get("inventory"), dict) else {}
        for key in ("memory", "network", "software", "storage", "cpu"):
            if key in inventory and inventory[key] is not None:
                encoded = _json_or_none(inventory[key])
                if encoded is not None:
                    updates[key] = encoded

        metrics = payload.get("metrics") if isinstance(payload.get("metrics"), dict) else {}
        def _maybe_str(field: str) -> Optional[str]:
            val = metrics.get(field)
            if isinstance(val, str):
                return val.strip()
            return None

        if "last_user" in metrics and metrics["last_user"]:
            updates["last_user"] = str(metrics["last_user"])
        if "operating_system" in metrics and metrics["operating_system"]:
            updates["operating_system"] = str(metrics["operating_system"])
        if "uptime" in metrics and metrics["uptime"] is not None:
            try:
                updates["uptime"] = int(metrics["uptime"])
            except Exception:
                pass
        for field in ("external_ip", "internal_ip", "device_type"):
            if field in payload and payload[field]:
                updates[field] = str(payload[field])

        conn = db_conn_factory()
        try:
            cur = conn.cursor()

            def _apply_updates() -> int:
                if not updates:
                    return 0
                columns = ", ".join(f"{col} = ?" for col in updates.keys())
                values = list(updates.values())
                normalized_guid = normalize_guid(ctx.guid)
                selected_guid: Optional[str] = None
                if normalized_guid:
                    cur.execute(
                        "SELECT guid FROM devices WHERE UPPER(guid) = ?",
                        (normalized_guid,),
                    )
                    rows = cur.fetchall()
                    for (stored_guid,) in rows or []:
                        if stored_guid == ctx.guid:
                            selected_guid = stored_guid
                            break
                    if not selected_guid and rows:
                        selected_guid = rows[0][0]
                target_guid = selected_guid or ctx.guid
                cur.execute(
                    f"UPDATE devices SET {columns} WHERE guid = ?",
                    values + [target_guid],
                )
                updated = cur.rowcount
                if updated > 0 and normalized_guid and target_guid != normalized_guid:
                    try:
                        cur.execute(
                            "UPDATE devices SET guid = ? WHERE guid = ?",
                            (normalized_guid, target_guid),
                        )
                    except sqlite3.IntegrityError:
                        pass
                return updated

            try:
                rowcount = _apply_updates()
            except sqlite3.IntegrityError as exc:
                if "devices.hostname" in str(exc) and "UNIQUE" in str(exc).upper():
                    # Another device already claims this hostname; keep the existing
                    # canonical hostname assigned during enrollment to avoid breaking
                    # the unique constraint and continue updating the remaining fields.
                    if "hostname" in updates:
                        updates.pop("hostname", None)
                    try:
                        rowcount = _apply_updates()
                    except sqlite3.IntegrityError:
                        raise
                    else:
                        log(
                            "server",
                            "heartbeat hostname collision ignored for guid="
                            f"{ctx.guid}",
                            context_label,
                        )
                else:
                    raise

            if rowcount == 0:
                log("server", f"heartbeat missing device record guid={ctx.guid}", context_label)
                return jsonify({"error": "device_not_registered"}), 404
            conn.commit()
        finally:
            conn.close()

        return jsonify({"status": "ok", "poll_after_ms": 15000})

    @blueprint.route("/api/agent/script/request", methods=["POST"])
    @require_device_auth(auth_manager)
    def script_request():
        ctx = _auth_context()
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        if ctx.status != "active":
            return jsonify(
                {
                    "status": "quarantined",
                    "poll_after_ms": 60000,
                    "sig_alg": "ed25519",
                    "signing_key": script_signer.public_base64_spki(),
                }
            )

        # Placeholder: actual dispatch logic will integrate with job scheduler.
        return jsonify(
            {
                "status": "idle",
                "poll_after_ms": 30000,
                "sig_alg": "ed25519",
                "signing_key": script_signer.public_base64_spki(),
            }
        )

    app.register_blueprint(blueprint)
