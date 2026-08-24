# Managing Engine Clusters
Borealis Engine clustering runs application workloads, K3s control-plane services, embedded etcd, and PostgreSQL across homogeneous Engine nodes. Cluster mode supports one, three, or five active Ubuntu nodes on same Layer 2 network.

!!! danger "Qualification gate"
    Cluster conversion remains disabled until exact installed stable K3s version passes Borealis probe conformance and disposable three-node plus five-node qualification. Do not copy or fabricate conformance record, use release candidate, fork Kubernetes, or bypass gate. Production conversion needs separate operator approval.

## Requirements

- Ubuntu 24.04 or newer on every node.
- Same CPU architecture and Borealis sizing profile.
- Static private IPv4 addresses on same Layer 2 network.
- Separate unused IPv4 addresses for K3s control-plane VIP and Borealis edge VIP.
- Explicit `BOREALIS_K3S_PEER_CIDRS` allowlist covering every current and planned cluster management address. Prefer one `/32` entry per Engine node.
- Clean Git worktrees using configured repository origin and same stable Borealis release.
- Working Longhorn iSCSI and NFSv4 client prerequisites plus enough capacity to run node-local candidates during drain. Normal Engine deployment installs missing host packages.
- Odd active membership: one, three, or five nodes.
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

## Add Nodes in Pairs

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

Node manager first prepares firewall, iSCSI, NFSv4, and K3s host prerequisites through fixed Engine workflow. It does not expose arbitrary shell execution. Invitation join then creates `Pending Quorum` admission and waits for Admin to approve complete pair. Approved members join application-drained and role-ineligible. Controller admits `1 -> 3` or `3 -> 5`, expands CloudNativePG, deploys pinned cluster revision on both nodes, then probes and soaks each node before activating it.

Temporary even K3s membership during pair admission does not disable healthy existing nodes. Architecture, Ubuntu version, hostname, node name, management IPv4, invitation lifetime, and invitation authentication are validated before membership work.

Safe downscale also works in pairs. Select both targets from Nodes view and type `REMOVE NODE PAIR`. Controller snapshots state, moves PostgreSQL and application leaders onto surviving member, scales CloudNativePG, and refuses fencing unless PostgreSQL vacated both targets. Each target then drains, writes persistent removal-fence marker, schedules local K3s disable/stop, becomes NotReady, and gets deleted through Kubernetes Node API. Remaining servers must stay `Ready=True` and `EtcdIsVoter=True` through soak before next target starts.

Emergency removal is single-node recovery for host already unreachable. Power target off through external management first, then type both `TARGET IS POWERED OFF` and `EMERGENCY REMOVE NODE`. Borealis never contacts target in this path. It deletes Node resource, verifies surviving etcd voters, scales PostgreSQL, and leaves cluster `Degraded Quorum` until replacement or paired repair restores supported odd membership.

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
7. Keep candidate outside shared Services through separate labels and selectors. Require startup, readiness, liveness, direct endpoint, database, scheduler, Agent-path, and candidate soak checks.
8. Promote candidate into active Deployment, then require shared Service, VIP, database, scheduler, Agent-path, WireGuard, and ready-soak checks.
9. Restore assignments, then clear application drain.

Update All orders non-leaders first, transfers roles, and updates one application node at time. Failure halts operation and leaves failed node drained; healthy old/new nodes keep serving. Retry resumes explicit operation. No automatic code rollback or skip-and-continue occurs. Cluster release baseline advances only after all active nodes reach target.

One-node updates require maintenance-outage acknowledgement. Three/five-node continuity covers ready HTTP/API/Agent endpoints, durable queued work, graceful drain, and reconnect after socket movement. Existing interactive shell, desktop, or WebSocket sessions cannot transfer live.

Engine release update and K3s upgrade stay separate operations. Maintenance view accepts stable `vX.Y.Z+k3sN` target only. Target must be newer than current version and stay on current minor or advance exactly one minor. Source version must already pass Borealis probe conformance, and upgrade image must use `registry/repository@sha256:digest` form.

K3s controller takes pre-change snapshot, orders non-leaders before leaders, drains one application node, and creates exclusive system-upgrade-controller Plan selecting only that server. Plan concurrency stays one and cordons host while immutable `k3s-upgrade` image applies exact version. Borealis then requires Node Ready, etcd voter health, exact kubelet version, local probe conformance, Engine health, and minimum-ready soak before clearing drain and moving to next server. Failed Plan or probe halts update and leaves node drained. One-node clusters require `ACCEPT OUTAGE`.

## Aegis Unlock Across Replicas

Aegis key stays memory-only. Clustered API replicas use cert-manager-issued TLS 1.3 mutual certificate endpoint on headless internal Service. One successful setup/unlock verifies key locally, resolves current API replica addresses, and sends bounded 32-byte key to every replica. Unlocked replicas continue bounded reconciliation so later replacement candidates receive key before promotion. Receiver re-verifies key against Aegis verification token before installing memory copy. Endpoint accepts no operator session, arbitrary payload, or non-mTLS caller. All-cold cluster restart still requires one operator unlock.

## Failure and Recovery Rules

- `Degraded Quorum` records lost durability or membership. Healthy nodes remain enabled.
- PostgreSQL synchronous quorum requires one replica acknowledgement on three nodes and two on five. Writes stop when durability quorum is unavailable; stale replica promotion is rejected.
- No automatic PostgreSQL or VIP failback. Operator chooses switchover after failed owner returns.
- Safe removal works in pairs. Emergency removal needs external power fence plus two exact confirmations and records degraded state.
- Longhorn snapshots use daily fourteen-snapshot retention plus pre-change snapshots. They are in-cluster recovery, not disaster recovery.
- Aegis key remains memory-only. All-cold restart requires operator unlock.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - Cluster state/events: `GET /api/server/cluster`, `GET /api/server/cluster/events`.
    - Enable/invite/admit/scale: `POST /api/server/cluster/enable`, `/invitations`, `/admissions/{id}/approve`, `/membership/scale`.
    - Node and database operations: `POST /api/server/cluster/nodes/{id}/maintenance`, `/remove`, `/postgres/switchover`, `/postgres/emergency-failover`. Safe remove requires canonical `paired_node_id` plus `REMOVE NODE PAIR`; emergency remove requires `TARGET IS POWERED OFF` plus `EMERGENCY REMOVE NODE`.
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

    - `BorealisCluster`, `BorealisNodeAdmission`, `BorealisNodeRuntime`, and `BorealisClusterOperation` hold desired/runtime Kubernetes state.
    - PostgreSQL tables `borealis_cluster_state`, `borealis_cluster_nodes`, `borealis_cluster_admissions`, `borealis_cluster_operations`, `borealis_cluster_operation_events`, `borealis_cluster_events`, `borealis_cluster_invitations`, `borealis_cluster_realtime_outbox`, and `borealis_cluster_leases` hold audit, events, invitations, outbox, and singleton leases.
    - Node manager accepts fixed verbs only, including persistent K3s membership fence and exact-version probe conformance. It never exposes arbitrary command or remote-shell execution. Manager stays active across controlled K3s restarts so enrollment and one-node-at-a-time K3s upgrades can wait for control plane recovery without losing operation process.
    - Blank-node join requires `--peer-cidrs`. Node manager validates and canonicalizes bounded private IPv4 CIDRs, then invokes fixed `Engine.sh --cluster-prepare-node` workflow before consuming invitation. Preparation installs K3s installation/firewall and Longhorn iSCSI/NFS prerequisites, but does not install or join K3s until paired admission approval arrives.
    - Control and edge VIPs use separate kube-vip leader leases, `/32` ARP advertisements, health listeners, metrics listeners, and disruption budgets. Bootstrap does not accept a running pod as proof: it waits for both DaemonSets, non-empty lease holders, local VIP addresses, and K3s `/readyz` through control VIP.
    - First-node conversion and later candidate promotion use explicit stop-start handoff for host-network Traefik and WireGuard pods because old and new pods cannot bind same host ports. Failed first-node handoff restores prior standalone host workload replica count.
    - First-node SQLite-to-etcd conversion temporarily restarts K3s. Cluster controller Job polling treats bounded Kubernetes API `429`, `5xx`, timeout, and connection failures as transient until step deadline. Only already-created `EnrollCluster` Job polling permits a two-minute `401`/`403` recovery window while K3s switches datastore; persistent authorization failure and authorization errors in every other operation still fail.
    - Conversion explicitly restarts control and edge kube-vip DaemonSets after K3s returns. Existing kube-vip processes surrender leases during datastore restart and an unchanged server-side apply does not restart them; controller verifies fresh rollouts, lease holders, local VIP addresses, and control-VIP `/readyz` before continuing.
    - Database cutover records CloudNativePG service URL before scaling retained standalone StatefulSet to zero. Subsequent deploys preserve that URL only when CNPG reports healthy primary endpoint and standalone replicas remain zero; failed partial conversion cannot silently revive stale database. Operator-owned site-worker Pod restoration waits separately for named Pod recreation and readiness.
    - Cluster-enable retries pass recorded immutable baseline SHA through controller Job and fixed node-manager contract. Workload initialization resolves that commit with command-scoped Git safe-directory trust, so later operator checkout movement cannot change retry revision or mutate global Git configuration.
    - CLI operation polling tolerates bounded transient API loss after mutation acceptance. It never resubmits POST; persistent loss reports operation ID as still running server-side and directs operator to inspect Operations view.
    - Engine persists canonical private `BOREALIS_K3S_PEER_CIDRS` values into runtime Secret. Node-scoped release deploy hydrates same allowlist and fails closed when missing, preventing rolling updates from silently replacing cluster firewall policy with empty peer access.
    - Shared Agent artifacts use Longhorn RWX storage, which requires host NFSv4 mount utilities in addition to iSCSI. Seed copies only missing or changed files, byte-compares every source file, and allows bounded time for large existing artifact caches.
    - K3s does not bundle CSI VolumeSnapshot API resources. Cluster bootstrap checksum-pins external-snapshotter `v8.5.0` CRDs, deploys digest-pinned common snapshot controller with leader election and probes, then restarts CloudNativePG so daily Longhorn snapshot support is discovered before database migration.
    - Cluster API replicas expose separate TLS 1.3 mTLS-only Aegis key receiver from `aegis_cluster_fanout.go`; cert-manager assets and headless Service live in `Data/Engine/K3s/cluster/aegis-mtls.yaml`.

    ### Qualification

    - Stable K3s must pass `Data/Engine/K3s/cluster/run-probe-conformance.sh`. Test forces liveness failure, waits for replacement container, then proves replacement remains `started=false`, runs startup probe, and does not run liveness early. This directly covers [Kubernetes issue 141155](https://github.com/kubernetes/kubernetes/issues/141155); direct process-kill restart alone is not sufficient conformance evidence.
    - K3s membership removal qualification must prove Kubernetes Node deletion removes embedded-etcd membership while fenced host stays disabled. Keep qualification gate until current K3s behavior also excludes regressions tracked by [k3s issue 13623](https://github.com/k3s-io/k3s/issues/13623) and [k3s issue 13498](https://github.com/k3s-io/k3s/issues/13498).
    - Merge/release needs disposable same-L2 Ubuntu qualification for `1 -> 3 -> 5`, `5 -> 3 -> 1`, replacement, partition, leader death, rolling Engine/K3s update failure, CNPG failover, snapshot restore, and continuous API/UI/Agent traffic.
