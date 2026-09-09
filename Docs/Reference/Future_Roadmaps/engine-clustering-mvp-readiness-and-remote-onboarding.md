# Engine Clustering MVP Readiness and Remote Onboarding

This review identifies work needed for reliable one-node and three-node Engine clustering, automatic recovery, and WebUI-driven SSH onboarding. It records the accepted review at [`78ddf977990e`](https://github.com/bunny-lab-io/Borealis/commit/78ddf977990e4cf7f69538f4caf67d6996785eed); it does not certify a release or describe SSH onboarding as available today.

Use [#493](https://github.com/bunny-lab-io/Borealis/issues/493) for current progress, decisions and handovers. Its [work register](https://github.com/bunny-lab-io/Borealis/issues/493#issuecomment-5552835571) links each active issue, PR, branch and latest checkpoint. [Closure recommendations](https://github.com/bunny-lab-io/Borealis/issues/493#issuecomment-5552835900) remain separate from executed closures. This Markdown review/design PR is the first deliverable; the umbrella stays open through implementation and final qualification.

## Agreed MVP

- Support one or three Engine nodes, paired expansion/shrink, and replacement of one externally fenced failed node.
- Add nodes through WebUI SSH provisioning, including explicit cleanup confirmation for detached standalone targets.
- Recover service and queued-work ownership within five minutes; place failed nodes in automatic maintenance and return them after five uninterrupted healthy minutes.
- Keep uncertain non-idempotent work visible without blindly duplicating execution.
- Disable new clustered HMR entry while preserving existing isolation exit/recovery.
- Keep dedicated Worker Nodes under [#486](https://github.com/bunny-lab-io/Borealis/issues/486) and preview isolation under [#461](https://github.com/bunny-lab-io/Borealis/issues/461).

## Delivery order and release gate

Start with fencing (H01) and validation coverage (H07), then bounded lease transport and stale task ownership (H03). Complete admission, controlled return, certificate lifecycle and release integrity before relying on automated onboarding. Deliver HMR entry gates, accurate versions and durable event timelines alongside the SSH workflow. Dependency and status changes belong in the live register.

Keep [#480](https://github.com/bunny-lab-io/Borealis/issues/480) and its existing draft [#482](https://github.com/bunny-lab-io/Borealis/pull/482) as final qualification records. Qualify one exact immutable release containing all required changes, including existing HMR restoration. Close #493 only after required fixes merge, agreed UI/onboarding works, qualification passes, current procedures match behavior, and remaining deferrals have named owners and explicit acceptance.

Current operator procedures remain in [Managing Engine Clusters](../../Engine/managing-engine-clusters.md), [Deploying the Engine](../../Engine/deploying-the-engine.md) and [Updating the Engine](../../Engine/updating-the-engine.md). Follow those procedures for available behavior; update them in the implementation PR that changes that behavior.

??? example "Detailed Codex Breakdown"

    ### Review method and boundaries

    This versioned design preserves the 22 accepted assessments, seven hardening plans, onboarding design and qualification scenarios from #493. Source findings refer to the reviewed baseline; earlier test results and live observations retain their original scope. Tracker initialization rechecked current GitHub states but did not rerun live failure injection. Implementation and release readiness require new evidence, not extrapolation from merged PRs.

    Read repository guidance, #493 body, work register, latest checkpoint, subsequent operator comments and linked active issue/PR before resuming. Verify current remote and checkout state, test evidence and runtime operations. Append checkpoints using issue's contract and update index links; explicitly record uncommitted/unpushed work and cleanup. Use child issues and PRs for implementation; reference #493 without auto-closing it. Keep new issues uncreated until their register item is claimed, then create/link the issue, branch and PR together. Existing issue/PR pairs take precedence over replacements.

    ### Per-issue and per-PR assessments

    #### Issue #476 — Add "Engine Version" column to Nodes AG Grid on Cluster Management Table for cluster version. (e.g. 2026.09.1.1)

    - **Original goal:** [Add "Engine Version" column to Nodes AG Grid on Cluster Management Table for cluster version. (e.g. 2026.09.1.1)](https://github.com/bunny-lab-io/Borealis/issues/476). GitHub state at tracker initialization: open.
    - **Evidence and assessment:** Engine Version column remains absent. Snapshot already supplies per-node release tag/SHA. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Cluster_Management.jsx).
    - **Remaining risk:** Recorded release metadata can be mistaken for freshly observed runtime identity.
    - **Plan and disposition:** Keep open. Add version column, SHA tooltip, pending-target indication, and unknown/stale reporting. Distinguish recorded release from freshly verified runtime identity. Work register: [U02](#ui-integration) / [H06](#h06).
    - **Validation and closure criteria:** Add focused WebUI/API tests for exact tag/SHA, pending target, unknown/stale observation and mixed versions; verify on all three nodes. Close #476 only after its version display and accurate identity states ship.

    #### Issue #467 — Create Cluster Event Timeline within "Details" row of AG Grid Table

    - **Original goal:** [Create Cluster Event Timeline within "Details" row of AG Grid Table](https://github.com/bunny-lab-io/Borealis/issues/467). GitHub state at tracker initialization: open.
    - **Evidence and assessment:** Current Details cell provides summary and diagnostics, without requested timeline. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Cluster_Management.jsx).
    - **Remaining risk:** Browser-local countdowns or lost event history hide stalled and resumed operations.
    - **Plan and disposition:** Keep open. Refine around reusable operation timeline supporting admission, removal, automatic recovery, and provisioning. Countdown derives from server timestamps; reconnect restores history. Work register: [U03](#ui-integration).
    - **Validation and closure criteria:** Test server-derived timestamps, cursor pagination, reconnect, interrupted operations and durable timeline history. Close #467 when admission, removal, recovery and provisioning share usable progress display.

    #### Issue #461 — Technical Debt: Remove clustered HMR dev/production divergence

    - **Original goal:** [Technical Debt: Remove clustered HMR dev/production divergence](https://github.com/bunny-lab-io/Borealis/issues/461). GitHub state at tracker initialization: open.
    - **Evidence and assessment:** HMR availability divergence remains intentional architecture. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go).
    - **Remaining risk:** Disabling HMR does not repair production lease transport, and removing exit paths strands already isolated clusters.
    - **Plan and disposition:** Keep open as post-MVP preview-isolation work. Disable new clustered HMR entry across UI/API/CLI for MVP; preserve restoration paths for existing isolation states. Work register: [U01](#ui-integration); post-MVP [#461](https://github.com/bunny-lab-io/Borealis/issues/461).
    - **Validation and closure criteria:** Reject new clustered HMR entry through UI/API/CLI, including enabled one-node clusters; test existing exit after #492. Keep #461 open until separate preview-isolation architecture removes its documented divergence.

    #### Issue #480 — Qualify fresh three-node Engine cluster

    - **Original goal:** [Qualify fresh three-node Engine cluster](https://github.com/bunny-lab-io/Borealis/issues/480). GitHub state at tracker initialization: open.
    - **Evidence and assessment:** Current architecture lacks recorded fresh three-node qualification.
    - **Remaining risk:** Historical split-VIP and retained single-node checks do not establish current three-node recovery.
    - **Plan and disposition:** Keep open as final release gate. Replace stale installer/access prerequisites with current evidence; expand matrix for findings below. Work register: [Q01](#q01).
    - **Validation and closure criteria:** Run full qualification matrix below against one immutable release and exact K3s/image identities. Update stale prerequisite evidence; close #480 only after retained results meet acceptance.

    #### Issue #466 — HMR standby controller must recover from blackholed CloudNativePG primary socket

    - **Original goal:** [HMR standby controller must recover from blackholed CloudNativePG primary socket](https://github.com/bunny-lab-io/Borealis/issues/466). GitHub state at tracker initialization: open.
    - **Evidence and assessment:** Recorded blackholed database socket failure remains consistent with current connector implementation. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_manager.go).
    - **Remaining risk:** Blocked active database socket can outlive lease budget and stale scheduler owner can continue dispatching.
    - **Plan and disposition:** Keep open. Broaden from HMR recovery to production controller and scheduler lease transport. HMR disablement does not repair shared failure mechanism. Work register: [H03](#h03) / [H07](#h07).
    - **Validation and closure criteria:** Use real PostgreSQL plus blackhole proxy, independent lease watchdog, late completion and owner-death tests. Require bounded failover and stale-generation rejection before closure.

    #### Issue #471 — Add "PostgreSQL (Active)" and "PostgreSQL (Replica)" Roles to All Nodes in the Nodes Table in Cluster Management.

    - **Original goal:** [Add "PostgreSQL (Active)" and "PostgreSQL (Replica)" Roles to All Nodes in the Nodes Table in Cluster Management.](https://github.com/bunny-lab-io/Borealis/issues/471). GitHub state at tracker initialization: closed.
    - **Evidence and assessment:** PostgreSQL role visibility exists; later UI uses dedicated Database column. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Cluster_Management.jsx).
    - **Remaining risk:** Aggregate database readiness cannot identify a specific failed replica; presentation must avoid implying certainty.
    - **Plan and disposition:** Keep closed. Preserve current presentation and test primary movement, partial readiness, and unknown state. Work register: [Q01](#q01); [U02](#ui-integration) preserves layout.
    - **Validation and closure criteria:** Check primary movement, partial readiness and unknown state in current Database column; keep historical issue closed unless evidence disproves its delivered scope.

    #### Issue #487 — Add immutable qualification release channel for Engine clusters

    - **Original goal:** [Add immutable qualification release channel for Engine clusters](https://github.com/bunny-lab-io/Borealis/issues/487). GitHub state at tracker initialization: closed.
    - **Evidence and assessment:** Qualification channel, ancestry checks, and schema-finalization tracking landed. Immutability enforcement remains incomplete. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go).
    - **Remaining risk:** Qualification-channel syntax and ancestry checks do not prove publication immutability.
    - **Plan and disposition:** Keep historical implementation closed; create linked release-integrity follow-up. Work register: [H06](#h06).
    - **Validation and closure criteria:** Keep issue closed for delivered channel. New linked follow-up requires mutable-release rejection, SHA-bound manifests, unavailable-catalog behavior and explicit qualification promotion tests.

    #### Issue #465 — Site Worker Data Display & Load-Balancing

    - **Original goal:** [Site Worker Data Display & Load-Balancing](https://github.com/bunny-lab-io/Borealis/issues/465). GitHub state at tracker initialization: closed.
    - **Evidence and assessment:** Closed as duplicate of #486, not as implemented functionality.
    - **Remaining risk:** Duplicate closure can be mistaken for completed worker visibility or load balancing.
    - **Plan and disposition:** Keep closed. Retain worker visibility/balancing acceptance under #486; exclude dedicated Worker architecture from MVP. Work register: Post-MVP [#486](https://github.com/bunny-lab-io/Borealis/issues/486).
    - **Validation and closure criteria:** Retain duplicate relationship and outstanding acceptance under #486. Do not add dedicated Worker architecture to MVP or claim implementation from #465 closure.

    #### Issue #481 — Design release-pinned curl Engine deployment

    - **Original goal:** [Design release-pinned curl Engine deployment](https://github.com/bunny-lab-io/Borealis/issues/481). GitHub state at tracker initialization: closed.
    - **Evidence and assessment:** Later comments record successful immutable fresh installs, including ownership repair validation. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Install-Engine.sh).
    - **Remaining risk:** Older remaining-work wording obscures later successful fresh-install evidence; standalone bootstrap cannot safely be reused unchanged for joiners.
    - **Plan and disposition:** Keep closed. Correct stale “remaining qualification” wording and link final evidence. Work register: [S01](#s01) / [Q01](#q01).
    - **Validation and closure criteria:** Link subsequent #489/#490/#491 evidence; keep closed. Repeat verified source/asset identity and ownership checks in dedicated joining workflow and final fresh-cluster run.

    #### Issue #376 — Tighten Agent reconnect delay after site-worker redeploy

    - **Original goal:** [Tighten Agent reconnect delay after site-worker redeploy](https://github.com/bunny-lab-io/Borealis/issues/376). GitHub state at tracker initialization: closed.
    - **Evidence and assessment:** ACK-gated reconnect, timeout/backoff changes, and operator-observed roughly ten-second recovery support closure.
    - **Remaining risk:** Historical reconnect observation does not prove recovery on current cluster and Agent release.
    - **Plan and disposition:** Keep closed. Include current Agent reconnect behavior in new cluster failure tests. Work register: [Q01](#q01).
    - **Validation and closure criteria:** Record exact Agent version, ACK-gated connection restoration and new authenticated traffic during host loss/VIP movement. Preserve existing reconnect tests and keep historical issue closed.

    #### PR #482 — issue/qualify-fresh-three-node-engine-cluster

    - **Original goal:** [issue/qualify-fresh-three-node-engine-cluster](https://github.com/bunny-lab-io/Borealis/pull/482). GitHub state at tracker initialization: open.
    - **Reviewed PR identity:** [`1db74b46b7c44303e5857a55eda245e6fffbe3ec`](https://github.com/bunny-lab-io/Borealis/commit/1db74b46b7c44303e5857a55eda245e6fffbe3ec); 0 changed files in PR record.
    - **Evidence and assessment:** Draft qualification anchor; currently zero changed files. Green checks do not establish runtime qualification.
    - **Remaining risk:** Zero-file draft and successful portable checks can be mistaken for runtime evidence.
    - **Plan and disposition:** Retain same PR/branch for #480. Refresh from reviewed baseline through normal workflow; record exact release and failure-test evidence. Work register: [Q01](#q01).
    - **Validation and closure criteria:** Reuse existing branch/PR with #480; record immutable release, topology, timestamps, failure injection, data/Agent checks and artifacts. Resolve baseline drift through normal approved workflow; do not fabricate pass records.

    #### PR #492 — Fix production WebUI restoration after HMR

    - **Original goal:** [Fix production WebUI restoration after HMR](https://github.com/bunny-lab-io/Borealis/pull/492). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`b1c8094feb4cd010079473304258855c1602f175`](https://github.com/bunny-lab-io/Borealis/commit/b1c8094feb4cd010079473304258855c1602f175); 4 changed files in PR record.
    - **Evidence and assessment:** Production candidate strips HMR mounts and forces production mode. Focused regression passes.
    - **Remaining risk:** Inspected stable release predates restoration fix; source merge alone does not repair deployed HMR state.
    - **Plan and disposition:** Keep merged. Require existing-HMR restoration test before deploying HMR disablement. Latest inspected stable release predates this fix. Work register: [U01](#ui-integration) / [Q01](#q01).
    - **Validation and closure criteria:** Qualify release containing fix with existing clustered HMR isolation, production restoration, mount cleanup and fresh UI/API access before rejecting new HMR entry.

    #### PR #491 — Validate published Engine release on fresh host

    - **Original goal:** [Validate published Engine release on fresh host](https://github.com/bunny-lab-io/Borealis/pull/491). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`7746609617ad8aed497d80cbc8be27ffa7feb1b7`](https://github.com/bunny-lab-io/Borealis/commit/7746609617ad8aed497d80cbc8be27ffa7feb1b7); 4 changed files in PR record.
    - **Evidence and assessment:** Fresh-install evidence and post-dispatch ownership repair support stated scope.
    - **Remaining risk:** Remote deployment may differ in ownership and host preparation from interactive fresh install.
    - **Plan and disposition:** Keep merged. Carry ownership checks into SSH provisioning tests. Work register: [S01](#s01) / [Q01](#q01).
    - **Validation and closure criteria:** Retain fresh-host evidence; add SSH-authenticated bootstrap and repeat-run ownership/readiness checks. New onboarding child owns differences; merged PR remains historical evidence.

    #### PR #490 — Qualify first immutable Engine release

    - **Original goal:** [Qualify first immutable Engine release](https://github.com/bunny-lab-io/Borealis/pull/490). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`2dda0e45e8643f502429688f17ce6382a9d8f054`](https://github.com/bunny-lab-io/Borealis/commit/2dda0e45e8643f502429688f17ce6382a9d8f054); 4 changed files in PR record.
    - **Evidence and assessment:** Release workflow permission correction landed; later publication/install evidence completes broader qualification.
    - **Remaining risk:** Original permission limitation and stale remaining section can obscure later completed publication evidence.
    - **Plan and disposition:** Keep merged. Link later evidence instead of treating original “Remaining” section as current blocker. Work register: [H06](#h06) / [S01](#s01) / [Q01](#q01).
    - **Validation and closure criteria:** Link later immutable publication and installer records. Verify new release metadata/assets and fresh install before qualification; keep merged workflow correction scoped to its original goal.

    #### PR #489 — Pin Engine curl deployments to immutable releases

    - **Original goal:** [Pin Engine curl deployments to immutable releases](https://github.com/bunny-lab-io/Borealis/pull/489). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`292cd0f86ec9f58b961acf4c36195ad858d4ff44`](https://github.com/bunny-lab-io/Borealis/commit/292cd0f86ec9f58b961acf4c36195ad858d4ff44); 16 changed files in PR record.
    - **Evidence and assessment:** Standalone installer enforces immutable release identity and verified assets. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Install-Engine.sh).
    - **Remaining risk:** Verified standalone install still creates independent welcome/database state unless join path is distinct.
    - **Plan and disposition:** Keep merged. Reuse verification contract for remote provisioning; add separate joining-node path. Work register: [S01](#s01) / [H06](#h06).
    - **Validation and closure criteria:** Reuse full-SHA and asset verification; test tampered/mismatched assets, absent release metadata and joining-node bootstrap with no standalone database/admin/Aegis initialization.

    #### PR #488 — issue/add-cluster-qualification-release-channel

    - **Original goal:** [issue/add-cluster-qualification-release-channel](https://github.com/bunny-lab-io/Borealis/pull/488). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`bbcbdd44934d071a217edc70449d6b4d1d5916cd`](https://github.com/bunny-lab-io/Borealis/commit/bbcbdd44934d071a217edc70449d6b4d1d5916cd); 22 changed files in PR record.
    - **Evidence and assessment:** Qualification workflow largely implemented; catalog omits GitHub `immutable` validation and fetches compatibility manifest through tag. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go).
    - **Remaining risk:** Mutable publication or tag-based compatibility fetch can break identity consistency during rolling operations.
    - **Plan and disposition:** Keep merged; linked follow-up enforces publication immutability and SHA-bound manifest retrieval. Work register: [H06](#h06).
    - **Validation and closure criteria:** New linked follow-up must reject mutable releases, bind manifest to resolved SHA, preserve target identity across retry, and recheck K3s compatibility after upgrades.

    #### PR #483 — issue/pin-curl-engine-deployments-to-releases

    - **Original goal:** [issue/pin-curl-engine-deployments-to-releases](https://github.com/bunny-lab-io/Borealis/pull/483). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`417ae0e68ecc259653ff42241aa790cd057373ff`](https://github.com/bunny-lab-io/Borealis/commit/417ae0e68ecc259653ff42241aa790cd057373ff); 5 changed files in PR record.
    - **Evidence and assessment:** Version-contract prerequisite entered `main` through #488. Installer work continued elsewhere.
    - **Remaining risk:** Merged prerequisite can be mistaken for completion of later installer work.
    - **Plan and disposition:** Keep closed. Record ancestry relationship; no duplicate implementation needed. Work register: [H06](#h06) / [S01](#s01).
    - **Validation and closure criteria:** Use current source ancestry and #488/#489 delivery when evaluating installer contract. No duplicate implementation or reopening needed; validate remaining integrity gaps under H06.

    #### PR #479 — fix/fixing-misc-k3s-cluster-deployment-issues

    - **Original goal:** [fix/fixing-misc-k3s-cluster-deployment-issues](https://github.com/bunny-lab-io/Borealis/pull/479). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`b673a29e86665e2d333f0fa8940ef9070e90b998`](https://github.com/bunny-lab-io/Borealis/commit/b673a29e86665e2d333f0fa8940ef9070e90b998); 32 changed files in PR record.
    - **Evidence and assessment:** Unified-VIP and first-node fixes landed; retained one-node qualification documented.
    - **Remaining risk:** Retained one-node qualification does not cover three-node single-VIP failover and PostgreSQL behavior.
    - **Plan and disposition:** Keep merged within stated scope. Require current three-node proof under #480/#482. Work register: [Q01](#q01).
    - **Validation and closure criteria:** Fresh immutable three-node run must exercise ingress/K3s/WireGuard shared VIP, admission, maintenance, host loss and replacement. Preserve PR closure within its documented scope.

    #### PR #477 — Fix fresh Engine bootstrap readiness

    - **Original goal:** [Fix fresh Engine bootstrap readiness](https://github.com/bunny-lab-io/Borealis/pull/477). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`c0e7327147bee57d379e2890abccaaa4f830bde1`](https://github.com/bunny-lab-io/Borealis/commit/c0e7327147bee57d379e2890abccaaa4f830bde1); 11 changed files in PR record.
    - **Evidence and assessment:** Fresh-bootstrap readiness repairs have supporting live evidence.
    - **Remaining risk:** Remote joining host must not replay standalone bootstrap state creation.
    - **Plan and disposition:** Keep merged. Reuse prerequisite checks; prevent joining hosts from deploying independent standalone database. Work register: [S01](#s01) / [Q01](#q01).
    - **Validation and closure criteria:** Exercise package-lock waits, storage/WireGuard prerequisites and certificate/readiness checks in appropriate first-node or join path; test repeat deployment and cleanup boundaries.

    #### PR #475 — issue/failover-vips-from-maintenance-nodes

    - **Original goal:** [issue/failover-vips-from-maintenance-nodes](https://github.com/bunny-lab-io/Borealis/pull/475). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`8e9772f0fe0c7bf9d7629b184488ab0e7066f1ea`](https://github.com/bunny-lab-io/Borealis/commit/8e9772f0fe0c7bf9d7629b184488ab0e7066f1ea); 11 changed files in PR record.
    - **Evidence and assessment:** Role-eligibility changes landed; later unified-VIP change supersedes original topology.
    - **Remaining risk:** Original role eligibility was qualified under earlier VIP topology.
    - **Plan and disposition:** Keep merged. Requalify maintenance/shutdown handoff against current single VIP. Work register: [H04](#h04) / [Q01](#q01).
    - **Validation and closure criteria:** Exercise maintenance/shutdown handoff, returning-node eligibility and fresh Agent connection through current single VIP. Keep merged changes; retain new qualification evidence under #480/#482.

    #### PR #472 — issue/postgresql-roles-cluster-nodes-table

    - **Original goal:** [issue/postgresql-roles-cluster-nodes-table](https://github.com/bunny-lab-io/Borealis/pull/472). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`d4109830d576d0d1c2b3e332367e62e9a3071fb6`](https://github.com/bunny-lab-io/Borealis/commit/d4109830d576d0d1c2b3e332367e62e9a3071fb6); 8 changed files in PR record.
    - **Evidence and assessment:** Database-role UI and recovery guidance landed. HMR guidance exceeds agreed MVP scope.
    - **Remaining risk:** Historical HMR recovery guidance can be mistaken for permission to enter new clustered HMR after MVP gate.
    - **Plan and disposition:** Keep merged. Update guidance alongside HMR gate and current role presentation. Work register: [U01](#ui-integration) / [Q01](#q01).
    - **Validation and closure criteria:** Update canonical guidance with U01 implementation, preserve recovery of existing isolation, and test current Database-column role changes under failover.

    #### PR #462 — Implement k3s Engine Node Clustering

    - **Original goal:** [Implement k3s Engine Node Clustering](https://github.com/bunny-lab-io/Borealis/pull/462). GitHub state at tracker initialization: merged.
    - **Reviewed PR identity:** [`879de3a2537c5233c9fbef3d52fdc22c8e5d4693`](https://github.com/bunny-lab-io/Borealis/commit/879de3a2537c5233c9fbef3d52fdc22c8e5d4693); 103 changed files in PR record.
    - **Evidence and assessment:** Historical split-VIP qualification supports foundation, but admission, fencing, transport, and certificate lifecycle gaps remain. [Reviewed source](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go).
    - **Remaining risk:** Foundation still contains removal, admission, transport and TLS lifecycle gaps despite extensive historical qualification.
    - **Plan and disposition:** Keep merged as foundation. Create bounded follow-up issues/PRs; avoid claiming historical tests qualify current architecture. Work register: [H01–H07](#required-hardening-plans) / [Q01](#q01).
    - **Validation and closure criteria:** Implement bounded shared hardening children and negative regressions; qualify exact resulting unified-VIP release. Keep foundation merged; historical split-VIP pass is evidence only for historical target.

    No listed open issue currently has sufficient evidence for closure. Closed historical items retain their delivered scope; linked hardening children own newly identified gaps. Recommendations above do not authorize closures.

    ### Required hardening plans

    #### H01 — Prove fencing before membership removal — highest priority. { #h01 }

    Current controller skips removal preparation when target is `NotReady`; retry preflight also treats that condition as fencing evidence. Network partition can produce same observation. See [removal handling](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go#L2015).

    - Persist successful fence acknowledgement tied to operation, target identity, Kubernetes UID, and etcd member identity.
    - Permit retry shortcuts only after matching durable fence evidence.
    - Without evidence, halt safely and expose externally fenced recovery path.
    - Test partition before fence, interruption after acknowledgement, missing Node resource, reused hostname, and stale operation replay.

    #### H02 — Make admission resumable and release abandoned slots. { #h02 }

    Admission endpoint always reads oldest 500 global events before filtering. Approved join can therefore time out on established clusters. Consumed invitations and pending admissions also lack complete restart/cancel/expiry handling. See [event handler](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go#L792).

    - Query admission-scoped events/state directly and bind access to matching invitation/admission.
    - Make accepted join requests idempotent; resume after lost response or process restart.
    - Add explicit cancellation, expiry, and reconciliation states. Release slots only after proving no unaccounted member remains.
    - Require HTTPS; reject redirect-based invitation disclosure.
    - Derive K3s version and network configuration from authoritative cluster state.
    - Test over 500 historical events, expired invitations, duplicate requests, failed second joiner, controller restart, and replacement admission.

    #### H03 — Bound lease transport and fence stale task execution. { #h03 }

    Current controller timeout depends on database call returning. Existing fake-driver tests honor cancellation, unlike recorded blackhole incident. Scheduler also launches work independently of continued leadership and completes work by ID without matching lease generation. See [controller lease](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go#L1243) and [scheduler completion](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_manager.go#L1457).

    - Give lease traffic dedicated, bounded transport with independently closable in-flight sockets.
    - Make lease-expiry watchdog independent of blocked renewal call.
    - Attach holder/generation to task claims, heartbeats, dispatch, and completion; reject stale-owner writes.
    - Bring queued-work takeover within five-minute budget without shortening arbitrary script execution limits.
    - Reconcile dispatched work by durable execution ID. Unknown non-idempotent outcomes become explicit operator-visible state.
    - Test real PostgreSQL traffic through blackhole proxy, owner death, late completion, and partition healing.

    #### H04 — Add automatic maintenance and controlled return. { #h04 }

    Existing steady-state controller records application-label drift; it does not implement requested general failed-node maintenance/re-entry policy.

    - Detect loss using node, application, database, and role health. Begin safe failover immediately; allow five-minute recovery/drain budget.
    - Stop new assignments and persist automatic-maintenance state for failed node. Retain etcd membership.
    - Return automatically after five uninterrupted minutes of required health, synchronized database state, matching release, and successful role checks.
    - Sample health throughout soak; reset timer on failure. Current soak checks only endpoints before and after waiting.
    - Serialize restoration with existing operations. After repeated failed returns, apply bounded cooldown and expose persistent fault instead of looping.
    - Never automatically clear operator-requested maintenance or remove a member merely because it is unreachable.

    #### H05 — Renew Aegis mTLS without restarting every key holder. { #h05 }

    Listener loads certificate once; certificate resource renews automatically. Long-lived processes can continue serving expired certificate. See [TLS loading](https://github.com/bunny-lab-io/Borealis/blob/78ddf977990e4cf7f69538f4caf67d6996785eed/Data/Engine/Containers/api-backend/cmd/api-backend/aegis_cluster_fanout.go#L231). Certificate consumers must reload renewed material. [cert-manager guidance](https://cert-manager.io/docs/usage/certificate/#issuance-behavior-rotation-of-the-private-key).

    - Reload validated certificate/key pairs atomically for new handshakes; retain last valid pair during incomplete updates.
    - Define overlapping trust during CA rotation.
    - Test renewal, expiry, malformed projection, joining API replica, and one surviving unlocked key holder.
    - Preserve memory-only Aegis key. Full cold restart still requires operator unlock.

    #### H06 — Align release integrity and K3s compatibility. { #h06 }

    - Require `immutable=true` for selectable stable and qualification releases.
    - Resolve tag once; fetch manifest by resolved SHA and retain verified identity throughout operation.
    - Use authoritative observed/configured K3s version instead of stale process environment.
    - Reconcile current default `v1.36.3+k3s1` with older qualification evidence for `v1.36.4+k3s1`; qualify exact selected baseline and prevent implicit downgrade.
    - Test mutable publication, moved tag, mismatched manifest, unavailable GitHub, K3s upgrade followed by Engine update, and qualification promotion.

    #### H07 — Repair validation coverage. { #h07 }

    Database runner omits four existing PostgreSQL tests covering maintenance recovery, completion state, retry checkpoint, and action-image pinning. Database CI path selection also misses ordinary cluster Go source changes.

    - Select every required PostgreSQL integration test through maintained inventory; fail on missing tests or required skips.
    - Include cluster/controller/store/scheduler changes in database lane selection.
    - Add behavioral regression tests for findings above, including negative fencing and interrupted admission.
    - Keep #460 probe guards and #463 kube-vip workaround until their independent removal criteria pass.

    ### UI integration acceptance { #ui-integration }

    - **U01 — clustered HMR entry:** reject new entry through WebUI, API and CLI on enabled one-node and three-node clusters; keep standalone development behavior and existing clustered exit/recovery paths. Test restoration using release containing #492. #461 keeps post-MVP isolation work; H03 remains required despite disablement.
    - **U02 — Engine version:** reuse #476. Show per-node version and full-SHA tooltip, observed freshness/unknown state and pending target. Never present recorded desired metadata as newly measured runtime identity.
    - **U03 — operation timeline:** reuse #467. Share durable server events across admission, removal, recovery and SSH onboarding. Use server timestamps for countdowns, restore pagination/history after reconnect, and show actionable stalled/failed state without credential payloads.

    ### S01 — WebUI remote Engine onboarding design { #s01 }

    Use existing cluster requirements: Ubuntu 24.04 or newer, AMD64, matching sizing, static private IPv4 on the same Layer 2 network. Preserve authoritative network mode, FQDN, CA, VIP and peer access. First-node conformance and conversion gates remain required; remote enrollment cannot bypass them. Exact input lengths, field classes, server/client validation and approval boundaries belong in S01 child API/UI design before implementation.

    #### Operator flow

    1. Choose **Add Engine Nodes**. Request two targets for `1 → 3`, or one replacement for recorded `2 active / 3 desired`.
    2. Enter private management IPv4, SSH port, username, and authentication method.
    3. Probe SSH host key before sending authentication credentials. Show SHA-256 fingerprint and host-console verification command:

       ```bash
       sudo ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256
       ```

       Display command for negotiated host-key type when different. Pin approved key; changed key requires fresh verification.
    4. Authenticate and inspect OS, architecture, sizing, storage, network, existing K3s state, and Borealis identity.
    5. Already joined/trusted target exits onboarding with **Start Over** or **Cancel**.
    6. Clean targets receive takeover confirmation. Detached standalone targets receive exact data-removal inventory and typed **PURGE AND JOIN `<IP>`** confirmation. Foreign multi-node members require safe detachment first.
    7. Display temporary Aegis credential-storage notice, then provision and show durable progress timeline.
    8. Finish only after membership, application, database, storage, Aegis, ingress, and Agent-path checks pass continuous health soak. Remove temporary SSH credentials automatically.

    #### Execution architecture

    - Extend existing cluster controller and operation records; avoid second membership controller.
    - Add Admin provisioning API family for host-key discovery, authenticated inspection, confirmation, start, status, retry, and safe cancellation. Reuse operation events.
    - Execute fixed SSH provisioning steps through Aegis-capable API workers under operation/target leases. Preserve restricted node-manager command boundary.
    - Add dedicated `Engine.sh` joining-node workflow. It prepares host and joins existing K3s cluster without creating standalone PostgreSQL, local administrator, separate Aegis cipher, or independent welcome flow.
    - Package verified node-bootstrap bundle containing pinned source and node manager. Support stable releases and explicitly opted-in immutable qualification releases.
    - Pre-stage both expansion targets before first membership change. Persist per-target checkpoints and reconcile actual state after disconnect/restart.
    - Existing CloudNativePG replication carries users and application data. Existing verified mTLS fanout supplies Aegis key.
    - Inherit cluster FQDN, CA trust, VIP, K3s configuration, sizing contract, peer allowlist, and exact Borealis release. Update peer access through documented fixed workflows.
    - Reject duplicate addresses, VIP collisions, changed host identity, unsupported hosts, and inconsistent cluster membership before destructive work.

    #### Secrets and cancellation

    - Store SSH password/key/passphrase/sudo secret encrypted under Aegis, scoped to onboarding operation.
    - Exclude secrets from URLs, command arguments, events, diagnostics, and copied payloads.
    - Delete active credential records after successful soak, cancellation, terminal failure, or expiry; retain only redacted audit metadata.
    - Retryable work may retain encrypted credentials within bounded lifetime; expiry requires resubmission. Controller startup reconciles overdue cleanup.
    - Cancellation before membership change cleans preparation. After membership change, reconcile and use supported removal/fencing workflow before reporting cancellation complete.
    - Existing cluster credentials required for normal operation remain managed separately from temporary SSH credentials.

    #### Removal and recovery

    - WebUI retains paired `3 → 1` removal and externally fenced failed-node replacement.
    - Automatic maintenance never performs destructive removal.
    - Partial expansion cannot claim healthy HA while membership remains transitional. Quorum loss stops mutation; no automatic forced-etcd reset or stale PostgreSQL promotion.
    - Worker-node selectors and execution-tier redesign remain deferred to #486.

    ### Q01 — Validation evidence and qualification matrix { #q01 }

    Historical validation recorded by accepted baseline review (not rerun by this documentation change):

    - `bash Tests/run-engine-go.sh` — passed.
    - `bash Tests/run-k3s-policy.sh` — passed.
    - Focused Python cluster recovery, workload reconciler, and release-bootstrap suites — **58 passed**.
    - `git diff --check` — passed; tracked worktree was clean at review.
    - Repository policy blocked by missing `node`.
    - PostgreSQL integration not run; Docker socket access denied.
    - No live cluster mutation or failure injection performed.

    MVP qualification under #480/#482 must cover:

    - Fresh immutable bootstrap, `1 → 3 → 1`, detached-target purge, existing-member rejection, and replacement.
    - Interrupted provisioning at every checkpoint, expired credentials, cancellation, and controller failover.
    - Single-host power loss and network partition without healing target before service recovery.
    - Five-minute recovery of new authenticated UI/API/Agent connections and safe queued-work ownership.
    - Automatic maintenance/re-entry, continuous soak reset, repeated flapping, and failure during planned maintenance.
    - PostgreSQL acknowledged-write preservation, synchronized promotion, snapshot restore, and extended replica outage.
    - VIP/WireGuard ownership, real Agent reconnect, task deduplication, certificate renewal, rolling Engine/K3s updates, and failed-update retry.
    - Existing HMR restoration followed by rejection of new clustered HMR entry.

    For each scenario, record exact source SHA, immutable release metadata and asset/image digests, K3s and Agent versions, topology, preconditions, failure injection and recovery timestamps, assertions, redacted artifacts, and residual risk. Power-loss/partition acceptance must show service recovery before healing the failed target. Preserve acknowledged PostgreSQL writes; distinguish safe queued-work takeover from unknown already-dispatched work. Never infer a real-database pass from a skipped integration test.

    Tracker/documentation validation results belong in the latest #493 checkpoint and linked PR; they do not substitute for Q01 runtime qualification. Missing local artifacts must be labeled non-durable with exact rerun commands.

    ### Related documentation

    - [Managing Engine Clusters](../../Engine/managing-engine-clusters.md) — current membership, maintenance, failover and recovery procedures.
    - [Deploying the Engine](../../Engine/deploying-the-engine.md) and [Updating the Engine](../../Engine/updating-the-engine.md) — published bootstrap and release workflow.
    - [Database Reference](../Data%20and%20Schema/db-reference.md) — PostgreSQL connection lifecycle before H02/H03/H04 implementation.
    - [Security Whitepaper](../security-whitepaper.md) — Aegis and trust boundaries before H05/S01 implementation.
    - [Validation and Unit Testing](../Unit_Testing.md) — portable test lanes and Tier 3 boundaries.
    - [K3s Migration Roadmap](k3s-migration-roadmap.md) — completed migration and deferred membership beyond three.

    ### Source map

    - Tracker entrypoint: `AGENTS.md`; durable progress: GitHub #493 index and checkpoints.
    - Cluster API and audit store: `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go`, `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster_store.go`.
    - Membership and recovery orchestration: `Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go`.
    - Scheduler ownership: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_manager.go`.
    - Aegis TLS lifecycle: `Data/Engine/Containers/api-backend/cmd/api-backend/aegis_cluster_fanout.go`.
    - Fixed host actions: `Data/Engine/Containers/api-backend/cmd/borealis-node-manager/`.
    - Bootstrap and release contract: `Engine.sh`, `Install-Engine.sh`, `Data/Engine/release-manifest.json`.
    - Cluster WebUI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Cluster_Management.jsx`.
    - Database runner and CI selection: `Tests/run-database-postgres.sh`, `Tests/manifests/ci-paths.json`.
