package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sshLonghornPluginFixture(f *sshStorageFixture) {
	driver, err := clusterSSHLonghornDriverTemplateImages(f.objects[clusterSSHLonghornDriverPath])
	if err != nil {
		panic(err)
	}
	root := "/var/lib/kubelet"
	// The fixture follows the same configured CSI path as deployment fixtures.
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHLonghornAttacherPath], "spec"), "template"), "spec")
	for _, raw := range spec["volumes"].([]any) {
		v := raw.(map[string]any)
		if v["name"] == "socket-dir" {
			root = strings.TrimSuffix(strings.TrimSuffix(clusterSSHStorageText(clusterSSHStorageMap(v, "hostPath"), "path"), "/"), clusterbootstrap.KubeletCSIDirectorySuffix)
		}
	}
	volumes := []any{}
	for _, pair := range [][2]string{{"kubernetes-csi-dir", root + "/plugins/kubernetes.io/csi"}, {"registration-dir", root + "/plugins_registry"}, {"socket-dir", root + clusterbootstrap.KubeletCSIDirectorySuffix}, {"pods-mount-dir", root + "/pods"}, {"host-dev", "/dev"}, {"host-proc", "/proc"}, {"host-sys", "/sys"}, {"lib-modules", "/lib/modules"}} {
		host := map[string]any{"path": pair[1]}
		if strings.HasPrefix(pair[1], root+"/") {
			host["type"] = "DirectoryOrCreate"
		}
		volumes = append(volumes, map[string]any{"name": pair[0], "hostPath": host})
	}
	containers := []any{}
	for _, name := range []string{"longhorn-csi-plugin", "node-driver-registrar", "longhorn-liveness-probe"} {
		c := map[string]any{"name": name, "image": driver.Liveness, "args": []any{"--v=4", "--csi-address=/csi/csi.sock"}, "volumeMounts": []any{map[string]any{"name": "socket-dir", "mountPath": "/csi/"}}}
		hook := ""
		if name == "longhorn-csi-plugin" {
			c["image"] = driver.Manager
			c["args"] = []any{"longhorn-manager", "-d", "csi", "--nodeid=$(NODE_ID)", "--endpoint=$(CSI_ENDPOINT)", "--drivername=driver.longhorn.io", "--manager-url=http://longhorn-backend:9500/v1"}
			c["env"] = []any{map[string]any{"name": "NODE_ID", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "spec.nodeName"}}}, map[string]any{"name": "CSI_ENDPOINT", "value": "unix:///csi/csi.sock"}, map[string]any{"name": "POD_NAMESPACE", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.namespace"}}}}
			for _, pair := range [][2]string{{"kubernetes-csi-dir", root + "/plugins/kubernetes.io/csi"}, {"pods-mount-dir", root + "/pods"}, {"host-dev", "/dev"}, {"host-proc", "/host/proc"}, {"host-sys", "/sys"}, {"lib-modules", "/lib/modules"}} {
				m := map[string]any{"name": pair[0], "mountPath": pair[1]}
				if pair[0] == "lib-modules" {
					m["readOnly"] = true
				}
				if pair[0] == "kubernetes-csi-dir" || pair[0] == "pods-mount-dir" {
					m["mountPropagation"] = "Bidirectional"
				}
				c["volumeMounts"] = append(c["volumeMounts"].([]any), m)
			}
			hook = "rm -f /csi//*"
		} else if name == "node-driver-registrar" {
			c["image"] = driver.Registrar
			c["args"] = []any{"--v=2", "--csi-address=$(ADDRESS)", "--kubelet-registration-path=" + root + clusterbootstrap.KubeletCSISocketSuffix}
			c["env"] = []any{map[string]any{"name": "ADDRESS", "value": "/csi/csi.sock"}}
			c["volumeMounts"] = append(c["volumeMounts"].([]any), map[string]any{"name": "registration-dir", "mountPath": "/registration"})
			hook = "rm -rf /registration/driver.longhorn.io /registration/driver.longhorn.io-reg.sock /csi//*"
		}
		if hook != "" {
			c["securityContext"] = map[string]any{"privileged": true}
			c["lifecycle"] = map[string]any{"preStop": map[string]any{"exec": map[string]any{"command": []any{"/bin/sh", "-c", hook}}}}
		}
		containers = append(containers, c)
	}
	template := map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "longhorn-csi-plugin"}}, "spec": map[string]any{"serviceAccountName": "longhorn-service-account", "containers": containers, "volumes": volumes}}
	ds := sshStorageObject("apps/v1", "DaemonSet", "longhorn-system", "longhorn-csi-plugin")
	ds["spec"] = map[string]any{"template": template}
	f.objects[clusterSSHLonghornPluginPath] = ds
	for _, member := range f.a.Source.Members {
		pod := sshStorageObject("v1", "Pod", "longhorn-system", "longhorn-csi-plugin-"+member.Name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["uid"] = member.NodeUID
		meta["labels"] = map[string]any{"app": "longhorn-csi-plugin"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "longhorn-csi-plugin", "uid": clusterSSHStorageMap(ds, "metadata")["uid"], "controller": true}}
		raw, _ := json.Marshal(template["spec"])
		var podSpec map[string]any
		_ = json.Unmarshal(raw, &podSpec)
		podSpec["nodeName"] = member.Name
		pod["spec"] = podSpec
		statuses := []any{}
		for i, raw := range containers {
			c := raw.(map[string]any)
			role := []string{"longhorn-manager", "csi-node-driver-registrar", "livenessprobe"}[i]
			cid := strings.Repeat(string(rune('a'+i)), 64)
			if i == 0 {
				cid = sshSourceNetworkFixture(member, f.a.K3sVersion).Kubelet.CSISocket.Listener.ContainerID
			}
			statuses = append(statuses, map[string]any{"name": c["name"], "image": c["image"], "imageID": "docker.io/longhornio/" + role + "@" + sshLonghornDriverPin(role).ManifestDigest, "ready": true, "started": true, "containerID": "containerd://" + cid, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-01T00:00:00Z"}}})
		}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": statuses}
		f.objects[clusterSSHLonghornPluginPodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}
func sshLonghornPluginPod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHLonghornPluginPodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func TestClusterSSHLonghornPlugin(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "missing daemonset", "foreign owner", "wrong node", "not ready", "foreign container", "image drift", "command override", "endpoint override", "socket subpath", "socket path", "missing peer", "peer mismatch", "peer UID", "peer drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			pod := sshLonghornPluginPod(f, 0)
			spec := clusterSSHStorageMap(pod, "spec")
			c := spec["containers"].([]any)[0].(map[string]any)
			runtime := clusterSSHStorageMap(pod, "status")["containerStatuses"].([]any)[0].(map[string]any)
			switch mode {
			case "missing daemonset":
				delete(f.objects, clusterSSHLonghornPluginPath)
			case "foreign owner":
				clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "wrong node":
				spec["nodeName"] = "other"
			case "not ready":
				runtime["ready"] = false
			case "foreign container":
				runtime["containerID"] = "docker://" + strings.Repeat("a", 64)
			case "image drift":
				runtime["imageID"] = "docker.io/longhornio/longhorn-manager@sha256:" + strings.Repeat("0", 64)
			case "command override":
				c["command"] = []any{"other"}
			case "endpoint override":
				c["env"].([]any)[1].(map[string]any)["value"] = "unix:///other.sock"
			case "socket subpath":
				c["volumeMounts"].([]any)[0].(map[string]any)["subPath"] = "other"
			case "socket path":
				spec["volumes"].([]any)[2].(map[string]any)["hostPath"].(map[string]any)["path"] = "/other"
			}
			snapshot, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if mode != "expansion" && mode != "replacement" && !strings.HasPrefix(mode, "peer") && mode != "missing peer" {
				if err == nil {
					t.Fatal("unsafe plugin accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			networks := []clusterbootstrap.SourceNetwork{}
			for _, member := range f.a.Source.Members {
				networks = append(networks, sshSourceNetworkFixture(member, f.a.K3sVersion))
			}
			switch mode {
			case "missing peer":
				snapshot.Requirements.LonghornPlugin = nil
			case "peer mismatch":
				networks[0].Kubelet.CSISocket.Listener.ContainerID = strings.Repeat("f", 64)
			case "peer UID":
				networks[0].Kubelet.CSISocket.Listener.UserID = 1
			case "peer drift":
				networks[0].Kubelet.CSISocket.Listener.PodUID = newClusterUUID()
			}
			if snapshot.Requirements.csiSocketMatchesNetworks(networks) != (mode == "expansion" || mode == "replacement") {
				t.Fatal("native peer binding mismatch")
			}
		})
	}
}
