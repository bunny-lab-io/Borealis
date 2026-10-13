package main

import "encoding/json"

// Recreate controller demand on a joining control-plane Engine.
// Charge the whole possible set to each target, without assuming distribution.
type clusterSSHSystemUpgradeResources struct {
	Envelope clusterSSHApplicationResource `json:"envelope"`
	Replicas uint64                        `json:"replicas"`
	Observed uint64                        `json:"observed"`
}

func (v clusterSSHSystemUpgradeResources) budget() (cpu, memory uint64, ok bool) {
	if v.Envelope.Name != "system-upgrade-controller" || !clusterSSHWorkloadResourceValid(v.Envelope) || v.Replicas == 0 || v.Replicas > clusterSSHLonghornDeploymentPodLimit || v.Observed == 0 || v.Observed > clusterSSHLonghornDeploymentPodLimit {
		return 0, 0, false
	}
	for n := uint64(0); n < max(v.Replicas, v.Observed); n++ {
		cpu, ok = clusterSSHCapacitySum(cpu, v.Envelope.CPURequestMilli)
		if !ok {
			return 0, 0, false
		}
		memory, ok = clusterSSHCapacitySum(memory, v.Envelope.MemoryLimitBytes)
		if !ok {
			return 0, 0, false
		}
	}
	return cpu, memory, true
}

// Support pinned v0.20.1 placement plus ordinary admission defaults.
// Added hard placement, scheduling or Pod resource contracts need separate proof.
func clusterSSHSystemUpgradeResourceSpec(spec map[string]any, pod bool) (clusterSSHApplicationResource, bool) {
	if !clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHSystemUpgradeAffinity(spec["affinity"]) ||
		!clusterSSHStorageEmptyList(spec["topologySpreadConstraints"]) || !clusterSSHStorageEmptyList(spec["schedulingGates"]) ||
		!clusterSSHStorageEmptyMap(spec["resources"]) || !clusterSSHStorageEmptyMap(spec["overhead"]) ||
		!clusterSSHStorageEmptyList(spec["resourceClaims"]) || !clusterSSHStorageEmptyText(spec["runtimeClassName"]) ||
		!clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) ||
		!clusterSSHStorageEmptyText(spec["priorityClassName"]) ||
		(!clusterSSHStorageEmptyText(spec["schedulerName"]) && spec["schedulerName"] != "default-scheduler") ||
		(!clusterSSHStorageEmptyText(spec["preemptionPolicy"]) && spec["preemptionPolicy"] != "PreemptLowerPriority") ||
		(!pod && !clusterSSHStorageEmptyText(spec["nodeName"])) {
		return clusterSSHApplicationResource{}, false
	}
	if raw, present := spec["priority"]; present {
		value, ok := clusterSSHStorageInteger(raw)
		if !ok || value != 0 {
			return clusterSSHApplicationResource{}, false
		}
	}
	if !clusterSSHSystemUpgradeTolerations(spec["tolerations"], pod) {
		return clusterSSHApplicationResource{}, false
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 {
		return clusterSSHApplicationResource{}, false
	}
	c, ok := containers[0].(map[string]any)
	if !ok || c["name"] != "system-upgrade-controller" || !clusterSSHStorageEmptyText(c["restartPolicy"]) || !clusterSSHStorageEmptyList(c["resizePolicy"]) {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources("system-upgrade-controller", c["resources"])
}

func clusterSSHSystemUpgradeResourcePlan(object map[string]any) (clusterSSHSystemUpgradeResources, bool) {
	spec := clusterSSHStorageMap(object, "spec")
	replicas, ok := clusterSSHStorageInteger(spec["replicas"])
	strategy := clusterSSHStorageMap(spec, "strategy")
	if !ok || replicas < 1 || replicas > clusterSSHLonghornDeploymentPodLimit || len(strategy) != 1 || strategy["type"] != "Recreate" {
		return clusterSSHSystemUpgradeResources{}, false
	}
	envelope, ok := clusterSSHSystemUpgradeResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), false)
	return clusterSSHSystemUpgradeResources{Envelope: envelope, Replicas: uint64(replicas)}, ok
}

// Match the pinned required node and Pod anti-affinity shape. No distribution
// credit is taken; this does not prove target labels or scheduling feasibility.
func clusterSSHSystemUpgradeAffinity(raw any) bool {
	canonical, err := json.Marshal(raw)
	return err == nil && string(canonical) == `{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"node-role.kubernetes.io/control-plane","operator":"Exists"},{"key":"kubernetes.io/os","operator":"In","values":["linux"]}]}]}},"podAntiAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":[{"labelSelector":{"matchExpressions":[{"key":"app.kubernetes.io/name","operator":"In","values":["system-upgrade-controller"]}]},"topologyKey":"kubernetes.io/hostname"}]}}`
}

func clusterSSHSystemUpgradeTolerations(raw any, pod bool) bool {
	values, ok := raw.([]any)
	if !ok || len(values) < 5 || len(values) > 7 {
		return false
	}
	wanted := map[string]string{"CriticalAddonsOnly": "", "node-role.kubernetes.io/master": "NoSchedule", "node-role.kubernetes.io/controlplane": "NoSchedule", "node-role.kubernetes.io/control-plane": "NoSchedule", "node-role.kubernetes.io/etcd": "NoExecute"}
	seen := map[string]bool{}
	for _, raw := range values {
		t, ok := raw.(map[string]any)
		key := clusterSSHStorageText(t, "key")
		if !ok || seen[key] || t["operator"] != "Exists" || !clusterSSHStorageEmptyText(t["value"]) {
			return false
		}
		seen[key] = true
		for field := range t {
			if !textInSet(field, "key", "operator", "value", "effect", "tolerationSeconds") {
				return false
			}
		}
		if effect, known := wanted[key]; known {
			if clusterSSHStorageText(t, "effect") != effect || (effect == "" && !clusterSSHStorageEmptyText(t["effect"])) || t["tolerationSeconds"] != nil {
				return false
			}
			delete(wanted, key)
		} else {
			seconds, valid := clusterSSHStorageInteger(t["tolerationSeconds"])
			if !pod || !textInSet(key, "node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable") || t["effect"] != "NoExecute" || !valid || seconds != 300 {
				return false
			}
		}
	}
	return len(wanted) == 0
}
