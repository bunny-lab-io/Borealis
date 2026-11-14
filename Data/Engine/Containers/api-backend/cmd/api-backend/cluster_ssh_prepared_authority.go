package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
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
		operation.State != "running" || !clusterSSHPreparedAuthorityPhase(operation.CurrentStep) || operation.Attempt < 1 {
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
	binding, err := readClusterSSHPreparedStoreBinding(ctx, c.store.db, a, operation, c.holder)
	if err != nil {
		return fail()
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	now, err := binding.lock(ctx, tx)
	if err != nil || ctx.Err() != nil || tx.Commit() != nil {
		return fail()
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
