package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClusterControllerStepTimeoutCoversLongNodeActions(t *testing.T) {
	tests := []struct {
		step string
		want time.Duration
	}{
		{step: "apply_cluster_foundation", want: 95 * time.Minute},
		{step: "node:id:redeploy_revision", want: 65 * time.Minute},
		{step: "node:id:promote_candidate", want: 65 * time.Minute},
		{step: "node:id:fetch_release", want: 35 * time.Minute},
	}
	for _, test := range tests {
		if got := clusterControllerStepTimeout(test.step); got != test.want {
			t.Fatalf("clusterControllerStepTimeout(%q)=%s want %s", test.step, got, test.want)
		}
	}
}

func TestWaitJobToleratesTemporaryKubernetesAPIOutage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 2 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"succeeded": 1}})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.waitJob(ctx, "cluster-test"); err != nil {
		t.Fatalf("temporary API outage failed Job wait: %v", err)
	}
	if requests != 3 {
		t.Fatalf("unexpected Job poll count %d", requests)
	}
}

func TestWaitJobRejectsPermanentKubernetesAPIError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
	}
	if err := runner.waitJob(context.Background(), "cluster-test"); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected permanent API error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("permanent API error retried %d times", requests)
	}
	certificateError := &url.Error{Op: http.MethodGet, URL: server.URL, Err: errors.New("x509: certificate signed by unknown authority")}
	if transientKubernetesAPIError(certificateError) {
		t.Fatal("permanent TLS trust error classified as transient")
	}
}

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

func TestClusterUpdateVerifiesIsolatedCandidateBeforePromotion(t *testing.T) {
	node := clusterControllerNode{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("c", 40), Payload: map[string]any{"scope": "all"}}
	steps, err := clusterOperationSteps(operation, []clusterControllerNode{node})
	if err != nil {
		t.Fatalf("build steps: %v", err)
	}
	positions := map[string]int{}
	for index, step := range steps {
		positions[strings.TrimPrefix(step.Name, "node:"+node.ID+":")] = index
	}
	ordered := []string{"redeploy_revision", "inspect_candidate_health", "minimum_candidate_soak", "promote_candidate", "inspect_health", "minimum_ready_soak", "exit_drain"}
	for index := 1; index < len(ordered); index++ {
		if positions[ordered[index-1]] >= positions[ordered[index]] {
			t.Fatalf("unsafe candidate order: %#v", positions)
		}
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

func TestSafeRemovalFencesAndDeletesPairSequentially(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", ApplicationState: "active", Roles: map[string]any{}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", ApplicationState: "active", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", ApplicationState: "active", Roles: map[string]any{"etcd_leader": true}},
	}
	operation := clusterControllerOperation{Kind: "node_remove", TargetNodeID: nodes[0].ID, Payload: map[string]any{
		"emergency": false, "removal_node_ids": []any{nodes[0].ID, nodes[1].ID}, "target_size": int64(1),
	}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	previousVerify := -1
	removed := 0
	for index, step := range steps {
		if strings.HasSuffix(step.Name, ":prepare_member_removal") {
			removed++
			if index+3 >= len(steps) || !strings.HasSuffix(steps[index+1].Name, ":wait_member_fenced") || !strings.HasSuffix(steps[index+2].Name, ":delete_member_node") || !strings.HasSuffix(steps[index+3].Name, ":verify_member_removed") {
				t.Fatalf("unsafe member removal order near %s: %#v", step.Name, steps)
			}
			if index <= previousVerify {
				t.Fatalf("second member began before first verification: %#v", steps)
			}
			previousVerify = index + 3
		}
	}
	if removed != 2 || steps[1].Name != "pre_change_snapshot" || steps[2].Name != "prepare_postgres_removal" {
		t.Fatalf("safe paired removal sequence incomplete: %#v", steps)
	}
}

func TestEmergencyRemovalNeverContactsUnreachableTarget(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Kind: "node_remove", TargetNodeID: nodes[0].ID, Payload: map[string]any{
		"emergency": true, "removal_node_ids": []any{nodes[0].ID}, "target_size": int64(2), "fencing_confirmation": "TARGET IS POWERED OFF",
	}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, step := range steps {
		joined += step.Name + "\n"
	}
	if strings.Contains(joined, "enter_drain") || strings.Contains(joined, "prepare_member_removal") || !strings.Contains(joined, "delete_member_node") || !strings.Contains(joined, "scale_postgres_membership") {
		t.Fatalf("unsafe emergency sequence: %s", joined)
	}
}

func TestK3sUpdateOrdersOneServerThroughConformanceBeforeNext(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{"etcd_leader": true}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Kind: "k3s_update", TargetRelease: "v1.36.4+k3s1", Payload: map[string]any{"scope": "all", "source_k3s_version": "v1.36.3+k3s1", "upgrade_image": "registry.example/k3s-upgrade@sha256:" + strings.Repeat("a", 64)}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	inFlight := false
	completed := 0
	for _, step := range steps {
		switch {
		case strings.HasSuffix(step.Name, ":apply_k3s_upgrade"):
			if inFlight {
				t.Fatalf("second K3s server started before prior restore: %#v", steps)
			}
			inFlight = true
		case strings.HasSuffix(step.Name, ":exit_drain"):
			if !inFlight {
				t.Fatalf("K3s server restored without upgrade: %#v", steps)
			}
			inFlight = false
			completed++
		}
	}
	if inFlight || completed != 3 {
		t.Fatalf("K3s sequence incomplete: %#v", steps)
	}
	joined := ""
	for _, step := range steps {
		joined += step.Name + "\n"
	}
	if !strings.Contains(joined, "post_k3s_conformance") || !strings.Contains(joined, "pre_change_snapshot") {
		t.Fatalf("K3s safety gates missing: %s", joined)
	}
}

func TestK3sPlanNameIsStableDNSLabel(t *testing.T) {
	name := clusterK3sPlanName("11111111-1111-4111-8111-111111111111", "engine-1")
	if len(name) > 63 || !clusterControllerNodeRegex.MatchString(name) || name != clusterK3sPlanName("11111111-1111-4111-8111-111111111111", "engine-1") {
		t.Fatalf("invalid K3s Plan name %q", name)
	}
}

func TestClusterRetryUsesFreshNodeActionsAndK3sPlans(t *testing.T) {
	operation := clusterControllerOperation{ID: "11111111-1111-4111-8111-111111111111", Attempt: 1}
	firstJob := clusterActionJobName(operation.ID, "attempt:1:node:engine-1:inspect_health")
	firstPlan := clusterK3sPlanName(clusterOperationAttemptKey(operation), "engine-1")
	operation.Attempt = 2
	secondJob := clusterActionJobName(operation.ID, "attempt:2:node:engine-1:inspect_health")
	secondPlan := clusterK3sPlanName(clusterOperationAttemptKey(operation), "engine-1")
	if firstJob == secondJob || firstPlan == secondPlan {
		t.Fatalf("retry reused action resources job=%q plan=%q", secondJob, secondPlan)
	}
}

func TestMemberRemovalHealthRequiresReadyEtcdVoters(t *testing.T) {
	voter := "True"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes/engine-1":
			http.NotFound(w, r)
		case "/api/v1/nodes/engine-2", "/api/v1/nodes/engine-3":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
				map[string]any{"type": "EtcdIsVoter", "status": voter},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	nodes := []clusterControllerNode{{ID: "1", Name: "engine-1"}, {ID: "2", Name: "engine-2"}, {ID: "3", Name: "engine-3"}}
	operation := clusterControllerOperation{Payload: map[string]any{"removal_node_ids": []any{"1"}}}
	healthy, err := runner.memberRemovalStateHealthy(context.Background(), operation, "engine-1", nodes)
	if err != nil || !healthy {
		t.Fatalf("expected remaining voter health, healthy=%v err=%v", healthy, err)
	}
	voter = "False"
	healthy, err = runner.memberRemovalStateHealthy(context.Background(), operation, "engine-1", nodes)
	if err != nil || healthy {
		t.Fatalf("expected non-voter rejection, healthy=%v err=%v", healthy, err)
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
