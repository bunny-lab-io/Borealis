---
tags:
  - Borealis
  - K3s
  - Clustering
  - Kubernetes
---

## Purpose

This document describes how to manage Borealis Engine clustering across the supported cluster lifecycle, including initial enablement, ingress cutover, node admission and removal, maintenance, Cluster-Wide Node Isolation, Engine and K3s updates, and recovery.  Borealis clustering runs application workloads, K3s control-plane services, embedded etcd, and PostgreSQL across homogeneous Engine nodes.

The current release supports one or three active Ubuntu nodes on the same Layer 2 network.  Temporary `2 active / 3 desired` membership is supported only as `Degraded Quorum` while replacing an externally fenced failed node.  Expansion or shrinking beyond three active nodes remains future roadmap work and is blocked by the current API, WebUI, and transactional store.

!!! danger "Cluster Qualification Gate"
Cluster conversion remains disabled until the exact installed stable K3s version passes Borealis guarded-probe conformance and disposable three-node qualification.  Do not copy or fabricate a conformance record, use a release candidate, fork Kubernetes, or bypass this gate.  Production conversion also requires separate operator approval.

## Requirements

* Ubuntu 24.04 or newer on every node
* AMD64/Intel server architecture using the same Borealis sizing profile; ARM nodes are not supported
* Static private IPv4 addresses on the same Layer 2 network
* One unused private IPv4 address on the same Layer 2 network for the Cluster Virtual IP
* An explicit `BOREALIS_K3S_PEER_CIDRS` allowlist covering every current and planned cluster management address; prefer one `/32` entry per Engine node
* Peer access to K3s and Spegel TCP ports `6443` and `5001`; the Borealis firewall manages both from the same allowlist
* Clean Git worktrees using the configured repository origin and the same pinned Borealis baseline
* The initial development baseline commit present in the configured origin before additional nodes join
* Working Longhorn iSCSI and NFSv4 client prerequisites plus enough capacity for one shared-artifact replica and node-local candidates on every Engine
* `multipathd` disabled on dedicated Engine hosts

  * Normal Engine deployment disables it when no genuine multipath storage exists
  * Hosts using genuine multipath storage must apply the Longhorn device blacklist before deployment
* Supported odd active membership of one or three nodes
* A content-addressed `rancher/k3s-upgrade` image configured in `BOREALIS_K3S_UPGRADE_IMAGE` before requesting a K3s control-plane update

## Enable the First Cluster Node

Run probe conformance against the installed stable K3s release before requesting cluster conversion.

On the first Engine node, run:

```sh
export BOREALIS_K3S_PEER_CIDRS=192.168.50.21/32,192.168.50.22/32,192.168.50.23/32

sudo bash Data/Engine/K3s/cluster/run-probe-conformance.sh

sudo --preserve-env=BOREALIS_K3S_PEER_CIDRS \
  bash Engine.sh --network-mode <NETWORK_MODE> deploy prod

# Supply the current Engine API URL and a recently issued Admin token.
export BOREALIS_CLUSTER_API_URL=https://engine.example.com
export BOREALIS_CLUSTER_ADMIN_TOKEN=<RECENT_ADMIN_ACCESS_TOKEN>

sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
  bash Engine.sh --cluster-enable \
  --cluster-vip 192.168.50.10
```

Use `public` or `local` for `<NETWORK_MODE>` according to the Engine deployment being converted.

Production redeploy after conformance publishes the exact-version pass state to the API workload.  In Cluster Management, "**Enable Cluster**" asks only for "**Cluster Virtual IP**".  The API derives the current Kubernetes node name and host management IPv4 from Pod metadata and enforces the AMD64 runtime, so the operator does not enter those values.  The CLI performs the same authenticated operation through `--cluster-vip`.

A GitHub release is not required for initial cluster formation.  An exact dotted-numeric tag at the deployed commit becomes the stable baseline.  A clean untagged checkout becomes an immutable `dev-<FIRST_12_COMMIT_CHARACTERS>` baseline tied to the full source SHA.  A dirty checkout has no valid baseline, and cluster enablement fails closed.

Before admitting additional nodes from a development baseline, push the development commit to the configured origin so every host can fetch the same object.  Probe conformance remains required for both stable and development baseline types.

Enablement creates the cluster CRDs, controller RBAC, node manager, fixed Cluster Virtual IP resources, per-node workloads, application availability policies, and CloudNativePG migration workflow.  Existing standalone PostgreSQL remains authoritative until logical import validates and traffic cuts over.

During database cutover, Borealis stops scheduler and operator assignments, gracefully drains site workers, and then pauses remaining database clients before the dump.  The old PVC and encrypted dump remain retained after the old StatefulSet scales down.

If the API restarts while the CLI watches enablement, the CLI reconnects for one minute without submitting a second request.  If the connection remains unavailable, copy the displayed operation ID and inspect "**Admin > Cluster Management > Cluster Events**" before attempting another action.  The accepted operation continues running server-side.

!!! warning "One-Way Database Cutover"
Cluster enablement does not automatically move database traffic back into the standalone StatefulSet.  Restoring from the retained old PVC or encrypted dump is explicit recovery work.

!!! warning "Split-VIP Preview Clusters"
In-place conversion from earlier preview clusters that use separate control-plane and edge VIPs is not supported.  Complete release qualification from fresh standalone Engine nodes.  Do not redeploy the unified-VIP release over a split-VIP cluster without a separate migration plan and proxy/K3s certificate cutover.

## Cut Over the Outer Reverse Proxy

Cluster conversion moves K3s API, public application ingress, and WireGuard ownership from the standalone Engine node address to the fixed Cluster Virtual IP.  Any outer or nested proxy that remains pinned to a standalone node address can lose Borealis WebUI, API, WebSocket, remote-desktop, or Agent tunnel access when that node enters maintenance, updates, or fails.  A healthy cluster does not make a node-address upstream highly available.

!!! warning "Plan Ingress Cutover Before Conversion"
Pre-stage the Cluster Virtual IP upstream as a disabled backend when the proxy supports it.  Enable or switch the backend immediately after cluster enablement advertises the Cluster Virtual IP and before draining the first Engine node.  Missing this cutover causes an operator-facing outage even while the cluster remains healthy.

After the Cluster Virtual IP becomes reachable, replace the standalone node address in every Borealis outer-proxy service:

* HTTP router: `<CLUSTER_VIP>:80`, preserving the Engine FQDN as the `Host` header
* HTTPS TCP passthrough router: `<CLUSTER_VIP>:443`, preserving TLS SNI and any configured PROXY protocol version
* WireGuard UDP router: `<CLUSTER_VIP>:30000`

Keep WebSocket upgrades enabled.  When the outer proxy sends forwarded headers or PROXY protocol, include its source IP address or CIDR in `BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS`.

The K3s API uses the same Cluster Virtual IP on TCP `6443`.  Application and WireGuard proxy routes remain on ports `80`, `443`, and `30000`.

### Validate the Outer Proxy

From the outer proxy host, test the actual Engine FQDN against the Cluster Virtual IP:

```sh
curl --resolve engine.example.com:443:192.168.50.10 \
  https://engine.example.com/health
```

Confirm the request returns HTTP `200`.  Then verify normal FQDN login, live updates, remote desktop, and a fresh Agent WireGuard handshake through the outer proxy.

Port `80` remains the HTTP/ACME and redirect entrypoint.  The HTTPS router must continue using TCP `443`.

## Add or Replace Nodes

Create an invitation in "**Admin > Cluster Management**", then copy the K3s server token to a root-only file on each new host through a trusted management channel.

On each joining host, run the node-manager join command. Borealis supplies the Cluster Virtual IP, pinned K3s version, and private peer allowlist after approval:

```sh
sudo borealis-node-manager join \
  --endpoint https://engine.example.com \
  --invite-bundle '<TARGET_BOUND_INVITATION>' \
  --node-name engine-02 \
  --management-ip 192.168.50.22 \
  --k3s-token-file /root/borealis-k3s-server.token
```

The endpoint must use HTTPS and must not redirect. Use `--ca-file /path/to/engine-ca.pem` when the Engine certificate uses a private CA. The node manager submits its identity and waits for approval before preparing the firewall, iSCSI, NFSv4, and K3s host prerequisites. It checks approval again before joining K3s.

Invitation join creates a `Pending Quorum` admission and waits for Admin approval.  AMD64 architecture, Ubuntu version, hostname, node name, management IPv4, invitation lifetime, and invitation authentication are validated before membership work begins.

### Resume or Cancel an Interrupted Join

Rerun the same command on the same host after a lost response or process restart. Borealis resumes the original admission and checks the hostname, management address, architecture, and OS version. Keep those values unchanged during recovery.

Invitations allow initial acceptance and approval for 15 minutes. An approved target can resume for up to 24 hours from invitation creation. After that, select **Renew Invitation** beside the retained admission, then rerun the command with the new bundle. Renewal revokes the previous bundle and preserves the original host and operation. Creating another invitation for that same retained node name performs the same renewal.

Select **Cancel Admission** only for an unapproved pending target. Expired pending admissions release unused capacity automatically. If Borealis finds approval, member, or operation evidence, it retains the slot as **Recovery Required**. Missing or NotReady Kubernetes nodes do not prove that a host never joined.

For a failed admission or an admission operation cancelled by an older release, open **Cluster Events**, select **Retry** on the original operation, and rerun join on the original hosts. Renew expired bundles as needed. Approved admission operations cannot be cancelled because a target may already have joined. Restore the original cohort, then use supported removal and fencing before replacing a joined host. A removed host retaining its local member-removal fence must be rebuilt before joining again.

Optional `--k3s-server`, `--k3s-version`, and `--peer-cidrs` flags assert expected values. They must match the settings supplied by Borealis; they cannot override cluster configuration.

### Expand from One Node to Three

Normal `1 -> 3` expansion requires a complete pair of joining nodes.  Temporary even K3s membership during pair admission does not disable healthy existing nodes.

After approval, members remain application-drained and role-ineligible while Borealis:

* Runs probe conformance pinned to each joining node
* Expands CloudNativePG
* Deploys isolated pinned candidates
* Probes and soaks the candidates
* Promotes them into active workloads
* Enables normal HA role eligibility
* Verifies active workload and WireGuard health through another soak
* Clears application drain
* Records restored three-node membership

!!! info "Current Membership Limit"
Three active nodes is the current release maximum.  Cluster Management disables invitation, admission approval, and pair-expansion controls after three nodes become active.  The public API and transactional store enforce the same limit, including stale invitation and pending-admission races.

### Replace an Externally Fenced Failed Node

After emergency removal records `2 active / 3 desired` `Degraded Quorum`, create one invitation and approve one replacement admission.  Single-invitation replacement is available only in this recorded recovery state.

Normal cluster-changing operations remain blocked until supported three-node membership is restored.

### Remove Two Nodes Safely

Safe downscale supports `3 -> 1` in the current release.

In the Nodes view, select both removal targets and type `REMOVE NODE PAIR`.

The controller first snapshots state, moves the synchronized PostgreSQL primary onto the surviving member, waits for healthy replication, and then scales CloudNativePG.  Failure to prepare the survivor stops the operation before database membership changes.

The controller also refuses fencing unless PostgreSQL has vacated both targets.  Each target is then processed in sequence:

* Drain the target
* Write the persistent removal-fence marker
* Disable future K3s service restarts while leaving the current process active
* Save the successful fence acknowledgement for this operation and exact target identity
* Ask K3s to transfer leadership and remove embedded-etcd membership
* Wait for durable removal confirmation and local K3s exit
* Delete the Kubernetes Node resource

Remaining servers must stay `Ready=True` and `EtcdIsVoter=True` through the required soak before the next target starts.

Shared Agent artifact storage must retain a healthy replica on the surviving Engine throughout retry and removal.  The controller reduces the Longhorn replica target to one only after both removed nodes are fenced and deleted.

!!! warning "Unreachable Does Not Prove Fenced"
    `NotReady` or a missing Node resource can mean a network partition. Planned removal stops unless Borealis has saved a matching successful fence acknowledgement. Restore the same target and retry, or power it off through external management and use **Emergency Remove a Failed Node** below.

    Retry accepts only the original Kubernetes and etcd identity. A reinstalled host using the same name is a different target. Older interrupted operations without recorded proof also stop; do not fabricate acknowledgement records or remove fence files to bypass this check.

### Emergency Remove a Failed Node

Emergency removal is a single-node recovery path for a host that is already unreachable.

!!! danger "Externally Fence the Target First"
Power the target off through external management before using emergency removal.  Borealis never contacts the target in this path.  The confirmation asserts that the host cannot run or rejoin.

In Cluster Management, confirm both required phrases:

* `TARGET IS POWERED OFF`
* `EMERGENCY REMOVE NODE`

Borealis deletes the Node resource, verifies surviving etcd voters, scales PostgreSQL to two instances with one synchronous acknowledgement, and records `2 active / 3 desired` `Degraded Quorum`.

Create one new invitation and approve its replacement admission to restore supported three-node membership.  Normal cluster-changing operations remain blocked during replacement recovery.

!!! danger "Never Fake Emergency Fencing"
Deleting live K3s server membership can allow the removed host to retain or rebuild conflicting local control-plane state.  The safe removal path uses the node-manager persistent fence instead.

## Read Cluster State

Navigate to "**Admin > Cluster Management**".

The interface provides the following views:

* **Overview**

  * Quorum
  * Active release
  * HMR state
  * Operation lease
* **Nodes**

  * Paginated node table
  * Combined membership and application status
  * Management IP
  * Plain-text database state
  * Active role ownership
  * Passed/total probe summary
  * Row action menu
* **Database**

  * Primary and replica state
  * Synchronous durability
  * Switchovers
  * Snapshots
* **Cluster Events**

  * Paginated operation history
  * Affected node hostnames
  * Status
  * Friendly operation names
  * Concise lifecycle details
  * Local timestamp
  * Cancellation for queued or waiting work
* **Maintenance**

  * Stable Engine releases
  * K3s updates
  * Paired admission and removal
  * Maintenance drain
  * Emergency actions

Global cluster-state banners include a close control on the far right.  The active isolation banner names the isolated node carrying all Borealis application traffic.  Dismissing a banner keeps that exact state hidden only while the current browser shell continues polling unchanged state.  A changed operation step, cluster condition, or isolated node appears as a new banner.

Only one exclusive cluster operation may mutate placement, membership, HMR, or releases at a time.  Controller state survives controller restart through PostgreSQL operation records and Kubernetes desired/runtime objects.

### Interpret Node and Database State

Role owners and the PostgreSQL primary display operator-facing node names while APIs retain immutable node IDs.

The Nodes table combines membership and application state into labels such as:

* `Active / Active`
* `Active / Drained`
* `Active / Cordoned`

The Database column displays linked `Active` text for the PostgreSQL primary.  The link opens the Database view.

Active replicas display `Replica Healthy` only when all configured CloudNativePG instances are Ready.  Incomplete or unknown replica readiness displays `Not Ready`.

The Database view reports configured and Ready CloudNativePG instances separately.

`Degraded Database` blocks normal cluster-changing operations until all configured instances return Ready.  Emergency PostgreSQL failover, externally fenced emergency removal, maintenance exit, and HMR exit remain available for recovery.  Required synchronous durability can remain available while redundancy is reduced.

### Use Cluster Events

Cluster Events preserves failed records for audit and translates internal operation kinds into operator-facing names such as `Maintenance Mode Enabled`, `Cluster-Wide Node Isolation Disabled`, and `Node Pair Removed`.

Each row identifies affected node hostnames, summarizes the latest lifecycle message or failure, and provides a copy control for full troubleshooting text containing operation metadata, raw step names, error context, redacted payload, and linked lifecycle events.

Cluster-wide work is labeled `Cluster-wide`.  Credentials and invitation secrets are redacted from copied structured data.

An older cluster-enable or membership failure becomes `Superseded` after a newer operation of the same kind succeeds and cannot be retried. The same rule applies to admission operations cancelled by older releases. Unsuperseded failed or cancelled admission operations expose **Retry** for the original cohort; other failed records remain diagnostic-only. Queued or waiting admission operations cannot be cancelled because a target may already have joined. Other queued or waiting operations retain cancellation at their supported safe boundary.

Maintenance explains an empty release catalog when no published stable cluster-compatible release exists at or above the pinned baseline.

## Manage Maintenance and Drain State

Node drain and restore boundaries update durable node state together with Kubernetes action progress.  A failed rolling update therefore leaves the affected node visibly drained with the operation reason until retry or explicit maintenance recovery proves health and reactivates it.

Maintenance admits one drained application node at a time.  Restore the existing drained node before draining another.

Before maintenance drain starts, the controller moves PostgreSQL, scheduler, Cluster Virtual IP, and WireGuard ownership away from the target and verifies that the Cluster Virtual IP lease names an eligible non-target node.  The drained node remains ineligible for Cluster Virtual IP ownership until verified maintenance exit restores role eligibility.

Embedded-etcd voter membership remains active during application maintenance.  Maintenance drain is not cluster membership removal.

The durable application-capacity gate blocks updates, HMR start, membership changes, normal PostgreSQL switchovers, and additional maintenance while any active member remains drained.

Successful maintenance recovery clears failed-operation degradation only after every recorded active application node is active and supported membership plus observed PostgreSQL health remain intact.

During Cluster-Wide Node Isolation, selecting "**Exit Maintenance Mode**" on the drained standby warns that isolation will end, requires `EXIT HMR`, and submits controller-owned isolation exit instead of an independent node restore.

## Probe and Shutdown Contract

Borealis uses three distinct probe contracts:

* **Startup** proves initialization completed; slow initialization belongs here
* **Readiness** proves the workload can serve its current role

  * API and scheduler readiness include required PostgreSQL access
  * WireGuard owner readiness requires the interface and usable tunnel state
  * WireGuard standby readiness requires the controller socket to be healthy and the shared interface withdrawn
  * The Route DaemonSet separately proves owner routes use the tunnel while standby routes use the Cluster Virtual IP
* **Liveness** detects local recoverable process failure only

  * Scheduler liveness never depends on PostgreSQL

Current stable K3s can incorrectly run liveness before startup after liveness restarts a container.  Borealis makes that early execution harmless by delaying liveness longer than the startup probe's complete failure budget for every container that uses both probes.

Startup remains responsible for failed initialization, readiness keeps traffic away, and liveness begins only after startup either succeeds or performs its own restart.  Exact-version qualification proves this delay resets across ten consecutive liveness-triggered replacements on every node.  This is Borealis compatibility protection, not a claim that the upstream bug is fixed.

### Drain Workloads

The drain sequence:

* Stops assignments
* Withdraws readiness
* Waits for node EndpointSlices to converge
* Forwards termination signals
* Bounds in-flight drain inside `terminationGracePeriodSeconds`

Workloads use `preStop`, `minReadySeconds`, topology constraints, and disruption budgets.  The `preStop` readiness marker exists only during the bounded shutdown hold and is removed before the hook returns, so a container restart inside the existing Pod cannot inherit stale drain state from pod-local temporary storage.

Maintenance and HMR drain remain durable through cluster-controller and node-label state.

### Restore Workloads

Returning from maintenance uses the reverse safety order.

Borealis starts node workloads while the node remains application-drained, waits for ready endpoints and the minimum-ready soak, restores role eligibility, verifies WireGuard and role-aware health through another soak, and only then clears drain.

A failed restore leaves the node drained instead of sending work to a partially restored service.

[ngrok probe guidance](https://ngrok.com/blog/probes) correctly treats readiness as traffic eligibility and liveness as a narrow local-health check.  Kubernetes starts endpoint removal and Pod termination concurrently, however, so Service withdrawal is not guaranteed to finish before `SIGTERM`.  Borealis therefore combines readiness withdrawal, EndpointSlice verification, `preStop`, signal handling, and connection draining.

See [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/) and [Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/) for the upstream behavior.

## Recover Cluster-Wide Node Isolation { #use-cluster-wide-node-isolation }

New Cluster-Wide Node Isolation / HMR entry is disabled on enabled one-node and three-node clusters. Use a standalone Engine for mutable development and publish an immutable release for cluster updates. Legacy acknowledgement flags do not enable clustered development.

Existing isolation keeps its recorded target and pinned production baseline. Maintenance shows that target and offers **Disable Isolation** when recovery is available. The cluster banner remains visible until isolation is disabled. Updates, membership changes and normal maintenance stay blocked during isolation.

Old queued or interrupted entry operations halt into failed restoration state before further runtime action. Recover through **Disable Isolation**; retrying the old entry is rejected. Cancelling queued HMR work retains the isolated target and recovery state until production restoration succeeds. Existing failed-exit retry and lost-target recovery remain available.

### Disable Isolation

Disabling isolation restores the saved pinned cluster baseline, not the local working tree.  Local edits remain available for a later commit or release.

The controller restores standby nodes one at a time.  Each node remains drained until workload and role-aware health complete two minimum-ready soaks.

The controller then moves roles away from the isolated node, drains its development workloads, builds the saved baseline revision as an isolated candidate, probes and soaks that candidate, promotes it, and repeats active plus role-aware health checks before clearing the final drain.

This ordering keeps production available while the isolated node returns to the pinned baseline.  A one-node cluster uses the same candidate gates with the expected maintenance interruption.

If the isolated node is lost, Borealis fences it before recovering pinned-baseline workloads on standby members through the same sequence.

Use [WebUI HMR Development](webui-hmr-development.md#clustered-node-isolation-hmr-preview) for existing-isolation recovery, secure CLI authentication and pre-deployment restoration qualification. Mutable development now requires a standalone Engine.

Isolation never distributes mutable source to standby nodes.  Only a Cluster Management stable-release update moves an accepted immutable revision across all nodes.

## Compare Engine Versions

Open **Admin > Cluster Management > Nodes** and compare the **Engine Version** column before updating. Each row shows that node's recorded release. Hover or focus the version to read its full commit SHA and report time. Missing release or SHA appears as **Unknown**; Borealis does not substitute the cluster baseline.

The second line distinguishes **Recent report**, **Stale report**, and **Report time unknown**. Reports older than five minutes are stale. These labels describe stored reports, not a fresh measurement of the running Engine version or a node-health verdict. A recent report can come from another lifecycle action.

During an Engine update, affected rows show **Pending** with the requested release. **Target recorded** means the stored release and SHA already match that operation's target. The tooltip keeps the target SHA separate from the recorded SHA. Failed, cancelled, or completed operations stop showing a pending target.

If refresh fails or no successful snapshot arrives for more than 15 seconds, rows show **Snapshot stale** while retaining the last records. Pending targets may also be outdated until refresh recovers. Restore API access and use **Refresh** before relying on that view for another operation.

## Perform Cluster-Aware Engine Updates

Use Maintenance instead of `git pull` on clustered Engines.  Cluster Management exposes separate stable and qualification selectors backed by the configured Borealis GitHub repository.  The cluster does not generate versions.

### Release Channels

Stable releases use `YYYY.MM.REVISION` or `YYYY.MM.REVISION.HOTFIX` and must be normal GitHub releases.

Qualification releases append `-rc.N`, such as `2026.09.2-rc.1`, and must have GitHub prerelease status.  `N` begins at `1` and increases for each candidate of the same intended stable version.

Branch names, draft or mutable releases, malformed tags, and tag/GitHub-channel mismatches never become selectable targets. Releases published before repository immutability was enabled require a newly published version.

Maintainers create immutable qualification and stable releases through [Publishing Engine Releases](publishing-engine-releases.md).  Operators select the approved result here; the cluster never creates, retags, or repairs a release.

Each selector hides versions older than the pinned baseline.  Same-version and newer entries remain visible, while incompatible entries remain disabled with a reason.

A stable release ranks after all `-rc.N` candidates that share its dotted base, which allows a tested qualification commit to become stable without being treated as a downgrade.

Borealis rolling support policy covers only the latest stable release or hotfix.  Qualification clusters remain visibly unsupported until promoted forward to stable.

A commit-backed `dev-*` baseline has no calendar version.  The API therefore shows only stable or qualification targets whose tagged commits contain the pinned development commit and satisfy compatibility checks.  The `dev-*` identity affects initial formation, membership, and HMR restoration; it never makes mutable branch heads selectable.

The selected tag resolves once to an immutable commit SHA.  The API verifies that the target is the same commit or a fast-forward descendant of the current pinned SHA.  The node manager independently verifies exact tag-to-SHA resolution, clean checkout, configured origin, and fast-forward ancestry before staging.

Downgrades and unrelated histories fail closed.

Queueing refreshes release verification even when the selector already shows a compatible version. If GitHub is unavailable or publication changed, restore access or select a newly published immutable release before retrying the request.

Engine compatibility follows the cluster's verified K3s version. A completed K3s upgrade changes eligible Engine releases immediately; an old API process environment cannot restore the previous baseline. Unknown or conflicting version observations block updates until cluster state is reconciled. Engine redeploy preserves installed K3s instead of applying the fresh-install default.

!!! info "Operations queued before immutable verification"
    Older queued Engine updates without immutable-release proof stop at preflight. Cancel queued work at its safe boundary, then select and queue a verified release. If an older operation already failed after changing runtime state, recover that state through the existing maintenance procedure before queueing fresh work. Retrying an old record does not manufacture missing verification.

Every cluster-capable release must include `Data/Engine/release-manifest.json`.  `allowed_release_channels` must explicitly contain `stable`, `qualification`, or both.  The manifest also declares the minimum rolling source, mixed-version window, schema phase, K3s baseline, and required probe conformance.

### Deploy a Qualification Release

Deploying a qualification release requires a whole-cluster action and the exact acknowledgement `DEPLOY QUALIFICATION`.

Borealis records the last stable baseline and displays an unsupported warning globally and in Cluster Management.

The expand-schema phase may run for a qualification candidate.  The contract/finalize phase waits for stable promotion.

Promotion from qualification also requires a whole-cluster stable action so every node moves from the RC tag to the stable tag even when both resolve to the same commit.  The stable promotion manifest must retain `expand-contract` while schema finalization remains pending.

Automatic rollback and destructive schema rollback are not supported.  Recover the failed operation, then promote or roll forward.

### Distribute Source and Images

No supported local-only source replication exists between Engine nodes.

The update controller requires every target node to fetch the selected published tag from the configured Git origin, verify that the tag resolves to the recorded SHA, and fast-forward a clean checkout before local image staging.  A dirty, diverged, or manually copied worktree fails closed.

An untagged clean checkout is allowed only as a commit-backed development baseline.  It cannot be selected as a rolling-update target.

K3s Spegel remains active for peer-to-peer container-image content.  Spegel can serve imported image layers between cluster nodes after Borealis pins them in containerd, but it does not copy the Git checkout, create release metadata, validate the compatibility manifest, or authorize a local branch as a cluster release.

Internal `FetchRelease`, `StageRevisionImages`, and `--cluster-stage-revision` contracts belong to the controller and node manager.  Operators must not call them as a substitute distribution path.

An offline or non-GitHub cluster release would require a separate signed release-bundle feature carrying source revision, manifest, image identity, and controller audit data.

Current supported paths remain:

* A published stable GitHub release through "**Update Node**" or "**Update All One at a Time**"
* An approved GitHub prerelease through "**Deploy Qualification One Node at a Time**"

### Update Engine Nodes

`Update Node` and `Update All` use the same node workflow:

1. Acquire the exclusive operation lease and create the pre-change snapshot.
2. Move PostgreSQL, edge/WireGuard, and scheduler leadership away when required.
3. Stop assignments, withdraw readiness, drain work, and verify EndpointSlice withdrawal.
4. Fetch the release and fast-forward the checkout to the stored SHA.  A dirty or diverged worktree fails without stash or reset.
5. Invoke the target revision's fixed image-staging workflow locally and build/import immutable images without starting candidate workloads.
6. When the release declares `expand-contract`, run the target image's fixed, idempotent expand-schema Job before the first target candidate starts.
7. Create node-pinned candidates outside shared Services through separate labels and selectors.  Require startup, readiness, liveness, direct-endpoint, database, scheduler, Agent-path, and candidate-soak checks.
8. Promote the candidate into the active Deployment, then require shared Service, VIP, database, scheduler, Agent-path, WireGuard, and ready-soak checks.
9. Restore role eligibility, repeat health and ready-soak checks, restore assignments, and then clear application drain.
10. For the stable channel, run the target image's finalize-schema Job only after every active node reports the target SHA.  The qualification channel defers this contract phase.  Verify the cluster and advance the baseline release/channel after finalization.

`Update All` records an immutable non-leader-first node order when the request begins, then transfers roles and updates one application node at a time.  Runtime role movement cannot reorder or repeat nodes in the middle of the operation.

A failure halts the operation and leaves the failed node drained.  Healthy old and new nodes continue serving.  Retry resumes the explicit operation.  Borealis does not automatically roll back code or skip the failed node.

The cluster release baseline advances only after all active nodes reach the target.

One-node updates require maintenance-outage acknowledgement.  Three-node continuity covers ready HTTP/API/Agent endpoints, durable queued work, graceful drain, and reconnect after socket movement.  Existing interactive shell, desktop, or WebSocket sessions cannot transfer live.

`Update Node` may intentionally leave the cluster in a mixed-version state.  The expand phase remains complete and the contract phase stays pending until the final active node reaches the same target through a later explicit update.

Borealis records both schema phases by immutable release SHA so controller restart or operator retry cannot apply an already completed phase twice.

## Update K3s

Engine release updates and K3s upgrades are separate operations.

The Maintenance view accepts only a stable `vX.Y.Z+k3sN` K3s target.  The target must be newer than the current version and remain on the current minor release or advance exactly one minor.

The source version must already pass Borealis probe conformance.  The upgrade image must use `registry/repository@sha256:digest` form.

The K3s controller:

1. Takes a pre-change snapshot.
2. Orders non-leaders before leaders.
3. Drains one application node.
4. Creates an exclusive system-upgrade-controller Plan selecting only that server.
5. Keeps Plan concurrency at one and cordons the host while the immutable `k3s-upgrade` image applies the exact version.
6. Requires Node Ready state, etcd voter health, exact kubelet version, and local probe conformance.
7. Starts application workloads while the node remains drained.
8. Requires Engine health and ready soak.
9. Restores role eligibility.
10. Requires a second health soak before clearing drain and moving to the next server.

A failed Plan or probe halts the update and leaves the node drained.

One-node clusters require the exact acknowledgement `ACCEPT OUTAGE`.

## Aegis Unlock Across Replicas

The Aegis key remains memory-only.

Clustered API replicas use a cert-manager-issued TLS 1.3 mutual-certificate endpoint on a headless internal Service.  One successful setup or unlock verifies the key locally, resolves the current API replica addresses, and sends the bounded 32-byte key to every replica.

Unlocked replicas continue bounded reconciliation so later replacement candidates receive the key before promotion.

The receiver re-verifies the key against the Aegis verification token before installing the memory copy.  The endpoint accepts no operator session, arbitrary payload, or non-mTLS caller.

An all-cold cluster restart still requires one operator unlock.

### Certificate Renewal and CA Rotation

API processes reload certificate, private key, and CA trust for new TLS handshakes. Normal cert-manager leaf renewal needs no API restart. A partial or malformed projection retains the last valid identity only while its certificate chain remains valid under current trust. Expiry stops key propagation; repair the certificate projection without restarting the surviving unlocked replica.

Node workload reconciliation creates `borealis-aegis-trust` once from the existing public CA certificate. Later deployments preserve this ConfigMap. Updated active and candidate replicas require its `ca-bundle.crt`; cert-manager continues to manage the leaf Secret separately. If this ConfigMap disappears after adoption, restore the approved trust bundle before redeploying.

Check renewal before the leaf or CA expires:

```bash
sudo k3s kubectl -n borealis get certificate borealis-api-aegis-mtls borealis-cluster-ca \
  -o custom-columns=NAME:.metadata.name,READY:.status.conditions[-1].status,EXPIRY:.status.notAfter,RENEWAL:.status.renewalTime
sudo k3s kubectl -n borealis get configmap borealis-aegis-trust
```

!!! warning "Keep one unlocked holder running"
    Complete rollout of certificate reload support on every API replica before rotating the CA. Keep at least one unlocked replica running throughout renewal, recovery, and staged CA rotation. Restarting every holder loses the memory-only key and requires operator unlock. Expired certificates never gain an automatic validity extension.

CA rotation requires three ordered stages. Prepare the next CA through the approved PKI procedure before changing the issuer. The files below contain public CA certificates only.

1. Publish both current and next CA certificates. Confirm every API replica has the same mounted bundle and passes fresh mutual-TLS checks before issuing leaves from the next CA.
2. Change the signing CA and request renewed API leaf certificates through cert-manager. Confirm every running replica serves the new certificate, accepts fresh mutual-TLS connections, and remains unlocked. Updating a CA issuer's Secret alone does not trigger leaf reissuance; follow the [cert-manager CA issuer guidance](https://cert-manager.io/docs/configuration/ca/).
3. Replace the bundle with the next CA alone after all replicas transition. Verify peers using retired certificates are rejected. Keep the ConfigMap present; deleting it is not a retirement mechanism.

Publish the overlap bundle from approved public certificate files:

```bash
cat current-ca.crt next-ca.crt > aegis-ca-overlap.pem
sudo k3s kubectl -n borealis create configmap borealis-aegis-trust \
  --from-file=ca-bundle.crt=aegis-ca-overlap.pem --dry-run=client -o yaml \
  | sudo k3s kubectl apply -f -
```

After every replica passes the new-certificate checks, retire the old CA:

```bash
sudo k3s kubectl -n borealis create configmap borealis-aegis-trust \
  --from-file=ca-bundle.crt=next-ca.crt --dry-run=client -o yaml \
  | sudo k3s kubectl apply -f -
```

!!! info "Trust changes are explicit"
    Removing a CA takes effect for fresh handshakes even when a leaf update is incomplete. A replica still using the removed CA fails closed until its identity is repaired. Malformed trust updates preserve the last valid bundle; inspect API logs and fix the projection. Kubernetes volume updates are asynchronous, so a successful ConfigMap write alone is not proof that every replica has adopted it. See validation commands in the detailed breakdown below.

## Failure and Recovery Rules

* `Degraded Quorum` records emergency two-of-three membership after an externally fenced removal.  Healthy nodes remain enabled, and single-replacement admission remains available.
* A failed cluster operation can also fail closed under `Degraded Quorum`.  Retry remains the normal recovery path.
* When a failed operation target is permanently unavailable and the cluster still records three active members, externally fence the target before using emergency removal.  The same two exact confirmations and three-member store checks still apply.
* `Degraded Database` records fewer Ready CloudNativePG instances than configured.  The controller returns status to `Healthy` after all configured instances recover but does not overwrite HMR, mixed-version, pending-membership, or operation-failure state.
* Planned PostgreSQL switchover creates a pre-change backup before role movement.
* Emergency PostgreSQL failover skips the new backup because a failed primary may be unable to produce one.  Use retained scheduled and previous pre-change backups when recovery requires older data.
* PostgreSQL synchronous quorum requires one replica acknowledgement on supported three-node clusters.  Writes stop when durability quorum is unavailable.
* Planned PostgreSQL role movement requires the selected replica to be actively streaming, participating in synchronous quorum, and caught up through the primary's current flushed WAL before promotion.
* The controller waits for all configured replicas to return synchronized after switchover before moving other application roles.
* When CloudNativePG reports missing archived WAL or failed `pg_rewind`, do not force that replica into service.

  * Keep the confirmed primary plus one synchronized replica.
  * Take a primary-targeted snapshot.
  * Rebuild the stale replica one at a time by deleting only its Pod and PVC.
  * Confirm the retained stale Longhorn volume does not consume node capacity required for the replacement volume before continuing.
* Borealis does not perform automatic PostgreSQL or Cluster Virtual IP failback.  The operator chooses switchover after the failed owner returns.
* Host-initiated shutdown or reboot performs bounded Cluster Virtual IP handoff to a Ready Engine peer before K3s stops.  A one-node cluster skips handoff because no peer exists.
* Sudden power loss still relies on lease expiry and can interrupt ingress until a new owner acquires the Cluster Virtual IP.
* Three-node clusters keep one healthy shared Agent-artifact volume replica on every Engine.
* Planned maintenance, node removal, and Engine or K3s updates wait for all three Agent-artifact copies.
* Emergency recovery and maintenance/HMR exit remain available when storage is degraded.
* Safe removal supports `3 -> 1`.
* Emergency removal from supported three-node membership requires an external power fence plus both exact confirmations and records degraded state.
* Longhorn snapshots use daily fourteen-snapshot retention plus pre-change snapshots.  They provide in-cluster recovery, not disaster recovery.
* Prove a recovery snapshot before relying on it by restoring it into a separately named one-instance CloudNativePG validation cluster.
* Kubernetes VolumeSnapshot data sources are namespace-scoped.  Keep the validation cluster in the same namespace with unique Services and PVCs, verify restored data without changing production, and remove only the validation resources after recording evidence.
* The Aegis key remains memory-only.  An all-cold restart requires operator unlock.

## Detailed Implementation Reference

??? example "Detailed Codex Breakdown"

    ### Clustered HMR entry boundary

    Public API, store creation/retry, controller dispatch and step runner reject `hmr_start`. Legacy queued/interrupted entry fails before Kubernetes intent and becomes `restore_failed` with its target retained. Failed HMR exit may queue through the generic quorum failure status; existing controller membership, health and fencing checks still apply. CLI dev commands require confirmed standalone membership; lookup failure never means standalone. WebUI retains recovery controls only. Canonical procedures, test/UI checks and exact-release #492 restoration gate live in [WebUI HMR Development](webui-hmr-development.md).

    ### Engine release identity

    - Nodes grid uses existing `nodes[].release_tag`, `release_sha`, and `last_seen_at`. It never fills missing node identity from `baseline_release`, probes, or K3s observations. These fields are lifecycle records; no fresh runtime Engine identity field exists in this snapshot contract.
    - `clusterNodeVersionPresentation` keeps recorded identity, report age and pending target separate. Only queued/running/waiting `engine_update` operations apply; `payload.update_node_ids` takes precedence, then node scope uses `payload.node_ids`, or all scope applies to active members. Removed nodes and K3s targets are excluded. Equal recorded tag/SHA shows target recorded without claiming a new runtime measurement.
    - Node report timestamps are Unix seconds. Missing, invalid or future timestamps have unknown age. A browser clock tick ages reports and snapshot freshness even when fetch remains unresolved. A failed refresh immediately marks the snapshot stale; a successful response clears that flag. Snapshot receipt is processed independently of release-catalog/history waits; superseded polling completions cannot replace newer records or freshness state. Fresh snapshot delivery does not refresh the node's stored report timestamp.
    - Browser verification after an approved qualified deployment: open `/cluster-management?tab=nodes`, compare all three rows, hover or keyboard-focus each version for the full SHA, inspect a rolling update's affected/unaffected rows, then interrupt browser API access and verify retained rows become stale and recover after reconnect. Portable tests cover the same presentation with three-node fixtures; Q01 retains live exact-release verification.
    - `clusterReleaseState` reads the persisted Engine baseline and `config.k3s_version`. Active-node `roles.k3s_version` observations veto known disagreement; missing configuration fails closed. Process environment is not an update authority. K3s operation completion advances configuration only after ordered node conformance succeeds.
    - Release picker cache includes repository, source release/SHA, API/raw source and authoritative K3s version. `resolveClusterRelease` bypasses cache, verifies current immutable publication, resolves one SHA, and reads `Data/Engine/release-manifest.json` at that SHA. Annotated tag objects are fetched by SHA from the configured repository, never from response-supplied URLs.
    - Internal operation payload retains `release_immutable`, `source_k3s_version`, source release/SHA and compatibility manifest alongside target release/SHA. Queue transaction rejects configuration/source changes under the cluster-state lock. GitHub calls and manifest processing happen after snapshot connections return to the pool.
    - Every Engine preflight, including retry, validates persisted proof and reads each active Node's `status.nodeInfo.kubeletVersion` before backups, role movement or drain. Node-manager release actions independently require exact tag-to-SHA identity. Retry keeps original proof and target; it never reselects a release or silently upgrades a legacy payload.
    - Fresh-install baseline remains `v1.36.3+k3s1`. Historical lab results on `v1.36.4+k3s1` are not interchangeable; Q01 must qualify the exact release manifest and observed version. `install_k3s_if_missing` skips installed K3s, so Engine update cannot implicitly downgrade an upgraded cluster.

    ### Aegis TLS runtime behavior

    - `aegis_cluster_tls.go` shares a mutex-protected reloader per `authService`. New listener handshakes and each bounded fanout use immutable snapshots with TLS 1.3 and verified client certificates. Session resumption and key-transfer connection reuse are disabled; redirects never receive the key. In-flight requests remain bounded by existing five-second server/client timeouts.
    - The reloader double-reads bounded regular PEM files and re-resolves projection symlinks, rejecting changing or mixed generations. It validates the leaf/key match, peer DNS name, server and client EKUs separately, and current chain validity. Well-formed CA removals are accepted independently of leaf readiness; cached credentials must still verify under newest accepted trust. No TLS reload persists, clears, or derives an Aegis key.
    - `reconcile-node-workloads.py` creates the public trust ConfigMap only before first adoption, using create-only semantics and preserving a concurrent winner. Read errors fail closed. A missing bundle referenced by an existing API Deployment requires operator restoration. Required projected Secret/ConfigMap sources share one `..data` generation; no `subPath` mount prevents refresh. Existing `BOREALIS_AEGIS_CLUSTER_TLS_CA` selects the bundle; the legacy default `ca.crt` remains only for previously rendered workloads.
    - Cert-manager leaf `ca.crt` is not a durable rotation policy. [Certificate resource guidance](https://cert-manager.io/docs/usage/certificate/#target-secret) explains why consumers need independently distributed trust. Keep the public ConfigMap backed up with the approved CA rotation record; never replace it automatically during renewal.

    ### Aegis TLS validation

    Use host OpenSSL to inspect each mounted public leaf and compare trust digests. These commands do not print private keys or Aegis material:

    ```bash
    for pod in $(sudo k3s kubectl -n borealis get pods -l borealis.io/aegis-peer=true -o jsonpath='{.items[*].metadata.name}'); do
      printf '%s\n' "$pod"
      sudo k3s kubectl -n borealis exec "$pod" -- cat /var/run/secrets/borealis-aegis-mtls/tls.crt \
        | openssl x509 -noout -serial -issuer -dates
      sudo k3s kubectl -n borealis exec "$pod" -- sha256sum /var/run/secrets/borealis-aegis-mtls/trust-bundle.pem
    done
    ```

    Mounted material alone does not prove serving identity. For every ordered pair of API replicas, run a fresh request from the source Pod to the target Pod IP, preserving the Service DNS name. Set `source_pod` and `target_ip` from the current API Pod inventory. Curl must finish successfully and print `400`: TLS and client authentication succeeded, then the bounded handler rejected an intentionally empty key. Do not use an actual Aegis key for probes.

    ```bash
    sudo k3s kubectl -n borealis exec "$source_pod" -- curl --silent --show-error \
      --connect-timeout 2 --max-time 5 --tlsv1.3 --noproxy '*' \
      --cacert /var/run/secrets/borealis-aegis-mtls/trust-bundle.pem \
      --cert /var/run/secrets/borealis-aegis-mtls/tls.crt \
      --key /var/run/secrets/borealis-aegis-mtls/tls.key \
      --resolve "api-backend-aegis.borealis.svc:9444:$target_ip" \
      --header 'Content-Type: application/json' --data '{"key":""}' \
      --output /dev/null --write-out '%{http_code}\n' \
      https://api-backend-aegis.borealis.svc:9444/internal/cluster/aegis-key
    ```

    During next-CA-only qualification, use only the new CA as client verification trust and inspect the server's presented certificate; successful full mutual authentication must use the new chain. Retired-client rejection needs a separately retained lab test identity under the old CA. Do not retain or expose production private keys for this probe. Confirm a newly started locked API replica receives the verified key while exactly one other replica remains unlocked. Then prove an all-cold test restart requires operator unlock. Record served serials, trust digests, Pod identities, release SHA, and outcomes in Q01 evidence. Portable TLS tests exercise these boundaries without modifying the deployed cluster.
### API Endpoints
- Cluster state and events: `GET /api/server/cluster`, `GET /api/server/cluster/events`
- Lightweight banner state: `GET /api/server/cluster/banner`
- Includes `hmr_node_name`
- Includes release channel and baseline
- Includes last stable identity
- Includes pending schema-finalization state
- Allows the global shell to display isolation or qualification risk without loading the Admin-only cluster snapshot
- Enable, invitation, admission, and scaling:
- `POST /api/server/cluster/enable`
- `POST /api/server/cluster/invitations`
- `POST /api/server/cluster/admissions/{id}/approve`
- `POST /api/server/cluster/admissions/{id}/cancel` — Admin session, canonical UUID, bounded JSON containing only exact `CANCEL ADMISSION` confirmation. UI sends this fixed enum; server rejects unknown fields and other values. Focused boundary tests live in `cluster_admission_test.go`.
- `POST /api/server/cluster/membership/scale`
- The enable body permits only `cluster_vip`.  The API derives node name and host management IPv4 from Downward API environment and architecture from the AMD64 Go runtime.
- Stable, qualification `*-rc.N`, or exact `dev-<12-character SHA prefix>` baseline must pair with the full lowercase commit SHA.
- The current release accepts pair preparation and `desired_size=3` while active size is one, or one replacement while state is exactly `2 active / 3 desired` `Degraded Quorum`.  Size-five and stale invitation/admission paths fail closed.
- Node and database operations:
- `POST /api/server/cluster/nodes/{id}/maintenance`
- `POST /api/server/cluster/nodes/{id}/remove`
- `POST /api/server/cluster/postgres/switchover`
- `POST /api/server/cluster/postgres/emergency-failover`
- Safe `3 -> 1` removal requires canonical `paired_node_id` plus `REMOVE NODE PAIR`.
- Emergency removal requires `TARGET IS POWERED OFF` plus `EMERGENCY REMOVE NODE`.
- Release, HMR, and operation endpoints:
- `GET /api/server/cluster/releases`
- `POST /api/server/cluster/hmr/start` — legacy bounded contract; returns `409 cluster_hmr_entry_disabled` after Admin/input validation.
- `POST /api/server/cluster/hmr/exit`
- `POST /api/server/cluster/updates`
- `POST /api/server/cluster/operations/{id}/retry`
- `POST /api/server/cluster/operations/{id}/cancel`
- Catalog entries include `channel=stable|qualification`.  Tag syntax and GitHub prerelease status must agree.
- Engine qualification update requires all scope and exact `DEPLOY QUALIFICATION`.
- Stable update requires exact `UPDATE CLUSTER`.
- The K3s form uses all scope, a stable 32-character-bounded version class, exact `UPDATE K3S`, and one-node outage acknowledgement.
- Bootstrap endpoints:
- `POST /api/bootstrap/cluster/join`
- `GET /api/bootstrap/cluster/join/{id}/events`
- Public handlers validate canonical UUIDs, dotted-numeric release tags up to 32 characters, DNS hostnames up to 253 characters, node names up to 63 characters, single-line reasons up to 256 characters, authenticated invitation bundles up to 16 KiB, and fixed acknowledgements.

```
### Source Map
- API, store, and controller: `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster*.go`, `cluster_controller.go`
- Fixed root helper: `Data/Engine/Containers/api-backend/cmd/borealis-node-manager/`
- Route daemon: `Data/Engine/Containers/api-backend/cmd/wireguard-route-daemon/`
- CRDs, controller, RBAC, and availability: `Data/Engine/K3s/cluster/`
- WebUI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Cluster_Management.jsx`
- Enable dialog uses shared glass overlay, input, and pill-action tokens from `Data/Engine/Containers/webui-frontend/data/web-interface/src/DialogStyles.jsx`.  It contains one `cluster_vip` field and no typed confirmation.
- WebUI tab keys are `overview`, `nodes`, `database`, `operations`, and `maintenance` through `?tab=` URL state.  `operations` appears as Cluster Events, and Engine release controls live under Maintenance.
- Nodes and Cluster Events use Quartz AG Grid with 44px rows and headers, 20-row default pagination, and `20`, `50`, and `100` selectors.
- Nodes derives Database text from `leaders.postgres_primary` with `roles.postgres_primary` fallback plus aggregate CloudNativePG readiness.
- Primary displays linked `Active`.
- Non-primary active members display `Replica Healthy` only when configured instances equal active membership, every configured instance is Ready, and `fully_ready` is not false.  Otherwise replicas display `Not Ready`.
- Inactive historical rows display `Not Active`.
- Aggregate readiness cannot identify one failed replica, so degraded state marks every passive replica `Not Ready`.
- Cluster Events incrementally consumes cursor-paginated `/api/server/cluster/events` records and joins them to operation rows for hostname resolution and copied diagnostics.
- Node row actions use shared `Grid_Row_Context_Menu_Button.jsx` and `Row_Context_Menu.jsx` components.
- Release compatibility: `Data/Engine/release-manifest.json`
- Initial cluster baseline: `Engine.sh` prefers an exact dotted-numeric tag.  A clean untagged checkout emits `dev-<FIRST_12_COMMIT_CHARACTERS>`; API, controller, CRD, and node manager require the identity to match the full SHA.  A dirty checkout emits no baseline.
- The development commit must be reachable from the configured origin for cross-node fetch and HMR restoration.
- Release catalog traversal does not assume GitHub publication order matches version order.  It skips drafts, malformed tags, GitHub-channel mismatches, and downgrade-class entries, then continues until the exact current tagged release appears.
- Development baselines stop after the newest page.
- Every target uses the GitHub compare API with the full baseline and target SHAs.  Only `ahead` or `identical` remains selectable.
- The manifest must explicitly allow the target channel.

### State Ownership
- `BorealisCluster`, `BorealisNodeAdmission`, `BorealisNodeRuntime`, and `BorealisClusterOperation` hold desired/runtime Kubernetes state.  PostgreSQL remains the durable audit and operation-event source.
- The controller reconciles cluster size, Cluster Virtual IP, baseline, HMR state, recent admissions, recent operations, and one `BorealisNodeRuntime` per active Engine node.
- CR specs carry desired state.  Status carries observed phase, approval, operation, Kubernetes node, runtime owner, probe, release, and drain state.
- `BorealisCluster` accepts active size two only for `2 active / 3 desired` `Degraded Quorum` replacement recovery.
- PostgreSQL retains legacy `control_plane_vip` and `edge_vip` columns during transition.  New enable operations write the same `cluster_vip` into both, and the controller rejects unequal values.
- The ten-second repair loop recreates missing resources, patches drift, and avoids unchanged status writes.
- PostgreSQL tables `cluster_state`, `cluster_nodes`, `cluster_admissions`, `cluster_operations`, `cluster_operation_events`, `cluster_audit_events`, `cluster_invitations`, `realtime_outbox`, `cluster_application_leases`, and `cluster_schema_phases` hold audit data, events, invitations, outbox state, singleton leases, and immutable schema-phase completion.
- The controller observes CloudNativePG configured and Ready counts, current primary Pod, phase, full-readiness, and synchronous durability quorum.
- Database observation persists under `cluster_state.config_json.database_runtime`, appears as top-level `database` in the cluster snapshot, and drives recoverable `Degraded Database` status.
- Transactional mutation gates also read observed database runtime directly, so `Mixed Version` or another higher-priority lifecycle status cannot mask reduced database readiness.
- Historical failed operations remain immutable.  The snapshot annotates globally superseded cluster-enable and membership failures, and the retry path rejects them transactionally after a later same-kind success.
- Retry records the failed durable step under `payload_json.retry_resume_step`, increments the attempt, and re-enters normal preflight.
- A successful preflight jumps to the recorded checkpoint instead of replaying completed drain, update, or membership steps.
- A preflight failure retains the checkpoint for the next attempt.  A missing or no-longer-valid checkpoint fails closed.
- This prevents a failed rolling update with one node already drained from restarting at the first node and temporarily draining two application nodes.
- The first controller claim pins its current immutable node-action image under `payload_json.action_image`.
- Every later controller holder uses that same image for operation-attempt Jobs, including after target-controller promotion.
- The source action image already exists on every current Engine node, so later targets do not depend on target-image distribution before their own staging step.
- Older operations without a pin use the current controller image until the next claim records it.
- Planned primary movement does not trust Pod Ready alone.
- The controller queries the current primary's `pg_stat_replication`, matches the exact target Pod `application_name`, requires `state=streaming`, requires `sync_state=sync|quorum`, and requires target `flush_lsn` at or beyond the query-time `pg_current_wal_flush_lsn()`.
- CloudNativePG manages application-role membership in the built-in read-only `pg_read_all_stats` role so PostgreSQL does not mask these replication fields.  The role does not grant table-data access or database mutation.
- After CloudNativePG changes primary, the controller requires healthy phase, every configured instance Ready, every expected replica streaming through current flushed WAL, and at least one synchronous replica before application-role eligibility changes.
- HMR step replay repeats the complete post-switchover gate even when the target already became primary.
- Safe paired removal resolves one active survivor and completes the same synchronized primary-transfer gate before reducing CloudNativePG membership.
- Step replay therefore repeats safe-survivor proof instead of allowing CloudNativePG to retain a primary on a removal target.
- If the survivor has no healthy synchronized replica, the operation fails before changing `spec.instances`.
- Planned removal records `payload_json.removal_fences` intent before creating the fixed `PrepareMemberRemoval` Job. Each record binds operation UUID, durable node UUID/name, Kubernetes UID, K3s etcd member name, action attempt and immutable action image.
- The node-manager validates expected Node UID/member identity before writing its persistent marker. Marker and restart-policy drop-in are atomically replaced and synced before successful acknowledgement. A marker from another operation or target fails closed. The action client checks the returned identity, fixed fence paths, service state and timestamp before reporting Job success; old helpers returning unbound success cannot establish proof. Upgrade installed node managers before planned removal.
- Controller acknowledgement requires the exact successful Job contract and matching observed Node identity. Intent/acknowledgement writes verify live PostgreSQL controller lease, active operation, current attempt/step and active target; they preserve existing payload fields and append `member_fence_intent` / `member_fence_acknowledged` events.
- Retry skips already-completed target drain/fence work only with matching durable acknowledgement. Missing Node is accepted only after acknowledgement exists. A completed exact Job can recover acknowledgement lost before database commit when the original Node identity is still observable; intent alone and NotReady never authorize removal.
- Both removal targets are checked before relaxing shared-artifact retry readiness. Managed etcd removal keeps the recorded member identity across polling; mutation uses Node UID/resourceVersion, and Node deletion uses the same identity plus UID/resourceVersion preconditions. Hostname reuse and stale confirmation cannot redirect removal.
- Source: `cluster_removal_fence.go` and `cluster_removal_fence_test.go` beside the controller; fixed host contract: `cmd/borealis-node-manager/member_removal.go` and its tests. H01 child #496 / PR #498 carries current validation; live failure qualification remains #480/#482.
- Fixed internal node-manager parameters `operation_id`, `node_id`, and `node_uid` are canonical lowercase 36-character UUIDs; `node_name` is a lowercase Kubernetes node identifier capped at 63 characters; `etcd_member_name` is 1-128 lowercase letters, digits or hyphens with alphanumeric first character. Controller constructs arguments from persisted identity; client and manager validate types/formats, and manager validates local identity before host work. These fields have no WebUI text entry or public API input. Focused controller/manager tests cover missing, malformed and stale identity plus unbound legacy acknowledgements; PostgreSQL inventory includes persistence/lease rejection.
- Fence wait tolerates temporary Kubernetes API errors through the bounded step timeout, then resumes delete and verification from the durable operation plan.
- Member verification scales the removed node's resident Borealis Operator and WireGuard deployments to zero before the final voter-health soak, preventing unschedulable infrastructure Pods from surviving successful removal.
- Controller preflight admits only replacement admission, emergency PostgreSQL failover, and maintenance exit while durable membership is temporarily two-of-three.
- The store also permits externally fenced emergency removal from a failed-operation `Degraded Quorum` state only while three active members remain recorded.  Normal removal and a second removal from replacement state remain rejected.
- Emergency PostgreSQL failover intentionally omits the pre-change backup so a failed primary cannot block promotion.  Planned switchover retains the mandatory backup.
- The controller persists `cluster_nodes.application_state` when node drain and exit steps cross their safe boundary.
- Enter-drain failure records a conservative drained state because the node manager writes the drain label before scaling workloads, preventing the API and UI from presenting a failed update target as active.
- During steady state, an unexpected Kubernetes `drained` label also records durable `k3s_restart_label_drift` drain and `Degraded Quorum`.  The controller never promotes an observed `active` label into durable active state without explicit verified recovery.
- The Cluster Virtual IP DaemonSet requires both existing control-plane and edge eligibility labels so the same node owns K3s API, application ingress, and WireGuard address.
- Before maintenance or HMR changes those labels, fixed node-manager reconciliation initializes both from current Kubernetes application-state labels, applies the combined selector, and waits for rollout.
- Labels are written before selector migration, preventing a migration-created VIP outage.
- Joined-node K3s configuration persists both eligibility labels across restart.
- The maintenance store validates active membership and current application state, permits one drained application node at a time, and accepts exit only for a drained target.
- Independent store and WebUI application-capacity gates keep normal operations closed while a durable active-member drain exists.
- HMR exit, emergency PostgreSQL failover, maintenance exit, and externally fenced emergency removal remain available.
- The store blocks independent maintenance mutation while HMR state is active.
- The WebUI therefore reroutes drained-node "**Exit Maintenance Mode**" during isolation to `/hmr/exit` after an explicit warning and `EXIT HMR`; the controller restores full pinned-production placement.
- Completion preserves `2/3` quorum degradation and remaining database degradation.  Verified three-node recovery returns Healthy only when no recorded active node remains drained.
- The node manager accepts fixed verbs only, including persistent K3s membership fence, exact-version probe conformance, drained application preparation, and `RunSchemaPhase` limited to `expand|finalize` plus the recorded target SHA.
- Preparation scales named node workloads and waits for rollouts without changing `borealis.io/application-state`.
- Operation retry also accepts an already-active state because an earlier attempt may have completed activation before a later step failed.  Any other state still fails closed.
- Only final activation clears drain after controller health gates.
- Joined-server configuration initially persists drained, role-ineligible labels.
- Every successful enter/exit drain transition atomically rewrites those runtime labels in `20-borealis-cluster-join.yaml`, preventing a later K3s restart from undoing controller-approved activation or maintenance fencing.
- The manager never exposes arbitrary command or remote-shell execution and remains active across controlled K3s restarts so enrollment and one-node-at-a-time K3s upgrades can wait for control-plane recovery without losing the operation process.
- Systemd `ExecStop` invokes local-only `shutdown-handoff`.  The exact `systemctl is-system-running=stopping` gate makes an ordinary service restart or update a no-op.
- A real host shutdown with a Ready peer temporarily labels the local node `borealis.io/engine-node=false`, waits up to 25 seconds for the fixed Cluster Virtual IP lease to name an alternate holder, and then allows K3s to stop.
- Persistent K3s configuration restores the Engine-node label after reboot.
- The installer precreates the exact `/etc/systemd/system/k3s.service.d` path, and the service sandbox grants only that systemd subtree for the persistent member-removal fence.  `ProtectSystem=strict` remains enabled.
- This ordering prevents a surviving kube-vip containerd shim from renewing a dead ingress lease after Traefik exits.
- Rolling schema work runs the target revision's `site-worker` image in a non-root, tokenless, node-pinned Job.
- Expand follows target-image build/import and precedes the first candidate health gate.
- Finalize runs only after every active `cluster_nodes.release_sha` equals the target.
- `cluster_schema_phases` makes both fixed phases restart-safe and idempotent.  Finalize refuses a missing expand record.
- The Agent-path update probe sends a credential-free `POST /api/agent/heartbeat` and requires exact `401 Unauthorized`.
- The candidate probe targets the isolated Pod IP.  The active probe targets the shared API Service.
- The expected authentication rejection proves the Agent route is registered and reachable without mutating Agent/device state or storing test credentials.
- A missing route returning `404`, accepting unauthenticated traffic, timeout, or any other status fails node health.
- Blank-node join requires an HTTPS origin and rejects every redirect for both POST submission and invitation-header polling. TLS verification remains enabled with system roots or an explicit private CA.
- Admission polling reads authoritative state and the latest 500 events scoped to the exact admission, consumed invitation, cluster, node and bearer hash. Approval does not depend on event presence or global history length.
- Initial unconsumed/pending invitation expiry remains 15 minutes; signed accepted-join access and stored creation timestamp impose a 24-hour bound for approved/admitted/recovery targets. Admin renewal swaps only the original admission's invitation binding and revokes previous access.
- Approval and original-operation retry pin `join_config` from `cluster_state.control_plane_vip`, `config_json.k3s_version`, active member addresses and the selected admission cohort. Exactly three private IPv4 peers are required. Client flags can assert those settings but cannot override them.
- The node manager validates settings, runs fixed `Engine.sh --cluster-prepare-node` only after approval, then rechecks approval/configuration before installing K3s. Preparation may outlast initial invitation lifetime without consuming an unapproved invitation.
- Controller reconciliation requires current lease ownership. Expired pending records release capacity only without approval, operation or non-removed member evidence. Failed/cancelled membership operations mark approved admissions `Recovery Required`; retry retains the original cohort and operation. No schema or automatic member deletion is involved.
- Expansion-pair approval records the same authenticated approval event for both admission IDs so both waiting joiners proceed.
- Degraded-quorum replacement records the same compatible approval event for one admission.
- Join installation replaces the running node-manager executable through atomic rename before enabling the service, avoiding in-place executable overwrite failures.
- Membership-admit completion reconciles `cluster_nodes` by unique `node_name`.
- Re-admitting a safely removed hostname revives the retained node row, preserves durable node ID and creation time, refreshes mutable host/release/probe state, clears the removal drain reason, and advances the new admission record to `Admitted` in the same transaction.
- Do not delete retained node history or replace durable node identity with the admission UUID.
- The cluster controller exposes distinct `/startup`, `/ready`, and `/live` contracts.
- Startup proves local initialization completed, readiness depends on PostgreSQL operation-store access, and liveness proves only local HTTP process responsiveness.
- Long database bootstrap, node redeploy, or Kubernetes reconciliation never makes liveness depend on controller-loop progress or external dependencies.
- The cluster controller renews its 20-second PostgreSQL ownership lease every five seconds through an independent heartbeat while operation steps run.
- Each acquisition has a five-second deadline.
- Timeout evicts the selected driver connection plus every remaining idle connection in the controller-only pool.
- This bounds reusable stale sockets after the driver returns but cannot interrupt an active `lib/pq` socket read blackholed during CloudNativePG primary failover.
- Live HMR partition recovery remains tracked in [issue #466](https://github.com/bunny-lab-io/Borealis/issues/466).
- Explicit ownership loss cancels step context immediately.
- Transient database errors during primary-Service handoff are retried for at most 15 seconds.  Persistent failure cancels before lease expiry.
- Claim, advance, failure, and completion transactions lock and verify the same live lease row before changing durable operation state, so a former holder cannot record progress after fencing.
- Node-action Job names retain operation-attempt identity while human-readable step labels replace illegal separators and hash-truncate values beyond Kubernetes' 63-character limit.
- The full unsanitized step remains in PostgreSQL and `BorealisClusterOperation` state for audit.
- Node-action Job creation is idempotent across controller restart and create races.
- The controller treats `409 AlreadyExists` as a resume candidate only after re-reading the Job and matching namespace, operation label, full step annotation, normalized step label, target node, ServiceAccount boundary, immutable image, command, and arguments.
- Engine-update replay alone may retain an existing Job's different immutable Borealis action image when the source controller created the exact operation, attempt, and step before the target controller acquired the lease in the middle of rollout.
- Every other image or identity mismatch fails closed without waiting on or replacing the existing Job.
- Node-action Job polling treats bounded Kubernetes API `429`, `5xx`, timeout, connection refusal/reset, and wrapped `io.EOF` failures as transient until step context ends.
- Authorization and TLS trust failures remain terminal.
- This prevents temporary control-plane restart transport loss from failing an action that remains active or completes successfully.
- Engine manages explicit `docker.io`, `ghcr.io`, `quay.io`, and `registry.k8s.io` entries in K3s `registries.yaml`.
- `embedded-registry: true` alone does not activate Spegel exchange.  Each source registry must be enabled on every node.
- Joined nodes receive mirror configuration before K3s starts.
- Borealis images are atomically staged under `/var/lib/rancher/k3s/agent/images` and imported by K3s.
- K3s pins these archives and adds the containerd distribution-source labels required by Spegel.
- Engine waits for both local image presence and the matching source label.  Direct `ctr images import` is not accepted because imported content can remain invisible to peers.
- Successful cluster-revision staging retains the current archive plus one rollback predecessor per managed service and deletes older service archives.
- This bounds restart-time preload work without removing the current or immediate rollback image.  Separately named operation-action and recovery archives remain untouched.
- The WireGuard route DaemonSet uses initialization-only startup, route-aware readiness, and local-process liveness.
- Cluster Virtual IP owner routes Agent CIDRs directly through `borealis-wg`.  Standby nodes route the same CIDRs through the fixed Cluster Virtual IP, so VIP movement changes next-hop ownership without rewriting every host route.
- A missing interface or temporary VIP convergence keeps readiness false while reconciliation remains alive.
- Full deploys and scoped WireGuard rebuild/reconcile operations apply the same DaemonSet renderer and wait for every Engine node to become ready.
- The WireGuard control Deployment remains running on every active Engine node.
- The controller checks the fixed Cluster Virtual IP on the host every second.  A non-owner suppresses validated activation mutations and withdraws any stale interface; the owner bootstraps a missing listener, interface, address, route, and firewall from shared identity and requires the live interface for readiness.
- The lifecycle withdrawal marker prevents the ownership loop from undoing `preStop` before the Pod exits.
- API replicas store active tunnel identity, readiness callbacks, transport timestamps, operator association, endpoint, expiry, and allowed ports in PostgreSQL with optimistic generation checks.
- The Cluster Virtual IP owner replays active session rows plus durable IP/key leases every three seconds, so a newly elected owner rebuilds only live listener peers and any API replica can answer status or scheduler readiness.
- Short-lived signed tokens remain derived rather than stored.
- Cluster conversion copies the existing server keypair into `borealis-wireguard-server-keys`.  API and WireGuard Pods mount the same read-only Secret on every node.
- The control process accepts Kubernetes projected-file symlinks only when the resolved key remains a regular file inside the mounted Secret directory.
- The API peer manager and Engine deployment both preserve `0770` on shared WireGuard configuration/key directories and `0640` on files so the root control process with only Borealis group membership can perform atomic configuration writes without broad filesystem capabilities.
- The WireGuard update candidate remains zero-replica to avoid host-port collision while the active owner-aware controller preserves ownership.
- The node manager reads only local K3s etcd metrics and reports the exact `etcd_server_is_leader` gauge as a node label.
- The cluster controller combines that report with the fixed Cluster Virtual IP lease, CloudNativePG current-primary Pod, scheduler application lease, and owner-aware WireGuard readiness.
- It persists observed etcd, Cluster Virtual IP, PostgreSQL, scheduler, and WireGuard owners into node-runtime roles.
- An update request pins a non-leader-first node ID sequence from these observations.  Later role movement cannot change the remaining sequence.
- Transfer-away fencing requires the Cluster Virtual IP lease to leave the target and keeps the WireGuard controller scaled without requiring previous-release standby readiness.
- The controller then accepts an actual eligible owner only after the WireGuard workload becomes Ready.
- New HMR entry is disabled before runtime role movement; existing exit/recovery retains its role and fencing checks.
- HMR exit pins PostgreSQL to the first restored standby but accepts any healthy non-target Cluster Virtual IP owner after target fencing.
- Drain withdrawal waits only on node-scoped traffic Services scaled or removed by application drain: API/Aegis, scheduler, guacd, Traefik, WebUI, and site workers.
- Resident operator and database endpoints remain available for control-plane and storage safety and cannot block rolling progress.
- Isolated candidate Pod endpoints remain available for candidate inspection and Aegis-key delivery but do not count as active traffic during withdrawal.
- Standalone generic Deployments remain zero-replica templates after cluster enablement.
- Scoped and full deploys update Secrets, Services, and templates without launching unpinned duplicate Pods.
- Per-node reconciliation always starts from the canonical generic template, then reapplies node affinity, immutable image, candidate fencing, and cluster-specific mounts while preserving each existing Deployment's immutable selector.
- Generated per-node Deployments use one authoritative server-side field manager and reclaim conflicting generated fields left by emergency `kubectl set` or patch operations.
- Existing node clones never remain the source of stale environment values, Secret references, probes, or operator-image allowlists.
- After promoted active workload passes rollout health, promotion copies immutable container images and the target revision to the matching generic template before deleting the candidate.
- The patch includes the fetched Kubernetes `resourceVersion`, so concurrent scaling or template mutation returns a conflict instead of overwriting newer state.
- Promotion validates that the returned Deployment remains zero-replica with the exact revision and images.
- A missing template, nonzero generic replica count, container mismatch, conflict, invalid response, or failed postcondition stops the operation without launching a generic workload.
- Baseline reconciliation preserves controller-owned application, edge, scheduler, and PostgreSQL eligibility labels on active and pending cluster members.
- Lost-HMR-target recovery does not commit durable `Degraded Quorum` state while the former target can still serve application traffic or own edge/WireGuard.
- If the target rejoins during recovery, the controller runs fixed `EnterApplicationDrain`, reapplies role-ineligible labels, waits for traffic-endpoint withdrawal, and requires the edge lease plus Ready WireGuard ownership on another node before committing the target as `hmr_target_lost`.
- The Cluster Virtual IP uses one kube-vip leader lease, `/32` ARP advertisement, local-metrics liveness, K3s API readiness on TCP `6443`, and a disruption budget.
- One owner prevents competing ARP advertisements while the same address serves K3s API, application ingress, and WireGuard.
- kube-vip `v1.1.0` health listener is intentionally disabled because it keeps the process alive after the manager loses leadership.  Normal process exit allows Kubernetes to restart the manager.
- Bootstrap does not accept a running Pod as proof.  It waits for DaemonSet rollout, a non-empty lease holder, the local VIP address, and K3s `/readyz` through the Cluster Virtual IP.
- First-node conversion uses explicit stop-start handoff for generic host-network Traefik and WireGuard Pods.
- Later Traefik candidate promotion uses the same host-port handoff.  The WireGuard candidate remains stopped while the active owner-aware controller is replaced.
- WireGuard promotion rewrites copied readiness to an owner-aware compatibility contract: control-reported standby is healthy only while the shared interface is absent, while active ownership still invokes the pinned image's full interface/listener/route health check.
- This allows HMR to restore a pinned production image from before standby-aware image probes without weakening owner validation.
- A failed first-node handoff restores the previous standalone host workload replica count.
- The job-scheduler candidate starts with `BOREALIS_SCHEDULER_LEADERSHIP_ELIGIBLE=false`.
- Its health probe can validate process and PostgreSQL without allowing the candidate to acquire the database lease or issue work.
- Promotion rewrites the flag to `true` before the active Deployment rollout.
- The cluster-controller candidate serves local probes but cannot acquire the operation lease.
- The API candidate disables background loops and stays outside public API traffic while remaining in the mTLS-only Aegis peer Service so the current memory key can reach the candidate before promotion.
- Promotion restores controller eligibility and API loops in the active Pod template.
- Candidate probes therefore cannot mutate cluster ownership or receive public application traffic before promotion.
- First-node SQLite-to-etcd conversion temporarily restarts K3s.
- Cluster-controller Job polling treats bounded Kubernetes API `429`, `5xx`, timeout, and connection failures as transient until the step deadline.
- Only already-created `EnrollCluster` Job polling permits a two-minute `401`/`403` recovery window while K3s switches datastore.  Persistent authorization failure and authorization errors in every other operation still fail.
- K3s Plan polling also treats bounded Kubernetes API `429`, `5xx`, timeout, and connection failures during the expected server restart as transient until the step deadline.
- Kubernetes Job `status.failed` counts retryable Pods, so the controller fails a Plan only when the Job publishes terminal `Failed=True`.
- A later successful retry can still complete the same Plan.
- Retry preflight lists exact operation-labeled Plans, verifies ownership, and deletes resources left by previous attempts before creating fresh attempt names.
- System Upgrade Controller normalizes Plan `status.latestVersion` from `vX.Y.Z+k3sN` to `vX.Y.Z-k3sN` and leaves the completed target cordoned.
- The controller accepts only the exact requested version or that exact separator normalization, explicitly uncordons the target, and still requires Ready etcd-voter health plus the exact `+k3s` kubelet version from Node status.
- Operation retry first checks the same Ready/voter/version contract and uncordons an already-upgraded target without submitting another restart Plan.
- Conversion explicitly restarts the Cluster Virtual IP kube-vip DaemonSet after K3s returns.
- The existing kube-vip process surrenders its lease during datastore restart, and unchanged server-side apply does not restart it.
- The controller verifies a fresh rollout, lease holder, local VIP address, and Cluster Virtual IP `/readyz` before continuing.
- Database cutover records the CloudNativePG Service URL before scaling the retained standalone StatefulSet to zero.
- Subsequent deploys preserve that URL only when CloudNativePG reports a healthy primary endpoint and standalone replicas remain zero.
- A failed partial conversion cannot silently revive the stale database.
- Operator-owned site-worker Pod restoration waits separately for named-Pod recreation and readiness.
- Cluster-enable and node-redeploy retries pass the recorded immutable baseline SHA through the controller Job and fixed node-manager contract.
- The node manager gives only the Engine child process command-scoped Git safe-directory trust plus the manager-owned writable build home under `Engine/Deploy/node-manager-home`.
- The hardened service keeps host `/root` unavailable.
- Workload initialization resolves the same pinned commit without mutating global Git configuration.
- Later operator checkout movement cannot change the retry revision.
- Engine updates split target-image staging from candidate creation.
- `StageRevisionImages` builds and imports every target image and writes root-owned `Engine/Deploy/cluster-staged-revision` only after imports finish.
- Expand schema then runs from the staged target image.
- `RedeployRevision` accepts the matching marker/image manifest before creating the candidate, preventing the target process from starting against pre-expand schema.
- Node redeploy starts the target release's staged manager as a separate transient activator outside the old service sandbox.
- The activator validates the pinned worktree and its own staged inode, waits until the node-action Job leaves Running or Pending state, atomically installs the target binary plus systemd unit, restarts the node manager outside the old service cgroup, verifies the running process uses the installed inode plus recreated Unix socket, and then records the target SHA in `/etc/borealis/node-manager-revision`.
- This allows the first rolling update to cross the old-manager/new-manager boundary without killing the action performing the update.
- Repeated redeploy skips refresh only when the recorded SHA and running executable already match.
- CLI operation polling tolerates bounded transient API loss after mutation acceptance.
- It never resubmits the POST.  Persistent loss reports the operation ID as still running server-side and directs the operator to inspect Cluster Events.
- Engine persists canonical private `BOREALIS_K3S_PEER_CIDRS` values into the runtime Secret.
- Node-scoped release deploy hydrates the same allowlist and fails closed when it is missing, preventing rolling updates from silently replacing cluster firewall policy with empty peer access.
- Shared Agent artifacts use one exact Longhorn RWX PVC.
- Three-node controller reconciliation raises only that bound volume to at least three replicas, verifies fixed PVC/PV/Longhorn ownership, and requires one healthy running replica on each active Engine before planned disruptive operations or pending-node workload activation.
- It never changes the global one-replica StorageClass policy and never reduces an operator-configured higher replica count.
- A `3 -> 1` downscale leaves the existing replica count unchanged to avoid deleting survivor copies.  Later expansion rebuilds missing per-node copies.
- Longhorn RWX storage requires host NFSv4 mount utilities in addition to iSCSI.
- `Engine.sh` checks device-mapper maps before storage reconciliation.
- Dedicated hosts with no genuine multipath maps have `multipathd.service` and `multipathd.socket` disabled.
- Longhorn-only maps are flushed by exact mapper name.
- Any genuine multipath map fails closed and directs the operator to configure the [Longhorn device blacklist](https://longhorn.io/kb/troubleshooting-volume-with-multipath/).
- Seed copies only missing or changed files, byte-compares every source file, and permits bounded time for large existing artifact caches.
- K3s does not bundle CSI VolumeSnapshot API resources.
- Cluster bootstrap checksum-pins external-snapshotter `v8.5.0` CRDs, deploys the digest-pinned common snapshot controller with leader election and probes, and then restarts CloudNativePG so daily Longhorn snapshot support is discovered before database migration.
- Restored `borealis-longhorn-local` volumes carry hard node affinity selected by CSI.
- Snapshot validation must select any Engine node with `borealis.io/engine-node=true` and allow Kubernetes to match the recovery Pod to the restored PV.
- Do not pin recovery to the presumed source host.
- A `VolumeBinding` node-affinity mismatch means the Pod pin conflicts with restored-PV topology, not that the snapshot data failed.
- Cluster API replicas expose a separate TLS 1.3 mTLS-only Aegis-key receiver from `aegis_cluster_fanout.go`.
- cert-manager assets and the headless Service live in `Data/Engine/K3s/cluster/aegis-mtls.yaml`.

### Qualification
- Stable K3s must pass `Data/Engine/K3s/cluster/run-probe-conformance.sh` on every Engine node.
- The test requires ten consecutive clean trials because affected K3s can alternate between correct and broken scheduling.
- Each trial pins the workload to the local node, forces a liveness failure, waits for the replacement container, proves replacement startup executes, and proves the startup-budget liveness delay resets so no early replacement liveness runs.
- One failed trial deletes the previous result and blocks cluster mode.
- A successful exact-version record lives at `/etc/rancher/k3s/borealis-probe-conformance.json`, includes ten-trial evidence, and sits inside the node manager's narrow writable configuration path.
- The manager never receives write access to the K3s server-state directory.
- Policy tests also require every Borealis, CloudNativePG, Longhorn CSI, kube-vip, and snapshot-controller liveness delay to exceed the corresponding startup failure budget.
- Full deployment reapplies dependency guards, waits for guarded CloudNativePG instances, and changes the site-worker probe-contract hash so existing bare Pods recycle safely.
- Per-node candidate reconciliation also overwrites copied active-probe settings with the target release's guarded delay before the candidate starts.
- Pair admission runs this gate before deploying application workloads.
- This contains [Kubernetes issue 141155](https://github.com/kubernetes/kubernetes/issues/141155) without a fork, release candidate, fabricated result, or unsafe override.
- Remove the delay shim only after the supported stable K3s release contains the upstream fix and full qualification passes.
- K3s membership-removal qualification must prove Kubernetes Node deletion removes embedded-etcd membership while the fenced host remains disabled.
- Keep the qualification gate until current K3s behavior also excludes regressions tracked by [k3s issue 13623](https://github.com/k3s-io/k3s/issues/13623) and [k3s issue 13498](https://github.com/k3s-io/k3s/issues/13498).
- Snapshot-restore qualification creates a unique one-instance CloudNativePG cluster from a completed Ready VolumeSnapshot, validates the original system ID plus point-in-time Borealis state, proves primary write capability through rolled-back temporary data, confirms production remains healthy, and deletes only the validation cluster/PVC after evidence is retained.
- The current merge/release target requires disposable same-Layer-2 Ubuntu qualification covering `1 -> 3 -> 1`, replacement, partition, leader death, rolling Engine/K3s update failure, CloudNativePG failover, snapshot restore, and continuous API/UI/Agent traffic.
- Odd membership changes beyond three nodes remain future roadmap work for a separate issue and pull request after the three-node work closes.
```
