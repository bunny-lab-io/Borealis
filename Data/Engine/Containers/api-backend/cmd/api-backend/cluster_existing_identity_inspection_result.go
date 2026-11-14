package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
)

const clusterExistingIdentityObserved = "existing_identity_observed"

type clusterExistingIdentityInspectionResult struct {
	Version    int
	PlanSHA256 string
	Source     clusterExistingIdentitySourceCustody
	Session    clusterbootstrap.SessionMessage
}

func (r clusterExistingIdentityInspectionResult) valid(c clusterExistingIdentityInspectionClaim) bool {
	binding, recovery, _ := c.session()
	m := r.Session
	return c.valid() && r.Version == 1 && r.PlanSHA256 == c.Confirmation.PlanSHA256 && r.Source.valid(c.Plan) &&
		binding.Validate() == nil && recovery.Validate(binding) == nil && m.Protocol == 1 && m.State == "verified" &&
		validClusterSSHPreparationDigest(m.Nonce) && validClusterSSHPreparationDigest(m.ManagerSHA256) && m.SourceSHA == c.Plan.Baseline.SourceSHA &&
		m.Binding != nil && *m.Binding == binding && m.IdentityRecovery != nil && *m.IdentityRecovery == recovery && m.IdentityEnvelope == nil
}

func (s *postgresOperatorStore) completeClusterExistingIdentityInspection(ctx context.Context, c clusterExistingIdentityInspectionClaim, result clusterExistingIdentityInspectionResult) error {
	if !result.valid(c) {
		return errClusterConflict
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > 32<<10 {
		return errClusterConflict
	}
	snapshot, err := readClusterExistingIdentityWorkSnapshot(ctx, s.db, c.Plan)
	if err != nil {
		return errClusterConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	if lockClusterExistingIdentityWork(ctx, tx, c) != nil {
		return errClusterConflict
	}
	w, err := readClusterExistingIdentityWorkRow(ctx, tx, c.Lease.OperationID, c.Lease.TargetID)
	if err != nil || !w.matches(c) || w.holder != c.Lease.Holder || w.generation != c.Lease.Generation {
		return errClusterConflict
	}
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, c.Plan.Controller, c.Plan.MaintenanceID, c.Plan.OperationID)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) {
		return errClusterConflict
	}
	if w.state == "prepared" && w.step == clusterExistingIdentityObserved && w.expires == 0 && w.inspectedAt > 0 && w.inspection == string(raw) {
		if checkClusterExistingIdentityWorkClock(ctx, tx, c, false) != nil || ctx.Err() != nil || tx.Commit() != nil {
			return errClusterConflict
		}
		return nil
	}
	if w.state != "running" || w.step != clusterExistingIdentityInspect || w.inspectedAt != 0 || checkClusterExistingIdentityWorkClock(ctx, tx, c, true) != nil {
		return errClusterConflict
	}
	// Retain expiry through event insertion. A blocked event cannot convert an
	// expired inspection into completion; final fence rolls all writes back.
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='prepared',current_step=$2,inspection_json=$3,
 inspected_at=floor(extract(epoch FROM clock_timestamp()))::bigint,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, c.Lease.TargetID, clusterExistingIdentityObserved, string(raw)); err != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,$2,'existing_identity_observed','waiting','Existing member verified; identity mutation remains separately fenced.','{}',floor(extract(epoch FROM clock_timestamp()))::bigint)`, c.Plan.OperationID, c.Plan.ClusterID); err != nil {
		return errClusterConflict
	}
	if checkClusterExistingIdentityWorkClock(ctx, tx, c, true) != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=0 WHERE id=$1`, c.Lease.TargetID); err != nil {
		return errClusterConflict
	}
	if ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}
