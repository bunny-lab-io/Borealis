package clusterbootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

const PreparationTargetCPUVersion = 1

// PreparationTargetCPU carries the fixed fresh-target kubelet plan and an
// equality receipt for its native CPU and MemTotal observation. Validate checks
// the wire contract, not host visibility, cgroups, affinity, configuration
// custody or freshness. Those proofs remain bound by the native observation.
type PreparationTargetCPU struct {
	Version           int    `json:"version"`
	PlanSHA256        string `json:"plan_sha256"`
	OnlineCPUs        string `json:"online_cpus"`
	AllocatableMilli  uint64 `json:"allocatable_milli"`
	MemoryKiB         uint64 `json:"memory_kib"`
	ObservationSHA256 string `json:"observation_sha256"`
}

// PreparationTargetCPUArguments makes existing fresh-host CPU defaults explicit.
// This is not a host/runtime reserve budget. These reservation-map flags replace
// entire configured maps, so callers must reject inherited custom reservations.
// Pinned provenance: AddKubeletConfigFlags and SetDefaults_KubeletConfiguration:
// https://github.com/k3s-io/kubernetes/blob/v1.36.3-k3s1/cmd/kubelet/app/options/options.go
// https://github.com/k3s-io/kubernetes/blob/v1.36.3-k3s1/pkg/kubelet/apis/config/v1beta1/defaults.go
// K3s defaultKubeletConfig adds no reservations; GetArgs preserves reserved-cpus=:
// https://github.com/k3s-io/k3s/blob/v1.36.3%2Bk3s1/pkg/daemons/agent/agent.go
// https://github.com/k3s-io/k3s/blob/v1.36.3%2Bk3s1/pkg/util/args.go
func PreparationTargetCPUArguments(k3sVersion string) ([]string, error) {
	if k3sVersion != K3sPins().Version {
		return nil, ErrPreparationConfig
	}
	return []string{
		"kube-reserved=cpu=0",
		"system-reserved=cpu=0",
		"reserved-cpus=",
		"cpu-manager-policy=none",
		"enforce-node-allocatable=pods",
	}, nil
}

// PreparationTargetCPUPlanDigest binds exact arguments to reviewed K3s release,
// launcher and embedded server executable. It does not attest target state.
func PreparationTargetCPUPlanDigest(k3sVersion string) (string, error) {
	args, err := PreparationTargetCPUArguments(k3sVersion)
	if err != nil {
		return "", err
	}
	pins := K3sPins()
	raw, err := json.Marshal(struct {
		Domain       string   `json:"domain"`
		Version      int      `json:"version"`
		K3sVersion   string   `json:"k3s_version"`
		BinarySHA256 string   `json:"binary_sha256"`
		ServerSHA256 string   `json:"server_sha256"`
		Arguments    []string `json:"arguments"`
	}{"borealis-preparation-target-cpu", PreparationTargetCPUVersion, k3sVersion, pins.Binary.SHA256, pins.ServerExecutable.SHA256, args})
	if err != nil {
		return "", ErrPreparationConfig
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (cpu PreparationTargetCPU) Validate(k3sVersion string) error {
	plan, err := PreparationTargetCPUPlanDigest(k3sVersion)
	if err != nil || cpu.Version != PreparationTargetCPUVersion || cpu.PlanSHA256 != plan ||
		!digestPattern.MatchString(cpu.ObservationSHA256) || cpu.ObservationSHA256 == strings.Repeat("0", 64) {
		return ErrPreparationConfig
	}
	count, err := preparationTargetCPUCount(cpu.OnlineCPUs)
	// Kubelet CapacityFromMachineInfo uses logical CPUs * 1000; the fixed plan
	// subtracts zero CPU reservation. Native evidence must separately prove all
	// these CPUs online and available to the planned service and Pod cgroups.
	// https://github.com/k3s-io/kubernetes/blob/v1.36.3-k3s1/pkg/kubelet/cadvisor/util.go
	// https://github.com/k3s-io/kubernetes/blob/v1.36.3-k3s1/pkg/kubelet/cm/node_container_manager_linux.go
	// MemTotal uses Linux's KiB convention. Preserve the existing exact byte
	// capacity ceiling when consumers convert to bytes; do not infer a reserve.
	if err != nil || cpu.AllocatableMilli != count*1000 || cpu.MemoryKiB == 0 || cpu.MemoryKiB > (1<<53-1)/1024 {
		return ErrPreparationConfig
	}
	return nil
}

// Accept only sorted, disjoint, maximal ranges of IDs 0..4095. The bound makes
// both parsing and the resulting milliCPU arithmetic independent of wire input.
func preparationTargetCPUCount(list string) (uint64, error) {
	if len(list) == 0 || len(list) > 4096*5 {
		return 0, ErrPreparationConfig
	}
	parse := func(raw string) (int, error) {
		if len(raw) < 1 || len(raw) > 4 || len(raw) > 1 && raw[0] == '0' {
			return 0, ErrPreparationConfig
		}
		for _, char := range raw {
			if char < '0' || char > '9' {
				return 0, ErrPreparationConfig
			}
		}
		id, err := strconv.ParseUint(raw, 10, 12)
		if err != nil {
			return 0, ErrPreparationConfig
		}
		return int(id), nil
	}
	previous := -2
	var count uint64
	for _, segment := range strings.Split(list, ",") {
		firstRaw, lastRaw, ranged := strings.Cut(segment, "-")
		first, err := parse(firstRaw)
		if err != nil || first <= previous+1 {
			return 0, ErrPreparationConfig
		}
		last := first
		if ranged {
			last, err = parse(lastRaw)
			if err != nil || last <= first {
				return 0, ErrPreparationConfig
			}
		}
		count += uint64(last-first) + 1
		previous = last
	}
	return count, nil
}
