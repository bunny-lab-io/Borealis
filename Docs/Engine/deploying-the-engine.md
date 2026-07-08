# Deploying the Engine
You can follow the instructions on this page to install the Borealis Engine onto a Linux host which acts as the heart of the automation platform.

!!! info "System Requirements"

    **Engine Host**:

    - Use a Linux server for the Engine. Ubuntu Server 24.04 LTS or newer is the preferred baseline.
    - While you can use something else like Fedora/Rocky Linux, it has not been tested as extensively yet.

    **DNS Records & Certificate Considerations**:

    - Choose an Engine FQDN before deployment. Agents and browsers must use this FQDN, not a raw IP address.
    - Externally Accessible deployments need public DNS and an email address for Let's Encrypt certificate registration (e.g. `infrastructure@bunny-lab.io`).
    - Internal-Only deployments should use private DNS when possible. Linux Agent install commands also carry an Engine IP fallback so agents without private DNS can still connect while validating the Engine FQDN.

    **Firewall Preparation**:

    - Keep WireGuard `UDP/30000` reachable to the Linux host for remote agent operations.

## Engine Deployment Profiles
The Engine container deployment system auto-detects host vCPU and RAM on every `Engine.sh deploy` or redeploy. Borealis scores CPU and RAM separately, selects the lower profile, writes profile tuning into `Engine/Deploy/compose.env`, and applies database plus site-worker scheduled task-slot settings through Docker Compose.

=== "Homelab"
    | Typical use | Endpoints | Active operators | vCPU | RAM | Scheduled task slots | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: | ---: |
    | Personal labs, testing, feature development, very small sites | Up to 250 | 1-3 | < 8 | < 16 GiB | 5 | 80-150 GiB |

=== "Small Business"
    | Typical use | Endpoints | Active operators | vCPU | RAM | Scheduled task slots | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: | ---: |
    | Smaller production environments | Up to 1,000 | 2-4 | 8-15 | 16-31 GiB | 8 | 150-250 GiB |

=== "MSP / Production"
    | Typical use | Endpoints | Active operators | vCPU | RAM | Scheduled task slots | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: | ---: |
    | Main Borealis target for SMB and managed-service usage | Up to 2,000 | 4-8 | 16-23 | 32-63 GiB | 12 | 500 GiB |

=== "Enterprise"
    | Typical use | Endpoints | Active operators | vCPU | RAM | Scheduled task slots | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: | ---: |
    | Larger single-node environments on current architecture | Up to 10,000 | 10-20 | 24+ | 64 GiB+ | 16 | 500 GiB-1 TiB |

=== "Enterprise Clustered"
    | Typical use | Endpoints | Active operators | vCPU | RAM | Scheduled task slots | NVMe storage |
    | --- | ---: | ---: | ---: | ---: | ---: | ---: |
    | Roadmap-only multi-node planning placeholder | 10,000+ | 20+ per node | 24+ per node | 64 GiB+ per node | 16 per node | 500 GiB-1 TiB per node |

!!! info "Scheduled task slots"

    Site-worker scheduled task slots limit active scheduled-lane work items per site worker. They are not a hard count of remote devices. A shared Ansible playbook batch uses one slot for its site batch and may target multiple devices inside that Ansible process. Individual Ansible mode uses one slot per target while active.

## Configure the Timezone
Before you do anything, be sure to set the timezone on the Engine host using the following command as an example, everything deployed into the Engine will inherit this timezone via `TZ` environment variables.
```sh
sudo timedatectl set-timezone America/Denver
```

If the time itself is somehow off despite having the correct timezone, you can correct it with the following command:
```sh
date -s "1 JAN 2025 03:30:00"
```

## Install the Engine
Use the following one-line installer command when starting from a fresh Linux host:

```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- deploy prod
```

During deployment, Borealis will install missing dependencies, prepare runtime configuration, builds changed service container images, and starts the Docker Compose stack automatically for you.

### Select Engine Access Profile
Borealis supports two Engine access profiles. Both require an FQDN so agents can validate TLS hostnames correctly.

=== "Externally Accessible"

    Use this when agents and operators reach Borealis through public DNS. This is the default profile.

    ```sh
    sudo bash Engine.sh --deployment-profile externally-accessible deploy prod
    ```

    Traefik requests and renews public Let's Encrypt certificates. Operators and agents trust the Engine through normal public CA validation.

=== "Internal-Only"

    Use this when Borealis stays on private DNS or VPN-only networks and should not request public certificates.

    ```sh
    sudo bash Engine.sh --deployment-profile internal-only deploy prod
    ```

    Traefik serves a Borealis-managed local CA leaf certificate for the Engine FQDN. Agent install commands include the local CA bundle automatically. Linux Agent install commands also include the Engine IP fallback from deployment metadata. The Agent first tries the FQDN normally, then uses that IP only as a connection route hint while keeping FQDN TLS validation. Browsers need the Borealis local CA imported into the operator device or managed trust store before they show the Engine as trusted.

!!! warning

    Do not enroll agents with raw IP `--server-url` values. Use the Engine FQDN as the URL. Internal-Only Linux agents may use the generated `--server-ip-fallback` route hint when DNS is unavailable.

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

### Docker Storage Cleanup
Every Engine deploy cleans Docker storage after the stack has reconciled successfully. Borealis prunes inactive Docker images and clears Docker builder cache while keeping timestamped per-service Buildx cache exports for 7 days under `Engine/Deploy/cache/buildkit/<service>/`. Each retained export is a complete Buildx cache snapshot from that service build, so source-only rebuilds can reuse dependency layers without letting cache directories grow forever.

`site-worker` images are handled carefully because the scheduler may need the current image even when no site-worker container is running. Borealis keeps the current site-worker image available and removes stale site-worker tags separately.

!!! warning "Shared Docker Hosts"
    Engine hosts should be dedicated to Borealis. Docker cleanup removes unused images and build cache from the host, which may affect unrelated Docker workloads if you co-host them. Set `BOREALIS_SKIP_DOCKER_PRUNE=1` before deploy only when you intentionally need to preserve unused Docker images or build cache.

### Configure the Engine
You will be asked as series of questions during initial setup for a new engine.  The questions will be generally straight-forward and not too complicated.

### Engine Host Timezone
Borealis reads the Linux host timezone during every `Engine.sh deploy` or redeploy and passes that value into the Engine containers as `TZ`. Server Info uses that propagated timezone for Engine-local clock displays.

Change the host timezone from the Linux shell, then redeploy the Engine so containers receive the updated value:

```sh
sudo timedatectl set-timezone America/Denver
sudo bash Engine.sh deploy prod
```

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

    - [Architecture Overview](../Reference/architecture-overview.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Security Whitepaper](../Reference/security-whitepaper.md)
    - [Engine Log Management](../Using%20the%20Platform/engine-log-management.md)

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
    - WireGuard: UDP 30000, engine virtual IP `10.255.0.1/32`, peer network `10.255.0.0/16`, shell port 47002

    WireGuard overlay overrides must stay private IPv4. The Engine virtual IP must be a `/32`, and the peer network must be `/16` through `/30` with the Engine address inside it.

    ### Public edge and trust

    - Borealis embedded Traefik manages the HTTPS identity and dynamic route state under `Engine/Services/traefik-edge/state/` and `Engine/Services/traefik-edge/config/`.
    - `Engine.sh --deployment-profile externally-accessible|internal-only` or `BOREALIS_ENGINE_DEPLOYMENT_PROFILE` selects the access profile. The default is `externally-accessible`.
    - Externally Accessible uses ACME/Let's Encrypt. Internal-Only disables ACME and generates a Borealis local CA plus DNS-only Engine leaf certificate under `Engine/Services/traefik-edge/state/local-ca/` and `Engine/Services/traefik-edge/state/local-certs/`.
    - Internal-Only CA/cert material is included in Backup/Restore. Keep the same FQDN when migrating a live Internal-Only Engine so existing agents and browsers keep trusting the restored service.
    - Agents must use the HTTPS FQDN and rely on CA + hostname validation. Internal-Only Linux installs can persist `server_ip_fallback` in `agent.json`; this changes the TCP dial target only after normal FQDN connection fails.
    - The Python Engine is not a direct public TLS endpoint in production.

    ### Agent install and enrollment notes

    - Windows Agent must run elevated to create the `BorealisAgent` service plus AutoUpdater/Watchdog scheduled tasks.
    - Enrollment requires a site enrollment code and operator approval. See [Device Approvals](../Using%20the%20Platform/device-approvals.md).
    - If enrollment fails, inspect `Agent/Logs/Agent/agent.log` and `Engine/Services/api-backend/logs/engine.log`.

    ### Health verification

    - Use `GET /health` to confirm the API is alive.
    - Use `GET /api/server/time` after login to verify session auth and API reachability.
    - Confirm WebSockets by opening the UI and checking that toasts and live updates work.
