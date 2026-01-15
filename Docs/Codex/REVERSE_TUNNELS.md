# Borealis Reverse VPN Tunnels (WireGuard) – Operator & Developer Guide

This document is the reference for Borealis reverse VPN tunnels built on WireGuard. The legacy WebSocket framing and domain-lane tunnel stack has been retired; the system now uses a single outbound WireGuard tunnel per agent with host-only routing and per-device ACLs.

## 1) High-Level Model
- Outbound-only: agents establish WireGuard tunnels to the Engine; no inbound access on devices.
- Transport: WireGuard/UDP on port 30000.
- Sessions: one live VPN tunnel per agent; multiple operators share it.
- Routing: host-only /32 per agent; AllowedIPs restricted to the agent /32 and engine /32; no client-to-client.
- Idle timeout: 15 minutes of no operator activity; no grace period.
- Keys: WireGuard server keys under `Engine/Certificates/VPN_Server`; client keys under `Agent/Borealis/Certificates/VPN_Client`.

## 2) Engine Components
- Orchestrator: `Data/Engine/services/VPN/vpn_tunnel_service.py`
  - Allocates per-agent /32, issues short-lived orchestration tokens, enforces single-session.
  - Starts/stops WireGuard listener, applies firewall rules, idles out on inactivity.
  - Emits Socket.IO events: `vpn_tunnel_start`, `vpn_tunnel_stop`, `vpn_tunnel_activity`.
- WireGuard manager: `Data/Engine/services/VPN/wireguard_server.py`
  - Generates server keys, renders config, manages `wireguard.exe` tunnel service, applies ACL rules.
- PowerShell bridge: `Data/Engine/services/WebSocket/vpn_shell.py`
  - Proxies UI shell input/output to the agent’s TCP shell server over WireGuard.
- Logging: `Engine/Logs/VPN_Tunnel/tunnel.log` plus Device Activity entries; shell I/O is in `Engine/Logs/VPN_Tunnel/remote_shell.log`.

## 3) API Endpoints
- `POST /api/tunnel/connect` → issues session material (tunnel_id, token, virtual_ip, endpoint, allowed_ports, idle_seconds).
- `GET /api/tunnel/status` → returns up/down status for an agent.
- `GET /api/tunnel/connect/status` → alias for status (used by UI before shell open).
- `GET /api/tunnel/active` → lists active VPN tunnel sessions (tunnel_id, agent_id, virtual_ip, last_activity, etc.).
- `DELETE /api/tunnel/disconnect` → immediate teardown (agent + engine cleanup).
- `GET /api/device/vpn_config/<agent_id>` → read per-agent allowed ports.
- `PUT /api/device/vpn_config/<agent_id>` → update allowed ports.

## 4) Agent Components
- Tunnel lifecycle: `Data/Agent/Roles/role_WireGuardTunnel.py`
  - Validates orchestration tokens, starts/stops WireGuard client service, enforces idle.
- Shell server: `Data/Agent/Roles/role_RemotePowershell.py`
- TCP PowerShell server bound to `0.0.0.0:47002`, restricted to VPN subnet (10.255.x.x).
- Logging: `Agent/Logs/VPN_Tunnel/tunnel.log` (tunnel lifecycle) and `Agent/Logs/VPN_Tunnel/remote_shell.log` (shell I/O).

## 5) Security & Auth
- TLS pinned for Engine API/Socket.IO.
- Orchestration tokens signed via Engine Ed25519 key; agent verifies signatures and stores the signing key.
- WireGuard AllowedIPs /32; no LAN routes; client-to-client blocked.
- Engine firewall rules enforce per-device allowed ports.

## 6) UI
- Device details now include an “Advanced Config” tab for per-device allowed ports.
- PowerShell MVP reuses `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx` with WireGuard APIs + VPN shell events.

## 7) Extending to New Protocols
- Add protocol ports to the device allowlist and UI toggles.
- Reuse the existing VPN tunnel; no new transport/domain lanes required.

## 8) Legacy Removal
- WebSocket tunnel domains, protocol handlers, and domain limits are removed.
- No `/tunnel` Socket.IO namespace or framed protocol messages remain.

## 9) Change Log (not exhaustive)
- 2025-11-30: Legacy WebSocket tunnel scaffold introduced (lease manager, framing, tokens).
- 2025-12-06: Legacy PowerShell handler simplified to pipes-only; UI status tweaks.
- 2025-12-18: Legacy domain lanes added (`remote-interactive-shell`, `remote-management`, `remote-video`) with limits.
- 2025-12-20: WireGuard reverse VPN migration complete; legacy WebSocket tunnels retired; VPN shell bridge + new APIs.
