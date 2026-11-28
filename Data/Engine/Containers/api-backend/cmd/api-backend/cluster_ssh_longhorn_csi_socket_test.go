package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func sshLonghornCSISocketPodFixture(pod map[string]any) {
	spec := clusterSSHStorageMap(pod, "spec")
	spec["volumes"] = []any{
		map[string]any{"name": "socket-dir", "hostPath": map[string]any{"path": "/var/lib/kubelet/plugins/driver.longhorn.io", "type": "DirectoryOrCreate"}},
		map[string]any{"name": "kube-api-access-abcde", "projected": map[string]any{"defaultMode": json.Number("420"), "sources": []any{
			map[string]any{"serviceAccountToken": map[string]any{"path": "token", "expirationSeconds": json.Number("3607")}},
			map[string]any{"configMap": map[string]any{"name": "kube-root-ca.crt", "items": []any{map[string]any{"key": "ca.crt", "path": "ca.crt"}}}},
			map[string]any{"downwardAPI": map[string]any{"items": []any{map[string]any{"path": "namespace", "fieldRef": map[string]any{"apiVersion": "v1", "fieldPath": "metadata.namespace"}}}}},
		}}},
	}
	spec["containers"].([]any)[0].(map[string]any)["volumeMounts"] = []any{
		map[string]any{"name": "socket-dir", "mountPath": "/csi/"},
		map[string]any{"name": "kube-api-access-abcde", "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount", "readOnly": true},
	}
}
func sshLonghornCSISocketSpecs(f *sshStorageFixture, role string) []map[string]any {
	var deployment, pod map[string]any
	if role == "csi-attacher" {
		deployment = f.objects[clusterSSHLonghornAttacherPath]
		pod = sshLonghornAttacherPod(f, 0)
	} else {
		r := clusterSSHLonghornCSIRole(role)
		deployment = f.objects[r.deploymentPath()]
		pod = sshLonghornCSIPod(f, r, 0)
	}
	rs := f.objects[clusterSSHLonghornAttacherReplicaSetPrefix+role+"-abcdef"]
	template := func(o map[string]any) map[string]any {
		return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(o, "spec"), "template"), "spec")
	}
	return []map[string]any{template(deployment), template(rs), clusterSSHStorageMap(pod, "spec")}
}
func sshLonghornCSISetSocket(f *sshStorageFixture, socket string) {
	for _, role := range []string{"csi-attacher", "csi-provisioner", "csi-resizer", "csi-snapshotter"} {
		for _, spec := range sshLonghornCSISocketSpecs(f, role)[:2] {
			clusterSSHStorageMap(spec["volumes"].([]any)[0].(map[string]any), "hostPath")["path"] = socket
		}
		for i := range f.a.Source.Members {
			var pod map[string]any
			if role == "csi-attacher" {
				pod = sshLonghornAttacherPod(f, i)
			} else {
				pod = sshLonghornCSIPod(f, clusterSSHLonghornCSIRole(role), i)
			}
			clusterSSHStorageMap(clusterSSHStorageMap(pod, "spec")["volumes"].([]any)[0].(map[string]any), "hostPath")["path"] = socket
		}
	}
}
func TestClusterSSHLonghornCSISocketSourceTopology(t *testing.T) {
	for _, role := range []string{"csi-attacher", "csi-provisioner", "csi-resizer", "csi-snapshotter"} {
		for layer := range 3 {
			for _, mode := range []string{"valid", "order", "defaults", "no projected Pod volume", "missing volumes", "extra volume", "duplicate volume", "wrong source type", "mixed source", "wrong host type", "host path mismatch", "relative path", "root path", "traversal", "unknown host key", "missing mounts", "extra mount", "duplicate mount", "unknown mount name", "wrong destination", "read-only socket", "mount propagation", "subPath", "subPathExpr", "volumeDevices", "template injection"} {
				t.Run(role+"/"+[]string{"Deployment", "ReplicaSet", "Pod"}[layer]+"/"+mode, func(t *testing.T) {
					f := newSSHStorageFixture(t, true)
					spec := sshLonghornCSISocketSpecs(f, role)[layer]
					volumes := spec["volumes"].([]any)
					c := spec["containers"].([]any)[0].(map[string]any)
					mounts := c["volumeMounts"].([]any)
					volume, mount := volumes[0].(map[string]any), mounts[0].(map[string]any)
					host := clusterSSHStorageMap(volume, "hostPath")
					switch mode {
					case "order":
						slices.Reverse(volumes)
						slices.Reverse(mounts)
					case "defaults":
						mount["readOnly"] = false
						mount["mountPropagation"] = "None"
						mount["mountPath"] = "/csi"
					case "no projected Pod volume":
						if layer == 2 {
							spec["volumes"] = volumes[:1]
							c["volumeMounts"] = mounts[:1]
						}
					case "missing volumes":
						delete(spec, "volumes")
					case "extra volume":
						spec["volumes"] = append(volumes, map[string]any{"name": "other", "emptyDir": map[string]any{}})
					case "duplicate volume":
						spec["volumes"] = append(volumes, volume)
					case "wrong source type":
						delete(volume, "hostPath")
						volume["emptyDir"] = map[string]any{}
					case "mixed source":
						volume["emptyDir"] = map[string]any{}
					case "wrong host type":
						host["type"] = "Socket"
					case "host path mismatch":
						host["path"] = "/srv/kubelet/plugins/driver.longhorn.io"
					case "relative path":
						host["path"] = "var/lib/kubelet/plugins/driver.longhorn.io"
					case "root path":
						host["path"] = "/plugins/driver.longhorn.io"
					case "traversal":
						host["path"] = "/var/lib/other/../kubelet/plugins/driver.longhorn.io"
					case "unknown host key":
						host["other"] = true
					case "missing mounts":
						delete(c, "volumeMounts")
					case "extra mount":
						c["volumeMounts"] = append(mounts, map[string]any{"name": "socket-dir", "mountPath": "/usr/local/bin"})
					case "duplicate mount":
						c["volumeMounts"] = append(mounts, mount)
					case "unknown mount name":
						mount["name"] = "other"
					case "wrong destination":
						mount["mountPath"] = "/"
					case "read-only socket":
						mount["readOnly"] = true
					case "mount propagation":
						mount["mountPropagation"] = "Bidirectional"
					case "subPath":
						mount["subPath"] = "other"
					case "subPathExpr":
						mount["subPathExpr"] = "$(POD_NAME)"
					case "volumeDevices":
						c["volumeDevices"] = []any{map[string]any{"name": "socket-dir", "devicePath": "/csi"}}
					case "template injection":
						if layer == 2 {
							volume["hostPath"] = map[string]any{"path": "/etc", "type": "DirectoryOrCreate"}
						} else {
							pod := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{}}}}
							sshLonghornCSISocketPodFixture(pod)
							injected := clusterSSHStorageMap(pod, "spec")
							spec["volumes"] = append(volumes, injected["volumes"].([]any)[1])
							c["volumeMounts"] = append(mounts, injected["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[1])
						}
					}
					v, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
					good := textInSet(mode, "valid", "order", "defaults", "no projected Pod volume")
					if good {
						if err != nil || v.Requirements.LonghornCSISocketPath != "/var/lib/kubelet/plugins/driver.longhorn.io" {
							t.Fatalf("valid socket rejected: %v", err)
						}
					} else if err != clusterbootstrap.ErrPreparationConfig || v.Requirements.observation != "" {
						t.Fatalf("unsafe topology escaped: %v", err)
					}
				})
			}
		}
	}
}
func TestClusterSSHLonghornCSISocketProjectionAndDrift(t *testing.T) {
	for _, root := range []string{"/var/lib/kubelet", "/var/lib/rancher/k3s/agent/kubelet", "/srv/custom kubelet"} {
		t.Run(root, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			socket := root + "/plugins/driver.longhorn.io"
			sshLonghornCSISetSocket(f, socket+"/")
			sshLonghornPluginFixture(f)
			v, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != nil || v.Requirements.LonghornCSISocketPath != socket {
				t.Fatalf("actual declared path lost: %v", err)
			}
			snapshot := clusterSSHStorageSnapshot{Requirements: v.Requirements, Observation: v.Requirements.observation}
			snapshot.Requirements.observation = ""
			if !validClusterSSHStorageSnapshot(snapshot, f.a.Source) {
				t.Fatal("valid projection rejected")
			}
			for _, bad := range []string{"", "/", "relative/plugins/driver.longhorn.io", "/plugins/driver.longhorn.io", socket + "/", root + "//plugins/driver.longhorn.io", socket + "\x00", "/" + strings.Repeat("x", 1024) + "/plugins/driver.longhorn.io"} {
				broken := snapshot
				broken.Requirements.LonghornCSISocketPath = bad
				if validClusterSSHStorageSnapshot(broken, f.a.Source) {
					t.Fatalf("bad socket projection accepted: %q", bad)
				}
			}
			consumed := false
			err = withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				defer f.mu.Unlock()
				sshLonghornCSISetSocket(f, "/srv/changed/plugins/driver.longhorn.io")
				return nil
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatalf("coherent socket drift escaped: %v", err)
			}
		})
	}
}
func TestClusterSSHLonghornCSISocketServiceAccountProjection(t *testing.T) {
	for _, mode := range []string{"valid", "source order", "writable", "wrong mount", "unknown volume", "secret source", "mode", "token path", "token expiration", "token audience", "CA name", "CA path", "namespace path", "namespace field", "duplicate source", "extra source", "missing mount", "subpath"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec := sshLonghornCSISocketSpecs(f, "csi-attacher")[2]
			volume := spec["volumes"].([]any)[1].(map[string]any)
			projection := clusterSSHStorageMap(volume, "projected")
			sources := projection["sources"].([]any)
			mount := spec["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[1].(map[string]any)
			switch mode {
			case "source order":
				slices.Reverse(sources)
			case "writable":
				mount["readOnly"] = false
			case "wrong mount":
				mount["mountPath"] = "/csi"
			case "unknown volume":
				volume["name"] = "other"
				mount["name"] = "other"
			case "secret source":
				delete(volume, "projected")
				volume["secret"] = map[string]any{"secretName": "other"}
			case "mode":
				projection["defaultMode"] = json.Number("511")
			case "token path":
				clusterSSHStorageMap(sources[0].(map[string]any), "serviceAccountToken")["path"] = "../other"
			case "token expiration":
				clusterSSHStorageMap(sources[0].(map[string]any), "serviceAccountToken")["expirationSeconds"] = json.Number("3607.0")
			case "token audience":
				clusterSSHStorageMap(sources[0].(map[string]any), "serviceAccountToken")["audience"] = "other"
			case "CA name":
				clusterSSHStorageMap(sources[1].(map[string]any), "configMap")["name"] = "other"
			case "CA path":
				clusterSSHStorageMap(sources[1].(map[string]any), "configMap")["items"].([]any)[0].(map[string]any)["path"] = "other"
			case "namespace path":
				clusterSSHStorageMap(sources[2].(map[string]any), "downwardAPI")["items"].([]any)[0].(map[string]any)["path"] = "other"
			case "namespace field":
				clusterSSHStorageMap(clusterSSHStorageMap(sources[2].(map[string]any), "downwardAPI")["items"].([]any)[0].(map[string]any), "fieldRef")["fieldPath"] = "metadata.name"
			case "duplicate source":
				sources[2] = sources[0]
			case "extra source":
				projection["sources"] = append(sources, sources[0])
			case "missing mount":
				spec["containers"].([]any)[0].(map[string]any)["volumeMounts"] = []any{spec["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[0]}
			case "subpath":
				mount["subPath"] = "token"
			}
			_, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if (err == nil) != textInSet(mode, "valid", "source order") {
				t.Fatalf("projection result mismatch: %v", err)
			}
		})
	}
}

func TestClusterSSHLonghornCSISocketNativeRootBinding(t *testing.T) {
	_, _, snapshot := sshBrokerFixture(t)
	source := snapshot.Sources[0]
	storage := snapshot.Storage.Requirements
	for _, root := range []string{"/var/lib/kubelet", "/srv/custom root", "/var/lib/rancher/k3s/agent/kubelet"} {
		source.Kubelet.Root = root
		storage.LonghornCSISocketPath = root + "/plugins/driver.longhorn.io"
		for _, mode := range []string{"one source", "replacement", "missing", "mismatch", "second mismatch", "invalid identity", "invalid namespace"} {
			t.Run(root+"/"+mode, func(t *testing.T) {
				current := storage
				sources := []clusterbootstrap.SourceNetwork{source}
				switch mode {
				case "replacement":
					sources = append(sources, source)
					sources[1].Kubelet.PID++
					sources[1].NodeUID = newClusterUUID()
					sources[1].Hostname = "second-source"
					sources[1].Kubelet.CSISocket.Listener.PodUID = newClusterUUID()
					sources[1].Kubelet.CSISocket.Listener.ContainerID = strings.Repeat("f", 64)
					second := storage.LonghornPlugin[0]
					second.NodeUID = sources[1].NodeUID
					second.Node = sources[1].Hostname
					second.PodUID = sources[1].Kubelet.CSISocket.Listener.PodUID
					second.ContainerID = sources[1].Kubelet.CSISocket.Listener.ContainerID
					current.LonghornPlugin = append(slices.Clone(storage.LonghornPlugin), second)
				case "missing":
					sources = nil
				case "mismatch":
					sources[0].Kubelet.Root = "/other"
				case "second mismatch":
					sources = append(sources, source)
					sources[1].Kubelet.Root = "/other"
				case "invalid identity":
					sources[0].Kubelet.Invocation = ""
				case "invalid namespace":
					sources[0].Kubelet.NetworkNamespace++
				}
				if current.csiSocketMatchesNetworks(sources) != (mode == "one source" || mode == "replacement") {
					t.Fatal("declared socket/native root binding")
				}
			})
		}
	}
}
