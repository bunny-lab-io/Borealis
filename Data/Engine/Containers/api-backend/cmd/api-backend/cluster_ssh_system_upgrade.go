package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const (
	clusterSSHSystemUpgradePath       = "/apis/apps/v1/namespaces/system-upgrade/deployments/system-upgrade-controller"
	clusterSSHSystemUpgradeConfigPath = "/api/v1/namespaces/system-upgrade/configmaps/default-controller-env"
)

// Desired controller/drain-helper inputs only. Running identities, upgrade Plan
// images, trust-directory contents and workload budgets remain separate evidence.
type clusterSSHSystemUpgradeImages struct {
	Controller string `json:"controller"`
	Kubectl    string `json:"kubectl"`
}

func clusterSSHSystemUpgradeImageValid(reference, role string) bool {
	if len(reference) > 256 {
		return false
	}
	// The reviewed manifest uses Docker Hub's rancher/... shorthand. Preserve
	// the observed spelling in receipts/projection; only these two spellings match.
	if strings.HasPrefix(reference, "rancher/") {
		reference = "docker.io/" + reference
	}
	repository := "docker.io/rancher/" + role
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, repository+":") && (reference == pin.Reference || reference == repository+"@"+pin.IndexDigest || reference == repository+"@"+pin.ManifestDigest) {
			return true
		}
	}
	return false
}

func (images clusterSSHSystemUpgradeImages) valid() bool {
	return clusterSSHSystemUpgradeImageValid(images.Controller, "system-upgrade-controller") && clusterSSHSystemUpgradeImageValid(images.Kubectl, "kubectl")
}

func observeClusterSSHSystemUpgradeImages(read func(string) (map[string]any, error)) (clusterSSHSystemUpgradeImages, error) {
	fail := func() (clusterSSHSystemUpgradeImages, error) {
		return clusterSSHSystemUpgradeImages{}, clusterbootstrap.ErrPreparationConfig
	}
	object, err := read(clusterSSHSystemUpgradePath)
	metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "system-upgrade")
	if err != nil || !ok || metadata.Name != "system-upgrade-controller" {
		return fail()
	}
	template := clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template")
	labels := clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")
	if labels["upgrade.cattle.io/controller"] != "system-upgrade-controller" {
		return fail()
	}
	spec := clusterSSHStorageMap(template, "spec")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "system-upgrade-controller" || !clusterSSHStorageEmptyList(container["command"]) ||
		!clusterSSHStorageEmptyList(container["args"]) || !clusterSSHStorageEmptyList(container["volumeDevices"]) ||
		!clusterSSHStorageEmptyText(container["workingDir"]) || !clusterSSHStorageEmptyText(container["restartPolicy"]) ||
		!clusterSSHStorageEmptyMap(container["lifecycle"]) || !clusterSSHSystemUpgradeEnvironment(container["env"], container["envFrom"]) ||
		!clusterSSHSystemUpgradeMounts(container["volumeMounts"], spec["volumes"]) {
		return fail()
	}
	config, err := read(clusterSSHSystemUpgradeConfigPath)
	metadata, ok = clusterSSHStorageMetadata(config, "v1", "ConfigMap", "system-upgrade")
	if err != nil || !ok || metadata.Name != "default-controller-env" || !clusterSSHStorageEmptyMap(config["binaryData"]) {
		return fail()
	}
	data := clusterSSHStorageMap(config, "data")
	wanted := map[string]string{
		"SYSTEM_UPGRADE_CONTROLLER_DEBUG": "false", "SYSTEM_UPGRADE_CONTROLLER_LEADER_ELECT": "true", "SYSTEM_UPGRADE_CONTROLLER_THREADS": "2",
		"SYSTEM_UPGRADE_JOB_ACTIVE_DEADLINE_SECONDS": "900", "SYSTEM_UPGRADE_JOB_BACKOFF_LIMIT": "99", "SYSTEM_UPGRADE_JOB_IMAGE_PULL_POLICY": "Always",
		"SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE_WINDOWS": "", "SYSTEM_UPGRADE_JOB_PRIVILEGED": "true", "SYSTEM_UPGRADE_JOB_TTL_SECONDS_AFTER_FINISH": "900", "SYSTEM_UPGRADE_PLAN_POLLING_INTERVAL": "15m",
	}
	if len(data) != len(wanted)+1 {
		return fail()
	}
	for key, value := range wanted {
		if data[key] != value {
			return fail()
		}
	}
	images := clusterSSHSystemUpgradeImages{Controller: clusterSSHStorageText(container, "image"), Kubectl: clusterSSHStorageText(data, "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE")}
	if !images.valid() {
		return fail()
	}
	return images, nil
}

func clusterSSHSystemUpgradeEnvironment(rawEnv, rawFrom any) bool {
	from, ok := rawFrom.([]any)
	if !ok || len(from) != 1 {
		return false
	}
	entry, ok := from[0].(map[string]any)
	if !ok || !(len(entry) == 1 || len(entry) == 2 && entry["prefix"] == "") {
		return false
	}
	ref := clusterSSHStorageMap(entry, "configMapRef")
	if ref["name"] != "default-controller-env" || !(len(ref) == 1 || len(ref) == 2 && ref["optional"] == false) {
		return false
	}
	env, ok := rawEnv.([]any)
	fields := map[string]string{"SYSTEM_UPGRADE_CONTROLLER_NAME": "metadata.labels['upgrade.cattle.io/controller']", "SYSTEM_UPGRADE_CONTROLLER_NAMESPACE": "metadata.namespace", "SYSTEM_UPGRADE_CONTROLLER_NODE_NAME": "spec.nodeName"}
	if !ok || len(env) != len(fields) {
		return false
	}
	for _, rawEntry := range env {
		entry, ok := rawEntry.(map[string]any)
		name := clusterSSHStorageText(entry, "name")
		path, known := fields[name]
		from := clusterSSHStorageMap(entry, "valueFrom")
		field := clusterSSHStorageMap(from, "fieldRef")
		if !ok || len(entry) != 2 || !known || len(from) != 1 || field["fieldPath"] != path || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
			return false
		}
		delete(fields, name)
	}
	return len(fields) == 0
}

// Match reviewed TLS host directories and private /tmp emptyDir. This neither
// reads nor qualifies host trust contents, and never creates host directories.
func clusterSSHSystemUpgradeMounts(rawMounts, rawVolumes any) bool {
	mounts, mountsOK := rawMounts.([]any)
	volumes, volumesOK := rawVolumes.([]any)
	wanted := map[string]string{"etc-ssl": "/etc/ssl", "etc-pki": "/etc/pki", "etc-ca-certificates": "/etc/ca-certificates", "tmp": "/tmp"}
	if !mountsOK || !volumesOK || len(mounts) != len(wanted) || len(volumes) != len(wanted) {
		return false
	}
	seen := map[string]bool{}
	for _, rawVolume := range volumes {
		volume, ok := rawVolume.(map[string]any)
		name := clusterSSHStorageText(volume, "name")
		path, known := wanted[name]
		if !ok || len(volume) != 2 || !known || seen[name] {
			return false
		}
		seen[name] = true
		if name == "tmp" {
			emptyDir, ok := volume["emptyDir"].(map[string]any)
			if !ok || len(emptyDir) != 0 {
				return false
			}
		} else {
			host := clusterSSHStorageMap(volume, "hostPath")
			if len(host) != 2 || host["path"] != path || host["type"] != "DirectoryOrCreate" {
				return false
			}
		}
	}
	for _, rawMount := range mounts {
		mount, ok := rawMount.(map[string]any)
		name := clusterSSHStorageText(mount, "name")
		path, known := wanted[name]
		if !ok || !known || mount["mountPath"] != path {
			return false
		}
		for key := range mount {
			if key != "name" && key != "mountPath" && key != "readOnly" && key != "mountPropagation" {
				return false
			}
		}
		if name == "tmp" {
			if value, exists := mount["readOnly"]; exists && value != false {
				return false
			}
		} else if mount["readOnly"] != true {
			return false
		}
		if value, exists := mount["mountPropagation"]; exists && value != "None" {
			return false
		}
		delete(wanted, name)
	}
	return len(wanted) == 0
}
