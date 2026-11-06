package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// A settled phase has no worker lease. This snapshot is deliberately separate
// from clusterSSHPreparationAuthority and cannot be passed to native dispatch.
type clusterSSHPreparedAuthority struct {
	payload       string
	baseline      clusterbootstrap.Expected
	proof         clusterSSHParentProof
	receipt       clusterSSHPreparationReceipt
	queue         clusterSSHQueueSource
	authorization clusterSSHPreparationAuthorization
	version       string
}

func (clusterSSHPreparedAuthority) String() string   { return "prepared SSH observation [redacted]" }
func (clusterSSHPreparedAuthority) GoString() string { return "prepared SSH observation [redacted]" }

// Node observation is not admission permission, complete voter agreement,
// capacity proof, consumer health, or a replacement for the original receipts.
type clusterSSHPreparedObservation struct {
	OperationID, Controller, PayloadSHA256, ClusterID, KubeSystemUID string
	Attempt, ObservedAt                                              int64
	Nodes                                                            map[string]clusterbootstrap.K3sJoinedNode
}

func parseClusterSSHPreparedAuthority(payload string, baseline clusterbootstrap.Expected, queue clusterSSHQueueSource,
	operation clusterControllerOperation, holder string) (clusterSSHPreparedAuthority, error) {
	fail := func() (clusterSSHPreparedAuthority, error) { return clusterSSHPreparedAuthority{}, errClusterConflict }
	if baseline.Validate() != nil || holder == "" || len(holder) > 1024 || operation.Kind != "ssh_onboarding" ||
		operation.State != "running" || operation.CurrentStep != clusterSSHPreparationOperationStep || operation.Attempt < 1 {
		return fail()
	}
	// Inspection parser uses only these original parent identities. This is not
	// a live stage_source claim: no worker holder, generation or step is supplied.
	anchor := clusterSSHTargetLease{OperationID: operation.ID, OperationAttempt: operation.Attempt, ControllerHolder: holder}
	proof, version, err := parseClusterSSHPreparationInspection([]byte(payload), anchor, baseline)
	if err != nil {
		return fail()
	}
	for _, id := range []string{proof.Source.ClusterID, proof.Source.KubeSystemUID} {
		if id == "00000000-0000-0000-0000-000000000000" {
			return fail()
		}
	}
	for _, member := range proof.Source.Members {
		for _, id := range []string{member.NodeID, member.NodeUID, member.BootID} {
			if id == "00000000-0000-0000-0000-000000000000" {
				return fail()
			}
		}
	}
	receipt, err := parseClusterSSHPreparationReceiptVersion(payload, proof, 2)
	if err != nil {
		return fail()
	}
	var authorizationRaw, qualificationRaw, confirmationRaw, readinessRaw, recoveryRaw json.RawMessage
	if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_preparation_authorization": &authorizationRaw,
		"ssh_qualification": &qualificationRaw, "ssh_preparation_confirmation": &confirmationRaw,
		"ssh_preparation_readiness": &readinessRaw, "ssh_identity_recovery": &recoveryRaw}) != nil ||
		len(authorizationRaw) == 0 || len(authorizationRaw) > 8<<10 || len(recoveryRaw) != 0 {
		return fail()
	}
	authorization, _, err := parseClusterSSHPreparationAuthorizationContent(authorizationRaw, qualificationRaw, confirmationRaw, readinessRaw, proof, queue)
	if err != nil || authorization.ConfirmationSHA256 != receipt.ConfirmationSHA256 {
		return fail()
	}
	selected, err := clusterSSHPreparationRecordedSource(payload, proof, receipt.ConfirmationSHA256)
	if err != nil {
		return fail()
	}
	source := queue
	source.ActiveOperationID = ""
	count, err := source.validate()
	if err != nil || count != len(proof.Cohort.Targets) || queue.ActiveOperationID != operation.ID ||
		queue.ClusterID != proof.Source.ClusterID || queue.Release != baseline.Release || queue.SHA != baseline.SourceSHA ||
		queue.ActiveSize != proof.Source.ActiveSize || queue.DesiredSize != proof.Source.DesiredSize || queue.Status != proof.Source.Status ||
		queue.HMRState != proof.Source.HMRState || queue.ControlVIP != proof.Source.ControlPlaneVIP || queue.EdgeVIP != proof.Source.EdgeVIP {
		return fail()
	}
	var configured string
	if clusterBootstrapObject([]byte(queue.ConfigJSON), map[string]any{"k3s_version": &configured}) != nil || configured != version {
		return fail()
	}
	stable := proof
	stable.Cohort.ObservedAt = 0
	cohortDigest, err := clusterSSHPreparationDigest(stable)
	if err != nil {
		return fail()
	}
	peers := []string{}
	seenNodes, seenMembers := map[string]bool{}, map[uint64]bool{}
	for _, member := range proof.Source.Members {
		peers = append(peers, member.Address)
		seenNodes[member.NodeUID] = true
	}
	for _, target := range proof.Cohort.Targets {
		peers = append(peers, target.Binding.Address)
	}
	slices.Sort(peers)
	for i, target := range proof.Cohort.Targets {
		result, reserved := receipt.Targets[i], authorization.Targets[i]
		p := result.Prejoin
		e := p.Expected
		b := e.Target
		if result.Generation != reserved.QualificationGeneration+1 || p.Receipt.Source != selected || e.Source != baseline ||
			e.KubeSystemUID != proof.Source.KubeSystemUID || e.CohortSHA256 != cohortDigest || e.K3sVersion != version ||
			e.ControlPlaneVIP != queue.ControlVIP || e.EdgeVIP != queue.EdgeVIP || e.TargetBootID != target.Report.BootID ||
			e.ManagementCIDR != target.Report.ConnectedPrefix || !slices.Equal(e.PeerAddresses, peers) ||
			b.ClusterID != queue.ClusterID || b.Address != target.Binding.Address || b.Port != target.Binding.Port ||
			b.Hostname != target.Report.Hostname || b.MachineID != target.Report.MachineID ||
			b.HostKeyAlgorithm != target.Key.Algorithm || b.HostKeyFingerprint != target.Key.Fingerprint ||
			seenNodes[p.Receipt.Node.NodeUID] || seenMembers[p.Receipt.MemberID] ||
			(i > 0 && (e.PodCIDR != receipt.Targets[0].Prejoin.Expected.PodCIDR || e.ServiceCIDR != receipt.Targets[0].Prejoin.Expected.ServiceCIDR)) {
			return fail()
		}
		seenNodes[p.Receipt.Node.NodeUID], seenMembers[p.Receipt.MemberID] = true, true
	}
	return clusterSSHPreparedAuthority{payload, baseline, proof, receipt, queue, authorization, version}, nil
}

// All parsing and credential hashing occur outside the transaction. Existing
// controller-first locks and a final database clock fence waited-on snapshots.
func (c *clusterController) loadClusterSSHPreparedAuthority(ctx context.Context, operation clusterControllerOperation) (clusterSSHPreparedAuthority, int64, error) {
	fail := func() (clusterSSHPreparedAuthority, int64, error) {
		return clusterSSHPreparedAuthority{}, 0, errClusterConflict
	}
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || !clusterUUIDRE.MatchString(operation.ID) {
		return fail()
	}
	var payload string
	var baseline clusterbootstrap.Expected
	if c.store.db.QueryRowContext(ctx, `SELECT o.payload_json,c.baseline_release,c.baseline_sha FROM engine.cluster_operations o,engine.cluster_state c
 WHERE o.id=$1 AND octet_length(o.payload_json)<=262144 AND c.id=1 AND c.active_operation_id=o.id`, operation.ID).
		Scan(&payload, &baseline.Release, &baseline.SourceSHA) != nil {
		return fail()
	}
	baseline.Repository, baseline.AllowQualification = clusterGitHubRepo(), strings.Contains(baseline.Release, "-rc.")
	queue, err := readClusterSSHQueueSource(ctx, c.store.db)
	if err != nil {
		return fail()
	}
	a, err := parseClusterSSHPreparedAuthority(payload, baseline, queue, operation, c.holder)
	if err != nil {
		return fail()
	}
	targets := slices.Clone(a.proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys := map[string]string{}, map[string]string{}
	for _, target := range targets {
		report, err := json.Marshal(target.Report)
		if err != nil {
			return fail()
		}
		reports[target.Binding.TargetID], keys[target.Binding.TargetID] = string(report), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, operation.ID, targets) != nil ||
		checkClusterSSHQualificationSource(ctx, tx, clusterSSHQualificationWork{Proof: a.proof, Source: queue}) != nil {
		return fail()
	}
	credentials := make([][2]string, len(a.proof.Cohort.Targets))
	for i, target := range a.proof.Cohort.Targets {
		if tx.QueryRowContext(ctx, `SELECT p.aegis_generation,p.ciphertext FROM engine.cluster_onboarding_targets t
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE t.id=$1 AND t.operation_id=$2 AND t.cluster_id=$3 AND t.operation_attempt=$4
 AND t.state='prepared' AND t.current_step=$5 AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=$6
 AND t.credential_state='available' AND t.management_ip=$7 AND t.ssh_port=$8 AND t.host_key_algorithm=$9 AND t.host_key_fingerprint=$10
 AND t.host_key_base64=$11 AND t.ordinal=$12 AND t.inspected_attempt=$4 AND t.inspected_generation=$13 AND t.inspected_at=$14 AND t.inspection_json=$15
 AND octet_length(p.aegis_generation) BETWEEN 1 AND 16384 AND octet_length(p.ciphertext) BETWEEN 1 AND 262144`,
			target.Binding.TargetID, operation.ID, queue.ClusterID, operation.Attempt, clusterSSHPreparationCompleteStep,
			a.receipt.Targets[i].Generation, target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint,
			keys[target.Binding.TargetID], target.Ordinal, target.Generation, target.InspectedAt, reports[target.Binding.TargetID]).
			Scan(&credentials[i][0], &credentials[i][1]) != nil {
			return fail()
		}
	}
	var now int64
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED (SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT floor(moment.now)::bigint FROM moment,engine.cluster_operations o,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND l.name=$5 AND l.holder=$6 AND l.expires_at>moment.now
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$7
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=o.id AND (p.expires_at>moment.now AND t.inspected_at<=moment.now) IS NOT TRUE)`,
		operation.ID, clusterSSHPreparationOperationStep, operation.Attempt, payload, clusterControllerLeaseName, c.holder, len(targets)).Scan(&now) != nil || tx.Commit() != nil {
		return fail()
	}
	for i, credential := range credentials {
		digest, err := clusterSSHPreparationDigest([]string{credential[0], credential[1]})
		if err != nil || digest != a.authorization.Targets[i].CredentialSHA256 || ctx.Err() != nil {
			return fail()
		}
	}
	return a, now, nil
}

func (c *clusterController) observeClusterSSHPrepared(parent context.Context, operation clusterControllerOperation) (clusterSSHPreparedObservation, error) {
	fail := func() (clusterSSHPreparedObservation, error) {
		return clusterSSHPreparedObservation{}, errClusterConflict
	}
	if c == nil || parent.Err() != nil {
		return fail()
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.kube == nil || runner.controllerHolder != c.holder {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, clusterSSHJoinedNodeObservationTimeout)
	defer cancel()
	before, started, err := c.loadClusterSSHPreparedAuthority(ctx, operation)
	if err != nil {
		return fail()
	}

	var nodes map[string]clusterbootstrap.K3sJoinedNode
	for round := 0; round < 2; round++ {
		nodes, err = observeClusterSSHJoinedNodes(ctx, runner.kube.getClusterSSHNodeCohortJSON, before.proof.Cohort, before.proof.Source, before.version)
		if err != nil || len(nodes) != len(before.receipt.Targets) {
			return fail()
		}
		for _, target := range before.receipt.Targets {
			if nodes[target.TargetID] != target.Prejoin.Receipt.Node {
				return fail()
			}
		}
	}
	after, finished, err := c.loadClusterSSHPreparedAuthority(ctx, operation)
	if err != nil || ctx.Err() != nil || finished < started || !reflect.DeepEqual(before, after) || runner.controllerHolder != c.holder {
		return fail()
	}
	payloadSum := sha256.Sum256([]byte(before.payload))
	return clusterSSHPreparedObservation{operation.ID, c.holder, hex.EncodeToString(payloadSum[:]), before.queue.ClusterID,
		before.proof.Source.KubeSystemUID, operation.Attempt, finished, nodes}, nil
}
