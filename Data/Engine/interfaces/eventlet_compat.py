"""Compatibility helpers for running Socket.IO under eventlet."""

from __future__ import annotations

import ssl
from typing import Any

try:  # pragma: no cover - optional dependency
    import eventlet  # type: ignore
except Exception:  # pragma: no cover - optional dependency
    eventlet = None  # type: ignore[assignment]


def _quiet_close(connection: Any) -> None:
    try:
        if hasattr(connection, "close"):
            connection.close()
    except Exception:
        pass


def _close_connection_quietly(protocol: Any) -> None:
    try:
        setattr(protocol, "close_connection", True)
    except Exception:
        pass
    conn = getattr(protocol, "socket", None) or getattr(protocol, "connection", None)
    if conn is not None:
        _quiet_close(conn)


def apply_eventlet_patches() -> None:
    """Apply Borealis-specific eventlet tweaks when the dependency is available."""

    if eventlet is None:  # pragma: no cover - guard for environments without eventlet
        return

    eventlet.monkey_patch(thread=False)

    try:
        from eventlet.wsgi import HttpProtocol  # type: ignore
    except Exception:  # pragma: no cover - import guard
        return

    original = HttpProtocol.handle_one_request  # type: ignore[attr-defined]

    def _handle_one_request(self: Any, *args: Any, **kwargs: Any) -> Any:  # type: ignore[override]
        try:
            return original(self, *args, **kwargs)
        except ssl.SSLError as exc:  # type: ignore[arg-type]
            reason = getattr(exc, "reason", "") or ""
            message = " ".join(str(arg) for arg in exc.args if arg)
            lower_reason = str(reason).lower()
            lower_message = message.lower()
            if (
                "http_request" in lower_message
                or lower_reason == "http request"
                or "unknown ca" in lower_message
                or lower_reason == "unknown ca"
                or "unknown_ca" in lower_message
            ):
                _close_connection_quietly(self)
                return None
            raise
        except ssl.SSLEOFError:
            _close_connection_quietly(self)
            return None
        except ConnectionAbortedError:
            _close_connection_quietly(self)
            return None

    HttpProtocol.handle_one_request = _handle_one_request  # type: ignore[assignment]


__all__ = ["apply_eventlet_patches"]
