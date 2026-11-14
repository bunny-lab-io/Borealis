package main

import "maps"

// Both containers are ordinary, persistent containers in the pinned DaemonSet.
// Every target receives one Pod, independent of the active source count. This
// subtotal does not bound terminating-Pod overlap or other Longhorn workloads.
type clusterSSHLonghornManagerResources struct {
	Manager clusterSSHApplicationResource `json:"manager"`
	PrePull clusterSSHApplicationResource `json:"pre_pull"`
}

func (v clusterSSHLonghornManagerResources) budget() (cpu, memory uint64, ok bool) {
	if v.Manager.Name != "longhorn-manager" || v.PrePull.Name != "pre-pull-share-manager-image" ||
		!clusterSSHWorkloadResourceValid(v.Manager) || !clusterSSHWorkloadResourceValid(v.PrePull) {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(v.Manager.CPURequestMilli, v.PrePull.CPURequestMilli)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(v.Manager.MemoryLimitBytes, v.PrePull.MemoryLimitBytes)
	return cpu, memory, ok
}

func clusterSSHLonghornManagerResourcePlan(object map[string]any) (clusterSSHLonghornManagerResources, bool) {
	fail := func() (clusterSSHLonghornManagerResources, bool) { return clusterSSHLonghornManagerResources{}, false }
	spec := clusterSSHStorageMap(object, "spec")
	strategy := clusterSSHStorageMap(spec, "updateStrategy")
	rolling := clusterSSHStorageMap(strategy, "rollingUpdate")
	if len(strategy) < 1 || len(strategy) > 2 || len(rolling) < 1 || len(rolling) > 2 || rolling["maxUnavailable"] != "100%" {
		return fail()
	}
	for key, raw := range strategy {
		if key != "rollingUpdate" && (key != "type" || raw != "RollingUpdate") {
			return fail()
		}
	}
	for key, raw := range rolling {
		if key == "maxUnavailable" {
			continue
		}
		surge, ok := clusterSSHStorageInteger(raw)
		if key != "maxSurge" || !ok || surge != 0 {
			return fail()
		}
	}
	return clusterSSHLonghornManagerResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), "")
}

func clusterSSHLonghornManagerResourceSpec(spec map[string]any, node string) (clusterSSHLonghornManagerResources, bool) {
	fail := func() (clusterSSHLonghornManagerResources, bool) { return clusterSSHLonghornManagerResources{}, false }
	if !clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHStorageEmptyList(spec["topologySpreadConstraints"]) ||
		!clusterSSHStorageEmptyList(spec["schedulingGates"]) || !clusterSSHStorageEmptyMap(spec["resources"]) ||
		!clusterSSHStorageEmptyMap(spec["overhead"]) || !clusterSSHStorageEmptyList(spec["resourceClaims"]) ||
		!clusterSSHStorageEmptyText(spec["runtimeClassName"]) || !clusterSSHStorageEmptyList(spec["initContainers"]) ||
		!clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || spec["priorityClassName"] != "longhorn-critical" ||
		(spec["hostNetwork"] != nil && spec["hostNetwork"] != false) ||
		(!clusterSSHStorageEmptyText(spec["schedulerName"]) && spec["schedulerName"] != "default-scheduler") ||
		(!clusterSSHStorageEmptyText(spec["preemptionPolicy"]) && spec["preemptionPolicy"] != "PreemptLowerPriority") {
		return fail()
	}
	if raw, present := spec["priority"]; present || node != "" {
		priority, ok := clusterSSHStorageInteger(raw)
		if !ok || priority != 1000000000 {
			return fail()
		}
	}
	if node == "" {
		if !clusterSSHStorageEmptyText(spec["nodeName"]) || !clusterSSHStorageEmptyMap(spec["affinity"]) || !clusterSSHStorageEmptyList(spec["tolerations"]) {
			return fail()
		}
	} else if spec["nodeName"] != node || !clusterSSHDaemonSetNodeAffinity(spec["affinity"], node) || !clusterSSHDaemonSetTolerations(spec["tolerations"], false) {
		return fail()
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 2 {
		return fail()
	}
	v := clusterSSHLonghornManagerResources{}
	seen := map[string]bool{}
	for _, raw := range containers {
		container, ok := raw.(map[string]any)
		name := clusterSSHStorageText(container, "name")
		if !ok || seen[name] || !clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyList(container["resizePolicy"]) {
			return fail()
		}
		seen[name] = true
		envelope, ok := clusterSSHWorkloadResources(name, container["resources"])
		if !ok {
			return fail()
		}
		switch name {
		case "longhorn-manager":
			v.Manager = envelope
		case "pre-pull-share-manager-image":
			v.PrePull = envelope
		default:
			return fail()
		}
	}
	_, _, ok = v.budget()
	return v, ok
}

// Validate the complete two-container set first, then apply the shared resize
// and allocation checks to immutable single-container views. Never rewrite the
// source object: its full receipt must still detect all configuration drift.
func clusterSSHLonghornManagerPodResources(v clusterSSHLonghornManagerResources, pod map[string]any, node string) bool {
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	observed, ok := clusterSSHLonghornManagerResourceSpec(spec, node)
	if !ok || observed != v {
		return false
	}
	statuses, ok := status["containerStatuses"].([]any)
	if !ok || len(statuses) != 2 {
		return false
	}
	byName := map[string]map[string]any{}
	for _, raw := range statuses {
		runtime, ok := raw.(map[string]any)
		name := clusterSSHStorageText(runtime, "name")
		if !ok || byName[name] != nil || (name != v.Manager.Name && name != v.PrePull.Name) {
			return false
		}
		byName[name] = runtime
	}
	for _, raw := range spec["containers"].([]any) {
		container := raw.(map[string]any)
		envelope := v.Manager
		if container["name"] == v.PrePull.Name {
			envelope = v.PrePull
		}
		view, podSpec, podStatus := maps.Clone(pod), maps.Clone(spec), maps.Clone(status)
		podSpec["containers"], podStatus["containerStatuses"] = []any{container}, []any{byName[envelope.Name]}
		view["spec"], view["status"] = podSpec, podStatus
		if !clusterSSHBoundedPodResources(envelope, view, envelope.Name, "") {
			return false
		}
	}
	return true
}
