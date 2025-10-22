"""Runtime entry point for the modular Borealis server."""

from __future__ import annotations

from typing import Any, Dict

try:  # pragma: no cover - import shim for script execution
    from .bootstrapper import bootstrap  # type: ignore
    from .app.runtime import monolith as active_monolith  # type: ignore
except ImportError:  # pragma: no cover - executed when run as script
    import pathlib
    import sys

    current_dir = pathlib.Path(__file__).resolve().parent
    if str(current_dir) not in sys.path:
        sys.path.insert(0, str(current_dir))

    from bootstrapper import bootstrap  # type: ignore
    from app.runtime import monolith as active_monolith  # type: ignore

app, socketio, services, scheduler = bootstrap()
__all__ = ["app", "socketio", "services", "scheduler", "main"]


def main(**kwargs: Dict[str, Any]) -> None:
    """Launch the Socket.IO server using the bundled TLS configuration."""

    certfile = getattr(active_monolith, "TLS_BUNDLE_PATH", None)
    keyfile = getattr(active_monolith, "TLS_KEY_PATH", None)
    socketio.run(
        app,
        host=kwargs.get("host", "0.0.0.0"),
        port=kwargs.get("port", 5000),
        certfile=kwargs.get("certfile", certfile),
        keyfile=kwargs.get("keyfile", keyfile),
    )


if __name__ == "__main__":
    main()
