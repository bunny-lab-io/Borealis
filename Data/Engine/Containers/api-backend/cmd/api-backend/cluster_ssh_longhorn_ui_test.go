package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"testing"
)

// The reviewed v1.12.0 Deployment uses these literal inputs and three emptyDirs.
func sshLonghornUIFixture() map[string]any {
	object := sshStorageObject("apps/v1", "Deployment", "longhorn-system", "longhorn-ui")
	mounts, volumes := []any{}, []any{}
	for _, entry := range [][2]string{{"nginx-cache", "/var/cache/nginx/"}, {"nginx-config", "/var/config/nginx/"}, {"var-run", "/var/run/"}} {
		mounts = append(mounts, map[string]any{"name": entry[0], "mountPath": entry[1]})
		volumes = append(volumes, map[string]any{"name": entry[0], "emptyDir": map[string]any{}})
	}
	object["spec"] = map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "longhorn-ui", "image": sshLonghornDriverPin("longhorn-ui").Reference,
			"env":          []any{map[string]any{"name": "LONGHORN_MANAGER_IP", "value": "http://longhorn-backend:9500"}, map[string]any{"name": "LONGHORN_UI_PORT", "value": "8000"}},
			"volumeMounts": mounts}}, "volumes": volumes,
	}}}
	return object
}

func sshLonghornUISpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornUIPath], "spec"), "template"), "spec")
}

func sshLonghornUIContainer(f *sshStorageFixture) map[string]any {
	return sshLonghornUISpec(f)["containers"].([]any)[0].(map[string]any)
}

func TestClusterSSHLonghornUISourceConfiguration(t *testing.T) {
	for _, mode := range []string{"reviewed tag", "index digest", "platform digest", "reordered inputs", "default mount fields",
		"missing deployment", "wrong kind", "wrong namespace", "wrong name", "missing UID", "missing revision", "deleting", "wrong container", "sidecar", "init", "ephemeral",
		"unreviewed image", "wrong image role", "image type", "command", "args", "envFrom", "working directory", "lifecycle", "restart policy", "device",
		"missing env", "duplicate env", "unknown env", "wrong manager", "wrong port", "env type", "valueFrom", "mixed valueFrom",
		"missing mounts", "mount type", "missing mount", "duplicate mount", "executable mount", "readOnly", "propagation", "subPath", "subPathExpr", "mount indirection",
		"missing volumes", "volume type", "duplicate volume", "extra volume", "configMap", "mixed volume", "memory volume", "limited volume", "invalid emptyDir"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec, container := sshLonghornUISpec(f), sshLonghornUIContainer(f)
			meta := clusterSSHStorageMap(f.objects[clusterSSHLonghornUIPath], "metadata")
			env := container["env"].([]any)
			mounts, volumes := container["volumeMounts"].([]any), spec["volumes"].([]any)
			firstEnv, mount, volume := env[0].(map[string]any), mounts[0].(map[string]any), volumes[0].(map[string]any)
			valid := false
			switch mode {
			case "reviewed tag":
				valid = true
			case "index digest", "platform digest":
				valid = true
				pin := sshLonghornDriverPin("longhorn-ui")
				digest := pin.IndexDigest
				if mode == "platform digest" {
					digest = pin.ManifestDigest
				}
				container["image"] = "docker.io/longhornio/longhorn-ui@" + digest
			case "reordered inputs":
				valid = true
				env[0], env[1] = env[1], env[0]
				mounts[0], mounts[2] = mounts[2], mounts[0]
				volumes[0], volumes[1] = volumes[1], volumes[0]
			case "default mount fields":
				valid = true
				mount["readOnly"], mount["mountPropagation"] = false, "None"
			case "missing deployment":
				delete(f.objects, clusterSSHLonghornUIPath)
			case "wrong kind":
				f.objects[clusterSSHLonghornUIPath]["kind"] = "DaemonSet"
			case "wrong namespace":
				meta["namespace"] = "other"
			case "wrong name":
				meta["name"] = "other"
			case "missing UID":
				delete(meta, "uid")
			case "missing revision":
				delete(meta, "resourceVersion")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "wrong container":
				container["name"] = "other"
			case "sidecar":
				spec["containers"] = []any{container, container}
			case "init":
				spec["initContainers"] = []any{map[string]any{"name": "config-writer", "image": container["image"]}}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{map[string]any{"name": "other"}}
			case "unreviewed image":
				container["image"] = "docker.io/longhornio/longhorn-ui:other"
			case "wrong image role":
				container["image"] = sshLonghornDriverPin("longhorn-manager").Reference
			case "image type":
				container["image"] = map[string]any{}
			case "command", "args":
				container[mode] = []any{"other"}
			case "envFrom":
				container["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
			case "working directory":
				container["workingDir"] = "/other"
			case "lifecycle":
				container["lifecycle"] = map[string]any{"postStart": map[string]any{"exec": map[string]any{"command": []any{"other"}}}}
			case "restart policy":
				container["restartPolicy"] = "Always"
			case "device":
				container["volumeDevices"] = []any{map[string]any{"name": "other"}}
			case "missing env":
				delete(container, "env")
			case "duplicate env":
				env[1] = env[0]
			case "unknown env":
				firstEnv["name"] = "OTHER"
			case "wrong manager":
				firstEnv["value"] = "http://other:9500"
			case "wrong port":
				env[1].(map[string]any)["value"] = "8080"
			case "env type":
				firstEnv["value"] = 8000
			case "valueFrom", "mixed valueFrom":
				firstEnv["valueFrom"] = map[string]any{"configMapKeyRef": map[string]any{"name": "other", "key": "manager"}}
				if mode == "valueFrom" {
					delete(firstEnv, "value")
				}
			case "missing mounts":
				delete(container, "volumeMounts")
			case "mount type":
				mounts[0] = "other"
			case "missing mount":
				container["volumeMounts"] = mounts[:2]
			case "duplicate mount":
				mounts[1] = mount
			case "executable mount":
				mount["mountPath"] = "/docker-entrypoint.d/"
			case "readOnly":
				mount["readOnly"] = true
			case "propagation":
				mount["mountPropagation"] = "Bidirectional"
			case "subPath", "subPathExpr":
				mount[mode] = "other"
			case "mount indirection":
				mount["mountPath"] = "$(CONFIG_PATH)"
			case "missing volumes":
				delete(spec, "volumes")
			case "volume type":
				volumes[0] = "other"
			case "duplicate volume":
				volumes[1] = volume
			case "extra volume":
				spec["volumes"] = append(volumes, map[string]any{"name": "other", "emptyDir": map[string]any{}})
			case "configMap", "mixed volume":
				volume["configMap"] = map[string]any{"name": "other"}
				if mode == "configMap" {
					delete(volume, "emptyDir")
				}
			case "memory volume":
				volume["emptyDir"] = map[string]any{"medium": "Memory"}
			case "limited volume":
				volume["emptyDir"] = map[string]any{"sizeLimit": "1Gi"}
			case "invalid emptyDir":
				volume["emptyDir"] = nil
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if valid {
				if err != nil || value.Requirements.LonghornUIImage != container["image"] {
					t.Fatalf("reviewed UI rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" || value.Requirements.LonghornUIImage != "" {
				t.Fatalf("unproved UI escaped: %v", err)
			}
		})
	}
}

func TestClusterSSHLonghornUIDeniedRead(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	get := func(ctx context.Context, path string, out any) error {
		if path == clusterSSHLonghornUIPath {
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
}
