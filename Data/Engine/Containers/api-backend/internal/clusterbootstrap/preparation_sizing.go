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
	return value, nil
}

var sizingMinimumCPU = [...]uint32{1, 8, 16, 24}
var sizingMinimumMiB = [...]uint64{1, 16384, 32768, 65536}

func (s PreparationSizing) valid() bool {
	return s.rank >= 0 && s.rank < len(sizingMinimumCPU) && s.referenceMiB >= sizingMinimumMiB[s.rank] &&
		s.referenceMiB <= 999999999 && s.postgresBytes > 0 && s.postgresBytes <= math.MaxInt64
}

// Fits preserves the source profile even on larger targets. A CPU-limited
// source may have a larger RAM reference than a target: only the profile floor
// and actual effective cap must fit. This is not available-RAM/reservation proof.
func (s PreparationSizing) Fits(cpu uint32, memoryKiB uint64) error {
	if !s.valid() || cpu < sizingMinimumCPU[s.rank] || cpu > 999999999 || memoryKiB == 0 || memoryKiB > 1<<53-1 || memoryKiB/1024 > 999999999 ||
		memoryKiB/1024 < sizingMinimumMiB[s.rank] || s.postgresBytes > memoryKiB*1024 {
		return ErrPreparationConfig
	}
	return nil
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
