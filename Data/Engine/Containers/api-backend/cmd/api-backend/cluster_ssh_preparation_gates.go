package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"time"
)

var clusterSSHPreparationGateCodes = [6]string{"immutable_artifacts", "clean_targets", "complete_capacity", "persistent_storage", "inherited_configuration", "supervised_recovery"}

// Native adapters must produce fresh complete-cohort domain proof for the exact
// proposed plan. A digest is an equality receipt, never permission by itself.
// Missing or incomplete native adapters fail closed with their own gate code.
type clusterSSHPreparationGates [6]func(context.Context, string) (string, error)

// Fixed, nonsecret native diagnostics may cross the controller progress boundary.
// Never include an underlying transport, credential or private settings error.
type clusterSSHPreparationGateBlocked struct {
	Gate   string
	Reason string
}

func (e clusterSSHPreparationGateBlocked) Error() string { return e.Gate + ": " + e.Reason }
func (clusterSSHPreparationGateBlocked) Unwrap() error   { return errClusterConflict }

func clusterSSHPreparationGateError(index int, cause error) error {
	code := clusterSSHPreparationGateCodes[index]
	var blocked clusterSSHPreparationGateBlocked
	if errors.As(cause, &blocked) && blocked.Gate == code {
		return blocked
	}
	return clusterSSHPreparationGateBlocked{code, "Fresh complete-cohort native proof failed; inspect this readiness gate before retrying preparation."}
}

type clusterSSHPreparationGateReceipt struct {
	Code   string `json:"code"`
	SHA256 string `json:"sha256"`
}

type clusterSSHPreparationReadinessReceipt struct {
	Version    int                                      `json:"version"`
	PlanSHA256 string                                   `json:"plan_sha256"`
	Checks     []clusterSSHPreparationGateReceipt       `json:"checks"`
	Targets    []clusterSSHPreparationTargetCPU         `json:"targets,omitempty"`
	Capacity   *clusterSSHPreparationCapacityProjection `json:"capacity,omitempty"`
	Packages   []clusterSSHPreparationTargetPackages    `json:"packages,omitempty"`
}

func clusterSSHPreparationReadiness(plan string, receipts [6]string) clusterSSHPreparationReadinessReceipt {
	r := clusterSSHPreparationReadinessReceipt{Version: 1, PlanSHA256: plan}
	for i, code := range clusterSSHPreparationGateCodes {
		r.Checks = append(r.Checks, clusterSSHPreparationGateReceipt{code, receipts[i]})
	}
	return r
}

func parseClusterSSHPreparationReadiness(raw []byte, a clusterSSHPreparationAuthorization) error {
	var r clusterSSHPreparationReadinessReceipt
	plan, err := clusterSSHPreparationPlanDigest(a)
	if err != nil || len(raw) == 0 || len(raw) > 8<<10 || json.Unmarshal(raw, &r) != nil ||
		(r.Version != 1 && r.Version != 2 && r.Version != 3 && r.Version != 4) || r.PlanSHA256 != plan || len(r.Checks) != len(clusterSSHPreparationGateCodes) {
		return errClusterConflict
	}
	canonical, err := json.Marshal(r)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return errClusterConflict
	}
	for i, check := range r.Checks {
		if check.Code != clusterSSHPreparationGateCodes[i] || !validClusterSSHPreparationDigest(check.SHA256) {
			return errClusterConflict
		}
	}
	if r.Version < 4 && len(raw) > 4096 || r.Version == 1 && (len(r.Targets) != 0 || r.Capacity != nil || len(r.Packages) != 0) || r.Version >= 2 && validateClusterSSHPreparationReadinessCPU(r, a) != nil {
		return errClusterConflict
	}
	return nil
}

// The caller synchronously consumes a short-lived capability; cancellation,
// expiry, failed/changed proof, ignored check errors and scope exit invalidate it.
// Every callback runs with no database connection retained and must join its IO.
func withClusterSSHPreparationApprovalProof(parent context.Context, plan string, gates clusterSSHPreparationGates,
	source func(context.Context) error, consume func(context.Context, clusterSSHPreparationApprovalProof) error) error {
	if parent.Err() != nil || !validClusterSSHPreparationDigest(plan) || source == nil || consume == nil {
		return errClusterConflict
	}
	for i, observe := range gates {
		if observe == nil {
			return clusterSSHPreparationGateBlocked{clusterSSHPreparationGateCodes[i], "Native readiness adapter is unavailable; preparation cannot be approved."}
		}
	}
	// Hash the owned release before starting the observation window. The owner
	// verifies the bytes again before dispatch. Commit fences the same source,
	// controller lease, original targets and confirmation in its transaction.
	bulk, stopBulk := context.WithTimeout(parent, clusterSSHPreparationArchiveBudget)
	defer stopBulk()
	if source(bulk) != nil || bulk.Err() != nil {
		return errClusterConflict
	}
	artifact, err := gates[0](bulk, plan)
	if err != nil || !validClusterSSHPreparationDigest(artifact) || bulk.Err() != nil {
		return clusterSSHPreparationGateError(0, err)
	}
	started := time.Now()
	ctx, cancel := context.WithDeadline(bulk, started.Add(clusterSSHPreparationFreshness))
	defer cancel()
	retained := [6]string{artifact}
	for i := 1; i < len(gates); i++ {
		digest, err := gates[i](ctx, plan)
		if err != nil || !validClusterSSHPreparationDigest(digest) || ctx.Err() != nil {
			return clusterSSHPreparationGateError(i, err)
		}
		retained[i] = digest
	}
	var used, verified atomic.Bool
	recheck := func(caller context.Context) error {
		if ctx.Err() != nil || caller.Err() != nil || !used.CompareAndSwap(false, true) {
			cancel()
			return errClusterConflict
		}
		bound, stop := context.WithCancel(ctx)
		defer stop()
		detach := context.AfterFunc(caller, stop)
		defer detach()
		if source(bound) != nil || caller.Err() != nil || bound.Err() != nil {
			cancel()
			return errClusterConflict
		}
		verified.Store(true)
		return nil
	}
	proof := clusterSSHPreparationApprovalProof{plan: plan, started: started, lifetime: ctx, recheck: recheck, receipts: retained}
	if err := consume(ctx, proof); err != nil {
		var blocked clusterSSHPreparationGateBlocked
		if errors.As(err, &blocked) {
			return blocked
		}
		return errClusterConflict
	}
	if !verified.Load() || !proof.valid(plan) || ctx.Err() != nil {
		return errClusterConflict
	}
	return nil
}

// Approval retains the existing source observation and complete proof contract.
// Native composition cannot approve while any required gate remains blocked.
func (c *clusterController) approveClusterSSHPreparation(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot,
	confirmation clusterSSHPreparationConfirmation, getJSON func(context.Context, string, any) error, gates clusterSSHPreparationGates, targetReaders ...clusterSSHPreparationTargetsRead) error {
	_, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	if c == nil || err != nil || getJSON == nil || !confirmation.valid(snapshot.work.Authorization, snapshot.work.Proof.Source) {
		return errClusterConflict
	}
	source := func(ctx context.Context) error {
		current, err := observeClusterSSHSourceCohort(ctx, getJSON, snapshot.work.Proof.Source)
		if err != nil || !reflect.DeepEqual(current, snapshot.work.Proof.Source) || ctx.Err() != nil {
			return errClusterConflict
		}
		return nil
	}
	var targets clusterSSHPreparationTargetsRead
	if len(targetReaders) > 1 {
		return errClusterConflict
	}
	if len(targetReaders) == 1 {
		targets = targetReaders[0]
	} else {
		client, err := newClusterSSHPreparationCleanBrokerClientFromEnv()
		if err == nil {
			targets = client.preparationTargetsRead(snapshot, c.preparationCleanTargetsProposalCheck(snapshot))
		}
	}
	if targets == nil {
		return clusterSSHPreparationGateBlocked{"complete_capacity", "Fresh native target CPU and memory evidence is unavailable through the authenticated API executor; preparation cannot be approved."}
	}
	return withClusterSSHPreparationCPUApprovalProof(ctx, snapshot, gates, source, targets, func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
		return c.commitClusterSSHPreparationApproval(ctx, snapshot, confirmation, proof)
	})
}
