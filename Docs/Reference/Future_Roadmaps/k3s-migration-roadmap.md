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

- [ ] Add restricted operator lifecycle verbs.
    - [ ] `RolloutKnownWorkload(service_key, image_ref)`
    - [ ] `RestartKnownWorkload(service_key)`
    - [ ] `ScaleKnownWorkload(service_key, replicas)`
    - [ ] `LaunchSiteWorker(site_id, worker_guid, image_ref, resource_profile)`
    - [ ] `RetireSiteWorker(worker_guid, reason)`
- [ ] Add image allowlist and digest/hash validation.
- [ ] Keep build work in `Engine.sh`, not runtime services.
- [ ] Validation:
    - [ ] Known service rollout succeeds with valid image.
    - [ ] Unknown service/image is rejected.
    - [ ] Failed rollout leaves previous healthy workload serving.

## Stage 4: First Low-Risk Workloads

- [ ] Move `webui-frontend` dev workload into K3s bridge mode.
    - [ ] Preserve dev HMR path where practical.
    - [ ] Keep Compose Traefik/API authoritative.
- [ ] Move `remote-desktop-guacd` into K3s bridge mode.
    - [ ] Keep ClusterIP-only exposure.
    - [ ] Preserve existing VNC/Guacamole API behavior.
- [ ] Validation:
    - [ ] WebUI dev path works.
    - [ ] Guacd readiness passes.
    - [ ] Existing Compose fallback remains available.

## Stage 5: Site Worker Migration

- [ ] Move dynamic site workers from Docker launch to operator-managed K3s pods.
    - [ ] Preserve one active worker per site in v1.
    - [ ] Preserve Agent Socket.IO registration.
    - [ ] Preserve worker idle TTL and stale worker retirement.
    - [ ] Preserve resource caps, no-new-privileges, dropped capabilities, read-only root, tmpfs.
- [ ] Replace Traefik route-file behavior with Kubernetes-native Service/Ingress routing or controlled bridge route.
- [ ] Validation:
    - [ ] Agents connect to site worker through existing enrollment/token flow.
    - [ ] Remote shell, file management, service/process actions work.
    - [ ] Worker metrics appear in Sites and Server Info.
    - [ ] Stale workers retire cleanly.

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
    - [ ] Preserve logs/secrets/cache path contracts through PVC/Secret/ConfigMap mapping.
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

- [ ] Move `postgres-db` into K3s StatefulSet with one PVC.
    - [ ] Preserve backup/restore semantics.
    - [ ] No HA/replication in v1.
    - [ ] No automatic PVC deletion.
- [ ] Validation:
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
- [ ] Stateful data migration must have reversible checkpoints.

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

    ### Security constraints

    - `borealis-operator` is the only planned K3s writer.
    - Runtime services call operator verbs, not Kubernetes APIs.
    - Operator verbs must map to known Borealis workloads and fixed pod templates.
    - No raw YAML apply, arbitrary pod spec, arbitrary image/env/volume/service account, or broad host access should be exposed through runtime APIs.
    - K3s Secrets and RBAC help isolate runtime state but do not replace Aegis for protected Borealis secrets.
