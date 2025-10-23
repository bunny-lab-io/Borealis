from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request

from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_sites", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_sites" not in app.blueprints:
        app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    services = current_app.extensions.get("engine_services")
    if services is None:  # pragma: no cover - defensive
        raise RuntimeError("engine services not initialized")
    return services


def _site_service():
    return _services().site_service


@blueprint.route("/api/sites", methods=["GET"])
def list_sites() -> object:
    records = _site_service().list_sites()
    return jsonify({"sites": [record.to_dict() for record in records]})


@blueprint.route("/api/sites", methods=["POST"])
def create_site() -> object:
    payload = request.get_json(silent=True) or {}
    name = payload.get("name")
    description = payload.get("description")
    try:
        record = _site_service().create_site(name or "", description or "")
    except ValueError as exc:
        if str(exc) == "missing_name":
            return jsonify({"error": "name is required"}), 400
        if str(exc) == "duplicate":
            return jsonify({"error": "name already exists"}), 409
        raise
    response = jsonify(record.to_dict())
    response.status_code = 201
    return response


@blueprint.route("/api/sites/delete", methods=["POST"])
def delete_sites() -> object:
    payload = request.get_json(silent=True) or {}
    ids = payload.get("ids") or []
    if not isinstance(ids, list):
        return jsonify({"error": "ids must be a list"}), 400
    deleted = _site_service().delete_sites(ids)
    return jsonify({"status": "ok", "deleted": deleted})


@blueprint.route("/api/sites/device_map", methods=["GET"])
def sites_device_map() -> object:
    host_param = (request.args.get("hostnames") or "").strip()
    filter_set = []
    if host_param:
        for part in host_param.split(","):
            normalized = part.strip()
            if normalized:
                filter_set.append(normalized)
    mapping = _site_service().map_devices(filter_set or None)
    return jsonify({"mapping": {hostname: entry.to_dict() for hostname, entry in mapping.items()}})


@blueprint.route("/api/sites/assign", methods=["POST"])
def assign_devices_to_site() -> object:
    payload = request.get_json(silent=True) or {}
    site_id = payload.get("site_id")
    hostnames = payload.get("hostnames") or []
    if not isinstance(hostnames, list):
        return jsonify({"error": "hostnames must be a list of strings"}), 400
    try:
        _site_service().assign_devices(site_id, hostnames)
    except ValueError as exc:
        message = str(exc)
        if message == "invalid_site_id":
            return jsonify({"error": "invalid site_id"}), 400
        if message == "invalid_hostnames":
            return jsonify({"error": "hostnames must be a list of strings"}), 400
        raise
    except LookupError:
        return jsonify({"error": "site not found"}), 404
    return jsonify({"status": "ok"})


@blueprint.route("/api/sites/rename", methods=["POST"])
def rename_site() -> object:
    payload = request.get_json(silent=True) or {}
    site_id = payload.get("id")
    new_name = payload.get("new_name") or ""
    try:
        record = _site_service().rename_site(site_id, new_name)
    except ValueError as exc:
        if str(exc) == "missing_name":
            return jsonify({"error": "new_name is required"}), 400
        if str(exc) == "duplicate":
            return jsonify({"error": "name already exists"}), 409
        raise
    except LookupError:
        return jsonify({"error": "site not found"}), 404
    return jsonify(record.to_dict())


__all__ = ["register", "blueprint"]
