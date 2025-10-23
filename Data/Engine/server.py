"""Flask application factory for the Borealis Engine."""

from __future__ import annotations

import importlib
import logging
import os
from pathlib import Path
from typing import Any, Iterable, Optional

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


def attach_legacy_bridge(
    app: Flask,
    settings: EngineSettings,
    *,
    logger: Optional[logging.Logger] = None,
) -> None:
    """Attach the legacy Flask application as a fallback dispatcher.

    Borealis ships a mature API surface inside ``Data/Server/server.py``.  The
    Engine will eventually supersede it, but during the migration the React
    frontend still expects the historical endpoints to exist.  This helper
    attempts to load the legacy application and wires it as a fallback WSGI
    dispatcher so any route the Engine does not yet implement transparently
    defers to the proven implementation.
    """

    log = logger or logging.getLogger("borealis.engine.legacy")

    if not _legacy_bridge_enabled():
        log.info("legacy-bridge-disabled")
        return

    legacy = _load_legacy_app(settings, app, log)
    if legacy is None:
        log.warning("legacy-bridge-unavailable")
        return

    app.config["ENGINE_LEGACY_BRIDGE_ACTIVE"] = True
    app.wsgi_app = _FallbackDispatcher(app.wsgi_app, legacy.wsgi_app)  # type: ignore[assignment]
    app.extensions["legacy_flask_app"] = legacy
    log.info("legacy-bridge-active")


def _legacy_bridge_enabled() -> bool:
    raw = os.getenv("BOREALIS_ENGINE_ENABLE_LEGACY_BRIDGE", "1")
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _load_legacy_app(
    settings: EngineSettings,
    engine_app: Flask,
    logger: logging.Logger,
) -> Optional[Flask]:
    try:
        legacy_module = importlib.import_module("Data.Server.server")
    except Exception as exc:  # pragma: no cover - defensive
        logger.exception("legacy-import-failed", exc_info=exc)
        return None

    legacy_app = getattr(legacy_module, "app", None)
    if not isinstance(legacy_app, Flask):
        logger.error("legacy-app-missing")
        return None

    # Align runtime configuration so both applications share database and
    # session state.
    try:
        setattr(legacy_module, "DB_PATH", str(settings.database_path))
    except Exception:  # pragma: no cover - defensive
        logger.warning("legacy-db-path-sync-failed", extra={"path": str(settings.database_path)})

    _synchronise_session_config(engine_app, legacy_app)

    return legacy_app


def _synchronise_session_config(engine_app: Flask, legacy_app: Flask) -> None:
    legacy_app.secret_key = engine_app.config.get("SECRET_KEY", legacy_app.secret_key)
    for key in (
        "SESSION_COOKIE_HTTPONLY",
        "SESSION_COOKIE_SECURE",
        "SESSION_COOKIE_SAMESITE",
        "SESSION_COOKIE_DOMAIN",
    ):
        value = engine_app.config.get(key)
        if value is not None:
            legacy_app.config[key] = value


class _FallbackDispatcher:
    """WSGI dispatcher that retries a secondary app when the primary 404s."""

    __slots__ = ("_primary", "_fallback", "_retry_statuses")

    def __init__(
        self,
        primary: Any,
        fallback: Any,
        *,
        retry_statuses: Iterable[int] = (404,),
    ) -> None:
        self._primary = primary
        self._fallback = fallback
        self._retry_statuses = {int(status) for status in retry_statuses}

    def __call__(self, environ: dict[str, Any], start_response: Any) -> Iterable[bytes]:
        captured_body: list[bytes] = []
        captured_status: dict[str, Any] = {}

        def _capture_start_response(status: str, headers: list[tuple[str, str]], exc_info: Any = None):
            captured_status["status"] = status
            captured_status["headers"] = headers
            captured_status["exc_info"] = exc_info

            def _write(data: bytes) -> None:
                captured_body.append(data)

            return _write

        primary_iterable = self._primary(environ, _capture_start_response)
        try:
            for chunk in primary_iterable:
                captured_body.append(chunk)
        finally:
            close = getattr(primary_iterable, "close", None)
            if callable(close):
                close()

        status_line = str(captured_status.get("status") or "500 Internal Server Error")
        try:
            status_code = int(status_line.split()[0])
        except Exception:  # pragma: no cover - defensive
            status_code = 500

        if status_code not in self._retry_statuses:
            start_response(status_line, captured_status.get("headers", []), captured_status.get("exc_info"))
            return captured_body

        return self._fallback(environ, start_response)


__all__ = ["attach_legacy_bridge", "create_app"]
