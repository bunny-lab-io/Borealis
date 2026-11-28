package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"
)

func sshLonghornUIResources() map[string]any {
	return map[string]any{"requests": map[string]any{"cpu": "15m", "memory": "20Mi"}, "limits": map[string]any{"cpu": "80m", "memory": "40Mi"}}
}
func sshLonghornUIResourceAffinity() map[string]any {
	return map[string]any{"podAntiAffinity": map[string]any{"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{"weight": 1, "podAffinityTerm": map[string]any{"topologyKey": "kubernetes.io/hostname", "labelSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "In", "values": []any{"longhorn-ui"}}}}}}}}}
}
func sshLonghornUIResourceValue(t *testing.T) clusterSSHLonghornUIResources {
	t.Helper()
	plan, ok := clusterSSHLonghornUIResourcePlan(sshLonghornUIFixture())
	if !ok {
		t.Fatal("invalid UI producer fixture")
	}
	plan.Observed = 1
	return plan
}
func sshLonghornUIOwnerSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornUIReplicaSetPrefix+"longhorn-ui-abcdef"], "spec"), "template"), "spec")
}
func TestClusterSSHLonghornUIResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "current allocation", "missing bounds", "Pod bounds differ", "owner bounds differ", "allocated differs", "runtime differs", "allocation status", "resize pending", "resize condition", "missing replicas", "zero replicas", "negative replicas", "excess replicas", "custom strategy", "custom surge", "custom unavailable", "malformed strategy", "malformed surge", "unknown strategy", "request above cap", "extra resource"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			ds := clusterSSHStorageMap(f.objects[clusterSSHLonghornUIPath], "spec")
			spec := sshLonghornUISpec(f)
			pod := sshLonghornUIPod(f, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			runtime := sshLonghornUIRuntime(f, 0)
			rs := sshLonghornUIOwnerSpec(f)
			wanted := sshLonghornUIResourceValue(t)
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
				ps["schedulerName"], ps["preemptionPolicy"] = "default-scheduler", "PreemptLowerPriority"
			case "admitted tolerations":
				ps["tolerations"] = []any{map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}, map[string]any{"key": "node.kubernetes.io/unreachable", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}}
			case "observed excess":
				list := f.objects[clusterSSHLonghornUIPodsPath(f.a.Source.Members[0].Name)]
				for _, name := range []string{"longhorn-ui-extra1", "longhorn-ui-extra2", "longhorn-ui-extra3"} {
					raw, _ := json.Marshal(pod)
					copy, _, err := clusterSSHStorageObject(raw)
					if err != nil {
						t.Fatal(err)
					}
					meta := clusterSSHStorageMap(copy, "metadata")
					meta["name"], meta["uid"] = name, newClusterUUID()
					list["items"] = append(list["items"].([]any), copy)
				}
				wanted.Observed = 4
			case "changed envelope":
				for _, s := range []map[string]any{spec, rs, ps} {
					c := s["containers"].([]any)[0].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["memory"] = "96Mi"
				}
				wanted.Envelope.MemoryLimitBytes = 96 << 20
			case "current allocation":
				runtime["resources"] = sshLonghornUIResources()
				runtime["allocatedResources"] = map[string]any{"cpu": "15m", "memory": "20Mi"}
			case "missing bounds":
				delete(sshLonghornUIContainer(f), "resources")
			case "Pod bounds differ":
				clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["memory"] = "96Mi"
			case "owner bounds differ":
				delete(rs["containers"].([]any)[0].(map[string]any), "resources")
			case "allocated differs":
				runtime["allocatedResources"] = map[string]any{"cpu": "16m", "memory": "20Mi"}
			case "runtime differs":
				runtime["resources"] = sshLonghornUIResources()
				clusterSSHStorageMap(clusterSSHStorageMap(runtime, "resources"), "limits")["memory"] = "96Mi"
			case "allocation status":
				runtime["allocatedResourcesStatus"] = []any{map[string]any{}}
			case "resize pending":
				status["resize"] = "InProgress"
			case "resize condition":
				status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "PodResizePending", "status": "True"})
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
			case "custom unavailable":
				ds["strategy"] = map[string]any{"rollingUpdate": map[string]any{"maxUnavailable": 1}}
			case "malformed strategy":
				ds["strategy"] = []any{}
			case "malformed surge":
				ds["strategy"] = map[string]any{"rollingUpdate": false}
			case "unknown strategy":
				ds["strategy"] = map[string]any{"other": true}
			case "request above cap":
				clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornUIContainer(f), "resources"), "requests")["cpu"] = "81m"
			case "extra resource":
				clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornUIContainer(f), "resources"), "requests")["ephemeral-storage"] = "1Gi"
			}
			before, _ := json.Marshal(f.objects)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "current allocation"}, mode)
			if (err == nil) != valid || (valid && got.Requirements.LonghornUIResources != wanted) {
				t.Fatal("unsafe UI projection", err, got.Requirements.LonghornUIResources, wanted)
			}
			if valid {
				cpu, memory, ok := got.Requirements.LonghornUIResources.budget()
				count := max(wanted.Replicas+wanted.Surge, wanted.Observed)
				if !ok || cpu != count*wanted.Envelope.CPURequestMilli || memory != count*wanted.Envelope.MemoryLimitBytes {
					t.Fatal("incorrect per-target UI demand")
				}
				if f.calls[clusterSSHLonghornUIReplicaSetPrefix+"longhorn-ui-abcdef"] != 1 {
					t.Fatal("owner reread added")
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHLonghornUIPodsPath(member.Name)] != 1 {
						t.Fatal("source reads changed")
					}
				}
			}
			after, _ := json.Marshal(f.objects)
			if string(before) != string(after) {
				t.Fatal("source evidence mutated")
			}
		})
	}
}

func TestClusterSSHLonghornUIResourcePlacement(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"selector":         func(s map[string]any) { s["nodeSelector"] = map[string]any{"disk": "fast"} },
		"affinity missing": func(s map[string]any) { delete(s, "affinity") },
		"affinity extra":   func(s map[string]any) { clusterSSHStorageMap(s, "affinity")["nodeAffinity"] = map[string]any{} },
		"hard affinity": func(s map[string]any) {
			s["affinity"] = map[string]any{"podAntiAffinity": map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": []any{}}}
		},
		"affinity weight": func(s map[string]any) {
			clusterSSHStorageMap(clusterSSHStorageMap(s, "affinity"), "podAntiAffinity")["preferredDuringSchedulingIgnoredDuringExecution"].([]any)[0].(map[string]any)["weight"] = 100
		},
		"topology":        func(s map[string]any) { s["topologySpreadConstraints"] = []any{map[string]any{}} },
		"scheduler":       func(s map[string]any) { s["schedulerName"] = "other" },
		"priority":        func(s map[string]any) { s["priority"] = 0 },
		"priority class":  func(s map[string]any) { s["priorityClassName"] = "other" },
		"preemption":      func(s map[string]any) { s["preemptionPolicy"] = "Never" },
		"toleration":      func(s map[string]any) { s["tolerations"] = []any{map[string]any{"operator": "Exists"}} },
		"gate":            func(s map[string]any) { s["schedulingGates"] = []any{map[string]any{}} },
		"host network":    func(s map[string]any) { s["hostNetwork"] = true },
		"service account": func(s map[string]any) { s["serviceAccountName"] = "other" },
		"overhead":        func(s map[string]any) { s["overhead"] = map[string]any{"memory": "1Mi"} },
		"Pod resources":   func(s map[string]any) { s["resources"] = sshLonghornUIResources() },
		"claims":          func(s map[string]any) { s["resourceClaims"] = []any{map[string]any{}} },
		"runtime class":   func(s map[string]any) { s["runtimeClassName"] = "other" },
		"resize policy": func(s map[string]any) {
			s["containers"].([]any)[0].(map[string]any)["resizePolicy"] = []any{map[string]any{}}
		},
	}
	for name, mutate := range mutations {
		for _, location := range []string{"Deployment", "owner", "Pod"} {
			t.Run(location+"/"+name, func(t *testing.T) {
				f := newSSHStorageFixture(t, false)
				spec := sshLonghornUISpec(f)
				if location == "owner" {
					spec = sshLonghornUIOwnerSpec(f)
				}
				if location == "Pod" {
					spec = clusterSSHStorageMap(sshLonghornUIPod(f, 0), "spec")
				}
				mutate(spec)
				if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != clusterbootstrap.ErrPreparationConfig {
					t.Fatal("unsupported UI placement accepted", err)
				}
			})
		}
	}
}

func TestClusterSSHLonghornUIResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing", "name", "zero request", "request above cap", "replicas", "observed", "empty observed", "surge", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshLonghornUIResourceValue(t)
			switch mode {
			case "missing":
				v = clusterSSHLonghornUIResources{}
			case "name":
				v.Envelope.Name = "longhorn-manager"
			case "zero request":
				v.Envelope.CPURequestMilli = 0
			case "request above cap":
				v.Envelope.MemoryRequestBytes = v.Envelope.MemoryLimitBytes + 1
			case "replicas":
				v.Replicas = 17
			case "observed":
				v.Observed = 17
			case "empty observed":
				v.Observed = 0
			case "surge":
				v.Surge = 0
			case "CPU overflow":
				v.Envelope.CPURequestMilli = clusterSSHCapacityLimit
				v.Envelope.CPULimitMilli = clusterSSHCapacityLimit
			case "memory overflow":
				v.Envelope.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if _, _, ok := v.budget(); ok {
				t.Fatal("invalid UI budget accepted")
			}
		})
	}
}

func TestClusterSSHLonghornUIResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"desired", "owner", "Pod", "coherent", "replicas", "placement", "observed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				specs := []map[string]any{sshLonghornUISpec(f), sshLonghornUIOwnerSpec(f), clusterSSHStorageMap(sshLonghornUIPod(f, 0), "spec")}
				for i, s := range specs {
					if mode == "coherent" || (mode == "desired" && i == 0) || (mode == "owner" && i == 1) || (mode == "Pod" && i == 2) {
						c := s["containers"].([]any)[0].(map[string]any)
						clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "81m"
					}
				}
				if mode == "replicas" {
					clusterSSHStorageMap(f.objects[clusterSSHLonghornUIPath], "spec")["replicas"] = 3
				}
				if mode == "placement" {
					specs[0]["priorityClassName"] = "other"
				}
				if mode == "observed" {
					raw, _ := json.Marshal(sshLonghornUIPod(f, 0))
					p, _, _ := clusterSSHStorageObject(raw)
					m := clusterSSHStorageMap(p, "metadata")
					m["name"], m["uid"] = "longhorn-ui-extra", newClusterUUID()
					list := f.objects[clusterSSHLonghornUIPodsPath(f.a.Source.Members[0].Name)]
					list["items"] = append(list["items"].([]any), p)
				}
				f.mu.Unlock()
				return checks.Inputs(ctx)
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("UI drift escaped", err)
			}
		})
	}
}

func TestClusterSSHLonghornUIResourceProducerParity(t *testing.T) {
	affinity, _ := json.Marshal(sshLonghornUIResourceAffinity())
	bounds, _ := json.Marshal(sshLonghornUIResources())
	raw, err := exec.Command("python3", "-c", `import json,pathlib,runpy,sys,yaml
m=runpy.run_path(str(pathlib.Path('../../../../../../Data/Engine/K3s/cluster/render-system-upgrade.py')))
s='''apiVersion: apps/v1
kind: Deployment
metadata:
  name: longhorn-ui
  namespace: longhorn-system
spec:
  replicas: 2
  template:
    spec:
      priorityClassName: longhorn-critical
      serviceAccountName: longhorn-ui-service-account
      affinity: '''+sys.argv[1]+'''
      containers:
      - name: longhorn-ui
'''
print(json.dumps(yaml.safe_load(m['render_longhorn_ui'](s,sys.argv[2]))))`, string(affinity), string(bounds)).Output()
	obj, _, parseErr := clusterSSHStorageObject(raw)
	if err != nil || parseErr != nil {
		t.Fatal("producer output", err, parseErr)
	}
	before, _ := json.Marshal(obj)
	plan, ok := clusterSSHLonghornUIResourcePlan(obj)
	plan.Observed = 1
	if !ok || plan != sshLonghornUIResourceValue(t) {
		t.Fatal("UI producer/consumer drift")
	}
	after, _ := json.Marshal(obj)
	if string(before) != string(after) {
		t.Fatal("source receipt mutated")
	}
}
