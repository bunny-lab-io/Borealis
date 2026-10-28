package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Soak events describe an existing timer, never health or permission to proceed.
// Expiring observations let readers discard an abandoned timer even when its
// controller cannot record interruption after losing the lease or database.
type clusterOperationSoak struct {
	OperationID  string `json:"operation_id"`
	Attempt      int64  `json:"attempt"`
	Step         string `json:"step"`
	RunID        string `json:"run_id"`
	Holder       string `json:"holder"`
	Phase        string `json:"phase"`
	StartedAtMS  int64  `json:"started_at_ms"`
	DeadlineAtMS int64  `json:"deadline_at_ms"`
	ObservedAtMS int64  `json:"observed_at_ms"`
	ValidUntilMS int64  `json:"valid_until_ms"`
}

type clusterOperationSoakRecorder func(context.Context, clusterOperationSoak, string) error
type clusterOperationSoakContext struct {
	operation clusterControllerOperation
	step      clusterControllerStep
	record    clusterOperationSoakRecorder
	holder    string
}
type clusterOperationSoakKey struct{}

func (r *kubernetesClusterStepRunner) soakContext(ctx context.Context, operation clusterControllerOperation, step clusterControllerStep) context.Context {
	return context.WithValue(ctx, clusterOperationSoakKey{}, clusterOperationSoakContext{operation, step, r.recordOperationSoak, r.controllerHolder})
}

func (r *kubernetesClusterStepRunner) waitOperationSoak(ctx context.Context, phase string) error {
	return waitClusterOperationSoak(ctx, phase, r.soak, time.Now, nil, nil)
}

// The optional timer channel makes cancellation/deadline tests deterministic.
// Recording is best effort: availability of timeline storage must not change
// existing health checks, configured wait duration, or controller authority.
func waitClusterOperationSoak(ctx context.Context, phase string, duration time.Duration, now func() time.Time, elapsed, observations <-chan time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binding, _ := ctx.Value(clusterOperationSoakKey{}).(clusterOperationSoakContext)
	start := now()
	run := clusterOperationSoak{OperationID: binding.operation.ID, Attempt: binding.operation.Attempt,
		Step: binding.step.Name, RunID: newClusterUUID(), Holder: binding.holder, Phase: phase,
		StartedAtMS: start.UnixMilli(), DeadlineAtMS: start.Add(duration).UnixMilli()}
	if elapsed == nil {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		elapsed = timer.C
	}
	record := func(eventCtx context.Context, event string) {
		if binding.record == nil {
			return
		}
		bounded, cancel := context.WithTimeout(eventCtx, clusterControllerLeaseAcquireTimeout)
		defer cancel()
		_ = binding.record(bounded, run, event)
	}
	record(ctx, "operation_soak_started")
	if observations == nil {
		ticker := time.NewTicker(clusterControllerLeaseRenewInterval)
		defer ticker.Stop()
		observations = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			// Cancellation cannot grant authority. The store still requires the
			// exact live lease and operation; a lost owner leaves expiring evidence.
			record(context.WithoutCancel(ctx), "operation_soak_interrupted")
			return ctx.Err()
		case <-elapsed:
			if err := ctx.Err(); err != nil {
				record(context.WithoutCancel(ctx), "operation_soak_interrupted")
				return err
			}
			record(ctx, "operation_soak_elapsed")
			return nil
		case <-observations:
			record(ctx, "operation_soak_observed")
		}
	}
}

func (c *clusterController) recordOperationSoak(ctx context.Context, run clusterOperationSoak, event string) error {
	if !clusterUUIDRE.MatchString(run.OperationID) || !clusterUUIDRE.MatchString(run.RunID) || run.Attempt < 1 ||
		run.Step == "" || run.Phase == "" || run.Holder != c.holder || run.StartedAtMS <= 0 || run.DeadlineAtMS < run.StartedAtMS ||
		!textInSet(event, "operation_soak_started", "operation_soak_observed", "operation_soak_elapsed", "operation_soak_interrupted") {
		return errClusterConflict
	}
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	var active, clusterID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(active_operation_id,''),cluster_id FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&active, &clusterID); err != nil {
		return err
	}
	var state, step string
	var attempt int64
	if err := tx.QueryRowContext(ctx, `SELECT state,current_step,attempt FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, run.OperationID).Scan(&state, &step, &attempt); err != nil {
		return err
	}
	if active != run.OperationID || state != "running" || step != run.Step || attempt != run.Attempt {
		return errClusterConflict
	}
	// Recheck time only after all blocking locks. A transaction beginning before
	// lease expiry cannot append timing evidence after expiry while waiting.
	var expiresAt, databaseNowMS int64
	if err := tx.QueryRowContext(ctx, `SELECT expires_at,(extract(epoch FROM clock_timestamp())*1000)::bigint FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, c.holder).Scan(&expiresAt, &databaseNowMS); err != nil {
		return err
	}
	if expiresAt*1000 <= databaseNowMS || ctx.Err() != nil {
		return errClusterControllerLeaseLost
	}
	// Timer and public server clock use server wall time. A regressed controller
	// clock cannot renew an older observation; a reader also checks its clock.
	nowMS := c.now().UnixMilli()
	var previous clusterOperationSoak
	var previousJSON, previousType string
	err = tx.QueryRowContext(ctx, `SELECT event_type,details_json FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type IN ('operation_soak_started','operation_soak_observed','operation_soak_elapsed','operation_soak_interrupted') ORDER BY id DESC LIMIT 1`, run.OperationID).Scan(&previousType, &previousJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previousJSON != "" && json.Unmarshal([]byte(previousJSON), &previous) != nil {
		return errClusterConflict
	}
	if nowMS < run.StartedAtMS || nowMS < previous.ObservedAtMS {
		return errClusterConflict
	}
	if event == "operation_soak_elapsed" && nowMS < run.DeadlineAtMS {
		return errClusterConflict
	}
	if event != "operation_soak_started" && (previous.RunID != run.RunID || previous.OperationID != run.OperationID || previous.Attempt != run.Attempt || previous.Step != run.Step || previous.Holder != run.Holder || previous.Phase != run.Phase || previous.StartedAtMS != run.StartedAtMS || previous.DeadlineAtMS != run.DeadlineAtMS || !textInSet(previousType, "operation_soak_started", "operation_soak_observed")) {
		return errClusterConflict
	}
	if event == "operation_soak_started" && previous.RunID == run.RunID {
		return errClusterConflict
	}
	if event == "operation_soak_started" && run.StartedAtMS < previous.ObservedAtMS {
		// A delayed initial write cannot replace a newer timer's evidence.
		return errClusterConflict
	}
	if event == "operation_soak_started" && textInSet(previousType, "operation_soak_started", "operation_soak_observed") {
		previous.ObservedAtMS = nowMS
		previous.ValidUntilMS = 0
		if err := insertClusterEvent(ctx, tx, run.OperationID, "", clusterID, "operation_soak_interrupted", "running", "Previous soak wait discarded; a new wait starts from zero.", soakEventDetails(previous), nowMS/1000); err != nil {
			return err
		}
	}
	run.ObservedAtMS = nowMS
	// Both existing ownership grace and exact current lease bound the display.
	run.ValidUntilMS = min(nowMS+clusterControllerLeaseRenewalGrace.Milliseconds(), expiresAt*1000)
	message := "Configured soak wait in progress; health verification remains separate."
	if event == "operation_soak_elapsed" {
		message = "Configured soak wait elapsed; controller verification remains pending."
		run.ValidUntilMS = 0
	}
	if event == "operation_soak_interrupted" {
		message = "Configured soak wait interrupted; elapsed time cannot be reused."
		run.ValidUntilMS = 0
	}
	if err := insertClusterEvent(ctx, tx, run.OperationID, "", clusterID, event, "running", message, soakEventDetails(run), nowMS/1000); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT (extract(epoch FROM clock_timestamp())*1000)::bigint`).Scan(&databaseNowMS); err != nil {
		return err
	}
	if expiresAt*1000 <= databaseNowMS || ctx.Err() != nil {
		return errClusterControllerLeaseLost
	}
	return tx.Commit()
}

func soakEventDetails(run clusterOperationSoak) map[string]any {
	return map[string]any{"operation_id": run.OperationID, "attempt": run.Attempt, "step": run.Step, "run_id": run.RunID, "holder": run.Holder, "phase": run.Phase,
		"started_at_ms": run.StartedAtMS, "deadline_at_ms": run.DeadlineAtMS, "observed_at_ms": run.ObservedAtMS, "valid_until_ms": run.ValidUntilMS}
}
