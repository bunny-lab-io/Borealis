# Remote Shell

Remote Shell opens an interactive PowerShell or shell session over the managed WireGuard tunnel. Use it for focused diagnostics, command-line repairs, and quick inspection when full automation is unnecessary.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Device_Remote_Shell.png" alt="Borealis Remote Shell" loading="lazy">
  <figcaption>Remote Shell bridges operator shell sessions to the agent over the persistent WireGuard tunnel.</figcaption>
</figure>

## Open Shell

1. Open a device.
2. Open `Remote Shell`.
3. Select `Open Shell`.
4. Wait for the tunnel and shell readiness check.
5. Run commands in the terminal area.

Use `Disconnect` when finished.

## Read Status

- `Connection Established` means browser, Engine bridge, WireGuard path, and agent shell listener are connected.
- `Agent Onboarding Underway` means the SYSTEM agent socket is not ready yet. Wait for onboarding to finish, then retry.
- Quiet shell sessions stay connected through keepalive pings even when no visible output is flowing.

## When To Use Other Tools

- Use File Management for browse, upload, download, and edits.
- Use Process Management for process inspection and End Task.
- Use Service Management for service start, stop, and restart.
- Use Scheduled Jobs or Scripts when the same command should run on more than one device.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `POST /api/shell/establish` - prepare shell session for an in-scope device.
    - `POST /api/shell/disconnect` - disconnect shell session.
    - `POST /api/tunnel/connect` - ensure tunnel material for in-scope agent.
    - `GET /api/tunnel/status` - tunnel status by agent.
    - `GET /api/tunnel/active` - active tunnel list.
    - `POST /api/agent/vpn/ensure` - persistent agent tunnel bootstrap.
    - `POST /api/agent/vpn/ready` - active tunnel readiness for dispatch.

    ### Related documentation

    - [Remote Desktop](remote-desktop.md)
    - [Engine Log Access](engine-log-management.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Security Whitepaper](../Reference/security-whitepaper.md)
    - [SSH Connection Logic](../Reference/ssh-connection-logic.md)

    ### Source map

    - Remote Shell UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_Shell.jsx`
    - Shell API: `Data/Engine/Containers/api-backend/cmd/api-backend/remote_shell.go`
    - Shell bridge/runtime: `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker_socket.py` and `Data/Engine/Containers/api-backend/data/services/WebSocket/vpn_shell.py`
    - Tunnel service: `Data/Engine/Containers/api-backend/data/services/VPN/vpn_tunnel_service.py`
    - Agent shell role: `Data/Agent/internal/roles/remote_shell/`
    - Agent WireGuard role: `Data/Agent/internal/roles/wireguard_tunnel/`

    ### Runtime behavior

    - WireGuard tunnel is persistent and shared across operators.
    - Remote shell uses agent TCP shell listener on port `47002` by default.
    - Engine and Agent log a shared shell `session_id` for cross-runtime correlation.
    - One active shell per browser/session owner is kept; superseded sessions close explicitly.
