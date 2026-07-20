# K3s Migration Roadmap

Borealis will migrate the Engine runtime from Docker Compose into single-node K3s through staged bridge rollout. The existing Engine deployment command shape stays intact while each workload moves only after explicit validation and one-way retirement planning for the old runtime equivalent.

## Goal

Migrate Borealis Engine from Docker Compose into single-node K3s through staged bridge rollout. Preserve existing `Engine.sh --network-mode <public|local> deploy <prod|dev>` command shape and idempotent deployment behavior.

## Principles

- [x] Keep `Engine.sh deploy` idempotent: reconcile, do not recreate healthy K3s state.
- [x] Start with single-node non-HA K3s.
- [x] Keep Compose Engine authoritative until each workload reaches explicit cutover.
- [ ] Introduce `borealis-operator` as only K3s writer.
- [x] Keep `api-backend`, `job-scheduler`, and migrated workloads Kubernetes-blind or operator-API-only.
- [x] Use allowlisted Borealis verbs, never raw YAML/kubectl from runtime services.
- [x] Do not rotate secrets, delete PVCs, or teardown cluster during normal deploy.
- [ ] Use Longhorn as the Borealis K3s PVC/persistent volume backend for workloads that need durable container storage.

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
    - [ ] Replace future `site-worker-orchestrator` role.
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
- [x] Replace Traefik route-file behavior with a controlled host-loopback bridge route while Compose API/PostgreSQL stay localhost-only.
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
    - [ ] Redeploy validates K3s site workers survive transient Compose PostgreSQL restarts without disconnecting the fleet.
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
    - [x] Bind bridge API to host loopback port `5001` while Compose API owns `127.0.0.1:5000`.
    - [x] Mirror generated runtime env into `borealis-api-backend-runtime-env`.
    - [x] Disable API-owned background loops in the bridge pod with `BOREALIS_API_BACKGROUND_LOOPS=0`.
    - [x] Use `Recreate` rollout strategy for the host-network bridge so single-node K3s does not deadlock on fixed port `5001`.
    - [ ] Make K3s `api-backend` authoritative.
    - [ ] Remove Compose `api-backend` after route/internal-caller validation.
    - [ ] Preserve Aegis bootstrap/unlock behavior.
    - [ ] Preserve internal API token behavior.
    - [x] Preserve logs/secrets/cache path contracts through fixed hostPath bridge mounts.
    - [ ] Replace fixed hostPath bridge mounts with Longhorn PVC/Secret/ConfigMap mapping where durable pod-local storage is required.
- [ ] Validation:
    - [x] Bridge pod rollout passes.
    - [x] `curl -fsS http://127.0.0.1:5001/health` passes.
    - [ ] `/health` passes.
    - [ ] Login, Aegis unlock, enrollment, heartbeat, API routes pass.
    - [ ] Realtime SSE works with one replica.
    - [ ] Compose API removal plan is documented after K3s API validation passes.

## Stage 8: Scheduler Cutover

- [x] Move `job-scheduler` into K3s as one replica.
    - [x] Add fixed K3s `job-scheduler` Deployment with `Recreate` rollout strategy.
    - [x] Remove Compose `job-scheduler` service and retire stale `borealis-engine-job-scheduler` containers during deploy.
    - [x] Use `borealis-operator` for K3s workload/site-worker lifecycle where the scheduler can complete the queued action safely.
    - [x] Preserve Postgres work leases and queue behavior through the existing scheduler manager and work-item tables.
    - [x] Keep scheduler Kubernetes-blind: no ServiceAccount token, no kubeconfig, no Docker socket.
    - [x] Keep temporary host-loopback access to the K3s API bridge and Compose PostgreSQL until API/PostgreSQL cutover.
- [ ] Validation:
    - [ ] Scheduled job tick creates expected runs after live redeploy.
    - [x] Service actions route through operator for K3s-owned restart paths in focused Go tests.
    - [x] No duplicate scheduler loops in deployment model: Compose service removed, stale container retired before K3s rollout, and K3s Deployment uses `Recreate`.
    - [x] Live redeploy confirms exactly one K3s scheduler pod and no Compose `borealis-engine-job-scheduler` container.

## Stage 9: PostgreSQL Cutover

- [ ] Move `postgres-db` into K3s StatefulSet with one Longhorn-backed PVC.
    - [x] Add `Engine.sh deploy` Longhorn reconcile phase before the first PVC-backed workload cutover.
    - [x] Install/reconcile Longhorn from pinned manifest version `v1.12.0` if it is enabled and not already present.
    - [x] Reconcile Longhorn host iSCSI prerequisites without deleting existing Longhorn resources or PVCs.
    - [x] Keep K3s `local-path` default unchanged for now; future Borealis PVC manifests should request `BOREALIS_K3S_PVC_STORAGE_CLASS` explicitly.
    - [x] Override Longhorn manifest default-StorageClass annotation so Longhorn stays explicit-use only.
    - [ ] Set PostgreSQL StatefulSet manifests to use the Longhorn StorageClass.
    - [ ] Preserve backup/restore semantics.
    - [ ] No HA/replication in v1.
    - [ ] No automatic PVC deletion.
- [ ] Validation:
    - [x] Static `Engine.sh` syntax passes after Longhorn reconcile addition.
    - [x] First live deploy reconciles Longhorn once.
    - [ ] Second live deploy after explicit-only StorageClass guard does not churn Longhorn pods.
    - [x] Longhorn system pods are ready and StorageClass is available.
    - [x] Live validation found upstream Longhorn default annotation drift; Engine reconcile now clears it after manifest apply.
    - [ ] Longhorn volume attaches, mounts, detaches, and reattaches cleanly on the single-node Engine host.
    - [ ] Logical backup/restore tested.
    - [ ] Existing Engine data imports cleanly.
    - [ ] DB profile settings preserved or documented.

## Stage 10: WireGuard Cutover

- [ ] Move `wireguard-tunnel` into K3s pinned host-network pod.
    - [ ] Preserve `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, control socket boundary.
    - [ ] Preserve firewall chains and peer `/32` policy.
- [ ] Validation:
    - [ ] Existing agents reconnect.
    - [ ] Remote shell/desktop over tunnel works.
    - [ ] Quarantine/revocation removes peer access.

## Stage 11: Compose Retirement

- [ ] Remove Compose ownership only after all K3s-owned services are stable.
- [ ] Keep migration and recovery docs until stable release.
- [ ] Update `Docs/Reference/Core Runtimes/Stack_Breakdown.md`, `engine-runtime.md`, `security-whitepaper.md`, SBOM if dependencies changed.
- [ ] Validation:
    - [ ] Fresh install path works.
    - [ ] Redeploy path works.
    - [ ] Backup/restore path works.
    - [ ] Narrow Engine tests pass.

## Open Risks

- [ ] `borealis-operator` RBAC must stay narrower than Docker socket power.
- [ ] Host networking must be minimized except WireGuard/edge needs.
- [ ] Runtime service actions must not become raw Kubernetes mutation API.
- [ ] K3s secrets must not replace Aegis security model.
- [ ] Compose-owned PostgreSQL can still bounce during bridge-stage deploys; K3s site workers must tolerate transient DB outage until PostgreSQL cutover.
- [ ] Agents that already hold stale connected Socket.IO state may require Agent release rollout or manual service restart before the new reconnect logic is active.
- [ ] Longhorn adds CSI/storage-manager dependencies that must be reconciled idempotently before PVC workloads depend on it.
- [ ] Stateful data migration must have reversible checkpoints and no automatic Longhorn volume/PVC deletion during normal deploy.

??? example "Detailed Codex Breakdown"

    ### Coordination surfaces

    - Branch: `migrate-borealis-docker-compose-into-k3s-kubernetes-cluster`.
    - Pull request: `Migrate Borealis Docker Compose into k3s Kubernetes Cluster`.
    - PR body is the active milestone checklist. Update it after each completed stage or meaningful milestone.
    - PR comments are the handoff log for staged work sessions. Add `Completed`, `Validation`, `Current risks`, and `Next step` after each meaningful work session.

    ### Related documentation

    - [Engine Deployment](../../Engine/deploying-the-engine.md) for current operator install and redeploy flow.
    - [Stack Breakdown](../Core%20Runtimes/Stack_Breakdown.md) for current Compose service boundaries.
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
