package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync/atomic"
	"time"
)

const clusterSSHPreparedAdmissionRunning = "Prepared admission execution is in progress or interrupted; original action records must be reconciled before another execution."
const clusterSSHPreparedAdmissionBlocked = "Prepared admission is blocked or interrupted; original cohort and action records remain reserved. Reconcile identity, quorum, credentials and existing Jobs before retrying."

// The normal controller retains the SSH operation and its original cohort.
// Neither invitations nor another operation/attempt are invented for admission.
func (c *clusterController) runSSHPreparedAdmissionParent(parent context.Context, operation clusterControllerOperation) error {
	if c == nil || c.store == nil || operation.Kind != "ssh_onboarding" || operation.State != "running" || !clusterSSHPreparedAuthorityPhase(operation.CurrentStep) {
		return errClusterConflict
	}
	started := time.Now()
	var first atomic.Pointer[string]
	remember := func(line string) { first.CompareAndSwap(nil, &line) }
	parent = context.WithValue(parent, clusterSSHPreparationObserverKey{}, remember)
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	original, _, err := c.loadClusterSSHPreparedAuthority(ctx, operation)
	phase := "original_prepared_authority"
	effects := "admission_not_started_in_this_call"
	blocked := func(cause error, scope context.Context) error {
		reason := "check_rejected"
		if errors.Is(cause, context.DeadlineExceeded) || errors.Is(scope.Err(), context.DeadlineExceeded) {
			reason = "deadline_exceeded"
		} else if errors.Is(cause, context.Canceled) || scope.Err() == context.Canceled {
			reason = "scope_canceled"
		}
		line := fmt.Sprintf("phase=%s observed=%s elapsed_ms=%d effects=%s", phase, reason, time.Since(started).Milliseconds(), effects)
		for _, target := range original.receipt.Targets {
			line += fmt.Sprintf(" prepared_target=%s preparation_generation=%d", target.TargetID, target.Generation)
		}
		if child := first.Load(); child != nil {
			line += " " + *child
		}
		log.Printf("SSH admission blocked operation=%s attempt=%d step=%s %s", operation.ID, operation.Attempt, operation.CurrentStep, line)
		return c.recordSSHPreparedAdmissionBlocked(parent, operation, line)
	}
	var observation clusterSSHPreparedObservation
	if err == nil && operation.CurrentStep == "verify_quorum" {
		phase = "serving_endpoints"
		err = c.withClusterSSHPreparedAdmissionRuntime(ctx, operation, original, func(ctx context.Context, runner *kubernetesClusterStepRunner) error {
			return runner.verifyReadyEndpoints(ctx)
		})
	}
	if err == nil {
		phase = "three_current_membership_proofs"
		err = c.withClusterSSHPreparedAdmissionProof(ctx, operation, func(ctx context.Context, value clusterSSHPreparedCohortMembershipObservation, checks clusterSSHPreparedMembershipChecks) error {
			// No state change inside this callback: final native reads and joined
			// cleanup must succeed before transition or any admission effect.
			observation = value.Prepared
			return checks.Authority(ctx)
		})
	}
	if err != nil {
		return blocked(err, ctx)
	}
	if operation.CurrentStep == clusterSSHPreparationOperationStep {
		err = c.beginClusterSSHPreparedAdmission(ctx, operation, original, observation)
		cancel()
		return err
	}
	if operation.CurrentStep == "verify_quorum" {
		// Independent whole-cohort quorum/identity proof is fresh here. Native
		// promotion and every configured health/role soak already completed.
		err = c.finishClusterSSHPreparedAdmission(ctx, operation, original, observation)
		cancel()
		return err
	}
	if err = c.reserveClusterSSHPreparedAdmissionRun(ctx, operation, original); err != nil {
		cancel()
		return err
	}
	cancel()
	// The bounded proof has closed. Long-running deployment/soak keeps its own
	// original SQL/Job custody and exact native target fences, never a retained
	// quorum-ready flag or an extended preparation-observation scope.
	phase = "reserved_admission_runtime"
	err = c.withClusterSSHPreparedAdmissionRuntime(parent, operation, original, func(ctx context.Context, runner *kubernetesClusterStepRunner) error {
		if bound := clusterSSHPreparedAdmissionRuntimeFrom(ctx); bound != nil {
			defer func() {
				if bound.mutated {
					effects = "admission_mutation_attempted_consult_jobs"
				}
			}()
		}
		projected := operation
		projected.Payload = copyMap(operation.Payload)
		ids, names := []any{}, []any{}
		for _, target := range original.receipt.Targets {
			ids, names = append(ids, target.TargetID), append(names, target.Prejoin.Receipt.Node.Hostname)
		}
		projected.Payload["admission_ids"], projected.Payload["node_names"] = ids, names
		projected.Payload["baseline_release"], projected.Payload["baseline_sha"] = original.baseline.Release, original.baseline.SourceSHA
		projected.Payload["k3s_version"] = original.version
		phase = "active_source_members"
		nodes, err := c.activeNodes(ctx)
		if err != nil || len(nodes) != len(original.proof.Source.Members) {
			return errClusterConflict
		}
		for _, member := range original.proof.Source.Members {
			found := false
			for _, node := range nodes {
				found = found || node.ID == member.NodeID && node.Name == member.Name && node.ApplicationState == "active"
			}
			if !found {
				return errClusterConflict
			}
		}
		ctx = runner.soakContext(ctx, operation, clusterControllerStep{Name: operation.CurrentStep})
		phase = "paired_workload_activation"
		if err := runner.admitPendingMembers(ctx, projected, nodes); err != nil {
			return err
		}
		phase = "activated_workload_endpoints"
		return runner.verifyReadyEndpoints(ctx)
	})
	if err != nil {
		// Keep original operation, attempt, deterministic Jobs and reservations.
		// A failed/uncertain effect cannot become generic retry or a new claim.
		return blocked(err, parent)
	}
	ctx, cancel = context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	return c.advanceClusterSSHPreparedAdmission(ctx, operation, original)
}

func (c *clusterController) recordSSHPreparedAdmissionBlocked(parent context.Context, operation clusterControllerOperation, diagnostic string) error {
	if c == nil || c.store == nil || c.store.db == nil || operation.Kind != "ssh_onboarding" || operation.State != "running" || !clusterSSHPreparedAuthorityPhase(operation.CurrentStep) {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	const message = clusterSSHPreparedAdmissionBlocked
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	// Failure reporting cannot take ownership, alter payload or release custody.
	var now int64
	err = tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_operations o SET error_text=$4,updated_at=floor(moment.now)
 FROM moment,engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3
 AND s.id=1 AND s.active_operation_id=o.id AND l.name=$5 AND l.holder=$6 AND l.expires_at>moment.now
 AND COALESCE(o.error_text,'')<>$4 RETURNING floor(moment.now)::bigint`, operation.ID, operation.CurrentStep, operation.Attempt,
		message, clusterControllerLeaseName, c.holder).Scan(&now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errClusterConflict
	}
	if insertClusterEvent(ctx, tx, operation.ID, "", "", "ssh_prepared_admission_blocked", "running", message+" "+diagnostic, map[string]any{"attempt": operation.Attempt, "step": operation.CurrentStep, "diagnostic": diagnostic, "custody": "original_cohort_reserved"}, now) != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}

func clusterSSHPreparedAdmissionSameObserved(operation clusterControllerOperation, holder string, original clusterSSHPreparedAuthority, observation clusterSSHPreparedObservation) bool {
	prepared := operation
	prepared.CurrentStep = clusterSSHPreparationOperationStep
	return clusterSSHPreparedAdmissionObservationMatches(prepared, holder, original, observation) &&
		textInSet(operation.CurrentStep, "apply_membership", "verify_quorum") && reflect.DeepEqual(operation.Payload, parseClusterJSON(original.payload))
}
