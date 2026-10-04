package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
	"time"
)

// Evidence bounds, not replica policy or host capacity minima.
const clusterSSHLonghornUIPodLimit = 16
const clusterSSHLonghornUIPodsPrefix = "/api/v1/namespaces/longhorn-system/pods?labelSelector=app%3Dlonghorn-ui&fieldSelector=spec.nodeName%3D"
const clusterSSHLonghornUIPodsSuffix = "&limit=16"
const clusterSSHLonghornUIReplicaSetPrefix = "/apis/apps/v1/namespaces/longhorn-system/replicasets/"

func clusterSSHLonghornUIPodsPath(node string) string {
	return clusterSSHLonghornUIPodsPrefix + node + clusterSSHLonghornUIPodsSuffix
}
func clusterSSHLonghornUIPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHLonghornUIPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, clusterSSHLonghornUIPodsSuffix)
	return ok && clusterSSHStorageName(node)
}
func clusterSSHLonghornUIReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHLonghornUIReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "longhorn-ui-") && clusterSSHStorageName(name)
}
func clusterSSHLonghornUIOwner(object map[string]any, kind string) (clusterSSHStorageIdentity, bool) {
	refs, ok := clusterSSHStorageMap(object, "metadata")["ownerReferences"].([]any)
	if !ok || len(refs) != 1 {
		return clusterSSHStorageIdentity{}, false
	}
	ref, ok := refs[0].(map[string]any)
	id := clusterSSHStorageIdentity{Name: clusterSSHStorageText(ref, "name"), UID: clusterSSHStorageText(ref, "uid")}
	return id, ok && ref["apiVersion"] == "apps/v1" && ref["kind"] == kind && ref["controller"] == true && clusterSSHStorageName(id.Name) && vipScopeUUID(id.UID)
}

// Deployment scheduling is not per-node. Require some owned running evidence,
// but no desired replica count or Pod on a failed member/candidate. All returned
// active-source Pods must pass; selectors never replace object checks.
func observeClusterSSHLonghornUIRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, deployment clusterSSHStorageIdentity, image string) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	if read == nil || readList == nil || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	nodes, podIDs, podNames := map[string]bool{}, map[string]bool{}, map[string]bool{}
	sets := map[string]clusterSSHStorageIdentity{}
	setIDs := map[string]bool{}
	resolved := ""
	count := 0
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) || nodes[member.Name] {
			return fail()
		}
		nodes[member.Name] = true
		pods, err := readList(clusterSSHLonghornUIPodsPath(member.Name), "Pod", "longhorn-system", clusterSSHLonghornUIPodLimit)
		if err != nil {
			return fail()
		}
		count += len(pods)
		if count > clusterSSHLonghornUIPodLimit {
			return fail()
		}
		for _, pod := range pods {
			id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "longhorn-system")
			if !ok || podIDs[id.UID] || podNames[id.Name] || clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["app"] != "longhorn-ui" {
				return fail()
			}
			podIDs[id.UID], podNames[id.Name] = true, true
			owner, ok := clusterSSHLonghornUIOwner(pod, "ReplicaSet")
			path := clusterSSHLonghornUIReplicaSetPrefix + owner.Name
			if !ok || !clusterSSHLonghornUIReplicaSetPathValid(path) {
				return fail()
			}
			set, seen := sets[owner.Name]
			if !seen {
				object, err := read(path)
				var valid bool
				set, valid = clusterSSHStorageMetadata(object, "apps/v1", "ReplicaSet", "longhorn-system")
				parent, owned := clusterSSHLonghornUIOwner(object, "Deployment")
				configured, configErr := clusterSSHLonghornUITemplateImage(object)
				if err != nil || !valid || set.Name != owner.Name || setIDs[set.UID] || !owned || parent.Name != deployment.Name || parent.UID != deployment.UID || configErr != nil || configured != image {
					return fail()
				}
				sets[owner.Name], setIDs[set.UID] = set, true
			}
			if set.UID != owner.UID {
				return fail()
			}
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			if spec["nodeName"] != member.Name || (spec["hostNetwork"] != nil && spec["hostNetwork"] != false) || spec["serviceAccountName"] != "longhorn-ui-service-account" || status["phase"] != "Running" || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || !clusterSSHStorageEmptyList(status["initContainerStatuses"]) || !clusterSSHStorageEmptyList(status["ephemeralContainerStatuses"]) {
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
			if !ok || container["name"] != "longhorn-ui" || container["image"] != image {
				return fail()
			}
			statuses, ok := status["containerStatuses"].([]any)
			if !ok || len(statuses) != 1 {
				return fail()
			}
			runtime, ok := statuses[0].(map[string]any)
			if !ok || runtime["name"] != "longhorn-ui" || runtime["image"] != image || runtime["ready"] != true || runtime["started"] != true {
				return fail()
			}
			state := clusterSSHStorageMap(runtime, "state")
			started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
			current := clusterSSHStorageText(runtime, "imageID")
			if len(state) != 1 || err != nil || started.IsZero() || !(clusterSSHSourceExternalImage{Configured: image, Resolved: current}).valid("docker.io/longhornio/longhorn-ui") || (resolved != "" && resolved != current) {
				return fail()
			}
			resolved = current
		}
	}
	if resolved == "" {
		return fail()
	}
	return resolved, nil
}
