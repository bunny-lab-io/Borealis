package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
)

type clusterSSHQualificationWork struct {
	Claims   []clusterSSHNetworkTargetClaim
	Baseline clusterbootstrap.Expected
	Payload  string
	Proof    clusterSSHParentProof
	Source   clusterSSHQueueSource
}

func (clusterSSHQualificationWork) String() string   { return "SSH qualification work [redacted]" }
func (clusterSSHQualificationWork) GoString() string { return "SSH qualification work [redacted]" }

// A waiting parent retains sole-controller ownership. Its durable inspection
// proof permits only this separate read-only queue, never preparation claiming.
func (s *postgresOperatorStore) pendingClusterSSHQualification(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT o.id FROM engine.cluster_operations o
 JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 WHERE o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets'
 AND EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id
 AND t.current_step IN ('inspection_complete','qualify') AND t.credential_state='available'
 AND t.lease_expires_at<=extract(epoch FROM clock_timestamp())) LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", errClusterUnavailable
	}
	return id, nil
}

// Exclusive parent lock serializes whole-cohort acquisition/completion. Keep
// controller -> cluster -> operation -> Aegis -> sorted credentials -> targets
// order, with no parsing, crypto or network work inside the transaction.
func lockClusterSSHQualification(ctx context.Context, tx *sql.Tx, operationID string, targets []clusterSSHInspectedTarget) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR SHARE`, []any{clusterControllerLeaseName}},
		{`SELECT 1 FROM engine.cluster_state WHERE id=1 FOR SHARE`, nil},
		{`SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, []any{operationID}},
		{`SELECT 1 FROM engine.aegis_cipher_state WHERE id=1 FOR SHARE`, nil},
	} {
		var n int
		if tx.QueryRowContext(ctx, q.sql, q.args...).Scan(&n) != nil {
			return errClusterConflict
		}
	}
	for _, t := range targets {
		var n int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_credentials WHERE target_id=$1 FOR SHARE`, t.Binding.TargetID).Scan(&n) != nil {
			return errClusterSSHCredentials
		}
	}
	for _, t := range targets {
		var n int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets WHERE id=$1 FOR UPDATE`, t.Binding.TargetID).Scan(&n) != nil {
			return errClusterConflict
		}
	}
	return nil
}

// Membership mutations serialize on cluster_state. Lock the retained members
// as well, then compare the exact released source snapshot before any commit.
func checkClusterSSHQualificationSource(ctx context.Context, tx *sql.Tx, work clusterSSHQualificationWork) error {
	members := slices.Clone(work.Proof.Source.Members)
	slices.SortFunc(members, func(a, b clusterSSHSourceMember) int { return strings.Compare(a.NodeID, b.NodeID) })
	for _, member := range members {
		var n int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2 AND management_ip=$3
 AND membership_state='Active' AND application_state='active' FOR SHARE`, member.NodeID, member.Name, member.Address).Scan(&n) != nil {
			return errClusterConflict
		}
	}
	current, err := readClusterSSHQueueSource(ctx, tx)
	if err != nil || current != work.Source {
		return errClusterConflict
	}
	return nil
}

func (s *postgresOperatorStore) claimClusterSSHQualification(ctx context.Context, operationID, holder string) (clusterSSHQualificationWork, error) {
	fail := func() (clusterSSHQualificationWork, error) { return clusterSSHQualificationWork{}, errClusterConflict }
	if !clusterUUIDRE.MatchString(operationID) || !clusterUUIDRE.MatchString(holder) {
		return fail()
	}
	var work clusterSSHQualificationWork
	var controller string
	var attempt int64
	if s.db.QueryRowContext(ctx, `SELECT o.payload_json,o.attempt,l.holder,c.baseline_release,c.baseline_sha
 FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_application_leases l ON l.name=$2 AND l.expires_at>extract(epoch FROM clock_timestamp())
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets'
 AND octet_length(o.payload_json)<=262144`, operationID, clusterControllerLeaseName).Scan(&work.Payload, &attempt, &controller, &work.Baseline.Release, &work.Baseline.SourceSHA) != nil {
		return fail()
	}
	work.Baseline.Repository = clusterGitHubRepo()
	work.Baseline.AllowQualification = strings.Contains(work.Baseline.Release, "-rc.")
	anchor := clusterSSHTargetLease{OperationID: operationID, Holder: holder, ControllerHolder: controller, OperationAttempt: attempt, OperationKind: "ssh_onboarding", OperationStep: clusterSSHQualificationStep, Step: "qualify"}
	var err error
	var version string
	work.Proof, version, err = parseClusterSSHPreparationInspection([]byte(work.Payload), anchor, work.Baseline)
	if err != nil {
		return fail()
	}
	work.Source, err = readClusterSSHQueueSource(ctx, s.db)
	if err != nil {
		return fail()
	}
	// Validate source shape/configuration only after the connection is released.
	source := work.Source
	if source.ActiveOperationID != operationID || source.ClusterID != work.Proof.Source.ClusterID ||
		source.ActiveSize != work.Proof.Source.ActiveSize || source.DesiredSize != work.Proof.Source.DesiredSize ||
		source.Status != work.Proof.Source.Status || source.HMRState != work.Proof.Source.HMRState ||
		source.ControlVIP != work.Proof.Source.ControlPlaneVIP || source.EdgeVIP != work.Proof.Source.EdgeVIP ||
		source.Release != work.Baseline.Release || source.SHA != work.Baseline.SourceSHA {
		return fail()
	}
	source.ActiveOperationID = ""
	var configured string
	count, sourceErr := source.validate()
	if sourceErr != nil || count != len(work.Proof.Cohort.Targets) ||
		clusterBootstrapObject([]byte(source.ConfigJSON), map[string]any{"k3s_version": &configured}) != nil || configured != version {
		return fail()
	}
	targets := slices.Clone(work.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys := map[string]string{}, map[string]string{}
	for _, t := range targets {
		raw, err := json.Marshal(t.Report)
		if err != nil {
			return fail()
		}
		reports[t.Binding.TargetID] = string(raw)
		keys[t.Binding.TargetID] = base64.StdEncoding.EncodeToString(t.Key.PublicKey)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, operationID, targets) != nil || checkClusterSSHQualificationSource(ctx, tx, work) != nil {
		return fail()
	}
	var n int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets' AND o.attempt=$2 AND o.payload_json=$3
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$4 AND c.baseline_release=$5 AND c.baseline_sha=$6
 AND l.name=$7 AND l.holder=$8 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$9`, operationID, attempt, work.Payload, work.Proof.Source.ClusterID, work.Baseline.Release, work.Baseline.SourceSHA, clusterControllerLeaseName, controller, len(targets)).Scan(&n) != nil {
		return fail()
	}
	claims := map[string]clusterSSHNetworkTargetClaim{}
	for _, t := range targets {
		claim := clusterSSHNetworkTargetClaim{Lease: anchor}
		claim.Lease.TargetID = t.Binding.TargetID
		claim.Sealed.binding = t.Binding
		if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_onboarding_targets t SET state='running',current_step='qualify',lease_holder=$2,
 lease_generation=t.lease_generation+1,lease_expires_at=floor(moment.now)+$3,updated_at=floor(moment.now)
 FROM moment,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a
 WHERE t.id=$1 AND t.operation_id=$4 AND t.cluster_id=$5 AND t.operation_attempt=$6
 AND t.management_ip=$7 AND t.ssh_port=$8 AND t.host_key_algorithm=$9 AND t.host_key_fingerprint=$10 AND t.host_key_base64=$11 AND t.ordinal=$12
 AND t.inspected_attempt=$6 AND t.inspected_generation=$13 AND t.inspected_at=$14 AND t.inspection_json=$15
 AND t.inspected_at>moment.now-$16 AND t.inspected_at<=moment.now AND t.credential_state='available'
 AND ((t.state='queued' AND t.current_step='inspection_complete' AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=t.inspected_generation)
 OR(t.state='running' AND t.current_step='qualify' AND t.lease_expires_at<=moment.now AND t.lease_generation>t.inspected_generation))
 AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
 RETURNING t.lease_generation,p.aegis_generation,p.ciphertext`, t.Binding.TargetID, holder, clusterSSHTargetLeaseSeconds, operationID, t.Binding.ClusterID, attempt, t.Binding.Address, t.Binding.Port, t.Key.Algorithm, t.Key.Fingerprint, keys[t.Binding.TargetID], t.Ordinal, t.Generation, t.InspectedAt, reports[t.Binding.TargetID], clusterSSHInspectionLifetimeSeconds).Scan(&claim.Lease.Generation, &claim.Sealed.generation, &claim.Sealed.ciphertext) != nil {
			return fail()
		}
		claims[t.Binding.TargetID] = claim
	}
	if tx.Commit() != nil {
		return fail()
	}
	for _, target := range work.Proof.Cohort.Targets {
		work.Claims = append(work.Claims, claims[target.Binding.TargetID])
	}
	return work, nil
}

func (s *postgresOperatorStore) completeClusterSSHQualification(ctx context.Context, work clusterSSHQualificationWork, report clusterSSHQualificationReport) error {
	if len(work.Claims) != len(work.Proof.Cohort.Targets) || len(work.Claims) < 1 || len(work.Claims) > 2 || !report.valid(work.Proof.Cohort.Attempt) {
		return errClusterConflict
	}
	payload := map[string]json.RawMessage{}
	if len(report.Storage) > 0 {
		if len(report.Storage) != len(work.Claims) {
			return errClusterConflict
		}
		for i, target := range report.Storage {
			if target.TargetID != work.Claims[i].Lease.TargetID {
				return errClusterConflict
			}
		}
	}
	if json.Unmarshal([]byte(work.Payload), &payload) != nil {
		return errClusterConflict
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > 16<<10 {
		return errClusterConflict
	}
	payload["ssh_qualification"] = raw
	updated, err := json.Marshal(payload)
	if err != nil || len(updated) > 256<<10 {
		return errClusterConflict
	}
	targets := slices.Clone(work.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys := map[string]string{}, map[string]string{}
	for _, target := range targets {
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return errClusterConflict
		}
		reports[target.Binding.TargetID] = string(raw)
		keys[target.Binding.TargetID] = base64.StdEncoding.EncodeToString(target.Key.PublicKey)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	operation := work.Claims[0].Lease
	if lockClusterSSHQualification(ctx, tx, operation.OperationID, targets) != nil || checkClusterSSHQualificationSource(ctx, tx, work) != nil {
		return errClusterConflict
	}
	var now int64
	if tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets' AND o.attempt=$2 AND o.payload_json=$3
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$4 AND c.baseline_release=$5 AND c.baseline_sha=$6
 AND l.name=$7 AND l.holder=$8 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$9`, operation.OperationID, operation.OperationAttempt, work.Payload, work.Proof.Source.ClusterID, work.Baseline.Release, work.Baseline.SourceSHA, clusterControllerLeaseName, operation.ControllerHolder, len(work.Claims)).Scan(&now) != nil {
		return errClusterConflict
	}
	for i, claim := range work.Claims {
		lease := claim.Lease
		target := work.Proof.Cohort.Targets[i]
		if !validClusterSSHObservationLease(lease) || lease.OperationStep != clusterSSHQualificationStep || lease.TargetID != work.Proof.Cohort.Targets[i].Binding.TargetID || lease.OperationID != operation.OperationID || lease.ControllerHolder != operation.ControllerHolder || lease.OperationAttempt != operation.OperationAttempt || claim.Sealed.binding != work.Proof.Cohort.Targets[i].Binding {
			return errClusterConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets t SET state='queued',current_step='qualification_complete',lease_holder='',lease_expires_at=0,updated_at=$9
 FROM engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a
 WHERE t.id=$1 AND t.operation_id=$2 AND t.operation_attempt=$3 AND t.state='running' AND t.current_step='qualify'
 AND t.lease_holder=$4 AND t.lease_generation=$5 AND t.lease_expires_at>extract(epoch FROM clock_timestamp()) AND t.credential_state='available'
 AND p.target_id=t.id AND p.aegis_generation=$6 AND p.ciphertext=$7 AND p.expires_at>extract(epoch FROM clock_timestamp())
 AND a.id=1 AND a.verification_token=p.aegis_generation AND t.inspected_generation=$8
 AND t.cluster_id=$10 AND t.management_ip=$11 AND t.ssh_port=$12 AND t.host_key_algorithm=$13 AND t.host_key_fingerprint=$14
 AND t.host_key_base64=$15 AND t.ordinal=$16 AND t.inspected_attempt=$3 AND t.inspected_at=$17 AND t.inspection_json=$18`, lease.TargetID, lease.OperationID, lease.OperationAttempt, lease.Holder, lease.Generation, claim.Sealed.generation, claim.Sealed.ciphertext, target.Generation, now,
			target.Binding.ClusterID, target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint, keys[target.Binding.TargetID], target.Ordinal, target.InspectedAt, reports[target.Binding.TargetID])
		if err != nil {
			return errClusterUnavailable
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errClusterConflict
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=$2,updated_at=$3 WHERE id=$1`, operation.OperationID, string(updated), now); err != nil {
		return errClusterUnavailable
	}
	if insertClusterEvent(ctx, tx, operation.OperationID, "", work.Proof.Source.ClusterID, "ssh_qualification_observed", "waiting", "Read-only qualification results recorded; remaining prerequisites and confirmation are required.", map[string]any{"attempt": operation.OperationAttempt}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}
