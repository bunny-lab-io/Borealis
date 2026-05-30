# Engine Runtime

Describe the Borealis Engine runtime, its services, configuration, and operational responsibilities.

## Runtime Summary
- Application factory: `Data/Engine/Containers/api-backend/data/server.py` (Flask + Socket.IO, Eventlet) inside the `api-backend` container.
- Configuration loader: `Data/Engine/Containers/api-backend/data/config.py` (environment-first, defaults, TLS discovery).
- API registration: `Data/Engine/Containers/api-backend/data/services/API/__init__.py` (groups + adapters).
- WebUI serving: `webui-frontend` container owns production static serving and dev Vite HMR; the Engine WebUI fallback remains for non-container and test paths.
- Realtime events: `Data/Engine/Containers/api-backend/data/services/WebSocket/` (quick job results, VPN shell bridge).
- VPN orchestration: `Data/Engine/Containers/api-backend/data/services/VPN/` (WireGuard server manager + tunnel service).
- Remote desktop proxy: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/` (Apache Guacamole VNC bridge through local `guacd`).
- Assemblies: `Data/Engine/Containers/api-backend/data/assembly_management/` and `Data/Engine/Containers/api-backend/data/services/assemblies/`.
- Watchdog runtime: `Data/Engine/Containers/api-backend/data/services/API/watchdogs/`.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /health` (No Authentication) - Engine liveness probe.
    - The Engine hosts all `/api/*` endpoints listed in [API Reference](../Data%20and%20Schema/api-reference.md).

    ### Related documentation

    - [Architecture Overview](../../Reference/architecture-overview.md)
    - [Docker Stack Breakdown](Stack_Breakdown.md)
    - [Database Reference](../Data%20and%20Schema/db-reference.md)
    - [Security and Trust](../../Reference/security-and-trust.md)
    - [API Reference](../Data%20and%20Schema/api-reference.md)
    - [Engine Log Management](../../Using%20the%20Platform/engine-log-management.md)
    - [Remote Shell](../../Using%20the%20Platform/remote-shell.md)
    - [Watchdogs](../../Using%20the%20Platform/watchdogs.md)
    - [Alerts](../../Using%20the%20Platform/alerts.md)

    ### Source vs runtime
    - Edit API/backend code in `Data/Engine/Containers/api-backend/data/`.
    - Edit WebUI code in `Data/Engine/Containers/webui-frontend/data/web-interface/` for committed source changes. For rapid dev-mode HMR edits, use `Engine/Services/webui-frontend/data/web-interface/`.
    - Keep `Data/Engine/` for package shims, unit tests, and container roots.
    - Container source lives under `Data/Engine/Containers/` for Compose, Dockerfiles, build manifests, service entrypoints, and service-owned source trees.
    - `Engine/` is generated runtime state. Do not edit it directly.
    - Deploy state lives in `Engine/Deploy/compose.env`, `Engine/Deploy/runtime.env`, `Engine/Deploy/webui-frontend.env`, `Engine/Deploy/image-manifest.json`, `Engine/Deploy/deploy-manifest.json`, and `Engine/Deploy/build.log`.
    - Service state lives in `Engine/Services/<role>/` with only directories used by that service.
    - Logs live under `Engine/Services/<role>/logs/`; api-backend writes API and domain logs under `Engine/Services/api-backend/logs/`.
    - Ansible runtime lives under `Engine/Services/api-backend/cache/Ansible/`.
    - TLS and signing certificates live under `Engine/Services/api-backend/secrets/Certificates/`.
    - Bundled official assemblies live under `Data/Engine/Containers/api-backend/data/Official_Assemblies/`; managed Aurora checkout lives under `Engine/Services/api-backend/cache/Aurora/`.
    - The Compose project name is `borealis-engine`.
    - `Engine.sh` computes input hashes from Dockerfiles, build context, container entrypoints, source files, dependency manifests, and mode inputs, then builds images as `borealis-engine/<service>:sha-<hash>`.
    - Mode inputs affect image hashes only for services with mode-specific build targets. Today that means `webui-frontend`; DB, guacd, WireGuard, Traefik, and API images do not rebuild merely because the operator switches `prod`/`dev`.
    - Docker Buildx cache is stored under `Engine/Deploy/cache/buildkit/<service>/` when usable; plain Docker build remains the fallback.
    - Deploy output uses compact colored service status lines in interactive terminals; set `NO_COLOR=1` to force plain text.
    - No-op redeploys reuse existing image tags and skip Compose when deploy manifest, runtime env, image hashes, and container state already match.
    - Image tag changes and WebUI mode changes are kept out of shared service state hashes; a WebUI-only image change or prod/dev mode flip should run scoped Compose reconciliation for `webui-frontend` only when the rest of the stack is healthy.
    - Scoped image redeploys use `docker compose up -d --no-deps --no-build <service...>` after Borealis has already built changed images, so unrelated services are not intentionally recreated.
    - Compose health checks gate startup: PostgreSQL `pg_isready`, WireGuard control socket presence, guacd TCP `4822`, WebUI loopback HTTP, API `/health`, and Traefik ping on `127.0.0.1:8082`.

    ### Container service boundaries
    - `api-backend` runs the Python Engine API, Socket.IO, live operator sessions, workflow APIs, and VNC WebSocket proxy. It binds `127.0.0.1:5000`.
    - `job-scheduler` owns the scheduled-job tick loop, Postgres work leases, Docker-backed service actions, and `site-worker-<uuid>` lifecycle. It owns the host Docker socket in container mode.
    - Site workers execute site-scoped pressure work such as automatic local-network onboarding outside the API process. They do not mount the Docker socket.
    - `webui-frontend` serves the production WebUI or Vite HMR on stable loopback port `127.0.0.1:8000`. Dev mode bind-mounts `Engine/Services/webui-frontend/data/web-interface/` into the container for host-side UI edits.
    - `traefik-edge` owns public HTTP/HTTPS on `80/443`, ACME storage, Traefik config, UI/API/Socket.IO/VNC routing, and edge logs.
    - `postgres-db` owns PostgreSQL state under `Engine/Services/postgres-db/state` and binds `127.0.0.1:5432`.
    - `remote-desktop-guacd` runs VNC-only `guacd` on `127.0.0.1:4822`.
    - `wireguard-tunnel` owns privileged WireGuard command execution, `/dev/net/tun`, `NET_ADMIN`, the `borealis-wg` interface, and the Unix control socket under `Engine/Services/wireguard-tunnel/run/control.sock`.

    ### Launcher commands
    - `Engine.sh deploy` or `Engine.sh deploy prod`: production WebUI.
    - `Engine.sh deploy dev`: Vite HMR WebUI behind Traefik. API, PostgreSQL, Traefik, guacd, and WireGuard stay on the current shared runtime config unless their own inputs changed.
    - `Engine.sh --service api-backend restart`: restart API container only.
    - `Engine.sh --service webui-frontend rebuild dev|prod`: rebuild and recreate WebUI container only.
    - `Engine.sh --service traefik-edge reload`: restart Traefik edge after config/env changes.
    - `Engine.sh --service postgres-db restart`: restart PostgreSQL container.
    - `Engine.sh --service remote-desktop-guacd restart`: restart guacd container.
    - `Engine.sh --service wireguard-tunnel reconcile`: query the WireGuard control socket from the tunnel container.

    ### One-shot legacy migration helpers
    - `Data/Engine/Containers/sterilize-systemd-runtime.sh`: migration-only helper that stops/removes legacy Borealis systemd units, disables host PostgreSQL units, best-effort removes old `borealis-wg` state, dumps the legacy `borealis` database when reachable, and renames `Engine/` to `Engine.old/`.
    - `Data/Engine/Containers/import-legacy-postgres-dump.sh <dump.sql>`: migration-only helper that imports a preserved logical dump into container PostgreSQL after deployment.
    - These helpers are not called by `Engine.sh`.

    ### EngineContext and lifecycle
    - `Data/Engine/Containers/api-backend/data/server.py` builds an `EngineContext` that includes:
      - TLS paths, WireGuard settings, scheduler, Socket.IO instance.
    - VNC proxy settings (VNC port, ws host/port, session TTL, Guacamole path, and `guacd` host/port).
    - The app factory wires in:
      - API registration: `API.register_api(app, context)`
      - WebUI static hosting: `WebUI.register_web_ui(app, context)`
      - Realtime events: `WebSocket.register_realtime(socketio, context)`
      - Watchdog API/runtime registration from `Data/Engine/Containers/api-backend/data/services/API/watchdogs/management.py`

    ### API groups and adapters
    - Default groups live in `Data/Engine/Containers/api-backend/data/services/API/__init__.py` (`DEFAULT_API_GROUPS`).
    - Each group has a registrar in `_GROUP_REGISTRARS`.
    - `EngineServiceAdapters` exposes:
      - `db_conn_factory` (PostgreSQL-backed DB adapter exposed through the shared compatibility layer).
      - `service_log` (per-service log files with rotation).
      - `jwt_service`, `dpop_validator`, rate limiters, signing keys, GitHub integration.

    ### Logging expectations
    - Main logs: `Engine/Services/api-backend/logs/engine.log` and `Engine/Services/api-backend/logs/error.log`.
    - API access log: `Engine/Services/api-backend/logs/api.log` (per-request stats).
    - Service logs: `Engine/Services/api-backend/logs/<service>.log` (created via `service_log`).
    - VPN logs: `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log` and `Engine/Services/api-backend/logs/VPN_Tunnel/remote_shell.log`.

    ### Adding or updating an API
    - Add new routes under `Data/Engine/Containers/api-backend/data/services/API/<domain>/`.
    - Ensure each module starts with the standard header block (purpose + API endpoints).
    - Update `Data/Engine/Containers/api-backend/data/services/API/__init__.py` if you add a new API group.
    - Update `Docs/Reference/Data and Schema/api-reference.md` and the relevant domain doc.

    ### WebUI hosting and dev mode
    - Production UI is served by the `webui-frontend` container from its built static output.
    - Dev UI runs Vite HMR behind `traefik-edge`.
    - The API backend sets `BOREALIS_WEBUI_EXTERNAL=1` in container mode so `Data.Engine.bootstrapper` skips Engine-side WebUI staging/build.
    - The SPA fallback in `Data/Engine/Containers/api-backend/data/services/WebUI/__init__.py` remains for tests and non-container execution.

    ### PostgreSQL profile notes
    - `Engine.sh deploy` detects vCPU and RAM on every deploy/redeploy, selects the lower CPU/RAM profile rank, and writes profile metadata into `Engine/Deploy/compose.env`.
    - Profile tuning owns Engine DB pool values, PostgreSQL startup settings, and `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY`.
    - Site-worker scheduled-lane values are active work-item slots: Homelab `5`, Small Business `8`, MSP / Production `12`, and Enterprise `16`. Enterprise Clustered remains docs-only at `16` per node.
    - Shared Ansible work items can target multiple hosts inside one slot. Individual Ansible work items target one host per slot.
    - PostgreSQL settings are applied through the `postgres-db` Compose command. Operators should not run manual PostgreSQL tuning steps for normal profile-managed deployments.

    ### WireGuard and VNC wiring
    - WireGuard server manager: `Data/Engine/Containers/api-backend/data/services/VPN/wireguard_server.py`.
    - Tunnel orchestration: `Data/Engine/Containers/api-backend/data/services/VPN/vpn_tunnel_service.py`.
    - VNC collaboration state: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/vnc_sessions.py`.
    - VNC proxy: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/vnc_proxy.py`.
    - Guacamole VNC bridge: `Data/Engine/Containers/api-backend/data/services/RemoteDesktop/guacamole_proxy.py`.
    - API entrypoints: `/api/vnc/viewers`, `/api/vnc/establish`, `/api/vnc/disconnect`, `/api/vnc/handoff`, `/api/vnc/sessions`, `/api/shell/establish`, `/api/shell/disconnect`.
    - Persistent tunnels are established by agents via `POST /api/agent/vpn/ensure`, then marked dispatch-ready by `POST /api/agent/vpn/ready` after the active service/config/firewall path is applied.
    - The Engine requests the current Agent VNC password on demand over the registered Agent Socket.IO channel during `/api/vnc/establish`, uses that live credential for the Guacamole token it is minting, and does not maintain an agent-level VNC password cache. It still fast-probes the advertised UltraVNC listener, waits for listener readiness before returning browser bootstrap data when that fast probe misses, skips the backend RFB VNCAuth probe by default to avoid consuming UltraVNC login attempts before Guacamole connects, and exposes active remote desktop session inventory in `GET /api/server/overview`. Set `BOREALIS_VNC_AUTH_PROBE=1` only for focused backend VNCAuth diagnostics.
    - Apache Guacamole is the sole browser remote desktop path. Guacamole VNC uses local `guacd` on `127.0.0.1:4822` by default, is served through `/remote-desktop/vnc/guacamole`, and never returns the UltraVNC password to the browser.
    - `remote-desktop-guacd` uses Apache Guacamole Server 1.6.0 in VNC-only mode, binds loopback port `4822`, and mirrors guacd stdout/stderr into `Engine/Services/remote-desktop-guacd/logs/guacd.log`.

    ### Assembly runtime
    - Assembly cache is initialized in `Data/Engine/Containers/api-backend/data/assembly_management` and attached to `context.assembly_cache`.
    - Quick jobs and scheduled jobs share this runtime to resolve scripts and variables.

    ### Watchdog evaluator runtime
    - `EngineContext.watchdog_runtime` owns the Borealis-native watchdog evaluator.
    - Registration and bootstrap happen in `Data/Engine/Containers/api-backend/data/server.py` after the primary API, WebUI, and Socket.IO registrars.
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
    - Engine deployment is Linux-only via `Engine.sh`.
    - Linux agent remains incomplete.

    ### Borealis Engine Codex (Full)
    Use this section for Engine work (successor to the legacy server). Shared guidance is consolidated in `Docs/Reference/ui-and-notifications.md` and other knowledgebase pages.

    #### Scope and runtime paths
    - Staging / launch: `Engine.sh` handles Linux first install, dependency checks, Engine container build, and Compose deployment. (`Agent.exe` is Windows Agent-only.)
    - Edit in `Data/Engine` and `Data/Engine/Containers`; use `Engine.sh deploy dev|prod` when source changes need to reach the running service.
    - Container redeploys use committed source JSON for `software_icons_overrides.json`, `software_uninstall_overrides.json`, and `software_uninstall_blocklist.json`; commit operator-tested hotloaded rules that must survive image rebuilds.
    - Raw one-line or repo-option `Engine.sh` runs sync first, then re-execs the installed `Engine.sh`; local `Engine.sh deploy` uses existing on-disk source and does not update git.

    #### Architecture
    - Runtime: `Data/Engine/Containers/api-backend/data/server.py` with NodeJS + Vite for live dev and Flask for production serving/API endpoints.

    #### Development guidelines
    - Every Python module under `Data/Engine` or `Engine/Data/Engine` starts with the standard commentary header (purpose + API endpoints). Add the header to any existing module before further edits.

    #### Logging
    - Primary API log: `Engine/Services/api-backend/logs/engine.log` with daily rotation (`engine.log.YYYY-MM-DD`); do not auto-delete rotated files.
    - Subsystems: `Engine/Services/api-backend/logs/<service>.log`; container build output: `Engine/Deploy/build.log`; Traefik logs: `Engine/Services/traefik-edge/logs/`.
    - Keep Engine-specific artifacts within `Engine/Services/<role>/logs/` or `Engine/Deploy/` to preserve the runtime boundary.

    #### Security and API parity
    - Uses Ed25519 device identities, EdDSA-signed access tokens, and a Borealis-managed Traefik edge with Let's Encrypt for the public browser/agent trust chain while the Python Engine stays on loopback HTTP.
    - Implements DPoP validation, short-lived access tokens (about 15 min), SHA-256 hashed refresh tokens (30-day) with explicit reuse errors.
    - Enrollment: operator approvals, conflict detection, auditor recording, pruning of expired codes/refresh tokens.
    - Background jobs and service adapters maintain compatibility with legacy DB schemas while enabling gradual API takeover.

    #### Protected secret storage
    - The Engine now exposes an Engine-global Aegis Cipher lifecycle through `Data/Engine/Containers/api-backend/data/services/aegis_cipher.py` and `Data/Engine/Containers/api-backend/data/services/API/access_management/aegis.py`.
    - The bootstrap gate for operator auth lives in `Data/Engine/Containers/api-backend/data/services/API/access_management/login.py` and `Data/Engine/Containers/api-backend/data/services/auth/bootstrap_state.py`.
    - Aegis v1 now protects stored credentials, the GitHub API token, operator password hashes, operator TOTP secrets, and passkey cryptographic material at rest using `scrypt` plus `AES-256-GCM`.
    - Directory Services adds LDAP/LDAPS and Active Directory credential providers under the auth API group. Generic LDAP uses service-account search plus user-DN bind; Active Directory uses Kerberos password verification with provider-managed realm/KDC settings. Operators can define provider-scoped host overrides so FQDN server URLs connect to explicit IP addresses without editing Engine host records; TLS still uses the FQDN for SNI and certificate name validation. Operators can download an LDAPS peer certificate from the provider editor, review subject/issuer/SAN/fingerprint metadata, and pin that certificate for future strict TLS checks. The optional `gssapi` Python package installs only when Kerberos build packages such as `krb5-config` are available, so core Engine and Ansible deployment are not blocked on hosts missing AD prerequisites.
    - Directory provider bind passwords and uploaded keytabs are Aegis-protected. Directory users are JIT cached in `users`, keep Borealis TOTP MFA, and cannot register passkeys.
    - Setup migrates any legacy plaintext credential, GitHub token, password hash, MFA secret, or passkey cryptographic row into Aegis envelopes and stores KDF metadata plus a verification token in `aegis_cipher_state`.
    - The derived key is cached only in Engine memory. Restarting the Engine relocks protected secrets until an admin re-enters the cipher.
    - Borealis does not render the login screen until bootstrap reaches `login_required`. Fresh installs require Aegis setup plus first-admin bootstrap; every later restart requires Aegis unlock before normal login or passkey auth can start.
    - While locked, operator-facing auth/session checks reject stale cookies and tokens until bootstrap unlock completes. Agent and device trust flows stay online because they do not depend on operator auth secrets.
    - Access Management now uses the Credentials page for Aegis status, rotation, and destructive force reset; setup and unlock moved to the bootstrap gate.
    - Force reset is the disaster-recovery path when the old cipher is gone: Borealis destroys unrecoverable operator auth secrets, clears the Aegis state row, marks existing users for recovery, marks affected credentials and the GitHub token for re-entry, and disables scheduled jobs that still point at wiped credentials.

    #### Reverse VPN tunnels
    - WireGuard reverse VPN design and lifecycle are documented in `Docs/Using the Platform/remote-shell.md` and `Docs/Using the Platform/remote-desktop.md`.
    - The original references were `REVERSE_TUNNELS.md` and `Reverse_VPN_Tunnel_Deployment.md` (now consolidated into this knowledgebase).
    - Engine orchestrator: `Data/Engine/Containers/api-backend/data/services/VPN/vpn_tunnel_service.py` with WireGuard manager `Data/Engine/Containers/api-backend/data/services/VPN/wireguard_server.py`.
    - UI shell bridge: `Data/Engine/Containers/api-backend/data/services/WebSocket/vpn_shell.py`.

    #### WebUI and WebSocket migration
    - Static/template handling: `Data/Engine/Containers/api-backend/data/services/WebUI`; deployment copy paths are wired through `Engine.sh` with TLS-aware URL generation.
    - Stage 6 tasks: migration switch in the legacy server for WebUI delegation and porting device/admin API endpoints into Engine services.
    - Stage 7 (queued): `register_realtime` hooks, Engine-side Socket.IO handlers, integration checks, legacy delegation updates.

    #### Platform parity
    - Linux is the Engine target platform. Keep Engine tooling aligned with Docker Engine plus Docker Compose, not Docker Desktop.

    #### Ansible support (shared state)
    - The Linux Engine now packages Ansible control-node tooling inside worker runtimes and installs Borealis-managed collections into `Engine/Services/api-backend/cache/Ansible/collections` for shared worker access.
    - Scheduled jobs support Engine-side shared Ansible execution for `local`, `ssh`, and `winrm` contexts.
    - Remote SSH/WinRM runs synthesize ephemeral inventories from Borealis device/filter state and active WireGuard sessions, using site-qualified inventory aliases for duplicate-hostname safety.
    - Shared remote Ansible transport follows the scheduled job execution context; device `connection_type` metadata does not override the operator-selected `ssh` or `winrm` mode.
    - The credentials API now backs stored SSH/WinRM credentials for scheduler selection, while quick-run, cancel, PSRP, and richer recap UX remain in progress.
    - When Aegis is locked, credential-backed shared Ansible runs are skipped instead of replayed later, and affected run targets record an explicit lock/reset reason instead of being reported as missing credentials.
    - When a credential survives an Aegis force reset but its secret material was destroyed, scheduled jobs surface `credential_reset_required` warnings and stay disabled until the operator restores the missing credential data.
