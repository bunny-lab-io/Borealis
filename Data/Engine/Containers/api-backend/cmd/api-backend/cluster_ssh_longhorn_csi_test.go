package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func sshLonghornCSIFixture(f *sshStorageFixture) {
	for _, role := range []clusterSSHLonghornCSIRole{clusterSSHLonghornProvisioner, clusterSSHLonghornResizer, clusterSSHLonghornSnapshotter} {
		sshLonghornCSIRoleFixture(f, role)
	}
}

// Constructor-shaped invocation inputs; host socket and resource proof stay separate.
func sshLonghornCSIDeploymentFixture(role clusterSSHLonghornCSIRole) map[string]any {
	object := sshStorageObject("apps/v1", "Deployment", "longhorn-system", string(role))
	object["spec"] = map[string]any{"replicas": 3, "template": map[string]any{"spec": map[string]any{
		"serviceAccountName": "longhorn-service-account",
		"containers": []any{map[string]any{"name": string(role), "image": sshLonghornDriverPin(string(role)).Reference,
			"args":         sshLonghornCSIStartupArgs(string(role)),
			"env":          sshLonghornCSIStartupEnv(),
			"volumeMounts": []any{map[string]any{"name": "socket-dir", "mountPath": "/csi/"}},
		}},
		"volumes": []any{map[string]any{"name": "socket-dir", "hostPath": map[string]any{"path": "/var/lib/kubelet/plugins/driver.longhorn.io", "type": "DirectoryOrCreate"}}},
	}}}
	return object
}
func sshLonghornCSIContainer(f *sshStorageFixture, role clusterSSHLonghornCSIRole) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[role.deploymentPath()], "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
}
func sshLonghornCSIRoleFixture(f *sshStorageFixture, role clusterSSHLonghornCSIRole) {
	f.objects[role.deploymentPath()] = sshLonghornCSIDeploymentFixture(role)
	for _, raw := range sshLonghornDriverContainer(f)["env"].([]any) {
		env := raw.(map[string]any)
		if env["name"] == strings.ToUpper(strings.ReplaceAll(string(role), "-", "_"))+"_IMAGE" {
			sshLonghornCSIContainer(f, role)["image"] = env["value"]
		}
	}
	sshLonghornCSIRuntimeFixture(f, role)
}

func sshLonghornCSIRuntimeFixture(f *sshStorageFixture, role clusterSSHLonghornCSIRole) {
	deployment := f.objects[role.deploymentPath()]
	rs := sshStorageObject("apps/v1", "ReplicaSet", "longhorn-system", string(role)+"-abcdef")
	clusterSSHStorageMap(rs, "metadata")["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": string(role), "uid": clusterSSHStorageMap(deployment, "metadata")["uid"], "controller": true}}
	// Separate template allocations let tests distinguish owner and Pod drift.
	rs["spec"] = sshLonghornCSIDeploymentFixture(role)["spec"]
	image := sshLonghornCSIContainer(f, role)["image"]
	clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["image"] = image
	f.objects[clusterSSHLonghornAttacherReplicaSetPrefix+string(role)+"-abcdef"] = rs
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "longhorn-system", string(role)+"-"+member.Name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = map[string]any{"app": string(role)}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": string(role) + "-abcdef", "uid": clusterSSHStorageMap(rs, "metadata")["uid"], "controller": true}}
		pod["spec"] = map[string]any{"nodeName": member.Name, "serviceAccountName": "longhorn-service-account", "containers": []any{map[string]any{"name": string(role), "image": image, "args": sshLonghornCSIStartupArgs(string(role)), "env": sshLonghornCSIStartupEnv()}}}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": string(role), "image": image, "imageID": role.repository() + "@" + sshLonghornDriverPin(string(role)).ManifestDigest, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}}}}
		f.objects[role.podsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshLonghornCSIPod(f *sshStorageFixture, role clusterSSHLonghornCSIRole, index int) map[string]any {
	return f.objects[role.podsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func sshLonghornCSIRuntime(f *sshStorageFixture, role clusterSSHLonghornCSIRole, index int) map[string]any {
	return clusterSSHStorageMap(sshLonghornCSIPod(f, role, index), "status")["containerStatuses"].([]any)[0].(map[string]any)
}

func TestClusterSSHLonghornCSISourceImages(t *testing.T) {
	for _, role := range []clusterSSHLonghornCSIRole{clusterSSHLonghornProvisioner, clusterSSHLonghornResizer, clusterSSHLonghornSnapshotter} {
		for _, mode := range []string{"expansion", "replacement", "replacement empty node", "platform configuration", "runtime index", "missing deployment", "wrong deployment", "missing UID", "deleting", "driver disagreement", "wrong role", "extra container", "init", "ephemeral", "missing list", "empty list", "pagination", "duplicate Pod", "foreign node", "wrong service account", "not ready", "wrong runtime", "missing runtime", "foreign owner", "foreign parent", "owner template mismatch", "owner init", "denied deployment", "denied Pods", "denied owner", "lost authority", "runtime drift", "owner drift", "startup drift", "collection revision"} {
			t.Run(string(role)+"/"+mode, func(t *testing.T) {
				f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
				path := role.podsPath(f.a.Source.Members[0].Name)
				list := f.objects[path]
				deployment := f.objects[role.deploymentPath()]
				rsPath := clusterSSHLonghornAttacherReplicaSetPrefix + string(role) + "-abcdef"
				rs := f.objects[rsPath]
				pod := sshLonghornCSIPod(f, role, 0)
				runtime := sshLonghornCSIRuntime(f, role, 0)
				container := sshLonghornCSIContainer(f, role)
				switch mode {
				case "replacement empty node":
					f.objects[role.podsPath(f.a.Source.Members[1].Name)]["items"] = []any{}
				case "platform configuration":
					image := role.repository() + "@" + sshLonghornDriverPin(string(role)).ManifestDigest
					for _, raw := range sshLonghornDriverContainer(f)["env"].([]any) {
						env := raw.(map[string]any)
						if env["name"] == strings.ToUpper(strings.ReplaceAll(string(role), "-", "_"))+"_IMAGE" {
							env["value"] = image
						}
					}
					sshLonghornDriverRuntimeFixture(f)
					sshLonghornCSIRoleFixture(f, role)
				case "runtime index":
					runtime["imageID"] = role.repository() + "@" + sshLonghornDriverPin(string(role)).IndexDigest
				case "missing deployment":
					delete(f.objects, role.deploymentPath())
				case "wrong deployment":
					clusterSSHStorageMap(deployment, "metadata")["name"] = "other"
				case "missing UID":
					delete(clusterSSHStorageMap(deployment, "metadata"), "uid")
				case "deleting":
					clusterSSHStorageMap(deployment, "metadata")["deletionTimestamp"] = "2026-10-04T00:00:00Z"
				case "driver disagreement":
					container["image"] = role.repository() + "@" + sshLonghornDriverPin(string(role)).ManifestDigest
				case "wrong role":
					container["image"] = sshLonghornDriverPin("csi-attacher").Reference
				case "extra container", "init", "ephemeral":
					spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(deployment, "spec"), "template"), "spec")
					key := "containers"
					if mode == "init" {
						key = "initContainers"
					}
					if mode == "ephemeral" {
						key = "ephemeralContainers"
					}
					if mode == "extra container" {
						spec[key] = []any{container, container}
					} else {
						spec[key] = []any{container}
					}
				case "missing list":
					delete(f.objects, path)
				case "empty list":
					list["items"] = []any{}
				case "pagination":
					clusterSSHStorageMap(list, "metadata")["continue"] = "more"
				case "duplicate Pod":
					list["items"] = []any{pod, pod}
				case "foreign node":
					clusterSSHStorageMap(pod, "spec")["nodeName"] = "other"
				case "wrong service account":
					clusterSSHStorageMap(pod, "spec")["serviceAccountName"] = "other"
				case "not ready":
					runtime["ready"] = false
				case "wrong runtime":
					runtime["imageID"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
				case "missing runtime":
					delete(runtime, "imageID")
				case "foreign owner":
					clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
				case "foreign parent":
					clusterSSHStorageMap(rs, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
				case "owner template mismatch":
					clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["image"] = role.repository() + "@" + sshLonghornDriverPin(string(role)).ManifestDigest
				case "owner init":
					clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["initContainers"] = []any{container}
				}
				get := func(ctx context.Context, p string, out any) error {
					if (mode == "denied deployment" && p == role.deploymentPath()) || (mode == "denied Pods" && p == path) || (mode == "denied owner" && p == rsPath) {
						return errors.New("private read denied")
					}
					err := f.get(ctx, p, out)
					if mode == "lost authority" && p == rsPath {
						f.mu.Lock()
						f.a.Source.Members[0].NodeUID = newClusterUUID()
						f.mu.Unlock()
					}
					return err
				}
				consumed := false
				err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(_ context.Context, r clusterSSHStorageRequirements, _ clusterSSHPreparationChecks) error {
					consumed = true
					if !r.LonghornCSIImages.valid(r.LonghornDriverImages) {
						t.Fatal("invalid projection")
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					switch mode {
					case "runtime drift":
						runtime["imageID"] = role.repository() + "@" + sshLonghornDriverPin(string(role)).IndexDigest
					case "owner drift":
						clusterSSHStorageMap(rs, "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "startup drift":
						container["args"] = []any{"--changed"}
					case "collection revision":
						clusterSSHStorageMap(list, "metadata")["resourceVersion"] = "999"
					}
					return nil
				})
				positive := textInSet(mode, "expansion", "replacement", "replacement empty node", "platform configuration", "runtime index", "collection revision")
				drift := textInSet(mode, "runtime drift", "owner drift", "startup drift")
				if positive {
					if err != nil || !consumed {
						t.Fatalf("valid source rejected: %v", err)
					}
				} else if err != clusterbootstrap.ErrSessionAuthority || consumed != drift {
					t.Fatalf("unsafe source escaped: %v consumed=%v", err, consumed)
				}
			})
		}
	}
}
func TestClusterSSHLonghornCSIPathsAndProjection(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	for _, role := range []clusterSSHLonghornCSIRole{clusterSSHLonghornProvisioner, clusterSSHLonghornResizer, clusterSSHLonghornSnapshotter} {
		for _, path := range []string{role.deploymentPath(), role.podsPath("node"), clusterSSHLonghornAttacherReplicaSetPrefix + string(role) + "-abc"} {
			if !clusterSSHLonghornCSIPathValid(path) {
				t.Fatalf("valid path rejected: %s", path)
			}
			for _, suffix := range []string{"/status", "?watch=true", "&watch=true"} {
				if clusterSSHLonghornCSIPathValid(path + suffix) {
					t.Fatal("broadened path accepted")
				}
			}
		}
		for _, path := range []string{role.podsPath("../other"), role.podsPath("node%2Fother"), strings.Replace(role.podsPath("node"), "limit=16", "limit=17", 1), strings.Replace(role.podsPath("node"), "app%3D", "other%3D", 1), clusterSSHLonghornAttacherReplicaSetPrefix + string(role) + "-../other"} {
			if clusterSSHLonghornCSIPathValid(path) {
				t.Fatal("unsafe selector accepted")
			}
		}
		for _, mode := range []string{"missing configured", "missing runtime", "wrong role", "driver disagreement"} {
			v := sshStorageSnapshotFixture(t, f.a)
			image := map[clusterSSHLonghornCSIRole]*clusterSSHSourceExternalImage{clusterSSHLonghornProvisioner: &v.Requirements.LonghornCSIImages.Provisioner, clusterSSHLonghornResizer: &v.Requirements.LonghornCSIImages.Resizer, clusterSSHLonghornSnapshotter: &v.Requirements.LonghornCSIImages.Snapshotter}[role]
			switch mode {
			case "missing configured":
				image.Configured = ""
			case "missing runtime":
				image.Resolved = ""
			case "wrong role":
				*image = v.Requirements.LonghornAttacherImage
			case "driver disagreement":
				image.Configured = image.Resolved
			}
			if validClusterSSHStorageSnapshot(v, f.a.Source) {
				t.Fatalf("%s %s projection accepted", role, mode)
			}
		}
	}
	unknown := clusterSSHLonghornCSIRole("other")
	if clusterSSHLonghornCSIPathValid(unknown.deploymentPath()) || unknown.podsPathValid(unknown.podsPath("node")) || unknown.replicaSetPathValid(clusterSSHLonghornAttacherReplicaSetPrefix+"other-abc") {
		t.Fatal("unknown role accepted")
	}
	if _, err := unknown.templateImage(sshLonghornCSIDeploymentFixture(clusterSSHLonghornProvisioner)); err == nil {
		t.Fatal("unknown role parsed")
	}
}
