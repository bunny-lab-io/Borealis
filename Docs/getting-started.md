# Getting Started with Borealis
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Help operators install, launch, and verify the Borealis Engine and (optionally) the Agent.

## Quick Start (Engine)
- Linux production, first install: `./bootstrap.sh -Engine` or `./bootstrap.sh --EngineProduction` (installs Docker Engine + Docker Compose, then deploys the Compose-backed Engine at `https://<your-public-fqdn>` through the Traefik edge container).
- Linux dev, first install: `./bootstrap.sh --EngineDev` (same Compose stack, with Vite HMR on loopback `127.0.0.1:5173` behind Traefik).
- Linux stable-channel install: `./bootstrap.sh --release-channel stable --EngineProduction`.
- Linux branch testing install: `./bootstrap.sh --repo-branch optimization/agent-context-socket-consolidation --EngineProduction`.
- Linux production, local redeploy after bootstrap: `./Engine.sh deploy prod` or `./Borealis.sh --EngineProduction`.
- Linux dev, local redeploy after bootstrap: `./Engine.sh deploy dev` or `./Borealis.sh --EngineDev`.
- Engine service action examples: `./Engine.sh --service api-backend restart`, `./Engine.sh --service webui-frontend rebuild dev`, `./Engine.sh --service traefik-edge reload`, and `./Engine.sh --service wireguard-tunnel reconcile`.
- Updates: `./Update.sh -Engine` fast-forwards the current branch then runs `Engine.sh deploy`; `./Update.sh -Agent` preserves the Agent updater flow then runs `Agent.sh deploy`.
- Add `--verbose` (or `BOREALIS_VERBOSE=1`) to stream the underlying package-manager, installer, and service output instead of the default quieter step view.
- Production TLS is managed by the embedded Traefik edge; the Python Engine stays on loopback HTTP.
- During Engine deployment, `Engine.sh` renders `Engine/Deploy/compose.env`, builds changed local images as `borealis-engine/<service>:sha-<hash>`, and starts the Compose project `borealis-engine`.
- Storage is displayed during profile detection as an advisory guideline only. It does not change which Engine profile gets selected.

## Optional: Install the Agent (Windows)
- Run in elevated PowerShell: `./Borealis.ps1`.
- Bootstrap one-liner:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --agent`
- Bootstrap one-liner pinned to the latest stable release tag:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --release-channel stable --agent`
- Bootstrap one-liner pinned to a testing branch:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/optimization/agent-context-socket-consolidation/bootstrap.ps1"))) --repo-branch optimization/agent-context-socket-consolidation --agent`
- When testing bootstrap-only changes that have not merged to `main` yet, fetch `bootstrap.ps1` from the same branch or commit you want to validate; otherwise `main` may download an older bootstrapper that does not understand the new flags even if the target branch does.
- Automated enrollment example:
  `./Borealis.ps1 -EnrollmentCode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Non-interactive server URL + enrollment example:
  `./Borealis.ps1 -ServerUrl "https://borealis.example.com" -EnrollmentCode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Bootstrap + server URL + enrollment example:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1"))) --agent --serverurl "https://borealis.example.com" --enrollmentcode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Branch bootstrap + server URL + enrollment example:
  `& ([ScriptBlock]::Create((Invoke-RestMethod "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/optimization/agent-context-socket-consolidation/bootstrap.ps1"))) --agent --repo-branch optimization/agent-context-socket-consolidation --serverurl "https://borealis.example.com" --enrollmentcode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"`
- Linux agents run from the same script-staged Python runtime model as the rest of Borealis, not shipped binaries. The Linux Agent path can be installed with `./Borealis.sh --Agent`; current parity notes live in `agent-runtime.md`.

## First Run Checklist
- Open the Engine URL and confirm the login page loads.
- Check `Engine/Services/api-backend/logs/engine.log` for startup messages. `Engine/Logs` is kept as a compatibility link when the container runtime creates a fresh `Engine/` tree.
- Verify liveness: `GET /health` returns `{"status":"ok"}`.

## Reverse Proxy Notes
- Borealis expects HTTPS for production use.
- Borealis owns its public TLS edge with the `traefik-edge` container and Let's Encrypt when a public FQDN plus ACME email are configured.
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
- Engine container source lives in `Data/Engine/Containers/`; generated runtime state lives under `Engine/Deploy/`, `Engine/Services/<role>/`, and `Engine/Shared/`.
- Always edit source under `Data/` and re-run the appropriate launcher to apply changes: `bootstrap.sh` for first-run Linux provisioning, `Engine.sh` for normal Engine redeploys, `Agent.sh` for Linux Agent redeploys, and `Borealis.ps1` / `bootstrap.ps1` for the Windows agent.

### Launch mechanics
- `bootstrap.sh` is the Linux first-run path that syncs the repo and opts into missing OS package installation before handing off to `Engine.sh` or `Agent.sh`; it accepts `--release-channel stable|unstable` plus `--repo-branch` / `--ref` for targeted deploys.
- `Engine.sh deploy` defaults to production and runs Docker Compose with project name `borealis-engine`.
- `Engine.sh deploy dev` runs the same service set but sets the WebUI frontend to Vite HMR behind Traefik.
- `Agent.sh deploy` stages the Linux Agent from existing local source and never updates git.
- `Borealis.sh` is now a compatibility router for legacy commands and service shortcuts.
- `bootstrap.ps1` and `Borealis.ps1` handle dependency setup and staging for the Windows agent runtime, and `bootstrap.ps1` now accepts the same release-channel and branch-selection bootstrap options as the Linux bootstrapper.
- When validating new bootstrap-only behavior before it merges to `main`, download `bootstrap.ps1` from the same branch or commit you intend to test; using the `main` bootstrapper with branch-only flags can fail before the repo sync step has a chance to pull the newer code.
- Dev mode (`--EngineDev`) uses Vite for the WebUI behind the Traefik edge container, while the Engine API stays on loopback.
- Production (`--EngineProduction`) runs the Engine API on loopback HTTP, serves the static WebUI from the WebUI frontend container, and publishes the app through Traefik.
- `Borealis.sh` now defaults to a compact step-oriented console view. Detailed bootstrap subprocess output is captured in `Engine/Logs/install.log` or `Agent/Logs/install.log` and is surfaced inline only on failures unless `--verbose` is enabled.
- `Engine/Deploy/image-manifest.json` records image hashes and tags. `Engine/Deploy/deploy-manifest.json` records mode, Compose file, env file, and service set.

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
