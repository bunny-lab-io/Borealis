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
	operator := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"borealis-operator"], "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
	operator["args"] = []any{"borealis-operator"}
	operator["env"] = []any{
		map[string]any{"name": "BOREALIS_SITE_WORKER_CPU_LIMIT", "value": "1000m"},
		map[string]any{"name": "BOREALIS_SITE_WORKER_MEMORY_LIMIT", "value": "512Mi"},
		map[string]any{"name": "BOREALIS_SITE_WORKER_PIDS_LIMIT", "value": "128"},
	}
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
			want := clusterSSHWorkerPolicy{Profile: "standard", OnlineWindow: 300, Limits: workerLimitsFixture()}
			switch mode {
			case "small", "large":
				set("BOREALIS_SITE_WORKER_RESOURCE_PROFILE", mode)
				want.Profile = mode
			case "literal precedence":
				set("BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "large")
				c["env"] = []any{map[string]any{"name": "BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "value": "small"}, map[string]any{"name": "BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", "value": "600"}}
				want.Profile, want.OnlineWindow = "small", 600
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

func TestClusterSSHWorkerPolicyRequiresOriginalOperatorLimits(t *testing.T) {
	setOperatorWorkerLimitsFixture(t, workerLimitsFixture())
	for _, mode := range []string{"missing deployment", "foreign identity", "command override", "args override", "envFrom", "missing env", "missing CPU", "missing memory", "missing PIDs", "duplicate env", "indirect memory", "extra field", "invalid CPU", "invalid memory", "invalid PIDs", "insufficient request"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			path := clusterSSHApplicationDeploymentPrefix + "borealis-operator"
			deployment := f.objects[path]
			c := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(deployment, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
			env := c["env"].([]any)
			switch mode {
			case "missing deployment":
				delete(f.objects, path)
			case "foreign identity":
				clusterSSHStorageMap(deployment, "metadata")["namespace"] = "other"
			case "command override":
				c["command"] = []any{"other"}
			case "args override":
				c["args"] = []any{"other"}
			case "envFrom":
				c["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "other"}}}
			case "missing env":
				delete(c, "env")
			case "missing CPU":
				c["env"] = env[1:]
			case "missing memory":
				c["env"] = []any{env[0], env[2]}
			case "missing PIDs":
				c["env"] = env[:2]
			case "duplicate env":
				c["env"] = append(env, env[0])
			case "indirect memory":
				env[1] = map[string]any{"name": "BOREALIS_SITE_WORKER_MEMORY_LIMIT", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.name"}}}
			case "extra field":
				env[1].(map[string]any)["extra"] = "ignored"
			case "invalid CPU":
				env[0].(map[string]any)["value"] = "1.0"
			case "invalid memory":
				env[1].(map[string]any)["value"] = "512m"
			case "invalid PIDs":
				env[2].(map[string]any)["value"] = "0"
			case "insufficient request":
				env[1].(map[string]any)["value"] = "96Mi"
			}
			if got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err == nil || got.Requirements.WorkerPolicy != (clusterSSHWorkerPolicy{}) {
				t.Fatal("local defaults masked invalid original producer", mode, err)
			}
		})
	}
}

func TestClusterSSHWorkerRecreationPreservesUnprovenProducerBound(t *testing.T) {
	for _, profile := range []string{"small", "standard", "large"} {
		for _, higher := range []bool{false, true} {
			t.Run(profile+"/"+map[bool]string{false: "lower", true: "higher"}[higher], func(t *testing.T) {
				old, _ := borealisOperatorSiteWorkerResourceProfile(profile)
				limits := borealisOperatorSiteWorkerLimits{"100m", "384Mi", "128"}
				if profile == "small" {
					limits.Memory = "128Mi"
				}
				if higher {
					limits.CPU, limits.Memory = "3000m", "1536Mi"
				}
				policy := clusterSSHWorkerPolicy{Profile: profile, OnlineWindow: 300, Limits: limits}
				declared, valid := policy.resources()
				prospective, safe := policy.recreationResources()
				oldCPU, _ := clusterSSHWorkloadMilliCPU(old.LimitCPU)
				oldMemory, _ := clusterSSHWorkloadMemory(old.LimitMemory)
				if !valid || !safe || prospective.CPULimitMilli != max(declared.CPULimitMilli, oldCPU) || prospective.MemoryLimitBytes != max(declared.MemoryLimitBytes, oldMemory) {
					t.Fatal("legacy producer uncertainty lowered charge", declared, prospective)
				}
				_, _, snapshot := sshBrokerFixture(t)
				snapshot.Storage.Requirements.WorkerPolicy = policy
				snapshot.Storage.Requirements.SiteWorkers = []clusterSSHSiteWorkerResource{}
				baseCPU, baseMemory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
				snapshot.DesiredWorkerSites = []int64{7}
				cpu, memory, ok2 := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
				if !ok || !ok2 || cpu != baseCPU+2*prospective.CPURequestMilli || memory != baseMemory+2*prospective.MemoryLimitBytes {
					t.Fatal("missing worker/candidate charge changed", cpu, memory)
				}
				if profile == "standard" && !higher && (declared.MemoryLimitBytes != 384<<20 || prospective.MemoryLimitBytes != 512<<20) {
					t.Fatal("declared384 and conservative512 collapsed")
				}
			})
		}
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
			// One bounded instance-manager memory cap per joining target.
			// Native CPU remains outside this partial subtotal.
			wantMemory += 128 << 20
			// Six ordinary application templates each retain candidate and surge.
			wantCPU += 1200
			wantMemory += 1536 << 20
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
			valid := slices.Contains([]string{"empty", "missing worker", "observed smaller", "observed larger", "two sites"}, mode)
			// Engine.sh holds one complete candidate cohort alongside the
			// observed/reconstructed workers, independently of application overlap.
			rotation := map[string][2]uint64{
				"empty":            {25, 256 << 20},
				"missing worker":   {75, 768 << 20},
				"observed smaller": {50, 512 << 20},
				"observed larger":  {50, 1 << 30},
				"two sites":        {100, 1 << 30},
			}[mode]
			wantCPU += rotation[0]
			wantMemory += rotation[1]
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
