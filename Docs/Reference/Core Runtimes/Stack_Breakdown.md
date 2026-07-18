# Borealis Docker Stack Breakdown

Explain the Borealis Engine Docker Compose stack, service ownership, startup order, runtime paths, and common operator commands.

## Scope
- Linux Engine only.
- Docker Engine plus Docker Compose plugin.
- No Docker Desktop.
- Single-node K3s baseline is reconciled by `Engine.sh`; Docker Compose remains workload source of truth until explicit workload cutover.
- Compose project name: `borealis-engine`.
- Compose source of truth: `Data/Engine/Containers/compose.yaml`.
- Runtime state: `Engine/`.

## Stack Services
| Service | Container | Main responsibility | Host network endpoint |
| --- | --- | --- | --- |
| `docker-proxy` | `borealis-engine-docker-proxy` | Read-only Docker API proxy for Engine container status reads and Sites site-worker stats | `127.0.0.1:2375` |
| `postgres-db` | `borealis-engine-postgres-db` | PostgreSQL database and persisted DB state | `127.0.0.1:5432` |
| `wireguard-tunnel` | `borealis-engine-wireguard-tunnel` | WireGuard interface, peer config, firewall/routing, constrained control socket | UDP `30000`, interface `borealis-wg` |
| `remote-desktop-guacd` | `borealis-engine-remote-desktop-guacd` | VNC-only Apache Guacamole guacd runtime | `127.0.0.1:4822` |
| `webui-frontend` | `borealis-engine-webui-frontend` | Production static WebUI or dev Vite HMR | `127.0.0.1:8000` |
| `api-backend` | `borealis-engine-api-backend` | Go API backend, live operator sessions, VNC session broker, workflow/runtime APIs | `127.0.0.1:5000` |
| `site-worker-orchestrator` | `borealis-engine-site-worker-orchestrator` | Protected Docker boundary for site-worker containers and allowlisted Engine service actions | Unix socket |
| `job-scheduler` | `borealis-engine-job-scheduler` | Go scheduler-manager mode from the api-backend binary, scheduled tick loop, Postgres work leases, service action queueing, and site-worker reconciliation | Internal only |
| `traefik-edge` | `borealis-engine-traefik-edge` | Public HTTP/HTTPS edge, ACME, UI/API/Socket.IO/VNC routing | `80`, `443`, health `127.0.0.1:8082` |

Most Engine containers use `network_mode: host`. Loopback assumptions are intentional. `docker-proxy` uses bridge networking with a loopback-only host port so the Docker API proxy is not exposed publicly.

`site-worker-orchestrator` is the only write boundary with `/var/run/docker.sock`. `job-scheduler` talks to it over `/opt/Borealis/Engine/Services/site-worker-orchestrator/run/orchestrator.sock` using the Engine internal HMAC token. `api-backend` and `job-scheduler` do not mount the Docker socket in container mode. They read container status and site-worker stats through `docker-proxy` with `CONTAINERS=1` and `POST=0`, then fall back to job-scheduler snapshots if the proxy is unavailable. Dynamic onboarding workers are launched as `site-worker-<uuid>` containers with no Docker socket, site id labels, `borealis.created_by=site-worker-orchestrator`, non-root user, `no-new-privileges`, dropped capabilities, read-only root filesystem, tmpfs `/tmp`, PID/memory/CPU caps, read-only Engine secret/config mounts, and a default idle timeout of 300 seconds.

## Least-Privilege Runtime
`Engine.sh` creates or repairs a `borealis-engine` system user/group with stable numeric IDs and writes `BOREALIS_ENGINE_RUNTIME_OWNER_UID:GID` into `Engine/Deploy/compose.env`. It also detects the host Docker socket GID as `BOREALIS_DOCKER_SOCKET_GID` so only socket-owning services get explicit supplemental group access.

Default Compose policy:
- `api-backend`, `job-scheduler`, `site-worker-orchestrator`, `webui-frontend`, and `docker-proxy` run as `borealis-engine`.
- `remote-desktop-guacd` runs as `borealis-engine`, binds guacd to `127.0.0.1:4822`, has no host bind mounts, and has no Docker socket access.
- `postgres-db` runs as the official PostgreSQL non-root UID by default so existing database state under `Engine/Services/postgres-db/state` keeps compatible ownership. `Engine.sh` writes that UID into `BOREALIS_POSTGRES_RUNTIME_UID` and uses the Borealis runtime group for shared host-side access.
- Hardened Engine services declare `no-new-privileges`, `cap_drop: [ALL]`, read-only root filesystem, tmpfs `/tmp`, `pids_limit`, `mem_limit`, and `cpus`.
- `docker-proxy` also gets tmpfs `/run` for HAProxy pid state under a read-only root filesystem.
- `traefik-edge` runs as UID `0` with the Borealis runtime group because Docker does not grant effective low-port bind capability to a non-root user under host networking. It drops all default capabilities and adds only `NET_BIND_SERVICE` for ports `80` and `443`.
- `wireguard-tunnel` remains explicit root exception because WireGuard interface setup needs `/dev/net/tun`, `NET_ADMIN`, and `NET_RAW`. It runs as UID `0` with the Borealis runtime group so dropped DAC capabilities do not block its service-local control socket. It still uses `no-new-privileges`, dropped default capabilities, read-only root filesystem, a writable service-local run directory, and resource limits.
- `docker-proxy` has read-only Docker socket access. `site-worker-orchestrator` has write Docker socket access. No other static service mounts the Docker socket.

Writable bind mounts are service runtime paths under `Engine/Services/`. `Engine.sh` chowns those paths to the runtime owner during deploy while preserving stricter modes for API secrets, WireGuard secrets, Traefik ACME storage, and PostgreSQL database state.

WireGuard key and listener config files are `0640 borealis-engine:borealis-engine`, and the WireGuard config directory is `0750`. The API backend writes those files as the runtime owner, while `wireguard-tunnel` reads them as UID `0` with only the Borealis runtime group and no DAC override capability.

`Engine/Deploy/runtime.env` and `Engine/Deploy/compose.env` are owned by `root:borealis-engine` with mode `0640`. They contain runtime secrets but must be readable by `site-worker-orchestrator`: dynamic `site-worker-*` launches use `runtime.env` as the Docker env file, and service snapshot reads use `compose.env` for Compose interpolation. `webui-frontend.env` remains `0600`.

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

# Reload is enough for later env-only trust list changes.
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32 bash Engine.sh --network-mode public --service traefik-edge reload prod
```

Validate with:
```sh
rg "POST /api/agent/enroll/request" Engine/Services/api-backend/logs/api.log
rg "enrollment rate limited key=ip" Engine/Services/api-backend/logs/device_enrollment.log
```

## Volume Bindings
`api-backend`:
```text
Engine/Services/api-backend -> /opt/Borealis/Engine/Services/api-backend
Engine/Services/traefik-edge/config -> /opt/Borealis/Engine/Services/traefik-edge/config
Engine/Services/traefik-edge/env    -> /opt/Borealis/Engine/Services/traefik-edge/env
Engine/Services/traefik-edge/logs   -> /opt/Borealis/Engine/Services/traefik-edge/logs
Engine/Services/traefik-edge/state  -> /opt/Borealis/Engine/Services/traefik-edge/state
Engine/Services/wireguard-tunnel/config  -> /opt/Borealis/Engine/Services/wireguard-tunnel/config
Engine/Services/wireguard-tunnel/run     -> /opt/Borealis/Engine/Services/wireguard-tunnel/run
Engine/Services/wireguard-tunnel/secrets -> /opt/Borealis/Engine/Services/wireguard-tunnel/secrets
```

`api-backend` does not mount the whole `Engine/Services` tree. It receives its own runtime plus specific Traefik and WireGuard paths needed for edge settings and tunnel control. It does not mount the Docker socket in container mode; Server Info and Sites read status through `docker-proxy` or job-scheduler snapshots, and service actions are queued for `job-scheduler` to hand to `site-worker-orchestrator`.

`docker-proxy`:
```text
${BOREALIS_DOCKER_SOCKET_PATH:-/var/run/docker.sock} -> /var/run/docker.sock:ro
127.0.0.1:2375 -> 2375
```

The proxy grants only Docker container read APIs, including status and site-worker stats, and denies POST operations. Do not expose `2375` beyond loopback.

`site-worker-orchestrator`:
```text
Engine/Deploy -> /opt/Borealis/Engine/Deploy:ro
Engine/Services/api-backend/cache -> /opt/Borealis/Engine/Services/api-backend/cache
Engine/Services/api-backend/logs/site-workers -> /opt/Borealis/Engine/Services/api-backend/logs/site-workers
Engine/Services/api-backend/secrets -> /opt/Borealis/Engine/Services/api-backend/secrets:ro
Engine/Services/site-worker-orchestrator/run -> /opt/Borealis/Engine/Services/site-worker-orchestrator/run
${BOREALIS_DOCKER_SOCKET_PATH:-/var/run/docker.sock} -> /var/run/docker.sock
```

The orchestrator listens only on the Unix socket path from `BOREALIS_SITE_WORKER_ORCHESTRATOR_SOCKET`. It accepts strict JSON requests signed with `X-Borealis-Internal-Token`, launches only images allowed by `BOREALIS_SITE_WORKER_IMAGE` plus optional `BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST`, adds non-root/read-only/resource-limit Docker flags itself, and rejects arbitrary Docker privileges, devices, capabilities, namespaces, command overrides, and environment overrides. Dynamic `site-worker-*` containers receive API config and secrets read-only, API cache and site-worker logs read-write, no Docker socket, and no Traefik config mount. Server Info does not expose an operator restart action for this service.

`job-scheduler`:
```text
Engine/Deploy -> /opt/Borealis/Engine/Deploy:ro
Engine/Services/api-backend/cache -> /opt/Borealis/Engine/Services/api-backend/cache
Engine/Services/api-backend/config -> /opt/Borealis/Engine/Services/api-backend/config:ro
Engine/Services/api-backend/logs -> /opt/Borealis/Engine/Services/api-backend/logs
Engine/Services/api-backend/secrets -> /opt/Borealis/Engine/Services/api-backend/secrets:ro
Engine/Services/site-worker-orchestrator/run -> /opt/Borealis/Engine/Services/site-worker-orchestrator/run
Engine/Services/traefik-edge/config/dynamic -> /opt/Borealis/Engine/Services/traefik-edge/config/dynamic
```

`job-scheduler` is the single writer for site-worker Traefik route files. It writes and retires `site-worker-<worker_guid>.yml` files under the dynamic Traefik config directory, then asks `site-worker-orchestrator` to launch or stop the corresponding Docker container. `site-worker-*` containers set `BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES=0` so legacy Python route helpers keep database state only and do not write files.

`postgres-db`:
```text
Engine/Services/postgres-db/state -> /var/lib/postgresql/data
Engine/Services/postgres-db/run   -> /var/run/borealis
```

`traefik-edge`:
```text
Engine/Services/traefik-edge -> /opt/Borealis/Engine/Services/traefik-edge
```

Traefik static config renders to `Engine/Services/traefik-edge/config/traefik.yml`. Core Borealis routes render to `Engine/Services/traefik-edge/config/dynamic/core.yml`. Site-worker route files use `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml` so Traefik can hotload worker route adds/removals from the watched dynamic directory. Externally Accessible deployments store ACME state in `state/acme.json`; Internal-Only deployments store Borealis local CA and leaf certificate material under `state/local-ca/` and `state/local-certs/`.

`remote-desktop-guacd`:
No host bind mounts. Guacd writes transient in-container logs under `/tmp/borealis-guacd-logs`, and operator diagnostics should use Docker logs for `borealis-engine-remote-desktop-guacd`.

`wireguard-tunnel`:
```text
Engine/Services/wireguard-tunnel -> /opt/Borealis/Engine/Services/wireguard-tunnel
```

`webui-frontend`:
```text
Engine/Services/webui-frontend/data/web-interface/src        -> /opt/Borealis/Data/Engine/web-interface/src:ro
Engine/Services/webui-frontend/data/web-interface/public     -> /opt/Borealis/Data/Engine/web-interface/public:ro
Engine/Services/webui-frontend/data/web-interface/Unit_Tests -> /opt/Borealis/Data/Engine/web-interface/Unit_Tests:ro
Engine/Services/webui-frontend/data/web-interface/index.html -> /opt/Borealis/Data/Engine/web-interface/index.html:ro
Engine/Services/webui-frontend/data/web-interface/package.json -> /opt/Borealis/Data/Engine/web-interface/package.json:ro
Engine/Services/webui-frontend/data/web-interface/tsconfig.json -> /opt/Borealis/Data/Engine/web-interface/tsconfig.json:ro
Engine/Services/webui-frontend/data/web-interface/vite.config.mts -> /opt/Borealis/Data/Engine/web-interface/vite.config.mts:ro
```

`Engine.sh` seeds `Engine/Services/webui-frontend/data/web-interface/` from committed WebUI source when the runtime copy is missing. It does not overwrite an existing runtime copy during normal deploys, so dev-mode Vite HMR edits survive rebuilds. The WebUI container reads that runtime copy but writes Vite cache under `/tmp`; edit files from the host, not from inside the container. Set `BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE=1` before deploy to discard and reseed the runtime WebUI source from committed source.

Borealis uses host bind mounts for runtime ownership clarity, not named volumes as a security boundary. Security comes from explicit narrow targets, read-only flags, non-root users, dropped capabilities, and one-writer ownership. Replacing a broad bind with a named volume does not make another container's write access safer by itself.

## Deploy Order
`Engine.sh --network-mode public|local deploy [prod|dev]` performs these phases:

1. Parse launch options.
2. If repo/release/branch options were supplied, sync the repository and re-exec installed `Engine.sh`.
3. Install or verify Engine dependencies.
4. Reconcile single-node K3s baseline. Install K3s only when missing, apply Borealis K3s config, apply IPv4 TCP `6443` firewall guard, verify service/kubeconfig/node/container-runtime state, disable bundled Traefik/ServiceLB, and create the `borealis` namespace.
5. Check for host PostgreSQL conflict on `127.0.0.1:5432`.
6. Create or repair `borealis-engine` runtime identity, then create service runtime tree under `Engine/Services/`.
7. Seed runtime WebUI source under `Engine/Services/webui-frontend/data/web-interface/` when missing.
8. Prune empty legacy runtime paths.
9. Resolve Engine FQDN, network mode, and certificate mode. Public resolves ACME email; Local generates or renews Borealis local CA/leaf certificate material.
10. Detect sizing profile from vCPU/RAM and render `Engine/Deploy/runtime.env` for shared container runtime settings, mode-scoped `webui-frontend.env`, and `Engine/Deploy/compose.env` for Compose interpolation plus profile-managed DB/site-worker tuning.
11. Compute service input hashes from each service's declared source, Dockerfile, build context, target mode, and dependency inputs.
12. Build changed local images as `borealis-engine/<service>:sha-<hash>`.
13. Write `Engine/Deploy/image-manifest.json`.
14. Re-render `compose.env` with resolved image tags while keeping service runtime env files free of image tag variables.
15. Compare compose/env/image hashes against `Engine/Deploy/deploy-manifest.json`.
16. Skip Compose if nothing changed and all containers are running.
17. Run scoped Compose `up -d --no-deps --no-build <service...>` when only service images changed or when switching prod/dev WebUI mode.
18. Run full Compose `up -d --no-build` when compose config, shared runtime env, or container state requires it.
19. Write `Engine/Deploy/deploy-manifest.json`.
20. Prune inactive Docker images, Docker builder cache, and Engine Buildx cache exports older than 7 days after successful reconciliation.

Build output follows `Engine.sh` service domains. `docker-proxy` is an external image and is not locally built.
| Domain | Item |
| --- | --- |
| Frontend | WebUI Frontend |
| Backend | API Backend |
| Backend | Job Scheduler |
| Backend | Site Worker Orchestrator |
| Backend | Site Worker |
| Backend | Guacamole Remote Desktop |
| Networking | Traefik Reverse Proxy |
| Networking | WireGuard Server |
| Database | PostgreSQL DB |
| Reconciliation | K3s Baseline |

Build domains are not the same as runtime dependency order.

## Local Build Behavior
Borealis-built Engine images are local in this pass. `docker-proxy` is pulled from GHCR as `ghcr.io/tecnativa/docker-socket-proxy:v0.4.2`; no Borealis image push or GHCR workflow is used.

Image naming:
```text
borealis-engine/<service>:sha-<inputhash12>
```

Build cache:
- Docker Buildx uses retained `Engine/Deploy/cache/buildkit/<service>/<YYYYMMDDTHHMMSSZ>-<inputhash12>` exports when available.
- Hosts without usable Buildx fall back to `DOCKER_BUILDKIT=1 docker build`.
- Successful Buildx builds write a full `mode=max` cache export for the service, and successful deploys prune inactive Docker images with `docker image prune -a`, clear Docker builder cache with `docker builder prune --all`, and delete whole Engine Buildx cache export directories older than 7 days. Set `BOREALIS_SKIP_DOCKER_PRUNE=1` to skip this cleanup on a shared Docker host.
- `api-backend` keeps repo-root build context because it packages `Data/Agent` and `Agent.exe`.
- `api-backend` uses an Alpine runtime image with the Go API binary plus `ca-certificates`, `git`, and `tzdata`. WireGuard command execution belongs to `wireguard-tunnel` through its control socket.
- `job-scheduler` and `site-worker-orchestrator` use the same Alpine scheduler image. `job-scheduler` runs the queue/reconcile mode without Docker socket access. `site-worker-orchestrator` runs the Docker-boundary mode with Bash/Python for detached `Engine.sh` service-action helpers, Docker CLI, Docker Compose plugin, `ca-certificates`, and `tzdata`.
- Service input hashes come from declared build inputs, not the repo-wide Git commit. A WebUI-only commit should not invalidate `api-backend` or `job-scheduler`.
- `api-backend` and `job-scheduler` share the Go api-backend binary. `Engine.sh` builds that binary only when one of those images needs a Docker rebuild, then reuses it for the rest of that deploy pass.
- `site-worker` is built as a local image but may not have a running container. Deploy cleanup protects the current site-worker image and removes stale site-worker tags only when no container still references them.
- `webui-frontend`, `traefik-edge`, `postgres-db`, `remote-desktop-guacd`, and `wireguard-tunnel` use service-local build contexts.
- Service-local build contexts carry their own `.dockerignore` files so `node_modules`, WebUI build output, Python bytecode, pytest caches, logs, and local test output stay out of image contexts.
- Deploy mode is part of the image hash only for services with explicit mode targets, currently `webui-frontend`. Switching between prod and dev should not make PostgreSQL, guacd, WireGuard, Traefik, or the API image appear changed unless their own inputs changed.
- `compose.env` carries image tags, stable env-file paths, runtime UID/GID, Docker socket GID, hardware deployment profile metadata, Engine access profile metadata, certificate paths, DB pool values, PostgreSQL startup settings, profile-managed resource caps, and site-worker scheduled work-item slots.
- `runtime.env` is shared by API, PostgreSQL, guacd, and WireGuard. It intentionally excludes image tag variables and keeps stable production WebUI defaults so one image or mode change does not mutate every container's environment.
- `webui-frontend.env` overrides shared runtime settings with the requested `BOREALIS_WEBUI_MODE`. Switching `prod`/`dev` should recreate only `webui-frontend` when all containers are already running.
- Traefik always routes the WebUI service to `127.0.0.1:8000`; the production static server and Vite HMR both bind that same loopback port.

Deploy output:
- `Engine.sh` renders a live ANSI deployment dashboard by default.
- The dashboard title shows `Production` or `Development`, the Engine network mode, the detected sizing `Profile`, and the active build log path.
- Service rows start as `Pending...` and update in place as deploy stages run.
- Service rows render in one table with `Domain`, `Item`, `Status`, and `Last Status Update` columns.
- `Last Status Update` uses a human-readable local timestamp such as `July 11th 2026 @ 3:03PM`.
- Domains include `Frontend`, `Backend`, `Networking`, `Database`, `Reconciliation`, `Housekeeping`, and `Complete`.
- Item names are friendly display labels such as `API Backend`, `Job Scheduler`, `Site Worker Orchestrator`, `Traefik Reverse Proxy`, `WireGuard Server`, and `PostgreSQL DB`.
- Service rows use compact status values such as `Up-to-Date`, `Building Go binary`, `(Re)Building Container Image`, `Ready - Image (Re)Built`, `Starting`, `Running`, `Running - Healthy`, `Reconciling Stack`, `Stack Reconciled`, or `Complete`.
- Shared build-artifact or image-reuse relationships appear only in transient status text. For example, the `Job Scheduler` row may show `[Shares API Backend Image] -> (Re)Building Container Image`, and the `Site Worker Orchestrator` row may show `[Shares Job Scheduler Image] -> Ready - Shared Image`. Runtime health updates later replace those sharing notes with `Starting`, `Running`, or `Running - Healthy`.
- Database schema setup updates the `PostgreSQL DB` row with table-level progress such as `Ensuring Table "devices" Exists`, writes each table progress line to `Engine/Deploy/build.log`, then returns the row to Docker health status after maintenance completes.
- Compose status uses the `Reconciliation` domain. Compose uses `Reconciling <service...>` for scoped service updates and `Reconciling Stack` only when shared Compose metadata must be applied. Services are not bulk-marked as `Starting` before Compose runs; after Compose exits, Engine polls transient Docker states and updates only rows whose runtime state actually changes.
- Image/cache pruning uses the `Housekeeping` domain.
- Cleanup reports Engine Buildx cache retention as removed and retained cache export counts.
- Successful deploys finish by updating the `Complete` domain with the WebUI URL.
- Full Docker build detail remains in `Engine/Deploy/build.log`.

WebUI targets:
- Production builds Docker target `prod`, which runs `npm run build` in a build stage, copies only the static `build/` output into the runtime stage, and serves it without `node_modules` or Vite preview.
- Dev builds Docker target `dev`, which keeps Vite HMR available with the full Node dependency tree and skips production static build work.
- Dev HMR source edits should happen under `Engine/Services/webui-frontend/data/web-interface/`; Compose bind-mounts that runtime copy into the WebUI container.

## Runtime Start Order
Compose dependency order:

1. `postgres-db`, `wireguard-tunnel`, `remote-desktop-guacd`, and `webui-frontend` can start independently.
2. `Engine.sh --network-mode public|local deploy` starts `postgres-db` first and runs one-shot Engine schema setup from the current `site-worker` image before API or scheduler reconciliation.
3. `postgres-db` must pass healthcheck:
```text
pg_isready -h 127.0.0.1 -p 5432 -U "$POSTGRES_USER" -d "$POSTGRES_DB"
```
4. `wireguard-tunnel` must create the Unix control socket.
5. `remote-desktop-guacd` must accept loopback TCP connections on `127.0.0.1:4822`.
6. `webui-frontend` must serve `/` on `127.0.0.1:8000` in prod and dev.
7. `api-backend` waits for:
```text
postgres-db: service_healthy
wireguard-tunnel: service_healthy
remote-desktop-guacd: service_healthy
```
8. `api-backend` must return HTTP `200` from `http://127.0.0.1:5000/health`.
9. `site-worker-orchestrator` waits for `api-backend` and `postgres-db`, owns the Docker socket, and must answer its Unix-socket healthcheck.
10. `job-scheduler` waits for `api-backend`, `postgres-db`, and `site-worker-orchestrator`.
11. `traefik-edge` waits for:
```text
api-backend: service_healthy
webui-frontend: service_healthy
```
12. `traefik-edge` must pass Traefik ping healthcheck on the loopback `borealis-health` entrypoint.

Traefik is the public edge. API and WebUI stay on loopback behind Traefik.

## Production vs Dev Mode
Production mode:
```sh
bash Engine.sh --network-mode local deploy prod
```

Production behavior:
- `BOREALIS_WEBUI_MODE=prod` is scoped to WebUI.
- WebUI frontend serves built static UI.
- Traefik routes public HTTPS to stable loopback services.

Dev mode:
```sh
bash Engine.sh --network-mode local deploy dev
```

Dev behavior:
- `BOREALIS_WEBUI_MODE=dev` is scoped to WebUI.
- WebUI frontend runs Vite HMR.
- Vite listens on loopback `127.0.0.1:8000`.
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

Use `deploy dev` instead of `deploy prod` for development Engine stacks. Use `--network-mode public` instead of `local` for public/MSP-friendly Engines.

## Service Commands
Restart API backend:
```sh
bash Engine.sh --network-mode local --service api-backend restart
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
bash Engine.sh --network-mode <public|local> --service <docker-proxy|api-backend|site-worker-orchestrator|job-scheduler|webui-frontend|traefik-edge|postgres-db|remote-desktop-guacd|wireguard-tunnel> <restart|rebuild|reload|reconcile> [prod|dev]
```

Action support:
| Action | Supported services | Effect |
| --- | --- | --- |
| `restart` | any Engine service | Runs `docker compose restart <service>` after refreshing runtime env. |
| `rebuild` | any Engine service | Rebuilds selected image, updates image manifest/env, recreates service with `up -d --no-deps`. |
| `reload` | `traefik-edge` only | Restarts Traefik after config/env changes. |
| `reconcile` | `wireguard-tunnel` only | Runs `borealis-wireguard-control-client reconcile` inside tunnel container. |

Server Info service actions use the same command surface. The API backend writes a service-action work item, then `job-scheduler` asks `site-worker-orchestrator` to launch a short-lived helper container from the scheduler image with `/opt/Borealis` and the Docker socket mounted while the API returns immediately. The helper is the remaining broad bind-mount exception because `Engine.sh --service` needs the host deploy script, Compose source, image/deploy manifests, service runtime paths, and Docker socket to run scoped restart/rebuild/reload/reconcile actions. The helper still uses `no-new-privileges`, dropped capabilities, read-only root filesystem, tmpfs `/tmp`, and resource limits. Server Info does not expose an operator restart action for `site-worker-orchestrator`.

## Direct Compose Commands
Use `Engine.sh` when possible. Direct Compose commands are useful for read-only inspection or emergency operations.

Base Compose command:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  ps
```

List containers:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  ps
```

Tail service logs:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  logs -f api-backend
```

Restart one service directly:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  restart api-backend
```

Avoid direct `docker compose down` during normal operations. It stops all Engine services, including PostgreSQL and WireGuard.

## Health Checks
Compose health/status:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  ps
```

API liveness:
```sh
curl -fsS http://127.0.0.1:5000/health
```

Container API healthcheck:
```sh
docker exec borealis-engine-api-backend borealis-api-backend-go api-healthcheck
```

WebUI liveness:
```sh
curl -fsS http://127.0.0.1:8000/
```

PostgreSQL readiness:
```sh
pg_isready -h 127.0.0.1 -p 5432 -U borealis -d borealis
```

guacd readiness:
```sh
docker exec borealis-engine-remote-desktop-guacd borealis-guacd-healthcheck
```

Traefik ping:
```sh
docker exec borealis-engine-traefik-edge traefik healthcheck --ping=true --ping.entryPoint=borealis-health --entryPoints.borealis-health.address=127.0.0.1:8082
```

Public edge reachability:
```sh
curl -Ik https://<engine-fqdn>/
```

WireGuard control socket and listener:
```sh
docker exec borealis-engine-wireguard-tunnel borealis-wireguard-healthcheck
docker exec borealis-engine-wireguard-tunnel borealis-wireguard-control-client ping
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

PostgreSQL logs use Docker stdout/stderr for `borealis-engine-postgres-db`.

WireGuard tunnel logs:
```text
Engine/Services/wireguard-tunnel/logs/
Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log
```

Guacd logs:
```text
docker logs borealis-engine-remote-desktop-guacd
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

Traefik config changed:
```sh
bash Engine.sh --network-mode local --service traefik-edge reload
```

Database stuck or unhealthy:
```sh
bash Engine.sh --network-mode local --service postgres-db restart
docker compose --project-name borealis-engine --env-file /opt/Borealis/Engine/Deploy/compose.env -f /opt/Borealis/Data/Engine/Containers/compose.yaml ps postgres-db
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
- `Engine.sh --network-mode public|local deploy` is idempotent for unchanged inputs and skips Compose when deploy manifest, env, image hashes, and running containers already match.
- K3s baseline reconcile is idempotent for unchanged config: deploys do not reinstall K3s, restart K3s, delete cluster state, rotate secrets, or delete PVCs unless later migration stages explicitly add a controlled workflow.
- Unchanged image hashes skip Docker builds.
- Service image changes use scoped Compose `up -d --no-deps --no-build <service...>` when compose config and non-image env settings are unchanged.
- Service-specific `rebuild` uses `--no-deps --no-build`, so dependent services are not intentionally restarted and Compose does not rebuild images Borealis already built.
- `restart` does not rebuild images.
- `reload` is currently a Traefik restart.
- `reconcile` is currently WireGuard-only.
- PostgreSQL uses host networking and must not conflict with host PostgreSQL on `127.0.0.1:5432`.
- WireGuard tunnel container uses host networking with `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, and `no-new-privileges`. It does not run with full Compose privileged mode.

## Troubleshooting Load Order
If `api-backend` does not start:
1. Check `postgres-db` health.
2. Check `wireguard-tunnel` started.
3. Check `remote-desktop-guacd` started.
4. Read `Engine/Services/api-backend/logs/error.log`.
5. Read `Engine/Deploy/build.log` if image build changed.

If `traefik-edge` returns `502`:
1. Check `api-backend` health on `127.0.0.1:5000`.
2. Check WebUI listener on `127.0.0.1:8000`.
3. Check `Engine/Services/traefik-edge/logs/`.
4. Reload Traefik only after confirming backend listeners.

If WebSocket or Socket.IO fails:
1. Check API backend health.
2. Check Traefik routing and access logs.
3. Confirm browser is using same HTTPS origin.
4. Restart `api-backend` only if backend loop is wedged.

If remote desktop fails:
1. Check `remote-desktop-guacd` container.
2. Check `127.0.0.1:4822`.
3. Check `api-backend` VNC WebSocket proxy on `127.0.0.1:4823`.
4. Check WireGuard readiness for target agent.

If remote shell, Ansible, or tunnel-backed operations fail:
1. Check `wireguard-tunnel`.
2. Run WireGuard reconcile.
3. Check `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log`.
4. Check target agent VPN logs.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Getting Started](../../Engine/deploying-the-engine.md)
    - [Engine Runtime](engine-runtime.md)
    - [Engine Log Management](../../Using%20the%20Platform/engine-log-management.md)
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
    Engine/Deploy/build.log
    ```

    Service runtime state is intentionally sparse:
    ```text
    Engine/Services/api-backend/config
    Engine/Services/api-backend/logs
    Engine/Services/api-backend/secrets
    Engine/Services/api-backend/cache/Ansible
    Engine/Services/api-backend/cache/Aurora
    Engine/Services/postgres-db/state
    Engine/Services/postgres-db/run
    Engine/Services/traefik-edge/config
    Engine/Services/traefik-edge/env
    Engine/Services/traefik-edge/logs
    Engine/Services/traefik-edge/state
    Engine/Services/webui-frontend/data/web-interface
    Engine/Services/site-worker-orchestrator/run
    Engine/Services/wireguard-tunnel/config
    Engine/Services/wireguard-tunnel/logs
    Engine/Services/wireguard-tunnel/secrets
    Engine/Services/wireguard-tunnel/run
    ```

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
    - Compose action (`up`, `up-scoped`, or `skipped`)
    - selected deployment profile and tuned values
    - service list
    - deploy timestamp

    `Engine/Deploy/k3s-baseline.sha256` records the Borealis-owned K3s config hash for `/etc/rancher/k3s/config.yaml.d/10-borealis.yaml`. Kubernetes namespace and node annotations carry the same hash under `borealis.io/k3s-config-hash`.

    Use these files to confirm whether source changes are actually deployed.

    - Edit Docker/Compose source under `Data/Engine/Containers/`.
    - Do not edit generated runtime under `Engine/` except when reading logs/manifests.
    - Use `Engine.sh --network-mode public|local deploy prod|dev` for full stack deployment.
    - Use `Engine.sh --network-mode public|local --service ...` for scoped service actions.
    - Validate launcher syntax after changing shell scripts:
    ```sh
    bash -n Engine.sh
    docker compose --env-file Data/Engine/Containers/compose.env.example -f Data/Engine/Containers/compose.yaml config
    python3 Data/Engine/Containers/check-compose-policy.py
    ```
    - Validate Stage 1 K3s runtime only on a host where installing/reconciling K3s is acceptable:
    ```sh
    sudo bash Engine.sh --network-mode local deploy prod
    sudo bash Engine.sh --network-mode local deploy prod
    sudo systemctl is-active k3s
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
    sudo iptables -C INPUT -p tcp --dport 6443 -j BOREALIS-K3S-API
    ```
    - Update this page when adding a service, port, volume, service action, or load-order dependency.
