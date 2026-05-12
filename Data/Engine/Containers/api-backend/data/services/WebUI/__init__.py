# ======================================================
# Data\Engine\services\WebUI\__init__.py
# Description: Registers the SPA static asset blueprint and 404 fallback for the Engine WebUI.
#
# API Endpoints (if applicable): None
# ======================================================

"""WebUI static asset handling for the Borealis Engine runtime."""
from __future__ import annotations

import logging
import os
from pathlib import Path
from typing import Optional

from flask import Blueprint, Flask, request, send_from_directory
from werkzeug.exceptions import NotFound

from ...server import EngineContext

_WEBUI_BLUEPRINT_NAME = "engine_webui"


def _resolve_static_root(app: Flask, context: EngineContext) -> Optional[Path]:
    static_folder = app.static_folder
    if not static_folder:
        context.logger.error("Engine WebUI static folder is not configured.")
        return None

    static_path = Path(static_folder).resolve()
    if not static_path.is_dir():
        context.logger.error("Engine WebUI static folder missing: %s", static_path)
        return None

    index_path = static_path / "index.html"
    if not index_path.is_file():
        context.logger.error("Engine WebUI missing index.html at %s", index_path)
        return None

    return static_path


def _register_spa_routes(app: Flask, static_root: Path, logger: logging.Logger) -> None:
    blueprint = Blueprint(
        _WEBUI_BLUEPRINT_NAME,
        __name__,
        static_folder=str(static_root),
        static_url_path="",
    )

    def send_index():
        return send_from_directory(str(static_root), "index.html")

    @blueprint.route("/", defaults={"requested_path": ""})
    @blueprint.route("/<path:requested_path>")
    def spa_entry(requested_path: str) -> object:
        if requested_path:
            try:
                return send_from_directory(str(static_root), requested_path)
            except NotFound:
                logger.debug("Engine WebUI asset not found: %s", requested_path)
        return send_index()

    app.register_blueprint(blueprint)

    if getattr(app, "_engine_webui_fallback_installed", False):
        return

    def _spa_fallback(error):
        request_path = (request.path or "").strip()
        if request_path.startswith("/api") or request_path.startswith("/socket.io"):
            return error
        if "." in Path(request_path).name:
            return error
        if request.method not in {"GET", "HEAD"}:
            return error
        try:
            return send_index()
        except Exception:
            return error

    app.register_error_handler(404, _spa_fallback)
    setattr(app, "_engine_webui_fallback_installed", True)


def register_web_ui(app: Flask, context: EngineContext) -> None:
    """Register static asset routes for the Engine WebUI."""

    external_webui = str(os.environ.get("BOREALIS_WEBUI_EXTERNAL") or "").strip().lower() in {"1", "true", "yes", "on"}
    if external_webui:
        context.logger.info("External WebUI service enabled; skipping Engine WebUI static route registration.")
        return

    static_root = _resolve_static_root(app, context)
    if static_root is None:
        return

    _register_spa_routes(app, static_root, context.logger)
    context.logger.info("Engine WebUI registered static assets from %s", static_root)
