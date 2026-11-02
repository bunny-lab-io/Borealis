package clusterbootstrap

import (
	"regexp"
	"strconv"
	"strings"
)

// PreparationWorkerLimits carries effective settings, not a capacity decision.
// PIDs is retained configuration only: Kubernetes Pod resources do not enforce it.
type PreparationWorkerLimits struct {
	CPULimitMilli, MemoryLimitBytes, PIDs uint64
}

var preparationWorkerCPU = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,3})?m?$`)
var preparationWorkerPIDs = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

func preparationWorkerMilliCPU(raw string) (uint64, error) {
	if !preparationWorkerCPU.MatchString(raw) {
		return 0, ErrPreparationConfig
	}
	value, isMilli := strings.CutSuffix(raw, "m")
	whole, fraction, fractional := strings.Cut(value, ".")
	if isMilli && fractional {
		return 0, ErrPreparationConfig
	}
	milli, _ := strconv.ParseUint(whole, 10, 64)
	if !isMilli {
		part, _ := strconv.ParseUint(fraction+strings.Repeat("0", 3-len(fraction)), 10, 64)
		milli = milli*1000 + part
	}
	if milli == 0 || milli > 999999999 {
		return 0, ErrPreparationConfig
	}
	return milli, nil
}

// ProfileWorkerLimits reads explicit inherited effective values, including
// supported overrides. It never substitutes rank defaults or joining-host RAM.
func (c *PreparationConfiguration) ProfileWorkerLimits() (PreparationWorkerLimits, error) {
	if c == nil {
		return PreparationWorkerLimits{}, ErrPreparationConfig
	}
	rawCPU, rawPIDs := c.wire.Runtime["BOREALIS_SITE_WORKER_CPU_LIMIT"], c.wire.Runtime["BOREALIS_SITE_WORKER_PIDS_LIMIT"]
	milli, cpuErr := preparationWorkerMilliCPU(rawCPU)
	if cpuErr != nil || !preparationWorkerPIDs.MatchString(rawPIDs) {
		return PreparationWorkerLimits{}, ErrPreparationConfig
	}
	memory, err := preparationMemoryBytes(c.wire.Runtime["BOREALIS_SITE_WORKER_MEMORY_LIMIT"])
	pids, _ := strconv.ParseUint(rawPIDs, 10, 64)
	if err != nil || memory > 9007199254740991 {
		return PreparationWorkerLimits{}, ErrPreparationConfig
	}
	return PreparationWorkerLimits{milli, memory, pids}, nil
}

// PreparationApplicationLimit is one renderer-declared service envelope. It
// carries no authority, available capacity, worker bound or host reserve.
type PreparationApplicationLimit struct {
	Name                            string
	CPULimitMilli, MemoryLimitBytes uint64
}

// ProfileApplicationLimits requires explicit effective limits matching the
// selected Engine.sh defaults. PostgreSQL uses the inherited reference memory,
// never the joining host's RAM. Actual renderer parity tests cover all ranks,
// integer truncation and clamps. Overrides stay unsupported by this subtotal.
func (c *PreparationConfiguration) ProfileApplicationLimits() ([]PreparationApplicationLimit, error) {
	sizing, err := c.Sizing()
	if err != nil || c.wire.Runtime["BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY"] != sizingScheduledSlots[sizing.rank] {
		return nil, ErrPreparationConfig
	}
	limits := make([]PreparationApplicationLimit, 0, 7)
	for _, service := range []struct {
		name, setting string
		mib, milli    [4]uint64
	}{
		{"api-backend", "API_BACKEND", [4]uint64{512, 1024, 2048, 3072}, [4]uint64{1500, 2000, 4000, 6000}},
		{"job-scheduler", "JOB_SCHEDULER", [4]uint64{256, 512, 1024, 2048}, [4]uint64{1000, 1500, 2000, 3000}},
		{"webui-frontend", "WEBUI_FRONTEND", [4]uint64{256, 512, 768, 1024}, [4]uint64{500, 1000, 1000, 1500}},
		{"traefik-edge", "TRAEFIK_EDGE", [4]uint64{256, 512, 1024, 2048}, [4]uint64{1000, 1500, 2000, 4000}},
		{"postgres", "POSTGRES_DB", [4]uint64{}, [4]uint64{1500, 2000, 4000, 6000}},
		{"remote-desktop-guacd", "REMOTE_DESKTOP_GUACD", [4]uint64{256, 512, 1024, 2048}, [4]uint64{1000, 1000, 2000, 4000}},
		{"wireguard-tunnel", "WIREGUARD_TUNNEL", [4]uint64{128, 256, 512, 1024}, [4]uint64{1000, 1000, 2000, 2000}},
	} {
		mib := service.mib[sizing.rank]
		if service.name == "postgres" {
			mib = sizing.postgresProfileMiB()
		}
		memory, memoryErr := preparationMemoryBytes(c.wire.Runtime["BOREALIS_"+service.setting+"_MEMORY_LIMIT"])
		cpu, cpuErr := strconv.ParseFloat(c.wire.Runtime["BOREALIS_"+service.setting+"_CPU_LIMIT"], 64)
		if memoryErr != nil || cpuErr != nil || memory != mib<<20 || cpu != float64(service.milli[sizing.rank])/1000 {
			return nil, ErrPreparationConfig
		}
		limits = append(limits, PreparationApplicationLimit{service.name, service.milli[sizing.rank], memory})
	}
	return limits, nil
}
