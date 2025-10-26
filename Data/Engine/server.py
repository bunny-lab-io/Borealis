"""Stage 1 Borealis Engine application factory.

This module establishes the foundational structure for the Engine runtime so
subsequent migration stages can progressively assume responsibility for the
API, WebUI, and WebSocket layers.
"""
from __future__ import annotations

import logging
import os
import ssl
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping, MutableMapping, Optional, Tuple

import eventlet
from flask import Flask
from flask_cors import CORS
from flask_socketio import SocketIO
from werkzeug.middleware.proxy_fix import ProxyFix

# Eventlet ensures Socket.IO long-polling and WebSocket support parity with the
# legacy server. We keep thread pools enabled for compatibility with blocking
# filesystem/database operations.
eventlet.monkey_patch(thread=False)

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

try:  # pragma: no-cover - optional during Stage 1 scaffolding.
    from Modules.crypto import certificates  # type: ignore
except Exception:  # pragma: no-cover - Engine can start without certificate helpers.
    certificates = None  # type: ignore[assignment]


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


__all__ = ["EngineContext", "create_app"]


def _resolve_static_folder() -> str:
    candidates = [
        _ENGINE_DIR / "web-interface" / "build",
        _ENGINE_DIR / "web-interface" / "dist",
        _ENGINE_DIR / "web-interface",
    ]
    for candidate in candidates:
        absolute = candidate.resolve()
        if absolute.is_dir():
            return str(absolute)
    # Fall back to the first candidate to maintain parity with the legacy
    # behaviour where the folder may not exist yet.
    return str(candidates[0].resolve())


def _initialise_logger(name: str = "borealis.engine") -> logging.Logger:
    logger = logging.getLogger(name)
    if not logger.handlers:
        handler = logging.StreamHandler()
        formatter = logging.Formatter("%(asctime)s-%(name)s-%(levelname)s: %(message)s")
        handler.setFormatter(formatter)
        logger.addHandler(handler)
    logger.setLevel(logging.INFO)
    logger.propagate = False
    return logger


def _discover_tls_material(config: Mapping[str, Any]) -> Tuple[Optional[str], Optional[str], Optional[str]]:
    cert_path = str(config.get("TLS_CERT_PATH") or os.environ.get("BOREALIS_TLS_CERT") or "") or None
    key_path = str(config.get("TLS_KEY_PATH") or os.environ.get("BOREALIS_TLS_KEY") or "") or None
    bundle_path = str(config.get("TLS_BUNDLE_PATH") or os.environ.get("BOREALIS_TLS_BUNDLE") or "") or None

    if certificates and not all([cert_path, key_path, bundle_path]):
        try:
            auto_cert, auto_key, auto_bundle = certificates.certificate_paths()
        except Exception:
            auto_cert = auto_key = auto_bundle = None
        else:
            cert_path = cert_path or auto_cert
            key_path = key_path or auto_key
            bundle_path = bundle_path or auto_bundle

    if cert_path:
        os.environ.setdefault("BOREALIS_TLS_CERT", cert_path)
    if key_path:
        os.environ.setdefault("BOREALIS_TLS_KEY", key_path)
    if bundle_path:
        os.environ.setdefault("BOREALIS_TLS_BUNDLE", bundle_path)
    return cert_path, key_path, bundle_path


def _coerce_config(source: Optional[Mapping[str, Any]]) -> MutableMapping[str, Any]:
    if source is None:
        return {}
    return dict(source)


def create_app(config: Optional[Mapping[str, Any]] = None) -> Tuple[Flask, SocketIO, EngineContext]:
    """Create the Stage 1 Engine Flask application."""

    runtime_config: MutableMapping[str, Any] = _coerce_config(config)
    logger = _initialise_logger()

    database_path = runtime_config.get("DATABASE_PATH") or os.path.abspath(
        os.path.join(_ENGINE_DIR, "..", "..", "database.db")
    )
    os.makedirs(os.path.dirname(database_path), exist_ok=True)

    static_folder = _resolve_static_folder()
    app = Flask(__name__, static_folder=static_folder, static_url_path="")
    app.wsgi_app = ProxyFix(app.wsgi_app, x_proto=1, x_host=1)

    cors_origins = runtime_config.get("CORS_ORIGINS") or os.environ.get("BOREALIS_CORS_ORIGINS")
    if cors_origins:
        origins = [origin.strip() for origin in str(cors_origins).split(",") if origin.strip()]
        CORS(app, supports_credentials=True, origins=origins)
    else:
        CORS(app, supports_credentials=True)

    app.secret_key = runtime_config.get("SECRET_KEY") or os.environ.get("BOREALIS_SECRET", "borealis-dev-secret")

    app.config.update(
        SESSION_COOKIE_HTTPONLY=True,
        SESSION_COOKIE_SAMESITE=runtime_config.get(
            "SESSION_COOKIE_SAMESITE",
            os.environ.get("BOREALIS_COOKIE_SAMESITE", "Lax"),
        ),
        SESSION_COOKIE_SECURE=bool(
            str(runtime_config.get("SESSION_COOKIE_SECURE", os.environ.get("BOREALIS_COOKIE_SECURE", "0"))).lower()
            in {"1", "true", "yes"}
        ),
    )
    app.config.setdefault("PREFERRED_URL_SCHEME", "https")

    cookie_domain = runtime_config.get("SESSION_COOKIE_DOMAIN") or os.environ.get("BOREALIS_COOKIE_DOMAIN")
    if cookie_domain:
        app.config["SESSION_COOKIE_DOMAIN"] = cookie_domain

    socketio = SocketIO(
        app,
        cors_allowed_origins="*",
        async_mode="eventlet",
        engineio_options={
            "max_http_buffer_size": 100_000_000,
            "max_websocket_message_size": 100_000_000,
        },
    )

    tls_cert_path, tls_key_path, tls_bundle_path = _discover_tls_material(runtime_config)

    context = EngineContext(
        database_path=database_path,
        logger=logger,
        scheduler=None,
        tls_cert_path=tls_cert_path,
        tls_key_path=tls_key_path,
        tls_bundle_path=tls_bundle_path,
        config=runtime_config,
    )

    from .services import API, WebSocket, WebUI  # Local import to avoid circular deps during bootstrap

    API.register_api(app, context)
    WebUI.register_web_ui(app, context)
    WebSocket.register_realtime(socketio, context)

    logger.debug("Engine application factory completed initialisation.")

    return app, socketio, context
