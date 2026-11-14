package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func sshLonghornManagerResources(name string) map[string]any {
	if name == "longhorn-manager" {
		return map[string]any{"requests": map[string]any{"cpu": "150m", "memory": "96Mi"}, "limits": map[string]any{"cpu": "400m", "memory": "192Mi"}}
	}
	return map[string]any{"requests": map[string]any{"cpu": "5m", "memory": "8Mi"}, "limits": map[string]any{"cpu": "25m", "memory": "24Mi"}}
}
func sshLonghornManagerResourceValue(t *testing.T) clusterSSHLonghornManagerResources {
	t.Helper()
	manager, ok := clusterSSHWorkloadResources("longhorn-manager", sshLonghornManagerResources("longhorn-manager"))
	prepull, valid := clusterSSHWorkloadResources("pre-pull-share-manager-image", sshLonghornManagerResources("pre-pull-share-manager-image"))
	if !ok || !valid {
		t.Fatal("invalid fixture")
	}
	return clusterSSHLonghornManagerResources{Manager: manager, PrePull: prepull}
}

func TestClusterSSHLonghornManagerResources(t *testing.T) {
	for _, role := range []string{"longhorn-manager", "pre-pull-share-manager-image"} {
		for _, mode := range []string{"expansion", "replacement", "API defaults", "current allocation", "changed envelope", "missing bounds", "missing Pod bounds", "Pod bounds differ", "runtime differs", "allocated differs", "allocation status", "resize pending", "resize condition", "request above cap", "extra resource", "resize policy", "restart policy", "CPU sum overflow", "memory sum overflow"} {
			t.Run(role+"/"+mode, func(t *testing.T) {
				f := newSSHStorageFixture(t, mode == "replacement")
				index := 0
				if role != "longhorn-manager" {
					index = 1
				}
				container := sshLonghornManagerSpec(f)["containers"].([]any)[index].(map[string]any)
				pod := sshLonghornManagerPod(f, 0)
				ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
				pc := ps["containers"].([]any)[index].(map[string]any)
				runtime := status["containerStatuses"].([]any)[index].(map[string]any)
				wanted := sshLonghornManagerResourceValue(t)
				switch mode {
				case "API defaults":
					strategy := clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornManagerPath], "spec"), "updateStrategy")
					strategy["type"] = "RollingUpdate"
					clusterSSHStorageMap(strategy, "rollingUpdate")["maxSurge"] = 0
					ps["schedulerName"], ps["preemptionPolicy"] = "default-scheduler", "PreemptLowerPriority"
				case "current allocation":
					runtime["resources"] = sshLonghornManagerResources(role)
					runtime["allocatedResources"] = clusterSSHStorageMap(sshLonghornManagerResources(role), "requests")
				case "changed envelope":
					bounds := map[string]any{"requests": map[string]any{"cpu": "0.125", "memory": "64Mi"}, "limits": map[string]any{"cpu": "64", "memory": "256Mi"}}
					container["resources"], pc["resources"] = bounds, bounds
					value := clusterSSHApplicationResource{Name: role, CPURequestMilli: 125, CPULimitMilli: 64000, MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20}
					if index == 0 {
						wanted.Manager = value
					} else {
						wanted.PrePull = value
					}
				case "missing bounds":
					delete(container, "resources")
				case "missing Pod bounds":
					delete(pc, "resources")
				case "Pod bounds differ":
					clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["cpu"] = "64"
				case "runtime differs":
					runtime["resources"] = sshLonghornManagerResources(role)
					clusterSSHStorageMap(clusterSSHStorageMap(runtime, "resources"), "limits")["cpu"] = "64"
				case "allocated differs":
					runtime["allocatedResources"] = map[string]any{"cpu": "151m", "memory": "96Mi"}
				case "allocation status":
					runtime["allocatedResourcesStatus"] = []any{map[string]any{}}
				case "resize pending":
					status["resize"] = "InProgress"
				case "resize condition":
					status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "PodResizePending", "status": "True"})
				case "request above cap":
					clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "requests")["cpu"] = "1"
				case "extra resource":
					clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "requests")["ephemeral-storage"] = "1Gi"
				case "resize policy":
					pc["resizePolicy"] = []any{map[string]any{"resourceName": "cpu", "restartPolicy": "NotRequired"}}
				case "restart policy":
					container["restartPolicy"] = "Always"
				case "CPU sum overflow":
					clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "requests")["cpu"] = "9007199254740991m"
					clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "limits")["cpu"] = "9007199254740991m"
				case "memory sum overflow":
					clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "limits")["memory"] = "9007199254740991"
				}
				before, _ := json.Marshal(f.objects)
				got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
				valid := mode == "expansion" || mode == "replacement" || mode == "API defaults" || mode == "current allocation" || mode == "changed envelope"
				if valid {
					if err != nil || got.Requirements.LonghornManagerResources != wanted {
						t.Fatal("lost resource projection", err)
					}
					cpu, memory, ok := got.Requirements.LonghornManagerResources.budget()
					if !ok || cpu != wanted.Manager.CPURequestMilli+wanted.PrePull.CPURequestMilli || memory != wanted.Manager.MemoryLimitBytes+wanted.PrePull.MemoryLimitBytes {
						t.Fatal("persistent containers not summed once per target")
					}
					podRaw, _ := json.Marshal(pod)
					podView, _, parseErr := clusterSSHStorageObject(podRaw)
					if parseErr != nil || !clusterSSHLonghornManagerPodResources(wanted, podView, f.a.Source.Members[0].Name) {
						t.Fatal("valid resource view rejected", parseErr)
					}
					afterView, _ := json.Marshal(podView)
					if string(podRaw) != string(afterView) {
						t.Fatal("per-container checks rewrote source receipt")
					}
					for _, member := range f.a.Source.Members {
						if f.calls[clusterSSHLonghornManagerPodsPath(member.Name)] != 1 {
							t.Fatal("source reads changed")
						}
					}
				} else if err != clusterbootstrap.ErrPreparationConfig || got.Requirements.observation != "" {
					t.Fatal("unproved resource envelope accepted", err)
				}
				after, _ := json.Marshal(f.objects)
				if string(before) != string(after) {
					t.Fatal("source receipt mutated")
				}
			})
		}
	}
}

func TestClusterSSHLonghornManagerResourcePlacement(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"selector":      func(s map[string]any) { s["nodeSelector"] = map[string]any{"disk": "ssd"} },
		"topology":      func(s map[string]any) { s["topologySpreadConstraints"] = []any{map[string]any{}} },
		"scheduler":     func(s map[string]any) { s["schedulerName"] = "other" },
		"priority":      func(s map[string]any) { s["priority"] = 0 },
		"class":         func(s map[string]any) { s["priorityClassName"] = "other" },
		"preemption":    func(s map[string]any) { s["preemptionPolicy"] = "Never" },
		"gate":          func(s map[string]any) { s["schedulingGates"] = []any{map[string]any{}} },
		"host network":  func(s map[string]any) { s["hostNetwork"] = true },
		"overhead":      func(s map[string]any) { s["overhead"] = map[string]any{"memory": "1Mi"} },
		"Pod resources": func(s map[string]any) { s["resources"] = sshLonghornManagerResources("longhorn-manager") },
		"claims":        func(s map[string]any) { s["resourceClaims"] = []any{map[string]any{}} },
		"runtime class": func(s map[string]any) { s["runtimeClassName"] = "other" },
	}
	for name, mutate := range mutations {
		for _, location := range []string{"template", "Pod"} {
			t.Run(location+"/"+name, func(t *testing.T) {
				f := newSSHStorageFixture(t, false)
				spec := sshLonghornManagerSpec(f)
				if location == "Pod" {
					spec = clusterSSHStorageMap(sshLonghornManagerPod(f, 0), "spec")
				}
				mutate(spec)
				if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != clusterbootstrap.ErrPreparationConfig {
					t.Fatal("unsupported placement accepted", err)
				}
			})
		}
	}
	for _, mode := range []string{"template node", "template affinity", "template tolerations", "Pod affinity missing", "Pod affinity foreign", "Pod affinity extra", "Pod priority missing", "Pod toleration missing", "Pod toleration duplicate", "Pod toleration expiry", "Pod toleration network", "strategy missing", "strategy custom", "strategy extra", "surge", "surge malformed", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec := sshLonghornManagerSpec(f)
			ps := clusterSSHStorageMap(sshLonghornManagerPod(f, 0), "spec")
			ds := clusterSSHStorageMap(f.objects[clusterSSHLonghornManagerPath], "spec")
			strategy := clusterSSHStorageMap(ds, "updateStrategy")
			rolling := clusterSSHStorageMap(strategy, "rollingUpdate")
			switch mode {
			case "template node":
				spec["nodeName"] = f.a.Source.Members[0].Name
			case "template affinity":
				spec["affinity"] = ps["affinity"]
			case "template tolerations":
				spec["tolerations"] = ps["tolerations"]
			case "Pod affinity missing":
				delete(ps, "affinity")
			case "Pod affinity foreign":
				ps["affinity"] = sshKubeVIPAffinity("other")
			case "Pod affinity extra":
				clusterSSHStorageMap(ps, "affinity")["podAntiAffinity"] = map[string]any{}
			case "Pod priority missing":
				delete(ps, "priority")
			case "Pod toleration missing":
				ps["tolerations"] = ps["tolerations"].([]any)[:5]
			case "Pod toleration duplicate":
				ps["tolerations"].([]any)[0] = ps["tolerations"].([]any)[1]
			case "Pod toleration expiry":
				ps["tolerations"].([]any)[0].(map[string]any)["tolerationSeconds"] = 300
			case "Pod toleration network":
				ps["tolerations"] = sshKubeVIPTolerations()
			case "strategy missing":
				delete(ds, "updateStrategy")
			case "strategy custom":
				strategy["type"] = "OnDelete"
			case "strategy extra":
				strategy["other"] = true
			case "surge":
				rolling["maxSurge"] = 1
			case "surge malformed":
				rolling["maxSurge"] = "0%"
			case "unavailable":
				rolling["maxUnavailable"] = 1
			}
			if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("unsupported placement accepted", err)
			}
		})
	}
}

func TestClusterSSHLonghornManagerResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"manager desired", "prepull desired", "manager Pod", "prepull Pod", "manager coherent", "prepull coherent", "placement"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				index := 0
				if strings.HasPrefix(mode, "prepull") {
					index = 1
				}
				if strings.HasSuffix(mode, "desired") || strings.HasSuffix(mode, "coherent") {
					c := sshLonghornManagerSpec(f)["containers"].([]any)[index].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "64"
				}
				if strings.HasSuffix(mode, "Pod") || strings.HasSuffix(mode, "coherent") {
					c := clusterSSHStorageMap(sshLonghornManagerPod(f, 0), "spec")["containers"].([]any)[index].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "64"
				}
				if mode == "placement" {
					sshLonghornManagerSpec(f)["priorityClassName"] = "other"
				}
				f.mu.Unlock()
				return checks.Inputs(ctx)
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("resource drift escaped", err)
			}
		})
	}
}

func TestClusterSSHLonghornManagerResourceProducerParity(t *testing.T) {
	// Exercise the existing producer with distinct synthetic operator inputs.
	// This minimal pinned layout is offline; full upstream parity uses cached lock evidence.
	raw, err := exec.Command("python3", "-c", `import json,pathlib,runpy,sys,yaml
m=runpy.run_path(str(pathlib.Path('../../../../../../Data/Engine/K3s/cluster/render-system-upgrade.py')))
source='''apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: longhorn-manager
  namespace: longhorn-system
spec:
  updateStrategy:
    rollingUpdate:
      maxUnavailable: 100%
  template:
    spec:
      priorityClassName: longhorn-critical
      containers:
      - name: longhorn-manager
      - name: pre-pull-share-manager-image
'''
print(json.dumps(yaml.safe_load(m['render_longhorn_manager'](source,sys.argv[1],sys.argv[2]))))`,
		`{"requests":{"cpu":"0.15","memory":"96Mi"},"limits":{"cpu":"0.4","memory":"192Mi"}}`,
		`{"requests":{"cpu":"5m","memory":"8Mi"},"limits":{"cpu":"25m","memory":"24Mi"}}`).Output()
	object, _, parseErr := clusterSSHStorageObject(raw)
	if err != nil || parseErr != nil {
		t.Fatal("producer output", err, parseErr)
	}
	got, ok := clusterSSHLonghornManagerResourcePlan(object)
	if !ok || got != sshLonghornManagerResourceValue(t) {
		t.Fatal("producer/consumer resource or placement mismatch")
	}
}

func TestClusterSSHLonghornManagerResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing manager", "missing prepull", "swapped roles", "zero request", "request above cap", "CPU overflow", "memory overflow", "subtotal overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshLonghornManagerResourceValue(t)
			switch mode {
			case "missing manager":
				v.Manager = clusterSSHApplicationResource{}
			case "missing prepull":
				v.PrePull = clusterSSHApplicationResource{}
			case "swapped roles":
				v.Manager, v.PrePull = v.PrePull, v.Manager
			case "zero request":
				v.PrePull.CPURequestMilli = 0
			case "request above cap":
				v.Manager.MemoryRequestBytes = v.Manager.MemoryLimitBytes + 1
			case "CPU overflow":
				v.Manager.CPURequestMilli, v.Manager.CPULimitMilli = clusterSSHCapacityLimit, clusterSSHCapacityLimit
			case "memory overflow":
				v.PrePull.MemoryLimitBytes = clusterSSHCapacityLimit
			case "subtotal overflow":
				v.PrePull.MemoryLimitBytes = clusterSSHCapacityLimit - v.Manager.MemoryLimitBytes
				f := newSSHStorageFixture(t, false)
				got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
				if err != nil {
					t.Fatal(err)
				}
				got.Requirements.LonghornManagerResources = v
				if _, _, ok := clusterSSHNodeWorkloadBudget(got.Requirements); ok {
					t.Fatal("subtotal overflow accepted")
				}
				return
			}
			if _, _, ok := v.budget(); ok {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}
