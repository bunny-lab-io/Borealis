package main

import (
	"context"
	"time"
)

// Reuse the preparation cleanup-fence pattern before the first admission
// effect. A process restart cannot blindly replay the runner's initial drains.
func (c *clusterController) reserveClusterSSHPreparedAdmissionRun(ctx context.Context, operation clusterControllerOperation, original clusterSSHPreparedAuthority) error {
	if operation.CurrentStep != "apply_membership" {
		return errClusterConflict
	}
	b, err := readClusterSSHPreparedStoreBinding(ctx, c.store.db, original, operation, c.holder)
	if err != nil {
		return err
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	now, err := b.lock(ctx, tx)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET error_text=$2,updated_at=$3 WHERE id=$1 AND COALESCE(error_text,'')=''`, operation.ID, clusterSSHPreparedAdmissionRunning, now)
	if err != nil {
		return errClusterConflict
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errClusterConflict
	}
	if _, err := b.clock(ctx, tx, operation.CurrentStep, 0); err != nil || ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}

func (c *clusterController) advanceClusterSSHPreparedAdmission(ctx context.Context, operation clusterControllerOperation, original clusterSSHPreparedAuthority) error {
	return c.settleClusterSSHPreparedAdmission(ctx, operation, original, clusterSSHPreparedObservation{}, false)
}

func (c *clusterController) finishClusterSSHPreparedAdmission(ctx context.Context, operation clusterControllerOperation, original clusterSSHPreparedAuthority, observed clusterSSHPreparedObservation) error {
	deadline, bounded := ctx.Deadline()
	if !bounded || time.Until(deadline) > 60*time.Second || !clusterSSHPreparedAdmissionSameObserved(operation, c.holder, original, observed) {
		return errClusterConflict
	}
	return c.settleClusterSSHPreparedAdmission(ctx, operation, original, observed, true)
}

// The existing membership runner proves deployment and soaks. This transaction
// retains its normal member-row behavior without manufacturing invitation rows
// or changing the original SSH operation kind/attempt. Native journals remain.
func (c *clusterController) settleClusterSSHPreparedAdmission(ctx context.Context, operation clusterControllerOperation, original clusterSSHPreparedAuthority, observed clusterSSHPreparedObservation, complete bool) error {
	if c == nil || c.store == nil || c.store.db == nil || operation.Kind != "ssh_onboarding" ||
		(!complete && operation.CurrentStep != "apply_membership") || (complete && operation.CurrentStep != "verify_quorum") {
		return errClusterConflict
	}
	b, err := readClusterSSHPreparedStoreBinding(ctx, c.store.db, original, operation, c.holder)
	if err != nil {
		return err
	}
	roles := marshalClusterJSON(map[string]any{"k3s_version": original.version})
	const health = `{"startup":"passed","readiness":"passed","liveness":"passed","direct_endpoint":"passed","service":"passed","database":"passed","scheduler":"passed","agent_path":"passed","wireguard":"passed"}`
	event, message := "operation_step_completed", "Prepared targets completed admission deployment and health soaks; final quorum verification remains."
	state := "running"
	if complete {
		event, message, state = "operation_succeeded", "Prepared cohort admitted after deployment, health soaks and independent quorum verification.", "succeeded"
	}
	details := marshalClusterJSON(map[string]any{"kind": operation.Kind, "attempt": operation.Attempt, "step": operation.CurrentStep})
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	now, err := b.lock(ctx, tx)
	if err != nil {
		return err
	}
	var currentError string
	if tx.QueryRowContext(ctx, `SELECT COALESCE(error_text,'') FROM engine.cluster_operations WHERE id=$1`, operation.ID).Scan(&currentError) != nil ||
		(!complete && currentError != clusterSSHPreparedAdmissionRunning) || (complete && currentError != "") {
		return errClusterConflict
	}
	// Existing SHARE locks retain these exact expiries until commit, including
	// after credential deletion. No terminal write may outlive their authority.
	var expiresAt int64
	if tx.QueryRowContext(ctx, `SELECT LEAST(l.expires_at,MIN(p.expires_at))
 FROM engine.cluster_application_leases l,engine.cluster_onboarding_credentials p
 JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id
 WHERE l.name=$1 AND l.holder=$2 AND t.operation_id=$3 GROUP BY l.expires_at`,
		clusterControllerLeaseName, b.holder, operation.ID).Scan(&expiresAt) != nil {
		return errClusterConflict
	}
	if complete {
		for _, target := range original.proof.Cohort.Targets {
			var durableID string
			err := tx.QueryRowContext(ctx, `INSERT INTO engine.cluster_nodes
 (id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,drain_reason,roles_json,probe_health_json,last_seen_at,created_at,updated_at)
 VALUES($1,$2,$2,$3,$4,$5,'Active','active',$6,$7,NULL,$8,$9,$10,$10,$10)
 ON CONFLICT(node_name) DO UPDATE SET hostname=EXCLUDED.hostname,management_ip=EXCLUDED.management_ip,
 architecture=EXCLUDED.architecture,os_version=EXCLUDED.os_version,membership_state='Active',application_state='active',
 release_tag=EXCLUDED.release_tag,release_sha=EXCLUDED.release_sha,drain_reason=NULL,roles_json=EXCLUDED.roles_json,
 probe_health_json=EXCLUDED.probe_health_json,last_seen_at=EXCLUDED.last_seen_at,updated_at=EXCLUDED.updated_at
 WHERE engine.cluster_nodes.membership_state='Removed' OR engine.cluster_nodes.id=EXCLUDED.id RETURNING id`,
				target.Binding.TargetID, target.Report.Hostname, target.Binding.Address, target.Report.Architecture, target.Report.OSVersion,
				original.baseline.Release, original.baseline.SourceSHA, roles, health, now).Scan(&durableID)
			if err != nil || durableID == "" {
				return errClusterConflict
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events
 (operation_id,cluster_id,event_type,state,message,details_json,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		operation.ID, original.queue.ClusterID, event, state, message, details, now); err != nil {
		return errClusterConflict
	}
	proofTime := int64(0)
	if complete {
		proofTime = observed.ObservedAt
	}
	if now, err = b.clock(ctx, tx, operation.CurrentStep, proofTime); err != nil || c.holder != b.holder || ctx.Err() != nil {
		return errClusterConflict
	}
	if !complete {
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step='verify_quorum',error_text=NULL,updated_at=$2 WHERE id=$1`, operation.ID, now)
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='joined',current_step='complete',credential_state='required',updated_at=$2 WHERE operation_id=$1`, operation.ID, now); err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id IN (SELECT id FROM engine.cluster_onboarding_targets WHERE operation_id=$1)`, operation.ID)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='succeeded',current_step='complete',error_text=NULL,finished_at=$2,updated_at=$2 WHERE id=$1`, operation.ID, now)
		}
		if err == nil {
			result, stateErr := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=3,desired_size=3,status='Healthy',active_operation_id=NULL,updated_at=$2 WHERE id=1 AND active_operation_id=$1 AND (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active')=3`, operation.ID, now)
			if stateErr != nil {
				return errClusterConflict
			}
			if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
				return errClusterConflict
			}
		}
	}
	if err != nil {
		return errClusterConflict
	}
	// DELETE and final state writes can wait on locks too. Check database time
	// after those waits; the deleted credentials cannot be queried by b.clock.
	var current bool
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED (SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT moment.now<$1 AND ($2::bigint=0 OR ($2::bigint<=moment.now AND $2::bigint>moment.now-60)) FROM moment`,
		expiresAt, proofTime).Scan(&current) != nil || !current || c.holder != b.holder || ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}
