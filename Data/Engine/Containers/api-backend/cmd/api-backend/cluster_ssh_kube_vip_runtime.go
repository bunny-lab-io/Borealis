package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
	"time"
)

const clusterSSHKubeVIPPodsPrefix = "/api/v1/namespaces/kube-system/pods?labelSelector=app.kubernetes.io%2Fname%3Dkube-vip-borealis-cluster&fieldSelector=spec.nodeName%3D"
const clusterSSHKubeVIPPodsSuffix = "&limit=2"

func clusterSSHKubeVIPPodsPath(node string) string {
	return clusterSSHKubeVIPPodsPrefix + node + clusterSSHKubeVIPPodsSuffix
}
func clusterSSHKubeVIPPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHKubeVIPPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, clusterSSHKubeVIPPodsSuffix)
	return ok && clusterSSHStorageName(node)
}

// Query only the frozen active sources: replacement does not require a running
// Pod on the failed member or a candidate. Selectors restrict transport, never
// replace independent object ownership, node, readiness and image checks.
func observeClusterSSHKubeVIPRuntime(readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, owner clusterSSHStorageIdentity, image string) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	if readList == nil || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	nodes, ids := map[string]bool{}, map[string]bool{}
	resolved := ""
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) || nodes[member.Name] {
			return fail()
		}
		nodes[member.Name] = true
		pods, err := readList(clusterSSHKubeVIPPodsPath(member.Name), "Pod", "kube-system", 2)
		if err != nil || len(pods) != 1 {
			return fail()
		}
		pod := pods[0]
		id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "kube-system")
		metadata := clusterSSHStorageMap(pod, "metadata")
		if !ok || ids[id.UID] || clusterSSHStorageMap(metadata, "labels")["app.kubernetes.io/name"] != "kube-vip-borealis-cluster" {
			return fail()
		}
		ids[id.UID] = true
		refs, ok := metadata["ownerReferences"].([]any)
		if !ok || len(refs) != 1 {
			return fail()
		}
		ref, ok := refs[0].(map[string]any)
		if !ok || ref["apiVersion"] != "apps/v1" || ref["kind"] != "DaemonSet" || ref["name"] != owner.Name || ref["uid"] != owner.UID || ref["controller"] != true {
			return fail()
		}
		spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
		if spec["nodeName"] != member.Name || spec["hostNetwork"] != true || spec["serviceAccountName"] != "kube-vip-borealis" || status["phase"] != "Running" ||
			!clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) ||
			!clusterSSHStorageEmptyList(status["initContainerStatuses"]) || !clusterSSHStorageEmptyList(status["ephemeralContainerStatuses"]) {
			return fail()
		}
		conditions, ok := status["conditions"].([]any)
		ready := 0
		if !ok {
			return fail()
		}
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if !ok {
				return fail()
			}
			if condition["type"] == "Ready" {
				if condition["status"] != "True" {
					return fail()
				}
				ready++
			}
		}
		if ready != 1 {
			return fail()
		}
		containers, ok := spec["containers"].([]any)
		if !ok || len(containers) != 1 {
			return fail()
		}
		container, ok := containers[0].(map[string]any)
		if !ok || container["name"] != "kube-vip" || container["image"] != image {
			return fail()
		}
		statuses, ok := status["containerStatuses"].([]any)
		if !ok || len(statuses) != 1 {
			return fail()
		}
		runtime, ok := statuses[0].(map[string]any)
		if !ok || runtime["name"] != "kube-vip" || runtime["image"] != image || runtime["ready"] != true || runtime["started"] != true {
			return fail()
		}
		state := clusterSSHStorageMap(runtime, "state")
		started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
		if len(state) != 1 || err != nil || started.IsZero() {
			return fail()
		}
		current := clusterSSHStorageText(runtime, "imageID")
		if !(clusterSSHSourceExternalImage{Configured: image, Resolved: current}).valid(clusterSSHKubeVIPRepository) || (resolved != "" && resolved != current) {
			return fail()
		}
		resolved = current
	}
	return resolved, nil
}
