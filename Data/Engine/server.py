"""Flask application factory for the Borealis Engine."""

from __future__ import annotations

from pathlib import Path

from flask import Flask
from flask_cors import CORS
from werkzeug.middleware.proxy_fix import ProxyFix

from .config import EngineSettings


def _resolve_static_folder(static_root: Path) -> tuple[str | None, str]:
    if static_root.exists():
        return str(static_root), "/"
    return None, "/static"


def create_app(settings: EngineSettings) -> Flask:
    """Create the Flask application instance for the Engine."""

    static_folder, static_url_path = _resolve_static_folder(settings.static_root)
    app = Flask(
        __name__,
        static_folder=static_folder,
        static_url_path=static_url_path,
    )

    app.config.update(
        SECRET_KEY=settings.secret_key,
        JSON_SORT_KEYS=False,
        SESSION_COOKIE_HTTPONLY=True,
        SESSION_COOKIE_SECURE=not settings.debug,
        SESSION_COOKIE_SAMESITE="Lax",
        ENGINE_DATABASE_PATH=str(settings.database_path),
    )

    # Respect upstream proxy headers when Borealis is hosted behind a TLS terminator.
    app.wsgi_app = ProxyFix(app.wsgi_app, x_for=1, x_proto=1, x_host=1)  # type: ignore[assignment]

    CORS(
        app,
        resources={r"/*": {"origins": list(settings.cors_allowed_origins)}},
        supports_credentials=True,
    )

    return app


__all__ = ["create_app"]
