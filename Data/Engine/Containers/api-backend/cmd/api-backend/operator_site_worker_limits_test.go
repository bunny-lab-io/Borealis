package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func workerLimitsFixture() borealisOperatorSiteWorkerLimits {
	return borealisOperatorSiteWorkerLimits{CPU: "1000m", Memory: "512Mi", PIDs: "128"}
}

func setOperatorWorkerLimitsFixture(t *testing.T, limits borealisOperatorSiteWorkerLimits) {
	t.Helper()
	t.Setenv("BOREALIS_SITE_WORKER_CPU_LIMIT", limits.CPU)
	t.Setenv("BOREALIS_SITE_WORKER_MEMORY_LIMIT", limits.Memory)
	t.Setenv("BOREALIS_SITE_WORKER_PIDS_LIMIT", limits.PIDs)
}

// Run only library-mode profile/manifest functions against private temporary
// configuration. No installed Engine, Kubernetes, package or service commands.
func renderOperatorWorkerLimits(t *testing.T, rank, raw string) (map[string]any, error, string) {
	t.Helper()
	root, err := filepath.Abs("../../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "limits.env"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", `set -euo pipefail
export BOREALIS_ENGINE_LIBRARY_MODE=1 BOREALIS_ENGINE_RUNTIME_OWNER_UID=64646 BOREALIS_ENGINE_RUNTIME_OWNER_GID=64646
export BOREALIS_ENGINE_HOST_ROOT="$2" BOREALIS_ENGINE_RUNTIME_ROOT="$2/Engine"
source "$1/Engine.sh"
COMPOSE_ENV="$2/limits.env"
if [[ "$3" != raw ]]; then
  load_profile_tuning 64 999999999 "$3" 999999999
  printf 'BOREALIS_SITE_WORKER_CPU_LIMIT=%s\nBOREALIS_SITE_WORKER_MEMORY_LIMIT=%s\nBOREALIS_SITE_WORKER_PIDS_LIMIT=%s\n' "$PROFILE_SITE_WORKER_CPU_LIMIT" "$PROFILE_SITE_WORKER_MEMORY_LIMIT" "$PROFILE_SITE_WORKER_PIDS_LIMIT" >"$COMPOSE_ENV"
fi
render_borealis_operator_manifest borealis-engine/api-backend:sha-cccccccccccc fixture fixture borealis-engine/api-backend:sha-cccccccccccc borealis-engine/site-worker:sha-cccccccccccc fixture
`, "worker-limits-fixture", root, directory, rank)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "PYTHONDONTWRITEBYTECODE=1"}
	out, err := command.CombinedOutput()
	if err != nil {
		return nil, err, string(out)
	}
	decode := exec.CommandContext(ctx, "python3", "-c", `import json,sys,yaml
objects=[obj for obj in yaml.safe_load_all(sys.stdin) if obj and obj.get('kind')=='Deployment']
assert len(objects)==1 and objects[0]['metadata']['name']=='borealis-operator'
print(json.dumps(objects[0]))`)
	decode.Env, decode.Stdin = command.Env, strings.NewReader(string(out))
	decoded, err := decode.CombinedOutput()
	var deployment map[string]any
	if err != nil || json.Unmarshal(decoded, &deployment) != nil {
		t.Fatalf("decode actual operator manifest: %v: %s", err, decoded)
	}
	meta := clusterSSHStorageMap(deployment, "metadata")
	meta["uid"], meta["resourceVersion"] = newClusterUUID(), "1"
	return deployment, nil, string(out)
}

func TestOperatorSiteWorkerEffectiveLimitProducerParity(t *testing.T) {
	for _, test := range []struct {
		name, rank, raw string
		limits          borealisOperatorSiteWorkerLimits
	}{
		{"homelab", "0", "", borealisOperatorSiteWorkerLimits{"1000m", "256Mi", "128"}},
		{"small-business", "1", "", borealisOperatorSiteWorkerLimits{"1000m", "384Mi", "128"}},
		{"msp", "2", "", borealisOperatorSiteWorkerLimits{"1500m", "512Mi", "128"}},
		{"enterprise", "3", "", borealisOperatorSiteWorkerLimits{"2000m", "512Mi", "128"}},
		{"millicore override", "raw", "BOREALIS_SITE_WORKER_CPU_LIMIT=250m\nBOREALIS_SITE_WORKER_MEMORY_LIMIT=384m\nBOREALIS_SITE_WORKER_PIDS_LIMIT=128\n", borealisOperatorSiteWorkerLimits{"250m", "384Mi", "128"}},
		{"equivalent decimal override", "raw", "BOREALIS_SITE_WORKER_CPU_LIMIT=0.250\nBOREALIS_SITE_WORKER_MEMORY_LIMIT=384m\nBOREALIS_SITE_WORKER_PIDS_LIMIT=128\n", borealisOperatorSiteWorkerLimits{"250m", "384Mi", "128"}},
		{"effective override", "raw", "BOREALIS_SITE_WORKER_CPU_LIMIT=1.125\nBOREALIS_SITE_WORKER_MEMORY_LIMIT=1536m\nBOREALIS_SITE_WORKER_PIDS_LIMIT=192\n", borealisOperatorSiteWorkerLimits{"1125m", "1536Mi", "192"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			deployment, err, output := renderOperatorWorkerLimits(t, test.rank, test.raw)
			if err != nil {
				t.Fatalf("render: %v: %s", err, output)
			}
			f := newSSHStorageFixture(t, false)
			f.objects[clusterSSHApplicationDeploymentPrefix+"borealis-operator"] = deployment
			// Observer must use original Deployment, never this process's env.
			setOperatorWorkerLimitsFixture(t, borealisOperatorSiteWorkerLimits{"9000m", "9Gi", "999"})
			observed, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != nil || observed.Requirements.WorkerPolicy.Limits != test.limits {
				t.Fatal("original source limit projection", observed.Requirements.WorkerPolicy, err)
			}
			setOperatorWorkerLimitsFixture(t, test.limits)
			t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
			var pod map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
					return
				}
				if r.Method != http.MethodPost {
					t.Error("unexpected operator mutation", r.Method)
					w.WriteHeader(500)
					return
				}
				var object map[string]any
				if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
					t.Error(err)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/pods") {
					pod = object
				}
				writeJSON(w, http.StatusCreated, object)
			}))
			defer server.Close()
			_, status, err := borealisOperatorTestClient(server).launchSiteWorker(context.Background(), borealisOperatorLaunchSiteWorkerRequest{
				SiteID: 7, SiteName: "Resource parity", WorkerGUID: schedulerK3sSiteWorkerGUID(7), ImageRef: "borealis-engine/site-worker:sha-cccccccccccc", ResourceProfile: "standard", RemoteOpsPort: 56001, RemoteDesktopPort: 61001,
			})
			if err != nil || status != http.StatusAccepted || pod == nil {
				t.Fatal("actual launch rejected inherited envelope", status, err)
			}
			want := map[string]any{"requests": map[string]any{"cpu": "50m", "memory": "128Mi"}, "limits": map[string]any{"cpu": test.limits.CPU, "memory": test.limits.Memory}}
			for _, kind := range []string{"initContainers", "containers"} {
				container := clusterSSHStorageMap(pod, "spec")[kind].([]any)[0].(map[string]any)
				if !reflect.DeepEqual(container["resources"], want) {
					t.Fatal("actual Pod differs from effective settings", kind, container["resources"], want)
				}
			}
			// Only API identity/status is synthetic; keep actual HTTP-produced spec.
			node := f.a.Source.Members[0].Name
			clusterSSHStorageMap(pod, "metadata")["uid"] = newClusterUUID()
			clusterSSHStorageMap(pod, "metadata")["resourceVersion"] = "1"
			clusterSSHStorageMap(pod, "spec")["nodeName"] = node
			clusterSSHStorageMap(pod, "spec")["schedulerName"] = "default-scheduler"
			pod["status"] = sshSiteWorkerPodFixture(t, node, 7)["status"]
			for _, kind := range []string{"containerStatuses", "initContainerStatuses"} {
				clusterSSHStorageMap(pod, "status")[kind].([]any)[0].(map[string]any)["resources"] = want
			}
			f.objects[clusterSSHSiteWorkerPodsPath(node)]["items"] = []any{pod}
			observed, err = observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			resources, valid := observed.Requirements.WorkerPolicy.resources()
			if err != nil || !valid || len(observed.Requirements.SiteWorkers) != 1 || observed.Requirements.SiteWorkers[0].Resources != resources {
				t.Fatal("native census disagrees with actual Pod", err)
			}
			candidate := sshWorkerRotationCandidate(t, pod)
			for _, kind := range []string{"initContainers", "containers"} {
				if !reflect.DeepEqual(clusterSSHStorageMap(candidate, "spec")[kind].([]any)[0].(map[string]any)["resources"], want) {
					t.Fatal("rotation changed inherited resources")
				}
			}
			_, _, snapshot := sshBrokerFixture(t)
			snapshot.Storage.Requirements = observed.Requirements
			snapshot.DesiredWorkerSites = []int64{}
			snapshot.Storage.Requirements.SiteWorkers = []clusterSSHSiteWorkerResource{}
			// Isolate application update overlap before adding any resident,
			// prospective worker or worker candidate demand below.
			baseCPU, baseMemory, valid := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			snapshot.DesiredWorkerSites = []int64{7}
			snapshot.Storage.Requirements.SiteWorkers = observed.Requirements.SiteWorkers
			cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			prospective, safe := observed.Requirements.WorkerPolicy.recreationResources()
			if !valid || !ok || !safe || cpu != baseCPU+2*max(resources.CPURequestMilli, prospective.CPURequestMilli) || memory != baseMemory+2*max(resources.MemoryLimitBytes, prospective.MemoryLimitBytes) {
				t.Fatal("original plus candidate budget changed", cpu, memory, ok)
			}
		})
	}
}

func TestOperatorSiteWorkerEffectiveLimitRejectsUncertainInputs(t *testing.T) {
	base := "BOREALIS_SITE_WORKER_CPU_LIMIT=1.00\nBOREALIS_SITE_WORKER_MEMORY_LIMIT=384m\nBOREALIS_SITE_WORKER_PIDS_LIMIT=128\n"
	for _, test := range []struct{ key, value string }{
		{"CPU", ""}, {"MEMORY", ""}, {"PIDS", ""}, {"CPU", "0"}, {"CPU", "1.0001"}, {"CPU", "0.1m"}, {"CPU", "250m "}, {"CPU", " 1.00"}, {"CPU", "1000000"},
		{"MEMORY", "0m"}, {"MEMORY", "384 m"}, {"MEMORY", "999999999Ti"}, {"MEMORY", "1e9"}, {"PIDS", "0"}, {"PIDS", "128 "}, {"PIDS", "-1"},
	} {
		t.Run(fmt.Sprintf("%s=%q", test.key, test.value), func(t *testing.T) {
			lines := strings.Split(base, "\n")
			prefix := "BOREALIS_SITE_WORKER_" + test.key + "_LIMIT="
			for i, line := range lines {
				if strings.HasPrefix(line, prefix) {
					lines[i] = prefix + test.value
					if test.value == "" {
						lines[i] = ""
					}
				}
			}
			_, err, output := renderOperatorWorkerLimits(t, "raw", strings.Join(lines, "\n"))
			if err == nil || strings.Contains(output, "apiVersion:") {
				t.Fatal("invalid limits emitted manifest", err, output)
			}
		})
	}
	t.Run("duplicate", func(t *testing.T) {
		_, err, out := renderOperatorWorkerLimits(t, "raw", base+"BOREALIS_SITE_WORKER_MEMORY_LIMIT=512m\n")
		if err == nil || strings.Contains(out, "apiVersion:") {
			t.Fatal("duplicate effective setting accepted")
		}
	})
	for _, limits := range []borealisOperatorSiteWorkerLimits{{}, {"0m", "384Mi", "128"}, {"1m", "384Mi", "128"}, {"1000m", "1Mi", "128"}, {"1000m", "384Mi", ""}, {"1000m", "384Mi", "128 "}, {"1000m", "384Mi", "0"}, {"1000m", "384m", "128"}} {
		t.Run(fmt.Sprint(limits), func(t *testing.T) {
			setOperatorWorkerLimitsFixture(t, limits)
			t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid limits reached Kubernetes")
				w.WriteHeader(500)
			}))
			defer server.Close()
			_, status, err := borealisOperatorTestClient(server).launchSiteWorker(context.Background(), borealisOperatorLaunchSiteWorkerRequest{SiteID: 7, SiteName: "Limits", WorkerGUID: schedulerK3sSiteWorkerGUID(7), ImageRef: "borealis-engine/site-worker:sha-cccccccccccc", ResourceProfile: "standard", RemoteOpsPort: 56001, RemoteDesktopPort: 61001})
			if err == nil || status != http.StatusBadRequest {
				t.Fatal("uncertain deployment limits accepted", status, err)
			}
		})
	}
}
