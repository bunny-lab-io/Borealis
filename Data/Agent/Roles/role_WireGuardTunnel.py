# ======================================================
# Data\Agent\Roles\role_WireGuardTunnel.py
# Description: WireGuard client lifecycle for outbound reverse VPN (Windows) with host-only /32 routing.
#
# API Endpoints (if applicable): None
# ======================================================

"""WireGuard client role (Windows) for reverse VPN tunnels.

This role prepares the WireGuard client config, manages a single active
session, and keeps the tunnel online while the agent service runs. It logs
lifecycle events to Agent/Logs/VPN_Tunnel/tunnel.log. It responds to Engine
Socket.IO events (`vpn_tunnel_start`, `vpn_tunnel_activity`) and periodically
ensures the persistent session via `/api/agent/vpn/ensure`.
"""

from __future__ import annotations

import base64
import ipaddress
import json
import os
import subprocess
import threading
import time
import re
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.parse import urlsplit

try:
    import winreg  # type: ignore
except Exception:  # pragma: no cover - non-Windows guard
    winreg = None
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import x25519

try:
    from signature_utils import verify_and_store_script_signature
except Exception:  # pragma: no cover - fallback for runtime path issues
    import sys
    from pathlib import Path as _Path

    base_dir = _Path(__file__).resolve().parents[1]
    if str(base_dir) not in sys.path:
        sys.path.insert(0, str(base_dir))
    from signature_utils import verify_and_store_script_signature

ROLE_NAME = "WireGuardTunnel"
ROLE_CONTEXTS = ["system"]
TUNNEL_NAME = "Borealis"
TUNNEL_DISPLAY_NAME = "Borealis"
SERVICE_DISPLAY_NAME = "Borealis - WireGuard - Agent"
TUNNEL_IDLE_ADDRESS = "169.254.255.254/32"
FIREWALL_RULE_NAME = "Borealis - WireGuard - Agent"
DEFAULT_VNC_PORT = 5900


def _env_int(name: str, default: int, *, min_value: int = 1, max_value: int = 3600) -> int:
    raw = os.environ.get(name)
    try:
        value = int(raw) if raw is not None else default
    except Exception:
        value = default
    if value < min_value:
        return min_value
    if value > max_value:
        return max_value
    return value


KEEPALIVE_SECONDS = _env_int("BOREALIS_WIREGUARD_KEEPALIVE_SECONDS", 30, min_value=10, max_value=600)
ENSURE_INITIAL_DELAY_SECONDS = _env_int("BOREALIS_WIREGUARD_ENSURE_DELAY", 10, min_value=0, max_value=300)
ENSURE_INTERVAL_SECONDS = _env_int("BOREALIS_WIREGUARD_ENSURE_INTERVAL", 60, min_value=15, max_value=3600)


def _log_path() -> Path:
    root = Path(__file__).resolve().parents[2] / "Logs" / "VPN_Tunnel"
    root.mkdir(parents=True, exist_ok=True)
    return root / "tunnel.log"


def _write_log(message: str) -> None:
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [wg-client] {message}\n")
    except Exception:
        pass




def _encode_key(raw: bytes) -> str:
    return base64.b64encode(raw).decode("ascii").strip()


def _generate_client_keys(root: Path) -> Dict[str, str]:
    root.mkdir(parents=True, exist_ok=True)
    priv_path = root / "client_private.key"
    pub_path = root / "client_public.key"

    if priv_path.is_file() and pub_path.is_file():
        try:
            private_key = priv_path.read_text(encoding="utf-8").strip()
            public_key = pub_path.read_text(encoding="utf-8").strip()
            if private_key and public_key:
                return {"private": private_key, "public": public_key}
        except Exception:
            pass

    key = x25519.X25519PrivateKey.generate()
    priv = _encode_key(
        key.private_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    pub = _encode_key(
        key.public_key().public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
    )
    try:
        priv_path.write_text(priv, encoding="utf-8")
        pub_path.write_text(pub, encoding="utf-8")
    except Exception:
        _write_log("Failed to persist WireGuard client keys.")
    return {"private": priv, "public": pub}


def _resolve_shell_port() -> int:
    raw = os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT")
    try:
        value = int(raw) if raw is not None else 47002
    except Exception:
        value = 47002
    if value < 1 or value > 65535:
        return 47002
    return value


def _resolve_vnc_port() -> int:
    raw = os.environ.get("BOREALIS_VNC_PORT")
    try:
        value = int(raw) if raw is not None else DEFAULT_VNC_PORT
    except Exception:
        value = DEFAULT_VNC_PORT
    if value < 1 or value > 65535:
        return DEFAULT_VNC_PORT
    return value


def _parse_allowed_ports(raw: Any) -> list[int]:
    if raw is None:
        return []
    if isinstance(raw, (list, tuple, set)):
        items = list(raw)
    else:
        text = str(raw).strip()
        if not text:
            return []
        items = [part.strip() for part in text.split(",") if part.strip()]
    ports: list[int] = []
    for item in items:
        try:
            port = int(item)
        except Exception:
            continue
        if 1 <= port <= 65535:
            ports.append(port)
    return list(dict.fromkeys(ports))


class SessionConfig:
    def __init__(
        self,
        *,
        token: Dict[str, Any],
        tunnel_id: str,
        virtual_ip: str,
        allowed_ips: str,
        endpoint: str,
        server_public_key: str,
        allowed_ports: str,
        idle_seconds: int = 900,
        preshared_key: Optional[str] = None,
        client_private_key: Optional[str] = None,
        client_public_key: Optional[str] = None,
    ) -> None:
        self.token = token
        self.tunnel_id = tunnel_id
        self.virtual_ip = virtual_ip
        self.allowed_ips = allowed_ips
        self.endpoint = endpoint
        self.server_public_key = server_public_key
        self.allowed_ports = allowed_ports
        self.idle_seconds = idle_seconds
        self.preshared_key = preshared_key
        self.client_private_key = client_private_key
        self.client_public_key = client_public_key


class WireGuardClient:
    def __init__(self) -> None:
        base = Path(__file__).resolve().parents[2]
        self.cert_root = base / "Borealis" / "Certificates" / "VPN_Client"
        self.temp_root = base / "Borealis" / "Temp"
        self.temp_root.mkdir(parents=True, exist_ok=True)
        self.service_name = TUNNEL_NAME
        self.display_name = TUNNEL_DISPLAY_NAME
        self.service_display_name = SERVICE_DISPLAY_NAME
        self.conf_path = self._wireguard_config_path()
        self.session: Optional[SessionConfig] = None
        self.idle_deadline: Optional[float] = None
        self._idle_thread: Optional[threading.Thread] = None
        self._stop_event = threading.Event()
        self._session_lock = threading.Lock()
        self._client_keys = _generate_client_keys(self.cert_root)
        self._wg_exe = self._resolve_wireguard_exe()
        self._last_install_already_present = False
        try:
            self._ensure_idle_service()
        except Exception:
            pass

    def _resolve_wireguard_exe(self) -> str:
        candidates = [
            str(Path(os.environ.get("ProgramFiles", "C:\\Program Files")) / "WireGuard" / "wireguard.exe"),
            "wireguard.exe",
        ]
        for candidate in candidates:
            if Path(candidate).is_file():
                return candidate
        return "wireguard.exe"

    def _service_id(self) -> str:
        return f"WireGuardTunnel${self.service_name}"

    def _service_reg_path(self) -> str:
        return f"SYSTEM\\CurrentControlSet\\Services\\{self._service_id()}"

    def _service_reg_exists(self) -> bool:
        if winreg is None:
            return False
        try:
            winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, self._service_reg_path())
            return True
        except FileNotFoundError:
            return False
        except PermissionError:
            _write_log("WireGuard service registry check denied; treating as present.")
            return True
        except Exception as exc:
            _write_log(f"WireGuard service registry check failed: {exc}")
        return False

    def _service_image_path(self) -> Optional[str]:
        if winreg is None:
            return None
        try:
            key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, self._service_reg_path())
            value, _ = winreg.QueryValueEx(key, "ImagePath")
            return str(value) if value else None
        except Exception:
            return None

    def _service_config_path(self) -> Optional[Path]:
        image_path = self._service_image_path()
        if not image_path:
            return None
        match = re.search(r'(?i)/tunnelservice\s+"([^"]+)"', image_path)
        if match:
            return Path(match.group(1))
        match = re.search(r"(?i)/tunnelservice\s+(\S+)", image_path)
        if match:
            return Path(match.group(1))
        return None

    def _service_state(self) -> Optional[str]:
        code, out, err = self._run(["sc.exe", "query", self._service_id()])
        if code != 0:
            return None
        text = out or err or ""
        for line in text.splitlines():
            if "STATE" not in line:
                continue
            match = re.search(r"STATE\s*:\s*\d+\s+(\w+)", line)
            if match:
                return match.group(1).upper()
        return None

    def _wireguard_config_path(self) -> Path:
        settings_dir = self.temp_root.parent / "Settings" / "WireGuard"
        candidates = [
            settings_dir,
            Path(os.environ.get("ProgramFiles", "C:\\Program Files")) / "WireGuard" / "Data" / "Configurations",
            Path(os.environ.get("ProgramData", "C:\\ProgramData")) / "Borealis" / "WireGuard" / "Configurations",
            self.temp_root,
        ]
        for config_dir in candidates:
            candidate = config_dir / f"{self.service_name}.conf"
            if candidate.is_file():
                return candidate
        for config_dir in candidates:
            try:
                config_dir.mkdir(parents=True, exist_ok=True)
                return config_dir / f"{self.service_name}.conf"
            except Exception:
                continue
        return self.temp_root / f"{self.service_name}.conf"

    def _write_config(self, text: str) -> bool:
        return self._write_config_to(self.conf_path, text)

    def _write_config_to(self, path: Path, text: str) -> bool:
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding="ascii")
            return True
        except Exception as exc:
            _write_log(f"Failed to write WireGuard config at {path}: {exc}")
            return False

    def _render_idle_config(self) -> str:
        private_key = self._client_keys["private"]
        return "\n".join(
            [
                "[Interface]",
                f"PrivateKey = {private_key}",
                f"Address = {TUNNEL_IDLE_ADDRESS}",
                "ListenPort = 0",
            ]
        )

    def _normalize_firewall_remote(self, allowed_ips: Optional[str]) -> Optional[str]:
        if not allowed_ips:
            return None
        try:
            network = ipaddress.ip_network(str(allowed_ips).strip(), strict=False)
        except Exception:
            _write_log(f"Refusing to apply tunnel firewall rule; invalid allowed_ips={allowed_ips}.")
            return None
        if network.prefixlen != 32:
            _write_log(f"Refusing to apply tunnel firewall rule; allowed_ips not /32: {network}.")
            return None
        return str(network)

    def _ensure_shell_firewall(self, allowed_ips: Optional[str], allowed_ports: Optional[str]) -> None:
        if os.name != "nt":
            return
        remote = self._normalize_firewall_remote(allowed_ips)
        if not remote:
            return
        ports = _parse_allowed_ports(allowed_ports)
        if not ports:
            fallback_ports = [_resolve_shell_port(), _resolve_vnc_port()]
            ports = [p for p in fallback_ports if 1 <= p <= 65535]
            ports = list(dict.fromkeys(ports))
            if ports:
                _write_log(
                    "WireGuard allowed_ports missing; defaulting to {0}.".format(
                        ",".join(str(p) for p in ports)
                    )
                )
        if not ports:
            _write_log("WireGuard allowed_ports empty; firewall rule not updated.")
            return
        port_text = ",".join(str(p) for p in ports)
        base_name = FIREWALL_RULE_NAME.replace("'", "''")
        for protocol in ("TCP", "UDP"):
            rule_name = f"{base_name} ({protocol})"
            command = (
                "$rule = Get-NetFirewallRule -DisplayName '{name}' -ErrorAction SilentlyContinue; "
                "if (-not $rule) {{ "
                "New-NetFirewallRule -DisplayName '{name}' -Direction Inbound -Action Allow "
                "-Protocol {protocol} -LocalPort {ports} -RemoteAddress {remote} -Profile Any | Out-Null; "
                "}} else {{ "
                "$rule | Set-NetFirewallRule -Direction Inbound -Action Allow -Enabled True -Profile Any | Out-Null; "
                "$rule | Set-NetFirewallPortFilter -Protocol {protocol} -LocalPort {ports} | Out-Null; "
                "$rule | Set-NetFirewallAddressFilter -RemoteAddress {remote} | Out-Null; "
                "}}"
            ).format(name=rule_name, remote=remote, ports=port_text, protocol=protocol)
            try:
                result = subprocess.run(
                    ["powershell.exe", "-NoProfile", "-Command", command],
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if result.returncode != 0:
                    _write_log(
                        f"Failed to ensure tunnel firewall rule ({protocol}): {result.stderr.strip()}"
                    )
                else:
                    _write_log(
                        f"Ensured tunnel firewall rule for {remote} ports={port_text} protocol={protocol}."
                    )
            except Exception as exc:
                _write_log(f"Failed to ensure tunnel firewall rule ({protocol}): {exc}")

    def _remove_shell_firewall(self) -> None:
        _write_log("WireGuard firewall teardown skipped (always-on).")

    def _service_exists(self) -> bool:
        code, _, _ = self._run(["sc.exe", "query", self._service_id()])
        if code == 0:
            return True
        return self._service_reg_exists()

    def _install_service(self) -> bool:
        code, out, err = self._run([self._wg_exe, "/installtunnelservice", str(self.conf_path)])
        self._last_install_already_present = False
        if code != 0:
            if "already installed and running" in err.lower():
                self._last_install_already_present = True
                _write_log("WireGuard tunnel service already installed; skipping install.")
                return True
            if "access is denied" in err.lower():
                _write_log("Failed to install WireGuard tunnel service: access denied; ensure agent runs elevated.")
                return False
            _write_log(f"Failed to install WireGuard tunnel service: code={code} err={err}")
            return False
        return True

    def _restart_service(self) -> bool:
        service_id = self._service_id()
        stop_code, _, stop_err = self._run(["sc.exe", "stop", service_id])
        if stop_code != 0 and stop_err:
            _write_log(f"WireGuard stop service returned code={stop_code} err={stop_err}")
        time.sleep(1)
        start_code, _, start_err = self._run(["sc.exe", "start", service_id])
        if start_code != 0 and start_err:
            _write_log(f"WireGuard start service returned code={start_code} err={start_err}")
        return start_code == 0

    def _ensure_adapter_name(self) -> None:
        if self.service_name == self.display_name:
            return
        args = [
            "netsh.exe",
            "interface",
            "set",
            "interface",
            f'name="{self.service_name}"',
            f'newname="{self.display_name}"',
        ]
        self._run(args)

    def _ensure_service_display_name(self) -> None:
        if not self.service_display_name:
            return
        if not self._service_exists():
            return
        args = [
            "sc.exe",
            "config",
            self._service_id(),
            "DisplayName=",
            self.service_display_name,
        ]
        code, _, err = self._run(args)
        if code != 0 and err:
            _write_log(f"WireGuard service display name update failed: {err}")

    def _ensure_idle_service(self) -> None:
        if self._service_exists():
            return
        if not Path(self._wg_exe).is_file():
            return
        idle_config = self._render_idle_config()
        if not self._write_config(idle_config):
            return
        if self._install_service():
            self._ensure_adapter_name()
            self._ensure_service_display_name()

    def _validate_token(self, token: Dict[str, Any], *, signing_client: Optional[Any] = None) -> None:
        payload = dict(token or {})
        signature = payload.pop("signature", None)
        signing_key = payload.pop("signing_key", None)
        sig_alg = payload.pop("sig_alg", None)

        required = ("agent_id", "tunnel_id", "expires_at", "port")
        missing = [field for field in required if field not in token or token[field] in ("", None)]
        if missing:
            raise ValueError(f"Missing token fields: {', '.join(missing)}")
        try:
            exp = float(payload["expires_at"])
        except Exception:
            raise ValueError("Invalid token expiry")
        if exp <= time.time():
            raise ValueError("Token expired")
        try:
            port = int(payload["port"])
        except Exception:
            raise ValueError("Invalid token port")
        if port < 1 or port > 65535:
            raise ValueError("Invalid token port")

        if not signature:
            if sig_alg or signing_key:
                raise ValueError("Token signature missing")
            stored_key = None
            if signing_client is not None and hasattr(signing_client, "load_server_signing_key"):
                try:
                    stored_key = signing_client.load_server_signing_key()
                except Exception:
                    stored_key = None
            if isinstance(stored_key, str) and stored_key.strip():
                raise ValueError("Token signature missing")
            return

        if signature:
            if sig_alg and str(sig_alg).lower() not in ("ed25519", "eddsa"):
                raise ValueError("Unsupported token signature algorithm")
            payload_bytes = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
            if not verify_and_store_script_signature(signing_client, payload_bytes, str(signature), signing_key):
                raise ValueError("Token signature invalid")

    def _run(self, args: list[str]) -> tuple[int, str, str]:
        try:
            proc = subprocess.run(args, capture_output=True, text=True, check=False)
            return proc.returncode, proc.stdout.strip(), proc.stderr.strip()
        except Exception as exc:  # pragma: no cover - runtime guard
            return 1, "", str(exc)

    def _render_config(self, session: SessionConfig) -> str:
        private_key = session.client_private_key or self._client_keys["private"]
        lines = [
            "[Interface]",
            f"PrivateKey = {private_key}",
            f"Address = {session.virtual_ip}",
            "",
            "[Peer]",
            f"PublicKey = {session.server_public_key}",
            f"AllowedIPs = {session.allowed_ips}",
            f"Endpoint = {session.endpoint}",
            f"PersistentKeepalive = {KEEPALIVE_SECONDS}",
        ]
        if session.preshared_key:
            lines.append(f"PresharedKey = {session.preshared_key}")
        return "\n".join(lines)

    def _start_idle_monitor(self) -> None:
        self._stop_event.clear()

        def _loop() -> None:
            while not self._stop_event.is_set():
                if self.idle_deadline and time.time() >= self.idle_deadline:
                    _write_log("Idle timeout reached; stopping WireGuard session.")
                    self.stop_session(reason="idle_timeout")
                    return
                time.sleep(5)

        t = threading.Thread(target=_loop, daemon=True)
        t.start()
        self._idle_thread = t

    def _stop_session_locked(self, reason: str = "stop", ignore_missing: bool = False) -> None:
        self._remove_shell_firewall()
        if not self._service_exists():
            if not ignore_missing:
                _write_log("WireGuard tunnel service not found when stopping session.")
            self.session = None
            self.idle_deadline = None
            self._stop_event.set()
            return

        idle_config = self._render_idle_config()
        wrote_idle = self._write_config(idle_config)
        service_config_path = self._service_config_path()
        if service_config_path and service_config_path != self.conf_path:
            wrote_idle = self._write_config_to(service_config_path, idle_config) or wrote_idle
        if wrote_idle:
            self._restart_service()
            self._ensure_adapter_name()
            self._ensure_service_display_name()
            _write_log(f"WireGuard client session stopped (reason={reason}).")
        elif not ignore_missing:
            _write_log("Failed to write idle WireGuard config.")
        self.session = None
        self.idle_deadline = None
        self._stop_event.set()

    def start_session(self, session: SessionConfig, *, signing_client: Optional[Any] = None) -> None:
        with self._session_lock:
            if self.session:
                if self.session.tunnel_id == session.tunnel_id:
                    _write_log("WireGuard session already active; reusing existing session.")
                    self.bump_activity()
                    return
                _write_log(
                    "WireGuard session replace: existing_tunnel_id={0} new_tunnel_id={1}".format(
                        self.session.tunnel_id, session.tunnel_id
                    )
                )
                self._stop_session_locked(reason="session_replace", ignore_missing=True)

            try:
                self._validate_token(session.token, signing_client=signing_client)
            except Exception as exc:
                _write_log(f"Refusing to start WireGuard session: {exc}")
                return

            rendered = self._render_config(session)
            if not self._write_config(rendered):
                _write_log("Failed to write WireGuard client config.")
                return
            _write_log(f"Rendered WireGuard client config to {self.conf_path}")

            service_config_path = self._service_config_path()
            if service_config_path and service_config_path != self.conf_path:
                if self._write_config_to(service_config_path, rendered):
                    _write_log(f"Rendered WireGuard client config to service path {service_config_path}")

            if not self._service_exists():
                if not self._install_service():
                    return

            service_present = self._service_exists()
            if not service_present and self._last_install_already_present:
                _write_log("WireGuard tunnel service presence inferred from install response.")
                service_present = True
            if not service_present:
                _write_log("WireGuard tunnel service still missing after install attempt.")
                return

            self._restart_service()
            self._ensure_adapter_name()
            self._ensure_service_display_name()
            self._ensure_shell_firewall(session.allowed_ips, session.allowed_ports)

            self.session = session
            self.idle_deadline = None
            _write_log("WireGuard client session started (persistent mode).")

    def stop_session(self, reason: str = "stop", ignore_missing: bool = False) -> None:
        with self._session_lock:
            self._stop_session_locked(reason=reason, ignore_missing=ignore_missing)

    def bump_activity(self) -> None:
        return


_client: Optional[WireGuardClient] = None
_client_lock = threading.Lock()


def _get_client() -> WireGuardClient:
    global _client
    if _client is None:
        with _client_lock:
            if _client is None:
                _client = WireGuardClient()
    return _client


def _parse_allowed_ips(value: Any, fallback: Optional[str]) -> Optional[str]:
    if isinstance(value, list):
        if not value:
            return fallback
        return str(value[0])
    if isinstance(value, str) and value.strip():
        return value.strip()
    return fallback


def _coerce_int(value: Any, default: int) -> int:
    try:
        return int(value)
    except Exception:
        return default


def _parse_endpoint_host(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    text = str(value).strip()
    if not text:
        return None
    if text.startswith("["):
        match = re.match(r"^\[([^\]]+)\]", text)
        if match:
            return match.group(1)
    host, sep, _ = text.rpartition(":")
    if sep and host:
        return host
    return text


def _parse_endpoint_port(value: Optional[str]) -> Optional[int]:
    if not value:
        return None
    text = str(value).strip()
    if not text:
        return None
    if text.startswith("["):
        match = re.match(r"^\[[^\]]+\]:(\d+)$", text)
        if match:
            try:
                return int(match.group(1))
            except Exception:
                return None
        return None
    _, sep, port = text.rpartition(":")
    if sep and port.isdigit():
        try:
            return int(port)
        except Exception:
            return None
    return None


def _format_endpoint(host: str, port: Optional[int]) -> Optional[str]:
    if not host:
        return None
    text = str(host).strip()
    if not text:
        return None
    if ":" in text and not text.startswith("["):
        text = f"[{text}]"
    if port is None:
        return text
    return f"{text}:{port}"


def _parse_server_url_host(server_url: Optional[str]) -> Optional[str]:
    if not server_url:
        return None
    try:
        parsed = urlsplit(str(server_url).strip())
        if parsed.hostname:
            return parsed.hostname.strip()
    except Exception:
        pass
    text = str(server_url).strip()
    if not text:
        return None
    if "://" in text:
        text = text.split("://", 1)[1]
    text = text.split("/", 1)[0].strip()
    if text.startswith("[") and "]" in text:
        text = text[1:text.index("]")]
    if ":" in text:
        host_part, port_part = text.rsplit(":", 1)
        if port_part.isdigit():
            text = host_part
    text = text.strip()
    return text or None


def _is_loopback_host(host: Optional[str]) -> bool:
    if not host:
        return True
    text = str(host).strip().lower()
    if not text:
        return True
    if text == "localhost":
        return True
    try:
        ip = ipaddress.ip_address(text)
        return ip.is_loopback or ip.is_unspecified
    except Exception:
        return False


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.client = _get_client()
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")
        self._get_server_url = hooks.get("get_server_url")
        self._ensure_stop = threading.Event()
        self._ensure_thread = threading.Thread(target=self._ensure_loop, daemon=True)
        self._ensure_thread.start()

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="VPN_Tunnel/tunnel.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass
        _write_log(message)

    def _http_client(self) -> Optional[Any]:
        try:
            if callable(self._http_client_factory):
                return self._http_client_factory()
        except Exception:
            return None
        return None

    def _resolve_endpoint(self, endpoint: Optional[str], token: Dict[str, Any]) -> Optional[str]:
        server_url = None
        if callable(self._get_server_url):
            try:
                server_url = self._get_server_url()
            except Exception:
                server_url = None

        server_host = _parse_server_url_host(server_url)
        if not server_host:
            return endpoint

        endpoint_host = _parse_endpoint_host(endpoint)
        if endpoint_host and not _is_loopback_host(endpoint_host):
            return endpoint

        endpoint_port = _parse_endpoint_port(endpoint)
        if endpoint_port is None:
            endpoint_port = _coerce_int(token.get("port"), 0)
            if endpoint_port <= 0:
                endpoint_port = None

        resolved = _format_endpoint(server_host, endpoint_port)
        if resolved and endpoint and resolved != endpoint:
            self._log(f"WireGuard endpoint override: {endpoint} -> {resolved}")
        return resolved or endpoint

    def _build_session(self, payload: Any) -> Optional[SessionConfig]:
        if not isinstance(payload, dict):
            self._log("WireGuard start payload missing/invalid.", error=True)
            return None

        payload_agent_id = payload.get("agent_id") or payload.get("agent_guid")
        if payload_agent_id:
            if str(payload_agent_id).strip() != str(self.ctx.agent_id).strip():
                return None

        token = payload.get("token") or payload.get("orchestration_token")
        if not isinstance(token, dict):
            self._log("WireGuard start missing token payload.", error=True)
            return None

        tunnel_id = payload.get("tunnel_id") or token.get("tunnel_id")
        if not tunnel_id:
            self._log("WireGuard start missing tunnel_id.", error=True)
            return None

        virtual_ip = payload.get("virtual_ip") or payload.get("client_virtual_ip")
        endpoint = payload.get("endpoint") or payload.get("server_endpoint")
        endpoint = self._resolve_endpoint(endpoint, token)
        server_public_key = payload.get("server_public_key") or payload.get("public_key")
        engine_virtual_ip = payload.get("engine_virtual_ip") or payload.get("engine_ip")

        allowed_ips = _parse_allowed_ips(payload.get("allowed_ips"), engine_virtual_ip)
        if not allowed_ips:
            self._log("WireGuard start missing allowed_ips/engine_virtual_ip.", error=True)
            return None
        if "," in allowed_ips or allowed_ips.endswith("/0") or "/32" not in allowed_ips:
            self._log("WireGuard allowed_ips must be a single /32.", error=True)
            return None

        if not virtual_ip or not endpoint or not server_public_key:
            self._log("WireGuard start missing required fields.", error=True)
            return None
        if "/32" not in str(virtual_ip):
            self._log("WireGuard virtual_ip must be /32.", error=True)
            return None

        idle_seconds = _coerce_int(payload.get("idle_seconds"), 900)
        allowed_ports = payload.get("allowed_ports")
        if isinstance(allowed_ports, list):
            allowed_ports = ",".join(str(p) for p in allowed_ports)
        allowed_ports = str(allowed_ports or "")

        return SessionConfig(
            token=token,
            tunnel_id=str(tunnel_id),
            virtual_ip=str(virtual_ip),
            allowed_ips=str(allowed_ips),
            endpoint=str(endpoint),
            server_public_key=str(server_public_key),
            allowed_ports=allowed_ports,
            idle_seconds=idle_seconds,
            preshared_key=payload.get("preshared_key"),
            client_private_key=payload.get("client_private_key"),
            client_public_key=payload.get("client_public_key"),
        )

    def _request_persistent_session(self) -> Optional[Dict[str, Any]]:
        client = self._http_client()
        if client is None:
            return None
        try:
            payload = client.post_json(
                "/api/agent/vpn/ensure",
                {"agent_id": self.ctx.agent_id, "reason": "agent_boot"},
                require_auth=True,
            )
        except Exception as exc:
            self._log(f"WireGuard ensure request failed: {exc}", error=True)
            return None
        if not isinstance(payload, dict):
            return None
        return payload

    def _ensure_loop(self) -> None:
        if ENSURE_INITIAL_DELAY_SECONDS:
            time.sleep(ENSURE_INITIAL_DELAY_SECONDS)
        while not self._ensure_stop.is_set():
            payload = self._request_persistent_session()
            if payload:
                incoming_tunnel = str(payload.get("tunnel_id") or "")
                current_tunnel = ""
                if self.client.session is not None:
                    try:
                        current_tunnel = str(self.client.session.tunnel_id)
                    except Exception:
                        current_tunnel = ""
                state = None
                try:
                    state = self.client._service_state()
                except Exception:
                    state = None
                service_ready = state in ("RUNNING", "START_PENDING")
                if incoming_tunnel and incoming_tunnel == current_tunnel and service_ready:
                    self._ensure_stop.wait(ENSURE_INTERVAL_SECONDS)
                    continue
                session = self._build_session(payload)
                if session:
                    self._log("WireGuard persistent session ensure received.")
                    self.client.start_session(session, signing_client=self._http_client())
            self._ensure_stop.wait(ENSURE_INTERVAL_SECONDS)

    def register_events(self) -> None:
        sio = self.ctx.sio

        @sio.on("vpn_tunnel_start")
        async def _vpn_tunnel_start(payload):
            session = self._build_session(payload)
            if not session:
                return
            self._log("WireGuard start request received.")
            self.client.start_session(session, signing_client=self._http_client())

        @sio.on("vpn_tunnel_stop")
        async def _vpn_tunnel_stop(payload):
            reason = "server_stop"
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                reason = payload.get("reason") or reason
            self._log(f"WireGuard stop requested (reason={reason}); persistent tunnels ignore stop.")


        @sio.on("vpn_tunnel_activity")
        async def _vpn_tunnel_activity(payload):
            self.client.bump_activity()

    def stop_all(self) -> None:
        try:
            self._ensure_stop.set()
        except Exception:
            pass
        try:
            self.client.stop_session(reason="agent_shutdown")
        except Exception:
            self._log("Failed to stop WireGuard client during shutdown.", error=True)
