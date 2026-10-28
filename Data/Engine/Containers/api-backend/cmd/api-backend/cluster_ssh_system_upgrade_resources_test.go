package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func sshSystemUpgradeResourceValue(t *testing.T) clusterSSHSystemUpgradeResources {
	t.Helper()
	f := &sshStorageFixture{objects: map[string]map[string]any{}}
	sshSystemUpgradeFixture(f)
	plan, ok := clusterSSHSystemUpgradeResourcePlan(f.objects[clusterSSHSystemUpgradePath])
	if !ok {
		t.Fatal("invalid explicitly bounded resource fixture")
	}
	plan.Observed = 1
	return plan
}

func TestClusterSSHSystemUpgradeResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "reordered tolerations", "missing affinity", "changed anti-affinity", "missing toleration", "extended toleration", "missing strategy", "changed toleration seconds", "missing bounds", "Pod bounds differ", "owner bounds differ", "allocated differs", "resize pending", "overhead", "Pod resources", "resource claims", "template node", "template selector", "Pod selector", "owner affinity", "topology", "scheduler", "priority", "tolerations", "template tolerations", "missing replicas", "zero replicas", "negative replicas", "excess replicas", "custom strategy", "custom surge", "malformed strategy", "malformed surge"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			deployment := f.objects[clusterSSHSystemUpgradePath]
			ds := clusterSSHStorageMap(deployment, "spec")
			spec := sshSystemUpgradeSpec(f)
			pod := sshSystemUpgradePod(f, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			rs := f.objects[clusterSSHSystemUpgradeReplicaSetPrefix+"system-upgrade-controller-abcdef"]
			rsSpec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")
			wanted := sshSystemUpgradeResourceValue(t)
			if mode == "replacement" {
				wanted.Observed = 2
			}
			switch mode {
			case "five replicas":
				ds["replicas"] = 5
				wanted.Replicas = 5
			case "API defaults":
				ds["strategy"] = map[string]any{"type": "Recreate"}
				ps["schedulerName"] = "default-scheduler"
				ps["priority"] = 0
				ps["preemptionPolicy"] = "PreemptLowerPriority"
			case "admitted tolerations":
				ps["tolerations"] = append(ps["tolerations"].([]any), map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}, map[string]any{"key": "node.kubernetes.io/unreachable", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300})
			case "observed excess":
				list := f.objects[clusterSSHSystemUpgradePodsPath(f.a.Source.Members[0].Name)]
				for _, name := range []string{"system-upgrade-controller-extra1", "system-upgrade-controller-extra2"} {
					raw, _ := json.Marshal(pod)
					var copy map[string]any
					_ = json.Unmarshal(raw, &copy)
					meta := clusterSSHStorageMap(copy, "metadata")
					meta["name"] = name
					meta["uid"] = newClusterUUID()
					list["items"] = append(list["items"].([]any), copy)
				}
				wanted.Observed = 3
			case "changed envelope":
				clusterSSHStorageMap(clusterSSHStorageMap(sshSystemUpgradeSpec(f)["containers"].([]any)[0].(map[string]any), "resources"), "limits")["memory"] = "300Mi"
				sshSystemUpgradeRuntimeFixture(f)
				wanted.Envelope.MemoryLimitBytes = 300 << 20
			case "reordered tolerations":
				slices.Reverse(ps["tolerations"].([]any))
			case "missing affinity":
				delete(spec, "affinity")
			case "changed anti-affinity":
				clusterSSHStorageMap(clusterSSHStorageMap(spec, "affinity"), "podAntiAffinity")["requiredDuringSchedulingIgnoredDuringExecution"].([]any)[0].(map[string]any)["topologyKey"] = "zone"
			case "missing toleration":
				ps["tolerations"] = ps["tolerations"].([]any)[:4]
			case "extended toleration":
				ps["tolerations"] = append(ps["tolerations"].([]any), map[string]any{"key": "other", "operator": "Exists"})
			case "changed toleration seconds":
				ps["tolerations"].([]any)[0].(map[string]any)["tolerationSeconds"] = 300
			case "missing strategy":
				delete(ds, "strategy")
			case "missing bounds":
				delete(sshSystemUpgradeSpec(f)["containers"].([]any)[0].(map[string]any), "resources")
			case "Pod bounds differ":
				clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["memory"] = "300Mi"
			case "owner bounds differ":
				delete(rsSpec["containers"].([]any)[0].(map[string]any), "resources")
			case "allocated differs":
				status["containerStatuses"].([]any)[0].(map[string]any)["allocatedResources"] = map[string]any{"cpu": "200m", "memory": "100Mi"}
			case "resize pending":
				status["resize"] = "Proposed"
			case "overhead":
				ps["overhead"] = map[string]any{"memory": "1Mi"}
			case "Pod resources":
				ps["resources"] = pc["resources"]
			case "resource claims":
				spec["resourceClaims"] = []any{map[string]any{"name": "other"}}
			case "template node":
				spec["nodeName"] = "old-node"
			case "template selector":
				spec["nodeSelector"] = map[string]any{"disk": "fast"}
			case "Pod selector":
				ps["nodeSelector"] = map[string]any{"disk": "fast"}
			case "owner affinity":
				rsSpec["affinity"] = map[string]any{"nodeAffinity": map[string]any{}}
			case "topology":
				spec["topologySpreadConstraints"] = []any{map[string]any{}}
			case "scheduler":
				spec["schedulerName"] = "other"
			case "priority":
				ps["priority"] = 1
			case "tolerations":
				ps["tolerations"] = []any{map[string]any{"operator": "Exists"}}
			case "template tolerations":
				spec["tolerations"] = []any{map[string]any{"operator": "Exists"}}
			case "missing replicas":
				delete(ds, "replicas")
			case "zero replicas":
				ds["replicas"] = 0
			case "negative replicas":
				ds["replicas"] = -1
			case "excess replicas":
				ds["replicas"] = 17
			case "custom strategy":
				ds["strategy"] = map[string]any{"type": "RollingUpdate"}
			case "custom surge":
				ds["strategy"] = map[string]any{"rollingUpdate": map[string]any{"maxSurge": "100%"}}
			case "malformed strategy":
				ds["strategy"] = []any{}
			case "malformed surge":
				ds["strategy"] = map[string]any{"rollingUpdate": false}
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "reordered tolerations"}, mode)
			if (err == nil) != valid || (valid && value.Requirements.SystemUpgradeResources != wanted) {
				t.Fatal("unsafe operator budget", err, value.Requirements.SystemUpgradeResources, wanted)
			}
			if valid {
				cpu, memory, ok := wanted.budget()
				count := max(wanted.Replicas, wanted.Observed)
				if !ok || cpu != count*wanted.Envelope.CPURequestMilli || memory != count*wanted.Envelope.MemoryLimitBytes {
					t.Fatal("incorrect cluster-wide operator budget")
				}
			}
		})
	}
}

func TestClusterSSHSystemUpgradeResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing", "name", "zero request", "request above cap", "replicas", "observed", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshSystemUpgradeResourceValue(t)
			switch mode {
			case "missing":
				v = clusterSSHSystemUpgradeResources{}
			case "name":
				v.Envelope.Name = "other"
			case "zero request":
				v.Envelope.CPURequestMilli = 0
			case "request above cap":
				v.Envelope.MemoryRequestBytes = v.Envelope.MemoryLimitBytes + 1
			case "replicas":
				v.Replicas = 17
			case "observed":
				v.Observed = 17
			case "CPU overflow":
				v.Replicas = 2
				v.Envelope.CPURequestMilli = clusterSSHCapacityLimit
				v.Envelope.CPULimitMilli = clusterSSHCapacityLimit
			case "memory overflow":
				v.Replicas = 2
				v.Envelope.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if _, _, ok := v.budget(); ok {
				t.Fatal("invalid operator projection accepted")
			}
		})
	}
}

func TestClusterSSHSystemUpgradeResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"desired", "Pod"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				if mode == "desired" {
					clusterSSHStorageMap(f.objects[clusterSSHSystemUpgradePath], "spec")["replicas"] = 2
				} else {
					clusterSSHStorageMap(sshSystemUpgradePod(f, 0), "spec")["overhead"] = map[string]any{"memory": "1Mi"}
				}
				f.mu.Unlock()
				return checks.Inputs(ctx)
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("operator resource drift escaped", err)
			}
		})
	}
}
