package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"reflect"
	"slices"
	"strings"
)

// Demands come from the fixed preparation plan, never from a browser promise
// about available space. They include peak installation/download/runtime bytes
// outside Longhorn. A capacity result is conditional on this exact demand set;
// callers must separately prove its completeness before confirmation.
type clusterSSHFilesystemDemand struct {
	Path  string
	Bytes uint64
	// Entries adds one allocation unit per known file/directory/layer entry and
	// consumes one available inode. This is a content bound, not complete FS
	// metadata, quotas, runtime growth or OS/external-image reserve evidence.
	Entries uint64
}

type clusterSSHStorageBudget struct {
	Filesystem        string `json:"filesystem"`
	AvailableBytes    uint64 `json:"available_bytes"`
	OtherBytes        uint64 `json:"other_bytes"`
	ReplicaBytes      uint64 `json:"replica_bytes"`
	RebuildBytes      uint64 `json:"rebuild_bytes"`
	MinimumFreeBytes  uint64 `json:"minimum_free_bytes"`
	LogicalLimitBytes uint64 `json:"logical_limit_bytes"`
	Fits              bool   `json:"fits"`
}

type clusterSSHTargetStorageCapacity struct {
	TargetID    string                    `json:"target_id"`
	StoragePath string                    `json:"storage_path"`
	Budgets     []clusterSSHStorageBudget `json:"budgets"`
	Fits        bool                      `json:"fits"`
}

const clusterSSHCapacityLimit = uint64(1<<53 - 1)

func clusterSSHCapacitySum(left, right uint64) (uint64, bool) {
	if left > clusterSSHCapacityLimit || right > clusterSSHCapacityLimit-left {
		return 0, false
	}
	return left + right, true
}

func clusterSSHStorageDemandPaths(policy clusterSSHStoragePolicy, demands []clusterSSHFilesystemDemand) ([]string, bool) {
	if len(demands) < 1 || len(demands) > 7 {
		return nil, false
	}
	paths := []string{policy.DefaultPath}
	seen := map[string]bool{}
	for _, d := range demands {
		p, ok := clusterSSHStoragePath(d.Path)
		if !ok || p != d.Path || d.Bytes == 0 || d.Bytes > clusterSSHCapacityLimit || d.Entries > clusterSSHCapacityLimit || seen[d.Path] {
			return nil, false
		}
		seen[d.Path] = true
		paths = append(paths, d.Path)
	}
	slices.Sort(paths)
	return slices.Compact(paths), true
}

// Plan a new empty filesystem disk only. An existing Longhorn directory needs
// actual node/disk/replica ownership and reservations, never a guessed zero.
// Native availability already excludes root-reserved blocks. All paths on one
// filesystem share one budget; identical IDs on different hosts stay separate.
func calculateClusterSSHStorageCapacity(requirements clusterSSHStorageRequirements, evidence clusterremote.FilesystemEvidence,
	demands []clusterSSHFilesystemDemand) ([]clusterSSHStorageBudget, error) {
	fail := func() ([]clusterSSHStorageBudget, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	paths, ok := clusterSSHStorageDemandPaths(requirements.Policy, demands)
	if !ok || !requirements.Policy.valid(requirements.Volumes) || len(evidence.Paths) != len(paths) || len(evidence.Filesystems) == 0 || len(evidence.Filesystems) > len(paths) {
		return fail()
	}
	// This input normally comes directly from the opaque native accessor. Keep
	// arithmetic independently bounded even when called by an internal adapter.
	fsIndex := map[string]int{}
	budgets := make([]clusterSSHStorageBudget, len(evidence.Filesystems))
	inodes := make([]uint64, len(evidence.Filesystems))
	for i, fs := range evidence.Filesystems {
		if fs.ID == "" || fs.TotalBytes == 0 || fs.TotalBytes > clusterSSHCapacityLimit || fs.AvailableBytes > fs.TotalBytes || (fs.Type != "ext4" && fs.Type != "xfs") ||
			fs.AllocationUnit < 512 || fs.AllocationUnit > 1<<20 || fs.AllocationUnit&(fs.AllocationUnit-1) != 0 || fs.TotalInodes == 0 || fs.TotalInodes > clusterSSHCapacityLimit || fs.AvailableInodes > fs.TotalInodes {
			return fail()
		}
		if _, exists := fsIndex[fs.ID]; exists {
			return fail()
		}
		fsIndex[fs.ID] = i
		budgets[i] = clusterSSHStorageBudget{Filesystem: fs.ID, AvailableBytes: fs.AvailableBytes, Fits: true}
	}
	pathIndex := map[string]int{}
	used := map[int]bool{}
	for i, p := range evidence.Paths {
		index, exists := fsIndex[p.Filesystem]
		if p.Path != paths[i] || !exists {
			return fail()
		}
		pathIndex[p.Path], used[index] = index, true
		if p.Path == requirements.Policy.DefaultPath && (p.Ancestor == p.Path || p.Ancestor == "" || (p.Ancestor != "/" && !strings.HasPrefix(p.Path, p.Ancestor+"/"))) {
			return fail()
		}
	}
	if len(used) != len(budgets) {
		return fail()
	}
	for _, d := range demands {
		index, exists := pathIndex[d.Path]
		if !exists {
			return fail()
		}
		fs := evidence.Filesystems[index]
		if d.Entries > clusterSSHCapacityLimit/fs.AllocationUnit {
			return fail()
		}
		bytes, ok := clusterSSHCapacitySum(d.Bytes, d.Entries*fs.AllocationUnit)
		if !ok {
			return fail()
		}
		if inodes[index], ok = clusterSSHCapacitySum(inodes[index], d.Entries); !ok || inodes[index] >= fs.AvailableInodes {
			return fail()
		}
		if budgets[index].OtherBytes, ok = clusterSSHCapacitySum(budgets[index].OtherBytes, bytes); !ok {
			return fail()
		}
	}
	index, exists := pathIndex[requirements.Policy.DefaultPath]
	if !exists {
		return fail()
	}
	b := &budgets[index]
	if requirements.ArtifactReplicaBytes == 0 || requirements.PostgresInstanceBytes == 0 {
		return fail()
	}
	if b.ReplicaBytes, ok = clusterSSHCapacitySum(requirements.ArtifactReplicaBytes, requirements.PostgresInstanceBytes); !ok {
		return fail()
	}
	// Keep room for one full largest-volume reconstruction as well as all newly
	// allocated replicas. Sparse current usage never discounts provisioned bytes.
	b.RebuildBytes = max(requirements.ArtifactReplicaBytes, requirements.PostgresInstanceBytes)
	fs := evidence.Filesystems[index]
	p := requirements.Policy
	reserved := (fs.TotalBytes*p.ReservedPercent + 99) / 100
	b.MinimumFreeBytes = (fs.TotalBytes*p.MinimalAvailablePercent + 99) / 100
	b.LogicalLimitBytes = min((fs.TotalBytes-reserved)*p.OverProvisioningPercent/100, clusterSSHCapacityLimit)
	logical, ok := clusterSSHCapacitySum(b.ReplicaBytes, b.RebuildBytes)
	if !ok {
		return fail()
	}
	if logical > b.LogicalLimitBytes {
		b.Fits = false
	}
	for i := range budgets {
		b := &budgets[i]
		required, ok := clusterSSHCapacitySum(b.OtherBytes, b.ReplicaBytes)
		if !ok {
			return fail()
		}
		required, ok = clusterSSHCapacitySum(required, b.RebuildBytes)
		if !ok {
			return fail()
		}
		required, ok = clusterSSHCapacitySum(required, b.MinimumFreeBytes)
		if !ok {
			return fail()
		}
		// Longhorn requires strictly greater remaining physical availability.
		// Non-storage filesystems also retain at least one byte beyond demands.
		b.Fits = b.Fits && b.AvailableBytes > required
	}
	return budgets, nil
}

// Consume only while source policy, every original target credential/claim and
// native filesystem observations remain live. The output is a conditional
// storage plan, not permission to provision, stage files, or join membership.
func withClusterSSHTargetStorageCapacity(parent context.Context, readers clusterSSHNetworkTargetReaders,
	source clusterSSHPreparationSnapshotRead, demands []clusterSSHFilesystemDemand,
	consume func(context.Context, []clusterSSHTargetStorageCapacity) error) error {
	if readers.within == nil || readers.Authority == nil || readers.Refresh == nil || source == nil || consume == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	demands = slices.Clone(demands)
	return readers.within(parent, func(ctx context.Context) error {
		a, err := readers.Authority(ctx)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		value, err := source(ctx)
		if err != nil || !validClusterSSHStorageSnapshot(value.Storage, a.Source) {
			return clusterbootstrap.ErrPreparationConfig
		}
		expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, value.Expected.PodCIDR, value.Expected.ServiceCIDR)
		if err != nil || !reflect.DeepEqual(expected, value.Expected) {
			return clusterbootstrap.ErrPreparationConfig
		}
		// Freeze owned copies before lending values to any observer/consumer.
		storage := value.Storage
		storage.Requirements.Volumes = slices.Clone(storage.Requirements.Volumes)
		storage.Requirements.Policy.Classes = slices.Clone(storage.Requirements.Policy.Classes)
		paths, ok := clusterSSHStorageDemandPaths(storage.Requirements.Policy, demands)
		if !ok {
			return clusterbootstrap.ErrPreparationConfig
		}
		selection := make([]clusterSSHTargetFilesystemSelection, len(a.Cohort.Targets))
		for i, t := range a.Cohort.Targets {
			selection[i] = clusterSSHTargetFilesystemSelection{TargetID: t.Binding.TargetID, Paths: slices.Clone(paths), RequirePersistent: true}
		}
		current := func() error {
			if readers.Refresh(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			next, err := source(ctx)
			if err != nil || !reflect.DeepEqual(expected, next.Expected) || !reflect.DeepEqual(storage, next.Storage) || ctx.Err() != nil || readers.Refresh(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		if current() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		if withClusterSSHTargetFilesystemEvidence(ctx, readers, selection, func(ctx context.Context, fs []clusterSSHTargetFilesystemEvidence) error {
			if current() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			out := make([]clusterSSHTargetStorageCapacity, len(fs))
			for i, target := range fs {
				budgets, err := calculateClusterSSHStorageCapacity(storage.Requirements, target.Storage, demands)
				if err != nil {
					return err
				}
				fits := true
				for _, b := range budgets {
					fits = fits && b.Fits
				}
				out[i] = clusterSSHTargetStorageCapacity{target.TargetID, storage.Requirements.Policy.DefaultPath, budgets, fits}
			}
			if consume(ctx, out) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return current()
		}) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return current()
	})
}
