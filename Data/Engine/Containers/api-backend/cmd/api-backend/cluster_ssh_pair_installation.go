package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
)

const clusterSSHPairStopReason = "Installation stopped by the operator. Both hosts remain reserved for journal and membership reconciliation."
const clusterSSHSystemDiskMinimum = 240_000_000_000

var clusterSSHPairCheckCodes = []string{"source_inputs", "host_profile", "replica_capacity", "cluster_connectivity", "installation_inputs"}

// Public installation inputs frozen by the qualification report. The digest
// of that report is already part of the operator's exact confirmation.
type clusterSSHPairInstallation struct {
	Inspection      string                                `json:"inspection_sha256"`
	Configuration   string                                `json:"configuration_sha256"`
	Manifests       map[string]string                     `json:"manifests"`
	PostgresImage   string                                `json:"postgres_image"`
	Targets         []clusterSSHPreparationTargetCPU      `json:"targets"`
	Packages        []clusterSSHPreparationTargetPackages `json:"packages"`
	SystemDiskBytes []uint64                              `json:"system_disk_bytes"`
	Demand          []clusterSSHPreparationCapacityTarget `json:"demand"`
}

func pairManifestLimits(suites ...string) map[string]int64 {
	limits := map[string]int64{clusterbootstrap.ManifestName: clusterbootstrap.MaxManifestBytes, clusterbootstrap.ImageInventoryName: clusterbootstrap.MaxImageInventoryBytes, clusterbootstrap.ExternalInventoryName: clusterbootstrap.MaxImageInventoryBytes, clusterbootstrap.K3sInventoryName: clusterbootstrap.MaxK3sInventoryBytes}
	for _, suite := range suites {
		limits[(clusterbootstrap.HostPackagePlan{Suite: suite}).InventoryName()] = clusterbootstrap.MaxImageInventoryBytes
	}
	return limits
}
func (p *clusterSSHPairInstallation) valid() bool {
	if p == nil || !validClusterSSHPreparationDigest(p.Inspection) || !validClusterSSHPreparationDigest(p.Configuration) || !clusterbootstrap.ValidPostgresImageReference(p.PostgresImage) || len(p.Targets) != 2 || len(p.Demand) != 2 || len(p.SystemDiskBytes) != 2 || !validClusterSSHPreparationPackages(p.Targets, p.Packages) {
		return false
	}
	var suites []string
	for _, pkg := range p.Packages {
		plan, _, err := pkg.Selection.Plan()
		if err != nil {
			return false
		}
		suites = append(suites, plan.Suite)
	}
	limits := pairManifestLimits(suites...)
	if len(limits) != len(p.Manifests) {
		return false
	}
	for name := range limits {
		if !clusterBootstrapDigestPattern.MatchString(p.Manifests[name]) {
			return false
		}
	}
	for i, t := range p.Targets {
		d := p.Demand[i].Capacity
		memory, ok := clusterSSHCapacitySum(d.MemoryLimitBytes, d.HostMemoryReserveBytes)
		if !validClusterSSHPreparationDigest(t.ObservationSHA256) || !clusterUUIDRE.MatchString(t.TargetID) || (i == 1 && p.Targets[0].TargetID == t.TargetID) || t.CPU.Validate(clusterbootstrap.K3sPins().Version) != nil || d.HostMemoryReserveBytes != clusterSSHHostMemoryReserveBytes || !ok || d.MemoryLimitBytes == 0 || memory > t.CPU.MemoryKiB*1024 || d.CPURequestMilli == 0 || d.CPURequestMilli > t.CPU.AllocatableMilli || p.Demand[i].TargetID != t.TargetID || p.SystemDiskBytes[i] < clusterSSHSystemDiskMinimum {
			return false
		}
	}
	return true
}

func inspectClusterSSHPair(ctx context.Context, work clusterSSHQualificationWork, readers clusterSSHNetworkTargetReaders, fetch clusterSSHPreparationSnapshotRead, report *clusterSSHQualificationReport, stage *int, observe func(string)) error {
	fail := clusterbootstrap.ErrPreparationConfig
	report.Version = 2
	report.Checks = nil
	for _, code := range clusterSSHPairCheckCodes {
		report.Checks = append(report.Checks, clusterSSHQualificationCheck{code, "pending"})
	}
	enter := func(i int) { *stage = i; observe(clusterSSHPairCheckCodes[i]) }
	pass := func() { report.Checks[*stage].State = "passed" }
	enter(0)
	value, err := fetch(ctx)
	if err != nil || len(value.QualificationCPU) != 2 {
		return fail
	}
	inspection, err := clusterSSHPreparationDigest(work.Proof)
	if err != nil {
		return err
	}
	p := &clusterSSHPairInstallation{Inspection: inspection, PostgresImage: value.Storage.Requirements.PostgresImage.Resolved, Manifests: map[string]string{}}
	p.Configuration, err = clusterSSHPreparationConfigurationReceiptFromSettings(work.Baseline, work.Proof, inspection, value.Expected.K3sVersion, value.Settings, value.Observation, value.Sources)
	if err != nil {
		return err
	}
	var suites []string
	for _, host := range work.Proof.Cohort.Targets {
		suite, err := clusterbootstrap.HostPackageSuiteForVersion(host.Report.OSVersion)
		if err != nil || host.Report.OSID != "ubuntu" {
			return fail
		}
		suites = append(suites, suite)
	}
	manifests, err := resolveClusterPackagedAssets(ctx, work.Baseline, pairManifestLimits(suites...))
	if err != nil {
		return err
	}
	for name, asset := range manifests {
		p.Manifests[name] = asset.Digest
	}
	ctx = context.WithValue(ctx, clusterSSHPairManifestKey{}, p.Manifests)
	pass()
	enter(1)
	cfg, err := clusterbootstrap.NewPreparationConfiguration(value.Expected, value.Settings)
	if err != nil {
		return err
	}
	sizing, err := cfg.Sizing()
	cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(value)
	if err != nil || !ok {
		return fail
	}
	cpuPlan, err := clusterbootstrap.PreparationTargetCPUPlanDigest(value.Expected.K3sVersion)
	if err != nil {
		return err
	}
	for i, host := range work.Proof.Cohort.Targets {
		claim := work.Claims[i].Lease
		var salt [32]byte
		if _, err := rand.Read(salt[:]); err != nil {
			return fail
		}
		q := clusterremote.PreparationProposal{Version: clusterremote.PreparationProposalVersion, Source: work.Baseline, CPUPlanSHA256: cpuPlan, ClusterID: host.Binding.ClusterID, OperationID: claim.OperationID, OperationAttempt: claim.OperationAttempt, ControllerHolder: claim.ControllerHolder, PlanSHA256: inspection, TargetID: claim.TargetID, QualificationGeneration: claim.Generation, Address: host.Binding.Address, Port: host.Binding.Port, HostKeyFingerprint: host.Key.Fingerprint, Hostname: host.Report.Hostname, MachineID: host.Report.MachineID, BootID: host.Report.BootID, Nonce: hex.EncodeToString(salt[:])}
		pr, err := readers.Proposal(ctx, host, q)
		if err != nil || pr.Validate(q) != nil || pr.CPU.AllocatableMilli != uint64(host.Report.CPUCount)*1000 || pr.CPU.MemoryKiB != host.Report.MemoryKiB || sizing.Fits(host.Report.CPUCount, host.Report.MemoryKiB) != nil {
			return fail
		}
		packages := pr.Packages.Clone()
		plan, _, err := packages.Plan()
		if err != nil || plan.Suite != suites[i] {
			return fail
		}
		digest, err := clusterSSHPreparationPackageDigest(packages)
		if err != nil {
			return err
		}
		p.Targets = append(p.Targets, clusterSSHPreparationTargetCPU{claim.TargetID, pr.ObservationSHA256, digest, pr.CPU})
		p.Packages = append(p.Packages, clusterSSHPreparationTargetPackages{claim.TargetID, pr.ObservationSHA256, packages})
		instance := value.QualificationCPU[i]
		request, ok := clusterSSHCapacitySum(cpu, instance.RequestMilli)
		required, fits := clusterSSHCapacitySum(memory, clusterSSHHostMemoryReserveBytes)
		if !ok || !fits || request > pr.CPU.AllocatableMilli || required > pr.CPU.MemoryKiB*1024 {
			return fail
		}
		p.Demand = append(p.Demand, clusterSSHPreparationCapacityTarget{claim.TargetID, clusterSSHPreparationTargetCapacity{request, memory, clusterSSHHostMemoryReserveBytes, instance}})
	}
	pass()
	enter(2)
	budgets, err := qualifyPairStorage(ctx, work, readers, value, p)
	if err != nil {
		return err
	}
	report.Storage = budgets
	if len(budgets) != 2 || !budgets[0].Fits || !budgets[1].Fits {
		return fail
	}
	pass()
	enter(3)
	for _, t := range work.Proof.Cohort.Targets {
		bytes, err := readers.SystemDisk(ctx, t, work.Proof.Source.ControlPlaneVIP)
		if err != nil || bytes < clusterSSHSystemDiskMinimum {
			return fail
		}
		p.SystemDiskBytes = append(p.SystemDiskBytes, bytes)
	}
	pass()
	enter(4)
	if !p.valid() || readers.Refresh(ctx) != nil {
		return fail
	}
	pass()
	report.Installation, report.Ready = p, true
	return nil
}

// Size the actual package and image set on the observed filesystem. Qualification
// does not unpack a source tree: reserve its enforced expansion limit for each of
// the incoming and installed copies until exact bytes are available at staging.
func qualifyPairStorage(ctx context.Context, work clusterSSHQualificationWork, readers clusterSSHNetworkTargetReaders, v clusterSSHPreparationSnapshot, p *clusterSSHPairInstallation) ([]clusterSSHTargetStorageCapacity, error) {
	fail := clusterbootstrap.ErrPreparationConfig
	app, err := resolveClusterSSHImageInventory(ctx, work.Baseline)
	if err != nil {
		return nil, err
	}
	appBytes, err := app.demands()
	if err != nil {
		return nil, err
	}
	external, err := resolveClusterSSHExternalInventory(ctx, work.Baseline)
	if err != nil {
		return nil, err
	}
	externalBytes, err := external.demands()
	if err != nil {
		return nil, err
	}
	k3s, err := resolveClusterSSHK3sInventory(ctx, work.Baseline, v.Expected.K3sVersion)
	if err != nil {
		return nil, err
	}
	k3sBytes, err := k3s.demands()
	if err != nil {
		return nil, err
	}
	assets, err := resolveClusterPackagedAssets(ctx, work.Baseline, map[string]int64{clusterbootstrap.BundleName: clusterbootstrap.MaxBundleBytes})
	if err != nil {
		return nil, err
	}
	source, err := clusterSSHPreparationConfigurationPlan(work.Baseline, work.Proof, p.Inspection, v.Expected.K3sVersion, v.Sources)
	if err != nil {
		return nil, err
	}
	perSuite := map[string][]clusterSSHFilesystemDemand{}
	targetPackages := make([][]clusterSSHFilesystemDemand, len(p.Packages))
	for i, selected := range p.Packages {
		plan, _, err := selected.Selection.Plan()
		if err != nil {
			return nil, err
		}
		if _, present := perSuite[plan.Suite]; !present {
			packages, err := resolveClusterSSHHostPackageInventory(ctx, work.Baseline, plan.Suite)
			if err != nil {
				return nil, err
			}
			perSuite[plan.Suite], err = pairPackageDemands(source, plan.Suite, packages, assets[clusterbootstrap.BundleName].Size)
			if err != nil {
				return nil, err
			}
		}
		targetPackages[i] = perSuite[plan.Suite]
	}
	demands, err := mergeClusterSSHFilesystemDemands(appBytes, externalBytes, k3sBytes, clusterSSHK3sConfigurationBudget())
	if err != nil {
		return nil, err
	}
	var result []clusterSSHTargetStorageCapacity
	err = withClusterSSHQualificationPostgresCapacity(ctx, v.Storage.Requirements.PostgresImage, demands, readers.Refresh, acquireClusterSSHQualificationPostgres, func(ctx context.Context, capacity clusterSSHPreparedImageCapacity) error {
		targetDemands := make([][]clusterSSHFilesystemDemand, len(p.Packages))
		selections := make([]clusterSSHTargetFilesystemSelection, len(p.Packages))
		for i, pkg := range p.Packages {
			demand, err := mergeClusterSSHFilesystemDemands(capacity.Target, targetPackages[i])
			if err != nil {
				return err
			}
			paths, ok := clusterSSHStorageDemandPaths(v.Storage.Requirements.Policy, demand)
			if !ok {
				return fail
			}
			targetDemands[i] = demand
			selections[i] = clusterSSHTargetFilesystemSelection{TargetID: pkg.TargetID, Paths: paths, RequirePersistent: true, RequirePackageCopies: len(pkg.Selection.Missing) > 0}
		}
		return readClusterSSHTargetFilesystemEvidence(ctx, readers, selections, false, func(ctx context.Context, evidence []clusterSSHTargetFilesystemEvidence) error {
			for i, t := range evidence {
				budgets, err := calculateClusterSSHPreparationStorageCapacity(v.Storage.Requirements, t.Storage, targetDemands[i], &p.Packages[i].Selection)
				if err != nil {
					return err
				}
				fits := len(budgets) > 0
				for _, budget := range budgets {
					fits = fits && budget.Fits
				}
				result = append(result, clusterSSHTargetStorageCapacity{t.TargetID, v.Storage.Requirements.Policy.DefaultPath, budgets, fits})
			}
			return ctx.Err()
		})
	})
	return result, err
}

func pairPackageDemands(source clusterbootstrap.PreparationPlan, suite string, packages clusterSSHHostPackageInventory, bundleSize int64) ([]clusterSSHFilesystemDemand, error) {
	hostFiles, err := clusterSSHPreparationPlanHostFileDemands(source, suite)
	if err != nil {
		return nil, err
	}
	pins := packages.inventory.Packages()
	scratch := clusterSSHFilesystemDemand{Path: "/opt/Borealis", Bytes: uint64(bundleSize + 2*clusterbootstrap.MaxExpandedBytes + packages.inventory.Archive().Size), Entries: uint64(2*clusterbootstrap.MaxEntries + len(pins) + 8)}
	var installed []clusterSSHFilesystemDemand
	for _, pkg := range pins {
		scratch.Bytes += uint64(pkg.Size)
		for _, dst := range pkg.DataPaths {
			path := dst.Path
			if path == "/lib" || path == "/sbin" {
				path = "/usr"
			}
			installed = append(installed, clusterSSHFilesystemDemand{Path: path, Bytes: uint64(dst.FileBytes + dst.LinkBytes), Entries: uint64(dst.Entries)})
		}
	}
	return mergeClusterSSHFilesystemDemands(hostFiles, installed, []clusterSSHFilesystemDemand{scratch})
}

type clusterSSHPairManifestKey struct{}

func pairReadiness(a clusterSSHPreparationAuthorization, p *clusterSSHPairInstallation) (clusterSSHPreparationReadinessReceipt, error) {
	plan, err := clusterSSHPreparationPlanDigest(a)
	if err != nil || !p.valid() || p.Inspection != a.InspectionSHA256 || len(a.Targets) != 2 {
		return clusterSSHPreparationReadinessReceipt{}, errClusterConflict
	}
	for i, t := range p.Targets {
		if t.TargetID != a.Targets[i].TargetID {
			return clusterSSHPreparationReadinessReceipt{}, errClusterConflict
		}
	}
	return clusterSSHPreparationReadinessReceipt{Version: 5, PlanSHA256: plan, Configuration: p.Configuration, Targets: p.Targets, Packages: p.Packages}, nil
}
func pairReport(payload string) (*clusterSSHQualificationReport, error) {
	var raw json.RawMessage
	var report clusterSSHQualificationReport
	if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_qualification": &raw}) != nil || json.Unmarshal(raw, &report) != nil || !completedClusterSSHQualification(&report) || report.Version != 2 {
		return nil, errClusterConflict
	}
	return &report, nil
}

func pairConfigurationMatches(a clusterSSHPreparationAuthority, v clusterSSHPreparationSnapshot, p *clusterSSHPairInstallation) bool {
	inputSource, err := pairSourceBoot(a.payload, a.Source, false)
	if err != nil {
		return false
	}
	networks := v.Sources
	if clusterbootstrap.IsPairExecutor() {
		if len(networks) != 1 || networks[0].NodeUID != a.Source.Members[0].NodeUID || networks[0].BootID != a.Source.Members[0].BootID {
			return false
		}
		networks = slices.Clone(networks)
		networks[0].BootID = inputSource.Members[0].BootID
	}
	digest, err := clusterSSHPreparationConfigurationReceiptFromSettings(a.Baseline, clusterSSHParentProof{Version: 1, Cohort: a.Cohort, Source: inputSource}, p.Inspection, a.K3sVersion, v.Settings, v.Observation, networks)
	return err == nil && digest == p.Configuration && v.Storage.Requirements.PostgresImage.Resolved == p.PostgresImage
}

func checkedPairReadiness(raw json.RawMessage, a clusterSSHPreparationAuthorization, report *clusterSSHQualificationReport) bool {
	expected, err := pairReadiness(a, report.Installation)
	var stored clusterSSHPreparationReadinessReceipt
	return err == nil && json.Unmarshal(raw, &stored) == nil && reflect.DeepEqual(stored, expected)
}

// One source observation supplies the pair's inherited settings, measured
// workload and disk demand. Sizing does not depend on Pod resource versions.
func (r *kubernetesClusterStepRunner) pairSourceSnapshot(ctx context.Context, a clusterSSHPreparationAuthority, owner clusterSSHPreparationAuthorityRead, census clusterSSHWorkerCensusRead, joining bool) (clusterSSHPreparationSnapshot, error) {
	fail := func(stage string) (clusterSSHPreparationSnapshot, error) {
		return clusterSSHPreparationSnapshot{}, fmt.Errorf("pair source %s: %w", stage, clusterbootstrap.ErrPreparationConfig)
	}
	get := func(ctx context.Context, path string, out any) error {
		if path == clusterSSHRuntimeSecretPath || path == "/api/v1/nodes" || path == "/api/v1/namespaces/kube-system" {
			return r.kube.getClusterSSHPreparationJSON(ctx, path, out)
		}
		return r.kube.getClusterSSHStorageJSON(ctx, path, out)
	}
	if joining {
		get = newClusterSSHJoiningProjection(owner, get)
	}
	original, err := observeClusterSSHSourceCohort(ctx, get, a.Source)
	if err != nil || !reflect.DeepEqual(original, a.Source) {
		return fail("original membership")
	}
	network, err := observeClusterSSHSourceNetworks(ctx, a, r.newSSHSourceNetworkRead(owner))
	if err != nil {
		return fail("source network")
	}
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, network[0].PodCIDR, network[0].ServiceCIDR, a.payload)
	if err != nil {
		return fail("installation identity")
	}
	var raw json.RawMessage
	if get(ctx, clusterSSHRuntimeSecretPath, &raw) != nil {
		return fail("inherited configuration")
	}
	secret, err := clusterbootstrap.ParsePreparationRuntimeSecret(raw)
	clear(raw)
	if err != nil {
		return fail("inherited configuration")
	}
	storage, err := observeClusterSSHStorage(ctx, a.Source, get)
	if err != nil {
		return fail("source storage")
	}
	usage := storage.Requirements
	digest := usage.observation
	usage.observation = ""
	v := clusterSSHPreparationSnapshot{Expected: expected, Settings: secret.Settings(), Observation: secret.ObservationSHA256(), Sources: network, Storage: clusterSSHStorageSnapshot{usage, digest}}
	if !validClusterSSHStorageSnapshot(v.Storage, a.Source) || !usage.KubeVIP.matchesNetworks(network) || !usage.csiSocketMatchesNetworks(network) {
		return fail("storage policy")
	}
	cfg, err := clusterbootstrap.NewPreparationConfiguration(expected, v.Settings)
	if err != nil {
		return fail("inherited profile")
	}
	apps, err := cfg.ProfileApplicationLimits()
	workers, workerErr := cfg.ProfileWorkerLimits()
	if err != nil || workerErr != nil || !clusterSSHPreparationProfileMatches(clusterSSHPreparationConfigurationProjection{Applications: apps, Workers: workers}, usage) {
		return fail("active profile")
	}
	v.DesiredWorkerSites, err = census(ctx, usage.WorkerPolicy.OnlineWindow)
	if err != nil || !clusterSSHWorkerCensusValid(v.DesiredWorkerSites) {
		return fail("worker census")
	}
	readNode := func(path string) (map[string]any, error) {
		var raw json.RawMessage
		if err := r.kube.getClusterSSHStorageJSON(ctx, path, &raw); err != nil {
			return nil, err
		}
		obj, _, err := clusterSSHStorageObject(raw)
		return obj, err
	}
	cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(v)
	if !ok {
		return fail("workload sizing")
	}
	for _, t := range a.Cohort.Targets {
		available := uint64(t.Report.CPUCount) * 1000
		instance, err := clusterSSHInstanceCPUForHost(readNode, t.Report.Hostname, available, usage.LonghornInstanceManagerResources)
		required, cpuOK := clusterSSHCapacitySum(cpu, instance.RequestMilli)
		requiredRAM, memOK := clusterSSHCapacitySum(memory, clusterSSHHostMemoryReserveBytes)
		if err != nil || !cpuOK || !memOK || required > available || requiredRAM > t.Report.MemoryKiB*1024 {
			return fail("target CPU or memory capacity")
		}
		v.QualificationCPU = append(v.QualificationCPU, instance)
	}
	if joining {
		report, err := pairReport(a.payload)
		if err != nil || !pairConfigurationMatches(a, v, report.Installation) {
			return fail("approved configuration or database image")
		}
	}
	current, err := owner(ctx)
	if err != nil || a.PayloadSHA256 != current.PayloadSHA256 || a.Lease != current.Lease || ctx.Err() != nil {
		return fail("operation ownership")
	}
	return v, nil
}

// A single, controller-issued recovery pass consumes a strictly newer generation.
// Invalid or legacy markers cannot advance a claim.
func pairRecoveryOffset(payload string) int64 {
	var record struct {
		Repeat    int `json:"ssh_pair_reconciliation"`
		Readiness struct {
			Version int `json:"version"`
		} `json:"ssh_preparation_readiness"`
	}
	if json.Unmarshal([]byte(payload), &record) != nil || (record.Repeat != 1 && record.Repeat != 2) || record.Readiness.Version != 5 {
		return 0
	}
	return int64(record.Repeat)
}
