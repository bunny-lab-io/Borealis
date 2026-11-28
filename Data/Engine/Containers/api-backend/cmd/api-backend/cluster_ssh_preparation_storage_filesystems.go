package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
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

// The supported persistent observer verifies a common root filesystem for the
// installation paths. Reserve once per selected target, including service
// startup when no packages are missing. The normal allocation-unit charge
// also applies to these extra entries.
const (
	clusterSSHPreparationTransientBytes   = 1 << 30
	clusterSSHPreparationTransientEntries = 4096
)

// Only the selected target borrows its own current database/cache sizes.
// Existing archive and installed-content rows exclude these working copies.
func calculateClusterSSHPreparationStorageCapacity(requirements clusterSSHStorageRequirements, evidence clusterremote.FilesystemEvidence,
	demands []clusterSSHFilesystemDemand, selection *clusterbootstrap.HostPackageSelection) ([]clusterSSHStorageBudget, error) {
	if selection == nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	metadata, err := selection.DpkgMetadataDemand()
	if err != nil {
		return nil, err
	}
	extra := []clusterSSHFilesystemDemand{{Path: "/var", Bytes: clusterSSHPreparationTransientBytes, Entries: clusterSSHPreparationTransientEntries}}
	if len(selection.Missing) > 0 {
		copies := evidence.PackageCopies
		if !copies.Valid() || copies.ProfileSHA256 != selection.ProfileSHA256 {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		extra = append(extra,
			clusterSSHFilesystemDemand{Path: "/var", Bytes: copies.Bytes, Entries: copies.Entries},
			clusterSSHFilesystemDemand{Path: "/var", Bytes: metadata.Bytes, Entries: metadata.Entries})
	}
	demands, err = mergeClusterSSHFilesystemDemands(demands, extra)
	if err != nil {
		return nil, err
	}
	return calculateClusterSSHStorageCapacity(requirements, evidence, demands)
}
