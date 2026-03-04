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

## Runtime Paths
- Source code: `Data/Engine/` (edit here).
- Runtime copy: `Engine/` (regenerated each launch).
- Database: `Engine/database.db` (default; configurable).
- Logs: `Engine/Logs/` (engine.log, error.log, api.log, service logs).
- Certificates: `Engine/Certificates/` (TLS bundle + code signing keys).
- WebUI build output: `Engine/web-interface/` (served as static assets).

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

### API groups and adapters
- Default groups live in `Data/Engine/services/API/__init__.py` (`DEFAULT_API_GROUPS`).
- Each group has a registrar in `_GROUP_REGISTRARS`.
- `EngineServiceAdapters` exposes:
  - `db_conn_factory` (SQLite with WAL and busy_timeout).
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

### WireGuard and VNC wiring
- WireGuard server manager: `Data/Engine/services/VPN/wireguard_server.py`.
- Tunnel orchestration: `Data/Engine/services/VPN/vpn_tunnel_service.py`.
- VNC proxy: `Data/Engine/services/RemoteDesktop/vnc_proxy.py`.
- API entrypoints: `/api/vnc/establish`, `/api/vnc/disconnect`, `/api/shell/establish`, `/api/shell/disconnect`.
- Persistent tunnels are established by agents via `POST /api/agent/vpn/ensure` and kept online.

### Assembly runtime
- Assembly cache is initialized in `Data/Engine/assembly_management` and attached to `context.assembly_cache`.
- Quick jobs and scheduled jobs share this runtime to resolve scripts and variables.

### Platform parity
- Engine deployment is Linux-only via `Borealis.sh`.
- Linux agent remains incomplete.

### Borealis Engine Codex (Full)
Use this section for Engine work (successor to the legacy server). Shared guidance is consolidated in `ui-and-notifications.md` and other knowledgebase pages.

#### Scope and runtime paths
- Bootstrap: `Borealis.sh` handles Engine staging and launch on Linux. (`Borealis.ps1` is agent-only.)
- Edit in `Data/Engine`; runtime copies live under `/Engine` and are discarded every time the engine is launched.

#### Architecture
- Runtime: `Data/Engine/server.py` with NodeJS + Vite for live dev and Flask for production serving/API endpoints.

#### Development guidelines
- Every Python module under `Data/Engine` or `Engine/Data/Engine` starts with the standard commentary header (purpose + API endpoints). Add the header to any existing module before further edits.

#### Logging
- Primary log: `Engine/Logs/engine.log` with daily rotation (`engine.log.YYYY-MM-DD`); do not auto-delete rotated files.
- Subsystems: `Engine/Logs/<service>.log`; install output to `Engine/Logs/install.log`.
- Keep Engine-specific artifacts within `Engine/Logs/` to preserve the runtime boundary.

#### Security and API parity
- Mirrors legacy mutual trust: Ed25519 device identities, EdDSA-signed access tokens, pinned Borealis root CA, TLS 1.3-only serving, Authorization headers and service-context markers on every device API.
- Implements DPoP validation, short-lived access tokens (about 15 min), SHA-256 hashed refresh tokens (30-day) with explicit reuse errors.
- Enrollment: operator approvals, conflict detection, auditor recording, pruning of expired codes/refresh tokens.
- Background jobs and service adapters maintain compatibility with legacy DB schemas while enabling gradual API takeover.

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
- Mirrors the agent's unfinished story: treat orchestration as experimental until packaging, connection management, and logging mature.
