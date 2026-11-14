package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"slices"
	"strings"
	"testing"
)

// Literal pinned constructor fixtures, independent of validator tables.
func sshLonghornCSIStartupArgs(role string) []any {
	args := []any{"--v=2", "--csi-address=$(ADDRESS)", "--timeout=1m50s", "--leader-election", "--leader-election-namespace=$(POD_NAMESPACE)", "--kube-api-qps=50", "--kube-api-burst=100", "--http-endpoint=:8000"}
	switch role {
	case "csi-provisioner":
		args = append(args, "--default-fstype=ext4", "--enable-capacity", "--capacity-ownerref-level=2", "--immediate-topology=false")
	case "csi-resizer":
		args = append(args, "--leader-election-namespace=$(POD_NAMESPACE)", "--handle-volume-inuse-error=false", "--feature-gates=RecoverVolumeExpansionFailure=false")
	}
	return args
}
func sshLonghornCSIStartupEnv() []any {
	return []any{
		map[string]any{"name": "ADDRESS", "value": "/csi/csi.sock"},
		map[string]any{"name": "POD_NAMESPACE", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.namespace"}}},
		map[string]any{"name": "NAMESPACE", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.namespace"}}},
		map[string]any{"name": "POD_NAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.name"}}},
	}
}
func TestClusterSSHLonghornCSIStartupContract(t *testing.T) {
	for _, role := range []string{"csi-attacher", "csi-provisioner", "csi-resizer", "csi-snapshotter"} {
		for _, mode := range []string{"constructor", "order", "split strings", "explicit bool", "field version", "resizer single namespace", "wrong role", "missing args", "oversized arg", "too many args", "null args", "args type", "arg type", "unknown flag", "missing flag", "wrong address flag", "wrong qps", "wrong http", "disabled election", "duplicate flag", "conflicting namespace", "excess namespace", "end of flags", "trailing positional", "bare false flag", "split boolean", "role flag swap", "command", "workingDir", "lifecycle", "envFrom", "missing env", "env type", "env entry type", "duplicate env", "unknown env", "wrong address", "address indirection", "wrong field", "field version wrong", "mixed field literal", "field secret", "missing namespace", "timezone override"} {
			t.Run(role+"/"+mode, func(t *testing.T) {
				testedRole := role
				args, env := sshLonghornCSIStartupArgs(role), sshLonghornCSIStartupEnv()
				c := map[string]any{"args": args, "env": env}
				switch mode {
				case "order":
					slices.Reverse(args)
					slices.Reverse(env)
				case "split strings":
					result := []any{}
					for _, raw := range args {
						a := raw.(string)
						flag, value, ok := strings.Cut(a, "=")
						if ok && flag != "--immediate-topology" && flag != "--handle-volume-inuse-error" {
							result = append(result, flag, value)
						} else {
							result = append(result, a)
						}
					}
					c["args"] = result
				case "explicit bool":
					args[3] = "--leader-election=true"
				case "field version":
					for _, raw := range env[1:] {
						clusterSSHStorageMap(clusterSSHStorageMap(raw.(map[string]any), "valueFrom"), "fieldRef")["apiVersion"] = "v1"
					}
				case "resizer single namespace":
					if role == "csi-resizer" {
						c["args"] = append(args[:8], args[9:]...)
					}
				case "wrong role":
					testedRole = "unknown"
				case "oversized arg":
					args[0] = strings.Repeat("x", 513)
				case "too many args":
					for range 30 {
						args = append(args, "--v=2")
					}
					c["args"] = args
				case "missing args":
					delete(c, "args")
				case "null args":
					c["args"] = nil
				case "args type":
					c["args"] = "--v=2"
				case "arg type":
					args[0] = 2
				case "unknown flag":
					c["args"] = append(args, "--kubeconfig=/other")
				case "missing flag":
					c["args"] = args[1:]
				case "wrong address flag":
					args[1] = "--csi-address=/other.sock"
				case "wrong qps":
					args[5] = "--kube-api-qps=51"
				case "wrong http":
					args[7] = "--http-endpoint=:8001"
				case "disabled election":
					args[3] = "--leader-election=false"
				case "duplicate flag":
					c["args"] = append(args, args[0])
				case "conflicting namespace":
					c["args"] = append(args, "--leader-election-namespace=other")
				case "excess namespace":
					c["args"] = append(args, args[4], args[4])
				case "end of flags":
					c["args"] = append([]any{"--"}, args...)
				case "trailing positional":
					c["args"] = append(args, "false")
				case "bare false flag":
					c["args"] = append(args, "--immediate-topology")
				case "split boolean":
					args[3] = "--leader-election"
					c["args"] = append(args[:4], append([]any{"true"}, args[4:]...)...)
				case "role flag swap":
					c["args"] = sshLonghornCSIStartupArgs("csi-provisioner")
					if role == "csi-provisioner" {
						c["args"] = sshLonghornCSIStartupArgs("csi-resizer")
					}
				case "command":
					c["command"] = []any{"sh", "-c", "other"}
				case "workingDir":
					c["workingDir"] = "/other"
				case "lifecycle":
					c["lifecycle"] = map[string]any{"postStart": map[string]any{"exec": map[string]any{"command": []any{"other"}}}}
				case "envFrom":
					c["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
				case "missing env":
					delete(c, "env")
				case "env type":
					c["env"] = "other"
				case "env entry type":
					env[0] = "ADDRESS"
				case "duplicate env":
					env[3] = env[1]
				case "unknown env":
					env[3].(map[string]any)["name"] = "OTHER"
				case "wrong address":
					env[0].(map[string]any)["value"] = "/other.sock"
				case "address indirection":
					env[0] = map[string]any{"name": "ADDRESS", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "other"}}}
				case "wrong field":
					clusterSSHStorageMap(clusterSSHStorageMap(env[1].(map[string]any), "valueFrom"), "fieldRef")["fieldPath"] = "metadata.name"
				case "field version wrong":
					clusterSSHStorageMap(clusterSSHStorageMap(env[1].(map[string]any), "valueFrom"), "fieldRef")["apiVersion"] = "v2"
				case "mixed field literal":
					env[1].(map[string]any)["value"] = "longhorn-system"
				case "field secret":
					env[1].(map[string]any)["valueFrom"] = map[string]any{"secretKeyRef": map[string]any{"name": "other"}}
				case "missing namespace":
					c["env"] = env[:3]
				case "timezone override":
					c["env"] = append(env, map[string]any{"name": "TZ", "value": "UTC"})
				}
				want := textInSet(mode, "constructor", "order", "split strings", "explicit bool", "field version", "resizer single namespace")
				if clusterSSHLonghornCSIStartup(c, testedRole) != want {
					t.Fatalf("startup result mismatched: role=%s good=%v", role, want)
				}
			})
		}
	}
}
func sshLonghornCSIStartupObjects(f *sshStorageFixture, role string) []map[string]any {
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
	container := func(o map[string]any) map[string]any {
		return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(o, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
	}
	return []map[string]any{container(deployment), container(rs), clusterSSHStorageMap(pod, "spec")["containers"].([]any)[0].(map[string]any)}
}
func TestClusterSSHLonghornCSIStartupSourceBoundaries(t *testing.T) {
	for _, role := range []string{"csi-attacher", "csi-provisioner", "csi-resizer", "csi-snapshotter"} {
		for _, layer := range []string{"Deployment", "ReplicaSet", "Pod"} {
			for _, mode := range []string{"valid reordered", "bad args", "bad environment", "command override"} {
				t.Run(role+"/"+layer+"/"+mode, func(t *testing.T) {
					f := newSSHStorageFixture(t, false)
					c := sshLonghornCSIStartupObjects(f, role)[slices.Index([]string{"Deployment", "ReplicaSet", "Pod"}, layer)]
					switch mode {
					case "valid reordered":
						slices.Reverse(c["args"].([]any))
						slices.Reverse(c["env"].([]any))
					case "bad args":
						c["args"] = []any{"--csi-address=/foreign.sock"}
					case "bad environment":
						c["env"].([]any)[0].(map[string]any)["value"] = "/foreign.sock"
					case "command override":
						c["command"] = []any{"other"}
					}
					v, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
					if mode == "valid reordered" {
						if err != nil {
							t.Fatal(err)
						}
					} else if err != clusterbootstrap.ErrPreparationConfig || v.Requirements.observation != "" {
						t.Fatalf("unproved startup escaped: %v", err)
					}
				})
			}
		}
	}
}

func TestClusterSSHLonghornCSIStartupReceiptDrift(t *testing.T) {
	for _, role := range []string{"csi-attacher", "csi-provisioner", "csi-resizer", "csi-snapshotter"} {
		t.Run(role, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				defer f.mu.Unlock()
				// Still semantically valid; retained full-object receipts must reject drift.
				slices.Reverse(sshLonghornCSIStartupObjects(f, role)[2]["args"].([]any))
				return nil
			})
			if !consumed || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatalf("startup receipt drift escaped: consumed=%v err=%v", consumed, err)
			}
		})
	}
}
