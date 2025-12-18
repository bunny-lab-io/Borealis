# ======================================================
# Data\Agent\Roles\role_WireGuardTunnel.py
# Description: WireGuard client lifecycle for outbound reverse VPN (Windows) with host-only /32 routing.
#
# API Endpoints (if applicable): None
# ======================================================

"""WireGuard client role (Windows) for reverse VPN tunnels.

This role prepares the WireGuard client config, manages a single active
session, enforces idle teardown, and logs lifecycle events to
Agent/Logs/reverse_tunnel.log. It binds to Engine Socket.IO events
(`vpn_tunnel_start`, `vpn_tunnel_stop`, `vpn_tunnel_activity`) to start/stop
the client session with the issued config/token.
"""

from __future__ import annotations

import base64
import json
import os
import subprocess
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Optional

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import x25519
from signature_utils import verify_and_store_script_signature

ROLE_NAME = "WireGuardTunnel"
ROLE_CONTEXTS = ["system"]


def _log_path() -> Path:
    root = Path(__file__).resolve().parents[2] / "Logs"
    root.mkdir(parents=True, exist_ok=True)
    return root / "reverse_tunnel.log"


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


@dataclass
class SessionConfig:
    token: Dict[str, Any]
    virtual_ip: str
    allowed_ips: str
    endpoint: str
    server_public_key: str
    allowed_ports: str
    idle_seconds: int = 900
    preshared_key: Optional[str] = None
    client_private_key: Optional[str] = None
    client_public_key: Optional[str] = None


class WireGuardClient:
    def __init__(self) -> None:
        base = Path(__file__).resolve().parents[2]
        self.cert_root = base / "Borealis" / "Certificates" / "VPN_Client"
        self.temp_root = base / "Borealis" / "Temp"
        self.temp_root.mkdir(parents=True, exist_ok=True)
        self.conf_path = self.temp_root / "borealis-wg-client.conf"
        self.service_name = "borealis-wg-client"
        self.session: Optional[SessionConfig] = None
        self.idle_deadline: Optional[float] = None
        self._idle_thread: Optional[threading.Thread] = None
        self._stop_event = threading.Event()
        self._client_keys = _generate_client_keys(self.cert_root)
        self._wg_exe = self._resolve_wireguard_exe()

    def _resolve_wireguard_exe(self) -> str:
        candidates = [
            str(Path(os.environ.get("ProgramFiles", "C:\\Program Files")) / "WireGuard" / "wireguard.exe"),
            "wireguard.exe",
        ]
        for candidate in candidates:
            if Path(candidate).is_file():
                return candidate
        return "wireguard.exe"

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
            "PersistentKeepalive = 20",
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

    def start_session(self, session: SessionConfig, *, signing_client: Optional[Any] = None) -> None:
        if self.session:
            _write_log("Rejecting start_session: existing session already active.")
            return

        try:
            self._validate_token(session.token, signing_client=signing_client)
        except Exception as exc:
            _write_log(f"Refusing to start WireGuard session: {exc}")
            return

        rendered = self._render_config(session)
        self.conf_path.write_text(rendered, encoding="utf-8")
        _write_log(f"Rendered WireGuard client config to {self.conf_path}")

        # Pre-stop any orphaned tunnel service using the same name
        self.stop_session(reason="preflight", ignore_missing=True)

        code, out, err = self._run([self._wg_exe, "/installtunnelservice", str(self.conf_path)])
        if code != 0:
            _write_log(f"Failed to install WireGuard client tunnel: code={code} err={err}")
            return

        self.session = session
        self.idle_deadline = time.time() + max(60, session.idle_seconds)
        _write_log("WireGuard client session started; idle timer armed.")
        self._start_idle_monitor()

    def stop_session(self, reason: str = "stop", ignore_missing: bool = False) -> None:
        code, out, err = self._run([self._wg_exe, "/uninstalltunnelservice", self.service_name])
        if code != 0:
            if not ignore_missing:
                _write_log(f"Failed to uninstall WireGuard client tunnel: code={code} err={err}")
        else:
            _write_log(f"WireGuard client session stopped (reason={reason}).")
        self.session = None
        self.idle_deadline = None
        self._stop_event.set()

    def bump_activity(self) -> None:
        if self.session and self.idle_deadline:
            self.idle_deadline = time.time() + max(60, self.session.idle_seconds)
            _write_log("WireGuard client activity bump; idle timer reset.")


client = WireGuardClient()


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


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.client = client
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="reverse_tunnel.log")
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

    def _build_session(self, payload: Any) -> Optional[SessionConfig]:
        if not isinstance(payload, dict):
            self._log("WireGuard start payload missing/invalid.", error=True)
            return None

        token = payload.get("token") or payload.get("orchestration_token")
        if not isinstance(token, dict):
            self._log("WireGuard start missing token payload.", error=True)
            return None

        virtual_ip = payload.get("virtual_ip") or payload.get("client_virtual_ip")
        endpoint = payload.get("endpoint") or payload.get("server_endpoint")
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
                reason = payload.get("reason") or reason
            self._log(f"WireGuard stop requested (reason={reason}).")
            self.client.stop_session(reason=str(reason))

        @sio.on("vpn_tunnel_activity")
        async def _vpn_tunnel_activity(payload):
            self.client.bump_activity()

    def stop_all(self) -> None:
        try:
            self.client.stop_session(reason="agent_shutdown")
        except Exception:
            self._log("Failed to stop WireGuard client during shutdown.", error=True)
