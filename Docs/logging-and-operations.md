# Logging and Operations
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Describe Borealis operational logging, retention, and core runtime checks.

## Log Locations
- Engine bootstrap/install log: `Engine/Logs/install.log` (launcher dependency/install output captured by `Borealis.sh`).
- Engine primary log: `Engine/Logs/engine.log` (daily rotation).
- Engine error log: `Engine/Logs/error.log`.
- Engine API access log: `Engine/Logs/api.log`.
- Engine Traefik log: `Engine/Logs/traefik.log`.
- Engine Traefik access log: `Engine/Logs/traefik-access.log`.
- Service logs: `Engine/Logs/<service>.log` (per-domain).
- Watchdog service log: `Engine/Logs/watchdogs.log`.
- VPN logs: `Engine/Logs/VPN_Tunnel/tunnel.log` and `Engine/Logs/VPN_Tunnel/remote_shell.log`.
- Agent bootstrap/install log: `Agent/Logs/install.log` (launcher dependency/install output captured by `Borealis.sh`).
- Agent logs: `Agent/Logs/agent.log` and `Agent/Logs/agent.error.log` (daily rotation).
- Updater diagnostics: `<ProjectRoot>/Updater.log` (shared log recreated by `Update.ps1` and `Update.sh` at the start of each update run so it only contains the latest session).

## Log Retention
- Retention is managed via `/api/server/logs` endpoints.
- Retention overrides are stored in `Engine/Logs/retention_policy.json`.

## Operational Health
- `GET /health` returns liveness status.
- `GET /api/server/time` returns server clock information after login.
- `GET /api/server/overview` returns the Borealis Server Info dashboard snapshot used by administrators for Engine host health, systemd service state, public certificate expiry, live operator presence, WireGuard runtime health, and Aegis lock state.
- `POST /api/server/services/<service_key>/restart` queues safe detached service restarts through `systemd-run` so the request can return before an Engine self-restart interrupts the caller.
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
- [Engine Runtime](engine-runtime.md)
- [Security and Trust](security-and-trust.md)
- [API Reference](api-reference.md)
- [Watchdogs](watchdogs.md)
- [Device Alerts](device-alerts.md)

## Codex Agent (Detailed)
### Engine log formatting
- Service logs are written via `service_log` in `Data/Engine/services/API/__init__.py`.
- Format: `[YYYY-MM-DD HH:MM:SS] [LEVEL][CONTEXT-<SCOPE>] message`.
- Context values are derived from agent context headers or message patterns.
- `Borealis.sh` keeps the terminal launch view intentionally compact by default and redirects package-manager, pip, ansible-galaxy, and similar bootstrap command output into the runtime `install.log` files unless `--verbose` or `BOREALIS_VERBOSE=1` is used.

### Log retention implementation
- `Data/Engine/services/API/server/log_management.py` manages retention.
- Retention overrides are stored in `Engine/Logs/retention_policy.json`.
- The API never deletes the active log file automatically.

### Operational checks
- Startup warnings appear in `Engine/Logs/engine.log`.
- API access metrics appear in `Engine/Logs/api.log` (method, path, duration, status).
- Embedded edge request outcomes appear in `Engine/Logs/traefik-access.log` (frontend path, upstream target, status, latency).
- VPN-specific logs are under `Engine/Logs/VPN_Tunnel/`.
- Watchdog evaluator ticks, incident transitions, and remediation dispatch failures are written through the `watchdogs` service log domain.
- The Server Info admin page is informational first: it surfaces service health, public cert expiry, live operator sessions, and WireGuard listener state. It intentionally does not embed journal tails or recent log snippets.
- Service restarts initiated from Server Info are queued through transient `systemd-run` units with a short delay instead of direct in-process `systemctl restart`, reducing the risk of cutting off the initiating request during Engine self-restarts.

### Agent logging notes
- Logs are scoped by context (SYSTEM vs CURRENTUSER) in prefixes.
- Role-specific logs live under `Agent/Logs/<service>.log`.
- VPN logs are kept in `Agent/Logs/VPN_Tunnel/`.
- Cross-platform updater traces are written to `<ProjectRoot>/Updater.log`, which is reset at the start of each run so operators can inspect the latest update failure from one file.

### Debug workflow
- Start with the log file closest to the symptom.
- Use API log lines to confirm the request reached the Engine.
- Use `traefik-access.log` to confirm whether the embedded edge returned a `502` before the Engine loopback runtime was ready.
- Use service logs to diagnose domain-specific behavior.
- If troubleshooting WireGuard, inspect both Engine and Agent VPN logs.
- If troubleshooting watchdogs, compare `watchdogs.log`, device inventory freshness, and the current `watchdog_device_state` / `watchdog_incidents` rows.

### Operational safety
- Do not delete logs by hand while debugging; use the log API or archive first.
- Keep runtime artifacts inside `Engine/` and `Agent/` to preserve boundaries, except for the intentionally shared updater trace at `<ProjectRoot>/Updater.log`.
- If you change log formats, update this document and `engine-runtime.md`.
