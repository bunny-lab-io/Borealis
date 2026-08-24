package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClusterUpdateOrdersNonLeadersBeforeLeaders(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{"postgres_primary": true, "edge_vip_owner": true}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{"scheduler_leader": true}},
	}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("a", 40), Payload: map[string]any{"scope": "all"}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatalf("build steps: %v", err)
	}
	firstDrain := ""
	lastDrain := ""
	for _, step := range steps {
		if strings.HasSuffix(step.Name, ":enter_drain") {
			if firstDrain == "" {
				firstDrain = step.NodeID
			}
			lastDrain = step.NodeID
		}
	}
	if firstDrain != nodes[1].ID || lastDrain != nodes[0].ID {
		t.Fatalf("unsafe update order first=%s last=%s", firstDrain, lastDrain)
	}
}

func TestClusterUpdateStateMachineRestoresEveryNodeBeforeFinalize(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("b", 40), Payload: map[string]any{"scope": "all"}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatalf("build steps: %v", err)
	}
	drained := map[string]bool{}
	restored := map[string]bool{}
	for _, step := range steps {
		if strings.HasSuffix(step.Name, ":enter_drain") {
			if len(drained) != len(restored) {
				t.Fatalf("more than one node drained at step %s", step.Name)
			}
			drained[step.NodeID] = true
		}
		if strings.HasSuffix(step.Name, ":exit_drain") {
			restored[step.NodeID] = true
		}
	}
	if len(drained) != 3 || len(restored) != 3 {
		t.Fatalf("state machine omitted nodes drained=%v restored=%v", drained, restored)
	}
}

func TestClusterActionJobUsesPinnedNodeAndRestrictedContainer(t *testing.T) {
	manifest := clusterActionJobManifest("cluster-action", "borealis", "engine-2", "registry.example/borealis@sha256:"+strings.Repeat("a", 64), []string{"client", "--verb", "InspectHealth"}, "operation", "inspect")
	spec := nestedMap(nestedMap(manifest, "spec"), "template")
	podSpec := nestedMap(spec, "spec")
	if cleanText(podSpec["nodeName"]) != "engine-2" || podSpec["automountServiceAccountToken"] != false {
		t.Fatalf("job not pinned/restricted: %#v", podSpec)
	}
	containers := anySlice(podSpec["containers"])
	security := nestedMap(containers[0].(map[string]any), "securityContext")
	if security["allowPrivilegeEscalation"] != false || security["readOnlyRootFilesystem"] != true {
		t.Fatalf("unsafe action security context: %#v", security)
	}
}

func TestHMRRequiresActiveTarget(t *testing.T) {
	_, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_start", TargetNodeID: "11111111-1111-4111-8111-111111111111"}, nil)
	if err == nil {
		t.Fatal("expected inactive HMR target rejection")
	}
}

func TestHMRNodeHealthRequiresReadyNodeScopedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes/engine-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}})
		case "/apis/discovery.k8s.io/v1/namespaces/borealis/endpointslices":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"endpoints": []any{map[string]any{"nodeName": "engine-1", "conditions": map[string]any{"ready": true}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	ready, err := runner.nodeReady(context.Background(), "engine-1")
	if err != nil || !ready {
		t.Fatalf("expected ready node, ready=%v err=%v", ready, err)
	}
	if err := runner.verifyReadyEndpointsForNode(context.Background(), "engine-1"); err != nil {
		t.Fatalf("expected node-scoped ready endpoint: %v", err)
	}
	if err := runner.verifyReadyEndpointsForNode(context.Background(), "engine-2"); err == nil {
		t.Fatal("expected node-scoped endpoint rejection for lost HMR node")
	}
}
