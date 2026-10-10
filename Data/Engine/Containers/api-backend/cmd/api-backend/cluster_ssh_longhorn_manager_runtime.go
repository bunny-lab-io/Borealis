package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
	"time"
)

const clusterSSHLonghornManagerPodsPrefix = "/api/v1/namespaces/longhorn-system/pods?labelSelector=app%3Dlonghorn-manager&fieldSelector=spec.nodeName%3D"
const clusterSSHLonghornManagerPodsSuffix = "&limit=2"

func clusterSSHLonghornManagerPodsPath(node string) string {
	return clusterSSHLonghornManagerPodsPrefix + node + clusterSSHLonghornManagerPodsSuffix
}
func clusterSSHLonghornManagerPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHLonghornManagerPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, clusterSSHLonghornManagerPodsSuffix)
	return ok && clusterSSHStorageName(node)
}

// Query only the frozen active sources: replacement does not require a running
// Pod on the failed member or a candidate. Selectors restrict transport, never
// replace independent object ownership, node, readiness and image checks.
func observeClusterSSHLonghornManagerRuntime(readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, owner clusterSSHStorageIdentity, images clusterSSHLonghornManagerImages) (string, string, error) {
	fail := func() (string, string, error) { return "", "", clusterbootstrap.ErrPreparationConfig }
	if readList == nil || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	nodes, ids := map[string]bool{}, map[string]bool{}
	resolved := [2]string{}
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) || nodes[member.Name] {
			return fail()
		}
		nodes[member.Name] = true
		pods, err := readList(clusterSSHLonghornManagerPodsPath(member.Name), "Pod", "longhorn-system", 2)
		if err != nil || len(pods) != 1 {
			return fail()
		}
		pod := pods[0]
		id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "longhorn-system")
		metadata := clusterSSHStorageMap(pod, "metadata")
		if !ok || ids[id.UID] || clusterSSHStorageMap(metadata, "labels")["app"] != "longhorn-manager" {
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
		if spec["nodeName"] != member.Name || (spec["hostNetwork"] != nil && spec["hostNetwork"] != false) || spec["serviceAccountName"] != "longhorn-service-account" || status["phase"] != "Running" ||
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
		if !ok || len(containers) != 2 {
			return fail()
		}
		configured := map[string]string{"longhorn-manager": images.Manager, "pre-pull-share-manager-image": images.Share}
		seen := map[string]bool{}
		for _, raw := range containers {
			container, ok := raw.(map[string]any)
			name := clusterSSHStorageText(container, "name")
			want, known := configured[name]
			if !ok || !known || seen[name] || container["image"] != want {
				return fail()
			}
			seen[name] = true
		}
		statuses, ok := status["containerStatuses"].([]any)
		if !ok || len(statuses) != 2 {
			return fail()
		}
		seen = map[string]bool{}
		current := [2]string{}
		for _, raw := range statuses {
			runtime, ok := raw.(map[string]any)
			name := clusterSSHStorageText(runtime, "name")
			want, known := configured[name]
			if !ok || !known || seen[name] || runtime["image"] != want || runtime["ready"] != true || runtime["started"] != true {
				return fail()
			}
			seen[name] = true
			state := clusterSSHStorageMap(runtime, "state")
			started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
			if len(state) != 1 || err != nil || started.IsZero() {
				return fail()
			}
			index, role := 0, "longhorn-manager"
			if name == "pre-pull-share-manager-image" {
				index, role = 1, "longhorn-share-manager"
			}
			current[index] = clusterSSHStorageText(runtime, "imageID")
			if !(clusterSSHSourceExternalImage{Configured: want, Resolved: current[index]}).valid("docker.io/longhornio/" + role) {
				return fail()
			}
		}
		if resolved != ([2]string{}) && resolved != current {
			return fail()
		}
		resolved = current
	}
	return resolved[0], resolved[1], nil
}
