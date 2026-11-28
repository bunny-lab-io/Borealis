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
	return clusterSSHSnapshotControllerTemplateImage(object)
}

func clusterSSHSnapshotControllerTemplateImage(object map[string]any) (string, error) {
	template := clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template")
	if clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")["app.kubernetes.io/name"] != "snapshot-controller" {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return clusterSSHSnapshotControllerSpecImage(clusterSSHStorageMap(template, "spec"), false)
}

func clusterSSHSnapshotControllerSpecImage(spec map[string]any, pod bool) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	if spec["serviceAccountName"] != "snapshot-controller" || spec["hostNetwork"] != nil && spec["hostNetwork"] != false {
		return fail()
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "snapshot-controller" || !clusterSSHStorageEmptyList(container["command"]) ||
		!clusterSSHStorageEmptyList(container["env"]) || !clusterSSHStorageEmptyList(container["envFrom"]) ||
		!clusterSSHStorageEmptyList(container["volumeDevices"]) || !clusterSSHStorageEmptyText(container["workingDir"]) ||
		!clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyMap(container["lifecycle"]) ||
		!clusterSSHSnapshotControllerArguments(container["args"]) {
		return fail()
	}
	mounts, volumes, ok := clusterSSHServiceAccountMounts(container["volumeMounts"], spec["volumes"], 0, pod)
	if !ok || len(mounts) != 0 || len(volumes) != 0 {
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
			// Boolean flags need explicit values; do not interpret a following
			// positional token as the reviewed leader-election value.
			if flag == "--leader-election" {
				return false
			}
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
