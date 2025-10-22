"""Interface adapters (HTTP, WebSocket, etc.) for the Borealis Engine."""

from __future__ import annotations

from .http import register_http_interfaces
from .ws import create_socket_server

__all__ = [
    "register_http_interfaces",
    "create_socket_server",
]
