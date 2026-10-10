package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
	"time"
)

// Evidence bounds, not replica policy or host capacity minima.
const clusterSSHLonghornDriverPodLimit = 16
const clusterSSHLonghornDriverPodsPrefix = "/api/v1/namespaces/longhorn-system/pods?labelSelector=app%3Dlonghorn-driver-deployer&fieldSelector=spec.nodeName%3D"
const clusterSSHLonghornDriverPodsSuffix = "&limit=16"
const clusterSSHLonghornDriverReplicaSetPrefix = "/apis/apps/v1/namespaces/longhorn-system/replicasets/"

func clusterSSHLonghornDriverPodsPath(node string) string {
	return clusterSSHLonghornDriverPodsPrefix + node + clusterSSHLonghornDriverPodsSuffix
}
func clusterSSHLonghornDriverPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHLonghornDriverPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, clusterSSHLonghornDriverPodsSuffix)
	return ok && clusterSSHStorageName(node)
}
func clusterSSHLonghornDriverReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHLonghornDriverReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "longhorn-driver-deployer-") && clusterSSHStorageName(name)
}

// Deployment scheduling is not per-node. Require some owned running evidence,
// but no desired replica count or Pod on a failed member/candidate. All returned
// active-source Pods must pass; selectors never replace object checks.
func observeClusterSSHLonghornDriverRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, deployment clusterSSHStorageIdentity, images clusterSSHLonghornDriverImages) (string, string, error) {
	fail := func() (string, string, error) { return "", "", clusterbootstrap.ErrPreparationConfig }
	image := images.Manager
	if read == nil || readList == nil || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	nodes, podIDs, podNames := map[string]bool{}, map[string]bool{}, map[string]bool{}
	sets := map[string]clusterSSHStorageIdentity{}
	setIDs := map[string]bool{}
	resolved, initResolved := "", ""
	count := 0
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) || nodes[member.Name] {
			return fail()
		}
		nodes[member.Name] = true
		pods, err := readList(clusterSSHLonghornDriverPodsPath(member.Name), "Pod", "longhorn-system", clusterSSHLonghornDriverPodLimit)
		if err != nil {
			return fail()
		}
		count += len(pods)
		if count > clusterSSHLonghornDriverPodLimit {
			return fail()
		}
		for _, pod := range pods {
			id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "longhorn-system")
			if !ok || podIDs[id.UID] || podNames[id.Name] || clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["app"] != "longhorn-driver-deployer" {
				return fail()
			}
			podIDs[id.UID], podNames[id.Name] = true, true
			owner, ok := clusterSSHLonghornUIOwner(pod, "ReplicaSet")
			path := clusterSSHLonghornDriverReplicaSetPrefix + owner.Name
			if !ok || !clusterSSHLonghornDriverReplicaSetPathValid(path) {
				return fail()
			}
			set, seen := sets[owner.Name]
			if !seen {
				object, err := read(path)
				var valid bool
				set, valid = clusterSSHStorageMetadata(object, "apps/v1", "ReplicaSet", "longhorn-system")
				parent, owned := clusterSSHLonghornUIOwner(object, "Deployment")
				configured, configErr := clusterSSHLonghornDriverTemplateImages(object)
				if err != nil || !valid || set.Name != owner.Name || setIDs[set.UID] || !owned || parent.Name != deployment.Name || parent.UID != deployment.UID || configErr != nil || configured != images {
					return fail()
				}
				sets[owner.Name], setIDs[set.UID] = set, true
			}
			if set.UID != owner.UID {
				return fail()
			}
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			if spec["nodeName"] != member.Name || (spec["hostNetwork"] != nil && spec["hostNetwork"] != false) || spec["serviceAccountName"] != "longhorn-service-account" || status["phase"] != "Running" || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || !clusterSSHStorageEmptyList(status["ephemeralContainerStatuses"]) {
				return fail()
			}
			conditions, ok := status["conditions"].([]any)
			ready := 0
			if !ok {
				return fail()
			}
			for _, raw := range conditions {
				c, ok := raw.(map[string]any)
				if !ok {
					return fail()
				}
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
			containers, ok := spec["containers"].([]any)
			if !ok || len(containers) != 1 {
				return fail()
			}
			container, ok := containers[0].(map[string]any)
			if !ok || container["name"] != "longhorn-driver-deployer" || container["image"] != image {
				return fail()
			}
			statuses, ok := status["containerStatuses"].([]any)
			if !ok || len(statuses) != 1 {
				return fail()
			}
			runtime, ok := statuses[0].(map[string]any)
			if !ok || runtime["name"] != "longhorn-driver-deployer" || runtime["image"] != image || runtime["ready"] != true || runtime["started"] != true {
				return fail()
			}
			state := clusterSSHStorageMap(runtime, "state")
			started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
			current := clusterSSHStorageText(runtime, "imageID")
			if len(state) != 1 || err != nil || started.IsZero() || !(clusterSSHSourceExternalImage{Configured: image, Resolved: current}).valid("docker.io/longhornio/longhorn-manager") || (resolved != "" && resolved != current) {
				return fail()
			}
			initCurrent, ok := clusterSSHLonghornDriverCompletedInit(spec, status, image)
			if !ok || (initResolved != "" && initResolved != initCurrent) {
				return fail()
			}
			initResolved = initCurrent
			resolved = current
		}
	}
	if resolved == "" {
		return fail()
	}
	return resolved, initResolved, nil
}

// A completed ordinary init is not a running sidecar: do not require its
// started/ready flags. Require successful termination and reviewed image identity.
func clusterSSHLonghornDriverCompletedInit(spec, status map[string]any, image string) (string, bool) {
	containers, ok := spec["initContainers"].([]any)
	if !ok || len(containers) != 1 {
		return "", false
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "wait-longhorn-manager" || container["image"] != image || !clusterSSHStorageEmptyText(container["restartPolicy"]) {
		return "", false
	}
	statuses, ok := status["initContainerStatuses"].([]any)
	if !ok || len(statuses) != 1 {
		return "", false
	}
	runtime, ok := statuses[0].(map[string]any)
	if !ok || runtime["name"] != "wait-longhorn-manager" || runtime["image"] != image {
		return "", false
	}
	state := clusterSSHStorageMap(runtime, "state")
	terminated := clusterSSHStorageMap(state, "terminated")
	exit, ok := clusterSSHStorageInteger(terminated["exitCode"])
	start, e1 := time.Parse(time.RFC3339Nano, clusterSSHStorageText(terminated, "startedAt"))
	finish, e2 := time.Parse(time.RFC3339Nano, clusterSSHStorageText(terminated, "finishedAt"))
	if len(state) != 1 || !ok || exit != 0 || e1 != nil || e2 != nil || start.IsZero() || finish.IsZero() || finish.Before(start) {
		return "", false
	}
	if raw, present := terminated["signal"]; present {
		signal, ok := clusterSSHStorageInteger(raw)
		if !ok || signal != 0 {
			return "", false
		}
	}
	resolved := clusterSSHStorageText(runtime, "imageID")
	return resolved, (clusterSSHSourceExternalImage{Configured: image, Resolved: resolved}).valid("docker.io/longhornio/longhorn-manager")
}
