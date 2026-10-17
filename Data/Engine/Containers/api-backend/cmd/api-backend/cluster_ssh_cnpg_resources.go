package main

// One cluster-wide operator Deployment can schedule on any joining Engine.
// Charge the whole possible set to each target, without assuming distribution.
type clusterSSHCNPGOperatorResources struct {
	Envelope clusterSSHApplicationResource `json:"envelope"`
	Replicas uint64                        `json:"replicas"`
	Observed uint64                        `json:"observed"`
	Surge    uint64                        `json:"surge"`
}

func (v clusterSSHCNPGOperatorResources) budget() (cpu, memory uint64, ok bool) {
	if v.Envelope.Name != "cnpg-operator" || !clusterSSHWorkloadResourceValid(v.Envelope) || v.Replicas == 0 || v.Replicas > clusterSSHLonghornDeploymentPodLimit || v.Observed == 0 || v.Observed > clusterSSHLonghornDeploymentPodLimit || v.Surge != (v.Replicas+3)/4 {
		return 0, 0, false
	}
	for n := uint64(0); n < max(v.Replicas+v.Surge, v.Observed); n++ {
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

// Support pinned producer's unconstrained placement and Kubernetes defaults.
// Added hard placement, scheduling or Pod resource contracts need separate proof.
func clusterSSHCNPGResourceSpec(spec map[string]any, pod bool) (clusterSSHApplicationResource, bool) {
	if !clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHStorageEmptyMap(spec["affinity"]) ||
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
	if !clusterSSHStorageEmptyList(spec["tolerations"]) {
		tolerations, ok := spec["tolerations"].([]any)
		if !pod || !ok || len(tolerations) > 2 {
			return clusterSSHApplicationResource{}, false
		}
		seen := map[string]bool{}
		for _, raw := range tolerations {
			t, ok := raw.(map[string]any)
			key := clusterSSHStorageText(t, "key")
			seconds, valid := clusterSSHStorageInteger(t["tolerationSeconds"])
			if !ok || len(t) != 4 || !textInSet(key, "node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable") || seen[key] || t["operator"] != "Exists" || t["effect"] != "NoExecute" || !valid || seconds != 300 {
				return clusterSSHApplicationResource{}, false
			}
			seen[key] = true
		}
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 {
		return clusterSSHApplicationResource{}, false
	}
	c, ok := containers[0].(map[string]any)
	if !ok || c["name"] != "manager" || !clusterSSHStorageEmptyText(c["restartPolicy"]) || !clusterSSHStorageEmptyList(c["resizePolicy"]) {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources("cnpg-operator", c["resources"])
}

func clusterSSHCNPGResourcePlan(object map[string]any) (clusterSSHCNPGOperatorResources, bool) {
	spec := clusterSSHStorageMap(object, "spec")
	replicas, ok := clusterSSHStorageInteger(spec["replicas"])
	if !ok || replicas < 1 || replicas > clusterSSHLonghornDeploymentPodLimit {
		return clusterSSHCNPGOperatorResources{}, false
	}
	// Pinned manifest omits strategy; API defaults it to RollingUpdate with
	// 25% surge (rounded up) and 25% unavailable. Reject unreviewed strategies.
	if raw, present := spec["strategy"]; present {
		strategy, ok := raw.(map[string]any)
		if !ok || len(strategy) > 2 || (!clusterSSHStorageEmptyText(strategy["type"]) && strategy["type"] != "RollingUpdate") {
			return clusterSSHCNPGOperatorResources{}, false
		}
		for key := range strategy {
			if key != "type" && key != "rollingUpdate" {
				return clusterSSHCNPGOperatorResources{}, false
			}
		}
		if raw, present := strategy["rollingUpdate"]; present {
			rolling, ok := raw.(map[string]any)
			if !ok || len(rolling) > 2 {
				return clusterSSHCNPGOperatorResources{}, false
			}
			for key, value := range rolling {
				if (key != "maxSurge" && key != "maxUnavailable") || value != "25%" {
					return clusterSSHCNPGOperatorResources{}, false
				}
			}
		}
	}
	envelope, ok := clusterSSHCNPGResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), false)
	return clusterSSHCNPGOperatorResources{Envelope: envelope, Replicas: uint64(replicas), Surge: uint64((replicas + 3) / 4)}, ok
}
