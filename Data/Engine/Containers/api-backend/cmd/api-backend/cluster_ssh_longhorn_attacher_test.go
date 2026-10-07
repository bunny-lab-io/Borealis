package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func sshLonghornAttacherDeploymentFixture() map[string]any {
	object := sshStorageObject("apps/v1", "Deployment", "longhorn-system", "csi-attacher")
	object["spec"] = map[string]any{"replicas": 3, "template": map[string]any{"spec": map[string]any{
		"serviceAccountName": "longhorn-service-account",
		"containers": []any{map[string]any{"name": "csi-attacher", "image": sshLonghornDriverPin("csi-attacher").Reference,
			"args":         sshLonghornCSIStartupArgs("csi-attacher"),
			"env":          sshLonghornCSIStartupEnv(),
			"volumeMounts": []any{map[string]any{"name": "socket-dir", "mountPath": "/csi/"}},
		}},
		"volumes": []any{map[string]any{"name": "socket-dir", "hostPath": map[string]any{"path": "/var/lib/kubelet/plugins/driver.longhorn.io", "type": "DirectoryOrCreate"}}},
	}}}
	return object
}
func sshLonghornAttacherContainer(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornAttacherPath], "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
}
func sshLonghornAttacherFixture(f *sshStorageFixture) {
	f.objects[clusterSSHLonghornAttacherPath] = sshLonghornAttacherDeploymentFixture()
	sshLonghornAttacherContainer(f)["image"] = sshLonghornDriverContainer(f)["env"].([]any)[3].(map[string]any)["value"]
	sshLonghornAttacherRuntimeFixture(f)
}

func sshLonghornAttacherRuntimeFixture(f *sshStorageFixture) {
	deployment := f.objects[clusterSSHLonghornAttacherPath]
	rs := sshStorageObject("apps/v1", "ReplicaSet", "longhorn-system", "csi-attacher-abcdef")
	clusterSSHStorageMap(rs, "metadata")["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "csi-attacher", "uid": clusterSSHStorageMap(deployment, "metadata")["uid"], "controller": true}}
	// Separate template allocations let tests distinguish owner and Pod drift.
	rs["spec"] = sshLonghornAttacherDeploymentFixture()["spec"]
	image := sshLonghornAttacherContainer(f)["image"]
	clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["image"] = image
	f.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-attacher-abcdef"] = rs
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "longhorn-system", "csi-attacher-"+member.Name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = map[string]any{"app": "csi-attacher"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "csi-attacher-abcdef", "uid": clusterSSHStorageMap(rs, "metadata")["uid"], "controller": true}}
		pod["spec"] = map[string]any{"nodeName": member.Name, "serviceAccountName": "longhorn-service-account", "containers": []any{map[string]any{"name": "csi-attacher", "image": image, "args": sshLonghornCSIStartupArgs("csi-attacher"), "env": sshLonghornCSIStartupEnv()}}}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "csi-attacher", "image": image, "imageID": "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").ManifestDigest, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}}}}
		f.objects[clusterSSHLonghornAttacherPodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshLonghornAttacherPod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func sshLonghornAttacherRuntime(f *sshStorageFixture, index int) map[string]any {
	return clusterSSHStorageMap(sshLonghornAttacherPod(f, index), "status")["containerStatuses"].([]any)[0].(map[string]any)
}
func TestClusterSSHLonghornAttacherRunningImages(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "runtime index", "reordered containers", "reordered statuses", "platform configuration", "missing list", "empty list", "duplicate Pod", "list continuation", "list remainder", "wrong list kind", "wrong namespace", "missing UID", "missing revision", "deleting", "wrong label", "foreign node", "wrong host network", "wrong service account", "missing owner", "duplicate owner", "foreign owner UID", "foreign owner kind", "foreign owner name", "foreign owner API", "noncontroller owner", "spec image mismatch", "spec container name", "sidecar", "init", "ephemeral", "init status", "ephemeral status", "not running", "missing Ready", "duplicate Ready", "not Ready", "missing runtime", "extra runtime", "wrong runtime name", "runtime image mismatch", "not ready", "not started", "waiting", "ambiguous state", "missing start", "zero start", "missing imageID", "tag imageID", "foreign imageID", "wrong pin", "platform to index", "replacement mixed images", "replacement cloned UID", "replacement missing Pod", "replacement empty node"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
			path := clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[0].Name)
			list := f.objects[path]
			pod := sshLonghornAttacherPod(f, 0)
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			owner := meta["ownerReferences"].([]any)[0].(map[string]any)
			container := spec["containers"].([]any)[0].(map[string]any)
			runtime := sshLonghornAttacherRuntime(f, 0)
			switch mode {
			case "reordered containers":
				slices.Reverse(spec["containers"].([]any))
			case "reordered statuses":
				slices.Reverse(status["containerStatuses"].([]any))
			case "runtime index":
				runtime["imageID"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").IndexDigest
			case "platform configuration", "platform to index":
				image := "docker.io/longhornio/csi-attacher" + "@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
				sshLonghornAttacherContainer(f)["image"] = image
				sshLonghornDriverContainer(f)["env"].([]any)[3].(map[string]any)["value"] = image
				sshLonghornDriverRuntimeFixture(f)
				rsSpec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-attacher-abcdef"], "spec"), "template"), "spec")
				rsSpec["containers"].([]any)[0].(map[string]any)["image"] = image
				container["image"], runtime["image"] = image, image
				if mode == "platform to index" {
					runtime["imageID"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").IndexDigest
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
				owner["kind"] = "DaemonSet"
			case "foreign owner name":
				owner["name"] = "other"
			case "foreign owner API":
				owner["apiVersion"] = "v1"
			case "noncontroller owner":
				owner["controller"] = false
			case "spec image mismatch":
				container["image"] = "docker.io/longhornio/csi-attacher" + "@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
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
				runtime["image"] = "docker.io/longhornio/csi-attacher" + "@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
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
				runtime["imageID"] = "docker.io/longhornio/csi-attacher" + ":latest"
			case "foreign imageID":
				runtime["imageID"] = sshSnapshotControllerPin().Reference
			case "wrong pin":
				runtime["imageID"] = "docker.io/longhornio/csi-attacher" + "@sha256:" + strings.Repeat("a", 64)
			case "replacement mixed images":
				sshLonghornAttacherRuntime(f, 1)["imageID"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").IndexDigest
			case "replacement cloned UID":
				clusterSSHStorageMap(sshLonghornAttacherPod(f, 1), "metadata")["uid"] = meta["uid"]
			case "replacement empty node":
				f.objects[clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[1].Name)]["items"] = []any{}
			case "replacement missing Pod":
				delete(f.objects, clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[1].Name))
			}
			// Unavailable failed/inactive member is outside this source evidence set.
			inactive := clusterSSHLonghornAttacherPodsPath("failed-member")
			f.objects[inactive] = map[string]any{"unavailable": true}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			good := textInSet(mode, "expansion", "replacement", "runtime index", "platform configuration", "reordered containers", "reordered statuses", "replacement empty node")
			if good {
				if err != nil || value.Requirements.LonghornAttacherImage.Resolved != runtime["imageID"] {
					t.Fatalf("valid running source rejected: %v", err)
				}
				for _, member := range f.a.Source.Members {
					if f.calls[clusterSSHLonghornAttacherPodsPath(member.Name)] != 1 {
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

func TestClusterSSHLonghornAttacherRuntimeDeniedOrChanged(t *testing.T) {
	for _, mode := range []string{"denied", "lost authority", "receipt drift", "image drift", "Pod replacement", "collection revision"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			path := clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[0].Name)
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
					clusterSSHStorageMap(sshLonghornAttacherPod(f, 0), "metadata")["annotations"] = map[string]any{"changed": "frozen Pod receipt"}
				case "image drift":
					sshLonghornAttacherRuntime(f, 0)["imageID"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").IndexDigest
				case "Pod replacement":
					clusterSSHStorageMap(sshLonghornAttacherPod(f, 0), "metadata")["uid"] = newClusterUUID()
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

func TestClusterSSHLonghornAttacherReplicaSetOwnership(t *testing.T) {
	for _, mode := range []string{"shared owner", "missing", "denied", "lost owner authority", "wrong kind", "wrong API", "wrong namespace", "wrong name", "missing UID", "missing revision", "deleting", "foreign UID", "missing owner", "multiple owners", "wrong parent UID", "wrong parent name", "wrong parent kind", "wrong parent API", "noncontroller", "owner traversal", "owner query", "unknown image", "wrong template container", "template init", "receipt drift", "coherent replacement"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			path := clusterSSHLonghornAttacherReplicaSetPrefix + "csi-attacher-abcdef"
			rs := f.objects[path]
			meta := clusterSSHStorageMap(rs, "metadata")
			parent := meta["ownerReferences"].([]any)[0].(map[string]any)
			podOwner := clusterSSHStorageMap(sshLonghornAttacherPod(f, 0), "metadata")["ownerReferences"].([]any)[0].(map[string]any)
			spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")
			switch mode {
			case "missing":
				delete(f.objects, path)
			case "wrong kind":
				rs["kind"] = "Deployment"
			case "wrong API":
				rs["apiVersion"] = "apps/v2"
			case "wrong namespace":
				meta["namespace"] = "borealis"
			case "wrong name":
				meta["name"] = "csi-attacher-other"
			case "missing UID":
				delete(meta, "uid")
			case "missing revision":
				delete(meta, "resourceVersion")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-04T00:00:00Z"
			case "foreign UID":
				meta["uid"] = newClusterUUID()
			case "missing owner":
				delete(meta, "ownerReferences")
			case "multiple owners":
				meta["ownerReferences"] = []any{parent, parent}
			case "wrong parent UID":
				parent["uid"] = newClusterUUID()
			case "wrong parent name":
				parent["name"] = "other"
			case "wrong parent kind":
				parent["kind"] = "DaemonSet"
			case "wrong parent API":
				parent["apiVersion"] = "v1"
			case "noncontroller":
				parent["controller"] = false
			case "owner traversal":
				podOwner["name"] = "csi-attacher-../other"
			case "owner query":
				podOwner["name"] = "csi-attacher-x?watch=true"
			case "unknown image":
				spec["containers"].([]any)[0].(map[string]any)["image"] = "longhornio/csi-attacher:unknown"
			case "wrong template container":
				spec["containers"].([]any)[0].(map[string]any)["name"] = "other"
			case "template init":
				spec["initContainers"] = []any{map[string]any{"name": "init"}}
			}
			consumed := false
			get := func(ctx context.Context, p string, out any) error {
				if mode == "denied" && p == path {
					return errors.New("private ReplicaSet denial")
				}
				err := f.get(ctx, p, out)
				if mode == "lost owner authority" && p == path {
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
				if mode == "receipt drift" {
					meta["annotations"] = map[string]any{"changed": "receipt"}
				}
				if mode == "coherent replacement" {
					uid := newClusterUUID()
					meta["uid"] = uid
					for i := range f.a.Source.Members {
						clusterSSHStorageMap(sshLonghornAttacherPod(f, i), "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = uid
					}
				}
				return nil
			})
			if mode == "shared owner" {
				if err != nil || !consumed || f.calls[path] < 2 || f.calls[path] != f.calls[clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[0].Name)] {
					t.Fatalf("shared ReplicaSet not read once per observation: %v calls=%d", err, f.calls[path])
				}
			} else if err == nil || (!textInSet(mode, "receipt drift", "coherent replacement") && consumed) {
				t.Fatalf("unproved owner escaped: %v consumed=%v", err, consumed)
			}
			if textInSet(mode, "owner traversal", "owner query") && f.calls[clusterSSHLonghornAttacherReplicaSetPrefix+podOwner["name"].(string)] != 0 {
				t.Fatal("unsafe owner path reached transport")
			}
		})
	}
}

func TestClusterSSHLonghornAttacherRuntimeBounds(t *testing.T) {
	for _, mode := range []string{"multiple on one source", "limit", "over limit", "aggregate over limit", "order", "cross-node duplicate name"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			first := f.objects[clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[0].Name)]
			second := f.objects[clusterSSHLonghornAttacherPodsPath(f.a.Source.Members[1].Name)]
			second["items"] = []any{}
			count := 2
			if textInSet(mode, "limit", "aggregate over limit") {
				count = 16
			}
			if mode == "over limit" {
				count = 17
			}
			items := []any{}
			for i := 0; i < count; i++ {
				pod := sshLonghornAttacherPod(f, 0)
				// Copy before replacing metadata; remaining immutable values can be shared.
				clone := map[string]any{}
				for k, v := range pod {
					clone[k] = v
				}
				meta := map[string]any{}
				for k, v := range clusterSSHStorageMap(pod, "metadata") {
					meta[k] = v
				}
				meta["name"] = "csi-attacher-" + newClusterUUID()
				meta["uid"] = newClusterUUID()
				clone["metadata"] = meta
				items = append(items, clone)
			}
			first["items"] = items
			if textInSet(mode, "aggregate over limit", "cross-node duplicate name") {
				clone := map[string]any{}
				for k, v := range items[0].(map[string]any) {
					clone[k] = v
				}
				spec := map[string]any{}
				for k, v := range clusterSSHStorageMap(clone, "spec") {
					spec[k] = v
				}
				spec["nodeName"] = f.a.Source.Members[1].Name
				clone["spec"] = spec
				meta := map[string]any{}
				for k, v := range clusterSSHStorageMap(clone, "metadata") {
					meta[k] = v
				}
				meta["uid"] = newClusterUUID()
				clone["metadata"] = meta
				if mode == "aggregate over limit" {
					meta["name"] = "csi-attacher-extra"
				}
				second["items"] = []any{clone}
			}
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if mode == "order" {
					slices.Reverse(items)
				}
				return nil
			})
			good := textInSet(mode, "multiple on one source", "limit", "order")
			if (err == nil) != good {
				t.Fatalf("bound/order result %v good=%v", err, good)
			}
		})
	}
}

func TestClusterSSHLonghornAttacherSourceImage(t *testing.T) {
	for _, mode := range []string{"valid", "missing Deployment", "wrong kind", "wrong name", "wrong namespace", "missing UID", "missing revision", "deleting", "missing container", "sidecar", "wrong container", "init", "ephemeral", "wrong role", "unknown image", "image type", "driver disagreement", "desired image drift", "retained startup drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			object := f.objects[clusterSSHLonghornAttacherPath]
			meta := clusterSSHStorageMap(object, "metadata")
			spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
			container := sshLonghornAttacherContainer(f)
			switch mode {
			case "missing Deployment":
				delete(f.objects, clusterSSHLonghornAttacherPath)
			case "wrong kind":
				object["kind"] = "DaemonSet"
			case "wrong name":
				meta["name"] = "other"
			case "wrong namespace":
				meta["namespace"] = "other"
			case "missing UID":
				delete(meta, "uid")
			case "missing revision":
				delete(meta, "resourceVersion")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-04T00:00:00Z"
			case "missing container":
				delete(spec, "containers")
			case "sidecar":
				spec["containers"] = []any{container, container}
			case "wrong container":
				container["name"] = "other"
			case "init":
				spec["initContainers"] = []any{container}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{container}
			case "wrong role":
				container["image"] = sshLonghornDriverPin("csi-provisioner").Reference
			case "unknown image":
				container["image"] = "docker.io/longhornio/csi-attacher:other"
			case "image type":
				container["image"] = map[string]any{}
			case "driver disagreement":
				container["image"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
			}
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				defer f.mu.Unlock()
				if mode == "desired image drift" {
					image := "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
					sshLonghornDriverContainer(f)["env"].([]any)[3].(map[string]any)["value"] = image
					sshLonghornDriverRuntimeFixture(f)
					sshLonghornAttacherFixture(f)
				}
				if mode == "retained startup drift" {
					container["args"] = []any{"--v=3"}
				}
				return nil
			})
			if mode == "valid" {
				if err != nil || !consumed {
					t.Fatalf("valid source rejected: %v", err)
				}
			} else if err == nil || (!textInSet(mode, "desired image drift", "retained startup drift") && consumed) {
				t.Fatalf("unproved source escaped: %v consumed=%v", err, consumed)
			}
		})
	}
}
