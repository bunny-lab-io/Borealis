"""Entrypoint for the Borealis Engine server."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from flask import Flask

from .config import EngineSettings, configure_logging, load_environment
from .interfaces import create_socket_server, register_http_interfaces
from .server import create_app


@dataclass(frozen=True, slots=True)
class EngineRuntime:
    """Aggregated runtime context produced by :func:`bootstrap`."""

    app: Flask
    settings: EngineSettings
    socketio: Optional[object]


def bootstrap() -> EngineRuntime:
    """Construct the Flask application and supporting infrastructure."""

    settings = load_environment()
    logger = configure_logging(settings)
    logger.info("bootstrap-started")
    app = create_app(settings)
    register_http_interfaces(app)
    socketio = create_socket_server(app, settings.socketio)
    logger.info("bootstrap-complete")
    return EngineRuntime(app=app, settings=settings, socketio=socketio)


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
