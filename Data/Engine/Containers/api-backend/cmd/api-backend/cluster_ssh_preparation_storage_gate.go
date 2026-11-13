package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sync/atomic"
)

// Adjacent projection of the complete_capacity reader's verified second round.
// No source Job, extra source inventory or future worker claim is created.
type clusterSSHPreparationStorageGateSource struct {
	Configuration clusterSSHPreparationConfigurationProjection
	Storage       clusterSSHStorageSnapshot
	Targets       clusterSSHPreparationCleanObservation
}

func (v clusterSSHPreparationStorageGateSource) clone() (clusterSSHPreparationStorageGateSource, error) {
	// Requirements contain nested workload/policy slices. The existing exported
	// snapshot intentionally excludes the collector's private observation field.
	raw, err := json.Marshal(v.Storage)
	var storage clusterSSHStorageSnapshot
	if err != nil || json.Unmarshal(raw, &storage) != nil {
		return clusterSSHPreparationStorageGateSource{}, clusterbootstrap.ErrPreparationConfig
	}
	// Preserve a malformed imported collector marker so the existing snapshot
	// validator rejects it; cloning must not sanitize unverified provenance.
	storage.Requirements.observation = v.Storage.Requirements.observation
	v.Storage = storage
	v.Configuration.Sources = slices.Clone(v.Configuration.Sources)
	v.Configuration.Applications = slices.Clone(v.Configuration.Applications)
	v.Targets.Targets = slices.Clone(v.Targets.Targets)
	v.Targets.Filesystems = cloneClusterSSHPreparationFilesystems(v.Targets.Filesystems)
	if v.Targets.Capacity != nil {
		capacity := v.Targets.Capacity.clone()
		v.Targets.Capacity = &capacity
	}
	return v, nil
}

type clusterSSHPreparationStorageGateSourceRead func(context.Context, string) (clusterSSHPreparationStorageGateSource, error)

// The production producer must compose every required installation/runtime
// family, including owned immutable material and generated configuration. An
// unavailable family returns an error; a subtotal is not complete demand. This
// contract has no caller-controlled completeness or Ready flag.
type clusterSSHPreparationCompleteStorageDemandRead func(context.Context, string, clusterbootstrap.PreparationPlan) ([]clusterSSHFilesystemDemand, error)
type clusterSSHPreparationFilesystemReader func([]string) clusterSSHPreparationTargetsRead

func (v clusterSSHPreparationStorageGateSource) plan(snapshot clusterSSHPreparationApprovalSnapshot, requested string) (clusterbootstrap.PreparationPlan, string, error) {
	fail := func() (clusterbootstrap.PreparationPlan, string, error) {
		return clusterbootstrap.PreparationPlan{}, "", clusterbootstrap.ErrPreparationConfig
	}
	work, capacity := snapshot.work, v.Targets.Capacity
	if capacity == nil || v.Targets.validate(snapshot) != nil || !validClusterSSHStorageSnapshot(v.Storage, work.Proof.Source) ||
		!clusterSSHPreparationProfileMatches(v.Configuration, v.Storage.Requirements) || capacity.CleanSHA256 != v.Targets.Digest ||
		capacity.ConfigurationSHA256 != v.Configuration.Digest || capacity.SecretSHA256 != v.Configuration.Secret || capacity.StorageSHA256 != v.Storage.Observation ||
		capacity.InstancePolicy != v.Storage.Requirements.LonghornInstanceManagerResources {
		return fail()
	}
	var version string
	if clusterBootstrapObject([]byte(work.Payload), map[string]any{"source_k3s_version": &version}) != nil || version != clusterbootstrap.K3sPins().Version {
		return fail()
	}
	plan, err := clusterSSHPreparationConfigurationPlan(work.Baseline, work.Proof, requested, version, v.Configuration.Sources)
	digest, capacityErr := capacity.digest(requested, v.Targets.Targets)
	if err != nil || capacityErr != nil {
		return fail()
	}
	return plan, digest, nil
}

// Waiting-proposal consumer. The ordinary six-gate scope owns its lifetime;
// the existing authenticated clean/filesystem broker owns, closes and joins all
// SSH/native reads. Claimed storage observers are deliberately not borrowed.
func newClusterSSHPreparationStorageRead(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error,
	source clusterSSHPreparationStorageGateSourceRead, completeDemand clusterSSHPreparationCompleteStorageDemandRead,
	filesystems clusterSSHPreparationFilesystemReader) func(context.Context, string) (string, error) {
	wire, wireErr := json.Marshal(snapshot.work)
	var work clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &work)
	owned := clusterSSHPreparationApprovalSnapshot{work: work}
	plan, planErr := clusterSSHPreparationPlanDigest(work.Authorization)
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	var retainedDigest string
	var retainedPaths []string
	var read clusterSSHPreparationTargetsRead
	return func(ctx context.Context, requested string) (string, error) {
		fail := func(reason string) (string, error) {
			failed.Store(true)
			return "", clusterSSHPreparationGateBlocked{"persistent_storage", reason}
		}
		if wireErr != nil || decodeErr != nil || planErr != nil || requested != plan || len(work.Claims) != 0 ||
			check == nil || source == nil || completeDemand == nil || filesystems == nil || ctx.Err() != nil || failed.Load() {
			return fail("Complete proposal-bound installation/runtime demand or persistent filesystem evidence is unavailable; preparation remains blocked.")
		}
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail("Storage observation expired; inspect and retry the original proposal.")
		}
		if check(ctx) != nil || ctx.Err() != nil || failed.Load() {
			return fail("Original preparation proposal changed or expired during storage observation.")
		}
		input, err := source(ctx, plan)
		if err != nil || check(ctx) != nil || ctx.Err() != nil {
			return fail("Verified source storage/profile and original target capacity evidence are unavailable; retry preparation.")
		}
		input, err = input.clone()
		configuration, capacity, bindingErr := input.plan(owned, plan)
		if err != nil || bindingErr != nil {
			return fail("Storage inputs no longer match the verified source profile or original target capacity; inspect and retry preparation.")
		}
		borrowed := configuration
		borrowed.PeerAddresses, borrowed.TargetAddresses = slices.Clone(configuration.PeerAddresses), slices.Clone(configuration.TargetAddresses)
		demands, err := completeDemand(ctx, plan, borrowed)
		if err != nil || check(ctx) != nil || ctx.Err() != nil {
			return fail("Complete owned installation/download/runtime demand is unavailable; supply every required demand family before preparation.")
		}
		demands = slices.Clone(demands)
		paths, ok := clusterSSHStorageDemandPaths(input.Storage.Requirements.Policy, demands)
		if !ok || !validClusterSSHPreparationFilesystemPaths(paths) || retainedPaths != nil && !slices.Equal(retainedPaths, paths) {
			return fail("Complete storage demand has invalid or changed persistent destinations; inspect the fixed installation plan.")
		}
		if read == nil {
			read = filesystems(slices.Clone(paths))
			retainedPaths = slices.Clone(paths)
		}
		if read == nil {
			return fail("Authenticated proposal-bound persistent filesystem reader is unavailable.")
		}
		observed, err := read(ctx, plan)
		if err != nil || observed.validate(owned) != nil || observed.Digest != input.Targets.Digest || !slices.Equal(observed.Targets, input.Targets.Targets) ||
			!validClusterSSHPreparationFilesystems(owned, paths, observed.Filesystems) || check(ctx) != nil || ctx.Err() != nil {
			return fail("Fresh persistent mounts, bytes or inodes are unavailable or changed on an original target; inspect targets again.")
		}
		evidence := cloneClusterSSHPreparationFilesystems(observed.Filesystems)
		budgets := make([]clusterSSHTargetStorageCapacity, len(evidence))
		for i, target := range evidence {
			values, err := calculateClusterSSHStorageCapacity(input.Storage.Requirements, target.Storage, demands)
			if err != nil || len(values) == 0 {
				return fail("Persistent storage ownership, mount topology or complete demand could not be validated on an original target.")
			}
			for _, value := range values {
				if !value.Fits {
					return fail("An original target lacks persistent bytes or inodes for complete installation, replicas and bounded recovery; provide capacity and retry.")
				}
			}
			budgets[i] = clusterSSHTargetStorageCapacity{TargetID: target.TargetID, StoragePath: input.Storage.Requirements.Policy.DefaultPath, Budgets: values, Fits: true}
		}
		after, err := source(ctx, plan)
		if err != nil || !reflect.DeepEqual(input, after) || check(ctx) != nil || ctx.Err() != nil || failed.Load() {
			return fail("Verified source/profile or original target capacity changed during storage observation; retry preparation.")
		}
		// Fresh counters already passed the complete calculation above. Keep
		// them in that actual evidence; only the equality fingerprint omits
		// volatile availability. Persistent identity, total capacity, allocation,
		// exact demand and every required budget remain bound across rereads.
		type filesystemIdentity struct {
			ID, Device, Type                        string
			TotalBytes, AllocationUnit, TotalInodes uint64
		}
		type requiredBudget struct {
			Filesystem                                                                  string
			OtherBytes, ReplicaBytes, RebuildBytes, MinimumFreeBytes, LogicalLimitBytes uint64
			Fits                                                                        bool
		}
		type targetStorage struct {
			TargetID, StoragePath, Receipt string
			MountNamespace                 uint64
			Paths                          []clusterremote.FilesystemPath
			Filesystems                    []filesystemIdentity
			Required                       []requiredBudget
		}
		stable := make([]targetStorage, len(evidence))
		for i, target := range evidence {
			stable[i] = targetStorage{TargetID: target.TargetID, StoragePath: budgets[i].StoragePath, Receipt: target.Storage.Receipt,
				MountNamespace: target.Storage.MountNamespace, Paths: target.Storage.Paths}
			for _, fs := range target.Storage.Filesystems {
				stable[i].Filesystems = append(stable[i].Filesystems, filesystemIdentity{fs.ID, fs.Device, fs.Type, fs.TotalBytes, fs.AllocationUnit, fs.TotalInodes})
			}
			for _, b := range budgets[i].Budgets {
				stable[i].Required = append(stable[i].Required, requiredBudget{b.Filesystem, b.OtherBytes, b.ReplicaBytes, b.RebuildBytes, b.MinimumFreeBytes, b.LogicalLimitBytes, b.Fits})
			}
		}
		digest, err := clusterSSHPreparationDigest(struct {
			Domain, Plan, Source, Capacity string
			Demands                        []clusterSSHFilesystemDemand
			Targets                        []targetStorage
		}{"preparation-persistent-storage-v1", plan, input.Storage.Observation, capacity, demands, stable})
		if err != nil || !validClusterSSHPreparationDigest(digest) || retainedDigest != "" && retainedDigest != digest || check(ctx) != nil || ctx.Err() != nil {
			return fail("Persistent storage inputs changed after observation; inspect and retry the original proposal.")
		}
		retainedDigest = digest
		return digest, nil
	}
}
