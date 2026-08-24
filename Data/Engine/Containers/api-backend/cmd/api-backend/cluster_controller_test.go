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
		{step: "node:id:prepare_restore", want: 15 * time.Minute},
	}
	for _, test := range tests {
		if got := clusterControllerStepTimeout(test.step); got != test.want {
			t.Fatalf("clusterControllerStepTimeout(%q)=%s want %s", test.step, got, test.want)
		}
	}
}

func TestClusterControllerStartupAndLivenessStayLocal(t *testing.T) {
	controller := &clusterController{}
	handler := controller.healthServer().Handler
	for _, path := range []string{"/startup", "/live"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d want %d", path, recorder.Code, http.StatusOK)
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

func TestClusterInitJobToleratesBoundedAuthorizationTransition(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 2 {
			http.Error(w, "forbidden during datastore restart", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"succeeded": 1}})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:                          &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:                     "borealis",
		jobPollInterval:               time.Millisecond,
		clusterInitAuthorizationGrace: 50 * time.Millisecond,
	}
	if err := runner.waitClusterInitJob(context.Background(), "cluster-test"); err != nil {
		t.Fatalf("cluster-init authorization transition failed Job wait: %v", err)
	}
	if requests != 3 {
		t.Fatalf("unexpected cluster-init Job poll count %d", requests)
	}
}

func TestClusterInitJobRejectsPersistentAuthorizationFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:                          &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:                     "borealis",
		jobPollInterval:               time.Millisecond,
		clusterInitAuthorizationGrace: 3 * time.Millisecond,
	}
	if err := runner.waitClusterInitJob(context.Background(), "cluster-test"); err == nil || !strings.Contains(err.Error(), "authorization did not recover") {
		t.Fatalf("expected persistent cluster-init authorization failure, got %v", err)
	}
	if requests < 2 {
		t.Fatalf("cluster-init authorization failure was not retried: %d requests", requests)
	}
}

func TestNodeActionJobDoesNotCreateAfterAuthorizationFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected mutating request after authorization failure: %s", r.Method)
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:        &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:   "borealis",
		actionImage: "registry.example/borealis@sha256:" + strings.Repeat("a", 64),
	}
	err := runner.nodeActionJob(
		context.Background(),
		clusterControllerOperation{ID: "11111111-1111-4111-8111-111111111111", Attempt: 1},
		clusterControllerStep{Name: "inspect", NodeID: "22222222-2222-4222-8222-222222222222"},
		clusterControllerNode{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2"},
		"InspectHealth",
	)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected authorization error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("authorization failure produced %d Kubernetes requests", requests)
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
	ordered := []string{"redeploy_revision", "inspect_candidate_health", "minimum_candidate_soak", "promote_candidate", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"}
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

func TestClusterActionJobNormalizesStepLabel(t *testing.T) {
	step := "admit:692f3ce6-038e-43c7-a7f8-9d0d425ce8bf:redeploy:" + strings.Repeat("x", 64)
	manifest := clusterActionJobManifest("cluster-action", "borealis", "engine-2", "registry.example/borealis@sha256:"+strings.Repeat("a", 64), []string{"client", "--verb", "InspectHealth"}, "operation", step)
	template := nestedMap(nestedMap(manifest, "spec"), "template")
	labels := nestedMap(template, "metadata")["labels"].(map[string]string)
	label := labels["borealis.io/operation-step"]
	if len(label) > 63 || !clusterControllerLabelValueRegex.MatchString(label) || strings.Contains(label, ":") {
		t.Fatalf("invalid operation-step label %q", label)
	}
	if label != clusterActionStepLabel(step) {
		t.Fatalf("operation-step label is not stable: %q", label)
	}
}

func TestMembershipAdmissionRunsNodeConformanceBeforeRedeploy(t *testing.T) {
	nodeID := "22222222-2222-4222-8222-222222222222"
	k3sVersion := "v1.36.3+k3s1"
	conformance := clusterAdmissionConformanceAction(nodeID, k3sVersion)
	if conformance.verb != "RunK3sProbeConformance" || conformance.targetRelease != k3sVersion {
		t.Fatalf("membership admission conformance action=%#v", conformance)
	}
	actions := clusterAdmissionWorkloadActions(nodeID)
	if len(actions) != 3 {
		t.Fatalf("membership admission workload actions=%d want 3", len(actions))
	}
	wantVerbs := []string{"RedeployRevision", "InspectCandidateHealth", "PromoteCandidate"}
	for index, want := range wantVerbs {
		if actions[index].verb != want {
			t.Fatalf("membership admission action %d=%s want %s", index, actions[index].verb, want)
		}
	}
	if !actions[1].soakAfter || actions[0].soakAfter || actions[2].soakAfter {
		t.Fatalf("membership admission candidate soak is not isolated: %#v", actions)
	}
	for _, action := range actions {
		if action.targetRelease != "" {
			t.Fatalf("membership admission K3s target leaked into workloads: %#v", actions)
		}
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
	positions := nodeStepPositions(steps, nodes[1].ID)
	assertStepOrder(t, positions, []string{"post_k3s_conformance", "prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
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

func TestHMRExitRestoresStandbyBeforeClearingDrain(t *testing.T) {
	targetID := "11111111-1111-4111-8111-111111111111"
	standbyID := "22222222-2222-4222-8222-222222222222"
	nodes := []clusterControllerNode{{ID: targetID, Name: "engine-1"}, {ID: standbyID, Name: "engine-2"}}
	steps, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_exit", TargetNodeID: targetID}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	positions := nodeStepPositions(steps, standbyID)
	assertStepOrder(t, positions, []string{"prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
	allPositions := map[string]int{}
	for index, step := range steps {
		allPositions[step.Name] = index
	}
	targetPositions := nodeStepPositions(steps, targetID)
	assertStepOrder(t, targetPositions, []string{"enter_drain", "wait_endpoint_withdrawal", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
	if positions["exit_drain"] >= allPositions["hmr_move_roles_to_standby"] ||
		allPositions["hmr_move_roles_to_standby"] >= targetPositions["enter_drain"] ||
		targetPositions["wait_endpoint_withdrawal"] >= allPositions["hmr_restore_pinned_release"] ||
		allPositions["hmr_restore_pinned_release"] >= allPositions["hmr_inspect_candidate"] ||
		allPositions["hmr_inspect_candidate"] >= allPositions["hmr_candidate_soak"] ||
		allPositions["hmr_candidate_soak"] >= allPositions["hmr_promote_candidate"] ||
		allPositions["hmr_promote_candidate"] >= allPositions["hmr_verify_production"] ||
		allPositions["hmr_verify_production"] >= targetPositions["restore_roles"] {
		t.Fatalf("unsafe HMR production restore order: %#v", allPositions)
	}
}

func TestHMRPinnedRestoreUsesSavedImmutableRelease(t *testing.T) {
	sha := strings.Repeat("a", 40)
	operation := clusterControllerOperation{Payload: map[string]any{"baseline_release": "2026.08.24", "baseline_sha": sha}}
	restore, err := hmrPinnedRestoreOperation(operation, clusterControllerNode{})
	if err != nil || restore.TargetRelease != "2026.08.24" || restore.TargetSHA != sha {
		t.Fatalf("saved release not restored: %#v err=%v", restore, err)
	}
	if _, err := hmrPinnedRestoreOperation(clusterControllerOperation{}, clusterControllerNode{}); err == nil {
		t.Fatal("missing pinned production release accepted")
	}
}

func TestSingleNodeHMRExitFencesHostPortsBeforeCandidate(t *testing.T) {
	targetID := "11111111-1111-4111-8111-111111111111"
	steps, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_exit", TargetNodeID: targetID}, []clusterControllerNode{{ID: targetID, Name: "engine-1"}})
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for index, step := range steps {
		positions[step.Name] = index
	}
	for _, required := range []string{"hmr_fence_target_roles", "node:" + targetID + ":enter_drain", "node:" + targetID + ":wait_endpoint_withdrawal", "hmr_restore_pinned_release"} {
		if _, exists := positions[required]; !exists {
			t.Fatalf("single-node HMR restore step %q missing: %#v", required, positions)
		}
	}
	if positions["hmr_fence_target_roles"] >= positions["node:"+targetID+":enter_drain"] ||
		positions["node:"+targetID+":wait_endpoint_withdrawal"] >= positions["hmr_restore_pinned_release"] {
		t.Fatalf("single-node HMR restore did not fence active host ports: %#v", positions)
	}
}

func TestMaintenanceExitProvesHealthBeforeClearingDrain(t *testing.T) {
	nodeID := "11111111-1111-4111-8111-111111111111"
	nodes := []clusterControllerNode{{ID: nodeID, Name: "engine-1"}}
	operation := clusterControllerOperation{Kind: "node_maintenance", TargetNodeID: nodeID, Payload: map[string]any{"action": "exit"}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	positions := nodeStepPositions(steps, nodeID)
	assertStepOrder(t, positions, []string{"prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
}

func nodeStepPositions(steps []clusterControllerStep, nodeID string) map[string]int {
	positions := map[string]int{}
	prefix := "node:" + nodeID + ":"
	for index, step := range steps {
		if strings.HasPrefix(step.Name, prefix) {
			positions[strings.TrimPrefix(step.Name, prefix)] = index
		}
	}
	return positions
}

func assertStepOrder(t *testing.T, positions map[string]int, ordered []string) {
	t.Helper()
	for index, name := range ordered {
		position, exists := positions[name]
		if !exists {
			t.Fatalf("restore step %q missing: %#v", name, positions)
		}
		if index > 0 && positions[ordered[index-1]] >= position {
			t.Fatalf("unsafe restore order: %#v", positions)
		}
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
