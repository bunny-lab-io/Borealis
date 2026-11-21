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
func observeClusterSSHCNPGOperatorRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, image string, bootstrap clusterSSHSourceExternalImage) (string, clusterSSHCNPGOperatorResources, error) {
	object, err := read(clusterSSHCNPGOperatorPath)
	id, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "cnpg-system")
	if err != nil || !ok || id.Name != "cnpg-controller-manager" || !clusterSSHCNPGOperatorImageValid(image, bootstrap) {
		return "", clusterSSHCNPGOperatorResources{}, clusterbootstrap.ErrPreparationConfig
	}
	plan, ok := clusterSSHCNPGResourcePlan(object)
	if !ok || readList == nil {
		return "", clusterSSHCNPGOperatorResources{}, clusterbootstrap.ErrPreparationConfig
	}
	checkedList := func(path, kind, namespace string, limit int) ([]map[string]any, error) {
		pods, err := readList(path, kind, namespace, limit)
		if err != nil {
			return nil, err
		}
		for _, pod := range pods {
			if !clusterSSHBoundedPodResources(plan.Envelope, pod, "manager", "") {
				return nil, clusterbootstrap.ErrPreparationConfig
			}
		}
		plan.Observed += uint64(len(pods))
		return pods, nil
	}
	resolved, err := observeClusterSSHLonghornDeploymentRuntime(read, checkedList, source, id, image, clusterSSHLonghornDeploymentWorkload{
		namespace: "cnpg-system", labelKey: "app.kubernetes.io/name", labelValue: "cloudnative-pg", name: "cnpg-controller-manager", containerName: "manager", serviceAccount: "cnpg-manager", repository: clusterSSHCNPGRepository,
		podsPath: clusterSSHCNPGPodsPath, replicaSetPrefix: clusterSSHCNPGReplicaSetPrefix, replicaSetPathValid: clusterSSHCNPGReplicaSetPathValid,
		templateImage: func(object map[string]any) (string, error) {
			return clusterSSHCNPGOperatorTemplateImage(object, bootstrap)
		},
		specValid: func(spec map[string]any, pod bool) bool {
			_, err := clusterSSHCNPGOperatorSpecImage(spec, pod, bootstrap)
			resources, ok := clusterSSHCNPGResourceSpec(spec, pod)
			return err == nil && ok && resources == plan.Envelope
		},
	})
	if _, _, ok := plan.budget(); err != nil || !ok {
		return "", clusterSSHCNPGOperatorResources{}, clusterbootstrap.ErrPreparationConfig
	}
	return resolved, plan, nil
}
