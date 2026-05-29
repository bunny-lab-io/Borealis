# Logging and Operations

## Purpose
Describe Borealis operational logging, retention, and core runtime checks.

## Log Locations
- Engine container build log: `Engine/Deploy/build.log`.
- Engine BuildKit cache: `Engine/Deploy/cache/buildkit/<service>/` when Docker Buildx is usable.
- Engine primary log: `Engine/Services/api-backend/logs/engine.log` (daily rotation).
- Engine error log: `Engine/Services/api-backend/logs/error.log`.
- Engine API access log: `Engine/Services/api-backend/logs/api.log`.
- Engine Traefik log: `Engine/Services/traefik-edge/logs/traefik.log`.
- Engine Traefik access log: `Engine/Services/traefik-edge/logs/traefik-access.log`.
- Engine guacd log: `Engine/Services/remote-desktop-guacd/logs/guacd.log`.
- Service logs: `Engine/Services/<role>/logs/` plus API per-domain logs under `Engine/Services/api-backend/logs/`.
- Watchdog service log: `Engine/Services/api-backend/logs/watchdogs.log`.
- VPN logs: `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log`, `Engine/Services/api-backend/logs/VPN_Tunnel/remote_shell.log`, and WireGuard control logs under `Engine/Services/wireguard-tunnel/logs/control.log`.
- Agent runtime logs: `Logs/Agent/agent.log`, `Logs/Agent/agent.error.log`, `Logs/Agent/remote_shell.log`, and `Logs/Agent/role_recovery.log`.
- Agent bootstrap/update diagnostics: `<AgentInstallRoot>/Logs/Agent/bootstrap.log`. Windows bootstrap truncates this file at each start so it contains only the latest run, and always writes verbose trace/command output there. Operator-facing console/GUI output stays limited to high-level steps, warnings, and errors.
- Agent WireGuard logs: `Logs/WireGuard/wireguard.log` and `Logs/WireGuard/wireguard-msi-install.log`.
- Agent UltraVNC logs: `Logs/UltraVNC/vnc.log` and `Logs/UltraVNC/ultravnc-msi-install.log`.

## Log Retention
- Retention is managed via `/api/server/logs` endpoints.
- Retention overrides are stored in `Engine/Services/api-backend/logs/retention_policy.json`.
- Agent retention is local in `agent.json` as `agent.log_retention_days`, defaults to `1`, rotates all Agent logs daily, and prunes rotated logs older than the configured value on next Agent start/write.

## Operational Health
- `GET /health` returns liveness status.
- `GET /api/server/time` returns server clock information after login.
- `GET /api/server/overview` returns the Borealis Server Info dashboard snapshot used by administrators for Engine host health, Compose service state, public certificate expiry, live operator presence, WireGuard runtime health, and Aegis lock state.
- `POST /api/server/services/<service_key>/restart` remains available for non-container/systemd compatibility paths. Container service operations are handled through `Engine.sh --service ...`.
- `POST /api/server/wireguard/recover` triggers a Borealis-managed WireGuard listener recovery attempt when live tunnels exist.
- Watchdog evaluator activity and remediation dispatch are logged through the `watchdogs` service log domain.

## API Endpoints
- `GET /health` (No Authentication) - liveness probe.
- `GET /api/server/time` (Operator Session) - server time.
- `GET /api/server/overview` (Admin) - aggregated server/admin dashboard snapshot.
- `POST /api/server/services/<service_key>/restart` (Admin) - queue safe detached service restart.
- `POST /api/server/wireguard/recover` (Admin) - recover Borealis WireGuard listener when active sessions exist.
- `GET /api/server/logs` (Admin) - list logs and retention metadata.
- `GET /api/server/logs/<log_name>/entries` (Admin) - tail log entries.
- `PUT /api/server/logs/retention` (Admin) - update retention policies.
- `DELETE /api/server/logs/<log_name>` (Admin) - delete log file(s).

## Related Documentation
- [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
- [Security and Trust](../Engine%20Deployment/security-and-trust.md)
- [API Reference](../Data%20and%20Schema/api-reference.md)
- [Watchdogs](../Automation%20and%20Execution/watchdogs.md)
- [Device Alerts](device-alerts.md)

??? example "Detailed Codex Breakdown"

    ### Engine log formatting
    - Service logs are written via `service_log` in `Data/Engine/Containers/api-backend/data/services/API/__init__.py`.
    - Format: `[YYYY-MM-DD HH:MM:SS] [LEVEL][CONTEXT-<SCOPE>] message`.
    - Context values are derived from agent context headers or message patterns.
    - `Engine.sh` records Docker build output in `Engine/Deploy/build.log`; container runtime logs are written to each service role's `logs/` directory or Docker stdout/stderr.

    ### Log retention implementation
    - `Data/Engine/Containers/api-backend/data/services/API/server/log_management.py` manages retention.
    - Retention overrides are stored in `Engine/Services/api-backend/logs/retention_policy.json`.
    - The API never deletes the active log file automatically.

    ### Operational checks
    - Startup warnings appear in `Engine/Services/api-backend/logs/engine.log`.
    - API access metrics appear in `Engine/Services/api-backend/logs/api.log` (method, path, duration, status).
    - Embedded edge request outcomes appear in `Engine/Services/traefik-edge/logs/traefik-access.log` (frontend path, upstream target, status, latency).
    - VPN-specific logs are under `Engine/Services/api-backend/logs/VPN_Tunnel/` and `Engine/Services/wireguard-tunnel/logs/`.
    - Watchdog evaluator ticks, incident transitions, and remediation dispatch failures are written through the `watchdogs` service log domain.
    - The Server Info admin page is informational first: it surfaces service health, public cert expiry, live operator sessions, and WireGuard listener state. It intentionally does not embed journal tails or recent log snippets.
    - Service restarts initiated from Server Info are systemd-only. In container mode use `Engine.sh --service <role> restart|rebuild|reload|reconcile`.

    ### Agent logging notes
    - Logs are scoped by context (SYSTEM vs CURRENTUSER) in prefixes.
    - Agent runtime, bootstrap, and remote shell logs live under `Logs/Agent/`.
    - Role recovery, watchdog repair, stale-liveness restart, and startup auth retry events live under `Logs/Agent/role_recovery.log`.
    - WireGuard role and installer logs live under `Logs/WireGuard/`.
    - UltraVNC role and installer logs live under `Logs/UltraVNC/`.
    - Cross-platform updater traces are written to `<AgentInstallRoot>/Logs/Agent/bootstrap.log`; Windows bootstrap starts by truncating this file and keeps verbose output out of operator-facing stdout/stderr streams.

    ### Debug workflow
    - Start with the log file closest to the symptom.
    - Use API log lines to confirm the request reached the Engine.
    - Use `traefik-access.log` to confirm whether the embedded edge returned a `502` before the Engine loopback runtime was ready.
    - Use service logs to diagnose domain-specific behavior.
    - If troubleshooting WireGuard, inspect both Engine and Agent VPN logs.
    - If troubleshooting watchdogs, compare `watchdogs.log`, device inventory freshness, and the current `watchdog_device_state` / `watchdog_incidents` rows.

    ### Operational safety
    - Do not delete logs by hand while debugging; use the log API or archive first.
    - Keep runtime artifacts inside `Engine/Services/<role>/`, `Engine/Deploy/`, and `Agent/` to preserve boundaries, except for the intentionally shared updater trace at `<ProjectRoot>/Updater.log`.
    - If you change log formats, update this document and `engine-runtime.md`.
