package clusterbootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func preparationSizingFixture(t *testing.T, rank int, reference, cap string) PreparationSizing {
	t.Helper()
	e, runtime := preparationFixture()
	runtime["BOREALIS_CLUSTER_SIZING_RANK"] = strconv.Itoa(rank)
	runtime["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = reference
	runtime["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = cap
	c, err := NewPreparationConfiguration(e, runtime)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Sizing()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPreparationSizingProfileBoundaries(t *testing.T) {
	for rank, minimum := range []struct {
		cpu uint32
		mib uint64
	}{{1, 1}, {8, 16384}, {16, 32768}, {24, 65536}} {
		t.Run(strconv.Itoa(rank), func(t *testing.T) {
			s := preparationSizingFixture(t, rank, strconv.FormatUint(minimum.mib, 10), "1Ki")
			// Rank zero's computed cap exceeds its rank floor even with a tiny override.
			hostMinimumMiB := max(minimum.mib, 1536)
			for _, host := range []struct {
				cpu uint32
				kib uint64
				ok  bool
			}{{minimum.cpu, hostMinimumMiB * 1024, true}, {minimum.cpu - 1, hostMinimumMiB * 1024, false},
				{minimum.cpu, hostMinimumMiB*1024 - 1, false}, {64, 262144 * 1024, true},
				{64, 0, false}, {64, 1 << 53, false}, {64, 1<<53 - 1, false}, {64, ^uint64(0), false},
				{999999999, 999999999*1024 + 1023, true}, {1000000000, minimum.mib * 1024, false}, {64, 1000000000 * 1024, false}} {
				if (s.Fits(host.cpu, host.kib) == nil) != host.ok {
					t.Fatalf("capacity boundary cpu=%d kib=%d", host.cpu, host.kib)
				}
			}
			if s.rank != rank || s.referenceMiB != minimum.mib {
				t.Fatal("larger target changed inherited profile")
			}
		})
	}
	// Smaller RAM than a CPU-limited source is allowed when the inherited
	// profile and both caps fit, without retuning from target RAM.
	s := preparationSizingFixture(t, 0, "131072", "4608m")
	if s.Fits(8, 32768*1024) != nil || s.Fits(8, 4608*1024) != nil || s.Fits(8, 4608*1024-1) == nil {
		t.Fatal("effective cap boundary or source reference substituted")
	}
	// Decimal T is not binary Ti; compare exact bytes without rounding down.
	s = preparationSizingFixture(t, 0, "131072", "1T")
	if s.Fits(8, 976562500) != nil || s.Fits(8, 976562499) == nil {
		t.Fatal("decimal byte boundary")
	}
}

func TestPreparationSizingRequiresOwnedExplicitConfiguration(t *testing.T) {
	for _, c := range []*PreparationConfiguration{nil, {}} {
		if _, err := c.Sizing(); err == nil {
			t.Fatal("unselected configuration accepted")
		}
	}
	e, runtime := preparationFixture()
	delete(runtime, "BOREALIS_POSTGRES_DB_MEMORY_LIMIT")
	c, err := NewPreparationConfiguration(e, runtime)
	if err != nil {
		t.Fatal("generic configuration lost optional-override compatibility")
	}
	if _, err := c.Sizing(); err == nil {
		t.Fatal("missing effective cap replaced with default")
	}
	for _, cap := range []string{"4GB", "4gb", "4ki", "4t", "8388608Ti", "9223373T"} {
		runtime["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = cap
		c, err = NewPreparationConfiguration(e, runtime)
		if err != nil {
			t.Fatal("generic syntactic quantity rejected", err)
		}
		if _, err := c.Sizing(); err == nil {
			t.Fatal("unsupported effective quantity accepted", cap)
		}
	}
	runtime["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "4g"
	c, err = NewPreparationConfiguration(e, runtime)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := c.Sizing()
	runtime["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "99g"
	runtime["BOREALIS_CLUSTER_SIZING_RANK"] = "3"
	after, err := c.Sizing()
	if err != nil || before != after {
		t.Fatal("caller changed owned selection")
	}
	raw, err := json.Marshal(before)
	var decoded PreparationSizing
	if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded.Fits(64, 262144*1024) == nil {
		t.Fatal("serialized projection gained selection")
	}
	for _, invalid := range []PreparationSizing{{}, {rank: -1, referenceMiB: 16384, postgresBytes: 1},
		{rank: 4, referenceMiB: 65536, postgresBytes: 1}, {rank: 1, referenceMiB: 16383, postgresBytes: 1},
		{referenceMiB: 1000000000, postgresBytes: 1}, {referenceMiB: 1, postgresBytes: 1 << 63}} {
		if invalid.Fits(64, 262144*1024) == nil {
			t.Fatal("invalid selection accepted")
		}
	}
}

// Extract only these pure functions. Never source or run Engine.sh startup,
// deployment, runtime detection or host mutation from a portable Go test.
func preparationSizingBash(t *testing.T, names []string, script string, args ...string) string {
	t.Helper()
	raw, err := os.ReadFile("../../../../../../Engine.sh")
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	source.WriteString("set -euo pipefail\ndie() { exit 7; }\n")
	for _, name := range names {
		marker := name + "() {\n"
		if strings.Count(string(raw), marker) != 1 {
			t.Fatal("runtime function missing or ambiguous", name)
		}
		_, tail, _ := strings.Cut(string(raw), marker)
		body, _, ok := strings.Cut(tail, "\n}\n")
		if !ok {
			t.Fatal("runtime function boundary missing", name)
		}
		source.WriteString(marker + body + "\n}\n")
	}
	source.WriteString(script)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", append([]string{"-c", source.String(), "sizing-fixture"}, args...)...).Output()
	if err != nil {
		t.Fatal("isolated sizing function failed", err)
	}
	return strings.TrimSpace(string(output))
}

func TestPreparationSizingRuntimeParity(t *testing.T) {
	for _, host := range []struct{ cpu, mib int }{{1, 1}, {7, 16383}, {8, 16384}, {15, 32767}, {16, 32768}, {23, 65535}, {24, 65536}, {64, 262144}} {
		out := preparationSizingBash(t, []string{"profile_rank_for_cpu", "profile_rank_for_memory"},
			"profile_rank_for_cpu \"$1\"; profile_rank_for_memory \"$2\"", strconv.Itoa(host.cpu), strconv.Itoa(host.mib))
		var cpuRank, memoryRank int
		if n, err := fmt.Sscanf(out, "%d\n%d", &cpuRank, &memoryRank); n != 2 || err != nil {
			t.Fatal("invalid runtime rank output")
		}
		for rank := range sizingMinimumCPU {
			if (uint32(host.cpu) >= sizingMinimumCPU[rank] && uint64(host.mib) >= sizingMinimumMiB[rank]) != (cpuRank >= rank && memoryRank >= rank) {
				t.Fatal("Go profile differs from runtime", host, rank)
			}
		}
	}
	for _, q := range []struct {
		raw, rendered string
		bytes         uint64
	}{{"4k", "4Ki", 4 << 10}, {"4K", "4Ki", 4 << 10}, {"4Ki", "4Ki", 4 << 10},
		{"4m", "4Mi", 4 << 20}, {"4M", "4Mi", 4 << 20}, {"4Mi", "4Mi", 4 << 20},
		{"4g", "4Gi", 4 << 30}, {"4G", "4Gi", 4 << 30}, {"4Gi", "4Gi", 4 << 30},
		{"4Ti", "4Ti", 4 << 40}, {"4T", "4T", 4000000000000},
		{"8388607Ti", "8388607Ti", 8388607 << 40}, {"9223372T", "9223372T", 9223372000000000000}} {
		out := preparationSizingBash(t, []string{"format_k3s_memory_quantity"}, "format_k3s_memory_quantity \"$1\"", q.raw)
		got, err := preparationMemoryBytes(q.raw)
		if out != q.rendered || err != nil || got != q.bytes {
			t.Fatal("effective quantity differs from runtime", q.raw)
		}
	}
	for _, value := range []string{"", "0m", "01m", "-1m", "+1m", "1.5Gi", "1", "1e9", "1P", "1t", "1GB", "1ki", "1 Mi", "1Gi\n", "1000000000Ki", "8388608Ti", "9223373T"} {
		if _, err := preparationMemoryBytes(value); err == nil {
			t.Fatal("invalid, ambiguous or overflowing bytes accepted", value)
		}
	}
}

func TestPreparationSizingComputedProfileRuntimeParity(t *testing.T) {
	functions := []string{"profile_rank_for_cpu", "profile_rank_for_memory", "profile_name_for_rank",
		"clamp_mib", "format_pg_memory_mib", "format_docker_memory_mib", "load_profile_tuning"}
	for rank, references := range [][]uint64{
		{1, 4095, 4096, 4097, 8195, 16383, 16384, 16387, 131072, 999999999},
		{16384, 16387, 32767, 32768, 32771, 999999999},
		{32768, 32771, 65535, 65536, 65539, 999999999},
		{65536, 65539, 98303, 98304, 98307, 999999999},
	} {
		for _, reference := range references {
			t.Run(fmt.Sprintf("rank%d/reference%d", rank, reference), func(t *testing.T) {
				// Extract actual runtime tuning and admission behavior without invoking
				// Engine startup, host detection, override loading or deployment.
				rankArg, refArg := strconv.Itoa(rank), strconv.FormatUint(reference, 10)
				out := preparationSizingBash(t, functions, `
load_profile_tuning 64 999999999 "$1" "$2"
printf '%s\n' "$PROFILE_POSTGRES_DB_MEMORY_LIMIT"
`, rankArg, refArg)
				computed, err := preparationMemoryBytes(out)
				if err != nil {
					t.Fatal("invalid runtime computed cap", out)
				}
				s := preparationSizingFixture(t, rank, refArg, "1Ki")
				if s.postgresProfileMiB()*(1<<20) != computed {
					t.Fatal("computed cap differs from Engine.sh", computed)
				}
				minimumKiB := max(sizingMinimumMiB[rank]*1024, computed/1024)
				// Runtime receives whole MiB; effective overrides still compare exact bytes.
				hosts := []uint64{minimumKiB - 1, minimumKiB, minimumKiB + 1, 262144 * 1024}
				args := []string{rankArg, refArg}
				for _, kib := range hosts {
					args = append(args, strconv.FormatUint(kib/1024, 10))
				}
				out = preparationSizingBash(t, functions, `
rank="$1"; reference="$2"; shift 2
for memory in "$@"; do
  if (load_profile_tuning 64 "$memory" "$rank" "$reference"); then
    printf 'fit\n'
  else
    printf 'reject\n'
  fi
done
`, args...)
				results := strings.Fields(out)
				if len(results) != len(hosts) {
					t.Fatal("invalid runtime guard results", out)
				}
				for i, kib := range hosts {
					if (s.Fits(64, kib) == nil) != (results[i] == "fit") {
						t.Fatalf("runtime guard differs at %d KiB: %s", kib, results[i])
					}
				}
				// Both caps remain mandatory: overrides below/equal/above the computed
				// cap cannot bypass it, and larger overrides retain exact KiB boundaries.
				for _, capKiB := range []uint64{1, computed/1024 - 1, computed / 1024, computed/1024 + 1, 262144 * 1024} {
					s = preparationSizingFixture(t, rank, refArg, fmt.Sprintf("%dKi", capKiB))
					boundary := max(minimumKiB, capKiB)
					if s.Fits(64, boundary) != nil || s.Fits(64, boundary-1) == nil {
						t.Fatalf("computed/effective cap boundary %d KiB", boundary)
					}
					if s.rank != rank || s.referenceMiB != reference {
						t.Fatal("target or override changed inherited tuning")
					}
				}
			})
		}
	}
}
