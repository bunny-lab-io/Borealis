package main

// One cluster-wide operator Deployment can schedule on any joining Engine.
// Charge the whole possible set to each target, without assuming distribution.
type clusterSSHCertManagerResources struct {
	Envelope clusterSSHApplicationResource `json:"envelope"`
	Replicas uint64                        `json:"replicas"`
	Observed uint64                        `json:"observed"`
	Surge    uint64                        `json:"surge"`
}

func (v clusterSSHCertManagerResources) budget(name string) (cpu, memory uint64, ok bool) {
	if !textInSet(name, "cert-manager-controller", "cert-manager-webhook", "cert-manager-cainjector") || v.Envelope.Name != name || !clusterSSHWorkloadResourceValid(v.Envelope) || v.Replicas == 0 || v.Replicas > clusterSSHLonghornDeploymentPodLimit || v.Observed == 0 || v.Observed > clusterSSHLonghornDeploymentPodLimit || v.Surge != (v.Replicas+3)/4 {
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

// Support pinned producer's Linux placement and Kubernetes defaults.
// Added hard placement, scheduling or Pod resource contracts need separate proof.
func clusterSSHCertManagerResourceSpec(spec map[string]any, pod bool, name string) (clusterSSHApplicationResource, bool) {
	selector := clusterSSHStorageMap(spec, "nodeSelector")
	if !textInSet(name, "cert-manager-controller", "cert-manager-webhook", "cert-manager-cainjector") || len(selector) != 1 || selector["kubernetes.io/os"] != "linux" || !clusterSSHStorageEmptyMap(spec["affinity"]) ||
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
	if !ok || c["name"] != name || !clusterSSHStorageEmptyText(c["restartPolicy"]) || !clusterSSHStorageEmptyList(c["resizePolicy"]) {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources(name, c["resources"])
}

func clusterSSHCertManagerResourcePlan(object map[string]any, name string) (clusterSSHCertManagerResources, bool) {
	spec := clusterSSHStorageMap(object, "spec")
	replicas, ok := clusterSSHStorageInteger(spec["replicas"])
	if !ok || replicas < 1 || replicas > clusterSSHLonghornDeploymentPodLimit {
		return clusterSSHCertManagerResources{}, false
	}
	// Pinned manifest omits strategy; API defaults it to RollingUpdate with
	// 25% surge (rounded up) and 25% unavailable. Reject unreviewed strategies.
	if raw, present := spec["strategy"]; present {
		strategy, ok := raw.(map[string]any)
		if !ok || len(strategy) > 2 || (!clusterSSHStorageEmptyText(strategy["type"]) && strategy["type"] != "RollingUpdate") {
			return clusterSSHCertManagerResources{}, false
		}
		for key := range strategy {
			if key != "type" && key != "rollingUpdate" {
				return clusterSSHCertManagerResources{}, false
			}
		}
		if raw, present := strategy["rollingUpdate"]; present {
			rolling, ok := raw.(map[string]any)
			if !ok || len(rolling) > 2 {
				return clusterSSHCertManagerResources{}, false
			}
			for key, value := range rolling {
				if (key != "maxSurge" && key != "maxUnavailable") || value != "25%" {
					return clusterSSHCertManagerResources{}, false
				}
			}
		}
	}
	envelope, ok := clusterSSHCertManagerResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), false, name)
	return clusterSSHCertManagerResources{Envelope: envelope, Replicas: uint64(replicas), Surge: uint64((replicas + 3) / 4)}, ok
}
