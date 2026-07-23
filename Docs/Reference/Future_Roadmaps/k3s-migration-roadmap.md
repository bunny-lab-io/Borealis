# K3s Migration Roadmap

Borealis will migrate the Engine runtime from Docker Compose into single-node K3s through staged bridge rollout. The existing Engine deployment command shape stays intact while each workload moves only after explicit validation and one-way retirement planning for the old runtime equivalent.

## Goal

Migrate Borealis Engine from Docker Compose into single-node K3s through staged bridge rollout. Preserve existing `Engine.sh --network-mode <public|local> deploy <prod|dev>` command shape and idempotent deployment behavior.

## Principles

- [x] Keep `Engine.sh deploy` idempotent: reconcile, do not recreate healthy K3s state.
- [x] Start with single-node non-HA K3s.
- [x] Keep Compose Engine authoritative until each workload reaches explicit cutover.
- [x] Introduce `borealis-operator` as only runtime K3s writer; `Engine.sh` remains the deploy-time bootstrap/reconcile writer.
- [x] Keep `api-backend`, `job-scheduler`, and migrated workloads Kubernetes-blind or operator-API-only.
- [x] Use allowlisted Borealis verbs, never raw YAML/kubectl from runtime services.
- [x] Do not rotate secrets, delete PVCs, or teardown cluster during normal deploy.
- [x] Use Longhorn as the Borealis K3s PVC/persistent volume backend for workloads that need durable container storage.
- [x] Propagate the Engine host timezone into Borealis-managed Compose containers and K3s pods.

## Stage 1: K3s Baseline

- [x] Add K3s detection/install/reconcile phase inside existing `Engine.sh deploy`.
    - [x] Install K3s only when missing.
    - [x] Verify K3s service, kubeconfig, node readiness, container runtime.
    - [x] Disable bundled public ingress features until Borealis owns cutover path.
    - [x] Create `borealis` namespace.
    - [x] Add managed labels/annotations for ownership and config hashes.
- [x] Validation:
    - [x] Run deploy twice back to back and confirm no cluster teardown or pod churn.
    - [x] Confirm existing Compose Engine remains healthy.
    - [x] Confirm K3s API not publicly exposed.

## Stage 2: `borealis-operator` Bridge

- [x] Add `borealis-operator` workload in K3s.
    - [x] Replace future `site-worker-orchestrator` role.
    - [x] Expose internal HMAC-authenticated Borealis API.
    - [x] Grant namespace-scoped RBAC only.
    - [x] Support read-only status verbs first.
- [x] Allowed verbs:
    - [x] `GetClusterSummary`
    - [x] `ListWorkloads`
    - [x] `GetWorkloadStatus`
    - [x] `ListSiteWorkers`
- [x] Blocked verbs:
    - [x] raw YAML apply
    - [x] arbitrary pod spec
    - [x] arbitrary image/env/volume/service account
    - [x] hostPath or privileged pod creation outside fixed allowlist
- [x] Validation:
    - [x] Compose `api-backend` can query operator status.
    - [x] Operator rejects unauthenticated and unsupported requests.
    - [x] Operator RBAC cannot mutate resources outside Borealis namespace.

## Stage 3: Workload Rollout Interface

- [x] Add restricted operator lifecycle verbs.
    - [x] `RolloutKnownWorkload(service_key, image_ref)`
    - [x] `RestartKnownWorkload(service_key)`
    - [x] `ScaleKnownWorkload(service_key, replicas)`
    - [x] `LaunchSiteWorker(site_id, worker_guid, image_ref, resource_profile)`
    - [x] `RetireSiteWorker(worker_guid, reason)`
- [x] Add image allowlist and digest/hash validation.
- [x] Keep build work in `Engine.sh`, not runtime services.
- [x] Validation:
    - [x] Known service rollout succeeds with valid image.
    - [x] Unknown service/image is rejected.
    - [x] Failed rollout leaves previous healthy workload serving.

## Stage 4: First Low-Risk Workloads

- [x] Move `webui-frontend` dev workload into K3s bridge mode.
    - [x] Preserve dev HMR path where practical.
    - [x] Keep Compose Traefik/API authoritative.
- [x] Move `remote-desktop-guacd` into K3s bridge mode.
    - [x] Keep ClusterIP-only exposure.
    - [x] Preserve existing VNC/Guacamole API behavior.
- [x] Add scoped service-action bridge reconciliation.
    - [x] Reconcile K3s bridge workloads after `webui-frontend` rebuilds.
    - [x] Reconcile K3s bridge workloads after `remote-desktop-guacd` rebuilds.
    - [x] Keep Compose routing authoritative while refreshing K3s bridge images.
- [x] Fix DEV-mode Vite dependency cache mounts.
    - [x] Mount `node_modules/.vite` as writable memory-backed cache for the transitional Compose WebUI path before Stage 6 retirement.
    - [x] Mount `node_modules/.vite` as writable memory-backed cache for K3s WebUI bridge pods.
    - [x] Keep WebUI runtime source mounts read-only.
- [x] Fix DEV-mode WebUI runtime source staging.
    - [x] Sync staged WebUI source into `Engine/Services/webui-frontend/data/web-interface/` on every `deploy dev`.
    - [x] Sync staged WebUI source during `--service webui-frontend rebuild dev`.
    - [x] Keep production deploys on seed-if-missing behavior unless `BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE=1`.
- [x] Validation:
    - [x] WebUI dev path works.
    - [x] Guacd readiness passes.
    - [x] Existing Compose path remained available until Stage 6 WebUI cutover removed it.
    - [x] Scoped WebUI and guacd rebuilds refresh bridge workloads without full deploy.
    - [x] Unchanged bridge pod UIDs stay stable during scoped bridge reconciliation.
    - [x] DEV Vite dependency modules return JavaScript MIME through public Traefik after scoped WebUI rebuild.
    - [x] DEV bootstrap runtime no longer opens root `/socket.io` during normal page load or operator-presence sync.
    - [x] `/api/server/timezones` reaches authenticated Go backend route instead of returning an API 404.
    - [x] DEV HMR source sync verified after next operator redeploy.

## Stage 5: Site Worker Migration

- [x] Move dynamic site workers from Docker launch to operator-managed K3s pods.
    - [x] Add `job-scheduler` lifecycle mode switch for bridge validation before K3s site-worker ownership became default.
    - [x] Route K3s site-worker launch/retire/list operations through `borealis-operator` allowlisted verbs.
    - [x] Preserve one active worker per site in v1 through scheduler reconciliation.
    - [x] Preserve worker idle TTL and stale worker retirement in the K3s bridge path.
    - [x] Preserve non-root UID/GID, dropped capabilities, read-only root, memory-backed `/tmp`, fixed resource requests/limits, and no ServiceAccount token.
    - [x] Retire legacy Docker site-worker containers through `site-worker-orchestrator` after K3s worker listing succeeds.
    - [x] Retire terminal K3s site-worker pods so failed pods do not block replacement workers.
    - [x] Restart K3s site-worker containers on nonzero exit while preserving idle TTL clean exits.
    - [x] Treat persistent transient heartbeat deadlocks as skipped heartbeat cycles instead of fatal worker exits.
    - [x] Retry site-worker database registration and heartbeat setup during transient PostgreSQL startup/unavailable windows.
    - [x] Preserve Agent Socket.IO registration after live redeploy validation.
    - [x] Add Agent stale connected Socket.IO detection so worker restarts do not leave Agents stuck on dead control channels.
    - [x] Name new K3s site-worker pods with deterministic sanitized site slugs for operator-readable Kubernetes and Server Info views.
    - [x] Enforce unique, bounded site-worker slugs on site create and rename so K3s worker names do not need UUID suffixes.
    - [x] Keep K3s worker GUIDs deterministic per site so Agent Socket.IO route URLs do not churn across redeploys.
- [x] Replace Traefik route-file behavior with a controlled host-loopback bridge route while PostgreSQL stays localhost-only.
    - [x] Keep route files owned by `job-scheduler`.
    - [x] Keep worker listeners bound to `127.0.0.1` inside host-network K3s pods.
    - [x] Prune terminal route rows and orphaned `site-worker-*.yml` files during scheduler reconciliation.
    - [ ] Replace temporary host-loopback worker bridge with ClusterIP-only routing after API/PostgreSQL cutover makes it practical.
- [x] Validation:
    - [x] Focused operator/scheduler unit tests pass.
    - [x] Static `Engine.sh` and Python worker checks pass.
    - [x] Agents connect to site worker through existing enrollment/token flow.
    - [x] Remote shell works through K3s site workers.
    - [x] File management works through K3s site workers.
    - [x] Process management works through K3s site workers.
    - [x] Service management works through K3s site workers.
    - [x] Connected Devices column reflects K3s worker registrations.
    - [x] Add Metrics Server-backed K3s site-worker CPU/RAM plumbing through `borealis-operator`.
    - [x] `/api/server/workers` returns `metrics.k8s.io` CPU/RAM payloads for every active K3s site worker.
    - [x] K3s resource metrics appear in Sites and Server Info.
    - [x] Stale workers retire cleanly.
    - [x] Redeploy validates K3s site workers survive PostgreSQL pod rollout/restart without disconnecting the fleet.
    - [ ] Agent release rollout validates stale connected Socket.IO sessions self-recover without manual Agent service restart.

## Stage 6: Production WebUI Cutover

- [x] Make K3s `webui-frontend` authoritative.
    - [x] Add production `BOREALIS_WEBUI_TRAFFIC_OWNER=k3s` route owner that renders Compose Traefik's core WebUI upstream to the K3s `webui-frontend` ClusterIP.
    - [x] Route dev mode through the K3s `webui-frontend` workload as well.
    - [x] Keep Compose Traefik as HTTP/HTTPS, certificate, and watched dynamic-route owner.
    - [x] Remove Compose `webui-frontend` from `compose.yaml`.
    - [x] Reject `BOREALIS_WEBUI_TRAFFIC_OWNER=docker-compose` in `Engine.sh`.
    - [x] Remove stale `borealis-engine-webui-frontend` container during deploy.
- [ ] Validation:
    - [x] Public network mode serves WebUI through K3s route owner.
    - [ ] Local network mode serves WebUI through K3s route owner. Deferred to [Technical Debt #374](https://github.com/bunny-lab-io/Borealis/issues/374) because active public-mode agents may reject the local CA trust plane during live testing.
    - [x] Static assets and SPA route handling work in public network mode.
    - [x] Guacamole dynamic Traefik route still works after WebUI cutover.
    - [x] Repeated deploys do not restart unchanged WebUI pod.
    - [x] Compose `webui-frontend` container is removed after a public-mode redeploy.

## Stage 7: API Backend Cutover

- [ ] Move `api-backend` into K3s as one replica.
    - [x] Add non-authoritative K3s `api-backend` bridge Deployment.
    - [x] Bind initial bridge API to host loopback port `5001` while Compose API owned `127.0.0.1:5000`.
    - [x] Mirror generated runtime env into `borealis-api-backend-runtime-env`.
    - [x] Disable API-owned background loops in the bridge pod with `BOREALIS_API_BACKGROUND_LOOPS=0` before traffic cutover.
    - [x] Use `Recreate` rollout strategy for the host-network bridge so single-node K3s does not deadlock on fixed port `5001`.
    - [x] Add one-shot K3s `api-backend` shadow DB validator Job that targets imported K3s PostgreSQL data without moving public API traffic.
    - [x] Make K3s `api-backend` authoritative for API, Socket.IO, and internal caller traffic on `127.0.0.1:5001`.
    - [x] Enable API-owned background loops in the K3s pod after Compose API retirement.
    - [x] Remove Compose `api-backend` from `compose.yaml` and retire stale `borealis-engine-api-backend` containers during deploy.
    - [x] Route Compose Traefik's `/api` and `/socket.io` upstream to the configured K3s API loopback port.
    - [x] Recycle existing K3s site-worker pods once when `BOREALIS_INTERNAL_API_BASE_URL` changes from Compose API `5000` to K3s API `5001`.
    - [x] Preserve Aegis bootstrap/unlock behavior.
    - [x] Preserve internal API token behavior.
    - [x] Preserve logs/secrets/cache path contracts through fixed hostPath bridge mounts.
    - [ ] Replace fixed hostPath bridge mounts with Longhorn PVC/Secret/ConfigMap mapping where durable pod-local storage is required.
- [x] Validation:
    - [x] Bridge pod rollout passes.
    - [x] `curl -fsS http://127.0.0.1:5001/health` passes.
    - [x] `Engine.sh --network-mode public|local --service api-backend shadow-db-validate prod` validates Go API bootstrap state against K3s PostgreSQL shadow data.
    - [x] `/health` passes after traffic-owner deploy.
    - [x] Public WebUI route returns HTTP 200 after K3s API cutover and Compose API retirement.
    - [x] Repeated public-mode deploy keeps K3s API, scheduler, and WebUI pods ready while Compose `api-backend`, `job-scheduler`, and `webui-frontend` containers remain absent.
    - [x] Login, Aegis unlock, enrollment, heartbeat, and API routes pass.
    - [x] Remote shell, file management, process management, service management, registry, and remote desktop pass after API traffic-owner cutover.
    - [x] Backup export passes after API traffic-owner cutover.
    - [x] All agents appear connected to their site workers after API traffic-owner cutover.
    - [x] Realtime SSE works with one replica.
    - [x] Compose API removal plan is documented: K3s API owns traffic before stale Compose API container removal; Compose PostgreSQL remains DB owner until Stage 9.

## Stage 8: Scheduler Cutover

- [x] Move `job-scheduler` into K3s as one replica.
    - [x] Add fixed K3s `job-scheduler` Deployment with `Recreate` rollout strategy.
    - [x] Remove Compose `job-scheduler` service and retire stale `borealis-engine-job-scheduler` containers during deploy.
    - [x] Use `borealis-operator` for K3s workload/site-worker lifecycle where the scheduler can complete the queued action safely.
    - [x] Preserve Postgres work leases and queue behavior through the existing scheduler manager and work-item tables.
    - [x] Keep scheduler Kubernetes-blind: no ServiceAccount token, no kubeconfig, no Docker socket.
    - [x] Keep temporary host-loopback access to K3s API and Compose PostgreSQL until PostgreSQL cutover.
- [x] Validation:
    - [x] Scheduled job tick creates expected runs after live redeploy.
    - [x] Service actions route through operator for K3s-owned restart paths in focused Go tests.
    - [x] No duplicate scheduler loops in deployment model: Compose service removed, stale container retired before K3s rollout, and K3s Deployment uses `Recreate`.
    - [x] Live redeploy confirms exactly one K3s scheduler pod and no Compose `borealis-engine-job-scheduler` container.
    - [x] Scoped `job-scheduler` rebuild applies the retired-orchestrator cleanup and fresh logs stay quiet for stale `site-worker-orchestrator` socket probes.

## Stage 9: PostgreSQL Cutover

- [x] Move `postgres-db` into K3s StatefulSet with one Longhorn-backed PVC.
    - [x] Add `Engine.sh deploy` Longhorn reconcile phase before the first PVC-backed workload cutover.
    - [x] Install/reconcile Longhorn from pinned manifest version `v1.12.0` if it is enabled and not already present.
    - [x] Reconcile Longhorn host iSCSI prerequisites without deleting existing Longhorn resources or PVCs.
    - [x] Keep K3s `local-path` default unchanged for now; future Borealis PVC manifests should request `BOREALIS_K3S_PVC_STORAGE_CLASS` explicitly.
    - [x] Override Longhorn manifest default-StorageClass annotation so Longhorn stays explicit-use only.
    - [x] Add non-authoritative K3s `postgres-db` shadow StatefulSet with one Longhorn-backed PVC.
    - [x] Set PostgreSQL StatefulSet manifests to use the Longhorn StorageClass.
    - [x] Keep K3s PostgreSQL ClusterIP-only.
    - [x] Preserve profile-managed PostgreSQL startup settings in the K3s StatefulSet manifest.
    - [x] Use `PGDATA=/var/lib/postgresql/data/pgdata` so Longhorn filesystem metadata does not block `initdb`.
    - [x] Add retry-safe PostgreSQL PVC ownership init path with narrow init-container ownership capabilities.
    - [x] Add controlled final cutover path.
        - [x] Quiesce K3s API backend, K3s job-scheduler, and K3s site-worker writers before the final Compose snapshot.
        - [x] Import the final Compose PostgreSQL logical snapshot into K3s PostgreSQL before moving runtime DB URLs.
        - [x] Re-render runtime env so API backend, job-scheduler, and site-worker pods use `postgres-db.borealis.svc`.
        - [x] Run a K3s schema initializer Job after PostgreSQL becomes traffic owner.
        - [x] Retire stale Compose `borealis-engine-postgres-db` containers and remove `postgres-db` from Compose.
    - [x] Preserve backup/export semantics.
        - [x] Keep Traefik ACME storage strict and Backup/Restore-readable while allowing Traefik restart/renewal.
        - [x] Add `postgres-db shadow-import` validation path that restores a logical Compose DB snapshot into the K3s shadow DB without changing live traffic ownership.
        - [x] Operator completed encrypted WebUI configuration export before cutover.
    - [x] No HA/replication in v1 StatefulSet.
    - [x] No automatic PVC deletion.
- [x] Validation:
    - [x] Static `Engine.sh` syntax passes after Longhorn reconcile addition.
    - [x] First live deploy reconciles Longhorn once.
    - [x] Second live deploy after explicit-only StorageClass guard does not churn Longhorn pods.
    - [x] Longhorn system pods are ready and StorageClass is available.
    - [x] Live validation found upstream Longhorn default annotation drift; Engine reconcile now clears it after manifest apply.
    - [x] Longhorn volume attaches, mounts, detaches, and reattaches cleanly on the single-node Engine host.
    - [x] Live deploy creates K3s `postgres-db-0` and `postgres-data-postgres-db-0` without changing Compose DB traffic owner.
    - [x] K3s PostgreSQL readiness passes on the shadow StatefulSet.
    - [x] Logical backup/restore tested through `pg_dump` from Compose PostgreSQL and `pg_restore` into K3s shadow PostgreSQL.
    - [x] Existing Engine data imports cleanly into K3s shadow PostgreSQL.
    - [x] Final cutover imports existing Engine data into authoritative K3s PostgreSQL.
    - [x] API backend, job-scheduler, and site-worker pods resolve `BOREALIS_DATABASE_URL` to `postgres-db.borealis.svc`.
    - [x] K3s PostgreSQL PVC is bound to Longhorn.
    - [x] Second live redeploy keeps PostgreSQL on K3s and does not rerun Compose snapshot/import.
    - [ ] Encrypted WebUI Backup/Restore import is validated against K3s PostgreSQL.
    - [x] DB profile settings preserved or documented.

## Stage 10: WireGuard Cutover

- [x] Move `wireguard-tunnel` into K3s pinned host-network pod.
    - [x] Preserve `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, control socket boundary.
    - [x] Preserve firewall chains and peer `/32` policy.
    - [x] Route queued WireGuard reconcile/recover actions from K3s `job-scheduler` through the mounted control socket instead of the Docker helper bridge.
- [ ] Validation:
    - [x] Existing agents reconnect.
        - [x] SQL worker audit shows K3s site-worker socket counts match online device counts after redeploy.
        - [x] WireGuard status shows fresh peer handshakes from the K3s host-network pod.
    - [x] Remote shell/desktop over tunnel works.
        - [x] API and K3s site-worker pods resolve and connect to `remote-desktop-guacd.borealis.svc.cluster.local:4822`.
        - [x] Operator browser smoke after guacd traffic-owner env propagation.
        - [x] WebUI login, Server Info, Device Inventory, Remote Desktop, Remote Shell, File Management, Process Management, and Service Management pass after WireGuard and guacd cutover.
    - [x] Focused scheduler tests confirm K3s WireGuard reconcile sends only predefined `reconcile` command to the control socket and does not require the helper bridge.
    - [x] Live Server Info `Recover Listener` action validates queued scheduler-to-control-socket path after redeploy.
    - [ ] Quarantine/revocation removes peer access.

## Stage 11: Compose Retirement

- [x] Remove Compose ownership only after all K3s-owned services are stable.
    - [x] Compose `webui-frontend` retired.
    - [x] Compose `api-backend` retired.
    - [x] Compose `job-scheduler` retired.
    - [x] Compose `postgres-db` retired.
    - [x] Compose `wireguard-tunnel` retired.
    - [x] Compose `remote-desktop-guacd` retired.
    - [x] Move `traefik-edge` into K3s as the public edge owner.
    - [x] Preserve HTTP/HTTPS host-network ports, ACME/local CA state, and watched dynamic route files.
    - [x] Route `traefik-edge reload` through K3s `job-scheduler` and `borealis-operator`, not a Docker helper.
    - [x] Retire `site-worker-orchestrator` as a long-running Compose service.
    - [x] Leave `compose.yaml` as an empty retired manifest.
    - [x] Move Server Overview service rows for K3s-owned workloads off retired Compose container lookups and onto `borealis-operator` workload status.
    - [x] Expose WebUI restart as an operator-routed K3s action so simple WebUI pod restarts no longer need the helper bridge.
    - [x] Keep WebUI rebuilds CLI-only through `Engine.sh --service webui-frontend rebuild prod|dev`; reject queued runtime WebUI rebuild helper actions.
    - [x] Route K3s API, WebUI, PostgreSQL, and guacd restarts through `borealis-operator` without Docker-helper fallback.
    - [x] Route K3s PostgreSQL restart service actions through `borealis-operator` instead of the Docker helper bridge.
    - [x] Route K3s WireGuard reconcile service actions through the scheduler-mounted control socket instead of the Docker helper bridge.
    - [x] Remove the `site-worker-orchestrator` service-action helper path after Traefik edge cutover.
    - [x] Remove unused packaged `site-worker-orchestrator` healthcheck wrapper from the K3s `job-scheduler` image.
    - [x] Remove remaining Compose bridge service rows after Traefik and orchestrator retirement.
    - [x] Stop Docker metadata reads for K3s site-worker rows once operator metrics are present.
    - [x] Retire Compose `docker-proxy` after K3s worker metrics and scheduler service snapshots became authoritative.
    - [x] Remove Docker CLI, Docker Compose plugin, and retired `compose.yaml` from the K3s `job-scheduler` image.
    - [x] Make empty, `auto`, and unknown site-worker lifecycle modes resolve to K3s instead of falling back to retired Docker helpers.
    - [x] Reject retired `site-worker-orchestrator` roles from the K3s `job-scheduler` entrypoint.
    - [x] Require `BOREALIS_ALLOW_LEGACY_DOCKER_SITE_WORKERS=1` before explicit Docker site-worker lifecycle modes can run.
    - [x] Reject retired `site-worker-orchestrator` roles from the `api-backend` container entrypoint.
    - [x] Remove retired `site-worker-orchestrator` resource-limit env generation from Engine deploy env.
    - [x] Remove unused Docker socket and service-action-helper env generation after Docker helper retirement.
    - [x] Remove unreachable Docker Compose service reconciliation branches from full Engine deploy.
    - [x] Remove retired Compose service-action rebuild fallback, old Compose schema initializer, and unused `docker compose up` helper.
    - [x] Narrow remaining legacy Docker status code to PostgreSQL snapshot support and shared retired-container cleanup.
    - [x] Stop Engine deploy from installing/requiring Docker Compose plugin and retarget legacy dump import helper to K3s PostgreSQL.
    - [x] Clean stale post-retirement docs that still described Compose as deployment/runtime tooling.
    - [x] Rename operator RBAC from stale `readonly` naming to lifecycle-scoped controller naming and add old RBAC cleanup.
- [ ] Keep migration and recovery docs until stable release.
- [x] Update `Docs/Reference/Core Runtimes/Stack_Breakdown.md`, `engine-runtime.md`, `security-whitepaper.md`, SBOM if dependencies changed.
- [ ] Validation:
    - [x] Fresh install path works.
    - [x] Redeploy path works.
    - [ ] Backup/restore path works.
    - [x] Operator WebUI smoke passes after Compose retirement: login, Server Info, Device Inventory, Remote Desktop, Remote Shell, Files, Process Management, Service Management, Registry, and Backup Export.
    - [x] Narrow Engine tests pass.
    - [x] Compose policy confirms retired services stay out of `compose.yaml`.
    - [x] Live Docker check confirms retired Compose containers are absent.
    - [x] `docker compose config --services` returns no service names.
    - [x] Server Overview unit tests confirm retired workloads render as K3s rows and legacy Compose snapshots are ignored.
    - [x] Server action tests confirm WebUI restart uses the operator path and WebUI rebuild is rejected from queued runtime helper paths.
    - [x] Scheduler tests confirm Traefik reload uses the operator path.
    - [x] Orchestrator tests confirm the helper path is retired.
    - [x] `Engine.sh --network-mode public --service webui-frontend restart prod` rolls the K3s WebUI Deployment without restoring the retired Compose WebUI container.
    - [x] Live deploy table reports K3s Traefik edge ready and Docker Compose retired.
    - [x] Live K3s rollout confirms `deployment/traefik-edge` healthy.
    - [x] Live redeploy applies `borealis-operator-controller` RBAC and removes legacy `borealis-operator-readonly` Role/RoleBinding.
    - [x] Live Docker check confirms stale `borealis-engine-traefik-edge` and `borealis-engine-site-worker-orchestrator` containers are absent.
    - [x] Docker cache corruption during required image restore is recoverable through builder-cache prune plus no-cache rebuild.

## Open Risks

- [ ] `borealis-operator` RBAC must stay narrower than Docker socket power.
- [ ] Host networking must be minimized except WireGuard/edge needs.
- [ ] Runtime service actions must not become raw Kubernetes mutation API.
- [ ] K3s secrets must not replace Aegis security model.
- [ ] Future PostgreSQL rollouts must avoid unnecessary site-worker churn and agent disconnects.
- [ ] Agents that already hold stale connected Socket.IO state may require Agent release rollout or manual service restart before the new reconnect logic is active.
- [ ] Longhorn adds CSI/storage-manager dependencies that must be reconciled idempotently before PVC workloads depend on it.
- [ ] Stateful data migration must have reversible checkpoints and no automatic Longhorn volume/PVC deletion during normal deploy.
- [ ] Full Engine WebUI unit lane needs K3s-compatible runtime test-cache staging. Tracked by [Technical Debt #377](https://github.com/bunny-lab-io/Borealis/issues/377).

??? example "Detailed Codex Breakdown"

    ### Coordination surfaces

    - Branch: `migrate-borealis-docker-compose-into-k3s-kubernetes-cluster`.
    - Pull request: `Migrate Borealis Docker Compose into k3s Kubernetes Cluster`.
    - PR body is the active milestone checklist. Update it after each completed stage or meaningful milestone.
    - PR comments are the handoff log for staged work sessions. Add `Completed`, `Validation`, `Current risks`, and `Next step` after each meaningful work session.

    ### Related documentation

    - [Engine Deployment](../../Engine/deploying-the-engine.md) for current operator install and redeploy flow.
    - [Stack Breakdown](../Core%20Runtimes/Stack_Breakdown.md) for current K3s service boundaries and retired Compose state.
    - [Engine Runtime](../Core%20Runtimes/engine-runtime.md) for Engine runtime paths and generated state.
    - [Security Whitepaper](../security-whitepaper.md) for Aegis, runtime trust, token, and network boundaries.
    - [Backup and Restore](../backup-restore.md) for stateful data checkpoints.
    - [Database Reference](../Data%20and%20Schema/db-reference.md) for PostgreSQL behavior and connection lifecycle.

    ### Source map

    - Engine deployment entrypoint: `Engine.sh`.
    - Engine container source and Compose policy: `Data/Engine/Containers/`.
    - Compose policy check: `Data/Engine/Containers/check-compose-policy.py`.
    - Web UI source: `Data/Engine/Containers/webui-frontend/data/web-interface/src/`.
    - API backend source: `Data/Engine/Containers/api-backend/data/`.
    - Job scheduler source: `Data/Engine/Containers/job-scheduler/data/`.
    - Current site worker orchestration code should be treated as bridge input until `borealis-operator` replaces it.

    ### Validation path

    - Roadmap-only changes require documentation review, path review, and `git status --short --branch`.
    - Stage 1 deploy script changes start with `bash -n Engine.sh`.
    - Compose-sensitive changes include `python3 Data/Engine/Containers/check-compose-policy.py`.
    - Runtime cutover stages require deploy twice back to back, cluster health checks, old-runtime retirement validation, and narrow Engine tests for touched backend areas.
    - Stage 3 validation includes a no-op live rollout against the current `borealis-operator` image, rejection checks for unknown services and unallowlisted mutable images, RBAC `can-i` checks for allowed named workload patching and denied Secret/Node access, and unit coverage for failed rollout rollback.
    - Longhorn validation starts before the first PVC-backed workload cutover. Confirm StorageClass presence, Longhorn manager/CSI pod readiness, volume attachment, pod restart persistence, and no PVC/volume deletion during repeated Engine deploys.

    ### Security constraints

    - `borealis-operator` is the planned runtime K3s writer. `Engine.sh` remains the deployment-time writer for cluster bootstrap and fixed operator manifests.
    - Runtime services call operator verbs, not Kubernetes APIs.
    - Operator verbs must map to known Borealis workloads and fixed pod templates.
    - No raw YAML apply, arbitrary pod spec, arbitrary image/env/volume/service account, or broad host access should be exposed through runtime APIs.
    - K3s Secrets and RBAC help isolate runtime state but do not replace Aegis for protected Borealis secrets.
    - Longhorn provides K3s persistent storage for workloads that need PVCs; it does not replace Borealis backup/restore, Aegis, or explicit data-migration checkpoints.
