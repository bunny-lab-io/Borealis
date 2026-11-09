package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Execute only the real candidate-name/manifest functions. Kubernetes is a
// single fixture GET; Engine startup, deployment and installed files are absent.
func sshWorkerRotationCandidate(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../../../../../Engine.sh")
	if err != nil {
		t.Fatal(err)
	}
	start, end := "agent_redeploy_candidate_name() {\n", "agent_redeploy_scheduler_deployments() {\n"
	if strings.Count(string(raw), start) != 1 || strings.Count(string(raw), end) != 1 {
		t.Fatal("candidate renderer missing or ambiguous")
	}
	_, body, _ := strings.Cut(string(raw), start)
	body, _, found := strings.Cut(body, end)
	if !found || strings.Count(body, "agent_redeploy_render_candidate() {\n") != 1 {
		t.Fatal("candidate renderer boundary missing")
	}
	directory := t.TempDir()
	input, err := json.Marshal(source)
	if err != nil || os.WriteFile(filepath.Join(directory, "source.json"), input, 0600) != nil {
		t.Fatal("fixture source unavailable", err)
	}
	script := "set -euo pipefail\n" + start + body + `
DEPLOY_DIR="$1"
BUILD_LOG="$1/build.log"
K3S_NAMESPACE=borealis
source_name="$2"
target_image=borealis-engine/site-worker:sha-dddddddddddd
k3s_kubectl() {
  [[ "$#" == 6 && "$1" == -n && "$2" == borealis && "$3" == get && "$4" == "pod/$source_name" && "$5" == -o && "$6" == json ]] || return 99
  cat "$DEPLOY_DIR/source.json"
}
candidate="$(agent_redeploy_candidate_name "$source_name" "$target_image")"
agent_redeploy_render_candidate "$source_name" "$candidate" agent-fixture "$target_image" artifact-fixture build-fixture 2026-11-01T00:00:00Z
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", script, "worker-rotation-fixture", directory, clusterSSHStorageText(clusterSSHStorageMap(source, "metadata"), "name"))
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "PYTHONDONTWRITEBYTECODE=1"}
	output, err := command.CombinedOutput()
	if err != nil || ctx.Err() != nil {
		t.Fatalf("isolated candidate render failed: %v: %s", err, output)
	}
	var candidate map[string]any
	if json.Unmarshal(output, &candidate) != nil {
		t.Fatal("invalid rendered candidate")
	}
	return candidate
}

func TestClusterSSHWorkerRotationProducerParity(t *testing.T) {
	for _, profileName := range []string{"small", "standard", "large"} {
		t.Run(profileName, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			member := f.a.Source.Members[0]
			original := sshSiteWorkerPodFixture(t, member.Name, 1)
			profile, _ := borealisOperatorSiteWorkerResourceProfile(profileName)
			resources := map[string]any{
				"requests": map[string]any{"cpu": profile.RequestCPU, "memory": profile.RequestMemory},
				"limits":   map[string]any{"cpu": profile.LimitCPU, "memory": profile.LimitMemory},
			}
			for _, pair := range [][2]string{{"spec", "containers"}, {"spec", "initContainers"}, {"status", "containerStatuses"}, {"status", "initContainerStatuses"}} {
				clusterSSHStorageMap(original, pair[0])[pair[1]].([]any)[0].(map[string]any)["resources"] = resources
			}
			candidate := sshWorkerRotationCandidate(t, original)
			for _, containers := range []string{"containers", "initContainers"} {
				got := clusterSSHStorageMap(candidate, "spec")[containers].([]any)[0].(map[string]any)["resources"]
				if !reflect.DeepEqual(got, resources) {
					t.Fatal("actual candidate changed worker resource envelope", containers)
				}
			}
			meta := clusterSSHStorageMap(candidate, "metadata")
			if clusterSSHStorageMap(meta, "labels")["borealis.io/redeploy-agent-candidate"] != "true" ||
				clusterSSHStorageMap(meta, "annotations")["borealis.io/redeploy-agent-source-pod"] != clusterSSHStorageMap(original, "metadata")["name"] ||
				clusterSSHStorageMap(candidate, "spec")["nodeName"] != nil {
				t.Fatal("candidate producer contract changed")
			}
			observe := func(pods ...map[string]any) ([]clusterSSHSiteWorkerResource, error) {
				return observeClusterSSHSiteWorkerResources(func(path, kind, namespace string, limit int) ([]map[string]any, error) {
					if path != clusterSSHSiteWorkerPodsPath(member.Name) || kind != "Pod" || namespace != "borealis" || limit != clusterSSHSiteWorkerLimit {
						t.Fatal("unexpected source read")
					}
					return pods, nil
				}, f.a.Source)
			}
			workers, err := observe(original)
			if err != nil || len(workers) != 1 {
				t.Fatal("native source rejected producer worker", err)
			}
			_, _, snapshot := sshBrokerFixture(t)
			snapshot.Storage.Requirements.SiteWorkers = []clusterSSHSiteWorkerResource{}
			baseCPU, baseMemory, valid := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			snapshot.Storage.Requirements.SiteWorkers = workers
			cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			if !valid || !ok || cpu != baseCPU+2*workers[0].Resources.CPURequestMilli || memory != baseMemory+2*workers[0].Resources.MemoryLimitBytes {
				t.Fatal("rotation does not retain original plus actual candidate envelope", cpu, memory, ok)
			}
			// Synthetic API identity/status cannot turn a real candidate into a
			// canonical source worker, alone or alongside its original GUID.
			meta["uid"], meta["resourceVersion"] = newClusterUUID(), "2"
			clusterSSHStorageMap(candidate, "spec")["nodeName"] = member.Name
			candidate["status"] = original["status"]
			for _, pods := range [][]map[string]any{{candidate}, {original, candidate}} {
				if _, err := observe(pods...); err == nil {
					t.Fatal("observed candidate reused as another prospective cohort")
				}
			}
			duplicate := workers[0]
			duplicate.Name += "-next-dddddddd"
			duplicate.UID = newClusterUUID()
			snapshot.Storage.Requirements.SiteWorkers = append(workers, duplicate)
			if _, _, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot); ok {
				t.Fatal("duplicate worker identity accepted by budget")
			}
		})
	}
}

func TestClusterSSHWorkerRotationDemandAndOverflow(t *testing.T) {
	for _, mode := range []string{"empty", "missing", "heterogeneous", "surplus same site", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			_, _, snapshot := sshBrokerFixture(t)
			r := &snapshot.Storage.Requirements
			r.SiteWorkers = []clusterSSHSiteWorkerResource{}
			baseCPU, baseMemory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			if !ok {
				t.Fatal("invalid non-worker fixture")
			}
			var wantCPU, wantMemory uint64
			switch mode {
			case "missing":
				snapshot.DesiredWorkerSites = []int64{1, 2}
				wantCPU, wantMemory = 200, 2<<30 // Two standard workers and their candidates.
			case "heterogeneous", "surplus same site":
				first := sshSiteWorkerResourceValue("engine-source")
				second := first
				second.Name, second.Worker, second.UID, second.SiteID = "site-worker-site-2", "worker-2", newClusterUUID(), 2
				second.Resources.CPURequestMilli, second.Resources.CPULimitMilli = 137, 1500
				second.Resources.MemoryLimitBytes = 1 << 30
				r.SiteWorkers = []clusterSSHSiteWorkerResource{first, second}
				if mode == "heterogeneous" {
					snapshot.DesiredWorkerSites = []int64{1, 2, 3}
					wantCPU, wantMemory = 474, 4<<30 // Preserve large worker; fill small/missing independently.
				} else {
					r.SiteWorkers[1].SiteID = 1
					snapshot.DesiredWorkerSites = []int64{1}
				}
			case "CPU overflow", "memory overflow":
				worker := sshSiteWorkerResourceValue("engine-source")
				if mode == "CPU overflow" {
					worker.Resources.CPURequestMilli, worker.Resources.CPULimitMilli = clusterSSHCapacityLimit/2, clusterSSHCapacityLimit/2
				} else {
					worker.Resources.MemoryLimitBytes = clusterSSHCapacityLimit / 2
				}
				r.SiteWorkers = []clusterSSHSiteWorkerResource{worker}
				if _, _, ok := clusterSSHNodeWorkloadBudget(*r); !ok {
					t.Fatal("overflow fixture does not fit before candidate")
				}
			}
			cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			if strings.HasSuffix(mode, "overflow") || mode == "surplus same site" {
				if ok || cpu != 0 || memory != 0 {
					t.Fatal("invalid worker cohort returned usable budget")
				}
			} else if !ok || cpu != baseCPU+wantCPU || memory != baseMemory+wantMemory {
				t.Fatal("wrong complete worker rotation demand", cpu, memory, wantCPU, wantMemory)
			}
		})
	}
}

func TestClusterSSHWorkerRotationTargetFit(t *testing.T) {
	for _, mode := range []string{"exact", "one millicpu", "one byte", "candidate CPU", "candidate memory", "host reserve omitted"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, false, "peer")
			_, _, snapshot := sshBrokerFixture(t)
			var err error
			snapshot.Expected, err = buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "4g"
			cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			if !ok || cpu >= 16000 || memory >= 32<<30 {
				t.Fatal("invalid target baseline")
			}
			// WireGuard is single-copy during declared application updates, so
			// balancing this envelope keeps the test about worker rotation only.
			app := &snapshot.Storage.Requirements.Applications[7]
			app.CPURequestMilli += 16000 - cpu
			app.MemoryLimitBytes += (32 << 30) - 7_500_000_000 - memory
			switch mode {
			case "one millicpu":
				app.CPURequestMilli++
			case "one byte":
				app.MemoryLimitBytes++
			case "candidate CPU":
				app.CPURequestMilli += 25
			case "candidate memory":
				app.MemoryLimitBytes += 256 << 20
			case "host reserve omitted":
				app.MemoryLimitBytes += 7_500_000_000
			}
			app.CPULimitMilli = app.CPURequestMilli
			steadyCPU, steadyMemory, ok := clusterSSHNodeWorkloadBudget(snapshot.Storage.Requirements)
			if !ok || steadyCPU > 16000 || steadyMemory > 32<<30 {
				t.Fatal("test must isolate candidate-only exhaustion")
			}
			consumed, reads := false, 0
			err = runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				return withClusterSSHTargetApplicationSizing(ctx, readers, func(context.Context) (clusterSSHPreparationSnapshot, error) {
					reads++
					return snapshot, nil
				}, func(context.Context, []clusterSSHTargetHostEvidence) error { consumed = true; return nil })
			})
			if (err == nil) != (mode == "exact") || consumed != (mode == "exact") || reads < 2 {
				t.Fatal("candidate workload escaped native target fit", err, consumed, reads)
			}
			f.assertClosed(t)
		})
	}
}
