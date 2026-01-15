# Borealis WireGuard Troubleshooting Handoff

This file is a self-contained handoff prompt + context for a new Codex agent to resume WireGuard tunnel troubleshooting.

## Prompt to Use in a New Codex Session

Copy/paste the prompt below into a new Codex chat:

"""
You are a new Codex agent working in d:\Github\Borealis. Please do the following:

1) Read AGENTS.md, Docs/Codex/BOREALIS_AGENT.md, Docs/Codex/BOREALIS_ENGINE.md, Docs/Codex/REVERSE_TUNNELS.md, then Docs/Codex/WireGuard_Troubleshooting.md (this file).
2) Note environment mapping:
   - D:\Github\Borealis = Engine (this device).
   - Z:\ = Agent (remote device) read-only share for logs/configs.
   - Use Z:\ to read agent logs/configs instead of asking the user to paste them.
3) Confirm the WireGuard listener on the Engine starts and stays running, then confirm the tunnel handshake from the remote agent.
4) Keep all config files inside the project root only:
   - Agent config path: Agent\Borealis\Settings\WireGuard\Borealis.conf
   - Engine config path: Engine\WireGuard\borealis-wg.conf
5) Make edits only in Data/Agent or Data/Engine. The user handles redeploying the Agent runtime on the remote device when needed.
6) If any doc in Docs\Codex is outdated, update it to reflect the current state and blockers.
"""

## Environment / Scope

- Workspace: D:\Github\Borealis (local project root for the Engine)
- Host OS: Windows 10/11 (build 26200). Engine runs on this machine.
- Remote Agent: mounted read-only at Z:\ (maps to C:\Borealis on the remote device; logs/configs under Z:\Agent\...).
- Agent/Engine launch: via Borealis.ps1, always elevated as admin.
- Network: Engine on 10.0.0.54; remote agent uses server_url.txt to derive endpoint host.
- WireGuard version: wireguard.exe 0.5.3, wg.exe 1.0.20210914.
- PIA (Private Internet Access) is installed and supplies a wintun driver (pia-wintun.sys). Do NOT treat the PIA adapter as the Borealis adapter.

## Desired Behavior

- Agent has a dedicated WireGuard adapter named "Borealis".
- Adapter provisioning is idempotent: if "Borealis" exists, do not recreate it.
- Configs must live inside the project root:
  - Agent: Agent\Borealis\Settings\WireGuard\Borealis.conf
  - Engine: Engine\WireGuard\borealis-wg.conf
- Agent brings up the WireGuard tunnel on vpn_tunnel_start, then remote shell/RDP/VNC/SSH flow through it.
- On stop/idle, the tunnel is torn down and firewall rules removed.

## Recent Changes (Current Repo State)

- Data/Agent/Roles/role_WireGuardTunnel.py
  - Lazy client init (avoid side effects on import).
  - Service name fix: WireGuard tunnel service is "WireGuardTunnel$Borealis".
  - Endpoint override: if Engine sends localhost, use host from server_url.txt and port from the token.
  - Config path preference: Agent\Borealis\Settings\WireGuard.
  - Service display name set to "Borealis - WireGuard - Agent".
- Data/Engine/services/VPN/wireguard_server.py
  - Engine config path: Engine\WireGuard\borealis-wg.conf (project root only).
  - Removed invalid "SaveConfig = false" line (WireGuard rejected it).
  - Service display name set to "Borealis - WireGuard - Engine".
  - Ensures the listener service is running after install, and raises if it fails.
- Borealis.ps1
  - Service name interpolation fixed to include the literal "$" in "WireGuardTunnel$Borealis".

Note: Data/Agent changes only apply after Borealis.ps1 re-stages the agent under Agent\.

## Current Symptoms (2026-01-13 23:40)

- `wg.exe show` confirms the tunnel is up with a recent handshake and RX/TX bytes on both Engine and Agent.
- Engine sees the remote agent peer at 10.0.0.55:59733; agent sees the engine endpoint at 10.0.0.54:30000.
- ICMP over the tunnel works: `Test-NetConnection -ComputerName 10.255.0.2 -Port 47002` reports `PingSucceeded=True` but `TcpTestSucceeded=False`.
- Remote shell connects to 10.255.0.2:47002 still time out; agent logs show the shell server listening but no accepted connections.
- Agent session idles out; the on-disk `Borealis.conf` reverts to idle-only [Interface] after stop (no [Peer]).
- `wireguard.exe /dumplog /tail` fails with "Stdout must be set" when run from PowerShell.

## Key Paths

- Agent WireGuard role: Data/Agent/Roles/role_WireGuardTunnel.py
- Agent VPN shell role: Data/Agent/Roles/role_VpnShell.py
- Engine WireGuard manager: Data/Engine/services/VPN/wireguard_server.py
- Engine tunnel service: Data/Engine/services/VPN/vpn_tunnel_service.py
- Agent tunnel logs: Z:\Agent\Logs\VPN_Tunnel\tunnel.log
- Agent shell logs: Z:\Agent\Logs\VPN_Tunnel\remote_shell.log
- Engine tunnel logs: Engine\Logs\VPN_Tunnel\tunnel.log
- Engine shell logs: Engine\Logs\VPN_Tunnel\remote_shell.log
- Agent WireGuard config: Z:\Agent\Borealis\Settings\WireGuard\Borealis.conf
- Engine WireGuard config: Engine\WireGuard\borealis-wg.conf

## Known WireGuard Services / Names

- Engine listener service name: "WireGuardTunnel$borealis-wg"
- Agent tunnel service name: "WireGuardTunnel$Borealis"
- Adapter name in Control Panel: "Borealis"
- Service display names:
  - "Borealis - WireGuard - Engine"
  - "Borealis - WireGuard - Agent"

## Suggested Verification Commands

- Engine service status:
  - Get-Service -Name "WireGuardTunnel$borealis-wg"
  - sc.exe query "WireGuardTunnel$borealis-wg"
  - netstat -ano -p udp | findstr :30000
- Engine WireGuard log tail:
  - cmd /c ""C:\\Program Files\\WireGuard\\wireguard.exe" /dumplog /tail > %TEMP%\\wg-tail.log"
  - powershell -NoProfile -Command "& 'C:\\Program Files\\WireGuard\\wireguard.exe' /dumplog /tail 2>&1 | Out-File $env:TEMP\\wg-tail.log"
- Agent tunnel state (remote, via Z:\ logs):
  - Z:\Agent\Logs\VPN_Tunnel\tunnel.log
  - Z:\Agent\Logs\VPN_Tunnel\remote_shell.log
  - Z:\Agent\Borealis\Settings\WireGuard\Borealis.conf

## Current Blockers / Next Steps

1) During an active session, run `Test-NetConnection -ComputerName 10.255.0.2 -Port 47002` on the Engine and confirm it reaches the agent.
2) If the TCP test times out, inspect agent-side firewall rules; the shell server listens but may be blocked on the WireGuard adapter.
   - Added a candidate fix in `Data/Agent/Roles/role_VpnShell.py` to add an inbound firewall rule for TCP/47002 from 10.255.0.1/32.
3) While the session is active, confirm `Agent\Borealis\Settings\WireGuard\Borealis.conf` includes a [Peer] with endpoint/AllowedIPs (it reverts to idle config after stop).
4) Capture engine + agent tunnel/shell logs around a failed shell open attempt and re-check WireGuard service state.
