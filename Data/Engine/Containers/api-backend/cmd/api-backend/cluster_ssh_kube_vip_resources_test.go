package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
)

func sshKubeVIPResources() map[string]any {
	return map[string]any{"requests": map[string]any{"cpu": "25m", "memory": "32Mi"}, "limits": map[string]any{"cpu": "250m", "memory": "128Mi"}}
}
func sshKubeVIPSelector() map[string]any {
	return map[string]any{"borealis.io/engine-node": "true", "borealis.io/control-plane-eligible": "true", "borealis.io/edge-eligible": "true"}
}
func sshKubeVIPAffinity(node string) map[string]any {
	return map[string]any{"nodeAffinity": map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{"nodeSelectorTerms": []any{map[string]any{"matchFields": []any{map[string]any{"key": "metadata.name", "operator": "In", "values": []any{node}}}}}}}}
}
func sshKubeVIPTolerations() []any {
	var values []any
	for _, key := range []string{"not-ready", "unreachable", "disk-pressure", "memory-pressure", "pid-pressure", "unschedulable", "network-unavailable"} {
		effect := "NoSchedule"
		if key == "not-ready" || key == "unreachable" {
			effect = "NoExecute"
		}
		values = append(values, map[string]any{"key": "node.kubernetes.io/" + key, "operator": "Exists", "effect": effect})
	}
	return values
}
func sshKubeVIPResourceValue(t *testing.T) clusterSSHApplicationResource {
	t.Helper()
	v, ok := clusterSSHWorkloadResources("kube-vip", sshKubeVIPResources())
	if !ok {
		t.Fatal("invalid fixture resources")
	}
	return v
}

func TestClusterSSHKubeVIPResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "API defaults", "changed envelope", "current allocation", "missing bounds", "Pod bounds differ", "missing Pod bounds", "allocated differs", "runtime differs", "resize pending", "resize condition", "overhead", "Pod resources", "resource claims", "runtime class", "template node", "missing selector", "extra selector", "Pod selector", "template affinity", "Pod affinity missing", "Pod affinity foreign", "Pod affinity extra", "topology", "scheduler", "priority", "priority class", "preemption", "scheduling gates", "template tolerations", "Pod toleration key", "Pod toleration expiry", "Pod toleration duplicate", "missing strategy", "custom strategy", "surge", "malformed surge", "unavailable", "unknown strategy field", "request above cap", "extra resource", "resize policy"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			ds := clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "spec")
			spec, container := sshKubeVIPSpec(f), sshKubeVIPContainer(f)
			pod := sshKubeVIPPod(f, 0)
			ps, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			pc := ps["containers"].([]any)[0].(map[string]any)
			runtime := sshKubeVIPRuntime(f, 0)
			rolling := clusterSSHStorageMap(clusterSSHStorageMap(ds, "updateStrategy"), "rollingUpdate")
			wanted := sshKubeVIPResourceValue(t)
			switch mode {
			case "API defaults":
				rolling["maxSurge"] = 0
				ps["schedulerName"], ps["priority"], ps["preemptionPolicy"] = "default-scheduler", 0, "PreemptLowerPriority"
			case "changed envelope":
				bounds := map[string]any{"requests": map[string]any{"cpu": "0.125", "memory": "64Mi"}, "limits": map[string]any{"cpu": "64", "memory": "256Mi"}}
				container["resources"], pc["resources"] = bounds, bounds
				wanted = clusterSSHApplicationResource{Name: "kube-vip", CPURequestMilli: 125, CPULimitMilli: 64000, MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20}
			case "current allocation":
				runtime["resources"] = sshKubeVIPResources()
				runtime["allocatedResources"] = map[string]any{"cpu": "25m", "memory": "32Mi"}
			case "missing bounds":
				delete(container, "resources")
			case "missing Pod bounds":
				delete(pc, "resources")
			case "Pod bounds differ":
				clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["cpu"] = "300m"
			case "allocated differs":
				runtime["allocatedResources"] = map[string]any{"cpu": "26m", "memory": "32Mi"}
			case "runtime differs":
				runtime["resources"] = sshKubeVIPResources()
				clusterSSHStorageMap(clusterSSHStorageMap(runtime, "resources"), "limits")["cpu"] = "300m"
			case "resize pending":
				status["resize"] = "InProgress"
			case "resize condition":
				status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "PodResizePending", "status": "True"})
			case "overhead":
				ps["overhead"] = map[string]any{"memory": "1Mi"}
			case "Pod resources":
				ps["resources"] = sshKubeVIPResources()
			case "resource claims":
				spec["resourceClaims"] = []any{map[string]any{"name": "extra"}}
			case "runtime class":
				spec["runtimeClassName"] = "other"
			case "template node":
				spec["nodeName"] = f.a.Source.Members[0].Name
			case "missing selector":
				delete(spec, "nodeSelector")
			case "extra selector":
				clusterSSHStorageMap(spec, "nodeSelector")["disk"] = "ssd"
			case "Pod selector":
				clusterSSHStorageMap(ps, "nodeSelector")["borealis.io/edge-eligible"] = "false"
			case "template affinity":
				spec["affinity"] = sshKubeVIPAffinity(f.a.Source.Members[0].Name)
			case "Pod affinity missing":
				delete(ps, "affinity")
			case "Pod affinity foreign":
				ps["affinity"] = sshKubeVIPAffinity("other")
			case "Pod affinity extra":
				clusterSSHStorageMap(ps, "affinity")["podAntiAffinity"] = map[string]any{}
			case "topology":
				spec["topologySpreadConstraints"] = []any{map[string]any{}}
			case "scheduler":
				ps["schedulerName"] = "other"
			case "priority":
				ps["priority"] = 1
			case "priority class":
				spec["priorityClassName"] = "other"
			case "preemption":
				ps["preemptionPolicy"] = "Never"
			case "scheduling gates":
				ps["schedulingGates"] = []any{map[string]any{"name": "gate"}}
			case "template tolerations":
				spec["tolerations"] = sshKubeVIPTolerations()
			case "Pod toleration key":
				ps["tolerations"].([]any)[0].(map[string]any)["key"] = "other"
			case "Pod toleration expiry":
				ps["tolerations"].([]any)[0].(map[string]any)["tolerationSeconds"] = 300
			case "Pod toleration duplicate":
				ps["tolerations"].([]any)[0] = ps["tolerations"].([]any)[1]
			case "missing strategy":
				delete(ds, "updateStrategy")
			case "custom strategy":
				clusterSSHStorageMap(ds, "updateStrategy")["type"] = "OnDelete"
			case "surge":
				rolling["maxSurge"] = 1
			case "malformed surge":
				rolling["maxSurge"] = "0%"
			case "unavailable":
				rolling["maxUnavailable"] = 2
			case "unknown strategy field":
				clusterSSHStorageMap(ds, "updateStrategy")["extra"] = true
			case "request above cap":
				clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "requests")["cpu"] = "1"
			case "extra resource":
				clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "limits")["ephemeral-storage"] = "1Gi"
			case "resize policy":
				container["resizePolicy"] = []any{map[string]any{"resourceName": "cpu", "restartPolicy": "NotRequired"}}
			}
			before, _ := json.Marshal(f.objects)
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			good := textInSet(mode, "expansion", "replacement", "API defaults", "changed envelope", "current allocation")
			if good {
				if err != nil || value.Requirements.KubeVIP.Resources != wanted {
					t.Fatalf("bounded source rejected: %v", err)
				}
				cpu, memory, ok := value.Requirements.KubeVIP.budget()
				if !ok || cpu != wanted.CPURequestMilli || memory != wanted.MemoryLimitBytes {
					t.Fatal("expected one envelope per target")
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHKubeVIPPodsPath(member.Name)] != 1 {
						t.Fatal("extra or missing source read")
					}
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("unproved envelope accepted: %v", err)
			}
			after, _ := json.Marshal(f.objects)
			if string(before) != string(after) {
				t.Fatal("source evidence mutated")
			}
		})
	}
}

func TestClusterSSHKubeVIPResourceProducerParity(t *testing.T) {
	// Read repository YAML only; never execute a renderer, kubectl or live source.
	raw, err := exec.Command("python3", "-c", `import json,pathlib,yaml
p=pathlib.Path('../../../../../../Data/Engine/K3s/cluster/kube-vip.yaml.in')
d=[v for v in yaml.safe_load_all(p.read_text()) if v['kind']=='DaemonSet']
assert len(d)==1
print(json.dumps(d[0]))`).Output()
	object, _, parseErr := clusterSSHStorageObject(raw)
	if err != nil || parseErr != nil {
		t.Fatal("producer parse", err)
	}
	before, _ := json.Marshal(object)
	value, ok := clusterSSHKubeVIPResourcePlan(object)
	if !ok || value != sshKubeVIPResourceValue(t) {
		t.Fatal("producer envelope or placement drift")
	}
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	if !reflect.DeepEqual(spec["nodeSelector"], sshKubeVIPSelector()) {
		t.Fatal("producer selectors drift")
	}
	after, _ := json.Marshal(object)
	if string(before) != string(after) {
		t.Fatal("producer mutated")
	}
}

func TestClusterSSHKubeVIPResourceScopeDrift(t *testing.T) {
	for _, mode := range []string{"desired", "Pod", "coherent bounds", "placement"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				if mode == "desired" || mode == "coherent bounds" {
					clusterSSHStorageMap(clusterSSHStorageMap(sshKubeVIPContainer(f), "resources"), "limits")["cpu"] = "300m"
				}
				if mode == "Pod" || mode == "coherent bounds" {
					pc := clusterSSHStorageMap(sshKubeVIPPod(f, 0), "spec")["containers"].([]any)[0].(map[string]any)
					clusterSSHStorageMap(clusterSSHStorageMap(pc, "resources"), "limits")["cpu"] = "300m"
				}
				if mode == "placement" {
					clusterSSHStorageMap(sshKubeVIPSpec(f), "nodeSelector")["borealis.io/edge-eligible"] = "false"
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

func TestClusterSSHKubeVIPResourceBudgetRejectsProjection(t *testing.T) {
	for _, mode := range []string{"missing", "name", "zero request", "request above cap", "CPU overflow", "memory overflow", "subtotal overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := clusterSSHKubeVIPConfiguration{Resources: sshKubeVIPResourceValue(t)}
			switch mode {
			case "missing":
				v.Resources = clusterSSHApplicationResource{}
			case "name":
				v.Resources.Name = "postgres"
			case "zero request":
				v.Resources.CPURequestMilli = 0
			case "request above cap":
				v.Resources.MemoryRequestBytes = v.Resources.MemoryLimitBytes + 1
			case "CPU overflow":
				v.Resources.CPULimitMilli = clusterSSHCapacityLimit + 1
			case "memory overflow":
				v.Resources.MemoryLimitBytes = clusterSSHCapacityLimit + 1
			case "subtotal overflow":
				v.Resources.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if mode == "subtotal overflow" {
				f := newSSHStorageFixture(t, false)
				s, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
				if err != nil {
					t.Fatal(err)
				}
				s.Requirements.KubeVIP = v
				if _, _, ok := clusterSSHNodeWorkloadBudget(s.Requirements); ok {
					t.Fatal("overflow accepted")
				}
			} else if _, _, ok := v.budget(); ok {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}
