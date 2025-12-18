# ======================================================
# Data\Engine\services\VPN\vpn_tunnel_service.py
# Description: WireGuard tunnel orchestration (single tunnel per agent, token issuance, idle handling).
#
# API Endpoints (if applicable): None
# ======================================================

"""WireGuard tunnel orchestration helpers for the Engine runtime."""

from __future__ import annotations

import base64
import ipaddress
import json
import threading
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from .wireguard_server import WireGuardServerManager


@dataclass
class VpnSession:
    tunnel_id: str
    agent_id: str
    virtual_ip: str
    token: Dict[str, Any]
    client_public_key: str
    client_private_key: str
    allowed_ports: Tuple[int, ...]
    created_at: float
    expires_at: float
    last_activity: float
    operator_ids: set[str] = field(default_factory=set)
    firewall_rules: List[str] = field(default_factory=list)
    activity_id: Optional[int] = None
    hostname: Optional[str] = None


class VpnTunnelService:
    def __init__(
        self,
        *,
        context: Any,
        wireguard_manager: WireGuardServerManager,
        db_conn_factory,
        socketio,
        service_log,
        signer: Optional[Any] = None,
        idle_seconds: int = 900,
    ) -> None:
        self.context = context
        self.wg = wireguard_manager
        self.db_conn_factory = db_conn_factory
        self.socketio = socketio
        self.service_log = service_log
        self.signer = signer
        self.logger = context.logger.getChild("vpn_tunnel")
        self.activity_logger = self.wg.logger.getChild("device_activity")
        self.idle_seconds = max(60, int(idle_seconds))
        self._lock = threading.Lock()
        self._sessions_by_agent: Dict[str, VpnSession] = {}
        self._sessions_by_tunnel: Dict[str, VpnSession] = {}
        self._engine_ip = ipaddress.ip_interface(context.wireguard_engine_virtual_ip)
        self._peer_network = ipaddress.ip_network(context.wireguard_peer_network, strict=False)
        self._idle_thread = threading.Thread(target=self._idle_loop, daemon=True)
        self._idle_thread.start()

    def _idle_loop(self) -> None:
        while True:
            time.sleep(10)
            now = time.time()
            expired: List[VpnSession] = []
            with self._lock:
                for session in list(self._sessions_by_agent.values()):
                    if session.last_activity + self.idle_seconds <= now:
                        expired.append(session)
            for session in expired:
                self.disconnect(session.agent_id, reason="idle_timeout")

    def _allocate_virtual_ip(self, agent_id: str) -> str:
        existing = self._sessions_by_agent.get(agent_id)
        if existing:
            return existing.virtual_ip

        used = {s.virtual_ip for s in self._sessions_by_agent.values()}
        for host in self._peer_network.hosts():
            if host == self._engine_ip.ip:
                continue
            candidate = f"{host}/32"
            if candidate not in used:
                return candidate
        raise RuntimeError("vpn_ip_pool_exhausted")

    def _load_allowed_ports(self, agent_id: str) -> Tuple[int, ...]:
        default = tuple(self.context.wireguard_acl_allowlist_windows or ())
        try:
            conn = self.db_conn_factory()
            cur = conn.cursor()
            try:
                cur.execute(
                    "SELECT operating_system FROM devices WHERE agent_id=? ORDER BY last_seen DESC LIMIT 1",
                    (agent_id,),
                )
                row = cur.fetchone()
                os_name = str(row[0]).lower() if row and row[0] else ""
            except Exception:
                os_name = ""
            if os_name and "windows" not in os_name:
                baseline = {5900, 3478}
                filtered = [p for p in default if p in baseline]
                if filtered:
                    default = tuple(filtered)
            cur.execute(
                "SELECT allowed_ports FROM device_vpn_config WHERE agent_id=?",
                (agent_id,),
            )
            row = cur.fetchone()
            if not row:
                return default
            raw = row[0] or ""
            ports = json.loads(raw) if raw else []
            ports = [int(p) for p in ports if isinstance(p, (int, float, str))]
            ports = [p for p in ports if 1 <= p <= 65535]
            return tuple(dict.fromkeys(ports)) or default
        except Exception:
            return default
        finally:
            try:
                conn.close()
            except Exception:
                pass

    def _generate_client_keys(self) -> Tuple[str, str]:
        from cryptography.hazmat.primitives import serialization
        from cryptography.hazmat.primitives.asymmetric import x25519

        key = x25519.X25519PrivateKey.generate()
        priv = base64.b64encode(
            key.private_bytes(
                encoding=serialization.Encoding.Raw,
                format=serialization.PrivateFormat.Raw,
                encryption_algorithm=serialization.NoEncryption(),
            )
        ).decode("ascii").strip()
        pub = base64.b64encode(
            key.public_key().public_bytes(
                encoding=serialization.Encoding.Raw,
                format=serialization.PublicFormat.Raw,
            )
        ).decode("ascii").strip()
        return priv, pub

    def _issue_token(self, agent_id: str, tunnel_id: str, expires_at: float) -> Dict[str, Any]:
        payload = {
            "agent_id": agent_id,
            "tunnel_id": tunnel_id,
            "port": self.context.wireguard_port,
            "expires_at": expires_at,
            "issued_at": time.time(),
        }
        if not self.signer:
            return dict(payload)

        token = dict(payload)
        try:
            payload_bytes = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
            signature = self.signer.sign(payload_bytes)
            token["signature"] = base64.b64encode(signature).decode("ascii")
            if hasattr(self.signer, "public_base64_spki"):
                token["signing_key"] = self.signer.public_base64_spki()
            token["sig_alg"] = "ed25519"
        except Exception:
            self.logger.debug("Failed to sign VPN orchestration token; sending unsigned.", exc_info=True)
        return token

    def _refresh_listener(self) -> None:
        peers: List[Mapping[str, object]] = []
        for session in self._sessions_by_agent.values():
            peer = self.wg.build_peer_profile(
                session.agent_id,
                session.virtual_ip,
                allowed_ports=session.allowed_ports,
            )
            peer = dict(peer)
            peer["public_key"] = session.client_public_key
            peers.append(peer)
        if not peers:
            self.wg.stop_listener()
            return
        self.wg.start_listener(peers)

    def connect(self, *, agent_id: str, operator_id: Optional[str]) -> Mapping[str, Any]:
        now = time.time()
        with self._lock:
            existing = self._sessions_by_agent.get(agent_id)
            if existing:
                if operator_id:
                    existing.operator_ids.add(operator_id)
                existing.last_activity = now
                return self._session_payload(existing)

            tunnel_id = uuid.uuid4().hex
            virtual_ip = self._allocate_virtual_ip(agent_id)
            allowed_ports = self._load_allowed_ports(agent_id)
            client_private, client_public = self._generate_client_keys()
            token = self._issue_token(agent_id, tunnel_id, now + 300)
            self.wg.require_orchestration_token(token)

            session = VpnSession(
                tunnel_id=tunnel_id,
                agent_id=agent_id,
                virtual_ip=virtual_ip,
                token=token,
                client_public_key=client_public,
                client_private_key=client_private,
                allowed_ports=allowed_ports,
                created_at=now,
                expires_at=now + 300,
                last_activity=now,
            )
            if operator_id:
                session.operator_ids.add(operator_id)
            self._sessions_by_agent[agent_id] = session
            self._sessions_by_tunnel[tunnel_id] = session

        try:
            self._refresh_listener()

            peer = self.wg.build_peer_profile(
                agent_id,
                virtual_ip,
                allowed_ports=allowed_ports,
            )
            rule_names = self.wg.apply_firewall_rules(peer)
            session.firewall_rules = rule_names
        except Exception:
            with self._lock:
                self._sessions_by_agent.pop(agent_id, None)
                self._sessions_by_tunnel.pop(tunnel_id, None)
            try:
                self._refresh_listener()
            except Exception:
                self.logger.debug("Failed to refresh WireGuard listener after connect rollback.", exc_info=True)
            raise

        payload = self._session_payload(session)
        self._emit_start(payload)
        self._log_device_activity(session, event="start")
        return payload

    def status(self, agent_id: str) -> Optional[Mapping[str, Any]]:
        with self._lock:
            session = self._sessions_by_agent.get(agent_id)
        if not session:
            return None
        return self._session_payload(session, include_token=False)

    def bump_activity(self, agent_id: str) -> None:
        with self._lock:
            session = self._sessions_by_agent.get(agent_id)
        if not session:
            return
        session.last_activity = time.time()
        try:
            if self.socketio:
                self.socketio.emit("vpn_tunnel_activity", {"agent_id": agent_id}, namespace="/")
        except Exception:
            self.logger.debug("vpn_tunnel_activity emit failed for agent_id=%s", agent_id, exc_info=True)

    def disconnect(self, agent_id: str, reason: str = "operator_stop") -> bool:
        with self._lock:
            session = self._sessions_by_agent.pop(agent_id, None)
            if not session:
                return False
            self._sessions_by_tunnel.pop(session.tunnel_id, None)

        try:
            self.wg.remove_firewall_rules(session.firewall_rules)
        except Exception:
            self.logger.debug("Failed to remove firewall rules for agent=%s", agent_id, exc_info=True)

        self._refresh_listener()
        self._emit_stop(session, reason)
        self._log_device_activity(session, event="stop", reason=reason)
        return True

    def disconnect_by_tunnel(self, tunnel_id: str, reason: str = "operator_stop") -> bool:
        with self._lock:
            session = self._sessions_by_tunnel.get(tunnel_id)
        if not session:
            return False
        return self.disconnect(session.agent_id, reason=reason)

    def _emit_start(self, payload: Mapping[str, Any]) -> None:
        if not self.socketio:
            return
        try:
            self.socketio.emit("vpn_tunnel_start", payload, namespace="/")
        except Exception:
            self.logger.debug("vpn_tunnel_start emit failed", exc_info=True)

    def _emit_stop(self, session: VpnSession, reason: str) -> None:
        if not self.socketio:
            return
        try:
            self.socketio.emit(
                "vpn_tunnel_stop",
                {"agent_id": session.agent_id, "tunnel_id": session.tunnel_id, "reason": reason},
                namespace="/",
            )
        except Exception:
            self.logger.debug("vpn_tunnel_stop emit failed", exc_info=True)

    def _log_device_activity(self, session: VpnSession, *, event: str, reason: Optional[str] = None) -> None:
        if self.db_conn_factory is None:
            self.activity_logger.info(
                "device_activity event=%s agent_id=%s tunnel_id=%s operator=%s reason=%s",
                event,
                session.agent_id,
                session.tunnel_id,
                ",".join(sorted(filter(None, session.operator_ids))) or "-",
                reason or "-",
            )
            return

        conn = None
        try:
            conn = self.db_conn_factory()
            cur = conn.cursor()
            hostname = session.hostname
            if not hostname:
                try:
                    cur.execute(
                        "SELECT hostname FROM devices WHERE agent_id = ? ORDER BY last_seen DESC LIMIT 1",
                        (session.agent_id,),
                    )
                    row = cur.fetchone()
                    if row and row[0]:
                        hostname = str(row[0]).strip()
                        session.hostname = hostname
                except Exception:
                    hostname = None

            if not hostname:
                self.activity_logger.info(
                    "device_activity event=%s agent_id=%s tunnel_id=%s operator=%s reason=%s hostname=unknown",
                    event,
                    session.agent_id,
                    session.tunnel_id,
                    ",".join(sorted(filter(None, session.operator_ids))) or "-",
                    reason or "-",
                )
                return

            now_ts = int(time.time())
            script_name = "Reverse VPN Tunnel (WireGuard)"

            if event == "start":
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        hostname,
                        session.tunnel_id,
                        script_name,
                        "vpn_tunnel",
                        now_ts,
                        "Running",
                        "",
                        "",
                    ),
                )
                session.activity_id = cur.lastrowid
                conn.commit()
                if self.socketio:
                    try:
                        self.socketio.emit(
                            "device_activity_changed",
                            {
                                "hostname": hostname,
                                "activity_id": session.activity_id,
                                "change": "created",
                                "source": "vpn_tunnel",
                            },
                        )
                    except Exception:
                        pass
                self.activity_logger.info(
                    "device_activity_start hostname=%s agent_id=%s tunnel_id=%s operator=%s activity_id=%s",
                    hostname,
                    session.agent_id,
                    session.tunnel_id,
                    ",".join(sorted(filter(None, session.operator_ids))) or "-",
                    session.activity_id or "-",
                )
                return

            if session.activity_id:
                status = "Completed" if event == "stop" else "Closed"
                cur.execute(
                    """
                    UPDATE activity_history
                       SET status=?,
                           stderr=COALESCE(stderr, '') || ?
                     WHERE id=?
                    """,
                    (
                        status,
                        f"\nreason: {reason}" if reason else "",
                        session.activity_id,
                    ),
                )
                conn.commit()
                if self.socketio:
                    try:
                        self.socketio.emit(
                            "device_activity_changed",
                            {
                                "hostname": hostname,
                                "activity_id": session.activity_id,
                                "change": "updated",
                                "source": "vpn_tunnel",
                            },
                        )
                    except Exception:
                        pass

            self.activity_logger.info(
                "device_activity event=%s hostname=%s agent_id=%s tunnel_id=%s operator=%s reason=%s activity_id=%s",
                event,
                hostname,
                session.agent_id,
                session.tunnel_id,
                ",".join(sorted(filter(None, session.operator_ids))) or "-",
                reason or "-",
                session.activity_id or "-",
            )
        except Exception:
            self.activity_logger.debug(
                "device_activity logging failed for tunnel_id=%s",
                session.tunnel_id,
                exc_info=True,
            )
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def _session_payload(self, session: VpnSession, *, include_token: bool = True) -> Mapping[str, Any]:
        payload: Dict[str, Any] = {
            "tunnel_id": session.tunnel_id,
            "agent_id": session.agent_id,
            "virtual_ip": session.virtual_ip,
            "engine_virtual_ip": str(self._engine_ip.ip),
            "allowed_ips": f"{self._engine_ip.ip}/32",
            "endpoint": f"{self._engine_ip.ip}:{self.context.wireguard_port}",
            "server_public_key": self.wg.server_public_key,
            "client_public_key": session.client_public_key,
            "client_private_key": session.client_private_key,
            "idle_seconds": self.idle_seconds,
            "allowed_ports": list(session.allowed_ports),
            "connected_operators": len([o for o in session.operator_ids if o]),
        }
        if include_token:
            payload["token"] = session.token
        return payload
