package main

import (
	"context"
	"testing"
)

func sshPostgresResourcesFixture() map[string]any {
	return map[string]any{"requests": map[string]any{"cpu": "1", "memory": "1Gi"}, "limits": map[string]any{"cpu": "2", "memory": "4Gi"}}
}

func sshPostgresResourceValue(t *testing.T) clusterSSHApplicationResource {
	t.Helper()
	v, ok := clusterSSHWorkloadResources("postgres", sshPostgresResourcesFixture())
	if !ok {
		t.Fatal("invalid fixture")
	}
	return v
}

func TestClusterSSHPostgresResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "canonical units", "missing cluster", "missing pod", "missing bootstrap", "missing limit", "unknown resource", "request exceeds cap", "pod drift", "bootstrap drift", "peer drift", "extra container", "extra init", "restartable init", "pod resources", "overhead", "runtime class", "claims", "ephemeral", "resize", "resize condition", "resize policy", "runtime drift", "allocation drift", "runtime absent", "allocated matching"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement" || mode == "peer drift")
			cluster := clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")
			pod := f.pod(0)
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			c := sshPostgresContainer(pod, false)
			init := spec["initContainers"].([]any)[0].(map[string]any)
			runtime := sshPostgresContainer(pod, true)
			switch mode {
			case "canonical units":
				clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "requests")["cpu"] = "1000m"
			case "missing cluster":
				delete(cluster, "resources")
			case "missing pod":
				delete(c, "resources")
			case "missing bootstrap":
				delete(init, "resources")
			case "missing limit":
				delete(clusterSSHStorageMap(clusterSSHStorageMap(cluster, "resources"), "limits"), "memory")
			case "unknown resource":
				clusterSSHStorageMap(clusterSSHStorageMap(cluster, "resources"), "requests")["example.com/gpu"] = "1"
			case "request exceeds cap":
				clusterSSHStorageMap(clusterSSHStorageMap(cluster, "resources"), "requests")["cpu"] = "3"
			case "pod drift":
				clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["memory"] = "5Gi"
			case "bootstrap drift":
				clusterSSHStorageMap(clusterSSHStorageMap(init, "resources"), "limits")["memory"] = "5Gi"
			case "peer drift":
				delete(sshPostgresContainer(f.pod(1), false), "resources")
			case "extra container":
				spec["containers"] = append(spec["containers"].([]any), c)
			case "extra init":
				spec["initContainers"] = append(spec["initContainers"].([]any), init)
			case "restartable init":
				init["restartPolicy"] = "Always"
			case "pod resources":
				spec["resources"] = sshPostgresResourcesFixture()
			case "overhead":
				spec["overhead"] = map[string]any{"memory": "1Gi"}
			case "runtime class":
				spec["runtimeClassName"] = "other"
			case "claims":
				spec["resourceClaims"] = []any{map[string]any{"name": "other"}}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{c}
			case "resize":
				status["resize"] = "InProgress"
			case "resize condition":
				status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "PodResizeInProgress", "status": "True"})
			case "resize policy":
				c["resizePolicy"] = []any{map[string]any{"resourceName": "cpu", "restartPolicy": "NotRequired"}}
			case "runtime drift":
				clusterSSHStorageMap(clusterSSHStorageMap(runtime, "resources"), "limits")["memory"] = "5Gi"
			case "allocation drift":
				runtime["allocatedResources"] = map[string]any{"cpu": "2", "memory": "1Gi"}
			case "allocated matching":
				runtime["allocatedResources"] = map[string]any{"cpu": "1000m", "memory": "1Gi"}
			case "runtime absent":
				delete(runtime, "resources")
			}
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := mode == "expansion" || mode == "replacement" || mode == "canonical units" || mode == "runtime absent" || mode == "allocated matching"
			if (err == nil) != valid {
				t.Fatal("unsafe CNPG evidence", err)
			}
			if valid {
				cpu, memory, ok := clusterSSHNodeWorkloadBudget(got.Requirements)
				if !ok || cpu != 2240+25+uint64(len(f.a.Source.Members))*100 || memory != (5<<30+1024<<20+128<<20)+uint64(len(f.a.Source.Members))*(128<<20) || got.Requirements.PostgresResources != sshPostgresResourceValue(t) {
					t.Fatal("wrong one-instance combined budget", cpu, memory, ok)
				}
			}
		})
	}
}

func TestClusterSSHPostgresResourceBudgetRejectsInvalidProjection(t *testing.T) {
	for _, mode := range []string{"missing", "name", "zero request", "request exceeds cap", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			r := clusterSSHStorageRequirements{SiteWorkers: []clusterSSHSiteWorkerResource{}, Applications: sshApplicationResourceValues(t), PostgresResources: sshPostgresResourceValue(t)}
			switch mode {
			case "missing":
				r.PostgresResources = clusterSSHApplicationResource{}
			case "name":
				r.PostgresResources.Name = "other"
			case "zero request":
				r.PostgresResources.CPURequestMilli = 0
			case "request exceeds cap":
				r.PostgresResources.MemoryRequestBytes = r.PostgresResources.MemoryLimitBytes + 1
			case "CPU overflow":
				r.PostgresResources.CPURequestMilli = clusterSSHCapacityLimit
				r.PostgresResources.CPULimitMilli = clusterSSHCapacityLimit
			case "memory overflow":
				r.PostgresResources.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if _, _, ok := clusterSSHNodeWorkloadBudget(r); ok {
				t.Fatal("invalid combined projection accepted")
			}
		})
	}
}

func TestClusterSSHPostgresResourcesBoundToScope(t *testing.T) {
	f := newSSHStorageFixture(t, true)
	entered := false
	err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
		entered = true
		f.mu.Lock()
		defer f.mu.Unlock()
		// Coherent new caps remain valid in isolation, but invalidate original scope.
		clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec"), "resources"), "limits")["memory"] = "5Gi"
		for i := range f.a.Source.Members {
			pod := f.pod(i)
			for _, group := range []struct{ part, key string }{{"spec", "containers"}, {"spec", "initContainers"}, {"status", "containerStatuses"}, {"status", "initContainerStatuses"}} {
				c := clusterSSHStorageMap(pod, group.part)[group.key].([]any)[0].(map[string]any)
				clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["memory"] = "5Gi"
			}
		}
		return nil
	})
	if err == nil || !entered {
		t.Fatal("accepted coherent CNPG resource drift", err)
	}
	if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != nil {
		t.Fatal("drift fixture not independently valid", err)
	}
}
