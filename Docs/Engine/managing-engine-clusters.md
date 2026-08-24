# Managing Engine Clusters
Borealis Engine clustering runs application workloads, K3s control-plane services, embedded etcd, and PostgreSQL across homogeneous Engine nodes. Cluster mode supports one, three, or five active Ubuntu nodes on same Layer 2 network.

!!! danger "Qualification gate"
    Cluster conversion remains disabled until exact installed stable K3s version passes Borealis probe conformance and disposable three-node plus five-node qualification. Do not copy or fabricate conformance record, use release candidate, fork Kubernetes, or bypass gate. Production conversion needs separate operator approval.

## Requirements

- Ubuntu 24.04 or newer on every node.
- Same CPU architecture and Borealis sizing profile.
- Static private IPv4 addresses on same Layer 2 network.
- Separate unused IPv4 addresses for K3s control-plane VIP and Borealis edge VIP.
- Explicit `BOREALIS_K3S_PEER_CIDRS` allowlist covering cluster management addresses.
- Clean Git worktrees using configured repository origin and same stable Borealis release.
- Working Longhorn prerequisites and enough capacity to run node-local candidates during drain.
- Odd active membership: one, three, or five nodes.

## Enable First Node

Run probe conformance against installed stable K3s before requesting conversion:

```sh
sudo bash Data/Engine/K3s/cluster/run-probe-conformance.sh
sudo bash Engine.sh --network-mode <public-or-local> deploy prod

# Supply current Engine API URL and recently issued Admin token.
export BOREALIS_CLUSTER_API_URL=https://engine.example.com
export BOREALIS_CLUSTER_ADMIN_TOKEN=<recent-admin-access-token>
sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
  bash Engine.sh --cluster-enable \
  --control-plane-vip 192.0.2.10 \
  --edge-vip 192.0.2.11
```

Production redeploy after conformance publishes exact-version pass state to API workload. Cluster Management **Enable Cluster** performs same authenticated operation without CLI environment variables.

Enablement creates cluster CRDs, controller RBAC, node manager, fixed VIP resources, per-node workloads, application availability policies, and CloudNativePG migration workflow. Existing standalone PostgreSQL remains authoritative until logical import validates and traffic cuts over. Old PVC plus encrypted dump remain retained after old StatefulSet scales down.

!!! warning "One-way database cutover"
    Cluster enablement does not automatically move database traffic back into standalone StatefulSet. Restore from retained old PVC or encrypted dump is explicit recovery work.

## Add Nodes in Pairs

Create invitation in Cluster Management, then run displayed node-manager join command on each new host. New members remain `Pending Quorum`, application-drained, and role-ineligible. Admin approves only complete pair. Controller admits `1 -> 3` or `3 -> 5`, expands CloudNativePG, deploys pinned cluster revision on both nodes, then probes and soaks each node before activating it.

Temporary even K3s membership during pair admission does not disable healthy existing nodes. Architecture, Ubuntu version, hostname, node name, management IPv4, invitation lifetime, and invitation authentication are validated before membership work.

## Read Cluster State

Open **Admin > Cluster Management**. Views show:

- Overview: quorum, active release, HMR state, operation lease.
- Nodes: etcd leader, control VIP, edge VIP, PostgreSQL primary, scheduler leader, WireGuard owner, drain reason, release, and probe state.
- Database: primary/replicas, synchronous durability, switchovers, snapshots.
- Updates: compatible stable releases, per-node update, ordered update-all.
- Operations: durable state, event history, retry, recovery, cancellation.
- Maintenance: paired admission/removal, maintenance drain, emergency actions.

One exclusive cluster operation may mutate placement, membership, HMR, or releases at time. Controller state survives controller restart through PostgreSQL operation records and Kubernetes desired/runtime objects.

## Probe and Shutdown Contract

Borealis follows three distinct probe contracts:

- Startup proves initialization completed. Slow initialization belongs here.
- Readiness proves workload can serve current role. API and scheduler readiness includes required PostgreSQL access. WireGuard readiness includes interface, edge lease, routes, and tunnel state.
- Liveness detects local recoverable process failure only. Scheduler liveness never depends on PostgreSQL.

Drain sequence stops assignments, withdraws readiness, waits for node EndpointSlices to converge, forwards termination signals, and bounds in-flight drain inside `terminationGracePeriodSeconds`. Workloads use `preStop`, `minReadySeconds`, topology constraints, and disruption budgets.

[ngrok probe guidance](https://ngrok.com/blog/probes) is sound: readiness must describe traffic eligibility while liveness should remain narrow. One caveat matters: Kubernetes starts endpoint removal and pod termination concurrently; Service withdrawal is not guaranteed to finish before `SIGTERM`. Borealis therefore combines readiness withdrawal, EndpointSlice verification, `preStop`, signal handling, and connection draining. See [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/) and [pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/).

## Cluster-Wide HMR

Starting `deploy dev` or `webui-frontend rebuild dev` on clustered Engine requires Admin step-up and typed `ENABLE HMR`. Non-interactive CLI also requires `--acknowledge-cluster-non-ha`.

> This moves all Borealis application traffic to this node and places every other Engine node in drained standby. Cluster loses application HA until production mode is restored.

Controller first checks quorum, capacity, target production health, and exclusive-operation state. It moves edge/WireGuard ownership, scheduler leadership, site workers, application endpoints, and caught-up PostgreSQL primary to selected HMR node. Standby nodes keep K3s, etcd, Longhorn, and PostgreSQL replicas running; application scheduling is drained without cordoning infrastructure.

Cluster banner remains visible on every page until exit completes. Updates, membership, and normal maintenance remain blocked during HMR.

Exit restores saved pinned production release, not local working tree. Local edits remain available for later commit/release. Controller validates production candidate through direct, Service, VIP, database, scheduler, Agent-path, and WireGuard checks, then restores standby nodes one at time. Lost HMR node gets fenced before pinned production workloads recover on standby members.

## Cluster-Aware Updates

Use Updates view instead of `git pull` on clustered Engines. Catalog shows published stable GitHub releases only. Drafts, prereleases, branch heads, nonnumeric tags, downgrades, and incompatible manifests cannot be selected.

Selected tag resolves once to immutable commit SHA. Controller records title, tag, SHA, source URL, initiator, and compatibility result. Every cluster-capable release must include `Data/Engine/release-manifest.json` declaring rolling source range, mixed-version window, schema phase, and K3s baseline.

`Update Node` and `Update All` use same node workflow:

1. Acquire exclusive operation lease and create pre-change snapshot.
2. Move PostgreSQL, edge/WireGuard, and scheduler leadership away when needed.
3. Stop assignments, withdraw readiness, drain work, and verify EndpointSlice withdrawal.
4. Fetch release and fast-forward checkout to stored SHA. Dirty or diverged worktree fails without stash/reset.
5. Invoke target revision node-scoped `Engine.sh` workflow locally.
6. Build/import immutable images and create node-pinned candidates.
7. Require startup, readiness, liveness, direct endpoint, Service, VIP, database, scheduler, Agent-path, and ready-soak checks.
8. Restore endpoints and assignments, then clear application drain.

Update All orders non-leaders first, transfers roles, and updates one application node at time. Failure halts operation and leaves failed node drained; healthy old/new nodes keep serving. Retry resumes explicit operation. No automatic code rollback or skip-and-continue occurs. Cluster release baseline advances only after all active nodes reach target.

One-node updates require maintenance-outage acknowledgement. Three/five-node continuity covers ready HTTP/API/Agent endpoints, durable queued work, graceful drain, and reconnect after socket movement. Existing interactive shell, desktop, or WebSocket sessions cannot transfer live.

Engine release update and K3s upgrade stay separate operations. K3s uses pinned stable version, etcd snapshot, one-server-at-time drain, leadership movement, post-upgrade conformance, and halt-on-failure.

## Failure and Recovery Rules

- `Degraded Quorum` records lost durability or membership. Healthy nodes remain enabled.
- PostgreSQL synchronous quorum requires one replica acknowledgement on three nodes and two on five. Writes stop when durability quorum is unavailable; stale replica promotion is rejected.
- No automatic PostgreSQL or VIP failback. Operator chooses switchover after failed owner returns.
- Safe removal works in pairs. Emergency removal needs explicit confirmation and records degraded state.
- Longhorn snapshots use daily fourteen-snapshot retention plus pre-change snapshots. They are in-cluster recovery, not disaster recovery.
- Aegis key remains memory-only. All-cold restart requires operator unlock.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - Cluster state/events: `GET /api/server/cluster`, `GET /api/server/cluster/events`.
    - Enable/invite/admit/scale: `POST /api/server/cluster/enable`, `/invitations`, `/admissions/{id}/approve`, `/membership/scale`.
    - Node and database operations: `POST /api/server/cluster/nodes/{id}/maintenance`, `/remove`, `/postgres/switchover`, `/postgres/emergency-failover`.
    - Release/HMR/operations: `GET /api/server/cluster/releases`; `POST /api/server/cluster/hmr/start`, `/hmr/exit`, `/updates`, `/operations/{id}/retry`, `/operations/{id}/cancel`.
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

    - `BorealisCluster`, `BorealisNodeAdmission`, `BorealisNodeRuntime`, and `BorealisClusterOperation` hold desired/runtime Kubernetes state.
    - PostgreSQL tables `borealis_cluster_state`, `borealis_cluster_nodes`, `borealis_cluster_admissions`, `borealis_cluster_operations`, `borealis_cluster_operation_events`, `borealis_cluster_events`, `borealis_cluster_invitations`, `borealis_cluster_realtime_outbox`, and `borealis_cluster_leases` hold audit, events, invitations, outbox, and singleton leases.
    - Node manager accepts fixed verbs only. It never exposes arbitrary command or remote-shell execution.

    ### Qualification

    - Stable K3s must pass `Data/Engine/K3s/cluster/run-probe-conformance.sh`, including regression coverage for [Kubernetes issue 141155](https://github.com/kubernetes/kubernetes/issues/141155).
    - Merge/release needs disposable same-L2 Ubuntu qualification for `1 -> 3 -> 5`, `5 -> 3 -> 1`, replacement, partition, leader death, rolling Engine/K3s update failure, CNPG failover, snapshot restore, and continuous API/UI/Agent traffic.
