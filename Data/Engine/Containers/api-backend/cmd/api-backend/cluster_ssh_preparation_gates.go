package main

import (
	"context"
	"encoding/json"
	"reflect"
	"time"
)

var clusterSSHPreparationGateCodes = [6]string{"immutable_artifacts", "clean_targets", "complete_capacity", "persistent_storage", "inherited_configuration", "supervised_recovery"}

// Native adapters must produce fresh complete-cohort domain proof for the exact
// proposed plan. A digest is an equality receipt, never permission by itself.
// No production adapters are installed yet; a missing entry fails closed.
type clusterSSHPreparationGates [6]func(context.Context, string) (string, error)

type clusterSSHPreparationGateReceipt struct {
	Code   string `json:"code"`
	SHA256 string `json:"sha256"`
}

type clusterSSHPreparationReadinessReceipt struct {
	Version    int                                `json:"version"`
	PlanSHA256 string                             `json:"plan_sha256"`
	Checks     []clusterSSHPreparationGateReceipt `json:"checks"`
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
	if err != nil || len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &r) != nil ||
		r.Version != 1 || r.PlanSHA256 != plan || len(r.Checks) != len(clusterSSHPreparationGateCodes) {
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
	for _, observe := range gates {
		if observe == nil {
			return errClusterConflict
		}
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	started := time.Now()
	gate := make(chan struct{}, 1)
	var retained [6]string
	initialized := false
	recheck := func(caller context.Context) error {
		fail := func() error { cancel(); return errClusterConflict }
		if caller.Err() != nil || ctx.Err() != nil {
			return fail()
		}
		bound, stop := context.WithCancel(caller)
		defer stop()
		detach := context.AfterFunc(ctx, stop)
		defer detach()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-bound.Done():
			return fail()
		}
		if source(bound) != nil || bound.Err() != nil || ctx.Err() != nil {
			return fail()
		}
		var observed [6]string
		for i, observe := range gates {
			value, err := observe(bound, plan)
			if err != nil || !validClusterSSHPreparationDigest(value) || bound.Err() != nil || ctx.Err() != nil {
				return fail()
			}
			observed[i] = value
		}
		if source(bound) != nil || bound.Err() != nil || ctx.Err() != nil || (initialized && observed != retained) {
			return fail()
		}
		if !initialized {
			retained, initialized = observed, true
		}
		return nil
	}
	if recheck(ctx) != nil {
		return errClusterConflict
	}
	proof := clusterSSHPreparationApprovalProof{plan: plan, started: started, lifetime: ctx, recheck: recheck, receipts: retained}
	if consume(ctx, proof) != nil || !proof.valid(plan) || ctx.Err() != nil {
		return errClusterConflict
	}
	return nil
}

// This coordinator deliberately has no runtime caller while native phase gates
// and authenticated operator confirmation remain unavailable. It composes only
// existing controller source observation with the explicit complete proof set.
func (c *clusterController) approveClusterSSHPreparation(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot,
	confirmation clusterSSHPreparationConfirmation, getJSON func(context.Context, string, any) error, gates clusterSSHPreparationGates) error {
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
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
	return withClusterSSHPreparationApprovalProof(ctx, plan, gates, source, func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
		return c.commitClusterSSHPreparationApproval(ctx, snapshot, confirmation, proof)
	})
}
