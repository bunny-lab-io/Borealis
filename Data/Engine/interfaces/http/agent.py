"""Agent REST endpoints for device communication."""

from __future__ import annotations

import math
from functools import wraps
from typing import Any, Callable, Optional, TypeVar, cast

from flask import Blueprint, Flask, current_app, g, jsonify, request

from Data.Engine.builders.device_auth import DeviceAuthRequestBuilder
from Data.Engine.domain.device_auth import DeviceAuthContext, DeviceAuthFailure
from Data.Engine.services.container import EngineServiceContainer
from Data.Engine.services.devices.device_inventory_service import DeviceHeartbeatError

AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"

blueprint = Blueprint("engine_agent", __name__)

F = TypeVar("F", bound=Callable[..., Any])


def _services() -> EngineServiceContainer:
    return cast(EngineServiceContainer, current_app.extensions["engine_services"])


def require_device_auth(func: F) -> F:
    @wraps(func)
    def wrapper(*args: Any, **kwargs: Any):
        services = _services()
        builder = (
            DeviceAuthRequestBuilder()
            .with_authorization(request.headers.get("Authorization"))
            .with_http_method(request.method)
            .with_htu(request.url)
            .with_service_context(request.headers.get(AGENT_CONTEXT_HEADER))
            .with_dpop_proof(request.headers.get("DPoP"))
        )
        try:
            auth_request = builder.build()
            context = services.device_auth.authenticate(auth_request, path=request.path)
        except DeviceAuthFailure as exc:
            payload = exc.to_dict()
            response = jsonify(payload)
            if exc.retry_after is not None:
                response.headers["Retry-After"] = str(int(math.ceil(exc.retry_after)))
            return response, exc.http_status

        g.device_auth = context
        try:
            return func(*args, **kwargs)
        finally:
            g.pop("device_auth", None)

    return cast(F, wrapper)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    if "engine_agent" not in app.blueprints:
        app.register_blueprint(blueprint)


@blueprint.route("/api/agent/heartbeat", methods=["POST"])
@require_device_auth
def heartbeat() -> Any:
    services = _services()
    payload = request.get_json(force=True, silent=True) or {}
    context = cast(DeviceAuthContext, g.device_auth)

    try:
        services.device_inventory.record_heartbeat(context=context, payload=payload)
    except DeviceHeartbeatError as exc:
        error_payload = {"error": exc.code}
        if exc.code == "device_not_registered":
            return jsonify(error_payload), 404
        if exc.code == "storage_conflict":
            return jsonify(error_payload), 409
        current_app.logger.exception(
            "device-heartbeat-error guid=%s code=%s", context.identity.guid.value, exc.code
        )
        return jsonify(error_payload), 500

    return jsonify({"status": "ok", "poll_after_ms": 15000})


@blueprint.route("/api/agent/script/request", methods=["POST"])
@require_device_auth
def script_request() -> Any:
    services = _services()
    context = cast(DeviceAuthContext, g.device_auth)

    signing_key: Optional[str] = None
    signer = services.script_signer
    if signer is not None:
        try:
            signing_key = signer.public_base64_spki()
        except Exception as exc:  # pragma: no cover - defensive logging
            current_app.logger.warning("script-signer-unavailable: %s", exc)

    status = "quarantined" if context.is_quarantined else "idle"
    poll_after = 60000 if context.is_quarantined else 30000

    response = {
        "status": status,
        "poll_after_ms": poll_after,
        "sig_alg": "ed25519",
    }
    if signing_key:
        response["signing_key"] = signing_key
    return jsonify(response)


__all__ = ["register", "blueprint", "heartbeat", "script_request", "require_device_auth"]
