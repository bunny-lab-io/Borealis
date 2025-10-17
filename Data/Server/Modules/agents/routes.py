from __future__ import annotations

import json
import time
from typing import Any, Callable, Dict, Optional

from flask import Blueprint, jsonify, request, g

from Modules.auth.device_auth import DeviceAuthManager, require_device_auth
from Modules.crypto.signing import ScriptSigner


def register(
    app,
    *,
    db_conn_factory: Callable[[], Any],
    auth_manager: DeviceAuthManager,
    log: Callable[[str, str], None],
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

    @blueprint.route("/api/agent/heartbeat", methods=["POST"])
    @require_device_auth(auth_manager)
    def heartbeat():
        ctx = getattr(g, "device_auth")
        payload = request.get_json(force=True, silent=True) or {}

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
            columns = ", ".join(f"{col} = ?" for col in updates.keys())
            params = list(updates.values())
            params.append(ctx.guid)
            cur.execute(
                f"UPDATE devices SET {columns} WHERE guid = ?",
                params,
            )
            if cur.rowcount == 0:
                log("server", f"heartbeat missing device record guid={ctx.guid}")
                return jsonify({"error": "device_not_registered"}), 404
            conn.commit()
        finally:
            conn.close()

        return jsonify({"status": "ok", "poll_after_ms": 15000})

    @blueprint.route("/api/agent/script/request", methods=["POST"])
    @require_device_auth(auth_manager)
    def script_request():
        ctx = getattr(g, "device_auth")
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
