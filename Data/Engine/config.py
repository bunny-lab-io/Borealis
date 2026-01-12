# ======================================================
# Data\Engine\config.py
# Description: Configuration loader aligning the Engine runtime with legacy defaults, logging policy, and TLS discovery.
#
# API Endpoints (if applicable): None
# ======================================================

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
``<ProjectRoot>/Engine/database.db`` so data persists across Engine redeploys.
``BOREALIS_CORS_ORIGINS``   comma separated list of allowed origins for CORS.
``BOREALIS_SECRET``         Flask session secret key.
``BOREALIS_COOKIE_*``       Session cookie policies (``SAMESITE``, ``SECURE``,
                            ``DOMAIN``).
``BOREALIS_TLS_*``          TLS certificate, private key, and bundle paths.

When TLS values are not provided explicitly the Engine provisions certificates
under ``Engine/Certificates`` (migrating any legacy material) so the runtime
remains self-contained.
Logs are written to ``Engine/Logs/engine.log`` with daily rotation and
errors are additionally duplicated to ``Engine/Logs/error.log`` so the
runtime integrates with the platform's logging policy.
"""

from __future__ import annotations

import logging
import os
from dataclasses import asdict, dataclass, field
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Any, Iterable, List, Mapping, MutableMapping, Optional, Sequence, Tuple

from .security import certificates


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
DEFAULT_DATABASE_PATH = PROJECT_ROOT / "Engine" / "database.db"
LOG_ROOT = PROJECT_ROOT / "Engine" / "Logs"
LOG_FILE_PATH = LOG_ROOT / "engine.log"
ERROR_LOG_FILE_PATH = LOG_ROOT / "error.log"
API_LOG_FILE_PATH = LOG_ROOT / "api.log"
VPN_TUNNEL_LOG_FILE_PATH = LOG_ROOT / "VPN_Tunnel" / "tunnel.log"
DEFAULT_WIREGUARD_PORT = 30000
DEFAULT_WIREGUARD_ENGINE_VIRTUAL_IP = "10.255.0.1/32"
DEFAULT_WIREGUARD_PEER_NETWORK = "10.255.0.0/24"
DEFAULT_WIREGUARD_SHELL_PORT = 47002
DEFAULT_WIREGUARD_ACL_WINDOWS = (3389, 5985, 5986, 5900, 3478, DEFAULT_WIREGUARD_SHELL_PORT)
VPN_SERVER_CERT_ROOT = PROJECT_ROOT / "Engine" / "Certificates" / "VPN_Server"


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


def _parse_int(
    raw: Any,
    *,
    default: int,
    minimum: Optional[int] = None,
    maximum: Optional[int] = None,
) -> int:
    try:
        value = int(raw)
    except Exception:
        return default
    if minimum is not None and value < minimum:
        return default
    if maximum is not None and value > maximum:
        return default
    return value


def _parse_port_range(
    raw: Any,
    *,
    default: Tuple[int, int],
) -> Tuple[int, int]:
    if raw is None:
        return default

    start, end = default
    candidate: Optional[Tuple[int, int]] = None

    def _clamp_pair(values: Tuple[int, int]) -> Tuple[int, int]:
        low, high = values
        if low < 1 or high > 65535 or low > high:
            return default
        return low, high

    if isinstance(raw, str):
        separators = ("-", ":", ",")
        for separator in separators:
            if separator in raw:
                parts = [part.strip() for part in raw.split(separator)]
                break
        else:
            parts = [raw.strip()]
        try:
            if len(parts) == 2:
                candidate = (int(parts[0]), int(parts[1]))
            elif len(parts) == 1 and parts[0]:
                port = int(parts[0])
                candidate = (port, port)
        except Exception:
            candidate = None
    elif isinstance(raw, Sequence):
        try:
            values = [int(part) for part in raw]
        except Exception:
            values = []
        if len(values) >= 2:
            candidate = (values[0], values[1])

    if candidate is None:
        return default

    return _clamp_pair(candidate)


def _parse_port_list(raw: Any, *, default: Tuple[int, ...]) -> Tuple[int, ...]:
    if raw is None:
        return default
    ports: List[int] = []
    if isinstance(raw, str):
        parts = [part.strip() for part in raw.split(",") if part.strip()]
    elif isinstance(raw, Sequence):
        parts = [str(part).strip() for part in raw if str(part).strip()]
    else:
        parts = []
    for part in parts:
        try:
            value = int(part)
        except Exception:
            continue
        if 1 <= value <= 65535:
            ports.append(value)
    if not ports:
        return default
    return tuple(dict.fromkeys(ports))


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
    vpn_tunnel_log_file: str
    wireguard_port: int
    wireguard_engine_virtual_ip: str
    wireguard_peer_network: str
    wireguard_server_private_key_path: str
    wireguard_server_public_key_path: str
    wireguard_acl_allowlist_windows: Tuple[int, ...]
    wireguard_shell_port: int
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

    required_prefix: List[str] = []
    for required in ("core", "auth"):
        if required not in cleaned:
            required_prefix.append(required)
    if required_prefix:
        cleaned = required_prefix + cleaned

    if "assemblies" not in cleaned:
        cleaned.append("assemblies")

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

    vpn_tunnel_log_file = str(
        runtime_config.get("VPN_TUNNEL_LOG_FILE")
        or runtime_config.get("WIREGUARD_LOG_FILE")
        or os.environ.get("BOREALIS_VPN_TUNNEL_LOG_FILE")
        or os.environ.get("BOREALIS_WIREGUARD_LOG_FILE")
        or VPN_TUNNEL_LOG_FILE_PATH
    )
    _ensure_parent(Path(vpn_tunnel_log_file))

    wireguard_port = _parse_int(
        runtime_config.get("WIREGUARD_PORT") or os.environ.get("BOREALIS_WIREGUARD_PORT"),
        default=DEFAULT_WIREGUARD_PORT,
        minimum=1,
        maximum=65535,
    )
    wireguard_engine_virtual_ip = str(
        runtime_config.get("WIREGUARD_ENGINE_VIRTUAL_IP")
        or os.environ.get("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP")
        or DEFAULT_WIREGUARD_ENGINE_VIRTUAL_IP
    )
    wireguard_peer_network = str(
        runtime_config.get("WIREGUARD_PEER_NETWORK")
        or os.environ.get("BOREALIS_WIREGUARD_PEER_NETWORK")
        or DEFAULT_WIREGUARD_PEER_NETWORK
    )
    wireguard_acl_allowlist_windows = _parse_port_list(
        runtime_config.get("WIREGUARD_WINDOWS_ALLOWLIST")
        or os.environ.get("BOREALIS_WIREGUARD_WINDOWS_ALLOWLIST"),
        default=DEFAULT_WIREGUARD_ACL_WINDOWS,
    )
    wireguard_shell_port = _parse_int(
        runtime_config.get("WIREGUARD_SHELL_PORT")
        or os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT"),
        default=DEFAULT_WIREGUARD_SHELL_PORT,
        minimum=1,
        maximum=65535,
    )
    wireguard_key_root = Path(
        runtime_config.get("WIREGUARD_KEY_ROOT")
        or os.environ.get("BOREALIS_WIREGUARD_KEY_ROOT")
        or VPN_SERVER_CERT_ROOT
    ).expanduser()
    _ensure_parent(wireguard_key_root / "placeholder")
    wireguard_server_private_key_path = str(wireguard_key_root / "server_private.key")
    wireguard_server_public_key_path = str(wireguard_key_root / "server_public.key")

    api_groups = _parse_api_groups(
        runtime_config.get("API_GROUPS") or os.environ.get("BOREALIS_API_GROUPS")
    )
    if not api_groups:
        api_groups = (
            "core",
            "auth",
            "tokens",
            "enrollment",
            "devices",
            "server",
            "assemblies",
            "scheduled_jobs",
        )

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
        vpn_tunnel_log_file=vpn_tunnel_log_file,
        wireguard_port=wireguard_port,
        wireguard_engine_virtual_ip=wireguard_engine_virtual_ip,
        wireguard_peer_network=wireguard_peer_network,
        wireguard_server_private_key_path=wireguard_server_private_key_path,
        wireguard_server_public_key_path=wireguard_server_public_key_path,
        wireguard_acl_allowlist_windows=wireguard_acl_allowlist_windows,
        wireguard_shell_port=wireguard_shell_port,
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
