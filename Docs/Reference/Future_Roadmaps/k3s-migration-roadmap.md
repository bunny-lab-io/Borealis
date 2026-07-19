# K3s Migration Roadmap

Borealis will migrate the Engine runtime from Docker Compose into single-node K3s through staged bridge rollout. The existing Engine deployment command shape stays intact while each workload moves only after explicit validation and rollback planning.

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
- [ ] Use Longhorn as the default K3s PVC/persistent volume backend for workloads that need durable container storage.

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
    - [x] Mount `node_modules/.vite` as writable memory-backed cache for Compose WebUI.
    - [x] Mount `node_modules/.vite` as writable memory-backed cache for K3s WebUI bridge pods.
    - [x] Keep WebUI runtime source mounts read-only.
- [x] Fix DEV-mode WebUI runtime source staging.
    - [x] Sync staged WebUI source into `Engine/Services/webui-frontend/data/web-interface/` on every `deploy dev`.
    - [x] Sync staged WebUI source during `--service webui-frontend rebuild dev`.
    - [x] Keep production deploys on seed-if-missing behavior unless `BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE=1`.
- [x] Validation:
    - [x] WebUI dev path works.
    - [x] Guacd readiness passes.
    - [x] Existing Compose fallback remains available.
    - [x] Scoped WebUI and guacd rebuilds refresh bridge workloads without full deploy.
    - [x] Unchanged bridge pod UIDs stay stable during scoped bridge reconciliation.
    - [x] DEV Vite dependency modules return JavaScript MIME through public Traefik after scoped WebUI rebuild.
    - [x] DEV bootstrap runtime no longer opens root `/socket.io` during normal page load or operator-presence sync.
    - [x] `/api/server/timezones` reaches authenticated Go backend route instead of returning an API 404.
    - [x] DEV HMR source sync verified after next operator redeploy.

## Stage 5: Site Worker Migration

- [ ] Move dynamic site workers from Docker launch to operator-managed K3s pods.
    - [x] Add `job-scheduler` lifecycle mode switch with Docker fallback through `BOREALIS_SITE_WORKER_LIFECYCLE_MODE`.
    - [x] Route K3s site-worker launch/retire/list operations through `borealis-operator` allowlisted verbs.
    - [x] Preserve one active worker per site in v1 through scheduler reconciliation.
    - [x] Preserve worker idle TTL and stale worker retirement in the K3s bridge path.
    - [x] Preserve non-root UID/GID, dropped capabilities, read-only root, memory-backed `/tmp`, fixed resource requests/limits, and no ServiceAccount token.
    - [x] Retire legacy Docker site-worker containers through `site-worker-orchestrator` after K3s worker listing succeeds.
    - [x] Retire terminal K3s site-worker pods so failed pods do not block replacement workers.
    - [x] Restart K3s site-worker containers on nonzero exit while preserving idle TTL clean exits.
    - [x] Treat persistent transient heartbeat deadlocks as skipped heartbeat cycles instead of fatal worker exits.
    - [x] Preserve Agent Socket.IO registration after live redeploy validation.
    - [x] Name new K3s site-worker pods with deterministic sanitized site slugs for operator-readable Kubernetes and Server Info views.
    - [x] Enforce unique, bounded site-worker slugs on site create and rename so K3s worker names do not need UUID suffixes.
- [x] Replace Traefik route-file behavior with a controlled host-loopback bridge route while Compose API/PostgreSQL stay localhost-only.
    - [x] Keep route files owned by `job-scheduler`.
    - [x] Keep worker listeners bound to `127.0.0.1` inside host-network K3s pods.
    - [ ] Replace temporary host-loopback worker bridge with ClusterIP-only routing after API/PostgreSQL cutover makes it practical.
- [ ] Validation:
    - [x] Focused operator/scheduler unit tests pass.
    - [x] Static `Engine.sh` and Python worker checks pass.
    - [x] Agents connect to site worker through existing enrollment/token flow.
    - [x] Remote shell works through K3s site workers.
    - [x] File management works through K3s site workers.
    - [x] Process management works through K3s site workers.
    - [x] Service management works through K3s site workers.
    - [x] Connected Devices column reflects K3s worker registrations.
    - [x] Add Metrics Server-backed K3s site-worker CPU/RAM plumbing through `borealis-operator`.
    - [ ] K3s resource metrics appear in Sites and Server Info.
    - [x] Stale workers retire cleanly.

## Stage 6: Production WebUI Cutover

- [ ] Make K3s `webui-frontend` authoritative.
    - [ ] Stop/disable Compose WebUI counterpart only after route validation.
    - [ ] Keep rollback path to Compose.
- [ ] Validation:
    - [ ] Public and local network modes serve WebUI.
    - [ ] Static assets and SPA fallback work.
    - [ ] Repeated deploys do not restart unchanged WebUI pod.

## Stage 7: API Backend Cutover

- [ ] Move `api-backend` into K3s as one replica.
    - [ ] Preserve Aegis bootstrap/unlock behavior.
    - [ ] Preserve internal API token behavior.
    - [ ] Preserve logs/secrets/cache path contracts through Longhorn PVC/Secret/ConfigMap mapping where durable pod-local storage is required.
- [ ] Validation:
    - [ ] `/health` passes.
    - [ ] Login, Aegis unlock, enrollment, heartbeat, API routes pass.
    - [ ] Realtime SSE works with one replica.
    - [ ] Compose fallback path remains documented until retired.

## Stage 8: Scheduler Cutover

- [ ] Move `job-scheduler` into K3s as one replica.
    - [ ] Use `borealis-operator` for workload/site-worker lifecycle.
    - [ ] Preserve Postgres work leases and queue behavior.
- [ ] Validation:
    - [ ] Scheduled job tick creates expected runs.
    - [ ] Service actions route through operator.
    - [ ] No duplicate scheduler loops.

## Stage 9: PostgreSQL Cutover

- [ ] Move `postgres-db` into K3s StatefulSet with one Longhorn-backed PVC.
    - [ ] Install/reconcile Longhorn before the first PVC-backed workload cutover if it is not already present.
    - [ ] Set Borealis workload manifests to use the Longhorn StorageClass explicitly or through a Borealis-managed default.
    - [ ] Preserve backup/restore semantics.
    - [ ] No HA/replication in v1.
    - [ ] No automatic PVC deletion.
- [ ] Validation:
    - [ ] Longhorn system pods are ready and StorageClass is available.
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
- [ ] Keep migration/rollback docs until stable release.
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
    - Runtime cutover stages require deploy twice back to back, cluster health checks, Compose fallback validation, and narrow Engine tests for touched backend areas.
    - Stage 3 validation includes a no-op live rollout against the current `borealis-operator` image, rejection checks for unknown services and unallowlisted mutable images, RBAC `can-i` checks for allowed named workload patching and denied Secret/Node access, and unit coverage for failed rollout rollback.
    - Longhorn validation starts before the first PVC-backed workload cutover. Confirm StorageClass presence, Longhorn manager/CSI pod readiness, volume attachment, pod restart persistence, and no PVC/volume deletion during repeated Engine deploys.

    ### Security constraints

    - `borealis-operator` is the planned runtime K3s writer. `Engine.sh` remains the deployment-time writer for cluster bootstrap and fixed operator manifests.
    - Runtime services call operator verbs, not Kubernetes APIs.
    - Operator verbs must map to known Borealis workloads and fixed pod templates.
    - No raw YAML apply, arbitrary pod spec, arbitrary image/env/volume/service account, or broad host access should be exposed through runtime APIs.
    - K3s Secrets and RBAC help isolate runtime state but do not replace Aegis for protected Borealis secrets.
    - Longhorn provides K3s persistent storage for workloads that need PVCs; it does not replace Borealis backup/restore, Aegis, or explicit data-migration checkpoints.
