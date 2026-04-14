# Architecture Overview
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Explain how Borealis is structured and how the core components interact end to end.

## Core Components
- Engine: Flask + Socket.IO runtime that hosts APIs, scheduled jobs, VPN orchestration, and WebUI assets.
- WebUI: React single page app served by the Engine (Vite in dev, static build in prod).
- Agent: Python runtime that enrolls, reports inventory, executes scripts, and opens VPN tunnels.
- PostgreSQL database: stores devices, approvals, schedules, activity history, tokens, configuration records, and assemblies.
- Assemblies: script definitions stored in PostgreSQL `assemblies.*` tables, with Aurora as the official authoring repo and a bundled seed snapshot kept in the Borealis repo.
- Remote access: WireGuard reverse VPN, remote PowerShell, and VNC via noVNC.

## How the Pieces Talk
- Enrollment: agent calls `/api/agent/enroll/request` and `/api/agent/enroll/poll`, operator approves, Engine issues tokens and cert bundle.
- Inventory: agent posts `/api/agent/heartbeat` and `/api/agent/details`, Engine updates device records.
- Quick jobs: operator calls `/api/scripts/quick_run`, Engine emits `quick_job_run` over Socket.IO, agent executes and returns `quick_job_result`.
- Scheduled jobs: scheduler reads jobs from DB, resolves targets (including filters), then emits quick jobs.
- VPN tunnels: agent calls `/api/agent/vpn/ensure`, Engine emits `vpn_tunnel_start`, agent keeps WireGuard client online.
- Remote shell: UI uses Socket.IO `vpn_shell_*` events, Engine bridges to agent TCP shell over WireGuard.
- VNC: operator calls `/api/vnc/establish`, Engine creates or joins a collaboration session, waits for agent listener readiness, then proxies noVNC WebSocket traffic to the agent VNC server.
- Notifications: operator or services call `/api/notifications/notify`, WebUI receives `borealis_notification` events.

## Directory Map (High Level)
- `Data/Engine/` - Engine source (authoritative).
- `Data/Agent/` - Agent source (authoritative).
- `Engine/` - Engine runtime copy (regenerated each launch).
- `Agent/` - Agent runtime copy (regenerated each launch).
- `Data/Engine/web-interface/src/` - WebUI source.
- `Data/Engine/web-interface/src/Flow_Editor/` - Flow Editor domain folder. Owns the workflow editor controller/compositor, canvas, sidebars, edge/node configuration panels, runtime wiring helpers, and the shared workflow node registry.
- `Engine/Logs/` and `Agent/Logs/` - runtime logs.
- `Data/Engine/Official_Assemblies/` - bundled official assembly seed snapshot.

## API Endpoints
None on this page. See [API Reference](api-reference.md).

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)
- [Security and Trust](security-and-trust.md)
- [Device Management](device-management.md)
- [Assemblies and Quick Jobs](assemblies.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [UI and Notifications](ui-and-notifications.md)
- [Migrating Pages to React Router](migrating-pages-to-react-router.md)

## Codex Agent (Detailed)
### Service map by folder
- Engine APIs: `Data/Engine/services/API/` (grouped by domain, registered in `Data/Engine/services/API/__init__.py`).
- Engine realtime: `Data/Engine/services/WebSocket/` (Socket.IO events: quick jobs, VPN shell, agent socket registry).
- WebUI hosting: `Data/Engine/services/WebUI/` (SPA static assets and 404 fallback).
- WebUI app shell and router: `Data/Engine/web-interface/src/app/` (providers, route tree, guarded layouts, route adapters, runtime bootstrap).
- Workflow authoring UI: `Data/Engine/web-interface/src/Flow_Editor/` plus `Data/Engine/web-interface/src/nodes/`.
  The React Router app layer routes into `Flow_Editor/Flow_Editor.jsx`, and the Flow Editor folder owns workflow load/save/run lifecycle, access checks, run snapshot hydration, shared node registration, and the React Flow canvas/sidebar surfaces.
- VPN orchestration: `Data/Engine/services/VPN/` (WireGuard server and tunnel lifecycle).
- Remote desktop proxy: `Data/Engine/services/RemoteDesktop/` (VNC WebSocket proxy).
- Filters and targeting: `Data/Engine/services/filters/matcher.py` (used by scheduled jobs and filter counts).
- Agent roles: `Data/Agent/Roles/` (script exec, screenshot, WireGuard tunnel, remote PowerShell, etc).

### End-to-end flow examples (use these to debug)
- Quick job:
  1) UI calls `/api/scripts/quick_run` with script path + hostnames.
  2) Engine signs script and emits `quick_job_run`.
  3) Agent role executes and posts `quick_job_result` over Socket.IO.
  4) Engine updates `activity_history` and emits `device_activity_changed`.
- VPN shell:
  1) UI calls `/api/shell/establish` to ensure shell readiness.
  2) Agent WireGuard role keeps the tunnel online; agent shell role listens on TCP 47002.
  3) UI opens `vpn_shell_open` Socket.IO event; Engine bridges to TCP shell.
  4) UI sends/receives `vpn_shell_send` and `vpn_shell_output` events.

### Runtime boundaries
- Do not edit `Engine/` or `Agent/` directly. They are recreated on each launch.
- Always edit `Data/Engine/` and `Data/Agent/` then re-run the bootstrap script.

### What to read first when debugging
- Start with logs: `Engine/Logs/engine.log` and `Agent/Logs/agent.log`.
- Check domain-specific logs (example: `Engine/Logs/VPN_Tunnel/tunnel.log`).
- Inspect active DB state in PostgreSQL (`engine.*` and `assemblies.*`) for device/job metadata.

### Interaction points to remember
- REST for inventory, enrollment, and admin actions.
- Socket.IO for realtime job results, VPN shell, and notifications.
- WireGuard for remote protocol transport (shell, VNC, future protocols).
