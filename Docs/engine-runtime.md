# Engine Runtime
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Describe the Borealis Engine runtime, its services, configuration, and operational responsibilities.

## Runtime Summary
- Application factory: `Data/Engine/server.py` (Flask + Socket.IO, Eventlet).
- Configuration loader: `Data/Engine/config.py` (environment-first, defaults, TLS discovery).
- API registration: `Data/Engine/services/API/__init__.py` (groups + adapters).
- WebUI serving: `Data/Engine/services/WebUI/` (SPA static assets and 404 fallback).
- Realtime events: `Data/Engine/services/WebSocket/` (quick job results, VPN shell bridge).
- VPN orchestration: `Data/Engine/services/VPN/` (WireGuard server manager + tunnel service).
- Remote desktop proxy: `Data/Engine/services/RemoteDesktop/` (VNC WebSocket bridge).
- Assemblies: `Data/Engine/assembly_management/` and `Data/Engine/services/assemblies/`.
- Watchdog runtime: `Data/Engine/services/API/watchdogs/`.

## Runtime Paths
- Source code: `Data/Engine/` (edit here).
- Runtime copy: `Engine/` (regenerated each launch).
- Database: PostgreSQL via `BOREALIS_DATABASE_URL`.
- Launcher-generated DB/profile env: `Engine/database.env` (database URL, Engine profile metadata, SQLAlchemy pool settings, PostgreSQL tuning values).
- Logs: `Engine/Logs/` (engine.log, error.log, api.log, service logs).
- Ansible runtime: `Engine/Ansible/` (staged manifest, installed collections, generated execution workspaces).
- Certificates: `Engine/Certificates/` (TLS bundle + code signing keys).
- WebUI build output: `Engine/web-interface/` (served as static assets).
- Bundled official assemblies: `Data/Engine/Official_Assemblies/` (generated seed snapshot).
- Aurora checkout: `Engine/Aurora/`.

## API Endpoints
- `GET /health` (No Authentication) - Engine liveness probe.
- The Engine hosts all `/api/*` endpoints listed in [API Reference](api-reference.md).

## Related Documentation
- [Architecture Overview](architecture-overview.md)
- [Database Reference](db-reference.md)
- [Security and Trust](security-and-trust.md)
- [API Reference](api-reference.md)
- [Logging and Operations](logging-and-operations.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [Technical Debt](technical-debt.md)
- [Watchdogs](watchdogs.md)
- [Device Alerts](device-alerts.md)
- [Aegis Cipher](features_to_implement/aegis_cipher.md)
- [Reverse Proxy Functionality](features_to_implement/reverse_proxy_functionality.md)

## Codex Agent (Detailed)
### Source vs runtime
- Edit only in `Data/Engine/`.
- `Engine/` is a runtime mirror and is staged by `Borealis.sh` on Linux.

### EngineContext and lifecycle
- `Data/Engine/server.py` builds an `EngineContext` that includes:
  - TLS paths, WireGuard settings, scheduler, Socket.IO instance.
  - VNC proxy settings (VNC port, ws host/port, session TTL).
- The app factory wires in:
  - API registration: `API.register_api(app, context)`
  - WebUI static hosting: `WebUI.register_web_ui(app, context)`
  - Realtime events: `WebSocket.register_realtime(socketio, context)`
  - Watchdog API/runtime registration from `Data/Engine/services/API/watchdogs/management.py`

### API groups and adapters
- Default groups live in `Data/Engine/services/API/__init__.py` (`DEFAULT_API_GROUPS`).
- Each group has a registrar in `_GROUP_REGISTRARS`.
- `EngineServiceAdapters` exposes:
  - `db_conn_factory` (PostgreSQL-backed DB adapter exposed through the shared compatibility layer).
  - `service_log` (per-service log files with rotation).
  - `jwt_service`, `dpop_validator`, rate limiters, signing keys, GitHub integration.

### Logging expectations
- Main logs: `Engine/Logs/engine.log` and `Engine/Logs/error.log`.
- API access log: `Engine/Logs/api.log` (per-request stats).
- Service logs: `Engine/Logs/<service>.log` (created via `service_log`).
- VPN logs: `Engine/Logs/VPN_Tunnel/tunnel.log` and `Engine/Logs/VPN_Tunnel/remote_shell.log`.

### Adding or updating an API
- Add new routes under `Data/Engine/services/API/<domain>/`.
- Ensure each module starts with the standard header block (purpose + API endpoints).
- Update `Data/Engine/services/API/__init__.py` if you add a new API group.
- Update `Docs/api-reference.md` and the relevant domain doc.

### WebUI hosting and dev mode
- Production UI is served from `Engine/web-interface/`.
- Dev UI uses Vite and still relies on Engine APIs for data.
- The SPA fallback in `Data/Engine/services/WebUI/__init__.py` prevents 404s on client routes.

### Launcher auto-profiling
- `Borealis.sh` profiles the Engine host during deployment and re-deployment.
- Profile selection is based on detected CPU and RAM only. Storage is surfaced in the CLI as guidance, but it does not affect the selected profile.
- The launcher currently auto-selects one of the single-node profiles:
  - `Homelab`
  - `Small Business`
  - `MSP / Production`
  - `Enterprise`
- The launcher writes the selected profile metadata and the resulting PostgreSQL/Engine DB tuning into `Engine/database.env`.
- PostgreSQL tuning is applied through launcher-managed `ALTER SYSTEM` statements during the PostgreSQL configuration step so a re-deploy can raise the profile if the host later gains CPU or RAM.

### WireGuard and VNC wiring
- WireGuard server manager: `Data/Engine/services/VPN/wireguard_server.py`.
- Tunnel orchestration: `Data/Engine/services/VPN/vpn_tunnel_service.py`.
- VNC collaboration state: `Data/Engine/services/RemoteDesktop/vnc_sessions.py`.
- VNC proxy: `Data/Engine/services/RemoteDesktop/vnc_proxy.py`.
- API entrypoints: `/api/vnc/establish`, `/api/vnc/disconnect`, `/api/vnc/handoff`, `/api/vnc/sessions`, `/api/shell/establish`, `/api/shell/disconnect`.
- Persistent tunnels are established by agents via `POST /api/agent/vpn/ensure` and kept online.
- The Engine caches each agent's currently advertised VNC password in memory, reuses that credential across collaboration sessions until the agent restarts or the agent-side daily VNC credential rotation publishes a new revision, fast-probes the advertised UltraVNC listener before re-emitting bootstrap events, waits for agent listener readiness before returning browser bootstrap data when that fast probe misses, and exposes active remote desktop session inventory in `GET /api/server/overview`.

### Assembly runtime
- Assembly cache is initialized in `Data/Engine/assembly_management` and attached to `context.assembly_cache`.
- Quick jobs and scheduled jobs share this runtime to resolve scripts and variables.

### Watchdog evaluator runtime
- `EngineContext.watchdog_runtime` owns the Borealis-native watchdog evaluator.
- Registration and bootstrap happen in `Data/Engine/server.py` after the primary API, WebUI, and Socket.IO registrars.
- The evaluator loop periodically checks enabled watchdogs whose `evaluation_interval_seconds` has elapsed.
- Immediate evaluation still happens on watchdog save and device-override updates so operator changes become visible without waiting for the scheduler tick.
- On startup, the runtime purges any lingering resolved incidents that belong to offline-only watchdogs before the evaluator loop begins.
- Runtime responsibilities include:
  - resolving explicit device and filter-backed targets
  - evaluating rules against cached device data
  - tracking per-device watchdog state
  - opening and resolving incidents
  - dispatching Engine toast notifications, service-control actions, and assembly remediation
  - emitting `watchdog_incidents_changed` and `device_watchdogs_changed`

### Platform parity
- Engine deployment is Linux-only via `Borealis.sh`.
- Linux agent remains incomplete.

### Borealis Engine Codex (Full)
Use this section for Engine work (successor to the legacy server). Shared guidance is consolidated in `ui-and-notifications.md` and other knowledgebase pages.

#### Scope and runtime paths
- Staging / launch: `Borealis.sh` handles Engine staging and launch on Linux once it is invoked directly or via `bootstrap.sh`. (`Borealis.ps1` is agent-only.)
- Edit in `Data/Engine`; the runtime copy used by `borealis-engine.service` still lives under `/Engine`, and direct `systemctl restart borealis-engine` restarts do not currently restage `Data/Engine` automatically. Use `Borealis.sh --EngineDev` or `Borealis.sh --EngineProduction` when source changes need to reach the running service.
- During `Borealis.sh` Engine restaging, Borealis now merges the runtime copies of `software_icons_overrides.json`, `software_uninstall_overrides.json`, and `software_uninstall_blocklist.json` back onto the freshly staged source payloads so operator hotloaded rules survive redeploys. Merge deduplication is name-based, and runtime/operator entries win when the same software name exists in both places.
- `bootstrap.sh` is the supported Linux first-run path for syncing the repo and installing missing OS packages. Direct `Borealis.sh` redeploys intentionally avoid repeated apt/yum/dnf package checks unless bootstrap has opted the run into system package installation.

#### Architecture
- Runtime: `Data/Engine/server.py` with NodeJS + Vite for live dev and Flask for production serving/API endpoints.

#### Development guidelines
- Every Python module under `Data/Engine` or `Engine/Data/Engine` starts with the standard commentary header (purpose + API endpoints). Add the header to any existing module before further edits.

#### Logging
- Primary log: `Engine/Logs/engine.log` with daily rotation (`engine.log.YYYY-MM-DD`); do not auto-delete rotated files.
- Subsystems: `Engine/Logs/<service>.log`; install output to `Engine/Logs/install.log`.
- Keep Engine-specific artifacts within `Engine/Logs/` to preserve the runtime boundary.

#### Security and API parity
- Uses Ed25519 device identities, EdDSA-signed access tokens, and a Borealis-managed Traefik edge with Let's Encrypt for the public browser/agent trust chain while the Python Engine stays on loopback HTTP.
- Implements DPoP validation, short-lived access tokens (about 15 min), SHA-256 hashed refresh tokens (30-day) with explicit reuse errors.
- Enrollment: operator approvals, conflict detection, auditor recording, pruning of expired codes/refresh tokens.
- Background jobs and service adapters maintain compatibility with legacy DB schemas while enabling gradual API takeover.

#### Protected secret storage
- The Engine now exposes an Engine-global Aegis Cipher lifecycle through `Data/Engine/services/aegis_cipher.py` and `Data/Engine/services/API/access_management/aegis.py`.
- The bootstrap gate for operator auth lives in `Data/Engine/services/API/access_management/login.py` and `Data/Engine/services/auth/bootstrap_state.py`.
- Aegis v1 now protects stored credentials, the GitHub API token, operator password hashes, operator TOTP secrets, and passkey cryptographic material at rest using `scrypt` plus `AES-256-GCM`.
- Directory Services adds LDAP/LDAPS and Active Directory credential providers under the auth API group. Generic LDAP uses service-account search plus user-DN bind; Active Directory uses Kerberos password verification with provider-managed realm/KDC settings. Operators can download an LDAPS peer certificate from the provider editor, review subject/issuer/SAN/fingerprint metadata, and pin that certificate for future strict TLS checks. The optional `gssapi` Python package installs only when Kerberos build packages such as `krb5-config` are available, so core Engine and Ansible deployment are not blocked on hosts missing AD prerequisites.
- Directory provider bind passwords and uploaded keytabs are Aegis-protected. Directory users are JIT cached in `users`, keep Borealis TOTP MFA, and cannot register passkeys.
- Setup migrates any legacy plaintext credential, GitHub token, password hash, MFA secret, or passkey cryptographic row into Aegis envelopes and stores KDF metadata plus a verification token in `aegis_cipher_state`.
- The derived key is cached only in Engine memory. Restarting the Engine relocks protected secrets until an admin re-enters the cipher.
- Borealis does not render the login screen until bootstrap reaches `login_required`. Fresh installs require Aegis setup plus first-admin bootstrap; every later restart requires Aegis unlock before normal login or passkey auth can start.
- While locked, operator-facing auth/session checks reject stale cookies and tokens until bootstrap unlock completes. Agent and device trust flows stay online because they do not depend on operator auth secrets.
- Access Management now uses the Credentials page for Aegis status, rotation, and destructive force reset; setup and unlock moved to the bootstrap gate.
- Force reset is the disaster-recovery path when the old cipher is gone: Borealis destroys unrecoverable operator auth secrets, clears the Aegis state row, marks existing users for recovery, marks affected credentials and the GitHub token for re-entry, and disables scheduled jobs that still point at wiped credentials.

#### Reverse VPN tunnels
- WireGuard reverse VPN design and lifecycle are documented in `vpn-and-remote-access.md`.
- The original references were `REVERSE_TUNNELS.md` and `Reverse_VPN_Tunnel_Deployment.md` (now consolidated into this knowledgebase).
- Engine orchestrator: `Data/Engine/services/VPN/vpn_tunnel_service.py` with WireGuard manager `Data/Engine/services/VPN/wireguard_server.py`.
- UI shell bridge: `Data/Engine/services/WebSocket/vpn_shell.py`.

#### WebUI and WebSocket migration
- Static/template handling: `Data/Engine/services/WebUI`; deployment copy paths are wired through `Borealis.sh` with TLS-aware URL generation.
- Stage 6 tasks: migration switch in the legacy server for WebUI delegation and porting device/admin API endpoints into Engine services.
- Stage 7 (queued): `register_realtime` hooks, Engine-side Socket.IO handlers, integration checks, legacy delegation updates.

#### Platform parity
- Linux is the Engine target platform. Keep Engine tooling aligned with Linux packaging and runtime behavior.

#### Ansible support (shared state)
- The Linux Engine now packages an Ansible control-node runtime inside the Engine venv and installs Borealis-managed collections into `Engine/Ansible/collections`.
- Scheduled jobs support Engine-side shared Ansible execution for `local`, `ssh`, and `winrm` contexts.
- Remote SSH/WinRM runs synthesize ephemeral inventories from Borealis device/filter state and active WireGuard sessions, using site-qualified inventory aliases for duplicate-hostname safety.
- Shared remote Ansible transport follows the scheduled job execution context; device `connection_type` metadata does not override the operator-selected `ssh` or `winrm` mode.
- The credentials API now backs stored SSH/WinRM credentials for scheduler selection, while quick-run, cancel, PSRP, and richer recap UX remain in progress.
- When Aegis is locked, credential-backed shared Ansible runs are skipped instead of replayed later, and affected run targets record an explicit lock/reset reason instead of being reported as missing credentials.
- When a credential survives an Aegis force reset but its secret material was destroyed, scheduled jobs surface `credential_reset_required` warnings and stay disabled until the operator restores the missing credential data.
