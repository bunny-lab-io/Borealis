package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync/atomic"
	"time"
)

// Full private bytes are reacquired from the original named Secret, never
// recovered from a digest or inferred from defaults. Every read must reproduce
// the approved receipt, including Secret UID/revision/content and all networks.
// A missing/replaced original observation makes recovery unavailable.
func (c *clusterController) preparationRecoverySourceRead(value clusterSSHPreparationRecoveryAuthority, payload string,
	check func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
	var runner *kubernetesClusterStepRunner
	if c != nil {
		runner, _ = c.runner.(*kubernetesClusterStepRunner)
	}
	var version string
	_ = clusterBootstrapObject([]byte(payload), map[string]any{"source_k3s_version": &version})
	get := func(ctx context.Context, path string, out any) error {
		if runner == nil || runner.kube == nil || runner.namespace != "borealis" || runner.controllerHolder != value.Reservation.ControllerHolder {
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
		if runner == nil || runner.controllerHolder != value.Reservation.ControllerHolder || check == nil || check(ctx) != nil {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
		}
		observed, err := runner.readSSHSourceObservation(ctx, member, value.Reservation.OperationID, version, "", false, func() bool { return check(ctx) == nil })
		return observed.Network, err
	}
	return newClusterSSHPreparationRecoverySourceRead(value, payload, check, get, network)
}

type clusterSSHPreparationRecoverySource struct {
	Expected []clusterbootstrap.PreparationExpected
	Settings map[string]string
}
type clusterSSHPreparationRecoverySourceRead func(context.Context) (clusterSSHPreparationRecoverySource, error)

func (clusterSSHPreparationRecoverySource) String() string {
	return "preparation recovery source [redacted]"
}
func (clusterSSHPreparationRecoverySource) GoString() string {
	return "preparation recovery source [redacted]"
}

// This constructor is private composition, not a capability. Only the issued
// claim's proof scope supplies check; no initial PreparationAuthority is made.
func newClusterSSHPreparationRecoverySourceRead(value clusterSSHPreparationRecoveryAuthority, payload string, check func(context.Context) error,
	getJSON func(context.Context, string, any) error, network clusterSSHSourceNetworkRead) clusterSSHPreparationRecoverySourceRead {
	wire, wireErr := json.Marshal(value)
	var owned clusterSSHPreparationRecoveryAuthority
	decodeErr := json.Unmarshal(wire, &owned)
	var version string
	var raw json.RawMessage
	payloadErr := clusterBootstrapObject([]byte(payload), map[string]any{"source_k3s_version": &version, "ssh_preparation_readiness": &raw})
	var readiness clusterSSHPreparationReadinessReceipt
	readinessErr := json.Unmarshal(raw, &readiness)
	record, recordErr := parseClusterSSHPreparationRecovery(payload, clusterSSHPreparationWork{Authorization: owned.Authorization})
	valid := wireErr == nil && decodeErr == nil && payloadErr == nil && readinessErr == nil && recordErr == nil &&
		reflect.DeepEqual(record, owned.Reservation) && parseClusterSSHPreparationReadiness(raw, owned.Authorization) == nil &&
		owned.Baseline.Validate() == nil && validateClusterSSHInspectionCohort(owned.Cohort, owned.Source) == nil &&
		len(owned.Leases) == len(owned.Cohort.Targets) && len(owned.Leases) == len(record.Targets) &&
		check != nil && getJSON != nil && network != nil
	if !valid {
		return unavailableClusterSSHPreparationRecoverySourceRead
	}
	projection := clusterSSHPreparationRecoverySourceProjection{Baseline: owned.Baseline, Cohort: owned.Cohort, Source: owned.Source,
		PlanSHA256: record.PlanSHA256, Version: version, Approved: readiness.Checks[4].SHA256, TargetCount: len(owned.Leases)}
	return newClusterSSHPreparationRecoverySourceProjectionRead(projection, check, getJSON, network,
		func(index int, version, pods, services string) (clusterbootstrap.PreparationExpected, error) {
			return buildClusterSSHPreparationRecoveryExpected(owned, index, version, pods, services)
		})
}

// Immutable read inputs only, not a lease or authority conversion. Each typed
// wrapper validates and owns its complete record before lending this projection.
type clusterSSHPreparationRecoverySourceProjection struct {
	Baseline                      clusterbootstrap.Expected
	Cohort                        clusterSSHInspectionCohort
	Source                        clusterSSHSourceCohort
	PlanSHA256, Version, Approved string
	TargetCount                   int
}

func unavailableClusterSSHPreparationRecoverySourceRead(context.Context) (clusterSSHPreparationRecoverySource, error) {
	return clusterSSHPreparationRecoverySource{}, clusterbootstrap.ErrPreparationConfig
}

func newClusterSSHPreparationRecoverySourceProjectionRead(projection clusterSSHPreparationRecoverySourceProjection,
	check func(context.Context) error, getJSON func(context.Context, string, any) error, network clusterSSHSourceNetworkRead,
	build func(int, string, string, string) (clusterbootstrap.PreparationExpected, error)) clusterSSHPreparationRecoverySourceRead {
	if check == nil || getJSON == nil || network == nil || build == nil {
		return unavailableClusterSSHPreparationRecoverySourceRead
	}
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	return func(parent context.Context) (clusterSSHPreparationRecoverySource, error) {
		fail := func() (clusterSSHPreparationRecoverySource, error) {
			failed.Store(true)
			return clusterSSHPreparationRecoverySource{}, clusterbootstrap.ErrPreparationConfig
		}
		if failed.Load() || parent.Err() != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail()
		}
		if failed.Load() || check(ctx) != nil {
			return fail()
		}
		get := func(ctx context.Context, path string, out any) error {
			if check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			if getJSON(ctx, path, out) != nil || check(ctx) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		readSource := func() error {
			current, err := observeClusterSSHSourceCohort(ctx, get, projection.Source)
			if err != nil || !reflect.DeepEqual(current, projection.Source) {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		readNetworks := func() ([]clusterbootstrap.SourceNetwork, error) {
			values := make([]clusterbootstrap.SourceNetwork, 0, len(projection.Source.Members))
			for _, member := range projection.Source.Members {
				if check(ctx) != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				observed, err := network(ctx, member)
				if err != nil || check(ctx) != nil || ctx.Err() != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				values = append(values, observed)
			}
			if validateClusterSSHPlannedSourceNetworks(projection.Cohort, projection.Source, projection.Version, values) != nil {
				return nil, clusterbootstrap.ErrPreparationConfig
			}
			return values, nil
		}
		readSecret := func() (clusterbootstrap.PreparationRuntimeSecret, error) {
			var raw json.RawMessage
			if get(ctx, clusterSSHRuntimeSecretPath, &raw) != nil {
				return clusterbootstrap.PreparationRuntimeSecret{}, clusterbootstrap.ErrPreparationConfig
			}
			defer clear(raw)
			return clusterbootstrap.ParsePreparationRuntimeSecret(raw)
		}
		if readSource() != nil {
			return fail()
		}
		sources, err := readNetworks()
		if err != nil {
			return fail()
		}
		secret, err := readSecret()
		if err != nil {
			return fail()
		}
		proof := clusterSSHParentProof{Version: 1, Cohort: projection.Cohort, Source: projection.Source}
		digest, err := clusterSSHPreparationConfigurationReceipt(projection.Baseline, proof, projection.PlanSHA256, projection.Version, secret, sources)
		if err != nil || digest != projection.Approved {
			return fail()
		}
		again, err := readNetworks()
		if err != nil || !slices.Equal(sources, again) || readSource() != nil {
			return fail()
		}
		current, err := readSecret()
		if err != nil || !secret.SameObservation(current) || check(ctx) != nil || ctx.Err() != nil || failed.Load() {
			return fail()
		}
		result := clusterSSHPreparationRecoverySource{Settings: secret.Settings()}
		for i := 0; i < projection.TargetCount; i++ {
			expected, err := build(i, projection.Version, sources[0].PodCIDR, sources[0].ServiceCIDR)
			if err != nil {
				return fail()
			}
			result.Expected = append(result.Expected, expected)
		}
		return result, nil
	}
}

func buildClusterSSHPreparationRecoveryExpected(value clusterSSHPreparationRecoveryAuthority, index int, version, pods, services string) (clusterbootstrap.PreparationExpected, error) {
	fail := func() (clusterbootstrap.PreparationExpected, error) {
		return clusterbootstrap.PreparationExpected{}, clusterbootstrap.ErrPreparationConfig
	}
	if index < 0 || index >= len(value.Leases) || len(value.Leases) != len(value.Reservation.Targets) || len(value.Leases) != len(value.Cohort.Targets) ||
		len(value.Leases) != len(value.Authorization.Targets) || value.Baseline.Validate() != nil || validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil {
		return fail()
	}
	for i, lease := range value.Leases {
		prior, approved := value.Reservation.Targets[i], value.Authorization.Targets[i]
		if lease.OperationKind != "ssh_onboarding" || lease.OperationStep != clusterSSHPreparationRecoveryOperationStep || lease.Step != clusterSSHPreparationRecoveryClaimStep ||
			lease.TargetID != prior.TargetID || lease.TargetID != value.Cohort.Targets[i].Binding.TargetID || lease.TargetID != approved.TargetID ||
			lease.Generation != prior.Generation || prior.Generation != prior.PreviousGeneration+1 || prior.PreviousGeneration != approved.QualificationGeneration+1 ||
			lease.Holder == prior.PreviousHolder || !clusterUUIDRE.MatchString(lease.Holder) || lease.Holder == "00000000-0000-0000-0000-000000000000" ||
			lease.Holder != value.Leases[0].Holder || lease.ControllerHolder != value.Reservation.ControllerHolder || lease.ControllerHolder != value.Cohort.ControllerHolder ||
			lease.OperationID != value.Reservation.OperationID || lease.OperationID != value.Cohort.OperationID || lease.OperationAttempt != value.Reservation.Attempt || lease.OperationAttempt != value.Cohort.Attempt {
			return fail()
		}
	}
	return assembleClusterSSHPreparationExpected(value.Cohort, value.Source, value.Leases[index], value.Baseline, version, pods, services)
}
