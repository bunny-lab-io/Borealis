# Borealis WireGuard Troubleshooting Handoff

This file is a self-contained handoff prompt + context for a new Codex agent to resume WireGuard tunnel troubleshooting.

## Prompt to Use in a New Codex Session

Copy/paste the prompt below into a new Codex chat:

"""
You are a new Codex agent working in d:\Github\Borealis. Please do the following:

1) Read AGENTS.md, Docs/Codex/BOREALIS_AGENT.md, Docs/Codex/BOREALIS_ENGINE.md, Docs/Codex/REVERSE_TUNNELS.md, then Docs/Codex/WireGuard_Troubleshooting.md.
2) Investigate why the WireGuard tunnel does not come up (remote shell timeouts) even though the Engine emits vpn_tunnel_start.
3) Focus on the WireGuard client lifecycle in Data/Agent/Roles/role_WireGuardTunnel.py and the bootstrap logic in Borealis.ps1 (WireGuard adapter provisioning).
4) Use Data/Agent for edits (runtime under Agent/ is ephemeral). Keep the adapter name "Borealis" and ensure idempotent behavior. Do not rely on the PIA adapter.
5) Provide concrete fixes + verification steps. Be careful with Windows services and avoid GUI popup dialogs when possible.
"""

## Environment / Scope

- Workspace: d:\Github\Borealis
- Host OS: Windows 10/11 (build 26200). Current tests run on the Windows 11 machine that also runs Engine + Agent.
- Agent/Engine launch: via Borealis.ps1, always elevated as admin.
- Network: Engine + Agent run on the same host during testing (Engine endpoint is "localhost:30000").
- WireGuard version: wireguard.exe 0.5.3, wg.exe 1.0.20210914.
- PIA (Private Internet Access) is installed and supplies a wintun driver (pia-wintun.sys). Do NOT treat the PIA adapter as the Borealis adapter.

## Desired Behavior

- Agent has a dedicated WireGuard adapter named "Borealis" (Description shows "WireGuard Tunnel").
- Adapter provisioning is idempotent: if "Borealis" exists, do not recreate it.
- WireGuard config should be stored under Agent\Borealis\Settings\WireGuard\Borealis.conf (preferred) and not only in Program Files.
- Agent should bring up the WireGuard tunnel on vpn_tunnel_start, then remote shell / RDP / VNC / SSH should flow through it.
- On stop/idle, the tunnel should be torn down and firewall rules removed.

## Recent Changes (Current Repo State)

- Data/Agent/Roles/role_WireGuardTunnel.py
  - Service name fix: WireGuard tunnel service is "WireGuardTunnel$Borealis".
  - Config path preference: Agent\Borealis\Settings\WireGuard.
  - Uses registry ImagePath to locate the actual service config when needed.
  - Adds a session lock to prevent concurrent start/stop.
- Borealis.ps1
  - WireGuard config search order includes Agent\Borealis\Settings\WireGuard.
  - Adapter provisioning reads the service ImagePath to write config when service exists.
  - Avoids /installtunnelservice if service still present to prevent GUI error dialogs.
  - Adapter name is "Borealis".

Note: Data/Agent changes only apply to runtime after Borealis.ps1 re-stages the agent under Agent/.

## Symptoms from Fresh Logs (2026-01-12 19:29)

Agent (Agent/Logs/VPN_Tunnel/tunnel.log):
- "WireGuard tunnel service already installed; skipping install."
- "WireGuard tunnel service still missing after install attempt."

Engine (Engine/Logs/VPN_Tunnel/tunnel.log):
- vpn_tunnel_session_create for agent LAB-OPERATOR-01_..._SYSTEM
- WireGuard listener installed (service=borealis-wg)
- vpn_api_status_response status=up

Engine (Engine/Logs/VPN_Tunnel/remote_shell.log):
- repeated vpn_shell_connect_attempt to 10.255.0.2:47002
- timeouts

Agent (Agent/Logs/VPN_Tunnel/remote_shell.log):
- VPN shell server listening on 0.0.0.0:47002

Net effect: engine believes tunnel is "up", but remote shell cannot reach 10.255.0.2. This implies the WireGuard client tunnel is not actually up on the agent.

## Key Paths

- Agent WireGuard role: Data/Agent/Roles/role_WireGuardTunnel.py
- Agent VPN shell role: Data/Agent/Roles/role_VpnShell.py
- Engine WireGuard manager: Data/Engine/services/VPN/wireguard_server.py
- Engine tunnel service: Data/Engine/services/VPN/vpn_tunnel_service.py
- Agent tunnel logs: Agent/Logs/VPN_Tunnel/tunnel.log
- Agent shell logs: Agent/Logs/VPN_Tunnel/remote_shell.log
- Engine tunnel logs: Engine/Logs/VPN_Tunnel/tunnel.log
- Engine shell logs: Engine/Logs/VPN_Tunnel/remote_shell.log
- Agent WireGuard config: Agent/Borealis/Settings/WireGuard/Borealis.conf

## Known WireGuard Services / Names

- Engine listener service name: "borealis-wg"
- Agent tunnel service name: "WireGuardTunnel$Borealis"
- Adapter name in Control Panel: "Borealis"

## Suggested Verification Commands

- Check agent service:
  - Get-Service -Name "WireGuardTunnel$Borealis"
  - Get-ItemProperty "HKLM:\\SYSTEM\\CurrentControlSet\\Services\\WireGuardTunnel$Borealis" | Select-Object ImagePath
- Confirm adapter exists:
  - Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -like "*WireGuard*" } | Select-Object Name, Status, InterfaceDescription, ifIndex
- Check WireGuard state:
  - "C:\\Program Files\\WireGuard\\wg.exe" show

## Troubleshooting Focus Areas

- Ensure runtime is up-to-date (Borealis.ps1 re-staging Data/Agent -> Agent/).
- Validate service detection vs. WireGuard install output (sc.exe vs registry).
- Confirm the config file used by the service matches Agent/Borealis/Settings/WireGuard/Borealis.conf.
- Confirm /installtunnelservice is not invoked when service already exists (avoid WireGuard GUI errors).
- Confirm the WireGuard tunnel actually connects (wg.exe show handshake) before attempting remote shell.

