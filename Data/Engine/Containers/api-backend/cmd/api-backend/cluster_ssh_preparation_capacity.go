package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Values from the existing waiting configuration read. No private settings,
// credentials, claimed worker identity or mutation capability leave that read.
type clusterSSHPreparationConfigurationProjection struct {
	Digest, Secret string
	Sources        []clusterbootstrap.SourceNetwork
	Sizing         clusterbootstrap.PreparationSizing
	Applications   []clusterbootstrap.PreparationApplicationLimit
	Workers        clusterbootstrap.PreparationWorkerLimits
}

func clusterSSHPreparationConfigurationProjectionFrom(work clusterSSHPreparationWork, plan, version string,
	secret clusterbootstrap.PreparationRuntimeSecret, sources []clusterbootstrap.SourceNetwork, digest string) (clusterSSHPreparationConfigurationProjection, error) {
	fail := func() (clusterSSHPreparationConfigurationProjection, error) {
		return clusterSSHPreparationConfigurationProjection{}, clusterbootstrap.ErrPreparationConfig
	}
	projection, err := clusterSSHPreparationConfigurationPlan(work.Baseline, work.Proof, plan, version, sources)
	if err != nil || !validClusterSSHPreparationDigest(digest) {
		return fail()
	}
	sizing, applications, workers, err := clusterbootstrap.PreparationPlanProfileLimits(projection, secret.Settings())
	if err != nil {
		return fail()
	}
	return clusterSSHPreparationConfigurationProjection{digest, secret.ObservationSHA256(), slices.Clone(sources), sizing, applications, workers}, nil
}

type clusterSSHPreparationCapacityTarget struct {
	TargetID string                              `json:"target_id"`
	Capacity clusterSSHPreparationTargetCapacity `json:"capacity"`
}

// Public bounds and equality receipts inside the existing readiness payload.
// This value carries no independent authority and no private runtime settings.
type clusterSSHPreparationCapacityProjection struct {
	Version             int                                   `json:"version"`
	CleanSHA256         string                                `json:"clean_sha256"`
	ConfigurationSHA256 string                                `json:"configuration_sha256"`
	SecretSHA256        string                                `json:"secret_sha256"`
	StorageSHA256       string                                `json:"storage_sha256"`
	CensusSHA256        string                                `json:"census_sha256"`
	InstancePolicy      clusterSSHInstanceManagerResources    `json:"instance_policy"`
	Targets             []clusterSSHPreparationCapacityTarget `json:"targets"`
}

func (p clusterSSHPreparationCapacityProjection) clone() clusterSSHPreparationCapacityProjection {
	p.Targets = slices.Clone(p.Targets)
	return p
}

func (p clusterSSHPreparationCapacityProjection) digest(plan string, targets []clusterSSHPreparationTargetCPU) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	if p.Version != 1 || !validClusterSSHPreparationDigest(plan) || len(p.Targets) < 1 || len(p.Targets) > 2 || len(p.Targets) != len(targets) {
		return fail()
	}
	for _, digest := range []string{p.CleanSHA256, p.ConfigurationSHA256, p.SecretSHA256, p.StorageSHA256, p.CensusSHA256} {
		if !validClusterSSHPreparationDigest(digest) {
			return fail()
		}
	}
	percentage, ok := clusterSSHInstancePercentage(p.InstancePolicy.CPUPercentage)
	if !ok || !p.InstancePolicy.valid() {
		return fail()
	}
	for i, target := range p.Targets {
		cpu, value := targets[i].CPU, target.Capacity
		longhorn := value.LonghornCPU
		request, ok := clusterSSHInstanceCPURequest(cpu.AllocatableMilli, longhorn.OverrideMilli, percentage)
		memory, fits := clusterSSHCapacitySum(value.MemoryLimitBytes, value.HostMemoryReserveBytes)
		if target.TargetID != targets[i].TargetID || cpu.Validate(clusterbootstrap.K3sPins().Version) != nil ||
			!ok || request != longhorn.RequestMilli || request > p.InstancePolicy.CPULimitMilli ||
			!validClusterSSHPreparationDigest(longhorn.ObservationSHA256) ||
			(longhorn.Present && !vipScopeUUID(longhorn.NodeUID)) || (!longhorn.Present && (longhorn.NodeUID != "" || longhorn.OverrideMilli != 0)) ||
			value.CPURequestMilli < request || value.CPURequestMilli > cpu.AllocatableMilli || value.MemoryLimitBytes == 0 ||
			!fits || memory > cpu.MemoryKiB*1024 || value.HostMemoryReserveBytes != clusterSSHHostMemoryReserveBytes {
			return fail()
		}
	}
	return clusterSSHPreparationDigest(struct {
		Domain, Plan string
		Projection   clusterSSHPreparationCapacityProjection
	}{"preparation-complete-capacity-v1", plan, p})
}

func clusterSSHPreparationProfileMatches(p clusterSSHPreparationConfigurationProjection, storage clusterSSHStorageRequirements) bool {
	applications, postgres, err := clusterSSHProfileApplicationsFromLimits(p.Applications)
	worker, ok := storage.WorkerPolicy.resources()
	pids, pidsErr := strconv.ParseUint(storage.WorkerPolicy.Limits.PIDs, 10, 64)
	return err == nil && slices.Equal(applications, storage.Applications) && postgres == storage.PostgresResources &&
		ok && pidsErr == nil && p.Workers.CPULimitMilli == worker.CPULimitMilli && p.Workers.MemoryLimitBytes == worker.MemoryLimitBytes && p.Workers.PIDs == pids
}

type clusterSSHPreparationConfigurationProjectionRead func(context.Context, string) (clusterSSHPreparationConfigurationProjection, error)

// Reuse the exact native source/Secret reader; extraction adds no native Job or
// private HTTP observation. Copies prevent an adjacent consumer changing proof.
func clusterSSHPreparationConfigurationProjectionReader(work clusterSSHPreparationWork, check func(context.Context) error,
	get func(context.Context, string, any) error, network clusterSSHSourceNetworkRead) (func(context.Context, string) (string, error), clusterSSHPreparationConfigurationProjectionRead) {
	var mu sync.Mutex
	var projected clusterSSHPreparationConfigurationProjection
	read := newClusterSSHPreparationConfigurationReadWithProjection(work, check, get, network, func(value clusterSSHPreparationConfigurationProjection) error {
		mu.Lock()
		defer mu.Unlock()
		value.Sources, value.Applications = slices.Clone(value.Sources), slices.Clone(value.Applications)
		projected = value
		return nil
	})
	projection := func(ctx context.Context, plan string) (clusterSSHPreparationConfigurationProjection, error) {
		digest, err := read(ctx, plan)
		mu.Lock()
		defer mu.Unlock()
		if err != nil || projected.Digest != digest || ctx.Err() != nil {
			return clusterSSHPreparationConfigurationProjection{}, clusterbootstrap.ErrPreparationConfig
		}
		value := projected
		value.Sources, value.Applications = slices.Clone(value.Sources), slices.Clone(value.Applications)
		return value, nil
	}
	return read, projection
}

// Native waiting adapter. The controller keeps its original proposal checker;
// clean target values come from its existing authenticated API executor reader.
// Returned approval projection is valid only after this capacity gate ran.
func (c *clusterController) preparationCapacityRead(snapshot clusterSSHPreparationApprovalSnapshot, targets clusterSSHPreparationTargetsRead) (
	capacity, configuration func(context.Context, string) (string, error), approval clusterSSHPreparationTargetsRead) {
	capacity, configuration, approval, _ = c.preparationCapacityStorageRead(snapshot, targets)
	return capacity, configuration, approval
}

// The storage projection borrows the same verified capacity rounds; it adds no
// source inventory or native network observation.
func (c *clusterController) preparationCapacityStorageRead(snapshot clusterSSHPreparationApprovalSnapshot, targets clusterSSHPreparationTargetsRead) (
	capacity, configuration func(context.Context, string) (string, error), approval clusterSSHPreparationTargetsRead, storage clusterSSHPreparationStorageGateSourceRead) {
	var runner *kubernetesClusterStepRunner
	if c != nil {
		runner, _ = c.runner.(*kubernetesClusterStepRunner)
	}
	check, get, network := clusterSSHPreparationConfigurationNativeInputs(snapshot.work, c.preparationCleanTargetsProposalCheck(snapshot), runner)
	configuration, projection := clusterSSHPreparationConfigurationProjectionReader(snapshot.work, check, get, network)
	census := func(ctx context.Context, window int64) ([]int64, error) {
		if c == nil || c.store == nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		return readClusterSSHWorkerCensus(ctx, c.store.db, window)
	}
	capacity, approval, storage = newClusterSSHPreparationCapacityStorageRead(snapshot, check, get, projection, targets, census)
	return capacity, configuration, approval, storage
}

// Two complete storage/census/Longhorn rounds borrow the fresh clean gate's
// typed CPU/MemTotal projection. The normal six-gate scope brackets this read
// with fresh native clean observations and the later configuration recheck.
// No claimed Expected or future worker lease is made.
func newClusterSSHPreparationCapacityRead(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error,
	getJSON func(context.Context, string, any) error, configuration clusterSSHPreparationConfigurationProjectionRead,
	targets clusterSSHPreparationTargetsRead, census clusterSSHWorkerCensusRead) (func(context.Context, string) (string, error), clusterSSHPreparationTargetsRead) {
	gate, approval, _ := newClusterSSHPreparationCapacityStorageRead(snapshot, check, getJSON, configuration, targets, census)
	return gate, approval
}

func newClusterSSHPreparationCapacityStorageRead(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error,
	getJSON func(context.Context, string, any) error, configuration clusterSSHPreparationConfigurationProjectionRead,
	targets clusterSSHPreparationTargetsRead, census clusterSSHWorkerCensusRead) (func(context.Context, string) (string, error), clusterSSHPreparationTargetsRead, clusterSSHPreparationStorageGateSourceRead) {
	wire, wireErr := json.Marshal(snapshot.work)
	var work clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &work)
	owned := clusterSSHPreparationApprovalSnapshot{work: work}
	plan, planErr := clusterSSHPreparationPlanDigest(work.Authorization)
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	var mu sync.Mutex
	var retained clusterSSHPreparationCapacityProjection
	var retainedTargets clusterSSHPreparationCleanObservation
	var retainedDigest string
	var retainedSource clusterSSHPreparationStorageGateSource
	gate := func(parent context.Context, requested string) (string, error) {
		fail := func(reason string) (string, error) {
			failed.Store(true)
			return "", clusterSSHPreparationGateBlocked{"complete_capacity", reason}
		}
		if wireErr != nil || decodeErr != nil || planErr != nil || requested != plan || len(work.Claims) != 0 ||
			check == nil || getJSON == nil || configuration == nil || targets == nil || census == nil || parent.Err() != nil || failed.Load() {
			return fail("Fresh waiting-proposal CPU, inherited-profile, workload census or Longhorn evidence is unavailable; inspect and retry the original proposal.")
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail("Capacity observation expired; inspect and retry the original proposal.")
		}
		get := func(ctx context.Context, path string, out any) error {
			if failed.Load() || check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			err := getJSON(ctx, path, out)
			allowedAbsent := err == errClusterSSHCNPGConfigAbsent && clusterSSHCNPGOptionalConfigPath(path) || errors.Is(err, errClusterSSHPreparationLonghornNodeAbsent) && clusterSSHPreparationLonghornNodePath(path)
			if (err != nil && !allowedAbsent) || check(ctx) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return err
		}
		var first clusterSSHPreparationCapacityProjection
		var clean clusterSSHPreparationCleanObservation
		var storageSnapshot clusterSSHStorageSnapshot
		// One fresh typed profile serves both storage/census rounds. Gate 4 uses
		// the same retained configuration reader for its later network/Secret
		// recheck; extracting limits must not create extra native source Jobs.
		profile, err := configuration(ctx, plan)
		if err != nil || check(ctx) != nil || ctx.Err() != nil {
			return fail("Inherited profile or source authority changed during capacity observation; inspect and retry the original proposal.")
		}
		for round := 0; round < 2; round++ {
			currentSource, err := observeClusterSSHSourceCohort(ctx, get, work.Proof.Source)
			if err != nil || !reflect.DeepEqual(currentSource, work.Proof.Source) {
				return fail("Original source cohort changed during capacity observation; inspect and retry the original proposal.")
			}
			storage, err := observeClusterSSHStorage(ctx, work.Proof.Source, get)
			if err != nil || !clusterSSHPreparationProfileMatches(profile, storage.Requirements) ||
				!storage.Requirements.KubeVIP.matchesNetworks(profile.Sources) || !storage.Requirements.csiSocketMatchesNetworks(profile.Sources) {
				return fail("Source workload resources do not match the supported inherited release/profile; restore matching effective limits and retry preparation.")
			}
			sites, err := census(ctx, storage.Requirements.WorkerPolicy.OnlineWindow)
			if err != nil || !clusterSSHWorkerCensusValid(sites) || check(ctx) != nil || ctx.Err() != nil {
				return fail("Complete current worker demand could not be bound to the proposal; resolve worker census drift and retry preparation.")
			}
			clean, err = targets(ctx, plan)
			if err != nil || clean.validate(owned) != nil || check(ctx) != nil || ctx.Err() != nil {
				return fail("Fresh native CPU and memory evidence changed or is unavailable for an original target; inspect targets again.")
			}
			value := clusterSSHPreparationCapacityProjection{Version: 1, CleanSHA256: clean.Digest, ConfigurationSHA256: profile.Digest,
				SecretSHA256: profile.Secret, StorageSHA256: storage.Requirements.observation, InstancePolicy: storage.Requirements.LonghornInstanceManagerResources}
			value.CensusSHA256, err = clusterSSHPreparationDigest(sites)
			if err != nil {
				return fail("Worker demand receipt is unavailable; retry preparation.")
			}
			storageReceipt := storage.Requirements.observation
			storage.Requirements.observation = ""
			subtotal := clusterSSHPreparationSnapshot{Observation: profile.Secret, Storage: clusterSSHStorageSnapshot{storage.Requirements, storageReceipt}, DesiredWorkerSites: sites}
			storageSnapshot = subtotal.Storage
			nodeReadFailed := false
			readNode := func(path string) (map[string]any, error) {
				var raw json.RawMessage
				if err := get(ctx, path, &raw); err != nil {
					nodeReadFailed = !errors.Is(err, errClusterSSHPreparationLonghornNodeAbsent)
					return nil, err
				}
				value, _, err := clusterSSHStorageObject(raw)
				nodeReadFailed = err != nil
				return value, err
			}
			for i, target := range clean.Targets {
				host := work.Proof.Cohort.Targets[i].Report
				capacity, err := calculateClusterSSHPreparationTargetCapacity(subtotal, target.CPU, host.Hostname, readNode)
				if nodeReadFailed {
					return fail("An original target's own Longhorn CPU override or proven absence is unavailable; restore its named authenticated Node observation and retry preparation.")
				}
				if err != nil || profile.Sizing.Fits(host.CPUCount, target.CPU.MemoryKiB) != nil {
					return fail("Capacity fit is unproven for an original target: verify its own Longhorn CPU policy and provide CPU/memory for the inherited profile, complete workload/overlap demand and existing 7.5 GB host allowance before retrying.")
				}
				value.Targets = append(value.Targets, clusterSSHPreparationCapacityTarget{target.TargetID, capacity})
			}
			currentSource, err = observeClusterSSHSourceCohort(ctx, get, work.Proof.Source)
			if err != nil || !reflect.DeepEqual(currentSource, work.Proof.Source) || check(ctx) != nil || ctx.Err() != nil || failed.Load() {
				return fail("Source/proposal authority changed during capacity observation; inspect and retry the original proposal.")
			}
			var secretRaw json.RawMessage
			if get(ctx, clusterSSHRuntimeSecretPath, &secretRaw) != nil {
				return fail("Inherited runtime settings became unavailable during capacity observation; retry preparation.")
			}
			secret, secretErr := clusterbootstrap.ParsePreparationRuntimeSecret(secretRaw)
			clear(secretRaw)
			if secretErr != nil || secret.ObservationSHA256() != profile.Secret {
				return fail("Inherited runtime settings changed during capacity observation; inspect and retry the original proposal.")
			}
			if round == 0 {
				first = value
			} else if !reflect.DeepEqual(first, value) {
				return fail("Capacity inputs changed between complete observations; stabilize source demand and target policy, then retry preparation.")
			}
		}
		digest, err := first.digest(plan, clean.Targets)
		mu.Lock()
		defer mu.Unlock()
		if err != nil || check(ctx) != nil || ctx.Err() != nil || failed.Load() || retainedDigest != "" && retainedDigest != digest {
			return fail("Capacity inputs or original proposal changed after observation; inspect and retry preparation.")
		}
		projectedCapacity := first.clone()
		ownedSource, cloneErr := (clusterSSHPreparationStorageGateSource{Configuration: profile, Storage: storageSnapshot,
			Targets: clusterSSHPreparationCleanObservation{Digest: clean.Digest, Targets: clean.Targets, Packages: clean.Packages, Capacity: &projectedCapacity}}).clone()
		if cloneErr != nil || check(ctx) != nil || ctx.Err() != nil || failed.Load() {
			return fail("Verified storage projection could not retain its complete source inputs; retry preparation.")
		}
		retained, retainedDigest = first.clone(), digest
		retainedTargets = clusterSSHPreparationCleanObservation{Digest: clean.Digest, Targets: slices.Clone(clean.Targets), Packages: cloneClusterSSHPreparationPackages(clean.Packages)}
		retainedSource = ownedSource
		return digest, nil
	}
	approval := func(ctx context.Context, requested string) (clusterSSHPreparationCleanObservation, error) {
		mu.Lock()
		defer mu.Unlock()
		if failed.Load() || ctx.Err() != nil || requested != plan || retainedDigest == "" || retainedTargets.validate(owned) != nil || check == nil || check(ctx) != nil || ctx.Err() != nil {
			failed.Store(true)
			return clusterSSHPreparationCleanObservation{}, clusterbootstrap.ErrPreparationConfig
		}
		value := retainedTargets
		value.Targets = slices.Clone(value.Targets)
		value.Packages = cloneClusterSSHPreparationPackages(value.Packages)
		projected := retained.clone()
		value.Capacity = &projected
		return value, nil
	}
	storage := func(ctx context.Context, requested string) (clusterSSHPreparationStorageGateSource, error) {
		mu.Lock()
		defer mu.Unlock()
		if failed.Load() || ctx.Err() != nil || requested != plan || retainedDigest == "" || check == nil || check(ctx) != nil || ctx.Err() != nil {
			failed.Store(true)
			return clusterSSHPreparationStorageGateSource{}, clusterbootstrap.ErrPreparationConfig
		}
		value, err := retainedSource.clone()
		if err != nil || check(ctx) != nil || ctx.Err() != nil {
			failed.Store(true)
			return clusterSSHPreparationStorageGateSource{}, clusterbootstrap.ErrPreparationConfig
		}
		return value, err
	}
	return gate, approval, storage
}

func clusterSSHPreparationCapacityReadiness(payload string) (*clusterSSHPreparationReadinessReceipt, error) {
	if payload == "" {
		return nil, nil
	}
	var raw, authorization json.RawMessage
	if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_preparation_readiness": &raw, "ssh_preparation_authorization": &authorization}) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var receipt clusterSSHPreparationReadinessReceipt
	var a clusterSSHPreparationAuthorization
	if json.Unmarshal(raw, &receipt) != nil || json.Unmarshal(authorization, &a) != nil || parseClusterSSHPreparationReadiness(raw, a) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	if receipt.Version < 3 {
		return nil, nil
	} // Existing legacy recovery gains no capacity projection.
	return &receipt, nil
}

// Joining may create a previously absent Longhorn Node after existing native
// startup authority proves its registering Kubernetes identity. Once seen, its
// UID and override cannot disappear or change within the existing reader.
func clusterSSHPreparationCapacityTransition(before, after []clusterSSHPreparationTargetInstanceCPU, joining bool) bool {
	if before == nil {
		return true
	}
	if len(before) != len(after) {
		return false
	}
	for i, previous := range before {
		current := after[i]
		if previous == current {
			continue
		}
		if !joining || !current.Present || current.OverrideMilli != previous.OverrideMilli || current.RequestMilli != previous.RequestMilli ||
			previous.Present && previous.NodeUID != current.NodeUID {
			return false
		}
	}
	return true
}

func clusterSSHPreparationCapacityCurrentValid(approved clusterSSHPreparationCapacityProjection, current []clusterSSHPreparationTargetInstanceCPU, joining bool) bool {
	if len(current) != len(approved.Targets) {
		return false
	}
	prior := make([]clusterSSHPreparationTargetInstanceCPU, len(approved.Targets))
	for i, value := range current {
		prior[i] = approved.Targets[i].Capacity.LonghornCPU
		if !validClusterSSHPreparationDigest(value.ObservationSHA256) || value.OverrideMilli != prior[i].OverrideMilli || value.RequestMilli != prior[i].RequestMilli ||
			value.Present && !vipScopeUUID(value.NodeUID) || !value.Present && (value.NodeUID != "" || value.OverrideMilli != 0) {
			return false
		}
	}
	return clusterSSHPreparationCapacityTransition(prior, current, joining)
}

// Called only inside the original claimed source/storage scope. Native target
// verification separately reobserves CPU/MemTotal before its first effect. This
// reuses that approved target binding and freshly verifies every source demand
// and target Longhorn input; a retained digest alone cannot satisfy this check.
func recheckClusterSSHPreparationCapacity(ctx context.Context, authority clusterSSHPreparationAuthority, value clusterSSHPreparationSnapshot,
	joining bool, getJSON func(context.Context, string, any) error) (string, []clusterSSHPreparationTargetInstanceCPU, error) {
	fail := func() (string, []clusterSSHPreparationTargetInstanceCPU, error) {
		return "", nil, clusterbootstrap.ErrPreparationConfig
	}
	receipt, err := clusterSSHPreparationCapacityReadiness(authority.payload)
	if err != nil {
		return fail()
	}
	if receipt == nil {
		if value.Expected.TargetCPU != nil {
			return fail()
		}
		return "", nil, nil
	}
	if getJSON == nil || ctx.Err() != nil || value.Expected.TargetCPU == nil || !validClusterSSHPreparationLease(authority.Lease) || authority.Recovery != nil ||
		len(receipt.Targets) != len(authority.Cohort.Targets) || !validClusterSSHStorageSnapshot(value.Storage, authority.Source) {
		return fail()
	}
	proof := clusterSSHParentProof{Cohort: authority.Cohort, Source: authority.Source}
	configuration, err := clusterSSHPreparationConfigurationReceiptFromSettings(authority.Baseline, proof, receipt.PlanSHA256, authority.K3sVersion, value.Settings, value.Observation, value.Sources)
	census, censusErr := clusterSSHPreparationDigest(value.DesiredWorkerSites)
	approved := receipt.Capacity
	if err != nil || censusErr != nil || approved.ConfigurationSHA256 != configuration || approved.SecretSHA256 != value.Observation ||
		approved.StorageSHA256 != value.Storage.Observation || approved.CensusSHA256 != census || approved.InstancePolicy != value.Storage.Requirements.LonghornInstanceManagerResources {
		return fail()
	}
	selected := false
	for i, target := range authority.Cohort.Targets {
		if receipt.Targets[i].TargetID != target.Binding.TargetID || receipt.Targets[i].CPU.MemoryKiB != target.Report.MemoryKiB || receipt.Targets[i].CPU.AllocatableMilli != uint64(target.Report.CPUCount)*1000 {
			return fail()
		}
		if target.Binding.TargetID == authority.Lease.TargetID {
			selected = *value.Expected.TargetCPU == receipt.Targets[i].CPU
		}
	}
	if !selected {
		return fail()
	}
	read := func(path string) (map[string]any, error) {
		var raw json.RawMessage
		if err := getJSON(ctx, path, &raw); err != nil {
			return nil, err
		}
		object, _, err := clusterSSHStorageObject(raw)
		return object, err
	}
	current := make([]clusterSSHPreparationTargetInstanceCPU, len(receipt.Targets))
	var registered map[string]string
	for i, target := range authority.Cohort.Targets {
		capacity, err := calculateClusterSSHPreparationTargetCapacity(value, receipt.Targets[i].CPU, target.Report.Hostname, read)
		if err != nil {
			return fail()
		}
		prior := approved.Targets[i].Capacity
		observed := capacity.LonghornCPU
		capacity.LonghornCPU = prior.LonghornCPU
		if capacity != prior {
			return fail()
		}
		if observed != prior.LonghornCPU {
			if !joining || authority.IdentitySource == nil || !observed.Present || observed.OverrideMilli != prior.LonghornCPU.OverrideMilli || observed.RequestMilli != prior.LonghornCPU.RequestMilli ||
				prior.LonghornCPU.Present && observed.NodeUID != prior.LonghornCPU.NodeUID {
				return fail()
			}
			if registered == nil {
				_, registered, err = projectClusterSSHJoiningNodes(ctx, authority, getJSON)
				if err != nil {
					return fail()
				}
			}
			if !vipScopeUUID(registered[target.Binding.TargetID]) {
				return fail()
			}
		}
		current[i] = observed
	}
	if ctx.Err() != nil {
		return fail()
	}
	return receipt.Checks[2].SHA256, current, nil
}
