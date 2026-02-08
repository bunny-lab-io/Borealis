# VPN and Remote Access
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Document Borealis remote access features: WireGuard reverse VPN tunnels, remote PowerShell, and VNC via noVNC.

## WireGuard Reverse VPN (High Level)
- Outbound-only: agents initiate tunnels to the Engine; no inbound listeners on devices.
- Transport: WireGuard UDP 30000.
- One persistent tunnel per agent, established at agent boot and shared across operators.
- Host-only routing: each agent gets a /32; no client-to-client routes.
- Keepalive: `PersistentKeepalive = 30` seconds on the agent.
- No idle teardown while the agent service is running.
- Port access: the tunnel is trusted end-to-end, and Engine/Agent firewall rules allow a global port allowlist between the Engine /32 and Agent /32 (defaults to 47002 and 5900, configurable via `BOREALIS_WIREGUARD_PORT_ALLOWLIST`).

## Remote PowerShell
- Uses the WireGuard tunnel and a TCP shell server on the agent.
- UI establishes sessions via `/api/shell/establish` and disconnects via `/api/shell/disconnect`.
- Engine bridges UI Socket.IO events to the agent TCP shell.
- Shell port default: 47002 (configurable).

## VNC via noVNC
- Engine issues VNC session info via `/api/vnc/establish` (ws_url + password).
- WebUI connects to `ws(s)://<engine_host>:4823/vnc`.
- VNC authentication is handled by the UltraVNC password; no extra per-session token is required.
- Agent runs UltraVNC as a Windows service; Borealis keeps the VNC firewall rule enabled for the Engine /32 and `/api/vnc/disconnect` only tears down the WebUI session.

## Reverse Proxy Configuration
Traefik dynamic config (replace service URL with the actual Borealis Engine URL):
```yml
http:
  routers:
    borealis:
      entryPoints:
        - websecure
      tls:
        certResolver: letsencrypt
      service: borealis
      rule: "Host(`borealis.example.com`) && PathPrefix(`/`)"
      middlewares:
        - cors-headers

  middlewares:
    cors-headers:
      headers:
        accessControlAllowOriginList:
          - "*"
        accessControlAllowMethods:
          - GET
          - POST
          - OPTIONS
        accessControlAllowHeaders:
          - Content-Type
          - Upgrade
          - Connection
        accessControlMaxAge: 100
        addVaryHeader: true

  services:
    borealis:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:5000"
        passHostHeader: true
```

## API Endpoints
- `POST /api/tunnel/connect` (Token Authenticated) - ensure WireGuard tunnel material for an agent.
- `GET /api/tunnel/status` (Token Authenticated) - tunnel status by agent.
- `GET /api/tunnel/active` (Token Authenticated) - list active tunnels.
- `POST /api/agent/vpn/ensure` (Device Authenticated) - agent-side persistent tunnel bootstrap.
- `POST /api/agent/vnc/ensure` (Device Authenticated) - ensure always-on VNC credentials for the agent.
- `POST /api/vnc/establish` (Token Authenticated) - establish VNC session.
- `POST /api/vnc/disconnect` (Token Authenticated) - disconnect VNC session (revokes WebUI session).
- `POST /api/shell/establish` (Token Authenticated) - establish remote shell session.
- `POST /api/shell/disconnect` (Token Authenticated) - disconnect remote shell session.

## Related Documentation
- [Device Management](device-management.md)
- [Agent Runtime](agent-runtime.md)
- [Security and Trust](security-and-trust.md)
- [API Reference](api-reference.md)

## Codex Agent (Detailed)
### Core Engine files
- Tunnel service: `Data/Engine/services/VPN/vpn_tunnel_service.py`.
- WireGuard server manager: `Data/Engine/services/VPN/wireguard_server.py`.
- Tunnel API: `Data/Engine/services/API/devices/tunnel.py`.
- Shell bridge: `Data/Engine/services/WebSocket/vpn_shell.py`.
- VNC session API: `Data/Engine/services/API/devices/vnc.py`.
- VNC proxy: `Data/Engine/services/RemoteDesktop/vnc_proxy.py`.

### Core Agent files
- WireGuard client role: `Data/Agent/Roles/role_WireGuardTunnel.py`.
- Remote PowerShell role: `Data/Agent/Roles/role_RemotePowershell.py`.
- VNC role: `Data/Agent/Roles/role_VNC.py`.

### Config paths
- Engine WireGuard config: `Engine/WireGuard/borealis-wg.conf`.
- Agent WireGuard config: `Agent/Borealis/Settings/WireGuard/Borealis.conf`.
- Engine WireGuard keys: `Engine/Certificates/VPN_Server/`.
- Agent WireGuard keys: `Agent/Borealis/Certificates/VPN_Client/`.

### Service names (Windows)
- Engine listener service: `WireGuardTunnel$borealis-wg`.
- Agent tunnel service: `WireGuardTunnel$Borealis`.
- Adapter name in Control Panel: `Borealis`.
- Display names:
  - `Borealis - WireGuard - Engine`
  - `Borealis - WireGuard - Agent`

### Event flow (WireGuard tunnel)
1) Agent calls `/api/agent/vpn/ensure` at boot (and periodically) to establish the persistent tunnel.
2) Engine creates or reuses the tunnel session and emits `vpn_tunnel_start`.
3) Agent verifies the token signature and starts the WireGuard client with `PersistentKeepalive = 30`.
4) Engine applies firewall rules that allow the configured port allowlist to the agent /32.
5) UI sessions (shell/VNC) reuse the existing tunnel without teardown.

### Event flow (Remote PowerShell)
1) UI opens a shell and emits `vpn_shell_open`.
2) Engine checks tunnel status and agent socket readiness.
3) Engine opens a TCP connection to agent shell on port 47002.
4) UI sends `vpn_shell_send`; Engine forwards to agent over TCP.
5) Agent returns stdout frames; Engine emits `vpn_shell_output`.

### Firewall rules
- Engine allows the configured port allowlist from the agent /32 (TCP/UDP) over the WireGuard tunnel.
- Agent allows the configured port allowlist from the Engine /32 (TCP/UDP) over the WireGuard tunnel.

### Persistent tunnel behavior
- WireGuard sessions stay online while the agent service is running.
- There are no tunnel-disconnect endpoints; only role-level disconnects (shell/VNC) exist.
- The tunnel and per-device firewall rules remain in place while the agent runs.
- Keepalive is handled by WireGuard (`PersistentKeepalive = 30` seconds).
- The agent periodically calls `/api/agent/vpn/ensure` to heal the tunnel if it drops.

### Logs to inspect
- Engine tunnel log: `Engine/Logs/VPN_Tunnel/tunnel.log`.
- Engine shell log: `Engine/Logs/VPN_Tunnel/remote_shell.log`.
- Agent tunnel log: `Agent/Logs/VPN_Tunnel/tunnel.log`.
- Agent shell log: `Agent/Logs/VPN_Tunnel/remote_shell.log`.

### Troubleshooting checklist
- Confirm WireGuard service is running (Engine and Agent).
- Confirm the agent successfully calls `/api/agent/vpn/ensure` after boot.
- Confirm `/api/tunnel/status` returns `status=up` and `agent_socket=true`.
- Verify `Agent/Borealis/Settings/WireGuard/Borealis.conf` during an active session.
- Test TCP shell reachability: `Test-NetConnection <agent_vpn_ip> -Port 47002`.

### Known limitations
- Legacy WebSocket tunnels are retired; only WireGuard is supported.
- VNC requires UltraVNC running on the agent.

### Reverse VPN Tunnels (WireGuard) - Full Reference
#### 1) High-level model
- Outbound-only: agents establish WireGuard tunnels to the Engine; no inbound access on devices.
- Transport: WireGuard/UDP on port 30000.
- Sessions: one persistent VPN tunnel per agent; multiple operators share it.
- Routing: host-only /32 per agent; AllowedIPs restricted to the agent /32 and engine /32; no client-to-client.
- Keepalive: `PersistentKeepalive = 30` seconds; tunnels stay up while agents run.
- Keys: WireGuard server keys under `Engine/Certificates/VPN_Server`; client keys under `Agent/Borealis/Certificates/VPN_Client`.

#### 2) Engine components
- Orchestrator: `Data/Engine/services/VPN/vpn_tunnel_service.py`
  - Allocates per-agent /32, issues short-lived orchestration tokens, enforces single-session.
  - Keeps the WireGuard listener online, applies firewall rules, and avoids idle teardown in persistent mode.
  - Emits Socket.IO events: `vpn_tunnel_start`, `vpn_tunnel_stop`, `vpn_tunnel_activity`.
- WireGuard manager: `Data/Engine/services/VPN/wireguard_server.py`
  - Generates server keys, renders config, manages `wireguard.exe` tunnel service, applies allowlist-based firewall rules.
- PowerShell bridge: `Data/Engine/services/WebSocket/vpn_shell.py`
  - Proxies UI shell input/output to the agent's TCP shell server over WireGuard.
- Logging: `Engine/Logs/VPN_Tunnel/tunnel.log` plus Device Activity entries; shell I/O is in `Engine/Logs/VPN_Tunnel/remote_shell.log`.

#### 3) API endpoints
- `POST /api/tunnel/connect` -> issues or reuses session material (tunnel_id, token, virtual_ip, endpoint).
- `GET /api/tunnel/status` -> returns up/down status for an agent.
- `GET /api/tunnel/active` -> lists active VPN tunnel sessions (tunnel_id, agent_id, virtual_ip, last_activity, etc.).
- `POST /api/agent/vpn/ensure` -> device-authenticated tunnel bootstrap for persistent mode.
- `POST /api/shell/establish` -> establish remote shell session.
- `POST /api/shell/disconnect` -> disconnect remote shell session.
- `POST /api/vnc/establish` -> establish VNC session.
- `POST /api/vnc/disconnect` -> disconnect VNC session.

#### 4) Agent components
- Tunnel lifecycle: `Data/Agent/Roles/role_WireGuardTunnel.py`
  - Validates orchestration tokens, starts WireGuard client service, keeps the tunnel persistent.
- Shell server: `Data/Agent/Roles/role_RemotePowershell.py`
- TCP PowerShell server bound to `0.0.0.0:47002`, restricted to VPN subnet (10.255.x.x).
- Logging: `Agent/Logs/VPN_Tunnel/tunnel.log` (tunnel lifecycle) and `Agent/Logs/VPN_Tunnel/remote_shell.log` (shell I/O).

#### 5) Security and auth
- TLS pinned for Engine API and Socket.IO.
- Orchestration tokens signed via Engine Ed25519 key; agent verifies signatures and stores the signing key.
- WireGuard AllowedIPs /32; no LAN routes; client-to-client blocked.
- Engine firewall rules allow the configured port allowlist between Engine /32 and Agent /32.

#### 6) UI
- Device details expose Remote Shell and VNC tabs; no per-device VPN port controls are shown.
- PowerShell MVP reuses `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx` with WireGuard APIs and VPN shell events.

#### 7) Extending to new protocols
- Reuse the existing VPN tunnel; no new transport/domain lanes required.

#### 8) Legacy removal
- WebSocket tunnel domains, protocol handlers, and domain limits are removed.
- No `/tunnel` Socket.IO namespace or framed protocol messages remain.

#### 9) Change log (not exhaustive)
- 2025-11-30: Legacy WebSocket tunnel scaffold introduced (lease manager, framing, tokens).
- 2025-12-06: Legacy PowerShell handler simplified to pipes-only; UI status tweaks.
- 2025-12-18: Legacy domain lanes added (`remote-interactive-shell`, `remote-management`, `remote-video`) with limits.
- 2025-12-20: WireGuard reverse VPN migration complete; legacy WebSocket tunnels retired; VPN shell bridge and new APIs.

### WireGuard Troubleshooting Handoff (Full)
This section consolidates the troubleshooting context and environment notes for WireGuard tunnel investigations. It is written as reference material only (no standalone prompts).

#### Environment and scope
- Workspace: D:\Github\Borealis (local project root for the Engine)
- Host OS: Windows 10/11 (build 26200). Engine runs on this machine.
- Remote Agent: mounted read-only at Z:\ (maps to C:\Borealis on the remote device; logs/configs under Z:\Agent\...).
- Agent and Engine launch: via Borealis.ps1, always elevated as admin.
- Network: Engine on 10.0.0.54; remote agent uses server_url.txt to derive endpoint host.
- WireGuard version: wireguard.exe 0.5.3, wg.exe 1.0.20210914.
- PIA (Private Internet Access) is installed and supplies a wintun driver (pia-wintun.sys). Do NOT treat the PIA adapter as the Borealis adapter.

#### Desired behavior
- Agent has a dedicated WireGuard adapter named "Borealis".
- Adapter provisioning is idempotent: if "Borealis" exists, do not recreate it.
- Configs must live inside the project root:
  - Agent: Agent\Borealis\Settings\WireGuard\Borealis.conf
  - Engine: Engine\WireGuard\borealis-wg.conf
- Agent ensures the WireGuard tunnel on boot via `/api/agent/vpn/ensure`, then remote shell/VNC/SSH flow through it.
- No idle teardown; tunnels and firewall rules stay in place while the agent is running.

#### Recent changes (current repo state)
- Data/Agent/Roles/role_WireGuardTunnel.py
  - Lazy client init (avoid side effects on import).
  - Service name fix: WireGuard tunnel service is "WireGuardTunnel$Borealis".
  - Endpoint override: if Engine sends localhost, use host from server_url.txt and port from the token.
  - Config path preference: Agent\Borealis\Settings\WireGuard.
  - Service display name set to "Borealis - WireGuard - Agent".
  - Persistent tunnels with `PersistentKeepalive = 30`.
  - Applies an allowlist firewall rule using the engine /32 from allowed_ips and the Engine allowlist payload.
- Data/Engine/services/VPN/wireguard_server.py
  - Engine config path: Engine\WireGuard\borealis-wg.conf (project root only).
  - Removed invalid "SaveConfig = false" line (WireGuard rejected it).
  - Service display name set to "Borealis - WireGuard - Engine".
  - Ensures the listener service is running after install, and raises if it fails.
- Borealis.ps1
  - Service name interpolation fixed to include the literal "$" in "WireGuardTunnel$Borealis".

Note: Data/Agent changes only apply after Borealis.ps1 re-stages the agent under Agent\.

#### Current symptoms (2026-01-14 00:05)
- Tunnel handshakes are healthy; TCP shell connectivity succeeds after adding an allowlist firewall rule for the engine /32.
- The firewall rule is now kept in place by `role_WireGuardTunnel.py` using the engine /32 in the `allowed_ips` payload.
- `wireguard.exe /dumplog /tail` still fails with "Stdout must be set" when run from PowerShell (use file redirection).

#### Key paths
- Agent WireGuard role: Data/Agent/Roles/role_WireGuardTunnel.py
- Agent VPN shell role: Data/Agent/Roles/role_RemotePowershell.py
- Engine WireGuard manager: Data/Engine/services/VPN/wireguard_server.py
- Engine tunnel service: Data/Engine/services/VPN/vpn_tunnel_service.py
- Agent tunnel logs: Z:\Agent\Logs\VPN_Tunnel\tunnel.log
- Agent shell logs: Z:\Agent\Logs\VPN_Tunnel\remote_shell.log
- Engine tunnel logs: Engine\Logs\VPN_Tunnel\tunnel.log
- Engine shell logs: Engine\Logs\VPN_Tunnel\remote_shell.log
- Agent WireGuard config: Z:\Agent\Borealis\Settings\WireGuard\Borealis.conf
- Engine WireGuard config: Engine\WireGuard\borealis-wg.conf

#### Known WireGuard services and names
- Engine listener service name: "WireGuardTunnel$borealis-wg"
- Agent tunnel service name: "WireGuardTunnel$Borealis"
- Adapter name in Control Panel: "Borealis"
- Service display names:
  - "Borealis - WireGuard - Engine"
  - "Borealis - WireGuard - Agent"

#### Suggested verification commands
- Engine service status:
  - Get-Service -Name "WireGuardTunnel$borealis-wg"
  - sc.exe query "WireGuardTunnel$borealis-wg"
  - netstat -ano -p udp | findstr :30000
- Engine WireGuard log tail:
  - cmd /c ""C:\Program Files\WireGuard\wireguard.exe" /dumplog /tail > %TEMP%\wg-tail.log"
  - powershell -NoProfile -Command "& 'C:\Program Files\WireGuard\wireguard.exe' /dumplog /tail 2>&1 | Out-File $env:TEMP\wg-tail.log"
- Agent tunnel state (remote, via Z:\ logs):
  - Z:\Agent\Logs\VPN_Tunnel\tunnel.log
  - Z:\Agent\Logs\VPN_Tunnel\remote_shell.log
  - Z:\Agent\Borealis\Settings\WireGuard\Borealis.conf

#### Current blockers and next steps
1) Ensure the agent runtime is re-staged so `role_WireGuardTunnel.py` applies the allowlist firewall rule on tunnel start.
2) During an active session, run `Test-NetConnection -ComputerName 10.255.0.2 -Port 47002` on the Engine and confirm it reaches the agent.
3) Confirm `Agent\Borealis\Settings\WireGuard\Borealis.conf` includes a [Peer] with endpoint/AllowedIPs while the agent is running.
4) Capture engine and agent tunnel/shell logs around a failed shell open attempt and re-check WireGuard service state if issues persist.
