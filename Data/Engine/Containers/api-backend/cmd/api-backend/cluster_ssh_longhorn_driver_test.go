package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"strings"
	"testing"
)

func sshLonghornDriverPin(role string) clusterbootstrap.ExternalImagePin {
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, "docker.io/longhornio/"+role+":") {
			return pin
		}
	}
	panic("missing Longhorn fixture pin")
}

// Mirrors the checksum-reviewed Longhorn driver Deployment, including its
// manager wait init container and all six indirect image environment values.
func sshLonghornDriverFixture() map[string]any {
	manager := sshLonghornDriverPin("longhorn-manager").Reference
	env := []any{}
	for _, field := range [][2]string{{"POD_NAMESPACE", "metadata.namespace"}, {"NODE_NAME", "spec.nodeName"}, {"SERVICE_ACCOUNT", "spec.serviceAccountName"}} {
		env = append(env, map[string]any{"name": field[0], "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": field[1]}}})
	}
	for _, image := range [][2]string{{"CSI_ATTACHER_IMAGE", "csi-attacher"}, {"CSI_PROVISIONER_IMAGE", "csi-provisioner"}, {"CSI_NODE_DRIVER_REGISTRAR_IMAGE", "csi-node-driver-registrar"}, {"CSI_RESIZER_IMAGE", "csi-resizer"}, {"CSI_SNAPSHOTTER_IMAGE", "csi-snapshotter"}, {"CSI_LIVENESS_PROBE_IMAGE", "livenessprobe"}} {
		env = append(env, map[string]any{"name": image[0], "value": sshLonghornDriverPin(image[1]).Reference})
	}
	object := sshStorageObject("apps/v1", "Deployment", "longhorn-system", "longhorn-driver-deployer")
	object["spec"] = map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "longhorn-driver-deployer", "image": manager, "env": env,
			"command": []any{"longhorn-manager", "-d", "deploy-driver", "--manager-image", manager, "--manager-url", "http://longhorn-backend:9500/v1"}}},
		"initContainers": []any{map[string]any{"name": "wait-longhorn-manager", "image": manager,
			"command": []any{"sh", "-c", `while [ $(curl -m 1 -s -o /dev/null -w "%{http_code}" http://longhorn-backend:9500/v1) != "200" ]; do echo waiting; sleep 2; done`}}},
	}}}
	return object
}

func sshLonghornDriverSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornDriverPath], "spec"), "template"), "spec")
}

func sshLonghornDriverContainer(f *sshStorageFixture) map[string]any {
	return sshLonghornDriverSpec(f)["containers"].([]any)[0].(map[string]any)
}

func TestClusterSSHLonghornDriverSourceConfiguration(t *testing.T) {
	for _, mode := range []string{"reviewed tags", "index digests", "platform digests", "split command args", "equals flags", "reordered flags", "API default fieldRef",
		"missing deployment", "wrong namespace", "wrong name", "missing UID", "deleting", "wrong container", "sidecar", "ephemeral", "missing init", "extra init", "restartable init", "init image", "init command", "init environment",
		"unreviewed manager", "malformed images", "different manager flag", "foreign manager URL", "duplicate flag", "missing flag", "unknown flag", "shell command", "missing command", "args type", "envFrom", "mount", "device",
		"missing env", "duplicate env", "unknown env", "missing CSI image", "CSI valueFrom", "mixed CSI value", "swapped CSI role", "unreviewed CSI", "expanded CSI", "wrong fieldRef", "literal downward field"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec := sshLonghornDriverSpec(f)
			main := sshLonghornDriverContainer(f)
			init := spec["initContainers"].([]any)[0].(map[string]any)
			cmd := main["command"].([]any)
			env := main["env"].([]any)
			firstImage := env[3].(map[string]any)
			meta := clusterSSHStorageMap(f.objects[clusterSSHLonghornDriverPath], "metadata")
			valid := false
			switch mode {
			case "reviewed tags":
				valid = true
			case "index digests", "platform digests":
				valid = true
				for _, role := range []string{"longhorn-manager", "csi-attacher", "csi-provisioner", "csi-node-driver-registrar", "csi-resizer", "csi-snapshotter", "livenessprobe"} {
					pin := sshLonghornDriverPin(role)
					digest := pin.IndexDigest
					if mode == "platform digests" {
						digest = pin.ManifestDigest
					}
					ref := "docker.io/longhornio/" + role + "@" + digest
					if role == "longhorn-manager" {
						main["image"], init["image"], cmd[4] = ref, ref, ref
						sshLonghornManagerSetImage(f, role, ref)
					}
					for _, e := range env {
						if e.(map[string]any)["value"] == pin.Reference {
							e.(map[string]any)["value"] = ref
						}
					}
				}
			case "split command args":
				valid = true
				main["command"], main["args"] = cmd[:3], cmd[3:]
			case "equals flags":
				valid = true
				main["command"] = append(cmd[:3:3], "--manager-image="+main["image"].(string), "--manager-url=http://longhorn-backend:9500/v1")
			case "reordered flags":
				valid = true
				cmd[3], cmd[4], cmd[5], cmd[6] = cmd[5], cmd[6], cmd[3], cmd[4]
			case "API default fieldRef":
				valid = true
				clusterSSHStorageMap(clusterSSHStorageMap(env[0].(map[string]any), "valueFrom"), "fieldRef")["apiVersion"] = "v1"
			case "missing deployment":
				delete(f.objects, clusterSSHLonghornDriverPath)
			case "wrong namespace":
				meta["namespace"] = "other"
			case "wrong name":
				meta["name"] = "other"
			case "missing UID":
				delete(meta, "uid")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "wrong container":
				main["name"] = "other"
			case "sidecar":
				spec["containers"] = []any{main, main}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{main}
			case "missing init":
				delete(spec, "initContainers")
			case "extra init":
				spec["initContainers"] = []any{init, init}
			case "restartable init":
				init["restartPolicy"] = "Always"
			case "init image":
				init["image"] = "other"
			case "init command":
				init["command"] = []any{"sh", "-c", "other"}
			case "init environment":
				init["env"] = []any{map[string]any{"name": "BASH_ENV", "value": "other"}}
			case "unreviewed manager":
				main["image"], init["image"], cmd[4] = "docker.io/longhornio/longhorn-manager:other", "docker.io/longhornio/longhorn-manager:other", "docker.io/longhornio/longhorn-manager:other"
			case "malformed images":
				main["image"], init["image"] = map[string]any{}, map[string]any{}
			case "different manager flag":
				cmd[4] = "other"
			case "foreign manager URL":
				cmd[6] = "http://other/v1"
			case "duplicate flag":
				cmd[5], cmd[6] = cmd[3], cmd[4]
			case "missing flag":
				main["command"] = cmd[:5]
			case "unknown flag":
				cmd[5] = "--other"
			case "shell command":
				main["command"] = []any{"sh", "-c", "longhorn-manager deploy-driver"}
			case "missing command":
				delete(main, "command")
				main["args"] = cmd
			case "args type":
				main["args"] = "other"
			case "envFrom":
				main["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
			case "mount":
				main["volumeMounts"] = []any{map[string]any{"name": "other", "mountPath": "/config"}}
			case "device":
				main["volumeDevices"] = []any{map[string]any{"name": "other", "devicePath": "/config"}}
			case "missing env":
				delete(main, "env")
			case "duplicate env":
				env[4] = env[3]
			case "unknown env":
				firstImage["name"] = "OTHER_IMAGE"
			case "missing CSI image":
				firstImage["value"] = ""
			case "CSI valueFrom":
				delete(firstImage, "value")
				firstImage["valueFrom"] = map[string]any{"configMapKeyRef": map[string]any{"name": "other", "key": "image"}}
			case "mixed CSI value":
				firstImage["valueFrom"] = map[string]any{}
			case "swapped CSI role":
				firstImage["value"] = sshLonghornDriverPin("csi-provisioner").Reference
			case "unreviewed CSI":
				firstImage["value"] = "docker.io/longhornio/csi-attacher:other"
			case "expanded CSI":
				firstImage["value"] = "$(IMAGE)"
			case "wrong fieldRef":
				clusterSSHStorageMap(clusterSSHStorageMap(env[0].(map[string]any), "valueFrom"), "fieldRef")["fieldPath"] = "metadata.name"
			case "literal downward field":
				env[0] = map[string]any{"name": "POD_NAMESPACE", "value": "longhorn-system"}
			}
			result, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if valid {
				if err != nil || !result.Requirements.LonghornDriverImages.valid() {
					t.Fatalf("reviewed configuration rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || result.Requirements.observation != "" {
				t.Fatalf("unproved configuration escaped: %v", err)
			}
		})
	}
}
