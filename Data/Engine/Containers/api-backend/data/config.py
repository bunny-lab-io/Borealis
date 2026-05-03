# ======================================================
# Data\Engine\config.py
# Description: Configuration loader aligning the Engine runtime with embedded Traefik public-edge defaults and logging policy.
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
invoking :func:`Data.Engine.server.create_app` manually. The shipped runtime is
loopback HTTP only and expects the embedded Traefik + Let's Encrypt edge to own
all public HTTPS traffic. Configuration is assembled from (in precedence
order): ``config`` mapping overrides provided to :func:`load_runtime_config`,
environment variables prefixed with ``BOREALIS_``, and finally built-in
defaults. Key environment variables are

``BOREALIS_DATABASE_URL``   PostgreSQL connection URL used by the Engine
                            runtime. This is required for production starts.
``BOREALIS_DB_SSLMODE``     PostgreSQL SSL mode (default: ``prefer``).
``BOREALIS_DB_POOL_SIZE``   SQLAlchemy pool size (default: ``10``).
``BOREALIS_DB_MAX_OVERFLOW`` SQLAlchemy max overflow (default: ``20``).
``BOREALIS_DB_CONNECT_TIMEOUT`` PostgreSQL connect timeout in seconds
                                (default: ``15``).
``BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS`` PostgreSQL idle-in-transaction timeout
                                       in milliseconds (default: ``60000``).
``BOREALIS_OFFICIAL_ASSEMBLIES_ROOT`` bundled official assembly catalog root
                                      (default: Engine package source
                                      ``Official_Assemblies`` folder).
``BOREALIS_OFFICIAL_ASSEMBLIES_REPO_URL`` repository URL shown in official
                                          assembly update prompts (default:
                                          ``https://github.com/bunny-lab-io/Aurora``).
``BOREALIS_OFFICIAL_ASSEMBLIES_REPO_GIT_URL`` Git URL used for the managed
                                              Aurora checkout (default:
                                              ``https://github.com/bunny-lab-io/Aurora.git``).
``BOREALIS_OFFICIAL_ASSEMBLIES_REPO_REF`` Aurora branch/tag/ref to fetch for
                                          official updates (default: ``main``).
``BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT`` Engine-managed Aurora checkout
                                               root (default:
                                               ``<ProjectRoot>/Engine/Services/api-backend/cache``).
``BOREALIS_OFFICIAL_ASSEMBLIES_MANIFEST_URL`` optional remote manifest URL used
                                              for on-demand official assembly
                                              updates.
``BOREALIS_OFFICIAL_ASSEMBLIES_REFRESH_SECONDS`` remote official catalog refresh
                                                TTL in seconds (default: ``300``).
``BOREALIS_CORS_ORIGINS``   comma separated list of allowed origins for CORS.
``BOREALIS_ENGINE_SECRET_PATH`` path to persistent generated secret (default:
                                ``<ProjectRoot>/Engine/Services/api-backend/secrets/engine_secret.txt``).
``BOREALIS_COOKIE_*``       Session cookie policies (``SAMESITE``, ``SECURE``,
                            ``DOMAIN``).
``BOREALIS_PUBLIC_*``       Public edge settings used when Borealis runs behind
                            the local Traefik TLS terminator.
``BOREALIS_LETSENCRYPT_SETTINGS_PATH`` path to the Borealis-managed public edge
                                      settings JSON rendered by ``Engine.sh``.

Direct public Engine TLS is no longer supported. The Python Engine always runs
on loopback HTTP behind the embedded Traefik edge, while internal Engine
certificates remain scoped to non-public uses such as WireGuard and code
signing.
Logs are written to ``Engine/Services/api-backend/logs/engine.log`` with
daily rotation and errors are additionally duplicated to
``Engine/Services/api-backend/logs/error.log`` so the runtime integrates with
the platform's logging policy.
"""

from __future__ import annotations

import logging
import os
from dataclasses import asdict, dataclass, field
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Any, Iterable, List, Mapping, MutableMapping, Optional, Sequence, Tuple

from . import edge_runtime
from .security import session_secret


ENGINE_DIR = Path(__file__).resolve().parent


def _discover_project_root() -> Path:
    """Locate the project root by searching for launchers or using overrides."""

    env_override = os.environ.get("BOREALIS_PROJECT_ROOT")
    if env_override:
        env_path = Path(env_override).expanduser().resolve()
        if env_path.is_dir():
            return env_path

    current = ENGINE_DIR
    for candidate in (current, *current.parents):
        if (
            (candidate / "Borealis.ps1").is_file()
            or (candidate / "Engine.sh").is_file()
            or (candidate / "Agent.sh").is_file()
        ):
            return candidate

    return ENGINE_DIR.parent.parent


PROJECT_ROOT = _discover_project_root()
API_SERVICE_ROOT = PROJECT_ROOT / "Engine" / "Services" / "api-backend"
WIREGUARD_SERVICE_ROOT = PROJECT_ROOT / "Engine" / "Services" / "wireguard-tunnel"
LOG_ROOT = API_SERVICE_ROOT / "logs"
LOG_FILE_PATH = LOG_ROOT / "engine.log"
ERROR_LOG_FILE_PATH = LOG_ROOT / "error.log"
API_LOG_FILE_PATH = LOG_ROOT / "api.log"
VPN_TUNNEL_LOG_FILE_PATH = LOG_ROOT / "VPN_Tunnel" / "tunnel.log"
ENGINE_SECRET_PATH = API_SERVICE_ROOT / "secrets" / "engine_secret.txt"

# WireGuard (VPN)
WIREGUARD_PORT = 30000
WIREGUARD_ENGINE_VIRTUAL_IP = "10.255.0.1/32"
WIREGUARD_PEER_NETWORK = "10.255.0.0/16"
VPN_SERVER_CERT_ROOT = WIREGUARD_SERVICE_ROOT / "secrets"

# Remote PowerShell (over WireGuard)
POWERSHELL_PORT = 47002

# VNC (UltraVNC over WireGuard)
VNC_PORT = 5900
VNC_WS_HOST = "127.0.0.1"
VNC_WS_PORT = 4823
VNC_SESSION_TTL_SECONDS = 120
GUACAMOLE_ENABLED = True
GUACD_HOST = "127.0.0.1"
GUACD_PORT = 4822
GUACAMOLE_VNC_WS_PATH = "/remote-desktop/vnc/guacamole"

# WireGuard port allowlist (Engine -> Agent /32)
WIREGUARD_PORT_ALLOWLIST = (
    POWERSHELL_PORT,
    VNC_PORT,
    22,
)


def _ensure_parent(path: Path) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
    except Exception:
        # Directory creation failure is non-fatal; subsequent file operations
        # will surface the issue with clearer context.
        pass


def _resolve_static_folder() -> str:
    candidate_roots = [
        PROJECT_ROOT / "Engine" / "Services" / "webui-frontend" / "cache" / "web-interface",
        PROJECT_ROOT / "web-interface",
        PROJECT_ROOT / "Data" / "Engine" / "Containers" / "webui-frontend" / "data" / "web-interface",
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

    return str((PROJECT_ROOT / "Engine" / "Services" / "webui-frontend" / "cache" / "web-interface").resolve())


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


def _load_edge_settings(config: Mapping[str, Any]) -> Optional[edge_runtime.LetsEncryptSettings]:
    settings_path_value = (
        config.get("LETSENCRYPT_SETTINGS_PATH")
        or os.environ.get("BOREALIS_LETSENCRYPT_SETTINGS_PATH")
        or edge_runtime.DEFAULT_SETTINGS_PATH
    )
    settings_path = Path(str(settings_path_value)).expanduser()
    if not settings_path.is_file():
        return None
    try:
        return edge_runtime.load_settings(settings_path)
    except Exception:
        return None


def _discover_tls_material(
    config: Mapping[str, Any],
    *,
    disable_tls: bool = False,
) -> Sequence[Optional[str]]:
    if disable_tls:
        return None, None, None

    cert_path = config.get("TLS_CERT_PATH") or None
    key_path = config.get("TLS_KEY_PATH") or None
    bundle_path = config.get("TLS_BUNDLE_PATH") or None
    return cert_path, key_path, bundle_path


@dataclass
class EngineSettings:
    """Resolved configuration values for the Engine runtime."""

    database_url: str
    db_sslmode: str
    db_pool_size: int
    db_max_overflow: int
    db_connect_timeout: int
    db_idle_in_transaction_timeout_ms: int
    official_assemblies_root: str
    official_assemblies_repo_url: str
    official_assemblies_repo_git_url: str
    official_assemblies_repo_ref: str
    official_assemblies_checkout_root: str
    official_assemblies_manifest_url: str
    official_assemblies_refresh_seconds: int
    static_folder: str
    cors_origins: Optional[List[str]]
    secret_key: str
    session_cookie_samesite: str
    session_cookie_secure: bool
    session_cookie_domain: Optional[str]
    public_edge_enabled: bool
    public_base_url: Optional[str]
    public_hostname: Optional[str]
    public_https_port: int
    public_vnc_path: str
    public_wireguard_host: Optional[str]
    public_wireguard_port: int
    disable_engine_tls: bool
    letsencrypt_settings_path: Optional[str]
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
    wireguard_port_allowlist: Tuple[int, ...]
    wireguard_shell_port: int
    vnc_port: int
    vnc_ws_host: str
    vnc_ws_port: int
    vnc_session_ttl_seconds: int
    guacamole_enabled: bool
    guacd_host: str
    guacd_port: int
    guacamole_vnc_ws_path: str
    raw: MutableMapping[str, Any] = field(default_factory=dict)

    def to_flask_config(self) -> MutableMapping[str, Any]:
        config: MutableMapping[str, Any] = {
            "SESSION_COOKIE_HTTPONLY": True,
            "SESSION_COOKIE_SAMESITE": self.session_cookie_samesite,
            "SESSION_COOKIE_SECURE": self.session_cookie_secure,
            "PREFERRED_URL_SCHEME": "https",
            "PUBLIC_EDGE_ENABLED": self.public_edge_enabled,
            "PUBLIC_BASE_URL": self.public_base_url,
            "PUBLIC_HOSTNAME": self.public_hostname,
            "PUBLIC_HTTPS_PORT": self.public_https_port,
            "PUBLIC_VNC_PATH": self.public_vnc_path,
            "PUBLIC_WIREGUARD_HOST": self.public_wireguard_host,
            "PUBLIC_WIREGUARD_PORT": self.public_wireguard_port,
            "GUACAMOLE_ENABLED": self.guacamole_enabled,
            "GUACD_HOST": self.guacd_host,
            "GUACD_PORT": self.guacd_port,
            "GUACAMOLE_VNC_WS_PATH": self.guacamole_vnc_ws_path,
            "DISABLE_ENGINE_TLS": self.disable_engine_tls,
            "LETSENCRYPT_SETTINGS_PATH": self.letsencrypt_settings_path,
            "DATABASE_URL": self.database_url,
            "TLS_CERT_PATH": self.tls_cert_path,
            "TLS_KEY_PATH": self.tls_key_path,
            "TLS_BUNDLE_PATH": self.tls_bundle_path,
            "SECRET_KEY": self.secret_key,
            "LOG_FILE": self.log_file,
            "ERROR_LOG_FILE": self.error_log_file,
            "API_LOG_FILE": self.api_log_file,
            "VPN_TUNNEL_LOG_FILE": self.vpn_tunnel_log_file,
            "STATIC_FOLDER": self.static_folder,
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

    database_url = str(
        runtime_config.get("DATABASE_URL")
        or os.environ.get("BOREALIS_DATABASE_URL")
        or ""
    ).strip()
    if not database_url:
        raise RuntimeError("BOREALIS_DATABASE_URL is required for the Borealis Engine runtime.")

    db_sslmode = str(
        runtime_config.get("DB_SSLMODE")
        or os.environ.get("BOREALIS_DB_SSLMODE")
        or "prefer"
    ).strip() or "prefer"
    db_pool_size = _parse_int(
        runtime_config.get("DB_POOL_SIZE") or os.environ.get("BOREALIS_DB_POOL_SIZE"),
        default=10,
        minimum=1,
        maximum=100,
    )
    db_max_overflow = _parse_int(
        runtime_config.get("DB_MAX_OVERFLOW") or os.environ.get("BOREALIS_DB_MAX_OVERFLOW"),
        default=20,
        minimum=0,
        maximum=200,
    )
    db_connect_timeout = _parse_int(
        runtime_config.get("DB_CONNECT_TIMEOUT") or os.environ.get("BOREALIS_DB_CONNECT_TIMEOUT"),
        default=15,
        minimum=1,
        maximum=300,
    )
    db_idle_in_transaction_timeout_ms = _parse_int(
        runtime_config.get("DB_IDLE_IN_TXN_TIMEOUT_MS")
        or os.environ.get("BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS"),
        default=60000,
        minimum=0,
        maximum=3600000,
    )
    official_assemblies_root = str(
        Path(
            runtime_config.get("OFFICIAL_ASSEMBLIES_ROOT")
            or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_ROOT")
            or ENGINE_DIR / "Official_Assemblies"
        ).expanduser()
    )
    official_assemblies_repo_url = str(
        runtime_config.get("OFFICIAL_ASSEMBLIES_REPO_URL")
        or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_REPO_URL")
        or "https://github.com/bunny-lab-io/Aurora"
    ).strip() or "https://github.com/bunny-lab-io/Aurora"
    official_assemblies_repo_git_url = str(
        runtime_config.get("OFFICIAL_ASSEMBLIES_REPO_GIT_URL")
        or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_REPO_GIT_URL")
        or "https://github.com/bunny-lab-io/Aurora.git"
    ).strip() or "https://github.com/bunny-lab-io/Aurora.git"
    official_assemblies_repo_ref = str(
        runtime_config.get("OFFICIAL_ASSEMBLIES_REPO_REF")
        or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_REPO_REF")
        or "main"
    ).strip() or "main"
    official_assemblies_checkout_root = str(
        Path(
            runtime_config.get("OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT")
            or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT")
            or API_SERVICE_ROOT / "cache"
        ).expanduser()
    )
    official_assemblies_manifest_url = str(
        runtime_config.get("OFFICIAL_ASSEMBLIES_MANIFEST_URL")
        or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_MANIFEST_URL")
        or ""
    ).strip()
    official_assemblies_refresh_seconds = _parse_int(
        runtime_config.get("OFFICIAL_ASSEMBLIES_REFRESH_SECONDS")
        or os.environ.get("BOREALIS_OFFICIAL_ASSEMBLIES_REFRESH_SECONDS"),
        default=300,
        minimum=30,
        maximum=86400,
    )

    static_folder = str(runtime_config.get("STATIC_FOLDER") or _resolve_static_folder())

    cors_origins = _parse_origins(
        runtime_config.get("CORS_ORIGINS") or os.environ.get("BOREALIS_CORS_ORIGINS")
    )

    edge_settings = _load_edge_settings(runtime_config)

    public_edge_enabled_default = bool(edge_settings.enabled) if edge_settings is not None else False
    public_edge_enabled = _parse_bool(
        runtime_config.get("PUBLIC_EDGE_ENABLED"),
        default=_parse_bool(
            os.environ.get("BOREALIS_PUBLIC_EDGE_ENABLED"),
            default=public_edge_enabled_default,
        ),
    )

    disable_engine_tls = True

    edge_public_base_url = edge_settings.public_base_url if edge_settings is not None else ""
    edge_public_hostname = edge_settings.public_hostname if edge_settings is not None else ""
    edge_public_https_port = edge_settings.https_port if edge_settings is not None else 443
    edge_public_vnc_path = edge_settings.public_vnc_path if edge_settings is not None else "/remote-desktop/vnc"
    edge_public_wireguard_host = edge_settings.public_wireguard_host if edge_settings is not None else ""
    edge_public_wireguard_port = edge_settings.public_wireguard_port if edge_settings is not None else WIREGUARD_PORT

    public_base_url_raw = (
        runtime_config.get("PUBLIC_BASE_URL")
        or os.environ.get("BOREALIS_PUBLIC_BASE_URL")
        or edge_public_base_url
    )
    public_hostname = str(
        runtime_config.get("PUBLIC_HOSTNAME")
        or os.environ.get("BOREALIS_PUBLIC_HOSTNAME")
        or edge_public_hostname
        or ""
    ).strip() or None
    public_https_port = _parse_int(
        runtime_config.get("PUBLIC_HTTPS_PORT")
        or os.environ.get("BOREALIS_PUBLIC_HTTPS_PORT"),
        default=edge_public_https_port,
        minimum=1,
        maximum=65535,
    )
    public_vnc_path = str(
        runtime_config.get("PUBLIC_VNC_PATH")
        or os.environ.get("BOREALIS_PUBLIC_VNC_PATH")
        or edge_public_vnc_path
    ).strip() or "/remote-desktop/vnc"
    if not public_vnc_path.startswith("/"):
        public_vnc_path = f"/{public_vnc_path}"
    if len(public_vnc_path) > 1:
        public_vnc_path = public_vnc_path.rstrip("/")
    public_wireguard_host = str(
        runtime_config.get("PUBLIC_WIREGUARD_HOST")
        or os.environ.get("BOREALIS_PUBLIC_WIREGUARD_HOST")
        or edge_public_wireguard_host
        or ""
    ).strip() or None
    public_wireguard_port = _parse_int(
        runtime_config.get("PUBLIC_WIREGUARD_PORT")
        or os.environ.get("BOREALIS_PUBLIC_WIREGUARD_PORT"),
        default=edge_public_wireguard_port,
        minimum=1,
        maximum=65535,
    )
    public_base_url = str(public_base_url_raw).strip() or None

    configured_secret = runtime_config.get("SECRET_KEY")
    if configured_secret and str(configured_secret).strip():
        secret_key = str(configured_secret).strip()
    else:
        secret_path = Path(
            runtime_config.get("ENGINE_SECRET_PATH")
            or os.environ.get("BOREALIS_ENGINE_SECRET_PATH")
            or ENGINE_SECRET_PATH
        ).expanduser()
        secret_key = session_secret.load_or_create_engine_secret(secret_path)

    session_cookie_samesite = str(
        runtime_config.get("SESSION_COOKIE_SAMESITE")
        or os.environ.get("BOREALIS_COOKIE_SAMESITE")
        or "Lax"
    )

    session_cookie_secure = _parse_bool(
        runtime_config.get("SESSION_COOKIE_SECURE"),
        default=_parse_bool(os.environ.get("BOREALIS_COOKIE_SECURE"), default=public_edge_enabled),
    )

    session_cookie_domain = runtime_config.get("SESSION_COOKIE_DOMAIN") or os.environ.get("BOREALIS_COOKIE_DOMAIN")
    session_cookie_domain = str(session_cookie_domain) if session_cookie_domain else None

    tls_cert_path, tls_key_path, tls_bundle_path = _discover_tls_material(
        runtime_config,
        disable_tls=disable_engine_tls,
    )

    log_file = str(runtime_config.get("LOG_FILE") or os.environ.get("BOREALIS_LOG_FILE") or LOG_FILE_PATH)
    _ensure_parent(Path(log_file))

    error_log_file = str(
        runtime_config.get("ERROR_LOG_FILE") or os.environ.get("BOREALIS_ERROR_LOG_FILE") or ERROR_LOG_FILE_PATH
    )
    _ensure_parent(Path(error_log_file))

    api_log_file = str(runtime_config.get("API_LOG_FILE") or os.environ.get("BOREALIS_API_LOG_FILE") or API_LOG_FILE_PATH)
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
        default=WIREGUARD_PORT,
        minimum=1,
        maximum=65535,
    )
    wireguard_engine_virtual_ip = str(
        runtime_config.get("WIREGUARD_ENGINE_VIRTUAL_IP")
        or os.environ.get("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP")
        or WIREGUARD_ENGINE_VIRTUAL_IP
    )
    wireguard_peer_network = str(
        runtime_config.get("WIREGUARD_PEER_NETWORK")
        or os.environ.get("BOREALIS_WIREGUARD_PEER_NETWORK")
        or WIREGUARD_PEER_NETWORK
    )
    wireguard_port_allowlist = _parse_port_list(
        runtime_config.get("WIREGUARD_PORT_ALLOWLIST")
        or os.environ.get("BOREALIS_WIREGUARD_PORT_ALLOWLIST"),
        default=WIREGUARD_PORT_ALLOWLIST,
    )
    wireguard_shell_port = _parse_int(
        runtime_config.get("WIREGUARD_SHELL_PORT")
        or os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT"),
        default=POWERSHELL_PORT,
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

    vnc_port = _parse_int(
        runtime_config.get("VNC_PORT") or os.environ.get("BOREALIS_VNC_PORT"),
        default=VNC_PORT,
        minimum=1,
        maximum=65535,
    )
    vnc_ws_host = str(
        runtime_config.get("VNC_WS_HOST")
        or os.environ.get("BOREALIS_VNC_WS_HOST")
        or (edge_runtime.DEFAULT_VNC_UPSTREAM_HOST if public_edge_enabled else VNC_WS_HOST)
    )
    vnc_ws_port = _parse_int(
        runtime_config.get("VNC_WS_PORT") or os.environ.get("BOREALIS_VNC_WS_PORT"),
        default=VNC_WS_PORT,
        minimum=1,
        maximum=65535,
    )
    vnc_session_ttl_seconds = _parse_int(
        runtime_config.get("VNC_SESSION_TTL_SECONDS")
        or os.environ.get("BOREALIS_VNC_SESSION_TTL_SECONDS"),
        default=VNC_SESSION_TTL_SECONDS,
        minimum=30,
        maximum=3600,
    )
    guacamole_enabled = _parse_bool(
        runtime_config.get("GUACAMOLE_ENABLED")
        if runtime_config.get("GUACAMOLE_ENABLED") is not None
        else os.environ.get("BOREALIS_GUACAMOLE_ENABLED"),
        default=GUACAMOLE_ENABLED,
    )
    guacd_host = str(
        runtime_config.get("GUACD_HOST")
        or os.environ.get("BOREALIS_GUACD_HOST")
        or GUACD_HOST
    ).strip() or GUACD_HOST
    guacd_port = _parse_int(
        runtime_config.get("GUACD_PORT") or os.environ.get("BOREALIS_GUACD_PORT"),
        default=GUACD_PORT,
        minimum=1,
        maximum=65535,
    )
    guacamole_vnc_ws_path = str(
        runtime_config.get("GUACAMOLE_VNC_WS_PATH")
        or os.environ.get("BOREALIS_GUACAMOLE_VNC_WS_PATH")
        or f"{public_vnc_path}/guacamole"
        or GUACAMOLE_VNC_WS_PATH
    ).strip() or GUACAMOLE_VNC_WS_PATH
    if not guacamole_vnc_ws_path.startswith("/"):
        guacamole_vnc_ws_path = f"/{guacamole_vnc_ws_path}"
    if len(guacamole_vnc_ws_path) > 1:
        guacamole_vnc_ws_path = guacamole_vnc_ws_path.rstrip("/")

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
            "filters",
            "server",
            "assemblies",
            "workflows",
            "scheduled_jobs",
            "notifications",
        )

    settings = EngineSettings(
        database_url=database_url,
        db_sslmode=db_sslmode,
        db_pool_size=db_pool_size,
        db_max_overflow=db_max_overflow,
        db_connect_timeout=db_connect_timeout,
        db_idle_in_transaction_timeout_ms=db_idle_in_transaction_timeout_ms,
        official_assemblies_root=official_assemblies_root,
        official_assemblies_repo_url=official_assemblies_repo_url,
        official_assemblies_repo_git_url=official_assemblies_repo_git_url,
        official_assemblies_repo_ref=official_assemblies_repo_ref,
        official_assemblies_checkout_root=official_assemblies_checkout_root,
        official_assemblies_manifest_url=official_assemblies_manifest_url,
        official_assemblies_refresh_seconds=official_assemblies_refresh_seconds,
        static_folder=static_folder,
        cors_origins=cors_origins,
        secret_key=secret_key,
        session_cookie_samesite=session_cookie_samesite,
        session_cookie_secure=session_cookie_secure,
        session_cookie_domain=session_cookie_domain,
        public_edge_enabled=public_edge_enabled,
        public_base_url=public_base_url,
        public_hostname=public_hostname,
        public_https_port=public_https_port,
        public_vnc_path=public_vnc_path,
        public_wireguard_host=public_wireguard_host,
        public_wireguard_port=public_wireguard_port,
        disable_engine_tls=disable_engine_tls,
        letsencrypt_settings_path=(edge_settings.settings_path if edge_settings is not None else None),
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
        wireguard_port_allowlist=wireguard_port_allowlist,
        wireguard_shell_port=wireguard_shell_port,
        vnc_port=vnc_port,
        vnc_ws_host=vnc_ws_host,
        vnc_ws_port=vnc_ws_port,
        vnc_session_ttl_seconds=vnc_session_ttl_seconds,
        guacamole_enabled=guacamole_enabled,
        guacd_host=guacd_host,
        guacd_port=guacd_port,
        guacamole_vnc_ws_path=guacamole_vnc_ws_path,
        raw=runtime_config,
    )
    return settings


def initialise_engine_logger(settings: EngineSettings, name: str = "borealis.engine") -> logging.Logger:
    """Configure Engine/runtime loggers to write to Engine log files."""

    formatter = logging.Formatter("%(asctime)s-%(name)s-%(levelname)s: %(message)s")
    console_logging = _parse_bool(os.environ.get("BOREALIS_ENGINE_CONSOLE_LOG"), default=False)

    def _normalised_path(path: str) -> str:
        try:
            return str(Path(path).expanduser().resolve())
        except Exception:
            return str(Path(path))

    def _has_file_handler(candidate_logger: logging.Logger, path: str, *, level: Optional[int] = None) -> bool:
        target = _normalised_path(path)
        for handler in candidate_logger.handlers:
            if not isinstance(handler, TimedRotatingFileHandler):
                continue
            base = getattr(handler, "baseFilename", "")
            if _normalised_path(str(base)) != target:
                continue
            if level is not None and getattr(handler, "level", logging.NOTSET) != level:
                continue
            return True
        return False

    def _remove_stream_handlers(candidate_logger: logging.Logger) -> None:
        for handler in list(candidate_logger.handlers):
            if isinstance(handler, logging.StreamHandler) and not isinstance(handler, logging.FileHandler):
                candidate_logger.removeHandler(handler)

    logger = logging.getLogger(name)
    if not console_logging:
        _remove_stream_handlers(logger)
    elif not any(
        isinstance(handler, logging.StreamHandler) and not isinstance(handler, logging.FileHandler)
        for handler in logger.handlers
    ):
        stream_handler = logging.StreamHandler()
        stream_handler.setFormatter(formatter)
        logger.addHandler(stream_handler)

    if not _has_file_handler(logger, settings.log_file):
        file_handler = TimedRotatingFileHandler(
            settings.log_file,
            when="midnight",
            backupCount=0,
            encoding="utf-8",
        )
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)

    if not _has_file_handler(logger, settings.error_log_file, level=logging.ERROR):
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

    # Route unscoped third-party warnings/errors into Engine files instead of stderr.
    root_logger = logging.getLogger()
    if not console_logging:
        _remove_stream_handlers(root_logger)
    if not _has_file_handler(root_logger, settings.log_file, level=logging.WARNING):
        root_file_handler = TimedRotatingFileHandler(
            settings.log_file,
            when="midnight",
            backupCount=0,
            encoding="utf-8",
        )
        root_file_handler.setLevel(logging.WARNING)
        root_file_handler.setFormatter(formatter)
        root_logger.addHandler(root_file_handler)
    if not _has_file_handler(root_logger, settings.error_log_file, level=logging.ERROR):
        root_error_handler = TimedRotatingFileHandler(
            settings.error_log_file,
            when="midnight",
            backupCount=0,
            encoding="utf-8",
        )
        root_error_handler.setLevel(logging.ERROR)
        root_error_handler.setFormatter(formatter)
        root_logger.addHandler(root_error_handler)
    root_logger.setLevel(logging.WARNING)
    logging.captureWarnings(True)

    return logger


__all__ = [
    "EngineSettings",
    "initialise_engine_logger",
    "load_runtime_config",
]
