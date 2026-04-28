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
import socket
import subprocess
import threading
import time
import re
import shutil
from pathlib import Path
from typing import Any, Dict, Optional

try:
    from runtime_paths import agent_borealis_root, agent_logs_root, agent_runtime_root
except Exception:  # pragma: no cover - fallback for runtime path issues
    import sys

    base_dir = Path(__file__).resolve().parents[1]
    if str(base_dir) not in sys.path:
        sys.path.insert(0, str(base_dir))
    from runtime_paths import agent_borealis_root, agent_logs_root, agent_runtime_root

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

ROLE_NAME = "wireguard"
ROLE_CONTEXTS = ["system"]
TUNNEL_NAME = "Borealis"
TUNNEL_DISPLAY_NAME = "Borealis"
SERVICE_DISPLAY_NAME = "Borealis - WireGuard - Agent"
TUNNEL_IDLE_ADDRESS = "169.254.255.254/32"
FIREWALL_RULE_NAME = "Borealis - WireGuard - Agent"
DEFAULT_VNC_PORT = 5900
DEFAULT_SSH_PORT = 22


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
WIREGUARD_INTERFACE_MTU = _env_int("BOREALIS_WIREGUARD_MTU", 1420, min_value=1280, max_value=65535)


def _log_path() -> Path:
    root = agent_logs_root(__file__) / "VPN_Tunnel"
    root.mkdir(parents=True, exist_ok=True)
    return root / "tunnel.log"


def _write_log(message: str) -> None:
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [wg-client] {message}\n")
    except Exception:
        pass


def _firewall_state_path() -> Path:
    root = agent_borealis_root(__file__) / "Settings" / "WireGuard"
    root.mkdir(parents=True, exist_ok=True)
    return root / "firewall_state.json"


def _load_firewall_state() -> Dict[str, Any]:
    path = _firewall_state_path()
    if not path.is_file():
        return {}
    try:
        raw = path.read_text(encoding="utf-8", errors="ignore").strip()
    except Exception:
        return {}
    if not raw:
        return {}
    try:
        data = json.loads(raw)
    except Exception:
        return {}
    if isinstance(data, dict):
        return data
    return {}


def _save_firewall_state(state: Dict[str, Any]) -> None:
    path = _firewall_state_path()
    try:
        payload = json.dumps(state, ensure_ascii=True, sort_keys=True, indent=2)
        path.write_text(payload + "\n", encoding="ascii")
    except Exception:
        return




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


def _engine_virtual_ip_from_allowed_ips(allowed_ips: Any) -> str:
    raw = str(allowed_ips or "").split(",", 1)[0].strip()
    if not raw:
        return ""
    try:
        return str(ipaddress.ip_interface(raw).ip)
    except Exception:
        return raw.split("/", 1)[0].strip()


def _prime_engine_path(session: "SessionConfig", *, reason: str = "reuse") -> bool:
    engine_ip = _engine_virtual_ip_from_allowed_ips(session.allowed_ips)
    if not engine_ip:
        return False
    reason_text = str(reason or "reuse").strip() or "reuse"
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.settimeout(0.1)
            sock.sendto(b"borealis-wg-probe", (engine_ip, 9))
        _write_log(
            "WireGuard path prime sent engine_ip={0} tunnel_id={1} reason={2}".format(
                engine_ip,
                session.tunnel_id or "-",
                reason_text,
            )
        )
        return True
    except Exception as exc:
        _write_log(
            "WireGuard path prime failed engine_ip={0} tunnel_id={1} reason={2} error={3}".format(
                engine_ip,
                session.tunnel_id or "-",
                reason_text,
                exc,
            )
        )
        return False


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
        force_restart: bool = False,
        restart_reason: Optional[str] = None,
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
        self.force_restart = bool(force_restart)
        self.restart_reason = str(restart_reason or "").strip()


class WireGuardClient:
    def __init__(self) -> None:
        base = agent_runtime_root(__file__)
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

    def _service_is_healthy(self) -> bool:
        return self._service_state() in ("RUNNING", "START_PENDING")

    def _wait_for_service_state(
        self,
        *,
        healthy_states: tuple[str, ...] = ("RUNNING", "START_PENDING"),
        timeout_seconds: float = 8.0,
        poll_interval: float = 0.5,
    ) -> Optional[str]:
        state = self._service_state()
        deadline = time.time() + max(timeout_seconds, 0.0)
        while state not in healthy_states and time.time() < deadline:
            time.sleep(max(poll_interval, 0.0))
            state = self._service_state()
        return state

    def _log_recovery_event(
        self,
        outcome: str,
        *,
        reason: str,
        tunnel_id: str,
        prior_state: Optional[str],
        detail: Optional[str] = None,
    ) -> None:
        message = "vpn_agent_recovery_{0} reason={1} tunnel_id={2} prior_service_state={3}".format(
            outcome,
            reason or "-",
            tunnel_id or "-",
            prior_state or "-",
        )
        if detail:
            message = "{0} detail={1}".format(message, detail)
        _write_log(message)

    def _wireguard_config_path(self) -> Path:
        settings_dir = self.temp_root.parent / "Settings" / "WireGuard"
        try:
            settings_dir.mkdir(parents=True, exist_ok=True)
        except Exception:
            pass
        return settings_dir / f"{self.service_name}.conf"

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
        mtu = getattr(self, "_interface_mtu", WIREGUARD_INTERFACE_MTU)
        return "\n".join(
            [
                "[Interface]",
                f"PrivateKey = {private_key}",
                f"Address = {TUNNEL_IDLE_ADDRESS}",
                "ListenPort = 0",
                f"MTU = {mtu}",
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
            fallback_ports = [_resolve_shell_port(), _resolve_vnc_port(), DEFAULT_SSH_PORT]
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
        state = _load_firewall_state()
        configured = bool(state.get("configured"))
        state_remote = str(state.get("remote") or "")
        state_ports = state.get("ports") or []
        if isinstance(state_ports, str):
            state_ports = _parse_allowed_ports(state_ports)
        if configured and state_remote == remote and list(state_ports) == list(ports):
            _write_log("WireGuard firewall already configured; skipping updates.")
            return

        base_name = FIREWALL_RULE_NAME.replace("'", "''")
        protocols = ("TCP", "UDP")
        for protocol in protocols:
            rule_name = f"{base_name} ({protocol})"
            remove_command = (
                "Get-NetFirewallRule -DisplayName '{name}' -ErrorAction SilentlyContinue | "
                "Remove-NetFirewallRule -ErrorAction SilentlyContinue"
            ).format(name=rule_name)
            try:
                subprocess.run(
                    ["powershell.exe", "-NoProfile", "-Command", remove_command],
                    capture_output=True,
                    text=True,
                    check=False,
                )
            except Exception:
                pass

        for protocol in protocols:
            rule_name = f"{base_name} ({protocol})"
            command = (
                "New-NetFirewallRule -DisplayName '{name}' -Direction Inbound -Action Allow "
                "-Protocol {protocol} -LocalPort {ports} -RemoteAddress {remote} -Profile Any | Out-Null"
            ).format(name=rule_name, remote=remote, ports=port_text, protocol=protocol)
            try:
                result = subprocess.run(
                    ["powershell.exe", "-NoProfile", "-Command", command],
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if result.returncode != 0:
                    detail = (result.stderr or result.stdout or "").strip()
                    _write_log(
                        f"Failed to create tunnel firewall rule ({protocol}): {detail}"
                    )
                else:
                    _write_log(
                        f"Created tunnel firewall rule for {remote} ports={port_text} protocol={protocol}."
                    )
            except Exception as exc:
                _write_log(f"Failed to create tunnel firewall rule ({protocol}): {exc}")
        _save_firewall_state({"configured": True, "remote": remote, "ports": ports})

    def _remove_shell_firewall(self) -> None:
        _write_log("WireGuard firewall teardown skipped (always-on).")

    def _service_exists(self) -> bool:
        code, _, _ = self._run(["sc.exe", "query", self._service_id()])
        if code == 0:
            return True
        return self._service_reg_exists()

    def _install_service(self, config_path: Optional[Path] = None) -> bool:
        target_path = config_path or self.conf_path
        code, out, err = self._run([self._wg_exe, "/installtunnelservice", str(target_path)])
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

    def _path_text(self, path: Optional[Path]) -> str:
        if path is None:
            return ""
        try:
            return str(path).strip()
        except Exception:
            return ""

    def _paths_equivalent(self, left: Optional[Path], right: Optional[Path]) -> bool:
        left_text = self._path_text(left)
        right_text = self._path_text(right)
        if not left_text or not right_text:
            return False
        normalized_left = left_text.replace("/", "\\").rstrip("\\").lower()
        normalized_right = right_text.replace("/", "\\").rstrip("\\").lower()
        return normalized_left == normalized_right

    def _service_binding_needs_repair(self, service_config_path: Optional[Path]) -> bool:
        if not self._service_exists():
            return False
        if not service_config_path:
            _write_log("WireGuard tunnel service config path missing; service reinstall required.")
            return True
        if self._paths_equivalent(service_config_path, self.conf_path):
            return False
        _write_log(
            "WireGuard tunnel service bound to stale config path {0}; expected {1}.".format(
                service_config_path,
                self.conf_path,
            )
        )
        return True

    def _uninstall_service(self) -> bool:
        code, out, err = self._run([self._wg_exe, "/uninstalltunnelservice", self.service_name])
        detail = (err or out or "").strip()
        if code != 0 and "not installed" not in detail.lower():
            _write_log(f"WireGuard tunnel service uninstall returned code={code} err={detail}")
        for _ in range(10):
            if not self._service_exists():
                return True
            time.sleep(0.5)
        return not self._service_exists()

    def _reinstall_service(self) -> bool:
        _write_log(f"Repairing WireGuard tunnel service binding using config {self.conf_path}.")
        try:
            self._run(["sc.exe", "stop", self._service_id()])
        except Exception:
            pass
        time.sleep(1)
        if not self._uninstall_service():
            _write_log("Failed to remove existing WireGuard tunnel service during repair.")
            return False
        if not self._install_service(config_path=self.conf_path):
            return False
        if not self._service_exists() and not self._last_install_already_present:
            _write_log("WireGuard tunnel service still missing after repair install attempt.")
            return False
        service_config_path = self._service_config_path()
        if self._service_binding_needs_repair(service_config_path):
            _write_log("WireGuard tunnel service binding repair did not converge on the runtime config path.")
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
        mtu = getattr(self, "_interface_mtu", WIREGUARD_INTERFACE_MTU)
        lines = [
            "[Interface]",
            f"PrivateKey = {private_key}",
            f"Address = {session.virtual_ip}",
            f"MTU = {mtu}",
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
            recovery_reason: Optional[str] = None
            prior_service_state: Optional[str] = None
            previous_tunnel_id = ""
            if self.session:
                previous_tunnel_id = str(self.session.tunnel_id or "")
                if self.session.tunnel_id == session.tunnel_id:
                    prior_service_state = self._service_state()
                    same_config = _session_config_equivalent(self.session, session)
                    if session.force_restart and prior_service_state in ("RUNNING", "START_PENDING"):
                        recovery_reason = session.restart_reason or "force_restart"
                        _write_log(
                            "WireGuard session force restart requested (reason={0}).".format(
                                recovery_reason
                            )
                        )
                    elif prior_service_state in ("RUNNING", "START_PENDING") and same_config:
                        _prime_engine_path(session, reason=session.restart_reason or "reuse")
                        _write_log("WireGuard session already active; reusing existing session.")
                        self.bump_activity()
                        return
                    if not session.force_restart:
                        recovery_reason = (
                            "same_tunnel_id_config_drift"
                            if prior_service_state in ("RUNNING", "START_PENDING")
                            else "same_tunnel_id_service_unhealthy"
                        )
                    self._log_recovery_event(
                        "attempt",
                        reason=recovery_reason,
                        tunnel_id=session.tunnel_id,
                        prior_state=prior_service_state,
                    )
                else:
                    prior_service_state = self._service_state()
                    if prior_service_state in ("RUNNING", "START_PENDING"):
                        recovery_reason = session.restart_reason or "tunnel_id_changed"
                        _write_log(
                            "WireGuard session tunnel_id changed; restarting active session "
                            "(existing_tunnel_id={0} new_tunnel_id={1}).".format(
                                previous_tunnel_id or "-",
                                session.tunnel_id or "-",
                            )
                        )
                        self._log_recovery_event(
                            "attempt",
                            reason=recovery_reason,
                            tunnel_id=session.tunnel_id,
                            prior_state=prior_service_state,
                        )

            try:
                self._validate_token(session.token, signing_client=signing_client)
            except Exception as exc:
                _write_log(f"Refusing to start WireGuard session: {exc}")
                if recovery_reason:
                    self._log_recovery_event(
                        "failed",
                        reason=recovery_reason,
                        tunnel_id=session.tunnel_id,
                        prior_state=prior_service_state,
                        detail="token_validation_failed",
                    )
                return

            if self.session and previous_tunnel_id and previous_tunnel_id != str(session.tunnel_id or ""):
                _write_log(
                    "WireGuard session replace: existing_tunnel_id={0} new_tunnel_id={1}".format(
                        previous_tunnel_id,
                        session.tunnel_id,
                    )
                )
                self.session = None
                self.idle_deadline = None

            rendered = self._render_config(session)
            if not self._write_config(rendered):
                _write_log("Failed to write WireGuard client config.")
                if recovery_reason:
                    self._log_recovery_event(
                        "failed",
                        reason=recovery_reason,
                        tunnel_id=session.tunnel_id,
                        prior_state=prior_service_state,
                        detail="config_write_failed",
                    )
                return
            _write_log(f"Rendered WireGuard client config to {self.conf_path}")

            service_config_path = self._service_config_path()
            needs_binding_repair = self._service_binding_needs_repair(service_config_path)
            if not self._service_exists():
                if not self._install_service():
                    if recovery_reason:
                        self._log_recovery_event(
                            "failed",
                            reason=recovery_reason,
                            tunnel_id=session.tunnel_id,
                            prior_state=prior_service_state,
                            detail="service_install_failed",
                        )
                    return
                service_config_path = self._service_config_path()
                needs_binding_repair = self._service_binding_needs_repair(service_config_path)

            service_present = self._service_exists()
            if not service_present and self._last_install_already_present:
                _write_log("WireGuard tunnel service presence inferred from install response.")
                service_present = True
            if not service_present:
                _write_log("WireGuard tunnel service still missing after install attempt.")
                if recovery_reason:
                    self._log_recovery_event(
                        "failed",
                        reason=recovery_reason,
                        tunnel_id=session.tunnel_id,
                        prior_state=prior_service_state,
                        detail="service_missing_after_install",
                    )
                return

            if needs_binding_repair:
                if not self._reinstall_service():
                    if recovery_reason:
                        self._log_recovery_event(
                            "failed",
                            reason=recovery_reason,
                            tunnel_id=session.tunnel_id,
                            prior_state=prior_service_state,
                            detail="service_reinstall_failed",
                        )
                    return

            self._restart_service()
            current_state = self._wait_for_service_state()
            if current_state not in ("RUNNING", "START_PENDING"):
                _write_log("WireGuard tunnel service unhealthy after restart; attempting service repair.")
                if not self._reinstall_service():
                    if recovery_reason:
                        self._log_recovery_event(
                            "failed",
                            reason=recovery_reason,
                            tunnel_id=session.tunnel_id,
                            prior_state=prior_service_state,
                            detail="service_reinstall_failed",
                        )
                    return
                self._restart_service()
                current_state = self._wait_for_service_state()
                if current_state not in ("RUNNING", "START_PENDING"):
                    _write_log("WireGuard tunnel service failed to reach RUNNING after repair attempt.")
                    if recovery_reason:
                        self._log_recovery_event(
                            "failed",
                            reason=recovery_reason,
                            tunnel_id=session.tunnel_id,
                            prior_state=prior_service_state,
                            detail="service_not_healthy_after_restart",
                        )
                    return

            self._ensure_adapter_name()
            self._ensure_service_display_name()
            self._ensure_shell_firewall(session.allowed_ips, session.allowed_ports)

            self.session = session
            self.idle_deadline = None
            if recovery_reason:
                self._log_recovery_event(
                    "success",
                    reason=recovery_reason,
                    tunnel_id=session.tunnel_id,
                    prior_state=prior_service_state,
                )
            _write_log("WireGuard client session started (persistent mode).")

    def stop_session(self, reason: str = "stop", ignore_missing: bool = False) -> None:
        with self._session_lock:
            self._stop_session_locked(reason=reason, ignore_missing=ignore_missing)

    def bump_activity(self) -> None:
        return


class LinuxWireGuardClient:
    def __init__(self) -> None:
        base = agent_runtime_root(__file__)
        self.cert_root = base / "Borealis" / "Certificates" / "VPN_Client"
        self.settings_root = base / "Borealis" / "Settings" / "WireGuard"
        self.settings_root.mkdir(parents=True, exist_ok=True)
        self.interface_name = self._resolve_interface_name()
        self.conf_path = self.settings_root / f"{self.interface_name}.conf"
        self.session: Optional[SessionConfig] = None
        self._session_lock = threading.Lock()
        self._client_keys = _generate_client_keys(self.cert_root)
        self._interface_mtu = WIREGUARD_INTERFACE_MTU
        self._wg_quick = shutil.which("wg-quick") or ""
        self._wg = shutil.which("wg") or ""
        self._ip = shutil.which("ip") or ""

    def _resolve_interface_name(self) -> str:
        raw = (os.environ.get("BOREALIS_WIREGUARD_INTERFACE") or TUNNEL_NAME or "borealis").strip().lower()
        cleaned = re.sub(r"[^a-z0-9_.-]", "", raw)
        if not cleaned:
            cleaned = "borealis"
        return cleaned[:15]

    def _run(self, args: list[str]) -> tuple[int, str, str]:
        try:
            proc = subprocess.run(args, capture_output=True, text=True, check=False)
            return proc.returncode, (proc.stdout or "").strip(), (proc.stderr or "").strip()
        except Exception as exc:  # pragma: no cover - runtime guard
            return 1, "", str(exc)

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

        if sig_alg and str(sig_alg).lower() not in ("ed25519", "eddsa"):
            raise ValueError("Unsupported token signature algorithm")
        payload_bytes = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
        if not verify_and_store_script_signature(signing_client, payload_bytes, str(signature), signing_key):
            raise ValueError("Token signature invalid")

    def _render_config(self, session: SessionConfig) -> str:
        private_key = session.client_private_key or self._client_keys["private"]
        mtu = getattr(self, "_interface_mtu", WIREGUARD_INTERFACE_MTU)
        lines = [
            "[Interface]",
            f"PrivateKey = {private_key}",
            f"Address = {session.virtual_ip}",
            f"MTU = {mtu}",
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

    def _write_config(self, text: str) -> bool:
        try:
            self.conf_path.parent.mkdir(parents=True, exist_ok=True)
            self.conf_path.write_text(text + "\n", encoding="ascii")
            return True
        except Exception as exc:
            _write_log(f"Failed to write WireGuard config at {self.conf_path}: {exc}")
            return False

    def _interface_exists(self) -> bool:
        if not self._ip:
            return False
        code, _, _ = self._run([self._ip, "link", "show", "dev", self.interface_name])
        return code == 0

    def _service_state(self) -> Optional[str]:
        if self._wg:
            code, out, _ = self._run([self._wg, "show", self.interface_name])
            if code == 0 and out:
                return "RUNNING"
        if self._interface_exists():
            return "RUNNING"
        return None

    def _ensure_interface_mtu(self) -> None:
        ip_cmd = getattr(self, "_ip", "") or ""
        interface_name = str(getattr(self, "interface_name", "") or "").strip()
        mtu = int(getattr(self, "_interface_mtu", WIREGUARD_INTERFACE_MTU))
        if not ip_cmd or not interface_name:
            return
        try:
            if not self._interface_exists():
                return
            code, out, err = self._run([ip_cmd, "link", "set", "dev", interface_name, "mtu", str(mtu)])
        except Exception as exc:
            _write_log(f"Failed to reapply WireGuard Linux MTU on {interface_name}: {exc}")
            return
        if code != 0:
            detail = (err or out or "unknown error").strip()
            _write_log(f"Failed to reapply WireGuard Linux MTU on {interface_name}: {detail}")

    def _bring_down(self) -> None:
        if self._wg_quick and self.conf_path.is_file():
            self._run([self._wg_quick, "down", str(self.conf_path)])
        if self._ip and self._interface_exists():
            self._run([self._ip, "link", "delete", "dev", self.interface_name])

    def _bring_up(self) -> bool:
        if not self._wg_quick:
            _write_log("WireGuard tools missing on Linux agent: 'wg-quick' not found.")
            return False
        code, out, err = self._run([self._wg_quick, "up", str(self.conf_path)])
        if code == 0:
            return True
        detail = (err or out or "").strip()
        _write_log(f"WireGuard Linux up failed: {detail or 'unknown error'}")
        # Retry once after forced down in case stale interface exists.
        self._bring_down()
        code, out, err = self._run([self._wg_quick, "up", str(self.conf_path)])
        if code == 0:
            return True
        detail = (err or out or "").strip()
        _write_log(f"WireGuard Linux retry up failed: {detail or 'unknown error'}")
        return False

    def _stop_session_locked(self, reason: str = "stop", ignore_missing: bool = False) -> None:
        if not ignore_missing and not self._interface_exists():
            _write_log("WireGuard Linux interface not found when stopping session.")
        self._bring_down()
        self.session = None
        _write_log(f"WireGuard Linux session stopped (reason={reason}).")

    def start_session(self, session: SessionConfig, *, signing_client: Optional[Any] = None) -> None:
        with self._session_lock:
            previous_tunnel_id = ""
            if self.session and self.session.tunnel_id == session.tunnel_id:
                same_config = _session_config_equivalent(self.session, session)
                if self._service_state() == "RUNNING" and same_config and not session.force_restart:
                    self._ensure_interface_mtu()
                    _prime_engine_path(session, reason=session.restart_reason or "reuse")
                    _write_log("WireGuard Linux session already active; reusing existing session.")
                    return
                if session.force_restart:
                    _write_log(
                        "WireGuard Linux session force restart requested (reason={0}).".format(
                            session.restart_reason or "force_restart"
                        )
                    )
                else:
                    _write_log("WireGuard Linux session config drift detected; refreshing existing tunnel.")
            elif self.session:
                previous_tunnel_id = str(self.session.tunnel_id or "")
                if self._service_state() == "RUNNING":
                    _write_log(
                        "WireGuard Linux session tunnel_id changed; restarting active session "
                        "(existing_tunnel_id={0} new_tunnel_id={1}).".format(
                            previous_tunnel_id or "-",
                            session.tunnel_id or "-",
                        )
                    )

            try:
                self._validate_token(session.token, signing_client=signing_client)
            except Exception as exc:
                _write_log(f"Refusing to start WireGuard Linux session: {exc}")
                return

            if self.session and previous_tunnel_id and previous_tunnel_id != str(session.tunnel_id or ""):
                _write_log(
                    "WireGuard Linux session replace: existing_tunnel_id={0} new_tunnel_id={1}".format(
                        previous_tunnel_id, session.tunnel_id
                    )
                )
                self._stop_session_locked(reason="session_replace", ignore_missing=True)

            rendered = self._render_config(session)
            if not self._write_config(rendered):
                return
            _write_log(f"Rendered WireGuard Linux client config to {self.conf_path}")

            # Apply latest configuration cleanly.
            self._bring_down()
            if not self._bring_up():
                return
            self._ensure_interface_mtu()

            self.session = session
            _write_log("WireGuard Linux session started (persistent mode).")

    def stop_session(self, reason: str = "stop", ignore_missing: bool = False) -> None:
        with self._session_lock:
            self._stop_session_locked(reason=reason, ignore_missing=ignore_missing)

    def bump_activity(self) -> None:
        return


_client: Optional[WireGuardClient] = None
_client_lock = threading.Lock()


def _get_client() -> Any:
    global _client
    if _client is None:
        with _client_lock:
            if _client is None:
                if os.name == "nt":
                    _client = WireGuardClient()
                else:
                    _client = LinuxWireGuardClient()
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


def _normalized_session_field(value: Optional[str]) -> str:
    return str(value or "").strip()


def _session_config_equivalent(current: Optional[SessionConfig], desired: Optional[SessionConfig]) -> bool:
    if current is None or desired is None:
        return False
    return (
        _normalized_session_field(current.tunnel_id) == _normalized_session_field(desired.tunnel_id)
        and _normalized_session_field(current.virtual_ip) == _normalized_session_field(desired.virtual_ip)
        and _normalized_session_field(current.allowed_ips) == _normalized_session_field(desired.allowed_ips)
        and _normalized_session_field(current.endpoint) == _normalized_session_field(desired.endpoint)
        and _normalized_session_field(current.server_public_key) == _normalized_session_field(desired.server_public_key)
        and _normalized_session_field(current.allowed_ports) == _normalized_session_field(desired.allowed_ports)
    )


def _session_transport_equivalent(current: Optional[SessionConfig], desired: Optional[SessionConfig]) -> bool:
    if current is None or desired is None:
        return False
    return (
        _normalized_session_field(current.virtual_ip) == _normalized_session_field(desired.virtual_ip)
        and _normalized_session_field(current.allowed_ips) == _normalized_session_field(desired.allowed_ips)
        and _normalized_session_field(current.endpoint) == _normalized_session_field(desired.endpoint)
        and _normalized_session_field(current.server_public_key) == _normalized_session_field(desired.server_public_key)
        and _normalized_session_field(current.allowed_ports) == _normalized_session_field(desired.allowed_ports)
        and _normalized_session_field(current.preshared_key) == _normalized_session_field(desired.preshared_key)
        and _normalized_session_field(current.client_private_key) == _normalized_session_field(desired.client_private_key)
        and _normalized_session_field(current.client_public_key) == _normalized_session_field(desired.client_public_key)
    )


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.client = _get_client()
        self.role_health_label = "WireGuard Service"
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")
        self._last_tunnel_snapshot: Dict[str, Any] = {}
        self._ensure_stop = threading.Event()
        self._ensure_cycle_lock = threading.Lock()
        self._ensure_thread_lock = threading.Lock()
        self._ensure_thread: Optional[threading.Thread] = None
        self._last_ready_notification_key: Optional[tuple[str, str]] = None
        self._last_ready_notification_at = 0.0
        try:
            self._log(
                "WireGuard role initialized runtime_root={0} config_path={1}".format(
                    agent_runtime_root(__file__),
                    self.client.conf_path,
                )
            )
        except Exception:
            pass
        self._start_ensure_thread(reason="role_init")

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
        _ = token
        return endpoint

    def _session_config_matches_live(self, session: SessionConfig) -> bool:
        current = getattr(getattr(self, "client", None), "session", None)
        if current is not None and not _session_transport_equivalent(current, session):
            return False
        snapshot = self._read_live_config_snapshot()
        if not snapshot.get("active_config"):
            return False
        expected_virtual_ip = str(session.virtual_ip or "").strip()
        expected_endpoint = str(session.endpoint or "").strip()
        live_virtual_ip = str(snapshot.get("virtual_ip") or "").strip()
        live_endpoint = str(snapshot.get("endpoint") or "").strip()
        if expected_virtual_ip and live_virtual_ip != expected_virtual_ip:
            return False
        if expected_endpoint and live_endpoint != expected_endpoint:
            return False
        return True

    def _remember_tunnel_snapshot(self, payload: Any) -> None:
        if not isinstance(payload, dict):
            return
        token = payload.get("token") or payload.get("orchestration_token")
        if not isinstance(token, dict):
            token = {}
        endpoint = payload.get("endpoint") or payload.get("server_endpoint")
        resolved_endpoint = self._resolve_endpoint(endpoint, token)
        self._last_tunnel_snapshot = {
            "tunnel_id": str(payload.get("tunnel_id") or token.get("tunnel_id") or "").strip(),
            "virtual_ip": str(payload.get("virtual_ip") or payload.get("client_virtual_ip") or "").strip(),
            "endpoint": str(resolved_endpoint or endpoint or "").strip(),
            "server_public_key": str(payload.get("server_public_key") or payload.get("public_key") or "").strip(),
            "observed_at": int(time.time()),
        }

    def _live_config_paths(self) -> list[Path]:
        candidates: list[Path] = []
        conf_path = getattr(self.client, "conf_path", None)
        if isinstance(conf_path, Path):
            candidates.append(conf_path)
        service_config_getter = getattr(self.client, "_service_config_path", None)
        if callable(service_config_getter):
            try:
                service_path = service_config_getter()
            except Exception:
                service_path = None
            if isinstance(service_path, Path):
                candidates.append(service_path)
        unique: list[Path] = []
        seen: set[str] = set()
        for candidate in candidates:
            try:
                key = str(candidate.resolve())
            except Exception:
                key = str(candidate)
            if key in seen:
                continue
            seen.add(key)
            unique.append(candidate)
        return unique

    def _read_live_config_snapshot(self) -> Dict[str, Any]:
        snapshot: Dict[str, Any] = {
            "virtual_ip": "",
            "wireguard_peer_ip": "",
            "endpoint": "",
            "active_config": False,
        }
        for path in self._live_config_paths():
            try:
                if not path.is_file():
                    continue
                raw = path.read_text(encoding="utf-8", errors="ignore")
            except Exception:
                continue
            virtual_ip = ""
            endpoint = ""
            for line in raw.splitlines():
                stripped = line.split("#", 1)[0].strip()
                if "=" not in stripped:
                    continue
                key, value = [part.strip() for part in stripped.split("=", 1)]
                key_lower = key.lower()
                if key_lower == "address" and not virtual_ip:
                    virtual_ip = value.split(",", 1)[0].strip()
                elif key_lower == "endpoint" and not endpoint:
                    endpoint = value.strip()
            peer_ip = virtual_ip.split("/", 1)[0] if virtual_ip else ""
            is_idle = virtual_ip == TUNNEL_IDLE_ADDRESS or peer_ip == TUNNEL_IDLE_ADDRESS.split("/", 1)[0]
            active_config = bool(virtual_ip and not is_idle)
            if active_config:
                return {
                    "virtual_ip": virtual_ip,
                    "wireguard_peer_ip": peer_ip,
                    "endpoint": endpoint,
                    "active_config": True,
                }
            if virtual_ip and not snapshot.get("virtual_ip"):
                snapshot["virtual_ip"] = virtual_ip
                snapshot["wireguard_peer_ip"] = peer_ip
            if endpoint and not snapshot.get("endpoint"):
                snapshot["endpoint"] = endpoint
        return snapshot

    def _build_session(self, payload: Any) -> Optional[SessionConfig]:
        if not isinstance(payload, dict):
            self._log("WireGuard start payload missing/invalid.", error=True)
            return None

        self._remember_tunnel_snapshot(payload)

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
            force_restart=bool(payload.get("force_restart")),
            restart_reason=payload.get("restart_reason"),
        )

    def health_report(self) -> dict:
        session = getattr(self.client, "session", None)
        tunnel_id = ""
        peer_ip = ""
        endpoint = ""
        if session is not None:
            try:
                tunnel_id = str(session.tunnel_id or "").strip()
            except Exception:
                tunnel_id = ""
            try:
                peer_ip = str(session.virtual_ip or "").strip()
            except Exception:
                peer_ip = ""
            try:
                endpoint = str(session.endpoint or "").strip()
            except Exception:
                endpoint = ""
        remembered_snapshot = self._last_tunnel_snapshot if isinstance(self._last_tunnel_snapshot, dict) else {}
        live_config_snapshot = self._read_live_config_snapshot()
        if not tunnel_id:
            tunnel_id = str(remembered_snapshot.get("tunnel_id") or "").strip()
        if not peer_ip:
            peer_ip = str(
                remembered_snapshot.get("virtual_ip")
                or live_config_snapshot.get("virtual_ip")
                or ""
            ).strip()
        if not endpoint:
            endpoint = str(
                live_config_snapshot.get("endpoint")
                or remembered_snapshot.get("endpoint")
                or ""
            ).strip()
        try:
            service_state = self.client._service_state()
        except Exception:
            service_state = None
        thread_alive = bool(self._ensure_thread and self._ensure_thread.is_alive())
        peer_ip_display = peer_ip.split("/", 1)[0] if peer_ip else ""
        detail_suffix = f" tunnel_id={tunnel_id}" if tunnel_id else ""
        live_config_active = bool(live_config_snapshot.get("active_config"))
        details = {
            "running_status": str(service_state or "Stopped"),
            "wireguard_peer_ip": peer_ip_display,
            "tunnel_id": tunnel_id,
            "endpoint": endpoint,
        }
        if service_state in ("RUNNING", "START_PENDING") and session is not None and peer_ip_display:
            return {
                "status": "healthy",
                "role_label": self.role_health_label,
                "detail": f"Persistent tunnel active (state={service_state or 'RUNNING'}).{detail_suffix}",
                "details": details,
            }
        if service_state in ("RUNNING", "START_PENDING") and live_config_active and peer_ip_display:
            return {
                "status": "healthy",
                "role_label": self.role_health_label,
                "detail": (
                    f"Persistent tunnel active (state={service_state or 'RUNNING'}; "
                    f"local session metadata rehydrating).{detail_suffix}"
                ),
                "details": details,
            }
        if service_state in ("RUNNING", "START_PENDING") and session is not None and not peer_ip_display:
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": f"Tunnel service is {service_state or 'RUNNING'} but no peer IP is assigned yet.{detail_suffix}",
                "details": details,
            }
        if not thread_alive:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "Persistent ensure loop stopped.",
                "details": details,
            }
        if session is not None or live_config_active or tunnel_id or peer_ip_display:
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": f"Tunnel expected but service state is {service_state or 'stopped'}.{detail_suffix}",
                "details": details,
            }
        return {
            "status": "recovering",
            "role_label": self.role_health_label,
            "detail": "Awaiting persistent tunnel session bootstrap.",
            "details": details,
        }

    def _request_persistent_session(self, *, reason: str = "agent_boot") -> Optional[Dict[str, Any]]:
        client = self._http_client()
        if client is None:
            return None
        try:
            payload = client.post_json(
                "/api/agent/vpn/ensure",
                {"agent_id": self.ctx.agent_id, "reason": str(reason or "agent_boot")},
                require_auth=True,
            )
        except Exception as exc:
            self._log(f"WireGuard ensure request failed: {exc}", error=True)
            return None
        if not isinstance(payload, dict):
            return None
        return payload

    def _session_is_active(self, session: SessionConfig) -> tuple[bool, str]:
        current = getattr(self.client, "session", None)
        current_tunnel = ""
        try:
            current_tunnel = str(getattr(current, "tunnel_id", "") or "")
        except Exception:
            current_tunnel = ""
        if current_tunnel != str(session.tunnel_id or ""):
            return False, ""
        try:
            wait_for_state = getattr(self.client, "_wait_for_service_state", None)
            if callable(wait_for_state):
                service_state = str(
                    wait_for_state(
                        healthy_states=("RUNNING",),
                        timeout_seconds=8.0,
                        poll_interval=0.5,
                    )
                    or ""
                )
            else:
                service_state = str(self.client._service_state() or "")
        except Exception:
            service_state = ""
        return service_state == "RUNNING", service_state

    def _notify_engine_ready(self, session: SessionConfig, *, reason: str) -> None:
        active, service_state = self._session_is_active(session)
        if not active:
            return
        ready_reason = str(reason or "unknown")
        allowed_ports = ",".join(str(port) for port in _parse_allowed_ports(session.allowed_ports))
        key = (str(session.tunnel_id or ""), allowed_ports)
        now = time.time()
        if ready_reason == "agent_boot" and self._last_ready_notification_key == key:
            if now - float(self._last_ready_notification_at or 0.0) < 60.0:
                return
        client = self._http_client()
        if client is None:
            return
        payload = {
            "agent_id": self.ctx.agent_id,
            "tunnel_id": session.tunnel_id,
            "virtual_ip": session.virtual_ip,
            "allowed_ports": _parse_allowed_ports(session.allowed_ports),
            "service_state": service_state,
            "reason": ready_reason,
        }
        try:
            client.post_json("/api/agent/vpn/ready", payload, require_auth=True)
            self._last_ready_notification_key = key
            self._last_ready_notification_at = now
            self._log(
                "WireGuard readiness reported to Engine (reason={0} tunnel_id={1} ports={2}).".format(
                    ready_reason,
                    session.tunnel_id or "-",
                    allowed_ports or "-",
                )
            )
        except Exception as exc:
            self._log(f"WireGuard readiness report failed: {exc}", error=True)

    def _run_ensure_cycle(self, *, reason: str = "agent_boot") -> None:
        with self._ensure_cycle_lock:
            payload = self._request_persistent_session(reason=reason)
            if not payload:
                return
            session = self._build_session(payload)
            if not session:
                return
            incoming_tunnel = str(session.tunnel_id or "")
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
                if self._session_config_matches_live(session):
                    self._notify_engine_ready(session, reason=reason)
                    return
                self._log("WireGuard persistent ensure detected config drift; forcing live session refresh.")
                try:
                    self.client.stop_session(reason="ensure_config_refresh", ignore_missing=True)
                except Exception as exc:
                    self._log(f"WireGuard config refresh stop failed: {exc}", error=True)
            self._log("WireGuard persistent session ensure received.")
            self.client.start_session(session, signing_client=self._http_client())
            self._notify_engine_ready(session, reason=reason)

    def _run_ensure_cycle_safe(self, *, reason: str, source: str) -> None:
        try:
            self._run_ensure_cycle(reason=reason)
        except Exception as exc:
            self._log(
                f"WireGuard ensure cycle failed (source={source} reason={reason}): {exc}",
                error=True,
            )

    def _ensure_loop(self) -> None:
        if ENSURE_INITIAL_DELAY_SECONDS:
            if self._ensure_stop.wait(ENSURE_INITIAL_DELAY_SECONDS):
                return
        while not self._ensure_stop.is_set():
            self._run_ensure_cycle_safe(reason="agent_boot", source="periodic")
            if self._ensure_stop.wait(ENSURE_INTERVAL_SECONDS):
                return

    def _start_ensure_thread(self, *, reason: str) -> None:
        with self._ensure_thread_lock:
            if self._ensure_stop.is_set():
                return
            if self._ensure_thread is not None and self._ensure_thread.is_alive():
                return
            self._ensure_thread = threading.Thread(
                target=self._ensure_loop,
                name="borealis-wireguard-ensure",
                daemon=True,
            )
            self._ensure_thread.start()
        self._log(f"WireGuard ensure supervisor started (reason={reason}).")

    def request_immediate_ensure(self, *, reason: str) -> None:
        request_reason = str(reason or "manual")
        self._start_ensure_thread(reason=request_reason)
        if self._ensure_stop.is_set():
            return
        self._log(f"WireGuard immediate ensure requested (reason={request_reason}).")
        threading.Thread(
            target=self._run_ensure_cycle_safe,
            kwargs={"reason": request_reason, "source": "immediate"},
            name=f"borealis-wireguard-ensure-{request_reason}",
            daemon=True,
        ).start()

    def register_events(self) -> None:
        sio = self.ctx.sio

        @sio.on("vpn_tunnel_start")
        async def _vpn_tunnel_start(payload):
            self._start_ensure_thread(reason="vpn_tunnel_start")
            session = self._build_session(payload)
            if not session:
                return
            self._log("WireGuard start request received.")
            self.client.start_session(session, signing_client=self._http_client())
            self._notify_engine_ready(session, reason="vpn_tunnel_start")

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
        ensure_thread = self._ensure_thread
        if ensure_thread is not None:
            try:
                ensure_thread.join(timeout=1.0)
            except Exception:
                pass
        try:
            self.client.stop_session(reason="agent_shutdown")
        except Exception:
            self._log("Failed to stop WireGuard client during shutdown.", error=True)
