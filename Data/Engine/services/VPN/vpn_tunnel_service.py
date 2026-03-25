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
import os
import threading
import time
import uuid
from datetime import datetime, timezone
from dataclasses import dataclass, field
from typing import Any, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from .wireguard_server import WireGuardServerManager


def _env_flag(name: str, *, default: bool) -> bool:
    value = os.environ.get(name)
    if value is None:
        return default
    return str(value).strip().lower() not in ("0", "false", "no", "off")


def _format_ports(ports: Iterable[int]) -> str:
    items = [str(p) for p in ports]
    if not items:
        return "all"
    return ",".join(items)


WATCHDOG_INTERVAL_SECONDS = 15
LISTENER_RECOVERY_MIN_INTERVAL_SECONDS = 30


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
    endpoint_host: Optional[str] = None


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
        self.persistent = _env_flag("BOREALIS_WIREGUARD_PERSISTENT", default=True)
        self._lock = threading.Lock()
        self._sessions_by_agent: Dict[str, VpnSession] = {}
        self._sessions_by_tunnel: Dict[str, VpnSession] = {}
        self._engine_ip = ipaddress.ip_interface(context.wireguard_engine_virtual_ip)
        self._peer_network = ipaddress.ip_network(context.wireguard_peer_network, strict=False)
        self._listener_healthy = False
        self._listener_health_reason = "startup"
        self._recovery_in_progress = False
        self._last_recovery_attempt_at: Optional[float] = None
        self._last_recovery_attempt_at_iso = ""
        self._last_throttle_log_at: Optional[float] = None
        self._watchdog_thread: Optional[Any] = None
        self._cleanup_listener()
        self._idle_thread: Optional[threading.Thread] = None
        if self.persistent:
            self._start_watchdog()
        else:
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
                self._service_log_event(
                    "vpn_tunnel_idle_timeout agent_id={0} tunnel_id={1} last_activity={2} last_activity_iso={3} idle_seconds={4}".format(
                        session.agent_id,
                        session.tunnel_id,
                        int(session.last_activity),
                        self._ts_to_iso(session.last_activity),
                        self.idle_seconds,
                    )
                )
                self.disconnect(session.agent_id, reason="idle_timeout", force=True)

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
        return tuple(self.context.wireguard_port_allowlist or ())

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

    def _ensure_token(self, session: VpnSession, *, now: Optional[float] = None) -> None:
        if not session:
            return
        current = now if now is not None else time.time()
        if session.expires_at > current + 30:
            return
        session.expires_at = current + 300
        session.token = self._issue_token(session.agent_id, session.tunnel_id, session.expires_at)

    def _normalize_endpoint_host(self, host: Optional[str]) -> Optional[str]:
        if not host:
            return None
        try:
            text = str(host).strip()
        except Exception:
            return None
        return text or None

    def _format_endpoint_host(self, host: str) -> str:
        if ":" in host and not host.startswith("["):
            return f"[{host}]"
        return host

    def _ts_to_iso(self, ts: float) -> str:
        try:
            return datetime.fromtimestamp(ts, timezone.utc).isoformat()
        except Exception:
            return ""

    def _service_log_event(self, message: str, *, level: str = "INFO") -> None:
        if not callable(self.service_log):
            return
        try:
            self.service_log("VPN_Tunnel/tunnel", message, level=level)
        except Exception:
            self.logger.debug("Failed to write vpn_tunnel service log entry", exc_info=True)

    def _cleanup_listener(self) -> None:
        self._service_log_event("vpn_listener_cleanup_skipped reason=startup")

    def _sleep(self, seconds: float) -> None:
        try:
            import eventlet  # type: ignore

            eventlet.sleep(seconds)
        except Exception:
            time.sleep(seconds)

    def _start_watchdog(self) -> None:
        def _loop() -> None:
            while True:
                try:
                    self._watchdog_tick()
                except Exception as exc:
                    self._service_log_event(
                        "vpn_listener_watchdog_failed error={0}".format(repr(exc)),
                        level="ERROR",
                    )
                    self.logger.debug("WireGuard listener watchdog tick failed.", exc_info=True)
                self._sleep(WATCHDOG_INTERVAL_SECONDS)

        starter = getattr(self.socketio, "start_background_task", None)
        try:
            if callable(starter):
                self._watchdog_thread = starter(_loop)
                self._service_log_event(
                    "vpn_listener_watchdog_started transport=socketio interval={0}".format(
                        WATCHDOG_INTERVAL_SECONDS
                    )
                )
                return
        except Exception:
            self.logger.debug("Failed to start WireGuard listener watchdog via socketio.", exc_info=True)

        thread = threading.Thread(target=_loop, daemon=True)
        thread.start()
        self._watchdog_thread = thread
        self._service_log_event(
            "vpn_listener_watchdog_started transport=thread interval={0}".format(
                WATCHDOG_INTERVAL_SECONDS
            )
        )

    def _active_sessions(self) -> List[VpnSession]:
        with self._lock:
            return list(self._sessions_by_agent.values())

    def _apply_listener_health_locked(
        self,
        health: Mapping[str, Any],
        *,
        active_sessions: bool,
        recovery_in_progress: Optional[bool] = None,
    ) -> None:
        self._listener_healthy = bool(health.get("healthy"))
        self._listener_health_reason = str(health.get("reason") or "")
        if recovery_in_progress is None:
            recovery_flag = active_sessions and not self._listener_healthy
        else:
            recovery_flag = active_sessions and bool(recovery_in_progress)
        self._recovery_in_progress = recovery_flag
        if self._listener_healthy:
            self._last_throttle_log_at = None

    def _clear_listener_health(self, *, reason: str) -> None:
        with self._lock:
            self._listener_healthy = False
            self._listener_health_reason = reason
            self._recovery_in_progress = False

    def _check_listener_health(self) -> Mapping[str, Any]:
        try:
            return dict(self.wg.check_listener_health())
        except Exception as exc:
            self.logger.debug("WireGuard listener health check failed.", exc_info=True)
            return {
                "healthy": False,
                "reason": "health_check_failed",
                "service_state": None,
                "error": repr(exc),
            }

    def _listener_health_payload(self, *, refresh: bool = False) -> Mapping[str, Any]:
        active_sessions = self._active_sessions()
        if not active_sessions:
            self._clear_listener_health(reason="no_active_sessions")
            return {
                "listener_healthy": False,
                "recovery_in_progress": False,
                "last_recovery_attempt_at": None,
                "last_recovery_attempt_at_iso": "",
            }

        if refresh:
            health = self._check_listener_health()
            with self._lock:
                self._apply_listener_health_locked(
                    health,
                    active_sessions=True,
                )

        with self._lock:
            return {
                "listener_healthy": self._listener_healthy,
                "recovery_in_progress": self._recovery_in_progress,
                "last_recovery_attempt_at": (
                    int(self._last_recovery_attempt_at) if self._last_recovery_attempt_at is not None else None
                ),
                "last_recovery_attempt_at_iso": self._last_recovery_attempt_at_iso,
            }

    def _build_listener_peers(self) -> List[Mapping[str, object]]:
        peers: List[Mapping[str, object]] = []
        for session in self._active_sessions():
            peer = self.wg.build_peer_profile(
                session.agent_id,
                session.virtual_ip,
                allowed_ports=session.allowed_ports,
            )
            peer = dict(peer)
            peer["public_key"] = session.client_public_key
            peers.append(peer)
        return peers

    def _refresh_listener(self) -> None:
        peers = self._build_listener_peers()
        if not peers:
            self._service_log_event("vpn_listener_stop reason=no_peers")
            self.wg.stop_listener()
            self._clear_listener_health(reason="no_active_sessions")
            return
        agent_list = ",".join(str(peer.get("agent_id", "")) for peer in peers if peer.get("agent_id"))
        self._service_log_event("vpn_listener_start peers={0} agents={1}".format(len(peers), agent_list))
        self.wg.start_listener(peers)
        health = self._check_listener_health()
        with self._lock:
            self._apply_listener_health_locked(health, active_sessions=True)

    def _recover_listener(self, *, trigger: str, reason: str) -> Mapping[str, Any]:
        active_sessions = self._active_sessions()
        if not active_sessions:
            self._clear_listener_health(reason="no_active_sessions")
            return self._listener_health_payload(refresh=False)

        initial_health = self._check_listener_health()
        if initial_health.get("healthy"):
            with self._lock:
                self._apply_listener_health_locked(initial_health, active_sessions=True, recovery_in_progress=False)
            return self._listener_health_payload(refresh=False)

        now = time.time()
        throttled = False
        with self._lock:
            self._apply_listener_health_locked(initial_health, active_sessions=True, recovery_in_progress=True)
            last_attempt = self._last_recovery_attempt_at
            if last_attempt is not None and (now - last_attempt) < LISTENER_RECOVERY_MIN_INTERVAL_SECONDS:
                if (
                    self._last_throttle_log_at is None
                    or (now - self._last_throttle_log_at) >= LISTENER_RECOVERY_MIN_INTERVAL_SECONDS
                ):
                    self._service_log_event(
                        "vpn_listener_recovery_throttled trigger={0} reason={1} service_reason={2} min_retry_interval={3}".format(
                            trigger or "-",
                            reason or "-",
                            self._listener_health_reason or "-",
                            LISTENER_RECOVERY_MIN_INTERVAL_SECONDS,
                        ),
                        level="WARNING",
                    )
                    self._last_throttle_log_at = now
                throttled = True
            else:
                self._last_recovery_attempt_at = now
                self._last_recovery_attempt_at_iso = self._ts_to_iso(now)

        if throttled:
            return self._listener_health_payload(refresh=False)

        prior_state = initial_health.get("service_state") or "-"
        prior_reason = initial_health.get("reason") or "unhealthy"
        self._service_log_event(
            "vpn_listener_recovery_attempt trigger={0} reason={1} prior_reason={2} prior_state={3}".format(
                trigger or "-",
                reason or "-",
                prior_reason,
                prior_state,
            ),
            level="WARNING",
        )

        try:
            self._refresh_listener()
        except Exception as exc:
            with self._lock:
                self._recovery_in_progress = True
                self._listener_healthy = False
                self._listener_health_reason = "refresh_failed"
            self._service_log_event(
                "vpn_listener_recovery_failed trigger={0} reason={1} error={2}".format(
                    trigger or "-",
                    reason or "-",
                    repr(exc),
                ),
                level="ERROR",
            )
            self.logger.debug("WireGuard listener recovery attempt failed.", exc_info=True)
            return self._listener_health_payload(refresh=False)

        recovered_health = self._check_listener_health()
        with self._lock:
            self._apply_listener_health_locked(
                recovered_health,
                active_sessions=True,
                recovery_in_progress=not bool(recovered_health.get("healthy")),
            )
        event_name = "vpn_listener_recovery_success" if recovered_health.get("healthy") else "vpn_listener_recovery_failed"
        event_level = "INFO" if recovered_health.get("healthy") else "ERROR"
        self._service_log_event(
            "{0} trigger={1} reason={2} service_reason={3} service_state={4}".format(
                event_name,
                trigger or "-",
                reason or "-",
                recovered_health.get("reason") or "-",
                recovered_health.get("service_state") or "-",
            ),
            level=event_level,
        )
        return self._listener_health_payload(refresh=False)

    def _ensure_listener_ready(self, *, trigger: str, reason: str) -> Mapping[str, Any]:
        health = self._listener_health_payload(refresh=True)
        if health.get("listener_healthy"):
            return health
        return self._recover_listener(trigger=trigger, reason=reason)

    def _watchdog_tick(self) -> None:
        active_sessions = self._active_sessions()
        if not active_sessions:
            self._clear_listener_health(reason="no_active_sessions")
            return
        health = self._listener_health_payload(refresh=True)
        if health.get("listener_healthy"):
            return
        self._recover_listener(trigger="watchdog", reason="listener_unhealthy")

    def _is_soft_disconnect(self, reason: Optional[str]) -> bool:
        if not reason:
            return False
        return str(reason).lower() in ("operator_disconnect", "component_unmount")

    def connect(
        self,
        *,
        agent_id: str,
        operator_id: Optional[str],
        endpoint_host: Optional[str] = None,
    ) -> Mapping[str, Any]:
        now = time.time()
        normalized_host = self._normalize_endpoint_host(endpoint_host)
        operator_text = operator_id or "-"
        self._service_log_event(
            "vpn_tunnel_connect_request agent_id={0} operator={1} endpoint_host={2}".format(
                agent_id or "-",
                operator_text,
                normalized_host or "-",
            )
        )
        with self._lock:
            existing = self._sessions_by_agent.get(agent_id)
            if existing:
                if operator_id:
                    existing.operator_ids.add(operator_id)
                if normalized_host and not existing.endpoint_host:
                    existing.endpoint_host = normalized_host
                existing.last_activity = now
                previous_expiry = existing.expires_at
                self._ensure_token(existing, now=now)
                refreshed = existing.expires_at != previous_expiry
                operator_list = ",".join(sorted(filter(None, existing.operator_ids))) or "-"
                self._service_log_event(
                    "vpn_tunnel_session_reuse agent_id={0} tunnel_id={1} operators={2} token_refreshed={3}".format(
                        existing.agent_id,
                        existing.tunnel_id,
                        operator_list,
                        str(refreshed).lower(),
                    )
                )
                payload = dict(self._session_payload(existing))
            else:
                payload = {}

        if payload:
            self._ensure_listener_ready(trigger="connect_reuse", reason="session_reuse")
            return payload

        with self._lock:
            tunnel_id = uuid.uuid4().hex
            virtual_ip = self._allocate_virtual_ip(agent_id)
            allowed_ports = self._load_allowed_ports(agent_id)
            client_private, client_public = self._generate_client_keys()
            token = self._issue_token(agent_id, tunnel_id, now + 300)
            self.wg.require_orchestration_token(token)
            token_signed = "signature" in token

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
                endpoint_host=normalized_host,
            )
            if operator_id:
                session.operator_ids.add(operator_id)
            self._sessions_by_agent[agent_id] = session
            self._sessions_by_tunnel[tunnel_id] = session

        try:
            self._service_log_event(
                "vpn_tunnel_session_create agent_id={0} tunnel_id={1} virtual_ip={2} allowed_ports={3} token_signed={4} token_expires={5}".format(
                    session.agent_id,
                    session.tunnel_id,
                    session.virtual_ip,
                    _format_ports(allowed_ports),
                    str(bool(token_signed)).lower(),
                    int(session.expires_at),
                )
            )
            self._refresh_listener()

            peer = self.wg.build_peer_profile(
                agent_id,
                virtual_ip,
                allowed_ports=allowed_ports,
            )
            rule_names = self.wg.apply_firewall_rules(peer)
            session.firewall_rules = rule_names
            self._service_log_event(
                "vpn_tunnel_firewall_applied agent_id={0} tunnel_id={1} rules={2}".format(
                    session.agent_id,
                    session.tunnel_id,
                    len(rule_names),
                )
            )
        except Exception:
            self._service_log_event(
                "vpn_tunnel_connect_failed agent_id={0} tunnel_id={1}".format(agent_id, tunnel_id),
                level="ERROR",
            )
            with self._lock:
                self._sessions_by_agent.pop(agent_id, None)
                self._sessions_by_tunnel.pop(tunnel_id, None)
            try:
                self._refresh_listener()
            except Exception:
                self.logger.debug("Failed to refresh WireGuard listener after connect rollback.", exc_info=True)
            raise

        payload = self._session_payload(session)
        operator_text = ",".join(sorted(filter(None, session.operator_ids))) or "-"
        self._service_log_event(
            "vpn_tunnel_start agent_id={0} tunnel_id={1} virtual_ip={2} endpoint={3} allowed_ports={4} operators={5}".format(
                session.agent_id,
                session.tunnel_id,
                session.virtual_ip,
                payload.get("endpoint", ""),
                _format_ports(session.allowed_ports),
                operator_text,
            )
        )
        self._emit_start(payload)
        self._log_device_activity(session, event="start")
        return payload

    def status(self, agent_id: str) -> Optional[Mapping[str, Any]]:
        with self._lock:
            session = self._sessions_by_agent.get(agent_id)
        if not session:
            return None
        payload = dict(self._session_payload(session, include_token=False))
        payload.update(self._listener_health_payload(refresh=True))
        return payload

    def list_sessions(self) -> List[Mapping[str, Any]]:
        with self._lock:
            sessions = sorted(self._sessions_by_agent.values(), key=lambda s: s.agent_id)
        health = self._listener_health_payload(refresh=bool(sessions))
        return [
            {
                **self._session_summary(session),
                **health,
            }
            for session in sessions
        ]

    def session_payload(self, agent_id: str, *, include_token: bool = True) -> Optional[Mapping[str, Any]]:
        with self._lock:
            session = self._sessions_by_agent.get(agent_id)
            if not session:
                return None
            if include_token:
                self._ensure_token(session)
            return self._session_payload(session, include_token=include_token)

    def request_agent_start(self, agent_id: str) -> Optional[Mapping[str, Any]]:
        payload = self.session_payload(agent_id, include_token=True)
        if not payload:
            self._service_log_event("vpn_tunnel_agent_start_missing agent_id={0}".format(agent_id or "-"))
            return None
        self._service_log_event(
            "vpn_tunnel_agent_start_emit agent_id={0} tunnel_id={1}".format(
                payload.get("agent_id", "-"),
                payload.get("tunnel_id", "-"),
            )
        )
        self._emit_start(payload)
        return payload

    def bump_activity(self, agent_id: str) -> None:
        with self._lock:
            session = self._sessions_by_agent.get(agent_id)
        if not session:
            return
        now = time.time()
        previous = session.last_activity
        session.last_activity = now
        idle_for = now - previous
        if idle_for >= 60:
            self._service_log_event(
                "vpn_tunnel_activity_bump agent_id={0} tunnel_id={1} idle_for={2}".format(
                    session.agent_id,
                    session.tunnel_id,
                    int(idle_for),
                )
            )
        try:
            if self.socketio:
                self.socketio.emit("vpn_tunnel_activity", {"agent_id": agent_id}, namespace="/")
        except Exception:
            self.logger.debug("vpn_tunnel_activity emit failed for agent_id=%s", agent_id, exc_info=True)

    def disconnect(
        self,
        agent_id: str,
        reason: str = "operator_stop",
        *,
        operator_id: Optional[str] = None,
        force: bool = False,
    ) -> bool:
        with self._lock:
            session = self._sessions_by_agent.get(agent_id)
            if not session:
                self._service_log_event(
                    "vpn_tunnel_disconnect_missing agent_id={0} reason={1}".format(agent_id or "-", reason or "-")
                )
                return False
            if self.persistent and not force:
                if operator_id:
                    try:
                        session.operator_ids.discard(operator_id)
                    except Exception:
                        pass
                session.last_activity = time.time()
                operator_text = ",".join(sorted(filter(None, session.operator_ids))) or "-"
                self._service_log_event(
                    "vpn_tunnel_keepalive agent_id={0} tunnel_id={1} reason={2} operators={3}".format(
                        session.agent_id,
                        session.tunnel_id,
                        reason or "-",
                        operator_text,
                    )
                )
                return True
            if not force and self._is_soft_disconnect(reason):
                if operator_id:
                    try:
                        session.operator_ids.discard(operator_id)
                    except Exception:
                        pass
                session.last_activity = time.time()
                operator_text = ",".join(sorted(filter(None, session.operator_ids))) or "-"
                self._service_log_event(
                    "vpn_tunnel_keepalive agent_id={0} tunnel_id={1} reason={2} operators={3}".format(
                        session.agent_id,
                        session.tunnel_id,
                        reason or "-",
                        operator_text,
                    )
                )
                return True
            session = self._sessions_by_agent.pop(agent_id, None)
            if session:
                self._sessions_by_tunnel.pop(session.tunnel_id, None)
            else:
                return False

        try:
            self.wg.remove_firewall_rules(session.firewall_rules)
        except Exception:
            self.logger.debug("Failed to remove firewall rules for agent=%s", agent_id, exc_info=True)

        self._refresh_listener()
        operator_text = ",".join(sorted(filter(None, session.operator_ids))) or "-"
        self._service_log_event(
            "vpn_tunnel_stop agent_id={0} tunnel_id={1} reason={2} operators={3}".format(
                session.agent_id,
                session.tunnel_id,
                reason,
                operator_text,
            )
        )
        self._emit_stop(session, reason)
        self._log_device_activity(session, event="stop", reason=reason)
        return True

    def disconnect_by_tunnel(
        self,
        tunnel_id: str,
        reason: str = "operator_stop",
        *,
        operator_id: Optional[str] = None,
        force: bool = False,
    ) -> bool:
        with self._lock:
            session = self._sessions_by_tunnel.get(tunnel_id)
        if not session:
            self._service_log_event(
                "vpn_tunnel_disconnect_missing tunnel_id={0} reason={1}".format(tunnel_id or "-", reason or "-")
            )
            return False
        return self.disconnect(session.agent_id, reason=reason, operator_id=operator_id, force=force)

    def _emit_start(self, payload: Mapping[str, Any]) -> None:
        if not self.socketio:
            return
        agent_id = None
        if isinstance(payload, Mapping):
            agent_id = payload.get("agent_id")
        emit_agent = getattr(self.context, "emit_agent_event", None)
        if agent_id and callable(emit_agent):
            try:
                if emit_agent(agent_id, "vpn_tunnel_start", payload):
                    self._service_log_event(
                        "vpn_tunnel_start_emit agent_id={0} transport=direct".format(agent_id or "-")
                    )
                    return
            except Exception:
                self.logger.debug("emit_agent_event failed for vpn_tunnel_start", exc_info=True)
                self._service_log_event(
                    "vpn_tunnel_start_emit_failed agent_id={0} transport=direct".format(agent_id or "-"),
                    level="WARNING",
                )
        try:
            self._service_log_event(
                "vpn_tunnel_start_emit agent_id={0} transport=broadcast".format(agent_id or "-")
            )
            self.socketio.emit("vpn_tunnel_start", payload, namespace="/")
        except Exception:
            self.logger.debug("vpn_tunnel_start emit failed", exc_info=True)
            self._service_log_event(
                "vpn_tunnel_start_emit_failed agent_id={0} transport=broadcast".format(agent_id or "-"),
                level="WARNING",
            )

    def _emit_stop(self, session: VpnSession, reason: str) -> None:
        if not self.socketio:
            return
        emit_agent = getattr(self.context, "emit_agent_event", None)
        if callable(emit_agent):
            try:
                if emit_agent(
                    session.agent_id,
                    "vpn_tunnel_stop",
                    {"agent_id": session.agent_id, "tunnel_id": session.tunnel_id, "reason": reason},
                ):
                    self._service_log_event(
                        "vpn_tunnel_stop_emit agent_id={0} tunnel_id={1} transport=direct".format(
                            session.agent_id,
                            session.tunnel_id,
                        )
                    )
                    return
            except Exception:
                self.logger.debug("emit_agent_event failed for vpn_tunnel_stop", exc_info=True)
                self._service_log_event(
                    "vpn_tunnel_stop_emit_failed agent_id={0} tunnel_id={1} transport=direct".format(
                        session.agent_id,
                        session.tunnel_id,
                    ),
                    level="WARNING",
                )
        try:
            self._service_log_event(
                "vpn_tunnel_stop_emit agent_id={0} tunnel_id={1} transport=broadcast".format(
                    session.agent_id,
                    session.tunnel_id,
                )
            )
            self.socketio.emit(
                "vpn_tunnel_stop",
                {"agent_id": session.agent_id, "tunnel_id": session.tunnel_id, "reason": reason},
                namespace="/",
            )
        except Exception:
            self.logger.debug("vpn_tunnel_stop emit failed", exc_info=True)
            self._service_log_event(
                "vpn_tunnel_stop_emit_failed agent_id={0} tunnel_id={1} transport=broadcast".format(
                    session.agent_id,
                    session.tunnel_id,
                ),
                level="WARNING",
            )

    def _log_device_activity(self, session: VpnSession, *, event: str, reason: Optional[str] = None) -> None:
        hostname = session.hostname
        conn = None
        try:
            if not hostname and self.db_conn_factory is not None:
                conn = self.db_conn_factory()
                cur = conn.cursor()
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
            self.activity_logger.info(
                "device_activity_suppressed event=%s hostname=%s agent_id=%s tunnel_id=%s operator=%s reason=%s",
                event,
                hostname or "-",
                session.agent_id,
                session.tunnel_id,
                ",".join(sorted(filter(None, session.operator_ids))) or "-",
                reason or "-",
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
        endpoint_host = session.endpoint_host or str(self._engine_ip.ip)
        endpoint_host = self._format_endpoint_host(endpoint_host)
        payload: Dict[str, Any] = {
            "tunnel_id": session.tunnel_id,
            "agent_id": session.agent_id,
            "virtual_ip": session.virtual_ip,
            "engine_virtual_ip": str(self._engine_ip.ip),
            "allowed_ips": f"{self._engine_ip.ip}/32",
            "endpoint": f"{endpoint_host}:{self.context.wireguard_port}",
            "server_public_key": self.wg.server_public_key,
            "client_public_key": session.client_public_key,
            "client_private_key": session.client_private_key,
            "idle_seconds": 0 if self.persistent else self.idle_seconds,
            "allowed_ports": list(session.allowed_ports),
            "connected_operators": len([o for o in session.operator_ids if o]),
        }
        if include_token:
            payload["token"] = session.token
        return payload

    def _session_summary(self, session: VpnSession) -> Mapping[str, Any]:
        endpoint_host = session.endpoint_host or str(self._engine_ip.ip)
        endpoint_host = self._format_endpoint_host(endpoint_host)
        return {
            "tunnel_id": session.tunnel_id,
            "agent_id": session.agent_id,
            "virtual_ip": session.virtual_ip,
            "engine_virtual_ip": str(self._engine_ip.ip),
            "endpoint": f"{endpoint_host}:{self.context.wireguard_port}",
            "allowed_ports": list(session.allowed_ports),
            "connected_operators": len([o for o in session.operator_ids if o]),
            "created_at": int(session.created_at),
            "created_at_iso": self._ts_to_iso(session.created_at),
            "last_activity": int(session.last_activity),
            "last_activity_iso": self._ts_to_iso(session.last_activity),
            "expires_at": int(session.expires_at),
            "expires_at_iso": self._ts_to_iso(session.expires_at),
            "idle_seconds": 0 if self.persistent else self.idle_seconds,
            "status": "up",
        }
