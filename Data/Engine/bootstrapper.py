"""Entrypoint helpers for running the Borealis Engine runtime.

The bootstrapper assembles configuration via :func:`Data.Engine.config.load_runtime_config`
before delegating to :func:`Data.Engine.server.create_app`.  It mirrors the
legacy server defaults by binding to ``0.0.0.0:5001`` and honouring the
``BOREALIS_ENGINE_*`` environment overrides for bind host/port.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any, Dict

from .server import EngineContext, create_app


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


def _prepare_tls_run_kwargs(context: EngineContext) -> Dict[str, Any]:
    """Validate and return TLS arguments for the Socket.IO runner."""

    run_kwargs: Dict[str, Any] = {}

    key_path_value = context.tls_key_path
    if not key_path_value:
        return run_kwargs

    key_path = Path(key_path_value)
    if not key_path.is_file():
        raise RuntimeError(f"Engine TLS key file not found: {key_path}")

    cert_candidates = []
    if context.tls_bundle_path:
        cert_candidates.append(context.tls_bundle_path)
    if context.tls_cert_path and context.tls_cert_path not in cert_candidates:
        cert_candidates.append(context.tls_cert_path)

    if not cert_candidates:
        raise RuntimeError("Engine TLS certificate path not configured; ensure certificates are provisioned.")

    missing_candidates = []
    for candidate in cert_candidates:
        candidate_path = Path(candidate)
        if candidate_path.is_file():
            run_kwargs["certfile"] = str(candidate_path)
            run_kwargs["keyfile"] = str(key_path)
            return run_kwargs
        missing_candidates.append(str(candidate_path))

    checked = ", ".join(missing_candidates)
    raise RuntimeError(f"Engine TLS certificate file not found. Checked: {checked}")


def main() -> None:
    config = _build_runtime_config()
    app, socketio, context = create_app(config)

    host = config.get("HOST", DEFAULT_HOST)
    port = int(config.get("PORT", DEFAULT_PORT))

    run_kwargs: Dict[str, Any] = {"host": host, "port": port}
    try:
        tls_kwargs = _prepare_tls_run_kwargs(context)
    except RuntimeError as exc:
        context.logger.error("TLS configuration error: %s", exc)
        raise
    else:
        if tls_kwargs:
            run_kwargs.update(tls_kwargs)
            context.logger.info("Engine TLS enabled using certificate %s", tls_kwargs["certfile"])

    socketio.run(app, **run_kwargs)


if __name__ == "__main__":  # pragma: no cover - manual launch helper
    main()
