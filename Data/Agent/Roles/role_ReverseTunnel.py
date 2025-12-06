import asyncio
import base64
import importlib.util
import json
import os
import struct
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.parse import urlparse

import aiohttp
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

# Capture import errors for the PowerShell handler so we can report why it is missing.
PS_IMPORT_ERROR: Optional[str] = None
tunnel_Powershell = None
try:
    from .ReverseTunnel import tunnel_Powershell  # type: ignore
except Exception as exc:  # pragma: no cover - best-effort logging only
    PS_IMPORT_ERROR = repr(exc)
    # Try manual import from file to survive non-package execution.
    try:
        _ps_path = Path(__file__).parent / "ReverseTunnel" / "tunnel_Powershell.py"
        if _ps_path.exists():
            spec = importlib.util.spec_from_file_location("tunnel_Powershell", _ps_path)
            if spec and spec.loader:
                module = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(module)  # type: ignore
                tunnel_Powershell = module
                PS_IMPORT_ERROR = None
    except Exception as exc2:  # pragma: no cover - diagnostic only
        PS_IMPORT_ERROR = f"{PS_IMPORT_ERROR} | fallback_load_failed={exc2!r}"

ROLE_NAME = "reverse_tunnel"
ROLE_CONTEXTS = ["interactive", "system"]

# Message types (keep in sync with Engine service)
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

FRAME_VERSION = 1
FRAME_HEADER_STRUCT = struct.Struct("<BBBBII")  # version, msg_type, flags, reserved, channel_id, length


@dataclass
class TunnelFrame:
    msg_type: int
    channel_id: int
    payload: bytes = field(default_factory=bytes)
    flags: int = 0
    version: int = FRAME_VERSION
    reserved: int = 0

    def encode(self) -> bytes:
        payload = self.payload or b""
        header = FRAME_HEADER_STRUCT.pack(
            self.version,
            self.msg_type,
            self.flags,
            self.reserved,
            int(self.channel_id),
            len(payload),
        )
        return header + payload


def decode_frame(raw: bytes) -> TunnelFrame:
    if len(raw) < FRAME_HEADER_STRUCT.size:
        raise ValueError("frame_too_small")
    version, msg_type, flags, reserved, channel_id, length = FRAME_HEADER_STRUCT.unpack_from(raw, 0)
    if version != FRAME_VERSION:
        raise ValueError(f"unsupported_version:{version}")
    if length < 0 or len(raw) < FRAME_HEADER_STRUCT.size + length:
        raise ValueError("invalid_length")
    payload = raw[FRAME_HEADER_STRUCT.size : FRAME_HEADER_STRUCT.size + length]
    if len(payload) != length:
        raise ValueError("length_mismatch")
    return TunnelFrame(msg_type=msg_type, channel_id=channel_id, payload=payload, flags=flags, version=version, reserved=reserved)


def heartbeat_frame(channel_id: int = 0, *, is_ack: bool = False) -> TunnelFrame:
    flags = 0x1 if is_ack else 0x0
    return TunnelFrame(msg_type=MSG_HEARTBEAT, channel_id=channel_id, flags=flags, payload=b"")


def close_frame(channel_id: int, code: int, reason: str = "") -> TunnelFrame:
    payload = json.dumps({"code": code, "reason": reason}, separators=(",", ":")).encode("utf-8")
    return TunnelFrame(msg_type=MSG_CLOSE, channel_id=channel_id, payload=payload)


def _norm_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _is_literal_ip(value: str) -> bool:
    try:
        import ipaddress

        ipaddress.ip_address(value.strip().strip("[]"))
        return True
    except Exception:
        return False


@dataclass
class ActiveTunnel:
    tunnel_id: str
    domain: str
    protocol: str
    port: int
    token: str
    url: str
    heartbeat_seconds: int
    idle_seconds: int
    grace_seconds: int
    expires_at: Optional[int]
    signing_key_hint: Optional[str] = None
    session: Optional[aiohttp.ClientSession] = None
    websocket: Optional[aiohttp.ClientWebSocketResponse] = None
    tasks: list = field(default_factory=list)
    send_queue: asyncio.Queue = field(default_factory=asyncio.Queue)
    channels: Dict[int, Any] = field(default_factory=dict)
    last_activity: float = field(default_factory=lambda: time.time())
    connected: bool = False
    stopping: bool = False
    stop_reason: Optional[str] = None


class BaseChannel:
    """Placeholder channel handler; protocol-specific handlers plug in later."""

    def __init__(self, role: "Role", tunnel: ActiveTunnel, channel_id: int, metadata: Optional[dict]):
        self.role = role
        self.tunnel = tunnel
        self.channel_id = channel_id
        self.metadata = metadata or {}

    async def start(self) -> None:
        # Nothing to prime for placeholder channels.
        return

    async def on_frame(self, frame: TunnelFrame) -> None:
        # Drop frames until protocol module is provided.
        return

    async def stop(self, code: int = CLOSE_OK, reason: str = "") -> None:
        await self.role._send_frame(self.tunnel, close_frame(self.channel_id, code, reason))


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self.sio = ctx.sio
        self.loop = ctx.loop or asyncio.get_event_loop()
        self.hooks = ctx.hooks or {}
        self._http_client_factory = self.hooks.get("http_client")
        self._log_hook = self.hooks.get("log_agent")
        self._active: Dict[str, ActiveTunnel] = {}
        self._domain_claims: Dict[str, str] = {}
        self._domain_limits: Dict[str, Optional[int]] = {
            "ps": 1,
            "rdp": 1,
            "vnc": 1,
            "webrtc": 1,
            "ssh": None,
            "winrm": None,
        }
        self._default_heartbeat = 20
        self._protocol_handlers: Dict[str, Any] = {}
        self._frame_cls = TunnelFrame
        self.close_frame = close_frame
        try:
            if tunnel_Powershell and hasattr(tunnel_Powershell, "PowershellChannel"):
                self._protocol_handlers["ps"] = tunnel_Powershell.PowershellChannel
                module_path = getattr(tunnel_Powershell, "__file__", None)
                self._log(f"reverse_tunnel ps handler registered (PowershellChannel) module={module_path}")
            else:
                hint = f" import_error={PS_IMPORT_ERROR}" if PS_IMPORT_ERROR else ""
                module_path = Path(__file__).parent / "ReverseTunnel" / "tunnel_Powershell.py"
                exists_hint = f" exists={module_path.exists()}"
                self._log(
                    f"reverse_tunnel ps handler NOT registered (missing module/class){hint}{exists_hint}",
                    error=True,
                )
        except Exception as exc:
            self._log(f"reverse_tunnel ps handler registration failed: {exc}", error=True)

    # ------------------------------------------------------------------ Logging
    def _log(self, message: str, *, error: bool = False) -> None:
        fname = "reverse_tunnel.log"
        try:
            if callable(self._log_hook):
                self._log_hook(message, fname=fname)
                if error:
                    self._log_hook(message, fname="agent.error.log")
        except Exception:
            pass

    # ------------------------------------------------------------------ Event wiring
    def register_events(self):
        @self.sio.on("reverse_tunnel_start")
        async def _reverse_tunnel_start(payload):
            await self._handle_tunnel_start(payload)

        @self.sio.on("reverse_tunnel_stop")
        async def _reverse_tunnel_stop(payload):
            tid = ""
            if isinstance(payload, dict):
                tid = _norm_text(payload.get("tunnel_id"))
            await self._stop_tunnel(tid, code=CLOSE_AGENT_SHUTDOWN, reason="server_stop")

    # ------------------------------------------------------------------ Token helpers
    def _decode_token_payload(self, token: str, *, signing_key_hint: Optional[str] = None) -> Dict[str, Any]:
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

        if len(parts) == 2:
            candidates = []
            hint = _norm_text(signing_key_hint)
            if hint:
                candidates.append(hint)
            client = self._http_client()
            if client and hasattr(client, "load_server_signing_key"):
                try:
                    stored = client.load_server_signing_key()
                except Exception:
                    stored = None
                if isinstance(stored, str) and stored.strip():
                    candidates.append(stored.strip())

            signature = _b64decode(parts[1])
            verified = False
            for candidate in candidates:
                try:
                    key_bytes = base64.b64decode(candidate, validate=True)
                    public_key = serialization.load_der_public_key(key_bytes)
                except Exception:
                    continue
                if not isinstance(public_key, ed25519.Ed25519PublicKey):
                    continue
                try:
                    public_key.verify(signature, payload_bytes)
                    verified = True
                    if client and hasattr(client, "store_server_signing_key"):
                        try:
                            client.store_server_signing_key(candidate)
                        except Exception:
                            pass
                    break
                except Exception:
                    continue
            if not verified:
                raise ValueError("token_signature_invalid")
        return payload

    def _validate_token(self, token: str, *, expected_agent: str, expected_domain: str, expected_protocol: str, expected_tunnel: str, signing_key_hint: Optional[str]) -> Dict[str, Any]:
        payload = self._decode_token_payload(token, signing_key_hint=signing_key_hint)

        def _matches(expected: str, actual: Any) -> bool:
            return _norm_text(expected).lower() == _norm_text(actual).lower()

        if expected_agent and not _matches(expected_agent, payload.get("agent_id")):
            raise ValueError("token_agent_mismatch")
        if expected_tunnel and not _matches(expected_tunnel, payload.get("tunnel_id")):
            raise ValueError("token_id_mismatch")
        if expected_domain and not _matches(expected_domain, payload.get("domain")):
            raise ValueError("token_domain_mismatch")
        if expected_protocol and not _matches(expected_protocol, payload.get("protocol")):
            raise ValueError("token_protocol_mismatch")

        expires_at = payload.get("expires_at")
        try:
            expires_ts = int(expires_at) if expires_at is not None else None
        except Exception:
            expires_ts = None
        if expires_ts is not None and expires_ts < int(time.time()):
            raise ValueError("token_expired")
        return payload

    # ------------------------------------------------------------------ Utility
    def _http_client(self):
        try:
            if callable(self._http_client_factory):
                return self._http_client_factory()
        except Exception:
            return None
        return None

    def _port_allowed(self, port: int) -> bool:
        return isinstance(port, int) and 1 <= port <= 65535

    def _domain_allowed(self, domain: str) -> bool:
        limit = self._domain_limits.get(domain)
        if limit is None:
            return True
        active = [tid for tid, t in self._active.items() if t.domain == domain and not t.stopping]
        pending = [tid for dom, tid in self._domain_claims.items() if dom == domain and tid not in active]
        count = len(set(active + pending))
        return count < limit

    def _build_ws_url(self, port: int) -> str:
        client = self._http_client()
        if client:
            try:
                client.refresh_base_url()
            except Exception:
                pass
            base_url = getattr(client, "base_url", "") or "https://localhost:5000"
        else:
            base_url = "https://localhost:5000"
        parsed = urlparse(base_url)
        host = parsed.hostname or "localhost"
        scheme = "wss" if (parsed.scheme or "").lower() == "https" else "ws"
        return f"{scheme}://{host}:{port}/"

    def _ssl_context(self, host: str):
        client = self._http_client()
        verify = getattr(getattr(client, "session", None), "verify", True)
        if verify is False:
            return False
        if isinstance(verify, str) and os.path.isfile(verify) and client and hasattr(client, "key_store"):
            try:
                ctx = client.key_store.build_ssl_context()
                if ctx and _is_literal_ip(host):
                    ctx.check_hostname = False
                return ctx
            except Exception:
                return None
        return None

    async def _emit_status(self, payload: Dict[str, Any]) -> None:
        try:
            await self.sio.emit("reverse_tunnel_status", payload)
        except Exception:
            pass

    def _mark_activity(self, tunnel: ActiveTunnel) -> None:
        tunnel.last_activity = time.time()

    # ------------------------------------------------------------------ Event handlers
    async def _handle_tunnel_start(self, payload: Any) -> None:
        if not isinstance(payload, dict):
            self._log("reverse_tunnel_start ignored: payload not a dict", error=True)
            return
        tunnel_id = _norm_text(payload.get("tunnel_id"))
        token = _norm_text(payload.get("token"))
        port = payload.get("port") or payload.get("assigned_port")
        protocol = _norm_text(payload.get("protocol") or "ps").lower() or "ps"
        domain = _norm_text(payload.get("domain") or protocol).lower() or protocol
        heartbeat_seconds = int(payload.get("heartbeat_seconds") or self._default_heartbeat or 20)
        idle_seconds = int(payload.get("idle_seconds") or 3600)
        grace_seconds = int(payload.get("grace_seconds") or 3600)
        signing_key_hint = _norm_text(payload.get("signing_key"))
        agent_hint = _norm_text(payload.get("agent_id"))

        # Ignore broadcasts targeting other agents (Socket.IO fanout sends to both contexts).
        if agent_hint and agent_hint.lower() != _norm_text(self.ctx.agent_id).lower():
            return

        if not token:
            self._log("reverse_tunnel_start rejected: missing token", error=True)
            await self._emit_status({"tunnel_id": tunnel_id, "agent_id": self.ctx.agent_id, "status": "error", "reason": "token_missing"})
            return

        if not tunnel_id:
            tunnel_id = _norm_text(payload.get("lease_id"))  # fallback alias

        try:
            claims = self._validate_token(
                token,
                expected_agent=self.ctx.agent_id,
                expected_domain=domain,
                expected_protocol=protocol,
                expected_tunnel=tunnel_id,
                signing_key_hint=signing_key_hint,
            )
        except Exception as exc:
            if str(exc) == "token_agent_mismatch":
                # Broadcast hit the wrong agent context; ignore quietly.
                return
            self._log(f"reverse_tunnel_start rejected: token validation failed ({exc})", error=True)
            await self._emit_status({"tunnel_id": tunnel_id, "agent_id": self.ctx.agent_id, "status": "error", "reason": "token_invalid"})
            return

        tunnel_id = _norm_text(claims.get("tunnel_id") or tunnel_id)
        if not tunnel_id:
            self._log("reverse_tunnel_start rejected: tunnel_id missing after token parse", error=True)
            return

        try:
            port = int(port or claims.get("assigned_port") or 0)
        except Exception:
            port = 0
        if not self._port_allowed(port):
            self._log(f"reverse_tunnel_start rejected: invalid port {port}", error=True)
            await self._emit_status({"tunnel_id": tunnel_id, "agent_id": self.ctx.agent_id, "status": "error", "reason": "invalid_port"})
            return

        domain = _norm_text(claims.get("domain") or domain).lower()
        protocol = _norm_text(claims.get("protocol") or protocol).lower()
        expires_at = claims.get("expires_at")

        if tunnel_id in self._active:
            self._log(f"reverse_tunnel_start ignored: tunnel already active tunnel_id={tunnel_id}")
            return
        if not self._domain_allowed(domain):
            self._log(f"reverse_tunnel_start rejected: domain limit for {domain}", error=True)
            await self._emit_status({"tunnel_id": tunnel_id, "agent_id": self.ctx.agent_id, "status": "error", "reason": "domain_limit"})
            return

        url = self._build_ws_url(port)
        parsed = urlparse(url)
        heartbeat_seconds = max(5, min(heartbeat_seconds, 120))
        idle_seconds = max(30, idle_seconds)
        grace_seconds = max(60, grace_seconds)

        tunnel = ActiveTunnel(
            tunnel_id=tunnel_id,
            domain=domain,
            protocol=protocol,
            port=port,
            token=token,
            url=url,
            heartbeat_seconds=heartbeat_seconds,
            idle_seconds=idle_seconds,
            grace_seconds=grace_seconds,
            expires_at=int(expires_at) if expires_at is not None else None,
            signing_key_hint=signing_key_hint or None,
        )

        self._active[tunnel_id] = tunnel
        self._domain_claims[domain] = tunnel_id
        self._log(f"reverse_tunnel_start accepted tunnel_id={tunnel_id} domain={domain} protocol={protocol} url={url}")
        await self._emit_status({"tunnel_id": tunnel_id, "agent_id": self.ctx.agent_id, "status": "connecting", "url": url})
        task = self.loop.create_task(self._run_tunnel(tunnel, host=parsed.hostname or "localhost"))
        tunnel.tasks.append(task)

    # ------------------------------------------------------------------ Core tunnel handling
    async def _run_tunnel(self, tunnel: ActiveTunnel, *, host: str) -> None:
        ssl_ctx = self._ssl_context(host)
        timeout = aiohttp.ClientTimeout(total=None, sock_connect=10, sock_read=None)
        self._log(
            f"reverse_tunnel dialing ws url={tunnel.url} tunnel_id={tunnel.tunnel_id} "
            f"agent_id={self.ctx.agent_id} ssl={'yes' if ssl_ctx else 'no'}"
        )
        try:
            tunnel.session = aiohttp.ClientSession(timeout=timeout)
            tunnel.websocket = await tunnel.session.ws_connect(
                tunnel.url,
                ssl=ssl_ctx,
                heartbeat=None,
                max_msg_size=0,
                timeout=timeout,
            )
            self._mark_activity(tunnel)
            self._log(f"reverse_tunnel connected ws tunnel_id={tunnel.tunnel_id} peer={getattr(tunnel.websocket, 'remote', None)}")
            await tunnel.websocket.send_bytes(
                TunnelFrame(
                    msg_type=MSG_CONNECT,
                    channel_id=0,
                    payload=json.dumps(
                        {
                            "agent_id": self.ctx.agent_id,
                            "tunnel_id": tunnel.tunnel_id,
                            "token": tunnel.token,
                            "protocol": tunnel.protocol,
                            "domain": tunnel.domain,
                            "version": FRAME_VERSION,
                        },
                        separators=(",", ":"),
                    ).encode("utf-8"),
                ).encode()
            )
            self._log(f"reverse_tunnel CONNECT sent tunnel_id={tunnel.tunnel_id}")

            sender = self.loop.create_task(self._pump_sender(tunnel))
            receiver = self.loop.create_task(self._pump_receiver(tunnel))
            heartbeats = self.loop.create_task(self._heartbeat_loop(tunnel))
            watchdog = self.loop.create_task(self._watchdog(tunnel))
            tunnel.tasks.extend([sender, receiver, heartbeats, watchdog])
            await asyncio.wait([sender, receiver, heartbeats, watchdog], return_when=asyncio.FIRST_COMPLETED)
        except Exception as exc:
            self._log(f"reverse_tunnel connection failed tunnel_id={tunnel.tunnel_id}: {exc}", error=True)
            await self._emit_status({"tunnel_id": tunnel.tunnel_id, "agent_id": self.ctx.agent_id, "status": "error", "reason": "connect_failed"})
        finally:
            await self._shutdown_tunnel(tunnel)

    async def _pump_sender(self, tunnel: ActiveTunnel) -> None:
        try:
            while tunnel.websocket and not tunnel.websocket.closed:
                frame: TunnelFrame = await tunnel.send_queue.get()
                try:
                    await tunnel.websocket.send_bytes(frame.encode())
                    self._mark_activity(tunnel)
                    self._log(
                        f"reverse_tunnel send frame tunnel_id={tunnel.tunnel_id} "
                        f"msg_type={frame.msg_type} channel={frame.channel_id} len={len(frame.payload or b'')}"
                    )
                except Exception:
                    break
        except asyncio.CancelledError:
            pass
        except Exception:
            self._log(f"reverse_tunnel sender failed tunnel_id={tunnel.tunnel_id}", error=True)
        finally:
            self._log(f"reverse_tunnel sender stopped tunnel_id={tunnel.tunnel_id}")

    async def _pump_receiver(self, tunnel: ActiveTunnel) -> None:
        ws = tunnel.websocket
        if ws is None:
            return
        try:
            async for msg in ws:
                if msg.type == aiohttp.WSMsgType.BINARY:
                    try:
                        frame = decode_frame(msg.data)
                    except Exception:
                        self._log(f"reverse_tunnel frame decode failed tunnel_id={tunnel.tunnel_id}", error=True)
                        continue
                    self._mark_activity(tunnel)
                    await self._handle_frame(tunnel, frame)
                elif msg.type in (aiohttp.WSMsgType.CLOSED, aiohttp.WSMsgType.ERROR, aiohttp.WSMsgType.CLOSE):
                    self._log(
                        f"reverse_tunnel websocket closed tunnel_id={tunnel.tunnel_id} "
                        f"code={ws.close_code} reason={ws.close_reason}"
                    )
                    break
        except asyncio.CancelledError:
            pass
        except Exception:
            self._log(f"reverse_tunnel receiver failed tunnel_id={tunnel.tunnel_id}", error=True)
        finally:
            self._log(f"reverse_tunnel receiver stopped tunnel_id={tunnel.tunnel_id}")

    async def _heartbeat_loop(self, tunnel: ActiveTunnel) -> None:
        try:
            while tunnel.websocket and not tunnel.websocket.closed:
                await asyncio.sleep(tunnel.heartbeat_seconds)
                await self._send_frame(tunnel, heartbeat_frame())
                self._log(f"reverse_tunnel heartbeat sent tunnel_id={tunnel.tunnel_id}")
        except asyncio.CancelledError:
            pass
        except Exception:
            self._log(f"reverse_tunnel heartbeat failed tunnel_id={tunnel.tunnel_id}", error=True)
        finally:
            self._log(f"reverse_tunnel heartbeat loop stopped tunnel_id={tunnel.tunnel_id}")

    async def _watchdog(self, tunnel: ActiveTunnel) -> None:
        try:
            while tunnel.websocket and not tunnel.websocket.closed:
                await asyncio.sleep(10)
                now = time.time()
                if tunnel.idle_seconds and (now - tunnel.last_activity) >= tunnel.idle_seconds:
                    await self._send_frame(tunnel, close_frame(0, CLOSE_IDLE_TIMEOUT, "idle_timeout"))
                    self._log(f"reverse_tunnel watchdog idle_timeout tunnel_id={tunnel.tunnel_id}")
                    break
                if tunnel.expires_at and (now - tunnel.expires_at) >= tunnel.grace_seconds:
                    await self._send_frame(tunnel, close_frame(0, CLOSE_GRACE_EXPIRED, "grace_expired"))
                    self._log(f"reverse_tunnel watchdog grace_expired tunnel_id={tunnel.tunnel_id}")
                    break
        except asyncio.CancelledError:
            pass
        except Exception:
            self._log(f"reverse_tunnel watchdog failed tunnel_id={tunnel.tunnel_id}", error=True)
        finally:
            self._log(f"reverse_tunnel watchdog stopped tunnel_id={tunnel.tunnel_id}")

    async def _handle_frame(self, tunnel: ActiveTunnel, frame: TunnelFrame) -> None:
        self._log(
            f"reverse_tunnel recv frame tunnel_id={tunnel.tunnel_id} "
            f"msg_type={frame.msg_type} channel={frame.channel_id} len={len(frame.payload or b'')}"
        )
        if frame.msg_type == MSG_HEARTBEAT:
            if frame.flags & 0x1:
                self._log(f"reverse_tunnel heartbeat ack tunnel_id={tunnel.tunnel_id}")
                return
            await self._send_frame(tunnel, heartbeat_frame(channel_id=frame.channel_id, is_ack=True))
            return
        if frame.msg_type == MSG_CONNECT_ACK:
            tunnel.connected = True
            await self._emit_status({"tunnel_id": tunnel.tunnel_id, "agent_id": self.ctx.agent_id, "status": "connected"})
            self._log(f"reverse_tunnel CONNECT_ACK tunnel_id={tunnel.tunnel_id}")
            return
        if frame.msg_type == MSG_CHANNEL_OPEN:
            await self._handle_channel_open(tunnel, frame)
            return
        if frame.msg_type == MSG_CLOSE:
            try:
                reason = json.loads(frame.payload.decode("utf-8"))
            except Exception:
                reason = {"code": CLOSE_UNEXPECTED_DISCONNECT, "reason": "close"}
            tunnel.stop_reason = reason.get("reason") if isinstance(reason, dict) else None
            await self._shutdown_tunnel(tunnel, send_close=False)
            return

        handler = tunnel.channels.get(frame.channel_id)
        if handler:
            try:
                await handler.on_frame(frame)
            except Exception:
                self._log(f"reverse_tunnel channel handler failed tunnel_id={tunnel.tunnel_id} channel={frame.channel_id}", error=True)
        else:
            if frame.msg_type in (MSG_DATA, MSG_CONTROL, MSG_WINDOW_UPDATE):
                await self._send_frame(tunnel, close_frame(frame.channel_id, CLOSE_PROTOCOL_ERROR, "unknown_channel"))

    async def _handle_channel_open(self, tunnel: ActiveTunnel, frame: TunnelFrame) -> None:
        try:
            payload = json.loads(frame.payload.decode("utf-8"))
        except Exception:
            payload = {}
        protocol = _norm_text(payload.get("protocol") or tunnel.protocol)
        metadata = payload.get("metadata") if isinstance(payload, dict) else {}

        if frame.channel_id in tunnel.channels:
            await self._send_frame(tunnel, close_frame(frame.channel_id, CLOSE_PROTOCOL_ERROR, "channel_exists"))
            return

        if protocol.lower() == "ps" and "ps" not in self._protocol_handlers:
            hint = f" import_error={PS_IMPORT_ERROR}" if PS_IMPORT_ERROR else ""
            module_path = Path(__file__).parent / "ReverseTunnel" / "tunnel_Powershell.py"
            exists_hint = f" exists={module_path.exists()}"
            self._log(
                f"reverse_tunnel ps handler missing; falling back to BaseChannel{hint}{exists_hint}",
                error=True,
            )

        handler_cls = self._protocol_handlers.get(protocol.lower()) or BaseChannel
        try:
            handler = handler_cls(self, tunnel, frame.channel_id, metadata)
        except Exception:
            handler = BaseChannel(self, tunnel, frame.channel_id, metadata)
        tunnel.channels[frame.channel_id] = handler
        await handler.start()
        await self._send_frame(
            tunnel,
            TunnelFrame(
                msg_type=MSG_CHANNEL_ACK,
                channel_id=frame.channel_id,
                payload=json.dumps({"status": "ok", "protocol": protocol}, separators=(",", ":")).encode("utf-8"),
            ),
        )
        self._log(
            f"reverse_tunnel channel_opened tunnel_id={tunnel.tunnel_id} channel={frame.channel_id} "
            f"protocol={protocol} handler={handler.__class__.__name__} metadata={metadata}"
        )

    async def _send_frame(self, tunnel: ActiveTunnel, frame: TunnelFrame) -> None:
        if tunnel.stopping:
            return
        try:
            tunnel.send_queue.put_nowait(frame)
        except Exception:
            await tunnel.send_queue.put(frame)

    async def _stop_tunnel(self, tunnel_id: str, *, code: int = CLOSE_AGENT_SHUTDOWN, reason: str = "requested") -> None:
        tunnel = self._active.get(tunnel_id)
        if not tunnel:
            return
        await self._send_frame(tunnel, close_frame(0, code, reason))
        await self._shutdown_tunnel(tunnel, send_close=False)

    async def _shutdown_tunnel(self, tunnel: ActiveTunnel, *, send_close: bool = True) -> None:
        if tunnel.stopping:
            return
        tunnel.stopping = True
        if send_close:
            try:
                await self._send_frame(tunnel, close_frame(0, CLOSE_AGENT_SHUTDOWN, "agent_shutdown"))
            except Exception:
                pass
        for task in list(tunnel.tasks):
            try:
                task.cancel()
            except Exception:
                pass
        if tunnel.websocket is not None:
            try:
                await tunnel.websocket.close()
            except Exception:
                pass
        if tunnel.session is not None:
            try:
                await tunnel.session.close()
            except Exception:
                pass
        self._active.pop(tunnel.tunnel_id, None)
        if tunnel.domain in self._domain_claims and self._domain_claims.get(tunnel.domain) == tunnel.tunnel_id:
            self._domain_claims.pop(tunnel.domain, None)
        self._log(f"reverse_tunnel stopped tunnel_id={tunnel.tunnel_id} reason={tunnel.stop_reason or 'closed'}")
        await self._emit_status({"tunnel_id": tunnel.tunnel_id, "agent_id": self.ctx.agent_id, "status": "closed", "reason": tunnel.stop_reason or "closed"})

    # ------------------------------------------------------------------ Lifecycle
    def stop_all(self):
        for tunnel_id in list(self._active.keys()):
            try:
                self.loop.create_task(self._stop_tunnel(tunnel_id, code=CLOSE_AGENT_SHUTDOWN, reason="agent_shutdown"))
            except Exception:
                pass
