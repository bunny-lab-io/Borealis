package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sshSnapshotRuntimeFixture(f *sshStorageFixture) {
	deployment := f.objects[clusterSSHSnapshotControllerPath]
	clone := func(v any) map[string]any {
		raw, _ := json.Marshal(v)
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	rs := sshStorageObject("apps/v1", "ReplicaSet", "kube-system", "snapshot-controller-abcdef")
	rs["spec"] = clone(deployment["spec"])
	clusterSSHStorageMap(rs, "metadata")["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "snapshot-controller", "uid": clusterSSHStorageMap(deployment, "metadata")["uid"], "controller": true}}
	f.objects[clusterSSHSnapshotReplicaSetPrefix+"snapshot-controller-abcdef"] = rs
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "kube-system", "snapshot-controller-"+member.Name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = map[string]any{"app.kubernetes.io/name": "snapshot-controller"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "snapshot-controller-abcdef", "uid": clusterSSHStorageMap(rs, "metadata")["uid"], "controller": true}}
		spec := clone(sshSnapshotControllerSpec(f))
		spec["nodeName"] = member.Name
		// Standard admission projection, without reading its secret payload.
		tmp := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{}}}}
		sshLonghornCSISocketPodFixture(tmp)
		admitted := clusterSSHStorageMap(tmp, "spec")
		spec["volumes"] = []any{admitted["volumes"].([]any)[1]}
		c := spec["containers"].([]any)[0].(map[string]any)
		c["volumeMounts"] = []any{admitted["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[1]}
		pod["spec"] = spec
		resolved := clusterSSHSnapshotControllerRepository + "@" + sshSnapshotControllerPin().IndexDigest
		if c["image"] == clusterSSHSnapshotControllerRepository+"@"+sshSnapshotControllerPin().ManifestDigest {
			resolved = c["image"].(string)
		}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "snapshot-controller", "image": c["image"], "imageID": resolved, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}}}}
		f.objects[clusterSSHSnapshotPodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshSnapshotRuntimePod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHSnapshotPodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}

func TestClusterSSHSnapshotControllerRunningIdentity(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "replacement one source", "runtime platform", "no evidence", "owner UID", "owner invocation", "Pod invocation", "credential subpath", "extra mount", "missing identity"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
			pod := sshSnapshotRuntimePod(f, 0)
			spec := clusterSSHStorageMap(pod, "spec")
			c := spec["containers"].([]any)[0].(map[string]any)
			rs := f.objects[clusterSSHSnapshotReplicaSetPrefix+"snapshot-controller-abcdef"]
			runtime := clusterSSHStorageMap(pod, "status")["containerStatuses"].([]any)[0].(map[string]any)
			switch mode {
			case "replacement one source", "no evidence":
				f.objects[clusterSSHSnapshotPodsPath(f.a.Source.Members[0].Name)]["items"] = []any{}
			case "runtime platform":
				runtime["imageID"] = clusterSSHSnapshotControllerRepository + "@" + sshSnapshotControllerPin().ManifestDigest
			case "owner UID":
				clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "owner invocation":
				clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["args"] = []any{"--other=true"}
			case "Pod invocation":
				c["args"] = []any{"--other=true"}
			case "credential subpath":
				c["volumeMounts"].([]any)[0].(map[string]any)["subPath"] = "token"
			case "extra mount":
				spec["volumes"] = append(spec["volumes"].([]any), map[string]any{"name": "other", "emptyDir": map[string]any{}})
			case "missing identity":
				delete(runtime, "imageID")
			}
			v, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			good := mode == "expansion" || strings.HasPrefix(mode, "replacement") || mode == "runtime platform"
			if (err == nil) != good {
				t.Fatalf("runtime observation: %v", err)
			}
			if good && !(clusterSSHSourceExternalImage{Configured: v.Requirements.SnapshotControllerImage, Resolved: v.Requirements.SnapshotControllerResolved}).valid(clusterSSHSnapshotControllerRepository) {
				t.Fatal("invalid runtime projection")
			}
		})
	}
}
