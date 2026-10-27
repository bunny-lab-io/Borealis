# Clustering MVP execution progress

Authoritative tracker: [#530](https://github.com/bunny-lab-io/Borealis/issues/530).
Operator explicitly requested this repository continuation file on2026-10-26.
Canonical procedures remain in [Managing Engine Clusters](Docs/Engine/managing-engine-clusters.md).

## Objective and readiness

Continue until clustering MVP is implemented, integrated, verified and ready for
direct operator testing. Continuous execution supersedes ten-slice/one-slice
stopping limits. Readiness requires executable primary workflows and intended-host
setup, passing critical functional/integration/smoke tests, resolved blocking
defects, and documented configuration/test data/operator steps. Component tests
alone do not establish readiness. Preserve one/three-Engine topology, calculated
provisioning, original-controller authority and all safety gates in #530.

## Published state and evidence

- Branch `issue/cluster-central-ssh-onboarding`, draft [PR#522](https://github.com/bunny-lab-io/Borealis/pull/522).
- Published base `4f9839473b84f02e7a59211348095f0df2b00f7b`; subsequent publication SHA stays in #530 active handoff.
- Fixed host custody/transport, source/account/directory/keys/host prefix and
  journal-based resume are integrated. Resume independently reconciles retained
  stage before later writes. Source absence recovery requires explicit proof,
  retained history and newer claim before renewed absence proof/no-replace retry.
- Published source-absence focused race suite passed three repetitions across
  bootstrap/remote/API/native; final source-only repeats passed after historical
  producer checks. Full node-manager race suite132tests, zero failures/skips;
  full-module vet/build and repository policy184tests/219routes passed.
- Prior full bootstrap race suite132tests passed at
  `d6710ce524c8431aa9c62c3724ef5900f3c06e79`; evidence applies only to unchanged code.
- All M1-M8 acceptance items remain open. No readiness percentage or live acceptance
  claim. Production preparation polling remains disabled pending delivery/recovery.

## Account continuation checkpoint

- Fixed group-only recovery independently proves existing reserved group and
  absent user, preserving source/configuration/NSS/UID-consumer custody.
- Before useradd, journal retains old producer/partial proof as continued history
  and persists new executor under a strictly newer generation. Version3 is used
  only for this transition; ordinary version2 journals remain readable. Older
  helpers reject continuation records. No database migration or existing account
  edit, password assignment, consumer startup or generic write replay.
- Fixed supervised SSH action and dispatcher perform continuation followed by
  independent complete-account reconciliation, including lost acknowledgement.
- Focused race suite passed three repetitions across bootstrap/native/remote/API
  (99.211s/1.078s/2.310s/2.199s). Full-module vet/build, repository policy and
  docs-reference checks passed. Full bootstrap/native race suite passed138/133top-level tests with zero
  failures/skips (347.861s/11.561s).
- Evidence: ignored `Unit_Test_Results/m3-account-continuation-20261027/`.
  Earlier evidence lives in adjacent `m3-source-absence`, `m3-preparation-resume`
  and `m3-host-custody` directories dated20261027. No live action/cleanup obligation.

## Ordered remaining work

1. Complete M2 runtime admission/import: seven-stage artifact/inert-service
   installation is published. Private join material and concrete source acquisition
   are implemented; wire worker broker into current preparation scope, then fixed
   target join configuration, journaled admission and actual import.
2. Finish M3 activation/existing-node interruption qualification. Preserve all
   original identity material and keep consumers fenced until complete proof.
3. Qualify bounded host recovery on Ubuntu. Unknown/unconfigured dpkg state stays
   fenced; no blind package/service replay or guessed absence.
4. Finish M1 supported-profile budgets, worker bounds, measured requirements and
   phase-correct readiness using existing calculated sizing.
5. Exercise M4 central WebUI paired expansion through original controller with
   actual infrastructure/application/Agent health soak.
6. Complete M5 interruption/cancellation/replacement/paired shrink/detached-target
   takeover, M6 maintenance/return recovery and M7 durable operator status.
7. Finish setup/testing instructions and exact immutable-candidate qualification.
   Merge/release authority remains separate; acceptance requires actual evidence.

## Constraints and outstanding evidence

- Workstation is source/control host, never Engine member or qualification node.
- Exact remote target, documented procedure, applicable authorization and advance
  notice required before runtime changes. Preserve Engine01 and private rollback
  journals. Fresh lab credentials remain only in conversation, never repository.
- No deployment, merge, release, credential reuse or live mutation from ordinary
  implementation authorization. No security-control relaxation. Native design
  retained; Ansible clustering discarded.
- Full unchanged clusterremote Netplan fixtures need Ubuntu bindings/generator;
  CachyOS workstation lacks them. Changed pinned transport tests pass. Required
  Ubuntu CI remains necessary; do not skip assertions or substitute old results.
- Strict docs needs Python3.12 per #538; Python3.14/Docker-access constraint remains.
- Published early-host recovery head1ea826d167037af09152fef3f48b7d9bfae56150:
  Container Policy40468656513 passed; PR Validation40468656463 still running at
  single check. These results do not validate current artifact-stream changes.
  No repeated pending polling or artifact downloads.
- Go1.25.12: `/home/nicole/.local/share/borealis-dev-tools/go1.25.12/bin/go`.
  Use GOMAXPROCS=2/-p1 and add user-local borealis-dev-tools/bin for policy tools.
- No runtime cleanup obligation, credential use, deployed-checkout edits or live
  qualification in this continuation. All local validation processes finished.

## Exact next executable action

Publish validated concrete source acquisition, then add worker-facing encrypted
broker using existing SQL/Aegis preparation authority and source delivery registry.
Register fixed callback beside broker on original controller; extend only this
request's bounded deadline to cover immutable source Job, preserving short existing
broker limits. Worker must recheck current claim/source before and after acquisition
and own/clear material through preparation scope. Then integrate fixed target join
configuration and journaled membership/import. Production polling stays disabled.
No live runtime action taken.

## Private source acquisition checkpoint

- Canonical public source request binds current claim, confirmed original source,
  source network and bounded expiry. Native private Unix endpoint brackets fixed
  root-file custody with actual source observations; no generic action exports it.
- Fixed non-root source Job client reads bounded complete frame and encrypts
  callback under separate existing-operator-secret domain. Only ciphertext digest,
  request digest and actual Job/Pod IDs appear in termination message. Existing
  socket/token mounts and SecretKeyRef suffice; no root mount, RBAC or credential
  rotation. Private bytes never enter argv, logs or Kubernetes status.
- Original-controller acquisition verifies full actual immutable Job/Pod, source
  placement, current authority and matching ciphertext receipt. Callback alone is
  insufficient. Two pending reads maximum, no Job POST replay, no overwritten
  delivery, single-use material consumption, private-buffer cleanup on every exit.
- Focused protocol/native/API race suite passed three repetitions in
  1.056s/1.453s/3.474s. Full native race166 top-level tests passed14.094s, zero
  failures/skips. Full-module vet, CGO-disabled node-manager build and docs-reference
  checks passed. Repository policy184tests/219routes passed. Publication recorded in #530.
  Evidence: `Unit_Test_Results/m2-k3s-source-acquisition-20261027/`.
- Base4f983 Container Policy40489019847 passed; PR Validation40489020019 pending
  at one check. No manual reruns, repeated pending polls or artifact downloads.
- Worker broker/preparation scope remains unwired. No live acquisition, runtime
  qualification or readiness claim; all M1-M8 remain open.

## Interrupted-firewall continuation checkpoint

- Concrete M3 safety prerequisite: old renderer flushed a referenced chain before
  refilling it. Canonical renderers now use one bounded iptables-restore --noflush
  commit with the same allowlist, rejecting stream metacharacters before writes.
- Disposable user/network namespace on bunny-box passed actual nft kernel checks:
  exact canonical rules, repeat idempotence, killed uncommitted restore, malformed
  transaction preserving prior rules, all eight INPUT construction prefixes,
  foreign-rule preservation and input-injection rejection. No host/Engine firewall
  changed; namespace exited and leaves no live runtime obligation.
- New fixed complete_prejoin_firewall path is being integrated. It requires all
  non-firewall effects complete, canonical stopped unit, exact owned chain/input
  construction prefix, source/key custody, both fences and newer generation.
  Distinct domain-separated continuation proof retains every producer; existing
  configured-host history stays valid. Separate complete-host proof is required.
- Evidence: ignored `Unit_Test_Results/m3-firewall-continuation-20261027/`.
  Focused race suite3 repetitions passed bootstrap98.620s/native1.315s/
  API2.482s/remote2.493s. Module vet/build, repository policy184tests/219routes
  and K3s policy passed. Full bootstrap/native race suite passed146/150top-level tests,
  zero failures/skips (433.024s/12.984s). All validation processes finished.
- Actual Ubuntu1.8.10/systemd and end-to-end qualification remain outstanding;
  local kernel used iptables1.8.13 nf_tables. Legacy fixture failed before any
  rule creation because ip_tables is unavailable and namespace cannot load host
  modules; no host module/control relaxation attempted. Fixture lock was private.
  No Engine action or credential use.

## Configured-host continuation checkpoint

- New fixed host continuation requires v2-fresh intent, newer claim, stopped
  prior executors, both fences and repeated source/key/native partial proof.
- Version4 retains host producer/proof history before effects and preserves
  account history. No partial state is labeled absent. Exact existing files keep
  their inodes; complete pending files promote without overwrite.
- Native class requires configured inventory, exact repository/files, running
  empty Docker, known services/kernel and fresh firewall. No APT/Docker replay.
  Final independent complete-host reconciliation remains mandatory.
- Focused race suite3 repetitions passed bootstrap97.005s/native5.189s/API2.432s/
  remote2.425s. Full-module vet/build, policy184tests/219routes and docs-reference
  checks passed. Full bootstrap/native race suite passed144/142top-level tests,
  zero failures/skips (401.988s/12.883s). Retained-scratch collision/authority/
  parent-replacement race tests passed three repetitions (5.092s).
- Dispatcher ordering tests initially exposed newly added recovery/proof sessions;
  exact connection/order expectations now cover both account and host recovery.
  No failure/cleanup assertion was suppressed.
- Evidence: ignored `Unit_Test_Results/m3-host-continuation-20261027/`.
- No live action, credential use, runtime cleanup obligation or dependency change.

## Fresh-host intent checkpoint

- Read-only fresh proof now precedes v2-fresh host intent under both fences.
  Existing in-action preflight remains. Source/key/authority drift or cancellation
  creates no host intent. Legacy v1 evidence is retained but cannot attest fresh
  preconditions or be silently adopted.
- Focused fresh/legacy/native race cases passed three repetitions (74.980s/1.059s).
  Full node-manager race suite passed134top-level tests, zero failures/skips
  (11.620s). Full-module vet, CGO-disabled build, repository policy and
  documentation-reference/diff checks passed. All existing host tests passed
  (bootstrap138.660s/native1.104s); one justified repeat recovered lost exit status.
- Evidence: ignored `Unit_Test_Results/m3-host-fresh-intent-20261027/`.

## Early host continuation checkpoint

- Fixed fresh and repository continuation actions require retained v2-fresh intent,
  current configured inventory, absent Docker/runtime consumers, source/key custody,
  both fences, strictly newer generation and stopped prior producers. Version4
  preserves every class-specific producer proof. No partial effect is called absent.
- Repository recovery accepts only exact ordered published/full-scratch key/source
  bytes. It preserves inodes, promotes without overwrite and completes missing
  suffix without download. Fixed Docker phase skips base installation and retains
  pre-Docker fresh-filter proof. Partial/foreign scratch and uncertain dpkg reject.
- Focused race suite3 passed bootstrap185.702s/native4.162s/API2.591s/remote2.613s.
  Full bootstrap/native race suites passed149/155top-level tests, zero failures or
  skips (486.856s/13.681s). Full-module vet plus final native vet, CGO-disabled
  build, repository policy184tests/219routes, docs references/gofmt/diff passed.
- Initial policy attempt lacked Go on PATH; rerun with documented Go/bin tools
  passed. Initial dispatcher fixture failed on added fallback sessions; corrected
  exact ordering/session assertions and reran. No assertions/tests suppressed.
- Evidence: `Unit_Test_Results/m3-early-host-recovery-20261027/`. Recovery validation
  processes finished. No live Engine action, credential use or runtime cleanup.

## Immutable artifact stream checkpoint

- K3s acquisition now reserves binary/installer/archive before first fetch, verifies
  pinned bytes and independently measures archive content. API adapter refreshes
  immutable release assets around guarded acquisition. No executable path escapes.
- Framed receivers cover K3s, all23 static external images and dynamic PostgreSQL.
  Each binds independently expected source/version/digest, reserves owned scratch,
  hashes/measures actual contents and leaves next protocol frame untouched.
  PostgreSQL rejects malformed metadata before archive IO; no sender measurement
  can substitute for actual manifest/platform/layer proof.
- Focused combined race suite3 passed bootstrap8.789s/API7.947s. Earlier K3s suite3
  passed1.324s/1.273s. Public compiled-pin paths reject synthetic fixtures. These
  prove component behavior, not real immutable release delivery or live readiness.
- Full-module vet, CGO-disabled node-manager build, repository policy184tests/
  219routes, docs references/gofmt/diff checks passed. Relevant parser/staging/
  transfer/acquisition regression ran in focused suite; unchanged full bootstrap/
  native evidence remains at recovery checkpoint above. New full CI required.
- Evidence: `Unit_Test_Results/m2-k3s-staging-20261027/`; validation processes
  finished. No live Engine action, credential use, runtime cleanup, dependency
  or public route change.
- Separate next-slice work already started: uncommitted `artifact_set.go` owns
  all four families and checks complete matching source bindings. Not yet tested
  or transported; finish aggregate frame/receiver and focused ownership/cleanup
  tests, then native journaled publication/import. No partial set grants readiness.

## In-flight complete artifact composition

- Aggregate ownership/frame/receiver and full source/configuration/identity/artifact
  composition implemented. Four families authenticate before mutation; exact EOF,
  independently selected PostgreSQL digest and reviewed K3s pins remain mandatory.
- Fixed37-file publication covers34 archives, pinned K3s binary, private installer
  and application manifest. Exact source extras, safe parent/inode ownership,
  no-replace writes, physical allocation and retained interrupted scratch tested.
- Journaled publication requires exact five prerequisite receipts, retains both
  exclusions and persists `stage_images` before effects. Independent reconciliation
  accepts only complete actual files under stopped-producer proof; no retry of
  partial effects. Earlier phases reject six-step journals and generated extras.
- Final focused publication/journal race passed69.640s after moving full source
  content proof to file creation/promotion/publication boundaries. Per-chunk
  authority/custody/parent/cancellation remain checked. Source-drift-before-promotion
  regression passed1.010s; aggregate/full-stream/old-consumer suite22.827s; native
  bounded custody1.089s. First broad run interrupted for this necessary throughput
  correction (no failure claimed); final bootstrap race regression passed568.563s (see JSON summary for count). Full native race156top-level tests
  passed15.916s with zero failures/skips. Policy184tests/219routes, final full-module
  vet, CGO-disabled node-manager build and docs-reference/diff checks passed.
  Evidence: `Unit_Test_Results/m2-complete-artifacts-20261027/`.
- Published base595dae CI both passed: Engine Container Policy40470272295 and
  Pull Request Validation40470272345. This is unchanged dependency-stream evidence,
  not validation of current uncommitted composition/publication.
- Exact next action: finish combined private transfer tests and affected package
  regression; publish coherent complete-artifact primitives, then wire fixed
  native session/SSH/dispatcher `stage_images` and read-only reconciliation.
  Bounded native inert-artifact custody is implemented/tested; new action still needs independent PostgreSQL source binding,
  six-step progress cursor and bounded large-stream transport. Existing12KiB
  acknowledged chunks/output cap must be sized for complete dependency payloads.
- Current complete-artifact batch ready for publication after focused validation. No live Engine action, credential
  use, K3s startup/import, public route/schema/dependency change or runtime cleanup.

## In-flight artifact session integration

- Published complete-artifact checkpoint22b7bcc has full bootstrap163/native156
  top-level race tests, zero failures/skips,568.563s/15.916s; focused journal/stream,
  full-module vet/build/policy evidence above. No live action.
- Fixed publish/reconcile artifact session binds independently selected PostgreSQL
  digest and original identity. Ordinary action limits stay unchanged. Artifact
  class uses256KiB chunks/512KiB frames, bounded complete-cohort bytes,128MiB
  acknowledgements,30-minute session/31-minute systemd maximum/32-minute shell
  maximum. Existing20-second authority lease and2-second watchdog stay unchanged;
  ordinary/artifact executors reject each other's runtime ceiling/request class.
- Native session receives bulk scratch under qualified /opt before journaled
  publication/reconciliation. Verified helper upload retains existing private
  /var/tmp wrapper. Pinned SSH uses fixed artifact verb and aggregate input.
- API acquisition now stages K3s and owns all four families under original
  source/claim scopes. Worker budget includes all three K3s assets and their owned
  directory. Target budgets retain archive generations and add final installer,
  bounded manifest and owned set directories. Existing exact low-space/source-
  drift assertions updated for added bytes/entries, never suppressed.
- Six-stage dispatcher writes then independently proves artifacts, including lost
  acknowledgements. Retained stage_images runs proof only; old prefix rejects that
  cursor. Partial/unknown publication, authority or cleanup failure stops progress.
  Extended90-minute acquisition/70-minute dispatch ceilings do not extend any
  credential expiry, claim/controller authority or short renewal deadline.
- Focused3 bootstrap28.253s/native1.492s/API4.182s passed. Remote shell-quoting
  fixture corrected to compare encoded literal and both timeout branches; final
  pinned SSH/command suite3 passed2.681s. Full native race passed14.306s; final
  full-module vet/build and repository policy184tests/219routes passed before last
  small capacity changes. Docs reference/diff passed. No new full bootstrap rerun:
  prior full suite applies only to unchanged artifact primitives, new protocol
  changes have focused coverage and require current batch CI.
- Final target-capacity suite3 passed20.487s,18top-level executions/zero skips
  or failures; external capacity suite3 passed2.376s. Final API vet passed. Evidence:
  Unit_Test_Results/m2-artifact-session-20261027/. Validation processes finished.
  Session/API/capacity batch based on22b7bcc ready for publication. Parent CI:
  Container Policy40477223891 passed; PR Validation40477223875 pending at one check.
  New batch requires its own CI; no pending poll or manual rerun.
- Exact next action: publish fixed artifact session/dispatcher batch, then inspect
  pinned installer contract and existing native join path (main.go around1985),
  implement journaled immutable K3s installation/import using original-controller
  authority and existing shared-identity fences. Legacy manual join downloads an
  installer and starts K3s directly; do not call it from immutable preparation.
  Preserve prepared source, calculated budgets and separate reconciliation.
  Production preparation polling disabled. No Engine changes/credentials used,
  no dependency/schema/public route changes, no runtime cleanup obligation.

## Inert K3s installation checkpoint

- `install_prejoin_k3s` / `reconcile_prejoin_k3s` append seventh journal stage
  `install_k3s`, reusing complete authenticated artifact input. Exact six prior
  receipts and actual37-file/source/identity/host proof precede fresh intent.
- Fixed native publication stages empty0600 environment then0644 service with no
  replacement. Pinned installer is not executed: its unconditional disable/removal
  violates fresh publication. Native adapter only daemon-reloads under both fences;
  it never enables/starts/imports/joins or releases identity consumers.
- Separate executor proves actual loaded, disabled, inactive service, fixed fragment,
  no drop-ins/jobs/PIDs, exact files and complete prerequisites. Partial state stays
  fenced. Older actions require actual unit/scratch absence even with stale systemd
  cache. Seven-stage dispatcher handles lost acknowledgement and retained cursor.
- Calculated demand adds exact unit bytes/two inodes on `/etc/systemd/system`;
  acquisition/dispatch ceilings150/130minutes preserve credential/lease expiry.
- Focused initial journal/native race passed97.297s/1.128s. Final protocol/SSH/native/
  dispatch focused3 passed12.830s/2.666s/1.369s/2.569s; added request-boundary3 passed
  1.107s. Final capacity/scope3 passed20.217s:15 non-PostgreSQL top-level executions.
  Regex also selected unchanged PostgreSQL scope suite:24 subcases skipped because
  no DSN; this is not PostgreSQL validation. Required CI remains responsible for it.
  Initial capacity fixture omitted new `/etc` path; fixed evidence path and exact
  aggregate bytes/inodes, preserving low-space/drift assertions.
- Full bootstrap race167 top-level tests passed663.565s, zero failures/skips.
  Final native race161 passed13.839s, zero failures/skips after stricter file absence.
  Full-module vet, CGO-disabled node-manager build, policy184tests/219routes and
  documentation reference checks passed. No dependency/public route/schema change.
- Evidence: `Unit_Test_Results/m2-k3s-runtime-20261027/`. Immutable upstream installer
  v1.36.3+k3s1 hash verified46177d4c99440b4c0311b67233823a8e8a2fc09693f6c89af1a7161e152fbfad;
  never executed. Actual Ubuntu systemd and end-to-end import remain unverified.
- Base6f567 Container Policy40481136317 passed; PR Validation40481136369 pending at
  one check. No manual reruns/repeated polling. No new PR comments since07:58:10Z.
- No live action, credential use or runtime cleanup obligation. M1-M8 remain open.

## Private K3s join-input checkpoint

- Secure server token/CA material owns buffers, validates fixed K10 server syntax
  and exact CA hash, binds current target/source, redacts formatting and refuses
  generic JSON. Stable effect digest survives worker takeover; export/frame still
  requires exact current generation. Close clears token/CA and revokes capability.
- Bounded private frame checks claim metadata before reading token/CA payload,
  verifies trailer plus independent CA/token semantics and consumes exactly one
  frame. Full source/identity/artifact/join composition requires EOF before native
  consumer. Old artifact-only consumer rejects join suffix. All owned input closes.
- Native read-only source scope permits only root-owned0600 server/token and0644
  server/tls/server-ca.crt, with bounded reads, no symlinks/hardlinks, exact ancestor/
  inode/mtime/owner/mode/content proof and independently supplied source authority.
  Retained refresh/material cannot survive scope closure. No generic action or
  live source/controller acquisition caller has been added yet.
- Focused material/source3 passed1.236s/1.105s; complete composition plus old/new
  receivers3 passed34.457s/bootstrap and1.117s/native. Final material/frame3 after
  stable-effect binding passed1.227s. Full native race163 top-level passed13.822s,
  zero failures/skips. Full-module vet, CGO-disabled node-manager build, repository
  policy184tests/219routes, docs-reference/gofmt/diff checks passed.
- Reuse fd447 full bootstrap167/663.565s for unchanged journal/publication core;
  changed composition has focused old/new EOF/cleanup validation above. No broad
  unchanged suite rerun. Evidence `Unit_Test_Results/m2-k3s-join-20261027/`.
- Source private acquisition is required for M2 join: API worker cannot safely read
  source root token. Smallest remedy is existing source Job/native socket plus
  encrypted callback and independently observed public ciphertext-digest receipt.
  No new Kubernetes privileges, plaintext Job logs or persistent credential store.
- No live credential use, runtime action, consumer startup or cleanup obligation.
  Actual source acquisition/admission/import and Ubuntu qualification remain open.

- fd447 Container Policy40486153751 passed; PR Validation40486153793 pending at
  one check. No manual reruns or repeated pending polls.
