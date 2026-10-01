package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const clusterSSHSnapshotControllerPath = "/apis/apps/v1/namespaces/kube-system/deployments/snapshot-controller"
const clusterSSHSnapshotControllerRepository = "registry.k8s.io/sig-storage/snapshot-controller"

func clusterSSHSnapshotControllerImageValid(reference string) bool {
	if len(reference) > 256 {
		return false
	}
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, clusterSSHSnapshotControllerRepository+"@") &&
			(reference == pin.Reference || reference == clusterSSHSnapshotControllerRepository+"@"+pin.IndexDigest || reference == clusterSSHSnapshotControllerRepository+"@"+pin.ManifestDigest) {
			return true
		}
	}
	return false
}

// Desired Deployment input only, not running image identity, snapshot health,
// storage demand, controller permissions or readiness.
func observeClusterSSHSnapshotControllerImage(read func(string) (map[string]any, error)) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	object, err := read(clusterSSHSnapshotControllerPath)
	metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "kube-system")
	if err != nil || !ok || metadata.Name != "snapshot-controller" {
		return fail()
	}
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || !clusterSSHStorageEmptyList(spec["volumes"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "snapshot-controller" || !clusterSSHStorageEmptyList(container["command"]) ||
		!clusterSSHStorageEmptyList(container["env"]) || !clusterSSHStorageEmptyList(container["envFrom"]) ||
		!clusterSSHStorageEmptyList(container["volumeMounts"]) || !clusterSSHStorageEmptyList(container["volumeDevices"]) ||
		!clusterSSHStorageEmptyText(container["workingDir"]) || !clusterSSHStorageEmptyText(container["restartPolicy"]) ||
		!clusterSSHStorageEmptyMap(container["lifecycle"]) || !clusterSSHSnapshotControllerArguments(container["args"]) {
		return fail()
	}
	image := clusterSSHStorageText(container, "image")
	if !clusterSSHSnapshotControllerImageValid(image) {
		return fail()
	}
	return image, nil
}

func clusterSSHSnapshotControllerArguments(raw any) bool {
	args, ok := raw.([]any)
	wanted := map[string]string{"--v": "2", "--leader-election": "true", "--leader-election-namespace": "kube-system", "--http-endpoint": ":8080"}
	if !ok || len(args) < len(wanted) || len(args) > 2*len(wanted) {
		return false
	}
	for i := 0; i < len(args); i++ {
		arg, ok := args[i].(string)
		if !ok {
			return false
		}
		flag, value, equals := strings.Cut(arg, "=")
		if !equals {
			i++
			if i >= len(args) {
				return false
			}
			value, ok = args[i].(string)
			if !ok {
				return false
			}
		}
		expected, known := wanted[flag]
		if !known || value != expected {
			return false
		}
		delete(wanted, flag)
	}
	return len(wanted) == 0
}
