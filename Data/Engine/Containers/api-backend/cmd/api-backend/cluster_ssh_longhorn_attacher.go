package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const clusterSSHLonghornAttacherPath = "/apis/apps/v1/namespaces/longhorn-system/deployments/csi-attacher"
const clusterSSHLonghornAttacherPodsPrefix = "/api/v1/namespaces/longhorn-system/pods?labelSelector=app%3Dcsi-attacher&fieldSelector=spec.nodeName%3D"
const clusterSSHLonghornAttacherPodsSuffix = "&limit=16"
const clusterSSHLonghornAttacherReplicaSetPrefix = "/apis/apps/v1/namespaces/longhorn-system/replicasets/"
const clusterSSHLonghornAttacherRepository = "docker.io/longhornio/csi-attacher"

func clusterSSHLonghornAttacherPodsPath(node string) string {
	return clusterSSHLonghornAttacherPodsPrefix + node + clusterSSHLonghornAttacherPodsSuffix
}
func clusterSSHLonghornAttacherPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHLonghornAttacherPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, clusterSSHLonghornAttacherPodsSuffix)
	return ok && clusterSSHStorageName(node)
}
func clusterSSHLonghornAttacherReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHLonghornAttacherReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "csi-attacher-") && clusterSSHStorageName(name)
}

// Reviewed image and invocation inputs. Socket mounts, underlying host contents,
// loaded process state and demand remain separate readiness inputs.
func clusterSSHLonghornAttacherTemplateImage(object map[string]any) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	image := clusterSSHStorageText(container, "image")
	if !ok || container["name"] != "csi-attacher" || !clusterSSHLonghornImageValid(image, "csi-attacher") || !clusterSSHLonghornCSIStartup(container, "csi-attacher") {
		return fail()
	}
	return image, nil
}

func observeClusterSSHLonghornAttacherImage(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, driver clusterSSHLonghornDriverImages) (clusterSSHSourceExternalImage, error) {
	fail := func() (clusterSSHSourceExternalImage, error) {
		return clusterSSHSourceExternalImage{}, clusterbootstrap.ErrPreparationConfig
	}
	object, err := read(clusterSSHLonghornAttacherPath)
	owner, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "longhorn-system")
	if err != nil || !ok || owner.Name != "csi-attacher" {
		return fail()
	}
	image, err := clusterSSHLonghornAttacherTemplateImage(object)
	if err != nil || image != driver.Attacher {
		return fail()
	}
	resolved, err := observeClusterSSHLonghornDeploymentRuntime(read, readList, source, owner, image, clusterSSHLonghornDeploymentWorkload{
		name: "csi-attacher", serviceAccount: "longhorn-service-account", repository: clusterSSHLonghornAttacherRepository,
		containerValid: func(c map[string]any) bool { return clusterSSHLonghornCSIStartup(c, "csi-attacher") },
		podsPath:       clusterSSHLonghornAttacherPodsPath, replicaSetPrefix: clusterSSHLonghornAttacherReplicaSetPrefix,
		replicaSetPathValid: clusterSSHLonghornAttacherReplicaSetPathValid, templateImage: clusterSSHLonghornAttacherTemplateImage,
	})
	if err != nil {
		return fail()
	}
	return clusterSSHSourceExternalImage{Configured: image, Resolved: resolved}, nil
}
