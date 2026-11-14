package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

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

func observeClusterSSHLonghornUIRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, deployment clusterSSHStorageIdentity, image string, plan clusterSSHLonghornUIResources) (string, clusterSSHLonghornUIResources, error) {
	fail := func() (string, clusterSSHLonghornUIResources, error) {
		return "", clusterSSHLonghornUIResources{}, clusterbootstrap.ErrPreparationConfig
	}
	if readList == nil {
		return fail()
	}
	checkedList := func(path, kind, namespace string, limit int) ([]map[string]any, error) {
		pods, err := readList(path, kind, namespace, limit)
		if err != nil {
			return nil, err
		}
		for _, pod := range pods {
			if !clusterSSHBoundedPodResources(plan.Envelope, pod, "longhorn-ui", "") {
				return nil, clusterbootstrap.ErrPreparationConfig
			}
		}
		plan.Observed += uint64(len(pods))
		return pods, nil
	}
	resolved, err := observeClusterSSHLonghornDeploymentRuntime(read, checkedList, source, deployment, image, clusterSSHLonghornDeploymentWorkload{
		namespace: "longhorn-system", labelKey: "app",
		name: "longhorn-ui", serviceAccount: "longhorn-ui-service-account", repository: "docker.io/longhornio/longhorn-ui",
		podsPath: clusterSSHLonghornUIPodsPath, replicaSetPrefix: clusterSSHLonghornUIReplicaSetPrefix,
		replicaSetPathValid: clusterSSHLonghornUIReplicaSetPathValid, templateImage: clusterSSHLonghornUITemplateImage,
		specValid: func(spec map[string]any, pod bool) bool {
			envelope, ok := clusterSSHLonghornUIResourceSpec(spec, pod)
			return ok && envelope == plan.Envelope
		},
	})
	if _, _, ok := plan.budget(); err != nil || !ok {
		return fail()
	}
	return resolved, plan, nil
}
