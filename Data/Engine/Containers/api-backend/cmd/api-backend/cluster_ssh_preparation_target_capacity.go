package main

import "borealis/api-backend/internal/clusterbootstrap"

type clusterSSHPreparationTargetCapacity struct {
	CPURequestMilli        uint64
	MemoryLimitBytes       uint64
	HostMemoryReserveBytes uint64
	LonghornCPU            clusterSSHPreparationTargetInstanceCPU
}

// Calculate the existing validated release/profile workload against one freshly
// observed target. The subtotal already includes desired/passive workers, full
// worker rotation, application overlap and instance-manager memory exactly once.
// Add only the target's own observed Longhorn CPU request and existing host
// memory reserve. CPU caps may overcommit; requests determine this CPU fit.
//
// This is not a readiness gate or authority receipt. Caller must retain existing
// source/profile validation and freshly bind the complete worker census, native
// waiting-target evidence, hostname and Longhorn object across bounded rereads.
func calculateClusterSSHPreparationTargetCapacity(snapshot clusterSSHPreparationSnapshot,
	targetCPU clusterbootstrap.PreparationTargetCPU, hostname string, read func(string) (map[string]any, error)) (clusterSSHPreparationTargetCapacity, error) {
	fail := func() (clusterSSHPreparationTargetCapacity, error) {
		return clusterSSHPreparationTargetCapacity{}, clusterbootstrap.ErrPreparationConfig
	}
	// Waiting proposals have no worker claim or PreparationExpected yet.
	if !validClusterSSHPreparationDigest(snapshot.Observation) ||
		!validClusterSSHPreparationDigest(snapshot.Storage.Observation) || targetCPU.Validate(clusterbootstrap.K3sPins().Version) != nil {
		return fail()
	}
	cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
	if !ok {
		return fail()
	}
	longhorn, err := observeClusterSSHPreparationTargetInstanceCPU(read, hostname, targetCPU, snapshot.Storage.Requirements.LonghornInstanceManagerResources)
	if err != nil {
		return fail()
	}
	cpu, ok = clusterSSHCapacitySum(cpu, longhorn.RequestMilli)
	if !ok || cpu > targetCPU.AllocatableMilli {
		return fail()
	}
	requiredMemory, ok := clusterSSHCapacitySum(memory, clusterSSHHostMemoryReserveBytes)
	// Native validation bounds KiB before multiplication by 1024.
	if !ok || requiredMemory > targetCPU.MemoryKiB*1024 {
		return fail()
	}
	return clusterSSHPreparationTargetCapacity{cpu, memory, clusterSSHHostMemoryReserveBytes, longhorn}, nil
}
