# Getting Started with Borealis
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Help operators install, launch, and verify the Borealis Engine and (optionally) the Agent.

## Quick Start (Engine)
- Linux production, first install from a cloned checkout: `./Engine.sh deploy prod` (installs Docker Engine + Docker Compose when missing, then deploys the Compose-backed Engine at `https://<your-public-fqdn>` through the Traefik edge container).
- Linux production, one-line install: `curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- deploy prod`.
- Linux dev, first install: `./Engine.sh deploy dev` (same Compose stack, with Vite HMR on loopback `127.0.0.1:5173` behind Traefik).
- Linux stable-channel install: `curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --release-channel stable deploy prod`.
- Linux branch testing install: `curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --repo-branch optimization/agent-context-socket-consolidation deploy prod`.
- Linux production, local redeploy: `./Engine.sh deploy prod` or `./Borealis.sh --EngineProduction`.
- Linux dev, local redeploy: `./Engine.sh deploy dev` or `./Borealis.sh --EngineDev`.
- Engine service action examples: `./Engine.sh --service api-backend restart`, `./Engine.sh --service webui-frontend rebuild dev`, `./Engine.sh --service traefik-edge reload`, and `./Engine.sh --service wireguard-tunnel reconcile`.
- Updates: `./Update.sh -Engine` fast-forwards the current branch then runs `Engine.sh deploy`; `./Update.sh -Agent` preserves the Agent updater flow then runs `Agent.sh deploy`.
- Production TLS is managed by the embedded Traefik edge; the Python Engine stays on loopback HTTP.
- During Engine deployment, `Engine.sh` renders `Engine/Deploy/compose.env`, builds changed local images as `borealis-engine/<service>:sha-<hash>`, and starts or updates the Compose project `borealis-engine`.
- No-op Engine deploys skip unchanged image builds and skip Compose when the deploy manifest, env, image hashes, and running containers already match.
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
- Linux agents run from the same script-staged Python runtime model as the rest of Borealis, not shipped binaries. The Linux Agent path can be installed with `./Agent.sh deploy`; current parity notes live in `Docs/Core Runtimes/agent-runtime.md`.
- Linux Agent branch bootstrap example:
  `curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/feature/containerize-all-borealis-services/Agent.sh | sudo bash -s -- --repo-branch feature/containerize-all-borealis-services deploy --serverurl "https://borealis.bunny-lab.io" --enrollmentcode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983" --newEngine`

## First Run Checklist
- Open the Engine URL and confirm the login page loads.
- Check `Engine/Services/api-backend/logs/engine.log` for startup messages.
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
- [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
- [Docker Stack Breakdown](../Core%20Runtimes/Stack_Breakdown.md)
- [Agent Runtime](../Core%20Runtimes/agent-runtime.md)
- [Security and Trust](security-and-trust.md)
- [Logging and Operations](../Operations%20and%20Remote%20Access/logging-and-operations.md)

## Codex Agent (Detailed)
### Bootstrap and runtime separation
- Engine API/backend source lives in `Data/Engine/Containers/api-backend/data/`.
- Engine WebUI source lives in `Data/Engine/Containers/webui-frontend/data/web-interface/`.
- Engine WebUI dev/HMR runtime source lives in `Engine/Services/webui-frontend/data/web-interface/` after first Engine deploy.
- Agent source code lives in `Data/Agent/`.
- Runtime copies are staged to `Engine/` and `Agent/` every launch; these are disposable.
- Engine container source lives in `Data/Engine/Containers/`; generated runtime state lives under `Engine/Deploy/` and sparse service-owned folders under `Engine/Services/<role>/`.
- Edit durable source under `Data/` and re-run the appropriate launcher to apply changes: `Engine.sh` for Linux Engine first install and redeploys, `Agent.sh` for Linux Agent first install and redeploys, and `Borealis.ps1` / `bootstrap.ps1` for the Windows agent. For rapid WebUI HMR testing, edit `Engine/Services/webui-frontend/data/web-interface/` while running `Engine.sh deploy dev`.

### Launch mechanics
- `Engine.sh` is the Linux Engine first-run and redeploy path. When run from a raw one-liner or with repo options, it syncs source first; local `Engine.sh deploy` uses existing on-disk source.
- `Engine.sh deploy` installs missing Engine OS dependencies, defaults to production, and runs Docker Compose with project name `borealis-engine`.
- `Engine.sh deploy dev` runs the same service set but sets the WebUI frontend to Vite HMR behind Traefik.
- `Agent.sh` is the Linux Agent first-run and redeploy path. When run from a raw one-liner or with repo options, it syncs source first; local `Agent.sh deploy` uses existing on-disk source.
- `Agent.sh deploy` installs missing Agent OS dependencies and stages the Linux Agent runtime.
- `Borealis.sh` is now a compatibility router for legacy commands and service shortcuts.
- `bootstrap.ps1` and `Borealis.ps1` handle dependency setup and staging for the Windows agent runtime, and `bootstrap.ps1` accepts release-channel and branch-selection options for Windows Agent installs.
- When validating new bootstrap-only behavior before it merges to `main`, download `bootstrap.ps1` from the same branch or commit you intend to test; using the `main` bootstrapper with branch-only flags can fail before the repo sync step has a chance to pull the newer code.
- Dev mode (`--EngineDev`) uses Vite for the WebUI behind the Traefik edge container, while the Engine API stays on loopback.
- Production (`--EngineProduction`) runs the Engine API on loopback HTTP, serves the static WebUI from the WebUI frontend container, and publishes the app through Traefik.
- `Borealis.sh` remains a compatibility router only; Engine and Agent dependency checks now live in their domain launchers.
- `Engine/Deploy/image-manifest.json` records image hashes and tags. `Engine/Deploy/deploy-manifest.json` records mode, Compose/env hashes, service image hashes, changed services, and whether Compose ran or was skipped.

### Configuration precedence
- Engine config is assembled by `Data/Engine/Containers/api-backend/data/config.py` in this order:
  1) Explicit overrides passed to the app factory.
  2) Environment variables prefixed with `BOREALIS_`.
  3) Defaults baked into `config.py`.
- Key defaults to remember:
  - Database: `BOREALIS_DATABASE_URL` (required PostgreSQL connection URL)
  - Bundled official assemblies: `Data/Engine/Containers/api-backend/data/Official_Assemblies/` (generated seed snapshot)
  - Aurora checkout: `Engine/Services/api-backend/cache/Aurora/`
  - Logs: `Engine/Services/api-backend/logs/engine.log`, `Engine/Services/api-backend/logs/error.log`, `Engine/Services/api-backend/logs/api.log`
  - WireGuard: UDP 30000, engine virtual IP `10.255.0.1/32`, shell port 47002

### Public edge and trust
- Borealis embedded Traefik manages the public HTTPS identity and ACME state under `Engine/Services/traefik-edge/state/` and `Engine/Services/traefik-edge/config/`.
- Agents must use the public HTTPS FQDN and rely on normal CA + hostname validation.
- The Python Engine is not a direct public TLS endpoint in production.

### Agent install and enrollment notes
- The Windows agent must run elevated to create services and scheduled tasks.
- Enrollment requires an install code and operator approval (see `Docs/Operations and Remote Access/device-management.md`).
- If enrollment fails, inspect `Agent/Logs/agent.log` and `Engine/Services/api-backend/logs/engine.log`.

### Health verification
- Use `GET /health` to confirm the API is alive.
- Use `GET /api/server/time` after login to verify session auth and API reachability.
- Confirm WebSockets by opening the UI and checking that toasts and live updates work.
