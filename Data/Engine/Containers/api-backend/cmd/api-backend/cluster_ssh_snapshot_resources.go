package main

import (
	"encoding/json"
	"maps"
)

// One cluster-wide operator Deployment can schedule on any joining Engine.
// Charge the whole possible set to each target, without assuming distribution.
type clusterSSHSnapshotResources struct {
	Envelope clusterSSHApplicationResource `json:"envelope"`
	Replicas uint64                        `json:"replicas"`
	Observed uint64                        `json:"observed"`
}

func (v clusterSSHSnapshotResources) budget() (cpu, memory uint64, ok bool) {
	if v.Envelope.Name != "snapshot-controller" || !clusterSSHWorkloadResourceValid(v.Envelope) || v.Replicas == 0 || v.Replicas > clusterSSHLonghornDeploymentPodLimit || v.Observed == 0 || v.Observed > clusterSSHLonghornDeploymentPodLimit {
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

// Support the repository producer's soft spreading and Kubernetes defaults.
// Added hard placement, scheduling or Pod resource contracts need separate proof.
func clusterSSHSnapshotResourceSpec(spec map[string]any, pod bool) (clusterSSHApplicationResource, bool) {
	if !clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHSnapshotAffinity(spec["affinity"]) ||
		!clusterSSHSnapshotTopology(spec["topologySpreadConstraints"]) || !clusterSSHStorageEmptyList(spec["schedulingGates"]) ||
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
	if !ok || c["name"] != "snapshot-controller" || !clusterSSHStorageEmptyText(c["restartPolicy"]) || !clusterSSHStorageEmptyList(c["resizePolicy"]) {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources("snapshot-controller", c["resources"])
}

func clusterSSHSnapshotResourcePlan(object map[string]any) (clusterSSHSnapshotResources, bool) {
	spec := clusterSSHStorageMap(object, "spec")
	replicas, ok := clusterSSHStorageInteger(spec["replicas"])
	strategy := clusterSSHStorageMap(spec, "strategy")
	rolling := clusterSSHStorageMap(strategy, "rollingUpdate")
	surge, surgeOK := clusterSSHStorageInteger(rolling["maxSurge"])
	unavailable, unavailableOK := clusterSSHStorageInteger(rolling["maxUnavailable"])
	if !ok || replicas < 1 || replicas > clusterSSHLonghornDeploymentPodLimit || len(strategy) != 2 || strategy["type"] != "RollingUpdate" || len(rolling) != 2 || !surgeOK || surge != 0 || !unavailableOK || unavailable != 1 {
		return clusterSSHSnapshotResources{}, false
	}
	envelope, ok := clusterSSHSnapshotResourceSpec(clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec"), false)
	return clusterSSHSnapshotResources{Envelope: envelope, Replicas: uint64(replicas)}, ok
}

func clusterSSHSnapshotAffinity(raw any) bool {
	canonical, err := json.Marshal(raw)
	return err == nil && string(canonical) == `{"podAntiAffinity":{"preferredDuringSchedulingIgnoredDuringExecution":[{"podAffinityTerm":{"labelSelector":{"matchLabels":{"app.kubernetes.io/name":"snapshot-controller"}},"topologyKey":"kubernetes.io/hostname"},"weight":100}]}}`
}

func clusterSSHSnapshotTopology(raw any) bool {
	values, ok := raw.([]any)
	if !ok || len(values) != 1 {
		return false
	}
	value, ok := values[0].(map[string]any)
	if !ok {
		return false
	}
	// Accept omitted/null or explicit Kubernetes defaults, without mutating the
	// source object whose complete receipt remains authoritative.
	copy := maps.Clone(value)
	for key, wanted := range map[string]string{"nodeAffinityPolicy": "Honor", "nodeTaintsPolicy": "Ignore"} {
		if value[key] != nil && value[key] != wanted {
			return false
		}
		delete(copy, key)
	}
	if !clusterSSHStorageEmptyList(copy["matchLabelKeys"]) {
		return false
	}
	delete(copy, "matchLabelKeys")
	canonical, err := json.Marshal(copy)
	return err == nil && string(canonical) == `{"labelSelector":{"matchLabels":{"app.kubernetes.io/name":"snapshot-controller"}},"maxSkew":1,"topologyKey":"kubernetes.io/hostname","whenUnsatisfiable":"ScheduleAnyway"}`
}
