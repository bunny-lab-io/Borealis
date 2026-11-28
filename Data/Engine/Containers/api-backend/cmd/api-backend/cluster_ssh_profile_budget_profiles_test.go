package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Real pure renderers supply both effective settings and independently observed
// resources. Large fixture hardware only avoids target-fit rejection here; it
// establishes no hardware recommendation, reserve or total-capacity proof.
func sshRenderedProfileContract(t *testing.T, profile string, rank int, reference string) (map[string]string, []clusterSSHApplicationResource, clusterSSHApplicationResource, borealisOperatorSiteWorkerLimits) {
	t.Helper()
	const root = "../../../../../.."
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	render := exec.CommandContext(ctx, "bash", filepath.Join(root, "Tests/render-k3s-manifests.sh"), directory, profile, "64", "999999999", reference)
	render.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "PYTHONDONTWRITEBYTECODE=1"}
	if out, err := render.CombinedOutput(); err != nil {
		t.Fatalf("production profile rendering: %v: %s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "profile.json"))
	var selected struct {
		Rank      int `json:"rank"`
		Reference int `json:"reference_memory_mib"`
		Slots     int `json:"scheduled_slots"`
	}
	if err != nil || json.Unmarshal(raw, &selected) != nil || selected.Rank != rank || strconv.Itoa(selected.Reference) != reference || selected.Slots != [4]int{5, 8, 12, 16}[rank] {
		t.Fatal("stronger joining host changed inherited selection", err)
	}
	raw, err = os.ReadFile(filepath.Join(directory, "application-limits.env"))
	if err != nil {
		t.Fatal(err)
	}
	settings := sshPreparationRuntimeFixture()
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 20 {
		t.Fatal("effective service setting inventory changed")
	}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatal("invalid effective service setting")
		}
		settings[key] = value
	}
	rendered := sshDecodeRenderedApplicationBudget(t, ctx, root, directory, render.Env)
	if len(rendered.Applications) != 8 || len(rendered.CNPG) != 2 {
		t.Fatal("production resource inventory changed")
	}
	for _, object := range rendered.Applications {
		metadata := clusterSSHStorageMap(object, "metadata")
		metadata["uid"], metadata["resourceVersion"] = newClusterUUID(), "1"
	}
	applications, err := observeClusterSSHApplicationResources(func(path string) (map[string]any, error) {
		return rendered.Applications[filepath.Base(path)], nil
	})
	if err != nil {
		t.Fatal("production application resources rejected", err)
	}
	postgres, valid := clusterSSHWorkloadResources("postgres", rendered.Postgres)
	if !valid {
		t.Fatal("production PostgreSQL resources rejected")
	}
	for _, resources := range rendered.CNPG {
		cnpg, valid := clusterSSHWorkloadResources("postgres", resources)
		if !valid || cnpg != postgres {
			t.Fatal("CNPG changed inherited resource envelope")
		}
	}
	limits, err := observeClusterSSHWorkerLimits(func(path string) (map[string]any, error) { return rendered.Applications[filepath.Base(path)], nil })
	if err != nil {
		t.Fatal("production worker limits rejected", err)
	}
	return settings, applications, postgres, limits
}

func TestClusterSSHProfileApplicationBudgets(t *testing.T) {
	for rank, profile := range []string{"homelab", "small-business", "msp", "enterprise"} {
		references := [4][]string{
			{"8192", "16383", "16384", "16387", "999999999"},
			{"16384", "24583", "32767", "32768", "999999999"},
			{"32768", "49159", "65535", "65536", "999999999"},
			{"65536", "81927", "98303", "98304", "999999999"},
		}
		for _, reference := range references[rank] {
			t.Run(profile+"/"+reference, func(t *testing.T) {
				settings, observed, postgres, _ := sshRenderedProfileContract(t, profile, rank, reference)
				cohort, source, lease, baseline := sshPreparationFixture(t)
				expected, err := buildClusterSSHPreparationExpected(cohort, source, lease, baseline, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16")
				if err != nil {
					t.Fatal(err)
				}
				configuration, err := clusterbootstrap.NewPreparationConfiguration(expected, settings)
				if err != nil {
					t.Fatal(err)
				}
				applications, instance, err := clusterSSHProfileApplications(configuration)
				if err != nil || !slices.Equal(applications, observed) || instance != postgres {
					t.Fatalf("production profile budget drift: %v: got %+v / %+v; want %+v / %+v", err, applications, instance, observed, postgres)
				}
				cpu, memory, valid := clusterSSHApplicationBudget(applications)
				if !valid || cpu != 325 || memory != [4]uint64{2080, 3744, 6816, 11680}[rank]<<20 || cpu+instance.CPURequestMilli != 425 {
					t.Fatal("declared application subtotal changed", cpu, memory)
				}
				if (configuration.SmallBusinessApplicationLimits() == nil) != (rank == 1 && reference == "16384") {
					t.Fatal("legacy Small Business contract widened")
				}
			})
		}
	}
}

func TestClusterSSHTargetProfileSizingAllProfiles(t *testing.T) {
	for rank, profile := range []string{"homelab", "small-business", "msp", "enterprise"} {
		// Non-default reference values cover integer MiB calculation in each
		// native expansion/replacement scope, not only at the pure helper.
		reference := [4]string{"12295", "24583", "49159", "81927"}[rank]
		settings, applications, postgres, workerLimits := sshRenderedProfileContract(t, profile, rank, reference)
		for _, mode := range []string{"expansion", "replacement", "missing cap", "application mismatch", "postgres mismatch", "application drift after", "postgres drift after", "same-budget reference drift", "claim lost", "sibling capacity"} {
			t.Run(profile+"/"+mode, func(t *testing.T) {
				f := newSSHNetworkTargetsFixture(t, mode == "replacement", "peer")
				snapshot := clusterSSHPreparationSnapshot{Settings: maps.Clone(settings), Storage: sshStorageSnapshotFixture(t, f.a), DesiredWorkerSites: []int64{}}
				snapshot.Storage.Requirements.WorkerPolicy.Limits = workerLimits
				snapshot.Storage.Requirements.Applications = slices.Clone(applications)
				snapshot.Storage.Requirements.PostgresResources = postgres
				consumed, reads := false, 0
				f.hostWire = func(i int, wire string) string {
					wire = strings.Replace(wire, "cpu_count=16", "cpu_count=64", 1)
					memory := "268435456"
					if mode == "sibling capacity" && i == 1 {
						memory = "1048576"
					}
					return strings.Replace(wire, "memory_kib=33554432", "memory_kib="+memory, 1)
				}
				source := func(context.Context) (clusterSSHPreparationSnapshot, error) {
					reads++
					expected, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
					snapshot.Expected = expected
					switch {
					case mode == "missing cap":
						delete(snapshot.Settings, "BOREALIS_API_BACKEND_MEMORY_LIMIT")
					case mode == "application mismatch", mode == "application drift after" && consumed:
						snapshot.Storage.Requirements.Applications[0].MemoryLimitBytes++
					case mode == "postgres mismatch", mode == "postgres drift after" && consumed:
						snapshot.Storage.Requirements.PostgresResources.MemoryLimitBytes++
					case mode == "same-budget reference drift" && reads == 2:
						value, _ := strconv.Atoi(reference)
						// These references end one MiB before the next integer25%
						// increment; subtracting one preserves all service caps.
						snapshot.Settings["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = strconv.Itoa(value - 1)
					}
					return snapshot, err
				}
				err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
					return withClusterSSHTargetProfileSizing(ctx, readers, source, func(context.Context, []clusterSSHTargetHostEvidence) error {
						consumed = true
						if mode == "claim lost" {
							f.lost.Store(1)
						}
						return nil
					})
				})
				valid := mode == "expansion" || mode == "replacement"
				if (err == nil) != valid || (valid && !consumed) {
					t.Fatal("wrong profile-sizing outcome", consumed, err)
				}
				late := strings.HasSuffix(mode, "after") || mode == "claim lost"
				if !valid && consumed != late {
					t.Fatal("wrong profile rejection boundary", consumed)
				}
			})
		}
	}
}
