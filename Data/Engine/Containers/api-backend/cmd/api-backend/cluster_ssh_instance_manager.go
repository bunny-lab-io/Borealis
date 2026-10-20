package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const clusterSSHInstanceOwnerPrefix = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/instancemanagers/"
const clusterSSHInstanceNodePrefix = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/nodes/"
const clusterSSHInstanceCPUPath = clusterSSHStorageSettingPrefix + "guaranteed-instance-manager-cpu"
const clusterSSHInstancePodsPrefix = "/api/v1/namespaces/longhorn-system/pods?labelSelector=longhorn.io%2Fcomponent%3Dinstance-manager&fieldSelector=spec.nodeName%3D"

func clusterSSHInstancePodsPath(node string) string {
	return clusterSSHInstancePodsPrefix + node + "&limit=2"
}
func clusterSSHInstancePodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHInstancePodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, "&limit=2")
	return ok && clusterSSHStorageName(node)
}
func clusterSSHInstanceNamedPathValid(path string) bool {
	if name, ok := strings.CutPrefix(path, clusterSSHInstanceOwnerPrefix); ok {
		return strings.HasPrefix(name, "instance-manager-") && clusterSSHStorageName(name)
	}
	for _, prefix := range []string{clusterSSHInstanceNodePrefix, "/api/v1/nodes/"} {
		if name, ok := strings.CutPrefix(path, prefix); ok {
			return clusterSSHStorageName(name)
		}
	}
	return false
}

var clusterSSHInstancePercentageRE = regexp.MustCompile(`^(0|[1-9][0-9]?)(\.[0-9]{1,6})?$`)

func clusterSSHInstanceNativeCPU(read func(string) (map[string]any, error), member clusterSSHSourceMember, percentage float64) (uint64, bool) {
	node, err := read(clusterSSHInstanceNodePrefix + member.Name)
	id, ok := clusterSSHStorageMetadata(node, "longhorn.io/v1beta2", "Node", "longhorn-system")
	if err != nil || !ok || id.Name != member.Name {
		return 0, false
	}
	override, ok := clusterSSHStorageInteger(clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"])
	if !ok || uint64(override) > clusterSSHCapacityLimit {
		return 0, false
	}
	kube, err := read("/api/v1/nodes/" + member.Name)
	id, ok = clusterSSHStorageMetadata(kube, "v1", "Node", "")
	allocatable, valid := clusterSSHWorkloadMilliCPU(clusterSSHStorageMap(clusterSSHStorageMap(kube, "status"), "allocatable")["cpu"])
	if err != nil || !ok || !valid || id.Name != member.Name || id.UID != member.NodeUID {
		return 0, false
	}
	if override != 0 {
		return uint64(override), true
	}
	// Match pinned Longhorn1.12's float64 math.Round; do not copy another node's
	// CPU request or substitute host hardware for Kubernetes allocatable CPU.
	value := math.Round(float64(allocatable) * percentage / 100.0)
	return uint64(value), value > 0 && value <= float64(clusterSSHCapacityLimit)
}

// Observation-only gate. Full policy, owner, node, setting and Pod receipts join
// existing authority-bound rereads. No target demand or readiness is inferred.
func observeClusterSSHInstanceManagers(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, images clusterSSHLonghornManagerImages) error {
	fail := func() error { return clusterbootstrap.ErrPreparationConfig }
	if read == nil || readList == nil || len(source.Members) < 1 || len(source.Members) > 2 || !images.valid() {
		return fail()
	}
	bounds, err := observeClusterSSHInstancePolicy(read)
	if err != nil {
		return fail()
	}
	setting, err := read(clusterSSHInstanceCPUPath)
	id, ok := clusterSSHStorageMetadata(setting, "longhorn.io/v1beta2", "Setting", "longhorn-system")
	raw := clusterSSHStorageText(setting, "value")
	if err != nil || !ok || id.Name != "guaranteed-instance-manager-cpu" || len(raw) > 1024 {
		return fail()
	}
	values, _, err := clusterSSHStorageObject(json.RawMessage(raw))
	text := clusterSSHStorageText(values, "v1")
	if err != nil || !clusterSSHInstancePercentageRE.MatchString(text) {
		return fail()
	}
	for key, value := range values {
		if key != "v1" && key != "v2" {
			return fail()
		}
		if _, ok := value.(string); !ok {
			return fail()
		}
	}
	percentage, err := strconv.ParseFloat(text, 64)
	if err != nil || percentage < 0 || percentage > 40 {
		return fail()
	}
	seenNodes, seenPods, seenOwners, seenContainers := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) || seenNodes[member.Name] {
			return fail()
		}
		seenNodes[member.Name] = true
		cpu, ok := clusterSSHInstanceNativeCPU(read, member, percentage)
		if !ok || cpu > bounds.CPULimitMilli {
			return fail()
		}
		pods, err := readList(clusterSSHInstancePodsPath(member.Name), "Pod", "longhorn-system", 2)
		if err != nil || len(pods) != 1 {
			return fail()
		}
		pod := pods[0]
		podID, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "longhorn-system")
		meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
		labels := clusterSSHStorageMap(meta, "labels")
		if !ok || seenPods[podID.UID] || !strings.HasPrefix(podID.Name, "instance-manager-") || labels["longhorn.io/component"] != "instance-manager" || labels["longhorn.io/data-engine"] != "v1" || labels["longhorn.io/instance-manager-type"] != "aio" || spec["nodeName"] != member.Name || spec["serviceAccountName"] != "longhorn-service-account" || status["phase"] != "Running" {
			return fail()
		}
		seenPods[podID.UID] = true
		owner, err := read(clusterSSHInstanceOwnerPrefix + podID.Name)
		ownerID, ok := clusterSSHStorageMetadata(owner, "longhorn.io/v1beta2", "InstanceManager", "longhorn-system")
		os, ost := clusterSSHStorageMap(owner, "spec"), clusterSSHStorageMap(owner, "status")
		if err != nil || !ok || ownerID.Name != podID.Name || seenOwners[ownerID.UID] || os["nodeID"] != member.Name || os["dataEngine"] != "v1" || os["type"] != "aio" || os["image"] != images.Instance || ost["ownerID"] != member.Name || ost["currentState"] != "running" {
			return fail()
		}
		seenOwners[ownerID.UID] = true
		refs, ok := meta["ownerReferences"].([]any)
		if !ok || len(refs) != 1 {
			return fail()
		}
		ref, ok := refs[0].(map[string]any)
		if !ok || ref["apiVersion"] != "longhorn.io/v1beta2" || ref["kind"] != "InstanceManager" || ref["name"] != ownerID.Name || ref["uid"] != ownerID.UID || ref["controller"] != true {
			return fail()
		}
		envelope := bounds
		envelope.CPURequestMilli = cpu
		if !clusterSSHBoundedPodResources(envelope, pod, "instance-manager", "") || !clusterSSHStorageEmptyList(status["ephemeralContainerStatuses"]) {
			return fail()
		}
		container := spec["containers"].([]any)[0].(map[string]any)
		runtime, ok := status["containerStatuses"].([]any)[0].(map[string]any)
		if !ok || container["image"] != images.Instance || runtime["image"] != images.Instance || runtime["ready"] != true || runtime["started"] != true {
			return fail()
		}
		state := clusterSSHStorageMap(runtime, "state")
		started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
		cid, ok := strings.CutPrefix(clusterSSHStorageText(runtime, "containerID"), "containerd://")
		if err != nil || started.IsZero() || len(state) != 1 || !ok || !clusterSSHSourceObservationRE.MatchString(cid) || cid == strings.Repeat("0", 64) || seenContainers[cid] {
			return fail()
		}
		seenContainers[cid] = true
		image := clusterSSHSourceExternalImage{Configured: images.Instance, Resolved: clusterSSHStorageText(runtime, "imageID")}
		if !image.valid("docker.io/longhornio/longhorn-instance-manager") {
			return fail()
		}
		ready := 0
		for _, raw := range status["conditions"].([]any) {
			c := raw.(map[string]any)
			if c["type"] == "Ready" {
				if c["status"] != "True" {
					return fail()
				}
				ready++
			}
		}
		if ready != 1 {
			return fail()
		}
	}
	return nil
}
