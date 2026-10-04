package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"time"
)

// Fixed internal UI/attacher contracts share proof mechanics, not authority.
// Transport still permits only each explicitly listed namespace/name/selector.
type clusterSSHLonghornDeploymentWorkload struct {
	name, serviceAccount, repository, replicaSetPrefix string
	podsPath                                           func(string) string
	replicaSetPathValid                                func(string) bool
	templateImage                                      func(map[string]any) (string, error)
}

// Transport/memory bound, never replica policy or host capacity minimum.
const clusterSSHLonghornDeploymentPodLimit = 16

// Deployment scheduling is not per-node. Require some owned running evidence,
// but no desired replica count or Pod on a failed member/candidate. All returned
// active-source Pods must pass; selectors never replace object checks.
func observeClusterSSHLonghornDeploymentRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, deployment clusterSSHStorageIdentity, image string, workload clusterSSHLonghornDeploymentWorkload) (string, error) {
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
		pods, err := readList(workload.podsPath(member.Name), "Pod", "longhorn-system", clusterSSHLonghornDeploymentPodLimit)
		if err != nil {
			return fail()
		}
		count += len(pods)
		if count > clusterSSHLonghornDeploymentPodLimit {
			return fail()
		}
		for _, pod := range pods {
			id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "longhorn-system")
			if !ok || podIDs[id.UID] || podNames[id.Name] || clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["app"] != workload.name {
				return fail()
			}
			podIDs[id.UID], podNames[id.Name] = true, true
			owner, ok := clusterSSHLonghornUIOwner(pod, "ReplicaSet")
			path := workload.replicaSetPrefix + owner.Name
			if !ok || !workload.replicaSetPathValid(path) {
				return fail()
			}
			set, seen := sets[owner.Name]
			if !seen {
				object, err := read(path)
				var valid bool
				set, valid = clusterSSHStorageMetadata(object, "apps/v1", "ReplicaSet", "longhorn-system")
				parent, owned := clusterSSHLonghornUIOwner(object, "Deployment")
				configured, configErr := workload.templateImage(object)
				if err != nil || !valid || set.Name != owner.Name || setIDs[set.UID] || !owned || parent.Name != deployment.Name || parent.UID != deployment.UID || configErr != nil || configured != image {
					return fail()
				}
				sets[owner.Name], setIDs[set.UID] = set, true
			}
			if set.UID != owner.UID {
				return fail()
			}
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			if spec["nodeName"] != member.Name || (spec["hostNetwork"] != nil && spec["hostNetwork"] != false) || spec["serviceAccountName"] != workload.serviceAccount || status["phase"] != "Running" || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || !clusterSSHStorageEmptyList(status["initContainerStatuses"]) || !clusterSSHStorageEmptyList(status["ephemeralContainerStatuses"]) {
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
			if !ok || container["name"] != workload.name || container["image"] != image {
				return fail()
			}
			statuses, ok := status["containerStatuses"].([]any)
			if !ok || len(statuses) != 1 {
				return fail()
			}
			runtime, ok := statuses[0].(map[string]any)
			if !ok || runtime["name"] != workload.name || runtime["image"] != image || runtime["ready"] != true || runtime["started"] != true {
				return fail()
			}
			state := clusterSSHStorageMap(runtime, "state")
			started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
			current := clusterSSHStorageText(runtime, "imageID")
			if len(state) != 1 || err != nil || started.IsZero() || !(clusterSSHSourceExternalImage{Configured: image, Resolved: current}).valid(workload.repository) || (resolved != "" && resolved != current) {
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
