# Deploying the Engine
You can follow the instructions on this page to install the Borealis Engine onto a Linux host which acts as the heart of the automation platform.

!!! info "System Requirements"

    **Engine Host**:

    - Use a Linux server for the Engine. Ubuntu Server 24.04 LTS or newer is the preferred baseline.
    - While you can use something else like Fedora/Rocky Linux, it has not been tested as extensively yet.
    - Run `Engine.sh` with `sudo` unless the shell user can access `/var/run/docker.sock`.

    **DNS Records & Certificate Considerations**:

    - Choose an Engine FQDN before deployment. Agents and browsers must use this FQDN, not a raw IP address.
    - Choose one Engine network mode before deployment and keep using that network mode on redeploys.
    - Public network mode needs public DNS and an email address for Let's Encrypt certificate registration (e.g. `infrastructure@bunny-lab.io`).
    - Local network mode should use private DNS when possible. Agent install commands also carry an Engine IP fallback so agents without private DNS can still connect while validating the Engine FQDN. Every deployment mode records the Engine host IP for authenticated Linux WireGuard session recovery when a public endpoint cannot hairpin back to the same host.

    **Firewall Preparation**:

    - Keep WireGuard `UDP/30000` reachable to the Linux host for remote agent operations.

## Engine Deployment Profiles
The Engine container deployment system auto-detects host CPU and RAM specs on every engine deployment or redeployment. Borealis scores CPU and RAM separately, selects the lower sizing profile, writes profile tuning into `Engine/Deploy/compose.env`, and applies database plus site-worker scheduled task-slot settings through K3s workload manifests. This sizing profile is separate from the network mode selected during install.

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

## Profile-Managed Container Limits
`Engine.sh` writes least-privilege runtime settings during every deploy. Normal Engine services and K3s pods run as the `borealis-engine` system user/group where supported, with read-only root filesystems, dropped Linux capabilities, `no-new-privileges`, profile-scaled CPU/memory caps, and fixed read-only host timezone data. PostgreSQL runs in K3s as the official non-root PostgreSQL UID on a Longhorn-backed PVC so imported database state keeps compatible ownership. Limits are caps, not reservations. K3s `remote-desktop-guacd` stays ClusterIP-only and does not mount the Docker socket.

Site-worker memory is per active worker. If 500 workers are active at once, aggregate memory pressure is roughly `500 x site-worker cap` plus Engine service overhead. Tune active worker concurrency and per-worker caps together before scaling large environments.

Scheduled task slots control claimed work items. Ansible controller concurrency has separate `BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY` limit and defaults to `2` per site worker so individual playbooks cannot consume worker memory for every claimed slot at once.

=== "Homelab"
    | Setting | Default |
    | --- | ---: |
    | Site-worker memory cap | `256m` |
    | Site-worker CPU cap | `1.00` |
    | Site-worker PID cap | `128` |

=== "Small Business"
    | Setting | Default |
    | --- | ---: |
    | Site-worker memory cap | `384m` |
    | Site-worker CPU cap | `1.00` |
    | Site-worker PID cap | `128` |

=== "MSP / Production"
    | Setting | Default |
    | --- | ---: |
    | Site-worker memory cap | `512m` |
    | Site-worker CPU cap | `1.50` |
    | Site-worker PID cap | `128` |

=== "Enterprise"
    | Setting | Default |
    | --- | ---: |
    | Site-worker memory cap | `512m` |
    | Site-worker CPU cap | `2.00` |
    | Site-worker PID cap | `128` |

PostgreSQL memory caps derive from the selected PostgreSQL profile so `shared_buffers`, cache sizing, and container caps move together. WebUI gets separate production and dev-mode defaults so Vite dev mode has more headroom than static production serving.

Override any limit before redeploy by exporting the matching env var, for example:
```sh
BOREALIS_SITE_WORKER_MEMORY_LIMIT=768m \
BOREALIS_API_BACKEND_MEMORY_LIMIT=2g \
bash Engine.sh --network-mode public deploy prod
```

## Configure the Timezone
Borealis reads the Linux host timezone during every `Engine.sh --network-mode public|local deploy` or redeploy and passes that value into Borealis K3s pods as `TZ` and `BOREALIS_ENGINE_HOST_TIMEZONE`. Engine-managed pods also receive read-only host timezone data mounts so minimal images resolve the same local timezone as the host. Server Info uses that propagated timezone for Engine-local clock displays.

Set the timezone before deploying the Engine:

```sh
sudo timedatectl set-timezone America/Denver
```

If the host timezone changes after deployment, redeploy the Engine with the same network mode (explained further below) so containers receive the updated timezone value:

=== "Public"

    ```sh
    sudo timedatectl set-timezone America/Denver
    sudo bash Engine.sh --network-mode public deploy prod
    ```

=== "Local"

    ```sh
    sudo timedatectl set-timezone America/Denver
    sudo bash Engine.sh --network-mode local deploy prod
    ```

If the time itself is somehow off despite having the correct timezone, correct it with the following command:

```sh
date -s "1 JAN 2025 03:30:00"
```

## Deploy the Engine
When deploying Borealis, you have to choose the Engine network mode first.  This is represented as either "local" or "public". Use the matching one-line installer command when starting from a fresh Linux host. Public and Local deployments use different TLS and network assumptions, so every deployment command must include `--network-mode`.

!!! warning "Network Mode Required"

    `Engine.sh` does not assume Public or Local mode. If `--network-mode` or `BOREALIS_ENGINE_NETWORK_MODE` is missing, deployment stops with a warning before repo sync, dependency setup, or container changes.

=== "Public"

    Use this when Borealis serves multiple sites, operators, or managed environments through public DNS. Public mode is the MSP-friendly architecture: agents and operators reach the Engine through a public FQDN, Traefik requests public Let's Encrypt certificates, and clients trust the Engine through normal public CA validation.

    ```sh
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --network-mode public deploy prod
    ```

    If Borealis sits behind an outer/nested reverse proxy, set `BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS` to the outer proxy IP/CIDR so Traefik preserves client IP metadata.  Dont worry, if you don't configure environment variables, you will be prompted during engine deployment for this information.

=== "Local"

    Use this when Borealis stays inside one local environment, such as a homelab, one company / small business, or VPN-only deployment. Local mode does not request public certificates. Traefik serves a Borealis-managed local CA leaf certificate for the Engine FQDN.

    ```sh
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --network-mode local deploy prod
    ```

    Agent install commands include the local CA bundle automatically (this makes the command larger). Automatically-generated agent install commands also include the Engine IP fallback from deployment metadata. The Agent first tries the FQDN normally, then uses that IP only as a connection route hint while keeping FQDN TLS validation. Linux WireGuard tunnel setup also falls back to that IP when `wg-quick` cannot resolve the Engine FQDN. Browsers need the Borealis local CA imported into the operator's device or managed trust store before they show the Engine as trusted. Local deployments do not ask for an outer reverse proxy during interactive deployment and assume that there is none.

During deployment, Borealis starts with a short bootstrap, installs the pinned Gum terminal renderer when missing, reconciles the single-node K3s baseline, prepares runtime configuration, builds the Engine-hosted Agent installer cache from `Data/Agent`, builds changed service container images, applies K3s-owned workloads, and keeps the retired Docker Compose manifest empty.

### Local Redeploy Commands
After the first install, update and redeploy from the checked-out Borealis repository. Keep the same network mode. Local `Engine.sh` runs use whatever source is already on disk, so pull the current GitHub branch before redeploying when you want newer Engine code.

=== "Public"

    ```sh
    cd /opt/Borealis

    # Use the repo branch you want to deploy from.
    git checkout main
    git pull --ff-only

    sudo bash Engine.sh --network-mode public deploy prod
    ```

=== "Local"

    ```sh
    cd /opt/Borealis

    # Use the repo branch you want to deploy from.
    git checkout main
    git pull --ff-only

    sudo bash Engine.sh --network-mode local deploy prod
    ```

!!! warning

    Do not enroll agents with raw IP `--server-url` values. Use the Engine FQDN as the URL. Local-mode agents will automatically attempt to use the generated `--server-ip-fallback` route hint when DNS is unavailable.

### K3s Cluster
Every full Engine deploy now creates or repairs a single-node K3s cluster baseline before retired Docker Compose reconciliation. K3s owns PostgreSQL, API backend, job-scheduler, WireGuard tunnel, Traefik edge, WebUI, remote-desktop-guacd, site-worker pods, and per-worker ClusterIP Services.

Multi-node mode is separate, gated conversion. Current release supports one or three homogeneous Ubuntu nodes, per-node application workloads, separate control/edge VIPs, CloudNativePG, paired quorum admission, cluster-wide HMR isolation, and one-node-at-time release updates. API and WebUI fence odd-numbered expansion or shrinking beyond three nodes; that work remains future roadmap scope. Read [Managing Engine Clusters](managing-engine-clusters.md) before attempting conversion. If Engine FQDN uses outer or nested reverse proxy, include [edge VIP upstream cutover](managing-engine-clusters.md#cut-over-outer-reverse-proxy) in conversion plan. Production conversion requires explicit operator checkpoint after stable-K3s conformance and disposable three-node qualification.

`Engine.sh` installs K3s only when the K3s binary and `k3s.service` are missing. Later deploys reconcile the Borealis-owned K3s config, API firewall, service health, kubeconfig permissions, node readiness, node labels, and `borealis` namespace without tearing down cluster. Multi-node firewall rules permit only explicitly configured private peer CIDRs for K3s API, etcd, kubelet, Spegel, flannel, and WireGuard ports.

K3s keeps bundled Traefik and ServiceLB disabled so Borealis-owned Traefik remains the only public ingress. Borealis also installs `borealis-k3s-api-firewall.service`, which applies a host iptables chain for TCP `6443`. The rule allows loopback and IPv4 K3s CNI/flannel traffic, then drops other inbound API traffic.

The first K3s-hosted Borealis workload is `borealis-operator`. It is an internal bridge for cluster status and restricted lifecycle verbs, exposed as a ClusterIP service inside the `borealis` namespace. Runtime services do not receive kubeconfig or kubectl access; the K3s API backend and K3s job-scheduler reach the operator through `BOREALIS_OPERATOR_BASE_URL` and an HMAC-authenticated Borealis API.

`postgres-db`, `api-backend`, `job-scheduler`, `wireguard-tunnel`, `traefik-edge`, `webui-frontend`, and `remote-desktop-guacd` workloads are reconciled into K3s. PostgreSQL is the authoritative database after Stage 9, uses one Longhorn-backed PVC, stays ClusterIP-only at `postgres-db.borealis.svc`, and is initialized through a K3s schema Job. The API backend owns traffic through the `api-backend.borealis.svc.cluster.local:5001` ClusterIP Service, and production/dev WebUI traffic is routed by K3s `traefik-edge` to the K3s `webui-frontend` ClusterIP after that Service is ready. The K3s `remote-desktop-guacd` Service is the guacd target for API and site-worker VNC flows. Site-worker routes target per-worker ClusterIP Services, so site-worker pods do not need host-network loopback. The K3s WireGuard tunnel pod owns the host-network UDP listener and constrained control socket after Stage 10. K3s `traefik-edge` owns HTTP/HTTPS, ACME or local CA certificate state, and watched dynamic route files under `Engine/Services/traefik-edge/config/dynamic/`. Later deploys remove stale retired Compose containers instead of recreating them. Scoped WebUI, API, scheduler, PostgreSQL, guacd, WireGuard, and Traefik rebuilds refresh K3s workloads so they follow the current image manifest before the next full deploy.

Quick checks after a deploy:
```sh
sudo systemctl is-active k3s
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get namespace borealis --show-labels
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/borealis-operator
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/api-backend
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/job-scheduler
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/wireguard-tunnel
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/traefik-edge
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/webui-frontend
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/remote-desktop-guacd
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get service postgres-db api-backend webui-frontend remote-desktop-guacd
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- borealis-api-backend-go api-healthcheck
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/job-scheduler -- sh -lc 'case "$BOREALIS_INTERNAL_API_BASE_URL" in http://api-backend.borealis.svc.cluster.local:5001) echo scheduler-api-url=k3s ;; *) exit 1 ;; esac'
curl -fsS http://127.0.0.1:8082/ping
rg "BOREALIS_POSTGRES_TRAFFIC_OWNER|BOREALIS_DATABASE_URL|BOREALIS_API_BACKEND_UPSTREAM_HOST|BOREALIS_INTERNAL_API_BASE_URL|BOREALIS_WEBUI_TRAFFIC_OWNER|BOREALIS_WEBUI_UPSTREAM_HOST|BOREALIS_TRAEFIK_EDGE_RUNTIME_OWNER" Engine/Deploy/compose.env
sudo iptables -C INPUT -p tcp --dport 6443 -j BOREALIS-K3S-API
```

### Docker Storage Cleanup
Every Engine deploy cleans Docker storage after the stack has reconciled successfully. Borealis prunes inactive non-Borealis Docker images, removes stale Borealis service tags, and clears Docker builder cache while keeping timestamped per-service Buildx cache exports for 7 days under `Engine/Deploy/cache/buildkit/<service>/`. Each retained export is a complete Buildx cache snapshot from that service build, so source-only rebuilds can reuse dependency layers without letting cache directories grow forever.

Borealis service images are handled carefully because K3s pods may need current images after Docker build cleanup. Borealis keeps current `io.borealis.service` images available for K3s import and removes stale service tags only when no Docker container still references them.

!!! warning "Shared Docker Hosts"
    Engine hosts should be dedicated to Borealis. Docker cleanup removes unused images and build cache from the host, which may affect unrelated Docker workloads if you co-host them. Set `BOREALIS_SKIP_DOCKER_PRUNE=1` before deploy only when you intentionally need to preserve unused Docker images or build cache.

### Configure the Engine
You will be asked as series of questions during initial setup for a new engine.  The questions will be generally straight-forward and not too complicated.

## Development Considerations

!!! warning "Local Changes (Developer-Focused)"

    `git pull --ff-only` stops when local files have changed or when the branch cannot fast-forward cleanly. Review those changes before deployment so Engine updates do not mix local edits with upstream source changes, causing headaches.

??? note "Optional: Development and Branch Installs"
    Use these commands only when testing changes or validating a specific release channel.  *Do not use in Production.*

    ```sh
    # Deploy the development stack with WebUI Vite HMR behind Traefik for Local validation.
    ./Engine.sh --network-mode local deploy dev

    # Install from the stable release channel for Public use.
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --network-mode public --release-channel stable deploy prod

    # Install from a specific branch for Local validation.
    curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --network-mode local --repo-branch optimization/agent-context-socket-consolidation deploy prod
    ```

## First Run Checklist
After deployment finishes:

1. Navigate to: `https://<your-engine-fqdn>`.
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
    - [WebUI HMR Development](webui-hmr-development.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Security Whitepaper](../Reference/security-whitepaper.md)
    - [Engine Log Access](../Using%20the%20Platform/engine-log-management.md)

    ### Bootstrap and runtime separation

    - Engine public API source lives in `Data/Engine/Containers/api-backend/cmd/api-backend/`. Python under `Data/Engine/Containers/site-worker/data/` belongs to site workers and pre-API schema bootstrap; API image does not ship it.
    - Engine WebUI source lives in `Data/Engine/Containers/webui-frontend/data/web-interface/`.
    - Engine WebUI dev/HMR runtime source lives in `Engine/Services/webui-frontend/data/web-interface/` after first Engine deploy.
    - Agent source code lives in `Data/Agent/`.
    - Runtime copies are staged to `Engine/` and `Agent/` every launch; these are disposable.
    - Engine container source lives in `Data/Engine/Containers/`; generated runtime state lives under `Engine/Deploy/` and sparse service-owned folders under `Engine/Services/<role>/`.
    - Edit durable source under `Data/` and re-run the appropriate launcher/build: `Engine.sh` for Linux Engine first install and redeploys, `Data/Agent/build-agent.sh` for Go Agent binaries, and `Agent.exe` for installed Agent service control. For WebUI HMR testing, use [WebUI HMR Development](webui-hmr-development.md).

    ### Launch mechanics

    - `Engine.sh` is the Linux Engine first-run and redeploy path. It starts by printing `Starting Borealis Engine Bootstrap`, then ensures the pinned Gum binary is available under `Dependencies/Gum/bin/gum` before the deployment dashboard starts. The Gum dashboard renders stable task rows with `Resource`, `Status`, `Task`, and `Sub-Task` columns. `Resource` names are Borealis blue, `Status` shows the coarse row state, `Task` shows the task progress bar plus `[completed/total] Task`, and `Sub-Task` shows the current muted detail. Intermediate checkpoint completions tick the task counter forward, so K3s bootstrap, storage, workloads, site workers, and Docker cleanup expose staged progress without jumping around the table or permanently expanding every checkpoint row. When run from a raw one-liner or with repo options, it syncs source first; local `Engine.sh --network-mode public|local deploy` uses existing on-disk source.
    - `Engine.sh --network-mode public|local deploy` installs missing Engine OS dependencies, builds Windows/Linux Agent binaries from `Data/Agent/build-agent.sh` into `Engine/Services/api-backend/cache/AgentUpdates`, reconciles a single-node K3s baseline plus the restricted `borealis-operator` bridge, applies PostgreSQL/API/scheduler/WireGuard/Traefik/WebUI/guacd K3s workloads, defaults to production, and keeps Docker Compose retired under project name `borealis-engine`.
    - `Engine.sh --network-mode public|local deploy dev` runs the same service set but sets the WebUI frontend to Vite HMR behind Traefik and refreshes the runtime HMR source from staged WebUI source. Switching between prod and dev should only recreate WebUI after the stack is already current.
    - `Engine.sh` owns runtime identity setup for Linux Engine containers. It creates/repairs `borealis-engine`, chowns writable service paths under `Engine/Services/`, and writes resource cap env vars into `Engine/Deploy/compose.env`.
    - Stage 1 K3s baseline writes Borealis-owned config to `/etc/rancher/k3s/config.yaml.d/10-borealis.yaml`, records the config hash in `Engine/Deploy/k3s-baseline.sha256`, installs K3s only when missing, restarts K3s only when the Borealis config changes, and never calls K3s teardown or uninstall helpers.
    - Stage 1 K3s baseline owns `borealis-k3s-api-firewall.service`, which reapplies the TCP `6443` iptables guard on boot and deploy.
    - Stage 2 deploys `borealis-operator` into the `borealis` namespace as a single-replica Deployment, ClusterIP Service, Secret, ServiceAccount, Role, and RoleBinding.
    - Stage 3 keeps runtime services Kubernetes-blind by routing lifecycle work through `borealis-operator`. Its API is `POST /v1/command` with `X-Borealis-Operator-Token`.
    - Operator status verbs are `GetClusterSummary`, `ListWorkloads`, `GetWorkloadStatus`, and `ListSiteWorkers`. Operator lifecycle verbs are `RolloutKnownWorkload`, `RestartKnownWorkload`, `ScaleKnownWorkload`, `LaunchSiteWorker`, and `RetireSiteWorker`.
    - Stage 4 deploys fixed-template `webui-frontend` and `remote-desktop-guacd` bridge workloads into K3s with ClusterIP Services only. `Engine/Deploy/k3s-webui-frontend.sha256` and `Engine/Deploy/k3s-remote-desktop-guacd.sha256` record separate manifest inputs so a WebUI-only image or dev-mode change does not reconcile guacd. `Engine/Deploy/k3s-bridge-workloads.sha256` remains an aggregate bridge record.
    - Stage 7 made K3s `api-backend` the API traffic owner. It uses `Engine/Deploy/k3s-api-backend.sha256`, the `api-backend.borealis.svc.cluster.local:5001` ClusterIP Service, narrow API cache/config/logs/secrets hostPath mounts, fixed Traefik/WireGuard hostPath mounts, generated Secret env mirroring, and `BOREALIS_API_BACKGROUND_LOOPS=1`. `Engine.sh` removes stale Compose API containers during deploy.
    - Stage 6 WebUI cutover changes Traefik's core WebUI upstream to the K3s `webui-frontend` ClusterIP by writing `BOREALIS_WEBUI_TRAFFIC_OWNER=k3s` and `BOREALIS_WEBUI_UPSTREAM_HOST=<cluster-ip>` into `Engine/Deploy/compose.env`. Production and dev WebUI traffic both use this K3s route. `Engine.sh` removes stale Compose WebUI containers during deploy.
    - Stage 8 made K3s `job-scheduler` the scheduler traffic owner. It runs one `Recreate` Deployment, owns scheduled ticks, service-action queueing, and K3s site-worker reconciliation, and removes stale Compose scheduler containers during deploy.
    - Stage 9 made K3s `postgres-db` the database traffic owner. It uses one Longhorn-backed PVC, runs schema initialization as a K3s Job, points runtime `BOREALIS_DATABASE_URL` at `postgres-db.borealis.svc`, and removes stale Compose PostgreSQL containers during deploy.
    - Stage 10 made K3s `wireguard-tunnel` the tunnel traffic owner. It runs one pinned host-network Deployment, preserves `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, UDP `30000`, the `borealis-wg` interface, and the service-local control socket, and removes stale Compose WireGuard containers during deploy.
    - Stage 11 made K3s `traefik-edge` the public edge owner. It runs one host-network Deployment, preserves HTTP/HTTPS ports, ACME/local CA state, and watched dynamic route files, and removes stale Compose Traefik plus site-worker-orchestrator containers during deploy.
    - In K3s-owned site-worker mode, `job-scheduler` talks to `borealis-operator` for site-worker lifecycle. The operator creates and deletes fixed-template site-worker pods plus matching ClusterIP Services, and scheduler route files target the worker Service IP or DNS name. The retired `site-worker-orchestrator` Unix socket and Docker fallback are no longer generated or called.
    - K3s API exposes authenticated admin status at `GET /api/server/k3s/operator`; it returns operator reachability and summary data without exposing the operator secret.
    - `Agent.exe` handles dependency setup, runtime staging, repair, update checks, service install/uninstall, and runtime for Agent installs.
    - Dev mode (`Engine.sh --network-mode public|local deploy dev`) uses Vite for the WebUI behind the K3s Traefik edge pod, while the Engine API stays cluster-internal behind the `api-backend` Service.
    - Production (`Engine.sh --network-mode public|local deploy prod`) runs the Engine API behind the K3s `api-backend` Service, serves the static WebUI from the K3s `webui-frontend` workload, and publishes the app through K3s Traefik.
    - Engine and Agent dependency checks live in their domain launchers.
    - `Engine/Deploy/image-manifest.json` records image hashes and tags. `Engine/Deploy/deploy-manifest.json` records mode, Compose/env hashes, service image hashes, changed services, and whether Compose ran or was skipped.

    ### Configuration precedence

    Site-worker config is assembled by `Data/Engine/Containers/site-worker/data/config.py` in this order:

    1. Explicit overrides passed to the app factory.
    2. Environment variables prefixed with `BOREALIS_`.
    3. Defaults baked into `config.py`.

    Key defaults:

    - Database: `BOREALIS_DATABASE_URL` (required PostgreSQL connection URL)
    - Managed official assemblies: `Engine/Services/api-backend/cache/Aurora/` (generated Aurora checkout)
    - Aurora checkout: `Engine/Services/api-backend/cache/Aurora/`
    - Logs: `Engine/Services/api-backend/logs/engine.log`, `Engine/Services/api-backend/logs/error.log`, `Engine/Services/api-backend/logs/api.log`
    - WireGuard: UDP 30000, engine virtual IP `10.255.0.1/32`, peer network `10.255.0.0/16`, shell port 47002

    WireGuard overlay overrides must stay private IPv4. The Engine virtual IP must be a `/32`, and the peer network must be `/16` through `/30` with the Engine address inside it.

    ### Public edge and trust

    - Borealis embedded Traefik manages the HTTPS identity and dynamic route state under `Engine/Services/traefik-edge/state/` and `Engine/Services/traefik-edge/config/`.
    - `Engine.sh --network-mode public|local` or `BOREALIS_ENGINE_NETWORK_MODE=public|local` selects the network mode. Engine deploy fails before sync/runtime work when no network mode is explicitly provided.
    - `--deployment-profile externally-accessible|internal-only` and `BOREALIS_ENGINE_DEPLOYMENT_PROFILE` remain compatibility aliases. New docs and operator commands should use `--network-mode public|local`.
    - Public mode maps to legacy `externally-accessible`, uses ACME/Let's Encrypt, and prompts for optional outer reverse-proxy trusted IPs only when interactive.
    - Local mode maps to legacy `internal-only`, disables ACME, skips outer reverse-proxy prompts, and generates a Borealis local CA plus DNS-only Engine leaf certificate under `Engine/Services/traefik-edge/state/local-ca/` and `Engine/Services/traefik-edge/state/local-certs/`.
    - Local-mode CA/cert material is included in Backup/Restore. Keep the same FQDN when migrating a live Local Engine so existing agents and browsers keep trusting the restored service.
    - Agents must use the HTTPS FQDN and rely on CA + hostname validation. Local-mode installs can persist `server_ip_fallback` in `agent.json`; this changes REST/Socket.IO TCP dial targets only after normal FQDN connection fails. For Linux WireGuard, every Engine mode sends the detected Engine host IP as a secondary endpoint inside the device-authenticated tunnel session. Agent tries public/local FQDN first, waits for a fresh peer handshake, then tries host IP when DNS fails or FQDN path produces no handshake. This handles agents running on same host that owns public UDP forwarding without exposing host IP in public-mode install commands.
    - The Python Engine is not a direct public TLS endpoint in production.

    ### Agent install and enrollment notes

    - Windows Agent must run elevated to create the `BorealisAgent` service plus AutoUpdater/Watchdog scheduled tasks.
    - Enrollment requires a site enrollment code and operator approval. See [Device Approvals](../Using%20the%20Platform/device-approvals.md).
    - If enrollment fails, inspect `Agent/Logs/Agent/agent.log` and `Engine/Services/api-backend/logs/engine.log`.

    ### Health verification

    - Use `GET /health` to confirm the API is alive.
    - Use `GET /api/server/time` after login to verify session auth and API reachability.
    - Confirm WebSockets by opening the UI and checking that toasts and live updates work.
