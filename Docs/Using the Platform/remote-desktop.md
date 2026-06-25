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

!!! info

    Slower endpoints can spend extra time on `Authenticating Session` while the Agent finishes VNC service and listener checks. Borealis continues probing before failing so underpowered virtual machines have time to become ready.

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
    - VNC establish performs a fast TCP probe first, then longer recovery and restart probes for slow Agent readiness.
    - Engine VNC readiness waits can be tuned with `BOREALIS_VNC_LIVE_CREDENTIAL_WAIT_SECONDS`, `BOREALIS_VNC_START_READY_WAIT_SECONDS`, `BOREALIS_VNC_RECOVERY_READY_WAIT_SECONDS`, `BOREALIS_VNC_RESTART_READY_WAIT_SECONDS`, `BOREALIS_VNC_AUTH_RETRY_START_READY_WAIT_SECONDS`, and `BOREALIS_VNC_AUTH_RETRY_READY_WAIT_SECONDS`.
    - Engine calls Agent `vnc_start` synchronously through the site-worker before issuing a browser Guacamole token, so stale TCP listener probes cannot race ahead of Agent-side VNC config and service readiness.
    - Browser startup waits give slower site-worker, `guacd`, and framebuffer paths enough time before reconnecting, so failed first frames do not churn new Guacamole tokens every few seconds.
    - Engine debounces Agent `vnc_stop` after operator disconnect with `BOREALIS_VNC_STOP_DEBOUNCE_SECONDS` so quick reconnects do not fight an in-flight UltraVNC stop.
    - Site-worker emits `first_frame` events after Guacamole sees the first display instruction, and the Engine records `first_frame_at` on the shared VNC session snapshot.
    - Guacamole startup treats post-ready backend status `519` as retryable, opens a fresh `guacd` session, and keeps `participant_id` plus token hints in VNC proxy logs for browser-to-Engine correlation.
    - Remote Desktop speed/quality preferences flow from WebUI through the Go VNC broker into the site-worker Guacamole session as `-2` through `2`; the default favors speed for lower-end endpoints.
    - Agent VNC readiness serializes local service checks, gives pending services time to settle, force-kills stuck `STOP_PENDING` UltraVNC processes after grace time, and waits for the listener before reporting ready.
