# ======================================================
# Data\Engine\services\API\server\info.py
# Description: Server information endpoints surfaced for administrative UX.
#
# API Endpoints (if applicable):
# - GET /api/server/time (Operator Session) - Returns the server clock in multiple formats.
# - GET /api/server/certificates/root (Operator Session) - Downloads the Borealis root CA certificate.
# ======================================================

from __future__ import annotations

from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, Optional

from flask import Blueprint, Flask, jsonify, send_file

from ...auth import RequestAuthContext
from ....security import certificates

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def _serialize_time(now_local: datetime, now_utc: datetime) -> Dict[str, Any]:
    tz_label = now_local.tzname()
    display = now_local.strftime("%Y-%m-%d %H:%M:%S %Z").strip()
    if not display:
        display = now_local.isoformat()
    return {
        "epoch": int(now_local.timestamp()),
        "iso": now_local.isoformat(),
        "utc": now_utc.isoformat(),
        "timezone": tz_label,
        "display": display,
    }


def _resolve_root_ca_path(adapters: "EngineServiceAdapters") -> Optional[Path]:
    candidates = []
    try:
        candidates.append(certificates.engine_certificates_root() / "borealis-root-ca.pem")
    except Exception:
        pass

    cert_path = getattr(adapters.context, "tls_cert_path", None)
    if cert_path:
        try:
            candidates.append(Path(str(cert_path)).expanduser().resolve().parent / "borealis-root-ca.pem")
        except Exception:
            candidates.append(Path(str(cert_path)).parent / "borealis-root-ca.pem")

    for candidate in candidates:
        try:
            if candidate and candidate.is_file():
                return candidate
        except Exception:
            continue
    return None


def register_info(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Expose server telemetry endpoints used by the admin interface."""

    blueprint = Blueprint("engine_server_info", __name__)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
    )

    @blueprint.route("/api/server/time", methods=["GET"])
    def server_time() -> Any:
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        now_utc = datetime.now(timezone.utc)
        now_local = now_utc.astimezone()
        payload = _serialize_time(now_local, now_utc)
        return jsonify(payload)

    @blueprint.route("/api/server/certificates/root", methods=["GET"])
    def server_root_ca() -> Any:
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        path = _resolve_root_ca_path(adapters)
        if not path:
            return jsonify({"error": "root_ca_missing"}), 404
        return send_file(
            str(path),
            mimetype="application/x-pem-file",
            as_attachment=True,
            download_name="borealis-root-ca.pem",
            max_age=0,
        )

    app.register_blueprint(blueprint)
