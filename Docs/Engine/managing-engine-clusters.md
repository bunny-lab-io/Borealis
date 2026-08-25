# Managing Engine Clusters
Borealis Engine clustering runs application workloads, K3s control-plane services, embedded etcd, and PostgreSQL across homogeneous Engine nodes. Current release supports one or three active Ubuntu nodes on same Layer 2 network, plus temporary two-of-three degraded membership while replacing externally fenced failed node. Odd-numbered expansion or shrinking beyond three nodes remains future roadmap work and is fenced from operator interfaces.

!!! danger "Qualification gate"
    Cluster conversion remains disabled until exact installed stable K3s version passes Borealis guarded-probe conformance and disposable three-node qualification. Do not copy or fabricate conformance record, use release candidate, fork Kubernetes, or bypass gate. Current API and WebUI refuse expansion beyond three active nodes. Production conversion needs separate operator approval.

## Requirements

- Ubuntu 24.04 or newer on every node.
- Same CPU architecture and Borealis sizing profile.
- Static private IPv4 addresses on same Layer 2 network.
- Separate unused IPv4 addresses for K3s control-plane VIP and Borealis edge VIP.
- Explicit `BOREALIS_K3S_PEER_CIDRS` allowlist covering every current and planned cluster management address. Prefer one `/32` entry per Engine node.
- Peer access on K3s/Spegel TCP ports `6443` and `5001`; Borealis firewall manages both from same allowlist.
- Clean Git worktrees using configured repository origin and same stable Borealis release.
- Working Longhorn iSCSI and NFSv4 client prerequisites plus enough capacity to run node-local candidates during drain. Normal Engine deployment installs missing host packages.
- Supported odd active membership: one or three nodes. Five-plus expansion and shrinking from five-plus membership remain future roadmap work.
- Content-addressed `rancher/k3s-upgrade` image in `BOREALIS_K3S_UPGRADE_IMAGE` before requesting K3s control-plane update.

## Enable First Node

Run probe conformance against installed stable K3s before requesting conversion:

```sh
export BOREALIS_K3S_PEER_CIDRS=192.0.2.21/32,192.0.2.22/32,192.0.2.23/32
sudo bash Data/Engine/K3s/cluster/run-probe-conformance.sh
sudo --preserve-env=BOREALIS_K3S_PEER_CIDRS \
  bash Engine.sh --network-mode <public-or-local> deploy prod

# Supply current Engine API URL and recently issued Admin token.
export BOREALIS_CLUSTER_API_URL=https://engine.example.com
export BOREALIS_CLUSTER_ADMIN_TOKEN=<recent-admin-access-token>
sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
  bash Engine.sh --cluster-enable \
  --control-plane-vip 192.0.2.10 \
  --edge-vip 192.0.2.11
```

Production redeploy after conformance publishes exact-version pass state to API workload. Cluster Management **Enable Cluster** performs same authenticated operation without CLI environment variables.

Enablement creates cluster CRDs, controller RBAC, node manager, fixed VIP resources, per-node workloads, application availability policies, and CloudNativePG migration workflow. Existing standalone PostgreSQL remains authoritative until logical import validates and traffic cuts over. Cutover stops scheduler and operator assignments, gracefully drains site workers, then pauses remaining database clients before dump. Old PVC plus encrypted dump remain retained after old StatefulSet scales down.

If API restarts while CLI watches enablement, CLI reconnects for one minute without submitting second request. If connection stays unavailable, copy displayed operation ID and inspect Operations view before trying anything again. Accepted operation keeps running server-side.

!!! warning "One-way database cutover"
    Cluster enablement does not automatically move database traffic back into standalone StatefulSet. Restore from retained old PVC or encrypted dump is explicit recovery work.

## Add or Replace Nodes

Create invitation in Cluster Management, copy K3s server token to root-only file on each new host through trusted management channel, then run node-manager join command. Include same private peer allowlist used on first node:

```sh
sudo borealis-node-manager join \
  --endpoint https://engine.example.com \
  --invite-bundle '<one-use-invitation>' \
  --node-name engine-02 \
  --management-ip 192.0.2.22 \
  --peer-cidrs 192.0.2.21/32,192.0.2.22/32,192.0.2.23/32 \
  --k3s-server https://192.0.2.10:6443 \
  --k3s-token-file /root/borealis-k3s-server.token
```

Node manager first prepares firewall, iSCSI, NFSv4, and K3s host prerequisites through fixed Engine workflow. It does not expose arbitrary shell execution. Invitation join then creates `Pending Quorum` admission and waits for Admin approval. Normal `1 -> 3` expansion requires complete pair. After externally fenced emergency removal leaves `2 active / 3 desired`, replacement requires one invitation and one approval. Approved members remain application-drained and role-ineligible. Probe conformance runs while pinned to each joining node. Borealis then expands CloudNativePG, deploys isolated pinned candidates, probes and soaks them, promotes them into active workloads, enables normal HA role eligibility, verifies active workload and WireGuard health through another soak, clears application drain, and records restored three-node membership.

!!! info "Current membership limit"
    Three active nodes is current release maximum. Cluster Management disables invitation, admission approval, and pair-expansion controls after three nodes become active. Single-invitation replacement becomes available only in recorded `2 active / 3 desired` `Degraded Quorum` state. Public API and transactional store enforce same limit and replacement state, including stale invitation and pending-admission races. Odd-numbered expansion or shrinking beyond three nodes needs separate future implementation, qualification, issue, and pull request.

Temporary even K3s membership during pair admission does not disable healthy existing nodes. Architecture, Ubuntu version, hostname, node name, management IPv4, invitation lifetime, and invitation authentication are validated before membership work.

Safe downscale supports `3 -> 1` in current release. Select both targets from Nodes view and type `REMOVE NODE PAIR`. Controller snapshots state, moves PostgreSQL and application leaders onto surviving member, scales CloudNativePG, and refuses fencing unless PostgreSQL vacated both targets. Each target then drains, writes persistent removal-fence marker, schedules local K3s disable/stop, becomes NotReady, and gets deleted through Kubernetes Node API. Remaining servers must stay `Ready=True` and `EtcdIsVoter=True` through soak before next target starts.

Emergency removal is single-node recovery for host already unreachable. Power target off through external management first, then type both `TARGET IS POWERED OFF` and `EMERGENCY REMOVE NODE`. Borealis never contacts target in this path. It deletes Node resource, verifies surviving etcd voters, scales PostgreSQL to two instances with one synchronous acknowledgement, and records `2 active / 3 desired` `Degraded Quorum`. Create one new invitation and approve its replacement admission to restore supported three-node membership. Normal cluster-changing operations stay blocked during replacement recovery.

!!! danger "Never fake emergency fencing"
    Deleting live K3s server membership can let removed host retain or rebuild conflicting local control-plane state. Emergency confirmation asserts host cannot run or rejoin. Safe path uses node-manager persistent fence instead.

## Read Cluster State

Open **Admin > Cluster Management**. Views show:

- Overview: quorum, active release, HMR state, operation lease.
- Nodes: etcd leader, control VIP, edge VIP, PostgreSQL primary, scheduler leader, WireGuard owner, drain reason, release, and probe state.
- Database: primary/replicas, synchronous durability, switchovers, snapshots.
- Updates: compatible stable releases, per-node update, ordered update-all.
- Operations: durable state, event history, retry, recovery, cancellation.
- Maintenance: paired admission/removal, maintenance drain, emergency actions.

One exclusive cluster operation may mutate placement, membership, HMR, or releases at time. Controller state survives controller restart through PostgreSQL operation records and Kubernetes desired/runtime objects.

Role owners and PostgreSQL primary display operator-facing node names while APIs retain immutable node IDs. Node cards separate membership and application state, and show K3s version as metadata rather than role. Database view reports configured and Ready CloudNativePG instances separately. `Degraded Database` blocks normal cluster-changing operations until all configured instances return Ready; emergency PostgreSQL failover, externally fenced emergency removal, maintenance exit, and HMR exit remain available for recovery. Required synchronous durability may remain available while redundancy is reduced.

Operations keeps failed records for audit. Older cluster-enable or membership failure becomes `superseded` after newer same-kind operation succeeds; superseded record cannot be retried. Technical error payload stays collapsed until operator opens details. Updates view explains empty catalog when no published stable cluster-compatible release exists at or above pinned baseline.

Node drain and restore boundaries update durable node state with Kubernetes action progress. Failed rolling update therefore keeps affected node visibly drained with operation reason until retry or explicit maintenance recovery proves health and reactivates it.

Maintenance admits one drained application node at time. Restore existing drained node before draining another. Durable application-capacity gate blocks updates, HMR start, membership changes, normal PostgreSQL switchovers, and additional maintenance while any active member remains drained. Successful maintenance recovery clears failed-operation degradation only after every recorded active application node is active and supported membership plus observed PostgreSQL health remain intact.

## Probe and Shutdown Contract

Borealis follows three distinct probe contracts:

- Startup proves initialization completed. Slow initialization belongs here.
- Readiness proves workload can serve current role. API and scheduler readiness includes required PostgreSQL access. WireGuard owner readiness requires interface and usable tunnel state; standby readiness requires controller socket healthy and shared interface withdrawn. Route DaemonSet separately proves owner routes use tunnel while standby routes use edge VIP.
- Liveness detects local recoverable process failure only. Scheduler liveness never depends on PostgreSQL.

Current stable K3s can incorrectly run liveness before startup after liveness restarts a container. Borealis makes that early execution harmless: every container using both probes delays liveness longer than startup probe's complete failure budget. Startup remains responsible for failed initialization, readiness keeps traffic away, and liveness begins only after startup either succeeds or performs its own restart. Exact-version qualification proves this delay resets across ten consecutive liveness-triggered replacements on every node. This is Borealis compatibility protection, not claim that upstream bug is fixed.

Drain sequence stops assignments, withdraws readiness, waits for node EndpointSlices to converge, forwards termination signals, and bounds in-flight drain inside `terminationGracePeriodSeconds`. Workloads use `preStop`, `minReadySeconds`, topology constraints, and disruption budgets.

Returning from maintenance uses reverse safety order. Borealis starts node workloads while node remains application-drained, waits for ready endpoints and minimum-ready soak, restores role eligibility, verifies WireGuard and role-aware health through another soak, then clears drain. Failed restore leaves node drained instead of sending work to partial service.

[ngrok probe guidance](https://ngrok.com/blog/probes) is sound: readiness must describe traffic eligibility while liveness should remain narrow. One caveat matters: Kubernetes starts endpoint removal and pod termination concurrently; Service withdrawal is not guaranteed to finish before `SIGTERM`. Borealis therefore combines readiness withdrawal, EndpointSlice verification, `preStop`, signal handling, and connection draining. See [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/) and [pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/).

## Cluster-Wide HMR

Starting `deploy dev` or `webui-frontend rebuild dev` on clustered Engine requires Admin step-up and typed `ENABLE HMR`. Non-interactive CLI also requires `--acknowledge-cluster-non-ha`.

> This moves all Borealis application traffic to this node and places every other Engine node in drained standby. Cluster loses application HA until production mode is restored.

Controller first checks quorum, capacity, target production health, and exclusive-operation state. It moves edge/WireGuard ownership, scheduler leadership, site workers, application endpoints, and caught-up PostgreSQL primary to selected HMR node. Standby nodes keep K3s, etcd, Longhorn, and PostgreSQL replicas running; application scheduling is drained without cordoning infrastructure.

Cluster banner remains visible on every page until exit completes. Updates, membership, and normal maintenance remain blocked during HMR.

Exit restores saved pinned production release, not local working tree. Local edits remain available for later commit/release. Controller first returns standby production nodes one at time; each stays drained until workload and role-aware health complete two minimum-ready soaks. It then moves roles away from HMR node, drains its development workloads, builds saved production revision as isolated candidate, probes and soaks candidate, promotes it, and repeats active plus role-aware health checks before clearing final drain. This ordering keeps production available while HMR node changes back. One-node cluster uses same candidate gates with expected maintenance interruption. Lost HMR node gets fenced before pinned production workloads recover on standby members through same sequence.

## Cluster-Aware Updates

Use Updates view instead of `git pull` on clustered Engines. Catalog shows published stable GitHub releases only. Drafts, prereleases, branch heads, nonnumeric tags, downgrades, and incompatible manifests cannot be selected.

Selected tag resolves once to immutable commit SHA. Controller records title, tag, SHA, source URL, initiator, and compatibility result. Every cluster-capable release must include `Data/Engine/release-manifest.json` declaring rolling source range, mixed-version window, schema phase, and K3s baseline.

`Update Node` and `Update All` use same node workflow:

1. Acquire exclusive operation lease and create pre-change snapshot.
2. Move PostgreSQL, edge/WireGuard, and scheduler leadership away when needed.
3. Stop assignments, withdraw readiness, drain work, and verify EndpointSlice withdrawal.
4. Fetch release and fast-forward checkout to stored SHA. Dirty or diverged worktree fails without stash/reset.
5. Invoke target revision's fixed image-staging workflow locally and build/import immutable images without starting candidate workloads.
6. When release declares `expand-contract`, run target image's fixed, idempotent expand-schema Job before first target candidate starts.
7. Create node-pinned candidates outside shared Services through separate labels and selectors. Require startup, readiness, liveness, direct endpoint, database, scheduler, Agent-path, and candidate soak checks.
8. Promote candidate into active Deployment, then require shared Service, VIP, database, scheduler, Agent-path, WireGuard, and ready-soak checks.
9. Restore role eligibility, repeat health and ready-soak checks, restore assignments, then clear application drain.
10. Run target image's finalize-schema Job only after every active node reports target SHA. Then verify cluster and advance baseline release.

Update All records immutable non-leader-first node order when request starts, then transfers roles and updates one application node at time. Runtime role movement cannot reorder or repeat nodes mid-operation. Failure halts operation and leaves failed node drained; healthy old/new nodes keep serving. Retry resumes explicit operation. No automatic code rollback or skip-and-continue occurs. Cluster release baseline advances only after all active nodes reach target.

One-node updates require maintenance-outage acknowledgement. Three-node continuity covers ready HTTP/API/Agent endpoints, durable queued work, graceful drain, and reconnect after socket movement. Existing interactive shell, desktop, or WebSocket sessions cannot transfer live.

`Update Node` may intentionally leave cluster in mixed-version state. Expand phase remains complete and contract phase stays pending until last active node reaches same target through later explicit update. Borealis records both phases by immutable release SHA, so controller restart or operator retry cannot apply completed phase twice.

Engine release update and K3s upgrade stay separate operations. Maintenance view accepts stable `vX.Y.Z+k3sN` target only. Target must be newer than current version and stay on current minor or advance exactly one minor. Source version must already pass Borealis probe conformance, and upgrade image must use `registry/repository@sha256:digest` form.

K3s controller takes pre-change snapshot, orders non-leaders before leaders, drains one application node, and creates exclusive system-upgrade-controller Plan selecting only that server. Plan concurrency stays one and cordons host while immutable `k3s-upgrade` image applies exact version. Borealis then requires Node Ready, etcd voter health, exact kubelet version, and local probe conformance. Application workloads start while node stays drained; Engine health, ready soak, restored-role health, and second soak must pass before drain clears and next server begins. Failed Plan or probe halts update and leaves node drained. One-node clusters require `ACCEPT OUTAGE`.

## Aegis Unlock Across Replicas

Aegis key stays memory-only. Clustered API replicas use cert-manager-issued TLS 1.3 mutual certificate endpoint on headless internal Service. One successful setup/unlock verifies key locally, resolves current API replica addresses, and sends bounded 32-byte key to every replica. Unlocked replicas continue bounded reconciliation so later replacement candidates receive key before promotion. Receiver re-verifies key against Aegis verification token before installing memory copy. Endpoint accepts no operator session, arbitrary payload, or non-mTLS caller. All-cold cluster restart still requires one operator unlock.

## Failure and Recovery Rules

- `Degraded Quorum` records emergency two-of-three membership after externally fenced removal. Healthy nodes remain enabled; single replacement admission remains available.
- Failed cluster operation also fails closed under `Degraded Quorum`. Retry remains normal recovery. When failed operation target is permanently unavailable and cluster still records three active members, externally fence target before using emergency removal; same two exact confirmations and three-member store checks still apply.
- `Degraded Database` records fewer Ready CloudNativePG instances than configured. Controller returns status to `Healthy` after all configured instances recover, but does not overwrite HMR, mixed-version, pending-membership, or operation-failure state.
- Planned PostgreSQL switchover creates pre-change backup before role movement. Emergency failover skips new backup because failed primary may be unable to produce one; use retained scheduled and prior pre-change backups when recovery needs older data.
- PostgreSQL synchronous quorum requires one replica acknowledgement on supported three-node clusters. Writes stop when durability quorum is unavailable; stale replica promotion is rejected.
- No automatic PostgreSQL or VIP failback. Operator chooses switchover after failed owner returns.
- Safe removal supports `3 -> 1`. Emergency removal from supported three-node membership needs external power fence plus two exact confirmations and records degraded state.
- Longhorn snapshots use daily fourteen-snapshot retention plus pre-change snapshots. They are in-cluster recovery, not disaster recovery.
- Aegis key remains memory-only. All-cold restart requires operator unlock.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - Cluster state/events: `GET /api/server/cluster`, `GET /api/server/cluster/events`.
    - Enable/invite/admit/scale: `POST /api/server/cluster/enable`, `/invitations`, `/admissions/{id}/approve`, `/membership/scale`. Current release accepts pair preparation and `desired_size=3` while active size is one, or one replacement while state is exactly `2 active / 3 desired` `Degraded Quorum`; size-five and stale invitation/admission paths fail closed.
    - Node and database operations: `POST /api/server/cluster/nodes/{id}/maintenance`, `/remove`, `/postgres/switchover`, `/postgres/emergency-failover`. Safe `3 -> 1` remove requires canonical `paired_node_id` plus `REMOVE NODE PAIR`; emergency remove requires `TARGET IS POWERED OFF` plus `EMERGENCY REMOVE NODE`.
    - Release/HMR/operations: `GET /api/server/cluster/releases`; `POST /api/server/cluster/hmr/start`, `/hmr/exit`, `/updates`, `/operations/{id}/retry`, `/operations/{id}/cancel`. Updates accepts distinct `update_type=engine|k3s`; K3s form uses all scope, stable 32-character-bounded version class, exact `UPDATE K3S`, and one-node outage acknowledgement.
    - Bootstrap: `POST /api/bootstrap/cluster/join`, `GET /api/bootstrap/cluster/join/{id}/events`.
    - Public handlers validate canonical UUIDs, dotted numeric release tags up to 32 characters, DNS hostnames up to 253 characters, node names up to 63 characters, single-line reasons up to 256 characters, authenticated invitation bundles up to 16 KiB, and fixed acknowledgements.

    ### Source map

    - API/store/controller: `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster*.go`, `cluster_controller.go`.
    - Fixed root helper: `Data/Engine/Containers/api-backend/cmd/borealis-node-manager/`.
    - Route daemon: `Data/Engine/Containers/api-backend/cmd/wireguard-route-daemon/`.
    - CRDs/controller/RBAC/availability: `Data/Engine/K3s/cluster/`.
    - WebUI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Cluster_Management.jsx`.
    - Release compatibility: `Data/Engine/release-manifest.json`.

    ### State ownership

    - `BorealisCluster`, `BorealisNodeAdmission`, `BorealisNodeRuntime`, and `BorealisClusterOperation` hold desired/runtime Kubernetes state. PostgreSQL remains durable audit and operation-event source.
    - Controller reconciles cluster size/VIPs/baseline/HMR state, recent admissions, recent operations, and one `BorealisNodeRuntime` per active Engine node. CR specs carry desired state; status carries observed phase, approval, operation, Kubernetes node, runtime-owner, probe, release, and drain state. `BorealisCluster` accepts active size two only for `2 active / 3 desired` `Degraded Quorum` replacement recovery. Ten-second repair loop recreates missing resources, patches drift, and avoids unchanged status writes.
    - PostgreSQL tables `cluster_state`, `cluster_nodes`, `cluster_admissions`, `cluster_operations`, `cluster_operation_events`, `cluster_audit_events`, `cluster_invitations`, `realtime_outbox`, `cluster_application_leases`, and `cluster_schema_phases` hold audit, events, invitations, outbox, singleton leases, and immutable schema-phase completion.
    - Controller observes CloudNativePG configured/Ready counts, current primary Pod, phase, full-readiness, and synchronous durability quorum. Observation persists under `cluster_state.config_json.database_runtime`, appears as top-level `database` in cluster snapshot, and drives recoverable `Degraded Database` status. Transactional mutation gates also read observed database runtime directly, so `Mixed Version` or another higher-priority lifecycle status cannot mask reduced database readiness. Historical failed operations remain immutable; snapshot annotates globally superseded cluster-enable/membership failures and retry path rejects them transactionally after later same-kind success.
    - Controller preflight admits only replacement admission, emergency PostgreSQL failover, and maintenance exit while durable membership is temporarily two-of-three. Store also permits externally fenced emergency removal from a failed-operation `Degraded Quorum` state only while three active members remain recorded; normal removal and second removal from replacement state stay rejected. Emergency PostgreSQL failover intentionally omits pre-change backup so failed primary cannot block promotion; planned switchover retains mandatory backup.
    - Controller persists `cluster_nodes.application_state` when node drain/exit steps cross their safe boundary. Enter-drain failure records conservative drained state because node manager writes drain label before scaling workloads, preventing API/UI from presenting failed update target as active. During steady state, unexpected Kubernetes `drained` label also records durable `k3s_restart_label_drift` drain and `Degraded Quorum`; controller never promotes observed `active` label into durable active state without explicit verified recovery.
    - Maintenance store validates active membership and current application state, permits one drained application node at time, and accepts exit only for drained target. Independent store/WebUI application-capacity gates keep normal operations closed while durable active-member drain exists; HMR exit, emergency PostgreSQL failover, maintenance exit, and externally fenced emergency removal stay available. Completion preserves `2/3` quorum degradation and remaining database degradation; verified three-node recovery returns Healthy only when no recorded active node remains drained.
    - Node manager accepts fixed verbs only, including persistent K3s membership fence, exact-version probe conformance, drained application preparation, and `RunSchemaPhase` limited to `expand|finalize` plus recorded target SHA. Preparation scales named node workloads and waits for rollouts without changing `borealis.io/application-state`; only final activation clears drain after controller health gates. Joined-server config initially persists drained, role-ineligible labels. Every successful enter/exit drain transition atomically rewrites those runtime labels in `20-borealis-cluster-join.yaml`, preventing later K3s restart from undoing controller-approved activation or maintenance fencing. Manager never exposes arbitrary command or remote-shell execution and stays active across controlled K3s restarts so enrollment and one-node-at-a-time K3s upgrades can wait for control plane recovery without losing operation process.
    - Rolling schema work runs target revision's `site-worker` image in non-root, tokenless, node-pinned Job. Expand follows target image build/import and precedes first candidate health gate. Finalize runs only after every active `cluster_nodes.release_sha` equals target. `cluster_schema_phases` makes both fixed phases restart-safe and idempotent; finalize refuses missing expand record.
    - Agent-path update probe sends credential-free `POST /api/agent/heartbeat` and requires exact `401 Unauthorized`. Candidate probe targets isolated Pod IP; active probe targets shared API Service. Expected auth rejection proves Agent route is registered and reachable without mutating Agent/device state or storing test credentials. Missing route (`404`), accepting unauthenticated traffic, timeout, or other status fails node health.
    - Blank-node join requires `--peer-cidrs`. Node manager validates and canonicalizes bounded private IPv4 CIDRs, then invokes fixed `Engine.sh --cluster-prepare-node` workflow before consuming invitation. Preparation installs K3s installation/firewall and Longhorn iSCSI/NFS prerequisites, but does not install or join K3s until membership admission approval arrives.
    - Expansion pair approval records same authenticated approval event for both admission IDs so both waiting joiners proceed. Degraded-quorum replacement records same compatible approval event for one admission. Join installation replaces running node-manager executable through atomic rename before enabling service, avoiding in-place executable overwrite failures.
    - Cluster controller exposes distinct `/startup`, `/ready`, and `/live` contracts. Startup proves local initialization completed, readiness depends on PostgreSQL operation-store access, and liveness proves only local HTTP process responsiveness. Long database bootstrap, node redeploy, or Kubernetes reconciliation never makes liveness depend on controller-loop progress or external dependencies.
    - Cluster controller renews its 20-second PostgreSQL ownership lease every five seconds through an independent heartbeat while operation steps run. Renewal error or ownership loss cancels step context immediately. Claim, advance, failure, and completion transactions lock and verify same live lease row before changing durable operation state, so former holder cannot record progress after fencing.
    - Node-action Job names retain operation-attempt identity while human-readable step labels replace illegal separators and hash-truncate values beyond Kubernetes' 63-character limit. Full unsanitized step remains in PostgreSQL and `BorealisClusterOperation` state for audit.
    - Node-action Job creation is idempotent across controller restart and create races. Controller treats `409 AlreadyExists` as resume candidate only after re-reading Job and matching namespace, operation label, full step annotation, normalized step label, target node, ServiceAccount boundary, immutable image, command, and arguments. Any mismatch fails closed without waiting on or replacing existing Job.
    - Engine manages explicit `docker.io`, `ghcr.io`, `quay.io`, and `registry.k8s.io` entries in K3s `registries.yaml`. `embedded-registry: true` alone does not activate Spegel exchange; each source registry must be enabled on every node. Joined nodes receive mirror configuration before K3s starts.
    - Borealis images are atomically staged under `/var/lib/rancher/k3s/agent/images` and imported by K3s. K3s pins these archives and adds containerd distribution-source labels required by Spegel. Engine waits for both local image presence and matching source label; direct `ctr images import` is not accepted because imported content can remain invisible to peers.
    - WireGuard route DaemonSet uses initialization-only startup, route-aware readiness, and local-process liveness. Edge owner routes Agent CIDRs directly through `borealis-wg`; standby nodes route same CIDRs through fixed edge VIP, so VIP movement changes next-hop ownership without rewriting every host route. Missing interface or temporary VIP convergence keeps readiness false while reconciliation remains alive. Full deploys and scoped WireGuard rebuild/reconcile operations apply same DaemonSet renderer and wait for every Engine node to become ready.
    - WireGuard control Deployment remains running on every active Engine node. Controller checks fixed edge VIP on host each second: non-owner suppresses validated activation mutations and withdraws any stale interface; owner accepts them and requires live interface for readiness. API replicas store active tunnel identity, readiness callbacks, transport timestamps, operator association, endpoint, expiry, and allowed ports in PostgreSQL with optimistic generation checks. Edge owner replays active session rows plus durable IP/key leases every three seconds, so newly elected owner rebuilds only live listener peers and any API replica can answer status or scheduler readiness. Short-lived signed tokens remain derived, not stored. Cluster conversion copies existing server keypair into `borealis-wireguard-server-keys`; API and WireGuard pods mount same read-only Secret on every node. WireGuard update candidate stays zero-replica to avoid host-port collision while active controller preserves ownership.
    - Node manager reads only local K3s etcd metrics and reports exact `etcd_server_is_leader` gauge as node label. Cluster controller combines that report with fixed kube-vip leases, CloudNativePG current primary Pod, scheduler application lease, and owner-aware WireGuard readiness. It persists observed etcd, control VIP, edge VIP, PostgreSQL, scheduler, and WireGuard owners into node runtime roles. Update request pins a non-leader-first node ID sequence from these observations; later role movement cannot change remaining sequence. Transfer-away fencing keeps WireGuard controller scaled without requiring prior-release standby readiness, then accepts any eligible non-target edge lease holder only after that actual owner's WireGuard workload is ready. HMR keeps exact-owner placement checks.
    - Drain withdrawal waits only on node-scoped traffic Services scaled or removed by application drain: API/Aegis, scheduler, guacd, Traefik, WebUI, and site workers. Resident operator and database endpoints remain available for control-plane and storage safety and cannot block rolling progress.
    - Standalone generic Deployments remain zero-replica templates after cluster enablement. Scoped/full deploys update Secrets, Services, and templates without launching unpinned duplicate Pods. Baseline reconciliation preserves controller-owned application, edge, scheduler, and PostgreSQL eligibility labels on active/pending cluster members.
    - Control and edge VIPs use separate kube-vip leader leases, `/32` ARP advertisements, local metrics liveness, advertised-VIP readiness, and disruption budgets. kube-vip `v1.1.0` health listener is intentionally disabled because it keeps process alive after manager loses leadership; normal process exit lets Kubernetes restart manager. Bootstrap does not accept running pod as proof: it waits for both DaemonSets, non-empty lease holders, local VIP addresses, and K3s `/readyz` through control VIP.
    - First-node conversion uses explicit stop-start handoff for generic host-network Traefik and WireGuard pods. Later Traefik candidate promotion uses same host-port handoff; WireGuard candidate stays stopped while active owner-aware controller is replaced. Failed first-node handoff restores prior standalone host workload replica count.
    - Job-scheduler candidate starts with `BOREALIS_SCHEDULER_LEADERSHIP_ELIGIBLE=false`. Health probe can validate process and PostgreSQL without candidate acquiring database lease or issuing work. Promotion rewrites flag to `true` before active Deployment rollout.
    - Cluster-controller candidate serves local probes but cannot acquire operation lease. API candidate disables background loops and stays outside public API traffic while remaining in mTLS-only Aegis peer Service so current memory key can reach candidate before promotion. Promotion restores controller eligibility and API loops in active pod template. Candidate probes therefore cannot mutate cluster ownership or receive public application traffic before promotion.
    - First-node SQLite-to-etcd conversion temporarily restarts K3s. Cluster controller Job polling treats bounded Kubernetes API `429`, `5xx`, timeout, and connection failures as transient until step deadline. Only already-created `EnrollCluster` Job polling permits a two-minute `401`/`403` recovery window while K3s switches datastore; persistent authorization failure and authorization errors in every other operation still fail.
    - Conversion explicitly restarts control and edge kube-vip DaemonSets after K3s returns. Existing kube-vip processes surrender leases during datastore restart and an unchanged server-side apply does not restart them; controller verifies fresh rollouts, lease holders, local VIP addresses, and control-VIP `/readyz` before continuing.
    - Database cutover records CloudNativePG service URL before scaling retained standalone StatefulSet to zero. Subsequent deploys preserve that URL only when CNPG reports healthy primary endpoint and standalone replicas remain zero; failed partial conversion cannot silently revive stale database. Operator-owned site-worker Pod restoration waits separately for named Pod recreation and readiness.
    - Cluster-enable and node-redeploy retries pass recorded immutable baseline SHA through controller Job and fixed node-manager contract. Node manager gives only Engine child process command-scoped Git safe-directory trust plus manager-owned writable build home under `Engine/Deploy/node-manager-home`; hardened service keeps host `/root` unavailable. Workload initialization resolves same pinned commit without mutating global Git configuration. Later operator checkout movement cannot change retry revision.
    - Engine updates split target image staging from candidate creation. `StageRevisionImages` builds/imports every target image and writes root-owned `Engine/Deploy/cluster-staged-revision` only after imports finish. Expand schema then runs from staged target image. `RedeployRevision` accepts matching marker/image manifest before creating candidate, preventing target process from starting against pre-expand schema.
    - Node redeploy starts target release's staged manager as separate transient activator outside old service sandbox. Activator validates pinned worktree and its own staged inode, waits until node-action Job leaves Running/Pending state, atomically installs target binary plus systemd unit, restarts node manager outside old service cgroup, verifies running process uses installed inode plus recreated Unix socket, then records target SHA in `/etc/borealis/node-manager-revision`. This lets first rolling update cross old-manager/new-manager boundary without killing action that performs update. Repeated redeploy skips refresh only when recorded SHA and running executable already match.
    - CLI operation polling tolerates bounded transient API loss after mutation acceptance. It never resubmits POST; persistent loss reports operation ID as still running server-side and directs operator to inspect Operations view.
    - Engine persists canonical private `BOREALIS_K3S_PEER_CIDRS` values into runtime Secret. Node-scoped release deploy hydrates same allowlist and fails closed when missing, preventing rolling updates from silently replacing cluster firewall policy with empty peer access.
    - Shared Agent artifacts use Longhorn RWX storage, which requires host NFSv4 mount utilities in addition to iSCSI. Seed copies only missing or changed files, byte-compares every source file, and allows bounded time for large existing artifact caches.
    - K3s does not bundle CSI VolumeSnapshot API resources. Cluster bootstrap checksum-pins external-snapshotter `v8.5.0` CRDs, deploys digest-pinned common snapshot controller with leader election and probes, then restarts CloudNativePG so daily Longhorn snapshot support is discovered before database migration.
    - Cluster API replicas expose separate TLS 1.3 mTLS-only Aegis key receiver from `aegis_cluster_fanout.go`; cert-manager assets and headless Service live in `Data/Engine/K3s/cluster/aegis-mtls.yaml`.

    ### Qualification

    - Stable K3s must pass `Data/Engine/K3s/cluster/run-probe-conformance.sh` on every Engine node. Test requires ten consecutive clean trials because affected K3s can alternate between correct and broken scheduling. Each trial pins workload to local node, forces liveness failure, waits for replacement container, proves replacement startup executes, and proves startup-budget liveness delay resets so no early replacement liveness runs. One failed trial deletes prior result and blocks cluster mode. Successful exact-version record lives at `/etc/rancher/k3s/borealis-probe-conformance.json`, includes ten-trial evidence, and sits inside node manager's narrow writable configuration path; manager never receives write access to K3s server-state directory. Policy tests also require every Borealis, CloudNativePG, Longhorn CSI, kube-vip, and snapshot-controller liveness delay to exceed corresponding startup failure budget. Full deployment reapplies dependency guards, waits for guarded CloudNativePG instances, and changes site-worker probe-contract hash so existing bare Pods recycle safely. Per-node candidate reconciliation also overwrites copied active probe settings with target release's guarded delay before candidate starts. Pair admission runs this gate before deploying application workloads. This safely contains [Kubernetes issue 141155](https://github.com/kubernetes/kubernetes/issues/141155) without fork, release candidate, fabricated result, or unsafe override. Remove delay shim only after supported stable K3s contains upstream fix and full qualification passes.
    - K3s membership removal qualification must prove Kubernetes Node deletion removes embedded-etcd membership while fenced host stays disabled. Keep qualification gate until current K3s behavior also excludes regressions tracked by [k3s issue 13623](https://github.com/k3s-io/k3s/issues/13623) and [k3s issue 13498](https://github.com/k3s-io/k3s/issues/13498).
    - Current merge/release target needs disposable same-L2 Ubuntu qualification for `1 -> 3 -> 1`, replacement, partition, leader death, rolling Engine/K3s update failure, CNPG failover, snapshot restore, and continuous API/UI/Agent traffic. Odd membership changes beyond three nodes remain future roadmap work for separate issue/PR after three-node work closes.
