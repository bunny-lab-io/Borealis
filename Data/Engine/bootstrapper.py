"""Entrypoint helpers for running the Borealis Engine runtime.

The bootstrapper assembles configuration via :func:`Data.Engine.config.load_runtime_config`
before delegating to :func:`Data.Engine.server.create_app`.  It mirrors the
legacy server defaults by binding to ``0.0.0.0:5001`` and honouring the
``BOREALIS_ENGINE_*`` environment overrides for bind host/port.
"""

from __future__ import annotations

import logging
import os
import shutil
from pathlib import Path
from typing import Any, Dict, Set

from .server import EngineContext, create_app


DEFAULT_HOST = "0.0.0.0"
DEFAULT_PORT = 5001


def _project_root() -> Path:
    return Path(__file__).resolve().parents[2]


def _build_runtime_config() -> Dict[str, Any]:
    return {
        "HOST": os.environ.get("BOREALIS_ENGINE_HOST", DEFAULT_HOST),
        "PORT": int(os.environ.get("BOREALIS_ENGINE_PORT", DEFAULT_PORT)),
        "TLS_CERT_PATH": os.environ.get("BOREALIS_TLS_CERT"),
        "TLS_KEY_PATH": os.environ.get("BOREALIS_TLS_KEY"),
        "TLS_BUNDLE_PATH": os.environ.get("BOREALIS_TLS_BUNDLE"),
    }


def _stage_web_interface_assets(logger: logging.Logger) -> None:
    project_root = _project_root()
    engine_web_root = project_root / "Engine" / "web-interface"
    legacy_source = project_root / "Data" / "Server" / "WebUI"

    package_json = engine_web_root / "package.json"
    src_dir = engine_web_root / "src"

    if package_json.is_file() and src_dir.is_dir():
        return

    if not legacy_source.is_dir():
        logger.warning(
            "Legacy WebUI source missing; unable to stage Engine web interface from %s",
            legacy_source,
        )
        return

    engine_web_root.mkdir(parents=True, exist_ok=True)

    preserved: Set[str] = {".gitignore", "README.md"}
    for child in engine_web_root.iterdir():
        if child.name in preserved:
            continue
        if child.is_dir():
            shutil.rmtree(child, ignore_errors=True)
        else:
            try:
                child.unlink()
            except FileNotFoundError:
                continue

    for item in legacy_source.iterdir():
        destination = engine_web_root / item.name
        if item.is_dir():
            shutil.copytree(item, destination, dirs_exist_ok=True)
        else:
            shutil.copy2(item, destination)

    if not package_json.is_file() or not src_dir.is_dir():
        raise RuntimeError(
            f"Failed to stage Engine web interface assets into {engine_web_root}"
        )

    logger.info(
        "Engine web interface staged from %s to %s", legacy_source, engine_web_root
    )


def _ensure_tls_material(context: EngineContext) -> None:
    """Ensure TLS certificate material exists, updating the context if created."""

    try:  # Lazy import so Engine still starts if legacy modules are unavailable.
        from Modules.crypto import certificates  # type: ignore
    except Exception:
        return

    try:
        cert_path, key_path, bundle_path = certificates.ensure_certificate()
    except Exception as exc:
        context.logger.error("Failed to auto-provision Engine TLS certificates: %s", exc)
        return

    cert_path_str = str(cert_path)
    key_path_str = str(key_path)
    bundle_path_str = str(bundle_path)

    if not context.tls_cert_path or not Path(context.tls_cert_path).is_file():
        context.tls_cert_path = cert_path_str
    if not context.tls_key_path or not Path(context.tls_key_path).is_file():
        context.tls_key_path = key_path_str
    if not context.tls_bundle_path or not Path(context.tls_bundle_path).is_file():
        context.tls_bundle_path = bundle_path_str


def _prepare_tls_run_kwargs(context: EngineContext) -> Dict[str, Any]:
    """Validate and return TLS arguments for the Socket.IO runner."""

    _ensure_tls_material(context)

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

    try:
        _stage_web_interface_assets(context.logger)
    except Exception as exc:
        context.logger.error("Failed to stage Engine web interface: %s", exc)
        raise

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
