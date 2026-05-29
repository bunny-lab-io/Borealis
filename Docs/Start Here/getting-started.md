# Getting Started with Borealis

## Purpose

Use this page to install the Borealis Engine, open the web interface, and verify the first startup. Agent installation is optional and can happen after the Engine is running.

!!! tip "Beginner path"
    Start with the Engine only. Add Agents after the login page works and `GET /health` returns `{"status":"ok"}`.

## Before You Start

- Use a Linux server for the Engine. Ubuntu Server 24.04 LTS or newer is the preferred baseline.
- Point a public FQDN at the Engine host before production deployment.
- Have an email address ready for Let's Encrypt certificate registration.
- Run install commands from a shell with `sudo` rights.
- Keep WireGuard UDP `30000` reachable for remote operations.

!!! warning "Engine host boundary"
    Do not install a Linux Agent into the same runtime root used by a deployed Engine. Keep Agent files in a dedicated Agent install directory.

## Install the Engine

Use the one-line installer when starting from a fresh Linux host:

```sh
# Download the Borealis launcher from GitHub and deploy the production Engine stack.
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- deploy prod
```

Use a cloned checkout when you already have the repository on disk:

```sh
# Run production deployment from the repository root.
./Engine.sh deploy prod
```

During deployment, Borealis installs missing Docker dependencies, prepares runtime configuration, builds changed service images, and starts the Docker Compose project.

??? note "Optional: development and branch installs"
    Use these commands only when testing changes or validating a specific release channel.

    ```sh
    # Deploy the development stack with WebUI Vite HMR behind Traefik.
    ./Engine.sh deploy dev

    # Install from the stable release channel.
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --release-channel stable deploy prod

    # Install from a specific branch for testing.
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --repo-branch optimization/agent-context-socket-consolidation deploy prod
    ```

## Verify First Run

After deployment finishes:

1. Open `https://<your-public-fqdn>`.
2. Confirm the Borealis login page loads.
3. Check Engine startup logs.
4. Confirm the health endpoint returns `{"status":"ok"}`.

```sh
# Watch Engine startup messages.
tail -f Engine/Services/api-backend/logs/engine.log

# Confirm the API process is alive.
curl -fsSL https://<your-public-fqdn>/health
```

!!! info "HTTPS and WireGuard"
    Borealis owns its public HTTPS edge with the `traefik-edge` container when a public FQDN and ACME email are configured. WireGuard stays separate as direct UDP traffic on port `30000`.

??? note "Optional: update or redeploy the Engine"
    Use redeploy commands after pulling updates or changing Engine configuration.

    ```sh
    # Update a cloned checkout, then redeploy production.
    git pull --ff-only
    ./Engine.sh deploy prod

    # Redeploy development mode.
    ./Engine.sh deploy dev
    ```

    No-op deploys skip unchanged image builds and skip Compose when the deploy manifest, environment, image hashes, and running containers already match.

??? note "Optional: service maintenance commands"
    Use service-scoped commands when troubleshooting one Engine component.

    ```sh
    # Restart the API backend container.
    ./Engine.sh --service api-backend restart

    # Rebuild the WebUI frontend container in development mode.
    ./Engine.sh --service webui-frontend rebuild dev

    # Reload Traefik edge configuration.
    ./Engine.sh --service traefik-edge reload

    # Reconcile WireGuard tunnel state.
    ./Engine.sh --service wireguard-tunnel reconcile
    ```

## Optional: Install an Agent

Install Agents after the Engine is reachable. Agent enrollment needs the Engine URL and a site enrollment code.

=== "Windows"

    Build or refresh the Windows Agent binary from Linux when source changes:

    ```sh
    # Build Agent binaries under Data/Agent/dist/.
    Data/Agent/build-agent.sh
    ```

    Run the Agent from elevated PowerShell:

    ```powershell
    # Start interactive Agent setup from a checkout.
    .\Data\Agent\dist\windows-amd64\Agent.exe
    ```

    Use non-interactive enrollment when the Engine URL and site enrollment code are already known:

    ```powershell
    # Enroll a Windows endpoint into a Borealis site.
    .\Data\Agent\dist\windows-amd64\Agent.exe --server-url "https://borealis.example.com" --site-enrollment-code "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"
    ```

=== "Linux"

    Linux Agent builds to `Data/Agent/dist/linux-amd64/Agent`. Install commands run the downloaded binary with the Engine URL and enrollment code, then stage the Agent as `/opt/Borealis/Agent/Agent`.

    !!! warning "Separate Engine and Agent installs"
        Do not place a Linux Agent inside a deployed Engine runtime root. Use a dedicated Agent install directory so Engine redeploys cannot overwrite Agent state.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /health` (No Authentication) - Engine liveness probe.
    - `GET /api/server/time` (Operator Session) - quick sanity check after login.

    ### Related documentation

    - [Architecture Overview](architecture-overview.md)
    - [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Core%20Runtimes/Stack_Breakdown.md)
    - [Agent Runtime](../Core%20Runtimes/agent-runtime.md)
    - [Security and Trust](security-and-trust.md)
    - [Logging and Operations](../Operations%20and%20Remote%20Access/logging-and-operations.md)

    ### Bootstrap and runtime separation

    - Engine API/backend source lives in `Data/Engine/Containers/api-backend/data/`.
    - Engine WebUI source lives in `Data/Engine/Containers/webui-frontend/data/web-interface/`.
    - Engine WebUI dev/HMR runtime source lives in `Engine/Services/webui-frontend/data/web-interface/` after first Engine deploy.
    - Agent source code lives in `Data/Agent/`.
    - Runtime copies are staged to `Engine/` and `Agent/` every launch; these are disposable.
    - Engine container source lives in `Data/Engine/Containers/`; generated runtime state lives under `Engine/Deploy/` and sparse service-owned folders under `Engine/Services/<role>/`.
    - Edit durable source under `Data/` and re-run the appropriate launcher/build: `Engine.sh` for Linux Engine first install and redeploys, `Data/Agent/build-agent.sh` for Go Agent binaries, and `Agent.exe` for installed Agent service control. For rapid WebUI HMR testing, edit `Engine/Services/webui-frontend/data/web-interface/` while running `Engine.sh deploy dev`.

    ### Launch mechanics

    - `Engine.sh` is the Linux Engine first-run and redeploy path. When run from a raw one-liner or with repo options, it syncs source first; local `Engine.sh deploy` uses existing on-disk source.
    - `Engine.sh deploy` installs missing Engine OS dependencies, defaults to production, and runs Docker Compose with project name `borealis-engine`.
    - `Engine.sh deploy dev` runs the same service set but sets the WebUI frontend to Vite HMR behind Traefik. Switching between prod and dev should only recreate WebUI after the stack is already current.
    - `Agent.exe` handles dependency setup, runtime staging, repair, update checks, service install/uninstall, and runtime for Agent installs.
    - Dev mode (`Engine.sh deploy dev`) uses Vite for the WebUI behind the Traefik edge container, while the Engine API stays on loopback.
    - Production (`Engine.sh deploy prod`) runs the Engine API on loopback HTTP, serves the static WebUI from the WebUI frontend container, and publishes the app through Traefik.
    - Engine and Agent dependency checks live in their domain launchers.
    - `Engine/Deploy/image-manifest.json` records image hashes and tags. `Engine/Deploy/deploy-manifest.json` records mode, Compose/env hashes, service image hashes, changed services, and whether Compose ran or was skipped.

    ### Configuration precedence

    Engine config is assembled by `Data/Engine/Containers/api-backend/data/config.py` in this order:

    1. Explicit overrides passed to the app factory.
    2. Environment variables prefixed with `BOREALIS_`.
    3. Defaults baked into `config.py`.

    Key defaults:

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

    - Windows Agent must run elevated to create the `BorealisAgent` service plus AutoUpdater/Watchdog scheduled tasks.
    - Enrollment requires an install code and operator approval. See [Device Management](../Operations%20and%20Remote%20Access/device-management.md).
    - If enrollment fails, inspect `Agent/Logs/Agent/agent.log` and `Engine/Services/api-backend/logs/engine.log`.

    ### Health verification

    - Use `GET /health` to confirm the API is alive.
    - Use `GET /api/server/time` after login to verify session auth and API reachability.
    - Confirm WebSockets by opening the UI and checking that toasts and live updates work.
