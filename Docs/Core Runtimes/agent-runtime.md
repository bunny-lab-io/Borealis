# Agent Runtime
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Describe the Borealis agent runtime, its roles, service modes, and how it communicates with the Engine.

## Runtime Summary
- Main entry: `Data/Agent/agent.py` (Python agent service).
- Service modes: SYSTEM plus a local helper mode launched into user sessions by the SYSTEM broker.
- Role system: `Data/Agent/role_manager.py` auto-loads `Data/Agent/Roles/role_*.py`.
- Session broker: `Data/Agent/session_runtime.py` launches and tracks per-session helpers, local IPC, and helper readiness.
- Desktop helper UI: `Data/Agent/Roles/role_currentuser_context.py` owns the current-user tray popup, using `Data/Agent/qt_compat.py` to prefer PySide6 while keeping the existing asyncio/Qt integration path.
- Networking: the SYSTEM runtime owns REST to Engine APIs plus the single Socket.IO connection; helpers use local IPC only.
- Security: Ed25519 identity keys, public CA + hostname validation for the Engine FQDN, signed script payloads, encrypted token storage.

## Role Catalog (Current)
- `role_system_context.py` (ROLE_NAME: `context_system`) - canonical SYSTEM task router for `quick_job_run`, `service_control_action`, and `agent_update_request`, with per-device task lanes for `software_management`, `scheduled_job_system`, `service_management`, and `agent_update`.
- `role_currentuser_context.py` (ROLE_NAME: `context_currentuser`) - interactive device-local script execution plus the current-user helper tray/status UI.
- `role_system_device_auditor.py` (ROLE_NAME: `device_auditor`) - summary, memory, storage, network, session, and process inventory.
- `role_system_file_management.py` (ROLE_NAME: `file_management`) - remote filesystem browse, lightweight text read/write, copy, cut, paste, create-folder, rename, move, delete, upload, folder-upload manifest replay, and download orchestration over the SYSTEM socket plus device-authenticated transfer endpoints.
- `role_system_service_management.py` (ROLE_NAME: `service_management`) - system service inventory and start/stop/restart control.
- `role_system_process_management.py` (ROLE_NAME: `process_management`) - live process snapshots, per-process CPU/memory/command-line metadata, parent-child process relationships, and operator-triggered process termination.
- `role_system_software_management.py` (ROLE_NAME: `software_management`) - installed software inventory, Windows icon payload publication, and software refresh boosts after software-management work.
- `role_system_heartbeat.py` (ROLE_NAME: `system_heartbeat`) - earliest SYSTEM startup timeline reporter. It is imported before normal role loading, buffers milestones until device auth is available, then publishes `system:system_heartbeat` status to the Engine.
- `role_system_remote_shell.py` (ROLE_NAME: `remote_shell`) - remote shell server over WireGuard (PowerShell on Windows, Bash on Linux).
- `role_system_vnc.py` (ROLE_NAME: `vnc`) - always-on UltraVNC server lifecycle.
- `role_system_wireguard.py` (ROLE_NAME: `wireguard`) - WireGuard client lifecycle.
- `role_currentuser_macros.py` and `role_currentuser_node_screenshot.py` remain legacy interactive-only roles and are not part of the supported helper-backed Windows runtime path.

## Agent Settings and Storage
- Settings root: `Agent/Borealis/Settings/` (runtime).
- Server URL: `Agent/Borealis/Settings/server_url.txt`.
- GUID and token storage: `Agent/Borealis/Settings/Agent_GUID.txt`, `access.jwt`, `refresh.token`.

## API Endpoints (Engine-facing)
- `POST /api/agent/enroll/request` (No Authentication) - start enrollment.
- `POST /api/agent/enroll/poll` (No Authentication) - finalize enrollment after approval.
- `POST /api/agent/token/refresh` (Refresh Token) - mint a new access token.
- `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat + metrics.
- `POST /api/agent/status` (Device Authenticated) - startup phase, boot ID, milestone timeline, and last-error telemetry for `system:system_heartbeat`.
- `POST /api/agent/details` (Device Authenticated) - hardware, inventory, and cached service payloads.
- `POST /api/agent/script/request` (Device Authenticated) - request work or receive idle signal.
- `POST /api/agent/vpn/ensure` (Device Authenticated) - persistent WireGuard tunnel bootstrap.
- `POST /api/agent/vpn/ready` (Device Authenticated) - active WireGuard tunnel readiness after service/config/firewall apply.
- `POST /api/agent/vnc/ensure` (Device Authenticated) - advertise the current boot-scoped VNC credential and reconcile always-on VNC readiness.
- `GET /api/agent/files/transfers/<transfer_id>/upload-item/<item_id>` (Device Authenticated) - fetch one Engine-staged upload item for the File Management role.
- `GET /api/agent/files/transfers/<transfer_id>/status` (Device Authenticated) - fetch one File Management transfer control snapshot so the agent can honor cancel requests mid-transfer.
- `POST /api/agent/files/transfers/<transfer_id>/progress` (Device Authenticated) - update Engine-side File Management transfer progress.
- `POST /api/agent/files/transfers/<transfer_id>/content` (Device Authenticated) - upload a completed File Management download artifact back to the Engine.

## Related Documentation
- [Security and Trust](../Start%20Here/security-and-trust.md)
- [Device Management](../Operations%20and%20Remote%20Access/device-management.md)
- [VPN and Remote Access](../Operations%20and%20Remote%20Access/vpn-and-remote-access.md)

## Codex Agent (Detailed)
### Source vs runtime
- Edit only in `Data/Agent/`.
- Linux runtime copy lives in `Agent/` and is regenerated by `Agent.sh deploy`.
- Windows runtime copy is regenerated by `Agent.ps1`.
- `Update.sh -Agent` preserves the current agent updater flow, then hands off to `Agent.sh deploy`.

### Service modes and context
- SYSTEM mode is used for elevated tasks (scheduled tasks, VPN, system scripts).
- The SYSTEM runtime is the only Borealis process that authenticates to the Engine, enrolls, refreshes tokens, or opens a Socket.IO connection.
- Interactive user work now runs in helper mode, launched by the SYSTEM broker into active or locked user sessions and reached over local IPC.
- Direct Session 0 UI is not supported; Borealis keeps the Engine-facing socket in SYSTEM and bridges into desktop sessions when current-user interaction is required.
- The helper tray UI now opens a dedicated bottom-right status popup from tray icon clicks instead of using a right-click context menu, and that popup is the supported user-facing control surface for helper restart/status actions.
- The agent still labels Engine traffic with `X-Borealis-Agent-Context`, but the supported Windows service path no longer relies on a standalone CURRENTUSER Engine identity.
- Headless Linux agents without an active graphical desktop report desktop-only health surfaces as `No Desktop Environment Active` instead of unhealthy/recovering. This applies to Current User Context helper dispatch and UI-side UltraVNC presentation so server-class Linux hosts do not look broken for missing desktop roles.

### Role discovery and extension
- Roles are discovered dynamically from `Data/Agent/Roles/`.
- Each role must define:
  - `ROLE_NAME` (string)
  - `ROLE_CONTEXTS` (list: `['system']`, `['interactive']`, `['helper']`, or combinations as needed)
  - `Role` class with optional `register_events`, `on_config`, and `stop_all`.
- To add a role:
  1) Create `Data/Agent/Roles/role_<context>_<purpose>.py`.
  2) Export `ROLE_NAME`, `ROLE_CONTEXTS`, and `Role`.
  3) Re-stage the agent runtime (`Agent.ps1`).

### Networking and authentication
- All REST calls flow through `AgentHttpClient` in `Data/Agent/agent.py`.
- `AgentHttpClient.ensure_authenticated()` handles enrollment and refresh.
- Socket.IO is used by the SYSTEM runtime for:
  - `quick_job_run` dispatch (system jobs plus helper-backed current-user jobs).
  - `file_management_request` browse, upload-conflict preflight, lightweight text-edit, copy/cut/paste mutate, and transfer orchestration for the Device Summary `File Management` tab.
  - `process_management_request` live process snapshots and process termination for the Device Summary `Processes` tab.
  - `vpn_tunnel_start` (WireGuard lifecycle; tunnels are persistent and ignore stop events).
  - `connect_agent` registration (agent socket registry).
- The SYSTEM socket advertises `helper_contexts=["currentuser"]` when the session broker is running so the Engine can route logical current-user work through the same socket.
- Helper processes never enroll, never refresh tokens, never open Socket.IO, and never talk to the Engine directly.
- SYSTEM-to-helper communication uses per-session local IPC keyed by Windows `session_id`; the helper trusts `broker_verified` payloads from the local broker and still validates signed payloads if used outside that path.
- WireGuard tunnels are ensured via `POST /api/agent/vpn/ensure` on boot and refreshed periodically.
- The ensure loop re-establishes the tunnel automatically after network hiccups.
- `role_system_heartbeat.py` starts before `AgentHttpClient.ensure_authenticated()`, normal `RoleManager.load()`, and the Socket.IO connect loop. It records process start, server config, identity, auth, status channel, socket, role load, helper broker, WireGuard, inventory, steady-state, and failure milestones.
- Heartbeats/details also carry per-role health snapshots so the Device Details `Agent Health` tab can show current role/service status with last-checked timestamps. Startup status uses `POST /api/agent/status` so the timeline can update before the full role-health heartbeat cycle. The SYSTEM runtime also refreshes startup/status telemetry every five minutes after initial boot so graceful restarts or missed startup flushes do not leave Agent Health stuck on empty startup telemetry.
- The VNC role generates one shared UltraVNC password when the role starts, rotates it again every 24 hours by default (`BOREALIS_VNC_CREDENTIAL_ROTATION_SECONDS`), keeps it in memory only, re-advertises it to the Engine through `POST /api/agent/vnc/ensure`, keeps UltraVNC continuously running once it has the Engine /32 firewall scope, and reports `ready`, `service_state`, `listener_state`, and `last_ready_at` through agent role health even when no operator is currently connected.

### Token storage
- Refresh tokens are stored encrypted (DPAPI on Windows) in `refresh.token`.
- Access tokens are stored in `access.jwt` with expiry metadata.
- GUID is stored in `Agent_GUID.txt`.
- When tokens are invalid or expired, the agent re-enrolls.

### Logging
- Primary log: `Agent/Logs/agent.log` with daily rotation.
- Error log: `Agent/Logs/agent.error.log`.
- VPN logs: `Agent/Logs/VPN_Tunnel/tunnel.log` and `remote_shell.log`.
- Role-specific logs may write to `Agent/Logs/<service>.log`.
- Updater diagnostics: `<ProjectRoot>/Updater.log` is recreated by both `Update.ps1` and `Update.sh` at the start of each run so it contains only the latest update session.

### Troubleshooting flow
- If enrollment fails, check:
  - `Agent/Logs/agent.log` for enrollment errors.
  - `Engine/Services/api-backend/logs/engine.log` for approval or auth failures.
- If current-user execution fails, confirm the SYSTEM broker is advertising helper capability, inspect session inventory for `helper_ready`, and expect `no_interactive_user_session` when no eligible user session exists.
- If a helper never appears after logon/unlock, inspect the broker reconcile loop in `Data/Agent/session_runtime.py` and confirm the legacy `Borealis Agent (UserHelper)` task has been removed rather than still competing with the broker-launched helper.
- The updater helper (`Data/Agent/update_helper.py`) requires a configured public HTTPS FQDN and uses normal CA + hostname validation only.
- Operator-requested manual updates arrive over the SYSTEM Socket.IO channel as `agent_update_request` and start the local AutoUpdater task/service immediately so the same scheduler-owned update path is used for both manual and hourly runs.
- Engine-managed release channels still decide what build the agent should adopt, but the agent now discovers and applies those targets during its scheduled updater cadence instead of reacting to live Engine push events.
- The scheduled AutoUpdater cadence is hourly with up to 15 minutes of random delay on normal timer-driven runs so fleets do not all recheck at once.
- Linux updater service runs `Update.sh -Agent` through `borealis-agent-updater.service`; `Agent.sh deploy` detects that service context and does not stop the active updater while refreshing the agent runtime.
- If scripts do not run:
  - Confirm `quick_job_run` events and the correct role context.
  - Verify signatures with `signature_utils` logs.
- If VPN fails:
  - Check agent WireGuard role logs and confirm `/api/agent/vpn/ensure` succeeds.
  - Ensure the Engine has an active tunnel session and the WireGuard service is running.
- If VNC fails:
  - Check `Agent/Logs/VPN_Tunnel/tunnel.log` for `vnc_start` / `vnc_stop` reconciliation events.
  - Call `POST /api/agent/vnc/ensure` and inspect `ready`, `service_state`, `listener_state`, `detail`, and `last_ready_at`.
  - Confirm the active collaboration session still exists from the Engine side with `GET /api/vnc/sessions`.

### Borealis Agent Codex (Full)
Use this section for agent-only work (Borealis agent runtime under `Data/Agent` -> `/Agent`). Shared guidance is consolidated in `ui-and-notifications.md` and the Engine runtime notes.

#### Scope and runtime paths
- Purpose: outbound-only connectivity, device telemetry, scripting, UI helpers.
- Bootstrap: `Agent.ps1` preps dependencies, activates the agent venv, keeps only the SYSTEM startup task, and removes the legacy `Borealis Agent (UserHelper)` scheduled task on install/update. Remote Windows onboarding stages `Agent_Service_Bootstrapper.exe` from `Data/Agent/Bootstrapper/`; that native service shim handles SCM/service semantics, temp cleanup, existing-agent repair checks, hostname reporting, `Agent.ps1` download, state/events, timeout, and process-tree termination before launching `Agent.ps1`. Existing Windows agents are treated as repairable only when the `Borealis Agent` scheduled task exists/runs and the Engine accepts the local access token; missing tasks or rejected tokens trigger re-deployment.
- Windows install hardens `Agent/Borealis/Settings/Tray` permissions during `Configure Agent Settings`; stale `*.tmp` tray status files are cleaned first, and remaining volatile temp-file ACL denials are logged without failing onboarding.
- Linux first install: `Agent.sh deploy` installs dependencies and stages the runtime.
- Raw one-line or repo-option `Agent.sh` runs sync first, then re-execs the installed `Agent.sh`; root shells do not need `sudo` in the pipe, and generated non-root commands still use `sudo bash` before script execution. Local non-root `Agent.sh deploy` self-escalates through `sudo` when available and uses existing on-disk source without updating git.
- Edit in `Data/Agent`, not `/Agent`; runtime copies are ephemeral and wiped regularly.
- Linux Agent installation is blocked when the install root contains deployed Engine runtime markers such as `Engine/Deploy/` or `Engine/Services/api-backend/`. The synced repository source under `Data/Engine/` is expected during Agent bootstrap and must not block first install.

#### Logging
- Primary log: `Agent/Logs/agent.log` with daily rotation to `agent.log.YYYY-MM-DD` (never auto-delete rotated files).
- Subsystems: log to `Agent/Logs/<service>.log` with the same rotation policy.
- Install/diagnostics: `Agent/Logs/install.log`; keep ad-hoc traces (for example, `system_last.ps1`) under `Agent/Logs/` to keep runtime state self-contained.
- Updater trace exception: `Update.ps1` and `Update.sh` recreate centralized diagnostics in `<ProjectRoot>/Updater.log` at the start of each run so operators can inspect the latest cross-platform update session from one file.
- Troubleshooting: prefix lines with `<timestamp>-<service-name>-<log-data>`; ask operators whether verbose logging should stay after resolution.

#### Security
- Generates device-wide Ed25519 keys on first launch (`Agent/Borealis/Certificates/Identity/`; DPAPI on Windows, `chmod 600` elsewhere).
- Refresh/access tokens are encrypted and bound to the device identity plus Engine-issued token state; mismatches force re-enrollment.
- REST and Socket.IO traffic use the public Engine FQDN with normal CA + hostname validation.
- Validates script payloads with backend-issued Ed25519 signatures before execution.
- Outbound-only; API/WebSocket calls flow through `AgentHttpClient.ensure_authenticated` for proactive refresh. Logs bootstrap, enrollment, token refresh, and signature events in `Agent/Logs/`.
- Helper processes inherit no Borealis token state and rely on the local SYSTEM broker for job delivery.

#### Reverse VPN tunnels
- WireGuard reverse VPN design and lifecycle are documented in `vpn-and-remote-access.md`.
- The original references were `REVERSE_TUNNELS.md` and `Reverse_VPN_Tunnel_Deployment.md` (now consolidated into this knowledgebase).
- Agent roles:
  - `Data/Agent/Roles/role_system_wireguard.py` (tunnel lifecycle)
  - `Data/Agent/Roles/role_system_remote_shell.py` (VPN remote shell TCP server)

#### Execution contexts and roles
- Auto-discovers roles from `Data/Agent/Roles/`; no loader changes needed.
- Naming: `role_<context>_<purpose>.py` with `ROLE_NAME`, `ROLE_CONTEXTS`, and optional hooks (`register_events`, `on_config`, `stop_all`).
- Standard supported one-socket roles: `role_system_context.py`, `role_currentuser_context.py`, `role_system_device_auditor.py`, `role_system_service_management.py`, `role_system_process_management.py`, `role_system_software_management.py`, `role_system_remote_shell.py`, `role_system_vnc.py`, `role_system_wireguard.py`.
- The remote filesystem surface now also includes `role_system_file_management.py`, which serializes transfer-heavy and inline text-edit work through the `file_management` lane, replays folder-upload manifests one file at a time into the requested destination tree, honors operator cancel requests during upload/download/archive work, and keeps browse/mutate requests on the SYSTEM socket.
- `role_currentuser_macros.py` and `role_currentuser_node_screenshot.py` remain legacy interactive-only implementations and are not part of the supported helper-backed Windows runtime path.
- SYSTEM tasks depend on scheduled-task creation rights; failures should surface through Engine logging.

#### Platform parity
- Windows is the reference path and has the broadest tested feature surface.
- Linux agents run from the script-staged Python runtime through `Agent.sh deploy`, not shipped binaries.
- Linux agents load the standard Agent roles and currently support WireGuard VPN, remote Bash/script execution, file/folder interaction, and Engine-side Ansible reachability to remote Linux devices.
- Linux does not have a system tray/helper UI yet, and remote desktop remains Windows-only through the UltraVNC/Apache Guacamole path.
- Linux agents probe for an active desktop environment through environment variables, display-manager state, display sockets, and common desktop processes. If none is active, desktop-only roles return `not_applicable`/`No Desktop Environment Active`.
- Linux service control, process management, and software management code paths exist but need validation before they should be described as parity features.

#### Ansible support
- The agent no longer hosts an Ansible playbook execution role.
- Borealis Ansible control-node execution is Engine-side and should target devices over the Engine-managed WireGuard paths.
- Agent responsibilities for the Ansible architecture are limited to:
  - maintaining device identity and inventory in the Engine
  - sustaining the reverse WireGuard tunnel and related remote-access services
  - exposing the device to Engine-driven automation over the VPN path
