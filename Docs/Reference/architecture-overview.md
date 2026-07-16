# Architecture Overview

Explain how Borealis is structured and how the core components interact end to end.

## Platform Shape
Borealis has two main runtime sides: the Engine server and the Agent clients. The Engine gives operators one web interface for inventory, remote operations, automation, reporting, credentials, and security controls. Agents run on managed endpoints and connect outbound to the Engine.

- **Engine (Server)**: Linux-hosted single-node control plane with Python backend services, PostgreSQL, Traefik, WebSockets, scheduling, automation, and web UI, split across isolated Docker containers.
- **Agent (Client)**: Cross-platform runtime written in Golang with Windows as the primary reference path, Linux support, role-based capabilities, signed task execution, device inventory, WireGuard tunneling, remote shell, file management, process management, and remote operation roles.
- **Transport**: Agents connect outbound to the Engine. Remote operations use WireGuard sessions with strict `/32` isolation and Engine-controlled port allowlists.
- **Data Layer**: PostgreSQL stores devices, inventory, jobs, activity history, alerts, assemblies, credentials metadata, and operational state.
- **Automation Model**: Assemblies can run through quick jobs, scheduled jobs, workflows, automated watchdog remediation, and Engine-side Ansible playbooks.
- **Security Model**: Aegis Cipher protects secrets, MFA is required by default, passkeys are supported, scripts are signed, and WireGuard tunnel access uses short-lived tokens.

!!! info "Operator mental model"
    Treat the Engine as the control plane and Agents as workers. Operators use the web UI; Agents phone home, report state, and execute approved work.

## Core Components
- Engine API backend: Flask + Socket.IO runtime that hosts APIs, scheduled jobs, VPN orchestration, VNC WebSocket proxy, and Engine-side Ansible execution.
- WebUI frontend: React single page app served by the WebUI container (Vite in dev, static build in prod).
- Traefik edge: public HTTP/HTTPS edge, ACME, and same-origin routing for UI, `/api`, `/socket.io`, and `/remote-desktop/vnc`.
- Agent: Go runtime binary (`Agent.exe`) that enrolls, reports heartbeat/status, executes scripts, and owns bootstrap/repair/update/runtime on installed hosts.
- PostgreSQL database: container-owned PostgreSQL state under `Engine/Services/postgres-db/state`; stores devices, approvals, schedules, activity history, tokens, configuration records, and assemblies.
- Assemblies: script definitions stored in PostgreSQL `assemblies.*` tables, with Aurora as the official authoring repo and a bundled seed snapshot kept in the Borealis repo.
- Remote access: WireGuard reverse VPN, remote PowerShell, and VNC via Apache Guacamole.

## How the Pieces Talk
- Enrollment: agent calls `/api/agent/enroll/request` and `/api/agent/enroll/poll`, operator approves, Engine issues tokens and cert bundle.
- Inventory: agent posts `/api/agent/heartbeat` and `/api/agent/details`, Engine updates device records.
- Quick jobs: operator calls `/api/scripts/quick_run`, Engine emits `quick_job_run` over the SYSTEM socket, and the SYSTEM broker either executes locally or forwards current-user work into a session helper before returning `quick_job_result`.
- Scheduled jobs: scheduler reads jobs from DB, resolves targets (including filters), then emits quick jobs.
- VPN tunnels: agent calls `/api/agent/vpn/ensure`, Engine emits `vpn_tunnel_start`, agent keeps WireGuard client online.
- Remote shell: UI uses Socket.IO `vpn_shell_*` events, Engine bridges to agent TCP shell over WireGuard.
- VNC: operator calls `/api/vnc/establish`, Engine creates or joins a collaboration session, waits for agent listener readiness, then proxies Apache Guacamole WebSocket traffic through local `guacd` to the agent VNC server.
- Notifications: operator or services call `/api/notifications/notify`, WebUI receives `borealis_notification` events.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    None on this page. See [API Reference](../Reference/Data%20and%20Schema/api-reference.md).

    ### Related documentation

    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Security Whitepaper](security-whitepaper.md)
    - [Device Auditing](../Using%20the%20Platform/device-auditing.md)
    - [Assemblies](../Using%20the%20Platform/Assemblies/assemblies.md)
    - [Scheduled Jobs](../Using%20the%20Platform/scheduled-jobs.md)
    - [Remote Shell](../Using%20the%20Platform/remote-shell.md)
    - [UI and Notifications](ui-and-notifications.md)
    - [Migrating Pages to React Router](../Reference/Migration%20Paths/migrating-pages-to-react-router.md)

    ### Source map

    - `Data/Engine/` - Engine package shim, unit tests, and container source roots.
    - `Data/Engine/Containers/api-backend/cmd/api-backend/` - Go Engine API/backend source (authoritative).
    - `Data/Engine/Containers/api-backend/data/` - retained Python worker/helper modules packaged by `site-worker`.
    - `Data/Engine/Containers/` - Engine container and Compose source (authoritative).
    - `Data/Agent/` - Agent source (authoritative).
    - `Engine/` - Engine generated runtime state (regenerated/deployed by `Engine.sh`).
    - `Engine/Deploy/` - Compose env, image manifest, deploy manifest, and build log.
    - `Engine/Services/<role>/` - container role config/env/logs/state/secrets/cache/run directories.
    - `Agent/` - Agent runtime copy (regenerated each launch).
    - `Data/Engine/Containers/webui-frontend/data/web-interface/src/` - WebUI source.
    - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/` - Flow Editor domain folder. Owns the workflow editor controller/compositor, canvas, sidebars, edge/node configuration panels, runtime wiring helpers, and the shared workflow node registry.
    - `Engine/Services/api-backend/logs/` and `Agent/Logs/` - runtime logs.
    - `Data/Engine/Containers/api-backend/data/Official_Assemblies/` - bundled official assembly seed snapshot.

    ### Service map by folder
    - Engine APIs: `Data/Engine/Containers/api-backend/cmd/api-backend/` (Go route handlers registered from `main.go`).
    - Site-worker realtime helpers: `Data/Engine/Containers/api-backend/data/services/WebSocket/` and `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker_socket.py`.
    - WebUI app shell and router: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/` (providers, route tree, guarded layouts, route adapters, runtime bootstrap).
    - Workflow authoring UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/` plus `Data/Engine/Containers/webui-frontend/data/web-interface/src/nodes/`.
      The React Router app layer routes into `Flow_Editor/Flow_Editor.jsx`, and the Flow Editor folder owns workflow load/save/run lifecycle, access checks, run snapshot hydration, shared node registration, and the React Flow canvas/sidebar surfaces.
    - VPN orchestration: `Data/Engine/Containers/api-backend/data/services/VPN/` (WireGuard server and tunnel lifecycle).
    - Remote desktop proxy: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/` (VNC WebSocket proxy).
    - Filters and targeting: `Data/Engine/Containers/api-backend/data/services/filters/matcher.py` (used by scheduled jobs and filter counts).
    - Agent runtime: `Data/Agent/cmd/agent` plus packages under `Data/Agent/internal/`.

    ### End-to-end flow examples (use these to debug)
    - Quick job:
      1) UI calls `/api/scripts/quick_run` with script path + hostnames.
      2) Engine signs the script, sets `target_context`, and emits `quick_job_run` to the host's SYSTEM socket.
      3) The SYSTEM broker either executes the SYSTEM role locally or routes current-user work into one or more session helpers over local IPC.
      4) The SYSTEM runtime posts `quick_job_result` over Socket.IO.
      5) Engine updates `activity_history` and emits `device_activity_changed`.
    - VPN shell:
      1) UI calls `/api/shell/establish` to ensure shell readiness.
      2) Agent WireGuard role keeps the tunnel online; agent shell role listens on TCP 47002.
      3) UI opens `vpn_shell_open` Socket.IO event; Engine bridges to TCP shell.
      4) UI sends/receives `vpn_shell_send` and `vpn_shell_output` events.

    ### Runtime boundaries
    - Do not edit `Engine/` or `Agent/` directly. They are recreated on each launch.
    - Edit Engine API source under `Data/Engine/Containers/api-backend/cmd/api-backend/`, retained Python worker/helper modules under `Data/Engine/Containers/api-backend/data/`, WebUI source under `Data/Engine/Containers/webui-frontend/data/web-interface/`, and Agent source under `Data/Agent/`; then re-run `Engine.sh` or `Data/Agent/build-agent.sh` as appropriate.

    ### What to read first when debugging
    - Start with logs: `Engine/Services/api-backend/logs/engine.log` and `Agent/Logs/Agent/agent.log`.
    - Check domain-specific logs (example: `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log`).
    - Inspect active DB state in PostgreSQL (`engine.*` and `assemblies.*`) for device/job metadata.

    ### Interaction points to remember
    - REST for inventory, enrollment, and admin actions.
    - Socket.IO for realtime job results, VPN shell, and notifications, with the supported Windows agent model exposing one SYSTEM socket per host.
    - Local IPC inside the agent for SYSTEM-to-helper current-user dispatch; helpers do not open their own Engine socket.
    - WireGuard for remote protocol transport (shell, VNC, future protocols).

    ### Container service map
    - `api-backend`: `127.0.0.1:5000`, Go Engine API, operator realtime, scheduler/workflow/watchdog API surfaces, VNC broker, WireGuard control integration.
    - `site-worker`: dynamic Python worker image for Ansible execution plus shell, file-transfer, VNC, and Agent Socket.IO helper runtimes.
    - `webui-frontend`: `127.0.0.1:8000` in production and dev.
    - `traefik-edge`: host `80/443`, same-origin routing, ACME, public edge logs.
    - `postgres-db`: `127.0.0.1:5432`, persistent database state.
    - `remote-desktop-guacd`: `127.0.0.1:4822`, VNC-only Guacamole daemon.
    - `wireguard-tunnel`: UDP `30000`, `borealis-wg`, WireGuard command control socket.
