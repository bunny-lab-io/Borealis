# Borealis Agents
## Logging Policy (Centralized, Rotated)
- **Log Locations**
  - Agent: `<ProjectRoot>/Logs/Agent/<service>.log`
  - Server: `<ProjectRoot>/Logs/Server/<service>.log`
- **General-Purpose Logs**
  - Agent: `agent.log`
  - Server: `server.log`
- **Dedicated Logs**
  - Subsystems with significant surface area must use their own `<service>.log`
    - Examples: `ansible.log`, `webrtc.log`, `scheduler.log`
- **Installation / Bootstrap Logs**
  - Agent install: `Logs/Agent/install.log`
  - Server install: `Logs/Server/install.log`
- **Rotation Policy**
  - All log writers must rotate daily.
  - On day rollover, rename:
    - `<service>.log` → `<service>.log.YYYY-MM-DD`
  - Append only to the current day’s log.
  - **Do not** auto-delete rotated logs.
- **Restrictions**
  - Logs must **only** be written under the project root.
  - Never write logs to:
    - `ProgramData`
    - `AppData`
    - User profiles
    - System temp directories
  - No alternative log fan-out (e.g., per-component folders) unless explicitly coordinated.  
    Prefer single log files per service.
- **Convergence**
  - This policy applies to all new contributions.
  - When modifying existing code, migrate ad-hoc logging into this pattern.

## Overview
Borealis pairs a no-code workflow canvas with a rapidly evolving remote management stack. The long-term goal is to orchestrate scripts, schedules, and workflows against distributed agents while keeping everything self-contained and portable.

Today the stable core focuses on workflow-driven API and automation scenarios. RMM-level inventory, patching, and fleet coordination exist in early form.

## Architecture At A Glance
- `Borealis.ps1` is the starting point for every aspect of Borealis. It bootstraps dependencies, configures bundled Python virtual environments, and deploys the agents and server from a singular script.
- Bundled assets live under `Data/Agent`, `Data/Server`, and `Dependencies`. Launching an agent or server copies the necessary data from these `Data/` directories into sibling `Agent/` and `Server/` directories so the development tree stays clean and the runtime stays portable.
- The server stack spans NodeJS + Vite for live development and Python Flask (`Data/Server/server.py`) for the production frontend (when not using the Vite dev server) and APIs, backed by Python helpers (`Data/Server/Python_API_Endpoints`) for OCR, scripting, and other services. The `script_engines.py` helper exposes a PowerShell runner for potential server-side orchestration, but no current Flask route invokes it; agent-side script execution lives under the roles in `Data/Agent`.
- Agents run inside the packaged Python venv (`Data/Agent` mirrored to `Agent/`). `agent.py` handles the primary connection and hot-loads roles from `Data/Agent/Roles` at startup.

## Dependencies & Packaging
`Dependencies/` holds the installers/download payloads Borealis bootstraps on first launch: Python, 7-Zip, AutoHotkey, and NodeJS. Versions are hard-pinned in `Borealis.ps1`; upgrading any runtime requires updating those version constants before repackaging. Nothing self-updates, so Codex should coordinate dependency bumps carefully and test both server and agent bootstrap paths.

## Agent Responsibilities

### Communication Channels
Agents establish TLS-secured REST calls to the Flask backend on port 5000 and keep an authenticated WebSocket session for interactive features such as screenshot capture. Future plans include WebRTC for higher-performance remote desktop. Every agent now performs an enrollment handshake (see **Secure Enrollment & Tokens** below) prior to opening either channel; all API access is bound to short-lived Ed25519-signed JWTs.

### Secure Enrollment & Tokens
- On first launch the agent generates an Ed25519 identity and stores the private key under `Agent/Borealis/Settings/agent_key.ed25519` (protected with DPAPI on Windows or chmod 600 elsewhere). The public key is retained as SPKI DER and fingerprinted with SHA-256.
- Enrollment starts with an installer code (minted in the Web UI) and proves key possession by signing the server nonce. Upon operator approval the server issues:
  - The canonical device GUID (persisted to `guid.txt` alongside the key material).
  - A short-lived access token (EdDSA/JWT) and a long-lived refresh token (stored encrypted via DPAPI and hashed server-side).
  - The server TLS certificate and script-signing public key so the agent can pin both for future sessions.
- Scripts delivered over REST are signed with the server's Ed25519 code-signing key. The agent validates the signature before anything is queued for execution.
- Access tokens are automatically refreshed before expiry. Refresh failures trigger a re-enrollment.
- All REST calls (heartbeat, script polling, device details, service check-in) use these tokens; WebSocket connections include the `Authorization` header as well.
- Specify the installer code via `--installer-code <code>`, `BOREALIS_INSTALLER_CODE`, or by adding `"installer_code": "<code>"` to `Agent/Borealis/Settings/agent_settings.json`.

### Execution Contexts
The agent runs in the interactive user session. SYSTEM-level script execution is provided by the ScriptExec SYSTEM role using ephemeral scheduled tasks; no separate supervisor or watchdog is required.

### Logging & State
All runtime logs live under `Logs/<ServiceName>` relative to the project root (`Logs/Agent` for the agent family). Logs rotate daily and adopt the `<service>.log.YYYY-MM-DD` suffix on rollover; nothing is deleted automatically. The project avoids writing to `%ProgramData%`, `%AppData%`, or other system directories so the entire footprint stays under the Borealis folder. Configuration and state currently live alongside the agent code.

## Roles & Extensibility
- Roles live under `Data/Agent/Roles/` and are auto‑discovered at startup; no changes are needed in `agent.py` when adding new roles.
- Naming convention: `role_<Purpose>.py` per role.
- Role interface (per module):
  - `ROLE_NAME`: canonical role name used by config (e.g., `screenshot`, `script_exec_system`).
  - `ROLE_CONTEXTS`: list of contexts this role runs in (`interactive`, `system`).
  - `class Role(ctx)`: optional hooks the agent loader will call:
    - `register_events()`: bind any Socket.IO listeners.
    - `on_config(roles: List[dict])`: start/stop per‑role tasks based on server config.
    - `stop_all()`: cancel tasks and cleanup.
- Standard roles currently shipped:
  - `role_DeviceInventory.py` — collects and periodically posts device inventory/summary.
  - `role_Screenshot.py` — region overlay + periodic capture with WebSocket updates.
  - `role_ScriptExec_CURRENTUSER.py` — runs PowerShell in the logged‑in session and provides the tray icon (restart/quit).
  - `role_ScriptExec_SYSTEM.py` — runs PowerShell as SYSTEM via ephemeral Scheduled Tasks.
  - `role_Macro.py` — macro and key/text send helpers.
- Considerations:
  - SYSTEM role requires administrative rights to create/run scheduled tasks as SYSTEM. If elevation is unavailable or policies restrict task creation, SYSTEM jobs will fail gracefully and report errors to the server.
  - Roles are “hot‑loaded” on startup only (no dynamic import while running).
  - Roles must avoid blocking the main event loop and be resilient to restarts.

## Packaging Notes
- `Borealis.ps1` deploys `agent.py`, `role_manager.py`, `Roles/`, and `Python_API_Endpoints/` into `Agent/Borealis/`.
- If packaging a single‑file EXE (PyInstaller), ensure `Roles/` and `Python_API_Endpoints/` are included as data files so role auto‑discovery works at runtime.

## Migration Summary
- Replaced monolithic role code with modular roles under `Data/Agent/Roles/`.
- Removed legacy helpers: `agent_supervisor.py`, `agent_roles.py`, `tray_launcher.py`, `agent_info.py`, and `script_agent.py` (functionality is now inside roles).
- `agent.py` contains only core transport/config logic and role loading.

## Operational Guidance
- Launch or test a single agent locally with `.\\Borealis.ps1 -Agent` (or combine with `-AgentAction install|repair|launch|remove` as needed). The same entry point manages the server (`-Server`) with either Vite or Flask flags.
- When debugging, tail files under `Logs/Agent`. Use the PowerShell packaging scripts in `Data/Agent/Scripts` to reinstall the user logon scheduled task if it drifts.
- Agent installs/repairs now stop only Agent venv Python processes (scoped to `Agent\\*`) and no longer kill global `node.exe`. This prevents accidental termination of the dev WebUI (Vite/esbuild) when working on agents.
- Known stability gaps include suspected Python memory leaks in both the server and agents under multi-day workloads, occasional heartbeat mismatches, and the flashing watchdog console window. A more robust keepalive should eventually remove the watchdog dependency.
- Expect the agent to remain running for days or weeks; contributions should focus on reconnect logic, light resource usage, and graceful shutdown/restart semantics.

## New: Agent Launch Model, Tasks, and Logging
- SYSTEM mode is launched via a wrapper to guarantee WorkingDirectory and capture stdout/stderr:
  - `Agent\\Borealis\\launch_service.ps1` is registered as the scheduled task action for the SYSTEM agent.
  - The wrapper runs `Agent\\Scripts\\pythonw.exe Agent\\Borealis\\agent.py --system-service --config SYSTEM` with `Set-Location` to `Agent\\Borealis` and redirects output to `%ProgramData%\\Borealis\\svc.out.log` and `svc.err.log`.
  - This avoids 0x1/0x2 Task Scheduler errors on hosts where WorkingDirectory is ignored.
- UserHelper (interactive) is still a direct task action to `pythonw.exe "Agent\\Borealis\\agent.py" --config CURRENTUSER`.
- Config files and inheritance:
- Base config now lives at `<ProjectRoot>\\Agent\\Borealis\\Settings\\agent_settings.json`.
- On first run per-suffix, the agent seeds: `Agent\\Borealis\\Settings\\agent_settings_SYSTEM.json` (SYSTEM) and `Agent\\Borealis\\Settings\\agent_settings_CURRENTUSER.json` (interactive) from the base when present.
- Server URL is stored in `<ProjectRoot>\\Agent\\Borealis\\Settings\\server_url.txt`. The deployment script prompts for it on install/repair; press Enter to accept the default `http://localhost:5000`.
- Logging:
  - Early bootstrap log: `<ProjectRoot>\\Logs\\Agent\\bootstrap.log` (helps verify launch + mode).
  - Main logs: `<ProjectRoot>\\Logs\\Agent\\agent.log`, `agent.error.log`.
  - Wrapper logs (SYSTEM task): `%ProgramData%\\Borealis\\svc.out.log`, `svc.err.log`.
  - Last SYSTEM script for debugging: `<ProjectRoot>\\Logs\\Agent\\system_last.ps1`.

## Recommended Dev Flows
- Start the server in Flask-only or dev mode before the agent so WebSocket connect succeeds:
  - Flask quick start: `.\\Borealis.ps1 -Server -Flask -Quick`.
  - Dev UI separately (if needed): `cd Server\\web-interface && npm run dev`.
- Launch/repair agent (elevated PowerShell): `.\\Borealis.ps1 -Agent -AgentAction install`.
- Manual short-run agent checks (non-blocking):
    - `Start-Process .\\Agent\\Scripts\\pythonw.exe -ArgumentList '".\\Agent\\Borealis\\agent.py" --system-service --config SYSTEM'`
    - Verify logs under `Logs\\Agent` and presence of `Agent\\Borealis\\Settings\\agent_settings_SYSTEM.json` and `Agent\\Borealis\\Settings\\server_url.txt`.

## Troubleshooting Checklist
- Agent task “Ready” with 0x1: ensure the SYSTEM task uses `launch_service.ps1` and that WorkingDirectory is `Agent\\Borealis`.
- No logs/configs created: verify venv exists under `Agent\\Scripts` and that wrapper points at the right paths.
- Agent connects but Devices empty: check `agent.error.log` for aiohttp errors and confirm the URL in `Agent\\Borealis\\Settings\\server_url.txt` is reachable; device details post occurs once on connect and then every ~5 minutes.
- Quick jobs “Running” forever: ensure SYSTEM and UserHelper agents are both running; check `system_last.ps1` and wrapper logs for PowerShell errors.
## State & Persistence
`database.db` currently stores device inventory, runtime facts, and job history. Workflow and scheduling metadata are not yet persisted, and no internal scheduler exists beyond WebUI prototypes. Planned scheduling work will need schema updates and migration guidance once implemented.

## Platform Parity
Windows is the reference environment today. `Borealis.ps1` owns the full deployment story, while `Borealis.sh` lags significantly and lacks the same packaging logic. Linux support needs feature parity (virtual environments, supervisor equivalents, and role loading) before macOS work resumes.

## Roadmap & Priorities
- Harden the agent core: modular role loading, reliable reconnect/keepalive, and watchdog replacement.
- Build inventory on demand (process lists, installed software, update metadata) and prepare for patch management workflows similar to commercial RMM tooling.
- Deliver the advanced scheduling matrix: workflows that trigger on timers or external API states, evaluate conditions, and fan out to script roles running as SYSTEM or the interactive user.
- Design a first-class update mechanism that can stage new agent builds, restart gracefully, and hot-detect new roles once they land on disk.
- Clean up deployment ergonomics so agents tolerate weeks of uptime without manual intervention and can accept hot-loaded role updates.

## Security Outlook
Security and authentication are intentionally deferred. There is currently no agent/server handshake, credential model, or ACL on powerful endpoints, so deployments must remain in controlled environments. A future milestone will introduce mutual registration, scoped API tokens, and hardened remote execution surfaces; until then, prioritize resilience and modularity while acknowledging the risk.


## Ansible Support (Unfinished — Do Not Use)

Important: The Ansible integration is not production‑ready. Do not rely on it for jobs, quick jobs, or troubleshooting. The current implementation is a work‑in‑progress and will change.

- Status
  - Agent and server contain early scaffolding for running playbooks and posting recap‑style output, but behavior is not reliable across Windows hosts.
  - Expect playbooks to stall, fail silently, or never deliver recaps/cancel events. Cancellation controls and live output are not guaranteed to function.
  - Packaging of Ansible dependencies and Windows collections is incomplete. Connection modes (local/PSRP/WinRM) are not fully exposed or managed.

- Known blockers (Windows)
  - ansible.windows.* modules require remoting (PSRP/WinRM) and typically cannot run with `connection: local` on the controller.
  - The SYSTEM service context is a poor fit for loopback remoting without explicit credentials/policy; this leads to no‑ops and “forever running” jobs.
  - Collection availability (e.g., `ansible.windows`) and interpreter/paths vary and are not yet normalized across agent installs.

- Near‑term guidance
  - Assume all Ansible and playbook‑related features are disabled for operational purposes.
  - Do not file bug reports for Ansible behavior; it is intentionally unfinished and unsupported at this time.

- Future direction (not started)
  - Database‑fed credential management (per device/site/global), stored securely and surfaced to playbook runs.
  - First‑class selection of connection types (local | PSRP | WinRM) from the UI and scheduler, with per‑run credential binding.
  - Reliable live output and cancel semantics; hardened recap ingestion and history.
  - Verified packaging of required Ansible components and Windows collections inside the agent venv.


## Current State Highlights

This section summarizes what is considered usable vs. experimental today.

- Stable/Usable
  - Agent heartbeat, reconnect logic (ongoing hardening), and device registration.
  - Device inventory collection (SYSTEM role) with periodic updates.
  - Script execution roles:
    - Current user (interactive PowerShell)
    - SYSTEM (PowerShell via ephemeral Scheduled Tasks)
  - Screenshot capture role with Socket.IO updates.
  - Unified SQLite database (`database.db`) for users, sites, device details, scheduled jobs, and activity history.
  - Web UI for device list/details, scheduling basics, assemblies (scripts/workflows) management.

- Experimental/WIP
  - Scheduling matrix beyond basic intervals and immediate/once semantics.
  - Long‑running agent stability under multi‑day workloads (memory/keepalive are being improved).
  - Any Ansible‑related feature (see above) — not supported.

- Terminology
  - “Assemblies” consolidates Scripts/Workflows (and future Playbooks) in the UI. Treat Playbooks as non‑functional until Ansible support matures.








