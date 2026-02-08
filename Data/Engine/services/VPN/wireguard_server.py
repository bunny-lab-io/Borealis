# ======================================================
# Data\Engine\services\VPN\wireguard_server.py
# Description: WireGuard server configuration scaffold (UDP/30000, host-only peers, ACL defaults).
#
# API Endpoints (if applicable): None
# ======================================================

"""WireGuard server scaffolding for the Engine runtime.

This module prepares WireGuard server material (keys, config rendering, ACL
defaults) without starting a live tunnel. It is designed for the Windows-first
reverse VPN migration where the Engine will run a host-only WireGuard listener
on UDP/30000 and issue per-agent /32 peers with restricted AllowedIPs.
"""

from __future__ import annotations

import base64
import ipaddress
import logging
import os
import re
import subprocess
import time
from dataclasses import dataclass
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
        self._ensure_cert_dir()
        self.server_private_key, self.server_public_key = self._ensure_server_keys()
        self._service_name = "borealis-wg"
        self._service_display_name = "Borealis - WireGuard - Engine"
        self._config_dir = self._resolve_config_dir()
        self._wireguard_exe = self._resolve_wireguard_exe()

    def _resolve_config_dir(self) -> Path:
        config_dir = engine_config.PROJECT_ROOT / "Engine" / "WireGuard"
        try:
            config_dir.mkdir(parents=True, exist_ok=True)
        except Exception:
            self.logger.error("Failed to ensure WireGuard config dir at %s", config_dir, exc_info=True)
        return config_dir

    def _resolve_wireguard_exe(self) -> str:
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
        except Exception:
            self.logger.warning("Failed to ensure VPN server certificate directory exists", exc_info=True)

    def _ensure_server_keys(self) -> Tuple[str, str]:
        priv_path = self.config.private_key_path
        pub_path = self.config.public_key_path

        if priv_path.is_file() and pub_path.is_file():
            try:
                private_key = priv_path.read_text(encoding="utf-8").strip()
                public_key = pub_path.read_text(encoding="utf-8").strip()
                if private_key and public_key:
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

        iface = self.config.engine_interface()
        lines = [
            "[Interface]",
            f"PrivateKey = {self.server_private_key}",
            f"ListenPort = {self.config.port}",
            f"Address = {iface}",
            "",
        ]

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

    def describe_acl_defaults(self) -> Mapping[str, object]:
        return {
            "windows": list(self._normalise_allowed_ports(self.config.acl_allowlist_ports)),
            "client_to_client": False,
            "host_only": True,
        }

    def apply_firewall_rules(self, peer: Mapping[str, object]) -> List[str]:
        """Apply outbound firewall allow rules for the agent's virtual IP/ports (Windows netsh)."""

        rules = self.build_firewall_rules(peer)
        rule_names: List[str] = []
        for idx, rule in enumerate(rules):
            name = f"Borealis-WG-Agent-{peer.get('agent_id','')}-{idx}"
            protocol = str(rule.get("protocol") or "TCP").upper()
            local_port = rule.get("local_port")
            remote_port = rule.get("remote_port")
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
            else:
                self.logger.info("Applied firewall rule %s", name)
                rule_names.append(name)
        return rule_names

    def remove_firewall_rules(self, rule_names: Sequence[str]) -> None:
        for name in rule_names:
            if not name:
                continue
            args = ["netsh", "advfirewall", "firewall", "delete", "rule", f"name={name}"]
            code, out, err = self._run_command(args)
            if code != 0:
                self.logger.warning("Failed to remove firewall rule %s code=%s err=%s", name, code, err)
            else:
                self.logger.info("Removed firewall rule %s", name)

    def start_listener(self, peers: Sequence[Mapping[str, object]]) -> None:
        """Render a temporary WireGuard config and start the service."""

        try:
            self._config_dir.mkdir(parents=True, exist_ok=True)
        except Exception:
            self.logger.warning("Failed to create temp dir for WireGuard config", exc_info=True)

        config_path = self._config_dir / f"{self._service_name}.conf"
        rendered = self.render_server_config(peers)
        config_path.write_text(rendered, encoding="utf-8")
        self.logger.info("Rendered WireGuard config to %s", config_path)

        if self._service_exists():
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

    def stop_listener(self, *, ignore_missing: bool = False) -> None:
        """Stop the WireGuard tunnel service (leave installed for reuse)."""

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
