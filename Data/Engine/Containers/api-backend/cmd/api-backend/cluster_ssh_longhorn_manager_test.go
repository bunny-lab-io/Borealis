package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"slices"
	"testing"
)

func TestClusterSSHLonghornManagerRequiredReadDenied(t *testing.T) {
	for _, denied := range []string{clusterSSHLonghornManagerPath, clusterSSHStorageSettingPrefix + "default-engine-image", clusterSSHStorageSettingPrefix + "support-bundle-manager-image"} {
		t.Run(denied, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			get := func(ctx context.Context, path string, out any) error {
				if path == denied {
					return errors.New("private forbidden Kubernetes diagnostic")
				}
				return f.get(ctx, path, out)
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, get)
			if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatal("denied read exported evidence or private diagnostics")
			}
			err = withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				return nil
			})
			// Failed work cancels/joins the enclosing lease guard, whose public
			// terminal error is ErrSessionAuthority. No consumer may run.
			if err != clusterbootstrap.ErrSessionAuthority || consumed {
				t.Fatal("denied required image evidence reached consumer or leaked diagnostics")
			}
		})
	}
}

func sshLonghornManagerFixture(f *sshStorageFixture) {
	command := []any{"longhorn-manager", "-d", "daemon"}
	for _, item := range [][2]string{{"--engine-image", "longhorn-engine"}, {"--instance-manager-image", "longhorn-instance-manager"}, {"--share-manager-image", "longhorn-share-manager"}, {"--backing-image-manager-image", "backing-image-manager"}, {"--support-bundle-manager-image", "support-bundle-kit"}, {"--manager-image", "longhorn-manager"}} {
		command = append(command, item[0], sshLonghornDriverPin(item[1]).Reference)
	}
	command = append(command, "--service-account", "longhorn-service-account", "--upgrade-version-check")
	env := []any{}
	for _, field := range [][2]string{{"POD_NAME", "metadata.name"}, {"POD_NAMESPACE", "metadata.namespace"}, {"POD_IP", "status.podIP"}, {"NODE_NAME", "spec.nodeName"}} {
		env = append(env, map[string]any{"name": field[0], "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": field[1]}}})
	}
	env = append(env, map[string]any{"name": "LONGHORN_DISTRO", "value": "longhorn"})
	mounts := []any{}
	for _, mount := range [][2]string{{"boot", "/host/boot/"}, {"dev", "/host/dev/"}, {"proc", "/host/proc/"}, {"etc", "/host/etc/"}, {"longhorn", "/var/lib/longhorn/"}, {"longhorn-grpc-tls", "/tls-files/"}} {
		value := map[string]any{"name": mount[0], "mountPath": mount[1]}
		if mount[0] == "boot" || mount[0] == "proc" || mount[0] == "etc" {
			value["readOnly"] = true
		}
		if mount[0] == "longhorn" {
			value["mountPropagation"] = "Bidirectional"
		}
		mounts = append(mounts, value)
	}
	object := sshStorageObject("apps/v1", "DaemonSet", "longhorn-system", "longhorn-manager")
	object["spec"] = map[string]any{"template": map[string]any{"spec": map[string]any{"serviceAccountName": "longhorn-service-account", "containers": []any{
		map[string]any{"name": "longhorn-manager", "image": sshLonghornDriverPin("longhorn-manager").Reference, "command": command, "env": env, "volumeMounts": mounts},
		map[string]any{"name": "pre-pull-share-manager-image", "image": sshLonghornDriverPin("longhorn-share-manager").Reference, "command": []any{"sh", "-c", "echo share-manager image pulled && sleep infinity"}},
	}}}}
	f.objects[clusterSSHLonghornManagerPath] = object
	for _, item := range [][2]string{{"default-engine-image", "longhorn-engine"}, {"support-bundle-manager-image", "support-bundle-kit"}} {
		setting := sshStorageObject("longhorn.io/v1beta2", "Setting", "longhorn-system", item[0])
		setting["value"] = sshLonghornDriverPin(item[1]).Reference
		f.objects[clusterSSHStorageSettingPrefix+item[0]] = setting
	}
}

func sshLonghornManagerSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornManagerPath], "spec"), "template"), "spec")
}

func sshLonghornManagerContainer(f *sshStorageFixture) map[string]any {
	return sshLonghornManagerSpec(f)["containers"].([]any)[0].(map[string]any)
}

func sshLonghornManagerSetImage(f *sshStorageFixture, role, ref string) {
	old := sshLonghornDriverPin(role).Reference
	main := sshLonghornManagerContainer(f)
	command := main["command"].([]any)
	for i, value := range command {
		if value == old {
			command[i] = ref
		}
	}
	if role == "longhorn-manager" {
		main["image"] = ref
	}
	if role == "longhorn-share-manager" {
		sshLonghornManagerSpec(f)["containers"].([]any)[1].(map[string]any)["image"] = ref
	}
	for _, name := range clusterSSHLonghornImageSettings {
		setting := f.objects[clusterSSHStorageSettingPrefix+name]
		if setting["value"] == old {
			setting["value"] = ref
		}
	}
}

func TestClusterSSHLonghornManagerSourceConfiguration(t *testing.T) {
	for _, mode := range []string{"reviewed tags", "index digests", "platform digests", "split args", "equals flags", "reordered containers", "API defaults", "deprecated Settings absent",
		"missing daemonset", "wrong identity", "missing UID", "deleting", "extra container", "init container", "ephemeral", "wrong service account", "manager image", "driver disagreement", "share image", "share command", "share env", "share mount",
		"shell command", "missing command", "wrong command type", "args type", "malformed flag", "missing image flag", "duplicate image flag", "unknown flag", "indirect image", "unreviewed image", "swapped role", "missing upgrade flag", "duplicate upgrade flag", "duplicate service flag",
		"envFrom", "unknown env", "duplicate env", "wrong fieldRef", "literal field", "wrong distro", "mounted command", "mount subpath", "duplicate mount", "wrong propagation", "devices",
		"missing engine setting", "missing support setting", "setting disagreement", "setting wrong name", "setting deletion", "setting wrong type", "setting malformed value", "setting no revision"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec := sshLonghornManagerSpec(f)
			main := sshLonghornManagerContainer(f)
			share := spec["containers"].([]any)[1].(map[string]any)
			cmd := main["command"].([]any)
			env := main["env"].([]any)
			mounts := main["volumeMounts"].([]any)
			meta := clusterSSHStorageMap(f.objects[clusterSSHLonghornManagerPath], "metadata")
			setting := f.objects[clusterSSHStorageSettingPrefix+"support-bundle-manager-image"]
			valid := false
			switch mode {
			case "reviewed tags", "deprecated Settings absent":
				valid = true
			case "index digests", "platform digests":
				valid = true
				for _, role := range []string{"longhorn-manager", "longhorn-engine", "longhorn-instance-manager", "longhorn-share-manager", "backing-image-manager", "support-bundle-kit"} {
					pin := sshLonghornDriverPin(role)
					digest := pin.IndexDigest
					if mode == "platform digests" {
						digest = pin.ManifestDigest
					}
					ref := "docker.io/longhornio/" + role + "@" + digest
					sshLonghornManagerSetImage(f, role, ref)
					if role == "longhorn-manager" {
						driver := sshLonghornDriverContainer(f)
						driver["image"], driver["command"].([]any)[4] = ref, ref
						sshLonghornDriverSpec(f)["initContainers"].([]any)[0].(map[string]any)["image"] = ref
					}
				}
			case "split args":
				valid = true
				main["command"], main["args"] = cmd[:3], cmd[3:]
			case "equals flags":
				valid = true
				joined := slices.Clone(cmd[:3])
				for i := 3; i < 17; i += 2 {
					joined = append(joined, cmd[i].(string)+"="+cmd[i+1].(string))
				}
				main["command"] = append(joined, cmd[17])
			case "reordered containers":
				valid = true
				slices.Reverse(spec["containers"].([]any))
			case "API defaults":
				valid = true
				clusterSSHStorageMap(clusterSSHStorageMap(env[0].(map[string]any), "valueFrom"), "fieldRef")["apiVersion"] = "v1"
				mounts[0].(map[string]any)["mountPropagation"] = "None"
			case "missing daemonset":
				delete(f.objects, clusterSSHLonghornManagerPath)
			case "wrong identity":
				meta["namespace"] = "other"
			case "missing UID":
				delete(meta, "uid")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "extra container":
				spec["containers"] = append(spec["containers"].([]any), main)
			case "init container":
				spec["initContainers"] = []any{main}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{main}
			case "wrong service account":
				spec["serviceAccountName"] = "other"
			case "manager image":
				main["image"] = "other"
			case "driver disagreement":
				sshLonghornManagerSetImage(f, "longhorn-manager", "docker.io/longhornio/longhorn-manager@"+sshLonghornDriverPin("longhorn-manager").ManifestDigest)
			case "share image":
				share["image"] = "other"
			case "share command":
				share["command"] = []any{"other"}
			case "share env":
				share["env"] = env
			case "share mount":
				share["volumeMounts"] = mounts
			case "shell command":
				cmd[0] = "sh"
			case "missing command":
				delete(main, "command")
			case "wrong command type":
				main["command"] = "other"
			case "args type":
				main["args"] = "other"
			case "malformed flag":
				cmd[3] = map[string]any{}
			case "missing image flag":
				main["command"] = append(slices.Clone(cmd[:3]), cmd[5:]...)
			case "duplicate image flag":
				cmd[5] = cmd[3]
			case "unknown flag":
				cmd[3] = "--config"
			case "indirect image":
				cmd[4] = "$(ENGINE_IMAGE)"
			case "unreviewed image":
				cmd[4] = "docker.io/longhornio/longhorn-engine:other"
			case "swapped role":
				cmd[4] = sshLonghornDriverPin("longhorn-manager").Reference
			case "missing upgrade flag":
				main["command"] = cmd[:17]
			case "duplicate upgrade flag":
				cmd[15] = cmd[17]
			case "duplicate service flag":
				cmd[3] = "--service-account"
			case "envFrom":
				main["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
			case "unknown env":
				env[0].(map[string]any)["name"] = "ENGINE_IMAGE"
			case "duplicate env":
				env[1] = env[0]
			case "wrong fieldRef":
				clusterSSHStorageMap(clusterSSHStorageMap(env[0].(map[string]any), "valueFrom"), "fieldRef")["fieldPath"] = "metadata.namespace"
			case "literal field":
				env[0] = map[string]any{"name": "POD_NAME", "value": "other"}
			case "wrong distro":
				env[4].(map[string]any)["value"] = "other"
			case "mounted command":
				mounts[0].(map[string]any)["mountPath"] = "/usr/local/bin/longhorn-manager"
			case "mount subpath":
				mounts[0].(map[string]any)["subPathExpr"] = "$(OTHER)"
			case "duplicate mount":
				mounts[1] = mounts[0]
			case "wrong propagation":
				mounts[4].(map[string]any)["mountPropagation"] = "None"
			case "devices":
				main["volumeDevices"] = []any{map[string]any{"name": "other", "devicePath": "/config"}}
			case "missing engine setting":
				delete(f.objects, clusterSSHStorageSettingPrefix+"default-engine-image")
			case "missing support setting":
				delete(f.objects, clusterSSHStorageSettingPrefix+"support-bundle-manager-image")
			case "setting disagreement":
				setting["value"] = "docker.io/longhornio/support-bundle-kit@" + sshLonghornDriverPin("support-bundle-kit").ManifestDigest
			case "setting wrong name":
				clusterSSHStorageMap(setting, "metadata")["name"] = "other"
			case "setting deletion":
				clusterSSHStorageMap(setting, "metadata")["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "setting wrong type":
				setting["kind"] = "ConfigMap"
			case "setting malformed value":
				setting["value"] = map[string]any{}
			case "setting no revision":
				delete(clusterSSHStorageMap(setting, "metadata"), "resourceVersion")
			}
			if valid {
				sshLonghornManagerRuntimeFixture(f)
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if valid {
				if err != nil || !value.Requirements.LonghornManagerImages.valid() {
					t.Fatalf("reviewed manager rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("unproved manager escaped: %v", err)
			}
			if f.calls[clusterSSHStorageSettingPrefix+"default-instance-manager-image"] != 0 || f.calls[clusterSSHStorageSettingPrefix+"default-backing-image-manager-image"] != 0 {
				t.Fatal("deprecated Settings became prerequisites")
			}
		})
	}
}
