package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sshSystemUpgradeRuntimeFixture(f *sshStorageFixture) {
	deployment := f.objects[clusterSSHSystemUpgradePath]
	clone := func(v any) map[string]any {
		raw, _ := json.Marshal(v)
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	rs := sshStorageObject("apps/v1", "ReplicaSet", "system-upgrade", "system-upgrade-controller-abcdef")
	rs["spec"] = clone(deployment["spec"])
	clusterSSHStorageMap(rs, "metadata")["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "system-upgrade-controller", "uid": clusterSSHStorageMap(deployment, "metadata")["uid"], "controller": true}}
	f.objects[clusterSSHSystemUpgradeReplicaSetPrefix+"system-upgrade-controller-abcdef"] = rs
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "system-upgrade", "system-upgrade-controller-"+member.Name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = map[string]any{"upgrade.cattle.io/controller": "system-upgrade-controller"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "system-upgrade-controller-abcdef", "uid": clusterSSHStorageMap(rs, "metadata")["uid"], "controller": true}}
		spec := clone(sshSystemUpgradeSpec(f))
		spec["nodeName"] = member.Name
		// Standard admission projection, without reading its secret payload.
		tmp := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{}}}}
		sshLonghornCSISocketPodFixture(tmp)
		admitted := clusterSSHStorageMap(tmp, "spec")
		spec["volumes"] = append(spec["volumes"].([]any), admitted["volumes"].([]any)[1])
		c := spec["containers"].([]any)[0].(map[string]any)
		c["volumeMounts"] = append(c["volumeMounts"].([]any), admitted["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[1])
		pod["spec"] = spec
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "system-upgrade-controller", "image": c["image"], "imageID": "docker.io/rancher/system-upgrade-controller@" + sshSystemUpgradePin("system-upgrade-controller").ManifestDigest, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}}}}
		f.objects[clusterSSHSystemUpgradePodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshSystemUpgradePod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHSystemUpgradePodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func TestClusterSSHSystemUpgradeRunningIdentity(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "replacement one source Pod", "runtime index", "missing list", "empty list", "duplicate Pod", "list continuation", "missing owner", "foreign owner UID", "foreign deployment UID", "owner template override", "Pod command override", "Pod env override", "credential subpath", "wrong node", "wrong label", "not ready", "not started", "ambiguous state", "unreviewed image", "wrong runtime image", "missing imageID", "extra status", "sidecar", "init", "ephemeral", "service account", "host network"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
			path := clusterSSHSystemUpgradePodsPath(f.a.Source.Members[0].Name)
			list := f.objects[path]
			pod := sshSystemUpgradePod(f, 0)
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			c := spec["containers"].([]any)[0].(map[string]any)
			runtime := status["containerStatuses"].([]any)[0].(map[string]any)
			owner := meta["ownerReferences"].([]any)[0].(map[string]any)
			rs := f.objects[clusterSSHSystemUpgradeReplicaSetPrefix+"system-upgrade-controller-abcdef"]
			switch mode {
			case "replacement one source Pod":
				list["items"] = []any{}
			case "runtime index":
				runtime["imageID"] = "docker.io/rancher/system-upgrade-controller@" + sshSystemUpgradePin("system-upgrade-controller").IndexDigest
			case "missing list":
				delete(f.objects, path)
			case "empty list":
				list["items"] = []any{}
			case "duplicate Pod":
				list["items"] = []any{pod, pod}
			case "list continuation":
				clusterSSHStorageMap(list, "metadata")["continue"] = "more"
			case "missing owner":
				delete(meta, "ownerReferences")
			case "foreign owner UID":
				owner["uid"] = newClusterUUID()
			case "foreign deployment UID":
				clusterSSHStorageMap(rs, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "owner template override":
				clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["command"] = []any{"other"}
			case "Pod command override":
				c["command"] = []any{"other"}
			case "Pod env override":
				c["envFrom"].([]any)[0].(map[string]any)["configMapRef"].(map[string]any)["name"] = "other"
			case "credential subpath":
				c["volumeMounts"].([]any)[4].(map[string]any)["subPath"] = "token"
			case "wrong node":
				spec["nodeName"] = "foreign"
			case "wrong label":
				meta["labels"] = map[string]any{"app": "system-upgrade-controller"}
			case "not ready":
				runtime["ready"] = false
			case "not started":
				runtime["started"] = false
			case "ambiguous state":
				clusterSSHStorageMap(runtime, "state")["waiting"] = map[string]any{}
			case "unreviewed image":
				runtime["imageID"] = "docker.io/rancher/system-upgrade-controller@sha256:" + strings.Repeat("0", 64)
			case "wrong runtime image":
				runtime["image"] = "rancher/kubectl:v1.30.3"
			case "missing imageID":
				delete(runtime, "imageID")
			case "extra status":
				status["containerStatuses"] = append(status["containerStatuses"].([]any), runtime)
			case "sidecar":
				spec["containers"] = append(spec["containers"].([]any), c)
			case "init":
				spec["initContainers"] = []any{c}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{c}
			case "service account":
				spec["serviceAccountName"] = "other"
			case "host network":
				spec["hostNetwork"] = true
			}
			v, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := mode == "expansion" || strings.HasPrefix(mode, "replacement") || mode == "runtime index"
			if (err == nil) != valid {
				t.Fatalf("runtime observation: %v", err)
			}
			if valid && !v.Requirements.SystemUpgradeImages.valid() {
				t.Fatal("invalid runtime projection")
			}
		})
	}
}
func TestClusterSSHSystemUpgradeRuntimeDrift(t *testing.T) {
	for _, mode := range []string{"owner replaced", "image changed", "configuration changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, v clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				pod := sshSystemUpgradePod(f, 0)
				switch mode {
				case "owner replaced":
					clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
				case "image changed":
					clusterSSHStorageMap(pod, "status")["containerStatuses"].([]any)[0].(map[string]any)["imageID"] = "docker.io/rancher/system-upgrade-controller@" + sshSystemUpgradePin("system-upgrade-controller").IndexDigest
				case "configuration changed":
					clusterSSHStorageMap(pod, "spec")["containers"].([]any)[0].(map[string]any)["env"].([]any)[0].(map[string]any)["value"] = "other"
				}
				f.mu.Unlock()
				return checks.Inputs(ctx)
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("drift escaped", err)
			}
		})
	}
}
