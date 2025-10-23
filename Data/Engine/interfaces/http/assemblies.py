"""HTTP endpoints for assembly management."""

from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request

from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_assemblies", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_assemblies" not in app.blueprints:
        app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    services = current_app.extensions.get("engine_services")
    if services is None:  # pragma: no cover - defensive
        raise RuntimeError("engine services not initialized")
    return services


def _assembly_service():
    return _services().assembly_service


def _value_error_response(exc: ValueError):
    code = str(exc)
    if code == "invalid_island":
        return jsonify({"error": "invalid island"}), 400
    if code == "path_required":
        return jsonify({"error": "path required"}), 400
    if code == "invalid_kind":
        return jsonify({"error": "invalid kind"}), 400
    if code == "invalid_destination":
        return jsonify({"error": "invalid destination"}), 400
    if code == "invalid_path":
        return jsonify({"error": "invalid path"}), 400
    if code == "cannot_delete_root":
        return jsonify({"error": "cannot delete root"}), 400
    return jsonify({"error": code or "invalid request"}), 400


def _not_found_response(exc: FileNotFoundError):
    code = str(exc)
    if code == "file_not_found":
        return jsonify({"error": "file not found"}), 404
    if code == "folder_not_found":
        return jsonify({"error": "folder not found"}), 404
    return jsonify({"error": "not found"}), 404


@blueprint.route("/api/assembly/list", methods=["GET"])
def list_assemblies() -> object:
    island = (request.args.get("island") or "").strip()
    try:
        listing = _assembly_service().list_items(island)
    except ValueError as exc:
        return _value_error_response(exc)
    return jsonify(listing.to_dict())


@blueprint.route("/api/assembly/load", methods=["GET"])
def load_assembly() -> object:
    island = (request.args.get("island") or "").strip()
    rel_path = (request.args.get("path") or "").strip()
    try:
        result = _assembly_service().load_item(island, rel_path)
    except ValueError as exc:
        return _value_error_response(exc)
    except FileNotFoundError as exc:
        return _not_found_response(exc)
    return jsonify(result.to_dict())


@blueprint.route("/api/assembly/create", methods=["POST"])
def create_assembly() -> object:
    payload = request.get_json(silent=True) or {}
    island = (payload.get("island") or "").strip()
    kind = (payload.get("kind") or "").strip().lower()
    rel_path = (payload.get("path") or "").strip()
    content = payload.get("content")
    item_type = payload.get("type")
    try:
        result = _assembly_service().create_item(
            island,
            kind=kind,
            rel_path=rel_path,
            content=content,
            item_type=item_type if isinstance(item_type, str) else None,
        )
    except ValueError as exc:
        return _value_error_response(exc)
    return jsonify(result.to_dict())


@blueprint.route("/api/assembly/edit", methods=["POST"])
def edit_assembly() -> object:
    payload = request.get_json(silent=True) or {}
    island = (payload.get("island") or "").strip()
    rel_path = (payload.get("path") or "").strip()
    content = payload.get("content")
    item_type = payload.get("type")
    try:
        result = _assembly_service().edit_item(
            island,
            rel_path=rel_path,
            content=content,
            item_type=item_type if isinstance(item_type, str) else None,
        )
    except ValueError as exc:
        return _value_error_response(exc)
    except FileNotFoundError as exc:
        return _not_found_response(exc)
    return jsonify(result.to_dict())


@blueprint.route("/api/assembly/rename", methods=["POST"])
def rename_assembly() -> object:
    payload = request.get_json(silent=True) or {}
    island = (payload.get("island") or "").strip()
    kind = (payload.get("kind") or "").strip().lower()
    rel_path = (payload.get("path") or "").strip()
    new_name = (payload.get("new_name") or "").strip()
    item_type = payload.get("type")
    try:
        result = _assembly_service().rename_item(
            island,
            kind=kind,
            rel_path=rel_path,
            new_name=new_name,
            item_type=item_type if isinstance(item_type, str) else None,
        )
    except ValueError as exc:
        return _value_error_response(exc)
    except FileNotFoundError as exc:
        return _not_found_response(exc)
    return jsonify(result.to_dict())


@blueprint.route("/api/assembly/move", methods=["POST"])
def move_assembly() -> object:
    payload = request.get_json(silent=True) or {}
    island = (payload.get("island") or "").strip()
    rel_path = (payload.get("path") or "").strip()
    new_path = (payload.get("new_path") or "").strip()
    kind = (payload.get("kind") or "").strip().lower()
    try:
        result = _assembly_service().move_item(
            island,
            rel_path=rel_path,
            new_path=new_path,
            kind=kind,
        )
    except ValueError as exc:
        return _value_error_response(exc)
    except FileNotFoundError as exc:
        return _not_found_response(exc)
    return jsonify(result.to_dict())


@blueprint.route("/api/assembly/delete", methods=["POST"])
def delete_assembly() -> object:
    payload = request.get_json(silent=True) or {}
    island = (payload.get("island") or "").strip()
    rel_path = (payload.get("path") or "").strip()
    kind = (payload.get("kind") or "").strip().lower()
    try:
        result = _assembly_service().delete_item(
            island,
            rel_path=rel_path,
            kind=kind,
        )
    except ValueError as exc:
        return _value_error_response(exc)
    except FileNotFoundError as exc:
        return _not_found_response(exc)
    return jsonify(result.to_dict())


__all__ = ["register", "blueprint"]
