package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

const clusterSSHQualificationStep = "qualify_ssh_targets"

type clusterSSHParentProgress struct {
	ClusterID                                          string
	Enabled, ActiveSize, DesiredSize, Members          int64
	Status, HMRState                                   string
	Targets, SafeTargets, Credentials, Complete, Fresh int64
	PendingAdmissions                                  int64
}

type clusterSSHParentReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Read every target, including missing credentials and unfinished inspections.
// A pending worker is ordinary progress, not an operation or quorum failure.
func readClusterSSHParentProgress(ctx context.Context, reader clusterSSHParentReader, operation clusterControllerOperation, holder string) (clusterSSHParentProgress, error) {
	var p clusterSSHParentProgress
	err := reader.QueryRowContext(ctx, `WITH moment AS (SELECT floor(extract(epoch FROM statement_timestamp()))::bigint AS now)
 SELECT c.cluster_id,c.enabled,c.active_size,c.desired_size,c.status,c.hmr_state,
  (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active'),
  (SELECT count(*) FROM engine.cluster_admissions WHERE cluster_id=c.cluster_id AND state IN ('Pending Quorum','Approved','Recovery Required')),
  count(t.id),
  count(t.id) FILTER (WHERE t.cluster_id=c.cluster_id AND t.operation_attempt=o.attempt
    AND t.state IN ('queued','running','recovery_required') AND t.current_step IN ('inspect','inspection_complete')),
  count(t.id) FILTER (WHERE t.credential_state='available' AND p.expires_at>moment.now AND a.id=1),
  count(t.id) FILTER (WHERE t.current_step='inspection_complete'),
  count(t.id) FILTER (WHERE t.current_step='inspection_complete' AND t.state='queued'
    AND t.lease_holder='' AND t.lease_expires_at=0 AND t.inspected_attempt=o.attempt
    AND t.inspected_generation=t.lease_generation AND t.inspected_generation>0
    AND t.inspected_at>moment.now-$6 AND t.inspected_at<=moment.now)
 FROM engine.cluster_operations o
 JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_application_leases l ON l.name=$5 AND l.holder=$2
 LEFT JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id
 LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 LEFT JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 CROSS JOIN moment
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.attempt=$3 AND o.current_step=$4
   AND l.expires_at>moment.now
 GROUP BY c.cluster_id,c.enabled,c.active_size,c.desired_size,c.status,c.hmr_state`, operation.ID, holder, operation.Attempt, operation.CurrentStep, clusterControllerLeaseName, clusterSSHInspectionLifetimeSeconds).
		Scan(&p.ClusterID, &p.Enabled, &p.ActiveSize, &p.DesiredSize, &p.Status, &p.HMRState, &p.Members, &p.PendingAdmissions,
			&p.Targets, &p.SafeTargets, &p.Credentials, &p.Complete, &p.Fresh)
	if errors.Is(err, sql.ErrNoRows) {
		return p, errClusterConflict
	}
	if err != nil {
		return p, errClusterUnavailable
	}
	return p, nil
}

func (p clusterSSHParentProgress) valid() error {
	expected, err := currentReleaseAdmissionBatchSize(p.ActiveSize, p.DesiredSize, p.Status)
	if err != nil || p.Enabled != 1 || !clusterUUIDRE.MatchString(p.ClusterID) || p.HMRState != "inactive" ||
		p.Members != p.ActiveSize || p.PendingAdmissions != 0 || p.Targets != int64(expected) || p.SafeTargets != p.Targets {
		return errClusterSSHCohort
	}
	if p.Credentials != p.Targets {
		return errClusterSSHCredentials
	}
	return nil
}

// This controller phase can only inspect. A waiting qualification boundary is
// not completion, operator approval, preparation authority or membership change.
func (c *clusterController) runSSHInspectionParent(parent context.Context, operation clusterControllerOperation, observe func(context.Context, string, any) error) error {
	if operation.Kind != "ssh_onboarding" || (operation.CurrentStep != "preflight" && operation.CurrentStep != clusterSSHInspectionOperationStep) {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	p, err := readClusterSSHParentProgress(ctx, c.store.db, operation, c.holder)
	if err != nil {
		return err
	}
	// Never release an operation containing an unknown/prepared/joined target.
	if p.SafeTargets != p.Targets {
		return errClusterSSHCohort
	}
	if err := p.valid(); err != nil {
		return c.transitionSSHInspectionParent(ctx, operation, "fail", nil, err)
	}
	if operation.CurrentStep == "preflight" {
		return c.transitionSSHInspectionParent(ctx, operation, "begin", nil, nil)
	}
	if p.Complete < p.Targets {
		return nil
	}
	if p.Fresh != p.Targets {
		return c.transitionSSHInspectionParent(ctx, operation, "fail", nil, errClusterSSHCohort)
	}
	cohort, source, err := c.store.assessClusterSSHInspectionCohort(ctx, operation.ID, c.holder, operation.Attempt, observe)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, errClusterUnavailable) || errors.Is(err, errClusterConflict) {
			return err
		}
		return c.transitionSSHInspectionParent(ctx, operation, "fail", nil, err)
	}
	proof := &clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	return c.transitionSSHInspectionParent(ctx, operation, "review", proof, nil)
}

type clusterSSHParentProof struct {
	Version int                        `json:"version"`
	Cohort  clusterSSHInspectionCohort `json:"cohort"`
	Source  clusterSSHSourceCohort     `json:"source"`
}

// Input encoding, hashes and public proof shaping finish before the transaction.
// Its exclusive controller lease serializes with every SSH worker transaction.
func (c *clusterController) transitionSSHInspectionParent(ctx context.Context, operation clusterControllerOperation, action string, proof *clusterSSHParentProof, cause error) error {
	if !textInSet(action, "begin", "review", "fail") ||
		(action == "begin" && operation.CurrentStep != "preflight") ||
		(action == "review" && operation.CurrentStep != clusterSSHInspectionOperationStep) ||
		!textInSet(operation.CurrentStep, "preflight", clusterSSHInspectionOperationStep) {
		return errClusterConflict
	}
	oldPayload := marshalClusterJSON(operation.Payload)
	payload := make(map[string]any, len(operation.Payload)+1)
	for key, value := range operation.Payload {
		payload[key] = value
	}
	var targets []clusterSSHInspectedTarget
	var reports []string
	var wireKeys []string
	if action == "review" {
		if proof == nil || proof.Version != 1 || proof.Cohort.OperationID != operation.ID || proof.Cohort.ControllerHolder != c.holder || proof.Cohort.Attempt != operation.Attempt ||
			validateClusterSSHInspectionCohort(proof.Cohort, proof.Source) != nil {
			return errClusterSSHCohort
		}
		raw, err := json.Marshal(proof)
		if err != nil || len(raw) > 128<<10 {
			return errClusterSSHCohort
		}
		digest := sha256.Sum256(raw)
		payload["ssh_inspection"] = map[string]any{"sha256": hex.EncodeToString(digest[:]), "proof": proof, "qualification_required": true}
		targets = append(targets, proof.Cohort.Targets...)
		sort.Slice(targets, func(i, j int) bool { return targets[i].Binding.TargetID < targets[j].Binding.TargetID })
		for _, target := range targets {
			raw, err := json.Marshal(target.Report)
			if err != nil {
				return errClusterSSHCohort
			}
			reports = append(reports, string(raw))
			wireKeys = append(wireKeys, base64.StdEncoding.EncodeToString(target.Key.PublicKey))
		}
	}
	newPayload := marshalClusterJSON(payload)
	state, step, event, message := "running", clusterSSHInspectionOperationStep, "ssh_inspection_started", "SSH target inspections started; host preparation is not authorized."
	errorText := ""
	if action == "review" {
		state, step, event, message = "waiting", clusterSSHQualificationStep, "ssh_inspection_complete", "SSH inspections complete. Further qualification and operator confirmation are required."
	} else if action == "fail" {
		state, step, event, message = "failed", operation.CurrentStep, "ssh_inspection_failed", "SSH inspection stopped without changing cluster membership or application state."
		errorText = errClusterSSHCohort.Error()
		for _, known := range []error{errClusterSSHCredentials, errClusterSSHCohortIdentity, errClusterSSHCohortNetwork, errClusterSSHCohortExisting, errClusterSSHCohortPlatform} {
			if errors.Is(cause, known) {
				errorText = known.Error()
			}
		}
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	var clusterID, activeID string
	if tx.QueryRowContext(ctx, `SELECT cluster_id,COALESCE(active_operation_id,'') FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&clusterID, &activeID) != nil || activeID != operation.ID {
		return errClusterConflict
	}
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 AND kind='ssh_onboarding' AND state='running'
 AND current_step=$2 AND attempt=$3 AND payload_json=$4 FOR UPDATE`, operation.ID, operation.CurrentStep, operation.Attempt, oldPayload).Scan(&present) != nil {
		return errClusterConflict
	}
	p, err := readClusterSSHParentProgress(ctx, tx, operation, c.holder)
	if err != nil || p.ClusterID != clusterID || p.SafeTargets != p.Targets || (action != "fail" && p.valid() != nil) {
		return errClusterConflict
	}
	if action == "review" {
		if p.Fresh != p.Targets || p.ActiveSize != proof.Source.ActiveSize || p.DesiredSize != proof.Source.DesiredSize ||
			p.Status != proof.Source.Status || p.HMRState != proof.Source.HMRState || p.ClusterID != proof.Source.ClusterID {
			return errClusterSSHCohort
		}
		// Acquire all credentials in target-ID order before reading target proof.
		// Cleanup uses the same order; Aegis reset must first acquire its state row.
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.aegis_cipher_state WHERE id=1 FOR SHARE`).Scan(&present) != nil {
			return errClusterSSHCredentials
		}
		for _, target := range targets {
			if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_credentials p JOIN engine.aegis_cipher_state a
 ON a.id=1 AND a.verification_token=p.aegis_generation WHERE p.target_id=$1 AND p.expires_at>extract(epoch FROM clock_timestamp()) FOR SHARE OF p`, target.Binding.TargetID).Scan(&present) != nil {
				return errClusterSSHCredentials
			}
		}
		for i, target := range targets {
			if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets WHERE id=$1 AND operation_id=$2 AND cluster_id=$3
 AND management_ip=$4 AND ssh_port=$5 AND host_key_algorithm=$6 AND host_key_fingerprint=$7 AND ordinal=$8 AND host_key_base64=$14
 AND operation_attempt=$9 AND lease_generation=$10 AND inspected_generation=$10 AND inspected_attempt=$9
 AND inspected_at=$11 AND inspected_at>extract(epoch FROM clock_timestamp())-$13 AND inspection_json=$12
 AND current_step='inspection_complete' AND state='queued' AND credential_state='available' AND lease_holder='' AND lease_expires_at=0
 FOR SHARE`, target.Binding.TargetID, operation.ID, clusterID, target.Binding.Address, target.Binding.Port,
				target.Key.Algorithm, target.Binding.Fingerprint, target.Ordinal, operation.Attempt, target.Generation, target.InspectedAt, reports[i], clusterSSHInspectionLifetimeSeconds, wireKeys[i]).Scan(&present) != nil {
				return errClusterSSHCohort
			}
		}
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 AND control_plane_vip=$1 AND edge_vip=$2`, proof.Source.ControlPlaneVIP, proof.Source.EdgeVIP).Scan(&present) != nil {
			return errClusterSSHCohort
		}
		for _, member := range proof.Source.Members {
			if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2 AND management_ip=$3 AND membership_state='Active' FOR SHARE`, member.NodeID, member.Name, member.Address).Scan(&present) != nil {
				return errClusterSSHCohort
			}
		}
	}
	var now int64
	if tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint`).Scan(&now) != nil {
		return errClusterUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state=$1,current_step=$2,payload_json=$3,error_text=$4,
 finished_at=CASE WHEN $1='failed' THEN $5 ELSE finished_at END,updated_at=$5 WHERE id=$6`, state, step, newPayload, nullClusterString(errorText), now, operation.ID); err != nil {
		return errClusterUnavailable
	}
	if action == "fail" {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='failed',credential_state='required',
 lease_holder='',lease_generation=lease_generation+1,lease_expires_at=0,updated_at=$2 WHERE operation_id=$1`, operation.ID, now); err != nil {
			return errClusterUnavailable
		}
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL,updated_at=$2 WHERE id=1 AND active_operation_id=$1`, operation.ID, now); err != nil {
			return errClusterUnavailable
		}
	}
	if insertClusterEvent(ctx, tx, operation.ID, "", clusterID, event, state, message, map[string]any{"step": step, "attempt": operation.Attempt}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	if action == "fail" {
		// Connection has returned before cleanup starts its own short transaction.
		return c.store.cleanupClusterSSHCredentials(ctx)
	}
	return nil
}

// Only the implemented read-only phase may release target reservations. Future
// prepared/joined states need their own cleanup/removal proof before cancellation.
func cancelClusterSSHInspectionTargets(ctx context.Context, tx *sql.Tx, operationID, step string) error {
	if !textInSet(step, "preflight", clusterSSHInspectionOperationStep, clusterSSHQualificationStep) {
		return errClusterConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT state,current_step FROM engine.cluster_onboarding_targets WHERE operation_id=$1 FOR UPDATE`, operationID)
	if err != nil {
		return errClusterUnavailable
	}
	safe, count := true, 0
	for rows.Next() {
		var state, targetStep string
		if err := rows.Scan(&state, &targetStep); err != nil {
			rows.Close()
			return errClusterUnavailable
		}
		count++
		safe = safe && textInSet(state, "queued", "running", "recovery_required") && textInSet(targetStep, "inspect", "inspection_complete")
	}
	scanErr, closeErr := rows.Err(), rows.Close()
	if scanErr != nil || closeErr != nil {
		return errClusterUnavailable
	}
	if !safe || count > 2 {
		return errClusterConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='cancelled',credential_state='required',
 lease_holder='',lease_generation=lease_generation+1,lease_expires_at=0,updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint
 WHERE operation_id=$1`, operationID); err != nil {
		return errClusterUnavailable
	}
	return nil
}
