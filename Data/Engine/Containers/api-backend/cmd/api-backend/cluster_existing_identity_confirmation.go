package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

const clusterExistingIdentityConfirmed = "identity_recovery_confirmed"
const clusterExistingIdentityInspect = "inspect_existing_identity"
const clusterExistingIdentityPlanLifetime = int64(300)

type clusterExistingIdentityConfirmation struct {
	Version                         int
	RequestID, PlanSHA256, Actor    string
	ConfirmedAt                     int64
	Binding                         clusterSSHCredentialBinding
	HostKeyAlgorithm, HostKeyBase64 string
	CredentialSHA256                string
}

type clusterExistingIdentityConfirmedPayload struct {
	Version      int
	Plan         clusterExistingIdentityPlan
	Confirmation clusterExistingIdentityConfirmation
}

func (c clusterExistingIdentityConfirmation) valid(p clusterExistingIdentityPlan) bool {
	digest, err := clusterSSHPreparationDigest(p)
	keyBytes, keyErr := base64.StdEncoding.Strict().DecodeString(c.HostKeyBase64)
	key := clusterremote.HostKey{Algorithm: c.HostKeyAlgorithm, Fingerprint: c.Binding.Fingerprint, PublicKey: keyBytes}
	if !p.valid() || err != nil || keyErr != nil || key.Validate() != nil || c.Version != 1 || !clusterUUIDRE.MatchString(c.RequestID) ||
		c.RequestID != c.Binding.TargetID || c.RequestID == "00000000-0000-0000-0000-000000000000" || c.RequestID == p.OperationID || c.RequestID == p.SourceID || c.RequestID == p.TargetID ||
		c.PlanSHA256 != digest || !validClusterExistingIdentityActor(c.Actor) || c.ConfirmedAt < p.ObservedAt || c.ConfirmedAt-p.ObservedAt >= clusterExistingIdentityPlanLifetime ||
		!c.Binding.valid() || c.Binding.OperationID != p.OperationID || c.Binding.ClusterID != p.ClusterID || !validClusterSSHPreparationDigest(c.CredentialSHA256) {
		return false
	}
	for _, m := range p.Members {
		if m.NodeID == p.TargetID {
			return c.Binding.Address == m.Address
		}
	}
	return false
}

func parseClusterExistingIdentityPayload(raw []byte) (clusterExistingIdentityPlan, *clusterExistingIdentityConfirmation, error) {
	if p, err := parseClusterExistingIdentityPlan(raw); err == nil {
		return p, nil, nil
	}
	var stored clusterExistingIdentityConfirmedPayload
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || json.Unmarshal(raw, &stored) != nil || stored.Version != 2 || !stored.Confirmation.valid(stored.Plan) {
		return clusterExistingIdentityPlan{}, nil, errClusterConflict
	}
	canonical, err := json.Marshal(stored)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return clusterExistingIdentityPlan{}, nil, errClusterConflict
	}
	return stored.Plan, &stored.Confirmation, nil
}

// Intent plus pinned credentials can authorize only a later read-only target
// observation. No worker claim or mutation proof is created by confirmation.
func (s *postgresOperatorStore) confirmClusterExistingIdentity(ctx context.Context, actor string, plan clusterExistingIdentityPlan, digest string, target clusterSSHPlannedTarget) (clusterExistingIdentityConfirmation, error) {
	fail := func() (clusterExistingIdentityConfirmation, error) {
		return clusterExistingIdentityConfirmation{}, errClusterConflict
	}
	if s == nil || s.db == nil || !plan.valid() || !validClusterExistingIdentityActor(actor) || validateClusterSSHPlannedTargets([]clusterSSHPlannedTarget{target}, 1) != nil {
		return fail()
	}
	snapshot, now, err := readClusterExistingIdentitySnapshotForOperation(ctx, s.db, plan.Controller, plan.MaintenanceID, plan.OperationID)
	if err != nil || !snapshot.valid(plan.SourceID) {
		return fail()
	}
	snapshotDigest, err := clusterSSHPreparationDigest(snapshot)
	if err != nil || snapshotDigest != plan.SnapshotSHA256 {
		return fail()
	}
	credentialDigest, err := clusterSSHPreparationDigest([]string{target.Sealed.generation, target.Sealed.ciphertext})
	if err != nil {
		return fail()
	}
	receipt := clusterExistingIdentityConfirmation{Version: 1, RequestID: target.Binding.TargetID, PlanSHA256: digest, Actor: actor, ConfirmedAt: now,
		Binding: target.Binding, HostKeyAlgorithm: target.Key.Algorithm, HostKeyBase64: target.KeyBase64, CredentialSHA256: credentialDigest}
	if !receipt.valid(plan) {
		return fail()
	}
	original, err := json.Marshal(plan)
	if err != nil {
		return fail()
	}
	payload, err := json.Marshal(clusterExistingIdentityConfirmedPayload{Version: 2, Plan: plan, Confirmation: receipt})
	if err != nil || len(payload) > 64<<10 {
		return fail()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&present) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 AND kind=$2 AND state='waiting' AND current_step=$3 AND attempt=1 AND payload_json=$4 AND target_node_id=$5 FOR UPDATE`,
			plan.OperationID, clusterExistingIdentityKind, clusterExistingIdentityReserved, string(original), plan.TargetID).Scan(&present) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR SHARE`, plan.MaintenanceID).Scan(&present) != nil {
		return fail()
	}
	for _, member := range plan.Members {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 FOR SHARE`, member.NodeID).Scan(&present) != nil {
			return fail()
		}
	}
	current, _, err := readClusterExistingIdentitySnapshotForOperation(ctx, tx, plan.Controller, plan.MaintenanceID, plan.OperationID)
	if err != nil || !reflect.DeepEqual(snapshot, current) {
		return fail()
	}
	// Lock Aegis before inserting any target; its generation and a fresh clock
	// are checked again after waits. Existing helper owns ciphertext-only storage.
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.aegis_cipher_state WHERE id=1 AND verification_token=$1 FOR SHARE`, target.Sealed.generation).Scan(&present) != nil {
		return fail()
	}
	var conflicts int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE state NOT IN ('completed','failed','cancelled') AND (management_ip=$1 OR host_key_fingerprint=$2)`, target.Binding.Address, target.Binding.Fingerprint).Scan(&conflicts) != nil || conflicts != 0 {
		return fail()
	}
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2 AND expires_at>extract(epoch FROM clock_timestamp())
 AND $3<=extract(epoch FROM clock_timestamp()) AND extract(epoch FROM clock_timestamp())<$3+$4`, clusterControllerLeaseName, plan.Controller, plan.ObservedAt, clusterExistingIdentityPlanLifetime).Scan(&present) != nil {
		return fail()
	}
	if insertClusterSSHPlannedTargets(ctx, tx, plan.OperationID, plan.ClusterID, []clusterSSHPlannedTarget{target}) != nil {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET current_step=$1 WHERE id=$2`, clusterExistingIdentityInspect, target.Binding.TargetID); err != nil {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$1,payload_json=$2,updated_at=$3 WHERE id=$4`, clusterExistingIdentityConfirmed, string(payload), now, plan.OperationID); err != nil {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,$2,'existing_identity_confirmed','waiting','Exact identity recovery plan confirmed; live proof remains required.','{}',$3)`, plan.OperationID, plan.ClusterID, now); err != nil {
		return fail()
	}
	// Target/unique-index insertion can itself wait. Repeat expiry after every
	// write before releasing the durable fence; rollback removes partial rows.
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2 AND expires_at>extract(epoch FROM clock_timestamp())
 AND $3<=extract(epoch FROM clock_timestamp()) AND extract(epoch FROM clock_timestamp())<$3+$4`, clusterControllerLeaseName, plan.Controller, plan.ObservedAt, clusterExistingIdentityPlanLifetime).Scan(&present) != nil {
		return fail()
	}
	if ctx.Err() != nil || tx.Commit() != nil {
		return fail()
	}
	return receipt, nil
}

// Cancel only an unclaimed read-only target. Any lease or phase transition
// requires dedicated execution reconciliation, never generic cancellation.
func cancelClusterExistingIdentityConfirmation(ctx context.Context, tx *sql.Tx, operation string) error {
	var id string
	rows, err := tx.QueryContext(ctx, `SELECT p.target_id FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=$1 ORDER BY p.target_id FOR UPDATE OF p`, operation)
	if err != nil {
		return errClusterConflict
	}
	for rows.Next() {
		var unused string
		if rows.Scan(&unused) != nil {
			rows.Close()
			return errClusterConflict
		}
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		return errClusterConflict
	}
	if tx.QueryRowContext(ctx, `SELECT id FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND state='queued' AND current_step=$2
 AND lease_holder='' AND lease_expires_at=0 AND inspected_at=0 AND operation_attempt=1 FOR UPDATE`, operation, clusterExistingIdentityInspect).Scan(&id) != nil {
		return errClusterConflict
	}
	var count int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1`, operation).Scan(&count) != nil || count != 1 {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='cancelled',current_step='cancelled',updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, id); err != nil {
		return errClusterConflict
	}
	return nil
}
