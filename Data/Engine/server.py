"""Flask application factory for the Borealis Engine."""

from __future__ import annotations

from pathlib import Path
from typing import Optional

from flask import Flask, request, send_from_directory
from flask_cors import CORS
from werkzeug.exceptions import NotFound
from werkzeug.middleware.proxy_fix import ProxyFix

from .config import EngineSettings


from .repositories.sqlite.connection import (
    SQLiteConnectionFactory,
    connection_factory as create_sqlite_connection_factory,
)


def _resolve_static_folder(static_root: Path) -> tuple[str, str]:
    return str(static_root), ""


def _register_spa_routes(app: Flask, assets_root: Path) -> None:
    """Serve the Borealis single-page application from *assets_root*.

    The logic mirrors the legacy server by routing any unknown front-end paths
    back to ``index.html`` so the React router can take over.
    """

    static_folder = assets_root

    @app.route("/", defaults={"path": ""})
    @app.route("/<path:path>")
    def serve_frontend(path: str) -> object:
        candidate = (static_folder / path).resolve()
        if path and candidate.is_file():
            return send_from_directory(str(static_folder), path)
        try:
            return send_from_directory(str(static_folder), "index.html")
        except Exception as exc:  # pragma: no cover - passthrough
            raise NotFound() from exc

    @app.errorhandler(404)
    def spa_fallback(error: Exception) -> object:  # pragma: no cover - routing
        request_path = (request.path or "").strip()
        if request_path.startswith("/api") or request_path.startswith("/socket.io"):
            return error
        if "." in Path(request_path).name:
            return error
        if request.method not in {"GET", "HEAD"}:
            return error
        try:
            return send_from_directory(str(static_folder), "index.html")
        except Exception:
            return error


def create_app(
    settings: EngineSettings,
    *,
    db_factory: Optional[SQLiteConnectionFactory] = None,
) -> Flask:
    """Create the Flask application instance for the Engine."""

    if db_factory is None:
        db_factory = create_sqlite_connection_factory(settings.database_path)

    static_folder, static_url_path = _resolve_static_folder(settings.flask.static_root)
    app = Flask(
        __name__,
        static_folder=static_folder,
        static_url_path=static_url_path,
    )

    app.config.update(
        SECRET_KEY=settings.flask.secret_key,
        JSON_SORT_KEYS=False,
        SESSION_COOKIE_HTTPONLY=True,
        SESSION_COOKIE_SECURE=not settings.debug,
        SESSION_COOKIE_SAMESITE="Lax",
        ENGINE_DATABASE_PATH=str(settings.database_path),
        ENGINE_DB_CONN_FACTORY=db_factory,
    )
    app.config.setdefault("PREFERRED_URL_SCHEME", "https")

    # Respect upstream proxy headers when Borealis is hosted behind a TLS terminator.
    app.wsgi_app = ProxyFix(app.wsgi_app, x_for=1, x_proto=1, x_host=1)  # type: ignore[assignment]

    CORS(
        app,
        resources={r"/*": {"origins": list(settings.flask.cors_allowed_origins)}},
        supports_credentials=True,
    )

    _register_spa_routes(app, Path(static_folder))

    return app


__all__ = ["create_app"]
