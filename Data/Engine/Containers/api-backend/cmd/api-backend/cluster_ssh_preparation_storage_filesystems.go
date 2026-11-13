package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"slices"
	"strings"
)

// Optional capability on the existing pinned proposal connection. No new SSH
// protocol or claim is needed to execute the fixed persistent-filesystem read.
type clusterSSHPreparationFilesystemClient interface {
	InspectFilesystem(context.Context, []byte, clusterremote.Target, clusterremote.HostKey, clusterremote.FilesystemRequest, func(context.Context) error) (clusterremote.TargetFilesystem, error)
}

func validClusterSSHPreparationFilesystemPaths(paths []string) bool {
	if paths == nil {
		return true // Existing clean/CPU request, with no filesystem projection.
	}
	return (clusterremote.FilesystemRequest{MachineID: strings.Repeat("1", 32), BootID: "11111111-1111-4111-8111-111111111111", Paths: paths, RequirePersistent: true}).Validate() == nil
}

func cloneClusterSSHPreparationFilesystems(values []clusterSSHTargetFilesystemEvidence) []clusterSSHTargetFilesystemEvidence {
	copy := slices.Clone(values)
	for i := range copy {
		copy[i].Storage = copy[i].Storage.Clone()
	}
	return copy
}

// Native provenance is checked by the API's opaque TargetFilesystem accessor;
// the authenticated response must still carry exactly the requested cohort and
// path projection. Availability is not a readiness or demand-completeness proof.
func validClusterSSHPreparationFilesystems(snapshot clusterSSHPreparationApprovalSnapshot, paths []string, values []clusterSSHTargetFilesystemEvidence) bool {
	if !validClusterSSHPreparationFilesystemPaths(paths) {
		return false
	}
	if paths == nil {
		return len(values) == 0
	}
	if len(values) != len(snapshot.work.Proof.Cohort.Targets) {
		return false
	}
	for i, value := range values {
		if value.TargetID != snapshot.work.Proof.Cohort.Targets[i].Binding.TargetID || value.Storage.MountNamespace == 0 ||
			!validClusterSSHPreparationDigest(value.Storage.Receipt) || len(value.Storage.Paths) != len(paths) || len(value.Storage.Filesystems) < 1 || len(value.Storage.Filesystems) > len(paths) {
			return false
		}
		for j, path := range value.Storage.Paths {
			if path.Path != paths[j] {
				return false
			}
		}
	}
	return true
}
