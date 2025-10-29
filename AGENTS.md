## Notes on Engine Migration
- The Engine lives under `Data/Engine` and is the in-progress replacement for the legacy server (`Data/Server/server.py`), targeting a more stable, flexible, and easier-to-troubleshoot codebase while holding feature parity across Python logic, API endpoints, WebSockets, and the Flask/Vite frontends.
- Migration is tracked in `Engine/Data/Engine/CODE_MIGRATION_TRACKER.md`. Stages 1–5 are complete, covering the Engine bootstrapper, configuration parity, API blueprint scaffolding, automated tests, and legacy bridge hooks.
- Stage 6 ("Plan WebUI migration") is underway: static/template handling moved into the Engine, deployment copy paths are wired, TLS-aware URL generation remains intact, and WebUI tests now exercise key routes. Still pending are the legacy WebUI delegation switch and porting device/admin API endpoints into Engine services (the current active task).
- Stage 7 will shift Socket.IO handlers into the Engine once WebUI parity is ready, adding `register_realtime` hooks, integration checks, and legacy delegation updates.
- Check the migration tracker before Engine edits to align with the staged rollout and avoid jumping ahead of the approved work.

## Architecture At A Glance
- `Borealis.ps1` is the starting point for every aspect of Borealis. It bootstraps dependencies, configures bundled Python virtual environments, and deploys the agents and Engine runtime from a singular script while keeping the legacy server launch path available for comparison during the migration.
- Bundled assets live under `Data/Agent`, `Data/Engine`, and `Dependencies`. Legacy artefacts remain under `Data/Server` solely to document past behaviour. Launching an agent or Engine instance copies the necessary data from these `Data/` directories into sibling `Agent/` and `Engine/` directories at runtime so the development tree stays clean and the runtime stays portable. Launching the legacy server still mirrors `Data/Server` into `/Server` when explicitly requested.
- The Engine stack spans NodeJS + Vite for live development and Python Flask (`Data/Engine/server.py`) for the production frontend (when not using the Vite dev server) and for API endpoints to the Borealis backend. The legacy server (`Data/Server/server.py`) remains only as a reference implementation until the Engine replaces it completely.
- The `script_engines.py` helper exposes a PowerShell runner for potential server-side orchestration, but no current Flask route invokes it; agent-side script execution lives under the roles in `Data/Agent`.
- Agents run inside the packaged Python venv (`Data/Agent` mirrored to `Agent/`). `agent.py` handles the primary connection and hot-loads roles from `Data/Agent/Roles` at agent startup.

## Runtime Folders to Not Modify
Borealis has 3 runtime folders that take staged code and copy it into a new folder to execute from. These folders are wiped often and data in them must not be edited under any circumstance. If you want to make changes, target the "Agent" and the "Engine" staging code. The "Server" staging tree is frozen in time to preserve the legacy behaviour during the Engine cutover. Eventually, when the Engine reaches full feature-parity with the legacy server, we will coordinate a clean cutover to deprecate the old Legacy Server.

The list below outlines the runtime folders and their originating data from their respective staging folders:
- /Server (Staging Folder: /Data/Server) `Do not make modifications to either the staging folder or runtime folder`
- /Engine (Staging Folder: /Data/Engine) `Freely make changes to the staging folder`
- /Agent (Staging Folder: /Data/Agent) `Freely make changes to the staging folder`

## Engine File Headers (Codex Agent Guidance)
- Any new Python modules created under `Data/Engine` or its staging counterpart `Engine/Data/Engine` must begin with the standardized commentary header that documents file purpose and API coverage.
- Mirror the exact formatting shown below, updating the file path, description, and endpoint list to match the new module. If the file does not expose API routes, set the section to `API Endpoints (if applicable): None`.
  ```text
  # ======================================================
  # Data\Engine\services\API\devices\management.py
  # Description: Device inventory, list view, site management, and repository hash endpoints for the Engine API transition layer.
  #
  # API Endpoints (if applicable):
  # - POST /api/agent/details (Device Authenticated) - Ingests hardware and inventory payloads from enrolled agents.
  # ======================================================
  ```
- Always adjust the first line after `# Description:` and each endpoint bullet so operators can quickly understand why the file exists and how to authenticate to any routes.
- When modifying an existing module that is missing this header, add it as part of the change before proceeding with further edits.

## Logging Policy (Centralized, Rotated)
- **Log Locations**
  - Agent: `<ProjectRoot>/Logs/Agent/<service>.log`
  - Legacy Server (reference runtime only): `<ProjectRoot>/Logs/Server/<service>.log`
  - Engine: `<ProjectRoot>/Logs/Engine/<service>.log`
- **General-Purpose Logs**
  - Agent: `agent.log`
  - Legacy Server: `server.log` (only when the legacy runtime is exercised)
  - Engine: `engine.log`
- **Dedicated Logs**
  - Subsystems with significant surface area must use their own `<service>.log`
    - Examples: `ansible.log`, `webrtc.log`, `scheduler.log`
- **Installation / Bootstrap Logs**
  - Agent install: `Logs/Agent/install.log`
  - Legacy server install: `Logs/Server/install.log`
  - Engine install: `Logs/Engine/install.log`
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
- **Troubleshooting Issues via Logs with the Operator**
  - When troubleshooting issues with the operator, you must ensure that you are adding extensive logging to whatever feature you are trying to troubleshoot
  - If the operator reports successfully resolving an issue, you are to ask them if they want you to remove the extra logging functionality or if they want to keep it.
  - When troubleshooting, logs will have the <timestamp>-<service-name>-<log-data> structure to every line of the logs.

## Security Overview
### Overall
- The Engine (and, until it is retired, the legacy server path) enforces mutual trust: each agent presents a unique Ed25519 identity to the backend, the backend issues EdDSA-signed (Ed25519) access tokens bound to that fingerprint, and both sides pin the generated Borealis root CA.
- End-to-end TLS everywhere: the Engine ships an ECDSA P-384 root + leaf chain and only serves TLS 1.3; agents require TLS 1.2+ and "pin" (store the server certificate for future verification) the delivered bundle for both REST and WebSocket traffic, eliminating man-in-the-middle avenues.
- Device enrollment is gated by enrollment/installer codes (*They have configurable expiration and usage limits*) and an operator approval queue; replay-resistant nonces plus rate limits (40 req/min/IP, 12 req/min/fingerprint) prevent brute force or code reuse.
- All device APIs now require Authorization: Bearer headers and a service-context (e.g. SYSTEM or CURRENTUSER) marker; missing, expired, mismatched, or revoked credentials are rejected before any business logic runs.  Operator-driven revoking / device quarantining logic is not yet implemented.
- Replay and credential theft defenses layer in DPoP proof validation (thumbprint binding) on the backend side and short-lived access tokens (15 min) with 30-day refresh tokens hashed via SHA-256.
- Centralized logging under `Logs/Engine` (and `Logs/Server` when using the legacy runtime) captures enrollment approvals, rate-limit hits, signature failures, and auth anomalies for post-incident review.

### Engine Security (Legacy Parity)
- Auto-manages PKI: a persistent Borealis root CA (ECDSA SECP384R1) signs leaf certificates that include localhost SANs, tightened filesystem permissions, and a combined bundle for agent identity / cert pinning.
- Script delivery is code-signed with an Ed25519 key stored under `Certificates/Server/Code-Signing`; agents refuse any payload whose signature or hash does not match the pinned public key.
- Device authentication checks GUID normalization, SSL fingerprint matches, token version counters, and quarantine flags before admitting requests; missing rows with valid tokens auto-recover into placeholder records to avoid accidental lockouts.
- Refresh tokens are never stored in cleartext, only SHA-256 hashes plus DPoP bindings land in SQLite, and reuse after revocation/expiry returns explicit error codes.
- Enrollment workflow queues approvals, detects hostname/fingerprint conflicts, offers merge/overwrite options, and records auditor identities so trust decisions are traceable.
- Background jobs prune expired enrollment codes and refresh tokens, keeping the attack surface small without silently deleting active credentials.

### Agent
- Generates device-wide Ed25519 key pairs on first launch, storing them under `Certificates/Agent/Identity/` with DPAPI protection on Windows (chmod 600 elsewhere) and persisting the Engine-issued GUID alongside.
- Stores refresh/access tokens encrypted (DPAPI) with companion metadata that pins them to the expected Engine certificate fingerprint; mismatches or refresh failures trigger a clean re-enrollment.
- Imports the Engine’s TLS bundle into a dedicated `ssl.SSLContext`, reuses it for the REST session, and injects it into the Socket.IO engine so WebSockets enjoy the same pinning and hostname checks.
- Treats every script payload as hostile until verified: only Ed25519 signatures from the backend are accepted, missing/invalid signatures are logged and dropped, and the trusted signing key is updated only after successful verification between the agent and the Engine.
- Operates outbound-only; there are no listener ports, and every API/WebSocket call flows through `AgentHttpClient.ensure_authenticated`, forcing token refresh logic before retrying.
- Logs bootstrap, enrollment, token refresh, and signature events to daily-rotated files under `Logs/Agent`, giving operators visibility without leaking secrets outside the project root.

### Execution Contexts
The agent runs in the interactive user session. SYSTEM-level script execution is provided by the ScriptExec SYSTEM role using ephemeral scheduled tasks; no separate supervisor or watchdog is required.

## Roles & Extensibility
- Roles live under `Data/Agent/Roles/` and are auto-discovered at startup; no changes are needed in `agent.py` when adding new roles.
- Naming convention: `role_<Purpose>.py` per role.
- Role interface (per module):
  - `ROLE_NAME`: canonical role name used by config (e.g., `screenshot`, `script_exec_system`).
  - `ROLE_CONTEXTS`: list of contexts this role runs in (`interactive`, `system`).
  - `class Role(ctx)`: optional hooks the agent loader will call:
    - `register_events()`: bind any Socket.IO listeners.
    - `on_config(roles: List[dict])`: start/stop per-role tasks based on Engine config.
    - `stop_all()`: cancel tasks and cleanup.
- Standard roles currently shipped:
  - `role_DeviceInventory.py` — collects and periodically posts device inventory/summary.
  - `role_Screenshot.py` — region overlay + periodic capture with WebSocket updates.
  - `role_ScriptExec_CURRENTUSER.py` — runs PowerShell in the logged-in session and provides the tray icon (restart/quit).
  - `role_ScriptExec_SYSTEM.py` — runs PowerShell as SYSTEM via ephemeral Scheduled Tasks.
  - `role_Macro.py` — macro and key/text send helpers.
- Considerations:
  - SYSTEM role requires administrative rights to create/run scheduled tasks as SYSTEM. If elevation is unavailable or policies restrict task creation, SYSTEM jobs will fail gracefully and report errors to the Engine.
  - Roles are “hot-loaded” on startup only (no dynamic import while running).
  - Roles must avoid blocking the main event loop and be resilient to restarts.

## Platform Parity
Windows is the reference environment today. `Borealis.ps1` owns the full deployment story, launching the Engine by default while still being able to start the legacy server path. `Borealis.sh` lags significantly and lacks the same packaging logic. Linux support needs feature parity (virtual environments, supervisor equivalents, and role loading) before macOS work resumes.

## Ansible Support (Unfinished — Do Not Use)
Important: The Ansible integration is not production-ready. Do not rely on it for jobs, quick jobs, or troubleshooting. The current implementation is a work-in-progress and will change.

- Status
  - Agent and Engine code paths contain early scaffolding for running playbooks and posting recap-style output, but behavior is not reliable across Windows hosts. The legacy server retains the same unfinished hooks for historical parity.
  - Expect playbooks to stall, fail silently, or never deliver recaps/cancel events. Cancellation controls and live output are not guaranteed to function.
  - Packaging of Ansible dependencies and Windows collections is incomplete. Connection modes (local/PSRP/WinRM) are not fully exposed or managed.

- Known blockers (Windows)
  - `ansible.windows.*` modules require remoting (PSRP/WinRM) and typically cannot run with `connection: local` on the controller.
  - The SYSTEM service context is a poor fit for loopback remoting without explicit credentials/policy; this leads to no-ops and “forever running” jobs.
  - Collection availability (e.g., `ansible.windows`) and interpreter/paths vary and are not yet normalized across agent installs.

- Near-term guidance
  - Assume all Ansible and playbook-related features are disabled for operational purposes.
  - Do not file bug reports for Ansible behavior; it is intentionally unfinished and unsupported at this time.

- Future direction (not started)
  - Database-fed credential management (per device/site/global), stored securely and surfaced to playbook runs.
  - First-class selection of connection types (local | PSRP | WinRM) from the UI and scheduler, with per-run credential binding.
  - Reliable live output and cancel semantics; hardened recap ingestion and history.
  - Verified packaging of required Ansible components and Windows collections inside the agent venv.
