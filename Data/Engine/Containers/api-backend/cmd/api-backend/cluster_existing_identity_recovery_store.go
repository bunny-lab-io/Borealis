package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

const clusterExistingIdentityRecover = "recover_existing_identity"
const clusterExistingIdentityRecovered = "existing_identity_recovered"

func (c *clusterController) reconcileClusterExistingIdentityMutation(ctx context.Context, getJSON func(context.Context, string, any) error) (bool, error) {
	var operation, target, step string
	err := c.store.db.QueryRowContext(ctx, `SELECT o.id,t.id,o.current_step FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id AND t.cluster_id=c.cluster_id
 WHERE o.kind=$1 AND t.state='prepared' AND t.lease_expires_at=0 AND
 ((o.state='waiting' AND o.current_step=$2 AND t.current_step=$3) OR (o.state='running' AND o.current_step=$4 AND t.current_step=$5)) LIMIT 1`,
		clusterExistingIdentityKind, clusterExistingIdentityConfirmed, clusterExistingIdentityObserved, clusterExistingIdentityRecover, clusterExistingIdentityRecovered).Scan(&operation, &target, &step)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errClusterConflict
	}
	if step == clusterExistingIdentityRecover {
		return true, c.completeClusterExistingIdentityOperation(ctx, operation, target, getJSON)
	}
	return true, c.reserveClusterExistingIdentityMutation(ctx, operation, target, getJSON)
}

// Closing recovery does not exit the operator's maintenance session. The
// existing explicit maintenance-exit operation owns restart and health checks.
func (c *clusterController) completeClusterExistingIdentityOperation(parent context.Context, operation, target string, getJSON func(context.Context, string, any) error) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	w, err := readClusterExistingIdentityRecoveryRow(ctx, c.store.db, operation, target)
	if err != nil {
		return errClusterConflict
	}
	claim, err := parseClusterExistingIdentityRecoveryWork(w, operation, target)
	if err != nil || !claim.valid() || claim.Inspection.Plan.Controller != c.holder || w.state != "prepared" || w.step != clusterExistingIdentityRecovered || w.expires != 0 {
		return errClusterConflict
	}
	p := claim.Inspection.Plan
	snapshot, err := readClusterExistingIdentityWorkSnapshot(ctx, c.store.db, p)
	if err != nil {
		return errClusterConflict
	}
	// Fresh source/target Node identity remains required; no target readiness,
	// consumer restart or automatic maintenance exit is inferred here.
	namespace, members, err := observeClusterExistingIdentityNodes(ctx, getJSON, snapshot)
	if err != nil || namespace != p.KubeSystemUID || !reflect.DeepEqual(members, p.Members) {
		return errClusterConflict
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present) != nil || lockClusterExistingIdentityWork(ctx, tx, claim.Inspection) != nil {
		return errClusterConflict
	}
	current, err := readClusterExistingIdentityRecoveryRow(ctx, tx, operation, target)
	if err != nil || !current.matchesRecovery(claim) || current.state != "prepared" || current.step != clusterExistingIdentityRecovered || current.expires != 0 {
		return errClusterConflict
	}
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, c.holder, p.MaintenanceID, operation)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='succeeded',current_step='complete',finished_at=floor(extract(epoch FROM clock_timestamp()))::bigint,
 updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, operation); err != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='completed',updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, target); err != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=1 AND active_operation_id=$1`, operation); err != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,$2,'completed','succeeded','Shared identity recovered; operator maintenance remains in effect.','{}',floor(extract(epoch FROM clock_timestamp()))::bigint)`, operation, p.ClusterID); err != nil {
		return errClusterConflict
	}
	clock := claim.Inspection
	clock.Lease = claim.Lease
	if checkClusterExistingIdentityWorkClock(ctx, tx, clock, false) != nil || ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}

type clusterExistingIdentityRecoveryClaim struct {
	Inspection clusterExistingIdentityInspectionClaim
	Result     clusterExistingIdentityInspectionResult
	ObservedAt int64
	Lease      clusterSSHTargetLease
	receipt    string
}

func validClusterExistingIdentityRecoveryLease(l clusterSSHTargetLease) bool {
	for _, id := range []string{l.OperationID, l.TargetID, l.Holder} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return l.OperationKind == clusterExistingIdentityKind && l.OperationStep == clusterExistingIdentityRecover && l.Step == clusterExistingIdentityRecover &&
		l.OperationAttempt == 1 && l.Generation > 1 && l.Generation < 1<<53 && len(l.ControllerHolder) > 0 && len(l.ControllerHolder) <= 1024
}

func (c clusterExistingIdentityRecoveryClaim) valid() bool {
	i, l := c.Inspection, c.Lease
	return i.valid() && c.Result.valid(i) && c.ObservedAt >= i.Confirmation.ConfirmedAt && validClusterExistingIdentityRecoveryLease(l) &&
		l.OperationID == i.Lease.OperationID && l.TargetID == i.Lease.TargetID && l.ControllerHolder == i.Lease.ControllerHolder && l.Generation > i.Lease.Generation &&
		c.receipt == marshalClusterJSON(c.Result)
}

func (c clusterExistingIdentityRecoveryClaim) session() (clusterbootstrap.SessionBinding, clusterbootstrap.IdentityRecoveryRequest) {
	b, r, _ := c.Inspection.session()
	b.HolderID, b.Generation = c.Lease.Holder, c.Lease.Generation
	return b, r
}

func parseClusterExistingIdentityRecoveryWork(w clusterExistingIdentityWork, operation, target string) (clusterExistingIdentityRecoveryClaim, error) {
	fail := func() (clusterExistingIdentityRecoveryClaim, error) {
		return clusterExistingIdentityRecoveryClaim{}, errClusterConflict
	}
	p, confirmation, err := parseClusterExistingIdentityPayload([]byte(w.claim.payload))
	if err != nil || confirmation == nil || p.OperationID != operation || confirmation.RequestID != target {
		return fail()
	}
	var result clusterExistingIdentityInspectionResult
	if len(w.inspection) > 32<<10 || json.Unmarshal([]byte(w.inspection), &result) != nil || result.Session.Binding == nil || w.inspection != marshalClusterJSON(result) {
		return fail()
	}
	b := result.Session.Binding
	i := clusterExistingIdentityInspectionClaim{Plan: p, Confirmation: *confirmation, Sealed: w.claim.Sealed, payload: w.claim.payload,
		Lease: clusterSSHTargetLease{OperationID: operation, TargetID: target, Holder: b.HolderID, Generation: b.Generation, ControllerHolder: p.Controller,
			OperationAttempt: 1, OperationKind: clusterExistingIdentityKind, OperationStep: clusterExistingIdentityConfirmed, Step: clusterExistingIdentityInspect}}
	i.Sealed.binding = confirmation.Binding
	if !i.valid() || !result.valid(i) || !w.matches(i) || w.inspectedAt < confirmation.ConfirmedAt {
		return fail()
	}
	l := i.Lease
	l.Holder, l.Generation, l.OperationStep, l.Step = w.holder, w.generation, clusterExistingIdentityRecover, clusterExistingIdentityRecover
	return clusterExistingIdentityRecoveryClaim{Inspection: i, Result: result, ObservedAt: w.inspectedAt, Lease: l, receipt: w.inspection}, nil
}

func readClusterExistingIdentityRecoveryRow(ctx context.Context, db clusterExistingIdentityWorkReader, operation, target string) (clusterExistingIdentityWork, error) {
	return readClusterExistingIdentityPhaseRow(ctx, db, operation, target, "running", clusterExistingIdentityRecover, clusterExistingIdentityRecover, clusterExistingIdentityRecovered)
}

func (w clusterExistingIdentityWork) matchesRecovery(c clusterExistingIdentityRecoveryClaim) bool {
	return w.matches(c.Inspection) && w.inspection == c.receipt && w.inspectedAt == c.ObservedAt && w.holder == c.Lease.Holder && w.generation == c.Lease.Generation
}

// Only original controller performs this transition, after complete repeated
// native proof and a fresh, exact supervised read-only inspection receipt.
func (c *clusterController) reserveClusterExistingIdentityMutation(parent context.Context, operation, target string, getJSON func(context.Context, string, any) error) error {
	if c == nil || c.store == nil || c.store.db == nil || getJSON == nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	w, err := readClusterExistingIdentityWork(ctx, c.store.db, operation, target)
	if err != nil || !w.claim.valid() || w.claim.Plan.Controller != c.holder || w.state != "prepared" || w.step != clusterExistingIdentityObserved || w.expires != 0 {
		return errClusterConflict
	}
	r, err := parseClusterExistingIdentityRecoveryWork(w, operation, target)
	if err != nil || r.ObservedAt > w.now || w.now-r.ObservedAt >= 300 {
		return errClusterConflict
	}
	snapshot, err := readClusterExistingIdentityWorkSnapshot(ctx, c.store.db, w.claim.Plan)
	if err != nil {
		return errClusterConflict
	}
	proof, err := readClusterExistingIdentitySourceCustody(ctx, getJSON, w.claim.Plan, r.Result.Source.Process)
	if err != nil || proof != r.Result.Source {
		return errClusterConflict
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	// Exclusive controller row precedes shared authority locks, preventing
	// concurrent transitions from upgrading operation locks in opposite order.
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present) != nil || lockClusterExistingIdentityWork(ctx, tx, w.claim) != nil {
		return errClusterConflict
	}
	current, err := readClusterExistingIdentityWorkRow(ctx, tx, operation, target)
	if err != nil || !current.matches(w.claim) || current.state != w.state || current.step != w.step || current.expires != 0 || current.holder != w.holder || current.generation != w.generation || current.inspection != w.inspection || current.inspectedAt != w.inspectedAt {
		return errClusterConflict
	}
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, c.holder, w.claim.Plan.MaintenanceID, operation)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='running',current_step=$2,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, operation, clusterExistingIdentityRecover); err != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET current_step=$2,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, target, clusterExistingIdentityRecover); err != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,$2,'existing_identity_recovery_reserved','running','Original controller reserved exact verified identity recovery.','{}',floor(extract(epoch FROM clock_timestamp()))::bigint)`, operation, w.claim.Plan.ClusterID); err != nil {
		return errClusterConflict
	}
	if checkClusterExistingIdentityWorkClock(ctx, tx, w.claim, false) != nil || tx.QueryRowContext(ctx, `SELECT 1 WHERE $1<=extract(epoch FROM clock_timestamp()) AND extract(epoch FROM clock_timestamp())<$1+300`, w.inspectedAt).Scan(&present) != nil || ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}

func (s *postgresOperatorStore) claimClusterExistingIdentityRecovery(ctx context.Context, operation, target, holder string, process clusterExistingIdentitySourceProcess) (clusterExistingIdentityRecoveryClaim, error) {
	fail := func() (clusterExistingIdentityRecoveryClaim, error) {
		return clusterExistingIdentityRecoveryClaim{}, errClusterConflict
	}
	if !clusterUUIDRE.MatchString(operation) || !clusterUUIDRE.MatchString(target) || !clusterUUIDRE.MatchString(holder) {
		return fail()
	}
	w, err := readClusterExistingIdentityRecoveryRow(ctx, s.db, operation, target)
	if err != nil {
		return fail()
	}
	c, err := parseClusterExistingIdentityRecoveryWork(w, operation, target)
	if err != nil || !process.valid(c.Inspection.Plan) {
		return fail()
	}
	snapshot, err := readClusterExistingIdentityWorkSnapshot(ctx, s.db, c.Inspection.Plan)
	if err != nil {
		return fail()
	}
	c.Lease.Holder, c.Lease.Generation = holder, w.generation+1
	if !c.valid() {
		return fail()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterExistingIdentityWork(ctx, tx, c.Inspection) != nil {
		return fail()
	}
	current, err := readClusterExistingIdentityRecoveryRow(ctx, tx, operation, target)
	if err != nil || !current.matches(c.Inspection) || current.inspection != c.receipt || current.inspectedAt != c.ObservedAt || current.step != clusterExistingIdentityRecover || current.generation != w.generation || current.holder != w.holder ||
		!((current.state == "prepared" && current.expires == 0 && current.holder == c.Inspection.Lease.Holder && current.generation == c.Inspection.Lease.Generation) ||
			((current.state == "running" || current.state == "recovery_required") && current.expires <= current.now && current.holder != "" && current.generation > c.Inspection.Lease.Generation)) {
		return fail()
	}
	p := c.Inspection.Plan
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, p.Controller, p.MaintenanceID, operation)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='running',lease_holder=$2,lease_generation=$3,
 lease_expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+$4,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, target, holder, c.Lease.Generation, clusterSSHTargetLeaseSeconds); err != nil {
		return fail()
	}
	clock := c.Inspection
	clock.Lease = c.Lease
	if checkClusterExistingIdentityWorkClock(ctx, tx, clock, true) != nil || ctx.Err() != nil || tx.Commit() != nil {
		return fail()
	}
	return c, nil
}

func (s *postgresOperatorStore) loadClusterExistingIdentityRecovery(ctx context.Context, c clusterExistingIdentityRecoveryClaim) (string, error) {
	if !c.valid() {
		return "", errClusterConflict
	}
	w, err := readClusterExistingIdentityRecoveryRow(ctx, s.db, c.Lease.OperationID, c.Lease.TargetID)
	if err != nil || !w.matchesRecovery(c) || w.state != "running" || w.step != clusterExistingIdentityRecover || w.expires <= w.now {
		return "", errClusterConflict
	}
	if _, err := readClusterExistingIdentityWorkSnapshot(ctx, s.db, c.Inspection.Plan); err != nil {
		return "", errClusterConflict
	}
	w, err = readClusterExistingIdentityRecoveryRow(ctx, s.db, c.Lease.OperationID, c.Lease.TargetID)
	if err != nil || !w.matchesRecovery(c) || w.state != "running" || w.step != clusterExistingIdentityRecover || w.expires <= w.now {
		return "", errClusterConflict
	}
	return clusterSSHPreparationDigest(struct {
		Payload, Receipt string
		Lease            clusterSSHTargetLease
	}{c.Inspection.payload, c.receipt, c.Lease})
}

func (s *postgresOperatorStore) renewClusterExistingIdentityRecovery(ctx context.Context, c clusterExistingIdentityRecoveryClaim) error {
	return s.updateClusterExistingIdentityRecovery(ctx, c, "renew")
}
func (s *postgresOperatorStore) completeClusterExistingIdentityRecovery(ctx context.Context, c clusterExistingIdentityRecoveryClaim) error {
	return s.updateClusterExistingIdentityRecovery(ctx, c, "complete")
}
func (s *postgresOperatorStore) requireClusterExistingIdentityRecovery(ctx context.Context, c clusterExistingIdentityRecoveryClaim) error {
	return s.updateClusterExistingIdentityRecovery(ctx, c, "require")
}

func (s *postgresOperatorStore) updateClusterExistingIdentityRecovery(ctx context.Context, c clusterExistingIdentityRecoveryClaim, action string) error {
	if !c.valid() || (action != "renew" && action != "complete" && action != "require") {
		return errClusterConflict
	}
	p := c.Inspection.Plan
	snapshot, err := readClusterExistingIdentityWorkSnapshot(ctx, s.db, p)
	if err != nil {
		return errClusterConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	if lockClusterExistingIdentityWork(ctx, tx, c.Inspection) != nil {
		return errClusterConflict
	}
	w, err := readClusterExistingIdentityRecoveryRow(ctx, tx, c.Lease.OperationID, c.Lease.TargetID)
	if err != nil || !w.matchesRecovery(c) {
		return errClusterConflict
	}
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, p.Controller, p.MaintenanceID, p.OperationID)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) {
		return errClusterConflict
	}
	clock := c.Inspection
	clock.Lease = c.Lease
	if action == "complete" && w.state == "prepared" && w.step == clusterExistingIdentityRecovered && w.expires == 0 ||
		action == "require" && w.state == "recovery_required" && w.step == clusterExistingIdentityRecover {
		if checkClusterExistingIdentityWorkClock(ctx, tx, clock, action == "require") != nil || ctx.Err() != nil || tx.Commit() != nil {
			return errClusterConflict
		}
		return nil
	}
	if w.state != "running" || w.step != clusterExistingIdentityRecover || checkClusterExistingIdentityWorkClock(ctx, tx, clock, true) != nil {
		return errClusterConflict
	}
	if action == "renew" {
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+$2 WHERE id=$1`, c.Lease.TargetID, clusterSSHTargetLeaseSeconds)
	} else {
		state, step, event := "recovery_required", clusterExistingIdentityRecover, "existing_identity_recovery_required"
		if action == "complete" {
			state, step, event = "prepared", clusterExistingIdentityRecovered, "existing_identity_recovered"
		}
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state=$2,current_step=$3,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, c.Lease.TargetID, state, step)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,$2,$3,'running','Identity recovery outcome recorded; consumers remain drained.','{}',floor(extract(epoch FROM clock_timestamp()))::bigint)`, p.OperationID, p.ClusterID, event)
		}
	}
	if err != nil || checkClusterExistingIdentityWorkClock(ctx, tx, clock, true) != nil {
		return errClusterConflict
	}
	if action == "complete" {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=0 WHERE id=$1`, c.Lease.TargetID); err != nil {
			return errClusterConflict
		}
	}
	if ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}
