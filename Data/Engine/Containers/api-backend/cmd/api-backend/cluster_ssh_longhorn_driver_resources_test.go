package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"
)

// Synthetic role-specific bounds; never deployment defaults.
func sshLonghornDriverResources(wait bool) map[string]any {
	if wait {
		return map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "12Mi"}, "limits": map[string]any{"cpu": "40m", "memory": "36Mi"}}
	}
	return map[string]any{"requests": map[string]any{"cpu": "25m", "memory": "28Mi"}, "limits": map[string]any{"cpu": "90m", "memory": "56Mi"}}
}
func sshLonghornDriverResourceValue(t *testing.T) clusterSSHLonghornDriverResources {
	t.Helper()
	plan, ok := clusterSSHLonghornDriverResourcePlan(sshLonghornDriverFixture())
	if !ok {
		t.Fatal("invalid driver producer fixture")
	}
	plan.Observed = 1
	return plan
}
func sshLonghornDriverOwnerSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornDriverReplicaSetPrefix+"longhorn-driver-deployer-abcdef"], "spec"), "template"), "spec")
}
func TestClusterSSHLonghornDriverResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "current allocation", "missing bounds", "Pod bounds differ", "owner bounds differ", "allocated differs", "runtime differs", "allocation status", "resize pending", "resize condition", "missing replicas", "zero replicas", "negative replicas", "excess replicas", "custom strategy", "custom surge", "custom unavailable", "malformed strategy", "malformed surge", "unknown strategy", "request above cap", "extra resource"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			ds := clusterSSHStorageMap(f.objects[clusterSSHLonghornDriverPath], "spec")
			spec := sshLonghornDriverSpec(f)
			pod := sshLonghornDriverPod(f, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			runtime := sshLonghornDriverRuntime(f, 0)
			rs := sshLonghornDriverOwnerSpec(f)
			wanted := sshLonghornDriverResourceValue(t)
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
				list := f.objects[clusterSSHLonghornDriverPodsPath(f.a.Source.Members[0].Name)]
				for _, name := range []string{"longhorn-driver-deployer-extra1", "longhorn-driver-deployer-extra2", "longhorn-driver-deployer-extra3"} {
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
				wanted.Main.MemoryLimitBytes = 96 << 20
			case "current allocation":
				runtime["resources"] = sshLonghornDriverResources(false)
				runtime["allocatedResources"] = map[string]any{"cpu": "25m", "memory": "28Mi"}
			case "missing bounds":
				delete(sshLonghornDriverContainer(f), "resources")
			case "Pod bounds differ":
				clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["memory"] = "96Mi"
			case "owner bounds differ":
				delete(rs["containers"].([]any)[0].(map[string]any), "resources")
			case "allocated differs":
				runtime["allocatedResources"] = map[string]any{"cpu": "26m", "memory": "28Mi"}
			case "runtime differs":
				runtime["resources"] = sshLonghornDriverResources(false)
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
				clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornDriverContainer(f), "resources"), "requests")["cpu"] = "91m"
			case "extra resource":
				clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornDriverContainer(f), "resources"), "requests")["ephemeral-storage"] = "1Gi"
			}
			before, _ := json.Marshal(f.objects)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "current allocation"}, mode)
			if (err == nil) != valid || (valid && got.Requirements.LonghornDriverResources != wanted) {
				t.Fatal("unsafe driver projection", err, got.Requirements.LonghornDriverResources, wanted)
			}
			if valid {
				cpu, memory, ok := got.Requirements.LonghornDriverResources.budget()
				count := max(wanted.Replicas+wanted.Surge, wanted.Observed)
				if !ok || cpu != count*max(wanted.Main.CPURequestMilli, wanted.Wait.CPURequestMilli) || memory != count*max(wanted.Main.MemoryLimitBytes, wanted.Wait.MemoryLimitBytes) {
					t.Fatal("incorrect per-target driver demand")
				}
				if f.calls[clusterSSHLonghornDriverReplicaSetPrefix+"longhorn-driver-deployer-abcdef"] != 1 {
					t.Fatal("owner reread added")
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHLonghornDriverPodsPath(member.Name)] != 1 {
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

func TestClusterSSHLonghornDriverResourcePlacement(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"selector":        func(s map[string]any) { s["nodeSelector"] = map[string]any{"disk": "fast"} },
		"affinity":        func(s map[string]any) { s["affinity"] = map[string]any{"nodeAffinity": map[string]any{}} },
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
		"Pod resources":   func(s map[string]any) { s["resources"] = sshLonghornDriverResources(false) },
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
				spec := sshLonghornDriverSpec(f)
				if location == "owner" {
					spec = sshLonghornDriverOwnerSpec(f)
				}
				if location == "Pod" {
					spec = clusterSSHStorageMap(sshLonghornDriverPod(f, 0), "spec")
				}
				mutate(spec)
				if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != clusterbootstrap.ErrPreparationConfig {
					t.Fatal("unsupported driver placement accepted", err)
				}
			})
		}
	}
}

func TestClusterSSHLonghornDriverResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"wait missing", "wait role", "wait request above cap", "wait CPU overflow", "wait memory overflow", "missing", "name", "zero request", "request above cap", "replicas", "observed", "empty observed", "surge", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshLonghornDriverResourceValue(t)
			switch mode {
			case "wait missing":
				v.Wait = clusterSSHApplicationResource{}
			case "wait role":
				v.Wait.Name = v.Main.Name
			case "wait request above cap":
				v.Wait.CPURequestMilli = v.Wait.CPULimitMilli + 1
			case "wait CPU overflow":
				v.Wait.CPURequestMilli = clusterSSHCapacityLimit
				v.Wait.CPULimitMilli = clusterSSHCapacityLimit
			case "wait memory overflow":
				v.Wait.MemoryLimitBytes = clusterSSHCapacityLimit
			case "missing":
				v = clusterSSHLonghornDriverResources{}
			case "name":
				v.Main.Name = "longhorn-manager"
			case "zero request":
				v.Main.CPURequestMilli = 0
			case "request above cap":
				v.Main.MemoryRequestBytes = v.Main.MemoryLimitBytes + 1
			case "replicas":
				v.Replicas = 17
			case "observed":
				v.Observed = 17
			case "empty observed":
				v.Observed = 0
			case "surge":
				v.Surge = 0
			case "CPU overflow":
				v.Main.CPURequestMilli = clusterSSHCapacityLimit
				v.Main.CPULimitMilli = clusterSSHCapacityLimit
			case "memory overflow":
				v.Main.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if _, _, ok := v.budget(); ok {
				t.Fatal("invalid driver budget accepted")
			}
		})
	}
}

func TestClusterSSHLonghornDriverResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"wait coherent", "desired", "owner", "Pod", "coherent", "replicas", "placement", "observed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				specs := []map[string]any{sshLonghornDriverSpec(f), sshLonghornDriverOwnerSpec(f), clusterSSHStorageMap(sshLonghornDriverPod(f, 0), "spec")}
				for i, s := range specs {
					if mode == "wait coherent" {
						c := s["initContainers"].([]any)[0].(map[string]any)
						clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "41m"
					}
					if mode == "coherent" || (mode == "desired" && i == 0) || (mode == "owner" && i == 1) || (mode == "Pod" && i == 2) {
						c := s["containers"].([]any)[0].(map[string]any)
						clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "91m"
					}
				}
				if mode == "replicas" {
					clusterSSHStorageMap(f.objects[clusterSSHLonghornDriverPath], "spec")["replicas"] = 3
				}
				if mode == "placement" {
					specs[0]["priorityClassName"] = "other"
				}
				if mode == "observed" {
					raw, _ := json.Marshal(sshLonghornDriverPod(f, 0))
					p, _, _ := clusterSSHStorageObject(raw)
					m := clusterSSHStorageMap(p, "metadata")
					m["name"], m["uid"] = "longhorn-driver-deployer-extra", newClusterUUID()
					list := f.objects[clusterSSHLonghornDriverPodsPath(f.a.Source.Members[0].Name)]
					list["items"] = append(list["items"].([]any), p)
				}
				f.mu.Unlock()
				return checks.Inputs(ctx)
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("driver drift escaped", err)
			}
		})
	}
}

func TestClusterSSHLonghornDriverResourceProducerParity(t *testing.T) {
	main, _ := json.Marshal(sshLonghornDriverResources(false))
	wait, _ := json.Marshal(sshLonghornDriverResources(true))
	raw, err := exec.Command("python3", "-c", `import json,pathlib,runpy,sys,yaml
m=runpy.run_path(str(pathlib.Path('../../../../../../Data/Engine/K3s/cluster/render-system-upgrade.py')))
s='''apiVersion: apps/v1
kind: Deployment
metadata:
  name: longhorn-driver-deployer
  namespace: longhorn-system
spec:
  replicas: 1
  template:
    spec:
      priorityClassName: longhorn-critical
      serviceAccountName: longhorn-service-account
      containers:
        - name: longhorn-driver-deployer
      initContainers:
        - name: wait-longhorn-manager
'''
print(json.dumps(yaml.safe_load(m['render_longhorn_driver'](s,sys.argv[1],sys.argv[2]))))`, string(main), string(wait)).Output()
	obj, _, parseErr := clusterSSHStorageObject(raw)
	if err != nil || parseErr != nil {
		t.Fatal("producer output", err, parseErr)
	}
	before, _ := json.Marshal(obj)
	plan, ok := clusterSSHLonghornDriverResourcePlan(obj)
	plan.Observed = 1
	if !ok || plan != sshLonghornDriverResourceValue(t) {
		t.Fatal("driver producer/consumer drift")
	}
	after, _ := json.Marshal(obj)
	if string(before) != string(after) {
		t.Fatal("source receipt mutated")
	}
}

func TestClusterSSHLonghornDriverWaitResources(t *testing.T) {
	for _, mode := range []string{"init CPU larger", "init memory larger", "missing", "owner drift", "Pod drift", "restartable", "resize policy", "runtime drift", "allocation drift", "allocation status", "current allocation", "extra init"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			pod := sshLonghornDriverPod(f, 0)
			ps := clusterSSHStorageMap(pod, "spec")
			init := ps["initContainers"].([]any)[0].(map[string]any)
			runtime := sshLonghornDriverInitRuntime(f, 0)
			desired := sshLonghornDriverSpec(f)["initContainers"].([]any)[0].(map[string]any)
			owner := sshLonghornDriverOwnerSpec(f)["initContainers"].([]any)[0].(map[string]any)
			wantCPU, wantMemory := uint64(50), uint64(112<<20)
			switch mode {
			case "init CPU larger", "init memory larger":
				for _, c := range []map[string]any{desired, owner, init} {
					r := clusterSSHStorageMap(c, "resources")
					if mode == "init CPU larger" {
						clusterSSHStorageMap(r, "requests")["cpu"] = "35m"
						wantCPU = 70
					} else {
						clusterSSHStorageMap(r, "limits")["memory"] = "100Mi"
						wantMemory = 200 << 20
					}
				}
			case "missing":
				delete(desired, "resources")
			case "owner drift":
				delete(owner, "resources")
			case "Pod drift":
				delete(init, "resources")
			case "restartable":
				init["restartPolicy"] = "Always"
			case "resize policy":
				init["resizePolicy"] = []any{map[string]any{}}
			case "runtime drift":
				runtime["resources"] = sshLonghornDriverResources(false)
			case "allocation drift":
				runtime["allocatedResources"] = map[string]any{"cpu": "11m", "memory": "12Mi"}
			case "allocation status":
				runtime["allocatedResourcesStatus"] = []any{map[string]any{}}
			case "current allocation":
				runtime["resources"] = sshLonghornDriverResources(true)
				runtime["allocatedResources"] = map[string]any{"cpu": "10m", "memory": "12Mi"}
			case "extra init":
				ps["initContainers"] = append(ps["initContainers"].([]any), init)
			}
			before, _ := json.Marshal(f.objects)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"init CPU larger", "init memory larger", "current allocation"}, mode)
			if (err == nil) != valid {
				t.Fatal("init resource acceptance", err)
			}
			if valid {
				cpu, memory, ok := got.Requirements.LonghornDriverResources.budget()
				if !ok || cpu != wantCPU || memory != wantMemory {
					t.Fatal("sequential max budget", cpu, memory)
				}
			}
			after, _ := json.Marshal(f.objects)
			if string(before) != string(after) {
				t.Fatal("receipt mutated")
			}
		})
	}
}
