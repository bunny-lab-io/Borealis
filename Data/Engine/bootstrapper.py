"""Entrypoint for the Borealis Engine server."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from flask import Flask

from .config import EngineSettings, configure_logging, load_environment
from .interfaces import (
    create_socket_server,
    register_http_interfaces,
    register_ws_interfaces,
)
from .repositories.sqlite import connection as sqlite_connection
from .repositories.sqlite import migrations as sqlite_migrations
from .server import create_app
from .services.container import build_service_container


@dataclass(frozen=True, slots=True)
class EngineRuntime:
    """Aggregated runtime context produced by :func:`bootstrap`."""

    app: Flask
    settings: EngineSettings
    socketio: Optional[object]
    db_factory: sqlite_connection.SQLiteConnectionFactory


def bootstrap() -> EngineRuntime:
    """Construct the Flask application and supporting infrastructure."""

    settings = load_environment()
    logger = configure_logging(settings)
    logger.info("bootstrap-started")

    db_factory = sqlite_connection.connection_factory(settings.database_path)
    if settings.apply_migrations:
        logger.info("migrations-start")
        with sqlite_connection.connection_scope(settings.database_path) as conn:
            sqlite_migrations.apply_all(conn)
        logger.info("migrations-complete")
    else:
        logger.info("migrations-skipped")

    app = create_app(settings, db_factory=db_factory)
    services = build_service_container(settings, db_factory=db_factory, logger=logger.getChild("services"))
    app.extensions["engine_services"] = services
    register_http_interfaces(app, services)
    socketio = create_socket_server(app, settings.socketio)
    register_ws_interfaces(socketio, services)
    logger.info("bootstrap-complete")
    return EngineRuntime(app=app, settings=settings, socketio=socketio, db_factory=db_factory)


def main() -> None:
    runtime = bootstrap()
    socketio = runtime.socketio
    if socketio is not None:
        socketio.run(  # type: ignore[call-arg]
            runtime.app,
            host=runtime.settings.server.host,
            port=runtime.settings.server.port,
            debug=runtime.settings.debug,
        )
    else:
        runtime.app.run(
            host=runtime.settings.server.host,
            port=runtime.settings.server.port,
            debug=runtime.settings.debug,
        )


if __name__ == "__main__":  # pragma: no cover - manual execution
    main()
