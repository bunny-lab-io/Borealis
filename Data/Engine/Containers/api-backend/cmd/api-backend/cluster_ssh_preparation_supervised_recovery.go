package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
)

// Fresh-target supervision permits a safe halt, not automatic effect replay.
// The native v4 proposal proves current systemd/cgroup prerequisites and absence
// of prior journals/producers. The immutable helper checks its actual executor
// before creating a journal, persists intent before effects, and requires a
// distinct stopped producer for uncertainty reconciliation. The original worker
// retains uncertain claims and settles only a complete joined cohort.
//
// Production must supply its actual v4 proposal reader and the live gate owned
// by withPreparationArtifacts. Neither historical qualification nor a receipt
// loaded from SQL can construct these capabilities. Final byte reproof and
// cleanup remain owned by that synchronous artifact scope.
func newClusterSSHPreparationSupervisedRecoveryRead(snapshot clusterSSHPreparationApprovalSnapshot,
	artifacts clusterSSHPreparationArtifactGate, cleanTargets func(context.Context, string) (string, error)) func(context.Context, string) (string, error) {
	wire, wireErr := json.Marshal(snapshot.work)
	var work clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &work)
	plan, planErr := clusterSSHPreparationPlanDigest(work.Authorization)
	valid := wireErr == nil && decodeErr == nil && planErr == nil && clusterremote.PreparationProposalVersion == 4 &&
		len(work.Claims) == 0 && work.Baseline.Validate() == nil && validateClusterSSHInspectionCohort(work.Proof.Cohort, work.Proof.Source) == nil &&
		work.Authorization.OperationID == work.Proof.Cohort.OperationID && work.Authorization.Attempt == work.Proof.Cohort.Attempt &&
		work.Authorization.ControllerHolder == work.Proof.Cohort.ControllerHolder && artifacts != nil && cleanTargets != nil
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	var retained [2]string
	return func(parent context.Context, requested string) (string, error) {
		fail := func() (string, error) {
			failed.Store(true)
			return "", clusterSSHPreparationGateBlocked{"supervised_recovery", "Fresh proposal-v4 supervision or immutable helper proof is unavailable or changed; preserve retained journals and inspect stopped-producer evidence before retrying preparation."}
		}
		if !valid || failed.Load() || parent.Err() != nil || requested != plan {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail()
		}
		if failed.Load() || ctx.Err() != nil {
			return fail()
		}
		artifact, err := artifacts(ctx, plan)
		if err != nil || !validClusterSSHPreparationDigest(artifact) || ctx.Err() != nil {
			return fail()
		}
		targets, err := cleanTargets(ctx, plan)
		if err != nil || !validClusterSSHPreparationDigest(targets) || ctx.Err() != nil || failed.Load() {
			return fail()
		}
		observed := [2]string{artifact, targets}
		if retained != ([2]string{}) && observed != retained {
			return fail()
		}
		digest, err := clusterSSHPreparationDigest(struct {
			Domain, Plan  string
			NativeVersion int
			Proofs        [2]string
		}{"preparation-supervised-safe-halt-v1", plan, clusterremote.PreparationProposalVersion, observed})
		if err != nil || ctx.Err() != nil || failed.Load() {
			return fail()
		}
		retained = observed
		return digest, nil
	}
}
