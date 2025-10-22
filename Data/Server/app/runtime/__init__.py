"""Runtime adapters exposing the current Borealis server implementation."""

from __future__ import annotations

from typing import NamedTuple

from flask import Flask
from flask_socketio import SocketIO


class ServerRuntime(NamedTuple):
    app: Flask
    socketio: SocketIO
    scheduler: object


def load_runtime() -> ServerRuntime:
    from . import monolith  # noqa: WPS433

    scheduler = getattr(monolith, "job_scheduler", None)
    return ServerRuntime(monolith.app, monolith.socketio, scheduler)
