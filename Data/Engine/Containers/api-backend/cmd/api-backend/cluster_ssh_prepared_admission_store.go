package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Later admission phases retain the original settled targets and source queue.
// Every loader also compares this phase with the locked operation row; a caller
// cannot relabel preparation, create a worker claim or change the operation kind.
func clusterSSHPreparedAuthorityPhase(step string) bool {
	return step == clusterSSHPreparationOperationStep || step == "apply_membership" || step == "verify_quorum"
}

// Private transaction inputs, never a receipt or a durable admission capability.
// All parsing, encoding and credential hashing finish before BeginTx.
type clusterSSHPreparedStoreBinding struct {
	authority   clusterSSHPreparedAuthority
	operation   clusterControllerOperation
	holder      string
	targets     []clusterSSHInspectedTarget
	reports     map[string]string
	keys        map[string]string
	credentials [][2]string
}

func (clusterSSHPreparedStoreBinding) String() string { return "prepared SSH store binding [redacted]" }
func (clusterSSHPreparedStoreBinding) GoString() string {
	return "prepared SSH store binding [redacted]"
}

func readClusterSSHPreparedStoreBinding(ctx context.Context, db *sql.DB, a clusterSSHPreparedAuthority,
	operation clusterControllerOperation, holder string) (clusterSSHPreparedStoreBinding, error) {
	fail := func() (clusterSSHPreparedStoreBinding, error) {
		return clusterSSHPreparedStoreBinding{}, errClusterConflict
	}
	if db == nil || ctx.Err() != nil {
		return fail()
	}
	// Reconstruct owned nested data from the exact retained payload, rather than
	// trusting a caller's mutable receipt, target list or credential digest.
	owned, err := parseClusterSSHPreparedAuthority(a.payload, a.baseline, a.queue, operation, holder)
	if err != nil || !reflect.DeepEqual(owned, a) {
		return fail()
	}
	b := clusterSSHPreparedStoreBinding{authority: owned, operation: operation, holder: holder,
		targets: slices.Clone(owned.proof.Cohort.Targets), reports: map[string]string{}, keys: map[string]string{},
		credentials: make([][2]string, len(owned.proof.Cohort.Targets))}
	// The generic operation payload is not authority and must not be retained.
	b.operation.Payload = nil
	slices.SortFunc(b.targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	for i, target := range owned.proof.Cohort.Targets {
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return fail()
		}
		b.reports[target.Binding.TargetID], b.keys[target.Binding.TargetID] = string(raw), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
		if db.QueryRowContext(ctx, `SELECT aegis_generation,ciphertext FROM engine.cluster_onboarding_credentials
 WHERE target_id=$1 AND octet_length(aegis_generation) BETWEEN 1 AND 16384 AND octet_length(ciphertext) BETWEEN 1 AND 262144`,
			target.Binding.TargetID).Scan(&b.credentials[i][0], &b.credentials[i][1]) != nil {
			return fail()
		}
		digest, err := clusterSSHPreparationDigest([]string{b.credentials[i][0], b.credentials[i][1]})
		if err != nil || digest != owned.authorization.Targets[i].CredentialSHA256 || ctx.Err() != nil {
			return fail()
		}
	}
	return b, nil
}

// Same controller -> cluster -> operation -> Aegis -> sorted credentials ->
// targets -> source member order serves both read-only custody and transition.
func (b clusterSSHPreparedStoreBinding) lock(ctx context.Context, tx *sql.Tx) (int64, error) {
	a, operation := b.authority, b.operation
	if lockClusterSSHQualification(ctx, tx, operation.ID, b.targets) != nil ||
		checkClusterSSHQualificationSource(ctx, tx, clusterSSHQualificationWork{Proof: a.proof, Source: a.queue}) != nil {
		return 0, errClusterConflict
	}
	for i, target := range a.proof.Cohort.Targets {
		var n int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets t
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE t.id=$1 AND t.operation_id=$2 AND t.cluster_id=$3 AND t.operation_attempt=$4
 AND t.state='prepared' AND t.current_step=$5 AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=$6
 AND t.credential_state='available' AND t.management_ip=$7 AND t.ssh_port=$8 AND t.host_key_algorithm=$9 AND t.host_key_fingerprint=$10
 AND t.host_key_base64=$11 AND t.ordinal=$12 AND t.inspected_attempt=$4 AND t.inspected_generation=$13 AND t.inspected_at=$14 AND t.inspection_json=$15
 AND p.aegis_generation=$16 AND p.ciphertext=$17`,
			target.Binding.TargetID, operation.ID, a.queue.ClusterID, operation.Attempt, clusterSSHPreparationCompleteStep,
			a.receipt.Targets[i].Generation, target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint,
			b.keys[target.Binding.TargetID], target.Ordinal, target.Generation, target.InspectedAt, b.reports[target.Binding.TargetID],
			b.credentials[i][0], b.credentials[i][1]).Scan(&n) != nil {
			return 0, errClusterConflict
		}
	}
	return b.clock(ctx, tx, operation.CurrentStep, 0)
}

// A final database clock follows all waits, including event insertion. The
// optional proof instant uses the existing 60-second membership proof ceiling;
// it never renews the caller's enclosing proof/commit deadline.
func (b clusterSSHPreparedStoreBinding) clock(ctx context.Context, tx *sql.Tx, step string, observed int64) (int64, error) {
	var now int64
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED (SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT floor(moment.now)::bigint FROM moment,engine.cluster_operations o,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND l.name=$5 AND l.holder=$6 AND l.expires_at>moment.now
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$7
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=o.id AND (p.expires_at>moment.now AND t.inspected_at<=moment.now) IS NOT TRUE)
 AND ($8::bigint=0 OR ($8::bigint<=moment.now AND $8::bigint>moment.now-60))`,
		b.operation.ID, step, b.operation.Attempt, b.authority.payload, clusterControllerLeaseName, b.holder, len(b.targets), observed).Scan(&now) != nil || ctx.Err() != nil {
		return 0, errClusterConflict
	}
	return now, nil
}

func clusterSSHPreparedAdmissionObservationMatches(operation clusterControllerOperation, holder string,
	a clusterSSHPreparedAuthority, observed clusterSSHPreparedObservation) bool {
	payloadHash := sha256.Sum256([]byte(a.payload))
	if operation.Kind != "ssh_onboarding" || operation.State != "running" || operation.CurrentStep != clusterSSHPreparationOperationStep ||
		observed.OperationID != operation.ID || observed.Controller != holder || observed.Attempt != operation.Attempt || observed.ObservedAt <= 0 ||
		observed.PayloadSHA256 != hex.EncodeToString(payloadHash[:]) || observed.ClusterID != a.queue.ClusterID ||
		observed.KubeSystemUID != a.proof.Source.KubeSystemUID || len(observed.Nodes) != len(a.receipt.Targets) || len(observed.Nodes) < 1 || len(observed.Nodes) > 2 {
		return false
	}
	for _, target := range a.receipt.Targets {
		if target.Prejoin == nil || observed.Nodes[target.TargetID] != target.Prejoin.Receipt.Node {
			return false
		}
	}
	return true
}

// Controller orchestration calls this only after withClusterSSHPreparedAdmissionProof
// returns successfully, including its final native reads and joined cleanup.
// Copy Prepared in the consumer; do not transition from inside that callback.
// Reuse the same bounded outer context for proof and commit. No quorum-ready
// receipt is persisted, and this store method cannot manufacture native proof.
func (c *clusterController) beginClusterSSHPreparedAdmission(ctx context.Context, operation clusterControllerOperation,
	original clusterSSHPreparedAuthority, observed clusterSSHPreparedObservation) error {
	deadline, bounded := ctx.Deadline()
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || !bounded || time.Until(deadline) > 60*time.Second ||
		!clusterSSHPreparedAdmissionObservationMatches(operation, c.holder, original, observed) {
		return errClusterConflict
	}
	b, err := readClusterSSHPreparedStoreBinding(ctx, c.store.db, original, operation, c.holder)
	if err != nil {
		return errClusterConflict
	}
	// Encode public event metadata before acquiring a transaction connection.
	details := marshalClusterJSON(map[string]any{"attempt": operation.Attempt, "step": "apply_membership"})
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterConflict
	}
	defer tx.Rollback()
	if _, err := b.lock(ctx, tx); err != nil {
		return errClusterConflict
	}
	now, err := b.clock(ctx, tx, clusterSSHPreparationOperationStep, observed.ObservedAt)
	if err != nil {
		return errClusterConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step='apply_membership',updated_at=$2
 WHERE id=$1 AND kind='ssh_onboarding' AND state='running' AND current_step=$3 AND attempt=$4 AND payload_json=$5`,
		operation.ID, now, clusterSSHPreparationOperationStep, operation.Attempt, original.payload)
	if err != nil {
		return errClusterConflict
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events
 (operation_id,cluster_id,event_type,state,message,details_json,created_at) VALUES($1,$2,$3,'running',$4,$5,$6)`,
		operation.ID, original.queue.ClusterID, "ssh_prepared_admission_started", "Prepared cohort entered admission.", details, now); err != nil {
		return errClusterConflict
	}
	if _, err := b.clock(ctx, tx, "apply_membership", observed.ObservedAt); err != nil || c.holder != b.holder || ctx.Err() != nil || tx.Commit() != nil {
		return errClusterConflict
	}
	return nil
}
