# Codex Guide: Borealis Engine

Use this doc for Engine work (successor to the legacy server). For shared guidance, see `Docs/Codex/SHARED.md`.

## Scope & Runtime Paths
- Role: actively developed replacement for `Data/Server/server.py` covering Python services, REST APIs, WebSockets, and Flask/Vite frontends.
- Bootstrap: `Borealis.ps1` launches the Engine by default while keeping the legacy server switch for regressions.
- Edit in `Data/Engine`; runtime copies live under `/Engine` and are discarded. `/Server` remains untouched unless explicitly running the legacy path.

## Migration & Architecture
- Migration tracker: `Engine/Data/Engine/CODE_MIGRATION_TRACKER.md` (Stages 1–5 complete; Stage 6 WebUI migration in progress; Stage 7 WebSocket migration queued).
- Runtime: `Data/Engine/server.py` with NodeJS + Vite for live dev and Flask for production serving/API endpoints.

## Development Guidelines
- Every new Python module under `Data/Engine` or `Engine/Data/Engine` starts with the standard commentary header (purpose + API endpoints). Add the header to any existing module before further edits.
- Check the migration tracker to avoid jumping ahead of approved stages.

## Logging
- Primary log: `Engine/Logs/engine.log` with daily rotation (`engine.log.YYYY-MM-DD`); do not auto-delete rotated files.
- Subsystems: `Engine/Logs/<service>.log`; install output to `Engine/Logs/install.log`.
- Keep Engine-specific artifacts within `Engine/Logs/` to preserve the runtime boundary.

## Security & API Parity
- Mirrors legacy mutual trust: Ed25519 device identities, EdDSA-signed access tokens, pinned Borealis root CA, TLS 1.3-only serving, Authorization headers + service-context markers on every device API.
- Implements DPoP validation, short-lived access tokens (~15 min), SHA-256–hashed refresh tokens (30-day) with explicit reuse errors.
- Enrollment: operator approvals, conflict detection, auditor recording, pruning of expired codes/refresh tokens.
- Background jobs and service adapters maintain compatibility with legacy DB schemas while enabling gradual API takeover.

## WebUI & WebSocket Migration
- Static/template handling: `Data/Engine/services/WebUI`; deployment copy paths are wired through `Borealis.ps1` with TLS-aware URL generation.
- Stage 6 tasks: migration switch in the legacy server for WebUI delegation and porting device/admin API endpoints into Engine services.
- Stage 7 (queued): `register_realtime` hooks, Engine-side Socket.IO handlers, integration checks, legacy delegation updates.

## Platform Parity
- Windows is primary target. Keep Engine tooling aligned with the agent experience; Linux packaging must catch up before macOS work resumes.

## Ansible Support (Shared State)
- Mirrors the agent’s unfinished story: treat orchestration as experimental until packaging, connection management, and logging mature.
