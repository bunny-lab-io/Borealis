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
import subprocess
import tempfile
import time
from dataclasses import dataclass
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Dict, Iterable, List, Mapping, Optional, Sequence, Tuple, Union

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import x25519


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
    acl_allowlist_windows: Tuple[int, ...]
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
        self._service_name = "BorealisWireGuard"
        self._temp_dir = Path(tempfile.gettempdir()) / "borealis-wg-engine"

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

    def _normalise_allowed_ports(
        self,
        candidate: Optional[Iterable[int]],
        overrides: Optional[Iterable[int]] = None,
    ) -> Tuple[int, ...]:
        ports: List[int] = []
        sources: List[Iterable[int]] = []
        if overrides:
            sources.append(overrides)
        if candidate:
            sources.append(candidate)
        if not sources:
            sources.append(self.config.acl_allowlist_windows)

        for source in sources:
            for port in source:
                try:
                    value = int(port)
                except Exception:
                    continue
                if 1 <= value <= 65535:
                    ports.append(value)
        if not ports:
            ports = list(self.config.acl_allowlist_windows)
        return tuple(sorted(dict.fromkeys(ports)))

    def require_orchestration_token(self, token: Optional[Mapping[str, object]]) -> Mapping[str, object]:
        """Validate orchestration token shape and expiry (best-effort)."""

        if not token:
            raise ValueError("Missing orchestration token for WireGuard peer")

        required_fields = ("agent_id", "tunnel_id", "expires_at")
        missing = [field for field in required_fields if field not in token or token[field] in (None, "")]
        if missing:
            raise ValueError(f"Invalid orchestration token; missing {', '.join(missing)}")

        try:
            expires_at = float(token["expires_at"])
        except Exception:
            raise ValueError("Invalid orchestration token expiry")

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
        self.logger.info(
            "Prepared WireGuard peer profile for agent=%s ip=%s allowed_ports=%s",
            agent_id,
            ip,
            ",".join(str(p) for p in allowed),
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
            "SaveConfig = false",
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
            "windows": list(self.config.acl_allowlist_windows),
            "client_to_client": False,
            "host_only": True,
        }

    def apply_firewall_rules(self, peer: Mapping[str, object]) -> None:
        """Apply outbound firewall allow rules for the agent's virtual IP/ports (Windows netsh)."""

        rules = self.build_firewall_rules(peer)
        for idx, rule in enumerate(rules):
            name = f"Borealis-WG-Agent-{peer.get('agent_id','')}-{idx}"
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
                f"protocol=TCP",
                f"localport={rule.get('local_port','')}",
            ]
            code, out, err = self._run_command(args)
            if code != 0:
                self.logger.warning("Failed to apply firewall rule %s code=%s err=%s", name, code, err)
            else:
                self.logger.info("Applied firewall rule %s", name)

    def start_listener(self, peers: Sequence[Mapping[str, object]]) -> None:
        """Render a temporary WireGuard config and start the service."""

        try:
            self._temp_dir.mkdir(parents=True, exist_ok=True)
        except Exception:
            self.logger.warning("Failed to create temp dir for WireGuard config", exc_info=True)

        config_path = self._temp_dir / "borealis-wg.conf"
        rendered = self.render_server_config(peers)
        config_path.write_text(rendered, encoding="utf-8")
        self.logger.info("Rendered WireGuard config to %s", config_path)

        args = ["wireguard.exe", "/installtunnelservice", str(config_path)]
        code, out, err = self._run_command(args)
        if code != 0:
            self.logger.error("Failed to install WireGuard tunnel service code=%s err=%s", code, err)
            raise RuntimeError(f"WireGuard installtunnelservice failed: {err}")
        self.logger.info("WireGuard listener installed (service=%s)", config_path.stem)

    def stop_listener(self) -> None:
        """Stop and remove the WireGuard tunnel service."""

        args = ["wireguard.exe", "/uninstalltunnelservice", "borealis-wg"]
        code, out, err = self._run_command(args)
        if code != 0:
            self.logger.warning("Failed to uninstall WireGuard tunnel service code=%s err=%s", code, err)
        else:
            self.logger.info("WireGuard tunnel service removed")

    def build_firewall_rules(
        self,
        peer: Mapping[str, object],
    ) -> List[Mapping[str, Union[str, int]]]:
        """Compute firewall allow rules for engine->agent (host-only)."""

        rules: List[Mapping[str, Union[str, int]]] = []
        ip = str(peer.get("virtual_ip", "")).split("/")[0]
        ports = peer.get("allowed_ports") or []
        try:
            port_list = [int(p) for p in ports]
        except Exception:
            port_list = []

        for port in port_list:
            rules.append(
                {
                    "direction": "outbound",
                    "remote_address": ip,
                    "local_port": port,
                    "action": "allow",
                    "description": f"WireGuard engine->agent allow port {port}",
                }
            )

        self.logger.info(
            "Prepared firewall rule plan for agent=%s rules=%s",
            peer.get("agent_id", ""),
            ",".join(str(rule.get("local_port")) for rule in rules),
        )
        return rules
