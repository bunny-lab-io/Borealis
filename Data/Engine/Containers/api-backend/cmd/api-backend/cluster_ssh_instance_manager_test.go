package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sshInstanceManagerFixture(f *sshStorageFixture) {
	items, ok := clusterSSHInstanceExpectedPolicy("64Mi", "1500m", "128Mi")
	if !ok {
		panic("invalid policy fixture")
	}
	for i, path := range []string{clusterSSHInstancePolicyPath, clusterSSHInstanceBindingPath} {
		object := sshStorageObject("admissionregistration.k8s.io/v1", items[i]["kind"].(string), "", clusterSSHInstancePolicyName)
		object["spec"] = items[i]["spec"]
		f.objects[path] = object
	}
	setting := sshStorageObject("longhorn.io/v1beta2", "Setting", "longhorn-system", "guaranteed-instance-manager-cpu")
	setting["value"] = `{"v1":"12","v2":"12"}`
	f.objects[clusterSSHInstanceCPUPath] = setting
	pin := sshLonghornDriverPin("longhorn-instance-manager")
	for i, member := range f.a.Source.Members {
		node := sshStorageObject("longhorn.io/v1beta2", "Node", "longhorn-system", member.Name)
		node["spec"] = map[string]any{"instanceManagerCPURequest": 0}
		f.objects[clusterSSHInstanceNodePrefix+member.Name] = node
		kube := sshStorageObject("v1", "Node", "", member.Name)
		clusterSSHStorageMap(kube, "metadata")["uid"] = member.NodeUID
		kube["status"] = map[string]any{"allocatable": map[string]any{"cpu": "8"}}
		f.objects["/api/v1/nodes/"+member.Name] = kube
		name := "instance-manager-" + newClusterUUID()
		owner := sshStorageObject("longhorn.io/v1beta2", "InstanceManager", "longhorn-system", name)
		owner["spec"] = map[string]any{"nodeID": member.Name, "type": "aio", "dataEngine": "v1", "image": pin.Reference}
		owner["status"] = map[string]any{"ownerID": member.Name, "currentState": "running"}
		f.objects[clusterSSHInstanceOwnerPrefix+name] = owner
		pod := sshStorageObject("v1", "Pod", "longhorn-system", name)
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = map[string]any{"longhorn.io/component": "instance-manager", "longhorn.io/data-engine": "v1", "longhorn.io/instance-manager-type": "aio"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "longhorn.io/v1beta2", "kind": "InstanceManager", "name": name, "uid": clusterSSHStorageMap(owner, "metadata")["uid"], "controller": true, "blockOwnerDeletion": true}}
		resources := map[string]any{"requests": map[string]any{"cpu": "960m", "memory": "64Mi"}, "limits": map[string]any{"cpu": "1500m", "memory": "128Mi"}}
		pod["spec"] = map[string]any{"nodeName": member.Name, "serviceAccountName": "longhorn-service-account", "restartPolicy": "Never", "containers": []any{map[string]any{"name": "instance-manager", "image": pin.Reference, "resources": resources}}}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "instance-manager", "image": pin.Reference, "imageID": "docker.io/longhornio/longhorn-instance-manager@" + pin.ManifestDigest, "containerID": fmt.Sprintf("containerd://%064x", i+1), "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-20T00:00:00Z"}}}}}
		f.objects[clusterSSHInstancePodsPath(member.Name)] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
	}
}

func sshInstancePod(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHInstancePodsPath(f.a.Source.Members[index].Name)]["items"].([]any)[0].(map[string]any)
}
func sshInstanceOwner(f *sshStorageFixture, index int) map[string]any {
	return f.objects[clusterSSHInstanceOwnerPrefix+clusterSSHStorageText(clusterSSHStorageMap(sshInstancePod(f, index), "metadata"), "name")]
}

func TestClusterSSHInstanceObservation(t *testing.T) {
	for _, mode := range []string{
		"expansion", "replacement", "binding API default", "node override", "native rounding",
		"missing policy", "missing binding", "fail open", "wrong policy scope", "policy bypass", "changed CEL", "binding bypass",
		"missing setting", "duplicate setting key", "wrong percentage", "zero percentage", "missing node override", "negative node override", "wrong node UID", "changed allocatable", "changed override", "request above limit",
		"missing Pod", "duplicate Pod", "continued list", "remaining list", "terminating Pod", "foreign node", "foreign account", "wrong label", "v2", "not aio",
		"missing owner", "foreign owner UID", "foreign owner node", "foreign owner image", "foreign owner type", "foreign owner engine", "noncontroller owner", "owner not running",
		"missing bounds", "wrong memory", "wrong CPU", "extra resource", "extra container", "init container", "overhead", "resize", "allocated mismatch",
		"missing runtime", "wrong image", "wrong imageID", "missing containerID", "not ready", "not started", "waiting", "missing Ready", "duplicate Ready",
		"replacement missing Pod", "replacement cloned Pod UID", "replacement cloned containerID",
	} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, strings.HasPrefix(mode, "replacement"))
			node := f.a.Source.Members[0].Name
			path := clusterSSHInstancePodsPath(node)
			pod, owner := sshInstancePod(f, 0), sshInstanceOwner(f, 0)
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			container := spec["containers"].([]any)[0].(map[string]any)
			resources := clusterSSHStorageMap(container, "resources")
			runtime := status["containerStatuses"].([]any)[0].(map[string]any)
			policy := clusterSSHStorageMap(f.objects[clusterSSHInstancePolicyPath], "spec")
			switch mode {
			case "binding API default":
				clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHInstanceBindingPath], "spec"), "matchResources")["matchPolicy"] = "Equivalent"
			case "node override":
				clusterSSHStorageMap(f.objects[clusterSSHInstanceNodePrefix+node], "spec")["instanceManagerCPURequest"] = 750
				clusterSSHStorageMap(resources, "requests")["cpu"] = "750m"
			case "native rounding":
				clusterSSHStorageMap(clusterSSHStorageMap(f.objects["/api/v1/nodes/"+node], "status"), "allocatable")["cpu"] = "8005m"
				clusterSSHStorageMap(resources, "requests")["cpu"] = "961m"
			case "missing policy":
				delete(f.objects, clusterSSHInstancePolicyPath)
			case "missing binding":
				delete(f.objects, clusterSSHInstanceBindingPath)
			case "fail open":
				policy["failurePolicy"] = "Ignore"
			case "wrong policy scope":
				clusterSSHStorageMap(policy, "matchConstraints")["matchPolicy"] = "Equivalent"
			case "policy bypass":
				policy["matchConditions"] = []any{}
			case "changed CEL":
				policy["mutations"].([]any)[0].(map[string]any)["jsonPatch"] = map[string]any{"expression": "[]"}
			case "binding bypass":
				clusterSSHStorageMap(f.objects[clusterSSHInstanceBindingPath], "spec")["policyName"] = "other"
			case "missing setting":
				delete(f.objects, clusterSSHInstanceCPUPath)
			case "duplicate setting key":
				f.objects[clusterSSHInstanceCPUPath]["value"] = `{"v1":"12","v1":"12"}`
			case "wrong percentage":
				f.objects[clusterSSHInstanceCPUPath]["value"] = `{"v1":"41"}`
			case "zero percentage":
				f.objects[clusterSSHInstanceCPUPath]["value"] = `{"v1":"0"}`
			case "missing node override":
				delete(clusterSSHStorageMap(f.objects[clusterSSHInstanceNodePrefix+node], "spec"), "instanceManagerCPURequest")
			case "negative node override":
				clusterSSHStorageMap(f.objects[clusterSSHInstanceNodePrefix+node], "spec")["instanceManagerCPURequest"] = -1
			case "wrong node UID":
				clusterSSHStorageMap(f.objects["/api/v1/nodes/"+node], "metadata")["uid"] = newClusterUUID()
			case "changed allocatable":
				clusterSSHStorageMap(clusterSSHStorageMap(f.objects["/api/v1/nodes/"+node], "status"), "allocatable")["cpu"] = "4"
			case "changed override":
				clusterSSHStorageMap(f.objects[clusterSSHInstanceNodePrefix+node], "spec")["instanceManagerCPURequest"] = 500
			case "request above limit":
				clusterSSHStorageMap(f.objects[clusterSSHInstanceNodePrefix+node], "spec")["instanceManagerCPURequest"] = 1600
			case "missing Pod":
				f.objects[path]["items"] = []any{}
			case "duplicate Pod":
				f.objects[path]["items"] = []any{pod, pod}
			case "continued list":
				clusterSSHStorageMap(f.objects[path], "metadata")["continue"] = "next"
			case "remaining list":
				clusterSSHStorageMap(f.objects[path], "metadata")["remainingItemCount"] = 1
			case "terminating Pod":
				meta["deletionTimestamp"] = "2026-10-20T00:00:00Z"
			case "foreign node":
				spec["nodeName"] = "foreign"
			case "foreign account":
				spec["serviceAccountName"] = "default"
			case "wrong label":
				clusterSSHStorageMap(meta, "labels")["longhorn.io/component"] = "other"
			case "v2":
				clusterSSHStorageMap(meta, "labels")["longhorn.io/data-engine"] = "v2"
			case "not aio":
				clusterSSHStorageMap(meta, "labels")["longhorn.io/instance-manager-type"] = "engine"
			case "missing owner":
				delete(f.objects, clusterSSHInstanceOwnerPrefix+clusterSSHStorageText(meta, "name"))
			case "foreign owner UID":
				meta["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "foreign owner node":
				clusterSSHStorageMap(owner, "spec")["nodeID"] = "other"
			case "foreign owner image":
				clusterSSHStorageMap(owner, "spec")["image"] = "other"
			case "foreign owner type":
				clusterSSHStorageMap(owner, "spec")["type"] = "engine"
			case "foreign owner engine":
				clusterSSHStorageMap(owner, "spec")["dataEngine"] = "v2"
			case "noncontroller owner":
				meta["ownerReferences"].([]any)[0].(map[string]any)["controller"] = false
			case "owner not running":
				clusterSSHStorageMap(owner, "status")["currentState"] = "starting"
			case "missing bounds":
				delete(container, "resources")
			case "wrong memory":
				clusterSSHStorageMap(resources, "limits")["memory"] = "127Mi"
			case "wrong CPU":
				clusterSSHStorageMap(resources, "requests")["cpu"] = "959m"
			case "extra resource":
				clusterSSHStorageMap(resources, "requests")["example.com/resource"] = "1"
			case "extra container":
				spec["containers"] = []any{container, container}
			case "init container":
				spec["initContainers"] = []any{container}
			case "overhead":
				spec["overhead"] = map[string]any{"cpu": "1m"}
			case "resize":
				status["resize"] = "InProgress"
			case "allocated mismatch":
				runtime["allocatedResources"] = map[string]any{"cpu": "961m", "memory": "64Mi"}
			case "missing runtime":
				delete(status, "containerStatuses")
			case "wrong image":
				container["image"] = "other"
			case "wrong imageID":
				runtime["imageID"] = "docker.io/longhornio/longhorn-instance-manager:latest"
			case "missing containerID":
				delete(runtime, "containerID")
			case "not ready":
				runtime["ready"] = false
			case "not started":
				runtime["started"] = false
			case "waiting":
				runtime["state"] = map[string]any{"waiting": map[string]any{}}
			case "missing Ready":
				status["conditions"] = []any{}
			case "duplicate Ready":
				status["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Ready", "status": "True"}}
			case "replacement missing Pod":
				delete(f.objects, clusterSSHInstancePodsPath(f.a.Source.Members[1].Name))
			case "replacement cloned Pod UID":
				clusterSSHStorageMap(sshInstancePod(f, 1), "metadata")["uid"] = meta["uid"]
			case "replacement cloned containerID":
				clusterSSHStorageMap(sshInstancePod(f, 1), "status")["containerStatuses"].([]any)[0].(map[string]any)["containerID"] = runtime["containerID"]
			}
			_, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := textInSet(mode, "expansion", "replacement", "binding API default", "node override", "native rounding")
			if valid && err != nil {
				t.Fatalf("valid source rejected: %v", err)
			}
			if !valid && !errors.Is(err, clusterbootstrap.ErrPreparationConfig) {
				t.Fatalf("unsupported source accepted: %v", err)
			}
		})
	}
}

func TestClusterSSHInstanceReceipts(t *testing.T) {
	for _, part := range []string{"policy", "binding", "setting", "owner", "Pod", "Longhorn node", "Kubernetes node"} {
		t.Run(part, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			node := f.a.Source.Members[0].Name
			object := map[string]map[string]any{"policy": f.objects[clusterSSHInstancePolicyPath], "binding": f.objects[clusterSSHInstanceBindingPath], "setting": f.objects[clusterSSHInstanceCPUPath], "owner": sshInstanceOwner(f, 0), "Pod": sshInstancePod(f, 0), "Longhorn node": f.objects[clusterSSHInstanceNodePrefix+node], "Kubernetes node": f.objects["/api/v1/nodes/"+node]}[part]
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				clusterSSHStorageMap(object, "metadata")["annotations"] = map[string]any{"changed": "complete private receipt"}
				return nil
			})
			if !consumed || !errors.Is(err, clusterbootstrap.ErrSessionAuthority) || f.calls[clusterSSHInstanceCPUPath] != 3 {
				t.Fatalf("drifting %s receipt escaped: consumed=%t err=%v", part, consumed, err)
			}
		})
	}
}

func TestClusterSSHInstancePolicyProducerParity(t *testing.T) {
	// Run canonical producer, not a second hand-maintained CEL implementation.
	script := filepath.Join("..", "..", "..", "..", "K3s", "cluster", "render-longhorn-instance-manager.py")
	for _, bounds := range [][3]string{{"64Mi", "1500m", "128Mi"}, {"683Mi", "937m", "1367Mi"}} {
		raw := fmt.Sprintf(`{"requests":{"memory":%q},"limits":{"cpu":%q,"memory":%q}}`, bounds[0], bounds[1], bounds[2])
		config := filepath.Join(t.TempDir(), "bounds.json")
		if err := os.WriteFile(config, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("python3", script, "policy", config)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("canonical producer failed: %v %s", err, output)
		}
		var bundle struct {
			Items []map[string]any `json:"items"`
		}
		if json.Unmarshal(output, &bundle) != nil {
			t.Fatal("invalid producer output")
		}
		expected, ok := clusterSSHInstanceExpectedPolicy(bounds[0], bounds[1], bounds[2])
		if !ok || !reflect.DeepEqual(bundle.Items, expected) {
			t.Fatal("embedded policy differs from canonical producer; regenerate template")
		}
	}
}
