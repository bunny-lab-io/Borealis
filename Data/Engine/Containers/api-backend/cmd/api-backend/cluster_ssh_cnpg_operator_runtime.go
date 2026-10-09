package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const clusterSSHCNPGPodsPrefix = "/api/v1/namespaces/cnpg-system/pods?labelSelector=app.kubernetes.io%2Fname%3Dcloudnative-pg&fieldSelector=spec.nodeName%3D"
const clusterSSHCNPGReplicaSetPrefix = "/apis/apps/v1/namespaces/cnpg-system/replicasets/"

func clusterSSHCNPGPodsPath(node string) string { return clusterSSHCNPGPodsPrefix + node + "&limit=16" }
func clusterSSHCNPGPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHCNPGPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, "&limit=16")
	return ok && clusterSSHStorageName(node)
}
func clusterSSHCNPGReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHCNPGReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "cnpg-controller-manager-") && clusterSSHStorageName(name)
}
func clusterSSHCNPGOperatorTemplateImage(object map[string]any, bootstrap clusterSSHSourceExternalImage) (string, error) {
	template := clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template")
	if clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")["app.kubernetes.io/name"] != "cloudnative-pg" {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return clusterSSHCNPGOperatorSpecImage(clusterSSHStorageMap(template, "spec"), false, bootstrap)
}
func clusterSSHCNPGOperatorSpecImage(spec map[string]any, pod bool, bootstrap clusterSSHSourceExternalImage) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	if spec["serviceAccountName"] != "cnpg-manager" || spec["hostNetwork"] != nil && spec["hostNetwork"] != false {
		return fail()
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	image := clusterSSHStorageText(container, "image")
	if !ok || container["name"] != "manager" || !clusterSSHCNPGOperatorImageValid(image, bootstrap) ||
		!clusterSSHStorageStrings(container["command"], "/manager") || !clusterSSHCNPGOperatorArguments(container["args"]) ||
		!clusterSSHStorageEmptyList(container["envFrom"]) || !clusterSSHCNPGOperatorEnvironment(container["env"], image) ||
		!clusterSSHStorageEmptyList(container["volumeDevices"]) || !clusterSSHStorageEmptyText(container["workingDir"]) ||
		!clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyMap(container["lifecycle"]) ||
		!clusterSSHCNPGOperatorDeclaredMounts(container, spec, pod) {
		return fail()
	}

	return image, nil
}
func clusterSSHCNPGOperatorDeclaredMounts(container, spec map[string]any, pod bool) bool {
	mounts, volumes, ok := clusterSSHServiceAccountMounts(container["volumeMounts"], spec["volumes"], 2, pod)
	return ok && clusterSSHCNPGOperatorMounts(mounts, volumes)
}
func observeClusterSSHCNPGOperatorRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, image string, bootstrap clusterSSHSourceExternalImage) (string, error) {
	object, err := read(clusterSSHCNPGOperatorPath)
	id, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "cnpg-system")
	if err != nil || !ok || id.Name != "cnpg-controller-manager" || !clusterSSHCNPGOperatorImageValid(image, bootstrap) {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return observeClusterSSHLonghornDeploymentRuntime(read, readList, source, id, image, clusterSSHLonghornDeploymentWorkload{
		namespace: "cnpg-system", labelKey: "app.kubernetes.io/name", labelValue: "cloudnative-pg", name: "cnpg-controller-manager", containerName: "manager", serviceAccount: "cnpg-manager", repository: clusterSSHCNPGRepository,
		podsPath: clusterSSHCNPGPodsPath, replicaSetPrefix: clusterSSHCNPGReplicaSetPrefix, replicaSetPathValid: clusterSSHCNPGReplicaSetPathValid,
		templateImage: func(object map[string]any) (string, error) {
			return clusterSSHCNPGOperatorTemplateImage(object, bootstrap)
		},
		specValid: func(spec map[string]any, pod bool) bool {
			_, err := clusterSSHCNPGOperatorSpecImage(spec, pod, bootstrap)
			return err == nil
		},
	})
}
