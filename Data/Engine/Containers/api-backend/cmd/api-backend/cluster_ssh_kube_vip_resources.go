package main

import "encoding/json"

// Each admitted Engine gets one kube-vip Pod under the fixed producer selectors.
// Zero rollout surge does not prove a bound on terminating-Pod overlap.
func (v clusterSSHKubeVIPConfiguration) budget() (cpu, memory uint64, ok bool) {
	if v.Resources.Name != "kube-vip" || !clusterSSHWorkloadResourceValid(v.Resources) {
		return 0, 0, false
	}
	return v.Resources.CPURequestMilli, v.Resources.MemoryLimitBytes, true
}

func clusterSSHKubeVIPResourcePlan(object map[string]any) (clusterSSHApplicationResource, bool) {
	spec := clusterSSHStorageMap(object, "spec")
	strategy := clusterSSHStorageMap(spec, "updateStrategy")
	rolling := clusterSSHStorageMap(strategy, "rollingUpdate")
	unavailable, ok := clusterSSHStorageInteger(rolling["maxUnavailable"])
	if len(strategy) != 2 || strategy["type"] != "RollingUpdate" || !ok || unavailable != 1 || len(rolling) < 1 || len(rolling) > 2 {
		return clusterSSHApplicationResource{}, false
	}
	for key, raw := range rolling {
		if key == "maxUnavailable" {
			continue
		}
		surge, valid := clusterSSHStorageInteger(raw)
		if key != "maxSurge" || !valid || surge != 0 {
			return clusterSSHApplicationResource{}, false
		}
	}
	return clusterSSHKubeVIPResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), "")
}

// Empty node means desired template. Pods require the controller-injected
// single-node affinity and only its standard host-network DaemonSet tolerations.
func clusterSSHKubeVIPResourceSpec(spec map[string]any, node string) (clusterSSHApplicationResource, bool) {
	selector := clusterSSHStorageMap(spec, "nodeSelector")
	if len(selector) != 3 || selector["borealis.io/engine-node"] != "true" || selector["borealis.io/control-plane-eligible"] != "true" || selector["borealis.io/edge-eligible"] != "true" ||
		!clusterSSHStorageEmptyList(spec["topologySpreadConstraints"]) || !clusterSSHStorageEmptyList(spec["schedulingGates"]) ||
		!clusterSSHStorageEmptyMap(spec["resources"]) || !clusterSSHStorageEmptyMap(spec["overhead"]) ||
		!clusterSSHStorageEmptyList(spec["resourceClaims"]) || !clusterSSHStorageEmptyText(spec["runtimeClassName"]) ||
		!clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) ||
		!clusterSSHStorageEmptyText(spec["priorityClassName"]) ||
		(!clusterSSHStorageEmptyText(spec["schedulerName"]) && spec["schedulerName"] != "default-scheduler") ||
		(!clusterSSHStorageEmptyText(spec["preemptionPolicy"]) && spec["preemptionPolicy"] != "PreemptLowerPriority") {
		return clusterSSHApplicationResource{}, false
	}
	if raw, present := spec["priority"]; present {
		priority, ok := clusterSSHStorageInteger(raw)
		if !ok || priority != 0 {
			return clusterSSHApplicationResource{}, false
		}
	}
	if node == "" {
		if !clusterSSHStorageEmptyText(spec["nodeName"]) || !clusterSSHStorageEmptyMap(spec["affinity"]) || !clusterSSHStorageEmptyList(spec["tolerations"]) {
			return clusterSSHApplicationResource{}, false
		}
	} else {
		if !clusterSSHStorageName(node) || spec["nodeName"] != node || !clusterSSHKubeVIPNodeAffinity(spec["affinity"], node) || !clusterSSHKubeVIPTolerations(spec["tolerations"]) {
			return clusterSSHApplicationResource{}, false
		}
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 {
		return clusterSSHApplicationResource{}, false
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "kube-vip" || !clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyList(container["resizePolicy"]) {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources("kube-vip", container["resources"])
}

func clusterSSHKubeVIPNodeAffinity(raw any, node string) bool {
	if !clusterSSHStorageName(node) {
		return false
	}
	canonical, err := json.Marshal(raw)
	name, _ := json.Marshal(node)
	return err == nil && string(canonical) == `{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchFields":[{"key":"metadata.name","operator":"In","values":[`+string(name)+`]}]}]}}}`
}

func clusterSSHKubeVIPTolerations(raw any) bool {
	values, ok := raw.([]any)
	if !ok || len(values) != 7 {
		return false
	}
	seen := map[string]bool{}
	for _, raw := range values {
		t, ok := raw.(map[string]any)
		key := clusterSSHStorageText(t, "key")
		effect := "NoSchedule"
		switch key {
		case "node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable":
			effect = "NoExecute"
		case "node.kubernetes.io/disk-pressure", "node.kubernetes.io/memory-pressure", "node.kubernetes.io/pid-pressure", "node.kubernetes.io/unschedulable", "node.kubernetes.io/network-unavailable":
		default:
			return false
		}
		if !ok || len(t) != 3 || seen[key] || t["operator"] != "Exists" || t["effect"] != effect {
			return false
		}
		seen[key] = true
	}
	return true
}
