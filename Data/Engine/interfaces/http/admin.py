"""Administrative HTTP endpoints for the Borealis Engine."""

from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request, session

from Data.Engine.services.container import EngineServiceContainer


blueprint = Blueprint("engine_admin", __name__, url_prefix="/api/admin")


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach administrative routes to *app*."""

    if "engine_admin" not in app.blueprints:
        app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    services = current_app.extensions.get("engine_services")
    if services is None:  # pragma: no cover - defensive
        raise RuntimeError("engine services not initialized")
    return services


def _admin_service():
    return _services().enrollment_admin_service


def _require_admin():
    username = session.get("username")
    role = (session.get("role") or "").strip().lower()
    if not isinstance(username, str) or not username:
        return jsonify({"error": "not_authenticated"}), 401
    if role != "admin":
        return jsonify({"error": "forbidden"}), 403
    return None


@blueprint.route("/enrollment-codes", methods=["GET"])
def list_enrollment_codes() -> object:
    guard = _require_admin()
    if guard:
        return guard

    status = request.args.get("status")
    records = _admin_service().list_install_codes(status=status)
    return jsonify({"codes": [record.to_dict() for record in records]})


@blueprint.route("/enrollment-codes", methods=["POST"])
def create_enrollment_code() -> object:
    guard = _require_admin()
    if guard:
        return guard

    payload = request.get_json(silent=True) or {}

    ttl_value = payload.get("ttl_hours")
    if ttl_value is None:
        ttl_value = payload.get("ttl") or 1
    try:
        ttl_hours = int(ttl_value)
    except (TypeError, ValueError):
        ttl_hours = 1

    max_uses_value = payload.get("max_uses")
    if max_uses_value is None:
        max_uses_value = payload.get("allowed_uses", 2)
    try:
        max_uses = int(max_uses_value)
    except (TypeError, ValueError):
        max_uses = 2

    creator = session.get("username") if isinstance(session.get("username"), str) else None

    try:
        record = _admin_service().create_install_code(
            ttl_hours=ttl_hours,
            max_uses=max_uses,
            created_by=creator,
        )
    except ValueError as exc:
        if str(exc) == "invalid_ttl":
            return jsonify({"error": "invalid_ttl"}), 400
        raise

    response = jsonify(record.to_dict())
    response.status_code = 201
    return response


@blueprint.route("/enrollment-codes/<code_id>", methods=["DELETE"])
def delete_enrollment_code(code_id: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    if not _admin_service().delete_install_code(code_id):
        return jsonify({"error": "not_found"}), 404
    return jsonify({"status": "deleted"})


@blueprint.route("/device-approvals", methods=["GET"])
def list_device_approvals() -> object:
    guard = _require_admin()
    if guard:
        return guard

    status = request.args.get("status")
    records = _admin_service().list_device_approvals(status=status)
    return jsonify({"approvals": [record.to_dict() for record in records]})


@blueprint.route("/device-approvals/<approval_id>/approve", methods=["POST"])
def approve_device_approval(approval_id: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    payload = request.get_json(silent=True) or {}
    guid = payload.get("guid")
    resolution_raw = payload.get("conflict_resolution") or payload.get("resolution")
    resolution = resolution_raw.strip().lower() if isinstance(resolution_raw, str) else None

    actor = session.get("username") if isinstance(session.get("username"), str) else None

    try:
        result = _admin_service().approve_device_approval(
            approval_id,
            actor=actor,
            guid=guid,
            conflict_resolution=resolution,
        )
    except LookupError:
        return jsonify({"error": "not_found"}), 404
    except ValueError as exc:
        code = str(exc)
        if code == "approval_not_pending":
            return jsonify({"error": "approval_not_pending"}), 409
        if code == "conflict_resolution_required":
            return jsonify({"error": "conflict_resolution_required"}), 409
        if code == "invalid_guid":
            return jsonify({"error": "invalid_guid"}), 400
        raise

    response = jsonify(result.to_dict())
    response.status_code = 200
    return response


@blueprint.route("/device-approvals/<approval_id>/deny", methods=["POST"])
def deny_device_approval(approval_id: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    actor = session.get("username") if isinstance(session.get("username"), str) else None

    try:
        result = _admin_service().deny_device_approval(approval_id, actor=actor)
    except LookupError:
        return jsonify({"error": "not_found"}), 404
    except ValueError as exc:
        if str(exc) == "approval_not_pending":
            return jsonify({"error": "approval_not_pending"}), 409
        raise

    return jsonify(result.to_dict())


__all__ = ["register", "blueprint"]
