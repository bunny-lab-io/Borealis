# Borealis Runtime Stack Breakdown

Explain the Borealis Engine K3s runtime, retired Docker Compose manifest, service ownership, startup order, runtime paths, and common operator commands.

## Scope
- Linux Engine only.
- Docker Engine plus Docker Buildx plugin.
- Docker Compose plugin is not required by `Engine.sh` deploy after Stage 11. It remains useful for development and CI checks that prove the retired Compose manifest stays empty.
- No Docker Desktop.
- Run `Engine.sh` with `sudo` unless the shell user can access `/var/run/docker.sock`.
- Single-node K3s baseline, Longhorn storage baseline, K3s PostgreSQL StatefulSet, K3s API/WebUI/scheduler/WireGuard/Traefik/guacd workloads, and restricted site-worker workloads are reconciled by `Engine.sh`; Docker Compose is retained only as an empty retired manifest.
- Compose project name: `borealis-engine`.
- Compose source of truth: `Data/Engine/Containers/compose.yaml`.
- Runtime state: `Engine/`.

## Compose State
`Data/Engine/Containers/compose.yaml` intentionally contains `services: {}` after Stage 11. `Engine.sh deploy` removes stale Compose containers during reconciliation and records Docker Compose as `Retired`.

## K3s Workloads
| Workload | K3s object | Main responsibility | Network endpoint |
| --- | --- | --- | --- |
| `borealis-operator` | Deployment, ServiceAccount, Role, RoleBinding, Secret, ClusterIP Service | Internal bridge for Borealis cluster status and restricted lifecycle verbs | `borealis-operator.borealis.svc`, port `8088` |
| `postgres-db` | StatefulSet, Secret, ClusterIP Services, Longhorn PVC | Authoritative PostgreSQL runtime after Stage 9 cutover | `postgres-db.borealis.svc`, port `5432` |
| `api-backend` | Deployment, Secret, ClusterIP Service | Authoritative Go API backend, live operator sessions, VNC session broker, workflow/runtime APIs after Stage 7 cutover | `api-backend.borealis.svc`, port `5001` |
| `job-scheduler` | Deployment, Secret | Authoritative scheduler manager, Postgres work leases, service-action queue, and K3s site-worker reconciliation after Stage 8 | No public Service; internal API calls use `api-backend.borealis.svc:5001` |
| `wireguard-tunnel` | Deployment, Secret | Authoritative WireGuard interface, peer config, firewall/routing, and constrained control socket after Stage 10 | host-network UDP `30000`, interface `borealis-wg` |
| `traefik-edge` | Deployment, Secret | Authoritative public HTTP/HTTPS edge, ACME/local CA, UI/API/Socket.IO/VNC routing, watched dynamic route files after Stage 11 | host-network `80`, `443`, health `127.0.0.1:8082` |
| `webui-frontend` | Deployment, ClusterIP Service | Production WebUI target and dev/HMR runtime after Stage 6 cutover | `webui-frontend.borealis.svc`, port `8000` |
| `remote-desktop-guacd` | Deployment, ClusterIP Service | Authoritative VNC-only Apache Guacamole guacd runtime after Stage 10 cutover | `remote-desktop-guacd.borealis.svc`, port `4822` |
| `site-worker-*` | Pod, per-worker ClusterIP Service | Dynamic site-scoped remote-operation and automation workers | `site-worker-<site-slug>.borealis.svc`, per-worker remote ops and remote desktop ports |

`borealis-operator` is the first K3s-hosted Borealis workload. It receives a namespace-scoped ServiceAccount with read-only status access plus restricted lifecycle access in the `borealis` namespace. It exposes `POST /v1/command` behind `X-Borealis-Operator-Token`. Status verbs are `GetClusterSummary`, `ListWorkloads`, `GetWorkloadStatus`, and `ListSiteWorkers`. `ListSiteWorkers` enriches site-worker pod state with CPU/RAM podmetrics from Metrics Server when available. Lifecycle verbs are `RolloutKnownWorkload`, `RestartKnownWorkload`, `ScaleKnownWorkload`, `LaunchSiteWorker`, and `RetireSiteWorker`. Lifecycle calls accept only known service keys, immutable allowlisted Borealis image refs, and fixed pod/service templates; there is still no raw YAML, arbitrary pod spec, secret, node, hostPath, privileged pod, arbitrary service account, or arbitrary env/volume path.

K3s `api-backend` and `job-scheduler` query the operator through `BOREALIS_OPERATOR_BASE_URL`, which `Engine.sh` resolves from the K3s Service ClusterIP after the operator Service exists. Runtime services do not receive kubeconfig or kubectl access.

K3s `postgres-db` uses one replica, a Longhorn-backed PVC named `postgres-data-postgres-db-0`, generated PostgreSQL credentials, and the same profile-managed PostgreSQL startup settings previously used by Compose. It initializes PostgreSQL under `/var/lib/postgresql/data/pgdata` instead of the PVC mount root so filesystem metadata such as `lost+found` does not block `initdb`. It is ClusterIP-only and annotated as `borealis.io/traffic-owner=k3s`; Engine runtime services use `postgres-db.borealis.svc:5432`.

Stage 9 cutover used the previous shadow import path as proof, then normal deploy quiesced K3s API/scheduler/site-worker writers, imported a final logical snapshot from Compose PostgreSQL, changed runtime `BOREALIS_DATABASE_URL` to the K3s Service, ran a K3s schema initializer Job, and retired stale Compose `borealis-engine-postgres-db` containers. `Engine.sh --network-mode public|local --service postgres-db shadow-import prod` is now a legacy pre-cutover validation command and refuses to run once K3s owns traffic.

The K3s `api-backend` runs one pod from the same API image, mirrors generated runtime env into `borealis-api-backend-runtime-env`, listens on pod networking at `${BOREALIS_API_BACKEND_K3S_BRIDGE_PORT:-5001}`, and exposes the `api-backend.borealis.svc` ClusterIP Service. The API pod keeps in-pod health and self-calls on loopback, while K3s Traefik, scheduler, and site-worker runtime env target the Service DNS. It owns API background loops after Stage 7 cutover with `BOREALIS_API_BACKGROUND_LOOPS=1`, and `Engine.sh` removes stale `borealis-engine-api-backend` containers during deploy.

`job-scheduler` runs one K3s replica with a `Recreate` rollout strategy so deploys do not create overlapping scheduler loops. It has no ServiceAccount token, no kubeconfig, and no Docker socket. It uses K3s PostgreSQL through `postgres-db.borealis.svc`, calls the K3s API backend through `api-backend.borealis.svc.cluster.local:5001`, receives runtime env through `borealis-job-scheduler-runtime-env`, and keeps fixed hostPath access to API cache/log/config/secrets plus Traefik dynamic route files.

`traefik-edge` runs as one K3s host-network pod. It keeps Borealis-managed Traefik as public edge, certificate owner, and watched dynamic-route reader without using bundled K3s Traefik, ServiceLB, or Kubernetes Ingress. It mounts only `Engine/Services/traefik-edge`, receives a scoped runtime Secret, and keeps existing ACME/local CA files and dynamic route hotload behavior.

`webui-frontend` and `remote-desktop-guacd` K3s workloads run one replica and remain ClusterIP-only. Stage 6 routes production and dev WebUI traffic from K3s Traefik edge to the K3s `webui-frontend` Service IP. K3s API and site-workers use the `remote-desktop-guacd.borealis.svc.cluster.local:4822` Service as their guacd target after Stage 10. Stage 6 removes the Compose WebUI service, and Stage 10 removes stale `borealis-engine-remote-desktop-guacd` containers during deploy. Dev WebUI pods use fixed read-only hostPath mounts for the same runtime source paths Compose used, plus memory-backed writable scratch for Vite optimizer and temp files.

## K3s Storage Baseline
| Component | K3s object | Main responsibility | Exposure |
| --- | --- | --- | --- |
| Longhorn | Namespace `longhorn-system`, controller Deployments, DaemonSets, CSI resources, upstream StorageClass `longhorn`, and Borealis StorageClass `borealis-longhorn` | Persistent volume backend for Borealis PVC-backed workloads | Cluster-internal |

`Engine.sh deploy` reconciles Longhorn before Borealis K3s workloads so PVC-backed cutover stages have a known storage baseline. K3s PostgreSQL is the first authoritative Longhorn-backed Borealis workload. The default manifest is pinned by `BOREALIS_K3S_LONGHORN_VERSION` and can be overridden with `BOREALIS_K3S_LONGHORN_MANIFEST_URL`.

Borealis uses `BOREALIS_K3S_PVC_STORAGE_CLASS` for workload manifests. Fresh installs default to the Borealis-owned `borealis-longhorn` StorageClass, which uses Longhorn with one replica for the single-node non-HA K3s baseline. `BOREALIS_K3S_BOREALIS_LONGHORN_STORAGE_CLASS` changes that class name, and `BOREALIS_K3S_BOREALIS_LONGHORN_REPLICA_COUNT` changes the replica count for new StorageClasses. `BOREALIS_K3S_STORAGE_CLASS` remains accepted as a compatibility alias for explicit operator overrides.

Existing PostgreSQL PVCs keep their current StorageClass during redeploy because StatefulSet volume claim templates and StorageClass parameters are effectively immutable for safe in-place operation. The upstream Longhorn manifest marks `longhorn` as a default StorageClass, so `Engine.sh` clears that default annotation after every Longhorn reconcile and keeps `borealis-longhorn` explicit-use only. K3s `local-path` remains default for non-Borealis or ad hoc PVCs until an explicit policy change.

Longhorn requires host iSCSI support. `Engine.sh deploy` installs or verifies `open-iscsi` on Debian-style systems, `iscsi-initiator-utils` on RHEL-style systems, or equivalent distro packages, loads `iscsi_tcp`, and verifies `iscsid` is running before applying Longhorn. Normal deploy does not delete Longhorn objects, volumes, PVCs, or existing PostgreSQL state.

After Stage 9, K3s PostgreSQL is the only supported traffic owner. `BOREALIS_K3S_POSTGRES_ENABLED=0` is not valid for normal deploy, and Engine fails fast instead of silently skipping required database reconciliation. PVC cleanup remains intentionally manual.

## Least-Privilege Runtime
`Engine.sh` creates or repairs a `borealis-engine` system user/group with stable numeric IDs and writes `BOREALIS_ENGINE_RUNTIME_OWNER_UID:GID` into `Engine/Deploy/compose.env`.

Default runtime policy:
- K3s `api-backend`, `job-scheduler`, `borealis-operator`, WebUI, guacd, and site-worker pods use the Borealis runtime UID/GID in their pod security contexts where supported.
- K3s `remote-desktop-guacd` runs as `borealis-engine`, exposes only its ClusterIP Service on port `4822`, mounts only read-only host timezone data, and has no Docker socket access.
- K3s `postgres-db` runs as the official PostgreSQL non-root UID by default so imported database state keeps compatible ownership. `Engine.sh` writes that UID into `BOREALIS_POSTGRES_RUNTIME_UID` and uses the Borealis runtime group for shared access where Kubernetes security context supports it.
- Hardened Engine services declare `no-new-privileges`, `cap_drop: [ALL]`, read-only root filesystem, tmpfs `/tmp`, `pids_limit`, `mem_limit`, and `cpus`.
- K3s `traefik-edge` runs as UID `0` with the Borealis runtime group because host-network ports `80` and `443` need low-port bind rights and Traefik must renew strict `0600` ACME state owned by the Borealis runtime user for Backup/Restore export. It drops all default capabilities and adds only `NET_BIND_SERVICE` plus `DAC_OVERRIDE`.
- K3s `wireguard-tunnel` remains explicit root exception because WireGuard interface setup needs `/dev/net/tun`, `NET_ADMIN`, and `NET_RAW`. It runs as UID `0` with the Borealis runtime group so dropped DAC capabilities do not block its service-local control socket. It still uses `no-new-privileges`, dropped default capabilities, read-only root filesystem, writable service-local run directory, and resource limits.
- No long-running Engine service mounts the Docker socket after Stage 11 Compose retirement.
- K3s `api-backend`, `job-scheduler`, `webui-frontend`, `remote-desktop-guacd`, `traefik-edge`, `wireguard-tunnel`, and site-worker pods run with no ServiceAccount token, dropped default capabilities where possible, read-only root filesystems, `RuntimeDefault` seccomp, tmpfs-style `emptyDir` for `/tmp`, read-only host timezone data mounts, and CPU/memory limits. API, WebUI, guacd, and site-workers stay ClusterIP-only; scheduler uses pod networking without a Service; Traefik and WireGuard use host networking only to preserve current HTTP/HTTPS and WireGuard listener contracts. Dev WebUI bridge pods also receive memory-backed writable `node_modules/.vite` and `node_modules/.vite-temp` mounts because Vite writes optimized dependency and resolved config bundles there.
- K3s `postgres-db` uses a short-lived root init container with only `CHOWN`, `DAC_OVERRIDE`, and `FOWNER` capabilities to create and own the PostgreSQL PVC data subdirectory across first-run and retry paths. The main PostgreSQL container still runs as the PostgreSQL runtime UID with dropped capabilities, `no-new-privileges`, read-only root filesystem, memory-backed scratch mounts, and ClusterIP-only exposure.

Writable bind mounts are service runtime paths under `Engine/Services/`. `Engine.sh` chowns those paths to the runtime owner during deploy while preserving stricter modes for API secrets, WireGuard secrets, and Traefik ACME storage. PostgreSQL runtime state now lives on the Longhorn PVC and is not deleted during normal deploy.

WireGuard key and listener config files are `0640 borealis-engine:borealis-engine`, and the WireGuard config directory is `0750`. The API backend writes those files as the runtime owner, while `wireguard-tunnel` reads them as UID `0` with only the Borealis runtime group and no DAC override capability.

`Engine/Deploy/runtime.env` and `Engine/Deploy/compose.env` are owned by `root:borealis-engine` with mode `0640`. They contain runtime settings and selected secrets needed to render K3s Secrets during deploy. `webui-frontend.env` remains `0600`.

## Reverse Proxy Client IP Preservation
When another reverse proxy sits in front of `traefik-edge`, Borealis must trust only that proxy IP or CIDR. Otherwise all API requests look like they originate from the proxy, and IP-scoped enrollment rate limits can block every agent behind it.

Set these Engine env values before deploy or `traefik-edge` reload:
```text
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32
BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS=
BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS=
```

`BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS` is the fallback list for both forwarded headers and PROXY protocol. Use the specific override variables only when HTTP and HTTPS have different upstream proxy paths. Keep the list narrow. Do not use `0.0.0.0/0` or clients can spoof `X-Forwarded-For`.

For HTTP `:80`, an outer HTTP reverse proxy should pass or append `X-Forwarded-For`; embedded Traefik trusts it only when the outer proxy address matches `forwardedHeaders.trustedIPs`.

For HTTPS with TLS passthrough, an outer TCP reverse proxy cannot add HTTP headers. Configure the outer TCP service to send PROXY protocol and configure Borealis embedded Traefik to trust that outer proxy IP:
```yaml
tcp:
  services:
    borealis-websecure:
      loadBalancer:
        proxyProtocol:
          version: 2
        servers:
          - address: "192.168.3.252:443"
```

If the outer proxy is itself behind another load balancer or proxy, configure that outer proxy to trust its upstream client-IP source first. Borealis can preserve only the client IP that reaches the outer proxy.

Deploy examples:
```sh
# Rebuild when the traefik-edge image source changed.
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32 bash Engine.sh --network-mode public --service traefik-edge rebuild prod

# Reload restarts the K3s Traefik edge pod after env-only trust list changes.
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32 bash Engine.sh --network-mode public --service traefik-edge reload prod
```

Validate with:
```sh
rg "POST /api/agent/enroll/request" Engine/Services/api-backend/logs/api.log
rg "enrollment rate limited key=ip" Engine/Services/api-backend/logs/device_enrollment.log
```

## Volume Bindings
All Borealis K3s pods receive these fixed read-only host timezone data mounts:
```text
/etc/localtime     -> /etc/localtime:ro
/usr/share/zoneinfo -> /usr/share/zoneinfo:ro
```

`api-backend`:
```text
emptyDir scratch root -> /opt/Borealis/Engine/Services/api-backend
Engine/Services/api-backend/cache -> /opt/Borealis/Engine/Services/api-backend/cache
Engine/Services/api-backend/config -> /opt/Borealis/Engine/Services/api-backend/config
Engine/Services/api-backend/logs -> /opt/Borealis/Engine/Services/api-backend/logs
Engine/Services/api-backend/secrets -> /opt/Borealis/Engine/Services/api-backend/secrets
Engine/Services/traefik-edge/config -> /opt/Borealis/Engine/Services/traefik-edge/config
Engine/Services/traefik-edge/env    -> /opt/Borealis/Engine/Services/traefik-edge/env
Engine/Services/traefik-edge/logs   -> /opt/Borealis/Engine/Services/traefik-edge/logs
Engine/Services/traefik-edge/state  -> /opt/Borealis/Engine/Services/traefik-edge/state
Engine/Services/wireguard-tunnel/config  -> /opt/Borealis/Engine/Services/wireguard-tunnel/config
Engine/Services/wireguard-tunnel/run     -> /opt/Borealis/Engine/Services/wireguard-tunnel/run
Engine/Services/wireguard-tunnel/secrets -> /opt/Borealis/Engine/Services/wireguard-tunnel/secrets
```

`api-backend` does not mount the whole `Engine/Services` tree or its whole service root from hostPath. It receives an `emptyDir` service root plus exact API cache/config/logs/secrets subpaths and specific Traefik and WireGuard paths needed for edge settings and tunnel control. It does not mount the Docker socket; Server Info reads K3s workload status from `borealis-operator`, and scheduler/site-worker task state comes from job-scheduler snapshots. Sites reads K3s site-worker metrics through the operator-backed `/api/server/workers` payload and skips Docker metadata reads once K3s metadata is present. Service actions are queued for K3s `job-scheduler`, which routes K3s-owned workload/site-worker lifecycle to `borealis-operator`.

K3s `api-backend` mounts only the reviewed API subpaths plus fixed Traefik and WireGuard runtime paths. It does not use host networking and does not mount kubeconfig, a ServiceAccount token, or the Docker socket. The pod uses the generated K3s Secret `borealis-api-backend-runtime-env` because Kubernetes pods do not support Compose `env_file`; the Secret mirrors deploy-time env and does not replace Aegis-protected application secrets.

`site-worker-orchestrator` is retired after Stage 11. Deploy still removes stale Compose-era containers with that name, but the Go runtime source, Docker lifecycle fallback, Unix socket, and K3s scheduler hostPath mount have been removed.

K3s `job-scheduler` hostPath contract:
```text
Engine/Deploy -> /opt/Borealis/Engine/Deploy:ro
Engine/Services/api-backend/cache -> /opt/Borealis/Engine/Services/api-backend/cache
Engine/Services/api-backend/config -> /opt/Borealis/Engine/Services/api-backend/config:ro
Engine/Services/api-backend/logs -> /opt/Borealis/Engine/Services/api-backend/logs
Engine/Services/api-backend/secrets -> /opt/Borealis/Engine/Services/api-backend/secrets:ro
Engine/Services/traefik-edge/config/dynamic -> /opt/Borealis/Engine/Services/traefik-edge/config/dynamic
```

`job-scheduler` is the single writer for site-worker Traefik route files. Stage 8 runs it in K3s as the authoritative scheduler manager, with `BOREALIS_SITE_WORKER_LIFECYCLE_MODE=k3s`. Empty, `auto`, unknown, and legacy `docker`, `compose`, or `site-worker-orchestrator` lifecycle modes all resolve to K3s after Compose retirement. It writes and retires `site-worker-<worker_guid>.yml` files under the dynamic Traefik config directory, routes each worker through the worker's ClusterIP Service IP or DNS name, and uses `borealis-operator` for worker launch/list/retire plus known workload restarts. `site-worker-*` containers set `BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES=0` so legacy Python route helpers keep database state only and do not write files.

K3s site-worker pods and Services use deterministic `site-worker-<sanitized-site-name>` names when a site name is available, with symbols stripped, whitespace collapsed to dashes, and the slug bounded to Kubernetes name limits. The worker UUID remains the internal worker identity in labels, environment, route metadata, and database records. Site create and rename paths reject names that would produce an empty slug, duplicate another site's slug, or exceed the K3s object-name budget. Existing healthy pods are not renamed in place; new names appear when workers are launched or replaced. K3s site-worker pods bind on pod networking behind a matching ClusterIP Service instead of host-network loopback. They use `restartPolicy: OnFailure` so transient nonzero worker exits restart inside the same pod, while normal idle TTL exits still complete cleanly. Scheduler reconcile retires terminal, stale host-loopback, and ClusterIP-missing K3s worker pods, then marks their worker rows lost so demand reconciliation can create replacement workers.

K3s site-worker resource metrics are read only by `borealis-operator` from `metrics.k8s.io` podmetrics inside the `borealis` namespace. K3s `api-backend` merges those operator snapshots into `/api/server/workers`, preserving the existing worker payload shape used by Sites and Server Info. Metrics Server provides CPU and RAM only here; Docker-backed workers remain the source for NET and DISK mini-trends.

K3s `postgres-db`:
```text
Longhorn PVC postgres-data-postgres-db-0 -> /var/lib/postgresql/data
emptyDir postgres-run -> /var/run/postgresql
```

K3s `traefik-edge`:
```text
Engine/Services/traefik-edge -> /opt/Borealis/Engine/Services/traefik-edge
```

Traefik static config renders to `Engine/Services/traefik-edge/config/traefik.yml`. Core Borealis routes render to `Engine/Services/traefik-edge/config/dynamic/core.yml`. Site-worker route files use `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml` so Traefik can hotload worker route adds/removals from the watched dynamic directory without a pod restart. Externally Accessible deployments store ACME state in `state/acme.json`; Internal-Only deployments store Borealis local CA and leaf certificate material under `state/local-ca/` and `state/local-certs/`.

`remote-desktop-guacd`:
No service data, secret, Docker socket, Traefik, API runtime, or host log bind mounts. Guacd only receives the common read-only host timezone data mounts, writes transient in-container logs under `/tmp/borealis-guacd-logs`, and operator diagnostics should use K3s pod logs for `remote-desktop-guacd`.

K3s `wireguard-tunnel`:
```text
Engine/Services/wireguard-tunnel -> /opt/Borealis/Engine/Services/wireguard-tunnel
/dev/net/tun -> /dev/net/tun
```

`wireguard-tunnel` runs as a K3s `hostNetwork` Deployment pinned to Borealis Engine nodes with `borealis.io/engine-node=true`. It keeps the existing WireGuard service runtime directory, control socket path, firewall rules, `/32` peer policy, and UDP listener port. The pod runs as root with the Borealis runtime group, no ServiceAccount token, read-only root filesystem, tmpfs `/tmp` and `/run`, `RuntimeDefault` seccomp, and only `NET_ADMIN` plus `NET_RAW`.

K3s `webui-frontend` dev hostPath mounts:
```text
Engine/Services/webui-frontend/data/web-interface/src        -> /opt/Borealis/Data/Engine/web-interface/src:ro
Engine/Services/webui-frontend/data/web-interface/public     -> /opt/Borealis/Data/Engine/web-interface/public:ro
Engine/Services/webui-frontend/data/web-interface/Unit_Tests -> /opt/Borealis/Data/Engine/web-interface/Unit_Tests:ro
Engine/Services/webui-frontend/data/web-interface/index.html -> /opt/Borealis/Data/Engine/web-interface/index.html:ro
Engine/Services/webui-frontend/data/web-interface/package.json -> /opt/Borealis/Data/Engine/web-interface/package.json:ro
Engine/Services/webui-frontend/data/web-interface/tsconfig.json -> /opt/Borealis/Data/Engine/web-interface/tsconfig.json:ro
Engine/Services/webui-frontend/data/web-interface/vite.config.mts -> /opt/Borealis/Data/Engine/web-interface/vite.config.mts:ro
```

`Engine.sh` seeds `Engine/Services/webui-frontend/data/web-interface/` from committed WebUI source when the runtime copy is missing. Development deploys and `webui-frontend rebuild dev` sync that runtime copy from `Data/Engine/Containers/webui-frontend/data/web-interface/` every time so Vite/HMR serves current staged source from the K3s WebUI pod. Production deploys keep the existing runtime copy unless it is missing, or unless `BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE=1` is set before deploy to discard and reseed it. The K3s WebUI pod reads that runtime copy but writes Vite optimized dependencies under memory-backed `node_modules/.vite` and Vite resolved config bundles under memory-backed `node_modules/.vite-temp`; edit durable WebUI source under `Data/`, then redeploy or rebuild dev mode to refresh HMR runtime source.

The WebUI global realtime bridge uses authenticated SSE at `/api/realtime/events` for normal app events such as inventory, service, notification, watchdog, and operator-presence refreshes. It does not connect to root `/socket.io` during normal page load or presence sync. Root Socket.IO remains allowlisted only for legacy workflow-node events that explicitly emit legacy requests, while remote shell and remote desktop use their own per-session worker URLs.

The K3s WebUI workload uses fixed read-only source mounts in dev mode and memory-backed `emptyDir` volumes for `node_modules/.vite` and `node_modules/.vite-temp`. In production mode it runs from the built image without host source mounts. Stage 6 makes the K3s WebUI workload the production and dev traffic target by setting `BOREALIS_WEBUI_TRAFFIC_OWNER=k3s` and rendering Traefik's core WebUI upstream to the K3s Service ClusterIP. `Engine.sh` removes stale Compose WebUI containers during deploy instead of recreating them.

Borealis uses host bind mounts for runtime ownership clarity, not named volumes as a security boundary. Security comes from explicit narrow targets, read-only flags, non-root users, dropped capabilities, and one-writer ownership. Replacing a broad bind with a named volume does not make another container's write access safer by itself.

## Deploy Order
`Engine.sh --network-mode public|local deploy [prod|dev]` performs these phases:

1. Parse launch options.
2. If repo/release/branch options were supplied, sync the repository and re-exec installed `Engine.sh`.
3. Install or verify Engine dependencies.
4. Reconcile single-node K3s baseline. Install K3s only when missing, apply Borealis K3s config, apply IPv4 TCP `6443` firewall guard, verify service/kubeconfig/node/container-runtime state, disable bundled Traefik/ServiceLB, and create the `borealis` namespace.
5. Resolve PostgreSQL traffic ownership. After Stage 9, K3s is the only supported owner and host-loopback PostgreSQL conflict checks are skipped.
6. Create or repair `borealis-engine` runtime identity, then create service runtime tree under `Engine/Services/`.
7. Seed runtime WebUI source under `Engine/Services/webui-frontend/data/web-interface/` when missing.
8. Prune empty legacy runtime paths.
9. Resolve Engine FQDN, network mode, and certificate mode. Public resolves ACME email; Local generates or renews Borealis local CA/leaf certificate material.
10. Detect sizing profile from vCPU/RAM and render `Engine/Deploy/runtime.env` for shared container runtime settings, mode-scoped `webui-frontend.env`, and `Engine/Deploy/compose.env` for Compose interpolation plus profile-managed DB/site-worker tuning.
11. Compute service input hashes from each service's declared source, Dockerfile, build context, target mode, and dependency inputs.
12. Build changed local images as `borealis-engine/<service>:sha-<hash>`.
13. Write `Engine/Deploy/image-manifest.json`.
14. Import the `borealis-operator` image into K3s containerd when missing, apply the fixed operator manifests, and wait for Deployment rollout.
15. Import `webui-frontend` and `remote-desktop-guacd` images into K3s containerd when missing, apply fixed workload manifests, and wait for Deployment rollouts.
16. Re-render `compose.env` with resolved image tags, the operator ClusterIP URL, and the WebUI upstream owner/ClusterIP while keeping service runtime env files free of image tag variables.
17. Import the `wireguard-tunnel` image into K3s containerd when missing, remove any stale Compose `borealis-engine-wireguard-tunnel` container, apply the K3s host-network Deployment, and verify the control socket.
18. Import the `postgres-db` image into K3s containerd when missing, apply the PostgreSQL StatefulSet/Service/PVC manifests, and wait for PVC binding plus StatefulSet rollout.
19. Run Engine schema initialization as a K3s Job against `postgres-db.borealis.svc`.
20. Import the `api-backend` image into K3s containerd when missing, apply the API Service/Deployment traffic-owner manifest, wait for Deployment rollout, then reconcile K3s Traefik so public API and Socket.IO routes target the API Service DNS.
21. Remove any stale Compose `borealis-engine-job-scheduler` container, import the `job-scheduler` image into K3s containerd when missing, apply the authoritative scheduler manifest with API Service DNS, and wait for Deployment rollout.
22. Verify the K3s `traefik-edge` ping endpoint after edge reconciliation.
23. Remove stale retired Compose containers for all former Engine services.
24. Write `Engine/Deploy/deploy-manifest.json` with Compose action `retired`.
25. Prune inactive Docker images, Docker builder cache, and Engine Buildx cache exports older than 7 days after successful reconciliation.

Build output follows `Engine.sh` service domains. Compose is reported under `Reconciliation` only as `Retired`.
| Domain | Item |
| --- | --- |
| k3s Cluster | Ensuring Cluster Exists |
| k3s Cluster | Longhorn Storage |
| k3s Cluster | K3s PostgreSQL DB |
| k3s Cluster | Borealis Operator |
| k3s Cluster | K3s API Backend |
| k3s Cluster | K3s Job Scheduler |
| k3s Cluster | K3s WireGuard Tunnel |
| k3s Cluster | K3s WebUI Frontend |
| k3s Cluster | Guacamole Bridge |
| Frontend | WebUI Frontend |
| Backend | API Backend |
| Backend | Job Scheduler |
| Backend | Site Worker Orchestrator |
| Backend | Site Worker |
| Backend | Guacamole Remote Desktop |
| Networking | Traefik Reverse Proxy |
| Networking | WireGuard Server |
| Database | PostgreSQL DB |

Build domains are not the same as runtime dependency order.

## Local Build Behavior
Borealis-built Engine images are local in this pass; no Borealis image push or GHCR workflow is used.

Image naming:
```text
borealis-engine/<service>:sha-<inputhash12>
```

Build cache:
- Docker Buildx uses retained `Engine/Deploy/cache/buildkit/<service>/<YYYYMMDDTHHMMSSZ>-<inputhash12>` exports when available.
- Hosts without usable Buildx fall back to `DOCKER_BUILDKIT=1 docker build`.
- Successful Buildx builds write a full `mode=max` cache export for the service, and successful deploys prune inactive non-Borealis Docker images with `docker image prune -a --filter label!=io.borealis.service`, prune stale Borealis service-labeled tags service-by-service, clear Docker builder cache with `docker builder prune --all`, and delete whole Engine Buildx cache export directories older than 7 days. Set `BOREALIS_SKIP_DOCKER_PRUNE=1` to skip this cleanup on a shared Docker host.
- If Docker cache metadata is corrupt during a required image build or cleanup restore, `Engine.sh` prunes Docker builder cache and retries that image build without cache before failing the deploy.
- `api-backend` keeps repo-root build context because it packages `Data/Agent` and `Agent.exe`.
- `api-backend` uses an Alpine runtime image with the Go API binary plus `ca-certificates`, `git`, and `tzdata`. WireGuard command execution belongs to `wireguard-tunnel` through its control socket.
- `job-scheduler` uses the Alpine scheduler image without Docker CLI, Docker Compose plugin, or Docker socket access. It runs only the queue/reconcile mode. The retired `site-worker-orchestrator` Go runtime source and Docker lifecycle fallback are removed, and the API/scheduler image entrypoints plus Go binary reject retired orchestrator roles.
- `borealis-operator` uses the same Go api-backend binary in a minimal Alpine image with `ca-certificates` and `tzdata`; it is built locally and imported into K3s containerd instead of started by Compose.
- Service input hashes come from declared build inputs, not the repo-wide Git commit. A WebUI-only commit should not invalidate `api-backend` or `job-scheduler`.
- `api-backend`, `job-scheduler`, and `borealis-operator` share the Go api-backend binary. `Engine.sh` builds that binary only when one of those images needs a Docker rebuild, then reuses it for the rest of that deploy pass.
- `site-worker` is built as a local image but may not have a running container. Deploy cleanup protects the current site-worker image and removes stale site-worker tags only when no container still references them.
- Scoped Agent binary redeployment rebuilds singular Agent ZIP cache and `site-worker` image only. Running API reads updated `agent_artifact.json` and ZIP through fixed hostPath mounts, so API image/restart is not required.
- `webui-frontend`, `traefik-edge`, `postgres-db`, `remote-desktop-guacd`, and `wireguard-tunnel` use service-local build contexts.
- Service-local build contexts carry their own `.dockerignore` files so `node_modules`, WebUI build output, Python bytecode, pytest caches, logs, and local test output stay out of image contexts.
- Deploy mode is part of the image hash only for services with explicit mode targets, currently `webui-frontend`. Switching between prod and dev should not make PostgreSQL, guacd, WireGuard, Traefik, or the API image appear changed unless their own inputs changed.
- `compose.env` carries image tags, stable env-file paths, runtime UID/GID, hardware deployment profile metadata, Engine access profile metadata, certificate paths, DB pool values, PostgreSQL startup settings, profile-managed resource caps, and site-worker scheduled work-item slots.
- `runtime.env` is shared by API, PostgreSQL, guacd, and WireGuard. It intentionally excludes image tag variables and keeps stable production WebUI defaults so one image or mode change does not mutate every container's environment.
- `webui-frontend.env` overrides shared runtime settings with the requested `BOREALIS_WEBUI_MODE`. Switching `prod`/`dev` should reconcile only the K3s `webui-frontend` workload when Compose services are already healthy.
- Traefik routes production and dev WebUI traffic to the K3s `webui-frontend` ClusterIP when `BOREALIS_WEBUI_TRAFFIC_OWNER=k3s`.

Deploy output:
- `Engine.sh` starts with `Starting Borealis Engine Bootstrap`, then installs or verifies the pinned Gum renderer under `Dependencies/Gum/bin/gum` before the deployment dashboard starts.
- Interactive TTY runs use the Gum dashboard unless `BOREALIS_DEPLOY_UI=plain` is set. Noninteractive output keeps the plain renderer.
- The Gum dashboard shows the title, mode/network mode, detected sizing profile, state counts, active build log path, Kubernetes-aware resource table, and current step beneath the table.
- Service rows start as `Pending...` and update in place as deploy stages run.
- Gum service rows render with `Domain`, `Resource`, `Action`, `State`, and `Kubernetes` columns. Plain service rows render with `Domain`, `Item`, `Status`, and `Last Status Update` columns.
- The `State` column uses a mint-green `Ready` marker so operators can scan completed Kubernetes resources quickly.
- The deployment dashboard omits the retired Docker Compose row and final WebUI smoke row because those are implementation/status details already reflected in log output and final command success.
- The `k3s Cluster` rows render first because Engine deploy reconciles the cluster and operator bridge before cleanup work.
- `Last Status Update` uses a human-readable local timestamp such as `July 11th 2026 @ 3:03PM`.
- Domains include `Frontend`, `Backend`, `Networking`, `Database`, `k3s Cluster`, and `Housekeeping`.
- Item names are friendly display labels such as `API Backend`, `Job Scheduler`, `Traefik Reverse Cluster Proxy`, `WireGuard Server`, and `PostgreSQL Database`.
- Service rows use compact status values such as `Up-to-Date`, `Building Go binary`, `(Re)Building Container Image`, `Ready - Image (Re)Built`, `Starting`, `Running`, `Running - Healthy`, `Reconciling Stack`, `Stack Reconciled`, or `Complete`.
- Shared build-artifact or image-reuse relationships appear only in transient status text. For example, the `Job Scheduler` row may show `[Shares API Backend Image] -> (Re)Building Container Image`. Runtime health updates later replace those sharing notes with K3s readiness state.
- Database schema setup updates the `PostgreSQL DB` row with table-level progress such as `Ensuring Table "devices" Exists`, writes each table progress line to `Engine/Deploy/build.log`, then returns the row to `Ready - K3s DB` after maintenance completes.
- K3s cluster status uses the `k3s Cluster` domain. `Ensuring k3s Cluster Exists` reports baseline reconcile progress. `Borealis Operator`, `API Backend`, `Job Scheduler`, `WireGuard Server`, `Traefik Reverse Cluster Proxy`, `WebUI Frontend`, and `Apache Guacamole` report image import, manifest apply, and Deployment rollout.
- Build log and deploy manifest still record Compose action as `retired` after Stage 11. The visible dashboard omits that row, and the Compose manifest stays empty so deploy can prove no retired service has drifted back into Compose ownership.
- Image/cache pruning uses the `Housekeeping` domain.
- Cleanup reports Engine Buildx cache retention as removed and retained cache export counts.
- Successful deploys still log the final WebUI URL and show it in the dashboard footer `Current`/`State` fields.
- Full Docker build detail remains in `Engine/Deploy/build.log`.

WebUI targets:
- Production builds Docker target `prod`, which runs `npm run build` in a build stage, copies only the static `build/` output into the runtime stage, and serves it without `node_modules` or Vite preview.
- Dev builds Docker target `dev`, which keeps Vite HMR available with the full Node dependency tree and skips production static build work.
- Dev HMR source edits should happen under `Engine/Services/webui-frontend/data/web-interface/`; the K3s dev pod hostPath-mounts that runtime copy.

## Runtime Start Order
K3s dependency order:

1. K3s `postgres-db` reconciles first among Borealis workloads and waits for StatefulSet readiness plus PVC binding.
2. `Engine.sh --network-mode public|local deploy` runs one-shot Engine schema setup from the current `site-worker` image as a K3s Job before API or scheduler reconciliation.
3. K3s `postgres-db` must pass readiness:
```text
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
```
4. `wireguard-tunnel` must create the Unix control socket.
5. K3s `remote-desktop-guacd` must accept ClusterIP TCP connections on port `4822`.
6. K3s `webui-frontend` must pass its Deployment readiness probe before Traefik is rendered with `BOREALIS_WEBUI_TRAFFIC_OWNER=k3s`. Once K3s owns the route, Compose `webui-frontend` is retired and stale containers are removed during deploy.
7. K3s `api-backend` must pass its Deployment readiness probe and expose HTTP `200` health through the `api-backend` ClusterIP Service.
8. K3s `job-scheduler` waits for its Deployment readiness probe after PostgreSQL schema initialization and API cutover reconcile. Its probe verifies PostgreSQL access; API-dependent queue work targets `api-backend.borealis.svc.cluster.local:5001`.
9. K3s `traefik-edge` starts after K3s API/WebUI readiness gates in `Engine.sh` and is reconciled immediately after API Service rollout.
10. K3s `traefik-edge` must pass Traefik ping healthcheck on the loopback `borealis-health` entrypoint.

Traefik is the public edge. API stays cluster-internal behind Traefik through the K3s `api-backend` Service. Production and dev WebUI are routed from Traefik to the K3s Service ClusterIP.

## Production vs Dev Mode
Production mode:
```sh
bash Engine.sh --network-mode local deploy prod
```

Production behavior:
- `BOREALIS_WEBUI_MODE=prod` is scoped to WebUI.
- K3s WebUI frontend serves built static UI.
- Traefik routes public HTTPS to the K3s WebUI ClusterIP, API ClusterIP Service, VNC loopback, and watched dynamic site-worker route files that target per-worker ClusterIP Services.

Dev mode:
```sh
bash Engine.sh --network-mode local deploy dev
```

Dev behavior:
- `BOREALIS_WEBUI_MODE=dev` is scoped to WebUI.
- WebUI frontend runs Vite HMR.
- Vite binds inside the pod on `0.0.0.0:8000` so the K3s `webui-frontend` Service can route to it. Health checks still target `127.0.0.1:8000` inside the pod.
- Browser HMR uses `wss://<engine-fqdn>/__vite_hmr` through K3s Traefik.
- Traefik still owns public HTTP/HTTPS and routes UI/API/WebSocket paths without changing its own upstream config.
- API, PostgreSQL, Traefik, guacd, and WireGuard stay running during a prod/dev mode flip unless their own image or shared runtime inputs changed.

Default deploy mode:
```sh
bash Engine.sh --network-mode local deploy
```

Equivalent to:
```sh
bash Engine.sh --network-mode local deploy prod
```

## Main Operator Commands
Deploy or redeploy production:
```sh
cd /opt/Borealis
bash Engine.sh --network-mode local deploy prod
```

Deploy or redeploy dev:
```sh
cd /opt/Borealis
bash Engine.sh --network-mode local deploy dev
```

Branch install or redeploy from raw launcher:
```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --network-mode local --repo-branch feature/containerize-all-borealis-services deploy prod
```

Update from a cloned checkout:
```sh
git pull --ff-only
bash Engine.sh --network-mode local deploy prod
```

Build and publish current Agent binaries, then replace outdated active site workers:
```sh
bash Engine.sh --redeploy-agent-binaries
```

Use `deploy dev` instead of `deploy prod` for development Engine stacks. Use `--network-mode public` instead of `local` for public/MSP-friendly Engines.

## Service Commands
Restart API backend:
```sh
bash Engine.sh --network-mode local --service api-backend restart
```

Restart WebUI frontend without rebuilding:
```sh
bash Engine.sh --network-mode local --service webui-frontend restart
```

Rebuild WebUI frontend in production mode:
```sh
bash Engine.sh --network-mode local --service webui-frontend rebuild prod
```

Rebuild WebUI frontend in dev mode:
```sh
bash Engine.sh --network-mode local --service webui-frontend rebuild dev
```

Reload Traefik edge:
```sh
bash Engine.sh --network-mode local --service traefik-edge reload
```

Restart PostgreSQL:
```sh
bash Engine.sh --network-mode local --service postgres-db restart
```

Restart guacd:
```sh
bash Engine.sh --network-mode local --service remote-desktop-guacd restart
```

Reconcile WireGuard tunnel state:
```sh
bash Engine.sh --network-mode local --service wireguard-tunnel reconcile
```

Generic service syntax:
```sh
bash Engine.sh --network-mode <public|local> --service <api-backend|job-scheduler|webui-frontend|traefik-edge|postgres-db|remote-desktop-guacd|wireguard-tunnel> <restart|rebuild|reload|reconcile> [prod|dev]
```

Action support:
| Action | Supported services | Effect |
| --- | --- | --- |
| `restart` | K3s `api-backend`, `postgres-db`, `webui-frontend`, `remote-desktop-guacd`, `traefik-edge`, and `wireguard-tunnel`; `job-scheduler` from CLI only | Restarts K3s API/WebUI/guacd/Traefik/WireGuard through Kubernetes Deployment rollout and K3s PostgreSQL through StatefulSet rollout. Server Info exposes K3s workload restarts except `job-scheduler` self-restart, which stays CLI-only. |
| `rebuild` | any Engine service | Rebuilds selected image, updates image manifest/env, and reconciles K3s-owned API/scheduler/PostgreSQL/WebUI/guacd/Traefik/WireGuard manifests as needed. |
| `reload` | `traefik-edge` only | Restarts the K3s Traefik edge Deployment after config/env changes. |
| `reconcile` | `wireguard-tunnel` only | Engine CLI runs `borealis-wireguard-control-client reconcile` inside the K3s tunnel pod. Queued Server Info recovery runs from K3s `job-scheduler` through the mounted WireGuard control socket. |

Server Info service rows show K3s-owned `api-backend`, `job-scheduler`, `postgres-db`, `remote-desktop-guacd`, `traefik-edge`, `webui-frontend`, and `wireguard-tunnel` through operator `GetWorkloadStatus` snapshots. Server worker rows merge K3s operator metrics before Docker metadata fallback, so K3s-owned site workers do not query Docker for status or stats.

Server Info service actions use a restricted subset of the command surface. The API backend writes a service-action work item, then K3s `job-scheduler` dispatches it. K3s-owned workload restarts that are safe to complete synchronously go through `borealis-operator`. K3s WireGuard reconcile/recovery runs through the mounted WireGuard control socket. `traefik-edge reload` is now an operator-routed restart of the known K3s Traefik Deployment, not a Docker helper. WebUI rebuilds are CLI-only through `Engine.sh --network-mode public|local --service webui-frontend rebuild prod|dev`. Server Info does not expose restart actions for K3s `job-scheduler`; use the Engine CLI for scheduler restart so the current queue item can complete before rollout starts.

## Direct Compose Commands
Use `Engine.sh` for all service operations. Direct Compose commands are now useful only to prove the Compose manifest is empty after Stage 11.

Confirm Compose owns no services:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  config --services
```

The command should return no service names. Use K3s commands and `Engine.sh --service ...` for operational work.

## Health Checks
Compose retirement status:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  config --services
```

API liveness:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- borealis-api-backend-go api-healthcheck
```

K3s API rollout:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/api-backend
```

WebUI liveness:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/webui-frontend -- borealis-webui-healthcheck
webui_ip="$(awk -F= '$1=="BOREALIS_WEBUI_UPSTREAM_HOST"{print $2}' Engine/Deploy/compose.env)"
curl -fsS "http://${webui_ip:-127.0.0.1}:8000/"
```

PostgreSQL readiness:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec postgres-db-0 -c postgres-db -- pg_isready -h 127.0.0.1 -p 5432 -U borealis -d borealis
```

guacd readiness:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/remote-desktop-guacd -- borealis-guacd-healthcheck
```

Traefik ping:
```sh
curl -fsS http://127.0.0.1:8082/ping
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/traefik-edge
```

Public edge reachability:
```sh
curl -Ik https://<engine-fqdn>/
```

WireGuard control socket and listener:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/wireguard-tunnel -- borealis-wireguard-healthcheck
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/wireguard-tunnel -- borealis-wireguard-control-client ping
sudo ss -lunp | grep ':30000'
sudo wg show borealis-wg
```

K3s baseline:
```sh
sudo systemctl is-active k3s
sudo systemctl is-active borealis-k3s-api-firewall
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get namespace borealis --show-labels
sudo iptables -C INPUT -p tcp --dport 6443 -j BOREALIS-K3S-API
```

K3s operator bridge:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/borealis-operator
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator list pods -n borealis
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator patch deployment/borealis-operator -n borealis
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator patch deployment/not-borealis -n borealis
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator get secrets -n borealis
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator list nodes
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- borealis-api-backend-go borealis-operator-healthcheck
```

K3s bridge workloads:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pvc postgres-data-postgres-db-0
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
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- sh -lc 'case "$BOREALIS_DATABASE_URL" in *@postgres-db.borealis.svc:5432/*) echo api-backend-db-url=k3s ;; *) exit 1 ;; esac'
```

## Logs
Container build log:
```text
Engine/Deploy/build.log
```

API backend logs:
```text
Engine/Services/api-backend/logs/engine.log
Engine/Services/api-backend/logs/error.log
Engine/Services/api-backend/logs/api.log
Engine/Services/api-backend/logs/<service>.log
```

Traefik logs:
```text
Engine/Services/traefik-edge/logs/
```

PostgreSQL logs use K3s pod stdout/stderr:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs statefulset/postgres-db -c postgres-db
```

WireGuard tunnel logs:
```text
Engine/Services/wireguard-tunnel/logs/
Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log
```

K3s WireGuard pod logs:
```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/wireguard-tunnel -c wireguard-tunnel
```

Guacd logs:
```text
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/remote-desktop-guacd
```

## Common Scenarios
API code changed:
```sh
bash Engine.sh --network-mode local --service api-backend rebuild prod
```

WebUI code changed, production:
```sh
bash Engine.sh --network-mode local --service webui-frontend rebuild prod
```

WebUI code changed, dev/HMR:
```sh
bash Engine.sh --network-mode local --service webui-frontend rebuild dev
```
Then verify the browser Network tab shows a connected `wss://<engine-fqdn>/__vite_hmr` websocket.

Traefik config changed:
```sh
bash Engine.sh --network-mode local --service traefik-edge reload
```

Database stuck or unhealthy:
```sh
bash Engine.sh --network-mode local --service postgres-db restart
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pvc postgres-data-postgres-db-0
```

WireGuard peers look stale:
```sh
bash Engine.sh --network-mode local --service wireguard-tunnel reconcile
```

Full safe redeploy:
```sh
bash Engine.sh --network-mode local deploy prod
```

## Operational Notes
- `Engine.sh --network-mode public|local deploy` is idempotent for unchanged inputs and keeps Compose retired when deploy manifest, env, image hashes, stored K3s input hashes, and live Kubernetes resource annotations already match.
- K3s baseline reconcile is idempotent for unchanged config: deploys do not reinstall K3s, restart K3s, delete cluster state, rotate secrets, or delete PVCs unless later migration stages explicitly add a controlled workflow.
- `borealis-operator` reconcile is idempotent for unchanged image, namespace, port, secret, RBAC, and image allowlists. Normal deploys apply fixed manifests, wait for rollout, and preserve the existing operator secret.
- K3s PostgreSQL/API/scheduler/WireGuard/WebUI/guacd reconcile is idempotent for unchanged images, mode, runtime owner IDs, ports, PVC settings, source paths, runtime-env hash, traffic-owner state, and profile caps. Unchanged saved hashes plus matching live annotations skip manifest apply and rollout without deleting PVCs or rerunning the Compose cutover import.
- Unchanged image hashes skip Docker builds.
- Service-specific `rebuild` updates image manifests and reconciles the known K3s workload instead of recreating Compose services.
- Service-specific API, WebUI, guacd, Traefik, and WireGuard rebuilds also reconcile the K3s baseline, operator, and workload manifests so pods follow the current image manifest without requiring a full deploy.
- `restart` does not rebuild images.
- `reload` is currently a K3s Traefik Deployment restart.
- `reconcile` is currently WireGuard-only.
- `--redeploy-agent-binaries` pauses scheduler after running work drains, keeps old site-worker Pods live while candidates start, uses stable ClusterIP Service selector for cutover, retains Traefik route files because ClusterIP does not change, then removes old Pods and resumes scheduler on new desired worker image.
- PostgreSQL is ClusterIP-only inside K3s and does not bind host `127.0.0.1:5432` after Stage 9.
- K3s WireGuard tunnel pod uses host networking with `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, and `no-new-privileges`. It does not run with full privileged mode.

## Troubleshooting Load Order
If `api-backend` does not start:
1. Check K3s `postgres-db` rollout and readiness.
2. Check K3s `wireguard-tunnel` started.
3. Check `remote-desktop-guacd` started.
4. Read `Engine/Services/api-backend/logs/error.log`.
5. Read `Engine/Deploy/build.log` if image build changed.

If `traefik-edge` returns `502`:
1. Check K3s `api-backend` rollout, Service endpoints, and in-pod healthcheck.
2. Check `BOREALIS_API_BACKEND_UPSTREAM_HOST`, `BOREALIS_API_BACKEND_UPSTREAM_PORT`, `BOREALIS_INTERNAL_API_BASE_URL`, `BOREALIS_WEBUI_TRAFFIC_OWNER`, and `BOREALIS_WEBUI_UPSTREAM_HOST` in `Engine/Deploy/compose.env`.
3. Check WebUI listener at the configured upstream host on port `8000`; after Stage 6 this is the K3s Service ClusterIP.
4. Check `Engine/Services/traefik-edge/logs/`.
5. Reload Traefik only after confirming backend listeners.

If WebSocket or Socket.IO fails:
1. Check API backend health.
2. Check Traefik routing and access logs.
3. Confirm browser is using same HTTPS origin.
4. Restart `api-backend` only if backend loop is wedged.

If remote desktop fails:
1. Check K3s `remote-desktop-guacd` pod.
2. Check `127.0.0.1:4822`.
3. Check `api-backend` VNC WebSocket proxy on `127.0.0.1:4823`.
4. Check WireGuard readiness for target agent.

If remote shell, Ansible, or tunnel-backed operations fail:
1. Check K3s `wireguard-tunnel`.
2. Run WireGuard reconcile.
3. Check `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log`.
4. Check target agent VPN logs.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Getting Started](../../Engine/deploying-the-engine.md)
    - [Engine Runtime](engine-runtime.md)
    - [Engine Log Access](../../Using%20the%20Platform/engine-log-management.md)
    - [Remote Shell](../../Using%20the%20Platform/remote-shell.md)

    ### Source and runtime layout

    Committed source lives under:
    ```text
    Data/Engine/Containers/
    ```

    Runtime output lives under:
    ```text
    Engine/
    ```

    Deploy state:
    ```text
    Engine/Deploy/compose.env
    Engine/Deploy/runtime.env
    Engine/Deploy/webui-frontend.env
    Engine/Deploy/image-manifest.json
    Engine/Deploy/deploy-manifest.json
    Engine/Deploy/k3s-baseline.sha256
    Engine/Deploy/k3s-longhorn.sha256
    Engine/Deploy/borealis-operator.sha256
    Engine/Deploy/k3s-postgres-db.sha256
    Engine/Deploy/k3s-postgres-schema.sha256
    Engine/Deploy/k3s-api-backend.sha256
    Engine/Deploy/k3s-job-scheduler.sha256
    Engine/Deploy/k3s-wireguard-tunnel.sha256
    Engine/Deploy/k3s-traefik-edge.sha256
    Engine/Deploy/k3s-webui-frontend.sha256
    Engine/Deploy/k3s-remote-desktop-guacd.sha256
    Engine/Deploy/k3s-bridge-workloads.sha256
    Engine/Deploy/build.log
    ```

    Service runtime state is intentionally sparse:
    ```text
    Engine/Services/api-backend/config
    Engine/Services/api-backend/logs
    Engine/Services/api-backend/secrets
    Engine/Services/api-backend/cache/Ansible
    Engine/Services/api-backend/cache/Aurora
    Engine/Services/traefik-edge/config
    Engine/Services/traefik-edge/env
    Engine/Services/traefik-edge/logs
    Engine/Services/traefik-edge/state
    Engine/Services/webui-frontend/data/web-interface
    Engine/Services/wireguard-tunnel/config
    Engine/Services/wireguard-tunnel/logs
    Engine/Services/wireguard-tunnel/secrets
    Engine/Services/wireguard-tunnel/run
    ```

    Retired Compose PostgreSQL paths under `Engine/Services/postgres-db/` may remain as a manual recovery artifact, but normal deploy does not use or delete them after Stage 9.

    Build cache, when Docker Buildx is available, lives under:
    ```text
    Engine/Deploy/cache/buildkit/<service>/<YYYYMMDDTHHMMSSZ>-<inputhash12>
    ```

    Operators should treat `Engine/` as generated runtime state. Edit committed source under `Data/Engine/Containers/`, then redeploy through `Engine.sh`. For live WebUI dev/HMR work, edit the seeded runtime WebUI source under `Engine/Services/webui-frontend/data/web-interface/`.

    ### Manifest files

    `Engine/Deploy/image-manifest.json` records:
    - image tag
    - input hash
    - Dockerfile path
    - build context
    - mode
    - timestamp

    `Engine/Deploy/deploy-manifest.json` records:
    - Compose project name
    - deploy mode
    - Compose file
    - Compose file hash
    - env file
    - env file hash
    - env settings hash excluding image tag and mode-scoped lines
    - service image tags and input hashes
    - changed services for the last deploy action
    - Compose action (`retired` after Stage 11)
    - selected deployment profile and tuned values
    - service list
    - deploy timestamp

    `Engine/Deploy/k3s-baseline.sha256` records the Borealis-owned K3s config hash for `/etc/rancher/k3s/config.yaml.d/10-borealis.yaml`. Kubernetes namespace and node annotations carry the same hash under `borealis.io/k3s-config-hash`.

    `Engine/Deploy/borealis-operator.sha256` records the operator manifest inputs: image tag, namespace, Service name, listen port, HMAC secret, and generated immutable image allowlists.

    `Engine/Deploy/k3s-api-backend.sha256` records Stage 7 API traffic-owner inputs: API image, deploy mode, namespace, runtime owner IDs, Service DNS, listen host, port, runtime-env hash, traffic owner, and profile resource caps.

    `Engine/Deploy/k3s-job-scheduler.sha256` records Stage 8 scheduler inputs: scheduler image, site-worker image, deploy mode, namespace, runtime owner IDs, runtime-env hash, API Service DNS target, K3s lifecycle mode, and profile resource caps.

    `Engine/Deploy/k3s-postgres-db.sha256` records Stage 9 PostgreSQL traffic-owner inputs: PostgreSQL image, deploy mode, namespace, runtime IDs, runtime-env hash, traffic owner, StorageClass, PVC size, generated Secret name, and profile PostgreSQL resource caps.

    `Engine/Deploy/k3s-postgres-schema.sha256` records the last successful K3s PostgreSQL schema initializer input. Full deploy skips the schema Job when this schema hash and live PostgreSQL StatefulSet annotation still match.

    `Engine/Deploy/k3s-wireguard-tunnel.sha256` records Stage 10 WireGuard traffic-owner inputs: WireGuard image, namespace, runtime group, listen port, runtime state path, host timezone, device path, control socket contract, and network capabilities.

    `Engine/Deploy/k3s-traefik-edge.sha256` records Stage 11 Traefik traffic-owner inputs: Traefik image, namespace, runtime group, health port, runtime state path, network mode, FQDN aliases, ACME/local CA settings, API/WebUI upstreams, trusted proxy settings, host timezone, and host-network security contract.

    `Engine/Deploy/k3s-webui-frontend.sha256` records Stage 4 WebUI bridge workload inputs: WebUI image, deploy mode, namespace, runtime owner IDs, port, runtime source path, Vite scratch paths, traffic owner, host timezone, and profile resource caps.

    `Engine/Deploy/k3s-remote-desktop-guacd.sha256` records Stage 4 guacd bridge workload inputs: guacd image, namespace, runtime owner IDs, port, host timezone, and profile resource caps.

    `Engine/Deploy/k3s-bridge-workloads.sha256` records the aggregate Stage 4 bridge state after both WebUI and guacd reconcile. Use the service-specific hash files to identify which bridge workload changed.

    Use these files to confirm whether source changes are actually deployed.

    - Edit Engine container, K3s workload, and retired Compose source under `Data/Engine/Containers/`.
    - Do not edit generated runtime under `Engine/` except when reading logs/manifests.
    - Use `Engine.sh --network-mode public|local deploy prod|dev` for full stack deployment.
    - Use `Engine.sh --network-mode public|local --service ...` for scoped service actions.
    - Validate launcher syntax after changing shell scripts:
    ```sh
    bash -n Engine.sh
    docker compose --env-file Data/Engine/Containers/compose.env.example -f Data/Engine/Containers/compose.yaml config
    python3 Data/Engine/Containers/check-compose-policy.py
    ```
    - Validate Stage 1-11 K3s runtime only on a host where installing/reconciling K3s is acceptable:
    ```sh
    sudo bash Engine.sh --network-mode local deploy prod
    sudo bash Engine.sh --network-mode local deploy prod
    sudo systemctl is-active k3s
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/borealis-operator
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator patch deployment/borealis-operator -n borealis
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator patch deployment/not-borealis -n borealis
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator get secrets -n borealis
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml auth can-i --as=system:serviceaccount:borealis:borealis-operator list nodes
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/job-scheduler
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pvc postgres-data-postgres-db-0
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/traefik-edge
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/webui-frontend
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/remote-desktop-guacd
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get service postgres-db webui-frontend remote-desktop-guacd
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- borealis-api-backend-go borealis-operator-healthcheck
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- sh -lc 'case "$BOREALIS_DATABASE_URL" in *@postgres-db.borealis.svc:5432/*) echo api-backend-db-url=k3s ;; *) exit 1 ;; esac'
    sudo iptables -C INPUT -p tcp --dport 6443 -j BOREALIS-K3S-API
    ```
    - Update this page when adding a service, port, volume, service action, or load-order dependency.
