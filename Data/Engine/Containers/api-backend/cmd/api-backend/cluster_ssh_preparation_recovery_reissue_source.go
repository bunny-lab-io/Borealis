package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
)

// Only a separately issued live proof scope supplies value/check. This reader
// reacquires original bytes; it grants no native action or preparation gate.
func (c *clusterController) preparationRecoveryReissuedSourceRead(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string,
	check func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
	var runner *kubernetesClusterStepRunner
	if c != nil {
		runner, _ = c.runner.(*kubernetesClusterStepRunner)
	}
	var version string
	_ = clusterBootstrapObject([]byte(payload), map[string]any{"source_k3s_version": &version})
	original := value.Lineage.Original
	get := func(ctx context.Context, path string, out any) error {
		if runner == nil || runner.kube == nil || runner.namespace != "borealis" || runner.controllerHolder != original.ControllerHolder {
			return clusterbootstrap.ErrPreparationConfig
		}
		if path == "/api/v1/nodes" || path == "/api/v1/namespaces/kube-system" {
			return runner.kube.getClusterSSHNodeCohortJSON(ctx, path, out)
		}
		if path != clusterSSHRuntimeSecretPath {
			return clusterbootstrap.ErrPreparationConfig
		}
		return runner.kube.getClusterSSHPreparationJSON(ctx, path, out)
	}
	network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
		if runner == nil || runner.controllerHolder != original.ControllerHolder || check == nil || check(ctx) != nil {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
		}
		observed, err := runner.readSSHSourceObservation(ctx, member, original.OperationID, version, "", false, func() bool { return check(ctx) == nil })
		return observed.Network, err
	}
	return newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, check, get, network)
}

func validClusterSSHPreparationRecoveryReissuedSource(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string) bool {
	lineage, err := parseClusterSSHPreparationRecoveryLineage(payload, clusterSSHPreparationWork{Authorization: value.Authorization})
	inspection, inspectionErr := clusterSSHPreparationDigest(clusterSSHParentProof{Version: 1, Cohort: value.Cohort, Source: value.Source})
	if err != nil || inspectionErr != nil || inspection != value.Authorization.InspectionSHA256 || len(lineage.Requests) == 0 || !reflect.DeepEqual(lineage, value.Lineage) || value.Baseline.Validate() != nil ||
		validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil || len(value.Leases) != len(value.Cohort.Targets) ||
		len(value.Leases) != len(value.Authorization.Targets) || len(value.ExpiresAt) != len(value.Leases) {
		return false
	}
	original := lineage.Original
	_, holder, targets := lineage.tip()
	if len(value.Leases) != len(targets) {
		return false
	}
	for i, lease := range value.Leases {
		if lease.OperationKind != "ssh_onboarding" || lease.OperationStep != clusterSSHPreparationRecoveryOperationStep || lease.Step != clusterSSHPreparationRecoveryClaimStep ||
			lease.TargetID != targets[i].TargetID || lease.TargetID != value.Cohort.Targets[i].Binding.TargetID || lease.TargetID != value.Authorization.Targets[i].TargetID ||
			lease.Generation != targets[i].Generation || lease.Holder != holder || value.ExpiresAt[lease.TargetID] <= 0 ||
			lease.ControllerHolder != original.ControllerHolder || lease.ControllerHolder != value.Cohort.ControllerHolder ||
			lease.OperationID != original.OperationID || lease.OperationID != value.Cohort.OperationID ||
			lease.OperationAttempt != original.Attempt || lease.OperationAttempt != value.Cohort.Attempt {
			return false
		}
	}
	return true
}

func newClusterSSHPreparationRecoveryReissuedSourceRead(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, check func(context.Context) error,
	getJSON func(context.Context, string, any) error, network clusterSSHSourceNetworkRead) clusterSSHPreparationRecoverySourceRead {
	wire, wireErr := json.Marshal(value)
	var owned clusterSSHPreparationRecoveryReissuedProofAuthority
	decodeErr := json.Unmarshal(wire, &owned)
	var version string
	var raw json.RawMessage
	payloadErr := clusterBootstrapObject([]byte(payload), map[string]any{"source_k3s_version": &version, "ssh_preparation_readiness": &raw})
	var readiness clusterSSHPreparationReadinessReceipt
	if wireErr != nil || decodeErr != nil || payloadErr != nil || json.Unmarshal(raw, &readiness) != nil ||
		!validClusterSSHPreparationRecoveryReissuedSource(owned, payload) || parseClusterSSHPreparationReadiness(raw, owned.Authorization) != nil ||
		check == nil || getJSON == nil || network == nil {
		return unavailableClusterSSHPreparationRecoverySourceRead
	}
	projection := clusterSSHPreparationRecoverySourceProjection{Baseline: owned.Baseline, Cohort: owned.Cohort, Source: owned.Source,
		PlanSHA256: owned.Lineage.Original.PlanSHA256, Version: version, Approved: readiness.Checks[4].SHA256, TargetCount: len(owned.Leases)}
	return newClusterSSHPreparationRecoverySourceProjectionRead(projection, check, getJSON, network,
		func(index int, version, pods, services string) (clusterbootstrap.PreparationExpected, error) {
			return buildClusterSSHPreparationRecoveryReissuedExpected(owned, payload, index, version, pods, services)
		})
}

func buildClusterSSHPreparationRecoveryReissuedExpected(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, index int,
	version, pods, services string) (clusterbootstrap.PreparationExpected, error) {
	if index < 0 || index >= len(value.Leases) || !validClusterSSHPreparationRecoveryReissuedSource(value, payload) {
		return clusterbootstrap.PreparationExpected{}, clusterbootstrap.ErrPreparationConfig
	}
	return assembleClusterSSHPreparationExpected(value.Cohort, value.Source, value.Leases[index], value.Baseline, version, pods, services)
}
