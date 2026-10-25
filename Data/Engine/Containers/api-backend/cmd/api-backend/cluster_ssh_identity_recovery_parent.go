package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
)

type clusterSSHIdentityRecoveryReservation struct {
	Operation clusterControllerOperation
	Baseline  clusterbootstrap.Expected
	Proof     clusterSSHParentProof
	Receipt   clusterSSHPreparationReceipt
	Target    clusterSSHInspectedTarget
	Payload   string
}

func completedClusterSSHQualification(report *clusterSSHQualificationReport) bool {
	if report == nil || !report.valid(report.Attempt) || len(report.Checks) != len(clusterSSHQualificationCodes) {
		return false
	}
	for index := 0; index < len(report.Checks)-1; index++ {
		if report.Checks[index].State != "passed" {
			return false
		}
	}
	return report.Checks[len(report.Checks)-1].State == "pending"
}

// Preparation completion is reserved for the future guarded preparation
// dispatcher. Once that separate phase has marked every target prepared, the
// original controller may select one drained Kubernetes Node for identity
// recovery. This read returns its connection before Kubernetes observation.
func (c *clusterController) loadClusterSSHIdentityRecoveryReservation(ctx context.Context, operation clusterControllerOperation) (clusterSSHIdentityRecoveryReservation, error) {
	fail := func() (clusterSSHIdentityRecoveryReservation, error) {
		return clusterSSHIdentityRecoveryReservation{}, errClusterConflict
	}
	if c == nil || c.store == nil || c.store.db == nil || operation.Kind != "ssh_onboarding" || operation.State != "running" ||
		operation.CurrentStep != clusterSSHPreparationOperationStep || !clusterUUIDRE.MatchString(operation.ID) || operation.Attempt < 1 {
		return fail()
	}
	var raw, controller, release, sha, targetID string
	var attempt int64
	err := c.store.db.QueryRowContext(ctx, `SELECT o.payload_json,o.attempt,l.holder,c.baseline_release,c.baseline_sha,t.id
 FROM engine.cluster_operations o
 JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_application_leases l ON l.name=$2 AND l.holder=$3 AND l.expires_at>extract(epoch FROM clock_timestamp())
 JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id AND t.state='prepared' AND t.current_step=$4
  AND t.credential_state='available' AND t.lease_holder='' AND t.lease_expires_at=0
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id AND p.expires_at>extract(epoch FROM clock_timestamp())
 JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$5 AND o.attempt=$6
 ORDER BY t.ordinal,t.id LIMIT 1`, operation.ID, clusterControllerLeaseName, c.holder, clusterSSHPreparationCompleteStep,
		clusterSSHPreparationOperationStep, operation.Attempt).Scan(&raw, &attempt, &controller, &release, &sha, &targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return fail()
	}
	if err != nil {
		return clusterSSHIdentityRecoveryReservation{}, errClusterUnavailable
	}
	if attempt != operation.Attempt || controller != c.holder {
		return fail()
	}
	baseline := clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: release, SourceSHA: sha, AllowQualification: strings.Contains(release, "-rc.")}
	if baseline.Validate() != nil {
		return fail()
	}
	anchor := clusterSSHTargetLease{OperationID: operation.ID, TargetID: targetID, ControllerHolder: controller,
		OperationAttempt: attempt, OperationStep: clusterSSHPreparationOperationStep, OperationKind: "ssh_onboarding"}
	proof, _, err := parseClusterSSHPreparationInspection([]byte(raw), anchor, baseline)
	if err != nil {
		return fail()
	}
	receipt, err := parseClusterSSHPreparationReceipt(raw, proof)
	if err != nil {
		return fail()
	}
	var qualificationRaw json.RawMessage
	if clusterBootstrapObject([]byte(raw), map[string]any{"ssh_qualification": &qualificationRaw}) != nil || len(qualificationRaw) == 0 {
		return fail()
	}
	qualification, err := parseClusterSSHQualificationReport(string(qualificationRaw), attempt)
	if err != nil || !completedClusterSSHQualification(qualification) {
		return fail()
	}
	for _, target := range proof.Cohort.Targets {
		if target.Binding.TargetID == targetID {
			return clusterSSHIdentityRecoveryReservation{Operation: operation, Baseline: baseline, Proof: proof, Receipt: receipt, Target: target, Payload: raw}, nil
		}
	}
	return fail()
}

type clusterSSHIdentityRecoveryDiscoveryNode struct {
	Metadata struct {
		Name              string            `json:"name"`
		UID               string            `json:"uid"`
		DeletionTimestamp *string           `json:"deletionTimestamp"`
		Labels            map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		NodeInfo struct {
			MachineID string `json:"machineID"`
			BootID    string `json:"bootID"`
		} `json:"nodeInfo"`
		Addresses []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
		} `json:"addresses"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

// Discover only the immutable target Node UID, then repeat the complete source
// and target proof through readClusterSSHIdentityRecoveryNodes. No identity is
// inferred from hostname, address or provisioning ID.
func discoverClusterSSHIdentityRecoveryRequest(ctx context.Context, getJSON func(context.Context, string, any) error,
	reservation clusterSSHIdentityRecoveryReservation) (clusterbootstrap.IdentityRecoveryRequest, error) {
	fail := func() (clusterbootstrap.IdentityRecoveryRequest, error) {
		return clusterbootstrap.IdentityRecoveryRequest{}, errClusterConflict
	}
	if getJSON == nil || reservation.Operation.ID == "" || reservation.Target.Binding.TargetID == "" ||
		validateClusterSSHInspectionCohort(reservation.Proof.Cohort, reservation.Proof.Source) != nil || len(reservation.Proof.Source.Members) == 0 || ctx.Err() != nil {
		return fail()
	}
	readNamespace := func() (string, error) {
		var namespace struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if getJSON(ctx, "/api/v1/namespaces/kube-system", &namespace) != nil || namespace.Metadata.UID != reservation.Proof.Source.KubeSystemUID || ctx.Err() != nil {
			return "", errClusterConflict
		}
		return namespace.Metadata.UID, nil
	}
	before, err := readNamespace()
	if err != nil {
		return fail()
	}
	var nodes struct {
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
		Items *[]clusterSSHIdentityRecoveryDiscoveryNode `json:"items"`
	}
	if getJSON(ctx, "/api/v1/nodes", &nodes) != nil || nodes.Items == nil || nodes.Metadata.Continue != "" || ctx.Err() != nil {
		return fail()
	}
	targetUID := ""
	for _, node := range *nodes.Items {
		if node.Metadata.Name != reservation.Target.Report.Hostname {
			continue
		}
		internal, ready := "", ""
		for _, address := range node.Status.Addresses {
			if address.Type == "InternalIP" {
				if internal != "" {
					return fail()
				}
				internal = address.Address
			}
		}
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" {
				if ready != "" {
					return fail()
				}
				ready = condition.Status
			}
		}
		if targetUID != "" || !clusterUUIDRE.MatchString(node.Metadata.UID) || node.Metadata.DeletionTimestamp != nil ||
			node.Status.NodeInfo.MachineID != reservation.Target.Report.MachineID || node.Status.NodeInfo.BootID != reservation.Target.Report.BootID ||
			internal != reservation.Target.Binding.Address || ready == "" || node.Metadata.Labels["borealis.io/engine-node"] != "true" ||
			node.Metadata.Labels["borealis.io/application-state"] != "drained" {
			return fail()
		}
		targetUID = node.Metadata.UID
	}
	after, err := readNamespace()
	if err != nil || before != after || targetUID == "" || ctx.Err() != nil {
		return fail()
	}
	source := reservation.Proof.Source.Members[0]
	recovery := clusterbootstrap.IdentityRecoveryRequest{SourceID: source.NodeID, SourceNodeUID: source.NodeUID, TargetNodeUID: targetUID}
	binding := clusterbootstrap.SessionBinding{ClusterID: reservation.Proof.Source.ClusterID, OperationID: reservation.Operation.ID,
		TargetID: reservation.Target.Binding.TargetID, HolderID: "00000000-0000-4000-8000-000000000001", Generation: 1,
		OperationAttempt: reservation.Operation.Attempt, Address: reservation.Target.Binding.Address, Port: reservation.Target.Binding.Port,
		Hostname: reservation.Target.Report.Hostname, MachineID: reservation.Target.Report.MachineID,
		HostKeyAlgorithm: reservation.Target.Key.Algorithm, HostKeyFingerprint: reservation.Target.Key.Fingerprint}
	if recovery.Validate(binding) != nil || readClusterSSHIdentityRecoveryNodes(ctx, getJSON, reservation.Proof.Source, reservation.Target, binding, recovery) != nil {
		return fail()
	}
	return recovery, nil
}

func (c *clusterController) commitClusterSSHIdentityRecoveryReservation(ctx context.Context,
	reservation clusterSSHIdentityRecoveryReservation, recovery clusterbootstrap.IdentityRecoveryRequest) error {
	if c == nil || c.store == nil || recovery.Validate(clusterbootstrap.SessionBinding{ClusterID: reservation.Target.Binding.ClusterID,
		OperationID: reservation.Operation.ID, TargetID: reservation.Target.Binding.TargetID,
		HolderID: "00000000-0000-4000-8000-000000000001", Generation: 1, OperationAttempt: reservation.Operation.Attempt,
		Address: reservation.Target.Binding.Address, Port: reservation.Target.Binding.Port, Hostname: reservation.Target.Report.Hostname,
		MachineID: reservation.Target.Report.MachineID, HostKeyAlgorithm: reservation.Target.Key.Algorithm,
		HostKeyFingerprint: reservation.Target.Key.Fingerprint}) != nil {
		return errClusterConflict
	}
	receipt, err := parseClusterSSHPreparationReceipt(reservation.Payload, reservation.Proof)
	if err != nil || !reflect.DeepEqual(receipt, reservation.Receipt) {
		return errClusterConflict
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(reservation.Payload), &payload) != nil || payload["ssh_identity_recovery"] != nil {
		return errClusterConflict
	}
	recoveryRaw, err := json.Marshal(recovery)
	if err != nil {
		return errClusterConflict
	}
	payload["ssh_identity_recovery"] = recoveryRaw
	updated, err := json.Marshal(payload)
	if err != nil || len(updated) > 256<<10 {
		return errClusterConflict
	}
	targets := slices.Clone(reservation.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	members := slices.Clone(reservation.Proof.Source.Members)
	slices.SortFunc(members, func(a, b clusterSSHSourceMember) int { return strings.Compare(a.NodeID, b.NodeID) })

	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 AND active_operation_id=$1 AND cluster_id=$2
 AND baseline_release=$3 AND baseline_sha=$4 AND active_size=$5 AND desired_size=$6 AND status=$7 AND hmr_state=$8
 AND control_plane_vip=$9 AND edge_vip=$10 FOR UPDATE`, reservation.Operation.ID, reservation.Proof.Source.ClusterID,
		reservation.Baseline.Release, reservation.Baseline.SourceSHA, reservation.Proof.Source.ActiveSize, reservation.Proof.Source.DesiredSize,
		reservation.Proof.Source.Status, reservation.Proof.Source.HMRState, reservation.Proof.Source.ControlPlaneVIP,
		reservation.Proof.Source.EdgeVIP).Scan(&present) != nil {
		return errClusterConflict
	}
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 AND kind='ssh_onboarding' AND state='running'
 AND current_step=$2 AND attempt=$3 AND payload_json=$4 FOR UPDATE`, reservation.Operation.ID, clusterSSHPreparationOperationStep,
		reservation.Operation.Attempt, reservation.Payload).Scan(&present) != nil {
		return errClusterConflict
	}
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.aegis_cipher_state WHERE id=1 FOR SHARE`).Scan(&present) != nil {
		return errClusterConflict
	}
	for _, target := range targets {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_credentials p JOIN engine.aegis_cipher_state a
 ON a.id=1 AND a.verification_token=p.aegis_generation WHERE p.target_id=$1
 AND p.expires_at>extract(epoch FROM clock_timestamp()) FOR SHARE OF p`, target.Binding.TargetID).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	for _, target := range targets {
		report, err := json.Marshal(target.Report)
		if err != nil {
			return errClusterConflict
		}
		key := base64.StdEncoding.EncodeToString(target.Key.PublicKey)
		var preparedGeneration int64
		for _, result := range reservation.Receipt.Targets {
			if result.TargetID == target.Binding.TargetID {
				preparedGeneration = result.Generation
				break
			}
		}
		if preparedGeneration == 0 || tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets WHERE id=$1 AND operation_id=$2 AND cluster_id=$3
 AND management_ip=$4 AND ssh_port=$5 AND host_key_algorithm=$6 AND host_key_fingerprint=$7 AND host_key_base64=$8 AND ordinal=$9
 AND operation_attempt=$10 AND inspected_attempt=$10 AND inspected_generation=$11 AND inspected_at=$12 AND inspection_json=$13
 AND state='prepared' AND current_step=$14 AND credential_state='available' AND lease_holder='' AND lease_expires_at=0
 AND lease_generation=$15 FOR UPDATE`, target.Binding.TargetID, reservation.Operation.ID, reservation.Proof.Source.ClusterID,
			target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint, key, target.Ordinal,
			reservation.Operation.Attempt, target.Generation, target.InspectedAt, string(report), clusterSSHPreparationCompleteStep, preparedGeneration).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	for _, member := range members {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2 AND management_ip=$3
 AND membership_state='Active' AND application_state='active' FOR SHARE`, member.NodeID, member.Name, member.Address).Scan(&present) != nil {
			return errClusterConflict
		}
	}
	var now int64
	if tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint`).Scan(&now) != nil {
		return errClusterUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET current_step=$2,updated_at=$3
 WHERE operation_id=$1 AND state='prepared' AND current_step=$4 AND credential_state='available'
 AND lease_holder='' AND lease_expires_at=0`, reservation.Operation.ID, clusterSSHIdentityRecoveryPendingStep, now, clusterSSHPreparationCompleteStep)
	if err != nil {
		return errClusterUnavailable
	}
	if changed, _ := result.RowsAffected(); changed != int64(len(targets)) {
		return errClusterConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET current_step=$2,updated_at=$3
 WHERE id=$1 AND operation_id=$4 AND state='prepared' AND current_step=$5 AND lease_holder='' AND lease_expires_at=0`,
		reservation.Target.Binding.TargetID, clusterSSHIdentityRecoveryTargetStep, now, reservation.Operation.ID, clusterSSHIdentityRecoveryPendingStep)
	if err != nil {
		return errClusterUnavailable
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errClusterConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$2,payload_json=$3,updated_at=$4
 WHERE id=$1 AND state='running' AND current_step=$5 AND attempt=$6 AND payload_json=$7`, reservation.Operation.ID,
		clusterSSHIdentityRecoveryOperationStep, string(updated), now, clusterSSHPreparationOperationStep, reservation.Operation.Attempt, reservation.Payload)
	if err != nil {
		return errClusterUnavailable
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errClusterConflict
	}
	if insertClusterEvent(ctx, tx, reservation.Operation.ID, "", reservation.Proof.Source.ClusterID, "ssh_identity_recovery_reserved", "running",
		"Original controller reserved one drained target for shared identity recovery; consumer restart and admission remain disabled.",
		map[string]any{"attempt": reservation.Operation.Attempt, "source_node_id": recovery.SourceID,
			"target_id": reservation.Target.Binding.TargetID, "step": clusterSSHIdentityRecoveryOperationStep}, now) != nil || tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}

// Recovery execution belongs to the existing Aegis-capable API worker. The
// controller only creates one exact durable reservation, then remains inert.
// It does not restart consumers, activate applications or admit membership.
func (c *clusterController) runSSHIdentityRecoveryParent(parent context.Context, operation clusterControllerOperation,
	getJSON func(context.Context, string, any) error) error {
	if operation.Kind != "ssh_onboarding" || operation.State != "running" {
		return errClusterConflict
	}
	if operation.CurrentStep == clusterSSHIdentityRecoveryOperationStep {
		return nil
	}
	if operation.CurrentStep != clusterSSHPreparationOperationStep {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	reservation, err := c.loadClusterSSHIdentityRecoveryReservation(ctx, operation)
	if err != nil {
		return err
	}
	recovery, err := discoverClusterSSHIdentityRecoveryRequest(ctx, getJSON, reservation)
	if err != nil {
		return err
	}
	return c.commitClusterSSHIdentityRecoveryReservation(ctx, reservation, recovery)
}
