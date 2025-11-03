# ======================================================
# Data\Engine\services\API\assemblies\management.py
# Description: Assembly REST API routes backed by AssemblyCache for multi-domain persistence.
#
# API Endpoints (if applicable):
# - GET /api/assemblies (Token Authenticated) - Lists assemblies with domain/source metadata.
# - GET /api/assemblies/<assembly_guid> (Token Authenticated) - Returns assembly metadata and payload reference.
# - POST /api/assemblies (Token Authenticated) - Creates a new assembly within the allowed domain.
# - PUT /api/assemblies/<assembly_guid> (Token Authenticated) - Updates an existing assembly and stages persistence.
# - DELETE /api/assemblies/<assembly_guid> (Token Authenticated) - Marks an assembly for deletion.
# - POST /api/assemblies/<assembly_guid>/clone (Token Authenticated (Admin+Dev Mode for non-user domains)) - Clones an assembly into a target domain.
# - POST /api/assemblies/dev-mode/switch (Token Authenticated (Admin)) - Enables or disables Dev Mode overrides for the current session.
# - POST /api/assemblies/dev-mode/write (Token Authenticated (Admin+Dev Mode)) - Flushes queued assembly writes immediately.
# - POST /api/assemblies/official/sync (Token Authenticated (Admin+Dev Mode)) - Rebuilds the official domain from staged JSON assemblies.
# ======================================================

"""Assembly CRUD REST endpoints backed by AssemblyCache."""

from __future__ import annotations

import logging
import os
from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from Data.Engine.assembly_management.models import AssemblyDomain
from ..assemblies.service import AssemblyRuntimeService

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


class AssemblyAPIService:
    """Facilitates assembly API routes with authentication and permission checks."""

    def __init__(self, app, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.logger = adapters.context.logger or logging.getLogger(__name__)
        cache = adapters.context.assembly_cache
        if cache is None:
            raise RuntimeError("Assembly cache not initialised; ensure Engine bootstrap executed.")
        self.runtime = AssemblyRuntimeService(cache, logger=self.logger)

    # ------------------------------------------------------------------
    # Authentication helpers
    # ------------------------------------------------------------------
    def require_user(self) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        user = self._current_user()
        if not user:
            return None, ({"error": "unauthorized"}, 401)
        return user, None

    def require_admin(self, *, dev_mode_required: bool = False) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if not self._is_admin(user):
            return {"error": "admin required"}, 403
        if dev_mode_required and not self._dev_mode_enabled():
            return {"error": "dev mode required"}, 403
        return None

    def require_mutation_for_domain(self, domain: AssemblyDomain) -> Optional[Tuple[Dict[str, Any], int]]:
        user, error = self.require_user()
        if error:
            return error
        if domain == AssemblyDomain.USER:
            return None
        if not self._is_admin(user):
            return {"error": "admin required for non-user domains"}, 403
        if not self._dev_mode_enabled():
            return {"error": "dev mode required for privileged domains"}, 403
        return None

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = self.app.secret_key or "borealis-dev-secret"
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, Any]]:
        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return {"username": username, "role": role}

        token = self._bearer_token()
        if not token:
            return None
        max_age = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
        try:
            data = self._token_serializer().loads(token, max_age=max_age)
            username = data.get("u")
            role = data.get("r") or "User"
            if username:
                return {"username": username, "role": role}
        except (BadSignature, SignatureExpired, Exception):
            return None
        return None

    def _bearer_token(self) -> Optional[str]:
        auth_header = request.headers.get("Authorization") or ""
        if auth_header.lower().startswith("bearer "):
            return auth_header.split(" ", 1)[1].strip()
        cookie_token = request.cookies.get("borealis_auth")
        if cookie_token:
            return cookie_token
        return None

    @staticmethod
    def _is_admin(user: Dict[str, Any]) -> bool:
        role = (user.get("role") or "").strip().lower()
        return role == "admin"

    def _dev_mode_enabled(self) -> bool:
        return bool(session.get("assemblies_dev_mode", False))

    def set_dev_mode(self, enabled: bool) -> None:
        session["assemblies_dev_mode"] = bool(enabled)

    # ------------------------------------------------------------------
    # Domain helpers
    # ------------------------------------------------------------------
    @staticmethod
    def parse_domain(value: Any) -> Optional[AssemblyDomain]:
        if value is None:
            return None
        candidate = str(value).strip().lower()
        for domain in AssemblyDomain:
            if domain.value == candidate:
                return domain
        return None


def register_assemblies(app, adapters: "EngineServiceAdapters") -> None:
    """Register assembly CRUD endpoints on the Flask app."""

    service = AssemblyAPIService(app, adapters)
    blueprint = Blueprint("assemblies", __name__, url_prefix="/api/assemblies")

    # ------------------------------------------------------------------
    # Collections
    # ------------------------------------------------------------------
    @blueprint.route("", methods=["GET"])
    def list_assemblies():
        _, error = service.require_user()
        if error:
            return jsonify(error[0]), error[1]

        domain = request.args.get("domain")
        kind = request.args.get("kind")
        items = service.runtime.list_assemblies(domain=domain, kind=kind)
        queue_state = service.runtime.queue_snapshot()
        return jsonify({"items": items, "queue": queue_state}), 200

    # ------------------------------------------------------------------
    # Single assembly retrieval
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>", methods=["GET"])
    def get_assembly(assembly_guid: str):
        _, error = service.require_user()
        if error:
            return jsonify(error[0]), error[1]
        data = service.runtime.get_assembly(assembly_guid)
        if not data:
            return jsonify({"error": "not found"}), 404
        data["queue"] = service.runtime.queue_snapshot()
        return jsonify(data), 200

    # ------------------------------------------------------------------
    # Creation
    # ------------------------------------------------------------------
    @blueprint.route("", methods=["POST"])
    def create_assembly():
        payload = request.get_json(silent=True) or {}
        domain = service.parse_domain(payload.get("domain"))
        if domain is None:
            return jsonify({"error": "invalid domain"}), 400
        error = service.require_mutation_for_domain(domain)
        if error:
            return jsonify(error[0]), error[1]
        try:
            record = service.runtime.create_assembly(payload)
            return jsonify(record), 201
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover - runtime guard
            service.logger.exception("Failed to create assembly.")
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Update
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>", methods=["PUT"])
    def update_assembly(assembly_guid: str):
        payload = request.get_json(silent=True) or {}
        existing = service.runtime.get_cached_entry(assembly_guid)
        if not existing:
            return jsonify({"error": "not found"}), 404
        error = service.require_mutation_for_domain(existing.domain)
        if error:
            return jsonify(error[0]), error[1]
        try:
            record = service.runtime.update_assembly(assembly_guid, payload)
            return jsonify(record), 200
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover - runtime guard
            service.logger.exception("Failed to update assembly %s.", assembly_id)
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Deletion
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>", methods=["DELETE"])
    def delete_assembly(assembly_guid: str):
        existing = service.runtime.get_cached_entry(assembly_guid)
        if not existing:
            return jsonify({"error": "not found"}), 404
        error = service.require_mutation_for_domain(existing.domain)
        if error:
            return jsonify(error[0]), error[1]
        try:
            service.runtime.delete_assembly(assembly_guid)
            return jsonify({"status": "queued"}), 202
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to delete assembly %s.", assembly_id)
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Clone between domains
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>/clone", methods=["POST"])
    def clone_assembly(assembly_guid: str):
        payload = request.get_json(silent=True) or {}
        target_domain_value = payload.get("target_domain")
        domain = service.parse_domain(target_domain_value)
        if domain is None:
            return jsonify({"error": "invalid target domain"}), 400
        error = service.require_mutation_for_domain(domain)
        if error:
            return jsonify(error[0]), error[1]
        new_guid = payload.get("new_assembly_guid")
        try:
            record = service.runtime.clone_assembly(
                assembly_guid,
                target_domain=domain.value,
                new_assembly_guid=new_guid,
            )
            return jsonify(record), 201
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to clone assembly %s.", assembly_id)
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Dev Mode toggle
    # ------------------------------------------------------------------
    @blueprint.route("/dev-mode/switch", methods=["POST"])
    def switch_dev_mode():
        error = service.require_admin()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        enabled = bool(payload.get("enabled"))
        service.set_dev_mode(enabled)
        return jsonify({"dev_mode": service._dev_mode_enabled()}), 200

    # ------------------------------------------------------------------
    # Immediate flush
    # ------------------------------------------------------------------
    @blueprint.route("/dev-mode/write", methods=["POST"])
    def flush_assemblies():
        error = service.require_admin(dev_mode_required=True)
        if error:
            return jsonify(error[0]), error[1]
        try:
            service.runtime.flush_writes()
            return jsonify({"status": "flushed"}), 200
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to flush assembly queue.")
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Official sync
    # ------------------------------------------------------------------
    @blueprint.route("/official/sync", methods=["POST"])
    def sync_official():
        error = service.require_admin(dev_mode_required=True)
        if error:
            return jsonify(error[0]), error[1]
        try:
            service.runtime.sync_official()
            return jsonify({"status": "synced"}), 200
        except Exception:  # pragma: no cover
            service.logger.exception("Official assembly sync failed.")
            return jsonify({"error": "internal server error"}), 500

    app.register_blueprint(blueprint)
