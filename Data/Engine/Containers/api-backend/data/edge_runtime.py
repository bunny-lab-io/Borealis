"""Helpers for Borealis-managed Let's Encrypt and Traefik runtime state.

This module owns persisted public edge configuration stored under
``Engine/Services/traefik-edge/state/Settings.json``. Engine.sh uses it to create/update the
settings file, render Traefik configuration, and emit a small environment file
that the Engine runtime can source without duplicating JSON parsing logic in
shell.
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import sys
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Dict, Mapping, Optional
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
        if (candidate / "Borealis.ps1").is_file():
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
DEFAULT_TRAEFIK_DYNAMIC_CONFIG_PATH = DEFAULT_TRAEFIK_ROOT / "dynamic.yml"
DEFAULT_TRAEFIK_LOG_ROOT = DEFAULT_TRAEFIK_SERVICE_ROOT / "logs"
DEFAULT_ENGINE_UPSTREAM_HOST = "127.0.0.1"
DEFAULT_ENGINE_UPSTREAM_PORT = 5000
DEFAULT_VNC_UPSTREAM_HOST = "127.0.0.1"
DEFAULT_VNC_UPSTREAM_PORT = 4823
DEFAULT_VITE_UPSTREAM_HOST = "127.0.0.1"
DEFAULT_VITE_UPSTREAM_PORT = 8000
DEFAULT_VNC_PUBLIC_PATH = "/remote-desktop/vnc"
DEFAULT_ACME_CHALLENGE_PATH = "/.well-known/acme-challenge/"
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


def _split_trusted_ips(value: Any) -> list[str]:
    ips: list[str] = []
    seen: set[str] = set()
    for raw in _normalize_text(value).replace("\n", ",").split(","):
        text = "".join(str(raw).split())
        if not text or text in seen:
            continue
        ips.append(text)
        seen.add(text)
    return ips


def _traefik_trusted_ip_lists() -> tuple[list[str], list[str]]:
    trusted_proxy_ips = _split_trusted_ips(os.environ.get("BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS"))
    forwarded_headers = _split_trusted_ips(os.environ.get("BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS"))
    proxy_protocol = _split_trusted_ips(os.environ.get("BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS"))
    return forwarded_headers or trusted_proxy_ips, proxy_protocol or trusted_proxy_ips


def _append_trusted_ips_section(lines: list[str], section_name: str, trusted_ips: list[str]) -> None:
    if not trusted_ips:
        return
    lines.extend(
        [
            f"    {section_name}:",
            "      trustedIPs:",
            *[f'        - "{trusted_ip}"' for trusted_ip in trusted_ips],
        ]
    )


def _normalize_path(value: Any, *, default: str, ensure_leading_slash: bool = False) -> str:
    text = _normalize_text(value) or default
    if ensure_leading_slash:
        if not text.startswith("/"):
            text = f"/{text}"
        if len(text) > 1 and text.endswith("/"):
            text = text.rstrip("/")
    return text


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
    vnc_upstream_host: str
    vnc_upstream_port: int
    settings_path: str
    runtime_env_path: str
    acme_storage_path: str
    traefik_static_config_path: str
    traefik_dynamic_config_path: str
    logs_directory: str

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
        acme_email=_normalize_text(raw.get("acme_email")) or defaults.acme_email,
        public_base_url=_normalize_base_url(raw.get("public_base_url"), fqdn=fqdn, https_port=https_port),
        public_vnc_path=_normalize_path(raw.get("public_vnc_path"), default=defaults.public_vnc_path, ensure_leading_slash=True),
        public_wireguard_host=_normalize_fqdn(raw.get("public_wireguard_host"), default=fqdn),
        public_wireguard_port=_parse_int(raw.get("public_wireguard_port"), default=defaults.public_wireguard_port),
        http_port=_parse_int(raw.get("http_port"), default=defaults.http_port),
        https_port=https_port,
        engine_upstream_host=_normalize_text(raw.get("engine_upstream_host")) or defaults.engine_upstream_host,
        engine_upstream_port=_parse_int(raw.get("engine_upstream_port"), default=defaults.engine_upstream_port),
        vnc_upstream_host=_normalize_text(raw.get("vnc_upstream_host")) or defaults.vnc_upstream_host,
        vnc_upstream_port=_parse_int(raw.get("vnc_upstream_port"), default=defaults.vnc_upstream_port),
        settings_path=str(path),
        runtime_env_path=str(Path(_normalize_text(raw.get("runtime_env_path")) or defaults.runtime_env_path).expanduser()),
        acme_storage_path=str(Path(_normalize_text(raw.get("acme_storage_path")) or defaults.acme_storage_path).expanduser()),
        traefik_static_config_path=str(Path(_normalize_text(raw.get("traefik_static_config_path")) or defaults.traefik_static_config_path).expanduser()),
        traefik_dynamic_config_path=str(Path(_normalize_text(raw.get("traefik_dynamic_config_path")) or defaults.traefik_dynamic_config_path).expanduser()),
        logs_directory=str(Path(_normalize_text(raw.get("logs_directory")) or defaults.logs_directory).expanduser()),
    )
    return settings


def save_settings(settings: LetsEncryptSettings) -> Path:
    path = Path(settings.settings_path).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(settings.as_json_dict(), indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return path


def _render_runtime_env(settings: LetsEncryptSettings) -> str:
    pairs = {
        "BOREALIS_PUBLIC_EDGE_ENABLED": "1" if settings.enabled else "0",
        "BOREALIS_PUBLIC_BASE_URL": settings.public_base_url,
        "BOREALIS_PUBLIC_HOSTNAME": settings.public_hostname,
        "BOREALIS_PUBLIC_HTTPS_PORT": str(settings.https_port),
        "BOREALIS_PUBLIC_HTTP_PORT": str(settings.http_port),
        "BOREALIS_PUBLIC_VNC_PATH": settings.public_vnc_path,
        "BOREALIS_PUBLIC_WIREGUARD_HOST": settings.public_wireguard_host,
        "BOREALIS_PUBLIC_WIREGUARD_PORT": str(settings.public_wireguard_port),
        "BOREALIS_VNC_WS_HOST": settings.vnc_upstream_host,
        "BOREALIS_VNC_WS_PORT": str(settings.vnc_upstream_port),
        "BOREALIS_COOKIE_SECURE": "1",
        "BOREALIS_LETSENCRYPT_SETTINGS_PATH": settings.settings_path,
        "BOREALIS_TRAEFIK_STATIC_CONFIG_PATH": settings.traefik_static_config_path,
        "BOREALIS_TRAEFIK_DYNAMIC_CONFIG_PATH": settings.traefik_dynamic_config_path,
        "BOREALIS_TRAEFIK_ACME_STORAGE_PATH": settings.acme_storage_path,
        "BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS": os.environ.get("BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS", ""),
        "BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS": os.environ.get(
            "BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS",
            "",
        ),
        "BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS": os.environ.get(
            "BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS",
            "",
        ),
    }
    lines = [f"export {key}={shlex.quote(value)}" for key, value in pairs.items()]
    return "\n".join(lines) + "\n"


def _render_static_config(settings: LetsEncryptSettings) -> str:
    forwarded_headers, proxy_protocol = _traefik_trusted_ip_lists()
    lines = [
        "entryPoints:",
        "  web:",
        f'    address: ":{settings.http_port}"',
    ]
    _append_trusted_ips_section(lines, "forwardedHeaders", forwarded_headers)
    lines.extend(
        [
            "  websecure:",
            f'    address: ":{settings.https_port}"',
        ]
    )
    _append_trusted_ips_section(lines, "forwardedHeaders", forwarded_headers)
    _append_trusted_ips_section(lines, "proxyProtocol", proxy_protocol)
    lines.extend(
        [
            "providers:",
            "  file:",
            f'    filename: "{settings.traefik_dynamic_config_path}"',
            "    watch: true",
            "certificatesResolvers:",
            "  letsencrypt:",
            "    acme:",
            f'      email: "{settings.acme_email}"',
            f'      storage: "{settings.acme_storage_path}"',
            "      httpChallenge:",
            "        entryPoint: web",
            "log:",
            "  level: INFO",
            f'  filePath: "{Path(settings.logs_directory) / "traefik.log"}"',
            "accessLog:",
            f'  filePath: "{Path(settings.logs_directory) / "traefik-access.log"}"',
            "",
        ]
    )
    return "\n".join(lines)


def _dev_ui_proxy_enabled() -> bool:
    return _parse_bool(os.environ.get("BOREALIS_DEV_UI_PROXY_ENABLED"), default=False)


def _render_dynamic_config(settings: LetsEncryptSettings) -> str:
    engine_url = f"http://{settings.engine_upstream_host}:{settings.engine_upstream_port}"
    vnc_url = f"http://{settings.vnc_upstream_host}:{settings.vnc_upstream_port}"
    vite_url = f"http://{DEFAULT_VITE_UPSTREAM_HOST}:{DEFAULT_VITE_UPSTREAM_PORT}"
    challenge_path = DEFAULT_ACME_CHALLENGE_PATH
    dev_ui_proxy_enabled = _dev_ui_proxy_enabled()
    router_lines = [
        "http:",
        "  middlewares:",
        "    redirect-to-https:",
        "      redirectScheme:",
        "        scheme: https",
        "        permanent: true",
        "  routers:",
        "    borealis-http:",
        "      entryPoints:",
        "        - web",
        f'      rule: "Host(`{settings.public_hostname}`) && !PathPrefix(`{challenge_path}`)"',
        "      middlewares:",
        "        - redirect-to-https",
        "      service: noop@internal",
        "    borealis-vnc:",
        "      entryPoints:",
        "        - websecure",
        f'      rule: "Host(`{settings.public_hostname}`) && PathPrefix(`{settings.public_vnc_path}`)"',
        "      service: borealis-vnc",
        "      priority: 100",
        "      tls:",
        "        certResolver: letsencrypt",
    ]
    if dev_ui_proxy_enabled:
        router_lines.extend(
            [
                "    borealis-engine-api:",
                "      entryPoints:",
                "        - websecure",
                f'      rule: "Host(`{settings.public_hostname}`) && (PathPrefix(`/api`) || PathPrefix(`/socket.io`))"',
                "      service: borealis-engine",
                "      priority: 90",
                "      tls:",
                "        certResolver: letsencrypt",
                "    borealis-ui-dev:",
                "      entryPoints:",
                "        - websecure",
                f'      rule: "Host(`{settings.public_hostname}`)"',
                "      service: borealis-vite",
                "      priority: 10",
                "      tls:",
                "        certResolver: letsencrypt",
            ]
        )
    else:
        router_lines.extend(
            [
                "    borealis-https:",
                "      entryPoints:",
                "        - websecure",
                f'      rule: "Host(`{settings.public_hostname}`) && !PathPrefix(`{settings.public_vnc_path}`)"',
                "      service: borealis-engine",
                "      priority: 10",
                "      tls:",
                "        certResolver: letsencrypt",
            ]
        )
    service_lines = [
        "  services:",
        "    borealis-engine:",
        "      loadBalancer:",
        "        servers:",
        f'          - url: "{engine_url}"',
        "    borealis-vnc:",
        "      loadBalancer:",
        "        servers:",
        f'          - url: "{vnc_url}"',
    ]
    if dev_ui_proxy_enabled:
        service_lines.extend(
            [
                "    borealis-vite:",
                "      loadBalancer:",
                "        servers:",
                f'          - url: "{vite_url}"',
            ]
        )
    return "\n".join(
        [*router_lines, *service_lines, ""]
    )


def write_runtime_artifacts(settings: LetsEncryptSettings) -> Dict[str, str]:
    save_settings(settings)

    logs_dir = Path(settings.logs_directory)
    logs_dir.mkdir(parents=True, exist_ok=True)

    acme_path = Path(settings.acme_storage_path)
    acme_path.parent.mkdir(parents=True, exist_ok=True)
    if not acme_path.exists():
        acme_path.write_text("{}", encoding="utf-8")
    try:
        os.chmod(acme_path, 0o600)
    except Exception:
        pass

    runtime_env_path = Path(settings.runtime_env_path)
    runtime_env_path.parent.mkdir(parents=True, exist_ok=True)
    runtime_env_path.write_text(_render_runtime_env(settings), encoding="utf-8")

    static_config_path = Path(settings.traefik_static_config_path)
    static_config_path.parent.mkdir(parents=True, exist_ok=True)
    static_config_path.write_text(_render_static_config(settings), encoding="utf-8")

    dynamic_config_path = Path(settings.traefik_dynamic_config_path)
    dynamic_config_path.parent.mkdir(parents=True, exist_ok=True)
    dynamic_config_path.write_text(_render_dynamic_config(settings), encoding="utf-8")

    return {
        "settings_path": settings.settings_path,
        "runtime_env_path": settings.runtime_env_path,
        "acme_storage_path": settings.acme_storage_path,
        "traefik_static_config_path": settings.traefik_static_config_path,
        "traefik_dynamic_config_path": settings.traefik_dynamic_config_path,
        "public_base_url": settings.public_base_url,
        "public_hostname": settings.public_hostname,
        "public_vnc_path": settings.public_vnc_path,
    }


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Manage Borealis Let's Encrypt/Traefik runtime files.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    ensure_parser = subparsers.add_parser("ensure-files", help="Create/update Settings.json and Traefik runtime files.")
    ensure_parser.add_argument("--settings-path", default=str(DEFAULT_SETTINGS_PATH))
    ensure_parser.add_argument("--fqdn", default="")
    ensure_parser.add_argument("--email", default="")

    show_parser = subparsers.add_parser("show-json", help="Print the resolved settings JSON.")
    show_parser.add_argument("--settings-path", default=str(DEFAULT_SETTINGS_PATH))

    return parser


def main(argv: Optional[list[str]] = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)

    if args.command == "ensure-files":
        settings = load_settings(
            Path(args.settings_path),
            create_if_missing=True,
            seed_fqdn=args.fqdn,
            seed_email=args.email,
        )
        artifacts = write_runtime_artifacts(settings)
        json.dump({"settings": settings.as_json_dict(), "artifacts": artifacts}, sys.stdout, indent=2, sort_keys=True)
        sys.stdout.write("\n")
        return 0

    if args.command == "show-json":
        settings = load_settings(Path(args.settings_path), create_if_missing=False)
        json.dump(settings.as_json_dict(), sys.stdout, indent=2, sort_keys=True)
        sys.stdout.write("\n")
        return 0

    parser.error("unknown command")
    return 2


if __name__ == "__main__":  # pragma: no cover - CLI helper
    raise SystemExit(main())
