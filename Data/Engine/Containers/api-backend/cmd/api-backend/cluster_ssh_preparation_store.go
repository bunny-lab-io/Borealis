package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

var clusterSSHPreparationResultDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Completion contains only fixed journal result digests. It is not produced by
// qualification and grants no restart, next-target or membership authority.
type clusterSSHPreparationResult struct {
	Version            int    `json:"version"`
	Attempt            int64  `json:"attempt"`
	TargetID           string `json:"target_id"`
	Generation         int64  `json:"generation"`
	ConfirmationSHA256 string `json:"confirmation_sha256"`
	StageSourceSHA256  string `json:"stage_source_sha256"`
	PrepareHostSHA256  string `json:"prepare_host_sha256"`
	StageImagesSHA256  string `json:"stage_images_sha256"`
	JoinClusterSHA256  string `json:"join_cluster_sha256"`
}

func (r clusterSSHPreparationResult) valid(lease clusterSSHTargetLease) bool {
	if r.Version != 1 || r.Attempt != lease.OperationAttempt || r.TargetID != lease.TargetID ||
		r.Generation != lease.Generation || !validClusterSSHPreparationLease(lease) {
		return false
	}
	return r.validProof(lease.OperationID, lease.TargetID, lease.OperationAttempt, 0)
}

func (r clusterSSHPreparationResult) validProof(operationID, targetID string, attempt, inspectedGeneration int64) bool {
	if !clusterUUIDRE.MatchString(operationID) || !clusterUUIDRE.MatchString(targetID) || r.Version != 1 || r.Attempt != attempt ||
		r.TargetID != targetID || r.Generation <= inspectedGeneration {
		return false
	}
	for _, digest := range []string{r.ConfirmationSHA256, r.StageSourceSHA256, r.PrepareHostSHA256, r.StageImagesSHA256, r.JoinClusterSHA256} {
		if !clusterSSHPreparationResultDigestRE.MatchString(digest) || digest == strings.Repeat("0", 64) {
			return false
		}
	}
	return true
}

type clusterSSHPreparationCompletion struct {
	Authority clusterSSHPreparationAuthority
	Sealed    sealedClusterSSHCredentials
	Result    clusterSSHPreparationResult
}

type clusterSSHPreparationReceipt struct {
	Version            int                           `json:"version"`
	Attempt            int64                         `json:"attempt"`
	ConfirmationSHA256 string                        `json:"confirmation_sha256"`
	Targets            []clusterSSHPreparationResult `json:"targets"`
}

func parseClusterSSHPreparationReceipt(raw string, proof clusterSSHParentProof) (clusterSSHPreparationReceipt, error) {
	fail := func() (clusterSSHPreparationReceipt, error) {
		return clusterSSHPreparationReceipt{}, errClusterConflict
	}
	var receiptRaw json.RawMessage
	if len(raw) == 0 || len(raw) > 256<<10 || clusterBootstrapObject([]byte(raw), map[string]any{"ssh_preparation": &receiptRaw}) != nil ||
		len(receiptRaw) == 0 || len(receiptRaw) > 8<<10 {
		return fail()
	}
	object, err := decodeClusterSSHObject(receiptRaw, map[string]bool{
		"version": true, "attempt": true, "confirmation_sha256": true, "targets": true,
	})
	if err != nil || len(object) != 4 {
		return fail()
	}
	var receipt clusterSSHPreparationReceipt
	if json.Unmarshal(receiptRaw, &receipt) != nil {
		return fail()
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !sameClusterSSHStoredJSON(receiptRaw, canonical, 0) || receipt.Version != 1 || receipt.Attempt != proof.Cohort.Attempt ||
		!clusterSSHPreparationResultDigestRE.MatchString(receipt.ConfirmationSHA256) || receipt.ConfirmationSHA256 == strings.Repeat("0", 64) ||
		len(receipt.Targets) != len(proof.Cohort.Targets) {
		return fail()
	}
	for index, target := range proof.Cohort.Targets {
		result := receipt.Targets[index]
		if !result.validProof(proof.Cohort.OperationID, target.Binding.TargetID, proof.Cohort.Attempt, target.Generation) ||
			result.ConfirmationSHA256 != receipt.ConfirmationSHA256 {
			return fail()
		}
	}
	return receipt, nil
}

// completeClusterSSHPreparation atomically settles a whole paired preparation
// cohort. Future dispatch must supply results only after every remote journal
// and joined scope succeeds. This function deliberately has no runtime caller
// until that proof-producing dispatcher exists.
func (s *postgresOperatorStore) completeClusterSSHPreparation(ctx context.Context, completions []clusterSSHPreparationCompletion) error {
	if s == nil || s.db == nil || ctx.Err() != nil || len(completions) < 1 || len(completions) > 2 {
		return errClusterConflict
	}
	first := completions[0].Authority
	payloadDigest := sha256.Sum256([]byte(first.payload))
	if !validClusterSSHPreparationLease(first.Lease) || first.Recovery != nil || first.Baseline.Validate() != nil ||
		first.PayloadSHA256 != hex.EncodeToString(payloadDigest[:]) || len(first.payload) == 0 || len(first.payload) > 256<<10 || !json.Valid([]byte(first.payload)) ||
		validateClusterSSHInspectionCohort(first.Cohort, first.Source) != nil || len(first.Cohort.Targets) != len(completions) ||
		first.Queue.ActiveOperationID != first.Lease.OperationID || first.Queue.ClusterID != first.Source.ClusterID ||
		first.Queue.Release != first.Baseline.Release || first.Queue.SHA != first.Baseline.SourceSHA {
		return errClusterConflict
	}
	inspection, _, err := parseClusterSSHPreparationInspection([]byte(first.payload), first.Lease, first.Baseline)
	if err != nil {
		return errClusterConflict
	}
	authorization, _, err := parseClusterSSHPreparationAuthorization(first.payload, inspection, first.Queue)
	if err != nil {
		return errClusterConflict
	}
	byTarget := make(map[string]clusterSSHPreparationCompletion, len(completions))
	confirmation := authorization.ConfirmationSHA256
	stableCohort := first.Cohort
	stableCohort.ObservedAt = 0
	inspectedCohort := inspection.Cohort
	inspectedCohort.ObservedAt = 0
	if !reflect.DeepEqual(stableCohort, inspectedCohort) || !reflect.DeepEqual(first.Source, inspection.Source) {
		return errClusterConflict
	}
	for _, completion := range completions {
		authority := completion.Authority
		stable := authority.Cohort
		stable.ObservedAt = 0
		if !completion.Result.valid(authority.Lease) || completion.Result.ConfirmationSHA256 != confirmation || byTarget[authority.Lease.TargetID].Result.Version != 0 ||
			authority.Recovery != nil || authority.payload != first.payload || authority.PayloadSHA256 != first.PayloadSHA256 ||
			authority.Baseline != first.Baseline || authority.K3sVersion != first.K3sVersion || authority.Queue != first.Queue ||
			validateClusterSSHInspectionCohort(authority.Cohort, authority.Source) != nil || !reflect.DeepEqual(stable, stableCohort) || !reflect.DeepEqual(authority.Source, first.Source) ||
			authority.Lease.OperationID != first.Lease.OperationID || authority.Lease.OperationAttempt != first.Lease.OperationAttempt ||
			authority.Lease.ControllerHolder != first.Lease.ControllerHolder || !completion.Sealed.binding.valid() ||
			completion.Sealed.binding.OperationID != authority.Lease.OperationID || completion.Sealed.binding.TargetID != authority.Lease.TargetID ||
			completion.Sealed.generation == "" || len(completion.Sealed.generation) > 16<<10 ||
			!strings.HasPrefix(completion.Sealed.ciphertext, aegisEnvelopePrefix) || len(completion.Sealed.ciphertext) > 256<<10 {
			return errClusterConflict
		}
		byTarget[authority.Lease.TargetID] = completion
	}
	receipt := clusterSSHPreparationReceipt{Version: 1, Attempt: first.Lease.OperationAttempt, ConfirmationSHA256: confirmation}
	for i, target := range first.Cohort.Targets {
		completion, ok := byTarget[target.Binding.TargetID]
		credentialDigest, err := clusterSSHPreparationDigest([]string{completion.Sealed.generation, completion.Sealed.ciphertext})
		reserved := authorization.Targets[i]
		if !ok || completion.Sealed.binding != target.Binding || completion.Authority.Lease.Generation != reserved.QualificationGeneration+1 ||
			err != nil || credentialDigest != reserved.CredentialSHA256 {
			return errClusterConflict
		}
		receipt.Targets = append(receipt.Targets, completion.Result)
	}
	payload := map[string]json.RawMessage{}
	var existingReceipt json.RawMessage
	if clusterBootstrapObject([]byte(first.payload), map[string]any{"ssh_preparation": &existingReceipt}) != nil || len(existingReceipt) != 0 ||
		json.Unmarshal([]byte(first.payload), &payload) != nil {
		return errClusterConflict
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil || len(rawReceipt) > 8<<10 {
		return errClusterConflict
	}
	payload["ssh_preparation"] = rawReceipt
	updated, err := json.Marshal(payload)
	if err != nil || len(updated) > 256<<10 {
		return errClusterConflict
	}

	targets := slices.Clone(first.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys := map[string]string{}, map[string]string{}
	for _, target := range targets {
		report, marshalErr := json.Marshal(target.Report)
		if marshalErr != nil {
			return errClusterConflict
		}
		reports[target.Binding.TargetID] = string(report)
		keys[target.Binding.TargetID] = base64.StdEncoding.EncodeToString(target.Key.PublicKey)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, first.Lease.OperationID, targets) != nil {
		return errClusterConflict
	}
	members := slices.Clone(first.Source.Members)
	slices.SortFunc(members, func(a, b clusterSSHSourceMember) int { return strings.Compare(a.NodeID, b.NodeID) })
	var present int
	for _, member := range members {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2 AND management_ip=$3
 AND membership_state='Active' AND application_state='active' FOR SHARE`, member.NodeID, member.Name, member.Address).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	currentSource, err := readClusterSSHQueueSource(ctx, tx)
	if err != nil || currentSource != first.Queue {
		return errClusterConflict
	}
	var now int64
	err = tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$5 AND c.baseline_release=$6 AND c.baseline_sha=$7
 AND c.active_size=$8 AND c.desired_size=$9 AND c.status=$10 AND c.hmr_state=$11 AND c.control_plane_vip=$12 AND c.edge_vip=$13
 AND l.name=$14 AND l.holder=$15 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$16`, first.Lease.OperationID,
		clusterSSHPreparationOperationStep, first.Lease.OperationAttempt, first.payload, first.Source.ClusterID, first.Baseline.Release,
		first.Baseline.SourceSHA, first.Source.ActiveSize, first.Source.DesiredSize, first.Source.Status, first.Source.HMRState,
		first.Source.ControlPlaneVIP, first.Source.EdgeVIP, clusterControllerLeaseName, first.Lease.ControllerHolder, len(targets)).Scan(&now)
	if errors.Is(err, sql.ErrNoRows) {
		return idempotentClusterSSHPreparationCompletion(ctx, tx, first, byTarget, targets, string(updated))
	}
	if err != nil {
		return errClusterUnavailable
	}
	for _, target := range targets {
		completion := byTarget[target.Binding.TargetID]
		lease, sealed := completion.Authority.Lease, completion.Sealed
		result, execErr := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets t SET state='prepared',current_step=$2,
 lease_holder='',lease_expires_at=0,updated_at=$3
 FROM engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a
 WHERE t.id=$1 AND t.operation_id=$4 AND t.cluster_id=$5 AND t.operation_attempt=$6
 AND t.management_ip=$7 AND t.ssh_port=$8 AND t.host_key_algorithm=$9 AND t.host_key_fingerprint=$10 AND t.host_key_base64=$11
 AND t.ordinal=$12 AND t.inspected_attempt=$6 AND t.inspected_generation=$13 AND t.inspected_at=$14 AND t.inspection_json=$15
 AND t.state='running' AND t.current_step='stage_source' AND t.lease_holder=$16 AND t.lease_generation=$17
 AND t.lease_expires_at>extract(epoch FROM clock_timestamp()) AND t.credential_state='available'
 AND p.target_id=t.id AND p.aegis_generation=$18 AND p.ciphertext=$19 AND p.expires_at>extract(epoch FROM clock_timestamp())
 AND a.id=1 AND a.verification_token=p.aegis_generation`, lease.TargetID, clusterSSHPreparationCompleteStep, now,
			lease.OperationID, target.Binding.ClusterID, lease.OperationAttempt, target.Binding.Address, target.Binding.Port,
			target.Key.Algorithm, target.Key.Fingerprint, keys[target.Binding.TargetID], target.Ordinal, target.Generation,
			target.InspectedAt, reports[target.Binding.TargetID], lease.Holder, lease.Generation, sealed.generation, sealed.ciphertext)
		if execErr != nil {
			return errClusterUnavailable
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return errClusterConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=$2,updated_at=$3
 WHERE id=$1 AND payload_json=$4 AND state='running' AND current_step=$5 AND attempt=$6`, first.Lease.OperationID,
		string(updated), now, first.payload, clusterSSHPreparationOperationStep, first.Lease.OperationAttempt)
	if err != nil {
		return errClusterUnavailable
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errClusterConflict
	}
	if insertClusterEvent(ctx, tx, first.Lease.OperationID, "", first.Source.ClusterID, "ssh_preparation_complete",
		clusterSSHPreparationCompleteStep, "Paired SSH preparation journals proved; identity recovery remains fenced.",
		map[string]any{"attempt": first.Lease.OperationAttempt, "confirmation_sha256": confirmation, "target_count": len(targets)}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}

func idempotentClusterSSHPreparationCompletion(ctx context.Context, tx *sql.Tx, first clusterSSHPreparationAuthority,
	byTarget map[string]clusterSSHPreparationCompletion, targets []clusterSSHInspectedTarget, payload string) error {
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$5 AND c.baseline_release=$6 AND c.baseline_sha=$7
 AND l.name=$8 AND l.holder=$9 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$10`, first.Lease.OperationID,
		clusterSSHPreparationOperationStep, first.Lease.OperationAttempt, payload, first.Source.ClusterID, first.Baseline.Release,
		first.Baseline.SourceSHA, clusterControllerLeaseName, first.Lease.ControllerHolder, len(targets)).Scan(&present) != nil {
		return errClusterConflict
	}
	for _, target := range targets {
		completion := byTarget[target.Binding.TargetID]
		report, err := json.Marshal(target.Report)
		if err != nil {
			return errClusterConflict
		}
		key := base64.StdEncoding.EncodeToString(target.Key.PublicKey)
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets t,engine.cluster_onboarding_credentials p,
 engine.aegis_cipher_state a WHERE t.id=$1 AND t.operation_id=$2 AND t.cluster_id=$3 AND t.operation_attempt=$4
 AND t.management_ip=$5 AND t.ssh_port=$6 AND t.host_key_algorithm=$7 AND t.host_key_fingerprint=$8 AND t.host_key_base64=$9
 AND t.ordinal=$10 AND t.inspected_attempt=$4 AND t.inspected_generation=$11 AND t.inspected_at=$12 AND t.inspection_json=$13
 AND t.state='prepared' AND t.current_step=$14 AND t.lease_holder='' AND t.lease_generation=$15 AND t.lease_expires_at=0
 AND t.credential_state='available' AND p.target_id=t.id AND p.aegis_generation=$16 AND p.ciphertext=$17
 AND p.expires_at>extract(epoch FROM clock_timestamp()) AND a.id=1 AND a.verification_token=p.aegis_generation`,
			target.Binding.TargetID, first.Lease.OperationID, first.Source.ClusterID, first.Lease.OperationAttempt, target.Binding.Address,
			target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint, key, target.Ordinal, target.Generation, target.InspectedAt,
			string(report), clusterSSHPreparationCompleteStep, completion.Authority.Lease.Generation, completion.Sealed.generation,
			completion.Sealed.ciphertext).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	return nil
}
