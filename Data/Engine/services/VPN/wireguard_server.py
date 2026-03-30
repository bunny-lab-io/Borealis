# ======================================================
# Data\Engine\services\VPN\wireguard_server.py
# Description: WireGuard server configuration scaffold (UDP/30000, host-only peers, ACL defaults, live peer reconciliation).
#
# API Endpoints (if applicable): None
# ======================================================

"""WireGuard server scaffolding for the Engine runtime.

This module prepares WireGuard server material (keys, config rendering, ACL
defaults) and applies listener/firewall state. Windows uses WireGuard tunnel
services while Linux keeps one persistent interface online and mutates peers
live with WireGuard tooling.
"""

from __future__ import annotations

import base64
import ipaddress
import logging
import os
import re
import shutil
import subprocess
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Dict, Iterable, List, Mapping, Optional, Sequence, Tuple, Union

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import x25519

from ... import config as engine_config

def _build_logger(log_path: Path) -> logging.Logger:
    logger = logging.getLogger("borealis.engine.wireguard")
    if not logger.handlers:
        formatter = logging.Formatter("%(asctime)s-%(name)s-%(levelname)s: %(message)s")
        handler = TimedRotatingFileHandler(str(log_path), when="midnight", backupCount=0, encoding="utf-8")
        handler.setFormatter(formatter)
        logger.addHandler(handler)
    logger.setLevel(logging.INFO)
    return logger


def _encode_key(raw: bytes) -> str:
    return base64.b64encode(raw).decode("ascii").strip()


@dataclass
class WireGuardServerConfig:
    port: int
    engine_virtual_ip: str
    peer_network: str
    private_key_path: Path
    public_key_path: Path
    acl_allowlist_ports: Tuple[int, ...]
    log_path: Path

    def engine_interface(self) -> ipaddress.IPv4Interface:
        return ipaddress.IPv4Interface(self.engine_virtual_ip)

    def peer_subnet(self) -> ipaddress.IPv4Network:
        return ipaddress.IPv4Network(self.peer_network, strict=False)


class WireGuardServerManager:
    """Prepares WireGuard server material (keys/config/ACL plans) for Engine use."""

    def __init__(self, config: WireGuardServerConfig) -> None:
        self.config = config
        self.logger = _build_logger(config.log_path)
        self._is_windows = os.name == "nt"
        self._ensure_cert_dir()
        self.server_private_key, self.server_public_key = self._ensure_server_keys()
        self._service_name = "borealis-wg"
        self._service_display_name = "Borealis - WireGuard - Engine"
        self._config_dir = self._resolve_config_dir()
        self._interface_name = self._resolve_interface_name()
        self._wireguard_exe = self._resolve_wireguard_exe()
        self._wg_quick = shutil.which("wg-quick") or ""
        self._wg = shutil.which("wg") or ""
        self._ip = shutil.which("ip") or ""
        self._iptables = shutil.which("iptables") or ""
        self._firewall_cmd = shutil.which("firewall-cmd") or ""
        self._linux_listener_rule_name = f"Borealis-WG-Listener-{self._interface_name}"
        self._linux_rule_specs: Dict[str, Dict[str, object]] = {}
        self._managed_peers: Dict[str, Dict[str, object]] = {}
        self._firewall_backend = self._detect_firewall_backend()
        self._listener_lock = threading.RLock()
        self._log_startup_context()

    def _resolve_config_dir(self) -> Path:
        config_dir = engine_config.PROJECT_ROOT / "Engine" / "WireGuard"
        try:
            config_dir.mkdir(parents=True, exist_ok=True)
            self._secure_path_permissions(config_dir, mode=0o700, label="WireGuard config dir")
        except Exception:
            self.logger.error("Failed to ensure WireGuard config dir at %s", config_dir, exc_info=True)
        return config_dir

    def _resolve_interface_name(self) -> str:
        raw = str(os.environ.get("BOREALIS_WIREGUARD_INTERFACE") or self._service_name or "borealis-wg").strip().lower()
        cleaned = re.sub(r"[^a-z0-9_.-]", "", raw)
        if not cleaned:
            cleaned = "borealis-wg"
        return cleaned[:15]

    def _legacy_interface_names(self) -> Tuple[str, ...]:
        raw = str(os.environ.get("BOREALIS_WIREGUARD_LEGACY_INTERFACES") or "borealis").strip().lower()
        names: List[str] = []
        for item in raw.split(","):
            cleaned = re.sub(r"[^a-z0-9_.-]", "", str(item or "").strip().lower())
            if not cleaned:
                continue
            cleaned = cleaned[:15]
            if cleaned == self._interface_name:
                continue
            if cleaned not in names:
                names.append(cleaned)
        return tuple(names)

    def _listener_config_path(self) -> Path:
        filename = f"{self._service_name}.conf" if self._is_windows else f"{self._interface_name}.conf"
        return self._config_dir / filename

    def _ensure_config_dir_exists(self) -> None:
        try:
            self._config_dir.mkdir(parents=True, exist_ok=True)
        except Exception:
            self.logger.warning("Failed to create temp dir for WireGuard config", exc_info=True)

    def _resolve_wireguard_exe(self) -> str:
        if not self._is_windows:
            return ""
        candidates = [
            str(Path(os.environ.get("ProgramFiles", "C:\\Program Files")) / "WireGuard" / "wireguard.exe"),
            str(Path(os.environ.get("ProgramFiles(x86)", "C:\\Program Files (x86)")) / "WireGuard" / "wireguard.exe"),
            "wireguard.exe",
        ]
        for candidate in candidates:
            if Path(candidate).is_file():
                return candidate
        return "wireguard.exe"

    def _ensure_cert_dir(self) -> None:
        try:
            self.config.private_key_path.parent.mkdir(parents=True, exist_ok=True)
            self._secure_path_permissions(
                self.config.private_key_path.parent,
                mode=0o700,
                label="WireGuard key directory",
            )
        except Exception:
            self.logger.warning("Failed to ensure VPN server certificate directory exists", exc_info=True)

    def _secure_path_permissions(self, path: Path, *, mode: int, label: str) -> None:
        if self._is_windows:
            return
        try:
            if path.exists():
                path.chmod(mode)
        except Exception:
            self.logger.warning("Failed to secure %s permissions at %s", label, path, exc_info=True)

    def _ensure_server_keys(self) -> Tuple[str, str]:
        priv_path = self.config.private_key_path
        pub_path = self.config.public_key_path

        if priv_path.is_file() and pub_path.is_file():
            try:
                private_key = priv_path.read_text(encoding="utf-8").strip()
                public_key = pub_path.read_text(encoding="utf-8").strip()
                if private_key and public_key:
                    self._secure_path_permissions(priv_path, mode=0o600, label="WireGuard private key")
                    self._secure_path_permissions(pub_path, mode=0o600, label="WireGuard public key")
                    self.logger.info("Loaded existing WireGuard server keys from %s", priv_path.parent)
                    return private_key, public_key
            except Exception:
                self.logger.warning("Failed to read existing WireGuard server keys; regenerating.", exc_info=True)

        private_key_obj = x25519.X25519PrivateKey.generate()
        public_key_obj = private_key_obj.public_key()
        private_key = _encode_key(
            private_key_obj.private_bytes(
                encoding=serialization.Encoding.Raw,
                format=serialization.PrivateFormat.Raw,
                encryption_algorithm=serialization.NoEncryption(),
            )
        )
        public_key = _encode_key(
            public_key_obj.public_bytes(
                encoding=serialization.Encoding.Raw,
                format=serialization.PublicFormat.Raw,
            )
        )

        try:
            priv_path.write_text(private_key, encoding="utf-8")
            pub_path.write_text(public_key, encoding="utf-8")
            self._secure_path_permissions(priv_path, mode=0o600, label="WireGuard private key")
            self._secure_path_permissions(pub_path, mode=0o600, label="WireGuard public key")
            self.logger.info("Generated WireGuard server keypair under %s", priv_path.parent)
        except Exception:
            self.logger.error("Failed to persist WireGuard server keys to disk", exc_info=True)

        return private_key, public_key

    def _run_command(self, args: Sequence[str]) -> Tuple[int, str, str]:
        try:
            proc = subprocess.run(args, capture_output=True, text=True, check=False)
            return proc.returncode, proc.stdout.strip(), proc.stderr.strip()
        except Exception as exc:
            return 1, "", str(exc)

    def _service_id(self) -> str:
        return f"WireGuardTunnel${self._service_name}"

    def _query_service_state(self) -> Optional[str]:
        code, out, err = self._run_command(["sc.exe", "query", self._service_id()])
        if code != 0:
            return None
        text = out or err
        for line in text.splitlines():
            if "STATE" not in line:
                continue
            match = re.search(r"STATE\s*:\s*\d+\s+(\w+)", line)
            if match:
                return match.group(1).upper()
        return None

    def _service_exists(self) -> bool:
        code, _, _ = self._run_command(["sc.exe", "query", self._service_id()])
        return code == 0

    def _stop_service(self, *, timeout: int = 20) -> bool:
        service_id = self._service_id()
        state = self._query_service_state()
        if not state:
            return False
        if state == "STOPPED":
            return True
        self._run_command(["sc.exe", "stop", service_id])
        for _ in range(max(1, timeout)):
            time.sleep(1)
            state = self._query_service_state()
            if state == "STOPPED":
                return True
        return False

    def _ensure_service_display_name(self) -> None:
        if not self._service_display_name:
            return
        args = ["sc.exe", "config", self._service_id(), "DisplayName=", self._service_display_name]
        code, out, err = self._run_command(args)
        if code != 0 and err:
            self.logger.warning("Failed to set WireGuard service display name: %s", err)

    def _ensure_service_running(self, *, timeout: int = 20) -> None:
        service_id = self._service_id()
        for _ in range(max(1, timeout)):
            state = self._query_service_state()
            if state == "RUNNING":
                return
            if state == "STOPPED":
                code, out, err = self._run_command(["sc.exe", "start", service_id])
                if code != 0:
                    self.logger.error("Failed to start WireGuard tunnel service %s err=%s", service_id, err)
                    break
            if state in ("START_PENDING", "STOP_PENDING"):
                time.sleep(1)
                continue
            time.sleep(1)
        state = self._query_service_state()
        if state == "START_PENDING":
            self.logger.warning("WireGuard tunnel service still START_PENDING; attempting restart.")
            self._stop_service(timeout=10)
            self._run_command(["sc.exe", "start", service_id])
            for _ in range(10):
                time.sleep(1)
                if self._query_service_state() == "RUNNING":
                    return
            state = self._query_service_state()
        raise RuntimeError(f"WireGuard tunnel service {service_id} failed to start (state={state})")

    def _service_is_active(self, name: str) -> bool:
        if not name:
            return False
        if shutil.which("systemctl"):
            code, out, err = self._run_command(["systemctl", "is-active", name])
            if code == 0 and (out or "").strip().lower() == "active":
                return True
        if shutil.which("service"):
            code, out, err = self._run_command(["service", name, "status"])
            if code == 0:
                return True
        return False

    def _is_firewalld_running(self) -> bool:
        if self._firewall_cmd:
            code, out, err = self._run_command([self._firewall_cmd, "--state"])
            if code == 0 and (out or "").strip().lower() == "running":
                return True
        return self._service_is_active("firewalld")

    def _is_iptables_service_running(self) -> bool:
        return self._service_is_active("iptables") or self._service_is_active("nftables")

    def _detect_firewall_backend(self) -> str:
        if self._is_windows:
            return "windows"
        if self._is_firewalld_running() and self._firewall_cmd:
            return "firewalld"
        if self._iptables:
            return "iptables"
        if self._is_iptables_service_running() and not self._iptables:
            self.logger.warning("iptables/nftables service appears active but 'iptables' binary was not found.")
        if self._firewall_cmd and not self._is_firewalld_running():
            self.logger.warning("firewall-cmd found but firewalld is not active; falling back to iptables if available.")
        return "none"

    def _log_startup_context(self) -> None:
        if self._is_windows:
            service_state = self._query_service_state() or "missing"
            wireguard_exe = self._wireguard_exe or "missing"
            self.logger.info(
                "vpn_startup_context platform=windows firewall_backend=netsh service_id=%s service_state=%s wireguard_exe=%s",
                self._service_id(),
                service_state,
                wireguard_exe,
            )
            return

        firewalld_active = self._is_firewalld_running()
        iptables_service_active = self._is_iptables_service_running()
        self.logger.info(
            "vpn_startup_context platform=linux firewall_backend=%s firewalld_active=%s iptables_service_active=%s firewall_cmd=%s iptables_bin=%s wg_quick=%s wg=%s ip=%s interface=%s",
            self._firewall_backend,
            "true" if firewalld_active else "false",
            "true" if iptables_service_active else "false",
            "present" if self._firewall_cmd else "missing",
            "present" if self._iptables else "missing",
            "present" if self._wg_quick else "missing",
            "present" if self._wg else "missing",
            "present" if self._ip else "missing",
            self._interface_name,
        )

    def _linux_interface_exists(self, name: Optional[str] = None) -> bool:
        interface_name = str(name or self._interface_name).strip()
        if not interface_name:
            return False
        if self._wg:
            code, out, err = self._run_command([self._wg, "show", interface_name])
            if code == 0:
                return True
        if self._ip:
            code, out, err = self._run_command([self._ip, "link", "show", "dev", interface_name])
            return code == 0
        return False

    def _linux_list_wireguard_interfaces(self) -> List[str]:
        if not self._ip:
            return []
        code, out, err = self._run_command([self._ip, "-d", "link", "show", "type", "wireguard"])
        if code != 0:
            return []
        names: List[str] = []
        for line in str(out or "").splitlines():
            match = re.match(r"^\d+:\s+([^:@]+)(?:@[^:]+)?:", str(line or "").strip())
            if not match:
                continue
            name = str(match.group(1) or "").strip()
            if name and name not in names:
                names.append(name)
        return names

    def _is_stale_managed_interface(self, name: str) -> bool:
        normalized = str(name or "").strip().lower()
        if not normalized or normalized == self._interface_name:
            return False
        if normalized in self._legacy_interface_names():
            return True
        return normalized.startswith("borealis")

    def _linux_bring_down(self, config_path: Path) -> None:
        if not self._wg_quick:
            return
        code, out, err = self._run_command([self._wg_quick, "down", str(config_path)])
        if code != 0 and self._linux_interface_exists():
            detail = err or out or "unknown error"
            self.logger.warning("Failed to bring down WireGuard listener interface %s: %s", self._interface_name, detail)

    def _linux_delete_interface(self, name: Optional[str] = None) -> None:
        interface_name = str(name or self._interface_name).strip()
        if not interface_name or not self._ip or not self._linux_interface_exists(interface_name):
            return
        code, out, err = self._run_command([self._ip, "link", "delete", "dev", interface_name])
        if code != 0 and self._linux_interface_exists(interface_name):
            detail = err or out or "unknown error"
            raise RuntimeError(f"WireGuard Linux interface cleanup failed: {detail}")
        self.logger.warning("Removed stale WireGuard Linux interface %s before retry.", interface_name)

    def _linux_reset_interface(self, config_path: Path) -> None:
        self._linux_bring_down(config_path)
        if self._linux_interface_exists():
            self._linux_delete_interface()

    def cleanup_stale_runtime(self) -> List[str]:
        if self._is_windows:
            return []
        removed: List[str] = []
        for interface_name in self._linux_list_wireguard_interfaces():
            if not self._is_stale_managed_interface(interface_name):
                continue
            try:
                self._linux_delete_interface(interface_name)
            except Exception:
                self.logger.warning(
                    "Failed to remove stale WireGuard interface %s during startup cleanup.",
                    interface_name,
                    exc_info=True,
                )
                continue
            removed.append(interface_name)
        return removed

    def _linux_apply_interface_runtime(self) -> None:
        if not self._wg:
            raise RuntimeError("WireGuard tools missing on Linux Engine: 'wg' not found")
        if not self._ip:
            raise RuntimeError("WireGuard tools missing on Linux Engine: 'ip' not found")

        code, out, err = self._run_command(
            [
                self._wg,
                "set",
                self._interface_name,
                "listen-port",
                str(int(self.config.port)),
                "private-key",
                str(self.config.private_key_path),
            ]
        )
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux interface configuration failed: {detail}")

        code, out, err = self._run_command(
            [
                self._ip,
                "address",
                "replace",
                str(self.config.engine_interface()),
                "dev",
                self._interface_name,
            ]
        )
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux address configuration failed: {detail}")

        code, out, err = self._run_command([self._ip, "link", "set", "up", "dev", self._interface_name])
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux interface activation failed: {detail}")

    def _linux_bring_up(self, config_path: Path) -> None:
        if not self._wg_quick:
            raise RuntimeError("WireGuard tools missing on Linux Engine: 'wg-quick' not found")
        code, out, err = self._run_command([self._wg_quick, "up", str(config_path)])
        if code == 0:
            return
        detail = (err or out or "unknown error").strip()
        self.logger.warning("WireGuard Linux up failed for %s: %s", self._interface_name, detail)
        self._linux_reset_interface(config_path)
        code, out, err = self._run_command([self._wg_quick, "up", str(config_path)])
        if code == 0:
            return
        detail = (err or out or "unknown error").strip()
        raise RuntimeError(f"WireGuard Linux listener failed to start: {detail}")

    def _normalise_peer_spec(self, peer: Mapping[str, object]) -> Dict[str, object]:
        agent_id = str(peer.get("agent_id") or "").strip()
        public_key = str(peer.get("public_key") or "").strip()
        allowed_ips = [str(item).strip() for item in (peer.get("allowed_ips") or []) if str(item).strip()]
        if not agent_id:
            raise ValueError("WireGuard peer is missing agent_id")
        if not public_key:
            raise ValueError(f"WireGuard peer {agent_id} is missing public_key")
        if not allowed_ips:
            raise ValueError(f"WireGuard peer {agent_id} is missing allowed_ips")
        normalized = dict(peer)
        normalized["agent_id"] = agent_id
        normalized["public_key"] = public_key
        normalized["allowed_ips"] = tuple(allowed_ips)
        return normalized

    def _normalise_peer_specs(self, peers: Sequence[Mapping[str, object]]) -> List[Dict[str, object]]:
        normalized_peers: List[Dict[str, object]] = []
        allowed_ip_owners: Dict[str, str] = {}
        for peer in peers:
            normalized = self._normalise_peer_spec(peer)
            agent_id = str(normalized["agent_id"])
            for allowed_ip in normalized["allowed_ips"]:
                existing_owner = allowed_ip_owners.get(str(allowed_ip))
                if existing_owner and existing_owner != agent_id:
                    raise ValueError(
                        "WireGuard allowed IP {0} is already assigned to agent {1}; "
                        "cannot also assign it to agent {2}".format(
                            allowed_ip,
                            existing_owner,
                            agent_id,
                        )
                    )
                allowed_ip_owners[str(allowed_ip)] = agent_id
            normalized_peers.append(normalized)
        return normalized_peers

    def _allowed_ips_text(self, peer: Mapping[str, object]) -> str:
        return ",".join(str(item).strip() for item in (peer.get("allowed_ips") or []) if str(item).strip())

    def _linux_list_current_peers(self) -> List[str]:
        if not self._wg or not self._linux_interface_exists():
            return []
        code, out, err = self._run_command([self._wg, "show", self._interface_name, "peers"])
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux peer enumeration failed: {detail}")
        return [line.strip() for line in str(out or "").splitlines() if line.strip()]

    def _linux_latest_handshakes(self) -> Dict[str, int]:
        if not self._wg or not self._linux_interface_exists():
            return {}
        code, out, err = self._run_command([self._wg, "show", self._interface_name, "latest-handshakes"])
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux latest-handshakes query failed: {detail}")
        handshakes: Dict[str, int] = {}
        for line in str(out or "").splitlines():
            parts = [part.strip() for part in line.split("\t")]
            if len(parts) != 2:
                continue
            public_key = parts[0]
            if not public_key:
                continue
            try:
                handshakes[public_key] = int(parts[1])
            except Exception:
                continue
        return handshakes

    def _ts_to_iso(self, ts: Optional[float]) -> str:
        if ts in (None, "", 0):
            return ""
        try:
            return datetime.fromtimestamp(float(ts), timezone.utc).isoformat()
        except Exception:
            return ""

    def _linux_remove_peer_by_public_key(self, public_key: str) -> None:
        if not public_key or not self._wg or not self._linux_interface_exists():
            return
        code, out, err = self._run_command([self._wg, "set", self._interface_name, "peer", public_key, "remove"])
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux peer removal failed: {detail}")

    def _linux_upsert_peer(self, peer: Mapping[str, object]) -> None:
        normalized = self._normalise_peer_spec(peer)
        previous = self._managed_peers.get(str(normalized["agent_id"]))
        previous_public_key = str(previous.get("public_key") or "").strip() if previous else ""
        public_key = str(normalized["public_key"])
        if previous_public_key and previous_public_key != public_key:
            self._linux_remove_peer_by_public_key(previous_public_key)

        allowed_ips = self._allowed_ips_text(normalized)
        if not allowed_ips:
            raise ValueError(f"WireGuard peer {normalized['agent_id']} is missing allowed_ips")
        code, out, err = self._run_command(
            [self._wg, "set", self._interface_name, "peer", public_key, "allowed-ips", allowed_ips]
        )
        if code != 0:
            detail = (err or out or "unknown error").strip()
            raise RuntimeError(f"WireGuard Linux peer upsert failed: {detail}")
        self._managed_peers[str(normalized["agent_id"])] = dict(normalized)

    def check_listener_health(self) -> Dict[str, Optional[Union[str, bool]]]:
        """Return the current listener health without mutating listener state."""

        if self._is_windows:
            service_exists = self._service_exists()
            service_state = self._query_service_state()
            if not service_exists:
                return {
                    "healthy": False,
                    "reason": "service_missing",
                    "service_state": service_state,
                    "peer_count": len(self._managed_peers),
                }
            if service_state in ("RUNNING", "START_PENDING"):
                return {
                    "healthy": True,
                    "reason": "service_running",
                    "service_state": service_state,
                    "peer_count": len(self._managed_peers),
                }
            return {
                "healthy": False,
                "reason": "service_unhealthy",
                "service_state": service_state,
                "peer_count": len(self._managed_peers),
            }

        if not self._linux_interface_exists():
            return {
                "healthy": False,
                "reason": "interface_down",
                "service_state": None,
                "peer_count": 0,
            }
        stale_interfaces = [name for name in self._linux_list_wireguard_interfaces() if self._is_stale_managed_interface(name)]
        if stale_interfaces:
            return {
                "healthy": False,
                "reason": "stale_interface_present",
                "service_state": "RUNNING",
                "peer_count": 0,
                "stale_interfaces": ",".join(stale_interfaces),
            }
        if not self._wg:
            return {
                "healthy": False,
                "reason": "wg_unavailable",
                "service_state": None,
                "peer_count": 0,
            }
        code, _out, _err = self._run_command([self._wg, "show", self._interface_name])
        if code != 0:
            return {
                "healthy": False,
                "reason": "wg_show_failed",
                "service_state": None,
                "peer_count": 0,
            }
        code, peers_out, _err = self._run_command([self._wg, "show", self._interface_name, "peers"])
        if code != 0:
            return {
                "healthy": False,
                "reason": "wg_peers_failed",
                "service_state": None,
                "peer_count": 0,
            }
        peers = [line.strip() for line in str(peers_out or "").splitlines() if line.strip()]
        if not peers:
            return {
                "healthy": False,
                "reason": "no_peers_configured",
                "service_state": None,
                "peer_count": 0,
            }
        return {
            "healthy": True,
            "reason": "listener_running",
            "service_state": "RUNNING",
            "peer_count": len(peers),
        }

    def check_peer_health(self, public_key: str) -> Dict[str, Optional[Union[str, bool, int, float]]]:
        """Return live transport health for a specific managed peer."""

        normalized_key = str(public_key or "").strip()
        if not normalized_key:
            return {
                "healthy": False,
                "reason": "peer_missing",
                "service_state": None,
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        if self._is_windows:
            service_exists = self._service_exists()
            service_state = self._query_service_state()
            peer_present = any(
                str(peer.get("public_key") or "").strip() == normalized_key
                for peer in self._managed_peers.values()
            )
            if not service_exists:
                return {
                    "healthy": False,
                    "reason": "service_missing",
                    "service_state": service_state,
                    "peer_present": peer_present,
                    "last_handshake_at": None,
                    "last_handshake_at_iso": "",
                    "handshake_age_seconds": None,
                }
            if service_state not in ("RUNNING", "START_PENDING"):
                return {
                    "healthy": False,
                    "reason": "service_unhealthy",
                    "service_state": service_state,
                    "peer_present": peer_present,
                    "last_handshake_at": None,
                    "last_handshake_at_iso": "",
                    "handshake_age_seconds": None,
                }
            if not peer_present:
                return {
                    "healthy": False,
                    "reason": "peer_missing",
                    "service_state": service_state,
                    "peer_present": False,
                    "last_handshake_at": None,
                    "last_handshake_at_iso": "",
                    "handshake_age_seconds": None,
                }
            return {
                "healthy": True,
                "reason": "peer_present",
                "service_state": service_state,
                "peer_present": True,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        if not self._linux_interface_exists():
            return {
                "healthy": False,
                "reason": "interface_down",
                "service_state": None,
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }
        if not self._wg:
            return {
                "healthy": False,
                "reason": "wg_unavailable",
                "service_state": None,
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        code, _out, _err = self._run_command([self._wg, "show", self._interface_name])
        if code != 0:
            return {
                "healthy": False,
                "reason": "wg_show_failed",
                "service_state": None,
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        try:
            peers = set(self._linux_list_current_peers())
        except Exception:
            return {
                "healthy": False,
                "reason": "wg_peers_failed",
                "service_state": None,
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }
        if normalized_key not in peers:
            return {
                "healthy": False,
                "reason": "peer_missing",
                "service_state": "RUNNING",
                "peer_present": False,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        try:
            handshakes = self._linux_latest_handshakes()
        except Exception:
            return {
                "healthy": False,
                "reason": "wg_latest_handshakes_failed",
                "service_state": "RUNNING",
                "peer_present": True,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        last_handshake_at = handshakes.get(normalized_key)
        if not last_handshake_at:
            return {
                "healthy": False,
                "reason": "no_handshake",
                "service_state": "RUNNING",
                "peer_present": True,
                "last_handshake_at": None,
                "last_handshake_at_iso": "",
                "handshake_age_seconds": None,
            }

        handshake_age_seconds = max(0, int(time.time() - int(last_handshake_at)))
        return {
            "healthy": True,
            "reason": "peer_ready",
            "service_state": "RUNNING",
            "peer_present": True,
            "last_handshake_at": int(last_handshake_at),
            "last_handshake_at_iso": self._ts_to_iso(float(last_handshake_at)),
            "handshake_age_seconds": handshake_age_seconds,
        }

    def _linux_firewall_ensure_rule(self, chain: str, params: Sequence[str], *, label: str) -> bool:
        if not self._iptables:
            return False
        check_args = [self._iptables, "-w", "-C", chain, *params]
        check_code, _, _ = self._run_command(check_args)
        if check_code == 0:
            return True
        add_args = [self._iptables, "-w", "-I", chain, "1", *params]
        add_code, out, err = self._run_command(add_args)
        if add_code != 0:
            self.logger.warning("Failed to apply Linux firewall rule %s code=%s err=%s", label, add_code, err or out)
            return False
        self.logger.info("Applied Linux firewall rule %s", label)
        return True

    def _linux_firewall_remove_rule(self, chain: str, params: Sequence[str], *, label: str) -> None:
        if not self._iptables:
            return
        while True:
            check_args = [self._iptables, "-w", "-C", chain, *params]
            check_code, _, _ = self._run_command(check_args)
            if check_code != 0:
                break
            del_args = [self._iptables, "-w", "-D", chain, *params]
            del_code, out, err = self._run_command(del_args)
            if del_code != 0:
                self.logger.warning("Failed to remove Linux firewall rule %s code=%s err=%s", label, del_code, err or out)
                break
        self.logger.info("Removed Linux firewall rule %s", label)

    def _firewalld_ensure_direct_rule(
        self,
        table: str,
        chain: str,
        priority: int,
        params: Sequence[str],
        *,
        label: str,
    ) -> bool:
        if not self._firewall_cmd:
            return False
        query_args = [
            self._firewall_cmd,
            "--direct",
            "--query-rule",
            "ipv4",
            table,
            chain,
            str(int(priority)),
            *params,
        ]
        check_code, _, _ = self._run_command(query_args)
        if check_code == 0:
            return True
        add_args = [
            self._firewall_cmd,
            "--direct",
            "--add-rule",
            "ipv4",
            table,
            chain,
            str(int(priority)),
            *params,
        ]
        add_code, out, err = self._run_command(add_args)
        if add_code != 0:
            self.logger.warning("Failed to apply firewalld direct rule %s code=%s err=%s", label, add_code, err or out)
            return False
        self.logger.info("Applied firewalld direct rule %s", label)
        return True

    def _firewalld_remove_direct_rule(
        self,
        table: str,
        chain: str,
        priority: int,
        params: Sequence[str],
        *,
        label: str,
    ) -> None:
        if not self._firewall_cmd:
            return
        while True:
            query_args = [
                self._firewall_cmd,
                "--direct",
                "--query-rule",
                "ipv4",
                table,
                chain,
                str(int(priority)),
                *params,
            ]
            check_code, _, _ = self._run_command(query_args)
            if check_code != 0:
                break
            remove_args = [
                self._firewall_cmd,
                "--direct",
                "--remove-rule",
                "ipv4",
                table,
                chain,
                str(int(priority)),
                *params,
            ]
            remove_code, out, err = self._run_command(remove_args)
            if remove_code != 0:
                self.logger.warning(
                    "Failed to remove firewalld direct rule %s code=%s err=%s",
                    label,
                    remove_code,
                    err or out,
                )
                break
        self.logger.info("Removed firewalld direct rule %s", label)

    def _ensure_linux_listener_rule(self) -> None:
        params = (
            "-p",
            "udp",
            "--dport",
            str(int(self.config.port)),
            "-m",
            "comment",
            "--comment",
            self._linux_listener_rule_name,
            "-j",
            "ACCEPT",
        )
        if self._firewall_backend == "firewalld":
            self._firewalld_ensure_direct_rule("filter", "INPUT", 0, params, label=self._linux_listener_rule_name)
            return
        if self._firewall_backend == "iptables":
            self._linux_firewall_ensure_rule("INPUT", params, label=self._linux_listener_rule_name)
            return
        self.logger.warning("No supported Linux firewall backend is available for listener rule management.")

    def _remove_linux_listener_rule(self) -> None:
        params = (
            "-p",
            "udp",
            "--dport",
            str(int(self.config.port)),
            "-m",
            "comment",
            "--comment",
            self._linux_listener_rule_name,
            "-j",
            "ACCEPT",
        )
        if self._firewall_backend == "firewalld":
            self._firewalld_remove_direct_rule("filter", "INPUT", 0, params, label=self._linux_listener_rule_name)
            return
        if self._firewall_backend == "iptables":
            self._linux_firewall_remove_rule("INPUT", params, label=self._linux_listener_rule_name)
            return

    def _normalise_allowed_ports(
        self,
        candidate: Optional[Iterable[int]],
        overrides: Optional[Iterable[int]] = None,
    ) -> Tuple[int, ...]:
        def _to_ports(value: Optional[Iterable[int]]) -> List[int]:
            if value is None:
                return []
            if isinstance(value, str):
                items = [part.strip() for part in value.split(",") if part.strip()]
            else:
                items = list(value)
            ports: List[int] = []
            for item in items:
                try:
                    port = int(item)
                except Exception:
                    continue
                if 1 <= port <= 65535:
                    ports.append(port)
            return ports

        source: Optional[Iterable[int]]
        if overrides is not None:
            source = overrides
        elif candidate is not None:
            source = candidate
        else:
            source = self.config.acl_allowlist_ports

        ports = _to_ports(source)
        if not ports and source is not self.config.acl_allowlist_ports:
            ports = _to_ports(self.config.acl_allowlist_ports)

        return tuple(dict.fromkeys(ports))

    def require_orchestration_token(self, token: Optional[Mapping[str, object]]) -> Mapping[str, object]:
        """Validate orchestration token shape and expiry (best-effort)."""

        if not token:
            raise ValueError("Missing orchestration token for WireGuard peer")

        required_fields = ("agent_id", "tunnel_id", "expires_at", "port")
        missing = [field for field in required_fields if field not in token or token[field] in (None, "")]
        if missing:
            raise ValueError(f"Invalid orchestration token; missing {', '.join(missing)}")

        try:
            expires_at = float(token["expires_at"])
        except Exception:
            raise ValueError("Invalid orchestration token expiry")

        try:
            port = int(token["port"])
        except Exception:
            raise ValueError("Invalid orchestration token port")
        if port != int(self.config.port):
            raise ValueError("Orchestration token port mismatch")

        now = time.time()
        if expires_at <= now:
            raise ValueError("Orchestration token expired")

        return dict(token)

    def build_peer_profile(
        self,
        agent_id: str,
        virtual_ip: str,
        allowed_ports: Optional[Iterable[int]] = None,
        override_ports: Optional[Iterable[int]] = None,
    ) -> Mapping[str, object]:
        """Construct a host-only peer profile (no client-to-client)."""

        network = self.config.peer_subnet()
        iface = self.config.engine_interface()
        ip = ipaddress.ip_interface(virtual_ip)
        if ip.network.prefixlen != 32:
            raise ValueError("Agent virtual IP must be /32")
        if ip.ip not in network:
            raise ValueError("Agent virtual IP must reside within peer network")

        allowed = self._normalise_allowed_ports(allowed_ports, overrides=override_ports)

        profile = {
            "agent_id": agent_id,
            "virtual_ip": str(ip),
            "allowed_ips": [str(ip)],
            "endpoint": f"{iface.ip}:{self.config.port}",
            "client_to_client": False,
            "engine_virtual_ip": str(iface.ip),
            "engine_interface": str(iface),
            "allowed_ports": allowed,
        }
        allowed_text = ",".join(str(p) for p in allowed) if allowed else "all"
        self.logger.info(
            "Prepared WireGuard peer profile for agent=%s ip=%s allowed_ports=%s",
            agent_id,
            ip,
            allowed_text,
        )
        return profile

    def render_server_config(
        self,
        peers: Sequence[Mapping[str, object]],
    ) -> str:
        """Render a host-only WireGuard server config (without applying it)."""

        lines = self.render_listener_base_config().splitlines()
        for peer in peers:
            allowed_ips = peer.get("allowed_ips") or []
            allowed_ip_text = ", ".join(str(item) for item in allowed_ips)
            pre_shared_key = peer.get("preshared_key")
            peer_public_key = peer.get("public_key")
            lines.extend(
                [
                    "[Peer]",
                    f"# agent_id={peer.get('agent_id', '')}",
                    f"AllowedIPs = {allowed_ip_text}",
                ]
            )
            if peer_public_key:
                lines.append(f"PublicKey = {peer_public_key}")
            if pre_shared_key:
                lines.append(f"PresharedKey = {pre_shared_key}")
            lines.append("")

        return "\n".join(lines)

    def render_listener_base_config(self) -> str:
        """Render the interface-only WireGuard config used to keep the listener online."""

        iface = self.config.engine_interface()
        return "\n".join(
            [
                "[Interface]",
                f"PrivateKey = {self.server_private_key}",
                f"ListenPort = {self.config.port}",
                f"Address = {iface}",
                "",
            ]
        )

    def _write_listener_config(self, text: str) -> Path:
        self._ensure_config_dir_exists()
        config_path = self._listener_config_path()
        config_path.write_text(text, encoding="utf-8")
        self._secure_path_permissions(config_path, mode=0o600, label="WireGuard listener config")
        self.logger.info("Rendered WireGuard config to %s", config_path)
        return config_path

    def _apply_windows_listener(self, config_path: Path, *, restart_existing: bool) -> None:
        if self._service_exists():
            if restart_existing:
                if not self._stop_service(timeout=20):
                    self.logger.warning("WireGuard tunnel service did not stop cleanly before restart.")
            self._ensure_service_display_name()
            self._ensure_service_running(timeout=25)
            return

        args = [self._wireguard_exe, "/installtunnelservice", str(config_path)]
        code, out, err = self._run_command(args)
        if code != 0:
            self.logger.error("Failed to install WireGuard tunnel service code=%s err=%s", code, err)
            raise RuntimeError(f"WireGuard installtunnelservice failed: {err}")
        self.logger.info("WireGuard listener installed (service=%s)", config_path.stem)
        self._ensure_service_display_name()
        self._ensure_service_running(timeout=25)

    def ensure_listener(self) -> None:
        with self._listener_lock:
            config_path = self._write_listener_config(self.render_listener_base_config())

            if not self._is_windows:
                stale_interfaces = self.cleanup_stale_runtime()
                if stale_interfaces:
                    self.logger.warning(
                        "Removed stale WireGuard interfaces before ensuring listener: %s",
                        ",".join(stale_interfaces),
                    )
                if not self._linux_interface_exists():
                    self._linux_bring_up(config_path)
                    self._managed_peers.clear()
                else:
                    self._linux_apply_interface_runtime()
                self._ensure_linux_listener_rule()
                self.logger.info("WireGuard listener ready on Linux interface %s", self._interface_name)
                return

            if self._service_exists():
                self._apply_windows_listener(config_path, restart_existing=False)
                return

            self._apply_windows_listener(config_path, restart_existing=False)

    def upsert_peer(self, peer: Mapping[str, object]) -> None:
        with self._listener_lock:
            normalized = self._normalise_peer_spec(peer)
            self._normalise_peer_specs(
                [
                    existing_peer
                    for existing_agent_id, existing_peer in self._managed_peers.items()
                    if str(existing_agent_id) != str(normalized["agent_id"])
                ]
                + [normalized]
            )
            if not self._is_windows:
                self.ensure_listener()
                self._linux_upsert_peer(normalized)
                return

            self._managed_peers[str(normalized["agent_id"])] = dict(normalized)
            self.start_listener(list(self._managed_peers.values()))

    def remove_peer(self, agent_id: str, *, public_key: str = "") -> None:
        with self._listener_lock:
            agent_key = str(agent_id or "").strip()
            managed = self._managed_peers.get(agent_key) or {}
            peer_public_key = str(public_key or managed.get("public_key") or "").strip()

            if not self._is_windows:
                if self._linux_interface_exists() and peer_public_key:
                    self._linux_remove_peer_by_public_key(peer_public_key)
                self._managed_peers.pop(agent_key, None)
                return

            if agent_key:
                self._managed_peers.pop(agent_key, None)
            remaining = list(self._managed_peers.values())
            if remaining:
                self.start_listener(remaining)
            else:
                self.stop_listener(ignore_missing=True)

    def reconcile_peers(self, peers: Sequence[Mapping[str, object]]) -> None:
        with self._listener_lock:
            normalized_peers = self._normalise_peer_specs(peers)
            desired: Dict[str, Dict[str, object]] = {}
            for normalized in normalized_peers:
                desired[str(normalized["agent_id"])] = normalized

            if not self._is_windows:
                self.ensure_listener()
                current_public_keys = set(self._linux_list_current_peers())
                desired_public_keys = {
                    str(peer.get("public_key") or "").strip()
                    for peer in desired.values()
                    if str(peer.get("public_key") or "").strip()
                }
                for stale_public_key in sorted(current_public_keys - desired_public_keys):
                    self._linux_remove_peer_by_public_key(stale_public_key)
                self._managed_peers = {}
                for peer in desired.values():
                    self._linux_upsert_peer(peer)
                self._managed_peers = {agent_id: dict(peer) for agent_id, peer in desired.items()}
                return

            self._managed_peers = {agent_id: dict(peer) for agent_id, peer in desired.items()}
            if self._managed_peers:
                self.start_listener(list(self._managed_peers.values()))
            else:
                self.ensure_listener()

    def describe_acl_defaults(self) -> Mapping[str, object]:
        return {
            "platform": "windows" if self._is_windows else "linux",
            "firewall_backend": self._firewall_backend if not self._is_windows else "netsh",
            "allowlist_ports": list(self._normalise_allowed_ports(self.config.acl_allowlist_ports)),
            "client_to_client": False,
            "host_only": True,
        }

    def apply_firewall_rules(self, peer: Mapping[str, object]) -> List[str]:
        """Apply outbound firewall allow rules for the agent's virtual IP/ports."""

        rules = self.build_firewall_rules(peer)
        rule_names: List[str] = []
        for idx, rule in enumerate(rules):
            name = f"Borealis-WG-Agent-{peer.get('agent_id','')}-{idx}"
            protocol = str(rule.get("protocol") or "TCP").upper()
            local_port = rule.get("local_port")
            remote_port = rule.get("remote_port")
            if self._is_windows:
                self._run_command(["netsh", "advfirewall", "firewall", "delete", "rule", f"name={name}"])
                args = [
                    "netsh",
                    "advfirewall",
                    "firewall",
                    "add",
                    "rule",
                    f"name={name}",
                    "dir=out",
                    "action=allow",
                    f"remoteip={rule.get('remote_address','')}",
                    f"protocol={protocol}",
                ]
                if remote_port:
                    args.append(f"remoteport={remote_port}")
                elif local_port:
                    args.append(f"localport={local_port}")
                code, out, err = self._run_command(args)
                if code != 0:
                    self.logger.warning("Failed to apply firewall rule %s code=%s err=%s", name, code, err)
                    continue
                self.logger.info("Applied firewall rule %s", name)
                rule_names.append(name)
                continue

            remote_ip = str(rule.get("remote_address") or "").strip()
            remote_ports = str(remote_port or local_port or "").strip()
            if not remote_ip or not remote_ports:
                continue
            params = (
                "-d",
                remote_ip,
                "-p",
                protocol.lower(),
                "-m",
                "multiport",
                "--dports",
                remote_ports,
                "-m",
                "comment",
                "--comment",
                name,
                "-j",
                "ACCEPT",
            )
            if self._firewall_backend == "firewalld":
                if self._firewalld_ensure_direct_rule("filter", "OUTPUT", 0, params, label=name):
                    self._linux_rule_specs[name] = {
                        "backend": "firewalld",
                        "table": "filter",
                        "chain": "OUTPUT",
                        "priority": 0,
                        "params": params,
                    }
                    rule_names.append(name)
                continue
            if self._firewall_backend == "iptables":
                if self._linux_firewall_ensure_rule("OUTPUT", params, label=name):
                    self._linux_rule_specs[name] = {
                        "backend": "iptables",
                        "chain": "OUTPUT",
                        "params": params,
                    }
                    rule_names.append(name)
                continue
            self.logger.warning("No supported Linux firewall backend; skipping firewall rule %s", name)
        return rule_names

    def remove_firewall_rules(self, rule_names: Sequence[str]) -> None:
        for name in rule_names:
            if not name:
                continue
            if self._is_windows:
                args = ["netsh", "advfirewall", "firewall", "delete", "rule", f"name={name}"]
                code, out, err = self._run_command(args)
                if code != 0:
                    self.logger.warning("Failed to remove firewall rule %s code=%s err=%s", name, code, err)
                else:
                    self.logger.info("Removed firewall rule %s", name)
                continue

            spec = self._linux_rule_specs.pop(name, None)
            if not spec:
                self.logger.debug("Linux firewall rule metadata missing for %s; skipping removal.", name)
                continue
            backend = str(spec.get("backend") or "").strip().lower()
            params = tuple(spec.get("params") or ())
            if backend == "firewalld":
                table = str(spec.get("table") or "filter")
                chain = str(spec.get("chain") or "OUTPUT")
                priority = int(spec.get("priority") or 0)
                self._firewalld_remove_direct_rule(table, chain, priority, params, label=name)
                continue
            if backend == "iptables":
                chain = str(spec.get("chain") or "OUTPUT")
                self._linux_firewall_remove_rule(chain, params, label=name)
                continue
            self.logger.debug("Unknown Linux firewall backend metadata for %s; skipping removal.", name)

    def start_listener(self, peers: Sequence[Mapping[str, object]]) -> None:
        """Render a temporary WireGuard config and start the service."""

        with self._listener_lock:
            normalized_peers = self._normalise_peer_specs(peers)
            if not self._is_windows:
                self.reconcile_peers(normalized_peers)
                return

            config_path = self._write_listener_config(self.render_server_config(normalized_peers))
            self._managed_peers = {
                str(peer["agent_id"]): dict(peer)
                for peer in normalized_peers
            }
            self._apply_windows_listener(config_path, restart_existing=True)

    def managed_peers_snapshot(self) -> Dict[str, Dict[str, object]]:
        with self._listener_lock:
            return {
                str(agent_id): dict(peer)
                for agent_id, peer in self._managed_peers.items()
                if str(agent_id).strip()
            }

    def stop_listener(self, *, ignore_missing: bool = False) -> None:
        """Stop the WireGuard tunnel service (leave installed for reuse)."""

        with self._listener_lock:
            self._managed_peers = {}
            if not self._is_windows:
                config_path = self._listener_config_path()
                interface_exists = self._linux_interface_exists()
                if not self._wg_quick:
                    if ignore_missing:
                        self.logger.info("WireGuard tools not available; Linux listener already absent")
                    else:
                        self.logger.warning("WireGuard tools not available; cannot stop Linux listener.")
                    self._remove_linux_listener_rule()
                    return
                code, out, err = self._run_command([self._wg_quick, "down", str(config_path)])
                if code != 0 and interface_exists and not ignore_missing:
                    self.logger.warning("WireGuard Linux listener did not stop cleanly: %s", err or out)
                else:
                    self.logger.info("WireGuard Linux listener stopped")
                self._remove_linux_listener_rule()
                return

            if not self._service_exists():
                if ignore_missing:
                    self.logger.info("WireGuard tunnel service already absent")
                    return
                self.logger.warning("WireGuard tunnel service not found during stop.")
                return

            if not self._stop_service(timeout=20):
                self.logger.warning("WireGuard tunnel service did not stop cleanly.")
                return
            self.logger.info("WireGuard tunnel service stopped")

    def build_firewall_rules(
        self,
        peer: Mapping[str, object],
    ) -> List[Mapping[str, Union[str, int]]]:
        """Compute firewall allow rules for engine->agent (host-only)."""

        rules: List[Mapping[str, Union[str, int]]] = []
        ip = str(peer.get("virtual_ip", "")).split("/")[0]
        allowed_ports = self._normalise_allowed_ports(peer.get("allowed_ports"))
        if not allowed_ports:
            self.logger.warning(
                "No allowed ports configured for agent=%s; firewall rules skipped.",
                peer.get("agent_id", ""),
            )
            return rules
        port_text = ",".join(str(p) for p in allowed_ports)
        for protocol in ("TCP", "UDP"):
            rules.append(
                {
                    "direction": "outbound",
                    "remote_address": ip,
                    "remote_port": port_text,
                    "protocol": protocol,
                    "action": "allow",
                    "description": f"WireGuard engine->agent allow {port_text}/{protocol}",
                }
            )

        self.logger.info(
            "Prepared firewall rule plan for agent=%s rules=%s",
            peer.get("agent_id", ""),
            port_text,
        )
        return rules
