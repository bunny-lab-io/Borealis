package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"database/sql"
	"encoding/base64"
	"reflect"
	"strings"
)

// This lease permits only verify_identity_recovery. It never satisfies the
// clean-host preparation or mutating identity-recovery lease validators.
type clusterExistingIdentityInspectionClaim struct {
	Plan         clusterExistingIdentityPlan
	Confirmation clusterExistingIdentityConfirmation
	Lease        clusterSSHTargetLease
	Sealed       sealedClusterSSHCredentials
	payload      string
}

func (c clusterExistingIdentityInspectionClaim) valid() bool {
	l := c.Lease
	digest, err := clusterSSHPreparationDigest([]string{c.Sealed.generation, c.Sealed.ciphertext})
	return c.Plan.valid() && c.Confirmation.valid(c.Plan) && err == nil && digest == c.Confirmation.CredentialSHA256 &&
		c.Sealed.binding == c.Confirmation.Binding && c.Sealed.generation != "" && len(c.Sealed.generation) <= 16<<10 &&
		strings.HasPrefix(c.Sealed.ciphertext, aegisEnvelopePrefix) && len(c.Sealed.ciphertext) <= 256<<10 &&
		l.OperationID == c.Plan.OperationID && l.TargetID == c.Confirmation.RequestID && l.ControllerHolder == c.Plan.Controller &&
		clusterUUIDRE.MatchString(l.Holder) && l.Holder != "00000000-0000-0000-0000-000000000000" && l.Generation > 0 && l.Generation < 1<<53 &&
		l.OperationAttempt == 1 && l.OperationKind == clusterExistingIdentityKind && l.OperationStep == clusterExistingIdentityConfirmed && l.Step == clusterExistingIdentityInspect &&
		c.payload == marshalClusterJSON(clusterExistingIdentityConfirmedPayload{Version: 2, Plan: c.Plan, Confirmation: c.Confirmation})
}

func (c clusterExistingIdentityInspectionClaim) session() (clusterbootstrap.SessionBinding, clusterbootstrap.IdentityRecoveryRequest, clusterremote.HostKey) {
	key, _ := base64.StdEncoding.Strict().DecodeString(c.Confirmation.HostKeyBase64)
	b := c.Confirmation.Binding
	binding := clusterbootstrap.SessionBinding{ClusterID: b.ClusterID, OperationID: b.OperationID, TargetID: b.TargetID,
		HolderID: c.Lease.Holder, Generation: c.Lease.Generation, OperationAttempt: 1, Address: b.Address, Port: b.Port,
		HostKeyAlgorithm: c.Confirmation.HostKeyAlgorithm, HostKeyFingerprint: b.Fingerprint}
	recovery := clusterbootstrap.IdentityRecoveryRequest{SourceID: c.Plan.SourceID}
	for _, m := range c.Plan.Members {
		if m.NodeID == c.Plan.SourceID {
			recovery.SourceNodeUID = m.NodeUID
		}
		if m.NodeID == c.Plan.TargetID {
			binding.Hostname, binding.MachineID, recovery.TargetNodeUID = m.Name, m.MachineID, m.NodeUID
		}
	}
	return binding, recovery, clusterremote.HostKey{Algorithm: c.Confirmation.HostKeyAlgorithm, Fingerprint: b.Fingerprint, PublicKey: key}
}

type clusterExistingIdentityWork struct {
	claim                                                 clusterExistingIdentityInspectionClaim
	state, holder, inspection, step                       string
	generation, expires, now, inspectedAt                 int64
	cluster, member, address, algorithm, key, fingerprint string
	port                                                  int
}

type clusterExistingIdentityWorkReader interface {
	clusterExistingIdentityReader
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Read bounded scalar rows only; parse/validate envelopes after connection
// release. This helper has no crypto, SSH or Kubernetes side effects.
func readClusterExistingIdentityWork(ctx context.Context, db clusterExistingIdentityWorkReader, operation, target string) (clusterExistingIdentityWork, error) {
	w, err := readClusterExistingIdentityWorkRow(ctx, db, operation, target)
	if err != nil {
		return w, err
	}
	p, receipt, err := parseClusterExistingIdentityPayload([]byte(w.claim.payload))
	if err != nil || receipt == nil || p.OperationID != operation || receipt.RequestID != target {
		return w, errClusterConflict
	}
	w.claim.Plan, w.claim.Confirmation, w.claim.Sealed.binding = p, *receipt, receipt.Binding
	w.claim.Lease = clusterSSHTargetLease{OperationID: operation, TargetID: target, Holder: w.holder, Generation: w.generation, ControllerHolder: p.Controller,
		OperationAttempt: 1, OperationKind: clusterExistingIdentityKind, OperationStep: clusterExistingIdentityConfirmed, Step: clusterExistingIdentityInspect}
	if !w.matches(w.claim) {
		return w, errClusterConflict
	}
	return w, nil
}

func (w clusterExistingIdentityWork) matches(c clusterExistingIdentityInspectionClaim) bool {
	return w.claim.payload == c.payload && w.cluster == c.Plan.ClusterID && w.member == c.Plan.TargetID &&
		w.address == c.Confirmation.Binding.Address && w.port == c.Confirmation.Binding.Port && w.algorithm == c.Confirmation.HostKeyAlgorithm &&
		w.key == c.Confirmation.HostKeyBase64 && w.fingerprint == c.Confirmation.Binding.Fingerprint &&
		w.claim.Sealed.generation == c.Sealed.generation && w.claim.Sealed.ciphertext == c.Sealed.ciphertext
}

func readClusterExistingIdentityWorkRow(ctx context.Context, db clusterExistingIdentityWorkReader, operation, target string) (clusterExistingIdentityWork, error) {
	var w clusterExistingIdentityWork
	err := db.QueryRowContext(ctx, `SELECT o.payload_json,c.cluster_id,o.target_node_id,t.management_ip,t.ssh_port,t.host_key_algorithm,t.host_key_base64,t.host_key_fingerprint,
 p.aegis_generation,p.ciphertext,t.state,t.lease_holder,t.lease_generation,t.lease_expires_at,t.inspected_at,t.inspection_json,t.current_step,
 floor(extract(epoch FROM clock_timestamp()))::bigint
 FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id AND t.cluster_id=c.cluster_id AND t.operation_attempt=o.attempt
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 JOIN engine.cluster_application_leases l ON l.name=$3 AND l.expires_at>extract(epoch FROM clock_timestamp())
 WHERE o.id=$1 AND t.id=$2 AND o.kind=$4 AND o.state='waiting' AND o.current_step=$5 AND o.attempt=1
 AND t.current_step IN ($6,$7) AND t.credential_state='available' AND t.ordinal=1
 AND p.expires_at>extract(epoch FROM clock_timestamp()) AND octet_length(o.payload_json)<=65536
 AND octet_length(p.aegis_generation)<=16384 AND octet_length(p.ciphertext)<=262144
 AND octet_length(t.inspection_json)<=32768
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets other WHERE other.operation_id=o.id AND other.id<>t.id)`,
		operation, target, clusterControllerLeaseName, clusterExistingIdentityKind, clusterExistingIdentityConfirmed, clusterExistingIdentityInspect, clusterExistingIdentityObserved).
		Scan(&w.claim.payload, &w.cluster, &w.member, &w.address, &w.port, &w.algorithm, &w.key, &w.fingerprint,
			&w.claim.Sealed.generation, &w.claim.Sealed.ciphertext, &w.state, &w.holder, &w.generation, &w.expires, &w.inspectedAt, &w.inspection, &w.step, &w.now)
	if err != nil {
		return w, errClusterConflict
	}
	return w, nil
}

func lockClusterExistingIdentityWork(ctx context.Context, tx *sql.Tx, c clusterExistingIdentityInspectionClaim) error {
	for _, q := range []struct {
		query string
		args  []any
	}{
		{`SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR SHARE`, []any{clusterControllerLeaseName}},
		{`SELECT 1 FROM engine.cluster_state WHERE id=1 FOR SHARE`, nil},
		{`SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR SHARE`, []any{c.Plan.OperationID}},
		{`SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR SHARE`, []any{c.Plan.MaintenanceID}},
	} {
		var present int
		if tx.QueryRowContext(ctx, q.query, q.args...).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	for _, m := range c.Plan.Members {
		var present int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 FOR SHARE`, m.NodeID).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	for _, q := range []struct {
		query string
		args  []any
	}{
		{`SELECT 1 FROM engine.aegis_cipher_state WHERE id=1 FOR SHARE`, nil},
		{`SELECT 1 FROM engine.cluster_onboarding_credentials WHERE target_id=$1 FOR SHARE`, []any{c.Confirmation.RequestID}},
		{`SELECT 1 FROM engine.cluster_onboarding_targets WHERE id=$1 FOR UPDATE`, []any{c.Confirmation.RequestID}},
	} {
		var present int
		if tx.QueryRowContext(ctx, q.query, q.args...).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	return nil
}

func readClusterExistingIdentityWorkSnapshot(ctx context.Context, db clusterExistingIdentityReader, p clusterExistingIdentityPlan) (clusterExistingIdentitySnapshot, error) {
	snapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, db, p.Controller, p.MaintenanceID, p.OperationID)
	if err != nil || !snapshot.valid(p.SourceID) {
		return snapshot, errClusterConflict
	}
	digest, err := clusterSSHPreparationDigest(snapshot)
	if err != nil || digest != p.SnapshotSHA256 {
		return snapshot, errClusterConflict
	}
	return snapshot, nil
}

// Target-row serialization permits one contender. Expired read-only inspections
// may retry at a new generation; no mutation phase is accepted here.
func (s *postgresOperatorStore) claimClusterExistingIdentityInspection(ctx context.Context, operation, target, holder string, process clusterExistingIdentitySourceProcess) (clusterExistingIdentityInspectionClaim, error) {
	fail := func() (clusterExistingIdentityInspectionClaim, error) {
		return clusterExistingIdentityInspectionClaim{}, errClusterConflict
	}
	if s == nil || s.db == nil || !clusterUUIDRE.MatchString(operation) || !clusterUUIDRE.MatchString(target) || !clusterUUIDRE.MatchString(holder) || holder == "00000000-0000-0000-0000-000000000000" {
		return fail()
	}
	w, err := readClusterExistingIdentityWork(ctx, s.db, operation, target)
	if err != nil || !process.valid(w.claim.Plan) {
		return fail()
	}
	snapshot, err := readClusterExistingIdentityWorkSnapshot(ctx, s.db, w.claim.Plan)
	if err != nil {
		return fail()
	}
	c := w.claim
	c.Lease.Holder, c.Lease.Generation = holder, w.generation+1
	if !c.valid() {
		return fail()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterExistingIdentityWork(ctx, tx, c) != nil {
		return fail()
	}
	current, err := readClusterExistingIdentityWorkRow(ctx, tx, operation, target)
	if err != nil || !current.matches(w.claim) || current.step != clusterExistingIdentityInspect || w.holder != current.holder || w.generation != current.generation || w.state != current.state || w.inspection != current.inspection || current.inspectedAt != 0 ||
		!((current.state == "queued" && current.holder == "" && current.expires == 0 && current.generation == 0) ||
			(current.state == "running" && current.holder != "" && current.expires <= current.now)) {
		return fail()
	}
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, c.Plan.Controller, c.Plan.MaintenanceID, operation)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) {
		return fail()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='running',lease_holder=$2,lease_generation=$3,
 lease_expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+$4,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, target, holder, c.Lease.Generation, clusterSSHTargetLeaseSeconds); err != nil {
		return fail()
	}
	if checkClusterExistingIdentityWorkClock(ctx, tx, c, true) != nil || ctx.Err() != nil || tx.Commit() != nil {
		return fail()
	}
	return c, nil
}

func checkClusterExistingIdentityWorkClock(ctx context.Context, tx *sql.Tx, c clusterExistingIdentityInspectionClaim, live bool) error {
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases l,engine.cluster_onboarding_credentials p,engine.cluster_onboarding_targets t
 WHERE l.name=$1 AND l.holder=$2 AND l.expires_at>extract(epoch FROM clock_timestamp()) AND p.target_id=$3 AND p.expires_at>extract(epoch FROM clock_timestamp())
 AND t.id=p.target_id AND t.lease_holder=$4 AND t.lease_generation=$5 AND (NOT $6 OR t.lease_expires_at>extract(epoch FROM clock_timestamp()))`,
		clusterControllerLeaseName, c.Plan.Controller, c.Lease.TargetID, c.Lease.Holder, c.Lease.Generation, live).Scan(&present) != nil {
		return errClusterConflict
	}
	return nil
}

// Authority reads return only a digest; callers cannot log credential envelopes.
func (s *postgresOperatorStore) loadClusterExistingIdentityInspection(ctx context.Context, c clusterExistingIdentityInspectionClaim) (string, error) {
	if !c.valid() {
		return "", errClusterConflict
	}
	w, err := readClusterExistingIdentityWork(ctx, s.db, c.Lease.OperationID, c.Lease.TargetID)
	if err != nil || !reflect.DeepEqual(w.claim, c) || w.step != clusterExistingIdentityInspect || w.state != "running" || w.expires <= w.now || w.inspectedAt != 0 {
		return "", errClusterConflict
	}
	if _, err := readClusterExistingIdentityWorkSnapshot(ctx, s.db, c.Plan); err != nil {
		return "", errClusterConflict
	}
	w, err = readClusterExistingIdentityWorkRow(ctx, s.db, c.Lease.OperationID, c.Lease.TargetID)
	if err != nil || !w.matches(c) || w.holder != c.Lease.Holder || w.generation != c.Lease.Generation || w.step != clusterExistingIdentityInspect || w.state != "running" || w.expires <= w.now || w.inspectedAt != 0 {
		return "", errClusterConflict
	}
	return clusterSSHPreparationDigest(struct {
		Payload string
		Lease   clusterSSHTargetLease
	}{c.payload, c.Lease})
}

func (s *postgresOperatorStore) renewClusterExistingIdentityInspection(ctx context.Context, c clusterExistingIdentityInspectionClaim) error {
	if !c.valid() {
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
	if err != nil || !w.matches(c) || w.holder != c.Lease.Holder || w.generation != c.Lease.Generation || w.step != clusterExistingIdentityInspect || w.state != "running" || w.expires <= w.now || w.inspectedAt != 0 {
		return errClusterConflict
	}
	nowSnapshot, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, c.Plan.Controller, c.Plan.MaintenanceID, c.Plan.OperationID)
	if err != nil || !reflect.DeepEqual(snapshot, nowSnapshot) || checkClusterExistingIdentityWorkClock(ctx, tx, c, true) != nil {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+$2,
 updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, c.Lease.TargetID, clusterSSHTargetLeaseSeconds); err != nil {
		return errClusterConflict
	}
	if checkClusterExistingIdentityWorkClock(ctx, tx, c, true) != nil || ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}
