package clusterbootstrap

import "strconv"

// These are Engine.sh's unconditional profile-derived settings, not a new
// provisioning policy or a complete supported-tuning/capacity declaration.
// Require the canonical strings written by that renderer; alternate spellings
// and overrides remain valid generic configuration but are not qualified here.
// Service caps and explicit-precedence settings retain their separate checks.
// Call only after valid() bounds the inherited rank and reference arithmetic.
func (s PreparationSizing) profileTuning() map[string]string {
	byRank := func(values [4]string) string { return values[s.rank] }
	cacheMinimum := [...]uint64{4096, 8192, 20480, 32768}
	cacheMaximum := [...]uint64{12288, 16384, 32768, 65536}
	cache := min(max(s.referenceMiB*625/1000, cacheMinimum[s.rank]), cacheMaximum[s.rank])
	return map[string]string{
		"BOREALIS_DB_POOL_SIZE":                             byRank([4]string{"10", "12", "20", "24"}),
		"BOREALIS_DB_MAX_OVERFLOW":                          byRank([4]string{"10", "16", "20", "24"}),
		"BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY":        sizingScheduledSlots[s.rank],
		"BOREALIS_POSTGRES_MAX_CONNECTIONS":                 byRank([4]string{"80", "120", "150", "180"}),
		"BOREALIS_POSTGRES_SHARED_BUFFERS":                  preparationPostgresMemoryMiB(s.postgresSharedMiB()),
		"BOREALIS_POSTGRES_EFFECTIVE_CACHE_SIZE":            preparationPostgresMemoryMiB(cache),
		"BOREALIS_POSTGRES_WORK_MEM":                        byRank([4]string{"4MB", "8MB", "8MB", "16MB"}),
		"BOREALIS_POSTGRES_MAINTENANCE_WORK_MEM":            byRank([4]string{"256MB", "512MB", "512MB", "1GB"}),
		"BOREALIS_POSTGRES_MAX_WORKER_PROCESSES":            byRank([4]string{"8", "8", "12", "16"}),
		"BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS":            byRank([4]string{"8", "8", "12", "16"}),
		"BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER": byRank([4]string{"2", "2", "4", "4"}),
		"BOREALIS_POSTGRES_AUTOVACUUM_MAX_WORKERS":          byRank([4]string{"3", "4", "5", "6"}),
		"BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT":    byRank([4]string{"1000", "1500", "2000", "2500"}),
		"BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME":              byRank([4]string{"30s", "20s", "15s", "15s"}),
		"BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR":  "0.02",
		"BOREALIS_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR": "0.01",
		"BOREALIS_POSTGRES_MAX_WAL_SIZE":                    byRank([4]string{"4GB", "6GB", "8GB", "12GB"}),
		"BOREALIS_POSTGRES_MIN_WAL_SIZE":                    byRank([4]string{"512MB", "1GB", "1GB", "2GB"}),
		"BOREALIS_POSTGRES_EFFECTIVE_IO_CONCURRENCY":        byRank([4]string{"16", "32", "64", "64"}),
		"BOREALIS_POSTGRES_WAL_COMPRESSION":                 "on",
		"BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT":              "15min",
		"BOREALIS_POSTGRES_CHECKPOINT_COMPLETION_TARGET":    "0.9",
		"BOREALIS_POSTGRES_RANDOM_PAGE_COST":                "1.1",
	}
}

// Mirror format_pg_memory_mib, preserving its integer-MiB truncation and
// canonical PostgreSQL spelling independently of container quantity parsing.
func preparationPostgresMemoryMiB(mib uint64) string {
	if mib >= 1024 && mib%1024 == 0 {
		return strconv.FormatUint(mib/1024, 10) + "GB"
	}
	return strconv.FormatUint(mib, 10) + "MB"
}
