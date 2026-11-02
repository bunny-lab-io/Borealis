package clusterbootstrap

import "strconv"

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
