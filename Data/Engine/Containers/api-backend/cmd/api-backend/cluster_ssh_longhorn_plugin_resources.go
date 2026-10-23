package main

import "maps"

// Three ordinary persistent containers contribute once per joining target.
// Active-source count does not multiply demand; terminating overlap remains open.
type clusterSSHLonghornPluginResources struct {
	Plugin    clusterSSHApplicationResource `json:"plugin"`
	Registrar clusterSSHApplicationResource `json:"registrar"`
	Liveness  clusterSSHApplicationResource `json:"liveness"`
}

func (v clusterSSHLonghornPluginResources) budget() (cpu, memory uint64, ok bool) {
	if v.Plugin.Name != "longhorn-csi-plugin" || v.Registrar.Name != "node-driver-registrar" || v.Liveness.Name != "longhorn-liveness-probe" {
		return 0, 0, false
	}
	for _, envelope := range []clusterSSHApplicationResource{v.Plugin, v.Registrar, v.Liveness} {
		if !clusterSSHWorkloadResourceValid(envelope) {
			return 0, 0, false
		}
		cpu, ok = clusterSSHCapacitySum(cpu, envelope.CPURequestMilli)
		if !ok {
			return 0, 0, false
		}
		memory, ok = clusterSSHCapacitySum(memory, envelope.MemoryLimitBytes)
		if !ok {
			return 0, 0, false
		}
	}
	return cpu, memory, true
}

func clusterSSHLonghornPluginResourcePlan(object map[string]any) (clusterSSHLonghornPluginResources, bool) {
	fail := func() (clusterSSHLonghornPluginResources, bool) { return clusterSSHLonghornPluginResources{}, false }
	spec := clusterSSHStorageMap(object, "spec")
	strategy := clusterSSHStorageMap(spec, "updateStrategy")
	rolling := clusterSSHStorageMap(strategy, "rollingUpdate")
	unavailable, valid := clusterSSHStorageInteger(rolling["maxUnavailable"])
	if len(strategy) != 2 || strategy["type"] != "RollingUpdate" || len(rolling) < 1 || len(rolling) > 2 || !valid || unavailable != 1 {
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
	return clusterSSHLonghornPluginResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), "")
}

func clusterSSHLonghornPluginResourceSpec(spec map[string]any, node string) (clusterSSHLonghornPluginResources, bool) {
	fail := func() (clusterSSHLonghornPluginResources, bool) { return clusterSSHLonghornPluginResources{}, false }
	if spec["serviceAccountName"] != "longhorn-service-account" || !clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHStorageEmptyList(spec["topologySpreadConstraints"]) ||
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
	if !ok || len(containers) != 3 {
		return fail()
	}
	v := clusterSSHLonghornPluginResources{}
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
		case "longhorn-csi-plugin":
			v.Plugin = envelope
		case "node-driver-registrar":
			v.Registrar = envelope
		case "longhorn-liveness-probe":
			v.Liveness = envelope
		default:
			return fail()
		}
	}
	_, _, ok = v.budget()
	return v, ok
}

// Validate the complete three-container set first, then apply the shared resize
// and allocation checks to immutable single-container views. Never rewrite the
// source object: its full receipt must still detect all configuration drift.
func clusterSSHLonghornPluginPodResources(v clusterSSHLonghornPluginResources, pod map[string]any, node string) bool {
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	observed, ok := clusterSSHLonghornPluginResourceSpec(spec, node)
	if !ok || observed != v {
		return false
	}
	statuses, ok := status["containerStatuses"].([]any)
	if !ok || len(statuses) != 3 {
		return false
	}
	byName := map[string]map[string]any{}
	for _, raw := range statuses {
		runtime, ok := raw.(map[string]any)
		name := clusterSSHStorageText(runtime, "name")
		if !ok || byName[name] != nil || (name != v.Plugin.Name && name != v.Registrar.Name && name != v.Liveness.Name) {
			return false
		}
		byName[name] = runtime
	}
	for _, raw := range spec["containers"].([]any) {
		container := raw.(map[string]any)
		envelope := v.Plugin
		if container["name"] == v.Registrar.Name {
			envelope = v.Registrar
		} else if container["name"] == v.Liveness.Name {
			envelope = v.Liveness
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
