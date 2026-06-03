# ======================================================
# Data\Engine\server.py
# Description: Flask/Socket.IO application factory wiring Engine services, logging, and legacy bridge registration.
#
# API Endpoints (if applicable): None
# ======================================================

"""Stage 2 Borealis Engine application factory.

Stage 1 introduced the structural skeleton for the Engine runtime.  Stage 2
builds upon that foundation by centralising configuration handling and logging
initialisation so the Engine mirrors the legacy server's start-up behaviour.
The factory delegates configuration resolution to :mod:`Data.Engine.config`
and emits structured logs to ``Engine/Services/api-backend/logs/engine.log`` (with an accompanying
error log) to align with the project's operational practices.
"""
from __future__ import annotations

import atexit
import importlib
import importlib.util
import logging
import os
import time
import ssl
from dataclasses import dataclass
from logging.handlers import TimedRotatingFileHandler
from typing import Any, Mapping, Optional, Sequence, Tuple


def _require_dependency(module: str, friendly_name: str) -> None:
    if importlib.util.find_spec(module) is None:  # pragma: no cover - import check
        raise RuntimeError(
            f"{friendly_name} (Python module '{module}') is required for the Borealis Engine runtime. "
            "Install the packaged dependencies by running Engine.sh or ensure the module is present in the active environment."
        )


_require_dependency("eventlet", "Eventlet")
_require_dependency("flask", "Flask")
_require_dependency("flask_socketio", "Flask-SocketIO")

import eventlet  # type: ignore  # noqa: E402  # pragma: no cover - import guarded above
from eventlet import wsgi as eventlet_wsgi  # type: ignore  # noqa: E402  # pragma: no cover

from flask import Flask, g, request  # noqa: E402
from flask_cors import CORS  # noqa: E402
from flask_socketio import SocketIO  # noqa: E402
from werkzeug.middleware.proxy_fix import ProxyFix  # noqa: E402

eventlet.monkey_patch(thread=False)  # pragma: no cover - aligns with legacy runtime

HttpProtocol = getattr(eventlet_wsgi, "HttpProtocol", None)
if HttpProtocol is not None and hasattr(HttpProtocol, "handle_one_request"):
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
                or "certificate unknown" in message
                or reason_text == "certificate unknown"
                or "certificate_unknown" in message
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

def _resolve_socketio_async_mode() -> str:
    requested = (os.environ.get("BOREALIS_SOCKETIO_ASYNC_MODE") or "eventlet").strip().lower() or "eventlet"
    if requested != "eventlet":
        return requested
    try:
        importlib.util.find_spec("engineio.async_drivers.eventlet")
        importlib.import_module("engineio.async_drivers.eventlet")
        return "eventlet"
    except Exception:
        return "threading"


_SOCKETIO_ASYNC_MODE = _resolve_socketio_async_mode()

_ASSEMBLY_SHUTDOWN_REGISTERED = False


from .config import EngineSettings, initialise_engine_logger, load_runtime_config
from .assembly_management import initialise_assembly_runtime


@dataclass
class EngineContext:
    """Shared handles that Engine services will consume."""

    database_url: str
    logger: logging.Logger
    scheduler: Any
    socketio: Optional[Any]
    tls_cert_path: Optional[str]
    tls_key_path: Optional[str]
    tls_bundle_path: Optional[str]
    public_edge_enabled: bool
    public_base_url: Optional[str]
    public_hostname: Optional[str]
    public_https_port: int
    public_vnc_path: str
    public_wireguard_host: Optional[str]
    public_wireguard_port: int
    disable_engine_tls: bool
    letsencrypt_settings_path: Optional[str]
    config: Mapping[str, Any]
    api_groups: Sequence[str]
    api_log_path: str
    vpn_tunnel_log_path: str
    wireguard_port: int
    wireguard_engine_virtual_ip: str
    wireguard_peer_network: str
    wireguard_server_private_key_path: str
    wireguard_server_public_key_path: str
    wireguard_port_allowlist: Tuple[int, ...]
    wireguard_shell_port: int
    vnc_port: int
    vnc_ws_host: str
    vnc_ws_port: int
    vnc_session_ttl_seconds: int
    guacamole_enabled: bool
    guacd_host: str
    guacd_port: int
    guacamole_vnc_ws_path: str
    assembly_cache: Optional[Any] = None
    vnc_proxy: Optional[Any] = None
    vnc_registry: Optional[Any] = None
    guacamole_vnc_registry: Optional[Any] = None
    aegis_cipher_service: Optional[Any] = None
    workflow_runtime: Optional[Any] = None
    watchdog_runtime: Optional[Any] = None
    agent_release_manager: Optional[Any] = None


__all__ = ["EngineContext", "create_app", "register_engine_api"]


def _build_engine_context(settings: EngineSettings, logger: logging.Logger) -> EngineContext:
    return EngineContext(
        database_url=settings.database_url,
        logger=logger,
        scheduler=None,
        socketio=None,
        tls_cert_path=settings.tls_cert_path,
        tls_key_path=settings.tls_key_path,
        tls_bundle_path=settings.tls_bundle_path,
        public_edge_enabled=settings.public_edge_enabled,
        public_base_url=settings.public_base_url,
        public_hostname=settings.public_hostname,
        public_https_port=settings.public_https_port,
        public_vnc_path=settings.public_vnc_path,
        public_wireguard_host=settings.public_wireguard_host,
        public_wireguard_port=settings.public_wireguard_port,
        disable_engine_tls=settings.disable_engine_tls,
        letsencrypt_settings_path=settings.letsencrypt_settings_path,
        config=settings.as_dict(),
        api_groups=settings.api_groups,
        api_log_path=settings.api_log_file,
        vpn_tunnel_log_path=settings.vpn_tunnel_log_file,
        wireguard_port=settings.wireguard_port,
        wireguard_engine_virtual_ip=settings.wireguard_engine_virtual_ip,
        wireguard_peer_network=settings.wireguard_peer_network,
        wireguard_server_private_key_path=settings.wireguard_server_private_key_path,
        wireguard_server_public_key_path=settings.wireguard_server_public_key_path,
        wireguard_port_allowlist=settings.wireguard_port_allowlist,
        wireguard_shell_port=settings.wireguard_shell_port,
        vnc_port=settings.vnc_port,
        vnc_ws_host=settings.vnc_ws_host,
        vnc_ws_port=settings.vnc_ws_port,
        vnc_session_ttl_seconds=settings.vnc_session_ttl_seconds,
        guacamole_enabled=settings.guacamole_enabled,
        guacd_host=settings.guacd_host,
        guacd_port=settings.guacd_port,
        guacamole_vnc_ws_path=settings.guacamole_vnc_ws_path,
        assembly_cache=None,
        aegis_cipher_service=None,
        workflow_runtime=None,
        watchdog_runtime=None,
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


def _register_assembly_shutdown_hook(assembly_cache, logger: logging.Logger) -> None:
    global _ASSEMBLY_SHUTDOWN_REGISTERED
    if _ASSEMBLY_SHUTDOWN_REGISTERED:
        return

    def _shutdown_assembly_cache() -> None:  # pragma: no cover - process shutdown
        try:
            assembly_cache.shutdown(flush=True)
        except Exception:
            logger.debug("Failed to shut down assembly cache cleanly", exc_info=True)

    atexit.register(_shutdown_assembly_cache)
    _ASSEMBLY_SHUTDOWN_REGISTERED = True


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
    context.socketio = socketio

    assembly_cache = initialise_assembly_runtime(logger=logger, config=settings.as_dict())
    assembly_cache.reload()
    context.assembly_cache = assembly_cache
    _register_assembly_shutdown_hook(assembly_cache, logger)

    api_logger = logging.getLogger("borealis.engine.api")
    if not api_logger.handlers:
        api_handler = TimedRotatingFileHandler(
            context.api_log_path,
            when="midnight",
            backupCount=0,
            encoding="utf-8",
        )
        api_handler.setFormatter(logging.Formatter("%(asctime)s-%(name)s-%(levelname)s: %(message)s"))
        api_logger.addHandler(api_handler)
    api_logger.setLevel(logging.INFO)
    api_logger.propagate = False

    @app.before_request
    def _engine_api_start_timer() -> None:  # pragma: no cover - runtime behaviour
        if request.path.startswith("/api"):
            g._engine_api_start = time.perf_counter()

    @app.after_request
    def _engine_api_log(response):  # pragma: no cover - runtime behaviour
        if request.path.startswith("/api"):
            start = getattr(g, "_engine_api_start", None)
            duration_ms = None
            if start is not None:
                duration_ms = (time.perf_counter() - start) * 1000.0
            client_ip = (request.headers.get("X-Forwarded-For") or request.remote_addr or "-").split(",")[0].strip()
            status = response.status_code
            success = 200 <= status < 400
            api_logger.info(
                "client=%s method=%s path=%s status=%s success=%s duration_ms=%.2f",
                client_ip,
                request.method,
                request.full_path.rstrip("?"),
                status,
                "true" if success else "false",
                duration_ms if duration_ms is not None else 0.0,
            )
        return response

    from .services import API, WebSocket, WebUI  # Local import to avoid circular deps during bootstrap

    API.register_api(app, context)
    WebUI.register_web_ui(app, context)
    WebSocket.register_realtime(socketio, context)
    release_manager = getattr(context, "agent_release_manager", None)
    if release_manager is not None:
        try:
            release_manager.start()
        except Exception:
            logger.error("Failed to start agent release channel manager.", exc_info=True)
    logger.debug("Engine application factory completed initialisation.")

    return app, socketio, context


def register_engine_api(app: Flask, *, config: Optional[Mapping[str, Any]] = None) -> EngineContext:
    """Register Engine-managed API blueprints onto an existing Flask app."""

    settings: EngineSettings = load_runtime_config(config)
    logger = initialise_engine_logger(settings)
    context = _build_engine_context(settings, logger)

    assembly_cache = initialise_assembly_runtime(logger=logger, config=settings.as_dict())
    assembly_cache.reload()
    context.assembly_cache = assembly_cache
    _register_assembly_shutdown_hook(assembly_cache, logger)

    from .services import API  # Local import avoids circular dependency at module import time

    API.register_api(app, context)
    _attach_transition_logging(app, context, logger)

    groups_display = ", ".join(context.api_groups) if context.api_groups else "none"
    logger.info("Engine API delegation activated for groups: %s", groups_display)

    return context
