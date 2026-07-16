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
- Choose `Prefer Speed` or `Prefer Quality` when bandwidth or visual fidelity matters. Changing this preference reconnects the desktop stream so Guacamole can apply the codec choice.
- Use the always-open display selector or the viewfinder to fit the full desktop or focus one monitor when the Windows endpoint has multiple displays.
- After a display is selected, Borealis switches the viewer to `Fit`, clips video and mouse input to that display, and previews that display region by itself. The viewfinder center-fits multi-display layouts so wide monitor spans stay inside the sidebar preview.
- Use `Fit` or `Scaled` when `Display: All` is selected. Single-display focus uses `Fit` only.
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
    - [Security Whitepaper](../Reference/security-whitepaper.md)

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
    - Engine checks for the VNC RFB banner before opening the browser socket. If the listener accepts TCP but does not speak RFB, startup fails as `vnc_backend_no_rfb_banner` so operators are not left waiting on a Guacamole retry loop.
    - VNC role keeps UltraVNC available after firewall scope and runtime credentials are ready.
    - Agent VNC config sets UltraVNC `[admin]` values `primary=1` and `secondary=1` so multi-monitor Windows endpoints start Guacamole sessions with the full desktop framebuffer instead of primary-only capture.
    - VNC establish performs bounded non-auth RFB banner readiness checks after Agent `vnc_start` reports ready. Engine-side establish work is capped by `BOREALIS_VNC_ESTABLISH_DEADLINE_SECONDS`, defaulting to 30 seconds and clamped to 30 seconds so operators do not wait through multi-minute browser launches.
    - Site-worker registration performs a non-auth RFB security preflight before handing healthy launches to Guacamole. The preflight reads the RFB security-type list only; it does not select VNCAuth, send the UltraVNC password, or consume a login attempt. It fails fast when UltraVNC returns security type `0`, such as `This server does not have a valid password enabled`, so operators are not left waiting on guacd retries that cannot succeed. Password-not-enabled failures return `vnc_password_not_enabled` as a non-retryable session error because Engine credential rotation cannot repair an UltraVNC listener that is not accepting password auth. UltraVNC lockout text returns `vnc_auth_lockout` as a non-retryable session error because another immediate connection attempt extends endpoint-side recovery instead of fixing the listener. Disable with `BOREALIS_VNC_SECURITY_PREFLIGHT=0`; tune the single TCP/read timeout with `BOREALIS_VNC_SECURITY_PREFLIGHT_TIMEOUT_SECONDS`.
    - Site-worker registration can perform a bounded RFB VNCAuth probe before the browser socket opens when `BOREALIS_VNC_AUTH_PROBE=1` or when a caller explicitly sends `auth_probe=true`. Keep this diagnostic off for first attempts because each probe consumes an UltraVNC login attempt and can trigger lockout or credential-rotation recovery on slower endpoints. If the first browser/Guacamole open fails, WebUI enables request-scoped `auth_probe` on the second establish attempt so Engine can distinguish credential/auth failure from target-side Guacamole transport failure. The site-worker and Go broker log structured probe fields including stage, server version, offered security types, selected security type, auth result, framebuffer dimensions, elapsed time, and socket error without logging the VNC password or challenge-response bytes.
    - Engine VNC readiness waits can be tuned with `BOREALIS_VNC_ESTABLISH_DEADLINE_SECONDS`, `BOREALIS_VNC_LIVE_CREDENTIAL_WAIT_SECONDS`, `BOREALIS_VNC_START_READY_WAIT_SECONDS`, `BOREALIS_VNC_RFB_FAST_READY_WAIT_SECONDS`, `BOREALIS_VNC_RFB_READY_WAIT_SECONDS`, `BOREALIS_VNC_SECURITY_PREFLIGHT_TIMEOUT_SECONDS`, `BOREALIS_VNC_AUTH_RETRY_CREDENTIAL_WAIT_SECONDS`, `BOREALIS_VNC_AUTH_RETRY_START_READY_WAIT_SECONDS`, `BOREALIS_VNC_AUTH_RETRY_READY_WAIT_SECONDS`, `BOREALIS_VNC_AUTH_RETRY_COOLDOWN_SECONDS`, and `BOREALIS_VNC_AUTH_LOCKOUT_COOLDOWN_SECONDS`. Per-step waits and auth retry cooldowns are clipped to the active establish deadline.
    - Engine calls Agent `vnc_start` synchronously through the site-worker before issuing a browser Guacamole token, so stale TCP listener probes cannot race ahead of Agent-side VNC config and service readiness.
    - Browser startup has a 30-second operator-facing connection budget and at most two establish attempts per Connect action. The site-worker owns Guacamole backend retries inside that window, so failed first frames do not churn overlapping Guacamole tokens or stack multi-minute VNC attempts against one endpoint.
    - Engine debounces Agent `vnc_stop` after operator disconnect with `BOREALIS_VNC_STOP_DEBOUNCE_SECONDS` so quick reconnects do not fight an in-flight UltraVNC stop.
    - Site-worker emits `first_frame` events after Guacamole sees the first display instruction, and the Engine records `first_frame_at` on the shared VNC session snapshot.
    - Guacamole startup treats post-ready backend status `519` as a target-side Guacamole transport failure after guacd has already tried its configured VNC autoretry path. Borealis keeps Guacamole VNC `autoretry=3` for parity with the pre-hardening Remote Desktop path, but the site-worker does not stack additional fresh guacd sessions for the same `519` failure. If guacd still fails, Engine records a Guacamole transport failure instead of assuming password failure. Guacd mirrors normal daemon output to Docker logs for `borealis-engine-remote-desktop-guacd` and keeps only transient in-container file logs under `/tmp/borealis-guacd-logs`.
    - Explicit VNCAuth diagnostics can report `vnc_auth_failed`. The next establish request uses the existing Agent `vnc_auth_retry` reason so the Agent rotates the runtime VNC credential and rewrites UltraVNC config without changing Agent code. Password-not-enabled preflight failures are kept separate as `vnc_password_not_enabled` and do not start Agent credential rotation.
    - Engine treats `vnc_auth_retry` as single-flight per Agent. While UltraVNC is restarting or settling after credential rotation, additional establish requests receive `vnc_auth_retry_in_progress` or `vnc_auth_retry_settling` with `retry_after_seconds` instead of sending another credential rotation request. Auth retry and UltraVNC lockout settle hints are capped at 30 seconds; after that window, normal launches try the current credential through Guacamole, while explicit diagnostic launches can still request the RFB auth probe.
    - Remote Desktop speed/quality preference is a two-state WebUI toggle. `Prefer Speed` sends performance `-2` and `image_codec=jpeg`; `Prefer Quality` sends performance `2` and `image_codec=png`. The setting flows from WebUI through the Go VNC broker into the site-worker Guacamole session. Changing it during an active or connecting session disconnects the browser stream and reconnects so Guacamole can renegotiate image codec and performance arguments.
    - WebUI display focus is client-side only: Guacamole keeps one full-framebuffer VNC session, while `Remote_Desktop.jsx` positions and scales the Guacamole display element inside an overflow-hidden clip layer to crop one monitor. `Display: All` supports `Fit` and `Scaled`; single-display focus forces `Fit` and clamps mouse coordinates to the selected display.
    - Windows Agent display topology prefers active monitor geometry from `EnumDisplayMonitors` when that data is at least as complete as `EnumDisplaySettingsEx`. This preserves physical positions such as secondary monitors to the left of the primary monitor instead of trusting a stale or flattened display-settings layout.
    - When live Agent topology only reports one display but the Guacamole framebuffer is clearly wider, WebUI uses `display_virtual_bounds` first to project the reported monitor into framebuffer coordinates, then uses reported framebuffer gaps when one monitor size is known. If Windows collapses the whole desktop into one very-wide display, WebUI uses aspect-ratio priors to expose best-effort display rows without hardcoding pixel resolutions.
    - Agent VNC readiness serializes local service checks, gives pending services time to settle, force-kills stuck `STOP_PENDING` UltraVNC processes after grace time, and waits for the listener before reporting ready.
