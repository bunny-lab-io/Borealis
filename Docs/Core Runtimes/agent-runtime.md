# Agent Runtime
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Describe the Borealis agent runtime, its roles, service modes, and how it communicates with the Engine.

## Runtime Summary
- Main entry: `Data/Agent/cmd/agent` builds one Go runtime binary named `Agent.exe`.
- Legacy Python source lives under `Data/Agent_Old` for reference during migration and is not installed as runtime fallback.
- Service modes: SYSTEM/root plus helper mode through the same binary. Windows CURRENTUSER quick jobs use direct session launch in the current Go migration branch; Linux CURRENTUSER reports unsupported until ported.
- Role system: compiled Go role registry under `Data/Agent/internal/roles`.
- Networking: SYSTEM/root runtime owns REST to Engine APIs plus the single Socket.IO connection.
- Security: Ed25519 identity keys, public CA + hostname validation for the Engine FQDN, signed script payloads, and `config.json` token/key storage.

## Role Catalog (Go v1)
- `internal/roles/system_context` - SYSTEM/root quick-job router and script execution for signed `quick_job_run` payloads.
- `internal/roles/current_user` - Windows CURRENTUSER direct session quick-job execution for active user sessions. Linux CURRENTUSER reports unsupported in first PR.
- `internal/roles/device_audit` - core CPU, memory, storage media type, removable media, network link speed, OS/build, hardware model/serial with motherboard serial fallback, last reboot, internal IP, device type, uptime, and last-user inventory published through heartbeat payloads.
- `internal/roles/file_management` - SYSTEM/root file-management browse, upload-conflict preflight, lightweight text editing, copy/cut/paste mutations, delete, mkdir, rename, move, upload pull, and download artifact transfer.
- `internal/roles/process_management` - SYSTEM/root live process snapshots, parent/child metadata, cache reuse, and operator-triggered process termination for the Device Summary `Processes` tab.
- `internal/roles/service_management` - SYSTEM/root service inventory publishing plus operator-triggered start, stop, and restart through `service_control_action`.
- `internal/roles/software_management` - SYSTEM/root Windows installed-app inventory, Linux dpkg/rpm inventory, refresh requests, and post-uninstall inventory refresh through the SYSTEM quick-job lane.
- Pending ports are tracked in `Data/Agent/Golang_Agent_Migration.md`: WireGuard, remote shell, VNC, tray UI, macros, and node screenshot.

## Agent Settings and Storage
- Installed configuration file: `config.json` beside `Agent.exe`.
- `config.json` stores `schema_version`, `server_url`, `enrollment_code`, `agent.guid`, `agent.agent_id`, Ed25519 keys, access/refresh tokens, and Engine script-signing trust material.
- Windows protection: ACL hardening is deferred in the current Go migration branch; files inherit permissions from `C:\Borealis`.
- Linux protection: root-owned `0600` file with `0700` parent directory.
- Writes are atomic temp-write + rename. No OS file-lock dependency is used.

## API Endpoints (Engine-facing)
- `POST /api/agent/enroll/request` (No Authentication) - start enrollment.
- `POST /api/agent/enroll/poll` (No Authentication) - finalize enrollment after approval.
- `POST /api/agent/token/refresh` (Refresh Token) - mint a new access token.
- `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat + metrics.
- `POST /api/agent/status` (Device Authenticated) - startup phase, boot ID, milestone timeline, and last-error telemetry for `startup:system_heartbeat`.
- `POST /api/agent/details` (Device Authenticated) - hardware, inventory, and cached service payloads.
- `POST /api/agent/script/request` (Device Authenticated) - request work or receive idle signal.
- `POST /api/agent/vpn/ensure` (Device Authenticated) - persistent WireGuard tunnel bootstrap.
- `POST /api/agent/vpn/ready` (Device Authenticated) - active WireGuard tunnel readiness after service/config/firewall apply.
- `POST /api/agent/vnc/ensure` (Device Authenticated) - advertise VNC readiness and reconcile always-on VNC state without returning the VNC password.
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
- Legacy Python source is archived in `Data/Agent_Old/`.
- Windows installed runtime is `C:\Borealis\Agent.exe` plus `C:\Borealis\config.json`.
- Linux installed runtime is a single compiled `Agent` binary managed by systemd; Linux bootstrap parity is still tracked in the migration ledger.
- `Update.sh -Agent` remains legacy while Go release-channel packaging is completed.

### Service modes and context
- SYSTEM mode is used for elevated tasks (scheduled tasks, VPN, system scripts).
- The SYSTEM runtime is the only Borealis process that authenticates to the Engine, enrolls, refreshes tokens, or opens a Socket.IO connection.
- Interactive user quick jobs now run through the SYSTEM broker by launching signed PowerShell/Batch payloads into active Windows user sessions. Full long-lived helper/tray IPC remains pending Go parity work.
- Direct Session 0 UI is not supported; Borealis keeps the Engine-facing socket in SYSTEM and bridges into desktop sessions when current-user interaction is required.
- The helper tray UI now opens a dedicated bottom-right status popup from tray icon clicks instead of using a right-click context menu, and that popup is the supported user-facing control surface for helper restart/status actions.
- The agent still labels Engine traffic with `X-Borealis-Agent-Context`, but the supported Windows service path no longer relies on a standalone CURRENTUSER Engine identity.
- Headless Linux agents without an active graphical desktop report desktop-only health surfaces as `No Desktop Environment Active` instead of unhealthy/recovering. This applies to Current User Context helper dispatch and UI-side UltraVNC presentation so server-class Linux hosts do not look broken for missing desktop roles.

### Role discovery and extension
- Go roles are compiled under `Data/Agent/internal/roles`.
- Add new role packages to the explicit registry in `cmd/agent`/runtime wiring instead of relying on dynamic Python module discovery.
- Legacy role behavior can be referenced in `Data/Agent_Old/Roles` while porting.

### Networking and authentication
- All REST calls flow through the Go auth client in `Data/Agent/internal/auth`.
- `EnsureAuthenticated` handles identity generation, enrollment, approval polling, and token refresh.
- Socket.IO is used by the SYSTEM runtime for:
  - `quick_job_run` dispatch (system jobs plus helper-backed current-user jobs).
  - `file_management_request` browse, upload-conflict preflight, lightweight text-edit, copy/cut/paste mutate, and transfer orchestration for the Device Summary `File Management` tab.
  - `process_management_request` live process snapshots and process termination for the Device Summary `Processes` tab.
  - `service_control_action` start, stop, and restart requests for services discovered by the Service Management role.
  - `software_inventory_refresh_request` operator-triggered software inventory refresh after icon/override or software action changes.
  - `vpn_tunnel_start` (WireGuard lifecycle; tunnels are persistent and ignore stop events).
  - `connect_agent` registration (agent socket registry).
- The SYSTEM socket advertises `helper_contexts=["currentuser"]` when the session broker is running so the Engine can route logical current-user work through the same socket.
- Helper processes never enroll, never refresh tokens, never open Socket.IO, and never talk to the Engine directly.
- Current Go Windows CURRENTUSER support uses direct `CreateProcessAsUser` session launch from SYSTEM for signed quick jobs. Real-host PowerShell Desktop canary validation passed, including denial when the user context attempted to write to root `C:\`. Full per-session local IPC helpers keyed by Windows `session_id` remain pending.
- WireGuard tunnels are ensured via `POST /api/agent/vpn/ensure` on boot and refreshed periodically.
- The ensure loop re-establishes the tunnel automatically after network hiccups.
- Go startup posts full timeline milestones before entering the Socket.IO connect loop and keeps heartbeat/status telemetry on the SYSTEM/root runtime.
- Heartbeats/details also carry per-role health snapshots so the Device Details `Agent Health` tab can show current role/service status with last-checked timestamps. Startup status uses `POST /api/agent/status` under the separate `startup` context so later SYSTEM role-health heartbeats do not erase the timeline row.
- The VNC role generates one shared UltraVNC password when the role starts, rotates it again every 24 hours by default (`BOREALIS_VNC_CREDENTIAL_ROTATION_SECONDS`), keeps it in memory only, and returns it to the Engine only through live Agent Socket.IO `vnc_credential_request` calls. The Agent does not probe UltraVNC auth locally by default because each loopback auth probe consumes an UltraVNC login attempt and can trip lockout before Guacamole connects. Set `BOREALIS_VNC_LOCAL_AUTH_VERIFY=1` only for focused diagnostics. The role keeps UltraVNC continuously running once it has the Engine /32 firewall scope, writes UltraVNC config under `%ProgramData%\UltraVNC\` with loopback allowed for local diagnostics, and reports `ready`, `service_state`, `listener_state`, and `last_ready_at` through agent role health even when no operator is currently connected.
- VNC role trace logs (`vnc_trace ...`) are disabled by default because the always-on health loop can otherwise produce high-volume logs during normal operation. Set `BOREALIS_VNC_TRACE=1` only for short diagnostic captures.
- The UltraVNC config writer enables capture performance flags (`TurboMode`, full-screen polling defaults, `EnableDriver`, and `EnableHook`) when the official UltraVNC helper DLLs are present beside `winvnc.exe`.

### Token storage
- Refresh/access tokens are stored in `config.json`.
- Device GUID and Engine agent ID are stored in `config.json`.
- When tokens are invalid or expired, the agent refreshes or re-enters enrollment.

### Logging
- Primary log: `Logs/agent.log` with daily rotation.
- Error log: `Logs/agent.error.log`.
- VPN logs: `Logs/VPN_Tunnel/tunnel.log` and `remote_shell.log`.
- Role-specific logs may write to `Logs/<service>.log`.
- Windows bootstrap/update diagnostics: `<AgentInstallRoot>/Logs/bootstrap.log`. Linux updater diagnostics: `<AgentInstallRoot>/Logs/bootstrap.log`.

### Troubleshooting flow
- If enrollment fails, check:
  - `Logs/agent.log` for enrollment errors.
  - `Engine/Services/api-backend/logs/engine.log` for approval or auth failures.
- If current-user execution fails, confirm the SYSTEM broker is advertising helper capability, inspect session inventory for `helper_ready`, and expect `no_interactive_user_session` when no eligible user session exists.
- If CURRENTUSER execution fails, inspect the Go helper broker migration status in `Data/Agent/Golang_Agent_Migration.md`.
- Operator-requested manual updates arrive over the SYSTEM Socket.IO channel as `agent_update_request` and start the local AutoUpdater task/service immediately so the same scheduler-owned update path is used for both manual and hourly runs.
- Engine-managed release channels still decide what build the agent should adopt, but the agent now discovers and applies those targets during its scheduled updater cadence instead of reacting to live Engine push events.
- The scheduled AutoUpdater cadence is hourly on Windows. Linux update cadence is pending Go release-channel packaging.
- If scripts do not run:
  - Confirm `quick_job_run` events and the correct role context.
  - Verify signatures with `signature_utils` logs.
- If VPN fails:
  - Check agent WireGuard role logs and confirm `/api/agent/vpn/ensure` succeeds.
  - Ensure the Engine has an active tunnel session and the WireGuard service is running.
- If VNC fails:
  - Check `Logs/VPN_Tunnel/tunnel.log` for `vnc_start` / `vnc_stop` reconciliation events.
  - Call `POST /api/agent/vnc/ensure` and inspect `ready`, `service_state`, `listener_state`, `detail`, and `last_ready_at`.
  - Confirm the active collaboration session still exists from the Engine side with `GET /api/vnc/sessions`.

### Borealis Agent Codex (Full)
Use this section for agent-only work (Borealis agent runtime under `Data/Agent` -> `/Agent`). Shared guidance is consolidated in `ui-and-notifications.md` and the Engine runtime notes.

#### Scope and runtime paths
- Purpose: outbound-only connectivity, device telemetry, scripting, UI helpers.
- Bootstrap: `Agent.exe` owns deploy, repair, update check, config write, scheduled-task registration, and runtime. Windows onboarding stages the Go binary from `Data/Agent/dist/windows-amd64/Agent.exe`; the installed copy runs from `C:\Borealis\Agent.exe`.
- Windows support dependencies: `Agent.exe` can still install UltraVNC and WireGuard from official installers for later role ports. It does not stage Python, create a venv, or call `launch_service.ps1`.
- Existing Windows agents are repairable when `C:\Borealis\Agent.exe`, the `Borealis Agent` scheduled task, and an Engine-accepted token in `config.json` are present.
- Linux first install: copy `Data/Agent/dist/linux-amd64/Agent` to `/opt/Borealis/Agent/Agent`, then run `/opt/Borealis/Agent/Agent --server-url <url> --site-enrollment-code <code> --install-service` as root.
- Edit in `Data/Agent`, not `/Agent`; runtime copies are ephemeral and wiped regularly.
- Keep Linux Agent installation separate from deployed Engine runtime roots.

#### Logging
- Primary log: `Logs/agent.log` with daily rotation to `agent.log.YYYY-MM-DD` (never auto-delete rotated files).
- Subsystems: log to `Logs/<service>.log` with the same rotation policy.
- Install/diagnostics: `Logs/install.log`; keep ad-hoc traces (for example, `system_last.ps1`) under `Logs/` to keep runtime state self-contained.
- Updater trace exception: `Agent.exe` writes bootstrap/update diagnostics to `<AgentInstallRoot>/Logs/bootstrap.log`.
- Troubleshooting: prefix lines with `<timestamp>-<service-name>-<log-data>`; ask operators whether verbose logging should stay after resolution.

#### Security
- Generates device-wide Ed25519 keys on first launch and stores PKCS8/SPKI base64 in `config.json`.
- Refresh/access tokens are stored in `config.json` and bound to the device identity plus Engine-issued token state; mismatches force re-enrollment.
- REST and Socket.IO traffic use the public Engine FQDN with normal CA + hostname validation.
- Validates script payloads with backend-issued Ed25519 signatures before execution.
- Outbound-only; API/WebSocket calls flow through the Go auth client for proactive refresh. Logs bootstrap, enrollment, token refresh, and signature events in `Logs/`.
- Helper processes inherit no Borealis token state and rely on the local SYSTEM broker for job delivery.

#### Reverse VPN tunnels
- WireGuard reverse VPN design and lifecycle are documented in `vpn-and-remote-access.md`.
- The original references were `REVERSE_TUNNELS.md` and `Reverse_VPN_Tunnel_Deployment.md` (now consolidated into this knowledgebase).
- Agent roles:
  - `Data/Agent/Roles/role_system_wireguard.py` (tunnel lifecycle)
  - `Data/Agent/Roles/role_system_remote_shell.py` (VPN remote shell TCP server)

#### Execution contexts and roles
- Go roles are explicit packages under `Data/Agent/internal/roles`.
- First PR supports SYSTEM/root quick-job script execution, Windows CURRENTUSER direct session PowerShell/Batch execution, core device audit inventory, SYSTEM/root file management, SYSTEM/root process management, SYSTEM/root service management, and SYSTEM/root software management.
- Pending ports are tracked in `Data/Agent/Golang_Agent_Migration.md`.
- SYSTEM tasks depend on scheduled-task creation rights; failures should surface through Engine logging.

#### Platform parity
- Windows is the reference path and has the broadest tested feature surface.
- Linux Go runtime builds as `Agent`, installs through systemd, and supports root/SYSTEM Bash quick jobs in first PR.
- Linux CURRENTUSER, tray/helper UI, WireGuard, remote shell, and VNC are pending Go ports.

#### Ansible support
- The agent no longer hosts an Ansible playbook execution role.
- Borealis Ansible control-node execution is Engine-side and should target devices over the Engine-managed WireGuard paths.
- Agent responsibilities for the Ansible architecture are limited to:
  - maintaining device identity and inventory in the Engine
  - sustaining the reverse WireGuard tunnel and related remote-access services
  - exposing the device to Engine-driven automation over the VPN path
