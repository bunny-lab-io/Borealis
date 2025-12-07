# Borealis Reverse Tunnels – Operator & Developer Guide

This document is the single reference for how Borealis reverse tunnels are organized, secured, and orchestrated. It is written for Codex agents extending the feature (new protocols, UI, or policy changes).

## 1) High-Level Model
- Outbound-only: Agents initiate all tunnel sockets to the Engine. No inbound openings on devices.
- Transport: WebSocket-over-TLS carrying a binary frame header (version | msg_type | flags | reserved | channel_id | length) plus payload.
- Leases: Engine issues short-lived leases per agent/domain/protocol. Each lease binds a tunnel_id to an ephemeral Engine port and a signed token.
- Domains: Concurrency “lanes” keep protocols isolated: `remote-interactive-shell` (2), `remote-management` (1), `remote-video` (2). Legacy aliases (`ps`, etc.) normalize into these lanes.
- Channels: Logical streams inside a tunnel (channel_id u32). PS uses channel 1; future protocols can open more channels per tunnel as needed.
- Tear-down: Idle/grace timeouts plus explicit operator stop. Closing a tunnel must close its protocol channel(s) and kill the agent process for interactive shells.

## 2) Engine Components
- Orchestrator: `Data/Engine/services/WebSocket/Agent/reverse_tunnel_orchestrator.py`
  - Lease manager: Port pool allocator, domain limit enforcement, idle/grace sweeper.
  - Token issuer/validator: Binds agent_id, tunnel_id, domain, protocol, port, expires_at.
  - Bridge: Maps agent sockets ↔ operator sockets; stores per-tunnel protocol server instances.
  - Logging: `Engine/Logs/reverse_tunnel.log` plus Device Activity start/stop entries.
  - Stop path: `stop_tunnel` closes protocol servers, emits `reverse_tunnel_stop` to agents, releases lease/bridge.
- Protocol registry: Domain/protocol handlers under `Data/Engine/services/WebSocket/Agent/Reverse_Tunnels/`:
  - `remote_interactive_shell/Protocols/Powershell.py` (live), `Bash.py` (placeholder).
  - `remote_management/Protocols/SSH.py`, `WinRM.py` (placeholders).
  - `remote_video/Protocols/VNC.py`, `RDP.py`, `WebRTC.py` (placeholders).
- API Endpoints:
  - `POST /api/tunnel/request` → allocates lease, returns {tunnel_id, port, token, idle_seconds, grace_seconds, domain, protocol}.
  - `DELETE /api/tunnel/<tunnel_id>` → operator-driven stop; pushes stop to agent and releases the lease.
  - Domain default for PowerShell requests is `remote-interactive-shell` (legacy `ps` still accepted).
- Operator Socket.IO namespace `/tunnel`:
  - `join`, `send`, `poll`, `ps_open`, `ps_send`, `ps_resize`, `ps_poll`.
  - Operator socket disconnect triggers `stop_tunnel` if no other operators remain attached.
- WebUI (current): `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx` requests PS leases in `remote-interactive-shell`, sends CLOSE frames, and calls DELETE on disconnect/unload.

## 3) Agent Components
- Role: `Data/Agent/Roles/role_ReverseTunnel.py`
  - Validates signed lease tokens; enforces domain limits (2/1/2 with legacy fallbacks).
  - Outbound TLS WS connect to assigned port; heartbeats + idle/grace watchdog; stop_all closes channels and sends CLOSE.
  - Protocol registry: loads handlers from `Data/Agent/Roles/Reverse_Tunnels/*/Protocols/*` (PowerShell live; others stubbed to close unsupported channels cleanly).
- PowerShell channel: `Data/Agent/Roles/ReverseTunnel/tunnel_Powershell.py` (pipes-only, no PTY); re-exported under `Reverse_Tunnels/remote_interactive_shell/Protocols/Powershell.py`.
- Logging: `Agent/Logs/reverse_tunnel.log` with channel/tunnel lifecycle.

## 4) Framing, Heartbeats, Close
- Header: version(1) | msg_type(1) | flags(1) | reserved(1) | channel_id(u32 LE) | length(u32 LE).
- Messages: CONNECT/ACK, CHANNEL_OPEN/ACK, DATA, CONTROL (resize), WINDOW_UPDATE (reserved), HEARTBEAT (ping/pong), CLOSE.
- Close codes: ok, idle_timeout, grace_expired, protocol_error, auth_failed, server_shutdown, agent_shutdown, domain_limit, unexpected_disconnect.
- Heartbeats: Engine → Agent loop; idle/grace sweeper ~15s on Engine; Agent watchdog closes on idle/grace.

## 5) Lifecycle (PowerShell example)
1. UI calls `POST /api/tunnel/request` with agent_id, protocol=ps, domain=remote-interactive-shell.
2. Engine allocates port/tunnel_id, signs token, starts listener, pushes `reverse_tunnel_start` to agent.
3. Agent dials WS to assigned port, sends CONNECT with token. Engine validates, binds bridge, sends CONNECT_ACK + heartbeat.
4. Operator Socket.IO `/tunnel` joins; Engine attaches operator, instantiates PS server, issues CHANNEL_OPEN.
5. Agent launches PowerShell (pipes), streams stdout/stderr as DATA; operator input via `ps_send`; optional resize via `ps_resize` (no-op on agent pipes).
6. On operator Disconnect/tab close, UI sends CLOSE frame and calls DELETE; Engine stop path notifies agent (`reverse_tunnel_stop`), closes channel, releases lease/domain slot.
7. Idle/grace expiry or agent disconnect also triggers close/release; domain slots free immediately.

## 6) Security & Auth
- TLS: Reuse existing pinned bundle; outbound-only agent sockets.
- Token: short-lived, binds agent_id/tunnel_id/domain/protocol/port/expires_at; optional signature verification (Ed25519 signer when configured).
- Operator auth: uses existing Engine session/cookie/bearer for `/tunnel` namespace and API endpoints.

## 7) Configuration Knobs (defaults)
- Port pool: 30000–40000; fixed port optional (context settings).
- Idle timeout: 3600s; Grace timeout: 3600s.
- Heartbeat interval: 20s (Engine → Agent).
- Domain limits: remote-interactive-shell=2, remote-management=1, remote-video=2; legacy aliases preserved.
- Log path: `Engine/Logs/reverse_tunnel.log`; `Agent/Logs/reverse_tunnel.log`.

## 8) Logs & Telemetry
- Engine: lease events, socket events, close reasons in `reverse_tunnel.log`; Device Activity start/stop with tunnel_id/operator_id when available.
- Agent: role lifecycle, channel start/stop, errors in `reverse_tunnel.log`.

## 9) Extending to New Protocols
- Add Engine handler under the appropriate domain folder and register in the orchestrator’s protocol registry.
- Add Agent handler under matching domain folder; update role registry to load it.
- Define channel open semantics (metadata), DATA/CONTROL usage, and close behavior.
- Update API/UI to allow selecting the protocol/domain and to send protocol-specific controls.

## 10) Outstanding Work
- Implement real handlers for Bash/SSH/WinRM/RDP/VNC/WebRTC and surface in UI.
- Add tests for DELETE stop path, per-domain limits, and browser disconnect cleanup.
- Consider a binary WebSocket browser bridge to replace Socket.IO for high-throughput protocols.

## 11) Risks & Watchpoints
- Eventlet/asyncio coexistence: tunnel loop runs on its own thread/loop; avoid blocking Socket.IO handlers.
- Port exhaustion: handle allocation failures cleanly; always release on stop/idle/grace.
- Buffer growth: add back-pressure before enabling high-throughput protocols.
- Security: strict token binding (agent_id/tunnel_id/domain/protocol/port/expiry) and TLS; reject framing errors.

## 12) Change Log (not exhaustive)
- 2025-11-30: Initial scaffold (lease manager, framing, tokens, API, Agent role, PS handlers).
- 2025-12-06: Simplified PS to pipes-only; improved handler imports; UI status tweaks.
- 2025-12-18: Domain lanes introduced (`remote-interactive-shell`, `remote-management`, `remote-video`) with limits 2/1/2; protocol handlers reorganized under `Reverse_Tunnels/*/Protocols/*`; orchestrator renamed to `reverse_tunnel_orchestrator.py`; explicit stop API/Socket.IO cleanup; WebUI Disconnect/unload calls DELETE + CLOSE for immediate teardown.
