package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func sshLonghornResizerResources() map[string]any {
	return map[string]any{"requests": map[string]any{"cpu": "50m", "memory": "40Mi"}, "limits": map[string]any{"cpu": "200m", "memory": "120Mi"}}
}
func sshLonghornResizerResourceAffinity() map[string]any {
	return map[string]any{"podAntiAffinity": map[string]any{"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{"weight": 1, "podAffinityTerm": map[string]any{"topologyKey": "kubernetes.io/hostname", "labelSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "In", "values": []any{"csi-resizer"}}}}}}}}}
}
func sshLonghornResizerResourceValue(t *testing.T) clusterSSHLonghornResizerResources {
	t.Helper()
	plan, ok := clusterSSHLonghornResizerResourcePlan(sshLonghornCSIDeploymentFixture(clusterSSHLonghornResizer))
	if !ok {
		t.Fatal("invalid resizer producer fixture")
	}
	plan.Observed = 1
	return plan
}
func sshLonghornResizerSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornResizer.deploymentPath()], "spec"), "template"), "spec")
}
func sshLonghornResizerOwnerSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-resizer-abcdef"], "spec"), "template"), "spec")
}
func TestClusterSSHLonghornResizerResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "current allocation", "missing bounds", "Pod bounds differ", "owner bounds differ", "allocated differs", "runtime differs", "allocation status", "resize pending", "resize condition", "missing strategy", "missing unavailable", "missing replicas", "zero replicas", "negative replicas", "excess replicas", "custom strategy", "custom surge", "custom unavailable", "malformed strategy", "malformed surge", "unknown strategy", "request above cap", "extra resource"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			ds := clusterSSHStorageMap(f.objects[clusterSSHLonghornResizer.deploymentPath()], "spec")
			spec := sshLonghornResizerSpec(f)
			pod := sshLonghornResizerPod(f, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			runtime := sshLonghornResizerRuntime(f, 0)
			rs := sshLonghornResizerOwnerSpec(f)
			wanted := sshLonghornResizerResourceValue(t)
			if mode == "replacement" {
				wanted.Observed = 2
			}
			switch mode {
			case "five replicas":
				ds["replicas"] = 5
				wanted.Replicas = 5
				wanted.Surge = 2
			case "API defaults":
				ds["strategy"] = map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": "25%", "maxUnavailable": 1}}
				ps["schedulerName"], ps["preemptionPolicy"] = "default-scheduler", "PreemptLowerPriority"
			case "admitted tolerations":
				ps["tolerations"] = []any{map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}, map[string]any{"key": "node.kubernetes.io/unreachable", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}}
			case "observed excess":
				list := f.objects[clusterSSHLonghornResizer.podsPath(f.a.Source.Members[0].Name)]
				for _, name := range []string{"csi-resizer-extra1", "csi-resizer-extra2", "csi-resizer-extra3", "csi-resizer-extra4"} {
					raw, _ := json.Marshal(pod)
					copy, _, err := clusterSSHStorageObject(raw)
					if err != nil {
						t.Fatal(err)
					}
					meta := clusterSSHStorageMap(copy, "metadata")
					meta["name"], meta["uid"] = name, newClusterUUID()
					list["items"] = append(list["items"].([]any), copy)
				}
				wanted.Observed = 5
			case "changed envelope":
				for _, s := range []map[string]any{spec, rs, ps} {
					c := s["containers"].([]any)[0].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["memory"] = "160Mi"
				}
				clusterSSHStorageMap(clusterSSHStorageMap(runtime, "resources"), "limits")["memory"] = "160Mi"
				wanted.Envelope.MemoryLimitBytes = 160 << 20
			case "current allocation":
				runtime["resources"] = sshLonghornResizerResources()
				runtime["allocatedResources"] = map[string]any{"cpu": "50m", "memory": "40Mi"}
			case "missing bounds":
				delete(sshLonghornResizerContainer(f), "resources")
			case "Pod bounds differ":
				clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["memory"] = "160Mi"
			case "owner bounds differ":
				delete(rs["containers"].([]any)[0].(map[string]any), "resources")
			case "allocated differs":
				runtime["allocatedResources"] = map[string]any{"cpu": "51m", "memory": "40Mi"}
			case "runtime differs":
				runtime["resources"] = sshLonghornResizerResources()
				clusterSSHStorageMap(clusterSSHStorageMap(runtime, "resources"), "limits")["memory"] = "160Mi"
			case "allocation status":
				runtime["allocatedResourcesStatus"] = []any{map[string]any{}}
			case "resize pending":
				status["resize"] = "InProgress"
			case "resize condition":
				status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "PodResizePending", "status": "True"})
			case "missing strategy":
				delete(ds, "strategy")
			case "missing unavailable":
				delete(clusterSSHStorageMap(clusterSSHStorageMap(ds, "strategy"), "rollingUpdate"), "maxUnavailable")
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
				clusterSSHStorageMap(clusterSSHStorageMap(ds, "strategy"), "rollingUpdate")["maxSurge"] = "100%"
			case "custom unavailable":
				clusterSSHStorageMap(clusterSSHStorageMap(ds, "strategy"), "rollingUpdate")["maxUnavailable"] = 2
			case "malformed strategy":
				ds["strategy"] = []any{}
			case "malformed surge":
				ds["strategy"] = map[string]any{"rollingUpdate": false}
			case "unknown strategy":
				ds["strategy"] = map[string]any{"other": true}
			case "request above cap":
				clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornResizerContainer(f), "resources"), "requests")["cpu"] = "201m"
			case "extra resource":
				clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornResizerContainer(f), "resources"), "requests")["ephemeral-storage"] = "1Gi"
			}
			before, _ := json.Marshal(f.objects)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := slices.Contains([]string{"expansion", "replacement", "five replicas", "API defaults", "admitted tolerations", "observed excess", "changed envelope", "current allocation"}, mode)
			if (err == nil) != valid || (valid && got.Requirements.LonghornResizerResources != wanted) {
				t.Fatal("unsafe resizer projection", err, got.Requirements.LonghornResizerResources, wanted)
			}
			if valid {
				cpu, memory, ok := got.Requirements.LonghornResizerResources.budget()
				count := max(wanted.Replicas+wanted.Surge, wanted.Observed)
				if !ok || cpu != count*wanted.Envelope.CPURequestMilli || memory != count*wanted.Envelope.MemoryLimitBytes {
					t.Fatal("incorrect per-target resizer demand")
				}
				if f.calls[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-resizer-abcdef"] != 1 {
					t.Fatal("owner reread added")
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHLonghornResizer.podsPath(member.Name)] != 1 {
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

func TestClusterSSHLonghornResizerResourcePlacement(t *testing.T) {
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
		"Pod resources":   func(s map[string]any) { s["resources"] = sshLonghornResizerResources() },
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
				spec := sshLonghornResizerSpec(f)
				if location == "owner" {
					spec = sshLonghornResizerOwnerSpec(f)
				}
				if location == "Pod" {
					spec = clusterSSHStorageMap(sshLonghornResizerPod(f, 0), "spec")
				}
				mutate(spec)
				if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != clusterbootstrap.ErrPreparationConfig {
					t.Fatal("unsupported resizer placement accepted", err)
				}
			})
		}
	}
}

func TestClusterSSHLonghornResizerResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing", "name", "zero request", "request above cap", "replicas", "observed", "empty observed", "surge", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := sshLonghornResizerResourceValue(t)
			switch mode {
			case "missing":
				v = clusterSSHLonghornResizerResources{}
			case "name":
				v.Envelope.Name = "csi-provisioner"
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
				t.Fatal("invalid resizer budget accepted")
			}
		})
	}
}

func TestClusterSSHLonghornResizerResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"desired", "owner", "Pod", "coherent", "replicas", "placement", "observed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				specs := []map[string]any{sshLonghornResizerSpec(f), sshLonghornResizerOwnerSpec(f), clusterSSHStorageMap(sshLonghornResizerPod(f, 0), "spec")}
				for i, s := range specs {
					if mode == "coherent" || (mode == "desired" && i == 0) || (mode == "owner" && i == 1) || (mode == "Pod" && i == 2) {
						c := s["containers"].([]any)[0].(map[string]any)
						clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "201m"
					}
				}
				if mode == "coherent" {
					clusterSSHStorageMap(clusterSSHStorageMap(sshLonghornResizerRuntime(f, 0), "resources"), "limits")["cpu"] = "201m"
				}
				if mode == "replicas" {
					clusterSSHStorageMap(f.objects[clusterSSHLonghornResizer.deploymentPath()], "spec")["replicas"] = 4
				}
				if mode == "placement" {
					specs[0]["priorityClassName"] = "other"
				}
				if mode == "observed" {
					raw, _ := json.Marshal(sshLonghornResizerPod(f, 0))
					p, _, _ := clusterSSHStorageObject(raw)
					m := clusterSSHStorageMap(p, "metadata")
					m["name"], m["uid"] = "csi-resizer-extra", newClusterUUID()
					list := f.objects[clusterSSHLonghornResizer.podsPath(f.a.Source.Members[0].Name)]
					list["items"] = append(list["items"].([]any), p)
				}
				f.mu.Unlock()
				return checks.Inputs(ctx)
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("Resizer drift escaped", err)
			}
		})
	}
}

func sshLonghornResizerContainer(f *sshStorageFixture) map[string]any {
	return sshLonghornCSIContainer(f, clusterSSHLonghornResizer)
}
func sshLonghornResizerPod(f *sshStorageFixture, index int) map[string]any {
	return sshLonghornCSIPod(f, clusterSSHLonghornResizer, index)
}
func sshLonghornResizerRuntime(f *sshStorageFixture, index int) map[string]any {
	return sshLonghornCSIRuntime(f, clusterSSHLonghornResizer, index)
}
