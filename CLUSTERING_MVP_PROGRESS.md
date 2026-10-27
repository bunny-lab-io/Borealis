# Clustering MVP execution progress

Authoritative project tracker: [#530](https://github.com/bunny-lab-io/Borealis/issues/530).
This repository continuation file was explicitly requested by the operator on
2026-10-26. It complements that tracker; canonical procedures remain in
[Managing Engine Clusters](Docs/Engine/managing-engine-clusters.md).

## Objective and readiness

Continue until the clustering MVP is ready for direct operator testing. The
2026-10-26 autonomous execution directive supersedes the ten-slice limit and
one-slice stopping rule. Continue through implementation, debugging, validation
and integration checkpoints without routine continuation approval.

Readiness requires integrated essential workflows, executable intended-host
setup, passing critical functional/integration/smoke tests, resolved blocking
defects, and documented configuration/test data/operator steps. Unit-test success
or completed components alone cannot establish readiness. Preserve #530's
one/three-Engine topology, calculated provisioning, authority and safety gates.

## Current verified state

- Branch/PR: `issue/cluster-central-ssh-onboarding`, [#522](https://github.com/bunny-lab-io/Borealis/pull/522).
- Published base: `81465ef2704005eda513ef223370a9c9052909f5`.
- Ordered native host adapter and independent repository/host proof implemented;
  fixed supervised host transport and five-stage preparation prefix now wired;
  production polling remains disabled pending complete delivery and recovery.
- Base verification: focused race suite three repeats; full node-manager race
  suite 128 tests, zero failures/skips; full-module vet and node-manager build;
  repository policy 184 tests/219 routes. Exact commands/evidence in #530.
- All M1-M8 acceptance items remain open. Existing component evidence is retained;
  no new readiness percentage or acceptance claim.

## Ordered remaining work

1. Add independently verified preparation progress/resume contract so restarting
   dispatch does not replay completed source/account/identity/host stages. Keep
   earlier phase-specific proofs strict.
2. Complete safe partial-effect forward recovery and remaining delivery dispatch;
   qualify interruption and existing-node identity recovery.
3. Finish M1 supported-profile budgets, worker bounds, measured requirements,
   phase-correct readiness integration and validation. Reuse existing sizing.
4. Finish M2 scoped artifact transfer/import and exact release/digest proof.
5. Execute M4 central WebUI paired expansion through original controller, with
   fresh target preparation and infrastructure/application/Agent health soak.
6. Complete M5 replacement, interruption/cancellation, paired shrink and confirmed
   detached-target takeover; then M6 maintenance/recovery and M7 durable status.
7. Resolve blocking defects, finish operator setup/testing instructions and
   immutable-candidate qualification. Required merge/release approvals remain
   separate gates; do not claim readiness without actual applicable evidence.

## Risks and boundaries

- Workstation is source/control host, never an Engine or qualification node.
- No live mutation, credential reuse, deployment, merge or release solely from
  implementation authorization. Apply exact-target notice, documented procedure
  and recorded authorization before runtime work; preserve rollback journals.
- Unknown outcomes retain fences. Never replay uncertain writes or non-idempotent
  work, infer absence from failed observation, or release markers from component
  success. Native design retained; Ansible clustering idea discarded.
- Local strict docs needs Python3.12 (#538); workstation Python3.14 and recorded
  Docker/sudo access limits currently block that fixture. Current-head CI may
  supply docs evidence. No security-control relaxation or stored credential use.
- Actual Ubuntu/systemd execution/interruption and full end-to-end readiness are
  unverified. No runtime cleanup obligation at the published base.

## Exact next action

Publish host custody/transport checkpoint, then implement explicit verified
progress and resumed preparation without replaying earlier completed prefixes.
Current dispatcher always starts source; strict journal phase proofs correctly
reject an already-advanced target. Add bounded progress proof, not fallthrough
from failed observation. Start with `internal/clusterbootstrap/journal_linux.go`,
`preparation_session.go`, `cmd/borealis-node-manager/bootstrap_source.go` and
`cmd/api-backend/cluster_ssh_prejoin_dispatch.go`.

## In-flight work

- Current checkout reconciled with PR/upstream; no preexisting unpublished work.
- Host-phase custody, fixed receiver journal actions, SSH sender methods and
  source/account/identity/host dispatcher sequence implemented locally.
- Focused race tests across node-manager/bootstrap/remote/API passed three runs:
  custody, request binding, journal host phase, pinned transport and dispatcher.
- Full node-manager race suite passed; full-module vet, CGO-disabled binary build,
  repository policy and docs-reference policy passed. Evidence retained under
  ignored `Unit_Test_Results/m3-host-custody-20261027/`.
- Full clusterremote race suite failed on missing Ubuntu Netplan bindings/native
  generator in CachyOS environment. Assertions unchanged; required Ubuntu CI
  lane remains pending. Strict documentation build likewise needs Python3.12.
- Host custody/transport checkpoint prepared for publication; no live evidence.
- Fresh lab SSH credentials supplied in conversation, intentionally absent from
  this file/repository. They do not remove exact-target runtime safety gates.
- No live runtime action or cleanup obligation.
