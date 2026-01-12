# Borealis Reverse VPN Tunnel Work — Handoff Prompt

You are resuming work on Borealis' WireGuard-based reverse VPN tunnel migration in
`d:\Github\Borealis`. You should assume no prior context. Start by reading `AGENTS.md`
and these docs (order matters):

- `Docs/Codex/BOREALIS_AGENT.md`
- `Docs/Codex/BOREALIS_ENGINE.md`
- `Docs/Codex/SHARED.md`
- `Docs/Codex/USER_INTERFACE.md`
- `Docs/Codex/Reverse_VPN_Tunnel_Deployment.md`

Do not implement Linux yet.

## Current Status (What Is Working)

- WireGuard tunnel comes up and the PowerShell VPN shell connects successfully.
- Agent log confirms: start request received, client config rendered, session started,
  and a shell connection accepted from `10.255.0.2`.
- Engine log shows WireGuard listener installed, firewall rules applied, device
  activity started.

## Key Fixes Already Applied

1) Port conflict fix
   - Default VPN shell port changed from `47001` to `47002`.
   - Updated in:
     - `Data/Engine/config.py`
     - `Data/Agent/Roles/role_VpnShell.py`
     - `Data/Engine/web-interface/src/Devices/Device_Details.jsx`
     - `Docs/Codex/REVERSE_TUNNELS.md`

2) Agent role load/import failures resolved
   - WireGuard role was failing to load due to `signature_utils` import path and a
     dataclass crash.
   - Added `sys.path` insertions in role manager to make helpers importable:
     - `Data/Agent/role_manager.py`
     - `Agent/Borealis/role_manager.py`
   - Added fallback import in WireGuard role:
     - `Data/Agent/Roles/role_WireGuardTunnel.py`
     - `Agent/Borealis/Roles/role_WireGuardTunnel.py`
   - Replaced `@dataclass SessionConfig` with a plain class in both roles to avoid
     `AttributeError: 'NoneType' object has no attribute '__dict__'`.

3) VPN shell read-loop noise suppressed
   - The engine threw `TimeoutError` on idle shell reads; now handled cleanly.
   - Updated in `Data/Engine/services/WebSocket/vpn_shell.py`:
     - `tcp.settimeout(15)`
     - Catch `socket.timeout` and `TimeoutError` and exit loop cleanly.

## Logs to Know

- Agent: `Agent/Logs/VPN_Tunnel/tunnel.log` (tunnel lifecycle) and `Agent/Logs/VPN_Tunnel/remote_shell.log` (shell I/O).
- Engine: `Engine/Logs/VPN_Tunnel/tunnel.log`, `Engine/Logs/VPN_Tunnel/remote_shell.log`, `Engine/Logs/engine.log`.

## What Likely Remains

- Ensure Section 7 (End-to-End Validation) in
  `Docs/Codex/Reverse_VPN_Tunnel_Deployment.md` has accurate `[x]` checkboxes for
  completed tests.
- Confirm UI/PowerShell web terminal behaves as expected (live output, disconnect
  cleanup, idle timeout).
- Validate no legacy tunnel references remain (if any cleanup missing).
- Update docs/checklists if any step is now complete or needs clarification.

## Important File Paths Touched

- `Data/Engine/config.py`
- `Data/Agent/Roles/role_VpnShell.py`
- `Data/Agent/Roles/role_WireGuardTunnel.py`
- `Agent/Borealis/Roles/role_WireGuardTunnel.py`
- `Data/Agent/role_manager.py`
- `Agent/Borealis/role_manager.py`
- `Data/Engine/web-interface/src/Devices/Device_Details.jsx`
- `Docs/Codex/REVERSE_TUNNELS.md`
- `Data/Engine/services/WebSocket/vpn_shell.py`

## Environment Notes

- Shell: PowerShell
- `approval_policy=never` (do not request escalations)
- `sandbox_mode=danger-full-access`

## Suggested Verification Steps

- Re-run UI PowerShell connect and confirm live terminal works.
- Check agent log for:
  - `WireGuard start request received`
  - `WireGuard client session started`
  - `Accepted shell connection from 10.255.0.2`
- Check engine log for:
  - `WireGuard listener installed`
  - No `Failed to connect vpn shell` warnings
  - No `TimeoutError` stack trace after the read-loop fix.

When you continue, keep `Data/Agent` and `Agent/Borealis` copies in sync where
appropriate.
