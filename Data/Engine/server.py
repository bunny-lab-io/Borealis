"""Stage 2 Borealis Engine application factory.

Stage 1 introduced the structural skeleton for the Engine runtime.  Stage 2
builds upon that foundation by centralising configuration handling and logging
initialisation so the Engine mirrors the legacy server's start-up behaviour.
The factory delegates configuration resolution to :mod:`Data.Engine.config`
and emits structured logs to ``Logs/Engine/engine.log`` (with an accompanying
error log) to align with the project's operational practices.
"""
from __future__ import annotations

import logging
import ssl
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping, Optional, Sequence, Tuple

try:  # pragma: no-cover - optional dependency when running without eventlet
    import eventlet  # type: ignore
except Exception:  # pragma: no-cover - fall back to threading mode
    eventlet = None  # type: ignore[assignment]
    logging.getLogger(__name__).warning(
        "Eventlet is not available; Engine will run Socket.IO in threading mode."
    )
else:  # pragma: no-cover - monkey patch only when eventlet is present
    eventlet.monkey_patch(thread=False)

from flask import Flask, request
from flask_cors import CORS
from flask_socketio import SocketIO
from werkzeug.middleware.proxy_fix import ProxyFix

if eventlet:
    try:  # pragma: no-cover - defensive import mirroring the legacy runtime.
        from eventlet.wsgi import HttpProtocol  # type: ignore
    except Exception:  # pragma: no-cover - the Engine should still operate without it.
        HttpProtocol = None  # type: ignore[assignment]
    else:
        _original_handle_one_request = HttpProtocol.handle_one_request

        def _quiet_tls_http_mismatch(self):  # type: ignore[override]
            """Mirror the legacy suppression of noisy TLS handshake errors."""

            def _close_connection_quietly():
                try:
                    self.close_connection = True  # type: ignore[attr-defined]
                except Exception:
                    pass
                try:
                    conn = getattr(self, "socket", None) or getattr(self, "connection", None)
                    if conn:
                        conn.close()
                except Exception:
                    pass

            try:
                return _original_handle_one_request(self)
            except ssl.SSLError as exc:  # type: ignore[arg-type]
                reason = getattr(exc, "reason", "")
                reason_text = str(reason).lower() if reason else ""
                message = " ".join(str(arg) for arg in exc.args if arg).lower()
                if (
                    "http_request" in message
                    or reason_text == "http request"
                    or "unknown ca" in message
                    or reason_text == "unknown ca"
                    or "unknown_ca" in message
                ):
                    _close_connection_quietly()
                    return None
                raise
            except ssl.SSLEOFError:
                _close_connection_quietly()
                return None
            except ConnectionAbortedError:
                _close_connection_quietly()
                return None

        HttpProtocol.handle_one_request = _quiet_tls_http_mismatch  # type: ignore[assignment]
else:
    HttpProtocol = None  # type: ignore[assignment]

_SOCKETIO_ASYNC_MODE = "eventlet" if eventlet else "threading"


# Ensure the legacy ``Modules`` package is importable when running from the
# Engine deployment directory.
_ENGINE_DIR = Path(__file__).resolve().parent
_SEARCH_ROOTS = [
    _ENGINE_DIR.parent / "Server",
    _ENGINE_DIR.parent.parent / "Data" / "Server",
]
for root in _SEARCH_ROOTS:
    modules_dir = root / "Modules"
    if modules_dir.is_dir():
        root_str = str(root)
        if root_str not in sys.path:
            sys.path.insert(0, root_str)

from .config import EngineSettings, initialise_engine_logger, load_runtime_config


@dataclass
class EngineContext:
    """Shared handles that Engine services will consume."""

    database_path: str
    logger: logging.Logger
    scheduler: Any
    tls_cert_path: Optional[str]
    tls_key_path: Optional[str]
    tls_bundle_path: Optional[str]
    config: Mapping[str, Any]
    api_groups: Sequence[str]


__all__ = ["EngineContext", "create_app", "register_engine_api"]


def _build_engine_context(settings: EngineSettings, logger: logging.Logger) -> EngineContext:
    return EngineContext(
        database_path=settings.database_path,
        logger=logger,
        scheduler=None,
        tls_cert_path=settings.tls_cert_path,
        tls_key_path=settings.tls_key_path,
        tls_bundle_path=settings.tls_bundle_path,
        config=settings.as_dict(),
        api_groups=settings.api_groups,
    )


def _attach_transition_logging(app: Flask, context: EngineContext, logger: logging.Logger) -> None:
    tracked = {group.strip().lower() for group in context.api_groups if group}
    if not tracked:
        tracked = {"tokens", "enrollment"}

    existing = getattr(app, "_engine_api_tracked_blueprints", set())
    if existing:
        tracked.update(existing)
    setattr(app, "_engine_api_tracked_blueprints", tracked)

    if getattr(app, "_engine_api_logging_installed", False):
        return

    @app.before_request
    def _log_engine_api_bridge() -> None:  # pragma: no cover - integration behaviour exercised in higher-level tests
        blueprint = (request.blueprint or "").lower()
        if blueprint and blueprint in getattr(app, "_engine_api_tracked_blueprints", tracked):
            logger.info(
                "Engine handling API request via legacy bridge: %s %s",
                request.method,
                request.path,
            )

    setattr(app, "_engine_api_logging_installed", True)


def create_app(config: Optional[Mapping[str, Any]] = None) -> Tuple[Flask, SocketIO, EngineContext]:
    """Create the Stage 2 Engine Flask application."""

    settings: EngineSettings = load_runtime_config(config)
    logger = initialise_engine_logger(settings)

    static_folder = settings.static_folder
    app = Flask(__name__, static_folder=static_folder, static_url_path="")
    app.wsgi_app = ProxyFix(app.wsgi_app, x_proto=1, x_host=1)

    cors_origins = settings.cors_origins
    if cors_origins:
        CORS(app, supports_credentials=True, origins=cors_origins)
    else:
        CORS(app, supports_credentials=True)

    app.secret_key = settings.secret_key

    app.config.update(settings.to_flask_config())

    socketio = SocketIO(
        app,
        cors_allowed_origins="*",
        async_mode=_SOCKETIO_ASYNC_MODE,
        engineio_options={
            "max_http_buffer_size": 100_000_000,
            "max_websocket_message_size": 100_000_000,
        },
    )

    context = _build_engine_context(settings, logger)

    from .services import API, WebSocket, WebUI  # Local import to avoid circular deps during bootstrap

    API.register_api(app, context)
    WebUI.register_web_ui(app, context)
    WebSocket.register_realtime(socketio, context)

    logger.debug("Engine application factory completed initialisation.")

    return app, socketio, context


def register_engine_api(app: Flask, *, config: Optional[Mapping[str, Any]] = None) -> EngineContext:
    """Register Engine-managed API blueprints onto an existing Flask app."""

    settings: EngineSettings = load_runtime_config(config)
    logger = initialise_engine_logger(settings)
    context = _build_engine_context(settings, logger)

    from .services import API  # Local import avoids circular dependency at module import time

    API.register_api(app, context)
    _attach_transition_logging(app, context, logger)

    groups_display = ", ".join(context.api_groups) if context.api_groups else "none"
    logger.info("Engine API delegation activated for groups: %s", groups_display)

    return context
