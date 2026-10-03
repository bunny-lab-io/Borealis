package main

import "borealis/api-backend/internal/clusterbootstrap"

const clusterSSHLonghornUIPath = "/apis/apps/v1/namespaces/longhorn-system/deployments/longhorn-ui"

// Observe desired image/configuration only. Running Pod image identity, emptyDir
// demand and workload fit remain separate evidence; this grants no readiness.
func observeClusterSSHLonghornUIImage(read func(string) (map[string]any, error)) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	object, err := read(clusterSSHLonghornUIPath)
	metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "longhorn-system")
	if err != nil || !ok || metadata.Name != "longhorn-ui" {
		return fail()
	}
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "longhorn-ui" || !clusterSSHStorageEmptyList(container["command"]) ||
		!clusterSSHStorageEmptyList(container["args"]) || !clusterSSHStorageEmptyList(container["envFrom"]) ||
		!clusterSSHStorageEmptyList(container["volumeDevices"]) || !clusterSSHStorageEmptyText(container["workingDir"]) ||
		!clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyMap(container["lifecycle"]) ||
		!clusterSSHLonghornUIEnvironment(container["env"]) || !clusterSSHLonghornUIMounts(container["volumeMounts"], spec["volumes"]) {
		return fail()
	}
	image := clusterSSHStorageText(container, "image")
	if !clusterSSHLonghornImageValid(image, "longhorn-ui") {
		return fail()
	}
	return image, nil
}

func clusterSSHLonghornUIEnvironment(raw any) bool {
	env, ok := raw.([]any)
	wanted := map[string]string{"LONGHORN_MANAGER_IP": "http://longhorn-backend:9500", "LONGHORN_UI_PORT": "8000"}
	if !ok || len(env) != len(wanted) {
		return false
	}
	for _, rawEntry := range env {
		entry, ok := rawEntry.(map[string]any)
		name := clusterSSHStorageText(entry, "name")
		value, known := wanted[name]
		if !ok || !known || len(entry) != 2 || entry["value"] != value {
			return false
		}
		delete(wanted, name)
	}
	return len(wanted) == 0
}

// Only the reviewed manifest's three disk-backed emptyDirs may supply nginx
// writable paths. Reject injected configuration, executable mounts and subpaths.
func clusterSSHLonghornUIMounts(rawMounts, rawVolumes any) bool {
	mounts, mountsOK := rawMounts.([]any)
	volumes, volumesOK := rawVolumes.([]any)
	wanted := map[string]string{"nginx-cache": "/var/cache/nginx/", "nginx-config": "/var/config/nginx/", "var-run": "/var/run/"}
	if !mountsOK || !volumesOK || len(mounts) != len(wanted) || len(volumes) != len(wanted) {
		return false
	}
	seen := map[string]bool{}
	for _, rawVolume := range volumes {
		volume, ok := rawVolume.(map[string]any)
		name := clusterSSHStorageText(volume, "name")
		_, known := wanted[name]
		emptyDir, disk := volume["emptyDir"].(map[string]any)
		if !ok || !known || seen[name] || len(volume) != 2 || !disk || len(emptyDir) != 0 {
			return false
		}
		seen[name] = true
	}
	for _, rawMount := range mounts {
		mount, ok := rawMount.(map[string]any)
		name := clusterSSHStorageText(mount, "name")
		path, known := wanted[name]
		if !ok || !known || mount["mountPath"] != path {
			return false
		}
		for key, value := range mount {
			switch key {
			case "name", "mountPath":
			case "readOnly":
				if value != false {
					return false
				}
			case "mountPropagation":
				if value != "None" {
					return false
				}
			default:
				return false
			}
		}
		delete(wanted, name)
	}
	return len(wanted) == 0
}
