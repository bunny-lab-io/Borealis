package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const clusterSSHApplicationDeploymentPrefix = "/apis/apps/v1/namespaces/borealis/deployments/"

// Keep in parity with reconcile-node-workloads.py DEPLOYMENT_SERVICES. Each
// joining target receives one active replica of every template, regardless of
// the generic template's current replica count. No candidate/failed-node reads.
var clusterSSHApplicationDeployments = []string{"api-backend", "borealis-cluster-controller", "borealis-operator", "job-scheduler", "remote-desktop-guacd", "traefik-edge", "webui-frontend", "wireguard-tunnel"}

type clusterSSHApplicationResource struct {
	Name               string `json:"name"`
	CPURequestMilli    uint64 `json:"cpu_request_milli"`
	CPULimitMilli      uint64 `json:"cpu_limit_milli"`
	MemoryRequestBytes uint64 `json:"memory_request_bytes"`
	MemoryLimitBytes   uint64 `json:"memory_limit_bytes"`
}

func clusterSSHApplicationDeploymentPathValid(path string) bool {
	return slices.ContainsFunc(clusterSSHApplicationDeployments, func(name string) bool {
		return path == clusterSSHApplicationDeploymentPrefix+name
	})
}

var clusterSSHWorkloadCPU = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(?:\.([0-9]{1,3}))?(m)?$`)
var clusterSSHWorkloadDecimalMemory = regexp.MustCompile(`^([1-9][0-9]{0,15})(k|M|G|T)$`)

// Bounded exact Kubernetes units, not Engine.sh's lowercase binary aliases.
// Never round fractional millicpu or fractional bytes into a smaller demand.
func clusterSSHWorkloadMilliCPU(value any) (uint64, bool) {
	s, ok := value.(string)
	parts := clusterSSHWorkloadCPU.FindStringSubmatch(s)
	if !ok || parts == nil || (parts[2] != "" && parts[3] != "") {
		return 0, false
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	if parts[3] == "" {
		n *= 1000
		if parts[2] != "" {
			fraction, _ := strconv.ParseUint(parts[2]+strings.Repeat("0", 3-len(parts[2])), 10, 64)
			n += fraction
		}
	}
	return n, n > 0 && n <= clusterSSHCapacityLimit
}

func clusterSSHWorkloadMemory(value any) (uint64, bool) {
	if n, ok := clusterSSHStorageBytes(value); ok {
		return n, n <= clusterSSHCapacityLimit
	}
	s, ok := value.(string)
	parts := clusterSSHWorkloadDecimalMemory.FindStringSubmatch(s)
	if !ok || parts == nil {
		return 0, false
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	factor := map[string]uint64{"k": 1000, "M": 1000000, "G": 1000000000, "T": 1000000000000}[parts[2]]
	if err != nil || n > clusterSSHCapacityLimit/factor {
		return 0, false
	}
	return n * factor, true
}

func clusterSSHApplicationBudget(values []clusterSSHApplicationResource) (cpu, memory uint64, ok bool) {
	if len(values) != len(clusterSSHApplicationDeployments) {
		return 0, 0, false
	}
	for i, v := range values {
		if v.Name != clusterSSHApplicationDeployments[i] || v.CPURequestMilli == 0 || v.CPURequestMilli > v.CPULimitMilli || v.CPULimitMilli > clusterSSHCapacityLimit ||
			v.MemoryRequestBytes == 0 || v.MemoryRequestBytes > v.MemoryLimitBytes || v.MemoryLimitBytes > clusterSSHCapacityLimit {
			return 0, 0, false
		}
		cpu, ok = clusterSSHCapacitySum(cpu, v.CPURequestMilli)
		if !ok {
			return 0, 0, false
		}
		// Conservative simultaneous application cap budget, not a Kubernetes
		// reservation or proof of available RAM. CPU caps may overcommit.
		memory, ok = clusterSSHCapacitySum(memory, v.MemoryLimitBytes)
		if !ok {
			return 0, 0, false
		}
	}
	return cpu, memory, true
}

func observeClusterSSHApplicationResources(read func(string) (map[string]any, error)) ([]clusterSSHApplicationResource, error) {
	fail := func() ([]clusterSSHApplicationResource, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if read == nil {
		return fail()
	}
	values := make([]clusterSSHApplicationResource, 0, len(clusterSSHApplicationDeployments))
	uids := map[string]bool{}
	for _, name := range clusterSSHApplicationDeployments {
		object, err := read(clusterSSHApplicationDeploymentPrefix + name)
		id, valid := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "borealis")
		spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
		// Current fixed application templates have one ordinary container. Refuse
		// unaccounted init/sidecars, Pod resources, DRA and RuntimeClass overhead.
		containers, containersOK := spec["containers"].([]any)
		if err != nil || !valid || id.Name != name || uids[id.UID] || !containersOK || len(containers) != 1 ||
			!clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) ||
			!clusterSSHStorageEmptyMap(spec["resources"]) || !clusterSSHStorageEmptyMap(spec["overhead"]) ||
			!clusterSSHStorageEmptyList(spec["resourceClaims"]) || !clusterSSHStorageEmptyText(spec["runtimeClassName"]) {
			return fail()
		}
		uids[id.UID] = true
		container, valid := containers[0].(map[string]any)
		v, resourcesOK := clusterSSHWorkloadResources(name, container["resources"])
		if !valid || !clusterSSHStorageName(clusterSSHStorageText(container, "name")) || !resourcesOK {
			return fail()
		}
		values = append(values, v)
	}
	if _, _, ok := clusterSSHApplicationBudget(values); !ok {
		return fail()
	}
	return values, nil
}

// Reuse the original sizing scope for full source binding, claim lifetime and
// repeated native target readings. Freeze this subtotal and its source receipt
// too; a caller cannot bypass drift by returning otherwise unchanged settings.
func withClusterSSHTargetApplicationSizing(parent context.Context, readers clusterSSHNetworkTargetReaders,
	source clusterSSHPreparationSnapshotRead, consume func(context.Context, []clusterSSHTargetHostEvidence) error) error {
	var retained []clusterSSHApplicationResource
	var manager clusterSSHLonghornManagerResources
	var provisioner clusterSSHLonghornProvisionerResources
	var resizer clusterSSHLonghornResizerResources
	var snapshotter clusterSSHLonghornSnapshotterResources
	var plugin clusterSSHLonghornPluginResources
	var attacher clusterSSHLonghornAttacherResources
	var ui clusterSSHLonghornUIResources
	var driver clusterSSHLonghornDriverResources
	var postgres, kubeVIP clusterSSHApplicationResource
	var operator clusterSSHCNPGOperatorResources
	var upgrade clusterSSHSystemUpgradeResources
	var certManager, webhook, cainjector clusterSSHCertManagerResources
	var snapshot clusterSSHSnapshotResources
	var workers []clusterSSHSiteWorkerResource
	var sites []int64
	var policy clusterSSHWorkerPolicy
	observation := ""
	var cpu, memory uint64
	settings := func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		if source == nil {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
		}
		v, err := source(ctx)
		values := v.Storage.Requirements.Applications
		var ok bool
		cpu, memory, ok = clusterSSHDesiredWorkerWorkloadBudget(v)
		if err != nil || !ok || !clusterSSHSourceObservationRE.MatchString(v.Storage.Observation) ||
			(retained != nil && (!slices.Equal(retained, values) || !slices.Equal(workers, v.Storage.Requirements.SiteWorkers) || !slices.Equal(sites, v.DesiredWorkerSites) || policy != v.Storage.Requirements.WorkerPolicy || postgres != v.Storage.Requirements.PostgresResources || operator != v.Storage.Requirements.CNPGOperatorResources || upgrade != v.Storage.Requirements.SystemUpgradeResources || snapshot != v.Storage.Requirements.SnapshotResources || certManager != v.Storage.Requirements.CertManagerResources || webhook != v.Storage.Requirements.CertManagerWebhookResources || cainjector != v.Storage.Requirements.CertManagerCainjectorResources || kubeVIP != v.Storage.Requirements.KubeVIP.Resources || manager != v.Storage.Requirements.LonghornManagerResources || driver != v.Storage.Requirements.LonghornDriverResources || attacher != v.Storage.Requirements.LonghornAttacherResources || provisioner != v.Storage.Requirements.LonghornProvisionerResources || resizer != v.Storage.Requirements.LonghornResizerResources || snapshotter != v.Storage.Requirements.LonghornSnapshotterResources || plugin != v.Storage.Requirements.LonghornPluginResources || ui != v.Storage.Requirements.LonghornUIResources || observation != v.Storage.Observation)) {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
		}
		retained, observation = slices.Clone(values), v.Storage.Observation
		postgres = v.Storage.Requirements.PostgresResources
		manager = v.Storage.Requirements.LonghornManagerResources
		attacher = v.Storage.Requirements.LonghornAttacherResources
		provisioner = v.Storage.Requirements.LonghornProvisionerResources
		resizer = v.Storage.Requirements.LonghornResizerResources
		snapshotter = v.Storage.Requirements.LonghornSnapshotterResources
		plugin = v.Storage.Requirements.LonghornPluginResources
		ui = v.Storage.Requirements.LonghornUIResources
		driver = v.Storage.Requirements.LonghornDriverResources
		kubeVIP = v.Storage.Requirements.KubeVIP.Resources
		operator = v.Storage.Requirements.CNPGOperatorResources
		upgrade = v.Storage.Requirements.SystemUpgradeResources
		snapshot = v.Storage.Requirements.SnapshotResources
		certManager = v.Storage.Requirements.CertManagerResources
		webhook = v.Storage.Requirements.CertManagerWebhookResources
		cainjector = v.Storage.Requirements.CertManagerCainjectorResources
		workers = slices.Clone(v.Storage.Requirements.SiteWorkers)
		sites, policy = slices.Clone(v.DesiredWorkerSites), v.Storage.Requirements.WorkerPolicy
		return v.Expected, v.Settings, nil
	}
	return withClusterSSHTargetSizing(parent, readers, settings, func(ctx context.Context, targets []clusterSSHTargetHostEvidence) error {
		if consume == nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		for _, target := range targets {
			// Fits already validated hardware bounds before this callback.
			if cpu > uint64(target.Host.CPUCount)*1000 || memory > target.Host.MemoryKiB*1024 {
				return clusterbootstrap.ErrPreparationConfig
			}
		}
		return consume(ctx, targets)
	})
}
