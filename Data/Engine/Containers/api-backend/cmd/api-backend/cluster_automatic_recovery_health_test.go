package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func automaticRecoveryNativeFixture() (clusterAutomaticRecoveryNodeBinding, map[string]any, map[string]any) {
	r, _ := automaticRecoveryFixture()
	b := clusterAutomaticRecoveryNodeBinding{r.ClusterID, newClusterUUID(), r.NodeID, r.NodeName, r.NodeUID, r.Controller}
	namespace := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{
		"name": "kube-system", "uid": b.KubeSystemUID, "resourceVersion": "1"}}
	node := map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{
		"name": b.NodeName, "uid": b.NodeUID, "resourceVersion": "2"},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}
	return b, namespace, node
}

func TestClusterAutomaticRecoveryNativeNodeObservation(t *testing.T) {
	for _, mode := range []string{"ready", "wrong node", "wrong uid", "replaced node", "deleting node", "aliased node deletion", "aliased node namespace", "nonready", "unknown readiness", "missing readiness", "duplicate readiness", "malformed condition", "duplicate uid", "aliased uid", "duplicate status", "wrong kind", "missing revision", "wrong namespace", "namespace drift", "deleting namespace", "aliased namespace deletion", "aliased namespace scope", "duplicate namespace uid", "trailing object", "trailing junk", "oversize", "redirect", "non200", "http failure", "cancelled before", "cancelled during", "expired read"} {
		t.Run(mode, func(t *testing.T) {
			b, namespace, node := automaticRecoveryNativeFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled before" {
				cancel()
			}
			if mode == "expired read" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 10*time.Millisecond)
				defer stop()
			}
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				call := calls.Add(1)
				if request.Method != http.MethodGet || request.URL.RawQuery != "" {
					t.Error("health reader attempted non-GET or cached-version request")
				}
				if mode == "http failure" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, request, "/redirected", http.StatusTemporaryRedirect)
					return
				}
				if mode == "non200" {
					w.WriteHeader(http.StatusAccepted)
				}
				if mode == "cancelled during" {
					cancel()
				}
				if mode == "expired read" {
					<-request.Context().Done()
					return
				}
				object := namespace
				isNode := request.URL.Path == "/api/v1/nodes/"+b.NodeName
				if isNode {
					object = node
					meta, status := clusterSSHStorageMap(node, "metadata"), clusterSSHStorageMap(node, "status")
					switch mode {
					case "wrong node":
						meta["name"] = "other-node"
					case "wrong uid":
						meta["uid"] = newClusterUUID()
					case "replaced node":
						if call == 3 {
							meta["uid"] = newClusterUUID()
						}
					case "deleting node":
						meta["deletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "aliased node deletion":
						meta["DeletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "aliased node namespace":
						meta["Namespace"] = "other"
					case "nonready", "unknown readiness":
						state := "False"
						if mode == "unknown readiness" {
							state = "Unknown"
						}
						status["conditions"] = []any{map[string]any{"type": "Ready", "status": state}}
					case "missing readiness":
						delete(status, "conditions")
					case "duplicate readiness":
						status["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Ready", "status": "False"}}
					case "malformed condition":
						status["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, nil}
					case "wrong kind":
						node["kind"] = "NodeList"
					case "missing revision":
						delete(meta, "resourceVersion")
					}
				} else if request.URL.Path != "/api/v1/namespaces/kube-system" {
					t.Error("unexpected native read", request.URL.Path)
				}
				if !isNode {
					meta := clusterSSHStorageMap(namespace, "metadata")
					switch mode {
					case "wrong namespace":
						meta["uid"] = newClusterUUID()
					case "namespace drift":
						if call == 4 {
							meta["uid"] = newClusterUUID()
						}
					case "deleting namespace":
						meta["deletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "aliased namespace deletion":
						meta["DeletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "aliased namespace scope":
						meta["Namespace"] = "other"
					}
				}
				raw, err := json.Marshal(object)
				if err != nil {
					t.Error(err)
				}
				wire := string(raw)
				if isNode && mode == "duplicate uid" || !isNode && mode == "duplicate namespace uid" {
					wire = strings.Replace(wire, `"uid":`, `"uid":"`+newClusterUUID()+`","uid":`, 1)
				}
				if isNode && mode == "aliased uid" {
					wire = strings.Replace(wire, `"uid":`, `"UID":"`+newClusterUUID()+`","uid":`, 1)
				}
				if isNode && mode == "duplicate status" {
					wire = strings.Replace(wire, `"status":"True"`, `"status":"False","status":"True"`, 1)
				}
				if isNode {
					switch mode {
					case "trailing object":
						wire += `{}`
					case "trailing junk":
						wire += ` invalid`
					case "oversize":
						wire += strings.Repeat(" ", 2<<20)
					}
				}
				_, _ = w.Write([]byte(wire))
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
			health, err := readClusterAutomaticRecoveryNodeHealth(ctx, kube.getClusterAutomaticRecoveryJSON, b)
			if mode != "ready" {
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatalf("uncertain native evidence became health: %+v %v", health, err)
				}
				if mode == "cancelled before" && calls.Load() != 0 {
					t.Fatal("cancelled reader reached Kubernetes")
				}
				if mode == "redirect" && calls.Load() != 1 {
					t.Fatal("native reader followed redirect")
				}
				return
			}
			if err != nil || calls.Load() != 4 || health != (clusterAutomaticRecoveryHealth{NodeName: b.NodeName, NodeUID: b.NodeUID, Node: true}) || health.healthy() {
				t.Fatalf("partial native result changed scope: %+v %v", health, err)
			}
			r, full := automaticRecoveryFixture()
			r.NodeName, r.NodeUID, full.NodeName, full.NodeUID = b.NodeName, b.NodeUID, b.NodeName, b.NodeUID
			r, _ = advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1001, full, nil)
			next, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1002, health, nil)
			if err != nil || next.Phase != "recovering" || next.HealthySince != 0 || next.RecoveryDeadline != r.RecoveryDeadline {
				t.Fatal("Node-only proof retained healthy continuity", next, err)
			}
		})
	}
}

func TestClusterAutomaticRecoveryNativeBindingAndDecoder(t *testing.T) {
	b, _, node := automaticRecoveryNativeFixture()
	for _, mode := range []string{"logical cluster", "native cluster", "node id", "node uid", "node name", "holder"} {
		changed := b
		switch mode {
		case "logical cluster":
			changed.ClusterID = ""
		case "native cluster":
			changed.KubeSystemUID = "00000000-0000-0000-0000-000000000000"
		case "node id":
			changed.NodeID = "bad"
		case "node uid":
			changed.NodeUID = ""
		case "node name":
			changed.NodeName = "../other"
		case "holder":
			changed.Controller = " "
		}
		if changed.valid() {
			t.Fatal("invalid original binding accepted", mode)
		}
	}
	var c *clusterAutomaticRecoveryCoordinator
	if _, err := c.nodeHealthObserver(b)(context.Background(), clusterControllerNode{}); err == nil {
		t.Fatal("missing native authority accepted")
	}
	raw, _ := json.Marshal(node)
	for _, wire := range []string{string(raw) + `{}`, `null`, `[]`, `{"metadata":{"uid":"first","uid":"last"}}`, `{"metadata":{"uid":"first","UID":"last"}}`} {
		var object clusterAutomaticRecoveryNativeObject
		if object.UnmarshalJSON([]byte(wire)) == nil {
			t.Fatal("ambiguous native object accepted")
		}
	}
	var object clusterAutomaticRecoveryNativeObject
	if object.UnmarshalJSON(raw) != nil || !reflect.DeepEqual(map[string]any(object), node) {
		t.Fatal("native decoder changed valid fields")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid native read reached transport")
	}))
	defer server.Close()
	kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
	for _, path := range []string{"/api/v1/nodes", "/api/v1/nodes/../secrets", "/api/v1/nodes/engine-2?resourceVersion=0", "/api/v1/namespaces/borealis/secrets", "https://other/api/v1/nodes/engine-2"} {
		if kube.getClusterAutomaticRecoveryJSON(context.Background(), path, &object) == nil {
			t.Fatal("unexpected read path accepted", path)
		}
	}
	kube.baseURL = strings.Replace(server.URL, "https:", "http:", 1)
	if kube.getClusterAutomaticRecoveryJSON(context.Background(), "/api/v1/nodes/engine-2", &object) == nil {
		t.Fatal("plaintext native health accepted")
	}
}
