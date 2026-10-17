package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sshCertManagerRuntimeFixture(f *sshStorageFixture) {
	clone := func(value any) map[string]any {
		raw, _ := json.Marshal(value)
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	for _, name := range clusterSSHCertManagerDeployments {
		role, label := clusterSSHCertManagerRole(name)
		deployment := f.objects[clusterSSHCertManagerDeploymentPrefix+name]
		rs := sshStorageObject("apps/v1", "ReplicaSet", "cert-manager", name+"-abcdef")
		rs["spec"] = clone(deployment["spec"])
		clusterSSHStorageMap(rs, "metadata")["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": name, "uid": clusterSSHStorageMap(deployment, "metadata")["uid"], "controller": true}}
		f.objects[clusterSSHCertManagerReplicaSetPrefix+name+"-abcdef"] = rs
		for _, member := range f.a.Source.Members {
			pod := sshStorageObject("v1", "Pod", "cert-manager", name+"-"+member.Name)
			meta := clusterSSHStorageMap(pod, "metadata")
			meta["labels"] = map[string]any{"app.kubernetes.io/name": label}
			meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": name + "-abcdef", "uid": clusterSSHStorageMap(rs, "metadata")["uid"], "controller": true}}
			spec := clone(sshCertManagerSpec(f, name))
			spec["nodeName"] = member.Name
			tmp := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{}}}}
			sshLonghornCSISocketPodFixture(tmp)
			admitted := clusterSSHStorageMap(tmp, "spec")
			spec["volumes"] = []any{admitted["volumes"].([]any)[1]}
			c := spec["containers"].([]any)[0].(map[string]any)
			c["volumeMounts"] = []any{admitted["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[1]}
			pod["spec"] = spec
			resolved := "quay.io/jetstack/cert-manager-" + role + "@" + sshCertManagerPin(role).IndexDigest
			if c["image"] == "quay.io/jetstack/cert-manager-"+role+"@"+sshCertManagerPin(role).ManifestDigest {
				resolved = c["image"].(string)
			}
			pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "cert-manager-" + role, "image": c["image"], "imageID": resolved, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}}}}
			f.objects[clusterSSHCertManagerPodsPath(name, member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
		}
	}
}
func sshCertManagerRuntimePod(f *sshStorageFixture, name string, index int) map[string]any {
	return f.objects[clusterSSHCertManagerPodsPath(name, f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func TestClusterSSHCertManagerRunningIdentity(t *testing.T) {
	for _, name := range clusterSSHCertManagerDeployments {
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"expansion", "replacement one source", "runtime platform", "no evidence", "wrong owner", "wrong role", "wrong service account", "Pod arguments changed", "owner arguments changed", "credential subpath", "extra volume", "missing runtime"} {
				t.Run(mode, func(t *testing.T) {
					f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
					pod := sshCertManagerRuntimePod(f, name, 0)
					spec := clusterSSHStorageMap(pod, "spec")
					c := spec["containers"].([]any)[0].(map[string]any)
					runtime := clusterSSHStorageMap(pod, "status")["containerStatuses"].([]any)[0].(map[string]any)
					role, _ := clusterSSHCertManagerRole(name)
					switch mode {
					case "replacement one source", "no evidence":
						f.objects[clusterSSHCertManagerPodsPath(name, f.a.Source.Members[0].Name)]["items"] = []any{}
					case "runtime platform":
						runtime["imageID"] = "quay.io/jetstack/cert-manager-" + role + "@" + sshCertManagerPin(role).ManifestDigest
					case "wrong owner":
						clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
					case "wrong role":
						runtime["imageID"] = "quay.io/jetstack/cert-manager-acmesolver@" + sshCertManagerPin("acmesolver").IndexDigest
					case "wrong service account":
						spec["serviceAccountName"] = "other"
					case "Pod arguments changed":
						c["args"].([]any)[0] = "--v=3"
					case "owner arguments changed":
						rs := f.objects[clusterSSHCertManagerReplicaSetPrefix+name+"-abcdef"]
						clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(rs, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["args"].([]any)[0] = "--v=3"
					case "credential subpath":
						c["volumeMounts"].([]any)[0].(map[string]any)["subPath"] = "token"
					case "extra volume":
						spec["volumes"] = append(spec["volumes"].([]any), map[string]any{"name": "other", "emptyDir": map[string]any{}})
					case "missing runtime":
						delete(runtime, "imageID")
					}
					result, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
					good := mode == "expansion" || strings.HasPrefix(mode, "replacement") || mode == "runtime platform"
					if (err == nil) != good {
						t.Fatalf("runtime observation: %v", err)
					}
					if good && !result.Requirements.CertManagerImages.valid() {
						t.Fatal("invalid runtime projection")
					}
				})
			}
		})
	}
}
