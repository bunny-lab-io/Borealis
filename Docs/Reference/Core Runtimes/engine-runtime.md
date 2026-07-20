# Engine Runtime

Describe the Borealis Engine runtime, its services, configuration, and operational responsibilities.

## Runtime Summary
- API runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go` (Go `net/http`) inside the `api-backend` container.
- Configuration loader: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go` (environment-first defaults).
- API registration: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go` (Go route registrars).
- Site-worker orchestrator: Go `site-worker-orchestrator` mode from the shared api-backend binary; it owns Docker write access for site-worker containers and allowlisted Engine service actions.
- K3s baseline, storage, and bridge workloads: `Engine.sh` installs and reconciles a single-node K3s control plane, Longhorn storage baseline, restricted `borealis-operator`, K3s API backend bridge, K3s job-scheduler workload, K3s WebUI workload, and guacd bridge workload for migration work.
- WebUI serving: production and dev traffic are routed by Compose Traefik to K3s `webui-frontend`. Stage 6 retires the Compose WebUI service; `Engine.sh` still keeps the source/build bridge that syncs WebUI source and reconciles the K3s workload.
- Realtime events: `Data/Engine/Containers/api-backend/cmd/api-backend/operator_realtime.go` and `remote_shell.go` (quick job results, VPN shell bridge).
- VPN orchestration: `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go` and `server_wireguard.go` (WireGuard runtime + tunnel service).
- Remote desktop proxy: `Data/Engine/Containers/api-backend/cmd/api-backend/vnc.go` and `vnc_runtime.go` (Apache Guacamole VNC bridge through local `guacd`).
- Assemblies: `Data/Engine/Containers/api-backend/cmd/api-backend/assemblies.go` and `assemblies_catalog.go`.
- Watchdog runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/watchdogs.go` and `watchdogs_runtime.go`.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /health` (No Authentication) - Engine liveness probe.
    - `GET /api/server/timezones` (Operator Session) - current Engine timezone metadata used by Server Info.
    - The Engine hosts all `/api/*` endpoints listed in [API Reference](../Data%20and%20Schema/api-reference.md).

    ### Related documentation

    - [Architecture Overview](../../Reference/architecture-overview.md)
    - [Docker Stack Breakdown](Stack_Breakdown.md)
    - [Database Reference](../Data%20and%20Schema/db-reference.md)
    - [Security Whitepaper](../../Reference/security-whitepaper.md)
    - [API Reference](../Data%20and%20Schema/api-reference.md)
    - [Backup and Restore](../../Using%20the%20Platform/backup-restore.md)
    - [Engine Log Management](../../Using%20the%20Platform/engine-log-management.md)
    - [Remote Shell](../../Using%20the%20Platform/remote-shell.md)
    - [Watchdogs](../../Using%20the%20Platform/watchdogs.md)
    - [Alerts](../../Using%20the%20Platform/alerts.md)

    ### Source vs runtime
    - Edit API/backend code in `Data/Engine/Containers/api-backend/data/`.
    - Edit WebUI code in `Data/Engine/Containers/webui-frontend/data/web-interface/` for committed source changes. For rapid dev-mode HMR edits, use `Engine/Services/webui-frontend/data/web-interface/`.
    - Keep `Data/Engine/` for package shims, unit tests, and container roots.
    - Container source lives under `Data/Engine/Containers/` for Compose, Dockerfiles, build manifests, service entrypoints, and service-owned source trees.
    - `Engine/` is generated runtime state. Do not edit it directly.
    - Deploy state lives in `Engine/Deploy/compose.env`, `Engine/Deploy/runtime.env`, `Engine/Deploy/webui-frontend.env`, `Engine/Deploy/image-manifest.json`, `Engine/Deploy/deploy-manifest.json`, `Engine/Deploy/k3s-baseline.sha256`, `Engine/Deploy/k3s-longhorn.sha256`, `Engine/Deploy/borealis-operator.sha256`, `Engine/Deploy/k3s-api-backend.sha256`, `Engine/Deploy/k3s-job-scheduler.sha256`, `Engine/Deploy/k3s-bridge-workloads.sha256`, and `Engine/Deploy/build.log`.
    - `Engine.sh` resolves the host timezone into `BOREALIS_ENGINE_HOST_TIMEZONE` and `TZ` in `Engine/Deploy/runtime.env` and `compose.env`. Compose services and Borealis K3s pods also receive fixed read-only host timezone data mounts for `/etc/localtime` and `/usr/share/zoneinfo` so minimal images resolve the same local time as the Engine host.
    - Service state lives in `Engine/Services/<role>/` with only directories used by that service.
    - Traefik ACME state lives under `Engine/Services/traefik-edge/state/acme.json` for Externally Accessible deployments. It is kept `0600` and owned by the Borealis runtime user so `api-backend` can include it in encrypted Backup/Restore exports while Traefik, which runs as root with `DAC_OVERRIDE`, can still read and update it. Internal-Only deployments store Borealis local CA material under `Engine/Services/traefik-edge/state/local-ca/` and managed leaf material under `Engine/Services/traefik-edge/state/local-certs/`.
    - Logs live under `Engine/Services/<role>/logs/`; api-backend writes API and domain logs under `Engine/Services/api-backend/logs/`.
    - Ansible runtime lives under `Engine/Services/api-backend/cache/Ansible/`.
    - TLS and signing certificates live under `Engine/Services/api-backend/secrets/Certificates/`.
    - Bundled official assemblies live under `Data/Engine/Containers/api-backend/data/Official_Assemblies/`; managed Aurora checkout lives under `Engine/Services/api-backend/cache/Aurora/`.
    - The Compose project name is `borealis-engine`.
    - `Engine.sh` computes input hashes from Dockerfiles, build context, container entrypoints, source files, dependency manifests, and mode inputs, then builds images as `borealis-engine/<service>:sha-<hash>`. Hashes use declared service inputs, not the repo-wide Git commit.
    - `api-backend`, `job-scheduler`, `site-worker-orchestrator`, and `borealis-operator` share the Go api-backend binary. `Engine.sh` prepares that binary only after one of those images is known to need a Docker rebuild, then reuses it within the same deploy pass.
    - `api-backend` uses `alpine:3.24` with `ca-certificates`, `git`, and `tzdata`; Python dependencies, Docker CLI plugins, OCR tooling, and WireGuard command-line tools are not installed in this container. WireGuard command execution belongs to `wireguard-tunnel` through its control socket.
    - `job-scheduler` uses `alpine:3.24` with Bash, Python 3, Docker CLI, Docker Compose plugin, `ca-certificates`, and `tzdata`; Docker Buildx is not installed in this image. Stage 8 runs it as a K3s Deployment with no ServiceAccount token and no Docker socket. The same image also runs `site-worker-orchestrator`, but only the orchestrator service mounts `/var/run/docker.sock`.
    - `borealis-operator` uses `alpine:3.24` with `ca-certificates`, `tzdata`, and the shared Go api-backend binary in `borealis-operator` process mode. It runs in K3s, not Compose, and receives generated immutable image allowlists from `Engine.sh`.
    - Go backup/restore routes live in `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go` and snapshot allow-listed PostgreSQL tables plus allow-listed Engine secret/config files.
    - Server overview and Sites install metadata expose deployment profile, FQDN aliases, certificate mode, local CA fingerprint/expiry, local CA base64 PEM, and `server_ip_fallback` for Internal-Only Agent install commands.
    - Internal-Only Engine IP fallback metadata is normalized by `Data/Engine/Containers/api-backend/cmd/api-backend/engine_ip_fallback.go` and sourced from `BOREALIS_ENGINE_IP_FALLBACK`.
    - Mode inputs affect image hashes only for services with mode-specific build targets. Today that means `webui-frontend`; DB, guacd, WireGuard, Traefik, and API images do not rebuild merely because the operator switches `prod`/`dev`.
    - Docker Buildx cache is stored as timestamped full `mode=max` exports under `Engine/Deploy/cache/buildkit/<service>/<YYYYMMDDTHHMMSSZ>-<inputhash12>` when usable; plain Docker build remains the fallback.
    - After successful deploy or service rebuild reconciliation, `Engine.sh` prunes inactive Docker images, clears Docker builder cache, and deletes whole Engine Buildx cache export directories older than 7 days. Set `BOREALIS_SKIP_DOCKER_PRUNE=1` to skip cleanup.
    - The current `site-worker` image is preserved even when no site-worker container is running. Stale site-worker tags are removed separately so scheduler-launched workers can still start after cleanup.
    - Deploy output uses a live ANSI dashboard by default. Rows render in one table with Domain, Item, Status, and Last Status Update columns. Last Status Update uses a local human-readable timestamp such as `July 11th 2026 @ 3:03PM`. Domains include Frontend, Backend, Networking, Database, k3s Cluster, Reconciliation, Housekeeping, and Complete. K3s cluster-related deploy statuses belong under the `k3s Cluster` domain; `Ensuring Cluster Exists` reports baseline reconcile first, `Longhorn Storage` reports storage dependency, manifest, rollout, and StorageClass readiness, then `Borealis Operator`, `K3s API Backend`, `K3s Job Scheduler`, `K3s WebUI Frontend`, and `Guacamole Bridge` report image import, manifest apply, and rollout before Docker Compose. Item names are human-friendly labels, while internal Docker/Compose service keys stay unchanged. Rows begin as `Pending...` and update in place as services build, start, and report Docker health. Containers with passing Docker health checks display `Running - Healthy`; containers without a health check display `Running`. Compose reconciliation does not bulk-mark services as `Starting`; after Compose exits, Engine polls transient Docker states and updates only rows whose runtime state actually changes. PostgreSQL schema maintenance streams table-level progress markers from `initialise_engine_database()` into the PostgreSQL DB row and writes each table progress line to `Engine/Deploy/build.log` before returning it to health status. The renderer uses cursor-home repaint plus clear-to-end to reduce terminal flicker. Shared build relationships appear in transient status text such as `[Shares API Backend Image] -> ...`, then disappear when runtime health updates replace them.
    - No-op redeploys reuse existing image tags and skip Compose when deploy manifest, runtime env, image hashes, and container state already match.
    - Image tag changes and WebUI mode changes are kept out of shared service state hashes; an API-only image change reconciles the K3s API traffic-owner Deployment, and a WebUI-only image change or prod/dev mode flip reconciles the K3s `webui-frontend` workload without intentionally recreating unrelated Compose services.
    - Scoped image redeploys use `docker compose up -d --no-deps --no-build <service...>` after Borealis has already built changed images, so unrelated services are not intentionally recreated.
    - Compose health checks gate startup: PostgreSQL `pg_isready`, WireGuard control socket presence, guacd TCP `4822`, WebUI loopback HTTP, Go API binary `api-healthcheck`, and Traefik ping on `127.0.0.1:8082`.

    ### Container service boundaries
    - K3s `api-backend` runs the Go Engine API, live operator sessions, workflow APIs, WireGuard/VNC orchestration, and VNC WebSocket proxy. It binds `127.0.0.1:5001` on Engine host loopback during Stage 7+.
    - Longhorn runs in K3s namespace `longhorn-system` as the Borealis storage baseline for future PVC-backed workloads. `Engine.sh deploy` installs or verifies host iSCSI prerequisites, applies the pinned Longhorn manifest, waits for Longhorn Deployments/DaemonSets and the configured StorageClass, marks the Longhorn StorageClass as explicit-use only, records `Engine/Deploy/k3s-longhorn.sha256`, and does not delete Longhorn resources or PVCs during normal deploy. `local-path` remains the cluster default until Borealis explicitly changes that policy; Borealis StatefulSets request `storageClassName` from `BOREALIS_K3S_PVC_STORAGE_CLASS`.
    - K3s `postgres-db` runs as a Stage 9 shadow StatefulSet with one Longhorn-backed PVC named `postgres-data-postgres-db-0`. It uses `PGDATA=/var/lib/postgresql/data/pgdata` so PostgreSQL does not initialize directly on the PVC mount root, uses the current profile-managed PostgreSQL settings and generated credentials, exposes only ClusterIP services inside K3s, records `Engine/Deploy/k3s-postgres-db.sha256`, and is annotated with `borealis.io/traffic-owner=docker-compose` while Compose PostgreSQL remains authoritative on `127.0.0.1:5432`. Normal deploy applies or updates the StatefulSet when `BOREALIS_K3S_POSTGRES_ENABLED=1`; disabling the flag does not delete the StatefulSet or PVC. Shadow import validation is explicit through `Engine.sh --network-mode public|local --service postgres-db shadow-import prod`; it dumps live Compose `engine` and `assemblies` schemas, restores them into the K3s shadow DB, refuses to run once K3s becomes traffic owner, and compares core counts before reporting success.
    - `borealis-operator` runs inside K3s and exposes a ClusterIP-only HMAC API on port `8088`. It accepts read-only cluster status verbs plus restricted lifecycle verbs for known workloads and fixed site-worker pod templates. Its ServiceAccount is namespace-scoped, can read Metrics Server podmetrics for Borealis worker CPU/RAM visibility, cannot read Secrets or list Nodes, and cannot patch workloads outside fixed Borealis resource names.
    - K3s `api-backend` runs as the Stage 7 traffic owner on host-network loopback port `5001` by default. It mirrors generated `runtime.env` into `borealis-api-backend-runtime-env`, mounts only fixed API/Traefik/WireGuard runtime paths, enables API-owned background loops with `BOREALIS_API_BACKGROUND_LOOPS=1`, uses `Recreate` rollout strategy because single-node host networking cannot run two pods on the same host port, and removes stale Compose `borealis-engine-api-backend` containers during deploy. Shadow DB validation is a separate disposable K3s Job created by `Engine.sh --network-mode public|local --service api-backend shadow-db-validate prod`; it overrides only `BOREALIS_DATABASE_URL` to reach the K3s PostgreSQL ClusterIP Service, runs the Go API DB healthcheck, then exits without opening a listener or moving production DB traffic.
    - K3s `webui-frontend` runs as a ClusterIP-only workload on port `8000`. Compose Traefik routes WebUI traffic to this Service after Stage 6. In dev mode it gets fixed read-only hostPath mounts to `Engine/Services/webui-frontend/data/web-interface/` for HMR parity.
    - K3s `remote-desktop-guacd` runs as a non-authoritative ClusterIP-only bridge on port `4822`. K3s API still connects to loopback guacd until the remote desktop cutover stage.
    - `site-worker-orchestrator` owns the host Docker socket in container mode. It listens on `/opt/Borealis/Engine/Services/site-worker-orchestrator/run/orchestrator.sock`, requires `X-Borealis-Internal-Token`, accepts strict JSON, launches only allowlisted site-worker images, and enforces fixed host networking plus fixed Borealis bind mounts.
    - K3s `job-scheduler` owns the scheduled-job tick loop, Postgres work leases, service action queueing, and site-worker reconciliation as one `Recreate`-strategy Deployment. It has no ServiceAccount token, no kubeconfig, and no Docker socket. Until API/PostgreSQL cutover, it uses host networking only for loopback PostgreSQL and the K3s API backend bridge on `127.0.0.1:5001`, receives generated runtime env through `borealis-job-scheduler-runtime-env`, writes site-worker Traefik route files through a fixed hostPath, and calls `borealis-operator` for K3s site-worker lifecycle and operator-safe workload restarts. Docker-backed workers keep `site-worker-<uuid>` container names; K3s bridge workers use deterministic `site-worker-<sanitized-site-name>` pod names and deterministic per-site worker GUIDs so Agent Socket.IO route URLs stay stable across redeploys.
    - Site workers execute site-scoped pressure work such as automatic local-network onboarding outside the API process. They do not mount the Docker socket. K3s bridge site workers still depend on Compose-owned PostgreSQL until Stage 9, so worker startup, registration, and heartbeat loops treat transient PostgreSQL startup/unavailable errors as retryable instead of fatal.
    - Compose `webui-frontend` is retired after Stage 6. `Engine.sh` removes any stale `borealis-engine-webui-frontend` container during deploy instead of recreating it.
    - `traefik-edge` owns public HTTP/HTTPS on `80/443`, ACME or Borealis local CA TLS identity, Traefik config, UI/API/Socket.IO/VNC routing, and edge logs.
    - `postgres-db` owns PostgreSQL state under `Engine/Services/postgres-db/state` and binds `127.0.0.1:5432`.
    - `remote-desktop-guacd` runs VNC-only `guacd` on `127.0.0.1:4822`.
    - `wireguard-tunnel` owns constrained WireGuard command execution, `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, the `borealis-wg` interface, and the Unix control socket under `Engine/Services/wireguard-tunnel/run/control.sock`.

    ### Launcher commands
    - `Engine.sh --network-mode public deploy prod`: production WebUI with public DNS and ACME/Let's Encrypt.
    - `Engine.sh --network-mode local deploy prod`: production WebUI with private DNS/VPN reachability and Borealis local CA.
    - `Engine.sh --network-mode public|local deploy prod|dev`: reconciles K3s baseline, Longhorn storage baseline, `borealis-operator`, the K3s PostgreSQL shadow StatefulSet, K3s API traffic owner, K3s job-scheduler, and WebUI/guacd workloads as part of deploy. It writes `/etc/rancher/k3s/config.yaml.d/10-borealis.yaml`, installs K3s only when the binary and `k3s.service` are missing, restarts K3s only after Borealis-owned config changes, creates the `borealis` namespace, applies namespace/node labels plus `borealis.io/k3s-config-hash` annotations, installs or verifies Longhorn iSCSI prerequisites, applies the pinned Longhorn manifest when enabled, waits for Longhorn rollout and StorageClass readiness, clears Longhorn default-StorageClass annotations so Borealis PVCs must request it explicitly, imports the operator/API/scheduler/postgres/site-worker/WebUI/guacd images into K3s containerd when missing, renders immutable operator image allowlists, applies fixed operator and workload manifests, waits for rollouts, recycles K3s site-worker pods when their internal API base URL or inherited timezone changes, retires old Compose API/job-scheduler/WebUI containers, and re-renders Compose env with operator ClusterIP, K3s API loopback, and WebUI ClusterIP route targets.
    - `Engine.sh --network-mode public|local --service api-backend shadow-db-validate prod`: refreshes the K3s PostgreSQL shadow import from Compose, runs a disposable K3s API backend validator Job against that imported K3s data, and leaves K3s API plus Compose PostgreSQL as traffic owners.
    - K3s startup config uses only `borealis.io/*` node labels. `Engine.sh` applies `app.kubernetes.io/part-of=borealis` later through admin `kubectl` because kubelet rejects `app.kubernetes.io/*` labels passed through `--node-labels`.
    - Stage 1 K3s baseline keeps bundled Traefik and ServiceLB disabled and owns `borealis-k3s-api-firewall.service` for TCP `6443` host firewall enforcement.
    - Local deploy writes `BOREALIS_ENGINE_IP_FALLBACK` into runtime env from an explicit override or the host default IPv4 route. Sites uses that value for Linux Agent install commands only when Engine network mode is Local.
    - `Engine.sh --network-mode public|local deploy dev`: Vite HMR WebUI behind Traefik. API, PostgreSQL, Traefik, guacd, and WireGuard stay on the current shared runtime config unless their own inputs changed.
    - `Engine.sh --network-mode public|local --service api-backend restart`: restart the K3s API backend Deployment and wait for rollout.
    - `Engine.sh --network-mode public|local --service api-backend rebuild prod`: rebuild the API image, reconcile the K3s API backend Deployment to the refreshed image, and retire any stale Compose API container.
    - `Engine.sh --network-mode public|local --service job-scheduler restart`: restart the K3s job-scheduler Deployment and wait for rollout.
    - `Engine.sh --network-mode public|local --service job-scheduler rebuild prod`: rebuild the scheduler image, retire any stale Compose scheduler container, and reconcile the K3s job-scheduler Deployment to the refreshed image.
    - `Engine.sh --network-mode public|local --service webui-frontend rebuild dev|prod`: rebuild the WebUI image, sync dev runtime source when requested, and reconcile the K3s WebUI workload.
    - `Engine.sh --network-mode public|local --service traefik-edge reload`: restart Traefik edge after config/env changes.
    - `Engine.sh --network-mode public|local --service postgres-db restart`: restart PostgreSQL container.
    - `Engine.sh --network-mode public|local --service postgres-db shadow-import prod`: validate the Stage 9 K3s PostgreSQL shadow DB by importing a logical snapshot from Compose PostgreSQL into the K3s Longhorn PVC without changing live DB traffic ownership.
    - `Engine.sh --network-mode public|local --service remote-desktop-guacd restart`: restart guacd container.
    - `Engine.sh --network-mode public|local --service remote-desktop-guacd rebuild dev|prod`: rebuild and recreate guacd container, then reconcile K3s bridge workloads so the non-authoritative guacd bridge follows the current image.
    - `Engine.sh --network-mode public|local --service wireguard-tunnel reconcile`: query the WireGuard control socket from the tunnel container.

    ### One-shot legacy migration helpers
    - `Data/Engine/Containers/sterilize-systemd-runtime.sh`: migration-only helper that stops/removes legacy Borealis systemd units, disables host PostgreSQL units, best-effort removes old `borealis-wg` state, dumps the legacy `borealis` database when reachable, and renames `Engine/` to `Engine.old/`.
    - `Data/Engine/Containers/import-legacy-postgres-dump.sh <dump.sql>`: migration-only helper that imports a preserved logical dump into container PostgreSQL after deployment.
    - These helpers are not called by `Engine.sh`.

    ### API lifecycle
    - `Data/Engine/Containers/api-backend/cmd/api-backend/main.go` loads environment configuration, initializes auth/database services, registers Go route groups, starts realtime/VPN/VNC/watchdog runtimes, and serves loopback HTTP.
    - VNC proxy settings use environment values for VNC port, WebSocket host/port, session TTL, Guacamole path, and `guacd` host/port.
    - WebUI production/dev serving belongs to `webui-frontend`; the Engine-side static handler remains only for tests and non-container paths.

    ### API groups and adapters
    - Route registrars live in `Data/Engine/Containers/api-backend/cmd/api-backend/*.go` and are wired from `main.go`.
    - Domain files keep route handlers close to domain storage and validation helpers.
    - `EngineServiceAdapters` exposes:
      - `db_conn_factory` (PostgreSQL-backed DB adapter exposed through the shared compatibility layer).
      - `service_log` (per-service log files with rotation).
      - `jwt_service`, `dpop_validator`, rate limiters, signing keys, GitHub integration.

    ### Logging expectations
    - Main logs: `Engine/Services/api-backend/logs/engine.log` and `Engine/Services/api-backend/logs/error.log`.
    - API access log: `Engine/Services/api-backend/logs/api.log` (per-request stats).
    - Service logs: `Engine/Services/api-backend/logs/<service>.log` (created via `service_log`).
    - VPN logs: `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log` and `Engine/Services/api-backend/logs/VPN_Tunnel/remote_shell.log`.

    ### Adding or updating an API
    - Add new Go routes under `Data/Engine/Containers/api-backend/cmd/api-backend/<domain>.go`.
    - Register new route groups from `Data/Engine/Containers/api-backend/cmd/api-backend/main.go`.
    - Update `Docs/Reference/Data and Schema/api-reference.md` and the relevant domain doc.

    ### WebUI hosting and dev mode
    - Production UI is served by the K3s `webui-frontend` workload from its built static output after Stage 6.
    - Dev UI runs Vite HMR from the K3s `webui-frontend` workload behind `traefik-edge`.
    - WebUI app-wide realtime uses `/api/realtime/events` SSE through `bootstrapClientRuntime.js`. Root `/socket.io` is not opened on normal page load or operator-presence sync; only explicitly allowlisted legacy workflow-node events can connect to that root Socket.IO path.
    - The WebUI image uses Node Alpine stages. The production target copies only built static output plus the dependency-free static server into the final image, while the development target keeps Vite and `node_modules` for HMR.
    - The API backend sets `BOREALIS_WEBUI_EXTERNAL=1` in container mode so `Data.Engine.bootstrapper` skips Engine-side WebUI staging/build.
    - The SPA handler in `Data/Engine/Containers/api-backend/data/services/WebUI/__init__.py` remains for tests and non-container execution.

    ### PostgreSQL profile notes
    - `Engine.sh --network-mode public|local deploy` detects vCPU and RAM on every deploy/redeploy, selects the lower CPU/RAM profile rank, and writes profile metadata into `Engine/Deploy/compose.env`.
    - `Engine.sh --network-mode public|local deploy` starts `postgres-db`, waits for health, and runs `Data.Engine.database.initialise_engine_database` from the current `site-worker` image before API/scheduler reconciliation.
    - During the K3s bridge stages, Compose can still recreate or restart `postgres-db` before every workload has moved to Kubernetes. K3s site-worker pods use host-loopback PostgreSQL during that window and must tolerate short connection-refused, database-starting, timeout, and closed-connection errors without exiting.
    - Profile tuning owns Engine DB pool values, PostgreSQL startup settings, and `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY`.
    - Site-worker scheduled-lane values are active work-item slots: Homelab `5`, Small Business `8`, MSP / Production `12`, and Enterprise `16`. Enterprise Clustered remains docs-only at `16` per node.
    - Shared Ansible work items can target multiple hosts inside one slot. Individual Ansible work items target one host per slot.
    - PostgreSQL settings are applied through the `postgres-db` Compose command. Operators should not run manual PostgreSQL tuning steps for normal profile-managed deployments.

    ### WireGuard and VNC wiring
    - WireGuard runtime and tunnel orchestration: `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go`.
    - Server WireGuard settings routes: `Data/Engine/Containers/api-backend/cmd/api-backend/server_wireguard.go`.
    - VNC collaboration state and proxy bootstrap: `Data/Engine/Containers/api-backend/cmd/api-backend/vnc.go` and `vnc_runtime.go`.
    - Guacamole VNC bridge settings are served by the Go VNC routes and connect through `remote-desktop-guacd`.
    - API entrypoints: `/api/vnc/viewers`, `/api/vnc/establish`, `/api/vnc/disconnect`, `/api/vnc/handoff`, `/api/vnc/sessions`, `/api/shell/establish`, `/api/shell/disconnect`.
    - Persistent tunnels are established by agents via `POST /api/agent/vpn/ensure`, then marked dispatch-ready by `POST /api/agent/vpn/ready` after the active service/config/firewall path is applied.
    - WireGuard peer leases skip the peer-network base address, peer-network broadcast address, and Engine virtual IP. Existing leases on those reserved addresses are ignored on load so the next connect can persist a usable `/32`.
    - The Engine requests the current Agent VNC password on demand over the registered Agent Socket.IO channel during `/api/vnc/establish`, uses that live credential for the Guacamole token it is minting, and does not maintain an agent-level VNC password cache. Normal launches require a non-auth RFB banner before returning browser bootstrap data, so a TCP-only or hung UltraVNC listener fails fast as `vnc_backend_no_rfb_banner` instead of burning Guacamole retries. The site-worker then performs a default non-auth RFB security preflight that reads only the security-type list and blocks security type `0` failures, including UltraVNC `valid password` not enabled errors, before guacd starts its VNC retry loop. UltraVNC password-not-enabled failures return `vnc_password_not_enabled` with a non-retryable 409 so the Go VNC broker does not start Agent credential rotation for an endpoint listener that is not accepting password auth. UltraVNC `too_many_auth_failures` lockout text returns `vnc_auth_lockout` with a non-retryable 423 so the broker does not extend endpoint-side lockout by cycling credentials or launching new guacd attempts. This preflight is controlled by `BOREALIS_VNC_SECURITY_PREFLIGHT` and `BOREALIS_VNC_SECURITY_PREFLIGHT_TIMEOUT_SECONDS`. Set `BOREALIS_VNC_AUTH_PROBE=1` or request-scoped `auth_probe=true` when the site-worker should perform a bounded RFB VNCAuth probe outside normal broker control. WebUI keeps auth probe off for first attempts, then enables request-scoped `auth_probe` on the second establish attempt only after Guacamole/browser startup fails, so Engine can classify credential failure without adding a login-consuming probe to every healthy launch. Probe results are logged with structured RFB stage fields and never include the VNC password, DES challenge, or DES response.
    - Site-worker Guacamole retry handles local guacd startup errors with a bounded retry loop, but it does not stack fresh guacd sessions for post-ready target status `519`. Guacamole VNC keeps `autoretry=3` to match the pre-hardening browser path; after that target-side failure, Borealis closes as `guacamole_unavailable` and lets the WebUI budget decide whether one more establish attempt remains. WebUI performs at most two establish attempts per Connect action and does not stack multi-minute fresh Guacamole token loops while the site-worker is still trying the same endpoint. Exhausted Guacamole backend retries are not proof of bad UltraVNC credentials.
    - Explicit VNCAuth diagnostic failures are reported back to the Go VNC broker as `vnc_auth_failed`. The next establish request uses Agent reason `vnc_auth_retry`, which is already supported by the Agent VNC role for runtime credential rotation and UltraVNC config rewrite. Password-not-enabled preflight failures are classified as `vnc_password_not_enabled`, not `vnc_auth_failed`, and do not trigger credential rotation.
    - VNC auth retry is single-flight per Agent. The Go broker returns `vnc_auth_retry_in_progress` or `vnc_auth_retry_settling` with `retry_after_seconds` while one credential rotation or post-rotation settle window is active. Auth-retry state clears after first Guacamole frame or a successful explicit auth probe, not after credential fetch alone. Auth retry and UltraVNC lockout settle hints are capped at 30 seconds by default through `BOREALIS_VNC_ESTABLISH_DEADLINE_SECONDS`, `BOREALIS_VNC_AUTH_RETRY_COOLDOWN_SECONDS`, and `BOREALIS_VNC_AUTH_LOCKOUT_COOLDOWN_SECONDS`.
    - Apache Guacamole is the sole browser remote desktop path. Guacamole VNC uses local `guacd` on `127.0.0.1:4822` by default, is served through `/remote-desktop/vnc/guacamole`, and never returns the UltraVNC password to the browser.
    - `remote-desktop-guacd` uses Apache Guacamole Server 1.6.0 in VNC-only mode, runs as the Borealis runtime owner, binds loopback port `4822`, uses read-only root filesystem hardening, mounts only read-only host timezone data, and mirrors guacd stdout/stderr to Docker logs.

    ### Assembly runtime
    - Assembly catalog and mutation routes live in `Data/Engine/Containers/api-backend/cmd/api-backend/assemblies.go` and `assemblies_catalog.go`.
    - Quick jobs, scheduled jobs, watchdogs, and workflows share this runtime to resolve scripts and variables.

    ### Watchdog evaluator runtime
    - Go `watchdogRuntime` owns Borealis-native watchdog evaluation and remediation.
    - Registration and bootstrap happen in `Data/Engine/Containers/api-backend/cmd/api-backend/main.go` after route registration.
    - The evaluator loop periodically checks enabled watchdogs whose `evaluation_interval_seconds` has elapsed.
    - Immediate evaluation still happens on watchdog save and device-override updates so operator changes become visible without waiting for the scheduler tick.
    - On startup, the runtime purges any lingering resolved incidents that belong to offline-only watchdogs before the evaluator loop begins.
    - Runtime responsibilities include:
      - resolving explicit device and filter-backed targets
      - evaluating rules against cached device data
      - tracking per-device watchdog state
      - opening and resolving incidents
      - dispatching Engine toast notifications, service-control actions, and assembly remediation
      - emitting `watchdog_incidents_changed` and `device_watchdogs_changed`

    ### Platform parity
    - Engine deployment is Linux-only via `Engine.sh`.
    - Linux agent remains incomplete.

    ### Borealis Engine Codex (Full)
    Use this section for Engine work (successor to the legacy server). Shared guidance is consolidated in `Docs/Reference/ui-and-notifications.md` and other knowledgebase pages.

    #### Scope and runtime paths
    - Staging / launch: `Engine.sh` handles Linux first install, dependency checks, K3s baseline reconcile, Engine container build, and Compose deployment. (`Agent.exe` is Windows Agent-only.)
    - Edit in `Data/Engine` and `Data/Engine/Containers`; use `Engine.sh --network-mode public|local deploy dev|prod` when source changes need to reach the running service.
    - Container redeploys use committed source JSON for `software_icons_overrides.json`, `software_uninstall_overrides.json`, and `software_uninstall_blocklist.json`; commit operator-tested hotloaded rules that must survive image rebuilds.
    - Raw one-line or repo-option `Engine.sh` runs sync first, then re-execs the installed `Engine.sh`; local `Engine.sh --network-mode public|local deploy` uses existing on-disk source and does not update git.

    #### Architecture
    - Runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go` for production API endpoints. WebUI production/dev serving belongs to `webui-frontend`.

    #### Development guidelines
    - Every Python module under `Data/Engine` or `Engine/Data/Engine` starts with the standard commentary header (purpose + API endpoints). Add the header to any existing module before further edits.

    #### Logging
    - Primary API log: `Engine/Services/api-backend/logs/engine.log` with daily rotation (`engine.log.YYYY-MM-DD`); do not auto-delete rotated files.
    - Subsystems: `Engine/Services/api-backend/logs/<service>.log`; container build output: `Engine/Deploy/build.log`; Traefik logs: `Engine/Services/traefik-edge/logs/`; PostgreSQL and guacd logs: Docker logs.
    - Keep Engine-specific artifacts within `Engine/Services/<role>/logs/` or `Engine/Deploy/` to preserve the runtime boundary.

    #### Security and API parity
    - Uses Ed25519 device identities, EdDSA-signed access tokens, and a Borealis-managed Traefik edge with Let's Encrypt for the public browser/agent trust chain while the Go Engine API stays on loopback HTTP.
    - Implements DPoP validation, short-lived access tokens (about 15 min), SHA-256 hashed refresh tokens (30-day) with explicit reuse errors.
    - Enrollment: operator approvals, conflict detection, auditor recording, pruning of expired codes/refresh tokens.
    - Background jobs and service adapters maintain compatibility with legacy DB schemas while enabling gradual API takeover.

    #### Protected secret storage
    - The Engine exposes an Engine-global Aegis Cipher lifecycle through `Data/Engine/Containers/api-backend/cmd/api-backend/aegis.go`, `aegis_crypto.go`, and `aegis_lifecycle.go`.
    - The bootstrap gate for operator auth lives in `Data/Engine/Containers/api-backend/cmd/api-backend/auth_bootstrap.go` and `auth_login.go`.
    - Aegis v1 now protects stored credentials, the GitHub API token, operator password hashes, operator TOTP secrets, and passkey cryptographic material at rest using `scrypt` plus `AES-256-GCM`.
    - Directory Services adds LDAP/LDAPS and Active Directory-compatible credential providers under the auth API group. Generic LDAP and Active Directory providers use service-account search plus LDAP/LDAPS user bind. Operators can define provider-scoped host overrides so FQDN server URLs connect to explicit IP addresses without editing Engine host records; TLS still uses the FQDN for SNI and certificate name validation. Operators can download an LDAPS peer certificate from the provider editor, review subject/issuer/SAN/fingerprint metadata, and pin that certificate for future strict TLS checks.
    - Directory provider bind passwords are Aegis-protected. Directory users are JIT cached in `users`, keep Borealis TOTP MFA, and cannot register passkeys.
    - Setup migrates any legacy plaintext credential, GitHub token, password hash, MFA secret, or passkey cryptographic row into Aegis envelopes and stores KDF metadata plus a verification token in `aegis_cipher_state`.
    - The derived key is cached only in Engine memory. Restarting the Engine relocks protected secrets until an admin re-enters the cipher.
    - Backup/Restore exports Engine configuration as encrypted JSON using the same Aegis-derived key. Restore imports `aegis_cipher_state` unchanged, clears the in-memory key, and returns `restart_required: true`.
    - Borealis does not render the login screen until bootstrap reaches `login_required`. Fresh installs require Aegis setup plus first-admin bootstrap; every later restart requires Aegis unlock before normal login or passkey auth can start.
    - While locked, operator-facing auth/session checks reject stale cookies and tokens until bootstrap unlock completes. Agent and device trust flows stay online because they do not depend on operator auth secrets.
    - Access Management now uses the Credentials page for Aegis status, rotation, and destructive force reset; setup and unlock moved to the bootstrap gate.
    - Force reset is the disaster-recovery path when the old cipher is gone: Borealis destroys unrecoverable operator auth secrets, clears the Aegis state row, marks existing users for recovery, marks affected credentials and the GitHub token for re-entry, and disables scheduled jobs that still point at wiped credentials.

    #### Reverse VPN tunnels
    - WireGuard reverse VPN design and lifecycle are documented in `Docs/Using the Platform/remote-shell.md` and `Docs/Using the Platform/remote-desktop.md`.
    - The original references were `REVERSE_TUNNELS.md` and `Reverse_VPN_Tunnel_Deployment.md` (now consolidated into this knowledgebase).
    - Engine orchestrator and WireGuard runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go`.
    - UI shell bridge: `Data/Engine/Containers/api-backend/cmd/api-backend/remote_shell.go`.

    #### WebUI and WebSocket migration
    - Static/template handling: `Data/Engine/Containers/api-backend/data/services/WebUI`; deployment copy paths are wired through `Engine.sh` with TLS-aware URL generation. Production and dev container traffic are served by K3s `webui-frontend` through Compose Traefik after Stage 6.
    - Stage 6 tasks: migration switch in the legacy server for WebUI delegation and porting device/admin API endpoints into Engine services.
    - Stage 7 (queued): `register_realtime` hooks, Engine-side Socket.IO handlers, integration checks, legacy delegation updates.

    #### Platform parity
    - Linux is the Engine target platform. Keep Engine tooling aligned with Docker Engine plus Docker Compose, not Docker Desktop.

    #### Ansible support (shared state)
    - The Linux Engine now packages Ansible control-node tooling inside worker runtimes and installs Borealis-managed collections into `Engine/Services/api-backend/cache/Ansible/collections` for shared worker access.
    - Scheduled jobs support Engine-side shared Ansible execution for `local`, `ssh`, and `winrm` contexts.
    - Remote SSH/WinRM runs synthesize ephemeral inventories from Borealis device/filter state and active WireGuard sessions, using site-qualified inventory aliases for duplicate-hostname safety.
    - Shared remote Ansible transport follows the scheduled job execution context; device `connection_type` metadata does not override the operator-selected `ssh` or `winrm` mode.
    - The credentials API now backs stored SSH/WinRM credentials for scheduler selection, while quick-run, cancel, PSRP, and richer recap UX remain in progress.
    - When Aegis is locked, credential-backed shared Ansible runs are skipped instead of replayed later, and affected run targets record an explicit lock/reset reason instead of being reported as missing credentials.
    - When a credential survives an Aegis force reset but its secret material was destroyed, scheduled jobs surface `credential_reset_required` warnings and stay disabled until the operator restores the missing credential data.
