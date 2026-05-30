# ======================================================
# Data\Engine\bootstrapper.py
# Description: Bootstrap utility that resolves runtime config, stages WebUI assets, and starts the loopback Engine Socket.IO server behind the embedded Traefik edge.
#
# API Endpoints (if applicable): None
# ======================================================

"""Entrypoint helpers for running the Borealis Engine runtime.

The bootstrapper assembles configuration via :func:`Data.Engine.config.load_runtime_config`
before delegating to :func:`Data.Engine.server.create_app`. The shipped Engine
runtime is fixed to loopback HTTP on ``127.0.0.1:5000`` and expects the
embedded Borealis Traefik edge to own all public HTTPS traffic.
"""

from __future__ import annotations

import logging
import os
import shutil
import subprocess
import time
from pathlib import Path
from typing import Any, Dict, Optional

from .config import initialise_engine_logger, load_runtime_config
from .server import EngineContext, create_app


DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 5000


def _bootstrap_logger() -> logging.Logger:
    """Create a bootstrap logger that writes to Engine log files."""

    settings = load_runtime_config()
    return initialise_engine_logger(settings, name="borealis.engine.bootstrap")


def _project_root() -> Path:
    """Locate the repository root by discovering the Borealis Agent entrypoint."""

    current = Path(__file__).resolve().parent

    for candidate in (current, *current.parents):
        if (candidate / "Agent.exe").is_file():
            return candidate

    raise RuntimeError("Unable to locate the Borealis project root; Agent.exe was not found in any parent directory.")


def _build_runtime_config() -> Dict[str, Any]:
    api_groups_override = os.environ.get("BOREALIS_ENGINE_API_GROUPS")
    if api_groups_override:
        api_groups: Any = api_groups_override
    else:
        # Keep this startup default in sync with Data.Engine.config and API.DEFAULT_API_GROUPS.
        api_groups = (
            "core",
            "auth",
            "tokens",
            "enrollment",
            "devices",
            "filters",
            "server",
            "assemblies",
            "workflows",
            "scheduled_jobs",
            "notifications",
        )

    return {
        "HOST": DEFAULT_HOST,
        "PORT": DEFAULT_PORT,
        "API_GROUPS": api_groups,
    }


def _stage_web_interface_assets(logger: Optional[logging.Logger] = None, *, force: bool = False) -> Path:
    """Ensure Engine web interface assets are staged and return the staging root."""

    if logger is None:
        logger = _bootstrap_logger()

    project_root = _project_root()
    engine_web_root = project_root / "Engine" / "Services" / "webui-frontend" / "cache" / "web-interface"
    modern_source = project_root / "Data" / "Engine" / "Containers" / "webui-frontend" / "data" / "web-interface"

    def _latest_mtime(root: Path) -> Optional[float]:
        """Return the most recent mtime under root, excluding build artifacts."""

        try:
            candidates = []
            for path in root.rglob("*"):
                # Skip heavy/ephemeral directories so we only compare source files
                if any(part in {"node_modules", "build", "dist"} for part in path.parts):
                    continue
                try:
                    stat = path.stat()
                except OSError:
                    continue
                if path.is_file():
                    candidates.append(stat.st_mtime)
            return max(candidates) if candidates else None
        except Exception:
            return None

    if not modern_source.is_dir():
        raise RuntimeError(
            f"Engine web interface source missing: {modern_source}"
        )

    index_path = engine_web_root / "index.html"
    if engine_web_root.exists() and index_path.is_file() and not force:
        source_mtime = _latest_mtime(modern_source)
        dest_mtime = _latest_mtime(engine_web_root)
        if dest_mtime is not None and source_mtime is not None and dest_mtime >= source_mtime:
            logger.info("Engine web interface already staged at %s; skipping copy (destination up-to-date).", engine_web_root)
            return engine_web_root
        logger.info(
            "Engine web interface detected newer source assets; refreshing staging directory %s.",
            engine_web_root,
        )

    if engine_web_root.exists():
        shutil.rmtree(engine_web_root)

    shutil.copytree(modern_source, engine_web_root)

    if not index_path.is_file():
        raise RuntimeError(
            f"Engine web interface staging failed; missing {index_path}"
        )

    logger.info("Engine web interface staged from %s to %s", modern_source, engine_web_root)
    return engine_web_root


def _determine_static_folder(staging_root: Path) -> str:
    for candidate in (staging_root / "build", staging_root / "dist", staging_root):
        if candidate.is_dir():
            return str(candidate)
    return str(staging_root)


def _resolve_npm_executable() -> str:
    env_cmd = os.environ.get("BOREALIS_NPM_CMD")
    if env_cmd:
        candidate = Path(env_cmd).expanduser()
        if candidate.is_file():
            return str(candidate)

    node_dir = os.environ.get("BOREALIS_NODE_DIR")
    if node_dir:
        candidate = Path(node_dir) / "npm.cmd"
        if candidate.is_file():
            return str(candidate)
        candidate = Path(node_dir) / "npm.exe"
        if candidate.is_file():
            return str(candidate)
        candidate = Path(node_dir) / "npm"
        if candidate.is_file():
            return str(candidate)

    if os.name == "nt":
        return "npm.cmd"
    return "npm"


def _resolve_npx_executable() -> str:
    env_cmd = os.environ.get("BOREALIS_NPX_CMD")
    if env_cmd:
        candidate = Path(env_cmd).expanduser()
        if candidate.is_file():
            return str(candidate)

    node_dir = os.environ.get("BOREALIS_NODE_DIR")
    if node_dir:
        candidate = Path(node_dir) / "npx.cmd"
        if candidate.is_file():
            return str(candidate)
        candidate = Path(node_dir) / "npx"
        if candidate.is_file():
            return str(candidate)

    if os.name == "nt":
        return "npx.cmd"
    return "npx"


def _run_npm(args: list[str], cwd: Path, logger: logging.Logger) -> None:
    command = [_resolve_npm_executable()] + args
    logger.info("Running npm command: %s", " ".join(command))
    start = time.time()
    try:
        completed = subprocess.run(command, cwd=str(cwd), capture_output=True, text=True, check=False)
    except FileNotFoundError as exc:
        raise RuntimeError("npm executable not found; ensure Node.js dependencies are installed.") from exc
    duration = time.time() - start
    if completed.returncode != 0:
        logger.error(
            "npm command failed (%ss): %s\nstdout: %s\nstderr: %s",
            f"{duration:.2f}",
            " ".join(command),
            completed.stdout.strip(),
            completed.stderr.strip(),
        )
        raise RuntimeError(f"npm command {' '.join(command)} failed with exit code {completed.returncode}")
    stdout = completed.stdout.strip()
    if stdout:
        logger.debug("npm stdout: %s", stdout)
    stderr = completed.stderr.strip()
    if stderr:
        logger.debug("npm stderr: %s", stderr)
    logger.info("npm command completed in %.2fs", duration)


def _run_vite(args: list[str], cwd: Path, logger: logging.Logger) -> None:
    command = [_resolve_npx_executable(), "vite"] + args
    logger.info("Running Vite command: %s", " ".join(command))
    start = time.time()
    try:
        completed = subprocess.run(command, cwd=str(cwd), capture_output=True, text=True, check=False)
    except FileNotFoundError as exc:
        raise RuntimeError("npx executable not found; ensure Node.js dependencies are installed.") from exc
    duration = time.time() - start
    if completed.returncode != 0:
        logger.error(
            "Vite command failed (%ss): %s\nstdout: %s\nstderr: %s",
            f"{duration:.2f}",
            " ".join(command),
            (completed.stdout or "").strip(),
            (completed.stderr or "").strip(),
        )
        raise RuntimeError(f"Vite command {' '.join(command)} failed with exit code {completed.returncode}")
    stdout = (completed.stdout or "").strip()
    if stdout:
        logger.debug("Vite stdout: %s", stdout)
    stderr = (completed.stderr or "").strip()
    if stderr:
        logger.debug("Vite stderr: %s", stderr)
    logger.info("Vite command completed in %.2fs", duration)


def _ensure_web_ui_build(staging_root: Path, logger: logging.Logger, *, mode: str) -> str:
    package_json = staging_root / "package.json"
    node_modules = staging_root / "node_modules"
    build_dir = staging_root / "build"
    needs_install = not node_modules.is_dir()
    needs_build = not build_dir.is_dir()

    def _latest_source_mtime(root: Path) -> Optional[float]:
        """Return the latest source/config mtime under the WebUI staging root."""

        try:
            latest: Optional[float] = None
            for path in root.rglob("*"):
                if any(part in {"node_modules", "build", "dist"} for part in path.parts):
                    continue
                if not path.is_file():
                    continue
                try:
                    mtime = path.stat().st_mtime
                except OSError:
                    continue
                latest = mtime if latest is None else max(latest, mtime)
            return latest
        except Exception:
            return None

    if not needs_build and package_json.is_file():
        build_index = build_dir / "index.html"
        if build_index.is_file():
            build_mtime = build_index.stat().st_mtime
            source_mtime = _latest_source_mtime(staging_root)
            package_mtime = package_json.stat().st_mtime
            comparison_mtime = max(package_mtime, source_mtime or package_mtime)
            needs_build = build_mtime < comparison_mtime
        else:
            needs_build = True

    if mode == "developer":
        logger.info("Engine launched in developer mode; skipping WebUI build step.")
        return _determine_static_folder(staging_root)

    if not package_json.is_file():
        logger.warning("WebUI package.json not found at %s; continuing without rebuild.", package_json)
        return _determine_static_folder(staging_root)

    if needs_install:
        _run_npm(["install", "--silent", "--no-fund", "--audit=false"], staging_root, logger)

    if needs_build:
        _run_vite(["build"], staging_root, logger)
    else:
        logger.info("Existing WebUI build found at %s; reuse.", build_dir)

    return _determine_static_folder(staging_root)


def main() -> None:
    config = _build_runtime_config()
    bootstrap_logger = _bootstrap_logger()
    mode = os.environ.get("BOREALIS_ENGINE_MODE", "production").strip().lower() or "production"
    external_webui = str(os.environ.get("BOREALIS_WEBUI_EXTERNAL") or "").strip().lower() in {"1", "true", "yes", "on"}
    staging_root: Optional[Path] = None
    if external_webui:
        bootstrap_logger.info("External WebUI service enabled; skipping Engine-side WebUI staging/build.")
    else:
        try:
            staging_root = _stage_web_interface_assets(logger=bootstrap_logger)
        except Exception as exc:
            bootstrap_logger.error(
                "Failed to stage Engine web interface: %s", exc
            )
            raise

    if staging_root:
        static_folder = _ensure_web_ui_build(staging_root, bootstrap_logger, mode=mode)
        config.setdefault("STATIC_FOLDER", static_folder)

    app, socketio, context = create_app(config)

    if staging_root:
        context.logger.info("Engine WebUI assets ready at %s", config["STATIC_FOLDER"])

    host = config.get("HOST", DEFAULT_HOST)
    port = int(config.get("PORT", DEFAULT_PORT))

    run_kwargs: Dict[str, Any] = {"host": host, "port": port}
    context.logger.info(
        "Engine loopback runtime ready on http://%s:%s behind the embedded Borealis Traefik edge.",
        host,
        port,
    )

    if getattr(socketio, "async_mode", "") == "threading":
        run_kwargs["allow_unsafe_werkzeug"] = True
        context.logger.warning(
            "Socket.IO is running in threading mode; allowing Werkzeug because the public Borealis edge handles front-end traffic."
        )

    try:
        socketio.run(app, **run_kwargs)
    except BaseException:
        context.logger.exception("Engine loopback runtime crashed.")
        raise
    finally:
        context.logger.error("Engine loopback runtime stopped.")


if __name__ == "__main__":  # pragma: no cover - manual launch helper
    main()
