"""Load public-edge settings used by Python site-worker processes."""

from __future__ import annotations

import json
import os
import re
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.parse import urlsplit, urlunsplit


ENGINE_DIR = Path(__file__).resolve().parent


def _discover_project_root() -> Path:
    env_override = os.environ.get("BOREALIS_PROJECT_ROOT")
    if env_override:
        candidate = Path(env_override).expanduser().resolve()
        if candidate.is_dir():
            return candidate

    current = ENGINE_DIR
    for candidate in (current, *current.parents):
        if (candidate / "Agent.exe").is_file():
            return candidate
    return ENGINE_DIR.parent.parent


PROJECT_ROOT = _discover_project_root()
DEFAULT_TRAEFIK_SERVICE_ROOT = PROJECT_ROOT / "Engine" / "Services" / "traefik-edge"
DEFAULT_SETTINGS_PATH = Path(
    os.environ.get("BOREALIS_LETSENCRYPT_SETTINGS_PATH")
    or DEFAULT_TRAEFIK_SERVICE_ROOT / "state" / "Settings.json"
)
DEFAULT_ACME_STORAGE_PATH = Path(
    os.environ.get("BOREALIS_TRAEFIK_ACME_STORAGE_PATH")
    or DEFAULT_TRAEFIK_SERVICE_ROOT / "state" / "acme.json"
)
DEFAULT_RUNTIME_ENV_PATH = Path(
    os.environ.get("BOREALIS_TRAEFIK_RUNTIME_ENV_PATH")
    or DEFAULT_TRAEFIK_SERVICE_ROOT / "env" / "runtime.env"
)
DEFAULT_TRAEFIK_ROOT = DEFAULT_TRAEFIK_SERVICE_ROOT / "config"
DEFAULT_TRAEFIK_STATIC_CONFIG_PATH = DEFAULT_TRAEFIK_ROOT / "traefik.yml"
DEFAULT_TRAEFIK_DYNAMIC_CONFIG_DIR = DEFAULT_TRAEFIK_ROOT / "dynamic"
DEFAULT_TRAEFIK_DYNAMIC_CONFIG_PATH = DEFAULT_TRAEFIK_DYNAMIC_CONFIG_DIR / "core.yml"
DEFAULT_TRAEFIK_LOG_ROOT = DEFAULT_TRAEFIK_SERVICE_ROOT / "logs"
DEFAULT_ENGINE_UPSTREAM_HOST = "127.0.0.1"
DEFAULT_ENGINE_UPSTREAM_PORT = 5000
DEFAULT_WEBUI_UPSTREAM_HOST = "127.0.0.1"
DEFAULT_WEBUI_UPSTREAM_PORT = 8000
DEFAULT_WEBUI_TRAFFIC_OWNER = "k3s"
DEFAULT_VNC_UPSTREAM_HOST = "127.0.0.1"
DEFAULT_VNC_UPSTREAM_PORT = 4823
DEFAULT_VNC_PUBLIC_PATH = "/remote-desktop/vnc"
DEFAULT_HTTP_PORT = 80
DEFAULT_HTTPS_PORT = 443
DEFAULT_WIREGUARD_PORT = 30000


def _parse_bool(value: Any, *, default: bool) -> bool:
    if value is None:
        return default
    if isinstance(value, bool):
        return value
    text = str(value).strip().lower()
    if text in {"1", "true", "yes", "on"}:
        return True
    if text in {"0", "false", "no", "off"}:
        return False
    return default


def _parse_int(value: Any, *, default: int, minimum: int = 1, maximum: int = 65535) -> int:
    try:
        parsed = int(value)
    except Exception:
        return default
    if parsed < minimum or parsed > maximum:
        return default
    return parsed


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _split_hostnames(primary: str, aliases: Any) -> list[str]:
    hosts: list[str] = []
    seen: set[str] = set()
    for raw in [primary, *_normalize_text(aliases).replace("\n", ",").split(",")]:
        host = _normalize_text(raw).lower().rstrip(".")
        if not host or host in seen or re.search(r"[`\"'\s]", host):
            continue
        hosts.append(host)
        seen.add(host)
    return hosts or ["localhost"]


def _normalize_path(value: Any, *, default: str, ensure_leading_slash: bool = False) -> str:
    text = _normalize_text(value) or default
    if ensure_leading_slash:
        if not text.startswith("/"):
            text = f"/{text}"
        if len(text) > 1 and text.endswith("/"):
            text = text.rstrip("/")
    return text


def _normalize_dynamic_config_path(value: Any, *, default: Path) -> Path:
    path = Path(_normalize_text(value) or str(default)).expanduser()
    if path.name in {"dynamic.yml", "dynamic.yaml"}:
        return path.parent / "dynamic" / "core.yml"
    return path


def _normalize_fqdn(value: Any, *, default: str = "") -> str:
    text = _normalize_text(value).lower()
    if not text:
        return default
    try:
        parsed = urlsplit(text if "://" in text else f"https://{text}")
        host = (parsed.hostname or "").strip().lower()
    except Exception:
        host = text
    return host or default


def _normalize_base_url(value: Any, *, fqdn: str, https_port: int) -> str:
    text = _normalize_text(value)
    if not text:
        netloc = fqdn
        if https_port not in {0, 443}:
            netloc = f"{fqdn}:{https_port}"
        return urlunsplit(("https", netloc, "", "", ""))

    if "://" not in text:
        text = f"https://{text}"
    try:
        parsed = urlsplit(text)
    except Exception:
        return text.rstrip("/")

    scheme = (parsed.scheme or "https").lower()
    host = (parsed.hostname or fqdn).strip()
    if not host:
        host = fqdn
    port = parsed.port
    if port is None and scheme == "https" and https_port not in {0, 443}:
        port = https_port
    netloc = host
    if port:
        netloc = f"{host}:{port}"
    return urlunsplit((scheme, netloc, "", "", "")).rstrip("/")


def _normalize_webui_traffic_owner(value: Any) -> str:
    text = _normalize_text(value).lower().replace("_", "-")
    if text in {"", "auto", "k3s", "kubernetes"}:
        return "k3s"
    if text in {"compose", "docker", "docker-compose"}:
        return "k3s"
    return DEFAULT_WEBUI_TRAFFIC_OWNER


@dataclass
class LetsEncryptSettings:
    enabled: bool
    fqdn: str
    acme_email: str
    public_base_url: str
    public_vnc_path: str
    public_wireguard_host: str
    public_wireguard_port: int
    http_port: int
    https_port: int
    engine_upstream_host: str
    engine_upstream_port: int
    webui_traffic_owner: str
    webui_upstream_host: str
    webui_upstream_port: int
    vnc_upstream_host: str
    vnc_upstream_port: int
    settings_path: str
    runtime_env_path: str
    acme_storage_path: str
    traefik_static_config_path: str
    traefik_dynamic_config_path: str
    logs_directory: str
    fqdn_aliases: str = ""

    @property
    def public_hostname(self) -> str:
        return self.fqdn

    def as_json_dict(self) -> Dict[str, Any]:
        return asdict(self)


def _default_settings(
    *,
    settings_path: Path,
    fqdn: str = "",
    acme_email: str = "",
) -> LetsEncryptSettings:
    normalised_fqdn = _normalize_fqdn(fqdn, default="localhost")
    https_port = DEFAULT_HTTPS_PORT
    base_url = _normalize_base_url("", fqdn=normalised_fqdn, https_port=https_port)
    return LetsEncryptSettings(
        enabled=True,
        fqdn=normalised_fqdn,
        acme_email=_normalize_text(acme_email),
        public_base_url=base_url,
        public_vnc_path=DEFAULT_VNC_PUBLIC_PATH,
        public_wireguard_host=normalised_fqdn,
        public_wireguard_port=DEFAULT_WIREGUARD_PORT,
        http_port=DEFAULT_HTTP_PORT,
        https_port=https_port,
        engine_upstream_host=DEFAULT_ENGINE_UPSTREAM_HOST,
        engine_upstream_port=DEFAULT_ENGINE_UPSTREAM_PORT,
        webui_traffic_owner=_normalize_webui_traffic_owner(os.environ.get("BOREALIS_WEBUI_TRAFFIC_OWNER")),
        webui_upstream_host=_normalize_text(os.environ.get("BOREALIS_WEBUI_UPSTREAM_HOST")) or DEFAULT_WEBUI_UPSTREAM_HOST,
        webui_upstream_port=_parse_int(os.environ.get("BOREALIS_WEBUI_UPSTREAM_PORT"), default=DEFAULT_WEBUI_UPSTREAM_PORT),
        vnc_upstream_host=DEFAULT_VNC_UPSTREAM_HOST,
        vnc_upstream_port=DEFAULT_VNC_UPSTREAM_PORT,
        settings_path=str(settings_path),
        runtime_env_path=str(DEFAULT_RUNTIME_ENV_PATH),
        acme_storage_path=str(DEFAULT_ACME_STORAGE_PATH),
        traefik_static_config_path=str(DEFAULT_TRAEFIK_STATIC_CONFIG_PATH),
        traefik_dynamic_config_path=str(DEFAULT_TRAEFIK_DYNAMIC_CONFIG_PATH),
        logs_directory=str(DEFAULT_TRAEFIK_LOG_ROOT),
    )


def _load_json(path: Path) -> Dict[str, Any]:
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def load_settings(
    settings_path: Optional[Path] = None,
    *,
    create_if_missing: bool = False,
    seed_fqdn: str = "",
    seed_email: str = "",
) -> LetsEncryptSettings:
    path = (settings_path or DEFAULT_SETTINGS_PATH).expanduser()
    defaults = _default_settings(settings_path=path, fqdn=seed_fqdn, acme_email=seed_email)
    raw = _load_json(path)
    if not raw and create_if_missing:
        save_settings(defaults)
        raw = _load_json(path)

    fqdn = _normalize_fqdn(raw.get("fqdn"), default=defaults.fqdn)
    https_port = _parse_int(raw.get("https_port"), default=defaults.https_port)
    settings = LetsEncryptSettings(
        enabled=_parse_bool(raw.get("enabled"), default=defaults.enabled),
        fqdn=fqdn,
        fqdn_aliases=",".join(
            _split_hostnames(
                fqdn,
                raw.get("fqdn_aliases") or os.environ.get("BOREALIS_PUBLIC_HOSTNAME_ALIASES"),
            )
        ),
        acme_email=_normalize_text(raw.get("acme_email")) or defaults.acme_email,
        public_base_url=_normalize_base_url(raw.get("public_base_url"), fqdn=fqdn, https_port=https_port),
        public_vnc_path=_normalize_path(raw.get("public_vnc_path"), default=defaults.public_vnc_path, ensure_leading_slash=True),
        public_wireguard_host=_normalize_fqdn(raw.get("public_wireguard_host"), default=fqdn),
        public_wireguard_port=_parse_int(raw.get("public_wireguard_port"), default=defaults.public_wireguard_port),
        http_port=_parse_int(raw.get("http_port"), default=defaults.http_port),
        https_port=https_port,
        engine_upstream_host=_normalize_text(raw.get("engine_upstream_host")) or defaults.engine_upstream_host,
        engine_upstream_port=_parse_int(raw.get("engine_upstream_port"), default=defaults.engine_upstream_port),
        webui_traffic_owner=_normalize_webui_traffic_owner(raw.get("webui_traffic_owner") or defaults.webui_traffic_owner),
        webui_upstream_host=_normalize_text(raw.get("webui_upstream_host")) or defaults.webui_upstream_host,
        webui_upstream_port=_parse_int(raw.get("webui_upstream_port"), default=defaults.webui_upstream_port),
        vnc_upstream_host=_normalize_text(raw.get("vnc_upstream_host")) or defaults.vnc_upstream_host,
        vnc_upstream_port=_parse_int(raw.get("vnc_upstream_port"), default=defaults.vnc_upstream_port),
        settings_path=str(path),
        runtime_env_path=str(Path(_normalize_text(raw.get("runtime_env_path")) or defaults.runtime_env_path).expanduser()),
        acme_storage_path=str(Path(_normalize_text(raw.get("acme_storage_path")) or defaults.acme_storage_path).expanduser()),
        traefik_static_config_path=str(Path(_normalize_text(raw.get("traefik_static_config_path")) or defaults.traefik_static_config_path).expanduser()),
        traefik_dynamic_config_path=str(
            _normalize_dynamic_config_path(
                raw.get("traefik_dynamic_config_path"),
                default=Path(defaults.traefik_dynamic_config_path),
            )
        ),
        logs_directory=str(Path(_normalize_text(raw.get("logs_directory")) or defaults.logs_directory).expanduser()),
    )
    return settings


def save_settings(settings: LetsEncryptSettings) -> Path:
    path = Path(settings.settings_path).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(settings.as_json_dict(), indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return path
