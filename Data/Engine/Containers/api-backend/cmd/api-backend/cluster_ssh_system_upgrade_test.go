package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func sshSystemUpgradePin(role string) clusterbootstrap.ExternalImagePin {
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, "docker.io/rancher/"+role+":") {
			return pin
		}
	}
	panic("missing upgrade fixture pin")
}

// Mirrors the reviewed v0.20.1 Deployment and default-controller-env ConfigMap.
func sshSystemUpgradeFixture(f *sshStorageFixture) {
	env := []any{}
	for _, field := range [][2]string{{"SYSTEM_UPGRADE_CONTROLLER_NAME", "metadata.labels['upgrade.cattle.io/controller']"}, {"SYSTEM_UPGRADE_CONTROLLER_NAMESPACE", "metadata.namespace"}, {"SYSTEM_UPGRADE_CONTROLLER_NODE_NAME", "spec.nodeName"}} {
		env = append(env, map[string]any{"name": field[0], "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": field[1]}}})
	}
	mounts, volumes := []any{}, []any{}
	for _, entry := range [][2]string{{"etc-ssl", "/etc/ssl"}, {"etc-pki", "/etc/pki"}, {"etc-ca-certificates", "/etc/ca-certificates"}} {
		mounts = append(mounts, map[string]any{"name": entry[0], "mountPath": entry[1], "readOnly": true})
		volumes = append(volumes, map[string]any{"name": entry[0], "hostPath": map[string]any{"path": entry[1], "type": "DirectoryOrCreate"}})
	}
	mounts = append(mounts, map[string]any{"name": "tmp", "mountPath": "/tmp"})
	volumes = append(volumes, map[string]any{"name": "tmp", "emptyDir": map[string]any{}})
	deployment := sshStorageObject("apps/v1", "Deployment", "system-upgrade", "system-upgrade-controller")
	deployment["spec"] = map[string]any{"template": map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"upgrade.cattle.io/controller": "system-upgrade-controller"}},
		"spec": map[string]any{"serviceAccountName": "system-upgrade", "containers": []any{map[string]any{"name": "system-upgrade-controller", "image": "rancher/system-upgrade-controller:v0.20.1", "env": env,
			"envFrom": []any{map[string]any{"configMapRef": map[string]any{"name": "default-controller-env"}}}, "volumeMounts": mounts}}, "volumes": volumes},
	}}
	config := sshStorageObject("v1", "ConfigMap", "system-upgrade", "default-controller-env")
	config["data"] = map[string]any{
		"SYSTEM_UPGRADE_CONTROLLER_DEBUG": "false", "SYSTEM_UPGRADE_CONTROLLER_LEADER_ELECT": "true", "SYSTEM_UPGRADE_CONTROLLER_THREADS": "2",
		"SYSTEM_UPGRADE_JOB_ACTIVE_DEADLINE_SECONDS": "900", "SYSTEM_UPGRADE_JOB_BACKOFF_LIMIT": "99", "SYSTEM_UPGRADE_JOB_IMAGE_PULL_POLICY": "Always",
		"SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE": "rancher/kubectl:v1.30.3", "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE_WINDOWS": "", "SYSTEM_UPGRADE_JOB_PRIVILEGED": "true",
		"SYSTEM_UPGRADE_JOB_TTL_SECONDS_AFTER_FINISH": "900", "SYSTEM_UPGRADE_PLAN_POLLING_INTERVAL": "15m",
	}
	f.objects[clusterSSHSystemUpgradePath], f.objects[clusterSSHSystemUpgradeConfigPath] = deployment, config
}

func sshSystemUpgradeSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHSystemUpgradePath], "spec"), "template"), "spec")
}

func TestClusterSSHSystemUpgradeSourceConfiguration(t *testing.T) {
	for _, mode := range []string{"packaged aliases", "canonical tags", "index digests", "platform digests", "shorthand digests", "default fields", "reordered inputs",
		"missing deployment", "missing config", "deployment namespace", "config namespace", "config name", "config UID", "config revision", "config deleting", "config kind", "controller label",
		"sidecar", "init", "ephemeral", "container name", "command", "args", "working directory", "lifecycle", "device", "controller image", "role mismatch", "foreign registry",
		"missing env", "duplicate env", "literal env", "wrong field", "image env override", "missing envFrom", "extra envFrom", "secret envFrom", "optional config", "wrong config", "prefix",
		"missing data", "missing kubectl", "kubectl image", "kubectl type", "unknown config", "binary config", "Windows helper", "config type", "custom threads", "indirect kubectl",
		"missing mounts", "duplicate mount", "executable mount", "writable trust", "readOnly tmp", "subpath", "propagation", "missing volumes", "duplicate volume", "host path", "host type", "config volume", "memory tmp"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec := sshSystemUpgradeSpec(f)
			container := spec["containers"].([]any)[0].(map[string]any)
			config := f.objects[clusterSSHSystemUpgradeConfigPath]
			meta, data := clusterSSHStorageMap(config, "metadata"), clusterSSHStorageMap(config, "data")
			env, from := container["env"].([]any), container["envFrom"].([]any)
			ref := clusterSSHStorageMap(from[0].(map[string]any), "configMapRef")
			mounts, volumes := container["volumeMounts"].([]any), spec["volumes"].([]any)
			mount, volume := mounts[0].(map[string]any), volumes[0].(map[string]any)
			valid := false
			switch mode {
			case "packaged aliases":
				valid = true
			case "canonical tags", "index digests", "platform digests", "shorthand digests":
				valid = true
				for _, role := range []string{"system-upgrade-controller", "kubectl"} {
					pin := sshSystemUpgradePin(role)
					value := pin.Reference
					if mode != "canonical tags" {
						digest := pin.ManifestDigest
						if mode == "index digests" {
							digest = pin.IndexDigest
						}
						value = "docker.io/rancher/" + role + "@" + digest
						if mode == "shorthand digests" {
							value = strings.TrimPrefix(value, "docker.io/")
						}
					}
					if role == "kubectl" {
						data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] = value
					} else {
						container["image"] = value
					}
				}
			case "default fields":
				valid = true
				ref["optional"] = false
				from[0].(map[string]any)["prefix"] = ""
				mount["mountPropagation"] = "None"
				clusterSSHStorageMap(clusterSSHStorageMap(env[0].(map[string]any), "valueFrom"), "fieldRef")["apiVersion"] = "v1"
			case "reordered inputs":
				valid = true
				env[0], env[2] = env[2], env[0]
				mounts[0], mounts[3] = mounts[3], mounts[0]
				volumes[0], volumes[3] = volumes[3], volumes[0]
			case "missing deployment":
				delete(f.objects, clusterSSHSystemUpgradePath)
			case "missing config":
				delete(f.objects, clusterSSHSystemUpgradeConfigPath)
			case "deployment namespace":
				clusterSSHStorageMap(f.objects[clusterSSHSystemUpgradePath], "metadata")["namespace"] = "other"
			case "config namespace":
				meta["namespace"] = "other"
			case "config name":
				meta["name"] = "other"
			case "config UID":
				delete(meta, "uid")
			case "config revision":
				delete(meta, "resourceVersion")
			case "config deleting":
				meta["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "config kind":
				config["kind"] = "Secret"
			case "controller label":
				clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHSystemUpgradePath], "spec"), "template"), "metadata")["labels"] = map[string]any{"upgrade.cattle.io/controller": "other"}
			case "sidecar":
				spec["containers"] = []any{container, container}
			case "init":
				spec["initContainers"] = []any{container}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{container}
			case "container name":
				container["name"] = "other"
			case "command", "args":
				container[mode] = []any{"other"}
			case "working directory":
				container["workingDir"] = "/other"
			case "lifecycle":
				container["lifecycle"] = map[string]any{"postStart": map[string]any{"exec": map[string]any{"command": []any{"other"}}}}
			case "device":
				container["volumeDevices"] = []any{map[string]any{"name": "other"}}
			case "controller image":
				container["image"] = "rancher/system-upgrade-controller:other"
			case "role mismatch":
				container["image"] = data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"]
			case "foreign registry":
				container["image"] = "other/rancher/system-upgrade-controller:v0.20.1"
			case "missing env":
				delete(container, "env")
			case "duplicate env":
				env[1] = env[0]
			case "literal env":
				env[0] = map[string]any{"name": "SYSTEM_UPGRADE_CONTROLLER_NAME", "value": "system-upgrade-controller"}
			case "wrong field":
				clusterSSHStorageMap(clusterSSHStorageMap(env[0].(map[string]any), "valueFrom"), "fieldRef")["fieldPath"] = "metadata.name"
			case "image env override":
				container["env"] = append(env, map[string]any{"name": "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE", "value": "other"})
			case "missing envFrom":
				delete(container, "envFrom")
			case "extra envFrom":
				container["envFrom"] = append(from, from[0])
			case "secret envFrom":
				from[0] = map[string]any{"secretRef": ref}
			case "optional config":
				ref["optional"] = true
			case "wrong config":
				ref["name"] = "other"
			case "prefix":
				from[0].(map[string]any)["prefix"] = "OTHER_"
			case "missing data":
				delete(config, "data")
			case "missing kubectl":
				delete(data, "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE")
			case "kubectl image":
				data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] = "rancher/kubectl:other"
			case "kubectl type":
				data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] = map[string]any{}
			case "unknown config":
				data["OTHER"] = "value"
			case "binary config":
				config["binaryData"] = map[string]any{"OTHER": "dmFsdWU="}
			case "Windows helper":
				data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE_WINDOWS"] = data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"]
			case "config type":
				data["SYSTEM_UPGRADE_JOB_PRIVILEGED"] = true
			case "custom threads":
				data["SYSTEM_UPGRADE_CONTROLLER_THREADS"] = "4"
			case "indirect kubectl":
				data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] = "$(IMAGE)"
			case "missing mounts":
				delete(container, "volumeMounts")
			case "duplicate mount":
				mounts[1] = mounts[0]
			case "executable mount":
				mount["mountPath"] = "/bin"
			case "writable trust":
				mount["readOnly"] = false
			case "readOnly tmp":
				mounts[3].(map[string]any)["readOnly"] = true
			case "subpath":
				mount["subPath"] = "other"
			case "propagation":
				mount["mountPropagation"] = "Bidirectional"
			case "missing volumes":
				delete(spec, "volumes")
			case "duplicate volume":
				volumes[1] = volumes[0]
			case "host path":
				clusterSSHStorageMap(volume, "hostPath")["path"] = "/other"
			case "host type":
				clusterSSHStorageMap(volume, "hostPath")["type"] = "Directory"
			case "config volume":
				volumes[0] = map[string]any{"name": "etc-ssl", "configMap": map[string]any{"name": "other"}}
			case "memory tmp":
				volumes[3].(map[string]any)["emptyDir"] = map[string]any{"medium": "Memory"}
			}
			if valid {
				sshSystemUpgradeRuntimeFixture(f)
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if valid {
				if err != nil || !value.Requirements.SystemUpgradeImages.valid() || value.Requirements.SystemUpgradeImages.Controller != container["image"] || value.Requirements.SystemUpgradeImages.Kubectl != data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] {
					t.Fatalf("reviewed configuration rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("unproved configuration escaped: %v", err)
			}
		})
	}
}

func TestClusterSSHSystemUpgradeDeniedReads(t *testing.T) {
	for _, denied := range []string{clusterSSHSystemUpgradePath, clusterSSHSystemUpgradeConfigPath} {
		t.Run(denied, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			get := func(ctx context.Context, path string, out any) error {
				if path == denied {
					return errors.New("private authorization failure")
				}
				return f.get(ctx, path, out)
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, get)
			if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("denied read escaped: %v", err)
			}
			consumed := false
			err = withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				return nil
			})
			if err != clusterbootstrap.ErrSessionAuthority || consumed {
				t.Fatalf("denied source reached consumer: %v", err)
			}
		})
	}
}
