# Agent Runtime
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Describe the Borealis agent runtime, its roles, service modes, and how it communicates with the Engine.

## Runtime Summary
- Main entry: `Data/Agent/cmd/agent` builds one Go runtime binary named `Agent.exe` on Windows and `Agent` on Linux.
- Service modes: SYSTEM/root plus same-binary helper mode. Windows CURRENTUSER tracks long-lived helper sentinels per active desktop session and executes signed quick jobs through SYSTEM-brokered `CreateProcessAsUser`. Linux CURRENTUSER reports unsupported until ported.
- Role system: compiled Go role registry under `Data/Agent/internal/roles`.
- Networking: SYSTEM/root runtime owns REST to Engine APIs plus the single Socket.IO connection.
- Security: Ed25519 identity keys, public CA + hostname validation for the Engine FQDN, signed script payloads, and `config.json` token/key storage.

## Role Catalog (Go v1)
- `internal/roles/system_context` - SYSTEM/root quick-job router and script execution for signed `quick_job_run` payloads.
- `internal/roles/current_user` - Windows CURRENTUSER helper sentinel broker, active-session health, direct signed quick-job execution for active user sessions, and Windows tray/status UI. Linux CURRENTUSER reports unsupported in first PR.
- `internal/roles/device_audit` - core CPU, memory, storage media type, removable media, network link speed, OS/build, hardware model/serial with motherboard serial fallback, last reboot, internal IP, device type, uptime, and last-user inventory published through heartbeat payloads.
- `internal/roles/file_management` - SYSTEM/root file-management browse, upload-conflict preflight, lightweight text editing, copy/cut/paste mutations, delete, mkdir, rename, move, upload pull, and download artifact transfer.
- `internal/roles/process_management` - SYSTEM/root live process snapshots, parent/child metadata, cache reuse, and operator-triggered process termination for the Device Summary `Processes` tab.
- `internal/roles/service_management` - SYSTEM/root service inventory publishing plus operator-triggered start, stop, and restart through `service_control_action`.
- `internal/roles/software_management` - SYSTEM/root Windows installed-app inventory with cached icon payloads, Linux dpkg/rpm inventory, refresh requests, and post-uninstall inventory refresh through the SYSTEM quick-job lane.
- `internal/roles/wireguard_tunnel` - SYSTEM/root persistent WireGuard reverse tunnel lifecycle, Engine `/api/agent/vpn/ensure` polling, `vpn_tunnel_start` handling, Windows tunnel-service apply, Linux `wg-quick` apply, and `/api/agent/vpn/ready` reporting.
- `internal/roles/remote_shell` - SYSTEM/root WireGuard-scoped TCP shell listener for Engine `vpn_shell_*` bridge traffic, using PowerShell on Windows and Bash/sh on Linux.
- `internal/roles/vnc` - Windows UltraVNC always-on lifecycle, runtime credential broker, Engine `/api/agent/vnc/ensure` bootstrap, Socket.IO credential/start events, firewall scope, and listener readiness reporting. Linux VNC reports unsupported.
- Pending ports are tracked in `Data/Agent/Golang_Agent_Migration.md`; Linux current-user/tray UI remains pending.

## Agent Settings and Storage
- Installed configuration file: `config.json` beside `Agent.exe`.
- WireGuard runtime configuration file: `wireguard.conf` beside `Agent.exe`/`Agent`, generated from Engine tunnel material.
- Startup cleanup removes `Temp` under the Agent install root so onboarding payload/state files do not persist after service start.
- `config.json` stores `schema_version`, `server_url`, `enrollment_code`, `agent.guid`, `agent.agent_id`, `agent.branch`, `agent.installed_build_id`, `dependency_versions.wireguard`, `dependency_versions.ultravnc`, Ed25519 keys, access/refresh tokens, and Engine script-signing trust material.
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
- Windows installed runtime is `C:\Borealis\Agent.exe` plus `C:\Borealis\config.json`.
- Linux installed runtime is a single compiled `Agent` binary managed by systemd. `--install-service` self-stages temp downloads into `/opt/Borealis/Agent/Agent`, writes `config.json`, and installs the service plus updater timer.
- Go Agent updates use Engine release channels and the local `--update-check` path.

### Service modes and context
- SYSTEM mode is used for elevated tasks (scheduled tasks, VPN, system scripts).
- The SYSTEM runtime is the only Borealis process that authenticates to the Engine, enrolls, refreshes tokens, or opens a Socket.IO connection.
- Interactive user quick jobs now run through the SYSTEM broker by launching signed PowerShell/Batch payloads into active Windows user sessions. Same-binary helper sentinels run in active desktop sessions so role health can report ready helper sessions without exposing Engine tokens to user context.
- Direct Session 0 UI is not supported; Borealis keeps the Engine-facing socket in SYSTEM and bridges into desktop sessions when current-user interaction is required.
- Windows same-binary helpers host the Borealis tray icon and a loopback-only local status UI. The UI shows safe agent status, role health, update/restart actions, diagnostics copy, and an Engine Web UI link while keeping the helper tokenless and free of Engine credentials. Remote access remains silent in this first UI version; the UI does not add remote-session banners, notifications, or consent prompts.
- The agent still labels Engine traffic with `X-Borealis-Agent-Context`, but the supported Windows service path no longer relies on a standalone CURRENTUSER Engine identity.
- Headless Linux agents without an active graphical desktop report desktop-only health surfaces as `No Desktop Environment Active` instead of unhealthy/recovering. This applies to Current User Context helper dispatch and UI-side UltraVNC presentation so server-class Linux hosts do not look broken for missing desktop roles.

### Role discovery and extension
- Go roles are compiled under `Data/Agent/internal/roles`.
- Add new role packages to the explicit registry in `cmd/agent`/runtime wiring instead of relying on dynamic Python module discovery.
- Add regression coverage under package-local Go `*_test.go` files and run the lane through `Data/Agent/Unit_Tests/Agent_Unit_Tests.sh` or `Data/Agent/Unit_Tests/Agent_Unit_Tests.ps1`.

### Networking and authentication
- All REST calls flow through the Go auth client in `Data/Agent/internal/auth`.
- `EnsureAuthenticated` handles identity generation, enrollment, approval polling, and token refresh.
- Socket.IO is used by the SYSTEM runtime for:
  - `quick_job_run` dispatch (system jobs plus broker-backed current-user jobs).
  - `file_management_request` browse, upload-conflict preflight, lightweight text-edit, copy/cut/paste mutate, and transfer orchestration for the Device Summary `File Management` tab.
  - `process_management_request` live process snapshots and process termination for the Device Summary `Processes` tab.
  - `service_control_action` start, stop, and restart requests for services discovered by the Service Management role.
  - `software_inventory_refresh_request` operator-triggered software inventory refresh after icon/override or software action changes.
  - `vpn_tunnel_start` (WireGuard lifecycle; tunnels are persistent and ignore stop events).
  - `vnc_start`, `vnc_stop`, `vnc_refresh`, and `vnc_credential_request` for Windows UltraVNC lifecycle and runtime password delivery.
  - `agent_update_request` to start the local platform updater path.
  - `connect_agent` registration (agent socket registry).
- The SYSTEM socket advertises `helper_contexts=["currentuser"]` when the session broker is running so the Engine can route logical current-user work through the same socket.
- Helper processes never enroll, never refresh tokens, never open Socket.IO, and never talk to the Engine directly. Tray UI actions go through a tokenized loopback SYSTEM broker that exposes only `status.get`, `agent.restart`, `agent.update_check`, and `diagnostics.copy_summary`.
- Current Go Windows CURRENTUSER support uses same-binary helper sentinels for session readiness, direct `CreateProcessAsUser` session launch from SYSTEM for signed quick jobs, and the Windows tray/status UI. Real-host PowerShell Desktop canary validation passed, including denial when the user context attempted to write to root `C:\`; tray UI real-host acceptance remains pending.
- WireGuard tunnels are ensured via `POST /api/agent/vpn/ensure` on boot and refreshed periodically.
- The ensure loop re-establishes the tunnel automatically after network hiccups.
- Go startup posts full timeline milestones before entering the Socket.IO connect loop and keeps heartbeat/status telemetry on the SYSTEM/root runtime.
- Heartbeats/details also carry per-role health snapshots so the Device Details `Agent Health` tab can show current role/service status with last-checked timestamps. Startup status uses `POST /api/agent/status` under the separate `startup` context so later SYSTEM role-health heartbeats do not erase the timeline row.
- The VNC role generates one shared UltraVNC password when the role starts, rotates it again every 24 hours by default (`BOREALIS_VNC_CREDENTIAL_ROTATION_SECONDS`), keeps it in memory only, and returns it to the Engine only through live Agent Socket.IO `vnc_credential_request` calls. The Agent does not probe UltraVNC auth locally by default because each loopback auth probe consumes an UltraVNC login attempt and can trip lockout before Guacamole connects. Set `BOREALIS_VNC_LOCAL_AUTH_VERIFY=1` only for focused diagnostics. The role keeps UltraVNC continuously running once it has the Engine /32 firewall scope, writes UltraVNC config under `%ProgramData%\UltraVNC\` with loopback allowed for local diagnostics, and reports `ready`, `service_state`, `listener_state`, `last_ready_at`, Windows `display_topology`, and Windows `display_virtual_bounds` through VNC ensure, credential, and role-health payloads even when no operator is currently connected.
- VNC role trace logs (`vnc_trace ...`) are disabled by default because the always-on health loop can otherwise produce high-volume logs during normal operation. Set `BOREALIS_VNC_TRACE=1` only for short diagnostic captures.
- The UltraVNC config writer enables capture performance flags (`TurboMode`, full-screen polling defaults, `EnableDriver`, and `EnableHook`) when the official UltraVNC helper DLLs are present beside `winvnc.exe`.

### Token storage
- Refresh/access tokens are stored in `config.json`.
- Device GUID and Engine agent ID are stored in `config.json`.
- When tokens are invalid or expired, the agent refreshes or re-enters enrollment.

### Logging
- Agent runtime log: `Logs/Agent/agent.log` with daily rotation.
- Agent error log: `Logs/Agent/agent.error.log`.
- Agent remote shell log: `Logs/Agent/remote_shell.log`.
- Agent bootstrap/update diagnostics: `<AgentInstallRoot>/Logs/Agent/bootstrap.log`; Windows bootstrap truncates this file at each start and always writes verbose trace/command output there while keeping console/GUI output minimal. Deferred Windows self-replacement writes retry, hash verification, finalization, and task restart output to `Logs/Agent/updater.log`. Linux updater diagnostics use `bootstrap.log`.
- WireGuard role log: `Logs/WireGuard/wireguard.log`.
- WireGuard MSI install log: `Logs/WireGuard/wireguard-msi-install.log`.
- UltraVNC role log: `Logs/UltraVNC/vnc.log`.
- UltraVNC MSI install log: `Logs/UltraVNC/ultravnc-msi-install.log`.

### Troubleshooting flow
- If enrollment fails, check:
  - `Logs/Agent/agent.log` for enrollment errors.
  - `Engine/Services/api-backend/logs/engine.log` for approval or auth failures.
- If current-user execution fails, confirm the SYSTEM broker is advertising helper capability, inspect session inventory for `helper_ready`, and expect `no_interactive_user_session` when no eligible user session exists.
- If CURRENTUSER execution fails, inspect the Go helper broker migration status in `Data/Agent/Golang_Agent_Migration.md`.
- Operator-requested manual updates arrive over the SYSTEM Socket.IO channel as `agent_update_request` and start the local AutoUpdater task/service immediately so the same scheduler-owned update path is used for both manual and hourly runs.
- Engine-managed release channels cache a Go Agent binary bundle containing `Data/Agent/dist/windows-amd64/Agent.exe` and `Data/Agent/dist/linux-amd64/Agent`. Agents download that authenticated bundle, verify SHA-256 when provided, stage the platform binary, and restart through the local service manager.
- Branch installs persist the operator-selected branch in `config.json` as `agent.branch`; local update checks use that branch when it is not `main` so feature-branch agents do not jump release channels accidentally.
- Installed build tracking lives in `config.json` as `agent.installed_build_id`; the Go Agent does not create a standalone `installed_build_id.txt`.
- Update checks do not persist `update_status.json`; transient state such as `state`, `update_available`, and `last_checked_at` is intentionally not stored by the Go Agent.
- Windows update archives and extracted repository payloads are staged under `C:\Borealis\Temp\Updater`; the updater removes update workspaces immediately, schedules full `C:\Borealis\Temp` cleanup after bootstrap exits so stdout/stderr handles are closed, and cleans old accidental `C:\Borealis\Agent` update workspaces.
- Windows self-update stages `Agent.exe.update` beside `Agent.exe` only while replacing a running binary. The detached updater retries the move, verifies the installed `Agent.exe` SHA-256, then runs `Agent.exe --finalize-update` so `config.json` receives the new `agent.installed_build_id` only after verified replacement. Failed deferred replacements remove their staged files, and later bootstrap/update starts remove stale `Agent.exe.tmp`, `Agent.exe.update.tmp`, and abandoned `Agent.exe.update` artifacts.
- The scheduled AutoUpdater cadence is hourly on Windows and Linux.
- If scripts do not run:
  - Confirm `quick_job_run` events and the correct role context.
  - Verify signatures with `signature_utils` logs.
- If VPN fails:
  - Check agent WireGuard role logs and confirm `/api/agent/vpn/ensure` succeeds.
  - Ensure the Engine has an active tunnel session and the WireGuard service is running.
- If VNC fails:
  - Check `Logs/WireGuard/wireguard.log` for tunnel lifecycle and WireGuard recovery events.
  - Call `POST /api/agent/vnc/ensure` and inspect `ready`, `service_state`, `listener_state`, `detail`, and `last_ready_at`.
  - Confirm the active collaboration session still exists from the Engine side with `GET /api/vnc/sessions`.

### Borealis Agent Codex (Full)
Use this section for agent-only work (Borealis agent runtime under `Data/Agent` -> `/Agent`). Shared guidance is consolidated in `ui-and-notifications.md` and the Engine runtime notes.

#### Scope and runtime paths
- Purpose: outbound-only connectivity, device telemetry, scripting, UI helpers.
- Bootstrap: `Agent.exe` owns deploy, repair, update check, config write, scheduled-task registration, and runtime. Windows onboarding stages the Go binary from `Data/Agent/dist/windows-amd64/Agent.exe`; the installed copy runs from `C:\Borealis\Agent.exe`.
- Windows support dependencies: `Agent.exe` can still install UltraVNC and WireGuard from official installers. Installed dependency versions live in `config.json` under `dependency_versions`, and transient installer payloads under `C:\Borealis\Dependencies` are removed after dependency reconciliation. It does not stage Python, create a venv, or call `launch_service.ps1`.
- Existing Windows agents are repairable when `C:\Borealis\Agent.exe`, the `Borealis Agent` scheduled task, and an Engine-accepted token in `config.json` are present.
- Linux first install: download `Data/Agent/dist/linux-amd64/Agent` to any temp path, then run it as root with `--server-url <url> --site-enrollment-code <code> --install-service`; the binary self-stages into `/opt/Borealis/Agent/Agent`, writes `config.json`, installs `borealis-agent.service`, and enables `borealis-agent-updater.timer`.
- Edit in `Data/Agent`, not `/Agent`; runtime copies are ephemeral and wiped regularly.
- Keep Linux Agent installation separate from deployed Engine runtime roots.

#### Logging
- Primary log: `Logs/Agent/agent.log` with daily rotation to `agent.log.YYYY-MM-DD` (never auto-delete rotated files).
- Agent support logs: `Logs/Agent/bootstrap.log` and `Logs/Agent/remote_shell.log`.
- WireGuard logs: `Logs/WireGuard/wireguard.log` and `Logs/WireGuard/wireguard-msi-install.log`.
- UltraVNC logs: `Logs/UltraVNC/vnc.log` and `Logs/UltraVNC/ultravnc-msi-install.log`.
- Keep ad-hoc traces (for example, `system_last.ps1`) under `Logs/` to keep runtime state self-contained.
- Updater trace exception: `Agent.exe` writes bootstrap/update diagnostics to `<AgentInstallRoot>/Logs/Agent/bootstrap.log`; Windows bootstrap starts by truncating this file and keeps verbose output out of operator-facing stdout/stderr streams.
- Troubleshooting: prefix lines with `<timestamp>-<service-name>-<log-data>`; ask operators whether verbose logging should stay after resolution.

#### Security
- Generates device-wide Ed25519 keys on first launch and stores PKCS8/SPKI base64 in `config.json`.
- Refresh/access tokens are stored in `config.json` and bound to the device identity plus Engine-issued token state; mismatches force re-enrollment.
- REST and Socket.IO traffic use the public Engine FQDN with normal CA + hostname validation.
- Validates script payloads with backend-issued Ed25519 signatures before execution.
- Outbound-only; API/WebSocket calls flow through the Go auth client for proactive refresh. Logs bootstrap, enrollment, token refresh, and signature events under `Logs/Agent/`.
- Helper processes inherit no Borealis token state and rely on the local SYSTEM broker for job delivery.

#### Reverse VPN tunnels
- WireGuard reverse VPN design and lifecycle are documented in `vpn-and-remote-access.md`.
- The original references were `REVERSE_TUNNELS.md` and `Reverse_VPN_Tunnel_Deployment.md` (now consolidated into this knowledgebase).
- Agent roles:
  - `Data/Agent/internal/roles/wireguard_tunnel` (Go tunnel lifecycle)
  - `Data/Agent/internal/roles/remote_shell` (Go VPN remote shell TCP server)
  - `Data/Agent/internal/roles/vnc` (Go Windows UltraVNC lifecycle and credential broker)

#### Execution contexts and roles
- Go roles are explicit packages under `Data/Agent/internal/roles`.
- First PR supports SYSTEM/root quick-job script execution, Windows CURRENTUSER helper session health plus direct session PowerShell/Batch execution, core device audit inventory, SYSTEM/root file management, SYSTEM/root process management, SYSTEM/root service management, SYSTEM/root software management, SYSTEM/root WireGuard tunnel lifecycle, SYSTEM/root Remote Shell over WireGuard, Windows VNC lifecycle/credential brokerage over WireGuard, and Go release-channel self-update.
- Pending ports are tracked in `Data/Agent/Golang_Agent_Migration.md`.
- SYSTEM tasks depend on scheduled-task creation rights; failures should surface through Engine logging.

#### Platform parity
- Windows is the reference path and has the broadest tested feature surface.
- Linux Go runtime builds as `Agent`, self-stages during `--install-service`, installs through systemd, enables an hourly updater timer, and supports root/SYSTEM Bash quick jobs in first PR.
- Linux CURRENTUSER and tray UI are pending Go ports. Linux VNC is explicitly unsupported in the current Go role.

#### Ansible support
- The agent no longer hosts an Ansible playbook execution role.
- Borealis Ansible control-node execution is Engine-side and should target devices over the Engine-managed WireGuard paths.
- Agent responsibilities for the Ansible architecture are limited to:
  - maintaining device identity and inventory in the Engine
  - sustaining the reverse WireGuard tunnel and related remote-access services
  - exposing the device to Engine-driven automation over the VPN path
