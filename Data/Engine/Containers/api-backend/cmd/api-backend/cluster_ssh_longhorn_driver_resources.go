package main

import "maps"

// The cluster-wide driver Deployment can schedule on any joining Engine.
// Charge the whole possible set to each target, without assuming distribution.
type clusterSSHLonghornDriverResources struct {
	Main     clusterSSHApplicationResource `json:"main"`
	Wait     clusterSSHApplicationResource `json:"wait"`
	Replicas uint64                        `json:"replicas"`
	Observed uint64                        `json:"observed"`
	Surge    uint64                        `json:"surge"`
}

func (v clusterSSHLonghornDriverResources) budget() (cpu, memory uint64, ok bool) {
	if v.Main.Name != "longhorn-driver-deployer" || v.Wait.Name != "wait-longhorn-manager" || !clusterSSHWorkloadResourceValid(v.Main) || !clusterSSHWorkloadResourceValid(v.Wait) || v.Replicas == 0 || v.Replicas > clusterSSHLonghornDriverPodLimit || v.Observed == 0 || v.Observed > clusterSSHLonghornDriverPodLimit || v.Surge != (v.Replicas+3)/4 {
		return 0, 0, false
	}
	for n := uint64(0); n < max(v.Replicas+v.Surge, v.Observed); n++ {
		cpu, ok = clusterSSHCapacitySum(cpu, max(v.Main.CPURequestMilli, v.Wait.CPURequestMilli))
		if !ok {
			return 0, 0, false
		}
		memory, ok = clusterSSHCapacitySum(memory, max(v.Main.MemoryLimitBytes, v.Wait.MemoryLimitBytes))
		if !ok {
			return 0, 0, false
		}
	}
	return cpu, memory, true
}

// Support pinned producer's unconstrained placement, priority and Kubernetes defaults.
// Added hard placement, scheduling or Pod resource contracts need separate proof.
func clusterSSHLonghornDriverResourceSpec(spec map[string]any, pod bool) (clusterSSHApplicationResource, clusterSSHApplicationResource, bool) {
	if !clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHStorageEmptyMap(spec["affinity"]) ||
		spec["serviceAccountName"] != "longhorn-service-account" || (spec["hostNetwork"] != nil && spec["hostNetwork"] != false) ||
		!clusterSSHStorageEmptyList(spec["topologySpreadConstraints"]) || !clusterSSHStorageEmptyList(spec["schedulingGates"]) ||
		!clusterSSHStorageEmptyMap(spec["resources"]) || !clusterSSHStorageEmptyMap(spec["overhead"]) ||
		!clusterSSHStorageEmptyList(spec["resourceClaims"]) || !clusterSSHStorageEmptyText(spec["runtimeClassName"]) ||
		!clusterSSHStorageEmptyList(spec["ephemeralContainers"]) ||
		spec["priorityClassName"] != "longhorn-critical" ||
		(!clusterSSHStorageEmptyText(spec["schedulerName"]) && spec["schedulerName"] != "default-scheduler") ||
		(!clusterSSHStorageEmptyText(spec["preemptionPolicy"]) && spec["preemptionPolicy"] != "PreemptLowerPriority") ||
		(!pod && !clusterSSHStorageEmptyText(spec["nodeName"])) {
		return clusterSSHApplicationResource{}, clusterSSHApplicationResource{}, false
	}
	if raw, present := spec["priority"]; present || pod {
		value, ok := clusterSSHStorageInteger(raw)
		if !ok || value != 1000000000 {
			return clusterSSHApplicationResource{}, clusterSSHApplicationResource{}, false
		}
	}
	if !clusterSSHStorageEmptyList(spec["tolerations"]) {
		tolerations, ok := spec["tolerations"].([]any)
		if !pod || !ok || len(tolerations) > 2 {
			return clusterSSHApplicationResource{}, clusterSSHApplicationResource{}, false
		}
		seen := map[string]bool{}
		for _, raw := range tolerations {
			t, ok := raw.(map[string]any)
			key := clusterSSHStorageText(t, "key")
			seconds, valid := clusterSSHStorageInteger(t["tolerationSeconds"])
			if !ok || len(t) != 4 || !textInSet(key, "node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable") || seen[key] || t["operator"] != "Exists" || t["effect"] != "NoExecute" || !valid || seconds != 300 {
				return clusterSSHApplicationResource{}, clusterSSHApplicationResource{}, false
			}
			seen[key] = true
		}
	}
	values := [2]clusterSSHApplicationResource{}
	for i, kind := range []struct{ field, name string }{{"containers", "longhorn-driver-deployer"}, {"initContainers", "wait-longhorn-manager"}} {
		containers, ok := spec[kind.field].([]any)
		if !ok || len(containers) != 1 {
			return values[0], values[1], false
		}
		c, ok := containers[0].(map[string]any)
		if !ok || c["name"] != kind.name || !clusterSSHStorageEmptyText(c["restartPolicy"]) || !clusterSSHStorageEmptyList(c["resizePolicy"]) {
			return values[0], values[1], false
		}
		values[i], ok = clusterSSHWorkloadResources(kind.name, c["resources"])
		if !ok {
			return values[0], values[1], false
		}
	}
	return values[0], values[1], true
}

func clusterSSHLonghornDriverResourcePlan(object map[string]any) (clusterSSHLonghornDriverResources, bool) {
	spec := clusterSSHStorageMap(object, "spec")
	replicas, ok := clusterSSHStorageInteger(spec["replicas"])
	if !ok || replicas < 1 || replicas > clusterSSHLonghornDriverPodLimit {
		return clusterSSHLonghornDriverResources{}, false
	}
	// Pinned manifest omits strategy; API defaults it to RollingUpdate with
	// 25% surge (rounded up) and 25% unavailable. Reject unreviewed strategies.
	if raw, present := spec["strategy"]; present {
		strategy, ok := raw.(map[string]any)
		if !ok || len(strategy) > 2 || (!clusterSSHStorageEmptyText(strategy["type"]) && strategy["type"] != "RollingUpdate") {
			return clusterSSHLonghornDriverResources{}, false
		}
		for key := range strategy {
			if key != "type" && key != "rollingUpdate" {
				return clusterSSHLonghornDriverResources{}, false
			}
		}
		if raw, present := strategy["rollingUpdate"]; present {
			rolling, ok := raw.(map[string]any)
			if !ok || len(rolling) > 2 {
				return clusterSSHLonghornDriverResources{}, false
			}
			for key, value := range rolling {
				if (key != "maxSurge" && key != "maxUnavailable") || value != "25%" {
					return clusterSSHLonghornDriverResources{}, false
				}
			}
		}
	}
	main, wait, ok := clusterSSHLonghornDriverResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), false)
	return clusterSSHLonghornDriverResources{Main: main, Wait: wait, Replicas: uint64(replicas), Surge: uint64((replicas + 3) / 4)}, ok
}

// Main and ordinary init run sequentially. Validate their distinct envelopes
// and kubelet allocation evidence through immutable views, retaining full receipts.
// Existing runtime observation separately requires successful init termination.
func clusterSSHLonghornDriverPodResources(v clusterSSHLonghornDriverResources, pod map[string]any) bool {
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	main, wait, ok := clusterSSHLonghornDriverResourceSpec(spec, true)
	if !ok || main != v.Main || wait != v.Wait {
		return false
	}
	for _, kind := range []struct {
		spec, status string
		envelope     clusterSSHApplicationResource
	}{
		{"containers", "containerStatuses", v.Main}, {"initContainers", "initContainerStatuses", v.Wait},
	} {
		view, podSpec, podStatus := maps.Clone(pod), maps.Clone(spec), maps.Clone(status)
		podSpec["containers"], podStatus["containerStatuses"] = spec[kind.spec], status[kind.status]
		delete(podSpec, "initContainers")
		delete(podStatus, "initContainerStatuses")
		view["spec"], view["status"] = podSpec, podStatus
		if !clusterSSHBoundedPodResources(kind.envelope, view, kind.envelope.Name, "") {
			return false
		}
	}
	return true
}
