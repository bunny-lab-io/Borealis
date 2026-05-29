# Overview
You can follow the instructions on this page to install the Borealis Engine onto a Linux host which acts as the heart of the automation platform.

!!! info "System Requirements"

    **Engine Host**:

    - Use a Linux server for the Engine. Ubuntu Server 24.04 LTS or newer is the preferred baseline.
    - While you can use something else like Fedora/Rocky Linux, it has not been tested as extensively yet.

    **DNS Records & Let's Encrypt Considerations**:

    - Point a public FQDN at the Engine host before production deployment (e.g. `borealis.bunny-lab.io`), this will be used by Let's Encrypt later during Engine setup.
    - Also be sure to have an email address ready for Let's Encrypt certificate registration (e.g. `infrastructure@bunny-lab.io`)

    **Firewall Preparation**:

    - Keep WireGuard `UDP/30000` reachable to the Linux host for remote agent operations.

## Engine Deployment Profiles
The Engine container deployment system uses conservative defaults from `Engine/Deploy/compose.env`. The sizing tables below are used for planning guidance and the engine will automatically scale itself to meet the system specs given to it everytime it is deployed/updated; if you tune database pool and PostgreSQL settings explicitly for larger installations.

=== "Homelab"
    | Typical use | Endpoints | Active operators | vCPU | RAM | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: |
    | Personal labs, testing, feature development, very small sites | Up to 250 | 1-3 | < 8 | < 16 GiB | 80-150 GiB |

=== "Small Business"
    | Typical use | Endpoints | Active operators | vCPU | RAM | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: |
    | Smaller production environments | Up to 1,000 | 2-4 | 8-15 | 16-31 GiB | 150-250 GiB |

=== "MSP / Production"
    | Typical use | Endpoints | Active operators | vCPU | RAM | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: |
    | Main Borealis target for SMB and managed-service usage | Up to 2,000 | 4-8 | 16-23 | 32-63 GiB | 500 GiB |

=== "Enterprise"
    | Typical use | Endpoints | Active operators | vCPU | RAM | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: |
    | Larger single-node environments on current architecture | Up to 10,000 | 10-20 | 24+ | 64 GiB+ | 500 GiB-1 TiB |

=== "Enterprise Clustered"
    | Typical use | Endpoints | Active operators | vCPU | RAM | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: |
    | Roadmap-only multi-node planning placeholder | 10,000+ | 20+ per node | 24+ per node | 64 GiB+ per node | 500 GiB-1 TiB per node |

## Install the Engine
Use the following one-line installer command when starting from a fresh Linux host:

```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- deploy prod
```

During deployment, Borealis will install missing dependencies, prepare runtime configuration, builds changed service container images, and starts the Docker Compose stack automatically for you.

??? note "Optional: Development and Branch Installs"
    Use these commands only when testing changes or validating a specific release channel.  *Do not use in Production.*

    ```sh
    # Deploy the development stack with WebUI Vite HMR behind Traefik.
    ./Engine.sh deploy dev

    # Install from the stable release channel.
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --release-channel stable deploy prod

    # Install from a specific branch for testing.
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --repo-branch optimization/agent-context-socket-consolidation deploy prod
    ```

### Configure the Engine
You will be asked as series of questions during initial setup for a new engine.  The questions will be generally straight-forward and not too complicated.

## First Run Checklist
After deployment finishes:

1. Navigate to: `https://<your-public-fqdn>`.
2. Confirm the Borealis Aegis Cipher page loads and configure a passphrase to encrypt all Engine secrets like machine credentials, passkeys, Github tokens, etc.

!!! warning "Do not Lose Aegis Cipher"
    If you lose the Aegis Cipher, you can forcefully reset it from the WebUI, but you will lose all stored credentials in the Engine, requiring you to manually re-enter all of them.

    Thankfully all affected credentials are clearly indicated and all scheduled jobs requiring the lost credentials are suspended until the credentials are re-entered.

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
