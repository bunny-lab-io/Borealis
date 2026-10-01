package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"slices"
	"strings"
)

const clusterSSHLonghornDriverPath = "/apis/apps/v1/namespaces/longhorn-system/deployments/longhorn-driver-deployer"

// This is the reviewed init-container command, compared as inert text only.
const clusterSSHLonghornDriverWait = `while [ $(curl -m 1 -s -o /dev/null -w "%{http_code}" http://longhorn-backend:9500/v1) != "200" ]; do echo waiting; sleep 2; done`

// Desired driver-deployer inputs, not proof of running CSI/manager images,
// effective Longhorn Settings, workload fit or readiness.
type clusterSSHLonghornDriverImages struct {
	Manager     string `json:"manager"`
	Attacher    string `json:"attacher"`
	Provisioner string `json:"provisioner"`
	Registrar   string `json:"registrar"`
	Resizer     string `json:"resizer"`
	Snapshotter string `json:"snapshotter"`
	Liveness    string `json:"liveness"`
}

func (images clusterSSHLonghornDriverImages) valid() bool {
	for role, reference := range map[string]string{
		"longhorn-manager": images.Manager, "csi-attacher": images.Attacher,
		"csi-provisioner": images.Provisioner, "csi-node-driver-registrar": images.Registrar,
		"csi-resizer": images.Resizer, "csi-snapshotter": images.Snapshotter, "livenessprobe": images.Liveness,
	} {
		repository := "docker.io/longhornio/" + role
		found := false
		for _, pin := range clusterbootstrap.ExternalImagePins() {
			if strings.HasPrefix(pin.Reference, repository+":") &&
				(reference == pin.Reference || reference == repository+"@"+pin.IndexDigest || reference == repository+"@"+pin.ManifestDigest) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func observeClusterSSHLonghornDriverImages(read func(string) (map[string]any, error)) (clusterSSHLonghornDriverImages, error) {
	fail := func() (clusterSSHLonghornDriverImages, error) {
		return clusterSSHLonghornDriverImages{}, clusterbootstrap.ErrPreparationConfig
	}
	object, err := read(clusterSSHLonghornDriverPath)
	metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "longhorn-system")
	if err != nil || !ok || metadata.Name != "longhorn-driver-deployer" {
		return fail()
	}
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, mainOK := spec["containers"].([]any)
	init, initOK := spec["initContainers"].([]any)
	if !mainOK || len(containers) != 1 || !initOK || len(init) != 1 || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	main, mainOK := containers[0].(map[string]any)
	wait, initOK := init[0].(map[string]any)
	if !mainOK || !initOK || main["name"] != "longhorn-driver-deployer" || wait["name"] != "wait-longhorn-manager" || clusterSSHStorageText(main, "image") != clusterSSHStorageText(wait, "image") {
		return fail()
	}
	for _, container := range []map[string]any{main, wait} {
		if !clusterSSHStorageEmptyList(container["envFrom"]) || !clusterSSHStorageEmptyList(container["volumeMounts"]) ||
			!clusterSSHStorageEmptyList(container["volumeDevices"]) || !clusterSSHStorageEmptyText(container["restartPolicy"]) {
			return fail()
		}
	}
	command, ok := clusterSSHLonghornDriverStrings(wait["command"])
	if !ok || !slices.Equal(command, []string{"sh", "-c", clusterSSHLonghornDriverWait}) ||
		!clusterSSHStorageEmptyList(wait["args"]) || !clusterSSHStorageEmptyList(wait["env"]) {
		return fail()
	}
	manager := clusterSSHStorageText(main, "image")
	if !clusterSSHLonghornDriverCommand(main, manager) {
		return fail()
	}
	env, ok := clusterSSHLonghornDriverEnvironment(main["env"])
	if !ok {
		return fail()
	}
	images := clusterSSHLonghornDriverImages{Manager: manager,
		Attacher: env["CSI_ATTACHER_IMAGE"], Provisioner: env["CSI_PROVISIONER_IMAGE"], Registrar: env["CSI_NODE_DRIVER_REGISTRAR_IMAGE"],
		Resizer: env["CSI_RESIZER_IMAGE"], Snapshotter: env["CSI_SNAPSHOTTER_IMAGE"], Liveness: env["CSI_LIVENESS_PROBE_IMAGE"],
	}
	if !images.valid() {
		return fail()
	}
	return images, nil
}

func clusterSSHLonghornDriverStrings(raw any) ([]string, bool) {
	if raw == nil {
		return nil, true
	}
	values, ok := raw.([]any)
	if !ok || len(values) > 16 {
		return nil, false
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok || len(text) > 512 {
			return nil, false
		}
		result[i] = text
	}
	return result, true
}

func clusterSSHLonghornDriverCommand(container map[string]any, manager string) bool {
	command, ok := clusterSSHLonghornDriverStrings(container["command"])
	args, argsOK := clusterSSHLonghornDriverStrings(container["args"])
	if !ok || !argsOK || len(command) == 0 {
		return false
	}
	command = append(command, args...)
	if len(command) < 5 || len(command) > 7 || !slices.Equal(command[:3], []string{"longhorn-manager", "-d", "deploy-driver"}) {
		return false
	}
	wanted := map[string]string{"--manager-image": manager, "--manager-url": "http://longhorn-backend:9500/v1"}
	for i := 3; i < len(command); i++ {
		flag, value, equals := strings.Cut(command[i], "=")
		if !equals {
			i++
			if i >= len(command) {
				return false
			}
			value = command[i]
		}
		expected, known := wanted[flag]
		if !known || value != expected {
			return false
		}
		delete(wanted, flag)
	}
	return len(wanted) == 0
}

// Only the pinned manifest's literal CSI images and three downward API fields
// are supported. Defaults, valueFrom image lookups and envFrom are not evidence.
func clusterSSHLonghornDriverEnvironment(raw any) (map[string]string, bool) {
	env, ok := raw.([]any)
	if !ok || len(env) != 9 {
		return nil, false
	}
	fields := map[string]string{"POD_NAMESPACE": "metadata.namespace", "NODE_NAME": "spec.nodeName", "SERVICE_ACCOUNT": "spec.serviceAccountName"}
	images := []string{"CSI_ATTACHER_IMAGE", "CSI_PROVISIONER_IMAGE", "CSI_NODE_DRIVER_REGISTRAR_IMAGE", "CSI_RESIZER_IMAGE", "CSI_SNAPSHOTTER_IMAGE", "CSI_LIVENESS_PROBE_IMAGE"}
	result := map[string]string{}
	seen := map[string]bool{}
	for _, rawEntry := range env {
		entry, ok := rawEntry.(map[string]any)
		name := clusterSSHStorageText(entry, "name")
		if !ok || len(entry) != 2 || seen[name] {
			return nil, false
		}
		seen[name] = true
		if path, exists := fields[name]; exists {
			from := clusterSSHStorageMap(entry, "valueFrom")
			field := clusterSSHStorageMap(from, "fieldRef")
			if len(from) != 1 || field["fieldPath"] != path || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
				return nil, false
			}
		} else if slices.Contains(images, name) {
			value, ok := entry["value"].(string)
			if !ok || value == "" || len(value) > 256 {
				return nil, false
			}
			result[name] = value
		} else {
			return nil, false
		}
	}
	return result, len(result) == 6
}
