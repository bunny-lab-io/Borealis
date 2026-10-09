package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const clusterSSHSnapshotPodsPrefix = "/api/v1/namespaces/kube-system/pods?labelSelector=app.kubernetes.io%2Fname%3Dsnapshot-controller&fieldSelector=spec.nodeName%3D"
const clusterSSHSnapshotReplicaSetPrefix = "/apis/apps/v1/namespaces/kube-system/replicasets/"

func clusterSSHSnapshotPodsPath(node string) string {
	return clusterSSHSnapshotPodsPrefix + node + "&limit=16"
}
func clusterSSHSnapshotPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHSnapshotPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, "&limit=16")
	return ok && clusterSSHStorageName(node)
}
func clusterSSHSnapshotReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHSnapshotReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "snapshot-controller-") && clusterSSHStorageName(name)
}
func observeClusterSSHSnapshotControllerRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, image string) (string, error) {
	object, err := read(clusterSSHSnapshotControllerPath)
	id, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "kube-system")
	if err != nil || !ok || id.Name != "snapshot-controller" || !clusterSSHSnapshotControllerImageValid(image) {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return observeClusterSSHLonghornDeploymentRuntime(read, readList, source, id, image, clusterSSHLonghornDeploymentWorkload{
		namespace: "kube-system", labelKey: "app.kubernetes.io/name", name: "snapshot-controller", serviceAccount: "snapshot-controller", repository: clusterSSHSnapshotControllerRepository,
		podsPath: clusterSSHSnapshotPodsPath, replicaSetPrefix: clusterSSHSnapshotReplicaSetPrefix, replicaSetPathValid: clusterSSHSnapshotReplicaSetPathValid,
		templateImage: clusterSSHSnapshotControllerTemplateImage,
		specValid: func(spec map[string]any, pod bool) bool {
			_, err := clusterSSHSnapshotControllerSpecImage(spec, pod)
			return err == nil
		},
	})
}
