package clusterbootstrap

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPreparationSizingProfileApplicationLimits(t *testing.T) {
	functions := []string{"profile_rank_for_cpu", "profile_rank_for_memory", "profile_name_for_rank",
		"clamp_mib", "format_pg_memory_mib", "format_docker_memory_mib", "load_profile_tuning"}
	for rank := 0; rank < 4; rank++ {
		for _, reference := range []uint64{1, 4095, 4096, 4099, 8192, 16383, 16384, 16387, 32768, 65535, 65536, 65539, 98304, 999999999} {
			if reference < sizingMinimumMiB[rank] {
				continue
			}
			t.Run(fmt.Sprintf("rank%d/reference%d", rank, reference), func(t *testing.T) {
				// Real production tuning supplies all effective values. An oversized
				// synthetic joining host permits testing the full reference domain.
				output := preparationSizingBash(t, functions, `load_profile_tuning 64 999999999 "$1" "$2"
for service in API_BACKEND JOB_SCHEDULER WEBUI_FRONTEND TRAEFIK_EDGE POSTGRES_DB REMOTE_DESKTOP_GUACD WIREGUARD_TUNNEL; do
  for resource in CPU MEMORY; do
    variable="PROFILE_${service}_${resource}_LIMIT"
    printf 'BOREALIS_%s_%s_LIMIT=%s\n' "$service" "$resource" "${!variable}"
  done
done
printf 'BOREALIS_CLUSTER_SIZING_RANK=%s\nBOREALIS_CLUSTER_SIZING_MEMORY_MIB=%s\nBOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=%s\n' "$PROFILE_RANK" "$PROFILE_CLUSTER_SIZING_MEMORY_MIB" "$PROFILE_SITE_WORKER_CONCURRENCY"
`, strconv.Itoa(rank), strconv.FormatUint(reference, 10))
				e, settings := preparationSizingRuntimeFixture()
				keys := []string{}
				for _, line := range strings.Split(output, "\n") {
					key, value, ok := strings.Cut(line, "=")
					if !ok {
						t.Fatal("invalid production profile output")
					}
					settings[key], keys = value, append(keys, key)
				}
				check := func(values map[string]string) ([]PreparationApplicationLimit, error) {
					c, err := NewPreparationConfiguration(e, values)
					if err != nil {
						return nil, err
					}
					return c.ProfileApplicationLimits()
				}
				limits, err := check(settings)
				if err != nil || len(keys) != 17 || len(limits) != 7 {
					t.Fatal("actual inherited defaults rejected", err)
				}
				for _, key := range keys {
					missing := maps.Clone(settings)
					delete(missing, key)
					if partial, err := check(missing); err == nil || partial != nil {
						t.Fatal("missing effective setting accepted", key)
					}
					// Reference changes can legitimately produce identical clamped
					// defaults; malformed reference remains rejected independently.
					missing[key] = "9"
					if strings.HasSuffix(key, "MEMORY_LIMIT") {
						missing[key] = "9Gi"
					}
					if key == "BOREALIS_CLUSTER_SIZING_MEMORY_MIB" {
						missing[key] = "0"
					}
					if partial, err := check(missing); err == nil || partial != nil {
						t.Fatal("unsupported effective setting accepted", key)
					}
				}
				// Supported equivalent units keep bytes exact, without admitting
				// sub-millicore overrides that round to a declared default.
				for _, limit := range limits {
					if limit.Name == "api-backend" {
						settings["BOREALIS_API_BACKEND_MEMORY_LIMIT"] = strconv.FormatUint(limit.MemoryLimitBytes>>20, 10) + "Mi"
						settings["BOREALIS_API_BACKEND_CPU_LIMIT"] = fmt.Sprintf("%.6f", float64(limit.CPULimitMilli)/1000)
					}
				}
				again, err := check(settings)
				if err != nil || !slices.Equal(again, limits) {
					t.Fatal("equivalent limits changed envelope", err)
				}
				limits[0].MemoryLimitBytes++
				fresh, err := check(settings)
				if err != nil || !slices.Equal(fresh, again) {
					t.Fatal("caller mutated retained profile", err)
				}
				settings["BOREALIS_API_BACKEND_CPU_LIMIT"] = fmt.Sprintf("%.6f", float64(again[0].CPULimitMilli)/1000+0.000001)
				if _, err := check(settings); err == nil {
					t.Fatal("rounded CPU override accepted")
				}
			})
		}
	}
	var absent *PreparationConfiguration
	if _, err := absent.ProfileApplicationLimits(); err == nil {
		t.Fatal("unselected configuration accepted")
	}
}
