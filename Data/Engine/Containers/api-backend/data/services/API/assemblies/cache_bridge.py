# ======================================================
# Data\Engine\services\API\assemblies\cache_bridge.py
# Description: Internal assembly cache maintenance bridge for retained Python runtimes.
#
# API Endpoints (if applicable):
# - POST /api/internal/assemblies/cache/reload (Internal Token) - Reloads AssemblyCache from persistence.
# - POST /api/internal/assemblies/cache/flush (Internal Token) - Flushes dirty AssemblyCache entries to persistence.
# ======================================================

"""Internal assembly cache maintenance endpoints."""

from __future__ import annotations

from typing import TYPE_CHECKING

from flask import jsonify, request

from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, validate_internal_token

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


def register_cache_bridge(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    if getattr(app, "_borealis_assembly_cache_bridge_routes", False):
        return

    def _require_internal() -> bool:
        try:
            secret = require_app_secret(app)
        except Exception:
            return False
        return validate_internal_token(secret, request.headers.get(INTERNAL_TOKEN_HEADER))

    def _cache():
        return getattr(adapters.context, "assembly_cache", None)

    @app.route("/api/internal/assemblies/cache/reload", methods=["POST"])
    def _internal_assembly_cache_reload():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        cache = _cache()
        if cache is None or not hasattr(cache, "reload"):
            return jsonify({"error": "assembly_cache_unavailable"}), 503
        cache.reload()
        return jsonify({"status": "reloaded"}), 200

    @app.route("/api/internal/assemblies/cache/flush", methods=["POST"])
    def _internal_assembly_cache_flush():
        if not _require_internal():
            return jsonify({"error": "unauthorized"}), 401
        cache = _cache()
        if cache is None or not hasattr(cache, "flush_now"):
            return jsonify({"error": "assembly_cache_unavailable"}), 503
        cache.flush_now()
        return jsonify({"status": "flushed"}), 200

    setattr(app, "_borealis_assembly_cache_bridge_routes", True)
