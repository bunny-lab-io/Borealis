# Getting Started with Borealis
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Help operators install, launch, and verify the Borealis Engine and (optionally) the Agent.

## Quick Start (Engine)
- Linux production, first install: `./bootstrap.sh --EngineProduction` (public UI at `https://<your-public-fqdn>` through the embedded Traefik + Let's Encrypt edge).
- Linux dev, first install: `./bootstrap.sh --EngineDev` (public UI on `https://<your-public-fqdn>` through the embedded Traefik edge, with Vite HMR running on loopback `127.0.0.1:5173` behind Traefik).
- Linux production, local redeploy after bootstrap: `./Borealis.sh --EngineProduction`.
- Linux dev, local redeploy after bootstrap: `./Borealis.sh --EngineDev`.
- Add `--verbose` (or `BOREALIS_VERBOSE=1`) to stream the underlying package-manager, installer, and service output instead of the default quieter step view.
- Production TLS is managed by the embedded Traefik edge; the Python Engine stays on loopback HTTP.
- During Engine deployment, `Borealis.sh` profiles the host CPU and RAM, prints the detected Engine profile in the CLI, and auto-configures PostgreSQL plus Engine DB pool settings from that profile.
- Storage is displayed during profile detection as an advisory guideline only. It does not change which Engine profile gets selected.

## Optional: Install the Agent (Windows)
- Run in elevated PowerShell: `./Borealis.ps1`.
- Bootstrap one-liner:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --agent`
- Automated enrollment example:
  `./Borealis.ps1 -EnrollmentCode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Non-interactive server URL + enrollment example:
  `./Borealis.ps1 -ServerUrl "https://borealis.example.com" -EnrollmentCode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Bootstrap + server URL + enrollment example:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --agent --serverurl "https://borealis.example.com" --enrollmentcode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Linux agent binaries are not available; `Borealis.sh --Agent` only stages settings.

## First Run Checklist
- Open the Engine URL and confirm the login page loads.
- Check `Engine/Logs/engine.log` for startup messages.
- Verify liveness: `GET /health` returns `{"status":"ok"}`.

## Reverse Proxy Notes
- Borealis expects HTTPS for production use.
- Borealis owns its public TLS edge with the embedded Traefik runtime and Let's Encrypt.
- Keep WireGuard separate from the HTTPS edge; it remains direct UDP on port `30000`.

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
- Always edit source under `Data/` and re-run the appropriate launcher to apply changes: `bootstrap.sh` for first-run Linux provisioning, `Borealis.sh` for normal Linux redeploys, and `Borealis.ps1` / `bootstrap.ps1` for the Windows agent.

### Launch mechanics
- `bootstrap.sh` is the Linux first-run path that syncs the repo and opts into missing OS package installation before handing off to `Borealis.sh`.
- `Borealis.sh` now focuses on runtime verification, venv activation, and staging for the Linux Engine runtime during normal redeploys; it no longer performs package-manager checks on every run unless bootstrap explicitly opts in.
- `Borealis.ps1` handles dependency setup and staging for the Windows agent runtime.
- Dev mode (`--EngineDev`) uses Vite for the WebUI behind the embedded Traefik edge, while the Engine API stays on loopback.
- Production (`--EngineProduction`) runs the Engine on loopback HTTP and publishes the app through the embedded Traefik edge.
- `Borealis.sh` now defaults to a compact step-oriented console view. Detailed bootstrap subprocess output is captured in `Engine/Logs/install.log` or `Agent/Logs/install.log` and is surfaced inline only on failures unless `--verbose` is enabled.
- Engine deployment auto-detects a CPU/RAM-based profile (`Homelab`, `Small Business`, `MSP / Production`, or `Enterprise`) and persists the resulting PostgreSQL and Engine DB tuning into `Engine/database.env`.
- Re-running `Borealis.sh` on a resized host re-evaluates the profile and prints the previous/new profile state in the CLI so operators can see when the deployment has scaled up.

### Configuration precedence
- Engine config is assembled by `Data/Engine/config.py` in this order:
  1) Explicit overrides passed to the app factory.
  2) Environment variables prefixed with `BOREALIS_`.
  3) Defaults baked into `config.py`.
- Key defaults to remember:
  - Database: `BOREALIS_DATABASE_URL` (required PostgreSQL connection URL)
  - Bundled official assemblies: `Data/Engine/Official_Assemblies/` (generated seed snapshot)
  - Aurora checkout: `Engine/Aurora/`
  - Logs: `Engine/Logs/engine.log`, `Engine/Logs/error.log`, `Engine/Logs/api.log`
  - WireGuard: UDP 30000, engine virtual IP `10.255.0.1/32`, shell port 47002

### Public edge and trust
- Borealis embedded Traefik manages the public HTTPS identity and ACME state under `Engine/LetsEncrypt/` and `Engine/Traefik/`.
- Agents must use the public HTTPS FQDN and rely on normal CA + hostname validation.
- The Python Engine is not a direct public TLS endpoint in production.

### Agent install and enrollment notes
- The Windows agent must run elevated to create services and scheduled tasks.
- Enrollment requires an install code and operator approval (see `device-management.md`).
- If enrollment fails, inspect `Agent/Logs/agent.log` and `Engine/Logs/engine.log`.

### Health verification
- Use `GET /health` to confirm the API is alive.
- Use `GET /api/server/time` after login to verify session auth and API reachability.
- Confirm WebSockets by opening the UI and checking that toasts and live updates work.
