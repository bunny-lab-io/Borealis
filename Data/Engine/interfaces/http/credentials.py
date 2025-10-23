from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request, session

from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_credentials", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_credentials" not in app.blueprints:
        app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    services = current_app.extensions.get("engine_services")
    if services is None:  # pragma: no cover - defensive
        raise RuntimeError("engine services not initialized")
    return services


def _credentials_service():
    return _services().credential_service


def _require_admin():
    username = session.get("username")
    role = (session.get("role") or "").strip().lower()
    if not isinstance(username, str) or not username:
        return jsonify({"error": "not_authenticated"}), 401
    if role != "admin":
        return jsonify({"error": "forbidden"}), 403
    return None


@blueprint.route("/api/credentials", methods=["GET"])
def list_credentials() -> object:
    guard = _require_admin()
    if guard:
        return guard

    site_id_param = request.args.get("site_id")
    connection_type = (request.args.get("connection_type") or "").strip() or None
    try:
        site_id = int(site_id_param) if site_id_param not in (None, "") else None
    except (TypeError, ValueError):
        site_id = None

    records = _credentials_service().list_credentials(
        site_id=site_id,
        connection_type=connection_type,
    )
    return jsonify({"credentials": records})


@blueprint.route("/api/credentials", methods=["POST"])
def create_credential() -> object:  # pragma: no cover - placeholder
    return jsonify({"error": "not implemented"}), 501


@blueprint.route("/api/credentials/<int:credential_id>", methods=["GET", "PUT", "DELETE"])
def credential_detail(credential_id: int) -> object:  # pragma: no cover - placeholder
    if request.method == "GET":
        return jsonify({"error": "not implemented"}), 501
    if request.method == "DELETE":
        return jsonify({"error": "not implemented"}), 501
    return jsonify({"error": "not implemented"}), 501


__all__ = ["register", "blueprint"]
