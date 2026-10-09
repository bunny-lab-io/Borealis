package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const clusterSSHSystemUpgradePodsPrefix = "/api/v1/namespaces/system-upgrade/pods?labelSelector=upgrade.cattle.io%2Fcontroller%3Dsystem-upgrade-controller&fieldSelector=spec.nodeName%3D"
const clusterSSHSystemUpgradeReplicaSetPrefix = "/apis/apps/v1/namespaces/system-upgrade/replicasets/"

func clusterSSHSystemUpgradePodsPath(node string) string {
	return clusterSSHSystemUpgradePodsPrefix + node + "&limit=16"
}
func clusterSSHSystemUpgradePodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHSystemUpgradePodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, "&limit=16")
	return ok && clusterSSHStorageName(node)
}
func clusterSSHSystemUpgradeReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHSystemUpgradeReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "system-upgrade-controller-") && clusterSSHStorageName(name)
}
func clusterSSHSystemUpgradeRuntimeImageValid(configured, resolved string) bool {
	if !clusterSSHSystemUpgradeImageValid(configured, "system-upgrade-controller") {
		return false
	}
	if strings.HasPrefix(configured, "rancher/") {
		configured = "docker.io/" + configured
	}
	// Kubelet status preserves the configured spelling; imageID is canonical.
	return (clusterSSHSourceExternalImage{Configured: configured, Resolved: resolved}).valid("docker.io/rancher/system-upgrade-controller")
}
func clusterSSHSystemUpgradeTemplateImage(object map[string]any) (string, error) {
	template := clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template")
	if clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")["upgrade.cattle.io/controller"] != "system-upgrade-controller" {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return clusterSSHSystemUpgradeSpecImage(clusterSSHStorageMap(template, "spec"), false)
}
func clusterSSHSystemUpgradeSpecImage(spec map[string]any, pod bool) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || spec["serviceAccountName"] != "system-upgrade" || spec["hostNetwork"] != nil && spec["hostNetwork"] != false {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "system-upgrade-controller" || !clusterSSHStorageEmptyList(container["command"]) || !clusterSSHStorageEmptyList(container["args"]) || !clusterSSHStorageEmptyList(container["volumeDevices"]) || !clusterSSHStorageEmptyText(container["workingDir"]) || !clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyMap(container["lifecycle"]) || !clusterSSHSystemUpgradeEnvironment(container["env"], container["envFrom"]) {
		return fail()
	}
	mounts, volumes, valid := clusterSSHServiceAccountMounts(container["volumeMounts"], spec["volumes"], 4, pod)
	if !valid || !clusterSSHSystemUpgradeMounts(mounts, volumes) {
		return fail()
	}
	image := clusterSSHStorageText(container, "image")
	if !clusterSSHSystemUpgradeImageValid(image, "system-upgrade-controller") {
		return fail()
	}
	return image, nil
}
