"""Enrollment HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request

from Data.Engine.builders.device_enrollment import EnrollmentRequestBuilder
from Data.Engine.services import EnrollmentValidationError
from Data.Engine.services.container import EngineServiceContainer

AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


blueprint = Blueprint("engine_enrollment", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach enrollment routes to *app*."""

    if "engine_enrollment" not in app.blueprints:
        app.register_blueprint(blueprint)


@blueprint.route("/api/agent/enroll/request", methods=["POST"])
def enrollment_request() -> object:
    services: EngineServiceContainer = current_app.extensions["engine_services"]
    payload = request.get_json(force=True, silent=True)
    builder = EnrollmentRequestBuilder().with_payload(payload).with_service_context(
        request.headers.get(AGENT_CONTEXT_HEADER)
    )
    try:
        normalized = builder.build()
        result = services.enrollment_service.request_enrollment(
            normalized,
            remote_addr=_remote_addr(),
        )
    except EnrollmentValidationError as exc:
        response = jsonify(exc.to_response())
        response.status_code = exc.http_status
        if exc.retry_after is not None:
            response.headers["Retry-After"] = f"{int(exc.retry_after)}"
        return response

    response_payload = {
        "status": result.status,
        "approval_reference": result.approval_reference,
        "server_nonce": result.server_nonce,
        "poll_after_ms": result.poll_after_ms,
        "server_certificate": result.server_certificate,
        "signing_key": result.signing_key,
    }
    response = jsonify(response_payload)
    response.status_code = result.http_status
    if result.retry_after is not None:
        response.headers["Retry-After"] = f"{int(result.retry_after)}"
    return response


@blueprint.route("/api/agent/enroll/poll", methods=["POST"])
def enrollment_poll() -> object:
    services: EngineServiceContainer = current_app.extensions["engine_services"]
    payload = request.get_json(force=True, silent=True) or {}
    approval_reference = str(payload.get("approval_reference") or "").strip()
    client_nonce = str(payload.get("client_nonce") or "").strip()
    proof_sig = str(payload.get("proof_sig") or "").strip()

    try:
        result = services.enrollment_service.poll_enrollment(
            approval_reference=approval_reference,
            client_nonce_b64=client_nonce,
            proof_signature_b64=proof_sig,
        )
    except EnrollmentValidationError as exc:
        return jsonify(exc.to_response()), exc.http_status

    body = {"status": result.status}
    if result.poll_after_ms is not None:
        body["poll_after_ms"] = result.poll_after_ms
    if result.reason:
        body["reason"] = result.reason
    if result.detail:
        body["detail"] = result.detail
    if result.tokens:
        body.update(
            {
                "guid": result.tokens.guid.value,
                "access_token": result.tokens.access_token,
                "refresh_token": result.tokens.refresh_token,
                "token_type": result.tokens.token_type,
                "expires_in": result.tokens.expires_in,
                "server_certificate": result.server_certificate or "",
                "signing_key": result.signing_key or "",
            }
        )
    else:
        if result.server_certificate:
            body["server_certificate"] = result.server_certificate
        if result.signing_key:
            body["signing_key"] = result.signing_key

    return jsonify(body), result.http_status


def _remote_addr() -> str:
    forwarded = request.headers.get("X-Forwarded-For")
    if forwarded:
        return forwarded.split(",")[0].strip()
    return (request.remote_addr or "unknown").strip()


__all__ = ["register", "blueprint", "enrollment_request", "enrollment_poll"]
