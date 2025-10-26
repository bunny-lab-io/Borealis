"""Entrypoint for the Borealis Engine server."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from flask import Flask

from .config import EngineSettings, configure_logging, load_environment
from .interfaces import (
    create_socket_server,
    register_http_interfaces,
    register_ws_interfaces,
)
from .interfaces.eventlet_compat import apply_eventlet_patches
from .repositories.sqlite import connection as sqlite_connection
from .repositories.sqlite import migrations as sqlite_migrations
from .runtime_layout import normalise_runtime_layout
from .server import create_app
from .services.container import build_service_container
from .services.crypto.certificates import ensure_certificate
from .webui.staging import stage_web_interface


apply_eventlet_patches()


@dataclass(frozen=True, slots=True)
class EngineRuntime:
    """Aggregated runtime context produced by :func:`bootstrap`."""

    app: Flask
    settings: EngineSettings
    socketio: Optional[object]
    db_factory: sqlite_connection.SQLiteConnectionFactory
    tls_certificate: Path
    tls_key: Path
    tls_bundle: Path


def bootstrap() -> EngineRuntime:
    """Construct the Flask application and supporting infrastructure."""

    preliminary_settings = load_environment()
    layout_result = normalise_runtime_layout(preliminary_settings.runtime_root)
    staging_result = stage_web_interface(
        preliminary_settings.project_root,
        preliminary_settings.runtime_root,
    )
    settings = load_environment()
    logger = configure_logging(settings)
    logger.info("bootstrap-started")
    if layout_result.changed:
        logger.info(
            "runtime-layout-normalised",
            extra={
                "legacy_root": str(layout_result.legacy_root),
                "moved": len(layout_result.moves),
                "removed": len(layout_result.removed),
            },
        )
    elif layout_result.reason:
        logger.debug(
            "runtime-layout-unchanged",
            extra={
                "legacy_root": str(layout_result.legacy_root),
                "reason": layout_result.reason,
            },
        )
    if staging_result.copied:
        logger.info(
            "webui-staged",
            extra={
                "source": str(staging_result.source),
                "destination": str(staging_result.destination),
            },
        )
    else:
        logger.warning(
            "webui-staging-skipped",
            extra={
                "source": str(staging_result.source),
                "destination": str(staging_result.destination),
                "reason": staging_result.reason or "unknown",
            },
        )

    staged_root = staging_result.destination.resolve()
    static_root = settings.flask.static_root.resolve()
    if staged_root != static_root:
        logger.warning(
            "webui-static-root-mismatch",
            extra={
                "expected": str(staged_root),
                "configured": str(static_root),
            },
        )

    cert_path, key_path, bundle_path = ensure_certificate()
    os.environ.setdefault("BOREALIS_TLS_BUNDLE", str(bundle_path))
    logger.info(
        "tls-material-ready",
        extra={
            "cert_path": str(cert_path),
            "key_path": str(key_path),
            "bundle_path": str(bundle_path),
        },
    )

    db_factory = sqlite_connection.connection_factory(settings.database_path)
    if settings.apply_migrations:
        logger.info("migrations-start")
        with sqlite_connection.connection_scope(settings.database_path) as conn:
            sqlite_migrations.apply_all(conn)
        logger.info("migrations-complete")
    else:
        logger.info("migrations-skipped")

    with sqlite_connection.connection_scope(settings.database_path) as conn:
        sqlite_migrations.ensure_default_admin(conn)
    logger.info("default-admin-ensured")

    app = create_app(settings, db_factory=db_factory)
    services = build_service_container(settings, db_factory=db_factory, logger=logger.getChild("services"))
    app.extensions["engine_services"] = services
    register_http_interfaces(app, services)

    socketio = create_socket_server(app, settings.socketio)
    register_ws_interfaces(socketio, services)
    services.scheduler_service.start(socketio)
    logger.info("bootstrap-complete")
    return EngineRuntime(
        app=app,
        settings=settings,
        socketio=socketio,
        db_factory=db_factory,
        tls_certificate=cert_path,
        tls_key=key_path,
        tls_bundle=bundle_path,
    )


def main() -> None:
    runtime = bootstrap()
    socketio = runtime.socketio
    certfile = str(runtime.tls_bundle)
    keyfile = str(runtime.tls_key)

    if socketio is not None:
        socketio.run(  # type: ignore[call-arg]
            runtime.app,
            host=runtime.settings.server.host,
            port=runtime.settings.server.port,
            debug=runtime.settings.debug,
            certfile=certfile,
            keyfile=keyfile,
        )
    else:
        runtime.app.run(
            host=runtime.settings.server.host,
            port=runtime.settings.server.port,
            debug=runtime.settings.debug,
            ssl_context=(certfile, keyfile),
        )


if __name__ == "__main__":  # pragma: no cover - manual execution
    main()
