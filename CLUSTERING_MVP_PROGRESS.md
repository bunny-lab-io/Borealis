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
- Published base `5e9e469d8f451eb92a95fba6561736e68ff1940b`.
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

1. Complete native host partial-effect recovery after independently proven
   configured packages and exact retained repository/files, with fresh executor
   history and both fences. No blind package/service replay.
2. Cover earlier host interruption classes through independently classified
   effects; unknown native state remains fenced, never guessed absent.
3. Finish M3 activation/existing-node interruption qualification and M2 complete
   immutable artifact delivery/import. Preserve all original identity material.
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
- Latest pushed head workflow lookup returned no runs yet. No repeated polling or
  artifact audit; required CI not established by local component evidence.
- Go1.25.12: `/home/nicole/.local/share/borealis-dev-tools/go1.25.12/bin/go`.
  Use GOMAXPROCS=2/-p1 and add user-local borealis-dev-tools/bin for policy tools.
- No runtime cleanup obligation, credential use, deployed-checkout edits or live
  qualification in this continuation. All local validation processes finished.

## Exact next executable action

Implement bounded host continuation after independently proven configured packages and
exact repository/host-file state, retaining fresh-producer history and both
fences. Never infer that old preflight succeeded or replay uncertain package
commands; earlier/unknown native states remain explicit recovery work.

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
