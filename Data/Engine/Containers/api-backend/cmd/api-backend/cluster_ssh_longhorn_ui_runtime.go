package main

import (
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

func observeClusterSSHLonghornUIRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, deployment clusterSSHStorageIdentity, image string) (string, error) {
	return observeClusterSSHLonghornDeploymentRuntime(read, readList, source, deployment, image, clusterSSHLonghornDeploymentWorkload{
		namespace: "longhorn-system", labelKey: "app",
		name: "longhorn-ui", serviceAccount: "longhorn-ui-service-account", repository: "docker.io/longhornio/longhorn-ui",
		podsPath: clusterSSHLonghornUIPodsPath, replicaSetPrefix: clusterSSHLonghornUIReplicaSetPrefix,
		replicaSetPathValid: clusterSSHLonghornUIReplicaSetPathValid, templateImage: clusterSSHLonghornUITemplateImage,
	})
}
