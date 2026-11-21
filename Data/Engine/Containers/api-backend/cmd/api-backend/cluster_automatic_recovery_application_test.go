package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func automaticRecoveryApplicationFixture() (clusterAutomaticRecoveryApplicationBinding, map[string]map[string]any) {
	n, namespace, node := automaticRecoveryNativeFixture()
	b := clusterAutomaticRecoveryApplicationBinding{Node: n, NamespaceUID: newClusterUUID(), ServiceUID: newClusterUUID(), EndpointSlice: "api-backend-original", EndpointSliceUID: newClusterUUID(), PodIP: "10.42.0.25", ServiceIP: "10.43.0.20", Port: 5000}
	object := func(version, kind, name, uid, ns string) map[string]any {
		return map[string]any{"apiVersion": version, "kind": kind, "metadata": map[string]any{"name": name, "uid": uid, "namespace": ns, "resourceVersion": "1"}}
	}
	objects := map[string]map[string]any{"/api/v1/namespaces/kube-system": namespace, "/api/v1/nodes/" + n.NodeName: node,
		"/api/v1/namespaces/borealis": object("v1", "Namespace", "borealis", b.NamespaceUID, "")}
	for i, name := range []string{"api-backend", "job-scheduler"} {
		w := clusterAutomaticRecoveryWorkloadBinding{Deployment: name + "-" + n.NodeName, DeploymentUID: newClusterUUID(), ReplicaSet: name + "-original-rs", ReplicaSetUID: newClusterUUID(), Pod: name + "-original-pod", PodUID: newClusterUUID(), Image: "registry.example/" + name + "@sha256:" + strings.Repeat("a", 64), ImageID: "registry.example/" + name + "@sha256:" + strings.Repeat("b", 64), ContainerID: fmt.Sprintf("containerd://%064x", i+1)}
		labels := func() map[string]any {
			return map[string]any{"app.kubernetes.io/name": name, "borealis.io/engine-node": n.NodeName, "borealis.io/node-workload": "true", "borealis.io/update-candidate": "false", "borealis.io/traffic-state": "active"}
		}
		container := func() []any {
			return []any{map[string]any{"name": name, "image": w.Image, "ports": []any{map[string]any{"name": "http", "containerPort": b.Port, "protocol": "TCP"}}}}
		}
		for _, kind := range []string{"Deployment", "ReplicaSet"} {
			resourceName, uid, collection := w.Deployment, w.DeploymentUID, "deployments"
			if kind == "ReplicaSet" {
				resourceName, uid, collection = w.ReplicaSet, w.ReplicaSetUID, "replicasets"
			}
			x := object("apps/v1", kind, resourceName, uid, "borealis")
			meta := clusterSSHStorageMap(x, "metadata")
			meta["labels"], meta["generation"] = labels(), 1
			x["spec"] = map[string]any{"replicas": 1, "selector": map[string]any{"matchLabels": labels()}, "template": map[string]any{"metadata": map[string]any{"labels": labels()}, "spec": map[string]any{"containers": container()}}}
			x["status"] = map[string]any{"observedGeneration": 1, "readyReplicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
			if kind == "ReplicaSet" {
				meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": w.Deployment, "uid": w.DeploymentUID, "controller": true}}
			}
			objects["/apis/apps/v1/namespaces/borealis/"+collection+"/"+resourceName] = x
		}
		pod := object("v1", "Pod", w.Pod, w.PodUID, "borealis")
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["labels"] = labels()
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": w.ReplicaSet, "uid": w.ReplicaSetUID, "controller": true}}
		pod["spec"] = map[string]any{"nodeName": n.NodeName, "containers": container()}
		pod["status"] = map[string]any{"phase": "Running", "podIP": b.PodIP, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": name, "image": w.Image, "imageID": w.ImageID, "containerID": w.ContainerID, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-01T00:00:00Z"}}}}}
		objects["/api/v1/namespaces/borealis/pods/"+w.Pod] = pod
		if i == 0 {
			b.API = w
		} else {
			b.Scheduler = w
		}
	}
	service := object("v1", "Service", "api-backend", b.ServiceUID, "borealis")
	service["spec"] = map[string]any{"clusterIP": b.ServiceIP, "selector": map[string]any{"app.kubernetes.io/name": "api-backend", "borealis.io/traffic-state": "active"}, "ports": []any{map[string]any{"name": "http", "port": b.Port, "targetPort": "http", "protocol": "TCP"}}}
	objects["/api/v1/namespaces/borealis/services/api-backend"] = service
	slice := object("discovery.k8s.io/v1", "EndpointSlice", b.EndpointSlice, b.EndpointSliceUID, "borealis")
	meta := clusterSSHStorageMap(slice, "metadata")
	meta["labels"] = map[string]any{"kubernetes.io/service-name": "api-backend"}
	meta["ownerReferences"] = []any{map[string]any{"apiVersion": "v1", "kind": "Service", "name": "api-backend", "uid": b.ServiceUID, "controller": true}}
	slice["addressType"], slice["ports"] = "IPv4", []any{map[string]any{"name": "http", "port": b.Port, "protocol": "TCP"}}
	slice["endpoints"] = []any{map[string]any{"addresses": []any{b.PodIP}, "nodeName": n.NodeName, "conditions": map[string]any{"ready": true, "serving": true, "terminating": false}, "targetRef": map[string]any{"kind": "Pod", "namespace": "borealis", "name": b.API.Pod, "uid": b.API.PodUID}}}
	objects["/apis/discovery.k8s.io/v1/namespaces/borealis/endpointslices/"+b.EndpointSlice] = slice
	return b, objects
}

func TestClusterAutomaticRecoveryApplicationNative(t *testing.T) {
	for _, mode := range []string{"healthy application", "missing Pod", "Pod UID", "namespace UID", "Service UID", "slice UID", "node UID", "scheduler unready", "stale generation", "API unready", "wrong owner", "wrong node", "wrong image", "runtime restarted", "candidate", "service selector", "service IP", "service target port", "slice port", "container port", "endpoint Pod UID", "endpoint node", "endpoint address", "endpoint ready absent", "endpoint serving", "endpoint terminating", "duplicate endpoint", "Pod drift during probe", "Service drift during probe", "slice drift during probe", "scheduler drift during probe", "node drift during probe", "duplicate JSON", "alias JSON", "aliased deletion", "trailing JSON", "HTTP failure", "redirect", "cancelled before", "cancelled during", "probe failure"} {
		t.Run(mode, func(t *testing.T) {
			b, objects := automaticRecoveryApplicationFixture()
			pod := objects["/api/v1/namespaces/borealis/pods/"+b.API.Pod]
			scheduler := objects["/apis/apps/v1/namespaces/borealis/deployments/"+b.Scheduler.Deployment]
			service := objects["/api/v1/namespaces/borealis/services/api-backend"]
			slice := objects["/apis/discovery.k8s.io/v1/namespaces/borealis/endpointslices/"+b.EndpointSlice]
			endpoint := slice["endpoints"].([]any)[0].(map[string]any)
			switch mode {
			case "missing Pod":
				delete(objects, "/api/v1/namespaces/borealis/pods/"+b.API.Pod)
			case "Pod UID":
				clusterSSHStorageMap(pod, "metadata")["uid"] = newClusterUUID()
			case "namespace UID":
				clusterSSHStorageMap(objects["/api/v1/namespaces/borealis"], "metadata")["uid"] = newClusterUUID()
			case "Service UID":
				clusterSSHStorageMap(service, "metadata")["uid"] = newClusterUUID()
			case "slice UID":
				clusterSSHStorageMap(slice, "metadata")["uid"] = newClusterUUID()
			case "node UID":
				clusterSSHStorageMap(objects["/api/v1/nodes/"+b.Node.NodeName], "metadata")["uid"] = newClusterUUID()
			case "scheduler unready":
				clusterSSHStorageMap(scheduler, "status")["readyReplicas"] = 0
			case "stale generation":
				clusterSSHStorageMap(scheduler, "metadata")["generation"] = 2
			case "API unready":
				clusterSSHStorageMap(pod, "status")["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
			case "wrong owner":
				clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "wrong node":
				clusterSSHStorageMap(pod, "spec")["nodeName"] = "other-node"
			case "wrong image":
				clusterSSHStorageMap(pod, "spec")["containers"].([]any)[0].(map[string]any)["image"] = "changed"
			case "runtime restarted":
				clusterSSHStorageMap(pod, "status")["containerStatuses"].([]any)[0].(map[string]any)["containerID"] = "containerd://" + strings.Repeat("f", 64)
			case "candidate":
				clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["borealis.io/traffic-state"] = "candidate"
			case "service selector":
				clusterSSHStorageMap(clusterSSHStorageMap(service, "spec"), "selector")["borealis.io/traffic-state"] = "candidate"
			case "service IP":
				clusterSSHStorageMap(service, "spec")["clusterIP"] = "10.43.0.99"
			case "service target port":
				clusterSSHStorageMap(service, "spec")["ports"].([]any)[0].(map[string]any)["targetPort"] = "other"
			case "slice port":
				slice["ports"].([]any)[0].(map[string]any)["name"] = "other"
			case "container port":
				clusterSSHStorageMap(pod, "spec")["containers"].([]any)[0].(map[string]any)["ports"].([]any)[0].(map[string]any)["containerPort"] = 5001
			case "endpoint Pod UID":
				clusterSSHStorageMap(endpoint, "targetRef")["uid"] = newClusterUUID()
			case "endpoint node":
				endpoint["nodeName"] = "other-node"
			case "endpoint address":
				endpoint["addresses"] = []any{"10.42.0.99"}
			case "endpoint ready absent":
				delete(clusterSSHStorageMap(endpoint, "conditions"), "ready")
			case "endpoint serving":
				clusterSSHStorageMap(endpoint, "conditions")["serving"] = false
			case "endpoint terminating":
				clusterSSHStorageMap(endpoint, "conditions")["terminating"] = true
			case "duplicate endpoint":
				slice["endpoints"] = []any{endpoint, endpoint}
			case "aliased deletion":
				clusterSSHStorageMap(pod, "metadata")["DeletionTimestamp"] = "now"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled before" {
				cancel()
			}
			var gets atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gets.Add(1)
				if r.Method != http.MethodGet || r.URL.RawQuery != "" {
					t.Error("unexpected request")
				}
				if mode == "HTTP failure" {
					w.WriteHeader(503)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "/redirected", 307)
					return
				}
				object, ok := objects[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				raw, _ := json.Marshal(object)
				wire := string(raw)
				if r.URL.Path == "/api/v1/namespaces/borealis/pods/"+b.API.Pod {
					if mode == "duplicate JSON" {
						wire = strings.Replace(wire, `"uid":`, `"uid":"duplicate","uid":`, 1)
					}
					if mode == "alias JSON" {
						wire = strings.Replace(wire, `"hostNetwork":`, `"HostNetwork":`, 1)
						wire = strings.Replace(wire, `"spec":{`, `"spec":{"HostNetwork":true,`, 1)
					}
					if mode == "trailing JSON" {
						wire += `{}`
					}
				}
				_, _ = w.Write([]byte(wire))
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
			get := func(ctx context.Context, path string, out any) error {
				return kube.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
			}
			probes := 0
			probe := func(ctx context.Context, address string, port int, path string) error {
				probes++
				if port != b.Port || probes < 4 && address != b.PodIP || probes == 4 && (address != b.ServiceIP || path != "/ready") {
					t.Error("probe escaped bound endpoint")
				}
				if mode == "probe failure" {
					return fmt.Errorf("private endpoint failure")
				}
				if mode == "cancelled during" {
					cancel()
				}
				if probes == 1 {
					var changed map[string]any
					switch mode {
					case "Pod drift during probe":
						changed = pod
					case "Service drift during probe":
						changed = service
					case "slice drift during probe":
						changed = slice
					case "scheduler drift during probe":
						changed = scheduler
					case "node drift during probe":
						changed = objects["/api/v1/nodes/"+b.Node.NodeName]
					}
					if changed != nil {
						clusterSSHStorageMap(changed, "metadata")["uid"] = newClusterUUID()
					}
				}
				return nil
			}
			health, err := readClusterAutomaticRecoveryApplication(ctx, get, probe, b)
			if mode != "healthy application" {
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatal("unknown became health", health, err)
				}
				return
			}
			want := clusterAutomaticRecoveryHealth{NodeName: b.Node.NodeName, NodeUID: b.Node.NodeUID, Node: true, Application: true}
			if err != nil || health != want || health.healthy() || probes != 4 || gets.Load() != 26 {
				t.Fatal("application proof changed scope", health, err, probes, gets.Load())
			}
			r, full := automaticRecoveryFixture()
			r.NodeName, r.NodeUID, full.NodeName, full.NodeUID = b.Node.NodeName, b.Node.NodeUID, b.Node.NodeName, b.Node.NodeUID
			r, _ = advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1001, full, nil)
			next, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1002, health, nil)
			if err != nil || next.HealthySince != 0 || next.RecoveryDeadline != r.RecoveryDeadline {
				t.Fatal("partial application proof retained continuity", next, err)
			}
		})
	}
}

type recoveryApplicationCleanupBody struct {
	io.Reader
	close func() error
}

func (b recoveryApplicationCleanupBody) Close() error { return b.close() }

func TestClusterAutomaticRecoveryApplicationHTTPCleanup(t *testing.T) {
	for _, mode := range []string{"healthy", "close failure", "cancel during close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			var closes, calls atomic.Int32
			body := recoveryApplicationCleanupBody{Reader: strings.NewReader(`{"ok":true,"phase":"ready"}`), close: func() error {
				closes.Add(1)
				if mode == "close failure" {
					return fmt.Errorf("private cleanup diagnostic")
				}
				if mode == "cancel during close" {
					close(entered)
					<-release
				}
				return nil
			}}
			client := &http.Client{Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.String() != "http://10.42.0.25:5000/ready" || r.Header.Get("Authorization") != "" {
					t.Error("probe changed target or credentials")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: http.Header{}}, nil
			})}
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				result <- recoveryApplicationHTTP(ctx, client, "10.42.0.25", 5000, "/ready")
			}()
			defer func() {
				unblock.Do(func() { close(release) })
				<-done
			}()
			if mode == "cancel during close" {
				select {
				case <-entered:
				case <-done:
					t.Fatal("observation returned before close")
				case <-time.After(time.Second):
					t.Fatal("close not reached")
				}
				cancel()
				select {
				case <-done:
					t.Fatal("canceled observation escaped unfinished close")
				case <-time.After(25 * time.Millisecond):
				}
				unblock.Do(func() { close(release) })
			}
			err := <-result
			if mode == "healthy" && err != nil || mode != "healthy" && err != errClusterConflict {
				t.Fatalf("cleanup outcome escaped health boundary: %v", err)
			}
			if closes.Load() != 1 || calls.Load() != 1 {
				t.Fatal("response not closed exactly once or request replayed")
			}
		})
	}
}

func TestClusterAutomaticRecoveryApplicationHTTP(t *testing.T) {
	for _, mode := range []string{"healthy", "status", "redirect", "wrong phase", "false", "duplicate", "alias", "trailing", "oversize", "cancel", "private error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), clusterControllerLeaseAcquireTimeout)
			defer cancel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.Host != "10.42.0.25:5000" || r.URL.Path != "/ready" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("probe leaked identity or changed target")
				}
				wire := `{"ok":true,"phase":"ready"}`
				switch mode {
				case "status":
					w.WriteHeader(503)
				case "redirect":
					http.Redirect(w, r, "/private", 307)
					return
				case "wrong phase":
					wire = `{"ok":true,"phase":"live"}`
				case "false":
					wire = `{"ok":false,"phase":"ready"}`
				case "duplicate":
					wire = `{"ok":false,"ok":true,"phase":"ready"}`
				case "alias":
					wire = `{"OK":true,"phase":"ready"}`
				case "trailing":
					wire += `{}`
				case "oversize":
					wire += strings.Repeat(" ", 1025)
				case "cancel":
					cancel()
					<-r.Context().Done()
					return
				case "private error":
					wire = `private credential material`
				}
				_, _ = w.Write([]byte(wire))
			}))
			defer server.Close()
			client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "10.42.0.25:5000" {
					t.Error("unbound dial", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			err := recoveryApplicationHTTP(ctx, client, "10.42.0.25", 5000, "/ready")
			if mode == "healthy" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err != errClusterConflict {
				t.Fatal("probe failure lost static boundary", err)
			}
			if calls.Load() != 1 {
				t.Fatal("redirect/retry escaped scope", calls.Load())
			}
			for _, bad := range []struct {
				address, path string
				port          int
			}{{"127.0.0.1", "/ready", 5000}, {"10.42.0.25", "/api/private", 5000}, {"10.42.0.25", "/ready", 0}, {"10.42.0.25", "/ready?token=x", 5000}} {
				if recoveryApplicationHTTP(ctx, client, bad.address, bad.port, bad.path) == nil {
					t.Fatal("invalid probe target accepted")
				}
			}
		})
	}
	var c *clusterAutomaticRecoveryCoordinator
	b, _ := automaticRecoveryApplicationFixture()
	if _, err := c.applicationHealthObserver(b)(context.Background(), clusterControllerNode{}); err == nil {
		t.Fatal("absent coordinator accepted")
	}
	want := b
	b.API.PodUID = b.Scheduler.PodUID
	if b.valid() || reflect.DeepEqual(b, want) {
		t.Fatal("duplicate original identity accepted")
	}
}
