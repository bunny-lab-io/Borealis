package main

import "borealis/api-backend/internal/clusterbootstrap"

// Only generated-file content and possible parent entries. The shared native
// renderer supplies exact non-key lengths; the key uses its existing input cap.
// Rows stay on actual destination/ancestor directories for native filesystem
// mapping. Callers must merge these with other complete demands and measured
// filesystem evidence; this pure adapter cannot establish readiness or custody.
func clusterSSHPreparationHostFileDemands(expected clusterbootstrap.PreparationExpected, suite string) ([]clusterSSHFilesystemDemand, error) {
	generated, err := clusterbootstrap.PreparationHostFileDemands(expected, suite)
	if err != nil {
		return nil, err
	}
	return clusterSSHPreparationGeneratedHostFileDemands(generated), nil
}

// Waiting-proposal counterpart. It borrows the existing validated source plan
// without making a claimed PreparationExpected or a future worker lease.
func clusterSSHPreparationPlanHostFileDemands(plan clusterbootstrap.PreparationPlan, suite string) ([]clusterSSHFilesystemDemand, error) {
	generated, err := clusterbootstrap.PreparationPlanHostFileDemands(plan, suite)
	if err != nil {
		return nil, err
	}
	return clusterSSHPreparationGeneratedHostFileDemands(generated), nil
}

func clusterSSHPreparationGeneratedHostFileDemands(generated []clusterbootstrap.PreparationHostFileDemand) []clusterSSHFilesystemDemand {
	result := make([]clusterSSHFilesystemDemand, len(generated))
	for i, demand := range generated {
		result[i] = clusterSSHFilesystemDemand{Path: demand.Path, Bytes: demand.Bytes, Entries: demand.Entries}
	}
	return result
}
