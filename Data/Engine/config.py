"""Configuration helpers for the Borealis Engine runtime.

Stage 2 of the migration focuses on lifting the legacy configuration loading
behaviour from :mod:`Data.Server.server` into reusable helpers so the Engine
start-up path honours the same environment variables, filesystem layout, and
logging expectations.  This module documents the supported launch parameters
and exposes typed helpers that the application factory consumes.

Launch overview
---------------
The Engine can be started via :func:`Data.Engine.bootstrapper.main` or by
invoking :func:`Data.Engine.server.create_app` manually.  Configuration is
assembled from (in precedence order):

``config`` mapping overrides provided to :func:`load_runtime_config`,
environment variables prefixed with ``BOREALIS_``, and finally built-in
defaults that mirror the legacy server runtime.  Key environment variables are

``BOREALIS_DATABASE_PATH``  path to the SQLite database file.  Defaults to
``<ProjectRoot>/database.db``.
``BOREALIS_CORS_ORIGINS``   comma separated list of allowed origins for CORS.
``BOREALIS_SECRET``         Flask session secret key.
``BOREALIS_COOKIE_*``       Session cookie policies (``SAMESITE``, ``SECURE``,
                            ``DOMAIN``).
``BOREALIS_TLS_*``          TLS certificate, private key, and bundle paths.

When TLS values are not provided explicitly the Engine falls back to the
certificate helper shipped with the legacy server, ensuring bundling parity.
Logs are written to ``Logs/Engine/engine.log`` with daily rotation and
errors are additionally duplicated to ``Logs/Engine/error.log`` so the
runtime integrates with the platform's logging policy.
"""

from __future__ import annotations

import logging
import os
from dataclasses import asdict, dataclass, field
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Any, Iterable, List, Mapping, MutableMapping, Optional, Sequence, Tuple

try:  # pragma: no-cover - optional dependency during early migration stages.
    from Modules.crypto import certificates  # type: ignore
except Exception:  # pragma: no-cover - Engine configuration still works without it.
    certificates = None  # type: ignore[assignment]


ENGINE_DIR = Path(__file__).resolve().parent


def _discover_project_root() -> Path:
    """Locate the project root by searching for Borealis.ps1 or using overrides."""

    env_override = os.environ.get("BOREALIS_PROJECT_ROOT")
    if env_override:
        env_path = Path(env_override).expanduser().resolve()
        if env_path.is_dir():
            return env_path

    current = ENGINE_DIR
    for candidate in (current, *current.parents):
        if (candidate / "Borealis.ps1").is_file():
            return candidate

    return ENGINE_DIR.parent.parent


PROJECT_ROOT = _discover_project_root()
DEFAULT_DATABASE_PATH = PROJECT_ROOT / "database.db"
LOG_ROOT = PROJECT_ROOT / "Logs" / "Engine"
LOG_FILE_PATH = LOG_ROOT / "engine.log"
ERROR_LOG_FILE_PATH = LOG_ROOT / "error.log"
API_LOG_FILE_PATH = LOG_ROOT / "api.log"


def _ensure_parent(path: Path) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
    except Exception:
        # Directory creation failure is non-fatal; subsequent file operations
        # will surface the issue with clearer context.
        pass


def _resolve_static_folder() -> str:
    candidate_roots = [
        PROJECT_ROOT / "Engine" / "web-interface",
        PROJECT_ROOT / "web-interface",
        ENGINE_DIR.parent / "Engine" / "web-interface",
        ENGINE_DIR.parent / "web-interface",
        ENGINE_DIR / "web-interface",
        PROJECT_ROOT / "Data" / "Server" / "web-interface",
    ]

    resolved_roots: List[Path] = []
    for root in candidate_roots:
        absolute_root = root.expanduser().resolve()
        if absolute_root not in resolved_roots:
            resolved_roots.append(absolute_root)

    for root in resolved_roots:
        for candidate in (root / "build", root / "dist", root):
            if candidate.is_dir():
                return str(candidate)

    if resolved_roots:
        return str(resolved_roots[0])

    return str((PROJECT_ROOT / "Engine" / "web-interface").resolve())


def _parse_origins(raw: Optional[Any]) -> Optional[List[str]]:
    if raw is None:
        return None
    if isinstance(raw, str):
        parts = [part.strip() for part in raw.split(",")]
    elif isinstance(raw, Sequence):
        parts = [str(part).strip() for part in raw]
    else:
        return None
    origins = [part for part in parts if part]
    return origins or None


def _parse_bool(raw: Any, *, default: bool = False) -> bool:
    if raw is None:
        return default
    if isinstance(raw, bool):
        return raw
    lowered = str(raw).strip().lower()
    if lowered in {"1", "true", "yes", "on"}:
        return True
    if lowered in {"0", "false", "no", "off"}:
        return False
    return default


def _discover_tls_material(config: Mapping[str, Any]) -> Sequence[Optional[str]]:
    cert_path = config.get("TLS_CERT_PATH") or os.environ.get("BOREALIS_TLS_CERT") or None
    key_path = config.get("TLS_KEY_PATH") or os.environ.get("BOREALIS_TLS_KEY") or None
    bundle_path = config.get("TLS_BUNDLE_PATH") or os.environ.get("BOREALIS_TLS_BUNDLE") or None

    if certificates and not all([cert_path, key_path, bundle_path]):
        try:
            auto_cert, auto_key, auto_bundle = certificates.certificate_paths()
        except Exception:
            auto_cert = auto_key = auto_bundle = None
        else:
            cert_path = cert_path or auto_cert
            key_path = key_path or auto_key
            bundle_path = bundle_path or auto_bundle

    if cert_path:
        os.environ.setdefault("BOREALIS_TLS_CERT", str(cert_path))
    if key_path:
        os.environ.setdefault("BOREALIS_TLS_KEY", str(key_path))
    if bundle_path:
        os.environ.setdefault("BOREALIS_TLS_BUNDLE", str(bundle_path))

    return cert_path, key_path, bundle_path


@dataclass
class EngineSettings:
    """Resolved configuration values for the Engine runtime."""

    database_path: str
    static_folder: str
    cors_origins: Optional[List[str]]
    secret_key: str
    session_cookie_samesite: str
    session_cookie_secure: bool
    session_cookie_domain: Optional[str]
    tls_cert_path: Optional[str]
    tls_key_path: Optional[str]
    tls_bundle_path: Optional[str]
    log_file: str
    error_log_file: str
    api_log_file: str
    api_groups: Tuple[str, ...]
    raw: MutableMapping[str, Any] = field(default_factory=dict)

    def to_flask_config(self) -> MutableMapping[str, Any]:
        config: MutableMapping[str, Any] = {
            "SESSION_COOKIE_HTTPONLY": True,
            "SESSION_COOKIE_SAMESITE": self.session_cookie_samesite,
            "SESSION_COOKIE_SECURE": self.session_cookie_secure,
            "PREFERRED_URL_SCHEME": "https",
        }
        if self.session_cookie_domain:
            config["SESSION_COOKIE_DOMAIN"] = self.session_cookie_domain
        return config

    def as_dict(self) -> MutableMapping[str, Any]:
        data = asdict(self)
        data["raw"] = dict(self.raw)
        return data


def _parse_api_groups(raw: Optional[Any]) -> Tuple[str, ...]:
    if raw is None:
        return tuple()
    if isinstance(raw, str):
        parts: Iterable[str] = (part.strip() for part in raw.split(","))
    elif isinstance(raw, Sequence):
        parts = (str(part).strip() for part in raw)
    else:
        return tuple()
    cleaned = [part.lower() for part in parts if part]
    if "auth" not in cleaned:
        cleaned.insert(0, "auth")
    return tuple(dict.fromkeys(cleaned))


def load_runtime_config(overrides: Optional[Mapping[str, Any]] = None) -> EngineSettings:
    """Resolve Engine configuration values.

    Parameters
    ----------
    overrides:
        Optional mapping of explicit configuration values.  These take
        precedence over environment variables and built-in defaults.
    """

    runtime_config: MutableMapping[str, Any] = dict(overrides or {})

    database_path = str(
        runtime_config.get("DATABASE_PATH")
        or os.environ.get("BOREALIS_DATABASE_PATH")
        or DEFAULT_DATABASE_PATH
    )
    database_path = os.path.abspath(database_path)
    _ensure_parent(Path(database_path))

    static_folder = str(runtime_config.get("STATIC_FOLDER") or _resolve_static_folder())

    cors_origins = _parse_origins(
        runtime_config.get("CORS_ORIGINS") or os.environ.get("BOREALIS_CORS_ORIGINS")
    )

    secret_key = str(runtime_config.get("SECRET_KEY") or os.environ.get("BOREALIS_SECRET") or "borealis-dev-secret")

    session_cookie_samesite = str(
        runtime_config.get("SESSION_COOKIE_SAMESITE")
        or os.environ.get("BOREALIS_COOKIE_SAMESITE")
        or "Lax"
    )

    session_cookie_secure = _parse_bool(
        runtime_config.get("SESSION_COOKIE_SECURE"),
        default=_parse_bool(os.environ.get("BOREALIS_COOKIE_SECURE"), default=False),
    )

    session_cookie_domain = runtime_config.get("SESSION_COOKIE_DOMAIN") or os.environ.get("BOREALIS_COOKIE_DOMAIN")
    session_cookie_domain = str(session_cookie_domain) if session_cookie_domain else None

    tls_cert_path, tls_key_path, tls_bundle_path = _discover_tls_material(runtime_config)

    log_file = str(runtime_config.get("LOG_FILE") or LOG_FILE_PATH)
    _ensure_parent(Path(log_file))

    error_log_file = str(runtime_config.get("ERROR_LOG_FILE") or ERROR_LOG_FILE_PATH)
    _ensure_parent(Path(error_log_file))

    api_log_file = str(runtime_config.get("API_LOG_FILE") or API_LOG_FILE_PATH)
    _ensure_parent(Path(api_log_file))

    api_groups = _parse_api_groups(
        runtime_config.get("API_GROUPS") or os.environ.get("BOREALIS_API_GROUPS")
    )
    if not api_groups:
        api_groups = ("auth", "tokens", "enrollment")

    settings = EngineSettings(
        database_path=database_path,
        static_folder=static_folder,
        cors_origins=cors_origins,
        secret_key=secret_key,
        session_cookie_samesite=session_cookie_samesite,
        session_cookie_secure=session_cookie_secure,
        session_cookie_domain=session_cookie_domain,
        tls_cert_path=tls_cert_path if tls_cert_path else None,
        tls_key_path=tls_key_path if tls_key_path else None,
        tls_bundle_path=tls_bundle_path if tls_bundle_path else None,
        log_file=str(log_file),
        error_log_file=str(error_log_file),
        api_log_file=str(api_log_file),
        api_groups=api_groups,
        raw=runtime_config,
    )
    return settings


def initialise_engine_logger(settings: EngineSettings, name: str = "borealis.engine") -> logging.Logger:
    """Configure the Engine logger to write to Engine log files."""

    logger = logging.getLogger(name)
    if not logger.handlers:
        formatter = logging.Formatter("%(asctime)s-%(name)s-%(levelname)s: %(message)s")

        stream_handler = logging.StreamHandler()
        stream_handler.setFormatter(formatter)
        logger.addHandler(stream_handler)

        file_handler = TimedRotatingFileHandler(
            settings.log_file,
            when="midnight",
            backupCount=0,
            encoding="utf-8",
        )
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)

        error_handler = TimedRotatingFileHandler(
            settings.error_log_file,
            when="midnight",
            backupCount=0,
            encoding="utf-8",
        )
        error_handler.setLevel(logging.ERROR)
        error_handler.setFormatter(formatter)
        logger.addHandler(error_handler)

    logger.setLevel(logging.INFO)
    logger.propagate = False
    return logger


__all__ = [
    "EngineSettings",
    "initialise_engine_logger",
    "load_runtime_config",
]
