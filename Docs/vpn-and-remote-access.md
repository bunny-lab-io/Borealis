# VPN and Remote Access
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Document Borealis remote access features: WireGuard reverse VPN tunnels, remote PowerShell, and VNC via Apache Guacamole.

## WireGuard Reverse VPN (High Level)
- Outbound-only: agents initiate tunnels to the Engine; no inbound listeners on devices.
- Transport: WireGuard UDP 30000.
- One persistent tunnel per agent, established at agent boot and shared across operators.
- Host-only routing: each agent gets a /32; no client-to-client routes.
- Keepalive: `PersistentKeepalive = 30` seconds on the agent.
- Borealis now renders an explicit WireGuard `MTU = 1420` on both the Engine listener and Agent client configs by default, configurable through `BOREALIS_WIREGUARD_MTU`, so jumbo-frame auto-detection cannot stall larger SSH/Ansible transfers on mixed-MTU paths.
- No idle teardown while the agent service is running.
- Agent recovery: if the Windows agent receives the same `tunnel_id` again and its WireGuard service is stopped or unhealthy, it rerenders the config and attempts an in-place service recovery instead of assuming the tunnel is still healthy.
- Agent reuse path: when the agent receives the same active tunnel config, it sends a short outbound UDP probe toward the Engine /32 before returning, so WireGuard can refresh the peer path without restarting the client service.
- Engine recovery: in persistent mode the Engine keeps one Linux WireGuard interface online, updates peers live one at a time during normal connect/disconnect activity, and only falls back to full peer reconciliation when the listener is unhealthy. A watchdog validates listener health every 15 seconds, uses an effective probe grace aligned with WireGuard keepalive timing before declaring `stale_handshake`, and rate-limits full recovery attempts to one every 30 seconds while active sessions exist.
- Linux listener routing: the Engine explicitly restores the configured WireGuard peer-subnet route on `borealis-wg` during listener bring-up and runtime reapply, which keeps `/32` Engine listener addressing able to reach agent `/32` peers after interface repairs.
- Session-scoped transport confirmations: shell output and shell idle-keepalive pongs can refresh tunnel health without requiring visible operator traffic, which keeps quiet RemoteShell sessions from being mistaken for a dead tunnel.
- Port access: the tunnel is trusted end-to-end, and Engine/Agent firewall rules allow a default port allowlist between the Engine /32 and Agent /32 (defaults to 47002, 5900, and 22, configurable via `BOREALIS_WIREGUARD_PORT_ALLOWLIST`). Standard SSH is therefore always available over the managed WireGuard path, while Engine-side remote Ansible can still widen an active session just-in-time for non-default SSH or WinRM transport ports without replacing the default shell/VNC/SSH ports.

## Remote PowerShell
- Uses the WireGuard tunnel and a TCP shell server on the agent.
- UI establishes sessions via `/api/shell/establish` and disconnects via `/api/shell/disconnect`.
- Engine bridges UI Socket.IO events to the agent TCP shell and sends both a readiness ping and idle keepalive pings on quiet sessions.
- The Windows shell bridge now reads PowerShell output with `PeekNamedPipe`/`ReadFile`-style chunk reads instead of waiting on line-buffered pipe behavior, which improves interactive output on Windows agents.
- Engine and agent propagate a shared shell `session_id` so `remote_shell.log` can correlate input, output, keepalive, and close events across both runtimes.
- Engine and agent each keep only one active shell session per browser/session owner and close superseded sessions explicitly.
- Shell port default: 47002 (configurable).

## VNC Viewer
- Engine issues Apache Guacamole VNC session info via `/api/vnc/establish`. The optional `viewer` field accepts `guacamole` and defaults to `guacamole`.
- Apache Guacamole connects through `/remote-desktop/vnc/guacamole`; the browser receives only a Borealis one-time token, while the Engine injects the UltraVNC host, port, and password into local `guacd`.
- `GET /api/vnc/viewers` reports Guacamole availability. Remote Desktop is available only when `guacd` is enabled and reachable.
- Guacamole v1 is VNC-only. RDP, SSH, and Telnet protocol plugins are not exposed.
- Borealis keeps one shared interactive collaboration session per device. Everyone who joins the session can type, click, and interact concurrently.
- VNC authentication is handled by a single shared UltraVNC password plus a Borealis one-time session token for the WebSocket proxy.
- The Windows agent generates that UltraVNC password when the VNC role starts, rotates it again every 24 hours by default (`BOREALIS_VNC_CREDENTIAL_ROTATION_SECONDS`), keeps it in memory only instead of persisting it into `vnc_state.json`, and re-advertises it to the Engine through `POST /api/agent/vnc/ensure`.
- Engine collaboration state reuses the currently advertised agent password across VNC sessions until the agent restarts, reboots, or the next agent-side daily credential rotation publishes a new revision.
- Agent runs UltraVNC as a Windows service; once the agent has its current controller credential and Engine /32 firewall scope, Borealis keeps the VNC listener running continuously instead of standing it down between sessions, and `/api/vnc/disconnect` now makes the caller leave the collaboration session or closes it entirely when requested.
- `POST /api/vnc/handoff` remains available only to reassign the session owner metadata; it no longer forces reconnects or changes who can interact. `GET /api/vnc/sessions` exposes active-session inventory for the WebUI and admin/server overview.
- `POST /api/agent/vnc/ensure` now returns readiness detail (`ready`, `service_state`, `listener_state`, `last_ready_at`, and session metadata) so the Engine can wait for the listener before minting browser bootstrap data.
- Before the proxy connects, the Engine prewarms the cached WireGuard path for registered agents, then fast-probes the agent's advertised UltraVNC listener; prewarmed fast probes use a slightly longer window (`BOREALIS_VNC_PREWARM_FAST_READY_WAIT_SECONDS`, default 2.0 seconds) so the Agent path-prime acknowledgement can land before the Engine falls back to `reason=vnc_bootstrap`.
- After soft browser disconnects (`operator_disconnect` and `component_unmount`), the Windows VNC role preserves a short reconnect-grace snapshot (default 45 seconds via `BOREALIS_VNC_DISCONNECT_GRACE_SECONDS`) for session metadata, but the UltraVNC listener itself now stays running instead of dropping to standby.
- When the last participant disconnects without explicitly closing the session, the Engine now retains that collaboration session briefly so a quick reconnect can reuse the same VNC password instead of forcing a brand-new UltraVNC restart.
- Fast-path VNC establish now uses a short optimistic probe window (`BOREALIS_VNC_FAST_READY_WAIT_SECONDS`, default 0.75 seconds, with `BOREALIS_VNC_FAST_READY_POLL_INTERVAL_SECONDS`, default 0.15 seconds) so healthy listeners connect without restarting WireGuard or UltraVNC.
- If the Engine does have to fall back to `reason=vnc_bootstrap`, the optional settle delay (`BOREALIS_VNC_BOOTSTRAP_SETTLE_SECONDS`) now defaults to `0.0` seconds instead of pausing every new session by default.
- If the backend listener does not become reachable after the readiness wait, the proxy escalates once with `reason=vnc_connect_retry`, and Borealis now applies an agent-level cooldown so closely spaced browser retries do not each force another shared transport recovery.
- The initial backend readiness window now defaults to 12 seconds (`BOREALIS_VNC_READY_WAIT_SECONDS`) and the post-recovery retry window defaults to 8 seconds (`BOREALIS_VNC_RETRY_READY_WAIT_SECONDS`), which makes normal UltraVNC startup less likely to be mistaken for a WireGuard failure.
- When the backend VNC TCP socket finally opens, Borealis confirms transport success with `reason=vnc_backend_connect` so a successful viewer bootstrap counts as real tunnel health.

## Public Edge Configuration
Borealis expects the public HTTPS identity to live on the embedded Traefik instance running on the engine host.
- The supported public entrypoint is the embedded Borealis Traefik + Let's Encrypt edge.
- Borealis web traffic, `/api`, `/socket.io`, and Guacamole VNC all stay on that same HTTPS origin.
- WireGuard remains a separate direct UDP service on port `30000`; it is not terminated or proxied through the HTTPS edge.
- Do not configure a separate public VNC endpoint. Borealis VNC stays same-origin at `/remote-desktop/vnc`.

## API Endpoints
- `POST /api/tunnel/connect` (Token Authenticated) - ensure WireGuard tunnel material for an agent.
- `GET /api/tunnel/status` (Token Authenticated) - tunnel status by agent, including `listener_healthy`, `recovery_in_progress`, `last_recovery_attempt_at`, and `last_recovery_attempt_at_iso`.
- `GET /api/tunnel/active` (Token Authenticated) - list active tunnels with the same listener-health fields.
- `POST /api/agent/vpn/ensure` (Device Authenticated) - agent-side persistent tunnel bootstrap.
- `POST /api/agent/vnc/ensure` (Device Authenticated) - ensure VNC readiness, advertise the agent's current boot-scoped UltraVNC credential, and return active session metadata.
- `GET /api/vnc/viewers` (Token Authenticated) - report Apache Guacamole VNC availability.
- `POST /api/vnc/establish` (Token Authenticated) - establish or join an Apache Guacamole VNC collaboration session.
- `POST /api/vnc/disconnect` (Token Authenticated) - leave or close a VNC collaboration session.
- `POST /api/vnc/handoff` (Token Authenticated) - reassign session-owner metadata inside a shared VNC collaboration session.
- `GET /api/vnc/sessions` (Token Authenticated) - list active VNC collaboration sessions.
- `POST /api/shell/establish` (Token Authenticated) - establish remote shell session.
- `POST /api/shell/disconnect` (Token Authenticated) - disconnect remote shell session.

## Related Documentation
- [Device Management](device-management.md)
- [Agent Runtime](agent-runtime.md)
- [Security and Trust](security-and-trust.md)
- [API Reference](api-reference.md)
- [Reverse Proxy Functionality](features_to_implement/reverse_proxy_functionality.md)

## Codex Agent (Detailed)
### Core Engine files
- Tunnel service: `Data/Engine/services/VPN/vpn_tunnel_service.py`.
- WireGuard server manager: `Data/Engine/services/VPN/wireguard_server.py`.
- Tunnel API: `Data/Engine/services/API/devices/tunnel.py`.
- Shell bridge: `Data/Engine/services/WebSocket/vpn_shell.py`.
- VNC session API: `Data/Engine/services/API/devices/vnc.py`.
- VNC collaboration manager: `Data/Engine/services/RemoteDesktop/vnc_sessions.py`.
- VNC proxy: `Data/Engine/services/RemoteDesktop/vnc_proxy.py`.
- Guacamole bridge: `Data/Engine/services/RemoteDesktop/guacamole_proxy.py`.

### Core Agent files
- WireGuard client role: `Data/Agent/Roles/role_system_wireguard.py`.
- Remote shell role: `Data/Agent/Roles/role_system_remote_shell.py`.
- VNC role: `Data/Agent/Roles/role_system_vnc.py`.

### Config paths
- Engine WireGuard config: `Engine/WireGuard/borealis-wg.conf`.
- Agent WireGuard config: `Agent/Borealis/Settings/WireGuard/Borealis.conf`.
- Engine WireGuard keys: `Engine/Certificates/VPN_Server/`.
- Agent WireGuard keys: `Agent/Borealis/Certificates/VPN_Client/`.

### Service names (Windows agent)
- Agent tunnel service: `WireGuardTunnel$Borealis`.
- Adapter name in Control Panel: `Borealis`.
- Display names:
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
3) Engine opens a TCP connection to agent shell on port 47002 and sends a readiness ping.
4) Agent starts interactive PowerShell, returns a ready pong, and both sides pin a shared `session_id` for log correlation.
5) UI sends `vpn_shell_send`; Engine forwards to agent over TCP and records per-message timing.
6) Agent returns stdout chunks immediately; Engine emits `vpn_shell_output` and uses output plus idle keepalive pongs to refresh tunnel health.
7) If a new shell replaces an older shell for the same browser SID or agent, Engine and agent close the superseded session explicitly.

### Firewall rules
- Engine allows the configured port allowlist from the agent /32 (TCP/UDP) over the WireGuard tunnel.
- Agent allows the configured port allowlist from the Engine /32 (TCP/UDP) over the WireGuard tunnel.

### Persistent tunnel behavior
- WireGuard sessions stay online while the agent service is running.
- There are no tunnel-disconnect endpoints; only role-level disconnects (shell/VNC) exist.
- The tunnel and per-device firewall rules remain in place while the agent runs.
- Keepalive is handled by WireGuard (`PersistentKeepalive = 30` seconds).
- The agent periodically calls `/api/agent/vpn/ensure` to heal the tunnel if it drops.
- On Windows agents, same-session ensure calls also recover a stopped `WireGuardTunnel$Borealis` service instead of returning early when the `tunnel_id` matches.
- Same-session ensure calls send a short outbound UDP probe to the Engine /32 before returning from reuse, which wakes WireGuard peer state for shell/VNC retries without churning the tunnel service.
- The Engine listener watchdog keeps shared listener state honest for all sessions, mutates peers live on the persistent interface during routine changes, uses an effective probe grace aligned with the WireGuard keepalive window before declaring `stale_handshake`, and marks status APIs as recovering while full peer reconciliation is underway.
- Quiet shell sessions no longer depend on operator traffic alone; shell keepalive pongs and shell output can confirm transport health between commands.
- VNC still shares the same listener recovery path, but Borealis now keeps UltraVNC continuously available between sessions, fast-probes the backend before reissuing bootstrap events, and uses a longer default readiness wait before `vnc_connect_retry`, so the shared listener is less likely to churn during normal reconnects.

### Logs to inspect
- Engine tunnel log: `Engine/Logs/VPN_Tunnel/tunnel.log`.
- Engine shell log: `Engine/Logs/VPN_Tunnel/remote_shell.log`.
- Agent tunnel log: `Agent/Logs/VPN_Tunnel/tunnel.log`.
- Agent shell log: `Agent/Logs/VPN_Tunnel/remote_shell.log`.
- Shell logs now carry a shared `session_id` plus distinct ready/keepalive pong entries, which makes it easier to line up one shell session across Engine and agent logs.
- Useful recovery keywords include `shell_connect_retry`, `shell_keepalive`, `vnc_bootstrap`, `vnc_connect_retry`, `vnc_backend_connect`, `vpn_transport_watchdog_recovery`, and `vpn_shell_output_timing`.

### Troubleshooting checklist
- Confirm WireGuard service is running (Engine and Agent).
- Confirm the agent successfully calls `/api/agent/vpn/ensure` after boot.
- Confirm `/api/tunnel/status` returns `status=up` and `agent_socket=true` for a healthy tunnel, and inspect `listener_healthy` / `recovery_in_progress` when the transport is degraded.
- On the Engine, verify peer-subnet routing lands on the WireGuard interface: `ip route get <agent_vpn_ip>` should resolve to `dev borealis-wg`, not the default LAN gateway.
- Verify the WireGuard interface MTU is clamped to the expected value on both ends: `ip -d link show borealis-wg` (Engine) and `ip -d link show borealis` (Linux Agent) should normally report `mtu 1420` unless `BOREALIS_WIREGUARD_MTU` has been intentionally overridden.
- Verify `Agent/Borealis/Settings/WireGuard/Borealis.conf` during an active session.
- Test TCP shell reachability: `Test-NetConnection <agent_vpn_ip> -Port 47002`.

### Known limitations
- Legacy WebSocket tunnels are retired; only WireGuard is supported.
- VNC requires UltraVNC running on the agent.
- The Engine still uses one shared WireGuard listener/interface, so a true interface-level failure remains a shared outage until the watchdog or operator recovery path restores it.
- On weaker agents, VNC can still expose residual transport issues, but the proxy now keeps UltraVNC continuously available between sessions, fast-probes healthy listeners before bootstrap, confirms successful backend connects as transport success, and avoids repeated forced recovery churn per browser session; see GitHub issues labeled `Technical Debt` for any remaining field issues.

### Reverse VPN Tunnels (WireGuard) - Full Reference
#### 1) High-level model
- Outbound-only: agents establish WireGuard tunnels to the Engine; no inbound access on devices.
- Transport: WireGuard/UDP on port 30000.
- Sessions: one persistent VPN tunnel per agent; multiple operators share it.
- Routing: host-only /32 per agent; AllowedIPs restricted to the agent /32 and engine /32; no client-to-client.
- MTU: Borealis sets an explicit WireGuard MTU of `1420` by default on both sides (override with `BOREALIS_WIREGUARD_MTU`) instead of relying on host/path auto-discovery, because oversized PMTU values can let interactive SSH succeed while larger staged payloads stall indefinitely.
- Keepalive: `PersistentKeepalive = 30` seconds; tunnels stay up while agents run.
- Keys: WireGuard server keys under `Engine/Certificates/VPN_Server`; client keys under `Agent/Borealis/Certificates/VPN_Client`.

#### 2) Engine components
- Orchestrator: `Data/Engine/services/VPN/vpn_tunnel_service.py`
  - Allocates per-agent /32, issues short-lived orchestration tokens, enforces single-session.
  - Keeps the WireGuard listener online, applies firewall rules, and avoids idle teardown in persistent mode.
  - Mutates peers one at a time during normal session adds/removals and uses full peer reconciliation only for bootstrap or unhealthy-listener recovery.
  - Runs the persistent-mode listener watchdog, records the last recovery attempt timestamp, and throttles full recovery attempts to avoid restart storms.
  - Emits Socket.IO events: `vpn_tunnel_start`, `vpn_tunnel_stop`, `vpn_tunnel_activity`.
- WireGuard manager: `Data/Engine/services/VPN/wireguard_server.py`
  - Generates server keys, bootstraps the Linux interface, restores the peer-subnet route on Linux listener ensure/reapply, mutates peers live with `wg set`, checks listener health, and applies allowlist-based firewall rules.
- PowerShell bridge: `Data/Engine/services/WebSocket/vpn_shell.py`
  - Proxies UI shell input/output to the agent's TCP shell server over WireGuard.
- Logging: `Engine/Logs/VPN_Tunnel/tunnel.log`; persistent WireGuard tunnel lifecycle is intentionally suppressed from Device Activity history, and shell I/O is in `Engine/Logs/VPN_Tunnel/remote_shell.log`.

#### 3) API endpoints
- `POST /api/tunnel/connect` -> issues or reuses session material (tunnel_id, token, virtual_ip, endpoint).
- `GET /api/tunnel/status` -> returns up/down status for an agent plus `listener_healthy`, `recovery_in_progress`, `last_recovery_attempt_at`, and `last_recovery_attempt_at_iso`.
- `GET /api/tunnel/active` -> lists active VPN tunnel sessions (tunnel_id, agent_id, virtual_ip, last_activity, etc.) plus the same shared listener-health fields.
- `POST /api/agent/vpn/ensure` -> device-authenticated tunnel bootstrap for persistent mode.
- `POST /api/agent/vnc/ensure` -> device-authenticated VNC readiness check, active session bootstrap, and agent credential advertisement for the Windows agent.
- `GET /api/vnc/viewers` -> report Apache Guacamole VNC availability.
- `POST /api/shell/establish` -> establish remote shell session.
- `POST /api/shell/disconnect` -> disconnect remote shell session.
- `POST /api/vnc/establish` -> establish or join an Apache Guacamole VNC collaboration session.
- `POST /api/vnc/disconnect` -> leave or close a VNC collaboration session.
- `POST /api/vnc/handoff` -> reassign session-owner metadata.
- `GET /api/vnc/sessions` -> list active VNC collaboration sessions.

#### 4) Agent components
- Tunnel lifecycle: `Data/Agent/Roles/role_system_wireguard.py`
  - Validates orchestration tokens, starts WireGuard client service, keeps the tunnel persistent, and retries same-session service recovery when the watchdog finds the Windows WireGuard service stopped.
- Shell server: `Data/Agent/Roles/role_system_remote_shell.py`
- TCP PowerShell server bound to `0.0.0.0:47002`, restricted to VPN subnet (10.255.x.x).
- Logging: `Agent/Logs/VPN_Tunnel/tunnel.log` (tunnel lifecycle) and `Agent/Logs/VPN_Tunnel/remote_shell.log` (shell I/O).

#### 5) Security and auth
- Agent HTTPS trust uses the public CA chain plus hostname validation for the Borealis FQDN.
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
- Host OS: Linux (Engine host).
- Remote Agent: mounted read-only at Z:\ (maps to C:\Borealis on the remote device; logs/configs under Z:\Agent\...).
- Agent and Engine launch:
  - Engine: `Borealis.sh` on Linux.
  - Agent (Windows): `Borealis.ps1` (or `bootstrap.ps1` -> `Borealis.ps1`) with elevation.
- Network: Engine on 10.0.0.54; remote agent uses server_url.txt to derive endpoint host.
- WireGuard tooling:
  - Engine host: `wg`, `ip`, and `wg-quick` for first-interface bootstrap only.
  - Windows agent host: `wireguard.exe` + `wg.exe`.

#### Desired behavior
- Agent has a dedicated WireGuard adapter named "Borealis".
- Adapter provisioning is idempotent: if "Borealis" exists, do not recreate it.
- Configs must live inside the project root:
  - Agent: Agent\Borealis\Settings\WireGuard\Borealis.conf
  - Engine: Engine\WireGuard\borealis-wg.conf
- Agent ensures the WireGuard tunnel on boot via `/api/agent/vpn/ensure`, then remote shell/VNC/SSH flow through it.
- After the agent applies the tunnel config and local firewall allowlist, it calls `/api/agent/vpn/ready`. Engine-side shared Ansible waits for that active-tunnel readiness signal before admitting SSH/WinRM targets into generated inventories.
- No idle teardown; tunnels and firewall rules stay in place while the agent is running.

#### Recent changes (current repo state)
- Data/Agent/Roles/role_system_wireguard.py
  - Lazy client init (avoid side effects on import).
  - Service name fix: WireGuard tunnel service is "WireGuardTunnel$Borealis".
  - Endpoint override: if Engine sends localhost, use host from server_url.txt and port from the token.
  - Config path preference: Agent\Borealis\Settings\WireGuard.
  - Service display name set to "Borealis - WireGuard - Agent".
  - Persistent tunnels with `PersistentKeepalive = 30`.
  - Applies an allowlist firewall rule using the engine /32 from allowed_ips and the Engine allowlist payload.
  - Reports active tunnel readiness to `/api/agent/vpn/ready` after the service/config/firewall path is applied.
- Data/Engine/services/VPN/wireguard_server.py
  - Engine config path: Engine\WireGuard\borealis-wg.conf (project root only).
  - Removed invalid "SaveConfig = false" line (WireGuard rejected it).
  - Keeps one Engine listener interface online and mutates peers live instead of restarting the shared interface for routine session changes.
- Data/Engine/services/VPN/vpn_tunnel_service.py
  - Uses an effective probe grace aligned with the WireGuard keepalive window before declaring `stale_handshake`.
  - Logs probe/confirmed/handshake ages during watchdog recovery to make transport failures easier to localize.
  - Throttles repetitive `shell_keepalive` confirmation logs so healthy quiet shells do not flood `tunnel.log`.
  - Records agent-side readiness for active tunnels and exposes `dispatch_ready` for scheduled SSH/WinRM dispatch.
- Data/Engine/services/WebSocket/vpn_shell.py
  - Adds readiness probes, idle shell keepalive pings, output timing diagnostics, and transport confirmations on shell output.
  - Tracks explicit close reasons (`close_request`, `superseded_sid`, `superseded_agent_session`, `ready_probe_failed`) so intentional closes no longer look like transport errors.
  - Replaces superseded shell sessions for the same browser SID or agent instead of allowing overlapping stale sessions to linger.
- Data/Agent/Roles/role_system_remote_shell.py
  - Replaces line-buffered Windows pipe reads with `PeekNamedPipe`-based chunk reads for interactive PowerShell output.
  - Propagates `session_id` into shell control and data frames for Engine/agent log correlation.
  - Logs readiness pongs and idle keepalive pongs separately and throttles idle keepalive log spam on the agent.
  - Closes superseded shell TCP sessions when a newer shell for the same agent connects.
- Data/Engine/services/API/devices/vnc.py and Data/Engine/services/RemoteDesktop/vnc_proxy.py
  - VNC establish first fast-probes the backend listener and only re-emits tunnel startup with `reason=vnc_bootstrap` when the listener is not already reachable.
  - Backend VNC connect retries only escalate with `reason=vnc_connect_retry` after the connect has been stalled for several seconds, the proxy bounds that forced recovery to one request per browser session, and an agent-level cooldown suppresses stacked recoveries from overlapping browser retries.
  - Successful backend VNC TCP connects confirm transport with `reason=vnc_backend_connect`.
  - The VNC backend writer socket enables `TCP_NODELAY`.

Note: Data/Agent changes only apply after Borealis.ps1 re-stages the agent under Agent\.

#### Current operational notes (2026-03-31)
- `LAB-OPERATOR-01` shell and VNC are generally interactive in fresh tests, and intentional shell closes now end with `vpn_shell_closed ... reason=close_request` instead of warning-level post-close read errors.
- `LAB-CAMERA-01` shell and VNC are functional, and the current proxy now delays and bounds VNC recovery escalation per browser session; if you still see repeated `vnc_connect_retry` after redeploying updated Engine code, treat that as a fresh regression.
- Shell timing investigation should rely on `vpn_shell_output_timing`, `vpn_shell_ready_pong`, `vpn_shell_closed`, agent-side `session_id`, and tunnel-side `shell_keepalive` / watchdog recovery events rather than browser symptoms alone.

#### Key paths
- Agent WireGuard role: Data/Agent/Roles/role_system_wireguard.py
- Agent VPN shell role: Data/Agent/Roles/role_system_remote_shell.py
- Engine WireGuard manager: Data/Engine/services/VPN/wireguard_server.py
- Engine tunnel service: Data/Engine/services/VPN/vpn_tunnel_service.py
- Engine shell bridge: Data/Engine/services/WebSocket/vpn_shell.py
- Engine VNC API: Data/Engine/services/API/devices/vnc.py
- Engine VNC proxy: Data/Engine/services/RemoteDesktop/vnc_proxy.py
- Agent tunnel logs: Z:\Agent\Logs\VPN_Tunnel\tunnel.log
- Agent shell logs: Z:\Agent\Logs\VPN_Tunnel\remote_shell.log
- Engine tunnel logs: Engine\Logs\VPN_Tunnel\tunnel.log
- Engine shell logs: Engine\Logs\VPN_Tunnel\remote_shell.log
- Agent WireGuard config: Z:\Agent\Borealis\Settings\WireGuard\Borealis.conf
- Engine WireGuard config: Engine\WireGuard\borealis-wg.conf

#### Known WireGuard services and names
- Engine interface name (Linux): "borealis-wg"
- Agent tunnel service name: "WireGuardTunnel$Borealis"
- Adapter name in Control Panel: "Borealis"
- Service display names:
  - "Borealis - WireGuard - Agent"

#### Suggested verification commands
- Engine service status:
  - `sudo ip link show dev borealis-wg`
  - `sudo wg show borealis-wg`
  - `sudo ss -lunp | grep :30000`
- Engine WireGuard log tail:
  - `tail -f Engine/Logs/VPN_Tunnel/tunnel.log`
- Agent tunnel state (remote, via Z:\ logs):
  - Z:\Agent\Logs\VPN_Tunnel\tunnel.log
  - Z:\Agent\Logs\VPN_Tunnel\remote_shell.log
  - Z:\Agent\Borealis\Settings\WireGuard\Borealis.conf

#### Current blockers and next steps
1) Re-stage the agent runtime after `role_system_remote_shell.py`, `role_system_wireguard.py`, or `role_system_vnc.py` changes so the runtime copy matches `Data/Agent/`.
2) For shell regressions, correlate `Engine/Logs/VPN_Tunnel/remote_shell.log` with `Z:\Agent\Logs\VPN_Tunnel\remote_shell.log` by `session_id` before assuming browser-side buffering.
3) For VNC regressions on weaker hosts, capture `vnc_connect_retry`, `vnc_backend_connect`, `vpn_transport_recovery_request`, and `vpn_transport_watchdog_recovery` from `Engine/Logs/VPN_Tunnel/tunnel.log` before changing shell code paths.
4) If issues persist, confirm `Agent\Borealis\Settings\WireGuard\Borealis.conf` still has a valid [Peer], verify `Test-NetConnection -ComputerName <agent_vpn_ip> -Port 47002`, and re-check WireGuard service state on both ends.
