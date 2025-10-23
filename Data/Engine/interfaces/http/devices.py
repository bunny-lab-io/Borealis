from __future__ import annotations

from ipaddress import ip_address

from flask import Blueprint, Flask, current_app, jsonify, request, session

from Data.Engine.services.container import EngineServiceContainer
from Data.Engine.services.devices import DeviceDescriptionError, RemoteDeviceError

blueprint = Blueprint("engine_devices", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_devices" not in app.blueprints:
        app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    services = current_app.extensions.get("engine_services")
    if services is None:  # pragma: no cover - defensive
        raise RuntimeError("engine services not initialized")
    return services


def _inventory():
    return _services().device_inventory


def _views():
    return _services().device_view_service


def _require_admin():
    username = session.get("username")
    role = (session.get("role") or "").strip().lower()
    if not isinstance(username, str) or not username:
        return jsonify({"error": "not_authenticated"}), 401
    if role != "admin":
        return jsonify({"error": "forbidden"}), 403
    return None


def _is_internal_request(req: request) -> bool:
    remote = (req.remote_addr or "").strip()
    if not remote:
        return False
    try:
        return ip_address(remote).is_loopback
    except ValueError:
        return remote in {"localhost"}


@blueprint.route("/api/devices", methods=["GET"])
def list_devices() -> object:
    devices = _inventory().list_devices()
    return jsonify({"devices": devices})


@blueprint.route("/api/devices/<guid>", methods=["GET"])
def get_device_by_guid(guid: str) -> object:
    device = _inventory().get_device_by_guid(guid)
    if not device:
        return jsonify({"error": "not found"}), 404
    return jsonify(device)


@blueprint.route("/api/device/details/<hostname>", methods=["GET"])
def get_device_details(hostname: str) -> object:
    payload = _inventory().get_device_details(hostname)
    return jsonify(payload)


@blueprint.route("/api/device/description/<hostname>", methods=["POST"])
def set_device_description(hostname: str) -> object:
    payload = request.get_json(silent=True) or {}
    description = payload.get("description")
    try:
        _inventory().update_device_description(hostname, description)
    except DeviceDescriptionError as exc:
        if exc.code == "invalid_hostname":
            return jsonify({"error": "invalid hostname"}), 400
        if exc.code == "not_found":
            return jsonify({"error": "not found"}), 404
        current_app.logger.exception(
            "device-description-error host=%s code=%s", hostname, exc.code
        )
        return jsonify({"error": "internal error"}), 500
    return jsonify({"status": "ok"})


@blueprint.route("/api/agent_devices", methods=["GET"])
def list_agent_devices() -> object:
    guard = _require_admin()
    if guard:
        return guard
    devices = _inventory().list_agent_devices()
    return jsonify({"devices": devices})


@blueprint.route("/api/ssh_devices", methods=["GET", "POST"])
def ssh_devices() -> object:
    return _remote_devices_endpoint("ssh")


@blueprint.route("/api/winrm_devices", methods=["GET", "POST"])
def winrm_devices() -> object:
    return _remote_devices_endpoint("winrm")


@blueprint.route("/api/ssh_devices/<hostname>", methods=["PUT", "DELETE"])
def ssh_device_detail(hostname: str) -> object:
    return _remote_device_detail("ssh", hostname)


@blueprint.route("/api/winrm_devices/<hostname>", methods=["PUT", "DELETE"])
def winrm_device_detail(hostname: str) -> object:
    return _remote_device_detail("winrm", hostname)


@blueprint.route("/api/agent/hash_list", methods=["GET"])
def agent_hash_list() -> object:
    if not _is_internal_request(request):
        remote_addr = (request.remote_addr or "unknown").strip() or "unknown"
        current_app.logger.warning(
            "/api/agent/hash_list denied non-local request from %s", remote_addr
        )
        return jsonify({"error": "forbidden"}), 403
    try:
        records = _inventory().collect_agent_hash_records()
    except Exception as exc:  # pragma: no cover - defensive logging
        current_app.logger.exception("/api/agent/hash_list error: %s", exc)
        return jsonify({"error": "internal error"}), 500
    return jsonify({"agents": records})


@blueprint.route("/api/device_list_views", methods=["GET"])
def list_device_list_views() -> object:
    views = _views().list_views()
    return jsonify({"views": [view.to_dict() for view in views]})


@blueprint.route("/api/device_list_views/<int:view_id>", methods=["GET"])
def get_device_list_view(view_id: int) -> object:
    view = _views().get_view(view_id)
    if not view:
        return jsonify({"error": "not found"}), 404
    return jsonify(view.to_dict())


@blueprint.route("/api/device_list_views", methods=["POST"])
def create_device_list_view() -> object:
    payload = request.get_json(silent=True) or {}
    name = (payload.get("name") or "").strip()
    columns = payload.get("columns") or []
    filters = payload.get("filters") or {}

    if not name:
        return jsonify({"error": "name is required"}), 400
    if name.lower() == "default view":
        return jsonify({"error": "reserved name"}), 400
    if not isinstance(columns, list) or not all(isinstance(x, str) for x in columns):
        return jsonify({"error": "columns must be a list of strings"}), 400
    if not isinstance(filters, dict):
        return jsonify({"error": "filters must be an object"}), 400

    try:
        view = _views().create_view(name, columns, filters)
    except ValueError as exc:
        if str(exc) == "duplicate":
            return jsonify({"error": "name already exists"}), 409
        raise
    response = jsonify(view.to_dict())
    response.status_code = 201
    return response


@blueprint.route("/api/device_list_views/<int:view_id>", methods=["PUT"])
def update_device_list_view(view_id: int) -> object:
    payload = request.get_json(silent=True) or {}
    updates: dict = {}
    if "name" in payload:
        name_val = payload.get("name")
        if name_val is None:
            return jsonify({"error": "name cannot be empty"}), 400
        normalized = (str(name_val) or "").strip()
        if not normalized:
            return jsonify({"error": "name cannot be empty"}), 400
        if normalized.lower() == "default view":
            return jsonify({"error": "reserved name"}), 400
        updates["name"] = normalized
    if "columns" in payload:
        columns_val = payload.get("columns")
        if not isinstance(columns_val, list) or not all(isinstance(x, str) for x in columns_val):
            return jsonify({"error": "columns must be a list of strings"}), 400
        updates["columns"] = columns_val
    if "filters" in payload:
        filters_val = payload.get("filters")
        if filters_val is not None and not isinstance(filters_val, dict):
            return jsonify({"error": "filters must be an object"}), 400
        if filters_val is not None:
            updates["filters"] = filters_val
    if not updates:
        return jsonify({"error": "no fields to update"}), 400

    try:
        view = _views().update_view(
            view_id,
            name=updates.get("name"),
            columns=updates.get("columns"),
            filters=updates.get("filters"),
        )
    except ValueError as exc:
        code = str(exc)
        if code == "duplicate":
            return jsonify({"error": "name already exists"}), 409
        if code == "missing_name":
            return jsonify({"error": "name cannot be empty"}), 400
        if code == "reserved":
            return jsonify({"error": "reserved name"}), 400
        return jsonify({"error": "invalid payload"}), 400
    except LookupError:
        return jsonify({"error": "not found"}), 404
    return jsonify(view.to_dict())


@blueprint.route("/api/device_list_views/<int:view_id>", methods=["DELETE"])
def delete_device_list_view(view_id: int) -> object:
    if not _views().delete_view(view_id):
        return jsonify({"error": "not found"}), 404
    return jsonify({"status": "ok"})


def _remote_devices_endpoint(connection_type: str) -> object:
    guard = _require_admin()
    if guard:
        return guard
    if request.method == "GET":
        devices = _inventory().list_remote_devices(connection_type)
        return jsonify({"devices": devices})

    payload = request.get_json(silent=True) or {}
    hostname = (payload.get("hostname") or "").strip()
    address = (
        payload.get("address")
        or payload.get("connection_endpoint")
        or payload.get("endpoint")
        or payload.get("host")
    )
    description = payload.get("description")
    os_hint = payload.get("operating_system") or payload.get("os")

    if not hostname:
        return jsonify({"error": "hostname is required"}), 400
    if not (address or "").strip():
        return jsonify({"error": "address is required"}), 400

    try:
        device = _inventory().upsert_remote_device(
            connection_type,
            hostname,
            address,
            description,
            os_hint,
            ensure_existing_type=None,
        )
    except RemoteDeviceError as exc:
        status = 409 if exc.code in {"conflict", "address_required"} else 500
        if exc.code == "conflict":
            return jsonify({"error": str(exc)}), 409
        if exc.code == "address_required":
            return jsonify({"error": "address is required"}), 400
        return jsonify({"error": str(exc)}), status
    return jsonify({"device": device}), 201


def _remote_device_detail(connection_type: str, hostname: str) -> object:
    guard = _require_admin()
    if guard:
        return guard
    normalized_host = (hostname or "").strip()
    if not normalized_host:
        return jsonify({"error": "invalid hostname"}), 400

    if request.method == "DELETE":
        try:
            _inventory().delete_remote_device(connection_type, normalized_host)
        except RemoteDeviceError as exc:
            if exc.code == "not_found":
                return jsonify({"error": "device not found"}), 404
            if exc.code == "invalid_hostname":
                return jsonify({"error": "invalid hostname"}), 400
            return jsonify({"error": str(exc)}), 500
        return jsonify({"status": "ok"})

    payload = request.get_json(silent=True) or {}
    address = (
        payload.get("address")
        or payload.get("connection_endpoint")
        or payload.get("endpoint")
    )
    description = payload.get("description")
    os_hint = payload.get("operating_system") or payload.get("os")

    if address is None and description is None and os_hint is None:
        return jsonify({"error": "no fields to update"}), 400

    try:
        device = _inventory().upsert_remote_device(
            connection_type,
            normalized_host,
            address if address is not None else "",
            description,
            os_hint,
            ensure_existing_type=connection_type,
        )
    except RemoteDeviceError as exc:
        if exc.code == "not_found":
            return jsonify({"error": "device not found"}), 404
        if exc.code == "address_required":
            return jsonify({"error": "address is required"}), 400
        return jsonify({"error": str(exc)}), 500
    return jsonify({"device": device})


__all__ = ["register", "blueprint"]
