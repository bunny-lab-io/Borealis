"""HTTP routes for Engine job management."""

from __future__ import annotations

from typing import Any, Optional

from flask import Blueprint, Flask, jsonify, request

from Data.Engine.services.container import EngineServiceContainer

bp = Blueprint("engine_job_management", __name__)


def register(app: Flask, services: EngineServiceContainer) -> None:
    bp.services = services  # type: ignore[attr-defined]
    app.register_blueprint(bp)


def _services() -> EngineServiceContainer:
    svc = getattr(bp, "services", None)
    if svc is None:  # pragma: no cover - guard
        raise RuntimeError("job management blueprint not initialized")
    return svc


@bp.route("/api/scheduled_jobs", methods=["GET"])
def list_jobs() -> Any:
    jobs = _services().scheduler_service.list_jobs()
    return jsonify({"jobs": jobs})


@bp.route("/api/scheduled_jobs", methods=["POST"])
def create_job() -> Any:
    payload = _json_body()
    try:
        job = _services().scheduler_service.create_job(payload)
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 400
    return jsonify({"job": job})


@bp.route("/api/scheduled_jobs/<int:job_id>", methods=["GET"])
def get_job(job_id: int) -> Any:
    job = _services().scheduler_service.get_job(job_id)
    if not job:
        return jsonify({"error": "job not found"}), 404
    return jsonify({"job": job})


@bp.route("/api/scheduled_jobs/<int:job_id>", methods=["PUT"])
def update_job(job_id: int) -> Any:
    payload = _json_body()
    try:
        job = _services().scheduler_service.update_job(job_id, payload)
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 400
    if not job:
        return jsonify({"error": "job not found"}), 404
    return jsonify({"job": job})


@bp.route("/api/scheduled_jobs/<int:job_id>", methods=["DELETE"])
def delete_job(job_id: int) -> Any:
    _services().scheduler_service.delete_job(job_id)
    return ("", 204)


@bp.route("/api/scheduled_jobs/<int:job_id>/toggle", methods=["POST"])
def toggle_job(job_id: int) -> Any:
    payload = _json_body()
    enabled = bool(payload.get("enabled", True))
    _services().scheduler_service.toggle_job(job_id, enabled)
    job = _services().scheduler_service.get_job(job_id)
    if not job:
        return jsonify({"error": "job not found"}), 404
    return jsonify({"job": job})


@bp.route("/api/scheduled_jobs/<int:job_id>/runs", methods=["GET"])
def list_runs(job_id: int) -> Any:
    days = request.args.get("days")
    days_int: Optional[int] = None
    if days is not None:
        try:
            days_int = max(0, int(days))
        except Exception:
            return jsonify({"error": "invalid days parameter"}), 400
    runs = _services().scheduler_service.list_runs(job_id, days=days_int)
    return jsonify({"runs": runs})


@bp.route("/api/scheduled_jobs/<int:job_id>/runs", methods=["DELETE"])
def purge_runs(job_id: int) -> Any:
    _services().scheduler_service.purge_runs(job_id)
    return ("", 204)


def _json_body() -> dict[str, Any]:
    if not request.data:
        return {}
    try:
        data = request.get_json(force=True, silent=False)  # type: ignore[arg-type]
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


__all__ = ["register"]
