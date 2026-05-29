# Remote Desktop

Remote Desktop opens a shared browser VNC session to a Windows agent through Borealis-managed WireGuard and Apache Guacamole. Use it when visual support is faster than shell, file, process, or service tools.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Remote_Desktop.png" alt="Borealis Remote Desktop session" loading="lazy">
  <figcaption>Remote Desktop uses browser VNC through Guacamole over the managed WireGuard tunnel.</figcaption>
</figure>

## Launch Session

1. Open a Windows device.
2. Open `Remote Desktop`.
3. Select `Launch Remote Desktop`.
4. Wait for readiness checks.
5. Work in the browser viewer.

If another operator already has the device open, Borealis joins the same shared collaboration session.

## Use Session Controls

- Reconnect if the browser stream drops after it was already ready.
- Adjust speed/quality preference when bandwidth is constrained.
- Use Ctrl+Alt+Del from session controls when Windows secure desktop needs it.
- Disconnect when finished. Closing the browser leaves a short reconnect window.

## Availability Rules

- Remote Desktop needs a supported Windows agent.
- Agent must be online with WireGuard and VNC roles healthy.
- `guacd` must be available on the Engine.
- Browser traffic stays same-origin under Borealis HTTPS; no separate public VNC endpoint is used.

!!! tip

    If Remote Desktop is unavailable, check Device Summary Agent Health first, then Server Info for `remote-desktop-guacd` health.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/vnc/viewers` - report Guacamole availability.
    - `POST /api/vnc/establish` - establish or join VNC collaboration session.
    - `POST /api/vnc/disconnect` - leave or close session.
    - `POST /api/vnc/handoff` - reassign session-owner metadata.
    - `GET /api/vnc/sessions` - list active sessions.
    - `POST /api/agent/vnc/ensure` - agent readiness and session metadata.

    ### Related documentation

    - [Remote Shell](remote-shell.md)
    - [Server Info](server-info.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Security and Trust](../Reference/security-and-trust.md)

    ### Source map

    - Remote Desktop UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_Desktop.jsx`
    - VNC API: `Data/Engine/Containers/api-backend/data/services/API/devices/vnc.py`
    - VNC proxy: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/vnc_proxy.py`
    - Guacamole bridge: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/guacamole_proxy.py`
    - Agent VNC role: `Data/Agent/internal/roles/vnc/`

    ### Runtime behavior

    - Engine asks the Agent for the current runtime VNC credential only when establishing a live session.
    - Browser receives a Borealis one-time token, not the UltraVNC password.
    - Guacamole connects through local `guacd`, then to the agent VNC listener over WireGuard.
    - VNC role keeps UltraVNC available after firewall scope and runtime credentials are ready.
