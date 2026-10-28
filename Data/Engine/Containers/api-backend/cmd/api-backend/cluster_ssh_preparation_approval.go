package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type clusterSSHPreparationApprovalSnapshot struct {
	work        clusterSSHPreparationWork
	credentials []sealedClusterSSHCredentials
}

func (clusterSSHPreparationApprovalSnapshot) String() string {
	return "SSH preparation approval [redacted]"
}
func (clusterSSHPreparationApprovalSnapshot) GoString() string {
	return "SSH preparation approval [redacted]"
}

// Only a synchronous phase-proof scope may supply this live capability. It
// cannot be serialized, restored from a digest or inferred from qualification.
type clusterSSHPreparationApprovalProof struct {
	receipts [6]string
	plan     string
	started  time.Time
	lifetime context.Context
	recheck  func(context.Context) error
}

func (p clusterSSHPreparationApprovalProof) valid(plan string) bool {
	for _, digest := range p.receipts {
		if !validClusterSSHPreparationDigest(digest) {
			return false
		}
	}
	return p.plan == plan && validClusterSSHPreparationDigest(plan) && !p.started.IsZero() &&
		time.Since(p.started) >= 0 && time.Since(p.started) < 30*time.Second && p.lifetime != nil &&
		p.lifetime.Err() == nil && p.recheck != nil
}

// Read a proposed approval without changing the waiting operation or taking
// worker leases. Database time is returned for a future authenticated request
// boundary; controller approval still requires independently fresh phase proof.
func (c *clusterController) loadClusterSSHPreparationApproval(ctx context.Context, operation clusterControllerOperation) (clusterSSHPreparationApprovalSnapshot, int64, error) {
	if c == nil {
		return clusterSSHPreparationApprovalSnapshot{}, 0, errClusterConflict
	}
	return c.store.readClusterSSHPreparationProposal(ctx, operation, c.holder)
}

// Read-only proposal projection can be shared with the Admin API. Passing the
// recorded owner to a read grants no controller lease or approval capability.
func (s *postgresOperatorStore) readClusterSSHPreparationProposal(ctx context.Context, operation clusterControllerOperation, holder string) (clusterSSHPreparationApprovalSnapshot, int64, error) {
	fail := func() (clusterSSHPreparationApprovalSnapshot, int64, error) {
		return clusterSSHPreparationApprovalSnapshot{}, 0, errClusterConflict
	}
	if s == nil || s.db == nil || ctx.Err() != nil || !clusterUUIDRE.MatchString(operation.ID) ||
		operation.Kind != "ssh_onboarding" || operation.State != "waiting" || operation.CurrentStep != clusterSSHQualificationStep ||
		operation.Attempt < 1 || holder == "" || len(holder) > 1024 {
		return fail()
	}
	var snapshot clusterSSHPreparationApprovalSnapshot
	w := &snapshot.work
	var now int64
	if s.db.QueryRowContext(ctx, `SELECT o.payload_json,c.baseline_release,c.baseline_sha,floor(extract(epoch FROM clock_timestamp()))::bigint
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step=$2 AND o.attempt=$3
 AND octet_length(o.payload_json)<=262144 AND c.id=1 AND c.active_operation_id=o.id
 AND l.name=$4 AND l.holder=$5 AND l.expires_at>extract(epoch FROM clock_timestamp())`, operation.ID, clusterSSHQualificationStep,
		operation.Attempt, clusterControllerLeaseName, holder).Scan(&w.Payload, &w.Baseline.Release, &w.Baseline.SourceSHA, &now) != nil {
		return fail()
	}
	w.Baseline.Repository, w.Baseline.AllowQualification = clusterGitHubRepo(), strings.Contains(w.Baseline.Release, "-rc.")
	if w.Baseline.Validate() != nil {
		return fail()
	}
	anchor := clusterSSHTargetLease{OperationID: operation.ID, OperationAttempt: operation.Attempt, ControllerHolder: holder}
	var version string
	var err error
	w.Proof, version, err = parseClusterSSHPreparationInspection([]byte(w.Payload), anchor, w.Baseline)
	if err != nil {
		return fail()
	}
	var reportRaw, authorizationRaw, confirmationRaw, receiptRaw, recoveryRaw, readinessRaw json.RawMessage
	if clusterBootstrapObject([]byte(w.Payload), map[string]any{"ssh_qualification": &reportRaw, "ssh_preparation_authorization": &authorizationRaw,
		"ssh_preparation_confirmation": &confirmationRaw, "ssh_preparation_readiness": &readinessRaw, "ssh_preparation": &receiptRaw, "ssh_identity_recovery": &recoveryRaw}) != nil ||
		len(authorizationRaw) != 0 || len(confirmationRaw) != 0 || len(readinessRaw) != 0 || len(receiptRaw) != 0 || len(recoveryRaw) != 0 {
		return fail()
	}
	report, err := parseClusterSSHQualificationReport(string(reportRaw), operation.Attempt)
	if err != nil || !completedClusterSSHQualification(report) || report.ObservedAt > now || now-report.ObservedAt >= clusterSSHInspectionLifetimeSeconds {
		return fail()
	}
	w.Source, err = readClusterSSHQueueSource(ctx, s.db)
	if err != nil {
		return fail()
	}
	source := w.Source
	if source.ActiveOperationID != operation.ID || source.ClusterID != w.Proof.Source.ClusterID || source.ActiveSize != w.Proof.Source.ActiveSize ||
		source.DesiredSize != w.Proof.Source.DesiredSize || source.Status != w.Proof.Source.Status || source.HMRState != w.Proof.Source.HMRState ||
		source.ControlVIP != w.Proof.Source.ControlPlaneVIP || source.EdgeVIP != w.Proof.Source.EdgeVIP || source.Release != w.Baseline.Release || source.SHA != w.Baseline.SourceSHA {
		return fail()
	}
	source.ActiveOperationID = ""
	count, err := source.validate()
	var configured string
	if err != nil || count != len(w.Proof.Cohort.Targets) ||
		clusterBootstrapObject([]byte(source.ConfigJSON), map[string]any{"k3s_version": &configured}) != nil || configured != version {
		return fail()
	}
	a := &w.Authorization
	a.Version, a.OperationID, a.Attempt, a.ControllerHolder = 1, operation.ID, operation.Attempt, holder
	if a.InspectionSHA256, err = clusterSSHPreparationDigest(w.Proof); err != nil {
		return fail()
	}
	if a.QualificationSHA256, err = clusterSSHPreparationDigest(report); err != nil {
		return fail()
	}
	if a.SourceSHA256, err = clusterSSHPreparationDigest(w.Source); err != nil {
		return fail()
	}
	for _, target := range w.Proof.Cohort.Targets {
		credential := sealedClusterSSHCredentials{binding: target.Binding}
		reserved := clusterSSHPreparationAuthorizedTarget{TargetID: target.Binding.TargetID}
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return fail()
		}
		if s.db.QueryRowContext(ctx, `SELECT t.lease_generation,p.aegis_generation,p.ciphertext
 FROM engine.cluster_onboarding_targets t,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a
 WHERE t.id=$1 AND t.operation_id=$2 AND t.operation_attempt=$3 AND t.cluster_id=$4
 AND t.state='queued' AND t.current_step='qualification_complete' AND t.lease_holder='' AND t.lease_expires_at=0
 AND t.lease_generation>t.inspected_generation AND t.inspected_generation=$5 AND t.inspected_attempt=$3
 AND t.inspected_at=$6 AND t.inspected_at>extract(epoch FROM clock_timestamp())-$7 AND t.inspected_at<=extract(epoch FROM clock_timestamp())
 AND t.inspection_json=$8 AND t.management_ip=$9 AND t.ssh_port=$10 AND t.host_key_algorithm=$11
 AND t.host_key_fingerprint=$12 AND t.host_key_base64=$13 AND t.ordinal=$14 AND t.credential_state='available'
 AND p.target_id=t.id AND p.expires_at>extract(epoch FROM clock_timestamp()) AND a.id=1 AND a.verification_token=p.aegis_generation
 AND octet_length(p.aegis_generation) BETWEEN 1 AND 16384 AND octet_length(p.ciphertext) BETWEEN 1 AND 262144`,
			target.Binding.TargetID, operation.ID, operation.Attempt, target.Binding.ClusterID, target.Generation, target.InspectedAt,
			clusterSSHInspectionLifetimeSeconds, string(raw), target.Binding.Address, target.Binding.Port, target.Key.Algorithm,
			target.Key.Fingerprint, base64.StdEncoding.EncodeToString(target.Key.PublicKey), target.Ordinal).
			Scan(&reserved.QualificationGeneration, &credential.generation, &credential.ciphertext) != nil || !strings.HasPrefix(credential.ciphertext, aegisEnvelopePrefix) {
			return fail()
		}
		reserved.CredentialSHA256, err = clusterSSHPreparationDigest([]string{credential.generation, credential.ciphertext})
		if err != nil {
			return fail()
		}
		a.Targets = append(a.Targets, reserved)
		snapshot.credentials = append(snapshot.credentials, credential)
	}
	if _, err := clusterSSHPreparationPlanDigest(*a); err != nil {
		return fail()
	}
	return snapshot, now, nil
}

// Commit only while the local live proof capability remains valid. The caller
// owns fresh source/target/artifact/readiness observation outside transactions.
// This function has no production caller until that complete proof adapter exists.
func (c *clusterController) commitClusterSSHPreparationApproval(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot,
	confirmation clusterSSHPreparationConfirmation, proof clusterSSHPreparationApprovalProof) error {
	w := snapshot.work
	plan, err := clusterSSHPreparationPlanDigest(w.Authorization)
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || err != nil || !confirmation.valid(w.Authorization, w.Proof.Source) ||
		w.Authorization.ControllerHolder != c.holder || !proof.valid(plan) || len(snapshot.credentials) != len(w.Proof.Cohort.Targets) ||
		len(w.Authorization.Targets) != len(snapshot.credentials) || proof.recheck(ctx) != nil || !proof.valid(plan) {
		return errClusterConflict
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(w.Payload), &payload) != nil || payload["ssh_preparation_authorization"] != nil || payload["ssh_preparation_confirmation"] != nil {
		return errClusterConflict
	}
	requested, err := parseClusterSSHPreparationRequest(payload["ssh_preparation_request"], w.Authorization, w.Proof.Source)
	if err != nil || requested != confirmation {
		return errClusterConflict
	}
	w.Authorization.ConfirmationSHA256, err = clusterSSHPreparationDigest(confirmation)
	if err != nil {
		return errClusterConflict
	}
	payload["ssh_preparation_authorization"], err = json.Marshal(w.Authorization)
	if err != nil {
		return errClusterConflict
	}
	payload["ssh_preparation_confirmation"], err = json.Marshal(confirmation)
	if err != nil {
		return errClusterConflict
	}
	payload["ssh_preparation_readiness"], err = json.Marshal(clusterSSHPreparationReadiness(plan, proof.receipts))
	if err != nil {
		return errClusterConflict
	}
	updated, err := json.Marshal(payload)
	if err != nil || len(updated) > 256<<10 {
		return errClusterConflict
	}
	_, qualification, err := parseClusterSSHPreparationAuthorization(string(updated), w.Proof, w.Source)
	if err != nil {
		return errClusterConflict
	}
	check, err := clusterSSHPreparationProposalCheck(snapshot)
	if err != nil {
		return errClusterConflict
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	if check(ctx, tx) != nil {
		return errClusterConflict
	}
	var now int64
	// All expiry checks use one fresh database clock after every row-lock wait.
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_operations o SET state='running',current_step=$2,payload_json=$3,updated_at=floor(moment.now)
 FROM moment,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step=$4 AND o.attempt=$5 AND o.payload_json=$6
 AND c.id=1 AND c.active_operation_id=o.id AND l.name=$7 AND l.holder=$8 AND l.expires_at>moment.now
 AND $9::bigint>moment.now-$11 AND $9::bigint<=moment.now AND $10::bigint>moment.now-$11 AND $10::bigint<=moment.now
 AND $10::bigint>=$9::bigint AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$12
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=o.id AND (t.inspected_at>moment.now-$11 AND t.inspected_at<=moment.now AND p.expires_at>moment.now) IS NOT TRUE)
 RETURNING floor(moment.now)::bigint`, w.Authorization.OperationID, clusterSSHPreparationOperationStep, string(updated), clusterSSHQualificationStep,
		w.Authorization.Attempt, w.Payload, clusterControllerLeaseName, c.holder, qualification.ObservedAt, confirmation.ConfirmedAt,
		clusterSSHInspectionLifetimeSeconds, len(w.Proof.Cohort.Targets)).Scan(&now) != nil || !proof.valid(plan) || ctx.Err() != nil {
		return errClusterConflict
	}
	if insertClusterEvent(ctx, tx, w.Authorization.OperationID, "", w.Source.ClusterID, "ssh_preparation_approved", "running",
		"Exact SSH preparation plan confirmed; guarded worker dispatch is required.", map[string]any{"attempt": w.Authorization.Attempt,
			"request_id": confirmation.RequestID, "plan_sha256": plan, "confirmation_sha256": w.Authorization.ConfirmationSHA256}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}
