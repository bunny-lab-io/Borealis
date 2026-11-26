package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"time"
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
 AND (t.current_step='inspection_complete' OR (COALESCE(o.payload_json::jsonb->'ssh_source_staging_required','false'::jsonb)='false'::jsonb OR COALESCE(o.error_text,'')=$1))
 AND t.lease_expires_at<=extract(epoch FROM clock_timestamp())) LIMIT 1`, clusterSSHStageReady).Scan(&id)
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
	work.Baseline = clusterBaselineExpected(work.Baseline.Release, work.Baseline.SourceSHA)
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
	var stageReady bool
	if tx.QueryRowContext(ctx, `SELECT (COALESCE(o.payload_json::jsonb->'ssh_source_staging_required','false'::jsonb)='false'::jsonb OR COALESCE(o.error_text,'')=$10)
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets' AND o.attempt=$2 AND o.payload_json=$3
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$4 AND c.baseline_release=$5 AND c.baseline_sha=$6
 AND l.name=$7 AND l.holder=$8 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$9`, operationID, attempt, work.Payload, work.Proof.Source.ClusterID, work.Baseline.Release, work.Baseline.SourceSHA, clusterControllerLeaseName, controller, len(targets), clusterSSHStageReady).Scan(&stageReady) != nil {
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
 AND t.inspected_at>moment.now-(CASE WHEN t.state='running' THEN $17::bigint ELSE $16::bigint END) AND t.inspected_at<=moment.now AND t.credential_state='available'
 AND ((t.state='queued' AND t.current_step='inspection_complete' AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=t.inspected_generation)
 OR($18 AND t.state='running' AND t.current_step='qualify' AND t.lease_expires_at<=moment.now AND t.lease_generation>t.inspected_generation))
 AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
 RETURNING t.lease_generation,p.aegis_generation,p.ciphertext`, t.Binding.TargetID, holder, clusterSSHTargetLeaseSeconds, operationID, t.Binding.ClusterID, attempt, t.Binding.Address, t.Binding.Port, t.Key.Algorithm, t.Key.Fingerprint, keys[t.Binding.TargetID], t.Ordinal, t.Generation, t.InspectedAt, reports[t.Binding.TargetID], clusterSSHFreshEvidenceSeconds, clusterSSHInspectionLifetimeSeconds, stageReady).Scan(&claim.Lease.Generation, &claim.Sealed.generation, &claim.Sealed.ciphertext) != nil {
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

// A progress entry describes work in flight. It has no role in admission or
// recovery. Record each check once per attempt under the original controller
// and exact operation payload. Qualification workers also match their lease.
func (s *postgresOperatorStore) recordSSHCheckStarted(parent context.Context, cohort clusterSSHInspectionCohort, payload, check string, worker clusterSSHTargetLease) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events
 (operation_id,cluster_id,event_type,state,message,details_json,created_at)
 SELECT o.id,s.cluster_id,'ssh_check_started','waiting',$1,json_build_object('attempt',o.attempt,'check',$2::text)::text,floor(extract(epoch FROM clock_timestamp()))
 FROM engine.cluster_operations o,engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.id=$3 AND o.attempt=$4 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets'
 AND o.payload_json=$5 AND s.id=1 AND s.active_operation_id=o.id AND s.cluster_id=$6
 AND l.name=$7 AND l.holder=$8 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND ($9='' OR EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t WHERE t.id=$9 AND t.operation_id=o.id
 AND t.current_step='qualify' AND t.lease_holder=$10 AND t.lease_generation=$11 AND t.lease_expires_at>extract(epoch FROM clock_timestamp())))
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_operation_events e WHERE e.operation_id=o.id AND e.event_type='ssh_check_started'
 AND e.details_json::jsonb->>'attempt'=o.attempt::text AND e.details_json::jsonb->>'check'=$2)`,
		"Checking "+strings.ReplaceAll(check, "_", " ")+".", check, cohort.OperationID, cohort.Attempt, payload, cohort.ClusterID,
		clusterControllerLeaseName, cohort.ControllerHolder, worker.TargetID, worker.Holder, worker.Generation)
	if err != nil && parent.Err() == nil {
		log.Printf("cluster operation %s: progress entry unavailable for %s", cohort.OperationID, check)
	}
}

// An expired snapshot can still report which read stopped. The event is
// historical and may only describe its original active target generation.
func (s *postgresOperatorStore) recordSSHWorkerStorageFailure(parent context.Context, lease clusterSSHTargetLease, phase string) {
	if !validClusterSSHObservationLease(lease) {
		return
	}
	state, label := "running", "Preparation worker storage check"
	if lease.isNetworkQualification() {
		state, label = "waiting", "Host readiness check"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events
 (operation_id,cluster_id,event_type,state,message,details_json,created_at)
 SELECT o.id,s.cluster_id,'ssh_worker_storage_blocked',o.state,$1,
 json_build_object('attempt',o.attempt,'target_id',t.id,'generation',t.lease_generation)::text,floor(extract(epoch FROM clock_timestamp()))
 FROM engine.cluster_operations o JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id,
 engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.kind='ssh_onboarding' AND o.id=$2 AND o.attempt=$3 AND o.current_step=$9 AND o.state=$10
 AND s.id=1 AND s.active_operation_id=o.id AND l.name=$4 AND l.holder=$5 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND t.id=$6 AND t.state='running' AND t.current_step=$11 AND t.lease_holder=$7 AND t.lease_generation=$8`,
		label+" stopped during "+phase+".", lease.OperationID, lease.OperationAttempt,
		clusterControllerLeaseName, lease.ControllerHolder, lease.TargetID, lease.Holder, lease.Generation, lease.OperationStep, state, lease.Step)
	if err != nil {
		log.Printf("cluster operation %s: worker storage failure could not be recorded", lease.OperationID)
	}
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
 AND (COALESCE(o.payload_json::jsonb->'ssh_source_staging_required','false'::jsonb)='false'::jsonb OR COALESCE(o.error_text,'')=$10)
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$4 AND c.baseline_release=$5 AND c.baseline_sha=$6
 AND l.name=$7 AND l.holder=$8 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$9`, operation.OperationID, operation.OperationAttempt, work.Payload, work.Proof.Source.ClusterID, work.Baseline.Release, work.Baseline.SourceSHA, clusterControllerLeaseName, operation.ControllerHolder, len(work.Claims), clusterSSHStageReady).Scan(&now) != nil {
		return errClusterConflict
	}
	// Cancellation and timeout reports can still release their claims. Never
	// publish a passing report after the database-clock budget has expired.
	if completedClusterSSHQualification(&report) && now >= work.deadline().Unix() {
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
	outcomes := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		outcomes = append(outcomes, strings.ReplaceAll(check.Code, "_", " ")+" "+check.State)
	}
	if insertClusterEvent(ctx, tx, operation.OperationID, "", work.Proof.Source.ClusterID, "ssh_qualification_observed", "waiting", "Host readiness checks: "+strings.Join(outcomes, ", ")+".", map[string]any{"attempt": operation.OperationAttempt}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}
