# ======================================================
# Data\Engine\services\WebSocket\Agent\ReverseTunnel.py
# Description: Async reverse tunnel scaffolding (Engine side) providing lease management, domain limits, and placeholders for WebSocket listeners.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine-side reverse tunnel scaffolding.

This module lays down the lease manager and configuration surface for the
Agent reverse tunnel without wiring listeners into the runtime.  It preserves
the existing Socket.IO control plane while preparing async WebSocket
infrastructure to serve per-agent reverse tunnels.
"""
from __future__ import annotations

import asyncio
import base64
import json
import logging
import secrets
import ssl
import struct
import time
from dataclasses import dataclass, field
from logging.handlers import TimedRotatingFileHandler
from pathlib import Path
from typing import Callable, Deque, Dict, Iterable, List, Optional, Tuple
from collections import deque
from threading import Thread

from .ReverseTunnelProtocols import PowershellChannelServer

try:  # websockets is added to engine requirements
    import websockets
    from websockets.server import serve as ws_serve
except Exception:  # pragma: no cover - dependency resolved at runtime
    websockets = None
    ws_serve = None

from ....server import EngineContext

TunnelState = str


def _utc_ts() -> float:
    return time.time()


def _generate_tunnel_id() -> str:
    # UUID4-like, but defer to secrets for a short scaffold without adding deps.
    hex_blob = secrets.token_hex(16)
    return f"{hex_blob[0:8]}-{hex_blob[8:12]}-{hex_blob[12:16]}-{hex_blob[16:20]}-{hex_blob[20:32]}"


class FrameDecodeError(Exception):
    """Raised when an incoming frame is malformed."""


class FrameValidationError(Exception):
    """Raised when a frame fails validation."""


# Message types
MSG_CONNECT = 0x01
MSG_CONNECT_ACK = 0x02
MSG_CHANNEL_OPEN = 0x03
MSG_CHANNEL_ACK = 0x04
MSG_DATA = 0x05
MSG_WINDOW_UPDATE = 0x06
MSG_HEARTBEAT = 0x07
MSG_CLOSE = 0x08
MSG_CONTROL = 0x09

# Close codes
CLOSE_OK = 0
CLOSE_IDLE_TIMEOUT = 1
CLOSE_GRACE_EXPIRED = 2
CLOSE_PROTOCOL_ERROR = 3
CLOSE_AUTH_FAILED = 4
CLOSE_SERVER_SHUTDOWN = 5
CLOSE_AGENT_SHUTDOWN = 6
CLOSE_DOMAIN_LIMIT = 7
CLOSE_UNEXPECTED_DISCONNECT = 8

FRAME_HEADER_STRUCT = struct.Struct("<BBBBII")  # version, msg_type, flags, reserved, channel_id, length
FRAME_VERSION = 1


@dataclass
class TunnelFrame:
    """Decoded tunnel frame."""

    msg_type: int
    channel_id: int
    payload: bytes = field(default_factory=bytes)
    flags: int = 0
    version: int = FRAME_VERSION
    reserved: int = 0

    def encode(self) -> bytes:
        payload_len = len(self.payload or b"")
        header = FRAME_HEADER_STRUCT.pack(
            self.version,
            self.msg_type,
            self.flags,
            self.reserved,
            int(self.channel_id),
            payload_len,
        )
        return header + (self.payload or b"")


def decode_frame(buffer: bytes) -> TunnelFrame:
    """Decode a single tunnel frame from bytes."""

    if len(buffer) < FRAME_HEADER_STRUCT.size:
        raise FrameDecodeError("frame_too_small")
    try:
        version, msg_type, flags, reserved, channel_id, length = FRAME_HEADER_STRUCT.unpack_from(buffer, 0)
    except struct.error as exc:
        raise FrameDecodeError(f"frame_unpack_error:{exc}") from exc

    if version != FRAME_VERSION:
        raise FrameValidationError(f"unsupported_version:{version}")
    if length < 0:
        raise FrameValidationError("invalid_length")
    expected_total = FRAME_HEADER_STRUCT.size + length
    if len(buffer) < expected_total:
        raise FrameDecodeError("incomplete_frame")
    payload = buffer[FRAME_HEADER_STRUCT.size : expected_total]
    if len(payload) != length:
        raise FrameValidationError("length_mismatch")

    return TunnelFrame(
        version=version,
        msg_type=msg_type,
        flags=flags,
        reserved=reserved,
        channel_id=channel_id,
        payload=payload,
    )


def heartbeat_frame(channel_id: int = 0, *, is_ack: bool = False) -> TunnelFrame:
    """Build a heartbeat ping/pong frame."""

    flags = 0x1 if is_ack else 0x0
    return TunnelFrame(msg_type=MSG_HEARTBEAT, channel_id=channel_id, flags=flags, payload=b"")


def close_frame(channel_id: int, code: int, reason: str = "") -> TunnelFrame:
    payload = json.dumps({"code": code, "reason": reason}, separators=(",", ":")).encode("utf-8")
    return TunnelFrame(msg_type=MSG_CLOSE, channel_id=channel_id, payload=payload)


def _build_tunnel_logger(log_path: Path) -> logging.Logger:
    """Create a dedicated reverse tunnel logger with daily rotation."""

    try:
        log_path.parent.mkdir(parents=True, exist_ok=True)
    except Exception:
        pass

    logger = logging.getLogger("borealis.engine.reverse_tunnel")
    if not logger.handlers:
        formatter = logging.Formatter("%(asctime)s-%(name)s-%(levelname)s: %(message)s")
        handler = TimedRotatingFileHandler(str(log_path), when="midnight", backupCount=0, encoding="utf-8")
        handler.setFormatter(formatter)
        logger.addHandler(handler)
    logger.setLevel(logging.INFO)
    logger.propagate = False
    return logger


@dataclass
class TunnelLease:
    tunnel_id: str
    agent_id: str
    domain: str
    protocol: str
    operator_id: Optional[str]
    assigned_port: int
    token: Optional[str] = None
    hostname: Optional[str] = None
    activity_id: Optional[int] = None
    created_at: float = field(default_factory=_utc_ts)
    expires_at: Optional[float] = None
    idle_timeout_seconds: int = 3600
    grace_timeout_seconds: int = 3600
    state: TunnelState = "pending"
    last_activity_ts: float = field(default_factory=_utc_ts)
    agent_connected_at: Optional[float] = None
    agent_disconnected_at: Optional[float] = None

    def mark_active(self) -> None:
        self.state = "active"
        self.agent_connected_at = _utc_ts()
        self.last_activity_ts = self.agent_connected_at

    def mark_disconnected(self) -> None:
        self.agent_disconnected_at = _utc_ts()
        self.last_activity_ts = self.agent_disconnected_at

    def touch(self) -> None:
        self.last_activity_ts = _utc_ts()

    def mark_closing(self) -> None:
        self.state = "closing"

    def mark_expired(self) -> None:
        self.state = "expired"

    def to_summary(self) -> Dict[str, object]:
        return {
            "tunnel_id": self.tunnel_id,
            "agent_id": self.agent_id,
            "domain": self.domain,
            "protocol": self.protocol,
            "operator_id": self.operator_id,
            "assigned_port": self.assigned_port,
            "state": self.state,
            "created_at": self.created_at,
            "expires_at": self.expires_at,
            "idle_timeout_seconds": self.idle_timeout_seconds,
            "grace_timeout_seconds": self.grace_timeout_seconds,
            "last_activity_ts": self.last_activity_ts,
            "agent_connected_at": self.agent_connected_at,
            "agent_disconnected_at": self.agent_disconnected_at,
        }


class DomainPolicy:
    """Enforce per-domain concurrency and defaults."""

    DEFAULT_LIMITS = {
        "ps": 1,
        "rdp": 1,
        "vnc": 1,
        "webrtc": 1,
        "ssh": None,  # Unlimited
        "winrm": None,  # Unlimited
    }

    def __init__(self, overrides: Optional[Dict[str, Optional[int]]] = None):
        merged = dict(self.DEFAULT_LIMITS)
        if overrides:
            merged.update(overrides)
        self.limits = merged

    def is_allowed(self, domain: str, active_count: int) -> bool:
        limit = self.limits.get(domain)
        if limit is None:
            return True
        return active_count < limit


class PortAllocator:
    """Simple round-robin port allocator with reuse tracking."""

    def __init__(self, start: int, end: int):
        if start < 1 or end > 65535 or start > end:
            raise ValueError("Invalid port range")
        self.start = start
        self.end = end
        self._next = start
        self._in_use: Dict[int, str] = {}

    def allocate(self, tunnel_id: str) -> Optional[int]:
        for _ in range(self.start, self.end + 1):
            candidate = self._next
            self._next += 1
            if self._next > self.end:
                self._next = self.start
            if candidate in self._in_use:
                continue
            self._in_use[candidate] = tunnel_id
            return candidate
        return None

    def release(self, port: int) -> None:
        self._in_use.pop(port, None)

    def in_use(self) -> Dict[int, str]:
        return dict(self._in_use)


class TunnelLeaseManager:
    """DHCP-like lease manager for reverse tunnels (Engine side)."""

    def __init__(
        self,
        *,
        port_range: Tuple[int, int],
        idle_timeout_seconds: int,
        grace_timeout_seconds: int,
        domain_policy: Optional[DomainPolicy] = None,
        logger: Optional[logging.Logger] = None,
    ):
        self._allocator = PortAllocator(port_range[0], port_range[1])
        self.idle_timeout_seconds = idle_timeout_seconds
        self.grace_timeout_seconds = grace_timeout_seconds
        self.domain_policy = domain_policy or DomainPolicy()
        self.logger = logger or logging.getLogger("borealis.engine.tunnel.lease")
        self._leases: Dict[str, TunnelLease] = {}

    def _active_for_agent_domain(self, agent_id: str, domain: str) -> int:
        active_states = {"pending", "active", "closing"}
        return sum(
            1
            for lease in self._leases.values()
            if lease.agent_id == agent_id and lease.domain == domain and lease.state in active_states
        )

    def allocate(
        self,
        *,
        agent_id: str,
        protocol: str,
        domain: str,
        operator_id: Optional[str],
        token: Optional[str] = None,
    ) -> TunnelLease:
        in_domain = self._active_for_agent_domain(agent_id, domain)
        if not self.domain_policy.is_allowed(domain, in_domain):
            raise RuntimeError(f"domain_limit:{domain}")

        tunnel_id = _generate_tunnel_id()
        port = self._allocator.allocate(tunnel_id)
        if port is None:
            raise RuntimeError("port_pool_exhausted")

        now_ts = _utc_ts()
        lease = TunnelLease(
            tunnel_id=tunnel_id,
            agent_id=agent_id,
            domain=domain,
            protocol=protocol,
            operator_id=operator_id,
            assigned_port=port,
            token=token,
            created_at=now_ts,
            expires_at=now_ts + self.grace_timeout_seconds,
            idle_timeout_seconds=self.idle_timeout_seconds,
            grace_timeout_seconds=self.grace_timeout_seconds,
            state="pending",
            last_activity_ts=now_ts,
        )
        self._leases[tunnel_id] = lease
        self.logger.info(
            "lease_allocated tunnel_id=%s agent_id=%s domain=%s protocol=%s port=%s",
            tunnel_id,
            agent_id,
            domain,
            protocol,
            port,
        )
        return lease

    def release(self, tunnel_id: str, *, reason: str = "released") -> None:
        lease = self._leases.pop(tunnel_id, None)
        if lease is None:
            return
        self._allocator.release(lease.assigned_port)
        self.logger.info(
            "lease_released tunnel_id=%s agent_id=%s port=%s reason=%s",
            tunnel_id,
            lease.agent_id,
            lease.assigned_port,
            reason,
        )

    def get(self, tunnel_id: str) -> Optional[TunnelLease]:
        return self._leases.get(tunnel_id)

    def touch(self, tunnel_id: str) -> None:
        lease = self._leases.get(tunnel_id)
        if lease:
            lease.touch()

    def mark_agent_connected(self, tunnel_id: str) -> None:
        lease = self._leases.get(tunnel_id)
        if lease:
            lease.mark_active()

    def mark_agent_disconnected(self, tunnel_id: str) -> None:
        lease = self._leases.get(tunnel_id)
        if lease:
            lease.mark_disconnected()

    def expire_idle(self, *, now_ts: Optional[float] = None) -> List[TunnelLease]:
        now = now_ts or _utc_ts()
        expired: List[TunnelLease] = []
        for lease in list(self._leases.values()):
            if lease.state == "expired":
                continue

            idle_age = now - lease.last_activity_ts
            if lease.state == "active" and idle_age >= lease.idle_timeout_seconds:
                lease.mark_expired()
                expired.append(lease)
                self.release(lease.tunnel_id, reason="idle_timeout")
                continue

            if lease.agent_disconnected_at:
                grace_age = now - lease.agent_disconnected_at
                if grace_age >= lease.grace_timeout_seconds:
                    lease.mark_expired()
                    expired.append(lease)
                    self.release(lease.tunnel_id, reason="grace_expired")
                    continue
        return expired

    def all_leases(self) -> Iterable[TunnelLease]:
        return list(self._leases.values())


class ReverseTunnelService:
    """Placeholder for the async tunnel listener and bridge wiring."""

    def __init__(
        self,
        context: EngineContext,
        *,
        signer: Optional[object] = None,
        db_conn_factory: Optional[Callable[[], object]] = None,
        socketio: Optional[object] = None,
    ):
        self.context = context
        self.logger = context.logger.getChild("tunnel.service")
        self.audit_logger = _build_tunnel_logger(Path(context.reverse_tunnel_log_path))
        self.lease_manager = TunnelLeaseManager(
            port_range=context.reverse_tunnel_port_range,
            idle_timeout_seconds=context.reverse_tunnel_idle_timeout_seconds,
            grace_timeout_seconds=context.reverse_tunnel_grace_timeout_seconds,
            logger=self.audit_logger.getChild("lease_manager"),
        )
        self._activity_logger = self.audit_logger.getChild("device_activity")
        self._db_conn_factory = db_conn_factory
        self._socketio = socketio
        self.fixed_port = context.reverse_tunnel_fixed_port
        self.heartbeat_seconds = context.reverse_tunnel_heartbeat_seconds
        self.log_path = Path(context.reverse_tunnel_log_path)
        self._loop: Optional[asyncio.AbstractEventLoop] = None
        self._loop_thread: Optional[Thread] = None
        self._running = False
        self._sweeper_task: Optional[asyncio.Future] = None
        self.signer = signer
        self._bridges: Dict[str, "TunnelBridge"] = {}
        self._port_servers: Dict[int, asyncio.AbstractServer] = {}
        self._agent_sockets: Dict[str, "websockets.WebSocketServerProtocol"] = {}
        self._ps_servers: Dict[str, PowershellChannelServer] = {}

    def _ensure_loop(self) -> None:
        if self._running and self._loop:
            return
        self._loop = asyncio.new_event_loop()
        self._running = True

        def _runner():
            asyncio.set_event_loop(self._loop)
            self.logger.info(
                "Reverse tunnel event loop started (fixed_port=%s port_range=%s-%s)",
                self.fixed_port,
                self.lease_manager._allocator.start,
                self.lease_manager._allocator.end,
            )
            self._loop.run_forever()

        self._loop_thread = Thread(target=_runner, name="reverse-tunnel-loop", daemon=True)
        self._loop_thread.start()
        self._start_lease_sweeper()

    def start(self) -> None:
        """Start the tunnel service loop."""

        if self._running:
            return
        self._ensure_loop()

    def stop(self) -> None:
        """Stop the tunnel service and release leases."""

        if not self._running:
            return
        for server in list(self._port_servers.values()):
            try:
                server.close()
            except Exception:
                pass
        self._port_servers.clear()
        for websocket in list(self._agent_sockets.values()):
            try:
                self._loop.call_soon_threadsafe(asyncio.create_task, websocket.close())
            except Exception:
                pass
        for lease in list(self.lease_manager.all_leases()):
            self.lease_manager.release(lease.tunnel_id, reason="service_stop")
        if self._sweeper_task:
            try:
                self._sweeper_task.cancel()
            except Exception:
                pass
        self._running = False
        if self._loop:
            self._loop.call_soon_threadsafe(self._loop.stop)
        self.logger.info("Reverse tunnel service stopped.")

    async def start_listener(self) -> None:
        """Placeholder async listener hook (no sockets yet)."""

        if not self._running:
            self.start()
        self.logger.debug("Reverse tunnel async listener placeholder running (no sockets bound).")

    async def handle_agent_connect(self, tunnel_id: str, token: str) -> TunnelBridge:
        """Validate agent token and attach to bridge (socket handling TBD)."""

        lease = self.lease_manager.get(tunnel_id)
        if lease is None:
            raise ValueError("unknown_tunnel")
        bridge = self.ensure_bridge(lease)
        bridge.attach_agent(token)
        return bridge

    async def handle_operator_connect(self, tunnel_id: str, operator_id: Optional[str]) -> TunnelBridge:
        """Attach operator to bridge (socket handling TBD)."""

        lease = self.lease_manager.get(tunnel_id)
        if lease is None:
            raise ValueError("unknown_tunnel")
        bridge = self.ensure_bridge(lease)
        bridge.attach_operator(operator_id)
        return bridge

    def agent_attach(self, tunnel_id: str, token: str) -> TunnelBridge:
        """Synchronous wrapper for agent attachment."""

        lease = self.lease_manager.get(tunnel_id)
        if lease is None:
            raise ValueError("unknown_tunnel")
        bridge = self.ensure_bridge(lease)
        bridge.attach_agent(token)
        return bridge

    def operator_attach(self, tunnel_id: str, operator_id: Optional[str]) -> TunnelBridge:
        """Synchronous wrapper for operator attachment."""

        lease = self.lease_manager.get(tunnel_id)
        if lease is None:
            raise ValueError("unknown_tunnel")
        bridge = self.ensure_bridge(lease)
        bridge.attach_operator(operator_id)
        if lease.domain.lower() == "ps":
            try:
                server = self.ensure_ps_server(tunnel_id)
                if server:
                    server.open_channel()
            except Exception:
                self.logger.debug("ps server open failed tunnel_id=%s", tunnel_id, exc_info=True)
        return bridge

    def _encode_token(self, payload: Dict[str, object]) -> str:
        """Encode a short-lived token binding the lease fields."""

        payload_bytes = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
        payload_b64 = base64.urlsafe_b64encode(payload_bytes).decode("ascii").rstrip("=")
        if self.signer:
            try:
                signature = self.signer.sign(payload_bytes)
                sig_b64 = base64.urlsafe_b64encode(signature).decode("ascii").rstrip("=")
                return f"{payload_b64}.{sig_b64}"
            except Exception:
                self.logger.debug("Reverse tunnel token signing failed; returning unsigned token", exc_info=True)
        return payload_b64

    def request_lease(
        self,
        *,
        agent_id: str,
        protocol: str,
        domain: str,
        operator_id: Optional[str],
    ) -> TunnelLease:
        self._ensure_loop()
        lease = self.lease_manager.allocate(
            agent_id=agent_id,
            protocol=protocol,
            domain=domain,
            operator_id=operator_id,
        )
        lease.token = self.issue_token(lease)
        self._spawn_port_listener(lease.assigned_port)
        self.audit_logger.info(
            "lease_created tunnel_id=%s agent_id=%s domain=%s protocol=%s port=%s operator=%s",
            lease.tunnel_id,
            lease.agent_id,
            lease.domain,
            lease.protocol,
            lease.assigned_port,
            operator_id or "-",
        )
        return lease

    def issue_token(self, lease: TunnelLease) -> str:
        expires_at = lease.created_at + lease.grace_timeout_seconds
        payload = {
            "agent_id": lease.agent_id,
            "tunnel_id": lease.tunnel_id,
            "assigned_port": lease.assigned_port,
            "protocol": lease.protocol,
            "domain": lease.domain,
            "expires_at": int(expires_at),
            "issued_at": int(lease.created_at),
        }
        token = self._encode_token(payload)
        lease.token = token
        lease.expires_at = expires_at
        return token

    def lease_summary(self, lease: TunnelLease) -> Dict[str, object]:
        return {
            "tunnel_id": lease.tunnel_id,
            "agent_id": lease.agent_id,
            "protocol": lease.protocol,
            "domain": lease.domain,
            "port": lease.assigned_port,
            "token": lease.token,
            "expires_at": lease.expires_at,
            "idle_seconds": lease.idle_timeout_seconds,
            "grace_seconds": lease.grace_timeout_seconds,
            "state": lease.state,
        }

    def decode_token(self, token: str) -> Dict[str, object]:
        """Decode and optionally verify a tunnel token (unsigned tokens allowed)."""

        if not token:
            raise ValueError("token_missing")

        def _b64decode(segment: str) -> bytes:
            padding = "=" * (-len(segment) % 4)
            return base64.urlsafe_b64decode(segment + padding)

        parts = token.split(".")
        payload_segment = parts[0]
        payload_bytes = _b64decode(payload_segment)
        try:
            payload = json.loads(payload_bytes.decode("utf-8"))
        except Exception as exc:
            raise ValueError("token_decode_error") from exc

        # Optional signature verification if present and signer is available.
        if len(parts) == 2 and self.signer:
            sig_segment = parts[1]
            try:
                signature = _b64decode(sig_segment)
            except Exception as exc:
                raise ValueError("token_signature_decode_error") from exc
            public_key = getattr(self.signer, "_public", None)
            if public_key:
                try:
                    public_key.verify(signature, payload_bytes)
                except Exception as exc:
                    raise ValueError("token_signature_invalid") from exc

        return payload

    def validate_token(
        self,
        token: str,
        *,
        agent_id: Optional[str] = None,
        tunnel_id: Optional[str] = None,
        domain: Optional[str] = None,
        protocol: Optional[str] = None,
    ) -> Dict[str, object]:
        """Validate a tunnel token against expected fields and expiry."""

        payload = self.decode_token(token)
        now = int(_utc_ts())

        def _matches(expected: Optional[str], actual: Optional[str]) -> bool:
            if expected is None:
                return True
            return str(expected).strip().lower() == str(actual or "").strip().lower()

        if not _matches(agent_id, payload.get("agent_id")):
            raise ValueError("token_agent_mismatch")
        if not _matches(tunnel_id, payload.get("tunnel_id")):
            raise ValueError("token_id_mismatch")
        if not _matches(domain, payload.get("domain")):
            raise ValueError("token_domain_mismatch")
        if not _matches(protocol, payload.get("protocol")):
            raise ValueError("token_protocol_mismatch")

        expires_at = payload.get("expires_at")
        try:
            expires_ts = int(expires_at) if expires_at is not None else None
        except Exception:
            expires_ts = None
        if expires_ts is not None and expires_ts < now:
            raise ValueError("token_expired")

        return payload

    def log_device_activity(
        self,
        lease: TunnelLease,
        *,
        event: str,
        reason: Optional[str] = None,
    ) -> None:
        """Device Activity logging for tunnel start/stop (DB + socket emit if available)."""

        agent_id = lease.agent_id
        operator_id = lease.operator_id
        tunnel_id = lease.tunnel_id

        if self._db_conn_factory is None:
            self._activity_logger.info(
                "device_activity event=%s agent_id=%s tunnel_id=%s operator=%s reason=%s",
                event,
                agent_id,
                tunnel_id,
                operator_id or "-",
                reason or "-",
            )
            return

        conn = None
        try:
            conn = self._db_conn_factory()
            cur = conn.cursor()

            hostname = lease.hostname
            if not hostname:
                try:
                    cur.execute(
                        "SELECT hostname FROM devices WHERE agent_id = ? ORDER BY last_seen DESC LIMIT 1",
                        (agent_id,),
                    )
                    row = cur.fetchone()
                    if row and row[0]:
                        hostname = str(row[0]).strip()
                        lease.hostname = hostname
                except Exception:
                    hostname = None

            if not hostname:
                self._activity_logger.info(
                    "device_activity event=%s agent_id=%s tunnel_id=%s operator=%s reason=%s hostname=unknown",
                    event,
                    agent_id,
                    tunnel_id,
                    operator_id or "-",
                    reason or "-",
                )
                return

            now_ts = int(_utc_ts())
            script_name = f"Reverse Tunnel ({lease.domain}/{lease.protocol})"

            if event == "start":
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        hostname,
                        lease.tunnel_id,
                        script_name,
                        "reverse_tunnel",
                        now_ts,
                        "Running",
                        "",
                        "",
                    ),
                )
                lease.activity_id = cur.lastrowid
                conn.commit()
                if self._socketio:
                    try:
                        self._socketio.emit(
                            "device_activity_changed",
                            {
                                "hostname": hostname,
                                "activity_id": lease.activity_id,
                                "change": "created",
                                "source": "reverse_tunnel",
                            },
                        )
                    except Exception:
                        pass
                self._activity_logger.info(
                    "device_activity_start hostname=%s agent_id=%s tunnel_id=%s operator=%s activity_id=%s",
                    hostname,
                    agent_id,
                    tunnel_id,
                    operator_id or "-",
                    lease.activity_id or "-",
                )
                return

            if lease.activity_id:
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
                        lease.activity_id,
                    ),
                )
                conn.commit()
                if self._socketio:
                    try:
                        self._socketio.emit(
                            "device_activity_changed",
                            {
                                "hostname": hostname,
                                "activity_id": lease.activity_id,
                                "change": "updated",
                                "source": "reverse_tunnel",
                            },
                        )
                    except Exception:
                        pass
            self._activity_logger.info(
                "device_activity event=%s hostname=%s agent_id=%s tunnel_id=%s operator=%s reason=%s activity_id=%s",
                event,
                hostname,
                agent_id,
                tunnel_id,
                operator_id or "-",
                reason or "-",
                lease.activity_id or "-",
            )
        except Exception:
            self._activity_logger.debug("device_activity logging failed for tunnel_id=%s", lease.tunnel_id, exc_info=True)
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def _dispatch_agent_frame(self, tunnel_id: str, frame: TunnelFrame) -> None:
        server = self._ps_servers.get(tunnel_id)
        if not server:
            return
        try:
            server.handle_agent_frame(frame)
        except Exception:
            self.logger.debug("ps handler error for tunnel_id=%s", tunnel_id, exc_info=True)

    def _start_lease_sweeper(self) -> None:
        async def _sweeper():
            while self._running and self._loop and not self._loop.is_closed():
                await asyncio.sleep(15)
                expired = self.lease_manager.expire_idle()
                for lease in expired:
                    self.log_device_activity(lease, event="stop", reason="idle_or_grace")
        if self._loop:
            self._sweeper_task = asyncio.run_coroutine_threadsafe(_sweeper(), self._loop)

    def _build_ssl_context(self) -> Optional[ssl.SSLContext]:
        cert = self.context.tls_cert_path or self.context.tls_bundle_path
        key = self.context.tls_key_path
        if not cert or not key:
            return None
        try:
            ctx = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
            ctx.load_cert_chain(certfile=cert, keyfile=key)
            return ctx
        except Exception:
            self.logger.debug("Failed to build SSL context for reverse tunnel listener", exc_info=True)
            return None

    def _spawn_port_listener(self, port: int) -> None:
        if ws_serve is None:
            self.logger.error("websockets dependency missing; cannot start tunnel listener")
            return
        if port in self._port_servers:
            return
        ssl_ctx = self._build_ssl_context()

        async def _handler(websocket, path):
            await self._handle_agent_socket(websocket, path, port=port)

        async def _start():
            server = await ws_serve(_handler, host="0.0.0.0", port=port, ssl=ssl_ctx, max_size=None, ping_interval=None)
            self._port_servers[port] = server

        asyncio.run_coroutine_threadsafe(_start(), self._loop)

    async def _handle_agent_socket(self, websocket, path: str, *, port: int) -> None:
        """Handle agent tunnel socket on assigned port."""

        tunnel_id = None
        try:
            raw = await asyncio.wait_for(websocket.recv(), timeout=10)
            frame = decode_frame(raw)
            if frame.msg_type != MSG_CONNECT:
                await websocket.close()
                return
            try:
                payload = json.loads(frame.payload.decode("utf-8"))
            except Exception:
                await websocket.close()
                return
            tunnel_id = str(payload.get("tunnel_id") or "").strip()
            agent_id = str(payload.get("agent_id") or "").strip()
            token = payload.get("token") or ""
            lease = self.lease_manager.get(tunnel_id)
            if lease is None or lease.assigned_port != port:
                await websocket.close()
                return
            # Token validation
            self.validate_token(
                token,
                agent_id=agent_id,
                tunnel_id=tunnel_id,
                domain=lease.domain,
                protocol=lease.protocol,
            )
            bridge = self.ensure_bridge(lease)
            bridge.attach_agent(token)
            self._agent_sockets[tunnel_id] = websocket
            await websocket.send(heartbeat_frame(channel_id=0, is_ack=True).encode())
            await websocket.send(TunnelFrame(msg_type=MSG_CONNECT_ACK, channel_id=0, payload=b"").encode())

            async def _pump_to_operator():
                while not websocket.closed:
                    try:
                        raw_msg = await websocket.recv()
                    except Exception:
                        break
                    try:
                        recv_frame = decode_frame(raw_msg)
                    except Exception:
                        continue
                    self.lease_manager.touch(tunnel_id)
                    try:
                        self._dispatch_agent_frame(tunnel_id, recv_frame)
                    except Exception:
                        pass
                    bridge.agent_to_operator(recv_frame)
            async def _pump_to_agent():
                while not websocket.closed:
                    frame = bridge.next_for_agent()
                    if frame is None:
                        await asyncio.sleep(0.05)
                        continue
                    try:
                        await websocket.send(frame.encode())
                    except Exception:
                        break
            async def _heartbeat():
                while not websocket.closed:
                    try:
                        await websocket.send(heartbeat_frame(channel_id=0).encode())
                    except Exception:
                        break
                    await asyncio.sleep(self.heartbeat_seconds)

            consumer = asyncio.create_task(_pump_to_operator())
            producer = asyncio.create_task(_pump_to_agent())
            heart = asyncio.create_task(_heartbeat())
            await asyncio.wait([consumer, producer, heart], return_when=asyncio.FIRST_COMPLETED)
        except Exception:
            self.logger.debug("Agent socket handler failed on port %s", port, exc_info=True)
        finally:
            if tunnel_id and tunnel_id in self._agent_sockets:
                self._agent_sockets.pop(tunnel_id, None)
            if tunnel_id:
                self.release_bridge(tunnel_id, reason="agent_socket_closed")

    def get_bridge(self, tunnel_id: str) -> Optional["TunnelBridge"]:
        return self._bridges.get(tunnel_id)

    def ensure_bridge(self, lease: TunnelLease) -> "TunnelBridge":
        bridge = self._bridges.get(lease.tunnel_id)
        if bridge is None:
            bridge = TunnelBridge(lease=lease, service=self)
            self._bridges[lease.tunnel_id] = bridge
        return bridge

    def ensure_ps_server(self, tunnel_id: str) -> Optional[PowershellChannelServer]:
        server = self._ps_servers.get(tunnel_id)
        if server:
            return server
        lease = self.lease_manager.get(tunnel_id)
        if lease is None or (lease.domain or "").lower() != "ps":
            return None
        bridge = self.ensure_bridge(lease)
        server = PowershellChannelServer(
            bridge=bridge,
            service=self,
            frame_cls=TunnelFrame,
            close_frame_fn=close_frame,
        )
        self._ps_servers[tunnel_id] = server
        return server

    def get_ps_server(self, tunnel_id: str) -> Optional[PowershellChannelServer]:
        return self._ps_servers.get(tunnel_id)

    def release_bridge(self, tunnel_id: str, *, reason: str = "bridge_released") -> None:
        bridge = self._bridges.pop(tunnel_id, None)
        if bridge:
            bridge.stop(reason=reason)
        if tunnel_id in self._ps_servers:
            try:
                self._ps_servers.pop(tunnel_id, None)
            except Exception:
                pass


class TunnelBridge:
    """Lightweight placeholder for mapping agent and operator sockets."""

    def __init__(self, *, lease: TunnelLease, service: ReverseTunnelService):
        self.lease = lease
        self.service = service
        self.logger = service.logger.getChild(f"bridge.{lease.tunnel_id}")
        self.agent_connected = False
        self.operator_attached = False
        self._agent_queue: Deque[TunnelFrame] = deque()
        self._operator_queue: Deque[TunnelFrame] = deque()
        self._closed = False

    def attach_agent(self, token: str) -> None:
        """Validate the agent token and mark the lease active (no socket binding yet)."""

        self.service.validate_token(
            token,
            agent_id=self.lease.agent_id,
            tunnel_id=self.lease.tunnel_id,
            domain=self.lease.domain,
            protocol=self.lease.protocol,
        )
        self.lease.mark_active()
        self.service.lease_manager.mark_agent_connected(self.lease.tunnel_id)
        self.agent_connected = True
        self.service.log_device_activity(self.lease, event="start")
        self.logger.info("agent_connected tunnel_id=%s agent_id=%s", self.lease.tunnel_id, self.lease.agent_id)

    def attach_operator(self, operator_id: Optional[str]) -> None:
        self.operator_attached = True
        if operator_id:
            self.lease.operator_id = operator_id
        self.logger.info("operator_attached tunnel_id=%s operator=%s", self.lease.tunnel_id, operator_id or "-")

    def stop(self, *, reason: str = "stopped") -> None:
        self.service.lease_manager.release(self.lease.tunnel_id, reason=reason)
        self.service.log_device_activity(self.lease, event="stop", reason=reason)
        self.logger.info(
            "bridge_stopped tunnel_id=%s agent_id=%s reason=%s",
            self.lease.tunnel_id,
            self.lease.agent_id,
            reason,
        )
        self._closed = True

    def agent_to_operator(self, frame: TunnelFrame) -> None:
        """Queue a frame from agent toward operator."""

        if self._closed:
            return
        self._operator_queue.append(frame)

    def operator_to_agent(self, frame: TunnelFrame) -> None:
        """Queue a frame from operator toward agent."""

        if self._closed:
            return
        try:
            self.service.lease_manager.touch(self.lease.tunnel_id)
        except Exception:
            pass
        self._agent_queue.append(frame)

    def next_for_agent(self) -> Optional[TunnelFrame]:
        if self._closed or not self._agent_queue:
            return None
        return self._agent_queue.popleft()

    def next_for_operator(self) -> Optional[TunnelFrame]:
        if self._closed or not self._operator_queue:
            return None
        return self._operator_queue.popleft()


__all__ = [
    "ReverseTunnelService",
    "TunnelLeaseManager",
    "TunnelLease",
    "DomainPolicy",
    "PortAllocator",
    "TunnelBridge",
    "TunnelFrame",
    "decode_frame",
    "heartbeat_frame",
    "close_frame",
    "FrameDecodeError",
    "FrameValidationError",
    "MSG_CONNECT",
    "MSG_CONNECT_ACK",
    "MSG_CHANNEL_OPEN",
    "MSG_CHANNEL_ACK",
    "MSG_DATA",
    "MSG_WINDOW_UPDATE",
    "MSG_HEARTBEAT",
    "MSG_CLOSE",
    "MSG_CONTROL",
    "CLOSE_OK",
    "CLOSE_IDLE_TIMEOUT",
    "CLOSE_GRACE_EXPIRED",
    "CLOSE_PROTOCOL_ERROR",
    "CLOSE_AUTH_FAILED",
    "CLOSE_SERVER_SHUTDOWN",
    "CLOSE_AGENT_SHUTDOWN",
    "CLOSE_DOMAIN_LIMIT",
    "CLOSE_UNEXPECTED_DISCONNECT",
]
