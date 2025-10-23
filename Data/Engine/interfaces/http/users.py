"""HTTP endpoints for operator account management."""

from __future__ import annotations

from flask import Blueprint, Flask, jsonify, request, session

from Data.Engine.services.auth import (
    AccountNotFoundError,
    CannotModifySelfError,
    InvalidPasswordHashError,
    InvalidRoleError,
    LastAdminError,
    LastUserError,
    OperatorAccountService,
    UsernameAlreadyExistsError,
)
from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_users", __name__)


def register(app: Flask, services: EngineServiceContainer) -> None:
    blueprint.services = services  # type: ignore[attr-defined]
    app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    svc = getattr(blueprint, "services", None)
    if svc is None:  # pragma: no cover - defensive
        raise RuntimeError("user blueprint not initialized")
    return svc


def _accounts() -> OperatorAccountService:
    return _services().operator_account_service


def _require_admin():
    username = session.get("username")
    role = (session.get("role") or "").strip().lower()
    if not isinstance(username, str) or not username:
        return jsonify({"error": "not_authenticated"}), 401
    if role != "admin":
        return jsonify({"error": "forbidden"}), 403
    return None


def _format_user(record) -> dict[str, object]:
    return {
        "username": record.username,
        "display_name": record.display_name,
        "role": record.role,
        "last_login": record.last_login,
        "created_at": record.created_at,
        "updated_at": record.updated_at,
        "mfa_enabled": 1 if record.mfa_enabled else 0,
    }


@blueprint.route("/api/users", methods=["GET"])
def list_users() -> object:
    guard = _require_admin()
    if guard:
        return guard

    records = _accounts().list_accounts()
    return jsonify({"users": [_format_user(record) for record in records]})


@blueprint.route("/api/users", methods=["POST"])
def create_user() -> object:
    guard = _require_admin()
    if guard:
        return guard

    payload = request.get_json(silent=True) or {}
    username = str(payload.get("username") or "").strip()
    password_sha512 = str(payload.get("password_sha512") or "").strip()
    role = str(payload.get("role") or "User")
    display_name = str(payload.get("display_name") or username)

    try:
        _accounts().create_account(
            username=username,
            password_sha512=password_sha512,
            role=role,
            display_name=display_name,
        )
    except UsernameAlreadyExistsError as exc:
        return jsonify({"error": str(exc)}), 409
    except (InvalidPasswordHashError, InvalidRoleError) as exc:
        return jsonify({"error": str(exc)}), 400

    return jsonify({"status": "ok"})


@blueprint.route("/api/users/<username>", methods=["DELETE"])
def delete_user(username: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    actor = session.get("username") if isinstance(session.get("username"), str) else None

    try:
        _accounts().delete_account(username, actor=actor)
    except CannotModifySelfError as exc:
        return jsonify({"error": str(exc)}), 400
    except LastUserError as exc:
        return jsonify({"error": str(exc)}), 400
    except LastAdminError as exc:
        return jsonify({"error": str(exc)}), 400
    except AccountNotFoundError as exc:
        return jsonify({"error": str(exc)}), 404

    return jsonify({"status": "ok"})


@blueprint.route("/api/users/<username>/reset_password", methods=["POST"])
def reset_password(username: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    payload = request.get_json(silent=True) or {}
    password_sha512 = str(payload.get("password_sha512") or "").strip()

    try:
        _accounts().reset_password(username, password_sha512)
    except InvalidPasswordHashError as exc:
        return jsonify({"error": str(exc)}), 400
    except AccountNotFoundError as exc:
        return jsonify({"error": str(exc)}), 404

    return jsonify({"status": "ok"})


@blueprint.route("/api/users/<username>/role", methods=["POST"])
def change_role(username: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    payload = request.get_json(silent=True) or {}
    role = str(payload.get("role") or "").strip()
    actor = session.get("username") if isinstance(session.get("username"), str) else None

    try:
        record = _accounts().change_role(username, role, actor=actor)
    except InvalidRoleError as exc:
        return jsonify({"error": str(exc)}), 400
    except LastAdminError as exc:
        return jsonify({"error": str(exc)}), 400
    except AccountNotFoundError as exc:
        return jsonify({"error": str(exc)}), 404

    if actor and actor.strip().lower() == username.strip().lower():
        session["role"] = record.role

    return jsonify({"status": "ok"})


@blueprint.route("/api/users/<username>/mfa", methods=["POST"])
def update_mfa(username: str) -> object:
    guard = _require_admin()
    if guard:
        return guard

    payload = request.get_json(silent=True) or {}
    enabled = bool(payload.get("enabled", False))
    reset_secret = bool(payload.get("reset_secret", False))

    try:
        _accounts().update_mfa(username, enabled=enabled, reset_secret=reset_secret)
    except AccountNotFoundError as exc:
        return jsonify({"error": str(exc)}), 404

    actor = session.get("username") if isinstance(session.get("username"), str) else None
    if actor and actor.strip().lower() == username.strip().lower() and not enabled:
        session.pop("mfa_pending", None)

    return jsonify({"status": "ok"})


__all__ = ["register", "blueprint"]
