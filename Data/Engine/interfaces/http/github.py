"""GitHub-related HTTP endpoints."""

from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request

from Data.Engine.services.container import EngineServiceContainer

blueprint = Blueprint("engine_github", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_github" not in app.blueprints:
        app.register_blueprint(blueprint)


@blueprint.route("/api/repo/current_hash", methods=["GET"])
def repo_current_hash() -> object:
    services: EngineServiceContainer = current_app.extensions["engine_services"]
    github = services.github_service

    repo = (request.args.get("repo") or "").strip() or None
    branch = (request.args.get("branch") or "").strip() or None
    refresh_flag = (request.args.get("refresh") or "").strip().lower()
    ttl_raw = request.args.get("ttl")
    try:
        ttl = int(ttl_raw) if ttl_raw else github.default_refresh_interval
    except ValueError:
        ttl = github.default_refresh_interval
    force_refresh = refresh_flag in {"1", "true", "yes", "force", "refresh"}

    snapshot = github.get_repo_head(repo, branch, ttl_seconds=ttl, force_refresh=force_refresh)
    payload = snapshot.to_dict()
    if not snapshot.sha:
        return jsonify(payload), 503
    return jsonify(payload)


@blueprint.route("/api/github/token", methods=["GET", "POST"])
def github_token() -> object:
    services: EngineServiceContainer = current_app.extensions["engine_services"]
    github = services.github_service

    if request.method == "GET":
        payload = github.get_token_status(force_refresh=True).to_dict()
        return jsonify(payload)

    data = request.get_json(silent=True) or {}
    token = data.get("token")
    normalized = str(token).strip() if token is not None else ""
    try:
        payload = github.update_token(normalized).to_dict()
    except Exception as exc:  # pragma: no cover - defensive logging
        current_app.logger.exception("failed to store GitHub token: %s", exc)
        return jsonify({"error": f"Failed to store token: {exc}"}), 500
    return jsonify(payload)


__all__ = ["register", "blueprint", "repo_current_hash", "github_token"]

