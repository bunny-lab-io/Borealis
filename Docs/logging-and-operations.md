# Logging and Operations
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Describe Borealis operational logging, retention, and core runtime checks.

## Log Locations
- Engine primary log: `Engine/Logs/engine.log` (daily rotation).
- Engine error log: `Engine/Logs/error.log`.
- Engine API access log: `Engine/Logs/api.log`.
- Service logs: `Engine/Logs/<service>.log` (per-domain).
- VPN logs: `Engine/Logs/VPN_Tunnel/tunnel.log` and `Engine/Logs/VPN_Tunnel/remote_shell.log`.
- Agent logs: `Agent/Logs/agent.log` and `Agent/Logs/agent.error.log` (daily rotation).

## Log Retention
- Retention is managed via `/api/server/logs` endpoints.
- Retention overrides are stored in `Engine/Logs/retention_policy.json`.

## Operational Health
- `GET /health` returns liveness status.
- `GET /api/server/time` returns server clock information after login.

## API Endpoints
- `GET /health` (No Authentication) - liveness probe.
- `GET /api/server/time` (Operator Session) - server time.
- `GET /api/server/logs` (Admin) - list logs and retention metadata.
- `GET /api/server/logs/<log_name>/entries` (Admin) - tail log entries.
- `PUT /api/server/logs/retention` (Admin) - update retention policies.
- `DELETE /api/server/logs/<log_name>` (Admin) - delete log file(s).

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [Security and Trust](security-and-trust.md)
- [API Reference](api-reference.md)

## Codex Agent (Detailed)
### Engine log formatting
- Service logs are written via `service_log` in `Data/Engine/services/API/__init__.py`.
- Format: `[YYYY-MM-DD HH:MM:SS] [LEVEL][CONTEXT-<SCOPE>] message`.
- Context values are derived from agent context headers or message patterns.

### Log retention implementation
- `Data/Engine/services/API/server/log_management.py` manages retention.
- Retention overrides are stored in `Engine/Logs/retention_policy.json`.
- The API never deletes the active log file automatically.

### Operational checks
- Startup warnings appear in `Engine/Logs/engine.log`.
- API access metrics appear in `Engine/Logs/api.log` (method, path, duration, status).
- VPN-specific logs are under `Engine/Logs/VPN_Tunnel/`.

### Agent logging notes
- Logs are scoped by context (SYSTEM vs CURRENTUSER) in prefixes.
- Role-specific logs live under `Agent/Logs/<service>.log`.
- VPN logs are kept in `Agent/Logs/VPN_Tunnel/`.

### Debug workflow
- Start with the log file closest to the symptom.
- Use API log lines to confirm the request reached the Engine.
- Use service logs to diagnose domain-specific behavior.
- If troubleshooting WireGuard, inspect both Engine and Agent VPN logs.

### Operational safety
- Do not delete logs by hand while debugging; use the log API or archive first.
- Keep runtime artifacts inside `Engine/` and `Agent/` to preserve boundaries.
- If you change log formats, update this document and `engine-runtime.md`.
