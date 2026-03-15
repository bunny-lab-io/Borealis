# Getting Started with Borealis
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Help operators install, launch, and verify the Borealis Engine and (optionally) the Agent.

## Quick Start (Engine)
- Linux production: `./Borealis.sh --EngineProduction` (Engine UI at `https://localhost:5000`).
- Linux dev: `./Borealis.sh --EngineDev` (Vite + Flask at `https://localhost:5173`).
- TLS is auto-provisioned under `Engine/Certificates` on first launch.

## Optional: Install the Agent (Windows)
- Run in elevated PowerShell: `./Borealis.ps1`.
- Bootstrap one-liner:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --agent`
- Automated enrollment example:
  `./Borealis.ps1 -EnrollmentCode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Non-interactive server URL + enrollment example:
  `./Borealis.ps1 -ServerUrl "https://localhost:5000" -EnrollmentCode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Bootstrap + server URL + enrollment example:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --agent --serverurl "https://localhost:5000" --enrollmentcode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Linux agent binaries are not available; `Borealis.sh --Agent` only stages settings.

## First Run Checklist
- Open the Engine URL and confirm the login page loads.
- Check `Engine/Logs/engine.log` for startup messages.
- Verify liveness: `GET /health` returns `{"status":"ok"}`.

## Reverse Proxy Notes
- Borealis expects HTTPS for production use.
- If you reverse proxy, preserve `Upgrade` headers for WebSockets and allow `/socket.io` paths.
- A Traefik example is included in `README.md`.

## API Endpoints
- `GET /health` (No Authentication) - Engine liveness probe.
- `GET /api/server/time` (Operator Session) - Quick sanity check after login.

## Related Documentation
- [Architecture Overview](architecture-overview.md)
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)
- [Security and Trust](security-and-trust.md)
- [Logging and Operations](logging-and-operations.md)

## Codex Agent (Detailed)
### Bootstrap and runtime separation
- The authoritative source code lives in `Data/Engine/` and `Data/Agent/`.
- Runtime copies are staged to `Engine/` and `Agent/` every launch; these are disposable.
- Always edit source under `Data/` and re-run the bootstrap scripts to apply changes.

### Launch mechanics
- `Borealis.sh` handles dependency setup, venv activation, and staging for the Linux Engine runtime.
- `Borealis.ps1` handles dependency setup and staging for the Windows agent runtime.
- Dev mode (`--EngineDev`) uses Vite for the WebUI and Flask for APIs.
- Production (`--EngineProduction`) serves the built SPA through Flask.

### Configuration precedence
- Engine config is assembled by `Data/Engine/config.py` in this order:
  1) Explicit overrides passed to the app factory.
  2) Environment variables prefixed with `BOREALIS_`.
  3) Defaults baked into `config.py`.
- Key defaults to remember:
  - Database: `BOREALIS_DATABASE_URL` (required PostgreSQL connection URL)
  - Bundled official assemblies: `Data/Engine/Official_Assemblies/`
  - Logs: `Engine/Logs/engine.log`, `Engine/Logs/error.log`, `Engine/Logs/api.log`
  - WireGuard: UDP 30000, engine virtual IP `10.255.0.1/32`, shell port 47002

### TLS and certificates
- Engine generates an ECDSA root + leaf chain on first boot.
- TLS bundle lives under `Engine/Certificates` and is pinned by agents.
- If you override TLS paths, update `BOREALIS_TLS_*` env vars and restart.

### Agent install and enrollment notes
- The Windows agent must run elevated to create services and scheduled tasks.
- Enrollment requires an install code and operator approval (see `device-management.md`).
- If enrollment fails, inspect `Agent/Logs/agent.log` and `Engine/Logs/engine.log`.

### Health verification
- Use `GET /health` to confirm the API is alive.
- Use `GET /api/server/time` after login to verify session auth and API reachability.
- Confirm WebSockets by opening the UI and checking that toasts and live updates work.
