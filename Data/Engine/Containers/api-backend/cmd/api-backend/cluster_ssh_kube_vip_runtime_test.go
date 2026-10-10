package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func sshKubeVIPRuntimeFixture(f *sshStorageFixture) {
	image := sshKubeVIPContainer(f)["image"]
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "kube-system", "kube-vip-"+member.Name)
		metadata := clusterSSHStorageMap(pod, "metadata")
		metadata["labels"] = map[string]any{"app.kubernetes.io/name": "kube-vip-borealis-cluster"}
		metadata["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "kube-vip-borealis-cluster", "uid": clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata")["uid"], "controller": true}}
		pod["spec"] = map[string]any{"nodeName": member.Name, "hostNetwork": true, "serviceAccountName": "kube-vip-borealis", "containers": []any{map[string]any{"name": "kube-vip", "image": image}}}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "kube-vip", "image": image, "imageID": clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}}}}
		f.objects[clusterSSHKubeVIPPodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshKubeVIPPod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHKubeVIPPodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func sshKubeVIPRuntime(f *sshStorageFixture, index int) map[string]any {
	return clusterSSHStorageMap(sshKubeVIPPod(f, index), "status")["containerStatuses"].([]any)[0].(map[string]any)
}

func TestClusterSSHKubeVIPRunningImages(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "runtime index", "platform configuration", "missing list", "empty list", "duplicate Pod", "list continuation", "list remainder", "wrong list kind", "wrong namespace", "missing UID", "missing revision", "deleting", "wrong label", "foreign node", "wrong host network", "wrong service account", "missing owner", "duplicate owner", "foreign owner UID", "foreign owner kind", "foreign owner name", "foreign owner API", "noncontroller owner", "spec image mismatch", "spec container name", "sidecar", "init", "ephemeral", "init status", "ephemeral status", "not running", "missing Ready", "duplicate Ready", "not Ready", "missing runtime", "extra runtime", "wrong runtime name", "runtime image mismatch", "not ready", "not started", "waiting", "ambiguous state", "missing start", "zero start", "missing imageID", "tag imageID", "foreign imageID", "wrong pin", "platform to index", "replacement mixed images", "replacement cloned UID", "replacement missing Pod"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
			path := clusterSSHKubeVIPPodsPath(f.a.Source.Members[0].Name)
			list := f.objects[path]
			pod := sshKubeVIPPod(f, 0)
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			owner := meta["ownerReferences"].([]any)[0].(map[string]any)
			container := spec["containers"].([]any)[0].(map[string]any)
			runtime := sshKubeVIPRuntime(f, 0)
			switch mode {
			case "runtime index":
				runtime["imageID"] = sshKubeVIPPin().Reference
			case "platform configuration", "platform to index":
				image := clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest
				sshKubeVIPContainer(f)["image"], container["image"], runtime["image"] = image, image, image
				if mode == "platform to index" {
					runtime["imageID"] = sshKubeVIPPin().Reference
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
				meta["labels"] = map[string]any{"app.kubernetes.io/name": "other"}
			case "foreign node":
				spec["nodeName"] = "foreign"
			case "wrong host network":
				spec["hostNetwork"] = false
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
				container["image"] = clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest
			case "spec container name":
				container["name"] = "other"
			case "sidecar":
				spec["containers"] = []any{container, container}
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
				status["containerStatuses"] = []any{runtime, runtime}
			case "wrong runtime name":
				runtime["name"] = "other"
			case "runtime image mismatch":
				runtime["image"] = clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest
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
				runtime["imageID"] = clusterSSHKubeVIPRepository + ":latest"
			case "foreign imageID":
				runtime["imageID"] = sshSnapshotControllerPin().Reference
			case "wrong pin":
				runtime["imageID"] = clusterSSHKubeVIPRepository + "@sha256:" + strings.Repeat("a", 64)
			case "replacement mixed images":
				sshKubeVIPRuntime(f, 1)["imageID"] = sshKubeVIPPin().Reference
			case "replacement cloned UID":
				clusterSSHStorageMap(sshKubeVIPPod(f, 1), "metadata")["uid"] = meta["uid"]
			case "replacement missing Pod":
				delete(f.objects, clusterSSHKubeVIPPodsPath(f.a.Source.Members[1].Name))
			}
			// Unavailable failed/inactive member is outside this source evidence set.
			inactive := clusterSSHKubeVIPPodsPath("failed-member")
			f.objects[inactive] = map[string]any{"unavailable": true}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			good := textInSet(mode, "expansion", "replacement", "runtime index", "platform configuration")
			if good {
				if err != nil || value.Requirements.KubeVIP.Resolved != runtime["imageID"] || !value.Requirements.KubeVIP.valid(f.a.Source) {
					t.Fatalf("valid running source rejected: %v", err)
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHKubeVIPPodsPath(member.Name)] != 1 {
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

func TestClusterSSHKubeVIPRuntimeDeniedOrChanged(t *testing.T) {
	for _, mode := range []string{"denied", "lost authority", "receipt drift", "image drift", "Pod replacement", "collection revision"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			path := clusterSSHKubeVIPPodsPath(f.a.Source.Members[0].Name)
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
					clusterSSHStorageMap(sshKubeVIPPod(f, 0), "metadata")["annotations"] = map[string]any{"changed": "frozen Pod receipt"}
				case "image drift":
					sshKubeVIPRuntime(f, 0)["imageID"] = sshKubeVIPPin().Reference
				case "Pod replacement":
					clusterSSHStorageMap(sshKubeVIPPod(f, 0), "metadata")["uid"] = newClusterUUID()
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
