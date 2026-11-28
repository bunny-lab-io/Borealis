package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func sshLonghornPluginResources(name string) map[string]any {
	cpu, mem, capCPU, capMem := "80m", "64Mi", "300m", "128Mi"
	if name == "node-driver-registrar" {
		cpu, mem, capCPU, capMem = "10m", "8Mi", "40m", "24Mi"
	}
	if name == "longhorn-liveness-probe" {
		cpu, mem, capCPU, capMem = "5m", "4Mi", "20m", "16Mi"
	}
	return map[string]any{"requests": map[string]any{"cpu": cpu, "memory": mem}, "limits": map[string]any{"cpu": capCPU, "memory": capMem}}
}
func sshLonghornPluginResourceValue(t *testing.T) clusterSSHLonghornPluginResources {
	t.Helper()
	v := clusterSSHLonghornPluginResources{}
	for _, item := range []struct {
		name string
		out  *clusterSSHApplicationResource
	}{{"longhorn-csi-plugin", &v.Plugin}, {"node-driver-registrar", &v.Registrar}, {"longhorn-liveness-probe", &v.Liveness}} {
		value, ok := clusterSSHWorkloadResources(item.name, sshLonghornPluginResources(item.name))
		if !ok {
			t.Fatal("invalid fixture")
		}
		*item.out = value
	}
	return v
}
func sshLonghornPluginResourceSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornPluginPath], "spec"), "template"), "spec")
}

func TestClusterSSHLonghornPluginResources(t *testing.T) {
	for _, role := range []string{"longhorn-csi-plugin", "node-driver-registrar", "longhorn-liveness-probe"} {
		for _, mode := range []string{"expansion", "replacement", "reordered", "duplicate container", "duplicate status", "API defaults", "current allocation", "changed envelope", "missing bounds", "missing Pod bounds", "Pod bounds differ", "runtime differs", "allocated differs", "allocation status", "resize pending", "resize condition", "request above cap", "extra resource", "resize policy", "restart policy", "CPU sum overflow", "memory sum overflow"} {
			t.Run(role+"/"+mode, func(t *testing.T) {
				f := newSSHStorageFixture(t, mode == "replacement")
				index := 0
				if role == "node-driver-registrar" {
					index = 1
				}
				if role == "longhorn-liveness-probe" {
					index = 2
				}
				container := sshLonghornPluginResourceSpec(f)["containers"].([]any)[index].(map[string]any)
				pod := sshLonghornPluginPod(f, 0)
				ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
				pc := ps["containers"].([]any)[index].(map[string]any)
				runtime := status["containerStatuses"].([]any)[index].(map[string]any)
				wanted := sshLonghornPluginResourceValue(t)
				switch mode {
				case "reordered":
					slices.Reverse(sshLonghornPluginResourceSpec(f)["containers"].([]any))
					slices.Reverse(ps["containers"].([]any))
					slices.Reverse(status["containerStatuses"].([]any))
				case "duplicate container":
					ps["containers"].([]any)[1] = ps["containers"].([]any)[0]
				case "duplicate status":
					status["containerStatuses"].([]any)[1] = status["containerStatuses"].([]any)[0]
				case "API defaults":
					strategy := clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornPluginPath], "spec"), "updateStrategy")
					strategy["type"] = "RollingUpdate"
					clusterSSHStorageMap(strategy, "rollingUpdate")["maxSurge"] = 0
					ps["schedulerName"], ps["preemptionPolicy"] = "default-scheduler", "PreemptLowerPriority"
				case "current allocation":
					runtime["resources"] = sshLonghornPluginResources(role)
					runtime["allocatedResources"] = clusterSSHStorageMap(sshLonghornPluginResources(role), "requests")
				case "changed envelope":
					bounds := map[string]any{"requests": map[string]any{"cpu": "0.125", "memory": "64Mi"}, "limits": map[string]any{"cpu": "64", "memory": "256Mi"}}
					container["resources"], pc["resources"] = bounds, bounds
					value := clusterSSHApplicationResource{Name: role, CPURequestMilli: 125, CPULimitMilli: 64000, MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20}
					if index == 0 {
						wanted.Plugin = value
					} else if index == 1 {
						wanted.Registrar = value
					} else {
						wanted.Liveness = value
					}
				case "missing bounds":
					delete(container, "resources")
				case "missing Pod bounds":
					delete(pc, "resources")
				case "Pod bounds differ":
					clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["cpu"] = "64"
				case "runtime differs":
					runtime["resources"] = sshLonghornPluginResources(role)
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
				valid := mode == "reordered" || mode == "expansion" || mode == "replacement" || mode == "API defaults" || mode == "current allocation" || mode == "changed envelope"
				if valid {
					if err != nil || got.Requirements.LonghornPluginResources != wanted {
						t.Fatal("lost resource projection", err)
					}
					cpu, memory, ok := got.Requirements.LonghornPluginResources.budget()
					if !ok || cpu != wanted.Plugin.CPURequestMilli+wanted.Registrar.CPURequestMilli+wanted.Liveness.CPURequestMilli || memory != wanted.Plugin.MemoryLimitBytes+wanted.Registrar.MemoryLimitBytes+wanted.Liveness.MemoryLimitBytes {
						t.Fatal("persistent containers not summed once per target")
					}
					podRaw, _ := json.Marshal(pod)
					podView, _, parseErr := clusterSSHStorageObject(podRaw)
					if parseErr != nil || !clusterSSHLonghornPluginPodResources(wanted, podView, f.a.Source.Members[0].Name) {
						t.Fatal("valid resource view rejected", parseErr)
					}
					afterView, _ := json.Marshal(podView)
					if string(podRaw) != string(afterView) {
						t.Fatal("per-container checks rewrote source receipt")
					}
					for _, member := range f.a.Source.Members {
						if f.calls[clusterSSHLonghornPluginPodsPath(member.Name)] != 1 {
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

func TestClusterSSHLonghornPluginResourcePlacement(t *testing.T) {
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
		"Pod resources": func(s map[string]any) { s["resources"] = sshLonghornPluginResources("longhorn-csi-plugin") },
		"claims":        func(s map[string]any) { s["resourceClaims"] = []any{map[string]any{}} },
		"runtime class": func(s map[string]any) { s["runtimeClassName"] = "other" },
	}
	for name, mutate := range mutations {
		for _, location := range []string{"template", "Pod"} {
			t.Run(location+"/"+name, func(t *testing.T) {
				f := newSSHStorageFixture(t, false)
				spec := sshLonghornPluginResourceSpec(f)
				if location == "Pod" {
					spec = clusterSSHStorageMap(sshLonghornPluginPod(f, 0), "spec")
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
			spec := sshLonghornPluginResourceSpec(f)
			ps := clusterSSHStorageMap(sshLonghornPluginPod(f, 0), "spec")
			ds := clusterSSHStorageMap(f.objects[clusterSSHLonghornPluginPath], "spec")
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
				rolling["maxUnavailable"] = 2
			}
			if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("unsupported placement accepted", err)
			}
		})
	}
}

func TestClusterSSHLonghornPluginResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"plugin desired", "registrar desired", "liveness desired", "plugin Pod", "registrar Pod", "liveness Pod", "plugin coherent", "registrar coherent", "liveness coherent", "placement"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				index := 0
				if strings.HasPrefix(mode, "registrar") {
					index = 1
				}
				if strings.HasPrefix(mode, "liveness") {
					index = 2
				}
				if strings.HasSuffix(mode, "desired") || strings.HasSuffix(mode, "coherent") {
					c := sshLonghornPluginResourceSpec(f)["containers"].([]any)[index].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "64"
				}
				if strings.HasSuffix(mode, "Pod") || strings.HasSuffix(mode, "coherent") {
					c := clusterSSHStorageMap(sshLonghornPluginPod(f, 0), "spec")["containers"].([]any)[index].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "64"
				}
				if mode == "placement" {
					sshLonghornPluginResourceSpec(f)["priorityClassName"] = "other"
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

func TestClusterSSHLonghornPluginResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing plugin", "missing registrar", "missing liveness", "swapped liveness", "swapped roles", "zero request", "request above cap", "CPU overflow", "memory overflow", "subtotal overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshLonghornPluginResourceValue(t)
			switch mode {
			case "missing plugin":
				v.Plugin = clusterSSHApplicationResource{}
			case "missing registrar":
				v.Registrar = clusterSSHApplicationResource{}
			case "missing liveness":
				v.Liveness = clusterSSHApplicationResource{}
			case "swapped liveness":
				v.Plugin, v.Liveness = v.Liveness, v.Plugin
			case "swapped roles":
				v.Plugin, v.Registrar = v.Registrar, v.Plugin
			case "zero request":
				v.Registrar.CPURequestMilli = 0
			case "request above cap":
				v.Plugin.MemoryRequestBytes = v.Plugin.MemoryLimitBytes + 1
			case "CPU overflow":
				v.Plugin.CPURequestMilli, v.Plugin.CPULimitMilli = clusterSSHCapacityLimit, clusterSSHCapacityLimit
			case "memory overflow":
				v.Registrar.MemoryLimitBytes = clusterSSHCapacityLimit
			case "subtotal overflow":
				v.Registrar.MemoryLimitBytes = clusterSSHCapacityLimit - v.Plugin.MemoryLimitBytes - v.Liveness.MemoryLimitBytes
				f := newSSHStorageFixture(t, false)
				got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
				if err != nil {
					t.Fatal(err)
				}
				got.Requirements.LonghornPluginResources = v
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
