"""Token management HTTP interface for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify, request

from Data.Engine.builders.device_auth import RefreshTokenRequestBuilder
from Data.Engine.domain.device_auth import DeviceAuthFailure
from Data.Engine.services.container import EngineServiceContainer
from Data.Engine.services import TokenRefreshError

blueprint = Blueprint("engine_tokens", __name__)


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach token management routes to *app*."""

    if "engine_tokens" not in app.blueprints:
        app.register_blueprint(blueprint)


@blueprint.route("/api/agent/token/refresh", methods=["POST"])
def refresh_token() -> object:
    services: EngineServiceContainer = current_app.extensions["engine_services"]
    builder = (
        RefreshTokenRequestBuilder()
        .with_payload(request.get_json(force=True, silent=True))
        .with_http_method(request.method)
        .with_htu(request.url)
        .with_dpop_proof(request.headers.get("DPoP"))
    )
    try:
        refresh_request = builder.build()
    except DeviceAuthFailure as exc:
        payload = exc.to_dict()
        return jsonify(payload), exc.http_status

    try:
        response = services.token_service.refresh_access_token(refresh_request)
    except TokenRefreshError as exc:
        return jsonify(exc.to_dict()), exc.http_status

    return jsonify(
        {
            "access_token": response.access_token,
            "expires_in": response.expires_in,
            "token_type": response.token_type,
        }
    )


__all__ = ["register", "blueprint", "refresh_token"]
