package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// This reservation is written only by the guarded controller approval transition,
// after phase-specific prerequisites and explicit confirmation. A qualification
// report (including all currently supported checks passing) cannot create it.
// No production writer or dispatcher is enabled by the claim consumer below.
type clusterSSHPreparationAuthorization struct {
	Version             int                                     `json:"version"`
	OperationID         string                                  `json:"operation_id"`
	Attempt             int64                                   `json:"attempt"`
	ControllerHolder    string                                  `json:"controller_holder"`
	InspectionSHA256    string                                  `json:"inspection_sha256"`
	QualificationSHA256 string                                  `json:"qualification_sha256"`
	SourceSHA256        string                                  `json:"source_sha256"`
	ConfirmationSHA256  string                                  `json:"confirmation_sha256"`
	Targets             []clusterSSHPreparationAuthorizedTarget `json:"targets"`
}

type clusterSSHPreparationAuthorizedTarget struct {
	TargetID                string `json:"target_id"`
	QualificationGeneration int64  `json:"qualification_generation"`
	CredentialSHA256        string `json:"credential_sha256"`
}

type clusterSSHPreparationWork struct {
	Claims         []clusterSSHNetworkTargetClaim
	Baseline       clusterbootstrap.Expected
	Payload        string
	Proof          clusterSSHParentProof
	Source         clusterSSHQueueSource
	Authorization  clusterSSHPreparationAuthorization
	IdentitySource engineidentity.Binding
}

func (clusterSSHPreparationWork) String() string   { return "SSH preparation work [redacted]" }
func (clusterSSHPreparationWork) GoString() string { return "SSH preparation work [redacted]" }

func clusterSSHPreparationDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", errClusterConflict
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func parseClusterSSHPreparationAuthorization(payload string, proof clusterSSHParentProof, source clusterSSHQueueSource) (clusterSSHPreparationAuthorization, *clusterSSHQualificationReport, error) {
	fail := func() (clusterSSHPreparationAuthorization, *clusterSSHQualificationReport, error) {
		return clusterSSHPreparationAuthorization{}, nil, errClusterConflict
	}
	var raw, qualificationRaw, completed, recovery, confirmationRaw, readinessRaw json.RawMessage
	if len(payload) == 0 || len(payload) > 256<<10 || clusterBootstrapObject([]byte(payload), map[string]any{
		"ssh_preparation_authorization": &raw, "ssh_qualification": &qualificationRaw,
		"ssh_preparation_confirmation": &confirmationRaw, "ssh_preparation_readiness": &readinessRaw,
		"ssh_preparation": &completed, "ssh_identity_recovery": &recovery,
	}) != nil || len(raw) == 0 || len(raw) > 8<<10 || len(completed) != 0 || len(recovery) != 0 {
		return fail()
	}
	var authorization clusterSSHPreparationAuthorization
	if json.Unmarshal(raw, &authorization) != nil {
		return fail()
	}
	canonical, err := json.Marshal(authorization)
	qualification, qualificationErr := parseClusterSSHQualificationReport(string(qualificationRaw), proof.Cohort.Attempt)
	inspectionDigest, inspectionErr := clusterSSHPreparationDigest(proof)
	qualificationDigest, digestErr := clusterSSHPreparationDigest(qualification)
	sourceDigest, sourceErr := clusterSSHPreparationDigest(source)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) || qualificationErr != nil || !completedClusterSSHQualification(qualification) ||
		inspectionErr != nil || digestErr != nil || sourceErr != nil || authorization.Version != 1 ||
		authorization.OperationID != proof.Cohort.OperationID || authorization.Attempt != proof.Cohort.Attempt ||
		authorization.ControllerHolder != proof.Cohort.ControllerHolder || authorization.InspectionSHA256 != inspectionDigest ||
		authorization.QualificationSHA256 != qualificationDigest || authorization.SourceSHA256 != sourceDigest ||
		!clusterSSHPreparationResultDigestRE.MatchString(authorization.ConfirmationSHA256) || authorization.ConfirmationSHA256 == strings.Repeat("0", 64) ||
		len(authorization.Targets) < 1 || len(authorization.Targets) > 2 || len(authorization.Targets) != len(proof.Cohort.Targets) {
		return fail()
	}
	for i, target := range proof.Cohort.Targets {
		reserved := authorization.Targets[i]
		if reserved.TargetID != target.Binding.TargetID || reserved.QualificationGeneration <= target.Generation ||
			reserved.QualificationGeneration == 1<<63-1 || !clusterSSHPreparationResultDigestRE.MatchString(reserved.CredentialSHA256) ||
			reserved.CredentialSHA256 == strings.Repeat("0", 64) {
			return fail()
		}
	}
	confirmation, err := parseClusterSSHPreparationConfirmation(confirmationRaw, authorization, proof.Source)
	if err != nil || confirmation.ConfirmedAt < qualification.ObservedAt || parseClusterSSHPreparationReadiness(readinessRaw, authorization) != nil {
		return fail()
	}
	return authorization, qualification, nil
}

// claimClusterSSHPreparation consumes an already reserved controller phase.
// It never moves qualification into preparation or reclaims uncertain remote
// work. The entire cohort acquires fresh claims together, under original
// controller ownership, before any credential decryption or SSH is possible.
func (c *clusterController) claimClusterSSHPreparation(ctx context.Context, operation clusterControllerOperation, holder string) (clusterSSHPreparationWork, error) {
	fail := func() (clusterSSHPreparationWork, error) { return clusterSSHPreparationWork{}, errClusterConflict }
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || !clusterUUIDRE.MatchString(holder) ||
		!clusterUUIDRE.MatchString(operation.ID) || operation.Kind != "ssh_onboarding" || operation.State != "running" ||
		operation.CurrentStep != clusterSSHPreparationOperationStep || operation.Attempt < 1 || c.holder == "" || len(c.holder) > 1024 {
		return fail()
	}
	var work clusterSSHPreparationWork
	if c.store.db.QueryRowContext(ctx, `SELECT o.payload_json,c.baseline_release,c.baseline_sha
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3
 AND octet_length(o.payload_json)<=262144 AND c.id=1 AND c.active_operation_id=o.id
 AND l.name=$4 AND l.holder=$5 AND l.expires_at>extract(epoch FROM clock_timestamp())`, operation.ID,
		clusterSSHPreparationOperationStep, operation.Attempt, clusterControllerLeaseName, c.holder).
		Scan(&work.Payload, &work.Baseline.Release, &work.Baseline.SourceSHA) != nil {
		return fail()
	}
	work.Baseline.Repository = clusterGitHubRepo()
	work.Baseline.AllowQualification = strings.Contains(work.Baseline.Release, "-rc.")
	if work.Baseline.Validate() != nil {
		return fail()
	}
	anchor := clusterSSHTargetLease{OperationID: operation.ID, OperationAttempt: operation.Attempt, ControllerHolder: c.holder,
		OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationOperationStep, Step: "stage_source", Holder: holder}
	var err error
	var version string
	work.Proof, version, err = parseClusterSSHPreparationInspection([]byte(work.Payload), anchor, work.Baseline)
	if err != nil {
		return fail()
	}
	work.Source, err = readClusterSSHQueueSource(ctx, c.store.db)
	if err != nil {
		return fail()
	}
	source := work.Source
	if source.ActiveOperationID != operation.ID || source.ClusterID != work.Proof.Source.ClusterID ||
		source.ActiveSize != work.Proof.Source.ActiveSize || source.DesiredSize != work.Proof.Source.DesiredSize ||
		source.Status != work.Proof.Source.Status || source.HMRState != work.Proof.Source.HMRState ||
		source.ControlVIP != work.Proof.Source.ControlPlaneVIP || source.EdgeVIP != work.Proof.Source.EdgeVIP ||
		source.Release != work.Baseline.Release || source.SHA != work.Baseline.SourceSHA {
		return fail()
	}
	source.ActiveOperationID = ""
	count, err := source.validate()
	var configured string
	if err != nil || count != len(work.Proof.Cohort.Targets) ||
		clusterBootstrapObject([]byte(source.ConfigJSON), map[string]any{"k3s_version": &configured}) != nil || configured != version {
		return fail()
	}
	var qualification *clusterSSHQualificationReport
	work.Authorization, qualification, err = parseClusterSSHPreparationAuthorization(work.Payload, work.Proof, work.Source)
	if err != nil {
		return fail()
	}
	var confirmationRaw json.RawMessage
	if clusterBootstrapObject([]byte(work.Payload), map[string]any{"ssh_preparation_confirmation": &confirmationRaw}) != nil {
		return fail()
	}
	confirmation, err := parseClusterSSHPreparationConfirmation(confirmationRaw, work.Authorization, work.Proof.Source)
	if err != nil {
		return fail()
	}
	work.IdentitySource = confirmation.IdentitySource
	targets := slices.Clone(work.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys, generations := map[string]string{}, map[string]string{}, map[string]int64{}
	sealed := make(map[string]sealedClusterSSHCredentials, len(targets))
	for i, target := range work.Proof.Cohort.Targets {
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return fail()
		}
		reports[target.Binding.TargetID] = string(raw)
		keys[target.Binding.TargetID] = base64.StdEncoding.EncodeToString(target.Key.PublicKey)
		generations[target.Binding.TargetID] = work.Authorization.Targets[i].QualificationGeneration
		credential := sealedClusterSSHCredentials{binding: target.Binding}
		if c.store.db.QueryRowContext(ctx, `SELECT aegis_generation,ciphertext FROM engine.cluster_onboarding_credentials
 WHERE target_id=$1 AND octet_length(aegis_generation) BETWEEN 1 AND 16384 AND octet_length(ciphertext) BETWEEN 1 AND 262144`, target.Binding.TargetID).
			Scan(&credential.generation, &credential.ciphertext) != nil || !strings.HasPrefix(credential.ciphertext, aegisEnvelopePrefix) {
			return fail()
		}
		// Hash only after the read returns its connection. Public reservation
		// stores no ciphertext; final UPDATE compares the exact observed bytes.
		digest, err := clusterSSHPreparationDigest([]string{credential.generation, credential.ciphertext})
		if err != nil || digest != work.Authorization.Targets[i].CredentialSHA256 {
			return fail()
		}
		sealed[target.Binding.TargetID] = credential
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, operation.ID, targets) != nil ||
		checkClusterSSHQualificationSource(ctx, tx, clusterSSHQualificationWork{Proof: work.Proof, Source: work.Source}) != nil {
		return fail()
	}
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations o,engine.cluster_state c
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND c.id=1 AND c.active_operation_id=o.id
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$5`, operation.ID,
		clusterSSHPreparationOperationStep, operation.Attempt, work.Payload, len(targets)).Scan(&present) != nil {
		return fail()
	}
	claims := make(map[string]clusterSSHNetworkTargetClaim, len(targets))
	for _, target := range targets {
		claim := clusterSSHNetworkTargetClaim{Lease: anchor, Sealed: sealed[target.Binding.TargetID]}
		claim.Lease.TargetID = target.Binding.TargetID
		if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_onboarding_targets t SET state='running',current_step='stage_source',lease_holder=$2,
 lease_generation=t.lease_generation+1,lease_expires_at=floor(moment.now)+$3,updated_at=floor(moment.now)
 FROM moment,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a
 WHERE t.id=$1 AND t.operation_id=$4 AND t.cluster_id=$5 AND t.operation_attempt=$6
 AND t.management_ip=$7 AND t.ssh_port=$8 AND t.host_key_algorithm=$9 AND t.host_key_fingerprint=$10 AND t.host_key_base64=$11 AND t.ordinal=$12
 AND t.inspected_attempt=$6 AND t.inspected_generation=$13 AND t.inspected_at=$14 AND t.inspection_json=$15
 AND t.inspected_at>moment.now-$16 AND t.inspected_at<=moment.now AND t.credential_state='available'
 AND t.state='queued' AND t.current_step='qualification_complete' AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=$17
 AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
 AND p.aegis_generation=$18 AND p.ciphertext=$19
 RETURNING t.lease_generation`, target.Binding.TargetID, holder, clusterSSHTargetLeaseSeconds,
			operation.ID, target.Binding.ClusterID, operation.Attempt, target.Binding.Address, target.Binding.Port, target.Key.Algorithm,
			target.Key.Fingerprint, keys[target.Binding.TargetID], target.Ordinal, target.Generation, target.InspectedAt,
			reports[target.Binding.TargetID], clusterSSHInspectionLifetimeSeconds, generations[target.Binding.TargetID], claim.Sealed.generation, claim.Sealed.ciphertext).
			Scan(&claim.Lease.Generation) != nil {
			return fail()
		}
		claims[target.Binding.TargetID] = claim
	}
	// Re-evaluate every clock-sensitive predicate after all locks and updates.
	// A lost acknowledgement deliberately cannot obtain another generation;
	// prior executors must be reconciled before a future retry path is enabled.
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT 1 FROM moment,engine.cluster_application_leases l,engine.aegis_cipher_state a
 WHERE l.name=$1 AND l.holder=$2 AND l.expires_at>moment.now AND a.id=1
 AND $3::bigint>moment.now-$4 AND $3::bigint<=moment.now
 AND $7::bigint>moment.now-$4 AND $7::bigint<=moment.now
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t
 LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=$5 AND (t.state='running' AND t.current_step='stage_source' AND t.lease_holder=$6
 AND t.lease_expires_at>moment.now AND t.inspected_at>moment.now-$4 AND t.inspected_at<=moment.now
 AND p.aegis_generation=a.verification_token AND p.expires_at>moment.now) IS NOT TRUE)`, clusterControllerLeaseName,
		c.holder, qualification.ObservedAt, clusterSSHInspectionLifetimeSeconds, operation.ID, holder, confirmation.ConfirmedAt).Scan(&present) != nil {
		return fail()
	}
	if tx.Commit() != nil {
		return fail()
	}
	for _, target := range work.Proof.Cohort.Targets {
		work.Claims = append(work.Claims, claims[target.Binding.TargetID])
	}
	return work, nil
}
