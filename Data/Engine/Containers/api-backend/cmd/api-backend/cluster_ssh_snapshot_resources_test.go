package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func sshSnapshotResourceValue(t *testing.T) clusterSSHSnapshotResources {
	t.Helper()
	plan, ok := clusterSSHSnapshotResourcePlan(sshSnapshotControllerFixture())
	if !ok {
		t.Fatal("invalid producer resource fixture")
	}
	plan.Observed = 1
	return plan
}

func TestClusterSSHSnapshotResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "topology defaults", "missing placement", "hard spread", "hard affinity", "changed taint policy", "changed affinity policy", "match label keys", "custom unavailable", "missing strategy", "missing bounds", "Pod bounds differ", "owner bounds differ", "allocated differs", "resize pending", "overhead", "Pod resources", "resource claims", "template node", "template selector", "Pod selector", "owner affinity", "topology", "scheduler", "priority", "tolerations", "template tolerations", "missing replicas", "zero replicas", "negative replicas", "excess replicas", "custom strategy", "custom surge", "malformed strategy", "malformed surge"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			deployment := f.objects[clusterSSHSnapshotControllerPath]
			ds := clusterSSHStorageMap(deployment, "spec")
			spec := sshSnapshotControllerSpec(f)
			pod := sshSnapshotRuntimePod(f, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			rs := f.objects[clusterSSHSnapshotReplicaSetPrefix+"snapshot-controller-abcdef"]
			rsSpec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")
			wanted := sshSnapshotResourceValue(t)
			if mode == "replacement" {
				wanted.Observed = 2
			}
			switch mode {
			case "five replicas":
				ds["replicas"] = 5
				wanted.Replicas = 5
			case "API defaults":
				ds["strategy"] = map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": 0, "maxUnavailable": 1}}
				ps["schedulerName"] = "default-scheduler"
				ps["priority"] = 0
				ps["preemptionPolicy"] = "PreemptLowerPriority"
			case "admitted tolerations":
				ps["tolerations"] = []any{map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}, map[string]any{"key": "node.kubernetes.io/unreachable", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}}
			case "observed excess":
				list := f.objects[clusterSSHSnapshotPodsPath(f.a.Source.Members[0].Name)]
				for _, name := range []string{"snapshot-controller-extra1", "snapshot-controller-extra2"} {
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
				clusterSSHStorageMap(clusterSSHStorageMap(sshSnapshotControllerContainer(f), "resources"), "limits")["memory"] = "300Mi"
				sshSnapshotRuntimeFixture(f)
				wanted.Envelope.MemoryLimitBytes = 300 << 20
			case "topology defaults":
				topology := ps["topologySpreadConstraints"].([]any)[0].(map[string]any)
				topology["nodeAffinityPolicy"], topology["nodeTaintsPolicy"] = "Honor", "Ignore"
				topology["matchLabelKeys"] = []any{}
			case "missing placement":
				delete(spec, "affinity")
			case "hard spread":
				ps["topologySpreadConstraints"].([]any)[0].(map[string]any)["whenUnsatisfiable"] = "DoNotSchedule"
			case "hard affinity":
				clusterSSHStorageMap(spec, "affinity")["nodeAffinity"] = map[string]any{}
			case "changed taint policy":
				ps["topologySpreadConstraints"].([]any)[0].(map[string]any)["nodeTaintsPolicy"] = "Honor"
			case "changed affinity policy":
				ps["topologySpreadConstraints"].([]any)[0].(map[string]any)["nodeAffinityPolicy"] = "Ignore"
			case "match label keys":
				ps["topologySpreadConstraints"].([]any)[0].(map[string]any)["matchLabelKeys"] = []any{"pod-template-hash"}
			case "custom unavailable":
				clusterSSHStorageMap(clusterSSHStorageMap(ds, "strategy"), "rollingUpdate")["maxUnavailable"] = 2
			case "missing strategy":
				delete(ds, "strategy")
			case "missing bounds":
				delete(sshSnapshotControllerContainer(f), "resources")
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
				ds["strategy"] = map[string]any{"type": "Recreate"}
			case "custom surge":
				clusterSSHStorageMap(clusterSSHStorageMap(ds, "strategy"), "rollingUpdate")["maxSurge"] = 1
			case "malformed strategy":
				ds["strategy"] = []any{}
			case "malformed surge":
				ds["strategy"] = map[string]any{"type": "RollingUpdate", "rollingUpdate": false}
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "topology defaults"}, mode)
			if (err == nil) != valid || (valid && value.Requirements.SnapshotResources != wanted) {
				t.Fatal("unsafe operator budget", err, value.Requirements.SnapshotResources, wanted)
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

func TestClusterSSHSnapshotResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing", "name", "zero request", "request above cap", "replicas", "observed", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshSnapshotResourceValue(t)
			switch mode {
			case "missing":
				v = clusterSSHSnapshotResources{}
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
				v.Envelope.CPURequestMilli = clusterSSHCapacityLimit
				v.Envelope.CPULimitMilli = clusterSSHCapacityLimit
			case "memory overflow":
				v.Envelope.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if _, _, ok := v.budget(); ok {
				t.Fatal("invalid operator projection accepted")
			}
		})
	}
}

func TestClusterSSHSnapshotResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"desired", "Pod"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				if mode == "desired" {
					clusterSSHStorageMap(f.objects[clusterSSHSnapshotControllerPath], "spec")["replicas"] = 3
				} else {
					clusterSSHStorageMap(sshSnapshotRuntimePod(f, 0), "spec")["overhead"] = map[string]any{"memory": "1Mi"}
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

func TestClusterSSHSnapshotTopologyPreservesReceiptInput(t *testing.T) {
	object := sshSnapshotControllerFixture()
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	topology := spec["topologySpreadConstraints"].([]any)[0].(map[string]any)
	topology["nodeAffinityPolicy"], topology["nodeTaintsPolicy"] = "Honor", "Ignore"
	topology["matchLabelKeys"] = []any{}
	before, _ := json.Marshal(object)
	if !clusterSSHSnapshotTopology(spec["topologySpreadConstraints"]) {
		t.Fatal("default topology rejected")
	}
	after, _ := json.Marshal(object)
	if string(before) != string(after) {
		t.Fatal("resource validation mutated receipt input")
	}
}
