# Borealis Agents

## Overview
Borealis pairs a no-code workflow canvas with a rapidly evolving remote management stack. The long-term goal is to orchestrate scripts, schedules, and workflows against distributed agents while keeping everything self-contained and portable.

Today the stable core focuses on workflow-driven API and automation scenarios. RMM-level inventory, patching, and fleet coordination exist in early form; the server orchestrator and agent heartbeat are the critical pieces Codex should prioritize.

## Architecture At A Glance
- `Borealis.ps1` is the entry point for every component. It bootstraps dependencies, clones bundled virtual environments, and spins up server, agent, Vite, or Flask modes on demand.
- Bundled assets live under `Data/Agent`, `Data/Server`, and `Dependencies`. Launching installs copies into sibling `Agent/` and `Server/` directories so the development tree stays clean and the runtime stays portable.
- The server stack spans NodeJS + Vite for live development and Flask (`Data/Server/server.py`) for production APIs, backed by Python helpers (`Data/Server/Python_API_Endpoints`) for OCR, scripting, and other services.
- Agents run inside the packaged Python venv (`Data/Agent` mirrored to `Agent/`). `agent.py` handles the primary connection and hot-loads roles from `Data/Agent/Roles` at startup.

## Dependencies & Packaging
`Dependencies/` holds the installers/download payloads Borealis bootstraps on first launch: Python, 7-Zip, AutoHotkey, and NodeJS. Versions are hard-pinned in `Borealis.ps1`; upgrading any runtime requires updating those version constants before repackaging. Nothing self-updates, so Codex should coordinate dependency bumps carefully and test both server and agent bootstrap paths.

## Agent Responsibilities

### Communication Channels
Agents establish REST calls to the Flask backend on port 5000 and keep a WebSocket session for interactive features such as screenshot capture. Future plans include WebRTC for higher-performance remote desktop. No authentication or enrollment handshake exists yet, so agents are implicitly trusted once launched.

### Execution Contexts
The agent runs in the interactive user session. SYSTEM-level script execution is provided by the ScriptExec SYSTEM role using ephemeral scheduled tasks; no separate supervisor or watchdog is required.

### Logging & State
All runtime logs live under `Logs/<ServiceName>` relative to the project root (`Logs/Agent` for the agent family). The project avoids writing to `%ProgramData%`, `%AppData%`, or other system directories so the entire footprint stays under the Borealis folder. Log rotation is not yet implemented; contributions should consider a built-in retention strategy. Configuration and state currently live alongside the agent code.

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
- Launch or test a single agent locally with `.\Borealis.ps1 -Agent` (or combine with `-AgentAction install|repair|launch|remove` as needed). The same entry point manages the server (`-Server`) with either Vite or Flask flags.
- When debugging, tail files under `Logs/Agent`. Use the PowerShell packaging scripts in `Data/Agent/Scripts` to reinstall the user logon scheduled task if it drifts.
- Updates today require manually stopping related processes (`taskkill /IM "node.exe" /IM "pythonw.exe" /IM "python.exe" /F`) followed by a fresh run of `Borealis.ps1 -Agent`. This is a known limitation; future work should automate graceful agent restarts and remote updates without collateral downtime.
- Known stability gaps include suspected Python memory leaks in both the server and agents under multi-day workloads, occasional heartbeat mismatches, and the flashing watchdog console window. A more robust keepalive should eventually remove the watchdog dependency.
- Expect the agent to remain running for days or weeks; contributions should focus on reconnect logic, light resource usage, and graceful shutdown/restart semantics.

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








