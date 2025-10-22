"""Composable builders executed during application bootstrap."""

from __future__ import annotations

from . import http, websocket
from ..runtime import ServerRuntime
from ..services import ServiceContainer


def configure_runtime(runtime: ServerRuntime, services: ServiceContainer) -> None:
    http.configure_http(runtime.app, services)
    websocket.configure_websockets(runtime.socketio, services)


__all__ = ["configure_runtime"]
