package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"
)

const (
	sshPreparationReturned      = "ssh_preparation_read_only_returned"
	sshPreparationReadOnlyClaim = "ssh_preparation_read_only_claimed"
	sshPreparationAllocated     = "ssh_preparation_allocation_started"
	sshPreparationStopped       = "Source inventory stopped before installation. Temporary access was released. Inspect both hosts and confirm a fresh operation."
	sshPreparationInspect       = "Preparation interrupted after work may have started. Verify original worker exit, both target journals and owned scratch before reconciling this operation."
	sshPreparationAccess        = "Preparation access expired. Verify the original worker and both host journals with fresh authorized access before reconciling. The old operation cannot reuse expired credentials."
	sshPreparationOwner         = "Preparation owner changed. Reconciliation requires the original Engine node and verified worker custody."
)

func sshPreparationFenceDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// The existing append-only operation journal binds both sides of the allocation
// barrier to one attempt. A legacy executor never writes the read-only claim.
func insertSSHPreparationFenceEvent(ctx context.Context, tx *sql.Tx, op string, attempt int64, holder, kind, payloadDigest string) error {
	message := "Joining worker claimed the approved targets for read-only source inventory."
	if kind == sshPreparationAllocated {
		message = "Source inventory passed. The joining worker may now allocate installation resources."
	}
	return insertClusterEvent(ctx, tx, op, "", "", kind, "running", message, map[string]any{
		"attempt": attempt, "worker_sha256": sshPreparationFenceDigest(holder), "payload_sha256": payloadDigest,
	}, time.Now().Unix())
}

// Both settlement and this one-shot permission serialize on the same operation
// row. After a lost commit reply the worker stops; absence of an acknowledgement
// can never be interpreted as absence of installation effects.
func (s *postgresOperatorStore) openSSHPreparationAllocation(ctx context.Context, claims []clusterSSHNetworkTargetClaim) error {
	if len(claims) == 0 || len(claims) > 2 {
		return errClusterConflict
	}
	first := claims[0].Lease
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	var payloadDigest string
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2 FOR SHARE`,
		clusterControllerLeaseName, first.ControllerHolder).Scan(&one); err != nil {
		return errClusterConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 AND active_operation_id=$1 FOR SHARE`, first.OperationID).Scan(&one); err != nil {
		return errClusterConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT encode(sha256(convert_to(payload_json,'UTF8')),'hex') FROM engine.cluster_operations WHERE id=$1 AND kind='ssh_onboarding' AND state='running'
 AND current_step='prepare_ssh_targets' AND attempt=$2 AND NOT(payload_json::jsonb ? 'ssh_preparation')
 AND payload_json::jsonb #>> '{ssh_preparation_authorization,controller_holder}'=$3 FOR UPDATE`,
		first.OperationID, first.OperationAttempt, first.ControllerHolder).Scan(&payloadDigest); err != nil {
		return errClusterConflict
	}
	seen := make(map[string]bool, len(claims))
	for _, claim := range claims {
		l := claim.Lease
		if !validClusterSSHPreparationLease(l) || l.OperationID != first.OperationID || l.OperationAttempt != first.OperationAttempt ||
			l.ControllerHolder != first.ControllerHolder || l.Holder != first.Holder || seen[l.TargetID] {
			return errClusterConflict
		}
		seen[l.TargetID] = true
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets t
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE t.operation_id=$1 AND t.id=$2 AND t.lease_holder=$3 AND t.lease_generation=$4
 AND t.state='running' AND t.current_step='stage_source' AND t.credential_state='available'
 AND p.ciphertext=$5 AND p.aegis_generation=$6 FOR SHARE OF a,p,t`, l.OperationID, l.TargetID, l.Holder, l.Generation,
			claim.Sealed.ciphertext, claim.Sealed.generation).Scan(&one); err != nil {
			return errClusterConflict
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT 1 WHERE
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1)=$2
 AND (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$3 AND details_json::jsonb->>'attempt'=$4
 AND details_json::jsonb->>'worker_sha256'=$5 AND details_json::jsonb->>'payload_sha256'=$7)=1
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$6)`,
		first.OperationID, len(claims), sshPreparationReadOnlyClaim, strconv.FormatInt(first.OperationAttempt, 10), sshPreparationFenceDigest(first.Holder), sshPreparationAllocated, payloadDigest).Scan(&one); err != nil {
		return errClusterConflict
	}
	if err := insertSSHPreparationFenceEvent(ctx, tx, first.OperationID, first.OperationAttempt, first.Holder, sshPreparationAllocated, payloadDigest); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases l WHERE l.name=$1 AND l.holder=$2
 AND l.expires_at>extract(epoch FROM clock_timestamp()) AND NOT EXISTS(
 SELECT 1 FROM engine.cluster_onboarding_targets t JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=$3 AND (t.lease_expires_at<=extract(epoch FROM clock_timestamp()) OR p.expires_at<=extract(epoch FROM clock_timestamp())))`,
		clusterControllerLeaseName, first.ControllerHolder, first.OperationID).Scan(&one); err != nil {
		return errClusterConflict
	}
	return tx.Commit()
}

// Only the original API dispatch calls this after every scope returned, its
// private scratch was removed, and the native target callback was never entered.
// Recording a return conveys no authority to execute or reissue credentials.
func (s *postgresOperatorStore) recordSSHPreparationReadOnlyReturn(ctx context.Context, lease clusterSSHTargetLease) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 AND attempt=$2 AND state='running'
 AND current_step='prepare_ssh_targets' FOR UPDATE`, lease.OperationID, lease.OperationAttempt).Scan(&one); err != nil {
		return errClusterConflict
	}
	// A second statement takes a fresh snapshot after the operation lock.
	var returned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$2
 AND details_json::jsonb->>'worker_sha256'=$3)`, lease.OperationID, sshPreparationReturned, sshPreparationFenceDigest(lease.Holder)).Scan(&returned); err != nil {
		return err
	}
	if returned {
		return tx.Commit()
	}
	// Keep diagnosis inside the committed return receipt, after scratch cleanup.
	// It is supplemental evidence and never participates in a fence predicate.
	evidence := map[string]any{"installation_effects": "none", "last_checkpoint": "read_only_worker_returned_after_cleanup"}
	if rejection := clusterSSHPrejoinRejectionFor(ctx); rejection != nil {
		evidence["rejection"] = rejection
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,event_type,state,message,details_json,created_at)
 SELECT operation_id,$3,'running','Source work ended and its temporary files were removed before target installation.',(details_json::jsonb || jsonb_build_object('diagnostic',$6::jsonb))::text,
 floor(extract(epoch FROM clock_timestamp())) FROM engine.cluster_operation_events
 WHERE operation_id=$1 AND event_type=$4 AND details_json::jsonb->>'worker_sha256'=$5 AND details_json::jsonb->>'attempt'=$2`,
		lease.OperationID, strconv.FormatInt(lease.OperationAttempt, 10), sshPreparationReturned, sshPreparationAllocated, sshPreparationFenceDigest(lease.Holder), string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errClusterConflict
	}
	return tx.Commit()
}

func (c *clusterController) reconcileSSHPreparationInterruption(parent context.Context, op clusterControllerOperation) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	// Retain the original, stronger native recovery reservation for legacy
	// claims. Fenced claims with uncertain effects retain their active operation.
	var fenced bool
	if err := c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM engine.cluster_operation_events
 WHERE operation_id=$1 AND event_type=$2)`, op.ID, sshPreparationReadOnlyClaim).Scan(&fenced); err != nil {
		return err
	}
	if !fenced {
		if _, err := c.reserveClusterSSHPreparationRecovery(ctx, op); err == nil {
			return nil
		}
	}
	return c.settleInterruptedSSHInventory(ctx, op)
}

func (c *clusterController) settleInterruptedSSHInventory(ctx context.Context, op clusterControllerOperation) error {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	var cluster string
	if err := tx.QueryRowContext(ctx, `SELECT cluster_id FROM engine.cluster_state WHERE id=1 AND active_operation_id=$1 FOR UPDATE`, op.ID).Scan(&cluster); err != nil {
		return errClusterConflict
	}
	var payload, reason string
	if err := tx.QueryRowContext(ctx, `SELECT payload_json,coalesce(error_text,'') FROM engine.cluster_operations WHERE id=$1 AND attempt=$2
 AND state='running' AND kind='ssh_onboarding' AND current_step='prepare_ssh_targets' AND NOT(payload_json::jsonb ? 'ssh_preparation')
 FOR UPDATE`, op.ID, op.Attempt).Scan(&payload, &reason); err != nil {
		return errClusterConflict
	}
	var original struct {
		Approval     clusterSSHPreparationAuthorization `json:"ssh_preparation_authorization"`
		Confirmation struct {
			Identity struct {
				Node string `json:"source_node_id"`
			} `json:"identity_source"`
		} `json:"ssh_preparation_confirmation"`
	}
	if json.Unmarshal([]byte(payload), &original) != nil || original.Approval.OperationID != op.ID || original.Approval.Attempt != op.Attempt ||
		len(original.Approval.Targets) < 1 || len(original.Approval.Targets) > 2 {
		return errClusterConflict
	}
	var claimDigest string
	err = tx.QueryRowContext(ctx, `SELECT details_json::jsonb->>'worker_sha256' FROM engine.cluster_operation_events
 WHERE operation_id=$1 AND details_json::jsonb->>'attempt'=$2 AND event_type=$3 AND details_json::jsonb->>'payload_sha256'=$4`, op.ID, strconv.FormatInt(op.Attempt, 10), sshPreparationReadOnlyClaim, sshPreparationFenceDigest(payload)).Scan(&claimDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var allocated, returned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$2),
 EXISTS(SELECT 1 FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$3
 AND details_json::jsonb->>'attempt'=$4 AND details_json::jsonb->>'worker_sha256'=$5)`,
		op.ID, sshPreparationAllocated, sshPreparationReturned, strconv.FormatInt(op.Attempt, 10), claimDigest).Scan(&allocated, &returned); err != nil {
		return err
	}
	safe := validClusterSSHPreparationDigest(claimDigest) && (!allocated || returned)
	block := sshPreparationInspect
	// Credential cleanup locks credential rows before changing target rows.
	if _, err := tx.ExecContext(ctx, `SELECT p.target_id FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id
 WHERE t.operation_id=$1 ORDER BY p.target_id FOR UPDATE OF p`, op.ID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1`, op.ID).Scan(&count); err != nil || count != len(original.Approval.Targets) {
		return errClusterConflict
	}
	for i, target := range original.Approval.Targets {
		var holder, access string
		var generation int64
		err := tx.QueryRowContext(ctx, `SELECT lease_holder,credential_state,lease_generation FROM engine.cluster_onboarding_targets
 WHERE operation_id=$1 AND id=$2 AND operation_attempt=$3 AND cluster_id=$4 AND ordinal=$5 AND state='running' AND current_step='stage_source'
 AND lease_expires_at<=extract(epoch FROM clock_timestamp()) FOR UPDATE`, op.ID, target.TargetID, op.Attempt, cluster, i+1).Scan(&holder, &access, &generation)
		if err != nil {
			return errClusterConflict
		}
		switch access {
		case "available":
			if holder == "" || generation != target.QualificationGeneration+1 {
				return errClusterConflict
			}
			safe = safe && sshPreparationFenceDigest(holder) == claimDigest
		case "required":
			if holder != "" || generation != target.QualificationGeneration+2 {
				return errClusterConflict
			}
			block = sshPreparationAccess
		default:
			return errClusterConflict
		}
	}
	if original.Approval.ControllerHolder != c.holder {
		var onSource bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2
 AND membership_state='Active' AND application_state='active')`, original.Confirmation.Identity.Node, os.Getenv("BOREALIS_ENGINE_NODE_NAME")).Scan(&onSource); err != nil {
			return err
		}
		if !onSource {
			safe = false
			block = sshPreparationOwner
		}
	}
	now := time.Now().Unix()
	if safe {
		if _, err = tx.ExecContext(ctx, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id IN(SELECT id FROM engine.cluster_onboarding_targets WHERE operation_id=$1)`, op.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='failed',current_step='source_inventory_stopped',credential_state='required',
 lease_holder='',lease_expires_at=0,lease_generation=lease_generation+1,updated_at=$2 WHERE operation_id=$1`, op.ID, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='failed',current_step='source_inventory_stopped',error_text=$2,finished_at=$3,updated_at=$3 WHERE id=$1`, op.ID, sshPreparationStopped, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL,updated_at=$2 WHERE id=1 AND active_operation_id=$1`, op.ID, now); err != nil {
			return err
		}
		if err = insertClusterEvent(ctx, tx, op.ID, "", cluster, "operation_failed", "failed", sshPreparationStopped, map[string]any{"attempt": op.Attempt}, now); err != nil {
			return err
		}
		if err = insertClusterAudit(ctx, tx, c.holder, "ssh_preparation_inventory_failed", op.ID, "failed", map[string]any{"attempt": op.Attempt}, now); err != nil {
			return err
		}
	} else if reason != block {
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET error_text=$2,updated_at=$3 WHERE id=$1`, op.ID, block, now); err != nil {
			return err
		}
		if err = insertClusterEvent(ctx, tx, op.ID, "", cluster, "ssh_preparation_reconciliation_blocked", "running", block, map[string]any{"attempt": op.Attempt}, now); err != nil {
			return err
		}
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2 AND expires_at>extract(epoch FROM clock_timestamp())`, clusterControllerLeaseName, c.holder).Scan(&live); err != nil {
		return errClusterControllerLeaseLost
	}
	return tx.Commit()
}
