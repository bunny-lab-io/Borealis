package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const clusterSSHStageLost = "Source staging is unavailable. Start a fresh inspection before preparation."
const clusterSSHAwaitConfirmation = "Preparation awaits explicit confirmation of the current plan."
const clusterSSHPreparationCleanupPending = "Preparation approval cleanup pending; worker dispatch remains blocked."

type clusterSSHPreparationGateScope func(context.Context, clusterSSHPreparationApprovalSnapshot,
	func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)) error

// Only the sole controller evaluates waiting confirmation and commits approval.
// API executors retain Aegis/identity custody and consume the durable reservation.
func (c *clusterController) runSSHPreparationParent(ctx context.Context, operation clusterControllerOperation, getJSON func(context.Context, string, any) error) error {
	if required, _ := operation.Payload["ssh_source_staging_required"].(bool); required && operation.State == "waiting" && operation.CurrentStep == clusterSSHQualificationStep {
		if operation.Payload["ssh_qualification"] == nil {
			return c.stageInspectionRelease(ctx, operation)
		}
		if c.sourceStage == nil {
			return c.recordSSHPreparationBlocked(ctx, operation, clusterSSHStageLost)
		}
	}
	var projection clusterSSHPreparationTargetsRead
	return c.runSSHPreparationParentWithGates(ctx, operation, getJSON,
		func(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot, consume func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)) error {
			client, err := newClusterSSHPreparationCleanBrokerClientFromEnv()
			var clean func(context.Context, string) (string, error)
			var filesystems clusterSSHPreparationFilesystemReader
			check := c.preparationCleanTargetsProposalCheck(snapshot)
			if err == nil {
				ctx, err = clusterPreparationReleaseContext(ctx, client, snapshot)
				if err != nil {
					return err
				}
				clean, projection = clusterSSHPreparationTargetCPUReaderPair(snapshot, client.preparationTargetsRead(snapshot, check))
				filesystems = func(paths []string) clusterSSHPreparationTargetsRead {
					return client.preparationTargetsFilesystemRead(snapshot, check, paths)
				}
			}
			capacity, configuration, approvedTargets, storageSource := c.preparationCapacityStorageRead(snapshot, projection)
			projection = approvedTargets
			return c.withPreparationNativeGates(ctx, snapshot, clusterSSHScratchParent(), clean, capacity, configuration, storageSource, filesystems, consume)
		}, func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
			if projection == nil {
				return clusterSSHPreparationCleanObservation{}, errClusterConflict
			}
			return projection(ctx, plan)
		})
}

func (c *clusterController) runSSHPreparationParentWithGates(parent context.Context, operation clusterControllerOperation,
	getJSON func(context.Context, string, any) error, scope clusterSSHPreparationGateScope, targets ...clusterSSHPreparationTargetsRead) error {
	if c == nil || c.store == nil || c.store.db == nil || operation.Kind != "ssh_onboarding" || parent.Err() != nil {
		return errClusterConflict
	}
	if operation.State == "running" && operation.CurrentStep == clusterSSHPreparationOperationStep {
		// Legacy completed preparations keep their existing recovery path. Native
		// v2 completion must never be reinterpreted as a legacy identity repair.
		if raw, err := json.Marshal(operation.Payload["ssh_preparation"]); err == nil && string(raw) != "null" {
			var receipt struct {
				Version int `json:"version"`
			}
			if json.Unmarshal(raw, &receipt) == nil && receipt.Version == 1 {
				return c.runSSHIdentityRecoveryParent(parent, operation, getJSON)
			}
			if receipt.Version == 2 {
				return c.runSSHPreparedAdmissionParent(parent, operation)
			}
		}
		if _, completed := operation.Payload["ssh_preparation"]; !completed && operation.Payload["ssh_preparation_authorization"] != nil {
			// Selection observed an expired cohort. Recheck custody atomically
			// because a worker may have renewed since the controller selected it.
			_, err := c.reserveClusterSSHPreparationRecovery(parent, operation)
			return err
		}
		return nil
	}
	if operation.State != "waiting" || operation.CurrentStep != clusterSSHQualificationStep || scope == nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	snapshot, now, err := c.loadClusterSSHPreparationApproval(ctx, operation)
	cancel()
	if err != nil {
		return c.recordSSHPreparationBlocked(parent, operation, "Preparation proposal is incomplete, expired, or changed; fresh inspection and qualification are required.")
	}
	var request json.RawMessage
	if clusterBootstrapObject([]byte(snapshot.work.Payload), map[string]any{"ssh_preparation_request": &request}) != nil {
		return c.recordSSHPreparationBlocked(parent, operation, "Preparation confirmation is invalid; inspect and confirm the current plan again.")
	}
	if len(request) == 0 {
		return c.recordSSHPreparationBlocked(parent, operation, clusterSSHAwaitConfirmation)
	}
	confirmation, err := parseClusterSSHPreparationRequest(request, snapshot.work.Authorization, snapshot.work.Proof.Source)
	if err != nil || confirmation.ConfirmedAt > now || now-confirmation.ConfirmedAt >= clusterSSHFreshEvidenceSeconds {
		return c.recordSSHPreparationBlocked(parent, operation, "Preparation confirmation is expired or changed; inspect and confirm the current plan again.")
	}
	if getJSON == nil {
		return c.recordSSHPreparationBlocked(parent, operation, "Preparation source observation is unavailable; original controller observation is required.")
	}
	var committedPayload string
	err = scope(parent, snapshot, func(ctx context.Context, gates clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error) {
		plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
		if err != nil {
			return clusterSSHPreparationReadinessReceipt{}, errClusterConflict
		}
		var committed clusterSSHPreparationReadinessReceipt
		if err := c.recordSSHPreparationBlocked(ctx, operation, clusterSSHPreparationCleanupPending); err != nil {
			return committed, err
		}
		for i, read := range gates {
			if read != nil {
				gates[i] = func(checkCtx context.Context, plan string) (string, error) {
					c.store.recordSSHCheckStarted(checkCtx, snapshot.work.Proof.Cohort, snapshot.work.Payload, clusterSSHPreparationGateCodes[i], clusterSSHTargetLease{})
					return read(checkCtx, plan)
				}
			}
		}
		if err = c.approveClusterSSHPreparation(ctx, snapshot, confirmation, getJSON, gates, targets...); err != nil {
			return committed, err
		}
		// The artifact scope validates the exact committed payload after approval,
		// then closes owned bytes before any worker can use their receipt.
		var payload string
		if c.store.db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1 AND state='running' AND current_step=$2 AND attempt=$3`, operation.ID, clusterSSHPreparationOperationStep, operation.Attempt).Scan(&payload) != nil {
			return committed, errClusterConflict
		}
		var raw json.RawMessage
		if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_preparation_readiness": &raw}) != nil || json.Unmarshal(raw, &committed) != nil || committed.PlanSHA256 != plan {
			return clusterSSHPreparationReadinessReceipt{}, errClusterConflict
		}
		committedPayload = payload
		return committed, nil
	})
	if err != nil {
		if committedPayload != "" {
			return errClusterConflict // Durable cleanup fence remains; never replay.
		}
		message := "Preparation readiness proof failed or expired; no new target work is authorized."
		if reasons := clusterSSHPreparationBlockedReasons(err); len(reasons) != 0 {
			message = "Preparation blocked: " + strings.Join(reasons, "; ")
		}
		return c.recordSSHPreparationBlocked(parent, operation, message)
	}
	// The whole native proof/artifact scope has closed successfully. Release
	// dispatch only for this exact committed payload under original ownership.
	if committedPayload == "" {
		return errClusterConflict
	}
	bound, stop := context.WithTimeout(parent, 3*time.Second)
	defer stop()
	result, err := c.store.db.ExecContext(bound, `UPDATE engine.cluster_operations o SET error_text=NULL
 FROM engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3
 AND o.payload_json=$4 AND o.error_text=$5 AND s.id=1 AND s.active_operation_id=o.id
 AND l.name=$6 AND l.holder=$7 AND l.expires_at>extract(epoch FROM clock_timestamp())`, operation.ID, clusterSSHPreparationOperationStep, operation.Attempt, committedPayload, clusterSSHPreparationCleanupPending, clusterControllerLeaseName, c.holder)
	if err != nil {
		return errClusterUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errClusterConflict
	}
	return nil
}

// Persist only changed public state. Waiting controller ticks cannot spam
// events, alter the confirmed payload, claim targets or refresh proof expiry.
// Losing the local stage after a native failure must preserve its diagnosis.
func (c *clusterController) recordSSHPreparationBlocked(parent context.Context, operation clusterControllerOperation, reason string) error {
	if c == nil || c.store == nil || c.store.db == nil || len(reason) == 0 || len(reason) > 4096 || operation.State != "waiting" || operation.CurrentStep != clusterSSHQualificationStep {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	var clusterID string
	var now int64
	err = tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_operations o SET error_text=$4,updated_at=floor(moment.now)
 FROM moment,engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step=$2 AND o.attempt=$3
 AND s.id=1 AND s.active_operation_id=o.id AND l.name=$5 AND l.holder=$6 AND l.expires_at>moment.now
 AND COALESCE(o.error_text,'')<>$4
 AND ($4<>$7 OR COALESCE(o.error_text,'') IN ('',$8,$9))
 RETURNING s.cluster_id,floor(moment.now)::bigint`, operation.ID,
		clusterSSHQualificationStep, operation.Attempt, reason, clusterControllerLeaseName, c.holder, clusterSSHStageLost, clusterSSHStageReady, clusterSSHAwaitConfirmation).Scan(&clusterID, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || insertClusterEvent(ctx, tx, operation.ID, "", clusterID, "ssh_preparation_blocked", "waiting", reason, map[string]any{"attempt": operation.Attempt}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}

// Retain only fixed canonical gate diagnostics from nested errors.Join values.
// No native/SQL/SSH error text is copied to operator-visible state.
func clusterSSHPreparationPublicGateErrors(err error) []error {
	seen := map[string]string{}
	var visit func(error)
	visit = func(err error) {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				visit(child)
			}
			return
		}
		var blocked clusterSSHPreparationGateBlocked
		if errors.As(err, &blocked) {
			if _, exists := seen[blocked.Gate]; !exists {
				seen[blocked.Gate] = ""
			}
			if blocked.Gate == "supervised_recovery" {
				switch blocked.Reason {
				case supervisionScopeUnavailable, supervisionHelperUnavailable, supervisionTargetsUnavailable, supervisionEvidenceChanged:
					seen[blocked.Gate] = blocked.Reason
				}
			}
		}
	}
	visit(err)
	var reasons []error
	canonical := append(clusterSSHPreparationNativeBlockers(false, false, false),
		clusterSSHPreparationGateBlocked{"immutable_artifacts", "The controller could not verify the release artifacts."},
		clusterSSHPreparationGateBlocked{"inherited_configuration", "The controller could not verify the inherited configuration."})
	for _, known := range canonical {
		if reason, exists := seen[known.Gate]; exists {
			if reason != "" {
				known.Reason = reason
			}
			reasons = append(reasons, known)
		}
	}
	return reasons
}

func clusterSSHPreparationBlockedReasons(err error) []string {
	var reasons []string
	for _, gate := range clusterSSHPreparationPublicGateErrors(err) {
		reasons = append(reasons, gate.Error())
	}
	return reasons
}
