package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
)

func (s *postgresOperatorStore) clusterSSHPreparationProposal(ctx context.Context, id string) (clusterSSHPreparationApprovalSnapshot, int64, error) {
	if s == nil || s.db == nil || !clusterUUIDRE.MatchString(id) || ctx.Err() != nil {
		return clusterSSHPreparationApprovalSnapshot{}, 0, errClusterConflict
	}
	op := clusterControllerOperation{ID: id, Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	var owner string
	if s.db.QueryRowContext(ctx, `SELECT o.attempt,l.holder FROM engine.cluster_operations o,
 engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step=$2
 AND c.id=1 AND c.active_operation_id=o.id AND l.name=$3 AND l.expires_at>extract(epoch FROM clock_timestamp())`,
		id, clusterSSHQualificationStep, clusterControllerLeaseName).Scan(&op.Attempt, &owner) != nil {
		return clusterSSHPreparationApprovalSnapshot{}, 0, errClusterConflict
	}
	return s.readClusterSSHPreparationProposal(ctx, op, owner)
}

// Prepare comparison bytes before SQL; the returned closure does SQL work only.
func clusterSSHPreparationProposalCheck(snapshot clusterSSHPreparationApprovalSnapshot) (func(context.Context, *sql.Tx) error, error) {
	w := snapshot.work
	if len(w.Proof.Cohort.Targets) < 1 || len(w.Proof.Cohort.Targets) > 2 ||
		len(snapshot.credentials) != len(w.Proof.Cohort.Targets) || len(w.Authorization.Targets) != len(w.Proof.Cohort.Targets) {
		return nil, errClusterConflict
	}
	targets := slices.Clone(w.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys, generations, credentials := map[string]string{}, map[string]string{}, map[string]int64{}, map[string]sealedClusterSSHCredentials{}
	for i, target := range w.Proof.Cohort.Targets {
		credential, reserved := snapshot.credentials[i], w.Authorization.Targets[i]
		digest, err := clusterSSHPreparationDigest([]string{credential.generation, credential.ciphertext})
		if err != nil || reserved.TargetID != target.Binding.TargetID || credential.binding != target.Binding || digest != reserved.CredentialSHA256 {
			return nil, errClusterConflict
		}
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return nil, errClusterConflict
		}
		reports[target.Binding.TargetID], keys[target.Binding.TargetID] = string(raw), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
		generations[target.Binding.TargetID], credentials[target.Binding.TargetID] = reserved.QualificationGeneration, credential
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		if lockClusterSSHQualification(ctx, tx, w.Authorization.OperationID, targets) != nil ||
			checkClusterSSHQualificationSource(ctx, tx, clusterSSHQualificationWork{Proof: w.Proof, Source: w.Source}) != nil {
			return errClusterConflict
		}
		for _, target := range targets {
			credential := credentials[target.Binding.TargetID]
			var present int
			if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets t,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a
 WHERE t.id=$1 AND t.operation_id=$2 AND t.operation_attempt=$3 AND t.cluster_id=$4
 AND t.state='queued' AND t.current_step='qualification_complete' AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=$5
 AND t.inspected_generation=$6 AND t.inspected_attempt=$3 AND t.inspected_at=$7 AND t.inspection_json=$8
 AND t.management_ip=$9 AND t.ssh_port=$10 AND t.host_key_algorithm=$11 AND t.host_key_fingerprint=$12 AND t.host_key_base64=$13 AND t.ordinal=$14
 AND t.credential_state='available' AND p.target_id=t.id AND p.aegis_generation=$15 AND p.ciphertext=$16
 AND a.id=1 AND a.verification_token=p.aegis_generation`, target.Binding.TargetID, w.Authorization.OperationID, w.Authorization.Attempt,
				target.Binding.ClusterID, generations[target.Binding.TargetID], target.Generation, target.InspectedAt, reports[target.Binding.TargetID],
				target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint, keys[target.Binding.TargetID], target.Ordinal,
				credential.generation, credential.ciphertext).Scan(&present) != nil {
				return errClusterConflict
			}
		}
		return nil
	}, nil
}

// The Admin API records confirmation intent, never readiness or worker leases.
// Exact retries return the original receipt without extending its timestamp.
// A changed request or proposal conflicts; fresh inspection is required after
// expiry. Original controller must still consume every live proof to approve.
func (s *postgresOperatorStore) requestClusterSSHPreparation(ctx context.Context, id, actor string, raw []byte) (clusterSSHPreparationConfirmation, error) {
	fail := func() (clusterSSHPreparationConfirmation, error) {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	snapshot, now, err := s.clusterSSHPreparationProposal(ctx, id)
	if err != nil {
		return fail()
	}
	w := snapshot.work
	confirmation, err := newClusterSSHPreparationConfirmation(raw, actor, now, w.Authorization)
	if err != nil {
		return fail()
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(w.Payload), &payload) != nil {
		return fail()
	}
	qualification, err := parseClusterSSHQualificationReport(string(payload["ssh_qualification"]), w.Authorization.Attempt)
	if err != nil || !completedClusterSSHQualification(qualification) {
		return fail()
	}
	replay := len(payload["ssh_preparation_request"]) != 0
	if replay {
		stored, err := parseClusterSSHPreparationRequest(payload["ssh_preparation_request"], w.Authorization)
		if err != nil || stored.RequestID != confirmation.RequestID || stored.Actor != confirmation.Actor ||
			stored.PlanSHA256 != confirmation.PlanSHA256 || stored.ConfirmedAt > now || now-stored.ConfirmedAt >= clusterSSHInspectionLifetimeSeconds {
			return fail()
		}
		confirmation = stored
	}
	payload["ssh_preparation_request"], err = json.Marshal(confirmation)
	if err != nil {
		return fail()
	}
	updated, err := json.Marshal(payload)
	if err != nil || len(updated) > 256<<10 {
		return fail()
	}
	check, err := clusterSSHPreparationProposalCheck(snapshot)
	if err != nil {
		return fail()
	}
	details := marshalClusterJSON(map[string]any{"request_id": confirmation.RequestID, "plan_sha256": confirmation.PlanSHA256, "actor": confirmation.Actor})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if check(ctx, tx) != nil {
		return fail()
	}
	// A final fresh DB clock follows all row-lock waits. SQL still compares the
	// original payload on replay, so concurrent writes cannot be acknowledged.
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_operations o SET payload_json=$2,updated_at=CASE WHEN $12 THEN o.updated_at ELSE floor(moment.now) END
 FROM moment,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step=$3 AND o.attempt=$4 AND o.payload_json=$5
 AND c.id=1 AND c.active_operation_id=o.id AND l.name=$6 AND l.holder=$7 AND l.expires_at>moment.now
 AND $8::bigint>moment.now-$9 AND $8::bigint<=moment.now
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$10
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=o.id AND (t.inspected_at>moment.now-$9 AND t.inspected_at<=moment.now AND p.expires_at>moment.now) IS NOT TRUE)
 AND $11::bigint>moment.now-$9 AND $11::bigint<=moment.now
 RETURNING floor(moment.now)::bigint`, id, string(updated), clusterSSHQualificationStep, w.Authorization.Attempt, w.Payload,
		clusterControllerLeaseName, w.Authorization.ControllerHolder, confirmation.ConfirmedAt, clusterSSHInspectionLifetimeSeconds,
		len(w.Proof.Cohort.Targets), qualification.ObservedAt, replay).Scan(&now) != nil || ctx.Err() != nil {
		return fail()
	}
	if !replay {
		if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,admission_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,NULL,$2,'ssh_preparation_requested','waiting',$3,$4,$5)`, id, w.Source.ClusterID,
			"Preparation confirmation recorded; controller readiness approval remains required.", details, now); err != nil {
			return fail()
		}
	}
	if tx.Commit() != nil {
		return fail()
	}
	return confirmation, nil
}
