package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

const (
	clusterSSHIdentityRecoveryOperationStep = "recover_ssh_identity"
	clusterSSHIdentityRecoveryTargetStep    = "recover_identity"
)

type clusterSSHIdentityRecoveryClaim struct {
	Lease    clusterSSHTargetLease
	Sealed   sealedClusterSSHCredentials
	Key      clusterremote.HostKey
	Binding  clusterbootstrap.SessionBinding
	Recovery clusterbootstrap.IdentityRecoveryRequest
	Source   engineidentity.Binding
	Baseline clusterbootstrap.Expected
}

func validClusterSSHIdentityRecoveryLease(lease clusterSSHTargetLease) bool {
	return clusterUUIDRE.MatchString(lease.OperationID) && clusterUUIDRE.MatchString(lease.TargetID) && clusterUUIDRE.MatchString(lease.Holder) &&
		lease.ControllerHolder != "" && len(lease.ControllerHolder) <= 1024 && lease.Generation > 0 && lease.OperationAttempt > 0 &&
		lease.OperationKind == "ssh_onboarding" && lease.OperationStep == clusterSSHIdentityRecoveryOperationStep && lease.Step == clusterSSHIdentityRecoveryTargetStep
}

func (c clusterSSHIdentityRecoveryClaim) valid() bool {
	return validClusterSSHIdentityRecoveryLease(c.Lease) && c.Sealed.binding.valid() && c.Sealed.binding.OperationID == c.Lease.OperationID &&
		c.Sealed.binding.TargetID == c.Lease.TargetID && c.Sealed.generation != "" && len(c.Sealed.generation) <= 16<<10 &&
		strings.HasPrefix(c.Sealed.ciphertext, aegisEnvelopePrefix) && len(c.Sealed.ciphertext) <= 256<<10 &&
		c.Key.Validate() == nil && c.Key.Fingerprint == c.Sealed.binding.Fingerprint && c.Binding.Validate() == nil &&
		c.Binding.ClusterID == c.Sealed.binding.ClusterID && c.Binding.OperationID == c.Lease.OperationID && c.Binding.TargetID == c.Lease.TargetID &&
		c.Binding.HolderID == c.Lease.Holder && c.Binding.Generation == c.Lease.Generation && c.Binding.OperationAttempt == c.Lease.OperationAttempt &&
		c.Binding.Address == c.Sealed.binding.Address && c.Binding.Port == c.Sealed.binding.Port && c.Binding.HostKeyAlgorithm == c.Key.Algorithm &&
		c.Binding.HostKeyFingerprint == c.Key.Fingerprint && c.Recovery.Validate(c.Binding) == nil && c.Source.ClusterID == c.Binding.ClusterID &&
		c.Source.SourceID == c.Recovery.SourceID && c.Source.SourceUID == c.Recovery.SourceNodeUID && c.Baseline.Validate() == nil
}

func parseClusterSSHIdentityRecovery(raw string, proof clusterSSHParentProof, target clusterSSHInspectedTarget) (clusterbootstrap.IdentityRecoveryRequest, engineidentity.Binding, error) {
	fail := func() (clusterbootstrap.IdentityRecoveryRequest, engineidentity.Binding, error) {
		return clusterbootstrap.IdentityRecoveryRequest{}, engineidentity.Binding{}, errClusterConflict
	}
	var payload map[string]json.RawMessage
	if len(raw) > 256<<10 || json.Unmarshal([]byte(raw), &payload) != nil {
		return fail()
	}
	value, ok := payload["ssh_identity_recovery"]
	if !ok || len(value) > 4<<10 {
		return fail()
	}
	object, err := decodeClusterSSHObject(value, map[string]bool{"source_node_id": true, "source_node_uid": true, "target_node_uid": true})
	if err != nil || len(object) != 3 {
		return fail()
	}
	var recovery clusterbootstrap.IdentityRecoveryRequest
	if json.Unmarshal(value, &recovery) != nil {
		return fail()
	}
	binding := clusterbootstrap.SessionBinding{ClusterID: proof.Source.ClusterID, OperationID: proof.Cohort.OperationID, TargetID: target.Binding.TargetID,
		HolderID: "00000000-0000-4000-8000-000000000001", Generation: 1, OperationAttempt: proof.Cohort.Attempt, Address: target.Binding.Address,
		Port: target.Binding.Port, Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, HostKeyAlgorithm: target.Key.Algorithm, HostKeyFingerprint: target.Key.Fingerprint}
	if recovery.Validate(binding) != nil {
		return fail()
	}
	for _, member := range proof.Source.Members {
		if member.NodeID == recovery.SourceID && member.NodeUID == recovery.SourceNodeUID {
			return recovery, engineidentity.Binding{ClusterID: proof.Source.ClusterID, SourceID: member.NodeID, SourceUID: member.NodeUID}, nil
		}
	}
	return fail()
}

// claimClusterSSHIdentityRecoveryTarget is a reserved mutating claim. No
// controller transition currently enters its operation/target steps. Once a
// reviewed transition does, this claim binds one exact inspected SSH target,
// original source Node identity, target Node UID expectation, credential
// envelope and controller lease before any crypto or network work begins.
func (s *postgresOperatorStore) claimClusterSSHIdentityRecoveryTarget(ctx context.Context, operationID, targetID, holder string) (clusterSSHIdentityRecoveryClaim, error) {
	fail := func() (clusterSSHIdentityRecoveryClaim, error) {
		return clusterSSHIdentityRecoveryClaim{}, errClusterConflict
	}
	if s == nil || s.db == nil || !clusterUUIDRE.MatchString(operationID) || !clusterUUIDRE.MatchString(targetID) || !clusterUUIDRE.MatchString(holder) {
		return fail()
	}
	var payload, controller, release, sha string
	var attempt int64
	err := s.db.QueryRowContext(ctx, `SELECT o.payload_json,o.attempt,l.holder,c.baseline_release,c.baseline_sha
 FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_application_leases l ON l.name=$2 AND l.expires_at>extract(epoch FROM clock_timestamp())
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$3
 AND octet_length(o.payload_json)<=262144`, operationID, clusterControllerLeaseName, clusterSSHIdentityRecoveryOperationStep).
		Scan(&payload, &attempt, &controller, &release, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return fail()
	}
	if err != nil {
		return clusterSSHIdentityRecoveryClaim{}, errClusterUnavailable
	}
	baseline := clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: release, SourceSHA: sha, AllowQualification: strings.Contains(release, "-rc.")}
	anchor := clusterSSHTargetLease{OperationID: operationID, TargetID: targetID, Holder: holder, ControllerHolder: controller, OperationAttempt: attempt,
		OperationStep: clusterSSHIdentityRecoveryOperationStep, Step: clusterSSHIdentityRecoveryTargetStep, OperationKind: "ssh_onboarding"}
	proof, _, err := parseClusterSSHPreparationInspection([]byte(payload), anchor, baseline)
	if err != nil {
		return fail()
	}
	var target clusterSSHInspectedTarget
	found := false
	for _, candidate := range proof.Cohort.Targets {
		if candidate.Binding.TargetID == targetID {
			target, found = candidate, true
			break
		}
	}
	if !found {
		return fail()
	}
	recovery, source, err := parseClusterSSHIdentityRecovery(payload, proof, target)
	if err != nil {
		return fail()
	}
	report, err := json.Marshal(target.Report)
	if err != nil {
		return fail()
	}
	key := base64.StdEncoding.EncodeToString(target.Key.PublicKey)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return clusterSSHIdentityRecoveryClaim{}, errClusterUnavailable
	}
	defer tx.Rollback()
	if err := lockClusterSSHWorkerAuthority(ctx, tx, operationID, targetID); err != nil {
		return clusterSSHIdentityRecoveryClaim{}, err
	}
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets WHERE id=$1 FOR UPDATE`, targetID).Scan(&present) != nil {
		return fail()
	}
	members := slices.Clone(proof.Source.Members)
	slices.SortFunc(members, func(a, b clusterSSHSourceMember) int { return strings.Compare(a.NodeID, b.NodeID) })
	for _, member := range members {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2 AND management_ip=$3
 AND membership_state='Active' AND application_state='active' FOR SHARE`, member.NodeID, member.Name, member.Address).Scan(&present) != nil {
			return fail()
		}
	}
	claim := clusterSSHIdentityRecoveryClaim{Lease: anchor, Key: target.Key, Recovery: recovery, Source: source, Baseline: baseline}
	err = tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_onboarding_targets t SET state='running',lease_holder=$3,lease_generation=t.lease_generation+1,
 lease_expires_at=floor(moment.now)+$4,updated_at=floor(moment.now)
 FROM moment,engine.cluster_operations o,engine.cluster_state c,engine.cluster_onboarding_credentials p,
 engine.aegis_cipher_state a,engine.cluster_application_leases l
 WHERE t.id=$2 AND t.operation_id=$1 AND t.cluster_id=$5 AND t.operation_attempt=$6
 AND t.management_ip=$7 AND t.ssh_port=$8 AND t.host_key_algorithm=$9 AND t.host_key_fingerprint=$10 AND t.host_key_base64=$11
 AND t.ordinal=$12 AND t.inspected_attempt=$6 AND t.inspected_generation=$13 AND t.inspected_at=$14 AND t.inspection_json=$15
 AND t.current_step=$16 AND t.state IN ('prepared','running','recovery_required') AND t.credential_state='available'
 AND t.lease_expires_at<=moment.now AND o.id=t.operation_id AND o.kind='ssh_onboarding' AND o.state='running'
 AND o.current_step=$17 AND o.attempt=$6 AND o.payload_json=$18
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=t.cluster_id AND c.baseline_release=$19 AND c.baseline_sha=$20
 AND c.active_size=$21 AND c.desired_size=$22 AND c.status=$23 AND c.hmr_state=$24
 AND c.control_plane_vip=$25 AND c.edge_vip=$26
 AND l.name=$27 AND l.holder=$28 AND l.expires_at>moment.now
 AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
 RETURNING t.lease_generation,p.aegis_generation,p.ciphertext`, operationID, targetID, holder, clusterSSHTargetLeaseSeconds,
		target.Binding.ClusterID, attempt, target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint, key, target.Ordinal,
		target.Generation, target.InspectedAt, string(report), clusterSSHIdentityRecoveryTargetStep, clusterSSHIdentityRecoveryOperationStep, payload,
		baseline.Release, baseline.SourceSHA, proof.Source.ActiveSize, proof.Source.DesiredSize, proof.Source.Status, proof.Source.HMRState,
		proof.Source.ControlPlaneVIP, proof.Source.EdgeVIP, clusterControllerLeaseName, controller).
		Scan(&claim.Lease.Generation, &claim.Sealed.generation, &claim.Sealed.ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return fail()
	}
	if err != nil {
		return clusterSSHIdentityRecoveryClaim{}, errClusterUnavailable
	}
	claim.Sealed.binding = target.Binding
	claim.Binding = clusterbootstrap.SessionBinding{ClusterID: target.Binding.ClusterID, OperationID: operationID, TargetID: targetID, HolderID: holder,
		Generation: claim.Lease.Generation, OperationAttempt: attempt, Address: target.Binding.Address, Port: target.Binding.Port,
		Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, HostKeyAlgorithm: target.Key.Algorithm, HostKeyFingerprint: target.Key.Fingerprint}
	if !claim.valid() {
		return fail()
	}
	if tx.Commit() != nil {
		return clusterSSHIdentityRecoveryClaim{}, errClusterUnavailable
	}
	return claim, nil
}
