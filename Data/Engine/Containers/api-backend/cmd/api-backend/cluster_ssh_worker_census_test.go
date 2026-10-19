package main

import (
	"context"
	"encoding/base64"
	"slices"
	"testing"
)

func sshWorkerPolicyFixture(f *sshStorageFixture) {
	c := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"job-scheduler"], "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
	c["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": clusterSSHWorkerSecretName}}}
	c["env"] = []any{}
	s := sshStorageObject("v1", "Secret", "borealis", clusterSSHWorkerSecretName)
	clusterSSHStorageMap(s, "metadata")["labels"] = map[string]any{"app.kubernetes.io/name": "job-scheduler", "app.kubernetes.io/part-of": "borealis", "app.kubernetes.io/managed-by": "Engine.sh"}
	s["type"], s["data"] = "Opaque", map[string]any{}
	f.objects[clusterSSHWorkerSecretPath] = s
}

func TestClusterSSHWorkerPolicy(t *testing.T) {
	t.Setenv("BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "large")
	t.Setenv("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", "3600")
	for _, mode := range []string{"defaults", "small", "large", "literal precedence", "window minimum", "window maximum", "unknown profile", "malformed window", "invalid base64", "indirect override", "duplicate env", "unknown secret", "optional secret", "extra source", "prefix", "foreign secret", "missing secret", "command override"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			secret := f.objects[clusterSSHWorkerSecretPath]
			data := clusterSSHStorageMap(secret, "data")
			c := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"job-scheduler"], "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
			ref := c["envFrom"].([]any)[0].(map[string]any)
			set := func(key, value string) { data[key] = base64.StdEncoding.EncodeToString([]byte(value)) }
			want := clusterSSHWorkerPolicy{Profile: "standard", OnlineWindow: 300}
			switch mode {
			case "small", "large":
				set("BOREALIS_SITE_WORKER_RESOURCE_PROFILE", mode)
				want.Profile = mode
			case "literal precedence":
				set("BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "large")
				c["env"] = []any{map[string]any{"name": "BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "value": "small"}, map[string]any{"name": "BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", "value": "600"}}
				want = clusterSSHWorkerPolicy{Profile: "small", OnlineWindow: 600}
			case "window minimum":
				set("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", "1")
				want.OnlineWindow = 60
			case "window maximum":
				set("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", "9999")
				want.OnlineWindow = 3600
			case "unknown profile":
				set("BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "unknown")
			case "malformed window":
				set("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", "not-a-number")
			case "invalid base64":
				data["BOREALIS_SITE_WORKER_RESOURCE_PROFILE"] = "!"
			case "indirect override":
				c["env"] = []any{map[string]any{"name": "BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "valueFrom": map[string]any{}}}
			case "duplicate env":
				c["env"] = []any{map[string]any{"name": "BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "value": "small"}, map[string]any{"name": "BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "value": "large"}}
			case "unknown secret":
				clusterSSHStorageMap(ref, "secretRef")["name"] = "other"
			case "optional secret":
				clusterSSHStorageMap(ref, "secretRef")["optional"] = true
			case "extra source":
				c["envFrom"] = append(c["envFrom"].([]any), ref)
			case "prefix":
				ref["prefix"] = "EXTRA_"
			case "foreign secret":
				clusterSSHStorageMap(clusterSSHStorageMap(secret, "metadata"), "labels")["app.kubernetes.io/managed-by"] = "other"
			case "missing secret":
				delete(f.objects, clusterSSHWorkerSecretPath)
			case "command override":
				c["command"] = []any{"other"}
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"defaults", "small", "large", "literal precedence", "window minimum", "window maximum"}, mode)
			if (err == nil) != valid || (valid && value.Requirements.WorkerPolicy != want) {
				t.Fatal("unsafe scheduler inputs", value.Requirements.WorkerPolicy, err)
			}
		})
	}
}

func TestClusterSSHDesiredWorkerWorkloadBudget(t *testing.T) {
	for _, mode := range []string{"empty", "missing worker", "observed smaller", "observed larger", "two sites", "duplicate workers", "missing census", "duplicate sites", "reordered sites", "zero site", "negative site", "oversized site", "too many sites", "missing policy", "unknown profile", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			_, _, snapshot := sshBrokerFixture(t)
			snapshot.DesiredWorkerSites = []int64{}
			wantCPU, wantMemory := uint64(2365), uint64(5<<30+1408<<20)
			// One kube-vip envelope per target, independent of worker demand.
			wantCPU += 25
			wantMemory += 128 << 20
			wantCPU += 155
			wantMemory += 216 << 20
			wantCPU += 45
			wantMemory += 120 << 20
			// Driver: two rollout slots, each max(main, sequential wait-init).
			wantCPU += 50
			wantMemory += 112 << 20
			// Attacher: three replicas plus one declared surge slot.
			wantCPU += 120
			wantMemory += 288 << 20
			// Provisioner: three replicas plus one default surge slot.
			wantCPU += 160
			wantMemory += 384 << 20
			// Resizer: three replicas plus one default surge slot.
			wantCPU += 200
			wantMemory += 480 << 20
			// CSI snapshotter: three replicas plus one default surge slot.
			wantCPU += 240
			wantMemory += 576 << 20
			// Three persistent CSI node-plugin containers, once per target.
			wantCPU += 95
			wantMemory += 168 << 20
			switch mode {
			case "missing worker":
				snapshot.DesiredWorkerSites = []int64{2}
				wantCPU += 50
				wantMemory += 512 << 20
			case "observed smaller":
				snapshot.DesiredWorkerSites = []int64{1}
				wantCPU += 25
				wantMemory += 256 << 20
			case "observed larger":
				snapshot.DesiredWorkerSites = []int64{1}
				snapshot.Storage.Requirements.SiteWorkers[0].Resources.MemoryLimitBytes = 1 << 30
				wantCPU += 25
				wantMemory += 768 << 20
			case "two sites":
				snapshot.DesiredWorkerSites = []int64{1, 2}
				wantCPU += 75
				wantMemory += 768 << 20
			case "duplicate workers":
				snapshot.DesiredWorkerSites = []int64{1}
				worker := snapshot.Storage.Requirements.SiteWorkers[0]
				worker.Name, worker.Worker, worker.UID = "site-worker-site-2", "worker-2", newClusterUUID()
				snapshot.Storage.Requirements.SiteWorkers = append(snapshot.Storage.Requirements.SiteWorkers, worker)
				wantCPU += 25
				wantMemory += 256 << 20
			case "missing census":
				snapshot.DesiredWorkerSites = nil
			case "duplicate sites":
				snapshot.DesiredWorkerSites = []int64{1, 1}
			case "reordered sites":
				snapshot.DesiredWorkerSites = []int64{2, 1}
			case "zero site":
				snapshot.DesiredWorkerSites = []int64{0}
			case "negative site":
				snapshot.DesiredWorkerSites = []int64{-1}
			case "oversized site":
				snapshot.DesiredWorkerSites = []int64{int64(clusterSSHCapacityLimit + 1)}
			case "too many sites":
				for i := 1; i <= clusterSSHSiteWorkerLimit+1; i++ {
					snapshot.DesiredWorkerSites = append(snapshot.DesiredWorkerSites, int64(i))
				}
			case "missing policy":
				snapshot.Storage.Requirements.WorkerPolicy = clusterSSHWorkerPolicy{}
			case "unknown profile":
				snapshot.Storage.Requirements.WorkerPolicy.Profile = "other"
			case "overflow":
				snapshot.Storage.Requirements.PostgresResources.MemoryLimitBytes = clusterSSHCapacityLimit - (1<<30 + 256<<20)
				snapshot.DesiredWorkerSites = []int64{2}
			}
			cpu, memory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			valid := slices.Contains([]string{"empty", "missing worker", "observed smaller", "observed larger", "two sites", "duplicate workers"}, mode)
			if ok != valid || (ok && (cpu != wantCPU || memory != wantMemory)) {
				t.Fatal("incorrect desired worker budget", cpu, memory, ok, wantCPU, wantMemory)
			}
		})
	}
}

func TestClusterSSHWorkerPolicyTemplateDrift(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	reads := 0
	get := func(ctx context.Context, path string, out any) error {
		if path == clusterSSHApplicationDeploymentPrefix+"job-scheduler" {
			reads++
			if reads == 2 {
				clusterSSHStorageMap(f.objects[path], "metadata")["resourceVersion"] = "changed"
			}
		}
		return f.get(ctx, path, out)
	}
	if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, get); err == nil || reads != 2 {
		t.Fatal("scheduler policy overwrote application observation", err, reads)
	}
}
