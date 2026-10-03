package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"slices"
	"strings"
)

const clusterSSHLonghornManagerPath = "/apis/apps/v1/namespaces/longhorn-system/daemonsets/longhorn-manager"

// Longhorn 1.12 deprecates the instance/backing-image Settings; those
// controller inputs come from manager arguments. Only these Settings remain
// required here. Never infer their effective values from packaged defaults.
var clusterSSHLonghornImageSettings = []string{"default-engine-image", "support-bundle-manager-image"}

type clusterSSHLonghornManagerImages struct {
	Manager         string `json:"manager"`
	ManagerResolved string `json:"manager_resolved"`
	ShareResolved   string `json:"share_resolved"`
	Engine          string `json:"engine"`
	Instance        string `json:"instance"`
	Share           string `json:"share"`
	Backing         string `json:"backing"`
	SupportBundle   string `json:"support_bundle"`
}

func (images clusterSSHLonghornManagerImages) valid() bool {
	return images.configuredValid() &&
		(clusterSSHSourceExternalImage{Configured: images.Manager, Resolved: images.ManagerResolved}).valid("docker.io/longhornio/longhorn-manager") &&
		(clusterSSHSourceExternalImage{Configured: images.Share, Resolved: images.ShareResolved}).valid("docker.io/longhornio/longhorn-share-manager")
}

func (images clusterSSHLonghornManagerImages) configuredValid() bool {
	for role, reference := range map[string]string{
		"longhorn-manager": images.Manager, "longhorn-engine": images.Engine, "longhorn-instance-manager": images.Instance,
		"longhorn-share-manager": images.Share, "backing-image-manager": images.Backing, "support-bundle-kit": images.SupportBundle,
	} {
		if !clusterSSHLonghornImageValid(reference, role) {
			return false
		}
	}
	return true
}

// Desired configuration, active image Settings and manager Pod image IDs.
// Existing engine generations, mount safety and workload readiness remain separate.
func observeClusterSSHLonghornManagerImages(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, driver clusterSSHLonghornDriverImages) (clusterSSHLonghornManagerImages, error) {
	fail := func() (clusterSSHLonghornManagerImages, error) {
		return clusterSSHLonghornManagerImages{}, clusterbootstrap.ErrPreparationConfig
	}
	object, err := read(clusterSSHLonghornManagerPath)
	id, ok := clusterSSHStorageMetadata(object, "apps/v1", "DaemonSet", "longhorn-system")
	if err != nil || !ok || id.Name != "longhorn-manager" || !driver.valid() {
		return fail()
	}
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 2 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || spec["serviceAccountName"] != "longhorn-service-account" {
		return fail()
	}
	byName := map[string]map[string]any{}
	for _, raw := range containers {
		container, ok := raw.(map[string]any)
		name := clusterSSHStorageText(container, "name")
		if !ok || byName[name] != nil || (name != "longhorn-manager" && name != "pre-pull-share-manager-image") ||
			!clusterSSHStorageEmptyList(container["envFrom"]) || !clusterSSHStorageEmptyList(container["volumeDevices"]) {
			return fail()
		}
		byName[name] = container
	}
	main, share := byName["longhorn-manager"], byName["pre-pull-share-manager-image"]
	images, ok := clusterSSHLonghornManagerCommand(main)
	if !ok || !images.configuredValid() || images.Manager != driver.Manager || clusterSSHStorageText(main, "image") != images.Manager ||
		clusterSSHStorageText(share, "image") != images.Share || !clusterSSHLonghornManagerEnvironment(main["env"]) || !clusterSSHLonghornManagerMounts(main["volumeMounts"]) {
		return fail()
	}
	command, ok := clusterSSHLonghornDriverStrings(share["command"])
	if !ok || !slices.Equal(command, []string{"sh", "-c", "echo share-manager image pulled && sleep infinity"}) ||
		!clusterSSHStorageEmptyList(share["args"]) || !clusterSSHStorageEmptyList(share["env"]) || !clusterSSHStorageEmptyList(share["volumeMounts"]) {
		return fail()
	}
	for name, want := range map[string]string{"default-engine-image": images.Engine, "support-bundle-manager-image": images.SupportBundle} {
		setting, err := read(clusterSSHStorageSettingPrefix + name)
		id, ok := clusterSSHStorageMetadata(setting, "longhorn.io/v1beta2", "Setting", "longhorn-system")
		if err != nil || !ok || id.Name != name || clusterSSHStorageText(setting, "value") != want {
			return fail()
		}
	}
	images.ManagerResolved, images.ShareResolved, err = observeClusterSSHLonghornManagerRuntime(readList, source, id, images)
	if err != nil || !images.valid() {
		return fail()
	}
	return images, nil
}

func clusterSSHLonghornManagerCommand(container map[string]any) (clusterSSHLonghornManagerImages, bool) {
	fail := func() (clusterSSHLonghornManagerImages, bool) { return clusterSSHLonghornManagerImages{}, false }
	command, ok := container["command"].([]any)
	if !ok || len(command) == 0 || len(command) > 32 {
		return fail()
	}
	args := []any{}
	if raw := container["args"]; raw != nil {
		args, ok = raw.([]any)
		if !ok || len(args) > 32 {
			return fail()
		}
	}
	command = append(slices.Clone(command), args...)
	if len(command) < 11 || len(command) > 18 || command[0] != "longhorn-manager" || command[1] != "-d" || command[2] != "daemon" {
		return fail()
	}
	images := clusterSSHLonghornManagerImages{}
	wanted := map[string]*string{"--manager-image": &images.Manager, "--engine-image": &images.Engine, "--instance-manager-image": &images.Instance,
		"--share-manager-image": &images.Share, "--backing-image-manager-image": &images.Backing, "--support-bundle-manager-image": &images.SupportBundle}
	service, upgrade := false, false
	for i := 3; i < len(command); i++ {
		arg, ok := command[i].(string)
		if !ok || len(arg) > 512 {
			return fail()
		}
		if arg == "--upgrade-version-check" {
			if upgrade {
				return fail()
			}
			upgrade = true
			continue
		}
		flag, value, equals := strings.Cut(arg, "=")
		if !equals {
			i++
			if i >= len(command) {
				return fail()
			}
			value, ok = command[i].(string)
		}
		if !ok || len(value) == 0 || len(value) > 256 {
			return fail()
		}
		if flag == "--service-account" {
			if service || value != "longhorn-service-account" {
				return fail()
			}
			service = true
			continue
		}
		destination, known := wanted[flag]
		if !known {
			return fail()
		}
		*destination = value
		delete(wanted, flag)
	}
	return images, len(wanted) == 0 && service && upgrade
}

func clusterSSHLonghornManagerEnvironment(raw any) bool {
	env, ok := raw.([]any)
	if !ok || len(env) != 5 {
		return false
	}
	fields := map[string]string{"POD_NAME": "metadata.name", "POD_NAMESPACE": "metadata.namespace", "POD_IP": "status.podIP", "NODE_NAME": "spec.nodeName"}
	distro := false
	for _, rawEntry := range env {
		entry, ok := rawEntry.(map[string]any)
		if !ok || len(entry) != 2 {
			return false
		}
		name := clusterSSHStorageText(entry, "name")
		if name == "LONGHORN_DISTRO" {
			if distro || entry["value"] != "longhorn" {
				return false
			}
			distro = true
			continue
		}
		path, known := fields[name]
		from := clusterSSHStorageMap(entry, "valueFrom")
		field := clusterSSHStorageMap(from, "fieldRef")
		if !known || len(from) != 1 || field["fieldPath"] != path || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
			return false
		}
		delete(fields, name)
	}
	return distro && len(fields) == 0
}

// Permit the packaged host/TLS mount destinations, with no subpath or other
// environment expansion that could replace the executable/configuration.
// This is not evidence of the underlying host paths' safety or contents.
func clusterSSHLonghornManagerMounts(raw any) bool {
	mounts, ok := raw.([]any)
	if !ok || len(mounts) != 6 {
		return false
	}
	wanted := map[string]string{"boot": "/host/boot/", "dev": "/host/dev/", "proc": "/host/proc/", "etc": "/host/etc/", "longhorn": "/var/lib/longhorn/", "longhorn-grpc-tls": "/tls-files/"}
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
		if name == "boot" || name == "proc" || name == "etc" {
			if mount["readOnly"] != true {
				return false
			}
		} else if value, exists := mount["readOnly"]; exists && value != false {
			return false
		}
		if name == "longhorn" {
			if mount["mountPropagation"] != "Bidirectional" {
				return false
			}
		} else if !clusterSSHStorageEmptyText(mount["mountPropagation"]) && mount["mountPropagation"] != "None" {
			return false
		}
		delete(wanted, name)
	}
	return len(wanted) == 0
}
