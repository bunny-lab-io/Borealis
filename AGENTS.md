## Borealis Agent
- **Purpose**: Runs under the packaged Python virtual environment (`Data/Agent` mirrored to `Agent/`) and provides outbound-only connectivity, device telemetry, scripting, and UI capabilities.
- **Bootstrap**: `Borealis.ps1` prepares dependencies, activates the agent venv, and launches the agent alongside the Engine runtime (legacy server boot remains available for parity checks).
- **Runtime Paths**: Do not edit `/Agent`; make changes in `Data/Agent` so the runtime copy stays ephemeral. Runtime folders are wiped regularly.

### Logging
- General log: `Agent/Logs/agent.log`; rotate daily to `agent.log.YYYY-MM-DD` and never delete automatically.
- Subsystems (e.g., `ansible`, `webrtc`, `scheduler`) must log to `Agent/Logs/<service>.log` and follow the same rotation policy.
- Installation output writes to `Agent/Logs/install.log`; keep ad-hoc diagnostics (e.g., `system_last.ps1`, ansible traces) under `Agent/Logs/` so runtime state stays self-contained.
- When troubleshooting with operators, prepend each line with `<timestamp>-<service-name>-<log-data>` and confirm whether to keep or remove verbose logging after resolution.

### Security
- Generates device-wide Ed25519 keys on first launch (`Certificates/Agent/Identity/` with DPAPI protection on Windows, `chmod 600` elsewhere).
- Stores refresh/access tokens encrypted and pins them to the Engine certificate fingerprint; mismatches force re-enrollment.
- Uses a dedicated `ssl.SSLContext` seeded with the Engine’s TLS bundle for REST and Socket.IO traffic.
- Validates all script payloads with Ed25519 signatures issued by the backend before execution.
- Enforces outbound-only communication; every API/WebSocket call flows through `AgentHttpClient.ensure_authenticated` to refresh tokens proactively.
- Logs bootstrap, enrollment, token refresh, and signature events under `Agent/Logs/`.

### Execution Contexts & Roles
- Roles auto-discover from `Data/Agent/Roles/` and require no loader changes.
- Naming convention: `role_<Purpose>.py`, with `ROLE_NAME`, `ROLE_CONTEXTS`, and optional lifecycle hooks (`register_events`, `on_config`, `stop_all`).
- Standard roles: `role_DeviceInventory.py`, `role_Screenshot.py`, `role_ScriptExec_CURRENTUSER.py`, `role_ScriptExec_SYSTEM.py`, `role_Macro.py`.
- SYSTEM tasks depend on scheduled-task creation rights; failures should surface cleanly through Engine logging.

### Platform Parity
- Windows remains the reference environment. Linux (`Borealis.sh`) trails in feature parity (venv setup, supervision, role loading) and must be aligned before macOS work continues.

### Ansible Support (Unfinished)
- Agent and Engine scaffolding exists but is unreliable: expect stalled or silent failures, inconsistent recap delivery, and incomplete packaging of required collections.
- Windows blockers: `ansible.windows.*` modules generally require PSRP/WinRM, SYSTEM context lacks loopback remoting guarantees, and interpreter paths vary.
- Guidance: treat Ansible features as disabled; do not file bugs until the packaging and controller story is complete.
- Future direction includes credential management, selectable connection types, reliable live output/cancel semantics, and packaged collections.

## Borealis Engine
- **Role**: The actively developed successor to the legacy server (`Data/Server/server.py`), aiming for feature parity across Python services, REST APIs, WebSockets, and the Flask/Vite frontends while improving stability, flexibility, and troubleshooting.
- **Migration Tracker**: `Engine/Data/Engine/CODE_MIGRATION_TRACKER.md` records stages, active tasks, and completed work. Stages 1–5 (bootstrap, configuration parity, API scaffolding, testing, and legacy bridge) are complete; Stage 6 (WebUI migration) is in progress; Stage 7 (WebSocket migration) is queued.
- **Architecture**: Runs via `Data/Engine/server.py` with NodeJS + Vite for live development and Flask for production serving and API endpoints. `Borealis.ps1` launches the Engine by default while keeping the legacy server switch available for regression comparisons.
- **Runtime Paths**: Edit code in `Data/Engine`; runtime copies are placed under `/Engine` and discarded frequently. `/Server` remains untouched unless explicitly running the legacy path.

### Development Guidelines
- Every new Python module under `Data/Engine` or `Engine/Data/Engine` must start with the standard commentary header describing purpose and any API endpoints. Add the header to existing modules that lack it before further edits.
- Reference the migration tracker before making Engine changes to avoid jumping ahead of the approved stage.

### Logging
- General log: `Engine/Logs/engine.log` with daily rotation (`engine.log.YYYY-MM-DD`); do not auto-delete rotated files.
- Subsystems should log to `Engine/Logs/<service>.log`; installation output belongs in `Engine/Logs/install.log`.
- Adhere to the centralized logging policy and keep Engine-specific artifacts within `Engine/Logs/` to preserve the runtime boundary.

### Security & API Parity
- Shares the mutual trust model with the legacy server: Ed25519 device identities, EdDSA-signed access tokens, pinned Borealis root CA, TLS 1.3-only serving, and Authorization headers plus service-context markers on every device API.
- Implements DPoP proof validation, short-lived access tokens (15 min), and SHA-256–hashed refresh tokens with 30-day lifetime and explicit reuse errors.
- Enrollment workflows include operator approval queues, conflict detection, auditor recording, and pruning of expired codes/refresh tokens.
- Background jobs and service adapters preserve compatibility with legacy database schemas while allowing gradual API takeover.

### WebUI & WebSocket Migration
- Static/template handling resides in `Data/Engine/services/WebUI`, with deployment copy paths wired through `Borealis.ps1` and TLS-aware URL generation intact.
- Pending tasks in Stage 6: add the migration switch in the legacy server for WebUI delegation and finish porting device/admin API endpoints into Engine services (current active task).
- Stage 7 will introduce `register_realtime` hooks, Engine-side Socket.IO handlers, integration checks, and legacy delegation updates.

### Platform Parity
- Windows support is the primary target. Ensure Engine tooling remains aligned with the agent experience; Linux packaging must catch up before macOS work resumes.

### Ansible Support (Shared State)
- Mirrors the agent’s unfinished story; Engine adapters should treat Ansible orchestration as experimental until packaging, connection management, and logging mature.

## Borealis Server (Legacy)
- **Role**: The historical Flask runtime under `Data/Server/server.py`. It remains available for reference and parity testing while the Engine takes over; no new features should land here.
- **Usage**: Launch only when comparing behaviour or during migration fallback scenarios. `Borealis.ps1` can still mirror `Data/Server` into `/Server`, but the staging tree itself must remain untouched.
- **Runtime Paths**: `/Server` and `Data/Server` are read-only for day-to-day work; edit Engine staging instead.

### Logging
- Legacy logs write to `Logs/Server/<service>.log` with the same rotation policy (`<service>.log.YYYY-MM-DD`). Installation logs belong in `Logs/Server/install.log`. Avoid changes unless investigating historical behaviour.

### Security Posture
- Shares the same mutual-authentication and TLS posture as the Engine. Device authentication checks GUID normalization, SSL fingerprint matches, token version counters, and quarantine flags before admitting requests.
- Refresh tokens remain hashed (SHA-256) and DPoP-bound; reuse after revocation or expiry returns explicit errors. Enrollment workflows preserve operator approvals and auditing.

### Platform Notes
- Exists primarily to document past behaviour and assist the Engine migration. Future platform parity work should target the Engine; the legacy server will be deprecated once feature parity is confirmed.
