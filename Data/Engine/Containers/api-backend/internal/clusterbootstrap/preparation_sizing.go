package clusterbootstrap

import (
	"math"
	"regexp"
	"strconv"
)

// PreparationSizing contains only the source's bounded sizing settings. It
// grants no authority and does not qualify storage or aggregate workloads.
// Keep fields private so zero/serialized values cannot stand in for selection.
type PreparationSizing struct {
	rank          int
	referenceMiB  uint64
	postgresBytes uint64
}

// Sizing requires the effective source cap explicitly. Generic preparation
// configuration still permits absent optional overrides, but capacity approval
// cannot guess an effective cap from another release's defaults.
func (c *PreparationConfiguration) Sizing() (PreparationSizing, error) {
	if c == nil || len(c.raw) == 0 {
		return PreparationSizing{}, ErrPreparationConfig
	}
	rank := c.wire.Runtime["BOREALIS_CLUSTER_SIZING_RANK"]
	reference := c.wire.Runtime["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"]
	memory, err := strconv.ParseUint(reference, 10, 64)
	if len(rank) != 1 || rank[0] < '0' || rank[0] > '3' || !preparationInteger.MatchString(reference) || err != nil || memory < 1 {
		return PreparationSizing{}, ErrPreparationConfig
	}
	cap, err := preparationMemoryBytes(c.wire.Runtime["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"])
	value := PreparationSizing{rank: int(rank[0] - '0'), referenceMiB: memory, postgresBytes: cap}
	if err != nil || !value.valid() {
		return PreparationSizing{}, ErrPreparationConfig
	}
	// Engine.sh renders these settings from the inherited profile even when
	// supplied explicitly. Reject tuning that would change on the joining host.
	// Absent optional settings continue deriving from the inherited profile.
	for key, expected := range value.profileTuning() {
		if supplied, present := c.wire.Runtime[key]; present && supplied != expected {
			return PreparationSizing{}, ErrPreparationConfig
		}
	}
	return value, nil
}

var sizingScheduledSlots = [...]string{"5", "8", "12", "16"}

// SmallBusinessApplicationLimits selects only the declared application budget
// qualified against Engine.sh. Explicit effective limits are required: missing
// settings cannot borrow this release's defaults. This is not a worker,
// infrastructure, host-reserve or complete allowed-tuning contract.
func (c *PreparationConfiguration) SmallBusinessApplicationLimits() error {
	sizing, err := c.Sizing()
	if err != nil || sizing.rank != 1 || sizing.referenceMiB != 16384 || c.wire.Runtime["BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY"] != "8" {
		return ErrPreparationConfig
	}
	for _, service := range []struct {
		name string
		mib  uint64
		cpu  float64
	}{
		{"API_BACKEND", 1024, 2}, {"JOB_SCHEDULER", 512, 1.5},
		{"WEBUI_FRONTEND", 512, 1}, {"TRAEFIK_EDGE", 512, 1.5},
		{"POSTGRES_DB", 6144, 2}, {"REMOTE_DESKTOP_GUACD", 512, 1},
		{"WIREGUARD_TUNNEL", 256, 1},
	} {
		memory, memoryErr := preparationMemoryBytes(c.wire.Runtime["BOREALIS_"+service.name+"_MEMORY_LIMIT"])
		cpu, cpuErr := strconv.ParseFloat(c.wire.Runtime["BOREALIS_"+service.name+"_CPU_LIMIT"], 64)
		if memoryErr != nil || cpuErr != nil || memory != service.mib<<20 || cpu != service.cpu {
			return ErrPreparationConfig
		}
	}
	return nil
}

// These are existing profile selection thresholds, not validated operational
// hardware minimums. Rank zero deliberately has no published safe floor.
var sizingMinimumCPU = [...]uint32{1, 8, 16, 24}
var sizingMinimumMiB = [...]uint64{1, 16384, 32768, 65536}

func (s PreparationSizing) valid() bool {
	return s.rank >= 0 && s.rank < len(sizingMinimumCPU) && s.referenceMiB >= sizingMinimumMiB[s.rank] &&
		s.referenceMiB <= 999999999 && s.postgresBytes > 0 && s.postgresBytes <= math.MaxInt64
}

// Fits preserves the source profile even on larger targets. A CPU-limited
// source may have a larger RAM reference than a target: only the profile floor
// and both computed and effective caps must fit. This is not available-RAM/reservation proof.
func (s PreparationSizing) Fits(cpu uint32, memoryKiB uint64) error {
	if !s.valid() || cpu < sizingMinimumCPU[s.rank] || cpu > 999999999 || memoryKiB == 0 || memoryKiB > 1<<53-1 || memoryKiB/1024 > 999999999 ||
		memoryKiB/1024 < sizingMinimumMiB[s.rank] || s.postgresBytes > memoryKiB*1024 {
		return ErrPreparationConfig
	}
	if s.postgresProfileMiB() > memoryKiB/1024 {
		return ErrPreparationConfig
	}
	return nil
}

// Mirror Engine.sh load_profile_tuning's guard before service overrides. The
// inherited reference drives shared_buffers, not the target's own RAM. Call
// only after valid() bounds both rank and arithmetic inputs.
func (s PreparationSizing) postgresProfileMiB() uint64 {
	extra := [...]uint64{512, 2048, 4096, 8192}
	return s.postgresSharedMiB() + extra[s.rank]
}

func (s PreparationSizing) postgresSharedMiB() uint64 {
	minimum := [...]uint64{1024, 4096, 8192, 12288}
	maximum := [...]uint64{4096, 8192, 16384, 24576}
	shared := s.referenceMiB * 25 / 100
	return min(max(shared, minimum[s.rank]), maximum[s.rank])
}

var preparationEffectiveMemory = regexp.MustCompile(`^([1-9][0-9]{0,8})(Ki|Mi|Gi|Ti|k|K|m|M|g|G|T)$`)

// Match Engine.sh format_k3s_memory_quantity after the private configuration's
// stricter integer-only validation: k/m/g (either case) become binary Ki/Mi/Gi,
// existing Ki/Mi/Gi/Ti remain binary, and T retains Kubernetes decimal meaning.
// Unsupported spellings and values outside signed byte bounds fail explicitly.
func preparationMemoryBytes(value string) (uint64, error) {
	matches := preparationEffectiveMemory.FindStringSubmatch(value)
	if matches == nil {
		return 0, ErrPreparationConfig
	}
	n, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, ErrPreparationConfig
	}
	factor := map[string]uint64{"k": 1 << 10, "K": 1 << 10, "Ki": 1 << 10,
		"m": 1 << 20, "M": 1 << 20, "Mi": 1 << 20, "g": 1 << 30, "G": 1 << 30, "Gi": 1 << 30,
		"Ti": 1 << 40, "T": 1000000000000}[matches[2]]
	if factor == 0 || n > math.MaxInt64/factor {
		return 0, ErrPreparationConfig
	}
	return n * factor, nil
}
