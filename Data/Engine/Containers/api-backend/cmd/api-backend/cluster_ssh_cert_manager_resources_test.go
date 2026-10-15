package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func sshCertManagerResourceValue(t *testing.T, name string) clusterSSHCertManagerResources {
	t.Helper()
	role, _ := clusterSSHCertManagerRole(name)
	plan, ok := clusterSSHCertManagerResourcePlan(sshCertManagerDeploymentFixture(name), "cert-manager-"+role)
	if !ok {
		t.Fatal("invalid producer resource fixture")
	}
	plan.Observed = 1
	return plan
}

func TestClusterSSHCertManagerResources(t *testing.T) {
	for _, name := range []string{"cert-manager", "cert-manager-webhook"} {
		t.Run(name, func(t *testing.T) { testClusterSSHCertManagerResources(t, name) })
	}
}

func testClusterSSHCertManagerResources(t *testing.T, name string) {
	role, _ := clusterSSHCertManagerRole(name)
	for _, mode := range []string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "missing bounds", "Pod bounds differ", "owner bounds differ", "allocated differs", "resize pending", "overhead", "Pod resources", "resource claims", "template node", "missing selector", "extra selector", "template selector", "Pod selector", "owner affinity", "topology", "scheduler", "priority", "tolerations", "template tolerations", "missing replicas", "zero replicas", "negative replicas", "excess replicas", "custom strategy", "custom surge", "malformed strategy", "malformed surge"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			deployment := f.objects[(clusterSSHCertManagerDeploymentPrefix + name)]
			ds := clusterSSHStorageMap(deployment, "spec")
			spec := sshCertManagerSpec(f, name)
			pod := sshCertManagerRuntimePod(f, name, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			rs := f.objects[clusterSSHCertManagerReplicaSetPrefix+name+"-abcdef"]
			rsSpec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")
			wanted := sshCertManagerResourceValue(t, name)
			if mode == "replacement" {
				wanted.Observed = 2
			}
			switch mode {
			case "five replicas":
				ds["replicas"] = 5
				wanted.Replicas = 5
				wanted.Surge = 2
			case "API defaults":
				ds["strategy"] = map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": "25%", "maxUnavailable": "25%"}}
				ps["schedulerName"] = "default-scheduler"
				ps["priority"] = 0
				ps["preemptionPolicy"] = "PreemptLowerPriority"
			case "admitted tolerations":
				ps["tolerations"] = []any{map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}, map[string]any{"key": "node.kubernetes.io/unreachable", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}}
			case "observed excess":
				list := f.objects[clusterSSHCertManagerPodsPath(name, f.a.Source.Members[0].Name)]
				for _, name := range []string{name + "-extra1", name + "-extra2"} {
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
				clusterSSHStorageMap(clusterSSHStorageMap(sshCertManagerContainer(f, name), "resources"), "limits")["memory"] = "300Mi"
				sshCertManagerRuntimeFixture(f)
				wanted.Envelope.MemoryLimitBytes = 300 << 20
			case "missing bounds":
				delete(sshCertManagerContainer(f, name), "resources")
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
			case "missing selector":
				delete(spec, "nodeSelector")
			case "extra selector":
				clusterSSHStorageMap(spec, "nodeSelector")["disk"] = "fast"
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
				ds["strategy"] = map[string]any{"rollingUpdate": map[string]any{"maxSurge": "100%"}}
			case "malformed strategy":
				ds["strategy"] = []any{}
			case "malformed surge":
				ds["strategy"] = map[string]any{"rollingUpdate": false}
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope"}, mode)
			got := value.Requirements.CertManagerResources
			if name == "cert-manager-webhook" {
				got = value.Requirements.CertManagerWebhookResources
			}
			if (err == nil) != valid || (valid && got != wanted) {
				t.Fatal("unsafe operator budget", err, got, wanted)
			}
			if valid {
				cpu, memory, ok := wanted.budget("cert-manager-" + role)
				count := max(wanted.Replicas+wanted.Surge, wanted.Observed)
				if !ok || cpu != count*wanted.Envelope.CPURequestMilli || memory != count*wanted.Envelope.MemoryLimitBytes {
					t.Fatal("incorrect cluster-wide operator budget")
				}
			}
		})
	}
}

func TestClusterSSHCertManagerResourceBudgetRejectsProjection(t *testing.T) {
	for _, name := range []string{"cert-manager", "cert-manager-webhook"} {
		t.Run(name, func(t *testing.T) { testClusterSSHCertManagerResourceBudgetRejectsProjection(t, name) })
	}
}

func testClusterSSHCertManagerResourceBudgetRejectsProjection(t *testing.T, name string) {
	role, _ := clusterSSHCertManagerRole(name)
	for _, mode := range []string{"missing", "name", "wrong role", "zero request", "request above cap", "replicas", "observed", "surge", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshCertManagerResourceValue(t, name)
			switch mode {
			case "missing":
				v = clusterSSHCertManagerResources{}
			case "name":
				v.Envelope.Name = "other"
			case "wrong role":
				v.Envelope.Name = "cert-manager-controller"
				if role == "controller" {
					v.Envelope.Name = "cert-manager-webhook"
				}
			case "zero request":
				v.Envelope.CPURequestMilli = 0
			case "request above cap":
				v.Envelope.MemoryRequestBytes = v.Envelope.MemoryLimitBytes + 1
			case "replicas":
				v.Replicas = 17
			case "observed":
				v.Observed = 17
			case "surge":
				v.Surge = 0
			case "CPU overflow":
				v.Envelope.CPURequestMilli = clusterSSHCapacityLimit
				v.Envelope.CPULimitMilli = clusterSSHCapacityLimit
			case "memory overflow":
				v.Envelope.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if _, _, ok := v.budget("cert-manager-" + role); ok {
				t.Fatal("invalid operator projection accepted")
			}
		})
	}
}

func TestClusterSSHCertManagerResourceScopeDrift(t *testing.T) {
	for _, name := range []string{"cert-manager", "cert-manager-webhook"} {
		t.Run(name, func(t *testing.T) { testClusterSSHCertManagerResourceScopeDrift(t, name) })
	}
}

func testClusterSSHCertManagerResourceScopeDrift(t *testing.T, name string) {
	for _, mode := range []string{"desired", "Pod"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				if mode == "desired" {
					clusterSSHStorageMap(f.objects[(clusterSSHCertManagerDeploymentPrefix+name)], "spec")["replicas"] = 2
				} else {
					clusterSSHStorageMap(sshCertManagerRuntimePod(f, name, 0), "spec")["overhead"] = map[string]any{"memory": "1Mi"}
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
