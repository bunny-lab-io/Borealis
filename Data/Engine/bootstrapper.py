"""Command-line bootstrapper for the Stage 1 Engine runtime."""
from __future__ import annotations

import os
from typing import Any, Dict

from .server import create_app


DEFAULT_HOST = "0.0.0.0"
DEFAULT_PORT = 5001


def _build_runtime_config() -> Dict[str, Any]:
    return {
        "HOST": os.environ.get("BOREALIS_ENGINE_HOST", DEFAULT_HOST),
        "PORT": int(os.environ.get("BOREALIS_ENGINE_PORT", DEFAULT_PORT)),
        "TLS_CERT_PATH": os.environ.get("BOREALIS_TLS_CERT"),
        "TLS_KEY_PATH": os.environ.get("BOREALIS_TLS_KEY"),
        "TLS_BUNDLE_PATH": os.environ.get("BOREALIS_TLS_BUNDLE"),
    }


def main() -> None:
    config = _build_runtime_config()
    app, socketio, context = create_app(config)

    host = config.get("HOST", DEFAULT_HOST)
    port = int(config.get("PORT", DEFAULT_PORT))

    run_kwargs: Dict[str, Any] = {"host": host, "port": port}
    if context.tls_bundle_path and context.tls_key_path:
        run_kwargs.update({"certfile": context.tls_bundle_path, "keyfile": context.tls_key_path})

    socketio.run(app, **run_kwargs)


if __name__ == "__main__":  # pragma: no cover - manual launch helper
    main()
