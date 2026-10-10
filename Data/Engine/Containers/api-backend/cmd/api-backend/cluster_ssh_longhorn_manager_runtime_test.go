package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func sshLonghornManagerRuntimeFixture(f *sshStorageFixture) {
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "longhorn-system", "longhorn-manager-"+member.Name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = map[string]any{"app": "longhorn-manager"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "longhorn-manager", "uid": clusterSSHStorageMap(f.objects[clusterSSHLonghornManagerPath], "metadata")["uid"], "controller": true}}
		containers, statuses := []any{}, []any{}
		for _, name := range []string{"longhorn-manager", "pre-pull-share-manager-image"} {
			role := name
			if name == "pre-pull-share-manager-image" {
				role = "longhorn-share-manager"
			}
			image := ""
			for _, raw := range sshLonghornManagerSpec(f)["containers"].([]any) {
				c := raw.(map[string]any)
				if c["name"] == name {
					image = c["image"].(string)
				}
			}
			containers = append(containers, map[string]any{"name": name, "image": image})
			statuses = append(statuses, map[string]any{"name": name, "image": image, "imageID": "docker.io/longhornio/" + role + "@" + sshLonghornDriverPin(role).ManifestDigest, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}})
		}
		pod["spec"] = map[string]any{"nodeName": member.Name, "serviceAccountName": "longhorn-service-account", "containers": containers}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": statuses}
		f.objects[clusterSSHLonghornManagerPodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshLonghornManagerPod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHLonghornManagerPodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func sshLonghornManagerRuntime(f *sshStorageFixture, index int) map[string]any {
	return clusterSSHStorageMap(sshLonghornManagerPod(f, index), "status")["containerStatuses"].([]any)[0].(map[string]any)
}
func sshLonghornManagerShareRuntime(f *sshStorageFixture, index int) map[string]any {
	return clusterSSHStorageMap(sshLonghornManagerPod(f, index), "status")["containerStatuses"].([]any)[1].(map[string]any)
}

func TestClusterSSHLonghornManagerRunningImages(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "runtime index", "reordered containers", "reordered statuses", "platform configuration", "missing list", "empty list", "duplicate Pod", "list continuation", "list remainder", "wrong list kind", "wrong namespace", "missing UID", "missing revision", "deleting", "wrong label", "foreign node", "wrong host network", "wrong service account", "missing owner", "duplicate owner", "foreign owner UID", "foreign owner kind", "foreign owner name", "foreign owner API", "noncontroller owner", "spec image mismatch", "spec container name", "sidecar", "init", "ephemeral", "init status", "ephemeral status", "not running", "missing Ready", "duplicate Ready", "not Ready", "missing runtime", "extra runtime", "wrong runtime name", "runtime image mismatch", "not ready", "not started", "waiting", "ambiguous state", "missing start", "zero start", "missing imageID", "tag imageID", "foreign imageID", "wrong pin", "platform to index", "replacement mixed images", "replacement cloned UID", "replacement missing Pod"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
			path := clusterSSHLonghornManagerPodsPath(f.a.Source.Members[0].Name)
			list := f.objects[path]
			pod := sshLonghornManagerPod(f, 0)
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			owner := meta["ownerReferences"].([]any)[0].(map[string]any)
			container := spec["containers"].([]any)[0].(map[string]any)
			runtime := sshLonghornManagerRuntime(f, 0)
			switch mode {
			case "reordered containers":
				slices.Reverse(spec["containers"].([]any))
			case "reordered statuses":
				slices.Reverse(status["containerStatuses"].([]any))
			case "runtime index":
				runtime["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
			case "platform configuration", "platform to index":
				image := "docker.io/longhornio/longhorn-manager" + "@" + sshLonghornDriverPin("longhorn-manager").ManifestDigest
				sshLonghornManagerSetImage(f, "longhorn-manager", image)
				driver := sshLonghornDriverContainer(f)
				driver["image"], driver["command"].([]any)[4] = image, image
				sshLonghornDriverSpec(f)["initContainers"].([]any)[0].(map[string]any)["image"] = image
				sshLonghornDriverRuntimeFixture(f)
				container["image"], runtime["image"] = image, image
				sshLonghornPluginFixture(f)
				if mode == "platform to index" {
					runtime["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
				}
			case "missing list":
				delete(f.objects, path)
			case "empty list":
				list["items"] = []any{}
			case "duplicate Pod":
				list["items"] = []any{pod, pod}
			case "list continuation":
				clusterSSHStorageMap(list, "metadata")["continue"] = "more"
			case "list remainder":
				clusterSSHStorageMap(list, "metadata")["remainingItemCount"] = 1
			case "wrong list kind":
				list["kind"] = "List"
			case "wrong namespace":
				meta["namespace"] = "borealis"
			case "missing UID":
				delete(meta, "uid")
			case "missing revision":
				delete(meta, "resourceVersion")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-02T00:00:00Z"
			case "wrong label":
				meta["labels"] = map[string]any{"app": "other"}
			case "foreign node":
				spec["nodeName"] = "foreign"
			case "wrong host network":
				spec["hostNetwork"] = true
			case "wrong service account":
				spec["serviceAccountName"] = "other"
			case "missing owner":
				delete(meta, "ownerReferences")
			case "duplicate owner":
				meta["ownerReferences"] = []any{owner, owner}
			case "foreign owner UID":
				owner["uid"] = newClusterUUID()
			case "foreign owner kind":
				owner["kind"] = "ReplicaSet"
			case "foreign owner name":
				owner["name"] = "other"
			case "foreign owner API":
				owner["apiVersion"] = "v1"
			case "noncontroller owner":
				owner["controller"] = false
			case "spec image mismatch":
				container["image"] = "docker.io/longhornio/longhorn-manager" + "@" + sshLonghornDriverPin("longhorn-manager").ManifestDigest
			case "spec container name":
				container["name"] = "other"
			case "sidecar":
				spec["containers"] = append(spec["containers"].([]any), container)
			case "init":
				spec["initContainers"] = []any{container}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{container}
			case "init status":
				status["initContainerStatuses"] = []any{runtime}
			case "ephemeral status":
				status["ephemeralContainerStatuses"] = []any{runtime}
			case "not running":
				status["phase"] = "Pending"
			case "missing Ready":
				status["conditions"] = []any{}
			case "duplicate Ready":
				status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "Ready", "status": "True"})
			case "not Ready":
				status["conditions"] = []any{map[string]any{"type": "Ready", "status": "Unknown"}}
			case "missing runtime":
				delete(status, "containerStatuses")
			case "extra runtime":
				status["containerStatuses"] = append(status["containerStatuses"].([]any), runtime)
			case "wrong runtime name":
				runtime["name"] = "other"
			case "runtime image mismatch":
				runtime["image"] = "docker.io/longhornio/longhorn-manager" + "@" + sshLonghornDriverPin("longhorn-manager").ManifestDigest
			case "not ready":
				runtime["ready"] = false
			case "not started":
				runtime["started"] = false
			case "waiting":
				runtime["state"] = map[string]any{"waiting": map[string]any{"reason": "ContainerCreating"}}
			case "ambiguous state":
				clusterSSHStorageMap(runtime, "state")["terminated"] = map[string]any{"exitCode": 0}
			case "missing start":
				runtime["state"] = map[string]any{"running": map[string]any{}}
			case "zero start":
				runtime["state"] = map[string]any{"running": map[string]any{"startedAt": "0001-01-01T00:00:00Z"}}
			case "missing imageID":
				delete(runtime, "imageID")
			case "tag imageID":
				runtime["imageID"] = "docker.io/longhornio/longhorn-manager" + ":latest"
			case "foreign imageID":
				runtime["imageID"] = sshSnapshotControllerPin().Reference
			case "wrong pin":
				runtime["imageID"] = "docker.io/longhornio/longhorn-manager" + "@sha256:" + strings.Repeat("a", 64)
			case "replacement mixed images":
				sshLonghornManagerRuntime(f, 1)["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
			case "replacement cloned UID":
				clusterSSHStorageMap(sshLonghornManagerPod(f, 1), "metadata")["uid"] = meta["uid"]
			case "replacement missing Pod":
				delete(f.objects, clusterSSHLonghornManagerPodsPath(f.a.Source.Members[1].Name))
			}
			// Unavailable failed/inactive member is outside this source evidence set.
			inactive := clusterSSHLonghornManagerPodsPath("failed-member")
			f.objects[inactive] = map[string]any{"unavailable": true}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			good := textInSet(mode, "expansion", "replacement", "runtime index", "platform configuration", "reordered containers", "reordered statuses")
			if good {
				if err != nil || value.Requirements.LonghornManagerImages.ManagerResolved != runtime["imageID"] || !value.Requirements.LonghornManagerImages.valid() {
					t.Fatalf("valid running source rejected: %v", err)
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHLonghornManagerPodsPath(member.Name)] != 1 {
						t.Fatal("active source not observed exactly once")
					}
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("unproved running image accepted: %v", err)
			}
			if f.calls[inactive] != 0 {
				t.Fatal("failed member became prerequisite for replacement")
			}
		})
	}
}

func TestClusterSSHLonghornManagerRuntimeDeniedOrChanged(t *testing.T) {
	for _, mode := range []string{"denied", "lost authority", "receipt drift", "image drift", "Pod replacement", "collection revision"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			path := clusterSSHLonghornManagerPodsPath(f.a.Source.Members[0].Name)
			consumed := false
			get := func(ctx context.Context, p string, out any) error {
				if p == path && mode == "denied" {
					return errors.New("private denial")
				}
				err := f.get(ctx, p, out)
				if p == path && mode == "lost authority" {
					f.mu.Lock()
					f.a.Source.Members[0].NodeUID = newClusterUUID()
					f.mu.Unlock()
				}
				return err
			}
			err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				defer f.mu.Unlock()
				switch mode {
				case "receipt drift":
					clusterSSHStorageMap(sshLonghornManagerPod(f, 0), "metadata")["annotations"] = map[string]any{"changed": "frozen Pod receipt"}
				case "image drift":
					sshLonghornManagerRuntime(f, 0)["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
				case "Pod replacement":
					clusterSSHStorageMap(sshLonghornManagerPod(f, 0), "metadata")["uid"] = newClusterUUID()
				case "collection revision":
					clusterSSHStorageMap(f.objects[path], "metadata")["resourceVersion"] = "999"
				}
				return nil
			})
			if mode == "collection revision" {
				if err != nil || !consumed {
					t.Fatalf("collection revision became Pod drift: %v", err)
				}
			} else if err == nil {
				t.Fatal("unproved source escaped")
			}
			if textInSet(mode, "denied", "lost authority") && consumed {
				t.Fatal("invalid source reached consumer")
			}
		})
	}
}

func TestClusterSSHLonghornManagerShareRuntime(t *testing.T) {
	for _, mode := range []string{"valid index", "wrong role", "missing identity", "tag identity", "missing status", "duplicate status", "duplicate spec", "wrong configured image", "wrong reported image", "not ready", "not started", "terminated", "missing start", "mixed member share"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			pod := sshLonghornManagerPod(f, 0)
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			share := sshLonghornManagerShareRuntime(f, 0)
			switch mode {
			case "valid index":
				for i := range f.a.Source.Members {
					sshLonghornManagerShareRuntime(f, i)["imageID"] = "docker.io/longhornio/longhorn-share-manager@" + sshLonghornDriverPin("longhorn-share-manager").IndexDigest
				}
			case "wrong role":
				share["imageID"] = sshLonghornManagerRuntime(f, 0)["imageID"]
			case "missing identity":
				delete(share, "imageID")
			case "tag identity":
				share["imageID"] = sshLonghornDriverPin("longhorn-share-manager").Reference
			case "missing status":
				status["containerStatuses"] = []any{sshLonghornManagerRuntime(f, 0)}
			case "duplicate status":
				status["containerStatuses"] = []any{share, share}
			case "duplicate spec":
				spec["containers"] = []any{spec["containers"].([]any)[1], spec["containers"].([]any)[1]}
			case "wrong configured image":
				spec["containers"].([]any)[1].(map[string]any)["image"] = sshLonghornDriverPin("longhorn-manager").Reference
			case "wrong reported image":
				share["image"] = sshLonghornDriverPin("longhorn-manager").Reference
			case "not ready":
				share["ready"] = false
			case "not started":
				share["started"] = false
			case "terminated":
				share["state"] = map[string]any{"terminated": map[string]any{"exitCode": 0}}
			case "missing start":
				share["state"] = map[string]any{"running": map[string]any{}}
			case "mixed member share":
				sshLonghornManagerShareRuntime(f, 1)["imageID"] = "docker.io/longhornio/longhorn-share-manager@" + sshLonghornDriverPin("longhorn-share-manager").IndexDigest
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if mode == "valid index" {
				if err != nil || value.Requirements.LonghornManagerImages.ShareResolved != share["imageID"] {
					t.Fatalf("reviewed share rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("unproved share escaped: %v", err)
			}
		})
	}
}
