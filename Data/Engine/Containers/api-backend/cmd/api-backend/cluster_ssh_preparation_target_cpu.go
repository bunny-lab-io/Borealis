package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
)

// This typed projection travels with the existing native clean-host receipt.
// Neither it nor its digest grants capacity, approval or mutation authority.
type clusterSSHPreparationTargetCPU struct {
	TargetID          string                                `json:"target_id"`
	ObservationSHA256 string                                `json:"observation_sha256"`
	CPU               clusterbootstrap.PreparationTargetCPU `json:"cpu"`
}

type clusterSSHPreparationCleanObservation struct {
	Digest  string                           `json:"digest"`
	Targets []clusterSSHPreparationTargetCPU `json:"targets"`
	// Private adjacent capacity projection; never supplied by the clean broker.
	Capacity *clusterSSHPreparationCapacityProjection `json:"-"`
}

type clusterSSHPreparationTargetsRead func(context.Context, string) (clusterSSHPreparationCleanObservation, error)

// The clean gate performs the fresh native read. Its adjacent approval step
// projects the same typed result without a second SSH round merely to extract
// fields already observed. Every approval recheck runs all gates first; this
// projection alone is never a freshness/authority capability.
func clusterSSHPreparationTargetCPUReaderPair(snapshot clusterSSHPreparationApprovalSnapshot, read clusterSSHPreparationTargetsRead) (func(context.Context, string) (string, error), clusterSSHPreparationTargetsRead) {
	var mu sync.Mutex
	var retained clusterSSHPreparationCleanObservation
	failed := false
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	clean := func(ctx context.Context, requested string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if read == nil || err != nil || failed || ctx.Err() != nil || requested != plan {
			failed = true
			return "", errClusterConflict
		}
		value, readErr := read(ctx, requested)
		if readErr != nil || value.validate(snapshot) != nil || ctx.Err() != nil || retained.Digest != "" && value.Digest != retained.Digest {
			failed = true
			return "", errClusterConflict
		}
		retained = clusterSSHPreparationCleanObservation{Digest: value.Digest, Targets: slices.Clone(value.Targets)}
		return value.Digest, nil
	}
	projection := func(ctx context.Context, requested string) (clusterSSHPreparationCleanObservation, error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil || failed || ctx.Err() != nil || requested != plan || retained.validate(snapshot) != nil {
			return clusterSSHPreparationCleanObservation{}, errClusterConflict
		}
		return clusterSSHPreparationCleanObservation{Digest: retained.Digest, Targets: slices.Clone(retained.Targets)}, nil
	}
	return clean, projection
}

func clusterSSHPreparationTargetCPUDigest(snapshot clusterSSHPreparationApprovalSnapshot, targets []clusterSSHPreparationTargetCPU) (string, error) {
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	if err != nil || len(targets) != len(snapshot.work.Proof.Cohort.Targets) || len(targets) < 1 || len(targets) > 2 {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	for i, target := range targets {
		expected := snapshot.work.Proof.Cohort.Targets[i]
		if target.TargetID != expected.Binding.TargetID || !validClusterSSHPreparationDigest(target.ObservationSHA256) ||
			target.CPU.Validate(clusterbootstrap.K3sPins().Version) != nil || target.CPU.AllocatableMilli != uint64(expected.Report.CPUCount)*1000 || target.CPU.MemoryKiB != expected.Report.MemoryKiB {
			return "", clusterbootstrap.ErrPreparationConfig
		}
	}
	return clusterSSHPreparationDigest(struct {
		Domain, Plan string
		Targets      []clusterSSHPreparationTargetCPU
	}{"preparation-clean-targets-v3", plan, targets})
}

func (o clusterSSHPreparationCleanObservation) validate(snapshot clusterSSHPreparationApprovalSnapshot) error {
	digest, err := clusterSSHPreparationTargetCPUDigest(snapshot, o.Targets)
	if err != nil || digest != o.Digest {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

func validateClusterSSHPreparationReadinessCPU(r clusterSSHPreparationReadinessReceipt, a clusterSSHPreparationAuthorization) error {
	if (r.Version != 2 && r.Version != 3) || len(r.Targets) != len(a.Targets) || len(r.Targets) < 1 || len(r.Targets) > 2 || len(r.Checks) != 6 {
		return errClusterConflict
	}
	for i, target := range r.Targets {
		if target.TargetID != a.Targets[i].TargetID || !validClusterSSHPreparationDigest(target.ObservationSHA256) || target.CPU.Validate(clusterbootstrap.K3sPins().Version) != nil {
			return errClusterConflict
		}
	}
	digest, err := clusterSSHPreparationDigest(struct {
		Domain, Plan string
		Targets      []clusterSSHPreparationTargetCPU
	}{"preparation-clean-targets-v3", r.PlanSHA256, r.Targets})
	if err != nil || digest != r.Checks[1].SHA256 {
		return errClusterConflict
	}
	if r.Version == 2 && r.Capacity != nil {
		return errClusterConflict
	}
	if r.Version == 3 {
		if r.Capacity == nil || r.Capacity.CleanSHA256 != r.Checks[1].SHA256 || r.Capacity.ConfigurationSHA256 != r.Checks[4].SHA256 {
			return errClusterConflict
		}
		capacity, err := r.Capacity.digest(r.PlanSHA256, r.Targets)
		if err != nil || capacity != r.Checks[2].SHA256 {
			return errClusterConflict
		}
	}
	return nil
}

// Add the typed native projection inside the existing synchronous approval
// capability. Its fresh reader must agree with the clean gate, including every
// later recheck. Approval commits it atomically in the existing readiness JSON.
// The production factory remains blocked until complete capacity/storage exist.
func withClusterSSHPreparationCPUApprovalProof(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot, gates clusterSSHPreparationGates,
	source func(context.Context) error, read clusterSSHPreparationTargetsRead, consume func(context.Context, clusterSSHPreparationApprovalProof) error) error {
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	if err != nil || read == nil || consume == nil {
		return errClusterConflict
	}
	return withClusterSSHPreparationApprovalProof(ctx, plan, gates, source, func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
		observed, err := read(ctx, plan)
		if err != nil || observed.validate(snapshot) != nil || observed.Digest != proof.receipts[1] || observed.Capacity == nil {
			return errClusterConflict
		}
		capacity := observed.Capacity.clone()
		capacityDigest, err := capacity.digest(plan, observed.Targets)
		if err != nil || capacityDigest != proof.receipts[2] || capacity.CleanSHA256 != proof.receipts[1] || capacity.ConfigurationSHA256 != proof.receipts[4] {
			return errClusterConflict
		}
		retained := slices.Clone(observed.Targets)
		proof.targetCPU = slices.Clone(retained)
		proof.capacity = &capacity
		original := proof.recheck
		var failed atomic.Bool
		proof.recheck = func(ctx context.Context) error {
			if failed.Load() || original(ctx) != nil {
				failed.Store(true)
				return errClusterConflict
			}
			fresh, err := read(ctx, plan)
			if err != nil || fresh.validate(snapshot) != nil || fresh.Digest != proof.receipts[1] || !slices.Equal(fresh.Targets, retained) ||
				fresh.Capacity == nil || !reflect.DeepEqual(*fresh.Capacity, capacity) {
				failed.Store(true)
				return errClusterConflict
			}
			return nil
		}
		if err := consume(ctx, proof); err != nil {
			return err
		}
		return proof.recheck(ctx)
	})
}

// Legacy readiness remains readable for its existing reconciliation paths, but
// never gains CPU evidence by inference. Native fresh publication rejects nil.
func clusterSSHPreparationExpectedCPU(payload, targetID string) (*clusterbootstrap.PreparationTargetCPU, error) {
	if payload == "" {
		return nil, nil
	}
	var raw, authorization json.RawMessage
	if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_preparation_readiness": &raw, "ssh_preparation_authorization": &authorization}) != nil {
		return nil, errClusterConflict
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var receipt clusterSSHPreparationReadinessReceipt
	var a clusterSSHPreparationAuthorization
	if json.Unmarshal(raw, &receipt) != nil || json.Unmarshal(authorization, &a) != nil || parseClusterSSHPreparationReadiness(raw, a) != nil {
		return nil, errClusterConflict
	}
	if receipt.Version == 1 {
		return nil, nil
	}
	for _, target := range receipt.Targets {
		if target.TargetID == targetID {
			value := target.CPU
			return &value, nil
		}
	}
	return nil, errClusterConflict
}
