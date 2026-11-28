package clusterbootstrap

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func preparationPlanProfileFixture(t *testing.T, rank int, reference uint64) (PreparationPlan, PreparationExpected, map[string]string, []string) {
	t.Helper()
	output := preparationSizingBash(t, []string{"profile_rank_for_cpu", "profile_rank_for_memory", "profile_name_for_rank",
		"clamp_mib", "format_pg_memory_mib", "format_docker_memory_mib", "load_profile_tuning"}, `load_profile_tuning 64 999999999 "$1" "$2"
for service in API_BACKEND JOB_SCHEDULER WEBUI_FRONTEND TRAEFIK_EDGE POSTGRES_DB REMOTE_DESKTOP_GUACD WIREGUARD_TUNNEL; do
  for resource in CPU MEMORY; do
    variable="PROFILE_${service}_${resource}_LIMIT"
    printf 'BOREALIS_%s_%s_LIMIT=%s\n' "$service" "$resource" "${!variable}"
  done
done
for resource in CPU MEMORY PIDS; do
  variable="PROFILE_SITE_WORKER_${resource}_LIMIT"
  printf 'BOREALIS_SITE_WORKER_%s_LIMIT=%s\n' "$resource" "${!variable}"
done
printf 'BOREALIS_CLUSTER_SIZING_RANK=%s\nBOREALIS_CLUSTER_SIZING_MEMORY_MIB=%s\nBOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=%s\n' "$PROFILE_RANK" "$PROFILE_CLUSTER_SIZING_MEMORY_MIB" "$PROFILE_SITE_WORKER_CONCURRENCY"
`, strconv.Itoa(rank), strconv.FormatUint(reference, 10))
	expected, runtime := preparationSizingRuntimeFixture()
	var keys []string
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || slices.Contains(keys, key) {
			t.Fatal("invalid or duplicate production profile output")
		}
		keys = append(keys, key)
		runtime[key] = value
	}
	if len(keys) != 20 {
		t.Fatal("effective profile setting inventory changed", len(keys))
	}
	plan := PreparationPlan{Source: expected.Source, PlanSHA256: expected.CohortSHA256,
		KubeSystemUID: expected.KubeSystemUID, K3sVersion: expected.K3sVersion,
		ManagementCIDR: expected.ManagementCIDR, PodCIDR: expected.PodCIDR, ServiceCIDR: expected.ServiceCIDR,
		ControlPlaneVIP: expected.ControlPlaneVIP, EdgeVIP: expected.EdgeVIP,
		PeerAddresses: slices.Clone(expected.PeerAddresses), TargetAddresses: []string{expected.Target.Address}}
	return plan, expected, runtime, keys
}

// Compose the existing claimed public methods independently of the waiting-plan
// helper. Fixture identity is used only for comparison, never added to the plan.
func preparationClaimedProfileLimits(expected PreparationExpected, runtime map[string]string) (PreparationSizing, []PreparationApplicationLimit, PreparationWorkerLimits, error) {
	configuration, err := NewPreparationConfiguration(expected, runtime)
	if err != nil {
		return PreparationSizing{}, nil, PreparationWorkerLimits{}, err
	}
	sizing, err := configuration.Sizing()
	if err != nil {
		return PreparationSizing{}, nil, PreparationWorkerLimits{}, err
	}
	applications, err := configuration.ProfileApplicationLimits()
	if err != nil {
		return PreparationSizing{}, nil, PreparationWorkerLimits{}, err
	}
	workers, err := configuration.ProfileWorkerLimits()
	return sizing, applications, workers, err
}

func TestPreparationPlanProfileLimitsClaimParity(t *testing.T) {
	for rank, profile := range []string{"homelab", "small-business", "msp", "enterprise"} {
		for _, reference := range []uint64{sizingMinimumMiB[rank], sizingMinimumMiB[rank] + 3, 999999999} {
			t.Run(fmt.Sprintf("%s/reference%d", profile, reference), func(t *testing.T) {
				// Existing helper extracts only pure production tuning functions;
				// no Engine startup, host detection, deployment or runtime runs.
				plan, expected, runtime, _ := preparationPlanProfileFixture(t, rank, reference)
				before := maps.Clone(runtime)
				peers, targets := slices.Clone(plan.PeerAddresses), slices.Clone(plan.TargetAddresses)
				wantSizing, wantApplications, wantWorkers, err := preparationClaimedProfileLimits(expected, runtime)
				if err != nil {
					t.Fatal("existing claimed profile rejected production defaults", err)
				}
				sizing, applications, workers, err := PreparationPlanProfileLimits(plan, runtime)
				if err != nil || sizing != wantSizing || !slices.Equal(applications, wantApplications) || workers != wantWorkers {
					t.Fatal("waiting plan differs from claimed profile", sizing, applications, workers, err)
				}
				if sizing.rank != rank || sizing.referenceMiB != reference || len(applications) != 7 || workers.CPULimitMilli == 0 {
					t.Fatal("waiting plan changed inherited selection")
				}
				if !maps.Equal(runtime, before) || !slices.Equal(plan.PeerAddresses, peers) || !slices.Equal(plan.TargetAddresses, targets) {
					t.Fatal("waiting plan changed caller-owned configuration")
				}
				applications[0].MemoryLimitBytes++
				againSizing, againApplications, againWorkers, err := PreparationPlanProfileLimits(plan, runtime)
				if err != nil || againSizing != wantSizing || !slices.Equal(againApplications, wantApplications) || againWorkers != wantWorkers {
					t.Fatal("returned application slice escaped into later plans", err)
				}
				// The same plan supports both proposed targets without a future
				// worker claim or target hardware changing inherited limits.
				for _, peer := range plan.PeerAddresses {
					if peer != plan.TargetAddresses[0] {
						plan.TargetAddresses = append(plan.TargetAddresses, peer)
						break
					}
				}
				twoSizing, twoApplications, twoWorkers, err := PreparationPlanProfileLimits(plan, runtime)
				if err != nil || twoSizing != wantSizing || !slices.Equal(twoApplications, wantApplications) || twoWorkers != wantWorkers {
					t.Fatal("second proposed target changed inherited profile", err)
				}
			})
		}
	}
}

func TestPreparationPlanProfileLimitsWorkerOverrides(t *testing.T) {
	for rank := 0; rank < 4; rank++ {
		plan, expected, original, _ := preparationPlanProfileFixture(t, rank, max(sizingMinimumMiB[rank], uint64(8195)))
		for _, override := range []struct {
			cpu, memory, pids string
			want              PreparationWorkerLimits
		}{
			{"250m", "384m", "128", PreparationWorkerLimits{250, 384 << 20, 128}},
			{"0.250", "384Mi", "128", PreparationWorkerLimits{250, 384 << 20, 128}},
			{"1.125", "1536m", "192", PreparationWorkerLimits{1125, 1536 << 20, 192}},
		} {
			t.Run(fmt.Sprintf("rank%d/%s", rank, override.cpu), func(t *testing.T) {
				runtime := maps.Clone(original)
				runtime["BOREALIS_SITE_WORKER_CPU_LIMIT"] = override.cpu
				runtime["BOREALIS_SITE_WORKER_MEMORY_LIMIT"] = override.memory
				runtime["BOREALIS_SITE_WORKER_PIDS_LIMIT"] = override.pids
				wantSizing, wantApplications, wantWorkers, err := preparationClaimedProfileLimits(expected, runtime)
				if err != nil || wantWorkers != override.want {
					t.Fatal("existing claimed worker override changed", wantWorkers, err)
				}
				sizing, applications, workers, err := PreparationPlanProfileLimits(plan, runtime)
				if err != nil || sizing != wantSizing || !slices.Equal(applications, wantApplications) || workers != override.want {
					t.Fatal("waiting plan discarded supported effective worker override", workers, err)
				}
			})
		}
	}
}

func TestPreparationPlanProfileLimitsRejectsUnsupportedSettings(t *testing.T) {
	plan, expected, original, keys := preparationPlanProfileFixture(t, 1, 24583)
	assertRejected := func(t *testing.T, runtime map[string]string) {
		t.Helper()
		if _, _, _, err := preparationClaimedProfileLimits(expected, runtime); err == nil {
			t.Fatal("invalid fixture no longer rejected by claimed profile")
		}
		sizing, applications, workers, err := PreparationPlanProfileLimits(plan, runtime)
		if !errors.Is(err, ErrPreparationConfig) || sizing != (PreparationSizing{}) || applications != nil || workers != (PreparationWorkerLimits{}) {
			t.Fatal("invalid waiting plan returned partial profile limits", sizing, applications, workers, err)
		}
	}
	for _, key := range keys {
		t.Run("missing/"+key, func(t *testing.T) {
			runtime := maps.Clone(original)
			delete(runtime, key)
			assertRejected(t, runtime)
		})
	}
	for name, setting := range map[string][2]string{
		"application CPU precision": {"BOREALIS_API_BACKEND_CPU_LIMIT", "2.000001"},
		"application memory":        {"BOREALIS_API_BACKEND_MEMORY_LIMIT", "1025Mi"},
		"unknown rank":              {"BOREALIS_CLUSTER_SIZING_RANK", "4"},
		"missing reference":         {"BOREALIS_CLUSTER_SIZING_MEMORY_MIB", "0"},
		"scheduled task slots":      {"BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY", "9"},
		"fractional millicores":     {"BOREALIS_SITE_WORKER_CPU_LIMIT", "0.1m"},
		"worker CPU precision":      {"BOREALIS_SITE_WORKER_CPU_LIMIT", "1.0001"},
		"worker memory overflow":    {"BOREALIS_SITE_WORKER_MEMORY_LIMIT", "8192Ti"},
		"worker PIDs":               {"BOREALIS_SITE_WORKER_PIDS_LIMIT", "0"},
		"unsupported runtime key":   {"BOREALIS_REPO_ROOT", "/tmp/foreign"},
	} {
		t.Run(name, func(t *testing.T) {
			runtime := maps.Clone(original)
			runtime[setting[0]] = setting[1]
			assertRejected(t, runtime)
		})
	}
}

func TestPreparationPlanProfileLimitsRejectsUnboundPlan(t *testing.T) {
	plan, _, runtime, _ := preparationPlanProfileFixture(t, 1, 24583)
	for name, mutate := range map[string]func(*PreparationPlan){
		"missing source":  func(p *PreparationPlan) { p.Source.SourceSHA = "" },
		"missing plan":    func(p *PreparationPlan) { p.PlanSHA256 = "" },
		"missing targets": func(p *PreparationPlan) { p.TargetAddresses = nil },
		"unsupported K3s": func(p *PreparationPlan) { p.K3sVersion = "v1.36.3+k3s2" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := plan
			mutate(&invalid)
			sizing, applications, workers, err := PreparationPlanProfileLimits(invalid, runtime)
			if !errors.Is(err, ErrPreparationConfig) || sizing != (PreparationSizing{}) || applications != nil || workers != (PreparationWorkerLimits{}) {
				t.Fatal("unbound waiting plan produced limits", err)
			}
		})
	}
}
